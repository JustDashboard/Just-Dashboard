package deploy

import (
	"strings"
	"testing"
)

// siteFindings runs preflight over a fixture's detection with the plan
// quick setup would save, and the source's submodule switch.
func siteFindings(t *testing.T, files map[string]string, submodules bool) ([]PreflightFinding, *DetectedCandidate) {
	t.Helper()
	result := detectShapeFixture(t, files)
	candidate := selectedOf(result)
	if candidate == nil {
		t.Fatalf("nothing selected: %#v", result.Candidates)
	}
	draft := nodeDraft(result)
	draft.Data.Source.IncludeSubmodules = submodules
	configuration := nodeTestConfiguration(BuildPlanConfig{Method: candidate.BuildMethod, Recipe: candidate.Recipe,
		BuildCommand: candidate.BuildCommand, StartCommand: candidate.StartCommand, OutputDirectory: candidate.OutputDirectory,
		SPAFallback: candidate.SPAFallback, PythonVersion: candidate.PythonVersion})
	return preflightFindings(draft, configuration, dockerHost, false), candidate
}

func TestPreflightSaysWhatASiteGeneratorNeeds(t *testing.T) {
	t.Parallel()
	hugo := map[string]string{
		"hugo.toml": "baseURL = '/'\ntheme = 'ananke'\n", "content/_index.md": "",
		".gitmodules": "[submodule \"themes/ananke\"]\n\tpath = themes/ananke\n\turl = https://github.com/theNewDynamic/gohugo-theme-ananke.git\n",
	}
	findings, _ := siteFindings(t, hugo, false)
	theme := findingByCode(findings, "site_theme_in_submodule")
	if theme == nil || theme.Severity != PreflightBlocked || theme.Measured != "themes/ananke" || theme.FieldID != "source.includeSubmodules" {
		t.Fatalf("theme = %+v", theme)
	}
	if unpinned := findingByCode(findings, "hugo_version_unpinned"); unpinned == nil || unpinned.Severity != PreflightWarning ||
		!strings.Contains(unpinned.Action, ".hvm") {
		t.Fatalf("unpinned = %+v", unpinned)
	}
	if findingByCode(findings, "start_command_missing") != nil || findingByCode(findings, "recipe_unsupported") != nil {
		t.Fatalf("a site was asked for a server: %+v", findings)
	}
	findings, _ = siteFindings(t, hugo, true)
	if findingByCode(findings, "site_theme_in_submodule") != nil {
		t.Fatal("a fetched submodule still blocks")
	}
	hugo[".hvm"] = "v0.120.0\n"
	findings, _ = siteFindings(t, hugo, true)
	if version := findingByCode(findings, "site_generator_version"); version == nil || !strings.Contains(version.Measured, "0.120.0") ||
		findingByCode(findings, "hugo_version_unpinned") != nil {
		t.Fatalf("an old pin = %+v", findings)
	}
	findings, _ = siteFindings(t, map[string]string{"Gemfile": "ruby '3.0.6'\ngem 'jekyll'\n", "_config.yml": "title: x\n", "_posts/a.md": ""}, false)
	if ruby := findingByCode(findings, "jekyll_ruby_version"); ruby == nil || ruby.Severity != PreflightWarning {
		t.Fatalf("ruby = %+v", findings)
	}
	if unpinned := findingByCode(findings, "dependencies_unpinned"); unpinned == nil || !strings.Contains(unpinned.Action, "bundle lock") ||
		strings.Contains(unpinned.Action, "uv lock") {
		t.Fatalf("jekyll unpinned = %+v", unpinned)
	}
}

func TestPreflightSaysHowAStaticSiteIsServed(t *testing.T) {
	t.Parallel()
	findings, _ := siteFindings(t, map[string]string{
		"package.json":         `{"scripts":{"build":"docusaurus build"},"dependencies":{"@docusaurus/core":"3"}}`,
		"package-lock.json":    "{}",
		"docusaurus.config.js": "module.exports = { baseUrl: '/project/' }",
		"static/_redirects":    "/old /new 301\n/post/:id /p/:id 301\n",
	}, false)
	if base := findingByCode(findings, "static_base_path"); base == nil || base.Severity != PreflightWarning || !strings.Contains(base.Measured, "/project") {
		t.Fatalf("base = %+v", findings)
	}
	if left := findingByCode(findings, "static_redirects_unsupported"); left == nil || left.Measured != "1 rule(s) left out" {
		t.Fatalf("left out = %+v", left)
	}
	if applied := findingByCode(findings, "static_hosting_rules"); applied == nil || applied.Severity != PreflightPass {
		t.Fatalf("applied = %+v", applied)
	}
	findings, _ = siteFindings(t, map[string]string{
		"package.json": `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"7"}}`, "package-lock.json": "{}",
		"vite.config.js": "export default { base: process.env.BASE }",
	}, false)
	if computed := findingByCode(findings, "static_base_path_computed"); computed == nil {
		t.Fatalf("computed base = %+v", findings)
	}
}

func TestPreflightNeedsNoStartCommandForASiteBuild(t *testing.T) {
	t.Parallel()
	for name, files := range map[string]map[string]string{
		"mkdocs": {"mkdocs.yml": "site_name: x\n", "requirements.txt": "mkdocs==1.6.1\n", "docs/index.md": ""},
		"lume":   {"deno.json": `{"imports":{"lume/":"https://deno.land/x/lume@v3.2.5/"},"tasks":{"build":"deno task lume","lume":"x"}}`, "deno.lock": "{}"},
	} {
		findings, candidate := siteFindings(t, files, false)
		if findingByCode(findings, "start_command_missing") != nil || candidate.StartCommand != "" {
			t.Fatalf("%s asked for a start command: %+v", name, findings)
		}
	}
	findings, _ := siteFindings(t, map[string]string{"mkdocs.yml": "site_name: x\n", "docs/index.md": ""}, false)
	if unpinned := findingByCode(findings, "dependencies_unpinned"); unpinned == nil || !strings.Contains(unpinned.Measured, "pinned MkDocs releases") ||
		!strings.Contains(unpinned.Action, "requirements.txt") {
		t.Fatalf("mkdocs unpinned = %+v", unpinned)
	}
}

// Advice about pinning dependencies names the recipe's own tool.
func TestUnpinnedDependencyAdviceSpeaksTheRecipesLanguage(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		candidate DetectedCandidate
		measured  string
		action    string
	}{
		{DetectedCandidate{Recipe: "php"}, "composer.json without composer.lock", "composer install"},
		{DetectedCandidate{Recipe: "deno"}, "deno.json imports without deno.lock", "deno install"},
		{DetectedCandidate{Recipe: "rust"}, "Cargo.toml without Cargo.lock", "cargo generate-lockfile"},
		{DetectedCandidate{Recipe: "site", Framework: "jekyll"}, "no Gemfile.lock; the build resolves the gems", "bundle lock"},
		{DetectedCandidate{Recipe: "python"}, "unpinned entries in the dependency manifest", "uv lock"},
	} {
		measured, action := unpinnedDependencyAdvice(&test.candidate)
		if measured != test.measured || !strings.Contains(action, test.action) || !strings.HasSuffix(action, "deploying as is works today.") {
			t.Fatalf("%+v: %q / %q", test.candidate, measured, action)
		}
		if test.candidate.Recipe != "python" && strings.Contains(action, "uv lock") {
			t.Fatalf("%s was told how to pin Python", test.candidate.Recipe)
		}
	}
}
