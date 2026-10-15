package vmconfig

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"kube-vm/os/firmware"
	"kube-vm/os/linux"
	"kube-vm/virtio"
	"kube-vm/vmm"
	"kube-vm/vmnet"
)

// Options describes a VM build request independent of its caller (CLI/daemon/web).
type Options struct {
	UID           string
	FirmwarePath  string
	KernelPath    string
	InitrdPath    string
	BlockSpecs    []string
	ISOPath       string
	Cmdline       string
	SerialConsole bool
	MemMiB        int
	NumCPU        int
	TapName       string
	NATMode       bool
	VhostNet      bool
	VSOCKCID      uint64
	VSOCKPort     uint32
	ConsoleIn     io.Reader
	ConsoleOut    io.Writer
}

const (
	DefaultVirtioConsoleCmdline = "console=hvc0 root=/dev/vda reboot=t"
	DefaultSerialConsoleCmdline = "console=ttyS0 root=/dev/vda reboot=t"
)

// Build creates a vmm.Config and optional network metadata from a normalized options set.
func Build(opts Options) (vmm.Config, vmnet.Config, error) {
	kernel := opts.KernelPath
	if kernel == "" {
		kernel = "bzImage"
	}
	cmdline := normalizeConsoleCmdline(opts.Cmdline, opts.SerialConsole)
	if opts.NATMode {
		cmdline = ensureNATCmdline(cmdline)
	}
	memMiB := opts.MemMiB
	if memMiB <= 0 {
		memMiB = 1024
	}
	numCPU := opts.NumCPU
	if numCPU <= 0 {
		numCPU = 1
	}
	snapshotKernel := opts.KernelPath
	if snapshotKernel == "" && opts.FirmwarePath == "" {
		snapshotKernel = kernel
	}

	var loader vmm.Loader
	if opts.FirmwarePath != "" {
		fw, err := readURL(opts.FirmwarePath)
		if err != nil {
			return vmm.Config{}, vmnet.Config{}, err
		}
		loader = &firmware.Loader{Firmware: fw}
	} else {
		bzImage, err := readURL(kernel)
		if err != nil {
			return vmm.Config{}, vmnet.Config{}, err
		}

		ll := &linux.Loader{Kernel: bzImage, Cmdline: cmdline}
		if opts.InitrdPath != "" {
			initrdBytes, err := readURL(opts.InitrdPath)
			if err != nil {
				return vmm.Config{}, vmnet.Config{}, err
			}
			ll.Initrd = initrdBytes
		}
		loader = ll
	}

	cfg := vmm.Config{
		MemSize:   memMiB << 20,
		NumCPU:    numCPU,
		Loader:    loader,
		SerialIn:  opts.ConsoleIn,
		SerialOut: opts.ConsoleOut,
		SnapshotConfig: vmm.SnapshotConfig{
			MemSize:       memMiB << 20,
			NumCPU:        numCPU,
			FirmwarePath:  opts.FirmwarePath,
			KernelPath:    snapshotKernel,
			InitrdPath:    opts.InitrdPath,
			ISOPath:       opts.ISOPath,
			Cmdline:       cmdline,
			TapName:       opts.TapName,
			NATMode:       opts.NATMode,
			VhostNet:      opts.VhostNet,
			SerialConsole: opts.SerialConsole,
			VSOCKCID:      opts.VSOCKCID,
			VSOCKPort:     opts.VSOCKPort,
			BlockSpecs:    append([]string(nil), opts.BlockSpecs...),
		},
	}
	if !opts.SerialConsole {
		cfg.SerialIn = nil
		cfg.SerialOut = nil
	}

	for _, s := range opts.BlockSpecs {
		spec, ro := strings.CutSuffix(s, ":ro")
		bd, err := blockDeviceFromSpec(spec, ro)
		if err != nil {
			return vmm.Config{}, vmnet.Config{}, err
		}
		cfg.Devices = append(cfg.Devices, bd)
	}

	if opts.ISOPath != "" {
		isoDev, err := blockDeviceFromSpec(opts.ISOPath, true)
		if err != nil {
			return vmm.Config{}, vmnet.Config{}, err
		}
		cfg.Devices = append(cfg.Devices, isoDev)
	}

	netCfg, err := vmnet.AttachDevice(&cfg, opts.UID, opts.TapName, opts.NATMode, opts.VhostNet)
	if err != nil {
		return vmm.Config{}, vmnet.Config{}, err
	}

	if opts.VSOCKPort != 0 {
		cid := opts.VSOCKCID
		if cid == 0 {
			cid = 3
		}
		if cid <= 2 {
			return vmm.Config{}, vmnet.Config{}, fmt.Errorf("kube-vm: virtio-vsock guest CID must be > 2")
		}
		backend, err := virtio.OpenVSOCK(opts.VSOCKPort)
		if err != nil {
			return vmm.Config{}, vmnet.Config{}, err
		}
		cfg.Devices = append(cfg.Devices, &virtio.VSockDevice{GuestCID: cid, Backend: backend})
	}

	if opts.ConsoleIn != nil && opts.ConsoleOut != nil {
		cfg.Devices = append(cfg.Devices, &virtio.ConsoleDevice{In: opts.ConsoleIn, Out: opts.ConsoleOut})
	}

	return cfg, netCfg, nil
}

func normalizeConsoleCmdline(cmdline string, serialConsole bool) string {
	if cmdline == "" {
		if serialConsole {
			return DefaultSerialConsoleCmdline
		}
		return DefaultVirtioConsoleCmdline
	}
	if serialConsole && cmdline == DefaultVirtioConsoleCmdline {
		return DefaultSerialConsoleCmdline
	}
	return cmdline
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
			return nil, err
		}
		if res.StatusCode != http.StatusOK {
			defer res.Body.Close()
			return nil, fmt.Errorf("response status %d != %d", res.StatusCode, http.StatusOK)
		}
		defer res.Body.Close()
		return io.ReadAll(res.Body)
	default:
		return nil, fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
}

func ensureParentDir(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if dir == "." || dir == "" || dir == string(filepath.Separator) {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory %q: %w", dir, err)
	}
	return nil
}

func blockDeviceFromSpec(spec string, readOnly bool) (*virtio.BlockDevice, error) {
	u, err := url.Parse(spec)
	if err != nil {
		return nil, fmt.Errorf("kube-vm: parse block device %q: %w", spec, err)
	}

	overlayPath := u.Query().Get("overlay")
	basePath := localBlockPath(u)
	if u.Scheme == "cow" {
		if readOnly {
			return nil, fmt.Errorf("kube-vm: block device %q cannot use copy-on-write mode in read-only mode", spec)
		}
		if overlayPath == "" {
			return nil, fmt.Errorf("kube-vm: block device %q is missing overlay= for cow: storage", spec)
		}
		if basePath == "" {
			return nil, fmt.Errorf("kube-vm: block device %q is missing a base path for cow: storage", spec)
		}
	}
	if overlayPath != "" && readOnly {
		return nil, fmt.Errorf("kube-vm: block device %q cannot use overlay in read-only mode", spec)
	}

	var stg virtio.BlockStorage
	ro := readOnly

	if err := ensureParentDir(basePath); err != nil {
		return nil, err
	}
	if err := ensureParentDir(overlayPath); err != nil {
		return nil, err
	}

	switch u.Scheme {
	case "cow":
		f, err := os.OpenFile(basePath, os.O_RDONLY, 0)
		if err != nil {
			return nil, fmt.Errorf("kube-vm: open COW base file %q: %w", basePath, err)
		}
		base := &virtio.FileStorage{File: f}
		overlayFile, err := os.OpenFile(overlayPath, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			return nil, fmt.Errorf("kube-vm: open block overlay %q: %w", overlayPath, err)
		}
		stg, err = virtio.OpenCOWStorage(base, overlayFile)
		if err != nil {
			return nil, fmt.Errorf("kube-vm: open block overlay %q: %w", overlayPath, err)
		}
	case "file", "":
		flags := os.O_RDWR
		if ro || overlayPath != "" {
			flags = os.O_RDONLY
		}
		f, err := os.OpenFile(basePath, flags, 0)
		if err != nil {
			return nil, fmt.Errorf("kube-vm: open block file %q: %w", basePath, err)
		}
		base := &virtio.FileStorage{File: f}
		if overlayPath == "" {
			stg = base
			break
		}
		overlayFile, err := os.OpenFile(overlayPath, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			return nil, fmt.Errorf("kube-vm: open block overlay %q: %w", overlayPath, err)
		}
		stg, err = virtio.OpenCOWStorage(base, overlayFile)
		if err != nil {
			return nil, fmt.Errorf("kube-vm: open block overlay %q: %w", overlayPath, err)
		}
	case "http", "https":
		if overlayPath != "" {
			return nil, fmt.Errorf("kube-vm: block device %q uses unsupported overlay query on remote URLs", spec)
		}
		ro = true
		stg = &virtio.HTTPStorage{URL: u.String()}
	case "mem":
		if overlayPath != "" {
			return nil, fmt.Errorf("kube-vm: block device %q uses unsupported overlay query on mem storage", spec)
		}
		sz, err := strconv.ParseInt(u.Opaque, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("kube-vm: parse mem block size %q: %w", u.Opaque, err)
		}
		stg = &virtio.MemStorage{Bytes: make([]byte, sz)}
	default:
		return nil, fmt.Errorf("kube-vm: unsupported block storage scheme %q", u.Scheme)
	}

	size, err := stg.Size()
	if err != nil {
		return nil, fmt.Errorf("kube-vm: block capacity %q: %w", spec, err)
	}
	if size < 0 {
		return nil, fmt.Errorf("kube-vm: negative block capacity for %q", spec)
	}
	return &virtio.BlockDevice{
		Storage: stg, ReadOnly: ro,
		Metrics: virtio.NewBlockMetrics(spec, uint64(size), ro),
	}, nil
}

func localBlockPath(u *url.URL) string {
	if u == nil {
		return ""
	}
	if u.Path != "" {
		return u.Path
	}
	return u.Opaque
}

func ensureNATCmdline(cmdline string) string {
	base := strings.TrimSpace(cmdline)
	if base == "" {
		return "ip=dhcp"
	}
	parts := strings.Fields(base)
	fixed := make([]string, 0, len(parts)+1)
	hasIP := false
	hasDHCPFlag := false
	for _, p := range parts {
		if strings.HasPrefix(p, "ip=") {
			fixed = append(fixed, "ip=dhcp")
			hasIP = true
			continue
		}
		if strings.HasPrefix(p, "dhcp=") {
			fixed = append(fixed, "dhcp=true")
			hasDHCPFlag = true
			continue
		}
		if p == "dhcp=true" {
			hasDHCPFlag = true
		}
		fixed = append(fixed, p)
	}
	if !hasIP {
		fixed = append(fixed, "ip=dhcp")
	}
	if !hasDHCPFlag {
		fixed = append(fixed, "dhcp=true")
	}
	return strings.Join(fixed, " ")
}
