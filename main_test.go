package main

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"kube-vm/control"
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
