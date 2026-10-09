package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

const consoleHistoryLimit = 500

type consoleHistory struct {
	lines   [][]byte
	partial []byte
}

func (h *consoleHistory) append(p []byte) {
	for len(p) > 0 {
		idx := bytes.IndexByte(p, '\n')
		if idx < 0 {
			h.partial = append(h.partial, p...)
			return
		}

		h.partial = append(h.partial, p[:idx+1]...)
		h.lines = append(h.lines, append([]byte(nil), h.partial...))
		if len(h.lines) > consoleHistoryLimit {
			over := len(h.lines) - consoleHistoryLimit
			copy(h.lines, h.lines[over:])
			h.lines = h.lines[:consoleHistoryLimit]
		}
		h.partial = h.partial[:0]
		p = p[idx+1:]
	}
}

func (h *consoleHistory) snapshot() []byte {
	size := len(h.partial)
	for _, line := range h.lines {
		size += len(line)
	}
	out := make([]byte, 0, size)
	for _, line := range h.lines {
		out = append(out, line...)
	}
	out = append(out, h.partial...)
	return out
}

// Handler exposes the operations that a VM server must implement.
type Handler interface {
	Status(uid string) string
	List() []VMSummary
	Examine(uid string) (VMDetails, error)
	Start(uid string, payload map[string]any) (string, error)
	Connect(uid string) error
	Pause(uid string) error
	Resume(uid string) error
	Save(uid string, path string) error
	Restore(uid string, path string) error
	Shutdown(uid string) error
}

// VMSummary describes a running VM instance.
type VMSummary struct {
	UID       string `json:"uid"`
	State     string `json:"state"`
	IPAddress string `json:"ip_address,omitempty"`
}

type VMNIC struct {
	Name      string `json:"name,omitempty"`
	Mode      string `json:"mode,omitempty"`
	RXPackets uint64 `json:"rx_packets,omitempty"`
	RXBytes   uint64 `json:"rx_bytes,omitempty"`
	TXPackets uint64 `json:"tx_packets,omitempty"`
	TXBytes   uint64 `json:"tx_bytes,omitempty"`
}

type VMDetails struct {
	UID          string   `json:"uid,omitempty"`
	State        string   `json:"state,omitempty"`
	StartedAt    string   `json:"started_at,omitempty"`
	UptimeSec    int64    `json:"uptime_sec,omitempty"`
	MemMiB       int      `json:"mem_mib,omitempty"`
	NumCPU       int      `json:"num_cpu,omitempty"`
	Firmware     string   `json:"firmware,omitempty"`
	Kernel       string   `json:"kernel,omitempty"`
	Initrd       string   `json:"initrd,omitempty"`
	ISO          string   `json:"iso,omitempty"`
	Cmdline      string   `json:"cmdline,omitempty"`
	Tap          string   `json:"tap,omitempty"`
	NAT          bool     `json:"nat,omitempty"`
	VhostNet     bool     `json:"vhost_net,omitempty"`
	NATSubnet    string   `json:"nat_subnet,omitempty"`
	NATGateway   string   `json:"nat_gateway,omitempty"`
	NATGuestIP   string   `json:"nat_guest_ip,omitempty"`
	Block        []string `json:"block,omitempty"`
	HostMemBytes uint64   `json:"host_mem_bytes,omitempty"`
	HostCPUSec   float64  `json:"host_cpu_sec,omitempty"`
	HostCPUPct   float64  `json:"host_cpu_pct,omitempty"`
	LastError    string   `json:"last_error,omitempty"`
	NICs         []VMNIC  `json:"nics,omitempty"`
}

// Request is a JSON request sent over the control socket.
type Request struct {
	Action  string         `json:"action"`
	Payload map[string]any `json:"payload,omitempty"`
}

// Response is sent back over the control socket.
type Response struct {
	OK      bool        `json:"ok"`
	UID     string      `json:"uid,omitempty"`
	State   string      `json:"state,omitempty"`
	VMs     []VMSummary `json:"vms,omitempty"`
	VM      *VMDetails  `json:"vm,omitempty"`
	Message string      `json:"message,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// Dial sends a request to the server at socketPath and decodes the response.
func Dial(socketPath, action string, payload map[string]any) (Response, error) {
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(Request{Action: action, Payload: payload}); err != nil {
		return Response{}, err
	}

	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return Response{}, err
	}

	return resp, nil
}

// Serve accepts requests on a Unix socket and dispatches them to the handler.
func Serve(ctx context.Context, socketPath string, handler Handler) error {
	if socketPath == "" {
		return errors.New("control: socket path is empty")
	}

	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("control: remove existing socket %q: %w", socketPath, err)
	}

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("control: listen %q: %w", socketPath, err)
	}

	if err := os.Chmod(socketPath, 0o660); err != nil {
		_ = ln.Close()
		_ = os.Remove(socketPath)
		return fmt.Errorf("control: chmod %q: %w", socketPath, err)
	}

	go func() {
		<-ctx.Done()
		_ = ln.Close()
		_ = os.Remove(socketPath)
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return fmt.Errorf("control: accept: %w", err)
			}
		}

		go handleConn(conn, handler)
	}
}

func handleConn(conn net.Conn, handler Handler) {
	defer conn.Close()

	var req Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		_ = sendResponse(conn, Response{OK: false, Error: "invalid request"})
		return
	}

	_ = sendResponse(conn, handleRequest(req, handler))
}

func handleRequest(req Request, handler Handler) Response {
	action := strings.ToLower(req.Action)
	uid := uidFromPayload(req.Payload)

	switch action {
	case "", "status":
		return Response{OK: true, UID: uid, State: handler.Status(uid), Message: "vm instance is available"}

	case "list":
		vms := handler.List()
		return Response{OK: true, VMs: vms, State: handler.Status(""), Message: "vm instances listed"}

	case "examine":
		vm, err := handler.Examine(uid)
		if err != nil {
			return Response{OK: false, Error: err.Error()}
		}
		return Response{OK: true, UID: vm.UID, State: vm.State, VM: &vm, Message: "vm instance details"}

	case "start":
		startUID, err := handler.Start(uid, req.Payload)
		if err != nil {
			return Response{OK: false, Error: err.Error()}
		}
		return Response{OK: true, UID: startUID, State: handler.Status(startUID), Message: "vm instance started"}

	case "connect":
		if err := handler.Connect(uid); err != nil {
			return Response{OK: false, Error: err.Error()}
		}
		return Response{OK: true, UID: uid, State: handler.Status(uid), Message: "connected to vm instance"}

	case "pause":
		if err := handler.Pause(uid); err != nil {
			return Response{OK: false, Error: err.Error()}
		}
		return Response{OK: true, UID: uid, State: handler.Status(uid), Message: "vm instance paused"}

	case "resume":
		if err := handler.Resume(uid); err != nil {
			return Response{OK: false, Error: err.Error()}
		}
		return Response{OK: true, UID: uid, State: handler.Status(uid), Message: "vm instance resumed"}

	case "save":
		path := ""
		if req.Payload != nil {
			if p, ok := req.Payload["path"]; ok {
				if s, ok := p.(string); ok {
					path = s
				}
			}
		}
		if path == "" {
			return Response{OK: false, Error: "save action requires a path payload"}
		}
		if err := handler.Save(uid, path); err != nil {
			return Response{OK: false, Error: err.Error()}
		}
		return Response{OK: true, UID: uid, State: handler.Status(uid), Message: "vm state saved"}

	case "restore":
		path := ""
		if req.Payload != nil {
			if p, ok := req.Payload["path"]; ok {
				if s, ok := p.(string); ok {
					path = s
				}
			}
		}
		if path == "" {
			return Response{OK: false, Error: "restore action requires a path payload"}
		}
		if err := handler.Restore(uid, path); err != nil {
			return Response{OK: false, Error: err.Error()}
		}
		return Response{OK: true, UID: uid, State: handler.Status(uid), Message: "vm state restored"}

	case "shutdown":
		if err := handler.Shutdown(uid); err != nil {
			return Response{OK: false, Error: err.Error()}
		}
		return Response{OK: true, UID: uid, State: "stopped", Message: "vm shutdown requested"}

	default:
		return Response{OK: false, Error: fmt.Sprintf("unknown action %q", req.Action)}
	}
}

func uidFromPayload(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	if uid, ok := payload["uid"]; ok {
		if s, ok := uid.(string); ok {
			return s
		}
	}
	return ""
}

func sendResponse(conn net.Conn, resp Response) error {
	enc := json.NewEncoder(conn)
	return enc.Encode(resp)
}

// ConsoleSession is a simple proxy for a VM console that can be attached over a Unix socket.
type ConsoleSession struct {
	mu      sync.Mutex
	cond    *sync.Cond
	writeMu sync.Mutex
	conn    net.Conn
	closed  bool
	dropped bool
	onClose func()
	history consoleHistory
}

func NewConsoleSession() *ConsoleSession {
	s := &ConsoleSession{}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *ConsoleSession) SetConn(conn net.Conn) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = conn.Close()
		return
	}
	oldConn := s.conn
	if s.conn != nil {
		fmt.Fprintf(os.Stderr, "kube-vm: console session: replacing existing client %s with %s\n", s.conn.RemoteAddr(), conn.RemoteAddr())
	} else {
		fmt.Fprintf(os.Stderr, "kube-vm: console session: client attached %s\n", conn.RemoteAddr())
	}
	s.conn = conn
	s.dropped = false
	history := s.history.snapshot()
	s.cond.Broadcast()
	s.mu.Unlock()

	if oldConn != nil {
		_ = oldConn.Close()
	}
	if len(history) == 0 {
		return
	}
	if _, err := writeAll(conn, history); err != nil && isConsoleConnError(err) {
		s.mu.Lock()
		if s.conn == conn {
			s.conn = nil
			s.dropped = false
		}
		s.mu.Unlock()
		fmt.Fprintf(os.Stderr, "kube-vm: console session: client disconnected while replaying history (%v); waiting for reattach\n", err)
		_ = conn.Close()
	}
}

func (s *ConsoleSession) SetOnClose(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onClose = fn
}

func (s *ConsoleSession) Close() error {
	s.writeMu.Lock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.writeMu.Unlock()
		return nil
	}
	s.closed = true
	fmt.Fprintln(os.Stderr, "kube-vm: console session: closing")
	if s.conn != nil {
		c := s.conn
		s.conn = nil
		_ = c.Close()
	}
	fn := s.onClose
	s.onClose = nil
	s.cond.Broadcast()
	s.mu.Unlock()
	s.writeMu.Unlock()
	if fn != nil {
		fn()
	}
	return nil
}

func (s *ConsoleSession) Read(p []byte) (int, error) {
	for {
		s.mu.Lock()
		for !s.closed && s.conn == nil {
			s.cond.Wait()
		}
		if s.closed && s.conn == nil {
			s.mu.Unlock()
			return 0, io.EOF
		}
		conn := s.conn
		s.mu.Unlock()
		if conn != nil {
			n, err := conn.Read(p)
			if err != nil && (errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrClosed) || errors.Is(err, syscall.EBADF) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET)) {
				s.mu.Lock()
				if s.conn == conn {
					s.conn = nil
					s.dropped = false
				}
				s.mu.Unlock()
				fmt.Fprintf(os.Stderr, "kube-vm: console session: client disconnected while reading (%v); waiting for reattach\n", err)
				continue
			}
			return n, err
		}
	}
}

func (s *ConsoleSession) Write(p []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, io.EOF
	}
	s.history.append(p)
	conn := s.conn
	dropped := s.dropped

	if conn == nil {
		if !dropped {
			if !s.closed && s.conn == nil && !s.dropped {
				s.dropped = true
				fmt.Fprintln(os.Stderr, "kube-vm: console session: no client attached; buffering console output until a client connects")
			}
		}
		s.mu.Unlock()
		return len(p), nil
	}
	s.mu.Unlock()

	n, err := writeAll(conn, p)
	if err != nil && isConsoleConnError(err) {
		s.mu.Lock()
		if s.conn == conn {
			s.conn = nil
			s.dropped = false
		}
		s.mu.Unlock()
		fmt.Fprintf(os.Stderr, "kube-vm: console session: client disconnected while writing (%v); waiting for reattach\n", err)
		// Treat disconnect as non-fatal; allow future reattach.
		return len(p), nil
	}

	return n, err
}

func isConsoleConnError(err error) bool {
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, os.ErrClosed) ||
		errors.Is(err, syscall.EBADF) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET)
}

func writeAll(w io.Writer, p []byte) (int, error) {
	written := 0
	for written < len(p) {
		n, err := w.Write(p[written:])
		written += n
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}

func ServeConsole(ctx context.Context, socketPath string, session *ConsoleSession) error {
	if socketPath == "" {
		return errors.New("control: console socket path is empty")
	}

	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("control: remove existing console socket %q: %w", socketPath, err)
	}

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("control: listen console socket %q: %w", socketPath, err)
	}

	if err := os.Chmod(socketPath, 0o660); err != nil {
		_ = ln.Close()
		_ = os.Remove(socketPath)
		return fmt.Errorf("control: chmod console socket %q: %w", socketPath, err)
	}

	fmt.Fprintf(os.Stderr, "kube-vm: console server: listening on %s\n", socketPath)

	go func() {
		<-ctx.Done()
		_ = ln.Close()
		_ = os.Remove(socketPath)
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return fmt.Errorf("control: accept console socket: %w", err)
			}
		}

		fmt.Fprintf(os.Stderr, "kube-vm: console server: accepted client %s\n", conn.RemoteAddr())
		session.SetConn(conn)
		go func(c net.Conn) {
			<-ctx.Done()
			_ = c.Close()
		}(conn)
	}
}
