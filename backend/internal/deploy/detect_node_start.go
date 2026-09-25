package deploy

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

// How a package without a framework is started, and how a workspace member
// is built, when its scripts do not say so plainly: a `start` script that
// runs a watcher or a development server, a Bun or Hono starter whose only
// script is `dev`, a member whose workspace dependencies build first.

// scriptStart is the start command for a package whose `start` script
// exists: the script, unless it starts a development server or a watcher;
// then another script that serves a build, or the watcher's own command
// without the watcher, run the way the script would have run it.
func (in nodeCommandInputs) scriptStart(runner string) string {
	scripts := in.manifest.Scripts
	if nodeScriptDevServer(scripts, "start") == "" {
		return runner + " run start"
	}
	if script := nodeServingScript(scripts, nodeProductionScripts); script != "" {
		return runner + " run " + script
	}
	command, ok := nodeWatchedCommand(scripts["start"], in.declared)
	if !ok {
		return runner + " run start"
	}
	command = withExecRunner(command, nodeExecRunner(runner), in.declared)
	// npm, pnpm, Bun and Yarn 1 ran prestart before the script.
	if strings.TrimSpace(scripts["prestart"]) != "" {
		command = runner + " run prestart && " + command
	}
	return command
}

func (in nodeCommandInputs) declared(name string) bool { return in.manifest.has(name) }

// derivedStart is the start of a server library's package that has no
// start script, main file or Procfile — the Hono and Elysia starters ship
// only `dev` — from its dev script without the watcher, or from the file its
// starter keeps the server in. why is the evidence.
func (in nodeCommandInputs) derivedStart(runner string) (command, why string) {
	for _, name := range []string{"dev", "develop", "watch"} {
		body := strings.TrimSpace(in.manifest.Scripts[name])
		if body == "" {
			continue
		}
		if command, ok := nodeWatchedCommand(body, in.declared); ok {
			if runner != "bun" {
				command = nodeBunFileRunner(command, runner, in.declared)
			}
			return withExecRunner(command, nodeExecRunner(runner), in.declared), name + " script without its watcher: " + boundedEvidence(body)
		}
	}
	if len(in.files.entries) == 0 {
		return "", ""
	}
	entry := in.files.entries[0]
	return nodeFileCommand(runner, entry, in.declared), "server entry " + entry
}

// nodeFileCommand runs a script file on the package's runtime: Bun runs
// TypeScript itself; on Node, TypeScript runs through tsx when the package
// installs it, else through Node's own type stripping.
func nodeFileCommand(runner, file string, declared func(string) bool) string {
	switch {
	case runner == "bun":
		return "bun " + file
	case strings.HasSuffix(file, ".ts") && declared("tsx"):
		return nodeExecRunner(runner) + " tsx " + file
	}
	return "node " + file
}

// nodeBunFileRunner moves a `bun <file>` a Bun starter's dev script runs to
// the package's runner when the package does not install with Bun.
func nodeBunFileRunner(command, runner string, declared func(string) bool) string {
	words := strings.Fields(command)
	if len(words) == 2 && words[0] == "bun" {
		return nodeFileCommand(runner, words[1], declared)
	}
	return command
}

var (
	nodeFetchExportRE  = regexp.MustCompile(`export\s+default\s+(?:\{[^}]*\bfetch\b|app\b|server\b|[A-Za-z_$][\w$]*\s*;?\s*$)`)
	nodeListenCallRE   = regexp.MustCompile(`\.listen\s*\(|\bserve\s*\(\s*\{|\bserve\s*\(\s*app\b|Bun\.serve\s*\(|createServer\s*\(`)
	nodeBunOnlyImports = regexp.MustCompile(`from\s+['"]bun['"]|\bBun\.(?:serve|file|write|env)\b`)
)

// nodeBunOnlyEntry says the file a start command runs is served only by
// Bun: it exports a fetch handler by default and starts no server of its
// own (Hono's Bun template), or calls Bun's own APIs. Under Node such an
// entry exits as soon as it has run.
func nodeBunOnlyEntry(head string) bool {
	if nodeBunOnlyImports.MatchString(head) {
		return true
	}
	return nodeFetchExportRE.MatchString(head) && !nodeListenCallRE.MatchString(head)
}

// nodeStartedFile is the script file a start command runs directly.
func nodeStartedFile(start string) string {
	segments := nodeCommandSegments(start)
	if len(segments) == 0 {
		return ""
	}
	words := nodeProgramWords(segments[len(segments)-1])
	if len(words) < 2 {
		return ""
	}
	switch path.Base(words[0]) {
	case "node", "tsx", "ts-node":
		for _, word := range words[1:] {
			if !strings.HasPrefix(word, "-") {
				return strings.TrimPrefix(word, "./")
			}
		}
	}
	return ""
}

// nodeWorkspaceBuild builds a workspace member after the workspace packages
// it depends on that build themselves, through the manager's own workspace
// commands, which find the workspace root from the member's directory:
// pnpm's filter builds the member's dependencies first itself; npm, Yarn and
// Bun name each dependency, in dependency order.
func nodeWorkspaceBuild(runner, member, script string, dependencies []string) string {
	if runner == "pnpm" {
		return "pnpm --filter " + member + "... run " + script
	}
	steps := make([]string, 0, len(dependencies)+1)
	for _, dependency := range dependencies {
		switch runner {
		case "yarn":
			steps = append(steps, "yarn workspace "+dependency+" run build")
		case "bun":
			steps = append(steps, "bun run --filter "+dependency+" build")
		default:
			steps = append(steps, "npm run build --workspace="+dependency)
		}
	}
	return strings.Join(append(steps, runner+" run "+script), " && ")
}

// nodeWorkspaceBuildOrder lists, dependencies first, the workspace packages
// a member depends on (directly or through another member) that have a build
// script of their own. members are the workspace's packages by name.
func nodeWorkspaceBuildOrder(member nodeInstallManifest, members map[string]nodeInstallManifest) []string {
	order := []string{}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var visit func(manifest nodeInstallManifest, depth int)
	visit = func(manifest nodeInstallManifest, depth int) {
		if depth > 16 {
			return
		}
		names := []string{}
		for _, kind := range []map[string]string{manifest.Dependencies, manifest.DevDependencies} {
			for name, spec := range kind {
				if _, ok := members[name]; ok && (strings.HasPrefix(spec, "workspace:") || !strings.Contains(spec, ":")) {
					names = append(names, name)
				}
			}
		}
		slices.Sort(names)
		for _, name := range slices.Compact(names) {
			if visited[name] || visiting[name] || name == member.Name {
				continue
			}
			visiting[name] = true
			visit(members[name], depth+1)
			visiting[name] = false
			visited[name] = true
			if strings.TrimSpace(members[name].Scripts["build"]) != "" && nodePackageNameRE.MatchString(name) {
				order = append(order, name)
			}
		}
	}
	visit(member, 0)
	return order
}
