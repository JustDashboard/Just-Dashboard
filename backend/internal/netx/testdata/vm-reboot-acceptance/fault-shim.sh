#!/bin/sh
#
# Guest-only fault injection for the reboot acceptance. The backend unit puts
# this in front of ip and systemctl. When /run/jd-vm-fault/phase names a
# recovery-journal phase and the durable journal has reached it, the backend's
# main process is killed with SIGKILL before this command runs: the change dies
# between two journal phases exactly as a crashed backend would. Otherwise the
# real tool runs unchanged.

tool=${0##*/}
real=$(PATH=/usr/sbin:/usr/bin:/sbin:/bin; command -v "$tool")
fault=/run/jd-vm-fault/phase
journal=/etc/just-dashboard/network/change.json

if [ -s "$fault" ] && [ -f "$journal" ]; then
	want=$(cat "$fault")
	if grep -q "^  \"phase\": \"$want\"," "$journal"; then
		rm -f "$fault"
		printf '%s %s %s journal=%s\n' "$(date -u +%FT%T.%NZ)" "$tool" "$*" "$want" >/run/jd-vm-fault/fired
		/usr/bin/systemctl kill --kill-whom=main --signal=KILL jd-vm-backend.service
		sleep 30
		exit 137
	fi
fi
exec "$real" "$@"
