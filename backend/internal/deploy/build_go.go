package deploy

import (
	"fmt"
	"go/parser"
	"go/token"
	"go/version"
	"regexp"
	"strings"
)

const defaultGoRecipeVersion = "1.26"

var goRecipeVersionRE = regexp.MustCompile(`^1\.(25|26)(\.[0-9]{1,3})?$`)
var stableGoVersionRE = regexp.MustCompile(`^1\.[0-9]{1,3}(\.[0-9]{1,3})?$`)
var cgoEnabledCommandRE = regexp.MustCompile(`(^|[\s;&])CGO_ENABLED\s*=\s*['"]?1(['"]|[\s;&]|$)`)

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

func chooseGoRecipeVersion(explicit, versionFile string, module []byte) (string, error) {
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
			return "", fmt.Errorf("%w: automatic Go builds require stable go.mod/toolchain versions; use a Dockerfile for custom toolchains", ErrUnsupportedBuilder)
		}
	}
	selected := strings.TrimPrefix(strings.TrimSpace(explicit), "go")
	if selected == "" {
		selected = strings.TrimPrefix(strings.TrimSpace(versionFile), "go")
	}
	if selected == "" {
		selected = minimum
		if version.Compare("go"+suggested, "go"+selected) > 0 {
			selected = suggested
		}
		// Older module language requirements remain compatible with the
		// maintained default; an explicit old toolchain is not silently replaced.
		if selected == "" || version.Compare("go"+selected, "go1.25") < 0 {
			selected = defaultGoRecipeVersion
		}
	}
	if !goRecipeVersionRE.MatchString(selected) {
		return "", fmt.Errorf("%w: the Go recipe supports stable Go 1.25 and 1.26; select a supported version or use a Dockerfile", ErrUnsupportedBuilder)
	}
	if minimum != "" && version.Compare("go"+selected, "go"+minimum) < 0 &&
		!(strings.Count(selected, ".") == 1 && version.Lang("go"+selected) == version.Lang("go"+minimum)) {
		return "", fmt.Errorf("%w: selected Go toolchain is older than go.mod requires", ErrUnsupportedBuilder)
	}
	return selected, nil
}

func sourceUsesCGO(content []byte) bool {
	file, err := parser.ParseFile(token.NewFileSet(), "source.go", content, parser.ImportsOnly)
	if err != nil {
		return false
	}
	for _, imported := range file.Imports {
		if imported.Path.Value == `"C"` || imported.Path.Value == "`C`" {
			return true
		}
	}
	return false
}
