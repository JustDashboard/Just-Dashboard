package deploy

import (
	"regexp"
	"strconv"
	"strings"
)

// Composer's version constraints, read the way Composer reads them, so the
// recipe can ask of a checkout what `composer install` would: does the
// locked version of a package still satisfy composer.json, and which PHP
// release satisfies the application and everything it locked. A constraint
// or version this reader does not understand — a branch alias, dev-main, an
// inline alias — is never a verdict: the caller skips it.

// composerVersion is a normalised release: four numeric parts, the
// stability they carry (dev < alpha < beta < RC < stable < patch) and that
// stability's own number, as Composer's VersionParser normalises 1.2 to
// 1.2.0.0 and v2.0.0-beta.3 to 2.0.0.0-beta3.
type composerVersion struct {
	parts     [4]int
	stability int
	suffix    int
}

const (
	composerDev = iota
	composerAlpha
	composerBeta
	composerRC
	composerStable
	composerPatch
)

var (
	composerVersionRE   = regexp.MustCompile(`^v?(\d{1,9})(?:\.(\d{1,9}))?(?:\.(\d{1,9}))?(?:\.(\d{1,9}))?(?:[.-]?(stable|beta|b|rc|alpha|a|patch|pl|p|dev)\.?(\d{0,9}))?$`)
	composerWildcardRE  = regexp.MustCompile(`^v?(\d{1,9})(?:\.(\d{1,9}))?(?:\.(\d{1,9}))?\.[*xX]$`)
	composerOperatorRE  = regexp.MustCompile(`^(<>|!=|>=|<=|==|=|>|<|\^|~)?\s*(.+)$`)
	composerStabilityAt = regexp.MustCompile(`@(?:dev|alpha|beta|rc|stable|RC)$`)
	composerAlternateRE = regexp.MustCompile(`\s*\|\|?\s*`)
)

// parseComposerVersion normalises one version string, or reports that it is
// not a release number (dev-main, 1.x-dev, a hash).
func parseComposerVersion(text string) (composerVersion, bool) {
	text = strings.ToLower(strings.TrimSpace(text))
	if build, _, found := strings.Cut(text, "+"); found {
		text = build
	}
	match := composerVersionRE.FindStringSubmatch(text)
	if match == nil {
		return composerVersion{}, false
	}
	var version composerVersion
	for index := 0; index < 4; index++ {
		if match[index+1] != "" {
			version.parts[index], _ = strconv.Atoi(match[index+1])
		}
	}
	version.stability = composerStable
	switch match[5] {
	case "dev":
		version.stability = composerDev
	case "alpha", "a":
		version.stability = composerAlpha
	case "beta", "b":
		version.stability = composerBeta
	case "rc":
		version.stability = composerRC
	case "patch", "pl", "p":
		version.stability = composerPatch
	}
	if match[6] != "" {
		version.suffix, _ = strconv.Atoi(match[6])
	}
	return version, true
}

func (v composerVersion) compare(other composerVersion) int {
	for index := range v.parts {
		if v.parts[index] != other.parts[index] {
			if v.parts[index] < other.parts[index] {
				return -1
			}
			return 1
		}
	}
	switch {
	case v.stability != other.stability:
		if v.stability < other.stability {
			return -1
		}
		return 1
	case v.suffix != other.suffix:
		if v.suffix < other.suffix {
			return -1
		}
		return 1
	}
	return 0
}

// composerBound is one side of a range; lower bounds are inclusive or not,
// and an upper bound at x.y.0.0-dev excludes every pre-release of x.y, the
// way Composer writes ^1.2 as >=1.2.0.0-dev <2.0.0.0-dev.
type composerBound struct {
	version   composerVersion
	inclusive bool
	set       bool
}

type composerRange struct {
	lower, upper composerBound
	not          *composerVersion
}

func (r composerRange) allows(version composerVersion) bool {
	if r.not != nil {
		return version.compare(*r.not) != 0
	}
	if r.lower.set {
		if c := version.compare(r.lower.version); c < 0 || (c == 0 && !r.lower.inclusive) {
			return false
		}
	}
	if r.upper.set {
		if c := version.compare(r.upper.version); c > 0 || (c == 0 && !r.upper.inclusive) {
			return false
		}
	}
	return true
}

func composerDevOf(parts [4]int) composerVersion {
	return composerVersion{parts: parts, stability: composerDev}
}

// composerAtomRange reads one constraint term: an operator and a version, a
// wildcard, a caret or a tilde.
func composerAtomRange(atom string) (composerRange, bool) {
	atom = composerStabilityAt.ReplaceAllString(strings.TrimSpace(atom), "")
	if atom == "*" || atom == "x" || atom == "X" {
		return composerRange{}, true
	}
	if match := composerWildcardRE.FindStringSubmatch(atom); match != nil {
		// 1.2.* is >=1.2.0.0-dev <1.3.0.0-dev.
		var floor [4]int
		depth := 0
		for index := 0; index < 3 && match[index+1] != ""; index++ {
			floor[index], _ = strconv.Atoi(match[index+1])
			depth = index
		}
		ceiling := floor
		ceiling[depth]++
		return composerRange{
			lower: composerBound{version: composerDevOf(floor), inclusive: true, set: true},
			upper: composerBound{version: composerDevOf(ceiling), set: true},
		}, true
	}
	operator := composerOperatorRE.FindStringSubmatch(atom)
	if operator == nil {
		return composerRange{}, false
	}
	text := strings.TrimSpace(operator[2])
	version, ok := parseComposerVersion(text)
	if !ok {
		return composerRange{}, false
	}
	// How many parts the constraint wrote decides what ^ and ~ round to.
	written := strings.Count(strings.SplitN(strings.TrimPrefix(strings.ToLower(text), "v"), "-", 2)[0], ".") + 1
	switch operator[1] {
	case "^":
		// The first non-zero part may not change: ^1.2.3 <2.0, ^0.3 <0.4,
		// ^0.0.3 <0.0.4.
		var ceiling [4]int
		switch {
		case version.parts[0] != 0 || written == 1:
			ceiling[0] = version.parts[0] + 1
		case version.parts[1] != 0 || written == 2:
			ceiling[1] = version.parts[1] + 1
		default:
			ceiling = [4]int{0, 0, version.parts[2] + 1, 0}
		}
		return composerRange{
			lower: composerBound{version: withDevFloor(version), inclusive: true, set: true},
			upper: composerBound{version: composerDevOf(ceiling), set: true},
		}, true
	case "~":
		// ~1.2 is >=1.2 <2.0; ~1.2.3 is >=1.2.3 <1.3.
		var ceiling [4]int
		if written <= 2 {
			ceiling[0] = version.parts[0] + 1
		} else {
			ceiling[0], ceiling[1] = version.parts[0], version.parts[1]+1
			if written == 4 {
				ceiling[1], ceiling[2] = version.parts[1], version.parts[2]+1
			}
		}
		return composerRange{
			lower: composerBound{version: withDevFloor(version), inclusive: true, set: true},
			upper: composerBound{version: composerDevOf(ceiling), set: true},
		}, true
	case ">=":
		return composerRange{lower: composerBound{version: withDevFloor(version), inclusive: true, set: true}}, true
	case ">":
		return composerRange{lower: composerBound{version: version, set: true}}, true
	case "<=":
		return composerRange{upper: composerBound{version: version, inclusive: true, set: true}}, true
	case "<":
		return composerRange{upper: composerBound{version: withDevFloor(version), set: true}}, true
	case "!=", "<>":
		return composerRange{not: &version}, true
	}
	return composerRange{
		lower: composerBound{version: version, inclusive: true, set: true},
		upper: composerBound{version: version, inclusive: true, set: true},
	}, true
}

// withDevFloor writes a stable bound the way Composer does, at its -dev
// point, so >=2.0 admits 2.0.0-beta1 and <2.0 refuses it.
func withDevFloor(version composerVersion) composerVersion {
	if version.stability == composerStable && version.suffix == 0 {
		version.stability = composerDev
	}
	return version
}

// composerConstraintAllows says whether a version satisfies a constraint.
// ok is false when either could not be read, which is never a verdict.
func composerConstraintAllows(constraint, version string) (allowed, ok bool) {
	parsed, readable := parseComposerVersion(version)
	if !readable {
		return false, false
	}
	return composerConstraintAllowsVersion(constraint, parsed)
}

func composerConstraintAllowsVersion(constraint string, version composerVersion) (allowed, ok bool) {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" || strings.Contains(constraint, " as ") || strings.Contains(strings.ToLower(constraint), "dev-") {
		return false, false
	}
	for _, alternative := range composerAlternateRE.Split(constraint, -1) {
		ranges, readable := composerConjunction(alternative)
		if !readable {
			return false, false
		}
		all := true
		for _, term := range ranges {
			all = all && term.allows(version)
		}
		if all {
			return true, true
		}
	}
	return false, true
}

// composerConjunction splits an AND of terms: commas or spaces between
// terms, an operator written apart from its version (">= 8.2") joined back,
// and a hyphen range ("1.0 - 2.0").
func composerConjunction(text string) ([]composerRange, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, false
	}
	if low, high, found := strings.Cut(text, " - "); found {
		lower, okLow := parseComposerVersion(low)
		upper, okHigh := parseComposerVersion(high)
		if !okLow || !okHigh {
			return nil, false
		}
		// 1.0 - 2.0 admits all of 2.0.x; 1.0 - 2.0.1 stops at 2.0.1.
		upperBound := composerBound{version: upper, inclusive: true, set: true}
		if parts := strings.Count(strings.TrimPrefix(strings.TrimSpace(high), "v"), "."); parts < 2 {
			ceiling := upper.parts
			ceiling[parts]++
			upperBound = composerBound{version: composerDevOf(ceiling), set: true}
		}
		return []composerRange{{lower: composerBound{version: withDevFloor(lower), inclusive: true, set: true}, upper: upperBound}}, true
	}
	fields := strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	terms := []string{}
	for index := 0; index < len(fields); index++ {
		field := fields[index]
		if composerOperatorOnly(field) && index+1 < len(fields) {
			field += fields[index+1]
			index++
		}
		terms = append(terms, field)
	}
	ranges := make([]composerRange, 0, len(terms))
	for _, term := range terms {
		parsed, ok := composerAtomRange(term)
		if !ok {
			return nil, false
		}
		ranges = append(ranges, parsed)
	}
	return ranges, len(ranges) > 0
}

func composerOperatorOnly(field string) bool {
	switch field {
	case ">=", "<=", ">", "<", "=", "==", "!=", "<>", "^", "~":
		return true
	}
	return false
}

// composerPlatformPackage names the requirements Composer checks against the
// running PHP rather than resolves from a repository.
func composerPlatformPackage(name string) bool {
	name = strings.ToLower(name)
	switch {
	case name == "php" || strings.HasPrefix(name, "php-") || name == "hhvm" || name == "composer" ||
		name == "composer-plugin-api" || name == "composer-runtime-api":
		return true
	case strings.HasPrefix(name, "ext-") || strings.HasPrefix(name, "lib-"):
		return true
	}
	return false
}

// phpReleaseVersion is how a catalogue release is judged against a
// constraint: as its newest patch, which is what the image carries.
func phpReleaseVersion(release string) composerVersion {
	version, _ := parseComposerVersion(release + ".99")
	return version
}
