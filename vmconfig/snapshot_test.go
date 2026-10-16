package vmconfig

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"kube-vm/kvm"
	"kube-vm/virtio"
	"kube-vm/virtio/mmio"
	"kube-vm/vmm"
)

func snapshotFixture(t *testing.T) (string, *vmm.VMSnapshot) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "saved.vm")
	state := &vmm.VMSnapshot{
		Magic: "kube-vm-snapshot-v1", Version: 3,
		Architecture: "amd64",
		Config: vmm.SnapshotConfig{
			UID: "original-vm", NetworkUID: "original-network",
			MemSize: 2 << 20, NumCPU: 2, SerialConsole: true,
			KernelPath: "/missing/original/kernel", InitrdPath: "/missing/original/initrd",
			Cmdline: "console=ttyS0 root=/dev/vda saved=yes",
		},
		Mem: make([]byte, 2<<20), CPU: make([]vmm.VMSnapshotCPU, 2),
		StorageDir: "saved.vm.storage",
	}
	// Seed the full CPU/kernel state pointers without invoking KVM. The fixture
	// intentionally contains no usable boot image or live host device state.
	seedSnapshotPointers(reflect.ValueOf(state).Elem())
	state.Serial = &vmm.SerialState{}
	for i := range state.CPU {
		seedSnapshotPointers(reflect.ValueOf(&state.CPU[i]).Elem())
	}
	if err := os.Mkdir(filepath.Join(filepath.Dir(path), state.StorageDir), 0o700); err != nil {
		t.Fatal(err)
	}
	return path, state
}

func seedSnapshotPointers(value reflect.Value) {
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		switch field.Type() {
		case reflect.TypeOf((*kvm.CPUState)(nil)):
			field.Set(reflect.ValueOf(&kvm.CPUState{}))
		case reflect.TypeOf((*kvm.VMState)(nil)):
			field.Set(reflect.ValueOf(&kvm.VMState{}))
		}
	}
}

func addSnapshotAsset(t *testing.T, path string, state *vmm.VMSnapshot, data []byte, readOnly, iso bool) string {
	t.Helper()
	name := fmt.Sprintf("disk-%d.img", len(state.Storage))
	assetPath := filepath.Join(filepath.Dir(path), state.StorageDir, name)
	if err := os.WriteFile(assetPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	state.Storage = append(state.Storage, mmio.StorageState{
		File: name, Size: int64(len(data)), ReadOnly: readOnly,
		SHA256: fmt.Sprintf("%x", sha256.Sum256(data)),
	})
	if iso {
		state.Config.ISOPath = "/missing/original/installer.iso"
	} else {
		state.Config.BlockSpecs = append(state.Config.BlockSpecs, "/missing/original/base.img")
	}
	state.DeviceState = append(state.DeviceState, mmio.DeviceState{SnapshotVersion: 1, Type: virtio.BlockDeviceID})
	return assetPath
}

func closeRestoredConfig(t *testing.T, cfg vmm.Config) {
	t.Helper()
	for _, device := range cfg.Devices {
		if block, ok := device.(*virtio.BlockDevice); ok {
			file := block.Storage.(*virtio.FileStorage).File
			if err := file.Close(); err != nil {
				t.Error(err)
			}
		}
	}
}

func TestBuildSnapshotClonesAssetsAndIgnoresLaunchDefaults(t *testing.T) {
	t.Parallel()
	path, state := snapshotFixture(t)
	original := bytes.Repeat([]byte("root"), 1024)
	savedPath := addSnapshotAsset(t, path, state, original, false, false)
	addSnapshotAsset(t, path, state, bytes.Repeat([]byte("data"), 1024), true, false)
	addSnapshotAsset(t, path, state, bytes.Repeat([]byte("iso!"), 1024), true, true)
	// A saved console before the disks must retain its MMIO slot, even though
	// the normal builder appends the console after the disks.
	state.DeviceState = append([]mmio.DeviceState{{SnapshotVersion: 1, Type: virtio.ConsoleDeviceID}}, state.DeviceState...)
	in, out := bytes.NewReader(nil), &bytes.Buffer{}
	cfg, netCfg, err := Build(Options{
		UID: "new-vm", NetworkUID: "wrong-network", SnapshotPath: path, Snapshot: state,
		KernelPath: "/must/not/read/kernel", InitrdPath: "/must/not/read/initrd",
		FirmwarePath: "/must/not/read/firmware", BlockSpecs: []string{"/must/not/open/disk"},
		ISOPath: "/must/not/open/iso", MemMiB: 1024, NumCPU: 8,
		Cmdline: "wrong command line", NATMode: true, VhostNet: true,
		VSOCKCID: 1, VSOCKPort: 999, ConsoleIn: in, ConsoleOut: out,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer closeRestoredConfig(t, cfg)
	if cfg.Snapshot != state || cfg.Loader != nil {
		t.Fatal("restore must pass through the snapshot and skip the loader")
	}
	if cfg.MemSize != state.Config.MemSize || cfg.NumCPU != state.Config.NumCPU {
		t.Fatalf("restored hardware = (%d, %d), want (%d, %d)", cfg.MemSize, cfg.NumCPU, state.Config.MemSize, state.Config.NumCPU)
	}
	if cfg.SnapshotConfig.Cmdline != state.Config.Cmdline || !cfg.SnapshotConfig.SerialConsole || cfg.SerialIn != in || cfg.SerialOut != out {
		t.Fatal("saved command line/serial configuration was not restored")
	}
	if cfg.SnapshotConfig.UID != "original-vm" || cfg.SnapshotConfig.NetworkUID != "original-network" {
		t.Fatalf("lost original identity: %+v", cfg.SnapshotConfig)
	}
	if cfg.SnapshotConfig.NATMode || netCfg.NAT || netCfg.TapName != "" || len(netCfg.NICs) != 0 {
		t.Fatal("launch defaults added networking absent from the snapshot")
	}
	if _, ok := cfg.Devices[0].(*virtio.ConsoleDevice); !ok {
		t.Fatal("saved console device order changed")
	}
	if len(cfg.Devices) != 4 || len(cfg.SnapshotConfig.BlockSpecs) != 2 {
		t.Fatalf("wrong device/storage count: %d/%d", len(cfg.Devices), len(cfg.SnapshotConfig.BlockSpecs))
	}
	var cloneDir string
	for i, asset := range state.Storage {
		block := cfg.Devices[i+1].(*virtio.BlockDevice)
		file := block.Storage.(*virtio.FileStorage).File
		if block.ReadOnly != asset.ReadOnly {
			t.Fatalf("disk %d read-only mismatch", i)
		}
		if asset.ReadOnly {
			if _, err := file.WriteAt([]byte("X"), 0); err == nil {
				t.Fatalf("read-only clone %d was opened writable", i)
			}
		}
		if i == 0 {
			cloneDir = filepath.Dir(file.Name())
		}
		if filepath.Dir(file.Name()) != cloneDir || file.Name() == savedPath {
			t.Fatal("assets were not copied into a private restore directory")
		}
		info, err := file.Stat()
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("clone permissions: %v, %v", info, err)
		}
		if i < 2 {
			spec, ro := strings.CutSuffix(cfg.SnapshotConfig.BlockSpecs[i], ":ro")
			u, err := url.Parse(spec)
			if err != nil || u.Path != file.Name() || ro != asset.ReadOnly {
				t.Fatalf("clone not exposed in BlockSpecs: %q", spec)
			}
		} else if cfg.SnapshotConfig.ISOPath != file.Name() {
			t.Fatal("ISO clone not exposed in snapshot metadata")
		}
	}
	if filepath.Dir(cloneDir) != filepath.Dir(path) {
		t.Fatal("restore directory is not next to the snapshot")
	}
	info, err := os.Stat(cloneDir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("restore directory permissions: %v, %v", info, err)
	}
	defer os.RemoveAll(cloneDir)
	root := cfg.Devices[1].(*virtio.BlockDevice)
	if _, err := root.Storage.(io.WriterAt).WriteAt([]byte("EDIT"), 0); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(savedPath)
	if err != nil || !bytes.Equal(saved, original) {
		t.Fatalf("saved asset mutated: %v", err)
	}
	if err := ValidateSnapshotAssets(path, state); err != nil {
		t.Fatalf("clone writes invalidated saved snapshot: %v", err)
	}
	if state.Config.BlockSpecs[0] != "/missing/original/base.img" {
		t.Fatal("Build mutated input snapshot metadata")
	}
}

func TestBuildSnapshotPathDoesNotReadBootImages(t *testing.T) {
	t.Parallel()
	path, state := snapshotFixture(t)
	state.Config.SerialConsole = false
	state.Serial = nil
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	err = gob.NewEncoder(f).Encode(state)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("encode snapshot: %v, %v", err, closeErr)
	}
	cfg, _, err := Build(Options{SnapshotPath: path, ConsoleIn: bytes.NewReader(nil), ConsoleOut: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Loader != nil || cfg.Snapshot == nil || cfg.SerialIn != nil || cfg.SerialOut != nil || len(cfg.Devices) != 0 {
		t.Fatal("snapshot-only build read boot images or added unsaved console hardware")
	}
}

func TestValidateSnapshotAssetsRejectsInvalidAssets(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"missing", "tampered", "short", "trailing", "directory", "symlink", "storage-symlink", "storage-traversal", "asset-traversal", "absolute", "bad-digest", "count", "writable-iso"} {
		t.Run(kind, func(t *testing.T) {
			path, state := snapshotFixture(t)
			asset := addSnapshotAsset(t, path, state, bytes.Repeat([]byte("A"), 512), false, false)
			switch kind {
			case "missing":
				if err := os.Remove(asset); err != nil {
					t.Fatal(err)
				}
			case "tampered", "short", "trailing":
				n := 512
				if kind == "short" {
					n--
				}
				if kind == "trailing" {
					n++
				}
				if err := os.WriteFile(asset, bytes.Repeat([]byte("B"), n), 0o600); err != nil {
					t.Fatal(err)
				}
			case "directory", "symlink":
				if err := os.Remove(asset); err != nil {
					t.Fatal(err)
				}
				if kind == "directory" {
					if err := os.Mkdir(asset, 0o700); err != nil {
						t.Fatal(err)
					}
				} else {
					target := filepath.Join(filepath.Dir(path), "target.img")
					if err := os.WriteFile(target, bytes.Repeat([]byte("A"), 512), 0o600); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, asset); err != nil {
						t.Fatal(err)
					}
				}
			case "storage-symlink":
				dir := filepath.Dir(asset)
				if err := os.Rename(dir, dir+"-real"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(dir+"-real", dir); err != nil {
					t.Fatal(err)
				}
			case "storage-traversal":
				state.StorageDir = "../outside"
			case "asset-traversal":
				state.Storage[0].File = "../outside.img"
			case "absolute":
				state.Storage[0].File = asset
			case "bad-digest":
				state.Storage[0].SHA256 = "invalid"
			case "count":
				state.Config.BlockSpecs = nil
			case "writable-iso":
				state.Config.BlockSpecs = nil
				state.Config.ISOPath = "original.iso"
			}
			if err := ValidateSnapshotAssets(path, state); err == nil {
				t.Fatal("accepted invalid snapshot asset")
			}
			if _, _, err := Build(Options{SnapshotPath: path, Snapshot: state}); err == nil {
				t.Fatal("built snapshot with invalid assets")
			}
			matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".kube-vm-restore-*"))
			if err != nil || len(matches) != 0 {
				t.Fatalf("failed restore left private copies: %v, %v", matches, err)
			}
		})
	}
}

func TestSnapshotNetworkIdentityAndTapOverrides(t *testing.T) {
	t.Parallel()
	_, state := snapshotFixture(t)
	state.Config.NATMode = true
	for _, identity := range []string{"saved-network", ""} {
		state.Config.NetworkUID = identity
		opts, err := snapshotOptions(Options{UID: "new-vm", NetworkUID: "new-network", MemMiB: 999, NumCPU: 99}, state)
		if err != nil {
			t.Fatal(err)
		}
		want := identity
		if want == "" {
			want = state.Config.UID
		}
		if opts.NetworkUID != want || !opts.NATMode || opts.UID != state.Config.UID {
			t.Fatalf("restored network identity: %+v", opts)
		}
	}
	if _, err := snapshotOptions(Options{RestoreTapName: "target0"}, state); err == nil {
		t.Fatal("accepted TAP override for NAT snapshot")
	}
	state.Config.NATMode = false
	if _, err := snapshotOptions(Options{TapName: "target0"}, state); err == nil {
		t.Fatal("accepted TAP override for disconnected snapshot")
	}
	state.Config.TapName = "saved0"
	for _, input := range []Options{{TapName: "target0"}, {RestoreTapName: "target0"}} {
		opts, err := snapshotOptions(input, state)
		if err != nil || opts.TapName != "target0" || opts.NATMode {
			t.Fatalf("TAP override: %+v, %v", opts, err)
		}
	}
}

func TestBuildSnapshotFailureRemovesPrivateCopies(t *testing.T) {
	t.Parallel()
	path, state := snapshotFixture(t)
	addSnapshotAsset(t, path, state, make([]byte, 512), false, false)
	// A vhost-net backend is not implemented, so building it fails after cloning.
	state.Config.TapName, state.Config.VhostNet = "saved0", true
	state.DeviceState = append(state.DeviceState, mmio.DeviceState{SnapshotVersion: 1, Type: virtio.NetworkDeviceID})
	if _, _, err := Build(Options{SnapshotPath: path, Snapshot: state}); err == nil {
		t.Fatal("expected unsupported network backend to fail")
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".kube-vm-restore-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("failed Build left private copies: %v, %v", matches, err)
	}
}

func TestSnapshotAssetsHashEntireFileAndRecheckDuringCopy(t *testing.T) {
	t.Parallel()
	path, state := snapshotFixture(t)
	data := bytes.Repeat([]byte("A"), 128<<10)
	asset := addSnapshotAsset(t, path, state, data, false, false)
	if err := ValidateSnapshotAssets(path, state); err != nil {
		t.Fatalf("valid asset: %v", err)
	}
	// Change only the final byte, beyond the streaming copy buffer size.
	data[len(data)-1] = 'B'
	if err := os.WriteFile(asset, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSnapshotAssets(path, state); err == nil {
		t.Fatal("validation did not hash the entire file")
	}
	if _, _, _, err := cloneSnapshotAssets(path, state); err == nil {
		t.Fatal("copy did not recheck the full digest after preflight validation")
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".kube-vm-restore-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("failed copy left private files: %v, %v", matches, err)
	}
}

func TestBuildNormalRecordsIdentityMetadata(t *testing.T) {
	t.Parallel()
	kernel := filepath.Join(t.TempDir(), "kernel")
	if err := os.WriteFile(kernel, []byte("boot image"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, identity := range []string{"", "stable-network"} {
		cfg, _, err := Build(Options{UID: "fresh-vm", NetworkUID: identity, KernelPath: kernel, MemMiB: 64, NumCPU: 3})
		if err != nil {
			t.Fatal(err)
		}
		want := identity
		if want == "" {
			want = "fresh-vm"
		}
		if cfg.Loader == nil || cfg.Snapshot != nil || cfg.MemSize != 64<<20 || cfg.NumCPU != 3 {
			t.Fatal("normal build behavior changed")
		}
		if cfg.SnapshotConfig.UID != "fresh-vm" || cfg.SnapshotConfig.NetworkUID != want {
			t.Fatalf("normal launch identity: %+v", cfg.SnapshotConfig)
		}
	}
}