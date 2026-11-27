#!/usr/bin/env bash
# Sourced by scripts/release.sh; asks origin and changes nothing but what a
# fetch of main writes.

# A version reaches installs only once origin has its tag v<version>, and
# .github/workflows/release.yml makes that tag for one version only: the one
# main's changelog names as latest, once Verify passes on that commit. A merge
# whose run failed or was cancelled is left untagged, and once a later version
# reaches main nothing will tag it. 0.8.5 and 0.8.7 went that way and nobody
# saw it until an audit. Cutting the next release is the last point at which
# the workflow can still publish the earlier one, so that is where this looks.

# The first version the workflow tagged. Nothing before it was ever tagged.
JD_FIRST_TAGGED_RELEASE=0.7.2

# The versions that reached main and were left without a tag, each with why,
# one per line as <version><tab><why>. Without this list the check would
# refuse every release after them. Adding one is the operator's decision,
# taken instead of publishing it; tagging an old version later is theirs too.
jd_known_untagged_releases() {
	printf '%s\t%s\n' \
		0.8.5 "Verify on its merge into main failed: the Streamlit fixture installed whatever was newest and an upstream release broke it (pinned in 0.8.6, 0fdb28ff7), and 0.8.6 reached main before a run of 0.8.5 passed" \
		0.8.7 "Verify on its merge into main was cancelled: the same commit pushed to patch/0.9.0 shares the run's concurrency group in .github/workflows/verify.yml, and 0.9.0 reached main before a run of 0.8.7 passed"
}

# jd_untagged_releases [checkout] [version] prints, one per line, each version
# in the changelog of origin's main that origin has no tag v<version> for,
# leaving out the version being cut, any before JD_FIRST_TAGGED_RELEASE and
# any jd_known_untagged_releases names. It fails, saying why on stderr, when
# origin cannot be asked: a check that could not look has not passed.
jd_untagged_releases() {
	local dir="${1:-.}" cutting="${2:-}" refs main manifest known v
	if ! refs=$(git -C "$dir" ls-remote --refs origin refs/heads/main 'refs/tags/v*' 2>&1); then
		printf 'could not ask origin which releases are tagged:\n%s\n' "$refs" >&2
		return 2
	fi
	main=$(printf '%s\n' "$refs" | awk '$2 == "refs/heads/main" { print $1 }')
	if [ -z "$main" ]; then
		echo "origin has no branch main to read the released versions from" >&2
		return 2
	fi
	# ls-remote names the commit; the clone may not have it yet.
	if ! git -C "$dir" cat-file -e "$main^{commit}" 2>/dev/null &&
		! git -C "$dir" fetch --quiet origin main >&2; then
		echo "could not fetch main from origin to read the released versions" >&2
		return 2
	fi
	if ! manifest=$(git -C "$dir" show "$main:backend/internal/selfupdate/changelog.json"); then
		echo "main on origin has no backend/internal/selfupdate/changelog.json to read" >&2
		return 2
	fi
	known=$'\n'$(jd_known_untagged_releases | cut -f1)$'\n'
	for v in $(printf '%s\n' "$manifest" | grep -o '"version": *"[^"]*"' | sed 's/.*"\([^"]*\)"$/\1/'); do
		[ "$v" = "$cutting" ] && continue
		printf '%s\n%s\n' "$JD_FIRST_TAGGED_RELEASE" "$v" | sort -V -C || continue
		case $known in *$'\n'"$v"$'\n'*) continue ;; esac
		printf '%s\n' "$refs" | awk -v tag="refs/tags/v$v" '$2 == tag { found = 1 } END { exit !found }' && continue
		echo "$v"
	done
}
