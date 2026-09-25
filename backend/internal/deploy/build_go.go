package deploy

import (
	"fmt"
	"go/version"
	"regexp"
	"strings"
)

// goRecipeFamilies are the Go releases the recipe builds with, oldest first,
// and the one list every other statement of them is made from: the version
// pattern plans are validated against, the default, the refusal text, and
// the Build settings field (deployment-defaults.ts, kept equal by a test).
// Go supports its two newest releases, so the family before them is still
// buildable when a plan or .go-version pins it, with a warning, and never
// chosen from go.mod alone. A release is added when golang:<family>-alpine
// is published; TestLiveGoRecipeCatalogueResolves resolves every entry.
var goRecipeFamilies = []struct {
	family string
	eol    bool
}{
	{"1.25", true},
	{"1.26", false},
	{"1.27", false},
}

// defaultGoRecipeVersion is the newest maintained family: what a module
// with no go line, or one older than the maintained releases, builds with.
var defaultGoRecipeVersion = goRecipeFamilies[len(goRecipeFamilies)-1].family

var goRecipeVersionRE = regexp.MustCompile(`^1\.(` + strings.Join(goRecipeMinors(), "|") + `)(\.[0-9]{1,3})?$`)
var stableGoVersionRE = regexp.MustCompile(`^1\.[0-9]{1,3}(\.[0-9]{1,3})?$`)

func goRecipeMinors() []string {
	minors := make([]string, 0, len(goRecipeFamilies))
	for _, entry := range goRecipeFamilies {
		minors = append(minors, strings.TrimPrefix(entry.family, "1."))
	}
	return minors
}

// goRecipeVersionList names the families for a sentence: "1.25, 1.26 or 1.27".
func goRecipeVersionList() string {
	families := make([]string, 0, len(goRecipeFamilies))
	for _, entry := range goRecipeFamilies {
		families = append(families, entry.family)
	}
	return strings.Join(families[:len(families)-1], ", ") + " or " + families[len(families)-1]
}

// goFamilyEOL says a family is in the catalogue only for pins: upstream no
// longer ships its security fixes.
func goFamilyEOL(family string) bool {
	for _, entry := range goRecipeFamilies {
		if entry.family == family {
			return entry.eol
		}
	}
	return false
}

// goFamilyMaintained says the recipe chooses the family on its own.
func goFamilyMaintained(family string) bool {
	for _, entry := range goRecipeFamilies {
		if entry.family == family {
			return !entry.eol
		}
	}
	return false
}

func goModuleMinimum(module []byte) string {
	for _, line := range strings.Split(string(module), "\n") {
		line, _, _ = strings.Cut(line, "//")
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "go" && stableGoVersionRE.MatchString(fields[1]) {
			return fields[1]
		}
	}
	return ""
}

// goVersionChoice is the toolchain the recipe builds with and why.
type goVersionChoice struct {
	version string
	// source is where it came from: "build.goVersion", ".go-version",
	// "go.mod", "toolchain" or "default".
	source  string
	minimum string
	// toolchain is the go.mod toolchain line when it asked for a release
	// newer than any the recipe builds with; the newest family builds
	// instead, and GOTOOLCHAIN=local keeps a real incompatibility a build
	// error rather than a download.
	downgraded string
	eol        bool
}

// note is the build evidence for a choice go.mod made: its go line is a
// minimum, and the family's maintained patch satisfies it.
func (c goVersionChoice) note() string {
	switch {
	case c.downgraded != "":
		return "go.mod asks for toolchain " + c.downgraded + ", newer than the recipe builds with; building with the maintained Go " + c.version
	case c.source == "go.mod" && c.minimum != "" && c.minimum != c.version:
		return "go " + c.minimum + " in go.mod is a minimum; building with the maintained Go " + c.version + " patch"
	case c.source == "toolchain":
		return "go.mod's toolchain line prefers Go " + c.version + "; building with its maintained patch"
	case c.source == "default" && c.minimum != "":
		return "go " + c.minimum + " in go.mod predates the maintained Go releases; building with Go " + c.version
	}
	return ""
}

func chooseGoRecipeVersion(explicit, versionFile string, module []byte) (string, error) {
	choice, err := resolveGoRecipeVersion(explicit, versionFile, module)
	return choice.version, err
}

// resolveGoRecipeVersion chooses the Go toolchain. A version the operator
// or .go-version pins is built exactly. What go.mod says is a minimum, so the
// recipe builds on that language family's image — its newest patch, with the
// security fixes a .0 lacks — and treats the toolchain line as a preference
// it follows while it can.
func resolveGoRecipeVersion(explicit, versionFile string, module []byte) (goVersionChoice, error) {
	minimum, suggested := "", ""
	for _, line := range strings.Split(string(module), "\n") {
		line, _, _ = strings.Cut(line, "//")
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		switch fields[0] {
		case "go":
			minimum = fields[1]
		case "toolchain":
			if fields[1] != "default" {
				suggested = strings.TrimPrefix(fields[1], "go")
			}
		}
	}
	for _, value := range []string{minimum, suggested} {
		if value != "" && !stableGoVersionRE.MatchString(value) {
			return goVersionChoice{}, fmt.Errorf("%w: automatic Go builds require stable go.mod/toolchain versions; use a Dockerfile for custom toolchains", ErrUnsupportedBuilder)
		}
	}
	choice := goVersionChoice{minimum: minimum}
	newest := defaultGoRecipeVersion
	switch {
	case strings.TrimSpace(explicit) != "":
		choice.version, choice.source = strings.TrimPrefix(strings.TrimSpace(explicit), "go"), "build.goVersion"
	case strings.TrimSpace(versionFile) != "":
		choice.version, choice.source = strings.TrimPrefix(strings.TrimSpace(versionFile), "go"), ".go-version"
	default:
		choice.version, choice.source = defaultGoRecipeVersion, "default"
		minimumFamily, suggestedFamily := "", ""
		if minimum != "" {
			minimumFamily = strings.TrimPrefix(version.Lang("go"+minimum), "go")
			if version.Compare("go"+minimumFamily, "go"+newest) > 0 {
				return goVersionChoice{}, fmt.Errorf("%w: go.mod requires Go %s; the Go recipe builds with Go %s; use a Dockerfile until the recipe supports it",
					ErrUnsupportedBuilder, minimum, goRecipeVersionList())
			}
		}
		if suggested != "" {
			suggestedFamily = strings.TrimPrefix(version.Lang("go"+suggested), "go")
		}
		switch {
		case suggestedFamily != "" && version.Compare("go"+suggestedFamily, "go"+newest) > 0:
			choice.version, choice.source, choice.downgraded = newest, "toolchain", "go"+suggested
		case suggestedFamily != "" && goFamilyMaintained(suggestedFamily) &&
			(minimumFamily == "" || version.Compare("go"+suggestedFamily, "go"+minimumFamily) >= 0):
			choice.version, choice.source = suggestedFamily, "toolchain"
		case minimumFamily != "" && goFamilyMaintained(minimumFamily):
			choice.version, choice.source = minimumFamily, "go.mod"
		}
	}
	if !goRecipeVersionRE.MatchString(choice.version) {
		return goVersionChoice{}, fmt.Errorf("%w: the Go recipe supports stable Go %s; select a supported version or use a Dockerfile", ErrUnsupportedBuilder, goRecipeVersionList())
	}
	if minimum != "" && version.Compare("go"+choice.version, "go"+minimum) < 0 &&
		!(strings.Count(choice.version, ".") == 1 && version.Lang("go"+choice.version) == version.Lang("go"+minimum)) {
		return goVersionChoice{}, fmt.Errorf("%w: selected Go toolchain is older than go.mod requires", ErrUnsupportedBuilder)
	}
	choice.eol = goFamilyEOL(strings.TrimPrefix(version.Lang("go"+choice.version), "go"))
	return choice, nil
}
