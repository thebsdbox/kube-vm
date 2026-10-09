package control

import (
	"context"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConsoleSessionReplaysHistoryOnReconnect(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	socketPath := filepath.Join(t.TempDir(), "console.sock")
	session := NewConsoleSession()
	errC := make(chan error, 1)
	go func() {
		errC <- ServeConsole(ctx, socketPath, session)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errC:
			if err != nil {
				t.Errorf("ServeConsole returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("ServeConsole did not stop")
		}
	})

	conn1 := mustDialConsole(t, socketPath)
	if _, err := session.Write([]byte("before reconnect\n")); err != nil {
		t.Fatalf("session.Write before reconnect: %v", err)
	}
	readConsoleExact(t, conn1, "before reconnect\n")
	if err := conn1.Close(); err != nil {
		t.Fatalf("close first console connection: %v", err)
	}

	if _, err := session.Write([]byte("while disconnected\n")); err != nil {
		t.Fatalf("session.Write while disconnected: %v", err)
	}

	conn2 := mustDialConsole(t, socketPath)
	defer conn2.Close()
	readConsoleExact(t, conn2, "before reconnect\nwhile disconnected\n")
}

func TestConsoleSessionReplaysLast500Lines(t *testing.T) {
	t.Parallel()

	session := NewConsoleSession()
	var all strings.Builder
	var want strings.Builder
	for i := 0; i < 600; i++ {
		line := fmt.Sprintf("line %03d\n", i)
		all.WriteString(line)
		if i >= 100 {
			want.WriteString(line)
		}
	}
	if _, err := session.Write([]byte(all.String())); err != nil {
		t.Fatalf("session.Write: %v", err)
	}

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		session.SetConn(serverConn)
	}()

	readConsoleExact(t, clientConn, want.String())
	if err := clientConn.Close(); err != nil {
		t.Fatalf("close replay client: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SetConn did not finish replay")
	}
}

func mustDialConsole(t *testing.T, socketPath string) net.Conn {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.DialTimeout("unix", socketPath, 100*time.Millisecond)
		if err == nil {
			return conn
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial console socket %s: %v", socketPath, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func readConsoleExact(t *testing.T, conn net.Conn, want string) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	buf := make([]byte, len(want))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read console output: %v", err)
	}
	if got := string(buf); got != want {
		t.Fatalf("console output = %q, want %q", got, want)
	}
}
