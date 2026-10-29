package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// The routes of a canary release (deploy/canary.go): what the environment's
// trial is reading, and an operator's promote or abort, which the run that
// holds the trial carries out.

type canaryResponse struct {
	// Canary is the open trial, or the most recent ended one so a page can
	// say how the last one went; null when there never was one.
	Canary *deploy.CanaryWindow `json:"canary"`
}

func (s *Server) handleDeploymentCanary(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	if _, err := s.modules.deployRuns.EnvironmentExecutionTarget(r.Context(), projectID, environmentID); err != nil {
		return mapDeployError(err)
	}
	response := canaryResponse{}
	window, err := s.modules.deployRuns.LatestCanary(r.Context(), environmentID)
	switch {
	case err == nil && window.ProjectID == projectID:
		response.Canary = window
	case err != nil && !errors.Is(err, deploy.ErrNoCanary):
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, response)
	return nil
}

func (s *Server) handleDeploymentCanaryPromote(w http.ResponseWriter, r *http.Request) error {
	return s.decideCanary(w, r, deploy.CanaryPromote, "deploy.canary.promote")
}

func (s *Server) handleDeploymentCanaryAbort(w http.ResponseWriter, r *http.Request) error {
	return s.decideCanary(w, r, deploy.CanaryAbort, "deploy.canary.abort")
}

// decideCanary records the operator's decision on the open trial. It answers
// 202 with the trial: the run holding it carries the decision out within a
// second, and its own transcript and outcome say what happened.
func (s *Server) decideCanary(w http.ResponseWriter, r *http.Request, decision, action string) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	if _, err := s.modules.deployRuns.EnvironmentExecutionTarget(r.Context(), projectID, environmentID); err != nil {
		return mapDeployError(err)
	}
	actor := httpx.MustPrincipal(r).Username()
	window, err := s.modules.deployRuns.RequestCanaryDecision(r.Context(), projectID, environmentID, decision, actor)
	if errors.Is(err, deploy.ErrNoCanary) {
		return httpx.Err(http.StatusConflict, "no_canary",
			"No canary is running for this environment, so there is nothing to promote or abort.")
	}
	if errors.Is(err, deploy.ErrCanaryDecided) {
		return httpx.Err(http.StatusConflict, "canary_decided", err.Error())
	}
	if err != nil {
		return mapDeployError(err)
	}
	httpx.SetAudit(r, action, fmt.Sprint(window.ReleaseID), map[string]any{
		"deploymentId": projectID, "environmentId": environmentID, "runId": window.RunID,
		"canaryId": window.ID, "liveReleaseId": window.LiveReleaseID,
	})
	httpx.JSON(w, http.StatusAccepted, window)
	return nil
}
