package mmio

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"kube-vm/virtio"
)

// StorageState describes an immutable, flattened logical disk artifact. File is
// relative to the snapshot directory; ReadOnly is the guest device property.
type StorageState struct {
	File     string
	Size     int64
	ReadOnly bool
	SHA256   string
}

type blockSnapshot interface {
	SnapshotBlockStorage() (virtio.BlockStorage, bool)
}

// ValidateSnapshotSupport rejects devices whose live state cannot be restored.
func (b *Bus) ValidateSnapshotSupport() error {
	for i, d := range b.dev {
		switch d.info.Type {
		case virtio.BlockDeviceID:
			if _, ok := d.handler.(blockSnapshot); !ok {
				return fmt.Errorf("device %d: block handler does not support storage snapshots", i)
			}
		case virtio.NetworkDeviceID, virtio.ConsoleDeviceID:
		case virtio.SocketDeviceID:
			return fmt.Errorf("device %d: VSock snapshots are unsupported (live connections cannot be restored)", i)
		default:
			return fmt.Errorf("device %d: unsupported snapshot device type %v", i, d.info.Type)
		}
	}
	return nil
}

// SnapshotStorage streams logical disks (including COW, memory and read-only
// media) to new 0600 files. The caller must hold Quiesce with guest CPUs stopped.
// Files must be treated as immutable after success. Existing files are never
// overwritten. Ordering is block-device configuration order, including ISO.
func (b *Bus) SnapshotStorage(dir string) (states []StorageState, err error) {
	if err := b.ValidateSnapshotSupport(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	var created []string
	defer func() {
		if err != nil {
			for _, path := range created {
				_ = os.Remove(path)
			}
		}
	}()
	for i, d := range b.dev {
		if d.info.Type != virtio.BlockDeviceID {
			continue
		}
		storage, ro := d.handler.(blockSnapshot).SnapshotBlockStorage()
		if storage == nil {
			return nil, fmt.Errorf("device %d: nil block storage", i)
		}
		if !ro {
			if s, ok := storage.(interface{ Sync() error }); ok {
				if err := s.Sync(); err != nil {
					return nil, fmt.Errorf("sync device %d: %w", i, err)
				}
			}
		}
		size, err := storage.Size()
		if err != nil {
			return nil, fmt.Errorf("device %d size: %w", i, err)
		}
		if size < 0 {
			return nil, fmt.Errorf("device %d: negative disk size", i)
		}
		name := fmt.Sprintf("disk-%d.img", len(states))
		path := filepath.Join(dir, name)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, err
		}
		created = append(created, path)
		hash := sha256.New()
		_, copyErr := io.CopyN(io.MultiWriter(f, hash), io.NewSectionReader(storage, 0, size), size)
		if copyErr == nil {
			copyErr = f.Sync()
		}
		closeErr := f.Close()
		if copyErr != nil {
			return nil, fmt.Errorf("snapshot device %d: %w", i, copyErr)
		}
		if closeErr != nil {
			return nil, closeErr
		}
		states = append(states, StorageState{File: name, Size: size, ReadOnly: ro, SHA256: fmt.Sprintf("%x", hash.Sum(nil))})
	}
	dirFile, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	syncErr := dirFile.Sync()
	closeErr := dirFile.Close()
	if syncErr != nil {
		return nil, syncErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return states, nil
}