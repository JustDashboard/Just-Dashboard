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
	// The reading is an addition to the watch: a host that cannot be asked
	// leaves it out rather than failing the section it sits in.
	if s.modules.commitStatuses != nil {
		if reading, err := s.modules.commitStatuses.Describe(r.Context(), projectID, environmentID); err == nil {
			status.CommitStatus = reading
		}
	}
	httpx.JSON(w, http.StatusOK, status)
	return nil
}

func (s *Server) handleDeploymentGitPolicyPut(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	// promotionOnly is optional: a client that predates it keeps whatever
	// the environment has rather than switching it off by omission.
	var request struct {
		deploy.GitDeploymentPolicy
		PromotionOnly *bool `json:"promotionOnly"`
	}
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	policy := request.GitDeploymentPolicy
	if request.PromotionOnly != nil {
		policy.PromotionOnly = *request.PromotionOnly
	} else if current, err := s.modules.deployRuns.GitDeploymentPolicy(r.Context(), projectID, environmentID); err == nil {
		policy.PromotionOnly = current.PromotionOnly
	}
	httpx.SetAudit(r, "deploy.git.policy", fmt.Sprint(projectID), map[string]any{
		"environmentId": environmentID, "automatic": policy.Automatic, "promotionOnly": policy.PromotionOnly,
	})
	updated, err := s.modules.deployRuns.SetGitDeploymentPolicy(r.Context(), projectID, environmentID, policy)
	if err != nil {
		return mapDeployError(err)
	}
	httpx.JSON(w, http.StatusOK, updated)
	return nil
}

func (s *Server) dispatchGitDeployment(ctx context.Context, target deploy.GitWatchTarget, revision, key string, changedPaths []string) (*deploy.EngineRun, error) {
	project, err := s.modules.deployStore.Get(ctx, target.ProjectID)
	if err != nil {
		return nil, err
	}
	metadata := map[string]any{"automatic": true, "gitPolicy": target.PolicyKey}
	if len(changedPaths) > 0 {
		metadata["changedPaths"] = changedPaths
	}
	run, err := s.enqueueNormalizedDeploymentAtSource(ctx, project, target.EnvironmentID,
		deploy.OperationDeploy, 0, deploy.TriggerGitPush, "git-monitor", key,
		metadata, revision, "", target.PlanRevision, target.PolicyKey)
	detail := map[string]any{"environmentId": target.EnvironmentID, "sourceRevision": revision}
	if run != nil {
		detail["runId"] = run.ID
	}
	encoded, _ := json.Marshal(detail)
	s.Audit.Record(ctx, audit.Entry{Actor: "system", Action: "deploy.git.change", Target: fmt.Sprint(target.ProjectID), Success: err == nil, Detail: string(encoded)})
	return run, err
}
