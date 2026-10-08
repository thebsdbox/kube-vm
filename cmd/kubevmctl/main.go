package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"kube-vm/control"
	"kube-vm/vmconfig"
	"kube-vm/vmm"
)

const consoleDetachByte = 0x1d // Ctrl+]

var errConsoleDetach = errors.New("kubevmctl: console detached")

var (
	socketPath string
	uid        string
	detach     bool
	firmware   string
	kernel     string
	initrd     string
	iso        string
	cmdline    string
	memSize    int
	numCPU     int
	snapshot   string
	tapName    string
	natMode    bool
	vhostNet   bool
	blkdev     flagStrings
	cowblk     flagStrings
)

func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "kubevmctl",
		Short: "Control kube-vm VMs over the control socket",
		Example: strings.TrimSpace(`
  kubevmctl list
  kubevmctl start --uid vm-123 --kernel bzImage --initrd initrd.cpio.gz
  kubevmctl pause --uid vm-123
  kubevmctl console --uid vm-123
`),
		SilenceUsage: true,
	}

	rootCmd.PersistentFlags().StringVarP(&socketPath, "socket", "s", defaultSocketPath(), "path to the kube-vm control socket")
	rootCmd.PersistentFlags().StringVarP(&uid, "uid", "u", "", "VM UID to target")

	rootCmd.AddCommand(newListCmd())
	rootCmd.AddCommand(newExamineCmd())
	rootCmd.AddCommand(newStatusCmd())
	rootCmd.AddCommand(newStartCmd())
	rootCmd.AddCommand(newPauseCmd())
	rootCmd.AddCommand(newResumeCmd())
	rootCmd.AddCommand(newConsoleCmd())
	rootCmd.AddCommand(newStopCmd())
	rootCmd.AddCommand(newSaveCmd())
	rootCmd.AddCommand(newRestoreCmd())
	rootCmd.AddCommand(newSnapshotCmd())

	return rootCmd
}

func newSnapshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Inspect local VM snapshot files",
	}
	cmd.AddCommand(newSnapshotInspectCmd())
	return cmd
}

func newSnapshotInspectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect <path>",
		Short: "Print snapshot metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			info, err := os.Stat(args[0])
			if err != nil {
				return err
			}
			snap, err := vmm.ReadSnapshot(args[0])
			if err != nil {
				return err
			}
			printSnapshotSummary(cmd.OutOrStdout(), args[0], info.Size(), snap)
			return nil
		},
	}
	return cmd
}

func printSnapshotSummary(w io.Writer, path string, fileSize int64, snap *vmm.VMSnapshot) {
	if snap == nil {
		return
	}
	cfg := snap.Config
	fmt.Fprintf(w, "path=%s\n", path)
	fmt.Fprintf(w, "magic=%s\n", snap.Magic)
	fmt.Fprintf(w, "version=%d\n", snap.Version)
	fmt.Fprintf(w, "file_bytes=%d\n", fileSize)
	if !snap.CreatedAt.IsZero() {
		fmt.Fprintf(w, "created_at=%s\n", snap.CreatedAt.Format(time.RFC3339))
	}
	fmt.Fprintf(w, "memory_bytes=%d\n", len(snap.Mem))
	fmt.Fprintf(w, "cpus=%d\n", len(snap.CPU))
	fmt.Fprintf(w, "kernel=%s\n", cfg.KernelPath)
	fmt.Fprintf(w, "initrd=%s\n", cfg.InitrdPath)
	fmt.Fprintf(w, "firmware=%s\n", cfg.FirmwarePath)
	fmt.Fprintf(w, "iso=%s\n", cfg.ISOPath)
	fmt.Fprintf(w, "cmdline=%s\n", cfg.Cmdline)
	fmt.Fprintf(w, "tap=%s\n", cfg.TapName)
	fmt.Fprintf(w, "nat=%t\n", cfg.NATMode)
	fmt.Fprintf(w, "vhost=%t\n", cfg.VhostNet)
	fmt.Fprintf(w, "vsock_cid=%d\n", cfg.VSOCKCID)
	fmt.Fprintf(w, "vsock_port=%d\n", cfg.VSOCKPort)
	for i, block := range cfg.BlockSpecs {
		fmt.Fprintf(w, "block[%d]=%s\n", i, block)
	}
}

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List running VMs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := control.Dial(socketPath, "list", nil)
			if err != nil {
				return err
			}
			if !resp.OK {
				if resp.Error != "" {
					return fmt.Errorf("%s", resp.Error)
				}
				return fmt.Errorf("control request failed")
			}
			if len(resp.VMs) == 0 {
				fmt.Println("no running VMs")
				return nil
			}
			fmt.Println("UID\tSTATE\tIP")
			for _, vm := range resp.VMs {
				ip := vm.IPAddress
				if ip == "" {
					ip = "-"
				}
				fmt.Printf("%s\t%s\t%s\n", vm.UID, vm.State, ip)
			}
			return nil
		},
	}
	return cmd
}

func newExamineCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "examine [uid]",
		Short: "Show detailed information about a VM",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				uid = args[0]
			}
			if uid == "" {
				return fmt.Errorf("uid is required")
			}
			resp, err := control.Dial(socketPath, "examine", map[string]any{"uid": uid})
			if err != nil {
				return err
			}
			if !resp.OK {
				if resp.Error != "" {
					return fmt.Errorf("%s", resp.Error)
				}
				return fmt.Errorf("control request failed")
			}
			if resp.VM == nil {
				return fmt.Errorf("control request returned no VM details")
			}
			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(resp.VM)
			}
			printVMDetails(cmd.OutOrStdout(), *resp.VM)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "emit VM details as JSON")
	return cmd
}

func printVMDetails(w io.Writer, vm control.VMDetails) {
	fmt.Fprintf(w, "uid=%s\n", vm.UID)
	fmt.Fprintf(w, "state=%s\n", vm.State)
	if vm.StartedAt != "" {
		fmt.Fprintf(w, "started_at=%s\n", vm.StartedAt)
	}
	if vm.UptimeSec > 0 {
		fmt.Fprintf(w, "uptime_sec=%d\n", vm.UptimeSec)
	}
	fmt.Fprintf(w, "mem_mib=%d\n", vm.MemMiB)
	fmt.Fprintf(w, "num_cpu=%d\n", vm.NumCPU)
	fmt.Fprintf(w, "firmware=%s\n", vm.Firmware)
	fmt.Fprintf(w, "kernel=%s\n", vm.Kernel)
	fmt.Fprintf(w, "initrd=%s\n", vm.Initrd)
	fmt.Fprintf(w, "iso=%s\n", vm.ISO)
	fmt.Fprintf(w, "cmdline=%s\n", vm.Cmdline)
	fmt.Fprintf(w, "tap=%s\n", vm.Tap)
	fmt.Fprintf(w, "nat=%t\n", vm.NAT)
	fmt.Fprintf(w, "vhost_net=%t\n", vm.VhostNet)
	fmt.Fprintf(w, "nat_subnet=%s\n", vm.NATSubnet)
	fmt.Fprintf(w, "nat_gateway=%s\n", vm.NATGateway)
	fmt.Fprintf(w, "nat_guest_ip=%s\n", vm.NATGuestIP)
	fmt.Fprintf(w, "host_mem_bytes=%d\n", vm.HostMemBytes)
	fmt.Fprintf(w, "host_cpu_sec=%.6f\n", vm.HostCPUSec)
	fmt.Fprintf(w, "host_cpu_pct=%.2f\n", vm.HostCPUPct)
	fmt.Fprintf(w, "last_error=%s\n", vm.LastError)
	for i, block := range vm.Block {
		fmt.Fprintf(w, "block[%d]=%s\n", i, block)
	}
	for i, nic := range vm.NICs {
		fmt.Fprintf(w, "nic[%d].name=%s\n", i, nic.Name)
		fmt.Fprintf(w, "nic[%d].mode=%s\n", i, nic.Mode)
		fmt.Fprintf(w, "nic[%d].rx_packets=%d\n", i, nic.RXPackets)
		fmt.Fprintf(w, "nic[%d].rx_bytes=%d\n", i, nic.RXBytes)
		fmt.Fprintf(w, "nic[%d].tx_packets=%d\n", i, nic.TXPackets)
		fmt.Fprintf(w, "nic[%d].tx_bytes=%d\n", i, nic.TXBytes)
	}
}

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status [uid]",
		Short: "Get the status for a VM",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				uid = args[0]
			}
			resp, err := control.Dial(socketPath, "status", map[string]any{"uid": uid})
			if err != nil {
				return err
			}
			if !resp.OK {
				if resp.Error != "" {
					return fmt.Errorf("%s", resp.Error)
				}
				return fmt.Errorf("control request failed")
			}
			fmt.Printf("uid=%s\nstate=%s\n", resp.UID, resp.State)
			return nil
		},
	}
	return cmd
}

func newStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start [uid]",
		Short: "Start a VM",
		Args:  cobra.MaximumNArgs(1),
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if natMode && tapName != "" {
				return fmt.Errorf("-nat and -tap are mutually exclusive")
			}
			if vhostNet && tapName == "" {
				return fmt.Errorf("-vhost-net requires -tap")
			}
			if vhostNet && natMode {
				return fmt.Errorf("-vhost-net cannot be used with -nat")
			}
			if len(args) > 0 {
				uid = args[0]
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			blockSpecs, err := vmconfig.MergeBlockSpecs([]string(blkdev), []string(cowblk))
			if err != nil {
				return err
			}
			payload := map[string]any{
				"firmware": firmware,
				"kernel":   kernel,
				"initrd":   initrd,
				"block":    blockSpecs,
				"iso":      iso,
				"cmdline":  cmdline,
				"mem":      memSize,
				"cpu":      numCPU,
				"tap":      tapName,
				"nat":      natMode,
				"vhost":    vhostNet,
			}
			if snapshot != "" {
				payload["snapshot"] = snapshot
			}
			if uid != "" {
				payload["uid"] = uid
			}
			resp, err := control.Dial(socketPath, "start", payload)
			if err != nil {
				return err
			}
			if !resp.OK {
				if resp.Error != "" {
					return fmt.Errorf("%s", resp.Error)
				}
				return fmt.Errorf("control request failed")
			}
			fmt.Printf("uid=%s\n", resp.UID)
			if !detach {
				return attachConsole(socketPath, resp.UID)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&uid, "uid", "u", uid, "VM UID to target")
	cmd.Flags().StringVar(&firmware, "firmware", "", "path to guest firmware image (BIOS/UEFI blob)")
	cmd.Flags().StringVar(&kernel, "kernel", "bzImage", "path to the guest kernel or URL")
	cmd.Flags().StringVar(&initrd, "initrd", "", "path to the guest initrd or URL")
	cmd.Flags().StringVar(&iso, "iso", "", "path to an ISO image file or URL")
	cmd.Flags().StringVar(&cmdline, "cmdline", "console=hvc0 reboot=t", "guest kernel command line")
	cmd.Flags().IntVar(&memSize, "mem", 1024, "set the VM's memory size in MiB")
	cmd.Flags().IntVar(&numCPU, "cpu", 1, "set the number of VCPUs")
	cmd.Flags().StringVar(&snapshot, "snapshot", "", "path to a snapshot to restore before starting the VM")
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "start VM and return without attaching console")
	cmd.Flags().StringVar(&tapName, "tap", "", "attach a virtio-net device backed by TAP (optional name)")
	cmd.Flags().BoolVar(&natMode, "nat", false, "attach a virtio-net device with host NAT (Linux only)")
	cmd.Flags().BoolVar(&vhostNet, "vhost-net", false, "enable vhost-net acceleration for TAP networking (Linux only, requires -tap)")
	cmd.Flags().Var(&blkdev, "block", "add a block device (multiple OK), append :ro for read-only")
	cmd.Flags().Var(&cowblk, "cow-block", "add a writable copy-on-write block device as BASE=OVERLAY (multiple OK)")
	return cmd
}

func newPauseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pause [uid]",
		Short: "Pause a running VM",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				uid = args[0]
			}
			resp, err := control.Dial(socketPath, "pause", map[string]any{"uid": uid})
			if err != nil {
				return err
			}
			if !resp.OK {
				if resp.Error != "" {
					return fmt.Errorf("%s", resp.Error)
				}
				return fmt.Errorf("control request failed")
			}
			fmt.Printf("uid=%s\n", resp.UID)
			return nil
		},
	}
	return cmd
}

func newResumeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resume [uid]",
		Short: "Resume a paused VM",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				uid = args[0]
			}
			resp, err := control.Dial(socketPath, "resume", map[string]any{"uid": uid})
			if err != nil {
				return err
			}
			if !resp.OK {
				if resp.Error != "" {
					return fmt.Errorf("%s", resp.Error)
				}
				return fmt.Errorf("control request failed")
			}
			fmt.Printf("uid=%s\n", resp.UID)
			return nil
		},
	}
	return cmd
}

func newConsoleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "console [uid]",
		Short: "Attach to the VM console",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				uid = args[0]
			}
			if uid == "" {
				return fmt.Errorf("uid is required")
			}
			_, err := control.Dial(socketPath, "connect", map[string]any{"uid": uid})
			if err != nil {
				return err
			}
			return attachConsole(socketPath, uid)
		},
	}
	return cmd
}

func newStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop [uid]",
		Short: "Stop a running VM",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				uid = args[0]
			}
			resp, err := control.Dial(socketPath, "shutdown", map[string]any{"uid": uid})
			if err != nil {
				return err
			}
			if !resp.OK {
				if resp.Error != "" {
					return fmt.Errorf("%s", resp.Error)
				}
				return fmt.Errorf("control request failed")
			}
			fmt.Printf("uid=%s\n", resp.UID)
			return nil
		},
	}
	return cmd
}

func newSaveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "save [uid] --path <path>",
		Short: "Save VM state to a snapshot",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				uid = args[0]
			}
			path, err := cmd.Flags().GetString("path")
			if err != nil || path == "" {
				return fmt.Errorf("save requires --path")
			}
			resp, err := control.Dial(socketPath, "save", map[string]any{"uid": uid, "path": path})
			if err != nil {
				return err
			}
			if !resp.OK {
				if resp.Error != "" {
					return fmt.Errorf("%s", resp.Error)
				}
				return fmt.Errorf("control request failed")
			}
			fmt.Printf("uid=%s\n", resp.UID)
			return nil
		},
	}
	cmd.Flags().String("path", "", "snapshot path for save/restore operations")
	return cmd
}

func newRestoreCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restore [uid] --path <path>",
		Short: "Restore VM state from a snapshot",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				uid = args[0]
			}
			path, err := cmd.Flags().GetString("path")
			if err != nil || path == "" {
				return fmt.Errorf("restore requires --path")
			}
			resp, err := control.Dial(socketPath, "restore", map[string]any{"uid": uid, "path": path})
			if err != nil {
				return err
			}
			if !resp.OK {
				if resp.Error != "" {
					return fmt.Errorf("%s", resp.Error)
				}
				return fmt.Errorf("control request failed")
			}
			fmt.Printf("uid=%s\n", resp.UID)
			return nil
		},
	}
	cmd.Flags().String("path", "", "snapshot path for save/restore operations")
	return cmd
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "kubevmctl:", err)
		os.Exit(1)
	}
}

func attachConsole(socketPath, uid string) error {
	if uid == "" {
		return fmt.Errorf("uid is required to attach to a VM")
	}
	if _, err := control.Dial(socketPath, "connect", map[string]any{"uid": uid}); err != nil {
		return err
	}

	consolePath := socketPath + "." + uid + ".console"
	conn, err := net.DialTimeout("unix", consolePath, 2*time.Second)
	if err != nil {
		return fmt.Errorf("console connection: %w", err)
	}
	defer conn.Close()
	fmt.Fprintf(os.Stderr, "connected to vm %s console (press Ctrl+] to detach)\n", uid)

	if term.IsTerminal(int(os.Stdin.Fd())) {
		old, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			return err
		}
		defer term.Restore(int(os.Stdin.Fd()), old)
	}

	errCh := make(chan error, 2)
	go func() {
		r := bufio.NewReader(os.Stdin)
		buf := []byte{0}
		for {
			b, err := r.ReadByte()
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrClosed) {
					errCh <- nil
					return
				}
				errCh <- err
				return
			}

			if b == consoleDetachByte {
				_ = conn.Close()
				errCh <- errConsoleDetach
				return
			}

			buf[0] = b
			if _, err := conn.Write(buf); err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrClosed) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
					errCh <- nil
					return
				}
				errCh <- err
				return
			}
		}

		/* unreachable */
	}()
	go func() {
		_, err := io.Copy(os.Stdout, conn)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) && !errors.Is(err, os.ErrClosed) && !errors.Is(err, syscall.EPIPE) && !errors.Is(err, syscall.ECONNRESET) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	for i := 0; i < 2; i++ {
		err := <-errCh
		if errors.Is(err, errConsoleDetach) {
			fmt.Fprintln(os.Stderr, "console detached; VM continues running")
			return nil
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func defaultSocketPath() string {
	if s := os.Getenv("KUBEVM_SOCKET"); s != "" {
		return s
	}
	return "/tmp/kube-vm/kube-vm.sock"
}

// flagStrings is a flag.Value that collects strings.
type flagStrings []string

func (*flagStrings) String() string {
	return ""
}

func (*flagStrings) Type() string {
	return "stringArray"
}

func (fs *flagStrings) Set(s string) error {
	*fs = append(*fs, s)
	return nil
}
