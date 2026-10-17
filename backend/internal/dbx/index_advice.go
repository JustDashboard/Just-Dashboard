package dbx

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Index advice, asked for.
//
// Version 0.7.5 removed the Advisor and every finding that arrived unasked.
// What is here is the opposite: two readings the operator requests from the
// Explain view, for the statement in front of them. Nothing runs on a timer,
// nothing is stored, and nothing is drawn until the button is pressed.
//
//   - PostgreSQL: candidate indexes for the columns a plan filters, joins and
//     sorts on, and a "what if" that makes one of them hypothetical with
//     HypoPG and plans the statement again. EXPLAIN without ANALYZE runs
//     nothing, and a hypothetical index lives in the session's memory, so
//     neither touches the database.
//   - SQL Server: the missing-index views the engine keeps about its own
//     plans, with the CREATE INDEX each one implies.

// HypoPG states: whether the extension that makes an index hypothetical can be
// used from this database.
const (
	HypoPGReady        = "ready"
	HypoPGNotCreated   = "not_created"
	HypoPGNotInstalled = "not_installed"
)

// HypoPG is the state of the extension, as a fact about this server.
type HypoPG struct {
	State   string `json:"state"`
	Version string `json:"version,omitempty"`
}

// ErrNoWhatIf is returned for an engine the what-if action is not offered on.
var ErrNoWhatIf = errors.New("hypothetical indexes are offered on PostgreSQL only")

// ErrHypoPGUnavailable is returned when the extension cannot be used.
type ErrHypoPGUnavailable struct{ State string }

func (e ErrHypoPGUnavailable) Error() string {
	if e.State == HypoPGNotCreated {
		return "HypoPG (the hypopg extension) is installed on this server but not created in this database"
	}
	return "HypoPG (the hypopg extension) is not installed on this server"
}

// IndexAdvice is the answer to "which indexes might this statement use?".
type IndexAdvice struct {
	Candidates []IndexCandidate `json:"candidates"`
	HypoPG     HypoPG           `json:"hypopg"`
}

// maxAdviceTables bounds the catalogue reads one request can cause.
const maxAdviceTables = 12

// maxCandidates is how many indexes are proposed for one statement.
const maxCandidates = 8

func hypopgState(ctx context.Context, q queryer) (HypoPG, error) {
	rows, err := q.QueryContext(ctx, `SELECT
	  (SELECT extversion FROM pg_extension WHERE extname = 'hypopg'),
	  EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'hypopg')`)
	if err != nil {
		return HypoPG{}, err
	}
	defer rows.Close()
	var version sql.NullString
	var available bool
	if rows.Next() {
		if err := rows.Scan(&version, &available); err != nil {
			return HypoPG{}, err
		}
	}
	if err := rows.Err(); err != nil {
		return HypoPG{}, err
	}
	switch {
	case version.Valid:
		return HypoPG{State: HypoPGReady, Version: version.String}, nil
	case available:
		return HypoPG{State: HypoPGNotCreated}, nil
	}
	return HypoPG{State: HypoPGNotInstalled}, nil
}

// planDocument takes a statement's plan as JSON.
func planDocument(ctx context.Context, q queryer, p planner, st *SQLStatement, verbose bool) (any, error) {
	text, err := p.explainSQL("", st.SQL, ExplainOptions{Format: ExplainJSON, Verbose: verbose})
	if err != nil {
		return nil, err
	}
	res, err := runOn(ctx, q, DriverPostgres, text, true, collectOptions{maxRows: MaxResultRows})
	if err != nil {
		return nil, err
	}
	return parsePlan(res)
}

// IndexCandidates proposes indexes for the statement st, which ExplainTarget
// accepted, and reports whether HypoPG could try them. Indexes that already
// lead with the same columns are not proposed again.
func IndexCandidates(ctx context.Context, db *sql.DB, driver Driver, st *SQLStatement) (*IndexAdvice, error) {
	if driver != DriverPostgres {
		return nil, ErrNoWhatIf
	}
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	p, ok := d.(planner)
	if !ok {
		return nil, ErrExplainUnsupported
	}
	plan, err := planDocument(ctx, db, p, st, true)
	if err != nil {
		return nil, err
	}
	state, err := hypopgState(ctx, db)
	if err != nil {
		return nil, err
	}
	out := &IndexAdvice{Candidates: []IndexCandidate{}, HypoPG: state}

	details := map[relationRef]*TableDetail{}
	read := 0
	for _, candidate := range candidatesFromUses(planColumnUses(plan)) {
		relation := relationRef{candidate.Schema, candidate.Table}
		detail, seen := details[relation]
		if !seen {
			if read == maxAdviceTables {
				continue
			}
			read++
			// A relation whose catalogue cannot be read proposes nothing: an
			// index is not suggested for a table that was not looked at.
			if detail, err = Detail(ctx, db, driver, candidate.Schema, candidate.Table); err != nil {
				detail = nil
			}
			details[relation] = detail
		}
		if detail == nil || !indexable(detail, candidate) || covered(detail, candidate) {
			continue
		}
		plan, err := PlanCreateIndex(ctx, db, driver, IndexSpec{
			Schema: candidate.Schema, Table: candidate.Table, Columns: candidate.Columns, Concurrently: true,
		})
		if err != nil {
			return nil, err
		}
		candidate.Statement = plan.String()
		out.Candidates = append(out.Candidates, candidate)
		if len(out.Candidates) == maxCandidates {
			break
		}
	}
	return out, nil
}

// indexable is whether the candidate names columns of a table an index can be
// made on. A partition is left to its parent, and a name the plan printed that
// is not a column of the table (a parse of something else) is dropped.
func indexable(detail *TableDetail, candidate IndexCandidate) bool {
	if detail.Type == "partition" || detail.Type == "view" {
		return false
	}
	held := map[string]bool{}
	for _, column := range detail.Columns {
		held[column.Name] = true
	}
	for _, column := range candidate.Columns {
		if !held[column] {
			return false
		}
	}
	return true
}

func covered(detail *TableDetail, candidate IndexCandidate) bool {
	for _, index := range detail.Indexes {
		if indexServes(index, candidate) {
			return true
		}
	}
	return false
}

// WhatIfIndex is the index to try.
type WhatIfIndex struct {
	Schema  string
	Table   string
	Columns []string
}

// WhatIf is a statement planned twice, without and with a hypothetical index.
type WhatIf struct {
	// Statement is the CREATE INDEX HypoPG was given.
	Statement string `json:"statement"`
	// IndexName is what HypoPG calls the index, which is how the plan names it.
	IndexName  string  `json:"indexName"`
	Before     any     `json:"before"`
	After      any     `json:"after"`
	BeforeCost float64 `json:"beforeCost"`
	AfterCost  float64 `json:"afterCost"`
	// Used reports that the planner chose the index.
	Used bool `json:"used"`
}

// TryIndex plans st without and with a hypothetical index over index's
// columns. Both plans are taken on one connection, inside the engine's
// read-only scope, so they are comparable and the only thing HypoPG touches is
// that connection's memory — which is then thrown away rather than pooled.
func TryIndex(ctx context.Context, db *sql.DB, driver Driver, st *SQLStatement, index WhatIfIndex) (*WhatIf, error) {
	if driver != DriverPostgres {
		return nil, ErrNoWhatIf
	}
	if len(index.Columns) == 0 {
		return nil, fmt.Errorf("an index needs at least one column")
	}
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	p, ok := d.(planner)
	if !ok {
		return nil, ErrExplainUnsupported
	}
	create, err := PlanCreateIndex(ctx, db, driver, IndexSpec{
		Schema: index.Schema, Table: index.Table, Columns: index.Columns,
	})
	if err != nil {
		return nil, err
	}
	s, err := openSession(ctx, db, driver)
	if err != nil {
		return nil, err
	}
	s.dirty = true
	defer s.close()

	out := &WhatIf{Statement: create.String()}
	err = s.read(ctx, func(ctx context.Context, q queryer) error {
		state, err := hypopgState(ctx, q)
		if err != nil {
			return err
		}
		if state.State != HypoPGReady {
			return ErrHypoPGUnavailable{State: state.State}
		}
		if out.Before, err = planDocument(ctx, q, p, st, false); err != nil {
			return err
		}
		rows, err := q.QueryContext(ctx, `SELECT indexname FROM hypopg_create_index($1)`, out.Statement)
		if err != nil {
			return err
		}
		for rows.Next() {
			if err := rows.Scan(&out.IndexName); err != nil {
				_ = rows.Close()
				return err
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if out.IndexName == "" {
			return fmt.Errorf("HypoPG made no index from %s", out.Statement)
		}
		out.After, err = planDocument(ctx, q, p, st, false)
		return err
	})
	if err != nil {
		return nil, err
	}
	out.BeforeCost = planTotalCost(out.Before)
	out.AfterCost = planTotalCost(out.After)
	out.Used = planUsesIndex(out.After, out.IndexName)
	return out, nil
}

// planTotalCost is the planner's cost for the whole statement.
func planTotalCost(plan any) float64 {
	roots, _ := plan.([]any)
	if len(roots) == 0 {
		return 0
	}
	root, _ := roots[0].(map[string]any)
	top, _ := root["Plan"].(map[string]any)
	switch cost := top["Total Cost"].(type) {
	case json.Number:
		f, _ := cost.Float64()
		return f
	case float64:
		return cost
	}
	return 0
}

// planUsesIndex reports whether any step of a plan reads the named index.
func planUsesIndex(plan any, name string) bool {
	switch v := plan.(type) {
	case []any:
		for _, item := range v {
			if planUsesIndex(item, name) {
				return true
			}
		}
	case map[string]any:
		if v["Index Name"] == name {
			return true
		}
		for _, item := range v {
			if planUsesIndex(item, name) {
				return true
			}
		}
	}
	return false
}
