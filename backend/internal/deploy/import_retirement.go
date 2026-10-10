package deploy

import (
	"context"
	"database/sql"
	"fmt"
)

// Retired imports keep their records and runtime intact. The older engine
// cannot reproduce their baseline, sealed values or startup ownership safely.
func RequireSupportedProject(ctx context.Context, db *sql.DB, projectID int64) error {
	var retired bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS(
   SELECT 1 FROM deploy_runs WHERE project_id = ? AND operation = 'import_adopt'
   UNION ALL
   SELECT 1 FROM deploy_sources src JOIN deploy_environments env ON env.id = src.environment_id
   WHERE env.project_id = ? AND src.kind = 'import'
     AND COALESCE(json_extract(src.config_json, '$.mode'), '') <> 'existing_checkout'
 )`, projectID, projectID).Scan(&retired)
	if err != nil {
		return err
	}
	if retired {
		return fmt.Errorf("%w: existing-workload import has been removed; this imported project is read-only; manage its runtime with the original tools or restore an import-capable version", ErrInvalidPlan)
	}
	return nil
}
