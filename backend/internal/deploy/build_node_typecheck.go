package deploy

import (
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// next build type-checks a TypeScript project after it compiles, on one
// core, and from nothing on every build: Next.js keeps TypeScript's
// incremental state in .next/cache/.tsbuildinfo, beside the compiler caches
// the recipe mounts (nextBuildCaches) rather than inside one. On a Next.js
// 16 application that was 30 to 50 s of a 60 to 106 s build, more than the
// compile and the prerender together.
//
// So the type check runs as a stage of its own beside the build: BuildKit
// runs two stages that do not depend on each other at once. The stage
// generates the route types and settles tsconfig.json as next build would
// (`next typegen`), then runs a check of next build's (nextTypeCheckSource)
// incrementally, its state in a cache mount of the environment's: on
// TypeScript's API where the application's TypeScript has one, and through
// its tsc command where it has none (TypeScript 7), which is the only check
// next build has for that package. The build
// runs next build with the type check left out, through a build adapter
// (NEXT_ADAPTER_PATH, which Next.js 16.4 reads as adapterPath) whose
// modifyConfig sets typescript.ignoreBuildErrors. The runtime stage copies
// an empty file from the type check stage, so the image waits for it and a
// type error still fails the build, as `error TSnnnn` (build_type_error).
//
// Only where that is the same check next build would make:
//   - a build command that is next build and nothing before it
//     (nextBuildIsPlain): next build itself, or a package script that is,
//     with no pre-script. A step before it (`generate-types && next build`,
//     a prebuild hook) can write files the source imports, and the check,
//     started from the source, would never see them;
//   - a package that declares typescript and has a tsconfig.json;
//   - Next.js 16.4 or later by its declared range, the release whose
//     adapter hook this was checked against;
//   - a next.config that names none of ignoreBuildErrors (the project
//     decides about type errors itself), tsconfigPath (tsc would read
//     another file) or adapterPath (the project's adapter would replace
//     this one);
//   - no build value named NEXT_ADAPTER_PATH;
//   - a server, not a static export;
//   - a server with the cores and memory for both at once
//     (hostTypeCheckBeside), since the check and the compile then peak
//     together.
// Anything else keeps next build's own check.

// nextAdapterEnv and nextAdapterPath are the variable Next.js reads its
// adapter from and where the build stage writes this one.
const (
	nextAdapterEnv  = "NEXT_ADAPTER_PATH"
	nextAdapterPath = "/opt/just-dashboard/next-adapter.mjs"
)

// nextAdapterSource is the adapter: it changes nothing but leaving the type
// check to the stage beside the build. One line, since every instruction of
// a generated Dockerfile is one line (withBuildValueGuards).
const nextAdapterSource = `export default { name: "just-dashboard", modifyConfig: (config) => ({ ...config, typescript: { ...config.typescript, ignoreBuildErrors: true } }) }`

// nextTypeCheckSource is the check next build would make, on whichever of
// TypeScript's two faces next build would use, after next typegen has
// written next build's settings into tsconfig.json. One line, like the
// adapter, and no single quote, since the stage writes it with printf.
//
// Next.js 16.4 reads the package as getTypeScriptPackageInfo does
// (next/dist/lib/typescript/runTypeScriptCli.js): the compiler API is
// lib/typescript.js, and the command is the package's tsc bin. TypeScript 7,
// the native compiler, ships the command and no API: requiring it gives a
// version and nothing else. So the script asks which the package has, and
// never calls an API that is not there.
const nextTypeCheckSource = nextTypeCheckPackage +
	`if (api && typeof api.getParsedCommandLineOfConfigFile === "function" && typeof api.createIncrementalProgram === "function") { ` + nextTypeCheckAPI + ` } else { ` + nextTypeCheckCLI + ` }`

// nextTypeCheckPackage finds the application's TypeScript: its manifest, and
// its API where the package has one.
const nextTypeCheckPackage = `const path = require("path"); const fs = require("fs"); ` +
	`const manifestPath = require.resolve("typescript/package.json", { paths: [process.cwd()] }); const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8")); ` +
	`const home = path.dirname(manifestPath); const apiPath = path.join(home, "lib", "typescript.js"); const api = fs.existsSync(apiPath) ? require(apiPath) : null; `

// nextTypeCheckAPI is the check on TypeScript's API, for a package that has
// one (TypeScript 5 and 6): what next/dist/lib/typescript/runTypeCheck.js
// does. The files tsconfig.json names, less .next/dev/types; its options
// with nothing emitted, incrementally, the state in nextTypeCheckState; its
// errors, less a configuration with no inputs (TS18003) and anything in a
// __tests__ or __mocks__ folder or a .test. or .spec. file, which that check
// leaves out too. Errors print as `file(line,col): error TSnnnn: …`.
const nextTypeCheckAPI = `const ts = api; ` +
	`const config = ts.getParsedCommandLineOfConfigFile(path.resolve("tsconfig.json"), {}, { ...ts.sys, onUnRecoverableConfigFileDiagnostic: (d) => { throw new Error(ts.flattenDiagnosticMessageText(d.messageText, "\n")) } }); ` +
	`const devTypes = path.resolve(".next/dev/types"); const rootNames = config.fileNames.filter((name) => !name.startsWith(devTypes)); ` +
	`const program = ts.createIncrementalProgram({ rootNames, options: { ...config.options, composite: false, incremental: true, declarationMap: false, emitDeclarationOnly: false, noEmit: true, tsBuildInfoFile: "` + nextTypeCheckState + `/tsbuildinfo" } }); ` +
	`const emitted = rootNames.length > 0 ? program.emit() : { diagnostics: [] }; const ignored = /[\\/]__(?:tests|mocks)__[\\/]|(?<=[\\/.])(?:spec|test)\.[^\\/]+$/; ` +
	`const found = config.errors.filter((d) => d.code !== 18003).concat(rootNames.length > 0 ? ts.getPreEmitDiagnostics(program).concat(emitted.diagnostics) : []); ` +
	`const errors = found.filter((d) => d.category === ts.DiagnosticCategory.Error && !(d.file && ignored.test(d.file.fileName))); ` +
	`if (errors.length > 0) { process.stderr.write(ts.formatDiagnostics(errors, { getCanonicalFileName: (name) => name, getCurrentDirectory: () => process.cwd(), getNewLine: () => "\n" })); process.exit(1) }`

// nextTypeCheckCLI is the check on TypeScript's command, for a package with
// no API (TypeScript 7): what next/dist/lib/typescript/runTypeCheckCli.js
// does, which is next build's own check of such a package. The command is
// the one next build finds: the package's tsc bin (or the tscN an aliased
// package renames it to), and lib/tsc.js in place of an extensionless bin of
// an ES module package, which older Node.js releases cannot start. It runs
// under this Node.js, twice. `tsc --showConfig` says whether the project is
// incremental; then the whole project with nothing emitted, its state in
// nextTypeCheckState when it is incremental. Nothing is left out here: the
// command checks every file tsconfig.json names, tests and .next/dev/types
// among them, and so does next build. --pretty false is the one argument
// next build does not pass, so errors print as `file(line,col): error
// TSnnnn: …` whatever the compiler makes of the build's output.
const nextTypeCheckCLI = `const { spawnSync } = require("child_process"); ` +
	`const stop = (message) => { process.stderr.write(message + "\n"); process.exit(1) }; ` +
	`const stopped = (run) => run.error ? String(run.error.message || run.error) : run.signal ? "the TypeScript compiler was stopped by " + run.signal : ""; ` +
	`const bins = typeof manifest.bin === "string" ? { tsc: manifest.bin } : manifest.bin || {}; const bin = bins.tsc || (Object.entries(bins).find(([name]) => /^tsc\d+$/.test(name)) || [])[1]; ` +
	`let tsc = bin ? path.resolve(home, bin) : ""; const entry = path.join(home, "lib", "tsc.js"); ` +
	`if (tsc && fs.existsSync(tsc) && manifest.type === "module" && path.extname(tsc) === "" && fs.existsSync(entry)) { tsc = entry } ` +
	`if (!tsc || !fs.existsSync(tsc)) { stop("TypeScript " + manifest.version + " has neither the compiler API nor a tsc command to check types with.") } ` +
	`const project = path.resolve("tsconfig.json"); ` +
	`const shown = spawnSync(process.execPath, [tsc, "--showConfig", "--project", project, "--pretty", "false"], { encoding: "utf8", maxBuffer: Infinity }); ` +
	`if (shown.status !== 0) { stop([shown.stdout, shown.stderr, stopped(shown)].filter(Boolean).join("\n").trim() || "tsc --showConfig failed.") } ` +
	`let options; try { options = JSON.parse(shown.stdout).compilerOptions || {} } catch (cause) { stop("Could not parse the output of tsc --showConfig: " + cause.message) } ` +
	`const args = ["--project", project, "--noEmit", "--declarationMap", "false", "--emitDeclarationOnly", "false", "--pretty", "false"]; ` +
	`if (options.incremental || options.composite) { args.push("--tsBuildInfoFile", "` + nextTypeCheckState + `/cli.tsbuildinfo") } ` +
	`const checked = spawnSync(process.execPath, [tsc, ...args], { stdio: "inherit" }); ` +
	`if (checked.status !== 0) { stop(stopped(checked) || "Failed to type check.") }`

// nextTypeCheckScript is where the type check stage writes it.
const nextTypeCheckScript = "/opt/just-dashboard/typecheck.cjs"

// nextTypeCheckMarker is the empty file the type check stage writes when it
// passes, and the runtime stage copies.
const nextTypeCheckMarker = "/tmp/jd-typecheck-passed"

// nextTypeCheckState is where the check keeps its incremental state, the target
// of the environment's typecheck cache mount.
const nextTypeCheckState = "/tmp/jd-typecheck"

// nextTypeCheckStage ends the stage the source was copied in (prebuild) and
// renders the type check stage from it, then starts the build stage from it
// with the adapter written. The check runs with the build's own mounts and
// defaults, since next typegen loads next.config as next build does.
func nextTypeCheckStage(plan nodeInstallPlan, cache recipeCache, buildSecrets string, bound map[string]bool, prebuild string) []string {
	check := "mkdir -p " + nextTypeCheckState + " && next typegen && node " + nextTypeCheckScript + " && touch " + nextTypeCheckMarker
	return []string{
		"FROM " + prebuild + " AS typecheck",
		"RUN mkdir -p /opt/just-dashboard && printf '%s\\n' '" + nextTypeCheckSource + "' > " + nextTypeCheckScript,
		plan.buildRun(cache.mount("next-typecheck", nextTypeCheckState)+buildSecrets, check, bound),
		"FROM " + prebuild + " AS build",
		"RUN mkdir -p " + nextAdapterPath[:strings.LastIndex(nextAdapterPath, "/")] + " && printf '%s\\n' '" + nextAdapterSource + "' > " + nextAdapterPath,
	}
}

// nextTypeCheckConfigRE matches the next.config settings that keep the
// type check in next build.
var nextTypeCheckConfigRE = regexp.MustCompile(`\b(?:ignoreBuildErrors|tsconfigPath|adapterPath)\b`)

// nextTypeCheckBeside reports whether a Next.js package's type check can
// run beside its build, from what detection reads of it.
func nextTypeCheckBeside(manifest nodeManifest, files nodeRootFiles, config nodeConfigText, configured bool) bool {
	if !manifest.has("typescript") || !files.has("tsconfig.json") || !nextAdapterRelease(manifest.version("next")) {
		return false
	}
	return !configured || !nextTypeCheckConfigRE.MatchString(config.text)
}

// nextAdapterReleaseRE reads the release a version or range starts at:
// 16.4.6, ^16.4.0, ~16.5, >=16.4.
var nextAdapterReleaseRE = regexp.MustCompile(`^\s*(?:[\^~]|>=?)?\s*v?(\d+)\.(\d+)`)

// nextAdapterRelease reports whether a declared Next.js version starts at
// 16.4 or later. A dist-tag (latest, canary) or a range this reads no
// release from does not.
func nextAdapterRelease(spec string) bool {
	match := nextAdapterReleaseRE.FindStringSubmatch(spec)
	if match == nil || strings.Contains(spec, "||") {
		return false
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	return major > 16 || (major == 16 && minor >= 4)
}

// nextBuildCommandRE is a command that is next build itself: values for its
// environment, next build's bin or a runner's exec of it, and options only.
// An argument that is no option (a directory) builds somewhere else.
var nextBuildCommandRE = regexp.MustCompile(`^(?:[A-Za-z_][A-Za-z0-9_]*=(?:'[^']*'|"[^"$` + "`" + `\\]*"|[^\s'"$` + "`" + `\\;&|<>()]*)\s+)*` +
	`(?:npx\s+|bunx\s+|bun\s+x\s+|pnpm\s+exec\s+|yarn\s+exec\s+|npm\s+exec\s+(?:--\s+)?)?` +
	`next\s+build(?:\s+-[-A-Za-z0-9]+(?:=[^\s'"$` + "`" + `\\;&|<>()]*)?)*$`)

// nextBuildScriptRE is a command that runs one of the package's scripts by
// name, with nothing after it.
var nextBuildScriptRE = regexp.MustCompile(`^(?:npm\s+run(?:-script)?|pnpm(?:\s+run)?|yarn(?:\s+run)?|bun(?:\s+--bun)?\s+run(?:\s+--bun)?)\s+([A-Za-z0-9:_.-]+)$`)

// nextBuildIsPlain reports whether a build command runs next build and
// nothing before it: next build itself, or a script of the package's that
// is, followed a few scripts deep. A script with a pre-script is not, since
// npm, Yarn 1 and Bun run it first. Anything else (`generate-types && next
// build`, a monorepo's task runner) is not either, whatever it runs.
func nextBuildIsPlain(scripts map[string]string, command string) bool {
	command = strings.TrimSpace(command)
	for range 4 {
		if nextBuildCommandRE.MatchString(command) {
			return true
		}
		match := nextBuildScriptRE.FindStringSubmatch(command)
		if match == nil {
			return false
		}
		script, ok := scripts[match[1]]
		if _, pre := scripts["pre"+match[1]]; !ok || pre {
			return false
		}
		command = strings.TrimSpace(script)
	}
	return false
}

// buildValueNamed reports whether a build has a value of that name.
func buildValueNamed(secrets []BuildSecretConfig, name string) bool {
	for _, secret := range secrets {
		if secret.Variable == name {
			return true
		}
	}
	return false
}

// hostTypeCheckBeside reports whether this server has the cores and memory
// to type-check beside a build: four cores, and 6 GiB, where a Next.js build
// peaks around 2 GiB (nodeHeavyBuilds) and the check adds up to one more beside
// what the server already runs. A server that cannot read its memory keeps
// the check in next build.
func hostTypeCheckBeside() bool {
	_, total := hostMemoryTotals()
	return typeCheckBesideFits(runtime.NumCPU(), total)
}

// hostCanTypeCheckBeside is the builder's reading of hostTypeCheckBeside.
func (b *ArtifactBuilder) hostCanTypeCheckBeside() bool {
	if b != nil && b.typeCheckBeside != nil {
		return b.typeCheckBeside()
	}
	return hostTypeCheckBeside()
}

func typeCheckBesideFits(cpus int, memory int64) bool {
	return cpus >= 4 && memory >= 6<<30
}
