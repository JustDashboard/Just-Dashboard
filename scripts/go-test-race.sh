#!/usr/bin/env bash
#
# Run packages under the race detector.
#
#   scripts/go-test-race.sh <processes> <package>...   (from backend/)
#
# With one process the packages go to a single `go test -race`, which runs
# them side by side. That is right for a package whose tests are parallel
# already and wrong for ./internal/api, whose seven hundred run one at a time:
# under the detector they are CPU-bound, so three of a runner's four cores sat
# idle. With more processes each package is compiled once and its tests are
# dealt by name across that many `go test` runs at once, which share the build
# cache. Names are read from the test files rather than from `go test -list`,
# which would compile the package once more just to print them; the two lists
# are the same.
#
# The latency budgets (`…StaysWithinItsBudgetAtReferenceScale`) are skipped
# here and asserted in the plain test run: the race detector multiplies a
# SQLite read by ten to twenty-five, so a budget measured under it measures the
# detector and the runner's load, not the read.

set -euo pipefail

processes=$1
shift

skip='StaysWithinItsBudgetAtReferenceScale'

if [ "$processes" -eq 1 ]; then
	exec go test -race -count=1 -json -skip "$skip" "$@"
fi

out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT

for pkg in "$@"; do
	mapfile -t names < <(
		grep -hoE '^func (Test|Fuzz)[A-Za-z0-9_]*\((t \*testing\.T|f \*testing\.F)\)' "$pkg"/*_test.go |
			sed -E 's/^func ([A-Za-z0-9_]+)\(.*/\1/'
	)

	go test -race -count=1 -run '^$' "$pkg" >/dev/null

	pids=()
	files=()
	for ((k = 0; k < processes; k++)); do
		slice=()
		for i in "${!names[@]}"; do
			if (((i % processes) == k)); then
				slice+=("${names[$i]}")
			fi
		done
		if [ "${#slice[@]}" -eq 0 ]; then
			continue
		fi
		pattern="^($(
			IFS='|'
			echo "${slice[*]}"
		))\$"
		file="$out/$(basename "$pkg").$k"
		go test -race -count=1 -json -run "$pattern" -skip "$skip" "$pkg" >"$file" &
		pids+=("$!")
		files+=("$file")
	done

	# Each process writes its own file so their JSON lines cannot interleave;
	# the gate fails if any of them did.
	status=0
	for pid in "${pids[@]}"; do
		wait "$pid" || status=1
	done
	cat "${files[@]}"
	if [ "$status" -ne 0 ]; then
		exit "$status"
	fi
done
