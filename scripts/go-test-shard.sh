#!/usr/bin/env bash
#
# Run one shard of the race gate.
#
#   scripts/go-test-shard.sh <shard> <of> <package>...   (from backend/)
#
# Under the race detector ./internal/api takes nine minutes and ./internal/deploy
# five, one package at a time, so the gate was the longest thing CI did. Each
# package's tests are split by name across <of> shards — every <of>th test, in
# declaration order — and each shard is its own parallel job. Within a shard the
# tests run in several processes at once (see `procs` below). Names are read
# from the test files rather than from `go test -list`, which would compile the
# package once more just to print them; the two lists are the same.
#
# The latency budgets (`…StaysWithinItsBudgetAtReferenceScale`) are skipped
# here and asserted in the plain test run: the race detector multiplies a
# SQLite read by ten to twenty-five, so a budget measured under it measures the
# detector and the runner's load, not the read.

set -euo pipefail

shard=$1
of=$2
shift 2

skip='StaysWithinItsBudgetAtReferenceScale'

# Under the race detector the tests are CPU-bound and run one at a time, so a
# four-core runner spends three of them idle. Each shard therefore compiles
# its package once and runs its tests as `procs` processes side by side; they
# share the build cache, so only the first pays for the instrumented compile.
procs=4

out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT

for pkg in "$@"; do
	if [ "$of" -eq 1 ]; then
		go test -race -count=1 -json -skip "$skip" "$pkg"
		continue
	fi
	mapfile -t names < <(
		grep -hoE '^func (Test|Fuzz)[A-Za-z0-9_]*\((t \*testing\.T|f \*testing\.F)\)' "$pkg"/*_test.go |
			sed -E 's/^func ([A-Za-z0-9_]+)\(.*/\1/'
	)
	picked=()
	for i in "${!names[@]}"; do
		if (((i % of) == shard - 1)); then
			picked+=("${names[$i]}")
		fi
	done
	if [ "${#picked[@]}" -eq 0 ]; then
		continue
	fi

	go test -race -count=1 -run '^$' "$pkg" >/dev/null

	pids=()
	files=()
	for ((k = 0; k < procs; k++)); do
		slice=()
		for i in "${!picked[@]}"; do
			if (((i % procs) == k)); then
				slice+=("${picked[$i]}")
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
