package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Promotion moves a staging release's exact image into production without
// building anything. It is an ordinary production run whose render step
// takes the image staging ran instead of one a build produced, with
// production's own plan, variables, checks and domains around it, and then
// goes through the same release tasks, readiness checks, cutover and
// automatic restore as any other release. On one host the image is already
// local, so nothing is pushed or pulled: the image is tagged under the
// production release's own name, which is also what keeps retention from
// removing it while production may still roll back to it.

var (
	// ErrPromotionSource refuses a release that is not a finished, kept
	// release of this deployment's staging environment.
	ErrPromotionSource = errors.New("only a successful, retained staging release can be promoted")
	// ErrPromotionTarget refuses promoting into anything but production.
	ErrPromotionTarget = errors.New("releases are promoted into production")
	// ErrPromotionImageMissing refuses a release whose image this server no
	// longer holds. Building it again is a new release, not this one.
	ErrPromotionImageMissing = errors.New("the staging release's image is no longer on this server; deploy staging again and promote the new release")
	// ErrPromotionIncompatible refuses an artifact production could not run
	// as the same thing staging ran.
	ErrPromotionIncompatible = errors.New("this staging release cannot be promoted into production")
)

// ReleasePromotion is where a promoted release came from. It is written into
// the promoted release's immutable provenance and the run's metadata, so
// release history, the run page and the "what changed" comparison can say
// "promoted from staging release #N" — or, for staging, "promoted from PR #N
// (sha)" — with the image's own id.
type ReleasePromotion struct {
	EnvironmentID   int64  `json:"environmentId"`
	EnvironmentName string `json:"environmentName"`
	ReleaseID       int64  `json:"releaseId"`
	ReleaseNumber   int64  `json:"releaseNumber"`
	RunID           int64  `json:"runId"`
	// ImageID is the local image id (the config digest) the container is
	// created from; ImageDigest is the release's recorded image digest,
	// which equals it for an image built here.
	ImageID        string `json:"imageId"`
	ImageDigest    string `json:"imageDigest"`
	SourceRevision string `json:"sourceRevision,omitempty"`
	// SourceKind is where the image was taken from: PromotionFromStaging, or
	// PromotionFromPreview with Preview naming the pull request. A record
	// written before previews could be promoted has none and is staging's.
	SourceKind string           `json:"sourceKind,omitempty"`
	Preview    *PromotedPreview `json:"preview,omitempty"`
}

// PromotionSource is a validated staging release ready to promote.
type PromotionSource struct {
	Release   ReleaseWithArtifacts
	Image     ReleaseArtifact
	Snapshot  runtimeReleaseSnapshot
	Promotion ReleasePromotion
	Identity  SourceIdentity
}

// LocalImageReader reads an image this server already holds. Docker is the
// authority here: a release row saying an artifact is available is not proof
// the bytes are still on disk.
type LocalImageReader interface {
	LocalImage(ctx context.Context, reference string) (ResolvedImage, error)
}

// promotionFromRun is the promotion a run's metadata carries, or nil.
func promotionFromRun(run EngineRun) *ReleasePromotion {
	var metadata struct {
		Promotion *ReleasePromotion `json:"promotion"`
	}
	if json.Unmarshal(run.Metadata, &metadata) != nil || metadata.Promotion == nil || metadata.Promotion.ReleaseID <= 0 {
		return nil
	}
	return metadata.Promotion
}

// promotionFromProvenance is the promotion a release's provenance records.
func promotionFromProvenance(provenance json.RawMessage) *ReleasePromotion {
	var recorded struct {
		Promotion *ReleasePromotion `json:"promotion"`
	}
	if json.Unmarshal(provenance, &recorded) != nil || recorded.Promotion == nil || recorded.Promotion.ReleaseID <= 0 {
		return nil
	}
	return recorded.Promotion
}

type promotionQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// PromotionSource validates releaseID as something production can take:
// a live or retained release of this project's staging environment, built as
// one image whose artifact is still available, from the same kind of source
// production deploys and, for Git, the same repository — production's release
// tasks run that commit's code and its history is read in that repository.
func (s *OrchestrationStore) PromotionSource(ctx context.Context, projectID, releaseID int64) (*PromotionSource, error) {
	return promotionSourceTx(ctx, s.db, projectID, releaseID)
}

func promotionSourceTx(ctx context.Context, q promotionQuery, projectID, releaseID int64) (*PromotionSource, error) {
	release, err := releaseByIDTx(ctx, q, releaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPromotionSource
	}
	if err != nil {
		return nil, err
	}
	if release.ProjectID != projectID {
		return nil, ErrPromotionSource
	}
	var kind EnvironmentKind
	var name string
	var archived int64
	if err := q.QueryRowContext(ctx, `SELECT kind, name, archived_at FROM deploy_environments WHERE id = ?`, release.EnvironmentID).
		Scan(&kind, &name, &archived); err != nil {
		return nil, err
	}
	if kind != EnvironmentStaging || archived != 0 || (release.State != "live" && release.State != "retained") {
		return nil, ErrPromotionSource
	}
	source, err := promotableReleaseTx(ctx, q, release)
	if err != nil {
		return nil, err
	}
	source.Promotion = ReleasePromotion{
		EnvironmentID: release.EnvironmentID, EnvironmentName: name,
		ReleaseID: release.ID, ReleaseNumber: release.Number, RunID: release.RunID,
		ImageID: source.Snapshot.Image.ConfigDigest, ImageDigest: release.ImageDigest,
		SourceRevision: release.SourceRevision, SourceKind: PromotionFromStaging,
	}
	// A staging release that was itself a pull request preview's image is
	// still that pull request's code, and production's record says so.
	if origin := release.Promotion; origin != nil && origin.Preview != nil {
		carried := *origin.Preview
		source.Promotion.Preview = &carried
	}

	var productionKind string
	var productionConfig string
	if err := q.QueryRowContext(ctx, `
		SELECT s.kind, s.config_json FROM deploy_environments e
		  JOIN deploy_sources s ON s.environment_id = e.id AND s.revision = e.desired_revision
		 WHERE e.project_id = ? AND e.kind = 'production' AND e.archived_at = 0`, projectID).
		Scan(&productionKind, &productionConfig); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrEnvironmentNotFound
		}
		return nil, err
	}
	if err := sameSourceAsTarget(productionKind, productionConfig, source.Identity, "staging", "production"); err != nil {
		return nil, err
	}
	return source, nil
}

// promotableReleaseTx reads a release as an image another environment can
// run: built as exactly one image whose artifact is still available, with a
// recorded local image id and a readable source identity. Where the image
// came from is the caller's to check.
func promotableReleaseTx(ctx context.Context, q promotionQuery, release *Release) (*PromotionSource, error) {
	artifacts, err := artifactsForReleaseTx(ctx, q, release.ID)
	if err != nil {
		return nil, err
	}
	source := &PromotionSource{Release: ReleaseWithArtifacts{Release: *release, Artifacts: artifacts}}
	images := 0
	for _, artifact := range artifacts {
		switch artifact.Kind {
		case ArtifactImage:
			images++
			source.Image = artifact
		case ArtifactCompose:
			return nil, fmt.Errorf("%w: a Compose release is several images", ErrPromotionIncompatible)
		}
	}
	if images != 1 {
		return nil, fmt.Errorf("%w: the release has no single image", ErrPromotionIncompatible)
	}
	if source.Image.State != "available" {
		return nil, ErrPromotionImageMissing
	}
	snapshot, err := decodeReleaseRuntimeSnapshot(&source.Release)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPromotionIncompatible, err)
	}
	if snapshot.Compose != nil || !contentDigestRE.MatchString(snapshot.Image.ConfigDigest) {
		return nil, fmt.Errorf("%w: the release names no local image id", ErrPromotionIncompatible)
	}
	source.Snapshot = snapshot
	if json.Unmarshal(release.SourceIdentity, &source.Identity) != nil {
		return nil, fmt.Errorf("%w: the release's source identity is unreadable", ErrPromotionIncompatible)
	}
	return source, nil
}

// sameSourceAsTarget holds a promotion to a target that deploys the same kind
// of source and, for remote Git, the same repository: the target's release
// tasks run the promoted commit's code and its history is read there.
func sameSourceAsTarget(targetKind, targetConfig string, identity SourceIdentity, from, to string) error {
	if SourceKind(targetKind) != identity.Kind {
		return fmt.Errorf("%w: %s built from a %s source and %s deploys a %s source",
			ErrPromotionIncompatible, from, identity.Kind, to, targetKind)
	}
	var target DraftSourceConfig
	if json.Unmarshal([]byte(targetConfig), &target) != nil {
		return ErrInvalidPlan
	}
	if IsRemoteGitSource(target) {
		remote, _, err := remoteForSource(target)
		if err != nil {
			return err
		}
		if !sameGitRemote(remote, identity.Remote) {
			return fmt.Errorf("%w: %s built from %s and %s deploys %s",
				ErrPromotionIncompatible, from, scrubSourceRemote(identity.Remote), to, scrubSourceRemote(remote))
		}
	}
	return nil
}

// sameGitRemote compares two recorded remotes the way a forge would: case and
// a trailing ".git" are spelling, not a different repository.
func sameGitRemote(a, b string) bool {
	normalize := func(value string) string {
		value = strings.ToLower(strings.TrimSpace(value))
		return strings.TrimSuffix(strings.TrimSuffix(value, "/"), ".git")
	}
	return a != "" && normalize(a) == normalize(b)
}

// validatePromotionAdmissionTx re-checks a promotion inside the enqueue
// transaction, so a release retired or pruned since the request was read
// cannot slip into the queue. Once the run row commits, retention keeps the
// source's artifacts for as long as the run is unfinished.
func validatePromotionAdmissionTx(ctx context.Context, tx *sql.Tx, req RunRequest, environmentKind EnvironmentKind) error {
	promotion := promotionFromRun(EngineRun{Metadata: req.Metadata})
	if req.Operation != OperationPromote {
		if promotion != nil {
			return fmt.Errorf("%w: only a promotion carries a promoted release", ErrInvalidPlan)
		}
		return nil
	}
	if promotion.FromPreview() {
		return validatePreviewPromotionAdmissionTx(ctx, tx, req, environmentKind, promotion)
	}
	if environmentKind != EnvironmentProduction {
		return ErrPromotionTarget
	}
	if promotion == nil {
		return ErrPromotionSource
	}
	source, err := promotionSourceTx(ctx, tx, req.ProjectID, promotion.ReleaseID)
	if err != nil {
		return err
	}
	if source.Promotion.ImageID != promotion.ImageID || source.Promotion.ImageDigest != promotion.ImageDigest {
		return fmt.Errorf("%w: the release's image changed since it was chosen", ErrPromotionSource)
	}
	return refuseShellTasksForPromotedPullRequestTx(ctx, tx, req.EnvironmentID, req.PlanRevision, source)
}

// promotedSourceIdentityTx is the source identity a promotion run executes
// with: the staging release's, not production's plan identity. Release tasks
// then materialize the promoted commit, and the release records where its
// image really came from.
func promotedSourceIdentityTx(ctx context.Context, q promotionQuery, run EngineRun) (*SourceIdentity, error) {
	promotion := promotionFromRun(run)
	if promotion == nil {
		return nil, fmt.Errorf("%w: promotion run has no source release", ErrInvalidPlan)
	}
	var raw string
	if err := q.QueryRowContext(ctx, `SELECT source_identity_json FROM deploy_releases WHERE id = ? AND project_id = ?`,
		promotion.ReleaseID, run.ProjectID).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPromotionSource
		}
		return nil, err
	}
	var identity SourceIdentity
	if json.Unmarshal([]byte(raw), &identity) != nil {
		return nil, fmt.Errorf("%w: the promoted release's source identity is unreadable", ErrInvalidPlan)
	}
	return &identity, nil
}

// promotionRetainedWhere matches a release that an unfinished promotion is
// about to take its image from. It is a fragment of the retention planner's
// and the prune reservation's SQL, applied to the release alias given.
func promotionRetainedWhere(alias string) string {
	return `EXISTS(SELECT 1 FROM deploy_runs pr
	  WHERE pr.operation = 'promote' AND pr.` + activeRunWhere + `
	    AND CAST(json_extract(pr.metadata_json, '$.promotion.releaseId') AS INTEGER) = ` + alias + `.id)`
}

// renderPromotedRuntime is render_runtime for a promotion. It builds the
// candidate release from the target's frozen plan and variables — production's
// around a staging release's image, or staging's around a pull request
// preview's — and proves the image is the same one by its id before and after
// tagging it under the target release's name.
func (e *NormalizedStepExecutor) renderPromotedRuntime(
	ctx context.Context,
	execution StepExecution,
	plan *StoredExecutionPlan,
) StepResult {
	promotion := promotionFromRun(execution.Run)
	if promotion == nil {
		return normalizedStepFailure(fmt.Errorf("%w: promotion run has no source release", ErrInvalidPlan))
	}
	source, err := e.store.promotionSourceForRun(ctx, execution.Run, promotion, plan)
	if err != nil {
		return promotionStepFailure(err)
	}
	if source.Promotion.ImageID != promotion.ImageID {
		return promotionStepFailure(fmt.Errorf("%w: the release's image changed since it was chosen", ErrPromotionSource))
	}
	if e.builder == nil || e.builder.backend == nil {
		return builderUnavailableFailure(ErrBuilderUnavailable)
	}
	_ = stepLog(execution, "status", fmt.Sprintf("Promoting %s: image %s, no build",
		promotionSourceWords(source.Promotion), source.Promotion.ImageID))
	if source.Promotion.FromPreview() && source.Promotion.Preview != nil {
		_ = stepLog(execution, "status", fmt.Sprintf("Commit %s was approved by %s; the release runs with staging's plan and variables, never the preview's",
			shortRevision(source.Promotion.SourceRevision), source.Promotion.Preview.ApprovedBy))
	}
	missing := ErrPromotionImageMissing
	if source.Promotion.FromPreview() && source.Promotion.Preview != nil {
		missing = refusePreviewImageMissing(source.Promotion.Preview.Number)
	}
	present, err := e.builder.backend.InspectImage(ctx, source.Promotion.ImageID)
	if err != nil || present.ConfigDigest != source.Promotion.ImageID {
		return promotionStepFailure(missing)
	}
	tag := releaseImageTag(execution.Run.EnvironmentID, execution.Run.ID)
	tagged, err := e.builder.backend.TagImage(ctx, source.Promotion.ImageID, tag)
	if err != nil {
		return promotionStepFailure(missing)
	}
	if tagged.ConfigDigest != source.Promotion.ImageID {
		return promotionStepFailure(fmt.Errorf("%w: %s names image %s, not %s", ErrPromotionIncompatible, tag, tagged.ConfigDigest, source.Promotion.ImageID))
	}
	_ = stepLog(execution, "status", "Tagged image "+source.Promotion.ImageID+" as "+tag)

	image := source.Snapshot.Image
	image.Reference = tag
	artifact := ReleaseArtifactInput{
		Kind: ArtifactImage, Reference: tag, Digest: source.Image.Digest,
		Metadata: append(json.RawMessage(nil), source.Image.Metadata...), SizeBytes: source.Image.SizeBytes,
	}
	dependencies, checks, domains, err := decodeStoredPlanInputs(plan)
	if err != nil {
		return normalizedStepFailure(err)
	}
	snapshot := runtimeReleaseSnapshot{
		Version: 1, Plan: plan.Runtime, Image: image,
		Variables:      append([]ReleaseVariableSnapshot(nil), plan.Variables...),
		Dependencies:   dependencies,
		Checks:         checks,
		Domains:        domains,
		PlanInputsHash: plan.PlanInputsDigest,
		SourceIdentity: source.Identity,
		// Both describe the image's own server, which is the same server
		// the source environment ran.
		ProxyTrust:     append([]string(nil), source.Snapshot.ProxyTrust...),
		WebConcurrency: source.Snapshot.WebConcurrency,
	}
	if err := validateRuntimeActivationStrategy(snapshot); err != nil {
		return normalizedStepFailure(err)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return normalizedStepFailure(err)
	}
	var provenance struct {
		Builder PreparedBuild `json:"builder"`
	}
	_ = json.Unmarshal(source.Release.Release.Provenance, &provenance)
	recorded := source.Promotion
	release, err := e.store.CreateCandidateRelease(ctx, execution.Run, execution.ClaimToken, CandidateReleaseInput{
		Artifacts: []ReleaseArtifactInput{artifact}, Prepared: provenance.Builder,
		RuntimeSnapshot: raw, RuntimeDigest: digestBytes(raw),
		BlueprintID: source.Release.Release.BlueprintID, BlueprintVersion: source.Release.Release.BlueprintVersion,
		SourceIdentity: &source.Identity, Promotion: &recorded,
	})
	if err != nil {
		return normalizedStepFailure(err)
	}
	target := "production"
	if source.Promotion.FromPreview() {
		target = "staging"
	}
	_ = stepLog(execution, "status", fmt.Sprintf("Recorded immutable candidate release #%d from %s with %s's configuration",
		release.Release.Number, promotionSourceWords(source.Promotion), target))
	return StepResult{State: StepPassed, Evidence: mustJSON(map[string]any{
		"releaseId": release.Release.ID, "releaseNumber": release.Release.Number,
		"configDigest": release.Release.ConfigDigest, "promotion": recorded, "tag": tag,
	})}
}

func promotionStepFailure(err error) StepResult {
	var refusal *PreviewPromotionRefusal
	switch {
	case errors.Is(err, ErrPromotionImageMissing):
		message := ErrPromotionImageMissing.Error()
		if errors.As(err, &refusal) {
			message = refusal.Message
		}
		return StepResult{State: StepFailed, ErrorCode: "promotion_image_missing", ErrorMessage: message}
	case errors.Is(err, ErrPromotionSource), errors.Is(err, ErrPromotionIncompatible), errors.Is(err, ErrPromotionTarget):
		return StepResult{State: StepFailed, ErrorCode: "promotion_refused", ErrorMessage: err.Error()}
	default:
		return normalizedStepFailure(err)
	}
}

// promotionSourceWords names a promotion's source the way a transcript
// reads it: "staging release #2", or "PR #12 (abc1234), preview release #3".
func promotionSourceWords(promotion ReleasePromotion) string {
	if promotion.FromPreview() && promotion.Preview != nil {
		return fmt.Sprintf("PR #%d (%s), preview release #%d",
			promotion.Preview.Number, shortRevision(promotion.SourceRevision), promotion.ReleaseNumber)
	}
	return fmt.Sprintf("%s release #%d", strings.ToLower(promotion.EnvironmentName), promotion.ReleaseNumber)
}

// PromotionStepKeys are a promotion's steps: a deploy's from render_runtime
// on. Production's release tasks run, against production's variables and
// therefore production's database, after its backup gate and before
// anything starts, exactly where a build's would — and, when production's
// terms ask for it, rehearsed first against a restored copy in the promoted
// image, exactly as a build's would be. A promotion used to leave the
// rehearsal out, so a release whose migration had only ever met staging's
// data ran it on production's unrehearsed.
var PromotionStepKeys = []StepKey{
	StepRenderRuntime, StepBackupGate, StepRehearseMigration, StepReleaseTask, StepProvisionCertificate,
	StepStartCandidate, StepVerifyReadiness, StepVerifySmoke, StepReplayTraffic, StepCanary, StepActivate,
	StepRetirePrevious, StepRecordRelease, StepNotify,
}

// PromotionPreview is what the Promote confirmation shows before anything is
// queued: the exact image, where it goes, and how the target's configuration
// differs from the one the source ran it with.
type PromotionPreview struct {
	Source PromotionSourceView `json:"source"`
	Target PromotionTargetView `json:"target"`
	// Detail compares the source release with what the target will run:
	// the same image under the target's plan, variables (by digest only),
	// checks and domains.
	Detail ReleaseDetailComparison `json:"detail"`
	// Blockers are why Promote would be refused now; empty means it would
	// be accepted.
	Blockers []PromotionBlocker `json:"blockers"`
}

type PromotionSourceView struct {
	// Kind is PromotionFromStaging or PromotionFromPreview.
	Kind            string  `json:"kind"`
	EnvironmentID   int64   `json:"environmentId"`
	EnvironmentName string  `json:"environmentName"`
	Release         Release `json:"release"`
	ImageID         string  `json:"imageId"`
	ImageDigest     string  `json:"imageDigest"`
	ImageSizeBytes  int64   `json:"imageSizeBytes"`
	ImagePresent    bool    `json:"imagePresent"`
	// Preview is the pull request a preview source was built from.
	Preview *PreviewPromotionView `json:"preview,omitempty"`
}

// PreviewPromotionView is the pull request behind a preview being promoted,
// as the confirmation names it: whose code, from which repository, and who
// approved the exact commit.
type PreviewPromotionView struct {
	Number         int    `json:"number"`
	Title          string `json:"title,omitempty"`
	URL            string `json:"url,omitempty"`
	Author         string `json:"author,omitempty"`
	HeadRepository string `json:"headRepository,omitempty"`
	Fork           bool   `json:"fork"`
	ApprovedBy     string `json:"approvedBy,omitempty"`
	ApprovalState  string `json:"approvalState,omitempty"`
}

type PromotionTargetView struct {
	EnvironmentID   int64    `json:"environmentId"`
	EnvironmentName string   `json:"environmentName"`
	LiveRelease     *Release `json:"liveRelease,omitempty"`
	DesiredRevision int      `json:"desiredRevision"`
	// ActiveRuns are production runs queued or in flight; the promotion is
	// queued behind them and replaces whatever they release.
	ActiveRuns    int  `json:"activeRuns"`
	PromotionOnly bool `json:"promotionOnly"`
	// PendingChanges are production settings saved but not yet live. They go
	// live with the promotion, since it runs production's current plan.
	PendingChanges []PendingChange `json:"pendingChanges"`
	// ReleaseTasks run before the cutover, against production.
	ReleaseTasks []string `json:"releaseTasks"`
	// Rehearsal says production's terms rehearse those tasks against a
	// restored copy of its database before they run for real; a task that
	// fails there ends the promotion with production untouched.
	Rehearsal bool `json:"rehearsal"`
}

type PromotionBlocker struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// PromotionPreview reads everything the confirmation of promoting a staging
// release into production needs.
func (s *OrchestrationStore) PromotionPreview(
	ctx context.Context,
	projectID, environmentID, releaseID int64,
	images LocalImageReader,
) (*PromotionPreview, error) {
	return s.PromotionPreviewFor(ctx, projectID, environmentID, PromotionRequest{Source: PromotionFromStaging, ReleaseID: releaseID}, images)
}

// PromotionPreviewFor reads everything the confirmation needs, for a staging
// release promoted into production or a pull request's preview promoted into
// staging. images may be nil where Docker is unavailable; the image is then
// reported as not found.
func (s *OrchestrationStore) PromotionPreviewFor(
	ctx context.Context,
	projectID, environmentID int64,
	request PromotionRequest,
	images LocalImageReader,
) (*PromotionPreview, error) {
	kind, err := s.EnvironmentKind(ctx, projectID, environmentID)
	if err != nil {
		return nil, err
	}
	switch {
	case request.FromPreview() && kind != EnvironmentStaging:
		return nil, RefusePreviewPromotionTarget(request.PreviewNumber)
	case !request.FromPreview() && kind != EnvironmentProduction:
		return nil, ErrPromotionTarget
	}
	preview := &PromotionPreview{Blockers: []PromotionBlocker{}}
	var source *PromotionSource
	if request.FromPreview() {
		if source, err = s.previewPromotionView(ctx, projectID, environmentID, request, preview); err != nil {
			return nil, err
		}
	} else {
		var sourceErr error
		source, sourceErr = s.PromotionSource(ctx, projectID, request.ReleaseID)
		if sourceErr != nil {
			if !errors.Is(sourceErr, ErrPromotionImageMissing) && !errors.Is(sourceErr, ErrPromotionIncompatible) {
				return nil, sourceErr
			}
			preview.Blockers = append(preview.Blockers, promotionBlocker(sourceErr))
			release, err := s.Release(ctx, request.ReleaseID)
			if err != nil {
				return nil, err
			}
			preview.Source.Kind = PromotionFromStaging
			preview.Source.Release = release.Release
			preview.Source.EnvironmentID = release.Release.EnvironmentID
			preview.Source.ImageDigest = release.Release.ImageDigest
			_ = s.db.QueryRowContext(ctx, `SELECT name FROM deploy_environments WHERE id = ?`, release.Release.EnvironmentID).
				Scan(&preview.Source.EnvironmentName)
		} else {
			preview.Source = PromotionSourceView{
				Kind:          PromotionFromStaging,
				EnvironmentID: source.Promotion.EnvironmentID, EnvironmentName: source.Promotion.EnvironmentName,
				Release: source.Release.Release, ImageID: source.Promotion.ImageID,
				ImageDigest: source.Promotion.ImageDigest, ImageSizeBytes: source.Image.SizeBytes,
			}
		}
	}
	if source != nil {
		if images != nil {
			if present, err := images.LocalImage(ctx, source.Promotion.ImageID); err == nil && present.ConfigDigest == source.Promotion.ImageID {
				preview.Source.ImagePresent = true
			}
		}
		if !preview.Source.ImagePresent {
			missing := ErrPromotionImageMissing
			if request.FromPreview() {
				missing = refusePreviewImageMissing(request.PreviewNumber)
			}
			preview.Blockers = append(preview.Blockers, promotionBlocker(missing))
		}
	}

	target := PromotionTargetView{EnvironmentID: environmentID, PendingChanges: []PendingChange{}, ReleaseTasks: []string{}}
	var liveID int64
	if err := s.db.QueryRowContext(ctx, `SELECT name, desired_revision, live_release_id FROM deploy_environments WHERE id = ?`, environmentID).
		Scan(&target.EnvironmentName, &target.DesiredRevision, &liveID); err != nil {
		return nil, err
	}
	if liveID != 0 {
		if live, err := s.Release(ctx, liveID); err == nil {
			target.LiveRelease = &live.Release
		}
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_runs WHERE environment_id = ? AND `+activeRunWhere, environmentID).
		Scan(&target.ActiveRuns); err != nil {
		return nil, err
	}
	policy, err := gitDeploymentPolicy(ctx, s.db, environmentID)
	if err != nil {
		return nil, err
	}
	target.PromotionOnly = policy.PromotionOnly
	if pending, err := pendingState(ctx, s.db, projectID, environmentID); err == nil && pending.Pending && liveID != 0 {
		target.PendingChanges = pending.Changes
	}
	desired, err := s.desiredRuntimeSnapshot(ctx, environmentID, target.DesiredRevision)
	if err != nil {
		return nil, err
	}
	var build BuildPlanConfig
	var buildJSON string
	if err := s.db.QueryRowContext(ctx, `SELECT config_json FROM deploy_build_plans WHERE environment_id = ? AND revision = ?`,
		environmentID, target.DesiredRevision).Scan(&buildJSON); err == nil && json.Unmarshal([]byte(buildJSON), &build) == nil {
		for _, task := range build.ReleaseTasks {
			target.ReleaseTasks = append(target.ReleaseTasks, task.Name)
		}
	}
	if number := pullRequestCodeIn(source); number > 0 {
		if err := refuseShellTasksForPullRequestCode(build.ReleaseTasks, strings.ToLower(target.EnvironmentName), number); err != nil {
			preview.Blockers = append(preview.Blockers, promotionBlocker(err))
		}
	}
	terms, err := s.RehearsalPolicy(ctx, environmentID)
	if err != nil {
		return nil, err
	}
	target.Rehearsal = terms.Enabled
	preview.Target = target
	preview.Detail = ReleaseDetailComparison{
		Status: statusUnavailable, Fields: []ReleaseFieldChange{}, Variables: []ReleaseListChange{},
		Dependencies: []ReleaseListChange{}, Checks: []ReleaseListChange{}, Domains: []ReleaseListChange{},
		Reason: "The source release has no readable runtime snapshot.",
	}
	if source != nil {
		desired.Image, desired.SourceIdentity = source.Snapshot.Image, source.Identity
		desired.ProxyTrust, desired.WebConcurrency = source.Snapshot.ProxyTrust, source.Snapshot.WebConcurrency
		preview.Detail = compareRuntimeSnapshots(source.Snapshot, desired,
			source.Release.Release.ExpectedDowntime, desired.Plan.Strategy == StrategyStopFirst)
	}
	return preview, nil
}

func promotionBlocker(err error) PromotionBlocker {
	var refusal *PreviewPromotionRefusal
	switch {
	case errors.As(err, &refusal):
		return PromotionBlocker{Code: refusal.Code, Message: refusal.Message}
	case errors.Is(err, ErrPromotionImageMissing):
		return PromotionBlocker{Code: "promotion_image_missing", Message: ErrPromotionImageMissing.Error()}
	default:
		return PromotionBlocker{Code: "promotion_incompatible", Message: err.Error()}
	}
}

// desiredRuntimeSnapshot is the runtime snapshot production's current plan
// would freeze if a run were queued now, read without queueing one: the same
// rows Enqueue snapshots, so the confirmation compares against exactly what
// the promotion will run.
func (s *OrchestrationStore) desiredRuntimeSnapshot(ctx context.Context, environmentID int64, revision int) (runtimeReleaseSnapshot, error) {
	snapshot := runtimeReleaseSnapshot{Version: 1, Variables: []ReleaseVariableSnapshot{}}
	var runtimeJSON string
	if err := s.db.QueryRowContext(ctx, `SELECT config_json FROM deploy_runtime_plans WHERE environment_id = ? AND revision = ?`,
		environmentID, revision).Scan(&runtimeJSON); err != nil {
		return snapshot, fmt.Errorf("%w: production's runtime plan is missing", ErrInvalidPlan)
	}
	if json.Unmarshal([]byte(runtimeJSON), &snapshot.Plan) != nil {
		return snapshot, fmt.Errorf("%w: production's runtime plan is malformed", ErrInvalidPlan)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT key, sensitivity, scopes, value_digest, value_mode FROM deploy_variable_revisions
		 WHERE environment_id = ? AND active = 1 ORDER BY key, id`, environmentID)
	if err != nil {
		return snapshot, err
	}
	for rows.Next() {
		var variable ReleaseVariableSnapshot
		if err := rows.Scan(&variable.Name, &variable.Sensitivity, &variable.Scopes, &variable.ValueDigest, &variable.ValueMode); err != nil {
			rows.Close()
			return snapshot, err
		}
		snapshot.Variables = append(snapshot.Variables, variable)
	}
	if err := rows.Close(); err != nil {
		return snapshot, err
	}
	dependencies, checks, err := currentPlanInputs(ctx, s.db, environmentID)
	if err != nil {
		return snapshot, err
	}
	snapshot.Dependencies, snapshot.Checks, snapshot.Domains, err = decodeStoredPlanInputs(&StoredExecutionPlan{
		Dependencies: dependencies, Checks: checks,
	})
	return snapshot, err
}
