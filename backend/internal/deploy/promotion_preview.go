package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// A pull request preview's image can be promoted into the project's staging
// environment, so the chain is preview → staging → production with nothing
// rebuilt on the way. The image is the one the preview built from the commit
// an administrator approved; it runs in staging with staging's frozen plan and
// variables, exactly as a staging release is promoted into production with
// production's. The preview's own configuration never crosses: its variables
// (and so the production values copied into it, which isolation keeps from
// being secrets of staging's) stay with the preview.
//
// Only staging takes a preview: production takes only what staging ran first.

// Where a promoted image was taken from.
const (
	PromotionFromStaging = "staging"
	PromotionFromPreview = "preview"
)

// ErrPreviewPromotion marks every refusal to promote a pull request preview.
// The refusal itself is a PreviewPromotionRefusal, whose words name the rule.
var ErrPreviewPromotion = errors.New("this pull request preview cannot be promoted")

// PreviewPromotionRefusal is why a preview cannot be promoted, in the words
// the operator acts on. It is ErrPreviewPromotion and also the general
// promotion error it is a case of (a missing image is ErrPromotionImageMissing,
// a wrong target ErrPromotionTarget), so the engine's step codes read it as
// they read a staging refusal.
type PreviewPromotionRefusal struct {
	Code    string
	Message string
	kind    error
}

func (r *PreviewPromotionRefusal) Error() string   { return r.Message }
func (r *PreviewPromotionRefusal) Unwrap() []error { return []error{ErrPreviewPromotion, r.kind} }

func refusePreviewPromotion(kind error, code, format string, args ...any) error {
	return &PreviewPromotionRefusal{Code: code, Message: fmt.Sprintf(format, args...), kind: kind}
}

// RefusePreviewPromotionTarget is a preview promoted anywhere but staging.
func RefusePreviewPromotionTarget(number int) error {
	return refusePreviewPromotion(ErrPromotionTarget, "preview_promotion_target",
		"PR #%d's preview is promoted into staging, never straight into production: promote it to staging, then staging's release to production", number)
}

func refusePreviewImageMissing(number int) error {
	return refusePreviewPromotion(ErrPromotionImageMissing, "preview_promotion_image_missing",
		"PR #%d's image is no longer on this server; build the preview again and promote the new release", number)
}

// PromotedPreview is the pull request a release promoted from a preview was
// built from, recorded with the release: its number, its page, and the
// administrator whose approval of the exact commit let it be built.
type PromotedPreview struct {
	Number         int    `json:"number"`
	URL            string `json:"url,omitempty"`
	Repository     string `json:"repository,omitempty"`
	HeadRepository string `json:"headRepository,omitempty"`
	ApprovedBy     string `json:"approvedBy"`
}

// FromPreview reports whether the image was taken from a pull request
// preview rather than from staging.
func (p *ReleasePromotion) FromPreview() bool {
	return p != nil && p.SourceKind == PromotionFromPreview
}

// PromotionRequest names what a promotion takes its image from: a staging
// release by id, or a pull request's preview by its number (its live release,
// which ReleaseID may name to be sure of it).
type PromotionRequest struct {
	Source        string
	ReleaseID     int64
	PreviewNumber int
}

// FromPreview reports whether the request names a preview.
func (r PromotionRequest) FromPreview() bool { return r.Source == PromotionFromPreview }

// previewCandidate is a preview as eligibility reads it.
type previewCandidate struct {
	triggerID, environmentID, liveReleaseID int64
	number                                  int
	ref, state, provider, repository        string
	archived, quarantined                   bool
}

// previewSelector finds the preview a promotion names: by its pull request
// number when it is asked for, and by the environment it recorded when a
// queued promotion is run, so a later preview of the same number is never
// taken for the one that was chosen.
type previewSelector struct {
	number        int
	environmentID int64
}

// previewCheck is how strictly a preview is read.
type previewCheck int

const (
	// previewAdmission is the decision to promote: the preview is open and
	// isolated, its release is live, and its commit's approval is current.
	previewAdmission previewCheck = iota
	// previewRender re-reads a queued promotion when it runs. The pull
	// request may have been merged and its preview closed meanwhile — the
	// image is held for the promotion by retention — but an approval an
	// administrator withdrew since, or an image that changed, stops it.
	previewRender
)

func findPromotablePreviewTx(ctx context.Context, q promotionQuery, projectID int64, selector previewSelector) (*previewCandidate, error) {
	const columns = `SELECT p.trigger_id,p.provider_ref,p.environment_id,p.state,e.live_release_id,e.archived_at<>0,
		t.provider,COALESCE(json_extract(t.config_json,'$.repository'),''),
		EXISTS(SELECT 1 FROM deploy_preview_quarantines q WHERE q.environment_id=e.id AND q.status<>'cleared')
		FROM deploy_preview_refs p JOIN deploy_environments e ON e.id=p.environment_id JOIN deploy_triggers t ON t.id=p.trigger_id
		WHERE e.project_id=? AND e.kind='preview' AND `
	var row *sql.Row
	if selector.environmentID > 0 {
		row = q.QueryRowContext(ctx, columns+`p.environment_id=?`, projectID, selector.environmentID)
	} else {
		if selector.number <= 0 {
			return nil, refusePreviewPromotion(ErrPromotionSource, "preview_promotion_not_found", "name the pull request whose preview is promoted")
		}
		// A number two triggers know is the open preview first, as the
		// panels read it.
		row = q.QueryRowContext(ctx, columns+`p.provider_ref=? ORDER BY (p.state='open') DESC,p.updated_at DESC,p.id DESC LIMIT 1`,
			projectID, strconv.Itoa(selector.number))
	}
	var candidate previewCandidate
	if err := row.Scan(&candidate.triggerID, &candidate.ref, &candidate.environmentID, &candidate.state, &candidate.liveReleaseID,
		&candidate.archived, &candidate.provider, &candidate.repository, &candidate.quarantined); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, refusePreviewPromotion(ErrPromotionSource, "preview_promotion_not_found",
				"PR #%d has no preview on this deployment; test the pull request first", selector.number)
		}
		return nil, err
	}
	candidate.number, _ = strconv.Atoi(candidate.ref)
	candidate.repository = strings.TrimSuffix(strings.TrimSpace(candidate.repository), ".git")
	return &candidate, nil
}

// previewReleaseTx is the preview's release a promotion takes: on admission
// the preview's live release, which releaseID may name; when run, the one
// the promotion recorded, which retention has kept.
func previewReleaseTx(ctx context.Context, q promotionQuery, projectID int64, candidate *previewCandidate, releaseID int64, check previewCheck) (*Release, error) {
	if check == previewAdmission {
		switch {
		case candidate.state != "open" || candidate.archived:
			return nil, refusePreviewPromotion(ErrPromotionSource, "preview_promotion_closed",
				"PR #%d's preview is closed; test the pull request again and promote the new preview", candidate.number)
		case candidate.quarantined:
			return nil, refusePreviewPromotion(ErrPromotionSource, "preview_promotion_quarantined",
				"PR #%d's preview predates preview isolation and is quarantined; approve its head again to rebuild it isolated", candidate.number)
		case candidate.liveReleaseID == 0:
			return nil, refusePreviewPromotion(ErrPromotionSource, "preview_promotion_not_live",
				"PR #%d's preview has no live release to promote; build it first", candidate.number)
		case releaseID != 0 && releaseID != candidate.liveReleaseID:
			return nil, refusePreviewPromotion(ErrPromotionSource, "preview_promotion_not_live",
				"release %d is not what PR #%d's preview is serving; only the preview's live release is promoted", releaseID, candidate.number)
		}
		releaseID = candidate.liveReleaseID
	}
	release, err := releaseByIDTx(ctx, q, releaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPromotionSource
	}
	if err != nil {
		return nil, err
	}
	if release.ProjectID != projectID || release.EnvironmentID != candidate.environmentID {
		return nil, ErrPromotionSource
	}
	live := release.State == "live"
	if !live && (check == previewAdmission || release.State != "retained") {
		return nil, refusePreviewPromotion(ErrPromotionSource, "preview_promotion_not_live",
			"PR #%d's release #%d is no longer live", candidate.number, release.Number)
	}
	return release, nil
}

// previewApprovalTx answers who approved the exact commit the release was
// built from. On admission the approval must be the pull request's current
// one; when a queued promotion runs it must only never have been withdrawn.
func previewApprovalTx(ctx context.Context, q promotionQuery, candidate *previewCandidate, revision string, check previewCheck) (string, ProviderEvent, error) {
	var state, approvedBy, encoded string
	err := q.QueryRowContext(ctx, `SELECT state,approved_by,event_json FROM deploy_preview_approvals WHERE trigger_id=? AND provider_ref=? AND revision=?`,
		candidate.triggerID, candidate.ref, revision).Scan(&state, &approvedBy, &encoded)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", ProviderEvent{}, err
	}
	var event ProviderEvent
	_ = json.Unmarshal([]byte(encoded), &event)
	approvedBy = strings.TrimSpace(approvedBy)
	current := state == "approved" && approvedBy != ""
	if check == previewRender {
		// A head approved and then superseded or closed was still reviewed
		// by an administrator; one rejected was withdrawn.
		current = approvedBy != "" && (state == "approved" || state == "superseded" || state == "closed")
	}
	if current {
		return approvedBy, event, nil
	}
	short := shortRevision(revision)
	fork := event.HeadRepository != "" && candidate.repository != "" && !sameRepository(event.HeadRepository, candidate.repository)
	var why string
	switch state {
	case "pending":
		why = fmt.Sprintf("commit %s of PR #%d is waiting for an administrator's approval", short, candidate.number)
	case "rejected":
		why = fmt.Sprintf("an administrator rejected commit %s of PR #%d", short, candidate.number)
	case "superseded":
		why = fmt.Sprintf("commit %s is no longer PR #%d's approved head: a newer head superseded it, so approve and build that one to promote it", short, candidate.number)
	case "closed":
		why = fmt.Sprintf("PR #%d was closed, which ended the approval of commit %s", candidate.number, short)
	default:
		why = fmt.Sprintf("commit %s of PR #%d was not approved by an administrator", short, candidate.number)
	}
	if fork {
		why = fmt.Sprintf("PR #%d comes from a fork (%s), and a fork's code is promoted only at a commit an administrator approved: %s",
			candidate.number, event.HeadRepository, why)
	}
	return "", event, refusePreviewPromotion(ErrPromotionSource, "preview_promotion_unapproved", "%s", why)
}

// previewPromotionSourceTx validates a pull request preview's release as
// something staging can take, and builds the promotion record: the preview's
// exact image, the commit, and who approved it.
func previewPromotionSourceTx(ctx context.Context, q promotionQuery, projectID int64, selector previewSelector, releaseID int64, check previewCheck) (*PromotionSource, error) {
	candidate, err := findPromotablePreviewTx(ctx, q, projectID, selector)
	if err != nil {
		return nil, err
	}
	release, err := previewReleaseTx(ctx, q, projectID, candidate, releaseID, check)
	if err != nil {
		return nil, err
	}
	approvedBy, event, err := previewApprovalTx(ctx, q, candidate, release.SourceRevision, check)
	if err != nil {
		return nil, err
	}
	source, err := promotableReleaseTx(ctx, q, release)
	if err != nil {
		return nil, previewImageRefusal(candidate.number, err)
	}
	var name string
	_ = q.QueryRowContext(ctx, `SELECT name FROM deploy_environments WHERE id = ?`, release.EnvironmentID).Scan(&name)
	headRepository := strings.TrimSpace(event.HeadRepository)
	source.Promotion = ReleasePromotion{
		EnvironmentID: release.EnvironmentID, EnvironmentName: name,
		ReleaseID: release.ID, ReleaseNumber: release.Number, RunID: release.RunID,
		ImageID: source.Snapshot.Image.ConfigDigest, ImageDigest: release.ImageDigest,
		SourceRevision: release.SourceRevision, SourceKind: PromotionFromPreview,
		Preview: &PromotedPreview{
			Number: candidate.number, Repository: candidate.repository, HeadRepository: headRepository,
			URL:        pullRequestURL(candidate.provider, source.Identity.Remote, candidate.repository, candidate.number),
			ApprovedBy: approvedBy,
		},
	}
	return source, nil
}

// previewImageRefusal words promotableReleaseTx's refusals for a preview.
func previewImageRefusal(number int, err error) error {
	switch {
	case errors.Is(err, ErrPromotionImageMissing):
		return refusePreviewImageMissing(number)
	case errors.Is(err, ErrPromotionIncompatible):
		return refusePreviewPromotion(ErrPromotionIncompatible, "preview_promotion_incompatible",
			"PR #%d's preview cannot be promoted into staging: %s", number, strings.TrimPrefix(err.Error(), ErrPromotionIncompatible.Error()+": "))
	default:
		return err
	}
}

// PreviewPromotionSource validates a pull request's preview as something the
// project's staging environment can take now. releaseID, when given, must be
// the preview's live release.
func (s *OrchestrationStore) PreviewPromotionSource(ctx context.Context, projectID int64, number int, releaseID int64) (*PromotionSource, error) {
	return previewPromotionSourceTx(ctx, s.db, projectID, previewSelector{number: number}, releaseID, previewAdmission)
}

// checkPreviewPromotionTargetTx holds a preview's promotion to the project's
// live staging environment, built from the same kind of source and, for Git,
// the same repository, and refuses it while staging runs a release task in
// the dashboard's shell: that task would check out the pull request's code
// and run it there, which nothing of a pull request's ever is.
func checkPreviewPromotionTargetTx(ctx context.Context, q promotionQuery, projectID, targetID int64, planRevision int, source *PromotionSource) error {
	number := 0
	if source.Promotion.Preview != nil {
		number = source.Promotion.Preview.Number
	}
	var kind EnvironmentKind
	var sourceKind, sourceConfig, buildConfig string
	if planRevision <= 0 {
		if err := q.QueryRowContext(ctx, `SELECT desired_revision FROM deploy_environments WHERE id = ?`, targetID).Scan(&planRevision); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrEnvironmentNotFound
			}
			return err
		}
	}
	err := q.QueryRowContext(ctx, `
		SELECT e.kind, s.kind, s.config_json, b.config_json FROM deploy_environments e
		  JOIN deploy_sources s ON s.environment_id = e.id AND s.revision = ?
		  JOIN deploy_build_plans b ON b.environment_id = e.id AND b.revision = ?
		 WHERE e.id = ? AND e.project_id = ? AND e.archived_at = 0`, planRevision, planRevision, targetID, projectID).
		Scan(&kind, &sourceKind, &sourceConfig, &buildConfig)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrEnvironmentNotFound
	}
	if err != nil {
		return err
	}
	if kind != EnvironmentStaging {
		return RefusePreviewPromotionTarget(number)
	}
	if err := sameSourceAsTarget(sourceKind, sourceConfig, source.Identity, fmt.Sprintf("PR #%d's preview", number), "staging"); err != nil {
		return previewImageRefusal(number, err)
	}
	var build BuildPlanConfig
	if json.Unmarshal([]byte(buildConfig), &build) != nil {
		return ErrInvalidPlan
	}
	return refuseShellTasksForPullRequestCode(build.ReleaseTasks, "staging", number)
}

// refuseShellTasksForPullRequestCode refuses a target whose release tasks
// include one that runs in the dashboard's shell, where it would run against
// a checkout of the pull request's commit. A preview runs no release task and
// builds in BuildKit; a task in the release image runs in a container of the
// image itself, which is what promoting it already decided to run.
func refuseShellTasksForPullRequestCode(tasks []ReleaseTaskConfig, target string, number int) error {
	for _, task := range tasks {
		if task.Runner != ReleaseTaskRunnerImage {
			return refusePreviewPromotion(ErrPromotionIncompatible, "preview_promotion_shell_task",
				"%s's release task %q runs in the dashboard's shell against the source tree, where PR #%d's code would run outside a container; set it to run in the release image to promote a pull request's image",
				target, task.Name, number)
		}
	}
	return nil
}

// pullRequestURL is the pull request's page on its forge, or "" when the
// forge is not one whose page can be named from the repository alone.
func pullRequestURL(provider, remote, repository string, number int) string {
	if number <= 0 || !validRepository(repository) {
		return ""
	}
	host := ""
	if parsed, err := url.Parse(remote); err == nil && parsed.Hostname() != "" {
		host = parsed.Hostname()
	} else if after, ok := strings.CutPrefix(remote, "git@"); ok {
		host, _, _ = strings.Cut(after, ":")
	}
	defaults := map[string]string{"github": "github.com", "gitlab": "gitlab.com", "bitbucket": "bitbucket.org"}
	if host == "" {
		host = defaults[provider]
	}
	if host == "" {
		return ""
	}
	base := "https://" + strings.ToLower(host) + "/" + repository
	switch provider {
	case "github":
		return fmt.Sprintf("%s/pull/%d", base, number)
	case "gitlab":
		return fmt.Sprintf("%s/-/merge_requests/%d", base, number)
	case "bitbucket":
		return fmt.Sprintf("%s/pull-requests/%d", base, number)
	case "gitea", "forgejo":
		return fmt.Sprintf("%s/pulls/%d", base, number)
	}
	return ""
}

// PromotionSourceFor reads the source a promotion request names.
func (s *OrchestrationStore) PromotionSourceFor(ctx context.Context, projectID int64, request PromotionRequest) (*PromotionSource, error) {
	if request.FromPreview() {
		return s.PreviewPromotionSource(ctx, projectID, request.PreviewNumber, request.ReleaseID)
	}
	return s.PromotionSource(ctx, projectID, request.ReleaseID)
}

// RequirePromotionImage asks Docker, the authority, whether it still holds
// the source's image by its id.
func RequirePromotionImage(ctx context.Context, images LocalImageReader, source *PromotionSource) error {
	if images == nil {
		return ErrBuilderUnavailable
	}
	if present, err := images.LocalImage(ctx, source.Promotion.ImageID); err != nil || present.ConfigDigest != source.Promotion.ImageID {
		if source.Promotion.FromPreview() && source.Promotion.Preview != nil {
			return refusePreviewImageMissing(source.Promotion.Preview.Number)
		}
		return ErrPromotionImageMissing
	}
	return nil
}

// validatePreviewPromotionAdmissionTx re-checks a preview's promotion inside
// the enqueue transaction: into staging only, the preview still eligible, and
// its image the one that was chosen.
func validatePreviewPromotionAdmissionTx(ctx context.Context, tx *sql.Tx, req RunRequest, environmentKind EnvironmentKind, promotion *ReleasePromotion) error {
	number := promotedPreviewNumber(promotion)
	if environmentKind != EnvironmentStaging {
		return RefusePreviewPromotionTarget(number)
	}
	if number <= 0 {
		return ErrPromotionSource
	}
	source, err := previewPromotionSourceTx(ctx, tx, req.ProjectID, previewSelector{number: number}, promotion.ReleaseID, previewAdmission)
	if err != nil {
		return err
	}
	if source.Promotion.EnvironmentID != promotion.EnvironmentID || source.Promotion.ImageID != promotion.ImageID ||
		source.Promotion.ImageDigest != promotion.ImageDigest || source.Promotion.SourceRevision != promotion.SourceRevision {
		return fmt.Errorf("%w: the preview's image changed since it was chosen", ErrPromotionSource)
	}
	return checkPreviewPromotionTargetTx(ctx, tx, req.ProjectID, req.EnvironmentID, req.PlanRevision, source)
}

// refuseShellTasksForPromotedPullRequestTx carries the shell-task rule on to
// production: a staging release whose image was promoted from a preview is
// still the pull request's code, and production's shell tasks would check it
// out and run it there.
func refuseShellTasksForPromotedPullRequestTx(ctx context.Context, q promotionQuery, environmentID int64, planRevision int, source *PromotionSource) error {
	number := pullRequestCodeIn(source)
	if number <= 0 {
		return nil
	}
	var raw string
	if err := q.QueryRowContext(ctx, `SELECT config_json FROM deploy_build_plans WHERE environment_id = ? AND revision = ?`,
		environmentID, planRevision).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: the build plan is missing", ErrInvalidPlan)
		}
		return err
	}
	var build BuildPlanConfig
	if json.Unmarshal([]byte(raw), &build) != nil {
		return ErrInvalidPlan
	}
	return refuseShellTasksForPullRequestCode(build.ReleaseTasks, "production", number)
}

// pullRequestCodeIn is the pull request whose code a promotion's image is,
// or 0: a preview's, or a staging release that was itself promoted from one.
func pullRequestCodeIn(source *PromotionSource) int {
	if source == nil {
		return 0
	}
	if number := promotedPreviewNumber(&source.Promotion); number > 0 {
		return number
	}
	return promotedPreviewNumber(source.Release.Release.Promotion)
}

// promotedPreviewNumber is the pull request a promotion record names: a
// preview's own, or the one a staging release promoted into production came
// from.
func promotedPreviewNumber(promotion *ReleasePromotion) int {
	if promotion == nil || promotion.Preview == nil {
		return 0
	}
	return promotion.Preview.Number
}

// refuseShellTasksForReleaseOfPullRequest is the release-task step's own
// hold on the rule, for every operation that runs release tasks — a
// promotion, and a redeploy or rollback that clones a release whose image is
// a pull request's: its shell tasks would check that pull request's commit
// out and run it on the host. Admission refuses a promotion earlier, in
// words; this is what a rollback meets.
func (e *NormalizedStepExecutor) refuseShellTasksForReleaseOfPullRequest(ctx context.Context, run EngineRun, tasks []ReleaseTaskConfig) error {
	if !needsSourceTree(tasks) {
		return nil
	}
	number := promotedPreviewNumber(promotionFromRun(run))
	if release, err := e.store.ReleaseForRun(ctx, run.ID); err == nil && number == 0 {
		number = promotedPreviewNumber(release.Release.Promotion)
	}
	if number == 0 {
		return nil
	}
	var kind string
	if err := e.store.db.QueryRowContext(ctx, `SELECT kind FROM deploy_environments WHERE id = ?`, run.EnvironmentID).Scan(&kind); err != nil {
		return err
	}
	return refuseShellTasksForPullRequestCode(tasks, kind, number)
}

// promotionSourceForRun re-reads a queued promotion's source when it runs,
// against the plan the run froze.
func (s *OrchestrationStore) promotionSourceForRun(ctx context.Context, run EngineRun, promotion *ReleasePromotion, plan *StoredExecutionPlan) (*PromotionSource, error) {
	if !promotion.FromPreview() {
		source, err := s.PromotionSource(ctx, run.ProjectID, promotion.ReleaseID)
		if err != nil {
			return nil, err
		}
		if number := pullRequestCodeIn(source); number > 0 {
			if err := refuseShellTasksForPullRequestCode(plan.Build.ReleaseTasks, "production", number); err != nil {
				return nil, err
			}
		}
		return source, nil
	}
	source, err := previewPromotionSourceTx(ctx, s.db, run.ProjectID, previewSelector{environmentID: promotion.EnvironmentID}, promotion.ReleaseID, previewRender)
	if err != nil {
		return nil, err
	}
	if err := checkPreviewPromotionTargetTx(ctx, s.db, run.ProjectID, run.EnvironmentID, run.PlanRevision, source); err != nil {
		return nil, err
	}
	return source, nil
}

// previewPromotionView fills the confirmation's source half for a preview.
// What the operator can change — the approval, the image, staging's release
// tasks — is a blocker, so the confirmation still shows what it would
// promote; a preview with nothing live to promote is an error.
func (s *OrchestrationStore) previewPromotionView(ctx context.Context, projectID, targetID int64, request PromotionRequest, view *PromotionPreview) (*PromotionSource, error) {
	candidate, err := findPromotablePreviewTx(ctx, s.db, projectID, previewSelector{number: request.PreviewNumber})
	if err != nil {
		return nil, err
	}
	release, err := previewReleaseTx(ctx, s.db, projectID, candidate, request.ReleaseID, previewAdmission)
	if err != nil {
		return nil, err
	}
	view.Source = PromotionSourceView{
		Kind: PromotionFromPreview, EnvironmentID: release.EnvironmentID, Release: *release, ImageDigest: release.ImageDigest,
		Preview: &PreviewPromotionView{Number: candidate.number},
	}
	_ = s.db.QueryRowContext(ctx, `SELECT e.name, p.title FROM deploy_environments e JOIN deploy_preview_refs p ON p.environment_id = e.id WHERE e.id = ?`,
		release.EnvironmentID).Scan(&view.Source.EnvironmentName, &view.Source.Preview.Title)
	approvedBy, event, approvalErr := previewApprovalTx(ctx, s.db, candidate, release.SourceRevision, previewAdmission)
	var state string
	_ = s.db.QueryRowContext(ctx, `SELECT state FROM deploy_preview_approvals WHERE trigger_id=? AND provider_ref=? AND revision=?`,
		candidate.triggerID, candidate.ref, release.SourceRevision).Scan(&state)
	view.Source.Preview.ApprovedBy, view.Source.Preview.ApprovalState = approvedBy, state
	view.Source.Preview.Author, view.Source.Preview.HeadRepository = event.Author, strings.TrimSpace(event.HeadRepository)
	view.Source.Preview.Fork = event.HeadRepository != "" && candidate.repository != "" && !sameRepository(event.HeadRepository, candidate.repository)
	if approvalErr != nil {
		var refusal *PreviewPromotionRefusal
		if !errors.As(approvalErr, &refusal) {
			return nil, approvalErr
		}
		view.Blockers = append(view.Blockers, promotionBlocker(approvalErr))
	}
	promotable, err := promotableReleaseTx(ctx, s.db, release)
	if err != nil {
		refused := previewImageRefusal(candidate.number, err)
		var refusal *PreviewPromotionRefusal
		if !errors.As(refused, &refusal) {
			return nil, refused
		}
		view.Blockers = append(view.Blockers, promotionBlocker(refused))
		return nil, nil
	}
	view.Source.ImageID, view.Source.ImageSizeBytes = promotable.Snapshot.Image.ConfigDigest, promotable.Image.SizeBytes
	view.Source.Preview.URL = pullRequestURL(candidate.provider, promotable.Identity.Remote, candidate.repository, candidate.number)
	promotable.Promotion = ReleasePromotion{
		EnvironmentID: release.EnvironmentID, EnvironmentName: view.Source.EnvironmentName,
		ReleaseID: release.ID, ReleaseNumber: release.Number, RunID: release.RunID,
		ImageID: promotable.Snapshot.Image.ConfigDigest, ImageDigest: release.ImageDigest,
		SourceRevision: release.SourceRevision, SourceKind: PromotionFromPreview,
		Preview: &PromotedPreview{Number: candidate.number, URL: view.Source.Preview.URL, Repository: candidate.repository,
			HeadRepository: view.Source.Preview.HeadRepository, ApprovedBy: approvedBy},
	}
	// Staging's source and release tasks are checked here rather than left to
	// the request, so the confirmation says what would refuse it. The shell
	// task rule is the target half's, which every confirmation reads.
	var targetKind, targetConfig string
	if err := s.db.QueryRowContext(ctx, `SELECT s.kind, s.config_json FROM deploy_environments e
		JOIN deploy_sources s ON s.environment_id = e.id AND s.revision = e.desired_revision WHERE e.id = ?`, targetID).
		Scan(&targetKind, &targetConfig); err != nil {
		return nil, err
	}
	if err := sameSourceAsTarget(targetKind, targetConfig, promotable.Identity, fmt.Sprintf("PR #%d's preview", candidate.number), "staging"); err != nil {
		refused := previewImageRefusal(candidate.number, err)
		var refusal *PreviewPromotionRefusal
		if !errors.As(refused, &refusal) {
			return nil, refused
		}
		view.Blockers = append(view.Blockers, promotionBlocker(refused))
	}
	return promotable, nil
}

// PreviewPromotion is a promotion of a preview's image into staging, as the
// preview's row reports it: the run that took it, how that run stands, and
// which of the preview's releases — at which commit — it took.
type PreviewPromotion struct {
	RunID          int64      `json:"runId"`
	RunNumber      int64      `json:"runNumber"`
	State          string     `json:"state"`
	EnvironmentID  int64      `json:"environmentId"`
	ReleaseNumber  int64      `json:"releaseNumber"`
	SourceRevision string     `json:"sourceRevision,omitempty"`
	RequestedAt    time.Time  `json:"requestedAt"`
	EndedAt        *time.Time `json:"endedAt,omitempty"`
}
