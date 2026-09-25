package deploy

import (
	"reflect"
	"strings"
	"testing"
)

// The site generators' own words for why a build stopped, from fixture
// builds on each generator's image.
func siteBuildCases() []buildCase {
	hugo := BuildPlanConfig{Method: BuildRecipe, Recipe: "site", BuildCommand: `hugo --gc --minify --baseURL "${HUGO_BASEURL:-/}"`, OutputDirectory: "public"}
	zola := BuildPlanConfig{Method: BuildRecipe, Recipe: "site", BuildCommand: "zola build --base-url /", OutputDirectory: "public"}
	jekyll := BuildPlanConfig{Method: BuildRecipe, Recipe: "site", BuildCommand: `bundle exec jekyll build --baseurl ""`, OutputDirectory: "_site"}
	mkdocs := BuildPlanConfig{Method: BuildRecipe, Recipe: "python", BuildCommand: "mkdocs build", OutputDirectory: "site"}
	sphinx := BuildPlanConfig{Method: BuildRecipe, Recipe: "python", BuildCommand: "sphinx-build -b html docs docs/_build/html", OutputDirectory: "docs/_build/html"}
	mdbook := BuildPlanConfig{Method: BuildRecipe, Recipe: "site", BuildCommand: "mdbook build", OutputDirectory: "book"}
	return []buildCase{
		{
			name: "Hugo theme submodule not fetched", command: hugo.BuildCommand, exit: 1, build: hugo,
			lines: []string{`ERROR failed to load modules: module "ananke" not found in "/project/themes/ananke"; either add it as a Hugo Module or store it in "/project/themes".: module does not exist`},
			want:  BuildCause{Code: "build_theme_missing", Phase: phaseBuild, Command: hugo.BuildCommand, ExitCode: 1, Detail: "hugo", Subjects: []string{"ananke"}},
		},
		{
			name: "Hugo template", command: hugo.BuildCommand, exit: 1, build: hugo,
			lines: []string{`ERROR error building site: "/project/layouts/index.html:1:1": parse of template failed: template: index.html:1: function "nosuchfunc" not defined`},
			want:  BuildCause{Code: "build_site_render_failed", Phase: phaseBuild, Command: hugo.BuildCommand, ExitCode: 1, Detail: "hugo", Subjects: []string{"layouts/index.html"}},
		},
		{
			name: "Zola theme", command: zola.BuildCommand, exit: 1, build: zola,
			lines: []string{"ERROR Failed to build the site", "ERROR Failed to load theme nope", "ERROR Reason: Failed to open file /app/themes/nope/theme.toml"},
			want:  BuildCause{Code: "build_theme_missing", Phase: phaseBuild, Command: zola.BuildCommand, ExitCode: 1, Detail: "zola", Subjects: []string{"nope"}},
		},
		{
			name: "Zola template", command: zola.BuildCommand, exit: 1, build: zola,
			lines: []string{"Building site...", "ERROR Failed to build the site", "ERROR error: Unknown filter `nosuchfilter`", " --> index.html:1:16"},
			want:  BuildCause{Code: "build_site_render_failed", Phase: phaseBuild, Command: zola.BuildCommand, ExitCode: 1, Detail: "zola", Subjects: []string{"index.html"}},
		},
		{
			name: "Jekyll theme gem", command: jekyll.BuildCommand, exit: 1, build: jekyll,
			lines: []string{"jekyll 4.4.1 | Error:  The minima theme could not be found."},
			want:  BuildCause{Code: "build_theme_missing", Phase: phaseBuild, Command: jekyll.BuildCommand, ExitCode: 1, Detail: "jekyll", Subjects: []string{"minima"}},
		},
		{
			name: "Jekyll Liquid", command: jekyll.BuildCommand, exit: 1, build: jekyll,
			lines: []string{"  Liquid Exception: Liquid syntax error (line 3): Unknown tag 'nope' in /_layouts/default.html", "             ERROR: YOUR SITE COULD NOT BE BUILT:"},
			want:  BuildCause{Code: "build_site_render_failed", Phase: phaseBuild, Command: jekyll.BuildCommand, ExitCode: 1, Detail: "jekyll", Subjects: []string{"_layouts/default.html"}},
		},
		{
			name: "MkDocs theme package", command: mkdocs.BuildCommand, exit: 1, build: mkdocs,
			lines: []string{"ERROR   -  Config value 'theme': Unrecognised theme name: 'material'. The available installed themes are: mkdocs, readthedocs", "Aborted with a configuration error!"},
			want:  BuildCause{Code: "build_theme_missing", Phase: phaseBuild, Command: mkdocs.BuildCommand, ExitCode: 1, Detail: "mkdocs", Subjects: []string{"material"}},
		},
		{
			name: "MkDocs plugin", command: mkdocs.BuildCommand, exit: 1, build: mkdocs,
			lines: []string{`ERROR   -  Config value 'plugins': The "minify" plugin is not installed`},
			want:  BuildCause{Code: "build_module_not_found", Phase: phaseBuild, Command: mkdocs.BuildCommand, ExitCode: 1, Detail: "mkdocs", Subjects: []string{"minify"}},
		},
		{
			name: "Sphinx extension", command: sphinx.BuildCommand, exit: 2, build: sphinx,
			lines: []string{"Extension error:", "Could not import extension myst_parser (exception: No module named 'myst_parser')"},
			want:  BuildCause{Code: "build_module_not_found", Phase: phaseBuild, Command: sphinx.BuildCommand, ExitCode: 2, Detail: "sphinx", Subjects: []string{"myst_parser"}},
		},
		{
			name: "Sphinx theme", command: sphinx.BuildCommand, exit: 2, build: sphinx,
			lines: []string{"Theme error:", "no theme named 'furo' found (missing theme.toml?)"},
			want:  BuildCause{Code: "build_theme_missing", Phase: phaseBuild, Command: sphinx.BuildCommand, ExitCode: 2, Detail: "sphinx", Subjects: []string{"furo"}},
		},
		{
			name: "mdBook preprocessor", command: mdbook.BuildCommand, exit: 101, build: mdbook,
			lines: []string{" INFO Book building has started",
				"ERROR The command `mdbook-mermaid` wasn't found, is the `mermaid` preprocessor installed? If you want to ignore this error when the `mermaid` preprocessor is not installed, set `optional = true` in the `[preprocessor.mermaid]` section of the book.toml configuration file.",
				"ERROR Unable to run the preprocessor `mermaid`"},
			want: BuildCause{Code: "build_command_not_found", Phase: phaseBuild, Command: mdbook.BuildCommand, ExitCode: 101, Detail: "mdbook", Subjects: []string{"mdbook-mermaid"}},
		},
		{
			name: "Eleventy template", command: "npx eleventy", exit: 1,
			build: BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "npx eleventy", OutputDirectory: "_site"},
			lines: []string{"[11ty] Problem writing Eleventy templates:", "[11ty] 1. Having trouble rendering njk template ./src/index.njk (via TemplateContentRenderError)"},
			want: BuildCause{Code: "build_site_render_failed", Phase: phaseBuild, Command: "npx eleventy", ExitCode: 1, Detail: "eleventy",
				Subjects: []string{"./src/index.njk"}},
		},
	}
}

func TestBuildFailureCauseNamesSiteGeneratorFailures(t *testing.T) {
	t.Parallel()
	for _, test := range siteBuildCases() {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			collector, err := feedBuild(test)
			cause := buildFailureCause(err, collector, causeContext{build: test.build, hostMemory: 4 << 30}, nil)
			if cause == nil || cause.LineSeq == 0 {
				t.Fatalf("cause = %+v", cause)
			}
			got := *cause
			got.LineSeq = 0
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("cause = %+v\nwant    %+v", got, test.want)
			}
			sentence := cause.sentence()
			if len(sentence) > causeSentenceLength || !strings.HasSuffix(sentence, ".") || causeTitle(cause.Code) == "" {
				t.Fatalf("sentence = %q", sentence)
			}
			if cause.Code == "build_theme_missing" && (test.want.Detail == "hugo" || test.want.Detail == "zola") && !strings.Contains(sentence, "submodules") {
				t.Fatalf("a missing theme does not point at submodules: %q", sentence)
			}
		})
	}
}
