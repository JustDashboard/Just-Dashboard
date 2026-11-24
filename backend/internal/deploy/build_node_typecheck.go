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
// (`next typegen`), then runs next build's own check on TypeScript's API
// (nextTypeCheckSource) incrementally, its state in a cache mount of the
// environment's; the build
// runs next build with the type check left out, through a build adapter
// (NEXT_ADAPTER_PATH, which Next.js 16.4 reads as adapterPath) whose
// modifyConfig sets typescript.ignoreBuildErrors. The runtime stage copies
// an empty file from the type check stage, so the image waits for it and a
// type error still fails the build, as `error TSnnnn` (build_type_error).
//
// Only where that is the same check next build would make:
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

// nextTypeCheckSource is the check: what next build's own type check
// (next/dist/lib/typescript/runTypeCheck.js) does, on TypeScript's public
// API, after next typegen has written next build's settings into
// tsconfig.json. The files tsconfig.json names, less .next/dev/types; its
// options with nothing emitted, incrementally, the state in
// nextTypeCheckState; its errors, less a configuration with no inputs
// (TS18003) and anything in a __tests__ or __mocks__ folder or a .test. or
// .spec. file, which next build leaves out too. Errors print as
// `file(line,col): error TSnnnn: …`. One line, like the adapter.
const nextTypeCheckSource = `const path = require("path"); const ts = require(require.resolve("typescript", { paths: [process.cwd()] })); ` +
	`const config = ts.getParsedCommandLineOfConfigFile(path.resolve("tsconfig.json"), {}, { ...ts.sys, onUnRecoverableConfigFileDiagnostic: (d) => { throw new Error(ts.flattenDiagnosticMessageText(d.messageText, "\n")) } }); ` +
	`const devTypes = path.resolve(".next/dev/types"); const rootNames = config.fileNames.filter((name) => !name.startsWith(devTypes)); ` +
	`const program = ts.createIncrementalProgram({ rootNames, options: { ...config.options, composite: false, incremental: true, declarationMap: false, emitDeclarationOnly: false, noEmit: true, tsBuildInfoFile: "` + nextTypeCheckState + `/tsbuildinfo" } }); ` +
	`const emitted = rootNames.length > 0 ? program.emit() : { diagnostics: [] }; const ignored = /[\\/]__(?:tests|mocks)__[\\/]|(?<=[\\/.])(?:spec|test)\.[^\\/]+$/; ` +
	`const found = config.errors.filter((d) => d.code !== 18003).concat(rootNames.length > 0 ? ts.getPreEmitDiagnostics(program).concat(emitted.diagnostics) : []); ` +
	`const errors = found.filter((d) => d.category === ts.DiagnosticCategory.Error && !(d.file && ignored.test(d.file.fileName))); ` +
	`if (errors.length > 0) { process.stderr.write(ts.formatDiagnostics(errors, { getCanonicalFileName: (name) => name, getCurrentDirectory: () => process.cwd(), getNewLine: () => "\n" })); process.exit(1) }`

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
