package stacks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// ErrNotFound is a project that is not a stack.
var ErrNotFound = errors.New("not a stack")

// Status is where a stack stands as a whole.
type Status string

const (
	// StatusWaiting: no live release yet; the deployment run is bringing it up.
	StatusWaiting     Status = "waiting"
	StatusConfiguring Status = "configuring"
	StatusReady       Status = "ready"
	// StatusAction: everything that applies is done but for what only the
	// person can do.
	StatusAction Status = "action_required"
	StatusFailed Status = "failed"
)

// Record is a stack deployment as the store holds it.
type Record struct {
	ProjectID           int64
	EnvironmentID       int64
	StackID             string
	Preset              string
	Status              Status
	LiveReleaseID       int64
	ConfiguredReleaseID int64
	ConfiguredVersion   string
	RequestedAt         time.Time
	StartedAt           time.Time
	FinishedAt          time.Time
	CreatedBy           string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// StepRecord is a step's last outcome.
type StepRecord struct {
	StepID   string
	Status   StepStatus
	Attempts int
	Message  string
	Error    string
	Outputs  map[string]string
	Action   *Action
	// Log is what the step said while it ran (Env.Logf), masked as Message
	// and Error are, the newest lines kept.
	Log        []string
	StartedAt  time.Time
	FinishedAt time.Time
}

// Store keeps stacks in the dashboard's database.
type Store struct {
	db     *sql.DB
	sealer *auth.Sealer
	now    func() time.Time
}

func NewStore(db *sql.DB, sealer *auth.Sealer) *Store {
	return &Store{db: db, sealer: sealer, now: time.Now}
}

// Register records a deployment as a stack. Registering one again keeps its
// record: a draft committed twice is one stack.
func (s *Store) Register(ctx context.Context, projectID, environmentID int64, stackID, preset, actor string) error {
	now := s.now().Unix()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO deploy_stacks (project_id, environment_id, stack_id, preset, status, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'waiting', ?, ?, ?)
		ON CONFLICT(project_id) DO NOTHING`,
		projectID, environmentID, stackID, preset, actor, now, now)
	return err
}

const recordColumns = `project_id, environment_id, stack_id, preset, status, live_release_id,
	configured_release_id, configured_version, requested_at, started_at, finished_at,
	created_by, created_at, updated_at`

func scanRecord(row interface{ Scan(...any) error }) (*Record, error) {
	var record Record
	var requested, started, finished, created, updated int64
	if err := row.Scan(&record.ProjectID, &record.EnvironmentID, &record.StackID, &record.Preset, &record.Status,
		&record.LiveReleaseID, &record.ConfiguredReleaseID, &record.ConfiguredVersion,
		&requested, &started, &finished, &record.CreatedBy, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	record.RequestedAt, record.StartedAt, record.FinishedAt = unixTime(requested), unixTime(started), unixTime(finished)
	record.CreatedAt, record.UpdatedAt = unixTime(created), unixTime(updated)
	return &record, nil
}

func unixTime(seconds int64) time.Time {
	if seconds == 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}

func unixOf(at time.Time) int64 {
	if at.IsZero() {
		return 0
	}
	return at.Unix()
}

// Get returns a project's stack, or ErrNotFound.
func (s *Store) Get(ctx context.Context, projectID int64) (*Record, error) {
	return scanRecord(s.db.QueryRowContext(ctx, `SELECT `+recordColumns+` FROM deploy_stacks WHERE project_id = ?`, projectID))
}

// List returns every stack.
func (s *Store) List(ctx context.Context) ([]Record, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+recordColumns+` FROM deploy_stacks ORDER BY project_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *record)
	}
	return out, rows.Err()
}

// RequestPass asks for a configuration pass now (Retry, Repair).
func (s *Store) RequestPass(ctx context.Context, projectID int64) error {
	now := s.now().Unix()
	result, err := s.db.ExecContext(ctx, `UPDATE deploy_stacks SET requested_at = ?, updated_at = ? WHERE project_id = ?`, now, now, projectID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

// SetWaiting records that the stack has no live release to configure.
func (s *Store) SetWaiting(ctx context.Context, projectID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE deploy_stacks SET status = 'waiting', live_release_id = 0, updated_at = ? WHERE project_id = ? AND status != 'waiting'`,
		s.now().Unix(), projectID)
	return err
}

// BeginPass records a pass starting for a release.
func (s *Store) BeginPass(ctx context.Context, projectID, releaseID int64) error {
	now := s.now().Unix()
	_, err := s.db.ExecContext(ctx, `
		UPDATE deploy_stacks SET status = 'configuring', live_release_id = ?, requested_at = 0,
		       started_at = ?, finished_at = 0, updated_at = ?
		 WHERE project_id = ?`, releaseID, now, now, projectID)
	return err
}

// FinishPass records a pass's outcome. Only a pass that got to the end of the
// graph marks the release configured; one cut short (the dashboard stopping)
// leaves it to run again.
func (s *Store) FinishPass(ctx context.Context, projectID, releaseID int64, version string, status Status, complete bool) error {
	now := s.now().Unix()
	if complete {
		_, err := s.db.ExecContext(ctx, `
			UPDATE deploy_stacks SET status = ?, configured_release_id = ?, configured_version = ?,
			       finished_at = ?, updated_at = ?
			 WHERE project_id = ?`, status, releaseID, version, now, now, projectID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE deploy_stacks SET status = ?, finished_at = ?, updated_at = ? WHERE project_id = ?`,
		status, now, now, projectID)
	return err
}

// InterruptedPasses are stacks a stopped dashboard left configuring: they
// are run again, which is safe because every step converges.
func (s *Store) ResetInterrupted(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE deploy_stacks SET status = 'failed', requested_at = ?, updated_at = ? WHERE status = 'configuring'`,
		s.now().Unix(), s.now().Unix())
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE deploy_stack_steps SET status = 'pending', error = 'interrupted when the dashboard stopped' WHERE status = 'running'`)
	return err
}

// Steps returns a stack's step records by step id.
func (s *Store) Steps(ctx context.Context, projectID int64) (map[string]StepRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT step_id, status, attempts, message, error, outputs_json, action_json, log_json, started_at, finished_at
		  FROM deploy_stack_steps WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]StepRecord{}
	for rows.Next() {
		var record StepRecord
		var outputs, action, log string
		var started, finished int64
		if err := rows.Scan(&record.StepID, &record.Status, &record.Attempts, &record.Message, &record.Error,
			&outputs, &action, &log, &started, &finished); err != nil {
			return nil, err
		}
		record.StartedAt, record.FinishedAt = unixTime(started), unixTime(finished)
		_ = json.Unmarshal([]byte(outputs), &record.Outputs)
		_ = json.Unmarshal([]byte(log), &record.Log)
		if action != "" {
			record.Action = &Action{}
			if json.Unmarshal([]byte(action), record.Action) != nil {
				record.Action = nil
			}
		}
		out[record.StepID] = record
	}
	return out, rows.Err()
}

// PutStep writes a step's outcome.
func (s *Store) PutStep(ctx context.Context, projectID int64, record StepRecord) error {
	outputs, err := json.Marshal(record.Outputs)
	if err != nil || record.Outputs == nil {
		outputs = []byte("{}")
	}
	action := ""
	if record.Action != nil {
		raw, _ := json.Marshal(record.Action)
		action = string(raw)
	}
	log, err := json.Marshal(record.Log)
	if err != nil || record.Log == nil {
		log = []byte("[]")
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO deploy_stack_steps (project_id, step_id, status, attempts, message, error, outputs_json, action_json, log_json, started_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, step_id) DO UPDATE SET status = excluded.status, attempts = excluded.attempts,
		       message = excluded.message, error = excluded.error, outputs_json = excluded.outputs_json,
		       action_json = excluded.action_json, log_json = excluded.log_json,
		       started_at = excluded.started_at, finished_at = excluded.finished_at`,
		projectID, record.StepID, record.Status, record.Attempts, truncate(record.Message, 2000), truncate(record.Error, 2000),
		string(outputs), action, string(log), unixOf(record.StartedAt), unixOf(record.FinishedAt))
	return err
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "…"
}

// Credentials is a stack's CredentialStore.
func (s *Store) Credentials(projectID int64) CredentialStore {
	return &storeCredentials{store: s, projectID: projectID}
}

// StoredCredentials names the credentials a stack holds.
func (s *Store) StoredCredentials(ctx context.Context, projectID int64) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM deploy_stack_credentials WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

type storeCredentials struct {
	store     *Store
	projectID int64
}

func (c *storeCredentials) Get(ctx context.Context, name string) (string, bool, error) {
	var sealed string
	err := c.store.db.QueryRowContext(ctx, `SELECT sealed FROM deploy_stack_credentials WHERE project_id = ? AND name = ?`,
		c.projectID, name).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	value, err := c.store.sealer.Open(sealed)
	if err != nil {
		return "", false, fmt.Errorf("could not open the stack's %s: %w", name, err)
	}
	return value, true, nil
}

func (c *storeCredentials) Put(ctx context.Context, name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("refused to store an empty %s", name)
	}
	sealed, err := c.store.sealer.Seal(value)
	if err != nil {
		return err
	}
	now := c.store.now().Unix()
	_, err = c.store.db.ExecContext(ctx, `
		INSERT INTO deploy_stack_credentials (project_id, name, sealed, created_at, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(project_id, name) DO UPDATE SET sealed = excluded.sealed, updated_at = excluded.updated_at`,
		c.projectID, name, sealed, now, now)
	return err
}

func (c *storeCredentials) Delete(ctx context.Context, name string) error {
	_, err := c.store.db.ExecContext(ctx, `DELETE FROM deploy_stack_credentials WHERE project_id = ? AND name = ?`, c.projectID, name)
	return err
}

// SetPreset records that the stack now runs another preset: its source was
// changed to that preset's template.
func (s *Store) SetPreset(ctx context.Context, projectID int64, preset string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE deploy_stacks SET preset = ?, updated_at = ? WHERE project_id = ?`,
		preset, s.now().Unix(), projectID)
	return err
}
