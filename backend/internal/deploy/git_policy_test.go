package deploy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

type gitPathsFake struct {
	gitRevisionFake
	paths        []string
	pathsErr     error
	comparedFrom string
}

func (r *gitPathsFake) ResolveGitChangedPaths(_ context.Context, _ DraftSourceConfig, before, _ string) ([]string, error) {
	r.comparedFrom = before
	return r.paths, r.pathsErr
}

func TestGitPolicyPollingAndHooksShareDecisions(t *testing.T) {
	f, _, watcher := gitWatchFixture(t)
	ctx := context.Background()
	a, b, c := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	enqueueGitFixture(t, f, a, TriggerManual)
	policy, err := f.runs.SetGitDeploymentPolicy(ctx, f.projectID, f.envID, GitDeploymentPolicy{Automatic: true, WatchInclude: []string{"services/api/**"}, WatchExclude: []string{"services/api/docs/**"}})
	if err != nil {
		t.Fatal(err)
	}
	resolver := &gitPathsFake{gitRevisionFake: gitRevisionFake{revision: b}, paths: []string{"services/api/docs/readme.md"}}
	watcher.sources = resolver
	event := ProviderEvent{Repository: "acme/app", Ref: "refs/heads/main", Revision: b, ChangedPaths: []string{"services/api/main.go"}}
	decision, err := watcher.EvaluateWebhook(ctx, f.projectID, f.envID, event)
	if err != nil || decision.Allowed || decision.Reason != "watch_paths_ignored" {
		t.Fatalf("hook decision=%+v, %v", decision, err)
	}
	if err := watcher.poll(ctx); err != nil {
		t.Fatal(err)
	}
	status, _ := f.runs.GitWatchStatus(ctx, f.projectID, f.envID)
	if status.Reason != decision.Reason || status.Revision != b {
		t.Fatalf("poll decision=%+v", status)
	}
	assertGitRunCount(t, f, 1)
	resolver.revision, resolver.paths, event.Revision = c, []string{"services/api/main.go"}, c
	decision, err = watcher.EvaluateWebhook(ctx, f.projectID, f.envID, event)
	if err != nil || !decision.Allowed || decision.Reason != "watched_paths_changed" || resolver.comparedFrom != a {
		t.Fatalf("relevant decision=%+v, baseline=%s, %v", decision, resolver.comparedFrom, err)
	}
	// Both paths race after evaluating the same complete change set. Queue
	// admission owns the commit fence; neither caller needs an in-memory lock.
	var group sync.WaitGroup
	errorsCh := make(chan error, 2)
	group.Add(2)
	go func() {
		defer group.Done()
		_, err := watcher.dispatch(ctx, decision.Target, c, "poll-race", nil)
		errorsCh <- err
	}()
	go func() {
		defer group.Done()
		_, _, err := f.runs.Enqueue(ctx, RunRequest{ProjectID: f.projectID, EnvironmentID: f.envID, PlanRevision: 1, Operation: OperationDeploy, Trigger: TriggerGitHub, Actor: "hook", RequestDigest: "hook", SourceRevision: c, ExpectedGitPolicy: policy.Key()})
		errorsCh <- err
	}()
	group.Wait()
	for range 2 {
		if err := <-errorsCh; err != nil {
			t.Fatal(err)
		}
	}
	assertGitRunCount(t, f, 2)
	if err := watcher.poll(ctx); err != nil {
		t.Fatal(err)
	}
	assertGitRunCount(t, f, 2)
	event.Revision = b
	late, err := watcher.EvaluateWebhook(ctx, f.projectID, f.envID, event)
	if err != nil || late.Allowed || late.Reason != "superseded_revision" {
		t.Fatalf("late event=%+v, %v", late, err)
	}
	event.Ref = "release"
	if _, err := watcher.EvaluateWebhook(ctx, f.projectID, f.envID, event); !errors.Is(err, ErrWrongRef) {
		t.Fatalf("wrong branch=%v", err)
	}
	event.Ref, event.Repository = "main", "other/repo"
	if _, err := watcher.EvaluateWebhook(ctx, f.projectID, f.envID, event); !errors.Is(err, ErrWrongRepository) {
		t.Fatalf("wrong repository=%v", err)
	}
}

func TestGitPolicyManualOnlyAndConcurrentPolicyEditsFenceAdmission(t *testing.T) {
	f, resolver, watcher := gitWatchFixture(t)
	ctx := context.Background()
	enqueueGitFixture(t, f, strings.Repeat("a", 40), TriggerManual)
	prior, err := f.runs.GitDeploymentPolicy(ctx, f.projectID, f.envID)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := f.runs.SetGitDeploymentPolicy(ctx, f.projectID, f.envID, GitDeploymentPolicy{Automatic: false})
	if err != nil {
		t.Fatal(err)
	}
	resolver.revision = strings.Repeat("b", 40)
	if err := watcher.poll(ctx); err != nil {
		t.Fatal(err)
	}
	decision, err := watcher.EvaluateWebhook(ctx, f.projectID, f.envID, ProviderEvent{Revision: resolver.revision})
	if err != nil || decision.Allowed || decision.Reason != "manual_only" {
		t.Fatalf("manual decision=%+v %v", decision, err)
	}
	for _, trigger := range []TriggerKind{TriggerGitPush, TriggerGitHub, TriggerGitLab, TriggerGitea, TriggerBitbucket, TriggerGenericHook, TriggerAPI, TriggerSchedule} {
		_, _, err := f.runs.Enqueue(ctx, RunRequest{ProjectID: f.projectID, EnvironmentID: f.envID, PlanRevision: 1, Operation: OperationDeploy, Trigger: trigger, Actor: "automatic", RequestDigest: "fixture", SourceRevision: resolver.revision})
		if !errors.Is(err, ErrGitManualOnly) {
			t.Fatalf("%s admission=%v", trigger, err)
		}
	}
	assertGitRunCount(t, f, 1)
	enqueueGitFixture(t, f, resolver.revision, TriggerManual)
	assertGitRunCount(t, f, 2)
	if _, err := f.runs.SetGitDeploymentPolicy(ctx, f.projectID, f.envID, prior); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale edit=%v", err)
	}
	policy.Automatic = true
	if _, err := f.runs.SetGitDeploymentPolicy(ctx, f.projectID, f.envID, policy); err != nil {
		t.Fatal(err)
	}
	_, _, err = f.runs.Enqueue(ctx, RunRequest{ProjectID: f.projectID, EnvironmentID: f.envID, PlanRevision: 1, Operation: OperationDeploy, Trigger: TriggerGitPush, Actor: "automatic", RequestDigest: "fixture", SourceRevision: strings.Repeat("c", 40), ExpectedGitPolicy: prior.Key()})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale observation=%v", err)
	}
}

func TestGitPolicyIncompleteDiffDoesNotAdvanceCursor(t *testing.T) {
	f, _, watcher := gitWatchFixture(t)
	ctx := context.Background()
	enqueueGitFixture(t, f, strings.Repeat("a", 40), TriggerManual)
	_, err := f.runs.SetGitDeploymentPolicy(ctx, f.projectID, f.envID, GitDeploymentPolicy{Automatic: true, WatchInclude: []string{"app/**"}})
	if err != nil {
		t.Fatal(err)
	}
	resolver := &gitPathsFake{gitRevisionFake: gitRevisionFake{revision: strings.Repeat("b", 40)}, pathsErr: fmt.Errorf("credential must never reach status")}
	watcher.sources = resolver
	if err := watcher.poll(ctx); err != nil {
		t.Fatal(err)
	}
	status, _ := f.runs.GitWatchStatus(ctx, f.projectID, f.envID)
	if status.Status != "unavailable" || status.Reason != "changes_unavailable" || status.Revision != strings.Repeat("a", 40) || strings.Contains(string(mustJSON(status)), "credential") {
		t.Fatalf("comparison failure=%+v", status)
	}
	assertGitRunCount(t, f, 1)
	resolver.pathsErr, resolver.paths = nil, []string{"app/main.go"}
	if err := watcher.poll(ctx); err != nil {
		t.Fatal(err)
	}
	assertGitRunCount(t, f, 2)
}

func TestGitPolicyReconsidersIgnoredHeadAfterManualDeploymentChangesBaseline(t *testing.T) {
	f, _, watcher := gitWatchFixture(t)
	ctx := context.Background()
	a, b, c := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	enqueueGitFixture(t, f, a, TriggerManual)
	if _, err := f.runs.SetGitDeploymentPolicy(ctx, f.projectID, f.envID, GitDeploymentPolicy{Automatic: true, WatchInclude: []string{"app/**"}}); err != nil {
		t.Fatal(err)
	}
	resolver := &gitPathsFake{gitRevisionFake: gitRevisionFake{revision: b}, paths: []string{"docs/readme.md"}}
	watcher.sources = resolver
	if err := watcher.poll(ctx); err != nil {
		t.Fatal(err)
	}
	assertGitRunCount(t, f, 1)
	enqueueGitFixture(t, f, c, TriggerManual)
	resolver.paths = []string{"app/main.go"}
	if err := watcher.poll(ctx); err != nil {
		t.Fatal(err)
	}
	if resolver.comparedFrom != c {
		t.Fatalf("used obsolete comparison baseline %s", resolver.comparedFrom)
	}
	assertGitRunCount(t, f, 3)
}

func TestGitPolicyPreservesLegacyFiltersAndManualMode(t *testing.T) {
	f := newAutomationFixture(t)
	runs := NewOrchestrationStore(f.store)
	ctx := context.Background()
	in := TriggerWrite{Name: "GitHub", Kind: TriggerGitHub, Provider: "github", Enabled: true, Config: TriggerConfig{Repository: "acme/app", Ref: "main", WatchInclude: []string{"app/**"}}}
	created, err := f.automation.CreateTrigger(ctx, f.projectID, f.environmentID, in)
	if err != nil {
		t.Fatal(err)
	}
	p, err := runs.GitDeploymentPolicy(ctx, f.projectID, f.environmentID)
	if err != nil || len(p.WatchInclude) != 1 || p.WatchInclude[0] != "app/**" {
		t.Fatalf("shared hook policy=%+v %v", p, err)
	}
	p.Automatic = false
	if _, err := runs.SetGitDeploymentPolicy(ctx, f.projectID, f.environmentID, p); err != nil {
		t.Fatal(err)
	}
	in.Config.WatchInclude = []string{"services/**"}
	if _, err := f.automation.UpdateTrigger(ctx, f.projectID, f.environmentID, created.Trigger.ID, in); err != nil {
		t.Fatal(err)
	}
	p, _ = runs.GitDeploymentPolicy(ctx, f.projectID, f.environmentID)
	if p.Automatic || p.WatchInclude[0] != "services/**" {
		t.Fatalf("hook edit overrode manual mode=%+v", p)
	}
	if _, err := f.store.DB.Exec(`DELETE FROM deploy_git_policies`); err != nil {
		t.Fatal(err)
	}
	p, _ = runs.GitDeploymentPolicy(ctx, f.projectID, f.environmentID)
	if !p.Inherited || p.Conflict || p.WatchInclude[0] != "services/**" {
		t.Fatalf("legacy inheritance=%+v", p)
	}
	in.Name, in.Config.WatchInclude = "Other", nil
	other, err := f.automation.CreateTrigger(ctx, f.projectID, f.environmentID, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB.Exec(`UPDATE deploy_triggers SET config_json=? WHERE id=?`, `{"repository":"acme/app","ref":"main","watchInclude":["other/**"]}`, other.Trigger.ID); err != nil {
		t.Fatal(err)
	}
	p, _ = runs.GitDeploymentPolicy(ctx, f.projectID, f.environmentID)
	if !p.Conflict {
		t.Fatalf("conflicting legacy policies allowed=%+v", p)
	}
	for _, invalid := range []string{"../app/**", "/etc/**", "app/../**", "[", "app\\*"} {
		if _, err := canonicalWatchPatterns([]string{invalid}); err == nil {
			t.Fatalf("invalid glob accepted: %q", invalid)
		}
	}
}
