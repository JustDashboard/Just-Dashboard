package backups

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

var ErrNotFound = errors.New("backup job not found")

type Store struct {
	st     *store.Store
	sealer *auth.Sealer
	paths  *files.Service
	// describeDatabase is the Databases owner's answer for a saved connection
	// id; nil means a job's dump list is bounded but not checked for existence.
	describeDatabase func(context.Context, int64) (DatabaseDescription, error)
}

func NewStore(st *store.Store, sealer *auth.Sealer, pathServices ...*files.Service) *Store {
	paths := files.New([]string{"/"})
	if len(pathServices) > 0 && pathServices[0] != nil {
		paths = pathServices[0]
	}
	return &Store{st: st, sealer: sealer, paths: paths}
}

// ValidatePaths runs both when saving and when executing a job. Metadata stays
// readable after roots change so an administrator can repair or delete the job.
func (s *Store) ValidatePaths(j *Job) error {
	if err := j.Validate(); err != nil {
		return err
	}
	for i, path := range j.Sources {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("backup source path is required")
		}
		resolved, err := s.paths.Resolve(path)
		if err != nil {
			return err
		}
		j.Sources[i] = resolved
	}
	if j.TargetKind == TargetLocal {
		resolved, err := s.paths.Resolve(j.Target.Path)
		if err != nil {
			return err
		}
		j.Target.Path = resolved
	}
	if err := s.validateSQLitePaths(j); err != nil {
		return err
	}
	return s.validateDatabaseDumps(context.Background(), j)
}

const jobCols = `id, name, sources, excludes, target_kind, target_cfg, secrets_enc, schedule, retention, enabled, created_at, recovery_json, sqlite_paths, database_dumps, retention_days, pause_containers`

func (s *Store) scanJob(row interface{ Scan(...any) error }) (*Job, error) {
	var (
		j                              Job
		sources, excludes, targetKind  string
		targetCfg, secrets, schedule   string
		enabled                        int
		created                        int64
		recovery, sqlitePaths          string
		databaseDumps, pauseContainers string
	)
	if err := row.Scan(&j.ID, &j.Name, &sources, &excludes, &targetKind,
		&targetCfg, &secrets, &schedule, &j.Retention, &enabled, &created, &recovery, &sqlitePaths, &databaseDumps, &j.RetentionDays, &pauseContainers); err != nil {
		return nil, err
	}
	j.Sources = decodeStrings(sources)
	j.Excludes = decodeStrings(excludes)
	j.TargetKind = TargetKind(targetKind)
	j.Schedule = schedule
	j.Enabled = enabled == 1
	j.CreatedAt = time.Unix(created, 0).UTC()
	j.HasCredentials = secrets != ""
	json.Unmarshal([]byte(targetCfg), &j.Target)
	if err := json.Unmarshal([]byte(recovery), &j.Recovery); err != nil {
		return nil, errors.New("backup recovery configuration is malformed")
	}
	j.SQLitePaths = decodeStrings(sqlitePaths)
	j.DatabaseDumps = decodeInt64s(databaseDumps)
	j.PauseContainers = decodeStrings(pauseContainers)
	return &j, nil
}

func decodeInt64s(raw string) []int64 {
	out := []int64{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return []int64{}
	}
	return out
}

func (s *Store) List(ctx context.Context) ([]*Job, error) {
	rows, err := s.st.DB.QueryContext(ctx, `SELECT `+jobCols+` FROM backup_jobs ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Job{}
	for rows.Next() {
		j, err := s.scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, j := range out {
		if err := s.attachReadings(ctx, j); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// attachReadings fills the figures a list shows beside a job: its last run,
// when it last succeeded, and what its retained artifacts add up to.
func (s *Store) attachReadings(ctx context.Context, j *Job) error {
	if last, err := s.LastRun(ctx, j.ID); err == nil {
		j.LastRun = last
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	var (
		runs     int
		size     int64
		lastGood sql.NullInt64
	)
	err := s.st.DB.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(size_bytes),0), MAX(started_at) FROM backup_runs WHERE job_id = ? AND status = ? AND artifact != ''`,
		j.ID, string(StatusSuccess)).Scan(&runs, &size, &lastGood)
	if err != nil {
		return err
	}
	j.Stored = StoredSummary{Runs: runs, Bytes: size}
	if lastGood.Valid && lastGood.Int64 > 0 {
		at := time.Unix(lastGood.Int64, 0).UTC()
		j.LastSuccessAt = &at
	}
	return nil
}

func (s *Store) Get(ctx context.Context, id int64) (*Job, error) {
	row := s.st.DB.QueryRowContext(ctx, `SELECT `+jobCols+` FROM backup_jobs WHERE id = ?`, id)
	j, err := s.scanJob(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return j, s.attachReadings(ctx, j)
}

// SetEnabled pauses or resumes a job's schedule without touching anything
// else about it, so a pause survives the edit form and never rewrites keys.
func (s *Store) SetEnabled(ctx context.Context, id int64, enabled bool) (*Job, error) {
	value := 0
	if enabled {
		value = 1
	}
	res, err := s.st.DB.ExecContext(ctx, `UPDATE backup_jobs SET enabled = ? WHERE id = ?`, value, id)
	if err != nil {
		return nil, err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return nil, ErrNotFound
	}
	return s.Get(ctx, id)
}

// Secrets opens the sealed credentials for one job. Callers use them and
// discard them; they are never cached or logged.
func (s *Store) Secrets(ctx context.Context, id int64) (*TargetSecrets, error) {
	var sealed string
	err := s.st.DB.QueryRowContext(ctx, `SELECT secrets_enc FROM backup_jobs WHERE id = ?`, id).Scan(&sealed)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if sealed == "" {
		return &TargetSecrets{}, nil
	}
	plain, err := s.sealer.Open(sealed)
	if err != nil {
		return nil, err
	}
	var secrets TargetSecrets
	if err := json.Unmarshal([]byte(plain), &secrets); err != nil {
		return nil, err
	}
	return &secrets, nil
}

func (s *Store) Create(ctx context.Context, j *Job, secrets *TargetSecrets) (*Job, error) {
	if err := s.ValidatePaths(j); err != nil {
		return nil, err
	}
	sealed := ""
	if secrets != nil && (secrets.AccessKeyID != "" || secrets.SecretAccessKey != "") {
		v, err := s.sealer.Seal(encodeJSON(secrets))
		if err != nil {
			return nil, err
		}
		sealed = v
	}
	enabled := 0
	if j.Enabled {
		enabled = 1
	}
	res, err := s.st.DB.ExecContext(ctx,
		`INSERT INTO backup_jobs(name, sources, excludes, target_kind, target_cfg, secrets_enc, schedule, retention, enabled, created_at, recovery_json, sqlite_paths, database_dumps, retention_days, pause_containers)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.Name, encodeJSON(j.Sources), encodeJSON(j.Excludes), string(j.TargetKind),
		encodeJSON(j.Target), sealed, j.Schedule, j.Retention, enabled, time.Now().Unix(), encodeJSON(j.Recovery), encodeJSON(j.SQLitePaths), encodeJSON(j.DatabaseDumps), j.RetentionDays, encodeJSON(j.PauseContainers))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.Get(ctx, id)
}

// Update rewrites a job. Credentials are only replaced when new ones are
// supplied, so editing a schedule does not silently wipe stored keys.
func (s *Store) Update(ctx context.Context, id int64, j *Job, secrets *TargetSecrets) (*Job, error) {
	if err := s.ValidatePaths(j); err != nil {
		return nil, err
	}
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	enabled := 0
	if j.Enabled {
		enabled = 1
	}
	if secrets != nil && (secrets.AccessKeyID != "" || secrets.SecretAccessKey != "") {
		sealed, err := s.sealer.Seal(encodeJSON(secrets))
		if err != nil {
			return nil, err
		}
		if _, err := s.st.DB.ExecContext(ctx, `UPDATE backup_jobs SET secrets_enc = ? WHERE id = ?`, sealed, id); err != nil {
			return nil, err
		}
	}
	_, err := s.st.DB.ExecContext(ctx,
		`UPDATE backup_jobs SET name = ?, sources = ?, excludes = ?, target_kind = ?, target_cfg = ?,
		 schedule = ?, retention = ?, enabled = ?, recovery_json = ?, sqlite_paths = ?, database_dumps = ?, retention_days = ?, pause_containers = ? WHERE id = ?`,
		j.Name, encodeJSON(j.Sources), encodeJSON(j.Excludes), string(j.TargetKind),
		encodeJSON(j.Target), j.Schedule, j.Retention, enabled, encodeJSON(j.Recovery), encodeJSON(j.SQLitePaths), encodeJSON(j.DatabaseDumps), j.RetentionDays, encodeJSON(j.PauseContainers), id)
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *Store) Delete(ctx context.Context, id int64) error {
	result, err := s.st.DB.ExecContext(ctx, `DELETE FROM backup_jobs WHERE id=?
 AND NOT EXISTS (SELECT 1 FROM backup_runs WHERE job_id=? AND status='running')
 AND NOT EXISTS (SELECT 1 FROM backup_restore_tests t JOIN backup_runs r ON r.id=t.run_id WHERE r.job_id=? AND t.state IN ('running','cleanup_failed'))`, id, id, id)
	if err == nil {
		if affected, _ := result.RowsAffected(); affected == 0 {
			var exists bool
			if err := s.st.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM backup_jobs WHERE id=?)`, id).Scan(&exists); err != nil {
				return err
			}
			if exists {
				return ErrAlreadyRunning
			}
		}
	}
	return err
}

func (s *Store) StartRun(ctx context.Context, jobID int64, trigger string) (int64, error) {
	res, err := s.st.DB.ExecContext(ctx,
		`INSERT INTO backup_runs(job_id, started_at, status, trigger) VALUES(?,?,?,?)`,
		jobID, time.Now().Unix(), string(StatusRunning), trigger)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) FinishRun(ctx context.Context, runID int64, status RunStatus, artifact string, size int64, log string) error {
	return s.finishRun(ctx, runID, status, artifact, size, log, nil)
}

func (s *Store) finishRun(ctx context.Context, runID int64, status RunStatus, artifact string, size int64, log string, manifest *Manifest) error {
	_, err := s.st.DB.ExecContext(ctx,
		`UPDATE backup_runs SET ended_at = ?, status = ?, artifact = ?, size_bytes = ?, log = ?, manifest_json = ? WHERE id = ?`,
		time.Now().Unix(), string(status), artifact, size, truncate(log, 16000), encodeJSON(manifest), runID)
	return err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

const runCols = `id, job_id, started_at, ended_at, status, artifact, size_bytes, log, trigger, manifest_json,
 COALESCE((SELECT record_json FROM backup_restore_tests WHERE run_id=backup_runs.id ORDER BY id DESC LIMIT 1),'null')`

func scanRun(row interface{ Scan(...any) error }) (*Run, error) {
	var (
		r             Run
		status        string
		started, ends int64
		manifest      string
		verification  string
	)
	if err := row.Scan(&r.ID, &r.JobID, &started, &ends, &status, &r.Artifact, &r.SizeBytes, &r.Log, &r.Trigger, &manifest, &verification); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(manifest), &r.Manifest); err != nil {
		return nil, fmt.Errorf("backup manifest is malformed: %w", err)
	}
	if r.Manifest != nil && r.Manifest.Version == 0 {
		r.Manifest = nil
	}
	if err := json.Unmarshal([]byte(verification), &r.RestoreVerification); err != nil {
		return nil, errors.New("backup recovery evidence is malformed")
	}
	r.Status = RunStatus(status)
	r.StartedAt = time.Unix(started, 0).UTC()
	if ends > 0 {
		e := time.Unix(ends, 0).UTC()
		r.EndedAt = &e
		r.Duration = e.Sub(r.StartedAt).String()
	}
	return &r, nil
}

func (s *Store) Runs(ctx context.Context, jobID int64, limit int) ([]*Run, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT `+runCols+` FROM backup_runs WHERE job_id = ? ORDER BY started_at DESC, id DESC LIMIT ?`, jobID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) LastRun(ctx context.Context, jobID int64) (*Run, error) {
	row := s.st.DB.QueryRowContext(ctx,
		`SELECT `+runCols+` FROM backup_runs WHERE job_id = ? ORDER BY started_at DESC, id DESC LIMIT 1`, jobID)
	r, err := scanRun(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return r, err
}

func (s *Store) Run(ctx context.Context, runID int64) (*Run, error) {
	row := s.st.DB.QueryRowContext(ctx, `SELECT `+runCols+` FROM backup_runs WHERE id = ?`, runID)
	r, err := scanRun(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return r, err
}

// SuccessfulRuns is what retention prunes against: a failed run has no
// artifact worth counting or deleting.
func (s *Store) SuccessfulRuns(ctx context.Context, jobID int64) ([]*Run, error) {
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT `+runCols+` FROM backup_runs WHERE job_id = ? AND status = ? AND artifact != ''
		 ORDER BY started_at DESC, id DESC`, jobID, string(StatusSuccess))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) DeleteRun(ctx context.Context, runID int64) error {
	_, err := s.st.DB.ExecContext(ctx, `DELETE FROM backup_runs WHERE id = ?`, runID)
	return err
}
