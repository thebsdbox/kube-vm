//go:build linux

package vmm

import (
	"bytes"
	"encoding/gob"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/sys/unix"
)

func TestResumeClearsImmediateExit(t *testing.T) {
	m := &VM{cpu: []*vcpu{{mm: make([]byte, 4096)}}}
	m.requestExitLocked()
	if got := m.cpu[0].State().ImmediateExit; got != 1 {
		t.Fatalf("pause request did not set ImmediateExit: got %d want 1", got)
	}

	m.clearImmediateExitLocked()
	if got := m.cpu[0].State().ImmediateExit; got != 0 {
		t.Fatalf("resume did not clear ImmediateExit: got %d want 0", got)
	}
}

func TestRestoreRejectsLegacyStateWithoutChangingVM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "guest.snap")
	payload := &VMSnapshot{
		Magic: "kube-vm-snapshot-v1",
		Mem:   make([]byte, 4096),
		CPU:   []VMSnapshotCPU{{}},
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(payload); err != nil {
		t.Fatalf("encode snapshot: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	m := &VM{
		mem: bytes.Repeat([]byte{0x5a}, 4096),
		cpu: []*vcpu{{mm: make([]byte, 4096)}},
	}
	m.pauseCond = sync.NewCond(&m.pauseMu)
	if err := m.RestoreState(path); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("RestoreState should reject incomplete legacy checkpoints: %v", err)
	}
	if !bytes.Equal(m.mem, bytes.Repeat([]byte{0x5a}, 4096)) {
		t.Fatal("rejected checkpoint modified guest RAM")
	}
	if m.paused {
		t.Fatal("restore left VM paused")
	}
	if got := m.cpu[0].State().ImmediateExit; got != 0 {
		t.Fatalf("restore left ImmediateExit set: got %d want 0", got)
	}
}

func TestRequestExitLockedDoesNotRaceWithVCpuStateReset(t *testing.T) {
	m := &VM{cpu: []*vcpu{{mm: make([]byte, 4096)}}}
	c := m.cpu[0]
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 1000; i++ {
			m.mu.Lock()
			m.requestExitLocked()
			m.mu.Unlock()
		}
	}()

	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 1000; i++ {
			c.stateMu.Lock()
			c.mm = make([]byte, 4096)
			c.mm = nil
			c.stateMu.Unlock()
		}
	}()

	close(start)
	wg.Wait()
}

func TestPauseWaitsForAllVCPUsToAcknowledge(t *testing.T) {
	m := &VM{running: true, cpu: []*vcpu{{mm: make([]byte, 4096)}, {mm: make([]byte, 4096)}}}
	m.pauseCond = sync.NewCond(&m.pauseMu)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := m.Pause(); err != nil {
			t.Errorf("Pause returned error: %v", err)
		}
	}()

	m.pauseMu.Lock()
	for !m.paused {
		m.pauseMu.Unlock()
		runtime.Gosched()
		m.pauseMu.Lock()
	}
	m.pauseMu.Unlock()

	select {
	case <-done:
		t.Fatal("Pause returned before any VCPU acknowledged pause")
	default:
	}

	ack := func() {
		m.pauseMu.Lock()
		m.pausedCPU++
		m.pauseCond.Broadcast()
		m.pauseMu.Unlock()
	}
	ack()

	select {
	case <-done:
		t.Fatal("Pause returned before all VCPUs acknowledged pause")
	default:
	}

	ack()
	<-done

	m.pauseMu.Lock()
	if !m.paused {
		m.pauseMu.Unlock()
		t.Fatal("Pause did not leave VM paused")
	}
	m.paused = false
	m.pausedCPU = 0
	m.pauseCond.Broadcast()
	m.pauseMu.Unlock()
}

func TestRequestExitLockedSignalsRunningVCPUs(t *testing.T) {
	oldTgkill := tgkill
	defer func() { tgkill = oldTgkill }()

	var (
		mu   sync.Mutex
		tids []int
	)
	tgkill = func(pid, tid int, sig unix.Signal) error {
		mu.Lock()
		defer mu.Unlock()
		tids = append(tids, tid)
		return nil
	}

	m := &VM{cpu: []*vcpu{{mm: make([]byte, 4096)}, {mm: make([]byte, 4096)}}}
	atomic.StoreInt32(&m.cpu[0].tid, 101)
	atomic.StoreInt32(&m.cpu[1].tid, 202)

	m.mu.Lock()
	m.requestExitLocked()
	m.mu.Unlock()

	if got := m.cpu[0].State().ImmediateExit; got != 1 {
		t.Fatalf("cpu0 immediate_exit = %d, want 1", got)
	}
	if got := m.cpu[1].State().ImmediateExit; got != 1 {
		t.Fatalf("cpu1 immediate_exit = %d, want 1", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(tids) != 2 {
		t.Fatalf("tgkill call count = %d, want 2", len(tids))
	}
	seen := map[int]bool{}
	for _, tid := range tids {
		seen[tid] = true
	}
	if !seen[101] || !seen[202] {
		t.Fatalf("signaled tids = %v, want 101 and 202", tids)
	}
}
