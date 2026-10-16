package virtio

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"kube-vm/virtio/virtq"
)

type blockingConsoleInput struct {
	started chan struct{}
	release chan struct{}
}

func (r *blockingConsoleInput) Read(p []byte) (int, error) {
	close(r.started)
	<-r.release
	return copy(p, "x"), io.EOF
}

func TestConsoleInputReadDoesNotClaimGuestBufferOrGate(t *testing.T) {
	in := &blockingConsoleInput{started: make(chan struct{}), release: make(chan struct{})}
	handler, err := (ConsoleDevice{In: in}).NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	ring := []virtq.Desc{{Len: 1, Flags: virtq.DescFAvail | virtq.DescFWrite}}
	mem := make([]byte, 1)
	var gate sync.RWMutex
	q := virtq.New(ring, nil, nil, virtq.Config{
		MemAt: func(uint64, int) ([]byte, error) { return mem, nil },
		BeginAccess: func() func() {
			gate.RLock()
			return gate.RUnlock
		},
	})
	done := make(chan error, 1)
	go func() { done <- handler.(*consoleHandler).handleRx(q) }()
	<-in.started
	if !gate.TryLock() {
		close(in.release)
		t.Fatal("console input Read holds the I/O gate")
	}
	if s := q.Snapshot(); s.AvailIndex != 0 || !s.AvailWrap {
		t.Errorf("console claimed a guest buffer before reading input: %+v", s)
	}
	gate.Unlock()
	close(in.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("console input did not finish")
	}
	if string(mem) != "x" || ring[0].Len != 1 {
		t.Fatal("input with EOF was not completed")
	}
}

func TestConsolePreservesInputUntilGuestCapacityReturns(t *testing.T) {
	handler, err := (ConsoleDevice{In: strings.NewReader("abcdefgh")}).NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	h := handler.(*consoleHandler)
	ring := []virtq.Desc{{Len: 4, Flags: virtq.DescFAvail | virtq.DescFWrite}}
	mem := make([]byte, 4)
	q := virtq.New(ring, nil, nil, virtq.Config{
		MemAt: func(uint64, int) ([]byte, error) { return mem, nil },
	})
	if err := h.handleRx(q); err != nil {
		t.Fatal(err)
	}
	if string(mem) != "abcd" || string(h.pending) != "efgh" {
		t.Fatalf("mem=%q pending=%q", mem, h.pending)
	}
	// No guest buffer yet: a new notification must not discard pending input.
	if err := h.handleRx(q); err != nil {
		t.Fatal(err)
	}
	if string(h.pending) != "efgh" {
		t.Fatalf("lost pending input: %q", h.pending)
	}
	ring[0] = virtq.Desc{Len: 4, Flags: virtq.DescFUsed | virtq.DescFWrite}
	if err := h.handleRx(q); err != nil {
		t.Fatal(err)
	}
	if string(mem) != "efgh" || len(h.pending) != 0 {
		t.Fatalf("mem=%q pending=%q", mem, h.pending)
	}
}

func TestConsoleHandlerDefaultReadsWholeBuffer(t *testing.T) {
	ring := []virtq.Desc{{Addr: 0x1, Len: 4, Flags: virtq.DescFAvail | virtq.DescFWrite}}
	mem := make([]byte, 4)
	notifyCount := 0

	h, err := (ConsoleDevice{In: strings.NewReader("abcd")}).NewHandler()
	if err != nil {
		t.Fatalf("NewHandler returned error: %v", err)
	}

	q := virtq.New(ring, new(virtq.EventSuppress), nil, virtq.Config{
		MemAt: func(addr uint64, length int) ([]byte, error) {
			if addr != 0x1 {
				t.Fatalf("addr = %#x, want %#x", addr, uint64(0x1))
			}
			return mem[:length], nil
		},
		Notify: func() error {
			notifyCount++
			return nil
		},
	})

	if err := h.(*consoleHandler).handleRx(q); err != nil {
		t.Fatalf("handleRx returned error: %v", err)
	}
	if got := string(mem); got != "abcd" {
		t.Fatalf("buffer = %q, want %q", got, "abcd")
	}
	if got := ring[0].Len; got != 4 {
		t.Fatalf("used length = %d, want 4", got)
	}
	if notifyCount != 1 {
		t.Fatalf("notify count = %d, want 1", notifyCount)
	}
}
