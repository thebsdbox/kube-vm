package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
	"golang.org/x/term"

	"kube-vm/control"
	"kube-vm/vmconfig"
	"kube-vm/vmm"
)

const consoleDetachByte = 0x1d // Ctrl+]

var errConsoleDetach = errors.New("kubevmctl: console detached")

var controlDial = control.Dial
var sshAuthMethodsProvider = defaultSSHAuthMethods
var sshHostKeyCallbackProvider = defaultSSHHostKeyCallback
var sshHostKeyPrompter = promptSSHHostKey
var sshPasswordPrompter = promptSSHPassword
var sshCommandRunner = runSSHCommand

type sshInvocation struct {
	Host          string
	User          string
	Port          int
	Command       []string
	IgnoreHostKey bool
}

var (
	socketPath    string
	uid           string
	detach        bool
	firmware      string
	kernel        string
	initrd        string
	iso           string
	cmdline       string
	serialConsole bool
	memSize       int
	numCPU        int
	snapshot      string
	tapName       string
	baseDisk      string
	natMode       bool
	vhostNet      bool
	blkdev        flagStrings
	cowblk        flagStrings
)

func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "kubevmctl",
		Short: "Control kube-vm VMs over the control socket",
		Example: strings.TrimSpace(`
  kubevmctl list
  kubevmctl start --uid vm-123 --kernel bzImage --initrd initrd.cpio.gz
  kubevmctl template save ubuntu --kernel bzImage --mem 4096
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
	rootCmd.AddCommand(newSSHCmd())
	rootCmd.AddCommand(newStopCmd())
	rootCmd.AddCommand(newSaveCmd())
	rootCmd.AddCommand(newRestoreCmd())
	rootCmd.AddCommand(newSnapshotCmd())
	rootCmd.AddCommand(newBaseDiskCmd())
	rootCmd.AddCommand(newTemplateCmd())

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
	fmt.Fprintf(w, "architecture=%s\n", snap.Architecture)
	fmt.Fprintf(w, "storage_dir=%s\n", snap.StorageDir)
	if err := snap.ValidateRestore(); err != nil {
		fmt.Fprintf(w, "resumable=false\nrestore_error=%s\n", err)
	} else {
		fmt.Fprintln(w, "resumable=true")
	}
	for i, disk := range snap.Storage {
		fmt.Fprintf(w, "disk[%d]=%s bytes=%d read_only=%t sha256=%s\n", i, disk.File, disk.Size, disk.ReadOnly, disk.SHA256)
	}
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

func templateConfigPath(socketPath string) string {
	if strings.TrimSpace(socketPath) == "" {
		socketPath = defaultSocketPath()
	}
	return filepath.Join(filepath.Dir(socketPath), "saved-configs.json")
}

func loadSavedTemplates(socketPath string) ([]map[string]any, error) {
	path := templateConfigPath(socketPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	var out []map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func writeSavedTemplates(socketPath string, templates []map[string]any) error {
	path := templateConfigPath(socketPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(templates, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func saveTemplate(socketPath, name string, payload map[string]any) (map[string]any, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("template name is required")
	}
	if payload == nil {
		return nil, fmt.Errorf("template payload is required")
	}

	templates, err := loadSavedTemplates(socketPath)
	if err != nil {
		return nil, err
	}
	if templates == nil {
		templates = []map[string]any{}
	}
	updatedAt := time.Now().UTC().Format(time.RFC3339)
	newItem := map[string]any{
		"id":         fmt.Sprintf("template-%d", time.Now().UnixNano()),
		"name":       name,
		"payload":    payload,
		"source":     "saved",
		"created_at": updatedAt,
		"updated_at": updatedAt,
	}
	for i, item := range templates {
		if !strings.EqualFold(strings.TrimSpace(fmt.Sprint(item["name"])), name) {
			continue
		}
		newItem["id"] = item["id"]
		if newItem["id"] == nil {
			newItem["id"] = fmt.Sprintf("template-%d", time.Now().UnixNano())
		}
		templates[i] = newItem
		if err := writeSavedTemplates(socketPath, templates); err != nil {
			return nil, err
		}
		return newItem, nil
	}
	templates = append(templates, newItem)
	if err := writeSavedTemplates(socketPath, templates); err != nil {
		return nil, err
	}
	return newItem, nil
}

func deleteTemplate(socketPath, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("template name is required")
	}
	templates, err := loadSavedTemplates(socketPath)
	if err != nil {
		return err
	}
	filtered := make([]map[string]any, 0, len(templates))
	for _, item := range templates {
		if !strings.EqualFold(strings.TrimSpace(fmt.Sprint(item["name"])), name) {
			filtered = append(filtered, item)
		}
	}
	return writeSavedTemplates(socketPath, filtered)
}

func templatePayloadFromFlags() (map[string]any, error) {
	blockSpecs, err := vmconfig.MergeBlockSpecs([]string(blkdev), []string(cowblk))
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"firmware":       firmware,
		"kernel":         kernel,
		"initrd":         initrd,
		"block":          blockSpecs,
		"iso":            iso,
		"cmdline":        cmdline,
		"serial_console": serialConsole,
		"mem":            memSize,
		"cpu":            numCPU,
		"tap":            tapName,
		"nat":            natMode,
		"vhost":          vhostNet,
	}
	if strings.TrimSpace(baseDisk) != "" {
		payload["base_disk"] = baseDisk
	}
	if snapshot != "" {
		payload["snapshot"] = snapshot
	}
	if uid != "" {
		payload["uid"] = uid
	}
	return payload, nil
}

func listTemplates(socketPath string) ([]map[string]any, error) {
	return loadSavedTemplates(socketPath)
}

func newBaseDiskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "base-disk",
		Short: "Inspect and list the reusable base-disk images",
	}
	cmd.AddCommand(newBaseDiskListCmd())
	return cmd
}

func newBaseDiskListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available base disks in /var/lib/kube-vm/base-disks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			disks, err := vmconfig.ListBaseDisks(vmconfig.DefaultBaseDiskDir)
			if err != nil {
				return err
			}
			if len(disks) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no base disks found")
				return nil
			}
			for _, disk := range disks {
				fmt.Fprintln(cmd.OutOrStdout(), disk)
			}
			return nil
		},
	}
	return cmd
}

func newTemplateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "template",
		Short: "Manage saved VM configuration templates",
	}
	cmd.AddCommand(newTemplateListCmd())
	cmd.AddCommand(newTemplateSaveCmd())
	cmd.AddCommand(newTemplateDeleteCmd())
	cmd.AddCommand(newTemplateStartCmd())
	return cmd
}

func newTemplateListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List saved VM templates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			templates, err := listTemplates(socketPath)
			if err != nil {
				return err
			}
			if len(templates) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no saved templates")
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), "NAME\tID\tSUMMARY")
			for _, item := range templates {
				name := strings.TrimSpace(fmt.Sprint(item["name"]))
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", name, fmt.Sprint(item["id"]), templateSummary(item["payload"]))
			}
			return nil
		},
	}
	return cmd
}

func newTemplateSaveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "save <name>",
		Short: "Save the current VM configuration as a template",
		Args:  cobra.ExactArgs(1),
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
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			payload, err := templatePayloadFromFlags()
			if err != nil {
				return err
			}
			item, err := saveTemplate(socketPath, args[0], payload)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "saved template %s id=%s\n", item["name"], item["id"])
			return nil
		},
	}
	cmd.Flags().StringVarP(&uid, "uid", "u", uid, "VM UID to target")
	cmd.Flags().StringVar(&firmware, "firmware", "", "path to guest firmware image (BIOS/UEFI blob)")
	cmd.Flags().StringVar(&kernel, "kernel", "bzImage", "path to the guest kernel or URL")
	cmd.Flags().StringVar(&initrd, "initrd", "", "path to the guest initrd or URL")
	cmd.Flags().StringVar(&iso, "iso", "", "path to an ISO image file or URL")
	cmd.Flags().StringVar(&cmdline, "cmdline", "console=hvc0 root=/dev/vda reboot=t", "guest kernel command line")
	cmd.Flags().BoolVar(&serialConsole, "serial-console", false, "attach a COM1 serial console and switch the stock cmdline to ttyS0 unless --cmdline overrides it")
	cmd.Flags().IntVar(&memSize, "mem", 1024, "set the VM's memory size in MiB")
	cmd.Flags().IntVar(&numCPU, "cpu", 1, "set the number of VCPUs")
	cmd.Flags().StringVar(&snapshot, "snapshot", "", "path to a snapshot to restore before starting the VM")
	cmd.Flags().StringVar(&tapName, "tap", "", "attach a virtio-net device backed by TAP (optional name)")
	cmd.Flags().StringVar(&baseDisk, "base-disk", "", "select a read-only base disk from /var/lib/kube-vm/base-disks; kube-vm creates a per-VM CoW overlay automatically")
	cmd.Flags().BoolVar(&natMode, "nat", false, "attach a virtio-net device with host NAT (Linux only)")
	cmd.Flags().BoolVar(&vhostNet, "vhost-net", false, "enable vhost-net acceleration for TAP networking (Linux only, requires -tap)")
	cmd.Flags().Var(&blkdev, "block", "add a block device (multiple OK), append :ro for read-only")
	cmd.Flags().Var(&cowblk, "cow-block", "add a writable copy-on-write block device as BASE=OVERLAY (multiple OK)")
	return cmd
}

func newTemplateDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a saved VM template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := deleteTemplate(socketPath, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted template %s\n", args[0])
			return nil
		},
	}
	return cmd
}

func newTemplateStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start <name>",
		Short: "Start a VM from a saved template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			templates, err := listTemplates(socketPath)
			if err != nil {
				return err
			}
			var payload map[string]any
			for _, item := range templates {
				if strings.EqualFold(strings.TrimSpace(fmt.Sprint(item["name"])), args[0]) {
					if p, ok := item["payload"].(map[string]any); ok {
						payload = p
					}
					break
				}
			}
			if payload == nil {
				return fmt.Errorf("template %q not found", args[0])
			}
			if uid != "" {
				payload = map[string]any{}
				for k, v := range payload {
					payload[k] = v
				}
				payload["uid"] = uid
			}
			resp, err := controlDial(socketPath, "start", payload)
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
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "start VM and return without attaching console")
	return cmd
}

func templateSummary(payload any) string {
	m, ok := payload.(map[string]any)
	if !ok || len(m) == 0 {
		return "n/a"
	}
	parts := make([]string, 0, 4)
	if mem, ok := m["mem"]; ok && mem != nil {
		parts = append(parts, fmt.Sprintf("mem=%v", mem))
	}
	if cpu, ok := m["cpu"]; ok && cpu != nil {
		parts = append(parts, fmt.Sprintf("cpu=%v", cpu))
	}
	if tap, ok := m["tap"]; ok && tap != nil && fmt.Sprint(tap) != "" {
		parts = append(parts, fmt.Sprintf("tap=%v", tap))
	} else if nat, ok := m["nat"]; ok && nat != nil && fmt.Sprint(nat) == "true" {
		parts = append(parts, "nat=true")
	}
	if kernel, ok := m["kernel"]; ok && kernel != nil && fmt.Sprint(kernel) != "" {
		parts = append(parts, fmt.Sprintf("kernel=%v", kernel))
	}
	if base, ok := m["base_disk"]; ok && base != nil && fmt.Sprint(base) != "" {
		parts = append(parts, fmt.Sprintf("base=%s", filepath.Base(fmt.Sprint(base))))
	}
	if len(parts) == 0 {
		return "custom"
	}
	return strings.Join(parts, ", ")
}

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List running VMs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := controlDial(socketPath, "list", nil)
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
			resp, err := controlDial(socketPath, "examine", map[string]any{"uid": uid})
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
			resp, err := controlDial(socketPath, "status", map[string]any{"uid": uid})
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
				"firmware":       firmware,
				"kernel":         kernel,
				"initrd":         initrd,
				"block":          blockSpecs,
				"iso":            iso,
				"cmdline":        cmdline,
				"serial_console": serialConsole,
				"mem":            memSize,
				"cpu":            numCPU,
				"tap":            tapName,
				"nat":            natMode,
				"vhost":          vhostNet,
			}
			if strings.TrimSpace(baseDisk) != "" {
				payload["base_disk"] = baseDisk
			}
			if snapshot != "" {
				payload["snapshot"] = snapshot
			}
			if uid != "" {
				payload["uid"] = uid
			}
			resp, err := controlDial(socketPath, "start", payload)
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
	cmd.Flags().StringVar(&cmdline, "cmdline", "console=hvc0 root=/dev/vda reboot=t", "guest kernel command line")
	cmd.Flags().BoolVar(&serialConsole, "serial-console", false, "attach a COM1 serial console and switch the stock cmdline to ttyS0 unless --cmdline overrides it")
	cmd.Flags().IntVar(&memSize, "mem", 1024, "set the VM's memory size in MiB")
	cmd.Flags().IntVar(&numCPU, "cpu", 1, "set the number of VCPUs")
	cmd.Flags().StringVar(&snapshot, "snapshot", "", "path to a snapshot to restore before starting the VM")
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "start VM and return without attaching console")
	cmd.Flags().StringVar(&tapName, "tap", "", "attach a virtio-net device backed by TAP (optional name)")
	cmd.Flags().StringVar(&baseDisk, "base-disk", "", "select a read-only base disk from /var/lib/kube-vm/base-disks; kube-vm creates a per-VM CoW overlay automatically")
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
			resp, err := controlDial(socketPath, "pause", map[string]any{"uid": uid})
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
			resp, err := controlDial(socketPath, "resume", map[string]any{"uid": uid})
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
			_, err := controlDial(socketPath, "connect", map[string]any{"uid": uid})
			if err != nil {
				return err
			}
			return attachConsole(socketPath, uid)
		},
	}
	return cmd
}

func newSSHCmd() *cobra.Command {
	var sshUser string
	var sshPort int
	var ignoreHostKey bool
	cmd := &cobra.Command{
		Use:                   "ssh <uid-or-ip> [-- remote-command ...]",
		Short:                 "Open an SSH session to a VM by UID or IP address",
		Args:                  cobra.MinimumNArgs(1),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			host, err := resolveSSHTarget(socketPath, args[0], sshUser)
			if err != nil {
				return err
			}
			invocation := sshInvocation{Host: host, User: sshUser, Port: sshPort, IgnoreHostKey: ignoreHostKey}
			if len(args) > 1 {
				invocation.Command = append([]string(nil), args[1:]...)
			}
			return sshCommandRunner(invocation)
		},
	}
	cmd.Flags().StringVarP(&sshUser, "user", "l", "", "SSH username to prepend to the destination")
	cmd.Flags().IntVarP(&sshPort, "port", "p", 22, "SSH port")
	cmd.Flags().BoolVar(&ignoreHostKey, "insecure-ignore-host-key", false, "skip SSH host key verification")
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
			resp, err := controlDial(socketPath, "shutdown", map[string]any{"uid": uid})
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
			resp, err := controlDial(socketPath, "save", map[string]any{"uid": uid, "path": path})
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
			resp, err := controlDial(socketPath, "restore", map[string]any{"uid": uid, "path": path})
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

func resolveSSHTarget(socketPath, target, user string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("ssh target is required")
	}

	host := target
	if ip := net.ParseIP(target); ip == nil {
		resp, err := controlDial(socketPath, "examine", map[string]any{"uid": target})
		if err != nil {
			return "", err
		}
		if !resp.OK {
			if resp.Error != "" {
				return "", fmt.Errorf("%s", resp.Error)
			}
			return "", fmt.Errorf("control request failed")
		}
		if resp.VM == nil {
			return "", fmt.Errorf("control request returned no VM details")
		}
		host = strings.TrimSpace(resp.VM.NATGuestIP)
		if host == "" {
			return "", fmt.Errorf("vm %s does not have a guest IP address", target)
		}
	}

	if user == "" {
		return host, nil
	}
	return host, nil
}

func runSSHCommand(invocation sshInvocation) error {
	sshUser, err := resolveSSHUser(invocation.User)
	if err != nil {
		return err
	}
	invocation.User = sshUser

	authMethods, cleanup, err := sshAuthMethodsProvider(invocation)
	if err != nil {
		return err
	}
	defer cleanup()

	hostKeyCallback, err := sshHostKeyCallbackProvider(invocation)
	if err != nil {
		return err
	}

	client, err := ssh.Dial("tcp", net.JoinHostPort(invocation.Host, strconv.Itoa(invocation.Port)), &ssh.ClientConfig{
		User:            sshUser,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         5 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("ssh dial %s: %w", invocation.Host, err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close()

	session.Stdin = os.Stdin
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr

	usePTY := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	var stopResize func()
	if usePTY {
		oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			return fmt.Errorf("ssh raw terminal: %w", err)
		}
		defer term.Restore(int(os.Stdin.Fd()), oldState)

		width, height, err := term.GetSize(int(os.Stdout.Fd()))
		if err != nil {
			width, height = 80, 24
		}
		modes := ssh.TerminalModes{
			ssh.ECHO:          1,
			ssh.TTY_OP_ISPEED: 14400,
			ssh.TTY_OP_OSPEED: 14400,
		}
		termType := os.Getenv("TERM")
		if termType == "" {
			termType = "xterm-256color"
		}
		if err := session.RequestPty(termType, height, width, modes); err != nil {
			return fmt.Errorf("ssh request pty: %w", err)
		}
		stopResize = forwardSSHWindowChanges(session, int(os.Stdout.Fd()))
		defer stopResize()
	}

	if len(invocation.Command) == 0 {
		if err := session.Shell(); err != nil {
			return fmt.Errorf("ssh shell: %w", err)
		}
		return session.Wait()
	}

	return session.Run(shellQuoteArgs(invocation.Command))
}

func resolveSSHUser(explicit string) (string, error) {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		return explicit, nil
	}
	if envUser := strings.TrimSpace(os.Getenv("USER")); envUser != "" {
		return envUser, nil
	}
	currentUser, err := user.Current()
	if err == nil && strings.TrimSpace(currentUser.Username) != "" {
		return currentUser.Username, nil
	}
	return "", fmt.Errorf("ssh user is required; use --user")
}

func defaultSSHAuthMethods(invocation sshInvocation) ([]ssh.AuthMethod, func(), error) {
	methods := make([]ssh.AuthMethod, 0, 4)
	cleanups := make([]func(), 0, 1)
	passwordProvider := newSSHPasswordProvider(invocation)

	if agentSock := strings.TrimSpace(os.Getenv("SSH_AUTH_SOCK")); agentSock != "" {
		agentConn, err := net.Dial("unix", agentSock)
		if err == nil {
			agentClient := agent.NewClient(agentConn)
			methods = append(methods, ssh.PublicKeysCallback(agentClient.Signers))
			cleanups = append(cleanups, func() { _ = agentConn.Close() })
		}
	}

	for _, path := range defaultSSHKeyPaths() {
		signer, err := readSSHSigner(path)
		if err != nil {
			continue
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}

	methods = append(methods,
		ssh.PasswordCallback(passwordProvider),
		ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
			password, err := passwordProvider()
			if err != nil {
				return nil, err
			}
			answers := make([]string, len(questions))
			for i := range questions {
				answers[i] = password
			}
			return answers, nil
		}),
	)

	cleanup := func() {
		for _, fn := range cleanups {
			fn()
		}
	}
	return methods, cleanup, nil
}

func newSSHPasswordProvider(invocation sshInvocation) func() (string, error) {
	var mu sync.Mutex
	var cached string
	var cachedErr error
	var resolved bool

	return func() (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if resolved {
			return cached, cachedErr
		}
		resolved = true
		cached, cachedErr = resolveSSHPassword(invocation)
		return cached, cachedErr
	}
}

func resolveSSHPassword(invocation sshInvocation) (string, error) {
	if password := os.Getenv("KUBEVM_SSH_PASSWORD"); password != "" {
		return password, nil
	}
	return sshPasswordPrompter(invocation)
}

func promptSSHPassword(invocation sshInvocation) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", fmt.Errorf("no SSH authentication methods available; set SSH_AUTH_SOCK, add a key under ~/.ssh, set KUBEVM_SSH_PASSWORD, or run from a terminal to enter a password")
	}
	defer tty.Close()

	if _, err := fmt.Fprintf(tty, "%s@%s password: ", invocation.User, invocation.Host); err != nil {
		return "", fmt.Errorf("prompt SSH password: %w", err)
	}
	password, err := term.ReadPassword(int(tty.Fd()))
	if _, printErr := fmt.Fprintln(tty); printErr != nil && err == nil {
		err = printErr
	}
	if err != nil {
		return "", fmt.Errorf("read SSH password: %w", err)
	}
	return string(password), nil
}

func defaultSSHKeyPaths() []string {
	homeDir, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(homeDir) == "" {
		return nil
	}
	sshDir := filepath.Join(homeDir, ".ssh")
	return []string{
		filepath.Join(sshDir, "id_ed25519"),
		filepath.Join(sshDir, "id_ecdsa"),
		filepath.Join(sshDir, "id_rsa"),
	}
}

func readSSHSigner(path string) (ssh.Signer, error) {
	keyData, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(keyData)
}

func defaultSSHHostKeyCallback(invocation sshInvocation) (ssh.HostKeyCallback, error) {
	if invocation.IgnoreHostKey || shouldIgnoreSSHHostKey() {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(homeDir) == "" {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	knownHostsPath := filepath.Join(homeDir, ".ssh", "known_hosts")
	callback, err := knownhosts.New(knownHostsPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load known_hosts: %w", err)
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if callback != nil {
			err := callback(hostname, remote, key)
			if err == nil {
				return nil
			}
			var keyErr *knownhosts.KeyError
			if !errors.As(err, &keyErr) || len(keyErr.Want) > 0 {
				return err
			}
		}

		accepted, err := approveUnknownSSHHost(hostname, key)
		if err != nil {
			return err
		}
		if !accepted {
			return fmt.Errorf("knownhosts: key is unknown")
		}
		if err := appendKnownHost(knownHostsPath, hostname, key); err != nil {
			return err
		}
		return nil
	}, nil
}

func shouldIgnoreSSHHostKey() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("KUBEVM_SSH_IGNORE_HOST_KEY"))) {
	case "1", "true", "yes", "y":
		return true
	default:
		return false
	}
}

func approveUnknownSSHHost(hostname string, key ssh.PublicKey) (bool, error) {
	if shouldAutoAcceptUnknownSSHHosts() {
		return true, nil
	}
	return sshHostKeyPrompter(hostname, key)
}

func shouldAutoAcceptUnknownSSHHosts() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("KUBEVM_SSH_ACCEPT_NEW_HOSTS"))) {
	case "1", "true", "yes", "y":
		return true
	default:
		return false
	}
}

func promptSSHHostKey(hostname string, key ssh.PublicKey) (bool, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false, fmt.Errorf("ssh host %s is unknown; set KUBEVM_SSH_ACCEPT_NEW_HOSTS=1 or run from a terminal to confirm the host key", displaySSHHost(hostname))
	}
	defer tty.Close()

	if _, err := fmt.Fprintf(tty, "The authenticity of host '%s' can't be established.\nKey fingerprint is %s.\nTrust this host and add it to known_hosts? (yes/no): ", displaySSHHost(hostname), ssh.FingerprintSHA256(key)); err != nil {
		return false, fmt.Errorf("prompt SSH host key: %w", err)
	}
	answer, err := bufio.NewReader(tty).ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("read SSH host key confirmation: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "yes", "y":
		return true, nil
	default:
		return false, nil
	}
}

func appendKnownHost(path, hostname string, key ssh.PublicKey) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create known_hosts directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open known_hosts: %w", err)
	}
	defer file.Close()

	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key)
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	if _, err := io.WriteString(file, line); err != nil {
		return fmt.Errorf("write known_hosts entry: %w", err)
	}
	return nil
}

func displaySSHHost(hostname string) string {
	host, port, err := net.SplitHostPort(hostname)
	if err != nil {
		return hostname
	}
	if port == "22" {
		return host
	}
	return hostname
}

func forwardSSHWindowChanges(session *ssh.Session, fd int) func() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case <-sigCh:
				width, height, err := term.GetSize(fd)
				if err != nil {
					continue
				}
				_ = session.WindowChange(height, width)
			}
		}
	}()
	return func() {
		close(done)
		signal.Stop(sigCh)
	}
}

func shellQuoteArgs(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuote(arg string) string {
	if arg == "" {
		return "''"
	}
	if strings.IndexFunc(arg, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("@%_+=:,./-", r))
	}) == -1 {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `"'"'"`) + "'"
}

func attachConsole(socketPath, uid string) error {
	if uid == "" {
		return fmt.Errorf("uid is required to attach to a VM")
	}
	if _, err := controlDial(socketPath, "connect", map[string]any{"uid": uid}); err != nil {
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
