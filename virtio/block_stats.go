package virtio

import (
	"sync/atomic"
	"time"
)

// BlockStats reports cumulative guest-visible I/O since device creation.
// Bytes count actual transfers (including partial transfers); operations count
// attempted requests, not individual scatter/gather segments.
type BlockStats struct {
	Spec          string `json:"spec"`
	CapacityBytes uint64 `json:"capacity_bytes"`
	ReadOnly      bool   `json:"read_only"`
	ReadOps       uint64 `json:"read_ops"`
	WriteOps      uint64 `json:"write_ops"`
	FlushOps      uint64 `json:"flush_ops"`
	ReadBytes     uint64 `json:"read_bytes"`
	WriteBytes    uint64 `json:"write_bytes"`
	Errors        uint64 `json:"errors"`
	CompletedOps  uint64 `json:"completed_ops"`
	InFlight      int64  `json:"in_flight"`
	IOTimeNS      uint64 `json:"io_time_ns"`
	MaxIOTimeNS   uint64 `json:"max_io_time_ns"`
}

// BlockMetrics can be sampled without acquiring any storage or device lock.
// Snapshots are approximate while a request is being updated.
type BlockMetrics struct {
	spec          string
	capacityBytes uint64
	readOnly      bool
	readOps       atomic.Uint64
	writeOps      atomic.Uint64
	flushOps      atomic.Uint64
	readBytes     atomic.Uint64
	writeBytes    atomic.Uint64
	errors        atomic.Uint64
	completedOps  atomic.Uint64
	inFlight      atomic.Int64
	ioTimeNS      atomic.Uint64
	maxIOTimeNS   atomic.Uint64
}

func NewBlockMetrics(spec string, capacityBytes uint64, readOnly bool) *BlockMetrics {
	return &BlockMetrics{spec: spec, capacityBytes: capacityBytes, readOnly: readOnly}
}

func (m *BlockMetrics) Snapshot() BlockStats {
	return BlockStats{
		Spec: m.spec, CapacityBytes: m.capacityBytes, ReadOnly: m.readOnly,
		ReadOps: m.readOps.Load(), WriteOps: m.writeOps.Load(), FlushOps: m.flushOps.Load(),
		ReadBytes: m.readBytes.Load(), WriteBytes: m.writeBytes.Load(), Errors: m.errors.Load(),
		CompletedOps: m.completedOps.Load(),
		InFlight: m.inFlight.Load(), IOTimeNS: m.ioTimeNS.Load(), MaxIOTimeNS: m.maxIOTimeNS.Load(),
	}
}

func (m *BlockMetrics) begin(op uint32) time.Time {
	if m == nil {
		return time.Time{}
	}
	switch op {
	case blkTIn:
		m.readOps.Add(1)
	case blkTOut:
		m.writeOps.Add(1)
	case blkTFlush:
		m.flushOps.Add(1)
	}
	m.inFlight.Add(1)
	return time.Now()
}

func (m *BlockMetrics) finish(start time.Time, readBytes, writeBytes uint64, failed bool) {
	if m == nil {
		return
	}
	m.readBytes.Add(readBytes)
	m.writeBytes.Add(writeBytes)
	if failed {
		m.errors.Add(1)
	}
	elapsed := uint64(time.Since(start))
	m.ioTimeNS.Add(elapsed)
	m.completedOps.Add(1)
	for old := m.maxIOTimeNS.Load(); elapsed > old; old = m.maxIOTimeNS.Load() {
		if m.maxIOTimeNS.CompareAndSwap(old, elapsed) {
			break
		}
	}
	m.inFlight.Add(-1)
}