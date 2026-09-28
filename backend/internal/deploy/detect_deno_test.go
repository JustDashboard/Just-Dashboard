package deploy

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// denoPlan detects a Deno tree, prepares the proposed plan and runs
// preflight over it.
func denoPlan(t *testing.T, files map[string]string, backend BuildBackend) (DetectedCandidate, PreparedBuild, []PreflightFinding) {
	t.Helper()
	result, candidate := detectNodeTree(t, files)
	if candidate.Recipe != "deno" {
		t.Fatalf("selected %s/%s: %+v", candidate.Recipe, candidate.Framework, result.Candidates)
	}
	build := BuildPlanConfig{Method: BuildRecipe, Recipe: "deno", BuildCommand: candidate.BuildCommand, StartCommand: candidate.StartCommand}
	if backend == nil {
		backend = &artifactBackendFake{}
	}
	prepared, err := NewArtifactBuilder(backend).Prepare(context.Background(), writeNodeTree(t, files), build, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	return candidate, prepared, preflightFindings(nodeDraft(result), nodeTestConfiguration(build), dockerHost, false)
}

// A stale deno.lock is the Deno twin of the npm incident: detected before
// Deploy, and installed without --frozen rather than stopping on "The
// lockfile is out of date".
func TestDenoLockStaleOrInSync(t *testing.T) {
	t.Parallel()
	inSync := map[string]string{
		"deno.json": `{"tasks":{"start":"deno run -A main.ts"},"imports":{"@std/fmt":"jsr:@std/fmt@^1.0.0","@std/assert":"jsr:@std/assert@^1.0.3","zod":"npm:zod@~3.23.0","@std/path/":"jsr:@std/path@^1.0.0/"}}`,
		"deno.lock": `{"version":"5","specifiers":{},"workspace":{"dependencies":["jsr:@std/assert@^1.0.3","jsr:@std/fmt@1","jsr:@std/path@1","npm:zod@3.23"]}}`,
		"main.ts":   "Deno.serve(() => new Response('x'))",
	}
	candidate, prepared, findings := denoPlan(t, inSync, nil)
	if len(candidate.Deno.LockStale) != 0 || findingByCode(findings, "deno_lock_outdated") != nil {
		t.Fatalf("an in-sync lock was called stale: %+v", candidate.Deno)
	}
	assertDockerfile(t, prepared.DockerfilePreview, []string{"RUN deno install --frozen\n", "RUN deno install --entrypoint main.ts\n"}, nil)

	stale := map[string]string{
		"deno.json":    inSync["deno.json"],
		"deno.lock":    `{"version":"4","workspace":{"dependencies":["jsr:@std/fmt@1"],"packageJson":{"dependencies":["npm:hono@^4.6.0"]}}}`,
		"package.json": `{"dependencies":{"hono":"^4.6.0","chalk":"^5.0.0"}}`,
		"main.ts":      inSync["main.ts"],
	}
	candidate, prepared, findings = denoPlan(t, stale, nil)
	if !slices.Equal(candidate.Deno.LockStale, []string{"jsr:@std/assert@^1.0.3", "jsr:@std/path@1", "npm:chalk@5", "npm:zod@3.23"}) {
		t.Fatalf("stale = %v", candidate.Deno.LockStale)
	}
	assertDockerfile(t, prepared.DockerfilePreview, []string{"RUN deno install\n"}, []string{"--frozen"})
	if outdated := findingByCode(findings, "deno_lock_outdated"); outdated == nil || outdated.Severity != PreflightWarning ||
		outdated.Title != "deno.lock does not match the project's dependencies" {
		t.Fatalf("deno_lock_outdated = %+v", outdated)
	}
	// A lock before version 3 records no workspace and is not judged.
	stale["deno.lock"] = `{"version":"2","remote":{}}`
	candidate, _, _ = denoPlan(t, stale, nil)
	if len(candidate.Deno.LockStale) != 0 {
		t.Fatalf("a version 2 lock was judged: %v", candidate.Deno.LockStale)
	}
}

// The Deno release is the repository's own declaration, pinned to an exact
// image; nothing declared builds on the catalogue's reviewed release.
func TestDenoRuntimeVersionPinned(t *testing.T) {
	t.Parallel()
	app := map[string]string{"deno.json": `{"tasks":{"start":"deno run --allow-net main.ts"}}`, "main.ts": "Deno.serve(() => new Response('x'))"}
	with := func(name, content string) map[string]string {
		files := map[string]string{name: content}
		for key, value := range app {
			files[key] = value
		}
		return files
	}
	for _, fixture := range []struct {
		name, file, content, image, toolchain, install, declared string
	}{
		{"default", "", "", "denoland/deno:alpine-2.9.7@", "deno 2.9.7", "RUN deno install\n", ""},
		{".dvmrc deno 1", ".dvmrc", "1.46.3\n", "denoland/deno:alpine-1.46.3@", "deno 1.46.3 (.dvmrc)", "RUN deno cache main.ts\n", ""},
		{".tool-versions", ".tool-versions", "nodejs 22.11.0\ndeno 2.5.6\n", "denoland/deno:alpine-2.5.6@", "deno 2.5.6 (.tool-versions)", "RUN deno install\n", ""},
		{"setup-deno major", ".github/workflows/ci.yml", "steps:\n  - uses: denoland/setup-deno@v2\n    with:\n      deno-version: v1.x\n", "denoland/deno:alpine-1.46.3@", "deno 1.46.3 (ci.yml)", "RUN deno cache main.ts\n", ""},
		{"a major the catalogue lacks", ".dvmrc", "3.0.0\n", "denoland/deno:alpine-2.9.7@", "deno 2.9.7", "RUN deno install\n", "3.0.0"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			files := app
			if fixture.file != "" {
				files = with(fixture.file, fixture.content)
			}
			candidate, prepared, findings := denoPlan(t, files, nil)
			assertDockerfile(t, prepared.DockerfilePreview, []string{"FROM " + fixture.image, fixture.install}, nil)
			if prepared.Toolchain != fixture.toolchain || candidate.Deno.Declared != fixture.declared ||
				(fixture.declared != "") != (findingByCode(findings, "deno_version_mismatch") != nil) {
				t.Fatalf("toolchain %q, deno %+v", prepared.Toolchain, candidate.Deno)
			}
		})
	}
	// A declared release with no image falls back to its major's reviewed
	// release, with a note, rather than failing on a registry lookup.
	_, prepared, _ := denoPlan(t, with(".dvmrc", "2.0.99\n"), &missingImageBackend{missing: "denoland/deno:alpine-2.0.99"})
	if prepared.Toolchain != "deno 2.9.7 (.dvmrc)" || !slices.ContainsFunc(prepared.Notes, func(note string) bool {
		return strings.Contains(note, "has no denoland/deno:alpine-2.0.99 image")
	}) {
		t.Fatalf("prepared = %q %v", prepared.Toolchain, prepared.Notes)
	}
}

// Fresh 1's start task is its development server; the preview task, or the
// main.ts it runs, serves what the build task wrote.
func TestDenoFresh1StartsThePreview(t *testing.T) {
	t.Parallel()
	fresh := func(tasks string) map[string]string {
		return map[string]string{
			"deno.json": `{"tasks":{` + tasks + `},"imports":{"$fresh/":"https://deno.land/x/fresh@1.7.3/"}}`,
			"main.ts":   `import { start } from "$fresh/server.ts"; await start(manifest)`, "dev.ts": "",
		}
	}
	for _, fixture := range []struct{ tasks, start string }{
		{`"start":"deno run -A --watch=static/,routes/ dev.ts","build":"deno run -A dev.ts build","preview":"deno run -A main.ts"`, "deno task preview"},
		{`"start":"deno run -A --watch=static/,routes/ dev.ts","build":"deno run -A dev.ts build"`, "deno run -A main.ts"},
	} {
		candidate, prepared, findings := denoPlan(t, fresh(fixture.tasks), nil)
		if candidate.Framework != "fresh" || candidate.StartCommand != fixture.start || candidate.BuildCommand != "deno task build" ||
			candidate.Deno.WatchStart || findingByCode(findings, "deno_start_watch_mode") != nil {
			t.Fatalf("fresh = %q %+v", candidate.StartCommand, candidate.Deno)
		}
		assertDockerfile(t, prepared.DockerfilePreview, []string{"RUN deno install --entrypoint main.ts\n", "RUN deno task build\n"}, nil)
	}
	files := fresh(`"start":"deno run -A --watch=static/,routes/ dev.ts"`)
	delete(files, "main.ts")
	files["server.tsx"] = ""
	candidate, _, findings := denoPlan(t, files, nil)
	if candidate.StartCommand != "deno task start" || !candidate.Deno.WatchStart || findingByCode(findings, "deno_start_watch_mode") == nil {
		t.Fatalf("watch = %q %+v", candidate.StartCommand, candidate.Deno)
	}
}

// A Deno 2 project that keeps its dependencies and scripts in package.json
// is Deno's, not a Node candidate that would run deno on the Node image.
func TestDenoPackageJSONProjects(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"package.json": `{"name":"api","scripts":{"start":"deno run -A main.ts"},"dependencies":{"hono":"^4.6.0"}}`,
		"deno.lock":    `{"version":"5","workspace":{"packageJson":{"dependencies":["npm:hono@^4.6.0"]}}}`,
		"main.ts":      `import { Hono } from "hono"; Deno.serve(new Hono().fetch)`,
	}
	result, candidate := detectNodeTree(t, files)
	node := slices.IndexFunc(result.Candidates, func(other DetectedCandidate) bool { return other.Recipe == "node" })
	if candidate.Recipe != "deno" || candidate.StartCommand != "deno task start" || node < 0 || result.Candidates[node].Confidence != ConfidenceLow {
		t.Fatalf("candidates = %+v", result.Candidates)
	}
	_, prepared, _ := denoPlan(t, files, nil)
	assertDockerfile(t, prepared.DockerfilePreview, []string{"RUN deno install --frozen\n", "RUN deno install --entrypoint main.ts\n", `CMD ["/bin/sh","-c","deno task start"]`}, nil)
	// A Node project whose build calls deno on the way stays a Node project.
	result, _ = detectNodeTree(t, map[string]string{"package.json": `{"scripts":{"build":"deno task gen && vite build","start":"vite preview"},"devDependencies":{"vite":"^7"}}`})
	if slices.ContainsFunc(result.Candidates, func(other DetectedCandidate) bool { return other.Recipe == "deno" }) {
		t.Fatalf("a Node build that calls deno became a Deno project: %+v", result.Candidates)
	}
	if _, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(),
		writeNodeTree(t, map[string]string{"package.json": `{"scripts":{"start":"node x.js"}}`, "package-lock.json": "{}"}),
		BuildPlanConfig{Method: BuildRecipe, Recipe: "deno", StartCommand: "deno task start"}, false, "t:1"); !errors.Is(err, ErrUnsupportedBuilder) {
		t.Fatalf("a Node project was built with the Deno recipe: %v", err)
	}
}

// The entry's graph is cached after the build, which may write a file it
// imports, and only when the checkout has it: Fresh 2's start serves what
// its build writes, whose imports deno.json already names.
func TestDenoEntryCachedAfterTheBuild(t *testing.T) {
	t.Parallel()
	fresh2 := map[string]string{
		"deno.json": `{"tasks":{"dev":"vite","build":"vite build","start":"deno serve -A _fresh/server.js"},"imports":{"fresh":"jsr:@fresh/core@^2.0.0","vite":"npm:vite@^7.1.3"}}`,
		"main.ts":   `import { App } from "fresh"; export const app = new App();`, "vite.config.ts": "",
	}
	candidate, prepared, _ := denoPlan(t, fresh2, nil)
	if candidate.Framework != "fresh" || candidate.StartCommand != "deno task start" || slices.ContainsFunc(candidate.Evidence, func(evidence DetectionEvidence) bool {
		return strings.HasPrefix(evidence.Reason, "caches ")
	}) {
		t.Fatalf("fresh 2 = %+v", candidate)
	}
	assertDockerfile(t, prepared.DockerfilePreview, []string{"RUN deno install\nRUN deno task build\nCMD "}, []string{"--entrypoint", "deno cache"})

	app := map[string]string{"deno.json": `{"tasks":{"build":"deno run -A gen.ts","start":"deno run -A main.ts"}}`, "main.ts": `import "./routes.gen.ts";`}
	_, prepared, _ = denoPlan(t, app, nil)
	assertDockerfile(t, prepared.DockerfilePreview, []string{"RUN deno install\nRUN deno task build\nRUN deno install --entrypoint main.ts\nCMD "}, nil)
	app[".dvmrc"] = "1.46.3\n"
	_, prepared, _ = denoPlan(t, app, nil)
	assertDockerfile(t, prepared.DockerfilePreview, []string{"COPY . .\nRUN deno task build\nRUN deno cache main.ts\nCMD "}, []string{"deno install"})
}

// The file the start command runs is cached with its whole module graph,
// URL and npm: imports written only in code included.
func TestDenoCommandEntry(t *testing.T) {
	t.Parallel()
	tasks := func(name string) string {
		return map[string]string{"start": "deno serve --port 3000 -A ./src/server.ts", "dev": "deno task start"}[name]
	}
	for command, want := range map[string]string{
		"deno task start": "src/server.ts", "deno task dev": "src/server.ts", "deno run --allow-net=0.0.0.0 main.tsx": "main.tsx",
		"deno run -A jsr:@std/http/file-server": "", "deno task missing": "", "./start.sh": "",
		"deno run -A ../outside.ts": "",
	} {
		if got := denoCommandEntry(command, tasks); got != want {
			t.Errorf("%q = %q, want %q", command, got, want)
		}
	}
}
