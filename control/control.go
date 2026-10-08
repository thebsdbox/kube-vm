package control

import (
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
	conn    net.Conn
	closed  bool
	dropped bool
	onClose func()
}

func NewConsoleSession() *ConsoleSession {
	s := &ConsoleSession{}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *ConsoleSession) SetConn(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		_ = conn.Close()
		return
	}
	if s.conn != nil {
		fmt.Fprintf(os.Stderr, "kube-vm: console session: replacing existing client %s with %s\n", s.conn.RemoteAddr(), conn.RemoteAddr())
		_ = s.conn.Close()
	} else {
		fmt.Fprintf(os.Stderr, "kube-vm: console session: client attached %s\n", conn.RemoteAddr())
	}
	s.conn = conn
	s.dropped = false
	s.cond.Broadcast()
}

func (s *ConsoleSession) SetOnClose(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onClose = fn
}

func (s *ConsoleSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
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
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, io.EOF
	}
	conn := s.conn
	dropped := s.dropped
	s.mu.Unlock()

	if conn == nil {
		if !dropped {
			s.mu.Lock()
			if !s.closed && s.conn == nil && !s.dropped {
				s.dropped = true
				fmt.Fprintln(os.Stderr, "kube-vm: console session: no client attached; dropping console output until a client connects")
			}
			s.mu.Unlock()
		}
		return len(p), nil
	}

	n, err := conn.Write(p)
	if err != nil && (errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrClosed) || errors.Is(err, syscall.EBADF) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET)) {
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
