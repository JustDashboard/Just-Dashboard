package deploy

import (
	"strings"
	"testing"
)

// siteCandidate is the one candidate a site fixture must produce, checked
// against what quick setup would save.
func siteCandidate(t *testing.T, files map[string]string, identity ...SourceIdentity) (DetectionResult, *DetectedCandidate) {
	t.Helper()
	result := detectShapeFixture(t, files, identity...)
	selected := selectedOf(result)
	if selected == nil {
		t.Fatalf("nothing selected (%s): %#v", result.SelectionReason, result.Candidates)
	}
	if err := validateDetectionResult(&DraftSourceConfig{Kind: SourceGit}, withGitSource(result)); err != nil {
		t.Fatalf("the detection would not save: %v", err)
	}
	return result, selected
}

func TestHugoSitesAreBuiltByHugo(t *testing.T) {
	t.Parallel()
	t.Run("layouts are templates, not a site", func(t *testing.T) {
		result, site := siteCandidate(t, map[string]string{
			"hugo.toml": "baseURL = 'https://user.github.io/repo/'\ntitle = 'x'\npublishDir = 'dist'\n", "content/_index.md": "---\ntitle: x\n---\n",
			"layouts/index.html": "{{ .Title }}", "themes/ananke/layouts/index.html": "{{ .Content }}", "themes/ananke/theme.toml": "min_version = '0.100.0'\n",
			"static/index.html": "<h1>static</h1>",
		})
		if len(result.Candidates) != 1 || site.Recipe != "site" || site.Framework != "hugo" || site.Profile != ProfileStatic || site.Port != 80 ||
			site.OutputDirectory != "dist" || site.BuildCommand != `hugo --gc --minify --baseURL "${HUGO_BASEURL:-/}"` || site.Confidence != ConfidenceHigh {
			t.Fatalf("candidates = %#v", result.Candidates)
		}
		baseURL := candidateVariable(site, "HUGO_BASEURL")
		if baseURL == nil || baseURL.Setup != "domain" || baseURL.Phase != "build" || baseURL.DomainTemplate != "{{scheme}}://{{hostname}}/" {
			t.Fatalf("HUGO_BASEURL = %#v", baseURL)
		}
		if site.StaticSite == nil || !site.StaticSite.Unpinned || site.StaticSite.Version != hugoDefaultVersion {
			t.Fatalf("site = %#v", site.StaticSite)
		}
	})
	t.Run("a theme submodule is named", func(t *testing.T) {
		_, site := siteCandidate(t, map[string]string{
			"hugo.toml": "baseURL = '/'\ntheme = 'ananke'\n", "content/_index.md": "",
			".gitmodules": "[submodule \"themes/ananke\"]\n\tpath = themes/ananke\n\turl = https://github.com/theNewDynamic/gohugo-theme-ananke.git\n",
		})
		if site.StaticSite.ThemeSubmodule != "themes/ananke" || !evidenceMentions(site, "Git submodule themes/ananke") {
			t.Fatalf("site = %#v", site.StaticSite)
		}
	})
	t.Run("a Hugo Modules go.mod is not a Go service", func(t *testing.T) {
		result, site := siteCandidate(t, map[string]string{
			"hugo.toml": "baseURL = '/'\n[module]\n[[module.imports]]\npath = 'github.com/google/docsy'\n[module.hugoVersion]\nmin = '0.146.0'\n",
			"go.mod":    "module example.com/site\n\ngo 1.22\n\nrequire github.com/google/docsy v0.12.0 // indirect\n", "content/_index.md": "",
		})
		if len(result.Candidates) != 1 || site.Framework != "hugo" || setAsideKind(result, "tooling") == nil {
			t.Fatalf("candidates = %#v, set aside %#v", result.Candidates, result.SetAside)
		}
		// A floor is not a pin: the next rebuild may take a newer release.
		if !site.StaticSite.Unpinned || !strings.Contains(site.StaticSite.Declared, "0.146.0 or newer") {
			t.Fatalf("site = %#v", site.StaticSite)
		}
	})
	t.Run("a Go program beside the site stays one", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"hugo.toml": "baseURL = '/'\n", "content/_index.md": "",
			"go.mod": "module example.com/app\n\ngo 1.26\n", "main.go": "package main\n\nfunc main() {}\n",
		})
		if candidateAtRoot(result, "", BuildRecipe) == nil || len(result.Candidates) != 2 {
			t.Fatalf("candidates = %#v", result.Candidates)
		}
	})
	t.Run("the legacy config name with content and a theme", func(t *testing.T) {
		_, site := siteCandidate(t, map[string]string{
			"config.toml": "baseurl = \"https://example.org/\"\ntheme = \"x\"\n", "content/post.md": "", "archetypes/default.md": "",
		})
		if site.Framework != "hugo" || !evidenceMentions(site, "baseURL beside content/") {
			t.Fatalf("site = %#v", site)
		}
	})
	t.Run("configuration in config/_default", func(t *testing.T) {
		_, site := siteCandidate(t, map[string]string{
			"site/config/_default/hugo.toml": "baseURL = '/'\n", "site/content/_index.md": "",
		})
		if site.Root != "site" || site.Framework != "hugo" {
			t.Fatalf("site = %#v", site)
		}
	})
	t.Run("pins", func(t *testing.T) {
		for name, files := range map[string]map[string]string{
			".hvm":                       {".hvm": "v0.148.2\n"},
			"netlify.toml":               {"netlify.toml": "[build.environment]\n  HUGO_VERSION = \"0.148.2\"\n"},
			"workflow":                   {".github/workflows/hugo.yml": "env:\n  HUGO_VERSION: 0.148.2\n"},
			"package.json hugo-extended": {"package.json": `{"devDependencies":{"hugo-extended":"^0.148.2"}}`},
			".tool-versions":             {".tool-versions": "hugo extended_0.148.2\n"},
		} {
			files["hugo.toml"], files["content/_index.md"] = "baseURL = '/'\n", ""
			_, site := siteCandidate(t, files)
			if site.StaticSite.Version != "0.148.2" || site.StaticSite.Unpinned || site.StaticSite.VersionIssue != "" {
				t.Fatalf("%s: site = %#v", name, site.StaticSite)
			}
		}
		_, site := siteCandidate(t, map[string]string{"hugo.toml": "baseURL = '/'\n", "content/_index.md": "", ".hvm": "v0.119.0\n"})
		if site.StaticSite.Version != hugoDefaultVersion || !strings.Contains(site.StaticSite.VersionIssue, "oldest official Hugo image is "+hugoOldestImage) {
			t.Fatalf("an old pin = %#v", site.StaticSite)
		}
	})
	t.Run("a theme's own configuration is not a site", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"themes/x/hugo.toml": "baseURL = '/'\n", "themes/x/exampleSite/hugo.toml": "baseURL = '/'\n",
		})
		for _, candidate := range result.Candidates {
			if candidate.Framework == "hugo" {
				t.Fatalf("a theme became a site: %#v", candidate)
			}
		}
	})
}

func TestZolaAndMdBookSitesAreBuiltByTheirGenerators(t *testing.T) {
	t.Parallel()
	result, zola := siteCandidate(t, map[string]string{
		"config.toml": "base_url = \"https://example.org\"\noutput_dir = \"out\"\n", "templates/index.html": "{{ config.title }}",
		"content/_index.md": "", "netlify.toml": "[build.environment]\n  ZOLA_VERSION = \"0.19.2\"\n",
	})
	if len(result.Candidates) != 1 || zola.Framework != "zola" || zola.Recipe != "site" || zola.OutputDirectory != "out" ||
		zola.BuildCommand != "zola build --base-url /" || zola.StaticSite.Version != "0.19.2" || setAsideKind(result, "template") == nil {
		t.Fatalf("zola = %#v", result.Candidates)
	}
	_, book := siteCandidate(t, map[string]string{
		"book.toml": "[book]\ntitle = \"x\"\n[build]\nbuild-dir = \"public\"\n", "src/SUMMARY.md": "# Summary\n",
	})
	if book.Framework != "mdbook" || book.OutputDirectory != "public" || book.BuildCommand != "mdbook build" || book.StaticSite.Version != "0.5.4" {
		t.Fatalf("mdbook = %#v", book)
	}
	_, several := siteCandidate(t, map[string]string{
		"book.toml": "[book]\ntitle = \"x\"\n[output.html]\ncurly-quotes = true\n[output.linkcheck]\noptional = true\n", "src/SUMMARY.md": "",
	})
	if several.OutputDirectory != "book/html" || several.StaticSite.Version != "0.4.52" || several.RecipeIssue != "" {
		t.Fatalf("mdbook with two renderers = %#v", several)
	}
	result = detectShapeFixture(t, map[string]string{
		"book.toml": "[book]\ntitle = \"x\"\n[preprocessor.mermaid]\ncommand = \"mdbook-mermaid\"\n", "src/SUMMARY.md": "",
	})
	if len(result.Candidates) != 1 || !strings.Contains(result.Candidates[0].RecipeIssue, "mdbook-mermaid") || result.Candidates[0].Confidence != ConfidenceLow {
		t.Fatalf("mdbook preprocessor = %#v", result.Candidates)
	}
}

func TestJekyllSitesAreBuiltByJekyll(t *testing.T) {
	t.Parallel()
	result, site := siteCandidate(t, map[string]string{
		"Gemfile": "source 'https://rubygems.org'\ngem 'jekyll', '~> 4.4'\n", "_config.yml": "title: Blog\nbaseurl: /blog\nurl: https://example.org\ndestination: public\n",
		"index.html": "---\nlayout: default\n---\n{% for post in site.posts %}{{ post.title }}{% endfor %}", "_posts/2024-01-01-a.md": "",
		".ruby-version": "3.2.4\n",
	})
	if len(result.Candidates) != 1 || site.Framework != "jekyll" || site.Recipe != "site" || site.OutputDirectory != "public" ||
		site.BuildCommand != `bundle exec jekyll build --baseurl ""` || site.StaticSite.Version != "3.2" || !site.UnpinnedDependencies ||
		!evidenceMentions(site, "baseurl /blog") {
		t.Fatalf("jekyll = %#v", result.Candidates)
	}
	_, pages := siteCandidate(t, map[string]string{"_config.yml": "title: Pages\ntheme: minima\n", "index.md": "# Hello\n"})
	if pages.Framework != "jekyll" || pages.Confidence != ConfidenceMedium ||
		pages.BuildCommand != `bundle exec jekyll build --baseurl "" --config _config.yml,`+jekyllURLOverride {
		t.Fatalf("pages = %#v", pages)
	}
	_, pinned := siteCandidate(t, map[string]string{
		"Gemfile": "ruby '2.7.8'\ngem 'github-pages', group: :jekyll_plugins\n", "_config.yml": "title: x\n",
		"Gemfile.lock": "GEM\n  specs:\n    github-pages (232)\n\nPLATFORMS\n  arm64-darwin-23\n\nDEPENDENCIES\n  github-pages\n",
	})
	if pinned.StaticSite.Version != jekyllDefaultRuby || !strings.Contains(pinned.StaticSite.VersionIssue, "2.7.8") ||
		!evidenceMentions(pinned, "no Linux platform") || pinned.UnpinnedDependencies {
		t.Fatalf("pinned = %#v", pinned)
	}
	// .nojekyll says the site is served as it is; Hexo's _config.yml is Hexo's.
	nojekyll := detectShapeFixture(t, map[string]string{"_config.yml": "theme: minima\n", ".nojekyll": "", "index.html": "<h1>x</h1>"})
	if selectedOf(nojekyll) == nil || selectedOf(nojekyll).BuildMethod != BuildStatic {
		t.Fatalf(".nojekyll = %#v", nojekyll.Candidates)
	}
	hexo := detectShapeFixture(t, map[string]string{
		"_config.yml": "title: x\npublic_dir: www\n", "source/_posts/a.md": "",
		"package.json": `{"name":"blog","dependencies":{"hexo":"^7.3.0"}}`, "package-lock.json": "{}",
	})
	if site := selectedOf(hexo); site == nil || site.Framework != "hexo" || site.OutputDirectory != "www" || site.BuildCommand != "npx hexo generate" {
		t.Fatalf("hexo = %#v", hexo.Candidates)
	}
}

func TestPythonDocumentationSitesAreBuiltAndServed(t *testing.T) {
	t.Parallel()
	result, mkdocs := siteCandidate(t, map[string]string{
		"mkdocs.yml":       "site_name: Docs\nsite_dir: public\ntheme:\n  name: material\nmarkdown_extensions:\n  - pymdownx.emoji:\n      emoji_index: !!python/name:material.extensions.emoji.twemoji\n",
		"requirements.txt": "mkdocs-material==9.7.7\n", "docs/index.md": "# x\n",
	})
	if len(result.Candidates) != 1 || mkdocs.Recipe != "python" || mkdocs.Framework != "mkdocs" || mkdocs.OutputDirectory != "public" ||
		mkdocs.BuildCommand != "mkdocs build" || mkdocs.StartCommand != "" || mkdocs.Profile != ProfileStatic || mkdocs.PythonVersion == "" ||
		mkdocs.UnpinnedDependencies || setAsideKind(result, "tooling") == nil {
		t.Fatalf("mkdocs = %#v / %#v", result.Candidates, result.SetAside)
	}
	_, bare := siteCandidate(t, map[string]string{"mkdocs.yml": "site_name: Docs\ntheme: material\nplugins:\n  - search\n  - minify\n", "docs/index.md": ""})
	if !bare.UnpinnedDependencies || !bare.StaticSite.Unpinned || !evidenceMentions(bare, "mkdocs-minify-plugin==") {
		t.Fatalf("bare mkdocs = %#v", bare)
	}
	unknown := detectShapeFixture(t, map[string]string{"mkdocs.yml": "site_name: Docs\nplugins:\n  - mystery\n", "docs/index.md": ""})
	if len(unknown.Candidates) != 1 || !strings.Contains(unknown.Candidates[0].RecipeIssue, "plugin mystery") {
		t.Fatalf("unknown plugin = %#v", unknown.Candidates)
	}
	// Sphinx builds from the project above its docs, where autodoc's code is.
	result, sphinx := siteCandidate(t, map[string]string{
		"pyproject.toml": "[project]\nname = \"lib\"\ndependencies = [\"requests\"]\n", "lib/__init__.py": "",
		"docs/conf.py":   "project = 'lib'\nextensions = ['sphinx.ext.autodoc', 'myst_parser']\nhtml_theme = 'furo'\n",
		"docs/index.rst": "lib\n===\n", "docs/requirements.txt": "sphinx\nfuro\nmyst-parser\n",
	})
	if sphinx.Root != "" || sphinx.Framework != "sphinx" || sphinx.BuildCommand != "sphinx-build -b html docs docs/_build/html" ||
		sphinx.OutputDirectory != "docs/_build/html" || !evidenceMentions(sphinx, "documentation requirements") || len(result.Candidates) != 1 {
		t.Fatalf("sphinx = %#v", result.Candidates)
	}
	_, rtd := siteCandidate(t, map[string]string{
		".readthedocs.yaml":  "version: 2\nbuild:\n  tools:\n    python: \"3.12\"\nsphinx:\n  configuration: doc/source/conf.py\npython:\n  install:\n    - requirements: doc/requirements.txt\n",
		"doc/source/conf.py": "project = 'x'\n", "doc/source/index.rst": "x\n",
	})
	if rtd.Framework != "sphinx" || rtd.PythonVersion != "3.12" || rtd.OutputDirectory != "doc/source/_build/html" {
		t.Fatalf("read the docs = %#v", rtd)
	}
	_, pelican := siteCandidate(t, map[string]string{
		"pelicanconf.py": "PATH = 'posts'\nOUTPUT_PATH = 'public/'\n", "publishconf.py": "from pelicanconf import *\n", "posts/a.md": "",
	})
	if pelican.Framework != "pelican" || pelican.BuildCommand != "pelican posts -o public -s publishconf.py" || pelican.OutputDirectory != "public" {
		t.Fatalf("pelican = %#v", pelican)
	}
	// A web application's own documentation is its manual, beside it.
	result = detectShapeFixture(t, map[string]string{
		"requirements.txt": "fastapi==0.116.1\nuvicorn==0.35.0\nmkdocs==1.6.1\n", "main.py": "from fastapi import FastAPI\napp = FastAPI()\n",
		"mkdocs.yml": "site_name: Manual\n", "docs/index.md": "",
	})
	if selected := selectedOf(result); selected == nil || selected.Framework != "fastapi" || len(result.Candidates) != 2 {
		t.Fatalf("the application lost to its manual: %#v", result.Candidates)
	}
}

func TestLumeAndNodeSiteGenerators(t *testing.T) {
	t.Parallel()
	_, lume := siteCandidate(t, map[string]string{
		"deno.json":  `{"imports":{"lume/":"https://deno.land/x/lume@v3.2.5/"},"tasks":{"lume":"echo \"import 'lume/cli.ts'\" | deno run -A -","build":"deno task lume"}}`,
		"_config.ts": "import lume from \"lume/mod.ts\";\nconst site = lume({ dest: \"./public\" });\nexport default site;\n", "index.vto": "",
	})
	if lume.Recipe != "deno" || lume.Framework != "lume" || lume.Profile != ProfileStatic || lume.Port != 80 || lume.StartCommand != "" ||
		lume.BuildCommand != "deno task build" || lume.OutputDirectory != "public" || len(lume.NeedsDecision) != 0 || lume.Confidence != ConfidenceHigh {
		t.Fatalf("lume = %#v", lume)
	}
	_, vuepress := siteCandidate(t, map[string]string{
		"package.json":             `{"scripts":{"docs:build":"vuepress build docs"},"devDependencies":{"vuepress":"2.0.0-rc.31"}}`,
		"package-lock.json":        "{}",
		"docs/.vuepress/config.js": "export default defineUserConfig({ base: '/repo/', title: 'x' })",
	})
	if vuepress.Framework != "vuepress" || vuepress.OutputDirectory != "docs/.vuepress/dist" || vuepress.BuildCommand != "npm run docs:build" ||
		vuepress.StaticSite == nil || vuepress.StaticSite.BasePath != "/repo" {
		t.Fatalf("vuepress = %#v", vuepress)
	}
	_, slidev := siteCandidate(t, map[string]string{"package.json": `{"dependencies":{"@slidev/cli":"53.0.0"}}`, "package-lock.json": "{}", "slides.md": ""})
	if slidev.Framework != "slidev" || !slidev.SPAFallback || slidev.OutputDirectory != "dist" || slidev.BuildCommand != "npx slidev build" {
		t.Fatalf("slidev = %#v", slidev)
	}
	_, eleventy := siteCandidate(t, map[string]string{
		"package.json": `{"devDependencies":{"@11ty/eleventy":"3.1.2"}}`, "package-lock.json": "{}",
		"eleventy.config.js": "export default function (c) { return { dir: { input: 'src', output: 'dist' } } }", "src/index.md": "",
	})
	if eleventy.OutputDirectory != "dist" || eleventy.BuildCommand != "npx @11ty/eleventy" || eleventy.Confidence == ConfidenceLow || len(eleventy.NeedsDecision) != 0 {
		t.Fatalf("eleventy = %#v", eleventy)
	}
	_, flag := siteCandidate(t, map[string]string{
		"package.json": `{"scripts":{"build":"eleventy --output=_public"},"devDependencies":{"@11ty/eleventy":"3.1.2"}}`, "package-lock.json": "{}",
	})
	if flag.OutputDirectory != "_public" {
		t.Fatalf("eleventy --output = %#v", flag)
	}
}

// A Flutter app's web/ folder is a template flutter build web fills in;
// served raw, it is a blank page that passes readiness.
func TestFlutterWebTemplateIsNeverServedRaw(t *testing.T) {
	t.Parallel()
	result := detectShapeFixture(t, map[string]string{
		"pubspec.yaml":   "name: app\ndependencies:\n  flutter:\n    sdk: flutter\n",
		"lib/main.dart":  "void main() {}\n",
		"web/index.html": "<!DOCTYPE html><html><body><script src=\"flutter_bootstrap.js\" async></script></body></html>",
	})
	if len(result.Candidates) != 1 || result.Candidates[0].Framework != "flutter" || result.Candidates[0].BuildMethod == BuildStatic ||
		!strings.Contains(result.Candidates[0].RecipeIssue, "flutter build web") {
		t.Fatalf("flutter = %#v", result.Candidates)
	}
	if setAsideKind(result, "static-files") == nil {
		t.Fatalf("web/ was not named: %#v", result.SetAside)
	}
}

func TestStaticSiteFactsAreRecorded(t *testing.T) {
	t.Parallel()
	_, docusaurus := siteCandidate(t, map[string]string{
		"package.json":         `{"scripts":{"build":"docusaurus build"},"dependencies":{"@docusaurus/core":"3"}}`,
		"package-lock.json":    "{}",
		"docusaurus.config.js": "module.exports = { title: 'x', url: 'https://me.github.io', baseUrl: '/my-project/' }",
	})
	if docusaurus.StaticSite == nil || docusaurus.StaticSite.BasePath != "/my-project" || docusaurus.StaticSite.BasePathSource != "docusaurus.config.js" {
		t.Fatalf("docusaurus = %#v", docusaurus.StaticSite)
	}
	_, computed := siteCandidate(t, map[string]string{
		"package.json": `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"7"}}`, "package-lock.json": "{}",
		"vite.config.js": "export default { base: process.env.BASE_URL || '/' }",
	})
	if computed.StaticSite == nil || !computed.StaticSite.BasePathExpression || computed.StaticSite.BasePath != "" {
		t.Fatalf("computed base = %#v", computed.StaticSite)
	}
	_, svelte := siteCandidate(t, map[string]string{
		"package.json":      `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"7","@sveltejs/kit":"2","@sveltejs/adapter-static":"3"}}`,
		"package-lock.json": "{}", "svelte.config.js": "import adapter from '@sveltejs/adapter-static'; export default { kit: { adapter: adapter({ fallback: '200.html' }) } }",
	})
	if !svelte.SPAFallback || !evidenceMentions(svelte, "200.html") {
		t.Fatalf("sveltekit fallback = %#v", svelte)
	}
	_, html := siteCandidate(t, map[string]string{
		"index.html": "<h1>x</h1>", "_redirects": "/old /new 301\n/post/:id /p/:id 301\n", "_headers": "/*\n  X-Frame-Options: DENY\n",
	})
	if html.StaticSite == nil || html.StaticSite.HostingRules != 2 || html.StaticSite.HostingRulesLeftOut != 1 || !evidenceMentions(html, "applied by the static server") {
		t.Fatalf("html = %#v", html)
	}
}

// A directory a generator's configuration names becomes part of a COPY line
// and a build command; anything but a plain path is the default instead.
// The live static-rules fixture, read by detection: its overlapping rules
// count once each, and what cannot be applied is counted as left out.
func TestOverlappingHostingRulesAreCountedOnce(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	copyFrameworkFixture(t, "static-rules", root)
	result, err := (Detector{}).DetectPath(t.Context(), root, SourceIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	site := selectedOf(result)
	if site == nil || site.BuildMethod != BuildStatic || !site.SPAFallback || site.StaticSite == nil ||
		site.StaticSite.HostingRules != 7 || site.StaticSite.HostingRulesLeftOut != 2 || !evidenceMentions(site, "every path to /app.html") {
		t.Fatalf("site = %#v", result.Candidates)
	}
}

func TestSiteDirectoriesAreOnlyPlainPaths(t *testing.T) {
	t.Parallel()
	_, pelican := siteCandidate(t, map[string]string{"pelicanconf.py": "PATH = 'posts; touch /x'\nOUTPUT_PATH = '$(id)'\n", "content/a.md": ""})
	if pelican.BuildCommand != "pelican content -o output -s pelicanconf.py" || pelican.OutputDirectory != "output" {
		t.Fatalf("pelican = %#v", pelican)
	}
	_, hugo := siteCandidate(t, map[string]string{"hugo.toml": "baseURL = '/'\npublishDir = '../outside'\n", "content/a.md": ""})
	if hugo.OutputDirectory != "public" {
		t.Fatalf("hugo = %#v", hugo)
	}
	for value, want := range map[string]string{"public": "public", "./dist/": "dist", "a b": "x", "../up": "x", "/abs": "x", "a;b": "x", "docs/_build/html": "docs/_build/html"} {
		if got := siteOutput(value, "x"); got != want {
			t.Fatalf("siteOutput(%q) = %q, want %q", value, got, want)
		}
	}
}
