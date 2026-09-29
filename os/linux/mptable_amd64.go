//go:build linux && amd64

package linux

import (
	"encoding/binary"
	"fmt"
)

const (
	mpConfigAddr = 0x000f1000
	mpFloatAddr  = 0x000f0000

	defaultLAPICAddr  = 0xfee00000
	defaultIOAPICAddr = 0xfec00000
)

func installMPTable(mem []byte, numCPU int) error {
	if numCPU < 1 {
		return fmt.Errorf("invalid cpu count for mptable: %d", numCPU)
	}

	cfg, err := buildMPConfigTable(numCPU)
	if err != nil {
		return err
	}

	fps := buildMPFloatingPointer(mpConfigAddr)

	if len(mem) < mpFloatAddr+len(fps) || len(mem) < mpConfigAddr+len(cfg) {
		return fmt.Errorf("guest memory too small for mptable")
	}

	copy(mem[mpConfigAddr:], cfg)
	copy(mem[mpFloatAddr:], fps)

	return nil
}

func buildMPConfigTable(numCPU int) ([]byte, error) {
	const (
		mpSpecRev         = 4
		entryProcessorLen = 20
		entryBusLen       = 8
		entryIOAPICLen    = 8
		entryIOIntLen     = 8
		entryLocalIntLen  = 8
		headerLen         = 44

		cpuFlagEnabled = 1 << 0
		cpuFlagBSP     = 1 << 1

		intTypeINT    = 0
		intTypeNMI    = 1
		intTypeExtINT = 3
	)

	isaIRQs := 16
	entryCount := numCPU + 1 + 1 + isaIRQs + 2
	tableLen := headerLen + numCPU*entryProcessorLen + entryBusLen + entryIOAPICLen + isaIRQs*entryIOIntLen + 2*entryLocalIntLen
	b := make([]byte, tableLen)
	off := 0

	copy(b[off:], []byte("PCMP"))
	off += 4
	binary.LittleEndian.PutUint16(b[off:], uint16(tableLen))
	off += 2
	b[off] = mpSpecRev
	off++
	b[off] = 0 // checksum (patched later)
	off++
	copy(b[off:], []byte("HYPEVM  "))
	off += 8
	copy(b[off:], []byte("HYPE-SMP    "))
	off += 12
	binary.LittleEndian.PutUint32(b[off:], 0)
	off += 4
	binary.LittleEndian.PutUint16(b[off:], 0)
	off += 2
	binary.LittleEndian.PutUint16(b[off:], uint16(entryCount))
	off += 2
	binary.LittleEndian.PutUint32(b[off:], defaultLAPICAddr)
	off += 4
	binary.LittleEndian.PutUint16(b[off:], 0)
	off += 2
	b[off] = 0
	off++
	b[off] = 0
	off++

	for i := 0; i < numCPU; i++ {
		b[off+0] = 0        // processor entry
		b[off+1] = uint8(i) // local apic id
		b[off+2] = 0x14     // local apic version
		b[off+3] = cpuFlagEnabled
		if i == 0 {
			b[off+3] |= cpuFlagBSP
		}
		binary.LittleEndian.PutUint32(b[off+4:], 0)
		binary.LittleEndian.PutUint32(b[off+8:], 0)
		binary.LittleEndian.PutUint32(b[off+12:], 0)
		binary.LittleEndian.PutUint32(b[off+16:], 0)
		off += entryProcessorLen
	}

	// Bus entry: ISA
	b[off+0] = 1
	b[off+1] = 0
	copy(b[off+2:], []byte("ISA   "))
	off += entryBusLen

	// IOAPIC entry
	ioapicID := uint8(numCPU)
	b[off+0] = 2
	b[off+1] = ioapicID
	b[off+2] = 0x11
	b[off+3] = 1
	binary.LittleEndian.PutUint32(b[off+4:], defaultIOAPICAddr)
	off += entryIOAPICLen

	// I/O interrupt source entries: route ISA IRQs 0-15 to IOAPIC INTIN 0-15.
	for irq := 0; irq < isaIRQs; irq++ {
		b[off+0] = 3 // I/O interrupt assignment
		b[off+1] = intTypeINT
		binary.LittleEndian.PutUint16(b[off+2:], 0) // conforms
		b[off+4] = 0                                // ISA bus id
		b[off+5] = uint8(irq)                       // IRQ on ISA bus
		b[off+6] = ioapicID
		b[off+7] = uint8(irq) // IOAPIC INTIN
		off += entryIOIntLen
	}

	// Local interrupt assignment: ExtINT on LINT0 for all local APICs.
	b[off+0] = 4
	b[off+1] = intTypeExtINT
	binary.LittleEndian.PutUint16(b[off+2:], 0)
	b[off+4] = 0
	b[off+5] = 0
	b[off+6] = 0xff
	b[off+7] = 0
	off += entryLocalIntLen

	// Local interrupt assignment: NMI on LINT1 for all local APICs.
	b[off+0] = 4
	b[off+1] = intTypeNMI
	binary.LittleEndian.PutUint16(b[off+2:], 0)
	b[off+4] = 0
	b[off+5] = 0
	b[off+6] = 0xff
	b[off+7] = 1

	b[7] = checksum8(b)
	return b, nil
}

func buildMPFloatingPointer(configAddr uint32) []byte {
	const mpSpecRev = 4
	b := make([]byte, 16)

	copy(b[0:], []byte("_MP_"))
	binary.LittleEndian.PutUint32(b[4:], configAddr)
	b[8] = 1
	b[9] = mpSpecRev
	b[10] = 0
	b[11] = 0
	b[12] = 0
	b[13] = 0
	b[14] = 0
	b[15] = 0

	b[10] = checksum8(b)
	return b
}

func checksum8(data []byte) byte {
	var sum uint8
	for _, b := range data {
		sum += b
	}

	return byte(0 - sum)
}
