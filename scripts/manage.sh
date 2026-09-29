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
  reset-password USER                Set a temporary password and clear lockout
  revoke-sessions USER                Sign out every browser session for USER

Stack
  status                             Show containers and their health
  logs [backend|frontend|proxy]       Follow logs (default: backend; Ctrl+C exits)
  restart                            Recreate the stack with current .env settings

Roles: readonly, limited, admin
Password commands prompt twice with hidden input. For automation, append
--password-stdin and pipe one password line from a trusted source. Passwords
never belong in command arguments. New and reset passwords must be changed
at sign-in. Resetting preserves two-factor and disabled state, and revokes
browser sessions and API tokens. Account changes are audited as local root.

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
	users|status|restart) [ "$#" -eq 0 ] || die "$command does not accept arguments. See --help." ;;
	revoke-sessions) [ "$#" -eq 1 ] && [ -n "$1" ] || die "revoke-sessions needs one username. See --help." ;;
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

case "$command" in
	status) exec "${compose[@]}" ps --all ;;
	logs) exec "${compose[@]}" logs --tail 100 --follow "${1:-backend}" ;;
	restart)
		printf 'Recreating Just Dashboard with current .env settings...\n'
		# Compose restart keeps the old container environment after an .env edit.
		exec "${compose[@]}" up -d --force-recreate
		;;
	users|revoke-sessions)
		exec "${compose[@]}" run --rm --no-deps -T backend --admin "$command" "$@"
		;;
	create-user|reset-password)
		if [ "$password_stdin" -eq 1 ]; then
			exec "${compose[@]}" run --rm --no-deps -T backend --admin "$command" "$@"
		fi
		[ -t 0 ] || die "Interactive password input needs a terminal; use --password-stdin for piped input."
		printf 'Temporary password: at least 12 characters, mixing three of uppercase, lowercase, digits and symbols.\n' >&2
		IFS= read -r -s -p 'Password: ' password || die "Password input cancelled."
		printf '\n' >&2
		IFS= read -r -s -p 'Confirm password: ' confirmation || die "Password input cancelled."
		printf '\n' >&2
		[ "$password" = "$confirmation" ] || die "Passwords do not match."
		printf '%s\n' "$password" | "${compose[@]}" run --rm --no-deps -T backend --admin "$command" "$@"
		;;
esac
