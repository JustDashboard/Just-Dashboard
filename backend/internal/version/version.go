// Package version carries the one string that says which build of the
// dashboard this is.
//
// It lives in its own package rather than in cmd/server because the answer is
// wanted in two unrelated places — the line the server logs at boot, and
// whatever later needs to identify this install to something else — and a
// constant in main is not reachable from either.
package version

// Version is the product version.
//
// A release lives in four files and `scripts/release.sh <version>` writes all
// of them: this line, the matching constant in frontend/src/lib/version.ts,
// the field npm demands in frontend/package.json, and the entry in
// backend/internal/selfupdate/changelog.json that says what the release
// actually contains. Two tests fail the run if any of them drift —
// version_test.go here, and TestChangelogHeadIsTheProductVersion next to the
// changelog — so there is no way to ship a UI claiming a version the server
// does not, and no way to ship a version with nothing to say about itself.
//
// That last one is not tidiness. Every install in the world decides whether to
// offer itself an update by comparing this constant against the changelog on
// the tracked branch, so a bump with no entry is a release nobody is told
// about, and an entry with no bump is every install offering itself an update
// it already has.
//
// It is a marketing version, not a semver contract: 0.5 is the dashboard as a
// complete single-server panel, 1.0 is when the API stops moving under people.
const Version = "0.7.0"
