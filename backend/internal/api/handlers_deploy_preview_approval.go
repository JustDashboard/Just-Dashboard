package api

import (
	"fmt"
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

func (s *Server) handleDeploymentPreviewApprovals(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	items, err := s.modules.deployAutomation.ListPreviewApprovals(r.Context(), projectID)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, items)
	return nil
}

func (s *Server) handleDeploymentPreviewApprove(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	approvalID, err := automationParam(r, "approval")
	if err != nil {
		return err
	}
	var request struct {
		Revision string `json:"revision"`
		Deploy   bool   `json:"deploy"`
	}
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	actor := httpx.MustPrincipal(r).Username()
	httpx.SetAudit(r, "deploy.preview.approve", fmt.Sprint(approvalID), map[string]any{"deploymentId": projectID, "revision": request.Revision, "deployRequested": request.Deploy})
	approval, err := s.modules.deployAutomation.ApprovePreview(r.Context(), projectID, approvalID, request.Revision, actor)
	if err != nil {
		return mapAutomationError(err)
	}
	trigger, _, err := s.modules.deployAutomation.TriggerByID(r.Context(), projectID, approval.TriggerID)
	if err != nil {
		return mapAutomationError(err)
	}
	preview, created, err := s.modules.deployAutomation.EnsurePreview(r.Context(), trigger, approval.Event)
	if err != nil {
		return mapAutomationError(err)
	}
	result := map[string]any{"preview": preview, "approval": approval}
	if request.Deploy {
		project, err := s.modules.deployStore.Get(r.Context(), projectID)
		if err != nil {
			return mapDeployError(err)
		}
		// A retried approval must answer with the run the first one created,
		// even though the preview it created now exists and would be labelled
		// an update.
		idempotencyKey := fmt.Sprintf("preview-approval:%d:%d", approvalID, approval.Generation)
		run, err := s.modules.deployRuns.RunByIdempotencyKey(r.Context(), preview.EnvironmentID, idempotencyKey)
		if err != nil {
			return mapDeployError(err)
		}
		if run == nil {
			operation := deploy.OperationPreviewUpdate
			if created {
				operation = deploy.OperationPreviewCreate
			}
			run, err = s.enqueueNormalizedDeploymentAtSource(r.Context(), project, preview.EnvironmentID, operation, 0, deploy.TriggerPreview, actor, idempotencyKey, nil, approval.Revision, "", 0)
			if err != nil {
				return mapDeployError(err)
			}
		}
		result["runId"] = run.ID
	}
	httpx.SetAudit(r, "deploy.preview.approve", approval.ProviderRef, map[string]any{"deploymentId": projectID, "revision": approval.Revision, "environmentId": preview.EnvironmentID, "deployed": request.Deploy})
	httpx.JSON(w, http.StatusAccepted, result)
	return nil
}

// handleDeploymentPreviewReject refuses a pending or previously approved
// preview revision. A rejected revision cannot be approved afterward without
// a new event from the provider: the trigger's next delivery for this pull
// request inserts a fresh, independent pending approval at its own revision.
func (s *Server) handleDeploymentPreviewReject(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	approvalID, err := automationParam(r, "approval")
	if err != nil {
		return err
	}
	var request struct {
		Revision string `json:"revision"`
	}
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	actor := httpx.MustPrincipal(r).Username()
	approval, err := s.modules.deployAutomation.RejectPreview(r.Context(), projectID, approvalID, request.Revision, actor)
	if err != nil {
		return mapAutomationError(err)
	}
	httpx.SetAudit(r, "deploy.preview.reject", approval.ProviderRef, map[string]any{
		"deploymentId": projectID, "revision": approval.Revision,
	})
	httpx.JSON(w, http.StatusOK, approval)
	return nil
}
