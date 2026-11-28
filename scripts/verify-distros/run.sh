#!/usr/bin/env bash
#
# Installs Just Dashboard on each distribution in distros.txt from a fresh
# machine, uses it, restarts the machine and uses it again. This is what the
# "Supported distributions" table in README.md is made from.
#
#   scripts/verify-distros/run.sh routes                             # as yourself: Go's cache
#   sudo env PATH="$PATH" scripts/verify-distros/run.sh setup        # once
#   sudo env PATH="$PATH" scripts/verify-distros/run.sh one debian-13
#   sudo env PATH="$PATH" scripts/verify-distros/run.sh all          # JDV_PARALLEL at a time (3)
#   sudo env PATH="$PATH" scripts/verify-distros/run.sh summary
#   scripts/verify-distros/run.sh compare [baseline]                 # what differs from ubuntu-24.04
#   sudo env PATH="$PATH" scripts/verify-distros/run.sh clean [name]
#
# Each machine is a privileged container booting the release's own systemd as
# PID 1 from its official image, with its own Docker inside (its image stores
# on volumes, as they would be on a disk rather than in overlayfs, and / a
# shared mount, as systemd leaves it on a machine), holding only what a
# minimal install has (systemd, D-Bus, git, the certificate store). Into it go
# a shallow clone of this checkout's HEAD — commit first — and `./install.sh`,
# answered the way a person answers it: install Docker, SSH tunnel, an admin
# with a chosen password, terminal on, no forced two-factor, no account link.
# Then check.mjs signs in, opens every page's data and runs a command in the
# web terminal; the machine is restarted and that is done again.
#
# What the machines share with the host: its kernel, and a pull-through cache
# of Docker Hub (so two dozen builds stay under Hub's anonymous pull limit;
# the images are the same ones). just-dashboard.com resolves to nowhere inside
# them, so test installs send no usage report and link to no account.
#
# Needs: Docker, Node 22+ and Go (for the route list) on the host, about 10 GB
# of disk per machine running at once, and 3 GB of memory per build. A machine
# that fails leaves its compose and journal logs beside its results and is
# removed, unless JDV_KEEP=1 leaves it running.

set -euo pipefail

HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(git -C "$HERE" rev-parse --show-toplevel)"
RESULTS="${JDV_RESULTS:-$REPO/.verify-distros}"
NETWORK=jdv
SUBNET=10.231.0.0/24
MIRROR_IP=10.231.0.2
NODE="${JDV_NODE:-$(command -v node || true)}"
GO="${JDV_GO:-$(command -v go || true)}"

die() { printf 'error: %s\n' "$*" >&2; exit 1; }
log() { printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*"; }

distro_line() {
	grep -vE '^\s*(#|$)' "$HERE/distros.txt" | awk -F'|' -v n="$1" '{k = $1; gsub(/ /, "", k)} k == n {print; found = 1} END {exit !found}'
}
field() { awk -F'|' -v i="$2" '{gsub(/^ +| +$/, "", $i); print $i}' <<<"$1"; }
names() { grep -vE '^\s*(#|$)' "$HERE/distros.txt" | awk -F'|' '{gsub(/ /, "", $1); print $1}'; }

setup() {
	docker network inspect "$NETWORK" >/dev/null 2>&1 ||
		docker network create --subnet "$SUBNET" "$NETWORK" >/dev/null
	if ! docker container inspect jdv-mirror >/dev/null 2>&1; then
		docker run -d --name jdv-mirror --restart unless-stopped \
			--network "$NETWORK" --ip "$MIRROR_IP" \
			-e REGISTRY_PROXY_REMOTEURL=https://registry-1.docker.io \
			-v jdv-mirror:/var/lib/registry registry:2 >/dev/null
	fi
	log "network $NETWORK and the Docker Hub cache at $MIRROR_IP:5000 are up"
}

# The routes check.mjs opens: every GET the router mounts, listed by an
# env-gated test so the list is the code's, not a copy.
routes() {
	mkdir -p "$RESULTS"
	[ -n "$GO" ] || die "go is needed to list the routes (set JDV_GO)"
	(cd "$REPO/backend" && JD_LIST_GET_ROUTES="$RESULTS/get-routes.txt" "$GO" test ./internal/api -run '^TestListGETRoutes$' -count=1 >/dev/null)
	log "$(grep -vc '{' "$RESULTS/get-routes.txt") GET routes without parameters"
}

machine_image() {
	local name="$1" image="$2" family="$3" dir="$RESULTS/$1/image" extra=""
	mkdir -p "$dir"
	local prepare init='CMD ["/usr/lib/systemd/systemd"]'
	case "$family" in
	apt) prepare='apt-get update && apt-get install -y --no-install-recommends systemd systemd-sysv dbus git ca-certificates && apt-get clean && rm -rf /var/lib/apt/lists/*' ;;
	dnf) prepare='dnf install -y systemd dbus git ca-certificates && dnf clean all' ;;
	zypper) prepare='zypper --non-interactive install systemd dbus-1 git-core ca-certificates && zypper clean -a' ;;
	pacman) prepare='pacman -Syu --noconfirm --needed systemd dbus git ca-certificates && pacman -Scc --noconfirm' ;;
	refuse-apk) prepare='apk add --no-cache bash git ca-certificates'; init='CMD ["sleep", "infinity"]' ;;
	refuse-apt) prepare='apt-get update && apt-get install -y --no-install-recommends git ca-certificates && apt-get clean'; init='CMD ["sleep", "infinity"]' ;;
	*) die "unknown family $family" ;;
	esac
	if [ -f "$HERE/prepare/$name.sh" ]; then
		cp "$HERE/prepare/$name.sh" "$dir/prepare.sh"
		extra='COPY prepare.sh /tmp/prepare.sh
RUN sh /tmp/prepare.sh && rm /tmp/prepare.sh'
	fi
	# systemd makes / a shared mount when it boots a machine, and skips that
	# in a container; the backend's /:/host:rslave needs it, so each boot
	# does what a real one does.
	cat >"$dir/shared-root.service" <<'EOF'
[Unit]
Description=Make / a shared mount, as systemd does on a machine
DefaultDependencies=no
Before=docker.service local-fs.target

[Service]
Type=oneshot
ExecStart=/bin/sh -c 'mount --make-rshared /'

[Install]
WantedBy=local-fs.target
EOF
	local unit='COPY shared-root.service /etc/systemd/system/shared-root.service
RUN mkdir -p /etc/systemd/system/local-fs.target.wants && ln -sf /etc/systemd/system/shared-root.service /etc/systemd/system/local-fs.target.wants/'
	case "$family" in refuse-*) unit="" ;; esac
	cat >"$dir/Dockerfile" <<EOF
FROM $image
ENV container=docker DEBIAN_FRONTEND=noninteractive
RUN $prepare
$extra
$unit
STOPSIGNAL SIGRTMIN+3
$init
EOF
	docker build -q -t "jdv-machine:$name" "$dir" >/dev/null
}

# on <name> <command> runs a command on the machine as root.
on() { local n="$1"; shift; docker exec -i "jdv-$n" bash -c "$*"; }

wait_booted() {
	local name="$1" state=""
	for _ in $(seq 1 90); do
		state="$(docker exec "jdv-$name" systemctl is-system-running 2>/dev/null || true)"
		case "$state" in running|degraded) return 0 ;; esac
		sleep 2
	done
	log "$name: systemd says '$state' after three minutes"
	return 1
}

wait_backend() {
	local name="$1" port
	port="$(on "$name" "grep -E '^JD_BACKEND_PORT=' /root/Just-Dashboard/.env | cut -d= -f2")"
	for _ in $(seq 1 150); do
		on "$name" "curl -fsS --max-time 3 http://127.0.0.1:$port/healthz" >/dev/null 2>&1 && return 0
		sleep 2
	done
	return 1
}

check() {
	local name="$1" phase="$2" port pid
	port="$(on "$name" "grep -E '^JD_PORT=' /root/Just-Dashboard/.env | cut -d= -f2")"
	pid="$(docker inspect -f '{{.State.Pid}}' "jdv-$name")"
	# The host's node, in the machine's network namespace: the dashboard listens
	# on the machine's loopback, and the machine has no node of its own.
	nsenter -t "$pid" -n "$NODE" "$HERE/check.mjs" "$port" admin "$(cat "$RESULTS/$name/password")" \
		"$RESULTS/get-routes.txt" "$phase" >"$RESULTS/$name/check-$phase.json" 2>"$RESULTS/$name/check-$phase.err"
}

one() {
	local name="$1" line image family out
	line="$(distro_line "$name")" || die "$name is not in distros.txt"
	image="$(field "$line" 2)"; family="$(field "$line" 3)"
	out="$RESULTS/$name"
	[ -n "$NODE" ] || die "node is needed (set JDV_NODE)"
	[ -s "$RESULTS/get-routes.txt" ] || routes
	clean "$name" >/dev/null 2>&1 || true
	mkdir -p "$out"
	rm -f "$out"/*.json "$out"/*.err "$out"/*.log "$out"/verdict "$out"/facts
	local started=$SECONDS
	log "$name: preparing the machine from $image"
	machine_image "$name" "$image" "$family"
	docker run -d --name "jdv-$name" --hostname "jdv-$name" --privileged --cgroupns=private \
		--network "$NETWORK" --add-host just-dashboard.com:0.0.0.0 \
		--tmpfs /run --tmpfs /run/lock \
		-v "jdv-$name-docker:/var/lib/docker" -v "jdv-$name-containerd:/var/lib/containerd" \
		"jdv-machine:$name" >/dev/null
	case "$family" in refuse-*) ;; *) wait_booted "$name" || { echo "fail: machine did not boot" >"$out/verdict"; keep_or_clean "$name"; return 1; } ;; esac

	# The harness's one addition: Docker Hub through the cache (see the header).
	on "$name" "mkdir -p /etc/docker && printf '{\"registry-mirrors\":[\"http://$MIRROR_IP:5000\"],\"insecure-registries\":[\"$MIRROR_IP:5000\"]}\n' >/etc/docker/daemon.json"

	local clone="$RESULTS/$name/clone"
	rm -rf "$clone"
	git clone -q --depth 1 --no-local "file://$REPO" "$clone"
	git -C "$clone" remote set-url origin https://github.com/JustDashboard/Just-Dashboard.git
	docker cp "$clone" "jdv-$name:/root/Just-Dashboard" >/dev/null
	rm -rf "$clone"
	git -C "$REPO" rev-parse --short HEAD >"$out/commit"

	local pw
	pw="Jdv-$(head -c 9 /dev/urandom | od -An -tx1 | tr -d ' \n')A1"
	printf '%s' "$pw" >"$out/password"
	log "$name: running install.sh"
	local rc=0
	printf 'y\n2\nadmin\nn\n%s\n%s\ny\nn\n' "$pw" "$pw" |
		timeout 90m docker exec -i "jdv-$name" bash -c 'cd /root/Just-Dashboard && NO_COLOR=1 ./install.sh --no-link' \
		>"$out/install.log" 2>&1 || rc=$?
	{
		echo "os: $(on "$name" '. /etc/os-release; echo "$PRETTY_NAME"')"
		echo "kernel: $(uname -r) (the host's)"
		echo "installer exit: $rc"
		echo "docker: $(on "$name" 'docker --version 2>/dev/null' || echo none)"
		echo "compose: $(on "$name" 'docker compose version --short 2>/dev/null' || echo none)"
		echo "install minutes: $(( (SECONDS - started) / 60 ))"
	} >"$out/facts"
	case "$family" in
	refuse-*)
		if [ "$rc" -ne 0 ] && grep -q 'does not run systemd' "$out/install.log" && ! on "$name" 'command -v docker' >/dev/null 2>&1; then
			echo "refused: stopped before changing anything" >"$out/verdict"
		else
			echo "fail: expected the installer to stop for want of systemd" >"$out/verdict"
		fi
		log "$name: $(cat "$out/verdict")"
		clean "$name" >/dev/null 2>&1 || true
		return 0
		;;
	esac
	if [ "$rc" -ne 0 ] || ! grep -q 'Setup complete' "$out/install.log"; then
		echo "fail: install.sh exited $rc" >"$out/verdict"
		log "$name: $(cat "$out/verdict")"
		keep_or_clean "$name"
		return 1
	fi
	on "$name" 'cd /root/Just-Dashboard && docker compose ps --format "{{.Service}} {{.State}} {{.Health}}"' >"$out/services" 2>&1 || true

	log "$name: using it"
	local first=0 second=0
	check "$name" install || first=$?

	log "$name: restarting the machine"
	docker restart -t 90 "jdv-$name" >/dev/null
	if wait_booted "$name" && wait_backend "$name"; then
		sleep 20 # the frontend and proxy come up after the backend
		check "$name" reboot || second=$?
		on "$name" 'cd /root/Just-Dashboard && docker compose ps --format "{{.Service}} {{.State}} {{.Health}}"' >"$out/services-after-reboot" 2>&1 || true
	else
		second=1
		echo '{"failures":["the dashboard did not come back after a restart"]}' >"$out/check-reboot.json"
	fi
	echo "minutes in all: $(( (SECONDS - started) / 60 ))" >>"$out/facts"

	if [ "$first" -eq 0 ] && [ "$second" -eq 0 ]; then
		echo "pass" >"$out/verdict"
		log "$name: pass"
		clean "$name" >/dev/null 2>&1 || true
	else
		echo "fail: the dashboard did not work (check-*.json)" >"$out/verdict"
		log "$name: $(cat "$out/verdict")"
		keep_or_clean "$name"
		return 1
	fi
}

# keep_or_clean <name> saves what explains a failure and removes the machine,
# or leaves it running for a look with JDV_KEEP=1.
keep_or_clean() {
	local name="$1" out="$RESULTS/$1"
	on "$name" 'cd /root/Just-Dashboard 2>/dev/null && docker compose logs --no-color --tail 300' >"$out/compose.log" 2>&1 || true
	on "$name" 'systemctl --failed --no-pager; journalctl -b --no-pager | tail -n 400' >"$out/journal.log" 2>&1 || true
	if [ "${JDV_KEEP:-0}" = 1 ]; then
		log "$name: machine kept for a look (run.sh clean $name)"
	else
		clean "$name" >/dev/null 2>&1 || true
	fi
}

clean() {
	local list=("$@")
	[ "${#list[@]}" -gt 0 ] || mapfile -t list < <(names)
	for name in "${list[@]}"; do
		docker rm -f "jdv-$name" >/dev/null 2>&1 || true
		docker volume rm "jdv-$name-docker" "jdv-$name-containerd" >/dev/null 2>&1 || true
	done
}

summary() {
	printf '%-22s %-8s %-46s %s\n' distro commit verdict os
	for name in $(names); do
		local out="$RESULTS/$name"
		[ -f "$out/verdict" ] || { printf '%-22s %s\n' "$name" "not run"; continue; }
		printf '%-22s %-8s %-46s %s\n' "$name" "$(cat "$out/commit" 2>/dev/null)" "$(cat "$out/verdict")" \
			"$(sed -n 's/^os: //p' "$out/facts" 2>/dev/null)"
	done
}

case "${1:-}" in routes | summary | compare | "") ;; *)
	[ "$(id -u)" -eq 0 ] || die "run this with sudo (it drives Docker and enters the machines' namespaces)" ;;
esac
case "${1:-}" in
setup) setup ;;
routes) routes ;;
one) [ -n "${2:-}" ] || die "one <name>"; setup; one "$2" ;;
all)
	setup
	[ -s "$RESULTS/get-routes.txt" ] || routes
	shift
	if [ "$#" -gt 0 ]; then printf '%s\n' "$@"; else names; fi |
		xargs -P "${JDV_PARALLEL:-3}" -I{} bash "$HERE/run.sh" one {} || true
	summary
	;;
summary) summary ;;
compare) "$NODE" "$HERE/compare.mjs" "$RESULTS" "${2:-ubuntu-24.04}" ;;
clean) shift; clean "$@" ;;
*) sed -n '3,30p' "$0"; exit 2 ;;
esac
