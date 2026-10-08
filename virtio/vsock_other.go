//go:build !linux

package virtio

import "fmt"

type VSOCK struct{}

func (*VSOCK) Read([]byte) (int, error) {
	return 0, fmt.Errorf("virtio-vsock backend is only supported on linux")
}

func (*VSOCK) Write([]byte) (int, error) {
	return 0, fmt.Errorf("virtio-vsock backend is only supported on linux")
}

func (*VSOCK) Close() error {
	return nil
}

func OpenVSOCK(port uint32) (*VSOCK, error) {
	return nil, fmt.Errorf("virtio-vsock backend is only supported on linux")
}
