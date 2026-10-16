// Package virtq partially implements packed virtqueues as described by the Virtual I/O
// Device (VIRTIO) Version 1.2 spec. Split virtqueues are not supported.
package virtq

import (
	"errors"
	"sync/atomic"
	"unsafe"
)

type Config struct {
	MemAt  func(addr uint64, len int) ([]byte, error)
	Notify func() error
	// BeginAccess obtains the bus I/O gate and returns its release function.
	// Nil permits standalone queues without a bus.
	BeginAccess func() func()
}

// QueueState is the host cursor state of a packed queue. It does not include
// guest memory. Snapshot and RestoreState require a quiesced queue.
type QueueState struct {
	AvailIndex, UsedIndex uint16
	AvailWrap, UsedWrap   bool
}

// WithAccess runs a complete chain or drain operation under the shared I/O gate.
// Do not nest calls or wait for host input inside fn.
func (q *Queue) WithAccess(fn func() error) error {
	if q.cfg.BeginAccess != nil {
		release := q.cfg.BeginAccess()
		defer release()
	}
	return fn()
}

func (q *Queue) Snapshot() QueueState {
	return QueueState{q.aidx, q.uidx, q.availWrap, q.usedWrap}
}

// RestoreState rejects out-of-range cursors and incomplete chains. The caller
// must hold the exclusive bus gate (or otherwise stop a standalone queue).
func (q *Queue) RestoreState(s QueueState) error {
	if len(q.ring) == 0 || len(q.ring) > 1<<15 || int(s.AvailIndex) >= len(q.ring) || int(s.UsedIndex) >= len(q.ring) {
		return errors.New("invalid packed queue cursor or ring size")
	}
	if s.AvailIndex != s.UsedIndex || s.AvailWrap != s.UsedWrap {
		return errors.New("packed queue snapshot contains an incomplete chain")
	}
	q.aidx, q.uidx, q.availWrap, q.usedWrap = s.AvailIndex, s.UsedIndex, s.AvailWrap, s.UsedWrap
	return nil
}

// Queue is a packed virtqueue.
type Queue struct {
	cfg  Config
	ring []Desc
	drvE *EventSuppress
	devE *EventSuppress

	aidx      uint16
	uidx      uint16
	availWrap bool
	usedWrap  bool
}

// Chain is a descriptor chain in a packed virtqueue.
type Chain struct {
	q    *Queue
	id   uint16
	skip uint16
	Desc []Desc
}

// Desc is a packed virtqueue descriptor.
type Desc struct {
	Addr  uint64
	Len   uint32
	ID    uint16
	Flags uint16
}

// EventSuppress is the driver or device event suppression area for a packed virtqueue.
type EventSuppress struct {
	Desc  uint16
	Flags uint16
}

const (
	DescFNext     = 1 // buffer continues in the next descriptor
	DescFWrite    = 2 // buffer is device wo (otherwise ro)
	DescFIndirect = 4 // buffer contains a descriptor table
	DescFAvail    = 1 << 7
	DescFUsed     = 1 << 15
)

const (
	eventFlagsEnable  = 0x0 // enable events
	eventFlagsDisable = 0x1 // disable events
	eventFlagsDesc    = 0x2 // enable events for a specific descriptor
	_                 = 0x3 // reserved
)

// New returns a new virtqueue backed by the given ring and event suppression areas.
func New(ring []Desc, drvE, devE *EventSuppress, cfg Config) *Queue {
	return &Queue{cfg: cfg, ring: ring, drvE: drvE, devE: devE, availWrap: true, usedWrap: true}
}

// Next returns the next available descriptor chain, or nil if no descriptors
// are available. It returns an error if the queue's MemAt callback fails while
// resolving an indirect descriptor, or if an indirect descriptor has a
// malformed buffer.
func (q *Queue) Next() (avail *Chain, err error) {
	if len(q.ring) == 0 {
		return
	}

	i, ok := q.advance()

	if !ok {
		return
	}

	c := &Chain{
		q:    q,
		id:   q.ring[i].ID,
		skip: 1,
		Desc: []Desc{q.ring[i]},
	}

	switch {
	case q.ring[i].Continues():
		for {
			if int(c.skip) == len(q.ring) {
				return nil, errors.New("descriptor chain exceeds ring size")
			}
			i, ok = q.advance()

			if !ok {
				return nil, errors.New("descriptor continues but no next descriptor is available")
			}
			c.Desc = append(c.Desc, q.ring[i])
			c.skip++

			if !q.ring[i].Continues() {
				break
			}
		}

		// For a direct packed chain, the buffer ID is in its last descriptor.
		c.id = q.ring[i].ID

	case q.ring[i].IsIndirect():
		data, err := q.getBuf(q.ring[i])
		if err != nil {
			return nil, err
		}

		if len(data) == 0 || len(data)%16 != 0 {
			return nil, errors.New("malformed indirect buffer")
		}

		c.Desc = unsafe.Slice((*Desc)(unsafe.Pointer(&data[0])), len(data)/16)
	}

	return c, nil
}

func (q *Queue) getBuf(d Desc) (buf []byte, err error) {
	if d.Len == 0 {
		return
	}

	buf, err = q.cfg.MemAt(d.Addr, int(d.Len))
	if err != nil {
		return
	}

	if len(buf) != int(d.Len) {
		return nil, errors.New("short buffer")
	}

	return
}

func (q *Queue) advance() (index uint16, ok bool) {
	// ID and flags form an aligned 32-bit word in a packed descriptor.
	// Load ownership before consuming descriptor fields written by the guest.
	flags := uint16(atomic.LoadUint32((*uint32)(unsafe.Pointer(&q.ring[q.aidx].ID))) >> 16)
	a := flags&DescFAvail != 0
	u := flags&DescFUsed != 0
	if a == u || a != q.availWrap {
		return
	}

	index = q.aidx
	ok = true

	q.aidx++
	if q.aidx == uint16(len(q.ring)) {
		q.aidx = 0
		q.availWrap = !q.availWrap
	}

	return
}

func (q *Queue) release(c *Chain, bytesWritten int) error {
	d := &q.ring[q.uidx]
	a := d.Flags&DescFAvail != 0
	u := d.Flags&DescFUsed != 0
	if a == u || a != q.usedWrap {
		panic("ring full")
	}

	var flags uint16

	if q.usedWrap {
		flags |= 1<<7 | 1<<15
	}

	if bytesWritten > 0 {
		flags |= DescFWrite
	}

	// Publish ownership last, after the payload, status and used length.
	// A whole-struct assignment may expose used flags before the other fields.
	d.Len = uint32(bytesWritten)
	atomic.StoreUint32((*uint32)(unsafe.Pointer(&d.ID)), uint32(c.id)|uint32(flags)<<16)

	uidx := q.uidx
	wrap := q.usedWrap

	q.uidx += c.skip
	if q.uidx >= uint16(len(q.ring)) {
		q.uidx -= uint16(len(q.ring))
		q.usedWrap = !q.usedWrap
	}

	if q.drvE != nil && q.cfg.Notify != nil {
		// A chain may pass the driver's requested event offset, including across
		// the ring boundary. Checking only its head can miss the wakeup.
		event := atomic.LoadUint32((*uint32)(unsafe.Pointer(q.drvE)))
		e := EventSuppress{Desc: uint16(event), Flags: uint16(event >> 16)}
		for n := uint16(0); n < c.skip; n++ {
			if e.ShouldNotify(uidx, wrap) {
				return q.cfg.Notify()
			}
			uidx++
			if uidx == uint16(len(q.ring)) {
				uidx = 0
				wrap = !wrap
			}
		}
	}

	return nil
}

// Buf returns a slice aliasing the buffer described by the descriptor at the
// given index. It panics if the index is out of range. If the queue's MemAt
// callback fails, Buf returns the error.
func (c *Chain) Buf(i int) ([]byte, error) {
	return c.q.getBuf(c.Desc[i])
}

// Release marks the chain as used, recording the number of bytes written to the
// chain. It returns an error if the queue's Notify callback fails.
func (c *Chain) Release(bytesWritten int) error {
	return c.q.release(c, bytesWritten)
}

// Continues returns true if the descriptor's buffer continues in the next descriptor.
func (d Desc) Continues() bool {
	return d.Flags&DescFNext != 0
}

// IsWO returns true if the descriptor's buffer is device write-only.
func (d Desc) IsWO() bool {
	return d.Flags&DescFWrite != 0
}

// IsRO returns true if the descriptor's buffer is device read-only.
func (d Desc) IsRO() bool {
	return !d.IsWO()
}

// IsIndirect returns true if the descriptor's buffer contains more descriptors.
func (d Desc) IsIndirect() bool {
	return d.Flags&DescFIndirect != 0
}

// ShouldNotify returns true if a notification should be sent for the given
// descriptor index and wrap counter, or false if the event is suppressed.
func (e EventSuppress) ShouldNotify(index uint16, wrap bool) bool {
	return e.Flags == eventFlagsEnable || (e.Flags == eventFlagsDesc &&
		e.Desc&^(1<<15) == index && (e.Desc>>15 == 1) == wrap)
}
