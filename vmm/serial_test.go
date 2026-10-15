package vmm

import (
	"bytes"
	"encoding/binary"
	"testing"

	"kube-vm/kvm"
)

func TestSerialDeviceWritesTHRToOutput(t *testing.T) {
	var out bytes.Buffer
	dev := newSerialDevice(nil, &out)

	if err := dev.write(serialCOM1Base, 1, []byte{'A'}); err != nil {
		t.Fatalf("write returned error: %v", err)
	}
	if got := out.String(); got != "A" {
		t.Fatalf("output = %q, want %q", got, "A")
	}
}

func TestSerialDeviceReportsTransmitReady(t *testing.T) {
	dev := newSerialDevice(nil, nil)

	buf := []byte{0}
	if err := dev.read(serialCOM1Base+5, 1, buf); err != nil {
		t.Fatalf("read returned error: %v", err)
	}
	if got := buf[0]; got != byte(serialLSRTHRE|serialLSRTEMT) {
		t.Fatalf("lsr = %#x, want %#x", got, byte(serialLSRTHRE|serialLSRTEMT))
	}
}

func TestSerialDeviceStoresLineControl(t *testing.T) {
	dev := newSerialDevice(nil, nil)
	if err := dev.write(serialCOM1Base+3, 1, []byte{0x83}); err != nil {
		t.Fatalf("write lcr returned error: %v", err)
	}
	buf := []byte{0}
	if err := dev.read(serialCOM1Base+3, 1, buf); err != nil {
		t.Fatalf("read lcr returned error: %v", err)
	}
	if got := buf[0]; got != 0x83 {
		t.Fatalf("lcr = %#x, want %#x", got, byte(0x83))
	}
}

func TestHandleIOPCICfgAddressRoundTrip(t *testing.T) {
	vm := &VM{}
	addrBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(addrBytes, 0x80001010)
	if err := vm.handleIO(&kvm.IOExitData{IsOut: true, Size: 4, Port: pciConfigAddrPort, Count: 1}, addrBytes); err != nil {
		t.Fatalf("handleIO write returned error: %v", err)
	}
	readBack := make([]byte, 4)
	if err := vm.handleIO(&kvm.IOExitData{IsOut: false, Size: 4, Port: pciConfigAddrPort, Count: 1}, readBack); err != nil {
		t.Fatalf("handleIO read returned error: %v", err)
	}
	if got := binary.LittleEndian.Uint32(readBack); got != 0x80001010 {
		t.Fatalf("pci config addr = %#x, want %#x", got, uint32(0x80001010))
	}
}

func TestHandleIOPCICfgDataReadsAsNoDevice(t *testing.T) {
	vm := &VM{}
	buf := make([]byte, 4)
	if err := vm.handleIO(&kvm.IOExitData{IsOut: false, Size: 4, Port: pciConfigDataPort, Count: 1}, buf); err != nil {
		t.Fatalf("handleIO read returned error: %v", err)
	}
	if got := binary.LittleEndian.Uint32(buf); got != 0xffffffff {
		t.Fatalf("pci config data = %#x, want %#x", got, uint32(0xffffffff))
	}
}

func TestHandleIOUnknownReadZerosBuffer(t *testing.T) {
	vm := &VM{}
	buf := []byte{1, 2, 3, 4}
	if err := vm.handleIO(&kvm.IOExitData{IsOut: false, Size: 1, Port: 0x1234, Count: 4}, buf); err != nil {
		t.Fatalf("handleIO unknown read returned error: %v", err)
	}
	if got := buf; !bytes.Equal(got, []byte{0, 0, 0, 0}) {
		t.Fatalf("unknown read buffer = %#v, want zeros", got)
	}
}
