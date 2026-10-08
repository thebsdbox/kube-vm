package kubevm

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"kube-vm/control"
	"kube-vm/vmm"
)

func TestVMServerShutdownRemovesStaleEntry(t *testing.T) {
	s := &vmServer{vms: map[string]*vmController{"abc123": nil}}

	if err := s.Shutdown("abc123"); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}

	if _, ok := s.vms["abc123"]; ok {
		t.Fatal("vm server still contains a shutdown VM entry")
	}
}

func TestServeConsoleStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	socketPath := filepath.Join(t.TempDir(), "console.sock")
	session := control.NewConsoleSession()

	errC := make(chan error, 1)
	go func() {
		errC <- control.ServeConsole(ctx, socketPath, session)
	}()

	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("dial console socket: %v", err)
	}
	session.SetConn(conn)

	cancel()

	select {
	case err := <-errC:
		if err != nil && err != context.Canceled {
			t.Fatalf("ServeConsole returned unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ServeConsole did not stop after context cancellation")
	}
}

func TestVMControllerShutdownDoesNotDeadlockOnConsoleClose(t *testing.T) {
	controller := &vmController{
		ctx:            context.Background(),
		consoleSession: control.NewConsoleSession(),
	}

	controller.consoleSession.SetOnClose(func() {
		_ = controller.Shutdown()
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := controller.Shutdown(); err != nil {
			t.Errorf("shutdown returned unexpected error: %v", err)
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("vm controller shutdown deadlocked on console close callback")
	}
}

func TestVMServerListIncludesNATGuestIP(t *testing.T) {
	s := &vmServer{vms: map[string]*vmController{
		"abc123": {uid: "abc123", vm: &vmm.VM{}, natGuestIP: "10.0.2.15"},
	}}

	list := s.List()
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}
	if list[0].IPAddress != "10.0.2.15" {
		t.Fatalf("IPAddress = %q, want %q", list[0].IPAddress, "10.0.2.15")
	}
}

func TestVMServerExamineReturnsSnapshotDetails(t *testing.T) {
	s := &vmServer{vms: map[string]*vmController{
		"abc123": {
			uid:        "abc123",
			vm:         &vmm.VM{},
			memMiB:     512,
			numCPU:     2,
			kernel:     "bzImage",
			nat:        true,
			natGuestIP: "10.0.2.15",
		},
	}}

	details, err := s.Examine("abc123")
	if err != nil {
		t.Fatalf("Examine returned error: %v", err)
	}
	if details.UID != "abc123" {
		t.Fatalf("UID = %q, want %q", details.UID, "abc123")
	}
	if details.NATGuestIP != "10.0.2.15" {
		t.Fatalf("NATGuestIP = %q, want %q", details.NATGuestIP, "10.0.2.15")
	}
	if details.NumCPU != 2 {
		t.Fatalf("NumCPU = %d, want 2", details.NumCPU)
	}
}

func TestWaitForConsoleSocketReturnsWhenSocketAppears(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	socketPath := filepath.Join(t.TempDir(), "console.sock")
	errC := make(chan error, 1)
	go func() {
		time.Sleep(20 * time.Millisecond)
		ln, err := net.Listen("unix", socketPath)
		if err != nil {
			errC <- err
			return
		}
		defer ln.Close()
		<-ctx.Done()
	}()

	if err := waitForConsoleSocket(ctx, socketPath, errC, time.Second); err != nil {
		t.Fatalf("waitForConsoleSocket returned error: %v", err)
	}
}

func TestWaitForConsoleSocketReturnsListenerError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	want := errors.New("listen failed")
	errC := make(chan error, 1)
	errC <- want

	err := waitForConsoleSocket(ctx, filepath.Join(t.TempDir(), "console.sock"), errC, time.Second)
	if !errors.Is(err, want) {
		t.Fatalf("waitForConsoleSocket error = %v, want %v", err, want)
	}
}

func TestWaitForConsoleSocketReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := waitForConsoleSocket(ctx, filepath.Join(t.TempDir(), "console.sock"), make(chan error), time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForConsoleSocket error = %v, want %v", err, context.Canceled)
	}
}
