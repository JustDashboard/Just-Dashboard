package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// The routes of metric-gated rollback (deploy/rollback_watch.go): what an
// environment's window is doing, its terms, the instant rollback an operator
// presses, and keeping the new release early.

type rollbackWatchResponse struct {
	Policy deploy.RollbackPolicy `json:"policy"`
	// Watch is the open window, or the most recent closed one so a page can
	// say what the last one decided; null when there never was one.
	Watch *deploy.RollbackWatch `json:"watch"`
	// Preview is true for a preview environment, which is never kept warm.
	Preview bool `json:"preview"`
}

func (s *Server) handleDeploymentRollbackWatch(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	target, err := s.modules.deployRuns.EnvironmentExecutionTarget(r.Context(), projectID, environmentID)
	if err != nil {
		return mapDeployError(err)
	}
	policy, err := s.modules.deployRuns.RollbackPolicy(r.Context(), environmentID)
	if err != nil {
		return httpx.Internal(err)
	}
	response := rollbackWatchResponse{Policy: policy, Preview: target.Kind == deploy.EnvironmentPreview}
	watch, err := s.modules.deployRuns.LatestRollbackWatch(r.Context(), environmentID)
	switch {
	case err == nil:
		response.Watch = watch
	case !errors.Is(err, deploy.ErrNoRollbackWatch):
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, response)
	return nil
}

func (s *Server) handleDeploymentRollbackPolicyPut(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	var policy deploy.RollbackPolicy
	if err := httpx.DecodeJSON(r, &policy); err != nil {
		return err
	}
	httpx.SetAudit(r, "deploy.rollback_policy.update", fmt.Sprint(projectID), map[string]any{
		"environmentId": environmentID, "keepWarm": policy.KeepWarm, "windowSeconds": policy.WindowSeconds,
		"automatic": policy.Automatic, "errorRateMargin": policy.ErrorRateMargin,
		"latencyFactor": policy.LatencyFactor, "minRequests": policy.MinRequests,
	})
	updated, err := s.modules.deployRuns.PutRollbackPolicy(r.Context(), projectID, environmentID, policy, httpx.MustPrincipal(r).Username())
	if err != nil {
		return mapDeployError(err)
	}
	httpx.JSON(w, http.StatusOK, updated)
	return nil
}

// openWatchFor is the environment's window if it is one an operator may act
// on now: open, watching, and not already being rolled back.
func (s *Server) openWatchFor(ctx context.Context, projectID, environmentID int64) (*deploy.RollbackWatch, error) {
	watch, err := s.modules.deployRuns.OpenRollbackWatch(ctx, environmentID)
	if errors.Is(err, deploy.ErrNoRollbackWatch) || (err == nil && watch.ProjectID != projectID) {
		return nil, httpx.Err(http.StatusConflict, "no_warm_release",
			"No release is kept warm for this environment, so there is nothing to switch back to instantly. Roll back from the release list instead.")
	}
	if err != nil {
		return nil, httpx.Internal(err)
	}
	if watch.State != deploy.RollbackWatchWatching || watch.RollbackRunID != 0 {
		return nil, httpx.Err(http.StatusConflict, "rollback_in_progress",
			fmt.Sprintf("Release #%d's window is already being acted on.", watch.ReleaseNumber))
	}
	return watch, nil
}

// handleDeploymentInstantRollback switches traffic back to the warm release.
// It is destructive and ordinarily confirmed, as a rollback from the release
// list is: it takes the live release out of service.
func (s *Server) handleDeploymentInstantRollback(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	project, err := s.modules.deployStore.Get(r.Context(), projectID)
	if err != nil {
		return mapDeployError(err)
	}
	watch, err := s.openWatchFor(r.Context(), projectID, environmentID)
	if err != nil {
		return err
	}
	p := httpx.MustPrincipal(r)
	run, err := s.enqueueInstantRollback(r.Context(), project, *watch, deploy.TriggerRollback, p.Username(),
		strings.TrimSpace(r.Header.Get("Idempotency-Key")), false, "")
	if err != nil {
		return mapDeployError(err)
	}
	if err := s.modules.deployRuns.RecordRollbackRun(r.Context(), watch.ID, run.ID); err != nil && !errors.Is(err, deploy.ErrRollbackWatchUnchanged) {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "deploy.release.instant_rollback", fmt.Sprint(watch.WarmReleaseID), map[string]any{
		"deploymentId": projectID, "environmentId": environmentID, "runId": run.ID,
		"watchId": watch.ID, "fromReleaseId": watch.ReleaseID,
	})
	httpx.JSON(w, http.StatusAccepted, run)
	return nil
}

// handleDeploymentKeepRelease ends the window now: the operator is satisfied
// with the new release, and the warm one gives its memory back.
func (s *Server) handleDeploymentKeepRelease(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	watch, err := s.openWatchFor(r.Context(), projectID, environmentID)
	if err != nil {
		return err
	}
	httpx.SetAudit(r, "deploy.release.keep", fmt.Sprint(watch.ReleaseID), map[string]any{
		"deploymentId": projectID, "environmentId": environmentID, "watchId": watch.ID,
		"warmReleaseId": watch.WarmReleaseID,
	})
	if err := s.modules.deployExecutor.KeepRelease(r.Context(), *watch, httpx.MustPrincipal(r).Username()); err != nil {
		if errors.Is(err, deploy.ErrRollbackWatchUnchanged) {
			return httpx.Err(http.StatusConflict, "rollback_in_progress", "The window changed while it was being closed.")
		}
		return httpx.Internal(err)
	}
	closed, err := s.modules.deployRuns.RollbackWatch(r.Context(), watch.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, closed)
	return nil
}

// enqueueInstantRollback is the one way an instant rollback run is made, for
// the button and the watcher alike.
func (s *Server) enqueueInstantRollback(
	ctx context.Context,
	project *deploy.Project,
	watch deploy.RollbackWatch,
	trigger deploy.TriggerKind,
	actor, idempotencyKey string,
	automatic bool,
	reason string,
) (*deploy.EngineRun, error) {
	return s.enqueueNormalizedDeploymentWithMetadata(ctx, project, watch.EnvironmentID, deploy.OperationRollback,
		watch.WarmReleaseID, trigger, actor, idempotencyKey, map[string]any{
			"instant": true, "watchId": watch.ID, "automatic": automatic, "reason": reason,
		})
}

// enqueueAutomaticRollback is the watcher's enqueue. The key names the window
// and the attempt, so a pass that asks twice for the same decision gets the
// same run.
func (s *Server) enqueueAutomaticRollback(ctx context.Context, watch deploy.RollbackWatch, verdict deploy.RollbackVerdict, attempt int) (*deploy.EngineRun, error) {
	project, err := s.modules.deployStore.Get(ctx, watch.ProjectID)
	if err != nil {
		return nil, err
	}
	return s.enqueueInstantRollback(ctx, project, watch, deploy.TriggerAutoRollback, "system",
		fmt.Sprintf("rollback-watch-%d-%d", watch.ID, attempt), true, verdict.Reason)
}

func (s *Server) auditRollbackWatch(ctx context.Context, action string, watch deploy.RollbackWatch, detail map[string]any, success bool) {
	encoded, _ := json.Marshal(detail)
	s.Audit.Record(ctx, audit.Entry{
		Actor: "system", Action: action, Target: fmt.Sprint(watch.ProjectID),
		Success: success, Detail: string(encoded),
	})
}
