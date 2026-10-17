# kube-vm

kube-vm is a Linux KVM-based VM runner written in Go. It supports:

- direct local VM boot
- daemon mode with a Unix socket control API
- a CLI client (`kube-vmctl`)
- an optional web frontend/API with token auth
- virtio console, block, and network devices
- detached VM start and later console attach

## Requirements

- Linux amd64 host
- `/dev/kvm` available
- Go 1.24+
- for TAP networking: permissions to create/use TAP interfaces
- for NAT networking: `ip` and `iptables` available on host
- for `build-disk`: Docker + loop mount permissions (`sudo`)

## Build

```sh
make build
```

This creates:

- `./kube-vm`
- `./kube-vmctl`

## Make Targets

- `make build`: build daemon and client
- `make build-vm`: build `kube-vm`
- `make build-client`: build `kube-vmctl`
- `make clean`: remove binaries
- `make demo`: start demo screen session from `kube-vm-screen.rc`
- `make exit-demo`: stop demo screen session
- `make build-disk DOCKER_TAG=<image>`: create ext4 disk image from Docker image contents

`build-disk` variables:

- `DOCKER_TAG` required
- `DOCKERFILE` optional (if set, image is built first)
- `DISK_IMG` default `kube-vm-disk.img`
- `MOUNT_DIR` default `/mnt`

Example:

```sh
make build-disk DOCKER_TAG=my-rootfs DOCKERFILE=./Dockerfile DISK_IMG=guest.img
```

## Running kube-vm Directly

Local mode (no daemon) example:

```sh
./kube-vm -server=false \
  -kernel ./bzImage \
  -initrd ./initrd.cpio.gz \
  -cmdline "console=hvc0 reboot=t"
```

Useful direct flags:

- `-mem` MiB (default `1024`)
- `-cpu` vCPUs (default `1`)
- `-kernel` kernel path/URL
- `-initrd` initrd path/URL
- `-cmdline` guest kernel cmdline
- `-firmware` firmware image path/URL (experimental firmware boot)
- `-iso` ISO path/URL (attached as read-only block)
- `-block` repeatable block device spec (supports `:ro`)
- `-cow-block BASE=OVERLAY` repeatable writable copy-on-write block device
- `-tap` TAP interface name
- `-nat` auto-create TAP with host NAT (Linux only, mutually exclusive with `-tap`)
- `-vhost-net` request vhost-net acceleration for TAP networking (currently rejected; requires `-tap` and cannot be combined with `-nat`)

## Daemon Mode

Start daemon and control socket:

```sh
./kube-vm -server -socket /tmp/kube-vm.sock
```

Control socket default is `/tmp/kube-vm.sock`.

## kube-vmctl

`kube-vmctl` controls the daemon over the Unix socket.

General:

```sh
./kube-vmctl -socket /tmp/kube-vm.sock <command> [flags]
```

Commands:

- `status` / `info`
- `list`
- `start` / `run`
- `connect` / `attach`
- `shutdown` / `stop` / `quit`

Examples:

```sh
# Start and attach console immediately
./kube-vmctl start -kernel ./bzImage -initrd ./initrd.cpio.gz

# Start detached
./kube-vmctl start -detach -kernel ./bzImage -initrd ./initrd.cpio.gz

# Start with a writable copy-on-write disk overlay
./kube-vmctl start -detach \
  --kernel ./bzImage \
  --initrd ./initrd.cpio.gz \
  --cow-block /var/lib/kube-vm/base.img=/var/lib/kube-vm/vm1.overlay

# List running VMs
./kube-vmctl list

# Attach later
./kube-vmctl connect -uid abc123

# SSH to a NATed guest by VM UID
./kube-vmctl ssh abc123

# Run a remote command against a guest
./kube-vmctl ssh 10.0.2.15 -- uname -a

# Pipe a local script into the guest shell
./kube-vmctl ssh abc123 -- sh -s < setup.sh

# Supply a password non-interactively for a script-driven SSH command
KUBEVM_SSH_PASSWORD=secret ./kube-vmctl ssh -l root abc123 -- sh -s < setup.sh

# Auto-accept a first-seen host key for non-interactive runs
KUBEVM_SSH_ACCEPT_NEW_HOSTS=1 KUBEVM_SSH_PASSWORD=secret ./kube-vmctl ssh -l root abc123 -- sh -s < setup.sh

# Disable SSH host key verification entirely
KUBEVM_SSH_IGNORE_HOST_KEY=1 KUBEVM_SSH_PASSWORD=secret ./kube-vmctl ssh -l root abc123

# Stop VM
./kube-vmctl shutdown -uid abc123
```

Existing-VM commands (`stop`, `pause`, `resume`, `status`, `examine`, `console`,
`ssh`, `save`, and `restore`) accept a full UID or a unique leading prefix,
either positionally or through `--uid` where supported. For example,
`kube-vmctl stop f` targets the only VM whose UID begins with `f`. If several
UIDs match, the command lists the matches and refuses to act until more
characters are supplied. An exact UID always wins over longer prefix matches.
New UIDs supplied to `start` or template launch commands are used literally.

Start-time flags accepted by `kube-vmctl`:

- `-uid` fixed VM UID (optional)
- `-firmware`
- `-kernel`
- `-initrd`
- `-iso`
- `-cmdline`
- `-mem`
- `-cpu`
- `-tap`
- `-nat` (Linux only, mutually exclusive with `-tap`)
- `-vhost-net` (requires `-tap`; currently rejected)
- `-block` (repeatable, supports `:ro`)
- `--cow-block BASE=OVERLAY` (repeatable; adds a writable `cow:` block device)
- `-detach`

Environment:

- `KUBEVM_SOCKET`: default socket path for `kube-vmctl`

SSH:

- `kube-vmctl ssh <uid-or-ip>` resolves a VM UID via `examine` and uses the guest NAT IP
- use `-l/--user` to select the SSH username and `-p/--port` to override port `22`
- use `--insecure-ignore-host-key` or `KUBEVM_SSH_IGNORE_HOST_KEY=1` to skip SSH host key verification entirely
- if no remote command is provided, `kube-vmctl ssh` opens an interactive shell with a PTY when run from a terminal
- pass a remote command after `--` to run commands or pipe a script over standard input
- SSH auth tries agent and local keys first, then falls back to `KUBEVM_SSH_PASSWORD` or an interactive `/dev/tty` password prompt
- unknown host keys are confirmed on first connect and written to `~/.ssh/known_hosts`; set `KUBEVM_SSH_ACCEPT_NEW_HOSTS=1` for non-interactive acceptance

## VM IDs and Console Sockets

- VM UIDs are 6 characters if auto-generated.
- Per-VM console socket path is:

```text
<socket>.<uid>.console
```

Example with default socket:

```text
/tmp/kube-vm.sock.abc123.console
```

## VM checkpoints: save, resume, and move hosts

New **version 3** checkpoints save guest RAM, vCPU registers and extended state,
MSRs/CPUID, interrupt controllers, timers/clock, UART registers, virtio registers,
packed-ring cursors, and logical contents of every disk (including COW, RAM disks,
and read-only ISO media). Saving pauses guest CPUs and quiesces device I/O while
capturing RAM and copying disks, then preserves the previous running/paused state.
This is a full checkpoint, not an incremental snapshot or live migration: large
disks can cause significant downtime and require equivalent additional space.

```sh
# Save a running VM. Paths are on the daemon host.
./kube-vmctl save --uid vm1 --path /var/lib/kube-vm/checkpoints/vm1.snap

# Inspect metadata, the sibling storage directory, sizes and disk checksums.
./kube-vmctl snapshot inspect /var/lib/kube-vm/checkpoints/vm1.snap

# Start a NEW VM from the checkpoint; no kernel/initrd/base image is needed.
./kube-vmctl start --uid resumed --detach \
  --snapshot /var/lib/kube-vm/checkpoints/vm1.snap

# Or resume as a standalone VM, without a daemon.
./kube-vm --snapshot /var/lib/kube-vm/checkpoints/vm1.snap

# Replace an existing VM with its saved checkpoint.
./kube-vmctl restore --uid vm1 --path /var/lib/kube-vm/checkpoints/vm1.snap
```

Create the checkpoint's parent directory first. Saving produces **two artifacts**:
the compressed state file and a uniquely named sibling storage directory shown
as `storage_dir` by `snapshot inspect`. Keep both, with the same relative layout,
when copying to another host. The original kernel, initrd, disk base and overlays
are not required at the destination. Disk sizes and SHA-256 hashes are checked
before restore; do not edit the checkpoint's disk assets.

Each restore creates a private `.kube-vm-restore-*` directory beside the checkpoint
and runs against those disk copies, never the saved assets. These directories hold
persistent guest changes and are **not automatically deleted**. VM details expose
their disk paths. Re-saving an existing checkpoint publishes new uniquely named
assets; old asset directories are retained for safety and can be removed manually
when no checkpoint references them. Protect/transfer all artifacts as sensitive
data: RAM and disks may contain credentials; encryption is not supplied.

**Restore replaces the current VM**, including its disks, with the saved state.
Current changes are not carried over; save them separately first. Existing
console sessions disconnect and must reconnect. In the dashboard use **Details &
actions → Save snapshot / Restore snapshot**, or **New VM → Advanced → Resume from
checkpoint** to boot a new instance. Saved memory/CPU/network settings override
ordinary launch-form and CLI boot defaults.

### Host and device compatibility

- Destination must run compatible Linux/amd64 KVM and this checkpoint format.
  CPU execution features, XSAVE layout, and KVM capabilities are checked
  conservatively. Different physical cache geometry or APIC IDs are permitted,
  but arbitrary Intel/AMD/model/kernel changes are not promised. TSC frequency
  differences require supported KVM scaling. Test target hosts before relying on
  checkpoints for recovery or migration.
- TAP/NAT backends are recreated. Saved NAT identity is retained even if the new
  VM UID differs. **Stop the original before resuming the copy on a shared
  network** to avoid duplicate IP/network identities. A TAP-mode checkpoint can
  use `--tap <destination-interface>`; this does not change its network mode.
  Destination permissions, routing, and firewall configuration must be suitable.
- Host-side NAT connection tracking, external TCP connections, shared console
  sessions, and host descriptors are not checkpointed. Connections may need
  re-establishing. Live VSock and vhost-net checkpoints are explicitly unsupported.
- Newly booted guests do not advertise nested VMX/SVM or supervisor XSAVES state,
  which are not supported by this format. Already-running guests exposing these
  features may be rejected when saving; restart them with the updated VMM first.
- Old version 1/2 files remain inspectable but **cannot be safely resumed**:
  they omitted essential CPU, device-queue, and disk state. Create a new checkpoint
  with the updated VMM rather than attempting a partial restore.
- Only restore trusted checkpoints. Disk hashes detect accidental corruption,
  not authenticity; checkpoints are neither signed nor encrypted. In-memory state
  of remote services is outside the VM and cannot be rolled back with the guest.

## Storage and Boot Options

### Storage statistics

`GET /api/v1/vms` includes a `storage` array for each VM with block devices.
The same array is included in control API VM details (`examine`). Entries follow
device attachment order, including ISO devices, and contain:

- `spec`, `capacity_bytes`, `read_only`: backing specification and device capacity.
- `read_ops`, `write_ops`, `flush_ops`: attempted guest requests, counted once per
  request rather than once per scatter/gather segment.
- `read_bytes`, `write_bytes`: actual transferred bytes, including partial I/O.
- `errors`: failed or unsupported requests with a valid request/status layout.
- `in_flight`: requests currently being processed, useful when I/O stalls.
- `completed_ops`: processed requests, including failures.
- `io_time_ns`, `max_io_time_ns`: total and maximum completed-request processing
  time in nanoseconds, including flush and completion notification time.

Counters reset when a new device is created and are sampled without storage
locks, so snapshots can be slightly inconsistent while requests complete.
They describe guest-visible block I/O, not filesystem used/free space or physical
host I/O (for example, COW write amplification is not included).

The dashboard opens with responsive VM cards containing essential resources,
state, and networking. Network-enabled VM cards show RX/TX byte totals and live
throughput; click the rate summary to open that VM's network graph. The detailed
network view also shows per-interface byte and packet rates.
**Console** connects directly; **Details & actions**
reveals boot paths, errors, pause/resume, snapshots, and stop controls. Stopping
a VM requires confirmation.

The **Console** panel sits directly below **Virtual machines** and starts
expanded. Its **− / +** header control collapses or expands the view without
disconnecting or stopping console output collection. Opening a console from a
VM card (including auto-connect and shared links) expands the panel again.

Statistics are hidden by default. Open **Statistics** in the toolbar for daemon
metrics, or a VM's **Statistics** button for its host resources, network counters,
and storage activity. Per-disk statistics include totals, capacity, read-only
status, errors, pending I/O, and average/maximum latency. Throughput and IOPS are
calculated between dashboard refreshes; the first sample has no rate yet.

The **Show History / Hide History** button inside the **Statistics explorer**
toggles a historical graph. A VM's **Statistics** button selects that VM and
opens its graph. Graph visibility is remembered in browser local storage when
available; the explorer itself remains collapsed on a fresh page load.

**New VM** opens the launch form, with less common settings under **Advanced
options**. The **Configuration library** and **Connection settings** can also
be expanded when needed; an explicitly saved configuration-library visibility
preference is retained.

- Choose server/all VMs, an individual VM, an individual disk, or a network
  interface as the source.
- Plot storage read/write throughput, IOPS, interval-average latency, pending
  requests, errors per second, network RX/TX throughput (bytes/sec), network
  RX/TX packet rates (packets/sec), VM host CPU, or memory.
- Network graphs aggregate available NIC rates for a VM or the entire fleet,
  or show one selected interface. RX/TX follows the host-side NIC counters:
  RX is traffic received from the backend for the guest; TX is traffic sent
  by the guest. Rates need two samples; restarts, replaced interfaces, and
  counter resets start a new baseline rather than producing spikes.
- Disk sources have no network metrics; NIC sources have no storage, CPU,
  or memory metrics. Server memory means daemon heap, while VM memory means
  assigned memory, not guest filesystem usage.
- Select a 5-minute, 15-minute, or one-hour window. Up to 720 samples from the
  last hour are retained in the open tab, including while the graph is hidden.
- History starts when the page opens and is cleared on reload; it is not a
  persistent server-side metrics archive. Missing samples and counter resets
  create gaps rather than negative rates or restart spikes.

### Block device specs

Block specs currently support:

- local file path or `file://...`
- `http://...` / `https://...` (read-only)
- `mem:<bytes>`

Append `:ro` to force read-only for file/mem specs.

Copy-on-write block specs are also supported through the canonical `cow:` form:

- `cow:/path/base.img?overlay=/path/vm.overlay`

This keeps the base image unchanged and redirects guest writes into the overlay file.

#### Recovering after guest I/O or filesystem errors

- Stop write-heavy workloads if the guest reports ext4 checksum errors. Do not
  keep stress-testing a filesystem that may already be damaged.
- Shut down the VM and make a backup before attempting filesystem repair. Never
  run a filesystem repair tool against an image that is attached to a running VM
  or mounted on the host.
- For a raw ext4 image, check and repair an offline copy with an ext4 filesystem
  checker. Partitioned images require checking the appropriate partition, not
  the entire disk image.
- A kube-vm COW overlay is a container, not a raw ext4 image. Do not run an ext4
  checker directly on the overlay. Preserve both the base and overlay for recovery;
  for a disposable VM, test with a known-good base and a new overlay instead.
- Rebuild kube-vm and restart the VM after block-device code changes. Existing
  processes do not pick up source changes, and fixes do not repair prior damage.
- Retest on a disposable, known-good image. Preserve guest kernel errors and host
  kube-vm logs if I/O still stalls; filesystem damage alone does not prove that
  the host storage device is failing.

Examples:

```sh
-block ./disk.raw
-block ./cloudinit.iso:ro
-block https://example.com/rootfs.img
-block mem:1048576
-block 'cow:/var/lib/kube-vm/base.img?overlay=/var/lib/kube-vm/vm1.overlay'
```

### ISO

Attach ISO as read-only block:

```sh
./kube-vmctl start -kernel ./bzImage -initrd ./initrd.cpio.gz -iso ./guest.iso
```

### Firmware (Experimental)

Firmware mode boots from `-firmware` and ignores `-kernel` / `-initrd`.

```sh
./kube-vmctl start -firmware ./bios.bin -iso ./guest.iso
```

## Networking (TAP)

Attach virtio-net backed by TAP:

```sh
./kube-vmctl start -kernel ./bzImage -initrd ./initrd.cpio.gz -tap tap0
```

If TAP does not exist, kube-vm attempts to create it via `/dev/net/tun`.

`-vhost-net` is accepted by both `kube-vm` and `kube-vmctl`, but current builds reject it at runtime because kernel vhost queue wiring has not been implemented yet.

## Networking (NAT)

Enable Linux host-side NAT with automatic TAP creation:

```sh
./kube-vmctl start -kernel ./bzImage -initrd ./initrd.cpio.gz -nat
```

NAT mode configures:

- per-VM TAP interface
- host IPv4 forwarding
- `iptables` forwarding and masquerade rules

Notes:

- `-nat` and `-tap` are mutually exclusive.
- NAT mode runs a per-VM DHCP service and hands out one lease (`nat_guest_ip`).
- Guest IP hint and gateway are exposed in VM stats (`nat_guest_ip`, `nat_gateway`, `nat_subnet`).

## Web Frontend and API

Enable web UI/API:

```sh
./kube-vm -server -web-host 127.0.0.1 -web-port 9090
```

Expose externally:

```sh
./kube-vm -server -web-host 0.0.0.0 -web-port 9090
```

Enable token auth (recommended when not localhost-only):

```sh
./kube-vm -server -web-host 0.0.0.0 -web-port 9090 -web-token "change-me"
```

Open:

```text
http://<host>:<port>
```

Web features:

- start/stop VM
- list VM state
- configure a copy-on-write base image and overlay file from the Start VM form
- daemon stats
- per-VM host metrics (`host_cpu_pct`, `host_cpu_sec`, `host_mem_bytes`)
- web console attach/input/close

Auth accepted by API when `-web-token` is set:

- `Authorization: Bearer <token>`
- `X-Auth-Token: <token>`
- query parameter `?token=<token>`

## Web API (Current)

- `GET /api/v1/vms`
- `POST /api/v1/vms/start`
- `POST /api/v1/vms/stop?uid=<uid>`
- `GET /api/v1/stats`
- `POST /api/v1/console/open?uid=<uid>`
- `GET /api/v1/console/poll?id=<session_id>`
- `POST /api/v1/console/input?id=<session_id>`
- `POST /api/v1/console/close?id=<session_id>`

## Notes

- shutdown paths are defensive and include panic recovery in API and VM close wrappers to avoid daemon crashes from stop requests.
- the firmware path is experimental and not a full PC BIOS/UEFI platform emulation.
- this project currently focuses on Linux amd64 hosts.

## Troubleshooting

- `kube-vmctl: vm: instance is not running`: check `kube-vmctl list` for valid UID.
- web API `unauthorized`: provide token matching `-web-token`.
- KVM open/compat errors: verify host virtualization support and `/dev/kvm` access.
- TAP failures: verify privileges and host network policy.
