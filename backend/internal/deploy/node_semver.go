package deploy

import (
	"strconv"
	"strings"
)

// A lockfile records the version each dependency resolved to; whether that
// version still satisfies the range package.json asks for decides whether npm
// ci and Bun's frozen install accept it. This is npm's range grammar — `^`,
// `~`, x-ranges, comparators, hyphen ranges and `||` — evaluated as data.
// Anything it does not model (dist-tags, URLs, git and workspace protocols,
// prereleases) is reported as unknown so a caller never calls a lockfile
// stale on a guess.

type nodeVersion struct{ major, minor, patch int }

func (v nodeVersion) less(other nodeVersion) bool {
	if v.major != other.major {
		return v.major < other.major
	}
	if v.minor != other.minor {
		return v.minor < other.minor
	}
	return v.patch < other.patch
}

// parseNodeVersion reads an exact release version. A prerelease is not
// modelled, so it is not a version this reader compares.
func parseNodeVersion(text string) (nodeVersion, bool) {
	text = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(text), "="), "v")
	text, _, _ = strings.Cut(text, "+")
	parts := strings.Split(text, ".")
	if len(parts) != 3 {
		return nodeVersion{}, false
	}
	numbers := [3]int{}
	for index, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 || part == "" || (len(part) > 1 && part[0] == '0') {
			return nodeVersion{}, false
		}
		numbers[index] = number
	}
	return nodeVersion{numbers[0], numbers[1], numbers[2]}, true
}

// nodePartial is a version with wildcards: 1, 1.2, 1.x, 1.2.*.
type nodePartial struct {
	parts int // how many of major, minor, patch are numbers
	v     nodeVersion
}

func parseNodePartial(text string) (nodePartial, bool) {
	text = strings.TrimPrefix(strings.TrimSpace(text), "v")
	text, _, _ = strings.Cut(text, "+")
	if text == "" || text == "*" || text == "x" || text == "X" {
		return nodePartial{}, true
	}
	if strings.Contains(text, "-") {
		return nodePartial{}, false
	}
	fields := strings.Split(text, ".")
	if len(fields) > 3 {
		return nodePartial{}, false
	}
	result := nodePartial{}
	numbers := [3]int{}
	for index, field := range fields {
		if field == "*" || field == "x" || field == "X" {
			break
		}
		number, err := strconv.Atoi(field)
		if err != nil || number < 0 {
			return nodePartial{}, false
		}
		if result.parts != index {
			return nodePartial{}, false
		}
		numbers[index] = number
		result.parts++
	}
	result.v = nodeVersion{numbers[0], numbers[1], numbers[2]}
	return result, true
}

// nodeBound is one side of an interval; an absent bound is unbounded.
type nodeBound struct {
	set       bool
	v         nodeVersion
	inclusive bool
}

type nodeInterval struct{ low, high nodeBound }

func (i nodeInterval) contains(v nodeVersion) bool {
	if i.low.set && (v.less(i.low.v) || (!i.low.inclusive && v == i.low.v)) {
		return false
	}
	if i.high.set && (i.high.v.less(v) || (!i.high.inclusive && v == i.high.v)) {
		return false
	}
	return true
}

// narrow intersects one comparator into an interval.
func (i *nodeInterval) narrow(other nodeInterval) {
	if other.low.set && (!i.low.set || i.low.v.less(other.low.v) || (i.low.v == other.low.v && !other.low.inclusive)) {
		i.low = other.low
	}
	if other.high.set && (!i.high.set || other.high.v.less(i.high.v) || (i.high.v == other.high.v && !other.high.inclusive)) {
		i.high = other.high
	}
}

func atLeast(v nodeVersion) nodeBound { return nodeBound{set: true, v: v, inclusive: true} }
func below(v nodeVersion) nodeBound   { return nodeBound{set: true, v: v} }

// nextAfter is the first version a partial no longer covers: 1 → 2.0.0,
// 1.2 → 1.3.0, 1.2.3 → 1.2.4.
func (p nodePartial) nextAfter() nodeVersion {
	switch p.parts {
	case 1:
		return nodeVersion{p.v.major + 1, 0, 0}
	case 2:
		return nodeVersion{p.v.major, p.v.minor + 1, 0}
	default:
		return nodeVersion{p.v.major, p.v.minor, p.v.patch + 1}
	}
}

func nodeComparator(text string) (nodeInterval, bool) {
	operator := ""
	for _, candidate := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
		if strings.HasPrefix(text, candidate) {
			operator, text = candidate, strings.TrimSpace(strings.TrimPrefix(text, candidate))
			break
		}
	}
	if operator == "~" {
		text = strings.TrimPrefix(text, ">")
	}
	partial, ok := parseNodePartial(text)
	if !ok {
		return nodeInterval{}, false
	}
	if partial.parts == 0 {
		if operator == "<" || operator == ">" {
			// "<*" and ">*" match nothing; not a range worth modelling.
			return nodeInterval{}, false
		}
		return nodeInterval{}, true
	}
	switch operator {
	case "", "=":
		if partial.parts == 3 {
			return nodeInterval{low: atLeast(partial.v), high: nodeBound{set: true, v: partial.v, inclusive: true}}, true
		}
		return nodeInterval{low: atLeast(partial.v), high: below(partial.nextAfter())}, true
	case ">=":
		return nodeInterval{low: atLeast(partial.v)}, true
	case ">":
		if partial.parts == 3 {
			return nodeInterval{low: nodeBound{set: true, v: partial.v}}, true
		}
		return nodeInterval{low: atLeast(partial.nextAfter())}, true
	case "<":
		return nodeInterval{high: below(partial.v)}, true
	case "<=":
		if partial.parts == 3 {
			return nodeInterval{high: nodeBound{set: true, v: partial.v, inclusive: true}}, true
		}
		return nodeInterval{high: below(partial.nextAfter())}, true
	case "~":
		if partial.parts == 1 {
			return nodeInterval{low: atLeast(partial.v), high: below(nodeVersion{partial.v.major + 1, 0, 0})}, true
		}
		return nodeInterval{low: atLeast(partial.v), high: below(nodeVersion{partial.v.major, partial.v.minor + 1, 0})}, true
	case "^":
		switch {
		case partial.v.major > 0 || partial.parts == 1:
			return nodeInterval{low: atLeast(partial.v), high: below(nodeVersion{partial.v.major + 1, 0, 0})}, true
		case partial.v.minor > 0 || partial.parts == 2:
			return nodeInterval{low: atLeast(partial.v), high: below(nodeVersion{0, partial.v.minor + 1, 0})}, true
		default:
			return nodeInterval{low: atLeast(partial.v), high: below(nodeVersion{0, 0, partial.v.patch + 1})}, true
		}
	}
	return nodeInterval{}, false
}

// nodeRangeSatisfies reports whether an exact version satisfies an npm
// range. known is false when either side is outside what this reader models.
func nodeRangeSatisfies(version, rangeText string) (satisfied, known bool) {
	v, ok := parseNodeVersion(version)
	if !ok {
		return false, false
	}
	rangeText = strings.TrimSpace(rangeText)
	if strings.HasPrefix(rangeText, "npm:") {
		// An alias, npm:name@range, is judged by its range.
		alias := strings.TrimPrefix(rangeText, "npm:")
		at := strings.LastIndex(alias, "@")
		if at <= 0 {
			return false, false
		}
		rangeText = alias[at+1:]
	}
	for _, r := range rangeText {
		if !(r >= '0' && r <= '9' || strings.ContainsRune(" .xX*^~<>=|-v", r)) {
			return false, false
		}
	}
	for _, alternative := range strings.Split(rangeText, "||") {
		interval, ok := nodeRangeInterval(strings.TrimSpace(alternative))
		if !ok {
			return false, false
		}
		if interval.contains(v) {
			return true, true
		}
	}
	return false, true
}

func nodeRangeInterval(text string) (nodeInterval, bool) {
	if low, high, found := strings.Cut(text, " - "); found {
		from, okFrom := parseNodePartial(low)
		to, okTo := parseNodePartial(high)
		if !okFrom || !okTo {
			return nodeInterval{}, false
		}
		interval := nodeInterval{}
		if from.parts > 0 {
			interval.low = atLeast(from.v)
		}
		switch to.parts {
		case 0:
		case 3:
			interval.high = nodeBound{set: true, v: to.v, inclusive: true}
		default:
			interval.high = below(to.nextAfter())
		}
		return interval, true
	}
	// "> = 1.2" and ">= 1.2" both mean ">=1.2": an operator binds to the
	// version after it.
	fields := strings.Fields(text)
	joined := []string{}
	for index := 0; index < len(fields); index++ {
		field := fields[index]
		if strings.Trim(field, "<>=^~") == "" && index+1 < len(fields) {
			field += fields[index+1]
			index++
		}
		joined = append(joined, field)
	}
	interval := nodeInterval{}
	for _, comparator := range joined {
		next, ok := nodeComparator(comparator)
		if !ok {
			return nodeInterval{}, false
		}
		interval.narrow(next)
	}
	return interval, true
}
