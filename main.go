package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"kube-vm/control"
	"kube-vm/hoststats"
	"kube-vm/vmconfig"
	"kube-vm/vmm"
	"kube-vm/vmnet"
	"kube-vm/webui"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func main() {
	fmt.Println("kube-vm is starting 🐙 ...")
	var (
		memSize      = flag.Int("mem", 1024, "set the VM's memory size in MiB")
		numCPU       = flag.Int("cpu", 1, "set the number of VCPUs")
		firmwarePath = flag.String("firmware", "", "boot guest from firmware image (BIOS/UEFI blob)")
		kernelPath   = flag.String("kernel", "bzImage", "load bzImage from file or URL")
		initrdPath   = flag.String("initrd", "", "load initial ramdisk from file or URL")
		isoPath      = flag.String("iso", "", "attach ISO image from file or URL as read-only block device")
		cmdline      = flag.String("cmdline", "console=hvc0 reboot=t", "set the kernel command line")
		tapName      = flag.String("tap", "", "attach a virtio-net device backed by TAP (optional name)")
		natMode      = flag.Bool("nat", false, "attach a virtio-net device with host NAT (Linux only)")
		socketPath   = flag.String("socket", "/tmp/kube-vm.sock", "path to the kube-vm control socket")
		webHost      = flag.String("web-host", "127.0.0.1", "host to bind the web frontend")
		webPort      = flag.Int("web-port", 0, "enable web frontend on this port (0 disables)")
		webToken     = flag.String("web-token", "", "optional bearer token required for web API access")
		serverMode   = flag.Bool("server", true, "run kube-vm in server mode and accept control requests over a UNIX socket")
		blkdev       flagStrings
	)

	flag.Var(&blkdev, "block", "add a block device (multiple OK)")
	flag.Parse()
	if *natMode && *tapName != "" {
		fmt.Fprintln(os.Stderr, "kube-vm: -nat and -tap are mutually exclusive")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), unix.SIGINT, unix.SIGTERM)
	defer stop()

	if !*serverMode {
		cfg, netCfg, err := vmconfig.Build(vmconfig.Options{
			UID:          "local",
			FirmwarePath: *firmwarePath,
			KernelPath:   *kernelPath,
			InitrdPath:   *initrdPath,
			BlockSpecs:   []string(blkdev),
			ISOPath:      *isoPath,
			Cmdline:      *cmdline,
			MemMiB:       *memSize,
			NumCPU:       *numCPU,
			TapName:      *tapName,
			NATMode:      *natMode,
			ConsoleIn:    os.Stdin,
			ConsoleOut:   os.Stdout,
		})
		if err != nil {
			panic(err)
		}
		if netCfg.NAT {
			fmt.Fprintf(os.Stderr, "kube-vm: NAT enabled on %s (guest route via %s, guest IP hint %s)\n", netCfg.TapName, netCfg.NATGateway, netCfg.NATGuestIP)
		} else if netCfg.TapName != "" {
			fmt.Fprintf(os.Stderr, "kube-vm: using tap backend %s\n", netCfg.TapName)
		}

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

	server := &vmServer{
		ctx:        ctx,
		socketPath: *socketPath,
		startedAt:  time.Now(),
	}
	go func() {
		if err := control.Serve(ctx, *socketPath, server); err != nil {
			fmt.Fprintf(os.Stderr, "kube-vm: control server: %v\n", err)
		}
	}()

	if *webPort > 0 {
		server.defaultStart = map[string]any{
			"mem":      *memSize,
			"cpu":      *numCPU,
			"firmware": *firmwarePath,
			"kernel":   *kernelPath,
			"initrd":   *initrdPath,
			"iso":      *isoPath,
			"cmdline":  *cmdline,
			"tap":      *tapName,
			"nat":      *natMode,
		}
		if len(blkdev) > 0 {
			blocks := make([]string, len(blkdev))
			copy(blocks, blkdev)
			server.defaultStart["block"] = blocks
		}

		addr := fmt.Sprintf("%s:%d", *webHost, *webPort)
		go func() {
			fmt.Fprintf(os.Stderr, "kube-vm: web frontend listening on http://%s\n", addr)
			svc := webui.Service{
				Start:               server.Start,
				Stop:                server.Shutdown,
				CheckVM:             server.Connect,
				ListVMs:             func() any { return server.vmStats() },
				DaemonStats:         func() any { return server.daemonStats() },
				DefaultStartPayload: server.defaultStartPayload,
				ConsoleSocket:       server.consoleSocket,
			}
			if err := webui.Serve(ctx, addr, svc, *webToken); err != nil {
				fmt.Fprintf(os.Stderr, "kube-vm: web frontend: %v\n", err)
			}
		}()
	}

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
	mu           sync.Mutex
	ctx          context.Context
	socketPath   string
	startedAt    time.Time
	defaultStart map[string]any
	vms          map[string]*vmController
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

func (s *vmServer) List() []control.VMSummary {
	s.mu.Lock()
	defer s.mu.Unlock()

	vms := make([]control.VMSummary, 0, len(s.vms))
	for uid, vm := range s.vms {
		if vm == nil || vm.vm == nil {
			continue
		}
		vms = append(vms, control.VMSummary{UID: uid, State: "running"})
	}

	return vms
}

func (s *vmServer) Start(uid string, payload map[string]any) (string, error) {
	if uid == "" {
		uid = generateUID()
	}
	if payload == nil {
		payload = map[string]any{}
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
	vmc := &vmController{
		ctx:            vmCtx,
		cancel:         cancel,
		consoleSession: consoleSession,
		uid:            uid,
		startedAt:      time.Now(),
		memMiB:         asInt(payload, "mem", 1024),
		numCPU:         asInt(payload, "cpu", 1),
		firmware:       asString(payload, "firmware", ""),
		kernel:         asString(payload, "kernel", "bzImage"),
		initrd:         asString(payload, "initrd", ""),
		iso:            asString(payload, "iso", ""),
		cmdline:        asString(payload, "cmdline", "console=hvc0 reboot=t"),
		tap:            asString(payload, "tap", ""),
		nat:            asBool(payload, "nat", false),
		block:          asStringSlice(payload, "block"),
	}
	consoleSession.SetOnClose(func() {
		logVM(uid, "console", "session closed; requesting shutdown")
		if err := vmc.Shutdown(); err != nil && !errors.Is(err, vmm.ErrVMClosed) {
			fmt.Fprintf(os.Stderr, "kube-vm: shutdown vm %s on console close: %v\n", uid, err)
		}
	})
	payload["uid"] = uid
	logVM(uid, "start", "building VM configuration")
	cfg, netCfg, err := vmconfig.Build(vmconfig.Options{
		UID:          uid,
		FirmwarePath: asString(payload, "firmware", ""),
		KernelPath:   asString(payload, "kernel", "bzImage"),
		InitrdPath:   asString(payload, "initrd", ""),
		BlockSpecs:   asStringSlice(payload, "block"),
		ISOPath:      asString(payload, "iso", ""),
		Cmdline:      asString(payload, "cmdline", "console=hvc0 reboot=t"),
		MemMiB:       asInt(payload, "mem", 1024),
		NumCPU:       asInt(payload, "cpu", 1),
		TapName:      asString(payload, "tap", ""),
		NATMode:      asBool(payload, "nat", false),
		ConsoleIn:    consoleSession,
		ConsoleOut:   consoleSession,
	})
	if err != nil {
		logVM(uid, "start", "config build failed: %v", err)
		return "", err
	}
	if netCfg.TapName != "" {
		vmc.tap = netCfg.TapName
	}
	if netCfg.NAT {
		vmc.nat = true
		vmc.natSubnet = netCfg.NATSubnet
		vmc.natGateway = netCfg.NATGateway
		vmc.natGuestIP = netCfg.NATGuestIP
	}
	if len(netCfg.NICs) > 0 {
		vmc.nics = append(vmc.nics[:0], netCfg.NICs...)
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
			vmc.setLastError(err)
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
	startedAt      time.Time
	memMiB         int
	numCPU         int
	firmware       string
	kernel         string
	initrd         string
	iso            string
	cmdline        string
	tap            string
	nat            bool
	natSubnet      string
	natGateway     string
	natGuestIP     string
	nics           []vmnet.NICStatsSource
	block          []string
	lastError      string
	hostCPUSec     float64
	hostCPUPercent float64
	lastCPUAt      time.Time
	lastCPUSec     float64
	consoleSession *control.ConsoleSession
	vm             *vmm.VM
	shuttingDown   bool
}

type vmStats struct {
	UID          string     `json:"uid"`
	State        string     `json:"state"`
	StartedAt    string     `json:"started_at,omitempty"`
	UptimeSec    int64      `json:"uptime_sec,omitempty"`
	MemMiB       int        `json:"mem_mib,omitempty"`
	NumCPU       int        `json:"num_cpu,omitempty"`
	Firmware     string     `json:"firmware,omitempty"`
	Kernel       string     `json:"kernel,omitempty"`
	Initrd       string     `json:"initrd,omitempty"`
	ISO          string     `json:"iso,omitempty"`
	Cmdline      string     `json:"cmdline,omitempty"`
	Tap          string     `json:"tap,omitempty"`
	NAT          bool       `json:"nat,omitempty"`
	NATSubnet    string     `json:"nat_subnet,omitempty"`
	NATGateway   string     `json:"nat_gateway,omitempty"`
	NATGuestIP   string     `json:"nat_guest_ip,omitempty"`
	NICs         []nicStats `json:"nics,omitempty"`
	Block        []string   `json:"block,omitempty"`
	HostMemBytes uint64     `json:"host_mem_bytes,omitempty"`
	HostCPUSec   float64    `json:"host_cpu_sec,omitempty"`
	HostCPUPct   float64    `json:"host_cpu_pct,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
}

type nicStats struct {
	Name      string `json:"name"`
	Mode      string `json:"mode"`
	RXPackets uint64 `json:"rx_packets"`
	RXBytes   uint64 `json:"rx_bytes"`
	TXPackets uint64 `json:"tx_packets"`
	TXBytes   uint64 `json:"tx_bytes"`
}

type daemonStats struct {
	State          string `json:"state"`
	StartedAt      string `json:"started_at"`
	UptimeSec      int64  `json:"uptime_sec"`
	VMTotal        int    `json:"vm_total"`
	GoRoutines     int    `json:"go_routines"`
	HeapAllocBytes uint64 `json:"heap_alloc_bytes"`
	HeapObjects    uint64 `json:"heap_objects"`
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
		err = safeCloseVM(vm)
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
		c.lastError = err.Error()
		logVM(c.uid, "shutdown", "controller shutdown error: %v", err)
	}
	return err
}

func (c *vmController) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.vm == nil
}

func (c *vmController) setLastError(err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	c.lastError = err.Error()
	c.mu.Unlock()
}

func (c *vmController) snapshot(now time.Time) vmStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updateHostCPULocked(now)

	out := vmStats{
		UID:          c.uid,
		State:        "stopped",
		MemMiB:       c.memMiB,
		NumCPU:       c.numCPU,
		Firmware:     c.firmware,
		Kernel:       c.kernel,
		Initrd:       c.initrd,
		ISO:          c.iso,
		Cmdline:      c.cmdline,
		Tap:          c.tap,
		NAT:          c.nat,
		NATSubnet:    c.natSubnet,
		NATGateway:   c.natGateway,
		NATGuestIP:   c.natGuestIP,
		HostMemBytes: uint64(c.memMiB) << 20,
		HostCPUSec:   c.hostCPUSec,
		HostCPUPct:   c.hostCPUPercent,
		LastError:    c.lastError,
	}
	if len(c.block) > 0 {
		out.Block = append([]string(nil), c.block...)
	}
	if len(c.nics) > 0 {
		out.NICs = make([]nicStats, 0, len(c.nics))
		for _, nic := range c.nics {
			if nic == nil {
				continue
			}
			snap := nic.Snapshot()
			out.NICs = append(out.NICs, nicStats{
				Name:      snap.Name,
				Mode:      snap.Mode,
				RXPackets: snap.RXPackets,
				RXBytes:   snap.RXBytes,
				TXPackets: snap.TXPackets,
				TXBytes:   snap.TXBytes,
			})
		}
	}
	if c.vm != nil {
		out.State = "running"
	}
	if !c.startedAt.IsZero() {
		out.StartedAt = c.startedAt.Format(time.RFC3339)
		out.UptimeSec = int64(now.Sub(c.startedAt).Seconds())
	}
	return out
}

func (c *vmController) updateHostCPULocked(now time.Time) {
	if c.vm == nil {
		c.hostCPUPercent = 0
		return
	}

	total := 0.0
	for _, tid := range c.vm.ThreadIDs() {
		sec, err := hoststats.ReadTaskCPUSec(tid)
		if err != nil {
			continue
		}
		total += sec
	}

	c.hostCPUSec = total
	if !c.lastCPUAt.IsZero() {
		dt := now.Sub(c.lastCPUAt).Seconds()
		if dt > 0 {
			dcpu := total - c.lastCPUSec
			if dcpu < 0 {
				dcpu = 0
			}
			c.hostCPUPercent = (dcpu / dt) * 100
		}
	}
	c.lastCPUSec = total
	c.lastCPUAt = now
}

func (s *vmServer) vmStats() []vmStats {
	s.mu.Lock()
	vms := make([]*vmController, 0, len(s.vms))
	for _, vm := range s.vms {
		if vm != nil {
			vms = append(vms, vm)
		}
	}
	s.mu.Unlock()

	now := time.Now()
	stats := make([]vmStats, 0, len(vms))
	for _, vm := range vms {
		stats = append(stats, vm.snapshot(now))
	}
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].UID < stats[j].UID
	})
	return stats
}

func (s *vmServer) daemonStats() daemonStats {
	s.mu.Lock()
	count := len(s.vms)
	startedAt := s.startedAt
	s.mu.Unlock()

	snap := hoststats.CaptureDaemonSnapshot(s.Status(""), startedAt, count)
	return daemonStats{
		State:          snap.State,
		StartedAt:      snap.StartedAt,
		UptimeSec:      snap.UptimeSec,
		VMTotal:        snap.VMTotal,
		GoRoutines:     snap.GoRoutines,
		HeapAllocBytes: snap.HeapAllocBytes,
		HeapObjects:    snap.HeapObjects,
	}
}

func (s *vmServer) defaultStartPayload() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]any, len(s.defaultStart))
	for k, v := range s.defaultStart {
		if blocks, ok := v.([]string); ok {
			cp := make([]string, len(blocks))
			copy(cp, blocks)
			out[k] = cp
			continue
		}
		out[k] = v
	}
	return out
}

func (s *vmServer) consoleSocket(uid string) (string, error) {
	s.mu.Lock()
	vm, ok := s.vms[uid]
	s.mu.Unlock()
	if !ok || vm == nil || vm.vm == nil {
		return "", errors.New("vm: instance is not running")
	}
	return s.socketPath + "." + uid + ".console", nil
}

func safeCloseVM(vm *vmm.VM) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic during vmm.Close: %v", r)
			fmt.Fprintf(os.Stderr, "kube-vm: panic during vmm.Close: %v\n%s", r, debug.Stack())
		}
	}()
	return vm.Close()
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

func asBool(payload map[string]any, key string, def bool) bool {
	if payload == nil {
		return def
	}
	v, ok := payload[key]
	if !ok || v == nil {
		return def
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		norm := strings.ToLower(strings.TrimSpace(val))
		switch norm {
		case "1", "true", "t", "yes", "y", "on":
			return true
		case "0", "false", "f", "no", "n", "off", "":
			return false
		default:
			return def
		}
	case int:
		return val != 0
	case int32:
		return val != 0
	case int64:
		return val != 0
	case float64:
		return val != 0
	default:
		return def
	}
}

func asStringSlice(payload map[string]any, key string) []string {
	if payload == nil {
		return nil
	}
	v, ok := payload[key]
	if !ok || v == nil {
		return nil
	}

	if ss, ok := v.([]string); ok {
		out := make([]string, 0, len(ss))
		for _, s := range ss {
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	}

	if aa, ok := v.([]any); ok {
		out := make([]string, 0, len(aa))
		for _, item := range aa {
			s, ok := item.(string)
			if !ok || s == "" {
				continue
			}
			out = append(out, s)
		}
		return out
	}

	if s, ok := v.(string); ok && s != "" {
		return []string{s}
	}

	return nil
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
