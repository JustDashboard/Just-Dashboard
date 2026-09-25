package deploy

import (
	"encoding/json"
	"path"
	"regexp"
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
	// in order of preference; a present one wins over Start unless it
	// starts a development server.
	StartScripts []string
	// BuildScript is the script that builds when it is not "build", and
	// Build the command that builds when the package has no such script.
	BuildScript string
	Build       string
	// Fallback is the page a single-page site answers unknown paths with
	// when it is not index.html (SvelteKit's 200.html); Base is the path the
	// site is served under ("/docs"); CleanURLs serves /about from
	// about.html, which Next.js export and SvelteKit's static adapter write.
	Fallback  string
	Base      string
	CleanURLs bool
	// BuildEnv are values the build command's RUN gives a variable the
	// build does not set (NITRO_PRESET), and BeforeBuild the steps that run
	// between the install and the build (adapter-node for adapter-auto).
	BuildEnv    []nodeEnvDefault
	BeforeBuild []string
}

type nodeFrameworkResolution struct {
	nodeFrameworkDefaults
	// Confidence is the detection confidence for this match; empty is high.
	Confidence DetectionConfidence
	Decisions  []string
	// Notes are what the framework's configuration decided, as evidence
	// with the file (relative to the package) that said it; Findings are
	// what preflight says about it before Deploy.
	Notes    []nodeFrameworkNote
	Findings []PreflightFinding
}

type nodeFrameworkNote struct{ file, reason string }

func (r *nodeFrameworkResolution) note(file, reason string) {
	r.Notes = append(r.Notes, nodeFrameworkNote{file: file, reason: reason})
}

func (r *nodeFrameworkResolution) decide(confidence DetectionConfidence, decision string) {
	if confidenceRank(confidence) < confidenceRank(r.Confidence) || r.Confidence == "" {
		r.Confidence = confidence
	}
	r.Decisions = append(r.Decisions, decision)
}

// nodeFramework is one catalogue entry: how a manifest names the framework
// and how its production build is served. The catalogue is ordered, and the
// first match wins, so a meta-framework built on Vite (Remix, SvelteKit,
// SolidStart) is recognised before Vite itself — every one of them lists
// `vite` as a dependency, and reading that alone made a Remix server a
// static site. when narrows a match beyond the dependency.
type nodeFramework struct {
	Name         string
	Label        string
	Dependencies []string
	when         func(nodeManifest) bool
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

// nitroPresetServer is a Nitro server whose configuration names a preset:
// Nitro writes no Node server for a provider's preset (vercel, netlify,
// cloudflare), so the build runs with NITRO_PRESET=node-server, which
// outranks the configuration file's preset.
func nitroPresetServer(config nodeConfigText, label string) nodeFrameworkResolution {
	match := nitroPresetRE.FindStringSubmatch(config.text)
	if match == nil {
		return nitroServer()
	}
	switch preset := match[1]; preset {
	case "node-server", "node", "node_server", "node-cluster", "node_cluster":
		return nitroServer()
	case "static", "github-pages", "github_pages":
		resolution := static(".output/public", false)
		resolution.note(config.name, "nitro preset "+preset+": a static site in .output/public")
		return resolution
	default:
		resolution := nitroServer()
		resolution.BuildEnv = []nodeEnvDefault{{"NITRO_PRESET", "node-server"}}
		resolution.note(config.name, "nitro preset "+preset+" writes no Node server; the build runs with NITRO_PRESET=node-server")
		resolution.Findings = append(resolution.Findings, nodeFinding("nitro_preset_overridden", PreflightWarning,
			label+"'s deployment preset is replaced for this server", config.name+": preset "+preset,
			"The "+preset+" preset writes the application for that provider's platform, with no server a container can run; the build sets NITRO_PRESET=node-server, which outranks the configuration, and serves .output/server/index.mjs.",
			"Remove the preset from "+config.name+", or set it only in that provider's build environment, so the repository builds the same way everywhere.", "configuration.build"))
		return resolution
	}
}

// svelteKitAdapterNode is the adapter-node release a SvelteKit major is
// built with when its configuration names another adapter, pinned like a
// base image: adapter-node 5 needs Kit 2.4 or later.
func svelteKitAdapterNode(kit string) string {
	switch semverMajor(kit) {
	case "1":
		return "1.3.1"
	case "2":
		if interval, ok := nodeRangeInterval(strings.TrimSpace(strings.Split(kit, "||")[0])); ok && interval.low.set &&
			interval.low.v.less(nodeVersion{2, 4, 0}) {
			return "3.0.3"
		}
		return "5.5.7"
	}
	return ""
}

// nodeAddCommand installs one exact package into the build stage without
// the install's lockfile check: the lockfile is changed only inside the
// image, never in the repository.
func nodeAddCommand(runner, spec string) string {
	switch runner {
	case "pnpm":
		return "pnpm add -D " + spec
	case "yarn":
		return "yarn add -D " + spec
	case "bun":
		return "bun add -d " + spec
	}
	return "npm install --no-save --no-audit --no-fund --legacy-peer-deps " + spec
}

// svelteKitAdapterWrapper replaces svelte.config.js with one that imports
// the repository's own and sets kit.adapter to adapter-node; the original
// keeps its preprocessors, aliases and every other option.
const svelteKitAdapterWrapper = `mv svelte.config.js svelte.config.user.js && printf '%s\n' ` +
	`"import config from './svelte.config.user.js';" ` +
	`"import adapter from '@sveltejs/adapter-node';" ` +
	`"export default { ...config, kit: { ...config.kit, adapter: adapter() } };" > svelte.config.js`

func sveltekitResolution(manifest nodeManifest, files nodeRootFiles, runner string) nodeFrameworkResolution {
	config, configured := files.configText("svelte")
	adapter := ""
	if configured {
		adapter = svelteAdapter(config.text)
	}
	node, staticAdapter := manifest.has("@sveltejs/adapter-node"), manifest.has("@sveltejs/adapter-static")
	if adapter == "" && node != staticAdapter {
		adapter = "@sveltejs/adapter-node"
		if staticAdapter {
			adapter = "@sveltejs/adapter-static"
		}
	}
	start := "node build"
	if runner == "bun" {
		start = "bun ./build/index.js"
	}
	switch adapter {
	case "@sveltejs/adapter-node":
		resolution := server(start, 3000, "build/index.js")
		// SvelteKit's own `start` script is not a convention; the
		// adapter's entrypoint is what serves a production build.
		resolution.StartScripts = nil
		return resolution
	case "@sveltejs/adapter-static":
		output := "build"
		fallback := ""
		if configured {
			if pages := literalRelativePath(sveltePagesRE, config.text); pages != "" {
				output = pages
			}
			if match := svelteFallbackRE.FindStringSubmatch(config.text); match != nil {
				fallback = match[1]
			}
		}
		resolution := static(output, fallback != "")
		resolution.CleanURLs = true
		if fallback != "" && fallback != "index.html" {
			resolution.Fallback = fallback
		}
		if fallback != "" && configured {
			resolution.note(config.name, "adapter-static fallback "+fallback+": a single-page site")
		}
		return resolution
	case "":
		if configured || (node && staticAdapter) {
			resolution := static("", false)
			resolution.decide(ConfidenceLow, "choose adapter-node or adapter-static in svelte.config.js; it names neither")
			return resolution
		}
	}
	version := svelteKitAdapterNode(manifest.version("@sveltejs/kit"))
	if version == "" || (configured && config.name != "svelte.config.js") {
		resolution := static("", false)
		resolution.decide(ConfidenceLow, "select adapter-node or adapter-static for this server; "+strings.TrimPrefix(orDefault(adapter, "no adapter"), "@sveltejs/")+" needs a repository change the recipe cannot make here")
		return resolution
	}
	resolution := server(start, 3000, "build/index.js")
	resolution.StartScripts = nil
	if !node {
		resolution.BeforeBuild = append(resolution.BeforeBuild, nodeAddCommand(runner, "@sveltejs/adapter-node@"+version))
	}
	if configured {
		resolution.BeforeBuild = append(resolution.BeforeBuild, svelteKitAdapterWrapper)
	} else {
		resolution.BeforeBuild = append(resolution.BeforeBuild, `printf '%s\n' "import adapter from '@sveltejs/adapter-node';" "export default { kit: { adapter: adapter() } };" > svelte.config.js`)
	}
	named := strings.TrimPrefix(orDefault(adapter, "no adapter"), "@sveltejs/")
	file := orDefault(config.name, "package.json")
	resolution.note(file, named+" builds for another platform; the build uses @sveltejs/adapter-node "+version+" and serves build/index.js")
	resolution.Findings = append(resolution.Findings, nodeFinding("sveltekit_adapter_substituted", PreflightWarning,
		"SvelteKit is built with adapter-node for this server", file+": "+named,
		named+" writes the application for a hosting provider, or nothing at all outside one; the build installs @sveltejs/adapter-node "+version+" inside the image and wraps svelte.config.js so kit.adapter is adapter(), then serves build/index.js. The repository is not changed.",
		"To build the same way everywhere, run `npm i -D @sveltejs/adapter-node` and import adapter from '@sveltejs/adapter-node' in svelte.config.js.", "configuration.build"))
	return resolution
}

func nextResolution(manifest nodeManifest, files nodeRootFiles, runner string) nodeFrameworkResolution {
	config, configured := files.configText("next")
	mode := ""
	if configured {
		mode = nextOutputMode(config.text)
	}
	var resolution nodeFrameworkResolution
	switch mode {
	case "export":
		output := literalRelativePath(nextDistDirRE, config.text)
		if output == "" {
			output = "out"
		}
		resolution = static(output, false)
		resolution.CleanURLs = true
		resolution.Base = literalBasePath(basePathRE, config.text)
		resolution.note(config.name, "output: 'export' writes a static site to "+output+"/, which next start refuses to serve")
		if files.nextImage != "" {
			resolution.Findings = append(resolution.Findings, nodeFinding("next_export_images", PreflightWarning,
				"next/image cannot optimise images in a static export", files.nextImage+" imports next/image; "+config.name+" does not set images.unoptimized",
				"The default image loader needs a server, so next build stops with \"Image Optimization using the default loader is not compatible with export\".",
				"Set images: { unoptimized: true } in "+config.name+", or give next/image a custom loader.", "configuration.build"))
		}
	case "unknown":
		resolution = server(nodeExecRunner(runner)+" next start", 3000, "")
		resolution.decide(ConfidenceMedium, config.name+" sets output from an expression; confirm whether it exports a static site (output directory out) or runs next start")
	default:
		resolution = server(nodeExecRunner(runner)+" next start", 3000, "")
	}
	if manifest.has("payload") && manifest.has("@payloadcms/next") {
		resolution.note("package.json", "Payload runs inside Next.js (payload with @payloadcms/next)")
	}
	return resolution
}

func astroResolution(manifest nodeManifest, files nodeRootFiles, _ string) nodeFrameworkResolution {
	config, configured := files.configText("astro")
	adapter := ""
	output := ""
	if configured {
		if match := astroAdapterRE.FindStringSubmatch(config.text); match != nil {
			adapter = match[1]
		}
		if match := astroOutputRE.FindStringSubmatch(config.text); match != nil {
			output = match[1]
		}
	}
	if adapter == "" && manifest.has("@astrojs/node") {
		adapter = "@astrojs/node"
	}
	if adapter == "" {
		for _, provider := range []string{"@astrojs/vercel", "@astrojs/netlify", "@astrojs/cloudflare", "@astrojs/deno"} {
			if manifest.has(provider) {
				adapter = provider
				break
			}
		}
	}
	switch {
	case adapter == "@astrojs/node":
		resolution := server("node ./dist/server/entry.mjs", 4321, "dist/server/entry.mjs")
		// The standalone Node adapter binds to localhost unless told
		// otherwise, which inside a container is a server nobody can
		// reach.
		resolution.Env = []string{"HOST=0.0.0.0"}
		if configured {
			if match := astroModeRE.FindStringSubmatch(config.text); match != nil && match[1] == "middleware" {
				resolution.decide(ConfidenceMedium, "Astro's Node adapter is in middleware mode, which exports a handler for another server; set mode: 'standalone' or give the start command of the server that mounts it")
				resolution.note(config.name, "@astrojs/node mode: 'middleware'")
			}
		}
		return resolution
	case adapter != "":
		resolution := static("dist", false)
		resolution.decide(ConfidenceLow, "Astro is configured for "+strings.TrimPrefix(adapter, "@astrojs/")+"; use @astrojs/node on this server, or a static build")
		return resolution
	case output == "server":
		resolution := static("dist", false)
		resolution.decide(ConfidenceLow, config.name+" sets output: 'server' without an adapter; add @astrojs/node, or build a static site")
		return resolution
	}
	resolution := static("dist", false)
	if configured {
		resolution.Base = literalBasePath(jsBaseRE, config.text)
	}
	return resolution
}

func nuxtResolution(manifest nodeManifest, files nodeRootFiles, runner string) nodeFrameworkResolution {
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
	if config, ok := files.configText("nuxt"); ok {
		return nitroPresetServer(config, "Nuxt")
	}
	return nitroServer()
}

// reactRouterResolution serves React Router's framework mode, or Remix,
// which it succeeded: its own server package when installed, the start
// script of a custom server (Express in the Epic Stack) when there is one,
// and the client build as a single-page site in SPA mode (ssr: false).
func reactRouterResolution(label, serve, binary string) func(nodeManifest, nodeRootFiles, string) nodeFrameworkResolution {
	return func(manifest nodeManifest, files nodeRootFiles, runner string) nodeFrameworkResolution {
		for _, kind := range []string{"react-router", "vite"} {
			config, ok := files.configText(kind)
			if !ok || !ssrFalseRE.MatchString(config.text) || (kind == "vite" && !strings.Contains(config.text, "remix(")) {
				continue
			}
			resolution := static("build/client", true)
			resolution.note(config.name, "ssr: false builds a single-page site in build/client")
			return resolution
		}
		entry := "build/server/index.js"
		if files.has("remix.config.js") && label == "Remix" {
			entry = "build/index.js"
		}
		if manifest.has(serve) {
			return server(nodeExecRunner(runner)+" "+binary+" ./"+entry, 3000, entry)
		}
		resolution := server("", 3000, "")
		if nodeServingScript(manifest.Scripts, []string{"start"}) == "" {
			resolution.decide(ConfidenceMedium, label+" has no "+serve+"; confirm the start command of its custom server")
		}
		return resolution
	}
}

func angularResolution(manifest nodeManifest, files nodeRootFiles, _ string) nodeFrameworkResolution {
	name, output, ok := angularOutput(files.angularJSON)
	mode := ""
	if ok {
		mode = angularOutputMode(files.angularJSON, name)
	}
	if manifest.has("@angular/ssr") && mode != "static" {
		if !ok {
			resolution := server("", 4000, "")
			resolution.StartScripts = nil
			resolution.decide(ConfidenceMedium, "angular.json names no application project; confirm the server start command")
			return resolution
		}
		entry := path.Join(path.Dir(output), "server", "server.mjs")
		if name != "" && path.Base(output) != "browser" {
			entry = path.Join(output, "server", "server.mjs")
		}
		resolution := server("node "+entry, 4000, entry)
		// The CLI's `start` is `ng serve`, the development server; the
		// script it writes for the production server is serve:ssr:<name>.
		resolution.StartScripts = nil
		for _, script := range []string{"serve:ssr:" + name, "serve:ssr"} {
			if nodeScriptRunsEntry(manifest.Scripts[script], entry) {
				resolution.StartScripts = []string{script}
				break
			}
		}
		return resolution
	}
	if !ok {
		resolution := static("dist", true)
		resolution.decide(ConfidenceMedium, "angular.json names no application project; confirm the output directory")
		return resolution
	}
	resolution := static(output, true)
	if mode == "static" {
		resolution.note("angular.json", "outputMode static: prerendered pages in "+output)
	}
	return resolution
}

func nestResolution(manifest nodeManifest, files nodeRootFiles, _ string) nodeFrameworkResolution {
	layout := nestEntry(files)
	start := "node " + strings.TrimSuffix(layout.entry, ".js")
	resolution := server(start, 3000, layout.entry)
	resolution.StartScripts = []string{"start:prod"}
	if layout.entry == "dist/main.js" {
		return resolution
	}
	resolution.note(orDefault(files.config["nest-cli"].name, "tsconfig.json"), "nest build writes "+layout.entry+": "+layout.reason)
	if body := manifest.Scripts["start:prod"]; body != "" && !nodeScriptRunsEntry(body, layout.entry) {
		resolution.StartScripts = nil
		resolution.Findings = append(resolution.Findings, nodeFinding("nest_output_layout", PreflightWarning,
			"NestJS writes its entry where start:prod does not look", "start:prod runs `"+boundedEvidence(body)+"`; the build writes "+layout.entry,
			layout.reason+"; the start command runs "+start+" instead of the script, which would stop with \"Cannot find module\".",
			`Add "include": ["src"] to tsconfig.build.json so the build writes dist/main.js, or change start:prod to `+start+".", "configuration.build.startCommand"))
	}
	return resolution
}

// adonisResolution serves AdonisJS from what `node ace build` writes: the
// compiled application in build/, whose package.json carries the #imports
// map, started from the package root so node_modules resolves. The
// template's `start` script is written to run inside build/, so it is not
// the start command here.
func adonisResolution(manifest nodeManifest, files nodeRootFiles, _ string) nodeFrameworkResolution {
	entry, ace := "build/bin/server.js", "build/ace.js"
	if semverMajor(manifest.version("@adonisjs/core")) == "5" {
		entry, ace = "build/server.js", "build/ace.js"
	}
	start := "node " + entry
	if manifest.has("@adonisjs/lucid") && files.has("database/migrations") {
		start = "node " + ace + " migration:run --force && " + start
	}
	resolution := server(start, 3333, entry)
	resolution.StartScripts = nil
	resolution.note("package.json", "AdonisJS serves "+entry+" from what node ace build writes")
	return resolution
}

// medusaResolution runs Medusa v2 the way its deployment guide does: from
// the .medusa/server directory `medusa build` writes, after applying its
// migrations. The build's package.json there names the same dependencies as
// the root's, which the image already installed, and the medusa binary is on
// PATH from node_modules/.bin.
func medusaResolution(nodeManifest, nodeRootFiles, string) nodeFrameworkResolution {
	resolution := server("cd .medusa/server && medusa db:migrate && medusa start", 9000, ".medusa/server/package.json")
	resolution.StartScripts = nil
	resolution.note("medusa-config.ts", "Medusa serves .medusa/server after medusa db:migrate")
	return resolution
}

// qwikCityResolution serves the adapter the build.server script builds with.
func qwikCityResolution(manifest nodeManifest, _ nodeRootFiles, _ string) nodeFrameworkResolution {
	adapterScript := manifest.Scripts["build.server"]
	for _, adapter := range []string{"express", "fastify", "node-server"} {
		if strings.Contains(adapterScript, "adapters/"+adapter+"/") {
			entry := "server/entry." + adapter + ".js"
			resolution := server("node server/entry."+adapter, 3000, entry)
			resolution.StartScripts = nil
			if nodeScriptRunsEntry(manifest.Scripts["serve"], entry) {
				resolution.StartScripts = []string{"serve"}
			}
			return resolution
		}
	}
	resolution := static("dist", false)
	resolution.CleanURLs = true
	if !strings.Contains(adapterScript, "adapters/static/") {
		resolution.decide(ConfidenceLow, "Qwik City has no server adapter; add one (npm run qwik add express) or the static adapter")
	}
	return resolution
}

// vikeResolution serves a Vike application through the server its own
// scripts start, or its prerendered pages.
func vikeResolution(manifest nodeManifest, files nodeRootFiles, _ string) nodeFrameworkResolution {
	for _, name := range []string{"start", "prod", "serve", "production"} {
		if body := manifest.Scripts[name]; body != "" && nodeScriptDevServer(manifest.Scripts, name) == "" && nodeRunsFile(body) {
			resolution := server("", 3000, "")
			resolution.StartScripts = []string{name}
			return resolution
		}
	}
	if config, ok := files.configText("vite"); ok && prerenderTrueRE.MatchString(config.text) {
		resolution := static("dist/client", false)
		resolution.CleanURLs = true
		resolution.note(config.name, "Vike prerenders every page into dist/client")
		return resolution
	}
	resolution := server("", 3000, "")
	resolution.StartScripts = nil
	resolution.decide(ConfidenceMedium, "Vike renders through a server the project provides; give its start command, or enable prerendering")
	return resolution
}

// tanstackResolution reads where TanStack Start writes its server: Nitro's
// .output when its Vite plugin (or the older vinxi app.config) builds it.
func tanstackResolution(_ nodeManifest, files nodeRootFiles, _ string) nodeFrameworkResolution {
	if config, ok := files.configText("app"); ok {
		return nitroPresetServer(config, "TanStack Start")
	}
	if config, ok := files.configText("vite"); ok && nitroVitePluginRE.MatchString(config.text) {
		resolution := nitroPresetServer(config, "TanStack Start")
		resolution.note(config.name, "the Nitro Vite plugin writes .output/server/index.mjs")
		return resolution
	}
	resolution := nitroServer()
	resolution.decide(ConfidenceMedium, "confirm the server entry TanStack Start writes for this version")
	return resolution
}

// siteBundler is a single-page site a bundler writes to the directory its
// configuration names, dist by default.
func siteBundler(kind string, outputRE ...*regexp.Regexp) func(nodeManifest, nodeRootFiles, string) nodeFrameworkResolution {
	return func(_ nodeManifest, files nodeRootFiles, _ string) nodeFrameworkResolution {
		output := "dist"
		if config, ok := files.configText(kind); ok {
			for _, re := range outputRE {
				if match := re.FindStringSubmatch(config.text); match != nil {
					if value := strings.TrimSuffix(strings.TrimPrefix(match[1], "./"), "/"); safeRelativePath(value) && nodeMemberPathRE.MatchString(value) {
						output = value
						break
					}
				}
			}
		}
		resolution := static(output, true)
		resolution.Confidence = ConfidenceMedium
		return resolution
	}
}

var (
	viteRootRE           = regexp.MustCompile(`(?:^|[\s{,])root\s*:\s*['"]([\w./-]+)['"]`)
	viteOutDirResolvedRE = regexp.MustCompile(`(?:^|[\s{,])outDir\s*:\s*path\.(?:resolve|join)\(\s*(?:__dirname|import\.meta\.dirname)\s*,\s*['"]([\w./-]+)['"]\s*\)`)
)

// viteResolution is a Vite site: the directory vite.config writes (outDir,
// under its root), served under its base.
func viteResolution(_ nodeManifest, files nodeRootFiles, _ string) nodeFrameworkResolution {
	resolution := static("dist", true)
	config, ok := files.configText("vite")
	if !ok {
		return resolution
	}
	root := literalRelativePath(viteRootRE, config.text)
	output := "dist"
	if match := viteOutDirResolvedRE.FindStringSubmatch(config.text); match != nil {
		// path.resolve(__dirname, "dist/public") names it from the package.
		output, root = strings.TrimSuffix(strings.TrimPrefix(match[1], "./"), "/"), ""
	} else if match := viteOutDirRE.FindStringSubmatch(config.text); match != nil {
		output = strings.TrimSuffix(strings.TrimPrefix(match[1], "./"), "/")
	}
	if root != "" {
		output = path.Join(root, output)
	}
	if output = path.Clean(output); safeRelativePath(output) && nodeMemberPathRE.MatchString(output) && output != "dist" {
		resolution.Output = output
		resolution.note(config.name, "the site is written to "+output)
	}
	resolution.Base = literalBasePath(jsBaseRE, config.text)
	return resolution
}

var nodeFrameworks = []nodeFramework{
	{
		// Headless CMS and commerce stacks come first: their own servers
		// serve an admin that Next.js or Vite merely builds.
		Name: "strapi", Label: "Strapi", Dependencies: []string{"@strapi/strapi"},
		resolve: func(_ nodeManifest, _ nodeRootFiles, runner string) nodeFrameworkResolution {
			resolution := server(nodeExecRunner(runner)+" strapi start", 1337, "")
			// The admin panel is built for production only when the build
			// runs with NODE_ENV=production.
			resolution.BuildEnv = []nodeEnvDefault{{"NODE_ENV", "production"}}
			return resolution
		},
	},
	{
		Name: "medusa", Label: "Medusa", Dependencies: []string{"@medusajs/medusa"},
		when:    func(manifest nodeManifest) bool { return manifest.Dependencies["@medusajs/medusa"] != "" },
		resolve: medusaResolution,
	},
	{
		Name: "directus", Label: "Directus", Dependencies: []string{"directus"},
		resolve: func(_ nodeManifest, _ nodeRootFiles, runner string) nodeFrameworkResolution {
			// bootstrap installs or migrates the database and is safe to run
			// on every start; start alone refuses an uninitialised one.
			exec := nodeExecRunner(runner)
			resolution := server(exec+" directus bootstrap && "+exec+" directus start", 8055, "")
			resolution.StartScripts = nil
			return resolution
		},
	},
	{
		Name: "keystone", Label: "KeystoneJS", Dependencies: []string{"@keystone-6/core"},
		resolve: func(_ nodeManifest, _ nodeRootFiles, runner string) nodeFrameworkResolution {
			exec := nodeExecRunner(runner)
			resolution := server(exec+" keystone start --with-migrations", 3000, "")
			resolution.StartScripts = nil
			resolution.Build = exec + " keystone build"
			return resolution
		},
	},
	{Name: "adonisjs", Label: "AdonisJS", Dependencies: []string{"@adonisjs/core"}, resolve: adonisResolution},
	{
		Name: "redwood", Label: "RedwoodJS", Dependencies: []string{"@redwoodjs/core"},
		resolve: func(_ nodeManifest, files nodeRootFiles, runner string) nodeFrameworkResolution {
			exec := nodeExecRunner(runner)
			start := exec + " rw serve"
			if files.has("api/db/migrations") {
				start = exec + " rw prisma migrate deploy && " + start
			}
			resolution := server(start, 8910, "")
			resolution.StartScripts = nil
			resolution.Build = exec + " rw build"
			resolution.Confidence = ConfidenceMedium
			return resolution
		},
	},
	{Name: "nextjs", Label: "Next.js", Dependencies: []string{"next"}, resolve: nextResolution},
	{Name: "sveltekit", Label: "SvelteKit", Dependencies: []string{"@sveltejs/kit"}, resolve: sveltekitResolution},
	{Name: "astro", Label: "Astro", Dependencies: []string{"astro"}, resolve: astroResolution},
	{Name: "nuxt", Label: "Nuxt", Dependencies: []string{"nuxt"}, resolve: nuxtResolution},
	{Name: "remix", Label: "Remix", Dependencies: []string{"@remix-run/dev"}, resolve: reactRouterResolution("Remix", "@remix-run/serve", "remix-serve")},
	{Name: "react-router", Label: "React Router", Dependencies: []string{"@react-router/dev"}, resolve: reactRouterResolution("React Router", "@react-router/serve", "react-router-serve")},
	{
		Name: "solid-start", Label: "SolidStart", Dependencies: []string{"@solidjs/start"},
		resolve: func(_ nodeManifest, files nodeRootFiles, _ string) nodeFrameworkResolution {
			if config, ok := files.configText("app"); ok {
				return nitroPresetServer(config, "SolidStart")
			}
			return nitroServer()
		},
	},
	{Name: "tanstack-start", Label: "TanStack Start", Dependencies: []string{"@tanstack/react-start", "@tanstack/solid-start", "@tanstack/start"}, resolve: tanstackResolution},
	{
		Name: "nitro", Label: "Nitro", Dependencies: []string{"nitropack", "nitro"},
		resolve: func(nodeManifest, nodeRootFiles, string) nodeFrameworkResolution { return nitroServer() },
	},
	{Name: "qwik-city", Label: "Qwik City", Dependencies: []string{"@builder.io/qwik-city", "@qwik.dev/router"}, resolve: qwikCityResolution},
	{
		Name: "analog", Label: "Analog", Dependencies: []string{"@analogjs/platform"},
		resolve: func(_ nodeManifest, files nodeRootFiles, _ string) nodeFrameworkResolution {
			if config, ok := files.configText("vite"); ok && ssrFalseRE.MatchString(config.text) {
				return static("dist/analog/public", true)
			}
			return server("node dist/analog/server/index.mjs", 3000, "dist/analog/server/index.mjs")
		},
	},
	{Name: "angular", Label: "Angular", Dependencies: []string{"@angular/core"}, resolve: angularResolution},
	{Name: "nestjs", Label: "NestJS", Dependencies: []string{"@nestjs/core"}, resolve: nestResolution},
	{Name: "vike", Label: "Vike", Dependencies: []string{"vike", "vite-plugin-ssr"}, resolve: vikeResolution},
	{
		Name: "waku", Label: "Waku", Dependencies: []string{"waku"},
		resolve: func(_ nodeManifest, _ nodeRootFiles, runner string) nodeFrameworkResolution {
			return server(nodeExecRunner(runner)+" waku start", 8080, "")
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
			script, directory := docsBuild(manifest.Scripts, "vitepress build")
			resolution := static(path.Join(directory, ".vitepress", "dist"), false)
			if script != "build" {
				resolution.BuildScript = script
			}
			return resolution
		},
	},
	{
		Name: "vuepress", Label: "VuePress", Dependencies: []string{"vuepress", "@vuepress/cli"},
		resolve: func(manifest nodeManifest, _ nodeRootFiles, _ string) nodeFrameworkResolution {
			script, directory := docsBuild(manifest.Scripts, "vuepress build")
			resolution := static(path.Join(directory, ".vuepress", "dist"), false)
			if script != "build" {
				resolution.BuildScript = script
			}
			return resolution
		},
	},
	{
		Name: "rspress", Label: "Rspress", Dependencies: []string{"rspress", "@rspress/core"},
		resolve: func(nodeManifest, nodeRootFiles, string) nodeFrameworkResolution { return static("doc_build", false) },
	},
	{
		Name: "eleventy", Label: "Eleventy", Dependencies: []string{"@11ty/eleventy"},
		resolve: func(nodeManifest, nodeRootFiles, string) nodeFrameworkResolution { return static("_site", false) },
	},
	{
		Name: "hexo", Label: "Hexo", Dependencies: []string{"hexo"},
		resolve: func(nodeManifest, nodeRootFiles, string) nodeFrameworkResolution { return static("public", false) },
	},
	{
		Name: "slidev", Label: "Slidev", Dependencies: []string{"@slidev/cli"},
		resolve: func(nodeManifest, nodeRootFiles, string) nodeFrameworkResolution { return static("dist", true) },
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
	{Name: "rsbuild", Label: "Rsbuild", Dependencies: []string{"@rsbuild/core"}, resolve: siteBundler("rsbuild", rsbuildDistRootRE)},
	{Name: "rspack", Label: "Rspack", Dependencies: []string{"@rspack/cli"}, resolve: siteBundler("rspack", webpackOutputPathRE)},
	{Name: "farm", Label: "Farm", Dependencies: []string{"@farmfe/core"}, resolve: siteBundler("farm", farmOutputPathRE)},
	{
		// webpack is also a dependency of tools that are not the build, so
		// only a build script that runs it makes it the site's bundler.
		Name: "webpack", Label: "webpack", Dependencies: []string{"webpack-cli", "webpack"},
		when: func(manifest nodeManifest) bool {
			for _, segment := range nodeReachedSegments(manifest.Scripts, []string{"npm run build"}, false) {
				if words := nodeProgramWords(segment); len(words) > 0 && (words[0] == "webpack" || words[0] == "webpack-cli") {
					return true
				}
			}
			return false
		},
		resolve: siteBundler("webpack", webpackOutputPathRE),
	},
	{Name: "vite", Label: "Vite", Dependencies: []string{"vite"}, resolve: viteResolution},
}

// nodeSiteBuilders are the frameworks that only build a site: a package that
// also serves it from its own server library is that server, not a site.
var nodeSiteBuilders = map[string]bool{
	"vite": true, "parcel": true, "create-react-app": true, "vue-cli": true, "webpack": true,
	"rsbuild": true, "rspack": true, "farm": true,
}

// nodeServerLibraries are the HTTP frameworks a plain Node service is built
// on. They name the workload for the operator; the package's own start
// script or main file says how it runs. Earlier entries win, so a server
// framework is named before the socket or GraphQL library mounted on it.
var nodeServerLibraries = []struct{ dependency, name string }{
	{"@hapi/hapi", "hapi"}, {"express", "express"}, {"fastify", "fastify"},
	{"hono", "hono"}, {"@hono/node-server", "hono"}, {"koa", "koa"}, {"elysia", "elysia"},
	{"vite-express", "express"}, {"h3", "h3"}, {"polka", "polka"}, {"restify", "restify"},
	{"@apollo/server", "apollo"}, {"apollo-server", "apollo"}, {"apollo-server-express", "apollo"},
	{"graphql-yoga", "graphql-yoga"}, {"@trpc/server", "trpc"}, {"socket.io", "socket.io"}, {"ws", "ws"},
}

func matchNodeFramework(manifest nodeManifest) *nodeFramework {
	for index := range nodeFrameworks {
		framework := &nodeFrameworks[index]
		if framework.when != nil && !framework.when(manifest) {
			continue
		}
		for _, dependency := range framework.Dependencies {
			if manifest.has(dependency) {
				return framework
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

// nodeRuntimeServerLibrary is the server library the package runs in
// production: one in dependencies, not only a devDependency (vitest's
// vite, a mock server).
func nodeRuntimeServerLibrary(manifest nodeManifest) string {
	for _, library := range nodeServerLibraries {
		if manifest.Dependencies[library.dependency] != "" {
			return library.name
		}
	}
	return ""
}

// resolveNodeFramework is the framework a package's build and start serve.
// A site builder beside a server library the package runs in production,
// with a start command that runs a file (Express or Fastify serving a Vite
// client: the Replit template), is that server; so is a package that lists
// Vite only for its tests, with no index.html or vite.config to build a site
// from. Detection and the recipe both decide through here, the recipe with
// the plan's start command.
func resolveNodeFramework(manifest nodeManifest, files nodeRootFiles, start string) (*nodeFramework, string) {
	framework := matchNodeFramework(manifest)
	if framework == nil || !nodeSiteBuilders[framework.Name] {
		return framework, ""
	}
	library := nodeRuntimeServerLibrary(manifest)
	if library != "" && start != "" && nodeRunsFile(strings.Join(nodeReachedSegments(manifest.Scripts, []string{start}, false), " && ")) {
		return nil, strings.ToLower(framework.Label) + " builds the client; the start command serves it with " + library
	}
	if framework.Name == "vite" && library != "" && !files.has("index.html") {
		if _, configured := files.configText("vite"); !configured {
			return nil, "vite has no index.html or vite.config here to build a site from; " + library + " serves the package"
		}
	}
	return framework, ""
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
	command = strings.TrimSpace(command)
	if command == start || strings.HasSuffix(command, "&& "+start) {
		return true
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
	return docsBuild(scripts, "vitepress build")
}

// docsBuild finds the script that runs a documentation generator's build
// command (`vitepress build docs`, `vuepress build docs`) and the docs
// directory it names.
func docsBuild(scripts map[string]string, command string) (string, string) {
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
		_, after, found := strings.Cut(scripts[name], command)
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
