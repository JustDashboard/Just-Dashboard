package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Statement statistics: which queries cost the server the most, summed over
// every time they ran.
//
// The activity list says what is running now; this says what has been
// running all week. They answer different questions — a query that takes ten
// milliseconds and runs ten thousand times an hour never appears in the
// activity list and is the top row here.
//
// Three engines keep this. Postgres needs pg_stat_statements loaded and the
// extension created; MySQL and MariaDB keep it in performance_schema;
// ClickHouse keeps every finished query in its query log, which is summed
// here by shape. The rest report Supported false and say why.

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
	var since sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT stats_reset FROM pg_stat_statements_info`).Scan(&since); err == nil {
		out.Since = timePtr(since)
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
// own setting.
func (clickhouseDialect) Statements(ctx context.Context, db *sql.DB, opts StatementsOptions) (*StatementsReport, error) {
	order := map[string]string{
		StatementsByTotal: "total", StatementsByMean: "mean", StatementsByCalls: "calls",
		StatementsByRows: "result_rows", StatementsByMax: "longest",
	}[opts.Sort]
	const scope = `type = 'QueryFinish' AND event_time > now() - INTERVAL ` + clickhouseStatementsWindow + `
	      AND current_database = currentDatabase() AND query NOT LIKE '%system.query_log%'`
	rows, err := db.QueryContext(ctx, `
	  SELECT toString(normalized_query_hash), substring(any(query), 1, 1000), toInt64(count()) AS calls,
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
