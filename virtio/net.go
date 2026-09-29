package virtio

import (
	"errors"
	"io"
	"log/slog"
	"sync"

	"kube-vm/virtio/virtq"
)

// NetDevice configures a virtio network device.
type NetDevice struct {
	// Backend is the host networking backend, such as a TAP device.
	Backend io.ReadWriteCloser
}

type netHandler struct {
	cfg NetDevice

	rxQ *virtq.Queue
	txQ *virtq.Queue

	wg sync.WaitGroup
}

const (
	netRxQ = 0
	netTxQ = 1

	virtioNetHdrLen = 10
	maxPacketLen    = 64 << 10
)

func (cfg NetDevice) NewHandler() (DeviceHandler, error) {
	if cfg.Backend == nil {
		return nil, errors.New("net device backend is nil")
	}

	return &netHandler{cfg: cfg}, nil
}

func (*netHandler) GetType() DeviceID {
	return NetworkDeviceID
}

func (*netHandler) GetFeatures() uint64 {
	return 0
}

func (*netHandler) Ready(negotiatedFeatures uint64) error {
	return nil
}

func (h *netHandler) QueueReady(num int, q *virtq.Queue, notify <-chan struct{}) error {
	switch num {
	case netRxQ:
		h.rxQ = q
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			if err := h.handleRx(); err != nil && !errors.Is(err, io.EOF) {
				slog.Error("net rx", "error", err)
			}
		}()

	case netTxQ:
		h.txQ = q
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			for range notify {
				if err := h.handleTx(q); err != nil {
					slog.Error("net tx", "error", err)
				}
			}
		}()
	}

	return nil
}

func (*netHandler) ReadConfig(p []byte, off int) error {
	return nil
}

func (h *netHandler) Close() error {
	err := h.cfg.Backend.Close()
	h.wg.Wait()
	return err
}

func (h *netHandler) handleRx() error {
	buf := make([]byte, maxPacketLen)

	for {
		n, err := h.cfg.Backend.Read(buf)
		if err != nil {
			return err
		}

		if h.rxQ == nil {
			continue
		}

		c, err := h.rxQ.Next()
		if err != nil {
			return err
		}

		if c == nil {
			// No guest RX buffer available, so drop this packet.
			continue
		}

		if rxWritableCapacity(c) < virtioNetHdrLen+n {
			if err := c.Release(0); err != nil {
				return err
			}
			continue
		}

		written, err := writeRxPacket(c, buf[:n])
		if err != nil {
			return err
		}

		if err := c.Release(written); err != nil {
			return err
		}
	}
}

func writeRxPacket(c *virtq.Chain, pkt []byte) (int, error) {
	var hdr [virtioNetHdrLen]byte

	written := 0
	needHdr := virtioNetHdrLen

	for i, d := range c.Desc {
		if !d.IsWO() {
			continue
		}

		buf, err := c.Buf(i)
		if err != nil {
			return 0, err
		}

		if needHdr > 0 {
			n := copy(buf, hdr[virtioNetHdrLen-needHdr:])
			buf = buf[n:]
			needHdr -= n
			written += n
		}

		if needHdr == 0 && len(pkt) > 0 && len(buf) > 0 {
			n := copy(buf, pkt)
			pkt = pkt[n:]
			written += n
		}

		if needHdr == 0 && len(pkt) == 0 {
			break
		}
	}

	if needHdr > 0 {
		return 0, nil
	}

	return written, nil
}

func rxWritableCapacity(c *virtq.Chain) int {
	cap := 0

	for i, d := range c.Desc {
		if !d.IsWO() {
			continue
		}

		buf, err := c.Buf(i)
		if err != nil {
			continue
		}

		cap += len(buf)
	}

	return cap
}

func (h *netHandler) handleTx(q *virtq.Queue) error {
	for {
		c, err := q.Next()
		if err != nil {
			return err
		}

		if c == nil {
			return nil
		}

		frame, err := readTxPacket(c)
		if err != nil {
			return err
		}

		if len(frame) > 0 {
			if _, err := h.cfg.Backend.Write(frame); err != nil {
				return err
			}
		}

		if err := c.Release(0); err != nil {
			return err
		}
	}
}

func readTxPacket(c *virtq.Chain) ([]byte, error) {
	buf := make([]byte, 0, maxPacketLen)

	for i, d := range c.Desc {
		if d.IsWO() {
			continue
		}

		part, err := c.Buf(i)
		if err != nil {
			return nil, err
		}

		buf = append(buf, part...)
	}

	if len(buf) < virtioNetHdrLen {
		return nil, nil
	}

	return buf[virtioNetHdrLen:], nil
}
