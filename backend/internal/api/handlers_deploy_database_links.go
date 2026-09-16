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
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT b.connection_id,c.name,n.network_name,b.status,b.checked_at FROM deploy_database_bindings b
		JOIN deploy_database_networks n ON n.environment_id=b.environment_id JOIN db_connections c ON c.id=b.connection_id WHERE b.environment_id=? ORDER BY c.name`, environmentID)
	if err != nil {
		return httpx.Internal(err)
	}
	defer rows.Close()
	type link struct {
		ConnectionID int64      `json:"connectionId"`
		Name         string     `json:"name"`
		Network      string     `json:"network"`
		Hostname     string     `json:"hostname"`
		Status       string     `json:"status"`
		CheckedAt    *time.Time `json:"checkedAt,omitempty"`
	}
	items := []link{}
	for rows.Next() {
		var item link
		var checked int64
		if err := rows.Scan(&item.ConnectionID, &item.Name, &item.Network, &item.Status, &checked); err != nil {
			return httpx.Internal(err)
		}
		item.Hostname = databaseDNSName(item.ConnectionID)
		if checked > 0 {
			stamp := time.Unix(checked, 0).UTC()
			item.CheckedAt = &stamp
			if time.Since(stamp) > 30*time.Second {
				item.Status = "stale"
			}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, items)
	return nil
}
