package main

import (
	"bytes"
	"strings"
	"testing"

	"kube-vm/control"
)

func TestListDisplaysUptimeAndBaseDisk(t *testing.T) {
	oldDial := controlDial
	t.Cleanup(func() { controlDial = oldDial })
	controlDial = func(_ string, action string, _ map[string]any) (control.Response, error) {
		if action != "list" {
			t.Fatalf("action = %q, want list", action)
		}
		return control.Response{OK: true, VMs: []control.VMSummary{
			{UID: "ubuntu", State: "running", IPAddress: "10.0.2.15", UptimeSec: 90061, BaseDisk: "/var/lib/kube-vm/base-disks/ubuntu.img"},
			{UID: "diskless", State: "running"},
		}}, nil
	}
	var out bytes.Buffer
	cmd := newListCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected header and two rows, got %q", out.String())
	}
	for _, column := range []string{"UID", "STATE", "IP", "UPTIME", "BASE DISK"} {
		if !strings.Contains(lines[0], column) {
			t.Errorf("header missing %q: %q", column, lines[0])
		}
	}
	if got := strings.Join(strings.Fields(lines[1]), " "); got != "ubuntu running 10.0.2.15 25h1m1s /var/lib/kube-vm/base-disks/ubuntu.img" {
		t.Errorf("VM row = %q", got)
	}
	if got := strings.Join(strings.Fields(lines[2]), " "); got != "diskless running - 0s -" {
		t.Errorf("diskless row = %q", got)
	}
}

func TestListEmptyOutput(t *testing.T) {
	oldDial := controlDial
	t.Cleanup(func() { controlDial = oldDial })
	controlDial = func(_ string, _ string, _ map[string]any) (control.Response, error) {
		return control.Response{OK: true}, nil
	}
	var out bytes.Buffer
	cmd := newListCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if out.String() != "no running VMs\n" {
		t.Fatalf("output = %q", out.String())
	}
}