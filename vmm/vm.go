//go:build linux

// Package vmm provides helpers for configuring and running a KVM virtual machine.
package vmm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"kube-vm/kvm"
	"kube-vm/virtio"
	"kube-vm/virtio/mmio"
	"kube-vm/vmm/arch"

	"golang.org/x/sys/unix"
)

// Config describes a new VM.
type Config struct {

	// MemSize is the size of the VM's memory in bytes.
	// It must be a multiple of the host's page size.
	// If MemSize is 0, the VM will have 1G of memory.
	MemSize int

	// NumCPU is the number of VCPUs attached to the VM.
	// If NumCPU is 0, the VM will have one VCPU.
	NumCPU int

	// Devices configures the VM's virtio-mmio devices.
	Devices []virtio.DeviceConfig

	// Loader configures the VM's memory and registers.
	Loader Loader

	// Arch, if set, is called to do arch-specific setup during VM creation.
	// If Arch is nil, a default implementation is used. Setting Arch is
	// probably only useful for testing, debugging, and development.
	Arch Arch
}

// VMInfo describes a configured VM in a form useful to the Loader.
// It is passed to the Loader's LoadMemory and LoadVCPU methods.
type VMInfo struct {

	// MemSize is the size of the VM's memory in bytes.
	// It is a multiple of the host's page size.
	MemSize int

	// NumCPU is the number of VCPUs attached to the VM.
	NumCPU int

	// Devices enumerates the VM's virtio-mmio devices.
	Devices []mmio.DeviceInfo
}

type Loader interface {

	// LoadMemory prepares the VM's memory before it boots.
	LoadMemory(info VMInfo, mem []byte) error

	// LoadVCPU prepares a VCPU before the VM boots.
	LoadVCPU(info VMInfo, slot int, regs *kvm.Regs, sregs *kvm.Sregs) error
}

type Arch interface {

	// SetupVM is called after the VM is created.
	// It sets up arch-specific "hardware" like the PIC.
	SetupVM(vm *kvm.VM) error

	// SetupMemory is called after the VM's memory is allocated.
	// It partitions the memory into regions. It can also write
	// arch-specific data to the memory if necessary.
	SetupMemory(mem []byte) ([]kvm.UserspaceMemoryRegion, error)

	// SetupVCPU is called after the VCPU is created and mmaped.
	// It sets up arch-specific features like MSRs and cpuid.
	SetupVCPU(slot int, vcpu *kvm.VCPU, state *kvm.VCPUState) error
}

type VM struct {
	fd   *kvm.VM
	mem  []byte
	cpu  []*vcpu
	mmio *mmio.Bus
	irqf map[int]int // irq:fd

	mu    sync.Mutex
	doneC chan struct{}
}

const (
	MemSizeMin     = 1 << 20 // 1M
	MemSizeDefault = 1 << 30 // 1G
	MemSizeMax     = 1 << 40 // 1T
	NumCPUMin      = 1
	NumCPUDefault  = 1
)

var (
	ErrOpenKVM             = errors.New("vmm: KVM is not available")
	ErrCompat              = errors.New("vmm: incompatible KVM")
	ErrConfig              = errors.New("vmm: invalid config")
	ErrGetVCPUMmapSize     = errors.New("vmm: get VCPU mmap size failed")
	ErrCreate              = errors.New("vmm: create failed")
	ErrSetup               = errors.New("vmm: setup failed")
	ErrAllocMemory         = errors.New("vmm: memory allocation failed")
	ErrSetupMemory         = errors.New("vmm: memory setup failed")
	ErrLoadMemory          = errors.New("vmm: memory load failed")
	ErrSetUserMemoryRegion = errors.New("vmm: set user memory region failed")
	ErrCreateVCPU          = errors.New("vmm: VCPU create failed")
	ErrMmapVCPU            = errors.New("vmm: VCPU mmap failed")
	ErrSetupVCPU           = errors.New("vmm: VCPU setup failed")
	ErrLoadVCPU            = errors.New("vmm: VCPU load failed")
	ErrVMClosed            = errors.New("vmm: VM closed")
)

// vcpu collects a VCPU fd and its mmaped state.
type vcpu struct {
	fd       *kvm.VCPU
	mm       []byte
	opC      chan vcpuOp
	shutdown chan struct{}
	doneC    chan struct{}
	tid      int32
}

// vcpuOp is an operation to be performed on a vcpu thread.
type vcpuOp struct {
	F func() error
	C chan error
}

func vmLogf(m *VM, phase, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "kube-vm: vmm vm=%p: %s: %s\n", m, phase, msg)
}

func init() {
	// SIGURG is used to interrupt KVM_RUN on specific VCPU threads during teardown.
	// It must be actively handled (not ignored) for tgkill(SIGURG) to unblock ioctls.
	sigC := make(chan os.Signal, 16)
	signal.Notify(sigC, unix.SIGURG)
	go func() {
		for range sigC {
		}
	}()
}

// New creates a new VM.
func New(cfg Config) (*VM, error) {
	sys, err := os.Open("/dev/kvm")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrOpenKVM, err)
	}

	defer sys.Close()

	if err := arch.ValidateKVM(sys); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCompat, err)
	}

	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrConfig, err)
	}

	// default arch
	if cfg.Arch == nil {
		a, err := arch.New(sys)
		if err != nil {
			panic(err)
		}

		cfg.Arch = a
	}

	if ncfg, ok := cfg.Arch.(interface{ SetNumCPU(int) }); ok {
		ncfg.SetNumCPU(cfg.NumCPU)
	}

	vm, err := kvm.CreateVM(sys)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCreate, err)
	}

	// install arch-specific "hardware"
	if err := cfg.Arch.SetupVM(vm); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSetup, err)
	}

	// create memory
	mem, err := unix.Mmap(-1, 0, cfg.MemSize,
		unix.PROT_READ|unix.PROT_WRITE,
		unix.MAP_PRIVATE|unix.MAP_ANONYMOUS|unix.MAP_NORESERVE)

	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAllocMemory, err)
	}

	// partition memory
	mrs, err := cfg.Arch.SetupMemory(mem)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSetupMemory, err)
	}

	// install memory
	for _, mr := range mrs {
		if err := kvm.SetUserMemoryRegion(vm, &mr); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrSetUserMemoryRegion, err)
		}
	}

	mmsz, err := kvm.GetVCPUMmapSize(sys)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrGetVCPUMmapSize, err)
	}

	// create VCPUs
	cpu := make([]*vcpu, cfg.NumCPU)
	for slot := range cpu {
		c := &vcpu{
			opC:      make(chan vcpuOp),
			shutdown: make(chan struct{}),
			doneC:    make(chan struct{}),
		}

		go func() {
			defer close(c.doneC)
			runtime.LockOSThread()
			atomic.StoreInt32(&c.tid, int32(unix.Gettid()))
			for {
				select {
				case <-c.shutdown:
					if c.fd != nil {
						_ = c.fd.Close()
					}
					return
				case op, ok := <-c.opC:
					if !ok {
						if c.fd != nil {
							_ = c.fd.Close()
						}
						return
					}
					op.C <- op.F()
				}
			}
		}()

		err := c.Do(func() error {
			fd, err := kvm.CreateVCPU(vm, slot)
			if err != nil {
				return fmt.Errorf("%w: slot %d: %w", ErrCreateVCPU, slot, err)
			}
			c.fd = fd

			mm, err := unix.Mmap(int(fd.Fd()), 0, mmsz,
				unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)

			if err != nil {
				return fmt.Errorf("%w: slot %d: %w", ErrMmapVCPU, slot, err)
			}
			c.mm = mm

			if err := cfg.Arch.SetupVCPU(slot, c.fd, c.State()); err != nil {
				return fmt.Errorf("%w: slot %d: %w", ErrSetupVCPU, slot, err)
			}

			if slot != 0 {
				if err := kvm.SetMPState(c.fd, &kvm.MPState{State: kvm.MPStateUninitialized}); err != nil {
					return fmt.Errorf("set mp state: slot %d: %w", slot, err)
				}
			}

			return nil
		})

		if err != nil {
			return nil, err
		}

		cpu[slot] = c
	}

	m := &VM{
		fd:    vm,
		cpu:   cpu,
		mem:   mem,
		irqf:  make(map[int]int),
		doneC: make(chan struct{}),
	}

	m.mmio, err = mmio.NewBus(cfg.Devices, mmio.Config{
		MemAt: func(addr uint64, len int) ([]byte, error) {
			return m.mem[addr : addr+uint64(len)], nil
		},

		Notify: func(irq int) error {
			if fd, ok := m.irqf[irq]; ok {
				if _, err := unix.Write(fd, []byte{0, 0, 0, 0, 0, 0, 0, 0}); err != nil {
					return err
				}
			}

			return nil
		},
	})

	if err != nil {
		return nil, fmt.Errorf("vm: create mmio bus: %w", err)
	}

	info := VMInfo{
		MemSize: len(m.mem),
		NumCPU:  len(m.cpu),
		Devices: m.mmio.Devices(),
	}

	// wire up device irqs
	for _, di := range info.Devices {
		fd, err := unix.Eventfd(0, unix.EFD_CLOEXEC)
		if err != nil {
			panic(err)
		}

		err = kvm.IRQFD(m.fd, &kvm.IRQFDConfig{
			Fd:  uint32(fd),
			GSI: uint32(di.IRQ),
		})

		if err != nil {
			panic(err)
		}

		m.irqf[di.IRQ] = fd
	}

	// load memory
	if err := cfg.Loader.LoadMemory(info, m.mem); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLoadMemory, err)
	}

	// load VCPUs
	for slot, c := range m.cpu {
		err := func() error {
			var (
				regs  kvm.Regs
				sregs kvm.Sregs
			)

			if err := kvm.GetRegs(c.fd, &regs); err != nil {
				return fmt.Errorf("get regs: %w", err)
			}

			if err := kvm.GetSregs(c.fd, &sregs); err != nil {
				return fmt.Errorf("get sregs: %w", err)
			}

			if err := cfg.Loader.LoadVCPU(info, slot, &regs, &sregs); err != nil {
				return err
			}

			if err := kvm.SetRegs(c.fd, &regs); err != nil {
				return fmt.Errorf("set regs: %w", err)
			}

			if err := kvm.SetSregs(c.fd, &sregs); err != nil {
				return fmt.Errorf("set sregs: %w", err)
			}

			return nil
		}()

		if err != nil {
			return nil, fmt.Errorf("%w: slot %d: %w", ErrLoadVCPU, slot, err)
		}
	}

	return m, nil
}

func (m *VM) Run(ctx context.Context) error {
	vmLogf(m, "run", "enter")
	select {
	case <-m.doneC:
		vmLogf(m, "run", "already closed before start")
		return ErrVMClosed

	default:
		break
	}

	requestExit := func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.requestExitLocked()
	}

	go func() {
		select {
		case <-m.doneC:
			vmLogf(m, "run", "done channel closed; requesting immediate exit")
			requestExit()

		case <-ctx.Done():
			vmLogf(m, "run", "context canceled; requesting immediate exit")
			requestExit()
		}
	}()

	type vcpuResult struct {
		slot int
		err  error
	}

	runVCPU := func(slot int, c *vcpu) error {
		for {
			if err := ctx.Err(); err != nil {
				return err
			}

			select {
			case <-c.shutdown:
				return ErrVMClosed
			default:
			}

			if err := kvm.Run(c.fd); err != nil {
				if err == unix.EINTR || err == unix.EAGAIN {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					if isShutdown(c.shutdown) {
						return ErrVMClosed
					}

					// EAGAIN can happen during shutdown or while a VCPU is briefly idle.
					time.Sleep(time.Millisecond)
					continue
				}

				if err == unix.EBADF || err == unix.EINVAL {
					if isShutdown(c.shutdown) {
						return ErrVMClosed
					}
				}

				return fmt.Errorf("run vcpu: %w", err)
			}

			state := c.State()

			switch state.ExitReason {
			case kvm.ExitIO:
				if err := ctx.Err(); err != nil {
					return err
				}
				continue

			case kvm.ExitHLT:
				if err := ctx.Err(); err != nil {
					return err
				}
				// Both BSP and APs can otherwise spin hard in HLT exit loops.
				time.Sleep(time.Millisecond)
				continue

			case kvm.ExitAPResetHold:
				if err := ctx.Err(); err != nil {
					return err
				}
				// APs can sit in reset-hold until the BSP sends INIT/SIPI.
				// Avoid a tight userspace spin while waiting.
				time.Sleep(time.Millisecond)
				continue

			case kvm.ExitMMIO:
				xd := state.MMIOExitData()
				if _, err := m.mmio.HandleMMIO(xd.PhysAddr, xd.Data[:xd.Len], xd.IsWrite); err != nil {
					return fmt.Errorf("vcpu mmio: %w", err)
				}
				if err := ctx.Err(); err != nil {
					return err
				}

			case kvm.ExitShutdown:
				return nil

			default:
				return fmt.Errorf("unexpected vmexit reason: %v", state.ExitReason)
			}
		}
	}

	errC := make(chan vcpuResult, len(m.cpu))

	for slot, c := range m.cpu {
		slot, c := slot, c
		go func() {
			errC <- vcpuResult{slot: slot, err: c.Do(func() error { return runVCPU(slot, c) })}
		}()
	}

	for {
		res := <-errC
		vmLogf(m, "run", "vcpu %d returned err=%v", res.slot, res.err)

		if res.slot != 0 {
			if res.err != nil && !errors.Is(res.err, context.Canceled) {
				requestExit()
				return fmt.Errorf("vcpu %d: %w", res.slot, res.err)
			}

			continue
		}

		requestExit()

		if res.err != nil && !errors.Is(res.err, context.Canceled) {
			vmLogf(m, "run", "bsp exit with error: %v", res.err)
			return res.err
		}

		if ctx.Err() != nil {
			vmLogf(m, "run", "exiting due to context cancel: %v", ctx.Err())
			return ctx.Err()
		}

		vmLogf(m, "run", "clean exit")
		return nil
	}
}

// Close stops the VM and releases its resources. It returns ErrVMClosed if the
// VM is already closed. Close closes the VCPUs and waits for them to stop. Then
// it closes the MMIO bus, which closes each of its devices in turn. Then the
// underlying VM fd is closed and the VM's memory is munmaped.
func (m *VM) Close() error {
	vmLogf(m, "close", "begin")
	m.mu.Lock()
	select {
	case <-m.doneC:
		m.mu.Unlock()
		vmLogf(m, "close", "already closed")
		return ErrVMClosed
	default:
		m.requestExitLocked()
		close(m.doneC)
		vmLogf(m, "close", "signaled immediate exit and closed done channel")
	}
	m.mu.Unlock()

	var (
		firstErr error
		hasErr   bool
	)

	if m.fd != nil {
		vmLogf(m, "close", "closing vm fd early")
		if err := m.fd.Close(); err != nil {
			if !hasErr {
				firstErr = fmt.Errorf("close vm fd: %w", err)
				hasErr = true
			}
		}
		m.fd = nil
	}

	for _, c := range m.cpu {
		if c == nil {
			continue
		}
		close(c.shutdown)
		vmLogf(m, "close", "signaled shutdown for vcpu tid=%d", atomic.LoadInt32(&c.tid))
		if tid := int(atomic.LoadInt32(&c.tid)); tid > 0 {
			_ = unix.Tgkill(unix.Getpid(), tid, unix.SIGURG)
		}
		// Force KVM_RUN to unblock even if the VCPU worker is still inside op.F.
		if c.fd != nil {
			if err := c.fd.Close(); err != nil {
				if !hasErr {
					firstErr = fmt.Errorf("close vcpu fd: %w", err)
					hasErr = true
				}
			}
			c.fd = nil
		}
	}

	for i, c := range m.cpu {
		if c == nil {
			continue
		}
		deadline := time.After(5 * time.Second)
		for {
			select {
			case <-c.doneC:
				vmLogf(m, "close", "vcpu %d worker exited", i)
				goto vcpuClosed
			case <-time.After(100 * time.Millisecond):
				if tid := int(atomic.LoadInt32(&c.tid)); tid > 0 {
					_ = unix.Tgkill(unix.Getpid(), tid, unix.SIGURG)
				}
			case <-deadline:
				vmLogf(m, "close", "timeout waiting for vcpu %d worker exit", i)
				if !hasErr {
					firstErr = fmt.Errorf("timeout waiting for vcpu %d shutdown", i)
					hasErr = true
				}
				goto vcpuClosed
			}
		}
	vcpuClosed:
		if c.mm != nil {
			if err := unix.Munmap(c.mm); err != nil {
				if !hasErr {
					firstErr = fmt.Errorf("unmap vcpu memory: %w", err)
					hasErr = true
				}
			}
			c.mm = nil
		}
	}

	if m.fd != nil {
		vmLogf(m, "close", "closing vm fd")
		if err := m.fd.Close(); err != nil {
			if !hasErr {
				firstErr = fmt.Errorf("close vm fd: %w", err)
				hasErr = true
			}
		}
		m.fd = nil
	}

	for _, fd := range m.irqf {
		if fd > 0 {
			_ = unix.Close(int(fd))
		}
	}
	m.irqf = nil
	vmLogf(m, "close", "closed irqfds")

	if m.mmio != nil {
		vmLogf(m, "close", "closing mmio bus")
		if err := m.mmio.Close(); err != nil {
			if !hasErr {
				firstErr = fmt.Errorf("close mmio: %w", err)
				hasErr = true
			}
		}
	}

	if m.mem != nil {
		vmLogf(m, "close", "unmapping guest memory")
		if err := unix.Munmap(m.mem); err != nil {
			if !hasErr {
				firstErr = fmt.Errorf("unmap memory: %w", err)
				hasErr = true
			}
		}
		m.mem = nil
	}

	if firstErr != nil {
		vmLogf(m, "close", "complete with error: %v", firstErr)
		return firstErr
	}
	vmLogf(m, "close", "complete")
	return nil
}

func isShutdown(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func (m *VM) requestExitLocked() {
	for _, c := range m.cpu {
		if c == nil || c.mm == nil {
			continue
		}
		c.State().ImmediateExit = 1
	}
}

// ThreadIDs returns current host thread IDs used by the VM's VCPU workers.
func (m *VM) ThreadIDs() []int {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]int, 0, len(m.cpu))
	for _, c := range m.cpu {
		if c == nil {
			continue
		}
		tid := int(atomic.LoadInt32(&c.tid))
		if tid > 0 {
			ids = append(ids, tid)
		}
	}
	return ids
}

func (c *vcpu) State() *kvm.VCPUState {
	if c == nil || c.mm == nil {
		return nil
	}
	return (*kvm.VCPUState)(unsafe.Pointer(&c.mm[0]))
}

// Do runs f on the VCPU's thread and returns its result. Calls are serialized.
func (c *vcpu) Do(f func() error) error {
	select {
	case <-c.shutdown:
		return ErrVMClosed
	default:
	}

	op := vcpuOp{f, make(chan error, 1)}
	select {
	case <-c.shutdown:
		return ErrVMClosed
	case c.opC <- op:
	}

	select {
	case <-c.shutdown:
		return ErrVMClosed
	case err := <-op.C:
		return err
	}
}

func (cfg Config) validate() error {
	if cfg.NumCPU < NumCPUMin {
		return fmt.Errorf("number of cpus must be at least %d", NumCPUMin)
	}

	if pgsz := os.Getpagesize(); cfg.MemSize%pgsz != 0 {
		return fmt.Errorf("memory size must be a multiple of the host page size (%d)", pgsz)
	}

	if cfg.MemSize < MemSizeMin {
		return fmt.Errorf("memory is too small: %d < %d", cfg.MemSize, MemSizeMin)
	}

	if cfg.MemSize > MemSizeMax {
		return fmt.Errorf("memory is too large: %d > %d", cfg.MemSize, MemSizeMax)
	}

	if cfg.Loader == nil {
		return errors.New("loader is not set")
	}

	return nil
}

func (cfg Config) withDefaults() Config {
	if cfg.NumCPU == 0 {
		cfg.NumCPU = NumCPUDefault
	}

	if cfg.MemSize == 0 {
		cfg.MemSize = MemSizeDefault
	}

	return cfg
}
