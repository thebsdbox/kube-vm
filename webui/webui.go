package webui

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"sync"
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

func parseStartRequest(r *http.Request) (string, map[string]any, error) {
	payload := map[string]any{}
	uid := ""

	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		var raw map[string]any
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			return "", nil, fmt.Errorf("invalid JSON payload: %w", err)
		}
		if v, ok := raw["uid"].(string); ok {
			uid = strings.TrimSpace(v)
		}
		for k, v := range raw {
			if k == "uid" {
				continue
			}
			payload[k] = v
		}
		return uid, payload, nil
	}

	if err := r.ParseForm(); err != nil {
		return "", nil, fmt.Errorf("invalid form payload: %w", err)
	}
	uid = strings.TrimSpace(r.FormValue("uid"))
	copyFormValue(payload, r, "kernel")
	copyFormValue(payload, r, "initrd")
	copyFormValue(payload, r, "firmware")
	copyFormValue(payload, r, "iso")
	copyFormValue(payload, r, "cmdline")
	copyFormValue(payload, r, "tap")
	copyFormValue(payload, r, "mem")
	copyFormValue(payload, r, "cpu")

	blocksRaw := strings.TrimSpace(r.FormValue("block"))
	if blocksRaw != "" {
		parts := strings.FieldsFunc(blocksRaw, func(r rune) bool {
			return r == '\n' || r == ','
		})
		blocks := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				blocks = append(blocks, p)
			}
		}
		if len(blocks) > 0 {
			payload["block"] = blocks
		}
	}
	return uid, payload, nil
}

func copyFormValue(payload map[string]any, r *http.Request, key string) {
	if val := strings.TrimSpace(r.FormValue(key)); val != "" {
		payload[key] = val
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func authorized(r *http.Request, token string) bool {
	if token == "" {
		return true
	}
	if strings.TrimSpace(r.Header.Get("X-Auth-Token")) == token {
		return true
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		if strings.TrimSpace(auth[7:]) == token {
			return true
		}
	}
	if strings.TrimSpace(r.URL.Query().Get("token")) == token {
		return true
	}
	return false
}

func randID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

type consoleSession struct {
	uid    string
	conn   net.Conn
	mu     sync.Mutex
	buf    []byte
	closed bool
}

func (s *consoleSession) append(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.buf = append(s.buf, data...)
	if len(s.buf) > 1<<20 {
		s.buf = s.buf[len(s.buf)-(1<<20):]
	}
}

func (s *consoleSession) poll() ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]byte, len(s.buf))
	copy(out, s.buf)
	s.buf = s.buf[:0]
	return out, s.closed
}

func (s *consoleSession) write(p []byte) error {
	s.mu.Lock()
	closed := s.closed
	conn := s.conn
	s.mu.Unlock()
	if closed || conn == nil {
		return errors.New("console is closed")
	}
	_, err := conn.Write(p)
	return err
}

func (s *consoleSession) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	conn := s.conn
	s.conn = nil
	s.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

type consoleManager struct {
	mu       sync.Mutex
	sessions map[string]*consoleSession
}

func newConsoleManager() *consoleManager {
	return &consoleManager{sessions: map[string]*consoleSession{}}
}

func (m *consoleManager) add(id, uid string, conn net.Conn) {
	s := &consoleSession{uid: uid, conn: conn}
	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				s.append(buf[:n])
			}
			if err != nil {
				s.close()
				return
			}
		}
	}()
}

func (m *consoleManager) get(id string) *consoleSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}

func (m *consoleManager) remove(id string) {
	m.mu.Lock()
	s := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if s != nil {
		s.close()
	}
}

func (m *consoleManager) closeAll() {
	m.mu.Lock()
	all := make([]*consoleSession, 0, len(m.sessions))
	for id, s := range m.sessions {
		delete(m.sessions, id)
		all = append(all, s)
	}
	m.mu.Unlock()
	for _, s := range all {
		s.close()
	}
}

const frontendHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>kube-vm control</title>
  <link rel="preconnect" href="https://fonts.googleapis.com">
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
  <link href="https://fonts.googleapis.com/css2?family=Sora:wght@400;600;700&family=IBM+Plex+Mono:wght@400;600&display=swap" rel="stylesheet">
  <style>
    :root {
      --bg: #f6efe6;
      --panel: #fffaf3;
      --ink: #1f2b22;
      --muted: #5d6d61;
      --accent: #0f7b6c;
      --accent-2: #d36a2e;
      --ok: #176d39;
      --stop: #9a2f2f;
      --line: #d9c9b3;
      --shadow: 0 14px 28px rgba(39, 35, 27, 0.14);
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      min-height: 100vh;
      font-family: "Sora", sans-serif;
      color: var(--ink);
      background: radial-gradient(circle at 20% 10%, rgba(211,106,46,0.18) 0%, rgba(246,239,230,0) 35%), radial-gradient(circle at 80% 0%, rgba(15,123,108,0.18) 0%, rgba(246,239,230,0) 40%), linear-gradient(180deg, #f8f1e8 0%, #f3ece3 100%);
      padding: 20px;
      animation: reveal 280ms ease-out;
    }
    @keyframes reveal { from { opacity: 0; transform: translateY(6px); } to { opacity: 1; transform: translateY(0); } }
    .wrap { max-width: 1160px; margin: 0 auto; display: grid; gap: 16px; }
    .title { margin: 0; font-size: clamp(1.4rem, 2.8vw, 2.2rem); letter-spacing: 0.01em; }
    .subtitle { margin: 6px 0 0; color: var(--muted); font-size: 0.95rem; }
    .cards { display: grid; gap: 12px; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); }
    .card, .panel { background: var(--panel); border: 1px solid var(--line); border-radius: 14px; box-shadow: var(--shadow); }
    .card { padding: 12px 14px; }
    .k { color: var(--muted); font-size: 0.8rem; margin-bottom: 4px; }
    .v { font-size: 1.05rem; font-weight: 700; font-family: "IBM Plex Mono", monospace; }
    .panel { overflow: hidden; }
    .panel h2 { margin: 0; padding: 12px 14px; border-bottom: 1px solid var(--line); font-size: 1rem; }
    .content { padding: 14px; }
    .grid { display: grid; gap: 10px; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); }
    label { display: block; font-size: 0.82rem; color: var(--muted); margin-bottom: 4px; }
    input, textarea { width: 100%; font: inherit; border: 1px solid var(--line); border-radius: 10px; padding: 9px 10px; background: #fff; }
    textarea { min-height: 72px; resize: vertical; }
    .actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 10px; }
    button { border: 0; border-radius: 10px; padding: 9px 13px; font: 600 0.92rem "Sora", sans-serif; cursor: pointer; transition: transform 120ms ease, opacity 120ms ease; }
    button:hover { transform: translateY(-1px); }
    .primary { background: var(--accent); color: #fff; }
    .warn { background: var(--accent-2); color: #fff; }
    .ghost { background: #ece2d2; color: #263428; }
    .status { min-height: 1.2em; color: var(--muted); font-size: 0.9rem; margin-top: 8px; }
    table { width: 100%; border-collapse: collapse; font-size: 0.9rem; }
    th, td { text-align: left; padding: 10px; border-bottom: 1px solid var(--line); vertical-align: top; }
    th { color: var(--muted); font-weight: 600; }
    .mono { font-family: "IBM Plex Mono", monospace; }
    .state { display: inline-block; padding: 2px 8px; border-radius: 999px; font-size: 0.78rem; font-weight: 700; letter-spacing: 0.02em; text-transform: uppercase; }
    .running { background: #ddf1e2; color: var(--ok); }
    .stopped { background: #f8e1e1; color: var(--stop); }
    .console { width: 100%; min-height: 220px; background: #101410; color: #ccf6cc; border-radius: 10px; padding: 10px; overflow: auto; white-space: pre-wrap; font: 400 0.83rem "IBM Plex Mono", monospace; }
    .two { display: grid; gap: 14px; grid-template-columns: 2fr 1fr; }
    @media (max-width: 920px) { .two { grid-template-columns: 1fr; } }
    @media (max-width: 760px) { body { padding: 14px; } th:nth-child(7), td:nth-child(7), th:nth-child(8), td:nth-child(8) { display: none; } }
  </style>
</head>
<body>
  <main class="wrap">
    <header>
      <h1 class="title">kube-vm web control</h1>
      <p class="subtitle">Start, stop, inspect status/statistics, and attach a live web console.</p>
    </header>

    <section class="panel"><h2>API auth token</h2><div class="content"><label>Bearer token (optional)</label><input id="token" placeholder="matches -web-token" /><div class="status">Stored in your browser local storage for this page origin.</div></div></section>

    <section class="cards" id="cards"></section>

    <section class="panel">
      <h2>Start VM</h2>
      <div class="content">
        <form id="start-form">
          <div class="grid">
            <div><label>UID (optional)</label><input name="uid" /></div>
            <div><label>Memory MiB</label><input name="mem" value="1024" /></div>
            <div><label>VCPUs</label><input name="cpu" value="1" /></div>
            <div><label>Kernel</label><input name="kernel" value="bzImage" /></div>
            <div><label>Initrd</label><input name="initrd" /></div>
            <div><label>Firmware</label><input name="firmware" /></div>
            <div><label>ISO</label><input name="iso" /></div>
            <div><label>TAP</label><input name="tap" /></div>
          </div>
          <div style="margin-top:10px"><label>Cmdline</label><input name="cmdline" value="console=hvc0 reboot=t" /></div>
          <div style="margin-top:10px"><label>Block devices (comma or newline separated)</label><textarea name="block"></textarea></div>
          <div class="actions"><button class="primary" type="submit">Start VM</button><button class="ghost" type="button" id="refresh">Refresh</button></div>
          <div class="status" id="status"></div>
        </form>
      </div>
    </section>

    <section class="panel">
      <h2>Virtual Machines</h2>
      <div class="content">
        <table>
          <thead><tr><th>UID</th><th>State</th><th>Uptime</th><th>Mem</th><th>CPU</th><th>Host CPU</th><th>Host Mem</th><th>Boot</th><th>Action</th></tr></thead>
          <tbody id="vm-rows"></tbody>
        </table>
      </div>
    </section>

    <section class="panel">
      <h2>Console</h2>
      <div class="content two">
        <div><div class="console" id="console-output"></div></div>
        <div>
          <label>VM UID</label><input id="console-uid" />
          <div class="actions"><button class="primary" id="console-open" type="button">Open Console</button><button class="ghost" id="console-close" type="button">Close</button></div>
          <label style="margin-top:8px">Input</label><textarea id="console-input" placeholder="type text and click Send"></textarea>
          <div class="actions"><button class="warn" id="console-send" type="button">Send</button></div>
          <div class="status" id="console-status"></div>
        </div>
      </div>
    </section>
  </main>

  <script>
    const cards = document.getElementById('cards');
    const rows = document.getElementById('vm-rows');
    const statusEl = document.getElementById('status');
    const tokenEl = document.getElementById('token');
    const consoleOut = document.getElementById('console-output');
    const consoleUID = document.getElementById('console-uid');
    const consoleInput = document.getElementById('console-input');
    const consoleStatus = document.getElementById('console-status');

    let consoleSessionID = '';
    let consolePollTimer = null;

    tokenEl.value = localStorage.getItem('kubevm_web_token') || '';
    tokenEl.addEventListener('change', () => localStorage.setItem('kubevm_web_token', tokenEl.value.trim()));

    function token() { return tokenEl.value.trim(); }

    async function api(path, opts) {
      opts = opts || {};
      opts.headers = opts.headers || {};
      if (token() !== '') {
        opts.headers['Authorization'] = 'Bearer ' + token();
      }
      const res = await fetch(path, opts);
      const body = await res.json();
      if (!res.ok || !body.ok) {
        throw new Error((body && body.error) || ('request failed: ' + res.status));
      }
      return body;
    }

    function fmtUptime(sec) {
      const s = Number(sec || 0);
      const h = Math.floor(s / 3600);
      const m = Math.floor((s % 3600) / 60);
      const r = s % 60;
      return String(h) + 'h ' + String(m) + 'm ' + String(r) + 's';
    }

    function bootLabel(vm) {
      if (vm.firmware) return 'firmware:' + vm.firmware;
      if (vm.kernel) return 'kernel:' + vm.kernel;
      return 'n/a';
    }

    function showCards(daemon) {
      cards.innerHTML = '';
      const items = [
        ['Daemon state', daemon.state || 'unknown'],
        ['Daemon uptime', fmtUptime(daemon.uptime_sec)],
        ['VM total', String(daemon.vm_total || 0)],
        ['Go routines', String(daemon.go_routines || 0)],
        ['Heap alloc bytes', String(daemon.heap_alloc_bytes || 0)]
      ];
      for (const item of items) {
        const d = document.createElement('div');
        d.className = 'card';
        d.innerHTML = '<div class="k">' + item[0] + '</div><div class="v">' + item[1] + '</div>';
        cards.appendChild(d);
      }
    }

    async function stopVM(uid) {
      statusEl.textContent = 'stopping ' + uid + '...';
      await api('/api/v1/vms/stop?uid=' + encodeURIComponent(uid), { method: 'POST' });
      statusEl.textContent = 'stopped ' + uid;
      await refresh();
    }

    function showRows(vms) {
      rows.innerHTML = '';
      if (!Array.isArray(vms) || vms.length === 0) {
        rows.innerHTML = '<tr><td colspan="9" class="mono">No virtual machines are currently registered.</td></tr>';
        return;
      }
      for (const vm of vms) {
        const tr = document.createElement('tr');
        const cls = vm.state === 'running' ? 'running' : 'stopped';
        tr.innerHTML = '<td class="mono">' + vm.uid + '</td>' +
          '<td><span class="state ' + cls + '">' + vm.state + '</span></td>' +
          '<td class="mono">' + fmtUptime(vm.uptime_sec) + '</td>' +
          '<td class="mono">' + (vm.mem_mib || 0) + ' MiB</td>' +
          '<td class="mono">' + (vm.num_cpu || 0) + '</td>' +
          '<td class="mono">' + Number(vm.host_cpu_pct || 0).toFixed(1) + '% (' + Number(vm.host_cpu_sec || 0).toFixed(2) + 's)</td>' +
          '<td class="mono">' + (vm.host_mem_bytes || 0) + '</td>' +
          '<td class="mono">' + bootLabel(vm) + '</td>' +
          '<td><button class="warn" data-stop="1" data-uid="' + vm.uid + '">Stop</button> <button class="ghost" data-console="1" data-uid="' + vm.uid + '">Console</button></td>';
        tr.querySelector('button[data-stop]').addEventListener('click', () => stopVM(vm.uid).catch(err => statusEl.textContent = err.message));
        tr.querySelector('button[data-console]').addEventListener('click', () => {
          consoleUID.value = vm.uid;
          openConsole().catch(err => consoleStatus.textContent = err.message);
        });
        rows.appendChild(tr);
      }
    }

    async function refresh() {
      const body = await api('/api/v1/vms');
      showCards(body.daemon || {});
      showRows(body.vms || []);
    }

    function appendConsole(text) {
      consoleOut.textContent += text;
      consoleOut.scrollTop = consoleOut.scrollHeight;
    }

    async function pollConsole() {
      if (!consoleSessionID) return;
      try {
        const body = await api('/api/v1/console/poll?id=' + encodeURIComponent(consoleSessionID));
        if (body.data_b64) {
          const decoded = atob(body.data_b64);
          appendConsole(decoded);
        }
        if (body.closed) {
          consoleStatus.textContent = 'console closed by backend';
          consoleSessionID = '';
          return;
        }
      } catch (err) {
        consoleStatus.textContent = String(err.message || err);
      }
      if (consoleSessionID) {
        consolePollTimer = setTimeout(pollConsole, 250);
      }
    }

    async function openConsole() {
      const uid = consoleUID.value.trim();
      if (!uid) throw new Error('console uid is required');
      if (consoleSessionID) {
        await closeConsole();
      }
      const body = await api('/api/v1/console/open?uid=' + encodeURIComponent(uid), { method: 'POST' });
      consoleSessionID = body.session_id;
      consoleOut.textContent = '';
      consoleStatus.textContent = 'console opened for ' + uid;
      pollConsole();
    }

    async function sendConsoleInput() {
      if (!consoleSessionID) throw new Error('open console first');
      const text = consoleInput.value;
      await api('/api/v1/console/input?id=' + encodeURIComponent(consoleSessionID), { method: 'POST', headers: { 'Content-Type': 'text/plain' }, body: text });
      consoleInput.value = '';
    }

    async function closeConsole() {
      if (!consoleSessionID) return;
      const sid = consoleSessionID;
      consoleSessionID = '';
      if (consolePollTimer) {
        clearTimeout(consolePollTimer);
        consolePollTimer = null;
      }
      await api('/api/v1/console/close?id=' + encodeURIComponent(sid), { method: 'POST' });
      consoleStatus.textContent = 'console closed';
    }

    document.getElementById('start-form').addEventListener('submit', async (ev) => {
      ev.preventDefault();
      statusEl.textContent = 'starting vm...';
      const fd = new FormData(ev.target);
      const payload = new URLSearchParams();
      for (const pair of fd.entries()) {
        if (String(pair[1]).trim() !== '') payload.append(pair[0], pair[1]);
      }
      const body = await api('/api/v1/vms/start', { method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, body: payload.toString() });
      statusEl.textContent = 'started ' + body.uid;
      await refresh();
    });

    document.getElementById('refresh').addEventListener('click', () => refresh().catch(err => statusEl.textContent = err.message));
    document.getElementById('console-open').addEventListener('click', () => openConsole().catch(err => consoleStatus.textContent = err.message));
    document.getElementById('console-send').addEventListener('click', () => sendConsoleInput().catch(err => consoleStatus.textContent = err.message));
    document.getElementById('console-close').addEventListener('click', () => closeConsole().catch(err => consoleStatus.textContent = err.message));

    refresh().catch(err => statusEl.textContent = err.message);
    setInterval(() => refresh().catch(() => {}), 5000);
  </script>
</body>
</html>`
