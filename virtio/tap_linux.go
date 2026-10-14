//go:build linux

package virtio

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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
	if _, err := exec.LookPath("ip"); err == nil {
		_ = cleanupStaleTAPs()
	}

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

func cleanupStaleTAPs() error {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "tap") || len(name) <= 3 {
			continue
		}

		state, err := os.ReadFile(filepath.Join("/sys/class/net", name, "operstate"))
		if err != nil {
			continue
		}
		stateStr := strings.TrimSpace(string(state))
		if stateStr == "down" || stateStr == "unknown" {
			if err := exec.Command("ip", "link", "del", "dev", name).Run(); err != nil {
				continue
			}
		}
	}
	return nil
}

func staleTapNames(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	var stale []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "tap") || len(name) <= 3 {
			continue
		}

		state, err := os.ReadFile(filepath.Join(root, name, "operstate"))
		if err != nil {
			continue
		}
		stateStr := strings.TrimSpace(string(state))
		if stateStr == "down" || stateStr == "unknown" {
			stale = append(stale, name)
		}
	}
	return stale, nil
}

func RemoveTAP(name string) error {
	if name == "" {
		return nil
	}

	if _, err := exec.LookPath("ip"); err == nil {
		_ = exec.Command("ip", "link", "set", "dev", name, "down").Run()
		for i := 0; i < 10; i++ {
			if err := exec.Command("ip", "link", "del", "dev", name).Run(); err == nil {
				return nil
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join("/sys/class/net", name)); err != nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("tap %s still exists after cleanup", name)
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
		if err := RemoveTAP(t.name); err != nil {
			_ = t.File.Close()
			return err
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
