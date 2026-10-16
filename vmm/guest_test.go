package vmm_test

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"kube-vm/virtio"
	"kube-vm/vmm"
)

func TestGuestTestPattern(t *testing.T) {
	for _, name := range []string{"TestConsole/guest", "TestConsole/case.+(1)/guest"} {
		t.Run(name, func(t *testing.T) {
			parts := strings.Split(name, "/")
			patterns := strings.Split(guestTestPattern(name), "/")
			if len(patterns) != len(parts) {
				t.Fatalf("pattern components: got %d want %d", len(patterns), len(parts))
			}
			for i, pattern := range patterns {
				re, err := regexp.Compile(pattern)
				if err != nil {
					t.Fatal(err)
				}
				if !re.MatchString(parts[i]) || re.MatchString(parts[i]+"Sibling") || re.MatchString("Prefix"+parts[i]) {
					t.Fatalf("pattern %q does not match only the exact component %q", pattern, parts[i])
				}
			}
		})
	}
}

func TestConsole(t *testing.T) {
	GuestTest{
		Host: func(t *testing.T, runGuest func(vmm.Config)) {
			out := new(bytes.Buffer)
			runGuest(vmm.Config{
				Devices: []virtio.DeviceConfig{
					&virtio.ConsoleDevice{
						Out: out,
					},
				},
			})

			if !strings.Contains(out.String(), "hello from the guest") {
				t.Error("the guest didn't say hello")
			}
		},

		Guest: func(t *testing.T) {
			if _, err := fmt.Fprintln(os.Stdout, "hello from the guest"); err != nil {
				t.Fatal(err)
			}
		},
	}.Run(t)
}
