package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Filter is one condition the grid's header controls produce.
//
// The operator is an enum, never text from the request: a filter is the one
// place in the browse path where the caller influences the *shape* of the WHERE
// clause rather than only its values, so the set of shapes is closed and every
// value inside it is still bound. `Column` is validated and quoted like any
// other identifier.
type Filter struct {
	Column string `json:"column"`
	Op     string `json:"op"`
	Value  string `json:"value"`
	// Values carries the operators that take more than one: the members of an
	// in-list, the two ends of a range.
	Values []string `json:"values,omitempty"`
}

// SortKey is one column of the order the grid asked for.
type SortKey struct {
	Column string `json:"column"`
	Desc   bool   `json:"desc"`
}

// BrowseOptions is everything the data grid can ask for.
type BrowseOptions struct {
	Schema string
	Table  string
	Limit  int
	Offset int
	// OrderBy and Desc are the single-column form, kept because the export and
	// every older caller speak it. Sort, when set, replaces them.
	OrderBy string
	Desc    bool
	Sort    []SortKey
	Filters []Filter
	// MatchAny joins the filters with OR instead of AND.
	MatchAny bool
	// Columns narrows the result to these columns, in this order. Empty means
	// every column.
	Columns []string
	// StableKey is appended to the order as a tie-break — the primary key, so
	// that two pages of the same sort cannot show the same row twice or skip
	// one. Paging with ties in the order is undefined on every engine.
	StableKey []string
	// ClipText cuts text cells to this many bytes. 0 leaves text whole.
	ClipText int
}

const (
	// MaxBrowseRows is the largest page the grid can ask for.
	MaxBrowseRows     = 1000
	defaultBrowseRows = 100
	// MaxFilterValues bounds an in-list. Past this the operator wants a query.
	MaxFilterValues = 200
	// MaxBrowseColumns bounds a projection and a sort.
	MaxBrowseColumns = 500
)

// comparisonOps are the operators that are one SQL comparison against one
// bound value.
var comparisonOps = map[string]string{
	"eq": "=", "ne": "<>", "lt": "<", "lte": "<=", "gt": ">", "gte": ">=",
}

// substringOps are the operators that look inside the text form of a column.
var substringOps = map[string]struct {
	kind   matchKind
	fold   bool
	negate bool
}{
	"contains":     {kind: matchContains},
	"not_contains": {kind: matchContains, negate: true},
	"icontains":    {kind: matchContains, fold: true},
	"prefix":       {kind: matchPrefix},
	"suffix":       {kind: matchSuffix},
}

// FilterOps is the operator list every SQL engine here supports, so the
// frontend does not keep a second copy that can drift from what the server
// accepts.
func FilterOps() []string {
	return []string{
		"eq", "ne", "lt", "lte", "gt", "gte", "in", "not_in", "between",
		"contains", "not_contains", "icontains", "prefix", "suffix",
		"is_null", "not_null",
	}
}

// FilterOpsFor is FilterOps for one engine: the shared list, plus the regular
// expression match where the engine has one.
func FilterOpsFor(driver Driver) []string {
	ops := FilterOps()
	if d, err := DialectFor(driver); err == nil {
		if _, ok := d.(regexMatcher); ok {
			ops = append(ops, "regex")
		}
	}
	return ops
}

// filterCondition renders one filter and appends what it binds. n is the next
// placeholder number.
func filterCondition(d Dialect, f Filter, n int) (string, []any, error) {
	col, err := d.QuoteIdent(f.Column)
	if err != nil {
		return "", nil, err
	}
	if sqlOp, ok := comparisonOps[f.Op]; ok {
		return col + " " + sqlOp + " " + d.Placeholder(n), []any{f.Value}, nil
	}
	if spec, ok := substringOps[f.Op]; ok {
		frag, pattern := textMatchFor(d, col, spec.kind, spec.fold, d.Placeholder(n))
		if spec.negate {
			frag = "NOT (" + frag + ")"
		}
		return frag, []any{pattern(f.Value)}, nil
	}
	switch f.Op {
	case "is_null":
		return col + " IS NULL", nil, nil
	case "not_null":
		return col + " IS NOT NULL", nil, nil
	case "in", "not_in":
		values := f.Values
		if len(values) == 0 {
			// A single value in the old field is a list of one, so the simplest
			// form of the request still means what it says.
			values = []string{f.Value}
		}
		if len(values) > MaxFilterValues {
			return "", nil, fmt.Errorf("a filter list may hold at most %d values", MaxFilterValues)
		}
		marks := make([]string, len(values))
		args := make([]any, len(values))
		for i, v := range values {
			marks[i] = d.Placeholder(n + i)
			args[i] = v
		}
		word := " IN ("
		if f.Op == "not_in" {
			word = " NOT IN ("
		}
		return col + word + strings.Join(marks, ", ") + ")", args, nil
	case "between":
		if len(f.Values) != 2 {
			return "", nil, fmt.Errorf("the between filter takes exactly two values")
		}
		return col + " BETWEEN " + d.Placeholder(n) + " AND " + d.Placeholder(n+1),
			[]any{f.Values[0], f.Values[1]}, nil
	case "regex":
		m, ok := d.(regexMatcher)
		if !ok {
			return "", nil, fmt.Errorf("this engine has no regular-expression match")
		}
		return m.regexMatch(col, d.Placeholder(n)), []any{f.Value}, nil
	}
	return "", nil, fmt.Errorf("unsupported filter operator %q", f.Op)
}

// buildWhere renders the filter list into a WHERE clause and its bound
// arguments. argStart is the first placeholder number to use, so the caller can
// place the filters before or after its own paging parameters.
func buildWhere(d Dialect, filters []Filter, argStart int) (string, []any, error) {
	return buildWhereMatch(d, filters, false, argStart)
}

func buildWhereMatch(d Dialect, filters []Filter, matchAny bool, argStart int) (string, []any, error) {
	if len(filters) == 0 {
		return "", nil, nil
	}
	parts := make([]string, 0, len(filters))
	args := []any{}
	for _, f := range filters {
		frag, bound, err := filterCondition(d, f, argStart+len(args))
		if err != nil {
			return "", nil, err
		}
		if matchAny && len(filters) > 1 {
			// Each condition is bracketed once they are alternatives: BETWEEN
			// carries an AND of its own.
			frag = "(" + frag + ")"
		}
		parts = append(parts, frag)
		args = append(args, bound...)
	}
	joiner := " AND "
	if matchAny {
		joiner = " OR "
	}
	return " WHERE " + strings.Join(parts, joiner), args, nil
}

// Browse pages through a table, optionally sorted and filtered.
//
// One row more than the page is asked for and then dropped, which is how
// Truncated comes to mean "there is another page" without a count.
func Browse(ctx context.Context, db *sql.DB, driver Driver, opts BrowseOptions) (*QueryResult, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	opts.Limit = clampRows(opts.Limit, defaultBrowseRows, MaxBrowseRows)
	if opts.Offset < 0 {
		opts.Offset = 0
	}
	sel, err := browseSelect(d, opts)
	if err != nil {
		return nil, err
	}

	tail, tailArgs := d.Paginate(opts.Limit+1, opts.Offset, len(sel.args)+1)
	// SQL Server's Paginate supplies its own mandatory ORDER BY; when the
	// operator has chosen one, theirs replaces it rather than sitting next to it.
	if sel.ordered {
		tail = strings.TrimPrefix(tail, "ORDER BY (SELECT NULL) ")
	}
	args := append(sel.args, tailArgs...)

	return runOn(ctx, db, driver, sel.query+" "+tail, true, collectOptions{
		maxRows: opts.Limit, clipText: opts.ClipText,
	}, args...)
}

// BrowsePage is a page of a table with what the grid needs beside it.
type BrowsePage struct {
	*QueryResult
	// PrimaryKey is what identifies a row of this table, in key order. Empty
	// when the table has none — the grid then keys an edit on the whole row.
	PrimaryKey []string `json:"primaryKey"`
	// EstimatedRows is the engine's own figure for the whole table, ignoring
	// the filters, from its statistics. Nil when the engine has none.
	EstimatedRows *int64 `json:"estimatedRows"`
	// Sort is the order that was applied, including the tie-break.
	Sort   []SortKey `json:"sort"`
	Limit  int       `json:"limit"`
	Offset int       `json:"offset"`
}

// BrowseTablePage is Browse for the grid: it resolves the primary key and uses
// it as the tie-break of whatever order was asked for, and reads the row
// estimate, so one request answers what the grid shows around the rows.
//
// With no order asked for, the page is ordered by the primary key alone. The
// alternative — no ORDER BY at all — is cheaper by nothing on a table with a
// key (the key's index is the order) and made page two a different set of rows
// each time it was asked for.
func BrowseTablePage(ctx context.Context, db *sql.DB, driver Driver, opts BrowseOptions) (*BrowsePage, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	opts.Limit = clampRows(opts.Limit, defaultBrowseRows, MaxBrowseRows)
	if opts.Offset < 0 {
		opts.Offset = 0
	}
	page := &BrowsePage{PrimaryKey: []string{}, Limit: opts.Limit, Offset: opts.Offset}
	// Not fatal: a login that cannot read the constraint catalogue still gets
	// its page, in whatever order the engine returns it.
	if pk, err := d.PrimaryKey(ctx, db, catalogSchema(ctx, db, d, opts.Schema), opts.Table); err == nil && pk != nil {
		page.PrimaryKey = pk
	}
	opts.StableKey = page.PrimaryKey
	page.Sort = effectiveSort(opts)

	page.QueryResult, err = Browse(ctx, db, driver, opts)
	if err != nil {
		return nil, err
	}
	if e, ok := d.(rowEstimator); ok {
		if n, err := e.rowEstimate(ctx, db, opts.Schema, opts.Table); err == nil && n >= 0 {
			page.EstimatedRows = &n
		}
	}
	return page, nil
}

// selection is one unpaged read of a table: the relation, the operator's
// conditions and the order they chose.
type selection struct {
	query   string
	args    []any
	ordered bool
}

// effectiveSort is the order a selection will carry: what was asked for, then
// the tie-break columns that are not already in it.
func effectiveSort(opts BrowseOptions) []SortKey {
	sort := append([]SortKey{}, opts.Sort...)
	if len(sort) == 0 && opts.OrderBy != "" {
		sort = append(sort, SortKey{Column: opts.OrderBy, Desc: opts.Desc})
	}
	seen := map[string]bool{}
	for _, k := range sort {
		seen[k.Column] = true
	}
	for _, col := range opts.StableKey {
		if !seen[col] {
			seen[col] = true
			sort = append(sort, SortKey{Column: col})
		}
	}
	return sort
}

// browseSelect assembles that read. It exists so the grid and the export cannot
// disagree about which rows they are looking at: the export used to be a bare
// SELECT * of the whole table, so narrowing a million rows to eleven and
// pressing Export as CSV produced a million-row file — the filters being
// applied "on the server, across the whole table" made that worse rather than
// better, because it is exactly the claim that makes the download look right.
func browseSelect(d Dialect, opts BrowseOptions) (*selection, error) {
	rel, err := qualify(d, opts.Schema, opts.Table)
	if err != nil {
		return nil, err
	}
	columns := "*"
	if len(opts.Columns) > 0 {
		if len(opts.Columns) > MaxBrowseColumns {
			return nil, fmt.Errorf("at most %d columns can be selected", MaxBrowseColumns)
		}
		quoted := make([]string, len(opts.Columns))
		for i, c := range opts.Columns {
			if quoted[i], err = d.QuoteIdent(c); err != nil {
				return nil, err
			}
		}
		columns = strings.Join(quoted, ", ")
	}
	where, args, err := buildWhereMatch(d, opts.Filters, opts.MatchAny, 1)
	if err != nil {
		return nil, err
	}
	sort := effectiveSort(opts)
	if len(sort) > MaxBrowseColumns {
		return nil, fmt.Errorf("at most %d columns can be sorted by", MaxBrowseColumns)
	}
	order := ""
	for i, k := range sort {
		col, err := d.QuoteIdent(k.Column)
		if err != nil {
			return nil, err
		}
		// The direction is a bool on the wire, never a string, so there is no
		// path by which it becomes anything but one of these two words.
		dir := "ASC"
		if k.Desc {
			dir = "DESC"
		}
		if i == 0 {
			order = " ORDER BY "
		} else {
			order += ", "
		}
		order += col + " " + dir
	}
	return &selection{
		query: "SELECT " + columns + " FROM " + rel + where + order, args: args, ordered: order != "",
	}, nil
}

// BrowseTable is the unfiltered, unsorted form, kept because most callers want
// exactly that and should not have to build an options struct to say so.
func BrowseTable(ctx context.Context, db *sql.DB, driver Driver, schema, table string, limit, offset int) (*QueryResult, error) {
	return Browse(ctx, db, driver, BrowseOptions{
		Schema: schema, Table: table, Limit: limit, Offset: offset,
	})
}

// Count returns the number of rows matching the filters.
//
// It is deliberately a separate request from the page fetch, and the UI treats
// it as optional. COUNT(*) on a large table is a full scan on most engines, so
// pairing it with every page turn would make paging quadratically slower the
// deeper you went — the page itself must stay cheap whether or not anyone asked
// how many rows there are.
func Count(ctx context.Context, db *sql.DB, driver Driver, opts BrowseOptions) (int64, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return 0, err
	}
	rel, err := qualify(d, opts.Schema, opts.Table)
	if err != nil {
		return 0, err
	}
	where, args, err := buildWhereMatch(d, opts.Filters, opts.MatchAny, 1)
	if err != nil {
		return 0, err
	}
	args, err = sqlArguments(args)
	if err != nil {
		return 0, err
	}
	var n int64
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+rel+where, args...).Scan(&n)
	return n, err
}
