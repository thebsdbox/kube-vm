package webui

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"
)

// Service provides the VM operations used by the web UI.
type Service struct {
	Start               func(uid string, payload map[string]any) (string, error)
	Stop                func(uid string) error
	CheckVM             func(uid string) error
	ListVMs             func() any
	DaemonStats         func() any
	DefaultStartPayload func() map[string]any
	ConsoleSocket       func(uid string) (string, error)
}

// Serve runs the web frontend and API on addr.
// If token is non-empty, API and console endpoints require bearer auth.
func Serve(ctx context.Context, addr string, svc Service, token string) error {
	mgr := newConsoleManager()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, frontendHTML)
	})

	api := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rv := recover(); rv != nil {
					fmt.Fprintf(os.Stderr, "kube-vm: webui panic: %v\n%s", rv, debug.Stack())
					writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "internal server error"})
				}
			}()
			if token != "" && !authorized(r, token) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "unauthorized"})
				return
			}
			h(w, r)
		}
	}

	mux.HandleFunc("/api/v1/vms", api(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"daemon": svc.DaemonStats(),
			"vms":    svc.ListVMs(),
		})
	}))

	mux.HandleFunc("/api/v1/vms/start", api(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
			return
		}
		uid, payload, err := parseStartRequest(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if len(payload) == 0 && svc.DefaultStartPayload != nil {
			payload = svc.DefaultStartPayload()
		}
		started, err := svc.Start(uid, payload)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "uid": started})
	}))

	mux.HandleFunc("/api/v1/vms/stop", api(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
			return
		}
		uid := strings.TrimSpace(r.URL.Query().Get("uid"))
		if uid == "" {
			_ = r.ParseForm()
			uid = strings.TrimSpace(r.FormValue("uid"))
		}
		if uid == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "uid is required"})
			return
		}
		if err := svc.Stop(uid); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "uid": uid})
	}))

	mux.HandleFunc("/api/v1/stats", api(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "daemon": svc.DaemonStats()})
	}))

	mux.HandleFunc("/api/v1/console/open", api(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
			return
		}
		uid := strings.TrimSpace(r.URL.Query().Get("uid"))
		if uid == "" {
			_ = r.ParseForm()
			uid = strings.TrimSpace(r.FormValue("uid"))
		}
		if uid == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "uid is required"})
			return
		}
		if svc.CheckVM != nil {
			if err := svc.CheckVM(uid); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
				return
			}
		}
		sock, err := svc.ConsoleSocket(uid)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		conn, err := net.DialTimeout("unix", sock, 2*time.Second)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		sid := randID()
		mgr.add(sid, uid, conn)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "session_id": sid, "uid": uid})
	}))

	mux.HandleFunc("/api/v1/console/poll", api(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
			return
		}
		sid := strings.TrimSpace(r.URL.Query().Get("id"))
		if sid == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "id is required"})
			return
		}
		s := mgr.get(sid)
		if s == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "console session not found"})
			return
		}
		chunk, closed := s.poll()
		if closed {
			mgr.remove(sid)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":       true,
			"closed":   closed,
			"data_b64": base64.StdEncoding.EncodeToString(chunk),
		})
	}))

	mux.HandleFunc("/api/v1/console/input", api(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
			return
		}
		sid := strings.TrimSpace(r.URL.Query().Get("id"))
		if sid == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "id is required"})
			return
		}
		s := mgr.get(sid)
		if s == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "console session not found"})
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 8192))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if err := s.write(body); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))

	mux.HandleFunc("/api/v1/console/close", api(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
			return
		}
		sid := strings.TrimSpace(r.URL.Query().Get("id"))
		if sid == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "id is required"})
			return
		}
		mgr.remove(sid)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))

	httpServer := &http.Server{Addr: addr, Handler: mux}
	go func() {
		<-ctx.Done()
		mgr.closeAll()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	err := httpServer.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
