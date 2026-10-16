package mmio

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kube-vm/virtio"
	"kube-vm/virtio/virtq"
)

type snapshotHandler struct {
	typ virtio.DeviceID
	ready int
	queues map[int]*virtq.Queue
	notify map[int]<-chan struct{}
	check func()
}

func (h *snapshotHandler) NewHandler() (virtio.DeviceHandler, error) { return h, nil }
func (h *snapshotHandler) GetType() virtio.DeviceID {
	if h.typ != 0 { return h.typ }
	return virtio.ConsoleDeviceID
}
func (*snapshotHandler) GetFeatures() uint64 { return 0 }
func (h *snapshotHandler) Ready(uint64) error { h.check(); h.ready++; return nil }
func (h *snapshotHandler) QueueReady(n int, q *virtq.Queue, c <-chan struct{}) error {
	h.check()
	h.queues[n], h.notify[n] = q, c
	return nil
}
func (*snapshotHandler) ReadConfig([]byte, int) error { return nil }
func (*snapshotHandler) Close() error { return nil }

func snapshotTestBus(t *testing.T) (*Bus, *snapshotHandler, []byte) {
	t.Helper()
	mem := make([]byte, 4096)
	h := &snapshotHandler{queues: make(map[int]*virtq.Queue), notify: make(map[int]<-chan struct{})}
	b, err := NewBus([]virtio.DeviceConfig{h}, Config{
		MemAt: func(addr uint64, size int) ([]byte, error) {
			if addr > uint64(len(mem)) || size < 0 || uint64(size) > uint64(len(mem))-addr {
				return nil, errors.New("outside guest memory")
			}
			return mem[int(addr):int(addr)+size], nil
		},
	})
	if err != nil { t.Fatal(err) }
	h.check = func() {
		if !b.dev[0].mu.TryLock() { t.Fatal("handler callback called with device mutex held") }
		b.dev[0].mu.Unlock()
	}
	return b, h, mem
}

func readySnapshot(b *Bus) []DeviceState {
	states := b.ExportDeviceState()
	states[0].Status = operatingNormally
	states[0].DriverFeatures = virtio.RequiredFeatures
	states[0].Queue[0] = queueState{Ready: 1, NumDesc: 4, DescAddr: 0x100, DriverAddr: 0x200, DeviceAddr: 0x204}
	states[0].QueueCursors[0] = virtq.QueueState{AvailIndex: 3, UsedIndex: 3, AvailWrap: false, UsedWrap: false}
	return states
}

func TestRestoreReadyQueuesAndInitialNotification(t *testing.T) {
	b, h, _ := snapshotTestBus(t)
	release := b.Quiesce()
	states := readySnapshot(b)
	encoded, err := json.Marshal(states)
	if err != nil { t.Fatal(err) }
	if err := json.Unmarshal(encoded, &states); err != nil { t.Fatal(err) }
	if err := b.RestoreDeviceState(states); err != nil { t.Fatal(err) }
	if h.ready != 1 || len(h.queues) != 1 { t.Fatalf("Ready=%d queues=%d", h.ready, len(h.queues)) }
	if got := h.queues[0].Snapshot(); got != states[0].QueueCursors[0] { t.Fatalf("cursor: %+v", got) }
	select {
	case <-h.notify[0]:
	default: t.Fatal("missing initial notification")
	}
	entered := make(chan struct{})
	done := make(chan struct{})
	go func() {
		close(entered)
		_ = h.queues[0].WithAccess(func() error { close(done); return nil })
	}()
	<-entered
	select {
	case <-done: t.Fatal("restored queue bypassed exclusive gate")
	default:
	}
	release()
	select {
	case <-done:
	case <-time.After(time.Second): t.Fatal("queue did not resume")
	}
	if err := b.Close(); err != nil { t.Fatal(err) }
}

func TestRestorePreflightValidation(t *testing.T) {
	for _, test := range []struct {
		name string
		mutate func([]DeviceState)
	}{
		{"old version", func(s []DeviceState) { s[0].SnapshotVersion = 0 }},
		{"missing cursor", func(s []DeviceState) { delete(s[0].QueueCursors, 0) }},
		{"wrong type", func(s []DeviceState) { s[0].Type = virtio.BlockDeviceID }},
		{"wrong features", func(s []DeviceState) { s[0].Features++ }},
		{"unnegotiated features", func(s []DeviceState) { s[0].DriverFeatures = 0 }},
		{"zero ring", func(s []DeviceState) { s[0].Queue[0].NumDesc = 0 }},
		{"large ring", func(s []DeviceState) { s[0].Queue[0].NumDesc = 65536 }},
		{"bad address", func(s []DeviceState) { s[0].Queue[0].DescAddr = 0x2000 }},
		{"unaligned address", func(s []DeviceState) { s[0].Queue[0].DescAddr++ }},
		{"overlap", func(s []DeviceState) { s[0].Queue[0].DriverAddr = 0x100 }},
		{"bad event memory", func(s []DeviceState) { s[0].Queue[0].DeviceAddr = 4096 }},
		{"bad cursor", func(s []DeviceState) { s[0].QueueCursors[0] = virtq.QueueState{AvailIndex: 4, UsedIndex: 4} }},
		{"later invalid queue", func(s []DeviceState) { s[0].Queue[1] = s[0].Queue[0] }},
	} {
		t.Run(test.name, func(t *testing.T) {
			b, h, _ := snapshotTestBus(t)
			release := b.Quiesce()
			defer release()
			s := readySnapshot(b)
			test.mutate(s)
			err := b.RestoreDeviceState(s)
			if err == nil { t.Fatal("invalid snapshot accepted") }
			if (test.name == "old version" || test.name == "missing cursor") && !strings.Contains(err.Error(), "version") { t.Fatalf("unclear version error: %v", err) }
			if h.ready != 0 || len(h.queues) != 0 { t.Fatal("started handler before all queues validated") }
		})
	}
}

func TestSnapshotStorageLogicalDisks(t *testing.T) {
	base := &virtio.MemStorage{Bytes: bytes.Repeat([]byte{0x11}, 8192)}
	overlay, err := os.CreateTemp(t.TempDir(), "overlay")
	if err != nil { t.Fatal(err) }
	defer overlay.Close()
	cow, err := virtio.OpenCOWStorage(base, overlay)
	if err != nil { t.Fatal(err) }
	patch := bytes.Repeat([]byte{0xaa}, 32)
	if _, err := cow.WriteAt(patch, 4080); err != nil { t.Fatal(err) }
	wantCOW := append([]byte(nil), base.Bytes...)
	copy(wantCOW[4080:], patch)
	mem := &virtio.MemStorage{Bytes: bytes.Repeat([]byte{0x22}, 512)}
	ro := &virtio.MemStorage{Bytes: bytes.Repeat([]byte{0x33}, 512)}
	b, err := NewBus([]virtio.DeviceConfig{
		virtio.BlockDevice{Storage: mem},
		virtio.BlockDevice{Storage: cow},
		virtio.BlockDevice{Storage: ro, ReadOnly: true},
	}, Config{})
	if err != nil { t.Fatal(err) }
	dir := t.TempDir()
	release := b.Quiesce()
	states, err := b.SnapshotStorage(dir)
	release()
	if err != nil { t.Fatal(err) }
	if len(states) != 3 { t.Fatalf("disks=%d", len(states)) }
	for i, want := range [][]byte{mem.Bytes, wantCOW, ro.Bytes} {
		s := states[i]
		if s.File != fmt.Sprintf("disk-%d.img", i) || s.Size != int64(len(want)) || s.ReadOnly != (i == 2) { t.Fatalf("metadata: %+v", s) }
		got, err := os.ReadFile(filepath.Join(dir, s.File))
		if err != nil { t.Fatal(err) }
		if !bytes.Equal(got, want) { t.Fatalf("disk %d: not logical bytes", i) }
		hash := sha256.Sum256(got)
		if s.SHA256 != fmt.Sprintf("%x", hash) { t.Fatal("bad hash") }
		stat, err := os.Stat(filepath.Join(dir, s.File))
		if err != nil { t.Fatal(err) }
		if stat.Mode().Perm() != 0600 { t.Fatalf("mode: %v", stat.Mode()) }
	}
	release = b.Quiesce()
	_, err = b.SnapshotStorage(dir)
	release()
	if err == nil { t.Fatal("overwrote immutable snapshot") }
	if err := b.Close(); err != nil { t.Fatal(err) }
}

func TestSnapshotRejectsVSock(t *testing.T) {
	h := &snapshotHandler{typ: virtio.SocketDeviceID}
	b, err := NewBus([]virtio.DeviceConfig{h}, Config{})
	if err != nil { t.Fatal(err) }
	if err := b.ValidateSnapshotSupport(); err == nil || !strings.Contains(err.Error(), "VSock") {
		t.Fatalf("VSock validation: %v", err)
	}
	if _, err := b.SnapshotStorage(t.TempDir()); err == nil { t.Fatal("silently skipped VSock") }
}

type failingSnapshotStorage struct {
	virtio.MemStorage
	syncErr error
	readErr error
	syncs int
}

func (s *failingSnapshotStorage) Sync() error { s.syncs++; return s.syncErr }
func (s *failingSnapshotStorage) ReadAt(p []byte, off int64) (int, error) {
	if s.readErr != nil { return 0, s.readErr }
	return s.MemStorage.ReadAt(p, off)
}

func TestSnapshotStorageErrorsAndCleanup(t *testing.T) {
	for _, stage := range []string{"sync", "read"} {
		t.Run(stage, func(t *testing.T) {
			failure := errors.New("injected " + stage + " error")
			storage := &failingSnapshotStorage{MemStorage: virtio.MemStorage{Bytes: make([]byte, 512)}}
			if stage == "sync" { storage.syncErr = failure } else { storage.readErr = failure }
			b, err := NewBus([]virtio.DeviceConfig{
				virtio.BlockDevice{Storage: &virtio.MemStorage{Bytes: make([]byte, 512)}},
				virtio.BlockDevice{Storage: storage},
			}, Config{})
			if err != nil { t.Fatal(err) }
			dir := t.TempDir()
			release := b.Quiesce()
			_, err = b.SnapshotStorage(dir)
			release()
			if !errors.Is(err, failure) { t.Fatalf("error not propagated: %v", err) }
			if storage.syncs != 1 { t.Fatal("writable storage not synced") }
			entries, err := os.ReadDir(dir)
			if err != nil { t.Fatal(err) }
			if len(entries) != 0 { t.Fatal("partial snapshot artifacts remain") }
		})
	}
}

type snapshotNetBackend struct { bytes.Buffer }
func (*snapshotNetBackend) Close() error { return nil }

func TestSnapshotNetworkHeaderLength(t *testing.T) {
	for _, header := range []byte{10, 12, 14, 11} {
		t.Run(fmt.Sprint(header), func(t *testing.T) {
			b, err := NewBus([]virtio.DeviceConfig{virtio.NetDevice{Backend: &snapshotNetBackend{}}}, Config{})
			if err != nil { t.Fatal(err) }
			release := b.Quiesce()
			states := b.ExportDeviceState()
			states[0].HandlerState = []byte{header}
			err = b.RestoreDeviceState(states)
			if header == 11 {
				if err == nil { t.Fatal("invalid header length accepted") }
			} else {
				if err != nil { t.Fatal(err) }
				if got := b.ExportDeviceState()[0].HandlerState; !bytes.Equal(got, []byte{header}) { t.Fatalf("header not restored: %v", got) }
			}
			release()
			if err := b.Close(); err != nil { t.Fatal(err) }
		})
	}
}