package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/assistant"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// A failed run asked about, of the provider an administrator set up for the
// SQL editor's assistant (handlers_db_assistant.go). The deployment package
// decides what is sent and masks it (deploy/run_diagnosis.go); this is the
// route: who may ask, the install's budget of model calls, the one request,
// and the answer kept with the run so reopening it costs nothing.
//
// Nothing is sent until somebody presses for it, nothing the answer says is
// applied, and the audit entry names the run, the provider and the cost —
// never what was sent or answered.

// diagnosisTurn bounds one answer. A model on this machine reads a long
// transcript slowly, so it is longer than a diagram's.
const diagnosisTurn = 3 * time.Minute

// runDiagnosisView is a diagnosis with its model named as a person names it.
type runDiagnosisView struct {
	*deploy.RunDiagnosis
	ModelLabel string `json:"modelLabel"`
}

func diagnosisView(d *deploy.RunDiagnosis) *runDiagnosisView {
	if d == nil {
		return nil
	}
	return &runDiagnosisView{RunDiagnosis: d, ModelLabel: assistant.ModelLabel(assistant.Provider(d.Provider), d.Model)}
}

// diagnosableRun says whether a run ended in a failure worth asking about.
func diagnosableRun(state deploy.RunState) bool {
	return state == deploy.RunFailed || state == deploy.RunRolledBack
}

// handleDeploymentRunDiagnosis reads the diagnosis kept for a run, which any
// account that may read the run may read: it was written from what the run's
// page shows.
func (s *Server) handleDeploymentRunDiagnosis(w http.ResponseWriter, r *http.Request) error {
	projectID, runID, err := deploymentRunIDs(r)
	if err != nil {
		return err
	}
	run, err := s.modules.deployRuns.Run(r.Context(), runID)
	if err != nil {
		return mapDeployError(err)
	}
	if run.ProjectID != projectID {
		return mapDeployError(deploy.ErrRunNotFound)
	}
	diagnosis, err := s.modules.deployRuns.RunDiagnosis(r.Context(), runID)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"diagnosis": diagnosisView(diagnosis)})
	return nil
}

// handleDeploymentRunDiagnose asks the provider why a run failed, or answers
// the diagnosis already kept unless the request asks again.
func (s *Server) handleDeploymentRunDiagnose(w http.ResponseWriter, r *http.Request) error {
	projectID, runID, err := deploymentRunIDs(r)
	if err != nil {
		return err
	}
	var req struct {
		Again bool `json:"again"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	target := fmt.Sprint(runID)
	httpx.SetAudit(r, "deploy.run.diagnose", target, nil)
	ctx := r.Context()
	snapshot, err := s.modules.deployRuns.Snapshot(ctx, runID)
	if err != nil {
		return mapDeployError(err)
	}
	if snapshot.Run.ProjectID != projectID {
		return mapDeployError(deploy.ErrRunNotFound)
	}
	if !diagnosableRun(snapshot.Run.State) {
		return httpx.Err(http.StatusConflict, "run_not_failed", "only a run that failed or rolled back is diagnosed")
	}
	if !req.Again {
		kept, err := s.modules.deployRuns.RunDiagnosis(ctx, runID)
		if err != nil {
			return httpx.Internal(err)
		}
		if kept != nil {
			httpx.SetAudit(r, "deploy.run.diagnose", target, map[string]any{"outcome": "kept", "provider": kept.Provider, "model": kept.Model})
			httpx.JSON(w, http.StatusOK, map[string]any{"diagnosis": diagnosisView(kept)})
			return nil
		}
	}
	settings, err := s.assistantSettings(ctx)
	if err != nil {
		return err
	}
	if settings == nil {
		return httpx.Err(http.StatusConflict, "assistant_not_configured",
			"the assistant is not set up: an administrator chooses who answers it in the assistant's settings")
	}
	if _, busy := s.assistant.diagnosing.LoadOrStore(runID, struct{}{}); busy {
		return httpx.Err(http.StatusConflict, "diagnosis_running", "this run is being diagnosed already; its answer appears here when it arrives")
	}
	defer s.assistant.diagnosing.Delete(runID)
	if !s.assistant.limit.Allow("install") {
		return httpx.Err(http.StatusTooManyRequests, "rate_limited",
			"the assistant has answered as many questions as this install allows in a minute; ask again shortly")
	}
	select {
	case s.assistant.slots <- struct{}{}:
		defer func() { <-s.assistant.slots }()
	default:
		return httpx.Err(http.StatusTooManyRequests, "assistant_busy",
			"the assistant is answering several questions already; ask again when one of them has finished")
	}

	material := deploy.DiagnosisMaterial{Snapshot: *snapshot}
	// An older run's settings may not be readable; the digest says so and
	// the transcript still goes.
	if plan, err := s.modules.deployRuns.ExecutionPlan(ctx, snapshot.Run); err == nil {
		material.Plan = plan
	}
	material.Transcript, material.Earlier, err = s.modules.deployRuns.RunTranscriptTail(ctx, runID, 5*deploy.DiagnosisTranscriptLines)
	if err != nil {
		return mapDeployError(err)
	}
	secrets, err := s.modules.deployPlanning.DiagnosisSecrets(ctx, snapshot.Run)
	if err != nil {
		httpx.SetAudit(r, "deploy.run.diagnose", target, map[string]any{"outcome": "unmasked"})
		return httpx.Err(http.StatusConflict, "diagnosis_unmasked",
			"the run's secret values could not be read to mask them, so nothing was sent: "+err.Error())
	}
	digest := deploy.BuildDiagnosisDigest(material, deploy.NewDiagnosisMasker(secrets))

	client, err := assistant.New(*settings, s.assistant.options)
	if err != nil {
		return httpx.Internal(err)
	}
	turn, cancel := context.WithTimeout(ctx, diagnosisTurn)
	defer cancel()
	reply, err := client.Stream(turn, assistant.Request{
		System:   deploy.DiagnosisSystemPrompt,
		Messages: []assistant.Message{{Role: "user", Blocks: []assistant.Block{{Type: assistant.BlockText, Text: digest.Text}}}},
	}, func(assistant.Event) {})
	detail := map[string]any{
		"provider": settings.Provider, "model": settings.Model, "usage": reply.Usage,
		"lines": len(digest.Lines), "masked": digest.Masked, "bytes": len(digest.Text),
	}
	if err != nil {
		detail["outcome"] = "failed"
		httpx.SetAudit(r, "deploy.run.diagnose", target, detail)
		return diagnosisFailure(ctx, turn, err)
	}
	text := replyText(reply)
	if reply.Stop == assistant.StopRefusal || strings.TrimSpace(text) == "" {
		detail["outcome"] = "unusable"
		httpx.SetAudit(r, "deploy.run.diagnose", target, detail)
		return httpx.Err(http.StatusBadGateway, "assistant_failed",
			"the assistant gave no answer about this run; ask again, or read the build logs below")
	}
	answer := deploy.ReadDiagnosisAnswer(text, digest.Lines)
	usage, _ := json.Marshal(reply.Usage)
	model := reply.Model
	if model == "" {
		model = settings.Model
	}
	diagnosis := deploy.RunDiagnosis{
		RunID: runID, Provider: string(settings.Provider), Model: model, Answer: answer,
		Sent: digest.Text, SentLines: len(digest.Lines), Masked: digest.Masked, Usage: usage,
		CreatedBy: httpx.MustPrincipal(r).Username(), CreatedAt: time.Now().UTC(),
	}
	if err := s.modules.deployRuns.SaveRunDiagnosis(ctx, diagnosis); err != nil {
		return httpx.Internal(err)
	}
	detail["outcome"] = "answered"
	detail["evidence"] = len(answer.Evidence)
	detail["unmatched"] = answer.Unmatched
	httpx.SetAudit(r, "deploy.run.diagnose", target, detail)
	httpx.JSON(w, http.StatusOK, map[string]any{"diagnosis": diagnosisView(&diagnosis)})
	return nil
}

// replyText is the answer's text, every text block in order.
func replyText(reply assistant.Reply) string {
	var text strings.Builder
	for _, b := range reply.Blocks {
		if b.Type == assistant.BlockText {
			text.WriteString(b.Text)
		}
	}
	return text.String()
}

func diagnosisFailure(request, turn context.Context, err error) error {
	switch {
	case request.Err() != nil:
		return httpx.Err(http.StatusBadGateway, "assistant_failed", "the request was stopped")
	case errors.Is(turn.Err(), context.DeadlineExceeded):
		return httpx.Err(http.StatusBadGateway, "assistant_failed", "the assistant took longer than three minutes and was stopped").Retry()
	}
	apiErr := httpx.Err(http.StatusBadGateway, "assistant_failed", err.Error())
	if pe, ok := assistant.IsProviderError(err); ok {
		apiErr.Raw, apiErr.Retryable = pe.Raw, pe.Retryable
	}
	return apiErr
}
