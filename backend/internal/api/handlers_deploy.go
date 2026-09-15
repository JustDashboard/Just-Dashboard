package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

func (s *Server) mountDeployRoutes(r chi.Router) {
	r.Route("/deploy", func(r chi.Router) {
		s.mountBlueprintRoutes(r)
		s.mountGameRoutes(r)
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireSession)
			r.Method(http.MethodGet, "/drafts/{draft}", s.handle(s.handleDeploymentDraftGet))
			r.Method(http.MethodPut, "/drafts/{draft}", s.handle(s.handleDeploymentDraftSave))
			r.Method(http.MethodPost, "/drafts/{draft}/detect", s.handle(s.handleDeploymentDraftDetect))
			r.Method(http.MethodPost, "/drafts/{draft}/preflight", s.handle(s.handleDeploymentDraftPreflight))
		})
		r.Method(http.MethodGet, "/", s.handle(s.handleDeployList))
		r.Method(http.MethodGet, "/hostname", s.handle(s.handleDeploymentHostname))
		r.Method(http.MethodGet, "/{id}", s.handle(s.handleDeployGet))
		r.Method(http.MethodGet, "/{id}/preview-frame", s.handle(s.handleDeploymentPreviewFrame))
		r.Method(http.MethodGet, "/{id}/runs", s.handle(s.handleDeployRuns))
		r.Method(http.MethodGet, "/{id}/runs/{run}", s.handle(s.handleDeploymentRunGet))
		r.Method(http.MethodGet, "/{id}/runs/{run}/logs", s.handle(s.handleDeploymentRunLogs))
		r.Method(http.MethodGet, "/{id}/runs/{run}/metrics", s.handle(s.handleDeploymentRunMetrics))
		r.Method(http.MethodGet, "/{id}/runs/{run}/stream", s.handle(s.handleDeploymentRunStream))
		r.Method(http.MethodGet, "/{id}/commits", s.handle(s.handleDeployCommits))
		r.Method(http.MethodGet, "/{id}/env", s.handle(s.handleDeployEnvList))
		r.Method(http.MethodGet, "/{id}/environments/{env}/releases", s.handle(s.handleDeploymentReleases))
		r.Method(http.MethodGet, "/{id}/operations", s.handle(s.handleDeploymentOperations))
		r.Method(http.MethodGet, "/{id}/environments/{env}/releases/{release}/comparison", s.handle(s.handleDeploymentReleaseComparison))
		r.Method(http.MethodGet, "/{id}/environments/{env}/variables", s.handle(s.handleDeploymentVariables))
		r.Method(http.MethodGet, "/{id}/environments/{env}/pending", s.handle(s.handleDeploymentPending))
		r.Method(http.MethodGet, "/{id}/environments/{env}/configuration", s.handle(s.handleDeploymentConfiguration))
		r.Method(http.MethodGet, "/{id}/environments/{env}/triggers", s.handle(s.handleDeploymentTriggers))
		r.Method(http.MethodGet, "/{id}/environments/{env}/schedules", s.handle(s.handleDeploymentSchedules))
		r.Method(http.MethodGet, "/{id}/environments/{env}/schedules/{schedule}/runs", s.handle(s.handleDeploymentScheduleRuns))
		r.Method(http.MethodGet, "/{id}/previews", s.handle(s.handleDeploymentPreviews))
		r.Method(http.MethodGet, "/notifications", s.handle(s.handleDeploymentNotifications))
		r.Method(http.MethodGet, "/notifications/{channel}/deliveries", s.handle(s.handleDeploymentNotificationDeliveries))

		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapServiceControl))
			r.Method(http.MethodPost, "/{id}/run", s.handle(s.handleDeployRun))
			r.Method(http.MethodPost, "/{id}/environments/{env}/runs", s.handle(s.handleDeploymentRunCreate))
			r.Method(http.MethodPost, "/{id}/runs/{run}/cancel", s.handle(s.handleDeploymentRunCancel))
			r.Method(http.MethodPost, "/{id}/runs/{run}/retry", s.handle(s.handleDeploymentRunRetry))
		})
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			r.Group(func(r chi.Router) {
				r.Use(httpx.RequireSession)
				r.Method(http.MethodPost, "/drafts", s.handle(s.handleDeploymentDraftCreate))
				r.Method(http.MethodPost, "/drafts/{draft}/commit", s.handle(s.handleDeploymentDraftCommit))
				r.Method(http.MethodPost, "/import/preview", s.handle(s.handleDeploymentImportPreview))
				r.Method(http.MethodPost, "/game/import/preview", s.handle(s.handleGameImportPreview))
				r.Method(http.MethodPost, "/import/adopt", s.handle(s.handleDeploymentImportAdopt))
				r.Method(http.MethodGet, "/{id}/environments/{env}/variables/{name}/reveal", s.handle(s.handleDeploymentVariableReveal))
				r.Method(http.MethodPut, "/{id}/environments/{env}/variables/{name}", s.handle(s.handleDeploymentVariablePut))
				r.Method(http.MethodDelete, "/{id}/environments/{env}/variables/{name}", s.handle(s.handleDeploymentVariableDelete))
				r.Method(http.MethodPost, "/{id}/environments/{env}/variables/import", s.handle(s.handleDeploymentDotenvImport))
				r.Method(http.MethodPost, "/{id}/environments/{env}/variables/{name}/generate", s.handle(s.handleDeploymentVariableGenerate))
				r.Method(http.MethodPost, "/{id}/environments/{env}/variables/{name}/rotate", s.handle(s.handleDeploymentVariableRotate))
				r.Method(http.MethodPut, "/{id}/environments/{env}/configuration", s.handle(s.handleDeploymentConfigurationSave))
				r.Method(http.MethodPost, "/{id}/removal-plan", s.handle(s.handleDeploymentRemovalPlan))
				r.Method(http.MethodPost, "/{id}/environments/{env}/triggers", s.handle(s.handleDeploymentTriggerCreate))
				r.Method(http.MethodPut, "/{id}/environments/{env}/triggers/{trigger}", s.handle(s.handleDeploymentTriggerUpdate))
				r.Method(http.MethodPost, "/{id}/environments/{env}/triggers/{trigger}/test", s.handle(s.handleDeploymentTriggerTest))
				r.Method(http.MethodDelete, "/{id}/environments/{env}/triggers/{trigger}", s.handle(s.handleDeploymentTriggerDelete))
				r.Method(http.MethodPost, "/{id}/environments/{env}/watch-paths/simulate", s.handle(s.handleDeploymentWatchPathSimulate))
				r.Method(http.MethodPost, "/{id}/environments/{env}/schedules", s.handle(s.handleDeploymentScheduleCreate))
				r.Method(http.MethodPost, "/{id}/environments/{env}/schedules/test", s.handle(s.handleDeploymentScheduleTest))
				r.Method(http.MethodPut, "/{id}/environments/{env}/schedules/{schedule}", s.handle(s.handleDeploymentScheduleUpdate))
				r.Method(http.MethodDelete, "/{id}/environments/{env}/schedules/{schedule}", s.handle(s.handleDeploymentScheduleDelete))
				r.Method(http.MethodPost, "/notifications", s.handle(s.handleDeploymentNotificationCreate))
				r.Method(http.MethodPut, "/notifications/{channel}", s.handle(s.handleDeploymentNotificationUpdate))
				r.Method(http.MethodPost, "/notifications/{channel}/test", s.handle(s.handleDeploymentNotificationTest))
				r.Method(http.MethodDelete, "/notifications/{channel}", s.handle(s.handleDeploymentNotificationDelete))
			})
			r.Method(http.MethodPost, "/", s.handle(s.handleDeployCreate))
			r.Method(http.MethodPut, "/{id}", s.handle(s.handleDeployUpdate))
			r.Method(http.MethodPost, "/{id}/rotate-secret", s.handle(s.handleDeployRotateSecret))
			r.Method(http.MethodPut, "/{id}/env", s.handle(s.handleDeployEnvSet))
			r.Method(http.MethodGet, "/{id}/env/reveal", s.handle(s.handleDeployEnvReveal))
			r.Method(http.MethodDelete, "/{id}/env/{key}", s.handle(s.handleDeployEnvDelete))
		})
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/{id}/permanent", s.handle(s.handleDeployPurge))
			r.Method(http.MethodDelete, "/{id}", s.handle(s.handleDeployDelete))
			r.Method(http.MethodPost, "/{id}/archive", s.handle(s.handleDeploymentArchive))
			r.Method(http.MethodPost, "/{id}/remove-managed", s.handle(s.handleDeploymentRemoveManaged))
			r.Method(http.MethodPost, "/{id}/rollback", s.handle(s.handleDeployRollback))
			r.Method(http.MethodPost, "/{id}/environments/{env}/rollback", s.handle(s.handleDeploymentReleaseRollback))
		})
	})
}

func mapDeployError(err error) error {
	switch {
	case errors.Is(err, deploy.ErrArchiveRequired):
		return httpx.Err(http.StatusConflict, "deployment_not_archived", err.Error())
	case errors.Is(err, deploy.ErrNotFound):
		return httpx.ErrNotFound
	case errors.Is(err, deploy.ErrAlreadyDeploying):
		return httpx.Err(http.StatusConflict, "already_running", err.Error())
	case errors.Is(err, deploy.ErrDisabled):
		return httpx.Err(http.StatusForbidden, "hook_disabled", err.Error())
	case errors.Is(err, deploy.ErrBadSignature):
		return httpx.Err(http.StatusUnauthorized, "bad_signature", err.Error())
	case errors.Is(err, deploy.ErrEnvironmentNotFound):
		return httpx.Err(http.StatusNotFound, "environment_not_found", err.Error())
	case errors.Is(err, deploy.ErrRunNotFound):
		return httpx.Err(http.StatusNotFound, "run_not_found", err.Error())
	case errors.Is(err, deploy.ErrIdempotencyConflict):
		return httpx.Err(http.StatusConflict, "idempotency_conflict", err.Error())
	case errors.Is(err, deploy.ErrRunTerminal):
		return httpx.Err(http.StatusConflict, "run_terminal", err.Error())
	case errors.Is(err, deploy.ErrRunNotCancellable):
		return httpx.Err(http.StatusConflict, "run_not_cancellable", err.Error())
	case errors.Is(err, deploy.ErrRunNotRetryable):
		return httpx.Err(http.StatusConflict, "run_not_retryable", err.Error())
	case errors.Is(err, deploy.ErrInvalidPlan), errors.Is(err, deploy.ErrArtifactMissing):
		return httpx.Err(http.StatusUnprocessableEntity, "invalid_release", err.Error())
	default:
		return httpx.BadRequest("%v", err)
	}
}

func (s *Server) enrichProject(r *http.Request, p *deploy.Project) {
	p.HookURL = "/api/v1/hooks/deploy/" + p.HookID
	if state, err := s.modules.deployer.Inspect(r.Context(), p); err == nil {
		p.CurrentSHA, p.CurrentRef, p.Dirty = state.SHA, state.Ref, state.Dirty
	}
}

func (s *Server) handleDeployList(w http.ResponseWriter, r *http.Request) error {
	if r.URL.Query().Get("view") == "fleet" {
		fleet, err := s.modules.deployRuns.Fleet(r.Context(), deploy.QueueBudget{
			Heavy: s.Cfg.DeployHeavySlots,
			Light: s.Cfg.DeployLightSlots,
		})
		if err != nil {
			return httpx.Internal(err)
		}
		httpx.JSON(w, http.StatusOK, fleet)
		return nil
	}
	projects, err := s.modules.deployStore.List(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	if r.URL.Query().Get("view") == "archived" {
		archived := make([]*deploy.Project, 0)
		for _, p := range projects {
			if p.ArchivedAt != nil {
				archived = append(archived, p)
			}
		}
		httpx.JSON(w, http.StatusOK, archived)
		return nil
	}
	for _, p := range projects {
		s.enrichProject(r, p)
	}
	httpx.JSON(w, http.StatusOK, projects)
	return nil
}

func (s *Server) handleDeployGet(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	p, err := s.modules.deployStore.Get(r.Context(), id)
	if err != nil {
		return mapDeployError(err)
	}
	s.enrichProject(r, p)
	if last, err := s.modules.deployStore.LastRun(r.Context(), id); err == nil {
		p.LastRun = last
	}
	running, err := s.modules.deployRuns.ProjectActive(r.Context(), id)
	if err != nil {
		return httpx.Internal(err)
	}
	summary, err := s.modules.deployRuns.DeploymentSummary(r.Context(), id, deploy.QueueBudget{
		Heavy: s.Cfg.DeployHeavySlots,
		Light: s.Cfg.DeployLightSlots,
	})
	if err != nil {
		return mapDeployError(err)
	}
	var runtimeOwner deploy.RuntimeObserver
	if s.modules.docker != nil {
		runtimeOwner = s.modules.docker
	}
	runtime := deploy.ObserveRuntimeServices(r.Context(), runtimeOwner, summary.EnvironmentID, summary.LiveReleaseID)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"project": p, "running": running, "deployment": summary, "runtime": runtime,
	})
	return nil
}

type deployProjectRequest struct {
	Name        string `json:"name"`
	RepoPath    string `json:"repoPath"`
	Branch      string `json:"branch"`
	ComposeFile string `json:"composeFile"`
	PreCommand  string `json:"preCommand"`
	PostCommand string `json:"postCommand"`
	Enabled     bool   `json:"enabled"`
}

func (req *deployProjectRequest) toProject() *deploy.Project {
	return &deploy.Project{
		Name: req.Name, RepoPath: req.RepoPath, Branch: req.Branch,
		ComposeFile: req.ComposeFile, PreCommand: req.PreCommand,
		PostCommand: req.PostCommand, Enabled: req.Enabled,
	}
}

func (s *Server) handleDeployCreate(w http.ResponseWriter, r *http.Request) error {
	var req deployProjectRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	project, secret, err := s.modules.deployStore.Create(r.Context(), req.toProject())
	if err != nil {
		return mapDeployError(err)
	}
	s.enrichProject(r, project)
	httpx.SetAudit(r, "deploy.project.create", project.Name,
		map[string]any{"repoPath": project.RepoPath, "branch": project.Branch})
	// The webhook secret is shown exactly once; only its sealed form is kept.
	httpx.JSON(w, http.StatusCreated, map[string]any{"project": project, "secret": secret})
	return nil
}

func (s *Server) handleDeployUpdate(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req deployProjectRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	project, err := s.modules.deployStore.Update(r.Context(), id, req.toProject())
	if err != nil {
		return mapDeployError(err)
	}
	s.enrichProject(r, project)
	httpx.SetAudit(r, "deploy.project.update", project.Name, req)
	httpx.JSON(w, http.StatusOK, project)
	return nil
}

func (s *Server) handleDeployDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	project, err := s.modules.deployStore.Archive(r.Context(), id)
	if err != nil {
		return mapDeployError(err)
	}
	// The legacy DELETE spelling remains compatible, but its 0.6.7 semantics
	// are the same safe archive as POST /archive. Runtime and data are removed
	// only by the separately previewed remove-managed operation.
	httpx.SetAudit(r, "deploy.archive", project.Name, map[string]any{
		"deploymentId": id, "legacyMethod": true, "resourcesRemoved": false,
	})
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleDeployRotateSecret(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	project, err := s.modules.deployStore.Get(r.Context(), id)
	if err != nil {
		return mapDeployError(err)
	}
	secret, err := s.modules.deployStore.RotateSecret(r.Context(), id)
	if err != nil {
		return mapDeployError(err)
	}
	httpx.SetAudit(r, "deploy.project.rotate_secret", project.Name, nil)
	httpx.JSON(w, http.StatusOK, map[string]string{"secret": secret})
	return nil
}

func (s *Server) handleDeployRun(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	project, err := s.modules.deployStore.Get(r.Context(), id)
	if err != nil {
		return mapDeployError(err)
	}
	method, err := s.modules.deployRuns.DeploymentBuildMethod(r.Context(), id)
	if err != nil {
		return mapDeployError(err)
	}
	p := httpx.MustPrincipal(r)
	var run *deploy.EngineRun
	if method == deploy.BuildLegacyCompose {
		run, err = s.enqueueLegacyDeployment(r.Context(), project, deploy.OperationDeploy,
			deploy.TriggerManual, p.Username(), "", "")
	} else {
		environmentID, _, environmentErr := s.modules.deployRuns.ProductionEnvironment(r.Context(), id)
		if environmentErr != nil {
			return mapDeployError(environmentErr)
		}
		run, err = s.enqueueNormalizedDeployment(r.Context(), project, environmentID,
			deploy.OperationDeploy, 0, deploy.TriggerManual, p.Username(), "")
	}
	if err != nil {
		return mapDeployError(err)
	}
	httpx.SetAudit(r, "deploy.run", project.Name, map[string]any{"branch": project.Branch})
	httpx.JSON(w, http.StatusAccepted, map[string]any{
		"started": true, "project": project.Name, "runId": run.ID, "state": run.State,
	})
	return nil
}

type rollbackRequest struct {
	Commit string `json:"commit"`
}

func (s *Server) handleDeployRollback(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req rollbackRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := deploy.ValidateSHA(req.Commit); err != nil {
		return httpx.BadRequest("%v", err)
	}
	project, err := s.modules.deployStore.Get(r.Context(), id)
	if err != nil {
		return mapDeployError(err)
	}
	// No typed phrase: a rollback is itself the recovery action, reached under
	// exactly the pressure that makes a typing exercise harmful, and it is
	// undone by deploying forward again.
	p := httpx.MustPrincipal(r)
	run, err := s.enqueueLegacyDeployment(r.Context(), project, deploy.OperationRollback,
		deploy.TriggerRollback, p.Username(), req.Commit, "")
	if err != nil {
		return mapDeployError(err)
	}
	httpx.SetAudit(r, "deploy.rollback", project.Name, map[string]any{"commit": req.Commit})
	httpx.JSON(w, http.StatusAccepted, map[string]any{
		"started": true, "commit": req.Commit, "runId": run.ID, "state": run.State,
	})
	return nil
}

func (s *Server) handleDeployRuns(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	limit := atoiDefault(r.URL.Query().Get("limit"), 30)
	if r.URL.Query().Get("view") == "engine" {
		runs, err := s.modules.deployRuns.ProjectRuns(r.Context(), id, limit)
		if err != nil {
			return httpx.Internal(err)
		}
		running, activeErr := s.modules.deployRuns.ProjectActive(r.Context(), id)
		if activeErr != nil {
			return httpx.Internal(activeErr)
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"runs": runs, "running": running})
		return nil
	}
	runs, err := s.modules.deployStore.Runs(r.Context(), id, limit)
	if err != nil {
		return httpx.Internal(err)
	}
	running, activeErr := s.modules.deployRuns.ProjectActive(r.Context(), id)
	if activeErr != nil {
		return httpx.Internal(activeErr)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"runs":    runs,
		"running": running,
	})
	return nil
}

type deploymentRunCreateRequest struct {
	Operation deploy.Operation `json:"operation"`
	ReleaseID int64            `json:"releaseId,omitempty"`
}

type deploymentRollbackRequest struct {
	ReleaseID int64 `json:"releaseId"`
}

func (s *Server) handleDeploymentReleases(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	environmentID, err := strconv.ParseInt(chi.URLParam(r, "env"), 10, 64)
	if err != nil || environmentID <= 0 {
		return httpx.BadRequest("environment id must be a positive integer")
	}
	releases, err := s.modules.deployRuns.EnvironmentReleases(
		r.Context(), projectID, environmentID, atoiDefault(r.URL.Query().Get("limit"), 30),
	)
	if err != nil {
		return mapDeployError(err)
	}
	httpx.JSON(w, http.StatusOK, releases)
	return nil
}

func (s *Server) handleDeploymentReleaseRollback(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	environmentID, err := strconv.ParseInt(chi.URLParam(r, "env"), 10, 64)
	if err != nil || environmentID <= 0 {
		return httpx.BadRequest("environment id must be a positive integer")
	}
	var req deploymentRollbackRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.ReleaseID <= 0 {
		return httpx.BadRequest("releaseId must be a positive integer")
	}
	project, err := s.modules.deployStore.Get(r.Context(), projectID)
	if err != nil {
		return mapDeployError(err)
	}
	p := httpx.MustPrincipal(r)
	run, err := s.enqueueNormalizedDeployment(r.Context(), project, environmentID,
		deploy.OperationRollback, req.ReleaseID, deploy.TriggerRollback, p.Username(),
		strings.TrimSpace(r.Header.Get("Idempotency-Key")))
	if err != nil {
		return mapDeployError(err)
	}
	httpx.SetAudit(r, "deploy.release.rollback", fmt.Sprint(req.ReleaseID), map[string]any{
		"deploymentId": projectID, "environmentId": environmentID, "runId": run.ID,
	})
	httpx.JSON(w, http.StatusAccepted, run)
	return nil
}

func (s *Server) handleDeploymentRunCreate(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	environmentID, err := strconv.ParseInt(chi.URLParam(r, "env"), 10, 64)
	if err != nil || environmentID <= 0 {
		return httpx.BadRequest("environment id must be a positive integer")
	}
	var req deploymentRunCreateRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Operation == "" {
		req.Operation = deploy.OperationDeploy
	}
	project, err := s.modules.deployStore.Get(r.Context(), projectID)
	if err != nil {
		return mapDeployError(err)
	}
	target, err := s.modules.deployRuns.EnvironmentExecutionTarget(r.Context(), projectID, environmentID)
	if err != nil {
		return mapDeployError(err)
	}
	p := httpx.MustPrincipal(r)
	var run *deploy.EngineRun
	if target.BuildMethod == deploy.BuildLegacyCompose {
		if req.Operation != deploy.OperationDeploy || req.ReleaseID != 0 {
			return httpx.BadRequest("operation %q is not available for a legacy deployment", req.Operation)
		}
		productionID, _, productionErr := s.modules.deployRuns.ProductionEnvironment(r.Context(), projectID)
		if productionErr != nil || productionID != environmentID {
			return mapDeployError(deploy.ErrEnvironmentNotFound)
		}
		run, err = s.enqueueLegacyDeployment(r.Context(), project, req.Operation,
			deploy.TriggerManual, p.Username(), "", strings.TrimSpace(r.Header.Get("Idempotency-Key")))
	} else {
		if req.Operation == deploy.OperationRollback {
			return httpx.BadRequest("use the rollback endpoint for a release rollback")
		}
		if req.ReleaseID != 0 {
			return httpx.BadRequest("releaseId is accepted only by rollback")
		}
		run, err = s.enqueueNormalizedDeployment(r.Context(), project, environmentID,
			req.Operation, 0, deploy.TriggerManual, p.Username(), strings.TrimSpace(r.Header.Get("Idempotency-Key")))
	}
	if err != nil {
		return mapDeployError(err)
	}
	httpx.SetAudit(r, "deploy.run.request", fmt.Sprint(run.ID), map[string]any{
		"deploymentId": projectID, "environmentId": environmentID, "operation": req.Operation,
	})
	httpx.JSON(w, http.StatusAccepted, run)
	return nil
}

func (s *Server) enqueueNormalizedDeployment(
	ctx context.Context,
	project *deploy.Project,
	environmentID int64,
	operation deploy.Operation,
	targetReleaseID int64,
	trigger deploy.TriggerKind,
	actor, idempotencyKey string,
) (*deploy.EngineRun, error) {
	return s.enqueueNormalizedDeploymentWithMetadata(ctx, project, environmentID, operation,
		targetReleaseID, trigger, actor, idempotencyKey, nil)
}

func (s *Server) enqueueNormalizedDeploymentWithMetadata(
	ctx context.Context,
	project *deploy.Project,
	environmentID int64,
	operation deploy.Operation,
	targetReleaseID int64,
	trigger deploy.TriggerKind,
	actor, idempotencyKey string,
	extraMetadata map[string]any,
) (*deploy.EngineRun, error) {
	target, err := s.modules.deployRuns.EnvironmentExecutionTarget(ctx, project.ID, environmentID)
	if err != nil {
		return nil, err
	}
	if target.BuildMethod == deploy.BuildLegacyCompose {
		return nil, fmt.Errorf("%w: normalized action requires a normalized deployment", deploy.ErrInvalidPlan)
	}
	planRevision := target.DesiredRevision
	variableSnapshotRunID := int64(0)
	var selected *deploy.ReleaseWithArtifacts
	switch operation {
	case deploy.OperationDeploy, deploy.OperationForceBuild, deploy.OperationPreviewCreate, deploy.OperationPreviewUpdate, deploy.OperationScheduled:
		if targetReleaseID != 0 {
			return nil, fmt.Errorf("%w: this operation does not accept a release target", deploy.ErrInvalidPlan)
		}
	case deploy.OperationRedeploy, deploy.OperationRestart:
		if targetReleaseID == 0 {
			targetReleaseID = target.LiveReleaseID
		}
		if targetReleaseID == 0 || targetReleaseID != target.LiveReleaseID {
			return nil, fmt.Errorf("%w: this operation requires the current live release", deploy.ErrInvalidPlan)
		}
	case deploy.OperationRollback:
		if targetReleaseID <= 0 || targetReleaseID == target.LiveReleaseID {
			return nil, fmt.Errorf("%w: rollback requires a retained, non-live release", deploy.ErrInvalidPlan)
		}
	default:
		return nil, fmt.Errorf("%w: operation %q is not a supported deployment action", deploy.ErrInvalidPlan, operation)
	}
	if targetReleaseID != 0 {
		selected, err = s.modules.deployRuns.Release(ctx, targetReleaseID)
		if err != nil {
			return nil, err
		}
		if selected.Release.ProjectID != project.ID || selected.Release.EnvironmentID != environmentID {
			return nil, fmt.Errorf("%w: release target belongs to another environment", deploy.ErrInvalidPlan)
		}
		if operation == deploy.OperationRollback && selected.Release.State != "retained" {
			return nil, fmt.Errorf("%w: rollback target is not retained", deploy.ErrInvalidPlan)
		}
		if (operation == deploy.OperationRedeploy || operation == deploy.OperationRestart) && selected.Release.State != "live" {
			return nil, fmt.Errorf("%w: operation target is no longer live", deploy.ErrInvalidPlan)
		}
		planRevision = selected.Release.PlanRevision
		variableSnapshotRunID = selected.Release.RunID
	}
	metadataValues := map[string]any{
		"compatibility": false, "targetReleaseId": targetReleaseID, "changedPaths": []string{},
	}
	for key, value := range extraMetadata {
		metadataValues[key] = value
	}
	metadata, err := json.Marshal(metadataValues)
	if err != nil {
		return nil, err
	}
	steps := append([]deploy.StepKey(nil), deploy.DefaultStepKeys...)
	slot := deploy.SlotHeavy
	priority := 500
	switch operation {
	case deploy.OperationRedeploy, deploy.OperationRollback:
		steps = []deploy.StepKey{
			deploy.StepRenderRuntime, deploy.StepBackupGate, deploy.StepProvisionCertificate,
			deploy.StepStartCandidate, deploy.StepVerifyReadiness, deploy.StepVerifySmoke,
			deploy.StepActivate, deploy.StepRetirePrevious, deploy.StepRecordRelease,
			deploy.StepNotify,
		}
	case deploy.OperationRestart:
		steps = []deploy.StepKey{
			deploy.StepStartCandidate, deploy.StepVerifyReadiness, deploy.StepVerifySmoke,
			deploy.StepRecordRelease, deploy.StepNotify,
		}
		slot = deploy.SlotLight
	case deploy.OperationPreviewRemove:
		steps = []deploy.StepKey{deploy.StepRetirePrevious, deploy.StepNotify}
		slot = deploy.SlotLight
	}
	if marker, _ := extraMetadata["scheduleMarker"].(bool); marker {
		steps = []deploy.StepKey{deploy.StepNotify}
		slot = deploy.SlotLight
	}
	if operation == deploy.OperationRollback {
		priority = 1000
	}
	digestInput := fmt.Sprintf("normalized:%d:%d:%s:%d:%d", project.ID, environmentID,
		operation, planRevision, targetReleaseID)
	digest := sha256.Sum256([]byte(digestInput))
	run, _, err := s.modules.deployRuns.Enqueue(ctx, deploy.RunRequest{
		ProjectID: project.ID, EnvironmentID: environmentID, Operation: operation,
		Trigger: trigger, Actor: actor, IdempotencyKey: idempotencyKey,
		RequestDigest: hex.EncodeToString(digest[:]), PlanRevision: planRevision,
		VariableSnapshotRunID: variableSnapshotRunID,
		Priority:              priority, SlotClass: slot, Metadata: metadata, Steps: steps,
	})
	if err != nil {
		return nil, err
	}
	s.modules.deployEngine.Notify()
	return run, nil
}

func (s *Server) handleDeploymentRunGet(w http.ResponseWriter, r *http.Request) error {
	projectID, runID, err := deploymentRunIDs(r)
	if err != nil {
		return err
	}
	snapshot, err := s.modules.deployRuns.Snapshot(r.Context(), runID)
	if err != nil {
		return mapDeployError(err)
	}
	if snapshot.Run.ProjectID != projectID {
		return mapDeployError(deploy.ErrRunNotFound)
	}
	httpx.JSON(w, http.StatusOK, snapshot)
	return nil
}

func (s *Server) handleDeploymentRunMetrics(w http.ResponseWriter, r *http.Request) error {
	projectID, runID, err := deploymentRunIDs(r)
	if err != nil {
		return err
	}
	snapshot, err := s.modules.deployRuns.Snapshot(r.Context(), runID)
	if err != nil {
		return mapDeployError(err)
	}
	if snapshot.Run.ProjectID != projectID {
		return mapDeployError(deploy.ErrRunNotFound)
	}
	var owner deploy.MetricsObserver
	if s.modules.metrics != nil {
		owner = s.modules.metrics
	}
	result, err := s.modules.deployRuns.RunMetrics(r.Context(), owner, *snapshot)
	if err != nil {
		return mapDeployError(err)
	}
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

func (s *Server) handleDeploymentRunLogs(w http.ResponseWriter, r *http.Request) error {
	projectID, runID, err := deploymentRunIDs(r)
	if err != nil {
		return err
	}
	snapshot, err := s.modules.deployRuns.Snapshot(r.Context(), runID)
	if err != nil {
		return mapDeployError(err)
	}
	if snapshot.Run.ProjectID != projectID {
		return mapDeployError(deploy.ErrRunNotFound)
	}
	var owner deploy.RuntimeObserver
	if s.modules.docker != nil {
		owner = s.modules.docker
	}
	httpx.JSON(w, http.StatusOK, deploy.ObserveRunLogs(r.Context(), owner, *snapshot))
	return nil
}

func (s *Server) handleDeploymentRunCancel(w http.ResponseWriter, r *http.Request) error {
	projectID, runID, err := deploymentRunIDs(r)
	if err != nil {
		return err
	}
	run, err := s.modules.deployRuns.Run(r.Context(), runID)
	if err != nil || run.ProjectID != projectID {
		if err == nil {
			err = deploy.ErrRunNotFound
		}
		return mapDeployError(err)
	}
	run, err = s.modules.deployEngine.Cancel(r.Context(), runID)
	if err != nil {
		return mapDeployError(err)
	}
	httpx.SetAudit(r, "deploy.run.cancel", fmt.Sprint(runID), nil)
	httpx.JSON(w, http.StatusAccepted, run)
	return nil
}

func (s *Server) handleDeploymentRunRetry(w http.ResponseWriter, r *http.Request) error {
	projectID, runID, err := deploymentRunIDs(r)
	if err != nil {
		return err
	}
	prior, err := s.modules.deployRuns.Run(r.Context(), runID)
	if err != nil || prior.ProjectID != projectID {
		if err == nil {
			err = deploy.ErrRunNotFound
		}
		return mapDeployError(err)
	}
	p := httpx.MustPrincipal(r)
	run, _, err := s.modules.deployRuns.Retry(r.Context(), runID, p.Username(),
		strings.TrimSpace(r.Header.Get("Idempotency-Key")))
	if err != nil {
		return mapDeployError(err)
	}
	s.modules.deployEngine.Notify()
	httpx.SetAudit(r, "deploy.run.retry", fmt.Sprint(run.ID), map[string]any{"retryOfRunId": runID})
	httpx.JSON(w, http.StatusAccepted, run)
	return nil
}

func (s *Server) handleDeploymentRunStream(w http.ResponseWriter, r *http.Request) error {
	projectID, runID, err := deploymentRunIDs(r)
	if err != nil {
		return err
	}
	snapshot, err := s.modules.deployRuns.Snapshot(r.Context(), runID)
	if err != nil {
		return mapDeployError(err)
	}
	if snapshot.Run.ProjectID != projectID {
		return mapDeployError(deploy.ErrRunNotFound)
	}
	after := int64(0)
	if raw := r.URL.Query().Get("after"); raw != "" {
		after, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || after < 0 {
			return httpx.BadRequest("after must be a non-negative sequence")
		}
	}
	backlog, live, unsubscribe, err := s.modules.deployRuns.Subscribe(r.Context(), runID, after)
	if err != nil {
		return mapDeployError(err)
	}
	defer unsubscribe()
	s.recordAudit(r, "deploy.run.stream.open", fmt.Sprint(runID), map[string]any{"after": after})
	conn, err := s.WS.Upgrade(w, r)
	if err != nil {
		return nil
	}
	defer conn.Close()
	ctx, cancel := contextWithCancel(r)
	defer cancel()
	go conn.Keepalive(ctx)
	go conn.DrainControl(cancel)
	if err := conn.Send("snapshot", snapshot); err != nil {
		return nil
	}
	if len(backlog) > 0 {
		if err := conn.Send("events", backlog); err != nil {
			return nil
		}
	}
	batch := make([]deploy.RunEvent, 0, 64)
	flush := time.NewTicker(120 * time.Millisecond)
	defer flush.Stop()
	send := func() bool {
		if len(batch) == 0 {
			return true
		}
		if err := conn.Send("events", batch); err != nil {
			return false
		}
		batch = batch[:0]
		return true
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case event, open := <-live:
			if !open {
				send()
				if final, err := s.modules.deployRuns.Snapshot(context.Background(), runID); err == nil {
					conn.Send("snapshot", final)
				}
				return nil
			}
			batch = append(batch, event)
			if len(batch) == cap(batch) && !send() {
				return nil
			}
		case <-flush.C:
			if !send() {
				return nil
			}
		}
	}
}

func deploymentRunIDs(r *http.Request) (projectID, runID int64, err error) {
	projectID, err = parseID(r)
	if err != nil {
		return 0, 0, err
	}
	runID, parseErr := strconv.ParseInt(chi.URLParam(r, "run"), 10, 64)
	if parseErr != nil || runID <= 0 {
		return 0, 0, httpx.BadRequest("run id must be a positive integer")
	}
	return projectID, runID, nil
}

func (s *Server) handleDeployCommits(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	project, err := s.modules.deployStore.Get(r.Context(), id)
	if err != nil {
		return mapDeployError(err)
	}
	commits, err := s.modules.deployer.History(r.Context(), project,
		atoiDefault(r.URL.Query().Get("limit"), 30))
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, commits)
	return nil
}

func (s *Server) handleDeployEnvList(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	vars, err := s.modules.deployStore.ListEnv(r.Context(), id, false)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, vars)
	return nil
}

// handleDeployEnvReveal returns the plaintext values. It is a separate,
// admin-only, audited endpoint precisely so that reading a project's secrets
// leaves a trail naming who read them and when.
func (s *Server) handleDeployEnvReveal(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	project, err := s.modules.deployStore.Get(r.Context(), id)
	if err != nil {
		return mapDeployError(err)
	}
	vars, err := s.modules.deployStore.ListEnv(r.Context(), id, true)
	if err != nil {
		return httpx.Internal(err)
	}
	p := httpx.MustPrincipal(r)
	s.Audit.Record(r.Context(), audit.Entry{
		UserID: p.UserID(), Username: p.Username(), Role: string(p.Role),
		IP: httpx.ClientIP(r), Actor: p.Kind,
		Action: "deploy.env.reveal", Target: project.Name,
		Method: r.Method, Path: r.URL.Path, Status: http.StatusOK, Success: true,
		Detail: audit.Detail(map[string]any{"count": len(vars)}),
	})
	httpx.JSON(w, http.StatusOK, vars)
	return nil
}

type envSetRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func (s *Server) handleDeployEnvSet(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req envSetRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	project, err := s.modules.deployStore.Get(r.Context(), id)
	if err != nil {
		return mapDeployError(err)
	}
	if err := s.modules.deployStore.SetEnv(r.Context(), id, req.Key, req.Value); err != nil {
		return mapDeployError(err)
	}
	// Only the variable name is recorded — auditing the value would defeat
	// the point of sealing it.
	httpx.SetAudit(r, "deploy.env.set", project.Name, map[string]any{"key": req.Key})
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleDeployEnvDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	key := chi.URLParam(r, "key")
	project, err := s.modules.deployStore.Get(r.Context(), id)
	if err != nil {
		return mapDeployError(err)
	}
	if err := s.modules.deployStore.DeleteEnv(r.Context(), id, key); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "deploy.env.delete", project.Name, map[string]any{"key": key})
	httpx.NoContent(w)
	return nil
}

// maxHookBody bounds what a webhook may post. Provider payloads are tens of
// kilobytes; anything larger is not a push event.
const maxHookBody = 1 << 20

// handleDeployWebhook is the CI entry point. It is the only route reached
// without a dashboard session, so it authenticates by HMAC over the raw body
// with the project's own secret — and it still sits behind the network
// allowlist that fronts the whole API.
func (s *Server) handleDeployWebhook(w http.ResponseWriter, r *http.Request) error {
	hookID := chi.URLParam(r, "hookID")
	httpx.SetAuditActor(r, "webhook")
	project, err := s.modules.deployStore.ByHookID(r.Context(), hookID)
	if err != nil {
		// A wrong hook id and a wrong signature are both reported as
		// unauthorized so the endpoint does not confirm which ids exist. The
		// attempted id goes into the audit trail, though: a run of these is
		// what an enumeration scan looks like from the inside.
		httpx.SetAudit(r, "deploy.webhook", hookID, map[string]any{"reason": "unknown hook id"})
		return httpx.Err(http.StatusUnauthorized, "unauthorized", "unknown or unauthorised deploy hook")
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxHookBody))
	if err != nil {
		return httpx.BadRequest("could not read request body")
	}
	signature := r.Header.Get("X-Hub-Signature-256")
	if signature == "" {
		signature = r.Header.Get("X-Signature-256")
	}
	if err := s.modules.deployStore.VerifySignature(r.Context(), project.ID, body, signature); err != nil {
		httpx.SetAudit(r, "deploy.webhook", project.Name, map[string]any{"reason": "signature mismatch"})
		return httpx.Err(http.StatusUnauthorized, "unauthorized", "unknown or unauthorised deploy hook")
	}
	if !project.Enabled {
		httpx.SetAudit(r, "deploy.webhook", project.Name, map[string]any{"reason": "hook disabled"})
		return httpx.Err(http.StatusForbidden, "hook_disabled", deploy.ErrDisabled.Error())
	}
	httpx.SetAudit(r, "deploy.webhook", project.Name,
		map[string]any{"branch": project.Branch, "bytes": len(body)})
	run, err := s.enqueueLegacyDeployment(r.Context(), project, deploy.OperationDeploy,
		deploy.TriggerLegacyHook, "ci", "", "")
	if err != nil {
		return mapDeployError(err)
	}
	httpx.JSON(w, http.StatusAccepted, map[string]any{
		"accepted": true, "project": project.Name, "branch": project.Branch,
		"runId": run.ID, "state": run.State,
	})
	return nil
}

func (s *Server) enqueueLegacyDeployment(
	ctx context.Context,
	project *deploy.Project,
	operation deploy.Operation,
	trigger deploy.TriggerKind,
	actor, targetCommit, idempotencyKey string,
) (*deploy.EngineRun, error) {
	environmentID, revision, err := s.modules.deployRuns.ProductionEnvironment(ctx, project.ID)
	if err != nil {
		return nil, err
	}
	metadata, err := json.Marshal(map[string]any{
		"compatibility": true, "targetCommit": targetCommit, "changedPaths": []string{},
	})
	if err != nil {
		return nil, err
	}
	digestInput := fmt.Sprintf("legacy:%d:%d:%s:%s:%s:%s",
		project.ID, environmentID, operation, trigger, project.Branch, targetCommit)
	digest := sha256.Sum256([]byte(digestInput))
	priority := 500
	if operation == deploy.OperationRollback {
		priority = 1000
	}
	run, _, err := s.modules.deployRuns.Enqueue(ctx, deploy.RunRequest{
		ProjectID: project.ID, EnvironmentID: environmentID,
		Operation: operation, Trigger: trigger, Actor: actor,
		IdempotencyKey: idempotencyKey,
		RequestDigest:  hex.EncodeToString(digest[:]), PlanRevision: revision,
		Priority: priority, SlotClass: deploy.SlotHeavy,
		Metadata: metadata, Steps: []deploy.StepKey{deploy.StepLegacyPipeline},
	})
	if err != nil {
		return nil, err
	}
	s.modules.deployEngine.Notify()
	return run, nil
}

func (s *Server) handleDeployPurge(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	project, err := s.modules.deployStore.PurgeArchived(r.Context(), id)
	if err != nil {
		return mapDeployError(err)
	}
	httpx.SetAudit(r, "deploy.project.purge", project.Name, map[string]any{"deploymentId": id, "resourcesRemoved": false})
	httpx.NoContent(w)
	return nil
}
