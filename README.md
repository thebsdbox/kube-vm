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

# List running VMs
./kube-vmctl list

# Attach later
./kube-vmctl connect -uid abc123

# Stop VM
./kube-vmctl shutdown -uid abc123
```

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
- `-detach`

Environment:

- `KUBEVM_SOCKET`: default socket path for `kube-vmctl`

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

## Storage and Boot Options

### Block device specs

Block specs currently support:

- local file path or `file://...`
- `http://...` / `https://...` (read-only)
- `mem:<bytes>`

Append `:ro` to force read-only for file/mem specs.

Examples:

```sh
-block ./disk.raw
-block ./cloudinit.iso:ro
-block https://example.com/rootfs.img
-block mem:1048576
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
