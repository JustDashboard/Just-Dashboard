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
	// Schema is where the extension's functions live, so they are called by
	// that name and not by whatever the session's search_path finds first.
	Schema string `json:"-"`
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
//
// Covered and Skipped say what became of the candidates that are not in the
// list, so an empty list is explained by the owner and not guessed at: Covered
// were left out because an index already leads with their columns, Skipped
// because their table could not be proposed for — a partition, a view, a
// foreign table, one whose catalogue could not be read, or one past the limit.
// A condition the reader does not recognise proposes nothing and is in neither.
type IndexAdvice struct {
	Candidates []IndexCandidate `json:"candidates"`
	Covered    int              `json:"covered"`
	Skipped    int              `json:"skipped"`
	HypoPG     HypoPG           `json:"hypopg"`
}

// maxAdviceTables bounds the catalogue reads one request can cause.
const maxAdviceTables = 12

// maxCandidates is how many indexes are proposed for one statement.
const maxCandidates = 8

func hypopgState(ctx context.Context, q queryer) (HypoPG, error) {
	rows, err := q.QueryContext(ctx, `SELECT
	  (SELECT extversion FROM pg_extension WHERE extname = 'hypopg'),
	  (SELECT n.nspname FROM pg_extension e JOIN pg_namespace n ON n.oid = e.extnamespace WHERE e.extname = 'hypopg'),
	  EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'hypopg')`)
	if err != nil {
		return HypoPG{}, err
	}
	defer rows.Close()
	var version, schema sql.NullString
	var available bool
	if rows.Next() {
		if err := rows.Scan(&version, &schema, &available); err != nil {
			return HypoPG{}, err
		}
	}
	if err := rows.Err(); err != nil {
		return HypoPG{}, err
	}
	switch {
	case version.Valid:
		return HypoPG{State: HypoPGReady, Version: version.String, Schema: schema.String}, nil
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
	// The plan is taken inside the engine's read-only scope, as TryIndex does:
	// the route is open on a protected connection, and planning can evaluate
	// stable and immutable functions.
	sess, err := openSession(ctx, db, driver)
	if err != nil {
		return nil, err
	}
	var plan any
	var state HypoPG
	err = sess.read(ctx, func(ctx context.Context, q queryer) error {
		var err error
		if plan, err = planDocument(ctx, q, p, st, true); err != nil {
			return err
		}
		state, err = hypopgState(ctx, q)
		return err
	})
	// Given back before the catalogue reads below ask the pool for theirs.
	sess.close()
	if err != nil {
		return nil, err
	}
	out := &IndexAdvice{Candidates: []IndexCandidate{}, HypoPG: state}

	details := map[relationRef]*TableDetail{}
	read := 0
	for _, candidate := range candidatesFromUses(planColumnUses(plan)) {
		if len(out.Candidates) == maxCandidates {
			break
		}
		relation := relationRef{candidate.Schema, candidate.Table}
		detail, seen := details[relation]
		if !seen {
			if read < maxAdviceTables {
				read++
				// A relation whose catalogue cannot be read proposes nothing: an
				// index is not suggested for a table that was not looked at.
				if detail, err = Detail(ctx, db, driver, candidate.Schema, candidate.Table); err != nil {
					detail = nil
				}
			}
			details[relation] = detail
		}
		if detail == nil || !indexable(detail, candidate) {
			out.Skipped++
			continue
		}
		if covered(detail, candidate) {
			out.Covered++
			continue
		}
		out.Candidates = append(out.Candidates, candidate)
	}
	return out, nil
}

// indexable is whether the candidate names columns of a table an index can be
// made on. Only a table or a materialized view is: a partition's index belongs
// to the parent, which a plan of a SELECT never names, and a view or a foreign
// table takes none from HypoPG or from CREATE INDEX. A name the plan printed
// that is not a column of the table (a parse of something else) is dropped.
func indexable(detail *TableDetail, candidate IndexCandidate) bool {
	switch detail.Type {
	case "", TableTypeTable, TableTypeMaterializedView:
	default:
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
		extension, err := d.QuoteIdent(state.Schema)
		if err != nil {
			return err
		}
		rows, err := q.QueryContext(ctx, `SELECT indexname FROM `+extension+`.hypopg_create_index($1)`, out.Statement)
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
