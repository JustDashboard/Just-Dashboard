package dbx

import (
	"context"
	"database/sql"
	"fmt"
)

// Statement statistics: which queries cost the server the most, summed over
// every time they ran.
//
// The activity list says what is running now; this says what has been
// running all week. They answer different questions — a query that takes ten
// milliseconds and runs ten thousand times an hour never appears in the
// activity list and is the top row here.
//
// Two engines keep this. Postgres needs pg_stat_statements loaded and the
// extension created; MySQL and MariaDB keep it in performance_schema by
// default. The rest report Supported false and say why.

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
}

type StatementsReport struct {
	Statements []Statement `json:"statements"`
	Supported  bool        `json:"supported"`
	Reason     string      `json:"reason,omitempty"`
	// TotalMs is the sum over every statement the engine tracks, so a row's
	// share of the whole can be drawn.
	TotalMs float64 `json:"totalMs"`
}

// StatementStatser is the optional dialect half.
type StatementStatser interface {
	Statements(ctx context.Context, db *sql.DB, limit int) (*StatementsReport, error)
}

func TopStatements(ctx context.Context, db *sql.DB, driver Driver, limit int) (*StatementsReport, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 25
	}
	s, ok := d.(StatementStatser)
	if !ok {
		return &StatementsReport{Statements: []Statement{}, Reason: "this engine keeps no per-statement statistics"}, nil
	}
	return s.Statements(ctx, db, limit)
}

func (postgresDialect) Statements(ctx context.Context, db *sql.DB, limit int) (*StatementsReport, error) {
	var installed bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_stat_statements')`).Scan(&installed); err != nil {
		return nil, err
	}
	if !installed {
		return &StatementsReport{Statements: []Statement{}, Reason: "pg_stat_statements is not installed on this server"}, nil
	}
	// Column names changed in 13 (total_time became total_exec_time). The
	// view is asked which it has rather than the version being parsed.
	timeCol, meanCol, maxCol := "total_exec_time", "mean_exec_time", "max_exec_time"
	var modern bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
	  WHERE table_name = 'pg_stat_statements' AND column_name = 'total_exec_time')`).Scan(&modern); err == nil && !modern {
		timeCol, meanCol, maxCol = "total_time", "mean_time", "max_time"
	}
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
	  SELECT queryid::text, LEFT(query, 1000), calls, %s, %s, %s, rows,
	         CASE WHEN shared_blks_hit + shared_blks_read > 0
	              THEN shared_blks_hit::float8 / (shared_blks_hit + shared_blks_read) ELSE -1 END,
	         (SELECT COALESCE(sum(%s), 0) FROM pg_stat_statements)
	  FROM pg_stat_statements
	  WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
	  ORDER BY %s DESC LIMIT $1`, timeCol, meanCol, maxCol, timeCol, timeCol), limit)
	if err != nil {
		// The extension exists but the view is not readable by this role, or
		// the library is not preloaded: say so rather than fail the page.
		return &StatementsReport{Statements: []Statement{}, Reason: err.Error()}, nil
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
	return out, rows.Err()
}

func (mysqlDialect) Statements(ctx context.Context, db *sql.DB, limit int) (*StatementsReport, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT COALESCE(DIGEST, ''), COALESCE(LEFT(DIGEST_TEXT, 1000), ''), COUNT_STAR,
	         SUM_TIMER_WAIT / 1e9, AVG_TIMER_WAIT / 1e9, MAX_TIMER_WAIT / 1e9, SUM_ROWS_SENT,
	         (SELECT COALESCE(SUM(SUM_TIMER_WAIT), 0) / 1e9 FROM performance_schema.events_statements_summary_by_digest)
	  FROM performance_schema.events_statements_summary_by_digest
	  WHERE SCHEMA_NAME IS NULL OR SCHEMA_NAME = DATABASE() OR DATABASE() IS NULL
	  ORDER BY SUM_TIMER_WAIT DESC LIMIT ?`, limit)
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
	return out, rows.Err()
}
