package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// dockerImageUpdates adapts Docker's registry update check to the deployment
// observer. Deployments never asks a registry anything itself.
type dockerImageUpdates struct{ client *dockerx.Client }

func (o dockerImageUpdates) CheckUpdate(ctx context.Context, reference string, force bool) deploy.ImageUpdateResult {
	status := o.client.CheckUpdate(ctx, reference, force)
	return deploy.ImageUpdateResult{
		Ref: status.Ref, State: status.State, LocalDigest: status.LocalDigest,
		RemoteDigest: status.RemoteDigest, Reason: status.Reason, CheckedAt: status.CheckedAt,
	}
}

// operationsOwners resolves the feature owners an operational summary reads
// from. A nil module stays nil so the summary reports it as unavailable rather
// than reporting an empty success.
func (s *Server) operationsOwners() deploy.OperationsOwners {
	owners := deploy.OperationsOwners{}
	if s.modules.docker != nil {
		owners.Runtime = s.modules.docker
		owners.Dependencies = newDeploymentDependencyObserver(s.Store, s.modules.backupStore, s.modules.docker)
	}
	if s.modules.proxy != nil {
		owners.Proxy = s.modules.proxy
		owners.Certificates = s.modules.proxy
	}
	return owners
}

func (s *Server) handleDeploymentOperations(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	summary, err := s.modules.deployRuns.DeploymentSummary(r.Context(), id, deploy.QueueBudget{
		Heavy: s.Cfg.DeployHeavySlots, Light: s.Cfg.DeployLightSlots,
	})
	if err != nil {
		return mapDeployError(err)
	}
	operations, err := s.modules.deployRuns.Operations(r.Context(), s.operationsOwners(), *summary)
	if err != nil {
		return mapDeployError(err)
	}
	httpx.JSON(w, http.StatusOK, operations)
	return nil
}

func (s *Server) handleDeploymentReleaseComparison(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	environmentID, err := strconv.ParseInt(chi.URLParam(r, "env"), 10, 64)
	if err != nil || environmentID <= 0 {
		return httpx.BadRequest("environment id must be a positive integer")
	}
	releaseID, err := strconv.ParseInt(chi.URLParam(r, "release"), 10, 64)
	if err != nil || releaseID <= 0 {
		return httpx.BadRequest("release id must be a positive integer")
	}
	release, err := s.modules.deployRuns.Release(r.Context(), releaseID)
	if err != nil {
		return mapDeployError(err)
	}
	if release.Release.ProjectID != projectID || release.Release.EnvironmentID != environmentID {
		return httpx.ErrNotFound
	}
	fromID := release.Release.PredecessorReleaseID
	if requested := r.URL.Query().Get("from"); requested != "" {
		parsed, parseErr := strconv.ParseInt(requested, 10, 64)
		if parseErr != nil || parsed <= 0 {
			return httpx.BadRequest("from must be a positive release id")
		}
		fromID = parsed
	}
	var updates deploy.ImageUpdateObserver
	if s.modules.docker != nil {
		updates = dockerImageUpdates{client: s.modules.docker}
	}
	update, err := s.modules.deployRuns.ReleaseUpdate(r.Context(), updates, releaseID)
	if err != nil {
		return mapDeployError(err)
	}
	if fromID == 0 {
		artifacts, artifactErr := s.modules.deployRuns.ReleaseArtifactStatus(r.Context(), releaseID)
		if artifactErr != nil {
			return mapDeployError(artifactErr)
		}
		httpx.JSON(w, http.StatusOK, map[string]any{
			"comparison": nil, "update": update, "artifacts": artifacts,
			"reason": "This is the first recorded release of this environment, so there is nothing to compare it against.",
		})
		return nil
	}
	comparison, err := s.modules.deployRuns.CompareReleaseDetail(r.Context(), fromID, releaseID)
	if err != nil {
		return mapDeployError(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"comparison": comparison, "update": update, "artifacts": comparison.Artifacts,
	})
	return nil
}
