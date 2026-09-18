package deploy

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

type recordingCommentPoster struct {
	mu    sync.Mutex
	calls []postedComment
	err   error
}

type postedComment struct {
	Repository string
	Number     int
	Marker     string
	Body       string
}

func (p *recordingCommentPoster) UpsertPullRequestComment(_ context.Context, repository string, number int, marker, body string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, postedComment{repository, number, marker, body})
	return p.err
}

func (p *recordingCommentPoster) all() []postedComment {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]postedComment(nil), p.calls...)
}

// previewFixture opens an approved preview for pull request 7 of acme/app and
// gives it a public address, the way a preview trigger with a domain pattern
// does.
func previewFixture(t *testing.T) (*automationFixture, *PreviewRef) {
	t.Helper()
	f := newAutomationFixture(t)
	created, err := f.automation.CreateTrigger(t.Context(), f.projectID, f.environmentID, TriggerWrite{Name: "Review", Kind: TriggerGitHub, Provider: "github", Enabled: true, Config: TriggerConfig{Repository: "acme/app", Ref: "main", Preview: true, Delivery: DeliveryApp}})
	if err != nil {
		t.Fatal(err)
	}
	event := ProviderEvent{PreviewNumber: 7, PreviewRef: "refs/pull/7/head", Revision: strings.Repeat("a", 40), Author: "dev", HeadRepository: "acme/app"}
	approvePreviewFixture(t, f, &created.Trigger, event)
	preview, _, err := f.automation.EnsurePreview(t.Context(), &created.Trigger, event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB.Exec(`INSERT INTO deploy_dependencies(environment_id,release_id,kind,ownership,resource_kind,resource_id,config_json,created_at) VALUES(?,0,'domain','managed','domain','pr-7.example.test','{"hostname":"pr-7.example.test","https":true}',1)`, preview.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	return f, preview
}

// A preview's pull request gets one comment that follows the run: building,
// then ready with the address, then removed. Each state is written once.
func TestPullRequestCommenterKeepsOneCommentCurrent(t *testing.T) {
	f, preview := previewFixture(t)
	poster := &recordingCommentPoster{}
	commenter := NewPullRequestCommenter(NewOrchestrationStore(f.store), poster, func() string { return "https://dash.example.test/" }, nil)
	run := EngineRun{ID: 91, RunNumber: 3, ProjectID: f.projectID, EnvironmentID: preview.EnvironmentID, State: RunPreparing, Operation: OperationPreviewCreate, SourceRevision: strings.Repeat("a", 40)}
	commenter.RunStarted(context.Background(), run)
	commenter.RunStarted(context.Background(), run)
	run.State = RunSucceeded
	commenter.RunFinished(context.Background(), run)
	calls := poster.all()
	if len(calls) != 2 {
		t.Fatalf("calls = %+v", calls)
	}
	marker := "<!-- just-dashboard:preview:" + itoa(preview.EnvironmentID) + " -->"
	if calls[0].Repository != "acme/app" || calls[0].Number != 7 || calls[0].Marker != marker || !strings.Contains(calls[0].Body, "🔄 Building") || !strings.Contains(calls[0].Body, "https://pr-7.example.test/") {
		t.Fatalf("building comment = %+v", calls[0])
	}
	if !strings.Contains(calls[1].Body, "✅ Ready") || !strings.Contains(calls[1].Body, "https://pr-7.example.test/") ||
		!strings.Contains(calls[1].Body, "Commit `aaaaaaa`") || !strings.Contains(calls[1].Body, "[Deployment run #3](https://dash.example.test/deploy/"+itoa(f.projectID)+"/runs/91)") {
		t.Fatalf("ready comment = %+v", calls[1])
	}
	failed := EngineRun{ID: 92, RunNumber: 4, ProjectID: f.projectID, EnvironmentID: preview.EnvironmentID, State: RunFailed, Operation: OperationPreviewUpdate, TerminalReason: "readiness checks failed"}
	commenter.RunFinished(context.Background(), failed)
	removed := EngineRun{ID: 93, RunNumber: 5, ProjectID: f.projectID, EnvironmentID: preview.EnvironmentID, State: RunSucceeded, Operation: OperationPreviewRemove}
	commenter.RunStarted(context.Background(), removed)
	commenter.RunFinished(context.Background(), removed)
	calls = poster.all()
	if len(calls) != 4 || !strings.Contains(calls[2].Body, "❌ Failed") || !strings.Contains(calls[2].Body, "Reason: readiness checks failed") || strings.Contains(calls[2].Body, "https://pr-7") {
		t.Fatalf("failed comment = %+v", calls)
	}
	if !strings.Contains(calls[3].Body, "🗑️ Removed") || strings.Contains(calls[3].Body, "https://pr-7") {
		t.Fatalf("removed comment = %+v", calls[3])
	}
}

// Production runs, previews of other providers and a poster that fails never
// change a run and never reach GitHub with the wrong pull request.
func TestPullRequestCommenterOnlySpeaksForGitHubPreviews(t *testing.T) {
	f, preview := previewFixture(t)
	poster := &recordingCommentPoster{err: errors.New("app not installed")}
	commenter := NewPullRequestCommenter(NewOrchestrationStore(f.store), poster, nil, nil)
	production := EngineRun{ID: 1, ProjectID: f.projectID, EnvironmentID: f.environmentID, State: RunSucceeded, Operation: OperationDeploy}
	commenter.RunFinished(context.Background(), production)
	if len(poster.all()) != 0 {
		t.Fatal("a production run was reported on a pull request")
	}
	if _, err := f.store.DB.Exec(`UPDATE deploy_triggers SET provider='gitea'`); err != nil {
		t.Fatal(err)
	}
	commenter.RunFinished(context.Background(), EngineRun{ID: 2, ProjectID: f.projectID, EnvironmentID: preview.EnvironmentID, State: RunSucceeded, Operation: OperationPreviewCreate})
	if len(poster.all()) != 0 {
		t.Fatal("a Gitea preview was reported to GitHub")
	}
	if _, err := f.store.DB.Exec(`UPDATE deploy_triggers SET provider='github'`); err != nil {
		t.Fatal(err)
	}
	commenter.RunFinished(context.Background(), EngineRun{ID: 3, ProjectID: f.projectID, EnvironmentID: preview.EnvironmentID, State: RunSucceeded, Operation: OperationPreviewCreate})
	if calls := poster.all(); len(calls) != 1 || calls[0].Number != 7 {
		t.Fatalf("calls = %+v", calls)
	}
}

// App delivery is a GitHub-only choice, and the lookup that routes an App
// delivery finds exactly the enabled triggers that asked for it.
func TestAppDeliveryTriggersAreFoundByRepository(t *testing.T) {
	f := newAutomationFixture(t)
	ctx := context.Background()
	if _, err := f.automation.CreateTrigger(ctx, f.projectID, f.environmentID, TriggerWrite{Name: "Gitea app", Kind: TriggerGitea, Provider: "gitea", Enabled: true, Config: TriggerConfig{Repository: "acme/app", Ref: "main", Delivery: DeliveryApp}}); err == nil {
		t.Fatal("App delivery was accepted for Gitea")
	}
	if _, err := f.automation.CreateTrigger(ctx, f.projectID, f.environmentID, TriggerWrite{Name: "No repo", Kind: TriggerGitHub, Provider: "github", Enabled: true, Config: TriggerConfig{Delivery: DeliveryApp}}); err == nil {
		t.Fatal("App delivery was accepted without a repository")
	}
	if _, err := f.automation.CreateTrigger(ctx, f.projectID, f.environmentID, TriggerWrite{Name: "Odd", Kind: TriggerGitHub, Provider: "github", Enabled: true, Config: TriggerConfig{Repository: "acme/app", Delivery: "carrier-pigeon"}}); err == nil {
		t.Fatal("an unknown delivery was accepted")
	}
	app, err := f.automation.CreateTrigger(ctx, f.projectID, f.environmentID, TriggerWrite{Name: "App", Kind: TriggerGitHub, Provider: "github", Enabled: true, Config: TriggerConfig{Repository: "Acme/App.git", Ref: "main", Delivery: " App "}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.automation.CreateTrigger(ctx, f.projectID, f.environmentID, TriggerWrite{Name: "Hook", Kind: TriggerGitHub, Provider: "github", Enabled: true, Config: TriggerConfig{Repository: "acme/app", Ref: "main"}}); err != nil {
		t.Fatal(err)
	}
	disabled, err := f.automation.CreateTrigger(ctx, f.projectID, f.environmentID, TriggerWrite{Name: "Off", Kind: TriggerGitHub, Provider: "github", Enabled: false, Config: TriggerConfig{Repository: "acme/app", Ref: "main", Delivery: DeliveryApp}})
	if err != nil {
		t.Fatal(err)
	}
	found, err := f.automation.TriggersForAppDelivery(ctx, "acme/app")
	if err != nil || len(found) != 1 || found[0].ID != app.Trigger.ID || found[0].Config.Delivery != DeliveryApp {
		t.Fatalf("found = %+v, %v (disabled %d)", found, err, disabled.Trigger.ID)
	}
	if found, err := f.automation.TriggersForAppDelivery(ctx, "acme/other"); err != nil || len(found) != 0 {
		t.Fatalf("other repository = %+v, %v", found, err)
	}
}
