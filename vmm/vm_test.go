//go:build linux

package vmm_test

import (
	"bytes"
	"compress/gzip"
	"encoding/gob"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kube-vm/kvm"
	"kube-vm/vmm"
)

func TestValidateMemSize(t *testing.T) {
	requireKVM(t) // New probes KVM before validating config.
	badSizes := []int{
		os.Getpagesize() - 1,
		os.Getpagesize() + 1,
		vmm.MemSizeMin - os.Getpagesize(),
		vmm.MemSizeMax + os.Getpagesize(),
	}

	for _, sz := range badSizes {
		_, err := vmm.New(vmm.Config{
			Loader:  &nopLoader{},
			MemSize: sz,
		})

		if !errors.Is(err, vmm.ErrConfig) {
			t.Errorf("MemSize %d: error isn't ErrConfig: %v", sz, err)
		}
	}
}

func TestValidateMissingLoader(t *testing.T) {
	requireKVM(t)
	_, err := vmm.New(vmm.Config{})

	if !errors.Is(err, vmm.ErrConfig) {
		t.Errorf("error isn't ErrConfig: %v", err)
	}
}

func TestValidateNumCPU(t *testing.T) {
	requireKVM(t)
	_, err := vmm.New(vmm.Config{
		Loader: nopLoader{},
		NumCPU: -1,
	})

	if !errors.Is(err, vmm.ErrConfig) {
		t.Errorf("error isn't ErrConfig: %v", err)
	}
}

func TestSetupVMError(t *testing.T) {
	requireKVM(t)
	boom := errors.New("boom")
	m, err := vmm.New(vmm.Config{
		Loader: nopLoader{},
		Arch: nopArch{
			SetupVMError: boom,
		},
	})

	if m != nil {
		t.Fatalf("vm is present: %v", m)
	}

	if !errors.Is(err, vmm.ErrSetup) {
		t.Errorf("error isn't ErrSetup: %v", err)
	}

	if !errors.Is(err, boom) {
		t.Errorf("no boom: %v", err)
	}
}

func TestSetupMemoryError(t *testing.T) {
	requireKVM(t)
	boom := errors.New("boom")
	m, err := vmm.New(vmm.Config{
		Loader: nopLoader{},
		Arch: nopArch{
			SetupMemoryError: boom,
		},
	})

	if m != nil {
		t.Fatalf("vm is present: %v", m)
	}

	if !errors.Is(err, vmm.ErrSetupMemory) {
		t.Errorf("error isn't ErrSetupMemory: %v", err)
	}

	if !errors.Is(err, boom) {
		t.Errorf("no boom: %v", err)
	}
}

func TestSetupVCPUError(t *testing.T) {
	requireKVM(t)
	boom := errors.New("boom")
	m, err := vmm.New(vmm.Config{
		Loader: nopLoader{},
		Arch: nopArch{
			SetupVCPUError: boom,
		},
	})

	if m != nil {
		t.Fatalf("vm is present: %v", m)
	}

	if !errors.Is(err, vmm.ErrSetupVCPU) {
		t.Errorf("error isn't ErrSetupVCPU: %v", err)
	}

	if !errors.Is(err, boom) {
		t.Errorf("no boom: %v", err)
	}
}

func TestVMSnapshotRoundTrip(t *testing.T) {
	in := vmm.VMSnapshot{
		Magic:   "kube-vm-snapshot-v1",
		Version: 2,
		Config: vmm.SnapshotConfig{
			MemSize:    4,
			NumCPU:     1,
			KernelPath: "bzImage",
			BlockSpecs: []string{"/tmp/disk.img:ro"},
		},
		Mem: []byte{0x10, 0x20, 0x30, 0x40},
		CPU: []vmm.VMSnapshotCPU{{
			Regs:  kvm.Regs{RAX: 42, RBX: 99},
			Sregs: kvm.Sregs{CS: kvm.Segment{Selector: 0x33}},
			FPU:   kvm.FPU{FCW: 0x1234},
			MP:    kvm.MPState{State: kvm.MPStateRunnable},
		}},
	}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(in); err != nil {
		t.Fatalf("encode snapshot: %v", err)
	}

	var out vmm.VMSnapshot
	if err := gob.NewDecoder(bytes.NewReader(buf.Bytes())).Decode(&out); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}

	if out.Magic != in.Magic {
		t.Fatalf("magic mismatch: got %q want %q", out.Magic, in.Magic)
	}
	if !bytes.Equal(out.Mem, in.Mem) {
		t.Fatalf("memory mismatch: got %x want %x", out.Mem, in.Mem)
	}
	if len(out.CPU) != 1 || out.CPU[0].Regs.RAX != in.CPU[0].Regs.RAX || out.CPU[0].Regs.RBX != in.CPU[0].Regs.RBX {
		t.Fatalf("cpu state mismatch: %#v", out.CPU)
	}
	if out.Config.KernelPath != in.Config.KernelPath || len(out.Config.BlockSpecs) != 1 || out.Config.BlockSpecs[0] != in.Config.BlockSpecs[0] {
		t.Fatalf("config mismatch: %#v", out.Config)
	}
}

func TestReadSnapshotSupportsCompressedMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vm.snap")

	in := vmm.VMSnapshot{
		Magic:     "kube-vm-snapshot-v1",
		Version:   2,
		CreatedAt: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC),
		Config: vmm.SnapshotConfig{
			MemSize:      4096,
			NumCPU:       2,
			KernelPath:   "bzImage",
			InitrdPath:   "initrd.img",
			FirmwarePath: "firmware.bin",
			ISOPath:      "installer.iso",
			Cmdline:      "console=hvc0",
			TapName:      "tap0",
			NATMode:      true,
			VhostNet:     true,
			BlockSpecs:   []string{"disk0.img", "disk1.img:ro"},
		},
		Mem: []byte{1, 2, 3, 4},
		CPU: []vmm.VMSnapshotCPU{{}, {}},
	}

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	gz := gzip.NewWriter(f)
	if err := gob.NewEncoder(gz).Encode(in); err != nil {
		t.Fatalf("encode snapshot: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}

	out, err := vmm.ReadSnapshot(path)
	if err != nil {
		t.Fatalf("ReadSnapshot returned error: %v", err)
	}
	if out.Version != 2 {
		t.Fatalf("version mismatch: got %d want 2", out.Version)
	}
	if out.Config.KernelPath != in.Config.KernelPath || len(out.Config.BlockSpecs) != len(in.Config.BlockSpecs) {
		t.Fatalf("config mismatch: %#v", out.Config)
	}
}

func TestReadSnapshotSupportsLegacyUncompressedFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.snap")

	in := vmm.VMSnapshot{
		Magic: "kube-vm-snapshot-v1",
		Mem:   []byte{0xaa, 0xbb},
		CPU:   []vmm.VMSnapshotCPU{{}},
	}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(in); err != nil {
		t.Fatalf("encode snapshot: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	out, err := vmm.ReadSnapshot(path)
	if err != nil {
		t.Fatalf("ReadSnapshot returned error: %v", err)
	}
	if out.Version != 1 {
		t.Fatalf("version mismatch: got %d want 1", out.Version)
	}
	if out.Config.MemSize != len(in.Mem) || out.Config.NumCPU != len(in.CPU) {
		t.Fatalf("legacy snapshot defaults mismatch: %#v", out.Config)
	}
}

func TestLoadMemoryError(t *testing.T) {
	requireKVM(t)
	boom := errors.New("boom")
	_, err := vmm.New(vmm.Config{
		Loader: &nopLoader{
			LoadMemoryError: boom,
		},
	})

	if err == nil {
		t.Fatal("expected error")
	}

	if !errors.Is(err, vmm.ErrLoadMemory) {
		t.Errorf("error isn't ErrLoadMemory: %v", err)
	}

	if !errors.Is(err, boom) {
		t.Error("no boom")
	}
}

func TestLoadVCPUError(t *testing.T) {
	requireKVM(t)
	boom := errors.New("boom")
	_, err := vmm.New(vmm.Config{
		Loader: &nopLoader{
			LoadVCPUError: boom,
		},
	})

	if err == nil {
		t.Fatal("expected error")
	}

	if !errors.Is(err, vmm.ErrLoadVCPU) {
		t.Errorf("error isn't ErrLoadVCPU: %v", err)
	}

	if !errors.Is(err, boom) {
		t.Error("no boom")
	}
}

type nopLoader struct {
	LoadMemoryError error
	LoadVCPUError   error
}

func (l nopLoader) LoadMemory(info vmm.VMInfo, mem []byte) error {
	return l.LoadMemoryError
}

func (l nopLoader) LoadVCPU(info vmm.VMInfo, slot int, regs *kvm.Regs, sregs *kvm.Sregs) error {
	return l.LoadVCPUError
}

type nopArch struct {
	SetupVMError     error
	SetupMemoryError error
	SetupVCPUError   error
}

func (a nopArch) SetupVM(vm *kvm.VM) error {
	return a.SetupVMError
}

func (a nopArch) SetupMemory(mem []byte) ([]kvm.UserspaceMemoryRegion, error) {
	return nil, a.SetupMemoryError
}

func (a nopArch) SetupVCPU(slot int, vcpu *kvm.VCPU, state *kvm.VCPUState) error {
	return a.SetupVCPUError
}
