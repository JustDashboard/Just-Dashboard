package dbx

import (
	"context"
	"database/sql"
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
	// SHOW CREATE TABLE returns two columns, and a view names its second column
	// differently, so they are scanned positionally.
	var name, ddl string
	if err := db.QueryRowContext(ctx, "SHOW CREATE TABLE "+rel).Scan(&name, &ddl); err != nil {
		return "", err
	}
	return ddl, nil
}

func (mysqlDialect) CastText(e string) string { return "CAST(" + e + " AS CHAR)" }

func (mysqlDialect) AddColumnKeyword() string { return "ADD COLUMN" }

func (mysqlDialect) BeforeDropColumn(context.Context, *sql.DB, string, string, string) error {
	return nil
}

func (mysqlDialect) ExplainPlan(ctx context.Context, db *sql.DB, query string) (*QueryResult, error) {
	checked, checkErr := ExplainStatement(query)
	if checkErr != nil {
		return nil, checkErr
	}
	query = checked
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
