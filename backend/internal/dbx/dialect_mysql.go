package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type mysqlDialect struct{}

func (mysqlDialect) Driver() Driver               { return DriverMySQL }
func (mysqlDialect) SQLDriverName() string        { return "mysql" }
func (mysqlDialect) NormaliseDSN(d string) string { return d }
func (mysqlDialect) VersionQuery() string         { return "SELECT version()" }
func (mysqlDialect) TunePool(*sql.DB)             {}
func (mysqlDialect) Placeholder(int) string       { return "?" }
func (mysqlDialect) DefaultSchema() string        { return "" }
func (mysqlDialect) SupportsDDL() bool            { return true }

// MySQL has no RETURNING (MariaDB 10.5 has it for DELETE only), so a row edit
// there reports what it affected rather than handing the stored row back.
func (mysqlDialect) SupportsReturning() bool { return false }

func (mysqlDialect) QuoteIdent(name string) (string, error) { return quoteBacktick(name) }

func (d mysqlDialect) Paginate(limit, offset, argStart int) (string, []any) {
	return standardLimit(d, limit, offset, argStart)
}

func (mysqlDialect) ColumnTypes() []string {
	return []string{
		"varchar(255)", "text", "longtext", "char(1)", "tinyint(1)",
		"smallint", "int", "bigint", "int unsigned", "bigint unsigned",
		"decimal(10,2)", "float", "double",
		"date", "time", "datetime", "timestamp", "year",
		"json", "blob", "longblob", "binary(16)", "enum('a','b')",
	}
}

func (mysqlDialect) Databases(ctx context.Context, db *sql.DB) ([]Database, error) {
	rows, err := db.QueryContext(ctx, `SELECT s.SCHEMA_NAME,
	                COALESCE(SUM(t.DATA_LENGTH + t.INDEX_LENGTH), 0),
	                '',
	                s.DEFAULT_CHARACTER_SET_NAME
	         FROM information_schema.SCHEMATA s
	         LEFT JOIN information_schema.TABLES t ON t.TABLE_SCHEMA = s.SCHEMA_NAME
	         GROUP BY s.SCHEMA_NAME, s.DEFAULT_CHARACTER_SET_NAME
	         ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	return scanDatabases(rows)
}

func (mysqlDialect) Tables(ctx context.Context, db *sql.DB, schema string) ([]Table, error) {
	rows, err := db.QueryContext(ctx, `SELECT TABLE_SCHEMA, TABLE_NAME,
	                CASE TABLE_TYPE WHEN 'BASE TABLE' THEN 'table' ELSE LOWER(TABLE_TYPE) END,
	                COALESCE(TABLE_ROWS, -1),
	                COALESCE(DATA_LENGTH + INDEX_LENGTH, 0),
	                COALESCE(TABLE_COMMENT, '')
	         FROM information_schema.TABLES
	         WHERE TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys')
	           AND (? = '' OR TABLE_SCHEMA = ?)
	         ORDER BY 1, 2`, schema, schema)
	if err != nil {
		return nil, err
	}
	return scanTables(rows)
}

// Columns uses COLUMN_TYPE rather than the standard DATA_TYPE, because only the
// former carries the length and unsigned flag — "int" and "int unsigned" are
// different columns and the Structure tab should not render them identically.
func (mysqlDialect) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error) {
	rows, err := db.QueryContext(ctx, `SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE,
	                COALESCE(COLUMN_DEFAULT, ''), ORDINAL_POSITION, COALESCE(COLUMN_KEY, '')
	         FROM information_schema.COLUMNS
	         WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
	         ORDER BY ORDINAL_POSITION`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Column{}
	for rows.Next() {
		var c Column
		var nullable string
		if err := rows.Scan(&c.Name, &c.Type, &nullable, &c.Default, &c.Position, &c.Key); err != nil {
			return nil, err
		}
		c.Nullable = nullable == "YES"
		out = append(out, c)
	}
	return out, rows.Err()
}

func (d mysqlDialect) PrimaryKey(ctx context.Context, db *sql.DB, schema, table string) ([]string, error) {
	return infoSchemaPrimaryKey(ctx, db, d, schema, table)
}

func (mysqlDialect) Indexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error) {
	rows, err := db.QueryContext(ctx, `SELECT INDEX_NAME, NON_UNIQUE, COLUMN_NAME
	          FROM information_schema.STATISTICS
	          WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
	          ORDER BY INDEX_NAME, SEQ_IN_INDEX`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	acc := newIndexAcc()
	for rows.Next() {
		var name, col string
		var nonUnique int
		if err := rows.Scan(&name, &nonUnique, &col); err != nil {
			return nil, err
		}
		acc.add(name, col, nonUnique == 0, name == "PRIMARY")
	}
	return acc.slice(), rows.Err()
}

func (mysqlDialect) ForeignKeys(ctx context.Context, db *sql.DB, schema, table string) ([]ForeignKey, error) {
	rows, err := db.QueryContext(ctx, `SELECT k.CONSTRAINT_NAME, k.COLUMN_NAME, k.REFERENCED_TABLE_SCHEMA,
	                 k.REFERENCED_TABLE_NAME, k.REFERENCED_COLUMN_NAME,
	                 COALESCE(r.UPDATE_RULE, ''), COALESCE(r.DELETE_RULE, '')
	          FROM information_schema.KEY_COLUMN_USAGE k
	          LEFT JOIN information_schema.REFERENTIAL_CONSTRAINTS r
	            ON r.CONSTRAINT_NAME = k.CONSTRAINT_NAME
	           AND r.CONSTRAINT_SCHEMA = k.TABLE_SCHEMA
	          WHERE k.TABLE_SCHEMA = ? AND k.TABLE_NAME = ?
	            AND k.REFERENCED_TABLE_NAME IS NOT NULL
	          ORDER BY k.CONSTRAINT_NAME, k.ORDINAL_POSITION`, schema, table)
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
		fk.OnUpdate, fk.OnDelete = upd, del
	}
	return acc.slice(), rows.Err()
}

// MySQL keeps the original statement and hands it back verbatim.
func (d mysqlDialect) CreateSQL(ctx context.Context, db *sql.DB, schema, table string, _ *TableDetail) (string, error) {
	rel, err := qualify(d, schema, table)
	if err != nil {
		return "", err
	}
	// SHOW CREATE TABLE answers in two columns for a table and four for a view
	// (the statement, then the character set and collation it was created
	// under), so the row is scanned by however many columns it has. Scanning
	// two failed on every view and left it with no definition at all.
	rows, err := db.QueryContext(ctx, "SHOW CREATE TABLE "+rel)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return "", err
	}
	if len(cols) < 2 || !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", err
		}
		return "", sql.ErrNoRows
	}
	cells := make([]sql.NullString, len(cols))
	ptrs := make([]any, len(cols))
	for i := range cells {
		ptrs[i] = &cells[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return "", err
	}
	return cells[1].String, rows.Err()
}

func (mysqlDialect) CastText(e string) string { return "CAST(" + e + " AS CHAR)" }

func (mysqlDialect) AddColumnKeyword() string { return "ADD COLUMN" }

func (mysqlDialect) BeforeDropColumn(context.Context, *sql.DB, string, string, string) error {
	return nil
}

func (d mysqlDialect) ExplainPlan(ctx context.Context, db *sql.DB, query string) (*QueryResult, error) {
	checked, checkErr := explainStatement(d.Driver(), query)
	if checkErr != nil {
		return nil, checkErr
	}
	query = checked.SQL
	return RunQuery(ctx, db, "EXPLAIN "+query, 500)
}

// Activity reads the process list. MySQL reports no blocking graph here — that
// lives in performance_schema and is off by default on plenty of servers — so
// BlockedBy stays empty rather than being guessed at.
func (mysqlDialect) Activity(ctx context.Context, db *sql.DB) ([]Activity, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT CAST(ID AS CHAR),
	         COALESCE(USER, ''),
	         COALESCE(DB, ''),
	         COALESCE(COMMAND, ''),
	         COALESCE(TIME, 0),
	         COALESCE(INFO, ''),
	         COALESCE(HOST, ''),
	         COALESCE(STATE, ''),
	         '',
	         CASE WHEN ID = CONNECTION_ID() THEN 1 ELSE 0 END
	  FROM information_schema.PROCESSLIST
	  ORDER BY TIME DESC`)
	if err != nil {
		return nil, err
	}
	return scanActivity(rows)
}

func (mysqlDialect) Kill(ctx context.Context, db *sql.DB, pid string) error {
	if err := validatePID(pid); err != nil {
		return err
	}
	// KILL takes no parameter marker on any MySQL version, which is why the id
	// is validated as digits above rather than bound.
	_, err := db.ExecContext(ctx, "KILL "+pid)
	return err
}

func (mysqlDialect) TableSizes(ctx context.Context, db *sql.DB, schema string) ([]TableSize, error) {
	// TABLE_ROWS is InnoDB's estimate from the same statistics the optimiser
	// uses, and DATA_FREE is excluded: it is space the table has claimed and is
	// not using, which belongs in a "reclaim this" conversation and not in a
	// comparison of which table is biggest.
	q := `SELECT TABLE_SCHEMA, TABLE_NAME,
	             COALESCE(TABLE_ROWS, 0),
	             COALESCE(DATA_LENGTH, 0) + COALESCE(INDEX_LENGTH, 0),
	             COALESCE(DATA_LENGTH, 0),
	             COALESCE(INDEX_LENGTH, 0)
	      FROM information_schema.TABLES
	      WHERE TABLE_TYPE = 'BASE TABLE' AND TABLE_SCHEMA = `
	var rows *sql.Rows
	var err error
	if schema == "" {
		rows, err = db.QueryContext(ctx, q+"DATABASE()")
	} else {
		rows, err = db.QueryContext(ctx, q+"?", schema)
	}
	if err != nil {
		return nil, err
	}
	return scanSizes(rows, schema)
}
func (d mysqlDialect) DropDatabaseSQL(name string) ([]DropStatement, error) {
	q, err := d.QuoteIdent(name)
	if err != nil {
		return nil, err
	}
	return []DropStatement{{SQL: "DROP DATABASE IF EXISTS " + q}}, nil
}

// MySQL is happy to drop the database the session has selected.
func (mysqlDialect) AdminDatabase() string { return "" }

// --- the workbench ---------------------------------------------------------

func (mysqlDialect) readScope() readScope { return readScopeSession }

// enterRead makes the session's transactions read-only.
//
// Not START TRANSACTION READ ONLY, which is what the driver offers: a
// statement that commits implicitly — every piece of DDL — ends that
// transaction first and then runs, so a CREATE or a DROP went straight through
// it. With the session itself read-only the server refuses both, on MySQL and
// MariaDB alike.
//
// The session is put back to whatever it was, which is read from whichever of
// the two names the server has for the variable. A server that will not take
// the setting at all (some that only speak the protocol) is left as it is: the
// classifier's verdict still stands in front of the statement, and refusing to
// read from such a server would help nobody.
func (mysqlDialect) enterRead(ctx context.Context, conn *sql.Conn) (func(context.Context) error, error) {
	unchanged := func(context.Context) error { return nil }
	var was int
	if err := conn.QueryRowContext(ctx, "SELECT @@session.transaction_read_only").Scan(&was); err != nil {
		if err := conn.QueryRowContext(ctx, "SELECT @@session.tx_read_only").Scan(&was); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return unchanged, nil
		}
	}
	if was == 1 {
		return unchanged, nil
	}
	if _, err := conn.ExecContext(ctx, "SET SESSION TRANSACTION READ ONLY"); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return unchanged, nil
	}
	return func(ctx context.Context) error {
		_, err := conn.ExecContext(ctx, "SET SESSION TRANSACTION READ WRITE")
		return err
	}, nil
}

// regexMatch uses REGEXP, which MySQL and MariaDB both spell the same way even
// though the engines behind it (ICU, PCRE) do not accept quite the same
// patterns.
func (d mysqlDialect) regexMatch(expr, ph string) string {
	return d.CastText(expr) + " REGEXP " + ph
}

func (mysqlDialect) rowEstimate(ctx context.Context, db *sql.DB, schema, table string) (int64, error) {
	var n sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT TABLE_ROWS FROM information_schema.TABLES
	         WHERE TABLE_SCHEMA = COALESCE(NULLIF(?, ''), DATABASE()) AND TABLE_NAME = ?
	           AND TABLE_TYPE = 'BASE TABLE'`, schema, table).Scan(&n)
	if err == sql.ErrNoRows || err == nil && !n.Valid {
		return -1, nil
	}
	return n.Int64, err
}

// keyExpr compares a JSON column through its text. MySQL compares a JSON value
// with a string as two different types, which is never equal, so a keyless row
// holding a document could not be found by the document it was read with.
func (d mysqlDialect) keyExpr(column Column, quoted string) string {
	if strings.EqualFold(column.Type, "json") {
		return d.CastText(quoted)
	}
	return quoted
}

func (mysqlDialect) byteLength(_ Column, quoted string) (string, bool, error) {
	return "OCTET_LENGTH(" + quoted + ")", false, nil
}

// emptyInsert: MySQL has no DEFAULT VALUES; an empty column list and an empty
// VALUES list is its spelling of the same thing.
func (mysqlDialect) emptyInsert() (string, error) { return "() VALUES ()", nil }

// generatedKeyQuery reads the AUTO_INCREMENT value the session's last INSERT
// produced, which is how MySQL answers what RETURNING answers elsewhere.
func (mysqlDialect) generatedKeyQuery() string { return "SELECT LAST_INSERT_ID()" }

// countsChangedRows reports that an UPDATE's affected-row count is the number
// of rows whose values changed, not the number the WHERE matched. An edit that
// writes back the value already there therefore reports zero, and cannot be
// told apart from an edit that matched nothing by the count alone.
func (mysqlDialect) countsChangedRows() bool { return true }

// explainSQL covers the two servers behind this driver, which agree on the
// estimated plan and disagree on the measured one: MySQL 8.0.18 added EXPLAIN
// ANALYZE, MariaDB has had ANALYZE as a statement of its own since 10.1.
func (mysqlDialect) explainSQL(version, statement string, opts ExplainOptions) (string, error) {
	json := opts.Format == ExplainJSON
	maria := strings.Contains(strings.ToLower(version), "mariadb")
	switch {
	case !opts.Analyze && json:
		return "EXPLAIN FORMAT=JSON " + statement, nil
	case !opts.Analyze:
		return "EXPLAIN " + statement, nil
	case maria && json:
		return "ANALYZE FORMAT=JSON " + statement, nil
	case maria:
		return "ANALYZE " + statement, nil
	case json:
		return "EXPLAIN ANALYZE FORMAT=JSON " + statement, nil
	}
	return "EXPLAIN ANALYZE " + statement, nil
}

// preparePlan asks MySQL for the second version of its JSON plan before a
// measured one is requested as JSON. From 8.3 the server produces EXPLAIN
// ANALYZE FORMAT=JSON only in that version, and a session left on the first —
// the default — is refused with "This version of MySQL doesn't yet support
// 'EXPLAIN ANALYZE with JSON format'", on a server that supports exactly that.
// An older MySQL has no such variable and refuses the SET; its refusal of the
// plan itself is then the answer, in its own words. MariaDB spells the
// statement differently and needs nothing.
func (mysqlDialect) preparePlan(ctx context.Context, conn *sql.Conn, version string, opts ExplainOptions) bool {
	if !opts.Analyze || opts.Format != ExplainJSON || strings.Contains(strings.ToLower(version), "mariadb") {
		return false
	}
	_, err := conn.ExecContext(ctx, "SET SESSION explain_json_format_version = 2")
	return err == nil
}

// cancelFunc kills the statement by the server's id for this connection.
//
// The driver answers a cancelled context by closing the socket, and the server
// goes on executing until it has rows to send. KILL QUERY from a second
// connection is the only thing that stops it. The id is read before the
// statement starts, on the connection that will run it.
func (mysqlDialect) cancelFunc(ctx context.Context, db *sql.DB, conn *sql.Conn) (func(), error) {
	var id uint64
	if err := conn.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&id); err != nil {
		return nil, fmt.Errorf("read the connection id: %w", err)
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// KILL takes no bind marker; the id is a number the server gave us.
		_, _ = db.ExecContext(ctx, "KILL QUERY "+strconv.FormatUint(id, 10))
	}, nil
}
