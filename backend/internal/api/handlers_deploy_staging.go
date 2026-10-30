package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// handleDeploymentEnvironments lists a deployment's production and staging
// environments for the environment switcher.
func (s *Server) handleDeploymentEnvironments(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	environments, err := s.modules.deployRuns.ProjectEnvironments(r.Context(), projectID)
	if err != nil {
		return mapDeployError(err)
	}
	httpx.JSON(w, http.StatusOK, environments)
	return nil
}

type stagingCreateRequest struct {
	Kind        string `json:"kind"`
	Ref         string `json:"ref"`
	Domain      string `json:"domain"`
	CopySecrets bool   `json:"copySecrets"`
}

// handleDeploymentStagingCreate adds the deployment's staging environment.
// It writes configuration and copies variables, secrets included when asked,
// so it takes what saving a configuration or a variable takes: an
// administrator in a session.
func (s *Server) handleDeploymentStagingCreate(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	var req stagingCreateRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Kind != "" && req.Kind != string(deploy.EnvironmentStaging) {
		return httpx.BadRequest("only a staging environment can be added")
	}
	project, err := s.modules.deployStore.Get(r.Context(), projectID)
	if err != nil {
		return mapDeployError(err)
	}
	productionID, revision, err := s.modules.deployRuns.ProductionEnvironment(r.Context(), projectID)
	if err != nil {
		return mapDeployError(err)
	}
	request := deploy.StagingRequest{
		Ref: strings.TrimSpace(req.Ref), Domain: req.Domain, CopySecrets: req.CopySecrets,
		Actor: httpx.MustPrincipal(r).Username(),
	}
	if request.Ref != "" {
		source, err := s.modules.deployRuns.SourceConfiguration(r.Context(), productionID, revision)
		if err != nil {
			return mapDeployError(err)
		}
		if !deploy.IsRemoteGitSource(source) {
			return mapDeployError(deploy.ErrRefNotApplicable)
		}
		if deploy.IsProviderPullRef(request.Ref) {
			return httpx.Err(http.StatusBadRequest, "invalid_ref", "a pull request head is built by a preview, not by staging")
		}
		// Resolved before the copy is written, so a branch that does not
		// exist yet is refused rather than left for the first run to find.
		request.Revision, err = s.modules.deploySources.ResolveGitRef(r.Context(), source, request.Ref)
		if err != nil {
			return mapDeployError(err)
		}
	}
	copied, err := s.modules.deployPlanning.CreateStagingEnvironment(r.Context(), projectID, request)
	if err != nil {
		return mapDeployError(err)
	}
	skipped := make([]string, 0, len(copied.Skipped))
	for _, item := range copied.Skipped {
		skipped = append(skipped, item.Name)
	}
	httpx.SetAudit(r, "deploy.environment.create", project.Name, map[string]any{
		"deploymentId": projectID, "environmentId": copied.Environment.ID, "kind": deploy.EnvironmentStaging,
		"ref": request.Ref, "revision": request.Revision, "domain": strings.ToLower(strings.TrimSpace(req.Domain)),
		// The flag is named so the audit redactor, which hides any key
		// containing "secret", keeps the decision legible.
		"copiedSensitiveValues": req.CopySecrets, "copied": copied.Copied, "skipped": skipped,
	})
	httpx.JSON(w, http.StatusCreated, copied)
	return nil
}

type stagingRemoveRequest struct {
	Volumes bool `json:"volumes"`
}

// handleDeploymentStagingRemove takes staging off the host and archives it.
// It is destructive — the containers go, and the volumes too when asked —
// with an ordinary confirmation: staging can be added again in a minute, and
// production is never touched.
func (s *Server) handleDeploymentStagingRemove(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	var req stagingRemoveRequest
	if r.ContentLength != 0 {
		if err := httpx.DecodeJSON(r, &req); err != nil {
			return err
		}
	}
	project, err := s.modules.deployStore.Get(r.Context(), projectID)
	if err != nil {
		return mapDeployError(err)
	}
	removal, err := s.modules.deployPlanning.BeginStagingRemoval(r.Context(), projectID, environmentID, req.Volumes)
	if err != nil {
		return mapDeployError(err)
	}
	removed, failure := s.removeStagingResources(r.Context(), removal)
	detail := map[string]any{"deploymentId": projectID, "environmentId": environmentID, "removed": removed, "volumes": req.Volumes}
	if failure != nil {
		// Put staging back so the removal can be retried; what did come off
		// is already gone and is said so.
		if err := s.modules.deployPlanning.AbortStagingRemoval(context.WithoutCancel(r.Context()), environmentID); err != nil {
			return httpx.Internal(err)
		}
		detail["error"] = failure.Error()
		httpx.SetAudit(r, "deploy.environment.remove", project.Name, detail)
		httpx.JSON(w, http.StatusConflict, map[string]any{
			"error":   httpx.Err(http.StatusConflict, "staging_removal_incomplete", failure.Error()),
			"removed": removed,
		})
		return nil
	}
	if err := s.modules.deployPlanning.FinishStagingRemoval(r.Context(), environmentID); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "deploy.environment.remove", project.Name, detail)
	httpx.JSON(w, http.StatusOK, map[string]any{"environmentId": environmentID, "removed": removed})
	return nil
}

// removeStagingResources removes only what BeginStagingRemoval named, through
// the owning features. It stops at the first failure.
func (s *Server) removeStagingResources(ctx context.Context, removal *deploy.StagingRemoval) ([]string, error) {
	removed := []string{}
	if len(removal.Containers) > 0 && s.modules.docker == nil {
		return removed, errors.New("Docker is unavailable")
	}
	for _, id := range removal.Containers {
		if err := s.modules.docker.RemoveContainer(ctx, id, true, true); err != nil && !dockerNotFound(err) {
			return removed, fmt.Errorf("container %s: %w", shortID(id), err)
		}
		removed = append(removed, "container "+shortID(id))
	}
	if removal.ProxySite != "" && s.modules.proxy != nil {
		if err := s.modules.proxy.DeleteSite(ctx, removal.ProxySite); err != nil && !proxySiteMissing(err, removal.ProxySite) {
			return removed, fmt.Errorf("route %s: %w", removal.ProxySite, err)
		}
		removed = append(removed, "route "+removal.ProxySite)
	}
	for _, id := range removal.Networks {
		if s.modules.deployDatabases == nil {
			return removed, errors.New("managed database networks are unavailable")
		}
		if err := s.modules.deployDatabases.RemoveNetworkByID(ctx, id); err != nil {
			return removed, fmt.Errorf("network %s: %w", shortID(id), err)
		}
		removed = append(removed, "network "+shortID(id))
	}
	for _, name := range removal.Volumes {
		if s.modules.docker == nil {
			return removed, errors.New("Docker is unavailable")
		}
		if err := s.modules.docker.RemoveVolume(ctx, name, false); err != nil && !dockerNotFound(err) {
			return removed, fmt.Errorf("volume %s: %w", name, err)
		}
		removed = append(removed, "volume "+name)
	}
	return removed, nil
}

func dockerNotFound(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no such")
}

// proxySiteMissing is a route that was never written — staging deployed
// nothing with a domain — which leaves nothing to remove. A file another
// site still links is not this case.
func proxySiteMissing(err error, name string) bool {
	return err != nil && err.Error() == "no such site: "+name
}

// handleDeploymentPromotionPreview is the Promote confirmation's read: the
// source release's exact image, whether this server still holds it, and how
// the target's configuration differs from the one the source ran it with —
// a staging release's into production (?releaseId=N), or a pull request
// preview's into staging (?source=preview&preview=N).
func (s *Server) handleDeploymentPromotionPreview(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	query := r.URL.Query()
	request := promotionRequest{Source: query.Get("source")}
	if raw := query.Get("releaseId"); raw != "" {
		if request.ReleaseID, err = strconv.ParseInt(raw, 10, 64); err != nil || request.ReleaseID <= 0 {
			return httpx.BadRequest("releaseId must be a positive integer")
		}
	}
	if raw := query.Get("preview"); raw != "" {
		if request.Preview, err = strconv.Atoi(raw); err != nil || request.Preview <= 0 {
			return httpx.BadRequest("preview must be a pull request number")
		}
	}
	named, err := request.validated()
	if err != nil {
		return err
	}
	preview, err := s.modules.deployRuns.PromotionPreviewFor(r.Context(), projectID, environmentID, named, s.promotionImages())
	if err != nil {
		return mapDeployError(err)
	}
	httpx.JSON(w, http.StatusOK, preview)
	return nil
}

func (s *Server) promotionImages() deploy.LocalImageReader {
	if s.modules.promotionImages != nil {
		return s.modules.promotionImages
	}
	if s.modules.deployArtifacts == nil {
		return nil
	}
	return s.modules.deployArtifacts
}

// promotionRequest names the promotion's source: a staging release by id
// (source omitted or "staging"), or a pull request's preview by its number
// (source "preview"; releaseId then may name the preview's live release).
type promotionRequest struct {
	ReleaseID int64  `json:"releaseId"`
	Source    string `json:"source"`
	Preview   int    `json:"preview"`
}

func (req promotionRequest) validated() (deploy.PromotionRequest, error) {
	switch req.Source {
	case "", deploy.PromotionFromStaging:
		if req.ReleaseID <= 0 {
			return deploy.PromotionRequest{}, httpx.BadRequest("releaseId must be a positive integer")
		}
		if req.Preview != 0 {
			return deploy.PromotionRequest{}, httpx.BadRequest(`preview names a pull request only with source "preview"`)
		}
		return deploy.PromotionRequest{Source: deploy.PromotionFromStaging, ReleaseID: req.ReleaseID}, nil
	case deploy.PromotionFromPreview:
		if req.Preview <= 0 {
			return deploy.PromotionRequest{}, httpx.BadRequest("preview must be the pull request's number")
		}
		if req.ReleaseID < 0 {
			return deploy.PromotionRequest{}, httpx.BadRequest("releaseId must be a positive integer")
		}
		return deploy.PromotionRequest{Source: deploy.PromotionFromPreview, PreviewNumber: req.Preview, ReleaseID: req.ReleaseID}, nil
	default:
		return deploy.PromotionRequest{}, httpx.BadRequest(`source must be "staging" or "preview"`)
	}
}

// handleDeploymentPromote queues a run that activates a staging release's
// exact image in production, or a pull request preview's in staging. It
// needs exactly what deploying the target needs, service.control, because
// that is what it does: the target changes to new code, through the same
// checks, cutover and automatic restore. A preview's image is code from a
// pull request, so promoting it takes what approving its commit took — an
// administrator, in a session or with a deploy key, which carries the
// administrator's role.
func (s *Server) handleDeploymentPromote(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	var req promotionRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	named, err := req.validated()
	if err != nil {
		return err
	}
	// Recorded before anything can refuse it, so a refusal is audited under
	// the same action with what was asked for; a success replaces it below.
	asked := map[string]any{"deploymentId": projectID, "environmentId": environmentID, "sourceKind": named.Source}
	target := fmt.Sprint(named.ReleaseID)
	if named.FromPreview() {
		asked["previewNumber"], target = named.PreviewNumber, fmt.Sprintf("PR #%d", named.PreviewNumber)
	}
	httpx.SetAudit(r, "deploy.release.promote", target, asked)
	principal := httpx.MustPrincipal(r)
	if named.FromPreview() && !principal.Can(auth.CapSystemAdmin) {
		return httpx.Err(http.StatusForbidden, "forbidden",
			"promoting a pull request's preview takes an administrator, as approving its commit does ("+string(auth.CapSystemAdmin)+")")
	}
	project, err := s.modules.deployStore.Get(r.Context(), projectID)
	if err != nil {
		return mapDeployError(err)
	}
	run, source, err := s.enqueuePromotion(r.Context(), project, environmentID, named,
		principal.Username(), strings.TrimSpace(r.Header.Get("Idempotency-Key")))
	if err != nil {
		return mapDeployError(err)
	}
	detail := map[string]any{
		"deploymentId": projectID, "environmentId": environmentID, "runId": run.ID,
		"sourceKind":        source.Promotion.SourceKind,
		"fromEnvironmentId": source.Promotion.EnvironmentID, "fromReleaseNumber": source.Promotion.ReleaseNumber,
		"imageId": source.Promotion.ImageID, "imageDigest": source.Promotion.ImageDigest,
		"sourceRevision": source.Promotion.SourceRevision,
	}
	if preview := source.Promotion.Preview; preview != nil {
		detail["previewNumber"], detail["pullRequestUrl"], detail["approvedBy"] = preview.Number, preview.URL, preview.ApprovedBy
	}
	httpx.SetAudit(r, "deploy.release.promote", fmt.Sprint(source.Promotion.ReleaseID), detail)
	httpx.JSON(w, http.StatusAccepted, run)
	return nil
}

// enqueuePromotion builds the promotion run: the target's current plan
// revision and, at enqueue, the target's current variables, with the source
// release named in the run's metadata as the only artifact it may use.
// Production takes a staging release; staging takes a pull request preview's.
func (s *Server) enqueuePromotion(
	ctx context.Context,
	project *deploy.Project,
	environmentID int64,
	request deploy.PromotionRequest,
	actor, idempotencyKey string,
) (*deploy.EngineRun, *deploy.PromotionSource, error) {
	target, err := s.modules.deployRuns.EnvironmentExecutionTarget(ctx, project.ID, environmentID)
	if err != nil {
		return nil, nil, err
	}
	switch {
	case request.FromPreview() && target.Kind != deploy.EnvironmentStaging:
		return nil, nil, deploy.RefusePreviewPromotionTarget(request.PreviewNumber)
	case !request.FromPreview() && target.Kind != deploy.EnvironmentProduction:
		return nil, nil, deploy.ErrPromotionTarget
	}
	if target.BuildMethod == deploy.BuildLegacyCompose {
		return nil, nil, fmt.Errorf("%w: a compatibility deployment cannot take a promotion", deploy.ErrPromotionIncompatible)
	}
	source, err := s.modules.deployRuns.PromotionSourceFor(ctx, project.ID, request)
	if err != nil {
		return nil, nil, err
	}
	if err := deploy.RequirePromotionImage(ctx, s.promotionImages(), source); err != nil {
		return nil, nil, err
	}
	targetSource, err := s.modules.deployRuns.SourceConfiguration(ctx, environmentID, target.DesiredRevision)
	if err != nil {
		return nil, nil, err
	}
	sourceRevision := ""
	if deploy.IsRemoteGitSource(targetSource) && deploy.ValidateSHA(source.Promotion.SourceRevision) == nil {
		// The run names the promoted commit, as a deploy of that commit
		// would; the source identity itself comes from the source release.
		sourceRevision = source.Promotion.SourceRevision
	}
	values := map[string]any{
		"compatibility": false, "targetReleaseId": 0, "changedPaths": []string{},
		"promotion": source.Promotion,
	}
	// The commit the source run read is the one the target now runs; a
	// promotion fetches no source of its own, so it carries that reading.
	if sourceRun, err := s.modules.deployRuns.Run(ctx, source.Promotion.RunID); err == nil {
		var recorded struct {
			Commit json.RawMessage `json:"commit"`
		}
		if json.Unmarshal(sourceRun.Metadata, &recorded) == nil && len(recorded.Commit) > 0 {
			values["commit"] = recorded.Commit
		}
	}
	metadata, err := json.Marshal(values)
	if err != nil {
		return nil, nil, err
	}
	releaseID := source.Promotion.ReleaseID
	digest := sha256.Sum256([]byte(fmt.Sprintf("normalized:%d:%d:%s:%d:%d:%s", project.ID, environmentID,
		deploy.OperationPromote, target.DesiredRevision, releaseID, source.Promotion.ImageID)))
	run, _, err := s.modules.deployRuns.Enqueue(ctx, deploy.RunRequest{
		ProjectID: project.ID, EnvironmentID: environmentID, Operation: deploy.OperationPromote,
		Trigger: deploy.TriggerManual, Actor: actor, IdempotencyKey: idempotencyKey,
		RequestDigest: hex.EncodeToString(digest[:]), PlanRevision: target.DesiredRevision,
		SourceRevision: sourceRevision, Priority: 500, SlotClass: deploy.SlotLight,
		Metadata: metadata, Steps: append([]deploy.StepKey(nil), deploy.PromotionStepKeys...),
	})
	if err != nil {
		return nil, nil, err
	}
	s.modules.deployEngine.Notify()
	return run, source, nil
}

// environmentQuery is the ?environment= a project-level read may be narrowed
// to; zero is production.
func environmentQuery(r *http.Request) (int64, error) {
	raw := r.URL.Query().Get("environment")
	if raw == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, httpx.BadRequest("environment must be a positive integer")
	}
	return id, nil
}
