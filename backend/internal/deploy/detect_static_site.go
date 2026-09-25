package deploy

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// A static site says more about how it is served than its output
// directory: the sub-path a GitHub Pages project site was built for, the page
// SvelteKit falls back to, the redirects and headers another host's files
// declare, and — for a site generator — the release it was written for. Each
// fact is read as bounded data, recorded on the candidate for preflight, and
// read again from the commit when the build is prepared, so the image follows
// the source rather than the detection that proposed it.

// DetectedStaticSite is what a static site's own files say about how it is
// built and served.
type DetectedStaticSite struct {
	// Generator is the site generator the recipe builds with (hugo, zola,
	// mdbook, jekyll, mkdocs, sphinx, pelican, zensical, lume); empty for a
	// framework's static output.
	Generator string `json:"generator,omitempty"`
	// Version is the generator release the recipe builds with, and Declared
	// what the repository asks for with the file that asks ("0.119.0
	// (netlify.toml)"); Unpinned says nothing asks. VersionIssue says why
	// the declared release is not the one that builds.
	Version      string `json:"version,omitempty"`
	Declared     string `json:"declared,omitempty"`
	Unpinned     bool   `json:"unpinned,omitempty"`
	VersionIssue string `json:"versionIssue,omitempty"`
	// BasePath is the sub-path the site was built for ("/docs"), and
	// BasePathSource the file that says so; BasePathExpression says the
	// file computes it, so the site is served at the root.
	BasePath           string `json:"basePath,omitempty"`
	BasePathSource     string `json:"basePathSource,omitempty"`
	BasePathExpression bool   `json:"basePathExpression,omitempty"`
	// ThemeSubmodule is the Git submodule the configured theme lives in,
	// which the build cannot do without.
	ThemeSubmodule string `json:"themeSubmodule,omitempty"`
	// HostingRules counts the redirects, rewrites and headers of another
	// host's files the server applies; HostingRulesLeftOut those it cannot.
	HostingRules        int `json:"hostingRules,omitempty"`
	HostingRulesLeftOut int `json:"hostingRulesLeftOut,omitempty"`
}

// siteGeneratorNames is the closed set of DetectedStaticSite.Generator.
var siteGeneratorNames = map[string]string{
	"hugo": "Hugo", "zola": "Zola", "mdbook": "mdBook", "jekyll": "Jekyll", "mkdocs": "MkDocs",
	"zensical": "Zensical", "sphinx": "Sphinx", "pelican": "Pelican", "lume": "Lume",
}

func validateDetectedStaticSite(candidate DetectedCandidate) error {
	site := candidate.StaticSite
	if site == nil {
		return nil
	}
	text := func(value string, limit int) bool {
		return len(value) <= limit && !strings.ContainsAny(value, "\x00\r\n") &&
			rejectPlanSecretLiteral("static site evidence", value) == nil
	}
	if (site.Generator != "" && siteGeneratorNames[site.Generator] == "") || !text(site.Version, 32) ||
		!text(site.Declared, 256) || !text(site.VersionIssue, 512) || !text(site.BasePathSource, 4096) ||
		(site.BasePath != "" && cleanStaticBasePath(site.BasePath) != site.BasePath) ||
		(site.ThemeSubmodule != "" && !safeRelativePath(site.ThemeSubmodule)) ||
		site.HostingRules < 0 || site.HostingRulesLeftOut < 0 {
		return fmt.Errorf("%w: static site evidence is malformed", ErrInvalidPlan)
	}
	return nil
}

func (c *DetectedCandidate) staticSite() *DetectedStaticSite {
	if c.StaticSite == nil {
		c.StaticSite = &DetectedStaticSite{}
	}
	return c.StaticSite
}

var (
	svelteKitFallbackRE = regexp.MustCompile(`\bfallback\s*:\s*['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`)
	angularBaseHrefRE   = regexp.MustCompile(`"baseHref"\s*:\s*"(/[^"]*)"`)
	scriptBaseFlagRE    = regexp.MustCompile(`--base(?:-href)?[= ]['"]?(/[^'"\s]*)`)
	jsConfigExtensions  = []string{".ts", ".mts", ".js", ".mjs", ".cjs"}
)

// staticBasePath reads the sub-path a framework's static output was built
// for: the literal, the file it came from, and whether the file computes it
// instead. output is the output directory, from which VitePress and VuePress
// place their configuration. A base is read only where it is a literal key
// of the exported configuration (`base: '/repo/'`); a value computed in the
// file (`base: process.env.BASE`) is named as evidence and the site is
// served at the root, since what it evaluates to on this server is not
// something reading can know.
func staticBasePath(read siteFiles, framework, output string, manifest nodeManifest) (string, string, bool) {
	literal := func(names []string, keys ...string) (string, string, bool) {
		for _, name := range names {
			content, ok := read(name)
			if !ok {
				continue
			}
			value := readJSConfig(content).value(keys...)
			switch {
			case value.expression:
				return "", name, true
			case value.found:
				return cleanStaticBasePath(value.literal), name, false
			}
			return "", "", false
		}
		return "", "", false
	}
	configs := func(stem string) []string {
		names := make([]string, 0, len(jsConfigExtensions))
		for _, extension := range jsConfigExtensions {
			names = append(names, stem+extension)
		}
		return names
	}
	build := manifest.Scripts["build"]
	switch framework {
	case "docusaurus":
		return literal(configs("docusaurus.config"), "baseUrl")
	case "vite", "vue-cli", "astro":
		if match := scriptBaseFlagRE.FindStringSubmatch(build); match != nil {
			return cleanStaticBasePath(match[1]), "package.json", false
		}
		if framework == "vue-cli" {
			return literal([]string{"vue.config.js", "vue.config.cjs", "vue.config.mjs", "vue.config.ts"}, "publicPath")
		}
		stem := "vite.config"
		if framework == "astro" {
			stem = "astro.config"
		}
		return literal(configs(stem), "base")
	case "vitepress", "vuepress":
		// Both keep their configuration beside the dist they write:
		// <docs>/.vitepress/config.ts and <docs>/.vuepress/config.js.
		if !strings.HasSuffix(output, "/dist") {
			return "", "", false
		}
		return literal(configs(path.Join(path.Dir(output), "config")), "base")
	case "sveltekit":
		return literal(configs("svelte.config"), "kit", "paths", "base")
	case "angular":
		if match := scriptBaseFlagRE.FindStringSubmatch(build); match != nil {
			return cleanStaticBasePath(match[1]), "package.json", false
		}
		if content, ok := read("angular.json"); ok {
			if match := angularBaseHrefRE.FindSubmatch(content); match != nil {
				return cleanStaticBasePath(string(match[1])), "angular.json", false
			}
		}
	case "create-react-app":
		// Create React App builds its asset URLs from homepage's path.
		if content, ok := read("package.json"); ok {
			var document struct {
				Homepage string `json:"homepage"`
			}
			if json.Unmarshal(content, &document) == nil && document.Homepage != "" {
				homepage := document.Homepage
				if _, rest, found := strings.Cut(homepage, "://"); found {
					_, route, _ := strings.Cut(rest, "/")
					homepage = "/" + route
				}
				if strings.HasPrefix(homepage, "/") {
					return cleanStaticBasePath(homepage), "package.json", false
				}
			}
		}
	case "gatsby":
		// pathPrefix applies only to a build run with --prefix-paths.
		if strings.Contains(build, "--prefix-paths") {
			return literal(configs("gatsby-config"), "pathPrefix")
		}
	case "nuxt":
		return literal(configs("nuxt.config"), "app", "baseURL")
	case "nextjs":
		// A static export (output: 'export', served from out/) keeps its
		// basePath like any other build.
		return literal(configs("next.config"), "basePath")
	case "slidev":
		if match := scriptBaseFlagRE.FindStringSubmatch(build); match != nil {
			return cleanStaticBasePath(match[1]), "package.json", false
		}
	}
	return "", "", false
}

// svelteKitFallbackPage is the page adapter-static's fallback option names
// in a svelte.config's text with its comments blanked: the page it writes
// for the paths it did not prerender, which nginx falls back to. A value
// that is not a plain page of the output names none.
func svelteKitFallbackPage(text string) string {
	if match := svelteKitFallbackRE.FindStringSubmatch(text); match != nil && staticFallbackRE.MatchString(match[1]) {
		return match[1]
	}
	return ""
}

var (
	eleventyOutputRE       = regexp.MustCompile(`\boutput\s*:\s*['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`)
	eleventyDirRE          = regexp.MustCompile(`\bdir\s*:\s*\{([^}]*)\}`)
	eleventyOutputScriptRE = regexp.MustCompile(`--output[= ]['"]?([^'"\s]+)`)
	hexoPublicDirRE        = regexp.MustCompile(`(?m)^public_dir\s*:\s*['"]?([^'"\s#]+)`)
)

// eleventyConfigs are the configuration files Eleventy reads, in its order.
var eleventyConfigs = []string{
	".eleventy.js", "eleventy.config.js", "eleventy.config.mjs", "eleventy.config.cjs", "eleventy.config.ts", "eleventy.config.mts",
}

// eleventyOutput is where Eleventy writes the site: the build script's
// --output, else the dir.output literal of its configuration, else _site.
func eleventyOutput(read siteFiles, manifest nodeManifest) (string, string) {
	for _, script := range []string{"build", "build:site", "build:eleventy"} {
		if match := eleventyOutputScriptRE.FindStringSubmatch(manifest.Scripts[script]); match != nil {
			if output := siteOutput(match[1], ""); output != "" {
				return output, "package.json"
			}
		}
	}
	for _, name := range eleventyConfigs {
		content, ok := read(name)
		if !ok {
			continue
		}
		if dir := eleventyDirRE.FindSubmatch(jsBlankComments(content)); dir != nil {
			if match := eleventyOutputRE.FindSubmatch(dir[1]); match != nil {
				if output := siteOutput(string(match[1]), ""); output != "" {
					return output, name
				}
			}
		}
		break
	}
	return "_site", ""
}

// hexoPublicDir is where hexo generate writes: _config.yml's public_dir.
func hexoPublicDir(read siteFiles) string {
	if content, ok := read("_config.yml"); ok {
		if match := hexoPublicDirRE.FindSubmatch(content); match != nil {
			return siteOutput(string(match[1]), "public")
		}
	}
	return "public"
}

// netlifyNodeVersion is the NODE_VERSION a netlify.toml build environment
// sets, the Node release the site was built with there.
func netlifyNodeVersion(content []byte) string {
	entries := readTOML(content)
	for _, table := range []string{"build.environment", "context.production.environment"} {
		if spec := boundedSpec(tomlText(entries, table, "NODE_VERSION")); spec != "" {
			return spec
		}
	}
	return ""
}
