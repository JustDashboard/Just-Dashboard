package api

import (
	"net/http"
	"strconv"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

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
