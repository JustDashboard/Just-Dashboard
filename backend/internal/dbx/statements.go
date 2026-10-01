package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Statement statistics: which queries cost the server the most, summed over
// every time they ran.
//
// The activity list says what is running now; this says what has been
// running all week. They answer different questions — a query that takes ten
// milliseconds and runs ten thousand times an hour never appears in the
// activity list and is the top row here.
//
// Five engines keep this. Postgres needs pg_stat_statements loaded and the
// extension created; MySQL and MariaDB keep it in performance_schema;
// ClickHouse keeps every finished query in its query log, which is summed
// here by shape; SQL Server keeps it beside each cached plan and Oracle beside
// each cursor in the shared pool. SQLite keeps none and says so.
//
// The text returned is the statement's shape, never one execution of it. The
// list is on the read surface, and a literal in a WHERE clause is somebody's
// e-mail address or a session token: PostgreSQL and MySQL hand back text with
// the constants already replaced, ClickHouse is asked to do the same, and for
// SQL Server and Oracle — which keep the text as it was sent — the constants
// are replaced here before the text leaves this package.

type Statement struct {
	ID      string  `json:"id"`
	Query   string  `json:"query"`
	Calls   int64   `json:"calls"`
	TotalMs float64 `json:"totalMs"`
	MeanMs  float64 `json:"meanMs"`
	MaxMs   float64 `json:"maxMs,omitempty"`
	Rows    int64   `json:"rows"`
	// HitRatio is the share of block reads served from cache, where the
	// engine reports it; -1 where it does not.
	HitRatio float64 `json:"hitRatio"`
	// Share is this statement's part of everything the engine tracked,
	// between 0 and 1. It is of the whole, not of the rows returned: the
	// tenth row of ten can still be half the server's work.
	Share float64 `json:"share"`
}

// StatementsEnable says what stands between this server and its statement
// statistics, in terms the page can act on.
type StatementsEnable struct {
	// Extension is the extension to create through the extensions route,
	// which is the same act and needs the same capability.
	Extension string `json:"extension,omitempty"`
	// SQL is the statement that enables collection, for the engines where it
	// is one statement.
	SQL string `json:"sql,omitempty"`
	// Available is false when the server does not have the module at all.
	Available bool `json:"available"`
	// Preloaded is false when the module is installed but not loaded at
	// start, so creating the extension would give a view that errors.
	Preloaded bool   `json:"preloaded"`
	Note      string `json:"note,omitempty"`
}

type StatementsReport struct {
	Statements []Statement `json:"statements"`
	Supported  bool        `json:"supported"`
	Reason     string      `json:"reason,omitempty"`
	// TotalMs is the sum over every statement the engine tracks, so a row's
	// share of the whole can be drawn.
	TotalMs float64 `json:"totalMs"`
	// Sort is the order the rows came back in, after defaulting.
	Sort string `json:"sort"`
	// Resettable is true when the statistics can be zeroed from here.
	Resettable bool `json:"resettable"`
	// Since is when the statistics last started from zero, where recorded.
	Since *time.Time `json:"since,omitempty"`
	// Enable is set when statistics are not being collected and could be.
	Enable *StatementsEnable `json:"enable,omitempty"`
}

// The orders the list can come back in. A closed set: the value becomes a
// column name in ORDER BY, and only these map to one.
const (
	StatementsByTotal = "total"
	StatementsByMean  = "mean"
	StatementsByCalls = "calls"
	StatementsByRows  = "rows"
	StatementsByMax   = "max"
)

// StatementSorts lists the accepted orders, default first.
func StatementSorts() []string {
	return []string{StatementsByTotal, StatementsByMean, StatementsByCalls, StatementsByRows, StatementsByMax}
}

// The bounds on how many statements one read returns.
const (
	defaultStatementsLimit = 25
	maxStatementsLimit     = 200
)

type StatementsOptions struct {
	Sort  string
	Limit int
}

func (o StatementsOptions) normalised() (StatementsOptions, error) {
	if o.Sort == "" {
		o.Sort = StatementsByTotal
	}
	known := false
	for _, s := range StatementSorts() {
		known = known || s == o.Sort
	}
	if !known {
		return o, fmt.Errorf("sort must be one of %s", strings.Join(StatementSorts(), ", "))
	}
	switch {
	case o.Limit <= 0:
		o.Limit = defaultStatementsLimit
	case o.Limit > maxStatementsLimit:
		o.Limit = maxStatementsLimit
	}
	return o, nil
}

// StatementStatser is the optional dialect half.
type StatementStatser interface {
	Statements(ctx context.Context, db *sql.DB, opts StatementsOptions) (*StatementsReport, error)
}

// StatementResetter is the optional half that zeroes the statistics.
type StatementResetter interface {
	ResetStatements(ctx context.Context, db *sql.DB) (statement string, err error)
}

// ErrBadStatementsOption marks a sort the request named that is not one of
// the closed set.
type ErrBadStatementsOption struct{ msg string }

func (e ErrBadStatementsOption) Error() string { return e.msg }

func TopStatements(ctx context.Context, db *sql.DB, driver Driver, opts StatementsOptions) (*StatementsReport, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	opts, err = opts.normalised()
	if err != nil {
		return nil, ErrBadStatementsOption{msg: err.Error()}
	}
	s, ok := d.(StatementStatser)
	if !ok {
		return &StatementsReport{Statements: []Statement{}, Sort: opts.Sort, Reason: "this engine keeps no per-statement statistics"}, nil
	}
	out, err := s.Statements(ctx, db, opts)
	if err != nil {
		return nil, err
	}
	out.Sort = opts.Sort
	if out.Statements == nil {
		out.Statements = []Statement{}
	}
	for i := range out.Statements {
		if out.TotalMs > 0 {
			out.Statements[i].Share = out.Statements[i].TotalMs / out.TotalMs
		}
	}
	if _, ok := d.(StatementResetter); ok && out.Supported {
		out.Resettable = true
	}
	return out, nil
}

// ResetStatements zeroes the engine's statement statistics and returns the
// statement that did it.
//
// Nothing in the database changes; what is lost is the history the list was
// drawn from. That is the point of it — a fix is measured by resetting and
// reading what accumulates afterwards — and it is why this is an ordinary
// write and not a destructive one.
func ResetStatements(ctx context.Context, db *sql.DB, driver Driver) (string, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return "", err
	}
	r, ok := d.(StatementResetter)
	if !ok {
		return "", ErrUnsupported
	}
	return r.ResetStatements(ctx, db)
}

// --- PostgreSQL --------------------------------------------------------------

// pgStatementsEnable works out why there are no statistics. The module is
// useless unless it was loaded at server start, so "create the extension" is
// only the answer when shared_preload_libraries already names it.
func pgStatementsEnable(ctx context.Context, db *sql.DB) *StatementsEnable {
	out := &StatementsEnable{Extension: "pg_stat_statements"}
	_ = db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'pg_stat_statements')`).Scan(&out.Available)
	var preload sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT current_setting('shared_preload_libraries', true)`).Scan(&preload); err == nil {
		for _, lib := range strings.Split(preload.String, ",") {
			if strings.TrimSpace(lib) == "pg_stat_statements" {
				out.Preloaded = true
			}
		}
	}
	switch {
	case !out.Available:
		out.Note = "The pg_stat_statements module is not installed on this server; it ships in the contrib package of the same PostgreSQL version."
	case !out.Preloaded:
		out.Note = "Add pg_stat_statements to shared_preload_libraries and restart the server first; the extension only records once it is loaded at start."
	default:
		out.SQL = "CREATE EXTENSION IF NOT EXISTS pg_stat_statements;"
	}
	return out
}

func (postgresDialect) Statements(ctx context.Context, db *sql.DB, opts StatementsOptions) (*StatementsReport, error) {
	var installed bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_stat_statements')`).Scan(&installed); err != nil {
		return nil, err
	}
	if !installed {
		return &StatementsReport{Statements: []Statement{}, Reason: "pg_stat_statements is not enabled in this database",
			Enable: pgStatementsEnable(ctx, db)}, nil
	}
	// Column names changed in 13 (total_time became total_exec_time). The
	// view is asked which it has rather than the version being parsed.
	timeCol, meanCol, maxCol := "total_exec_time", "mean_exec_time", "max_exec_time"
	var modern bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
	  WHERE table_name = 'pg_stat_statements' AND column_name = 'total_exec_time')`).Scan(&modern); err == nil && !modern {
		timeCol, meanCol, maxCol = "total_time", "mean_time", "max_time"
	}
	order := map[string]string{
		StatementsByTotal: timeCol, StatementsByMean: meanCol, StatementsByCalls: "calls",
		StatementsByRows: "rows", StatementsByMax: maxCol,
	}[opts.Sort]
	// queryid is NULL for a statement another role ran when this one may not
	// read all statistics, and for utility statements with tracking off; the
	// text is NULL in the same rows.
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
	  SELECT COALESCE(queryid::text, ''), COALESCE(LEFT(query, 1000), ''), calls, %s, %s, %s, rows,
	         CASE WHEN shared_blks_hit + shared_blks_read > 0
	              THEN shared_blks_hit::float8 / (shared_blks_hit + shared_blks_read) ELSE -1 END,
	         (SELECT COALESCE(sum(%s), 0) FROM pg_stat_statements
	          WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database()))
	  FROM pg_stat_statements
	  WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
	  ORDER BY %s DESC LIMIT $1`, timeCol, meanCol, maxCol, timeCol, order), opts.Limit)
	if err != nil {
		// The extension exists but the view is not readable by this role, or
		// the library is not preloaded: say so rather than fail the page.
		return &StatementsReport{Statements: []Statement{}, Reason: err.Error(), Enable: pgStatementsEnable(ctx, db)}, nil
	}
	defer rows.Close()
	out := &StatementsReport{Statements: []Statement{}, Supported: true}
	for rows.Next() {
		var s Statement
		if err := rows.Scan(&s.ID, &s.Query, &s.Calls, &s.TotalMs, &s.MeanMs, &s.MaxMs, &s.Rows, &s.HitRatio, &out.TotalMs); err != nil {
			return nil, err
		}
		out.Statements = append(out.Statements, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// When the counters were last zeroed, on the versions that record it (14+).
	// The catalogue is asked whether the view is there; asking the view itself
	// would log an error on every older server each time the page opened.
	var recorded bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('pg_stat_statements_info') IS NOT NULL`).Scan(&recorded); err == nil && recorded {
		var since sql.NullTime
		if err := db.QueryRowContext(ctx, `SELECT stats_reset FROM pg_stat_statements_info`).Scan(&since); err == nil {
			out.Since = timePtr(since)
		}
	}
	return out, nil
}

// ResetStatements zeroes the extension's counters for every database: the
// function takes filters from 12 on, and the argument-less form is the one
// every version has. Only a superuser, or a role granted the function, may.
func (postgresDialect) ResetStatements(ctx context.Context, db *sql.DB) (string, error) {
	const stmt = "SELECT pg_stat_statements_reset()"
	_, err := db.ExecContext(ctx, stmt)
	return stmt, err
}

// --- MySQL / MariaDB ---------------------------------------------------------

func (mysqlDialect) Statements(ctx context.Context, db *sql.DB, opts StatementsOptions) (*StatementsReport, error) {
	order := map[string]string{
		StatementsByTotal: "SUM_TIMER_WAIT", StatementsByMean: "AVG_TIMER_WAIT", StatementsByCalls: "COUNT_STAR",
		StatementsByRows: "SUM_ROWS_SENT", StatementsByMax: "MAX_TIMER_WAIT",
	}[opts.Sort]
	const scope = `(SCHEMA_NAME IS NULL OR SCHEMA_NAME = DATABASE() OR DATABASE() IS NULL)`
	rows, err := db.QueryContext(ctx, `
	  SELECT COALESCE(DIGEST, ''), COALESCE(LEFT(DIGEST_TEXT, 1000), ''), COUNT_STAR,
	         SUM_TIMER_WAIT / 1e9, AVG_TIMER_WAIT / 1e9, MAX_TIMER_WAIT / 1e9, SUM_ROWS_SENT,
	         (SELECT COALESCE(SUM(SUM_TIMER_WAIT), 0) / 1e9
	          FROM performance_schema.events_statements_summary_by_digest WHERE `+scope+`)
	  FROM performance_schema.events_statements_summary_by_digest
	  WHERE `+scope+`
	  ORDER BY `+order+` DESC LIMIT ?`, opts.Limit)
	if err != nil {
		return &StatementsReport{Statements: []Statement{}, Reason: err.Error()}, nil
	}
	defer rows.Close()
	out := &StatementsReport{Statements: []Statement{}, Supported: true}
	for rows.Next() {
		var s Statement
		if err := rows.Scan(&s.ID, &s.Query, &s.Calls, &s.TotalMs, &s.MeanMs, &s.MaxMs, &s.Rows, &out.TotalMs); err != nil {
			return nil, err
		}
		s.HitRatio = -1
		out.Statements = append(out.Statements, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out.Statements) == 0 {
		// The table exists on every server and is empty on one started with
		// performance_schema off, which is how MariaDB ships. An empty list
		// there is not "no statements have run".
		var enabled sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT @@performance_schema`).Scan(&enabled); err == nil &&
			(enabled.String == "0" || strings.EqualFold(enabled.String, "OFF")) {
			return &StatementsReport{Statements: []Statement{},
				Reason: "performance_schema is off on this server, so it records no statement statistics",
				Enable: &StatementsEnable{Available: true,
					Note: "Set performance_schema = ON in the server's configuration file and restart it; the setting cannot be changed while the server runs."}}, nil
		}
	}
	return out, nil
}

func (mysqlDialect) ResetStatements(ctx context.Context, db *sql.DB) (string, error) {
	const stmt = "TRUNCATE TABLE performance_schema.events_statements_summary_by_digest"
	_, err := db.ExecContext(ctx, stmt)
	return stmt, err
}

// --- ClickHouse --------------------------------------------------------------

// clickhouseStatementsWindow is how far back the query log is summed. The log
// is a table that grows without bound; a day is recent enough to describe the
// workload and bounded enough to read on every poll.
const clickhouseStatementsWindow = "1 DAY"

// Statements sums the query log by normalised shape. There is nothing to
// reset: the log is the server's record, and its retention is the server's
// own setting. The text shown is the shape too — normalizeQuery puts a ? where
// each literal was — because the log holds every execution as it was sent.
func (clickhouseDialect) Statements(ctx context.Context, db *sql.DB, opts StatementsOptions) (*StatementsReport, error) {
	order := map[string]string{
		StatementsByTotal: "total", StatementsByMean: "mean", StatementsByCalls: "calls",
		StatementsByRows: "result_rows", StatementsByMax: "longest",
	}[opts.Sort]
	const scope = `type = 'QueryFinish' AND event_time > now() - INTERVAL ` + clickhouseStatementsWindow + `
	      AND current_database = currentDatabase() AND query NOT LIKE '%system.query_log%'`
	rows, err := db.QueryContext(ctx, `
	  SELECT toString(normalized_query_hash), substring(normalizeQuery(any(query)), 1, 1000), toInt64(count()) AS calls,
	         toFloat64(sum(query_duration_ms)) AS total, toFloat64(avg(query_duration_ms)) AS mean,
	         toFloat64(max(query_duration_ms)) AS longest, toInt64(sum(result_rows)) AS result_rows,
	         (SELECT toFloat64(sum(query_duration_ms)) FROM system.query_log WHERE `+scope+`)
	  FROM system.query_log
	  WHERE `+scope+`
	  GROUP BY normalized_query_hash
	  ORDER BY `+order+` DESC LIMIT ?`, opts.Limit)
	if err != nil {
		return &StatementsReport{Statements: []Statement{}, Reason: err.Error()}, nil
	}
	defer rows.Close()
	out := &StatementsReport{Statements: []Statement{}, Supported: true}
	since := time.Now().UTC().Add(-24 * time.Hour)
	out.Since = &since
	for rows.Next() {
		var s Statement
		if err := rows.Scan(&s.ID, &s.Query, &s.Calls, &s.TotalMs, &s.MeanMs, &s.MaxMs, &s.Rows, &out.TotalMs); err != nil {
			return nil, err
		}
		s.HitRatio = -1
		out.Statements = append(out.Statements, s)
	}
	return out, rows.Err()
}

// --- SQL Server --------------------------------------------------------------

// Statements sums sys.dm_exec_query_stats by query hash, which is SQL Server's
// own notion of "the same statement with different constants".
//
// The view describes the plan cache: a statement's figures run from when its
// plan was compiled and go when the plan is evicted or the server restarts, so
// there is no single moment the statistics started from and nothing here
// reports one. Zeroing them would mean emptying the plan cache, which makes
// every statement on the server compile again — not a thing to offer behind a
// "reset" button, so this engine has none. The view needs VIEW SERVER STATE;
// a login without it is told so rather than shown an error.
func (mssqlDialect) Statements(ctx context.Context, db *sql.DB, opts StatementsOptions) (*StatementsReport, error) {
	order := map[string]string{
		StatementsByTotal: "total_ms", StatementsByMean: "mean_ms", StatementsByCalls: "calls",
		StatementsByRows: "rows_", StatementsByMax: "max_ms",
	}[opts.Sort]
	// A plan belongs to the database it was compiled in; that is the only place
	// the view says which database a statement ran against.
	rows, err := db.QueryContext(ctx, `
	  SELECT TOP (@p1) id, ISNULL(text, ''), calls, total_ms, ISNULL(mean_ms, 0), max_ms, rows_, hit, grand
	  FROM (
	    SELECT CONVERT(VARCHAR(34), a.query_hash, 1) AS id,
	           a.calls, a.total_ms, a.total_ms / NULLIF(a.calls, 0) AS mean_ms, a.max_ms, a.rows_,
	           CASE WHEN a.logical_reads > 0
	                THEN CAST(a.logical_reads - a.physical_reads AS FLOAT) / a.logical_reads ELSE -1 END AS hit,
	           SUM(a.total_ms) OVER () AS grand,
	           (SELECT TOP 1 LEFT(SUBSTRING(t.text, r.statement_start_offset / 2 + 1,
	                     (CASE WHEN r.statement_end_offset = -1 THEN DATALENGTH(t.text)
	                           ELSE r.statement_end_offset END - r.statement_start_offset) / 2 + 1), 4000)
	            FROM sys.dm_exec_query_stats r
	            CROSS APPLY sys.dm_exec_sql_text(r.sql_handle) t
	            WHERE r.query_hash = a.query_hash
	            ORDER BY r.total_elapsed_time DESC) AS text
	    FROM (
	      SELECT s.query_hash, SUM(s.execution_count) AS calls,
	             CAST(SUM(s.total_elapsed_time) AS FLOAT) / 1000 AS total_ms,
	             CAST(MAX(s.max_elapsed_time) AS FLOAT) / 1000 AS max_ms,
	             SUM(s.total_rows) AS rows_,
	             SUM(s.total_logical_reads) AS logical_reads,
	             SUM(s.total_physical_reads) AS physical_reads
	      FROM sys.dm_exec_query_stats s
	      CROSS APPLY sys.dm_exec_plan_attributes(s.plan_handle) pa
	      WHERE pa.attribute = 'dbid' AND CAST(pa.value AS INT) = DB_ID()
	      GROUP BY s.query_hash
	    ) a
	  ) x
	  ORDER BY `+order+` DESC`, opts.Limit)
	if err != nil {
		return &StatementsReport{Statements: []Statement{}, Reason: "The plan cache's statistics could not be read (they need VIEW SERVER STATE): " + err.Error()}, nil
	}
	defer rows.Close()
	out := &StatementsReport{Statements: []Statement{}, Supported: true}
	for rows.Next() {
		var s Statement
		if err := rows.Scan(&s.ID, &s.Query, &s.Calls, &s.TotalMs, &s.MeanMs, &s.MaxMs, &s.Rows, &s.HitRatio, &out.TotalMs); err != nil {
			return nil, err
		}
		s.Query = clipText(statementShape(s.Query, true), 1000)
		out.Statements = append(out.Statements, s)
	}
	return out, rows.Err()
}

// --- Oracle ------------------------------------------------------------------

// Statements reads v$sqlstats: one row per statement in the shared pool, kept
// for as long as the cursor is. Oracle records no longest single execution, so
// that order is refused rather than answered with another; and the recursive
// statements the server runs on its own behalf are left out where it can be
// told which they are, because on a quiet database they are most of the list.
// The view needs a grant an application schema rarely has, and the answer to
// that is a sentence, not a failed page.
func (oracleDialect) Statements(ctx context.Context, db *sql.DB, opts StatementsOptions) (*StatementsReport, error) {
	if opts.Sort == StatementsByMax {
		return nil, ErrBadStatementsOption{msg: "Oracle keeps no longest execution per statement; sort by total, mean, calls or rows"}
	}
	order := map[string]string{
		StatementsByTotal: "total_ms", StatementsByMean: "mean_ms", StatementsByCalls: "calls", StatementsByRows: "rows_",
	}[opts.Sort]
	query := func(scope string) string {
		return `
		  SELECT id, text, calls, total_ms, mean_ms, rows_, hit, grand
		  FROM (
		    SELECT s.sql_id AS id, SUBSTR(s.sql_text, 1, 1000) AS text, s.executions AS calls,
		           s.elapsed_time / 1000 AS total_ms,
		           s.elapsed_time / 1000 / GREATEST(s.executions, 1) AS mean_ms,
		           s.rows_processed AS rows_,
		           CASE WHEN s.buffer_gets > 0 THEN GREATEST(s.buffer_gets - s.disk_reads, 0) / s.buffer_gets ELSE -1 END AS hit,
		           SUM(s.elapsed_time / 1000) OVER () AS grand
		    FROM v$sqlstats s
		    WHERE s.executions > 0` + scope + `
		  )
		  ORDER BY ` + order + ` DESC
		  FETCH FIRST :1 ROWS ONLY`
	}
	// v$sql is what knows who parsed a statement. The schemas left out are the
	// ones only the server itself works as; SYSTEM is not among them, because
	// an administrator — and this dashboard, very often — signs in as it. An
	// account granted the statistics and not the cursors still gets the list,
	// with the server's own statements in it.
	rows, err := db.QueryContext(ctx, query(`
		      AND NOT EXISTS (SELECT 1 FROM v$sql c WHERE c.sql_id = s.sql_id
		                      AND c.parsing_schema_name IN ('SYS', 'DBSNMP', 'AUDSYS', 'XDB', 'MDSYS', 'CTXSYS', 'ORDSYS', 'WMSYS', 'LBACSYS', 'GSMADMIN_INTERNAL'))`), opts.Limit)
	if err != nil {
		rows, err = db.QueryContext(ctx, query(""), opts.Limit)
	}
	if err != nil {
		return &StatementsReport{Statements: []Statement{}, Reason: "The shared pool's statistics could not be read (they need SELECT on v$sqlstats): " + err.Error()}, nil
	}
	defer rows.Close()
	out := &StatementsReport{Statements: []Statement{}, Supported: true}
	for rows.Next() {
		var s Statement
		if err := rows.Scan(&s.ID, nullText{&s.Query}, &s.Calls, &s.TotalMs, &s.MeanMs, &s.Rows, &s.HitRatio, &out.TotalMs); err != nil {
			return nil, err
		}
		s.Query = statementShape(s.Query, false)
		out.Statements = append(out.Statements, s)
	}
	return out, rows.Err()
}

// --- the shape of a statement ------------------------------------------------

// statementShape replaces every literal in a statement's text with a ?, for
// the engines that keep the text as it was sent.
//
// It reads the text the way a SQL lexer would, far enough to know what is a
// literal: a quoted identifier is copied whole, a comment is copied whole (an
// apostrophe in one is not the start of a string), a string becomes one ?
// whatever is inside it, and a number becomes one ? unless it is part of a
// name or of a bind marker. The text an engine hands over may have been cut
// short; a string still open at the end is a literal like any other and is
// dropped with the rest. brackets is true for SQL Server, where [..] quotes a
// name.
func statementShape(text string, brackets bool) string {
	var b strings.Builder
	b.Grow(len(text))
	name := func(c byte) bool {
		return c == '_' || c == '$' || c == '#' || c == '@' || c == ':' || c >= 0x80 ||
			(c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	}
	digit := func(c byte) bool { return c >= '0' && c <= '9' }
	last := func() byte {
		if b.Len() == 0 {
			return 0
		}
		return b.String()[b.Len()-1]
	}
	// copyThrough copies from i up to and including the closing delimiter, a
	// doubled one being the delimiter itself, and returns where it stopped.
	copyThrough := func(i int, closing byte) int {
		j := i + 1
		for j < len(text) {
			if text[j] == closing {
				if j+1 < len(text) && text[j+1] == closing {
					j += 2
					continue
				}
				j++
				break
			}
			j++
		}
		b.WriteString(text[i:j])
		return j
	}
	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == '-' && i+1 < len(text) && text[i+1] == '-':
			end := strings.IndexByte(text[i:], '\n')
			if end < 0 {
				end = len(text) - i
			}
			b.WriteString(text[i : i+end])
			i += end
		case c == '/' && i+1 < len(text) && text[i+1] == '*':
			end := strings.Index(text[i+2:], "*/")
			if end < 0 {
				b.WriteString(text[i:])
				i = len(text)
				break
			}
			b.WriteString(text[i : i+2+end+2])
			i += 2 + end + 2
		case c == '"' || c == '`':
			i = copyThrough(i, c)
		case c == '[' && brackets:
			i = copyThrough(i, ']')
		case c == '\'':
			// The prefix of a national or an alternatively quoted string is part
			// of the literal: N'..', q'[..]', nq'{..}'.
			written := b.String()
			prefix := len(written)
			quoted := prefix > 0 && (written[prefix-1] == 'q' || written[prefix-1] == 'Q')
			if quoted {
				prefix--
			}
			if prefix > 0 && (written[prefix-1] == 'n' || written[prefix-1] == 'N') {
				prefix--
			}
			if prefix > 0 && name(written[prefix-1]) {
				// The letters end a name; they are not a prefix.
				prefix, quoted = len(written), false
			}
			b.Reset()
			b.WriteString(written[:prefix])
			j := i + 1
			if quoted && j < len(text) {
				// q'<delimiter> … <delimiter>' — nothing inside is an escape.
				closing := text[j]
				if at := strings.IndexByte("[{(<", closing); at >= 0 {
					closing = "]})>"[at]
				}
				end := strings.Index(text[j+1:], string([]byte{closing, '\''}))
				if end < 0 {
					j = len(text)
				} else {
					j += 1 + end + 2
				}
			} else {
				for j < len(text) {
					if text[j] == '\'' {
						if j+1 < len(text) && text[j+1] == '\'' {
							j += 2
							continue
						}
						j++
						break
					}
					j++
				}
			}
			b.WriteByte('?')
			i = j
		case digit(c) && !name(last()):
			j := i
			if c == '0' && j+1 < len(text) && (text[j+1] == 'x' || text[j+1] == 'X') {
				j += 2
				for j < len(text) && (digit(text[j]) || (text[j]|0x20 >= 'a' && text[j]|0x20 <= 'f')) {
					j++
				}
			} else {
				for j < len(text) && (digit(text[j]) || text[j] == '.') {
					j++
				}
				if j < len(text) && (text[j] == 'e' || text[j] == 'E') {
					k := j + 1
					if k < len(text) && (text[k] == '+' || text[k] == '-') {
						k++
					}
					if k < len(text) && digit(text[k]) {
						for k < len(text) && digit(text[k]) {
							k++
						}
						j = k
					}
				}
			}
			b.WriteByte('?')
			i = j
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// clipText cuts a string to at most n bytes without splitting a character.
func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
