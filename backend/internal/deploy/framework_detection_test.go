package deploy

import (
	"path/filepath"
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
