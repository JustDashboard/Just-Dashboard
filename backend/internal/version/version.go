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
const Version = "0.8.6"

// Commit is the short commit this binary was built from, when that is not the
// release's own commit. Empty in a build of a release.
//
// Version alone says which release the code started from, and until a release
// is cut that is the last one: a server running a patch branch reported 0.8.5
// in the wordmark, in `jd version` and in every bug report, exactly as a
// server running the real 0.8.5 did. The build stamps this with
// `-ldflags -X` (backend/Dockerfile's JD_COMMIT), and the things that build
// the image, which are install.sh, an update and a rebuild from Settings, pass
// it only when the checkout is not on the commit the release's tag names. A
// plain `docker compose build` passes nothing and is not told apart.
//
// A variable because the linker has to be able to set it.
var Commit string

// Build is the version as a person reporting a problem should quote it:
// "0.8.5" for a release, "0.8.5+98c9349" for anything else built from 0.8.5's
// line. Version stays the thing to compare; this is only ever shown.
func Build() string {
	if Commit == "" {
		return Version
	}
	return Version + "+" + Commit
}
