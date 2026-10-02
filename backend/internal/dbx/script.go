package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// A script is several statements run in order on one connection.
//
// One connection is the point. Run through the pool, each statement of a
// script could land on a different session, so a SET, a temporary table or a
// BEGIN written on one line would not exist on the next. The connection is
// held for the whole script and then closed rather than returned: whatever the
// script left on it — an open transaction, a search_path, a role — must not be
// what the next request finds.

// ScriptOptions is how a script is run.
type ScriptOptions struct {
	// Transaction wraps the whole script in one transaction: committed when
	// every statement succeeded, rolled back at the first error.
	Transaction bool
	// MaxRows caps each statement's result.
	MaxRows int
}

// ScriptRowBudget is the most rows all the results of one script carry
// between them. Past it each further result holds a single row and is flagged
// truncated: the statements still run, and the answer says what was cut.
const ScriptRowBudget = 4 * MaxResultRows

// ScriptStep is one statement of a script and what became of it.
type ScriptStep struct {
	Index int    `json:"index"`
	SQL   string `json:"sql"`
	Line  int    `json:"line"`
	Risk  Risk   `json:"risk"`
	// Status is "ok", "error", or "skipped" for a statement after the one that
	// failed.
	Status     string       `json:"status"`
	Result     *QueryResult `json:"result,omitempty"`
	Error      string       `json:"error,omitempty"`
	DurationMs int64        `json:"durationMs"`
}

const (
	StepOK      = "ok"
	StepError   = "error"
	StepSkipped = "skipped"

	// ScriptReadOnly, ScriptCommitted and ScriptRolledBack say what happened to
	// the script as a whole; ScriptNone means each statement stood on its own.
	ScriptNone       = "none"
	ScriptReadOnly   = "read_only"
	ScriptCommitted  = "committed"
	ScriptRolledBack = "rolled_back"
)

// ScriptResult is the answer to a script.
type ScriptResult struct {
	Steps []ScriptStep `json:"statements"`
	// Failed is the index of the statement that stopped the script, or -1.
	Failed      int    `json:"failed"`
	Transaction string `json:"transaction"`
	DurationMs  int64  `json:"durationMs"`
}

// RunScript runs statements in order on one connection and stops at the first
// error. Every statement is answered for: run, failed, or skipped.
//
// A statement that fails is not an error from this function — the script ran,
// and its result says how far. The error return is for the script that could
// not be run at all.
func RunScript(ctx context.Context, db *sql.DB, driver Driver, statements []SQLStatement, opts ScriptOptions) (*ScriptResult, error) {
	if len(statements) == 0 {
		return nil, fmt.Errorf("the script holds no statement")
	}
	worst := WorstRisk(statements)
	if opts.Transaction && worst.Level != "read" {
		if driver == DriverClickHouse {
			return nil, fmt.Errorf("ClickHouse has no transactions, so a script cannot be run inside one")
		}
		for _, st := range statements {
			if transactionWords[st.leader] {
				return nil, fmt.Errorf("line %d manages a transaction itself; turn off running the script in one transaction", st.Line)
			}
		}
	}
	s, err := openSession(ctx, db, driver)
	if err != nil {
		return nil, err
	}
	defer s.close()

	start := time.Now()
	out := &ScriptResult{Steps: make([]ScriptStep, len(statements)), Failed: -1, Transaction: ScriptNone}
	for i, st := range statements {
		out.Steps[i] = ScriptStep{Index: i, SQL: st.SQL, Line: st.Line, Risk: st.Risk, Status: StepSkipped}
	}
	// Every result of a script is held until the last one is in, so the rows
	// are budgeted across the whole of it and not only per statement.
	budget := ScriptRowBudget
	// each runs the statements on q until one fails.
	each := func(ctx context.Context, q queryer, scoped bool) {
		for i := range statements {
			st := &statements[i]
			began := time.Now()
			limit := min(clampRows(opts.MaxRows, defaultMaxRows, MaxResultRows), max(budget, 1))
			var res *QueryResult
			var err error
			if scoped && st.Risk.Level == "read" {
				// A read inside a script that also writes still runs where the
				// engine will not let it write, one statement at a time.
				err = s.read(ctx, func(ctx context.Context, q queryer) error {
					var err error
					res, err = s.run(ctx, q, st, limit)
					return err
				})
			} else {
				res, err = s.run(ctx, q, st, limit)
			}
			if res != nil {
				budget -= res.RowCount
			}
			out.Steps[i].DurationMs = time.Since(began).Milliseconds()
			if err != nil {
				out.Steps[i].Status, out.Steps[i].Error = StepError, err.Error()
				out.Failed = i
				return
			}
			out.Steps[i].Status, out.Steps[i].Result = StepOK, res
		}
	}

	switch {
	case worst.Level == "read":
		// Nothing in the script claims to write, so the whole of it runs in one
		// read-only scope and the connection goes back to the pool clean.
		out.Transaction = ScriptReadOnly
		err = s.read(ctx, func(ctx context.Context, q queryer) error {
			each(ctx, q, false)
			return nil
		})
	case opts.Transaction:
		s.dirty = true
		var tx *sql.Tx
		if tx, err = s.conn.BeginTx(ctx, nil); err != nil {
			break
		}
		each(ctx, tx, false)
		if out.Failed >= 0 {
			_ = tx.Rollback()
			out.Transaction = ScriptRolledBack
			break
		}
		if err = tx.Commit(); err != nil {
			// The statements ran and the engine refused to keep them. That is
			// the last statement's failure as far as the operator is concerned.
			last := len(out.Steps) - 1
			out.Steps[last].Status, out.Steps[last].Error = StepError, "commit failed: "+err.Error()
			out.Failed, out.Transaction, err = last, ScriptRolledBack, nil
			break
		}
		out.Transaction = ScriptCommitted
	default:
		s.dirty = true
		// Wrapping a read in a transaction of its own is only possible when
		// the script has none of its own, and a script that opens one is
		// classified destructive for it. So below that level each read is
		// scoped, and at it the statements run exactly as written.
		each(ctx, s.conn, !worst.Destructive)
	}
	if err != nil {
		return nil, err
	}
	out.DurationMs = time.Since(start).Milliseconds()
	return out, nil
}
