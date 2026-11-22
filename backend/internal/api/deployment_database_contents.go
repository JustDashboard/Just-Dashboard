package api

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// deploymentDatabaseContents tells a deployment whether a linked database is
// new — no row of the application's in any table — before a build that needs
// rows loads the project's seed into it (deploy/build_database.go). A seed
// usually resets the tables it fills, so the answer errs one way only: an
// engine this cannot read, a database with more tables than it looks at, or
// any doubt, is a database that holds rows.
type deploymentDatabaseContents struct{ server *Server }

// databaseBookkeepingTables are the tables a schema step itself writes, which
// a database's being new does not depend on: the migration tools' records,
// Django's content types and permissions (its migrate fills them), and
// PostGIS's reference systems (the extension fills them).
var databaseBookkeepingTables = map[string]bool{
	"__drizzle_migrations": true, "_prisma_migrations": true, "knex_migrations": true, "knex_migrations_lock": true,
	"sequelizemeta": true, "schema_migrations": true, "ar_internal_metadata": true, "django_migrations": true,
	"django_content_type": true, "auth_permission": true, "mikro_orm_migrations": true, "migrations": true,
	"typeorm_metadata": true, "adonis_schema": true, "adonis_schema_versions": true, "goose_db_version": true,
	"flyway_schema_history": true, "atlas_schema_revisions": true, "spatial_ref_sys": true,
	"__efmigrationshistory": true, "doctrine_migration_versions": true, "phinxlog": true, "alembic_version": true,
}

// databaseBookkeepingSchemas hold only bookkeeping: Drizzle's migration
// records and the PostGIS extensions' own data.
var databaseBookkeepingSchemas = map[string]bool{"drizzle": true, "topology": true, "tiger": true, "tiger_data": true}

// databaseContentsTableLimit bounds the tables looked at; a database with
// more is not new.
const databaseContentsTableLimit = 500

func (c deploymentDatabaseContents) DatabaseHasRows(ctx context.Context, connectionID int64) (bool, error) {
	s := c.server
	if s == nil || s.modules.dbs == nil {
		return true, errors.New("databases are unavailable on this server")
	}
	conn, dsn, err := s.dbConnRow(ctx, connectionID)
	if err != nil {
		return true, err
	}
	probe, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var list string
	switch conn.Driver {
	case dbx.DriverPostgres:
		list = `SELECT format('%I.%I', schemaname, tablename), schemaname, tablename FROM pg_tables
			WHERE schemaname NOT IN ('pg_catalog', 'information_schema') AND schemaname NOT LIKE 'pg\_toast%' AND schemaname NOT LIKE 'pg\_temp%'
			ORDER BY 1 LIMIT 501`
	case dbx.DriverMySQL:
		list = `SELECT CONCAT('` + "`" + `', REPLACE(table_name, '` + "`" + `', '` + "``" + `'), '` + "`" + `'), table_schema, table_name
			FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE' ORDER BY 1 LIMIT 501`
	default:
		return true, nil
	}
	pool, err := s.modules.dbs.Pool(probe, conn.ID, conn.Driver, dsn)
	if err != nil {
		return true, err
	}
	tables, total, err := databaseContentTables(probe, pool, list)
	if err != nil {
		return true, err
	}
	if total > databaseContentsTableLimit {
		return true, nil
	}
	for _, table := range tables {
		var present bool
		// The name is the database's own quoting of its own catalogue entry.
		if err := pool.QueryRowContext(probe, `SELECT EXISTS (SELECT 1 FROM `+table+`)`).Scan(&present); err != nil {
			return true, err
		}
		if present {
			return true, nil
		}
	}
	return false, nil
}

// databaseContentTables are the quoted names of the application's tables,
// bookkeeping left out, and how many tables the listing returned in all.
func databaseContentTables(ctx context.Context, pool *sql.DB, list string) ([]string, int, error) {
	rows, err := pool.QueryContext(ctx, list)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	tables, total := []string{}, 0
	for rows.Next() {
		var quoted, schema, name string
		if err := rows.Scan(&quoted, &schema, &name); err != nil {
			return nil, 0, err
		}
		total++
		if databaseBookkeepingSchemas[strings.ToLower(schema)] || databaseBookkeepingTables[strings.ToLower(name)] {
			continue
		}
		tables = append(tables, quoted)
	}
	return tables, total, rows.Err()
}
