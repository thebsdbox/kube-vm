package virtio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

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

	stopCh   chan struct{}
	stopOnce sync.Once

	txTraceLeft int32
	rxTraceLeft int32
	hdrLen      int32
}

const (
	netRxQ = 0
	netTxQ = 1

	virtioNetHdrLen     = 10
	maxPacketLen        = 64 << 10
	netDHCPDebugLogPath = "/tmp/kube-vm-dhcp.log"
)

func (cfg NetDevice) NewHandler() (DeviceHandler, error) {
	if cfg.Backend == nil {
		return nil, errors.New("net device backend is nil")
	}

	return &netHandler{cfg: cfg, stopCh: make(chan struct{}), txTraceLeft: 200, rxTraceLeft: 200, hdrLen: virtioNetHdrLen}, nil
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
		netDHCPLogf("kube-vm: net: rx queue ready")
		h.rxQ = q
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			netDHCPLogf("kube-vm: net: rx loop start")
			if err := h.handleRx(); err != nil && !errors.Is(err, io.EOF) {
				slog.Error("net rx", "error", err)
				netDHCPLogf("kube-vm: net: rx loop error err=%v", err)
			}
			netDHCPLogf("kube-vm: net: rx loop stop")
		}()

	case netTxQ:
		netDHCPLogf("kube-vm: net: tx queue ready")
		h.txQ = q
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			defer netDHCPLogf("kube-vm: net: tx loop stop")
			netDHCPLogf("kube-vm: net: tx loop start")
			for {
				select {
				case <-h.stopCh:
					return
				case _, ok := <-notify:
					if !ok {
						return
					}
					if err := h.handleTx(q); err != nil {
						slog.Error("net tx", "error", err)
						netDHCPLogf("kube-vm: net: tx loop error err=%v", err)
					}
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
	h.stopOnce.Do(func() {
		close(h.stopCh)
	})
	err := h.cfg.Backend.Close()
	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		netDHCPLogf("kube-vm: net: close timeout waiting for goroutines")
	}
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

		hdrLen := int(atomic.LoadInt32(&h.hdrLen))
		if hdrLen < virtioNetHdrLen || hdrLen > virtioNetHdrLen+4 {
			hdrLen = virtioNetHdrLen
		}
		if rxWritableCapacity(c) < hdrLen+n {
			if err := c.Release(0); err != nil {
				return err
			}
			continue
		}

		if msg, ok := dhcpFrameSummary(buf[:n]); ok {
			slog.Info("net dhcp rx", "summary", msg)
			netDHCPLogf("kube-vm: net dhcp rx: %s", msg)
		}
		if h.takeRxTrace() {
			netDHCPLogf("kube-vm: net rx frame: %s", frameSummary(buf[:n]))
		}

		written, err := writeRxPacket(c, buf[:n], hdrLen)
		if err != nil {
			return err
		}

		if err := c.Release(written); err != nil {
			return err
		}
	}
}

func writeRxPacket(c *virtq.Chain, pkt []byte, hdrLen int) (int, error) {
	hdr := make([]byte, hdrLen)

	written := 0
	needHdr := hdrLen

	for i, d := range c.Desc {
		if !d.IsWO() {
			continue
		}

		buf, err := c.Buf(i)
		if err != nil {
			return 0, err
		}

		if needHdr > 0 {
			n := copy(buf, hdr[hdrLen-needHdr:])
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

		frame, hdrLen, err := readTxPacket(c)
		if err != nil {
			return err
		}
		if hdrLen == virtioNetHdrLen || hdrLen == virtioNetHdrLen+2 || hdrLen == virtioNetHdrLen+4 {
			atomic.StoreInt32(&h.hdrLen, int32(hdrLen))
		}

		if len(frame) > 0 {
			if msg, ok := dhcpFrameSummary(frame); ok {
				slog.Info("net dhcp tx", "summary", msg)
				netDHCPLogf("kube-vm: net dhcp tx: %s", msg)
			}
			if h.takeTxTrace() {
				netDHCPLogf("kube-vm: net tx frame: %s", frameSummary(frame))
			}
			if _, err := h.cfg.Backend.Write(frame); err != nil {
				return err
			}
		}

		if err := c.Release(0); err != nil {
			return err
		}
	}
}

func readTxPacket(c *virtq.Chain) ([]byte, int, error) {
	buf := make([]byte, 0, maxPacketLen)

	for i, d := range c.Desc {
		if d.IsWO() {
			continue
		}

		part, err := c.Buf(i)
		if err != nil {
			return nil, 0, err
		}

		buf = append(buf, part...)
	}

	if len(buf) < virtioNetHdrLen {
		return nil, virtioNetHdrLen, nil
	}

	hdrLen := detectTxHeaderLen(buf)
	if len(buf) < hdrLen {
		return nil, hdrLen, nil
	}
	return buf[hdrLen:], hdrLen, nil
}

func detectTxHeaderLen(buf []byte) int {
	candidates := []int{virtioNetHdrLen, virtioNetHdrLen + 2, virtioNetHdrLen + 4}
	bestLen := virtioNetHdrLen
	bestScore := -1
	for _, off := range candidates {
		score := ethernetScoreAt(buf, off)
		if score > bestScore {
			bestScore = score
			bestLen = off
		}
	}
	return bestLen
}

func looksLikeEthernetFrame(f []byte) bool {
	if len(f) < 14 {
		return false
	}
	ethType := binary.BigEndian.Uint16(f[12:14])
	if ethType >= 0x0600 {
		return true
	}
	return false
}

func ethernetScoreAt(buf []byte, off int) int {
	if len(buf) < off+14 {
		return -1000
	}
	f := buf[off:]
	score := 0
	if looksLikeEthernetFrame(f) {
		score += 1
	}
	ethType := binary.BigEndian.Uint16(f[12:14])
	switch ethType {
	case 0x0800, 0x0806, 0x86dd:
		score += 100
	default:
		if ethType >= 0x0600 {
			score += 2
		}
	}
	if len(f) >= 6 {
		d0, d1 := f[0], f[1]
		if d0 == 0xff && d1 == 0xff {
			score += 10
		}
		if d0 == 0x33 && d1 == 0x33 {
			score += 10
		}
		if d0 == 0x00 && d1 == 0x00 {
			score -= 5
		}
	}
	return score
}

func dhcpFrameSummary(frame []byte) (string, bool) {
	// Ethernet II + IPv4 + UDP minimum header length.
	if len(frame) < 14+20+8 {
		return "", false
	}
	if binary.BigEndian.Uint16(frame[12:14]) != 0x0800 {
		return "", false
	}
	ipStart := 14
	ihl := int(frame[ipStart]&0x0f) * 4
	if ihl < 20 || len(frame) < ipStart+ihl+8 {
		return "", false
	}
	if frame[ipStart+9] != 17 {
		return "", false
	}
	udpStart := ipStart + ihl
	srcPort := binary.BigEndian.Uint16(frame[udpStart : udpStart+2])
	dstPort := binary.BigEndian.Uint16(frame[udpStart+2 : udpStart+4])
	if !((srcPort == 68 && dstPort == 67) || (srcPort == 67 && dstPort == 68)) {
		return "", false
	}
	srcIP := frame[ipStart+12 : ipStart+16]
	dstIP := frame[ipStart+16 : ipStart+20]
	msgType := "unknown"
	payload := frame[udpStart+8:]
	if mt, ok := parseDHCPMessageType(payload); ok {
		msgType = mt
	}
	return fmt.Sprintf("%d.%d.%d.%d:%d -> %d.%d.%d.%d:%d type=%s len=%d",
		srcIP[0], srcIP[1], srcIP[2], srcIP[3], srcPort,
		dstIP[0], dstIP[1], dstIP[2], dstIP[3], dstPort,
		msgType, len(frame)), true
}

func parseDHCPMessageType(payload []byte) (string, bool) {
	if len(payload) < 240 {
		return "", false
	}
	if payload[236] != 99 || payload[237] != 130 || payload[238] != 83 || payload[239] != 99 {
		return "", false
	}
	i := 240
	for i < len(payload) {
		code := payload[i]
		i++
		if code == 0 {
			continue
		}
		if code == 255 {
			break
		}
		if i >= len(payload) {
			break
		}
		l := int(payload[i])
		i++
		if i+l > len(payload) {
			break
		}
		if code == 53 && l >= 1 {
			switch payload[i] {
			case 1:
				return "discover", true
			case 2:
				return "offer", true
			case 3:
				return "request", true
			case 5:
				return "ack", true
			default:
				return fmt.Sprintf("type-%d", payload[i]), true
			}
		}
		i += l
	}
	return "", false
}

func (h *netHandler) takeTxTrace() bool {
	for {
		v := atomic.LoadInt32(&h.txTraceLeft)
		if v <= 0 {
			return false
		}
		if atomic.CompareAndSwapInt32(&h.txTraceLeft, v, v-1) {
			return true
		}
	}
}

func (h *netHandler) takeRxTrace() bool {
	for {
		v := atomic.LoadInt32(&h.rxTraceLeft)
		if v <= 0 {
			return false
		}
		if atomic.CompareAndSwapInt32(&h.rxTraceLeft, v, v-1) {
			return true
		}
	}
}

func frameSummary(frame []byte) string {
	if len(frame) < 14 {
		return fmt.Sprintf("short-eth len=%d", len(frame))
	}
	dst := macString(frame[0:6])
	src := macString(frame[6:12])
	ethType := binary.BigEndian.Uint16(frame[12:14])
	off := 14
	if ethType == 0x8100 && len(frame) >= 18 {
		ethType = binary.BigEndian.Uint16(frame[16:18])
		off = 18
	}
	switch ethType {
	case 0x0806:
		return fmt.Sprintf("src=%s dst=%s eth=arp len=%d", src, dst, len(frame))
	case 0x0800:
		if len(frame) < off+20 {
			return fmt.Sprintf("src=%s dst=%s eth=ipv4 short-ip len=%d", src, dst, len(frame))
		}
		ihl := int(frame[off]&0x0f) * 4
		if ihl < 20 || len(frame) < off+ihl {
			return fmt.Sprintf("src=%s dst=%s eth=ipv4 bad-ihl=%d len=%d", src, dst, ihl, len(frame))
		}
		proto := frame[off+9]
		srcIP := frame[off+12 : off+16]
		dstIP := frame[off+16 : off+20]
		if proto == 17 && len(frame) >= off+ihl+8 {
			udp := off + ihl
			sport := binary.BigEndian.Uint16(frame[udp : udp+2])
			dport := binary.BigEndian.Uint16(frame[udp+2 : udp+4])
			return fmt.Sprintf("src=%s dst=%s eth=ipv4 udp %d.%d.%d.%d:%d -> %d.%d.%d.%d:%d len=%d", src, dst, srcIP[0], srcIP[1], srcIP[2], srcIP[3], sport, dstIP[0], dstIP[1], dstIP[2], dstIP[3], dport, len(frame))
		}
		return fmt.Sprintf("src=%s dst=%s eth=ipv4 proto=%d %d.%d.%d.%d -> %d.%d.%d.%d len=%d", src, dst, proto, srcIP[0], srcIP[1], srcIP[2], srcIP[3], dstIP[0], dstIP[1], dstIP[2], dstIP[3], len(frame))
	case 0x86dd:
		return fmt.Sprintf("src=%s dst=%s eth=ipv6 len=%d", src, dst, len(frame))
	default:
		return fmt.Sprintf("src=%s dst=%s eth=0x%04x len=%d", src, dst, ethType, len(frame))
	}
}

func macString(b []byte) string {
	if len(b) < 6 {
		return "00:00:00:00:00:00"
	}
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4], b[5])
}

func netDHCPLogf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("%s %s\n", time.Now().Format(time.RFC3339), msg)
	_, _ = os.Stderr.WriteString(line)
	f, err := os.OpenFile(netDHCPDebugLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(line)
	_ = f.Close()
}
