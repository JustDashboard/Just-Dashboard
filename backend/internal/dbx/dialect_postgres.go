package dbx

import (
	"context"
	"database/sql"
)

type postgresDialect struct{}

func (postgresDialect) Driver() Driver               { return DriverPostgres }
func (postgresDialect) SQLDriverName() string        { return "pgx" }
func (postgresDialect) NormaliseDSN(d string) string { return d }
func (postgresDialect) VersionQuery() string         { return "SELECT version()" }
func (postgresDialect) TunePool(*sql.DB)             {}
func (postgresDialect) SupportsReturning() bool      { return true }
func (postgresDialect) DefaultSchema() string        { return "public" }
func (postgresDialect) SupportsDDL() bool            { return true }

func (postgresDialect) QuoteIdent(name string) (string, error) { return quoteDouble(name) }

// Postgres numbers its bind markers, so the same value used twice must be
// passed twice unless the caller reuses the number deliberately.
func (postgresDialect) Placeholder(n int) string { return "$" + itoa(n) }

func (d postgresDialect) Paginate(limit, offset, argStart int) (string, []any) {
	return standardLimit(d, limit, offset, argStart)
}

func (postgresDialect) ColumnTypes() []string {
	return []string{
		"text", "varchar(255)", "char(1)", "boolean",
		"smallint", "integer", "bigint", "serial", "bigserial",
		"numeric(10,2)", "real", "double precision",
		"date", "time", "timestamp", "timestamptz", "interval",
		"json", "jsonb", "uuid", "bytea", "inet",
	}
}

func (postgresDialect) Databases(ctx context.Context, db *sql.DB) ([]Database, error) {
	rows, err := db.QueryContext(ctx, `SELECT d.datname,
	                pg_database_size(d.datname),
	                COALESCE(pg_get_userbyid(d.datdba), ''),
	                pg_encoding_to_char(d.encoding)
	         FROM pg_database d
	         WHERE NOT d.datistemplate
	         ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	return scanDatabases(rows)
}

// The catalogue snapshot can still contain a relation concurrently dropped
// by another session. PostgreSQL returns NULL for its size in that case.
const postgresTablesQuery = `SELECT n.nspname, c.relname,
	                CASE c.relkind WHEN 'r' THEN 'table' WHEN 'v' THEN 'view'
	                               WHEN 'm' THEN 'materialized view' ELSE c.relkind::text END,
	                COALESCE(c.reltuples::bigint, -1),
	                COALESCE(pg_total_relation_size(c.oid), 0),
	                COALESCE(obj_description(c.oid), '')
	         FROM pg_class c
	         JOIN pg_namespace n ON n.oid = c.relnamespace
	         WHERE c.relkind IN ('r','v','m','p')
	           AND n.nspname NOT IN ('pg_catalog','information_schema')
	           AND ($1 = '' OR n.nspname = $1)
	         ORDER BY 1, 2`

func (postgresDialect) Tables(ctx context.Context, db *sql.DB, schema string) ([]Table, error) {
	rows, err := db.QueryContext(ctx, postgresTablesQuery, schema)
	if err != nil {
		return nil, err
	}
	return scanTables(rows)
}

func (d postgresDialect) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error) {
	return infoSchemaColumns(ctx, db, d, schema, table)
}

func (d postgresDialect) PrimaryKey(ctx context.Context, db *sql.DB, schema, table string) ([]string, error) {
	return infoSchemaPrimaryKey(ctx, db, d, schema, table)
}

// Indexes reads pg_index rather than information_schema, which has no index
// view at all. unnest ... WITH ORDINALITY is what keeps a composite index's
// columns in their declared order rather than alphabetical.
func (postgresDialect) Indexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error) {
	rows, err := db.QueryContext(ctx, `SELECT i.relname, ix.indisunique, ix.indisprimary, a.attname
	          FROM pg_class t
	          JOIN pg_namespace n ON n.oid = t.relnamespace
	          JOIN pg_index ix ON t.oid = ix.indrelid
	          JOIN pg_class i ON i.oid = ix.indexrelid
	          JOIN unnest(ix.indkey) WITH ORDINALITY AS k(attnum, ord) ON true
	          JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
	          WHERE n.nspname = $1 AND t.relname = $2
	          ORDER BY i.relname, k.ord`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	acc := newIndexAcc()
	for rows.Next() {
		var name, col string
		var unique, primary bool
		if err := rows.Scan(&name, &unique, &primary, &col); err != nil {
			return nil, err
		}
		acc.add(name, col, unique, primary)
	}
	return acc.slice(), rows.Err()
}

func (postgresDialect) ForeignKeys(ctx context.Context, db *sql.DB, schema, table string) ([]ForeignKey, error) {
	rows, err := db.QueryContext(ctx, `SELECT con.conname, att.attname, nsp.nspname, cl.relname, att2.attname,
	                 con.confupdtype, con.confdeltype
	          FROM pg_constraint con
	          JOIN pg_class rel ON rel.oid = con.conrelid
	          JOIN pg_namespace rn ON rn.oid = rel.relnamespace
	          JOIN pg_class cl ON cl.oid = con.confrelid
	          JOIN pg_namespace nsp ON nsp.oid = cl.relnamespace
	          JOIN unnest(con.conkey) WITH ORDINALITY AS k(attnum, ord) ON true
	          JOIN pg_attribute att ON att.attrelid = con.conrelid AND att.attnum = k.attnum
	          JOIN unnest(con.confkey) WITH ORDINALITY AS fk(attnum, ord) ON fk.ord = k.ord
	          JOIN pg_attribute att2 ON att2.attrelid = con.confrelid AND att2.attnum = fk.attnum
	          WHERE con.contype = 'f' AND rn.nspname = $1 AND rel.relname = $2
	          ORDER BY con.conname, k.ord`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	acc := newFKAcc()
	for rows.Next() {
		var name, col, refSchema, refTable, refCol, upd, del string
		if err := rows.Scan(&name, &col, &refSchema, &refTable, &refCol, &upd, &del); err != nil {
			return nil, err
		}
		fk := acc.get(name)
		fk.Columns = append(fk.Columns, col)
		fk.RefSchema, fk.RefTable = refSchema, refTable
		fk.RefColumns = append(fk.RefColumns, refCol)
		fk.OnUpdate, fk.OnDelete = pgFKAction(upd), pgFKAction(del)
	}
	return acc.slice(), rows.Err()
}

// pgFKAction maps pg_constraint's single-char action codes to words.
func pgFKAction(c string) string {
	switch c {
	case "a":
		return "NO ACTION"
	case "r":
		return "RESTRICT"
	case "c":
		return "CASCADE"
	case "n":
		return "SET NULL"
	case "d":
		return "SET DEFAULT"
	default:
		return ""
	}
}

// Postgres keeps no canonical DDL text — pg_dump reconstructs it — so the
// statement is synthesised from what was introspected and labelled as generated
// where it is shown.
func (d postgresDialect) CreateSQL(_ context.Context, _ *sql.DB, schema, table string, detail *TableDetail) (string, error) {
	return synthCreateTable(d, schema, table, detail), nil
}

func (postgresDialect) CastText(e string) string { return "CAST(" + e + " AS TEXT)" }

func (postgresDialect) AddColumnKeyword() string { return "ADD COLUMN" }

func (postgresDialect) BeforeDropColumn(context.Context, *sql.DB, string, string, string) error {
	return nil
}

// EXPLAIN without ANALYZE plans the statement and does not run it.
func (postgresDialect) ExplainPlan(ctx context.Context, db *sql.DB, query string) (*QueryResult, error) {
	checked, checkErr := ExplainStatement(query)
	if checkErr != nil {
		return nil, checkErr
	}
	query = checked
	return RunQuery(ctx, db, "EXPLAIN "+query, 500)
}

// Activity reads pg_stat_activity, joined to itself through pg_blocking_pids so
// a blocked session names the one holding it. That join is the whole value:
// twenty sessions waiting and one culprit is a readable picture, twenty
// sessions "waiting" is not.
// clock_timestamp() rather than now(): now() is the *transaction* timestamp,
// which for the statement asking this question is a fraction of a millisecond
// before its own query_start — so the session doing the asking reported a
// negative age. Wall-clock time is what "how long has this been running" means.
func (postgresDialect) Activity(ctx context.Context, db *sql.DB) ([]Activity, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT a.pid::text,
	         COALESCE(a.usename, ''),
	         COALESCE(a.datname, ''),
	         COALESCE(a.state, ''),
	         GREATEST(COALESCE(EXTRACT(EPOCH FROM (clock_timestamp() - a.query_start)), 0), 0)::float8,
	         COALESCE(a.query, ''),
	         COALESCE(host(a.client_addr), ''),
	         COALESCE(a.wait_event_type || ':' || a.wait_event, ''),
	         COALESCE((SELECT string_agg(b::text, ',') FROM unnest(pg_blocking_pids(a.pid)) b), ''),
	         CASE WHEN a.pid = pg_backend_pid() THEN 1 ELSE 0 END
	  FROM pg_stat_activity a
	  WHERE a.backend_type = 'client backend'
	  ORDER BY a.query_start NULLS LAST`)
	if err != nil {
		return nil, err
	}
	return scanActivity(rows)
}

// Kill asks the backend to terminate. pg_terminate_backend rather than
// pg_cancel_backend: cancelling only stops the current statement and leaves an
// idle-in-transaction session still holding its locks, which is usually the
// thing the operator is actually trying to clear.
func (postgresDialect) Kill(ctx context.Context, db *sql.DB, pid string) error {
	if err := validatePID(pid); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, "SELECT pg_terminate_backend($1::int)", pid)
	return err
}

// reltuples is the planner's estimate, refreshed by ANALYZE. A table never
// analysed reports -1, which would render as a negative row count, so it is
// floored here rather than in the UI. Size functions return NULL if another
// session drops a relation still visible in this catalogue snapshot.
const postgresTableSizesQuery = `
		SELECT n.nspname, c.relname,
		       GREATEST(c.reltuples, 0)::bigint,
		       COALESCE(pg_total_relation_size(c.oid), 0),
		       COALESCE(pg_table_size(c.oid), 0),
		       COALESCE(pg_indexes_size(c.oid), 0)
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind IN ('r', 'p') AND n.nspname = $1`

func (postgresDialect) TableSizes(ctx context.Context, db *sql.DB, schema string) ([]TableSize, error) {
	if schema == "" {
		schema = "public"
	}
	rows, err := db.QueryContext(ctx, postgresTableSizesQuery, schema)
	if err != nil {
		return nil, err
	}
	return scanSizes(rows, schema)
}

// Postgres refuses to drop a database anything is connected to, and says so
// with "is being accessed by other users" rather than naming them. 13 added
// WITH (FORCE); terminating the backends first works on every version and is
// the same thing by hand.
func (d postgresDialect) DropDatabaseSQL(name string) ([]DropStatement, error) {
	q, err := d.QuoteIdent(name)
	if err != nil {
		return nil, err
	}
	return []DropStatement{
		{
			SQL: "SELECT pg_terminate_backend(pid) FROM pg_stat_activity " +
				"WHERE datname = " + d.Placeholder(1) + " AND pid <> pg_backend_pid()",
			Args: []any{name},
		},
		{SQL: "DROP DATABASE IF EXISTS " + q},
	}, nil
}

// A session cannot drop the database it is in, and the one database every
// Postgres install has is the maintenance one.
func (postgresDialect) AdminDatabase() string { return "postgres" }
