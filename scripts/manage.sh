#!/usr/bin/env bash
# Run from any directory; Compose reads configuration without executing .env.
set -euo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
	cat <<'EOF'
Just Dashboard · terminal tools

Usage: sudo ./scripts/manage.sh COMMAND [ARGUMENTS]

Accounts (dashboard accounts, not Linux users)
  users                              List accounts, roles and two-factor status
  create-user USER [ROLE]             Create an account (default: readonly)
  reset-password USER                Set a new password and clear lockout
  revoke-sessions USER                Sign out every browser session for USER

Linking to just-dashboard.com (optional)
  link [SCOPE]                       Link this server to your just-dashboard.com account.
                                     Prints a code to enter there, then waits for you to
                                     approve it. SCOPE is observe (default), operate or
                                     maintain; widen it later in Settings. Exits 0 once
                                     linked, 2 if it was declined or not approved in time
  unlink                             Unlink it
  link-status                        Say whether it is linked

Connecting to Just Hub on your tailnet (optional)
  hub connect URL [OPTIONS]          Connect this server to a Just Hub. URL is the hub's
                                     tailnet name, for example hub.example.ts.net. Prints a
                                     code and this server's fingerprint, then waits for the
                                     hub's owner to approve it there. Options:
                                       --scope observe|operate|maintain (default maintain)
                                       --token-stdin  a one-time token from the hub: asked
                                                      for with echo off at a terminal, or
                                                      read as one line from a pipe
                                       --wait 15m     how long to wait, at most 1h
                                     Exits 0 once connected, 2 if it was declined, not
                                     approved in time or interrupted
  hub disconnect                     Disconnect it
  hub status                         Say whether it is connected

Stack
  status                             Show containers and their health
  logs [backend|frontend|proxy]       Follow logs (default: backend; Ctrl+C exits)
  restart                            Recreate the stack with current .env settings
  install-cli                        Install the jd command line to /usr/local/bin/jd from
                                     the built backend image (install.sh does this too)

Version, and updating over SSH
  version                            Say what is installed: the checkout's version and
                                     commit, what the built images call themselves, and
                                     the last update the dashboard recorded
  update [VERSION] [--yes]           Fast-forward the checkout to the newest release tag on
                                     origin (or to VERSION), keep the running images as
                                     :previous, rebuild and restart
  rollback [COMMIT] [--yes]          Go back to what ran before the last update: the
                                     :previous images, and the checkout at COMMIT (default:
                                     the commit the dashboard's last update started from)

Roles: readonly, limited, admin
Password commands prompt twice with hidden input. For automation, append
--password-stdin and pipe one password line from a trusted source. Passwords
never belong in command arguments. The password you set is the one you sign
in with; it is not asked for again. Resetting preserves two-factor and disabled
state, and revokes browser sessions and API tokens. Account changes are
audited as local root.

Linking is optional and refused when JD_ACCOUNT_LINK=false. The code link prints
is only a pointer to the request; the key it is signed with never leaves the
dashboard. See docs/guides/linking-and-usage.md.

Connecting to a hub is optional and refused when JD_HUB_LINK=false. The hub must
be another machine on this server's own tailnet, and is only reached over it.

The Version page updates the dashboard while it is up, and that is the update to
prefer: it copies the database first and goes back by itself when the new
version does not answer. update and rollback are for when the dashboard is not
there to do it. Both say what they will do and ask before changing anything
(--yes answers for you); update copies no database and installs only a release
tag. See "If an update fails" in docs/guides/administration.md.

Examples:
  sudo ./scripts/create-user.sh alice limited
  sudo ./scripts/reset-password.sh admin
  sudo ./scripts/manage.sh status

Account commands work with a stopped backend, using the already-built image.
Run this from SSH when restarting the stack; the web terminal disconnects.
EOF
}

die() { printf 'Error: %s\n' "$*" >&2; exit 1; }

command="${1:---help}"
shift "$(( $# > 0 ? 1 : 0 ))"
case "$command" in --help|-h|help) usage; exit 0 ;; esac

password_stdin=0
token_stdin=0
assume_yes=0
case "$command" in
	create-user|reset-password)
		if [ "${!#:-}" = "--password-stdin" ]; then
			password_stdin=1
			set -- "${@:1:$#-1}"
		fi
		[ "$#" -ge 1 ] && [ -n "$1" ] || die "$command needs a username. See --help."
		if [ "$command" = "create-user" ]; then
			[ "$#" -le 2 ] || die "create-user accepts USER [ROLE]. See --help."
			case "${2:-readonly}" in readonly|limited|admin) ;; *) die "Role must be readonly, limited or admin." ;; esac
		else
			[ "$#" -eq 1 ] || die "reset-password accepts one username. See --help."
		fi
		;;
	users|status|restart|unlink|link-status|install-cli|version) [ "$#" -eq 0 ] || die "$command does not accept arguments. See --help." ;;
	update|rollback)
		if [ "${!#:-}" = "--yes" ]; then
			assume_yes=1
			set -- "${@:1:$#-1}"
		fi
		[ "$#" -le 1 ] || die "$command accepts one argument. See --help."
		if [ "$command" = "update" ]; then
			[ "$#" -eq 0 ] || [[ "$1" =~ ^v?[0-9]+\.[0-9]+(\.[0-9]+)?$ ]] || die "update takes a version such as 0.8.6. See --help."
		else
			[ "$#" -eq 0 ] || [[ "$1" =~ ^[0-9a-f]{7,40}$ ]] || die "rollback takes a commit such as 98c9349. See --help."
		fi
		;;
	link)
		[ "$#" -le 1 ] || die "link accepts one scope. See --help."
		case "${1:-observe}" in observe|operate|maintain) ;; *) die "Scope must be observe, operate or maintain." ;; esac
		;;
	revoke-sessions) [ "$#" -eq 1 ] && [ -n "$1" ] || die "revoke-sessions needs one username. See --help." ;;
	hub)
		case "${1:-}" in
			connect)
				[ "$#" -ge 2 ] && [ -n "$2" ] && [ "${2#-}" = "$2" ] || die "hub connect needs the hub's address, for example hub.example.ts.net. See --help."
				i=3
				while [ "$i" -le "$#" ]; do
					arg="${!i}"
					case "$arg" in
						--token-stdin) token_stdin=1 ;;
						# A token on the command line is in the process list and the
						# shell history; it only ever travels on standard input.
						--token|--token=*) die "pass the token on standard input with --token-stdin" ;;
						--scope=*|--wait=*) value="${arg#*=}" ;;
						--scope|--wait)
							i=$((i + 1))
							[ "$i" -le "$#" ] || die "$arg needs a value. See --help."
							value="${!i}"
							;;
						*) die "hub connect does not take $arg. See --help." ;;
					esac
					case "$arg" in
						--scope|--scope=*)
							case "$value" in observe|operate|maintain) ;; *) die "Scope must be observe, operate or maintain." ;; esac
							;;
					esac
					i=$((i + 1))
				done
				;;
			disconnect|status) [ "$#" -eq 1 ] || die "hub $1 does not accept arguments. See --help." ;;
			*) die "hub takes connect, disconnect or status. See --help." ;;
		esac
		;;
	logs)
		[ "$#" -le 1 ] || die "logs accepts one service. See --help."
		case "${1:-backend}" in backend|frontend|proxy) ;; *) die "Service must be backend, frontend or proxy." ;; esac
		;;
	*) die "Unknown command. See --help." ;;
esac

[ "$(id -u)" -eq 0 ] || die "Run with sudo; these tools administer the dashboard on this host."
[ -f "$root/.env" ] || die "No .env in $root. Complete installation first."
command -v docker >/dev/null 2>&1 || die "Docker is not installed."
if docker compose version >/dev/null 2>&1; then
	compose=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
	compose=(docker-compose)
else
	die "Docker Compose is not installed."
fi
cd -- "$root"

# What version, update and rollback share. The dashboard's data is at one path
# on the host (docker-compose.yml mounts it there); the override is for tests.
data="${JD_HOST_DATA_DIR:-/var/lib/just-dashboard}"
images=(just-dashboard-backend just-dashboard-frontend)
updater=just-dashboard-updater
signers=backend/internal/selfupdate/allowed_signers

# git in the checkout, as whoever owns it. This script runs as root, and a
# fetch or a reset made as root leaves files in .git its owner cannot write.
checkout_git() {
	local owner home
	owner="$(stat -c %U -- "$root" 2>/dev/null || echo root)"
	if [ "$owner" != root ] && [ "$owner" != UNKNOWN ] && command -v runuser >/dev/null 2>&1; then
		home="$(getent passwd "$owner" 2>/dev/null | cut -d: -f6 || true)"
		runuser -u "$owner" -- env HOME="${home:-/}" git -C "$root" "$@"
	else
		git -c "safe.directory=$root" -C "$root" "$@"
	fi
}

confirm() {
	[ "$assume_yes" -eq 1 ] && return 0
	[ -t 0 ] || die "$command changes the running dashboard. Run it at a terminal, or append --yes."
	local answer=""
	read -r -p "$1 [y/N] " answer || true
	case "$answer" in y|Y|yes|Yes|YES) ;; *) die "Nothing was changed." ;; esac
}

updater_running() { [ -n "$(docker ps --quiet --filter "name=^${updater}\$" 2>/dev/null)" ]; }
image_exists() { docker image inspect "$1" >/dev/null 2>&1; }

# Which images the :previous tags name now, as one word. A record of an update
# says which commit those images ran at, and that is true only until something
# keeps the images again; this is what rollback compares to find out.
previous_ids() {
	local name id ids=""
	for name in "${images[@]}"; do
		id="$(docker image inspect --format '{{.Id}}' "$name:previous" 2>/dev/null || true)"
		ids="${ids:+$ids,}${id:-none}"
	done
	printf '%s' "$ids"
}

# One field of the record the dashboard keeps of its last update.
recorded() {
	[ -r "$data/self-update.json" ] || return 0
	sed -n "s/^  \"$1\": \"\([^\"]*\)\".*/\1/p" "$data/self-update.json" 2>/dev/null | head -n 1 || true
}

# The commit a build of the checkout is stamped with; nothing for a release.
build_commit() {
	[ -f scripts/build-stamp.sh ] || return 0
	# shellcheck source=scripts/build-stamp.sh
	source scripts/build-stamp.sh
	jd_build_commit "$root"
}

# One image at a time, as install.sh builds them: together the two compilers
# need more memory than a small server has.
build_stack() {
	local args=() commit services service
	commit="$(build_commit)"
	[ -z "$commit" ] || args=(--build-arg "JD_COMMIT=$commit")
	if services="$("${compose[@]}" config --services 2>/dev/null)" && [ -n "$services" ]; then
		for service in $services; do
			"${compose[@]}" build ${args[@]+"${args[@]}"} "$service" || return 1
		done
	else
		"${compose[@]}" build ${args[@]+"${args[@]}"} || return 1
	fi
}

# A release key in the checkout means this version installs only a release
# signed with it, and so does this command. The same three checks as the
# dashboard's updater (verifyReleaseTag in backend/internal/selfupdate): a tag
# object, named for this release, with a good signature by a key in the file as
# the checkout has it committed. Every setting that decides the verdict is
# given here, so the checkout's own git configuration cannot vouch for a tag.
verify_release_tag() {
	local tag="$1" keys named
	keys="$(mktemp)" || die "Could not write the release keys to check the signature against."
	chmod 0644 "$keys"
	if ! checkout_git show "HEAD:$signers" >"$keys" 2>/dev/null || ! grep -qEv '^[[:space:]]*(#|$)' "$keys"; then
		rm -f "$keys"
		return 0
	fi
	refuse() { rm -f "$keys"; die "The tag $tag $1, so it was not installed; nothing was changed. This version installs only a release signed with one of the keys it carries."; }
	[ "$(checkout_git cat-file -t "refs/tags/$tag" 2>/dev/null)" = tag ] || refuse "is a lightweight tag, which carries no signature"
	named="$(checkout_git cat-file tag "refs/tags/$tag" | sed -n '/^$/q; s/^tag //p' | head -n 1)"
	[ "$named" = "$tag" ] || refuse "is a signed tag made for \"$named\", not for this release"
	checkout_git -c "gpg.ssh.allowedSignersFile=$keys" -c gpg.ssh.program=ssh-keygen -c gpg.minTrustLevel=fully \
		-c gpg.program=false -c gpg.openpgp.program=false -c gpg.x509.program=false \
		verify-tag "refs/tags/$tag" || refuse "is not signed by a Just Dashboard release key"
	rm -f "$keys"
}

case "$command" in
	status) exec "${compose[@]}" ps --all ;;
	version)
		release="$(sed -n 's/^const Version = "\(.*\)"$/\1/p' backend/internal/version/version.go 2>/dev/null || true)"
		printf 'Just Dashboard %s\n' "${release:-(version unknown)}"
		if head="$(git -c "safe.directory=$root" -C "$root" rev-parse --short HEAD 2>/dev/null)"; then
			branch="$(git -c "safe.directory=$root" -C "$root" symbolic-ref --quiet --short HEAD 2>/dev/null || echo "no branch")"
			if [ -n "$(build_commit)" ]; then note="not the $release release"; else note="the $release release"; fi
			printf '  Checkout     %s at %s on %s (%s)\n' "$root" "$head" "$branch" "$note"
		else
			printf '  Checkout     %s (git cannot read it)\n' "$root"
		fi
		for tag in latest previous; do
			image_exists "just-dashboard-backend:$tag" || continue
			said="$(docker run --rm --entrypoint /usr/local/bin/jd "just-dashboard-backend:$tag" version 2>/dev/null || true)"
			if [ "$tag" = latest ]; then label="Built"; else label="Kept"; fi
			printf '  %-12s just-dashboard-backend:%s calls itself %s\n' "$label" "$tag" "${said:-nothing (it carries no jd)}"
		done
		if [ -r "$data/self-update.json" ]; then
			printf '  Last update  %s to %s, %s\n' "$(recorded fromVersion)" "$(recorded toVersion)" "$(recorded status)"
			printf '  Transcript   %s\n' "$data/self-update.log"
			copy="$(recorded databaseCopy)"
			[ -z "$copy" ] || printf '  Database copy taken before it: %s\n' "$copy"
		else
			printf '  Last update  none recorded by the dashboard\n'
		fi
		if updater_running; then
			printf '  An update is running now. Follow it with: docker logs -f %s\n' "$updater"
		fi
		;;
	update)
		updater_running && die "An update started from the dashboard is still running. Follow it with: docker logs -f $updater"
		command -v git >/dev/null 2>&1 || die "git is not installed."
		from="$(checkout_git rev-parse HEAD 2>/dev/null)" || die "$root is not a git checkout this can read."
		# Origin is asked which tags it has, and only the one being installed
		# is fetched. `git fetch --tags origin` also brought every branch
		# origin has, and the project's repository keeps the screenshots of its
		# pull requests on branches of their own; and into a checkout cloned at
		# depth one, each older tag brings the whole history below it.
		printf 'Asking origin for its release tags...\n'
		listed="$(checkout_git ls-remote --tags --refs origin 'refs/tags/v*')" || die "Could not ask origin for its tags. Nothing was changed."
		if [ -n "${1:-}" ]; then
			tag="v${1#v}"
		else
			tag="$(printf '%s\n' "$listed" | awk '{ sub("^refs/tags/", "", $2); print $2 }' | grep -E '^v[0-9]+\.[0-9]+(\.[0-9]+)?$' | sort -V | tail -n 1 || true)"
			[ -n "$tag" ] || die "origin has no release tag, and this installs nothing else. Nothing was changed."
		fi
		printf '%s\n' "$listed" | awk -v ref="refs/tags/$tag" '$2 == ref { found = 1 } END { exit !found }' ||
			die "There is no release tag $tag. Nothing was changed."
		checkout_git fetch origin "refs/tags/$tag:refs/tags/$tag" || die "Could not fetch $tag from origin. Nothing was changed."
		target="$(checkout_git rev-parse --verify --quiet "refs/tags/$tag^{commit}")" || die "There is no release tag $tag. Nothing was changed."
		# A checkout cloned with --depth has nothing below the commit it was
		# cloned at, so git cannot tell a tag that is behind HEAD from one on
		# another line. The history is fetched once, when the answer needs it.
		if ! checkout_git merge-base --is-ancestor HEAD "$target" &&
			[ "$(checkout_git rev-parse --is-shallow-repository 2>/dev/null)" = true ]; then
			printf 'This checkout was cloned without its history, which is needed to place %s. Fetching it...\n' "$tag"
			checkout_git fetch --unshallow origin || die "Could not fetch the history from origin. Nothing was changed."
		fi
		if checkout_git merge-base --is-ancestor "$target" HEAD; then
			printf 'The checkout is already on %s or past it, so there is nothing to update.\n' "$tag"
			printf 'To rebuild what is here: sudo ./install.sh\n'
			exit 0
		fi
		checkout_git merge-base --is-ancestor HEAD "$target" ||
			die "The checkout has commits that $tag does not have, so it cannot be fast-forwarded. Nothing was changed."
		verify_release_tag "$tag"
		printf '\nThis will:\n'
		printf '  keep the images that are running as :previous\n'
		printf '  fast-forward the checkout from %s to %s (%s); your own edits stay, and git refuses if one is in the way\n' "${from:0:7}" "$tag" "${target:0:7}"
		printf '  rebuild both images, which takes a few minutes, and recreate the containers\n'
		printf 'The database is not copied first. The Version page does that; use it when the dashboard is up.\n'
		confirm "Update to $tag?"
		for name in "${images[@]}"; do
			if image_exists "$name:latest"; then
				docker image tag "$name:latest" "$name:previous" || die "Could not keep $name:latest as $name:previous. Nothing was changed."
			fi
		done
		# What rollback reads when it is given no commit: the commit the images
		# just kept ran at, where this takes the checkout, and which images
		# :previous now names. Written before the checkout moves, so the record
		# is never older than the tags it describes, also when git then refuses.
		printf '%s %s %s\n' "$from" "$target" "$(previous_ids)" >"$data/manage-update.last" 2>/dev/null || true
		checkout_git merge --ff-only "$target" || die "git would not fast-forward the checkout (the reason is above). The dashboard is running as it was."
		undo="sudo ./scripts/manage.sh rollback ${from:0:12}"
		build_stack || die "The build failed. The previous version is still running. Put the checkout and the images back with: $undo"
		"${compose[@]}" up -d --remove-orphans || die "The new version could not be started. Go back with: $undo"
		printf '\nUpdated to %s. Check it with: sudo ./scripts/manage.sh status\n' "$tag"
		printf 'If it does not come up, go back with: %s\n' "$undo"
		;;
	rollback)
		updater_running && die "An update is still running, and it goes back by itself when it fails. Follow it with: docker logs -f $updater"
		kept=()
		for name in "${images[@]}"; do
			if image_exists "$name:previous"; then kept+=("$name"); fi
		done
		[ "${#kept[@]}" -gt 0 ] ||
			die "No :previous image is kept on this server, so there is nothing built to go back to. To go back by rebuilding: git reset --keep COMMIT, then sudo ./install.sh"
		commit="${1:-}"
		head="$(checkout_git rev-parse HEAD 2>/dev/null || true)"
		if [ -z "$commit" ] && [ -n "$head" ]; then
			# The commit the :previous images ran at, from whichever record still
			# describes them, and only when the checkout is where that update left
			# it. This script's own record holds until the images are kept again
			# (it names them). The dashboard's holds until this script keeps them
			# after it, which makes this script's record the newer file. A stale
			# record moved the checkout two versions back under images one version
			# back, so a checkout somebody has moved since, or images kept since,
			# are not put back on a guess.
			was="" now="" ids=""
			if [ -r "$data/manage-update.last" ]; then
				read -r was now ids <"$data/manage-update.last" || true
			fi
			if [ -n "$was" ] && [ "$now" = "$head" ] && [ -n "$ids" ] && [ "$ids" = "$(previous_ids)" ]; then
				commit="$was"
			elif [ "$(recorded toCommit)" = "$head" ] && ! [ "$data/manage-update.last" -nt "$data/self-update.json" ]; then
				commit="$(recorded fromCommit)"
			fi
		fi
		full=""
		if [ -n "$commit" ]; then
			[ -n "$head" ] || die "$root is not a git checkout this can read. Nothing was changed."
			full="$(checkout_git rev-parse --verify --quiet "$commit^{commit}")" || die "The checkout has no commit $commit. Nothing was changed."
		fi
		printf 'This will:\n'
		for name in "${kept[@]}"; do
			printf '  put %s:previous back as %s:latest\n' "$name" "$name"
		done
		if [ -n "$full" ] && [ "$full" != "$head" ]; then
			printf '  move the checkout from %s back to %s (git reset --keep: your own edits stay, and git refuses if one is in the way)\n' "${head:0:7}" "${full:0:7}"
		elif [ -n "$full" ]; then
			printf '  leave the checkout where it is: it is already on %s\n' "${full:0:7}"
		else
			printf '  leave the checkout where it is: it is not where a recorded update left it. To move it too, name the commit: rollback COMMIT\n'
		fi
		printf '  recreate the containers from those images\n'
		printf 'The database is left as it is: the previous version can read it.\n'
		confirm "Go back to the previous version?"
		if [ -n "$full" ] && [ "$full" != "$head" ]; then
			checkout_git reset --keep "$full" || die "git would not move the checkout back (the reason is above). Nothing was changed."
		fi
		for name in "${kept[@]}"; do
			docker image tag "$name:previous" "$name:latest" || die "Could not put $name:previous back as $name:latest."
		done
		"${compose[@]}" up -d --no-build --remove-orphans || die "The containers could not be recreated. See: sudo ./scripts/manage.sh status"
		printf '\nThe previous version is back. Check it with: sudo ./scripts/manage.sh status\n'
		copy="$(recorded databaseCopy)"
		if [ -n "$copy" ]; then
			printf 'The database was not touched. The copy taken before the last update from the dashboard is %s;\n' "$copy"
			printf '"Updating" in docs/guides/administration.md says how to put it back, if the data itself is wrong.\n'
		fi
		;;
	logs) exec "${compose[@]}" logs --tail 100 --follow "${1:-backend}" ;;
	restart)
		printf 'Recreating Just Dashboard with current .env settings...\n'
		# Compose restart keeps the old container environment after an .env edit.
		exec "${compose[@]}" up -d --force-recreate
		;;
	users|revoke-sessions|unlink|link-status)
		exec "${compose[@]}" run --rm --no-deps -T backend --admin "$command" "$@"
		;;
	install-cli)
		# The image install.sh built carries jd beside the dashboard; the copy
		# is written whole and moved into place, so a jd already running keeps
		# its file.
		target="${JD_CLI_PATH:-/usr/local/bin/jd}"
		tmp="$(mktemp "$(dirname -- "$target")/.jd-XXXXXX")" || die "Cannot write to $(dirname -- "$target")."
		trap 'rm -f "$tmp"' EXIT
		docker run --rm --entrypoint cat just-dashboard-backend:latest /usr/local/bin/jd >"$tmp" ||
			die "The backend image has no jd. Build the stack first (install.sh, or a dashboard update)."
		[ -s "$tmp" ] || die "The backend image has no jd. Build the stack first (install.sh, or a dashboard update)."
		chmod 0755 "$tmp"
		mv -f "$tmp" "$target"
		trap - EXIT
		printf 'Installed %s: %s\n' "$target" "$("$target" version 2>/dev/null || echo jd)"
		;;
	link)
		# exec keeps the command's own exit status: 2 means declined or not approved in time.
		exec "${compose[@]}" run --rm --no-deps -T backend --admin link --scope "${1:-observe}"
		;;
	hub)
		sub="$1"
		shift
		# exec keeps the command's own exit status: 2 means declined, not approved in
		# time or interrupted.
		if [ "$token_stdin" -eq 0 ]; then
			exec "${compose[@]}" run --rm --no-deps -T backend --admin "hub-$sub" "$@"
		fi
		# The token is read here and handed to the admin command on its standard
		# input, never as an argument, so it is in no process list and no shell
		# history: asked for with echo off at a terminal, one line from a pipe
		# (how the hub's installer passes it).
		if [ -t 0 ]; then
			IFS= read -r -s -p 'Link token: ' token || die "Token input cancelled."
			printf '\n' >&2
		else
			IFS= read -r token || [ -n "$token" ] || die "--token-stdin read no token; pipe the line the hub printed."
		fi
		[ -n "$token" ] || die "--token-stdin read no token; paste the token the hub showed."
		exec "${compose[@]}" run --rm --no-deps -T backend --admin "hub-$sub" "$@" <<<"$token"
		;;
	create-user|reset-password)
		if [ "$password_stdin" -eq 1 ]; then
			exec "${compose[@]}" run --rm --no-deps -T backend --admin "$command" "$@"
		fi
		[ -t 0 ] || die "Interactive password input needs a terminal; use --password-stdin for piped input."
		printf 'New password: at least 12 characters, mixing three of uppercase, lowercase, digits and symbols.\n' >&2
		IFS= read -r -s -p 'Password: ' password || die "Password input cancelled."
		printf '\n' >&2
		IFS= read -r -s -p 'Confirm password: ' confirmation || die "Password input cancelled."
		printf '\n' >&2
		[ "$password" = "$confirmation" ] || die "Passwords do not match."
		printf '%s\n' "$password" | "${compose[@]}" run --rm --no-deps -T backend --admin "$command" "$@"
		;;
esac
