package deploy

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func approvePreviewFixture(t *testing.T, f *automationFixture, trigger *Trigger, event ProviderEvent) {
	t.Helper()
	if _, _, err := f.automation.EnsurePreview(t.Context(), trigger, event); !errors.Is(err, ErrPreviewApproval) {
		t.Fatalf("unreviewed code did not stop for approval: %v", err)
	}
	approvals, err := f.automation.ListPreviewApprovals(t.Context(), f.projectID)
	if err != nil {
		t.Fatal(err)
	}
	for _, approval := range approvals {
		if approval.TriggerID == trigger.ID && approval.Revision == event.Revision && approval.ProviderRef == strings.TrimPrefix(strings.TrimSuffix(event.PreviewRef, "/head"), "refs/pull/") {
			if _, err := f.automation.ApprovePreview(t.Context(), f.projectID, approval.ID, event.Revision, "admin"); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("approval was not persisted")
}

func TestPreviewApprovalIsBoundToExactRevisionAndProjectBeforeCreatingWork(t *testing.T) {
	f := newAutomationFixture(t)
	created, err := f.automation.CreateTrigger(t.Context(), f.projectID, f.environmentID, TriggerWrite{Name: "Review", Kind: TriggerGitHub, Provider: "github", Enabled: true, Config: TriggerConfig{Repository: "acme/app", Ref: "main", Preview: true}})
	if err != nil {
		t.Fatal(err)
	}
	event := ProviderEvent{PreviewNumber: 1, PreviewRef: "refs/pull/1/head", Revision: strings.Repeat("a", 40), Author: "untrusted", HeadRepository: "untrusted/app"}
	if _, _, err := f.automation.EnsurePreview(t.Context(), &created.Trigger, event); !errors.Is(err, ErrPreviewApproval) {
		t.Fatal(err)
	}
	var environments int
	if err := f.store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_environments WHERE kind='preview'`).Scan(&environments); err != nil || environments != 0 {
		t.Fatalf("unapproved code created an environment: %d, %v", environments, err)
	}
	approvals, err := f.automation.ListPreviewApprovals(t.Context(), f.projectID)
	if err != nil || len(approvals) != 1 {
		t.Fatalf("approval = %v, %v", approvals, err)
	}
	for _, request := range []struct {
		project  int64
		revision string
	}{{f.projectID + 1, event.Revision}, {f.projectID, strings.Repeat("b", 40)}} {
		if _, err := f.automation.ApprovePreview(t.Context(), request.project, approvals[0].ID, request.revision, "admin"); !errors.Is(err, ErrPreviewApproval) {
			t.Fatalf("mismatched approval was accepted: %v", err)
		}
	}
	event.Revision = strings.Repeat("b", 40)
	if _, _, err := f.automation.EnsurePreview(t.Context(), &created.Trigger, event); !errors.Is(err, ErrPreviewApproval) {
		t.Fatal(err)
	}
	if _, err := f.automation.ApprovePreview(t.Context(), f.projectID, approvals[0].ID, approvals[0].Revision, "admin"); !errors.Is(err, ErrPreviewApproval) {
		t.Fatalf("superseded revision was approved: %v", err)
	}
	approvePreviewFixture(t, f, &created.Trigger, event)
	preview, _, err := f.automation.EnsurePreview(t.Context(), &created.Trigger, event)
	if err != nil {
		t.Fatal(err)
	}
	runs := NewOrchestrationStore(f.store)
	request := RunRequest{ProjectID: f.projectID, EnvironmentID: preview.EnvironmentID, PlanRevision: 1, Operation: OperationPreviewUpdate, Trigger: TriggerPreview, Actor: "admin", RequestDigest: "fixture", SourceRevision: strings.Repeat("c", 40)}
	if _, _, err := runs.Enqueue(t.Context(), request); !errors.Is(err, ErrPreviewApproval) {
		t.Fatalf("queue admitted a different revision: %v", err)
	}
}

func TestPreviewNumbersAreScopedToTheirTrigger(t *testing.T) {
	f := newAutomationFixture(t)
	var first *PreviewRef
	for _, name := range []string{"First source", "Second source"} {
		trigger, err := f.automation.CreateTrigger(t.Context(), f.projectID, f.environmentID, TriggerWrite{Name: name, Kind: TriggerGitHub, Provider: "github", Enabled: true, Config: TriggerConfig{Repository: "acme/app", Ref: "main", Preview: true}})
		if err != nil {
			t.Fatal(err)
		}
		event := ProviderEvent{PreviewNumber: 42, PreviewRef: "refs/pull/42/head", Revision: strings.Repeat("a", 40)}
		approvePreviewFixture(t, f, &trigger.Trigger, event)
		preview, _, err := f.automation.EnsurePreview(t.Context(), &trigger.Trigger, event)
		if err != nil {
			t.Fatal(err)
		}
		if first != nil && (preview.EnvironmentID == first.EnvironmentID || preview.EnvironmentSlug == first.EnvironmentSlug) {
			t.Fatal("two triggers reused the same preview environment")
		}
		first = preview
	}
}

func TestPreviewPlansReplaceAllProductionStorageAndOmitHostReleaseTasks(t *testing.T) {
	f := newAutomationFixture(t)
	runtime := RuntimePlanConfig{Strategy: StrategyStopFirst, InternalPort: 8080, HostPort: 8080, Mounts: []RuntimeMount{
		{Source: "production-data", Target: "/data", Ownership: OwnershipManaged},
		{Source: "/srv/production-secrets", Target: "/config", ReadOnly: true, Ownership: OwnershipLinked},
	}}
	build := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", Secrets: []BuildSecretConfig{{Variable: "TOKEN", Step: "install"}}, ReleaseTasks: []ReleaseTaskConfig{{Name: "migration", Command: "./migrate", TimeoutSeconds: 60}}}
	for _, statement := range []string{
		`INSERT INTO deploy_sources(environment_id,revision,kind,config_json,identity_json,digest,created_at) SELECT environment_id,2,kind,config_json,identity_json,digest,created_at FROM deploy_sources WHERE environment_id=?`,
		`UPDATE deploy_environments SET desired_revision=2 WHERE id=?`,
	} {
		if _, err := f.store.DB.Exec(statement, f.environmentID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.store.DB.Exec(`INSERT INTO deploy_build_plans(environment_id,revision,method,config_json,evidence_json,preview,digest,created_at) VALUES(?,2,'recipe',?,'{}','','build',1)`, f.environmentID, string(mustJSON(build))); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB.Exec(`INSERT INTO deploy_runtime_plans(environment_id,revision,config_json,preview,digest,created_at) VALUES(?,2,?,'','runtime',1)`, f.environmentID, string(mustJSON(runtime))); err != nil {
		t.Fatal(err)
	}
	created, err := f.automation.CreateTrigger(t.Context(), f.projectID, f.environmentID, TriggerWrite{Name: "Review", Kind: TriggerGitHub, Provider: "github", Enabled: true, Config: TriggerConfig{Repository: "acme/app", Ref: "main", Preview: true}})
	if err != nil {
		t.Fatal(err)
	}
	event := ProviderEvent{PreviewNumber: 2, PreviewRef: "refs/pull/2/head", Revision: strings.Repeat("a", 40)}
	approvePreviewFixture(t, f, &created.Trigger, event)
	preview, _, err := f.automation.EnsurePreview(t.Context(), &created.Trigger, event)
	if err != nil {
		t.Fatal(err)
	}
	var buildRaw, runtimeRaw string
	if err := f.store.DB.QueryRow(`SELECT b.config_json,r.config_json FROM deploy_build_plans b JOIN deploy_runtime_plans r ON r.environment_id=b.environment_id AND r.revision=b.revision WHERE b.environment_id=?`, preview.EnvironmentID).Scan(&buildRaw, &runtimeRaw); err != nil {
		t.Fatal(err)
	}
	build, runtime = BuildPlanConfig{}, RuntimePlanConfig{}
	if err := json.Unmarshal([]byte(buildRaw), &build); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(runtimeRaw), &runtime); err != nil {
		t.Fatal(err)
	}
	if err := validatePreviewPlan(preview.EnvironmentID, build, runtime); err != nil {
		t.Fatal(err)
	}
	if len(build.ReleaseTasks) != 0 || len(build.Secrets) != 0 || strings.Contains(runtimeRaw, "production") {
		t.Fatalf("production inputs inherited: %s %s", buildRaw, runtimeRaw)
	}
	for _, mount := range runtime.Mounts {
		if mount.Source != previewVolumeName(preview.EnvironmentID, mount.Target) {
			t.Fatal(mount)
		}
	}
	for _, mutate := range []func(*RuntimePlanConfig){
		func(p *RuntimePlanConfig) { p.PreviewIsolation = false },
		func(p *RuntimePlanConfig) {
			p.Mounts = append([]RuntimeMount(nil), p.Mounts...)
			p.Mounts[0].Source = "production-data"
		},
		func(p *RuntimePlanConfig) { p.HostNetwork = true },
		func(p *RuntimePlanConfig) { p.Privileged = true },
	} {
		copy := runtime
		mutate(&copy)
		if err := validatePreviewPlan(preview.EnvironmentID, build, copy); !errors.Is(err, ErrPreviewIsolation) {
			t.Fatalf("unsafe preview accepted: %+v, %v", copy, err)
		}
	}
}
