package main

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"kube-vm/control"
)

func TestResolveVMUID(t *testing.T) {
	oldDial := controlDial
	t.Cleanup(func() { controlDial = oldDial })
	for _, tt := range []struct {
		name, prefix, want, wantError string
		uids                          []string
	}{
		{name: "one character", prefix: "f", uids: []string{"fa123", "z987"}, want: "fa123"},
		{name: "two characters", prefix: "fa", uids: []string{"fb123", "fa123"}, want: "fa123"},
		{name: "exact wins", prefix: "f", uids: []string{"fb123", "fa123", "f"}, want: "f"},
		{name: "ambiguous", prefix: "f", uids: []string{"fb123", "fa123"}, wantError: "matches fa123, fb123; use more characters"},
		{name: "not a substring", prefix: "a1", uids: []string{"fa123"}, wantError: "no VM matches"},
		{name: "case sensitive", prefix: "F", uids: []string{"fa123"}, wantError: "no VM matches"},
		{name: "empty list", prefix: "f", wantError: "no VM matches"},
		{name: "empty prefix", prefix: "", uids: []string{"fa123"}, wantError: "uid is required"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			controlDial = func(socket, action string, _ map[string]any) (control.Response, error) {
				if socket != "control.sock" || action != "list" {
					t.Fatalf("unexpected request: %s %s", socket, action)
				}
				resp := control.Response{OK: true}
				for _, id := range tt.uids {
					resp.VMs = append(resp.VMs, control.VMSummary{UID: id})
				}
				return resp, nil
			}
			got, err := resolveVMUID("control.sock", tt.prefix)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("error = %v, want %q", err, tt.wantError)
				}
			} else if err != nil || got != tt.want {
				t.Fatalf("resolved UID = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestVMPrefixCommands(t *testing.T) {
	oldDial := controlDial
	t.Cleanup(func() { controlDial = oldDial })
	for _, name := range []string{"stop", "pause", "resume", "status", "examine", "save", "restore"} {
		for _, flag := range []string{"", "--uid", "-u"} {
			t.Run(name+"/"+flag, func(t *testing.T) {
				called := false
				controlDial = func(_ string, action string, payload map[string]any) (control.Response, error) {
					if action == "list" {
						return control.Response{OK: true, VMs: []control.VMSummary{{UID: "fb123"}, {UID: "fa123"}}}, nil
					}
					wantAction := name
					if name == "stop" {
						wantAction = "shutdown"
					}
					if action != wantAction || payload["uid"] != "fa123" {
						t.Fatalf("request %s %+v; want %s targeting fa123", action, payload, wantAction)
					}
					if (name == "save" || name == "restore") && payload["path"] != "checkpoint.snap" {
						t.Fatalf("lost snapshot path: %+v", payload)
					}
					called = true
					return control.Response{OK: true, VM: &control.VMDetails{UID: "fa123"}}, nil
				}
				args := []string{name}
				if flag != "" {
					args = append(args, flag)
				}
				args = append(args, "fa")
				if name == "save" || name == "restore" {
					args = append(args, "--path", "checkpoint.snap")
				}
				cmd := newRootCmd()
				cmd.SetOut(io.Discard)
				cmd.SetErr(io.Discard)
				cmd.SetArgs(args)
				if err := cmd.Execute(); err != nil || !called {
					t.Fatalf("execute: %v, action called: %t", err, called)
				}
			})
		}
	}
}

func TestVMPrefixAmbiguityNeverActs(t *testing.T) {
	oldDial := controlDial
	t.Cleanup(func() { controlDial = oldDial })
	controlDial = func(_ string, action string, _ map[string]any) (control.Response, error) {
		if action != "list" {
			t.Fatalf("unsafe request after ambiguous prefix: %s", action)
		}
		return control.Response{OK: true, VMs: []control.VMSummary{{UID: "fb123"}, {UID: "fa123"}}}, nil
	}
	for _, name := range []string{"stop", "pause", "resume", "status", "examine", "save", "restore", "console", "ssh"} {
		t.Run(name, func(t *testing.T) {
			args := []string{name, "f"}
			if name == "save" || name == "restore" {
				args = append(args, "--path", "checkpoint.snap")
			}
			cmd := newRootCmd()
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(args)
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "ambiguous") {
				t.Fatalf("expected ambiguity rejection, got %v", err)
			}
		})
	}
}

func TestVMPrefixListFailuresDoNotAct(t *testing.T) {
	oldDial := controlDial
	t.Cleanup(func() { controlDial = oldDial })
	for _, tt := range []struct {
		resp control.Response
		err  error
	}{
		{err: errors.New("socket unavailable")},
		{resp: control.Response{Error: "permission denied"}},
		{resp: control.Response{}},
	} {
		controlDial = func(_ string, action string, _ map[string]any) (control.Response, error) {
			if action != "list" {
				t.Fatalf("unexpected action after list failure: %s", action)
			}
			return tt.resp, tt.err
		}
		if _, err := dialVM("control.sock", "shutdown", map[string]any{"uid": "f"}); err == nil {
			t.Fatal("expected discovery failure")
		}
	}
}

func TestVMPrefixPreservesPayload(t *testing.T) {
	oldDial := controlDial
	t.Cleanup(func() { controlDial = oldDial })
	controlDial = func(_ string, action string, payload map[string]any) (control.Response, error) {
		if action == "list" {
			return control.Response{OK: true, VMs: []control.VMSummary{{UID: "fa123"}}}, nil
		}
		if payload["uid"] != "fa123" || payload["path"] != "checkpoint.snap" {
			t.Fatalf("request = %+v", payload)
		}
		return control.Response{OK: true}, nil
	}
	payload := map[string]any{"uid": "f", "path": "checkpoint.snap"}
	resp, err := dialVM("control.sock", "save", payload)
	if err != nil || resp.UID != "fa123" || payload["uid"] != "f" {
		t.Fatalf("result %+v, %v; caller payload %+v", resp, err, payload)
	}
}

func TestConsolePrefixUsesResolvedSocket(t *testing.T) {
	oldDial := controlDial
	t.Cleanup(func() { controlDial = oldDial })
	controlDial = func(_ string, action string, payload map[string]any) (control.Response, error) {
		if action == "list" {
			return control.Response{OK: true, VMs: []control.VMSummary{{UID: "fa123"}}}, nil
		}
		if action != "connect" || payload["uid"] != "fa123" {
			t.Fatalf("console request: %s %+v", action, payload)
		}
		return control.Response{OK: true, UID: "fa123"}, nil
	}
	socket := filepath.Join(t.TempDir(), "control.sock")
	err := attachConsole(socket, "f")
	if err == nil || !strings.Contains(err.Error(), socket+".fa123.console") {
		t.Fatalf("expected missing full-UID console socket, got %v", err)
	}
}

func TestSSHPrefixResolvesGuestIP(t *testing.T) {
	oldDial := controlDial
	t.Cleanup(func() { controlDial = oldDial })
	controlDial = func(_ string, action string, payload map[string]any) (control.Response, error) {
		if action == "list" {
			return control.Response{OK: true, VMs: []control.VMSummary{{UID: "fa123"}}}, nil
		}
		if action != "examine" || payload["uid"] != "fa123" {
			t.Fatalf("SSH request: %s %+v", action, payload)
		}
		return control.Response{OK: true, VM: &control.VMDetails{NATGuestIP: "10.0.2.15"}}, nil
	}
	host, err := resolveSSHTarget("control.sock", "f", "root")
	if err != nil || host != "10.0.2.15" {
		t.Fatalf("SSH host = %q, %v", host, err)
	}
}

func TestStartUIDIsLiteral(t *testing.T) {
	oldDial := controlDial
	t.Cleanup(func() { controlDial = oldDial })
	called := false
	controlDial = func(_ string, action string, payload map[string]any) (control.Response, error) {
		if action != "start" || payload["uid"] != "f" {
			t.Fatalf("start must not resolve UID: %s %+v", action, payload)
		}
		called = true
		return control.Response{OK: true, UID: "f"}, nil
	}
	cmd := newRootCmd()
	cmd.SetArgs([]string{"start", "--uid", "f", "--detach"})
	if err := cmd.Execute(); err != nil || !called {
		t.Fatalf("start: %v, called %t", err, called)
	}
}

func TestStatusWithoutUIDDoesNotResolve(t *testing.T) {
	oldDial := controlDial
	t.Cleanup(func() { controlDial = oldDial })
	controlDial = func(_ string, action string, payload map[string]any) (control.Response, error) {
		if action != "status" || payload["uid"] != "" {
			t.Fatalf("daemon status must not resolve UID: %s %+v", action, payload)
		}
		return control.Response{OK: true}, nil
	}
	cmd := newRootCmd()
	cmd.SetArgs([]string{"status"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
}