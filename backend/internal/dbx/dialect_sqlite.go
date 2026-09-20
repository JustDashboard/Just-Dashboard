package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

type sqliteDialect struct{}

func (sqliteDialect) Driver() Driver         { return DriverSQLite }
func (sqliteDialect) SQLDriverName() string  { return "sqlite" }
func (sqliteDialect) VersionQuery() string   { return "SELECT 'SQLite ' || sqlite_version()" }
func (sqliteDialect) Placeholder(int) string { return "?" }
func (sqliteDialect) DefaultSchema() string  { return "main" }
func (sqliteDialect) SupportsDDL() bool      { return true }

// SQLite gained RETURNING in 3.35; the pure-Go driver bundles a newer engine
// than that, so it can always be relied on here.
func (sqliteDialect) SupportsReturning() bool { return true }

func (sqliteDialect) QuoteIdent(name string) (string, error) { return quoteDouble(name) }

// TunePool holds SQLite to a single connection. The file serialises writers, so
// a pool of five racing connections turns every concurrent write into a
// "database is locked" error; one connection plus the busy timeout in the DSN
// is the documented way to put a single-file database behind a web front end.
func (sqliteDialect) TunePool(db *sql.DB) { db.SetMaxOpenConns(1) }

// NormaliseDSN makes a caller-supplied file path safe to open read-write under
// a web front end, appending the busy timeout and foreign-key pragmas the
// dashboard's own store uses. An already-parameterised DSN is left alone so an
// operator can still pass mode=ro.
func (sqliteDialect) NormaliseDSN(dsn string) string {
	if strings.Contains(dsn, "?") || strings.HasPrefix(dsn, "file:") {
		return dsn
	}
	return dsn + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
}

func (d sqliteDialect) Paginate(limit, offset, argStart int) (string, []any) {
	return standardLimit(d, limit, offset, argStart)
}

func (sqliteDialect) ColumnTypes() []string {
	return []string{"TEXT", "INTEGER", "REAL", "BLOB", "NUMERIC", "BOOLEAN", "DATETIME"}
}

// Databases reports the attached files. A SQLite connection is one file, but
// ATTACHed databases show up as extra rows and "main" is always present; the
// path is reported so the operator can see which file they are browsing.
func (sqliteDialect) Databases(ctx context.Context, db *sql.DB) ([]Database, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA database_list`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Database{}
	for rows.Next() {
		var seq int
		var name, file string
		if err := rows.Scan(&seq, &name, &file); err != nil {
			return nil, err
		}
		out = append(out, Database{Name: name, Owner: file})
	}
	return out, rows.Err()
}

// Tables reads sqlite_master. Row and size figures are not free here — each
// would be a COUNT(*) per table on every listing — so the row count is reported
// as unknown rather than making the table list quadratic.
func (sqliteDialect) Tables(ctx context.Context, db *sql.DB, _ string) ([]Table, error) {
	// -1, not 0: the catalogue saying nothing must not read as it saying the
	// table is empty. Size stays 0, which `omitempty` drops from the wire.
	rows, err := db.QueryContext(ctx, `SELECT 'main', name,
	                CASE type WHEN 'table' THEN 'table' ELSE type END,
	                -1, 0, ''
	         FROM sqlite_master
	         WHERE type IN ('table','view')
	           AND name NOT LIKE 'sqlite_%'
	         ORDER BY name`)
	if err != nil {
		return nil, err
	}
	return scanTables(rows)
}

// pragmaRows runs a PRAGMA that takes a table name. The pragma cannot bind a
// parameter, so the identifier is validated and quoted like every other
// generated statement in this package.
func pragmaRows(ctx context.Context, db *sql.DB, pragma, table string) (*sql.Rows, error) {
	q, err := quoteDouble(table)
	if err != nil {
		return nil, err
	}
	return db.QueryContext(ctx, "PRAGMA "+pragma+"("+q+")")
}

type sqliteColumnInfo struct {
	Column
	pkPos int
}

func sqliteTableInfo(ctx context.Context, db *sql.DB, table string) ([]sqliteColumnInfo, error) {
	rows, err := pragmaRows(ctx, db, "table_info", table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sqliteColumnInfo{}
	for rows.Next() {
		var (
			cid, notnull, pk int
			name, ctype      string
			dflt             sql.NullString
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		c := sqliteColumnInfo{pkPos: pk}
		c.Name, c.Type, c.Nullable = name, ctype, notnull == 0
		c.Default, c.Position = dflt.String, cid+1
		if pk > 0 {
			c.Key = "PRI"
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (sqliteDialect) Columns(ctx context.Context, db *sql.DB, _, table string) ([]Column, error) {
	info, err := sqliteTableInfo(ctx, db, table)
	if err != nil {
		return nil, err
	}
	out := make([]Column, 0, len(info))
	for _, c := range info {
		out = append(out, c.Column)
	}
	return out, nil
}

func (sqliteDialect) PrimaryKey(ctx context.Context, db *sql.DB, _, table string) ([]string, error) {
	info, err := sqliteTableInfo(ctx, db, table)
	if err != nil {
		return nil, err
	}
	pks := make([]sqliteColumnInfo, 0, 2)
	for _, c := range info {
		if c.pkPos > 0 {
			pks = append(pks, c)
		}
	}
	// The pragma's pk column is the 1-based position within the key, not a flag,
	// so a composite key keeps its declared order.
	sort.Slice(pks, func(i, j int) bool { return pks[i].pkPos < pks[j].pkPos })
	out := []string{}
	for _, c := range pks {
		out = append(out, c.Name)
	}
	return out, nil
}

func (sqliteDialect) Indexes(ctx context.Context, db *sql.DB, _, table string) ([]Index, error) {
	rows, err := pragmaRows(ctx, db, "index_list", table)
	if err != nil {
		return nil, err
	}
	type meta struct {
		name            string
		unique, primary bool
	}
	var metas []meta
	for rows.Next() {
		var (
			seq, unique, partial int
			name, origin         string
		)
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			rows.Close()
			return nil, err
		}
		metas = append(metas, meta{name: name, unique: unique == 1, primary: origin == "pk"})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	out := []Index{}
	for _, m := range metas {
		ir, err := pragmaRows(ctx, db, "index_info", m.name)
		if err != nil {
			continue
		}
		ix := Index{Name: m.name, Unique: m.unique, Primary: m.primary, Columns: []string{}}
		for ir.Next() {
			var seqno, cid int
			var col sql.NullString
			if err := ir.Scan(&seqno, &cid, &col); err != nil {
				ir.Close()
				return nil, err
			}
			if col.Valid {
				ix.Columns = append(ix.Columns, col.String)
			}
		}
		ir.Close()
		out = append(out, ix)
	}
	return out, nil
}

func (sqliteDialect) ForeignKeys(ctx context.Context, db *sql.DB, _, table string) ([]ForeignKey, error) {
	rows, err := pragmaRows(ctx, db, "foreign_key_list", table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	acc := newFKAcc()
	for rows.Next() {
		var (
			id, seq                     int
			refTable, from              string
			to                          sql.NullString
			onUpdate, onDelete, matchSp string
		)
		if err := rows.Scan(&id, &seq, &refTable, &from, &to, &onUpdate, &onDelete, &matchSp); err != nil {
			return nil, err
		}
		// SQLite names no foreign keys, so the constraint id doubles as one. A
		// NULL "to" means the key references the parent's primary key implicitly.
		fk := acc.get(fmt.Sprintf("fk_%d", id))
		fk.Columns = append(fk.Columns, from)
		fk.RefTable = refTable
		fk.RefColumns = append(fk.RefColumns, to.String)
		fk.OnUpdate, fk.OnDelete = onUpdate, onDelete
	}
	return acc.slice(), rows.Err()
}

// SQLite stores the statement it was given and hands it straight back.
func (sqliteDialect) CreateSQL(ctx context.Context, db *sql.DB, _, table string, _ *TableDetail) (string, error) {
	var ddl sql.NullString
	err := db.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE name = ? AND type IN ('table','view')`, table).Scan(&ddl)
	if err != nil {
		return "", err
	}
	return ddl.String, nil
}

func (sqliteDialect) CastText(e string) string { return "CAST(" + e + " AS TEXT)" }

func (sqliteDialect) AddColumnKeyword() string { return "ADD COLUMN" }

func (sqliteDialect) BeforeDropColumn(context.Context, *sql.DB, string, string, string) error {
	return nil
}

func (sqliteDialect) ExplainPlan(ctx context.Context, db *sql.DB, query string) (*QueryResult, error) {
	checked, checkErr := ExplainStatement(query)
	if checkErr != nil {
		return nil, checkErr
	}
	query = checked
	// Bare EXPLAIN in SQLite dumps bytecode; QUERY PLAN is the readable form.
	return RunQuery(ctx, db, "EXPLAIN QUERY PLAN "+query, 500)
}

// SQLite is a library, not a server: there are no other sessions to list.
func (sqliteDialect) Activity(context.Context, *sql.DB) ([]Activity, error) {
	return nil, ErrNoActivityView
}

func (sqliteDialect) Kill(context.Context, *sql.DB, string) error {
	return ErrNoActivityView
}

func (d sqliteDialect) TableSizes(ctx context.Context, db *sql.DB, _ string) ([]TableSize, error) {
	tables, err := d.Tables(ctx, db, "")
	if err != nil {
		return nil, err
	}
	// dbstat is a virtual table that only exists when SQLite was built with
	// SQLITE_ENABLE_DBSTAT_VTAB, which is a build flag and not a guarantee. It
	// is tried once and the whole feature degrades to row counts if it is
	// missing, rather than being probed per table.
	pages := map[string]int64{}
	if rows, err := db.QueryContext(ctx, `SELECT name, SUM(pgsize) FROM dbstat GROUP BY name`); err == nil {
		for rows.Next() {
			var name string
			var size int64
			if rows.Scan(&name, &size) == nil {
				pages[name] = size
			}
		}
		rows.Close()
	}

	out := make([]TableSize, 0, len(tables))
	for _, t := range tables {
		if strings.EqualFold(t.Type, "view") {
			continue
		}
		// COUNT(*) is exact here where every other engine's is an estimate.
		// SQLite has no stored row count to read, and the alternative is
		// reporting nothing — on a file-backed database a full count of a table
		// worth showing in a size list is cheap enough to be the right trade.
		q, err := d.QuoteIdent(t.Name)
		if err != nil {
			continue
		}
		var n int64
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+q).Scan(&n); err != nil {
			continue
		}
		size := pages[t.Name]
		out = append(out, TableSize{
			Schema: "", Table: t.Name, Rows: n,
			Bytes: size, DataBytes: size, IndexBytes: 0,
		})
	}
	return out, nil
}

// SQLiteDSNPath splits a SQLite DSN into the filesystem path it names and
// everything else, and SQLiteDSNWithPath puts one back together with the path
// replaced.
//
// They exist so the layer above can put that path through the same containment
// check every other client-supplied path goes through. A SQLite DSN *is* a
// path, which makes it precisely the case invariant 6 is about: one that does
// not look like a file operation. The three forms an operator may write are
// a bare path, a path with driver parameters, and the `file:` URI the driver
// also accepts — all three have to be recognised, because a check that missed
// one would be a check with a documented way round it.
func SQLiteDSNPath(dsn string) (path, rest string) {
	path = strings.TrimPrefix(dsn, "file:")
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path, rest = path[:i], path[i:]
	}
	return path, rest
}

func SQLiteDSNWithPath(dsn, path string) string {
	_, rest := SQLiteDSNPath(dsn)
	if strings.HasPrefix(dsn, "file:") {
		return "file:" + path + rest
	}
	return path + rest
}

// A SQLite database is a file, so there is no statement that removes one —
// DropDatabase deletes the file (and its journal) instead and never reaches
// here. The method exists because the interface is what makes the compiler
// name an engine that has not answered a question.
func (sqliteDialect) DropDatabaseSQL(string) ([]DropStatement, error) {
	return nil, fmt.Errorf("a SQLite database is a file; it is removed rather than dropped")
}

func (sqliteDialect) AdminDatabase() string { return "" }
