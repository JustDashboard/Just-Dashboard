package deploy

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// compiledLiveFixtures are the Go and Rust shapes TestLiveDetectedFrameworkBuildAndServing
// builds beyond a single module or crate.
var compiledLiveFixtures = []string{"go-workspace", "go-embed", "rust-workspace", "trunk", "leptos"}

// livePrepareAndBuildWithin prepares the candidate at member inside the
// checkout at root and builds it from the context preparation chose, as
// prepare_context and build_artifact do: a workspace member builds from its
// workspace.
func livePrepareAndBuildWithin(t *testing.T, builder *ArtifactBuilder, root, member, tag string, config BuildPlanConfig,
	variables map[string]string, emit func(BuildLog) error) BuildArtifactResult {
	t.Helper()
	config.RootDirectory = member
	prepared, err := builder.PrepareWithin(context.Background(), root, filepath.Join(root, filepath.FromSlash(member)), config, false, tag, buildVariableNames(variables)...)
	if err != nil {
		t.Fatal(err)
	}
	context := filepath.Join(root, filepath.FromSlash(member))
	if prepared.ContextDirectory != "" {
		context = filepath.Join(root, filepath.FromSlash(prepared.ContextDirectory))
	}
	result, err := builder.Build(t.Context(), context, tag, config, prepared, variables, "", SourceIdentity{}, nil, emit)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

var trunkWasmRE = regexp.MustCompile(`(?:href|src)="(/[^"]+_bg\.wasm)"`)

// compiledLiveContent is what a compiled fixture serves beyond its page and
// scripts, fetched to prove the build did what the recipe says: the Go
// server's cgo SQLite, zone data and generated templ component, and the
// Trunk site's WebAssembly, which is where its text is compiled.
func compiledLiveContent(t *testing.T, name, html string, fetch func(string) string) string {
	t.Helper()
	switch name {
	case "go-embed":
		status := fetch("/status")
		if !strings.Contains(status, "sqlite 3.") || !strings.Contains(status, "Europe/Bucharest") {
			t.Fatalf("status = %q", status)
		}
		return status
	case "trunk":
		match := trunkWasmRE.FindStringSubmatch(html)
		if match == nil {
			t.Fatalf("no WebAssembly module in %q", html)
		}
		return fetch(match[1])
	}
	return ""
}

// TestLiveGoRecipeCatalogueResolves resolves every image the Go and Rust
// recipes can name: each Go family on Alpine, the Alpine release a
// dynamically linked Go build pairs with, and the Debian images the Leptos
// and Trunk builds run on. A family added to goRecipeFamilies before its
// image is published fails here, not in an operator's build.
func TestLiveGoRecipeCatalogueResolves(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to resolve the Go and Rust catalogue images")
	}
	backend := NewDockerArtifactBackend(liveC4Docker(t))
	references := []string{}
	for _, entry := range goRecipeFamilies {
		references = append(references, "golang:"+entry.family+"-alpine")
		if !entry.eol {
			references = append(references, "golang:"+entry.family+"-alpine"+compiledDynamicAlpine)
		}
	}
	for _, key := range []string{"go", "go:dynamic", "rust", "rust:leptos", "rust:trunk"} {
		references = append(references, recipeBaseCatalogue[key]...)
	}
	for _, reference := range uniqueOrdered(references) {
		resolved, err := backend.ResolveImage(t.Context(), reference, "")
		if err != nil || validateResolvedImage(resolved) != nil {
			t.Fatalf("resolve %s: %v", reference, err)
		}
		arm64 := slices.ContainsFunc(resolved.Platforms, func(platform string) bool { return strings.HasPrefix(platform, "linux/arm64") })
		if !slices.Contains(resolved.Platforms, "linux/amd64") || !arm64 {
			t.Fatalf("%s is not published for amd64 and arm64: %v", reference, resolved.Platforms)
		}
	}
}
