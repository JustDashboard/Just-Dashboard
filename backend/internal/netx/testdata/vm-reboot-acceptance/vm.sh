#!/usr/bin/env bash
#
# Disposable QEMU/KVM guest for the network reboot acceptance.
#
#   vm.sh prepare | start | wait | ssh CMD... | put SRC DST | reboot | provision [steps] | stop | cleanup
#
# Only the guest is ever rebooted. The guest gets user-mode networking alone
# (QEMU's slirp, no bridge, tap or host route) and one loopback-only SSH
# forward. QEMU runs as the invoking account with the existing kvm group added
# to that one process through setpriv; host group membership, packages,
# services and network are not changed.
#
# Inputs (environment):
#   JD_VM_ARTIFACTS  writable task directory (overlay, seed, key, logs)
#   JD_VM_PREREQ     verified prerequisites: the cloud image, guest-archives/
#                    and host-tools/ (QEMU, qemu-img, genisoimage)
#   JD_VM_BACKEND    static backend executable built from the source under test
#   JD_VM_SSH_PORT   loopback SSH forward, 43250-43259 (default 43250)

set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
art=${JD_VM_ARTIFACTS:?set JD_VM_ARTIFACTS}
pre=${JD_VM_PREREQ:?set JD_VM_PREREQ}
port=${JD_VM_SSH_PORT:-43250}
image=$pre/ubuntu-24.04-minimal-cloudimg-amd64.img
image_sha256=2f119f50bf29fe40e73b69ae2a736e8210d8146796f036e0324315b6b3a777df
tools=$pre/host-tools
vm=$art/vm
logs=$art/logs
mkdir -p "$vm" "$logs"

[[ $port =~ ^4325[0-9]$ ]] || { echo "JD_VM_SSH_PORT must be within 43250-43259" >&2; exit 2; }

qemu_env=(env "LD_LIBRARY_PATH=$tools/usr/lib/x86_64-linux-gnu")
ssh_opts=(-i "$vm/key" -p "$port" -o UserKnownHostsFile="$vm/known_hosts" -o StrictHostKeyChecking=accept-new
	-o ConnectTimeout=5 -o ServerAliveInterval=5 -o ServerAliveCountMax=3 -o LogLevel=ERROR -o BatchMode=yes)

log() { printf '%s %s\n' "$(date -u +%FT%TZ)" "$*" | tee -a "$logs/vm.log" >&2; }

guest() { ssh "${ssh_opts[@]}" jd@127.0.0.1 "$@"; }

# The four NICs have fixed MACs and PCI slots so their guest names
# (enp0s3..enp0s6) and permanent addresses are the same on every boot.
# Only the first reaches the outside (for the guest's own package fetch and
# SSH); the other three are restricted slirp segments.
qemu_argv() {
	local kvm_gid
	kvm_gid=$(getent group kvm | cut -d: -f3)
	printf '%s\n' sudo -n setpriv --reuid="$(id -u)" --regid="$(id -g)" --groups="$kvm_gid,$(id -g)" -- \
		"${qemu_env[@]}" "$tools/usr/bin/qemu-system-x86_64" \
		-name jd-vm-reboot -machine q35,accel=kvm -cpu host -smp 2 -m 2048 \
		-L "$tools/usr/share/seabios" -L "$tools/usr/share/qemu" \
		-display none -monitor none -serial "file:$logs/serial.log" \
		-smbios type=1,serial=jd-vm-reboot-acceptance \
		-drive "file=$vm/overlay.qcow2,if=none,id=disk0,format=qcow2" -device virtio-blk-pci,drive=disk0,addr=0x7 \
		-drive "file=$vm/seed.iso,if=none,id=seed,format=raw,readonly=on" -device virtio-blk-pci,drive=seed,addr=0x8 \
		-netdev "user,id=n0,hostfwd=tcp:127.0.0.1:$port-:22" -device virtio-net-pci,netdev=n0,mac=52:54:00:4a:44:03,addr=0x3,romfile= \
		-netdev user,id=n1,restrict=on,net=10.0.4.0/24 -device virtio-net-pci,netdev=n1,mac=52:54:00:4a:44:04,addr=0x4,romfile= \
		-netdev user,id=n2,restrict=on,net=10.0.5.0/24 -device virtio-net-pci,netdev=n2,mac=52:54:00:4a:44:05,addr=0x5,romfile= \
		-netdev user,id=n3,restrict=on,net=10.0.6.0/24 -device virtio-net-pci,netdev=n3,mac=52:54:00:4a:44:06,addr=0x6,romfile= \
		-pidfile "$vm/qemu.pid" -daemonize
}

prepare() {
	echo "$image_sha256  $image" | sha256sum -c - >>"$logs/vm.log"
	[ -e "$vm/overlay.qcow2" ] && { echo "overlay already exists" >&2; exit 1; }
	"${qemu_env[@]}" "$tools/usr/bin/qemu-img" create -q -f qcow2 -F qcow2 -b "$image" "$vm/overlay.qcow2" 8G
	"${qemu_env[@]}" "$tools/usr/bin/qemu-img" info "$vm/overlay.qcow2" >>"$logs/vm.log"
	ssh-keygen -q -t ed25519 -N '' -C jd-vm-reboot-acceptance -f "$vm/key"
	local seed=$vm/seed
	mkdir -p "$seed"
	sed "s|@SSH_KEY@|$(cat "$vm/key.pub")|" "$here/user-data" >"$seed/user-data"
	printf 'instance-id: jd-vm-reboot-acceptance\nlocal-hostname: jd-vm-reboot\n' >"$seed/meta-data"
	cp "$here/network-config" "$seed/network-config"
	"${qemu_env[@]}" "$tools/usr/bin/genisoimage" -quiet -output "$vm/seed.iso" -volid cidata -joliet -rock \
		"$seed/user-data" "$seed/meta-data" "$seed/network-config"
	log "prepared overlay, seed and ephemeral key"
}

start() {
	[ -e "$vm/qemu.pid" ] && kill -0 "$(cat "$vm/qemu.pid")" 2>/dev/null && { echo "QEMU already running" >&2; exit 1; }
	mapfile -t argv < <(qemu_argv)
	printf '%q ' "${argv[@]}" >"$logs/qemu-argv.log"
	echo >>"$logs/qemu-argv.log"
	"${argv[@]}"
	log "QEMU started as PID $(cat "$vm/qemu.pid")"
}

wait_ssh() {
	local deadline=$((SECONDS + ${1:-300}))
	until guest true 2>/dev/null; do
		[ $SECONDS -lt $deadline ] || { log "guest SSH did not answer"; return 1; }
		kill -0 "$(cat "$vm/qemu.pid")" || { log "QEMU exited"; return 1; }
		sleep 3
	done
	guest 'cloud-init status --wait >/dev/null 2>&1 || true; systemctl is-system-running --wait >/dev/null 2>&1 || true'
}

boot_id() { guest cat /proc/sys/kernel/random/boot_id; }

# A guest reboot: the same QEMU process resets the machine, so the PID, disk and
# serial log continue. A changed kernel boot ID is the evidence of the reboot.
reboot_guest() {
	local before after
	before=$(boot_id)
	log "rebooting guest (boot $before)"
	guest 'sudo systemctl reboot' || true
	sleep 5
	wait_ssh 300
	after=$(boot_id)
	[ "$after" != "$before" ] || { log "boot ID unchanged after reboot"; return 1; }
	log "guest rebooted (boot $before -> $after)"
	echo "$after"
}

put() { scp -q -i "$vm/key" -P "$port" -o UserKnownHostsFile="$vm/known_hosts" -o BatchMode=yes "$@"; }

# Packages: the verified offline owner closure (NetworkManager/libnm 1.46 and
# the exact kernel extra modules) through dpkg, then nftables and iptables
# from the guest's own signed archive over its user-mode network. The dashboard
# is laid out on the guest as production lays it out on a host: /etc/just-dashboard,
# /var/lib/just-dashboard and the two generated units, run as root outside
# Docker (see the evidence document for that difference).
provision() {
	local stage=/var/tmp/jd-vm-acceptance steps=${1:-packages,owners,backend}
	guest "sudo install -d -m 0755 -o jd $stage $stage/debs"
	if [[ $steps == *packages* ]]; then
		put "$pre"/guest-archives/*.deb "jd@127.0.0.1:$stage/debs/"
		guest "cd $stage/debs && sha256sum *.deb" >"$logs/guest-debs.sha256"
	fi
	local files=("$here/guest.py" "$here/fault-shim.sh" "$here/jd-vm-backend.service" "$here/guest-setup.sh")
	[[ $steps == *backend* ]] && files+=("${JD_VM_BACKEND:?set JD_VM_BACKEND}")
	put "${files[@]}" "jd@127.0.0.1:$stage/"
	guest "sudo bash $stage/guest-setup.sh $stage $steps" 2>&1 | tee -a "$logs/provision.log"
}

stop() {
	local pid
	pid=$(cat "$vm/qemu.pid" 2>/dev/null) || { log "no QEMU PID file"; return 0; }
	if kill -0 "$pid" 2>/dev/null; then
		guest 'sudo systemctl poweroff' 2>/dev/null || true
		for _ in $(seq 1 60); do kill -0 "$pid" 2>/dev/null || break; sleep 1; done
		if kill -0 "$pid" 2>/dev/null; then
			log "QEMU $pid still running after poweroff; sending TERM"
			kill "$pid"
			for _ in $(seq 1 20); do kill -0 "$pid" 2>/dev/null || break; sleep 1; done
		fi
	fi
	kill -0 "$pid" 2>/dev/null && { log "QEMU $pid did not stop"; return 1; }
	log "QEMU $pid stopped"
	rm -f "$vm/qemu.pid"
}

cleanup() {
	[ -e "$vm/qemu.pid" ] && kill -0 "$(cat "$vm/qemu.pid")" 2>/dev/null && { echo "stop QEMU first" >&2; exit 1; }
	rm -rf "$vm/overlay.qcow2" "$vm/seed.iso" "$vm/seed" "$vm/key" "$vm/key.pub" "$vm/known_hosts"
	log "removed overlay, seed and ephemeral key; logs kept in $logs"
}

case ${1:-} in
prepare) prepare ;;
start) start ;;
wait) wait_ssh "${2:-300}" ;;
ssh) shift; guest "$@" ;;
put) shift; put "$@" ;;
reboot) reboot_guest ;;
provision) provision "${2:-}" ;;
stop) stop ;;
cleanup) cleanup ;;
*) sed -n '3,20p' "$0"; exit 2 ;;
esac
