//go:build linux

package virtio

import (
	"fmt"
	"io"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// VSOCK is a listening AF_VSOCK backend for a virtio-vsock device.
type VSOCK struct {
	fd   int
	conn *os.File
	mu   sync.Mutex
}

func OpenVSOCK(port uint32) (*VSOCK, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("open vsock socket: %w", err)
	}

	addr := &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: port}
	if err := unix.Bind(fd, addr); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("bind vsock socket: %w", err)
	}
	if err := unix.Listen(fd, 1); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("listen vsock socket: %w", err)
	}

	return &VSOCK{fd: fd}, nil
}

func (v *VSOCK) ensureConn() error {
	if v.conn != nil {
		return nil
	}
	fd, _, err := unix.Accept(v.fd)
	if err != nil {
		return err
	}
	v.conn = os.NewFile(uintptr(fd), fmt.Sprintf("vsock:%d", v.fd))
	return nil
}

func (v *VSOCK) Read(p []byte) (int, error) {
	if v == nil {
		return 0, io.EOF
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.ensureConn(); err != nil {
		return 0, err
	}
	return v.conn.Read(p)
}

func (v *VSOCK) Write(p []byte) (int, error) {
	if v == nil {
		return 0, io.ErrClosedPipe
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.ensureConn(); err != nil {
		return 0, err
	}
	return v.conn.Write(p)
}

func (v *VSOCK) Close() error {
	if v == nil {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.conn != nil {
		_ = v.conn.Close()
		v.conn = nil
	}
	if v.fd != 0 {
		_ = unix.Close(v.fd)
		v.fd = 0
	}
	return nil
}
