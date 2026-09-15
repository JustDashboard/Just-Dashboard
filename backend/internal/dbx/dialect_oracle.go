package dbx

import (
	"context"
	"database/sql"
	"strings"
)

type oracleDialect struct{}

func (oracleDialect) Driver() Driver                      { return DriverOracle }
func (oracleDialect) SQLDriverName() string               { return "oracle" }
func (oracleDialect) NormaliseDSN(d string) string        { return d }
func (oracleDialect) TunePool(*sql.DB)                    {}
func (oracleDialect) Placeholder(n int) string            { return ":" + itoa(n) }
func (oracleDialect) SupportsReturning() bool             { return false }
func (oracleDialect) SupportsDDL() bool                   { return true }
func (oracleDialect) QuoteIdent(s string) (string, error) { return quoteDouble(s) }

func (oracleDialect) VersionQuery() string {
	return "SELECT banner FROM v$version WHERE ROWNUM = 1"
}

// Oracle's schemas are its users, and the connected user's own schema is the
// one they mean by default. There is no static answer, so the empty string
// stands for "whatever CURRENT_SCHEMA resolves to" and the catalogue queries
// below fall back to it.
func (oracleDialect) DefaultSchema() string { return "" }

// Paginate uses the SQL:2008 form, which Oracle has supported since 12c. The
// older ROWNUM-subquery idiom is not emitted: it needs the whole statement
// rewritten rather than a tail appended, and 11g has been out of support for
// years.
func (d oracleDialect) Paginate(limit, offset, argStart int) (string, []any) {
	return fetchFirstLimit(d, limit, offset, argStart)
}

func (oracleDialect) ColumnTypes() []string {
	return []string{
		"VARCHAR2(255)", "NVARCHAR2(255)", "CHAR(1)", "CLOB",
		"NUMBER", "NUMBER(10)", "NUMBER(18,2)", "BINARY_FLOAT", "BINARY_DOUBLE",
		"DATE", "TIMESTAMP", "TIMESTAMP WITH TIME ZONE",
		"RAW(16)", "BLOB",
	}
}

// Databases lists schemas. Oracle's "database" is the instance; the unit a user
// browses is the schema, so those are what the picker is filled with — matching
// what every Oracle tool shows in the same position.
func (oracleDialect) Databases(ctx context.Context, db *sql.DB) ([]Database, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT u.username,
	         NVL((SELECT SUM(s.bytes) FROM dba_segments s WHERE s.owner = u.username), 0),
	         u.username,
	         NULL
	  FROM all_users u
	  WHERE u.oracle_maintained = 'N'
	  ORDER BY u.username`)
	if err != nil {
		// dba_segments and oracle_maintained both need grants a plain
		// application account will not have. Falling back to the bare user list
		// keeps the picker populated instead of failing the page outright.
		// Aliased, and ordered by position: `SELECT username, 0, username`
		// with an `ORDER BY username` is ORA-00960 "ambiguous column naming
		// in select list", because the sort cannot tell the two apart.
		rows, err = db.QueryContext(ctx, `
		  SELECT u.username AS db_name, 0 AS db_size, u.username AS db_owner, NULL AS db_enc
		  FROM all_users u ORDER BY 1`)
		if err != nil {
			return nil, err
		}
	}
	return scanDatabases(rows)
}

func (oracleDialect) Tables(ctx context.Context, db *sql.DB, schema string) ([]Table, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT owner, table_name, 'table', NVL(num_rows, 0), 0, NULL
	  FROM all_tables
	  WHERE (:1 IS NULL OR owner = :1)
	    AND owner NOT IN ('SYS','SYSTEM','OUTLN','XDB','MDSYS','CTXSYS','DBSNMP')
	  UNION ALL
	  SELECT owner, view_name, 'view', 0, 0, NULL
	  FROM all_views
	  WHERE (:1 IS NULL OR owner = :1)
	    AND owner NOT IN ('SYS','SYSTEM','OUTLN','XDB','MDSYS','CTXSYS','DBSNMP')
	  ORDER BY 1, 2`, oracleSchemaArg(schema))
	if err != nil {
		return nil, err
	}
	return scanTables(rows)
}

// oracleSchemaArg turns the empty string into a real NULL, because Oracle
// treats ” as NULL in comparisons and an `owner = ”` predicate would silently
// match nothing rather than everything.
func oracleSchemaArg(schema string) any {
	if strings.TrimSpace(schema) == "" {
		return nil
	}
	return schema
}

func (oracleDialect) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT column_name,
	         data_type ||
	           CASE WHEN data_type IN ('VARCHAR2','NVARCHAR2','CHAR','RAW')
	                THEN '(' || data_length || ')'
	                WHEN data_type = 'NUMBER' AND data_precision IS NOT NULL
	                THEN '(' || data_precision || ',' || NVL(data_scale,0) || ')'
	                ELSE '' END,
	         nullable,
	         -- data_default is a LONG. Wrapping it in TO_CHAR or NVL to
	         -- normalise the NULL is ORA-00932 "expression is of data type
	         -- LONG, which is incompatible with expected data type CHAR", so
	         -- it is selected raw and the NULL is absorbed by nullText.
	         data_default, column_id
	  FROM all_tab_columns
	  WHERE owner = NVL(:1, SYS_CONTEXT('USERENV','CURRENT_SCHEMA')) AND table_name = :2
	  ORDER BY column_id`, oracleSchemaArg(schema), table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Column{}
	for rows.Next() {
		var c Column
		var nullable string
		if err := rows.Scan(&c.Name, &c.Type, &nullable, nullText{&c.Default}, &c.Position); err != nil {
			return nil, err
		}
		// Oracle spells nullability 'Y'/'N', not the standard 'YES'/'NO'.
		c.Nullable = nullable == "Y"
		out = append(out, c)
	}
	return out, rows.Err()
}

func (oracleDialect) PrimaryKey(ctx context.Context, db *sql.DB, schema, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT cc.column_name
	  FROM all_constraints c
	  JOIN all_cons_columns cc
	    ON cc.constraint_name = c.constraint_name AND cc.owner = c.owner
	  WHERE c.constraint_type = 'P'
	    AND c.owner = NVL(:1, SYS_CONTEXT('USERENV','CURRENT_SCHEMA'))
	    AND c.table_name = :2
	  ORDER BY cc.position`, oracleSchemaArg(schema), table)
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

func (oracleDialect) Indexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT i.index_name, i.uniqueness, ic.column_name
	  FROM all_indexes i
	  JOIN all_ind_columns ic
	    ON ic.index_name = i.index_name AND ic.index_owner = i.owner
	  WHERE i.table_owner = NVL(:1, SYS_CONTEXT('USERENV','CURRENT_SCHEMA'))
	    AND i.table_name = :2
	  ORDER BY i.index_name, ic.column_position`, oracleSchemaArg(schema), table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	acc := newIndexAcc()
	for rows.Next() {
		var name, uniqueness, col string
		if err := rows.Scan(&name, &uniqueness, &col); err != nil {
			return nil, err
		}
		acc.add(name, col, uniqueness == "UNIQUE", false)
	}
	return acc.slice(), rows.Err()
}

func (oracleDialect) ForeignKeys(ctx context.Context, db *sql.DB, schema, table string) ([]ForeignKey, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT c.constraint_name, cc.column_name, rc.owner, rc.table_name, rcc.column_name,
	         c.delete_rule
	  FROM all_constraints c
	  JOIN all_cons_columns cc
	    ON cc.constraint_name = c.constraint_name AND cc.owner = c.owner
	  JOIN all_constraints rc
	    ON rc.constraint_name = c.r_constraint_name AND rc.owner = c.r_owner
	  JOIN all_cons_columns rcc
	    ON rcc.constraint_name = rc.constraint_name AND rcc.owner = rc.owner
	   AND rcc.position = cc.position
	  WHERE c.constraint_type = 'R'
	    AND c.owner = NVL(:1, SYS_CONTEXT('USERENV','CURRENT_SCHEMA'))
	    AND c.table_name = :2
	  ORDER BY c.constraint_name, cc.position`, oracleSchemaArg(schema), table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	acc := newFKAcc()
	for rows.Next() {
		var name, col, refSchema, refTable, refCol, del string
		if err := rows.Scan(&name, &col, &refSchema, &refTable, &refCol, nullText{&del}); err != nil {
			return nil, err
		}
		fk := acc.get(name)
		fk.Columns = append(fk.Columns, col)
		fk.RefSchema, fk.RefTable = refSchema, refTable
		fk.RefColumns = append(fk.RefColumns, refCol)
		fk.OnDelete = del
	}
	return acc.slice(), rows.Err()
}

// CreateSQL asks DBMS_METADATA, which produces exact DDL but needs a grant many
// application accounts lack. A failure there falls back to the synthesised
// form rather than leaving the Structure tab with no definition at all.
func (d oracleDialect) CreateSQL(ctx context.Context, db *sql.DB, schema, table string, detail *TableDetail) (string, error) {
	var ddl string
	err := db.QueryRowContext(ctx,
		`SELECT DBMS_METADATA.GET_DDL('TABLE', :1, NVL(:2, SYS_CONTEXT('USERENV','CURRENT_SCHEMA'))) FROM dual`,
		table, oracleSchemaArg(schema)).Scan(&ddl)
	if err == nil && strings.TrimSpace(ddl) != "" {
		return ddl, nil
	}
	return synthCreateTable(d, schema, table, detail), nil
}

func (oracleDialect) CastText(e string) string { return "TO_CHAR(" + e + ")" }

// Oracle, like SQL Server, takes ADD without COLUMN.
func (oracleDialect) AddColumnKeyword() string { return "ADD" }

func (oracleDialect) BeforeDropColumn(context.Context, *sql.DB, string, string, string) error {
	return nil
}

// ExplainPlan writes the plan to Oracle's plan table and then formats it, which
// is the only way Oracle exposes one. EXPLAIN PLAN FOR does not execute the
// statement it describes.
func (oracleDialect) ExplainPlan(ctx context.Context, db *sql.DB, query string) (*QueryResult, error) {
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
	if _, err := conn.ExecContext(ctx, "EXPLAIN PLAN FOR "+query); err != nil {
		return nil, err
	}
	rows, err := conn.QueryContext(ctx,
		"SELECT plan_table_output FROM TABLE(DBMS_XPLAN.DISPLAY())")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectRows(rows, 500, query)
}

// Activity reads v$session, which needs a grant a plain application account may
// not have; the handler surfaces that refusal rather than hiding it.
func (oracleDialect) Activity(ctx context.Context, db *sql.DB) ([]Activity, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT TO_CHAR(s.sid) || ',' || TO_CHAR(s.serial#),
	         -- No NVL(x, '') on the text columns: Oracle has no empty string,
	         -- so that guard returns NULL unchanged. nullText absorbs it.
	         s.username,
	         SYS_CONTEXT('USERENV','DB_NAME'),
	         s.status,
	         NVL(s.last_call_et, 0),
	         SUBSTR(q.sql_text, 1, 4000),
	         s.machine,
	         s.event,
	         TO_CHAR(s.blocking_session),
	         CASE WHEN s.sid = SYS_CONTEXT('USERENV','SID') THEN 1 ELSE 0 END
	  FROM v$session s
	  LEFT JOIN v$sql q ON q.sql_id = s.sql_id
	  WHERE s.type = 'USER'
	  ORDER BY s.last_call_et DESC`)
	if err != nil {
		return nil, err
	}
	return scanActivity(rows)
}

// Kill takes the "sid,serial#" pair Activity reported, which is why the pid
// validator admits a comma-free hex/digit string and this splits it back out.
func (oracleDialect) Kill(ctx context.Context, db *sql.DB, pid string) error {
	for _, part := range strings.Split(pid, ",") {
		if err := validatePID(part); err != nil {
			return err
		}
	}
	_, err := db.ExecContext(ctx, "ALTER SYSTEM KILL SESSION '"+pid+"' IMMEDIATE")
	return err
}

func (oracleDialect) TableSizes(ctx context.Context, db *sql.DB, schema string) ([]TableSize, error) {
	// user_segments rather than dba_segments: the dashboard connects as an
	// ordinary account far more often than as one with the DBA role, and a
	// storage panel that errors for everyone but a DBA is worse than one that
	// reports the schema the connection is already in.
	rows, err := db.QueryContext(ctx, `
		SELECT t.owner, t.table_name,
		       NVL(t.num_rows, 0),
		       NVL(s.bytes, 0),
		       NVL(s.bytes, 0),
		       NVL(i.bytes, 0)
		FROM all_tables t
		LEFT JOIN (SELECT segment_name, SUM(bytes) bytes FROM user_segments
		           WHERE segment_type = 'TABLE' GROUP BY segment_name) s
		  ON s.segment_name = t.table_name
		LEFT JOIN (SELECT ui.table_name, SUM(us.bytes) bytes
		           FROM user_indexes ui JOIN user_segments us ON us.segment_name = ui.index_name
		           GROUP BY ui.table_name) i
		  ON i.table_name = t.table_name
		WHERE t.owner = NVL(:1, SYS_CONTEXT('USERENV','CURRENT_SCHEMA'))`,
		oracleSchemaArg(schema))
	if err != nil {
		return nil, err
	}
	return scanSizes(rows, schema)
}

// Oracle has no database to drop: what the rest of this package calls a
// database is a user, and everything it owns goes with it under CASCADE.
// Dropping the user the session is connected as fails — "cannot drop a user
// that is currently connected" — which is the engine's answer and a clearer
// one than anything this could substitute.
func (d oracleDialect) DropDatabaseSQL(name string) ([]DropStatement, error) {
	q, err := d.QuoteIdent(name)
	if err != nil {
		return nil, err
	}
	return []DropStatement{{SQL: "DROP USER " + q + " CASCADE"}}, nil
}

func (oracleDialect) AdminDatabase() string { return "" }
