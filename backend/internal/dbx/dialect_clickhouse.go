package dbx

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2"
)

type clickhouseDialect struct{}

func (clickhouseDialect) Driver() Driver                      { return DriverClickHouse }
func (clickhouseDialect) SQLDriverName() string               { return "clickhouse" }
func (clickhouseDialect) NormaliseDSN(d string) string        { return d }
func (clickhouseDialect) VersionQuery() string                { return "SELECT 'ClickHouse ' || version()" }
func (clickhouseDialect) TunePool(*sql.DB)                    {}
func (clickhouseDialect) Placeholder(int) string              { return "?" }
func (clickhouseDialect) DefaultSchema() string               { return "default" }
func (clickhouseDialect) SupportsReturning() bool             { return false }
func (clickhouseDialect) QuoteIdent(s string) (string, error) { return quoteBacktick(s) }

// ClickHouse's CREATE TABLE requires a table engine and, for the MergeTree
// family that anyone actually uses, a sorting key. A generic column-list form
// would emit statements the server rejects, so the DDL builder declines to
// offer itself here rather than producing plausible-looking SQL that never
// runs. Its own SQL console remains available through the Query tab.
func (clickhouseDialect) SupportsDDL() bool { return false }

func (d clickhouseDialect) Paginate(limit, offset, argStart int) (string, []any) {
	return standardLimit(d, limit, offset, argStart)
}

func (clickhouseDialect) ColumnTypes() []string {
	return []string{
		"String", "FixedString(16)", "UUID",
		"Int8", "Int16", "Int32", "Int64", "UInt8", "UInt32", "UInt64",
		"Float32", "Float64", "Decimal(18,4)",
		"Date", "DateTime", "DateTime64(3)",
		"Bool", "JSON", "Array(String)",
	}
}

func (clickhouseDialect) Databases(ctx context.Context, db *sql.DB) ([]Database, error) {
	// The size comes from a joined aggregate rather than a correlated
	// subquery: ClickHouse's analyzer — the default since 24.3 — refuses to
	// resolve an outer column inside a subquery ("Resolve identifier 'd.name'
	// from parent scope only supported for constants and CTE"), so the obvious
	// form parses on an older server and fails outright on a current one. The
	// LEFT JOIN also keeps a database with no parts yet, which is every
	// freshly created one.
	rows, err := db.QueryContext(ctx, `
	  SELECT d.name,
	         toInt64(ifNull(s.bytes, 0)),
	         ifNull(d.engine, ''),
	         ''
	  FROM system.databases d
	  LEFT JOIN (
	    SELECT database, sum(bytes_on_disk) AS bytes
	    FROM system.parts WHERE active GROUP BY database
	  ) s ON s.database = d.name
	  ORDER BY d.name`)
	if err != nil {
		return nil, err
	}
	return scanDatabases(rows)
}

func (clickhouseDialect) Tables(ctx context.Context, db *sql.DB, schema string) ([]Table, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT database, name,
	         if(engine LIKE '%View', 'view', 'table'),
	         -- The cast comes first: total_rows is Nullable(UInt64), and asking
	         -- ClickHouse 24.8/25.8 for a supertype of that and a signed -1
	         -- literal is NO_COMMON_TYPE, which fails the whole catalogue.
	         ifNull(toInt64(total_rows), -1),
	         toInt64(ifNull(total_bytes, 0)),
	         ifNull(comment, '')
	  FROM system.tables
	  WHERE database NOT IN ('system','INFORMATION_SCHEMA','information_schema')
	    AND (? = '' OR database = ?)
	  ORDER BY database, name`, schema, schema)
	if err != nil {
		return nil, err
	}
	return scanTables(rows)
}

// Columns reads system.columns rather than information_schema: only the former
// reports whether a column is part of the sorting key, and ClickHouse spells
// optionality as a Nullable(...) wrapper on the type rather than a flag.
func (clickhouseDialect) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT name, type, ifNull(default_expression, ''), position, is_in_primary_key
	  FROM system.columns
	  WHERE database = ? AND table = ?
	  ORDER BY position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Column{}
	for rows.Next() {
		var c Column
		var inPK uint8
		if err := rows.Scan(&c.Name, &c.Type, &c.Default, &c.Position, &inPK); err != nil {
			return nil, err
		}
		c.Nullable = strings.HasPrefix(c.Type, "Nullable(")
		if inPK == 1 {
			c.Key = "PRI"
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// PrimaryKey reports the sorting key columns. ClickHouse has no primary-key
// constraint in the relational sense — the key orders parts and is not unique —
// so it identifies a place in the table, not a row. Nothing may edit by it:
// ApplyChanges refuses this engine outright, and the key is reported only so
// the Structure tab can show what the table is ordered by.
func (clickhouseDialect) PrimaryKey(ctx context.Context, db *sql.DB, schema, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT name FROM system.columns
	  WHERE database = ? AND table = ? AND is_in_primary_key = 1
	  ORDER BY position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Indexes reports the data-skipping indices, which are the only named index
// objects ClickHouse has. The sorting key is reported as a synthetic primary
// entry so the Structure tab can show what the table is actually ordered by.
func (clickhouseDialect) Indexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error) {
	out := []Index{}
	var sortingKey string
	if err := db.QueryRowContext(ctx,
		`SELECT ifNull(sorting_key, '') FROM system.tables WHERE database = ? AND name = ?`,
		schema, table).Scan(&sortingKey); err == nil && sortingKey != "" {
		cols := []string{}
		for _, c := range strings.Split(sortingKey, ",") {
			if c = strings.TrimSpace(c); c != "" {
				cols = append(cols, c)
			}
		}
		out = append(out, Index{Name: "sorting key", Columns: cols, Primary: true})
	}
	rows, err := db.QueryContext(ctx,
		`SELECT name, expr FROM system.data_skipping_indices WHERE database = ? AND table = ?`,
		schema, table)
	if err != nil {
		// A server too old for this table still has a usable sorting key above.
		return out, nil
	}
	defer rows.Close()
	for rows.Next() {
		var name, expr string
		if err := rows.Scan(&name, &expr); err != nil {
			return out, nil
		}
		out = append(out, Index{Name: name, Columns: []string{expr}})
	}
	return out, nil
}

// ClickHouse has no referential constraints at all.
func (clickhouseDialect) ForeignKeys(ctx context.Context, db *sql.DB, schema, table string) ([]ForeignKey, error) {
	return noForeignKeys(ctx, db, schema, table)
}

func (clickhouseDialect) CreateSQL(ctx context.Context, db *sql.DB, schema, table string, _ *TableDetail) (string, error) {
	rel, err := qualify(clickhouseDialect{}, schema, table)
	if err != nil {
		return "", err
	}
	var ddl string
	if err := db.QueryRowContext(ctx, "SHOW CREATE TABLE "+rel).Scan(&ddl); err != nil {
		return "", err
	}
	// ClickHouse returns the statement with literal \n escapes in some client
	// protocols; rendering those as newlines is the difference between readable
	// DDL and one very long line.
	return strings.ReplaceAll(ddl, `\n`, "\n"), nil
}

func (clickhouseDialect) CastText(e string) string { return "toString(" + e + ")" }

func (clickhouseDialect) AddColumnKeyword() string { return "ADD COLUMN" }

func (clickhouseDialect) BeforeDropColumn(context.Context, *sql.DB, string, string, string) error {
	return nil
}

func (d clickhouseDialect) ExplainPlan(ctx context.Context, db *sql.DB, query string) (*QueryResult, error) {
	checked, checkErr := explainStatement(d.Driver(), query)
	if checkErr != nil {
		return nil, checkErr
	}
	query = checked.SQL
	return RunQuery(ctx, db, "EXPLAIN "+query, 500)
}

// Activity reads system.processes. ClickHouse identifies a running query by a
// UUID rather than a connection id, which is why the pid field is a string.
func (clickhouseDialect) Activity(ctx context.Context, db *sql.DB) ([]Activity, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT toString(query_id),
	         ifNull(user, ''),
	         ifNull(current_database, ''),
	         'running',
	         toFloat64(elapsed),
	         ifNull(query, ''),
	         ifNull(toString(address), ''),
	         '',
	         '',
	         CASE WHEN query_id = queryID() THEN 1 ELSE 0 END
	  FROM system.processes
	  ORDER BY elapsed DESC`)
	if err != nil {
		return nil, err
	}
	return scanActivity(rows)
}

func (clickhouseDialect) Kill(ctx context.Context, db *sql.DB, pid string) error {
	if err := validatePID(pid); err != nil {
		return err
	}
	// The query id is a UUID, and KILL QUERY does take a bound parameter here.
	_, err := db.ExecContext(ctx, "KILL QUERY WHERE query_id = ? SYNC", pid)
	return err
}

func (clickhouseDialect) TableSizes(ctx context.Context, db *sql.DB, schema string) ([]TableSize, error) {
	if schema == "" {
		schema = "default"
	}
	// system.parts holds one row per data part, and only the active ones are
	// real: a merge leaves the inputs listed until they are cleaned up, so
	// summing everything double-counts a table that was recently written to.
	// Sizes are compressed bytes, which is what the disk actually holds.
	rows, err := db.QueryContext(ctx, `
		SELECT database, table,
		       toInt64(sum(rows)),
		       toInt64(sum(bytes_on_disk)),
		       toInt64(sum(data_compressed_bytes)),
		       toInt64(sum(marks_bytes))
		FROM system.parts
		WHERE active AND database = ?
		GROUP BY database, table`, schema)
	if err != nil {
		return nil, err
	}
	return scanSizes(rows, schema)
}
func (d clickhouseDialect) DropDatabaseSQL(name string) ([]DropStatement, error) {
	q, err := d.QuoteIdent(name)
	if err != nil {
		return nil, err
	}
	return []DropStatement{{SQL: "DROP DATABASE IF EXISTS " + q}}, nil
}

// ClickHouse's connection database is only a default for unqualified names, so
// there is nothing to move away from.
func (clickhouseDialect) AdminDatabase() string { return "" }

// --- the workbench ---------------------------------------------------------

func (clickhouseDialect) readScope() readScope { return readScopeSetting }

// refuseChanges: rows here cannot be edited one at a time. See
// ErrChangesUnsupported for why.
func (clickhouseDialect) refuseChanges() error { return ErrChangesUnsupported }

// clickhouseReadOnly attaches readonly=2 to a query: reads only, and — unlike
// readonly=1 — the other settings a connection string carries may still be
// sent along with it.
func clickhouseReadOnly(ctx context.Context) context.Context {
	return clickhouse.Context(ctx, clickhouse.WithSettings(clickhouse.Settings{"readonly": 2}))
}

// clickhouseRefusedSetting reports that the server would not let this account
// set readonly at all. That happens when the account is already restricted to
// reads, or when its profile pins the setting; either way the request that was
// refused is the setting, not the statement.
func clickhouseRefusedSetting(err error) bool {
	var ex *clickhouse.Exception
	if !errors.As(err, &ex) {
		return false
	}
	switch ex.Code {
	case 164: // READONLY
		return strings.Contains(ex.Message, "readonly") && strings.Contains(ex.Message, "setting")
	case 452: // SETTING_CONSTRAINT_VIOLATION
		return strings.Contains(ex.Message, "readonly")
	}
	return false
}

// textMatch uses the position functions instead of LIKE. ClickHouse's LIKE has
// no ESCAPE clause, and these take the operator's text as text.
func (d clickhouseDialect) textMatch(expr string, kind matchKind, fold bool, ph string) (string, func(string) string) {
	text, verbatim := d.CastText(expr), func(value string) string { return value }
	if fold {
		text, ph = "lowerUTF8("+text+")", "lowerUTF8("+ph+")"
	}
	switch kind {
	case matchPrefix:
		return "startsWith(" + text + ", " + ph + ")", verbatim
	case matchSuffix:
		return "endsWith(" + text + ", " + ph + ")", verbatim
	}
	return "position(" + text + ", " + ph + ") > 0", verbatim
}

func (d clickhouseDialect) regexMatch(expr, ph string) string {
	return "match(" + d.CastText(expr) + ", " + ph + ")"
}

// rowEstimate reads total_rows, which the MergeTree family keeps exactly.
func (clickhouseDialect) rowEstimate(ctx context.Context, db *sql.DB, schema, table string) (int64, error) {
	if schema == "" {
		schema = "default"
	}
	var n int64
	err := db.QueryRowContext(ctx,
		`SELECT ifNull(toInt64(total_rows), -1) FROM system.tables WHERE database = ? AND name = ?`,
		schema, table).Scan(&n)
	if err == sql.ErrNoRows {
		return -1, nil
	}
	return n, err
}

func (d clickhouseDialect) byteLength(_ Column, quoted string) string {
	return "length(" + d.CastText(quoted) + ")"
}
