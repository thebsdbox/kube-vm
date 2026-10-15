package vmconfig

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestBlockDeviceFromSpecWithOverlayUsesCopyOnWriteStorage(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	basePath := filepath.Join(dir, "disk.img")
	overlayPath := filepath.Join(dir, "disk.overlay")
	baseData := bytes.Repeat([]byte("A"), 4096)
	if err := os.WriteFile(basePath, baseData, 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}

	spec, err := EncodeCOWBlockSpec(basePath, overlayPath)
	if err != nil {
		t.Fatalf("EncodeCOWBlockSpec: %v", err)
	}
	dev, err := blockDeviceFromSpec(spec, false)
	if err != nil {
		t.Fatalf("blockDeviceFromSpec: %v", err)
	}
	if dev.ReadOnly {
		t.Fatal("device unexpectedly marked read-only")
	}
	if dev.Metrics == nil {
		t.Fatal("missing storage statistics")
	}
	metrics := dev.Metrics.Snapshot()
	if metrics.Spec != spec || metrics.CapacityBytes != uint64(len(baseData)) || metrics.ReadOnly {
		t.Fatalf("storage metadata: %+v", metrics)
	}

	writer, ok := dev.Storage.(io.WriterAt)
	if !ok {
		t.Fatal("storage does not implement io.WriterAt")
	}

	patch := []byte("BBBB")
	if n, err := writer.WriteAt(patch, 128); err != nil || n != len(patch) {
		t.Fatalf("WriteAt = (%d, %v), want (%d, nil)", n, err, len(patch))
	}

	readBack := make([]byte, len(patch))
	if _, err := dev.Storage.ReadAt(readBack, 128); err != nil {
		t.Fatalf("ReadAt overlay-backed storage: %v", err)
	}
	if !bytes.Equal(readBack, patch) {
		t.Fatalf("overlay-backed read = %q, want %q", readBack, patch)
	}

	baseCheck, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatalf("ReadFile base: %v", err)
	}
	if bytes.Equal(baseCheck[128:128+len(patch)], patch) {
		t.Fatalf("base image mutated: got %q", baseCheck[128:128+len(patch)])
	}
}

func TestBlockDeviceFromSpecLegacyOverlayQueryStillWorks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	basePath := filepath.Join(dir, "disk.img")
	overlayPath := filepath.Join(dir, "disk.overlay")
	if err := os.WriteFile(basePath, bytes.Repeat([]byte("X"), 4096), 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}

	dev, err := blockDeviceFromSpec(basePath+"?overlay="+overlayPath, false)
	if err != nil {
		t.Fatalf("legacy overlay query blockDeviceFromSpec: %v", err)
	}
	if _, ok := dev.Storage.(io.WriterAt); !ok {
		t.Fatal("legacy overlay query storage does not implement io.WriterAt")
	}
}

func TestBlockDeviceFromSpecCreatesMissingOverlayParentDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	baseDir := filepath.Join(dir, "base")
	overlayDir := filepath.Join(dir, "missing", "nested")
	basePath := filepath.Join(baseDir, "disk.img")
	overlayPath := filepath.Join(overlayDir, "disk.overlay")
	if err := os.MkdirAll(filepath.Dir(basePath), 0o755); err != nil {
		t.Fatalf("MkdirAll base dir: %v", err)
	}
	if err := os.WriteFile(basePath, bytes.Repeat([]byte("X"), 4096), 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}

	dev, err := blockDeviceFromSpec(basePath+"?overlay="+overlayPath, false)
	if err != nil {
		t.Fatalf("blockDeviceFromSpec with missing overlay dir: %v", err)
	}
	if _, ok := dev.Storage.(io.WriterAt); !ok {
		t.Fatal("storage does not implement io.WriterAt")
	}
	if _, err := os.Stat(overlayPath); err != nil {
		t.Fatalf("overlay file should be created automatically: %v", err)
	}
}

func TestMergeBlockSpecsAppendsCanonicalCOWSpecs(t *testing.T) {
	t.Parallel()

	merged, err := MergeBlockSpecs([]string{"/data/root.img:ro"}, []string{"/base/disk.img=/overlay/vm.overlay"})
	if err != nil {
		t.Fatalf("MergeBlockSpecs: %v", err)
	}
	if len(merged) != 2 {
		t.Fatalf("len(merged) = %d, want 2", len(merged))
	}
	if merged[0] != "/data/root.img:ro" {
		t.Fatalf("merged[0] = %q, want %q", merged[0], "/data/root.img:ro")
	}
	if want := "cow:/base/disk.img?overlay=%2Foverlay%2Fvm.overlay"; merged[1] != want {
		t.Fatalf("merged[1] = %q, want %q", merged[1], want)
	}
}

func TestNormalizeConsoleCmdlineUsesTTYS0WhenSerialConsoleEnabled(t *testing.T) {
	t.Parallel()

	if got := normalizeConsoleCmdline("", true); got != DefaultSerialConsoleCmdline {
		t.Fatalf("normalizeConsoleCmdline(empty, true) = %q, want %q", got, DefaultSerialConsoleCmdline)
	}
	if got := normalizeConsoleCmdline(DefaultVirtioConsoleCmdline, true); got != DefaultSerialConsoleCmdline {
		t.Fatalf("normalizeConsoleCmdline(default virtio, true) = %q, want %q", got, DefaultSerialConsoleCmdline)
	}
	custom := "console=ttyS1 root=/dev/vda"
	if got := normalizeConsoleCmdline(custom, true); got != custom {
		t.Fatalf("normalizeConsoleCmdline(custom, true) = %q, want %q", got, custom)
	}
}
