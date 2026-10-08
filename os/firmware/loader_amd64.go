//go:build linux && amd64

package firmware

import (
	"fmt"

	"kube-vm/kvm"
	"kube-vm/vmm"
)

// Loader prepares the VM to boot from a firmware image in real mode.
//
// The firmware blob is copied into low memory and execution starts at the
// beginning of that blob for VCPU0.
type Loader struct {
	Firmware []byte

	entry uint64
}

const lowMemLimit = 0x100000 // 1 MiB

func (l *Loader) LoadMemory(info vmm.VMInfo, mem []byte) error {
	if len(l.Firmware) == 0 {
		return fmt.Errorf("firmware loader: firmware image is empty")
	}

	if len(l.Firmware) > lowMemLimit {
		return fmt.Errorf("firmware loader: firmware image too large: %d > %d", len(l.Firmware), lowMemLimit)
	}

	if len(mem) < lowMemLimit {
		return fmt.Errorf("firmware loader: guest memory must be at least %d bytes", lowMemLimit)
	}

	addr := lowMemLimit - len(l.Firmware)
	copy(mem[addr:addr+len(l.Firmware)], l.Firmware)
	l.entry = uint64(addr)

	return nil
}

func (l *Loader) LoadVCPU(info vmm.VMInfo, slot int, regs *kvm.Regs, sregs *kvm.Sregs) error {
	if slot != 0 {
		return nil
	}

	selector := uint16(l.entry >> 4)
	base := uint64(selector) << 4
	offset := l.entry - base

	realModeCode := kvm.Segment{
		Base:     base,
		Limit:    0xffff,
		Selector: selector,
		Type:     0xb,
		Present:  0x1,
		S:        0x1,
		DB:       0x1,
		G:        0x0,
	}

	realModeData := kvm.Segment{
		Base:     0,
		Limit:    0xffff,
		Selector: 0,
		Type:     0x3,
		Present:  0x1,
		S:        0x1,
		DB:       0x1,
		G:        0x0,
	}

	sregs.CS = realModeCode
	sregs.DS = realModeData
	sregs.ES = realModeData
	sregs.FS = realModeData
	sregs.GS = realModeData
	sregs.SS = realModeData

	regs.RIP = offset
	regs.RFlags = 0x2

	return nil
}
