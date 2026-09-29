package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"

	"kube-vm/control"
	"kube-vm/os/linux"
	"kube-vm/virtio"
	"kube-vm/vmm"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func main() {

	var (
		memSize    = flag.Int("mem", 1024, "set the VM's memory size in MiB")
		numCPU     = flag.Int("cpu", 1, "set the number of VCPUs")
		kernelPath = flag.String("kernel", "bzImage", "load bzImage from file or URL")
		initrdPath = flag.String("initrd", "", "load initial ramdisk from file or URL")
		cmdline    = flag.String("cmdline", "console=hvc0 reboot=t", "set the kernel command line")
		tapName    = flag.String("tap", "", "attach a virtio-net device backed by TAP (optional name)")
		socketPath = flag.String("socket", "/tmp/kube-vm.sock", "path to the kube-vm control socket")
		serverMode = flag.Bool("server", true, "run kube-vm in server mode and accept control requests over a UNIX socket")

		blkdev flagStrings
	)

	flag.Var(&blkdev, "block", "add a block device (multiple OK)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), unix.SIGINT, unix.SIGTERM)
	defer stop()

	if !*serverMode {
		bzImage, err := readURL(*kernelPath)
		if err != nil {
			panic(err)
		}

		ll := &linux.Loader{
			Kernel:  bzImage,
			Cmdline: *cmdline,
		}

		if *initrdPath != "" {
			initrd, err := readURL(*initrdPath)
			if err != nil {
				panic(err)
			}

			ll.Initrd = initrd
		}

		cfg := vmm.Config{
			MemSize: *memSize << 20,
			NumCPU:  *numCPU,
			Loader:  ll,
		}

		for _, s := range blkdev {
			s, ro := strings.CutSuffix(s, ":ro")
			u, err := url.Parse(s)
			if err != nil {
				panic(err)
			}

			var stg virtio.BlockStorage

			switch u.Scheme {
			case "file", "":
				var flg int
				if !ro {
					flg = os.O_RDWR
				}
				f, err := os.OpenFile(u.Path, flg, 0)
				if err != nil {
					panic(err)
				}
				stg = &virtio.FileStorage{File: f}
			case "http", "https":
				ro = true
				stg = &virtio.HTTPStorage{URL: u.String()}
			case "mem":
				sz, err := strconv.ParseInt(u.Opaque, 10, 64)
				if err != nil {
					panic(err)
				}
				stg = &virtio.MemStorage{Bytes: make([]byte, sz)}
			default:
				panic("unsupported block storage scheme: " + u.Scheme)
			}

			cfg.Devices = append(cfg.Devices, &virtio.BlockDevice{ReadOnly: ro, Storage: stg})
		}

		if *tapName != "" {
			tap, name, err := virtio.OpenTAP(*tapName)
			if err != nil {
				panic(err)
			}
			fmt.Fprintf(os.Stderr, "kube-vm: using tap backend %s\n", name)
			cfg.Devices = append(cfg.Devices, &virtio.NetDevice{Backend: tap})
		}

		cfg.Devices = append(cfg.Devices, &virtio.ConsoleDevice{In: os.Stdin, Out: os.Stdout})

		m, err := vmm.New(cfg)
		if err != nil {
			panic(err)
		}

		if term.IsTerminal(int(os.Stdin.Fd())) {
			old, err := term.MakeRaw(int(os.Stdin.Fd()))
			if err != nil {
				panic(err)
			}
			defer term.Restore(int(os.Stdin.Fd()), old)
		}

		err = m.Run(ctx)
		if errors.Is(err, context.Canceled) {
			return
		}
		if err != nil {
			panic(err)
		}
		return
	}

	server := &vmServer{ctx: ctx, socketPath: *socketPath}
	go func() {
		if err := control.Serve(ctx, *socketPath, server); err != nil {
			fmt.Fprintf(os.Stderr, "kube-vm: control server: %v\n", err)
		}
	}()

	<-ctx.Done()
	server.mu.Lock()
	defer server.mu.Unlock()
	for _, vm := range server.vms {
		if vm != nil {
			_ = vm.Shutdown()
		}
	}
}

type vmServer struct {
	mu         sync.Mutex
	ctx        context.Context
	socketPath string
	vms        map[string]*vmController
}

func logVM(uid, phase, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "kube-vm: vm %s: %s: %s\n", uid, phase, msg)
}

func (s *vmServer) Status(uid string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if uid == "" {
		if len(s.vms) == 0 {
			return "stopped"
		}
		return "running"
	}
	if vm, ok := s.vms[uid]; ok && vm != nil && vm.vm != nil {
		return "running"
	}
	return "stopped"
}

func (s *vmServer) Start(uid string, payload map[string]any) (string, error) {
	if uid == "" {
		uid = generateUID()
	}

	s.mu.Lock()
	if _, ok := s.vms[uid]; ok {
		s.mu.Unlock()
		return "", fmt.Errorf("vm: uid %s already exists", uid)
	}
	if s.vms == nil {
		s.vms = make(map[string]*vmController)
	}
	s.mu.Unlock()

	consoleSession := control.NewConsoleSession()
	vmCtx, cancel := context.WithCancel(s.ctx)
	vmc := &vmController{ctx: vmCtx, cancel: cancel, consoleSession: consoleSession, uid: uid}
	consoleSession.SetOnClose(func() {
		logVM(uid, "console", "session closed; requesting shutdown")
		if err := vmc.Shutdown(); err != nil && !errors.Is(err, vmm.ErrVMClosed) {
			fmt.Fprintf(os.Stderr, "kube-vm: shutdown vm %s on console close: %v\n", uid, err)
		}
	})
	logVM(uid, "start", "building VM configuration")
	cfg, err := buildVMConfig(payload, consoleSession)
	if err != nil {
		logVM(uid, "start", "config build failed: %v", err)
		return "", err
	}

	logVM(uid, "start", "creating VMM instance")
	m, err := vmm.New(cfg)
	if err != nil {
		logVM(uid, "start", "VMM create failed: %v", err)
		return "", fmt.Errorf("vm: start failed: %w", err)
	}
	logVM(uid, "start", "VMM instance created")

	vmc.vm = m
	consolePath := s.socketPath + "." + uid + ".console"
	go func() {
		logVM(uid, "console", "serving console socket %s", consolePath)
		if err := control.ServeConsole(vmCtx, consolePath, consoleSession); err != nil {
			fmt.Fprintf(os.Stderr, "kube-vm: console server %s: %v\n", consolePath, err)
		}
		logVM(uid, "console", "console server exited")
	}()

	go func() {
		defer func() {
			logVM(uid, "cleanup", "run goroutine cleanup begin")
			if vmc.isClosed() {
				s.mu.Lock()
				if s.vms[uid] == vmc {
					delete(s.vms, uid)
				}
				s.mu.Unlock()
				logVM(uid, "cleanup", "removed from server registry")
			}
			_ = consoleSession.Close()
			if err := os.Remove(consolePath); err != nil && !errors.Is(err, os.ErrNotExist) {
				fmt.Fprintf(os.Stderr, "kube-vm: remove console socket %s: %v\n", consolePath, err)
			}
			logVM(uid, "cleanup", "run goroutine cleanup complete")
		}()

		logVM(uid, "run", "starting VM run loop")
		if err := m.Run(vmCtx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, vmm.ErrVMClosed) {
			fmt.Fprintf(os.Stderr, "kube-vm: vm %s exited: %v\n", uid, err)
		}
		logVM(uid, "run", "run loop exited; invoking shutdown")
		if err := vmc.Shutdown(); err != nil && !errors.Is(err, vmm.ErrVMClosed) {
			fmt.Fprintf(os.Stderr, "kube-vm: shutdown vm %s: %v\n", uid, err)
		}
		logVM(uid, "run", "shutdown after run loop complete")
	}()

	s.mu.Lock()
	s.vms[uid] = vmc
	s.mu.Unlock()

	return uid, nil
}

func (s *vmServer) Connect(uid string) error {
	s.mu.Lock()
	vm, ok := s.vms[uid]
	s.mu.Unlock()
	if !ok || vm == nil || vm.vm == nil {
		return errors.New("vm: instance is not running")
	}
	return nil
}

func (s *vmServer) Shutdown(uid string) error {
	s.mu.Lock()
	vm, ok := s.vms[uid]
	if !ok || vm == nil {
		delete(s.vms, uid)
		s.mu.Unlock()
		logVM(uid, "shutdown", "requested but VM not found")
		return nil
	}
	s.mu.Unlock()

	logVM(uid, "shutdown", "control shutdown requested")
	err := vm.Shutdown()
	if err != nil {
		logVM(uid, "shutdown", "shutdown failed: %v", err)
	} else {
		logVM(uid, "shutdown", "shutdown completed")
	}

	if err == nil && vm.isClosed() {
		s.mu.Lock()
		delete(s.vms, uid)
		s.mu.Unlock()
	}

	return err
}

func generateUID() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 6)
	_, err := rand.Read(b)
	if err != nil {
		for i := range b {
			b[i] = alphabet[i%len(alphabet)]
		}
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// readURL reads body from a file path or URL.
// It supports file, http, and https schemes.
type vmController struct {
	mu             sync.Mutex
	ctx            context.Context
	cancel         context.CancelFunc
	uid            string
	consoleSession *control.ConsoleSession
	vm             *vmm.VM
	shuttingDown   bool
}

func (c *vmController) Status() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.vm == nil {
		return "stopped"
	}
	return "running"
}

func (c *vmController) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.vm == nil {
		return errors.New("vm: instance is not running")
	}
	return nil
}

func (c *vmController) Shutdown() error {
	c.mu.Lock()
	if c.shuttingDown {
		c.mu.Unlock()
		logVM(c.uid, "shutdown", "already shutting down")
		return nil
	}
	c.shuttingDown = true
	vm := c.vm
	if c.cancel != nil {
		c.cancel()
	}
	c.mu.Unlock()
	logVM(c.uid, "shutdown", "begin controller shutdown")

	if c.consoleSession != nil {
		c.consoleSession.SetOnClose(nil)
		_ = c.consoleSession.Close()
		logVM(c.uid, "shutdown", "console session closed")
	}

	var err error
	if vm != nil {
		logVM(c.uid, "shutdown", "calling vmm.Close")
		err = vm.Close()
		if errors.Is(err, vmm.ErrVMClosed) {
			err = nil
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.shuttingDown = false
	if err == nil {
		c.vm = nil
		logVM(c.uid, "shutdown", "controller shutdown complete")
	} else {
		logVM(c.uid, "shutdown", "controller shutdown error: %v", err)
	}
	return err
}

func (c *vmController) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.vm == nil
}

func buildVMConfig(payload map[string]any, consoleSession *control.ConsoleSession) (vmm.Config, error) {
	kernel := asString(payload, "kernel", "bzImage")
	initrd := asString(payload, "initrd", "")
	cmdline := asString(payload, "cmdline", "console=hvc0 reboot=t")
	memSize := asInt(payload, "mem", 1024)
	numCPU := asInt(payload, "cpu", 1)
	tapName := asString(payload, "tap", "")

	bzImage, err := readURL(kernel)
	if err != nil {
		return vmm.Config{}, err
	}

	ll := &linux.Loader{Kernel: bzImage, Cmdline: cmdline}
	if initrd != "" {
		initrdBytes, err := readURL(initrd)
		if err != nil {
			return vmm.Config{}, err
		}
		ll.Initrd = initrdBytes
	}

	cfg := vmm.Config{
		MemSize: memSize << 20,
		NumCPU:  numCPU,
		Loader:  ll,
	}

	if tapName != "" {
		tap, name, err := virtio.OpenTAP(tapName)
		if err != nil {
			return vmm.Config{}, err
		}
		fmt.Fprintf(os.Stderr, "kube-vm: using tap backend %s\n", name)
		cfg.Devices = append(cfg.Devices, &virtio.NetDevice{Backend: tap})
	}

	cfg.Devices = append(cfg.Devices, &virtio.ConsoleDevice{In: consoleSession, Out: consoleSession})
	return cfg, nil
}

func asString(payload map[string]any, key, def string) string {
	if payload == nil {
		return def
	}
	if v, ok := payload[key]; ok {
		switch val := v.(type) {
		case string:
			if val != "" {
				return val
			}
		case fmt.Stringer:
			if s := val.String(); s != "" {
				return s
			}
		}
	}
	return def
}

func asInt(payload map[string]any, key string, def int) int {
	if payload == nil {
		return def
	}
	if v, ok := payload[key]; ok {
		switch val := v.(type) {
		case int:
			return val
		case int32:
			return int(val)
		case int64:
			return int(val)
		case float64:
			return int(val)
		case string:
			if n, err := strconv.Atoi(val); err == nil {
				return n
			}
		}
	}
	return def
}

func readURL(s string) (body []byte, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("kube-vm: read URL %s: %w", s, err)
		}
	}()

	u, err := url.Parse(s)
	if err != nil {
		return nil, err
	}

	switch u.Scheme {
	case "", "file":
		return os.ReadFile(u.Path)

	case "http", "https":
		res, err := http.Get(u.String())
		if err != nil {
			panic(err)
		}

		if res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("response status %d != %d", res.StatusCode, 200)
		}

		defer res.Body.Close()
		return io.ReadAll(res.Body)

	default:
		panic(u.Scheme)
	}
}

// flagStrings is a flag.Value that collects strings.
type flagStrings []string

func (*flagStrings) String() string {
	return ""
}

func (fs *flagStrings) Set(s string) error {
	*fs = append(*fs, s)
	return nil
}
