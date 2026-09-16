package deploy

import (
	"context"
	"errors"
)

// Closing a PR cancels queued/build work while letting an atomic cutover settle.
// The subsequent removal still waits on the same environment lease.
func (s *OrchestrationStore) CancelPreviewWork(ctx context.Context, environmentID int64) error {
	var kind EnvironmentKind
	if err := s.db.QueryRowContext(ctx, `SELECT kind FROM deploy_environments WHERE id=?`, environmentID).Scan(&kind); err != nil {
		return err
	}
	if kind != EnvironmentPreview {
		return ErrPreviewIsolation
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM deploy_runs WHERE environment_id=? AND operation<>'preview_remove' AND state NOT IN ('succeeded','failed','cancelled','rolled_back','superseded')`, environmentID)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := s.RequestCancellation(ctx, id); err != nil && !errors.Is(err, ErrRunNotCancellable) && !errors.Is(err, ErrRunTerminal) {
			return err
		}
	}
	return nil
}
