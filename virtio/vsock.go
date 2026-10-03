package virtio

import (
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"sync"

	"kube-vm/virtio/virtq"
)

const (
	vsockCIDHost = 2
	vsockRXQ     = 0
	vsockTXQ     = 1
)

// VSockDevice configures a virtio-vsock device.
type VSockDevice struct {
	// GuestCID is the guest's assigned vsock CID. Values 0, 1, and 2 are reserved.
	GuestCID uint64
	// Backend is the host-side connection used for guest/host traffic.
	Backend io.ReadWriteCloser
}

type vsockHandler struct {
	cfg VSockDevice
	rxQ *virtq.Queue
	txQ *virtq.Queue
	wg  sync.WaitGroup
}

func (cfg VSockDevice) NewHandler() (DeviceHandler, error) {
	if cfg.GuestCID == 0 || cfg.GuestCID <= vsockCIDHost {
		return nil, errors.New("vsock guest CID must be greater than 2")
	}
	if cfg.Backend == nil {
		return nil, errors.New("vsock device backend is nil")
	}
	return &vsockHandler{cfg: cfg}, nil
}

func (*vsockHandler) GetType() DeviceID {
	return SocketDeviceID
}

func (*vsockHandler) GetFeatures() uint64 {
	return 0
}

func (*vsockHandler) Ready(negotiatedFeatures uint64) error {
	return nil
}

func (h *vsockHandler) QueueReady(num int, q *virtq.Queue, notify <-chan struct{}) error {
	switch num {
	case vsockRXQ:
		h.rxQ = q
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			if err := h.handleRx(); err != nil && !errors.Is(err, io.EOF) {
				slog.Error("vsock rx", "error", err)
			}
		}()
	case vsockTXQ:
		h.txQ = q
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			for range notify {
				if err := h.handleTx(q); err != nil {
					slog.Error("vsock tx", "error", err)
				}
			}
		}()
	}
	return nil
}

func (h *vsockHandler) ReadConfig(p []byte, off int) error {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], h.cfg.GuestCID)
	copy(p, b[off:])
	return nil
}

func (h *vsockHandler) Close() error {
	if h.cfg.Backend != nil {
		_ = h.cfg.Backend.Close()
	}
	h.wg.Wait()
	return nil
}

func (h *vsockHandler) handleRx() error {
	buf := make([]byte, 64<<10)
	for {
		n, err := h.cfg.Backend.Read(buf)
		if err != nil {
			return err
		}
		if n == 0 || h.rxQ == nil {
			continue
		}

		c, err := h.rxQ.Next()
		if err != nil {
			return err
		}
		if c == nil {
			continue
		}

		written, err := copyChain(c, buf[:n])
		if err != nil {
			return err
		}
		if err := c.Release(written); err != nil {
			return err
		}
	}
}

func (h *vsockHandler) handleTx(q *virtq.Queue) error {
	for {
		c, err := q.Next()
		if err != nil {
			return err
		}
		if c == nil {
			return nil
		}

		buf := make([]byte, 0, 64<<10)
		for i, d := range c.Desc {
			if d.IsWO() {
				continue
			}
			part, err := c.Buf(i)
			if err != nil {
				return err
			}
			buf = append(buf, part...)
		}
		if len(buf) > 0 {
			if _, err := h.cfg.Backend.Write(buf); err != nil {
				return err
			}
		}
		if err := c.Release(0); err != nil {
			return err
		}
	}
}

func copyChain(c *virtq.Chain, data []byte) (int, error) {
	written := 0
	for i, d := range c.Desc {
		if !d.IsWO() {
			continue
		}
		buf, err := c.Buf(i)
		if err != nil {
			return written, err
		}
		n := copy(buf, data)
		written += n
		data = data[n:]
		if len(data) == 0 {
			return written, nil
		}
	}
	return written, nil
}
