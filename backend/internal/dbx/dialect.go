package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// A Dialect is everything one SQL engine does differently.
//
// The alternative — a `switch driver` inside each of the dozen catalogue and
// builder functions — worked for three engines and stopped working at seven:
// every new engine meant finding and extending eleven separate switches, and a
// missed one failed at runtime rather than at compile time. With a Dialect the
// compiler names what an engine has not implemented, and everything an engine
// does differently sits in one readable file next to the SQL it needs.
//
// Implementations own only the *shape* of their SQL. The security rules are not
// theirs to weaken: identifiers pass validateIdent and are quoted by doubling
// the engine's own quote character, values are always bound as parameters, and
// no dialect is given a path that interpolates a value into a statement.
type Dialect interface {
	Driver() Driver

	// --- connecting -------------------------------------------------------

	// SQLDriverName is the name the engine's driver registered with
	// database/sql, which is rarely the name this dashboard calls it.
	SQLDriverName() string
	// NormaliseDSN adjusts a caller-supplied connection string where the engine
	// needs it (SQLite's pragmas, say). Most engines return it unchanged.
	NormaliseDSN(dsn string) string
	// VersionQuery returns a statement selecting one string describing the
	// server, used to confirm a tested connection reached what the operator
	// expected.
	VersionQuery() string
	// TunePool applies engine-specific pool limits on top of the defaults.
	TunePool(db *sql.DB)

	// --- syntax -----------------------------------------------------------

	// QuoteIdent validates and quotes an identifier for this engine.
	QuoteIdent(name string) (string, error)
	// Placeholder renders the n-th (1-based) bind marker.
	Placeholder(n int) string
	// SupportsReturning reports whether INSERT/UPDATE ... RETURNING * works, so
	// a row edit can hand the stored row back rather than guessing at it.
	SupportsReturning() bool
	// Paginate renders the limit/offset tail, with its bind arguments in the
	// order the rendered clause expects them — which is not the same order on
	// every engine.
	Paginate(limit, offset, argStart int) (string, []any)
	// DefaultSchema is where to look when the operator has named no schema.
	DefaultSchema() string
	// CastText wraps a column reference so it can be compared against text.
	// A "contains" filter has to work on a numeric or date column too, and
	// `LIKE` against a non-character type is an error on the stricter engines
	// rather than an implicit coercion.
	CastText(expr string) string

	// --- catalogue --------------------------------------------------------

	Databases(ctx context.Context, db *sql.DB) ([]Database, error)
	Tables(ctx context.Context, db *sql.DB, schema string) ([]Table, error)
	Columns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error)
	PrimaryKey(ctx context.Context, db *sql.DB, schema, table string) ([]string, error)
	Indexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error)
	ForeignKeys(ctx context.Context, db *sql.DB, schema, table string) ([]ForeignKey, error)
	schemaQueries() schemaQueries
	// CreateSQL returns DDL recreating the table. Engines that keep the original
	// text hand it back; the rest synthesise it from the introspected structure.
	CreateSQL(ctx context.Context, db *sql.DB, schema, table string, d *TableDetail) (string, error)

	// --- DDL --------------------------------------------------------------

	// ColumnTypes are the type names the create-table form offers. A closed
	// list per engine, because a free-text type field in a DDL builder is how
	// you get a column of type "vachar".
	ColumnTypes() []string
	// AddColumnKeyword is how this engine introduces a new column.
	//
	// Most spell it `ALTER TABLE t ADD COLUMN c type`. SQL Server and Oracle
	// spell it `ALTER TABLE t ADD c type` and reject the COLUMN keyword outright
	// — a difference invisible until a real server parses the statement, which
	// is exactly how it was found.
	AddColumnKeyword() string
	// Activity lists what the server is currently running. Engines with no
	// server-side session concept return ErrNoActivityView.
	Activity(ctx context.Context, db *sql.DB) ([]Activity, error)
	// Kill terminates a session or statement by the handle Activity reported.
	Kill(ctx context.Context, db *sql.DB, pid string) error
	// ExplainPlan returns the engine's execution plan for a statement.
	//
	// Every implementation must describe the statement without running it. That
	// is not a nicety: the Query tab offers this next to a Run button, and a
	// "show me the plan" that quietly executed a DELETE would be the worst
	// button in the product. Where an engine's plan command has an executing
	// variant (Postgres's EXPLAIN ANALYZE, say), it is deliberately not used.
	ExplainPlan(ctx context.Context, db *sql.DB, query string) (*QueryResult, error)
	// BeforeDropColumn clears anything the engine requires gone before a column
	// can be dropped. Most engines require nothing and return nil.
	BeforeDropColumn(ctx context.Context, db *sql.DB, schema, table, column string) error
	// TableSizes reports what each table in a schema costs on disk. Engines
	// that cannot say return zero bytes with the row estimates they do have,
	// rather than an error — a missing size is information, not a failure.
	TableSizes(ctx context.Context, db *sql.DB, schema string) ([]TableSize, error)
	// SupportsDDL reports whether this engine accepts the generated DDL at all.
	// ClickHouse's CREATE TABLE needs an engine and a sorting key, so it opts
	// out rather than emitting statements that will not run.
	SupportsDDL() bool
	// DropDatabaseSQL returns the statements that remove a database entirely.
	//
	// Only the **last** one has to succeed. The others are preparation the
	// engine needs and that is harmless when it does not apply — terminating
	// the sessions still attached, putting SQL Server into single-user mode —
	// and failing the whole drop because a database had no connections to
	// terminate would be absurd.
	DropDatabaseSQL(name string) ([]DropStatement, error)
	// AdminDatabase is another database on the same server to connect to while
	// dropping one, because Postgres and SQL Server both refuse to drop the
	// database the session is currently in. An empty string means this engine
	// does not care and the connection can stay where it is.
	AdminDatabase() string
}

// A DropStatement is one step of removing a database, with its bound
// arguments. Arguments rather than interpolation even here: a database name is
// an identifier in one of these statements and a *value* in the next, and the
// rule that values are bound does not get an exception because the statement
// happens to be administrative.
type DropStatement struct {
	SQL  string
	Args []any
}

// dialects is the registry. A driver with no entry here is not a SQL engine
// this package can drive, which is exactly what ErrUnsupported reports.
var dialects = map[Driver]Dialect{}

func register(d Dialect) { dialects[d.Driver()] = d }

func init() {
	register(postgresDialect{})
	register(mysqlDialect{})
	register(sqliteDialect{})
	register(mssqlDialect{})
	register(clickhouseDialect{})
	register(oracleDialect{})
}

// DialectFor returns the dialect for a driver, or ErrUnsupported.
func DialectFor(d Driver) (Dialect, error) {
	dl, ok := dialects[d]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, d)
	}
	return dl, nil
}

// --- identifier safety ----------------------------------------------------

// validateIdent is the guard every generated identifier passes.
//
// It deliberately does not restrict identifiers to a conservative character
// class. Real schemas contain hyphens, spaces and non-ASCII letters, and the
// earlier `^[A-Za-z_][A-Za-z0-9_$]*$` rule meant a table named `user-profiles`
// could not be browsed at all — the dashboard reported it in the table list and
// then refused to open it. What actually makes a quoted identifier safe is
// quoting it and doubling the quote character inside, which quoteWith does; the
// only things that cannot be made safe that way are a NUL, which truncates the
// string inside several C client libraries, and the control characters, which
// no legitimate identifier contains.
func validateIdent(name string) error {
	if name == "" {
		return fmt.Errorf("empty identifier")
	}
	if len(name) > 128 {
		return fmt.Errorf("identifier %q is too long", name[:32]+"…")
	}
	for _, r := range name {
		if r == 0 || (r < 0x20) || r == 0x7f {
			return fmt.Errorf("identifier contains a control character")
		}
	}
	return nil
}

// quoteWith validates an identifier and wraps it in the engine's quote
// characters, doubling any occurrence of the closing quote inside the name.
// Doubling is the complete escape for a quoted identifier in every engine here:
// none of them treats a backslash as an escape inside one.
func quoteWith(name, open, close string) (string, error) {
	if err := validateIdent(name); err != nil {
		return "", err
	}
	return open + strings.ReplaceAll(name, close, close+close) + close, nil
}

func quoteDouble(name string) (string, error) { return quoteWith(name, `"`, `"`) }
func quoteBacktick(name string) (string, error) {
	return quoteWith(name, "`", "`")
}
func quoteBracket(name string) (string, error) { return quoteWith(name, "[", "]") }

// --- shared query helpers -------------------------------------------------

// standardLimit is the LIMIT/OFFSET tail used by every engine that speaks it.
func standardLimit(d Dialect, limit, offset, argStart int) (string, []any) {
	return fmt.Sprintf("LIMIT %s OFFSET %s",
		d.Placeholder(argStart), d.Placeholder(argStart+1)), []any{limit, offset}
}

// fetchFirstLimit is the SQL:2008 tail, which takes offset before limit. SQL
// Server and Oracle use it; SQL Server additionally requires an ORDER BY in
// front of OFFSET, which its dialect supplies.
func fetchFirstLimit(d Dialect, limit, offset, argStart int) (string, []any) {
	return fmt.Sprintf("OFFSET %s ROWS FETCH NEXT %s ROWS ONLY",
		d.Placeholder(argStart), d.Placeholder(argStart+1)), []any{offset, limit}
}

// infoSchemaColumns reads columns from the SQL-standard information_schema,
// which Postgres, MySQL, SQL Server and ClickHouse all provide in compatible
// enough form. Only the bind marker differs, so the dialect supplies that.
func infoSchemaColumns(ctx context.Context, db *sql.DB, d Dialect, schema, table string) ([]Column, error) {
	query := `SELECT column_name, data_type, is_nullable, COALESCE(column_default, ''), ordinal_position,
	                 character_maximum_length, numeric_precision, numeric_scale
	          FROM information_schema.columns
	          WHERE table_schema = ` + d.Placeholder(1) + `
	            AND table_name = ` + d.Placeholder(2) + `
	          ORDER BY ordinal_position`
	rows, err := db.QueryContext(ctx, query, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Column{}
	for rows.Next() {
		var (
			c                        Column
			nullable                 string
			length, precision, scale sql.NullInt64
		)
		if err := rows.Scan(&c.Name, &c.Type, &nullable, &c.Default, &c.Position,
			&length, &precision, &scale); err != nil {
			return nil, err
		}
		// information_schema spells this as the text 'YES'/'NO' rather than a
		// boolean, and ClickHouse answers 0/1, so both forms are accepted.
		c.Nullable = strings.EqualFold(nullable, "YES") || nullable == "1"
		c.Type = withTypeSize(c.Type, length, precision, scale)
		out = append(out, c)
	}
	return out, rows.Err()
}

// withTypeSize puts the length or precision back onto a type name.
//
// information_schema splits `nvarchar(255)` into a data_type of `nvarchar` and
// a separate length column, and the bare name is not the same type: SQL Server
// reads `NVARCHAR` with no length in a CREATE TABLE as NVARCHAR(1). So the DDL
// the Structure tab shows, and the DDL a dump replays, silently truncated every
// string column to one character — a restore that "succeeded" into a table that
// could not hold the data.
func withTypeSize(name string, length, precision, scale sql.NullInt64) string {
	lower := strings.ToLower(name)
	if strings.ContainsRune(name, '(') {
		return name
	}
	holdsText := strings.Contains(lower, "char") || strings.Contains(lower, "binary")
	isDecimal := strings.Contains(lower, "numeric") || strings.Contains(lower, "decimal")
	switch {
	case length.Valid && holdsText:
		if length.Int64 < 0 {
			// SQL Server reports -1 for the MAX variants.
			return name + "(MAX)"
		}
		if length.Int64 > 0 {
			return name + "(" + itoa(int(length.Int64)) + ")"
		}
	case precision.Valid && isDecimal:
		if scale.Valid && scale.Int64 > 0 {
			return name + "(" + itoa(int(precision.Int64)) + "," + itoa(int(scale.Int64)) + ")"
		}
		return name + "(" + itoa(int(precision.Int64)) + ")"
	}
	return name
}

// infoSchemaPrimaryKey reads the ordered primary-key columns from the standard
// constraint views.
func infoSchemaPrimaryKey(ctx context.Context, db *sql.DB, d Dialect, schema, table string) ([]string, error) {
	query := `SELECT kcu.column_name
	          FROM information_schema.table_constraints tc
	          JOIN information_schema.key_column_usage kcu
	            ON kcu.constraint_name = tc.constraint_name
	           AND kcu.table_schema = tc.table_schema
	           AND kcu.table_name = tc.table_name
	          WHERE tc.constraint_type = 'PRIMARY KEY'
	            AND tc.table_schema = ` + d.Placeholder(1) + `
	            AND tc.table_name = ` + d.Placeholder(2) + `
	          ORDER BY kcu.ordinal_position`
	rows, err := db.QueryContext(ctx, query, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// scanTables is the common row loop for a catalogue query returning the six
// Table fields in order.
// nullText scans a possibly-NULL text column into a plain string, NULL
// becoming "".
//
// Oracle makes this unavoidable rather than merely tidy: it has no empty
// string — ” *is* NULL there — so the usual `NVL(col, ”)` guard is a no-op
// and every optional text column in its catalogue comes back NULL, which
// database/sql refuses to scan into a string. The other engines want it too:
// a comment nobody wrote, an owner the catalogue does not record and a
// referential action that is the default are all legitimately NULL on all of
// them, and the alternative is an engine-shaped COALESCE in every query.
type nullText struct{ dst *string }

func (n nullText) Scan(v any) error {
	switch x := v.(type) {
	case nil:
		*n.dst = ""
	case string:
		*n.dst = x
	case []byte:
		*n.dst = string(x)
	default:
		*n.dst = fmt.Sprint(x)
	}
	return nil
}

func scanTables(rows *sql.Rows) ([]Table, error) {
	defer rows.Close()
	out := []Table{}
	for rows.Next() {
		var t Table
		if err := rows.Scan(&t.Schema, &t.Name, &t.Type, &t.Rows, &t.Size, nullText{&t.Comment}); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// scanDatabases is the common row loop for a database listing.
func scanDatabases(rows *sql.Rows) ([]Database, error) {
	defer rows.Close()
	out := []Database{}
	for rows.Next() {
		var d Database
		if err := rows.Scan(&d.Name, &d.Size, nullText{&d.Owner}, nullText{&d.Encoding}); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// indexAcc and fkAcc accumulate rows that arrive one column at a time into
// ordered per-name groups, preserving first-seen order of the groups.
type indexAcc struct {
	order []string
	byKey map[string]*Index
}

func newIndexAcc() *indexAcc { return &indexAcc{byKey: map[string]*Index{}} }

func (a *indexAcc) add(name, col string, unique, primary bool) {
	ix, ok := a.byKey[name]
	if !ok {
		ix = &Index{Name: name, Unique: unique, Primary: primary, Columns: []string{}}
		a.byKey[name] = ix
		a.order = append(a.order, name)
	}
	if col != "" {
		ix.Columns = append(ix.Columns, col)
	}
}

func (a *indexAcc) slice() []Index {
	out := make([]Index, 0, len(a.order))
	for _, n := range a.order {
		out = append(out, *a.byKey[n])
	}
	return out
}

type fkAcc struct {
	order []string
	byKey map[string]*ForeignKey
}

func newFKAcc() *fkAcc { return &fkAcc{byKey: map[string]*ForeignKey{}} }

func (a *fkAcc) get(name string) *ForeignKey {
	fk, ok := a.byKey[name]
	if !ok {
		fk = &ForeignKey{Name: name, Columns: []string{}, RefColumns: []string{}}
		a.byKey[name] = fk
		a.order = append(a.order, name)
	}
	return fk
}

func (a *fkAcc) slice() []ForeignKey {
	out := make([]ForeignKey, 0, len(a.order))
	for _, n := range a.order {
		out = append(out, *a.byKey[n])
	}
	return out
}

// noForeignKeys is for engines that have no referential constraints at all
// (ClickHouse). Returning an empty slice rather than an error keeps the
// Structure tab rendering the facts the engine does have.
func noForeignKeys(context.Context, *sql.DB, string, string) ([]ForeignKey, error) {
	return []ForeignKey{}, nil
}
