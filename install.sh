#!/usr/bin/env bash
#
# One-command setup for Just Dashboard.
#
#   git clone https://github.com/Wayy01/Just-Dashboard.git
#   cd Just-Dashboard && sudo ./install.sh
#
# It asks a handful of questions, writes .env, builds the stack and leaves you
# with a dashboard you can actually reach. The questions that matter are about
# reachability: this thing is root-equivalent, so where it listens is the most
# consequential decision in the whole install, and the script will not make it
# quietly on your behalf.

set -euo pipefail

# ── output ──────────────────────────────────────────────────────────────────

if [ -t 1 ] && [ "$(tput colors 2>/dev/null || echo 0)" -ge 8 ]; then
	BOLD=$(tput bold); DIM=$(tput dim); RESET=$(tput sgr0)
	BLUE=$(tput setaf 4); GREEN=$(tput setaf 2); YELLOW=$(tput setaf 3); RED=$(tput setaf 1)
else
	BOLD=""; DIM=""; RESET=""; BLUE=""; GREEN=""; YELLOW=""; RED=""
fi

say()  { printf '%s\n' "$*"; }
step() { printf '\n%s==>%s %s%s%s\n' "$BLUE" "$RESET" "$BOLD" "$*" "$RESET"; }
ok()   { printf '  %s✓%s %s\n' "$GREEN" "$RESET" "$*"; }
warn() { printf '  %s!%s %s\n' "$YELLOW" "$RESET" "$*"; }
die()  { printf '\n%serror:%s %s\n' "$RED" "$RESET" "$*" >&2; exit 1; }

# Prompts read plain stdin rather than /dev/tty. This script requires a cloned
# repository to run in, so it is never piped from curl — and reading stdin is
# what lets the whole flow be exercised non-interactively in a test.
#
# ask <prompt> <default> -> echoes the answer
ask() {
	local prompt="$1" default="${2:-}" reply
	if [ -n "$default" ]; then
		read -r -p "  $prompt [$default]: " reply || true
		printf '%s' "${reply:-$default}"
	else
		read -r -p "  $prompt: " reply || true
		printf '%s' "$reply"
	fi
}

# yes_no <prompt> <default y|n>
yes_no() {
	local prompt="$1" default="$2" reply
	local hint="y/N"; [ "$default" = "y" ] && hint="Y/n"
	while true; do
		read -r -p "  $prompt [$hint]: " reply || true
		reply="${reply:-$default}"
		case "${reply,,}" in
			y|yes) return 0 ;;
			n|no)  return 1 ;;
			*) say "  please answer y or n" ;;
		esac
	done
}

# ── preflight ───────────────────────────────────────────────────────────────

# Read rather than repeated, so the installer cannot announce a version this
# checkout is not. Empty if the file moves — a nameless banner beats a wrong one.
version="$(sed -n 's/^const Version = "\(.*\)"$/\1/p' backend/internal/version/version.go 2>/dev/null)"

say ""
say "${BOLD}Just Dashboard${version:+ $version} — setup${RESET}"
say "${DIM}Self-hosted management for a single Linux server.${RESET}"

[ "$(id -u)" -eq 0 ] || die "run this with sudo — the dashboard manages the host, so setup needs root."
[ -f docker-compose.yml ] || die "run this from inside the cloned repository (docker-compose.yml is not here)."

step "Installing and checking required host tools"
source scripts/install-dependencies.sh
jd_install_dependencies || die "Required host tools could not be provisioned. See the package-manager error above; re-run setup after resolving it."

step "Checking what this machine already has"

need_docker=0
if command -v docker >/dev/null 2>&1; then
	ok "docker $(docker --version | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)"
else
	warn "docker is not installed"
	need_docker=1
fi

if docker compose version >/dev/null 2>&1; then
	COMPOSE="docker compose"
	ok "docker compose plugin"
elif command -v docker-compose >/dev/null 2>&1; then
	COMPOSE="docker-compose"
	ok "docker-compose (v1)"
else
	[ "$need_docker" -eq 0 ] && warn "docker compose is not installed"
	need_docker=1
	COMPOSE="docker compose"
fi

ok "curl, openssl and certbot are ready"

if [ "$need_docker" -eq 1 ]; then
	say ""
	if yes_no "Install Docker now using the official get.docker.com script?" y; then
		step "Installing Docker"
		curl -fsSL https://get.docker.com | sh
		systemctl enable --now docker || true
		docker compose version >/dev/null 2>&1 && COMPOSE="docker compose"
		ok "docker installed"
	else
		die "Docker is required. Install it and re-run this script."
	fi
fi

# ── upgrading from VPS Dashboard ────────────────────────────────────────────

# The project was called VPS Dashboard, its settings were prefixed VPSD_ and it
# kept its state under /var/lib/vps-dashboard. Compose now names the new paths
# outright, so an install that predates the rename has to be moved across
# before the stack is rebuilt — otherwise it comes up against an empty database
# and bootstraps a second admin over a perfectly good one.

if [ -f .env ] && grep -q '^VPSD_' .env; then
	step "Migrating settings from the old VPSD_ prefix"
	cp .env ".env.backup.$(date +%s)"
	sed -i 's/^VPSD_/JD_/' .env
	sed -i 's|/var/lib/vps-dashboard|/var/lib/just-dashboard|g; s|/var/backups/vps-dashboard|/var/backups/just-dashboard|g' .env
	ok ".env now uses JD_* (a backup of the old one is alongside it)"
fi

for pair in "/var/lib/vps-dashboard /var/lib/just-dashboard" "/var/backups/vps-dashboard /var/backups/just-dashboard"; do
	set -- $pair
	if [ -d "$1" ] && [ ! -d "$2" ]; then
		step "Moving $1 to $2"
		mv "$1" "$2"
		ok "moved — this is your database and your archives, so it is a move, not a copy"
	fi
done

if docker compose ls --all 2>/dev/null | grep -q '^vps-dashboard'; then
	step "Retiring the old vps-dashboard compose project"
	# The project is named in docker-compose.yml, so a rename leaves the old
	# containers running and holding the ports the new ones want.
	docker compose -p vps-dashboard down --remove-orphans >/dev/null 2>&1 || true
	ok "old stack stopped"
fi

# ── existing install ────────────────────────────────────────────────────────

if [ -f .env ]; then
	step "An .env already exists"
	warn "It holds JD_MASTER_KEY, which encrypts your stored secrets."
	warn "Replacing it makes every stored TOTP seed, database password and"
	warn "backup credential unreadable."
	say ""
	if yes_no "Keep the existing .env and just rebuild?" y; then
		KEEP_ENV=1
	else
		yes_no "${RED}Really overwrite it?${RESET} Existing secrets become unrecoverable" n \
			|| die "nothing changed."
		cp .env ".env.backup.$(date +%s)"
		ok "old .env backed up alongside"
		KEEP_ENV=0
	fi
else
	KEEP_ENV=0
fi

# ── reachability ────────────────────────────────────────────────────────────

if [ "$KEEP_ENV" -eq 0 ]; then

step "How will you reach the dashboard?"
say ""
say "  This dashboard is ${BOLD}root-equivalent${RESET}: anyone who reaches it with a valid"
say "  session effectively has root on this machine. There are two ways in, and"
say "  neither of them puts anything on the public internet."
say ""

# The proxy binds loopback in every configuration, so the allowlist has to
# admit it in every configuration too — otherwise the tunnel reaches Caddy and
# then dies at the backend's perimeter check.
LOOPBACK="127.0.0.1/32,::1/128"

# tailscale_hostname prints this machine's MagicDNS name, or nothing.
#
# --json is the stable place to read it from; the same field taken from the
# human output would depend on column alignment. Self comes before Peer in that
# document, so the first DNSName is this machine's. The trailing dot is
# stripped because it is a DNS-correct FQDN and nobody types one into a browser.
tailscale_hostname() {
	tailscale status --json 2>/dev/null |
		sed -n 's/.*"DNSName": *"\([^"]*\)".*/\1/p' | head -1 | sed 's/\.$//' || true
}

TS_IP=""
TS_NAME=""
if command -v tailscale >/dev/null 2>&1; then
	TS_IP="$(tailscale ip -4 2>/dev/null | head -1 || true)"
	TS_NAME="$(tailscale_hostname)"
fi

say "  ${BOLD}1${RESET}) ${GREEN}Tailscale${RESET} ${BOLD}— recommended${RESET}${TS_IP:+  ${GREEN}already connected: $TS_IP${RESET}}"
say "     ${DIM}Reach it from your laptop or phone anywhere, with nothing exposed to${RESET}"
say "     ${DIM}the internet. With HTTPS enabled on your tailnet it also gets a real,${RESET}"
say "     ${DIM}publicly trusted certificate — so the browser shows an ordinary padlock${RESET}"
say "     ${DIM}rather than a warning. Set up for you if you do not have it.${RESET}"
say ""
say "  ${BOLD}2${RESET}) SSH tunnel"
say "     ${DIM}No new software, no account, nothing listening beyond loopback. Served${RESET}"
say "     ${DIM}over plain HTTP on localhost, which browsers treat as secure — the ssh${RESET}"
say "     ${DIM}connection is already the encryption. Needs an ssh -L command open.${RESET}"
say ""

CHOICE="$(ask "Choose 1 or 2" 1)"

case "$CHOICE" in
1)
	if [ -z "$TS_IP" ]; then
		if command -v tailscale >/dev/null 2>&1; then
			warn "tailscale is installed but this machine is not on a tailnet."
		else
			warn "tailscale is not installed."
		fi
		if yes_no "Install and connect Tailscale now?" y; then
			command -v tailscale >/dev/null 2>&1 || curl -fsSL https://tailscale.com/install.sh | sh
			say ""
			say "  ${BOLD}Tailscale will print a URL. Open it to authorise this machine.${RESET}"
			say ""
			tailscale up || warn "tailscale up did not complete"
			TS_IP="$(tailscale ip -4 2>/dev/null | head -1 || true)"
			[ -n "$TS_IP" ] && ok "connected as $TS_IP"
		fi
	fi
	if [ -z "$TS_IP" ]; then
		warn "Tailscale is not available; falling back to an SSH tunnel."
		warn "You can switch later from the dashboard's own settings page."
		SITE="localhost"
		BIND=""
		TLS_MODE="off"
		CIDRS="$LOOPBACK"
		ACCESS_KIND="tunnel"
	else
		TS_NAME="$(tailscale_hostname)"
		BIND="$TS_IP"
		CIDRS="100.64.0.0/10,$LOOPBACK"
		ACCESS_KIND="tailscale"
		# A certificate is worth trying for before anything else is decided:
		# it is the difference between an address the browser trusts and one it
		# complains about every time, and the failure is entirely recoverable.
		if [ -n "$TS_NAME" ]; then
			step "Asking Tailscale for a certificate"
			say "  ${DIM}A real Let's Encrypt certificate for $TS_NAME, issued through your${RESET}"
			say "  ${DIM}tailnet. It needs HTTPS turned on for the tailnet — one switch in the${RESET}"
			say "  ${DIM}admin console, under DNS.${RESET}"
			say ""
			mkdir -p /var/lib/just-dashboard/certs
			if tailscale cert \
				--cert-file /var/lib/just-dashboard/certs/site.crt \
				--key-file /var/lib/just-dashboard/certs/site.key \
				"$TS_NAME" >/dev/null 2>&1; then
				chmod 0644 /var/lib/just-dashboard/certs/site.crt
				chmod 0640 /var/lib/just-dashboard/certs/site.key
				SITE="$TS_NAME"
				TLS_MODE="tailscale"
				ok "certificate issued — the browser will show a normal padlock"
			else
				SITE="$TS_NAME"
				TLS_MODE="internal"
				warn "Tailscale would not issue a certificate for $TS_NAME."
				warn "That is almost always HTTPS being off for the tailnet: turn it on at"
				warn "https://login.tailscale.com/admin/dns and re-run this, or switch it on"
				warn "later from the dashboard's own settings page."
				warn "Until then the dashboard uses its own CA and the browser warns once."
			fi
		else
			SITE="$TS_IP"
			TLS_MODE="internal"
			warn "This machine has no MagicDNS name, so a trusted certificate is not"
			warn "possible; using the tailnet address with the dashboard's own CA."
		fi
	fi
	;;
2)
	SITE="localhost"
	BIND=""
	# Plain HTTP on loopback, deliberately. The tunnel is already encrypted
	# and authenticated, and http://localhost is a secure context in every
	# browser — so this is the one configuration with no warning to click
	# through and no certificate to explain.
	TLS_MODE="off"
	CIDRS="$LOOPBACK"
	ACCESS_KIND="tunnel"
	;;
*)
	die "pick 1 or 2."
	;;
esac

# ── account and behaviour ───────────────────────────────────────────────────

step "First administrator account"

ADMIN_USER="$(ask "Username" admin)"

GENERATED_PW=0
if yes_no "Generate a strong password for it?" y; then
	# 24 characters from a mixed alphabet, then a guaranteed one of each class
	# so it always satisfies the server's own strength rule.
	ADMIN_PW="$(openssl rand -base64 24 | tr -d '/+=' | cut -c1-20)Aa1!"
	GENERATED_PW=1
else
	while true; do
		read -r -s -p "  Password (min 12 chars, mixing 3 of upper/lower/digit/symbol): " ADMIN_PW; echo
		read -r -s -p "  Confirm: " ADMIN_PW2; echo
		[ "$ADMIN_PW" = "$ADMIN_PW2" ] || { warn "they do not match"; continue; }
		[ "${#ADMIN_PW}" -ge 12 ] || { warn "at least 12 characters"; continue; }
		break
	done
fi

step "Behaviour"

TERMINAL=true
yes_no "Enable the web terminal? (a real shell with this process's privileges)" y || TERMINAL=false

# Two-factor is offered to every account and demanded of none by default.
#
# It used to be compulsory, and on a dashboard reachable only over a tailnet or
# an ssh tunnel that was a decision made on the operator's behalf rather than
# with them: the network is already authenticated, and the first ninety seconds
# of the product were an authenticator app you could not skip. Enrolment is one
# button on the account page whenever you want it, and an account that has
# enrolled is always asked for its code whatever this says.
REQUIRE_2FA=false
yes_no "Require an authenticator app for every account? (you can enrol later either way)" n && REQUIRE_2FA=true

# ── ports ───────────────────────────────────────────────────────────────────
#
# Only one of these three is ever typed by a person: JD_PORT, the one in the
# URL. The other two are internal — the frontend and the backend are bound to
# loopback and reached only by the proxy — and they used to default to 3000 and
# 8080, which are the two most contested numbers on a Linux server. 3000 in
# particular is every Node app ever started, so a machine that already uses it
# is the normal case rather than the exotic one.
#
# It mattered more than a collision usually does because of *how* it failed.
# Only the frontend and backend would refuse to bind; the proxy in front of
# them comes up perfectly clean and forwards to whatever already holds the
# port — so the operator opens the dashboard, gets somebody else's application
# over the dashboard's own certificate, and has nothing anywhere that says why.
#
# So the two internal ports are now picked at random from the high range, out
# of the way of anything an operator would deliberately run, and checked free
# before they are written. The one port a person has to remember keeps its
# memorable default and only moves if something is already there.

step "Ports"

port_taken() {
	if command -v ss >/dev/null 2>&1; then
		ss -lntH "sport = :$1" 2>/dev/null | grep -q . && return 0
	elif command -v lsof >/dev/null 2>&1; then
		lsof -nP -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1 && return 0
	fi
	return 1
}

# who_has is for the message only. Being unable to name the process is not a
# reason to stay quiet about the port — hence the `|| true`, which is
# load-bearing: this runs under `set -e` and its result is consumed by a bare
# assignment, so a grep that matches nothing would take the whole installer
# down at precisely the moment a port turned out to be in use.
who_has() {
	if command -v ss >/dev/null 2>&1; then
		ss -lntpH "sport = :$1" 2>/dev/null |
			grep -o '"[^"]*"' | head -1 | tr -d '"' || true
	fi
	return 0
}

# Reserve choices across the three services before any container is started.
PICKED=""
ALLOCATED_PORTS=" "
pick_port() {
 local want="$1" name="$2" p="$1" tries=0
 while port_taken "$p" || [[ "$ALLOCATED_PORTS" == *" $p "* ]]; do
  tries=$((tries + 1))
  [ "$tries" -ge 64512 ] && die "no available dashboard port remains for $name"
  p=$((1024 + (p - 1024 + 1) % 64512))
 done
 if [ "$p" != "$want" ]; then warn "$name: $want is in use; using $p instead"; else ok "$name: $p"; fi
 PICKED="$p"
 ALLOCATED_PORTS="$ALLOCATED_PORTS$p "
}
random_port() { pick_port "$((20000 + RANDOM % 40000))" "$1"; }

pick_port 8443 "dashboard (the port you connect to)"; JD_PORT="$PICKED"
random_port "backend API (internal)";                 JD_BACKEND_PORT="$PICKED"
random_port "frontend (internal)";                    JD_FRONTEND_PORT="$PICKED"

# ── write .env ──────────────────────────────────────────────────────────────

step "Writing configuration"

MASTER_KEY="$(openssl rand -hex 32)"

umask 077
cat > .env <<EOF
# Written by install.sh on $(date -u +%Y-%m-%dT%H:%M:%SZ).
# This file contains secrets. Keep it at mode 600 and never commit it.

# Encrypts TOTP seeds, database connection strings, deploy env vars and backup
# credentials at rest. Losing it loses every stored secret.
JD_MASTER_KEY=$MASTER_KEY

# The single address the stack answers on, and the name on its certificate.
JD_SITE=$SITE

# The interface it listens on, when that is not the same string as the address
# above. Blank in every configuration but Tailscale, where the certificate is
# issued to a MagicDNS name and the socket is opened on the tailnet IP behind
# it — the proxy container resolves names through Docker's resolver rather than
# the host's, so it must not be asked to resolve that name itself.
JD_BIND=$BIND

# How the connection is trusted:
#
#   tailscale  a real, publicly trusted certificate from your tailnet. No
#              browser warning. Renewed automatically by the dashboard.
#   internal   Caddy's own CA. Encrypted, but the browser says "not secure"
#              because nothing off this machine has heard of the issuer.
#   off        plain HTTP, loopback only. The ssh tunnel is already the
#              encryption, and browsers treat http://localhost as secure, so
#              this is the configuration with nothing to click through.
JD_TLS=$TLS_MODE

# The port you connect to, and the two behind it. The dashboard port keeps a
# memorable default and moves only if something already holds it. The other two
# are internal, reached only by the proxy over loopback, and are picked at
# random from the high range: their old defaults (3000 and 8080) collide on a
# great many servers, and the collision used to surface as the dashboard
# proxying you to somebody else's application.
JD_PORT=$JD_PORT
JD_BACKEND_PORT=$JD_BACKEND_PORT
JD_FRONTEND_PORT=$JD_FRONTEND_PORT

# Checked before authentication: an address outside this list cannot even
# reach the login handler.
JD_ALLOWED_CIDRS=$CIDRS
JD_TRUSTED_PROXIES=127.0.0.1/32
JD_ALLOWED_ORIGINS=

JD_TERMINAL_ENABLED=$TERMINAL

# Whether an account with no authenticator may sign in. An account that has
# enrolled one is always asked for its code, whatever this says.
JD_REQUIRE_2FA=$REQUIRE_2FA

# Used once, to create the first account. Safe to remove afterwards.
JD_BOOTSTRAP_USER=$ADMIN_USER
JD_BOOTSTRAP_PASSWORD=$ADMIN_PW

JD_COMPOSE_ROOTS=/opt,/srv,/home
JD_GIT_ROOTS=/opt,/srv,/home,/root
JD_DEPLOY_ROOTS=/opt,/srv,/home,/root
JD_LOG_ROOTS=/var/log
JD_FILE_ROOTS=/
JD_BACKUP_DIR=/var/backups/just-dashboard
JD_LOG_LEVEL=info
EOF
chmod 600 .env
ok ".env written (mode 600)"

fi  # KEEP_ENV

# ── settings an older .env has never heard of ───────────────────────────────
#
# A re-run that keeps an existing .env skipped the port checks entirely, which
# left the one case that most needs them unserved: an install made before the
# ports were settings at all, on a machine that has since started using 3000
# for something else. That operator had to be told to hand-edit .env, which is
# not a fix, it is a workaround with a person in the middle of it.
#
# So the same checks run here, and the file is amended rather than rewritten:
# a variable missing from an older .env is appended, and one whose port is now
# held by something else is rewritten to a free one. A port that is merely
# non-default is left exactly as it is — it was a deliberate choice, and this
# is not the place to second-guess it.
if [ "$KEEP_ENV" -eq 1 ]; then
	# The freshly built backend fills missing port values and reconciles
	# existing owners immediately before startup below.

	# Settings that did not exist when this .env was written.
	#
	# Two of them are filled in with the behaviour the install already had, so a
	# re-run never changes how a working dashboard behaves: JD_TLS=internal is
	# what every install before this one did, and an empty JD_BIND means the
	# proxy binds exactly what it bound yesterday.
	#
	# The third is not that kind of setting. Two-factor used to be compulsory
	# and is now a policy whose default is *optional*, so an upgrade changes it
	# unless somebody decides otherwise — and a security setting that changes
	# under an operator without being mentioned is the thing this whole block
	# exists to avoid. It is asked rather than assumed, and the answer is
	# written down, so this install and one upgraded by `docker compose up`
	# differ only where the operator said they should.
	step "Settings this .env predates"

	fill_env() {
		local var="$1" value="$2" note="$3"
		if grep -qE "^$var=" .env; then
			return
		fi
		printf '%s=%s\n' "$var" "$value" >> .env
		ok "$var=$value  ${DIM}($note)${RESET}"
	}

	fill_env JD_TLS internal "how the connection is trusted; the settings page can change it"
	fill_env JD_BIND "" "the interface to listen on, when it differs from JD_SITE"

	if ! grep -qE '^JD_REQUIRE_2FA=' .env; then
		say ""
		say "  ${BOLD}Two-factor is no longer compulsory by default.${RESET}"
		say "  ${DIM}An account that has enrolled an authenticator is still asked for its${RESET}"
		say "  ${DIM}code at every sign-in — that does not change. What changes is whether${RESET}"
		say "  ${DIM}an account with no authenticator can sign in at all. Your install has${RESET}"
		say "  ${DIM}required one until now.${RESET}"
		say ""
		KEEP_REQUIRE_2FA=false
		yes_no "Keep requiring an authenticator for every account?" n && KEEP_REQUIRE_2FA=true
		printf 'JD_REQUIRE_2FA=%s\n' "$KEEP_REQUIRE_2FA" >> .env
		ok "JD_REQUIRE_2FA=$KEEP_REQUIRE_2FA"
	fi
fi

# ── build and start ─────────────────────────────────────────────────────────

# Clipboard images cross from the backend container into a host-side shell.
# The shared root therefore has to be owned by the root-running backend: it
# deliberately refuses a directory a local account could replace underneath
# it. Compose normally creates a missing bind source as root, but an existing
# directory keeps its old owner, which left image paste returning HTTP 500 on
# installs where the operator had created this path first.
CLIPBOARD_ROOT=/tmp/just-dashboard
if [ ! -e "$CLIPBOARD_ROOT" ] && [ ! -L "$CLIPBOARD_ROOT" ]; then
	mkdir --mode=0711 "$CLIPBOARD_ROOT"
fi
if [ ! -d "$CLIPBOARD_ROOT" ] || [ -L "$CLIPBOARD_ROOT" ]; then
	die "$CLIPBOARD_ROOT must be a real directory, not a file or symlink."
fi
# Do not follow an object swapped in by the current owner. Once the real
# directory is root-owned, /tmp's sticky bit prevents that owner replacing it.
chown --no-dereference root:root "$CLIPBOARD_ROOT"
if [ ! -d "$CLIPBOARD_ROOT" ] || [ -L "$CLIPBOARD_ROOT" ]; then
	die "$CLIPBOARD_ROOT changed while its ownership was being secured."
fi
chmod 0711 "$CLIPBOARD_ROOT"

step "Building and starting the stack"
say "  ${DIM}First build compiles the Go backend and the Next.js frontend; give it a few minutes.${RESET}"
say ""

$COMPOSE build
# Use the freshly built backend so installs and in-app lifecycle operations
# share port ownership detection and update every consumer through .env.
docker run --rm --network host --pid host \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "$PWD:$PWD" \
  just-dashboard-backend:latest -prepare-ports "$PWD" -start-stack

step "Waiting for the dashboard to answer"

SITE_ADDR="$(grep -E '^JD_SITE=' .env | cut -d= -f2-)"

# Read back rather than reused from above: a re-run that keeps an existing
# .env never entered the block that chose them, and printing the defaults at
# an install that is not using the defaults is how somebody ends up tunnelling
# to the wrong port and concluding the dashboard is broken.
# The `|| true` is load-bearing under `set -euo pipefail`: grep exits non-zero
# when the variable is absent, pipefail promotes that to the pipeline's status,
# and a bare assignment consuming it aborts the script. Absent is the normal
# case on an .env written before these variables existed — which is precisely
# the install being re-run.
env_port() { grep -E "^$1=" .env 2>/dev/null | cut -d= -f2- | tr -d '[:space:]' || true; }
JD_PORT="$(env_port JD_PORT)";                   JD_PORT="${JD_PORT:-8443}"
JD_BACKEND_PORT="$(env_port JD_BACKEND_PORT)";   JD_BACKEND_PORT="${JD_BACKEND_PORT:-8080}"
JD_FRONTEND_PORT="$(env_port JD_FRONTEND_PORT)"; JD_FRONTEND_PORT="${JD_FRONTEND_PORT:-3000}"

HEALTHY=0
for _ in $(seq 1 60); do
	if curl -fsS --max-time 3 "http://127.0.0.1:$JD_BACKEND_PORT/healthz" >/dev/null 2>&1; then
		HEALTHY=1; break
	fi
	sleep 2
done

if [ "$HEALTHY" -eq 1 ]; then
	ok "backend is healthy"
else
	warn "the backend did not answer within two minutes"
	say ""
	say "  Check what it is saying:"
	say "    ${BOLD}$COMPOSE logs backend${RESET}"
	exit 1
fi

# ── how to get in ───────────────────────────────────────────────────────────

say ""
say "${GREEN}${BOLD}The dashboard is running.${RESET}"
say ""

PUBLIC_HOST="$(curl -fsS --max-time 5 https://api.ipify.org 2>/dev/null || true)"

# Printed after every route except the loopback-only one, where it would just
# be repeating the instructions above it.
tunnel_fallback() {
	say ""
	say "  ${DIM}If that is ever unreachable, an SSH tunnel still works:${RESET}"
	say "    ${DIM}ssh -N -L $JD_PORT:localhost:$JD_PORT $(logname 2>/dev/null || echo root)@${PUBLIC_HOST:-YOUR_SERVER}${RESET}"
	say "    ${DIM}then open $SCHEME://localhost:$JD_PORT${RESET}"
}

# A re-run that kept its .env never entered the block that chose these, so they
# are read back from the file rather than defaulted — printing tunnel
# instructions to somebody whose dashboard is on a tailnet is how an operator
# concludes the installer has reconfigured something behind their back.
if [ "${KEEP_ENV:-0}" -eq 1 ]; then
	TLS_MODE="$(env_port JD_TLS)"; TLS_MODE="${TLS_MODE:-internal}"
	if [ "$SITE_ADDR" = "localhost" ] || [ "$SITE_ADDR" = "127.0.0.1" ]; then
		ACCESS_KIND="tunnel"
	else
		ACCESS_KIND="tailscale"
	fi
fi

# The scheme follows the certificate: the tunnel configuration is deliberately
# plain HTTP on loopback, and printing an https:// URL for it would send the
# operator to a port that answers nothing.
SCHEME="https"
[ "${TLS_MODE:-internal}" = "off" ] && SCHEME="http"

case "${ACCESS_KIND:-tunnel}" in
tunnel)
	say "  ${BOLD}Reach it over an SSH tunnel.${RESET} From your laptop:"
	say ""
	say "    ${BLUE}ssh -N -L $JD_PORT:localhost:$JD_PORT $(logname 2>/dev/null || echo root)@${PUBLIC_HOST:-YOUR_SERVER}${RESET}"
	say ""
	say "  Then open ${BOLD}$SCHEME://localhost:$JD_PORT${RESET} while that stays open."
	;;
tailscale)
	say "  ${BOLD}Reach it from any device on your tailnet:${RESET}"
	say ""
	say "    ${BLUE}$SCHEME://$SITE_ADDR:$JD_PORT${RESET}"
	tunnel_fallback
	;;
esac

say ""
case "${TLS_MODE:-internal}" in
tailscale)
	say "  ${GREEN}The certificate is a real one${RESET}, issued to $SITE_ADDR through your tailnet."
	say "  ${DIM}No browser warning, and the dashboard renews it before it expires.${RESET}"
	;;
off)
	say "  ${DIM}Served over plain HTTP on loopback. That is not a downgrade: the ssh${RESET}"
	say "  ${DIM}tunnel is already encrypted and authenticated, and browsers treat${RESET}"
	say "  ${DIM}http://localhost as a secure origin — so there is no warning to click${RESET}"
	say "  ${DIM}through and no certificate to explain.${RESET}"
	;;
*)
	say "  ${DIM}The certificate is signed by Caddy's own CA, so the browser warns once.${RESET}"
	say "  ${DIM}That is expected: the link is already encrypted by the tunnel or tailnet.${RESET}"
	say "  ${DIM}Turn HTTPS on for your tailnet to replace it with a trusted one — the${RESET}"
	say "  ${DIM}dashboard's own settings page can then switch to it without a re-install.${RESET}"
	;;
esac

if [ "${KEEP_ENV:-0}" -eq 0 ]; then
	say ""
	say "  ${BOLD}Sign in with${RESET}"
	say "    username  ${BOLD}$ADMIN_USER${RESET}"
	if [ "${GENERATED_PW:-0}" -eq 1 ]; then
		say "    password  ${BOLD}$ADMIN_PW${RESET}"
		say ""
		warn "Save that password now — it is shown once, and you must change it at first login."
	else
		say "    password  ${DIM}(the one you chose)${RESET}"
	fi
	say ""
	if [ "${REQUIRE_2FA:-false}" = "true" ]; then
		say "  You will be asked to enrol an authenticator app before anything else works."
	else
		say "  ${DIM}Two-factor is off by default. Turn it on for your account from${RESET}"
		say "  ${DIM}Account → Two-factor authentication whenever you want it.${RESET}"
	fi
fi

say ""
say "  ${DIM}Useful from here:${RESET}"
say "    $COMPOSE logs -f backend    ${DIM}# what the server is doing${RESET}"
say "    $COMPOSE restart            ${DIM}# after editing .env by hand${RESET}"
say "    $COMPOSE down               ${DIM}# stop it${RESET}"
say ""
say "  ${DIM}Ports, address, certificate and two-factor are all editable from the${RESET}"
say "  ${DIM}dashboard itself, under Operations → Dashboard → Configuration. It${RESET}"
say "  ${DIM}restarts into a change and puts the old one back if it does not come up.${RESET}"
say ""
