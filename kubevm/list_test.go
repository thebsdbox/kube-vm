package kubevm

import (
	"encoding/json"
	"testing"
	"time"

	"kube-vm/control"
)

func TestBaseDiskFromBlockSpecs(t *testing.T) {
	for _, tt := range []struct {
		name   string
		blocks []string
		want   string
	}{
		{"diskless", nil, ""},
		{"raw", []string{"/disks/base.img"}, "/disks/base.img"},
		{"cow", []string{"cow:/disks/base.img?overlay=%2Fvms%2Fvm.overlay"}, "/disks/base.img"},
		{"relative cow", []string{"cow:base.img?overlay=vm.overlay"}, "base.img"},
		{"file URL", []string{"file:///disks/base%20image.img"}, "/disks/base image.img"},
		{"legacy overlay", []string{"file:/disks/base.img?overlay=vm.overlay"}, "/disks/base.img"},
		{"prefer base", []string{"/disks/data.img", "cow:/disks/base.img?overlay=vm.overlay"}, "/disks/base.img"},
		{"memory only", []string{"mem:4096"}, ""},
		{"skip memory", []string{"mem:4096", "/disks/base.img"}, "/disks/base.img"},
		{"remote", []string{"https://example.org/base.img"}, "https://example.org/base.img"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := baseDiskFromBlockSpecs(tt.blocks); got != tt.want {
				t.Fatalf("base disk = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestListIncludesUptimeAndBaseDisk(t *testing.T) {
	s := &vmServer{vms: map[string]*vmController{
		"vm1": {
			uid: "vm1", startedAt: time.Now().Add(-3661 * time.Second),
			natGuestIP: "10.0.2.15", block: []string{"cow:/disks/ubuntu.img?overlay=vm1.overlay"},
		},
	}}
	rows := s.List()
	if len(rows) != 1 {
		t.Fatalf("list = %+v", rows)
	}
	if rows[0].UID != "vm1" || rows[0].IPAddress != "10.0.2.15" || rows[0].BaseDisk != "/disks/ubuntu.img" || rows[0].UptimeSec < 3661 || rows[0].UptimeSec > 3662 {
		t.Fatalf("summary = %+v", rows[0])
	}
	encoded, err := json.Marshal(control.Response{OK: true, VMs: rows})
	if err != nil {
		t.Fatal(err)
	}
	var decoded control.Response
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.VMs[0] != rows[0] {
		t.Fatalf("metadata changed over control API: %+v", decoded.VMs)
	}
}