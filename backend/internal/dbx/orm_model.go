package dbx

import (
	"fmt"
	"sort"
	"strings"
)

// The schema as a generator reads it.
//
// Table, TableDetail and Column are shaped for the Structure tab: what to draw.
// A generator needs facts those do not carry — that a column's type is an enum
// and which one, that its default is a sequence rather than an expression, that
// a unique index is partial and so proves nothing about the column — and it
// needs tables told apart by schema, which a map keyed by bare name cannot do.
// So generation has its own structure, built from the same introspection and
// then filled in by orm_catalog.go where a live server can say more.

// ORMSchema is one generation's input.
type ORMSchema struct {
	Driver Driver
	// Flavor is the product behind the driver where the difference changes the
	// output: "cockroachdb" behind postgres, "mariadb" behind mysql. Empty
	// means the driver's own product.
	Flavor string
	Tables []ORMTable
	// Enums are the named enum types the engine keeps as objects of their own
	// (PostgreSQL). An engine that spells an enum inside the column type
	// (MySQL, ClickHouse) has none here; its values are read from the type.
	Enums []ORMEnum
	// Detailed is true once a live catalogue has said which columns are
	// auto-incrementing. Without it, an integer primary key with no default is
	// assumed to be one, which is what it nearly always is.
	Detailed bool
	// Warnings are what introspection could not read, carried into the result.
	Warnings []string
}

// ORMTableKind is what a relation is, as far as a model is concerned.
type ORMTableKind string

const (
	ORMKindTable       ORMTableKind = "table"
	ORMKindView        ORMTableKind = "view"
	ORMKindMatView     ORMTableKind = "materialized view"
	ORMKindPartitioned ORMTableKind = "partitioned table"
	// ORMKindPartition is one partition of a partitioned table. It holds a
	// slice of its parent's rows and is never a model of its own.
	ORMKindPartition ORMTableKind = "partition"
)

type ORMTable struct {
	Schema      string
	Name        string
	Kind        ORMTableKind
	Comment     string
	Columns     []ORMColumn
	PrimaryKey  []string
	Indexes     []ORMIndex
	ForeignKeys []ForeignKey
	Checks      []ORMCheck
	// CreateSQL is the engine's own statement where it keeps one (MySQL,
	// SQLite, ClickHouse, Oracle with DBMS_METADATA), or a view's definition.
	CreateSQL string
	// PartitionKey is PostgreSQL's "RANGE (created_at)" for a partitioned table.
	PartitionKey string
}

type ORMColumn struct {
	Name     string
	Type     string
	Nullable bool
	Default  string
	Comment  string
	// AutoIncrement is true for a column the engine numbers itself: serial,
	// identity, AUTO_INCREMENT, SQLite's INTEGER PRIMARY KEY.
	AutoIncrement bool
	// Identity is "always" or "by default" for an SQL-standard identity column.
	Identity string
	// Generated marks a computed column. It is read-only, and its expression
	// is Default's sibling rather than a default.
	Generated     bool
	GeneratedExpr string
	// DefaultExpr is true when the catalogue says the default is an expression
	// rather than a literal. MySQL prints both the same way and tells them
	// apart only in a separate column.
	DefaultExpr bool
	// OnUpdateNow is MySQL's ON UPDATE CURRENT_TIMESTAMP.
	OnUpdateNow bool
	// EnumSchema and EnumName name the column's enum type where the engine has
	// named enum types. The type string alone cannot: PostgreSQL prints an
	// enum's name exactly as it prints any other user-defined type's.
	EnumSchema string
	EnumName   string
}

type ORMIndex struct {
	Name    string
	Columns []string
	Unique  bool
	Primary bool
	// Desc is per column, parallel to Columns; nil means all ascending.
	Desc []bool
	// Method is the access method where it is not the engine's default b-tree:
	// "gin", "hash", "fulltext".
	Method string
	// Partial and Expression mark an index that cannot stand for a constraint
	// on its columns: it covers some rows, or something computed from them.
	Partial    bool
	Expression bool
	// Definition is the engine's own CREATE INDEX text, where it has one.
	Definition string
}

type ORMCheck struct {
	Name       string
	Definition string
}

type ORMEnum struct {
	Schema string
	Name   string
	Values []string
}

// ormKindOf reads the type word a table listing reported. The dialects do not
// agree on it — PostgreSQL's listing passes an unmapped relkind through as a
// letter — so every spelling met in practice is named here.
func ormKindOf(listed string) (kind ORMTableKind, model bool) {
	switch strings.ToLower(strings.TrimSpace(listed)) {
	case "view", "v", "system view":
		return ORMKindView, true
	case "materialized view", "m", "matview":
		return ORMKindMatView, true
	case "p", "partitioned table", "partitioned":
		return ORMKindPartitioned, true
	case "partition":
		return ORMKindPartition, true
	case "sequence":
		// MariaDB lists a sequence among its tables. It has no columns a model
		// could describe.
		return ORMKindTable, false
	}
	return ORMKindTable, true
}

// NewORMSchema builds the generator's structure from what ListTables and Detail
// return. Details are looked up by "schema.name" first and by bare name second:
// the bare form is what older callers pass, and it is only trusted when the
// detail says it belongs to the same schema, so two schemas' same-named tables
// no longer borrow each other's columns.
func NewORMSchema(driver Driver, tables []Table, details map[string]*TableDetail) *ORMSchema {
	s := &ORMSchema{Driver: driver}
	for _, t := range tables {
		d := details[t.Schema+"."+t.Name]
		if d == nil {
			d = details[t.Name]
			if d != nil && d.Schema != "" && t.Schema != "" && d.Schema != t.Schema {
				d = nil
			}
		}
		if d == nil {
			continue
		}
		s.AddTable(t, d)
	}
	return s
}

// AddTable appends one introspected table. A relation that is not a table or a
// view at all — a sequence — is left out.
func (s *ORMSchema) AddTable(t Table, d *TableDetail) {
	kind, model := ormKindOf(t.Type)
	if !model || d == nil {
		return
	}
	ot := ORMTable{
		Schema: t.Schema, Name: t.Name, Kind: kind, Comment: t.Comment,
		PrimaryKey: append([]string(nil), d.PrimaryKey...),
		CreateSQL:  d.CreateSQL,
	}
	for _, c := range d.Columns {
		ot.Columns = append(ot.Columns, ORMColumn{
			Name: c.Name, Type: c.Type, Nullable: c.Nullable, Default: c.Default,
		})
	}
	for _, ix := range d.Indexes {
		ot.Indexes = append(ot.Indexes, ORMIndex{
			Name: ix.Name, Columns: append([]string(nil), ix.Columns...),
			Unique: ix.Unique, Primary: ix.Primary,
		})
	}
	ot.ForeignKeys = append(ot.ForeignKeys, d.ForeignKeys...)
	s.Tables = append(s.Tables, ot)
}

// --- the prepared model ---------------------------------------------------

// ormTable is a table selected for output, with everything the generators
// would otherwise each work out for themselves.
type ormTable struct {
	*ORMTable
	cols  []*ormCol
	byCol map[string]*ormCol
	pk    map[string]bool
	// uniques are the usable unique indexes other than the primary key, single
	// column and composite alike. indexes are the usable plain ones.
	uniques []ORMIndex
	indexes []ORMIndex
	skipped []ORMIndex
	rels    []*ormRel // foreign keys leaving this table, to selected tables
	back    []*ormRel // foreign keys arriving at it
	view    bool
}

type ormCol struct {
	*ORMColumn
	t   ormType
	def ormDefault
}

// ormRel is one foreign key between two selected tables.
type ormRel struct {
	from, to *ormTable
	fk       ForeignKey
	// name is the relation's stable name, "<child>_<columns>", used wherever a
	// target needs both ends of a relation to agree on what it is called.
	name string
	// optional is true when every key column is nullable; unique when the key
	// columns are themselves a key of the child, which makes it one-to-one.
	optional bool
	unique   bool
	self     bool
}

// ormGen is one generation in progress: the selected tables, the enums they
// use, the options, and the warnings collected along the way.
type ormGen struct {
	schema   *ORMSchema
	driver   Driver
	flavor   string
	opts     ORMOptions
	models   []*ormTable
	enums    []*ormEnum
	warnings []string
	warned   map[string]bool
	// refusal is set by a generator that finds, once it sees the schema, that
	// its target cannot represent it at all.
	refusal string
	// multiSchema is true when the models live in more than one schema, which
	// is when a name alone stops identifying a table.
	multiSchema   bool
	defaultSchema string
	dialect       Dialect
}

// ormEnum is an enum as emitted: a named type, or one read out of a column's
// type and named after the column it belongs to.
type ormEnum struct {
	Schema string
	Name   string
	Values []string
	// inline is true when the engine has no enum object and the values live in
	// the column type.
	inline bool
}

func (g *ormGen) warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if g.warned[msg] {
		return
	}
	g.warned[msg] = true
	g.warnings = append(g.warnings, msg)
}

func (g *ormGen) refuse(format string, args ...any) {
	g.refusal = fmt.Sprintf(format, args...)
}

func newORMGen(s *ORMSchema, o ORMOptions) *ormGen {
	g := &ormGen{
		schema: s, driver: s.Driver, flavor: s.Flavor, opts: o,
		warned: map[string]bool{},
	}
	if d, err := DialectFor(s.Driver); err == nil {
		g.dialect = d
		g.defaultSchema = d.DefaultSchema()
	}
	for _, w := range s.Warnings {
		g.warn("%s", w)
	}
	g.prepare()
	return g
}

func ormTableKey(schema, name string) string { return schema + "\x00" + name }

// label names a table in a sentence, qualified only when that tells the reader
// something.
func (g *ormGen) label(t *ormTable) string {
	if g.multiSchema && t.Schema != "" {
		return t.Schema + "." + t.Name
	}
	return t.Name
}

func (g *ormGen) prepare() {
	named := map[string]*ORMEnum{}
	for i := range g.schema.Enums {
		e := &g.schema.Enums[i]
		named[ormTableKey(e.Schema, e.Name)] = e
	}
	usedEnums := map[string]*ormEnum{}
	partitions := 0

	byKey := map[string]*ormTable{}
	inScope := map[string]bool{}
	for i := range g.schema.Tables {
		t := &g.schema.Tables[i]
		inScope[ormTableKey(t.Schema, t.Name)] = true
		switch t.Kind {
		case ORMKindPartition:
			partitions++
			continue
		case ORMKindView, ORMKindMatView:
			if !g.opts.Views {
				continue
			}
		}
		if len(t.Columns) == 0 {
			g.warn("%s has no columns that could be read and was left out.", t.Name)
			continue
		}
		m := &ormTable{
			ORMTable: t, byCol: map[string]*ormCol{}, pk: map[string]bool{},
			view: t.Kind == ORMKindView || t.Kind == ORMKindMatView,
		}
		for _, c := range t.PrimaryKey {
			m.pk[c] = true
		}
		for j := range t.Columns {
			c := &ormCol{ORMColumn: &t.Columns[j]}
			if m.pk[c.Name] {
				// SQLite reports an INTEGER PRIMARY KEY as nullable because the
				// declaration carried no NOT NULL. No key column holds a NULL.
				c.Nullable = false
			}
			m.cols = append(m.cols, c)
			m.byCol[c.Name] = c
		}
		g.models = append(g.models, m)
		byKey[ormTableKey(t.Schema, t.Name)] = m
	}
	switch {
	case partitions == 1:
		g.warn("One partition was left out: a partition holds a slice of its parent table's rows, and the parent is the model.")
	case partitions > 1:
		g.warn("%d partitions were left out: a partition holds a slice of its parent table's rows, and the parent is the model.", partitions)
	}

	schemas := map[string]bool{}
	for _, m := range g.models {
		schemas[m.Schema] = true
	}
	g.multiSchema = len(schemas) > 1

	for _, m := range g.models {
		if m.Kind == ORMKindPartitioned {
			g.warn("%s is a partitioned table. It is emitted as an ordinary model; its partitioning is not something this target can describe.", g.label(m))
		}
		for _, c := range m.cols {
			g.resolveColumn(m, c, named, usedEnums)
		}
		g.resolveIndexes(m)
	}
	g.resolveImplicitRefs(byKey)
	g.resolveRelations(byKey, inScope)

	for _, e := range usedEnums {
		g.enums = append(g.enums, e)
	}
	sort.Slice(g.enums, func(i, j int) bool {
		if g.enums[i].Schema != g.enums[j].Schema {
			return g.enums[i].Schema < g.enums[j].Schema
		}
		return g.enums[i].Name < g.enums[j].Name
	})
}

// resolveColumn parses a column's type and default for the source engine and
// ties it to its enum, if it has one.
func (g *ormGen) resolveColumn(m *ormTable, c *ormCol, named map[string]*ORMEnum, used map[string]*ormEnum) {
	c.t = parseORMType(g.driver, c.Type)

	var enum *ormEnum
	switch {
	case c.EnumName != "":
		key := ormTableKey(c.EnumSchema, c.EnumName)
		if e := named[key]; e != nil {
			if enum = used[key]; enum == nil {
				enum = &ormEnum{Schema: e.Schema, Name: e.Name, Values: e.Values}
			}
			c.t.Kind, c.t.Name = ormEnumKind, e.Name
		}
	case c.t.Kind == ormEnumKind && len(c.t.Values) > 0:
		// MySQL and ClickHouse: the values are in the type, and the enum is
		// named after the column that owns it, as Prisma names them.
		enum = &ormEnum{Schema: m.Schema, Name: m.Name + "_" + c.Name, Values: c.t.Values, inline: true}
	case c.t.Kind == ormUnknown && g.driver == DriverPostgres:
		// Without the catalogue's word for it, a type that carries an enum's
		// name is taken to be that enum. Schema-qualified spellings win over
		// bare ones, and a bare one is looked for in the table's own schema
		// before anywhere else.
		if e := ormEnumByTypeName(g.schema.Enums, m.Schema, c.t.Name); e != nil {
			key := ormTableKey(e.Schema, e.Name)
			if enum = used[key]; enum == nil {
				enum = &ormEnum{Schema: e.Schema, Name: e.Name, Values: e.Values}
			}
			c.t.Kind, c.t.Name = ormEnumKind, e.Name
		}
	}
	if enum != nil {
		if g.opts.Enums || !ormTargetHasOption(g.opts.Target, "enums") {
			c.t.Enum = enum
			c.t.Values = enum.Values
			used[ormTableKey(enum.Schema, enum.Name)] = enum
		} else {
			// Asked not to emit enums: the column is what its values are, text.
			c.t = ormType{Kind: ormText, Name: "text", Raw: c.t.Raw, Array: c.t.Array}
		}
	}

	if g.driver == DriverSQLite && len(m.PrimaryKey) == 1 && m.pk[c.Name] &&
		strings.EqualFold(strings.TrimSpace(c.Type), "integer") {
		// An INTEGER PRIMARY KEY is SQLite's rowid under another name: the
		// engine assigns it whether or not AUTOINCREMENT was written.
		c.AutoIncrement = true
		if strings.Contains(strings.ToUpper(m.CreateSQL), "AUTOINCREMENT") {
			// The keyword changes one thing — a deleted row's id is never
			// reused — and the targets that can say so need to know.
			c.Identity = "autoincrement"
		}
	}
	if c.t.Serial {
		c.AutoIncrement = true
	}
	c.def = parseORMDefault(g.driver, g.flavor, c)
	if c.def.Kind == ormDefAuto {
		c.AutoIncrement = true
	}
	if c.AutoIncrement {
		c.def = ormDefault{Kind: ormDefAuto, Raw: c.Default}
	}
	if !g.schema.Detailed && c.def.Kind == ormDefNone && len(m.PrimaryKey) == 1 && m.pk[c.Name] &&
		c.t.isInteger() && !m.view && (g.driver == DriverMySQL || g.driver == DriverMSSQL) {
		// These two engines keep "auto-increment" outside the default, so with
		// no catalogue detail it cannot be seen. See ORMSchema.Detailed.
		c.def = ormDefault{Kind: ormDefAssumedAuto}
	}
}

func ormTargetHasOption(t ORMTarget, id string) bool {
	spec, ok := ormTargetSpecFor(t)
	if !ok {
		return false
	}
	_, has := spec.option(id)
	return has
}

// ormEnumByTypeName finds the enum a type name refers to: "status",
// "analytics.status" or the quoted forms format_type prints. A bare name is
// looked for in the table's own schema before any other.
func ormEnumByTypeName(enums []ORMEnum, tableSchema, typeName string) *ORMEnum {
	parts := ormSplitQualified(strings.TrimSpace(typeName))
	schema, name := "", parts[len(parts)-1]
	if len(parts) == 2 {
		schema = parts[0]
	}
	if name == "" || len(parts) > 2 {
		return nil
	}
	var elsewhere *ORMEnum
	for i := range enums {
		e := &enums[i]
		switch {
		case e.Name != name:
		case schema != "" && e.Schema == schema, schema == "" && e.Schema == tableSchema:
			return e
		case schema == "" && elsewhere == nil:
			elsewhere = e
		}
	}
	return elsewhere
}

// ormSplitQualified splits `schema.name` or `"Schema"."Name"` into its parts,
// honouring double quotes so a dot inside a quoted name is not a separator.
func ormSplitQualified(s string) []string {
	var parts []string
	var b strings.Builder
	quoted := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '"':
			if quoted && i+1 < len(s) && s[i+1] == '"' {
				b.WriteByte('"')
				i++
				continue
			}
			quoted = !quoted
		case ch == '.' && !quoted:
			parts = append(parts, b.String())
			b.Reset()
		default:
			b.WriteByte(ch)
		}
	}
	return append(parts, b.String())
}

// resolveIndexes sorts a table's indexes into the ones a target can state as a
// fact about columns and the ones it cannot.
func (g *ormGen) resolveIndexes(m *ormTable) {
	if g.driver == DriverClickHouse {
		// ClickHouse's sorting key and data-skipping indices order and prune
		// parts. Neither is a constraint, and no target here has a word for
		// them; the SQL target prints the engine's own statement instead.
		return
	}
	for _, ix := range m.Indexes {
		// The primary key's own index says nothing the key does not. Oracle
		// lists it as an ordinary unique index, so it is recognised by what it
		// covers as well as by its flag.
		if ix.Primary || (ix.Unique && !ix.Partial && !ix.Expression && sameColumnList(ix.Columns, m.PrimaryKey)) {
			continue
		}
		usable := !ix.Partial && !ix.Expression && len(ix.Columns) > 0
		for _, c := range ix.Columns {
			if m.byCol[c] == nil {
				usable = false
			}
		}
		switch strings.ToLower(ix.Method) {
		case "fulltext", "spatial":
			usable = false
		}
		if !usable {
			m.skipped = append(m.skipped, ix)
			continue
		}
		if ix.Unique {
			m.uniques = append(m.uniques, ix)
		} else {
			m.indexes = append(m.indexes, ix)
		}
	}
}

// warnSkippedIndexes reports the indexes a target left out. An index over an
// expression or part of a table is real and does its job in the database; it
// only has no spelling as "these columns are indexed" or "this column is
// unique", and claiming either would be wrong.
func (g *ormGen) warnSkippedIndexes() {
	for _, m := range g.models {
		for _, ix := range m.skipped {
			why := "is not a plain index on columns"
			switch {
			case ix.Partial:
				why = "covers only some rows"
			case ix.Expression:
				why = "is over an expression"
			case strings.EqualFold(ix.Method, "fulltext"):
				why = "is a full-text index"
			case strings.EqualFold(ix.Method, "spatial"):
				why = "is a spatial index"
			}
			g.warn("Index %s on %s %s and was left out; it still exists in the database.", ix.Name, g.label(m), why)
		}
	}
}

// resolveImplicitRefs fills in the referenced columns SQLite leaves out when a
// key points at its parent's primary key (`REFERENCES users`), so every target
// reads a foreign key that names both of its ends.
func (g *ormGen) resolveImplicitRefs(byKey map[string]*ormTable) {
	for _, m := range g.models {
		for i := range m.ForeignKeys {
			fk := &m.ForeignKeys[i]
			if len(fk.RefColumns) == 0 || fk.RefColumns[0] != "" {
				continue
			}
			refSchema := fk.RefSchema
			if refSchema == "" {
				refSchema = m.Schema
			}
			if to := byKey[ormTableKey(refSchema, fk.RefTable)]; to != nil && len(to.PrimaryKey) == len(fk.Columns) {
				fk.RefColumns = append([]string(nil), to.PrimaryKey...)
			}
		}
	}
}

// plainIndexes are a table's indexes for a target that can only say "these
// columns are indexed". One built on another access method is real, but
// declaring it as the b-tree it is not would have a schema sync rebuild it, so
// it is reported and left out.
func (g *ormGen) plainIndexes(m *ormTable) []ORMIndex {
	var out []ORMIndex
	for _, ix := range m.indexes {
		switch method := strings.ToLower(ix.Method); method {
		case "", "btree":
			out = append(out, ix)
		default:
			g.warn("Index %s on %s uses the %s access method, which this target does not describe; it was left out.", ix.Name, g.label(m), method)
		}
	}
	return out
}

// ormAction normalises a referential action to SQL's own words, or "" when the
// catalogue did not say.
func ormAction(rule string) string {
	switch a := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(rule), "_", " ")); a {
	case "NO ACTION", "RESTRICT", "CASCADE", "SET NULL", "SET DEFAULT":
		return a
	}
	return ""
}

func (g *ormGen) resolveRelations(byKey map[string]*ormTable, inScope map[string]bool) {
	// A target with no notion of a relation has no use for them, and no reason
	// to report the ones it would have left out.
	if !g.opts.Relations || !ormTargetHasOption(g.opts.Target, "relations") {
		return
	}
	for _, m := range g.models {
		if m.view {
			continue
		}
		for _, fk := range m.ForeignKeys {
			if fk.RefTable == "" || len(fk.Columns) == 0 || len(fk.Columns) != len(fk.RefColumns) {
				continue
			}
			refSchema := fk.RefSchema
			if refSchema == "" {
				refSchema = m.Schema
			}
			to := byKey[ormTableKey(refSchema, fk.RefTable)]
			if to == nil {
				// A key into a table that was read but not selected is the
				// caller's own choice. One into a schema that was never read is
				// worth saying, because nothing on the page shows it.
				if !inScope[ormTableKey(refSchema, fk.RefTable)] && !g.opts.Selected {
					g.warn("%s.%s references %s.%s, which is outside the selected schemas; the relation was left out.",
						g.label(m), strings.Join(fk.Columns, ", "), refSchema, fk.RefTable)
				}
				continue
			}
			ok := true
			optional := true
			for _, c := range fk.Columns {
				col := m.byCol[c]
				if col == nil {
					ok = false
					break
				}
				if !col.Nullable {
					optional = false
				}
			}
			for _, c := range fk.RefColumns {
				if to.byCol[c] == nil {
					ok = false
				}
			}
			if !ok {
				continue
			}
			r := &ormRel{
				from: m, to: to, fk: fk, optional: optional, self: m == to,
				name:   m.Name + "_" + strings.Join(fk.Columns, "_"),
				unique: m.isKey(fk.Columns),
			}
			m.rels = append(m.rels, r)
			to.back = append(to.back, r)
		}
	}
}

// isKey reports whether a set of columns identifies one row: it is the primary
// key, or exactly the columns of a usable unique index.
func (m *ormTable) isKey(cols []string) bool {
	if ormSameColumns(cols, m.PrimaryKey) {
		return true
	}
	for _, ix := range m.uniques {
		if ormSameColumns(cols, ix.Columns) {
			return true
		}
	}
	return false
}

// sameColumnList reports whether two column lists are the same columns in the
// same order.
func sameColumnList(a, b []string) bool {
	if len(a) == 0 || len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func ormSameColumns(a, b []string) bool {
	if len(a) == 0 || len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, c := range a {
		seen[c] = true
	}
	for _, c := range b {
		if !seen[c] {
			return false
		}
	}
	return true
}

// uniqueOn returns the single-column unique index on a column, if there is one.
func (m *ormTable) uniqueOn(col string) (ORMIndex, bool) {
	for _, ix := range m.uniques {
		if len(ix.Columns) == 1 && ix.Columns[0] == col {
			return ix, true
		}
	}
	return ORMIndex{}, false
}

// compositeUniques are the unique indexes over more than one column.
func (m *ormTable) compositeUniques() []ORMIndex {
	var out []ORMIndex
	for _, ix := range m.uniques {
		if len(ix.Columns) > 1 {
			out = append(out, ix)
		}
	}
	return out
}

// singlePK returns the lone primary-key column, or nil for a composite or
// missing key.
func (m *ormTable) singlePK() *ormCol {
	if len(m.PrimaryKey) != 1 {
		return nil
	}
	return m.byCol[m.PrimaryKey[0]]
}

// dbFills reports whether the database supplies a column's value when an
// insert leaves it out — the question every "insert shape" asks.
func (c *ormCol) dbFills() bool {
	return c.Generated || c.AutoIncrement || c.def.Kind != ormDefNone
}

// qualified reports whether a table's schema has to be written for the output
// to find it: always when models span schemas, and on the engines whose
// connection lands in a default schema when the table is not in that one.
func (g *ormGen) qualified(t *ormTable) bool {
	if t.Schema == "" || g.driver == DriverSQLite {
		return false
	}
	if g.multiSchema {
		return true
	}
	switch g.driver {
	case DriverPostgres, DriverMSSQL:
		return g.defaultSchema != "" && t.Schema != g.defaultSchema
	}
	return false
}

// nothingToGenerate says why a generation has no model to write. An empty file
// with a header on it would look like an answer; the reason is the answer.
func (g *ormGen) nothingToGenerate() string {
	views, unreadable := 0, 0
	for i := range g.schema.Tables {
		switch t := &g.schema.Tables[i]; {
		case t.Kind == ORMKindView || t.Kind == ORMKindMatView:
			views++
		case t.Kind != ORMKindPartition && len(t.Columns) == 0:
			unreadable++
		}
	}
	switch {
	case views > 0 && !g.opts.Views && ormTargetHasOption(g.opts.Target, "views"):
		return fmt.Sprintf("there are no tables here to generate from, only %d view(s); turn on views to include them", views)
	case views > 0 && !g.opts.Views:
		return fmt.Sprintf("there are no tables here to generate from, only %d view(s), which this target does not model", views)
	case unreadable > 0:
		return fmt.Sprintf("none of the %d table(s) here could be read", unreadable)
	}
	return "there are no tables here to generate from"
}

func (g *ormGen) counts() ORMCounts {
	c := ORMCounts{Enums: len(g.enums)}
	for _, m := range g.models {
		if m.view {
			c.Views++
		} else {
			c.Tables++
		}
		c.Relations += len(m.rels)
	}
	return c
}

// schemaNames lists the schemas the models live in, sorted.
func (g *ormGen) schemaNames() []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range g.models {
		if m.Schema != "" && !seen[m.Schema] {
			seen[m.Schema] = true
			out = append(out, m.Schema)
		}
	}
	for _, e := range g.enums {
		if e.Schema != "" && !e.inline && !seen[e.Schema] {
			seen[e.Schema] = true
			out = append(out, e.Schema)
		}
	}
	sort.Strings(out)
	return out
}

// --- selection ------------------------------------------------------------

// ormSelect keeps the items a caller named. A name is either "schema.table" or
// a bare table name, which matches in every schema that was read: a dot inside
// a table's own name is still found, because the bare form is tried too. A
// name that matches nothing is an error rather than a quietly shorter result —
// the caller asked for a table and is owed either it or a reason.
func ormSelect[T any](items []T, key func(T) (schema, name string), names []string) ([]T, error) {
	if len(names) == 0 {
		return items, nil
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = false
	}
	var out []T
	for _, it := range items {
		schema, name := key(it)
		_, bare := want[name]
		_, qualified := want[schema+"."+name]
		if bare {
			want[name] = true
		}
		if qualified {
			want[schema+"."+name] = true
		}
		if bare || qualified {
			out = append(out, it)
		}
	}
	for _, n := range names {
		if !want[n] {
			return nil, ormRequestErrorf("table %q is not in the selected schemas", n)
		}
	}
	return out, nil
}
