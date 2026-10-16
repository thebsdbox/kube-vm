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
  <style>
		html { font-size: 100%; scroll-behavior: smooth; }
    :root {
	--bg: #f4f6fa;
	--panel: #ffffff;
	--ink: #17243b;
	--muted: #63718a;
	--accent: #4361d8;
	--accent-2: #c77a20;
      --ok: #176d39;
      --stop: #9a2f2f;
	--line: #e2e7f0;
	--shadow: 0 4px 20px rgba(23, 36, 59, 0.04);
    }
		body.dark {
			--bg: #10161c;
			--panel: #17202a;
			--ink: #e8eef5;
			--muted: #b0bfce;
			--accent: #91a5ff;
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
			background: var(--bg);
			padding: 32px;
			line-height: 1.6;
      animation: reveal 280ms ease-out;
    }
		body.dark {
			background: var(--bg);
		}
    @keyframes reveal { from { opacity: 0; transform: translateY(6px); } to { opacity: 1; transform: translateY(0); } }
	.wrap { width: 100%; max-width: 1440px; margin: 0 auto; display: grid; gap: 24px; }
    .title { margin: 0; font-size: clamp(1.4rem, 2.8vw, 2.2rem); letter-spacing: 0.01em; }
    .subtitle { margin: 6px 0 0; color: var(--muted); font-size: 0.95rem; }
    .cards { display: grid; gap: 12px; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); }
    .card, .panel { background: var(--panel); border: 1px solid var(--line); border-radius: 14px; box-shadow: var(--shadow); }
    .card { padding: 12px 14px; }
    .k { color: var(--muted); font-size: 0.8rem; margin-bottom: 4px; }
    .v { font-size: 1.05rem; font-weight: 700; font-family: "IBM Plex Mono", monospace; }
    .panel { overflow: hidden; }
    .panel h2 { margin: 0; padding: 12px 14px; border-bottom: 1px solid var(--line); font-size: 1rem; }
	.panel-head { display: flex; align-items: center; justify-content: space-between; gap: 10px; padding: 12px 14px; border-bottom: 1px solid var(--line); }
	.panel-head h2 { padding: 0; border-bottom: 0; }
	.panel-head { flex-wrap: wrap; }
	.stats-history { margin-top: 16px; border-top: 1px solid var(--line); padding-top: 14px; }
	.stats-history[hidden] { display: none; }
	.history-controls { display: grid; gap: 10px; grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); }
	.history-chart { margin-top: 12px; border: 1px solid var(--line); border-radius: 12px; background: var(--bg); overflow: hidden; }
	.history-chart svg { display: block; width: 100%; height: auto; min-height: 180px; }
	.history-chart text { fill: var(--muted); font: 12px "IBM Plex Mono", monospace; }
	.history-chart .chart-grid { stroke: var(--line); stroke-width: 1; }
	.history-chart .chart-series { fill: none; stroke-width: 2.5; stroke-linecap: round; stroke-linejoin: round; }
	.history-legend { display: flex; flex-wrap: wrap; gap: 16px; margin-top: 10px; font-size: 0.85rem; }
	.history-key::before { content: ''; display: inline-block; width: 18px; height: 3px; margin-right: 7px; vertical-align: middle; background: var(--series-color); }
    .content { padding: 14px; }
    .grid { display: grid; gap: 10px; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); }
    .compact-grid { grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); gap: 8px; }
    .field { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
    label { display: block; font-size: 0.82rem; color: var(--muted); margin-bottom: 4px; }
	input, textarea, select { width: 100%; font: inherit; border: 1px solid var(--line); border-radius: 10px; padding: 9px 10px; background: #fff; }
    .compact-grid input, .compact-grid textarea { padding: 7px 8px; }
	.compact-grid select { padding: 7px 8px; }
	.base-disk-field { grid-column: span 2; min-width: min(100%, 20rem); }
	.base-disk-field select { min-width: 0; }
    textarea { min-height: 72px; resize: vertical; }
    .advanced-wrap { margin-top: 12px; }
    .advanced-toggle { display: inline-flex; align-items: center; gap: 6px; }
    .advanced { display: none; margin-top: 10px; }
    .advanced.open { display: block; }
    .actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 10px; }
    button { border: 0; border-radius: 10px; padding: 9px 13px; font: 600 0.92rem "Sora", sans-serif; cursor: pointer; transition: transform 120ms ease, opacity 120ms ease; }
    button:hover { transform: translateY(-1px); }
    .primary { background: var(--accent); color: #fff; }
	.toggle-on { background: var(--accent); color: #fff; }
	.toggle-off { background: #ece2d2; color: #263428; }
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
	.masthead { display: flex; justify-content: space-between; align-items: center; gap: 20px; flex-wrap: wrap; padding: 8px 0 16px; }
	.brand { display: flex; align-items: center; gap: 14px; }
	.brand-mark { display: grid; place-items: center; width: 46px; height: 46px; border-radius: 14px; background: var(--accent); color: var(--panel); font-weight: 700; letter-spacing: -2px; font-size: 1.4rem; }
	.eyebrow { color: var(--muted); text-transform: uppercase; letter-spacing: 0.12em; font-size: 0.7rem; font-weight: 700; }
	.title { font-size: 1.65rem; letter-spacing: -0.04em; }
	.subtitle { font-size: 0.9rem; }
	.toolbar { display: flex; flex-wrap: wrap; align-items: center; gap: 10px; }
	.panel { border-radius: 18px; scroll-margin-top: 24px; }
	.panel-head, .panel h2 { padding: 22px 26px; }
	.panel-head h2 { padding: 0; }
	.section-note { margin: 4px 0 0; color: var(--muted); font-size: 0.85rem; }
	.content { padding: 26px; }
	.panel > summary { list-style: none; cursor: pointer; padding: 22px 26px; display: flex; align-items: center; gap: 16px; }
	.panel > summary::-webkit-details-marker { display: none; }
	.panel > summary::after { content: '+'; margin-left: auto; color: var(--muted); font-size: 1.35rem; }
	.panel[open] > summary { border-bottom: 1px solid var(--line); }
	.panel[open] > summary::after { content: '−'; }
	.panel > summary strong { display: block; font-size: 1rem; }
	.panel > summary small { display: block; color: var(--muted); font-size: 0.85rem; margin-top: 3px; }
	.grid, .compact-grid { gap: 20px; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); }
	.compact-grid input, .compact-grid textarea, .compact-grid select, input, textarea, select { padding: 11px 13px; min-height: 44px; color: var(--ink); background: var(--panel); border-radius: 10px; }
	label { font-size: 0.82rem; font-weight: 600; margin-bottom: 7px; }
	input:disabled { opacity: 0.55; }
	input[type="checkbox"] { min-height: 18px; height: 18px; width: 18px; padding: 0; accent-color: var(--accent); }
	button { min-height: 42px; font-size: 0.85rem; padding: 10px 16px; }
	button:focus-visible, summary:focus-visible, a:focus-visible, input:focus-visible, select:focus-visible, textarea:focus-visible { outline: 3px solid var(--accent); outline-offset: 3px; }
	.primary, .toggle-on { color: #fff; background: #4361d8; }
	.ghost, .toggle-off { color: var(--ink); background: var(--bg); border: 1px solid var(--line); }
	.danger { color: var(--stop); background: transparent; border: 1px solid var(--line); }
	.actions { gap: 10px; margin-top: 20px; }
	.status { font-size: 0.85rem; overflow-wrap: anywhere; }
	.status:empty { display: none; }
	.fleet-summary { color: var(--muted); font-size: 0.85rem; }
	.vm-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 340px), 1fr)); gap: 20px; }
	.vm-card { border: 1px solid var(--line); border-radius: 14px; padding: 22px; min-width: 0; background: var(--panel); }
	.vm-card h3 { margin: 0; font-size: 1.05rem; overflow-wrap: anywhere; }
	.vm-card .card-heading { display: flex; justify-content: space-between; align-items: center; gap: 12px; }
	.vm-resources { display: flex; gap: 16px; flex-wrap: wrap; color: var(--muted); font-size: 0.85rem; margin: 18px 0; }
	.vm-network { font-size: 0.82rem; color: var(--muted); overflow-wrap: anywhere; }
	.vm-more { margin-top: 18px; border-top: 1px solid var(--line); padding-top: 14px; }
	.vm-more > summary { cursor: pointer; color: var(--muted); font-size: 0.85rem; }
	.vm-more dl { font-size: 0.8rem; }
	.vm-more dt { color: var(--muted); margin-top: 12px; }
	.vm-more dd { margin: 3px 0 0; overflow-wrap: anywhere; }
	.empty-state { padding: 40px 24px; text-align: center; color: var(--muted); grid-column: 1 / -1; }
	.empty-state strong { display: block; color: var(--ink); font-size: 1.1rem; margin-bottom: 8px; }
	.stats-toolbar { margin: 26px 0 14px; display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 12px; }
	.metrics-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 280px), 1fr)); gap: 20px; }
	.metric-section { padding: 20px; border: 1px solid var(--line); border-radius: 12px; overflow-wrap: anywhere; }
	.metric-section h3 { margin: 0 0 12px; font-size: 0.95rem; }
	.metric-section p { color: var(--muted); font-size: 0.85rem; }
	.console-shell { height: 400px; }
	.two { grid-template-columns: minmax(0, 1fr) 290px; gap: 28px; }
	.console-tools .actions button { flex: 1 1 auto; }
	.config-tools { display: flex; align-items: end; gap: 14px; flex-wrap: wrap; margin-bottom: 20px; }
	.config-tools .field { flex: 1 1 240px; }
	.config-tools .actions { margin: 0; }
	@media (max-width: 960px) { .two { grid-template-columns: 1fr; } .console-tools { max-width: none; } }
	@media (max-width: 600px) { body { padding: 16px; } .wrap { gap: 18px; } .content, .panel-head, .panel h2, .panel > summary { padding: 20px; } .panel-head h2 { padding: 0; } .base-disk-field { grid-column: span 1; min-width: 0; } .masthead .toolbar { width: 100%; } .grid, .compact-grid { grid-template-columns: 1fr; } .vm-card { padding: 18px; } }
	@media (prefers-reduced-motion: reduce) { html { scroll-behavior: auto; } body { animation: none; } button { transition: none; } }
  </style>
</head>
<body>
  <main class="wrap">
		<header>
			<div class="masthead">
				<div class="brand"><span class="brand-mark" aria-hidden="true">kv</span><div><div class="eyebrow">Virtual machine workspace</div><h1 class="title">kube-vm</h1></div></div>
				<div class="toolbar">
					<button class="ghost" type="button" data-reveal="statistics-panel">Statistics</button>
					<button class="ghost" type="button" data-reveal="connection-settings">Connection</button>
					<button class="ghost" id="theme-toggle" type="button">Appearance</button>
					<button class="primary" type="button" data-reveal="launch-panel">+ New VM</button>
				</div>
			</div>
			<div class="fleet-summary" id="fleet-summary">Connecting to your workspace…</div>
			<div class="status" id="status" role="status" aria-live="polite"></div>
		</header>

		<section class="panel" id="machines-panel" aria-labelledby="machines-heading">
			<div class="panel-head"><div><h2 id="machines-heading">Virtual machines</h2><p class="section-note">Your machines, without the noise. Open a console or explore details when you need them.</p></div><button class="ghost" type="button" id="refresh">Refresh</button></div>
			<div class="content"><div class="vm-grid" id="vm-rows"><div class="empty-state">Loading virtual machines…</div></div></div>
		</section>

		<details class="panel" id="statistics-panel">
			<summary><div><strong>Statistics explorer</strong><small>Server health, VM resources, network traffic, storage I/O, and historical graphs</small></div></summary>
			<div class="content">
				<section class="cards" id="cards"></section>
				<div class="stats-toolbar"><div class="field"><label for="statistics-vm">Inspect a virtual machine</label><select id="statistics-vm"><option value="">Select a VM for detailed statistics</option></select></div><button id="stats-history-toggle" class="toggle-off" type="button" aria-controls="stats-history" aria-expanded="false">Show History</button></div>
				<div class="metrics-grid" id="vm-statistics"><p class="section-note">Choose a VM to explore CPU, memory, network, and disk activity.</p></div>
				<div class="stats-history" id="stats-history" hidden>
					<div class="history-controls">
						<div><label for="stats-history-source">Source</label><select id="stats-history-source"><option value="all">Server / all VMs</option></select></div>
						<div><label for="stats-history-metric">Metric</label><select id="stats-history-metric">
							<option value="throughput">Storage throughput</option>
							<option value="iops">Storage IOPS</option>
							<option value="latency">Storage average latency</option>
							<option value="pending">Storage pending requests</option>
							<option value="errors">Storage errors / second</option>
							<option value="network">Network throughput</option>
							<option value="packets">Network packet rate</option>
							<option value="cpu">VM host CPU</option>
							<option value="memory">Memory</option>
						</select></div>
						<div><label for="stats-history-window">Time window</label><select id="stats-history-window">
							<option value="5">Last 5 minutes</option><option value="15" selected>Last 15 minutes</option><option value="60">Last hour</option>
						</select></div>
					</div>
					<div class="history-chart" id="stats-history-chart"></div>
					<div class="history-legend" id="stats-history-legend"></div>
					<div class="status" id="stats-history-summary"></div>
					<div class="status">Samples are collected every 5 seconds while this page is open, even when hidden. Up to one hour is kept in this tab; reloading clears history. Memory shows daemon heap or VM assigned memory.</div>
					<div class="status">Network RX/TX follows the host-side NIC counters: RX is traffic delivered to the guest, TX is traffic sent by the guest. Choose a VM or network interface for network graphs; disk sources have no network metrics.</div>
				</div>
			</div>
		</details>

		<details class="panel" id="launch-panel">
			<summary><div><strong>Create a virtual machine</strong><small>Choose a kernel, allocate resources, and launch a new instance</small></div></summary>
      <div class="content">
        <form id="start-form">
          <div class="grid compact-grid">
						<div class="field"><label for="start-uid">Machine ID</label><input id="start-uid" name="uid" placeholder="Automatically generated" /></div>
						<div class="field"><label for="start-memory">Memory (MiB)</label><input id="start-memory" name="mem" type="number" min="1" value="1024" /></div>
						<div class="field"><label for="start-cpu">Virtual CPUs</label><input id="start-cpu" name="cpu" type="number" min="1" value="1" /></div>
						<div class="field"><label for="start-kernel">Kernel image</label><input id="start-kernel" name="kernel" value="bzImage" /></div>
						<div class="field"><label for="start-initrd">Initial RAM disk</label><input id="start-initrd" name="initrd" value="initramfs.cpio.gz" /></div>
			<div class="field base-disk-field"><label for="base-disk">Base disk</label><select name="base_disk" id="base-disk"><option value="">none</option></select></div>
            <div class="field" style="justify-content: end;">
              <label>&nbsp;</label>
              <label style="display:flex; align-items:center; gap:8px; margin:0;">
                <input id="start-nat" name="nat" type="checkbox" style="width:auto;" />
                <span>NAT</span>
              </label>
            </div>
          </div>
					<p class="section-note">Enable NAT for automatic guest networking. Custom TAP and boot options are under Advanced.</p>
					<div class="advanced-wrap">
					  <button class="ghost advanced-toggle" id="advanced-toggle" type="button" aria-expanded="false">Show advanced</button>
					</div>
          <div id="advanced-start-fields" class="advanced">
            <div class="grid compact-grid" style="margin-top:8px;">
							<div class="field"><label for="start-firmware">Firmware image (optional)</label><input id="start-firmware" name="firmware" /></div>
							<div class="field"><label for="start-iso">ISO image (optional)</label><input id="start-iso" name="iso" /></div>
							<div class="field"><label for="start-tap">Custom TAP interface</label><input id="start-tap" name="tap" /></div>
							<div class="field"><label for="start-vhost">vhost-net (currently unavailable)</label><input id="start-vhost" name="vhost" type="checkbox" style="width:auto;" /></div>
			  <div class="field" style="grid-column: 1 / -1;"><label>Cmdline</label><input name="cmdline" value="console=hvc0 root=/dev/vda reboot=t" /></div>
              <div class="field"><label>Snapshot</label><input name="snapshot" placeholder="/tmp/kube-vm/vm.snap" /></div>
              <div class="field"><label>CoW base</label><input name="cow_base" placeholder="/var/lib/kube-vm/base.img" /></div>
              <div class="field"><label>CoW overlay</label><input name="cow_overlay" placeholder="/var/lib/kube-vm/vm.overlay" /></div>
            </div>
					<div style="margin-top:10px; display:flex; align-items:center; gap:8px;">
				<input id="start-serial-console" name="serial_console" type="checkbox" style="width:auto;" />
				<label for="start-serial-console" style="margin:0;">Serial console (ttyS0)</label>
			</div>
            <div class="status">If both CoW fields are set, kube-vm will attach a writable copy-on-write disk backed by the base image and overlay file.</div>
            <div style="margin-top:10px"><label>Block devices (comma or newline separated)</label><textarea name="block"></textarea></div>
					<div style="margin-top:10px; display:flex; align-items:center; gap:8px;">
						<input id="start-connect" type="checkbox" checked style="width:auto;" />
						<label for="start-connect" style="margin:0;">Auto-connect web console after VM start</label>
					</div>
          </div>
					<div class="actions"><button class="primary" type="submit">Start virtual machine</button><button class="ghost" type="button" data-reveal="saved-configurations">Use a saved configuration</button></div>
        </form>
      </div>
		</details>

		<section class="panel" id="saved-configurations">
			<div class="panel-head">
				<div><h2>Configuration library</h2><p class="section-note">Save a setup once. Launch it again in a click.</p></div>
				<button class="ghost" id="config-toggle" type="button" aria-controls="saved-config-content" aria-expanded="false">Show configurations</button>
			</div>
			<div class="content" id="saved-config-content" style="display:none;">
				<div class="config-tools">
					<div class="field">
						<label>Configuration name</label>
						<input id="config-name" placeholder="example: ubuntu-ci" />
					</div>
				<div class="actions">
					<button class="primary" id="config-save" type="button">Save Current Form</button>
					<button class="ghost" id="config-clear-recent" type="button">Clear Recent</button>
				</div>
				</div>
				<div class="status" id="config-status"></div>
				<div id="saved-config-list" style="margin-top:10px; overflow:auto;">
					<table>
						<thead><tr><th>Name</th><th>Type</th><th>Summary</th><th>Saved</th><th>Actions</th></tr></thead>
						<tbody id="config-rows"></tbody>
					</table>
				</div>
			</div>
		</section>

		<section class="panel" id="console-panel">
			<div class="panel-head"><div><h2>Console</h2><p class="section-note">Connect to a machine or share a dedicated terminal window.</p></div></div>
      <div class="content two">
				<div>
					<div class="console"><div class="console-shell" id="console-terminal"></div></div>
					<div class="status" id="console-hint">Click terminal to focus. Paste with Ctrl+Shift+V or Cmd+V. Ctrl+L clears local terminal view.</div>
				</div>
				<div class="console-tools">
					<label for="console-uid">Machine ID</label><input id="console-uid" placeholder="Select Console on a VM above" />
		  <div class="actions"><button class="primary" id="console-open" type="button">Open Console</button><button class="ghost" id="console-open-window" type="button">Open Shared Window</button><button class="ghost" id="console-copy-link" type="button">Copy Share Link</button><button class="ghost" id="console-close" type="button">Close</button></div>
		  <div style="margin-top:10px;">
			<label for="console-share-mode">Shared window mode</label>
			<select id="console-share-mode">
				<option value="rw">Read-write</option>
				<option value="ro">Read-only</option>
			</select>
		  </div>
					<div id="console-fallback-controls" style="display:none; margin-top:8px;">
						<label>Fallback input</label>
						<textarea id="console-fallback-input" placeholder="Type input for guest console"></textarea>
						<div class="actions"><button class="warn" id="console-fallback-send" type="button">Send Input</button></div>
					</div>
          <div class="status" id="console-status"></div>
        </div>
      </div>
    </section>
		<details class="panel" id="connection-settings">
			<summary><div><strong>Connection settings</strong><small>API authentication for this browser</small></div></summary>
			<div class="content"><label for="token">Bearer token (optional)</label><input type="password" id="token" autocomplete="off" placeholder="Matches the server's web token" /><p class="section-note">Stored in local storage for this browser origin. Only use a trusted browser.</p></div>
		</details>
  </main>

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
	const serialConsoleEl = document.getElementById('start-serial-console');
	const tapInputEl = document.querySelector('input[name="tap"]');
	const cmdlineInputEl = document.querySelector('input[name="cmdline"]');
	const configNameEl = document.getElementById('config-name');
	const configRowsEl = document.getElementById('config-rows');
	const configStatusEl = document.getElementById('config-status');
	const configSaveEl = document.getElementById('config-save');
	const configClearRecentEl = document.getElementById('config-clear-recent');
	const configToggleEl = document.getElementById('config-toggle');
	const configPanelContentEl = document.getElementById('saved-config-content');
	const baseDiskSelectEl = document.getElementById('base-disk');
	const advancedToggleEl = document.getElementById('advanced-toggle');
	const advancedFieldsEl = document.getElementById('advanced-start-fields');
	const consoleTermEl = document.getElementById('console-terminal');
	const consoleHint = document.getElementById('console-hint');
    const consoleUID = document.getElementById('console-uid');
    const consoleStatus = document.getElementById('console-status');
	const consoleShareModeEl = document.getElementById('console-share-mode');
	const fallbackControls = document.getElementById('console-fallback-controls');
	const fallbackInput = document.getElementById('console-fallback-input');

    let consoleSessionID = '';
    let consolePollTimer = null;
	let consoleOffset = 0;
	let consoleReadOnly = false;
	let consoleShared = false;
	let term = null;
	let inputBuffer = '';
	let inputFlushTimer = null;
	let fallbackMode = false;
	let consoleDecoder = createConsoleDecoder();
	const savedConfigKey = 'kubevm_web_saved_configs_v1';
	const recentConfigKey = 'kubevm_web_recent_configs_v1';
	const savedConfigVisibilityKey = 'kubevm_web_saved_configs_visible_v1';
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
		if (serialConsoleEl && cmdlineInputEl) {
			serialConsoleEl.addEventListener('change', function() {
				const virtioDefault = 'console=hvc0 root=/dev/vda reboot=t';
				const serialDefault = 'console=ttyS0 root=/dev/vda reboot=t';
				if (serialConsoleEl.checked && cmdlineInputEl.value.trim() === virtioDefault) {
					cmdlineInputEl.value = serialDefault;
				} else if (!serialConsoleEl.checked && cmdlineInputEl.value.trim() === serialDefault) {
					cmdlineInputEl.value = virtioDefault;
				}
			});
		}

		function toggleAdvancedFields() {
			if (!advancedFieldsEl || !advancedToggleEl) {
				return;
			}
			const open = advancedFieldsEl.classList.toggle('open');
			advancedToggleEl.textContent = open ? 'Hide advanced' : 'Show advanced';
			advancedToggleEl.setAttribute('aria-expanded', String(open));
		}

		if (advancedToggleEl && advancedFieldsEl) {
			advancedToggleEl.addEventListener('click', toggleAdvancedFields);
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

		async function refreshBaseDiskOptions() {
			if (!baseDiskSelectEl) return;
			try {
				const body = await api('/api/v1/disks/base');
				const disks = Array.isArray(body && body.disks) ? body.disks : [];
				const current = baseDiskSelectEl.value;
				baseDiskSelectEl.innerHTML = '<option value="">none</option>' + disks.map(function(path) {
					const label = String(path || '').replace(/^.*\/base-disks\//, '');
					return '<option value="' + path + '">' + label + '</option>';
				}).join('');
				if (disks.includes(current)) {
					baseDiskSelectEl.value = current;
				}
			} catch (_) {
				baseDiskSelectEl.innerHTML = '<option value="">none</option>';
			}
		}

		function loadSavedConfigs() {
			if (!window.__kubevmGlobalConfigs || !Array.isArray(window.__kubevmGlobalConfigs)) {
				return [];
			}
			return window.__kubevmGlobalConfigs.filter(function(item) {
				return !!item && (item.source === 'saved' || item.source === 'global');
			});
		}

		function saveSavedConfigs(items) {
			return items || [];
		}

		async function saveConfigToServer(cfg, name, id) {
			const body = await api('/api/v1/configs/global', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ id: id || '', name: name, payload: cfg })
			});
			return body && body.config ? body.config : null;
		}

		async function deleteConfigFromServer(id) {
			await api('/api/v1/configs/global?id=' + encodeURIComponent(id), { method: 'DELETE' });
		}

		function loadRecentConfigs() {
			return safeParseJSON(localStorage.getItem(recentConfigKey) || '[]', []);
		}

		function saveRecentConfigs(items) {
			localStorage.setItem(recentConfigKey, JSON.stringify(items || []));
		}

		function normalizeConfigPayload(cfg) {
			const extracted = normalizeBlockFields(cfg.block, cfg.cow_base, cfg.cow_overlay);
			return {
				uid: String(cfg.uid || '').trim(),
				mem: String(cfg.mem || '').trim(),
				cpu: String(cfg.cpu || '').trim(),
				kernel: String(cfg.kernel || '').trim(),
				initrd: String(cfg.initrd || '').trim(),
				firmware: String(cfg.firmware || '').trim(),
				iso: String(cfg.iso || '').trim(),
				serial_console: !!cfg.serial_console,
				base_disk: String(cfg.base_disk || '').trim(),
				tap: String(cfg.tap || '').trim(),
				nat: !!cfg.nat,
				vhost: !!cfg.vhost,
				snapshot: String(cfg.snapshot || '').trim(),
				cow_base: extracted.cow_base,
				cow_overlay: extracted.cow_overlay,
				cmdline: String(cfg.cmdline || '').trim(),
				block: extracted.block
			};
		}

		function normalizeBlockFields(rawBlock, rawCowBase, rawCowOverlay) {
			const blocks = blockValueToList(rawBlock);
			let cowBase = String(rawCowBase || '').trim();
			let cowOverlay = String(rawCowOverlay || '').trim();
			const remaining = [];
			for (const spec of blocks) {
				const parsed = parseCOWBlockSpec(spec);
				if (parsed && cowBase === '' && cowOverlay === '') {
					cowBase = parsed.base;
					cowOverlay = parsed.overlay;
					continue;
				}
				remaining.push(spec);
			}
			return {
				cow_base: cowBase,
				cow_overlay: cowOverlay,
				block: remaining.join('\n')
			};
		}

		function blockValueToList(value) {
			if (Array.isArray(value)) {
				return value.map(function(item) {
					return String(item || '').trim();
				}).filter(Boolean);
			}
			const text = String(value || '').trim();
			if (text === '') {
				return [];
			}
			return text.split(/\n|,/).map(function(item) {
				return String(item || '').trim();
			}).filter(Boolean);
		}

		function parseCOWBlockSpec(spec) {
			const raw = String(spec || '').trim();
			if (!raw.startsWith('cow:')) {
				return null;
			}
			const body = raw.slice(4);
			const q = body.indexOf('?');
			const base = q >= 0 ? body.slice(0, q) : body;
			const query = q >= 0 ? body.slice(q + 1) : '';
			const params = new URLSearchParams(query);
			const overlay = params.get('overlay') || '';
			if (base === '' || overlay === '') {
				return null;
			}
			return {
				base: decodeURIComponent(base),
				overlay: overlay
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
				serial_console: !!(serialConsoleEl && serialConsoleEl.checked),
				base_disk: fd.get('base_disk'),
				tap: fd.get('tap'),
				nat: !!(natModeEl && natModeEl.checked),
				vhost: !!(vhostNetEl && vhostNetEl.checked),
				snapshot: fd.get('snapshot'),
				cow_base: fd.get('cow_base'),
				cow_overlay: fd.get('cow_overlay'),
				cmdline: fd.get('cmdline'),
				block: fd.get('block')
			});
		}

		function applyFormConfig(cfg) {
			if (!startFormEl || !cfg) {
				return;
			}
			revealPanel('launch-panel');
			const fields = ['uid', 'mem', 'cpu', 'kernel', 'initrd', 'firmware', 'iso', 'base_disk', 'tap', 'snapshot', 'cow_base', 'cow_overlay', 'cmdline', 'block'];
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
			if (serialConsoleEl) {
				serialConsoleEl.checked = !!cfg.serial_console;
			}
			refreshNetworkModeUI();
		}

		function configSummary(cfg) {
			const network = cfg.nat ? 'nat' : (cfg.tap ? 'tap:' + cfg.tap + (cfg.vhost ? ' vhost' : '') : 'none');
			const baseDisk = cfg.base_disk ? ', base ' + String(cfg.base_disk).replace(/^.*\/base-disks\//, '') : '';
			const serialMode = cfg.serial_console ? ', ttyS0' : '';
			const storage = cfg.cow_base && cfg.cow_overlay
				? ', cow ' + cfg.cow_base + ' -> ' + cfg.cow_overlay
				: (cfg.block ? ', block ' + String(cfg.block).split(/\n|,/).filter(Boolean).length : '');
			return 'mem ' + (cfg.mem || 'n/a') + ' MiB, cpu ' + (cfg.cpu || 'n/a') + ', ' + network + ', kernel ' + (cfg.kernel || 'n/a') + baseDisk + storage + serialMode;
		}

		function newConfigID() {
			return String(Date.now()) + '-' + String(Math.floor(Math.random() * 100000));
		}

		async function loadGlobalConfigRows() {
			try {
				const body = await api('/api/v1/configs/global');
				return Array.isArray(body.configs) ? body.configs : [];
			} catch (_) {
				return [];
			}
		}

		function renderConfigRows() {
			if (!configRowsEl) {
				return;
			}
			const saved = loadSavedConfigs();
			const recent = loadRecentConfigs();
			const rows = [];
			for (const item of saved) {
				rows.push({ source: item && item.source === 'global' ? 'global' : 'saved', item: item });
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

			configRowsEl.textContent = '';
			if (rows.length === 0) {
				const empty = uiElement('tr');
				const cell = uiElement('td', 'mono', 'No saved or recent VM configurations yet.');
				cell.colSpan = 5;
				empty.appendChild(cell);
				configRowsEl.appendChild(empty);
				return;
			}

			for (const rec of rows) {
				const cfg = normalizeConfigPayload(rec.item.payload || {});
				const tr = document.createElement('tr');
				const savedAt = rec.item.updated_at || rec.item.created_at || '';
				for (const value of [rec.item.name || 'unnamed', rec.source, configSummary(cfg), savedAt]) {
					tr.appendChild(uiElement('td', 'mono', value));
				}
				const actions = uiElement('td');
				function action(label, className) {
					const button = uiElement('button', className, label);
					button.type = 'button';
					actions.appendChild(button);
					return button;
				}
				action('Load', 'ghost').addEventListener('click', function() {
					applyFormConfig(cfg);
					if (configStatusEl) {
						configStatusEl.textContent = 'loaded config "' + (rec.item.name || 'unnamed') + '"';
					}
				});

				action('Start', 'primary').addEventListener('click', async function() {
					applyFormConfig(cfg);
					if (startFormEl) {
						skipRememberRecentOnce = true;
						startFormEl.requestSubmit();
					}
				});

				if (rec.source !== 'global') action('Delete', 'danger').addEventListener('click', async function() {
					try {
						if (rec.source === 'saved' || rec.source === 'global') {
							await deleteConfigFromServer(rec.item.id);
							await refreshGlobalConfigs();
						} else {
							saveRecentConfigs(loadRecentConfigs().filter(function(x) { return x.id !== rec.item.id; }));
							renderConfigRows();
						}
						if (configStatusEl) {
							configStatusEl.textContent = 'deleted config "' + (rec.item.name || 'unnamed') + '"';
						}
					} catch (err) {
						if (configStatusEl) {
							configStatusEl.textContent = 'delete failed: ' + String(err.message || err);
						}
					}
				});

				tr.appendChild(actions);
				configRowsEl.appendChild(tr);
			}
		}

	function setSavedConfigsVisible(visible) {
		const isVisible = !!visible;
		if (configPanelContentEl) {
			configPanelContentEl.style.display = isVisible ? '' : 'none';
		}
		if (configToggleEl) {
			configToggleEl.textContent = isVisible ? 'Hide configurations' : 'Show configurations';
			configToggleEl.setAttribute('aria-expanded', String(isVisible));
			configToggleEl.classList.toggle('toggle-on', isVisible);
			configToggleEl.classList.toggle('toggle-off', !isVisible);
		}
		localStorage.setItem(savedConfigVisibilityKey, isVisible ? '1' : '0');
	}

		async function saveCurrentConfig() {
			const cfg = collectFormConfig();
			const name = String((configNameEl && configNameEl.value) || '').trim();
			if (name === '') {
				if (configStatusEl) {
					configStatusEl.textContent = 'enter a configuration name before saving';
				}
				return;
			}
			try {
				const existing = loadSavedConfigs().find(function(x) {
					return String(x.name || '').toLowerCase() === name.toLowerCase();
				});
				await saveConfigToServer(cfg, name, existing ? existing.id : '');
				await refreshGlobalConfigs();
				if (configStatusEl) {
					configStatusEl.textContent = 'saved config "' + name + '"';
				}
			} catch (err) {
				if (configStatusEl) {
					configStatusEl.textContent = 'save failed: ' + String(err.message || err);
				}
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
				themeToggleEl.textContent = dark ? 'Light mode' : 'Dark mode';
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
        const value = token();
        opts.headers['Authorization'] = 'Bearer ' + value;
        opts.headers['X-Auth-Token'] = value;
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

		const storageSamples = new Map();

		function storageStatsView(vm) {
			const view = document.createElement('div');
			const disks = Array.isArray(vm.storage) ? vm.storage : [];
			if (disks.length === 0) {
				view.textContent = 'n/a';
				storageSamples.delete(vm.uid);
				return view;
			}
			view.style.minWidth = '260px';
			const now = performance.now();
			const previous = storageSamples.get(vm.uid);
			const elapsed = previous ? (now - previous.time) / 1000 : 0;
			disks.forEach(function(disk, index) {
				const item = document.createElement('div');
				if (index > 0) item.style.marginTop = '12px';
				const heading = document.createElement('strong');
				heading.textContent = 'Disk ' + (index + 1) + ' · ' + (disk.read_only ? 'RO' : 'RW') + ' · ' + fmtBytes(disk.capacity_bytes);
				item.appendChild(heading);
				const line = function(text) {
					const el = document.createElement('div');
					el.textContent = text;
					item.appendChild(el);
				};
				const spec = document.createElement('details');
				const summary = document.createElement('summary');
				summary.textContent = 'Backing storage';
				spec.appendChild(summary);
				const path = document.createElement('div');
				path.textContent = disk.spec || 'unknown';
				path.style.overflowWrap = 'anywhere';
				spec.appendChild(path);
				item.appendChild(spec);
				line('Read ' + fmtBytes(disk.read_bytes) + ' / ' + (disk.read_ops || 0) + ' ops');
				line('Write ' + fmtBytes(disk.write_bytes) + ' / ' + (disk.write_ops || 0) + ' ops');
				const old = previous && previous.startedAt === vm.started_at ? previous.disks[index] : null;
				if (old && old.spec === disk.spec && elapsed > 0 &&
					disk.read_bytes >= old.read_bytes && disk.write_bytes >= old.write_bytes && disk.completed_ops >= old.completed_ops) {
					line('R ' + fmtBytes((disk.read_bytes - old.read_bytes) / elapsed) + '/s · W ' +
						fmtBytes((disk.write_bytes - old.write_bytes) / elapsed) + '/s · ' +
						((disk.completed_ops - old.completed_ops) / elapsed).toFixed(1) + ' IOPS');
				} else {
					line('Throughput: sampling…');
				}
				line('Flushes ' + (disk.flush_ops || 0) + ' · Pending ' + (disk.in_flight || 0) + ' · Errors ' + (disk.errors || 0));
				const avgMS = disk.completed_ops > 0 ? Number(disk.io_time_ns || 0) / disk.completed_ops / 1e6 : 0;
				line('Latency avg ' + avgMS.toFixed(2) + ' ms · max ' + (Number(disk.max_io_time_ns || 0) / 1e6).toFixed(2) + ' ms');
				view.appendChild(item);
			});
			storageSamples.set(vm.uid, {time: now, startedAt: vm.started_at, disks: disks});
			return view;
		}

    function showCards(daemon) {
      cards.innerHTML = '';
      const items = [
        ['Daemon state', daemon.state || 'unknown'],
        ['Daemon uptime', fmtUptime(daemon.uptime_sec)],
        ['VM total', String(daemon.vm_total || 0)],
        ['Go routines', String(daemon.go_routines || 0)],
        ['Heap alloc', fmtBytes(daemon.heap_alloc_bytes || 0)],
        ['Heap objects', String(daemon.heap_objects || 0)]
      ];
      for (const item of items) {
        const d = document.createElement('div');
        d.className = 'card';
        d.innerHTML = '<div class="k">' + item[0] + '</div><div class="v">' + item[1] + '</div>';
        cards.appendChild(d);
      }
    }

		function createStatsHistory() {
			const samples = [];
			let previousDisks = new Map();
			let previousNICs = new Map();
			const number = value => Number.isFinite(Number(value)) && Number(value) >= 0 ? Number(value) : 0;
			function empty(label) {
				return {label: label, cpu: null, memory: null, read: null, write: null, iops: null, errors: null, rx: null, tx: null, rxPackets: null, txPackets: null, pending: 0, latency: null, latencyNS: 0, latencyOps: 0};
			}
			function merge(target, disk) {
				for (const key of ['read', 'write', 'iops', 'errors', 'rx', 'tx', 'rxPackets', 'txPackets']) {
					if (disk[key] !== null) target[key] = (target[key] || 0) + disk[key];
				}
				target.pending += disk.pending;
				if (disk.latency !== null) {
					target.latencyNS += disk.latencyNS;
					target.latencyOps += disk.latencyOps;
					target.latency = target.latencyOps > 0 ? target.latencyNS / target.latencyOps / 1e6 : 0;
				}
			}
			function record(daemon, vms, at) {
				if (!Number.isFinite(at) || (samples.length && at <= samples[samples.length - 1].at)) return;
				const series = new Map();
				const all = empty('Server / all VMs');
				all.cpu = 0;
				all.memory = number(daemon.heap_alloc_bytes);
				series.set('all', all);
				const nextDisks = new Map();
				const nextNICs = new Map();
				for (const vm of Array.isArray(vms) ? vms : []) {
					const vmKey = 'vm:' + vm.uid;
					const total = empty('VM ' + vm.uid);
					total.cpu = number(vm.host_cpu_pct);
					total.memory = number(vm.host_mem_bytes);
					all.cpu += total.cpu;
					series.set(vmKey, total);
					(Array.isArray(vm.nics) ? vm.nics : []).forEach(function(nic, index) {
						if (!nic) return;
						const key = vmKey + ':nic:' + index;
						const identity = JSON.stringify([vm.uid, vm.started_at, index, nic.name, nic.mode]);
						const old = previousNICs.get(identity);
						const point = empty('VM ' + vm.uid + ' / NIC ' + (index + 1) + ' · ' + (nic.name || 'unnamed') + ' (' + (nic.mode || 'unknown') + ')');
						point.pending = null;
						const seconds = old ? (at - old.at) / 1000 : 0;
						const fields = {rx: 'rx_bytes', tx: 'tx_bytes', rxPackets: 'rx_packets', txPackets: 'tx_packets'};
						const valid = old && seconds > 0 && Object.values(fields).every(field =>
							Number.isFinite(nic[field]) && Number.isFinite(old.nic[field]) && old.nic[field] >= 0 && nic[field] >= old.nic[field]);
						if (valid) {
							for (const [metric, field] of Object.entries(fields)) point[metric] = (nic[field] - old.nic[field]) / seconds;
						}
						nextNICs.set(identity, {at: at, nic: Object.assign({}, nic)});
						series.set(key, point);
						merge(total, point);
						merge(all, point);
					});
					(Array.isArray(vm.storage) ? vm.storage : []).forEach(function(disk, index) {
						if (!disk) return;
						const key = vmKey + ':disk:' + index;
						const identity = JSON.stringify([vm.uid, vm.started_at, index, disk.spec]);
						const old = previousDisks.get(identity);
						const point = empty('VM ' + vm.uid + ' / disk ' + (index + 1));
						point.pending = number(disk.in_flight);
						const seconds = old ? (at - old.at) / 1000 : 0;
						const fields = ['read_bytes', 'write_bytes', 'completed_ops', 'errors', 'io_time_ns'];
						const valid = old && seconds > 0 && fields.every(field =>
							Number.isFinite(Number(disk[field])) && Number.isFinite(Number(old.disk[field])) && Number(disk[field]) >= Number(old.disk[field]));
						if (valid) {
							point.read = (disk.read_bytes - old.disk.read_bytes) / seconds;
							point.write = (disk.write_bytes - old.disk.write_bytes) / seconds;
							point.iops = (disk.completed_ops - old.disk.completed_ops) / seconds;
							point.errors = (disk.errors - old.disk.errors) / seconds;
							point.latencyOps = disk.completed_ops - old.disk.completed_ops;
							point.latencyNS = disk.io_time_ns - old.disk.io_time_ns;
							point.latency = point.latencyOps > 0 ? point.latencyNS / point.latencyOps / 1e6 : 0;
						}
						nextDisks.set(identity, {at: at, disk: Object.assign({}, disk)});
						series.set(key, point);
						merge(total, point);
						merge(all, point);
					});
				}
				previousDisks = nextDisks;
				previousNICs = nextNICs;
				samples.push({at: at, series: series});
				while (samples.length > 720 || (samples.length && at - samples[0].at > 3600000)) samples.shift();
			}
			return {samples: samples, record: record};
		}

		const statsHistory = createStatsHistory();
		const statsHistoryVisibilityKey = 'kubevm_web_stats_history_visible_v1';
		let statsHistoryVisible = false;
		const historyMetrics = {
			throughput: {title: 'Storage throughput', keys: ['read', 'write'], labels: ['Read', 'Write'], format: value => fmtBytes(value) + '/s'},
			iops: {title: 'Storage IOPS', keys: ['iops'], labels: ['Completed I/O'], format: value => value.toFixed(1) + ' ops/s'},
			latency: {title: 'Storage average latency', keys: ['latency'], labels: ['Average latency'], format: value => value.toFixed(2) + ' ms'},
			pending: {title: 'Storage pending requests', keys: ['pending'], labels: ['Pending requests'], format: value => value.toFixed(0)},
			errors: {title: 'Storage errors / second', keys: ['errors'], labels: ['Failed requests'], format: value => value.toFixed(2) + '/s'},
			network: {title: 'Network throughput', keys: ['rx', 'tx'], labels: ['Receive (RX)', 'Transmit (TX)'], format: value => fmtBytes(value) + '/s'},
			packets: {title: 'Network packet rate', keys: ['rxPackets', 'txPackets'], labels: ['Receive (RX)', 'Transmit (TX)'], format: value => value.toFixed(1) + ' packets/s'},
			cpu: {title: 'VM host CPU', keys: ['cpu'], labels: ['Host CPU'], format: value => value.toFixed(1) + '%'},
			memory: {title: 'Memory', keys: ['memory'], labels: ['Memory'], format: value => fmtBytes(value)}
		};

		function updateHistorySources() {
			const select = document.getElementById('stats-history-source');
			if (!select) return;
			const selected = select.value;
			const sources = new Map([['all', 'Server / all VMs']]);
			for (const sample of statsHistory.samples) {
				for (const [key, point] of sample.series) sources.set(key, point.label);
			}
			select.textContent = '';
			for (const [key, label] of sources) {
				const option = document.createElement('option');
				option.value = key;
				option.textContent = label;
				select.appendChild(option);
			}
			select.value = sources.has(selected) ? selected : 'all';
		}

		function renderStatsHistory() {
			if (!statsHistoryVisible) return;
			const container = document.getElementById('stats-history-chart');
			const summary = document.getElementById('stats-history-summary');
			const legend = document.getElementById('stats-history-legend');
			const source = document.getElementById('stats-history-source');
			const metric = historyMetrics[document.getElementById('stats-history-metric').value] || historyMetrics.throughput;
			const windowMS = Number(document.getElementById('stats-history-window').value) * 60000;
			const end = statsHistory.samples.length ? statsHistory.samples[statsHistory.samples.length - 1].at : Date.now();
			const start = end - windowMS;
			const samples = statsHistory.samples.filter(sample => sample.at >= start);
			container.textContent = '';
			legend.textContent = '';
			const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
			svg.setAttribute('viewBox', '0 0 900 290');
			svg.setAttribute('role', 'img');
			svg.setAttribute('aria-label', metric.title + ' history for ' + source.options[source.selectedIndex].textContent);
			function element(tag, attributes, text) {
				const el = document.createElementNS('http://www.w3.org/2000/svg', tag);
				for (const key of Object.keys(attributes)) el.setAttribute(key, String(attributes[key]));
				if (text !== undefined) el.textContent = text;
				svg.appendChild(el);
				return el;
			}
			const values = [];
			for (const sample of samples) {
				const point = sample.series.get(source.value);
				if (point) for (const key of metric.keys) if (Number.isFinite(point[key])) values.push(point[key]);
			}
			const max = Math.max(1, ...values) * 1.1;
			const x = at => 115 + (at - start) / windowMS * 765;
			const y = value => 245 - value / max * 220;
			for (let i = 0; i <= 4; i++) {
				const value = max * i / 4;
				element('line', {x1: 115, x2: 880, y1: y(value), y2: y(value), class: 'chart-grid'});
				element('text', {x: 105, y: y(value) + 4, 'text-anchor': 'end'}, metric.format(value));
			}
			for (let i = 0; i <= 2; i++) {
				const at = start + windowMS * i / 2;
				element('text', {x: x(at), y: 274, 'text-anchor': i === 2 ? 'end' : 'start'}, new Date(at).toLocaleTimeString());
			}
			const colors = ['var(--accent)', 'var(--accent-2)'];
			metric.keys.forEach(function(key, index) {
				let segment = [];
				let previousAt = null;
				function flush() {
					if (segment.length === 1) element('circle', {cx: segment[0][0], cy: segment[0][1], r: 3, fill: colors[index]});
					if (segment.length > 1) element('polyline', {points: segment.map(point => point.join(',')).join(' '), stroke: colors[index], class: 'chart-series'});
					segment = [];
				}
				for (const sample of samples) {
					const point = sample.series.get(source.value);
					const value = point ? point[key] : null;
					if (!Number.isFinite(value)) { flush(); previousAt = null; continue; }
					if (previousAt !== null && sample.at - previousAt > 20000) flush();
					segment.push([x(sample.at), y(value)]);
					previousAt = sample.at;
				}
				flush();
				const label = document.createElement('span');
				label.className = 'history-key';
				label.style.setProperty('--series-color', colors[index]);
				label.textContent = metric.labels[index];
				legend.appendChild(label);
			});
			if (values.length === 0) element('text', {x: 500, y: 130, 'text-anchor': 'middle'}, 'Waiting for samples for this source and metric…');
			container.appendChild(svg);
			const latestSample = samples.length ? samples[samples.length - 1].series.get(source.value) : null;
			const latest = metric.keys.map((key, index) => metric.labels[index] + ': ' +
				(latestSample && Number.isFinite(latestSample[key]) ? metric.format(latestSample[key]) : 'n/a'));
			summary.textContent = latest.join(' · ') + ' · ' + samples.length + ' samples in view';
		}

		function setStatsHistoryVisible(visible) {
			statsHistoryVisible = Boolean(visible);
			const panel = document.getElementById('stats-history');
			const button = document.getElementById('stats-history-toggle');
			panel.hidden = !statsHistoryVisible;
			button.textContent = statsHistoryVisible ? 'Hide History' : 'Show History';
			button.setAttribute('aria-expanded', String(statsHistoryVisible));
			button.classList.toggle('toggle-on', statsHistoryVisible);
			button.classList.toggle('toggle-off', !statsHistoryVisible);
			try { localStorage.setItem(statsHistoryVisibilityKey, statsHistoryVisible ? '1' : '0'); } catch (_) {}
			renderStatsHistory();
		}

		function initStatsHistory() {
			const button = document.getElementById('stats-history-toggle');
			button.addEventListener('click', () => setStatsHistoryVisible(!statsHistoryVisible));
			for (const id of ['stats-history-source', 'stats-history-metric', 'stats-history-window']) {
				document.getElementById(id).addEventListener('change', renderStatsHistory);
			}
			let visible = false;
			try { visible = localStorage.getItem(statsHistoryVisibilityKey) === '1'; } catch (_) {}
			setStatsHistoryVisible(visible);
		}

    async function stopVM(uid) {
			if (!window.confirm('Stop virtual machine ' + uid + '? Any unsaved guest work may be lost.')) return;
      statusEl.textContent = 'stopping ' + uid + '...';
      await api('/api/v1/vms/stop?uid=' + encodeURIComponent(uid), { method: 'POST' });
      statusEl.textContent = 'stopped ' + uid;
      await refresh();
    }

    async function pauseVM(uid) {
      statusEl.textContent = 'pausing ' + uid + '...';
      await api('/api/v1/vms/pause?uid=' + encodeURIComponent(uid), { method: 'POST' });
      statusEl.textContent = 'paused ' + uid;
      await refresh();
    }

    async function resumeVM(uid) {
      statusEl.textContent = 'resuming ' + uid + '...';
      await api('/api/v1/vms/resume?uid=' + encodeURIComponent(uid), { method: 'POST' });
      statusEl.textContent = 'resumed ' + uid;
      await refresh();
    }

    async function saveVM(uid) {
      const path = window.prompt('Snapshot path for ' + uid + ':', '/tmp/kube-vm/' + uid + '.snap');
      if (!path) {
        return;
      }
      statusEl.textContent = 'saving ' + uid + '...';
      await api('/api/v1/vms/save?uid=' + encodeURIComponent(uid) + '&path=' + encodeURIComponent(path), { method: 'POST' });
      statusEl.textContent = 'saved ' + uid + ' to ' + path;
      await refresh();
    }

    async function restoreVM(uid) {
      const path = window.prompt('Snapshot path for ' + uid + ':', '/tmp/kube-vm/' + uid + '.snap');
      if (!path) {
        return;
      }
      statusEl.textContent = 'restoring ' + uid + '...';
      await api('/api/v1/vms/restore?uid=' + encodeURIComponent(uid) + '&path=' + encodeURIComponent(path), { method: 'POST' });
      statusEl.textContent = 'restored ' + uid + ' from ' + path;
      await refresh();
    }

		let dashboardVMs = [];
		const expandedVMs = new Set();

		function uiElement(tag, className, text) {
			const element = document.createElement(tag);
			if (className) element.className = className;
			if (text !== undefined) element.textContent = String(text);
			return element;
		}

		function revealPanel(id) {
			const panel = document.getElementById(id);
			if (!panel) return;
			if (panel.tagName === 'DETAILS') panel.open = true;
			if (id === 'saved-configurations') setSavedConfigsVisible(true);
			panel.scrollIntoView({behavior: 'smooth', block: 'start'});
			const target = panel.querySelector('summary, input, button, select');
			if (target) target.focus({preventScroll: true});
		}

		function renderVMStatistics() {
			const select = document.getElementById('statistics-vm');
			const container = document.getElementById('vm-statistics');
			container.textContent = '';
			const vm = dashboardVMs.find(item => item.uid === select.value);
			if (!vm) {
				container.appendChild(uiElement('p', 'section-note', 'Choose a VM to explore CPU, memory, network, and disk activity.'));
				return;
			}
			function section(title) {
				const node = uiElement('section', 'metric-section');
				node.appendChild(uiElement('h3', '', title));
				container.appendChild(node);
				return node;
			}
			const resources = section('Host resources');
			resources.appendChild(uiElement('p', 'mono', 'CPU ' + Number(vm.host_cpu_pct || 0).toFixed(1) + '% · ' + Number(vm.host_cpu_sec || 0).toFixed(2) + ' CPU seconds'));
			resources.appendChild(uiElement('p', 'mono', 'Assigned memory ' + fmtBytes(vm.host_mem_bytes)));
			resources.appendChild(uiElement('p', 'mono', 'Uptime ' + fmtUptime(vm.uptime_sec)));
			const network = section('Network activity');
			const nics = Array.isArray(vm.nics) ? vm.nics : [];
			if (!nics.length) network.appendChild(uiElement('p', '', 'No network counters available.'));
			for (const nic of nics) {
				if (!nic) continue;
				network.appendChild(uiElement('p', 'mono', (nic.name || 'NIC') + ' · ' + (nic.mode || 'unknown')));
				network.appendChild(uiElement('p', 'mono', 'RX ' + fmtBytes(nic.rx_bytes) + ' / ' + (nic.rx_packets || 0) + ' packets'));
				network.appendChild(uiElement('p', 'mono', 'TX ' + fmtBytes(nic.tx_bytes) + ' / ' + (nic.tx_packets || 0) + ' packets'));
			}
			const storage = section('Storage activity');
			storage.appendChild(storageStatsView(vm));
		}

		function updateVMStatisticsOptions() {
			const select = document.getElementById('statistics-vm');
			const selected = select.value;
			select.textContent = '';
			const placeholder = uiElement('option', '', 'Select a VM for detailed statistics');
			placeholder.value = '';
			select.appendChild(placeholder);
			for (const vm of dashboardVMs) {
				const option = uiElement('option', '', vm.uid + ' · ' + vm.state);
				option.value = vm.uid;
				select.appendChild(option);
			}
			select.value = dashboardVMs.some(vm => vm.uid === selected) ? selected : '';
		}

		function openVMStatistics(uid) {
			revealPanel('statistics-panel');
			document.getElementById('statistics-vm').value = uid;
			renderVMStatistics();
			const source = document.getElementById('stats-history-source');
			if (Array.from(source.options).some(option => option.value === 'vm:' + uid)) source.value = 'vm:' + uid;
			setStatsHistoryVisible(true);
		}

		function createVMCard(vm) {
			const card = uiElement('article', 'vm-card');
			const heading = uiElement('div', 'card-heading');
			heading.appendChild(uiElement('h3', 'mono', vm.uid));
			heading.appendChild(uiElement('span', 'state ' + (vm.state === 'running' ? 'running' : 'stopped'), vm.state || 'unknown'));
			card.appendChild(heading);
			const resources = uiElement('div', 'vm-resources');
			for (const label of [(vm.num_cpu || 0) + ' vCPU', fmtBytes(Number(vm.mem_mib || 0) * 1024 * 1024) + ' RAM', fmtUptime(vm.uptime_sec)]) {
				resources.appendChild(uiElement('span', '', label));
			}
			card.appendChild(resources);
			card.appendChild(uiElement('div', 'vm-network', vm.nat_guest_ip ? 'Guest IP · ' + vm.nat_guest_ip : (vm.tap ? 'Network · ' + vm.tap : 'No network attached')));
			const actions = uiElement('div', 'actions');
			function button(parent, label, name, callback, className) {
				const node = uiElement('button', className || 'ghost', label);
				node.type = 'button';
				node.setAttribute('data-vm', vm.uid);
				node.setAttribute('data-action', name);
				node.setAttribute('aria-label', label + ' · ' + vm.uid);
				node.addEventListener('click', () => Promise.resolve().then(callback).catch(err => statusEl.textContent = err.message));
				parent.appendChild(node);
				return node;
			}
			button(actions, 'Console', 'console', () => { consoleUID.value = vm.uid; return openConsole(); }, 'primary');
			button(actions, 'Statistics', 'statistics', () => openVMStatistics(vm.uid));
			card.appendChild(actions);
			const more = uiElement('details', 'vm-more');
			more.open = expandedVMs.has(vm.uid);
			const moreSummary = uiElement('summary', '', 'Details & actions');
			moreSummary.setAttribute('data-vm', vm.uid);
			moreSummary.setAttribute('data-action', 'details');
			more.appendChild(moreSummary);
			more.addEventListener('toggle', () => more.open ? expandedVMs.add(vm.uid) : expandedVMs.delete(vm.uid));
			const details = uiElement('dl');
			for (const [label, value] of [['Boot image', bootLabel(vm)], ['Networking', networkLabel(vm)], ['Disks', blockValueToList(vm.block).join(', ') || vm.iso || 'None'], ['Last error', vm.last_error || 'None']]) {
				details.appendChild(uiElement('dt', '', label));
				details.appendChild(uiElement('dd', 'mono', value));
			}
			more.appendChild(details);
			const extra = uiElement('div', 'actions');
			button(extra, 'Pause', 'pause', () => pauseVM(vm.uid));
			button(extra, 'Resume', 'resume', () => resumeVM(vm.uid));
			button(extra, 'Save snapshot', 'save', () => saveVM(vm.uid));
			button(extra, 'Restore snapshot', 'restore', () => restoreVM(vm.uid));
			button(extra, 'Stop VM', 'stop', () => stopVM(vm.uid), 'danger');
			more.appendChild(extra);
			card.appendChild(more);
			return card;
		}

		function showRows(vms) {
			dashboardVMs = Array.isArray(vms) ? vms : [];
			const active = document.activeElement;
			const focusVM = active && active.getAttribute('data-vm');
			const focusAction = active && active.getAttribute('data-action');
			rows.textContent = '';
			const present = new Set(dashboardVMs.map(vm => vm.uid));
			for (const uid of storageSamples.keys()) if (!present.has(uid)) storageSamples.delete(uid);
			for (const uid of expandedVMs) if (!present.has(uid)) expandedVMs.delete(uid);
			if (!dashboardVMs.length) {
				const empty = uiElement('div', 'empty-state');
				empty.appendChild(uiElement('strong', '', 'Your next machine starts here'));
				empty.appendChild(uiElement('p', '', 'Create a new virtual machine or reuse a configuration from your library.'));
				const create = uiElement('button', 'primary', '+ Create a VM');
				create.type = 'button';
				create.addEventListener('click', () => revealPanel('launch-panel'));
				empty.appendChild(create);
				rows.appendChild(empty);
			}
			for (const vm of dashboardVMs) rows.appendChild(createVMCard(vm));
			if (focusVM && focusAction) {
				for (const button of rows.querySelectorAll('[data-vm]')) {
					if (button.getAttribute('data-vm') === focusVM && button.getAttribute('data-action') === focusAction) button.focus({preventScroll: true});
				}
			}
			updateVMStatisticsOptions();
		}

		function initWorkspace() {
			for (const button of document.querySelectorAll('[data-reveal]')) {
				button.addEventListener('click', () => revealPanel(button.getAttribute('data-reveal')));
			}
			document.getElementById('statistics-vm').addEventListener('change', function() {
				renderVMStatistics();
				const source = document.getElementById('stats-history-source');
				const key = this.value ? 'vm:' + this.value : 'all';
				if (Array.from(source.options).some(option => option.value === key)) source.value = key;
				renderStatsHistory();
			});
			document.getElementById('statistics-panel').addEventListener('toggle', function() {
				if (this.open) {
					renderVMStatistics();
					renderStatsHistory();
				}
			});
		}

    async function refresh() {
      const body = await api('/api/v1/vms');
			statsHistory.record(body.daemon || {}, body.vms || [], Date.now());
			updateHistorySources();
      showCards(body.daemon || {});
      showRows(body.vms || []);
			const count = dashboardVMs.filter(vm => vm.state === 'running').length;
			document.getElementById('fleet-summary').textContent = count + ' running · ' + dashboardVMs.length + ' machines · Updated ' + new Date().toLocaleTimeString();
			if (document.getElementById('statistics-panel').open) renderVMStatistics();
			renderStatsHistory();
    }

		function ensureTerminal() {
			if (term) return;
			const InlineTerminalCtor = createInlineTerminalCtor();
			term = new InlineTerminalCtor();
			consoleHint.textContent = 'Running built-in terminal mode.';
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
			if (typeof term.fit === 'function') {
				term.fit();
			}
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
				if (term && typeof term.fit === 'function') {
					term.fit();
				}
			});
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

			InlineTerminal.prototype.fit = function() {
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
			if (consoleReadOnly) {
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

		async function pollConsole(sessionID) {
			if (!sessionID || sessionID !== consoleSessionID) return;
      try {
				const body = await api('/api/v1/console/poll?id=' + encodeURIComponent(sessionID) + '&offset=' + encodeURIComponent(String(consoleOffset || 0)));
				if (sessionID !== consoleSessionID) {
					return;
				}
        if (typeof body.offset === 'number' && Number.isFinite(body.offset) && body.offset >= 0) {
					consoleOffset = body.offset;
				}
        if (body.data_b64) {
					const decoded = decodeConsoleChunk(body.data_b64);
          appendConsole(decoded);
        }
        if (body.closed) {
          consoleStatus.textContent = 'console closed by backend';
					consoleSessionID = '';
				resetConsoleSessionState();
				setConsoleMode(false, false);
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

		function buildConsoleShareUrl(mode) {
			if (!consoleSessionID) {
				throw new Error('open console first');
			}
			const url = new URL(window.location.origin + '/console');
			url.searchParams.set('console_session', consoleSessionID);
			url.searchParams.set('console_mode', mode === 'ro' ? 'ro' : 'rw');
			const uid = consoleUID.value.trim();
			if (uid) {
				url.searchParams.set('console_uid', uid);
			}
			return url.toString();
		}

		function openConsoleWindow() {
			if (!consoleSessionID) {
				throw new Error('open console first');
			}
			const mode = consoleShareModeEl && consoleShareModeEl.value === 'ro' ? 'ro' : 'rw';
			const shareUrl = buildConsoleShareUrl(mode);
			copyTextToClipboard(shareUrl).catch(function() {});
			const popup = window.open(shareUrl, '_blank', 'width=980,height=680,resizable=yes,scrollbars=yes');
			if (!popup) {
				throw new Error('popup blocked by the browser');
			}
			if (consoleStatus) {
				consoleStatus.textContent = 'shared console URL copied and opened (' + mode + ')';
			}
			return popup;
		}

		async function copyConsoleShareLink() {
			if (!consoleSessionID) {
				throw new Error('open console first');
			}
			const mode = consoleShareModeEl && consoleShareModeEl.value === 'ro' ? 'ro' : 'rw';
			const shareUrl = buildConsoleShareUrl(mode);
			await copyTextToClipboard(shareUrl);
			if (consoleStatus) {
				consoleStatus.textContent = 'shared console URL copied (' + mode + ')';
			}
		}

		async function copyTextToClipboard(text) {
			if (navigator.clipboard && navigator.clipboard.writeText) {
				await navigator.clipboard.writeText(text);
				return true;
			}
			const ta = document.createElement('textarea');
			ta.value = text;
			ta.setAttribute('readonly', 'readonly');
			ta.style.position = 'fixed';
			ta.style.left = '-9999px';
			ta.style.top = '0';
			document.body.appendChild(ta);
			ta.focus();
			ta.select();
			let copied = false;
			try {
				copied = document.execCommand && document.execCommand('copy');
			} catch (_) {
				copied = false;
			}
			ta.remove();
			if (copied) {
				return true;
			}
			window.prompt('Copy this shared console URL', text);
			return false;
		}

		function revealConsole() {
			const panel = consoleTermEl && consoleTermEl.closest ? consoleTermEl.closest('.panel') : consoleTermEl;
			if (panel && panel.scrollIntoView) {
				panel.scrollIntoView({ behavior: 'smooth', block: 'start' });
			}
		}

		function delay(ms) {
			return new Promise(function(resolve) {
				setTimeout(resolve, ms);
			});
		}

		function setConsoleMode(isReadOnly, isShared) {
			consoleReadOnly = !!isReadOnly;
			consoleShared = !!isShared;
			if (fallbackControls) {
				fallbackControls.style.display = consoleReadOnly ? 'none' : '';
			}
			if (consoleShareModeEl && consoleShared) {
				consoleShareModeEl.value = consoleReadOnly ? 'ro' : 'rw';
			}
		}

		function resetConsoleSessionState() {
			consoleOffset = 0;
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
			resetConsoleSessionState();
			setConsoleMode(false, false);
			if (term) {
				term.reset();
				term.writeln('connected to ' + uid + '\\r\\n');
			} else {
				consoleTermEl.textContent += 'connected to ' + uid + '\n\n';
			}
      consoleStatus.textContent = 'console opened for ' + uid;
			if (term && typeof term.fit === 'function') {
				term.fit();
			}
			revealConsole();
			if (term) {
				term.focus();
			}
			pollConsole(consoleSessionID);
    }

		async function attachSharedConsole(sessionID, uid, mode) {
			if (!sessionID) {
				throw new Error('console session is required');
			}
			ensureTerminal();
			if (consoleSessionID) {
				await closeConsole();
			}
			consoleSessionID = sessionID;
			resetConsoleSessionState();
			setConsoleMode(mode === 'ro', true);
			if (term) {
				term.reset();
				term.writeln('shared console connected' + (uid ? ' to ' + uid : '') + '\r\n');
				if (consoleReadOnly) {
					term.writeln('read-only view\r\n');
				}
			} else {
				consoleTermEl.textContent += 'shared console connected' + (uid ? ' to ' + uid : '') + '\n';
				if (consoleReadOnly) {
					consoleTermEl.textContent += 'read-only view\n';
				}
			}
			consoleStatus.textContent = 'shared console opened' + (consoleReadOnly ? ' (read-only)' : ' (read-write)');
			if (term && typeof term.fit === 'function') {
				term.fit();
			}
			revealConsole();
			if (term) {
				term.focus();
			}
			pollConsole(consoleSessionID);
		}

		async function openConsoleWithRetry(uid, attempts, delayMs) {
			consoleUID.value = uid;
			let lastErr = null;
			const tries = Math.max(1, attempts || 1);
			for (let i = 0; i < tries; i++) {
				try {
					if (i > 0) {
						consoleStatus.textContent = 'retrying console connect for ' + uid + ' (' + (i + 1) + '/' + tries + ')';
					}
					await openConsole();
					return;
				} catch (err) {
					lastErr = err;
					if (i === tries - 1) {
						break;
					}
					await delay(delayMs || 150);
				}
			}
			throw lastErr || new Error('console connect failed');
		}

		async function sendFallbackInput() {
			if (!consoleSessionID) throw new Error('open console first');
			if (consoleReadOnly) throw new Error('shared console is read-only');
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
			const wasShared = consoleShared;
      consoleSessionID = '';
			resetConsoleSessionState();
			setConsoleMode(false, false);
			if (!wasShared) {
				await api('/api/v1/console/close?id=' + encodeURIComponent(sid), { method: 'POST' });
				consoleStatus.textContent = 'console closed';
			} else {
				consoleStatus.textContent = 'shared console closed locally';
			}
			if (term) {
				term.writeln('\\r\\n[console closed]');
			} else {
				consoleTermEl.textContent += '\n[console closed]\n';
			}
    }

    startFormEl.addEventListener('submit', async (ev) => {
      ev.preventDefault();
      statusEl.textContent = 'starting vm...';
      try {
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
			if (startConnectEl && startConnectEl.checked && body.uid) {
				try {
					consoleStatus.textContent = 'opening console for ' + body.uid + '...';
					await openConsoleWithRetry(body.uid, 10, 150);
				} catch (err) {
					consoleStatus.textContent = String(err.message || err);
				}
			}
			await refresh();
			if (skipRememberRecentOnce) {
				skipRememberRecentOnce = false;
			} else {
				try {
					rememberRecentConfig(runConfig, body.uid);
				} catch (err) {
					if (configStatusEl) {
						configStatusEl.textContent = String(err.message || err);
					}
				}
			}
      } catch (err) {
        const msg = err && err.message ? err.message : String(err);
        statusEl.textContent = 'start failed: ' + msg;
        if (configStatusEl) {
          configStatusEl.textContent = 'start failed: ' + msg;
        }
        if (consoleStatus) {
          consoleStatus.textContent = 'start failed: ' + msg;
        }
      }
    });

const refreshBtn = document.getElementById('refresh');
		const consoleOpenBtn = document.getElementById('console-open');
		const consoleOpenWindowBtn = document.getElementById('console-open-window');
		const consoleCopyLinkBtn = document.getElementById('console-copy-link');
		const consoleCloseBtn = document.getElementById('console-close');
		const consoleFallbackSendBtn = document.getElementById('console-fallback-send');
		if (refreshBtn) refreshBtn.addEventListener('click', () => refresh().catch(err => statusEl.textContent = err.message));
		if (consoleOpenBtn) consoleOpenBtn.addEventListener('click', () => openConsole().catch(err => consoleStatus.textContent = err.message));
		if (consoleOpenWindowBtn) consoleOpenWindowBtn.addEventListener('click', () => openConsoleWindow().catch(err => consoleStatus.textContent = err.message));
		if (consoleCopyLinkBtn) consoleCopyLinkBtn.addEventListener('click', () => copyConsoleShareLink().catch(err => consoleStatus.textContent = err.message));
		if (consoleCloseBtn) consoleCloseBtn.addEventListener('click', () => closeConsole().catch(err => consoleStatus.textContent = err.message));
		if (consoleFallbackSendBtn) consoleFallbackSendBtn.addEventListener('click', () => sendFallbackInput().catch(err => consoleStatus.textContent = err.message));
		if (configSaveEl) {
			configSaveEl.addEventListener('click', saveCurrentConfig);
		}
		if (configClearRecentEl) {
			configClearRecentEl.addEventListener('click', () => {
				saveRecentConfigs([]);
				renderConfigRows();
				if (configStatusEl) {
					configStatusEl.textContent = 'cleared recent configuration history';
				}
			});
		}
		if (configToggleEl) {
			configToggleEl.addEventListener('click', () => {
				const currentlyVisible = !configPanelContentEl || configPanelContentEl.style.display !== 'none';
				setSavedConfigsVisible(!currentlyVisible);
			});
			setSavedConfigsVisible(localStorage.getItem(savedConfigVisibilityKey) === '1');
		}

		async function refreshGlobalConfigs() {
			window.__kubevmGlobalConfigs = await loadGlobalConfigRows();
			renderConfigRows();
		}

		initTheme();
		initStatsHistory();
		initWorkspace();
		ensureTerminal();
		refreshBaseDiskOptions().catch(() => {});
		refreshGlobalConfigs().catch(() => {});
		renderConfigRows();
    refresh().catch(err => statusEl.textContent = err.message);
		const urlParams = new URLSearchParams(window.location.search);
		const autoConsoleSessionID = urlParams.get('console_session');
		const autoConsoleMode = (urlParams.get('console_mode') || '').toLowerCase();
		const autoConsoleUID = urlParams.get('console_uid') || urlParams.get('console');
		if (autoConsoleUID) {
			consoleUID.value = autoConsoleUID;
		}
		if (autoConsoleSessionID) {
			attachSharedConsole(autoConsoleSessionID, autoConsoleUID, autoConsoleMode).catch(err => consoleStatus.textContent = err.message);
		} else if (autoConsoleUID) {
			openConsole().catch(err => consoleStatus.textContent = err.message);
		}
    setInterval(() => refresh().catch(() => {}), 5000);
  </script>
</body>
</html>`
