package api

import (
	"fmt"
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// The terms of migration rehearsal (deploy/rehearsal.go): read by anyone who
// may read the environment, saved by an administrator in a session.

func (s *Server) handleDeploymentRehearsal(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	if _, err := s.modules.deployRuns.EnvironmentExecutionTarget(r.Context(), projectID, environmentID); err != nil {
		return mapDeployError(err)
	}
	policy, err := s.modules.deployRuns.RehearsalPolicy(r.Context(), environmentID)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, policy)
	return nil
}

func (s *Server) handleDeploymentRehearsalPut(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	var policy deploy.RehearsalPolicy
	if err := httpx.DecodeJSON(r, &policy); err != nil {
		return err
	}
	httpx.SetAudit(r, "deploy.rehearsal.update", fmt.Sprint(projectID), map[string]any{
		"environmentId": environmentID, "enabled": policy.Enabled, "connectionId": policy.ConnectionID,
		"whenUnavailable": policy.WhenUnavailable, "restoreTimeoutSeconds": policy.RestoreTimeoutSeconds,
		"maxBackupMb": policy.MaxBackupMB,
	})
	saved, err := s.modules.deployRuns.PutRehearsalPolicy(r.Context(), projectID, environmentID, policy, httpx.MustPrincipal(r).Username())
	if err != nil {
		return mapDeployError(err)
	}
	httpx.JSON(w, http.StatusOK, saved)
	return nil
}
