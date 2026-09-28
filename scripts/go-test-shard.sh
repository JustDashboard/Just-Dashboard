#!/usr/bin/env bash
#
# Run one shard of the race gate.
#
#   scripts/go-test-shard.sh <shard> <of> <package>...   (from backend/)
#
# Under the race detector ./internal/api takes nine minutes and ./internal/deploy
# five, one package at a time, so the gate was the longest thing CI did. Each
# package's tests are split by name across <of> shards — every <of>th test, in
# declaration order — and each shard is its own parallel job. Names are read
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
	pattern="^($(
		IFS='|'
		echo "${picked[*]}"
	))\$"
	go test -race -count=1 -json -run "$pattern" -skip "$skip" "$pkg"
done
