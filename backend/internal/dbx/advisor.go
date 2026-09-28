package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
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
// Two halves. The generic checks walk the introspected structure the Structure
// tab already reads, so they hold on every SQL engine: a table with no primary
// key, a foreign key column with no index. The engine checks read statistics
// only that engine keeps, and each dialect that has them implements Adviser.

type Advice struct {
	ID string `json:"id"`
	// Level uses the finding vocabulary the rest of the product draws:
	// critical, warning, notice.
	Level string `json:"level"`
	// Category is performance, schema, security or maintenance.
	Category string `json:"category"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Advice   string `json:"advice,omitempty"`
	// Objects names what the finding is about, one per table or index.
	Objects []string `json:"objects,omitempty"`
	// SQL is the statement that would fix it, where one statement does.
	SQL string `json:"sql,omitempty"`
}

// Adviser is the engine-specific half. Optional: an engine without it still
// gets the generic checks.
type Adviser interface {
	Advise(ctx context.Context, db *sql.DB, schema string) ([]Advice, error)
}

// AdviseReport is the whole answer: findings ordered worst first, and what was
// looked at, so an empty list can be read as "nothing found" rather than
// "nothing checked".
type AdviseReport struct {
	Findings      []Advice `json:"findings"`
	TablesChecked int      `json:"tablesChecked"`
	// EngineChecks is false where only the generic structure checks ran.
	EngineChecks bool `json:"engineChecks"`
}

// Advise runs every check for a schema.
func Advise(ctx context.Context, db *sql.DB, driver Driver, schema string) (*AdviseReport, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	if schema == "" {
		schema = d.DefaultSchema()
	}
	report := &AdviseReport{Findings: []Advice{}}
	generic, checked, err := adviseStructure(ctx, db, d, schema)
	if err != nil {
		return nil, err
	}
	report.TablesChecked = checked
	report.Findings = append(report.Findings, generic...)
	if a, ok := d.(Adviser); ok {
		report.EngineChecks = true
		engine, err := a.Advise(ctx, db, schema)
		if err != nil {
			return nil, err
		}
		report.Findings = append(report.Findings, engine...)
	}
	rank := map[string]int{"critical": 0, "warning": 1, "notice": 2}
	sort.SliceStable(report.Findings, func(i, j int) bool {
		return rank[report.Findings[i].Level] < rank[report.Findings[j].Level]
	})
	return report, nil
}

// maxAdvisedTables bounds the structure walk: three catalogue queries per
// table is fine for a schema of fifty and an outage for one of five thousand.
const maxAdvisedTables = 300

func adviseStructure(ctx context.Context, db *sql.DB, d Dialect, schema string) ([]Advice, int, error) {
	tables, err := d.Tables(ctx, db, schema)
	if err != nil {
		return nil, 0, err
	}
	noKey := []string{}
	unindexed := []string{}
	unindexedSQL := []string{}
	checked := 0
	for _, t := range tables {
		if !strings.EqualFold(t.Type, "table") && !strings.EqualFold(t.Type, "base table") {
			continue
		}
		if checked >= maxAdvisedTables {
			break
		}
		checked++
		pk, err := d.PrimaryKey(ctx, db, t.Schema, t.Name)
		if err != nil {
			return nil, checked, err
		}
		if len(pk) == 0 {
			noKey = append(noKey, t.Name)
		}
		fks, err := d.ForeignKeys(ctx, db, t.Schema, t.Name)
		if err != nil {
			return nil, checked, err
		}
		if len(fks) == 0 {
			continue
		}
		indexes, err := d.Indexes(ctx, db, t.Schema, t.Name)
		if err != nil {
			return nil, checked, err
		}
		for _, fk := range fks {
			if indexCovers(indexes, fk.Columns) {
				continue
			}
			unindexed = append(unindexed, t.Name+"("+strings.Join(fk.Columns, ", ")+")")
			if stmt, err := createIndexSQL(d, t.Schema, t.Name, fk.Columns); err == nil {
				unindexedSQL = append(unindexedSQL, stmt)
			}
		}
	}
	out := []Advice{}
	if len(noKey) > 0 {
		out = append(out, Advice{
			ID: "no-primary-key", Level: "warning", Category: "schema",
			Title:   fmt.Sprintf("%d table%s with no primary key", len(noKey), pluralS(len(noKey))),
			Detail:  "A table without a primary key cannot be edited row by row from this dashboard, is skipped by logical replication, and gives an ORM nothing to identify a row by.",
			Advice:  "Add an identity column, or declare the column that already identifies the row as the key.",
			Objects: noKey,
		})
	}
	if len(unindexed) > 0 {
		out = append(out, Advice{
			ID: "unindexed-foreign-key", Level: "warning", Category: "performance",
			Title:   fmt.Sprintf("%d foreign key%s with no index", len(unindexed), pluralS(len(unindexed))),
			Detail:  "Every delete or update of the referenced row scans the whole referencing table to check the constraint, and every join on the key does the same.",
			Advice:  "Create an index on the referencing columns.",
			Objects: unindexed,
			SQL:     strings.Join(unindexedSQL, "\n"),
		})
	}
	return out, checked, nil
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

func createIndexSQL(d Dialect, schema, table string, columns []string) (string, error) {
	t, err := d.QuoteIdent(table)
	if err != nil {
		return "", err
	}
	if schema != "" && d.Driver() != DriverMySQL && d.Driver() != DriverSQLite {
		s, err := d.QuoteIdent(schema)
		if err != nil {
			return "", err
		}
		t = s + "." + t
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

func (postgresDialect) Advise(ctx context.Context, db *sql.DB, schema string) ([]Advice, error) {
	out := []Advice{}

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
		return nil, err
	}
	unused, unusedSQL := []string{}, []string{}
	var unusedBytes int64
	for rows.Next() {
		var index, table string
		var size int64
		if err := rows.Scan(&index, &table, &size); err != nil {
			rows.Close()
			return nil, err
		}
		unused = append(unused, table+"."+index)
		unusedBytes += size
		if q, err := quoteDouble(index); err == nil {
			s, _ := quoteDouble(schema)
			unusedSQL = append(unusedSQL, "DROP INDEX "+s+"."+q+";")
		}
	}
	rows.Close()
	if len(unused) > 0 {
		out = append(out, Advice{
			ID: "unused-index", Level: "notice", Category: "performance",
			Title:   fmt.Sprintf("%d index%s never read since statistics were reset", len(unused), map[bool]string{true: "", false: "es"}[len(unused) == 1]),
			Detail:  fmt.Sprintf("Together they take %s and cost every write to their tables, and the planner has not used one of them.", humanBytes(unusedBytes)),
			Advice:  "Drop the ones that are not there for a reason the statistics cannot see — a report run once a year, a replica's workload.",
			Objects: unused,
			SQL:     strings.Join(unusedSQL, "\n"),
		})
	}

	// Two indexes with the same definition on the same table.
	rows, err = db.QueryContext(ctx, `
	  SELECT c.relname, string_agg(i.relname, ', ' ORDER BY i.relname)
	  FROM pg_index x
	  JOIN pg_class c ON c.oid = x.indrelid
	  JOIN pg_class i ON i.oid = x.indexrelid
	  JOIN pg_namespace n ON n.oid = c.relnamespace
	  WHERE n.nspname = $1
	  GROUP BY c.relname, x.indrelid, x.indkey::text, x.indclass::text, x.indoption::text, COALESCE(pg_get_expr(x.indpred, x.indrelid), '')
	  HAVING count(*) > 1`, schema)
	if err != nil {
		return nil, err
	}
	dupes := []string{}
	for rows.Next() {
		var table, names string
		if err := rows.Scan(&table, &names); err != nil {
			rows.Close()
			return nil, err
		}
		dupes = append(dupes, table+": "+names)
	}
	rows.Close()
	if len(dupes) > 0 {
		out = append(out, Advice{
			ID: "duplicate-index", Level: "warning", Category: "performance",
			Title:   fmt.Sprintf("%d set%s of identical indexes", len(dupes), pluralS(len(dupes))),
			Detail:  "Each set covers the same columns in the same order with the same options; the second copy costs every write and buys nothing.",
			Advice:  "Keep one of each set.",
			Objects: dupes,
		})
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
		return nil, err
	}
	never, bloated, bloatedSQL := []string{}, []string{}, []string{}
	for rows.Next() {
		var table string
		var live, dead int64
		var neverAnalysed bool
		if err := rows.Scan(&table, &live, &dead, &neverAnalysed); err != nil {
			rows.Close()
			return nil, err
		}
		if neverAnalysed {
			never = append(never, table)
		}
		if dead > 10000 && dead > live/5 {
			bloated = append(bloated, fmt.Sprintf("%s (%d dead of %d live)", table, dead, live))
			if q, err := quoteDouble(table); err == nil {
				s, _ := quoteDouble(schema)
				bloatedSQL = append(bloatedSQL, "VACUUM ANALYZE "+s+"."+q+";")
			}
		}
	}
	rows.Close()
	if len(never) > 0 {
		out = append(out, Advice{
			ID: "never-analysed", Level: "warning", Category: "maintenance",
			Title:   fmt.Sprintf("%d table%s the planner has no statistics for", len(never), pluralS(len(never))),
			Detail:  "Without statistics the planner guesses row counts, and a guess on a table of a thousand rows or more is how a query picks a sequential scan over the index that exists for it.",
			Advice:  "Run ANALYZE once; autovacuum keeps it current afterwards.",
			Objects: never,
			SQL:     "ANALYZE;",
		})
	}
	if len(bloated) > 0 {
		out = append(out, Advice{
			ID: "dead-rows", Level: "warning", Category: "maintenance",
			Title:   fmt.Sprintf("%d table%s carrying dead rows", len(bloated), pluralS(len(bloated))),
			Detail:  "More than a fifth of the table is rows that were updated or deleted and not yet reclaimed, which every scan still reads past.",
			Advice:  "Vacuum them, and check autovacuum is keeping up with the write rate on these tables.",
			Objects: bloated,
			SQL:     strings.Join(bloatedSQL, "\n"),
		})
	}

	// Integer sequences approaching their ceiling — the failure that arrives
	// as "duplicate key" on a Tuesday afternoon with no warning.
	rows, err = db.QueryContext(ctx, `
	  SELECT s.sequencename, COALESCE(s.last_value, 0), s.max_value
	  FROM pg_sequences s
	  WHERE s.schemaname = $1 AND s.last_value IS NOT NULL
	    AND s.last_value::numeric > s.max_value::numeric * 0.8`, schema)
	if err == nil {
		exhausting := []string{}
		for rows.Next() {
			var name string
			var last, max int64
			if err := rows.Scan(&name, &last, &max); err != nil {
				rows.Close()
				return nil, err
			}
			exhausting = append(exhausting, fmt.Sprintf("%s (%d of %d)", name, last, max))
		}
		rows.Close()
		if len(exhausting) > 0 {
			out = append(out, Advice{
				ID: "sequence-exhaustion", Level: "critical", Category: "schema",
				Title:   fmt.Sprintf("%d sequence%s past 80%% of its ceiling", len(exhausting), pluralS(len(exhausting))),
				Detail:  "When a sequence reaches its maximum every insert into its table fails.",
				Advice:  "Change the column and the sequence to bigint before it runs out.",
				Objects: exhausting,
			})
		}
	}

	// Server-wide readings: connections against the limit, the cache hit
	// rate, sessions holding transactions open, and whether the statement
	// statistics this page would draw on are being collected at all.
	var used, max int
	if err := db.QueryRowContext(ctx, `
	  SELECT (SELECT count(*) FROM pg_stat_activity),
	         current_setting('max_connections')::int`).Scan(&used, &max); err == nil && max > 0 {
		if used*100/max >= 80 {
			out = append(out, Advice{
				ID: "connections-near-limit", Level: "critical", Category: "performance",
				Title:  fmt.Sprintf("%d of %d connections in use", used, max),
				Detail: "When the limit is reached every new client is refused with \"too many connections\", including this dashboard.",
				Advice: "Put a pooler (PgBouncer) in front of the server, or raise max_connections and restart.",
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
				ID: "cache-hit-ratio", Level: "warning", Category: "performance",
				Title:  fmt.Sprintf("Cache hit ratio is %.1f%%", ratio),
				Detail: "More than a tenth of block reads went to disk rather than shared memory, which on a server whose working set should fit means shared_buffers is too small for it.",
				Advice: "Raise shared_buffers (a quarter of memory is the usual start) and effective_cache_size, then restart.",
			})
		}
	}
	var idleInTx int
	if err := db.QueryRowContext(ctx, `
	  SELECT count(*) FROM pg_stat_activity
	  WHERE state = 'idle in transaction' AND state_change < now() - interval '5 minutes'`).Scan(&idleInTx); err == nil && idleInTx > 0 {
		out = append(out, Advice{
			ID: "idle-in-transaction", Level: "warning", Category: "performance",
			Title:  fmt.Sprintf("%d session%s idle in a transaction for over five minutes", idleInTx, pluralS(idleInTx)),
			Detail: "An open transaction holds its locks and stops vacuum reclaiming anything newer than it, however long it sits there doing nothing.",
			Advice: "Set idle_in_transaction_session_timeout so the server ends them, and find the client that forgets to commit. Monitor lists them.",
		})
	}
	var statements bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_stat_statements')`).Scan(&statements); err == nil && !statements {
		out = append(out, Advice{
			ID: "no-stat-statements", Level: "notice", Category: "maintenance",
			Title:  "Statement statistics are not being collected",
			Detail: "pg_stat_statements is not installed, so nothing here can say which queries cost the most.",
			Advice: "Add it to shared_preload_libraries, restart, then enable it under Server → Extensions; Monitor draws the slowest statements from it.",
			SQL:    "CREATE EXTENSION IF NOT EXISTS pg_stat_statements;",
		})
	}
	var encryption string
	if err := db.QueryRowContext(ctx, `SELECT current_setting('password_encryption')`).Scan(&encryption); err == nil && strings.EqualFold(encryption, "md5") {
		out = append(out, Advice{
			ID: "md5-passwords", Level: "warning", Category: "security",
			Title:  "New passwords are hashed with MD5",
			Detail: "password_encryption is md5, which every current client and server replaced with SCRAM-SHA-256.",
			Advice: "Set password_encryption = scram-sha-256 and reset each role's password once.",
		})
	}
	rows, err = db.QueryContext(ctx, `
	  SELECT rolname FROM pg_roles
	  WHERE rolsuper AND rolcanlogin AND rolname NOT IN ('postgres') ORDER BY rolname`)
	if err == nil {
		supers := []string{}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err == nil {
				supers = append(supers, name)
			}
		}
		rows.Close()
		if len(supers) > 0 {
			out = append(out, Advice{
				ID: "extra-superusers", Level: "notice", Category: "security",
				Title:   fmt.Sprintf("%d login role%s besides postgres %s superuser", len(supers), pluralS(len(supers)), map[bool]string{true: "is", false: "are"}[len(supers) == 1]),
				Detail:  "A superuser can read every database, run programs on the server and change any setting; an application account with that grant is one leaked credential from the whole machine.",
				Advice:  "Give applications a role with exactly the database they use, under Server → Roles.",
				Objects: supers,
			})
		}
	}
	return out, nil
}

// --- MySQL / MariaDB ---------------------------------------------------------

func (mysqlDialect) Advise(ctx context.Context, db *sql.DB, schema string) ([]Advice, error) {
	out := []Advice{}
	rows, err := db.QueryContext(ctx, `
	  SELECT TABLE_NAME, ENGINE FROM information_schema.TABLES
	  WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE' AND ENGINE IS NOT NULL AND ENGINE <> 'InnoDB'
	  ORDER BY TABLE_NAME LIMIT 50`, schema)
	if err != nil {
		return nil, err
	}
	nonInnoDB, sqls := []string{}, []string{}
	for rows.Next() {
		var table, engine string
		if err := rows.Scan(&table, &engine); err != nil {
			rows.Close()
			return nil, err
		}
		nonInnoDB = append(nonInnoDB, table+" ("+engine+")")
		if q, err := quoteBacktick(table); err == nil {
			sqls = append(sqls, "ALTER TABLE "+q+" ENGINE=InnoDB;")
		}
	}
	rows.Close()
	if len(nonInnoDB) > 0 {
		out = append(out, Advice{
			ID: "non-innodb", Level: "warning", Category: "schema",
			Title:   fmt.Sprintf("%d table%s not on InnoDB", len(nonInnoDB), pluralS(len(nonInnoDB))),
			Detail:  "MyISAM and its relatives lock the whole table on write, keep no transactions and no foreign keys, and are repaired rather than recovered after a crash.",
			Advice:  "Convert them to InnoDB.",
			Objects: nonInnoDB,
			SQL:     strings.Join(sqls, "\n"),
		})
	}

	var connected, max int
	if err := db.QueryRowContext(ctx, `
	  SELECT (SELECT COUNT(*) FROM information_schema.PROCESSLIST), @@max_connections`).Scan(&connected, &max); err == nil && max > 0 && connected*100/max >= 80 {
		out = append(out, Advice{
			ID: "connections-near-limit", Level: "critical", Category: "performance",
			Title:  fmt.Sprintf("%d of %d connections in use", connected, max),
			Detail: "When the limit is reached every new client is refused with \"Too many connections\", including this dashboard.",
			Advice: "Raise max_connections, or put ProxySQL in front of the server.",
		})
	}
	var slowLog string
	if err := db.QueryRowContext(ctx, `SELECT @@slow_query_log`).Scan(&slowLog); err == nil && (slowLog == "0" || strings.EqualFold(slowLog, "OFF")) {
		out = append(out, Advice{
			ID: "slow-log-off", Level: "notice", Category: "maintenance",
			Title:  "The slow query log is off",
			Detail: "Nothing records which statements take longest, so the first sign of a slow query is a slow page.",
			Advice: "Turn it on with a threshold that matches the application, or read Monitor's statement statistics from performance_schema.",
			SQL:    "SET GLOBAL slow_query_log = ON;\nSET GLOBAL long_query_time = 1;",
		})
	}
	var perfSchema string
	if err := db.QueryRowContext(ctx, `SELECT @@performance_schema`).Scan(&perfSchema); err == nil && (perfSchema == "0" || strings.EqualFold(perfSchema, "OFF")) {
		out = append(out, Advice{
			ID: "no-performance-schema", Level: "notice", Category: "maintenance",
			Title:  "performance_schema is off",
			Detail: "Statement statistics come from performance_schema, and with it off Monitor cannot say which queries cost the most.",
			Advice: "Set performance_schema = ON in the server configuration and restart.",
		})
	}
	rows, err = db.QueryContext(ctx, `
	  SELECT User, Host FROM mysql.user
	  WHERE Super_priv = 'Y' AND Host = '%' ORDER BY User`)
	if err == nil {
		open := []string{}
		for rows.Next() {
			var user, host string
			if err := rows.Scan(&user, &host); err == nil {
				open = append(open, "'"+user+"'@'"+host+"'")
			}
		}
		rows.Close()
		if len(open) > 0 {
			out = append(out, Advice{
				ID: "superuser-any-host", Level: "warning", Category: "security",
				Title:   fmt.Sprintf("%d superuser account%s accept%s sign-in from any host", len(open), pluralS(len(open)), map[bool]string{true: "s", false: ""}[len(open) == 1]),
				Detail:  "An account with every privilege and a host of % is reachable from wherever the port is, which on a published server is the internet.",
				Advice:  "Restrict the host to where the application actually runs, or give the application an account with only its own database.",
				Objects: open,
			})
		}
	}
	var buffer int64
	if err := db.QueryRowContext(ctx, `SELECT @@innodb_buffer_pool_size`).Scan(&buffer); err == nil && buffer > 0 && buffer < 128*1024*1024 {
		out = append(out, Advice{
			ID: "small-buffer-pool", Level: "notice", Category: "performance",
			Title:  fmt.Sprintf("InnoDB buffer pool is %s", humanBytes(buffer)),
			Detail: "The buffer pool is the cache every read goes through; at the default 128 MB or less a database larger than that reads from disk.",
			Advice: "Set innodb_buffer_pool_size to most of the memory the server can spare.",
		})
	}
	return out, nil
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
