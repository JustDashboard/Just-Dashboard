package api

import (
	"net/http"
	"strconv"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// deploymentSourceUpdateRequest accepts the same DraftSourceConfig shape a
// draft's source step uses, plus the desired-revision guard every other
// environment mutation already requires.
type deploymentSourceUpdateRequest struct {
	Revision int `json:"revision"`
	deploy.DraftSourceConfig
}

// deploymentSourceUpdateResult wraps the refreshed EnvironmentConfiguration
// with the source and the freshly resolved identity: the configuration
// response alone carries no source field, and a UI that just changed a
// branch or image needs to show what the inspection actually found (the
// resolved commit, or the new registry digest) without a second request.
type deploymentSourceUpdateResult struct {
	*deploy.EnvironmentConfiguration
	Source   deploy.DraftSourceConfig `json:"source"`
	Identity deploy.SourceIdentity    `json:"identity"`
}

// handleDeploymentSourceUpdate changes a committed project's source. It
// validates the new source for its own kind, runs the same read-only
// inspection a draft's detect step runs (resolving the Git ref or the
// registry digest) so an unreachable branch or image is refused with the
// adapter's own message before anything is written, then persists a fresh
// source revision. The source kind itself cannot change — SaveEnvironmentSource
// refuses that — because the build and runtime rows it clones forward were
// built for the kind that is already live.
func (s *Server) handleDeploymentSourceUpdate(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	var request deploymentSourceUpdateRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	source := request.DraftSourceConfig
	if err := source.ValidateForDeployment(); err != nil {
		return mapDeploymentPlanningError(err)
	}
	detection, err := s.modules.deploySources.Analyze(r.Context(), source)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	configuration, err := s.modules.deployPlanning.SaveEnvironmentSource(
		r.Context(), projectID, environmentID, request.Revision, source, detection.Source,
	)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	httpx.SetAudit(r, "deploy.source.update", strconv.FormatInt(environmentID, 10), map[string]any{
		"deploymentId": projectID, "environmentId": environmentID,
		"revision": configuration.Revision, "kind": source.Kind,
	})
	httpx.JSON(w, http.StatusOK, deploymentSourceUpdateResult{
		EnvironmentConfiguration: configuration, Source: source, Identity: detection.Source,
	})
	return nil
}
