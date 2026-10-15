package virtio

import (
	"strings"
	"testing"

	"kube-vm/virtio/virtq"
)

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
