package deploy

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestFrameworkDetectionSelectsServingRuntimeAndActualBuildFile(t *testing.T) {
	for _, fixture := range []struct {
		name, manifest, framework, output, start string
		port                                     int
	}{
		{"vite", `{"scripts":{"build":"vite build","start":"vite --port 5173"},"devDependencies":{"vite":"8"}}`, "vite", "dist", "", 80},
		{"svelte node", `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"8","@sveltejs/kit":"2","@sveltejs/adapter-node":"5"}}`, "sveltekit", "", "node build", 3000},
		{"svelte static", `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"8","@sveltejs/kit":"2","@sveltejs/adapter-static":"3"}}`, "sveltekit", "build", "", 80},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := t.TempDir()
			writeBuildFixture(t, root, "package.json", fixture.manifest)
			writeBuildFixture(t, root, "package-lock.json", `{}`)
			result, err := (Detector{}).DetectPath(t.Context(), root, SourceIdentity{})
			if err != nil || len(result.Candidates) != 1 {
				t.Fatalf("detect: %+v, %v", result, err)
			}
			candidate := result.Candidates[0]
			if candidate.Framework != fixture.framework || candidate.OutputDirectory != fixture.output || candidate.StartCommand != fixture.start || candidate.Port != fixture.port {
				t.Fatalf("incorrect runtime defaults: %+v", candidate)
			}
		})
	}
	root := t.TempDir()
	writeBuildFixture(t, root, "public/index.html", "static fixture")
	writeBuildFixture(t, root, "apps/web/Containerfile", "FROM scratch")
	result, err := (Detector{}).DetectPath(t.Context(), root, SourceIdentity{})
	if err != nil || len(result.Candidates) != 2 {
		t.Fatalf("detect: %+v, %v", result, err)
	}
	for _, candidate := range result.Candidates {
		switch candidate.BuildMethod {
		case BuildStatic:
			if candidate.OutputDirectory != "" || candidate.Port != 80 {
				t.Fatal(candidate)
			}
		case BuildDockerfile:
			if filepath.ToSlash(filepath.Join(candidate.Root, candidate.Dockerfile)) != "apps/web/Containerfile" {
				t.Fatal(candidate)
			}
		}
	}
}

// What detection still owes the operator, now that a decision is what decides
// which of `/deploy/new`'s four screens opens.
//
// Every one of these used to carry one unconditionally: a Dockerfile owed
// "confirm container port and readiness check" even where its single EXPOSE
// had answered it, a Go module owed an executable and a start command the
// recipe resolves for itself, a Deno project owed the port it had just been
// given, and a static site owed a public directory that is its own candidate
// root. A decision on any of them sent the reader to the first screen to read
// a sentence with no field under it — so the two-press import the browser
// suite covers was unreachable for most real repositories, and no test here
// said so because none of them asserted this list.
func TestDetectionOnlyOwesDecisionsItCannotAnswer(t *testing.T) {
	for _, fixture := range []struct {
		name     string
		files    map[string]string
		port     int
		evidence string
	}{
		{"dockerfile with one exposed port", map[string]string{"Dockerfile": "FROM nginx\nEXPOSE 8080\n"}, 8080, "EXPOSE 8080/tcp"},
		{"dockerfile naming no port", map[string]string{"Dockerfile": "FROM nginx\n"}, 0, ""},
		{"dockerfile naming several ports", map[string]string{"Dockerfile": "FROM nginx\nEXPOSE 80\nEXPOSE 8443\n"}, 0, ""},
		{"go module", map[string]string{"go.mod": "module example.com/app\n\ngo 1.26\n"}, 0, ""},
		{"deno project", map[string]string{"deno.json": `{"tasks":{"start":"deno run -A main.ts"}}`}, 8000, "Deno.serve default port 8000"},
		{"static site at the root", map[string]string{"index.html": "<html></html>"}, 80, ""},
		{"static site under public", map[string]string{"public/index.html": "<html></html>"}, 80, ""},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := t.TempDir()
			for path, content := range fixture.files {
				writeBuildFixture(t, root, path, content)
			}
			result, err := (Detector{}).DetectPath(t.Context(), root, SourceIdentity{})
			if err != nil || len(result.Candidates) != 1 {
				t.Fatalf("detect: %+v, %v", result, err)
			}
			candidate := result.Candidates[0]
			if len(candidate.NeedsDecision) != 0 {
				t.Fatalf("detection owes a decision it can answer itself: %#v", candidate.NeedsDecision)
			}
			if candidate.Port != fixture.port {
				t.Fatalf("port = %d, want %d", candidate.Port, fixture.port)
			}
			if fixture.evidence == "" {
				return
			}
			reasons := ""
			for _, item := range candidate.Evidence {
				reasons += item.Reason + "\n"
			}
			if !strings.Contains(reasons, fixture.evidence) {
				t.Fatalf("evidence = %q, want it to carry %q", reasons, fixture.evidence)
			}
		})
	}
}
