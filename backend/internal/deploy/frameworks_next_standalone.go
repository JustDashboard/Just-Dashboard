package deploy

import (
	"regexp"
	"strings"
)

// A Next.js app whose next.config says `output: 'standalone'` already has
// next build write a server that carries only the files it traces —
// .next/standalone/server.js with the node_modules it needs — so its image
// needs neither the whole node_modules nor the source. Detection used to
// propose `next start` for it anyway, which serves from all of /app. A new
// detection now proposes the standalone server, and the recipe's runtime
// stage then copies only .next/standalone (nodeStandaloneCopy). A stored
// start command is never rewritten: a plan that runs next start keeps it,
// and preflight says the standalone server is there (next_standalone_start).
//
// Next.js has no switch for an app that does not set standalone itself
// (16.4 reads no NEXT_PRIVATE_STANDALONE), and the recipe never wraps a
// repository's next.config to add one.

// nextStandaloneUnreadableRE matches the settings that move the standalone
// server somewhere the recipe would have to evaluate the config to find.
var nextStandaloneUnreadableRE = regexp.MustCompile(`(?:^|[\s{,])(?:distDir|outputFileTracingRoot)\s*:`)

// nextStandaloneResolution is a standalone app's server: the server.js
// next build writes, under the member's own path when the package is a
// workspace member, since Next.js traces from the workspace root its
// lockfile marks. The server binds HOSTNAME (planNodeServing sets it) and
// the PORT the runtime injects.
func nextStandaloneResolution(manifest nodeManifest, files nodeRootFiles, config nodeConfigText, runner string) nodeFrameworkResolution {
	if nextStandaloneUnreadableRE.MatchString(config.text) {
		resolution := server(nodeExecRunner(runner)+" next start", 3000, "")
		resolution.note(config.name, "output: 'standalone', with a distDir or outputFileTracingRoot the recipe does not evaluate; next start serves the build")
		return resolution
	}
	entry := ".next/standalone/server.js"
	if files.member != "" && nodeMemberPathRE.MatchString(files.member) && safeRelativePath(files.member) {
		entry = ".next/standalone/" + files.member + "/server.js"
	}
	resolution := server("node "+entry, 3000, entry)
	// The project's start script wins when it is more than next start or
	// the standalone server itself: it may run something first.
	if script := strings.TrimSpace(manifest.Scripts["start"]); script == "" || nextStartScriptRE.MatchString(script) ||
		strings.TrimPrefix(strings.TrimPrefix(script, "node "), "./") == entry {
		resolution.StartScripts = nil
	}
	resolution.note(config.name, "output: 'standalone' writes a self-contained server; it starts with node "+entry+" and its image carries only what that server traces")
	resolution.Findings = append(resolution.Findings, nodeFinding("next_standalone_start", PreflightPass,
		"next.config builds a standalone server", config.name+" sets output: 'standalone'",
		boundedFindingText("next build writes "+entry+" with only the files the server needs. A start command that runs it ships an image of that folder alone, several times smaller than one that runs next start over the whole application and node_modules."),
		"Set the start command to `node "+entry+"`.", "configuration.build.startCommand"))
	return resolution
}

// nextStartScriptRE is a start script that only runs next start.
var nextStartScriptRE = regexp.MustCompile(`^next start(?:\s+(?:-p|--port|-H|--hostname)\s+\S+)*$`)

// nodeStandaloneCopy is the runtime stage's copy of a standalone server, or
// nil when the start command does more than run it with node: a command or a
// package script that runs anything else may need the rest of /app.
// workdir is the package's directory in the image.
func nodeStandaloneCopy(start, workdir string) []string {
	fields := strings.Fields(start)
	if len(fields) != 2 || fields[0] != "node" {
		return nil
	}
	entry := strings.TrimPrefix(fields[1], "./")
	if !strings.HasPrefix(entry, ".next/standalone/") || !strings.HasSuffix(entry, "/server.js") ||
		!safeRelativePath(entry) || !nodeMemberPathRE.MatchString(entry) {
		return nil
	}
	return []string{"COPY --from=build " + workdir + "/.next/standalone " + workdir + "/.next/standalone"}
}
