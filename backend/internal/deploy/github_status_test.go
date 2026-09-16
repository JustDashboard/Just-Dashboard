package deploy

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

type recordingStatusPoster struct {
	mu     sync.Mutex
	posted []postedStatus
	err    error
}

type postedStatus struct {
	Repository string
	SHA        string
	Status     CommitStatus
}

func (p *recordingStatusPoster) PostCommitStatus(_ context.Context, repository, sha string, status CommitStatus) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.posted = append(p.posted, postedStatus{repository, sha, status})
	return p.err
}

func (p *recordingStatusPoster) all() []postedStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]postedStatus(nil), p.posted...)
}

func githubSourceFixture(t *testing.T, remote string) (*automationFixture, *OrchestrationStore) {
	t.Helper()
	f := newAutomationFixture(t)
	// Source revisions are immutable rows, so the GitHub identity is a second
	// revision and the runs below plan against it.
	identity := mustJSON(SourceIdentity{Kind: SourceGit, Remote: remote, Repository: "acme/shop", Ref: "main", Revision: strings.Repeat("b", 40)})
	if _, err := f.store.DB.Exec(`INSERT INTO deploy_sources(environment_id,revision,kind,config_json,identity_json,digest,created_at) VALUES(?,2,'git','{}',?,'source-2',1)`, f.environmentID, string(identity)); err != nil {
		t.Fatal(err)
	}
	return f, NewOrchestrationStore(f.store)
}

func TestCommitStatusPublisherReportsEveryOutcomeOnce(t *testing.T) {
	f, runs := githubSourceFixture(t, "https://github.com/acme/shop.git")
	poster := &recordingStatusPoster{}
	publisher := NewCommitStatusPublisher(runs, poster, func() string { return "https://dash.example.test" }, nil)
	run := EngineRun{ID: 31, ProjectID: f.projectID, EnvironmentID: f.environmentID, PlanRevision: 2, State: RunPreparing, Operation: OperationDeploy, SourceRevision: strings.Repeat("a", 40)}
	publisher.RunStarted(context.Background(), run)
	publisher.RunStarted(context.Background(), run)
	run.State, run.TerminalReason = RunFailed, "readiness checks failed"
	publisher.RunFinished(context.Background(), run)
	posted := poster.all()
	if len(posted) != 2 {
		t.Fatalf("posted = %+v", posted)
	}
	if posted[0].Repository != "acme/shop" || posted[0].SHA != strings.Repeat("a", 40) || posted[0].Status.State != "pending" || posted[0].Status.Context != "just-dashboard/production" || !strings.HasSuffix(posted[0].Status.TargetURL, "/deploy/"+itoa(f.projectID)+"/runs/31") {
		t.Fatalf("pending status = %+v", posted[0])
	}
	if posted[1].Status.State != "failure" || !strings.Contains(posted[1].Status.Description, "readiness checks failed") {
		t.Fatalf("failure status = %+v", posted[1])
	}
	success := EngineRun{ID: 32, ProjectID: f.projectID, EnvironmentID: f.environmentID, PlanRevision: 2, State: RunSucceeded, Operation: OperationRedeploy}
	publisher.RunFinished(context.Background(), success)
	if posted := poster.all(); len(posted) != 3 || posted[2].SHA != strings.Repeat("b", 40) || posted[2].Status.State != "success" {
		t.Fatalf("success without run revision must fall back to the source identity: %+v", posted)
	}
}

func TestCommitStatusPublisherRespectsPolicyRestartAndForeignRemotes(t *testing.T) {
	f, runs := githubSourceFixture(t, "https://github.com/acme/shop.git")
	poster := &recordingStatusPoster{err: errors.New("gh unavailable")}
	publisher := NewCommitStatusPublisher(runs, poster, nil, nil)
	restart := EngineRun{ID: 40, ProjectID: f.projectID, EnvironmentID: f.environmentID, PlanRevision: 2, State: RunSucceeded, Operation: OperationRestart}
	publisher.RunFinished(context.Background(), restart)
	if len(poster.all()) != 0 {
		t.Fatal("a restart deploys no new commit and must not post")
	}
	deploy := EngineRun{ID: 41, ProjectID: f.projectID, EnvironmentID: f.environmentID, PlanRevision: 2, State: RunSucceeded, Operation: OperationDeploy}
	publisher.RunFinished(context.Background(), deploy)
	if len(poster.all()) != 1 {
		t.Fatalf("posted = %+v", poster.all())
	}
	policy, err := runs.GitDeploymentPolicy(context.Background(), f.projectID, f.environmentID)
	if err != nil || !policy.CommitStatuses {
		t.Fatalf("policy = %+v err=%v", policy, err)
	}
	policy.CommitStatuses = false
	if _, err := runs.SetGitDeploymentPolicy(context.Background(), f.projectID, f.environmentID, policy); err != nil {
		t.Fatal(err)
	}
	reloaded, err := runs.GitDeploymentPolicy(context.Background(), f.projectID, f.environmentID)
	if err != nil || reloaded.CommitStatuses || reloaded.Revision != policy.Revision+1 {
		t.Fatalf("reloaded policy = %+v err=%v", reloaded, err)
	}
	if reloaded.Key() != (GitDeploymentPolicy{Automatic: reloaded.Automatic, WatchInclude: reloaded.WatchInclude, WatchExclude: reloaded.WatchExclude, CommitStatuses: true, Revision: reloaded.Revision}).Key() {
		t.Fatal("commit-status reporting must not change the decision key")
	}
	publisher.RunFinished(context.Background(), EngineRun{ID: 42, ProjectID: f.projectID, EnvironmentID: f.environmentID, PlanRevision: 2, State: RunSucceeded, Operation: OperationDeploy})
	if len(poster.all()) != 1 {
		t.Fatal("a disabled policy must not post")
	}

	foreign, foreignRuns := githubSourceFixture(t, "https://gitlab.example.test/acme/shop.git")
	foreignPublisher := NewCommitStatusPublisher(foreignRuns, poster, nil, nil)
	foreignPublisher.RunFinished(context.Background(), EngineRun{ID: 43, ProjectID: foreign.projectID, EnvironmentID: foreign.environmentID, PlanRevision: 2, State: RunSucceeded, Operation: OperationDeploy})
	if len(poster.all()) != 1 {
		t.Fatal("a non-GitHub remote must not post")
	}
}

func TestGitHubRepositoryParsing(t *testing.T) {
	cases := map[string]string{
		"https://github.com/acme/shop.git":      "acme/shop",
		"git@github.com:acme/shop.git":          "acme/shop",
		"ssh://git@github.com/acme/shop":        "acme/shop",
		"https://www.github.com/acme/shop/":     "acme/shop",
		"https://github.example.com/acme/shop":  "",
		"https://gitlab.com/acme/shop.git":      "",
		"https://github.com/acme":               "",
		"https://github.com/acme/shop/extra/pa": "",
	}
	for remote, want := range cases {
		got := githubRepository(SourceIdentity{Kind: SourceGit, Remote: remote})
		if got != want {
			t.Errorf("%s → %q, want %q", remote, got, want)
		}
	}
	if githubRepository(SourceIdentity{Kind: SourceGit, Remote: "https://github.com/acme/shop.git", Repository: "other/name"}) != "" {
		t.Error("a repository that disagrees with its remote must be refused")
	}
}
