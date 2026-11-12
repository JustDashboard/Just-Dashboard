// Package selfupdate is how a Just Dashboard install learns that a newer one
// exists, shows what changed in it, and moves itself onto it.
//
// Everything else in this product manages something on the server. This
// manages the product, and the awkwardness of that is the whole design
// problem: the process being asked to install an update is one of the
// containers the update replaces, so it cannot watch its own replacement
// happen. The answer taken here is that the backend never runs the upgrade
// itself — it starts a sibling container that does, and reads the outcome back
// off disk once it has been restarted into the new version. install.go is that
// half; this file is the other one, which is where "what changed" comes from.
//
// The changelog is a **data file, not prose**. Markdown would have been less
// work to write and impossible to use: the dashboard has to compare versions,
// select the entries between the installed one and the newest one, and render
// them as a list of changes with a kind against each — none of which is a
// thing you can do to a paragraph without writing a parser and then trusting
// it against release notes nobody validated. changelog.json is validated by
// the test suite and read on both sides of the network: embedded here, so an
// install always knows its own history without asking anyone; and fetched from
// the repository, so it also knows the history it does not have yet. One
// shape, one thing to edit for a release.
//
// The two sides are not read equally strictly, and that is deliberate. The
// copy compiled in is this build's own and ParseManifest refuses anything it
// cannot render. The published copy is written by a release that does not
// exist yet and read by every install still in the field, so
// ParsePublishedManifest takes what it understands and steps over the rest:
// an install that cannot read the newest file can never be told about the
// release that would teach it to.
package selfupdate

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// changelogJSON is the release history compiled into this build.
//
// It is embedded rather than read from disk because the answer must not depend
// on which files happened to be copied into the image — an install that cannot
// say what version it is and what that version contains is exactly the install
// whose operator is trying to work out whether to upgrade.
//
//go:embed changelog.json
var changelogJSON []byte

// ManifestPath is where the same file lives in the repository, which is what
// the check fetches. It is a path within the tree rather than a release asset
// on purpose: writing a release's notes is a commit, so there is no second
// place to write them, and an install started from a fork or a branch reads
// whatever that fork or branch actually says. What the file cannot say is
// whether a version it names was released; that is the tag's to say (see
// check.go).
const ManifestPath = "backend/internal/selfupdate/changelog.json"

// Kind classifies one line of a release. The set is closed because the UI
// paints each one differently and a release inventing a seventh kind should
// fail the test suite rather than render as an unstyled grey row. Adding one
// is not free even with the tests changed: installs already in the field show
// it as "changed" (see readable), so their operators lose the distinction.
type Kind string

const (
	Added      Kind = "added"
	Changed    Kind = "changed"
	Fixed      Kind = "fixed"
	Removed    Kind = "removed"
	Security   Kind = "security"
	Deprecated Kind = "deprecated"
)

var kinds = map[Kind]bool{
	Added: true, Changed: true, Fixed: true,
	Removed: true, Security: true, Deprecated: true,
}

// Valid reports whether k is one of the six.
func (k Kind) Valid() bool { return kinds[k] }

// Change is one line of a release.
type Change struct {
	Kind Kind   `json:"kind"`
	Text string `json:"text"`
	// Detail is the sentence under the line, for a change whose consequence is
	// not obvious from its title. Optional, and most changes do not have one —
	// release notes that explain every entry are read as thoroughly as a
	// licence agreement.
	Detail string `json:"detail,omitempty"`
}

// Release is one version.
type Release struct {
	Version string `json:"version"`
	// Date is a plain calendar day (2006-01-02), not a timestamp. Releases are
	// cut on a day, and an operator comparing "when did I install this" with a
	// changelog does not care about the hour.
	Date string `json:"date"`
	// Title names the release in a few words — "In-app updates" — so the
	// notification in the sidebar can say what is in the version rather than
	// only its number.
	Title string `json:"title"`
	// Summary is the paragraph at the top of the entry.
	Summary string   `json:"summary,omitempty"`
	Changes []Change `json:"changes"`
	// Breaking marks a release that needs the operator to do something by
	// hand. The UI refuses to hide it behind a one-click install.
	Breaking bool `json:"breaking,omitempty"`
	// BreakingNote says what has to be done. Required when Breaking is set;
	// the alternative is a warning triangle with nothing behind it.
	BreakingNote string `json:"breakingNote,omitempty"`
}

// Day parses Date, or returns the zero time when it is missing. Callers render
// the zero time as nothing rather than as 1 January year 1.
func (r Release) Day() time.Time {
	t, err := time.Parse("2006-01-02", r.Date)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// Manifest is the whole file: every release this project has cut, newest
// first.
type Manifest struct {
	// Latest is the version at the head of Releases, repeated as its own field
	// so a client that only wants to know whether it is behind does not have
	// to reach into an array to find out.
	Latest   string    `json:"latest"`
	Releases []Release `json:"releases"`
	// Unread are the versions a published file names that this build could not
	// read the entry of, newest first. Always empty for the compiled-in copy,
	// which is refused whole instead. It exists so that an install with nothing
	// readable left to offer says a newer release is out, rather than that it
	// is up to date.
	Unread []string `json:"-"`
}

// Local is the history this build was compiled with. It never fails: the file
// is embedded and the test suite parses it, so a manifest that does not parse
// cannot be committed.
func Local() Manifest {
	m, err := ParseManifest(changelogJSON)
	if err != nil {
		// Unreachable while TestLocalManifestIsValid passes, which is the
		// point of that test. Degrading to an empty history is still better
		// than refusing to boot the dashboard over its own release notes.
		return Manifest{Releases: []Release{}}
	}
	return m
}

// ParseManifest reads this build's own changelog and checks it makes sense.
//
// It is strict because it is the gate on what gets published: the test suite
// runs it over the committed file, so a release that could not be rendered is
// a failed test rather than a notice every install shows wrongly. A file that
// came over the network is read with ParsePublishedManifest instead.
func ParseManifest(b []byte) (Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("changelog is not valid JSON: %w", err)
	}
	if len(m.Releases) == 0 {
		return Manifest{}, fmt.Errorf("changelog lists no releases")
	}
	seen := map[string]bool{}
	for i, r := range m.Releases {
		if !ValidVersion(r.Version) {
			return Manifest{}, fmt.Errorf("release %d: %q is not a version", i, r.Version)
		}
		if seen[r.Version] {
			return Manifest{}, fmt.Errorf("release %s appears twice", r.Version)
		}
		seen[r.Version] = true
		if err := checkRelease(r); err != nil {
			return Manifest{}, err
		}
	}
	sortNewestFirst(m.Releases)
	head := m.Releases[0].Version
	if m.Latest == "" {
		m.Latest = head
	} else if Compare(m.Latest, head) != 0 {
		return Manifest{}, fmt.Errorf("changelog says latest is %s but its newest release is %s", m.Latest, head)
	}
	return m, nil
}

// checkRelease is what one entry has to carry before it can be shown.
func checkRelease(r Release) error {
	if strings.TrimSpace(r.Title) == "" {
		return fmt.Errorf("release %s has no title", r.Version)
	}
	if _, err := time.Parse("2006-01-02", r.Date); err != nil {
		return fmt.Errorf("release %s: date %q is not YYYY-MM-DD", r.Version, r.Date)
	}
	if len(r.Changes) == 0 {
		return fmt.Errorf("release %s lists no changes", r.Version)
	}
	for j, c := range r.Changes {
		if !c.Kind.Valid() {
			return fmt.Errorf("release %s change %d: %q is not one of added/changed/fixed/removed/security/deprecated", r.Version, j, c.Kind)
		}
		if strings.TrimSpace(c.Text) == "" {
			return fmt.Errorf("release %s change %d has no text", r.Version, j)
		}
	}
	// A warning triangle with nothing behind it teaches the operator to
	// ignore warning triangles.
	if r.Breaking && strings.TrimSpace(r.BreakingNote) == "" {
		return fmt.Errorf("release %s is marked breaking but says nothing about what to do", r.Version)
	}
	return nil
}

// sortNewestFirst orders by version rather than demanding the file be sorted:
// the order of the array is a detail of how it was edited, and a release
// inserted in the wrong place should not change which version an install
// thinks is newest.
func sortNewestFirst(releases []Release) {
	sort.SliceStable(releases, func(i, j int) bool {
		return Compare(releases[i].Version, releases[j].Version) > 0
	})
}

// ParsePublishedManifest reads a changelog that some other release wrote: the
// file fetched from the repository, and the one in a commit about to be
// installed.
//
// The reader is always older than the file, so what it does not understand is
// stepped over rather than refused. A field it has never heard of is ignored, a
// kind of change outside its six is shown as "changed", and an entry it cannot
// make sense of is left out while the rest are kept. With ParseManifest on this
// path, one new field in one release made every install in the field report
// that the published changelog could not be read, the release that would have
// fixed it included.
//
// What is kept is still checked: every release returned has a version this
// scheme can compare, a title, a day and at least one change with text, so
// nothing here can end as a banner offering to install "". A file with no such
// release is an error.
//
// Latest is the newest release that could be read, whatever the file's own
// `latest` says. The versions that were named but not read are in Unread, so a
// caller with nothing left to offer can say that something newer is out.
func ParsePublishedManifest(b []byte) (Manifest, error) {
	var file struct {
		Latest   string            `json:"latest"`
		Releases []json.RawMessage `json:"releases"`
	}
	if err := json.Unmarshal(b, &file); err != nil {
		return Manifest{}, fmt.Errorf("changelog is not valid JSON: %w", err)
	}
	m := Manifest{Releases: []Release{}}
	seen := map[string]bool{}
	unread := map[string]bool{}
	for _, raw := range file.Releases {
		var r Release
		if err := json.Unmarshal(raw, &r); err != nil {
			// The entry's shape has changed. Its version is still worth knowing,
			// when it can be read on its own.
			var named struct {
				Version string `json:"version"`
			}
			if json.Unmarshal(raw, &named) == nil && ValidVersion(named.Version) {
				unread[Normalise(named.Version)] = true
			}
			continue
		}
		if !ValidVersion(r.Version) || seen[Normalise(r.Version)] {
			continue
		}
		readable(&r)
		if checkRelease(r) != nil {
			unread[Normalise(r.Version)] = true
			continue
		}
		seen[Normalise(r.Version)] = true
		m.Releases = append(m.Releases, r)
	}
	if len(m.Releases) == 0 {
		return Manifest{}, fmt.Errorf("changelog lists no releases this version can read")
	}
	sortNewestFirst(m.Releases)
	m.Latest = m.Releases[0].Version
	if ValidVersion(file.Latest) && !seen[Normalise(file.Latest)] {
		unread[Normalise(file.Latest)] = true
	}
	for version := range unread {
		if !seen[version] {
			m.Unread = append(m.Unread, version)
		}
	}
	sort.Slice(m.Unread, func(i, j int) bool { return Compare(m.Unread[i], m.Unread[j]) > 0 })
	return m, nil
}

// readable rewrites the parts of a published entry that a newer release may
// have written differently into the form this build renders.
func readable(r *Release) {
	// A day that grew a time of day is still that day.
	if len(r.Date) > 10 {
		if _, err := time.Parse("2006-01-02", r.Date[:10]); err == nil {
			r.Date = r.Date[:10]
		}
	}
	changes := r.Changes[:0:0]
	for _, c := range r.Changes {
		if strings.TrimSpace(c.Text) == "" {
			continue
		}
		if !c.Kind.Valid() {
			// The line is still true, and "changed" is the kind that claims
			// least about it.
			c.Kind = Changed
		}
		changes = append(changes, c)
	}
	r.Changes = changes
	// The warning is kept even with nothing behind it: an operator told to
	// read the notes first loses less than one who was not told.
	if r.Breaking && strings.TrimSpace(r.BreakingNote) == "" {
		r.BreakingNote = "This release needs something done by hand. Its notes say what; this version cannot show that part of them."
	}
}

// NewerUnread is the newest version a published changelog names, beyond
// version, whose entry this build could not read, or "".
func (m Manifest) NewerUnread(version string) string {
	for _, unread := range m.Unread {
		if Compare(unread, version) > 0 {
			return unread
		}
	}
	return ""
}

// Since returns the releases strictly newer than version, newest first.
//
// This is the list the "what you would be installing" panel shows, and it is
// deliberately every intervening release rather than only the newest one: an
// install three versions behind is upgrading past three sets of changes, and
// showing only the last would hide the two that explain what happened to it.
func (m Manifest) Since(version string) []Release {
	out := []Release{}
	for _, r := range m.Releases {
		if Compare(r.Version, version) > 0 {
			out = append(out, r)
		}
	}
	return out
}

// Find returns one release by version.
func (m Manifest) Find(version string) (Release, bool) {
	for _, r := range m.Releases {
		if Compare(r.Version, version) == 0 {
			return r, true
		}
	}
	return Release{}, false
}

// HasBreaking reports whether any release in the list needs manual work, which
// is what turns a one-click install into a "read this first".
func HasBreaking(releases []Release) bool {
	for _, r := range releases {
		if r.Breaking {
			return true
		}
	}
	return false
}
