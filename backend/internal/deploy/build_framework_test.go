package deploy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestFrameworkFollowsTheCandidateTheBuildStillDescribes(t *testing.T) {
	t.Parallel()
	candidate := DetectedCandidate{ID: "web", Root: "apps/web", Framework: "nextjs"}
	detection := &DetectionResult{Candidates: []DetectedCandidate{{ID: "api", Framework: "fastify"}, candidate}, SelectedID: "web"}
	for _, test := range []struct {
		name      string
		detection *DetectionResult
		build     BuildPlanConfig
		want      string
	}{
		{"chosen candidate", detection, BuildPlanConfig{RootDirectory: "apps/web/"}, "nextjs"},
		{"client value replaced", detection, BuildPlanConfig{RootDirectory: "apps/web", Framework: "remix"}, "nextjs"},
		{"build moved elsewhere", detection, BuildPlanConfig{RootDirectory: "apps/api", Framework: "remix"}, ""},
		{"no detection keeps the server copy", nil, BuildPlanConfig{Framework: "astro"}, "astro"},
		{"repository root", &DetectionResult{Candidates: []DetectedCandidate{{ID: "root", Framework: "vite"}}, SelectedID: "root"},
			BuildPlanConfig{RootDirectory: "."}, "vite"},
	} {
		if got := detectedFramework(test.detection, test.build); got != test.want {
			t.Errorf("%s: detectedFramework = %q, want %q", test.name, got, test.want)
		}
	}

	stored := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", RootDirectory: "apps/web", Framework: "nextjs"}
	for _, test := range []struct {
		name string
		next BuildPlanConfig
		want string
	}{
		{"same build, new command", BuildPlanConfig{Method: BuildRecipe, Recipe: "node", RootDirectory: "apps/web/", BuildCommand: "pnpm build"}, "nextjs"},
		{"client cannot rename it", BuildPlanConfig{Method: BuildRecipe, Recipe: "node", RootDirectory: "apps/web", Framework: "remix"}, "nextjs"},
		{"recipe changed", BuildPlanConfig{Method: BuildRecipe, Recipe: "python", RootDirectory: "apps/web"}, ""},
		{"method changed", BuildPlanConfig{Method: BuildDockerfile, RootDirectory: "apps/web"}, ""},
		{"root changed", BuildPlanConfig{Method: BuildRecipe, Recipe: "node", RootDirectory: "apps/admin"}, ""},
	} {
		if got := carriedFramework(stored, test.next); got != test.want {
			t.Errorf("%s: carriedFramework = %q, want %q", test.name, got, test.want)
		}
	}
}

// The framework is a name, not a build input: recording it must not make a
// plan differ from the same plan without it, or every existing project would
// show a pending build change the moment one was recorded.
func TestBuildPlanDigestLeavesTheFrameworkOut(t *testing.T) {
	t.Parallel()
	build := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{}}
	plain, _ := json.Marshal(build)
	named := build
	named.Framework = "nextjs"
	if buildPlanDigest(named) != digestBytes(plain) || buildPlanDigest(build) != digestBytes(plain) {
		t.Fatal("the framework changed the build plan digest")
	}
	changed := build
	changed.BuildCommand = "next build"
	if buildPlanDigest(changed) == buildPlanDigest(build) {
		t.Fatal("a real build change kept the same digest")
	}
}

// Committing a draft records the framework its chosen candidate was detected
// as — never the browser's — and later saves carry it while the build and
// source still describe that code, dropping it once the source moves.
func TestCommittedFrameworkIsCarriedUntilTheSourceMoves(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	draft, err := fixture.plans.Create(ctx, 41, "operator")
	if err != nil {
		t.Fatal(err)
	}
	draft = saveCompletePlanningDraft(t, fixture.plans, draft)
	candidate := newDetectedCandidate("", BuildNone, DetectedCandidate{
		Name: "web", Profile: ProfileWorker, Confidence: ConfidenceHigh, Framework: "nextjs",
		Evidence: []DetectionEvidence{{Path: "package.json", Reason: "fixture"}}, NeedsDecision: []string{},
	})
	draft, err = fixture.plans.SaveDetection(ctx, draft.ID, 41, true, draft.Revision, DetectionResult{
		Source:     draft.Data.Detection.Source,
		Candidates: []DetectedCandidate{candidate}, SelectedID: candidate.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	configuration := *draft.Data.Configuration
	configuration.Build.Framework = "remix"
	draft, err = fixture.plans.Save(ctx, draft.ID, 41, true, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftConfiguration, Configuration: &configuration,
	})
	if err != nil {
		t.Fatal(err)
	}
	if draft.Data.Configuration.Build.Framework != "" {
		t.Fatalf("draft kept the browser's framework %q", draft.Data.Configuration.Build.Framework)
	}
	preflight, err := PreflightDraft(ctx, draft, &preflightObserverFake{observation: HostObservation{
		Facilities: map[string]FacilityObservation{"git": {Available: true}}, Paths: []PathObservation{}, Ports: []PortObservation{},
	}}, true)
	if err != nil {
		t.Fatal(err)
	}
	draft, err = fixture.plans.SavePreflight(ctx, draft.ID, 41, true, draft.Revision, preflight)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := fixture.plans.Commit(ctx, draft.ID, 41, true, DraftCommitRequest{Revision: draft.Revision})
	if err != nil {
		t.Fatal(err)
	}
	projectID, environmentID := committed.ProjectID, committed.EnvironmentID
	buildDigest := func(revision int) string {
		t.Helper()
		var digest string
		if err := fixture.store.DB.QueryRow(`SELECT digest FROM deploy_build_plans WHERE environment_id = ? AND revision = ?`,
			environmentID, revision).Scan(&digest); err != nil {
			t.Fatal(err)
		}
		return digest
	}

	read, err := fixture.plans.EnvironmentConfiguration(ctx, projectID, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	unnamed := read.Build
	unnamed.Framework = ""
	unnamedJSON, _ := json.Marshal(unnamed)
	if read.Build.Framework != "nextjs" || buildDigest(1) != digestBytes(unnamedJSON) {
		t.Fatalf("committed build = %#v digest %s", read.Build, buildDigest(1))
	}

	resent := read.Build
	resent.Framework = "remix"
	saved, err := fixture.plans.SaveEnvironmentConfiguration(ctx, projectID, environmentID, ConfigurationWriteRequest{
		Revision: read.Revision, Build: resent, Runtime: read.Runtime, Dependencies: read.Dependencies,
		Checks: read.Checks, Domains: read.Domains,
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Build.Framework != "nextjs" || buildDigest(saved.Revision) != buildDigest(1) {
		t.Fatalf("saved build = %#v", saved.Build)
	}

	source, identity := *read.Source, *read.Identity
	source.Ref, identity.Ref = "release", "release"
	branched, err := fixture.plans.SaveEnvironmentSource(ctx, projectID, environmentID, saved.Revision, source, identity)
	if err != nil {
		t.Fatal(err)
	}
	if branched.Build.Framework != "nextjs" {
		t.Fatalf("a new branch of the same repository dropped the framework: %#v", branched.Build)
	}

	source.URL = "https://example.test/owner/other.git"
	identity.Remote, identity.Repository = source.URL, "owner/other"
	moved, err := fixture.plans.SaveEnvironmentSource(ctx, projectID, environmentID, branched.Revision, source, identity)
	if err != nil {
		t.Fatal(err)
	}
	var movedPreview string
	if err := fixture.store.DB.QueryRow(`SELECT preview FROM deploy_build_plans WHERE environment_id = ? AND revision = ?`,
		environmentID, moved.Revision).Scan(&movedPreview); err != nil {
		t.Fatal(err)
	}
	// The digest is unchanged, so dropping the name never reads as a pending
	// build change beside the source change that caused it.
	if moved.Build.Framework != "" || strings.Contains(movedPreview, "nextjs") || buildDigest(moved.Revision) != buildDigest(1) {
		t.Fatalf("moved source kept the framework: %#v preview %s", moved.Build, movedPreview)
	}
}
