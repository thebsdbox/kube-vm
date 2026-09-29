package arch

import "kube-vm/kvm"

var archCaps = []kvm.Cap{
	kvm.CapExtCPUID,
	kvm.CapTSCDeadlineTimer,
}
