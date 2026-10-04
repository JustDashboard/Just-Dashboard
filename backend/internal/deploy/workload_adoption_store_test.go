package deploy

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func recoveredStoreFixture(t *testing.T) *RecoveredWorkload {
	t.Helper()
	source := DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposePaste, ComposeFiles: []ComposeDocument{{Path: "compose.yml", Content: "services:\n  web:\n    image: nginx:alpine\n    environment:\n      API_TOKEN: ${API_TOKEN}\n"}}}
	analysis, err := analyzeComposeDocuments(source.ComposeFiles)
	if err != nil {
		t.Fatal(err)
	}
	configuration := canonicalConfiguration(PlanConfiguration{
		Build: BuildPlanConfig{Method: BuildCompose}, Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst},
		Variables: []PlannedVariable{{Name: "API_TOKEN", Sensitivity: "secret", Scopes: []string{"runtime"}, Required: true}},
	})
	snapshot := runtimeReleaseSnapshot{Version: 1, Plan: configuration.Runtime, Compose: &ResolvedComposeSnapshot{
		SourceDigest: analysis.Digest, Files: []string{"compose.yml"}, PrimaryService: "web",
		Services: []ResolvedComposeService{{Plan: analysis.Services[0], Reference: "nginx:alpine", Digest: testImageDigest, ConfigDigest: testImageDigest}},
	}}
	return &RecoveredWorkload{Source: source, Configuration: configuration, Environment: map[string]string{"API_TOKEN": "original-private-value"},
		Detection: DetectionResult{Source: SourceIdentity{Kind: SourceCompose, Digest: analysis.Digest, ComposeFiles: analysis.Files, Services: []string{"web"}}, Compose: &analysis, Candidates: []DetectedCandidate{{ID: "compose", Name: "Compose stack", Profile: ProfileCompose, BuildMethod: BuildCompose, Confidence: ConfidenceHigh}}, SelectedID: "compose"},
		Adoption: &WorkloadAdoption{Key: "container:old", Kind: "container", ResourceID: "old", Name: "web", Manager: "Docker", Blockers: []string{},
			BaselineSource: source, BaselineConfiguration: configuration, BaselineDigest: testImageDigest, Snapshot: mustJSON(snapshot),
			Runtime: ReleaseRuntimeInput{Kind: "container", RuntimeID: "old", Name: "web", Metadata: mustJSON(dockerReleaseRuntimeMetadata{Version: 1, PrimaryContainerID: "old"})}},
	}
}

func checkRecoveredDraft(t *testing.T, fixture *planningStoreFixture, draft *Draft) *Draft {
	t.Helper()
	preflight, err := PreflightDraft(t.Context(), draft, &preflightObserverFake{observation: HostObservation{Facilities: map[string]FacilityObservation{"docker": {Available: true}, "compose": {Available: true}}}}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range preflight.Findings {
		if finding.Severity == PreflightBlocked || finding.Severity == PreflightDecision {
			t.Fatalf("preflight blocked: %+v", finding)
		}
	}
	draft, err = fixture.plans.SavePreflight(t.Context(), draft.ID, 41, false, draft.Revision, preflight)
	if err != nil {
		t.Fatal(err)
	}
	return draft
}

func TestWorkloadAdoptionCommitsFullDeploymentAndOriginalPrivateBaseline(t *testing.T) {
	fixture := newPlanningStoreFixture(t)
	draft, err := fixture.plans.CreateRecoveredDraft(t.Context(), 41, "operator", DraftIntentConfig{Name: "adopted-web", Profile: ProfileCompose}, recoveredStoreFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(mustJSON(draft))
	var data, originalEnc, currentEnc string
	if err := fixture.store.DB.QueryRow(`SELECT data_json,adoption_enc,environment_enc FROM deploy_drafts WHERE id=?`, draft.ID).Scan(&data, &originalEnc, &currentEnc); err != nil {
		t.Fatal(err)
	}
	if originalEnc == "" || currentEnc == "" || strings.Contains(encoded+data, "original-private-value") || strings.Contains(encoded, originalEnc) {
		t.Fatal("captured values were not kept private")
	}
	// An edit is the desired plan; the running app and its rollback inputs
	// must still describe what was captured, even after a browser reload.
	configuration := *draft.Data.Configuration
	configuration.Runtime.MemoryMB = 128
	dotenv := "API_TOKEN=desired-private-value"
	draft, err = fixture.plans.Save(t.Context(), draft.ID, 41, false, DraftSaveRequest{Revision: draft.Revision, Step: DraftConfiguration, Configuration: &configuration, Dotenv: &dotenv})
	if err != nil {
		t.Fatal(err)
	}
	draft, err = fixture.plans.Get(t.Context(), draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	draft = checkRecoveredDraft(t, fixture, draft)
	ack := []string{}
	for _, finding := range draft.Findings {
		if finding.Severity == PreflightWarning {
			ack = append(ack, finding.Code)
		}
	}
	result, err := fixture.plans.Commit(t.Context(), draft.ID, 41, false, DraftCommitRequest{Revision: draft.Revision, AcknowledgedWarnings: ack})
	if err != nil {
		t.Fatal(err)
	}
	if result.PlanRevision != 2 || !result.Created {
		t.Fatalf("result=%+v", result)
	}
	runs := NewOrchestrationStore(fixture.store)
	live, err := runs.LiveRelease(t.Context(), result.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if live.Release.PlanRevision != 1 || live.Release.State != "live" || !live.Release.Pinned {
		t.Fatalf("baseline=%+v", live.Release)
	}
	runtime, err := runs.RuntimeForRelease(t.Context(), live.Release.ID)
	if err != nil || runtime.RuntimeID != "old" || runtime.State != "live" {
		t.Fatalf("runtime=%+v err=%v", runtime, err)
	}
	baseline, err := decodeReleaseRuntimeSnapshot(live)
	if err != nil || baseline.Plan.MemoryMB != 0 {
		t.Fatalf("baseline settings overwritten: %+v %v", baseline, err)
	}
	values, err := fixture.plans.OpenRunScopedVariables(t.Context(), live.Release.RunID, result.EnvironmentID, "runtime")
	if err != nil || len(values) != 1 || values[0].Value != "original-private-value" {
		t.Fatalf("baseline values=%+v err=%v", values, err)
	}
	desired, err := fixture.plans.OpenScopedVariables(t.Context(), result.EnvironmentID, "runtime")
	if err != nil || len(desired) != 1 || desired[0].Value != "desired-private-value" {
		t.Fatalf("desired values=%+v err=%v", desired, err)
	}
	pending, err := fixture.plans.PendingState(t.Context(), result.ProjectID, result.EnvironmentID)
	if err != nil || !pending.Pending {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	if _, err := fixture.plans.RegisterObservedWorkload(t.Context(), ObservedWorkloadRegistration{Name: "duplicate", ResourceKind: "docker_container", ResourceID: "old", SourceMode: SourceModeExistingContainer, Observed: json.RawMessage(`{"key":"container:old","kind":"container","resourceId":"old"}`)}); !errors.Is(err, ErrWorkloadAlreadyImported) {
		t.Fatalf("resource received two owners: %v", err)
	}
	repeated, err := fixture.plans.Commit(t.Context(), draft.ID, 41, false, DraftCommitRequest{Revision: draft.Revision})
	if err != nil || repeated.Created || repeated.ProjectID != result.ProjectID || repeated.PlanRevision != 2 {
		t.Fatalf("repeat=%+v err=%v", repeated, err)
	}
	var queued int
	if err := fixture.store.DB.QueryRow(`SELECT count(*) FROM deploy_runs WHERE state NOT IN ('succeeded','failed','cancelled')`).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("adoption queued execution: %d %v", queued, err)
	}
}

func TestWorkloadAdoptionFailureLeavesNoProjectOrLiveRelease(t *testing.T) {
	fixture := newPlanningStoreFixture(t)
	recovered := recoveredStoreFixture(t)
	draft, err := fixture.plans.CreateRecoveredDraft(t.Context(), 41, "operator", DraftIntentConfig{Name: "invalid-baseline", Profile: ProfileCompose}, recovered)
	if err != nil {
		t.Fatal(err)
	}
	draft = checkRecoveredDraft(t, fixture, draft)
	// Simulate a damaged persisted capture. Commit must roll back desired
	// records, secrets, history and ownership together.
	draft.Data.Adoption.Snapshot = json.RawMessage(`{"version":0}`)
	if _, err := fixture.store.DB.Exec(`UPDATE deploy_drafts SET data_json=? WHERE id=?`, string(mustJSON(draft.Data)), draft.ID); err != nil {
		t.Fatal(err)
	}
	ack := []string{}
	for _, finding := range draft.Findings {
		if finding.Severity == PreflightWarning {
			ack = append(ack, finding.Code)
		}
	}
	if _, err := fixture.plans.Commit(t.Context(), draft.ID, 41, false, DraftCommitRequest{Revision: draft.Revision, AcknowledgedWarnings: ack}); err == nil {
		t.Fatal("damaged baseline committed")
	}
	for _, table := range []string{"deploy_projects", "deploy_environments", "deploy_releases", "deploy_variable_revisions", "deploy_runs", "deploy_dependencies"} {
		var count int
		if err := fixture.store.DB.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s partial rows=%d err=%v", table, count, err)
		}
	}
}

func TestWorkloadAdoptionUnchangedRecipeHasNoPendingChanges(t *testing.T) {
	fixture := newPlanningStoreFixture(t)
	recovered := recoveredStoreFixture(t)
	recovered.Configuration.Runtime.ComposeProjectName = "original-stack"
	recovered.Adoption.BaselineConfiguration = recovered.Configuration
	draft, err := fixture.plans.CreateRecoveredDraft(t.Context(), 41, "operator", DraftIntentConfig{Name: "unchanged-import", Profile: ProfileCompose}, recovered)
	if err != nil {
		t.Fatal(err)
	}
	draft = checkRecoveredDraft(t, fixture, draft)
	ack := []string{}
	for _, finding := range draft.Findings {
		if finding.Severity == PreflightWarning {
			ack = append(ack, finding.Code)
		}
	}
	result, err := fixture.plans.Commit(t.Context(), draft.ID, 41, false, DraftCommitRequest{Revision: draft.Revision, AcknowledgedWarnings: ack})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := fixture.plans.PendingState(t.Context(), result.ProjectID, result.EnvironmentID)
	if err != nil || pending.Pending {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	config, err := fixture.plans.EnvironmentConfiguration(t.Context(), result.ProjectID, result.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := fixture.plans.SaveEnvironmentConfiguration(t.Context(), result.ProjectID, result.EnvironmentID, ConfigurationWriteRequest{Revision: config.Revision, Build: config.Build, Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst}, Dependencies: config.Dependencies, Checks: config.Checks, Domains: config.Domains})
	if err != nil || saved.Runtime.ComposeProjectName != "original-stack" {
		t.Fatalf("lost project identity: %+v %v", saved, err)
	}
	foreign := saved.Runtime
	foreign.ComposeProjectName = "another-app"
	if _, err := fixture.plans.SaveEnvironmentConfiguration(t.Context(), result.ProjectID, result.EnvironmentID, ConfigurationWriteRequest{Revision: saved.Revision, Build: saved.Build, Runtime: foreign, Dependencies: saved.Dependencies}); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("claimed another stack: %v", err)
	}
}

func TestWorkloadAdoptionReviewChecksRemainDesiredUntilDeploy(t *testing.T) {
	fixture := newPlanningStoreFixture(t)
	recovered := recoveredStoreFixture(t)
	draft, err := fixture.plans.CreateRecoveredDraft(t.Context(), 41, "operator", DraftIntentConfig{Name: "edited-check-import", Profile: ProfileCompose}, recovered)
	if err != nil {
		t.Fatal(err)
	}
	configuration := *draft.Data.Configuration
	configuration.Checks = []PlannedCheck{{Name: "new-readiness", Kind: "http", Phase: "readiness", Required: true, Config: json.RawMessage(`{"path":"/health"}`)}}
	draft, err = fixture.plans.Save(t.Context(), draft.ID, 41, false, DraftSaveRequest{Revision: draft.Revision, Step: DraftConfiguration, Configuration: &configuration})
	if err != nil {
		t.Fatal(err)
	}
	draft = checkRecoveredDraft(t, fixture, draft)
	ack := []string{}
	for _, finding := range draft.Findings {
		if finding.Severity == PreflightWarning {
			ack = append(ack, finding.Code)
		}
	}
	result, err := fixture.plans.Commit(t.Context(), draft.ID, 41, false, DraftCommitRequest{Revision: draft.Revision, AcknowledgedWarnings: ack})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := fixture.plans.PendingState(t.Context(), result.ProjectID, result.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range pending.Changes {
		if change.Kind == "check" {
			return
		}
	}
	t.Fatalf("review falsely rewrote live checks: %+v", pending)
}
