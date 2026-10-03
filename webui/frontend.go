package webui

const frontendHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>kube-vm control</title>
  <link rel="preconnect" href="https://fonts.googleapis.com">
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
  <link href="https://fonts.googleapis.com/css2?family=Sora:wght@400;600;700&family=IBM+Plex+Mono:wght@400;600&display=swap" rel="stylesheet">
	<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/xterm@5.5.0/css/xterm.min.css" />
  <style>
		html { font-size: 125%; }
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
		body.dark {
			--bg: #10161c;
			--panel: #17202a;
			--ink: #e8eef5;
			--muted: #b0bfce;
			--accent: #2ea98f;
			--accent-2: #e58d47;
			--ok: #53d487;
			--stop: #f18484;
			--line: #2a3a49;
			--shadow: 0 16px 30px rgba(0, 0, 0, 0.32);
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
		body.dark {
			background: radial-gradient(circle at 15% 10%, rgba(46, 169, 143, 0.16) 0%, rgba(16,22,28,0) 40%), radial-gradient(circle at 85% 0%, rgba(229,141,71,0.12) 0%, rgba(16,22,28,0) 45%), linear-gradient(180deg, #0f151b 0%, #10161c 100%);
		}
    @keyframes reveal { from { opacity: 0; transform: translateY(6px); } to { opacity: 1; transform: translateY(0); } }
	.wrap { width: 90%; max-width: none; margin: 0 auto; display: grid; gap: 16px; }
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
	body.dark .running { background: #183528; }
	body.dark .stopped { background: #3d1d21; }
	.console { width: 100%; min-height: 260px; background: #101410; border-radius: 10px; border: 1px solid #2a352a; padding: 8px; overflow: hidden; }
	.console-shell { width: 100%; height: 300px; }
	.console-fallback { width: 100%; height: 300px; overflow: auto; white-space: pre-wrap; color: #ccf6cc; font: 400 0.83rem "IBM Plex Mono", monospace; padding: 6px; }
		.two { display: grid; gap: 14px; grid-template-columns: 1fr; }
		.head-row { display: flex; justify-content: space-between; align-items: center; gap: 10px; }
		@media (max-width: 920px) { .two { grid-template-columns: 1fr; } }
    @media (max-width: 760px) { body { padding: 14px; } th:nth-child(7), td:nth-child(7), th:nth-child(8), td:nth-child(8) { display: none; } }
  </style>
</head>
<body>
  <main class="wrap">
    <header>
			<div class="head-row">
				<h1 class="title">kube-vm web control</h1>
				<button class="ghost" id="theme-toggle" type="button">Toggle Dark Mode</button>
			</div>
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
            <div><label>Initrd</label><input name="initrd" value="initramfs.cpio.gz" /></div>
            <div><label>Firmware</label><input name="firmware" /></div>
            <div><label>ISO</label><input name="iso" /></div>
            <div><label>TAP</label><input name="tap" /></div>
					<div style="display:flex; align-items:center; gap:8px; margin-top:18px;">
						<input id="start-nat" name="nat" type="checkbox" style="width:auto;" />
						<label for="start-nat" style="margin:0;">Enable NAT networking</label>
					</div>
					<div style="display:flex; align-items:center; gap:8px; margin-top:18px;">
						<input id="start-vhost" name="vhost" type="checkbox" style="width:auto;" />
						<label for="start-vhost" style="margin:0;">Request vhost-net acceleration</label>
					</div>
          </div>
					<div class="status">NAT and TAP are mutually exclusive. vhost-net requires TAP and is currently rejected if requested.</div>
          <div style="margin-top:10px"><label>Cmdline</label><input name="cmdline" value="console=hvc0 reboot=t" /></div>
          <div style="margin-top:10px"><label>Block devices (comma or newline separated)</label><textarea name="block"></textarea></div>
					<div style="margin-top:10px; display:flex; align-items:center; gap:8px;">
						<input id="start-connect" type="checkbox" checked style="width:auto;" />
						<label for="start-connect" style="margin:0;">Auto-connect web console after VM start</label>
					</div>
          <div class="actions"><button class="primary" type="submit">Start VM</button><button class="ghost" type="button" id="refresh">Refresh</button></div>
          <div class="status" id="status"></div>
        </form>
      </div>
    </section>

		<section class="panel">
			<h2>Saved VM Configurations</h2>
			<div class="content">
				<div class="grid">
					<div>
						<label>Configuration name</label>
						<input id="config-name" placeholder="example: ubuntu-ci" />
					</div>
				</div>
				<div class="actions">
					<button class="primary" id="config-save" type="button">Save Current Form</button>
					<button class="ghost" id="config-clear-recent" type="button">Clear Recent</button>
				</div>
				<div class="status" id="config-status"></div>
				<div style="margin-top:10px; overflow:auto;">
					<table>
						<thead><tr><th>Name</th><th>Type</th><th>Summary</th><th>Saved</th><th>Actions</th></tr></thead>
						<tbody id="config-rows"></tbody>
					</table>
				</div>
			</div>
		</section>

    <section class="panel">
      <h2>Virtual Machines</h2>
      <div class="content">
				<table>
					<thead><tr><th>UID</th><th>State</th><th>Uptime</th><th>Mem</th><th>CPU</th><th>Host CPU</th><th>Host Mem</th><th>Boot</th><th>Network</th><th>NIC Stats</th><th>Action</th></tr></thead>
          <tbody id="vm-rows"></tbody>
        </table>
      </div>
    </section>

    <section class="panel">
      <h2>Console</h2>
      <div class="content two">
				<div>
					<div class="console"><div class="console-shell" id="console-terminal"></div></div>
					<div class="status" id="console-hint">Click terminal to focus. Paste with Ctrl+Shift+V or Cmd+V. Ctrl+L clears local terminal view.</div>
				</div>
        <div>
          <label>VM UID</label><input id="console-uid" />
          <div class="actions"><button class="primary" id="console-open" type="button">Open Console</button><button class="ghost" id="console-close" type="button">Close</button></div>
					<div id="console-fallback-controls" style="display:none; margin-top:8px;">
						<label>Fallback input</label>
						<textarea id="console-fallback-input" placeholder="Type input for guest console"></textarea>
						<div class="actions"><button class="warn" id="console-fallback-send" type="button">Send Input</button></div>
					</div>
          <div class="status" id="console-status"></div>
        </div>
      </div>
    </section>
  </main>

	<script src="https://cdn.jsdelivr.net/npm/xterm@5.5.0/lib/xterm.min.js"></script>
	<script src="https://cdn.jsdelivr.net/npm/xterm-addon-fit@0.8.0/lib/xterm-addon-fit.min.js"></script>
  <script>
    const cards = document.getElementById('cards');
    const rows = document.getElementById('vm-rows');
    const startFormEl = document.getElementById('start-form');
    const statusEl = document.getElementById('status');
	const startConnectEl = document.getElementById('start-connect');
    const tokenEl = document.getElementById('token');
	const themeToggleEl = document.getElementById('theme-toggle');
	const natModeEl = document.getElementById('start-nat');
	const vhostNetEl = document.getElementById('start-vhost');
	const tapInputEl = document.querySelector('input[name="tap"]');
	const cmdlineInputEl = document.querySelector('input[name="cmdline"]');
	const configNameEl = document.getElementById('config-name');
	const configRowsEl = document.getElementById('config-rows');
	const configStatusEl = document.getElementById('config-status');
	const configSaveEl = document.getElementById('config-save');
	const configClearRecentEl = document.getElementById('config-clear-recent');
	const consoleTermEl = document.getElementById('console-terminal');
	const consoleHint = document.getElementById('console-hint');
    const consoleUID = document.getElementById('console-uid');
    const consoleStatus = document.getElementById('console-status');
	const fallbackControls = document.getElementById('console-fallback-controls');
	const fallbackInput = document.getElementById('console-fallback-input');

    let consoleSessionID = '';
    let consolePollTimer = null;
	let term = null;
	let fitAddon = null;
	let inputBuffer = '';
	let inputFlushTimer = null;
	let fallbackMode = false;
	let consoleDecoder = createConsoleDecoder();
	const savedConfigKey = 'kubevm_web_saved_configs_v1';
	const recentConfigKey = 'kubevm_web_recent_configs_v1';
	let refreshNetworkModeUI = function() {};
	let skipRememberRecentOnce = false;

    tokenEl.value = localStorage.getItem('kubevm_web_token') || '';
    tokenEl.addEventListener('change', () => localStorage.setItem('kubevm_web_token', tokenEl.value.trim()));
		if (startConnectEl) {
			const savedAutoConnect = localStorage.getItem('kubevm_web_autoconnect');
			if (savedAutoConnect !== null) {
				startConnectEl.checked = savedAutoConnect === '1';
			}
			startConnectEl.addEventListener('change', function() {
				localStorage.setItem('kubevm_web_autoconnect', startConnectEl.checked ? '1' : '0');
			});
		}
		if (natModeEl && tapInputEl) {
			refreshNetworkModeUI = function() {
				tapInputEl.disabled = natModeEl.checked;
				if (vhostNetEl) {
					vhostNetEl.disabled = natModeEl.checked;
					if (natModeEl.checked) {
						vhostNetEl.checked = false;
					}
				}
				if (natModeEl.checked) {
					tapInputEl.value = '';
					tapInputEl.placeholder = 'disabled while NAT is enabled';
				} else {
					tapInputEl.placeholder = '';
				}
			};
			natModeEl.addEventListener('change', refreshNetworkModeUI);
			refreshNetworkModeUI();
		}

		function nowISO() {
			return new Date().toISOString();
		}

		function safeParseJSON(raw, fallback) {
			try {
				const parsed = JSON.parse(raw);
				return parsed == null ? fallback : parsed;
			} catch (_) {
				return fallback;
			}
		}

		function loadSavedConfigs() {
			return safeParseJSON(localStorage.getItem(savedConfigKey) || '[]', []);
		}

		function saveSavedConfigs(items) {
			localStorage.setItem(savedConfigKey, JSON.stringify(items || []));
		}

		function loadRecentConfigs() {
			return safeParseJSON(localStorage.getItem(recentConfigKey) || '[]', []);
		}

		function saveRecentConfigs(items) {
			localStorage.setItem(recentConfigKey, JSON.stringify(items || []));
		}

		function normalizeConfigPayload(cfg) {
			return {
				uid: String(cfg.uid || '').trim(),
				mem: String(cfg.mem || '').trim(),
				cpu: String(cfg.cpu || '').trim(),
				kernel: String(cfg.kernel || '').trim(),
				initrd: String(cfg.initrd || '').trim(),
				firmware: String(cfg.firmware || '').trim(),
				iso: String(cfg.iso || '').trim(),
				tap: String(cfg.tap || '').trim(),
				nat: !!cfg.nat,
				vhost: !!cfg.vhost,
				cmdline: String(cfg.cmdline || '').trim(),
				block: String(cfg.block || '').trim()
			};
		}

		function collectFormConfig() {
			const fd = new FormData(startFormEl);
			return normalizeConfigPayload({
				uid: fd.get('uid'),
				mem: fd.get('mem'),
				cpu: fd.get('cpu'),
				kernel: fd.get('kernel'),
				initrd: fd.get('initrd'),
				firmware: fd.get('firmware'),
				iso: fd.get('iso'),
				tap: fd.get('tap'),
				nat: !!(natModeEl && natModeEl.checked),
				vhost: !!(vhostNetEl && vhostNetEl.checked),
				cmdline: fd.get('cmdline'),
				block: fd.get('block')
			});
		}

		function applyFormConfig(cfg) {
			if (!startFormEl || !cfg) {
				return;
			}
			const fields = ['uid', 'mem', 'cpu', 'kernel', 'initrd', 'firmware', 'iso', 'tap', 'cmdline', 'block'];
			for (const key of fields) {
				const el = startFormEl.elements.namedItem(key);
				if (el && typeof el.value !== 'undefined') {
					el.value = cfg[key] || '';
				}
			}
			if (natModeEl) {
				natModeEl.checked = !!cfg.nat;
			}
			if (vhostNetEl) {
				vhostNetEl.checked = !!cfg.vhost;
			}
			refreshNetworkModeUI();
		}

		function configSummary(cfg) {
			const network = cfg.nat ? 'nat' : (cfg.tap ? 'tap:' + cfg.tap + (cfg.vhost ? ' vhost' : '') : 'none');
			return 'mem ' + (cfg.mem || 'n/a') + ' MiB, cpu ' + (cfg.cpu || 'n/a') + ', ' + network + ', kernel ' + (cfg.kernel || 'n/a');
		}

		function newConfigID() {
			return String(Date.now()) + '-' + String(Math.floor(Math.random() * 100000));
		}

		function renderConfigRows() {
			if (!configRowsEl) {
				return;
			}
			const saved = loadSavedConfigs();
			const recent = loadRecentConfigs();
			const rows = [];
			for (const item of saved) {
				rows.push({ source: 'saved', item: item });
			}
			for (const item of recent) {
				rows.push({ source: 'recent', item: item });
			}
			rows.sort(function(a, b) {
				const at = String(a.item.updated_at || a.item.created_at || '');
				const bt = String(b.item.updated_at || b.item.created_at || '');
				if (at > bt) return -1;
				if (at < bt) return 1;
				return 0;
			});

			configRowsEl.innerHTML = '';
			if (rows.length === 0) {
				configRowsEl.innerHTML = '<tr><td colspan="5" class="mono">No saved or recent VM configurations yet.</td></tr>';
				return;
			}

			for (const rec of rows) {
				const cfg = normalizeConfigPayload(rec.item.payload || {});
				const tr = document.createElement('tr');
				const savedAt = rec.item.updated_at || rec.item.created_at || '';
				tr.innerHTML =
					'<td class="mono">' + (rec.item.name || 'unnamed') + '</td>' +
					'<td class="mono">' + rec.source + '</td>' +
					'<td class="mono">' + configSummary(cfg) + '</td>' +
					'<td class="mono">' + savedAt + '</td>' +
					'<td>' +
					'<button class="ghost" data-load="1" data-source="' + rec.source + '" data-id="' + rec.item.id + '">Load</button> ' +
					'<button class="primary" data-start="1" data-source="' + rec.source + '" data-id="' + rec.item.id + '">Start</button> ' +
					'<button class="warn" data-delete="1" data-source="' + rec.source + '" data-id="' + rec.item.id + '">Delete</button>' +
					'</td>';

				tr.querySelector('button[data-load]').addEventListener('click', function() {
					applyFormConfig(cfg);
					if (configStatusEl) {
						configStatusEl.textContent = 'loaded config "' + (rec.item.name || 'unnamed') + '"';
					}
				});

				tr.querySelector('button[data-start]').addEventListener('click', async function() {
					applyFormConfig(cfg);
					if (startFormEl) {
						skipRememberRecentOnce = true;
						startFormEl.requestSubmit();
					}
				});

				tr.querySelector('button[data-delete]').addEventListener('click', function() {
					if (rec.source === 'saved') {
						saveSavedConfigs(loadSavedConfigs().filter(function(x) { return x.id !== rec.item.id; }));
					} else {
						saveRecentConfigs(loadRecentConfigs().filter(function(x) { return x.id !== rec.item.id; }));
					}
					renderConfigRows();
					if (configStatusEl) {
						configStatusEl.textContent = 'deleted config "' + (rec.item.name || 'unnamed') + '"';
					}
				});

				configRowsEl.appendChild(tr);
			}
		}

		function saveCurrentConfig() {
			const cfg = collectFormConfig();
			const name = String((configNameEl && configNameEl.value) || '').trim();
			if (name === '') {
				if (configStatusEl) {
					configStatusEl.textContent = 'enter a configuration name before saving';
				}
				return;
			}
			const now = nowISO();
			const items = loadSavedConfigs();
			const existing = items.find(function(x) { return String(x.name || '').toLowerCase() === name.toLowerCase(); });
			if (existing) {
				existing.payload = cfg;
				existing.updated_at = now;
			} else {
				items.unshift({
					id: newConfigID(),
					name: name,
					payload: cfg,
					created_at: now,
					updated_at: now
				});
			}
			saveSavedConfigs(items.slice(0, 40));
			renderConfigRows();
			if (configStatusEl) {
				configStatusEl.textContent = 'saved config "' + name + '"';
			}
		}

		function rememberRecentConfig(cfg, uid) {
			const now = nowISO();
			const payload = normalizeConfigPayload(cfg || {});
			const key = JSON.stringify(payload);
			let items = loadRecentConfigs().filter(function(x) {
				return JSON.stringify(normalizeConfigPayload((x && x.payload) || {})) !== key;
			});
			items.unshift({
				id: newConfigID(),
				name: uid ? ('run:' + uid) : 'recent',
				payload: payload,
				created_at: now,
				updated_at: now
			});
			saveRecentConfigs(items.slice(0, 20));
			renderConfigRows();
		}

		function ensureDHCPInCmdline(cmdline, natEnabled) {
			if (!natEnabled) {
				return cmdline;
			}
			const base = String(cmdline || '').trim();
			if (base === '') {
				return 'ip=dhcp dhcp=true';
			}
			let out = base;
			if (/\bip=\S+/.test(out)) {
				out = out.replace(/\bip=\S+/, 'ip=dhcp');
			} else {
				out += ' ip=dhcp';
			}
			if (/\bdhcp=\S+/.test(out)) {
				out = out.replace(/\bdhcp=\S+/, 'dhcp=true');
			} else if (!/\bdhcp=true\b/.test(out)) {
				out += ' dhcp=true';
			}
			return out.trim();
		}

		function applyTheme(theme) {
			const dark = theme === 'dark';
			document.body.classList.toggle('dark', dark);
			if (themeToggleEl) {
				themeToggleEl.textContent = dark ? 'Switch To Light Mode' : 'Switch To Dark Mode';
			}
		}

		function initTheme() {
			let theme = localStorage.getItem('kubevm_web_theme');
			if (theme !== 'dark' && theme !== 'light') {
				theme = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
			}
			applyTheme(theme);
			if (themeToggleEl) {
				themeToggleEl.addEventListener('click', function() {
					const next = document.body.classList.contains('dark') ? 'light' : 'dark';
					localStorage.setItem('kubevm_web_theme', next);
					applyTheme(next);
				});
			}
		}

    function token() { return tokenEl.value.trim(); }

		function createConsoleDecoder() {
			if (typeof TextDecoder === 'undefined') {
				return null;
			}
			return new TextDecoder('utf-8', { fatal: false });
		}

		function resetConsoleDecoder() {
			consoleDecoder = createConsoleDecoder();
		}

		function base64ToBytes(b64) {
			const bin = atob(b64);
			const out = new Uint8Array(bin.length);
			for (let i = 0; i < bin.length; i++) {
				out[i] = bin.charCodeAt(i);
			}
			return out;
		}

		function decodeConsoleChunk(b64) {
			const bytes = base64ToBytes(b64);
			if (consoleDecoder) {
				return consoleDecoder.decode(bytes, { stream: true });
			}
			let s = '';
			for (let i = 0; i < bytes.length; i++) {
				s += String.fromCharCode(bytes[i]);
			}
			return s;
		}

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

		function networkLabel(vm) {
			if (vm.nat) {
				const subnet = vm.nat_subnet || 'n/a';
				const gw = vm.nat_gateway || 'n/a';
				const gip = vm.nat_guest_ip || 'n/a';
				const tap = vm.tap || 'n/a';
				return 'nat ' + subnet + ' gw:' + gw + ' ip:' + gip + ' tap:' + tap;
			}
			if (vm.tap) {
				return 'tap ' + vm.tap + (vm.vhost_net ? ' vhost' : '');
			}
			return 'none';
		}

		function fmtBytes(n) {
			const v = Number(n || 0);
			if (v < 1024) return String(v) + ' B';
			if (v < 1024 * 1024) return (v / 1024).toFixed(1) + ' KiB';
			if (v < 1024 * 1024 * 1024) return (v / (1024 * 1024)).toFixed(1) + ' MiB';
			return (v / (1024 * 1024 * 1024)).toFixed(1) + ' GiB';
		}

		function nicStatsLabel(vm) {
			if (!Array.isArray(vm.nics) || vm.nics.length === 0) {
				return 'n/a';
			}
			const lines = [];
			for (const nic of vm.nics) {
				if (!nic) continue;
				const name = nic.name || 'nic';
				const mode = nic.mode || 'unknown';
				const rx = (nic.rx_packets || 0) + ' pkts / ' + fmtBytes(nic.rx_bytes);
				const tx = (nic.tx_packets || 0) + ' pkts / ' + fmtBytes(nic.tx_bytes);
				lines.push(name + ' (' + mode + ') rx ' + rx + ' tx ' + tx);
			}
			return lines.length > 0 ? lines.join('<br/>') : 'n/a';
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
				rows.innerHTML = '<tr><td colspan="11" class="mono">No virtual machines are currently registered.</td></tr>';
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
		  '<td class="mono">' + networkLabel(vm) + '</td>' +
				'<td class="mono">' + nicStatsLabel(vm) + '</td>' +
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

		function ensureTerminal() {
			if (term) return;
			const XTermCtor = window.Terminal || window.XTerm || createInlineTerminalCtor();
			const FitCtor = (window.FitAddon && window.FitAddon.FitAddon) || createNoopFitAddonCtor();
			if (!window.Terminal && !window.XTerm) {
				consoleHint.textContent = 'Running built-in terminal mode (xterm CDN not available).';
			}
			term = new XTermCtor({
				cursorBlink: true,
				convertEol: true,
				fontFamily: 'IBM Plex Mono, monospace',
				fontSize: 13,
				theme: {
					background: '#101410',
					foreground: '#ccf6cc',
					cursor: '#f2f2f2',
					selectionBackground: '#254425'
				}
			});
			fitAddon = new FitCtor();
			term.loadAddon(fitAddon);
			term.attachCustomKeyEventHandler(function(ev) {
				if (ev.type !== 'keydown') {
					return true;
				}
				const key = (ev.key || '').toLowerCase();
				const hasSelection = !!(term && typeof term.hasSelection === 'function' && term.hasSelection());

				if ((ev.ctrlKey || ev.metaKey) && !ev.shiftKey && key === 'l') {
					term.clear();
					ev.preventDefault();
					return false;
				}

				if ((ev.ctrlKey || ev.metaKey) && !ev.shiftKey && key === 'c') {
					if (hasSelection || !consoleSessionID) {
						return true;
					}
					queueConsoleInput('\x03');
					ev.preventDefault();
					return false;
				}

				if (!ev.ctrlKey && !ev.metaKey && key === 'delete') {
					if (consoleSessionID) {
						queueConsoleInput('\x1b[3~');
					}
					ev.preventDefault();
					return false;
				}

				if (((ev.ctrlKey && ev.shiftKey) || ev.metaKey) && key === 'v') {
					if (navigator.clipboard && navigator.clipboard.readText) {
						navigator.clipboard.readText().then(function(text) {
							if (text) {
								queueConsoleInput(text);
							}
						}).catch(function(err) {
							consoleStatus.textContent = String(err.message || err);
						});
					}
					ev.preventDefault();
					return false;
				}

				return true;
			});
			term.open(consoleTermEl);
			fitAddon.fit();
			term.writeln('kube-vm web console ready.');
			term.writeln('Use Open Console to attach.\\r\\n');
			consoleTermEl.addEventListener('paste', function(ev) {
				if (!consoleSessionID) {
					return;
				}
				const text = ev.clipboardData && ev.clipboardData.getData ? ev.clipboardData.getData('text') : '';
				if (text) {
					queueConsoleInput(text);
					ev.preventDefault();
				}
			});
			term.onData(function(data) {
				queueConsoleInput(data);
			});
			window.addEventListener('resize', function() {
				if (fitAddon) {
					fitAddon.fit();
				}
			});
		}

		function createNoopFitAddonCtor() {
			return function NoopFitAddon() {
				this.fit = function() {};
			};
		}

		function createInlineTerminalCtor() {
			function InlineTerminal() {
				this.el = null;
				this.handlers = [];
				this.keyHandlers = [];
				this.ansi = { bold: false, fg: '', bg: '' };
			}

			InlineTerminal.prototype.loadAddon = function(addon) {
				if (addon && typeof addon.fit === 'function') {
					addon.fit();
				}
			};

			InlineTerminal.prototype.attachCustomKeyEventHandler = function(fn) {
				this.keyHandlers.push(fn);
			};

			InlineTerminal.prototype.open = function(el) {
				this.el = el;
				this.el.className = 'console-fallback';
				this.el.tabIndex = 0;
				const self = this;

				this.el.addEventListener('keydown', function(ev) {
					for (let i = 0; i < self.keyHandlers.length; i++) {
						if (!self.keyHandlers[i](ev)) {
							return;
						}
					}

					let out = '';
					if (ev.key === 'Enter') {
						out = '\r';
					} else if (ev.key === 'Backspace') {
						out = '\x7f';
					} else if (ev.key === 'Delete') {
						out = '\x1b[3~';
					} else if (ev.key === 'Tab') {
						out = '\t';
					} else if (ev.key === 'ArrowUp') {
						out = '\x1b[A';
					} else if (ev.key === 'ArrowDown') {
						out = '\x1b[B';
					} else if (ev.key === 'ArrowRight') {
						out = '\x1b[C';
					} else if (ev.key === 'ArrowLeft') {
						out = '\x1b[D';
					} else if (ev.ctrlKey && ev.key && ev.key.length === 1) {
						const c = ev.key.toUpperCase().charCodeAt(0);
						if (c >= 65 && c <= 90) {
							out = String.fromCharCode(c - 64);
						}
					} else if (!ev.metaKey && !ev.altKey && ev.key && ev.key.length === 1) {
						out = ev.key;
					}

					if (out !== '') {
						ev.preventDefault();
						for (let i = 0; i < self.handlers.length; i++) {
							self.handlers[i](out);
						}
					}
				});

				this.el.addEventListener('paste', function(ev) {
					const text = ev.clipboardData && ev.clipboardData.getData ? ev.clipboardData.getData('text') : '';
					if (text) {
						ev.preventDefault();
						for (let i = 0; i < self.handlers.length; i++) {
							self.handlers[i](text);
						}
					}
				});
			};

			InlineTerminal.prototype.writeln = function(s) {
				this.write(String(s) + '\n');
			};

			InlineTerminal.prototype._stateToStyle = function() {
				let style = '';
				if (this.ansi.fg) {
					style += 'color:' + this.ansi.fg + ';';
				}
				if (this.ansi.bg) {
					style += 'background-color:' + this.ansi.bg + ';';
				}
				if (this.ansi.bold) {
					style += 'font-weight:700;';
				}
				return style;
			};

			InlineTerminal.prototype._applySGR = function(params) {
				const fg = ['#101410', '#cc6666', '#8dc891', '#e8d48a', '#7fa7d6', '#bf8ed8', '#7fd0cf', '#d8d8d8'];
				const fgBright = ['#6f7780', '#ff8f8f', '#b7f1b9', '#ffe7a8', '#9ec3ff', '#ddb2ff', '#a8f5f5', '#ffffff'];
				const bg = ['#101410', '#5b1f1f', '#1f5b2e', '#5b4d1f', '#1f355b', '#4a1f5b', '#1f5b58', '#c8c8c8'];
				const bgBright = ['#374047', '#7f3232', '#327f45', '#7f6d32', '#324f7f', '#69327f', '#327f7a', '#ffffff'];

				if (!params.length) {
					params = [0];
				}

				for (let i = 0; i < params.length; i++) {
					const code = params[i];
					if (code === 0) {
						this.ansi.bold = false;
						this.ansi.fg = '';
						this.ansi.bg = '';
						continue;
					}
					if (code === 1) {
						this.ansi.bold = true;
						continue;
					}
					if (code === 22) {
						this.ansi.bold = false;
						continue;
					}
					if (code === 39) {
						this.ansi.fg = '';
						continue;
					}
					if (code === 49) {
						this.ansi.bg = '';
						continue;
					}
					if (code >= 30 && code <= 37) {
						this.ansi.fg = fg[code - 30];
						continue;
					}
					if (code >= 90 && code <= 97) {
						this.ansi.fg = fgBright[code - 90];
						continue;
					}
					if (code >= 40 && code <= 47) {
						this.ansi.bg = bg[code - 40];
						continue;
					}
					if (code >= 100 && code <= 107) {
						this.ansi.bg = bgBright[code - 100];
						continue;
					}
				}
			};

			InlineTerminal.prototype.write = function(s) {
				if (!this.el) {
					return;
				}
				s = String(s);
				let i = 0;
				let chunk = '';
				const self = this;

				function flushChunk() {
					if (!chunk) {
						return;
					}
					const span = document.createElement('span');
					const style = self._stateToStyle();
					if (style) {
						span.style.cssText = style;
					}
					span.textContent = chunk;
					self.el.appendChild(span);
					chunk = '';
				}

				while (i < s.length) {
					const ch = s[i];
					if (ch === '\x1b' && s[i+1] === '[') {
						flushChunk();
						let j = i + 2;
						while (j < s.length && !(/[A-Za-z]/).test(s[j])) {
							j++;
						}
						if (j < s.length) {
							const cmd = s[j];
							if (cmd === 'm') {
								const raw = s.slice(i+2, j).trim();
								const params = raw === '' ? [0] : raw.split(';').map(function(v) {
									const n = parseInt(v, 10);
									return isNaN(n) ? 0 : n;
								});
								this._applySGR(params);
							}
							i = j + 1;
							continue;
						}
					}

					if (ch === '\r') {
						i++;
						continue;
					}

					if (ch === '\n') {
						flushChunk();
						this.el.appendChild(document.createElement('br'));
						i++;
						continue;
					}

					chunk += ch;
					i++;
				}

				flushChunk();
				this.el.scrollTop = this.el.scrollHeight;
			};

			InlineTerminal.prototype.onData = function(fn) {
				this.handlers.push(fn);
			};

			InlineTerminal.prototype.focus = function() {
				if (this.el) {
					this.el.focus();
				}
			};

			InlineTerminal.prototype.clear = function() {
				if (this.el) {
					this.el.textContent = '';
				}
			};

			InlineTerminal.prototype.reset = function() {
				this.clear();
			};

			return InlineTerminal;
		}

		function enableFallbackConsole(message) {
			if (fallbackMode) {
				return;
			}
			fallbackMode = true;
			fallbackControls.style.display = 'block';
			consoleHint.textContent = message + ' Output remains readable, and input can be sent via the fallback textarea.';
			consoleTermEl.className = 'console-fallback';
			consoleTermEl.textContent = 'kube-vm web console fallback ready.\nUse Open Console to attach.\n\n';
		}

		function appendConsole(text) {
			ensureTerminal();
			if (term) {
				term.write(text);
				return;
			}
			consoleTermEl.textContent += text;
			consoleTermEl.scrollTop = consoleTermEl.scrollHeight;
		}

		function queueConsoleInput(data) {
			if (!consoleSessionID) {
				return;
			}
			inputBuffer += data;
			if (inputFlushTimer) {
				return;
			}
			inputFlushTimer = setTimeout(function() {
				inputFlushTimer = null;
				flushConsoleInput().catch(function(err) {
					consoleStatus.textContent = String(err.message || err);
				});
			}, 20);
		}

		async function flushConsoleInput() {
			if (!consoleSessionID || inputBuffer === '') {
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

		async function pollConsole(sessionID) {
			if (!sessionID || sessionID !== consoleSessionID) return;
      try {
				const body = await api('/api/v1/console/poll?id=' + encodeURIComponent(sessionID));
				if (sessionID !== consoleSessionID) {
					return;
				}
        if (body.data_b64) {
					const decoded = decodeConsoleChunk(body.data_b64);
          appendConsole(decoded);
        }
        if (body.closed) {
          consoleStatus.textContent = 'console closed by backend';
          consoleSessionID = '';
          return;
        }
      } catch (err) {
				if (sessionID !== consoleSessionID) {
					return;
				}
        consoleStatus.textContent = String(err.message || err);
      }
			if (sessionID === consoleSessionID) {
				consolePollTimer = setTimeout(function() {
					pollConsole(sessionID);
				}, 250);
      }
    }

    async function openConsole() {
      const uid = consoleUID.value.trim();
      if (!uid) throw new Error('console uid is required');
			ensureTerminal();
      if (consoleSessionID) {
        await closeConsole();
      }
      const body = await api('/api/v1/console/open?uid=' + encodeURIComponent(uid), { method: 'POST' });
      consoleSessionID = body.session_id;
			inputBuffer = '';
			resetConsoleDecoder();
			if (term) {
				term.reset();
				term.writeln('connected to ' + uid + '\\r\\n');
			} else {
				consoleTermEl.textContent += 'connected to ' + uid + '\n\n';
			}
      consoleStatus.textContent = 'console opened for ' + uid;
			if (fitAddon) {
				fitAddon.fit();
			}
			if (term) {
				term.focus();
			}
			pollConsole(consoleSessionID);
    }

		async function sendFallbackInput() {
			if (!consoleSessionID) throw new Error('open console first');
			const text = fallbackInput.value;
			if (!text) {
				return;
			}
			await api('/api/v1/console/input?id=' + encodeURIComponent(consoleSessionID), {
				method: 'POST',
				headers: { 'Content-Type': 'text/plain' },
				body: text
			});
			fallbackInput.value = '';
		}

    async function closeConsole() {
      if (!consoleSessionID) return;
      const sid = consoleSessionID;
      consoleSessionID = '';
			inputBuffer = '';
			resetConsoleDecoder();
			if (inputFlushTimer) {
				clearTimeout(inputFlushTimer);
				inputFlushTimer = null;
			}
      if (consolePollTimer) {
        clearTimeout(consolePollTimer);
        consolePollTimer = null;
      }
      await api('/api/v1/console/close?id=' + encodeURIComponent(sid), { method: 'POST' });
      consoleStatus.textContent = 'console closed';
			if (term) {
				term.writeln('\\r\\n[console closed]');
			} else {
				consoleTermEl.textContent += '\n[console closed]\n';
			}
    }

    startFormEl.addEventListener('submit', async (ev) => {
      ev.preventDefault();
      statusEl.textContent = 'starting vm...';
      const fd = new FormData(ev.target);
			const runConfig = collectFormConfig();
			if (natModeEl && natModeEl.checked) {
				const fixedCmdline = ensureDHCPInCmdline(fd.get('cmdline'), true);
				fd.set('cmdline', fixedCmdline);
				runConfig.cmdline = fixedCmdline;
				if (cmdlineInputEl) {
					cmdlineInputEl.value = fixedCmdline;
				}
			}
      const payload = new URLSearchParams();
      for (const pair of fd.entries()) {
        if (String(pair[1]).trim() !== '') payload.append(pair[0], pair[1]);
      }
      const body = await api('/api/v1/vms/start', { method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, body: payload.toString() });
      statusEl.textContent = 'started ' + body.uid;
			if (skipRememberRecentOnce) {
				skipRememberRecentOnce = false;
			} else {
				rememberRecentConfig(runConfig, body.uid);
			}
      await refresh();
			if (startConnectEl && startConnectEl.checked && body.uid) {
				consoleUID.value = body.uid;
				await openConsole();
			}
    });

    document.getElementById('refresh').addEventListener('click', () => refresh().catch(err => statusEl.textContent = err.message));
		document.getElementById('console-open').addEventListener('click', () => openConsole().catch(err => consoleStatus.textContent = err.message));
    document.getElementById('console-close').addEventListener('click', () => closeConsole().catch(err => consoleStatus.textContent = err.message));
		document.getElementById('console-fallback-send').addEventListener('click', () => sendFallbackInput().catch(err => consoleStatus.textContent = err.message));
		configSaveEl.addEventListener('click', saveCurrentConfig);
		configClearRecentEl.addEventListener('click', () => {
			saveRecentConfigs([]);
			renderConfigRows();
			if (configStatusEl) {
				configStatusEl.textContent = 'cleared recent configuration history';
			}
		});

		initTheme();
		ensureTerminal();
		renderConfigRows();
    refresh().catch(err => statusEl.textContent = err.message);
    setInterval(() => refresh().catch(() => {}), 5000);
  </script>
</body>
</html>`
