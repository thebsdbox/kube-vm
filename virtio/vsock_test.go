//go:build linux

package virtio

import (
	"encoding/binary"
	"testing"
)

func TestVSockDeviceNewHandlerRejectsReservedCID(t *testing.T) {
	if _, err := (VSockDevice{GuestCID: 2, Backend: &VSOCK{}}).NewHandler(); err == nil {
		t.Fatal("expected reserved guest CID to be rejected")
	}
}

func TestVSockDeviceReadConfig(t *testing.T) {
	h, err := (VSockDevice{GuestCID: 0x1122334455667788, Backend: &VSOCK{}}).NewHandler()
	if err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 8)
	if err := h.ReadConfig(buf, 0); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint64(buf); got != 0x1122334455667788 {
		t.Fatalf("guest CID mismatch: got %#x", got)
	}
}
