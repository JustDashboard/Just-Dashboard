package deploy

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetiredImportRefusesNewRunsAndKeepsOrdinaryProjects(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	ctx := context.Background()
	if err := RequireSupportedProject(ctx, f.store.DB, f.projectID); err != nil {
		t.Fatal(err)
	}
	run := f.enqueue(t, environmentID)
	if _, err := f.store.DB.Exec(`UPDATE deploy_runs SET operation = 'import_adopt' WHERE id = ?`, run.ID); err != nil {
		t.Fatal(err)
	}
	_, _, err := f.runs.Enqueue(ctx, RunRequest{ProjectID: f.projectID, EnvironmentID: environmentID, Operation: OperationDeploy, Trigger: TriggerManual, Actor: "tester"})
	if !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("retired import admission: %v", err)
	}
	var count int
	if err := f.store.DB.QueryRow(`SELECT count(*) FROM deploy_runs WHERE project_id = ?`, f.projectID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("refused admission created runs: %d", count)
	}
}

func TestRetiredObservationRefusesButExistingCheckoutRemainsSupported(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	if _, err := f.store.DB.Exec(`INSERT INTO deploy_sources(environment_id, revision, kind, config_json, identity_json, digest, created_at)
 VALUES(?, 1, 'import', '{"mode":"existing_container"}', '{}', 'test', 1)`, environmentID); err != nil {
		t.Fatal(err)
	}
	if err := RequireSupportedProject(t.Context(), f.store.DB, f.projectID); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("observation guard: %v", err)
	}
	f = newOrchestrationFixture(t)
	environmentID = f.addEnvironment(t, "production", EnvironmentProduction)
	if _, err := f.store.DB.Exec(`INSERT INTO deploy_sources(environment_id, revision, kind, config_json, identity_json, digest, created_at)
 VALUES(?, 1, 'import', '{"mode":"existing_checkout"}', '{}', 'test', 1)`, environmentID); err != nil {
		t.Fatal(err)
	}
	if err := RequireSupportedProject(t.Context(), f.store.DB, f.projectID); err != nil {
		t.Fatalf("checkout compatibility: %v", err)
	}
}

func TestRetiredImportedQueuedRunExecutesNoSteps(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	baseline := f.enqueue(t, environmentID)
	queued := f.enqueue(t, environmentID)
	if _, err := f.store.DB.Exec(`UPDATE deploy_runs SET operation = 'import_adopt', state = 'succeeded', status = 'success' WHERE id = ?`, baseline.ID); err != nil {
		t.Fatal(err)
	}
	executor := &recordingExecutor{}
	engine := NewEngine(f.runs, executor, fixedReconciler{}, EngineConfig{WorkerID: "retired-import"}, nil)
	lease, err := f.runs.ClaimNext(t.Context(), "retired-import", QueueBudget{}, time.Minute)
	if err != nil || lease == nil || lease.RunID != queued.ID {
		t.Fatalf("claim: %#v %v", lease, err)
	}
	if err := engine.execute(t.Context(), *lease); err != nil {
		t.Fatal(err)
	}
	run, err := f.runs.Run(t.Context(), queued.ID)
	if err != nil || run.State != RunFailed || run.TerminalCode != "import_removed" {
		t.Fatalf("queued retirement: %#v %v", run, err)
	}
	if keys := executor.executed(); len(keys) != 0 {
		t.Fatalf("retired run executed: %v", keys)
	}
}

func TestRetirementPreservesLiteralVariablesThatLookLikeReferences(t *testing.T) {
	f := newPlanningStoreFixture(t)
	projectID, environmentID := insertConfigurationFixture(t, f)
	literal := "${{variable.MISSING}}"
	_, err := f.plans.PutVariable(t.Context(), projectID, environmentID, "LITERAL", "tester", VariableWriteRequest{Revision: 1, Value: &literal, Sensitivity: "plain", Scopes: []string{"runtime"}})
	if err != nil {
		t.Fatal(err)
	}
	values, err := f.plans.OpenScopedVariables(t.Context(), environmentID, "runtime")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		if value.Name == "LITERAL" {
			if value.Value != literal {
				t.Fatalf("literal changed: %q", value.Value)
			}
			return
		}
	}
	t.Fatal("literal variable missing")
}
