//go:build !linux

package vmnet

import (
	"errors"

	"kube-vm/virtio"
	"kube-vm/vmm"
)

type Config struct {
	TapName    string
	NAT        bool
	VhostNet   bool
	NATSubnet  string
	NATGateway string
	NATGuestIP string
	NICs       []NICStatsSource
}

func AttachDevice(cfg *vmm.Config, uid, tapName string, natMode bool, vhostNet bool) (Config, error) {
	if natMode {
		return Config{}, errors.New("nat networking is only supported on linux")
	}
	if vhostNet {
		return Config{}, errors.New("vhost-net is only supported on linux")
	}
	if tapName == "" {
		return Config{}, nil
	}
	tap, name, err := virtio.OpenTAP(tapName)
	if err != nil {
		return Config{}, err
	}
	countedBackend, nicStats := wrapCountingBackend(tap, name, "tap")
	cfg.Devices = append(cfg.Devices, &virtio.NetDevice{Backend: countedBackend})
	return Config{TapName: name, NICs: []NICStatsSource{nicStats}}, nil
}
