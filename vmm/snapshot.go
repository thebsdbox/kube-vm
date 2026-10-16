//go:build linux && amd64

package vmm

import (
	"compress/gzip"
	"encoding/gob"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"kube-vm/kvm"
	"kube-vm/virtio"
	"kube-vm/vmm/arch"
)

const SnapshotVersion = 3

func (m *VM) memoryAt(addr uint64, size int) ([]byte, error) {
	if size < 0 {
		return nil, errors.New("negative guest memory size")
	}
	if len(m.mem) > arch.MMIOHoleAddr {
		if addr >= arch.AfterMMIOHoleAddr {
			addr -= arch.AfterMMIOHoleAddr - arch.MMIOHoleAddr
		} else if addr >= arch.MMIOHoleAddr || uint64(size) > arch.MMIOHoleAddr-addr {
			return nil, errors.New("guest buffer overlaps MMIO hole")
		}
	}
	if addr > uint64(len(m.mem)) || uint64(size) > uint64(len(m.mem))-addr {
		return nil, errors.New("guest buffer exceeds RAM")
	}
	return m.mem[int(addr):int(addr)+size], nil
}

// SerialState contains UART registers, not host descriptors or connections.
type SerialState struct {
	IER, LCR, MCR, SCR, DLL, DLM byte
}

// ValidateRestore deliberately rejects older partial snapshots. They can still
// be inspected, but lack enough state for a reliable continuation of execution.
func (s *VMSnapshot) ValidateRestore() error {
	if s == nil || s.Magic != snapshotMagic {
		return errors.New("invalid VM snapshot")
	}
	if s.Version != SnapshotVersion {
		return fmt.Errorf("snapshot version %d cannot be resumed safely; create a new version %d checkpoint (old snapshots omit CPU/device/disk state)", s.Version, SnapshotVersion)
	}
	if s.Architecture != runtime.GOARCH {
		return fmt.Errorf("snapshot architecture %q does not match %q", s.Architecture, runtime.GOARCH)
	}
	if len(s.Mem) != s.Config.MemSize || len(s.Mem) < MemSizeMin || len(s.Mem)%os.Getpagesize() != 0 {
		return errors.New("snapshot memory size is invalid or disagrees with launch metadata")
	}
	if len(s.CPU) < 1 || len(s.CPU) != s.Config.NumCPU {
		return errors.New("snapshot CPU count disagrees with launch metadata")
	}
	if s.Hardware == nil {
		return errors.New("snapshot is missing interrupt-controller/timer state")
	}
	for i, cpu := range s.CPU {
		if cpu.Hardware == nil {
			return fmt.Errorf("snapshot is missing extended CPU state for vCPU %d", i)
		}
	}
	if s.Config.VSOCKPort != 0 || s.Config.VhostNet {
		return errors.New("snapshots with vhost-net or live VSock devices are not supported")
	}
	if s.Config.SerialConsole != (s.Serial != nil) {
		return errors.New("snapshot serial console state disagrees with launch metadata")
	}
	wantDisks := len(s.Config.BlockSpecs)
	if s.Config.ISOPath != "" { wantDisks++ }
	deviceDisks := 0
	for _, device := range s.DeviceState {
		if device.SnapshotVersion != 1 {
			return errors.New("snapshot device state is missing packed-queue cursor metadata")
		}
		for n, queue := range device.Queue {
			if queue.Ready == 0 { continue }
			cursor, ok := device.QueueCursors[n]
			if !ok || queue.Ready != 1 || queue.NumDesc == 0 || queue.NumDesc > 1<<15 ||
				uint32(cursor.AvailIndex) >= queue.NumDesc || uint32(cursor.UsedIndex) >= queue.NumDesc ||
				cursor.AvailIndex != cursor.UsedIndex || cursor.AvailWrap != cursor.UsedWrap {
				return fmt.Errorf("snapshot device %v queue %d has incomplete or invalid packed cursor state", device.Type, n)
			}
		}
		if device.Type == virtio.BlockDeviceID { deviceDisks++ }
	}
	if len(s.Storage) != wantDisks || deviceDisks != wantDisks {
		return errors.New("snapshot disk assets, devices, and launch metadata disagree")
	}
	return nil
}

// ValidateHost performs a non-destructive CPU compatibility preflight.
func (s *VMSnapshot) ValidateHost() error {
	if err := s.ValidateRestore(); err != nil {
		return err
	}
	for i, cpu := range s.CPU {
		if err := kvm.ValidateCPUHost(cpu.Hardware); err != nil {
			return fmt.Errorf("snapshot CPU %d is incompatible with this host: %w", i, err)
		}
	}
	return nil
}

func (m *VM) captureSnapshot(dir string) (state *VMSnapshot, err error) {
	if isShutdown(m.doneC) {
		return nil, ErrVMClosed
	}
	if m.mmio != nil {
		if err := m.mmio.ValidateSnapshotSupport(); err != nil {
			return nil, err
		}
	}
	m.pauseMu.Lock()
	wasPaused := m.paused
	m.pauseMu.Unlock()
	if err := m.pause(); err != nil {
		return nil, err
	}
	if !wasPaused {
		defer func() {
			if resumeErr := m.resume(); err == nil {
				err = resumeErr
			}
		}()
	}
	if m.mmio != nil {
		release := m.mmio.Quiesce()
		defer release()
	}
	m.pauseMu.Lock()
	exited := m.runExited
	m.pauseMu.Unlock()
	if exited {
		return nil, errors.New("cannot checkpoint a VM whose run loop has exited")
	}
	state = &VMSnapshot{
		Magic: snapshotMagic, Version: SnapshotVersion, Architecture: runtime.GOARCH,
		CreatedAt: time.Now().UTC(),
		Config: m.snapshotConfig.withVMDefaults(len(m.mem), len(m.cpu)),
		CPU: make([]VMSnapshotCPU, len(m.cpu)), StorageDir: filepath.Base(dir),
	}
	if state.Config.VhostNet || state.Config.VSOCKPort != 0 {
		return nil, errors.New("vhost-net and VSock snapshots are not supported")
	}
	state.Hardware, err = kvm.CaptureVMState(m.fd)
	if err != nil {
		return nil, fmt.Errorf("save VM hardware: %w", err)
	}
	for i, c := range m.cpu {
		if c == nil || c.fd == nil {
			return nil, fmt.Errorf("save CPU %d: vCPU is unavailable", i)
		}
		hardware, err := kvm.CaptureCPUState(c.fd)
		if err != nil {
			return nil, fmt.Errorf("save CPU %d: %w", i, err)
		}
		state.CPU[i] = VMSnapshotCPU{Regs: hardware.Regs, Sregs: hardware.Sregs, FPU: hardware.FPU, MP: hardware.MP, Hardware: hardware}
	}
	if m.mmio != nil {
		state.Storage, err = m.mmio.SnapshotStorage(dir)
		if err != nil {
			return nil, fmt.Errorf("save checkpoint disks: %w", err)
		}
		state.DeviceState = m.mmio.ExportDeviceState()
	}
	state.Mem = append([]byte(nil), m.mem...)
	if m.serial != nil {
		m.serial.mu.Lock()
		d := m.serial
		state.Serial = &SerialState{d.ier, d.lcr, d.mcr, d.scr, d.dll, d.dlm}
		m.serial.mu.Unlock()
	}
	m.pciConfigMu.Lock()
	state.PCIConfigAddr = m.pciConfigAddr
	m.pciConfigMu.Unlock()
	return state, state.ValidateRestore()
}

func (m *VM) saveSnapshot(path string) error {
	if path == "" {
		return errors.New("snapshot path is required")
	}
	dir, err := os.MkdirTemp(filepath.Dir(path), filepath.Base(path)+".storage-")
	if err != nil {
		return fmt.Errorf("create snapshot storage directory: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(dir)
		}
	}()
	state, err := m.captureSnapshot(dir)
	if err != nil {
		return err
	}
	if err := syncDirectory(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	gz := gzip.NewWriter(tmp)
	if err := gob.NewEncoder(gz).Encode(state); err != nil {
		_ = gz.Close()
		return fmt.Errorf("encode snapshot: %w", err)
	}
	if err := gz.Close(); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	committed = true // Never remove assets after publishing their manifest.
	return syncDirectory(filepath.Dir(path))
}

func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func (m *VM) restoreSnapshot(state *VMSnapshot) error {
	if err := state.ValidateRestore(); err != nil {
		return err
	}
	m.pauseMu.Lock()
	running := m.running || m.runExited
	m.pauseMu.Unlock()
	if running {
		return errors.New("cannot overwrite a running VM: restore by starting a fresh VM from the checkpoint")
	}
	if len(state.Mem) != len(m.mem) || len(state.CPU) != len(m.cpu) {
		return errors.New("snapshot RAM/vCPU topology does not match the destination VM")
	}
	if (m.serial != nil) != (state.Serial != nil) {
		return errors.New("snapshot serial device does not match the destination VM")
	}
	if m.mmio == nil && len(state.DeviceState) != 0 {
		return errors.New("snapshot devices are missing from destination VM")
	}
	if m.mmio != nil {
		release := m.mmio.Quiesce()
		defer release()
	}
	copy(m.mem, state.Mem)
	if err := kvm.RestoreVMState(m.fd, state.Hardware); err != nil {
		return fmt.Errorf("restore VM hardware: %w", err)
	}
	for i, cpu := range state.CPU {
		if m.cpu[i] == nil || m.cpu[i].fd == nil {
			return fmt.Errorf("restore CPU %d: vCPU is unavailable", i)
		}
		if err := kvm.RestoreCPUState(m.cpu[i].fd, cpu.Hardware); err != nil {
			return fmt.Errorf("restore CPU %d: %w", i, err)
		}
	}
	if m.mmio != nil {
		if err := m.mmio.RestoreDeviceState(state.DeviceState); err != nil {
			return fmt.Errorf("restore device queues: %w", err)
		}
	}
	if state.Serial != nil {
		s := state.Serial
		m.serial.mu.Lock()
		m.serial.ier, m.serial.lcr, m.serial.mcr = s.IER, s.LCR, s.MCR
		m.serial.scr, m.serial.dll, m.serial.dlm = s.SCR, s.DLL, s.DLM
		m.serial.mu.Unlock()
	}
	m.pciConfigMu.Lock()
	m.pciConfigAddr = state.PCIConfigAddr
	m.pciConfigMu.Unlock()
	return m.resume()
}