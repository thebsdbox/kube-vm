//go:build linux

package nat

import (
	"context"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"kube-vm/virtio"

	"golang.org/x/sys/unix"
)

type Config struct {
	TapName    string
	SubnetCIDR string
	GatewayIP  string
	GuestIP    string
}

type backend struct {
	io.ReadWriteCloser
	cleanup []func()
}

const dhcpDebugLogPath = "/tmp/kube-vm-dhcp.log"

func (n *backend) Close() error {
	var firstErr error
	for i := len(n.cleanup) - 1; i >= 0; i-- {
		n.cleanup[i]()
	}
	if n.ReadWriteCloser != nil {
		if err := n.ReadWriteCloser.Close(); err != nil {
			firstErr = err
		}
	}
	return firstErr
}

func OpenBackend(uid string) (io.ReadWriteCloser, Config, error) {
	if uid == "" {
		uid = "vm"
	}
	dhcpLogf("kube-vm: nat: OpenBackend uid=%s", uid)
	if _, err := exec.LookPath("ip"); err != nil {
		dhcpLogf("kube-vm: nat: missing ip command: %v", err)
		return nil, Config{}, fmt.Errorf("nat mode requires ip command: %w", err)
	}
	if _, err := exec.LookPath("iptables"); err != nil {
		dhcpLogf("kube-vm: nat: missing iptables command: %v", err)
		return nil, Config{}, fmt.Errorf("nat mode requires iptables command: %w", err)
	}

	tap, tapName, err := virtio.OpenTAP("")
	if err != nil {
		dhcpLogf("kube-vm: nat: tap create failed uid=%s err=%v", uid, err)
		return nil, Config{}, fmt.Errorf("nat tap create: %w", err)
	}
	dhcpLogf("kube-vm: nat: tap created uid=%s tap=%s", uid, tapName)

	cfg, err := allocateSubnet(uid)
	if err != nil {
		_ = tap.Close()
		return nil, Config{}, err
	}
	cfg.TapName = tapName

	if err := runCmd("ip", "link", "set", "dev", tapName, "up"); err != nil {
		_ = tap.Close()
		return nil, Config{}, fmt.Errorf("nat link up %s: %w", tapName, err)
	}
	if err := runCmd("ip", "addr", "add", cfg.GatewayIP+"/30", "dev", tapName); err != nil {
		_ = tap.Close()
		return nil, Config{}, fmt.Errorf("nat address assign %s: %w", tapName, err)
	}
	if err := os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1\n"), 0o644); err != nil {
		_ = tap.Close()
		return nil, Config{}, fmt.Errorf("nat enable ip_forward: %w", err)
	}

	forwardIn := []string{"-A", "FORWARD", "-i", tapName, "-j", "ACCEPT"}
	forwardOut := []string{"-A", "FORWARD", "-o", tapName, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"}
	masq := []string{"-t", "nat", "-A", "POSTROUTING", "-s", cfg.SubnetCIDR, "-j", "MASQUERADE"}
	dhcpIn := []string{"-A", "INPUT", "-i", tapName, "-p", "udp", "--sport", "68", "--dport", "67", "-j", "ACCEPT"}
	dhcpOut := []string{"-A", "OUTPUT", "-o", tapName, "-p", "udp", "--sport", "67", "--dport", "68", "-j", "ACCEPT"}

	if err := runCmd("iptables", forwardIn...); err != nil {
		_ = tap.Close()
		return nil, Config{}, fmt.Errorf("nat add forward-in rule: %w", err)
	}
	if err := runCmd("iptables", forwardOut...); err != nil {
		_ = runCmd("iptables", swapAppendDelete(forwardIn)...)
		_ = tap.Close()
		return nil, Config{}, fmt.Errorf("nat add forward-out rule: %w", err)
	}
	if err := runCmd("iptables", masq...); err != nil {
		_ = runCmd("iptables", swapAppendDelete(forwardOut)...)
		_ = runCmd("iptables", swapAppendDelete(forwardIn)...)
		_ = tap.Close()
		return nil, Config{}, fmt.Errorf("nat add masquerade rule: %w", err)
	}
	if err := runCmd("iptables", dhcpIn...); err != nil {
		_ = runCmd("iptables", swapAppendDelete(masq)...)
		_ = runCmd("iptables", swapAppendDelete(forwardOut)...)
		_ = runCmd("iptables", swapAppendDelete(forwardIn)...)
		_ = tap.Close()
		return nil, Config{}, fmt.Errorf("nat add dhcp input rule: %w", err)
	}
	if err := runCmd("iptables", dhcpOut...); err != nil {
		_ = runCmd("iptables", swapAppendDelete(dhcpIn)...)
		_ = runCmd("iptables", swapAppendDelete(masq)...)
		_ = runCmd("iptables", swapAppendDelete(forwardOut)...)
		_ = runCmd("iptables", swapAppendDelete(forwardIn)...)
		_ = tap.Close()
		return nil, Config{}, fmt.Errorf("nat add dhcp output rule: %w", err)
	}

	dhcpCleanup, err := startDHCPServer(uid, tapName, cfg)
	if err != nil {
		dhcpLogf("kube-vm: nat: dhcp start failed uid=%s tap=%s err=%v", uid, tapName, err)
		_ = runCmd("iptables", swapAppendDelete(dhcpOut)...)
		_ = runCmd("iptables", swapAppendDelete(dhcpIn)...)
		_ = runCmd("iptables", swapAppendDelete(masq)...)
		_ = runCmd("iptables", swapAppendDelete(forwardOut)...)
		_ = runCmd("iptables", swapAppendDelete(forwardIn)...)
		_ = tap.Close()
		return nil, Config{}, err
	}
	dhcpLogf("kube-vm: nat: dhcp started uid=%s tap=%s", uid, tapName)

	n := &backend{ReadWriteCloser: tap}
	n.cleanup = append(n.cleanup,
		dhcpCleanup,
		func() { _ = runCmd("iptables", swapAppendDelete(dhcpOut)...) },
		func() { _ = runCmd("iptables", swapAppendDelete(dhcpIn)...) },
		func() { _ = runCmd("iptables", swapAppendDelete(masq)...) },
		func() { _ = runCmd("iptables", swapAppendDelete(forwardOut)...) },
		func() { _ = runCmd("iptables", swapAppendDelete(forwardIn)...) },
		func() { _ = runCmd("ip", "addr", "del", cfg.GatewayIP+"/30", "dev", tapName) },
		func() { _ = runCmd("ip", "link", "set", "dev", tapName, "down") },
	)
	dhcpLogf("kube-vm: nat: backend ready uid=%s tap=%s", uid, tapName)

	return n, cfg, nil
}

func allocateSubnet(uid string) (Config, error) {
	h := sha1.Sum([]byte(uid))
	idx := int(h[0])<<8 | int(h[1])
	idx = idx % (256 * 64)
	third := idx / 64
	fourth := (idx % 64) * 4
	netPrefix := fmt.Sprintf("10.249.%d.%d", third, fourth)
	return Config{
		SubnetCIDR: netPrefix + "/30",
		GatewayIP:  fmt.Sprintf("10.249.%d.%d", third, fourth+1),
		GuestIP:    fmt.Sprintf("10.249.%d.%d", third, fourth+2),
	}, nil
}

func swapAppendDelete(rule []string) []string {
	if len(rule) == 0 {
		return nil
	}
	out := make([]string, len(rule))
	copy(out, rule)
	for i := 0; i < len(out)-1; i++ {
		if out[i] == "-A" {
			out[i] = "-D"
			return out
		}
	}
	return out
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, msg)
	}
	return nil
}

func startDHCPServer(uid, tapName string, cfg Config) (func(), error) {
	dhcpLogf("kube-vm: dhcp[%s]: starting uid=%s gateway=%s guest=%s subnet=%s", tapName, uid, cfg.GatewayIP, cfg.GuestIP, cfg.SubnetCIDR)
	serverIP := net.ParseIP(cfg.GatewayIP).To4()
	guestIP := net.ParseIP(cfg.GuestIP).To4()
	if serverIP == nil || guestIP == nil {
		dhcpLogf("kube-vm: dhcp[%s]: invalid ip config gateway=%s guest=%s", tapName, cfg.GatewayIP, cfg.GuestIP)
		return nil, fmt.Errorf("nat dhcp server invalid ip config gateway=%q guest=%q", cfg.GatewayIP, cfg.GuestIP)
	}
	_, subnet, err := net.ParseCIDR(cfg.SubnetCIDR)
	if err != nil {
		dhcpLogf("kube-vm: dhcp[%s]: invalid subnet=%s err=%v", tapName, cfg.SubnetCIDR, err)
		return nil, fmt.Errorf("nat dhcp server invalid subnet %q: %w", cfg.SubnetCIDR, err)
	}
	bcast := subnetBroadcastIPv4(subnet)
	if bcast == nil {
		dhcpLogf("kube-vm: dhcp[%s]: broadcast derivation failed for subnet=%s", tapName, cfg.SubnetCIDR)
		return nil, fmt.Errorf("nat dhcp server could not derive broadcast for %q", cfg.SubnetCIDR)
	}

	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var controlErr error
			err := c.Control(func(fd uintptr) {
				_ = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
				_ = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
				if err := unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, tapName); err != nil {
					controlErr = fmt.Errorf("bind dhcp socket to %s: %w", tapName, err)
					return
				}
				if err := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_BROADCAST, 1); err != nil {
					controlErr = fmt.Errorf("enable dhcp broadcast on %s: %w", tapName, err)
					return
				}
			})
			if err != nil {
				return err
			}
			return controlErr
		},
	}

	pc, err := lc.ListenPacket(context.Background(), "udp4", ":67")
	if err != nil {
		dhcpLogf("kube-vm: dhcp[%s]: listen failed err=%v", tapName, err)
		return nil, fmt.Errorf("nat dhcp server listen on %s: %w", tapName, err)
	}
	conn, ok := pc.(*net.UDPConn)
	if !ok {
		_ = pc.Close()
		dhcpLogf("kube-vm: dhcp[%s]: unexpected conn type=%T", tapName, pc)
		return nil, fmt.Errorf("nat dhcp server internal: unexpected packet conn type %T", pc)
	}
	dhcpLogf("kube-vm: dhcp[%s]: listening on udp/67 broadcast=%s", tapName, bcast.String())

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 1500)
		for {
			n, src, err := conn.ReadFromUDP(buf)
			if err != nil {
				select {
				case <-stop:
					dhcpLogf("kube-vm: dhcp[%s]: read loop stopping", tapName)
					return
				default:
					dhcpLogf("kube-vm: dhcp[%s]: read error err=%v", tapName, err)
					return
				}
			}
			dhcpLogf("kube-vm: dhcp[%s]: packet n=%d src=%v xid=%08x", tapName, n, src, dhcpXID(buf[:n]))
			if n < 240 {
				dhcpLogf("kube-vm: dhcp[%s]: drop short packet n=%d", tapName, n)
				continue
			}
			req := append([]byte(nil), buf[:n]...)
			if req[0] != 1 {
				dhcpLogf("kube-vm: dhcp[%s]: drop non-bootrequest op=%d", tapName, req[0])
				continue
			}
			msgType, ok := dhcpOptionByte(buf[:n], 53)
			if !ok {
				dhcpLogf("kube-vm: dhcp[%s]: drop missing dhcp type option xid=%08x", tapName, dhcpXID(req))
				continue
			}
			dhcpLogf("kube-vm: dhcp[%s]: recv type=%s(%d) chaddr=%s xid=%08x", tapName, dhcpTypeName(msgType), msgType, dhcpCHAddr(req), dhcpXID(req))

			var replyType byte
			switch msgType {
			case 1:
				replyType = 2
			case 3:
				replyType = 5
			default:
				dhcpLogf("kube-vm: dhcp[%s]: ignore type=%s(%d) xid=%08x", tapName, dhcpTypeName(msgType), msgType, dhcpXID(req))
				continue
			}

			yiaddr := guestIP
			if reqIP, ok := dhcpOptionIPv4(req, 50); ok {
				if reqIP.Equal(guestIP) {
					yiaddr = reqIP
				}
			}

			resp, err := buildDHCPReply(req, replyType, serverIP, yiaddr)
			if err != nil {
				dhcpLogf("kube-vm: dhcp[%s]: build reply failed xid=%08x err=%v", tapName, dhcpXID(req), err)
				continue
			}

			dst := &net.UDPAddr{IP: net.IPv4bcast, Port: 68}
			if req[10]&0x80 == 0 {
				if ci := net.IP(req[12:16]).To4(); ci != nil && !ci.Equal(net.IPv4zero) {
					dst = &net.UDPAddr{IP: ci, Port: 68}
				}
			}
			if _, err := conn.WriteToUDP(resp, dst); err != nil {
				dhcpLogf("kube-vm: dhcp[%s]: send primary failed dst=%s xid=%08x err=%v", tapName, dst.String(), dhcpXID(req), err)
			} else {
				dhcpLogf("kube-vm: dhcp[%s]: sent %s to dst=%s yiaddr=%s xid=%08x", tapName, dhcpTypeName(replyType), dst.String(), yiaddr.String(), dhcpXID(req))
			}
			if !dst.IP.Equal(bcast) {
				if _, err := conn.WriteToUDP(resp, &net.UDPAddr{IP: bcast, Port: 68}); err != nil {
					dhcpLogf("kube-vm: dhcp[%s]: send subnet-bcast failed dst=%s xid=%08x err=%v", tapName, bcast.String(), dhcpXID(req), err)
				} else {
					dhcpLogf("kube-vm: dhcp[%s]: sent %s to subnet-bcast=%s yiaddr=%s xid=%08x", tapName, dhcpTypeName(replyType), bcast.String(), yiaddr.String(), dhcpXID(req))
				}
			}
			if !dst.IP.Equal(net.IPv4bcast) {
				if _, err := conn.WriteToUDP(resp, &net.UDPAddr{IP: net.IPv4bcast, Port: 68}); err != nil {
					dhcpLogf("kube-vm: dhcp[%s]: send global-bcast failed xid=%08x err=%v", tapName, dhcpXID(req), err)
				} else {
					dhcpLogf("kube-vm: dhcp[%s]: sent %s to global-bcast yiaddr=%s xid=%08x", tapName, dhcpTypeName(replyType), yiaddr.String(), dhcpXID(req))
				}
			}
		}
	}()

	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			dhcpLogf("kube-vm: dhcp[%s]: shutting down", tapName)
			close(stop)
			_ = conn.Close()
			done := make(chan struct{})
			go func() {
				wg.Wait()
				close(done)
			}()
			select {
			case <-done:
				dhcpLogf("kube-vm: dhcp[%s]: stopped", tapName)
			case <-time.After(2 * time.Second):
				dhcpLogf("kube-vm: dhcp[%s]: shutdown timeout waiting for reader; continuing", tapName)
			}
		})
	}

	return cleanup, nil
}

func dhcpOptionByte(pkt []byte, key byte) (byte, bool) {
	if len(pkt) < 240 {
		return 0, false
	}
	if pkt[236] != 99 || pkt[237] != 130 || pkt[238] != 83 || pkt[239] != 99 {
		return 0, false
	}
	i := 240
	for i < len(pkt) {
		code := pkt[i]
		i++
		if code == 0 {
			continue
		}
		if code == 255 {
			break
		}
		if i >= len(pkt) {
			break
		}
		l := int(pkt[i])
		i++
		if i+l > len(pkt) {
			break
		}
		if code == key && l >= 1 {
			return pkt[i], true
		}
		i += l
	}
	return 0, false
}

func dhcpOptionIPv4(pkt []byte, key byte) (net.IP, bool) {
	if len(pkt) < 240 {
		return nil, false
	}
	if pkt[236] != 99 || pkt[237] != 130 || pkt[238] != 83 || pkt[239] != 99 {
		return nil, false
	}
	i := 240
	for i < len(pkt) {
		code := pkt[i]
		i++
		if code == 0 {
			continue
		}
		if code == 255 {
			break
		}
		if i >= len(pkt) {
			break
		}
		l := int(pkt[i])
		i++
		if i+l > len(pkt) {
			break
		}
		if code == key && l == 4 {
			return net.IPv4(pkt[i], pkt[i+1], pkt[i+2], pkt[i+3]).To4(), true
		}
		i += l
	}
	return nil, false
}

func subnetBroadcastIPv4(n *net.IPNet) net.IP {
	if n == nil {
		return nil
	}
	ip := n.IP.To4()
	if ip == nil {
		return nil
	}
	mask := net.IP(n.Mask).To4()
	if mask == nil {
		return nil
	}
	out := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		out[i] = ip[i] | ^mask[i]
	}
	return out
}

func buildDHCPReply(req []byte, msgType byte, serverIP, guestIP net.IP) ([]byte, error) {
	if len(req) < 240 {
		return nil, fmt.Errorf("dhcp request too short")
	}
	if len(serverIP) != 4 || len(guestIP) != 4 {
		return nil, fmt.Errorf("invalid dhcp ipv4 configuration")
	}

	reply := make([]byte, 240, 320)
	reply[0] = 2
	reply[1] = req[1]
	reply[2] = req[2]
	reply[3] = req[3]
	copy(reply[4:8], req[4:8])
	copy(reply[10:12], req[10:12])
	copy(reply[28:44], req[28:44])
	copy(reply[16:20], guestIP)
	copy(reply[20:24], serverIP)
	reply[236] = 99
	reply[237] = 130
	reply[238] = 83
	reply[239] = 99

	appendOption := func(code byte, value []byte) {
		reply = append(reply, code, byte(len(value)))
		reply = append(reply, value...)
	}

	appendOption(53, []byte{msgType})
	appendOption(54, []byte(serverIP.To4()))
	appendOption(1, []byte{255, 255, 255, 252})
	appendOption(3, []byte(serverIP.To4()))
	appendOption(6, []byte{1, 1, 1, 1, 8, 8, 8, 8})
	lease := make([]byte, 4)
	binary.BigEndian.PutUint32(lease, 3600)
	appendOption(51, lease)
	appendOption(58, []byte{0x00, 0x00, 0x07, 0x08})
	appendOption(59, []byte{0x00, 0x00, 0x0c, 0xa8})
	reply = append(reply, 255)

	return reply, nil
}

func dhcpXID(pkt []byte) uint32 {
	if len(pkt) < 8 {
		return 0
	}
	return binary.BigEndian.Uint32(pkt[4:8])
}

func dhcpCHAddr(pkt []byte) string {
	if len(pkt) < 44 {
		return ""
	}
	hlen := int(pkt[2])
	if hlen <= 0 {
		hlen = 6
	}
	if hlen > 16 {
		hlen = 16
	}
	end := 28 + hlen
	if end > len(pkt) {
		end = len(pkt)
	}
	if end <= 28 {
		return ""
	}
	parts := make([]string, 0, end-28)
	for _, b := range pkt[28:end] {
		parts = append(parts, fmt.Sprintf("%02x", b))
	}
	return strings.Join(parts, ":")
}

func dhcpTypeName(t byte) string {
	switch t {
	case 1:
		return "discover"
	case 2:
		return "offer"
	case 3:
		return "request"
	case 5:
		return "ack"
	default:
		return "unknown"
	}
}

func dhcpLogf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format(time.RFC3339)
	msg := fmt.Sprintf("%s %s\n", timestamp, line)
	_, _ = os.Stderr.WriteString(msg)
	f, err := os.OpenFile(dhcpDebugLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(msg)
	_ = f.Close()
}
