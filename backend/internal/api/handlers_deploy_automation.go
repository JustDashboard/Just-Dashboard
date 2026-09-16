package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

func (s *Server) handleDeploymentTriggers(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	items, err := s.modules.deployAutomation.ListTriggers(r.Context(), projectID, environmentID)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, items)
	return nil
}

func (s *Server) handleDeploymentTriggerCreate(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	var req deploy.TriggerWrite
	if err = httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	result, err := s.modules.deployAutomation.CreateTrigger(r.Context(), projectID, environmentID, req)
	if err != nil {
		return mapAutomationError(err)
	}
	httpx.SetAudit(r, "deploy.trigger.create", result.Trigger.Name, map[string]any{"kind": result.Trigger.Kind, "repository": result.Trigger.Config.Repository})
	httpx.JSON(w, http.StatusCreated, result)
	return nil
}
func automationParam(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		return 0, httpx.BadRequest("%s id must be positive", name)
	}
	return id, nil
}
func (s *Server) handleDeploymentTriggerUpdate(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	triggerID, err := automationParam(r, "trigger")
	if err != nil {
		return err
	}
	var req deploy.TriggerWrite
	if err = httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	item, err := s.modules.deployAutomation.UpdateTrigger(r.Context(), projectID, environmentID, triggerID, req)
	if err != nil {
		return mapAutomationError(err)
	}
	httpx.SetAudit(r, "deploy.trigger.update", item.Name, map[string]any{"kind": item.Kind})
	httpx.JSON(w, http.StatusOK, item)
	return nil
}
func (s *Server) handleDeploymentTriggerDelete(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	triggerID, err := automationParam(r, "trigger")
	if err != nil {
		return err
	}
	if err = s.modules.deployAutomation.DeleteTrigger(r.Context(), projectID, environmentID, triggerID); err != nil {
		return mapAutomationError(err)
	}
	httpx.SetAudit(r, "deploy.trigger.delete", fmt.Sprint(triggerID), nil)
	httpx.NoContent(w)
	return nil
}
func (s *Server) handleDeploymentTriggerTest(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	triggerID, err := automationParam(r, "trigger")
	if err != nil {
		return err
	}
	item, _, err := s.modules.deployAutomation.TriggerByID(r.Context(), projectID, triggerID)
	if err != nil || item.EnvironmentID != environmentID {
		return mapAutomationError(deploy.ErrTriggerNotFound)
	}
	httpx.SetAudit(r, "deploy.trigger.test", item.Name, map[string]any{"kind": item.Kind, "provider": item.Provider})
	httpx.JSON(w, http.StatusOK, map[string]any{"valid": true, "enabled": item.Enabled, "provider": item.Provider})
	return nil
}

type watchSimulationRequest struct {
	ChangedPaths []string `json:"changedPaths"`
	Include      []string `json:"include"`
	Exclude      []string `json:"exclude"`
}

func (s *Server) handleDeploymentWatchPathSimulate(w http.ResponseWriter, r *http.Request) error {
	if _, _, err := deploymentEnvironmentIDs(r); err != nil {
		return err
	}
	var req watchSimulationRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	matched := deploy.MatchWatchPaths(req.ChangedPaths, req.Include, req.Exclude)
	httpx.SetAudit(r, "deploy.trigger.test", "watch-paths", map[string]any{"changedCount": len(req.ChangedPaths), "matched": matched})
	httpx.JSON(w, http.StatusOK, map[string]any{"matched": matched})
	return nil
}

func (s *Server) handleDeploymentSchedules(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	items, err := s.modules.deployAutomation.ListSchedules(r.Context(), projectID, environmentID)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, items)
	return nil
}

func (s *Server) handleDeploymentScheduleCreate(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	var req deploy.ScheduleWrite
	if err = httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	item, err := s.modules.deployAutomation.CreateSchedule(r.Context(), projectID, environmentID, req)
	if err != nil {
		return mapAutomationError(err)
	}
	httpx.SetAudit(r, "deploy.schedule.create", item.Name, map[string]any{"expression": item.Expression, "timezone": item.Timezone})
	httpx.JSON(w, http.StatusCreated, item)
	return nil
}
func (s *Server) handleDeploymentScheduleUpdate(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	scheduleID, err := automationParam(r, "schedule")
	if err != nil {
		return err
	}
	var req deploy.ScheduleWrite
	if err = httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	item, err := s.modules.deployAutomation.UpdateSchedule(r.Context(), projectID, environmentID, scheduleID, req)
	if err != nil {
		return mapAutomationError(err)
	}
	httpx.SetAudit(r, "deploy.schedule.update", item.Name, map[string]any{"expression": item.Expression, "timezone": item.Timezone})
	httpx.JSON(w, http.StatusOK, item)
	return nil
}
func (s *Server) handleDeploymentScheduleTest(w http.ResponseWriter, r *http.Request) error {
	if _, _, err := deploymentEnvironmentIDs(r); err != nil {
		return err
	}
	var req deploy.ScheduleWrite
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	timezone := req.Timezone
	if timezone == "" {
		timezone = "UTC"
	}
	next, err := deploy.NextCron(req.Expression, timezone, time.Now().UTC())
	if err != nil {
		return mapAutomationError(err)
	}
	httpx.SetAudit(r, "deploy.schedule.test", req.Name, map[string]any{"expression": req.Expression, "timezone": timezone})
	httpx.JSON(w, http.StatusOK, map[string]any{"nextRunAt": next})
	return nil
}
func (s *Server) handleDeploymentScheduleDelete(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	scheduleID, err := automationParam(r, "schedule")
	if err != nil {
		return err
	}
	if err = s.modules.deployAutomation.DeleteSchedule(r.Context(), projectID, environmentID, scheduleID); err != nil {
		return mapAutomationError(err)
	}
	httpx.SetAudit(r, "deploy.schedule.delete", fmt.Sprint(scheduleID), nil)
	httpx.NoContent(w)
	return nil
}
func (s *Server) handleDeploymentScheduleRuns(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	scheduleID, err := automationParam(r, "schedule")
	if err != nil {
		return err
	}
	runs, err := s.modules.deployRuns.ProjectRuns(r.Context(), projectID, 200)
	if err != nil {
		return mapDeployError(err)
	}
	filtered := make([]deploy.EngineRun, 0)
	for _, run := range runs {
		if run.EnvironmentID != environmentID {
			continue
		}
		var metadata struct {
			ScheduleID int64 `json:"scheduleId"`
		}
		if json.Unmarshal(run.Metadata, &metadata) == nil && metadata.ScheduleID == scheduleID {
			filtered = append(filtered, run)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"runs": filtered})
	return nil
}

func (s *Server) handleDeploymentPreviews(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	items, err := s.modules.deployAutomation.ListPreviews(r.Context(), projectID)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, items)
	return nil
}

func (s *Server) handleDeploymentNotifications(w http.ResponseWriter, r *http.Request) error {
	items, err := s.modules.deployAutomation.ListNotificationChannels(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, items)
	return nil
}
func (s *Server) handleDeploymentNotificationDeliveries(w http.ResponseWriter, r *http.Request) error {
	id, err := automationParam(r, "channel")
	if err != nil {
		return err
	}
	items, err := s.modules.deployAutomation.NotificationDeliveries(r.Context(), id, 50)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, items)
	return nil
}
func (s *Server) handleDeploymentNotificationCreate(w http.ResponseWriter, r *http.Request) error {
	var req deploy.NotificationWrite
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	channel, secret, err := s.modules.deployAutomation.CreateNotificationChannel(r.Context(), req)
	if err != nil {
		return mapAutomationError(err)
	}
	httpx.SetAudit(r, "deploy.notification.create", channel.Name, map[string]any{"events": channel.Events})
	httpx.JSON(w, http.StatusCreated, map[string]any{"channel": channel, "secret": secret})
	return nil
}
func (s *Server) handleDeploymentNotificationUpdate(w http.ResponseWriter, r *http.Request) error {
	id, err := automationParam(r, "channel")
	if err != nil {
		return err
	}
	var req deploy.NotificationWrite
	if err = httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	channel, err := s.modules.deployAutomation.UpdateNotificationChannel(r.Context(), id, req)
	if err != nil {
		return mapAutomationError(err)
	}
	httpx.SetAudit(r, "deploy.notification.update", channel.Name, map[string]any{"events": channel.Events})
	httpx.JSON(w, http.StatusOK, channel)
	return nil
}
func (s *Server) handleDeploymentNotificationTest(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(chi.URLParam(r, "channel"), 10, 64)
	if err != nil || id <= 0 {
		return httpx.BadRequest("notification channel id must be positive")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	envelope := deploy.NotificationEnvelope{
		Event: deploy.NotificationEventTest, State: string(deploy.RunSucceeded), SentAt: time.Now().UTC(),
		ProjectName: "Just Dashboard", EnvironmentName: "test", Operation: string(deploy.OperationDeploy),
		Trigger: string(deploy.TriggerManual), Actor: httpx.MustPrincipal(r).Username(), URL: strings.TrimRight(s.dashboardEndpoint(), "/") + "/deploy",
	}
	err = s.modules.deployAutomation.DeliverNotification(ctx, nil, id, envelope)
	if err != nil {
		httpx.SetAudit(r, "deploy.notification.test", fmt.Sprint(id), map[string]any{"delivered": false})
		return httpx.Err(http.StatusBadGateway, "notification_failed", "notification delivery failed: "+err.Error())
	}
	httpx.SetAudit(r, "deploy.notification.test", fmt.Sprint(id), map[string]any{"delivered": true})
	httpx.JSON(w, http.StatusOK, map[string]any{"delivered": true})
	return nil
}

// handleDeploymentNotificationEnabled pauses or resumes a channel. It is its
// own route because the full update contract requires the channel's
// configuration, which the list view deliberately never has.
func (s *Server) handleDeploymentNotificationEnabled(w http.ResponseWriter, r *http.Request) error {
	id, err := automationParam(r, "channel")
	if err != nil {
		return err
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err = httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	channel, err := s.modules.deployAutomation.SetNotificationChannelEnabled(r.Context(), id, req.Enabled)
	if err != nil {
		return mapAutomationError(err)
	}
	httpx.SetAudit(r, "deploy.notification.update", channel.Name, map[string]any{"enabled": channel.Enabled})
	httpx.JSON(w, http.StatusOK, channel)
	return nil
}

// handleDeploymentNotificationOptions tells the form what it may offer, so the
// closed vocabularies live in one place.
func (s *Server) handleDeploymentNotificationOptions(w http.ResponseWriter, r *http.Request) error {
	httpx.JSON(w, http.StatusOK, map[string]any{
		"kinds": deploy.NotificationKinds, "events": deploy.NotificationEvents,
		"smtpSecurity": []string{deploy.SMTPSecurityStartTLS, deploy.SMTPSecurityTLS, deploy.SMTPSecurityNone},
	})
	return nil
}
func (s *Server) handleDeploymentNotificationDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := automationParam(r, "channel")
	if err != nil {
		return err
	}
	if err = s.modules.deployAutomation.DeleteNotificationChannel(r.Context(), id); err != nil {
		return mapAutomationError(err)
	}
	httpx.SetAudit(r, "deploy.notification.delete", fmt.Sprint(id), nil)
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleDeploymentProviderWebhook(w http.ResponseWriter, r *http.Request) error {
	return s.handleAutomationWebhook(w, r, true)
}

func (s *Server) handleDeploymentGenericWebhook(w http.ResponseWriter, r *http.Request) error {
	return s.handleAutomationWebhook(w, r, false)
}

func (s *Server) handleAutomationWebhook(w http.ResponseWriter, r *http.Request, providerSpecific bool) error {
	hookID := chi.URLParam(r, "hookID")
	httpx.SetAuditActor(r, "webhook")
	var trigger *deploy.Trigger
	var secret string
	var err error
	if rawTriggerID := chi.URLParam(r, "triggerID"); rawTriggerID != "" {
		projectID, parseErr := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		triggerID, triggerErr := strconv.ParseInt(rawTriggerID, 10, 64)
		if parseErr != nil || triggerErr != nil || projectID <= 0 || triggerID <= 0 {
			return httpx.Err(http.StatusUnauthorized, "bad_signature", "unknown or unauthorised deploy hook")
		}
		trigger, secret, err = s.modules.deployAutomation.TriggerByID(r.Context(), projectID, triggerID)
	} else {
		trigger, secret, err = s.modules.deployAutomation.TriggerByHook(r.Context(), hookID)
	}
	if err != nil {
		return httpx.Err(http.StatusUnauthorized, "bad_signature", "unknown or unauthorised deploy hook")
	}
	if !trigger.Enabled {
		return httpx.Err(http.StatusForbidden, "hook_disabled", "deployment hook is disabled")
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, deploy.MaxAutomationBody))
	if err != nil {
		return httpx.Err(http.StatusRequestEntityTooLarge, "payload_too_large", "webhook payload exceeds 4 MiB")
	}

	var event deploy.ProviderEvent
	if providerSpecific {
		provider := strings.ToLower(chi.URLParam(r, "provider"))
		if provider != trigger.Provider {
			err = deploy.ErrWrongEvent
		} else {
			event, err = deploy.VerifyProvider(provider, r.Header, body, secret)
		}
	} else {
		event = deploy.ProviderEvent{DeliveryID: strings.TrimSpace(r.Header.Get("Idempotency-Key")), Event: "deploy", Repository: trigger.Config.Repository, Ref: trigger.Config.Ref}
		if event.DeliveryID == "" {
			event.DeliveryID = fmt.Sprintf("body:%x", sha256.Sum256(body))
		}
		if !deploy.VerifyGenericHook(body, secret, r.Header.Get("X-JD-Signature-256")) {
			err = deploy.ErrBadHookSignature
		}
	}
	if err == nil {
		err = validateAutomationEvent(trigger, event)
	}
	if err == nil && event.PreviewNumber > 0 && !event.PreviewClosed && !deploy.MatchWatchPaths(event.ChangedPaths, trigger.Config.WatchInclude, trigger.Config.WatchExclude) {
		err = deploy.ErrWatchPathsIgnored
	}
	if err != nil {
		_ = s.modules.deployAutomation.RecordDelivery(r.Context(), trigger, event, body, "rejected", automationReason(err), 0)
		return mapAutomationError(err)
	}
	// Reserve the provider delivery before preview or queue side effects. This
	// unique row is the replay fence even when two identical requests arrive
	// concurrently.
	if err = s.modules.deployAutomation.RecordDelivery(r.Context(), trigger, event, body, "processing", "", 0); err != nil {
		return mapAutomationError(err)
	}
	var decision *deploy.GitDeploymentDecision
	policyKey := ""
	if event.PreviewNumber == 0 {
		policy, policyErr := s.modules.deployRuns.GitDeploymentPolicy(r.Context(), trigger.ProjectID, trigger.EnvironmentID)
		if policyErr == nil && !policy.Automatic {
			policyErr = deploy.ErrGitManualOnly
		}
		if policyErr == nil && policy.Conflict {
			policyErr = deploy.ErrGitPolicyConflict
		}
		if policyErr == nil {
			policyKey = policy.Key()
			decision, policyErr = s.modules.deployGit.EvaluateWebhook(r.Context(), trigger.ProjectID, trigger.EnvironmentID, event)
		}
		if policyErr == nil && decision == nil && !deploy.MatchWatchPaths(event.ChangedPaths, policy.WatchInclude, policy.WatchExclude) {
			policyErr = deploy.ErrWatchPathsIgnored
		}
		if policyErr != nil {
			_ = s.modules.deployAutomation.FinishDelivery(r.Context(), trigger, event.DeliveryID, "rejected", automationReason(policyErr), 0)
			return mapAutomationError(policyErr)
		}
		if decision != nil && !decision.Allowed {
			if err := s.modules.deployGit.RecordDecision(r.Context(), decision, nil); err != nil {
				return httpx.Internal(err)
			}
			if err := s.modules.deployAutomation.FinishDelivery(r.Context(), trigger, event.DeliveryID, "suppressed", decision.Reason, decision.RunID); err != nil {
				return httpx.Internal(err)
			}
			httpx.SetAudit(r, "deploy.provider.delivery", trigger.Name, map[string]any{"deliveryId": event.DeliveryID, "reason": decision.Reason, "revision": decision.Revision})
			httpx.JSON(w, http.StatusAccepted, map[string]any{"accepted": false, "reason": decision.Reason, "runId": decision.RunID})
			return nil
		}
	}
	project, err := s.modules.deployStore.Get(r.Context(), trigger.ProjectID)
	if err != nil {
		return mapDeployError(err)
	}
	environmentID, operation, runTrigger := trigger.EnvironmentID, deploy.OperationDeploy, trigger.Kind
	if trigger.Config.Preview && event.PreviewNumber > 0 {
		preview, created, previewErr := s.modules.deployAutomation.EnsurePreview(r.Context(), trigger, event)
		if previewErr != nil {
			if errors.Is(previewErr, deploy.ErrPreviewApproval) || (event.PreviewClosed && errors.Is(previewErr, deploy.ErrWrongEvent)) {
				status, reason := "pending", "preview_approval_required"
				if event.PreviewClosed {
					status, reason = "accepted", "preview_closed"
				}
				if err := s.modules.deployAutomation.FinishDelivery(r.Context(), trigger, event.DeliveryID, status, reason, 0); err != nil {
					return mapAutomationError(err)
				}
				httpx.SetAudit(r, "deploy.preview.review", trigger.Name, map[string]any{"deliveryId": event.DeliveryID, "revision": event.Revision, "state": status})
				httpx.JSON(w, http.StatusAccepted, map[string]any{"accepted": true, "approvalRequired": !event.PreviewClosed})
				return nil
			}
			_ = s.modules.deployAutomation.FinishDelivery(r.Context(), trigger, event.DeliveryID, "rejected", automationReason(previewErr), 0)
			return mapAutomationError(previewErr)
		}
		environmentID, runTrigger = preview.EnvironmentID, deploy.TriggerPreview
		if event.PreviewClosed {
			operation = deploy.OperationPreviewRemove
			if err := s.modules.deployRuns.CancelPreviewWork(r.Context(), environmentID); err != nil {
				return mapDeployError(err)
			}
		} else if created {
			operation = deploy.OperationPreviewCreate
		} else {
			operation = deploy.OperationPreviewUpdate
		}
	}
	sourceRevision, expectedPlanRevision := "", 0
	if runTrigger == deploy.TriggerPreview && !event.PreviewClosed {
		sourceRevision = event.Revision
	}
	if decision != nil {
		sourceRevision, expectedPlanRevision, policyKey = decision.Revision, decision.Target.PlanRevision, decision.Target.PolicyKey
	}
	run, err := s.enqueueNormalizedDeploymentAtSource(r.Context(), project, environmentID, operation, 0, runTrigger, "webhook", trigger.HookID+":"+event.DeliveryID, nil, sourceRevision, expectedPlanRevision, policyKey)
	if decision != nil {
		if run != nil {
			decision.RunID = run.ID
		}
		if recordErr := s.modules.deployGit.RecordDecision(r.Context(), decision, err); recordErr != nil && err == nil {
			err = recordErr
		}
	}
	if err != nil {
		_ = s.modules.deployAutomation.FinishDelivery(r.Context(), trigger, event.DeliveryID, "rejected", "enqueue_failed", 0)
		return mapDeployError(err)
	}
	reason := "hook_requested"
	if decision != nil {
		reason = decision.Reason
	}
	if err = s.modules.deployAutomation.FinishDelivery(r.Context(), trigger, event.DeliveryID, "accepted", reason, run.ID); err != nil {
		return mapAutomationError(err)
	}
	httpx.SetAudit(r, "deploy.provider.delivery", trigger.Name, map[string]any{"provider": trigger.Provider, "deliveryId": event.DeliveryID, "runId": run.ID})
	httpx.JSON(w, http.StatusAccepted, map[string]any{"accepted": true, "runId": run.ID, "state": run.State})
	return nil
}

func validateAutomationEvent(t *deploy.Trigger, e deploy.ProviderEvent) error {
	if e.PreviewNumber > 0 && !t.Config.Preview {
		return deploy.ErrWrongEvent
	}
	allowed := len(t.Config.Events) == 0
	for _, value := range t.Config.Events {
		if strings.EqualFold(value, e.Event) {
			allowed = true
		}
	}
	if !allowed {
		return deploy.ErrWrongEvent
	}
	if t.Config.Repository != "" && !strings.EqualFold(strings.TrimSuffix(t.Config.Repository, ".git"), strings.TrimSuffix(e.Repository, ".git")) {
		return deploy.ErrWrongRepository
	}
	if t.Config.Ref != "" && e.PreviewNumber == 0 {
		want, got := strings.TrimPrefix(t.Config.Ref, "refs/heads/"), strings.TrimPrefix(e.Ref, "refs/heads/")
		if want != got {
			return deploy.ErrWrongRef
		}
	}
	return nil
}

func automationReason(err error) string {
	switch {
	case errors.Is(err, deploy.ErrGitManualOnly):
		return "manual_only"
	case errors.Is(err, deploy.ErrGitPolicyConflict):
		return "policy_conflict"
	case errors.Is(err, deploy.ErrPreviewApproval):
		return "preview_approval_required"
	case errors.Is(err, deploy.ErrPreviewIsolation):
		return "preview_isolation_required"
	case errors.Is(err, deploy.ErrPreviewCleanupPending):
		return "preview_cleanup_pending"
	case errors.Is(err, deploy.ErrBadHookSignature):
		return "bad_signature"
	case errors.Is(err, deploy.ErrWrongEvent):
		return "wrong_event"
	case errors.Is(err, deploy.ErrWrongRepository):
		return "wrong_repository"
	case errors.Is(err, deploy.ErrWrongRef):
		return "wrong_ref"
	case errors.Is(err, deploy.ErrWatchPathsIgnored):
		return "watch_paths_ignored"
	case errors.Is(err, deploy.ErrDeliveryReplayed):
		return "delivery_replayed"
	case errors.Is(err, deploy.ErrPreviewQuota):
		return "preview_quota"
	}
	return "rejected"
}

func mapAutomationError(err error) error {
	if errors.Is(err, deploy.ErrInvalidNotification) {
		return httpx.BadRequest("%v", err)
	}
	if errors.Is(err, deploy.ErrTriggerNotFound) {
		return httpx.Err(http.StatusNotFound, "not_found", "automation resource not found")
	}
	code, status := automationReason(err), http.StatusUnprocessableEntity
	if errors.Is(err, deploy.ErrBadHookSignature) {
		status = http.StatusUnauthorized
	}
	if errors.Is(err, deploy.ErrDeliveryReplayed) {
		status = http.StatusConflict
	}
	if code != "rejected" {
		return httpx.Err(status, code, err.Error())
	}
	return httpx.BadRequest("%v", err)
}

func (s *Server) dispatchDeploymentSchedule(ctx context.Context, item deploy.ScheduleDispatch) error {
	project, err := s.modules.deployStore.Get(ctx, item.Schedule.ProjectID)
	if err != nil {
		return err
	}
	type chainEvidence struct {
		Ordinal    int    `json:"ordinal"`
		Action     string `json:"action"`
		Status     string `json:"status"`
		Code       string `json:"code,omitempty"`
		DurationMS int64  `json:"durationMs"`
	}
	evidence := make([]chainEvidence, 0, len(item.Schedule.Steps))
	chainFailed := false
	for ordinal, step := range item.Schedule.Steps {
		started := time.Now()
		status, code := "passed", ""
		stepCtx, cancel := context.WithTimeout(ctx, scheduleStepTimeout(step.Config))
		switch step.Action {
		case "deploy", "restart":
			operation := deploy.OperationDeploy
			if step.Action == "restart" {
				operation = deploy.OperationRestart
			}
			run, runErr := s.enqueueNormalizedDeploymentWithMetadata(stepCtx, project, item.Schedule.EnvironmentID, operation, 0, deploy.TriggerSchedule, fmt.Sprintf("scheduler:%d", item.Schedule.ID), fmt.Sprintf("schedule:%d:%d:%d", item.Schedule.ID, item.DueAt.Unix(), ordinal), map[string]any{"scheduleId": item.Schedule.ID, "scheduleName": item.Schedule.Name, "scheduleDueAt": item.DueAt, "chainOrdinal": ordinal, "chainAction": step.Action})
			if runErr == nil {
				runErr = s.waitForScheduledRun(stepCtx, run.ID)
			}
			if runErr != nil {
				status, code = "failed", "deployment_failed"
			}
		case "backup":
			var config struct {
				JobID int64 `json:"jobId"`
			}
			if json.Unmarshal(step.Config, &config) != nil || config.JobID <= 0 {
				status, code = "failed", "invalid_plan"
			} else if _, runErr := s.modules.backupRunner.Execute(stepCtx, config.JobID, "deployment_schedule"); runErr != nil {
				status, code = "failed", "backup_failed"
			}
		case "container_command":
			var config struct {
				ContainerID string   `json:"containerId"`
				Argv        []string `json:"argv"`
			}
			if json.Unmarshal(step.Config, &config) != nil || strings.TrimSpace(config.ContainerID) == "" || len(config.Argv) == 0 {
				status, code = "failed", "invalid_plan"
			} else if _, _, runErr := s.modules.docker.ExecCheck(stepCtx, config.ContainerID, config.Argv, scheduleStepTimeout(step.Config)); runErr != nil {
				status, code = "failed", "container_command_failed"
			}
		case "game_command":
			status, code = "unavailable", "game_console_unavailable"
		default:
			status, code = "failed", "invalid_plan"
		}
		cancel()
		evidence = append(evidence, chainEvidence{Ordinal: ordinal, Action: step.Action, Status: status, Code: code, DurationMS: time.Since(started).Milliseconds()})
		if status != "passed" && step.Required {
			chainFailed = true
			break
		}
	}
	_, markerErr := s.enqueueNormalizedDeploymentWithMetadata(ctx, project, item.Schedule.EnvironmentID, deploy.OperationScheduled, 0, deploy.TriggerSchedule, fmt.Sprintf("scheduler:%d", item.Schedule.ID), fmt.Sprintf("schedule:%d:%d:summary", item.Schedule.ID, item.DueAt.Unix()), map[string]any{"scheduleId": item.Schedule.ID, "scheduleName": item.Schedule.Name, "scheduleDueAt": item.DueAt, "scheduleMarker": true, "chainStatus": map[bool]string{true: "failed", false: "succeeded"}[chainFailed], "chain": evidence})
	return markerErr
}

func scheduleStepTimeout(raw json.RawMessage) time.Duration {
	var value struct {
		TimeoutSeconds int `json:"timeoutSeconds"`
	}
	_ = json.Unmarshal(raw, &value)
	if value.TimeoutSeconds <= 0 {
		value.TimeoutSeconds = 3600
	}
	if value.TimeoutSeconds > 43200 {
		value.TimeoutSeconds = 43200
	}
	return time.Duration(value.TimeoutSeconds) * time.Second
}
func (s *Server) waitForScheduledRun(ctx context.Context, runID int64) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		run, err := s.modules.deployRuns.Run(ctx, runID)
		if err != nil {
			return err
		}
		if run.State.Terminal() {
			// A rolled-back deployment kept the old release serving, but the
			// step the chain asked for did not happen; a following restart
			// would restart the release the operator meant to replace.
			if run.State == deploy.RunSucceeded {
				return nil
			}
			return fmt.Errorf("scheduled deployment ended in %s", run.State)
		}
		select {
		case <-ctx.Done():
			cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _ = s.modules.deployEngine.Cancel(cancelCtx, runID)
			cancel()
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
