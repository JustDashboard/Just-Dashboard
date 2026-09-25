package deploy

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestGoRecipeSelectsSourceAndExplicitVersions(t *testing.T) {
	for _, fixture := range []struct{ name, explicit, file, module, want, source, downgraded string }{
		{"default", "", "", "module fixture", "1.27", "default", ""},
		// go.mod's go line is a minimum: the family's maintained image, never
		// the unpatched .0 it names.
		{"module minimum", "", "", "module fixture\ngo 1.26.0\n", "1.26", "go.mod", ""},
		{"module patch", "", "", "module fixture\ngo 1.26.8\n", "1.26", "go.mod", ""},
		{"newest module", "", "", "module fixture\ngo 1.27\n", "1.27", "go.mod", ""},
		{"toolchain", "", "", "module fixture\ngo 1.25\ntoolchain go1.26.8\n", "1.26", "toolchain", ""},
		{"toolchain written by go get", "", "", "module fixture\ngo 1.26.0\ntoolchain go1.27.1\n", "1.27", "toolchain", ""},
		{"toolchain newer than the catalogue", "", "", "module fixture\ngo 1.26.0\ntoolchain go1.28.2\n", "1.27", "toolchain", "go1.28.2"},
		// A family past upstream support is never chosen from go.mod.
		{"end-of-life minimum", "", "", "module fixture\ngo 1.25.0\ntoolchain go1.25.3\n", "1.27", "default", ""},
		{"version file", "", "1.25.6\n", "module fixture\ngo 1.25\n", "1.25.6", ".go-version", ""},
		{"explicit", "1.26.8", "1.25.6", "module fixture\ngo 1.25\n", "1.26.8", "build.goVersion", ""},
		{"older module", "", "", "module fixture\ngo 1.21\n", "1.27", "default", ""},
		{"too old", "1.25.1", "", "module fixture\ngo 1.26\n", "", "", ""},
		{"future go line", "", "", "module fixture\ngo 1.28\n", "", "", ""},
		{"unsupported", "1.99.1", "", "module fixture", "", "", ""},
		{"custom toolchain", "", "", "module fixture\ntoolchain go1.26-custom\n", "", "", ""},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			choice, err := resolveGoRecipeVersion(fixture.explicit, fixture.file, []byte(fixture.module))
			if fixture.want == "" {
				if err == nil {
					t.Fatalf("unsupported version accepted: %+v", choice)
				}
				return
			}
			if err != nil || choice.version != fixture.want || choice.source != fixture.source || choice.downgraded != fixture.downgraded {
				t.Fatalf("choice=%+v, error=%v", choice, err)
			}
		})
	}
	if choice, _ := resolveGoRecipeVersion("", "", []byte("module fixture\ngo 1.26.0\n")); choice.note() != "go 1.26.0 in go.mod is a minimum; building with the maintained Go 1.26 patch" {
		t.Fatalf("note = %q", choice.note())
	}
	if choice, _ := resolveGoRecipeVersion("1.25", "", nil); !choice.eol {
		t.Fatalf("an end-of-life pin was not marked: %+v", choice)
	}
}

// The Build settings field states the same catalogue as the recipe, so a
// version the recipe builds is never refused in the form.
func TestGoRecipeCatalogueMatchesTheBuildSettings(t *testing.T) {
	content, err := os.ReadFile("../../../frontend/src/components/deploy/deployment-defaults.ts")
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`export const GO_VERSIONS = \[([^\]]*)\]`).FindSubmatch(content)
	if match == nil {
		t.Fatal("deployment-defaults.ts no longer lists GO_VERSIONS")
	}
	families := []string{}
	for _, entry := range goRecipeFamilies {
		families = append(families, `"`+entry.family+`"`)
	}
	if got := strings.Join(strings.Fields(strings.ReplaceAll(string(match[1]), ",", " ")), ", "); got != strings.Join(families, ", ") {
		t.Fatalf("frontend Go versions %s, recipe %s", got, strings.Join(families, ", "))
	}
	if !goRecipeVersionRE.MatchString("1.27.1") || goRecipeVersionRE.MatchString("1.24") || goRecipeVersionRE.MatchString("1.28") {
		t.Fatal("the version pattern is not the catalogue's")
	}
}

func TestGoRecipeHonorsCommandsAndBuildsCGO(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "go.mod", "module fixture\ngo 1.26\n")
	writeBuildFixture(t, root, "main.go", "package main\nfunc main() {}\n")
	config := BuildPlanConfig{Method: BuildRecipe, Recipe: "go", GoVersion: "1.26.8", BuildCommand: "go generate ./... && go build -o /out/app .", StartCommand: "/app --generated"}
	builder := NewArtifactBuilder(&artifactBackendFake{})
	prepared, err := builder.Prepare(t.Context(), root, config, false, "fixture:go")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{config.BuildCommand, config.StartCommand, "GOTOOLCHAIN=local", "golang:1.26.8-alpine@sha256:", "CGO_ENABLED=0"} {
		if !strings.Contains(prepared.DockerfilePreview, required) {
			t.Fatalf("ignored Go configuration %q", required)
		}
	}
	if prepared.GoVersion != "1.26.8" {
		t.Fatal(prepared)
	}
	// A local file importing "C" with no pure-Go twin needs cgo: the build
	// stage gets a C toolchain and links statically, where it used to refuse.
	writeBuildFixture(t, root, "native.go", "package main\n// #include <stdlib.h>\nimport \"C\"\n")
	config.BuildCommand = ""
	prepared, err = builder.Prepare(t.Context(), root, config, false, "fixture:go")
	if err != nil {
		t.Fatalf("local cgo: %v", err)
	}
	for _, required := range []string{"RUN apk add --no-cache gcc musl-dev", "CGO_ENABLED=1", `-linkmode external -extldflags "-static"`, "FROM alpine:3.22@sha256:"} {
		if !strings.Contains(prepared.DockerfilePreview, required) {
			t.Fatalf("cgo build missing %q:\n%s", required, prepared.DockerfilePreview)
		}
	}
	detection, err := (Detector{}).DetectPath(t.Context(), root, SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	candidate := selectedDetectionCandidate(&detection)
	if candidate == nil || candidate.RecipeIssue != "" || candidate.Go == nil || len(candidate.Go.CGOLocal) != 1 {
		t.Fatalf("local cgo evidence: %+v", detection)
	}
	// A cgo file restricted to cgo builds, beside its `!cgo` twin, builds
	// pure Go.
	writeBuildFixture(t, root, "native.go", "//go:build cgo\n\npackage main\n\nimport \"C\"\n")
	writeBuildFixture(t, root, "native_stub.go", "//go:build !cgo\n\npackage main\n")
	prepared, err = builder.Prepare(t.Context(), root, config, false, "fixture:go")
	if err != nil || !strings.Contains(prepared.DockerfilePreview, "CGO_ENABLED=0") {
		t.Fatalf("a twinned cgo file turned cgo on: %v\n%s", err, prepared.DockerfilePreview)
	}
}
