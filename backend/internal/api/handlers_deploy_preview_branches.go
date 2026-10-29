package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// Database branches' routes (deploy/preview_branches.go,
// deployment_preview_branches.go):
//
//	PUT  /deploy/{id}/environments/{env}/database-links/{connection}/previews
//	     what one linked database gives the environment's previews: nothing,
//	     a branch from the newest backup, or a branch with columns masked.
//	     system.admin, session; audited deploy.preview_branch.policy.
//	POST /deploy/{id}/pull-requests/{number}/preview/branches/{connection}/refresh
//	     a preview's branch again from the newest backup, keeping its address.
//	     system.admin, session; audited deploy.preview_branch.refresh.
//
// The policy is read on the database links report, and each preview's
// branches on the pull-request listing, beside its services.

type previewBranchPolicyRequest struct {
	Mode          string   `json:"mode"`
	MaskedColumns []string `json:"maskedColumns"`
}

func connectionParam(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "connection"), 10, 64)
	if err != nil || id <= 0 {
		return 0, httpx.BadRequest("connection must be a positive integer")
	}
	return id, nil
}

func (s *Server) handleDeploymentPreviewBranchPolicyPut(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	connectionID, err := connectionParam(r)
	if err != nil {
		return err
	}
	var request previewBranchPolicyRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	httpx.SetAudit(r, "deploy.preview_branch.policy", fmt.Sprint(projectID), map[string]any{
		"environmentId": environmentID, "connectionId": connectionID, "mode": request.Mode,
		"maskedColumns": request.MaskedColumns,
	})
	saved, err := s.modules.deployRuns.PutPreviewBranchPolicy(r.Context(), projectID, environmentID, deploy.PreviewBranchPolicy{
		ConnectionID: connectionID, Mode: request.Mode, MaskedColumns: request.MaskedColumns,
	}, httpx.MustPrincipal(r).Username())
	if err != nil {
		return mapDeployError(err)
	}
	httpx.JSON(w, http.StatusOK, saved)
	return nil
}

func (s *Server) handleDeploymentPreviewBranchRefresh(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	number, err := pullNumber(r)
	if err != nil {
		return err
	}
	connectionID, err := connectionParam(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	preview, err := s.modules.deployAutomation.PreviewByNumber(ctx, projectID, number)
	if err != nil {
		return httpx.Internal(err)
	}
	if preview == nil || preview.State != "open" {
		return httpx.Err(http.StatusNotFound, "preview_not_found", fmt.Sprintf("pull request #%d has no open preview", number))
	}
	httpx.SetAudit(r, "deploy.preview_branch.refresh", strconv.Itoa(number), map[string]any{
		"deploymentId": projectID, "environmentId": preview.EnvironmentID, "connectionId": connectionID,
	})
	// The ready branch's container and volume go before the new one is asked
	// for: the new one answers by the same name on the preview's network.
	if current, err := s.modules.deployRuns.ActivePreviewBranch(ctx, preview.EnvironmentID, connectionID); err == nil && current.State == deploy.PreviewBranchReady {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		removeErr := s.modules.rehearser.removeRehearsals(cleanup, map[string]string{rehearsalLabel: previewBranchRehearsal(current.ID)}, nil)
		cancel()
		// Without Docker there is nothing to remove, and nothing restores
		// the new branch either until it is back.
		if removeErr != nil && !errors.Is(removeErr, dockerx.ErrUnavailable) {
			return httpx.Err(http.StatusConflict, "preview_branch_busy", "the branch's copy could not be removed yet: "+removeErr.Error())
		}
	}
	ended, next, err := s.modules.deployRuns.RefreshPreviewBranch(ctx, preview.EnvironmentID, connectionID, httpx.MustPrincipal(r).Username())
	switch {
	case errors.Is(err, deploy.ErrPreviewBranchNotFound):
		return httpx.Err(http.StatusNotFound, "preview_branch_not_found", "the preview holds no branch of that database")
	case errors.Is(err, deploy.ErrPreviewBranchFork):
		return httpx.Err(http.StatusConflict, "preview_branch_fork", err.Error())
	case errors.Is(err, deploy.ErrPreviewBranchPolicy):
		return httpx.Err(http.StatusConflict, "preview_branch_policy", "the environment no longer gives its previews a branch of that database; set it again under Settings → Databases")
	case errors.Is(err, deploy.ErrPreviewBranchEnded):
		return httpx.Err(http.StatusConflict, "preview_branch_busy", err.Error())
	case err != nil:
		return mapDeployError(err)
	}
	s.modules.previewBranches.Poke()
	httpx.JSON(w, http.StatusAccepted, map[string]any{"branch": next, "replaced": ended.ID})
	return nil
}

// attachPreviewBranches puts each listed preview's database branches on it.
func (s *Server) attachPreviewBranches(ctx context.Context, previews []deploy.PreviewRef) {
	for index := range previews {
		branches, err := s.modules.deployRuns.PreviewBranches(ctx, previews[index].EnvironmentID)
		if err == nil && len(branches) > 0 {
			previews[index].Branches = branches
		}
	}
}
