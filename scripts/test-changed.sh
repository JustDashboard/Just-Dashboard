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
#     `bun test src` (under a second); ESLint on the whole tree when the rules
#     or the dependencies changed (package.json, bun.lock, tsconfig.json, the
#     ESLint configuration);
#   - `go build ./...` and `go vet` on the changed packages, then the tests
#     beside each changed Go file: those in test files sharing its name, or
#     the longest `_`-separated prefix of it that some test file shares
#     (`handlers_db_fleet.go` runs `handlers_db*_test.go`), and the whole
#     package only when no test file shares even its first word;
#   - the whole package for a change no test file can be matched to by name:
#     a file under backend/ that is not Go (testdata, a golden file, an
#     embedded template, the release changelog) runs the nearest package above
#     it, and a file anywhere that a package's tests read from outside it
#     (`frontend/src/lib/version.ts`, `docker-compose.yml`) runs that package.
#     Both are asked of `scripts/ci-plan.py`, so the two pickers cannot drift;
#   - for a change to backend/go.mod, every package whose imports, followed
#     all the way down, enter a module whose version line changed, and every
#     package when the `go` or `toolchain` line did;
#   - `bash -n` on each changed shell script, and the tests under scripts/
#     (`scripts/test_*.py`) that name a changed part of what an operator runs
#     or the stack is made from: install.sh, scripts/, docker-compose.yml,
#     deploy/, .env.example and the Dockerfiles. A test names a file by its
#     last component, as it must to run or read it, and a changed test runs
#     as itself;
#   - for a change under sdk/, the deployment API clients' own checks: `tsc`
#     and `bun test` in sdk/typescript, `go vet` and `go test` in sdk/go
#     (its own module, which the backend's packages do not reach);
#   - the browser specs that open a page the change renders. A changed file is
#     followed through `@/` imports up to the pages and layouts that use it,
#     and a spec is picked when it names one of those addresses. A module whose
#     pages span more than one section (`ui/`, `lib/`, `ChoiceCard`, the shell)
#     would pick most of the suite for a change that seldom breaks more than
#     its look, so it runs `design-system.spec.ts` and `navigation.spec.ts`
#     instead, which open every page. A module of one section that the shell
#     reads as well (the Databases engine registry draws the rail) keeps that
#     section's specs beside those two: its own pages are what it breaks
#     first. What every page is built from or served through (the
#     dependencies, the build's configuration and scripts, the request proxy)
#     runs those two as well. Changed specs, and the specs of a changed fixture, run as
#     themselves — a fixture followed through relative imports however deep,
#     so a table under fixtures/proxy/ reaches the spec that imports
#     proxy-fixtures.ts — and any UI change also runs the design-system spec.
#     `python3 scripts/test_test_changed.py` checks the fixture's picking and
#     the section's.
#
# Without [base], the base is whichever of origin/main and origin/patch/* the
# branch is fewest commits ahead of — the branch it was started from.
#
# The browser specs run against the frontend on port 43117, or the one
# JD_BROWSER_BASE_URL names, which must be serving a build of this tree.
# JD_BROWSER_WORKERS sets a positive worker count for memory-constrained hosts;
# unset, the existing Playwright configuration determines concurrency.
#
# JD_TEST_CHANGED_PLAN=1 prints what was picked and runs none of it.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

browser_workers=${JD_BROWSER_WORKERS:-}
if [ -n "$browser_workers" ] && ! [[ "$browser_workers" =~ ^[1-9][0-9]*$ ]]; then
	echo "JD_BROWSER_WORKERS must be a positive integer" >&2
	exit 2
fi

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

# The pages and layouts under app/ that reach this file through imports — by
# the `@/` alias or relative to the importing file — the file itself included
# when it is one.
pages_of() {
	local -A seen=()
	local queue=("$1") f spec module importer from
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
		# And through relative imports: the parts of the data grid and of the
		# diagram name each other as "./values", and a change to one of them
		# reaches a page only by way of the files beside it.
		module=${f%.*}
		module=${module%/index}
		while IFS= read -r importer; do
			while IFS= read -r from; do
				if [ "$(normalise "$(dirname "$importer")/$from")" = "$module" ]; then queue+=("$importer"); fi
			done < <(grep -oE 'from "\.\.?/[^"]+"' "$importer" | sed -E 's/^from "(.*)"$/\1/')
		done < <(grep -rlE --include='*.ts' --include='*.tsx' "from \"\.\.?/([^\"]*/)?$(basename "$module")\"" frontend/src || true)
	done
}

# The address a page or layout serves, with `(group)` segments dropped.
address_of() {
	local path=${1#frontend/src/app}
	path=${path%/*}
	path=$(sed -E 's#/\([^)]*\)##g' <<<"$path")
	echo "${path:-/}"
}

# The section a module is part of, for the directories that are one section's
# own. The rail and the palette read the Databases engine registry, so a change
# to it reaches every page — and it is still a database page it breaks first.
home_section() {
	case $1 in frontend/src/components/database/*) echo databases ;; esac
}

# Those of the pages given that are inside a section.
pages_under() {
	local section=$1 p
	shift
	for p in "$@"; do
		if [ "$(address_of "$p" | cut -d/ -f2)" = "$section" ]; then echo "$p"; fi
	done
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

# A path with its `.` and `..` segments folded away, as an import resolves it.
normalise() {
	local IFS=/ part out=()
	for part in $1; do
		case $part in
		"" | .) ;;
		..) [ "${#out[@]}" -gt 0 ] && unset 'out[-1]' ;;
		*) out+=("$part") ;;
		esac
	done
	echo "${out[*]}"
}

# The specs that reach a browser-test module through relative imports, with
# any number of fixture modules between them.
specs_importing() {
	local -A seen=()
	local queue=("${1%.ts}") module importer from
	while [ "${#queue[@]}" -gt 0 ]; do
		module=${queue[0]}
		queue=("${queue[@]:1}")
		[ -n "${seen[$module]:-}" ] && continue
		seen[$module]=1
		case $module in *.spec) echo "${module#frontend/}.ts" ;; esac
		while IFS= read -r importer; do
			while IFS= read -r from; do
				[ "$(normalise "$(dirname "$importer")/$from")" = "$module" ] && queue+=("${importer%.ts}")
			done < <(grep -oE 'from "\.\.?/[^"]+"' "$importer" | sed -E 's/^from "(.*)"$/\1/')
		done < <(grep -rlE --include='*.ts' "from \"\.\.?/([^\"]*/)?$(basename "$module")\"" frontend/tests/browser || true)
	done
}

lintable=()
specs=()
ui=
broad=
lint_all=
for f in "${changed[@]}"; do
	case $f in
	frontend/*.ts | frontend/*.tsx | frontend/*.js | frontend/*.mjs)
		[ -e "$f" ] && lintable+=("${f#frontend/}")
		;;
	esac
	# What the lint rules and the tools that run them are made of: a change
	# here can fail a file the change never touched.
	case $f in
	frontend/package.json | frontend/bun.lock | frontend/tsconfig.json | frontend/eslint.config.mjs | frontend/eslint-rules/*) lint_all=1 ;;
	esac
	# What every page is built from or served through, which no walk along
	# one page's imports finds.
	case $f in
	frontend/package.json | frontend/bun.lock | frontend/next.config.ts | frontend/playwright.config.ts | frontend/postcss.config.mjs | frontend/tsconfig.json | frontend/scripts/* | frontend/src/proxy.ts) broad=1 ;;
	esac
	case $f in
	frontend/src/*.tsx | frontend/src/*.css) ui=1 ;;
	esac
	case $f in
	frontend/tests/browser/*.spec.ts) specs+=("${f#frontend/}") ;;
	frontend/tests/browser/*.ts)
		mapfile -t -O "${#specs[@]}" specs < <(specs_importing "$f")
		;;
	frontend/src/app/layout.tsx | "frontend/src/app/(dashboard)/layout.tsx" | frontend/src/*.css) broad=1 ;;
	frontend/src/*.ts | frontend/src/*.tsx)
		[ -e "$f" ] || continue
		mapfile -t reached < <(pages_of "$f")
		sections=$(for p in "${reached[@]}"; do address_of "$p" | cut -d/ -f2; done | sort -u | wc -l)
		if [ "$sections" -gt 1 ]; then
			broad=1
			home=$(home_section "$f")
			[ -n "$home" ] || continue
			mapfile -t reached < <(pages_under "$home" "${reached[@]}")
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

# The tests under scripts/ that a changed file reaches: the test itself when
# it is one, and for a part of what an operator runs or the stack is made
# from, each test naming it by its last component (`manage.sh`,
# `docker-compose.yml`, `Caddyfile`), which is how a test must name what it
# runs or reads.
script_tests_for() {
	case $1 in
	scripts/test_*.py)
		if [ -e "$1" ]; then echo "$1"; fi
		;;
	scripts/* | install.sh | docker-compose.yml | .env.example | deploy/* | backend/Dockerfile | frontend/Dockerfile)
		grep -lF -- "$(basename "$1")" scripts/test_*.py || true
		;;
	esac
}

script_tests=()
shell_scripts=()
for f in "${changed[@]}"; do
	mapfile -t -O "${#script_tests[@]}" script_tests < <(script_tests_for "$f")
	case $f in
	*.sh) [ -e "$f" ] && shell_scripts+=("$f") ;;
	esac
done
if [ "${#script_tests[@]}" -gt 0 ]; then
	mapfile -t script_tests < <(printf '%s\n' "${script_tests[@]}" | sort -u)
fi

# The packages a change to go.mod reaches, as ./paths: every one (./...) when
# the go or toolchain line moved, and otherwise those whose imports, followed
# all the way down, enter a module whose version line changed. A bumped
# module nothing imports any more picks nothing.
packages_using_changed_modules() {
	local diff modules self
	diff=$(git diff "$fork" -- backend/go.mod)
	if grep -qE '^[+-](go|toolchain) ' <<<"$diff"; then
		echo ./...
		return
	fi
	modules=$(sed -n -E 's/^[+-][[:space:]]+([^[:space:]]+)[[:space:]]+v[^[:space:]]+.*$/\1/p' <<<"$diff" | sort -u)
	[ -n "$modules" ] || return 0
	self=$(cd backend && go list -m)
	(cd backend && go list -f '{{.ImportPath}}{{range .Deps}} {{.}}{{end}}{{range .TestImports}} {{.}}{{end}}{{range .XTestImports}} {{.}}{{end}}' ./...) |
		awk -v modules="$modules" -v self="$self" '
			BEGIN { count = split(modules, module, "\n") }
			{
				for (field = 2; field <= NF; field++)
					for (m = 1; m <= count; m++)
						if ($field == module[m] || index($field, module[m] "/") == 1) {
							print "." substr($1, length(self) + 1)
							next
						}
			}'
}

# The packages to run whole for changed files that are not Go: the nearest
# package above a file under backend/, and any package whose tests read the
# file from outside it. scripts/ci-plan.py already answers both for the
# workflow, so it is asked rather than copied.
packages_for_other_files() {
	python3 - "$root" "$@" <<'PY'
import importlib.util
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
spec = importlib.util.spec_from_file_location("ci_plan", root / "scripts/ci-plan.py")
ci_plan = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ci_plan)
touched, _, read = ci_plan.go_affected(ci_plan.go_packages(root), sys.argv[2:])
for package in sorted(touched | read):
    print("./" + package.removeprefix("backend/"))
PY
}

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
				sed -E 's/^func ([A-Za-z0-9_]+)\(.*/\1/' || true
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
other=()
modules_changed=
for f in "${changed[@]}"; do
	case $f in
	backend/go.mod) modules_changed=1 ;;
	backend/go.sum | backend/*.go) ;;
	*) other+=("$f") ;;
	esac
done
whole=()
if [ "${#other[@]}" -gt 0 ]; then
	mapfile -t whole < <(packages_for_other_files "${other[@]}")
fi
if [ -n "$modules_changed" ]; then
	mapfile -t -O "${#whole[@]}" whole < <(packages_using_changed_modules)
fi
for pkg in "${whole[@]}"; do
	if [ "$pkg" = ./... ] || [ -d "backend/${pkg#./}" ]; then tests[$pkg]=""; fi
done
# Every package is every package: naming the others beside it would run them twice.
if [ "${tests[./...]-unset}" = "" ]; then
	tests=([./...]="")
fi
packages=("${!tests[@]}")

if [ -n "${JD_TEST_CHANGED_PLAN:-}" ]; then
	# What was picked, and nothing run: a look before a long wait, and what
	# the tests of this script read.
	echo "Shell syntax: ${shell_scripts[*]:-}"
	echo "Script tests: ${script_tests[*]:-}"
	if [ -n "$lint_all" ]; then echo "Lint: ."; else echo "Lint: ${lintable[*]:-}"; fi
	printf 'Go packages:'
	if [ "${#packages[@]}" -gt 0 ]; then
		while read -r pkg; do
			if [ -n "${tests[$pkg]}" ]; then printf ' %s(by name)' "$pkg"; else printf ' %s' "$pkg"; fi
		done < <(printf '%s\n' "${packages[@]}" | sort)
	fi
	printf '\n'
	echo "Browser specs: ${specs[*]:-}"
	exit 0
fi

for f in "${shell_scripts[@]}"; do
	bash -n "$f"
done
if [ "${#script_tests[@]}" -gt 0 ]; then
	echo "Script tests: ${script_tests[*]}"
	for t in "${script_tests[@]}"; do
		python3 "$t"
	done
fi

# The type-check keeps what it learnt about each file, by the file's contents,
# in frontend/tsconfig.tsbuildinfo, and a new worktree has none: its first
# check was over three minutes and 2.4 GB of work that another worktree had
# already done. One copy is kept in the git directory every worktree of this
# clone shares. A worktree with none of its own starts from that copy, and a
# check that passes leaves its own there. The file holds no absolute path, and
# a copy from another commit costs only the files that differ and what depends
# on them, which is what CI's cache of the same file does (verify.yml).
typecheck() {
	local cache
	cache="$(git rev-parse --path-format=absolute --git-common-dir)/jd-cache/tsconfig.tsbuildinfo"
	if [ ! -f tsconfig.tsbuildinfo ] && [ -f "$cache" ]; then
		cp "$cache" tsconfig.tsbuildinfo
	fi
	bunx tsc --noEmit
	if [ -f tsconfig.tsbuildinfo ]; then
		mkdir -p "${cache%/*}"
		cp tsconfig.tsbuildinfo "$cache.$$" && mv "$cache.$$" "$cache"
	fi
}

if [ "${#lintable[@]}" -gt 0 ] || [ -n "$lint_all" ]; then
	(
		cd frontend
		if [ "${#lintable[@]}" -gt 0 ]; then bunx prettier --check "${lintable[@]}"; fi
		if [ -n "$lint_all" ]; then
			bunx eslint --no-warn-ignored .
		else
			bunx eslint "${lintable[@]}"
		fi
		typecheck
		bun test src
	)
fi

sdk_go=
sdk_ts=
for f in "${changed[@]}"; do
	case $f in
	sdk/go/*) sdk_go=1 ;;
	sdk/typescript/*) sdk_ts=1 ;;
	esac
done
if [ -n "$sdk_ts" ]; then
	# The client has no dependencies of its own; the frontend's TypeScript
	# compiler checks it.
	if [ ! -x frontend/node_modules/.bin/tsc ]; then
		echo "sdk/typescript: run bun install --frozen-lockfile in frontend/ first, for tsc" >&2
		exit 1
	fi
	(cd sdk/typescript && ../../frontend/node_modules/.bin/tsc -p . && bun test)
fi
if [ -n "$sdk_go" ]; then
	(cd sdk/go && GOWORK=off go vet ./... && GOWORK=off go test ./...)
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
	browser_args=()
	if [ -n "$browser_workers" ]; then
		browser_args+=("--workers=$browser_workers")
	fi
	(cd frontend && bunx playwright test "${specs[@]}" "${browser_args[@]}") || status=$?
fi
if [ -n "$gotest" ]; then
	wait "$gotest" || status=$?
fi
exit "$status"
