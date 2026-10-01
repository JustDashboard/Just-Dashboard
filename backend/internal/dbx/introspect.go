package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// Index is one index on a table, with the columns it covers in order.
type Index struct {
	Name string `json:"name"`
	// Columns are the key columns in order. A key part that is an expression
	// rather than a column is its text, and Expression says there is one.
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
	Primary bool     `json:"primary"`
	// Method is the access method or index type: btree, gin, hash, FULLTEXT,
	// CLUSTERED. Empty where the engine has only one.
	Method string `json:"method,omitempty"`
	// Predicate is a partial index's WHERE clause. A unique index with one is
	// not a promise that the column is unique.
	Predicate string `json:"predicate,omitempty"`
	// Include lists the columns stored in the index without being part of its
	// key, which is why they must not be read as key columns.
	Include    []string `json:"include,omitempty"`
	Expression bool     `json:"expression,omitempty"`
	// Definition is the CREATE INDEX statement where the engine reports one or
	// one could be assembled faithfully.
	Definition string `json:"definition,omitempty"`
	Size       int64  `json:"size,omitempty"`
	// Constraint names the primary-key, unique or exclusion constraint this
	// index exists to enforce. Such an index cannot be dropped on its own; the
	// constraint has to be.
	Constraint string `json:"constraint,omitempty"`
	// Invalid marks an index the engine keeps but will not use — a failed
	// CREATE INDEX CONCURRENTLY leaves one behind.
	Invalid bool `json:"invalid,omitempty"`
}

// ForeignKey is one referential constraint. Composite keys keep their columns
// paired with the referenced columns in order, which is what a relationship
// diagram and a "jump to referenced row" action both need.
type ForeignKey struct {
	Name       string   `json:"name"`
	Columns    []string `json:"columns"`
	RefSchema  string   `json:"refSchema,omitempty"`
	RefTable   string   `json:"refTable"`
	RefColumns []string `json:"refColumns"`
	OnUpdate   string   `json:"onUpdate,omitempty"`
	OnDelete   string   `json:"onDelete,omitempty"`
}

// IncomingForeignKey is a foreign key seen from the table it points at: which
// other table holds it, and which of that table's columns reference which of
// this one's. It is what answers "what breaks if I delete this row".
type IncomingForeignKey struct {
	Name       string   `json:"name"`
	Schema     string   `json:"schema,omitempty"`
	Table      string   `json:"table"`
	Columns    []string `json:"columns"`
	RefColumns []string `json:"refColumns"`
	OnUpdate   string   `json:"onUpdate,omitempty"`
	OnDelete   string   `json:"onDelete,omitempty"`
}

// Constraint is a check, unique or exclusion constraint. Primary and foreign
// keys have their own fields on TableDetail and are not repeated here.
type Constraint struct {
	Name string `json:"name"`
	// Type is "check", "unique" or "exclusion".
	Type    string   `json:"type"`
	Columns []string `json:"columns"`
	// Definition is the clause as it would follow the constraint's name in a
	// CREATE TABLE: `CHECK (price >= 0)`, `UNIQUE (email)`.
	Definition string `json:"definition,omitempty"`
}

const (
	ConstraintCheck     = "check"
	ConstraintUnique    = "unique"
	ConstraintExclusion = "exclusion"
)

// TableDetail is everything the Structure tab shows and everything row editing
// needs: the column list, which columns form the primary key (so an edit can be
// scoped to one row), the indexes, the foreign keys in both directions, the
// other constraints, and the DDL that would recreate the table.
type TableDetail struct {
	Schema      string       `json:"schema"`
	Name        string       `json:"name"`
	Columns     []Column     `json:"columns"`
	PrimaryKey  []string     `json:"primaryKey"`
	Indexes     []Index      `json:"indexes"`
	ForeignKeys []ForeignKey `json:"foreignKeys"`
	CreateSQL   string       `json:"createSql,omitempty"`
	// CreateSQLSource says whether CreateSQL is the engine's own text
	// ("engine") or was assembled from the catalogue ("generated").
	CreateSQLSource string `json:"createSqlSource,omitempty"`

	// Type is what the relation is: table, view, materialized view,
	// partitioned table, partition. Empty when the engine was not asked.
	Type    string `json:"type,omitempty"`
	Owner   string `json:"owner,omitempty"`
	Comment string `json:"comment,omitempty"`
	// Rows is the planner's estimate, -1 where there is none.
	Rows      int64 `json:"estimatedRows"`
	Size      int64 `json:"size,omitempty"`
	DataSize  int64 `json:"dataSize,omitempty"`
	IndexSize int64 `json:"indexSize,omitempty"`
	// Constraints are the check, unique and exclusion constraints.
	Constraints []Constraint `json:"constraints"`
	// ReferencedBy are the foreign keys in other tables that point here.
	ReferencedBy []IncomingForeignKey `json:"referencedBy"`
	// Facts are what else the engine says about the table, in display order:
	// a storage engine, a partition key, a collation, row-level security.
	Facts []ObjectFact `json:"facts"`
}

// Detail assembles a TableDetail with one call per fact rather than one giant
// join, because the failure modes differ and because each engine answers them
// from a different catalogue.
//
// Only the column read is fatal. A table whose indexes cannot be listed because
// the login lacks a grant on the catalogue view is still a table worth showing:
// failing the whole request there would turn a partial answer into no answer,
// and the least-privileged accounts are exactly the ones a dashboard should
// stay usable for.
//
// The CreateSQL it returns is the statement a dump can replay ahead of the
// table's rows. Where the engine keeps no text of its own that means a
// generated CREATE TABLE which leaves identity and generated-column clauses
// out on purpose: a replay inserts every column's stored value, and both of
// those refuse an explicit one. DescribeTable is the reader's version.
func Detail(ctx context.Context, db *sql.DB, driver Driver, schema, table string) (*TableDetail, error) {
	return describeTable(ctx, db, driver, schema, table, false)
}

// DescribeTable is Detail as it is shown to a person: the same facts, with the
// generated DDL written the way the table is actually declared — identity,
// generated columns, partitioning and comments included — and a view answered
// with its own definition rather than a CREATE TABLE of its columns.
func DescribeTable(ctx context.Context, db *sql.DB, driver Driver, schema, table string) (*TableDetail, error) {
	return describeTable(ctx, db, driver, schema, table, true)
}

func describeTable(ctx context.Context, db *sql.DB, driver Driver, schema, table string, faithful bool) (*TableDetail, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	cols, err := tableColumns(ctx, db, d, schema, table)
	if err != nil {
		return nil, err
	}
	detail := &TableDetail{
		Schema: schema, Name: table, Columns: cols,
		PrimaryKey: []string{}, Indexes: []Index{}, ForeignKeys: []ForeignKey{},
		Constraints: []Constraint{}, ReferencedBy: []IncomingForeignKey{},
		Facts: []ObjectFact{}, Rows: -1,
	}
	cd, _ := d.(catalogDialect)
	if cd != nil {
		// First, because it names the schema the table was actually found in.
		// A request that names none is answered from the connection's own, and
		// the dialect's reads below take a schema literally: asked for "" they
		// find nothing, which is how a table came back with columns and no key.
		_ = cd.tableFacts(ctx, db, schema, table, detail)
		if detail.Facts == nil {
			detail.Facts = []ObjectFact{}
		}
	}
	if schema == "" {
		schema = detail.Schema
	}
	// reader stands in for the dialect's own reads on an engine where those
	// cannot address the schema that was asked for.
	reader, _ := d.(schemaTableReader)
	primaryKey := d.PrimaryKey
	if reader != nil {
		primaryKey = reader.tablePrimaryKey
	}
	if pk, err := primaryKey(ctx, db, schema, table); err == nil && pk != nil {
		detail.PrimaryKey = pk
	}
	if ix, err := tableIndexes(ctx, db, d, schema, table); err == nil && ix != nil {
		detail.Indexes = ix
	}
	if len(detail.PrimaryKey) == 0 {
		// information_schema hides a constraint from a login that holds nothing
		// but SELECT on the table, so a read-only account saw every Postgres
		// table as keyless. The primary index is visible to anyone.
		for _, ix := range detail.Indexes {
			if ix.Primary && len(ix.Columns) > 0 && !ix.Expression {
				detail.PrimaryKey = append([]string{}, ix.Columns...)
				break
			}
		}
	}
	foreignKeys := d.ForeignKeys
	if reader != nil {
		foreignKeys = reader.tableForeignKeys
	}
	if fks, err := foreignKeys(ctx, db, schema, table); err == nil && fks != nil {
		// A reference with no schema of its own points into the table's.
		detail.ForeignKeys = qualifyReferences(fks, detail.Schema)
	}
	if cd != nil {
		if cs, err := cd.tableConstraints(ctx, db, schema, table); err == nil && cs != nil {
			detail.Constraints = cs
		}
		if in, err := cd.tableReferencedBy(ctx, db, schema, table); err == nil && in != nil {
			detail.ReferencedBy = in
		}
	}
	inPK := map[string]bool{}
	for _, c := range detail.PrimaryKey {
		inPK[c] = true
	}
	for i := range detail.Columns {
		if detail.Columns[i].Key == "" && inPK[detail.Columns[i].Name] {
			detail.Columns[i].Key = "PRI"
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if reader != nil {
		detail.CreateSQL, _ = reader.tableCreateSQL(ctx, db, schema, table)
	} else {
		detail.CreateSQL, _ = d.CreateSQL(ctx, db, schema, table, detail)
	}
	detail.CreateSQLSource = DefinitionFromEngine
	generated := strings.TrimSpace(detail.CreateSQL) != "" &&
		detail.CreateSQL == synthCreateTable(d, schema, table, detail)
	if generated {
		detail.CreateSQLSource = DefinitionGenerated
	}
	if !faithful {
		return detail, nil
	}
	isView := strings.EqualFold(detail.Type, TableTypeView) || strings.EqualFold(detail.Type, TableTypeMaterializedView)
	if cd != nil && (isView && generated || strings.TrimSpace(detail.CreateSQL) == "") {
		// A view has no CREATE TABLE. Where the engine's table read could not
		// produce its text (or produced a table-shaped guess), ask for the
		// view's own definition.
		kind := KindView
		if strings.EqualFold(detail.Type, TableTypeMaterializedView) {
			kind = KindMaterializedView
		}
		if def, err := cd.objectDefinition(ctx, db, ObjectRef{Kind: kind, Schema: schema, Name: table}); err == nil &&
			strings.TrimSpace(def.Definition) != "" {
			detail.CreateSQL = strings.TrimSpace(def.Definition)
			detail.CreateSQLSource = def.Source
			if detail.CreateSQLSource == "" {
				detail.CreateSQLSource = DefinitionFromEngine
			}
			return detail, nil
		}
	}
	if generated {
		detail.CreateSQL = renderCreateTable(d, schema, table, detail, true)
	}
	return detail, nil
}

// PrimaryKeyColumns returns the ordered primary-key columns. Row editing refuses
// to run without one, so this is a security-relevant answer as much as a
// descriptive one: it is what bounds an UPDATE or DELETE to a single row.
func PrimaryKeyColumns(ctx context.Context, db *sql.DB, driver Driver, schema, table string) ([]string, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	return d.PrimaryKey(ctx, db, schema, table)
}

// Relations returns every foreign key in a schema, keyed by TableKey of the
// table that holds it.
//
// It reads them with the bulk catalogue the diagram uses — one query per five
// hundred tables — and asks a table by itself only where that was refused. One
// query per table was three thousand round trips on a schema of three
// thousand tables, for a map most of whose tables have no key to report.
//
// Only relations that can hold a foreign key are asked: a view and a
// materialized view have none, and a partition's are its parent's repeated
// once per partition.
func Relations(ctx context.Context, db *sql.DB, driver Driver, schema string) (map[string][]ForeignKey, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	listed, err := listTables(ctx, db, d, schema)
	if err != nil {
		return nil, err
	}
	tables := make([]Table, 0, len(listed))
	for _, t := range listed {
		if holdsForeignKeys(t.Type) {
			tables = append(tables, t)
		}
	}
	catalog := withSchemaCatalog(ctx, db, d, tables, catalogForeign)
	out := map[string][]ForeignKey{}
	for _, t := range tables {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fks, err := catalog.ForeignKeys(ctx, db, t.Schema, t.Name)
		if err != nil || len(fks) == 0 {
			continue
		}
		out[TableKey(t.Schema, t.Name)] = qualifyReferences(fks, t.Schema)
	}
	return out, nil
}

// qualifyReferences fills in the schema of a reference the engine reported
// without one. SQLite names no schema on a foreign key because the referenced
// table is always in the same file; left empty, the reference would not match
// the key the referenced table is filed under.
func qualifyReferences(fks []ForeignKey, schema string) []ForeignKey {
	for i := range fks {
		if fks[i].RefSchema == "" {
			fks[i].RefSchema = schema
		}
	}
	return fks
}

// synthCreateTable builds a CREATE TABLE from an introspected detail, for the
// engines that keep no canonical DDL text of their own. It is the form a dump
// replays: columns, defaults, keys, constraints and indexes, and deliberately
// not identity or generated-column clauses (see Detail).
func synthCreateTable(d Dialect, schema, table string, detail *TableDetail) string {
	return renderCreateTable(d, schema, table, detail, false)
}

// renderCreateTable writes the statement. faithful adds what a reader expects
// and a replay cannot carry: identity, generated columns, the partition key
// and comments.
//
// It is a description built from the catalogue rather than a byte-exact
// reproduction — types and defaults come back already normalised — so it is
// labelled as generated wherever it is shown.
func renderCreateTable(d Dialect, schema, table string, detail *TableDetail, faithful bool) string {
	rel, err := qualify(d, schema, table)
	if err != nil {
		rel = table
	}
	quote := func(name string) string {
		q, err := d.QuoteIdent(name)
		if err != nil {
			return name
		}
		return q
	}
	quoteAll := func(names []string) string {
		out := make([]string, 0, len(names))
		for _, n := range names {
			out = append(out, quote(n))
		}
		return strings.Join(out, ", ")
	}

	lines := []string{}
	for _, c := range detail.Columns {
		lines = append(lines, "  "+renderColumnDefinition(d.Driver(), quote(c.Name), c, faithful))
	}
	if len(detail.PrimaryKey) > 0 {
		line := "  "
		for _, ix := range detail.Indexes {
			if ix.Primary && ix.Constraint != "" {
				line += "CONSTRAINT " + quote(ix.Constraint) + " "
				break
			}
		}
		lines = append(lines, line+"PRIMARY KEY ("+quoteAll(detail.PrimaryKey)+")")
	}
	for _, c := range detail.Constraints {
		if strings.TrimSpace(c.Definition) == "" {
			continue
		}
		lines = append(lines, "  CONSTRAINT "+quote(c.Name)+" "+c.Definition)
	}
	for _, fk := range detail.ForeignKeys {
		refRel, _ := qualify(d, fk.RefSchema, fk.RefTable)
		line := "  "
		if fk.Name != "" {
			line += "CONSTRAINT " + quote(fk.Name) + " "
		}
		line += fmt.Sprintf("FOREIGN KEY (%s) REFERENCES %s (%s)",
			quoteAll(fk.Columns), refRel, quoteAll(fk.RefColumns))
		if fk.OnDelete != "" && fk.OnDelete != "NO ACTION" {
			line += " ON DELETE " + fk.OnDelete
		}
		if fk.OnUpdate != "" && fk.OnUpdate != "NO ACTION" {
			line += " ON UPDATE " + fk.OnUpdate
		}
		lines = append(lines, line)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "CREATE TABLE %s (\n%s\n)", rel, strings.Join(lines, ",\n"))
	if faithful {
		for _, f := range detail.Facts {
			if f.Name == factPartitionKey {
				b.WriteString(" PARTITION BY " + f.Value)
			}
		}
	}
	b.WriteString(";")

	// An index that enforces a constraint was created by the constraint above;
	// creating it again by name is an error on every engine.
	for _, ix := range detail.Indexes {
		if ix.Primary || ix.Constraint != "" || strings.TrimSpace(ix.Definition) == "" {
			continue
		}
		b.WriteString("\n\n" + strings.TrimRight(strings.TrimSpace(ix.Definition), ";") + ";")
	}
	if faithful {
		for _, stmt := range commentStatements(d, rel, detail) {
			b.WriteString("\n\n" + stmt + ";")
		}
	}
	return b.String()
}

// factPartitionKey is the fact name a dialect files a partition key under, so
// the generated DDL can find it without a field that only one engine fills.
const factPartitionKey = "Partition key"

// renderColumnDefinition writes one column of a generated CREATE TABLE.
func renderColumnDefinition(driver Driver, quoted string, c Column, faithful bool) string {
	if faithful && c.Generated != "" && driver == DriverMSSQL {
		// A computed column in SQL Server has no declared type at all.
		line := quoted + " AS " + c.Generated
		if c.GeneratedKind == "stored" {
			line += " PERSISTED"
		}
		return line
	}
	line := quoted + " " + c.Type
	if faithful {
		switch {
		case c.Generated != "" && driver == DriverOracle:
			line += " GENERATED ALWAYS AS (" + c.Generated + ") VIRTUAL"
		case c.Generated != "":
			line += " GENERATED ALWAYS AS (" + c.Generated + ") " + strings.ToUpper(c.GeneratedKind)
		case c.Identity != "" && driver == DriverMSSQL:
			line += " " + strings.ToUpper(c.Identity)
		case c.Identity != "":
			line += " GENERATED " + strings.ToUpper(c.Identity) + " AS IDENTITY"
		}
	}
	if !c.Nullable {
		line += " NOT NULL"
	}
	if c.Default != "" {
		line += " DEFAULT " + c.Default
	}
	return strings.TrimRight(line, " ")
}

// commentStatements renders the table's and columns' comments for the engines
// that set them with a statement of their own. SQL Server keeps them as
// extended properties behind a stored procedure, which is not DDL a reader
// expects under a CREATE TABLE, so it gets none.
func commentStatements(d Dialect, rel string, detail *TableDetail) []string {
	if d.Driver() != DriverPostgres && d.Driver() != DriverOracle {
		return nil
	}
	out := []string{}
	literal := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	if detail.Comment != "" {
		out = append(out, "COMMENT ON TABLE "+rel+" IS "+literal(detail.Comment))
	}
	for _, c := range detail.Columns {
		if c.Comment == "" {
			continue
		}
		q, err := d.QuoteIdent(c.Name)
		if err != nil {
			continue
		}
		out = append(out, "COMMENT ON COLUMN "+rel+"."+q+" IS "+literal(c.Comment))
	}
	return out
}

// SchemaOutline is the whole schema flattened to names, which is what the SQL
// editor's completion needs and all it needs.
//
// It is one payload rather than a completion endpoint the editor calls per
// keystroke: a round trip per character is unusable over the VPN tunnel these
// dashboards are reached through, and a schema's shape does not change between
// keystrokes. The cost is a handful of catalogue queries on open, which is why
// the result is small — names only, no types, no constraints.
type SchemaOutline struct {
	Schema string `json:"schema"`
	// Tables maps TableKey to the column names in order.
	Tables map[string][]string `json:"tables"`
	// Entries names every key in Tables by its parts, so a caller never has to
	// take a key apart to learn which schema a table is in.
	Entries []OutlineEntry `json:"entries"`
	// Truncated says the schema held more relations than Limit and Total how
	// many it holds.
	Truncated bool `json:"truncated"`
	Total     int  `json:"total"`
	Limit     int  `json:"limit"`
}

// OutlineEntry is one relation in the outline.
type OutlineEntry struct {
	ID     string `json:"id"`
	Schema string `json:"schema"`
	Name   string `json:"name"`
	Type   string `json:"type"`
}

// The outline's bound is far above the graph's: it is names only, read in
// batches of five hundred, and an editor that cannot complete a table because
// it was the 121st would be worse than one that takes a second longer to open.
const (
	DefaultOutlineTables = 5000
	MaxOutlineTables     = 20000
)

func Outline(ctx context.Context, db *sql.DB, driver Driver, schema string) (*SchemaOutline, error) {
	return OutlineWithLimit(ctx, db, driver, schema, DefaultOutlineTables)
}

// OutlineWithLimit is Outline with the caller's bound on how many relations
// it will describe.
func OutlineWithLimit(ctx context.Context, db *sql.DB, driver Driver, schema string, limit int) (*SchemaOutline, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = DefaultOutlineTables
	}
	limit = min(limit, MaxOutlineTables)
	tables, err := listTables(ctx, db, d, schema)
	if err != nil {
		return nil, err
	}
	out := &SchemaOutline{
		Schema: schema, Tables: map[string][]string{}, Entries: []OutlineEntry{},
		Total: len(tables), Limit: limit,
	}
	if len(tables) > limit {
		tables, out.Truncated = tables[:limit], true
	}
	catalog := withSchemaCatalog(ctx, db, d, tables, catalogColumns)
	for _, t := range tables {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key := TableKey(t.Schema, t.Name)
		out.Entries = append(out.Entries, OutlineEntry{ID: key, Schema: t.Schema, Name: t.Name, Type: t.Type})
		cols, err := catalog.Columns(ctx, db, t.Schema, t.Name)
		if err != nil {
			// A table whose columns cannot be read still belongs in the
			// completion list by name; dropping it would make the editor claim
			// it does not exist.
			out.Tables[key] = []string{}
			continue
		}
		names := make([]string, 0, len(cols))
		for _, c := range cols {
			names = append(names, c.Name)
		}
		out.Tables[key] = names
	}
	return out, nil
}

// SchemaGraph is a whole schema in the shape a diagram needs: every table with
// its columns, which of them are keys, and the references between them.
//
// It exists because drawing the schema from the routes that already existed
// meant one request for the table list, one for the relations, and then one
// per table for the columns — forty round trips to render one picture, each
// one a chance for the diagram to be half-drawn. Introspecting once on the
// server and sending the finished shape is both faster and atomic: what
// arrives is one schema at one moment rather than forty answers from forty.
type SchemaGraph struct {
	Schema string       `json:"schema"`
	Tables []GraphTable `json:"tables"`
	Edges  []GraphEdge  `json:"edges"`
	// Truncated says the schema was larger than the cap and only part of it is
	// here. A diagram of nine hundred tables is not a diagram, but silently
	// drawing a third of one is worse than saying so — and saying so means
	// saying how many there are (Total) and how many were asked for (Limit).
	Truncated bool `json:"truncated"`
	Total     int  `json:"total"`
	Limit     int  `json:"limit"`
}

type GraphTable struct {
	// ID is TableKey(Schema, Name): the node id, and what every edge and every
	// saved position refers to. The bare name was the id until two schemas had
	// a table of the same name and one node took the other's place.
	ID      string        `json:"id"`
	Schema  string        `json:"schema"`
	Name    string        `json:"name"`
	Type    string        `json:"type"`
	Rows    int64         `json:"rows"`
	Comment string        `json:"comment,omitempty"`
	Columns []GraphColumn `json:"columns"`
}

type GraphColumn struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Nullable   bool   `json:"nullable"`
	PrimaryKey bool   `json:"primaryKey"`
	// ForeignKey names the table this column points at, so a column can be
	// drawn as a reference without the caller re-deriving it from the edges;
	// ForeignKeyID is that table's node id.
	ForeignKey   string `json:"foreignKey,omitempty"`
	ForeignKeyID string `json:"foreignKeyId,omitempty"`
	Unique       bool   `json:"unique,omitempty"`
}

// GraphEdge is one foreign key, anchored to the columns at both ends so the
// line can be drawn between the rows it actually relates rather than between
// two boxes.
type GraphEdge struct {
	Name string `json:"name"`
	// From and To are node ids. FromTable and ToTable are the bare names kept
	// for labels; an edge is never looked up by them.
	From       string `json:"from"`
	To         string `json:"to"`
	FromSchema string `json:"fromSchema,omitempty"`
	FromTable  string `json:"fromTable"`
	FromColumn string `json:"fromColumn"`
	ToSchema   string `json:"toSchema,omitempty"`
	ToTable    string `json:"toTable"`
	ToColumn   string `json:"toColumn"`
	OnDelete   string `json:"onDelete,omitempty"`
	OnUpdate   string `json:"onUpdate,omitempty"`
	// Cardinality is "many-to-one" unless the referencing column is itself
	// unique, which makes it one-to-one — the distinction a reader of the
	// diagram is actually looking for.
	Cardinality string `json:"cardinality"`
}

// The graph's default bound keeps the picture a picture. A caller that knows
// what it is asking for may raise it; past the maximum the reply is megabytes
// of boxes no canvas lays out.
const (
	DefaultGraphTables = 120
	MaxGraphTables     = 1000
)

func BuildSchemaGraph(ctx context.Context, db *sql.DB, driver Driver, schema string) (*SchemaGraph, error) {
	return BuildSchemaGraphWithLimit(ctx, db, driver, schema, DefaultGraphTables)
}

// BuildSchemaGraphWithLimit is BuildSchemaGraph with the caller's bound.
func BuildSchemaGraphWithLimit(ctx context.Context, db *sql.DB, driver Driver, schema string, limit int) (*SchemaGraph, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = DefaultGraphTables
	}
	limit = min(limit, MaxGraphTables)
	listed, err := listTables(ctx, db, d, schema)
	if err != nil {
		return nil, err
	}
	tables := make([]Table, 0, len(listed))
	for _, t := range listed {
		if drawable(t.Type) {
			tables = append(tables, t)
		}
	}
	out := &SchemaGraph{Schema: schema, Tables: []GraphTable{}, Edges: []GraphEdge{}, Total: len(tables), Limit: limit}
	if len(tables) > limit {
		// What is cut should be what the picture can best do without: a view
		// has no relations to draw, so tables keep their place ahead of them.
		sort.SliceStable(tables, func(i, j int) bool {
			return holdsForeignKeys(tables[i].Type) && !holdsForeignKeys(tables[j].Type)
		})
		tables, out.Truncated = tables[:limit], true
	}
	catalog := withSchemaCatalog(ctx, db, d, tables, catalogEverything)

	// Which columns are unique decides whether a reference is one-to-one or
	// many-to-one, and it is read from the indexes that are being fetched
	// anyway rather than from a second pass.
	uniqueCols := map[string]map[string]bool{}

	for _, t := range tables {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id := TableKey(t.Schema, t.Name)
		gt := GraphTable{ID: id, Schema: t.Schema, Name: t.Name, Type: t.Type, Rows: t.Rows, Comment: t.Comment, Columns: []GraphColumn{}}
		cols, err := catalog.Columns(ctx, db, t.Schema, t.Name)
		if err != nil {
			// A table whose columns will not read is still part of the shape:
			// dropping it would silently delete a box other tables point at.
			out.Tables = append(out.Tables, gt)
			continue
		}
		pk, _ := catalog.PrimaryKey(ctx, db, t.Schema, t.Name)
		isPK := map[string]bool{}
		for _, c := range pk {
			isPK[c] = true
		}
		uniq := map[string]bool{}
		if indexes, err := catalog.Indexes(ctx, db, t.Schema, t.Name); err == nil {
			for _, ix := range indexes {
				// A partial unique index promises uniqueness only among the
				// rows it covers, and one over an expression promises nothing
				// about the column inside it.
				if ix.Unique && len(ix.Columns) == 1 && ix.Predicate == "" && !ix.Expression {
					uniq[ix.Columns[0]] = true
				}
			}
		}
		uniqueCols[id] = uniq

		fkOf, fkIDOf := map[string]string{}, map[string]string{}
		if holdsForeignKeys(t.Type) {
			if fks, err := catalog.ForeignKeys(ctx, db, t.Schema, t.Name); err == nil {
				for _, fk := range qualifyReferences(fks, t.Schema) {
					to := TableKey(fk.RefSchema, fk.RefTable)
					for i, col := range fk.Columns {
						refCol := ""
						if i < len(fk.RefColumns) {
							refCol = fk.RefColumns[i]
						}
						fkOf[col], fkIDOf[col] = fk.RefTable, to
						out.Edges = append(out.Edges, GraphEdge{
							Name: fk.Name, From: id, To: to,
							FromSchema: t.Schema, FromTable: t.Name, FromColumn: col,
							ToSchema: fk.RefSchema, ToTable: fk.RefTable, ToColumn: refCol,
							OnDelete: fk.OnDelete, OnUpdate: fk.OnUpdate,
						})
					}
				}
			}
		}
		for _, c := range cols {
			gt.Columns = append(gt.Columns, GraphColumn{
				Name: c.Name, Type: c.Type, Nullable: c.Nullable,
				PrimaryKey: isPK[c.Name], ForeignKey: fkOf[c.Name], ForeignKeyID: fkIDOf[c.Name],
				Unique: uniq[c.Name],
			})
		}
		out.Tables = append(out.Tables, gt)
	}

	// Cardinality is decided after every table is known, because it depends on
	// the referencing column's uniqueness rather than the referenced one's.
	for i, e := range out.Edges {
		if uniqueCols[e.From][e.FromColumn] {
			out.Edges[i].Cardinality = "one-to-one"
		} else {
			out.Edges[i].Cardinality = "many-to-one"
		}
	}
	return out, nil
}
