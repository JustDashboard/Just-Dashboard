package deploy

import (
	"encoding/json"
	"path"
	"sort"
	"strings"
)

// nodeManifest is the inert view of package.json that detection and the
// recipe read: names, version strings and script text. Nothing in it is ever
// evaluated on the dashboard host.
type nodeManifest struct {
	Name            string            `json:"name"`
	Main            string            `json:"main"`
	Module          string            `json:"module"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func parseNodeManifest(content []byte, manifest *nodeManifest) bool {
	return json.Unmarshal(manifestText(content), manifest) == nil
}

func (m nodeManifest) has(name string) bool {
	return m.Dependencies[name] != "" || m.DevDependencies[name] != ""
}

func (m nodeManifest) version(name string) string {
	if version := m.Dependencies[name]; version != "" {
		return version
	}
	return m.DevDependencies[name]
}

// nodeRootFiles are the files beside package.json a framework's defaults can
// depend on. Both are read as data: angular.json is JSON, a Procfile is lines.
type nodeRootFiles struct {
	angularJSON []byte
	procfile    []byte
}

// nodeFrameworkDefaults is what a build of a recognised framework produces on
// this server: a site in Output served by nginx, or a server started by Start
// that listens on Port. Env is what the runtime stage needs for that server
// to answer from inside a container, and Entry is the file the build must
// have written, checked before the image is accepted so a wrong output path
// fails the build with its name instead of failing the readiness gate.
type nodeFrameworkDefaults struct {
	Output string
	SPA    bool
	Start  string
	Port   int
	Env    []string
	Entry  string
	// StartScripts names the package scripts that serve a production build,
	// in order of preference; a present one wins over Start.
	StartScripts []string
	// BuildScript is the script that builds when it is not "build".
	BuildScript string
	// DefaultBuild is the framework's own build binary and arguments, run
	// through the manager's runner when the package has no build script:
	// `npx @11ty/eleventy` is how Eleventy's documentation builds a site.
	DefaultBuild string
}

type nodeFrameworkResolution struct {
	nodeFrameworkDefaults
	// Confidence is the detection confidence for this match; empty is high.
	Confidence DetectionConfidence
	Decisions  []string
}

// nodeFramework is one catalogue entry: how a manifest names the framework
// and how its production build is served. The catalogue is ordered, and the
// first match wins, so a meta-framework built on Vite (Remix, SvelteKit,
// SolidStart) is recognised before Vite itself — every one of them lists
// `vite` as a dependency, and reading that alone made a Remix server a
// static site.
type nodeFramework struct {
	Name         string
	Label        string
	Dependencies []string
	resolve      func(manifest nodeManifest, files nodeRootFiles, runner string) nodeFrameworkResolution
}

func static(output string, spa bool) nodeFrameworkResolution {
	return nodeFrameworkResolution{nodeFrameworkDefaults: nodeFrameworkDefaults{Output: output, SPA: spa}}
}

func server(start string, port int, entry string) nodeFrameworkResolution {
	return nodeFrameworkResolution{nodeFrameworkDefaults: nodeFrameworkDefaults{Start: start, Port: port, Entry: entry, StartScripts: []string{"start"}}}
}

// nitroServer is the Nitro output every Nitro-based framework (Nuxt,
// SolidStart, TanStack Start, Nitro itself) writes.
func nitroServer() nodeFrameworkResolution {
	return server("node .output/server/index.mjs", 3000, ".output/server/index.mjs")
}

var nodeFrameworks = []nodeFramework{
	{
		Name: "nextjs", Label: "Next.js", Dependencies: []string{"next"},
		resolve: func(_ nodeManifest, _ nodeRootFiles, runner string) nodeFrameworkResolution {
			return server(nodeExecRunner(runner)+" next start", 3000, "")
		},
	},
	{
		Name: "sveltekit", Label: "SvelteKit", Dependencies: []string{"@sveltejs/kit"},
		resolve: func(manifest nodeManifest, _ nodeRootFiles, runner string) nodeFrameworkResolution {
			switch {
			case manifest.has("@sveltejs/adapter-node") && !manifest.has("@sveltejs/adapter-static"):
				start := "node build"
				if runner == "bun" {
					start = "bun ./build/index.js"
				}
				resolution := server(start, 3000, "build/index.js")
				// SvelteKit's own `start` script is not a convention; the
				// adapter's entrypoint is what serves a production build.
				resolution.StartScripts = nil
				return resolution
			case manifest.has("@sveltejs/adapter-static") && !manifest.has("@sveltejs/adapter-node"):
				return static("build", false)
			}
			resolution := static("", false)
			resolution.Confidence = ConfidenceLow
			resolution.Decisions = []string{"select adapter-node or adapter-static for this server; adapter-auto and provider adapters require a Dockerfile"}
			return resolution
		},
	},
	{
		Name: "astro", Label: "Astro", Dependencies: []string{"astro"},
		resolve: func(manifest nodeManifest, _ nodeRootFiles, _ string) nodeFrameworkResolution {
			if manifest.has("@astrojs/node") {
				resolution := server("node ./dist/server/entry.mjs", 4321, "dist/server/entry.mjs")
				// The standalone Node adapter binds to localhost unless told
				// otherwise, which inside a container is a server nobody can
				// reach.
				resolution.Env = []string{"HOST=0.0.0.0"}
				return resolution
			}
			for _, adapter := range []string{"@astrojs/vercel", "@astrojs/netlify", "@astrojs/cloudflare", "@astrojs/deno"} {
				if manifest.has(adapter) {
					resolution := static("dist", false)
					resolution.Confidence = ConfidenceLow
					resolution.Decisions = []string{"Astro is configured for " + strings.TrimPrefix(adapter, "@astrojs/") + "; use @astrojs/node on this server, or a static build"}
					return resolution
				}
			}
			return static("dist", false)
		},
	},
	{
		Name: "nuxt", Label: "Nuxt", Dependencies: []string{"nuxt"},
		resolve: func(manifest nodeManifest, _ nodeRootFiles, runner string) nodeFrameworkResolution {
			build := manifest.Scripts["build"]
			if strings.Contains(build, "nuxt generate") || strings.Contains(build, "nuxi generate") {
				return static(".output/public", false)
			}
			if major := semverMajor(manifest.version("nuxt")); major == "2" {
				// Nuxt 2 serves from .nuxt through its own binary; there is
				// no standalone server entry to point node at.
				resolution := server(nodeExecRunner(runner)+" nuxt start", 3000, "")
				resolution.Confidence = ConfidenceMedium
				return resolution
			}
			return nitroServer()
		},
	},
	{
		Name: "remix", Label: "Remix", Dependencies: []string{"@remix-run/dev"},
		resolve: func(manifest nodeManifest, _ nodeRootFiles, runner string) nodeFrameworkResolution {
			if manifest.has("@remix-run/serve") {
				return server(nodeExecRunner(runner)+" remix-serve ./build/server/index.js", 3000, "build/server/index.js")
			}
			resolution := server("", 3000, "")
			resolution.Confidence = ConfidenceMedium
			resolution.Decisions = []string{"Remix has no @remix-run/serve; confirm the start command of its custom server"}
			return resolution
		},
	},
	{
		Name: "react-router", Label: "React Router", Dependencies: []string{"@react-router/dev"},
		resolve: func(manifest nodeManifest, _ nodeRootFiles, runner string) nodeFrameworkResolution {
			if manifest.has("@react-router/serve") {
				return server(nodeExecRunner(runner)+" react-router-serve ./build/server/index.js", 3000, "build/server/index.js")
			}
			resolution := server("", 3000, "")
			resolution.Confidence = ConfidenceMedium
			resolution.Decisions = []string{"React Router has no @react-router/serve; confirm the start command of its custom server"}
			return resolution
		},
	},
	{
		Name: "solid-start", Label: "SolidStart", Dependencies: []string{"@solidjs/start"},
		resolve: func(nodeManifest, nodeRootFiles, string) nodeFrameworkResolution { return nitroServer() },
	},
	{
		Name: "tanstack-start", Label: "TanStack Start", Dependencies: []string{"@tanstack/react-start", "@tanstack/solid-start", "@tanstack/start"},
		resolve: func(nodeManifest, nodeRootFiles, string) nodeFrameworkResolution {
			resolution := nitroServer()
			resolution.Confidence = ConfidenceMedium
			resolution.Decisions = []string{"confirm the server entry TanStack Start writes for this version"}
			return resolution
		},
	},
	{
		Name: "nitro", Label: "Nitro", Dependencies: []string{"nitropack", "nitro"},
		resolve: func(nodeManifest, nodeRootFiles, string) nodeFrameworkResolution { return nitroServer() },
	},
	{
		Name: "angular", Label: "Angular", Dependencies: []string{"@angular/core"},
		resolve: func(manifest nodeManifest, files nodeRootFiles, _ string) nodeFrameworkResolution {
			name, output, ok := angularOutput(files.angularJSON)
			if manifest.has("@angular/ssr") {
				if !ok {
					resolution := server("", 4000, "")
					resolution.Confidence = ConfidenceMedium
					resolution.Decisions = []string{"angular.json names no application project; confirm the server start command"}
					return resolution
				}
				entry := path.Join(path.Dir(output), "server", "server.mjs")
				if name != "" && path.Base(output) != "browser" {
					entry = path.Join(output, "server", "server.mjs")
				}
				return server("node "+entry, 4000, entry)
			}
			if !ok {
				resolution := static("dist", true)
				resolution.Confidence = ConfidenceMedium
				resolution.Decisions = []string{"angular.json names no application project; confirm the output directory"}
				return resolution
			}
			return static(output, true)
		},
	},
	{
		Name: "nestjs", Label: "NestJS", Dependencies: []string{"@nestjs/core"},
		resolve: func(nodeManifest, nodeRootFiles, string) nodeFrameworkResolution {
			resolution := server("node dist/main", 3000, "dist/main.js")
			resolution.StartScripts = []string{"start:prod"}
			return resolution
		},
	},
	{
		Name: "gatsby", Label: "Gatsby", Dependencies: []string{"gatsby"},
		resolve: func(nodeManifest, nodeRootFiles, string) nodeFrameworkResolution { return static("public", false) },
	},
	{
		Name: "docusaurus", Label: "Docusaurus", Dependencies: []string{"@docusaurus/core"},
		resolve: func(nodeManifest, nodeRootFiles, string) nodeFrameworkResolution { return static("build", false) },
	},
	{
		Name: "vitepress", Label: "VitePress", Dependencies: []string{"vitepress"},
		resolve: func(manifest nodeManifest, _ nodeRootFiles, _ string) nodeFrameworkResolution {
			script, directory := vitepressBuild(manifest.Scripts)
			resolution := static(path.Join(directory, ".vitepress", "dist"), false)
			if script != "build" {
				resolution.BuildScript = script
			}
			return resolution
		},
	},
	{
		Name: "eleventy", Label: "Eleventy", Dependencies: []string{"@11ty/eleventy"},
		resolve: func(manifest nodeManifest, _ nodeRootFiles, _ string) nodeFrameworkResolution {
			// The build script's --output wins over the configuration file's,
			// which the shape pass reads (detect_site_generators.go).
			output, _ := eleventyOutput(func(string) ([]byte, bool) { return nil, false }, manifest)
			resolution := static(output, false)
			resolution.DefaultBuild = "@11ty/eleventy"
			return resolution
		},
	},
	{
		Name: "hexo", Label: "Hexo", Dependencies: []string{"hexo"},
		resolve: func(nodeManifest, nodeRootFiles, string) nodeFrameworkResolution {
			resolution := static("public", false)
			resolution.DefaultBuild = "hexo generate"
			return resolution
		},
	},
	{
		Name: "vuepress", Label: "VuePress", Dependencies: []string{"vuepress", "vuepress-vite", "vuepress-webpack", "@vuepress/cli"},
		resolve: func(manifest nodeManifest, _ nodeRootFiles, _ string) nodeFrameworkResolution {
			script, directory := siteScriptDirectory(manifest.Scripts, "vuepress build", "vuepress-vite build", "vuepress-webpack build")
			resolution := static(path.Join(directory, ".vuepress", "dist"), false)
			if script != "build" {
				resolution.BuildScript = script
			}
			return resolution
		},
	},
	{
		// Slidev's slides are routes of one page: /2 is the second slide.
		Name: "slidev", Label: "Slidev", Dependencies: []string{"@slidev/cli"},
		resolve: func(nodeManifest, nodeRootFiles, string) nodeFrameworkResolution {
			resolution := static("dist", true)
			resolution.DefaultBuild = "slidev build"
			return resolution
		},
	},
	{
		Name: "create-react-app", Label: "Create React App", Dependencies: []string{"react-scripts"},
		resolve: func(nodeManifest, nodeRootFiles, string) nodeFrameworkResolution { return static("build", true) },
	},
	{
		Name: "vue-cli", Label: "Vue CLI", Dependencies: []string{"@vue/cli-service"},
		resolve: func(nodeManifest, nodeRootFiles, string) nodeFrameworkResolution { return static("dist", true) },
	},
	{
		Name: "ember", Label: "Ember", Dependencies: []string{"ember-cli"},
		resolve: func(nodeManifest, nodeRootFiles, string) nodeFrameworkResolution { return static("dist", true) },
	},
	{
		Name: "parcel", Label: "Parcel", Dependencies: []string{"parcel"},
		resolve: func(nodeManifest, nodeRootFiles, string) nodeFrameworkResolution { return static("dist", true) },
	},
	{
		Name: "vite", Label: "Vite", Dependencies: []string{"vite"},
		resolve: func(nodeManifest, nodeRootFiles, string) nodeFrameworkResolution { return static("dist", true) },
	},
}

// nodeServerLibraries are the HTTP frameworks a plain Node service is built
// on. They name the workload for the operator; the package's own start
// script or main file says how it runs.
var nodeServerLibraries = []struct{ dependency, name string }{
	{"@hapi/hapi", "hapi"}, {"express", "express"}, {"fastify", "fastify"},
	{"hono", "hono"}, {"koa", "koa"}, {"elysia", "elysia"},
}

func matchNodeFramework(manifest nodeManifest) *nodeFramework {
	for index := range nodeFrameworks {
		for _, dependency := range nodeFrameworks[index].Dependencies {
			if manifest.has(dependency) {
				return &nodeFrameworks[index]
			}
		}
	}
	return nil
}

func nodeFrameworkByName(name string) *nodeFramework {
	for index := range nodeFrameworks {
		if nodeFrameworks[index].Name == name {
			return &nodeFrameworks[index]
		}
	}
	return nil
}

func matchNodeServerLibrary(manifest nodeManifest) string {
	for _, library := range nodeServerLibraries {
		if manifest.has(library.dependency) {
			return library.name
		}
	}
	return ""
}

// frameworkDefaultStart reports whether the configured start command still
// ends in the framework's own entrypoint command, with or without a schema
// step chained in front of it. Only then does the recipe check that the
// build produced the entry file: a custom start command is the operator's
// own, and may serve from anywhere.
func frameworkDefaultStart(command, start string) bool {
	if start == "" {
		return false
	}
	segments := strings.Split(command, "&&")
	return strings.TrimSpace(segments[len(segments)-1]) == start
}

// semverMajor reads the major version out of a dependency range such as
// "^2.15.0", "~3", "3.x" or ">=2.0.0".
func semverMajor(version string) string {
	version = strings.TrimLeft(strings.TrimSpace(version), "^~>=<v ")
	major, _, _ := strings.Cut(version, ".")
	for _, r := range major {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return major
}

// vitepressBuild finds the script that runs `vitepress build` and the docs
// directory it names, since VitePress writes its site under that directory
// rather than under the package root.
func vitepressBuild(scripts map[string]string) (string, string) {
	names := make([]string, 0, len(scripts))
	for name := range scripts {
		names = append(names, name)
	}
	sort.Strings(names)
	// "build" first when it is the one; then the conventional "docs:build".
	sort.SliceStable(names, func(i, j int) bool {
		rank := func(name string) int {
			switch name {
			case "build":
				return 0
			case "docs:build":
				return 1
			}
			return 2
		}
		return rank(names[i]) < rank(names[j])
	})
	for _, name := range names {
		_, after, found := strings.Cut(scripts[name], "vitepress build")
		if !found {
			continue
		}
		directory := "."
		for _, field := range strings.Fields(after) {
			if strings.HasPrefix(field, "-") {
				continue
			}
			if field == "&&" || field == "||" || field == ";" {
				break
			}
			if safeRelativePath(field) {
				directory = field
			}
			break
		}
		return name, directory
	}
	return "build", "."
}

// angularOutput reads the application project's build output out of
// angular.json: the workspace default project when it is an application,
// otherwise the first application in key order. The application builder
// (`@angular/build:application`, `@angular-devkit/build-angular:application`)
// writes browser files under a `browser` subdirectory of the output path;
// the older browser builders write them at the output path itself.
func angularOutput(content []byte) (string, string, bool) {
	var workspace struct {
		DefaultProject string `json:"defaultProject"`
		Projects       map[string]struct {
			ProjectType string `json:"projectType"`
			Architect   map[string]struct {
				Builder string          `json:"builder"`
				Options json.RawMessage `json:"options"`
			} `json:"architect"`
			Targets map[string]struct {
				Builder string          `json:"builder"`
				Options json.RawMessage `json:"options"`
			} `json:"targets"`
		} `json:"projects"`
	}
	if len(content) == 0 || json.Unmarshal(content, &workspace) != nil {
		return "", "", false
	}
	names := make([]string, 0, len(workspace.Projects))
	for name, project := range workspace.Projects {
		if project.ProjectType == "application" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "", "", false
	}
	sort.Strings(names)
	name := names[0]
	if _, ok := workspace.Projects[workspace.DefaultProject]; ok && workspace.Projects[workspace.DefaultProject].ProjectType == "application" {
		name = workspace.DefaultProject
	}
	project := workspace.Projects[name]
	targets := project.Architect
	if len(targets) == 0 {
		targets = project.Targets
	}
	build, ok := targets["build"]
	if !ok {
		return "", "", false
	}
	base := path.Join("dist", name)
	browser := "browser"
	var options struct {
		OutputPath json.RawMessage `json:"outputPath"`
	}
	if len(build.Options) > 0 && json.Unmarshal(build.Options, &options) == nil && len(options.OutputPath) > 0 {
		var literal string
		var structured struct {
			Base    string `json:"base"`
			Browser string `json:"browser"`
		}
		switch {
		case json.Unmarshal(options.OutputPath, &literal) == nil && literal != "":
			base = literal
		case json.Unmarshal(options.OutputPath, &structured) == nil && structured.Base != "":
			base = structured.Base
			if structured.Browser != "" {
				browser = structured.Browser
			}
		}
	}
	base = strings.TrimSuffix(strings.TrimSpace(base), "/")
	if !safeRelativePath(base) {
		return "", "", false
	}
	if strings.HasSuffix(build.Builder, ":application") {
		if browser == "" {
			return name, base, true
		}
		return name, path.Join(base, browser), true
	}
	return name, base, true
}

// procfileProcess reads the command a Heroku-style Procfile declares for a
// process type. The file is one `name: command` per line; comments and blank
// lines are skipped.
func procfileProcess(content []byte, process string) string {
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, command, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(name) != process {
			continue
		}
		return strings.TrimSpace(command)
	}
	return ""
}

// siteScriptDirectory finds the script that runs one of a generator's build
// commands and the source directory it names, since VuePress, like
// VitePress, writes its site under that directory.
func siteScriptDirectory(scripts map[string]string, commands ...string) (string, string) {
	names := make([]string, 0, len(scripts))
	for name := range scripts {
		names = append(names, name)
	}
	sort.Strings(names)
	sort.SliceStable(names, func(i, j int) bool {
		return (names[i] == "build" || names[i] == "docs:build") && names[j] != "build" && names[j] != "docs:build"
	})
	for _, name := range names {
		for _, command := range commands {
			_, after, found := strings.Cut(scripts[name], command)
			if !found {
				continue
			}
			for _, field := range strings.Fields(after) {
				if strings.HasPrefix(field, "-") {
					continue
				}
				if field != "&&" && field != "||" && field != ";" && safeRelativePath(field) {
					return name, path.Clean(field)
				}
				break
			}
			return name, "."
		}
	}
	return "build", "."
}
