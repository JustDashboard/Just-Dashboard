package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"
)

var ErrPortUnavailable = errors.New("deployment port is unavailable")

type ReleaseRuntime struct {
	ReleaseID        int64           `json:"releaseId"`
	EnvironmentID    int64           `json:"environmentId"`
	Kind             string          `json:"kind"`
	RuntimeID        string          `json:"runtimeId"`
	Name             string          `json:"name,omitempty"`
	WorkingDirectory string          `json:"workingDirectory,omitempty"`
	Host             string          `json:"host,omitempty"`
	Port             int             `json:"port,omitempty"`
	State            string          `json:"state"`
	Metadata         json.RawMessage `json:"metadata"`
	CreatedAt        time.Time       `json:"createdAt"`
	UpdatedAt        time.Time       `json:"updatedAt"`
}

type ReleaseRuntimeInput struct {
	ReleaseID        int64
	Kind             string
	RuntimeID        string
	Name             string
	WorkingDirectory string
	Host             string
	Port             int
	Metadata         json.RawMessage
}

func scanReleaseRuntime(row scanner) (*ReleaseRuntime, error) {
	var runtime ReleaseRuntime
	var metadata string
	var createdAt, updatedAt int64
	if err := row.Scan(
		&runtime.ReleaseID, &runtime.EnvironmentID, &runtime.Kind, &runtime.RuntimeID,
		&runtime.Name, &runtime.WorkingDirectory, &runtime.Host, &runtime.Port,
		&runtime.State, &metadata, &createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}
	runtime.Metadata = validRawJSON(metadata)
	runtime.CreatedAt, runtime.UpdatedAt = unixTime(createdAt), unixTime(updatedAt)
	return &runtime, nil
}

const releaseRuntimeColumns = `
release_id, environment_id, kind, runtime_id, name, working_directory,
host, port, state, metadata_json, created_at, updated_at`

func (s *OrchestrationStore) RuntimeForRelease(ctx context.Context, releaseID int64) (*ReleaseRuntime, error) {
	runtime, err := scanReleaseRuntime(s.db.QueryRowContext(ctx,
		`SELECT `+releaseRuntimeColumns+` FROM deploy_release_runtimes WHERE release_id = ?`, releaseID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrArtifactMissing
	}
	return runtime, err
}

func (s *OrchestrationStore) RecordCandidateRuntime(
	ctx context.Context,
	run EngineRun,
	claimToken string,
	input ReleaseRuntimeInput,
) (*ReleaseRuntime, error) {
	if input.ReleaseID <= 0 || (input.Kind != "container" && input.Kind != "compose") ||
		input.RuntimeID == "" || input.Port < 0 || input.Port > 65535 ||
		len(input.Metadata) > maxEventBytes || (len(input.Metadata) != 0 && !json.Valid(input.Metadata)) {
		return nil, fmt.Errorf("%w: candidate runtime identity is invalid", ErrInvalidPlan)
	}
	if len(input.Metadata) == 0 {
		input.Metadata = json.RawMessage(`{}`)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	if err := assertLeaseTx(ctx, tx, run.ID, claimToken, now); err != nil {
		return nil, err
	}
	var releaseRunID, environmentID int64
	var releaseState string
	if err := tx.QueryRowContext(ctx, `
		SELECT run_id, environment_id, state FROM deploy_releases WHERE id = ?`, input.ReleaseID).
		Scan(&releaseRunID, &environmentID, &releaseState); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrArtifactMissing
		}
		return nil, err
	}
	if releaseRunID != run.ID || environmentID != run.EnvironmentID || releaseState != "candidate" {
		return nil, fmt.Errorf("%w: runtime does not belong to this candidate release", ErrInvalidPlan)
	}
	existing, err := scanReleaseRuntime(tx.QueryRowContext(ctx,
		`SELECT `+releaseRuntimeColumns+` FROM deploy_release_runtimes WHERE release_id = ?`, input.ReleaseID))
	if err == nil {
		if existing.Kind != input.Kind || existing.RuntimeID != input.RuntimeID || existing.Name != input.Name ||
			existing.WorkingDirectory != input.WorkingDirectory || existing.Host != input.Host ||
			existing.Port != input.Port || string(existing.Metadata) != string(input.Metadata) {
			return nil, fmt.Errorf("%w: candidate release already names another runtime", ErrInvalidPlan)
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_release_runtimes(
		  release_id, environment_id, kind, runtime_id, name, working_directory,
		  host, port, state, metadata_json, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, 'candidate', ?, ?, ?)`, input.ReleaseID,
		run.EnvironmentID, input.Kind, input.RuntimeID, input.Name, input.WorkingDirectory,
		input.Host, input.Port, string(input.Metadata), now.Unix(), now.Unix()); err != nil {
		return nil, err
	}
	runtime, err := scanReleaseRuntime(tx.QueryRowContext(ctx,
		`SELECT `+releaseRuntimeColumns+` FROM deploy_release_runtimes WHERE release_id = ?`, input.ReleaseID))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return runtime, nil
}

func (s *OrchestrationStore) SetRuntimeState(
	ctx context.Context,
	runID int64,
	claimToken string,
	releaseID int64,
	state string,
) error {
	if state != "candidate" && state != "ready" && state != "live" && state != "draining" &&
		state != "stopped" && state != "retired" && state != "failed" {
		return fmt.Errorf("invalid release runtime state %q", state)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	if err := assertLeaseTx(ctx, tx, runID, claimToken, now); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE deploy_release_runtimes SET state = ?, updated_at = ?
		 WHERE release_id = ?`, state, now.Unix(), releaseID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrArtifactMissing
	}
	return tx.Commit()
}

func (s *OrchestrationStore) LiveRelease(ctx context.Context, environmentID int64) (*ReleaseWithArtifacts, error) {
	var releaseID int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT live_release_id FROM deploy_environments WHERE id = ?`, environmentID).Scan(&releaseID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrEnvironmentNotFound
		}
		return nil, err
	}
	if releaseID == 0 {
		return nil, ErrArtifactMissing
	}
	return s.Release(ctx, releaseID)
}

func (s *OrchestrationStore) CompletePreviewRemoval(ctx context.Context, runID int64, claimToken string, releaseID int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = assertLeaseTx(ctx, tx, runID, claimToken, s.now().UTC()); err != nil {
		return err
	}
	var environmentID int64
	var kind string
	var liveID int64
	if err = tx.QueryRowContext(ctx, `SELECT r.environment_id,e.kind,e.live_release_id FROM deploy_runs r JOIN deploy_environments e ON e.id=r.environment_id WHERE r.id=?`, runID).Scan(&environmentID, &kind, &liveID); err != nil {
		return err
	}
	if kind != string(EnvironmentPreview) || liveID != releaseID {
		return fmt.Errorf("%w: preview live release changed during cleanup", ErrInvalidPlan)
	}
	now := s.now().UTC().Unix()
	if _, err = tx.ExecContext(ctx, `UPDATE deploy_release_runtimes SET state='retired',updated_at=? WHERE release_id=?`, now, releaseID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE deploy_releases SET state='retained',retired_at=? WHERE id=?`, now, releaseID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE deploy_environments SET live_release_id=0,archived_at=?,updated_at=? WHERE id=?`, now, now, environmentID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *OrchestrationStore) LinkRunToLiveRelease(
	ctx context.Context,
	runID int64,
	claimToken string,
	releaseID int64,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	if err := assertLeaseTx(ctx, tx, runID, claimToken, now); err != nil {
		return err
	}
	var environmentID, liveReleaseID int64
	if err := tx.QueryRowContext(ctx,
		`SELECT environment_id FROM deploy_runs WHERE id = ?`, runID).Scan(&environmentID); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT live_release_id FROM deploy_environments WHERE id = ?`, environmentID).Scan(&liveReleaseID); err != nil {
		return err
	}
	if releaseID != liveReleaseID {
		return fmt.Errorf("%w: run target is no longer the live release", ErrInvalidPlan)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deploy_runs SET release_id = ? WHERE id = ?`, releaseID, runID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *OrchestrationStore) ActivateCandidate(
	ctx context.Context,
	runID int64,
	claimToken string,
	releaseID int64,
) (*Release, error) {
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
	candidate, err := releaseByIDTx(ctx, tx, releaseID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrArtifactMissing
		}
		return nil, err
	}
	if candidate.RunID != runID || candidate.State != "candidate" {
		return nil, fmt.Errorf("%w: release is not this run's candidate", ErrInvalidPlan)
	}
	var liveReleaseID int64
	if err := tx.QueryRowContext(ctx, `
		SELECT live_release_id FROM deploy_environments WHERE id = ?`, candidate.EnvironmentID).
		Scan(&liveReleaseID); err != nil {
		return nil, err
	}
	if liveReleaseID != candidate.PredecessorReleaseID {
		return nil, fmt.Errorf("%w: live release changed after candidate creation", ErrInvalidPlan)
	}
	var runtimeState string
	if err := tx.QueryRowContext(ctx,
		`SELECT state FROM deploy_release_runtimes WHERE release_id = ?`, releaseID).Scan(&runtimeState); err != nil {
		return nil, ErrArtifactMissing
	}
	if runtimeState != "ready" && runtimeState != "candidate" {
		return nil, fmt.Errorf("%w: candidate runtime is not ready", ErrInvalidPlan)
	}
	if liveReleaseID != 0 {
		if _, err := tx.ExecContext(ctx,
			`UPDATE deploy_releases SET state = 'retained' WHERE id = ?`, liveReleaseID); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE deploy_release_runtimes SET state = 'draining', updated_at = ?
			 WHERE release_id = ? AND state IN ('live', 'ready', 'candidate')`, now.Unix(), liveReleaseID); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE deploy_releases SET state = 'live', activated_at = ? WHERE id = ?`, now.Unix(), releaseID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE deploy_release_runtimes SET state = 'live', updated_at = ? WHERE release_id = ?`, now.Unix(), releaseID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE deploy_environments SET live_release_id = ?, updated_at = ? WHERE id = ?`,
		releaseID, now.Unix(), candidate.EnvironmentID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE deploy_runs SET release_id = ? WHERE id = ?`, releaseID, runID); err != nil {
		return nil, err
	}
	activated, err := releaseByIDTx(ctx, tx, releaseID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return activated, nil
}

func (s *OrchestrationStore) MarkPreviousRetired(
	ctx context.Context,
	runID int64,
	claimToken string,
	candidateReleaseID int64,
) (int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	if err := assertLeaseTx(ctx, tx, runID, claimToken, now); err != nil {
		return 0, err
	}
	var predecessorID int64
	if err := tx.QueryRowContext(ctx, `
		SELECT predecessor_release_id FROM deploy_releases
		 WHERE id = ? AND run_id = ? AND state = 'live'`, candidateReleaseID, runID).
		Scan(&predecessorID); err != nil {
		return 0, ErrArtifactMissing
	}
	if predecessorID == 0 {
		return 0, tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE deploy_releases SET retired_at = ? WHERE id = ?`, now.Unix(), predecessorID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE deploy_release_runtimes SET state = 'retired', updated_at = ?
		 WHERE release_id = ?`, now.Unix(), predecessorID); err != nil {
		return 0, err
	}
	return predecessorID, tx.Commit()
}

type PortLease struct {
	ID            int64
	EnvironmentID int64
	RunID         int64
	Address       string
	Port          int
	Protocol      string
	Token         string
	ExpiresAt     time.Time
}

func (s *OrchestrationStore) AcquirePortLease(
	ctx context.Context,
	runID, environmentID int64,
	claimToken, address string,
	preferredPort int,
	ttl time.Duration,
) (*PortLease, error) {
	if address == "" {
		address = "127.0.0.1"
	}
	if address != "127.0.0.1" && address != "::1" {
		return nil, fmt.Errorf("%w: candidate lease must use loopback", ErrPortUnavailable)
	}
	if preferredPort < 0 || preferredPort > 65535 {
		return nil, ErrPortUnavailable
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	for attempt := 0; attempt < 16; attempt++ {
		listener, err := net.Listen("tcp", net.JoinHostPort(address, strconv.Itoa(preferredPort)))
		if err != nil {
			return nil, fmt.Errorf("%w: loopback port cannot be reserved", ErrPortUnavailable)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		s.writeMu.Lock()
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			s.writeMu.Unlock()
			listener.Close()
			return nil, err
		}
		now := s.now().UTC()
		if err = assertLeaseTx(ctx, tx, runID, claimToken, now); err == nil {
			_, err = tx.ExecContext(ctx, `DELETE FROM deploy_port_leases WHERE expires_at < ?`, now.Unix())
		}
		token := ""
		if err == nil {
			token, err = newClaimToken()
		}
		expires := now.Add(ttl)
		var result sql.Result
		if err == nil {
			result, err = tx.ExecContext(ctx, `
				INSERT INTO deploy_port_leases(
				  environment_id, run_id, address, port, protocol, purpose, token, expires_at, created_at)
				VALUES(?, ?, ?, ?, 'tcp', 'candidate', ?, ?, ?)`,
				environmentID, runID, address, port, token, expires.Unix(), now.Unix())
		}
		var id int64
		if err == nil {
			id, err = result.LastInsertId()
		}
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		s.writeMu.Unlock()
		_ = listener.Close()
		if err == nil {
			return &PortLease{
				ID: id, EnvironmentID: environmentID, RunID: runID, Address: address,
				Port: port, Protocol: "tcp", Token: token, ExpiresAt: expires,
			}, nil
		}
		if preferredPort != 0 || !isUniqueConstraint(err) {
			return nil, err
		}
	}
	return nil, ErrPortUnavailable
}

func (s *OrchestrationStore) ReleasePortLease(ctx context.Context, runID int64, token string) error {
	if token == "" {
		return nil
	}
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM deploy_port_leases WHERE run_id = ? AND token = ?`, runID, token)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected > 1 {
		return fmt.Errorf("multiple deployment port leases matched one token")
	}
	return nil
}
