package webui

import (
	"os/exec"
	"strings"
	"testing"
)

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
const vm = {uid: 'one', started_at: 'first', host_cpu_pct: 20, host_mem_bytes: 4096, storage: [disk]};
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

// Visibility preferences and chart rendering work without a browser chart library.
initStatsHistory();
assert.equal(document.getElementById('stats-history').hidden, true);
assert.equal(document.getElementById('stats-history-toggle').attributes['aria-expanded'], 'false');
statsHistory.record(daemon, [vm], 1000);
statsHistory.record(daemon, [{...vm, storage: [next]}], 6000);
statsHistory.record(daemon, [{...vm, storage: [next]}], 11000);
updateHistorySources();
assert.equal(document.getElementById('stats-history-source').options.length, 3);
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