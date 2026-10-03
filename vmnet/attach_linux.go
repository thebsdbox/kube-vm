//go:build linux

package vmnet

import (
	"errors"

	"kube-vm/nat"
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
	if natMode && tapName != "" {
		return Config{}, errors.New("network: -nat and -tap are mutually exclusive")
	}
	if vhostNet && natMode {
		return Config{}, errors.New("network: -vhost-net cannot be used with -nat")
	}
	if natMode {
		backend, natCfg, err := nat.OpenBackend(uid)
		if err != nil {
			return Config{}, err
		}
		countedBackend, nicStats := wrapCountingBackend(backend, natCfg.TapName, "nat")
		cfg.Devices = append(cfg.Devices, &virtio.NetDevice{Backend: countedBackend})
		return Config{
			TapName:    natCfg.TapName,
			NAT:        true,
			NATSubnet:  natCfg.SubnetCIDR,
			NATGateway: natCfg.GatewayIP,
			NATGuestIP: natCfg.GuestIP,
			NICs:       []NICStatsSource{nicStats},
		}, nil
	}
	if tapName == "" {
		if vhostNet {
			return Config{}, errors.New("network: -vhost-net requires -tap")
		}
		return Config{}, nil
	}
	if vhostNet {
		return Config{}, errors.New("network: vhost-net is not implemented")
	}

	tap, name, err := virtio.OpenTAP(tapName)
	if err != nil {
		return Config{}, err
	}
	countedBackend, nicStats := wrapCountingBackend(tap, name, "tap")
	cfg.Devices = append(cfg.Devices, &virtio.NetDevice{Backend: countedBackend})
	return Config{TapName: name, NICs: []NICStatsSource{nicStats}}, nil
}
