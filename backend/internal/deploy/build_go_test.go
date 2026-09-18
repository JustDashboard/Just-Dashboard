package deploy

import (
	"strings"
	"testing"
)

func TestGoRecipeSelectsSourceAndExplicitVersions(t *testing.T) {
	for _, fixture := range []struct{ name, explicit, file, module, want string }{
		{"default", "", "", "module fixture", "1.26"},
		{"module", "", "", "module fixture\ngo 1.26.8\n", "1.26.8"},
		{"toolchain", "", "", "module fixture\ngo 1.25\ntoolchain go1.26.8\n", "1.26.8"},
		{"version file", "", "1.25.6\n", "module fixture\ngo 1.25\n", "1.25.6"},
		{"explicit", "1.26.8", "1.25.6", "module fixture\ngo 1.25\n", "1.26.8"},
		{"older module", "", "", "module fixture\ngo 1.21\n", "1.26"},
		{"too old", "1.25.1", "", "module fixture\ngo 1.26\n", ""},
		{"unsupported", "1.99.1", "", "module fixture", ""},
		{"custom toolchain", "", "", "module fixture\ntoolchain go1.26-custom\n", ""},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			got, err := chooseGoRecipeVersion(fixture.explicit, fixture.file, []byte(fixture.module))
			if fixture.want == "" {
				if err == nil {
					t.Fatalf("unsupported version accepted: %s", got)
				}
				return
			}
			if err != nil || got != fixture.want {
				t.Fatalf("version=%q, error=%v", got, err)
			}
		})
	}
}

func TestGoRecipeHonorsCommandsAndRejectsCGOBeforeBuilding(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "go.mod", "module fixture\ngo 1.26\n")
	writeBuildFixture(t, root, "main.go", "package main\nfunc main() {}\n")
	config := BuildPlanConfig{Method: BuildRecipe, Recipe: "go", GoVersion: "1.26.8", BuildCommand: "go generate ./... && go build -o /out/app .", StartCommand: "/app --generated"}
	builder := NewArtifactBuilder(&artifactBackendFake{})
	prepared, err := builder.Prepare(t.Context(), root, config, false, "fixture:go")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{config.BuildCommand, config.StartCommand, "GOTOOLCHAIN=local", "golang:1.26.8-alpine@sha256:"} {
		if !strings.Contains(prepared.DockerfilePreview, required) {
			t.Fatalf("ignored Go configuration %q", required)
		}
	}
	if prepared.GoVersion != "1.26.8" {
		t.Fatal(prepared)
	}
	writeBuildFixture(t, root, "native.go", "package main\nimport \"C\"\n")
	if _, err := builder.Prepare(t.Context(), root, config, false, "fixture:go"); err == nil || !strings.Contains(err.Error(), "CGO") {
		t.Fatalf("CGO admission: %v", err)
	}
	detection, err := (Detector{}).DetectPath(t.Context(), root, SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	candidate := selectedDetectionCandidate(&detection)
	if candidate == nil || !strings.Contains(candidate.RecipeIssue, "CGO") {
		t.Fatalf("missing early CGO evidence: %+v", detection)
	}
	draft := &Draft{Data: DraftData{Detection: &detection, Source: &DraftSourceConfig{Kind: SourceGit}, Intent: &DraftIntentConfig{Profile: ProfileService}}}
	findings := preflightFindings(draft, PlanConfiguration{Build: config}, HostObservation{}, true)
	for _, finding := range findings {
		if finding.Code == "recipe_unsupported" && finding.Severity == PreflightBlocked {
			return
		}
	}
	t.Fatal("preflight did not block unsupported CGO")
}
