package api

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
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
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT b.connection_id,c.name,c.driver,n.network_name,b.status,b.detail,b.checked_at,
		EXISTS(SELECT 1 FROM db_poolers p WHERE p.connection_id=b.connection_id) FROM deploy_database_bindings b
		JOIN deploy_database_networks n ON n.environment_id=b.environment_id JOIN db_connections c ON c.id=b.connection_id WHERE b.environment_id=? ORDER BY c.name`, environmentID)
	if err != nil {
		return httpx.Internal(err)
	}
	defer rows.Close()
	items := []databaseLink{}
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
		var item databaseLink
		var checked int64
		if err := rows.Scan(&item.ConnectionID, &item.Name, &item.Driver, &item.Network, &item.Status, &item.Detail, &checked, &item.Pooler); err != nil {
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

// handleDeploymentDatabaseAddresses reports the saved databases the
// environment's variables name by address rather than link: an application
// handed postgres://…@203.0.113.7:5435/app reaches the container this server
// publishes on 5435, and nothing the links report says so. The variables are
// matched with the address book the database map reads containers' environments
// with, and only their names leave the server.
func (s *Server) handleDeploymentDatabaseAddresses(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	values, err := s.modules.deployPlanning.LiteralVariableValues(r.Context(), projectID, environmentID)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	items := []databaseAddress{}
	if len(values) == 0 {
		httpx.JSON(w, http.StatusOK, items)
		return nil
	}
	env := make([]string, 0, len(values))
	for name, value := range values {
		env = append(env, name+"="+value)
	}
	sort.Strings(env)
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	ctx = s.modules.docker.WithReadSnapshot(ctx)
	conns, err := s.existingConnections(ctx)
	if err != nil {
		return err
	}
	hosts := hostAddresses()
	for _, conn := range conns {
		if conn.Driver == dbx.DriverSQLite {
			continue
		}
		_, dsn, err := s.dbConnRow(ctx, conn.ID)
		if err != nil {
			continue
		}
		info, err := dbx.ParseDSN(conn.Driver, dsn)
		if err != nil || info == nil {
			continue
		}
		port, _ := strconv.Atoi(info.Port)
		server := s.serverBehind(ctx, conn, info)
		var serverIPs []string
		if server != nil {
			if detail, err := s.modules.docker.Inspect(ctx, server.container.ID); err == nil {
				serverIPs = containerIPs(detail)
			}
		}
		named := topoAddresses(conn, info, port, server, serverIPs, hosts).naming(env)
		if len(named) == 0 {
			continue
		}
		items = append(items, databaseAddress{
			ConnectionID: conn.ID, Name: conn.Name, Driver: string(conn.Driver),
			Database: conn.Database, Variables: named,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	httpx.JSON(w, http.StatusOK, items)
	return nil
}

// databaseLink is one database an environment reads, as the links route lists it.
type databaseLink struct {
	ConnectionID int64  `json:"connectionId"`
	Name         string `json:"name"`
	Driver       string `json:"driver"`
	Database     string `json:"database"`
	Network      string `json:"network"`
	Hostname     string `json:"hostname"`
	// Pooler is whether the connection has a connection pooler, which
	// the pooled form of its reference (${{database.N.pooled}}) reaches.
	Pooler bool   `json:"pooler"`
	Status string `json:"status"`
	// Detail is why the last reconciliation could not repair the binding.
	// Empty while it is connected.
	Detail    string     `json:"detail,omitempty"`
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
}

// databaseAddress is where an environment reaches one linked database.
type databaseAddress struct {
	ConnectionID int64  `json:"connectionId"`
	Name         string `json:"name"`
	Driver       string `json:"driver"`
	Database     string `json:"database"`
	// Variables are the names whose values hold its address.
	Variables []string `json:"variables"`
}
