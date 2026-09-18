package deploy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestAcquireSourceRecordsCommitMetadataOnTheRun exercises the acquire_source
// step against a real local Git checkout end to end: Materialize reads the
// commit the frozen revision names, and the step folds it into the run's
// persisted metadata through MergeRunMetadata, next to whatever the run was
// enqueued with.
func TestAcquireSourceRecordsCommitMetadataOnTheRun(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	runPlanningGitFixture(t, repository, "init")
	runPlanningGitFixture(t, repository, "config", "user.email", "fixture@example.test")
	runPlanningGitFixture(t, repository, "config", "user.name", "Release Author")
	writeBuildFixture(t, repository, "message.txt", "content\n")
	runPlanningGitFixture(t, repository, "add", "message.txt")
	runPlanningGitFixture(t, repository, "commit", "-m", "Fix checkout flow")
	revision := strings.TrimSpace(runPlanningGitOutput(t, repository, "rev-parse", "HEAD"))

	fixture := newOrchestrationFixture(t)
	environmentID := fixture.addEnvironment(t, "production", EnvironmentProduction)
	run := fixture.enqueue(t, environmentID, func(req *RunRequest) {
		req.Metadata = json.RawMessage(`{"targetReleaseId":0,"compatibility":false}`)
	})

	analyzer := NewHostSourceAnalyzer([]string{repository}, nil, t.TempDir(), nil, nil)
	executor := &NormalizedStepExecutor{store: fixture.runs, sources: analyzer, workspaceRoot: t.TempDir()}
	plan := &StoredExecutionPlan{
		SourceConfig:   DraftSourceConfig{Kind: SourceLocal, Mode: SourceModeLocalCheckout, LocalPath: repository},
		SourceIdentity: SourceIdentity{Kind: SourceLocal, LocalPath: repository, Revision: revision},
	}
	output := &recordingStepOutput{}
	result := executor.acquireSource(context.Background(), StepExecution{Run: *run, Output: output}, plan)
	if result.State != StepPassed {
		t.Fatalf("acquireSource = %#v", result)
	}
	if strings.Contains(output.joined(), "warning:") {
		t.Fatalf("unexpected warning logged for a readable commit: %s", output.joined())
	}

	updated, err := fixture.runs.Run(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		TargetReleaseID int64 `json:"targetReleaseId"`
		Commit          struct {
			SHA        string `json:"sha"`
			Subject    string `json:"subject"`
			Author     string `json:"author"`
			AuthoredAt string `json:"authoredAt"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(updated.Metadata, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Commit.SHA != revision || decoded.Commit.Subject != "Fix checkout flow" || decoded.Commit.Author != "Release Author" {
		t.Fatalf("run metadata commit = %#v (raw %s)", decoded.Commit, updated.Metadata)
	}
	if decoded.Commit.AuthoredAt == "" {
		t.Fatalf("run metadata commit has no authoredAt: %#v", decoded.Commit)
	}
}

// TestAcquireSourceLeavesMetadataAloneForNonGitSources proves the merge is
// scoped to Git sources: an image or Compose release has no commit to show
// and must not gain an empty or fabricated commit key.
func TestAcquireSourceLeavesMetadataAloneForNonGitSources(t *testing.T) {
	t.Parallel()
	fixture := newOrchestrationFixture(t)
	environmentID := fixture.addEnvironment(t, "production", EnvironmentProduction)
	run := fixture.enqueue(t, environmentID, func(req *RunRequest) {
		req.Metadata = json.RawMessage(`{"targetReleaseId":0,"compatibility":false}`)
	})

	analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(), nil, nil)
	executor := &NormalizedStepExecutor{store: fixture.runs, sources: analyzer, workspaceRoot: t.TempDir()}
	plan := &StoredExecutionPlan{
		SourceConfig:   DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposePaste, ComposeFiles: []ComposeDocument{{Path: "compose.yml", Content: "services: {}\n"}}},
		SourceIdentity: SourceIdentity{Kind: SourceCompose, Digest: fakeContentDigest("plain-compose")},
	}
	output := &recordingStepOutput{}
	result := executor.acquireSource(context.Background(), StepExecution{Run: *run, Output: output}, plan)
	if result.State != StepPassed {
		t.Fatalf("acquireSource = %#v", result)
	}
	if strings.Contains(output.joined(), "commit") {
		t.Fatalf("unexpected commit-related transcript output for a non-Git source: %s", output.joined())
	}

	updated, err := fixture.runs.Run(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(updated.Metadata), "commit") {
		t.Fatalf("non-Git source recorded commit metadata: %s", updated.Metadata)
	}
}
