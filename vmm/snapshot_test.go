//go:build linux && amd64

package vmm

import (
	"bytes"
	"compress/gzip"
	"encoding/gob"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kube-vm/kvm"
)

func checkpointFixture() *VMSnapshot {
	return &VMSnapshot{
		Magic: snapshotMagic, Version: SnapshotVersion, Architecture: "amd64",
		Config: SnapshotConfig{MemSize: MemSizeMin, NumCPU: 1},
		Mem: make([]byte, MemSizeMin),
		CPU: []VMSnapshotCPU{{Hardware: &kvm.CPUState{Regs: kvm.Regs{RAX: 42}}}},
		Hardware: &kvm.VMState{},
	}
}

func TestCheckpointValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		mutate func(*VMSnapshot)
	}{
		{"legacy", func(s *VMSnapshot) { s.Version = 2 }},
		{"future", func(s *VMSnapshot) { s.Version++ }},
		{"architecture", func(s *VMSnapshot) { s.Architecture = "arm64" }},
		{"memory metadata", func(s *VMSnapshot) { s.Config.MemSize++ }},
		{"CPU metadata", func(s *VMSnapshot) { s.Config.NumCPU++ }},
		{"kernel state", func(s *VMSnapshot) { s.Hardware = nil }},
		{"extended CPU state", func(s *VMSnapshot) { s.CPU[0].Hardware = nil }},
		{"UART state", func(s *VMSnapshot) { s.Config.SerialConsole = true }},
		{"VSock", func(s *VMSnapshot) { s.Config.VSOCKPort = 1234 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := checkpointFixture()
			if err := s.ValidateRestore(); err != nil { t.Fatal(err) }
			tc.mutate(s)
			if err := s.ValidateRestore(); err == nil { t.Fatal("accepted incomplete/incompatible checkpoint") }
		})
	}
}

func TestRestoreRunningVMRejectedBeforeMemoryChange(t *testing.T) {
	s := checkpointFixture()
	m := &VM{running: true, mem: bytes.Repeat([]byte{0x55}, MemSizeMin), cpu: []*vcpu{{}}}
	if err := m.restoreSnapshot(s); err == nil || !strings.Contains(err.Error(), "running VM") {
		t.Fatalf("running restore: %v", err)
	}
	if !bytes.Equal(m.mem, bytes.Repeat([]byte{0x55}, MemSizeMin)) { t.Fatal("live RAM was overwritten") }
}

func TestRestoreTopologyCheckedBeforeMemoryChange(t *testing.T) {
	m := &VM{mem: bytes.Repeat([]byte{0x55}, MemSizeMin), cpu: []*vcpu{{}, {}}}
	if err := m.restoreSnapshot(checkpointFixture()); err == nil { t.Fatal("accepted CPU topology mismatch") }
	if m.mem[0] != 0x55 { t.Fatal("RAM changed before topology validation") }
}

func TestFailedSnapshotDoesNotReplaceExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "saved.snap")
	if err := os.WriteFile(path, []byte("previous checkpoint"), 0o600); err != nil { t.Fatal(err) }
	m := &VM{mem: make([]byte, MemSizeMin)} // No KVM fd: capture must fail safely.
	if err := m.SaveState(path); err == nil { t.Fatal("expected unavailable hardware error") }
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "previous checkpoint" { t.Fatalf("previous checkpoint overwritten: %q, %v", data, err) }
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 { t.Fatalf("failed save left assets: %v, %v", entries, err) }
	if m.paused { t.Fatal("failed save left previously running VM paused") }
}

func TestCheckpointExtendedStateRoundTripAndGzipIntegrity(t *testing.T) {
	s := checkpointFixture()
	s.CPU[0].Hardware.XSAVE = bytes.Repeat([]byte{0x12}, 4096)
	s.CPU[0].Hardware.MSRs = []kvm.MSREntry{{Index: 0x174, Data: 7}}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if err := gob.NewEncoder(gz).Encode(s); err != nil { t.Fatal(err) }
	if err := gz.Close(); err != nil { t.Fatal(err) }
	out, err := decodeSnapshot(bytes.NewReader(buf.Bytes()))
	if err != nil { t.Fatal(err) }
	if err := out.ValidateRestore(); err != nil { t.Fatal(err) }
	if out.CPU[0].Hardware.Regs.RAX != 42 || out.CPU[0].Hardware.MSRs[0].Data != 7 || !bytes.Equal(out.CPU[0].Hardware.XSAVE, s.CPU[0].Hardware.XSAVE) {
		t.Fatal("extended CPU state did not round-trip")
	}
	broken := append([]byte(nil), buf.Bytes()...)
	broken[len(broken)-8] ^= 0xff
	if _, err := decodeSnapshot(bytes.NewReader(broken)); err == nil { t.Fatal("accepted corrupt gzip trailer") }
	if _, err := decodeSnapshot(bytes.NewReader(buf.Bytes()[:len(buf.Bytes())-4])); err == nil { t.Fatal("accepted truncated gzip trailer") }
}

func TestSnapshotMemoryAccessRejectsInvalidBuffers(t *testing.T) {
	m := &VM{mem: make([]byte, 4096)}
	for _, tc := range []struct { addr uint64; size int }{{4090, 16}, {^uint64(0), 2}, {0, -1}} {
		if _, err := m.memoryAt(tc.addr, tc.size); err == nil { t.Fatalf("accepted invalid RAM range %+v", tc) }
	}
	if b, err := m.memoryAt(4092, 4); err != nil || len(b) != 4 { t.Fatalf("valid RAM range: %v", err) }
}