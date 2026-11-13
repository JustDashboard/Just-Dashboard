#!/usr/bin/env bash
#
# What the dependencies owe: the advisories that reach this code, and what has
# fallen behind.
#
#   scripts/deps-check.sh        (needs the network; from anywhere in the repository)
#
# Nothing ran this kind of check on a schedule, so dependency work happened
# when something broke: five advisories reached the backend at the 0.8.6 audit,
# two of them with a fix that had been out for weeks. scripts/release.sh names
# this as a step before a release is cut, and it is cheap to run in between.
#
# It fails on an advisory that reaches the code and is not listed below, and
# only reports what is behind, because a newer version is a choice and an
# advisory is not.

set -uo pipefail

cd "$(dirname "$0")/.."

# Advisories that reach the code and have no fixed version to move to, each
# with why it is carried. A line here is a decision: take it out when the
# reason goes away, and never add one for an advisory that has a fix.
#
#   GO-2026-4887, GO-2026-4883: github.com/docker/docker, in the daemon's
#   handling of authorisation plugins and plugin privileges. The dashboard
#   uses the module as a client and runs no plugin; the module has no fixed
#   release, and what ends the report is moving internal/dockerx to
#   github.com/moby/moby/client.
accepted="GO-2026-4887 GO-2026-4883"

status=0
section() { printf '\n== %s ==\n' "$*"; }

section "Advisories that reach the backend (govulncheck)"
report=$(cd backend && go run golang.org/x/vuln/cmd/govulncheck@latest ./... 2>&1)
ran=$?
printf '%s\n' "$report"
if [ "$ran" -ne 0 ] && ! grep -q '^Vulnerability #' <<<"$report"; then
	echo "govulncheck did not run, so nothing is known about the backend." >&2
	status=1
fi
unlisted=
for id in $(grep -E '^Vulnerability #[0-9]+: ' <<<"$report" | grep -oE 'GO-[0-9]+-[0-9]+' | sort -u); do
	case " $accepted " in
	*" $id "*) ;;
	*) unlisted="$unlisted $id" ;;
	esac
done
if [ -n "$unlisted" ]; then
	echo "Reaches the backend and is not accepted in scripts/deps-check.sh:$unlisted" >&2
	status=1
else
	echo "Nothing reaches the backend beyond what scripts/deps-check.sh accepts ($accepted)."
fi

section "Direct Go modules that are behind"
(cd backend && go list -m -u -f '{{if and (not .Indirect) .Update}}{{.Path}} {{.Version}} -> {{.Update.Version}}{{end}}' all) ||
	echo "go list could not ask for newer versions." >&2

if command -v bun >/dev/null 2>&1; then
	section "Advisories in the frontend's packages (bun audit)"
	(cd frontend && bun audit) || status=1
	section "Frontend packages that are behind (bun outdated)"
	(cd frontend && bun outdated) || true
else
	section "Frontend"
	echo "bun is not on PATH, so the frontend's packages were not checked." >&2
	status=1
fi

exit "$status"
