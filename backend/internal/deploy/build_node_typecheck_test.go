package deploy

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	typeCheckManifest = `{"name":"web","version":"0.1.0","scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"%s"},"devDependencies":{"typescript":"^5"}}`
	typeCheckBunLock  = `{"lockfileVersion":1,"workspaces":{"":{"dependencies":{"next":"%s",},"devDependencies":{"typescript":"^5",},},},"packages":{"next":["next@16.4.6","",{},"x"],"typescript":["typescript@5.9.3","",{},"x"],}}`
)

// typeCheckApp is a Next.js package a type check could run beside: next at
// the given version, TypeScript and a tsconfig.json, with extra files
// replacing or adding to those.
func typeCheckApp(t *testing.T, next string, extra map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"package.json":   strings.Replace(typeCheckManifest, "%s", next, 1),
		"bun.lock":       strings.Replace(typeCheckBunLock, "%s", next, 1),
		"tsconfig.json":  `{"compilerOptions":{"strict":true,"incremental":true}}`,
		"next.config.ts": "export default { cacheComponents: true }\n",
		"app/page.tsx":   "export default function Page() { return null }\n",
	}
	for name, content := range extra {
		if content == "" {
			delete(files, name)
			continue
		}
		files[name] = content
	}
	for name, content := range files {
		writeBuildFixture(t, root, name, content)
	}
	return root
}

func prepareTypeCheckApp(t *testing.T, root string, host bool, config BuildPlanConfig) string {
	t.Helper()
	builder := NewArtifactBuilder(&artifactBackendFake{})
	builder.typeCheckBeside = func() bool { return host }
	config.Method, config.Recipe, config.PackageManager = BuildRecipe, "node", "bun"
	if config.BuildCommand == "" {
		config.BuildCommand = "bun run build"
	}
	if config.StartCommand == "" && config.OutputDirectory == "" {
		config.StartCommand = "bun run start"
	}
	prepared, err := builder.Prepare(context.Background(), root, config, false, "just-dashboard/deployment-7:run-1")
	if err != nil {
		t.Fatal(err)
	}
	return prepared.DockerfilePreview
}

// A Next.js 16.4 TypeScript build type-checks in a stage beside the build:
// the check with the build's own values and its incremental state in the
// environment's cache, the build with next build's own check left out
// through the adapter, and the image waiting for the check.
func TestNextBuildTypeChecksBesideTheBuild(t *testing.T) {
	t.Parallel()
	dockerfile := prepareTypeCheckApp(t, typeCheckApp(t, "16.4.6", nil), true, BuildPlanConfig{
		Secrets: []BuildSecretConfig{{Variable: "DATABASE_URL", Step: "build"}},
	})
	assertDockerfile(t, dockerfile, []string{
		"FROM toolchain AS source\nWORKDIR /app\n",
		"COPY . /app/\nENV PATH=/app/node_modules/.bin:$PATH\nFROM source AS typecheck\n" +
			"RUN mkdir -p /opt/just-dashboard && printf '%s\\n' '" + nextTypeCheckSource + "' > /opt/just-dashboard/typecheck.cjs\nARG JD_BUILD_VALUES_1\n" +
			"RUN --mount=type=cache,id=jd-deployment-7-next-typecheck,target=/tmp/jd-typecheck,sharing=locked " +
			"--mount=type=secret,id=DATABASE_URL,env=DATABASE_URL,required=true mkdir -p /tmp/jd-typecheck && next typegen && node /opt/just-dashboard/typecheck.cjs && touch /tmp/jd-typecheck-passed\n" +
			"FROM source AS build\n" +
			"RUN mkdir -p /opt/just-dashboard && printf '%s\\n' '" + nextAdapterSource + "' > /opt/just-dashboard/next-adapter.mjs\n",
		`NEXT_ADAPTER_PATH="${NEXT_ADAPTER_PATH:-/opt/just-dashboard/next-adapter.mjs}"`,
		"COPY --from=typecheck /tmp/jd-typecheck-passed /tmp/jd-typecheck-passed\nCMD",
	}, nil)
	// The check never names the adapter, which only the build stage has.
	check := dockerfile[strings.Index(dockerfile, "FROM source AS typecheck"):strings.Index(dockerfile, "FROM source AS build")]
	if strings.Contains(check, nextAdapterEnv) {
		t.Fatalf("the type check names the adapter:\n%s", check)
	}
	// The build command's own RUN still ends with the build command, which is
	// where a schema step cuts the file.
	build := dockerfile[strings.Index(dockerfile, "FROM source AS build"):]
	if !strings.Contains(build, "--mount=type=secret,id=DATABASE_URL,env=DATABASE_URL,required=true export ") ||
		!strings.Contains(build, " && bun run build\n") {
		t.Fatalf("the build RUN changed shape:\n%s", build)
	}
}

// The schema step a build that read a missing table gets is cut at the build
// command's RUN of the same file, in the build stage, and never runs the type
// check: its last stage does not depend on it.
func TestSchemaStepCutsTheBuildStageBesideTheTypeCheck(t *testing.T) {
	t.Parallel()
	dockerfile := prepareTypeCheckApp(t, typeCheckApp(t, "16.4.6", nil), true, BuildPlanConfig{
		Secrets: []BuildSecretConfig{{Variable: "DATABASE_URL", Step: "build"}},
	})
	schema, ok := schemaDockerfile(dockerfile, "bun run build", "bunx drizzle-kit migrate", []string{"DATABASE_URL"})
	if !ok {
		t.Fatalf("no schema Dockerfile from:\n%s", dockerfile)
	}
	last := schema[strings.LastIndex(schema, "\nFROM "):]
	if !strings.HasPrefix(last, "\nFROM source AS build\n") || !strings.HasSuffix(last,
		"ARG JD_SCHEMA_RUN\nRUN --mount=type=secret,id=DATABASE_URL,env=DATABASE_URL,required=true bunx drizzle-kit migrate\n") {
		t.Fatalf("the schema step's stage:\n%s", last)
	}
	if strings.Contains(last, "bun run build") || strings.Contains(last, "typecheck") {
		t.Fatalf("the schema step's stage runs the build or the type check:\n%s", last)
	}
}

// Anything that would make the stage check something else than next build
// would keeps next build's own check, with no adapter.
func TestNextBuildKeepsItsOwnTypeCheck(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		next   string
		extra  map[string]string
		host   bool
		config BuildPlanConfig
	}{
		{name: "a server without the room for both", next: "16.4.6", host: false},
		{name: "Next.js before 16.4", next: "16.3.2", host: true},
		{name: "a dist-tag", next: "latest", host: true},
		{name: "no tsconfig.json", next: "16.4.6", host: true, extra: map[string]string{"tsconfig.json": ""}},
		{name: "no TypeScript", next: "16.4.6", host: true, extra: map[string]string{
			"package.json": `{"name":"web","scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16.4.6"}}`,
			"bun.lock":     `{"lockfileVersion":1,"workspaces":{"":{"dependencies":{"next":"16.4.6",},},},"packages":{"next":["next@16.4.6","",{},"x"],}}`,
		}},
		{name: "the project decides about type errors", next: "16.4.6", host: true, extra: map[string]string{
			"next.config.ts": "export default { typescript: { ignoreBuildErrors: process.env.CI === '1' } }\n"}},
		{name: "another tsconfig", next: "16.4.6", host: true, extra: map[string]string{
			"next.config.ts": "export default { typescript: { tsconfigPath: 'tsconfig.build.json' } }\n"}},
		{name: "the project's own adapter", next: "16.4.6", host: true, extra: map[string]string{
			"next.config.ts": "export default { adapterPath: './adapter.mjs' }\n"}},
		{name: "a build value naming an adapter", next: "16.4.6", host: true, config: BuildPlanConfig{
			Secrets: []BuildSecretConfig{{Variable: "NEXT_ADAPTER_PATH", Step: "build"}}}},
		{name: "a static export", next: "16.4.6", host: true, extra: map[string]string{
			"next.config.ts": "export default { output: 'export' }\n"}, config: BuildPlanConfig{OutputDirectory: "out"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dockerfile := prepareTypeCheckApp(t, typeCheckApp(t, test.next, test.extra), test.host, test.config)
			assertDockerfile(t, dockerfile, nil, []string{"typecheck", "next typegen", "next-adapter.mjs", "ignoreBuildErrors"})
		})
	}
}

// typeCheckScripts is typeCheckApp's package.json with these scripts.
func typeCheckScripts(scripts string) string {
	return `{"name":"web","version":"0.1.0","scripts":` + scripts + `,"dependencies":{"next":"16.4.6"},"devDependencies":{"typescript":"^5"}}`
}

// A repository that generates the types its pages import before it
// compiles — in the build script, in a prebuild hook, or in the build
// command — keeps next build's own check: the check beside the build starts
// from the source, which has no generated file, and would fail with TS2307
// where next build passes.
func TestABuildThatGeneratesTypesFirstKeepsNextBuildsCheck(t *testing.T) {
	t.Parallel()
	generated := map[string]string{
		"scripts/generate-types.mjs": "import { writeFileSync } from 'node:fs'\nwriteFileSync('generated/types.ts', 'export type Post = { slug: string }\\n')\n",
		"app/page.tsx":               "import type { Post } from '../generated/types'\nexport default function Page() { const post: Post = { slug: 'a' }; return post.slug }\n",
	}
	for _, test := range []struct {
		name    string
		scripts string
		command string
	}{
		{name: "the build script generates first", scripts: `{"generate-types":"node scripts/generate-types.mjs","build":"bun run generate-types && next build","start":"next start"}`},
		{name: "a prebuild hook generates first", scripts: `{"prebuild":"node scripts/generate-types.mjs","build":"next build","start":"next start"}`},
		{name: "a script it runs has a pre-script", scripts: `{"prebuild:next":"node scripts/generate-types.mjs","build":"bun run build:next","build:next":"next build","start":"next start"}`},
		{name: "the build command generates first", scripts: `{"build":"next build","start":"next start"}`, command: "node scripts/generate-types.mjs && bun run build"},
		{name: "the build runs through a task runner", scripts: `{"build":"turbo run build","start":"next start"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			extra := map[string]string{"package.json": typeCheckScripts(test.scripts)}
			for name, content := range generated {
				extra[name] = content
			}
			dockerfile := prepareTypeCheckApp(t, typeCheckApp(t, "16.4.6", extra), true, BuildPlanConfig{BuildCommand: test.command})
			assertDockerfile(t, dockerfile, nil, []string{"typecheck", "next typegen", "next-adapter.mjs", "ignoreBuildErrors"})
		})
	}
}

// A build that is next build and nothing before it keeps the check beside
// it, through whichever runner and script names it.
func TestAPlainNextBuildTypeChecksBesideIt(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		scripts string
		command string
	}{
		{name: "next build through a script", scripts: `{"build":"next build","start":"next start","postbuild":"next-sitemap"}`},
		{name: "options and a value for its environment", scripts: `{"build":"NODE_OPTIONS='--max-old-space-size=4096' next build --turbopack","start":"next start"}`},
		{name: "a script that runs another", scripts: `{"build":"bun run build:next","build:next":"next build","start":"next start"}`},
		{name: "next build as the command", scripts: `{"build":"bun run generate && next build","start":"next start"}`, command: "bunx next build"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dockerfile := prepareTypeCheckApp(t, typeCheckApp(t, "16.4.6", map[string]string{"package.json": typeCheckScripts(test.scripts)}), true,
				BuildPlanConfig{BuildCommand: test.command})
			assertDockerfile(t, dockerfile, []string{"FROM source AS typecheck\n", "next typegen", "next-adapter.mjs"}, nil)
		})
	}
}

func TestNextBuildIsPlainOnlyWhenNothingRunsBeforeIt(t *testing.T) {
	t.Parallel()
	scripts := map[string]string{
		"build": "next build", "build:turbo": "next build --turbopack", "build:gen": "prisma generate && next build",
		"prebuild:hooked": "node gen.mjs", "build:hooked": "next build", "build:chain": "npm run build:turbo",
		"loop": "npm run loop", "build:dir": "next build apps/web", "build:env": `NEXT_TELEMETRY_DISABLED=1 ANALYZE="" next build`,
		"build:subst": "NODE_OPTIONS=$(node opts.js) next build",
	}
	for command, want := range map[string]bool{
		"next build": true, "next build --turbopack --debug": true, "npx next build": true, "bunx next build": true,
		"bun x next build": true, "pnpm exec next build": true, "yarn exec next build": true, "npm exec -- next build": true,
		"npm run build": true, "npm run-script build": true, "pnpm build": true, "pnpm run build": true, "yarn build": true,
		"yarn run build": true, "bun run build": true, "bun --bun run build": true, "bun run --bun build": true,
		"bun run build:chain": true, "bun run build:env": true,
		"": false, "bun run build:gen": false, "bun run build:hooked": false, "bun run loop": false, "bun run missing": false,
		"bun run build:dir": false, "bun run build:subst": false, "bun build": false, "next build; rm -rf /": false,
		"next build && next export": false, "bun run build -- --turbopack": false, "next dev": false,
		"NODE_OPTIONS=`id` next build": false,
	} {
		if got := nextBuildIsPlain(scripts, command); got != want {
			t.Errorf("nextBuildIsPlain(%q) = %v, want %v", command, got, want)
		}
	}
	// A prebuild hook makes bun run build run it first.
	if nextBuildIsPlain(map[string]string{"prebuild": "node gen.mjs", "build": "next build"}, "bun run build") {
		t.Error("a prebuild hook was left out")
	}
}

func TestNextAdapterReleaseReadsWhereARangeStarts(t *testing.T) {
	t.Parallel()
	for spec, want := range map[string]bool{
		"16.4.6": true, "^16.4.0": true, "~16.5.1": true, ">=16.4": true, "v16.4.0": true, "17.0.0-canary.3": true,
		"16.3.9": false, "^16.0.0": false, "15.5.4": false, "latest": false, "canary": false, "": false,
		"^15.0.0 || ^16.4.0": false, "workspace:*": false,
	} {
		if got := nextAdapterRelease(spec); got != want {
			t.Errorf("nextAdapterRelease(%q) = %v, want %v", spec, got, want)
		}
	}
}

func TestTypeCheckBesideNeedsFourCoresAndSixGiB(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		cpus   int
		memory int64
		want   bool
	}{
		{4, 6 << 30, true}, {12, 47 << 30, true}, {2, 16 << 30, false}, {8, 4 << 30, false}, {8, 0, false},
	} {
		if got := typeCheckBesideFits(test.cpus, test.memory); got != test.want {
			t.Errorf("typeCheckBesideFits(%d, %d) = %v, want %v", test.cpus, test.memory, got, test.want)
		}
	}
}

// The stage writes the check with printf '%s\n' '…' in one instruction, so a
// line break or a single quote in it would cut the script short.
func TestNextTypeCheckSourceIsOnePrintfArgument(t *testing.T) {
	t.Parallel()
	for name, source := range map[string]string{"the check": nextTypeCheckSource, "the adapter": nextAdapterSource} {
		if strings.ContainsAny(source, "'\n\r") {
			t.Errorf("%s has a single quote or a line break:\n%s", name, source)
		}
	}
}

const (
	// typeCheckCommandManifest is the shape of typescript@7: an ES module
	// package whose main says its version, with a command and no API.
	typeCheckCommandManifest = `{"name":"typescript","version":"7.0.2","type":"module","bin":{"tsc":"./bin/tsc"},"exports":{"./package.json":"./package.json",".":"./lib/version.cjs"}}`
	// typeCheckAPIManifest is the shape of typescript@5 and 6.
	typeCheckAPIManifest = `{"name":"typescript","version":"5.9.3","main":"./lib/typescript.js","bin":{"tsc":"./bin/tsc"}}`
	// typeCheckCommand stands in for tsc: it records its arguments, answers
	// --showConfig from JD_FAKE_CONFIG and otherwise prints JD_FAKE_ERROR, when
	// there is one, and fails as tsc does on an error.
	typeCheckCommand = `const fs = require("fs"); const args = process.argv.slice(2); fs.appendFileSync("calls.jsonl", JSON.stringify(args) + "\n"); ` +
		`if (args.includes("--showConfig")) { process.stdout.write(process.env.JD_FAKE_CONFIG || "{}") } else if (process.env.JD_FAKE_ERROR) { console.log(process.env.JD_FAKE_ERROR); process.exit(2) }`
	// typeCheckAPI stands in for lib/typescript.js with the calls the check
	// makes: it records the program's options and reports JD_FAKE_ERROR, when
	// there is one, in the file JD_FAKE_ERROR_FILE.
	typeCheckAPI = `const fs = require("fs"); const failing = process.env.JD_FAKE_ERROR ? [{ category: 1, code: 2322, messageText: process.env.JD_FAKE_ERROR, file: { fileName: process.env.JD_FAKE_ERROR_FILE } }] : []; ` +
		`module.exports = { sys: {}, DiagnosticCategory: { Error: 1 }, flattenDiagnosticMessageText: (text) => String(text), ` +
		`getParsedCommandLineOfConfigFile: () => ({ fileNames: ["/app/app/page.tsx"], options: { strict: true }, errors: [] }), ` +
		`createIncrementalProgram: (program) => { fs.appendFileSync("calls.jsonl", JSON.stringify(["api", program.options.tsBuildInfoFile, program.options.noEmit]) + "\n"); return { emit: () => ({ diagnostics: [] }) } }, ` +
		`getPreEmitDiagnostics: () => failing, formatDiagnostics: (found) => found.map((d) => d.file.fileName + "(1,7): error TS" + d.code + ": " + d.messageText + "\n").join("") }`
)

// runTypeCheckScript runs the check as the stage does, under Node.js, in an
// application whose node_modules/typescript holds the given files, and
// returns what it printed, whether it passed and the calls it made.
func runTypeCheckScript(t *testing.T, node string, typescript map[string]string, env ...string) (string, bool, [][]any) {
	t.Helper()
	root := t.TempDir()
	writeBuildFixture(t, root, "tsconfig.json", `{"compilerOptions":{"strict":true}}`)
	for name, content := range typescript {
		writeBuildFixture(t, root, filepath.Join("node_modules", "typescript", name), content)
	}
	script := filepath.Join(t.TempDir(), "typecheck.cjs")
	if err := os.WriteFile(script, []byte(nextTypeCheckSource+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(node, script)
	command.Dir = root
	command.Env = append(os.Environ(), env...)
	output, err := command.CombinedOutput()
	var calls [][]any
	recorded, _ := os.ReadFile(filepath.Join(root, "calls.jsonl"))
	for _, line := range strings.Split(strings.TrimSpace(string(recorded)), "\n") {
		if line == "" {
			continue
		}
		var call []any
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			t.Fatalf("a recorded call %q: %v", line, err)
		}
		calls = append(calls, call)
	}
	return string(output), err == nil, calls
}

// The check asks the application's TypeScript which face it has, as next
// build does. typescript@7 has a command and no API: the stage used to call
// the API regardless and failed every such deploy with "is not a function".
func TestNextTypeCheckScriptUsesWhatTypeScriptHas(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("the type check script runs under Node.js")
	}
	command := map[string]string{
		"package.json": typeCheckCommandManifest, "lib/version.cjs": `module.exports = { version: "7.0.2", versionMajorMinor: "7.0" }`,
		// An ES module package: its extensionless bin is not what runs.
		"bin/tsc": `throw new Error("the extensionless bin ran")`, "lib/tsc.js": `import { createRequire } from "node:module"; createRequire(import.meta.url)("./fake.cjs")`,
		"lib/fake.cjs": typeCheckCommand,
	}
	api := map[string]string{
		"package.json": typeCheckAPIManifest, "lib/typescript.js": typeCheckAPI,
		"bin/tsc": `require("fs").appendFileSync("calls.jsonl", JSON.stringify(["the command ran"]) + "\n")`,
	}
	// What next build's own run of the command passes
	// (runTypeCheckCli.js), and --pretty false.
	check := []any{"--noEmit", "--declarationMap", "false", "--emitDeclarationOnly", "false", "--pretty", "false"}
	incremental := append(slices.Clone(check), "--tsBuildInfoFile", nextTypeCheckState+"/cli.tsbuildinfo")
	for _, test := range []struct {
		name       string
		typescript map[string]string
		env        []string
		passes     bool
		prints     []string
		calls      [][]any
	}{
		{name: "the command, types fine", typescript: command, env: []string{`JD_FAKE_CONFIG={"compilerOptions":{"incremental":true}}`}, passes: true,
			calls: [][]any{{"--showConfig", "--pretty", "false"}, incremental}},
		{name: "the command, a project that is not incremental keeps no state", typescript: command, env: []string{`JD_FAKE_CONFIG={"compilerOptions":{"strict":true}}`}, passes: true,
			calls: [][]any{{"--showConfig", "--pretty", "false"}, check}},
		{name: "the command, a type error", typescript: command, env: []string{`JD_FAKE_CONFIG={"compilerOptions":{"composite":true}}`,
			"JD_FAKE_ERROR=app/page.tsx(1,7): error TS2322: Type 'string' is not assignable to type 'number'."},
			prints: []string{"app/page.tsx(1,7): error TS2322: Type 'string' is not assignable to type 'number'.", "Failed to type check."},
			calls:  [][]any{{"--showConfig", "--pretty", "false"}, incremental}},
		{name: "the command, a configuration it cannot read", typescript: command, env: []string{"JD_FAKE_CONFIG=error TS5083: Cannot read file."},
			prints: []string{"Could not parse the output of tsc --showConfig"}, calls: [][]any{{"--showConfig", "--pretty", "false"}}},
		{name: "a command an aliased package renamed", typescript: map[string]string{
			"package.json": `{"name":"@typescript/typescript7","version":"7.0.2","bin":{"tsc7":"./lib/renamed.js"}}`, "lib/renamed.js": typeCheckCommand},
			env: []string{`JD_FAKE_CONFIG={"compilerOptions":{}}`}, passes: true, calls: [][]any{{"--showConfig", "--pretty", "false"}, check}},
		{name: "neither an API nor a command", typescript: map[string]string{"package.json": `{"name":"typescript","version":"7.9.0"}`},
			prints: []string{"TypeScript 7.9.0 has neither the compiler API nor a tsc command"}},
		{name: "the API, types fine", typescript: api, passes: true, calls: [][]any{{"api", nextTypeCheckState + "/tsbuildinfo", true}}},
		{name: "the API, a type error", typescript: api, env: []string{"JD_FAKE_ERROR=Type 'string' is not assignable to type 'number'.", "JD_FAKE_ERROR_FILE=/app/app/page.tsx"},
			prints: []string{"/app/app/page.tsx(1,7): error TS2322: Type 'string' is not assignable to type 'number'."},
			calls:  [][]any{{"api", nextTypeCheckState + "/tsbuildinfo", true}}},
		{name: "the API, a type error in a test file", typescript: api, env: []string{"JD_FAKE_ERROR=Type 'string' is not assignable to type 'number'.", "JD_FAKE_ERROR_FILE=/app/app/page.test.tsx"},
			passes: true, calls: [][]any{{"api", nextTypeCheckState + "/tsbuildinfo", true}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			output, passed, calls := runTypeCheckScript(t, node, test.typescript, test.env...)
			if passed != test.passes {
				t.Fatalf("passed = %v, want %v:\n%s", passed, test.passes, output)
			}
			for _, want := range test.prints {
				if !strings.Contains(output, want) {
					t.Errorf("the output does not say %q:\n%s", want, output)
				}
			}
			if len(calls) != len(test.calls) {
				t.Fatalf("calls = %v, want %v:\n%s", calls, test.calls, output)
			}
			for index, want := range test.calls {
				got := calls[index]
				// The command is given the project by its whole path, which
				// is the test's own folder.
				if at := slices.Index(got, "--project"); at >= 0 && at+1 < len(got) {
					if project, _ := got[at+1].(string); filepath.Base(project) != "tsconfig.json" || !filepath.IsAbs(project) {
						t.Errorf("call %d names the project %q", index, project)
					}
					got = slices.Delete(slices.Clone(got), at, at+2)
				}
				if !slices.Equal(got, want) {
					t.Errorf("call %d = %v, want %v", index, got, want)
				}
			}
		})
	}
}
