//go:build linux && amd64

package kvm

import (
	"encoding/binary"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

func TestGuestMSRIndicesExcludeOnlyDisabledNestedState(t *testing.T) {
	indices := []int{0x10, 0x174, 0x480, 0x48f, 0xc0010114, 0xc0010117, 0xc0000080}
	filtered := guestMSRIndices(indices, nil)
	if len(filtered) != 3 || filtered[0] != 0x10 || filtered[1] != 0x174 || filtered[2] != 0xc0000080 {
		t.Fatalf("filtered required MSRs or kept disabled nested state: %#v", filtered)
	}
	withNested := guestMSRIndices(indices, []CPUIDEntry2{{Function: 1, ECX: 1 << 5}, {Function: 0x80000001, ECX: 1 << 2}})
	if len(withNested) != len(indices) {
		t.Fatal("advertised nested state was silently dropped")
	}
}

func TestSnapshotFingerprintIgnoresPhysicalTopology(t *testing.T) {
	for _, entry := range []CPUIDEntry2{
		{Function: 1, EBX: 0xff000000},
		{Function: 4, EAX: 123, EBX: 456, ECX: 789},
		{Function: 0xb, EDX: 12},
	} {
		zero := CPUIDEntry2{Function: entry.Function}
		if stableHostCPUID(entry) != stableHostCPUID(zero) {
			t.Fatalf("unstable topology/cache fingerprint: %#v", entry)
		}
	}
	if stableHostCPUID(CPUIDEntry2{Function: 1, ECX: 1}) == stableHostCPUID(CPUIDEntry2{Function: 1}) {
		t.Fatal("CPU execution-feature mismatch was ignored")
	}
}

func TestSnapshotABI(t *testing.T) {
	for _, tt := range []struct {
		name string
		got, want uintptr
	}{
		{"Regs", unsafe.Sizeof(Regs{}), 144},
		{"Sregs", unsafe.Sizeof(Sregs{}), 312},
		{"FPU", unsafe.Sizeof(FPU{}), 416},
		{"MPState", unsafe.Sizeof(MPState{}), 4},
		{"MSREntry", unsafe.Sizeof(MSREntry{}), 16},
		{"CPUIDEntry2", unsafe.Sizeof(CPUIDEntry2{}), 40},
		{"ClockData", unsafe.Sizeof(ClockData{}), 48},
		{"LAPICState", unsafe.Sizeof(LAPICState{}), 1024},
		{"VCPUEvents", unsafe.Sizeof(VCPUEvents{}), 64},
		{"VCPUEvents.Flags", unsafe.Offsetof(VCPUEvents{}.Flags), 20},
		{"VCPUEvents.TripleFault", unsafe.Offsetof(VCPUEvents{}.TripleFaultPending), 28},
		{"VCPUEvents.Payload", unsafe.Offsetof(VCPUEvents{}.ExceptionPayload), 56},
		{"DebugRegs", unsafe.Sizeof(DebugRegs{}), 128},
		{"XCR", unsafe.Sizeof(XCR{}), 16},
		{"XCRState", unsafe.Sizeof(XCRState{}), 392},
		{"XCRState.XCRs", unsafe.Offsetof(XCRState{}.XCRs), 8},
		{"IRQChipState", unsafe.Sizeof(IRQChipState{}), 520},
		{"IRQChipState alignment", unsafe.Alignof(IRQChipState{}), 8},
		{"IRQChipState.Chip", unsafe.Offsetof(IRQChipState{}.Chip), 8},
		{"PITChannelState", unsafe.Sizeof(PITChannelState{}), 24},
		{"PITChannelState.Time", unsafe.Offsetof(PITChannelState{}.CountLoadTime), 16},
		{"PITState", unsafe.Sizeof(PITState{}), 112},
	} {
		if tt.got != tt.want {
			t.Errorf("%s ABI: got %d, want %d", tt.name, tt.got, tt.want)
		}
	}
	// Independently reconstruct _IOC from installed Linux headers. The GET
	// and SET request numbers overlap across fd types; direction matters.
	for _, tt := range []struct {
		request, direction, number, size uintptr
	}{
		{kGetIRQChip, 3, 0x62, 520}, {kSetIRQChip, 2, 0x63, 520},
		{kGetLAPIC, 2, 0x8e, 1024}, {kSetLAPIC, 1, 0x8f, 1024},
		{kGetCPUID2, 3, 0x91, 8},
		{kGetVCPUEvents, 2, 0x9f, 64}, {kSetVCPUEvents, 1, 0xa0, 64},
		{kGetDebugRegs, 2, 0xa1, 128}, {kSetDebugRegs, 1, 0xa2, 128},
		{kGetXSave, 2, 0xa4, 4096}, {kSetXSave, 1, 0xa5, 4096},
		{kGetXSave2, 2, 0xcf, 4096},
		{kGetXCRs, 2, 0xa6, 392}, {kSetXCRs, 1, 0xa7, 392},
		{kGetPIT2, 2, 0x9f, 112}, {kSetPIT2, 1, 0xa0, 112},
		{kGetTSCKHz, 0, 0xa3, 0}, {kSetTSCKHz, 0, 0xa2, 0},
	} {
		want := tt.direction<<30 | tt.size<<16 | 0xae<<8 | tt.number
		if tt.request != want {
			t.Errorf("ioctl %#x != ABI %#x", tt.request, want)
		}
	}
}

func TestSnapshotDynamicMSRList(t *testing.T) {
	for _, request := range []uintptr{kGetMSRIndexList, kGetMSRFeatureIndexList} {
		calls := 0
		indices, err := getMSRIndicesWith(nil, request, func(_ snapshotFD, got uintptr, p unsafe.Pointer) (uintptr, error) {
			calls++
			if got != request { t.Fatalf("request %#x", got) }
			capacity := *(*uint32)(p)
			*(*uint32)(p) = 700
			if capacity < 700 { return 0, unix.E2BIG }
			for i := range unsafe.Slice((*uint32)(unsafe.Add(p, 4)), 700) {
				unsafe.Slice((*uint32)(unsafe.Add(p, 4)), 700)[i] = uint32(i + 100)
			}
			return 0, nil
		})
		if err != nil || calls != 2 || len(indices) != 700 || indices[699] != 799 {
			t.Fatalf("list len=%d calls=%d err=%v", len(indices), calls, err)
		}
	}
	_, err := getMSRIndicesWith(nil, kGetMSRIndexList, func(_ snapshotFD, _ uintptr, p unsafe.Pointer) (uintptr, error) {
		*(*uint32)(p) = maxSnapshotEntries + 1
		return 0, unix.E2BIG
	})
	if err == nil { t.Fatal("accepted unbounded MSR list allocation") }
}

func TestSnapshotMSRBatchesAndPartialCounts(t *testing.T) {
	entries := make([]MSREntry, 700)
	for i := range entries { entries[i] = MSREntry{Index: uint32(i + 1000), Data: uint64(i)} }
	for _, write := range []bool{false, true} {
		calls := 0
		out, err := transferMSRsWith(nil, entries, write, func(_ snapshotFD, request uintptr, p unsafe.Pointer) (uintptr, error) {
			want := uintptr(kGetMSRs)
			if write { want = kSetMSRs }
			if request != want { t.Fatalf("request %#x != %#x", request, want) }
			n := int(*(*uint32)(p))
			if n > 255 { t.Fatalf("oversized MSR batch %d", n) }
			batch := unsafe.Slice((*MSREntry)(unsafe.Add(p, 8)), n)
			if batch[0].Index != entries[calls*255].Index { t.Fatal("wrong flexible-array offset/order") }
			calls++
			return uintptr(n), nil
		})
		if err != nil || calls != 3 || !slices.Equal(out, entries) { t.Fatalf("batch transfer: calls=%d err=%v", calls, err) }
		for _, partialCount := range []uintptr{0, 3, 254} {
			calls = 0
			out, err := transferMSRsWith(nil, entries, write, func(_ snapshotFD, _ uintptr, p unsafe.Pointer) (uintptr, error) {
				calls++
				if calls == 2 { return partialCount, nil }
				return uintptr(*(*uint32)(p)), nil
			})
			var partial *MSRCountError
			if !errors.As(err, &partial) || partial.Completed != 255+int(partialCount) || partial.Requested != 700 || partial.Index != entries[255+partialCount].Index || len(out) != partial.Completed {
				t.Fatalf("partial transfer out=%d err=%v", len(out), err)
			}
			if calls != 2 { t.Fatalf("continued past failing MSR: %d calls", calls) }
		}
	}
	if err := checkMSRCount("test", entries[:1], 2); err == nil { t.Fatal("accepted impossible syscall count") }
	_, err := transferMSRsWith(nil, entries, false, func(snapshotFD, uintptr, unsafe.Pointer) (uintptr, error) { return 0, unix.EIO })
	if !errors.Is(err, unix.EIO) { t.Fatalf("lost ioctl error: %v", err) }
	if _, err := GetMSRs(nil, []int{-1}); !errors.Is(err, unix.EINVAL) { t.Fatalf("negative index: %v", err) }
	if _, err := GetMSRs(nil, []int{1 << 32}); !errors.Is(err, unix.EINVAL) { t.Fatalf("overflow index: %v", err) }
}

func TestSnapshotCPUIDCompatibility(t *testing.T) {
	host := []CPUIDEntry2{
		{Function: 1, EAX: 0x123, EBX: 0x10000, ECX: 1 << 26, EDX: 1 << 4},
		{Function: 7, EBX: 1 << 5},
		{Function: 0xd, Index: 2, EAX: 256, EBX: 576},
		{Function: 0x80000001},
	}
	s := CPUState{HostCPUID: slices.Clone(host), CPUID: slices.Clone(host)}
	// Topology and dynamic OSXSAVE/hypervisor bits need not match the host.
	s.CPUID[0].EBX = 0x7000000
	s.CPUID[0].ECX |= (1 << 27) | (1 << 31)
	s.CPUID[1].ECX |= 1 << 4 // Guest-controlled OSPKE.
	if err := validateCPUCompatibility(&s, host); err != nil { t.Fatal(err) }
	before := slices.Clone(s.CPUID)
	if err := validateCPUCompatibility(&s, host); err != nil || !slices.Equal(before, s.CPUID) { t.Fatal("validation changed configured CPUID") }
	for _, mutate := range []func(*CPUState){
		func(s *CPUState) { s.CPUID[0].ECX |= 1 << 28 },
		func(s *CPUState) { s.CPUID[1].EBX |= 1 << 16 },
		func(s *CPUState) { s.CPUID[2].EBX++ },
		func(s *CPUState) { s.CPUID[0].ECX |= 1 << 5 },
		func(s *CPUState) { s.CPUID[3].ECX |= 1 << 2 },
		func(s *CPUState) { s.HostCPUID[0].EAX++ },
		func(s *CPUState) { s.HostCPUID = nil },
		func(s *CPUState) { s.CPUID = nil },
	} {
		bad := CPUState{HostCPUID: slices.Clone(host), CPUID: slices.Clone(host)}
		mutate(&bad)
		if err := validateCPUCompatibility(&bad, host); err == nil { t.Fatal("accepted incompatible CPUID") }
	}
}

func TestSnapshotDynamicCPUID(t *testing.T) {
	configured := make([]CPUIDEntry2, 600)
	for i := range configured {
		configured[i] = CPUIDEntry2{Function: uint32(i), Index: uint32(i % 3), EAX: uint32(i + 42)}
	}
	for _, request := range []uintptr{kGetCPUID2, kGetSupportedCPUID} {
		calls := 0
		got, err := getCPUIDWith(nil, request, func(_ snapshotFD, req uintptr, p unsafe.Pointer) (uintptr, error) {
			calls++
			if req != request {
				t.Fatalf("wrong CPUID request %#x", req)
			}
			capacity := int(*(*uint32)(p))
			*(*uint32)(p) = uint32(len(configured))
			if capacity < len(configured) {
				return 0, unix.E2BIG
			}
			copy(unsafe.Slice((*CPUIDEntry2)(unsafe.Add(p, 8)), capacity), configured)
			return 0, nil
		})
		if err != nil || calls != 2 || !slices.Equal(got, configured) {
			t.Fatalf("CPUID round trip: calls=%d err=%v", calls, err)
		}
	}
	err := setCPUIDWith(nil, configured, func(_ snapshotFD, request uintptr, p unsafe.Pointer) (uintptr, error) {
		if request != kSetCPUID2 || int(*(*uint32)(p)) != len(configured) {
			t.Fatal("wrong SET_CPUID2 header")
		}
		entries := unsafe.Slice((*CPUIDEntry2)(unsafe.Add(p, 8)), len(configured))
		if !slices.Equal(entries, configured) {
			t.Fatal("configured CPUID entries changed")
		}
		return 0, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotXSaveValidation(t *testing.T) {
	for _, size := range []int{4096, 11008} {
		data := make([]byte, size)
		if err := validateXSave(data, size); err != nil { t.Fatal(err) }
		if err := validateXSave(data, size+64); err == nil { t.Fatal("accepted XSAVE size mismatch") }
		binary.LittleEndian.PutUint64(data[520:], 1<<63)
		if err := validateXSave(data, size); err == nil { t.Fatal("accepted compacted XSAVE") }
		binary.LittleEndian.PutUint64(data[520:], 0)
		data[528] = 1
		if err := validateXSave(data, size); err == nil { t.Fatal("accepted XSAVE extension") }
	}
	if err := validateXSave(make([]byte, 100), 100); err == nil { t.Fatal("accepted short XSAVE") }
	if err := validateXSave(nil, 0); err != nil { t.Fatal(err) }
	for _, tt := range []struct {
		cpuid []CPUIDEntry2
		size int
		valid bool
	}{
		{[]CPUIDEntry2{{Function: 0xd, Index: 2, EAX: 256, EBX: 576}}, 4096, true},
		{[]CPUIDEntry2{{Function: 0xd, Index: 18, EAX: 8192, EBX: 2816}}, 11008, true},
		{[]CPUIDEntry2{{Function: 0xd, Index: 18, EAX: 8192, EBX: 2816}}, 4096, false},
		{[]CPUIDEntry2{{Function: 0xd, Index: 12, EAX: 24, ECX: 1}}, 4096, false},
		{[]CPUIDEntry2{{Function: 1, ECX: 1 << 26}}, 0, false},
	} {
		if err := validateXSaveLayout(tt.cpuid, tt.size); (err == nil) != tt.valid { t.Fatalf("layout valid=%v err=%v", tt.valid, err) }
	}
}

func TestSnapshotNilInputs(t *testing.T) {
	if _, err := CaptureCPUState(nil); err == nil { t.Fatal("accepted nil vCPU") }
	if err := RestoreCPUState(nil, nil); err == nil { t.Fatal("accepted nil CPU state") }
	if _, err := CaptureVMState(nil); err == nil { t.Fatal("accepted nil VM") }
	if err := RestoreVMState(nil, nil); err == nil { t.Fatal("accepted nil VM state") }
	if _, err := snapshotBuffer(8, maxSnapshotEntries+1, 40); err == nil { t.Fatal("accepted oversized flexible array") }
}

func TestSnapshotKVMRoundTrip(t *testing.T) {
	sys, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil { t.Skipf("KVM unavailable: %v", err) }
	defer sys.Close()
	newVM := func() *VM {
		vm, err := CreateVM(sys)
		if err != nil { t.Fatal(err) }
		t.Cleanup(func() { vm.Close() })
		if err := CreateIRQChip(vm); err != nil { t.Fatal(err) }
		if err := CreatePIT2(vm, &PITConfig{Flags: PITSpeakerDummy}); err != nil { t.Fatal(err) }
		return vm
	}
	vm := newVM()
	vcpu, err := CreateVCPU(vm, 0)
	if err != nil { t.Fatal(err) }
	defer vcpu.Close()
	cpuid, err := GetSupportedCPUID(sys)
	if err != nil { t.Fatal(err) }
	for i := range cpuid {
		if cpuid[i].Function == 1 { cpuid[i].ECX &^= 1 << 5 }
		if cpuid[i].Function == 0x80000001 { cpuid[i].ECX &^= 1 << 2 }
	}
	if err := SetCPUID2(vcpu, cpuid); err != nil { t.Fatal(err) }
	regs := Regs{RAX: 0xcafef00d, RIP: 0xfff0, RFlags: 2}
	if err := SetRegs(vcpu, &regs); err != nil { t.Fatal(err) }
	vmState, err := CaptureVMState(vm)
	if err != nil { t.Fatal(err) }
	cpuState, err := CaptureCPUState(vcpu)
	if err != nil {
		// Extended supervisor XSAVE layouts are explicitly outside this API.
		if strings.Contains(err.Error(), "unsupported supervisor XSAVE") { t.Skip(err) }
		t.Fatal(err)
	}
	dstVM := newVM()
	dst, err := CreateVCPU(dstVM, 0)
	if err != nil { t.Fatal(err) }
	defer dst.Close()
	if err := RestoreVMState(dstVM, vmState); err != nil { t.Fatal(err) }
	if err := RestoreCPUState(dst, cpuState); err != nil { t.Fatal(err) }
	got, err := CaptureCPUState(dst)
	if err != nil { t.Fatal(err) }
	if got.Regs != cpuState.Regs || got.Sregs != cpuState.Sregs || got.MP != cpuState.MP { t.Fatal("register round trip mismatch") }
	if !slices.Equal(got.CPUID, cpuState.CPUID) { t.Fatal("configured CPUID changed during restore") }
	if !slices.Equal(got.XSAVE, cpuState.XSAVE) { t.Fatal("XSAVE changed during restore") }
}