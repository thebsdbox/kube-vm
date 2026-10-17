package kubevm

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"kube-vm/control"
	"kube-vm/hoststats"
	"kube-vm/virtio"
	"kube-vm/vmconfig"
	"kube-vm/vmm"
	"kube-vm/vmnet"
	"kube-vm/webui"

	"golang.org/x/term"
)

type Options struct {
	MemSize       int
	NumCPU        int
	FirmwarePath  string
	KernelPath    string
	InitrdPath    string
	ISOPath       string
	Cmdline       string
	SerialConsole bool
	TapName       string
	NATMode       bool
	VhostNet      bool
	VSOCKCID      uint64
	VSOCKPort     uint32
	SocketPath    string
	WebHost       string
	WebPort       int
	WebToken      string
	ServerMode    bool
	BlockSpecs    []string
	SnapshotPath  string
}

func DefaultOptions() Options {
	return Options{
		MemSize:    1024,
		NumCPU:     1,
		KernelPath: "bzImage",
		Cmdline:    "console=hvc0 root=/dev/vda reboot=t",
		SocketPath: "/tmp/kube-vm/kube-vm.sock",
		WebHost:    "127.0.0.1",
		ServerMode: false,
	}
}

func Run(ctx context.Context, opts Options) error {
	if opts.NATMode && opts.TapName != "" {
		return fmt.Errorf("-nat and -tap are mutually exclusive")
	}
	if opts.VhostNet && opts.TapName == "" {
		return fmt.Errorf("-vhost-net requires -tap")
	}
	if opts.VhostNet && opts.NATMode {
		return fmt.Errorf("-vhost-net cannot be used with -nat")
	}

	if !opts.ServerMode {
		return runLocalVM(ctx, opts)
	}

	server := &vmServer{
		ctx:         ctx,
		socketPath:  opts.SocketPath,
		configsPath: filepath.Join(filepath.Dir(opts.SocketPath), "saved-configs.json"),
		startedAt:   time.Now(),
	}
	go func() {
		if err := control.Serve(ctx, opts.SocketPath, server); err != nil {
			fmt.Fprintf(os.Stderr, "kube-vm: control server: %v\n", err)
		}
	}()

	if opts.WebPort > 0 {
		server.defaultStart = map[string]any{
			"mem":            opts.MemSize,
			"cpu":            opts.NumCPU,
			"firmware":       opts.FirmwarePath,
			"kernel":         opts.KernelPath,
			"initrd":         opts.InitrdPath,
			"iso":            opts.ISOPath,
			"cmdline":        opts.Cmdline,
			"serial_console": opts.SerialConsole,
			"tap":            opts.TapName,
			"nat":            opts.NATMode,
			"vhost":          opts.VhostNet,
			"vsock":          opts.VSOCKPort,
			"vsock-cid":      opts.VSOCKCID,
		}
		if len(opts.BlockSpecs) > 0 {
			blocks := make([]string, len(opts.BlockSpecs))
			copy(blocks, opts.BlockSpecs)
			server.defaultStart["block"] = blocks
		}

		addr := fmt.Sprintf("%s:%d", opts.WebHost, opts.WebPort)
		go func() {
			fmt.Fprintf(os.Stderr, "kube-vm: web frontend listening on http://%s\n", addr)
			svc := webui.Service{
				Start:               server.Start,
				Stop:                server.Shutdown,
				Pause:               server.Pause,
				Resume:              server.Resume,
				Save:                server.Save,
				Restore:             server.Restore,
				CheckVM:             server.Connect,
				ListVMs:             func() any { return server.vmStats() },
				DaemonStats:         func() any { return server.daemonStats() },
				DefaultStartPayload: server.defaultStartPayload,
				GlobalConfigs:       server.defaultConfigs,
				BaseDisks:           server.listBaseDisks,
				SaveConfig:          server.saveConfig,
				DeleteConfig:        server.deleteConfig,
				ConsoleSocket:       server.consoleSocket,
			}
			if err := webui.Serve(ctx, addr, svc, opts.WebToken); err != nil {
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
	return nil
}

func runLocalVM(ctx context.Context, opts Options) error {
	cfg, netCfg, err := vmconfig.Build(vmconfig.Options{
		UID:           "local",
		SnapshotPath:  opts.SnapshotPath,
		FirmwarePath:  opts.FirmwarePath,
		KernelPath:    opts.KernelPath,
		InitrdPath:    opts.InitrdPath,
		BlockSpecs:    append([]string(nil), opts.BlockSpecs...),
		ISOPath:       opts.ISOPath,
		Cmdline:       opts.Cmdline,
		SerialConsole: opts.SerialConsole,
		MemMiB:        opts.MemSize,
		NumCPU:        opts.NumCPU,
		TapName:       opts.TapName,
		NATMode:       opts.NATMode,
		VhostNet:      opts.VhostNet,
		VSOCKCID:      opts.VSOCKCID,
		VSOCKPort:     opts.VSOCKPort,
		ConsoleIn:     os.Stdin,
		ConsoleOut:    os.Stdout,
	})
	if err != nil {
		return err
	}
	if netCfg.NAT {
		fmt.Fprintf(os.Stderr, "kube-vm: NAT enabled on %s (guest route via %s, guest IP hint %s)\n", netCfg.TapName, netCfg.NATGateway, netCfg.NATGuestIP)
	} else if netCfg.TapName != "" {
		if netCfg.VhostNet {
			fmt.Fprintf(os.Stderr, "kube-vm: using tap backend %s with vhost-net\n", netCfg.TapName)
		} else {
			fmt.Fprintf(os.Stderr, "kube-vm: using tap backend %s\n", netCfg.TapName)
		}
	}

	m, err := vmm.New(cfg)
	if err != nil {
		return err
	}
	defer m.Close()

	if term.IsTerminal(int(os.Stdin.Fd())) {
		old, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			return err
		}
		defer term.Restore(int(os.Stdin.Fd()), old)
	}

	err = m.Run(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

type vmServer struct {
	mu           sync.Mutex
	ctx          context.Context
	socketPath   string
	configsPath  string
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
	vms := make([]*vmController, 0, len(s.vms))
	for _, vm := range s.vms {
		if vm != nil {
			vms = append(vms, vm)
		}
	}
	s.mu.Unlock()

	now := time.Now()
	summaries := make([]control.VMSummary, 0, len(vms))
	for _, vm := range vms {
		snap := vm.snapshot(now)
		summaries = append(summaries, control.VMSummary{
			UID:       snap.UID,
			State:     snap.State,
			IPAddress: snap.NATGuestIP,
			UptimeSec: snap.UptimeSec,
			BaseDisk:  baseDiskFromBlockSpecs(snap.Block),
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].UID < summaries[j].UID
	})
	return summaries
}

// Prefer an overlay's base image; otherwise report the first file-backed disk.
func baseDiskFromBlockSpecs(blocks []string) string {
	var fallback string
	for _, spec := range blocks {
		u, err := url.Parse(strings.TrimSpace(spec))
		if err != nil {
			continue
		}
		path := u.Path
		if path == "" {
			path = u.Opaque
		}
		if u.Scheme == "cow" || u.Query().Get("overlay") != "" {
			if path != "" {
				return path
			}
		}
		if fallback == "" {
			switch u.Scheme {
			case "", "file":
				fallback = path
			case "http", "https":
				fallback = u.Redacted()
			}
		}
	}
	return fallback
}

func (s *vmServer) Examine(uid string) (control.VMDetails, error) {
	s.mu.Lock()
	vm, ok := s.vms[uid]
	s.mu.Unlock()
	if !ok || vm == nil {
		return control.VMDetails{}, errors.New("vm: instance is not running")
	}
	return vmDetailsFromSnapshot(vm.snapshot(time.Now())), nil
}

func (s *vmServer) Start(uid string, payload map[string]any) (string, error) {
	if uid == "" {
		uid = generateUID()
	}
	if payload == nil {
		payload = map[string]any{}
	}
	logVM(uid, "start", "request payload keys=%v", sortedPayloadKeys(payload))
	if asString(payload, "snapshot", "") == "" {
		if err := normalizeBaseDiskPayload(uid, payload); err != nil {
			logVM(uid, "start", "base disk normalization failed: %v", err)
			return "", err
		}
	}

	s.mu.Lock()
	if _, ok := s.vms[uid]; ok {
		s.mu.Unlock()
		logVM(uid, "start", "rejected: uid already exists")
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
		runDone:        make(chan struct{}),
		startedAt:      time.Now(),
		memMiB:         asInt(payload, "mem", 1024),
		numCPU:         asInt(payload, "cpu", 1),
		firmware:       asString(payload, "firmware", ""),
		kernel:         asString(payload, "kernel", "bzImage"),
		initrd:         asString(payload, "initrd", ""),
		iso:            asString(payload, "iso", ""),
		cmdline:        asString(payload, "cmdline", "console=hvc0 root=/dev/vda reboot=t"),
		serialConsole:  asBool(payload, "serial_console", false),
		tap:            asString(payload, "tap", ""),
		nat:            asBool(payload, "nat", false),
		vhost:          asBool(payload, "vhost", false),
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
		UID:           uid,
		SnapshotPath:  asString(payload, "snapshot", ""),
		FirmwarePath:  asString(payload, "firmware", ""),
		KernelPath:    asString(payload, "kernel", "bzImage"),
		InitrdPath:    asString(payload, "initrd", ""),
		BlockSpecs:    asStringSlice(payload, "block"),
		ISOPath:       asString(payload, "iso", ""),
		Cmdline:       asString(payload, "cmdline", "console=hvc0 root=/dev/vda reboot=t"),
		SerialConsole: asBool(payload, "serial_console", false),
		MemMiB:        asInt(payload, "mem", 1024),
		NumCPU:        asInt(payload, "cpu", 1),
		TapName:       asString(payload, "tap", ""),
		NATMode:       asBool(payload, "nat", false),
		VhostNet:      asBool(payload, "vhost", false),
		VSOCKCID:      uint64(asInt(payload, "vsock-cid", 0)),
		VSOCKPort:     uint32(asInt(payload, "vsock", 0)),
		ConsoleIn:     consoleSession,
		ConsoleOut:    consoleSession,
	})
	if err != nil {
		logVM(uid, "start", "config build failed: %v", err)
		cancel()
		consoleSession.SetOnClose(nil)
		_ = consoleSession.Close()
		return "", err
	}
	// Snapshot hardware and disk copies replace CLI/form defaults.
	vmc.memMiB, vmc.numCPU = cfg.MemSize>>20, cfg.NumCPU
	vmc.firmware, vmc.kernel, vmc.initrd = cfg.SnapshotConfig.FirmwarePath, cfg.SnapshotConfig.KernelPath, cfg.SnapshotConfig.InitrdPath
	vmc.iso, vmc.cmdline, vmc.serialConsole = cfg.SnapshotConfig.ISOPath, cfg.SnapshotConfig.Cmdline, cfg.SnapshotConfig.SerialConsole
	vmc.tap, vmc.nat, vmc.vhost = cfg.SnapshotConfig.TapName, cfg.SnapshotConfig.NATMode, cfg.SnapshotConfig.VhostNet
	vmc.block = append([]string(nil), cfg.SnapshotConfig.BlockSpecs...)
	if netCfg.TapName != "" {
		vmc.tap = netCfg.TapName
	}
	if netCfg.VhostNet {
		vmc.vhost = true
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
	for _, device := range cfg.Devices {
		if block, ok := device.(*virtio.BlockDevice); ok && block.Metrics != nil {
			vmc.storage = append(vmc.storage, block.Metrics)
		}
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
	consoleErrC := make(chan error, 1)
	go func() {
		logVM(uid, "console", "serving console socket %s", consolePath)
		if err := control.ServeConsole(vmCtx, consolePath, consoleSession); err != nil {
			select {
			case consoleErrC <- err:
			default:
			}
			fmt.Fprintf(os.Stderr, "kube-vm: console server %s: %v\n", consolePath, err)
		}
		logVM(uid, "console", "console server exited")
	}()
	if err := waitForConsoleSocket(vmCtx, consolePath, consoleErrC, 2*time.Second); err != nil {
		_ = vmc.Shutdown()
		return "", fmt.Errorf("vm: console startup failed: %w", err)
	}

	go func() {
		defer close(vmc.runDone)
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

func (s *vmServer) Pause(uid string) error {
	s.mu.Lock()
	vm, ok := s.vms[uid]
	s.mu.Unlock()
	if !ok || vm == nil || vm.vm == nil {
		return errors.New("vm: instance is not running")
	}
	return vm.vm.Pause()
}

func (s *vmServer) Resume(uid string) error {
	s.mu.Lock()
	vm, ok := s.vms[uid]
	s.mu.Unlock()
	if !ok || vm == nil || vm.vm == nil {
		return errors.New("vm: instance is not running")
	}
	return vm.vm.Resume()
}

func (s *vmServer) Save(uid, path string) error {
	s.mu.Lock()
	vm, ok := s.vms[uid]
	s.mu.Unlock()
	if !ok || vm == nil || vm.vm == nil {
		return errors.New("vm: instance is not running")
	}
	return vm.vm.SaveState(path)
}

func (s *vmServer) Restore(uid, path string) error {
	state, err := vmm.ReadSnapshot(path)
	if err != nil {
		return err
	}
	// Reject missing/corrupt/old checkpoints before stopping a running VM.
	if err := vmconfig.ValidateSnapshotAssets(path, state); err != nil {
		return err
	}
	if err := state.ValidateHost(); err != nil {
		return err
	}
	s.mu.Lock()
	vm, ok := s.vms[uid]
	s.mu.Unlock()
	if !ok || vm == nil || vm.vm == nil {
		return errors.New("vm: instance is not running")
	}
	if err := s.Shutdown(uid); err != nil {
		return fmt.Errorf("stop VM before checkpoint restore: %w", err)
	}
	if vm.runDone != nil {
		select {
		case <-vm.runDone:
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-time.After(10 * time.Second):
			return errors.New("timed out waiting for old VM cleanup; checkpoint is unchanged")
		}
	}
	_, err = s.Start(uid, map[string]any{"snapshot": path})
	return err
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

func sortedPayloadKeys(payload map[string]any) []string {
	keys := make([]string, 0, len(payload))
	for k := range payload {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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

func waitForConsoleSocket(ctx context.Context, socketPath string, errC <-chan error, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		if _, err := os.Stat(socketPath); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errC:
			if err != nil {
				return err
			}
		case <-deadline.C:
			return fmt.Errorf("timeout waiting for console socket %q", socketPath)
		case <-ticker.C:
		}
	}
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
	vhost          bool
	serialConsole  bool
	natSubnet      string
	natGateway     string
	natGuestIP     string
	nics           []vmnet.NICStatsSource
	storage        []*virtio.BlockMetrics
	block          []string
	lastError      string
	hostCPUSec     float64
	hostCPUPercent float64
	lastCPUAt      time.Time
	lastCPUSec     float64
	consoleSession *control.ConsoleSession
	vm             *vmm.VM
	shuttingDown   bool
	runDone        chan struct{}
}

type vmStats struct {
	UID          string              `json:"uid"`
	State        string              `json:"state"`
	StartedAt    string              `json:"started_at,omitempty"`
	UptimeSec    int64               `json:"uptime_sec,omitempty"`
	MemMiB       int                 `json:"mem_mib,omitempty"`
	NumCPU       int                 `json:"num_cpu,omitempty"`
	Firmware     string              `json:"firmware,omitempty"`
	Kernel       string              `json:"kernel,omitempty"`
	Initrd       string              `json:"initrd,omitempty"`
	ISO          string              `json:"iso,omitempty"`
	Cmdline      string              `json:"cmdline,omitempty"`
	Tap          string              `json:"tap,omitempty"`
	NAT          bool                `json:"nat,omitempty"`
	VhostNet     bool                `json:"vhost_net,omitempty"`
	NATSubnet    string              `json:"nat_subnet,omitempty"`
	NATGateway   string              `json:"nat_gateway,omitempty"`
	NATGuestIP   string              `json:"nat_guest_ip,omitempty"`
	NICs         []nicStats          `json:"nics,omitempty"`
	Block        []string            `json:"block,omitempty"`
	Storage      []virtio.BlockStats `json:"storage,omitempty"`
	HostMemBytes uint64              `json:"host_mem_bytes,omitempty"`
	HostCPUSec   float64             `json:"host_cpu_sec,omitempty"`
	HostCPUPct   float64             `json:"host_cpu_pct,omitempty"`
	LastError    string              `json:"last_error,omitempty"`
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

func vmDetailsFromSnapshot(snap vmStats) control.VMDetails {
	out := control.VMDetails{
		UID:          snap.UID,
		State:        snap.State,
		StartedAt:    snap.StartedAt,
		UptimeSec:    snap.UptimeSec,
		MemMiB:       snap.MemMiB,
		NumCPU:       snap.NumCPU,
		Firmware:     snap.Firmware,
		Kernel:       snap.Kernel,
		Initrd:       snap.Initrd,
		ISO:          snap.ISO,
		Cmdline:      snap.Cmdline,
		Tap:          snap.Tap,
		NAT:          snap.NAT,
		VhostNet:     snap.VhostNet,
		NATSubnet:    snap.NATSubnet,
		NATGateway:   snap.NATGateway,
		NATGuestIP:   snap.NATGuestIP,
		HostMemBytes: snap.HostMemBytes,
		HostCPUSec:   snap.HostCPUSec,
		HostCPUPct:   snap.HostCPUPct,
		LastError:    snap.LastError,
		Storage:      append([]virtio.BlockStats(nil), snap.Storage...),
	}
	if len(snap.Block) > 0 {
		out.Block = append([]string(nil), snap.Block...)
	}
	if len(snap.NICs) > 0 {
		out.NICs = make([]control.VMNIC, 0, len(snap.NICs))
		for _, nic := range snap.NICs {
			out.NICs = append(out.NICs, control.VMNIC{
				Name:      nic.Name,
				Mode:      nic.Mode,
				RXPackets: nic.RXPackets,
				RXBytes:   nic.RXBytes,
				TXPackets: nic.TXPackets,
				TXBytes:   nic.TXBytes,
			})
		}
	}
	return out
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
	if c.tap != "" {
		if tapErr := virtio.RemoveTAP(c.tap); tapErr != nil {
			if err == nil {
				err = tapErr
			} else {
				logVM(c.uid, "shutdown", "tap cleanup error: %v", tapErr)
			}
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
		VhostNet:     c.vhost,
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
	for _, disk := range c.storage {
		if disk != nil {
			out.Storage = append(out.Storage, disk.Snapshot())
		}
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

func (s *vmServer) loadSavedConfigs() []map[string]any {
	if s.configsPath == "" {
		return nil
	}
	data, err := os.ReadFile(s.configsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		fmt.Fprintf(os.Stderr, "kube-vm: config store read failed: %v\n", err)
		return nil
	}
	if len(data) == 0 {
		return nil
	}
	var items []map[string]any
	if err := json.Unmarshal(data, &items); err != nil {
		fmt.Fprintf(os.Stderr, "kube-vm: config store parse failed: %v\n", err)
		return nil
	}
	return items
}

func (s *vmServer) storeSavedConfigs(items []map[string]any) error {
	if s.configsPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.configsPath), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config store: %w", err)
	}
	if err := os.WriteFile(s.configsPath, data, 0o644); err != nil {
		return fmt.Errorf("write config store: %w", err)
	}
	return nil
}

func (s *vmServer) listBaseDisks() []string {
	disks, err := vmconfig.ListBaseDisks(vmconfig.DefaultBaseDiskDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kube-vm: list base disks: %v\n", err)
		return nil
	}
	return disks
}

func normalizeBaseDiskPayload(uid string, payload map[string]any) error {
	if payload == nil {
		return nil
	}
	if uid == "" {
		return nil
	}
	if baseDisk := strings.TrimSpace(asString(payload, "base_disk", "")); baseDisk != "" {
		spec, err := vmconfig.EncodeBaseDiskCOWSpec(baseDisk, uid)
		if err != nil {
			return fmt.Errorf("prepare base disk %q: %w", baseDisk, err)
		}
		blocks := asStringSlice(payload, "block")
		blocks = append(blocks, spec)
		payload["block"] = blocks
		delete(payload, "base_disk")
	}
	blocks := asStringSlice(payload, "block")
	if len(blocks) == 0 {
		return nil
	}
	filtered := make([]string, 0, len(blocks))
	for _, block := range blocks {
		candidate := strings.TrimSpace(block)
		if candidate == "" {
			continue
		}
		baseCandidate := strings.TrimSuffix(candidate, ":ro")
		if resolved, err := vmconfig.ResolveBaseDisk(baseCandidate); err == nil && resolved != "" {
			spec, err := vmconfig.EncodeBaseDiskCOWSpec(resolved, uid)
			if err != nil {
				return fmt.Errorf("prepare base disk %q: %w", candidate, err)
			}
			filtered = append(filtered, spec)
			continue
		}
		filtered = append(filtered, block)
	}
	payload["block"] = filtered
	return nil
}

func (s *vmServer) saveConfig(item map[string]any) (map[string]any, error) {
	if item == nil {
		return nil, fmt.Errorf("config payload is required")
	}
	name, _ := item["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("config name is required")
	}
	payload, ok := item["payload"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("config payload is required")
	}
	id := strings.TrimSpace(asString(item, "id", ""))
	if id == "" {
		id = fmt.Sprintf("saved-%d", time.Now().UnixNano())
	}
	newItem := map[string]any{
		"id":         id,
		"name":       name,
		"payload":    payload,
		"created_at": asString(item, "created_at", time.Now().UTC().Format(time.RFC3339)),
		"updated_at": time.Now().UTC().Format(time.RFC3339),
		"source":     "saved",
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	items := s.loadSavedConfigs()
	updated := false
	for i, cfg := range items {
		if cfg["id"] == id {
			items[i] = newItem
			updated = true
			break
		}
	}
	if !updated {
		items = append(items, newItem)
	}
	if err := s.storeSavedConfigs(items); err != nil {
		return nil, err
	}
	return newItem, nil
}

func (s *vmServer) deleteConfig(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := s.loadSavedConfigs()
	filtered := make([]map[string]any, 0, len(items))
	for _, cfg := range items {
		if asString(cfg, "id", "") != id {
			filtered = append(filtered, cfg)
		}
	}
	return s.storeSavedConfigs(filtered)
}

func (s *vmServer) defaultConfigs() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	configs := make([]map[string]any, 0, 4)
	if len(s.defaultStart) > 0 {
		payload := make(map[string]any, len(s.defaultStart))
		for k, v := range s.defaultStart {
			if blocks, ok := v.([]string); ok {
				cp := make([]string, len(blocks))
				copy(cp, blocks)
				payload[k] = cp
				continue
			}
			payload[k] = v
		}
		configs = append(configs, map[string]any{
			"id":      "global-default",
			"name":    "Global Default",
			"source":  "global",
			"payload": payload,
		})
	}
	for _, cfg := range s.loadSavedConfigs() {
		configs = append(configs, cfg)
	}
	return configs
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

	appendSplit := func(out []string, raw string) []string {
		for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == ',' }) {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
		return out
	}

	if ss, ok := v.([]string); ok {
		out := make([]string, 0, len(ss))
		for _, s := range ss {
			out = appendSplit(out, s)
		}
		return out
	}

	if aa, ok := v.([]any); ok {
		out := make([]string, 0, len(aa))
		for _, item := range aa {
			s, ok := item.(string)
			if !ok || strings.TrimSpace(s) == "" {
				continue
			}
			out = appendSplit(out, s)
		}
		return out
	}

	if s, ok := v.(string); ok {
		return appendSplit(nil, s)
	}

	return nil
}
