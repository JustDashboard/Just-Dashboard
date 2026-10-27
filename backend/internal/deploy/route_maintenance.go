package deploy

import (
	"context"
	"database/sql"
	"errors"
)

// EnvironmentRouteName is the name an environment's public route has on the
// proxy, for an environment of this project that is not archived. The
// maintenance switch and the route's pages are found by it.
func (s *PlanningStore) EnvironmentRouteName(ctx context.Context, projectID, environmentID int64) (string, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM deploy_environments WHERE id = ? AND project_id = ? AND archived_at = 0`,
		environmentID, projectID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrEnvironmentNotFound
	}
	if err != nil {
		return "", err
	}
	return deploymentRouteName(id), nil
}
