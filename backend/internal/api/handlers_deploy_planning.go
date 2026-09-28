package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

func (s *Server) handleDeploymentDraftCreate(w http.ResponseWriter, r *http.Request) error {
	// Decode an optional empty object so unknown fields are still rejected and
	// clients cannot assume creation accepts wizard data without validation.
	if r.ContentLength > 0 {
		var request struct{}
		if err := httpx.DecodeJSON(r, &request); err != nil {
			return err
		}
	}
	principal := httpx.MustPrincipal(r)
	draft, err := s.modules.deployPlanning.Create(r.Context(), principal.UserID(), principal.Username())
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	httpx.SetAudit(r, "deploy.draft.create", draft.ID, nil)
	httpx.JSON(w, http.StatusCreated, draft)
	return nil
}

// handleDeploymentDraftList answers the caller's own uncommitted drafts so
// the new-project page can offer to resume one instead of starting over.
func (s *Server) handleDeploymentDraftList(w http.ResponseWriter, r *http.Request) error {
	principal := httpx.MustPrincipal(r)
	drafts, err := s.modules.deployPlanning.ListDrafts(r.Context(), principal.UserID())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, drafts)
	return nil
}

// handleDeploymentDraftDiscard throws away one of the caller's unfinished
// setups, so the resume list on the new-project page stays the work that is
// actually still wanted.
func (s *Server) handleDeploymentDraftDiscard(w http.ResponseWriter, r *http.Request) error {
	principal := httpx.MustPrincipal(r)
	id := chi.URLParam(r, "draft")
	if err := s.modules.deployPlanning.Discard(
		r.Context(), id, principal.UserID(), principal.Can(auth.CapSystemAdmin),
	); err != nil {
		return mapDeploymentPlanningError(err)
	}
	httpx.SetAudit(r, "deploy.draft.discard", id, nil)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) handleDeploymentDraftGet(w http.ResponseWriter, r *http.Request) error {
	draft, err := s.deploymentDraftForPrincipal(r)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, draft)
	return nil
}

func (s *Server) handleDeploymentDraftSave(w http.ResponseWriter, r *http.Request) error {
	var request deploy.DraftSaveRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	// A draft becomes a production environment, never a preview.
	if request.Source != nil && deploy.IsProviderPullRef(request.Source.Ref) {
		return refusePullRequestHead(request.Source.Ref)
	}
	principal := httpx.MustPrincipal(r)
	draft, err := s.modules.deployPlanning.Save(
		r.Context(), chi.URLParam(r, "draft"), principal.UserID(),
		principal.Can(auth.CapSystemAdmin), request,
	)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	httpx.SetAudit(r, "deploy.draft.save", draft.ID, map[string]any{
		"step": request.Step, "revision": draft.Revision,
	})
	httpx.JSON(w, http.StatusOK, draft)
	return nil
}

type draftRevisionRequest struct {
	Revision   int    `json:"revision"`
	SelectedID string `json:"selectedId,omitempty"`
}

func (s *Server) handleDeploymentDraftDetect(w http.ResponseWriter, r *http.Request) error {
	var request draftRevisionRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	draft, err := s.deploymentDraftForPrincipal(r)
	if err != nil {
		return err
	}
	if request.Revision != draft.Revision {
		return mapDeploymentPlanningError(deploy.ErrDraftRevision)
	}
	if draft.Data.Source == nil {
		return mapDeploymentPlanningError(deploy.ErrDraftIncomplete)
	}
	detection, err := s.modules.deploySources.Analyze(r.Context(), *draft.Data.Source)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	if request.SelectedID != "" {
		selected := false
		for _, candidate := range detection.Candidates {
			if candidate.ID == request.SelectedID {
				selected = true
				break
			}
		}
		if !selected {
			return mapDeploymentPlanningError(fmt.Errorf(
				"%w: selected candidate %q was not among the detected candidates", deploy.ErrInvalidPlan, request.SelectedID))
		}
		if detection.SelectedID != request.SelectedID {
			detection.SelectionReason = "chosen on the project step"
		}
		detection.SelectedID = request.SelectedID
	}
	principal := httpx.MustPrincipal(r)
	draft, err = s.modules.deployPlanning.SaveDetection(
		r.Context(), draft.ID, principal.UserID(), principal.Can(auth.CapSystemAdmin),
		request.Revision, detection,
	)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	httpx.SetAudit(r, "deploy.draft.detect", draft.ID, map[string]any{
		"revision": draft.Revision, "candidateCount": len(detection.Candidates),
		"truncated": detection.Truncated,
	})
	httpx.JSON(w, http.StatusOK, draft)
	return nil
}

func (s *Server) handleDeploymentDraftPreflight(w http.ResponseWriter, r *http.Request) error {
	var request draftRevisionRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	draft, err := s.deploymentDraftForPrincipal(r)
	if err != nil {
		return err
	}
	if request.Revision != draft.Revision {
		return mapDeploymentPlanningError(deploy.ErrDraftRevision)
	}
	principal := httpx.MustPrincipal(r)
	preflight, err := deploy.PreflightDraftWithSource(
		r.Context(), draft, s.modules.deployPreflight, s.deploymentSourceInspector(), principal.Can(auth.CapSystemAdmin),
	)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	draft, err = s.modules.deployPlanning.SavePreflight(
		r.Context(), draft.ID, principal.UserID(), principal.Can(auth.CapSystemAdmin),
		request.Revision, preflight,
	)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	httpx.SetAudit(r, "deploy.draft.preflight", draft.ID, map[string]any{
		"revision": draft.Revision, "findingCount": len(preflight.Findings), "digest": preflight.Digest,
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"draft": draft, "preflight": preflight})
	return nil
}

func (s *Server) handleDeploymentDraftCommit(w http.ResponseWriter, r *http.Request) error {
	var request deploy.DraftCommitRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	draft, err := s.deploymentDraftForPrincipal(r)
	if err != nil {
		return err
	}
	if draft.Data.Source != nil && draft.Data.Source.Kind == deploy.SourceImport {
		return httpx.Err(http.StatusBadRequest, "import_adopt_required", "use the import adoption endpoint for an observed workload")
	}
	principal := httpx.MustPrincipal(r)
	result, err := s.modules.deployPlanning.Commit(
		r.Context(), chi.URLParam(r, "draft"), principal.UserID(), true, request,
	)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	scheduled := s.applyBlueprintSchedules(r, draft, result)
	httpx.SetAudit(r, "deploy.create", resultProjectTarget(result), map[string]any{
		"draftId": chi.URLParam(r, "draft"), "environmentId": result.EnvironmentID,
		"planRevision": result.PlanRevision, "created": result.Created,
		"blueprintSchedules": scheduled,
	})
	status := http.StatusCreated
	if !result.Created {
		status = http.StatusOK
	}
	httpx.JSON(w, status, result)
	return nil
}

func resultProjectTarget(result *deploy.DraftCommitResult) string {
	if result == nil {
		return ""
	}
	return strconv.FormatInt(result.ProjectID, 10)
}

func (s *Server) handleDeploymentImportPreview(w http.ResponseWriter, r *http.Request) error {
	var source deploy.DraftSourceConfig
	if err := httpx.DecodeJSON(r, &source); err != nil {
		return err
	}
	if source.Kind != deploy.SourceImport {
		return httpx.BadRequest("import preview requires source kind %q", deploy.SourceImport)
	}
	preview, err := s.modules.deploySources.PreviewImport(r.Context(), source)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	httpx.SetAudit(r, "deploy.import.preview", source.ResourceID, map[string]any{
		"kind": preview.Kind, "unsupported": len(preview.Unsupported),
	})
	httpx.JSON(w, http.StatusOK, preview)
	return nil
}

type importAdoptRequest struct {
	DraftID                 string   `json:"draftId"`
	Revision                int      `json:"revision"`
	AcknowledgedWarnings    []string `json:"acknowledgedWarnings"`
	AcknowledgedUnsupported []string `json:"acknowledgedUnsupported"`
}

func (s *Server) handleDeploymentImportAdopt(w http.ResponseWriter, r *http.Request) error {
	var request importAdoptRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	if request.DraftID == "" {
		return httpx.BadRequest("draftId is required")
	}
	draft, err := s.modules.deployPlanning.Get(r.Context(), request.DraftID)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	principal := httpx.MustPrincipal(r)
	if err := deploy.AuthorizeDraft(draft, principal.UserID(), principal.Can(auth.CapSystemAdmin)); err != nil {
		return mapDeploymentPlanningError(err)
	}
	if draft.Data.Source == nil || draft.Data.Source.Kind != deploy.SourceImport {
		return httpx.BadRequest("only an observed import draft can be adopted")
	}
	preview, err := s.modules.deploySources.PreviewImport(r.Context(), *draft.Data.Source)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	if !sameStringSet(preview.Unsupported, request.AcknowledgedUnsupported) {
		return httpx.Err(http.StatusPreconditionRequired, "import_acknowledgement_required",
			"acknowledge the exact unsupported import fields returned by the latest preview")
	}
	result, err := s.modules.deployPlanning.Commit(r.Context(), request.DraftID, principal.UserID(), true,
		deploy.DraftCommitRequest{Revision: request.Revision, AcknowledgedWarnings: request.AcknowledgedWarnings})
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	httpx.SetAudit(r, "deploy.import.adopt", preview.ResourceID, map[string]any{
		"draftId": request.DraftID, "deploymentId": result.ProjectID,
		"environmentId": result.EnvironmentID, "unsupported": len(preview.Unsupported),
	})
	status := http.StatusCreated
	if !result.Created {
		status = http.StatusOK
	}
	httpx.JSON(w, status, result)
	return nil
}

func sameStringSet(expected, actual []string) bool {
	if len(expected) != len(actual) {
		return false
	}
	seen := make(map[string]int, len(expected))
	for _, value := range expected {
		seen[value]++
	}
	for _, value := range actual {
		if seen[value] == 0 {
			return false
		}
		seen[value]--
	}
	return true
}

// deploymentSourceInspector is the analyzer as a SourceInspector, or none: a
// typed nil pointer would read as an inspector and fail on first use.
func (s *Server) deploymentSourceInspector() deploy.SourceInspector {
	if s.modules.deploySources == nil {
		return nil
	}
	return s.modules.deploySources
}

func (s *Server) deploymentDraftForPrincipal(r *http.Request) (*deploy.Draft, error) {
	draft, err := s.modules.deployPlanning.Get(r.Context(), chi.URLParam(r, "draft"))
	if err != nil {
		return nil, mapDeploymentPlanningError(err)
	}
	principal := httpx.MustPrincipal(r)
	if err := deploy.AuthorizeDraft(draft, principal.UserID(), principal.Can(auth.CapSystemAdmin)); err != nil {
		return nil, mapDeploymentPlanningError(err)
	}
	return draft, nil
}

func mapDeploymentPlanningError(err error) error {
	// Checked first, above every errors.Is case below: a RemovalFailure
	// carries its own status and message from the remover that raised it, and
	// must not be reclassified just because its wrapped cause also happens to
	// satisfy one of those sentinels (a missing dependency inside a removal,
	// say, is still a removal_failed, not a deploy_not_found).
	var removal *deploy.RemovalFailure
	if errors.As(err, &removal) {
		status := http.StatusConflict
		if removal.Unavailable {
			status = http.StatusServiceUnavailable
		}
		return httpx.Err(status, "removal_failed", removal.Error())
	}
	switch {
	case errors.Is(err, deploy.ErrPreviewApproval), errors.Is(err, deploy.ErrPreviewIsolation):
		return mapAutomationError(err)
	// Ownership and expiry are distinguished from a genuinely missing id: a
	// 404 for someone else's draft would tell a caller that guessed ids that
	// this one belongs to nobody, and an expired draft is a different,
	// recoverable situation ("start over") from one that never existed.
	case errors.Is(err, deploy.ErrDraftForbidden):
		return httpx.Err(http.StatusForbidden, "draft_forbidden", "this deployment draft belongs to another user")
	case errors.Is(err, deploy.ErrDraftExpired):
		return httpx.Err(http.StatusGone, "draft_expired", "this deployment draft has expired")
	case errors.Is(err, deploy.ErrDraftNotFound):
		return httpx.Err(http.StatusNotFound, "draft_not_found", "deployment draft was not found")
	case errors.Is(err, deploy.ErrImportNotFound):
		return httpx.Err(http.StatusNotFound, "deploy_not_found", "deployment import resource was not found")
	case errors.Is(err, deploy.ErrDraftRevision):
		return httpx.Err(http.StatusConflict, "draft_revision_conflict", err.Error())
	case errors.Is(err, deploy.ErrRevisionConflict):
		return httpx.Err(http.StatusConflict, "revision_conflict", err.Error())
	case errors.Is(err, deploy.ErrEnvironmentNotFound):
		return httpx.Err(http.StatusNotFound, "environment_not_found", err.Error())
	case errors.Is(err, deploy.ErrVariableCycle):
		return httpx.Err(http.StatusUnprocessableEntity, "variable_cycle", err.Error())
	case errors.Is(err, deploy.ErrVariableNotFound):
		return httpx.Err(http.StatusNotFound, "variable_not_found", err.Error())
	case errors.Is(err, deploy.ErrInvalidVariable):
		return httpx.Err(http.StatusBadRequest, "invalid_variable", err.Error())
	case errors.Is(err, deploy.ErrRemovalPlanChanged):
		return httpx.Err(http.StatusConflict, "removal_plan_changed", err.Error())
	case errors.Is(err, deploy.ErrRemovalTarget):
		return httpx.Err(http.StatusBadRequest, "invalid_removal_target", err.Error())
	case errors.Is(err, deploy.ErrDeploymentActive):
		return httpx.Err(http.StatusConflict, "deployment_not_archived", err.Error())
	case errors.Is(err, deploy.ErrNotFound):
		return httpx.Err(http.StatusNotFound, "deploy_not_found", err.Error())
	case errors.Is(err, deploy.ErrDraftCommitted):
		return httpx.Err(http.StatusConflict, "draft_committed", err.Error())
	case errors.Is(err, deploy.ErrDraftIncomplete):
		return httpx.Err(http.StatusUnprocessableEntity, "invalid_plan", err.Error())
	case errors.Is(err, deploy.ErrInvalidPlan):
		apiErr := httpx.Err(http.StatusUnprocessableEntity, "invalid_plan", err.Error())
		var validation *deploy.ValidationError
		if errors.As(err, &validation) {
			apiErr.Field = validation.Field
		}
		return apiErr
	case errors.Is(err, deploy.ErrPreflightBlocked):
		return httpx.Err(http.StatusConflict, "preflight_blocked", err.Error())
	case errors.Is(err, deploy.ErrInvalidSource):
		return httpx.Err(http.StatusBadRequest, "invalid_source", err.Error())
	case errors.Is(err, deploy.ErrInvalidRef):
		return httpx.Err(http.StatusBadRequest, "invalid_ref", err.Error())
	case errors.Is(err, deploy.ErrInvalidImage):
		return httpx.Err(http.StatusBadRequest, "invalid_image", err.Error())
	case errors.Is(err, deploy.ErrInvalidCompose):
		return httpx.Err(http.StatusBadRequest, "invalid_compose", err.Error())
	case errors.Is(err, deploy.ErrUnsupportedSource):
		return httpx.Err(http.StatusUnprocessableEntity, "unsupported_source", err.Error())
	case errors.Is(err, deploy.ErrRefNotFound):
		return httpx.Err(http.StatusBadRequest, "ref_not_found", err.Error())
	case errors.As(err, new(*deploy.SourceFailure)):
		return httpx.Err(http.StatusBadGateway, sourceFailureCode(err), err.Error())
	case errors.Is(err, deploy.ErrGitUnavailable):
		return httpx.Err(http.StatusServiceUnavailable, "git_unavailable", "Git source evidence is unavailable")
	case errors.Is(err, deploy.ErrDockerUnavailable):
		return httpx.Err(http.StatusServiceUnavailable, "docker_unavailable", "Docker or registry evidence is unavailable")
	case errors.Is(err, deploy.ErrSourceUnavailable):
		return httpx.Err(http.StatusUnprocessableEntity, "invalid_source", "deployment source evidence is unavailable")
	default:
		return httpx.Internal(err)
	}
}

// applyBlueprintSchedules creates the schedules a blueprint marked as defaults,
// once, when its deployment is first created. A failure here is recorded in the
// audit entry rather than failing the deployment: the schedules are a
// convenience, and the deployment already exists by this point.
func (s *Server) applyBlueprintSchedules(
	r *http.Request,
	draft *deploy.Draft,
	result *deploy.DraftCommitResult,
) []string {
	if !result.Created || draft.Data.Source == nil ||
		draft.Data.Source.Mode != deploy.SourceModeBlueprint || s.modules.deployAutomation == nil {
		return nil
	}
	plan, err := deploy.RenderBlueprintPlan(*draft.Data.Source, draft.Data.Intent.Name)
	if err != nil {
		return nil
	}
	created := []string{}
	for _, write := range deploy.BlueprintSchedules(plan.Rendered) {
		if _, err := s.modules.deployAutomation.CreateSchedule(
			r.Context(), result.ProjectID, result.EnvironmentID, write); err == nil {
			created = append(created, write.Name)
		}
	}
	return created
}
