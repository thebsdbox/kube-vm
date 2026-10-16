//go:build linux && amd64

package vmm_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"path/filepath"
	"testing"
	"time"

	"kube-vm/kvm"
	"kube-vm/vmm"
)

type checkpointLoopLoader struct{}

func (checkpointLoopLoader) LoadMemory(_ vmm.VMInfo, mem []byte) error {
	// real-mode: inc dword [0x2000]; jmp back. RAX remains a sentinel.
	copy(mem[0x1000:], []byte{0x66, 0xff, 0x06, 0x00, 0x20, 0xeb, 0xf9})
	return nil
}

func (checkpointLoopLoader) LoadVCPU(_ vmm.VMInfo, _ int, regs *kvm.Regs, sregs *kvm.Sregs) error {
	*regs = kvm.Regs{RIP: 0x1000, RFlags: 2, RAX: 0x1234, RSP: 0x4000}
	sregs.CS = kvm.Segment{Limit: 0xffff, Type: 11, Present: 1, S: 1}
	sregs.DS = kvm.Segment{Limit: 0xffff, Type: 3, Present: 1, S: 1}
	sregs.SS = sregs.DS
	sregs.ES = sregs.DS
	sregs.CR0 &^= 1
	return nil
}

func runCheckpointVM(t *testing.T, vm *vmm.VM) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- vm.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = vm.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("checkpoint test VM failed to exit")
		}
	})
}

func TestSnapshotGuestContinuation(t *testing.T) {
	if isGuest { t.Skip("host-only KVM continuation test") }
	requireKVM(t)
	source, err := vmm.New(vmm.Config{MemSize: vmm.MemSizeMin, NumCPU: 1, Loader: checkpointLoopLoader{}})
	if err != nil { t.Fatal(err) }
	runCheckpointVM(t, source)
	path := filepath.Join(t.TempDir(), "source.snap")
	var saved *vmm.VMSnapshot
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := source.SaveState(path); err != nil { t.Fatalf("save running guest: %v", err) }
		saved, err = vmm.ReadSnapshot(path)
		if err != nil { t.Fatal(err) }
		if binary.LittleEndian.Uint32(saved.Mem[0x2000:]) != 0 { break }
		if time.Now().After(deadline) { t.Fatal("source guest never executed") }
	}
	if saved.CPU[0].Hardware.Regs.RAX != 0x1234 { t.Fatal("source CPU sentinel changed") }
	restored, err := vmm.New(vmm.Config{
		MemSize: saved.Config.MemSize, NumCPU: saved.Config.NumCPU,
		Snapshot: saved, SnapshotConfig: saved.Config,
	})
	if err != nil { t.Fatalf("fresh checkpoint boot: %v", err) }
	// Capture before running: state must be identical, not a reboot at the loader.
	copyPath := filepath.Join(t.TempDir(), "restored.snap")
	if err := restored.SaveState(copyPath); err != nil { _ = restored.Close(); t.Fatal(err) }
	copyState, err := vmm.ReadSnapshot(copyPath)
	if err != nil { _ = restored.Close(); t.Fatal(err) }
	if !bytes.Equal(saved.Mem, copyState.Mem) || saved.CPU[0].Hardware.Regs != copyState.CPU[0].Hardware.Regs {
		_ = restored.Close()
		t.Fatal("RAM/registers were not preserved by fresh checkpoint boot")
	}
	runCheckpointVM(t, restored)
	deadline = time.Now().Add(5 * time.Second)
	for {
		if err := restored.SaveState(copyPath); err != nil { t.Fatalf("save resumed guest: %v", err) }
		next, err := vmm.ReadSnapshot(copyPath)
		if err != nil { t.Fatal(err) }
		if binary.LittleEndian.Uint32(next.Mem[0x2000:]) != binary.LittleEndian.Uint32(saved.Mem[0x2000:]) {
			if next.CPU[0].Hardware.Regs.RAX != 0x1234 { t.Fatal("resumed CPU sentinel changed") }
			break
		}
		if time.Now().After(deadline) { t.Fatal("restored guest did not resume execution") }
	}
}