#!/usr/bin/env bash
#
# Cut a release.
#
#   scripts/release.sh 0.6
#
# A version of Just Dashboard lives in six files, and every install in the
# world decides whether it is behind by comparing two of them. So this exists
# to make bumping a release one action rather than six, and the Go test suite
# fails the build if any of them ever drift apart:
#
#   backend/internal/version/version.go          what the server logs and reports
#   frontend/src/lib/version.ts                  what the wordmark shows
#   frontend/package.json                        because npm demands the field
#   README.md                                    what the repository claims to be
#   backend/internal/selfupdate/changelog.json   what the release *contains*
#   docs/api/deploy-openapi.json                 what the SDKs are generated from
#
# The seventh place is the tag v<version>, which this does not make:
# .github/workflows/release.yml tags the merge once Verify has passed on it,
# and only while main's changelog still names that version as its newest. So
# before writing anything this asks origin whether every version already on
# main has its tag, and refuses while one does not (scripts/release-tags.sh):
# cutting the next release is the last chance to publish the one before it.
# A build of any commit but the tagged one says so by itself, as 0.6+98c9349
# (backend/internal/version, scripts/build-stamp.sh).
#
# The changelog is the one this cannot write for you, and it is deliberately
# first: a release with no release notes is a notification an operator cannot
# act on, and "write it afterwards" means never. Add the entry, then run this.
#
# CHANGELOG.md is generated from the same file, so nothing is written twice.

set -euo pipefail

cd "$(dirname "$0")/.."

if [ -t 1 ] && [ "$(tput colors 2>/dev/null || echo 0)" -ge 8 ]; then
	BOLD=$(tput bold); RESET=$(tput sgr0)
	GREEN=$(tput setaf 2); RED=$(tput setaf 1); DIM=$(tput dim)
else
	BOLD=""; RESET=""; GREEN=""; RED=""; DIM=""
fi
ok()  { printf '  %s✓%s %s\n' "$GREEN" "$RESET" "$*"; }
die() { printf '\n%serror:%s %s\n' "$RED" "$RESET" "$*" >&2; exit 1; }

VERSION="${1:-}"
[ -n "$VERSION" ] || die "usage: scripts/release.sh <version>   (e.g. scripts/release.sh 0.6)"
VERSION="${VERSION#v}"

# Two or three numeric components. The product version is a marketing version,
# not a semver contract — but it is compared numerically by every install, so
# anything this cannot compare must not ship.
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+(\.[0-9]+)?$ ]] \
	|| die "\"$VERSION\" is not a version this scheme can compare; use 0.6 or 0.6.1"

CURRENT=$(sed -n 's/^const Version = "\(.*\)"$/\1/p' backend/internal/version/version.go)
[ -n "$CURRENT" ] || die "could not read the current version out of backend/internal/version/version.go"

printf '\n%sReleasing Just Dashboard %s → %s%s\n\n' "$BOLD" "$CURRENT" "$VERSION" "$RESET"

# Asked of origin, never assumed; scripts/release-tags.sh says why.
# shellcheck source=scripts/release-tags.sh
source scripts/release-tags.sh
untagged=$(jd_untagged_releases . "$VERSION") \
	|| die "could not check that every release on main is tagged (see above); cut a release with origin in reach"
if [ -n "$untagged" ]; then
	die "merged into main but never tagged, so never published: $(printf '%s\n' "$untagged" | paste -sd ' ' -)

Installs are offered a version only once origin has its tag v<version>, and
.github/workflows/release.yml tags only the version main's changelog names as
latest: once $VERSION reaches main, nothing will tag a version it moved past.

Find the Verify run of that merge: gh run list --workflow verify.yml --branch main
  still running   wait for it; Release follows when it passes
  cancelled       re-run it (gh run rerun <id>); Release follows when it passes
  failed          fix main first; Release follows the run of the fix
If the operator decides to leave a version unpublished instead, add it and the
reason to jd_known_untagged_releases in scripts/release-tags.sh."
fi
ok "every release on main is tagged but those known not to be: $(jd_known_untagged_releases | cut -f1 | paste -sd ' ' -) (scripts/release-tags.sh)"

# The changelog is checked before anything is written, so a run that stops here
# leaves the tree exactly as it found it.
( cd backend && go run ./scripts/gen-changelog.go -expect "$VERSION" -out ../CHANGELOG.md ) \
	|| die "the changelog does not describe $VERSION yet — see above"
ok "CHANGELOG.md regenerated"

# npm wants three components and the product version usually has two. The
# release is what has to agree; the trailing zero is npm's problem.
NPM_VERSION="$VERSION"
[[ "$NPM_VERSION" =~ ^[0-9]+\.[0-9]+$ ]] && NPM_VERSION="$VERSION.0"

sed -i.bak "s/^const Version = \".*\"$/const Version = \"$VERSION\"/" \
	backend/internal/version/version.go
sed -i.bak "s/^export const VERSION = \".*\"$/export const VERSION = \"$VERSION\"/" \
	frontend/src/lib/version.ts
sed -i.bak "0,/\"version\": \".*\"/s//\"version\": \"$NPM_VERSION\"/" \
	frontend/package.json
# The README is the version somebody reads before they install anything, and
# it is the one nothing used to write — which is how the front page of the
# repository sat at 0.5.1 while the product shipped 0.5.8. It is rewritten here
# and pinned by the version test, so it cannot drift again.
sed -i.bak "s/^\*\*Version [0-9][0-9.]*\*\*/**Version $VERSION**/" README.md
rm -f backend/internal/version/version.go.bak frontend/src/lib/version.ts.bak \
	frontend/package.json.bak README.md.bak
ok "version constants bumped"

# The API description is generated from the build, so its info.version is
# whatever version.go says when it is written. Nothing rewrote it at a release,
# which is how it sat at 0.8.4 while the product shipped 0.8.5.
( cd backend && JD_UPDATE_OPENAPI=1 go test ./internal/api -run '^TestTheCommittedDeployOpenAPIDescriptionIsCurrent$' >/dev/null ) \
	|| die "docs/api/deploy-openapi.json could not be regenerated; run: cd backend && JD_UPDATE_OPENAPI=1 go test ./internal/api -run TestTheCommittedDeployOpenAPIDescriptionIsCurrent"
ok "docs/api/deploy-openapi.json regenerated"

# The tests are the enforcement, not this script: they are what fails on a
# release cut by hand, or by an agent that edited four files and forgot the
# fifth.
( cd backend && go test ./internal/version/ ./internal/selfupdate/ >/dev/null ) \
	|| die "the version tests do not pass; run: cd backend && go test ./internal/version/ ./internal/selfupdate/"
ok "version and changelog agree"

cat <<EOF

${BOLD}Released $VERSION locally.${RESET} What is left:

  ${DIM}# read the diff — six files and the generated changelog${RESET}
  git diff --stat

  ${DIM}# advisories that reach the code and what is behind; needs the network${RESET}
  scripts/deps-check.sh

  ${DIM}# move the base images and Caddy to what their tags name today, then build${RESET}
  ${DIM}# both images, which nothing else does before an operator's server${RESET}
  scripts/pin-images.py && docker compose build

  ${DIM}# the checks the diff reaches and both builds, as CONTRIBUTING requires${RESET}
  scripts/test-changed.sh
  (cd backend && go build ./...)
  (cd frontend && bun run build)

  git commit -am "Release $VERSION"
  ${DIM}# push the release branch and open its pull request into main${RESET}

Merging into main does not release $VERSION by itself. Once Verify passes on
that merge, .github/workflows/release.yml tags it v$VERSION and publishes the
GitHub release from this changelog entry, and the tag is what installs wait
for: they are offered $VERSION, and install exactly that commit, only once
v$VERSION exists. They look every two hours and whenever the dashboard is
opened. If Verify fails or is cancelled, nothing is tagged and nothing is
offered. A later green run on main tags it only while main still carries
$VERSION as its newest release, so publish it before the next version is cut;
this script refuses to cut one while $VERSION is on main without its tag.
Until main's run of the merge has finished, push its commit to no other
branch: a run of the same commit on a new patch branch cancels it.
EOF
