package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// Some automation steps execute through their feature owner before a run is
// queued. Check ownership at those boundaries as well as at queue admission.
func (s *Server) requireManagedDeploymentAutomation(ctx context.Context, projectID, environmentID int64) error {
	var kind, configuration string
	err := s.Store.DB.QueryRowContext(ctx, `
		SELECT COALESCE(src.kind, ''), COALESCE(src.config_json, '{}')
		  FROM deploy_environments e
		  LEFT JOIN deploy_sources src ON src.environment_id = e.id AND src.revision = e.desired_revision
		 WHERE e.id = ? AND e.project_id = ? AND e.archived_at = 0`, environmentID, projectID).
		Scan(&kind, &configuration)
	if errors.Is(err, sql.ErrNoRows) {
		return deploy.ErrEnvironmentNotFound
	}
	if err != nil {
		return err
	}
	if kind != string(deploy.SourceImport) {
		return nil
	}
	var source deploy.DraftSourceConfig
	if json.Unmarshal([]byte(configuration), &source) == nil && source.Mode == deploy.SourceModeExistingCheckout {
		return nil
	}
	return fmt.Errorf("%w: imported workloads keep automation with their original manager", deploy.ErrInvalidPlan)
}
