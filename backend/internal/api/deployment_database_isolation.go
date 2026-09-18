package api

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// Resolve references before build credentials are exposed, not only when the
// runtime joins its network. Saved aliases of the same endpoint share a boundary.
func (o *deploymentDatabaseNetworks) checkEnvironmentIsolation(ctx context.Context, environmentID int64, conn *dbConnection, dsn string) error {
	var kind string
	if err := o.server.Store.DB.QueryRowContext(ctx, `SELECT kind FROM deploy_environments WHERE id=?`, environmentID).Scan(&kind); err != nil {
		return err
	}
	rows, err := o.server.Store.DB.QueryContext(ctx, `SELECT DISTINCT c.id,c.driver,c.dsn_enc FROM db_connections c
		JOIN (
		  SELECT environment_id,connection_id FROM deploy_database_bindings
		  UNION SELECT d.environment_id,linked.id FROM deploy_dependencies d JOIN db_connections linked
		  ON CAST(d.resource_id AS INTEGER)=linked.id OR d.resource_id=linked.name
		  WHERE d.release_id=0 AND (d.kind='database' OR d.resource_kind='database_connection')
		) links ON links.connection_id=c.id JOIN deploy_environments e ON e.id=links.environment_id
		WHERE e.id<>? AND (e.kind='preview' OR ?='preview')`, environmentID, kind)
	if err != nil {
		return err
	}
	type connection struct {
		id     int64
		driver dbx.Driver
		sealed string
	}
	var others []connection
	for rows.Next() {
		var other connection
		if err := rows.Scan(&other.id, &other.driver, &other.sealed); err != nil {
			rows.Close()
			return err
		}
		others = append(others, other)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	info, err := dbx.ParseDSN(conn.Driver, dsn)
	if err != nil {
		return errors.New("database isolation could not be verified")
	}
	for _, other := range others {
		if other.id == conn.ID {
			return deploy.ErrPreviewIsolation
		}
		if other.driver != conn.Driver {
			continue
		}
		otherDSN, err := o.server.Sealer.Open(other.sealed)
		if err != nil {
			return errors.New("database isolation could not be verified")
		}
		otherInfo, err := dbx.ParseDSN(other.driver, otherDSN)
		if err != nil {
			return errors.New("database isolation could not be verified")
		}
		sameEndpoint := strings.EqualFold(strings.TrimSuffix(info.Host, "."), strings.TrimSuffix(otherInfo.Host, ".")) && info.Port == otherInfo.Port
		if databaseLoopback(info.Host) && databaseLoopback(otherInfo.Host) && info.Port == otherInfo.Port {
			sameEndpoint = true
		}
		if conn.Driver == dbx.DriverSQLite {
			sameEndpoint = filepath.Clean(info.Database) == filepath.Clean(otherInfo.Database)
		}
		if sameEndpoint {
			return deploy.ErrPreviewIsolation
		}
	}
	return nil
}
