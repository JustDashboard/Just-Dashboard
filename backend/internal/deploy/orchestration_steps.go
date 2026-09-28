package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

const stepColumns = `
id, run_id, step_key, ordinal, status, attempt, timeout_seconds,
started_at, ended_at, evidence_json, error_code, error_message,
cleanup_json, last_seq`

func scanRunStep(row scanner) (*RunStep, error) {
	var step RunStep
	var started, ended int64
	var evidence, cleanup string
	if err := row.Scan(
		&step.ID, &step.RunID, &step.Key, &step.Ordinal, &step.State,
		&step.Attempt, &step.TimeoutSeconds, &started, &ended, &evidence,
		&step.ErrorCode, &step.ErrorMessage, &cleanup, &step.LastSeq,
	); err != nil {
		return nil, err
	}
	step.StartedAt = unixTimePtr(started)
	step.EndedAt = unixTimePtr(ended)
	step.Evidence = validRawJSON(evidence)
	step.Cleanup = validRawJSON(cleanup)
	if step.ErrorCode == "health_gate_failed" || outputCauseCode(step.ErrorCode) {
		var health checkStepEvidence
		var wrapped struct {
			Health      checkStepEvidence           `json:"health"`
			Diagnostics *runtimeDiagnosticsEvidence `json:"diagnostics"`
		}
		_ = json.Unmarshal(step.Evidence, &health)
		if len(health.Checks) == 0 && json.Unmarshal(step.Evidence, &wrapped) == nil {
			health = wrapped.Health
		}
		if len(health.Checks) > 0 {
			step.ErrorMessage = checkFailureMessage(health.Phase, health.Outcome, health.Checks...) +
				diagnosticsSuffix(wrapped.Diagnostics)
		}
	}
	return &step, nil
}

func validRawJSON(value string) json.RawMessage {
	raw := json.RawMessage(value)
	if !json.Valid(raw) {
		return json.RawMessage(`{}`)
	}
	return raw
}

func (s *OrchestrationStore) Steps(ctx context.Context, runID int64) ([]RunStep, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+stepColumns+` FROM deploy_steps
		 WHERE run_id = ? ORDER BY ordinal, attempt`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []RunStep
	for rows.Next() {
		step, err := scanRunStep(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *step)
	}
	return result, rows.Err()
}

func (s *OrchestrationStore) Snapshot(ctx context.Context, runID int64) (*RunSnapshot, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	run, err := scanEngineRun(tx.QueryRowContext(ctx,
		`SELECT `+engineRunColumns+` FROM deploy_runs WHERE id = ?`, runID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRunNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT `+stepColumns+` FROM deploy_steps
		 WHERE run_id = ? ORDER BY ordinal, attempt`, runID)
	if err != nil {
		return nil, err
	}
	var steps []RunStep
	for rows.Next() {
		step, err := scanRunStep(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		steps = append(steps, *step)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &RunSnapshot{Run: *run, Steps: steps}, nil
}

type StepTransition struct {
	State        StepState
	Retry        bool
	Evidence     json.RawMessage
	Cleanup      json.RawMessage
	ErrorCode    string
	ErrorMessage string
}

func (s *OrchestrationStore) TransitionStep(
	ctx context.Context,
	runID int64,
	key StepKey,
	claimToken string,
	input StepTransition,
) (*RunStep, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if !validStepKey(key) {
		return nil, fmt.Errorf("invalid deployment step %q", key)
	}
	if len(input.Evidence) > maxEventBytes || len(input.Cleanup) > maxEventBytes {
		return nil, ErrEventPayloadTooLarge
	}
	for _, raw := range []json.RawMessage{input.Evidence, input.Cleanup} {
		if len(raw) != 0 && !json.Valid(raw) {
			return nil, fmt.Errorf("deployment step evidence is not valid JSON")
		}
	}
	if len(input.ErrorMessage) > 8192 || len(input.ErrorCode) > 128 {
		return nil, fmt.Errorf("deployment step error is too large")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	if err := assertLeaseTx(ctx, tx, runID, claimToken, now); err != nil {
		return nil, err
	}
	current, err := scanRunStep(tx.QueryRowContext(ctx, `
		SELECT `+stepColumns+` FROM deploy_steps
		 WHERE run_id = ? AND step_key = ? ORDER BY attempt DESC LIMIT 1`, runID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrStepNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := ValidateStepTransition(current.State, input.State); err != nil {
		return nil, err
	}

	var updated *RunStep
	if current.State == StepFailed && input.State == StepPending {
		if !input.Retry {
			return nil, fmt.Errorf("%w: failed step retry must increment attempt", ErrInvalidTransition)
		}
		result, err := tx.ExecContext(ctx, `
			INSERT INTO deploy_steps(
			  run_id, step_key, ordinal, status, attempt, timeout_seconds)
			VALUES(?, ?, ?, ?, ?, ?)`,
			runID, key, current.Ordinal, StepPending, current.Attempt+1, current.TimeoutSeconds)
		if err != nil {
			return nil, err
		}
		stepID, err := result.LastInsertId()
		if err != nil {
			return nil, err
		}
		updated, err = scanRunStep(tx.QueryRowContext(ctx,
			`SELECT `+stepColumns+` FROM deploy_steps WHERE id = ?`, stepID))
		if err != nil {
			return nil, err
		}
	} else {
		started := int64(0)
		if input.State == StepRunning && current.StartedAt == nil {
			started = now.Unix()
		}
		ended := int64(0)
		if stepAttemptTerminal(input.State) {
			ended = now.Unix()
		}
		evidence := string(current.Evidence)
		if len(input.Evidence) != 0 {
			evidence = string(input.Evidence)
		}
		cleanup := string(current.Cleanup)
		if len(input.Cleanup) != 0 {
			cleanup = string(input.Cleanup)
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE deploy_steps
			   SET status = ?,
			       started_at = CASE WHEN ? <> 0 THEN ? ELSE started_at END,
			       ended_at = CASE WHEN ? <> 0 THEN ? ELSE ended_at END,
			       evidence_json = ?, error_code = ?, error_message = ?, cleanup_json = ?
			 WHERE id = ? AND status = ?`,
			input.State, started, started, ended, ended, evidence,
			input.ErrorCode, input.ErrorMessage, cleanup, current.ID, current.State)
		if err != nil {
			return nil, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return nil, fmt.Errorf("%w: deployment step changed concurrently", ErrInvalidTransition)
		}
		updated, err = scanRunStep(tx.QueryRowContext(ctx,
			`SELECT `+stepColumns+` FROM deploy_steps WHERE id = ?`, current.ID))
		if err != nil {
			return nil, err
		}
	}

	event, err := appendEventTx(ctx, tx, now, runID, updated.ID, EventStepState,
		"status", "", mustJSON(map[string]any{
			"key": updated.Key, "state": updated.State, "attempt": updated.Attempt,
			"evidence": updated.Evidence, "errorCode": updated.ErrorCode,
			"errorMessage": updated.ErrorMessage,
		}))
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE deploy_steps SET last_seq = ? WHERE id = ?`, event.Seq, updated.ID); err != nil {
		return nil, err
	}
	updated.LastSeq = event.Seq
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.publish([]RunEvent{event})
	return updated, nil
}

func stepAttemptTerminal(state StepState) bool {
	switch state {
	case StepPassed, StepWarning, StepFailed, StepSkipped, StepCancelled, StepUnavailable:
		return true
	default:
		return false
	}
}

// CancelOpenSteps records cleanup of every attempt that never reached a
// terminal state. It uses ordinary transition rules and therefore emits the
// same sequenced evidence as an individually cancelled step.
func (s *OrchestrationStore) CancelOpenSteps(
	ctx context.Context,
	runID int64,
	claimToken string,
) error {
	steps, err := s.Steps(ctx, runID)
	if err != nil {
		return err
	}
	latest := make(map[StepKey]RunStep)
	for _, step := range steps {
		if existing, ok := latest[step.Key]; !ok || step.Attempt > existing.Attempt {
			latest[step.Key] = step
		}
	}
	for _, key := range DefaultStepKeys {
		step, ok := latest[key]
		if !ok || stepAttemptTerminal(step.State) {
			continue
		}
		if !CanTransitionStep(step.State, StepCancelled) {
			continue
		}
		if _, err := s.TransitionStep(ctx, runID, key, claimToken, StepTransition{State: StepCancelled}); err != nil {
			return err
		}
	}
	return nil
}
