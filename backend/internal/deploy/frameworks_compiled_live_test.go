package deploy

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// compiledLiveFixtures are the Go and Rust shapes TestLiveDetectedFrameworkBuildAndServing
// builds beyond a single module or crate.
var compiledLiveFixtures = []string{"go-workspace", "go-embed", "rust-workspace", "trunk", "leptos"}

// livePrepareAndBuildWithin prepares the candidate at member inside the
// checkout at root and builds it from the context preparation chose, as
// prepare_context and build_artifact do: a workspace member builds from its
// workspace, and a module or project from the directory that owns it.
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
	// Every value a live build is given is treated as secret, as the
	// strictest redaction the executor applies.
	secret := map[string]bool{}
	for name := range variables {
		secret[name] = true
	}
	result, err := builder.Build(t.Context(), context, tag, config, prepared, variables, secret, "", SourceIdentity{}, nil, emit)
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

// javaCatalogueImages and dotnetCatalogueImages are every image the Java and
// .NET recipes can resolve: the build images of each tool and release, the
// runtimes, and the JDKs a toolchain is provided from.
func javaCatalogueImages() []string {
	var images []string
	for _, release := range javaRecipeReleases {
		version := strconv.Itoa(release)
		images = append(images, "maven:3-eclipse-temurin-"+version, "eclipse-temurin:"+version+"-jdk", "eclipse-temurin:"+version+"-jre")
		if release <= 21 {
			images = append(images, "gradle:8-jdk"+version)
		}
		if release >= 17 {
			images = append(images, "gradle:9-jdk"+version)
		}
	}
	return images
}

// jvmLanguageCatalogueImages are the Scala and Clojure builders and the JRE
// each release runs on.
func jvmLanguageCatalogueImages() []string {
	var images []string
	for _, jdk := range jvmLanguageJDKs {
		for _, recipe := range []jvmLanguageRecipe{{jdk: jdk, sbt: "1"}, {jdk: jdk, sbt: "2"}, {jdk: jdk, tool: "lein"}, {jdk: jdk, tool: "tools-deps"}} {
			images = append(images, recipe.bases()...)
		}
	}
	return images
}

func dotnetCatalogueImages() []string {
	var images []string
	for _, version := range dotnetRecipeVersions {
		for _, image := range []string{"sdk", "aspnet", "runtime"} {
			images = append(images, "mcr.microsoft.com/dotnet/"+image+":"+version)
		}
	}
	return images
}

// Every reviewed base must run where the dashboard runs: the Java 11 and
// 17 Alpine JREs the recipe used to run on were published for amd64 alone,
// so an arm64 server failed every such build after Deploy. The images are
// resolved, not pulled.
func TestLiveRecipeBaseCatalogueRunsOnAmd64AndArm64(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to resolve the recipe catalogue")
	}
	client := liveC4Docker(t)
	backend := NewDockerArtifactBackend(client)
	seen := map[string]bool{}
	images := append(append(javaCatalogueImages(), dotnetCatalogueImages()...), jvmLanguageCatalogueImages()...)
	for _, bases := range recipeBaseCatalogue {
		images = append(images, bases...)
	}
	sort.Strings(images)
	for _, image := range images {
		if seen[image] {
			continue
		}
		seen[image] = true
		t.Run(image, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			resolved, err := backend.ResolveImage(ctx, image, "")
			if err != nil {
				t.Fatal(err)
			}
			for _, platform := range []string{"linux/amd64", "linux/arm64"} {
				if !platformListContains(resolved.Platforms, platform) {
					t.Fatalf("%s publishes %v, not %s", image, resolved.Platforms, platform)
				}
			}
		})
	}
}
