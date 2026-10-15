package vmm

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"

	"kube-vm/kvm"
)

const (
	serialCOM1Base     = 0x3f8
	serialCOM1End      = serialCOM1Base + 7
	pciConfigAddrPort  = 0xcf8
	pciConfigDataPort  = 0xcfc
	serialLSRDataReady = 1 << 0
	serialLSRTHRE      = 1 << 5
	serialLSRTEMT      = 1 << 6
)

type serialDevice struct {
	mu  sync.Mutex
	in  io.Reader
	out io.Writer
	ier byte
	lcr byte
	mcr byte
	scr byte
	dll byte
	dlm byte
}

func newSerialDevice(in io.Reader, out io.Writer) *serialDevice {
	return &serialDevice{in: in, out: out}
}

func (d *serialDevice) Close() error {
	return nil
}

func (d *serialDevice) handles(port uint16) bool {
	return port >= serialCOM1Base && port <= serialCOM1End
}

func (d *serialDevice) read(port uint16, size uint8, data []byte) error {
	if size != 1 {
		return fmt.Errorf("unsupported serial read size %d", size)
	}
	for i := range data {
		value, err := d.readRegister(port)
		if err != nil {
			return err
		}
		data[i] = value
	}
	return nil
}

func (d *serialDevice) write(port uint16, size uint8, data []byte) error {
	if size != 1 {
		return fmt.Errorf("unsupported serial write size %d", size)
	}
	for _, value := range data {
		if err := d.writeRegister(port, value); err != nil {
			return err
		}
	}
	return nil
}

func (d *serialDevice) readRegister(port uint16) (byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	off := port - serialCOM1Base
	switch off {
	case 0:
		if d.lcr&0x80 != 0 {
			return d.dll, nil
		}
		return 0, nil
	case 1:
		if d.lcr&0x80 != 0 {
			return d.dlm, nil
		}
		return d.ier, nil
	case 2:
		return 0x01, nil
	case 3:
		return d.lcr, nil
	case 4:
		return d.mcr, nil
	case 5:
		return byte(serialLSRTHRE | serialLSRTEMT), nil
	case 6:
		return 0xb0, nil
	case 7:
		return d.scr, nil
	default:
		return 0, fmt.Errorf("unsupported serial read port %#x", port)
	}
}

func (d *serialDevice) writeRegister(port uint16, value byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	off := port - serialCOM1Base
	switch off {
	case 0:
		if d.lcr&0x80 != 0 {
			d.dll = value
			return nil
		}
		if d.out != nil {
			_, err := d.out.Write([]byte{value})
			return err
		}
		return nil
	case 1:
		if d.lcr&0x80 != 0 {
			d.dlm = value
			return nil
		}
		d.ier = value
		return nil
	case 3:
		d.lcr = value
		return nil
	case 4:
		d.mcr = value
		return nil
	case 7:
		d.scr = value
		return nil
	default:
		return nil
	}
}

func (m *VM) handleIO(xd *kvm.IOExitData, data []byte) error {
	if m.serial != nil && m.serial.handles(xd.Port) {
		if xd.IsOut {
			return m.serial.write(xd.Port, xd.Size, data)
		}
		return m.serial.read(xd.Port, xd.Size, data)
	}
	if m.handlePCIConfigIO(xd, data) {
		return nil
	}
	if xd.IsOut {
		return nil
	}
	for i := range data {
		data[i] = 0
	}
	return nil
}

func (m *VM) handlePCIConfigIO(xd *kvm.IOExitData, data []byte) bool {
	if xd.Port < pciConfigAddrPort || xd.Port > pciConfigDataPort+3 {
		return false
	}

	m.pciConfigMu.Lock()
	defer m.pciConfigMu.Unlock()

	if xd.IsOut {
		switch xd.Port {
		case pciConfigAddrPort:
			if len(data) >= 4 {
				m.pciConfigAddr = binary.LittleEndian.Uint32(data[:4])
			}
		}
		return true
	}

	for i := range data {
		data[i] = 0
	}
	switch xd.Port {
	case pciConfigAddrPort:
		if len(data) >= 4 {
			binary.LittleEndian.PutUint32(data[:4], m.pciConfigAddr)
		}
	default:
		for i := range data {
			data[i] = 0xff
		}
	}
	return true
}
