package dbx

import (
	"context"
	"database/sql"
	"strings"
)

// What the table editor and the query runner need from an engine beyond the
// Dialect interface. Each is its own small interface, found by type assertion,
// because none of them is something every engine has: an engine with no regular
// expressions simply does not implement regexMatcher, and the operator list it
// advertises is shorter by one.

// matchKind is where in the text a substring filter looks.
type matchKind uint8

const (
	matchContains matchKind = iota
	matchPrefix
	matchSuffix
)

// textMatcher is implemented by the engines whose substring test is not the
// shared LIKE … ESCAPE form.
type textMatcher interface {
	// textMatch renders the predicate for expr against the bind marker ph, and
	// returns how the operator's value becomes the bound argument.
	textMatch(expr string, kind matchKind, fold bool, ph string) (string, func(string) string)
}

// regexMatcher is implemented by the engines with a regular-expression
// operator. The pattern is the engine's own flavour; nothing here translates.
type regexMatcher interface {
	regexMatch(expr, ph string) string
}

// rowEstimator answers "about how many rows" from the engine's statistics,
// without counting. -1 means the engine has no figure for this table.
type rowEstimator interface {
	rowEstimate(ctx context.Context, db *sql.DB, schema, table string) (int64, error)
}

// keyComparer is implemented by the engines where some column types cannot be
// compared with =. A table with no primary key is matched on its whole row, so
// every column has to be comparable somehow.
type keyComparer interface {
	keyExpr(column Column, quoted string) string
}

// keyMatcher is implemented by the engines where some column types have no =
// at all, so the whole test is the engine's to write and not only its left
// side.
type keyMatcher interface {
	// keyMatch renders the test that a column holds the value operand stands
	// for, or "" for a type that is compared the ordinary way.
	keyMatch(column Column, quoted, operand string) string
}

// valueWriter is implemented by an engine that will not read some values from
// the text the grid shows them as.
type valueWriter interface {
	// writeValue returns what to bind for a value of this column, and how the
	// bind marker that stands for it is written in the statement. A nil
	// function is the marker on its own.
	writeValue(column Column, arg any) (bound any, write func(operand string) string)
}

// columnReader is implemented by an engine whose driver does not read every
// column type as SELECT * returns it. A table's columns are then selected by
// name, each through the expression that makes it readable, and typed from the
// catalogue rather than from what the driver calls the result.
type columnReader interface {
	// readExpr is how a column is selected. It is quoted, unchanged, for a
	// type the driver reads as it is stored.
	readExpr(column Column, quoted string) string
}

// columnTexter is implemented by an engine whose text form of some types is
// not the one the grid shows: a substring filter looks in the text the reader
// is looking at.
type columnTexter interface {
	// textExpr is the column as that text. quoted, unchanged, for the types
	// the engine's own cast already reads that way.
	textExpr(column Column, quoted string) string
}

// needsCatalog reports whether an engine's statements depend on what the
// catalogue says a column is.
func needsCatalog(d Dialect) bool {
	_, reads := d.(columnReader)
	_, writes := d.(valueWriter)
	_, texts := d.(columnTexter)
	return reads || writes || texts
}

// planner is implemented by the engines with more than one form of plan: a
// JSON form a tree can be drawn from, or one that executes the statement and
// reports what actually happened.
type planner interface {
	// explainSQL returns the statement that produces the plan. version is the
	// server's own version string, for the engines whose syntax depends on it.
	explainSQL(version, statement string, opts ExplainOptions) (string, error)
}

// likeEscape is the escape character every LIKE this package renders uses.
// Not the backslash: writing a backslash in a string literal means one thing
// on MySQL and another under NO_BACKSLASH_ESCAPES, and this character means
// the same thing everywhere.
const likeEscape = "!"

// likePattern makes an operator's text literal inside a LIKE pattern. Without
// it a search for "50%" matched everything that started with 50.
func likePattern(value string, kind matchKind, wildcards string) string {
	var b strings.Builder
	for _, r := range value {
		if strings.ContainsRune(wildcards, r) {
			b.WriteString(likeEscape)
		}
		b.WriteRune(r)
	}
	switch kind {
	case matchPrefix:
		return b.String() + "%"
	case matchSuffix:
		return "%" + b.String()
	}
	return "%" + b.String() + "%"
}

// likeMatch is the shared substring test. The column is cast to text first —
// LIKE against an integer column is an error on Postgres rather than an
// implicit coercion, and "find me the row with 42 in the id" is a thing
// operators reasonably try. Folding lowers both sides, which is the one
// spelling of case-insensitive that every engine here agrees on.
func likeMatch(d Dialect, expr string, kind matchKind, fold bool, ph, wildcards string) (string, func(string) string) {
	left, right := d.CastText(expr), ph
	if fold {
		left, right = "LOWER("+left+")", "LOWER("+ph+")"
	}
	return left + " LIKE " + right + " ESCAPE '" + likeEscape + "'",
		func(value string) string { return likePattern(value, kind, wildcards) }
}

// textMatchFor renders a substring test for any dialect.
func textMatchFor(d Dialect, expr string, kind matchKind, fold bool, ph string) (string, func(string) string) {
	if m, ok := d.(textMatcher); ok {
		return m.textMatch(expr, kind, fold, ph)
	}
	return likeMatch(d, expr, kind, fold, ph, likeEscape+"%_")
}

// keyExprFor is how a column is compared when it identifies a row.
func keyExprFor(d Dialect, column Column, quoted string) string {
	if c, ok := d.(keyComparer); ok {
		return c.keyExpr(column, quoted)
	}
	return quoted
}

// keyMatchFor is the test that a column holds the value operand stands for.
func keyMatchFor(d Dialect, column Column, quoted, operand string) string {
	if m, ok := d.(keyMatcher); ok {
		if test := m.keyMatch(column, quoted, operand); test != "" {
			return test
		}
	}
	return keyExprFor(d, column, quoted) + " = " + operand
}

// projection is a table's select list for a columnReader, and what each of its
// columns holds according to the catalogue.
type projection struct {
	list  string
	kinds map[string]string
}

// projectionFor returns how a table's columns are selected: nil for the
// engines that are asked for * and read what comes back. only narrows and
// orders the list; a name the catalogue does not have is left for the engine
// to refuse.
func projectionFor(d Dialect, cols []Column, only []string) (*projection, error) {
	reader, ok := d.(columnReader)
	if !ok || len(cols) == 0 {
		return nil, nil
	}
	known := make(map[string]Column, len(cols))
	names := make([]string, 0, len(cols))
	for _, c := range cols {
		known[c.Name] = c
		names = append(names, c.Name)
	}
	if len(only) > 0 {
		names = only
	}
	p := &projection{kinds: map[string]string{}}
	parts := make([]string, 0, len(names))
	for _, name := range names {
		quoted, err := d.QuoteIdent(name)
		if err != nil {
			return nil, err
		}
		if col, ok := known[name]; ok {
			p.kinds[name] = ValueKind(d.Driver(), col.Type)
			if expr := reader.readExpr(col, quoted); expr != quoted {
				quoted = expr + " AS " + quoted
			}
		}
		parts = append(parts, quoted)
	}
	p.list = strings.Join(parts, ", ")
	return p, nil
}

// currentSchemaQueries ask each engine where it looks up a table named without
// a schema. It is asked rather than assumed: a ClickHouse connection is in the
// database its DSN names, not in `default`, and a PostgreSQL role may have a
// search_path that does not start with `public`.
var currentSchemaQueries = map[Driver]string{
	DriverPostgres:   "SELECT current_schema()",
	DriverMySQL:      "SELECT DATABASE()",
	DriverMSSQL:      "SELECT SCHEMA_NAME()",
	DriverClickHouse: "SELECT currentDatabase()",
}

// catalogSchema is the schema to look a table up in when the request named
// none. The statement itself can leave the schema out and let the engine
// resolve the name; a catalogue query cannot, because it filters on the schema
// as a value. Oracle's catalogue queries resolve an empty schema themselves and
// SQLite has none to resolve.
func catalogSchema(ctx context.Context, db *sql.DB, d Dialect, schema string) string {
	if schema != "" {
		return schema
	}
	if query, ok := currentSchemaQueries[d.Driver()]; ok {
		var current sql.NullString
		if err := db.QueryRowContext(ctx, query).Scan(&current); err == nil && current.String != "" {
			return current.String
		}
	}
	return d.DefaultSchema()
}
