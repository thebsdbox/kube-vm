package webui

const consoleOnlyHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>kube-vm console</title>
  <style>
    :root {
      --bg: #0f151b;
      --panel: #17202a;
      --ink: #e8eef5;
      --muted: #b0bfce;
      --line: #2a3a49;
      --shadow: 0 16px 30px rgba(0, 0, 0, 0.32);
      --console-bg: #101410;
      --console-fg: #ccf6cc;
    }
    * { box-sizing: border-box; }
    html, body { height: 100%; }
    body {
      margin: 0;
      font-family: system-ui, -apple-system, Segoe UI, Roboto, Helvetica, Arial, sans-serif;
      color: var(--ink);
      background: radial-gradient(circle at 15% 10%, rgba(46, 169, 143, 0.16) 0%, rgba(16,22,28,0) 40%), radial-gradient(circle at 85% 0%, rgba(229,141,71,0.12) 0%, rgba(16,22,28,0) 45%), linear-gradient(180deg, #0f151b 0%, #10161c 100%);
      overflow: hidden;
    }
    .shell {
      height: 100%;
      display: grid;
      grid-template-rows: auto 1fr auto;
      gap: 12px;
      padding: 16px;
    }
    .header {
      display: flex;
      justify-content: space-between;
      align-items: center;
      gap: 12px;
      padding: 12px 14px;
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 14px;
      box-shadow: var(--shadow);
    }
    .title {
      margin: 0;
      font-size: 1rem;
      letter-spacing: 0.01em;
    }
    .meta {
      color: var(--muted);
      font-size: 0.84rem;
      font-family: "IBM Plex Mono", monospace;
    }
    .console-panel {
      min-height: 0;
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 14px;
      box-shadow: var(--shadow);
      overflow: hidden;
      display: flex;
      flex-direction: column;
    }
    .console-shell {
      flex: 1;
      width: 100%;
      min-height: 0;
      background: var(--console-bg);
      color: var(--console-fg);
      font: 400 13px/1.5 "IBM Plex Mono", monospace;
      padding: 12px;
      overflow: auto;
      white-space: pre-wrap;
      word-break: break-word;
      outline: none;
    }
    .console-shell .line { display: block; }
    .console-status {
      padding: 8px 14px 12px;
      color: var(--muted);
      font-size: 0.84rem;
      font-family: "IBM Plex Mono", monospace;
      border-top: 1px solid var(--line);
    }
    .hidden-input {
      position: fixed;
      left: -9999px;
      top: -9999px;
      opacity: 0;
      pointer-events: none;
    }
    .console-fg-black { color: #101410; }
    .console-fg-red { color: #cc6666; }
    .console-fg-green { color: #8dc891; }
    .console-fg-yellow { color: #e8d48a; }
    .console-fg-blue { color: #7fa7d6; }
    .console-fg-magenta { color: #bf8ed8; }
    .console-fg-cyan { color: #7fd0cf; }
    .console-fg-white { color: #d8d8d8; }
    .console-fg-bright-black { color: #6f7780; }
    .console-fg-bright-red { color: #ff8f8f; }
    .console-fg-bright-green { color: #b7f1b9; }
    .console-fg-bright-yellow { color: #ffe7a8; }
    .console-fg-bright-blue { color: #9ec3ff; }
    .console-fg-bright-magenta { color: #ddb2ff; }
    .console-fg-bright-cyan { color: #a8f5f5; }
    .console-fg-bright-white { color: #ffffff; }
    .console-bg-black { background-color: #101410; }
    .console-bg-red { background-color: #5b1f1f; }
    .console-bg-green { background-color: #1f5b2e; }
    .console-bg-yellow { background-color: #5b4d1f; }
    .console-bg-blue { background-color: #1f355b; }
    .console-bg-magenta { background-color: #4a1f5b; }
    .console-bg-cyan { background-color: #1f5b58; }
    .console-bg-white { background-color: #c8c8c8; }
    .console-bg-bright-black { background-color: #374047; }
    .console-bg-bright-red { background-color: #7f3232; }
    .console-bg-bright-green { background-color: #327f45; }
    .console-bg-bright-yellow { background-color: #7f6d32; }
    .console-bg-bright-blue { background-color: #324f7f; }
    .console-bg-bright-magenta { background-color: #69327f; }
    .console-bg-bright-cyan { background-color: #327f7a; }
    .console-bg-bright-white { background-color: #ffffff; }
    .console-bold { font-weight: 700; }
  </style>
</head>
<body>
  <main class="shell">
    <div class="header">
      <div>
        <h1 class="title">Shared Console</h1>
        <div class="meta" id="console-meta">connecting...</div>
      </div>
      <div class="meta" id="console-mode"></div>
    </div>
    <section class="console-panel">
      <div class="console-shell" id="console-terminal" tabindex="0"></div>
      <input id="console-hidden-input" class="hidden-input" autocomplete="off" autocapitalize="off" spellcheck="false" />
      <div class="console-status" id="console-status">starting...</div>
    </section>
  </main>

  <script>
    const consoleTermEl = document.getElementById('console-terminal');
    const consoleMetaEl = document.getElementById('console-meta');
    const consoleModeEl = document.getElementById('console-mode');
    const consoleStatusEl = document.getElementById('console-status');
    const hiddenInput = document.getElementById('console-hidden-input');

    let consoleSessionID = '';
    let consoleUID = '';
    let consoleMode = 'rw';
    let consoleReadOnly = false;
    let consoleOffset = 0;
    let consolePollTimer = null;
    let inputBuffer = '';
    let inputFlushTimer = null;
    let consoleBuffer = [];
    let currentSpan = null;
    let currentFg = '';
    let currentBg = '';
    let currentBold = false;

    function authToken() {
      return (localStorage.getItem('kubevm_web_token') || '').trim();
    }

    async function api(path, opts) {
      opts = opts || {};
      opts.headers = opts.headers || {};
      const token = authToken();
      if (token !== '') {
        opts.headers['Authorization'] = 'Bearer ' + token;
      }
      const res = await fetch(path, opts);
      const body = await res.json();
      if (!res.ok || !body.ok) {
        throw new Error(body.error || ('http ' + res.status));
      }
      return body;
    }

    function setStatus(text) {
      consoleStatusEl.textContent = text;
    }

    function clearTerminal() {
      consoleTermEl.textContent = '';
      consoleBuffer = [];
      currentSpan = null;
      currentFg = '';
      currentBg = '';
      currentBold = false;
    }

    function makeClassName() {
      const classes = [];
      if (currentFg) classes.push('console-fg-' + currentFg);
      if (currentBg) classes.push('console-bg-' + currentBg);
      if (currentBold) classes.push('console-bold');
      return classes.join(' ');
    }

    function flushText(text) {
      if (!text) return;
      if (!currentSpan) {
        currentSpan = document.createElement('span');
        const cls = makeClassName();
        if (cls) currentSpan.className = cls;
        consoleTermEl.appendChild(currentSpan);
      }
      currentSpan.appendChild(document.createTextNode(text));
      consoleTermEl.scrollTop = consoleTermEl.scrollHeight;
    }

    function applySgr(params) {
      if (!params.length) params = [0];
      for (let i = 0; i < params.length; i++) {
        const code = params[i];
        if (code === 0) {
          currentFg = '';
          currentBg = '';
          currentBold = false;
          currentSpan = null;
          continue;
        }
        if (code === 1) { currentBold = true; currentSpan = null; continue; }
        if (code === 22) { currentBold = false; currentSpan = null; continue; }
        if (code === 39) { currentFg = ''; currentSpan = null; continue; }
        if (code === 49) { currentBg = ''; currentSpan = null; continue; }
        const fgMap = ['black','red','green','yellow','blue','magenta','cyan','white'];
        const bgMap = fgMap;
        const fgBrightMap = ['bright-black','bright-red','bright-green','bright-yellow','bright-blue','bright-magenta','bright-cyan','bright-white'];
        if (code >= 30 && code <= 37) { currentFg = fgMap[code - 30]; currentSpan = null; continue; }
        if (code >= 90 && code <= 97) { currentFg = fgBrightMap[code - 90]; currentSpan = null; continue; }
        if (code >= 40 && code <= 47) { currentBg = bgMap[code - 40]; currentSpan = null; continue; }
        if (code >= 100 && code <= 107) { currentBg = fgBrightMap[code - 100]; currentSpan = null; continue; }
      }
    }

    function appendConsole(text) {
      text = String(text || '');
      let i = 0;
      let chunk = '';
      while (i < text.length) {
        const ch = text[i];
        if (ch === '\x1b' && text[i + 1] === '[') {
          if (chunk) {
            flushText(chunk);
            chunk = '';
          }
          let j = i + 2;
          while (j < text.length && !(/[A-Za-z]/).test(text[j])) j++;
          if (j < text.length) {
            const cmd = text[j];
            if (cmd === 'm') {
              const raw = text.slice(i + 2, j).trim();
              const params = raw === '' ? [0] : raw.split(';').map(v => {
                const n = parseInt(v, 10);
                return Number.isNaN(n) ? 0 : n;
              });
              applySgr(params);
            }
            i = j + 1;
            continue;
          }
        }
        if (ch === '\r') {
          i += 1;
          continue;
        }
        if (ch === '\n') {
          if (chunk) {
            flushText(chunk);
            chunk = '';
          }
          consoleTermEl.appendChild(document.createElement('br'));
          currentSpan = null;
          i += 1;
          continue;
        }
        chunk += ch;
        i += 1;
      }
      if (chunk) {
        flushText(chunk);
      }
    }

    function decodeConsoleChunk(dataB64) {
      const binary = atob(dataB64 || '');
      const bytes = new Uint8Array(binary.length);
      for (let i = 0; i < binary.length; i++) {
        bytes[i] = binary.charCodeAt(i);
      }
      if (window.TextDecoder) {
        return new TextDecoder('utf-8', { fatal: false }).decode(bytes);
      }
      let out = '';
      for (let i = 0; i < bytes.length; i++) {
        out += String.fromCharCode(bytes[i]);
      }
      return out;
    }

    function resetSessionState() {
      consoleOffset = 0;
      inputBuffer = '';
      if (inputFlushTimer) {
        clearTimeout(inputFlushTimer);
        inputFlushTimer = null;
      }
      if (consolePollTimer) {
        clearTimeout(consolePollTimer);
        consolePollTimer = null;
      }
    }

    function queueConsoleInput(data) {
      if (!consoleSessionID || consoleReadOnly) return;
      inputBuffer += data;
      if (inputFlushTimer) return;
      inputFlushTimer = setTimeout(function() {
        inputFlushTimer = null;
        flushConsoleInput().catch(function(err) {
          setStatus(String(err.message || err));
        });
      }, 20);
    }

    async function flushConsoleInput() {
      if (!consoleSessionID || inputBuffer === '') return;
      if (consoleReadOnly) {
        inputBuffer = '';
        return;
      }
      const data = inputBuffer;
      inputBuffer = '';
      await api('/api/v1/console/input?id=' + encodeURIComponent(consoleSessionID), {
        method: 'POST',
        headers: { 'Content-Type': 'text/plain' },
        body: data
      });
    }

    async function pollConsole() {
      if (!consoleSessionID) return;
      try {
        const body = await api('/api/v1/console/poll?id=' + encodeURIComponent(consoleSessionID) + '&offset=' + encodeURIComponent(String(consoleOffset || 0)) + '&console_mode=' + encodeURIComponent(consoleReadOnly ? 'ro' : 'rw'));
        if (typeof body.offset === 'number' && Number.isFinite(body.offset) && body.offset >= 0) {
          consoleOffset = body.offset;
        }
        if (body.data_b64) {
          appendConsole(decodeConsoleChunk(body.data_b64));
        }
        if (body.closed) {
          setStatus('console closed by backend');
          consoleSessionID = '';
          return;
        }
      } catch (err) {
        setStatus(String(err.message || err));
      }
      consolePollTimer = setTimeout(pollConsole, 250);
    }

    function bindInputHandlers() {
      if (!consoleReadOnly) {
        consoleTermEl.addEventListener('keydown', function(ev) {
          const key = (ev.key || '').toLowerCase();
          if ((ev.ctrlKey || ev.metaKey) && !ev.shiftKey && key === 'l') {
            clearTerminal();
            ev.preventDefault();
            return;
          }
          if ((ev.ctrlKey || ev.metaKey) && !ev.shiftKey && key === 'c') {
            queueConsoleInput('\x03');
            ev.preventDefault();
            return;
          }
          if (!ev.ctrlKey && !ev.metaKey && key === 'delete') {
            queueConsoleInput('\x1b[3~');
            ev.preventDefault();
            return;
          }
          if (ev.key === 'Enter') { queueConsoleInput('\r'); ev.preventDefault(); return; }
          if (ev.key === 'Backspace') { queueConsoleInput('\x7f'); ev.preventDefault(); return; }
          if (ev.key === 'Tab') { queueConsoleInput('\t'); ev.preventDefault(); return; }
          if (ev.key === 'ArrowUp') { queueConsoleInput('\x1b[A'); ev.preventDefault(); return; }
          if (ev.key === 'ArrowDown') { queueConsoleInput('\x1b[B'); ev.preventDefault(); return; }
          if (ev.key === 'ArrowRight') { queueConsoleInput('\x1b[C'); ev.preventDefault(); return; }
          if (ev.key === 'ArrowLeft') { queueConsoleInput('\x1b[D'); ev.preventDefault(); return; }
          if (!ev.metaKey && !ev.altKey && ev.key && ev.key.length === 1) {
            queueConsoleInput(ev.key);
          }
        });
        consoleTermEl.addEventListener('paste', function(ev) {
          const text = ev.clipboardData && ev.clipboardData.getData ? ev.clipboardData.getData('text') : '';
          if (text) {
            queueConsoleInput(text);
            ev.preventDefault();
          }
        });
      }
    }

    function attachConsole(sessionID, uid, mode) {
      consoleSessionID = sessionID;
      consoleUID = uid || '';
      consoleMode = mode === 'ro' ? 'ro' : 'rw';
      consoleReadOnly = consoleMode === 'ro';
      resetSessionState();
      clearTerminal();
      if (consoleMetaEl) {
        consoleMetaEl.textContent = uid ? ('VM ' + uid) : 'shared console';
      }
      if (consoleModeEl) {
        consoleModeEl.textContent = consoleReadOnly ? 'read-only' : 'read-write';
      }
      setStatus('shared console opened ' + (consoleReadOnly ? '(read-only)' : '(read-write)'));
      bindInputHandlers();
      consoleTermEl.focus();
      pollConsole();
    }

    const urlParams = new URLSearchParams(window.location.search);
    const sessionID = urlParams.get('console_session');
    const uid = urlParams.get('console_uid') || '';
    const mode = (urlParams.get('console_mode') || 'rw').toLowerCase();
    if (sessionID) {
      attachConsole(sessionID, uid, mode);
    } else {
      setStatus('missing console_session parameter');
    }
    consoleTermEl.addEventListener('click', function() {
      consoleTermEl.focus();
    });
  </script>
</body>
</html>`
