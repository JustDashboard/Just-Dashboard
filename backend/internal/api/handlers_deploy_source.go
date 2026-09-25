package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// refusePullRequestHead answers a source ref that names a pull request's head
// where no preview is being made. Such a ref reaches an environment only
// through "Test this pull request", where an administrator approves the exact
// commit; typed into a project's source it would build anyone's pull request
// as production.
func refusePullRequestHead(ref string) error {
	return httpx.Err(http.StatusBadRequest, "invalid_ref",
		fmt.Sprintf("%s is a pull request head; a project's source is a branch or tag, and a pull request is built as a preview from its page", ref))
}

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
	// Proposal compares the plan with what detection read at the new source,
	// so the page can offer the fields detection now answers differently.
	Proposal *deploy.DetectionProposal `json:"proposal,omitempty"`
}

// handleDeploymentSourceUpdate changes a committed project's source. It
// validates the new source for its own kind, runs the same read-only
// inspection a draft's detect step runs (resolving the Git ref or the
// registry digest) so an unreachable branch or image is refused with the
// adapter's own message before anything is written, then persists a fresh
// source revision whose detection evidence is what that inspection read. The
// source kind itself cannot change — SaveEnvironmentSource refuses that —
// because the build and runtime rows it clones forward were built for the
// kind that is already live.
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
	if deploy.IsProviderPullRef(source.Ref) {
		target, err := s.modules.deployRuns.EnvironmentExecutionTarget(r.Context(), projectID, environmentID)
		if err != nil {
			return mapDeployError(err)
		}
		if target.Kind != deploy.EnvironmentPreview {
			return refusePullRequestHead(source.Ref)
		}
	}
	detection, err := s.modules.deploySources.Analyze(r.Context(), source)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	configuration, proposal, err := s.modules.deployPlanning.SaveEnvironmentSourceDetection(
		r.Context(), projectID, environmentID, request.Revision, source, detection,
	)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	httpx.SetAudit(r, "deploy.source.update", strconv.FormatInt(environmentID, 10), map[string]any{
		"deploymentId": projectID, "environmentId": environmentID,
		"revision": configuration.Revision, "kind": source.Kind,
	})
	httpx.JSON(w, http.StatusOK, deploymentSourceUpdateResult{
		EnvironmentConfiguration: configuration, Source: source, Identity: detection.Source, Proposal: proposal,
	})
	return nil
}
