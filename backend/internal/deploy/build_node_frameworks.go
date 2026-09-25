package deploy

import (
	"path"
	"slices"
	"strings"
)

// What the Node recipe renders around a framework's build for the commands
// the plan runs: the entry the build must write, the steps after the build
// that its server needs, the runtime environment, and the fallback page of a
// static site, which the static server (build_static_serving.go) tries
// first. It is decided with the plan's own start command, so an operator's
// start command gets the same treatment as a detected one.

// nodeServing is what the recipe adds for the configured commands.
type nodeServing struct {
	// entry is the file the build must have written, checked after it.
	entry string
	// afterBuild are RUN lines after the build command; runtimeEnv are ENV
	// lines of the server's runtime stage.
	afterBuild []string
	runtimeEnv []string
	// page is the fallback page the framework writes into its static output
	// when it is not index.html.
	page  string
	notes []nodeFrameworkNote
}

// planNodeServing decides nodeServing for a plan. A static output's fallback
// page is the framework's only when the plan serves the framework's own
// output directory, since another directory may not hold it; a server's
// entry is checked when the start command, or a package script it runs,
// runs it.
func planNodeServing(manifest nodeManifest, framework nodeRecipeFramework, config BuildPlanConfig) nodeServing {
	serving := nodeServing{}
	resolution := framework.resolution
	if output := strings.TrimSpace(config.OutputDirectory); output != "" {
		if framework.name != "" && path.Clean(output) == path.Clean(resolution.Output) {
			serving.page = resolution.Fallback
		}
		return serving
	}
	start := strings.TrimSpace(config.StartCommand)
	if standalone := nodeStandaloneServer(manifest.Scripts, start); standalone != "" {
		// next build writes the standalone server without the static assets
		// and public files it serves; Next.js's own Docker example copies
		// them in, and the server binds HOSTNAME, which in a container is
		// the container's own name.
		directory := path.Dir(standalone)
		serving.entry = standalone
		serving.afterBuild = append(serving.afterBuild,
			"mkdir -p "+directory+"/.next && cp -r .next/static "+directory+"/.next/ && if [ -d public ]; then cp -r public "+directory+"/; fi")
		serving.runtimeEnv = append(serving.runtimeEnv, "HOSTNAME=0.0.0.0")
		serving.notes = append(serving.notes, nodeFrameworkNote{file: "package.json",
			reason: "the start command runs " + standalone + "; the build copies .next/static and public beside it"})
		return serving
	}
	if resolution.Entry != "" && (frameworkDefaultStart(start, resolution.Start) || nodeStartRunsEntry(manifest.Scripts, start, resolution.Entry)) {
		serving.entry = resolution.Entry
	}
	return serving
}

// nodeStandaloneServer is the Next.js standalone server a start command, or
// a package script it runs, starts: `node .next/standalone/server.js`, or
// the member's server in a monorepo's standalone output.
func nodeStandaloneServer(scripts map[string]string, start string) string {
	if start == "" {
		return ""
	}
	for _, segment := range nodeReachedSegments(scripts, []string{start}, false) {
		for _, word := range nodeProgramWords(segment) {
			word = strings.TrimPrefix(word, "./")
			if strings.HasPrefix(word, ".next/standalone/") && strings.HasSuffix(word, "server.js") &&
				safeRelativePath(word) && nodeMemberPathRE.MatchString(word) {
				return path.Clean(word)
			}
		}
	}
	return ""
}

// nodeMeteorRefusal is why a Meteor application is not built by the recipe.
const nodeMeteorRefusal = "Meteor builds with its own toolchain (meteor build), which the JavaScript recipe does not install; deploy it with a Dockerfile"

// nodeForeignRunners are programs of another language's toolchain, which no
// stage of the Node image provides; a command that reaches one stops with
// "not found" after the install, so it is refused before the build.
var nodeForeignRunners = []string{
	"deno", "python", "python3", "pip", "pip3", "uv", "poetry", "php", "composer", "ruby", "bundle", "rails",
	"java", "mvn", "gradle", "go", "cargo", "dotnet",
}

// nodeForeignRunner is the first foreign program the build (with the
// install's lifecycle scripts) or the start command reaches, and the field
// that runs it; provided are the programs the stage has beside Node. python3
// is in the build stage when a native addon brings the compilers in, so only
// a start command that reaches it, or a build without them, is refused.
func nodeForeignRunner(facts nodeInstallFacts, build, start string, provided []string) (string, string) {
	compilers := slices.ContainsFunc(nodeNativeAddons, facts.present)
	for _, command := range []struct {
		text, field string
		lifecycle   bool
	}{{build, "configuration.build.buildCommand", true}, {start, "configuration.build.startCommand", false}} {
		tools := map[string]bool{}
		for _, segment := range nodeReachedSegments(facts.manifest.Scripts, []string{command.text}, command.lifecycle) {
			if words := nodeSegmentWords(segment); len(words) > 0 {
				tools[path.Base(words[0])] = true
			}
		}
		for _, runner := range nodeForeignRunners {
			if !tools[runner] || slices.Contains(provided, runner) ||
				(runner == "python3" && compilers && command.field == "configuration.build.buildCommand") {
				continue
			}
			return runner, command.field
		}
	}
	return "", ""
}
