//go:build linux && amd64

package kvm

import (
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"slices"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ABI definitions and ioctl numbers below follow linux/kvm.h and asm/kvm.h.
// In particular SET_IRQCHIP is _IOR, not _IOW, and XSAVE2 still encodes 4096.
const (
	kGetIRQChip        = 0xc208ae62
	kSetIRQChip        = 0x8208ae63
	kGetLAPIC          = 0x8400ae8e
	kSetLAPIC          = 0x4400ae8f
	kGetCPUID2         = 0xc008ae91
	kGetVCPUEvents     = 0x8040ae9f
	kSetVCPUEvents     = 0x4040aea0
	kGetDebugRegs      = 0x8080aea1
	kSetDebugRegs      = 0x4080aea2
	kGetXSave          = 0x9000aea4
	kSetXSave          = 0x5000aea5
	kGetXCRs           = 0x8188aea6
	kSetXCRs           = 0x4188aea7
	kGetPIT2           = 0x8070ae9f
	kSetPIT2           = 0x4070aea0
	kSetTSCKHz         = 0xaea2
	kGetTSCKHz         = 0xaea3
	kGetXSave2         = 0x9000aecf
	maxSnapshotEntries = 1 << 20
)

// LAPICState has the layout of struct kvm_lapic_state.
type LAPICState struct {
	Regs [1024]byte
}

// VCPUEvents has the layout of struct kvm_vcpu_events, including payload and
// triple-fault fields. Flags returned by GET must be retained for SET.
type VCPUEvents struct {
	Exception struct {
		Injected, Number, HasErrorCode, Pending uint8
		ErrorCode                               uint32
	}
	Interrupt           struct{ Injected, Number, Soft, Shadow uint8 }
	NMI                 struct{ Injected, Pending, Masked, Padding uint8 }
	SIPIVector          uint32
	Flags               uint32
	SMI                 struct{ SMM, Pending, SMMInsideNMI, LatchedInit uint8 }
	TripleFaultPending  uint8
	Reserved            [26]byte
	ExceptionHasPayload uint8
	ExceptionPayload    uint64
}

// DebugRegs has the layout of struct kvm_debugregs (not KVM_SET_GUEST_DEBUG).
type DebugRegs struct {
	DB              [4]uint64
	DR6, DR7, Flags uint64
	Reserved        [9]uint64
}

// XCR has the layout of struct kvm_xcr.
type XCR struct {
	Index, Reserved uint32
	Value           uint64
}

// XCRState has the layout of struct kvm_xcrs.
type XCRState struct {
	Count, Flags uint32
	XCRs         [16]XCR
	Padding      [16]uint64
}

// IRQChipState holds a PIC (ID 0/1) or IOAPIC (ID 2), including the entire
// 512-byte ABI union. Chip begins at offset 8 and the ABI size is 520 bytes.
type IRQChipState struct {
	_       [0]uint64 // The IOAPIC member gives the C union 8-byte alignment.
	ID      uint32
	Padding uint32
	Chip    [512]byte
}

// PITChannelState has the layout of struct kvm_pit_channel_state.
type PITChannelState struct {
	Count                                                                  uint32
	LatchedCount                                                           uint16
	CountLatched, StatusLatched, Status, ReadState, WriteState, WriteLatch uint8
	RWMode, Mode, BCD, Gate                                                uint8
	CountLoadTime                                                          int64
}

// PITState has the layout of struct kvm_pit_state2.
type PITState struct {
	Channels [3]PITChannelState
	Flags    uint32
	Reserved [9]uint32
}

// CPUState is hardware state for a stopped vCPU, not a complete VM snapshot.
// Nil XCRs/Debug and empty XSAVE indicate unavailable source capabilities;
// TSCKHz == 0 means KVM_CAP_GET_TSC_KHZ was unavailable. HostCPUID is a
// compatibility fingerprint, NOT the configured guest CPUID in CPUID.
// This implementation deliberately requires identical supported host CPUID
// tables on restore; a different CPU/kernel may be rejected even if compatible.
// Guests advertising nested VMX/SVM are refused (nested state is not captured).
type CPUState struct {
	Regs      Regs
	Sregs     Sregs
	FPU       FPU
	MP        MPState
	LAPIC     LAPICState
	Events    VCPUEvents
	XSAVE     []byte
	XCRs      *XCRState
	Debug     *DebugRegs
	MSRs      []MSREntry
	CPUID     []CPUIDEntry2
	HostCPUID []CPUIDEntry2
	TSCKHz    uint32
}

// VMState assumes an in-kernel PIC/IOAPIC and PIT2, as created by SetupVM.
// Clock is nil only when KVM_CAP_ADJUST_CLOCK is unavailable.
type VMState struct {
	IRQChips [3]IRQChipState
	PIT      PITState
	Clock    *ClockData
}

type snapshotFD interface{ Fd() uintptr }

type snapshotCall func(snapshotFD, uintptr, unsafe.Pointer) (uintptr, error)

func snapshotIOCTL(f snapshotFD, request uintptr, data unsafe.Pointer) (uintptr, error) {
	r, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), request, uintptr(data))
	runtime.KeepAlive(data)
	runtime.KeepAlive(f)
	if errno != 0 {
		return 0, errno
	}
	return r, nil
}

func snapshotOp(f snapshotFD, request uintptr, data unsafe.Pointer, name string) error {
	_, err := snapshotIOCTL(f, request, data)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// Flexible arrays use uint64 backing storage to guarantee natural alignment.
func snapshotBuffer(header, count, stride int) ([]uint64, error) {
	if count < 0 || count > maxSnapshotEntries || stride <= 0 {
		return nil, fmt.Errorf("invalid KVM array length %d", count)
	}
	return make([]uint64, (header+count*stride+7)/8), nil
}

func getMSRIndices(f snapshotFD, request uintptr) ([]int, error) {
	return getMSRIndicesWith(f, request, snapshotIOCTL)
}

func getMSRIndicesWith(f snapshotFD, request uintptr, call snapshotCall) ([]int, error) {
	count := 256
	for attempts := 0; attempts < 8; attempts++ {
		buf, err := snapshotBuffer(4, count, 4)
		if err != nil {
			return nil, err
		}
		p := unsafe.Pointer(&buf[0])
		*(*uint32)(p) = uint32(count)
		_, err = call(f, request, p)
		n := int(*(*uint32)(p))
		if err == unix.E2BIG && n > count {
			count = n
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("KVM MSR index list: %w", err)
		}
		if n > count {
			return nil, fmt.Errorf("KVM returned oversized MSR list: %d > %d", n, count)
		}
		indices := make([]int, n)
		for i, v := range unsafe.Slice((*uint32)(unsafe.Add(p, 4)), n) {
			indices[i] = int(v)
		}
		return indices, nil
	}
	return nil, fmt.Errorf("KVM MSR index list keeps growing")
}

// MSRCountError reports the successfully transferred prefix. KVM stops at the
// first unhandled MSR even when the ioctl's errno is zero. Restore is not atomic.
type MSRCountError struct {
	Operation            string
	Completed, Requested int
	Index                uint32
}

func (e *MSRCountError) Error() string {
	return fmt.Sprintf("%s: transferred %d/%d MSRs; first unhandled index %#x", e.Operation, e.Completed, e.Requested, e.Index)
}

func checkMSRCount(operation string, entries []MSREntry, count uintptr) error {
	if count > uintptr(len(entries)) {
		return fmt.Errorf("%s: invalid MSR count %d > %d", operation, count, len(entries))
	}
	if int(count) != len(entries) {
		return &MSRCountError{operation, int(count), len(entries), entries[count].Index}
	}
	return nil
}

func transferMSRs(f snapshotFD, entries []MSREntry, write bool) ([]MSREntry, error) {
	return transferMSRsWith(f, entries, write, snapshotIOCTL)
}

func transferMSRsWith(f snapshotFD, entries []MSREntry, write bool, call snapshotCall) ([]MSREntry, error) {
	out := make([]MSREntry, 0, len(entries))
	request, name := uintptr(kGetMSRs), "KVM_GET_MSRS"
	if write {
		request, name = kSetMSRs, "KVM_SET_MSRS"
	}
	// KVM limits each ioctl to a small batch, independently of the list size.
	for start := 0; start < len(entries); start += 255 {
		end := min(start+255, len(entries))
		buf, err := snapshotBuffer(8, end-start, 16)
		if err != nil {
			return out, err
		}
		p := unsafe.Pointer(&buf[0])
		*(*uint32)(p) = uint32(end - start)
		batch := unsafe.Slice((*MSREntry)(unsafe.Add(p, 8)), end-start)
		copy(batch, entries[start:end])
		n, err := call(f, request, p)
		if err != nil {
			return out, fmt.Errorf("%s at entry %d: %w", name, start, err)
		}
		if err := checkMSRCount(name, batch, n); err != nil {
			if n <= uintptr(len(batch)) {
				out = append(out, batch[:n]...)
			}
			if partial, ok := err.(*MSRCountError); ok {
				partial.Completed += start
				partial.Requested = len(entries)
			}
			return out, err
		}
		out = append(out, batch...)
	}
	return out, nil
}

func getCPUID(f snapshotFD, request uintptr) ([]CPUIDEntry2, error) {
	return getCPUIDWith(f, request, snapshotIOCTL)
}

func getCPUIDWith(f snapshotFD, request uintptr, call snapshotCall) ([]CPUIDEntry2, error) {
	count := 256
	for attempts := 0; attempts < 12; attempts++ {
		buf, err := snapshotBuffer(8, count, 40)
		if err != nil {
			return nil, err
		}
		p := unsafe.Pointer(&buf[0])
		*(*uint32)(p) = uint32(count)
		_, err = call(f, request, p)
		n := int(*(*uint32)(p))
		if err == unix.E2BIG || err == unix.ENOMEM {
			count = max(count*2, n)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("KVM CPUID: %w", err)
		}
		if n > count {
			return nil, fmt.Errorf("KVM returned oversized CPUID: %d > %d", n, count)
		}
		return slices.Clone(unsafe.Slice((*CPUIDEntry2)(unsafe.Add(p, 8)), n)), nil
	}
	return nil, fmt.Errorf("KVM CPUID table keeps growing")
}

func setCPUID(f snapshotFD, entries []CPUIDEntry2) error {
	return setCPUIDWith(f, entries, snapshotIOCTL)
}

func setCPUIDWith(f snapshotFD, entries []CPUIDEntry2, call snapshotCall) error {
	buf, err := snapshotBuffer(8, len(entries), 40)
	if err != nil {
		return err
	}
	p := unsafe.Pointer(&buf[0])
	*(*uint32)(p) = uint32(len(entries))
	copy(unsafe.Slice((*CPUIDEntry2)(unsafe.Add(p, 8)), len(entries)), entries)
	if _, err := call(f, kSetCPUID2, p); err != nil {
		return fmt.Errorf("KVM_SET_CPUID2: %w", err)
	}
	return nil
}

func snapshotSystem() (*os.File, error) {
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("snapshot open /dev/kvm: %w", err)
	}
	return f, nil
}

// Use VM-scoped capability checks when available, never vCPU CHECK_EXTENSION.
func snapshotCap(sys *os.File, vm *VM, cap Cap) (int, error) {
	vmCheck, err := CheckExtension(sys, CapCheckExtensionVM)
	if err != nil {
		return 0, fmt.Errorf("KVM_CHECK_EXTENSION(VM): %w", err)
	}
	var f snapshotFD = sys
	if vmCheck != 0 && vm != nil {
		f = vm
	}
	n, err := CheckExtension(f, cap)
	if err != nil {
		return 0, fmt.Errorf("KVM_CHECK_EXTENSION(%s): %w", cap, err)
	}
	return n, nil
}

func requireSnapshotCaps(sys *os.File, vm *VM, caps ...Cap) error {
	for _, cap := range caps {
		n, err := snapshotCap(sys, vm, cap)
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("snapshot requires %s", cap)
		}
	}
	return nil
}

func snapshotXSaveSize(sys *os.File, vm *VM) (int, bool, error) {
	n, err := snapshotCap(sys, vm, CapXSave2)
	if err != nil {
		return 0, false, err
	}
	if n != 0 {
		// The system capability can describe a different size from this VM.
		if vm == nil {
			return 0, false, fmt.Errorf("XSAVE2 requires a vCPU returned by CreateVCPU (VM-scoped size)")
		}
		vmCheck, err := CheckExtension(sys, CapCheckExtensionVM)
		if err != nil {
			return 0, false, err
		}
		if vmCheck == 0 {
			return 0, false, fmt.Errorf("XSAVE2 requires VM-scoped capability checks")
		}
		if n < 4096 || n > maxSnapshotEntries || n%4 != 0 {
			return 0, false, fmt.Errorf("unsupported XSAVE2 size %d", n)
		}
		return n, true, nil
	}
	n, err = snapshotCap(sys, vm, CapXSave)
	if err != nil {
		return 0, false, err
	}
	if n != 0 {
		return 4096, false, nil
	}
	return 0, false, nil
}

// validateCPUCompatibility is deliberately conservative. The fingerprint also
// protects XSAVE component offsets, physical-address width and PMU geometry;
// these cannot be checked by treating all CPUID registers as feature bitmaps.
func validateCPUCompatibility(state *CPUState, host []CPUIDEntry2) error {
	if len(state.HostCPUID) == 0 || len(state.HostCPUID) != len(host) {
		return fmt.Errorf("incompatible destination CPU: supported CPUID table size differs from snapshot")
	}
	for i, source := range state.HostCPUID {
		dest := host[i]
		if stableHostCPUID(source) != stableHostCPUID(dest) {
			return fmt.Errorf("incompatible destination CPU: supported CPUID leaf %#x/%d differs: source=%#v destination=%#v", source.Function, source.Index, source, dest)
		}
	}
	if len(state.CPUID) == 0 {
		return fmt.Errorf("snapshot has no configured CPUID")
	}
	for _, guest := range state.CPUID {
		var supported CPUIDEntry2
		found := false
		for _, h := range host {
			if h.Function == guest.Function && h.Index == guest.Index {
				supported, found = h, true
				break
			}
		}
		// Hypervisor leaves are userspace-defined, not physical CPU features.
		if guest.Function >= 0x40000000 && guest.Function < 0x50000000 {
			continue
		}
		if !found {
			return fmt.Errorf("unsupported configured CPUID leaf %#x/%d", guest.Function, guest.Index)
		}
		var bad uint32
		switch guest.Function {
		case 1:
			if guest.ECX&(1<<5) != 0 {
				return fmt.Errorf("snapshot does not support nested VMX state; disable guest VMX before boot")
			}
			bad = (guest.ECX &^ uint32((1<<27)|(1<<31)) &^ supported.ECX) | (guest.EDX &^ supported.EDX)
		case 6:
			bad = (guest.EAX &^ supported.EAX) | (guest.ECX &^ supported.ECX)
		case 7:
			guestECX := guest.ECX
			// OSPKE, like OSXSAVE, reflects guest CR4 rather than a CPU
			// feature. PKU itself must still be supported by the destination.
			if guest.Index == 0 {
				guestECX &^= 1 << 4
			}
			bad = (guest.EBX &^ supported.EBX) | (guestECX &^ supported.ECX) | (guest.EDX &^ supported.EDX)
			if guest.Index != 0 {
				bad |= guest.EAX &^ supported.EAX
			}
		case 0xd:
			if guest.Index <= 1 {
				bad = (guest.EAX &^ supported.EAX) | (guest.EDX &^ supported.EDX)
				if guest.Index == 1 {
					bad |= guest.ECX &^ supported.ECX
				}
			} else if guest.EAX != supported.EAX || guest.EBX != supported.EBX || guest.ECX != supported.ECX || guest.EDX != supported.EDX {
				return fmt.Errorf("unsupported XSAVE component layout in CPUID %#x/%d", guest.Function, guest.Index)
			}
		case 0x80000001:
			if guest.ECX&(1<<2) != 0 {
				return fmt.Errorf("snapshot does not support nested SVM state; disable guest SVM before boot")
			}
			bad = (guest.ECX &^ supported.ECX) | (guest.EDX &^ supported.EDX)
		case 0x80000008:
			bad = guest.EBX &^ supported.EBX
		}
		if bad != 0 {
			return fmt.Errorf("unsupported configured CPU features in CPUID %#x/%d: %#x", guest.Function, guest.Index, bad)
		}
	}
	return nil
}

// KVM_GET_SUPPORTED_CPUID includes the physical thread's APIC/topology IDs.
// Calls from different goroutines/threads on one host must compare equal;
// guest topology itself is separately preserved by the configured CPUID table.
func stableHostCPUID(entry CPUIDEntry2) CPUIDEntry2 {
	switch entry.Function {
	case 1:
		entry.EBX &^= 0xff000000
	case 0xb, 0x1f:
		entry.EDX = 0
	case 0x8000001e:
		entry.EAX = 0
		entry.EBX &^= 0xff
		entry.ECX &^= 0xff
	case 4, 0x80000005, 0x80000006, 0x8000001d:
		// P/E cores can expose different cache geometry on the same host.
		// The guest keeps its saved virtual cache descriptions via SET_CPUID2.
		entry.EAX, entry.EBX, entry.ECX, entry.EDX = 0, 0, 0, 0
	}
	return entry
}

// ValidateCPUHost checks compatibility without modifying a VM. Destination
// device/capability checks still occur during actual restore.
func ValidateCPUHost(state *CPUState) error {
	if state == nil {
		return fmt.Errorf("snapshot CPU state is missing")
	}
	sys, err := snapshotSystem()
	if err != nil {
		return err
	}
	defer sys.Close()
	host, err := GetSupportedCPUID(sys)
	if err != nil {
		return err
	}
	return validateCPUCompatibility(state, host)
}

func validateXSave(data []byte, size int) error {
	if len(data) != size {
		return fmt.Errorf("incompatible XSAVE size: snapshot %d, destination %d; refusing truncation or padding", len(data), size)
	}
	if size == 0 {
		return nil
	}
	if size < 4096 {
		return fmt.Errorf("invalid XSAVE size %d", size)
	}
	// KVM uses standard host CPUID offsets, not compacted XSAVES layout.
	if binary.LittleEndian.Uint64(data[520:528]) != 0 {
		return fmt.Errorf("unsupported compacted/extended XSAVE format (XCOMP_BV)")
	}
	for _, b := range data[528:576] {
		if b != 0 {
			return fmt.Errorf("unsupported XSAVE header extension")
		}
	}
	return nil
}

func validateXSaveLayout(cpuid []CPUIDEntry2, size int) error {
	for _, entry := range cpuid {
		if entry.Function == 1 && entry.ECX&(1<<26) != 0 && size == 0 {
			return fmt.Errorf("guest advertises XSAVE but KVM cannot capture XSAVE state")
		}
		if entry.Function != 0xd || entry.Index < 2 || entry.EAX == 0 {
			continue
		}
		// XSS components are not part of KVM's standard XSAVE image. Refuse
		// them rather than guessing an XSAVES compacted layout.
		if entry.ECX&1 != 0 {
			return fmt.Errorf("unsupported supervisor XSAVE component %d", entry.Index)
		}
		if uint64(entry.EBX)+uint64(entry.EAX) > uint64(size) {
			return fmt.Errorf("XSAVE component %d ends at %d beyond KVM buffer %d; refusing truncated extended state", entry.Index, uint64(entry.EBX)+uint64(entry.EAX), size)
		}
	}
	return nil
}

// CaptureCPUState captures a quiesced vCPU. All vCPUs and interrupt/device
// producers must be stopped, with pending KVM_RUN I/O exits completed first.
// It never runs the vCPU. KVM errors and partial MSR reads are fatal.
func CaptureCPUState(vcpu *VCPU) (*CPUState, error) {
	if vcpu == nil || vcpu.File == nil {
		return nil, fmt.Errorf("nil vCPU")
	}
	sys, err := snapshotSystem()
	if err != nil {
		return nil, err
	}
	defer sys.Close()
	if err := requireSnapshotCaps(sys, vcpu.vm, CapIRQChip, CapMPState, CapExtCPUID, CapVCPUEvents); err != nil {
		return nil, err
	}
	s := new(CPUState)
	s.HostCPUID, err = GetSupportedCPUID(sys)
	if err != nil {
		return nil, err
	}
	s.CPUID, err = getCPUID(vcpu, kGetCPUID2)
	if err != nil {
		return nil, err
	}
	if err := validateCPUCompatibility(s, s.HostCPUID); err != nil {
		return nil, err
	}
	ops := []struct {
		request uintptr
		data    unsafe.Pointer
		name    string
	}{
		{kGetRegs, unsafe.Pointer(&s.Regs), "KVM_GET_REGS"},
		{kGetSregs, unsafe.Pointer(&s.Sregs), "KVM_GET_SREGS"},
		{kGetFPU, unsafe.Pointer(&s.FPU), "KVM_GET_FPU"},
		{kGetMPState, unsafe.Pointer(&s.MP), "KVM_GET_MP_STATE"},
		{kGetLAPIC, unsafe.Pointer(&s.LAPIC), "KVM_GET_LAPIC"},
		{kGetVCPUEvents, unsafe.Pointer(&s.Events), "KVM_GET_VCPU_EVENTS"},
	}
	for _, op := range ops {
		if err := snapshotOp(vcpu, op.request, op.data, op.name); err != nil {
			return nil, err
		}
	}
	size, xsave2, err := snapshotXSaveSize(sys, vcpu.vm)
	if err != nil {
		return nil, err
	}
	if err := validateXSaveLayout(s.CPUID, size); err != nil {
		return nil, err
	}
	if size != 0 {
		s.XSAVE = make([]byte, size)
		request := uintptr(kGetXSave)
		if xsave2 {
			request = kGetXSave2
		}
		if err := snapshotOp(vcpu, request, unsafe.Pointer(&s.XSAVE[0]), "KVM_GET_XSAVE(2)"); err != nil {
			return nil, err
		}
		if err := validateXSave(s.XSAVE, size); err != nil {
			return nil, err
		}
	}
	n, err := snapshotCap(sys, vcpu.vm, CapXCRS)
	if err != nil {
		return nil, err
	}
	if n != 0 {
		s.XCRs = new(XCRState)
		if err := snapshotOp(vcpu, kGetXCRs, unsafe.Pointer(s.XCRs), "KVM_GET_XCRS"); err != nil {
			return nil, err
		}
		if s.XCRs.Count > 16 || s.XCRs.Flags != 0 {
			return nil, fmt.Errorf("unsupported XCR state format")
		}
	}
	n, err = snapshotCap(sys, vcpu.vm, CapDebugRegs)
	if err != nil {
		return nil, err
	}
	if n != 0 {
		s.Debug = new(DebugRegs)
		if err := snapshotOp(vcpu, kGetDebugRegs, unsafe.Pointer(s.Debug), "KVM_GET_DEBUGREGS"); err != nil {
			return nil, err
		}
	}
	indices, err := GetMSRIndexList(sys)
	if err != nil {
		return nil, err
	}
	indices = guestMSRIndices(indices, s.CPUID)
	s.MSRs, err = GetMSRs(vcpu, indices)
	if err != nil {
		return nil, err
	}
	n, err = snapshotCap(sys, vcpu.vm, CapGetTSCKHz)
	if err != nil {
		return nil, err
	}
	if n != 0 {
		khz, err := snapshotIOCTL(vcpu, kGetTSCKHz, nil)
		if err != nil {
			return nil, fmt.Errorf("KVM_GET_TSC_KHZ: %w", err)
		}
		if khz == 0 || khz > uintptr(^uint32(0)) {
			return nil, fmt.Errorf("invalid TSC frequency %d", khz)
		}
		s.TSCKHz = uint32(khz)
	}
	return s, nil
}

// RestoreCPUState restores a stopped vCPU with the same ID and VM setup. Guest
// memory must already be installed. Restore VMState before restoring CPUs.
// Validation precedes writes, but ioctl failure can leave partially restored
// state: do not resume after an error. Source state is not modified.
func RestoreCPUState(vcpu *VCPU, state *CPUState) error {
	if vcpu == nil || vcpu.File == nil || state == nil {
		return fmt.Errorf("nil vCPU or CPU state")
	}
	sys, err := snapshotSystem()
	if err != nil {
		return err
	}
	defer sys.Close()
	if err := requireSnapshotCaps(sys, vcpu.vm, CapIRQChip, CapMPState, CapExtCPUID, CapVCPUEvents); err != nil {
		return err
	}
	host, err := GetSupportedCPUID(sys)
	if err != nil {
		return err
	}
	if err := validateCPUCompatibility(state, host); err != nil {
		return err
	}
	size, _, err := snapshotXSaveSize(sys, vcpu.vm)
	if err != nil {
		return err
	}
	if err := validateXSave(state.XSAVE, size); err != nil {
		return err
	}
	if err := validateXSaveLayout(state.CPUID, size); err != nil {
		return err
	}
	for _, optional := range []struct {
		cap     Cap
		present bool
	}{
		{CapXCRS, state.XCRs != nil}, {CapDebugRegs, state.Debug != nil}, {CapGetTSCKHz, state.TSCKHz != 0},
	} {
		n, err := snapshotCap(sys, vcpu.vm, optional.cap)
		if err != nil {
			return err
		}
		if (n != 0) != optional.present {
			return fmt.Errorf("snapshot/destination capability mismatch: %s", optional.cap)
		}
	}
	if state.XCRs != nil && (state.XCRs.Count > 16 || state.XCRs.Flags != 0) {
		return fmt.Errorf("unsupported XCR state format")
	}
	var events VCPUEvents
	if err := snapshotOp(vcpu, kGetVCPUEvents, unsafe.Pointer(&events), "preflight KVM_GET_VCPU_EVENTS"); err != nil {
		return err
	}
	if state.Events.Flags&^events.Flags != 0 {
		return fmt.Errorf("destination lacks VCPU event extensions %#x", state.Events.Flags&^events.Flags)
	}
	// Preflight every MSR index; never discard unsupported snapshot entries.
	indices, err := GetMSRIndexList(sys)
	if err != nil {
		return err
	}
	indices = guestMSRIndices(indices, state.CPUID)
	if len(state.MSRs) != len(indices) {
		return fmt.Errorf("incomplete/incompatible MSR state: snapshot %d entries, destination requires %d", len(state.MSRs), len(indices))
	}
	available := make(map[uint32]bool, len(indices))
	for _, index := range indices {
		available[uint32(index)] = true
	}
	for _, entry := range state.MSRs {
		if !available[entry.Index] {
			return fmt.Errorf("unsupported or duplicate snapshot MSR %#x", entry.Index)
		}
		delete(available, entry.Index)
	}
	if state.TSCKHz != 0 {
		khz, err := snapshotIOCTL(vcpu, kGetTSCKHz, nil)
		if err != nil {
			return fmt.Errorf("KVM_GET_TSC_KHZ: %w", err)
		}
		if khz != uintptr(state.TSCKHz) {
			n, err := snapshotCap(sys, vcpu.vm, CapTSCControl)
			if err != nil {
				return err
			}
			if n == 0 {
				return fmt.Errorf("TSC frequency mismatch: snapshot %d kHz, destination %d kHz; KVM TSC scaling unavailable", state.TSCKHz, khz)
			}
			_, _, errno := unix.Syscall(unix.SYS_IOCTL, vcpu.Fd(), kSetTSCKHz, uintptr(state.TSCKHz))
			if errno != 0 {
				return fmt.Errorf("KVM_SET_TSC_KHZ(%d): %w", state.TSCKHz, errno)
			}
			actual, err := snapshotIOCTL(vcpu, kGetTSCKHz, nil)
			if err != nil {
				return fmt.Errorf("verify KVM_GET_TSC_KHZ: %w", err)
			}
			if actual != uintptr(state.TSCKHz) {
				return fmt.Errorf("KVM TSC scaling did not preserve frequency: got %d, want %d kHz", actual, state.TSCKHz)
			}
		}
	}
	if err := SetCPUID2(vcpu, state.CPUID); err != nil {
		return err
	}
	// Work on copies: GET/SET ioctl arguments must not alias caller-owned state.
	s := *state
	if err := snapshotOp(vcpu, kSetSregs, unsafe.Pointer(&s.Sregs), "KVM_SET_SREGS"); err != nil {
		return err
	}
	if err := snapshotOp(vcpu, kSetFPU, unsafe.Pointer(&s.FPU), "KVM_SET_FPU"); err != nil {
		return err
	}
	if s.XCRs != nil {
		x := *s.XCRs
		if err := snapshotOp(vcpu, kSetXCRs, unsafe.Pointer(&x), "KVM_SET_XCRS"); err != nil {
			return err
		}
	}
	if size != 0 {
		x := slices.Clone(s.XSAVE)
		if err := snapshotOp(vcpu, kSetXSave, unsafe.Pointer(&x[0]), "KVM_SET_XSAVE"); err != nil {
			return err
		}
	}
	if s.Debug != nil {
		d := *s.Debug
		if err := snapshotOp(vcpu, kSetDebugRegs, unsafe.Pointer(&d), "KVM_SET_DEBUGREGS"); err != nil {
			return err
		}
	}
	if err := snapshotOp(vcpu, kSetLAPIC, unsafe.Pointer(&s.LAPIC), "KVM_SET_LAPIC"); err != nil {
		return err
	}
	// LAPIC must precede MSRs, especially IA32_TSC_DEADLINE. Frequency must be
	// restored before IA32_TSC; preserve KVM's MSR list ordering otherwise.
	if err := SetMSRs(vcpu, s.MSRs); err != nil {
		return err
	}
	if err := snapshotOp(vcpu, kSetRegs, unsafe.Pointer(&s.Regs), "KVM_SET_REGS"); err != nil {
		return err
	}
	if err := snapshotOp(vcpu, kSetVCPUEvents, unsafe.Pointer(&s.Events), "KVM_SET_VCPU_EVENTS"); err != nil {
		return err
	}
	return snapshotOp(vcpu, kSetMPState, unsafe.Pointer(&s.MP), "KVM_SET_MP_STATE")
}

// The system list includes nested virtualization control MSRs even when the
// configured guest CPU disables VMX/SVM. KVM correctly refuses access then;
// these are not part of that guest's execution state. Never ignore a partial
// transfer of the remaining (required) MSRs.
func guestMSRIndices(indices []int, cpuid []CPUIDEntry2) []int {
	var vmx, svm bool
	for _, leaf := range cpuid {
		if leaf.Function == 1 {
			vmx = leaf.ECX&(1<<5) != 0
		}
		if leaf.Function == 0x80000001 {
			svm = leaf.ECX&(1<<2) != 0
		}
	}
	filtered := make([]int, 0, len(indices))
	for _, index := range indices {
		if !vmx && index >= 0x480 && index <= 0x49f {
			continue
		}
		if !svm && (index == 0xc0010114 || index == 0xc0010117) {
			continue
		}
		filtered = append(filtered, index)
	}
	return filtered
}

// CaptureVMState requires stopped vCPUs and quiesced interrupt producers.
// An absent in-kernel IRQ chip/PIT is an error, not an empty optional state.
func CaptureVMState(vm *VM) (*VMState, error) {
	if vm == nil || vm.File == nil {
		return nil, fmt.Errorf("nil VM")
	}
	sys, err := snapshotSystem()
	if err != nil {
		return nil, err
	}
	defer sys.Close()
	if err := requireSnapshotCaps(sys, vm, CapIRQChip, CapPITState2); err != nil {
		return nil, err
	}
	s := new(VMState)
	for i := range s.IRQChips {
		s.IRQChips[i].ID = uint32(i)
		if err := snapshotOp(vm, kGetIRQChip, unsafe.Pointer(&s.IRQChips[i]), "KVM_GET_IRQCHIP"); err != nil {
			return nil, err
		}
	}
	if err := snapshotOp(vm, kGetPIT2, unsafe.Pointer(&s.PIT), "KVM_GET_PIT2"); err != nil {
		return nil, err
	}
	n, err := snapshotCap(sys, vm, CapAdjustClock)
	if err != nil {
		return nil, err
	}
	if n != 0 {
		s.Clock = new(ClockData)
		if err := GetClock(vm, s.Clock); err != nil {
			return nil, fmt.Errorf("KVM_GET_CLOCK: %w", err)
		}
	}
	return s, nil
}

// RestoreVMState restores existing in-kernel IRQ chips/PIT, then the clock.
// The caller must recreate the same VM configuration, IRQ routing and irqfds;
// these APIs do not capture userspace devices, memory or external backends.
// Clock restoration freezes guest time across downtime (no wall-time catchup).
func RestoreVMState(vm *VM, state *VMState) error {
	if vm == nil || vm.File == nil || state == nil {
		return fmt.Errorf("nil VM or VM state")
	}
	for i, chip := range state.IRQChips {
		if chip.ID != uint32(i) {
			return fmt.Errorf("invalid IRQ chip ID %d at position %d", chip.ID, i)
		}
	}
	sys, err := snapshotSystem()
	if err != nil {
		return err
	}
	defer sys.Close()
	if err := requireSnapshotCaps(sys, vm, CapIRQChip, CapPITState2); err != nil {
		return err
	}
	n, err := snapshotCap(sys, vm, CapAdjustClock)
	if err != nil {
		return err
	}
	if (n != 0) != (state.Clock != nil) {
		return fmt.Errorf("snapshot/destination capability mismatch: KVM_CAP_ADJUST_CLOCK")
	}
	// Probe required devices before making changes.
	if _, err := CaptureVMState(vm); err != nil {
		return fmt.Errorf("destination VM setup: %w", err)
	}
	s := *state
	for i := range s.IRQChips {
		if err := snapshotOp(vm, kSetIRQChip, unsafe.Pointer(&s.IRQChips[i]), "KVM_SET_IRQCHIP"); err != nil {
			return err
		}
	}
	if err := snapshotOp(vm, kSetPIT2, unsafe.Pointer(&s.PIT), "KVM_SET_PIT2"); err != nil {
		return err
	}
	if s.Clock != nil {
		// GET-only TSC_STABLE and host wall-clock metadata must not be replayed.
		clock := ClockData{Clock: s.Clock.Clock}
		if err := SetClock(vm, &clock); err != nil {
			return fmt.Errorf("KVM_SET_CLOCK: %w", err)
		}
	}
	return nil
}
