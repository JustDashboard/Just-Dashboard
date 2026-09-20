package api

import (
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

func (s *Server) handleDeploymentDatabaseLinks(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	if _, err := s.modules.deployRuns.EnvironmentExecutionTarget(r.Context(), projectID, environmentID); err != nil {
		return mapDeployError(err)
	}
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT b.connection_id,c.name,c.driver,n.network_name,b.status,b.detail,b.checked_at FROM deploy_database_bindings b
		JOIN deploy_database_networks n ON n.environment_id=b.environment_id JOIN db_connections c ON c.id=b.connection_id WHERE b.environment_id=? ORDER BY c.name`, environmentID)
	if err != nil {
		return httpx.Internal(err)
	}
	defer rows.Close()
	type link struct {
		ConnectionID int64  `json:"connectionId"`
		Name         string `json:"name"`
		Driver       string `json:"driver"`
		Database     string `json:"database"`
		Network      string `json:"network"`
		Hostname     string `json:"hostname"`
		Status       string `json:"status"`
		// Detail is why the last reconciliation could not repair the binding.
		// Empty while it is connected.
		Detail    string     `json:"detail,omitempty"`
		CheckedAt *time.Time `json:"checkedAt,omitempty"`
	}
	items := []link{}
	// The database a connection opens is inside the sealed DSN rather than in
	// a column of its own, so it is read the way every other database route
	// reads it. A connection whose DSN no longer resolves against the current
	// roots keeps its row without the name, rather than failing the report.
	databaseName := func(id int64) string {
		conn, _, err := s.dbConnRow(r.Context(), id)
		if err != nil {
			return ""
		}
		return conn.Database
	}
	for rows.Next() {
		var item link
		var checked int64
		if err := rows.Scan(&item.ConnectionID, &item.Name, &item.Driver, &item.Network, &item.Status, &item.Detail, &checked); err != nil {
			return httpx.Internal(err)
		}
		item.Hostname = databaseDNSName(item.ConnectionID)
		item.Database = databaseName(item.ConnectionID)
		if checked > 0 {
			stamp := time.Unix(checked, 0).UTC()
			item.CheckedAt = &stamp
			if time.Since(stamp) > 30*time.Second {
				item.Status = "stale"
			}
		}
		// A connected binding keeps no reason; a stale one's reason belongs to
		// the pass that last succeeded, and reporting it beside "stale" reads
		// as the cause of the staleness rather than as history.
		if item.Status == "connected" || item.Status == "stale" {
			item.Detail = ""
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, items)
	return nil
}
