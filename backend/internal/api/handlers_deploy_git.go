package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

func (s *Server) handleDeploymentGitWatch(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	status, err := s.modules.deployRuns.GitWatchStatus(r.Context(), projectID, environmentID)
	if err != nil {
		return mapDeployError(err)
	}
	httpx.JSON(w, http.StatusOK, status)
	return nil
}

func (s *Server) dispatchGitDeployment(ctx context.Context, target deploy.GitWatchTarget, revision, key string) (*deploy.EngineRun, error) {
	project, err := s.modules.deployStore.Get(ctx, target.ProjectID)
	if err != nil {
		return nil, err
	}
	run, err := s.enqueueNormalizedDeploymentAtSource(ctx, project, target.EnvironmentID,
		deploy.OperationDeploy, 0, deploy.TriggerGitPush, "git-monitor", key,
		map[string]any{"automatic": true}, revision, target.PlanRevision)
	detail := map[string]any{"environmentId": target.EnvironmentID, "sourceRevision": revision}
	if run != nil {
		detail["runId"] = run.ID
	}
	encoded, _ := json.Marshal(detail)
	s.Audit.Record(ctx, audit.Entry{Actor: "system", Action: "deploy.git.change", Target: fmt.Sprint(target.ProjectID), Success: err == nil, Detail: string(encoded)})
	return run, err
}
