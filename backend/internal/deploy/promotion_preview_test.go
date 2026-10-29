package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

const previewRemote = "https://github.com/acme/app.git"

// previewPromotionFixture is a remote-Git production with a staging
// environment and pull request previews of it: the shape a preview's image
// is promoted through.
type previewPromotionFixture struct {
	*releaseStoreFixture
	automation *AutomationStore
	staging    int64
	trigger    *Trigger
}

func newPreviewPromotionFixture(t *testing.T) *previewPromotionFixture {
	t.Helper()
	f := newReleaseStoreFixture(t)
	f.addPlan(t, 1, strings.Repeat("a", 40))
	useRemoteGitSource(t, f, f.envID, strings.Repeat("a", 40))
	addVariable(t, f, f.envID, "GREETING", "production", "plain")
	ctx := context.Background()
	created, err := f.variables.CreateStagingEnvironment(ctx, f.projectID, StagingRequest{Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	// Staging's own value, and one only staging has: what a promoted
	// preview must run with.
	if _, err := f.base.DB.Exec(`UPDATE deploy_variable_revisions SET active = 0 WHERE environment_id = ? AND key = 'GREETING'`, created.Environment.ID); err != nil {
		t.Fatal(err)
	}
	addVariable(t, f, created.Environment.ID, "GREETING", "staging", "plain")
	addVariable(t, f, created.Environment.ID, "STAGING_ONLY", "staging-secret", "secret")
	automation := NewAutomationStore(f.base, f.variables.sealer)
	automation.now = func() time.Time { return f.now }
	trigger, err := automation.EnsurePullRequestTrigger(ctx, f.projectID, f.envID, "acme/app", "main")
	if err != nil {
		t.Fatal(err)
	}
	return &previewPromotionFixture{releaseStoreFixture: f, automation: automation, staging: created.Environment.ID, trigger: trigger}
}

// useRemoteGitSource makes an environment's saved source the GitHub
// repository previews fetch pull request heads from.
func useRemoteGitSource(t *testing.T, f *releaseStoreFixture, environmentID int64, revision string) {
	t.Helper()
	config, _ := json.Marshal(DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: previewRemote, Ref: "main"})
	identity, _ := json.Marshal(SourceIdentity{Kind: SourceGit, Remote: previewRemote, Repository: "acme/app", Ref: "main", Revision: revision})
	if _, err := f.base.DB.Exec(`DELETE FROM deploy_sources WHERE environment_id = ?`, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.base.DB.Exec(`INSERT INTO deploy_sources(environment_id, revision, kind, config_json, identity_json, digest, created_at) VALUES(?, 1, 'git', ?, ?, ?, ?)`,
		environmentID, string(config), string(identity), digestBytes(config, identity), f.now.Unix()); err != nil {
		t.Fatal(err)
	}
}

func addVariable(t *testing.T, f *releaseStoreFixture, environmentID int64, key, value, sensitivity string) {
	t.Helper()
	sealed, err := f.variables.sealer.Seal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.base.DB.Exec(
		"INSERT INTO deploy_variable_revisions(environment_id, key, revision, sensitivity, scopes, value_enc, value_digest, active, created_by, created_at, value_mode) VALUES(?, ?, (SELECT COALESCE(MAX(revision), 0) + 1 FROM deploy_variable_revisions WHERE environment_id = ? AND key = ?), ?, 'runtime', ?, ?, 1, 'admin', ?, 'literal')",
		environmentID, key, environmentID, key, sensitivity, sealed, digestBytes([]byte(value)), f.now.Unix()); err != nil {
		t.Fatal(err)
	}
}

// approvedPreview is a pull request an administrator approved and the
// preview built and made live, with a secret of the preview's own.
func (f *previewPromotionFixture) approvedPreview(t *testing.T, number int, revision, headRepository, imageID string) (*PreviewApproval, *PreviewRef, *ReleaseWithArtifacts) {
	t.Helper()
	ctx := context.Background()
	event := pullRequestEvent(number, revision)
	event.HeadRepository = headRepository
	approval, err := f.automation.RecordPreviewApproval(ctx, f.trigger, event, "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	preview, _, err := f.automation.EnsurePreview(ctx, f.trigger, event)
	if err != nil {
		t.Fatal(err)
	}
	addVariable(t, f.releaseStoreFixture, preview.EnvironmentID, "PREVIEW_ONLY", "preview-secret", "secret")
	if imageID == "" {
		return approval, preview, nil
	}
	return approval, preview, stagedRelease(t, f.releaseStoreFixture, preview.EnvironmentID, imageID)
}

// enqueuePreviewPromotion queues what POST …/promote {source:"preview"} queues.
func (f *previewPromotionFixture) enqueuePreviewPromotion(t *testing.T, environmentID int64, number int) (*EngineRun, error) {
	t.Helper()
	ctx := context.Background()
	source, err := f.runs.PreviewPromotionSource(ctx, f.projectID, number, 0)
	if err != nil {
		return nil, err
	}
	metadata, _ := json.Marshal(map[string]any{"targetReleaseId": 0, "promotion": source.Promotion})
	run, _, err := f.runs.Enqueue(ctx, RunRequest{
		ProjectID: f.projectID, EnvironmentID: environmentID, Operation: OperationPromote, Trigger: TriggerManual,
		Actor: "admin", RequestDigest: fmt.Sprintf("promote-preview-%d-%d", number, time.Now().UnixNano()),
		PlanRevision: 1, SourceRevision: source.Promotion.SourceRevision, SlotClass: SlotLight, Metadata: metadata,
		Steps: PromotionStepKeys,
	})
	return run, err
}

// setReleaseTasks gives an environment's first build plan these release
// tasks. A plan revision is immutable to the store's own writers, so the row
// is replaced under its id, which releases built from it keep naming.
func setReleaseTasks(t *testing.T, f *releaseStoreFixture, environmentID int64, tasks ...ReleaseTaskConfig) {
	t.Helper()
	var id, created int64
	var method, raw, evidence, preview string
	if err := f.base.DB.QueryRow(`SELECT id, method, config_json, evidence_json, preview, created_at FROM deploy_build_plans WHERE environment_id = ? AND revision = 1`,
		environmentID).Scan(&id, &method, &raw, &evidence, &preview, &created); err != nil {
		t.Fatal(err)
	}
	var build BuildPlanConfig
	if err := json.Unmarshal([]byte(raw), &build); err != nil {
		t.Fatal(err)
	}
	build.ReleaseTasks = tasks
	encoded, _ := json.Marshal(build)
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM deploy_build_plans WHERE id = ?`, []any{id}},
		{`INSERT INTO deploy_build_plans(id, environment_id, revision, method, config_json, evidence_json, preview, digest, created_at) VALUES(?, ?, 1, ?, ?, ?, ?, ?, ?)`,
			[]any{id, environmentID, method, string(encoded), evidence, preview, digestBytes(encoded), created}},
	} {
		if _, err := f.base.DB.Exec(statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}

type heldImages map[string]bool

func (h heldImages) LocalImage(_ context.Context, reference string) (ResolvedImage, error) {
	if !h[reference] {
		return ResolvedImage{}, errors.New("No such image: " + reference)
	}
	return ResolvedImage{Reference: reference, Digest: reference, ConfigDigest: reference}, nil
}

func refusalCode(err error) string {
	var refusal *PreviewPromotionRefusal
	if errors.As(err, &refusal) {
		return refusal.Code
	}
	return ""
}

func TestApprovedPreviewIsPromotedIntoStagingWithStagingsConfiguration(t *testing.T) {
	t.Parallel()
	f := newPreviewPromotionFixture(t)
	ctx := context.Background()
	revision := strings.Repeat("c", 40)
	imageID := fakeContentDigest("preview-image")
	_, preview, built := f.approvedPreview(t, 7, revision, "acme/app", imageID)

	source, err := f.runs.PreviewPromotionSource(ctx, f.projectID, 7, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := RequirePromotionImage(ctx, heldImages{imageID: true}, source); err != nil {
		t.Fatal(err)
	}
	recorded := source.Promotion
	if recorded.SourceKind != PromotionFromPreview || recorded.ReleaseID != built.Release.ID || recorded.ImageID != imageID ||
		recorded.SourceRevision != revision || recorded.EnvironmentID != preview.EnvironmentID || recorded.Preview == nil ||
		recorded.Preview.Number != 7 || recorded.Preview.ApprovedBy != "reviewer" ||
		recorded.Preview.URL != "https://github.com/acme/app/pull/7" || recorded.Preview.Repository != "acme/app" {
		t.Fatalf("promotion record = %#v %#v", recorded, recorded.Preview)
	}

	run, err := f.enqueuePreviewPromotion(t, f.staging, 7)
	if err != nil {
		t.Fatal(err)
	}
	steps, _ := f.runs.Steps(ctx, run.ID)
	if len(steps) != len(PromotionStepKeys) || steps[0].Key != StepRenderRuntime {
		t.Fatalf("a preview's promotion builds: %#v", steps)
	}
	lease, err := f.runs.ClaimNext(ctx, "staging-worker", QueueBudget{Heavy: 1, Light: 1}, time.Minute)
	if err != nil || lease == nil || lease.RunID != run.ID {
		t.Fatalf("claim = %#v, %v", lease, err)
	}
	claimed, _ := f.runs.Run(ctx, run.ID)
	plan, err := f.runs.ExecutionPlan(ctx, *claimed)
	if err != nil {
		t.Fatal(err)
	}
	if plan.SourceIdentity.Revision != revision || plan.SourceIdentity.Ref != "refs/pull/7/head" {
		t.Fatalf("the promotion executes staging's identity instead of the preview's: %#v", plan.SourceIdentity)
	}
	backend := &artifactBackendFake{inspected: map[string]ResolvedImage{imageID: {Reference: imageID, Digest: imageID, ConfigDigest: imageID}},
		local: map[string]string{imageID: imageID}}
	executor := &NormalizedStepExecutor{store: f.runs, variables: f.variables, builder: NewArtifactBuilder(backend)}
	output := &recordingStepOutput{}
	result := executor.renderRuntime(ctx, StepExecution{Run: *claimed, ClaimToken: lease.Token, Output: output}, plan)
	if result.State != StepPassed {
		t.Fatalf("render = %#v\n%s", result, output.joined())
	}
	if len(backend.builds) != 0 || len(backend.pulls) != 0 {
		t.Fatalf("a preview's promotion built or pulled: %#v %#v", backend.builds, backend.pulls)
	}
	tag := releaseImageTag(f.staging, run.ID)
	if len(backend.tags) != 1 || backend.tags[0] != [2]string{imageID, tag} {
		t.Fatalf("tags = %#v", backend.tags)
	}
	promoted, err := f.runs.ReleaseForRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Release.EnvironmentID != f.staging || promoted.Release.SourceRevision != revision {
		t.Fatalf("promoted release = %#v", promoted.Release)
	}
	got := promoted.Release.Promotion
	if !got.FromPreview() || got.Preview == nil || got.Preview.Number != 7 || got.Preview.ApprovedBy != "reviewer" ||
		got.Preview.URL != "https://github.com/acme/app/pull/7" || got.SourceRevision != revision || got.ImageID != imageID {
		t.Fatalf("the staging release does not say where it came from: %#v", got)
	}
	snapshot, err := decodeReleaseRuntimeSnapshot(promoted)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Image.ConfigDigest != imageID || snapshot.Image.Reference != tag {
		t.Fatalf("snapshot image = %#v", snapshot.Image)
	}
	names := []string{}
	digests := map[string]string{}
	for _, variable := range snapshot.Variables {
		names = append(names, variable.Name)
		digests[variable.Name] = variable.ValueDigest
	}
	slices.Sort(names)
	// Staging's own variables: never the preview's secret, never
	// production's secret that staging did not copy.
	if !slices.Equal(names, []string{"GREETING", "STAGING_ONLY"}) {
		t.Fatalf("the promoted release runs with %v, not staging's variables", names)
	}
	if digests["GREETING"] != digestBytes([]byte("staging")) {
		t.Fatalf("GREETING is not staging's value: %s", digests["GREETING"])
	}
	if snapshot.Plan.PreviewIsolation || snapshot.Plan.BindAddress != "127.0.0.1" {
		t.Fatalf("the promoted release runs the preview's plan: %#v", snapshot.Plan)
	}
	transcript := output.joined()
	for _, want := range []string{"PR #7 (ccccccc), preview release #", "no build", "approved by reviewer", "with staging's configuration"} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("transcript lacks %q:\n%s", want, transcript)
		}
	}

	// The release-task step holds the shell rule itself, for the promotion
	// and for any run that clones this release, as a rollback does: the
	// candidate's own record is enough.
	shell := *plan
	shell.Build.ReleaseTasks = []ReleaseTaskConfig{{Name: "seed", Command: "./seed", TimeoutSeconds: 60, Env: []string{}}}
	for _, run := range []EngineRun{*claimed, {ID: claimed.ID, ProjectID: claimed.ProjectID, EnvironmentID: claimed.EnvironmentID, Operation: OperationRollback}} {
		refused := executor.runReleaseTasks(ctx, StepExecution{Run: run, ClaimToken: lease.Token, Output: &recordingStepOutput{}}, &shell)
		if refused.State != StepFailed || refused.ErrorCode != "promotion_refused" ||
			!strings.Contains(refused.ErrorMessage, `staging's release task "seed" runs in the dashboard's shell`) {
			t.Fatalf("a shell task over PR #7's code (%s) = %#v", run.Operation, refused)
		}
	}

	// The preview's row says it was promoted, and by which run.
	previews, err := f.automation.ListPreviews(ctx, f.projectID)
	if err != nil {
		t.Fatal(err)
	}
	var listed *PreviewRef
	for index := range previews {
		if previews[index].Number == 7 {
			listed = &previews[index]
		}
	}
	if listed == nil || listed.Promotion == nil || listed.Promotion.RunID != run.ID || listed.Promotion.EnvironmentID != f.staging ||
		listed.Promotion.ReleaseNumber != built.Release.Number || listed.Promotion.SourceRevision != revision {
		t.Fatalf("listed preview's promotion = %#v", listed)
	}

	// While the promotion is unfinished the preview's image is held, and
	// said to be held for staging.
	decisions, err := f.runs.ArtifactRetentionPlan(ctx, f.now)
	if err != nil {
		t.Fatal(err)
	}
	held := false
	for _, decision := range decisions {
		if decision.Artifact.ReleaseID == built.Release.ID && decision.Artifact.Kind == ArtifactImage {
			held = decision.Retain && decision.Reason == "source of an unfinished promotion to staging"
		}
	}
	if !held {
		t.Fatalf("the preview's image is not held for the promotion: %#v", decisions)
	}
}

func TestPreviewPromotionRefusals(t *testing.T) {
	t.Parallel()
	f := newPreviewPromotionFixture(t)
	ctx := context.Background()
	imageID := fakeContentDigest("approved-preview")
	f.approvedPreview(t, 7, strings.Repeat("c", 40), "acme/app", imageID)

	// Never straight into production: the request, the confirmation and the
	// queue all refuse it in words.
	if _, err := f.enqueuePreviewPromotion(t, f.envID, 7); refusalCode(err) != "preview_promotion_target" ||
		!errors.Is(err, ErrPromotionTarget) || !strings.Contains(err.Error(), "never straight into production") {
		t.Fatalf("a preview into production = %v", err)
	}
	if _, err := f.runs.PromotionPreviewFor(ctx, f.projectID, f.envID, PromotionRequest{Source: PromotionFromPreview, PreviewNumber: 7}, heldImages{}); refusalCode(err) != "preview_promotion_target" {
		t.Fatalf("the confirmation of a preview into production = %v", err)
	}
	// A staging release still goes only to production.
	stagingRelease := stagedRelease(t, f.releaseStoreFixture, f.staging, fakeContentDigest("staging-own"))
	metadata, _ := json.Marshal(map[string]any{"promotion": map[string]any{"releaseId": stagingRelease.Release.ID}})
	if _, _, err := f.runs.Enqueue(ctx, RunRequest{
		ProjectID: f.projectID, EnvironmentID: f.staging, Operation: OperationPromote, Trigger: TriggerManual,
		Actor: "admin", RequestDigest: "staging-into-staging", PlanRevision: 1, Metadata: metadata,
	}); !errors.Is(err, ErrPromotionTarget) || errors.Is(err, ErrPreviewPromotion) {
		t.Fatalf("a staging release into staging = %v", err)
	}

	// A fork's head whose approval an administrator withdrew.
	forkRevision := strings.Repeat("d", 40)
	approval, _, _ := f.approvedPreview(t, 8, forkRevision, "octocat/app", fakeContentDigest("fork-preview"))
	if _, err := f.automation.RejectPreview(ctx, f.projectID, approval.ID, forkRevision, "admin"); err != nil {
		t.Fatal(err)
	}
	_, err := f.runs.PreviewPromotionSource(ctx, f.projectID, 8, 0)
	if refusalCode(err) != "preview_promotion_unapproved" || !errors.Is(err, ErrPromotionSource) ||
		!strings.Contains(err.Error(), "comes from a fork (octocat/app)") || !strings.Contains(err.Error(), "rejected commit ddddddd") {
		t.Fatalf("an unapproved fork = %v", err)
	}
	view, err := f.runs.PromotionPreviewFor(ctx, f.projectID, f.staging, PromotionRequest{Source: PromotionFromPreview, PreviewNumber: 8}, heldImages{fakeContentDigest("fork-preview"): true})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Blockers) != 1 || view.Blockers[0].Code != "preview_promotion_unapproved" || view.Source.Preview == nil ||
		!view.Source.Preview.Fork || view.Source.Preview.ApprovalState != "rejected" || view.Source.ImageID == "" {
		t.Fatalf("the confirmation of an unapproved fork = %#v %#v", view.Blockers, view.Source.Preview)
	}

	// A head superseded by a newer one is no longer the approved head.
	f.approvedPreview(t, 9, strings.Repeat("e", 40), "acme/app", fakeContentDigest("superseded"))
	if _, err := f.automation.RecordPreviewApproval(ctx, f.trigger, pullRequestEvent(9, strings.Repeat("f", 40)), "reviewer"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runs.PreviewPromotionSource(ctx, f.projectID, 9, 0); refusalCode(err) != "preview_promotion_unapproved" ||
		!strings.Contains(err.Error(), "superseded") {
		t.Fatalf("a superseded head = %v", err)
	}

	// A preview with nothing live, and a pull request with no preview.
	f.approvedPreview(t, 10, strings.Repeat("1", 40), "acme/app", "")
	if _, err := f.runs.PreviewPromotionSource(ctx, f.projectID, 10, 0); refusalCode(err) != "preview_promotion_not_live" {
		t.Fatalf("a preview with no live release = %v", err)
	}
	if _, err := f.runs.PreviewPromotionSource(ctx, f.projectID, 404, 0); refusalCode(err) != "preview_promotion_not_found" {
		t.Fatalf("a pull request with no preview = %v", err)
	}
	if _, err := f.runs.PreviewPromotionSource(ctx, f.projectID, 7, stagingRelease.Release.ID); refusalCode(err) != "preview_promotion_not_live" {
		t.Fatalf("a release the preview is not serving = %v", err)
	}

	// Staging's shell release task would run the pull request's code
	// outside a container.
	setReleaseTasks(t, f.releaseStoreFixture, f.staging, ReleaseTaskConfig{Name: "migrate", Command: "npm run migrate", TimeoutSeconds: 60, Env: []string{}})
	if _, err := f.enqueuePreviewPromotion(t, f.staging, 7); refusalCode(err) != "preview_promotion_shell_task" ||
		!strings.Contains(err.Error(), `"migrate" runs in the dashboard's shell`) {
		t.Fatalf("staging with a shell release task = %v", err)
	}
	view, err = f.runs.PromotionPreviewFor(ctx, f.projectID, f.staging, PromotionRequest{Source: PromotionFromPreview, PreviewNumber: 7}, heldImages{imageID: true})
	if err != nil || len(view.Blockers) != 1 || view.Blockers[0].Code != "preview_promotion_shell_task" {
		t.Fatalf("the confirmation with a shell release task = %#v, %v", view, err)
	}
	setReleaseTasks(t, f.releaseStoreFixture, f.staging, ReleaseTaskConfig{Name: "migrate", Command: "npm run migrate", TimeoutSeconds: 60, Env: []string{}, Runner: ReleaseTaskRunnerImage})

	// An image this server no longer holds, and one whose artifact is gone.
	source, err := f.runs.PreviewPromotionSource(ctx, f.projectID, 7, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := RequirePromotionImage(ctx, heldImages{}, source); refusalCode(err) != "preview_promotion_image_missing" || !errors.Is(err, ErrPromotionImageMissing) {
		t.Fatalf("an image Docker does not hold = %v", err)
	}
	view, err = f.runs.PromotionPreviewFor(ctx, f.projectID, f.staging, PromotionRequest{Source: PromotionFromPreview, PreviewNumber: 7}, heldImages{})
	if err != nil || len(view.Blockers) != 1 || view.Blockers[0].Code != "preview_promotion_image_missing" || view.Source.ImagePresent {
		t.Fatalf("the confirmation of a missing image = %#v, %v", view, err)
	}
	if _, err := f.base.DB.Exec(`UPDATE deploy_release_artifacts SET state = 'pruned' WHERE release_id = ?`, source.Release.Release.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runs.PreviewPromotionSource(ctx, f.projectID, 7, 0); refusalCode(err) != "preview_promotion_image_missing" ||
		!strings.Contains(err.Error(), "build the preview again") {
		t.Fatalf("a pruned image = %v", err)
	}
}

// Between the request and the render step the pull request may merge and its
// preview close; the image is held for the promotion, so it still runs. An
// approval an administrator withdrew meanwhile stops it.
func TestQueuedPreviewPromotionRechecksTheApprovalWhenItRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, withdraw := range []bool{false, true} {
		f := newPreviewPromotionFixture(t)
		revision := strings.Repeat("c", 40)
		imageID := fakeContentDigest("queued-preview")
		approval, _, _ := f.approvedPreview(t, 7, revision, "acme/app", imageID)
		run, err := f.enqueuePreviewPromotion(t, f.staging, 7)
		if err != nil {
			t.Fatal(err)
		}
		if withdraw {
			if _, err := f.automation.RejectPreview(ctx, f.projectID, approval.ID, revision, "admin"); err != nil {
				t.Fatal(err)
			}
		} else if _, err := f.base.DB.Exec(`UPDATE deploy_preview_approvals SET state = 'closed' WHERE id = ?`, approval.ID); err != nil {
			t.Fatal(err)
		}
		lease, err := f.runs.ClaimNext(ctx, "staging-worker", QueueBudget{Heavy: 1, Light: 1}, time.Minute)
		if err != nil || lease == nil || lease.RunID != run.ID {
			t.Fatalf("claim = %#v, %v", lease, err)
		}
		claimed, _ := f.runs.Run(ctx, run.ID)
		plan, err := f.runs.ExecutionPlan(ctx, *claimed)
		if err != nil {
			t.Fatal(err)
		}
		backend := &artifactBackendFake{inspected: map[string]ResolvedImage{imageID: {Reference: imageID, Digest: imageID, ConfigDigest: imageID}},
			local: map[string]string{imageID: imageID}}
		executor := &NormalizedStepExecutor{store: f.runs, variables: f.variables, builder: NewArtifactBuilder(backend)}
		result := executor.renderRuntime(ctx, StepExecution{Run: *claimed, ClaimToken: lease.Token, Output: &recordingStepOutput{}}, plan)
		switch {
		case withdraw && (result.State != StepFailed || result.ErrorCode != "promotion_refused" || !strings.Contains(result.ErrorMessage, "rejected")):
			t.Fatalf("a withdrawn approval = %#v", result)
		case !withdraw && result.State != StepPassed:
			t.Fatalf("a preview closed after the request = %#v", result)
		}
	}
}

// A staging release promoted from a preview is still the pull request's
// code: production takes it as any staging release, but not while a
// production release task would run it in the dashboard's shell.
func TestAPromotedPreviewReachesProductionOnlyThroughStaging(t *testing.T) {
	t.Parallel()
	f := newPreviewPromotionFixture(t)
	ctx := context.Background()
	imageID := fakeContentDigest("chain")
	f.approvedPreview(t, 7, strings.Repeat("c", 40), "acme/app", imageID)
	run, err := f.enqueuePreviewPromotion(t, f.staging, 7)
	if err != nil {
		t.Fatal(err)
	}
	lease, _ := f.runs.ClaimNext(ctx, "staging-worker", QueueBudget{Heavy: 1, Light: 1}, time.Minute)
	claimed, _ := f.runs.Run(ctx, run.ID)
	plan, err := f.runs.ExecutionPlan(ctx, *claimed)
	if err != nil {
		t.Fatal(err)
	}
	backend := &artifactBackendFake{inspected: map[string]ResolvedImage{imageID: {Reference: imageID, Digest: imageID, ConfigDigest: imageID}},
		local: map[string]string{imageID: imageID}}
	executor := &NormalizedStepExecutor{store: f.runs, variables: f.variables, builder: NewArtifactBuilder(backend)}
	if result := executor.renderRuntime(ctx, StepExecution{Run: *claimed, ClaimToken: lease.Token, Output: &recordingStepOutput{}}, plan); result.State != StepPassed {
		t.Fatalf("render = %#v", result)
	}
	promoted, err := f.runs.ReleaseForRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	activateFixtureRelease(t, f.releaseStoreFixture, f.staging, promoted.Release.ID, run.ID, lease.Token)

	setReleaseTasks(t, f.releaseStoreFixture, f.envID, ReleaseTaskConfig{Name: "migrate", Command: "./migrate", TimeoutSeconds: 60, Env: []string{}})
	if _, err := enqueuePromotion(t, f.releaseStoreFixture, promoted.Release.ID); refusalCode(err) != "preview_promotion_shell_task" ||
		!strings.Contains(err.Error(), "production's release task") || !strings.Contains(err.Error(), "PR #7") {
		t.Fatalf("production with a shell task taking a preview's code = %v", err)
	}
	setReleaseTasks(t, f.releaseStoreFixture, f.envID, ReleaseTaskConfig{Name: "migrate", Command: "./migrate", TimeoutSeconds: 60, Env: []string{}, Runner: ReleaseTaskRunnerImage})
	productionRun, err := enqueuePromotion(t, f.releaseStoreFixture, promoted.Release.ID)
	if err != nil {
		t.Fatalf("staging's release of a preview into production = %v", err)
	}
	// Production's record is staging's promotion, and still names the pull
	// request whose code the image is.
	record := promotionFromRun(*productionRun)
	if record == nil || record.FromPreview() || record.SourceKind != PromotionFromStaging || record.Preview == nil ||
		record.Preview.Number != 7 || record.Preview.ApprovedBy != "reviewer" {
		t.Fatalf("production's promotion record = %#v", record)
	}
}

func TestPullRequestURLNamesEachForgesPage(t *testing.T) {
	t.Parallel()
	for _, row := range []struct{ provider, remote, repository, want string }{
		{"github", "https://github.com/acme/app.git", "acme/app", "https://github.com/acme/app/pull/7"},
		{"github", "git@github.com:acme/app.git", "acme/app", "https://github.com/acme/app/pull/7"},
		{"gitlab", "https://gitlab.example.com/acme/app.git", "acme/app", "https://gitlab.example.com/acme/app/-/merge_requests/7"},
		{"bitbucket", "", "acme/app", "https://bitbucket.org/acme/app/pull-requests/7"},
		{"gitea", "https://git.example.com/acme/app.git", "acme/app", "https://git.example.com/acme/app/pulls/7"},
		{"generic", "https://git.example.com/acme/app.git", "acme/app", ""},
		{"github", "https://github.com/acme/app.git", "", ""},
	} {
		if got := pullRequestURL(row.provider, row.remote, row.repository, 7); got != row.want {
			t.Errorf("pullRequestURL(%q, %q, %q) = %q, want %q", row.provider, row.remote, row.repository, got, row.want)
		}
	}
}

func TestStagingPromotionIsNamedAsAPreviewsInNotifications(t *testing.T) {
	t.Parallel()
	message := renderNotification(NotificationEnvelope{Event: NotificationEventSucceeded, Operation: string(OperationPromote), EnvironmentName: "Staging", ProjectName: "api"})
	if !strings.Contains(message.Summary, "Promotion from a pull request preview") {
		t.Fatalf("summary = %q", message.Summary)
	}
	message = renderNotification(NotificationEnvelope{Event: NotificationEventSucceeded, Operation: string(OperationPromote), EnvironmentName: "Production", ProjectName: "api"})
	if !strings.Contains(message.Summary, "Promotion from staging") {
		t.Fatalf("summary = %q", message.Summary)
	}
}
