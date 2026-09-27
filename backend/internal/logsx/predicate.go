package logsx

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// A field predicate narrows lines by what was read out of them rather than by
// their text: f=user:postgres, f=status:>=500, f=event:!probe. A text search
// for "postgres" also finds the database name, the path and the comment that
// mention it; a predicate on the user attr finds the lines where postgres is
// the user, which is the question "only lines where user is postgres" asked.
//
// The grammar is small on purpose, because the browser evaluates the same
// predicates over a live tail and the two must agree to the byte. The shared
// vectors in testdata/predicates.json are what holds them to it.

const (
	// maxPredicates and maxPredicateValue bound what a URL can make every
	// line pay for. Thirty-two is more chips than a filter bar can show.
	maxPredicates     = 32
	maxPredicateValue = 256
	maxKeyLength      = 64
)

// ValidKey says whether a string can name a field: letters, digits and the
// punctuation JSON loggers put in keys (log.level, @timestamp, x-request-id).
// A colon is not among them, which is what makes the first colon of
// "client:2001:db8::1" the split point and the rest an IPv6 address.
func ValidKey(key string) bool {
	if key == "" || len(key) > maxKeyLength {
		return false
	}
	for i := 0; i < len(key); i++ {
		switch c := key[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '_', c == '.', c == '@', c == '-':
		default:
			return false
		}
	}
	return true
}

type predicateOp uint8

const (
	opEqual predicateOp = iota
	opContains
	opNotEqual
	opNotContains
	opPresent
	opAbsent
	opGreater
	opGreaterEqual
	opLess
	opLessEqual
)

type predicate struct {
	op     predicateOp
	value  string // as written, for the equality forms
	folded string // lowercased, for the substring forms
	number float64
}

// fieldTest is every predicate on one key. Exact and substring forms are
// alternatives — "user is postgres or root" is two chips on one key, and
// requiring both would match nothing. Negations and comparisons are
// constraints, AND'ed with that choice: "status >= 500 and not 503" is the
// range an operator means, where OR'ing it would have matched every line.
type fieldTest struct {
	key string
	any []predicate
	all []predicate
}

// parsePredicate reads one f= value. The first character decides the form,
// and a literal that happens to start with an operator is written with the
// "=" escape, so "~admin" as a path is f=path:=~admin rather than a substring
// search for "admin".
func parsePredicate(raw string) (string, predicate, error) {
	key, value, found := strings.Cut(raw, ":")
	if !found {
		return "", predicate{}, fmt.Errorf("field filter %q needs a key and a value, as key:value", raw)
	}
	if !ValidKey(key) {
		return "", predicate{}, fmt.Errorf("field filter %q: %q is not a field name", raw, key)
	}
	if key == "level" {
		return "", predicate{}, fmt.Errorf("field filter %q: filter levels with the level chips, not a field", raw)
	}
	if len(value) > maxPredicateValue {
		return "", predicate{}, fmt.Errorf("field filter on %q: the value is longer than %d bytes", key, maxPredicateValue)
	}
	literal := func(op predicateOp, text string) (string, predicate, error) {
		if text == "" {
			return "", predicate{}, fmt.Errorf("field filter %q has nothing to compare with", raw)
		}
		return key, predicate{op: op, value: text, folded: strings.ToLower(text)}, nil
	}
	compare := func(op predicateOp, text string) (string, predicate, error) {
		n, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return "", predicate{}, fmt.Errorf("field filter %q compares with %q, which is not a number (write =%s to match it literally)", raw, text, value)
		}
		return key, predicate{op: op, number: n}, nil
	}
	switch {
	case value == "*":
		return key, predicate{op: opPresent}, nil
	case value == "!*":
		return key, predicate{op: opAbsent}, nil
	case strings.HasPrefix(value, "!~"):
		return literal(opNotContains, value[2:])
	case strings.HasPrefix(value, "~"):
		return literal(opContains, value[1:])
	case strings.HasPrefix(value, ">="):
		return compare(opGreaterEqual, value[2:])
	case strings.HasPrefix(value, ">"):
		return compare(opGreater, value[1:])
	case strings.HasPrefix(value, "<="):
		return compare(opLessEqual, value[2:])
	case strings.HasPrefix(value, "<"):
		return compare(opLess, value[1:])
	case strings.HasPrefix(value, "!"):
		return literal(opNotEqual, value[1:])
	case strings.HasPrefix(value, "="):
		return literal(opEqual, value[1:])
	case strings.HasPrefix(value, "*"):
		return "", predicate{}, fmt.Errorf("field filter %q: write =%s to match a value starting with *", raw, value)
	}
	return literal(opEqual, value)
}

// compileFields groups the predicates by key, in the order the keys first
// appear, so evaluation reads each value once however many chips name it.
func compileFields(raw []string) ([]fieldTest, error) {
	if len(raw) > maxPredicates {
		return nil, fmt.Errorf("at most %d field filters, got %d", maxPredicates, len(raw))
	}
	var tests []fieldTest
	for _, r := range raw {
		key, p, err := parsePredicate(r)
		if err != nil {
			return nil, err
		}
		i := 0
		for i < len(tests) && tests[i].key != key {
			i++
		}
		if i == len(tests) {
			tests = append(tests, fieldTest{key: key})
		}
		if p.op == opEqual || p.op == opContains {
			tests[i].any = append(tests[i].any, p)
		} else {
			tests[i].all = append(tests[i].all, p)
		}
	}
	return tests, nil
}

func (t *fieldTest) match(l *Line) bool {
	var value string
	var ok bool
	if t.key == "pattern" {
		// The virtual key a facet offers is filterable too, so pressing a
		// pattern in Insights narrows to it. It is computed here only when a
		// chip asks for it, and never stored on the line.
		value = Pattern(l)
		ok = value != ""
	} else {
		value, ok = l.Value(t.key)
	}
	for i := range t.all {
		if !t.all[i].test(value, ok) {
			return false
		}
	}
	if len(t.any) == 0 {
		return true
	}
	for i := range t.any {
		if t.any[i].test(value, ok) {
			return true
		}
	}
	return false
}

// test answers one predicate. A missing value satisfies only the negative
// forms: "hide lines where user is postgres" must keep the lines that have no
// user at all, and "status >= 500" must not keep a line with no status.
func (p *predicate) test(value string, ok bool) bool {
	switch p.op {
	case opPresent:
		return ok
	case opAbsent:
		return !ok
	case opEqual:
		return ok && strings.EqualFold(value, p.value)
	case opNotEqual:
		return !ok || !strings.EqualFold(value, p.value)
	case opContains:
		return ok && containsFoldAny(value, p.folded)
	case opNotContains:
		return !ok || !containsFoldAny(value, p.folded)
	}
	if !ok {
		return false
	}
	n, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return false
	}
	switch p.op {
	case opGreater:
		return n > p.number
	case opGreaterEqual:
		return n >= p.number
	case opLess:
		return n < p.number
	case opLessEqual:
		return n <= p.number
	}
	return false
}

// containsFoldAny is a case-insensitive substring test that folds beyond ASCII
// when either side needs it. The allocation-free ASCII path is the common
// case; a user name or a path with an accent in it is not rare enough to get
// the wrong answer, and the browser folds it with toLowerCase.
func containsFoldAny(s, lowerNeedle string) bool {
	if isASCII(s) && isASCII(lowerNeedle) {
		return indexFold(s, lowerNeedle, 0) >= 0
	}
	return strings.Contains(strings.ToLower(s), lowerNeedle)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
