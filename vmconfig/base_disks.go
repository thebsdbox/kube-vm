package vmconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const DefaultBaseDiskDir = "/var/lib/kube-vm/base-disks"

const defaultVMDiskDir = "/var/lib/kube-vm/vms"

func effectiveBaseDiskDir() string {
	if dir := strings.TrimSpace(os.Getenv("KUBE_VM_BASE_DISK_DIR")); dir != "" {
		return dir
	}
	return DefaultBaseDiskDir
}

func effectiveVMDiskDir() string {
	if dir := strings.TrimSpace(os.Getenv("KUBE_VM_VM_DIR")); dir != "" {
		return dir
	}
	return defaultVMDiskDir
}

func ListBaseDisks(dir string) ([]string, error) {
	if strings.TrimSpace(dir) == "" {
		dir = effectiveBaseDiskDir()
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if strings.TrimSpace(path) == "" {
			continue
		}
		out = append(out, filepath.Clean(path))
	}
	sort.Strings(out)
	return out, nil
}

func ResolveBaseDisk(baseDisk string) (string, error) {
	baseDisk = strings.TrimSpace(baseDisk)
	if baseDisk == "" {
		return "", nil
	}
	clean := filepath.Clean(baseDisk)
	baseDir := filepath.Clean(effectiveBaseDiskDir())
	if filepath.IsAbs(clean) {
		if clean == baseDir || strings.HasPrefix(clean, baseDir+string(os.PathSeparator)) {
			return clean, nil
		}
		return "", fmt.Errorf("base disk %q must live under %s", baseDisk, effectiveBaseDiskDir())
	}
	entries, err := ListBaseDisks(effectiveBaseDiskDir())
	if err != nil {
		return "", err
	}
	for _, candidate := range entries {
		if filepath.Base(candidate) == clean || filepath.Clean(candidate) == filepath.Clean(baseDir+string(os.PathSeparator)+clean) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("base disk %q was not found in %s", baseDisk, effectiveBaseDiskDir())
}

func PrepareVMOverlayForBaseDisk(uid, baseDisk string) (string, error) {
	basePath, err := ResolveBaseDisk(baseDisk)
	if err != nil {
		return "", err
	}
	vmDir := filepath.Join(effectiveVMDiskDir(), uid)
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		return "", fmt.Errorf("create VM storage dir %q: %w", vmDir, err)
	}
	overlayPath := filepath.Join(vmDir, filepath.Base(basePath))
	f, err := os.OpenFile(overlayPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return "", fmt.Errorf("create CoW overlay %q: %w", overlayPath, err)
	}
	_ = f.Close()
	return overlayPath, nil
}

func EncodeBaseDiskCOWSpec(baseDisk, uid string) (string, error) {
	basePath, err := ResolveBaseDisk(baseDisk)
	if err != nil {
		return "", err
	}
	overlayPath, err := PrepareVMOverlayForBaseDisk(uid, basePath)
	if err != nil {
		return "", err
	}
	return EncodeCOWBlockSpec(basePath, overlayPath)
}
