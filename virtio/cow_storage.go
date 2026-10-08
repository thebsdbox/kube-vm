package virtio

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"
)

const defaultCOWClusterSize int64 = 64 << 10

var cowStorageMagic = [16]byte{'k', 'u', 'b', 'e', 'v', 'm', '-', 'c', 'o', 'w', '-', 'v', '1'}

type cowStorageHeader struct {
	Magic       [16]byte
	Version     uint32
	ClusterSize uint32
	Size        uint64
}

type cowClusterRef struct {
	dataOffset int64
}

// COWStorage is a copy-on-write block storage layer backed by a read-only base
// device plus an append-only overlay file.
type COWStorage struct {
	Base    BlockStorage
	Overlay *os.File

	mu          sync.RWMutex
	clusterSize int64
	size        int64
	appendOff   int64
	clusters    map[int64]cowClusterRef
}

// OpenCOWStorage creates or opens a persistent copy-on-write overlay for base.
func OpenCOWStorage(base BlockStorage, overlay *os.File) (*COWStorage, error) {
	return openCOWStorage(base, overlay, defaultCOWClusterSize)
}

func openCOWStorage(base BlockStorage, overlay *os.File, clusterSize int64) (*COWStorage, error) {
	if base == nil {
		return nil, fmt.Errorf("cow storage requires a base device")
	}
	if overlay == nil {
		return nil, fmt.Errorf("cow storage requires an overlay file")
	}
	if clusterSize <= 0 {
		return nil, fmt.Errorf("cow storage cluster size must be positive")
	}

	size, err := base.Size()
	if err != nil {
		return nil, fmt.Errorf("cow storage base size: %w", err)
	}

	st, err := overlay.Stat()
	if err != nil {
		return nil, fmt.Errorf("cow storage stat overlay: %w", err)
	}

	cs := &COWStorage{
		Base:        base,
		Overlay:     overlay,
		clusterSize: clusterSize,
		size:        size,
		clusters:    make(map[int64]cowClusterRef),
	}

	if st.Size() == 0 {
		if err := cs.writeHeader(); err != nil {
			return nil, err
		}
		return cs, nil
	}

	if err := cs.load(); err != nil {
		return nil, err
	}
	if cs.size != size {
		return nil, fmt.Errorf("cow storage base size mismatch: overlay=%d base=%d", cs.size, size)
	}

	return cs, nil
}

func (cs *COWStorage) ReadAt(p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	cs.mu.RLock()
	defer cs.mu.RUnlock()

	if off < 0 {
		return 0, fmt.Errorf("cow storage read negative offset %d", off)
	}
	if off >= cs.size {
		return 0, io.EOF
	}

	total := 0
	for total < len(p) && off+int64(total) < cs.size {
		curOff := off + int64(total)
		cluster := curOff / cs.clusterSize
		clusterOff := curOff % cs.clusterSize
		chunk := minInt64(int64(len(p)-total), cs.clusterDataLen(cluster)-clusterOff)

		var n int
		var err error
		if ref, ok := cs.clusters[cluster]; ok {
			n, err = cs.Overlay.ReadAt(p[total:total+int(chunk)], ref.dataOffset+clusterOff)
		} else {
			n, err = cs.Base.ReadAt(p[total:total+int(chunk)], curOff)
		}
		total += n
		if err != nil && err != io.EOF {
			return total, err
		}
		if int64(n) != chunk {
			return total, io.EOF
		}
	}

	if total < len(p) {
		return total, io.EOF
	}
	return total, nil
}

func (cs *COWStorage) WriteAt(p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	cs.mu.Lock()
	defer cs.mu.Unlock()

	if off < 0 {
		return 0, fmt.Errorf("cow storage write negative offset %d", off)
	}
	if off >= cs.size {
		return 0, io.EOF
	}

	requested := len(p)
	remaining := cs.size - off
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}

	written := 0
	for written < len(p) {
		curOff := off + int64(written)
		cluster := curOff / cs.clusterSize
		clusterOff := int(curOff % cs.clusterSize)
		chunk := int(minInt64(int64(len(p)-written), cs.clusterSize-int64(clusterOff)))

		buf := make([]byte, cs.clusterSize)
		if err := cs.readClusterLocked(cluster, buf); err != nil {
			return written, err
		}
		copy(buf[clusterOff:clusterOff+chunk], p[written:written+chunk])

		if err := cs.appendClusterLocked(cluster, buf); err != nil {
			return written, err
		}
		written += chunk
	}

	if written < len(p) || written < requested {
		return written, io.ErrShortWrite
	}
	return written, nil
}

func (cs *COWStorage) Size() (int64, error) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.size, nil
}

func (cs *COWStorage) Sync() error {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.Overlay.Sync()
}

func (cs *COWStorage) writeHeader() error {
	h := cowStorageHeader{
		Magic:       cowStorageMagic,
		Version:     1,
		ClusterSize: uint32(cs.clusterSize),
		Size:        uint64(cs.size),
	}
	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.LittleEndian, h); err != nil {
		return fmt.Errorf("cow storage encode header: %w", err)
	}
	if _, err := cs.Overlay.WriteAt(buf.Bytes(), 0); err != nil {
		return fmt.Errorf("cow storage write header: %w", err)
	}
	cs.appendOff = int64(buf.Len())
	return nil
}

func (cs *COWStorage) load() error {
	var h cowStorageHeader
	size := int64(binary.Size(h))
	buf := make([]byte, size)
	if _, err := io.ReadFull(io.NewSectionReader(cs.Overlay, 0, size), buf); err != nil {
		return fmt.Errorf("cow storage read header: %w", err)
	}
	if err := binary.Read(bytes.NewReader(buf), binary.LittleEndian, &h); err != nil {
		return fmt.Errorf("cow storage decode header: %w", err)
	}
	if h.Magic != cowStorageMagic {
		return fmt.Errorf("cow storage magic mismatch")
	}
	if h.Version != 1 {
		return fmt.Errorf("cow storage version %d unsupported", h.Version)
	}
	if h.ClusterSize == 0 {
		return fmt.Errorf("cow storage invalid cluster size 0")
	}

	cs.clusterSize = int64(h.ClusterSize)
	cs.size = int64(h.Size)
	cs.appendOff = size

	st, err := cs.Overlay.Stat()
	if err != nil {
		return fmt.Errorf("cow storage stat overlay: %w", err)
	}
	entrySize := int64(8) + cs.clusterSize
	for cs.appendOff < st.Size() {
		if st.Size()-cs.appendOff < entrySize {
			return fmt.Errorf("cow storage overlay truncated at offset %d", cs.appendOff)
		}

		clusterBuf := make([]byte, 8)
		if _, err := cs.Overlay.ReadAt(clusterBuf, cs.appendOff); err != nil {
			return fmt.Errorf("cow storage read record header: %w", err)
		}
		cluster := int64(binary.LittleEndian.Uint64(clusterBuf))
		cs.clusters[cluster] = cowClusterRef{dataOffset: cs.appendOff + 8}
		cs.appendOff += entrySize
	}

	return nil
}

func (cs *COWStorage) readClusterLocked(cluster int64, dst []byte) error {
	if int64(len(dst)) < cs.clusterSize {
		return fmt.Errorf("cow storage cluster buffer too small")
	}
	clear(dst)
	if ref, ok := cs.clusters[cluster]; ok {
		if _, err := io.ReadFull(io.NewSectionReader(cs.Overlay, ref.dataOffset, cs.clusterSize), dst[:cs.clusterSize]); err != nil {
			return fmt.Errorf("cow storage read overlay cluster %d: %w", cluster, err)
		}
		return nil
	}

	valid := cs.clusterDataLen(cluster)
	if valid <= 0 {
		return nil
	}
	if _, err := cs.Base.ReadAt(dst[:valid], cluster*cs.clusterSize); err != nil && err != io.EOF {
		return fmt.Errorf("cow storage read base cluster %d: %w", cluster, err)
	}
	return nil
}

func (cs *COWStorage) appendClusterLocked(cluster int64, data []byte) error {
	if int64(len(data)) < cs.clusterSize {
		return fmt.Errorf("cow storage cluster data too short")
	}

	head := make([]byte, 8)
	binary.LittleEndian.PutUint64(head, uint64(cluster))
	if _, err := cs.Overlay.WriteAt(head, cs.appendOff); err != nil {
		return fmt.Errorf("cow storage write record header: %w", err)
	}
	dataOff := cs.appendOff + int64(len(head))
	if _, err := cs.Overlay.WriteAt(data[:cs.clusterSize], dataOff); err != nil {
		return fmt.Errorf("cow storage write cluster: %w", err)
	}
	cs.clusters[cluster] = cowClusterRef{dataOffset: dataOff}
	cs.appendOff = dataOff + cs.clusterSize
	return nil
}

func (cs *COWStorage) clusterDataLen(cluster int64) int64 {
	remaining := cs.size - cluster*cs.clusterSize
	if remaining <= 0 {
		return 0
	}
	if remaining < cs.clusterSize {
		return remaining
	}
	return cs.clusterSize
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
