package virtio

import (
	"encoding/binary"
	"errors"
	"sync"
	"testing"

	"kube-vm/virtio/virtq"
)

func statsRequest(h *blockHandler, op uint32, data []byte) (byte, error) {
	header := make([]byte, 16)
	binary.LittleEndian.PutUint32(header, op)
	status := []byte{0xff}
	buffers := [][]byte{header}
	if op != blkTFlush {
		buffers = append(buffers, data)
	}
	buffers = append(buffers, status)
	ring := make([]virtq.Desc, len(buffers))
	for i, buf := range buffers {
		flags := uint16(virtq.DescFAvail | virtq.DescFNext)
		if i == len(buffers)-1 {
			flags = virtq.DescFAvail | virtq.DescFWrite
		} else if i > 0 && op == blkTIn {
			flags |= virtq.DescFWrite
		}
		ring[i] = virtq.Desc{Addr: uint64(i), Len: uint32(len(buf)), Flags: flags}
	}
	q := virtq.New(ring, nil, nil, virtq.Config{
		MemAt: func(addr uint64, length int) ([]byte, error) { return buffers[int(addr)], nil },
	})
	err := h.handle(q)
	return status[0], err
}

func TestBlockMetricsRequests(t *testing.T) {
	storage := &syncStorage{buf: make([]byte, 4096)}
	metrics := NewBlockMetrics("mem:4096", 4096, false)
	handler, err := (BlockDevice{Storage: storage, Metrics: metrics}).NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h := handler.(*blockHandler)
	for _, op := range []uint32{blkTOut, blkTIn, blkTFlush} {
		if status, err := statsRequest(h, op, []byte("abcd")); err != nil || status != blkSOK {
			t.Fatalf("op %d: status=%d err=%v", op, status, err)
		}
	}
	storage.syncErr = errors.New("injected flush error")
	if status, err := statsRequest(h, blkTFlush, nil); err != nil || status != blkSIOErr {
		t.Fatalf("failed flush: status=%d err=%v", status, err)
	}
	s := metrics.Snapshot()
	if s.ReadOps != 1 || s.WriteOps != 1 || s.FlushOps != 2 || s.CompletedOps != 4 {
		t.Fatalf("operation counters: %+v", s)
	}
	if s.ReadBytes != 4 || s.WriteBytes != 4 || s.Errors != 1 || s.InFlight != 0 {
		t.Fatalf("transfer counters: %+v", s)
	}
	if s.IOTimeNS < s.MaxIOTimeNS || s.Spec != "mem:4096" || s.CapacityBytes != 4096 || s.ReadOnly {
		t.Fatalf("invalid statistics: %+v", s)
	}
}

type blockedStatsStorage struct {
	*syncStorage
	entered chan struct{}
	resume  chan struct{}
}

func (s *blockedStatsStorage) WriteAt(p []byte, off int64) (int, error) {
	close(s.entered)
	<-s.resume
	return s.syncStorage.WriteAt(p, off)
}

func TestBlockMetricsSnapshotDuringBlockedIO(t *testing.T) {
	s := &blockedStatsStorage{
		syncStorage: &syncStorage{buf: make([]byte, 4096)},
		entered:     make(chan struct{}), resume: make(chan struct{}),
	}
	m := NewBlockMetrics("blocked", 4096, false)
	handler, err := (BlockDevice{Storage: s, Metrics: m}).NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := statsRequest(handler.(*blockHandler), blkTOut, []byte("data"))
		done <- err
	}()
	<-s.entered
	// Release the worker even if an assertion fails.
	defer func() {
		close(s.resume)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	snapshot := m.Snapshot()
	if snapshot.InFlight != 1 || snapshot.WriteOps != 1 || snapshot.CompletedOps != 0 {
		t.Fatalf("in-flight statistics: %+v", snapshot)
	}
}

func TestBlockMetricsConcurrentSnapshots(t *testing.T) {
	m := NewBlockMetrics("disk", 4096, false)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				start := m.begin(blkTOut)
				_ = m.Snapshot()
				m.finish(start, 0, 512, false)
			}
		}()
	}
	wg.Wait()
	s := m.Snapshot()
	if s.WriteOps != 8000 || s.CompletedOps != 8000 || s.WriteBytes != 8000*512 || s.InFlight != 0 {
		t.Fatalf("concurrent counters: %+v", s)
	}
}
