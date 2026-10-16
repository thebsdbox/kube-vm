package virtio

import (
	"io"
	"log/slog"
	"sync"

	"kube-vm/virtio/virtq"
)

// ConsoleDevice configures a virtio console device.
type ConsoleDevice struct {
	In  io.Reader
	Out io.Writer
}

type consoleHandler struct {
	cfg     ConsoleDevice
	wg      sync.WaitGroup
	stop    chan struct{}
	once    sync.Once
	pending []byte // accessed only under the queue gate
}

const (
	consoleRxQ = 0
	consoleTxQ = 1
)

func (cfg ConsoleDevice) NewHandler() (DeviceHandler, error) {
	return &consoleHandler{cfg: cfg, stop: make(chan struct{})}, nil
}

func (h *consoleHandler) GetType() DeviceID {
	return ConsoleDeviceID
}

func (*consoleHandler) GetFeatures() uint64 {
	return 0
}

func (*consoleHandler) Ready(negotiatedFeatures uint64) error {
	return nil
}

func (h *consoleHandler) QueueReady(num int, q *virtq.Queue, notify <-chan struct{}) error {
	switch num {
	case consoleRxQ:
		if h.cfg.In != nil {
			h.wg.Add(1)
			go func() {
				defer h.wg.Done()
				h.runRx(q, notify)
			}()
		}

	case consoleTxQ:
		if h.cfg.Out != nil {
			h.wg.Add(1)
			go func() {
				defer h.wg.Done()
				for range notify {
					if err := h.handleTx(q); err != nil {
						if err == io.EOF {
							return
						}
						slog.Error("console tx", "err", err)
					}
				}
			}()
		}
	}

	return nil
}

func (h *consoleHandler) ReadConfig(p []byte, off int) error {
	return nil
}

func (h *consoleHandler) Close() error {
	h.once.Do(func() { close(h.stop) })
	h.wg.Wait()
	return nil
}

func (h *consoleHandler) handleRx(q *virtq.Queue) error {
	// Read only into host-owned memory, never into a claimed guest chain.
	if len(h.pending) == 0 {
		buf := make([]byte, 4096)
		n, err := h.cfg.In.Read(buf)
		if n == 0 {
			return err
		}
		return q.WithAccess(func() error {
			h.pending = append(h.pending, buf[:n]...)
			return h.drainRx(q)
		})
	}
	return q.WithAccess(func() error { return h.drainRx(q) })
}

func (h *consoleHandler) runRx(q *virtq.Queue, notify <-chan struct{}) {
	reads := make(chan []byte)
	// An arbitrary io.Reader cannot be interrupted. If it blocks forever this
	// helper may remain, but it owns no guest memory and does not delay Close.
	go func() {
		defer close(reads)
		for {
			buf := make([]byte, 4096)
			n, err := h.cfg.In.Read(buf)
			select {
			case reads <- buf[:n]:
			case <-h.stop:
				return
			}
			if err != nil {
				if err != io.EOF {
					slog.Error("console input", "err", err)
				}
				return
			}
		}
	}()
	for {
		// Apply backpressure rather than losing input when the guest is full.
		var source <-chan []byte
		_ = q.WithAccess(func() error {
			if len(h.pending) == 0 {
				source = reads
			}
			return nil
		})
		select {
		case <-h.stop:
			return
		case _, ok := <-notify:
			if !ok {
				return
			}
		case in, ok := <-source:
			if !ok {
				return
			}
			_ = q.WithAccess(func() error {
				h.pending = append(h.pending, in...)
				return nil
			})
		}
		if err := q.WithAccess(func() error { return h.drainRx(q) }); err != nil {
			slog.Error("console rx", "err", err)
			return
		}
	}
}

func (h *consoleHandler) SnapshotHandlerState() []byte    { return append([]byte(nil), h.pending...) }
func (*consoleHandler) ValidateHandlerState([]byte) error { return nil }
func (h *consoleHandler) RestoreHandlerState(s []byte) error {
	h.pending = append([]byte(nil), s...)
	return nil
}

func (h *consoleHandler) drainRx(q *virtq.Queue) error {
	for len(h.pending) > 0 {
		c, err := q.Next()
		if err != nil {
			return err
		}

		if c == nil {
			break
		}

		var n int
		for i, d := range c.Desc {
			if !d.IsWO() {
				continue
			}

			buf, gbe := c.Buf(i)
			if gbe != nil {
				return gbe
			}

			copied := copy(buf, h.pending)
			h.pending = h.pending[copied:]
			n += copied
		}

		if err := c.Release(n); err != nil {
			return err
		}
	}

	return nil
}

func (h *consoleHandler) handleTx(q *virtq.Queue) error {
	return q.WithAccess(func() error { return h.drainTx(q) })
}

func (h *consoleHandler) drainTx(q *virtq.Queue) error {
	for {
		c, err := q.Next()
		if err != nil {
			return err
		}

		if c == nil {
			break
		}

		for i, d := range c.Desc {
			if d.IsWO() {
				break
			}

			buf, err := c.Buf(i)
			if err != nil {
				return err
			}

			if _, err := h.cfg.Out.Write(buf); err != nil {
				return err
			}
		}

		if err := c.Release(0); err != nil {
			return err
		}
	}

	return nil
}
