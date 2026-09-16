package deploy

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

var (
	ErrArchiveRequired = errors.New("archive the deployment before deleting its records permanently")
	ErrNotFound        = errors.New("deploy project not found")
	ErrBadSignature    = errors.New("webhook signature does not match")
	ErrDisabled        = errors.New("this deploy hook is disabled")
)

type Store struct {
	st     *store.Store
	sealer *auth.Sealer
	// paths is the canonical symlink-aware resolver scoped to JD_DEPLOY_ROOTS.
	paths *files.Service
}

func NewStore(st *store.Store, sealer *auth.Sealer, roots []string) *Store {
	return &Store{st: st, sealer: sealer, paths: files.New(roots)}
}

const projectCols = `id, CASE WHEN archived_name != '' THEN archived_name ELSE name END, profile, repo_path, branch, compose_file, pre_command, post_command, hook_id, enabled, created_at, updated_at, archived_at`

func scanProject(row interface{ Scan(...any) error }) (*Project, error) {
	var (
		p                          Project
		enabled                    int
		created, updated, archived int64
	)
	if err := row.Scan(&p.ID, &p.Name, &p.Profile, &p.RepoPath, &p.Branch, &p.ComposeFile,
		&p.PreCommand, &p.PostCommand, &p.HookID, &enabled, &created, &updated, &archived); err != nil {
		return nil, err
	}
	p.Enabled = enabled == 1
	p.CreatedAt = time.Unix(created, 0).UTC()
	if updated == 0 {
		updated = created
	}
	p.UpdatedAt = time.Unix(updated, 0).UTC()
	p.ArchivedAt = unixTimePtr(archived)
	return &p, nil
}

func (s *Store) List(ctx context.Context) ([]*Project, error) {
	rows, err := s.st.DB.QueryContext(ctx, `SELECT `+projectCols+` FROM deploy_projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, p := range out {
		p.EnvVarCount = s.countEnv(ctx, p.ID)
		if last, err := s.LastRun(ctx, p.ID); err == nil {
			p.LastRun = last
		}
	}
	return out, nil
}

func (s *Store) Get(ctx context.Context, id int64) (*Project, error) {
	row := s.st.DB.QueryRowContext(ctx, `SELECT `+projectCols+` FROM deploy_projects WHERE id = ?`, id)
	p, err := scanProject(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.EnvVarCount = s.countEnv(ctx, p.ID)
	return p, nil
}

func (s *Store) ByHookID(ctx context.Context, hookID string) (*Project, error) {
	row := s.st.DB.QueryRowContext(ctx, `SELECT `+projectCols+` FROM deploy_projects WHERE hook_id = ?`, hookID)
	p, err := scanProject(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return p, err
}

func (s *Store) countEnv(ctx context.Context, projectID int64) int {
	var n int
	s.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_env WHERE project_id = ?`, projectID).Scan(&n)
	return n
}

// Create returns the webhook secret alongside the project. It is the only time
// the secret is available: only its sealed form is retained.
func (s *Store) Create(ctx context.Context, p *Project) (*Project, string, error) {
	if err := p.Validate(s.paths); err != nil {
		return nil, "", err
	}
	secret := auth.RandomToken(24)
	sealed, err := s.sealer.Seal(secret)
	if err != nil {
		return nil, "", err
	}
	hookID := auth.RandomToken(12)
	enabled := 1
	if !p.Enabled {
		enabled = 0
	}
	res, err := s.st.DB.ExecContext(ctx,
		`INSERT INTO deploy_projects(name, repo_path, branch, compose_file, pre_command, post_command, hook_secret, hook_id, enabled, created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`,
		p.Name, p.RepoPath, p.Branch, p.ComposeFile, p.PreCommand, p.PostCommand,
		sealed, hookID, enabled, time.Now().Unix())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, "", errors.New("a project with that name already exists")
		}
		return nil, "", err
	}
	id, _ := res.LastInsertId()
	created, err := s.Get(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if err := s.st.NormalizeLegacyDeployments(ctx); err != nil {
		return nil, "", err
	}
	return created, secret, nil
}

func (s *Store) Update(ctx context.Context, id int64, p *Project) (*Project, error) {
	if err := p.Validate(s.paths); err != nil {
		return nil, err
	}
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	enabled := 0
	if p.Enabled {
		enabled = 1
	}
	_, err := s.st.DB.ExecContext(ctx,
		`UPDATE deploy_projects SET name = ?, repo_path = ?, branch = ?, compose_file = ?,
		 pre_command = ?, post_command = ?, enabled = ? WHERE id = ?`,
		p.Name, p.RepoPath, p.Branch, p.ComposeFile, p.PreCommand, p.PostCommand, enabled, id)
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *Store) Delete(ctx context.Context, id int64) error {
	_, err := s.st.DB.ExecContext(ctx, `DELETE FROM deploy_projects WHERE id = ?`, id)
	return err
}

// Archive is the default deployment lifecycle action. It disables every
// trigger but deliberately leaves runtime, release, artifact, route and data
// ownership rows intact; removing managed resources is separately previewed.
func (s *Store) Archive(ctx context.Context, id int64) (*Project, error) {
	tx, err := s.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Unix()
	result, err := tx.ExecContext(ctx, `
		UPDATE deploy_projects SET
		       archived_name = CASE WHEN archived_name = '' THEN name ELSE archived_name END,
		       name = CASE WHEN archived_name = '' THEN '__jd_archived_' || id || '_' || lower(hex(randomblob(16))) ELSE name END,
		       archived_at = CASE WHEN archived_at = 0 THEN ? ELSE archived_at END,
		       enabled = 0, updated_at = ? WHERE id = ?`, now, now, id)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE deploy_triggers SET enabled = 0, updated_at = ?
		 WHERE environment_id IN (SELECT id FROM deploy_environments WHERE project_id = ?)`, now, id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// RotateSecret issues a new webhook secret, invalidating the old one.
func (s *Store) RotateSecret(ctx context.Context, id int64) (string, error) {
	secret := auth.RandomToken(24)
	sealed, err := s.sealer.Seal(secret)
	if err != nil {
		return "", err
	}
	res, err := s.st.DB.ExecContext(ctx, `UPDATE deploy_projects SET hook_secret = ? WHERE id = ?`, sealed, id)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrNotFound
	}
	return secret, nil
}

// VerifySignature checks a GitHub-style HMAC over the raw request body. The
// comparison is constant time so the signature cannot be discovered by timing
// repeated requests.
func (s *Store) VerifySignature(ctx context.Context, projectID int64, body []byte, presented string) error {
	var sealed string
	err := s.st.DB.QueryRowContext(ctx, `SELECT hook_secret FROM deploy_projects WHERE id = ?`, projectID).Scan(&sealed)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	secret, err := s.sealer.Open(sealed)
	if err != nil {
		return err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(strings.TrimSpace(presented))) {
		return ErrBadSignature
	}
	return nil
}

// --- environment variables ---

func (s *Store) ListEnv(ctx context.Context, projectID int64, reveal bool) ([]EnvVar, error) {
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT key, value_enc, updated_at FROM deploy_env WHERE project_id = ? ORDER BY key`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EnvVar{}
	for rows.Next() {
		var (
			key, sealed string
			updated     int64
		)
		if err := rows.Scan(&key, &sealed, &updated); err != nil {
			return nil, err
		}
		value, err := s.sealer.Open(sealed)
		if err != nil {
			return nil, err
		}
		v := EnvVar{Key: key, UpdatedAt: time.Unix(updated, 0).UTC(), Masked: mask(value)}
		// Revealing is a separate, audited action rather than the default,
		// so a list view never puts secrets on screen by accident.
		if reveal {
			v.Value = value
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) SetEnv(ctx context.Context, projectID int64, key, value string) error {
	if err := ValidateEnvKey(key); err != nil {
		return err
	}
	sealed, err := s.sealer.Seal(value)
	if err != nil {
		return err
	}
	_, err = s.st.DB.ExecContext(ctx,
		`INSERT INTO deploy_env(project_id, key, value_enc, updated_at) VALUES(?,?,?,?)
		 ON CONFLICT(project_id, key) DO UPDATE SET value_enc = excluded.value_enc, updated_at = excluded.updated_at`,
		projectID, key, sealed, time.Now().Unix())
	return err
}

func (s *Store) DeleteEnv(ctx context.Context, projectID int64, key string) error {
	_, err := s.st.DB.ExecContext(ctx, `DELETE FROM deploy_env WHERE project_id = ? AND key = ?`, projectID, key)
	return err
}

// EnvMap is what the deployer writes into the project's .env file.
func (s *Store) EnvMap(ctx context.Context, projectID int64) (map[string]string, error) {
	vars, err := s.ListEnv(ctx, projectID, true)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(vars))
	for _, v := range vars {
		out[v.Key] = v.Value
	}
	return out, nil
}

// --- runs ---

const runCols = `id, run_number, project_id, started_at, ended_at, status, trigger, actor, from_commit, to_commit, log`

func scanRun(row interface{ Scan(...any) error }) (*Run, error) {
	var (
		r             Run
		status        string
		started, ends int64
	)
	if err := row.Scan(&r.ID, &r.RunNumber, &r.ProjectID, &started, &ends, &status,
		&r.Trigger, &r.Actor, &r.FromCommit, &r.ToCommit, &r.Log); err != nil {
		return nil, err
	}
	r.Status = RunStatus(status)
	r.StartedAt = time.Unix(started, 0).UTC()
	if ends > 0 {
		e := time.Unix(ends, 0).UTC()
		r.EndedAt = &e
		r.Duration = e.Sub(r.StartedAt).String()
	}
	// Only a successful deployment leaves a commit worth returning to.
	r.Rollbackable = r.Status == StatusSuccess && r.FromCommit != "" && r.FromCommit != r.ToCommit
	return &r, nil
}

func (s *Store) StartRun(ctx context.Context, projectID int64, trigger, actor, fromCommit string) (int64, error) {
	res, err := s.st.DB.ExecContext(ctx,
		`INSERT INTO deploy_runs(project_id, started_at, status, trigger, actor, from_commit) VALUES(?,?,?,?,?,?)`,
		projectID, time.Now().Unix(), string(StatusRunning), trigger, actor, fromCommit)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) FinishRun(ctx context.Context, runID int64, status RunStatus, toCommit, log string) error {
	_, err := s.st.DB.ExecContext(ctx,
		`UPDATE deploy_runs SET ended_at = ?, status = ?, to_commit = ?, log = ? WHERE id = ?`,
		time.Now().Unix(), string(status), toCommit, truncate(log, 64000), runID)
	return err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func (s *Store) Runs(ctx context.Context, projectID int64, limit int) ([]*Run, error) {
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT `+runCols+` FROM deploy_runs WHERE project_id = ? ORDER BY started_at DESC LIMIT ?`,
		projectID, limit)
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

func (s *Store) Run(ctx context.Context, runID int64) (*Run, error) {
	row := s.st.DB.QueryRowContext(ctx, `SELECT `+runCols+` FROM deploy_runs WHERE id = ?`, runID)
	r, err := scanRun(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return r, err
}

func (s *Store) LastRun(ctx context.Context, projectID int64) (*Run, error) {
	row := s.st.DB.QueryRowContext(ctx,
		`SELECT `+runCols+` FROM deploy_runs WHERE project_id = ? ORDER BY started_at DESC LIMIT 1`, projectID)
	r, err := scanRun(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return r, err
}

// PurgeArchived forgets dashboard records, never host resources. Runs go first
// because their variable revision joins intentionally do not cascade from variables.
func (s *Store) PurgeArchived(ctx context.Context, id int64) (*Project, error) {
	tx, err := s.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	project, err := scanProject(tx.QueryRowContext(ctx, `SELECT `+projectCols+` FROM deploy_projects WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if project.ArchivedAt == nil {
		return nil, ErrArchiveRequired
	}
	var active int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_runs WHERE project_id = ? AND
 ((state = '' AND status = 'running') OR (state <> '' AND state NOT IN ('succeeded','failed','cancelled','rolled_back','superseded')))`, id).Scan(&active)
	if err != nil {
		return nil, err
	}
	if active > 0 {
		return nil, ErrAlreadyDeploying
	}
	for _, query := range []string{
		`DELETE FROM deploy_notification_deliveries WHERE run_id IN (SELECT id FROM deploy_runs WHERE project_id = ?)`,
		`DELETE FROM deploy_drafts WHERE committed_project_id = ?`,
		`DELETE FROM deploy_runs WHERE project_id = ?`,
		`DELETE FROM deploy_projects WHERE id = ?`,
	} {
		if _, err = tx.ExecContext(ctx, query, id); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return project, nil
}
