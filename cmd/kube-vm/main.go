package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"

	kubevm "kube-vm/kubevm"
	"kube-vm/vmconfig"

	"golang.org/x/sys/unix"
)

func main() {
	fmt.Println("kube-vm is starting")
	opts := kubevm.DefaultOptions()
	var blkdev flagStrings
	var cowblk flagStrings
	vsockPort := uint(opts.VSOCKPort)

	flag.IntVar(&opts.MemSize, "mem", opts.MemSize, "set the VM's memory size in MiB")
	flag.IntVar(&opts.NumCPU, "cpu", opts.NumCPU, "set the number of VCPUs")
	flag.StringVar(&opts.FirmwarePath, "firmware", opts.FirmwarePath, "boot guest from firmware image (BIOS/UEFI blob)")
	flag.StringVar(&opts.KernelPath, "kernel", opts.KernelPath, "load bzImage from file or URL")
	flag.StringVar(&opts.InitrdPath, "initrd", opts.InitrdPath, "load initial ramdisk from file or URL")
	flag.StringVar(&opts.ISOPath, "iso", opts.ISOPath, "attach ISO image from file or URL as read-only block device")
	flag.StringVar(&opts.SnapshotPath, "snapshot", "", "resume a saved checkpoint and its sibling disk directory (no kernel/initrd required)")
	flag.StringVar(&opts.Cmdline, "cmdline", opts.Cmdline, "set the kernel command line")
	flag.BoolVar(&opts.SerialConsole, "serial-console", opts.SerialConsole, "attach a COM1 serial console and switch the stock cmdline to ttyS0 unless --cmdline overrides it")
	flag.StringVar(&opts.TapName, "tap", opts.TapName, "attach a virtio-net device backed by TAP (optional name)")
	flag.BoolVar(&opts.NATMode, "nat", opts.NATMode, "attach a virtio-net device with host NAT (Linux only)")
	flag.BoolVar(&opts.VhostNet, "vhost-net", opts.VhostNet, "enable vhost-net acceleration for TAP networking (Linux only, requires -tap)")
	flag.Uint64Var(&opts.VSOCKCID, "vsock-cid", opts.VSOCKCID, "attach a virtio-vsock device with guest CID (0 disables, 1/2 reserved)")
	flag.UintVar(&vsockPort, "vsock", vsockPort, "attach a virtio-vsock device backed by a host AF_VSOCK listener on port (0 disables)")
	flag.StringVar(&opts.SocketPath, "socket", opts.SocketPath, "path to the kube-vm control socket")
	flag.StringVar(&opts.WebHost, "web-host", opts.WebHost, "host to bind the web frontend")
	flag.IntVar(&opts.WebPort, "web-port", opts.WebPort, "enable web frontend on this port (0 disables)")
	flag.StringVar(&opts.WebToken, "web-token", opts.WebToken, "optional bearer token required for web API access")
	flag.BoolVar(&opts.ServerMode, "server", opts.ServerMode, "run kube-vm in server mode and accept control requests over a UNIX socket")
	flag.Var(&blkdev, "block", "add a block device (multiple OK)")
	flag.Var(&cowblk, "cow-block", "add a writable copy-on-write block device as BASE=OVERLAY (multiple OK)")
	if err := flag.CommandLine.Parse(standaloneArgs(os.Args[1:])); err != nil {
		fmt.Fprintf(os.Stderr, "kube-vm: %v\n", err)
		os.Exit(2)
	}
	opts.VSOCKPort = uint32(vsockPort)
	blockSpecs, err := vmconfig.MergeBlockSpecs([]string(blkdev), []string(cowblk))
	if err != nil {
		fmt.Fprintf(os.Stderr, "kube-vm: %v\n", err)
		os.Exit(2)
	}
	opts.BlockSpecs = append(opts.BlockSpecs[:0], blockSpecs...)

	if err := os.MkdirAll(filepath.Dir(opts.SocketPath), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "kube-vm: failed to create runtime dir: %v\n", err)
		os.Exit(2)
	}

	setupLogging(filepath.Dir(opts.SocketPath))

	ctx, stop := signal.NotifyContext(context.Background(), unix.SIGINT, unix.SIGTERM)
	defer stop()

	if err := kubevm.Run(ctx, opts); err != nil {
		fmt.Fprintf(os.Stderr, "kube-vm: %v\n", err)
		os.Exit(1)
	}
}

type flagStrings []string

func (*flagStrings) String() string {
	return ""
}

func (fs *flagStrings) Set(s string) error {
	*fs = append(*fs, s)
	return nil
}

func standaloneArgs(args []string) []string {
	if len(args) == 0 {
		return args
	}
	switch args[0] {
	case "start", "run", "boot":
		return args[1:]
	default:
		return args
	}
}

func setupLogging(runtimeDir string) {
	logFile := filepath.Join(runtimeDir, "kube-vm.log")
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		slog.Error("failed to open log file", "path", logFile, "err", err)
		return
	}

	multiWriter := io.MultiWriter(os.Stderr, f)
	handler := slog.NewJSONHandler(multiWriter, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(handler))
}
