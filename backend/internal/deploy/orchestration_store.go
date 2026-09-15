package deploy

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	basestore "github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

var (
	ErrEnvironmentNotFound  = errors.New("deployment environment not found")
	ErrRunNotFound          = errors.New("deployment run not found")
	ErrStepNotFound         = errors.New("deployment step not found")
	ErrIdempotencyConflict  = errors.New("deployment idempotency key conflicts with another request")
	ErrLeaseLost            = errors.New("deployment queue lease was lost")
	ErrRunTerminal          = errors.New("deployment run is terminal")
	ErrRunNotCancellable    = errors.New("deployment run is not cancellable")
	ErrRunNotRetryable      = errors.New("deployment run is not retryable")
	ErrEventPayloadTooLarge = errors.New("deployment event payload is too large")
)

const (
	maxLogTextBytes = 64 << 10
	maxEventBytes   = 256 << 10
	defaultPriority = 100
	defaultLeaseTTL = 30 * time.Second
)

// OrchestrationStore owns atomic deployment run state. It is separate from
// Store while the 0.6.6 response shape remains supported; both address the
// same stable deploy_projects and deploy_runs identities.
type OrchestrationStore struct {
	db     *sql.DB
	now    func() time.Time
	broker *eventBroker
	// SQLite permits concurrent readers but one writer. Serializing this
	// process's short orchestration transactions prevents a cancellation from
	// racing a heartbeat/state transition through a deferred read-to-write
	// upgrade and leaking SQLITE_BUSY_SNAPSHOT instead of a fenced domain
	// result. Database constraints and claim tokens remain the cross-process
	// authority.
	writeMu sync.Mutex
}

func NewOrchestrationStore(st *basestore.Store) *OrchestrationStore {
	return &OrchestrationStore{db: st.DB, now: time.Now, broker: newEventBroker()}
}

const engineRunColumns = `
id, project_id, environment_id, state, operation, trigger, actor,
requested_at, queued_at, claimed_at, heartbeat_at, ended_at,
cancel_requested, superseded_by, retry_of_run_id, idempotency_key,
request_digest, plan_revision, release_id, candidate_release_id,
terminal_code, terminal_reason, lease_until, priority, slot_class,
metadata_json`

type scanner interface{ Scan(...any) error }

func scanEngineRun(row scanner) (*EngineRun, error) {
	var (
		r                                            EngineRun
		requested, queued, claimed, heartbeat, ended int64
		leaseUntil                                   int64
		cancelRequested                              int
		metadata                                     string
	)
	if err := row.Scan(
		&r.ID, &r.ProjectID, &r.EnvironmentID, &r.State, &r.Operation, &r.Trigger,
		&r.Actor, &requested, &queued, &claimed, &heartbeat, &ended,
		&cancelRequested, &r.SupersededBy, &r.RetryOfRunID, &r.IdempotencyKey,
		&r.RequestDigest, &r.PlanRevision, &r.ReleaseID, &r.CandidateReleaseID,
		&r.TerminalCode, &r.TerminalReason, &leaseUntil, &r.Priority, &r.SlotClass,
		&metadata,
	); err != nil {
		return nil, err
	}
	r.RequestedAt = unixTime(requested)
	r.QueuedAt = unixTimePtr(queued)
	r.ClaimedAt = unixTimePtr(claimed)
	r.HeartbeatAt = unixTimePtr(heartbeat)
	r.EndedAt = unixTimePtr(ended)
	r.LeaseUntil = unixTimePtr(leaseUntil)
	r.CancelRequested = cancelRequested != 0
	r.Metadata = json.RawMessage(metadata)
	if !json.Valid(r.Metadata) {
		r.Metadata = json.RawMessage(`{}`)
	}
	return &r, nil
}

func (s *OrchestrationStore) Run(ctx context.Context, runID int64) (*EngineRun, error) {
	run, err := scanEngineRun(s.db.QueryRowContext(ctx,
		`SELECT `+engineRunColumns+` FROM deploy_runs WHERE id = ?`, runID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRunNotFound
	}
	return run, err
}

func (s *OrchestrationStore) ProductionEnvironment(
	ctx context.Context,
	projectID int64,
) (environmentID int64, planRevision int, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT id, desired_revision FROM deploy_environments
		 WHERE project_id = ? AND slug = 'production' AND archived_at = 0`, projectID).
		Scan(&environmentID, &planRevision)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrEnvironmentNotFound
	}
	return
}

func (s *OrchestrationStore) ProjectActive(ctx context.Context, projectID int64) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM deploy_runs
		 WHERE project_id = ? AND state IN (
		   'requested', 'validating', 'queued', 'preparing', 'running', 'verifying',
		   'activating', 'failed_activation', 'restoring_previous', 'cancelling'
		 )`, projectID).Scan(&count)
	return count > 0, err
}

func (s *OrchestrationStore) Retry(
	ctx context.Context,
	runID int64,
	actor, idempotencyKey string,
) (*EngineRun, bool, error) {
	prior, err := s.Run(ctx, runID)
	if err != nil {
		return nil, false, err
	}
	if prior.State != RunFailed && prior.State != RunCancelled {
		return nil, false, ErrRunNotRetryable
	}
	steps, err := s.Steps(ctx, runID)
	if err != nil {
		return nil, false, err
	}
	digest := prior.RequestDigest + ":retry:" + fmt.Sprint(runID)
	return s.Enqueue(ctx, RunRequest{
		ProjectID: prior.ProjectID, EnvironmentID: prior.EnvironmentID,
		Operation: prior.Operation, Trigger: TriggerManual, Actor: actor,
		IdempotencyKey: idempotencyKey, RequestDigest: digest,
		PlanRevision: prior.PlanRevision, RetryOfRunID: prior.ID,
		VariableSnapshotRunID: prior.ID,
		Priority:              prior.Priority, SlotClass: prior.SlotClass,
		Metadata: append(json.RawMessage(nil), prior.Metadata...), Steps: orderedKeys(steps),
	})
}

// Enqueue commits the request, full step list and requested/validating/queued
// events together. Returning a run therefore proves it can be recovered after
// the caller disconnects. The bool is false for an idempotent replay.
func (s *OrchestrationStore) Enqueue(ctx context.Context, req RunRequest) (*EngineRun, bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := normalizeRunRequest(&req); err != nil {
		return nil, false, err
	}
	if req.IdempotencyKey != "" {
		if existing, err := s.idempotentRun(ctx, req); existing != nil || err != nil {
			return existing, false, err
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()

	var environmentKind string
	err = tx.QueryRowContext(ctx, `
		SELECT kind FROM deploy_environments
		 WHERE id = ? AND project_id = ? AND archived_at = 0`,
		req.EnvironmentID, req.ProjectID).Scan(&environmentKind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, ErrEnvironmentNotFound
	}
	if err != nil {
		return nil, false, err
	}

	// Existing workloads keep restart/removal controls, but unsupported blueprint
	// deployments cannot enter the queue through stale drafts or direct API calls.
	switch req.Operation {
	case OperationDeploy, OperationRedeploy, OperationForceBuild, OperationPreviewCreate, OperationPreviewUpdate:
		var kind string
		sourceErr := tx.QueryRowContext(ctx, `SELECT kind FROM deploy_sources WHERE environment_id = ? ORDER BY revision DESC LIMIT 1`, req.EnvironmentID).Scan(&kind)
		if sourceErr != nil && !errors.Is(sourceErr, sql.ErrNoRows) {
			return nil, false, sourceErr
		}
		if SourceKind(kind) == SourceBlueprint {
			return nil, false, fmt.Errorf("%w: blueprint deployment is unavailable in this release", ErrUnsupportedSource)
		}
	}

	now := s.now().UTC()
	metadata := string(req.Metadata)
	result, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_runs(
		  project_id, environment_id, started_at, status, state, operation, trigger,
		  actor, requested_at, retry_of_run_id, idempotency_key, request_digest,
		  plan_revision, priority, slot_class, metadata_json)
		VALUES(?, ?, ?, 'running', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		req.ProjectID, req.EnvironmentID, now.Unix(), RunRequested, req.Operation,
		req.Trigger, req.Actor, now.Unix(), req.RetryOfRunID, req.IdempotencyKey,
		req.RequestDigest, req.PlanRevision, req.Priority, req.SlotClass, metadata)
	if err != nil {
		if req.IdempotencyKey != "" && isUniqueConstraint(err) {
			_ = tx.Rollback()
			existing, existingErr := s.idempotentRun(ctx, req)
			return existing, false, existingErr
		}
		return nil, false, err
	}
	runID, err := result.LastInsertId()
	if err != nil {
		return nil, false, err
	}
	if err := snapshotRunVariablesTx(
		ctx, tx, runID, req.EnvironmentID, req.VariableSnapshotRunID, now,
	); err != nil {
		return nil, false, err
	}
	if err := snapshotRunPlanInputsTx(
		ctx, tx, runID, req.EnvironmentID, req.VariableSnapshotRunID, now,
	); err != nil {
		return nil, false, err
	}

	for ordinal, key := range req.Steps {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO deploy_steps(run_id, step_key, ordinal, status, attempt)
			VALUES(?, ?, ?, ?, 1)`, runID, key, ordinal+1, StepPending); err != nil {
			return nil, false, err
		}
	}

	var events []RunEvent
	for _, state := range []RunState{RunRequested, RunValidating, RunQueued} {
		if state != RunRequested {
			if err := setRunStateTx(ctx, tx, runID, state, now); err != nil {
				return nil, false, err
			}
		}
		event, err := appendEventTx(ctx, tx, now, runID, 0, EventRunState, "status", "",
			mustJSON(map[string]any{"state": state}))
		if err != nil {
			return nil, false, err
		}
		events = append(events, event)
	}

	if err := s.supersedeEligibleTx(ctx, tx, req, runID, environmentKind, now, &events); err != nil {
		return nil, false, err
	}
	run, err := scanEngineRun(tx.QueryRowContext(ctx,
		`SELECT `+engineRunColumns+` FROM deploy_runs WHERE id = ?`, runID))
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	s.publish(events)
	return run, true, nil
}

type runVariableRevisionRef struct {
	id       int64
	snapshot ReleaseVariableSnapshot
}

func snapshotRunVariablesTx(
	ctx context.Context,
	tx *sql.Tx,
	runID, environmentID, copyFromRunID int64,
	now time.Time,
) error {
	refs := []runVariableRevisionRef{}
	expectedDigest := ""
	if copyFromRunID != 0 {
		var sourceEnvironmentID int64
		if err := tx.QueryRowContext(ctx, `
			SELECT environment_id, digest
			  FROM deploy_run_variable_snapshots
			 WHERE run_id = ?`, copyFromRunID).
			Scan(&sourceEnvironmentID, &expectedDigest); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: retry source has no variable snapshot", ErrInvalidPlan)
			}
			return err
		}
		if sourceEnvironmentID != environmentID {
			return fmt.Errorf("%w: retry variable snapshot belongs to another environment", ErrInvalidPlan)
		}
	}

	query := `
		SELECT v.id, v.key, v.sensitivity, v.scopes, v.value_digest
		  FROM deploy_variable_revisions v`
	args := []any{environmentID}
	if copyFromRunID == 0 {
		query += ` WHERE v.environment_id = ? AND v.active = 1 ORDER BY v.key, v.id`
	} else {
		query += `
		  JOIN deploy_run_variable_revisions rv ON rv.variable_revision_id = v.id
		 WHERE rv.run_id = ? AND v.environment_id = ?
		 ORDER BY rv.ordinal`
		args = []any{copyFromRunID, environmentID}
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var ref runVariableRevisionRef
		if err := rows.Scan(
			&ref.id, &ref.snapshot.Name, &ref.snapshot.Sensitivity,
			&ref.snapshot.Scopes, &ref.snapshot.ValueDigest,
		); err != nil {
			rows.Close()
			return err
		}
		if !contentDigestRE.MatchString(ref.snapshot.ValueDigest) {
			rows.Close()
			return fmt.Errorf("%w: variable %s has no immutable value digest", ErrInvalidPlan, ref.snapshot.Name)
		}
		if len(refs) != 0 && refs[len(refs)-1].snapshot.Name == ref.snapshot.Name {
			rows.Close()
			return fmt.Errorf("%w: variable %s has multiple active revisions", ErrInvalidPlan, ref.snapshot.Name)
		}
		refs = append(refs, ref)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	variables := make([]ReleaseVariableSnapshot, 0, len(refs))
	for _, ref := range refs {
		variables = append(variables, ref.snapshot)
	}
	digest := digestReleaseVariables(variables)
	if expectedDigest != "" && expectedDigest != digest {
		return fmt.Errorf("%w: retry variable snapshot digest does not match its revisions", ErrInvalidPlan)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_run_variable_snapshots(
		  run_id, environment_id, digest, copied_from_run_id, created_at)
		VALUES(?, ?, ?, ?, ?)`, runID, environmentID, digest, copyFromRunID, now.Unix()); err != nil {
		return err
	}
	for ordinal, ref := range refs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO deploy_run_variable_revisions(run_id, variable_revision_id, ordinal)
			VALUES(?, ?, ?)`, runID, ref.id, ordinal+1); err != nil {
			return err
		}
	}
	return nil
}

func snapshotRunPlanInputsTx(
	ctx context.Context,
	tx *sql.Tx,
	runID, environmentID, copyFromRunID int64,
	now time.Time,
) error {
	var dependenciesJSON, checksJSON []byte
	expectedDigest := ""
	if copyFromRunID != 0 {
		var sourceEnvironmentID int64
		var dependencies, checks string
		if err := tx.QueryRowContext(ctx, `
			SELECT environment_id, dependencies_json, checks_json, digest
			  FROM deploy_run_plan_snapshots
			 WHERE run_id = ?`, copyFromRunID).
			Scan(&sourceEnvironmentID, &dependencies, &checks, &expectedDigest); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: retry source has no dependency/check snapshot", ErrInvalidPlan)
			}
			return err
		}
		if sourceEnvironmentID != environmentID {
			return fmt.Errorf("%w: retry plan snapshot belongs to another environment", ErrInvalidPlan)
		}
		dependenciesJSON, checksJSON = []byte(dependencies), []byte(checks)
	} else {
		dependencies := []json.RawMessage{}
		checks := []json.RawMessage{}
		for _, query := range []struct {
			sql    string
			target *[]json.RawMessage
		}{
			{`SELECT json_object('kind', kind, 'ownership', ownership, 'resourceKind', resource_kind,
			                    'resourceId', resource_id, 'config', json(config_json))
			    FROM deploy_dependencies WHERE environment_id = ? AND release_id = 0
			   ORDER BY kind, resource_kind, resource_id`, &dependencies},
			{`SELECT json_object('name', name, 'kind', kind, 'phase', phase, 'config', json(config_json),
			                    'required', json(CASE WHEN required <> 0 THEN 'true' ELSE 'false' END))
			    FROM deploy_checks WHERE environment_id = ? ORDER BY ordinal, name`, &checks},
		} {
			rows, err := tx.QueryContext(ctx, query.sql, environmentID)
			if err != nil {
				return err
			}
			for rows.Next() {
				var raw string
				if err := rows.Scan(&raw); err != nil {
					rows.Close()
					return err
				}
				*query.target = append(*query.target, json.RawMessage(raw))
			}
			if err := rows.Close(); err != nil {
				return err
			}
		}
		var err error
		dependenciesJSON, err = json.Marshal(dependencies)
		if err != nil {
			return err
		}
		checksJSON, err = json.Marshal(checks)
		if err != nil {
			return err
		}
	}
	if !json.Valid(dependenciesJSON) || !json.Valid(checksJSON) {
		return fmt.Errorf("%w: dependency/check snapshot is malformed", ErrInvalidPlan)
	}
	digest := digestBytes(dependenciesJSON, checksJSON)
	if expectedDigest != "" && expectedDigest != digest {
		return fmt.Errorf("%w: retry dependency/check snapshot digest is inconsistent", ErrInvalidPlan)
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_run_plan_snapshots(
		  run_id, environment_id, dependencies_json, checks_json, digest,
		  copied_from_run_id, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)`, runID, environmentID, string(dependenciesJSON),
		string(checksJSON), digest, copyFromRunID, now.Unix())
	return err
}

func normalizeRunRequest(req *RunRequest) error {
	if req.ProjectID <= 0 || req.EnvironmentID <= 0 {
		return fmt.Errorf("invalid deployment or environment id")
	}
	if !validOperation(req.Operation) {
		return fmt.Errorf("invalid deployment operation %q", req.Operation)
	}
	if !validTrigger(req.Trigger) {
		return fmt.Errorf("invalid deployment trigger %q", req.Trigger)
	}
	if req.RequestDigest == "" {
		return fmt.Errorf("request digest is required")
	}
	if len(req.IdempotencyKey) > 256 || len(req.RequestDigest) > 256 {
		return fmt.Errorf("idempotency key or request digest is too long")
	}
	if req.PlanRevision <= 0 {
		return fmt.Errorf("plan revision must be positive")
	}
	if req.Priority == 0 {
		req.Priority = defaultPriority
	}
	if req.SlotClass == "" {
		req.SlotClass = SlotLight
	}
	if !req.SlotClass.valid() {
		return fmt.Errorf("invalid deployment slot class %q", req.SlotClass)
	}
	if len(req.Metadata) == 0 {
		req.Metadata = json.RawMessage(`{}`)
	}
	if !json.Valid(req.Metadata) || len(req.Metadata) > maxEventBytes {
		return fmt.Errorf("invalid or oversized deployment metadata")
	}
	if len(req.Steps) == 0 {
		req.Steps = append([]StepKey(nil), DefaultStepKeys...)
	}
	seen := make(map[StepKey]bool, len(req.Steps))
	for _, key := range req.Steps {
		if !validStepKey(key) || seen[key] {
			return fmt.Errorf("invalid or duplicate deployment step %q", key)
		}
		seen[key] = true
	}
	return nil
}

func (s *OrchestrationStore) idempotentRun(ctx context.Context, req RunRequest) (*EngineRun, error) {
	run, err := scanEngineRun(s.db.QueryRowContext(ctx, `
		SELECT `+engineRunColumns+` FROM deploy_runs
		 WHERE project_id = ? AND environment_id = ? AND operation = ? AND idempotency_key = ?`,
		req.ProjectID, req.EnvironmentID, req.Operation, req.IdempotencyKey))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if run.RequestDigest != req.RequestDigest {
		return nil, ErrIdempotencyConflict
	}
	return run, nil
}

func setRunStateTx(ctx context.Context, tx *sql.Tx, runID int64, state RunState, now time.Time) error {
	queuedAt := int64(0)
	if state == RunQueued {
		queuedAt = now.Unix()
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE deploy_runs
		   SET state = ?, queued_at = CASE WHEN ? <> 0 THEN ? ELSE queued_at END
		 WHERE id = ?`, state, queuedAt, queuedAt, runID)
	return err
}

func isUniqueConstraint(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}

func mustJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func newClaimToken() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func unixTime(value int64) time.Time { return time.Unix(value, 0).UTC() }

func unixTimePtr(value int64) *time.Time {
	if value == 0 {
		return nil
	}
	result := unixTime(value)
	return &result
}

func legacyStatus(state RunState) string {
	switch state {
	case RunSucceeded, RunRolledBack:
		return string(StatusSuccess)
	case RunFailed, RunCancelled, RunSuperseded:
		return string(StatusFailed)
	default:
		return string(StatusRunning)
	}
}

func truncateUTF8(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value, true
}
