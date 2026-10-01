//go:build linux

package virtio

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// TapDevice is a TAP-backed network file descriptor with explicit teardown semantics.
type TapDevice struct {
	*os.File
	name string
}

// OpenTAP creates or opens a TAP device and returns it with its final name.
// If name is empty, the kernel assigns a name (for example, tap0).
func OpenTAP(name string) (*TapDevice, string, error) {
	fd, err := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", fmt.Errorf("open /dev/net/tun: %w", err)
	}

	ifreq := ifreqFlags{Flags: unix.IFF_TAP | unix.IFF_NO_PI}
	copy(ifreq.Name[:], name)

	_, _, errno := unix.Syscall(unix.SYS_IOCTL,
		uintptr(fd),
		uintptr(unix.TUNSETIFF),
		uintptr(unsafe.Pointer(&ifreq)))

	if errno != 0 {
		unix.Close(fd)
		return nil, "", fmt.Errorf("ioctl TUNSETIFF: %w", errno)
	}

	finalName := cstring(ifreq.Name[:])
	return &TapDevice{File: os.NewFile(uintptr(fd), finalName), name: finalName}, finalName, nil
}

func (t *TapDevice) Close() error {
	if t == nil || t.File == nil {
		return nil
	}

	if t.name != "" {
		if err := unix.IoctlSetPointerInt(int(t.File.Fd()), unix.TUNSETPERSIST, 0); err != nil {
			if err != unix.ENOTTY && err != unix.ENODEV && err != unix.EINVAL {
				_ = t.File.Close()
				return err
			}
		}
	}

	return t.File.Close()
}

type ifreqFlags struct {
	Name  [unix.IFNAMSIZ]byte
	Flags uint16
	_     [40 - unix.IFNAMSIZ - 2]byte
}

func cstring(p []byte) string {
	for i, b := range p {
		if b == 0 {
			return string(p[:i])
		}
	}

	return string(p)
}
