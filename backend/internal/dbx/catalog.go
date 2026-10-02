package dbx

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// The catalogue is everything a database holds that is not a row: its schemas,
// and inside each one the tables, views, routines, triggers, sequences and
// types. The table list alone was enough while the product was a data browser;
// a schema browser has to be able to say "there is a trigger on this table"
// and "this column is one of these five labels", and both live here.
//
// Every engine answers from a different set of system views, so the queries
// are per-dialect (catalog_<engine>.go) and this file holds only the shapes
// they fill and the walk that calls them. Nothing here writes.

// Object kinds, as they travel in CatalogObject.Kind and back in ObjectRef.Kind.
const (
	KindTable            = "table"
	KindView             = "view"
	KindMaterializedView = "materialized_view"
	KindFunction         = "function"
	KindProcedure        = "procedure"
	KindTrigger          = "trigger"
	KindSequence         = "sequence"
	KindEnum             = "enum"
	KindDomain           = "domain"
	KindComposite        = "composite"
	KindRange            = "range"
	KindType             = "type"
	KindEvent            = "event"
	KindPackage          = "package"
	KindSynonym          = "synonym"
	KindDictionary       = "dictionary"
)

// Catalogue groups: the keys of Catalog.Objects, which are also the branches a
// schema tree draws. A group an engine does not have is absent from the map,
// which is how the tree knows not to draw an empty "Sequences" under MySQL.
const (
	GroupTables            = "tables"
	GroupViews             = "views"
	GroupMaterializedViews = "materializedViews"
	GroupFunctions         = "functions"
	GroupProcedures        = "procedures"
	GroupTriggers          = "triggers"
	GroupSequences         = "sequences"
	GroupTypes             = "types"
	GroupEvents            = "events"
	GroupPackages          = "packages"
	GroupSynonyms          = "synonyms"
	GroupDictionaries      = "dictionaries"
)

// Table types, as ListTables, the graph and a table's detail report them.
// Anything else is the engine's own word for a relation it lists.
const (
	TableTypeTable            = "table"
	TableTypeView             = "view"
	TableTypeMaterializedView = "materialized view"
	// A partitioned table holds no rows itself; its partitions do. Both are
	// named apart from an ordinary table because a diagram of two hundred
	// monthly partitions is two hundred copies of one box.
	TableTypePartitioned = "partitioned table"
	TableTypePartition   = "partition"
)

// CatalogGroups names the groups an engine's catalogue answers with, in the
// order a tree draws them. It is the static form of what ReadCatalog returns,
// for the driver catalogue to advertise before any connection exists: a page
// can know that Postgres has sequences and MySQL does not without asking a
// server. flavor is the product behind the driver where one driver serves
// several ("mariadb" behind mysql); an empty flavor is the driver's own.
func CatalogGroups(driver Driver, flavor string) []string {
	switch driver {
	case DriverPostgres:
		return []string{GroupTables, GroupViews, GroupMaterializedViews, GroupFunctions, GroupProcedures,
			GroupTriggers, GroupSequences, GroupTypes}
	case DriverMySQL:
		groups := []string{GroupTables, GroupViews, GroupFunctions, GroupProcedures, GroupTriggers, GroupEvents}
		if strings.EqualFold(flavor, "mariadb") {
			groups = append(groups, GroupSequences)
		}
		return groups
	case DriverSQLite:
		return []string{GroupTables, GroupViews, GroupTriggers}
	case DriverMSSQL:
		return []string{GroupTables, GroupViews, GroupFunctions, GroupProcedures, GroupTriggers,
			GroupSequences, GroupTypes, GroupSynonyms}
	case DriverOracle:
		return []string{GroupTables, GroupViews, GroupMaterializedViews, GroupFunctions, GroupProcedures,
			GroupPackages, GroupTriggers, GroupSequences, GroupTypes, GroupSynonyms}
	case DriverClickHouse:
		return []string{GroupTables, GroupViews, GroupMaterializedViews, GroupDictionaries, GroupFunctions}
	default:
		return []string{}
	}
}

// DefaultCatalogLimit bounds each group. A schema with forty thousand
// functions (PostGIS plus a few extensions gets there) is listed up to the
// bound and says it stopped, rather than building a reply nobody can scroll.
const (
	DefaultCatalogLimit = 5000
	MaxCatalogLimit     = 20000
)

// CatalogSchema is one namespace: a Postgres or SQL Server schema, a MySQL or
// ClickHouse database, an Oracle user, a SQLite attached file.
type CatalogSchema struct {
	Name    string `json:"name"`
	Owner   string `json:"owner,omitempty"`
	Comment string `json:"comment,omitempty"`
	// System marks the engine's own namespaces, which a tree hides by default
	// and a curious operator may still open.
	System bool `json:"system,omitempty"`
	// Default marks the namespace an unqualified name resolves to on this
	// connection.
	Default bool `json:"default,omitempty"`
	// Tables counts the relations (tables and views) inside, -1 where the
	// engine would have to be asked once per schema to say.
	Tables int `json:"tables"`
	// Detail is one engine-specific phrase: a character set, a database
	// engine, the file an attached SQLite database lives in.
	Detail string `json:"detail,omitempty"`
}

// CatalogObject is one entry in the tree, with what the catalogue can say
// about it without reading the object itself.
type CatalogObject struct {
	Kind    string `json:"kind"`
	Schema  string `json:"schema"`
	Name    string `json:"name"`
	Owner   string `json:"owner,omitempty"`
	Comment string `json:"comment,omitempty"`
	// Rows is the planner's estimate and -1 where there is none. It is absent
	// for kinds that hold no rows.
	Rows *int64 `json:"estimatedRows,omitempty"`
	Size int64  `json:"size,omitempty"`
	// Table is the table a trigger fires on, or the column a sequence feeds
	// ("table.column"); for a partition, the table it is a partition of.
	Table string `json:"table,omitempty"`
	// Signature is the argument list that tells two overloads apart. It goes
	// back verbatim when the definition is asked for.
	Signature string `json:"signature,omitempty"`
	Returns   string `json:"returns,omitempty"`
	Language  string `json:"language,omitempty"`
	// Detail is one engine-specific phrase: a storage engine, a trigger's
	// timing and events, a domain's base type.
	Detail string `json:"detail,omitempty"`
	// Values are an enum's labels in their declared order.
	Values []string `json:"values,omitempty"`
	// Extension names the extension that installed the object. Its functions
	// are real and callable, and nobody browsing their own schema wants the
	// nine hundred PostGIS ones mixed in with the four they wrote.
	Extension string `json:"extension,omitempty"`
}

// Catalog is one read of a database's object tree.
type Catalog struct {
	// Schema is the namespace Objects came from, and empty when they came from
	// every one that is not the engine's own.
	Schema        string          `json:"schema"`
	DefaultSchema string          `json:"defaultSchema"`
	Schemas       []CatalogSchema `json:"schemas"`
	// Objects is keyed by group. A group the engine does not have is absent; a
	// group it has and that is empty is an empty list.
	Objects map[string][]CatalogObject `json:"objects"`
	// Truncated names the groups that reached Limit and were cut there.
	Truncated []string `json:"truncated"`
	Limit     int      `json:"limit"`
	// Errors holds the reason a group could not be read, keyed by group. One
	// refused system view must not cost the operator the other seven groups.
	Errors map[string]string `json:"errors,omitempty"`
}

// CatalogOptions narrows a catalogue read.
type CatalogOptions struct {
	// Schema is the namespace to list. Empty means the connection's own unless
	// All is set.
	Schema string
	All    bool
	Limit  int
}

// catalogGroup is one branch of the tree and the query that fills it. limit is
// already one more than the caller wants, so a full page proves truncation.
type catalogGroup struct {
	name string
	read func(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error)
}

// catalogDialect is the catalogue side of a dialect. It is a second interface
// rather than more methods on Dialect because nothing else in the package
// needs it: the browse, mutate and dump paths compile against Dialect and are
// not made to care that a sixth kind of object can now be listed.
type catalogDialect interface {
	// catalogSchemas lists the namespaces and names the one an unqualified
	// name resolves to on this connection.
	catalogSchemas(ctx context.Context, db *sql.DB) (schemas []CatalogSchema, current string, err error)
	// catalogGroups returns the engine's groups in tree order. It takes the
	// connection because one driver serves more than one product: MariaDB has
	// sequences and MySQL does not.
	catalogGroups(ctx context.Context, db *sql.DB) []catalogGroup
	// objectDefinition returns the CREATE text of one object.
	objectDefinition(ctx context.Context, db *sql.DB, ref ObjectRef) (*ObjectDefinition, error)

	// The table reads below fill what Dialect's own reads leave out. Each is
	// best effort: a login that may not read one system view still gets the
	// facts from the others.
	tableColumns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error)
	tableIndexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error)
	tableConstraints(ctx context.Context, db *sql.DB, schema, table string) ([]Constraint, error)
	tableReferencedBy(ctx context.Context, db *sql.DB, schema, table string) ([]IncomingForeignKey, error)
	tableFacts(ctx context.Context, db *sql.DB, schema, table string, detail *TableDetail) error
}

// relationTypeReader is implemented by an engine whose table list cannot tell
// every kind of relation apart. It returns the type of each relation the list
// gets wrong: a Postgres partition, which is listed as a table; a ClickHouse
// materialized view, which is listed as a view.
type relationTypeReader interface {
	relationTypes(ctx context.Context, db *sql.DB, schema string) (map[catalogTable]string, error)
}

// schemaTableReader is implemented by an engine whose own table reads cannot
// be pointed at a schema. SQLite's are written for the main database, so a
// table in an attached file was answered with the facts of whatever table has
// the same name in main.
type schemaTableReader interface {
	tablePrimaryKey(ctx context.Context, db *sql.DB, schema, table string) ([]string, error)
	tableForeignKeys(ctx context.Context, db *sql.DB, schema, table string) ([]ForeignKey, error)
	tableCreateSQL(ctx context.Context, db *sql.DB, schema, table string) (string, error)
}

func catalogFor(driver Driver) (Dialect, catalogDialect, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, nil, err
	}
	cd, ok := d.(catalogDialect)
	if !ok {
		return nil, nil, fmt.Errorf("%w: %s has no object catalogue", ErrUnsupported, driver)
	}
	return d, cd, nil
}

// ReadCatalog lists a database's schemas and the objects of one of them.
//
// Only the schema list is fatal. Each group is its own query against its own
// system view, and the least-privileged logins are exactly the ones that can
// read pg_class and not pg_proc: failing the whole tree there would turn a
// partial answer into none.
func ReadCatalog(ctx context.Context, db *sql.DB, driver Driver, opts CatalogOptions) (*Catalog, error) {
	_, cd, err := catalogFor(driver)
	if err != nil {
		return nil, err
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultCatalogLimit
	}
	limit = min(limit, MaxCatalogLimit)

	schemas, current, err := cd.catalogSchemas(ctx, db)
	if err != nil {
		return nil, err
	}
	if schemas == nil {
		schemas = []CatalogSchema{}
	}
	schema := strings.TrimSpace(opts.Schema)
	if schema == "" && !opts.All {
		schema = current
	}
	out := &Catalog{
		Schema: schema, DefaultSchema: current, Schemas: schemas,
		Objects: map[string][]CatalogObject{}, Truncated: []string{}, Limit: limit,
	}
	for _, group := range cd.catalogGroups(ctx, db) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		objects, err := group.read(ctx, db, schema, limit+1)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if out.Errors == nil {
				out.Errors = map[string]string{}
			}
			out.Errors[group.name] = err.Error()
			out.Objects[group.name] = []CatalogObject{}
			continue
		}
		if objects == nil {
			objects = []CatalogObject{}
		}
		if len(objects) > limit {
			objects = objects[:limit]
			out.Truncated = append(out.Truncated, group.name)
		}
		out.Objects[group.name] = objects
	}
	return out, nil
}

// ObjectRef names one object. Signature tells overloaded routines apart and
// Table says which table a trigger is on, on the engines where a trigger's
// name is only unique per table.
type ObjectRef struct {
	Kind      string
	Schema    string
	Name      string
	Signature string
	Table     string
}

// ObjectFact is one named detail of an object, kept as an ordered list rather
// than a map because the order is part of what makes a definition readable.
type ObjectFact struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ObjectDefinition is an object's CREATE statement and what else is worth
// saying about it.
type ObjectDefinition struct {
	Kind      string `json:"kind"`
	Schema    string `json:"schema"`
	Name      string `json:"name"`
	Signature string `json:"signature,omitempty"`
	Table     string `json:"table,omitempty"`
	// Definition is the CREATE text. Source says whether the server wrote it
	// ("engine") or it was assembled here from the catalogue ("generated"):
	// the first can be pasted back, the second is a faithful description that
	// has never been parsed by anything.
	Definition string `json:"definition"`
	Source     string `json:"source"`
	// Note explains a definition that is missing or partial — most often a
	// login that may see the object and not its text.
	Note     string       `json:"note,omitempty"`
	Owner    string       `json:"owner,omitempty"`
	Comment  string       `json:"comment,omitempty"`
	Language string       `json:"language,omitempty"`
	Returns  string       `json:"returns,omitempty"`
	Values   []string     `json:"values,omitempty"`
	Details  []ObjectFact `json:"details"`
}

const (
	DefinitionFromEngine = "engine"
	DefinitionGenerated  = "generated"
)

// ErrObjectNotFound is returned when the catalogue has no object by that name
// and kind, so the handler can answer 404 rather than an empty definition.
var ErrObjectNotFound = errors.New("no such object")

// ErrAmbiguousObject is returned when a name alone does not pick one object:
// an overloaded routine asked for without its signature, a trigger name used
// on two tables.
var ErrAmbiguousObject = errors.New("more than one object by that name")

// ErrInvalidObject is returned for a reference that names nothing a catalogue
// could hold: no kind, or a name that is not an identifier.
var ErrInvalidObject = errors.New("invalid object reference")

// ErrNoDefinition is returned for a kind an engine does not have at all.
var ErrNoDefinition = errors.New("this engine has no such kind of object")

// ReadObjectDefinition returns the definition of one catalogue object.
func ReadObjectDefinition(ctx context.Context, db *sql.DB, driver Driver, ref ObjectRef) (*ObjectDefinition, error) {
	d, cd, err := catalogFor(driver)
	if err != nil {
		return nil, err
	}
	ref.Kind = strings.TrimSpace(ref.Kind)
	if ref.Kind == "" {
		return nil, fmt.Errorf("%w: an object kind is required", ErrInvalidObject)
	}
	if err := validateIdent(ref.Name); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidObject, err)
	}
	if ref.Schema != "" {
		if err := validateIdent(ref.Schema); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidObject, err)
		}
	}
	if ref.Kind == KindTable {
		return tableDefinition(ctx, db, d, ref)
	}
	def, err := cd.objectDefinition(ctx, db, ref)
	if err != nil {
		return nil, err
	}
	if def.Details == nil {
		def.Details = []ObjectFact{}
	}
	if def.Source == "" {
		def.Source = DefinitionFromEngine
	}
	def.Definition = strings.TrimSpace(def.Definition)
	return def, nil
}

// tableDefinition answers the object route for a table from the same read the
// table route uses, so the two cannot disagree about what a table is.
func tableDefinition(ctx context.Context, db *sql.DB, d Dialect, ref ObjectRef) (*ObjectDefinition, error) {
	detail, err := DescribeTable(ctx, db, d.Driver(), ref.Schema, ref.Name)
	if err != nil {
		return nil, err
	}
	// A table may legitimately have no columns; one the catalogue has no entry
	// for either is not there.
	if len(detail.Columns) == 0 && detail.Type == "" {
		return nil, ErrObjectNotFound
	}
	def := &ObjectDefinition{
		Kind: KindTable, Schema: detail.Schema, Name: detail.Name,
		Definition: strings.TrimSpace(detail.CreateSQL), Source: detail.CreateSQLSource,
		Owner: detail.Owner, Comment: detail.Comment,
		Details: append([]ObjectFact{}, detail.Facts...),
	}
	if def.Source == "" {
		def.Source = DefinitionFromEngine
	}
	return def, nil
}

// TableKey is how this package names a table wherever one name has to stand
// for it: the keys of the relations and outline maps, the node ids of the
// schema graph. The bare name did that job until two schemas held a table
// called `users`, at which point one of them silently overwrote the other.
func TableKey(schema, name string) string {
	if schema == "" {
		return name
	}
	return schema + "." + name
}

// holdsForeignKeys reports whether a relation of this type can carry
// referential constraints of its own. Views and materialized views cannot, and
// a partition's are its parent's, repeated.
func holdsForeignKeys(tableType string) bool {
	switch strings.ToLower(tableType) {
	case TableTypeTable, "base table", TableTypePartitioned:
		return true
	default:
		return false
	}
}

// listTables is the dialect's table list with what it gets wrong put right:
// Postgres reports a partitioned table by its relkind letter and a partition
// as a table, ClickHouse reports a materialized view as a view, and MySQL
// writes the word VIEW where a view's comment would be.
func listTables(ctx context.Context, db *sql.DB, d Dialect, schema string) ([]Table, error) {
	tables, err := d.Tables(ctx, db, schema)
	if err != nil {
		return nil, err
	}
	var types map[catalogTable]string
	if reader, ok := d.(relationTypeReader); ok {
		// Best effort: a list that cannot tell these apart is still the list,
		// and was the whole answer until now.
		types, _ = reader.relationTypes(ctx, db, schema)
	}
	for i := range tables {
		t := &tables[i]
		if t.Type == "p" {
			t.Type = TableTypePartitioned
		}
		if actual, ok := types[catalogTable{t.Schema, t.Name}]; ok {
			t.Type = actual
		}
		if strings.EqualFold(t.Type, TableTypeView) && t.Comment == "VIEW" {
			t.Comment = ""
		}
	}
	return tables, nil
}

// drawable reports whether a relation belongs in a schema diagram. A partition
// is its parent's storage — drawn, it is one more copy of the same box with
// the same keys — and a sequence or a dictionary is not a table of the model
// at all, whatever the engine's table list says.
func drawable(tableType string) bool {
	switch strings.ToLower(tableType) {
	case TableTypePartition, "sequence", "dictionary":
		return false
	default:
		return true
	}
}

// tableColumns reads a table's columns through the catalogue dialect, which
// knows real type names, comments and identity, and falls back to the plain
// dialect read when that is refused or finds nothing — an older server, a
// system view the login may not read.
func tableColumns(ctx context.Context, db *sql.DB, d Dialect, schema, table string) ([]Column, error) {
	if cd, ok := d.(catalogDialect); ok {
		if cols, err := cd.tableColumns(ctx, db, schema, table); err == nil && len(cols) > 0 {
			return cols, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return d.Columns(ctx, db, schema, table)
}

// tableIndexes is the same arrangement for indexes: method, predicate, size
// and expression columns where the engine reports them, the plain list where
// it does not.
func tableIndexes(ctx context.Context, db *sql.DB, d Dialect, schema, table string) ([]Index, error) {
	if cd, ok := d.(catalogDialect); ok {
		if indexes, err := cd.tableIndexes(ctx, db, schema, table); err == nil && indexes != nil {
			return indexes, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return d.Indexes(ctx, db, schema, table)
}

// --- helpers shared by the per-engine catalogue files -----------------------

// jsonStrings decodes a JSON array of strings a query aggregated server-side.
// Aggregating to JSON rather than to the engine's own array type keeps the
// scan free of driver-specific array handling, and an enum label or a column
// name may contain any separator a delimited string could be split on.
func jsonStrings(raw string) []string {
	out := []string{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	if out == nil {
		out = []string{}
	}
	return out
}

func rowEstimate(n int64) *int64 { return &n }

// facts builds an ObjectFact list, leaving out the ones with nothing to say.
func facts(pairs ...string) []ObjectFact {
	out := []ObjectFact{}
	for i := 0; i+1 < len(pairs); i += 2 {
		if strings.TrimSpace(pairs[i+1]) != "" {
			out = append(out, ObjectFact{Name: pairs[i], Value: pairs[i+1]})
		}
	}
	return out
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

// scanObjects runs a catalogue query and hands each row to scan.
func scanObjects(ctx context.Context, db *sql.DB, query string, args []any, scan func(*sql.Rows) (CatalogObject, error)) ([]CatalogObject, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CatalogObject{}
	for rows.Next() {
		o, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// queryText runs a query expected to return one text column and joins the
// rows, which is how the engines that store source a line at a time are read.
func queryText(ctx context.Context, db *sql.DB, sep, query string, args ...any) (string, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var s string
		if err := rows.Scan(nullText{&s}); err != nil {
			return "", err
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, sep), rows.Err()
}

// scanNamed scans the current row into a map keyed by lower-cased column
// name. SHOW CREATE answers with a different column count per object kind and
// per server version, so its rows are read by name rather than by position.
func scanNamed(rows *sql.Rows) (map[string]string, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	vals := make([]string, len(cols))
	dest := make([]any, len(cols))
	for i := range vals {
		dest[i] = nullText{&vals[i]}
	}
	if err := rows.Scan(dest...); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(cols))
	for i, c := range cols {
		out[strings.ToLower(c)] = vals[i]
	}
	return out, nil
}

// sortObjects orders a group by schema, name and signature, for the engines
// whose catalogue query cannot sort the assembled rows itself.
func sortObjects(objects []CatalogObject) {
	sort.SliceStable(objects, func(i, j int) bool {
		a, b := objects[i], objects[j]
		if a.Schema != b.Schema {
			return a.Schema < b.Schema
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Signature < b.Signature
	})
}

// --- reading a CREATE TABLE's own text ---------------------------------------
//
// Two engines keep a table's check constraints nowhere but in the statement it
// was created with: SQLite, whose catalogue is that statement, and ClickHouse,
// whose system tables list everything about a table except its constraints.
// Listing them means reading the statement, and only as far as finding where
// each item of its body starts and ends — what an expression says is carried
// over as written.

// quoteCloser returns the index of the character that closes the quoted region
// opened at i, or the end of the text when nothing closes it.
func quoteCloser(text string, i int, closer byte, backslash bool) int {
	for i++; i < len(text); i++ {
		switch {
		case backslash && text[i] == '\\':
			i++
		case text[i] == closer:
			if i+1 < len(text) && text[i+1] == closer {
				i++
				continue
			}
			return i
		}
	}
	return len(text)
}

// commentEnd returns the index of the last character of the comment starting
// at i, or -1 when no comment starts there.
func commentEnd(text string, i int) int {
	switch {
	case strings.HasPrefix(text[i:], "--"):
		if end := strings.IndexByte(text[i:], '\n'); end >= 0 {
			return i + end
		}
		return len(text) - 1
	case strings.HasPrefix(text[i:], "/*"):
		if end := strings.Index(text[i+2:], "*/"); end >= 0 {
			return i + 2 + end + 1
		}
		return len(text) - 1
	}
	return -1
}

// createTableItems splits the body of a CREATE TABLE — the first parenthesised
// list in it — into its comma-separated items: the columns, constraints and
// indexes. A comma inside parentheses, brackets, a quoted region or a comment
// separates nothing.
func createTableItems(driver Driver, createSQL string) []string {
	quotes, backslash := fragmentQuotes(driver), backslashEscapes(driver)
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(createSQL); i++ {
		c := createSQL[i]
		if closer, ok := quotes[c]; ok {
			i = quoteCloser(createSQL, i, closer, backslash)
			continue
		}
		if end := commentEnd(createSQL, i); end >= 0 {
			i = end
			continue
		}
		switch c {
		case '(', '[', '{':
			if depth++; depth == 1 {
				start = i + 1
			}
		case ')', ']', '}':
			if depth--; depth == 0 {
				return append(out, createSQL[start:i])
			}
		case ',':
			if depth == 1 {
				out = append(out, createSQL[start:i])
				start = i + 1
			}
		}
	}
	return out
}

// An itemToken is one top-level piece of a CREATE TABLE item. A parenthesised
// group is a single token, so a keyword inside an expression is never mistaken
// for one of the item's own.
type itemToken struct {
	kind itemTokenKind
	// text is a word as written, a quoted name without its quotes, or the
	// inside of a group.
	text string
	// end is where the token stops in the item, so what follows it can be
	// taken as written.
	end int
}

type itemTokenKind int

const (
	itemWord itemTokenKind = iota
	itemQuotedName
	itemGroup
	itemOther
)

// keyword reports whether the token is the bare word given, in any case.
func (t itemToken) keyword(word string) bool {
	return t.kind == itemWord && strings.EqualFold(t.text, word)
}

func (t itemToken) name() bool { return t.kind == itemWord || t.kind == itemQuotedName }

func createItemTokens(driver Driver, item string) []itemToken {
	quotes, backslash := fragmentQuotes(driver), backslashEscapes(driver)
	var out []itemToken
	for i := 0; i < len(item); i++ {
		c := item[i]
		if closer, ok := quotes[c]; ok {
			end := quoteCloser(item, i, closer, backslash)
			tok := itemToken{kind: itemOther, text: item[i:min(end+1, len(item))], end: min(end+1, len(item))}
			if c != '\'' && end < len(item) {
				tok.kind = itemQuotedName
				tok.text = strings.ReplaceAll(item[i+1:end], string([]byte{closer, closer}), string(closer))
			}
			out = append(out, tok)
			i = end
			continue
		}
		if end := commentEnd(item, i); end >= 0 {
			i = end
			continue
		}
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
		case c == '(':
			depth, end := 0, len(item)
			for j := i; j < len(item); j++ {
				if closer, ok := quotes[item[j]]; ok {
					j = quoteCloser(item, j, closer, backslash)
					continue
				}
				if item[j] == '(' {
					depth++
				} else if item[j] == ')' {
					if depth--; depth == 0 {
						end = j
						break
					}
				}
			}
			out = append(out, itemToken{kind: itemGroup, text: item[min(i+1, end):end], end: min(end+1, len(item))})
			i = end
		case isASCIILetter(c) || isASCIIDigit(c) || c == '_' || c >= 0x80:
			start := i
			for i < len(item) && (isASCIILetter(item[i]) || isASCIIDigit(item[i]) || item[i] == '_' || item[i] == '$' || item[i] >= 0x80) {
				i++
			}
			out = append(out, itemToken{kind: itemWord, text: item[start:i], end: i})
			i--
		default:
			out = append(out, itemToken{kind: itemOther, text: string(c), end: i + 1})
		}
	}
	return out
}
