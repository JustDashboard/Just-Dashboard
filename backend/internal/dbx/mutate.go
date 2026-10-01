package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

// Row editing is the sharp edge of a data browser: it writes to the database on
// the operator's behalf from a form, not from typed SQL. Two rules make that
// safe and keep it out of the query classifier's hands entirely.
//
//  1. Column and table names are validated identifiers, quoted, never bound —
//     the same choke point BrowseTable uses. Values are always bound.
//  2. Every UPDATE and DELETE names one row and is held to it. The key is the
//     table's primary key as the catalogue reports it, not whatever columns the
//     request chose, and the statement runs in a transaction that is rolled
//     back unless it touched exactly one row. ApplyChanges is where both are
//     enforced; the single-row functions below are that with a set of one.

// ErrNoPrimaryKey is returned when an edit is attempted on a table the caller
// could not supply key columns for. The handler turns it into advice to use the
// Query tab, where an explicit WHERE clause is the operator's responsibility.
var ErrNoPrimaryKey = fmt.Errorf("table has no primary key, so a single row cannot be identified for editing")

// buildInsert renders an INSERT for the given (sorted) columns. returning asks
// for the inserted row back, which only some engines can do — the dialect
// decides, so a caller never has to know which.
func buildInsert(d Dialect, schema, table string, cols []string, returning bool) (string, error) {
	rel, err := qualify(d, schema, table)
	if err != nil {
		return "", err
	}
	qCols := make([]string, len(cols))
	marks := make([]string, len(cols))
	for i, c := range cols {
		q, err := d.QuoteIdent(c)
		if err != nil {
			return "", err
		}
		qCols[i] = q
		marks[i] = d.Placeholder(i + 1)
	}
	q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", rel,
		joinComma(qCols), joinComma(marks))
	if returning && d.SupportsReturning() {
		q += " RETURNING *"
	}
	return q, nil
}

// sortedKeys gives the columns a stable order so the generated SQL and the
// argument slice cannot drift apart, and so a builder is deterministic to test.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// InsertRow inserts one row from a column→value map.
func InsertRow(ctx context.Context, db *sql.DB, driver Driver, schema, table string, values map[string]any) (*QueryResult, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("no column values supplied")
	}
	return applyOneChange(ctx, db, driver, schema, table, Change{Op: ChangeInsert, Values: values})
}

// UpdateRow updates the row identified by key with the given values.
func UpdateRow(ctx context.Context, db *sql.DB, driver Driver, schema, table string, values, key map[string]any) (*QueryResult, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("no column values supplied")
	}
	if len(key) == 0 {
		return nil, ErrNoPrimaryKey
	}
	return applyOneChange(ctx, db, driver, schema, table, Change{Op: ChangeUpdate, Key: key, Values: values})
}

// DeleteRow removes the row identified by key.
func DeleteRow(ctx context.Context, db *sql.DB, driver Driver, schema, table string, key map[string]any) (*QueryResult, error) {
	if len(key) == 0 {
		return nil, ErrNoPrimaryKey
	}
	return applyOneChange(ctx, db, driver, schema, table, Change{Op: ChangeDelete, Key: key})
}

// applyOneChange runs a change set of one and answers in the shape the
// single-row routes have always answered in: the statement, what it affected,
// and the row where the engine hands one back.
func applyOneChange(ctx context.Context, db *sql.DB, driver Driver, schema, table string, change Change) (*QueryResult, error) {
	set, err := ApplyChanges(ctx, db, driver, ChangeSet{Schema: schema, Table: table, Changes: []Change{change}})
	if err != nil {
		return nil, err
	}
	out := set.Results[0]
	res := &QueryResult{
		Columns: []string{}, Types: []string{}, Rows: [][]any{},
		Affected: out.Affected, Duration: set.Duration, Statement: out.Statement,
	}
	if out.Row != nil {
		res.Columns = sortedKeys(out.Row)
		row := make([]any, len(res.Columns))
		for i, c := range res.Columns {
			row[i] = out.Row[c]
		}
		res.Rows, res.RowCount = [][]any{row}, 1
	}
	return res, nil
}

func joinComma(parts []string) string { return joinWith(parts, ", ") }

func joinWith(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}
