package kubevm

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kube-vm/control"
	"kube-vm/virtio"
	"kube-vm/vmm"
)

func writeLegacyCheckpoint(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy.snap")
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(&vmm.VMSnapshot{Magic: "kube-vm-snapshot-v1", Version: 2}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRestoreInvalidCheckpointPreservesExistingVM(t *testing.T) {
	vm := &vmController{uid: "existing", vm: &vmm.VM{}}
	s := &vmServer{ctx: context.Background(), vms: map[string]*vmController{"existing": vm}}
	if err := s.Restore("existing", writeLegacyCheckpoint(t)); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("restore invalid checkpoint: %v", err)
	}
	if s.vms["existing"] != vm || vm.vm == nil || vm.shuttingDown {
		t.Fatal("invalid checkpoint changed or stopped the existing VM")
	}
}

func TestSnapshotStartDoesNotNormalizeOrdinaryBaseDisk(t *testing.T) {
	s := &vmServer{ctx: context.Background()}
	_, err := s.Start("resumed", map[string]any{
		"snapshot": writeLegacyCheckpoint(t), "base_disk": "missing-base-image",
		"kernel": "/missing/kernel", "mem": 8192, "cpu": 8,
	})
	if err == nil || !strings.Contains(err.Error(), "snapshot version") {
		t.Fatalf("snapshot boot should validate checkpoint, not ordinary boot defaults: %v", err)
	}
	if len(s.vms) != 0 {
		t.Fatal("failed snapshot boot registered a VM")
	}
}

func TestVMStorageStatsSnapshotAndDetails(t *testing.T) {
	c := &vmController{uid: "diskvm", storage: []*virtio.BlockMetrics{
		virtio.NewBlockMetrics("cow:/base?overlay=/vm", 8192, false),
		nil,
		virtio.NewBlockMetrics("installer.iso", 4096, true),
	}}
	snapshot := c.snapshot(time.Now())
	if len(snapshot.Storage) != 2 || snapshot.Storage[1].Spec != "installer.iso" || !snapshot.Storage[1].ReadOnly {
		t.Fatalf("storage snapshot: %+v", snapshot.Storage)
	}
	details := vmDetailsFromSnapshot(snapshot)
	if len(details.Storage) != 2 || details.Storage[0].CapacityBytes != 8192 {
		t.Fatalf("storage details: %+v", details.Storage)
	}
	for _, payload := range []any{snapshot, details} {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Storage []map[string]any `json:"storage"`
		}
		if err := json.Unmarshal(encoded, &body); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"spec", "capacity_bytes", "read_only", "read_ops", "write_ops", "flush_ops", "read_bytes", "write_bytes", "errors", "in_flight", "completed_ops", "io_time_ns", "max_io_time_ns"} {
			if _, ok := body.Storage[0][field]; !ok {
				t.Errorf("API missing %q in %s", field, encoded)
			}
		}
	}
	details.Storage[0].Spec = "changed"
	if snapshot.Storage[0].Spec == "changed" {
		t.Fatal("details aliases snapshot storage")
	}
}

func TestVMServerShutdownRemovesStaleEntry(t *testing.T) {
	s := &vmServer{vms: map[string]*vmController{"abc123": nil}}

	if err := s.Shutdown("abc123"); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}

	if _, ok := s.vms["abc123"]; ok {
		t.Fatal("vm server still contains a shutdown VM entry")
	}
}

func TestDefaultOptionsStartInLocalMode(t *testing.T) {
	opts := DefaultOptions()
	if opts.ServerMode {
		t.Fatal("DefaultOptions should not enable server mode by default")
	}
}

func TestServeConsoleStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	socketPath := filepath.Join(t.TempDir(), "console.sock")
	session := control.NewConsoleSession()

	errC := make(chan error, 1)
	go func() {
		errC <- control.ServeConsole(ctx, socketPath, session)
	}()

	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("dial console socket: %v", err)
	}
	session.SetConn(conn)

	cancel()

	select {
	case err := <-errC:
		if err != nil && err != context.Canceled {
			t.Fatalf("ServeConsole returned unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ServeConsole did not stop after context cancellation")
	}
}

func TestVMControllerShutdownDoesNotDeadlockOnConsoleClose(t *testing.T) {
	controller := &vmController{
		ctx:            context.Background(),
		consoleSession: control.NewConsoleSession(),
	}

	controller.consoleSession.SetOnClose(func() {
		_ = controller.Shutdown()
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := controller.Shutdown(); err != nil {
			t.Errorf("shutdown returned unexpected error: %v", err)
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("vm controller shutdown deadlocked on console close callback")
	}
}

func TestVMServerListIncludesNATGuestIP(t *testing.T) {
	s := &vmServer{vms: map[string]*vmController{
		"abc123": {uid: "abc123", vm: &vmm.VM{}, natGuestIP: "10.0.2.15"},
	}}

	list := s.List()
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}
	if list[0].IPAddress != "10.0.2.15" {
		t.Fatalf("IPAddress = %q, want %q", list[0].IPAddress, "10.0.2.15")
	}
}

func TestVMServerExamineReturnsSnapshotDetails(t *testing.T) {
	s := &vmServer{vms: map[string]*vmController{
		"abc123": {
			uid:        "abc123",
			vm:         &vmm.VM{},
			memMiB:     512,
			numCPU:     2,
			kernel:     "bzImage",
			nat:        true,
			natGuestIP: "10.0.2.15",
		},
	}}

	details, err := s.Examine("abc123")
	if err != nil {
		t.Fatalf("Examine returned error: %v", err)
	}
	if details.UID != "abc123" {
		t.Fatalf("UID = %q, want %q", details.UID, "abc123")
	}
	if details.NATGuestIP != "10.0.2.15" {
		t.Fatalf("NATGuestIP = %q, want %q", details.NATGuestIP, "10.0.2.15")
	}
	if details.NumCPU != 2 {
		t.Fatalf("NumCPU = %d, want 2", details.NumCPU)
	}
}

func TestWaitForConsoleSocketReturnsWhenSocketAppears(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	socketPath := filepath.Join(t.TempDir(), "console.sock")
	errC := make(chan error, 1)
	go func() {
		time.Sleep(20 * time.Millisecond)
		ln, err := net.Listen("unix", socketPath)
		if err != nil {
			errC <- err
			return
		}
		defer ln.Close()
		<-ctx.Done()
	}()

	if err := waitForConsoleSocket(ctx, socketPath, errC, time.Second); err != nil {
		t.Fatalf("waitForConsoleSocket returned error: %v", err)
	}
}

func TestWaitForConsoleSocketReturnsListenerError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	want := errors.New("listen failed")
	errC := make(chan error, 1)
	errC <- want

	err := waitForConsoleSocket(ctx, filepath.Join(t.TempDir(), "console.sock"), errC, time.Second)
	if !errors.Is(err, want) {
		t.Fatalf("waitForConsoleSocket error = %v, want %v", err, want)
	}
}

func TestWaitForConsoleSocketReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := waitForConsoleSocket(ctx, filepath.Join(t.TempDir(), "console.sock"), make(chan error), time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForConsoleSocket error = %v, want %v", err, context.Canceled)
	}
}

func TestVMServerSavedConfigsPersistAcrossReload(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "saved-configs.json")
	server := &vmServer{configsPath: configPath, defaultStart: map[string]any{"kernel": "bzImage"}}
	payload := map[string]any{"mem": "2048", "cpu": "2", "kernel": "bzImage"}

	stored, err := server.saveConfig(map[string]any{"name": "ubuntu", "payload": payload})
	if err != nil {
		t.Fatalf("saveConfig returned error: %v", err)
	}
	if stored["id"] == "" {
		t.Fatal("saveConfig did not return a persisted config id")
	}

	reloaded := &vmServer{configsPath: configPath, defaultStart: map[string]any{"kernel": "bzImage"}}
	configs := reloaded.defaultConfigs()
	if len(configs) != 2 {
		t.Fatalf("len(defaultConfigs()) = %d, want 2", len(configs))
	}
	if configs[1]["name"] != "ubuntu" {
		t.Fatalf("saved config name = %q, want %q", configs[1]["name"], "ubuntu")
	}
	if asString(configs[1]["payload"].(map[string]any), "mem", "") != "2048" {
		t.Fatalf("payload mem = %q, want %q", asString(configs[1]["payload"].(map[string]any), "mem", ""), "2048")
	}
}

func TestAsStringSliceSplitsNewlineDelimitedBlockSpecs(t *testing.T) {
	payload := map[string]any{"block": []any{"/home/dan/ginit/initramfs.raw\n./disks/test.img"}}
	got := asStringSlice(payload, "block")
	if len(got) != 2 {
		t.Fatalf("len(asStringSlice(...)) = %d, want 2", len(got))
	}
	if got[0] != "/home/dan/ginit/initramfs.raw" {
		t.Fatalf("got[0] = %q, want %q", got[0], "/home/dan/ginit/initramfs.raw")
	}
	if got[1] != "./disks/test.img" {
		t.Fatalf("got[1] = %q, want %q", got[1], "./disks/test.img")
	}
}

func TestNormalizeBaseDiskPayloadMovesBaseDiskToCOWBlock(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "base-disks")
	vmDir := filepath.Join(root, "vms")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatalf("MkdirAll base dir: %v", err)
	}
	basePath := filepath.Join(baseDir, "ubuntu.img")
	if err := os.WriteFile(basePath, []byte("base"), 0o644); err != nil {
		t.Fatalf("WriteFile base disk: %v", err)
	}
	t.Setenv("KUBE_VM_BASE_DISK_DIR", baseDir)
	t.Setenv("KUBE_VM_VM_DIR", vmDir)

	payload := map[string]any{"base_disk": basePath}
	if err := normalizeBaseDiskPayload("vm-123", payload); err != nil {
		t.Fatalf("normalizeBaseDiskPayload returned error: %v", err)
	}
	if _, ok := payload["base_disk"]; ok {
		t.Fatal("base_disk should be removed after normalization")
	}
	blocks := asStringSlice(payload, "block")
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	if !strings.HasPrefix(blocks[0], "cow:"+basePath+"?overlay=") {
		t.Fatalf("block spec = %q, want cow spec rooted at %q", blocks[0], basePath)
	}
	if strings.Contains(blocks[0], ":ro") {
		t.Fatalf("block spec = %q, want writable CoW overlay", blocks[0])
	}
	if _, err := os.Stat(filepath.Join(vmDir, "vm-123", "ubuntu.img")); err != nil {
		t.Fatalf("overlay path was not created: %v", err)
	}
}

func TestNormalizeBaseDiskPayloadConvertsReadOnlyBaseDiskBlockToWritableCOW(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "base-disks")
	vmDir := filepath.Join(root, "vms")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatalf("MkdirAll base dir: %v", err)
	}
	basePath := filepath.Join(baseDir, "ubuntu.img")
	if err := os.WriteFile(basePath, []byte("base"), 0o644); err != nil {
		t.Fatalf("WriteFile base disk: %v", err)
	}
	t.Setenv("KUBE_VM_BASE_DISK_DIR", baseDir)
	t.Setenv("KUBE_VM_VM_DIR", vmDir)

	payload := map[string]any{"block": []string{basePath + ":ro"}}
	if err := normalizeBaseDiskPayload("vm-456", payload); err != nil {
		t.Fatalf("normalizeBaseDiskPayload returned error: %v", err)
	}
	blocks := asStringSlice(payload, "block")
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	if strings.Contains(blocks[0], ":ro") {
		t.Fatalf("block spec = %q, want writable CoW overlay", blocks[0])
	}
	if !strings.HasPrefix(blocks[0], "cow:"+basePath+"?overlay=") {
		t.Fatalf("block spec = %q, want cow spec rooted at %q", blocks[0], basePath)
	}
}
