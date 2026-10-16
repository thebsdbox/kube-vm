package mmio

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"sync"
	"unsafe"

	"kube-vm/virtio"
	"kube-vm/virtio/virtq"

	"golang.org/x/sys/unix"
)

type Config struct {
	MemAt  func(addr uint64, size int) ([]byte, error)
	Notify func(irq int) error
}

type Bus struct {
	cfg    Config
	dev    []*device
	ioGate sync.RWMutex
}

// Quiesce waits for complete device operations and prevents new I/O until the
// single-use release function is called. Stop guest CPUs/MMIO first. Do not
// call Close while holding this gate.
func (b *Bus) Quiesce() func() {
	b.ioGate.Lock()
	return b.ioGate.Unlock
}

func (b *Bus) beginAccess() func() {
	b.ioGate.RLock()
	return b.ioGate.RUnlock
}

type device struct {
	bus  *Bus
	info DeviceInfo

	mu      sync.Mutex
	handler virtio.DeviceHandler
	state   deviceState

	qC     map[int]chan struct{}
	queues map[int]*virtq.Queue
}

type deviceState struct {
	status  uint32
	version uint32

	deviceFeaturesSel uint32
	driverFeaturesSel uint32
	driverFeatures    uint64

	queueSel uint32
	queue    [16]queueState

	intStatus uint32
}

type queueState struct {
	Ready      uint32
	NumDesc    uint32
	DescAddr   uint64 // address of the descriptor area
	DriverAddr uint64 // address of the driver area
	DeviceAddr uint64 // address of the device area
}

const (
	statusAcknowledge = 1   // recognized by the guest
	statusDriver      = 2   // the guest has a driver
	statusFeaturesOK  = 8   // features negotiated
	statusDriverOK    = 4   // ready to drive
	statusNeedsReset  = 64  // fatal device error
	statusFailed      = 128 // fatal driver error

	negotiatingFeatures = statusAcknowledge | statusDriver
	configuringQueues   = negotiatingFeatures | statusFeaturesOK
	operatingNormally   = configuringQueues | statusDriverOK
)

var le = binary.LittleEndian

// NewBus creates a new bus and installs a device for for each of the given
// handlers. The configured MemAt callback is called when a device needs to
// access a virtqueue in guest memory. The configured Notify callback is called
// when a device needs to notify the guest of a config or buffer event.
//
// Devices are assigned an IRQ and a 4K memory region. See the Devices method.
func NewBus(devices []virtio.DeviceConfig, cfg Config) (*Bus, error) {
	const sz = 0x1000

	var (
		irq  = 5
		addr = uint64(0xd0000000)
	)

	b := &Bus{
		cfg: cfg,
		dev: make([]*device, len(devices)),
	}

	for i, dc := range devices {
		h, err := dc.NewHandler()
		if err != nil {
			return nil, fmt.Errorf("create device[%d]: %w", i, err)
		}

		d := &device{
			bus: b,

			info: DeviceInfo{
				Type: h.GetType(),
				IRQ:  irq,
				Addr: addr,
				Size: sz,
			},

			handler: h,
			qC:      make(map[int]chan struct{}),
			queues:  make(map[int]*virtq.Queue),
		}

		b.dev[i] = d

		irq++
		addr += sz
	}

	return b, nil
}

// HandleMMIO routes an MMIO event to the appropriate device.
// It returns (found=false, err=nil) if no device is found.
func (b *Bus) HandleMMIO(addr uint64, data []byte, isWrite bool) (found bool, err error) {
	var dev *device
	for _, d := range b.dev {
		if addr >= d.info.Addr && addr < d.info.Addr+d.info.Size {
			dev = d
			break
		}
	}

	if dev == nil {
		return false, nil
	}

	off := int(addr - dev.info.Addr)
	return true, dev.HandleMMIO(off, data, isWrite)
}

// Devices returns a slice describing the installed devices.
func (b *Bus) Devices() []DeviceInfo {
	dd := make([]DeviceInfo, len(b.dev))
	for i, d := range b.dev {
		dd[i] = d.info
	}

	return dd
}

// Close closes all devices, returning the first error.
func (b *Bus) Close() error {
	for _, d := range b.dev {
		if err := d.Close(); err != nil {
			return fmt.Errorf("close %v: %w", d.info.Type, err)
		}
	}

	return nil
}

// DeviceState represents the exported state of a device that can be saved/restored.
type DeviceState struct {
	SnapshotVersion   uint32
	Type              virtio.DeviceID
	Features          uint64
	QueueCursors      map[int]virtq.QueueState
	HandlerState      []byte
	Status            uint32
	Version           uint32
	DeviceFeaturesSel uint32
	DriverFeaturesSel uint32
	DriverFeatures    uint64
	QueueSel          uint32
	Queue             [16]queueState
	IntStatus         uint32
}

// ExportDeviceState requires Quiesce and stopped guest CPUs/MMIO until guest
// memory has also been copied.
func (b *Bus) ExportDeviceState() []DeviceState {
	states := make([]DeviceState, len(b.dev))
	for i, d := range b.dev {
		d.mu.Lock()
		states[i] = DeviceState{
			SnapshotVersion:   1,
			Type:              d.info.Type,
			Features:          d.getFeatures(),
			QueueCursors:      make(map[int]virtq.QueueState),
			Status:            d.state.status,
			Version:           d.state.version,
			DeviceFeaturesSel: d.state.deviceFeaturesSel,
			DriverFeaturesSel: d.state.driverFeaturesSel,
			DriverFeatures:    d.state.driverFeatures,
			QueueSel:          d.state.queueSel,
			Queue:             d.state.queue,
			IntStatus:         d.state.intStatus,
		}
		for n, q := range d.queues {
			states[i].QueueCursors[n] = q.Snapshot()
		}
		d.mu.Unlock()
		if h, ok := d.handler.(handlerSnapshot); ok {
			states[i].HandlerState = h.SnapshotHandlerState()
		}
	}
	return states
}

type handlerSnapshot interface {
	SnapshotHandlerState() []byte
	ValidateHandlerState([]byte) error
	RestoreHandlerState([]byte) error
}

// RestoreDeviceState requires a fresh bus, restored guest memory, stopped CPUs
// and the caller holding Quiesce. All devices and queues are validated before
// starting any handler. On handler startup failure, discard the bus.
func (b *Bus) RestoreDeviceState(states []DeviceState) error {
	if err := b.ValidateSnapshotSupport(); err != nil {
		return err
	}
	if len(states) != len(b.dev) {
		return fmt.Errorf("device count mismatch: got %d want %d", len(states), len(b.dev))
	}
	prepared := make([]map[int]*virtq.Queue, len(states))
	var regions [][2]uint64
	for i, ds := range states {
		d := b.dev[i]
		if len(d.queues) != 0 || d.state.status != 0 {
			return fmt.Errorf("device %d: restore requires a fresh bus", i)
		}
		for _, qs := range ds.Queue {
			if qs.Ready != 0 && ds.SnapshotVersion != 1 {
				return fmt.Errorf("device %d: snapshot version error: ready queues require packed cursor state version 1", i)
			}
		}
		if ds.SnapshotVersion != 1 || ds.Type != d.info.Type || ds.Features != d.getFeatures() {
			return fmt.Errorf("device %d: snapshot version, type or features mismatch", i)
		}
		if ds.QueueSel >= 16 || ds.DeviceFeaturesSel > 1 || ds.DriverFeaturesSel > 1 || ds.DriverFeatures&^ds.Features != 0 {
			return fmt.Errorf("device %d: invalid selectors or negotiated features", i)
		}
		const knownStatus = statusAcknowledge | statusDriver | statusFeaturesOK | statusDriverOK | statusNeedsReset | statusFailed
		if ds.Status&^uint32(knownStatus) != 0 ||
			(ds.Status&statusDriver != 0 && ds.Status&statusAcknowledge == 0) ||
			(ds.Status&statusFeaturesOK != 0 && ds.Status&negotiatingFeatures != negotiatingFeatures) {
			return fmt.Errorf("device %d: invalid device status", i)
		}
		if ds.Status&statusDriverOK != 0 && (ds.DriverFeatures&virtio.RequiredFeatures != virtio.RequiredFeatures || ds.Status&configuringQueues != configuringQueues) {
			return fmt.Errorf("device %d: invalid ready status/features", i)
		}
		if ds.Status&statusDriverOK != 0 && d.info.Type == virtio.BlockDeviceID {
			_, readOnly := d.handler.(blockSnapshot).SnapshotBlockStorage()
			// VIRTIO_BLK_F_RO must be acknowledged before a read-only device runs.
			if readOnly && ds.DriverFeatures&(1<<4) == 0 {
				return fmt.Errorf("device %d: read-only block feature was not negotiated", i)
			}
		}
		if h, ok := d.handler.(handlerSnapshot); ok {
			if err := h.ValidateHandlerState(ds.HandlerState); err != nil {
				return fmt.Errorf("device %d: %w", i, err)
			}
		} else if len(ds.HandlerState) != 0 {
			return fmt.Errorf("device %d: unsupported handler state", i)
		}
		prepared[i] = make(map[int]*virtq.Queue)
		for n, qs := range ds.Queue {
			if qs.Ready > 1 {
				return fmt.Errorf("device %d queue %d: invalid ready bit", i, n)
			}
			if qs.Ready == 0 {
				continue
			}
			if ds.Status&configuringQueues != configuringQueues {
				return fmt.Errorf("device %d queue %d: ready queue before feature negotiation", i, n)
			}
			cursor, ok := ds.QueueCursors[n]
			if !ok {
				return fmt.Errorf("device %d queue %d: snapshot version error: missing packed cursor state", i, n)
			}
			if !validQueueNumber(d.info.Type, n) {
				return fmt.Errorf("device %d: unsupported queue %d", i, n)
			}
			q, err := d.newQueue(qs)
			if err != nil {
				return fmt.Errorf("device %d queue %d: %w", i, n, err)
			}
			for _, r := range [][2]uint64{
				{qs.DescAddr, qs.DescAddr + uint64(qs.NumDesc)*16},
				{qs.DriverAddr, qs.DriverAddr + 4},
				{qs.DeviceAddr, qs.DeviceAddr + 4},
			} {
				for _, prev := range regions {
					if r[0] < prev[1] && prev[0] < r[1] {
						return fmt.Errorf("device %d queue %d: overlapping queue memory regions", i, n)
					}
				}
				regions = append(regions, r)
			}
			if err := q.RestoreState(cursor); err != nil {
				return fmt.Errorf("device %d queue %d: %w", i, n, err)
			}
			prepared[i][n] = q
		}
		if len(prepared[i]) != len(ds.QueueCursors) {
			return fmt.Errorf("device %d: cursor state for non-ready queue", i)
		}
	}
	for i, ds := range states {
		d := b.dev[i]
		d.mu.Lock()
		d.state.status = ds.Status
		d.state.version = ds.Version
		d.state.deviceFeaturesSel = ds.DeviceFeaturesSel
		d.state.driverFeaturesSel = ds.DriverFeaturesSel
		d.state.driverFeatures = ds.DriverFeatures
		d.state.queueSel = ds.QueueSel
		d.state.queue = ds.Queue
		d.state.intStatus = ds.IntStatus
		d.queues = prepared[i]
		d.mu.Unlock()
		if h, ok := d.handler.(handlerSnapshot); ok {
			if err := h.RestoreHandlerState(ds.HandlerState); err != nil {
				return err
			}
		}
		if ds.Status&statusDriverOK != 0 {
			if err := d.handler.Ready(ds.DriverFeatures); err != nil {
				return fmt.Errorf("device %d Ready: %w", i, err)
			}
		}
		for n := 0; n < len(ds.Queue); n++ {
			q, ok := prepared[i][n]
			if !ok {
				continue
			}
			c := make(chan struct{}, 1)
			d.mu.Lock()
			d.qC[n] = c
			d.mu.Unlock()
			if err := d.handler.QueueReady(n, q, c); err != nil {
				return fmt.Errorf("device %d queue %d: %w", i, n, err)
			}
			c <- struct{}{}
		}
		if ds.IntStatus != 0 && b.cfg.Notify != nil {
			if err := b.cfg.Notify(d.info.IRQ); err != nil {
				return fmt.Errorf("device %d restore interrupt: %w", i, err)
			}
		}
	}
	return nil
}

func (d *device) HandleMMIO(off int, data []byte, isWrite bool) (err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	defer func() {
		if err != nil && !(d.needsReset() || d.driverFailed()) {
			notify := d.isOperatingNormally()
			d.state.status |= statusNeedsReset
			d.state.version++

			if notify {
				d.state.intStatus |= intStatusConfigChange
				d.mu.Unlock()
				var notifyErr error
				if d.bus.cfg.Notify != nil {
					notifyErr = d.bus.cfg.Notify(d.info.IRQ)
				}
				d.mu.Lock()
				if notifyErr != nil {
					slog.Error("virtio config change notification failed",
						"irq", d.info.IRQ, "err", notifyErr)
				}
			}
		}
	}()

	if isWrite {
		return d.writeMMIO(off, data)
	}

	return d.readMMIO(off, data)
}

// Close closes the device's notification channels,
// then calls the handler's Close method,
// returning any error.
func (d *device) Close() error {
	d.mu.Lock()

	for _, c := range d.qC {
		close(c)
	}
	d.qC = make(map[int]chan struct{})
	d.mu.Unlock()

	return d.handler.Close()
}

func (d *device) readMMIO(off int, p []byte) error {
	switch off {
	case regMagicValue:
		le.PutUint32(p, virtio.MagicValue)

	case regVersion:
		le.PutUint32(p, virtio.Version)

	case regDeviceID:
		le.PutUint32(p, uint32(d.handler.GetType()))

	// FIX: ?
	case regVendorID:
		le.PutUint32(p, 0xffff)

	case regDeviceFeatures:
		le.PutUint32(p, uint32(d.getFeatures()>>(32*d.state.deviceFeaturesSel)))

	case regQueueNumMax:
		le.PutUint32(p, 1<<15)

	case regQueueReady:
		le.PutUint32(p, d.selectedQueue().Ready)

	case regInterruptStatus:
		le.PutUint32(p, d.state.intStatus)

	case regStatus:
		le.PutUint32(p, d.state.status)

	case regConfigGeneration:
		le.PutUint32(p, d.state.version)

	default:
		switch {
		case off >= regDeviceConfigStart:
			if err := d.handler.ReadConfig(p, off-regDeviceConfigStart); err != nil {
				panic(err)
			}

		default:
			panic(off)
		}
	}

	return nil
}

func (d *device) writeMMIO(off int, p []byte) error {
	// if the device or driver has failed, only allow status register writes (to reset)
	if d.state.status&(statusNeedsReset|statusFailed) > 0 && off != regStatus {
		return unix.EPERM
	}

	switch off {
	case regDeviceFeaturesSel:
		return d.writeDeviceFeaturesSel(le.Uint32(p))

	case regDriverFeatures:
		return d.writeDriverFeatures(le.Uint32(p))

	case regDriverFeaturesSel:
		return d.writeDriverFeaturesSel(le.Uint32(p))

	case regQueueSel:
		return d.writeQueueSel(le.Uint32(p))

	case regQueueNum:
		return d.writeQueueNum(le.Uint32(p))

	case regQueueReady:
		return d.writeQueueReady(le.Uint32(p))

	case regQueueNotify:
		return d.writeQueueNotify(le.Uint32(p))

	case regInterruptAck:
		return d.writeInterruptAck(le.Uint32(p))

	case regStatus:
		return d.writeStatus(le.Uint32(p))

	case regQueueDescLow:
		return d.writeQueueDescLow(le.Uint32(p))

	case regQueueDescHigh:
		return d.writeQueueDescHigh(le.Uint32(p))

	case regQueueDriverLow:
		return d.writeQueueDriverLow(le.Uint32(p))

	case regQueueDriverHigh:
		return d.writeQueueDriverHigh(le.Uint32(p))

	case regQueueDeviceLow:
		return d.writeQueueDeviceLow(le.Uint32(p))

	case regQueueDeviceHigh:
		return d.writeQueueDeviceHigh(le.Uint32(p))

	default:
		panic(off)
	}
}

func (d *device) writeStatus(v uint32) error {
	if v == 0 {
		// reset
		d.state = deviceState{}
		return nil
	}

	if v&statusNeedsReset > 0 || v < d.state.status {
		panic("bad status")
	}

	d.state.status = v
	d.state.version++

	if v&statusFailed > 0 {
		panic("driver failed")
	}

	if d.isOperatingNormally() {
		if d.state.driverFeatures&virtio.RequiredFeatures != virtio.RequiredFeatures {
			panic("missing required feature bits")
		}

		features := d.state.driverFeatures
		d.mu.Unlock()
		err := d.handler.Ready(features)
		d.mu.Lock()
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *device) writeDeviceFeaturesSel(v uint32) error {
	if !d.isNegotiatingFeatures() {
		return unix.EPERM
	}

	if v > 1 {
		return unix.EINVAL
	}

	d.state.deviceFeaturesSel = v

	return nil
}

func (d *device) writeDriverFeaturesSel(v uint32) error {
	if !d.isNegotiatingFeatures() {
		return unix.EPERM
	}

	if v > 1 {
		return unix.EINVAL
	}

	d.state.driverFeaturesSel = v
	return nil
}

func (d *device) writeDriverFeatures(v uint32) error {
	if !d.isNegotiatingFeatures() {
		return unix.EPERM
	}

	d.state.driverFeatures |= uint64(v) << (32 * d.state.driverFeaturesSel)

	if d.state.driverFeatures&^d.getFeatures() != 0 {
		return unix.EINVAL
	}

	return nil
}

func (d *device) writeQueueSel(v uint32) error {
	if !d.isConfiguringQueues() {
		return unix.EPERM
	}

	if v >= uint32(len(d.state.queue)) {
		return unix.EINVAL
	}
	d.state.queueSel = v
	return nil
}

func (d *device) writeQueueNum(v uint32) error {
	if !d.isConfiguringQueues() {
		return unix.EPERM
	}

	d.selectedQueue().NumDesc = v
	return nil
}

func (d *device) writeQueueDescLow(v uint32) error {
	if !d.isConfiguringQueues() || d.selectedQueue().Ready == 1 {
		return unix.EPERM
	}

	d.selectedQueue().DescAddr |= uint64(v)
	return nil
}

func (d *device) writeQueueDescHigh(v uint32) error {
	if !d.isConfiguringQueues() || d.selectedQueue().Ready == 1 {
		return unix.EPERM
	}

	d.selectedQueue().DescAddr |= uint64(v) << 32
	return nil
}

func (d *device) writeQueueDriverLow(v uint32) error {
	if !d.isConfiguringQueues() || d.selectedQueue().Ready == 1 {
		return unix.EPERM
	}

	d.selectedQueue().DriverAddr |= uint64(v)
	return nil
}

func (d *device) writeQueueDriverHigh(v uint32) error {
	if !d.isConfiguringQueues() || d.selectedQueue().Ready == 1 {
		return unix.EPERM
	}

	d.selectedQueue().DriverAddr |= uint64(v) << 32
	return nil
}

func (d *device) writeQueueDeviceLow(v uint32) error {
	if !d.isConfiguringQueues() || d.selectedQueue().Ready == 1 {
		return unix.EPERM
	}

	d.selectedQueue().DeviceAddr |= uint64(v)
	return nil
}

func (d *device) writeQueueDeviceHigh(v uint32) error {
	if !d.isConfiguringQueues() || d.selectedQueue().Ready == 1 {
		return unix.EPERM
	}

	d.selectedQueue().DeviceAddr |= uint64(v) << 32
	return nil
}

func (d *device) writeQueueReady(v uint32) error {
	if !d.isConfiguringQueues() {
		return unix.EPERM
	}

	if v != 1 {
		return unix.EINVAL
	}

	if d.selectedQueue().Ready == 1 {
		return unix.EPERM
	}

	d.selectedQueue().Ready = 1
	d.state.version++

	qn := int(d.state.queueSel)
	if !validQueueNumber(d.info.Type, qn) {
		return unix.EINVAL
	}
	q, err := d.newQueue(*d.selectedQueue())
	if err != nil {
		return err
	}
	qc := make(chan struct{}, 1)
	d.qC[qn] = qc
	d.queues[qn] = q
	d.mu.Unlock()
	err = d.handler.QueueReady(qn, q, qc)
	d.mu.Lock()
	return err
}

func validQueueNumber(typ virtio.DeviceID, n int) bool {
	switch typ {
	case virtio.BlockDeviceID:
		return n == 0
	case virtio.NetworkDeviceID, virtio.ConsoleDeviceID:
		return n >= 0 && n < 2
	case virtio.SocketDeviceID:
		return n >= 0 && n < 3
	default:
		return false
	}
}

func (d *device) newQueue(qs queueState) (*virtq.Queue, error) {
	if qs.NumDesc == 0 || qs.NumDesc > 1<<15 {
		return nil, fmt.Errorf("invalid packed queue size %d", qs.NumDesc)
	}
	region := func(addr uint64, size int, alignment uint64) ([]byte, error) {
		if addr%alignment != 0 || addr > ^uint64(0)-uint64(size) {
			return nil, fmt.Errorf("invalid queue memory address %#x", addr)
		}
		if d.bus.cfg.MemAt == nil {
			return nil, fmt.Errorf("queue memory is not installed")
		}
		p, err := d.bus.cfg.MemAt(addr, size)
		if err != nil {
			return nil, err
		}
		if len(p) != size || uintptr(unsafe.Pointer(&p[0]))%uintptr(alignment) != 0 {
			return nil, fmt.Errorf("short or unaligned queue memory at %#x", addr)
		}
		return p, nil
	}
	rngA, err := region(qs.DescAddr, int(16*qs.NumDesc), 16)
	if err != nil {
		return nil, err
	}

	drvA, err := region(qs.DriverAddr, 4, 4)
	if err != nil {
		return nil, err
	}

	devA, err := region(qs.DeviceAddr, 4, 4)
	if err != nil {
		return nil, err
	}
	ends := [3]uint64{qs.DescAddr + uint64(16*qs.NumDesc), qs.DriverAddr + 4, qs.DeviceAddr + 4}
	starts := [3]uint64{qs.DescAddr, qs.DriverAddr, qs.DeviceAddr}
	for i := range starts {
		for j := 0; j < i; j++ {
			if starts[i] < ends[j] && starts[j] < ends[i] {
				return nil, fmt.Errorf("overlapping queue memory regions")
			}
		}
	}

	var (
		ring = unsafe.Slice((*virtq.Desc)(unsafe.Pointer(&rngA[0])), qs.NumDesc)
		drvE = (*virtq.EventSuppress)(unsafe.Pointer(&drvA[0]))
		devE = (*virtq.EventSuppress)(unsafe.Pointer(&devA[0]))
	)

	q := virtq.New(ring, drvE, devE, virtq.Config{
		MemAt:       d.bus.cfg.MemAt,
		BeginAccess: d.bus.beginAccess,
		Notify: func() error {
			d.mu.Lock()

			d.state.intStatus |= intStatusUsedBuffer
			d.mu.Unlock()
			if d.bus.cfg.Notify == nil {
				return nil
			}
			if err := d.bus.cfg.Notify(d.info.IRQ); err != nil {
				return err
			}

			return nil
		},
	})

	return q, nil
}

func (d *device) writeQueueNotify(v uint32) error {
	if !d.isOperatingNormally() {
		return unix.EPERM
	}

	if v >= uint32(len(d.state.queue)) || d.state.queue[v].Ready != 1 {
		return unix.EPERM
	}

	if c, ok := d.qC[int(v)]; ok {
		select {
		case c <- struct{}{}:
		default:
		}
	}

	return nil
}

func (d *device) writeInterruptAck(v uint32) error {
	if !d.isOperatingNormally() {
		return unix.EPERM
	}

	// clear flags
	d.state.intStatus &^= v

	return nil
}

func (d *device) getFeatures() uint64 {
	return virtio.RequiredFeatures | d.handler.GetFeatures()
}

func (d *device) isNegotiatingFeatures() bool {
	return d.state.status == negotiatingFeatures
}

func (d *device) isConfiguringQueues() bool {
	return d.state.status == configuringQueues
}

func (d *device) isOperatingNormally() bool {
	return d.state.status == operatingNormally
}

func (d *device) needsReset() bool {
	return d.state.status&statusNeedsReset != 0
}

func (d *device) driverFailed() bool {
	return d.state.status&statusFailed != 0
}

func (d *device) selectedQueue() *queueState {
	return &d.state.queue[d.state.queueSel]
}
