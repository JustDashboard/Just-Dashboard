package selfupdate

import (
	"os"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/version"
)

// The embedded changelog is held to the strict parser, which refuses more than
// the one that reads it off the network does, so a file that an install could
// not show breaks the test run first.
func TestLocalManifestIsValid(t *testing.T) {
	m, err := ParseManifest(changelogJSON)
	if err != nil {
		t.Fatalf("%s is not a valid changelog: %v", ManifestPath, err)
	}
	if len(m.Releases) == 0 {
		t.Fatal("the changelog lists no releases")
	}
	if Local().Latest != m.Latest {
		t.Fatalf("Local() disagrees with ParseManifest about the latest release")
	}
}

// The rule that makes releasing one action rather than four: the newest entry
// in the changelog *is* the version this build claims to be. Bumping the
// constant without writing the entry fails here, and so does writing the entry
// without bumping the constant — which is the direction that would otherwise
// ship an install permanently offering itself an update it already has.
func TestChangelogHeadIsTheProductVersion(t *testing.T) {
	m := Local()
	if Compare(m.Latest, version.Version) != 0 {
		t.Fatalf("the changelog's newest release is %s but this build is %s — "+
			"a release bumps internal/version, frontend/src/lib/version.ts, "+
			"frontend/package.json and %s together (scripts/release.sh does all four)",
			m.Latest, version.Version, ManifestPath)
	}
	head := m.Releases[0]
	if strings.TrimSpace(head.Summary) == "" {
		t.Errorf("release %s has no summary; the sidebar notice has nothing to say", head.Version)
	}
}

// CHANGELOG.md is generated from the same file for people reading the
// repository rather than the dashboard. It is checked loosely — that the
// current release appears in it at all — because the generator owns its
// formatting and pinning that here would make every wording change a test
// failure.
func TestRootChangelogMentionsThisRelease(t *testing.T) {
	b, err := os.ReadFile("../../../CHANGELOG.md")
	if err != nil {
		t.Skipf("no CHANGELOG.md to compare against: %v", err)
	}
	want := "## " + Local().Latest
	if !strings.Contains(string(b), want) {
		t.Fatalf("CHANGELOG.md has no %q section; run scripts/release.sh to regenerate it", want)
	}
}

func TestParseManifestRejectsWhatItCannotRender(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string
	}{
		{"no releases", `{"releases":[]}`, "no releases"},
		{"unknown kind", `{"releases":[{"version":"1.0","date":"2026-01-01","title":"x",
			"changes":[{"kind":"improved","text":"y"}]}]}`, "not one of"},
		{"bad date", `{"releases":[{"version":"1.0","date":"January","title":"x",
			"changes":[{"kind":"added","text":"y"}]}]}`, "YYYY-MM-DD"},
		{"unversioned", `{"releases":[{"version":"next","date":"2026-01-01","title":"x",
			"changes":[{"kind":"added","text":"y"}]}]}`, "is not a version"},
		{"duplicate", `{"releases":[
			{"version":"1.0","date":"2026-01-01","title":"x","changes":[{"kind":"added","text":"y"}]},
			{"version":"1.0","date":"2026-01-02","title":"z","changes":[{"kind":"added","text":"y"}]}]}`, "twice"},
		{"no changes", `{"releases":[{"version":"1.0","date":"2026-01-01","title":"x","changes":[]}]}`, "no changes"},
		{"empty text", `{"releases":[{"version":"1.0","date":"2026-01-01","title":"x",
			"changes":[{"kind":"added","text":"  "}]}]}`, "no text"},
		{"breaking with nothing behind it", `{"releases":[{"version":"1.0","date":"2026-01-01","title":"x",
			"breaking":true,"changes":[{"kind":"added","text":"y"}]}]}`, "says nothing about what to do"},
		{"latest disagrees", `{"latest":"2.0","releases":[{"version":"1.0","date":"2026-01-01","title":"x",
			"changes":[{"kind":"added","text":"y"}]}]}`, "newest release is"},
		{"stray field", `{"releases":[{"version":"1.0","date":"2026-01-01","title":"x",
			"notes":"oops","changes":[{"kind":"added","text":"y"}]}]}`, "not valid JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseManifest([]byte(tc.json))
			if err == nil {
				t.Fatalf("accepted a changelog it should have refused")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// The array's order is how it was edited, not what is newest. A release
// appended to the end of the file must still be found as the head.
func TestParseManifestSortsNewestFirst(t *testing.T) {
	m, err := ParseManifest([]byte(`{"releases":[
		{"version":"0.5","date":"2026-01-01","title":"a","changes":[{"kind":"added","text":"x"}]},
		{"version":"0.10","date":"2026-03-01","title":"c","changes":[{"kind":"added","text":"x"}]},
		{"version":"0.6","date":"2026-02-01","title":"b","changes":[{"kind":"added","text":"x"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Latest != "0.10" {
		t.Fatalf("latest is %q, want 0.10 — string ordering has crept back in", m.Latest)
	}
	got := []string{m.Releases[0].Version, m.Releases[1].Version, m.Releases[2].Version}
	want := []string{"0.10", "0.6", "0.5"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order %v, want %v", got, want)
		}
	}
}

// Since is what "View changes" shows, and showing only the newest release to
// somebody three versions behind hides the two that explain what happened to
// their install.
func TestSinceListsEveryInterveningRelease(t *testing.T) {
	m, err := ParseManifest([]byte(`{"releases":[
		{"version":"0.8","date":"2026-04-01","title":"d","changes":[{"kind":"added","text":"x"}]},
		{"version":"0.7","date":"2026-03-01","title":"c","changes":[{"kind":"added","text":"x"}]},
		{"version":"0.6","date":"2026-02-01","title":"b","changes":[{"kind":"added","text":"x"}]},
		{"version":"0.5","date":"2026-01-01","title":"a","changes":[{"kind":"added","text":"x"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(m.Since("0.5")); got != 3 {
		t.Fatalf("0.5 is behind by %d releases, want 3", got)
	}
	if got := len(m.Since("0.8")); got != 0 {
		t.Fatalf("the newest release is offered %d updates, want 0", got)
	}
	// 0.5 and 0.5.0 are the same release, not an upgrade path.
	if got := len(m.Since("0.5.0")); got != 3 {
		t.Fatalf("0.5.0 is behind by %d releases, want 3", got)
	}
}

func TestHasBreaking(t *testing.T) {
	if HasBreaking([]Release{{Version: "1.0"}}) {
		t.Fatal("a release with no breaking flag reported as breaking")
	}
	if !HasBreaking([]Release{{Version: "1.0"}, {Version: "0.9", Breaking: true}}) {
		t.Fatal("a breaking release in the middle of the list was missed — an operator " +
			"upgrading past it would never be told")
	}
}

// The published changelog is written by a release that does not exist yet and
// read by this one. A field, a kind or an entry it does not understand used to
// fail the whole read, on every install at once, with the release that would
// have fixed the reader among the ones it could no longer see.
func TestAPublishedChangelogIsReadPastWhatThisVersionDoesNotUnderstand(t *testing.T) {
	published := `{"latest":"0.8","schema":2,"releases":[
		{"version":"0.8","date":"2026-04-01T09:30:00Z","title":"d","audience":"everyone",
			"changes":[{"kind":"performance","text":"twice as fast","since":"0.7"},{"kind":"fixed","text":"x"},{"kind":"added","text":" "}]},
		{"version":"0.7","date":"2026-03-01","title":"c","changes":"see the website"},
		{"version":"0.6","date":"March","title":"b","changes":[{"kind":"added","text":"x"}]},
		{"version":"0.5","date":"2026-01-01","title":"a","changes":[{"kind":"added","text":"x"}]},
		{"version":"0.5","date":"2026-01-02","title":"a again","changes":[{"kind":"added","text":"x"}]},
		{"version":"soon","date":"2026-05-01","title":"e","changes":[{"kind":"added","text":"x"}]}]}`
	if _, err := ParseManifest([]byte(published)); err == nil {
		t.Fatal("the strict parser accepted it, so this test is not testing the lenient one")
	}
	m, err := ParsePublishedManifest([]byte(published))
	if err != nil {
		t.Fatal(err)
	}
	if m.Latest != "0.8" || len(m.Releases) != 2 || m.Releases[1].Version != "0.5" {
		t.Fatalf("read %+v, want 0.8 and 0.5", m.Releases)
	}
	head := m.Releases[0]
	if head.Date != "2026-04-01" {
		t.Errorf("date %q: a day with a time of day is still that day", head.Date)
	}
	if len(head.Changes) != 2 || head.Changes[0].Kind != Changed || head.Changes[0].Text != "twice as fast" || head.Changes[1].Kind != Fixed {
		t.Errorf("changes %+v: a kind this version has no colour for is shown as changed, and an empty line is dropped", head.Changes)
	}
	if m.Releases[1].Title != "a" {
		t.Errorf("the second entry for 0.5 replaced the first: %q", m.Releases[1].Title)
	}
	if got := strings.Join(m.Unread, " "); got != "0.7 0.6" {
		t.Errorf("unread %q, want the two entries that were named and could not be shown", got)
	}
}

func TestAPublishedChangelogWithNothingReadableIsAnError(t *testing.T) {
	for name, published := range map[string]string{
		"not json":             `not json`,
		"no releases":          `{"releases":[]}`,
		"nothing with a title": `{"releases":[{"version":"1.0","date":"2026-01-01","changes":[{"kind":"added","text":"y"}]}]}`,
		"nothing versioned":    `{"releases":[{"version":"next","date":"2026-01-01","title":"x","changes":[{"kind":"added","text":"y"}]}]}`,
	} {
		if m, err := ParsePublishedManifest([]byte(published)); err == nil {
			t.Errorf("%s: read %+v out of it", name, m)
		}
	}
}

// A release marked breaking stays marked, whatever happened to its note: the
// install is being moved past it either way.
func TestAPublishedBreakingReleaseKeepsItsWarning(t *testing.T) {
	m, err := ParsePublishedManifest([]byte(`{"releases":[{"version":"1.0","date":"2026-01-01","title":"x",
		"breaking":true,"breakingSteps":["one","two"],"changes":[{"kind":"added","text":"y"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !HasBreaking(m.Releases) || strings.TrimSpace(m.Releases[0].BreakingNote) == "" {
		t.Fatalf("the warning was lost: %+v", m.Releases[0])
	}
}

// The file every install fetches is the one at ManifestPath on the tracked
// branch, and every release up to 0.8.5 stops reading it at FieldManifestCap
// and then fails to parse what was cut off. Nothing can be sent to those
// installs to change that, so the file has to stay under it, and this fails
// while there is still room to do something about it: the file grew about
// 4 KB a day through the 0.8 releases.
func TestTheChangelogStaysWhereInstallsInTheFieldCanReadIt(t *testing.T) {
	const warnAt = 700 << 10
	if warnAt >= FieldManifestCap {
		t.Fatal("the warning has to come before the limit")
	}
	if len(changelogJSON) > warnAt {
		t.Fatalf("%s is %d bytes. Installs on 0.8.5 and older read only the first %d and then report that "+
			"the published changelog could not be read, so they would never be offered another release.\n"+
			"Before this file reaches that size: keep the recent releases here, move the older ones to a file "+
			"beside it that only this build embeds (CHANGELOG.md stays complete), and raise this number only "+
			"once no install older than 0.8.6 is worth reaching. See \"The published changelog\" in "+
			"docs/internal/backend/databases-proxy-platform.md.",
			ManifestPath, len(changelogJSON), FieldManifestCap)
	}
}
