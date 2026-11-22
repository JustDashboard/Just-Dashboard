package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func baseResolutionFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeBuildFixture(t, root, "package.json", `{"scripts":{"start":"node server.js"}}`)
	writeBuildFixture(t, root, "package-lock.json", "{}")
	return root
}

var nodeRecipeConfig = BuildPlanConfig{Method: BuildRecipe, Recipe: "node", StartCommand: "npm start"}

// Within ten minutes a base is not asked about again; after them it is.
func TestReviewedBasesAreResolvedOncePerTenMinutes(t *testing.T) {
	t.Parallel()
	root := baseResolutionFixture(t)
	backend := &artifactBackendFake{}
	builder := NewArtifactBuilder(backend)
	clock := time.Date(2026, 11, 22, 14, 2, 0, 0, time.UTC)
	builder.bases.now = func() time.Time { return clock }
	prepare := func() PreparedBuild {
		t.Helper()
		prepared, err := builder.Prepare(context.Background(), root, nodeRecipeConfig, false, "just-dashboard/deployment-1:run-1")
		if err != nil {
			t.Fatal(err)
		}
		return prepared
	}
	first := prepare()
	asked := len(backend.resolves)
	if asked == 0 || !slices.Contains(backend.resolves, "node:22-alpine") {
		t.Fatalf("first preparation resolved %v", backend.resolves)
	}
	clock = clock.Add(9 * time.Minute)
	second := prepare()
	if len(backend.resolves) != asked {
		t.Fatalf("a fresh answer was asked again: %v", backend.resolves)
	}
	if second.DockerfilePreview != first.DockerfilePreview || !slices.Equal(second.BaseImages[0].Platforms, first.BaseImages[0].Platforms) {
		t.Fatal("a remembered answer rendered another Dockerfile")
	}
	clock = clock.Add(2 * time.Minute)
	prepare()
	if len(backend.resolves) != 2*asked {
		t.Fatalf("an answer older than ten minutes was not asked again: %v", backend.resolves)
	}
}

// A registry that does not answer leaves the last answer in use, with a
// note on the run that says so and when it was given; one that answers
// "no such tag" is believed.
func TestARegistryOutageFallsBackToTheLastAnswerWithANote(t *testing.T) {
	t.Parallel()
	root := baseResolutionFixture(t)
	backend := &artifactBackendFake{}
	builder := NewArtifactBuilder(backend)
	clock := time.Date(2026, 11, 22, 14, 2, 0, 0, time.UTC)
	builder.bases.now = func() time.Time { return clock }
	if _, err := builder.Prepare(context.Background(), root, nodeRecipeConfig, false, "t:1"); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(3 * time.Hour)
	for _, outage := range []string{
		`Get "https://registry-1.docker.io/v2/": dial tcp: lookup registry-1.docker.io: no such host`,
		"toomanyrequests: You have reached your unauthenticated pull rate limit",
		"received unexpected HTTP status: 503 Service Unavailable",
	} {
		backend.resolveErr = errors.New(outage)
		prepared, err := builder.Prepare(context.Background(), root, nodeRecipeConfig, false, "t:1")
		if err != nil {
			t.Fatalf("%s: a deploy failed only because the registry did not answer: %v", outage, err)
		}
		if !strings.Contains(prepared.DockerfilePreview, "node:22-alpine@"+fakeContentDigest("node:22-alpine")) {
			t.Fatalf("%s: the last answer was not used:\n%s", outage, prepared.DockerfilePreview)
		}
		note := ""
		for _, candidate := range prepared.Notes {
			if strings.Contains(candidate, "used node:22-alpine as resolved at 14:02 UTC on 22 Nov 2026") {
				note = candidate
			}
		}
		if note == "" || !strings.HasPrefix(note, "Docker Hub ") {
			t.Fatalf("%s: notes = %v", outage, prepared.Notes)
		}
	}
	backend.resolveErr = errors.New("manifest unknown: manifest unknown")
	if _, err := builder.Prepare(context.Background(), root, nodeRecipeConfig, false, "t:1"); !errors.Is(err, ErrBuilderUnavailable) {
		t.Fatalf("a tag the registry says is gone was built from memory: %v", err)
	}
	backend.resolveErr = errors.New("dial tcp: i/o timeout")
	if _, err := NewArtifactBuilder(backend).Prepare(context.Background(), root, nodeRecipeConfig, false, "t:1"); err == nil {
		t.Fatal("a base never answered for was built during an outage")
	}
}

// The last answers outlive the process, so a dashboard restarted during an
// outage still builds what it built before.
func TestLastBaseAnswersAreKeptAcrossARestart(t *testing.T) {
	t.Parallel()
	root := baseResolutionFixture(t)
	file := filepath.Join(t.TempDir(), "deploy-base-resolutions.json")
	if _, err := NewArtifactBuilder(&artifactBackendFake{}).WithBaseResolutionFile(file).
		Prepare(context.Background(), root, nodeRecipeConfig, false, "t:1"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(raw), `"node:22-alpine"`) || !strings.Contains(string(raw), "resolvedAt") {
		t.Fatalf("stored answers = %s (%v)", raw, err)
	}
	down := &artifactBackendFake{resolveErr: errors.New("dial tcp 54.0.0.1:443: connect: network is unreachable")}
	restarted := NewArtifactBuilder(down).WithBaseResolutionFile(file)
	restarted.bases.now = func() time.Time { return time.Now().Add(time.Hour) }
	prepared, err := restarted.Prepare(context.Background(), root, nodeRecipeConfig, false, "t:1")
	if err != nil {
		t.Fatalf("a restarted dashboard could not build during an outage: %v", err)
	}
	if !slices.ContainsFunc(prepared.Notes, func(note string) bool {
		return strings.HasPrefix(note, "Docker Hub did not answer; used node:22-alpine")
	}) {
		t.Fatalf("notes = %v", prepared.Notes)
	}
	if err := os.WriteFile(file, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewArtifactBuilder(&artifactBackendFake{}).WithBaseResolutionFile(file).
		Prepare(context.Background(), root, nodeRecipeConfig, false, "t:1"); err != nil {
		t.Fatalf("an unreadable file stopped a build: %v", err)
	}
}

func TestRegistryDisplayNames(t *testing.T) {
	t.Parallel()
	for reference, want := range map[string]string{
		"node:22-alpine": "Docker Hub", "oven/bun:1-alpine": "Docker Hub", "docker.io/library/node:22": "Docker Hub",
		"ghcr.io/gohugoio/hugo:v0.152.2": "ghcr.io", "mcr.microsoft.com/dotnet/sdk:8.0": "mcr.microsoft.com",
		"localhost/app:1": "localhost", "registry.example:5000/team/app:1": "registry.example:5000",
	} {
		if got := registryDisplayName(reference); got != want {
			t.Errorf("%s: %s, want %s", reference, got, want)
		}
	}
}
