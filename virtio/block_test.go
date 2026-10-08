package virtio

import (
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"kube-vm/virtio/virtq"
)

type syncStorage struct {
	buf        []byte
	syncCalled bool
	syncErr    error
}

func (s *syncStorage) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(s.buf)) {
		return 0, io.EOF
	}
	n := copy(p, s.buf[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (s *syncStorage) WriteAt(p []byte, off int64) (int, error) {
	end := int(off) + len(p)
	if end > len(s.buf) {
		grown := make([]byte, end)
		copy(grown, s.buf)
		s.buf = grown
	}
	return copy(s.buf[off:], p), nil
}

func (s *syncStorage) Size() (int64, error) {
	return int64(len(s.buf)), nil
}

func (s *syncStorage) Sync() error {
	s.syncCalled = true
	return s.syncErr
}

func TestBlockHandlerGetFeaturesAdvertisesFlushForSyncableStorage(t *testing.T) {
	h, err := (BlockDevice{Storage: &syncStorage{buf: make([]byte, 4096)}}).NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	features := h.GetFeatures()
	if features&blkFFlush == 0 {
		t.Fatalf("features %#x do not advertise flush", features)
	}
}

func TestBlockHandlerOutRequestReportsStatusByteLength(t *testing.T) {
	storage := &syncStorage{buf: make([]byte, 4096)}
	h := &blockHandler{cfg: BlockDevice{Storage: storage}, r: storage, w: storage, s: storage}

	hdr := make([]byte, 16)
	binary.LittleEndian.PutUint32(hdr[0:], blkTOut)
	binary.LittleEndian.PutUint64(hdr[8:], 0)
	data := []byte("abcd")
	status := []byte{0xff}

	ring := []virtq.Desc{
		{Addr: 1, Len: uint32(len(hdr)), Flags: virtq.DescFAvail | virtq.DescFNext},
		{Addr: 2, Len: uint32(len(data)), Flags: virtq.DescFAvail | virtq.DescFNext},
		{Addr: 3, Len: uint32(len(status)), Flags: virtq.DescFAvail | virtq.DescFWrite},
	}

	q := virtq.New(ring, new(virtq.EventSuppress), nil, virtq.Config{
		MemAt: func(addr uint64, length int) ([]byte, error) {
			switch addr {
			case 1:
				return hdr, nil
			case 2:
				return data, nil
			case 3:
				return status, nil
			default:
				return nil, errors.New("unexpected address")
			}
		},
	})

	if err := h.handle(q); err != nil {
		t.Fatal(err)
	}
	if ring[0].Len != 1 {
		t.Fatalf("used length = %d, want 1 status byte", ring[0].Len)
	}
	if status[0] != blkSOK {
		t.Fatalf("status = %d, want %d", status[0], blkSOK)
	}
	if got := string(storage.buf[:len(data)]); got != string(data) {
		t.Fatalf("write payload = %q, want %q", got, data)
	}
}

func TestBlockHandlerFlushRequestSyncsStorage(t *testing.T) {
	storage := &syncStorage{buf: make([]byte, 4096)}
	h := &blockHandler{cfg: BlockDevice{Storage: storage}, r: storage, w: storage, s: storage}

	hdr := make([]byte, 16)
	binary.LittleEndian.PutUint32(hdr[0:], blkTFlush)
	status := []byte{0xff}

	ring := []virtq.Desc{
		{Addr: 1, Len: uint32(len(hdr)), Flags: virtq.DescFAvail | virtq.DescFNext},
		{Addr: 2, Len: 0, Flags: virtq.DescFAvail | virtq.DescFNext},
		{Addr: 3, Len: uint32(len(status)), Flags: virtq.DescFAvail | virtq.DescFWrite},
	}

	q := virtq.New(ring, new(virtq.EventSuppress), nil, virtq.Config{
		MemAt: func(addr uint64, length int) ([]byte, error) {
			switch addr {
			case 1:
				return hdr, nil
			case 2:
				return nil, nil
			case 3:
				return status, nil
			default:
				return nil, errors.New("unexpected address")
			}
		},
	})

	if err := h.handle(q); err != nil {
		t.Fatal(err)
	}
	if !storage.syncCalled {
		t.Fatal("flush request did not call Sync")
	}
	if ring[0].Len != 1 {
		t.Fatalf("used length = %d, want 1 status byte", ring[0].Len)
	}
	if status[0] != blkSOK {
		t.Fatalf("status = %d, want %d", status[0], blkSOK)
	}
}
