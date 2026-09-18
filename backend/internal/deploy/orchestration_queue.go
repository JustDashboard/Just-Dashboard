package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type QueueBudget struct {
	Heavy int
	Light int
}

func (b QueueBudget) normalized() QueueBudget {
	if b.Heavy <= 0 {
		b.Heavy = 1
	}
	if b.Light <= 0 {
		b.Light = 2
	}
	return b
}

func (s *OrchestrationStore) ClaimNext(
	ctx context.Context,
	worker string,
	budget QueueBudget,
	ttl time.Duration,
) (*QueueLease, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var lastErr error
	for attempt := 0; attempt < 8; attempt++ {
		lease, err := s.claimNextOnce(ctx, worker, budget, ttl)
		if err == nil || !retryableClaimError(err) {
			return lease, err
		}
		lastErr = err
		timer := time.NewTimer(time.Duration(attempt+1) * 2 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func (s *OrchestrationStore) claimNextOnce(
	ctx context.Context,
	worker string,
	budget QueueBudget,
	ttl time.Duration,
) (*QueueLease, error) {
	if worker == "" {
		return nil, fmt.Errorf("deployment worker identity is required")
	}
	budget = budget.normalized()
	if ttl <= 0 {
		ttl = defaultLeaseTTL
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.now().UTC()

	var heavy, light int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(CASE WHEN slot_class = 'heavy' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN slot_class = 'light' THEN 1 ELSE 0 END), 0)
		  FROM deploy_queue_leases`).Scan(&heavy, &light); err != nil {
		return nil, err
	}
	classes := make([]SlotClass, 0, 2)
	if heavy < budget.Heavy {
		classes = append(classes, SlotHeavy)
	}
	if light < budget.Light {
		classes = append(classes, SlotLight)
	}
	if len(classes) == 0 {
		return nil, tx.Commit()
	}

	query := `
		SELECT r.id, r.environment_id, r.slot_class
		  FROM deploy_runs r
		 WHERE r.state = ? AND r.cancel_requested = 0
		   AND NOT EXISTS (
		     SELECT 1 FROM deploy_queue_leases l WHERE l.environment_id = r.environment_id
		   ) AND (`
	args := []any{RunQueued}
	for i, class := range classes {
		if i > 0 {
			query += " OR "
		}
		query += "r.slot_class = ?"
		args = append(args, class)
	}
	query += `) ORDER BY r.priority DESC, r.requested_at, r.id LIMIT 1`
	var runID, environmentID int64
	var class SlotClass
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&runID, &environmentID, &class); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, tx.Commit()
		}
		return nil, err
	}
	token, err := newClaimToken()
	if err != nil {
		return nil, err
	}
	expires := now.Add(ttl)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_queue_leases(
		  run_id, environment_id, claim_token, slot_class, claimed_by,
		  expires_at, heartbeat_at, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		runID, environmentID, token, class, worker, expires.Unix(), now.Unix(), now.Unix()); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE deploy_runs
		   SET state = ?, claimed_at = ?, heartbeat_at = ?, lease_token = ?, lease_until = ?
		 WHERE id = ? AND state = ? AND cancel_requested = 0`,
		RunPreparing, now.Unix(), now.Unix(), token, expires.Unix(), runID, RunQueued)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, ErrLeaseLost
	}
	event, err := appendEventTx(ctx, tx, now, runID, 0, EventRunState, "status", "",
		mustJSON(map[string]any{"state": RunPreparing, "worker": worker}))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.publish([]RunEvent{event})
	return &QueueLease{
		RunID: runID, EnvironmentID: environmentID, Token: token, SlotClass: class,
		ClaimedBy: worker, ClaimedAt: now, HeartbeatAt: now, ExpiresAt: expires,
	}, nil
}

func retryableClaimError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") ||
		strings.Contains(message, "sqlite_busy") ||
		strings.Contains(message, "deploy_queue_leases") && strings.Contains(message, "unique")
}

func (s *OrchestrationStore) Heartbeat(
	ctx context.Context,
	runID int64,
	claimToken string,
	ttl time.Duration,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if ttl <= 0 {
		ttl = defaultLeaseTTL
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	expires := now.Add(ttl)
	result, err := tx.ExecContext(ctx, `
		UPDATE deploy_queue_leases
		   SET heartbeat_at = ?, expires_at = ?
		 WHERE run_id = ? AND claim_token = ? AND expires_at >= ?`,
		now.Unix(), expires.Unix(), runID, claimToken, now.Unix())
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrLeaseLost
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE deploy_runs SET heartbeat_at = ?, lease_until = ?
		 WHERE id = ? AND lease_token = ?`,
		now.Unix(), expires.Unix(), runID, claimToken); err != nil {
		return err
	}
	return tx.Commit()
}

func assertLeaseTx(ctx context.Context, tx *sql.Tx, runID int64, claimToken string, now time.Time) error {
	if claimToken == "" {
		return ErrLeaseLost
	}
	var expires int64
	err := tx.QueryRowContext(ctx, `
		SELECT expires_at FROM deploy_queue_leases
		 WHERE run_id = ? AND claim_token = ?`, runID, claimToken).Scan(&expires)
	if errors.Is(err, sql.ErrNoRows) || expires < now.Unix() {
		return ErrLeaseLost
	}
	return err
}

func (s *OrchestrationStore) SetRunCommits(
	ctx context.Context,
	runID int64,
	claimToken, fromCommit, toCommit string,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := assertLeaseTx(ctx, tx, runID, claimToken, s.now().UTC()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE deploy_runs
		   SET from_commit = CASE WHEN ? <> '' THEN ? ELSE from_commit END,
		       to_commit = CASE WHEN ? <> '' THEN ? ELSE to_commit END
		 WHERE id = ? AND lease_token = ?`,
		fromCommit, fromCommit, toCommit, toCommit, runID, claimToken); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *OrchestrationStore) TransitionRun(
	ctx context.Context,
	runID int64,
	claimToken string,
	to RunState,
	detail TransitionDetail,
) (*EngineRun, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	if err := assertLeaseTx(ctx, tx, runID, claimToken, now); err != nil {
		return nil, err
	}
	var from RunState
	if err := tx.QueryRowContext(ctx, `SELECT state FROM deploy_runs WHERE id = ?`, runID).Scan(&from); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRunNotFound
		}
		return nil, err
	}
	if from.Terminal() {
		return nil, ErrRunTerminal
	}
	if err := ValidateRunTransition(from, to); err != nil {
		return nil, err
	}
	ended := int64(0)
	if to.Terminal() {
		ended = now.Unix()
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE deploy_runs
		   SET state = ?, status = ?, ended_at = ?, terminal_code = ?, terminal_reason = ?,
		       lease_token = CASE WHEN ? <> 0 THEN '' ELSE lease_token END,
		       lease_until = CASE WHEN ? <> 0 THEN 0 ELSE lease_until END
		 WHERE id = ? AND state = ? AND lease_token = ?`,
		to, legacyStatus(to), ended, detail.Code, detail.Reason,
		ended, ended, runID, from, claimToken)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, ErrLeaseLost
	}
	if _, failing := candidateFailureStates[to]; failing {
		if _, err := tx.ExecContext(ctx, `
			UPDATE deploy_releases SET state = 'failed'
			 WHERE state = 'candidate' AND id = (SELECT candidate_release_id FROM deploy_runs WHERE id = ?)`,
			runID); err != nil {
			return nil, err
		}
	}
	event, err := appendEventTx(ctx, tx, now, runID, 0, EventRunState, "status", "",
		mustJSON(map[string]any{"from": from, "state": to, "code": detail.Code, "reason": detail.Reason}))
	if err != nil {
		return nil, err
	}
	if to.Terminal() {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM deploy_queue_leases WHERE run_id = ? AND claim_token = ?`, runID, claimToken); err != nil {
			return nil, err
		}
	}
	run, err := scanEngineRun(tx.QueryRowContext(ctx,
		`SELECT `+engineRunColumns+` FROM deploy_runs WHERE id = ?`, runID))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.publish([]RunEvent{event})
	if to.Terminal() {
		s.broker.closeRun(runID)
	}
	return run, nil
}

func (s *OrchestrationStore) RequestCancellation(ctx context.Context, runID int64) (*EngineRun, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	var from RunState
	if err := tx.QueryRowContext(ctx, `SELECT state FROM deploy_runs WHERE id = ?`, runID).Scan(&from); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRunNotFound
		}
		return nil, err
	}
	if from.Terminal() {
		return nil, ErrRunTerminal
	}
	if !CanTransitionRun(from, RunCancelling) {
		return nil, ErrRunNotCancellable
	}
	events := make([]RunEvent, 0, 2)
	if _, err := tx.ExecContext(ctx, `
		UPDATE deploy_runs SET state = ?, cancel_requested = 1 WHERE id = ? AND state = ?`,
		RunCancelling, runID, from); err != nil {
		return nil, err
	}
	event, err := appendEventTx(ctx, tx, now, runID, 0, EventRunState, "status", "",
		mustJSON(map[string]any{"from": from, "state": RunCancelling}))
	if err != nil {
		return nil, err
	}
	events = append(events, event)
	// No process owns a queued/requested run, so cancellation is complete in
	// this transaction. Claimed work retains its lease for bounded cleanup.
	if from == RunQueued || from == RunRequested || from == RunValidating {
		if _, err := tx.ExecContext(ctx, `
			UPDATE deploy_runs
			   SET state = ?, status = ?, ended_at = ?, lease_token = '', lease_until = 0
			 WHERE id = ? AND state = ?`,
			RunCancelled, legacyStatus(RunCancelled), now.Unix(), runID, RunCancelling); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM deploy_queue_leases WHERE run_id = ?`, runID); err != nil {
			return nil, err
		}
		event, err = appendEventTx(ctx, tx, now, runID, 0, EventRunState, "status", "",
			mustJSON(map[string]any{"from": RunCancelling, "state": RunCancelled}))
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	run, err := scanEngineRun(tx.QueryRowContext(ctx,
		`SELECT `+engineRunColumns+` FROM deploy_runs WHERE id = ?`, runID))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.publish(events)
	if run.State.Terminal() {
		s.broker.closeRun(runID)
	}
	return run, nil
}

func (s *OrchestrationStore) ActiveLease(ctx context.Context, runID int64) (*QueueLease, error) {
	var lease QueueLease
	var claimed, heartbeat, expires int64
	err := s.db.QueryRowContext(ctx, `
		SELECT run_id, environment_id, claim_token, slot_class, claimed_by,
		       created_at, heartbeat_at, expires_at
		  FROM deploy_queue_leases WHERE run_id = ?`, runID).Scan(
		&lease.RunID, &lease.EnvironmentID, &lease.Token, &lease.SlotClass, &lease.ClaimedBy,
		&claimed, &heartbeat, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLeaseLost
	}
	if err != nil {
		return nil, err
	}
	lease.ClaimedAt = unixTime(claimed)
	lease.HeartbeatAt = unixTime(heartbeat)
	lease.ExpiresAt = unixTime(expires)
	return &lease, nil
}

func (s *OrchestrationStore) ExpiredLeases(ctx context.Context) ([]QueueLease, error) {
	now := s.now().UTC().Unix()
	rows, err := s.db.QueryContext(ctx, `
		SELECT run_id, environment_id, claim_token, slot_class, claimed_by,
		       created_at, heartbeat_at, expires_at
		  FROM deploy_queue_leases WHERE expires_at < ? ORDER BY expires_at, run_id`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []QueueLease
	for rows.Next() {
		var lease QueueLease
		var claimed, heartbeat, expires int64
		if err := rows.Scan(&lease.RunID, &lease.EnvironmentID, &lease.Token,
			&lease.SlotClass, &lease.ClaimedBy, &claimed, &heartbeat, &expires); err != nil {
			return nil, err
		}
		lease.ClaimedAt = unixTime(claimed)
		lease.HeartbeatAt = unixTime(heartbeat)
		lease.ExpiresAt = unixTime(expires)
		result = append(result, lease)
	}
	return result, rows.Err()
}

// ReclaimExpiredLease fences the old process with a new random token before
// reconciliation reads or mutates step evidence. It does not create a second
// host slot: the existing lease row remains the accounting record.
func (s *OrchestrationStore) ReclaimExpiredLease(
	ctx context.Context,
	expired QueueLease,
	worker string,
	ttl time.Duration,
) (*QueueLease, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if worker == "" {
		return nil, fmt.Errorf("deployment worker identity is required")
	}
	if ttl <= 0 {
		ttl = defaultLeaseTTL
	}
	token, err := newClaimToken()
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	expires := now.Add(ttl)
	result, err := tx.ExecContext(ctx, `
		UPDATE deploy_queue_leases
		   SET claim_token = ?, claimed_by = ?, heartbeat_at = ?, expires_at = ?
		 WHERE run_id = ? AND claim_token = ? AND expires_at < ?`,
		token, worker, now.Unix(), expires.Unix(), expired.RunID, expired.Token, now.Unix())
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, ErrLeaseLost
	}
	runResult, err := tx.ExecContext(ctx, `
		UPDATE deploy_runs
		   SET lease_token = ?, heartbeat_at = ?, lease_until = ?
		 WHERE id = ? AND lease_token = ?`,
		token, now.Unix(), expires.Unix(), expired.RunID, expired.Token)
	if err != nil {
		return nil, err
	}
	if affected, _ := runResult.RowsAffected(); affected != 1 {
		return nil, ErrLeaseLost
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &QueueLease{
		RunID: expired.RunID, EnvironmentID: expired.EnvironmentID,
		Token: token, SlotClass: expired.SlotClass, ClaimedBy: worker,
		ClaimedAt: expired.ClaimedAt, HeartbeatAt: now, ExpiresAt: expires,
	}, nil
}

func (s *OrchestrationStore) supersedeEligibleTx(
	ctx context.Context,
	tx *sql.Tx,
	req RunRequest,
	newRunID int64,
	environmentKind string,
	now time.Time,
	events *[]RunEvent,
) error {
	if req.Trigger == TriggerManual || req.Trigger == TriggerRollback ||
		req.Operation == OperationRollback || req.Operation == OperationRemoveManaged {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, operation, trigger, metadata_json
		  FROM deploy_runs
		 WHERE environment_id = ? AND state = ? AND id <> ?
		   AND NOT EXISTS (SELECT 1 FROM deploy_queue_leases l WHERE l.run_id = deploy_runs.id)
		 ORDER BY requested_at, id`, req.EnvironmentID, RunQueued, newRunID)
	if err != nil {
		return err
	}
	type candidate struct {
		id        int64
		operation Operation
		trigger   TriggerKind
		metadata  string
	}
	var candidates []candidate
	for rows.Next() {
		var candidate candidate
		if err := rows.Scan(&candidate.id, &candidate.operation, &candidate.trigger, &candidate.metadata); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	newPaths := changedPaths(req.Metadata)
	for _, candidate := range candidates {
		if candidate.trigger == TriggerManual || candidate.trigger == TriggerRollback ||
			candidate.operation == OperationRollback || candidate.operation == OperationRemoveManaged {
			continue
		}
		if environmentKind == "production" &&
			(!automaticTrigger(req.Trigger) || !automaticTrigger(candidate.trigger)) {
			continue
		}
		if !coversPaths(newPaths, changedPaths(json.RawMessage(candidate.metadata))) {
			continue
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE deploy_runs
			   SET state = ?, status = ?, ended_at = ?, superseded_by = ?,
			       terminal_code = 'superseded', terminal_reason = 'Replaced by a newer queued run'
			 WHERE id = ? AND state = ?`,
			RunSuperseded, legacyStatus(RunSuperseded), now.Unix(), newRunID,
			candidate.id, RunQueued)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			continue
		}
		event, err := appendEventTx(ctx, tx, now, candidate.id, 0, EventRunState,
			"status", "", mustJSON(map[string]any{
				"state": RunSuperseded, "supersededBy": newRunID,
			}))
		if err != nil {
			return err
		}
		*events = append(*events, event)
	}
	return nil
}

func automaticTrigger(trigger TriggerKind) bool {
	switch trigger {
	case TriggerLegacyHook, TriggerGenericHook, TriggerGitHub, TriggerGitLab,
		TriggerBitbucket, TriggerGitea, TriggerAPI, TriggerSchedule, TriggerPreview:
		return true
	default:
		return false
	}
}

func changedPaths(raw json.RawMessage) []string {
	var metadata struct {
		ChangedPaths []string `json:"changedPaths"`
	}
	_ = json.Unmarshal(raw, &metadata)
	return metadata.ChangedPaths
}

func coversPaths(newer, older []string) bool {
	set := make(map[string]struct{}, len(newer))
	for _, path := range newer {
		set[path] = struct{}{}
	}
	for _, path := range older {
		if _, ok := set[path]; !ok {
			return false
		}
	}
	return true
}
