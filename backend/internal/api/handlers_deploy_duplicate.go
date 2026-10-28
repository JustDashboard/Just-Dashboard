package api

import (
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

type deploymentDuplicateRequest struct {
	Name string `json:"name"`
}

// handleDeploymentDuplicate creates a resumable draft pre-filled from
// projectID's own current desired source and configuration, so the operator
// reviews and commits it through the ordinary draft flow (detect, preflight,
// commit) instead of retyping the whole wizard. It never modifies the source
// project.
func (s *Server) handleDeploymentDuplicate(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	var request deploymentDuplicateRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	// A deploy key's draft is the key's own (draftKeyID); a key narrowed to
	// some deployments reaches this route for those alone (deployKeyGate),
	// and the deployment it commits the draft into is added to its grant.
	principal := httpx.MustPrincipal(r)
	draft, err := s.modules.deployPlanning.DuplicateForKey(r.Context(), projectID, principal.UserID(), principal.Username(), request.Name, draftKeyID(principal))
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	httpx.SetAudit(r, "deploy.project.duplicate", draft.ID, map[string]any{
		"sourceProjectId": projectID, "name": request.Name,
	})
	httpx.JSON(w, http.StatusCreated, draft)
	return nil
}
