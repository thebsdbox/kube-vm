package vmconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"kube-vm/virtio"
	"kube-vm/virtio/mmio"
	"kube-vm/vmm"

	"golang.org/x/sys/unix"
)

// ValidateSnapshotAssets validates restore compatibility and fully reads every
// saved disk to verify its size and SHA256, without creating or modifying files.
// path is the snapshot file location; StorageDir is a sibling directory basename.
func ValidateSnapshotAssets(path string, state *vmm.VMSnapshot) error {
	if state == nil {
		return fmt.Errorf("kube-vm: nil snapshot")
	}
	if err := state.ValidateRestore(); err != nil {
		return fmt.Errorf("kube-vm: validate snapshot: %w", err)
	}
	if err := validateStorageLayout(state); err != nil {
		return err
	}
	if len(state.Storage) == 0 {
		return nil
	}
	dir, err := openSnapshotStorageDir(path, state.StorageDir)
	if err != nil {
		return err
	}
	defer dir.Close()
	for _, asset := range state.Storage {
		if err := readSnapshotAsset(dir, asset, io.Discard); err != nil {
			return err
		}
	}
	return nil
}

func snapshotOptions(opts Options, state *vmm.VMSnapshot) (Options, error) {
	saved := state.Config
	// TapName is an optional destination override, never a change of network mode.
	tapOverride := opts.RestoreTapName
	if tapOverride == "" {
		tapOverride = opts.TapName
	}
	if tapOverride != "" && (saved.NATMode || saved.TapName == "") {
		return Options{}, fmt.Errorf("kube-vm: TAP override requires a TAP-mode snapshot")
	}
	identity := saved.NetworkUID
	if identity == "" {
		identity = saved.UID
	}
	if saved.NATMode && identity == "" {
		return Options{}, fmt.Errorf("kube-vm: NAT snapshot is missing its network identity")
	}
	restored := Options{
		UID:           saved.UID,
		NetworkUID:    identity,
		FirmwarePath:  saved.FirmwarePath,
		KernelPath:    saved.KernelPath,
		InitrdPath:    saved.InitrdPath,
		Cmdline:       saved.Cmdline,
		MemMiB:        saved.MemSize >> 20,
		NumCPU:        saved.NumCPU,
		SerialConsole: saved.SerialConsole,
		TapName:       saved.TapName,
		NATMode:       saved.NATMode,
		VhostNet:      saved.VhostNet,
		VSOCKCID:      saved.VSOCKCID,
		VSOCKPort:     saved.VSOCKPort,
		ConsoleIn:     opts.ConsoleIn,
		ConsoleOut:    opts.ConsoleOut,
	}
	if tapOverride != "" {
		restored.TapName = tapOverride
	}
	return restored, nil
}

func snapshotBasename(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!filepath.IsAbs(name) && filepath.Base(name) == name &&
		!strings.ContainsAny(name, "/\\\x00")
}

func snapshotDeviceOrder(devices []virtio.DeviceConfig, state *vmm.VMSnapshot) ([]virtio.DeviceConfig, error) {
	if len(devices) != len(state.DeviceState) {
		return nil, fmt.Errorf("kube-vm: snapshot device count mismatch: built %d, saved %d", len(devices), len(state.DeviceState))
	}
	byType := make(map[virtio.DeviceID][]virtio.DeviceConfig)
	for _, device := range devices {
		var kind virtio.DeviceID
		switch device.(type) {
		case *virtio.BlockDevice:
			kind = virtio.BlockDeviceID
		case *virtio.NetDevice:
			kind = virtio.NetworkDeviceID
		case *virtio.ConsoleDevice:
			kind = virtio.ConsoleDeviceID
		case *virtio.VSockDevice:
			kind = virtio.SocketDeviceID
		}
		byType[kind] = append(byType[kind], device)
	}
	ordered := make([]virtio.DeviceConfig, 0, len(devices))
	for i, saved := range state.DeviceState {
		available := byType[saved.Type]
		if len(available) == 0 {
			return nil, fmt.Errorf("kube-vm: snapshot device %d type %d cannot be recreated", i, saved.Type)
		}
		ordered = append(ordered, available[0])
		if block, ok := available[0].(*virtio.BlockDevice); ok {
			features := saved.Features &^ virtio.RequiredFeatures
			block.SnapshotFeatures = &features
		}
		byType[saved.Type] = available[1:]
	}
	return ordered, nil
}

func validateStorageLayout(state *vmm.VMSnapshot) error {
	want := len(state.Config.BlockSpecs)
	if state.Config.ISOPath != "" {
		want++
	}
	if len(state.Storage) != want {
		return fmt.Errorf("kube-vm: snapshot has %d storage assets, want %d", len(state.Storage), want)
	}
	if (want != 0 || state.StorageDir != "") && !snapshotBasename(state.StorageDir) {
		return fmt.Errorf("kube-vm: invalid snapshot storage directory %q", state.StorageDir)
	}
	seen := make(map[string]bool, want)
	for i, asset := range state.Storage {
		if !snapshotBasename(asset.File) || seen[asset.File] {
			return fmt.Errorf("kube-vm: invalid or duplicate snapshot asset name %q", asset.File)
		}
		seen[asset.File] = true
		digest, err := hex.DecodeString(asset.SHA256)
		if err != nil || len(digest) != sha256.Size || asset.Size < 0 {
			return fmt.Errorf("kube-vm: invalid snapshot asset size or SHA256 for %q", asset.File)
		}
		if i == len(state.Config.BlockSpecs) && !asset.ReadOnly {
			return fmt.Errorf("kube-vm: snapshot ISO asset must be read-only")
		}
	}
	return nil
}

func openSnapshotStorageDir(path, name string) (*os.File, error) {
	// Do not follow symlinks for either the storage directory or its assets.
	dirPath := filepath.Join(filepath.Dir(path), name)
	fd, err := unix.Open(dirPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("kube-vm: open snapshot storage directory: %w", err)
	}
	return os.NewFile(uintptr(fd), dirPath), nil
}

func readSnapshotAsset(dir *os.File, asset mmio.StorageState, dst io.Writer) error {
	fd, err := unix.Openat(int(dir.Fd()), asset.File, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("kube-vm: open snapshot asset %q: %w", asset.File, err)
	}
	f := os.NewFile(uintptr(fd), asset.File)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("kube-vm: stat snapshot asset %q: %w", asset.File, err)
	}
	if !info.Mode().IsRegular() || info.Size() != asset.Size {
		return fmt.Errorf("kube-vm: snapshot asset %q is not a regular file of size %d", asset.File, asset.Size)
	}
	hash := sha256.New()
	// Read through EOF rather than accepting a short read or ignoring trailing data.
	n, err := io.Copy(io.MultiWriter(dst, hash), f)
	if err != nil {
		return fmt.Errorf("kube-vm: read snapshot asset %q: %w", asset.File, err)
	}
	if n != asset.Size || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), asset.SHA256) {
		return fmt.Errorf("kube-vm: snapshot asset %q size or SHA256 mismatch", asset.File)
	}
	return nil
}

func cloneSnapshotAssets(path string, state *vmm.VMSnapshot) (dir string, blocks []string, iso string, err error) {
	parent, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return "", nil, "", err
	}
	dir, err = os.MkdirTemp(parent, ".kube-vm-restore-")
	if err != nil {
		return "", nil, "", fmt.Errorf("kube-vm: create restore directory: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	if len(state.Storage) > 0 {
		var source *os.File
		source, err = openSnapshotStorageDir(path, state.StorageDir)
		if err != nil {
			return dir, nil, "", err
		}
		defer source.Close()
		for i, asset := range state.Storage {
			clonePath := filepath.Join(dir, fmt.Sprintf("disk-%d.img", i))
			var f *os.File
			f, err = os.OpenFile(clonePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return dir, nil, "", err
			}
			// Recheck the digest during copying in case an asset changed after validation.
			err = readSnapshotAsset(source, asset, f)
			if err == nil {
				err = f.Sync()
			}
			closeErr := f.Close()
			if err == nil {
				err = closeErr
			}
			if err != nil {
				return dir, nil, "", err
			}
			if i < len(state.Config.BlockSpecs) {
				// URL encoding protects literal '?' and ':' in the snapshot's parent path.
				spec := (&url.URL{Scheme: "file", Path: clonePath}).String()
				if asset.ReadOnly {
					spec += ":ro"
				}
				blocks = append(blocks, spec)
			} else {
				iso = clonePath
			}
		}
	}
	for _, directory := range []string{dir, parent} {
		var d *os.File
		d, err = os.Open(directory)
		if err != nil {
			return dir, nil, "", err
		}
		err = d.Sync()
		closeErr := d.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return dir, nil, "", err
		}
	}
	return dir, blocks, iso, err
}