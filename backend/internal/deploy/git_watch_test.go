package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type gitRevisionFake struct {
	revision string
	err      error
}

func (r *gitRevisionFake) ResolveGitRevision(context.Context, DraftSourceConfig) (string, error) {
	return r.revision, r.err
}

func gitWatchFixture(t *testing.T) (*releaseStoreFixture, *gitRevisionFake, *GitWatcher) {
	t.Helper()
	f := newReleaseStoreFixture(t)
	f.addPlan(t, 1, strings.Repeat("a", 40))
	source := DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://github.com/acme/app.git", Ref: "main"}
	identity := SourceIdentity{Kind: SourceGit, Remote: source.URL, Repository: "acme/app", Ref: "main", Revision: strings.Repeat("a", 40)}
	if _, err := f.base.DB.Exec(`DELETE FROM deploy_sources WHERE environment_id=?`, f.envID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.base.DB.Exec(`INSERT INTO deploy_sources(environment_id,revision,kind,config_json,identity_json,digest,created_at) VALUES(?,1,'git',?,?,?,?)`,
		f.envID, string(mustJSON(source)), string(mustJSON(identity)), digestBytes(mustJSON(source), mustJSON(identity)), f.now.Unix()); err != nil {
		t.Fatal(err)
	}
	resolver := &gitRevisionFake{revision: identity.Revision}
	watcher := NewGitWatcher(f.runs, resolver, func(ctx context.Context, target GitWatchTarget, revision, key string) (*EngineRun, error) {
		run, _, err := f.runs.Enqueue(ctx, RunRequest{ProjectID: target.ProjectID, EnvironmentID: target.EnvironmentID,
			Operation: OperationDeploy, Trigger: TriggerGitPush, Actor: "git-monitor", IdempotencyKey: key,
			RequestDigest: fmt.Sprint(target.PlanRevision), PlanRevision: target.PlanRevision,
			ExpectedPlanRevision: target.PlanRevision, SourceRevision: revision})
		return run, err
	})
	return f, resolver, watcher
}

func enqueueGitFixture(t *testing.T, f *releaseStoreFixture, revision string, trigger TriggerKind) *EngineRun {
	t.Helper()
	run, _, err := f.runs.Enqueue(context.Background(), RunRequest{ProjectID: f.projectID, EnvironmentID: f.envID,
		Operation: OperationDeploy, Trigger: trigger, Actor: "test", RequestDigest: "fixture", PlanRevision: 1, SourceRevision: revision})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func assertGitRunCount(t *testing.T, f *releaseStoreFixture, want int) {
	t.Helper()
	var got int
	if err := f.base.DB.QueryRow(`SELECT COUNT(*) FROM deploy_runs WHERE environment_id=?`, f.envID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("runs=%d, want %d", got, want)
	}
}

func TestGitWatcherDefaultsPinCommitsAndRememberFailedAttempts(t *testing.T) {
	f, resolver, watcher := gitWatchFixture(t)
	ctx := context.Background()
	status, err := f.runs.GitWatchStatus(ctx, f.projectID, f.envID)
	if err != nil || !status.Automatic || status.Status != "awaiting_first_deployment" {
		t.Fatalf("initial status=%+v, %v", status, err)
	}
	if err := watcher.poll(ctx); err != nil {
		t.Fatal(err)
	}
	assertGitRunCount(t, f, 0)
	// Older runs have no new source_revision column value. Their saved source
	// identity still establishes the baseline when the feature is upgraded.
	enqueueGitFixture(t, f, "", TriggerManual)
	if err := watcher.poll(ctx); err != nil {
		t.Fatal(err)
	}
	assertGitRunCount(t, f, 1)

	resolver.revision = strings.Repeat("b", 40)
	if err := watcher.poll(ctx); err != nil {
		t.Fatal(err)
	}
	assertGitRunCount(t, f, 2)
	status, err = f.runs.GitWatchStatus(ctx, f.projectID, f.envID)
	if err != nil || status.Status != "watching" || status.Revision != resolver.revision {
		t.Fatalf("watch status=%+v, %v", status, err)
	}
	run, err := f.runs.Run(ctx, status.RunID)
	if err != nil {
		t.Fatal(err)
	}
	resolver.revision = strings.Repeat("c", 40)
	plan, err := f.runs.ExecutionPlan(ctx, *run)
	if err != nil || plan.SourceIdentity.Revision != strings.Repeat("b", 40) {
		t.Fatalf("pinned plan=%+v, %v", plan, err)
	}
	if _, err := f.base.DB.Exec(`UPDATE deploy_runs SET source_revision=? WHERE id=?`, resolver.revision, run.ID); err == nil {
		t.Fatal("source revision was mutable")
	}
	if _, err := f.runs.RequestCancellation(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	retry, _, err := f.runs.Retry(ctx, run.ID, "operator", "retry")
	if err != nil || retry.SourceRevision != run.SourceRevision {
		t.Fatalf("retry=%+v, %v", retry, err)
	}
	assertGitRunCount(t, f, 3)

	resolver.revision = strings.Repeat("b", 40)
	restarted := NewGitWatcher(NewOrchestrationStore(f.base), resolver, watcher.dispatch)
	if err := restarted.poll(ctx); err != nil {
		t.Fatal(err)
	}
	assertGitRunCount(t, f, 3)
	resolver.revision = strings.Repeat("c", 40)
	if err := watcher.poll(ctx); err != nil {
		t.Fatal(err)
	}
	assertGitRunCount(t, f, 4)
	// Force-pushing the branch back to an earlier object is still a new change.
	resolver.revision = strings.Repeat("b", 40)
	if err := watcher.poll(ctx); err != nil {
		t.Fatal(err)
	}
	assertGitRunCount(t, f, 5)
	if _, err := NewStore(f.base, nil, nil).Archive(ctx, f.projectID); err != nil {
		t.Fatal(err)
	}
	resolver.revision = strings.Repeat("d", 40)
	if err := watcher.poll(ctx); err != nil {
		t.Fatal(err)
	}
	assertGitRunCount(t, f, 5)
}

func TestGitWatcherErrorsAndConcurrentProviderDeliveryDoNotDuplicateRuns(t *testing.T) {
	f, resolver, watcher := gitWatchFixture(t)
	ctx := context.Background()
	enqueueGitFixture(t, f, strings.Repeat("a", 40), TriggerManual)
	resolver.err = errors.New("private credential must not be exposed")
	if err := watcher.poll(ctx); err != nil {
		t.Fatal(err)
	}
	status, err := f.runs.GitWatchStatus(ctx, f.projectID, f.envID)
	if err != nil || status.Status != "unavailable" || strings.Contains(string(mustJSON(status)), "credential") {
		t.Fatalf("error status=%+v, %v", status, err)
	}
	assertGitRunCount(t, f, 1)
	resolver.err, resolver.revision = nil, strings.Repeat("b", 40)
	targets, err := f.runs.gitWatchTargets(ctx, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	errorsCh := make(chan error, 3)
	for i := 0; i < 2; i++ {
		group.Add(1)
		go func() { defer group.Done(); errorsCh <- watcher.check(ctx, targets[0]) }()
	}
	group.Add(1)
	go func() {
		defer group.Done()
		_, _, err := f.runs.Enqueue(ctx, RunRequest{ProjectID: f.projectID, EnvironmentID: f.envID,
			Operation: OperationDeploy, Trigger: TriggerGitHub, Actor: "webhook", RequestDigest: "provider", PlanRevision: 1, SourceRevision: resolver.revision})
		errorsCh <- err
	}()
	group.Wait()
	for i := 0; i < 3; i++ {
		if err := <-errorsCh; err != nil {
			t.Fatal(err)
		}
	}
	assertGitRunCount(t, f, 2)
	if err := watcher.poll(ctx); err != nil {
		t.Fatal(err)
	}
	assertGitRunCount(t, f, 2)
}

func TestGitWatcherRecoversEnqueueBeforeCursorWrite(t *testing.T) {
	f, resolver, watcher := gitWatchFixture(t)
	ctx := context.Background()
	enqueueGitFixture(t, f, strings.Repeat("a", 40), TriggerManual)
	resolver.revision = strings.Repeat("b", 40)
	dispatch := watcher.dispatch
	watcher.dispatch = func(ctx context.Context, target GitWatchTarget, revision, key string) (*EngineRun, error) {
		run, err := dispatch(ctx, target, revision, key)
		if err != nil {
			return nil, err
		}
		return run, errors.New("simulated interruption after durable enqueue")
	}
	if err := watcher.poll(ctx); err != nil {
		t.Fatal(err)
	}
	assertGitRunCount(t, f, 2)
	watcher.dispatch = dispatch
	if err := watcher.poll(ctx); err != nil {
		t.Fatal(err)
	}
	assertGitRunCount(t, f, 2)
	status, err := f.runs.GitWatchStatus(ctx, f.projectID, f.envID)
	if err != nil || status.Status != "watching" || status.Revision != resolver.revision {
		t.Fatalf("recovered status=%+v, %v", status, err)
	}
}

func TestResolveGitRevisionOnlyReadsExactBranch(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\n[ \"$1\" = ls-remote ] && [ \"$2\" = --exit-code ] && [ \"$3\" = --refs ] && [ \"$5\" = refs/heads/main ] || exit 9\nprintf '%s\\t%s\\n' aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa refs/heads/main\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(), nil, nil)
	source := DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://github.com/acme/app.git", Ref: "main"}
	revision, err := analyzer.ResolveGitRevision(context.Background(), source)
	if err != nil || revision != strings.Repeat("a", 40) {
		t.Fatalf("revision=%s, %v", revision, err)
	}
	source.Ref = "--upload-pack=bad"
	if _, err := analyzer.ResolveGitRevision(context.Background(), source); err == nil {
		t.Fatal("accepted an option as a ref")
	}
}
