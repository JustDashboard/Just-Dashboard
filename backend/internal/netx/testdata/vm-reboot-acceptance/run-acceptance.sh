#!/usr/bin/env bash
#
# The complete reboot acceptance against a provisioned guest (vm.sh prepare,
# start, wait, provision, reboot). Every guest phase writes one log under
# $JD_VM_ARTIFACTS/logs/phases; a failing phase is recorded and the run goes on,
# so one result never hides the next. The summary is the last line of each log.
#
#   run-acceptance.sh [first-step]   (resume from a numbered step)

set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
logs=${JD_VM_ARTIFACTS:?set JD_VM_ARTIFACTS}/logs/phases
mkdir -p "$logs"
from=${1:-1}
step=0
failed=0

run() {
	step=$((step + 1))
	[ "$step" -lt "$from" ] && return 0
	local name
	name=$(printf '%02d-%s' "$step" "$(echo "$*" | tr ' ' '-')")
	if [ "$1" = reboot ]; then
		"$here/vm.sh" reboot >"$logs/$name.log" 2>&1
	else
		"$here/vm.sh" ssh "sudo /usr/local/lib/jd-vm-acceptance/guest.py $*" >"$logs/$name.log" 2>&1
	fi
	local rc=$?
	[ $rc -ne 0 ] && failed=$((failed + 1))
	printf '%s %-48s exit %d\n' "$(date -u +%FT%TZ)" "$name" "$rc" | tee -a "$logs/summary.log"
}

# Baseline and C001: the owned object set across a reboot.
run baseline
run c001-apply
run reboot
run c001-verify
run c004-ownership
run c002-cancel 1

# F4/C002: backend death between journal phases. Timer cases first (no
# reboot), then the same phases with a reboot before the ninety-second
# deadline, so only the boot recovery unit can act.
for phase in awaiting_confirmation runtime_applied persisted; do
	run f4-kill "$phase" 10
	run f4-timer "$phase-10"
done
for phase in awaiting_confirmation runtime_applied persisted; do
	run f4-kill "$phase" 11
	run reboot
	run f4-boot "$phase-11"
done

# P7: runtime drift healed by a boot; boot-input drift reported after a boot,
# repaired through review and confirmation, then restored by the next boot;
# a systemd-networkd restart.
run p7-runtime-drift
run reboot
run p7-runtime-reboot
run p7-file-drift
run reboot
run p7-file-verify
run reboot
run p7-repaired-verify
run p7-daemon-restart

# P11: three real native owners. Confirmed edits persist across a reboot;
# unconfirmed edits are recovered by the real timer and by the boot unit.
run p11-read
for owner in netplan-networkd netplan-nm networkd; do
	run p11-confirm "$owner"
done
run reboot
for owner in netplan-networkd netplan-nm networkd; do
	run p11-persist "$owner"
done
# The first networkd reload after a boot, recorded on its own; the
# transaction cases below settle networkd first.
run p11-first-edit
for owner in netplan-networkd netplan-nm networkd; do
	run p11-kill "$owner" 17
	run p11-timer "$owner-17"
done
for owner in netplan-networkd netplan-nm networkd; do
	run p11-kill "$owner" 18
	run reboot
	run p11-boot "$owner-18"
done

# P14: explicit download SQM restored by the packaged helper at boot.
run p14-apply
run reboot
run p14-verify

# C027 VRF, last because it swaps in the routing branch's server: traffic
# through a real VRF, then that package's l3mdev rule, VRF reading and boot
# restoration (needs `vm.sh provision routing`).
run vrf-kernel
run vrf-routing
run reboot
run vrf-routing-verify

"$here/vm.sh" collect
echo "failed steps: $failed" | tee -a "$logs/summary.log"
[ "$failed" -eq 0 ]
