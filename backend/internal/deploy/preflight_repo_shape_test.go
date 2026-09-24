package deploy

import (
	"strings"
	"testing"
)

func TestDetectionOutcomeSaysWhatTheSourceIs(t *testing.T) {
	empty := DetectionResult{SetAside: []DetectionSetAside{{Path: "templates/index.html", Reason: "a template", Kind: "template"}}}
	findings := detectionOutcomeFindings(&empty, PlanConfiguration{})
	if len(findings) != 1 || findings[0].Code != "detection_empty" || !strings.Contains(findings[0].Measured, "templates/index.html") {
		t.Fatalf("empty = %#v", findings)
	}
	selected := detectShapeFixture(t, map[string]string{
		"package.json": nextManifest, "package-lock.json": "{}",
		"examples/basic/package.json": viteManifest, "examples/basic/package-lock.json": "{}",
	})
	findings = detectionOutcomeFindings(&selected, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}})
	if len(findings) != 1 || findings[0].Code != "detection_selected" ||
		!strings.Contains(findings[0].Measured, "examples/basic is an example") {
		t.Fatalf("selected = %#v", findings)
	}
}

func TestRepositoryShapeFindingsForTheSelectedCandidate(t *testing.T) {
	recipe := PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}}
	t.Run("static files of an application", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"package.json": expressManifest, "package-lock.json": "{}", "docs/index.html": "<html></html>",
		})
		docs := candidateAtRoot(result, "docs", BuildStatic)
		result.SelectedID = docs.ID
		findings := repoShapeFindings(&result, PlanConfiguration{Build: BuildPlanConfig{Method: BuildStatic, RootDirectory: "docs"}})
		if !hasFinding(findings, "static_candidate_nested", PreflightWarning) {
			t.Fatalf("findings = %#v", findings)
		}
	})
	t.Run("an example chosen by hand", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"package.json": nextManifest, "package-lock.json": "{}",
			"examples/basic/package.json": viteManifest, "examples/basic/package-lock.json": "{}",
		})
		example := candidateAtRoot(result, "examples/basic", BuildRecipe)
		result.SelectedID = example.ID
		findings := repoShapeFindings(&result, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, RootDirectory: "examples/basic"}})
		if !hasFinding(findings, "selected_candidate_demoted", PreflightWarning) {
			t.Fatalf("findings = %#v", findings)
		}
	})
	t.Run("the api of a split repository", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"web/package.json": viteManifest, "web/package-lock.json": "{}", "web/.env.example": "VITE_API_URL=\n",
			"api/package.json": expressManifest, "api/package-lock.json": "{}",
		})
		findings := repoShapeFindings(&result, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, RootDirectory: "api"}})
		if !hasFinding(findings, "companion_service_not_deployed", PreflightWarning) {
			t.Fatalf("findings = %#v", findings)
		}
	})
	t.Run("a committed virtualenv in the build context", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"requirements.txt": "flask==3.1\n", "app.py": "from flask import Flask\napp = Flask(__name__)\n",
			".env/pyvenv.cfg": "home = /usr/bin\n", "myenv/pyvenv.cfg": "home = /usr/bin\n",
		})
		if !hasFinding(repoShapeFindings(&result, recipe), "committed_virtualenv", PreflightWarning) {
			t.Fatalf("set aside = %#v", result.SetAside)
		}
	})
}

func TestPreflightAsksWhetherGitLFSIsInstalled(t *testing.T) {
	draft := completePlanningDraftModel()
	draft.Data.Source = &DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/o/r.git", IncludeLFS: true}
	if request := preflightObservationRequest(draft, *draft.Data.Configuration); !request.NeedsGitLFS {
		t.Fatalf("request = %#v", request)
	}
	draft.Data.Source.IncludeLFS = false
	if request := preflightObservationRequest(draft, *draft.Data.Configuration); request.NeedsGitLFS {
		t.Fatalf("request = %#v", request)
	}
}
