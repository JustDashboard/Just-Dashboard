package dbx

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ExplainOptions is what the operator may ask of a plan. The options are a
// closed set here and never text from the request: `(ANALYZE)` typed into the
// statement is refused by the plan gate, and the only way to an executing plan
// is the Analyze flag, which the handler authorises as an execution.
type ExplainOptions struct {
	// Analyze runs the statement and reports what actually happened.
	Analyze bool
	// Format is "text" or "json". Empty means text.
	Format string
}

const (
	ExplainText = "text"
	ExplainJSON = "json"
)

// Explanation is a plan in the form it was asked for.
type Explanation struct {
	Result *QueryResult `json:"result"`
	// Plan is the parsed JSON plan, present when the format is json, so the
	// client draws a tree from a document rather than parsing one out of a
	// result cell.
	Plan     any    `json:"plan,omitempty"`
	Format   string `json:"format"`
	Analyzed bool   `json:"analyzed"`
	// RolledBack reports that the statement was executed inside a transaction
	// that was then rolled back, so what it changed is gone again.
	RolledBack bool `json:"rolledBack"`
}

// ErrExplainUnsupported is returned for a plan form the engine does not have.
var ErrExplainUnsupported = errors.New("this engine does not offer that form of plan")

// ExplainTarget returns the one statement a plan may be asked for, classified,
// so the caller can decide whether running it — which is what Analyze does — is
// allowed before anything reaches the database.
func ExplainTarget(driver Driver, query string) (*SQLStatement, error) {
	return explainStatement(driver, query)
}

// ExplainForms reports which plans beyond the plain text one an engine has.
func ExplainForms(driver Driver) (jsonPlan, analyze bool) {
	d, err := DialectFor(driver)
	if err != nil {
		return false, false
	}
	p, ok := d.(planner)
	if !ok {
		return false, false
	}
	_, jsonErr := p.explainSQL("", "SELECT 1", ExplainOptions{Format: ExplainJSON})
	_, analyzeErr := p.explainSQL("", "SELECT 1", ExplainOptions{Analyze: true})
	return jsonErr == nil, analyzeErr == nil
}

// planPreparer is implemented by a dialect whose server has to be told
// something on the session before it will produce an executing plan.
type planPreparer interface {
	// preparePlan readies conn for the plan opts asks for, and reports
	// whether it left the session changed.
	preparePlan(ctx context.Context, conn *sql.Conn, version string, opts ExplainOptions) bool
}

// preparePlanSession lets the dialect ready the session a plan is about to run
// on. A session it changed is not handed to the next request, whose plans
// would come back in the shape this one asked for.
func preparePlanSession(ctx context.Context, s *session, version string, opts ExplainOptions) {
	if p, ok := s.dialect.(planPreparer); ok && p.preparePlan(ctx, s.conn, version, opts) {
		s.dirty = true
	}
}

// Explain returns the plan for a statement ExplainTarget accepted.
//
// Without Analyze nothing is executed, on any engine. With it the statement
// runs: a read inside the engine's read-only scope, anything else inside a
// transaction that is rolled back, so the plan of a DELETE can be measured
// without the rows staying deleted.
func Explain(ctx context.Context, db *sql.DB, driver Driver, st *SQLStatement, opts ExplainOptions) (*Explanation, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	switch opts.Format {
	case "":
		opts.Format = ExplainText
	case ExplainText, ExplainJSON:
	default:
		return nil, fmt.Errorf("format must be %q or %q", ExplainText, ExplainJSON)
	}
	out := &Explanation{Format: opts.Format, Analyzed: opts.Analyze}
	if !opts.Analyze && opts.Format == ExplainText {
		out.Result, err = d.ExplainPlan(ctx, db, st.SQL)
		return out, err
	}
	p, ok := d.(planner)
	if !ok {
		return nil, ErrExplainUnsupported
	}
	version := ""
	if driver == DriverMySQL {
		// MySQL and MariaDB spell an executing plan differently, and only the
		// server knows which of the two it is.
		_ = db.QueryRowContext(ctx, d.VersionQuery()).Scan(&version)
	}
	text, err := p.explainSQL(version, st.SQL, opts)
	if err != nil {
		return nil, err
	}
	plan := &SQLStatement{SQL: text, returnsRows: true}

	switch {
	case !opts.Analyze:
		out.Result, err = runOn(ctx, db, driver, text, true, collectOptions{maxRows: MaxResultRows})
	case st.Risk.Level == "read":
		var s *session
		if s, err = openSession(ctx, db, driver); err != nil {
			return nil, err
		}
		defer s.close()
		preparePlanSession(ctx, s, version, opts)
		err = s.read(ctx, func(ctx context.Context, q queryer) error {
			var err error
			out.Result, err = s.run(ctx, q, plan, MaxResultRows)
			return err
		})
	default:
		var s *session
		if s, err = openSession(ctx, db, driver); err != nil {
			return nil, err
		}
		s.dirty = true
		defer s.close()
		preparePlanSession(ctx, s, version, opts)
		var tx *sql.Tx
		if tx, err = s.conn.BeginTx(ctx, nil); err != nil {
			return nil, err
		}
		out.Result, err = s.run(ctx, tx, plan, MaxResultRows)
		if rollbackErr := tx.Rollback(); rollbackErr != nil && err == nil {
			// The plan ran and its changes could not be undone. That is not a
			// plan to hand back as if nothing happened.
			err = fmt.Errorf("the statement ran and could not be rolled back: %w", rollbackErr)
		}
		out.RolledBack = err == nil
	}
	if err != nil {
		return nil, err
	}
	if opts.Format == ExplainJSON {
		out.Plan, err = parsePlan(out.Result)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// parsePlan reads the JSON document a plan command returned. Engines hand it
// back as one cell, or as one cell per line of it.
func parsePlan(res *QueryResult) (any, error) {
	var text strings.Builder
	for _, row := range res.Rows {
		if len(row) == 0 {
			continue
		}
		switch cell := row[0].(type) {
		case string:
			text.WriteString(cell)
		default:
			b, err := json.Marshal(cell)
			if err != nil {
				return nil, err
			}
			text.Write(b)
		}
		text.WriteByte('\n')
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(text.String())))
	// Costs and row counts stay the digits the engine printed.
	dec.UseNumber()
	var plan any
	if err := dec.Decode(&plan); err != nil {
		return nil, fmt.Errorf("the engine's plan was not valid JSON: %v", err)
	}
	return plan, nil
}
