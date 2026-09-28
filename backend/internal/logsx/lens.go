package logsx

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// A Lens is what the dashboard knows about one kind of log: how Postgres
// spells a failed login, what nginx means by "*42 upstream timed out", which
// sshd line is a scanner. It reads each line after ParseLine and names what
// the line records (Event) and the values in it (Attrs), so a service's page
// can offer "auth failures by client" instead of a grep box — and so the live
// tail, the history search, the facets and the export all agree, because they
// all read the line through the same lens before any filter looks at it.
type Lens struct {
	ID string
	// Events and Attrs are the vocabulary this lens can produce. They are not
	// consulted at runtime; the golden test writes them out so the frontend's
	// registry is checked against what the parsers actually emit, and a new
	// event cannot ship without a label.
	Events []string
	Attrs  []string
	// New starts a reader for one ordered stream — one file (each archive
	// separately), one container, one journalctl run. A reader may keep state
	// between lines: the pid of the Postgres statement a DETAIL belongs to,
	// the apt transaction an Install line is part of.
	New func() Reader
}

// Reader reads one line at a time, in order, after ParseLine and before any
// filter test. It may set Event, Attrs, Cont, Lens, Level and Timestamp; it
// never changes Text, Source, Stream, No, File or Match. Readers are
// single-pass and forward-only: a value can be carried onto later lines, but
// an emitted line is never edited, so when the defining line of a record comes
// last, the event goes on that line.
type Reader interface {
	Read(l *Line)
}

// ReaderFunc adapts a stateless function to Reader.
type ReaderFunc func(*Line)

func (f ReaderFunc) Read(l *Line) { f(l) }

var lenses = map[string]*Lens{}

// register is called from each lens file's init. A duplicate id is a
// programming error, and panicking at start-up is how it gets noticed.
func register(l *Lens) {
	if l == nil || l.ID == "" || l.New == nil {
		panic("logsx: incomplete lens registration")
	}
	if _, dup := lenses[l.ID]; dup {
		panic("logsx: lens registered twice: " + l.ID)
	}
	lenses[l.ID] = l
}

// LensNone is the id that asks for no lens at all, as opposed to the empty
// string, which asks for the one the source would be detected as.
const LensNone = "none"

// LensByID answers the lens for an id; "" and "none" answer nil with no error.
func LensByID(id string) (*Lens, error) {
	if id == "" || id == LensNone {
		return nil, nil
	}
	l, ok := lenses[id]
	if !ok {
		return nil, fmt.Errorf("unknown lens %q", id)
	}
	return l, nil
}

// LensIDs lists every registered lens, sorted, for validation messages and the
// golden test.
func LensIDs() []string {
	ids := make([]string, 0, len(lenses))
	for id := range lenses {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// SetAttr records a value the lens read out of the line. An empty value is not
// recorded: "missing" and "empty" are the same thing to a facet, and an empty
// key would count as present to the `*` predicate.
func (l *Line) SetAttr(key, value string) {
	if value == "" {
		return
	}
	if l.Attrs == nil {
		l.Attrs = make(map[string]string, 4)
	}
	// A value kept by a facet tally outlives the line; cloning here means the
	// tally holds a few bytes rather than pinning a megabyte-long line.
	l.Attrs[key] = strings.Clone(value)
}

// SetAttrNumber records a number in the one spelling every reader of it parses.
func (l *Line) SetAttrNumber(key string, value float64) {
	l.SetAttr(key, strconv.FormatFloat(value, 'f', -1, 64))
}

// Value answers a predicate or facet key: the event, the stream and the source
// by name, then the lens's attrs, then a structured line's own fields.
func (l *Line) Value(key string) (string, bool) {
	switch key {
	case "event":
		return l.Event, l.Event != ""
	case "stream":
		return l.Stream, l.Stream != ""
	case "source":
		return l.Source, l.Source != ""
	case "level":
		if l.Level == "" {
			return LevelUnknown, true
		}
		return l.Level, true
	}
	if v, ok := l.Attrs[key]; ok && v != "" {
		return v, true
	}
	if v, ok := l.Fields[key]; ok && v != "" {
		return v, true
	}
	return "", false
}

// HasOwnLevel says whether the level came from the line itself — a JSON or
// logfmt level key, or a level word in its text — rather than being empty or
// assigned from the journal's priority. A lens that infers a level from a
// status code only does so for a line that did not state one.
func (l *Line) HasOwnLevel() bool {
	return l.levelFrom == levelFromStructured || l.levelFrom == levelFromWord
}

// Where a line's level came from, which decides what may overrule it: a lens
// that read the format's own severity token beats everything; a structured
// level key beats the journal priority; the free-text word scan is the weakest
// and is never used for journal lines, where "error-reporting" in prose would
// otherwise overrule a priority the program chose deliberately.
const (
	levelFromNone uint8 = iota
	levelFromWord
	levelFromPriority
	levelFromStructured
	levelFromLens
)

// SetLevel is how a lens states the level the format itself carries (a
// Postgres "ERROR:", a Redis "#", Mongo's "s":"E") or one a status or crash
// rule implies. It wins over every other source.
func (l *Line) SetLevel(level string) {
	l.Level = level
	l.levelFrom = levelFromLens
}

var (
	fpLiteral  = regexp.MustCompile(`'(?:[^']|'')*'|"(?:[^"\\]|\\.)*"|\b0x[0-9a-fA-F]+\b|\b\d+(?:\.\d+)?\b|\$\d+`)
	fpInList   = regexp.MustCompile(`\(\s*\?(?:\s*,\s*\?)*\s*\)`)
	fpValues   = regexp.MustCompile(`values\s*(?:\(\?\)\s*,?\s*)+`)
	fpComment  = regexp.MustCompile(`--[^\n]*|/\*.*?\*/`)
	fpSpace    = regexp.MustCompile(`\s+`)
	fpIdentNum = regexp.MustCompile(`([a-z_])\d+\b`)
)

// NormaliseStatement reduces a statement to its shape: literals become ?, an
// IN list or a VALUES run collapses, comments and case and whitespace go.
// These are pt-query-digest's rules, and they are what makes "the same query
// with a different id" one row instead of ten thousand.
func NormaliseStatement(query string) string {
	s := fpComment.ReplaceAllString(query, " ")
	s = strings.ToLower(s)
	s = fpLiteral.ReplaceAllString(s, "?")
	s = fpInList.ReplaceAllString(s, "(?)")
	s = fpValues.ReplaceAllString(s, "values (?) ")
	s = fpIdentNum.ReplaceAllString(s, "${1}?")
	s = fpSpace.ReplaceAllString(s, " ")
	return strings.TrimSpace(strings.TrimRight(strings.TrimSpace(s), ";"))
}

// Fingerprint names a statement's shape in twelve hex digits: FNV-1a over the
// normalised text. Short enough for a URL predicate, and shared by every lens
// and by the database query log so a slow statement found in the server log
// and the same statement in SLOWLOG are one group.
func Fingerprint(query string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(NormaliseStatement(query)))
	return fmt.Sprintf("%016x", h.Sum64())[:12]
}
