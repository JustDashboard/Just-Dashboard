package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// The advisor: what the engine's own catalogue says is wrong with a database,
// phrased as findings with a fix attached.
//
// Every hosted platform grew one of these — Supabase calls it Advisors,
// PlanetScale Insights — because the questions are the same on every server
// and nobody asks them until something is slow: which foreign keys have no
// index behind them, which indexes are never read, which tables were never
// analysed, how close the connection limit is. The catalogue has known the
// answers all along. This asks, once, and hands back a list.
//
// Three halves. The generic checks walk the introspected structure the
// Structure tab already reads, so they hold on every SQL engine: a table with
// no primary key, a foreign key column with no index. The engine checks read
// statistics only that engine keeps, and each dialect that has them
// implements Adviser. And one check reads no catalogue at all: whether the
// release the server runs is still maintained.
//
// Nothing here changes anything. A finding carries the statement that would
// fix it; running it is the operator's act, through the route that statement
// belongs to.

// The four things a finding can be about. They are what the page groups by.
const (
	AdviceSecurity    = "security"
	AdvicePerformance = "performance"
	AdviceReliability = "reliability"
	AdviceMaintenance = "maintenance"
)

type Advice struct {
	ID string `json:"id"`
	// Level uses the finding vocabulary the rest of the product draws:
	// critical, warning, notice.
	Level string `json:"level"`
	// Category is security, performance, reliability or maintenance.
	Category string `json:"category"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Advice   string `json:"advice,omitempty"`
	// Objects names what the finding is about in words, one per object.
	Objects []string `json:"objects,omitempty"`
	// Targets is the same list as things the page can link to, each with the
	// statement that fixes that one object where one statement does.
	Targets []AdviceTarget `json:"targets,omitempty"`
	// SQL is every target's fix together, where one exists.
	SQL string `json:"sql,omitempty"`
	// Link is the dashboard page the fix is made on, when it is not a
	// statement: a path under /databases/<id>, without that prefix.
	Link string `json:"link,omitempty"`
}

// AdviceTarget is one object a finding is about.
type AdviceTarget struct {
	// Kind is table, index, sequence, schema, role, setting, extension,
	// slot, object, database, server or container.
	Kind   string `json:"kind"`
	Schema string `json:"schema,omitempty"`
	Name   string `json:"name"`
	// Table is the table an index belongs to.
	Table  string `json:"table,omitempty"`
	Detail string `json:"detail,omitempty"`
	SQL    string `json:"sql,omitempty"`
}

// targetSQL joins the fixes of a finding's targets into the one block the
// finding carries.
func targetSQL(targets []AdviceTarget) string {
	lines := []string{}
	for _, t := range targets {
		if t.SQL != "" {
			lines = append(lines, t.SQL)
		}
	}
	return strings.Join(lines, "\n")
}

// Adviser is the engine-specific half. Optional: an engine without it still
// gets the generic checks. The second result names what could not be
// assessed, so a check the account may not run reads as not assessed rather
// than as passed.
type Adviser interface {
	Advise(ctx context.Context, db *sql.DB, schema string) ([]Advice, []string, error)
}

// AdviseReport is the whole answer: findings ordered worst first, and what was
// looked at, so an empty list can be read as "nothing found" rather than
// "nothing checked".
type AdviseReport struct {
	CheckedAt     time.Time `json:"checkedAt"`
	Silences      []string  `json:"silences"`
	TablesOmitted int       `json:"tablesOmitted"`
	Findings      []Advice  `json:"findings"`
	TablesChecked int       `json:"tablesChecked"`
	// EngineChecks is false where only the generic structure checks ran.
	EngineChecks bool `json:"engineChecks"`
	// Truncated is true when the schema has more tables than the structure
	// checks walk, so "no finding" covers only the first TablesChecked.
	Truncated bool `json:"truncated"`
	// Version is the server's own version string, and EndOfLife what the
	// table says about that release, when it knows it.
	Version   string     `json:"version,omitempty"`
	EndOfLife *EndOfLife `json:"endOfLife,omitempty"`
}

// Advise runs every check for a schema.
func Advise(ctx context.Context, db *sql.DB, driver Driver, schema string) (*AdviseReport, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	if schema == "" {
		schema = d.DefaultSchema()
		// On MySQL and ClickHouse a "schema" is a database, and the one meant
		// when none is named is the one the connection opened. MySQL's
		// default was empty, so the structure walk covered every database on
		// the server while the engine checks matched none of them;
		// ClickHouse's was the database called "default", whichever one the
		// connection was in.
		if current := currentDatabaseQuery[driver]; current != "" {
			var name sql.NullString
			if err := db.QueryRowContext(ctx, current).Scan(&name); err == nil && name.String != "" {
				schema = name.String
			}
		}
	}
	report := &AdviseReport{Findings: []Advice{}, CheckedAt: time.Now().UTC(), Silences: []string{}}
	generic, checked, omitted, err := adviseStructure(ctx, db, d, schema)
	if err != nil {
		return nil, err
	}
	report.TablesChecked = checked
	report.TablesOmitted = omitted
	if omitted > 0 {
		report.Truncated = true
		report.Silences = append(report.Silences, fmt.Sprintf("Structure checks are bounded at %d tables; %d more tables were not assessed.", maxAdvisedTables, omitted))
	}
	report.Findings = append(report.Findings, generic...)
	if a, ok := d.(Adviser); ok {
		engine, silences, err := a.Advise(ctx, db, schema)
		if err != nil {
			report.Silences = append(report.Silences, "Engine statistics could not be assessed: "+err.Error())
		} else {
			report.EngineChecks = true
			report.Findings = append(report.Findings, engine...)
			report.Silences = append(report.Silences, silences...)
		}
	}
	if err := db.QueryRowContext(ctx, d.VersionQuery()).Scan(&report.Version); err == nil {
		if eol, ok := VersionEndOfLife(driver, report.Version, report.CheckedAt); ok {
			report.EndOfLife = eol
			if finding, ok := adviseEndOfLife(eol); ok {
				report.Findings = append(report.Findings, finding)
			}
		}
	}
	SortAdvice(report.Findings)
	return report, nil
}

// currentDatabaseQuery asks an engine whose schemas are databases which one
// the connection is in.
var currentDatabaseQuery = map[Driver]string{
	DriverMySQL:      `SELECT DATABASE()`,
	DriverClickHouse: `SELECT currentDatabase()`,
}

// SortAdvice orders findings worst first, keeping the order they were found
// in within a level.
func SortAdvice(findings []Advice) {
	rank := map[string]int{"critical": 0, "warning": 1, "notice": 2}
	sort.SliceStable(findings, func(i, j int) bool {
		return rank[findings[i].Level] < rank[findings[j].Level]
	})
}

// eolWarningDays is how far ahead of the date the advisor starts saying so:
// long enough to plan an upgrade, short enough not to nag for years.
const eolWarningDays = 180

func adviseEndOfLife(eol *EndOfLife) (Advice, bool) {
	name := eol.Product + " " + eol.Release
	when := eol.Date.Format("January 2006")
	target := []AdviceTarget{{Kind: "server", Name: name}}
	switch {
	case eol.Past:
		return Advice{
			ID: "version-end-of-life", Level: "warning", Category: AdviceSecurity,
			Title:   name + " is past its end of life",
			Detail:  "Maintenance for this release ended in " + when + ". It still runs, and that is the trouble: a vulnerability found in it from here on is not fixed.",
			Advice:  "Plan an upgrade to a maintained release. Take a backup first, and read the release notes for each major version in between.",
			Objects: []string{name}, Targets: target,
		}, true
	case eol.DaysLeft <= eolWarningDays:
		return Advice{
			ID: "version-end-of-life-soon", Level: "notice", Category: AdviceSecurity,
			Title:   fmt.Sprintf("%s reaches its end of life in %d days", name, eol.DaysLeft),
			Detail:  "Maintenance for this release ends in " + when + "; after that it receives no security fixes.",
			Advice:  "Plan the upgrade now, while there is no hurry.",
			Objects: []string{name}, Targets: target,
		}, true
	}
	return Advice{}, false
}

// maxAdvisedTables bounds the structure walk: three catalogue queries per
// table is fine for a schema of fifty and an outage for one of five thousand.
const maxAdvisedTables = 300

// maxKeyFixes bounds how many keyless tables have a fix worked out for them.
const maxKeyFixes = 50

func adviseStructure(ctx context.Context, db *sql.DB, d Dialect, schema string) ([]Advice, int, int, error) {
	tables, err := d.Tables(ctx, db, schema)
	if err != nil {
		return nil, 0, 0, err
	}
	noKey := []AdviceTarget{}
	unindexed := []AdviceTarget{}
	checked := 0
	omitted := 0
	for _, t := range tables {
		if !advisedTable(t.Type) {
			continue
		}
		if checked >= maxAdvisedTables {
			omitted++
			continue
		}
		checked++
		pk, err := d.PrimaryKey(ctx, db, t.Schema, t.Name)
		if err != nil {
			return nil, checked, 0, err
		}
		if len(pk) == 0 {
			target := AdviceTarget{Kind: "table", Schema: t.Schema, Name: t.Name}
			// Working out a fix reads the table's columns and indexes, which
			// on some engines is two slow catalogue queries; a schema with
			// more keyless tables than this gets the statement for the first
			// of them and the name of the rest.
			if len(noKey) < maxKeyFixes {
				target.SQL, target.Detail = primaryKeyFix(ctx, db, d, t.Schema, t.Name)
			}
			noKey = append(noKey, target)
		}
		fks, err := d.ForeignKeys(ctx, db, t.Schema, t.Name)
		if err != nil {
			return nil, checked, 0, err
		}
		if len(fks) == 0 {
			continue
		}
		indexes, err := d.Indexes(ctx, db, t.Schema, t.Name)
		if err != nil {
			return nil, checked, 0, err
		}
		for _, fk := range fks {
			if indexCovers(indexes, fk.Columns) {
				continue
			}
			target := AdviceTarget{Kind: "table", Schema: t.Schema, Name: t.Name, Detail: strings.Join(fk.Columns, ", ")}
			if stmt, err := createIndexSQL(d, t.Schema, t.Name, fk.Columns); err == nil {
				target.SQL = stmt
			}
			unindexed = append(unindexed, target)
		}
	}
	out := []Advice{}
	if len(noKey) > 0 {
		objects := make([]string, len(noKey))
		for i, t := range noKey {
			objects[i] = t.Name
		}
		out = append(out, Advice{
			ID: "no-primary-key", Level: "warning", Category: AdviceReliability,
			Title:   fmt.Sprintf("%d table%s with no primary key", len(noKey), pluralS(len(noKey))),
			Detail:  "A table without a primary key cannot be edited row by row from this dashboard, is skipped by logical replication, and gives an ORM nothing to identify a row by.",
			Advice:  "Add an identity column, or declare the column that already identifies the row as the key.",
			Objects: objects, Targets: noKey, SQL: targetSQL(noKey),
		})
	}
	if len(unindexed) > 0 {
		objects := make([]string, len(unindexed))
		for i, t := range unindexed {
			objects[i] = t.Name + "(" + t.Detail + ")"
		}
		out = append(out, Advice{
			ID: "unindexed-foreign-key", Level: "warning", Category: AdvicePerformance,
			Title:   fmt.Sprintf("%d foreign key%s with no index", len(unindexed), pluralS(len(unindexed))),
			Detail:  "Every delete or update of the referenced row scans the whole referencing table to check the constraint, and every join on the key does the same.",
			Advice:  "Create an index on the referencing columns.",
			Objects: objects, Targets: unindexed, SQL: targetSQL(unindexed),
		})
	}
	return out, checked, omitted, nil
}

// primaryKeyFix renders the statement that gives a table a primary key, where
// there is one that does not need a decision only its owner can make, and says
// in a few words what the statement does.
//
// A table that already has a unique index over columns that cannot be NULL has
// a key in everything but name, and declaring it is the whole fix. A table
// with no such index needs a new column. That is offered on PostgreSQL and SQL
// Server only, where an INSERT that names no columns goes on working with an
// identity column added: on MySQL and Oracle the same statement breaks every
// such INSERT in the application, and a fix that does that is not one to hand
// over ready to run. SQLite cannot add a key to a table that exists, and a
// ClickHouse table's key is fixed when it is created.
func primaryKeyFix(ctx context.Context, db *sql.DB, d Dialect, schema, table string) (stmt, detail string) {
	driver := d.Driver()
	if driver == DriverSQLite || driver == DriverClickHouse {
		return "", ""
	}
	rel, err := qualify(d, schema, table)
	if err != nil {
		return "", ""
	}
	columns, err := d.Columns(ctx, db, schema, table)
	if err != nil {
		return "", ""
	}
	indexes, err := d.Indexes(ctx, db, schema, table)
	if err != nil {
		return "", ""
	}
	return primaryKeyStatement(d, rel, table, columns, indexes)
}

// primaryKeyStatement is primaryKeyFix once the catalogue has been read.
func primaryKeyStatement(d Dialect, rel, table string, columns []Column, indexes []Index) (stmt, detail string) {
	required := map[string]bool{}
	hasID := false
	for _, c := range columns {
		required[strings.ToLower(c.Name)] = !c.Nullable
		hasID = hasID || strings.EqualFold(c.Name, "id")
	}
	for _, ix := range indexes {
		if !ix.Unique || ix.Primary || len(ix.Columns) == 0 {
			continue
		}
		quoted := make([]string, 0, len(ix.Columns))
		for _, c := range ix.Columns {
			// A column the table does not list is an expression, and one that
			// may be NULL identifies no row.
			q, err := d.QuoteIdent(c)
			if err != nil || !required[strings.ToLower(c)] {
				quoted = nil
				break
			}
			quoted = append(quoted, q)
		}
		if quoted == nil {
			continue
		}
		detail = "declares the unique index " + ix.Name + " as the key"
		if d.Driver() == DriverPostgres {
			// The index that is already there becomes the key's, rather than a
			// second one being built beside it.
			name, err := d.QuoteIdent(table + "_pkey")
			index, ierr := d.QuoteIdent(ix.Name)
			if err != nil || ierr != nil {
				continue
			}
			return "ALTER TABLE " + rel + " ADD CONSTRAINT " + name + " PRIMARY KEY USING INDEX " + index + ";", detail
		}
		return "ALTER TABLE " + rel + " ADD PRIMARY KEY (" + strings.Join(quoted, ", ") + ");", detail
	}
	if hasID {
		return "", ""
	}
	switch d.Driver() {
	case DriverPostgres:
		return "ALTER TABLE " + rel + " ADD COLUMN id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY;",
			"adds an identity column; the table is rewritten and locked while it is"
	case DriverMSSQL:
		return "ALTER TABLE " + rel + " ADD id BIGINT IDENTITY(1,1) NOT NULL PRIMARY KEY;",
			"adds an identity column; the table is locked while every row is given a number"
	}
	return "", ""
}

// advisedTable reports whether a catalogue entry is a table the structure
// checks apply to. A partitioned table is one: PostgreSQL lists it under its
// relation kind, "p", and it was being passed over with the views.
func advisedTable(kind string) bool {
	switch strings.ToLower(kind) {
	case "table", "base table", "p", "partitioned table":
		return true
	}
	return false
}

// indexCovers reports whether some index begins with exactly the key's
// columns in order — the only shape the planner uses for the constraint check.
func indexCovers(indexes []Index, columns []string) bool {
	for _, ix := range indexes {
		if len(ix.Columns) < len(columns) {
			continue
		}
		match := true
		for i, c := range columns {
			if !strings.EqualFold(ix.Columns[i], c) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// createIndexSQL renders the index a foreign key is missing. The table is
// qualified wherever the engine has schemas to qualify it with — MySQL's
// "schema" is a database, and a statement pasted into a console that has
// another one selected would otherwise build the index on the wrong table or
// on none.
func createIndexSQL(d Dialect, schema, table string, columns []string) (string, error) {
	t, err := qualify(d, schema, table)
	if err != nil {
		return "", err
	}
	name, err := d.QuoteIdent(table + "_" + strings.Join(columns, "_") + "_idx")
	if err != nil {
		return "", err
	}
	quoted := make([]string, len(columns))
	for i, c := range columns {
		q, err := d.QuoteIdent(c)
		if err != nil {
			return "", err
		}
		quoted[i] = q
	}
	return "CREATE INDEX " + name + " ON " + t + " (" + strings.Join(quoted, ", ") + ");", nil
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// --- PostgreSQL ------------------------------------------------------------

func (d postgresDialect) Advise(ctx context.Context, db *sql.DB, schema string) ([]Advice, []string, error) {
	out := []Advice{}
	rel := func(name string) string {
		q, err := qualify(d, schema, name)
		if err != nil {
			return ""
		}
		return q
	}

	// Indexes nobody reads. Unique and primary indexes are constraints and
	// stay whatever their scan count; the rest exist only to be read.
	rows, err := db.QueryContext(ctx, `
	  SELECT s.indexrelname, s.relname, pg_relation_size(s.indexrelid)
	  FROM pg_stat_user_indexes s
	  JOIN pg_index i ON i.indexrelid = s.indexrelid
	  WHERE s.schemaname = $1 AND s.idx_scan = 0 AND NOT i.indisunique AND NOT i.indisprimary
	    AND pg_relation_size(s.indexrelid) > 1024 * 1024
	  ORDER BY pg_relation_size(s.indexrelid) DESC LIMIT 50`, schema)
	if err != nil {
		return nil, nil, err
	}
	unused, unusedTargets := []string{}, []AdviceTarget{}
	var unusedBytes int64
	for rows.Next() {
		var index, table string
		var size int64
		if err := rows.Scan(&index, &table, &size); err != nil {
			rows.Close()
			return nil, nil, err
		}
		unused = append(unused, table+"."+index)
		unusedBytes += size
		target := AdviceTarget{Kind: "index", Schema: schema, Name: index, Table: table, Detail: humanBytes(size)}
		if q := rel(index); q != "" {
			target.SQL = "DROP INDEX " + q + ";"
		}
		unusedTargets = append(unusedTargets, target)
	}
	rows.Close()
	if len(unused) > 0 {
		out = append(out, Advice{
			ID: "unused-index", Level: "notice", Category: AdvicePerformance,
			Title:   fmt.Sprintf("%d index%s never read since statistics were reset", len(unused), map[bool]string{true: "", false: "es"}[len(unused) == 1]),
			Detail:  fmt.Sprintf("Together they take %s and cost every write to their tables, and the planner has not used one of them.", humanBytes(unusedBytes)),
			Advice:  "Drop the ones that are not there for a reason the statistics cannot see — a report run once a year, a replica's workload.",
			Objects: unused, Targets: unusedTargets, SQL: targetSQL(unusedTargets),
		})
	}

	// Two indexes with the same definition on the same table. The expression
	// list is part of what makes them the same: two expression indexes have
	// no key columns at all, and compared by columns alone every pair of
	// them on a table looked identical. Within a set, an index that backs a
	// constraint sorts first, because it is the one that cannot be dropped.
	rows, err = db.QueryContext(ctx, `
	  SELECT c.relname,
	         string_agg(i.relname, chr(31) ORDER BY (con.oid IS NULL), x.indisunique DESC, i.relname),
	         string_agg((con.oid IS NOT NULL)::int::text, chr(31) ORDER BY (con.oid IS NULL), x.indisunique DESC, i.relname)
	  FROM pg_index x
	  JOIN pg_class c ON c.oid = x.indrelid
	  JOIN pg_class i ON i.oid = x.indexrelid
	  JOIN pg_namespace n ON n.oid = c.relnamespace
	  LEFT JOIN pg_constraint con ON con.conindid = x.indexrelid AND con.contype IN ('p', 'u', 'x')
	  WHERE n.nspname = $1
	  GROUP BY c.relname, x.indrelid, i.relam, x.indkey::text, x.indclass::text, x.indoption::text,
	           x.indcollation::text, COALESCE(pg_get_expr(x.indexprs, x.indrelid), ''),
	           COALESCE(pg_get_expr(x.indpred, x.indrelid), '')
	  HAVING count(*) > 1`, schema)
	if err != nil {
		return nil, nil, err
	}
	dupes, dupeTargets := []string{}, []AdviceTarget{}
	for rows.Next() {
		var table, names, backed string
		if err := rows.Scan(&table, &names, &backed); err != nil {
			rows.Close()
			return nil, nil, err
		}
		set, constraint := splitUnit(names), splitUnit(backed)
		dupes = append(dupes, table+": "+strings.Join(set, ", "))
		// The first of each set stays; the rest are the copies. One that
		// enforces a constraint is named without a statement: dropping it is
		// dropping the constraint, which is not this finding's to suggest.
		for i := 1; i < len(set); i++ {
			target := AdviceTarget{Kind: "index", Schema: schema, Name: set[i], Table: table, Detail: "same as " + set[0]}
			if constraint[i] == "0" {
				if q := rel(set[i]); q != "" {
					target.SQL = "DROP INDEX " + q + ";"
				}
			}
			dupeTargets = append(dupeTargets, target)
		}
	}
	rows.Close()
	if len(dupes) > 0 {
		out = append(out, Advice{
			ID: "duplicate-index", Level: "warning", Category: AdvicePerformance,
			Title:   fmt.Sprintf("%d set%s of identical indexes", len(dupes), pluralS(len(dupes))),
			Detail:  "Each set covers the same columns in the same order with the same options; the second copy costs every write and buys nothing.",
			Advice:  "Keep one of each set.",
			Objects: dupes, Targets: dupeTargets, SQL: targetSQL(dupeTargets),
		})
	}

	// An index a failed CREATE INDEX CONCURRENTLY left behind: maintained on
	// every write, used by no query.
	rows, err = db.QueryContext(ctx, `
	  SELECT i.relname, c.relname
	  FROM pg_index x
	  JOIN pg_class c ON c.oid = x.indrelid
	  JOIN pg_class i ON i.oid = x.indexrelid
	  JOIN pg_namespace n ON n.oid = c.relnamespace
	  WHERE n.nspname = $1 AND NOT x.indisvalid
	  ORDER BY c.relname, i.relname LIMIT 50`, schema)
	if err == nil {
		invalid, targets := []string{}, []AdviceTarget{}
		for rows.Next() {
			var index, table string
			if err := rows.Scan(&index, &table); err != nil {
				rows.Close()
				return nil, nil, err
			}
			invalid = append(invalid, table+"."+index)
			target := AdviceTarget{Kind: "index", Schema: schema, Name: index, Table: table}
			if q := rel(index); q != "" {
				target.SQL = "REINDEX INDEX CONCURRENTLY " + q + ";"
			}
			targets = append(targets, target)
		}
		rows.Close()
		if len(invalid) > 0 {
			out = append(out, Advice{
				ID: "invalid-index", Level: "warning", Category: AdviceReliability,
				Title:   fmt.Sprintf("%d invalid index%s", len(invalid), map[bool]string{true: "", false: "es"}[len(invalid) == 1]),
				Detail:  "A concurrent index build that failed leaves its index behind marked invalid: every write still maintains it and no query can use it. If it was to enforce uniqueness, nothing is enforcing it.",
				Advice:  "Rebuild it, or drop it and create it again.",
				Objects: invalid, Targets: targets, SQL: targetSQL(targets),
			})
		}
	}

	// Tables with stale or missing statistics, and tables carrying dead rows.
	rows, err = db.QueryContext(ctx, `
	  SELECT relname, n_live_tup, n_dead_tup,
	         last_analyze IS NULL AND last_autoanalyze IS NULL
	  FROM pg_stat_user_tables
	  WHERE schemaname = $1 AND (
	        (n_live_tup > 1000 AND last_analyze IS NULL AND last_autoanalyze IS NULL)
	     OR (n_dead_tup > 10000 AND n_dead_tup > n_live_tup / 5))
	  ORDER BY n_dead_tup DESC LIMIT 50`, schema)
	if err != nil {
		return nil, nil, err
	}
	never, neverTargets := []string{}, []AdviceTarget{}
	bloated, bloatedTargets := []string{}, []AdviceTarget{}
	for rows.Next() {
		var table string
		var live, dead int64
		var neverAnalysed bool
		if err := rows.Scan(&table, &live, &dead, &neverAnalysed); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if neverAnalysed {
			never = append(never, table)
			target := AdviceTarget{Kind: "table", Schema: schema, Name: table}
			if q := rel(table); q != "" {
				target.SQL = "ANALYZE " + q + ";"
			}
			neverTargets = append(neverTargets, target)
		}
		if dead > 10000 && dead > live/5 {
			detail := fmt.Sprintf("%d dead of %d live", dead, live)
			bloated = append(bloated, table+" ("+detail+")")
			target := AdviceTarget{Kind: "table", Schema: schema, Name: table, Detail: detail}
			if q := rel(table); q != "" {
				target.SQL = "VACUUM ANALYZE " + q + ";"
			}
			bloatedTargets = append(bloatedTargets, target)
		}
	}
	rows.Close()
	if len(never) > 0 {
		out = append(out, Advice{
			ID: "never-analysed", Level: "warning", Category: AdviceMaintenance,
			Title:   fmt.Sprintf("%d table%s the planner has no statistics for", len(never), pluralS(len(never))),
			Detail:  "Without statistics the planner guesses row counts, and a guess on a table of a thousand rows or more is how a query picks a sequential scan over the index that exists for it.",
			Advice:  "Run ANALYZE once; autovacuum keeps it current afterwards.",
			Objects: never, Targets: neverTargets, SQL: targetSQL(neverTargets),
		})
	}
	if len(bloated) > 0 {
		out = append(out, Advice{
			ID: "dead-rows", Level: "warning", Category: AdviceMaintenance,
			Title:   fmt.Sprintf("%d table%s carrying dead rows", len(bloated), pluralS(len(bloated))),
			Detail:  "More than a fifth of the table is rows that were updated or deleted and not yet reclaimed, which every scan still reads past.",
			Advice:  "Vacuum them, and check autovacuum is keeping up with the write rate on these tables.",
			Objects: bloated, Targets: bloatedTargets, SQL: targetSQL(bloatedTargets),
		})
	}

	// Integer sequences approaching their ceiling — the failure that arrives
	// as "duplicate key" on a Tuesday afternoon with no warning. The column a
	// sequence feeds is read beside it, because widening one without the other
	// only moves where the insert fails.
	rows, err = db.QueryContext(ctx, `
	  SELECT s.sequencename, COALESCE(s.last_value, 0), s.max_value, s.data_type::text,
	         COALESCE(o.relname, ''), COALESCE(o.attname, ''), COALESCE(o.typname, '')
	  FROM pg_sequences s
	  LEFT JOIN LATERAL (
	    SELECT c.relname, a.attname, format_type(a.atttypid, NULL) AS typname
	    FROM pg_class q
	    JOIN pg_namespace qn ON qn.oid = q.relnamespace
	    JOIN pg_depend d ON d.classid = 'pg_class'::regclass AND d.objid = q.oid
	                    AND d.refclassid = 'pg_class'::regclass AND d.deptype IN ('a', 'i')
	    JOIN pg_class c ON c.oid = d.refobjid
	    JOIN pg_attribute a ON a.attrelid = d.refobjid AND a.attnum = d.refobjsubid
	    WHERE q.relkind = 'S' AND q.relname = s.sequencename AND qn.nspname = s.schemaname
	    LIMIT 1
	  ) o ON true
	  WHERE s.schemaname = $1 AND s.last_value IS NOT NULL
	    AND s.last_value::numeric > s.max_value::numeric * 0.8`, schema)
	if err == nil {
		exhausting, targets := []string{}, []AdviceTarget{}
		for rows.Next() {
			var name, kind, table, column, columnType string
			var last, max int64
			if err := rows.Scan(&name, &last, &max, &kind, &table, &column, &columnType); err != nil {
				rows.Close()
				return nil, nil, err
			}
			detail := fmt.Sprintf("%d of %d", last, max)
			exhausting = append(exhausting, name+" ("+detail+")")
			targets = append(targets, AdviceTarget{Kind: "sequence", Schema: schema, Name: name, Table: table, Detail: detail,
				SQL: pgSequenceFix(d, schema, name, kind, max, table, column, columnType)})
		}
		rows.Close()
		if len(exhausting) > 0 {
			out = append(out, Advice{
				ID: "sequence-exhaustion", Level: "critical", Category: AdviceReliability,
				Title:   fmt.Sprintf("%d sequence%s past 80%% of its ceiling", len(exhausting), pluralS(len(exhausting))),
				Detail:  "When a sequence reaches its maximum every insert into its table fails.",
				Advice:  "Change the column and the sequence to bigint before it runs out. Changing a column's type rewrites its table and locks it while it does, so pick a quiet moment.",
				Objects: exhausting, Targets: targets, SQL: targetSQL(targets),
			})
		}
	}

	out = append(out, pgServerAdvice(ctx, db)...)
	return out, nil, nil
}

// pgSequenceFix renders what gives a sequence room: bigint for the sequence
// and for the column it feeds, or — for a sequence that is already bigint and
// was given a lower ceiling by hand — the ceiling taken away. A bigint
// sequence at its natural maximum has no fix and gets none.
func pgSequenceFix(d postgresDialect, schema, name, kind string, max int64, table, column, columnType string) string {
	seq, err := qualify(d, schema, name)
	if err != nil {
		return ""
	}
	if kind == "bigint" {
		if max == 1<<63-1 {
			return ""
		}
		return "ALTER SEQUENCE " + seq + " NO MAXVALUE;"
	}
	stmts := []string{}
	if table != "" && column != "" && columnType != "bigint" {
		rel, err := qualify(d, schema, table)
		col, cerr := d.QuoteIdent(column)
		if err == nil && cerr == nil {
			stmts = append(stmts, "ALTER TABLE "+rel+" ALTER COLUMN "+col+" TYPE bigint;")
		}
	}
	return strings.Join(append(stmts, "ALTER SEQUENCE "+seq+" AS bigint;"), "\n")
}

// raisedConnectionLimit is the limit a "connections near the limit" fix
// suggests: half as many again, rounded up to the next fifty. Enough to end
// the refusals, and not so much that the memory each connection may take is
// suddenly several times what the server was sized for.
func raisedConnectionLimit(current int) int {
	raised := current + current/2
	if rest := raised % 50; rest != 0 {
		raised += 50 - rest
	}
	return raised
}

// pgSettingFix renders the two statements that persist a parameter and apply
// it, which is what every "set this" fix on PostgreSQL is.
func pgSettingFix(name, value string) string {
	return "ALTER SYSTEM SET " + name + " = " + dumpString(DriverPostgres, value) + ";\nSELECT pg_reload_conf();"
}

// pgServerAdvice is the server-wide readings: connections against the limit,
// the cache hit rate, sessions holding transactions open, how far the
// database is from transaction-id wraparound, and the settings that decide
// whether a crash loses data.
func pgServerAdvice(ctx context.Context, db *sql.DB) []Advice {
	out := []Advice{}
	server := func(name string) []AdviceTarget { return []AdviceTarget{{Kind: "setting", Name: name}} }

	var used, max int
	if err := db.QueryRowContext(ctx, `
	  SELECT (SELECT count(*) FROM pg_stat_activity WHERE backend_type = 'client backend'),
	         current_setting('max_connections')::int`).Scan(&used, &max); err == nil && max > 0 {
		if used*100/max >= 80 {
			out = append(out, Advice{
				ID: "connections-near-limit", Level: "critical", Category: AdviceReliability,
				Title:   fmt.Sprintf("%d of %d connections in use", used, max),
				Detail:  "When the limit is reached every new client is refused with \"too many connections\", including this dashboard.",
				Advice:  "Put a pooler (PgBouncer) in front of the server, or raise max_connections and restart: the statement stores the new limit, and only a restart applies it.",
				Targets: server("max_connections"), Link: "performance",
				SQL: "ALTER SYSTEM SET max_connections = " + dumpString(DriverPostgres, itoa(raisedConnectionLimit(max))) + ";",
			})
		}
	}
	var hit, read float64
	if err := db.QueryRowContext(ctx, `
	  SELECT COALESCE(sum(blks_hit), 0), COALESCE(sum(blks_read), 0)
	  FROM pg_stat_database WHERE datname = current_database()`).Scan(&hit, &read); err == nil && hit+read > 100000 {
		ratio := hit / (hit + read) * 100
		if ratio < 90 {
			out = append(out, Advice{
				ID: "cache-hit-ratio", Level: "warning", Category: AdvicePerformance,
				Title:   fmt.Sprintf("Cache hit ratio is %.1f%%", ratio),
				Detail:  "More than a tenth of block reads went to disk rather than shared memory, which on a server whose working set should fit means shared_buffers is too small for it.",
				Advice:  "Raise shared_buffers (a quarter of memory is the usual start) and effective_cache_size, then restart.",
				Targets: server("shared_buffers"), Link: "settings",
			})
		}
	}
	var idleInTx int
	if err := db.QueryRowContext(ctx, `
	  SELECT count(*) FROM pg_stat_activity
	  WHERE state = 'idle in transaction' AND state_change < now() - interval '5 minutes'`).Scan(&idleInTx); err == nil && idleInTx > 0 {
		out = append(out, Advice{
			ID: "idle-in-transaction", Level: "warning", Category: AdviceReliability,
			Title:   fmt.Sprintf("%d session%s idle in a transaction for over five minutes", idleInTx, pluralS(idleInTx)),
			Detail:  "An open transaction holds its locks and stops vacuum reclaiming anything newer than it, however long it sits there doing nothing.",
			Advice:  "Set idle_in_transaction_session_timeout so the server ends them, and find the client that forgets to commit. Performance lists them.",
			Targets: server("idle_in_transaction_session_timeout"), Link: "performance",
			SQL: pgSettingFix("idle_in_transaction_session_timeout", "10min"),
		})
	}

	// Transaction ids are thirty-two bits. A table that vacuum has not frozen
	// in two billion transactions makes the server refuse writes to protect
	// what it holds; the warning comes while there is still time.
	var age float64
	if err := db.QueryRowContext(ctx, `SELECT age(datfrozenxid) FROM pg_database WHERE datname = current_database()`).Scan(&age); err == nil && age > 1_000_000_000 {
		level := "warning"
		if age > 1_500_000_000 {
			level = "critical"
		}
		out = append(out, Advice{
			ID: "transaction-id-wraparound", Level: level, Category: AdviceReliability,
			Title:   fmt.Sprintf("%.1f billion transactions since the database was last frozen", age/1e9),
			Detail:  "At two billion the server stops accepting writes until a vacuum has frozen the old rows, and that vacuum has to finish first.",
			Advice:  "Run a freezing vacuum now, and find out what has been stopping autovacuum: a long-open transaction, a forgotten replication slot, or autovacuum switched off.",
			Targets: []AdviceTarget{{Kind: "database", Name: "current database"}},
			SQL:     "VACUUM (FREEZE, VERBOSE);",
		})
	}

	// A slot nobody reads from keeps every WAL segment since it was last
	// read. The disk fills, and the server stops.
	if rows, err := db.QueryContext(ctx, `
	  SELECT slot_name, COALESCE(pg_wal_lsn_diff(`+pgCurrentLSN+`, restart_lsn), 0)::bigint
	  FROM pg_replication_slots WHERE NOT active AND NOT temporary ORDER BY 2 DESC`); err == nil {
		slots, targets := []string{}, []AdviceTarget{}
		for rows.Next() {
			var name string
			var retained int64
			if rows.Scan(&name, &retained) != nil {
				continue
			}
			slots = append(slots, name+" ("+humanBytes(retained)+" retained)")
			targets = append(targets, AdviceTarget{Kind: "slot", Name: name, Detail: humanBytes(retained) + " retained",
				SQL: "SELECT pg_drop_replication_slot(" + dumpString(DriverPostgres, name) + ");"})
		}
		rows.Close()
		if len(slots) > 0 {
			out = append(out, Advice{
				ID: "inactive-replication-slot", Level: "warning", Category: AdviceReliability,
				Title:   fmt.Sprintf("%d replication slot%s with nothing reading %s", len(slots), pluralS(len(slots)), map[bool]string{true: "it", false: "them"}[len(slots) == 1]),
				Detail:  "The server keeps every write-ahead log segment a slot has not consumed. A slot whose consumer is gone keeps them all, until the disk is full.",
				Advice:  "Reconnect the replica or subscriber that owns each slot, or drop the ones that have no owner any more.",
				Objects: slots, Targets: targets, SQL: targetSQL(targets), Link: "performance",
			})
		}
	}

	var fsync, fullPageWrites string
	if err := db.QueryRowContext(ctx, `SELECT current_setting('fsync'), current_setting('full_page_writes')`).Scan(&fsync, &fullPageWrites); err == nil {
		for name, value := range map[string]string{"fsync": fsync, "full_page_writes": fullPageWrites} {
			if value != "off" {
				continue
			}
			out = append(out, Advice{
				ID: name + "-off", Level: "critical", Category: AdviceReliability,
				Title:   name + " is off",
				Detail:  "With this off a power cut or a kernel crash can leave the data files corrupt beyond what the write-ahead log can repair. It is a setting for loading a database that can be loaded again, not for running one.",
				Advice:  "Turn it on.",
				Targets: server(name), SQL: pgSettingFix(name, "on"), Link: "settings",
			})
		}
	}
	var autovacuum string
	if err := db.QueryRowContext(ctx, `SELECT current_setting('autovacuum')`).Scan(&autovacuum); err == nil && autovacuum == "off" {
		out = append(out, Advice{
			ID: "autovacuum-off", Level: "warning", Category: AdviceMaintenance,
			Title:   "Autovacuum is off",
			Detail:  "Nothing reclaims dead rows or refreshes the planner's statistics unless somebody runs VACUUM and ANALYZE by hand, and tables grow without bound until they do.",
			Advice:  "Turn it on; tune it per table if one table needs a different schedule.",
			Targets: server("autovacuum"), SQL: pgSettingFix("autovacuum", "on"), Link: "settings",
		})
	}

	var statements bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_stat_statements')`).Scan(&statements); err == nil && !statements {
		finding := Advice{
			ID: "no-stat-statements", Level: "notice", Category: AdviceMaintenance,
			Title:   "Statement statistics are not being collected",
			Detail:  "pg_stat_statements is not enabled in this database, so nothing here can say which queries cost the most.",
			Targets: []AdviceTarget{{Kind: "extension", Name: "pg_stat_statements"}}, Link: "performance",
		}
		// The statement only helps once the library is loaded at start;
		// offering it before then would create a view that errors.
		if enable := pgStatementsEnable(ctx, db); enable.SQL != "" {
			finding.Advice = "Enable the extension; Performance draws the slowest statements from it."
			finding.SQL = enable.SQL
		} else {
			finding.Advice = enable.Note
		}
		out = append(out, finding)
	}
	var encryption string
	if err := db.QueryRowContext(ctx, `SELECT current_setting('password_encryption')`).Scan(&encryption); err == nil && strings.EqualFold(encryption, "md5") {
		out = append(out, Advice{
			ID: "md5-passwords", Level: "warning", Category: AdviceSecurity,
			Title:   "New passwords are hashed with MD5",
			Detail:  "password_encryption is md5, which every current client and server replaced with SCRAM-SHA-256.",
			Advice:  "Set password_encryption = scram-sha-256 and reset each role's password once.",
			Targets: server("password_encryption"), SQL: pgSettingFix("password_encryption", "scram-sha-256"), Link: "settings",
		})
	}
	// Any role may create objects in public on a server set up before 15,
	// which is how one application's account shadows another's functions.
	var publicCreate bool
	if err := db.QueryRowContext(ctx, `
	  SELECT has_schema_privilege('public', 'public', 'CREATE')
	  WHERE EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = 'public')`).Scan(&publicCreate); err == nil && publicCreate {
		out = append(out, Advice{
			ID: "public-schema-writable", Level: "warning", Category: AdviceSecurity,
			Title:   "Every role can create objects in the public schema",
			Detail:  "Any account that can connect can create a table or a function in public, including one that shadows a function another account calls.",
			Advice:  "Revoke it, and grant CREATE to the roles that own the schema's objects.",
			Targets: []AdviceTarget{{Kind: "schema", Name: "public"}},
			SQL:     "REVOKE CREATE ON SCHEMA public FROM PUBLIC;", Link: "access",
		})
	}
	// The account asking is left out: it is the dashboard's own connection,
	// which has to be a superuser to manage roles and parameters, and a fix
	// that demotes it would be the last thing this page ever did.
	rows, err := db.QueryContext(ctx, `
	  SELECT rolname FROM pg_roles
	  WHERE rolsuper AND rolcanlogin AND rolname NOT IN ('postgres', current_user) ORDER BY rolname`)
	if err == nil {
		supers, targets := []string{}, []AdviceTarget{}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err == nil {
				supers = append(supers, name)
				target := AdviceTarget{Kind: "role", Name: name}
				if q, err := quoteDouble(name); err == nil {
					target.SQL = "ALTER ROLE " + q + " NOSUPERUSER;"
				}
				targets = append(targets, target)
			}
		}
		rows.Close()
		if len(supers) > 0 {
			out = append(out, Advice{
				ID: "extra-superusers", Level: "notice", Category: AdviceSecurity,
				Title:   fmt.Sprintf("%d login role%s besides postgres %s superuser", len(supers), pluralS(len(supers)), map[bool]string{true: "is", false: "are"}[len(supers) == 1]),
				Detail:  "A superuser can read every database, run programs on the server and change any setting; an application account with that grant is one leaked credential from the whole machine.",
				Advice:  "Give applications a role with exactly the database they use, under Access.",
				Objects: supers, Targets: targets, SQL: targetSQL(targets), Link: "access",
			})
		}
	}
	return out
}

// --- MySQL / MariaDB ---------------------------------------------------------

func (d mysqlDialect) Advise(ctx context.Context, db *sql.DB, schema string) ([]Advice, []string, error) {
	out := []Advice{}
	silences := []string{}
	rel := func(name string) string {
		q, err := qualify(d, schema, name)
		if err != nil {
			return ""
		}
		return q
	}
	rows, err := db.QueryContext(ctx, `
	  SELECT TABLE_NAME, ENGINE FROM information_schema.TABLES
	  WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE' AND ENGINE IS NOT NULL AND ENGINE <> 'InnoDB'
	  ORDER BY TABLE_NAME LIMIT 50`, schema)
	if err != nil {
		return nil, nil, err
	}
	nonInnoDB, targets := []string{}, []AdviceTarget{}
	for rows.Next() {
		var table, engine string
		if err := rows.Scan(&table, &engine); err != nil {
			rows.Close()
			return nil, nil, err
		}
		nonInnoDB = append(nonInnoDB, table+" ("+engine+")")
		target := AdviceTarget{Kind: "table", Schema: schema, Name: table, Detail: engine}
		if q := rel(table); q != "" {
			target.SQL = "ALTER TABLE " + q + " ENGINE=InnoDB;"
		}
		targets = append(targets, target)
	}
	rows.Close()
	if len(nonInnoDB) > 0 {
		out = append(out, Advice{
			ID: "non-innodb", Level: "warning", Category: AdviceReliability,
			Title:   fmt.Sprintf("%d table%s not on InnoDB", len(nonInnoDB), pluralS(len(nonInnoDB))),
			Detail:  "MyISAM and its relatives lock the whole table on write, keep no transactions and no foreign keys, and are repaired rather than recovered after a crash.",
			Advice:  "Convert them to InnoDB.",
			Objects: nonInnoDB, Targets: targets, SQL: targetSQL(targets),
		})
	}

	// Space a table's file holds and does not use. InnoDB never gives it
	// back on its own; a rebuild does.
	rows, err = db.QueryContext(ctx, `
	  SELECT TABLE_NAME, DATA_FREE, DATA_LENGTH + INDEX_LENGTH FROM information_schema.TABLES
	  WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE'
	    AND DATA_FREE > 100 * 1024 * 1024 AND DATA_FREE > (DATA_LENGTH + INDEX_LENGTH) / 4
	  ORDER BY DATA_FREE DESC LIMIT 50`, schema)
	if err == nil {
		fragmented, targets := []string{}, []AdviceTarget{}
		for rows.Next() {
			var table string
			var free, size int64
			if err := rows.Scan(&table, &free, &size); err != nil {
				rows.Close()
				return nil, nil, err
			}
			detail := humanBytes(free) + " free beside " + humanBytes(size)
			fragmented = append(fragmented, table+" ("+detail+")")
			target := AdviceTarget{Kind: "table", Schema: schema, Name: table, Detail: detail}
			if q := rel(table); q != "" {
				target.SQL = "OPTIMIZE TABLE " + q + ";"
			}
			targets = append(targets, target)
		}
		rows.Close()
		if len(fragmented) > 0 {
			out = append(out, Advice{
				ID: "table-free-space", Level: "notice", Category: AdviceMaintenance,
				Title:   fmt.Sprintf("%d table%s holding free space", len(fragmented), pluralS(len(fragmented))),
				Detail:  "After large deletes a table's file keeps the space the rows used. It is reused by later inserts and never returned to the disk.",
				Advice:  "Rebuild the tables if the disk needs the space back. The rebuild copies the table, so it needs room for the copy while it runs.",
				Objects: fragmented, Targets: targets, SQL: targetSQL(targets), Link: "performance",
			})
		}
	}

	setting := func(name string) []AdviceTarget { return []AdviceTarget{{Kind: "setting", Name: name}} }
	var connected, max int
	if err := db.QueryRowContext(ctx, `
	  SELECT (SELECT COUNT(*) FROM information_schema.PROCESSLIST), @@max_connections`).Scan(&connected, &max); err == nil && max > 0 && connected*100/max >= 80 {
		out = append(out, Advice{
			ID: "connections-near-limit", Level: "critical", Category: AdviceReliability,
			Title:   fmt.Sprintf("%d of %d connections in use", connected, max),
			Detail:  "When the limit is reached every new client is refused with \"Too many connections\", including this dashboard.",
			Advice:  "Raise max_connections, or put ProxySQL in front of the server.",
			Targets: setting("max_connections"), Link: "settings",
			SQL: mysqlConnectionLimitFix(mysqlIsMariaDB(ctx, db), max),
		})
	}
	var slowLog string
	if err := db.QueryRowContext(ctx, `SELECT @@slow_query_log`).Scan(&slowLog); err == nil && (slowLog == "0" || strings.EqualFold(slowLog, "OFF")) {
		out = append(out, Advice{
			ID: "slow-log-off", Level: "notice", Category: AdviceMaintenance,
			Title:   "The slow query log is off",
			Detail:  "Nothing records which statements take longest, so the first sign of a slow query is a slow page.",
			Advice:  "Turn it on with a threshold that matches the application, or read Performance's statement statistics from performance_schema.",
			Targets: setting("slow_query_log"), Link: "settings",
			SQL: "SET GLOBAL slow_query_log = ON;\nSET GLOBAL long_query_time = 1;",
		})
	}
	var perfSchema string
	if err := db.QueryRowContext(ctx, `SELECT @@performance_schema`).Scan(&perfSchema); err == nil && (perfSchema == "0" || strings.EqualFold(perfSchema, "OFF")) {
		out = append(out, Advice{
			ID: "no-performance-schema", Level: "notice", Category: AdviceMaintenance,
			Title:   "performance_schema is off",
			Detail:  "Statement statistics, index use counts and the lock table all come from performance_schema, and with it off Performance cannot say which queries cost the most or which indexes are unused.",
			Advice:  "Set performance_schema = ON in the server configuration and restart.",
			Targets: setting("performance_schema"),
		})
	}
	rows, err = db.QueryContext(ctx, `
	  SELECT User, Host FROM mysql.user
	  WHERE Super_priv = 'Y' AND Host = '%' ORDER BY User`)
	if err != nil {
		silences = append(silences, "Accounts could not be assessed: "+err.Error())
	} else {
		open, targets := []string{}, []AdviceTarget{}
		for rows.Next() {
			var user, host string
			if err := rows.Scan(&user, &host); err == nil {
				open = append(open, "'"+user+"'@'"+host+"'")
				targets = append(targets, AdviceTarget{Kind: "role", Name: user, Detail: host})
			}
		}
		rows.Close()
		if len(open) > 0 {
			out = append(out, Advice{
				ID: "superuser-any-host", Level: "warning", Category: AdviceSecurity,
				Title:   fmt.Sprintf("%d superuser account%s accept%s sign-in from any host", len(open), pluralS(len(open)), map[bool]string{true: "s", false: ""}[len(open) == 1]),
				Detail:  "An account with every privilege and a host of % is reachable from wherever the port is, which on a published server is the internet.",
				Advice:  "Restrict the host to where the application actually runs, or give the application an account with only its own database.",
				Objects: open, Targets: targets, Link: "access",
			})
		}

		// The anonymous account: a user with no name, which every client
		// that sends none signs in as.
		if arows, err := db.QueryContext(ctx, `SELECT Host FROM mysql.user WHERE User = '' ORDER BY Host`); err == nil {
			anonymous, targets := []string{}, []AdviceTarget{}
			for arows.Next() {
				var host string
				if arows.Scan(&host) != nil {
					continue
				}
				anonymous = append(anonymous, "''@'"+host+"'")
				target := AdviceTarget{Kind: "role", Name: "", Detail: host}
				if account, err := mysqlAnonymousAccount(host); err == nil {
					target.SQL = "DROP USER " + account + ";"
				}
				targets = append(targets, target)
			}
			arows.Close()
			if len(anonymous) > 0 {
				out = append(out, Advice{
					ID: "anonymous-account", Level: "warning", Category: AdviceSecurity,
					Title:   fmt.Sprintf("%d anonymous account%s", len(anonymous), pluralS(len(anonymous))),
					Detail:  "An account with an empty name matches any user name, so a client that mistypes its own is signed in as this one rather than refused.",
					Advice:  "Drop them; no application needs one.",
					Objects: anonymous, Targets: targets, SQL: targetSQL(targets), Link: "access",
				})
			}
		}
	}
	var buffer int64
	if err := db.QueryRowContext(ctx, `SELECT @@innodb_buffer_pool_size`).Scan(&buffer); err == nil && buffer > 0 && buffer < 128*1024*1024 {
		out = append(out, Advice{
			ID: "small-buffer-pool", Level: "notice", Category: AdvicePerformance,
			Title:   fmt.Sprintf("InnoDB buffer pool is %s", humanBytes(buffer)),
			Detail:  "The buffer pool is the cache every read goes through; at the default 128 MB or less a database larger than that reads from disk.",
			Advice:  "Set innodb_buffer_pool_size to most of the memory the server can spare.",
			Targets: setting("innodb_buffer_pool_size"), Link: "settings",
		})
	}
	return out, silences, nil
}

// mysqlConnectionLimitFix raises max_connections, which both engines apply
// at once. MySQL can also persist it; MariaDB cannot from a session, and its
// statement holds until the next restart.
func mysqlConnectionLimitFix(mariadb bool, current int) string {
	if mariadb {
		return "SET GLOBAL max_connections = " + itoa(raisedConnectionLimit(current)) + ";"
	}
	return "SET PERSIST max_connections = " + itoa(raisedConnectionLimit(current)) + ";"
}

// mysqlAnonymousAccount renders the account whose user half is empty, the
// one case where that is legitimate and the one mysqlAccount refuses.
func mysqlAnonymousAccount(host string) (string, error) {
	named, err := mysqlAccount("x", host)
	if err != nil {
		return "", err
	}
	return "''" + strings.TrimPrefix(named, "'x'"), nil
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
