//go:build linux

package virtio

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// OpenTAP creates or opens a TAP device and returns it with its final name.
// If name is empty, the kernel assigns a name (for example, tap0).
func OpenTAP(name string) (*os.File, string, error) {
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
	return os.NewFile(uintptr(fd), finalName), finalName, nil
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
