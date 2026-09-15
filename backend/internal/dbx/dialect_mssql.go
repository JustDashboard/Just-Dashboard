package dbx

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"time"
)

type mssqlDialect struct{}

func (mssqlDialect) Driver() Driver                      { return DriverMSSQL }
func (mssqlDialect) SQLDriverName() string               { return "sqlserver" }
func (mssqlDialect) NormaliseDSN(d string) string        { return d }
func (mssqlDialect) VersionQuery() string                { return "SELECT @@VERSION" }
func (mssqlDialect) TunePool(*sql.DB)                    {}
func (mssqlDialect) DefaultSchema() string               { return "dbo" }
func (mssqlDialect) SupportsDDL() bool                   { return true }
func (mssqlDialect) Placeholder(n int) string            { return "@p" + itoa(n) }
func (mssqlDialect) QuoteIdent(s string) (string, error) { return quoteBracket(s) }

// SQL Server's OUTPUT clause is its RETURNING, but it sits in a different place
// in the statement and breaks on tables carrying triggers. The row editors fall
// back to reporting what they affected, which is always correct.
func (mssqlDialect) SupportsReturning() bool { return false }

// Paginate supplies the ORDER BY that SQL Server requires in front of OFFSET.
// `(SELECT NULL)` is the documented way to satisfy that requirement without
// asking the engine to actually sort, which is what keeps a browse page cheap
// on a table with no useful index.
func (d mssqlDialect) Paginate(limit, offset, argStart int) (string, []any) {
	clause, args := fetchFirstLimit(d, limit, offset, argStart)
	return "ORDER BY (SELECT NULL) " + clause, args
}

func (mssqlDialect) ColumnTypes() []string {
	return []string{
		"nvarchar(255)", "nvarchar(max)", "varchar(255)", "char(1)", "bit",
		"tinyint", "smallint", "int", "bigint",
		"decimal(18,2)", "numeric(18,2)", "float", "real", "money",
		"date", "time", "datetime2", "datetimeoffset",
		"uniqueidentifier", "varbinary(max)", "xml",
	}
}

// Databases lists user databases, plus whichever one this connection is
// actually using.
//
// The system databases are noise and stay hidden — but a fresh instance has no
// user databases at all, and hiding master too left the picker empty while the
// operator was demonstrably connected to something. An empty list that is not
// the truth is worse than one extra row.
func (mssqlDialect) Databases(ctx context.Context, db *sql.DB) ([]Database, error) {
	rows, err := db.QueryContext(ctx, `SELECT d.name,
	              ISNULL(CAST(SUM(CAST(mf.size AS BIGINT)) * 8192 AS BIGINT), 0),
	              ISNULL(SUSER_SNAME(d.owner_sid), ''),
	              ISNULL(d.collation_name, '')
	       FROM sys.databases d
	       LEFT JOIN sys.master_files mf ON mf.database_id = d.database_id
	       WHERE d.database_id > 4 OR d.name = DB_NAME()
	       GROUP BY d.name, d.owner_sid, d.collation_name
	       ORDER BY d.name`)
	if err != nil {
		return nil, err
	}
	return scanDatabases(rows)
}

// Tables reads the catalogue views rather than information_schema so that row
// counts and on-disk size come back in the same pass. Both subqueries use only
// catalogue views, which need no VIEW DATABASE STATE grant — the dynamic
// management views that would be tidier do, and failing the whole listing for a
// least-privilege login is worse than an approximate size.
func (mssqlDialect) Tables(ctx context.Context, db *sql.DB, schema string) ([]Table, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT s.name, t.name, 'table',
	         ISNULL((SELECT SUM(p.rows) FROM sys.partitions p
	                  WHERE p.object_id = t.object_id AND p.index_id IN (0,1)), 0),
	         ISNULL((SELECT SUM(au.total_pages) * 8192 FROM sys.allocation_units au
	                  JOIN sys.partitions p2 ON p2.partition_id = au.container_id
	                 WHERE p2.object_id = t.object_id), 0),
	         ISNULL(CAST(ep.value AS NVARCHAR(4000)), '')
	  FROM sys.tables t
	  JOIN sys.schemas s ON s.schema_id = t.schema_id
	  LEFT JOIN sys.extended_properties ep
	         ON ep.major_id = t.object_id AND ep.minor_id = 0 AND ep.name = 'MS_Description'
	  WHERE (@p1 = '' OR s.name = @p1)
	  UNION ALL
	  SELECT s.name, v.name, 'view', 0, 0, ''
	  FROM sys.views v
	  JOIN sys.schemas s ON s.schema_id = v.schema_id
	  WHERE (@p1 = '' OR s.name = @p1)
	  ORDER BY 1, 2`, schema)
	if err != nil {
		return nil, err
	}
	return scanTables(rows)
}

func (d mssqlDialect) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error) {
	return infoSchemaColumns(ctx, db, d, schema, table)
}

func (d mssqlDialect) PrimaryKey(ctx context.Context, db *sql.DB, schema, table string) ([]string, error) {
	return infoSchemaPrimaryKey(ctx, db, d, schema, table)
}

func (mssqlDialect) Indexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT i.name, i.is_unique, i.is_primary_key, c.name
	  FROM sys.indexes i
	  JOIN sys.objects o ON o.object_id = i.object_id
	  JOIN sys.schemas s ON s.schema_id = o.schema_id
	  JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id
	  JOIN sys.columns c ON c.object_id = i.object_id AND c.column_id = ic.column_id
	  WHERE s.name = @p1 AND o.name = @p2 AND i.name IS NOT NULL
	  ORDER BY i.name, ic.key_ordinal`, schema, table)
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

func (mssqlDialect) ForeignKeys(ctx context.Context, db *sql.DB, schema, table string) ([]ForeignKey, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT fk.name, pc.name, rs.name, rt.name, rc.name,
	         fk.update_referential_action_desc, fk.delete_referential_action_desc
	  FROM sys.foreign_keys fk
	  JOIN sys.tables pt ON pt.object_id = fk.parent_object_id
	  JOIN sys.schemas ps ON ps.schema_id = pt.schema_id
	  JOIN sys.foreign_key_columns fkc ON fkc.constraint_object_id = fk.object_id
	  JOIN sys.columns pc ON pc.object_id = fkc.parent_object_id AND pc.column_id = fkc.parent_column_id
	  JOIN sys.tables rt ON rt.object_id = fk.referenced_object_id
	  JOIN sys.schemas rs ON rs.schema_id = rt.schema_id
	  JOIN sys.columns rc ON rc.object_id = fkc.referenced_object_id AND rc.column_id = fkc.referenced_column_id
	  WHERE ps.name = @p1 AND pt.name = @p2
	  ORDER BY fk.name, fkc.constraint_column_id`, schema, table)
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
		fk.OnUpdate, fk.OnDelete = underscoreToWords(upd), underscoreToWords(del)
	}
	return acc.slice(), rows.Err()
}

// SQL Server has no SHOW CREATE TABLE; scripting one out is an SMO operation,
// not a query, so the statement is synthesised from the introspected structure.
func (d mssqlDialect) CreateSQL(_ context.Context, _ *sql.DB, schema, table string, detail *TableDetail) (string, error) {
	return synthCreateTable(d, schema, table, detail), nil
}

func (mssqlDialect) CastText(e string) string { return "CAST(" + e + " AS NVARCHAR(MAX))" }

// SQL Server rejects the COLUMN keyword after ADD.
func (mssqlDialect) AddColumnKeyword() string { return "ADD" }

// BeforeDropColumn removes the column's default constraint.
//
// SQL Server materialises `DEFAULT 0` as a separate constraint object and then
// refuses to drop the column while that object references it — "one or more
// objects access this column", which tells the operator nothing about what to
// do next. The constraint is an implementation detail of the column being
// dropped, so clearing it here is not overreach: it is the rest of the same
// action. Anything else still holding the column (an index, a check
// constraint) is a real dependency and its error is left to surface.
func (d mssqlDialect) BeforeDropColumn(ctx context.Context, db *sql.DB, schema, table, column string) error {
	rows, err := db.QueryContext(ctx, `
	  SELECT dc.name
	  FROM sys.default_constraints dc
	  JOIN sys.columns c ON c.object_id = dc.parent_object_id AND c.column_id = dc.parent_column_id
	  JOIN sys.objects o ON o.object_id = dc.parent_object_id
	  JOIN sys.schemas s ON s.schema_id = o.schema_id
	  WHERE s.name = @p1 AND o.name = @p2 AND c.name = @p3`, schema, table, column)
	if err != nil {
		return err
	}
	names := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		names = append(names, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	rel, err := qualify(d, schema, table)
	if err != nil {
		return err
	}
	for _, n := range names {
		q, err := d.QuoteIdent(n)
		if err != nil {
			continue
		}
		if _, err := db.ExecContext(ctx, "ALTER TABLE "+rel+" DROP CONSTRAINT "+q); err != nil {
			return err
		}
	}
	return nil
}

// ExplainPlan uses SHOWPLAN_ALL, which is a session mode rather than a
// statement prefix: with it on, the server plans everything it is sent and
// executes none of it.
//
// That makes the mode itself the hazard — a connection left in it would
// silently stop running the operator's queries. So this takes a single
// connection out of the pool, turns the mode on and off around the one
// statement, and closes it, which guarantees no pooled connection can be
// handed back still in plan-only mode.
func (mssqlDialect) ExplainPlan(ctx context.Context, db *sql.DB, query string) (result *QueryResult, err error) {
	checked, checkErr := ExplainStatement(query)
	if checkErr != nil {
		return nil, checkErr
	}
	query = checked
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "SET SHOWPLAN_ALL ON"); err != nil {
		// A transport error can arrive after the server changed its mode.
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		return nil, err
	}
	defer func() {
		// Cancellation must not return a plan-only connection to the pool.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, resetErr := conn.ExecContext(cleanup, "SET SHOWPLAN_ALL OFF"); resetErr != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			result = nil
			err = errors.Join(err, resetErr)
		}
	}()
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	res, err := collectRows(rows, 500, query)
	rows.Close()
	return res, err
}

// Activity joins the request and session views, and reports blocking_session_id
// directly — SQL Server is the one engine here that hands the blocking graph
// over without a recursive query.
func (mssqlDialect) Activity(ctx context.Context, db *sql.DB) ([]Activity, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT CAST(r.session_id AS NVARCHAR(16)),
	         ISNULL(s.login_name, ''),
	         ISNULL(DB_NAME(r.database_id), ''),
	         ISNULL(r.status, ''),
	         CAST(ISNULL(r.total_elapsed_time, 0) / 1000.0 AS FLOAT),
	         ISNULL(SUBSTRING(t.text, 1, 4000), ''),
	         ISNULL(s.host_name, ''),
	         ISNULL(r.wait_type, ''),
	         CASE WHEN ISNULL(r.blocking_session_id, 0) = 0 THEN ''
	              ELSE CAST(r.blocking_session_id AS NVARCHAR(16)) END,
	         CASE WHEN r.session_id = @@SPID THEN 1 ELSE 0 END
	  FROM sys.dm_exec_requests r
	  LEFT JOIN sys.dm_exec_sessions s ON s.session_id = r.session_id
	  OUTER APPLY sys.dm_exec_sql_text(r.sql_handle) t
	  WHERE r.session_id > 50
	  ORDER BY r.total_elapsed_time DESC`)
	if err != nil {
		return nil, err
	}
	return scanActivity(rows)
}

func (mssqlDialect) Kill(ctx context.Context, db *sql.DB, pid string) error {
	if err := validatePID(pid); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, "KILL "+pid)
	return err
}

func (mssqlDialect) TableSizes(ctx context.Context, db *sql.DB, schema string) ([]TableSize, error) {
	if schema == "" {
		schema = "dbo"
	}
	// index_id < 2 is the heap or the clustered index — the one partition set
	// that holds the rows. Summing every index_id would count each row once per
	// nonclustered index and report a table with four indexes as four times its
	// size.
	rows, err := db.QueryContext(ctx, `
		SELECT s.name, t.name,
		       SUM(CASE WHEN p.index_id < 2 THEN p.row_count ELSE 0 END),
		       SUM(p.reserved_page_count) * 8192,
		       SUM(p.in_row_data_page_count + p.lob_used_page_count + p.row_overflow_used_page_count) * 8192,
		       (SUM(p.used_page_count) - SUM(p.in_row_data_page_count + p.lob_used_page_count + p.row_overflow_used_page_count)) * 8192
		FROM sys.dm_db_partition_stats p
		JOIN sys.tables t ON t.object_id = p.object_id
		JOIN sys.schemas s ON s.schema_id = t.schema_id
		WHERE s.name = @p1
		GROUP BY s.name, t.name`, schema)
	if err != nil {
		return nil, err
	}
	return scanSizes(rows, schema)
}

// SQL Server will not drop a database while anything is connected, and unlike
// Postgres it has a switch for it: single-user mode with an immediate rollback
// disconnects everyone else. That statement fails when the database is already
// gone, which is why it is preparation rather than the drop itself.
func (d mssqlDialect) DropDatabaseSQL(name string) ([]DropStatement, error) {
	q, err := d.QuoteIdent(name)
	if err != nil {
		return nil, err
	}
	return []DropStatement{
		{SQL: "ALTER DATABASE " + q + " SET SINGLE_USER WITH ROLLBACK IMMEDIATE"},
		{SQL: "DROP DATABASE IF EXISTS " + q},
	}, nil
}

func (mssqlDialect) AdminDatabase() string { return "master" }
