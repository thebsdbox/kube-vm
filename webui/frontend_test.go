package webui

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func TestDashboardProgressiveDisclosure(t *testing.T) {
	markup := strings.Split(frontendHTML, "<script>")[0]
	ids := map[string]bool{}
	for _, match := range regexp.MustCompile(`\bid="([^"]+)"`).FindAllStringSubmatch(markup, -1) {
		if ids[match[1]] {
			t.Errorf("duplicate dashboard ID %q", match[1])
		}
		ids[match[1]] = true
	}
	for _, id := range []string{"machines-panel", "launch-panel", "statistics-panel", "connection-settings", "saved-config-content", "vm-statistics", "statistics-vm", "console-panel", "stats-history"} {
		if !ids[id] {
			t.Errorf("missing workspace element %q", id)
		}
	}
	for _, id := range []string{"launch-panel", "statistics-panel", "connection-settings"} {
		pattern := regexp.MustCompile(`<details\b[^>]*\bid="` + id + `"[^>]*>`)
		tag := pattern.FindString(markup)
		if tag == "" || regexp.MustCompile(`\bopen\b`).MatchString(tag) {
			t.Errorf("%s should be collapsed by default: %s", id, tag)
		}
	}
	if !strings.Contains(markup, `class="vm-grid" id="vm-rows"`) {
		t.Error("VM list must use responsive cards rather than a wide table")
	}
	if strings.Index(markup, `id="machines-panel"`) > strings.Index(markup, `id="statistics-panel"`) {
		t.Error("machines should precede detailed statistics")
	}
}

func TestWorkspaceCardsAndNavigation(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	start := strings.Index(frontendHTML, "let dashboardVMs =")
	end := strings.Index(frontendHTML, "async function refresh()")
	if start < 0 || end <= start {
		t.Fatal("workspace implementation not found")
	}
	configStart := strings.Index(frontendHTML, "function renderConfigRows()")
	configEnd := strings.Index(frontendHTML, "function setSavedConfigsVisible(")
	if configStart < 0 || configEnd <= configStart {
		t.Fatal("configuration library implementation not found")
	}
	script := `
const assert = require('node:assert/strict');
class Element {
	constructor(tag = 'div') { this.tagName = tag.toUpperCase(); this.children = []; this.attributes = {}; this.events = {}; this.value = ''; this.open = false; }
	set textContent(value) { this.text = value; this.children = []; }
	get textContent() { return this.text || ''; }
	appendChild(child) { this.children.push(child); return child; }
	setAttribute(name, value) { this.attributes[name] = value; }
	getAttribute(name) { return this.attributes[name] || null; }
	addEventListener(name, callback) { this.events[name] = callback; }
	get options() { return this.children; }
	querySelector() { return null; }
	querySelectorAll(selector) {
		return this.children.flatMap(child => [child, ...child.querySelectorAll(selector)])
			.filter(child => selector === '[data-vm]' ? child.getAttribute('data-vm') : child.tagName === 'BUTTON');
	}
	scrollIntoView() { this.scrolled = true; }
	focus() { document.activeElement = this; }
}
const elements = new Map();
const document = {
	activeElement: null, createElement: tag => new Element(tag),
	getElementById: id => {
		if (!elements.has(id)) elements.set(id, new Element(id.endsWith('panel') ? 'details' : 'div'));
		return elements.get(id);
	}, querySelectorAll: () => []
};
const rows = new Element();
const statusEl = new Element();
const consoleUID = new Element();
const storageSamples = new Map();
const fmtBytes = bytes => bytes + ' B';
const fmtUptime = seconds => seconds + 's';
const bootLabel = vm => vm.kernel;
const networkLabel = vm => vm.tap || 'none';
const blockValueToList = value => Array.isArray(value) ? value : [];
const storageStatsView = vm => { const el = new Element(); el.textContent = 'disk counters'; return el; };
const configRowsEl = new Element();
let savedConfigs = [];
const loadSavedConfigs = () => savedConfigs;
const loadRecentConfigs = () => [];
const normalizeConfigPayload = cfg => cfg;
const configSummary = cfg => cfg.kernel;
let consoleCalls = 0, historyVisible = false;
async function openConsole() { consoleCalls++; }
async function pauseVM() {} async function resumeVM() {} async function saveVM() {}
async function restoreVM() {} async function stopVM() {}
function setStatsHistoryVisible(value) { historyVisible = value; }
function renderStatsHistory() {}
function setSavedConfigsVisible() {}
function text(el) { return [el.textContent, ...el.children.map(text)].join('\n'); }
` + frontendHTML[configStart:configEnd] + frontendHTML[start:end] + `
(async () => {
	savedConfigs = [{id: 'global', source: 'global', name: '<img src=x onerror=alert(1)>', payload: {kernel: '<kernel>'}}];
	renderConfigRows();
	assert.equal(configRowsEl.children.length, 1);
	assert.equal(configRowsEl.children[0].children[0].textContent, savedConfigs[0].name);
	assert.equal(configRowsEl.children[0].children[2].textContent, '<kernel>');
	assert.deepEqual(configRowsEl.querySelectorAll('button').map(button => button.textContent), ['Load', 'Start']);
	savedConfigs[0].source = 'saved';
	renderConfigRows();
	assert.deepEqual(configRowsEl.querySelectorAll('button').map(button => button.textContent), ['Load', 'Start', 'Delete']);
	showRows([]);
	assert.match(text(rows), /Your next machine starts here/);
	rows.querySelectorAll('button')[0].events.click();
	assert.equal(document.getElementById('launch-panel').open, true);
	const vm = {uid: '<script>unsafe</script>', state: 'running', num_cpu: 2, mem_mib: 512, uptime_sec: 60, kernel: '/kernel', nics: [], storage: []};
	showRows([vm]);
	assert.equal(rows.children.length, 1);
	const card = rows.children[0];
	assert.equal(card.tagName, 'ARTICLE');
	assert.ok(text(card).includes(vm.uid)); // Machine IDs are text, not innerHTML.
	assert.ok(!text(card).includes('CPU seconds')); // Detailed metrics stay out of cards.
	let buttons = rows.querySelectorAll('button');
	assert.deepEqual(buttons.map(button => button.getAttribute('data-action')), ['console', 'statistics', 'pause', 'resume', 'save', 'restore', 'stop']);
	await buttons[0].events.click();
	assert.equal(consoleUID.value, vm.uid); assert.equal(consoleCalls, 1);
	const more = card.children.find(child => child.tagName === 'DETAILS');
	assert.equal(more.open, false);
	more.open = true; more.events.toggle();
	buttons[0].focus();
	showRows([vm]);
	assert.equal(rows.children[0].children.find(child => child.tagName === 'DETAILS').open, true);
	assert.equal(document.activeElement.getAttribute('data-action'), 'console');
	const source = document.getElementById('stats-history-source');
	const option = new Element('option'); option.value = 'vm:' + vm.uid; source.appendChild(option);
	await rows.querySelectorAll('button')[1].events.click();
	assert.equal(document.getElementById('statistics-panel').open, true);
	assert.equal(document.getElementById('statistics-vm').value, vm.uid);
	assert.equal(source.value, 'vm:' + vm.uid); assert.equal(historyVisible, true);
	assert.match(text(document.getElementById('vm-statistics')), /Host resources/);
	assert.match(text(document.getElementById('vm-statistics')), /disk counters/);
	const navigation = new Element('button'); navigation.setAttribute('data-reveal', 'connection-settings');
	elements.set('connection-settings', new Element('details'));
	document.querySelectorAll = () => [navigation];
	initWorkspace();
	navigation.events.click();
	assert.equal(document.getElementById('connection-settings').open, true);
	const selectedVM = document.getElementById('statistics-vm');
	source.value = 'all'; selectedVM.events.change.call(selectedVM);
	assert.equal(source.value, 'vm:' + vm.uid);
	const statistics = document.getElementById('statistics-panel');
	statistics.events.toggle.call(statistics);
	assert.match(text(document.getElementById('vm-statistics')), /Storage activity/);
	showRows([]);
	assert.equal(expandedVMs.size, 0);
})().catch(error => { console.error(error); process.exit(1); });
`
	cmd := exec.Command(node, "-e", script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("workspace regression: %v\n%s", err, output)
	}
}

func TestFrontendJavaScriptSyntax(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	start := strings.Index(frontendHTML, "<script>")
	end := strings.LastIndex(frontendHTML, "</script>")
	if start < 0 || end <= start {
		t.Fatal("dashboard script not found")
	}
	cmd := exec.Command(node, "--check")
	cmd.Stdin = strings.NewReader(frontendHTML[start+len("<script>") : end])
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("invalid dashboard JavaScript: %v\n%s", err, output)
	}
}

func TestStorageStatsDashboardRendering(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	bytesStart := strings.Index(frontendHTML, "function fmtBytes(")
	bytesEnd := strings.Index(frontendHTML, "function nicStatsLabel(")
	statsStart := strings.Index(frontendHTML, "const storageSamples =")
	statsEnd := strings.Index(frontendHTML, "function showCards(")
	if bytesStart < 0 || bytesEnd <= bytesStart || statsStart < 0 || statsEnd <= statsStart {
		t.Fatal("storage rendering helpers not found")
	}
	script := `
const assert = require('node:assert/strict');
let now = 0;
const performance = {now: () => now};
const document = {createElement: () => ({
  textContent: '', style: {}, children: [],
  appendChild(child) { this.children.push(child); }
})};
function text(node) { return [node.textContent, ...node.children.map(text)].join('\n'); }
` + frontendHTML[bytesStart:bytesEnd] + frontendHTML[statsStart:statsEnd] + `
assert.equal(text(storageStatsView({uid: 'empty'})).trim(), 'n/a');
const disk = {spec: '<img src=x onerror=alert(1)>', capacity_bytes: 4096,
  read_only: true, read_bytes: 0, write_bytes: 0, read_ops: 0, write_ops: 0,
  completed_ops: 0, io_time_ns: 0, max_io_time_ns: 0};
let output = text(storageStatsView({uid: 'vm', started_at: 'first', storage: [disk]}));
assert.match(output, /Disk 1 · RO · 4.0 KiB/);
assert.match(output, /Throughput: sampling/);
assert.ok(output.includes(disk.spec)); // Spec remains a text node, not HTML.
now = 1000;
const next = {...disk, read_bytes: 1024, read_ops: 2, completed_ops: 2,
  io_time_ns: 4000000, max_io_time_ns: 3000000, in_flight: 1, errors: 1};
output = text(storageStatsView({uid: 'vm', started_at: 'first', storage: [next]}));
assert.match(output, /R 1.0 KiB\/s/);
assert.match(output, /2.0 IOPS/);
assert.match(output, /Pending 1 · Errors 1/);
assert.match(output, /avg 2.00 ms · max 3.00 ms/);
now = 2000;
output = text(storageStatsView({uid: 'vm', started_at: 'second', storage: [disk]}));
assert.match(output, /Throughput: sampling/);
`
	cmd := exec.Command(node, "-e", script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("storage dashboard regression: %v\n%s", err, output)
	}
}

func TestStatisticsHistorySamplingAndVisibility(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	start := strings.Index(frontendHTML, "function createStatsHistory()")
	end := strings.Index(frontendHTML, "async function stopVM(")
	if start < 0 || end <= start {
		t.Fatal("statistics history implementation not found")
	}
	script := `
const assert = require('node:assert/strict');
class Element {
  constructor() {
    this.children = []; this.attributes = {}; this.value = ''; this.hidden = false;
    this.events = {}; this.classes = new Set();
    this.style = {setProperty() {}};
    this.classList = {toggle: (name, on) => on ? this.classes.add(name) : this.classes.delete(name)};
  }
  set textContent(text) { this.text = text; this.children = []; }
  get textContent() { return this.text || ''; }
  appendChild(child) { this.children.push(child); }
  setAttribute(key, value) { this.attributes[key] = value; }
  addEventListener(event, callback) { this.events[event] = callback; }
  get options() { return this.children; }
  get selectedIndex() { return this.children.findIndex(child => child.value === this.value); }
}
const elements = new Map();
const document = {
  createElement: () => new Element(), createElementNS: () => new Element(),
  getElementById: id => { if (!elements.has(id)) elements.set(id, new Element()); return elements.get(id); }
};
const preferences = new Map();
const localStorage = {
  setItem: (key, value) => preferences.set(key, value),
  getItem: key => preferences.get(key)
};
const fmtBytes = value => value.toFixed(1) + ' B';
document.getElementById('stats-history-source').value = 'all';
document.getElementById('stats-history-metric').value = 'throughput';
document.getElementById('stats-history-window').value = '15';
const option = new Element(); option.value = 'all'; option.textContent = 'Server / all VMs';
document.getElementById('stats-history-source').appendChild(option);
` + frontendHTML[start:end] + `
const history = createStatsHistory();
const disk = {spec: 'disk.img', read_bytes: 0, write_bytes: 0, completed_ops: 0, errors: 0, io_time_ns: 0, in_flight: 1};
const nic = {name: 'tap0', mode: 'tap', rx_bytes: 1000, tx_bytes: 2000, rx_packets: 10, tx_packets: 20};
const vm = {uid: 'one', started_at: 'first', host_cpu_pct: 20, host_mem_bytes: 4096, storage: [disk], nics: [nic]};
const daemon = {heap_alloc_bytes: 2048};
history.record(daemon, [vm], 1000);
assert.equal(history.samples[0].series.get('all').read, null);
assert.equal(history.samples[0].series.get('all').cpu, 20);
assert.equal(history.samples[0].series.get('all').memory, 2048);
assert.equal(history.samples[0].series.get('vm:one').memory, 4096);
assert.equal(history.samples[0].series.get('vm:one:disk:0').pending, 1);
const next = {...disk, read_bytes: 10240, write_bytes: 5120, completed_ops: 10, errors: 2, io_time_ns: 20000000};
history.record(daemon, [{...vm, storage: [next]}], 6000);
const sample = history.samples[1].series.get('all');
assert.equal(sample.read, 2048); assert.equal(sample.write, 1024);
assert.equal(sample.iops, 2); assert.equal(sample.errors, 0.4); assert.equal(sample.latency, 2);
history.record(daemon, [{...vm, started_at: 'restart', storage: [next]}], 11000);
assert.equal(history.samples[2].series.get('all').read, null);
history.record(daemon, [{...vm, started_at: 'restart', storage: [disk]}], 16000);
assert.equal(history.samples[3].series.get('all').read, null); // Counter rollback.
history.record(daemon, [], 21000);
assert.equal(history.samples[4].series.has('vm:one'), false);
history.record(daemon, [vm], 26000);
assert.equal(history.samples[5].series.get('all').read, null); // No stale baseline after removal.
history.record(daemon, [vm], 26000);
assert.equal(history.samples.length, 6); // Duplicate timestamps ignored.
const bounded = createStatsHistory();
for (let i = 0; i < 1000; i++) bounded.record(daemon, [], i * 5000);
assert.equal(bounded.samples.length, 720);
bounded.record(daemon, [], 10000000);
assert.equal(bounded.samples.length, 1); // Old history expires after a gap.

// Network rates share the same bounded history and aggregate interfaces and VMs.
const network = createStatsHistory();
const nic2 = {...nic, name: '<tap1>'};
const initialVM = {...vm, nics: [nic, nic2]};
const otherVM = {...vm, uid: 'two', nics: [nic]};
network.record(daemon, [initialVM, otherVM], 1000);
assert.equal(network.samples[0].series.get('all').rx, null);
assert.equal(network.samples[0].series.get('vm:one:nic:0').rxPackets, null);
assert.equal(network.samples[0].series.get('vm:one:nic:0').pending, null);
assert.ok(network.samples[0].series.get('vm:one:nic:1').label.includes('<tap1>'));
const nextNIC = {...nic, rx_bytes: 11240, tx_bytes: 7120, rx_packets: 20, tx_packets: 40};
const nextNIC2 = {...nic2, rx_bytes: 6120, tx_bytes: 12240, rx_packets: 30, tx_packets: 30};
const nextOther = {...nic, rx_bytes: 3560, rx_packets: 15};
const nextVM = {...vm, nics: [nextNIC, nextNIC2]};
network.record(daemon, [nextVM, {...otherVM, nics: [nextOther]}], 6000);
const networkSample = network.samples[1].series;
assert.equal(networkSample.get('vm:one:nic:0').rx, 2048);
assert.equal(networkSample.get('vm:one:nic:0').tx, 1024);
assert.equal(networkSample.get('vm:one:nic:0').rxPackets, 2);
assert.equal(networkSample.get('vm:one:nic:0').txPackets, 4);
assert.equal(networkSample.get('vm:one').rx, 3072);
assert.equal(networkSample.get('vm:one').tx, 3072);
assert.equal(networkSample.get('all').rx, 3584);
assert.equal(networkSample.get('all').tx, 3072);
assert.equal(networkSample.get('all').rxPackets, 7);
assert.equal(networkSample.get('all').txPackets, 6);
assert.equal(networkSample.get('vm:one:disk:0').rx, null);
network.record(daemon, [{...nextVM, started_at: 'restart'}], 11000);
assert.equal(network.samples[2].series.get('vm:one').rx, null);
const resetVM = {...nextVM, started_at: 'restart', nics: [{...nextNIC, rx_bytes: 0}]};
network.record(daemon, [resetVM], 16000);
assert.equal(network.samples[3].series.get('all').rx, null);
network.record(daemon, [resetVM], 21000);
assert.equal(network.samples[4].series.get('all').rx, 0); // Idle is zero, not missing.
network.record(daemon, [{...resetVM, nics: []}], 26000);
assert.equal(network.samples[5].series.has('vm:one:nic:0'), false);
network.record(daemon, [resetVM], 31000);
assert.equal(network.samples[6].series.get('all').rx, null); // Reattached NIC needs a baseline.
network.record(daemon, [{...resetVM, nics: [{...resetVM.nics[0], name: 'replacement'}]}], 36000);
assert.equal(network.samples[7].series.get('all').rx, null);
network.record(daemon, [], 41000);
network.record(daemon, [resetVM], 46000);
assert.equal(network.samples[9].series.get('all').rx, null); // Removed VM cannot reuse its baseline.
for (const invalid of [undefined, null, NaN, Infinity, -1]) {
	const invalidHistory = createStatsHistory();
	invalidHistory.record(daemon, [vm], 1000);
	invalidHistory.record(daemon, [{...vm, nics: [{...nic, rx_bytes: invalid}]}], 6000);
	assert.equal(invalidHistory.samples[1].series.get('all').rx, null);
	invalidHistory.record(daemon, [vm], 11000);
	assert.equal(invalidHistory.samples[2].series.get('all').rx, null);
	invalidHistory.record(daemon, [vm], 16000);
	assert.equal(invalidHistory.samples[3].series.get('all').rx, 0);
}

// Visibility preferences and chart rendering work without a browser chart library.
initStatsHistory();
assert.equal(document.getElementById('stats-history').hidden, true);
assert.equal(document.getElementById('stats-history-toggle').attributes['aria-expanded'], 'false');
statsHistory.record(daemon, [vm], 1000);
statsHistory.record(daemon, [{...vm, storage: [next]}], 6000);
statsHistory.record(daemon, [{...vm, storage: [next]}], 11000);
updateHistorySources();
assert.equal(document.getElementById('stats-history-source').options.length, 4);
document.getElementById('stats-history-toggle').events.click();
assert.equal(document.getElementById('stats-history').hidden, false);
assert.equal(document.getElementById('stats-history-toggle').textContent, 'Hide History');
assert.equal(document.getElementById('stats-history-toggle').classes.has('toggle-on'), true);
assert.equal(preferences.get(statsHistoryVisibilityKey), '1');
const svg = document.getElementById('stats-history-chart').children[0];
assert.equal(svg.attributes.role, 'img');
assert.ok(svg.children.some(child => child.attributes.class === 'chart-series'));
assert.ok(svg.children.every(child => !JSON.stringify(child.attributes).includes('NaN')));
assert.equal(document.getElementById('stats-history-legend').children.length, 2);
document.getElementById('stats-history-source').value = 'vm:one:nic:0';
for (const metric of ['network', 'packets']) {
	document.getElementById('stats-history-metric').value = metric;
	renderStatsHistory();
	const networkSVG = document.getElementById('stats-history-chart').children[0];
	assert.match(networkSVG.attributes['aria-label'], /Network .*NIC 1/);
	assert.ok(networkSVG.children.some(child => child.attributes.class === 'chart-series'));
	assert.ok(networkSVG.children.every(child => !JSON.stringify(child.attributes).includes('NaN')));
	assert.deepEqual(document.getElementById('stats-history-legend').children.map(child => child.textContent), ['Receive (RX)', 'Transmit (TX)']);
	assert.match(document.getElementById('stats-history-summary').textContent, metric === 'network' ? /B\/s/ : /packets\/s/);
}
setStatsHistoryVisible(false);
statsHistory.record(daemon, [], 16000);
assert.equal(statsHistory.samples.length, 4); // Sampling continues while hidden.
assert.equal(preferences.get(statsHistoryVisibilityKey), '0');
// Restricted localStorage must not break the dashboard.
localStorage.setItem = () => { throw new Error('blocked'); };
localStorage.getItem = () => { throw new Error('blocked'); };
initStatsHistory();
assert.equal(statsHistoryVisible, false);
`
	cmd := exec.Command(node, "-e", script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("statistics history regression: %v\n%s", err, output)
	}
}
