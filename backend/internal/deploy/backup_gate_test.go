package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type backupGateFake struct {
	requests []BackupGateRequest
	evidence BackupGateEvidence
	err      error
}

func (f *backupGateFake) Evaluate(_ context.Context, request BackupGateRequest) (BackupGateEvidence, error) {
	f.requests = append(f.requests, request)
	return f.evidence, f.err
}

func TestRequiredBackupGateRunsBeforeStatefulChangeAndPersistsSafeEvidence(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	ended := now.Add(-time.Minute)
	fake := &backupGateFake{evidence: BackupGateEvidence{
		JobID: 42, RunID: 91, Status: "success", StartedAt: ended.Add(-time.Second),
		EndedAt: &ended, Fresh: true,
	}}
	executor := &NormalizedStepExecutor{backups: fake}
	plan := &StoredExecutionPlan{
		Runtime: RuntimePlanConfig{Mounts: []RuntimeMount{{Source: "/srv/state", Target: "/data", Ownership: OwnershipLinked}}},
		Dependencies: jsonRawFixture{
			`{"kind":"backup","ownership":"linked","resourceKind":"backup_job","resourceId":"42","config":{"requiredBeforeDeploy":true,"maxAgeSeconds":3600}}`,
			`{"kind":"database","ownership":"linked","resourceKind":"database_connection","resourceId":"19"}`,
			`{"kind":"storage","ownership":"linked","resourceKind":"docker_volume","resourceId":"app-uploads"}`,
		}.raw(),
	}
	result := executor.backupGate(context.Background(), StepExecution{}, plan)
	if result.State != StepPassed || len(fake.requests) != 1 {
		t.Fatalf("backup gate = %#v, requests=%#v", result, fake.requests)
	}
	request := fake.requests[0]
	if request.JobID != 42 || !request.RequiredBeforeDeploy || request.MaxAgeSeconds != 3600 ||
		len(request.PersistentSources) != 2 || request.PersistentSources[0] != "/srv/state" || request.PersistentSources[1] != "app-uploads" ||
		len(request.DatabaseConnections) != 1 || request.DatabaseConnections[0] != 19 {
		t.Fatalf("backup request = %#v", request)
	}
	if string(result.Evidence) == "" || containsAny(string(result.Evidence), "/srv/state", "artifact", "secret") {
		t.Fatalf("unsafe backup evidence = %s", result.Evidence)
	}
}

func TestRequiredBackupFailureBlocksBeforeCandidateStart(t *testing.T) {
	fake := &backupGateFake{
		evidence: BackupGateEvidence{JobID: 7, RunID: 8, Status: "failed"},
		err:      errors.New("fixture backup failed"),
	}
	executor := &NormalizedStepExecutor{backups: fake}
	plan := &StoredExecutionPlan{Dependencies: jsonRawFixture{
		`{"kind":"backup","ownership":"linked","resourceKind":"backup_job","resourceId":"7","config":{"requiredBeforeDeploy":true}}`,
	}.raw()}
	result := executor.backupGate(context.Background(), StepExecution{}, plan)
	if result.State != StepFailed || result.ErrorCode != "backup_required" || len(fake.requests) != 1 {
		t.Fatalf("failed backup gate = %#v, requests=%#v", result, fake.requests)
	}
	if containsAny(result.ErrorMessage, "fixture backup failed") {
		t.Fatalf("raw backup error crossed the step boundary: %q", result.ErrorMessage)
	}
}

func TestRequiredBackupFailureStopsRunBeforeRuntimeAndLeavesLivePointerUntouched(t *testing.T) {
	fixture := newOrchestrationFixture(t)
	environmentID := fixture.addEnvironment(t, "production", EnvironmentProduction)
	run := fixture.enqueue(t, environmentID)
	executor := &recordingExecutor{results: map[StepKey]StepResult{
		StepBackupGate: {State: StepFailed, ErrorCode: "backup_required", ErrorMessage: "required backup did not complete"},
	}}
	engine := NewEngine(fixture.runs, executor, fixedReconciler{}, EngineConfig{
		WorkerID: "backup-gate-fixture", PollEvery: 5 * time.Millisecond,
	}, nil)
	if err := engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := waitForRunState(t, fixture.runs, run.ID, RunFailed)
	if got.TerminalCode != "backup_required" {
		t.Fatalf("terminal code = %q", got.TerminalCode)
	}
	want := []StepKey{}
	for _, key := range DefaultStepKeys {
		want = append(want, key)
		if key == StepBackupGate {
			break
		}
	}
	if keys := executor.executed(); !equalStepKeys(keys, want) {
		t.Fatalf("executed through failed backup gate = %#v, want %#v", keys, want)
	}
	var liveReleaseID, releases, runtimes int
	if err := fixture.store.DB.QueryRow(`SELECT live_release_id FROM deploy_environments WHERE id = ?`, environmentID).Scan(&liveReleaseID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_releases WHERE environment_id = ?`, environmentID).Scan(&releases); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.DB.QueryRow(`
		SELECT COUNT(*) FROM deploy_release_runtimes rr
		JOIN deploy_releases r ON r.id = rr.release_id
		WHERE r.environment_id = ?`, environmentID).Scan(&runtimes); err != nil {
		t.Fatal(err)
	}
	if liveReleaseID != 0 || releases != 0 || runtimes != 0 {
		t.Fatalf("backup failure changed live state: live=%d releases=%d runtimes=%d", liveReleaseID, releases, runtimes)
	}
	shutdownEngine(t, engine)
}

// jsonRawFixture keeps raw JSON fixtures readable without sharing a helper
// with production code.
type jsonRawFixture []string

func (values jsonRawFixture) raw() []json.RawMessage {
	result := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		result = append(result, json.RawMessage(value))
	}
	return result
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
