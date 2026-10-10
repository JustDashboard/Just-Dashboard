package netdiag

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const policyKey = "network.diagnostics.retention"

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) insert(ctx context.Context, run Run) error {
	data, err := json.Marshal(run)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO network_diagnostic_runs(id,name,job_id,status,created_at,updated_at,payload) VALUES(?,?,?,?,?,?,?)`,
		run.ID, run.Name, run.JobID, run.Status, run.CreatedAt.UnixMilli(), run.UpdatedAt.UnixMilli(), string(data))
	return err
}

func (s *Store) update(ctx context.Context, run Run) error {
	data, err := json.Marshal(run)
	if err != nil {
		return err
	}
	if len(data) > MaxExportBytes {
		return errors.New("diagnostic record exceeded its storage bound")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE network_diagnostic_runs SET job_id=?,status=?,updated_at=?,payload=? WHERE id=?`,
		run.JobID, run.Status, run.UpdatedAt.UnixMilli(), string(data), run.ID)
	return changed(res, err)
}

func changed(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) bindJob(ctx context.Context, id, jobID string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE network_diagnostic_runs SET job_id=? WHERE id=?`, jobID, id)
	return changed(res, err)
}

func (s *Store) Get(ctx context.Context, id string) (Run, error) {
	var run Run
	var name, jobID, status, data string
	var updated int64
	err := s.db.QueryRowContext(ctx, `SELECT name,job_id,status,updated_at,payload FROM network_diagnostic_runs WHERE id=?`, id).
		Scan(&name, &jobID, &status, &updated, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return run, ErrNotFound
	}
	if err != nil {
		return run, err
	}
	if err := json.Unmarshal([]byte(data), &run); err != nil {
		return run, fmt.Errorf("unreadable diagnostic record: %w", err)
	}
	run.Name, run.JobID, run.Status, run.UpdatedAt = name, jobID, status, time.UnixMilli(updated).UTC()
	return run, nil
}

func (s *Store) List(ctx context.Context) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM network_diagnostic_runs ORDER BY created_at DESC,id DESC`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	runs := make([]Run, 0, len(ids))
	for _, id := range ids {
		run, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func (s *Store) rename(ctx context.Context, id, name string, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE network_diagnostic_runs SET name=?,updated_at=? WHERE id=?`, name, now.UnixMilli(), id)
	return changed(res, err)
}

func (s *Store) remove(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM network_diagnostic_runs WHERE id=?`, id)
	return changed(res, err)
}

func (s *Store) retention(ctx context.Context) (Retention, error) {
	var data string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, policyKey).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultRetention, nil
	}
	if err != nil {
		return Retention{}, err
	}
	var policy Retention
	if err := json.Unmarshal([]byte(data), &policy); err != nil {
		return policy, err
	}
	return policy, policy.Validate()
}

func (s *Store) saveRetention(ctx context.Context, policy Retention) error {
	data, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, policyKey, string(data))
	return err
}

// Running work is never evicted. Retention is applied on reads, startup and
// completed writes. Reads suppress expired artifacts even without new runs;
// there is no additional background retention worker.
func (s *Store) prune(ctx context.Context, policy Retention, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM network_diagnostic_runs WHERE status IN ('completed','failed','cancelled','interrupted') AND
		(created_at < ? OR id IN (SELECT id FROM network_diagnostic_runs WHERE status IN ('completed','failed','cancelled','interrupted') ORDER BY created_at DESC,id DESC LIMIT -1 OFFSET ?))`,
		now.Add(-time.Duration(policy.MaxAgeHours)*time.Hour).UnixMilli(), policy.MaxRuns)
	return err
}
