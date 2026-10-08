package virtio

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCOWStoragePreservesBaseAndPersistsOverlay(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.img")
	overlayPath := filepath.Join(dir, "base.overlay")

	baseData := bytes.Repeat([]byte("0123456789abcdef"), 512)
	if err := os.WriteFile(basePath, baseData, 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}

	baseFile, err := os.Open(basePath)
	if err != nil {
		t.Fatalf("open base: %v", err)
	}
	defer baseFile.Close()

	overlayFile, err := os.OpenFile(overlayPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("open overlay: %v", err)
	}
	defer overlayFile.Close()

	storage, err := openCOWStorage(&FileStorage{File: baseFile}, overlayFile, 4096)
	if err != nil {
		t.Fatalf("open cow storage: %v", err)
	}

	patch := []byte("PATCHED")
	if n, err := storage.WriteAt(patch, 100); err != nil || n != len(patch) {
		t.Fatalf("WriteAt = (%d, %v), want (%d, nil)", n, err, len(patch))
	}
	if err := storage.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	got := make([]byte, 160)
	if _, err := storage.ReadAt(got, 64); err != nil {
		t.Fatalf("ReadAt after write: %v", err)
	}
	if !bytes.Equal(got[36:36+len(patch)], patch) {
		t.Fatalf("patched bytes = %q, want %q", got[36:36+len(patch)], patch)
	}

	baseCheck := make([]byte, len(patch))
	if _, err := (&FileStorage{File: baseFile}).ReadAt(baseCheck, 100); err != nil {
		t.Fatalf("read base: %v", err)
	}
	if bytes.Equal(baseCheck, patch) {
		t.Fatalf("base image mutated: got %q", baseCheck)
	}

	reopenedOverlay, err := os.OpenFile(overlayPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("reopen overlay: %v", err)
	}
	defer reopenedOverlay.Close()

	reopened, err := openCOWStorage(&FileStorage{File: baseFile}, reopenedOverlay, 4096)
	if err != nil {
		t.Fatalf("reopen cow storage: %v", err)
	}

	reopenedBuf := make([]byte, len(patch))
	if _, err := reopened.ReadAt(reopenedBuf, 100); err != nil {
		t.Fatalf("reopened ReadAt: %v", err)
	}
	if !bytes.Equal(reopenedBuf, patch) {
		t.Fatalf("reopened bytes = %q, want %q", reopenedBuf, patch)
	}
}
