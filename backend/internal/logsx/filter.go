package logsx

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"
)

// Filter is the single description of "which lines does the operator want".
//
// It is shared by the live tail, the history search and the export, because
// the three used to answer the same question differently: the tail did a
// case-insensitive substring test and nothing else, search had regex and time
// bounds the tail could not express, and the export had neither. An operator
// who narrowed a live stream to one request id and then pressed Export got the
// whole file back, which is the kind of divergence nobody reports as a bug —
// they just stop trusting the page.
type Filter struct {
	Query      string   `json:"query,omitempty"`
	Exclude    string   `json:"exclude,omitempty"`
	Regex      bool     `json:"regex"`
	IgnoreCase bool     `json:"ignoreCase"`
	Levels     []string `json:"levels,omitempty"`
	// Lens is the lens every line is read through before the filter looks at
	// it, resolved by the caller: the one the operator forced, or the one the
	// source was detected as. Empty reads no lens, which is exactly how every
	// line was read before lenses existed; "none" is the operator saying so.
	Lens string `json:"lens,omitempty"`
	// Fields are the raw f= predicates on what the lens and the structured
	// parse read out of a line (see predicate.go).
	Fields []string `json:"fields,omitempty"`

	include func(string) bool
	reject  func(string) bool
	levels  map[string]bool
	fields  []fieldTest
}

// LevelUnknown is the pseudo-level for a line the parser could not classify,
// which on a typical host is most of them. Without a chip of its own, turning
// on any level filter silently hid every line that did not happen to contain
// one of a dozen English words — including the continuation lines of the very
// stack trace being hunted.
const LevelUnknown = "unknown"

// Levels is the closed set the UI offers, worst first.
var Levels = []string{"critical", "error", "warn", "info", "debug", LevelUnknown}

// NewFilter compiles the matchers once. A bad regular expression, an unknown
// lens or a malformed field predicate is reported here rather than per line,
// so the socket refuses to open with a message instead of streaming nothing
// and looking broken.
func NewFilter(f Filter) (*Filter, error) {
	out := &Filter{
		Query:      f.Query,
		Exclude:    f.Exclude,
		Regex:      f.Regex,
		IgnoreCase: f.IgnoreCase,
		Lens:       f.Lens,
	}
	if _, err := LensByID(f.Lens); err != nil {
		return nil, err
	}
	build := func(pattern, what string) (func(string) bool, error) {
		if pattern == "" {
			return nil, nil
		}
		if f.Regex {
			expr := pattern
			if f.IgnoreCase {
				expr = "(?i)" + expr
			}
			re, err := regexp.Compile(expr)
			if err != nil {
				return nil, fmt.Errorf("invalid %s expression: %w", what, err)
			}
			return re.MatchString, nil
		}
		if f.IgnoreCase {
			needle := strings.ToLower(pattern)
			return func(s string) bool { return containsFold(s, needle) }, nil
		}
		return func(s string) bool { return strings.Contains(s, pattern) }, nil
	}
	var err error
	if out.include, err = build(f.Query, "search"); err != nil {
		return nil, err
	}
	if out.reject, err = build(f.Exclude, "exclude"); err != nil {
		return nil, err
	}
	for _, l := range f.Levels {
		l = strings.ToLower(strings.TrimSpace(l))
		if l == "" {
			continue
		}
		if l != LevelUnknown {
			if l = Normalise(l); l == "" {
				continue
			}
		}
		if out.levels == nil {
			out.levels = map[string]bool{}
		}
		out.levels[l] = true
		out.Levels = append(out.Levels, l)
	}
	if out.fields, err = compileFields(f.Fields); err != nil {
		return nil, err
	}
	if len(f.Fields) > 0 {
		out.Fields = append([]string(nil), f.Fields...)
	}
	return out, nil
}

// Empty reports whether the filter would keep every line, which lets a caller
// skip the per-line work entirely on the common case of an unfiltered tail. A
// lens alone does not make a filter: it names lines, it does not drop them.
func (f *Filter) Empty() bool {
	return f == nil || (f.include == nil && f.reject == nil && len(f.levels) == 0 && len(f.fields) == 0)
}

// MatchText tests only the text, for the callers that have not parsed a level
// yet and want to skip the parse when the text alone rules the line out.
func (f *Filter) MatchText(text string) bool {
	if f == nil {
		return true
	}
	if f.include != nil && !f.include(text) {
		return false
	}
	if f.reject != nil && f.reject(text) {
		return false
	}
	return true
}

// MatchLevel tests a parsed level against the chips. An empty level answers to
// LevelUnknown rather than to nothing, so the "unknown" chip is reachable.
func (f *Filter) MatchLevel(level string) bool {
	if f == nil || len(f.levels) == 0 {
		return true
	}
	if level == "" {
		level = LevelUnknown
	}
	return f.levels[level]
}

// MatchFields tests the field predicates. Different keys are AND'ed; within
// a key the forms combine as fieldTest describes.
func (f *Filter) MatchFields(l *Line) bool {
	if f == nil {
		return true
	}
	for i := range f.fields {
		if !f.fields[i].match(l) {
			return false
		}
	}
	return true
}

// MatchParsed is everything but the text, for the callers that tested the raw
// text before parsing and should not pay for it twice.
func (f *Filter) MatchParsed(l *Line) bool {
	return f.MatchLevel(l.Level) && f.MatchFields(l)
}

func (f *Filter) Match(l Line) bool {
	return f.match(&l)
}

func (f *Filter) match(l *Line) bool {
	return f.MatchText(l.Text) && f.MatchParsed(l)
}

// containsFold is a case-insensitive substring test that allocates nothing.
//
// The obvious spelling — strings.Contains(strings.ToLower(s), needle) — builds
// a copy of every line it is handed, which on a search over a rotated set is a
// million allocations to answer a question about a dozen bytes. The needle is
// already lowercase; only ASCII is folded, which is what the previous version
// effectively did too, since a log level and a request id are ASCII.
func containsFold(s, lowerNeedle string) bool {
	return indexFold(s, lowerNeedle, 0) >= 0
}

// indexFold is the same search returning where it matched, in s's own byte
// offsets. Searching a lowercased *copy* would answer in the copy's offsets,
// and folding can change a string's length — so the highlight ranges taken
// from it were subtly wrong on exactly the lines where it mattered.
func indexFold(s, lowerNeedle string, from int) int {
	n := len(lowerNeedle)
	if n == 0 {
		return from
	}
	first := lowerNeedle[0]
	for i := from; i <= len(s)-n; i++ {
		if lowerASCII(s[i]) != first {
			continue
		}
		k := 1
		for ; k < n; k++ {
			if lowerASCII(s[i+k]) != lowerNeedle[k] {
				break
			}
		}
		if k == n {
			return i
		}
	}
	return -1
}

// utf16Ranges converts byte offsets into the units JavaScript indexes strings
// by. Go counts bytes and the browser counts UTF-16 code units, so a line with
// one accented character before the match had every highlight shifted left —
// which paints the wrong run of text and, for a multi-byte character straddling
// the boundary, renders a replacement glyph.
func utf16Ranges(text string, ranges [][2]int) [][2]int {
	ascii := true
	for i := 0; i < len(text); i++ {
		if text[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii || len(ranges) == 0 {
		return ranges
	}
	out := make([][2]int, 0, len(ranges))
	next := 0 // index into ranges of the next endpoint to convert
	side := 0 // 0 = start, 1 = end
	var current [2]int
	units := 0
	for i, r := range text {
		for next < len(ranges) && ranges[next][side] == i {
			current[side] = units
			if side == 1 {
				out = append(out, current)
				next++
				side = 0
			} else {
				side = 1
			}
		}
		if next >= len(ranges) {
			break
		}
		units += utf16.RuneLen(r)
	}
	// A range ending at the very end of the string has no rune to trigger on.
	for next < len(ranges) {
		current[side] = units
		if side == 1 {
			out = append(out, current)
			next++
			side = 0
		} else {
			side = 1
		}
	}
	return out
}

// Highlights returns the ranges of the search term inside a line — in UTF-16
// units, which is what the browser will slice by — so it can mark the match
// without reimplementing the matcher, which it cannot do faithfully anyway for
// a Go regular expression.
func (f *Filter) Highlights(text string) [][2]int {
	if f == nil || f.Query == "" {
		return nil
	}
	if f.Regex {
		expr := f.Query
		if f.IgnoreCase {
			expr = "(?i)" + expr
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil
		}
		found := re.FindAllStringIndex(text, 32)
		out := make([][2]int, 0, len(found))
		for _, m := range found {
			if m[1] > m[0] {
				out = append(out, [2]int{m[0], m[1]})
			}
		}
		return utf16Ranges(text, out)
	}
	needle := f.Query
	var find func(int) int
	if f.IgnoreCase {
		lower := strings.ToLower(f.Query)
		find = func(from int) int { return indexFold(text, lower, from) }
	} else {
		find = func(from int) int {
			i := strings.Index(text[from:], needle)
			if i < 0 {
				return -1
			}
			return from + i
		}
	}
	out := [][2]int{}
	for from := 0; from < len(text) && len(out) < 32; {
		i := find(from)
		if i < 0 {
			break
		}
		out = append(out, [2]int{i, i + len(needle)})
		from = i + len(needle)
	}
	return utf16Ranges(text, out)
}
