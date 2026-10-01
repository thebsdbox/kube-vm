package vmnet

import (
	"io"
	"sync/atomic"
)

// NICStats describes host-side packet counters for a VM network interface.
type NICStats struct {
	Name      string `json:"name"`
	Mode      string `json:"mode"`
	RXPackets uint64 `json:"rx_packets"`
	RXBytes   uint64 `json:"rx_bytes"`
	TXPackets uint64 `json:"tx_packets"`
	TXBytes   uint64 `json:"tx_bytes"`
}

// NICStatsSource produces point-in-time NIC counter snapshots.
type NICStatsSource interface {
	Snapshot() NICStats
}

type countingBackend struct {
	io.ReadWriteCloser
	name      string
	mode      string
	rxPackets atomic.Uint64
	rxBytes   atomic.Uint64
	txPackets atomic.Uint64
	txBytes   atomic.Uint64
}

func wrapCountingBackend(backend io.ReadWriteCloser, name, mode string) (io.ReadWriteCloser, NICStatsSource) {
	cb := &countingBackend{ReadWriteCloser: backend, name: name, mode: mode}
	return cb, cb
}

func (b *countingBackend) Read(p []byte) (int, error) {
	n, err := b.ReadWriteCloser.Read(p)
	if n > 0 {
		b.rxPackets.Add(1)
		b.rxBytes.Add(uint64(n))
	}
	return n, err
}

func (b *countingBackend) Write(p []byte) (int, error) {
	n, err := b.ReadWriteCloser.Write(p)
	if n > 0 {
		b.txPackets.Add(1)
		b.txBytes.Add(uint64(n))
	}
	return n, err
}

func (b *countingBackend) Snapshot() NICStats {
	return NICStats{
		Name:      b.name,
		Mode:      b.mode,
		RXPackets: b.rxPackets.Load(),
		RXBytes:   b.rxBytes.Load(),
		TXPackets: b.txPackets.Load(),
		TXBytes:   b.txBytes.Load(),
	}
}
