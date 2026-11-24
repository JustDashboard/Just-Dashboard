package deploy

import (
	"context"
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
