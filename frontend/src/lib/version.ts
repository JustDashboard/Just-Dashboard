/**
 * The product version, in one place.
 *
 * Every place the version is shown reads it from here. Bumping it is not a
 * job to do by hand: `scripts/release.sh <version>` writes this line, the
 * matching constant in `backend/internal/version/version.go`, the field npm
 * demands in `package.json`, and regenerates `CHANGELOG.md` from
 * `backend/internal/selfupdate/changelog.json` — which is where the release
 * notes go, and which the Go test run checks against this number.
 *
 * That check is what makes the update notice trustworthy: every install
 * compares its own version against the changelog published on the tracked
 * branch, so a version with no changelog entry is a release nobody hears
 * about.
 *
 * It is a marketing version, not a semver contract: 0.5 is the dashboard as
 * a complete single-server panel, 1.0 is when the API stops moving under
 * people. Add features, bump to 0.6.
 */
export const VERSION = "0.6.7"
