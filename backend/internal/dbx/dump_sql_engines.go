package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

// The built-in dump for the three engines that keep the text of what was
// created: MySQL and MariaDB (SHOW CREATE), SQLite (sqlite_master) and
// ClickHouse (SHOW CREATE). Their table definitions already carry the indexes
// and constraints, so what each plan adds is the objects beside the tables,
// the columns an INSERT must not name, and the session a restore has to run in.

// --- MySQL and MariaDB ----------------------------------------------------

// mysqlDefiner is the account a view is recorded as created by. Restored
// somewhere that account does not exist, the view is created and then cannot
// be read; without the clause it belongs to whoever restores it.
var mysqlDefiner = regexp.MustCompile("(?i) DEFINER=(`[^`]*`|[^@ ]+)@(`[^`]*`|[^ ]+)")

func planMySQLDump(ctx context.Context, q dumpQueryer, sel dumpSelection) (*dumpPlan, error) {
	plan := &dumpPlan{
		notes: []string{"not included: stored routines, triggers, events, grants"},
		session: []dumpStatement{
			stmt("SET NAMES utf8mb4"),
			// The zone the rows were read in, so a TIMESTAMP is the instant it
			// was and not that wall-clock time on a server set elsewhere.
			stmt("SET time_zone = '+00:00'"),
			// A row whose auto-increment column holds 0 is a row, not a
			// request for the next number.
			stmt("SET sql_mode = 'NO_AUTO_VALUE_ON_ZERO'"),
			// Each CREATE TABLE names its foreign keys inline, so a table can
			// only be created after the ones it points at — unless the check
			// is off, which is also what lets two tables point at each other.
			stmt("SET FOREIGN_KEY_CHECKS = 0"),
		},
		sessionEnd: []dumpStatement{stmt("SET FOREIGN_KEY_CHECKS = 1")},
	}

	// Scoped to the connection's own database. The catalogue read used to be
	// of every database the account could see, so the file held other
	// databases' tables and the restore began by dropping them.
	rows, err := q.QueryContext(ctx, `
	  SELECT TABLE_NAME, TABLE_TYPE
	  FROM information_schema.TABLES
	  WHERE TABLE_SCHEMA = DATABASE()
	  ORDER BY TABLE_NAME`)
	if err != nil {
		return nil, fmt.Errorf("cannot list tables: %w", err)
	}
	type entry struct{ name, kind string }
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.name, &e.kind); err != nil {
			rows.Close()
			return nil, err
		}
		entries = append(entries, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// The name a selection may qualify a table with. Here the schema and the
	// database are the same thing, and the table list shows it as the schema.
	var current sql.NullString
	if err := q.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&current); err == nil && !current.Valid {
		return nil, fmt.Errorf("no database named in the connection string; specify one explicitly")
	}
	schema := current.String

	views := []dumpObject{}
	for _, e := range entries {
		rel, err := quoteBacktick(e.name)
		if err != nil {
			plan.skipped = append(plan.skipped, e.name+": "+err.Error())
			continue
		}
		kind := strings.ToUpper(e.kind)
		switch {
		case strings.Contains(kind, "VIEW"):
			if !sel.wants(schema, e.name) || (sel.narrowed() && !sel.named(schema, e.name)) {
				continue
			}
			var (
				name, ddl string
				rest      [2]sql.NullString
			)
			if err := q.QueryRowContext(ctx, "SHOW CREATE VIEW "+rel).Scan(&name, &ddl, &rest[0], &rest[1]); err != nil {
				plan.skipped = append(plan.skipped, "view "+e.name+": "+err.Error())
				continue
			}
			views = append(views, dumpObject{
				rel: rel, name: e.name,
				drop:   stmt("DROP VIEW IF EXISTS " + rel),
				create: rawStmt(mysqlDefiner.ReplaceAllString(ddl, "")),
			})
		case kind == "SEQUENCE":
			// MariaDB lists a sequence among the tables. Read as one it is a
			// single row of counters, and INSERTing that back is refused.
			if !sel.wants(schema, e.name) || (sel.narrowed() && !sel.named(schema, e.name)) {
				continue
			}
			var name, ddl string
			if err := q.QueryRowContext(ctx, "SHOW CREATE SEQUENCE "+rel).Scan(&name, &ddl); err != nil {
				plan.skipped = append(plan.skipped, "sequence "+e.name+": "+err.Error())
				continue
			}
			obj := dumpObject{rel: rel, name: e.name, drop: stmt("DROP SEQUENCE IF EXISTS " + rel), create: rawStmt(ddl)}
			var next sql.NullString
			if err := q.QueryRowContext(ctx, "SELECT next_not_cached_value FROM "+rel).Scan(&next); err == nil &&
				next.Valid && numberText.MatchString(next.String) {
				plan.afterAll = append(plan.afterAll, stmt("ALTER SEQUENCE "+rel+" RESTART WITH "+next.String))
			}
			plan.sequences = append(plan.sequences, obj)
		default:
			if !sel.wants(schema, e.name) {
				continue
			}
			var name, ddl string
			if err := q.QueryRowContext(ctx, "SHOW CREATE TABLE "+rel).Scan(&name, &ddl); err != nil {
				plan.skipped = append(plan.skipped, e.name+": "+err.Error())
				continue
			}
			cols, err := mysqlInsertableColumns(ctx, q, e.name)
			if err != nil {
				plan.skipped = append(plan.skipped, e.name+": "+err.Error())
				continue
			}
			plan.tables = append(plan.tables, dumpTable{
				table: Table{Schema: schema, Name: e.name}, detail: &TableDetail{}, rel: rel,
				create: rawStmt(ddl), drop: stmt("DROP TABLE IF EXISTS " + rel),
				selectSQL: "SELECT " + strings.Join(cols, ", ") + " FROM " + rel,
				noData:    len(cols) == 0,
			})
		}
	}
	nameObjectRefs(views)
	plan.views = orderObjects(views)
	return plan, nil
}

// mysqlInsertableColumns lists a table's columns without the generated ones,
// which the server computes and refuses to be given.
func mysqlInsertableColumns(ctx context.Context, q dumpQueryer, table string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `
	  SELECT COLUMN_NAME, EXTRA
	  FROM information_schema.COLUMNS
	  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?
	  ORDER BY ORDINAL_POSITION`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name, extra string
		if err := rows.Scan(&name, &extra); err != nil {
			return nil, err
		}
		upper := strings.ToUpper(extra)
		if strings.Contains(upper, "GENERATED") && !strings.Contains(upper, "DEFAULT_GENERATED") ||
			strings.Contains(upper, "VIRTUAL") || strings.Contains(upper, "PERSISTENT") {
			continue
		}
		quoted, err := quoteBacktick(name)
		if err != nil {
			return nil, err
		}
		out = append(out, quoted)
	}
	return out, rows.Err()
}

// --- SQLite ---------------------------------------------------------------

// sqliteMainSchema is what SQLite calls the database a connection opened, and
// what the table list shows as every table's schema.
const sqliteMainSchema = "main"

func planSQLiteDump(ctx context.Context, q dumpQueryer, sel dumpSelection) (*dumpPlan, error) {
	plan := &dumpPlan{}
	// The underscore is escaped: bare, it matches any character, and a table
	// called "sqlitex" was left out of the dump as one of the engine's own.
	rows, err := q.QueryContext(ctx, `
	  SELECT type, name, tbl_name, COALESCE(sql, '')
	  FROM sqlite_master
	  WHERE name NOT LIKE 'sqlite\_%' ESCAPE '\'
	  ORDER BY rowid`)
	if err != nil {
		return nil, fmt.Errorf("cannot list tables: %w", err)
	}
	type entry struct{ kind, name, table, sql string }
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.kind, &e.name, &e.table, &e.sql); err != nil {
			rows.Close()
			return nil, err
		}
		entries = append(entries, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// A virtual table keeps its content in ordinary tables named after it.
	// Those are made by creating the virtual table and filled by inserting
	// into it; dumped as tables of their own they collide with both.
	var virtual []string
	for _, e := range entries {
		if e.kind == "table" && strings.HasPrefix(strings.ToUpper(strings.TrimSpace(e.sql)), "CREATE VIRTUAL TABLE") {
			virtual = append(virtual, e.name)
		}
	}
	shadow := func(name string) bool {
		for _, v := range virtual {
			if strings.HasPrefix(name, v+"_") {
				return true
			}
		}
		return false
	}

	tables := map[string]bool{}
	views := []dumpObject{}
	for _, e := range entries {
		rel, err := quoteDouble(e.name)
		if err != nil {
			plan.skipped = append(plan.skipped, e.name+": "+err.Error())
			continue
		}
		switch e.kind {
		case "table":
			if !sel.wants(sqliteMainSchema, e.name) || shadow(e.name) {
				continue
			}
			cols, autoincrement, err := sqliteInsertableColumns(ctx, q, e.name, e.sql)
			if err != nil {
				plan.skipped = append(plan.skipped, e.name+": "+err.Error())
				continue
			}
			table := dumpTable{
				table: Table{Schema: sqliteMainSchema, Name: e.name}, detail: &TableDetail{}, rel: rel,
				create: rawStmt(e.sql), drop: stmt("DROP TABLE IF EXISTS " + rel),
				selectSQL: "SELECT " + strings.Join(cols, ", ") + " FROM " + rel,
				noData:    len(cols) == 0,
			}
			if autoincrement {
				// The counter is kept apart from the rows and may be ahead of
				// them: the ids of deleted rows are never handed out again.
				var seq sql.NullInt64
				if err := q.QueryRowContext(ctx, `SELECT seq FROM sqlite_sequence WHERE name = ?`, e.name).Scan(&seq); err == nil && seq.Valid {
					name := "'" + strings.ReplaceAll(e.name, "'", "''") + "'"
					table.afterData = []dumpStatement{
						stmt("DELETE FROM sqlite_sequence WHERE name = " + name),
						stmt(fmt.Sprintf("INSERT INTO sqlite_sequence (name, seq) VALUES (%s, %d)", name, seq.Int64)),
					}
				}
			}
			tables[e.name] = true
			plan.tables = append(plan.tables, table)
		case "view":
			if !sel.wants(sqliteMainSchema, e.name) || (sel.narrowed() && !sel.named(sqliteMainSchema, e.name)) {
				continue
			}
			views = append(views, dumpObject{
				rel: rel, name: e.name, drop: stmt("DROP VIEW IF EXISTS " + rel), create: rawStmt(e.sql),
			})
		}
	}
	// Indexes and triggers belong to a table, and go with it. An index with
	// no text is one the engine made for a constraint and will make again.
	for _, e := range entries {
		if e.sql == "" || !tables[e.table] {
			continue
		}
		switch e.kind {
		case "index":
			plan.after = append(plan.after, rawStmt(e.sql))
		case "trigger":
			// After the rows: a trigger that fires on insert would otherwise
			// run once for every row the dump puts back.
			plan.afterAll = append(plan.afterAll, rawStmt(e.sql))
		}
	}
	nameObjectRefs(views)
	plan.views = orderObjects(views)
	for _, e := range entries {
		// A trigger on a view (INSTEAD OF) goes after the view it is on.
		if e.kind != "trigger" || e.sql == "" || tables[e.table] {
			continue
		}
		for i := range plan.views {
			if plan.views[i].name == e.table {
				plan.views[i].after = append(plan.views[i].after, rawStmt(e.sql))
			}
		}
	}
	return plan, nil
}

// sqliteInsertableColumns lists a table's columns without the generated ones,
// and reports whether the table numbers its rows with AUTOINCREMENT.
func sqliteInsertableColumns(ctx context.Context, q dumpQueryer, table, createSQL string) ([]string, bool, error) {
	quoted, err := quoteDouble(table)
	if err != nil {
		return nil, false, err
	}
	rows, err := q.QueryContext(ctx, "PRAGMA table_xinfo("+quoted+")")
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var (
			cid, notnull, pk, hidden int
			name, ctype              string
			dflt                     sql.NullString
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk, &hidden); err != nil {
			return nil, false, err
		}
		// 2 and 3 are generated columns, virtual and stored; 1 is a virtual
		// table's hidden column.
		if hidden != 0 {
			continue
		}
		q, err := quoteDouble(name)
		if err != nil {
			return nil, false, err
		}
		out = append(out, q)
	}
	return out, strings.Contains(strings.ToUpper(createSQL), "AUTOINCREMENT"), rows.Err()
}

// --- ClickHouse -----------------------------------------------------------

func planClickHouseDump(ctx context.Context, q dumpQueryer, database string, sel dumpSelection) (*dumpPlan, error) {
	plan := &dumpPlan{notes: []string{
		"not included: dictionaries, users, quotas and row policies",
	}}
	rows, err := q.QueryContext(ctx, `
	  SELECT name, engine
	  FROM system.tables
	  WHERE database = ? AND NOT is_temporary
	  ORDER BY name`, database)
	if err != nil {
		return nil, fmt.Errorf("cannot list tables: %w", err)
	}
	type entry struct{ name, engine string }
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.name, &e.engine); err != nil {
			rows.Close()
			return nil, err
		}
		entries = append(entries, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	d := clickhouseDialect{}
	views := []dumpObject{}
	materialized := 0
	for _, e := range entries {
		// The storage behind a materialized view that names no target. It is
		// made by creating the view.
		if strings.HasPrefix(e.name, ".inner") {
			continue
		}
		rel, err := qualify(d, database, e.name)
		if err != nil {
			plan.skipped = append(plan.skipped, e.name+": "+err.Error())
			continue
		}
		isView := strings.HasSuffix(e.engine, "View")
		if e.engine == "Dictionary" {
			continue
		}
		if !sel.wants(database, e.name) || (isView && sel.narrowed() && !sel.named(database, e.name)) {
			continue
		}
		var ddl string
		if err := q.QueryRowContext(ctx, "SHOW CREATE TABLE "+rel).Scan(&ddl); err != nil {
			plan.skipped = append(plan.skipped, e.name+": "+err.Error())
			continue
		}
		// ClickHouse returns the statement with literal \n escapes in some
		// client protocols.
		ddl = strings.ReplaceAll(ddl, `\n`, "\n")
		if isView {
			if e.engine == "MaterializedView" {
				materialized++
			}
			views = append(views, dumpObject{
				rel: rel, name: e.name, drop: stmt("DROP VIEW IF EXISTS " + rel), create: rawStmt(ddl),
			})
			continue
		}
		plan.tables = append(plan.tables, dumpTable{
			table: Table{Schema: database, Name: e.name}, detail: &TableDetail{}, rel: rel,
			create: rawStmt(ddl), drop: stmt("DROP TABLE IF EXISTS " + rel),
		})
	}
	if materialized > 0 {
		// A materialized view here is a trigger on inserts, not a stored
		// query: there is no REFRESH that would refill it. One writing TO a
		// table is whole, since that table is dumped; one with storage of its
		// own comes back empty and fills from what is inserted after.
		plan.notes = append(plan.notes, fmt.Sprintf(
			"%d materialized views are recreated; rows held in a view's own storage are not copied", materialized))
	}
	nameObjectRefs(views)
	plan.views = orderObjects(views)
	return plan, nil
}
