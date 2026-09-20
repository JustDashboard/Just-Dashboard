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
}

// BrowseOptions is everything the data grid can ask for.
type BrowseOptions struct {
	Schema  string
	Table   string
	Limit   int
	Offset  int
	OrderBy string
	Desc    bool
	Filters []Filter
}

// filterOps maps an operator name to the SQL it renders and how many values it
// binds. A missing entry is a rejected request, so a new operator cannot be
// smuggled in as a string.
var filterOps = map[string]struct {
	sql  string // %s is the (quoted) column, %p the placeholder
	args int
}{
	"eq":       {"%s = %p", 1},
	"ne":       {"%s <> %p", 1},
	"lt":       {"%s < %p", 1},
	"lte":      {"%s <= %p", 1},
	"gt":       {"%s > %p", 1},
	"gte":      {"%s >= %p", 1},
	"contains": {"", 1}, // rendered specially: needs the dialect's text cast
	"prefix":   {"", 1},
	"is_null":  {"%s IS NULL", 0},
	"not_null": {"%s IS NOT NULL", 0},
}

// FilterOps is the operator list the UI offers, so the frontend does not keep a
// second copy that can drift from what the server accepts.
func FilterOps() []string {
	return []string{"eq", "ne", "lt", "lte", "gt", "gte", "contains", "prefix", "is_null", "not_null"}
}

// buildWhere renders the filter list into a WHERE clause and its bound
// arguments. argStart is the first placeholder number to use, so the caller can
// place the filters before or after its own paging parameters.
func buildWhere(d Dialect, filters []Filter, argStart int) (string, []any, error) {
	if len(filters) == 0 {
		return "", nil, nil
	}
	parts := make([]string, 0, len(filters))
	args := []any{}
	n := argStart
	for _, f := range filters {
		spec, ok := filterOps[f.Op]
		if !ok {
			return "", nil, fmt.Errorf("unsupported filter operator %q", f.Op)
		}
		col, err := d.QuoteIdent(f.Column)
		if err != nil {
			return "", nil, err
		}
		switch f.Op {
		case "contains", "prefix":
			// Both compare against text, so the column is cast first — LIKE
			// against an integer column is an error on Postgres rather than an
			// implicit coercion, and "find me the row with 42 in the id" is a
			// thing operators reasonably try.
			parts = append(parts, fmt.Sprintf("%s LIKE %s", d.CastText(col), d.Placeholder(n)))
			pattern := f.Value + "%"
			if f.Op == "contains" {
				pattern = "%" + f.Value + "%"
			}
			args = append(args, pattern)
			n++
		default:
			frag := strings.ReplaceAll(spec.sql, "%s", col)
			if spec.args == 1 {
				frag = strings.ReplaceAll(frag, "%p", d.Placeholder(n))
				args = append(args, f.Value)
				n++
			}
			parts = append(parts, frag)
		}
	}
	return " WHERE " + strings.Join(parts, " AND "), args, nil
}

// Browse pages through a table, optionally sorted and filtered.
//
// Sorting is opt-in and never defaulted. Adding an implicit ORDER BY would turn
// a cheap page fetch into a full sort on a table with millions of rows and no
// useful index — but paging with no order at all is formally undefined, so the
// grid asks for a sort the moment the operator cares about which rows page two
// holds, and the column it names is validated against the table rather than
// trusted.
func Browse(ctx context.Context, db *sql.DB, driver Driver, opts BrowseOptions) (*QueryResult, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	if opts.Limit <= 0 || opts.Limit > 1000 {
		opts.Limit = 100
	}
	if opts.Offset < 0 {
		opts.Offset = 0
	}
	sel, err := browseSelect(d, opts)
	if err != nil {
		return nil, err
	}

	tail, tailArgs := d.Paginate(opts.Limit, opts.Offset, len(sel.args)+1)
	// SQL Server's Paginate supplies its own mandatory ORDER BY; when the
	// operator has chosen one, theirs replaces it rather than sitting next to it.
	if sel.ordered {
		tail = strings.TrimPrefix(tail, "ORDER BY (SELECT NULL) ")
	}
	args := append(sel.args, tailArgs...)

	return RunQuery(ctx, db, sel.query+" "+tail, opts.Limit, args...)
}

// selection is one unpaged read of a table: the relation, the operator's
// conditions and the order they chose.
type selection struct {
	query   string
	args    []any
	ordered bool
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
	where, args, err := buildWhere(d, opts.Filters, 1)
	if err != nil {
		return nil, err
	}
	order := ""
	if opts.OrderBy != "" {
		col, err := d.QuoteIdent(opts.OrderBy)
		if err != nil {
			return nil, err
		}
		// The direction is a bool on the wire, never a string, so there is no
		// path by which it becomes anything but one of these two words.
		dir := "ASC"
		if opts.Desc {
			dir = "DESC"
		}
		order = " ORDER BY " + col + " " + dir
	}
	return &selection{query: "SELECT * FROM " + rel + where + order, args: args, ordered: order != ""}, nil
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
	where, args, err := buildWhere(d, opts.Filters, 1)
	if err != nil {
		return 0, err
	}
	var n int64
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+rel+where, args...).Scan(&n)
	return n, err
}
