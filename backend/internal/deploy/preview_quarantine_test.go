package deploy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type quarantineFixture struct {
	*automationFixture
	previewID, releaseID, runID int64
	trigger                     Trigger
	event                       ProviderEvent
	runs                        *OrchestrationStore
}

func newQuarantineFixture(t *testing.T) *quarantineFixture {
	t.Helper()
	f := newAutomationFixture(t)
	trigger, err := f.automation.CreateTrigger(t.Context(), f.projectID, f.environmentID, TriggerWrite{Name: "Legacy preview", Kind: TriggerGitHub, Provider: "github", Enabled: true, Config: TriggerConfig{Repository: "acme/app", Ref: "main", Preview: true}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.store.DB.Exec(`INSERT INTO deploy_environments(id,project_id,name,slug,kind,created_at,updated_at) VALUES(?,?,'Legacy preview','pr-42','preview',1,1)`, time.Now().UnixNano(), f.projectID)
	if err != nil {
		t.Fatal(err)
	}
	previewID, _ := result.LastInsertId()
	for _, statement := range []string{
		`INSERT INTO deploy_sources(environment_id,revision,kind,config_json,identity_json,digest,created_at) SELECT ?,revision,kind,config_json,identity_json,digest,created_at FROM deploy_sources WHERE environment_id=?`,
		`INSERT INTO deploy_build_plans(environment_id,revision,method,config_json,evidence_json,preview,digest,created_at) SELECT ?,revision,method,config_json,evidence_json,preview,digest,created_at FROM deploy_build_plans WHERE environment_id=?`,
		`INSERT INTO deploy_runtime_plans(environment_id,revision,config_json,preview,digest,created_at) SELECT ?,revision,config_json,preview,digest,created_at FROM deploy_runtime_plans WHERE environment_id=?`,
	} {
		if _, err := f.store.DB.Exec(statement, previewID, f.environmentID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.store.DB.Exec(`INSERT INTO deploy_preview_refs(trigger_id,provider_ref,environment_id,state,updated_at) VALUES(?,'42',?,'open',1)`, trigger.Trigger.ID, previewID); err != nil {
		t.Fatal(err)
	}
	result, err = f.store.DB.Exec(`INSERT INTO deploy_runs(project_id,environment_id,started_at,status,state,plan_revision) VALUES(?,?,1,'running','running',1)`, f.projectID, previewID)
	if err != nil {
		t.Fatal(err)
	}
	runID, _ := result.LastInsertId()
	if _, err := f.store.DB.Exec(`INSERT INTO deploy_steps(run_id,step_key,ordinal,status,started_at) VALUES(?,'start_candidate',1,'running',1)`, runID); err != nil {
		t.Fatal(err)
	}
	result, err = f.store.DB.Exec(`INSERT INTO deploy_releases(project_id,environment_id,release_number,run_id,state,created_at) VALUES(?,?,1,?,'live',1)`, f.projectID, previewID, runID)
	if err != nil {
		t.Fatal(err)
	}
	releaseID, _ := result.LastInsertId()
	if _, err := f.store.DB.Exec(`INSERT INTO deploy_release_runtimes(release_id,environment_id,kind,runtime_id,state,created_at,updated_at) VALUES(?,?,'container','legacy-owned-container','live',1,1)`, releaseID, previewID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB.Exec(`UPDATE deploy_environments SET live_release_id=? WHERE id=?`, releaseID, previewID); err != nil {
		t.Fatal(err)
	}
	return &quarantineFixture{automationFixture: f, previewID: previewID, releaseID: releaseID, runID: runID, trigger: trigger.Trigger,
		event: ProviderEvent{PreviewNumber: 42, PreviewRef: "refs/pull/42/head", Revision: strings.Repeat("b", 40)}, runs: NewOrchestrationStore(f.store)}
}

type quarantineOwnerStub struct {
	err     error
	targets []PreviewQuarantineTarget
}

func (o *quarantineOwnerStub) QuarantinePreview(_ context.Context, target PreviewQuarantineTarget) error {
	o.targets = append(o.targets, target)
	return o.err
}

type quarantineRoutesStub struct {
	err   error
	names []string
}

func (r *quarantineRoutesStub) RemoveDeploymentRoute(_ context.Context, name string) error {
	r.names = append(r.names, name)
	return r.err
}

func TestPreviewQuarantineFencesLegacyWorkAndRetriesBeforeClearing(t *testing.T) {
	f := newQuarantineFixture(t)
	ids, err := f.runs.PreparePreviewQuarantines(t.Context())
	if err != nil || !reflect.DeepEqual(ids, []int64{f.previewID}) {
		t.Fatalf("prepared=%v, %v", ids, err)
	}
	var state, code, step string
	if err := f.store.DB.QueryRow(`SELECT state,terminal_code FROM deploy_runs WHERE id=?`, f.runID).Scan(&state, &code); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DB.QueryRow(`SELECT status FROM deploy_steps WHERE run_id=?`, f.runID).Scan(&step); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || code != "preview_quarantined" || step != "failed" {
		t.Fatalf("legacy work still runnable: %s %s %s", state, code, step)
	}
	approvePreviewFixture(t, f.automationFixture, &f.trigger, f.event)
	if _, _, err := f.automation.EnsurePreview(t.Context(), &f.trigger, f.event); !errors.Is(err, ErrPreviewIsolation) {
		t.Fatalf("configured before isolation: %v", err)
	}
	owner := &quarantineOwnerStub{err: errors.New("Docker unavailable")}
	routes := &quarantineRoutesStub{}
	controller := NewPreviewQuarantineController(f.runs, owner, routes, nil, nil)
	if err := controller.Reconcile(t.Context()); err == nil {
		t.Fatal("missing failure")
	}
	if len(routes.names) != 1 || routes.names[0] != deploymentRouteName(f.previewID) {
		t.Fatalf("did not withdraw only preview route: %v", routes.names)
	}
	previews, err := f.automation.ListPreviews(t.Context(), f.projectID)
	if err != nil || len(previews) != 1 || previews[0].IsolationStatus != "pending" {
		t.Fatalf("failure lost admission block: %v %v", previews, err)
	}
	owner.err = nil
	routes.err = errors.New("proxy unavailable")
	if err := controller.Reconcile(t.Context()); err == nil {
		t.Fatal("route failure was accepted")
	}
	routes.err = nil
	if err := controller.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(owner.targets) != 3 || !reflect.DeepEqual(owner.targets[0].ReleaseIDs, []int64{f.releaseID}) || !reflect.DeepEqual(owner.targets[0].ContainerIDs, []string{"legacy-owned-container"}) {
		t.Fatalf("lost exact runtime ownership: %v", owner.targets)
	}
	var liveID int64
	if err := f.store.DB.QueryRow(`SELECT live_release_id FROM deploy_environments WHERE id=?`, f.previewID).Scan(&liveID); err != nil {
		t.Fatal(err)
	}
	if liveID != 0 {
		t.Fatal("stopped preview still marked live")
	}
	if err := f.store.DB.QueryRow(`SELECT state FROM deploy_releases WHERE id=?`, f.releaseID).Scan(&state); err != nil || state != "quarantined" {
		t.Fatalf("old release remained activatable: %s %v", state, err)
	}
	if _, err := f.runs.CloneCandidateRelease(t.Context(), EngineRun{ProjectID: f.projectID, EnvironmentID: f.previewID, PlanRevision: 1}, "", f.releaseID); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("legacy rollback allowed: %v", err)
	}
	if _, _, err := f.automation.EnsurePreview(t.Context(), &f.trigger, f.event); err != nil {
		t.Fatal(err)
	}
	previews, err = f.automation.ListPreviews(t.Context(), f.projectID)
	if err != nil || previews[0].IsolationStatus != "cleared" {
		t.Fatalf("approved setup could not recover: %v %v", previews, err)
	}
	ids, err = f.runs.PreparePreviewQuarantines(t.Context())
	if err != nil || len(ids) != 0 {
		t.Fatalf("quarantined twice after review: %v %v", ids, err)
	}
	var productionKind string
	if err := f.store.DB.QueryRow(`SELECT kind FROM deploy_environments WHERE id=?`, f.environmentID).Scan(&productionKind); err != nil || productionKind != "production" {
		t.Fatalf("production changed: %s %v", productionKind, err)
	}
}

func TestPreviewQuarantineLeavesReviewedIsolatedPreviewRunning(t *testing.T) {
	f := newAutomationFixture(t)
	trigger, err := f.automation.CreateTrigger(t.Context(), f.projectID, f.environmentID, TriggerWrite{Name: "Safe preview", Kind: TriggerGitHub, Provider: "github", Enabled: true, Config: TriggerConfig{Repository: "acme/app", Ref: "main", Preview: true}})
	if err != nil {
		t.Fatal(err)
	}
	event := ProviderEvent{PreviewNumber: 1, PreviewRef: "refs/pull/1/head", Revision: strings.Repeat("a", 40)}
	approvePreviewFixture(t, f, &trigger.Trigger, event)
	if _, _, err := f.automation.EnsurePreview(t.Context(), &trigger.Trigger, event); err != nil {
		t.Fatal(err)
	}
	runs := NewOrchestrationStore(f.store)
	ids, err := runs.PreparePreviewQuarantines(t.Context())
	if err != nil || len(ids) != 0 {
		t.Fatalf("safe preview stopped: %v %v", ids, err)
	}
	// A pending new head does not revoke the review of the still-running head.
	event.Revision = strings.Repeat("b", 40)
	if _, _, err := f.automation.EnsurePreview(t.Context(), &trigger.Trigger, event); !errors.Is(err, ErrPreviewApproval) {
		t.Fatal(err)
	}
	ids, err = runs.PreparePreviewQuarantines(t.Context())
	if err != nil || len(ids) != 0 {
		t.Fatalf("new head quarantined reviewed old head: %v %v", ids, err)
	}
}

func TestQuarantinedRuntimeCannotRestart(t *testing.T) {
	if err := NewDockerRuntimeOwner(nil).StartExisting(t.Context(), ReleaseRuntime{State: "quarantined"}, nil, nil); !errors.Is(err, ErrPreviewIsolation) {
		t.Fatalf("restart accepted: %v", err)
	}
}

func TestPreviewQuarantineResumesPersistedIntentAtStartup(t *testing.T) {
	f := newQuarantineFixture(t)
	if _, err := f.runs.PreparePreviewQuarantines(t.Context()); err != nil {
		t.Fatal(err)
	}
	owner, routes := &quarantineOwnerStub{}, &quarantineRoutesStub{}
	var phases []string
	controller := NewPreviewQuarantineController(NewOrchestrationStore(f.store), owner, routes,
		func(_ context.Context, id int64, phase string, success bool) {
			if id != f.previewID || !success {
				t.Errorf("unexpected audit: %d %s %v", id, phase, success)
			}
			phases = append(phases, phase)
		}, nil)
	if err := controller.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	controller.Stop()
	controller.Stop()
	if !reflect.DeepEqual(phases, []string{"stop", "route", "complete"}) {
		t.Fatalf("pending intent was not recovered with audit: %v", phases)
	}
	previews, err := f.automation.ListPreviews(t.Context(), f.projectID)
	if err != nil || previews[0].IsolationStatus != "quarantined" {
		t.Fatalf("startup did not settle isolation: %v %v", previews, err)
	}
}
