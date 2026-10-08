//go:build linux && amd64

package linux_test

import (
	"context"
	"os"
	"testing"

	"kube-vm/os/linux"
	"kube-vm/vmm"
)

func TestLoaderReboot(t *testing.T) {
	bzImage, err := os.ReadFile("../../.build/linux/guest/arch/x86/boot/bzImage")
	if err != nil {
		t.Fatal(err)
	}

	initrd, err := os.ReadFile("../../.build/initrd.cpio.gz")
	if err != nil {
		t.Fatal(err)
	}

	cfg := vmm.Config{
		Loader: &linux.Loader{
			Kernel:  bzImage,
			Initrd:  initrd,
			Cmdline: "reboot=t rdinit=/sbin/reboot -- -f",
		},
	}

	m, err := vmm.New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if err := m.Run(context.Background()); err != nil {
		t.Error(err)
	}
}
