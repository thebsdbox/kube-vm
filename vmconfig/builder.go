package vmconfig

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
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
	UID          string
	FirmwarePath string
	KernelPath   string
	InitrdPath   string
	BlockSpecs   []string
	ISOPath      string
	Cmdline      string
	MemMiB       int
	NumCPU       int
	TapName      string
	NATMode      bool
	VhostNet     bool
	VSOCKCID     uint64
	VSOCKPort    uint32
	ConsoleIn    io.Reader
	ConsoleOut   io.Writer
}

// Build creates a vmm.Config and optional network metadata from a normalized options set.
func Build(opts Options) (vmm.Config, vmnet.Config, error) {
	kernel := opts.KernelPath
	if kernel == "" {
		kernel = "bzImage"
	}
	cmdline := opts.Cmdline
	if cmdline == "" {
		cmdline = "console=hvc0 reboot=t"
	}
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
		MemSize: memMiB << 20,
		NumCPU:  numCPU,
		Loader:  loader,
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

func blockDeviceFromSpec(spec string, readOnly bool) (*virtio.BlockDevice, error) {
	u, err := url.Parse(spec)
	if err != nil {
		return nil, fmt.Errorf("kube-vm: parse block device %q: %w", spec, err)
	}

	var stg virtio.BlockStorage
	ro := readOnly

	switch u.Scheme {
	case "file", "":
		flags := os.O_RDWR
		if ro {
			flags = os.O_RDONLY
		}
		f, err := os.OpenFile(u.Path, flags, 0)
		if err != nil {
			return nil, fmt.Errorf("kube-vm: open block file %q: %w", u.Path, err)
		}
		stg = &virtio.FileStorage{File: f}
	case "http", "https":
		ro = true
		stg = &virtio.HTTPStorage{URL: u.String()}
	case "mem":
		sz, err := strconv.ParseInt(u.Opaque, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("kube-vm: parse mem block size %q: %w", u.Opaque, err)
		}
		stg = &virtio.MemStorage{Bytes: make([]byte, sz)}
	default:
		return nil, fmt.Errorf("kube-vm: unsupported block storage scheme %q", u.Scheme)
	}

	return &virtio.BlockDevice{Storage: stg, ReadOnly: ro}, nil
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
