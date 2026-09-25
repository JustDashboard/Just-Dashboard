#!/usr/bin/env bash
#
# Run the checks a change can break, and nothing else.
#
#   scripts/test-changed.sh [base]   (from anywhere in the repository)
#
# The whole browser suite and `go test ./...` took long enough that a change
# waited on them for minutes to learn about pages and packages it never
# touched. This reads the diff against the branch the work started from —
# committed, staged, unstaged and untracked alike — and runs:
#
#   - Prettier and ESLint on the changed frontend files, `tsc --noEmit`, and
#     `bun test src` (under a second);
#   - `go build ./...` and `go vet` on the changed packages, then the tests
#     beside each changed Go file: those in test files sharing its name, or
#     the longest `_`-separated prefix of it that some test file shares
#     (`handlers_db_fleet.go` runs `handlers_db*_test.go`), and the whole
#     package only when no test file shares even its first word;
#   - the browser specs that open a page the change renders. A changed file is
#     followed through `@/` imports up to the pages and layouts that use it,
#     and a spec is picked when it names one of those addresses. A module whose
#     pages span more than one section (`ui/`, `lib/`, `ChoiceCard`, the shell)
#     would pick most of the suite for a change that seldom breaks more than
#     its look, so it runs `design-system.spec.ts` and `navigation.spec.ts`
#     instead, which open every page. Changed specs, and the specs of a changed
#     fixture, run as themselves, and any UI change also runs the design-system
#     spec.
#
# Without [base], the base is whichever of origin/main and origin/patch/* the
# branch is fewest commits ahead of — the branch it was started from.
#
# The browser specs run against the frontend on port 43117, or the one
# JD_BROWSER_BASE_URL names, which must be serving a build of this tree.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

base=${1:-}
if [ -z "$base" ]; then
	fewest=
	for ref in $(git for-each-ref --format='%(refname:short)' refs/remotes/origin/main 'refs/remotes/origin/patch/*'); do
		ahead=$(git rev-list --count "$ref..HEAD")
		if [ -z "$fewest" ] || [ "$ahead" -lt "$fewest" ]; then
			base=$ref
			fewest=$ahead
		fi
	done
fi
fork=$(git merge-base "$base" HEAD)

mapfile -t changed < <(
	{
		git diff --name-only "$fork"
		git ls-files --others --exclude-standard
	} | sort -u
)
if [ "${#changed[@]}" -eq 0 ]; then
	echo "Nothing changed against $base."
	exit 0
fi
echo "Against $base: ${#changed[@]} changed files."

# The pages and layouts under app/ that reach this file through `@/` imports,
# the file itself included when it is one.
pages_of() {
	local -A seen=()
	local queue=("$1") f spec
	while [ "${#queue[@]}" -gt 0 ]; do
		f=${queue[0]}
		queue=("${queue[@]:1}")
		[ -n "${seen[$f]:-}" ] && continue
		seen[$f]=1
		case $f in frontend/src/app/*/page.tsx | frontend/src/app/*/layout.tsx | frontend/src/app/page.tsx) echo "$f" ;; esac
		spec=${f#frontend/src/}
		spec=${spec%.*}
		spec=${spec%/index}
		mapfile -t -O "${#queue[@]}" queue < <(
			grep -rlF --include='*.ts' --include='*.tsx' "from \"@/$spec\"" frontend/src || true
		)
	done
}

# The address a page or layout serves, with `(group)` segments dropped.
address_of() {
	local path=${1#frontend/src/app}
	path=${path%/*}
	path=$(sed -E 's#/\([^)]*\)##g' <<<"$path")
	echo "${path:-/}"
}

# The specs naming an address. A `[segment]` matches whatever a spec puts
# there, `${id}` included; a layout matches every address under it.
specs_naming() {
	local address=$1 kind=$2 pattern end
	pattern=$(sed -E -e 's#\[\.\.\.[^]]*\]#[^"'"'"'`?]*#g' -e 's#\[[^]]*\]#[^/"'"'"'`?]+#g' <<<"$address")
	end='["'"'"'`?#]|\$\{'
	[ "$kind" = layout ] && end="$end|/"
	grep -lE "[\"'\`]$pattern($end)" frontend/tests/browser/*.spec.ts | sed 's#^frontend/##' || true
}

lintable=()
specs=()
ui=
broad=
for f in "${changed[@]}"; do
	case $f in
	frontend/*.ts | frontend/*.tsx | frontend/*.js | frontend/*.mjs)
		[ -e "$f" ] && lintable+=("${f#frontend/}")
		;;
	esac
	case $f in
	frontend/src/*.tsx | frontend/src/*.css) ui=1 ;;
	esac
	case $f in
	frontend/tests/browser/*.spec.ts) specs+=("${f#frontend/}") ;;
	frontend/tests/browser/*.ts)
		mapfile -t -O "${#specs[@]}" specs < <(
			grep -lF "from \"./$(basename "${f%.ts}")\"" frontend/tests/browser/*.spec.ts | sed 's#^frontend/##' || true
		)
		;;
	frontend/src/app/layout.tsx | "frontend/src/app/(dashboard)/layout.tsx" | frontend/src/*.css) broad=1 ;;
	frontend/src/*.ts | frontend/src/*.tsx)
		[ -e "$f" ] || continue
		mapfile -t reached < <(pages_of "$f")
		sections=$(for p in "${reached[@]}"; do address_of "$p" | cut -d/ -f2; done | sort -u | wc -l)
		if [ "$sections" -gt 1 ]; then
			broad=1
			continue
		fi
		for p in "${reached[@]}"; do
			mapfile -t -O "${#specs[@]}" specs < <(specs_naming "$(address_of "$p")" "$(basename "$p" .tsx)")
		done
		;;
	esac
done
[ -n "$broad" ] && specs+=(tests/browser/design-system.spec.ts tests/browser/navigation.spec.ts)
[ -n "$ui" ] && specs+=(tests/browser/design-system.spec.ts)
if [ "${#specs[@]}" -gt 0 ]; then
	mapfile -t specs < <(printf '%s\n' "${specs[@]}" | sort -u | while read -r s; do [ -e "frontend/$s" ] && echo "$s"; done)
fi

# The tests beside one Go file, by name, or nothing when the whole package
# should run.
tests_beside() {
	local dir stem files
	dir=$(dirname "$1")
	stem=$(basename "$1" .go)
	stem=${stem%_test}
	while :; do
		files=("$dir/$stem"*_test.go)
		if [ -e "${files[0]}" ]; then
			grep -hoE '^func (Test|Fuzz)[A-Za-z0-9_]*\((t \*testing\.T|f \*testing\.F)\)' "${files[@]}" |
				sed -E 's/^func ([A-Za-z0-9_]+)\(.*/\1/'
			return
		fi
		[[ $stem == *_* ]] || return 0
		stem=${stem%_*}
	done
}

declare -A tests=()
for f in "${changed[@]}"; do
	case $f in backend/*.go) ;; *) continue ;; esac
	dir=$(dirname "$f")
	[ -d "$dir" ] || continue
	pkg=./${dir#backend/}
	[ "${tests[$pkg]-unset}" = "" ] && continue
	names=$(tests_beside "$f" | tr '\n' '|')
	if [ -z "$names" ]; then
		tests[$pkg]=""
	else
		tests[$pkg]="${tests[$pkg]:-}${tests[$pkg]:+|}${names%|}"
	fi
done
packages=("${!tests[@]}")

if [ "${#lintable[@]}" -gt 0 ]; then
	(
		cd frontend
		bunx prettier --check "${lintable[@]}"
		bunx eslint "${lintable[@]}"
		bunx tsc --noEmit
		bun test src
	)
fi

gotest=
if [ "${#packages[@]}" -gt 0 ]; then
	echo "Go packages: ${packages[*]}"
	(cd backend && go build ./... && go vet "${packages[@]}")
	# The packages' tests and the browser specs share nothing, so they run at
	# once rather than one after the other.
	(
		cd backend
		for pkg in "${packages[@]}"; do
			if [ -n "${tests[$pkg]}" ]; then
				go test -run "^(${tests[$pkg]})\$" "$pkg"
			else
				go test "$pkg"
			fi
		done
	) &
	gotest=$!
fi

status=0
if [ "${#specs[@]}" -gt 0 ]; then
	echo "Browser specs: ${specs[*]}"
	(cd frontend && bunx playwright test "${specs[@]}") || status=$?
fi
if [ -n "$gotest" ]; then
	wait "$gotest" || status=$?
fi
exit "$status"
