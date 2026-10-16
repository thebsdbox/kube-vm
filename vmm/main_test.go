package vmm_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"

	"kube-vm/os/linux"
	"kube-vm/virtio"
	"kube-vm/vmm"

	"github.com/cavaliergopher/cpio"
	"golang.org/x/sys/unix"
)

type GuestTest struct {
	Host  func(t *testing.T, runGuest func(vmm.Config))
	Guest func(t *testing.T)
}

// guest is set to the string "guest" using -ldflags "-X ..." when the test
// binary destined to become /init is built. The isGuest and isHost bools
// use this value to decide where the test is running.
var guest string

var (
	isGuest = guest == "guest"
	isHost  = !isGuest
)

var kernelBytes []byte
var initrdBytes []byte

var guestImageOnce sync.Once
var guestImageErr error
var guestImageSkip string

func TestMain(m *testing.M) {
	if isGuest {
		m.Run()

		// If the tests are running in the VM, the exit code returned by Run is
		// discarded because the kernel really doesn't like it when PID 1 exits.
		// Instead, we reboot! The host can figure out what happened by parsing
		// console output.

		if err := unix.Reboot(syscall.LINUX_REBOOT_CMD_RESTART); err != nil {
			panic(err)
		}
		return
	}

	// Unit tests must not require a kernel, KVM, or a recursive guest build.
	os.Exit(m.Run())
}

func prepareGuestImage() {
	kb, err := os.ReadFile("../bzImage")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			guestImageSkip = fmt.Sprintf("guest tests require ../bzImage: %v", err)
		} else {
			guestImageErr = fmt.Errorf("read guest kernel: %w", err)
		}
		return
	}

	kernelBytes = kb
	if _, err := exec.LookPath("go"); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			guestImageSkip = fmt.Sprintf("guest tests require the Go toolchain: %v", err)
		} else {
			guestImageErr = err
		}
		return
	}

	// build static test exe
	exe := new(bytes.Buffer)
	build := exec.Command("go", "test", "-c", "-o", "/dev/stdout",
		"-tags", "guest,netgo",
		"-ldflags", "-X kube-vm/vmm_test.guest=guest", ".")
	build.Stdout = exe
	build.Stderr = os.Stderr
	build.Env = append(os.Environ(), "CGO_ENABLED=0")

	if err := build.Run(); err != nil {
		guestImageErr = fmt.Errorf("build guest test binary: %w", err)
		return
	}

	// generate initrd
	ib := new(bytes.Buffer)
	zw := gzip.NewWriter(ib)
	cw := cpio.NewWriter(zw)

	err = cw.WriteHeader(&cpio.Header{
		Name: "init",
		Mode: 0755,
		Size: int64(exe.Len()),
	})

	if err != nil {
		guestImageErr = fmt.Errorf("write initrd header: %w", err)
		return
	}

	if _, err := cw.Write(exe.Bytes()); err != nil {
		guestImageErr = fmt.Errorf("write initrd executable: %w", err)
		return
	}

	if err := cw.Close(); err != nil {
		guestImageErr = fmt.Errorf("close initrd archive: %w", err)
		return
	}

	if err := zw.Close(); err != nil {
		guestImageErr = fmt.Errorf("close initrd gzip: %w", err)
		return
	}

	initrdBytes = ib.Bytes()
}

// requireKVM only skips known host prerequisites. In particular, errors from
// creating/configuring/running a VM are test failures, not reasons to skip.
func requireKVM(t *testing.T) {
	t.Helper()
	f, err := os.Open("/dev/kvm")
	if err != nil {
		for _, unavailable := range []error{
			os.ErrNotExist, os.ErrPermission, unix.ENODEV, unix.ENXIO, unix.ENOSYS,
		} {
			if errors.Is(err, unavailable) {
				t.Skipf("requires accessible /dev/kvm: %v", err)
			}
		}
		t.Fatalf("probe /dev/kvm: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close /dev/kvm probe: %v", err)
	}
}

// testing splits -test.run at slashes; anchor and quote each component so a
// guest cannot accidentally run sibling tests (or interpret name metacharacters).
func guestTestPattern(name string) string {
	parts := strings.Split(name, "/")
	for i, part := range parts {
		parts[i] = "^" + regexp.QuoteMeta(part) + "$"
	}
	return strings.Join(parts, "/")
}

func (gt GuestTest) Run(t *testing.T) {
	t.Helper()

	switch {
	case isHost:
		t.Run("host", func(tt *testing.T) {
			requireKVM(tt)
			guestImageOnce.Do(prepareGuestImage)
			if guestImageSkip != "" {
				tt.Skip(guestImageSkip)
			}
			if guestImageErr != nil {
				tt.Fatal(guestImageErr)
			}
			gt.Host(tt, func(cfg vmm.Config) {
				runGuest(tt, t.Name()+"/guest", cfg)
			})
		})

	case isGuest:
		t.Run("guest", gt.Guest)
	}
}

func runGuest(t *testing.T, testName string, cfg vmm.Config) {
	t.Helper()

	if cfg.Loader != nil {
		t.Fatal("loader must be nil")
	}

	var console *virtio.ConsoleDevice
	for _, dev := range cfg.Devices {
		if c, ok := dev.(*virtio.ConsoleDevice); ok {
			console = c
		}
	}

	if console == nil {
		console = new(virtio.ConsoleDevice)
		cfg.Devices = append(cfg.Devices, console)
	}

	out := new(bytes.Buffer)
	outs := []io.Writer{out}
	if console.Out != nil {
		outs = append(outs, console.Out)
	}

	if testing.Verbose() {
		outs = append(outs, os.Stdout)
	}

	console.Out = io.MultiWriter(outs...)

	cfg.Loader = &linux.Loader{
		Kernel:  kernelBytes,
		Initrd:  initrdBytes,
		Cmdline: fmt.Sprintf("reboot=t console=hvc0 -- -test.v -test.run=%s", guestTestPattern(testName)),
	}

	m, err := vmm.New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if err := m.Run(context.Background()); err != nil {
		t.Error(err)
	}

	if err := m.Close(); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "\r\nPASS\r\n") {
		t.Fatalf("guest did not report PASS; console output:\n%s", out.String())
	}
}
