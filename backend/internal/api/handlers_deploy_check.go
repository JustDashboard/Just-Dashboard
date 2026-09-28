package api

import (
	"net/http"
	"strconv"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// handleDeploymentCheck is preflight before Deploy is pressed: the saved plan
// evaluated against the commit a deployment would build now, with the same
// evaluation analyze_plan runs before any build. It is advisory — a warning
// never gates a run request, and analyze_plan remains the gate every trigger
// passes — so it carries the capability that starting a run does. It changes
// nothing and is asked on every project arrival and after every save, so it
// is kept out of the audit log the way a read is.
func (s *Server) handleDeploymentCheck(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	var request deploy.DeploymentCheckRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	httpx.SkipAudit(r)
	result, err := s.modules.deployChecker.Check(r.Context(), projectID, environmentID, request)
	if err != nil {
		return mapDeployError(err)
	}
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

// handleDeploymentDetect reads the environment's source again — the branch
// head, or a local checkout's recorded commit — and compares what detection
// proposes with the saved plan, field by field. It writes nothing: Build
// settings applies a field through the ordinary configuration save.
func (s *Server) handleDeploymentDetect(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	var request struct{}
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	proposal, err := s.modules.deployChecker.Detect(r.Context(), projectID, environmentID)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	httpx.SetAudit(r, "deploy.source.detect", strconv.FormatInt(environmentID, 10), map[string]any{
		"deploymentId": projectID, "environmentId": environmentID,
		"revision": proposal.Revision, "changes": len(proposal.Changes),
	})
	httpx.JSON(w, http.StatusOK, proposal)
	return nil
}
