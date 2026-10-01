package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// Reading a live database into an ORMSchema.
//
// The table list and each table's columns, keys and indexes come from the
// same calls the rest of the dashboard makes (ListTables, Detail), so a
// generated file never disagrees with the Structure tab about what a table
// is. What those calls do not carry — and a generator cannot do without — is
// read here, per engine: which type is an enum and what its labels are, which
// column is an identity or computed, which unique index is partial and so
// proves nothing, which relation is a partition of another.
//
// Everything in this file beyond the table list is best effort. A catalogue
// view the login may not read costs the output a detail and adds a sentence to
// the warnings; it never fails the generation.

// ORMScope says what a generation reads: the schemas to list, and optionally
// the tables to keep. No schema means every schema the connection lists.
type ORMScope struct {
	Schemas []string
	Tables  []string
	// Statements asks for each table's own CREATE statement where the engine
	// keeps one. Only the SQL target prints it, and on Oracle fetching it
	// (DBMS_METADATA) can take seconds a table, so nothing else pays for it.
	Statements bool
	// SkipViews leaves the views listed but unread, for a generation that will
	// not emit them: a view costs the same catalogue queries a table does.
	SkipViews bool
}

// Scope turns a request's schema and table selection into what the loader
// reads. database is the connection's own database, which is what "no schema
// named" means on the engines where a schema is a database: one connection
// string reaches one of them, and a schema file for every database on the
// server at once describes nothing a client could open.
func (r ORMRequest) Scope(driver Driver, database string) (ORMScope, error) {
	scope := ORMScope{}
	add := func(dst *[]string, value, what string, limit int) error {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil
		}
		if len(value) > limit {
			return ormRequestErrorf("%s name is too long", what)
		}
		for _, r := range value {
			if r < 0x20 || r == 0x7f {
				return ormRequestErrorf("%s name contains a control character", what)
			}
		}
		*dst = append(*dst, value)
		return nil
	}
	if err := add(&scope.Schemas, r.Schema, "schema", 128); err != nil {
		return scope, err
	}
	for _, s := range r.Schemas {
		if err := add(&scope.Schemas, s, "schema", 128); err != nil {
			return scope, err
		}
	}
	for _, t := range r.Tables {
		// A table may be given as schema.table: two names and a dot.
		if err := add(&scope.Tables, t, "table", 257); err != nil {
			return scope, err
		}
	}
	if len(scope.Schemas) > 64 {
		return scope, ormRequestErrorf("at most 64 schemas can be read at once")
	}
	if len(scope.Tables) > ormMaxTables {
		return scope, ormRequestErrorf("at most %d tables can be selected at once", ormMaxTables)
	}
	if len(scope.Schemas) == 0 && database != "" && (driver == DriverMySQL || driver == DriverClickHouse) {
		scope.Schemas = []string{database}
	}
	scope.Statements = r.Target == ORMSQL
	// Whether views are written is the target's default unless the request
	// says; a request the target refuses is refused where its options are read.
	if opts, err := r.Options(); err == nil {
		scope.SkipViews = !opts.Views
	}
	return scope, nil
}

// ormMaxTables bounds one generation. Each table costs several catalogue
// queries; past this many the honest answer is "pick some", not a request that
// runs into its deadline and returns half a schema. It counts what is read:
// a partition is not, and neither is a view the target will not emit.
const ormMaxTables = 1000

type ormLoader struct {
	ctx    context.Context
	db     *sql.DB
	driver Driver
	schema *ORMSchema
	// pgVersion is PostgreSQL's server_version_num, 0 where it was not read.
	pgVersion int
	// failed is set when the facts a generator leans on (which columns the
	// engine numbers itself) could not be read.
	failed bool
}

func (l *ormLoader) warn(format string, args ...any) {
	l.schema.Warnings = append(l.schema.Warnings, fmt.Sprintf(format, args...))
}

// each runs a best-effort catalogue query, handing every row to scan.
func (l *ormLoader) each(what, query string, args []any, scan func(*sql.Rows) error) bool {
	rows, err := l.db.QueryContext(l.ctx, query, args...)
	if err != nil {
		l.warn("The catalogue could not be read for %s (%v); the output is missing that detail.", what, err)
		return false
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			l.warn("The catalogue could not be read for %s (%v); the output is missing that detail.", what, err)
			return false
		}
	}
	if err := rows.Err(); err != nil {
		l.warn("The catalogue could not be read for %s (%v); the output is missing that detail.", what, err)
		return false
	}
	return true
}

// LoadORMSchema reads the tables in scope and everything a generator needs to
// know about them.
func LoadORMSchema(ctx context.Context, db *sql.DB, driver Driver, scope ORMScope) (*ORMSchema, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	l := &ormLoader{ctx: ctx, db: db, driver: driver, schema: &ORMSchema{Driver: driver}}

	schemas := []string{}
	seenSchema := map[string]bool{}
	for _, s := range scope.Schemas {
		if s = strings.TrimSpace(s); s != "" && !seenSchema[s] {
			seenSchema[s] = true
			schemas = append(schemas, s)
		}
	}
	if len(schemas) == 0 {
		schemas = []string{""}
	}
	var tables []Table
	seen := map[string]bool{}
	for _, schema := range schemas {
		listed, err := d.Tables(ctx, db, schema)
		if err != nil {
			return nil, err
		}
		for _, t := range listed {
			if key := ormTableKey(t.Schema, t.Name); !seen[key] {
				seen[key] = true
				tables = append(tables, t)
			}
		}
	}
	tables, err = ormSelect(tables, func(t Table) (string, string) { return t.Schema, t.Name }, scope.Tables)
	if err != nil {
		return nil, err
	}

	// The schemas the tables are actually in, which is what the per-engine
	// catalogue queries are asked about.
	var present []string
	seenSchema = map[string]bool{}
	for _, t := range tables {
		if !seenSchema[t.Schema] {
			seenSchema[t.Schema] = true
			present = append(present, t.Schema)
		}
	}
	// Which relations are partitions has to be known before the listing is
	// measured: a table partitioned by day lists a thousand relations within
	// three years and is still one model, and its partitions share its schema,
	// so "narrow it by schema" would be no way out.
	facts := newORMFacts()
	l.relationFacts(facts, present)
	read, unread := ormReadPlan(tables, facts.partition, scope.SkipViews)
	l.schema.Tables = append(l.schema.Tables, unread...)
	if len(read) > ormMaxTables {
		return nil, ormRequestErrorf("%d tables is more than one generation reads (%d); narrow it by schema or by table", len(read), ormMaxTables)
	}
	l.columnFacts(facts, present)

	done := 0
	for _, t := range read {
		detail, err := l.detail(d, t, scope.Statements)
		if ctx.Err() != nil {
			return nil, fmt.Errorf("introspection ran out of time after %d of %d tables; choose fewer tables or one schema", done, len(read))
		}
		if err != nil {
			l.warn("%s could not be read (%v) and was left out.", t.Name, err)
			continue
		}
		before := len(l.schema.Tables)
		l.schema.AddTable(t, detail)
		if len(l.schema.Tables) > before {
			facts.apply(&l.schema.Tables[len(l.schema.Tables)-1])
		}
		done++
	}
	l.schema.Enums = facts.enums
	l.schema.Flavor = facts.flavor
	l.schema.Detailed = !l.failed
	return l.schema, nil
}

// ormReadPlan splits a listing into the relations whose columns will be read
// and the ones that are only counted: a partition, whose columns are its
// parent's, and — when the generation will not emit them — a view. The second
// kind is kept as a name and a kind, so the generator can still report the
// partitions it left out and answer "nothing but views here".
func ormReadPlan(tables []Table, partition map[string]bool, skipViews bool) (read []Table, unread []ORMTable) {
	for _, t := range tables {
		kind, model := ormKindOf(t.Type)
		switch {
		case partition[ormTableKey(t.Schema, t.Name)]:
			unread = append(unread, ORMTable{Schema: t.Schema, Name: t.Name, Kind: ORMKindPartition})
		case !model:
		case skipViews && (kind == ORMKindView || kind == ORMKindMatView):
			unread = append(unread, ORMTable{Schema: t.Schema, Name: t.Name, Kind: kind})
		default:
			read = append(read, t)
		}
	}
	return read, unread
}

// detail reads one table the way Detail does — the same dialect calls, with the
// same rule that only the column read is fatal — and differs in two things: a
// part that could not be read is said rather than passed over, and the CREATE
// statement is fetched only when something will print it. SQLite's is always
// read: it is one row of sqlite_master, and it is the only place the
// AUTOINCREMENT keyword is recorded.
func (l *ormLoader) detail(d Dialect, t Table, statements bool) (*TableDetail, error) {
	cols, err := d.Columns(l.ctx, l.db, t.Schema, t.Name)
	if err != nil {
		return nil, err
	}
	detail := &TableDetail{
		Schema: t.Schema, Name: t.Name, Columns: cols,
		PrimaryKey: []string{}, Indexes: []Index{}, ForeignKeys: []ForeignKey{},
	}
	if pk, err := d.PrimaryKey(l.ctx, l.db, t.Schema, t.Name); err == nil {
		detail.PrimaryKey = pk
	} else {
		l.warn("The primary key of %s could not be read (%v); it is treated as having none.", t.Name, err)
	}
	if ix, err := d.Indexes(l.ctx, l.db, t.Schema, t.Name); err == nil {
		detail.Indexes = ix
	} else {
		l.warn("The indexes of %s could not be read (%v); unique constraints and indexes are missing for it.", t.Name, err)
	}
	if fks, err := d.ForeignKeys(l.ctx, l.db, t.Schema, t.Name); err == nil {
		detail.ForeignKeys = fks
	} else {
		l.warn("The foreign keys of %s could not be read (%v); its relations are missing.", t.Name, err)
	}
	if statements || l.driver == DriverSQLite {
		detail.CreateSQL, _ = d.CreateSQL(l.ctx, l.db, t.Schema, t.Name, detail)
	}
	return detail, nil
}

// --- per-engine facts -----------------------------------------------------

type ormColumnFact struct {
	typ           string
	enumSchema    string
	enumName      string
	identity      string
	auto          bool
	generated     bool
	generatedExpr string
	defaultExpr   bool
	// hasDefault says def replaces the column's default, even with "".
	hasDefault  bool
	def         string
	onUpdateNow bool
	comment     string
	// nullable is only read when the column itself has to be built from the
	// fact, because the shared introspection listed none for its table.
	nullable bool
}

type ormIndexFact struct {
	unique, primary     bool
	partial, expression bool
	method              string
	definition          string
	keyColumns          int // 0 = all listed columns are key columns
	desc                []bool
	included            map[string]bool
}

type ormFacts struct {
	flavor    string
	enums     []ORMEnum
	partition map[string]bool
	kind      map[string]ORMTableKind
	viewSQL   map[string]string
	partKey   map[string]string
	// comment replaces a table's comment where the catalogue was asked for it
	// directly; nil means it was not.
	comment map[string]*string
	columns map[string]map[string]ormColumnFact
	// columnOrder is each table's columns in catalogue order.
	columnOrder map[string][]string
	indexes     map[string]map[string]ormIndexFact
	// indexOrder keeps the indexes the catalogue listed, so one the shared
	// introspection missed can still be added in a stable place.
	indexOrder map[string][]string
	checks     map[string][]ORMCheck
}

func newORMFacts() *ormFacts {
	return &ormFacts{
		partition: map[string]bool{}, kind: map[string]ORMTableKind{}, viewSQL: map[string]string{},
		comment: map[string]*string{},
		partKey: map[string]string{}, columns: map[string]map[string]ormColumnFact{},
		columnOrder: map[string][]string{},
		indexes:     map[string]map[string]ormIndexFact{}, indexOrder: map[string][]string{},
		checks: map[string][]ORMCheck{},
	}
}

// relationFacts reads what is true of a relation as a whole, one row each:
// cheap enough to ask of a listing of any size, and needed before anything else
// because it says which relations are not read at all.
func (l *ormLoader) relationFacts(f *ormFacts, schemas []string) {
	if l.driver == DriverPostgres {
		l.postgresRelations(f, schemas)
	}
}

// columnFacts reads the per-column and per-index detail, once the listing is
// known to be of a size worth reading.
func (l *ormLoader) columnFacts(f *ormFacts, schemas []string) {
	switch l.driver {
	case DriverPostgres:
		l.postgresFacts(f, schemas)
	case DriverMySQL:
		l.mysqlFacts(f, schemas)
	case DriverSQLite:
		l.sqliteFacts(f)
	case DriverMSSQL:
		l.mssqlFacts(f, schemas)
	case DriverOracle:
		l.oracleFacts(f, schemas)
	}
}

func (f *ormFacts) setColumn(table, name string, fact ormColumnFact) {
	if f.columns[table] == nil {
		f.columns[table] = map[string]ormColumnFact{}
	}
	if _, seen := f.columns[table][name]; !seen {
		f.columnOrder[table] = append(f.columnOrder[table], name)
	}
	f.columns[table][name] = fact
}

func (f *ormFacts) setIndex(table, name string, fact ormIndexFact) {
	if f.indexes[table] == nil {
		f.indexes[table] = map[string]ormIndexFact{}
	}
	if _, seen := f.indexes[table][name]; !seen {
		f.indexOrder[table] = append(f.indexOrder[table], name)
	}
	f.indexes[table][name] = fact
}

// apply lays the engine's facts over a table built from the shared
// introspection.
func (f *ormFacts) apply(t *ORMTable) {
	key := ormTableKey(t.Schema, t.Name)
	if kind, ok := f.kind[key]; ok {
		t.Kind = kind
	}
	if def := f.viewSQL[key]; def != "" {
		t.CreateSQL = def
	}
	if comment := f.comment[key]; comment != nil {
		t.Comment = *comment
	}
	t.PartitionKey = f.partKey[key]
	t.Checks = f.checks[key]

	if cols := f.columns[key]; cols != nil {
		if len(t.Columns) == 0 {
			// information_schema has no columns for a materialized view, so
			// the shared introspection finds none; the catalogue does.
			for _, name := range f.columnOrder[key] {
				t.Columns = append(t.Columns, ORMColumn{Name: name, Nullable: cols[name].nullable})
			}
		}
		for i := range t.Columns {
			c := &t.Columns[i]
			fact, ok := cols[c.Name]
			if !ok {
				continue
			}
			if fact.typ != "" {
				c.Type = fact.typ
			}
			c.EnumSchema, c.EnumName = fact.enumSchema, fact.enumName
			c.Identity = fact.identity
			c.AutoIncrement = c.AutoIncrement || fact.auto || fact.identity != ""
			c.Generated, c.GeneratedExpr = fact.generated, fact.generatedExpr
			c.DefaultExpr = fact.defaultExpr
			c.OnUpdateNow = fact.onUpdateNow
			c.Comment = fact.comment
			if fact.hasDefault {
				c.Default = fact.def
			}
			if c.Generated {
				c.Default = ""
			}
		}
	}

	facts := f.indexes[key]
	if facts == nil {
		return
	}
	known := map[string]bool{}
	for i := range t.Indexes {
		ix := &t.Indexes[i]
		known[ix.Name] = true
		fact, ok := facts[ix.Name]
		if !ok {
			continue
		}
		ix.Partial, ix.Expression = fact.partial, fact.expression
		ix.Method, ix.Definition = fact.method, fact.definition
		if len(fact.included) > 0 {
			kept := ix.Columns[:0]
			for _, col := range ix.Columns {
				if !fact.included[col] {
					kept = append(kept, col)
				}
			}
			ix.Columns = kept
		}
		if fact.keyColumns > 0 && fact.keyColumns < len(ix.Columns) {
			// The rest are INCLUDE columns: carried by the index, not part of
			// what it orders or makes unique.
			ix.Columns = ix.Columns[:fact.keyColumns]
		}
		if len(fact.desc) >= len(ix.Columns) {
			ix.Desc = fact.desc[:len(ix.Columns)]
		}
	}
	// An index over nothing but expressions has no column for the shared
	// introspection to list, so it lists no index at all. It is added here,
	// marked as what it is, so it is reported instead of silently missing.
	for _, name := range f.indexOrder[key] {
		if fact := facts[name]; !known[name] && fact.expression {
			t.Indexes = append(t.Indexes, ORMIndex{
				Name: name, Columns: []string{}, Unique: fact.unique, Primary: fact.primary,
				Partial: fact.partial, Expression: true, Method: fact.method, Definition: fact.definition,
			})
		}
	}
}

// --- PostgreSQL -----------------------------------------------------------

// postgresCatalog is the handful of catalogue columns younger than a
// PostgreSQL that is still met on old servers, each with what stands in for it
// where it does not exist yet. One missing column fails its whole query, and
// with it every enum, array, domain and identity column in the schema, so the
// query is written for the server that will run it.
type postgresCatalog struct {
	partition  string // pg_class.relispartition, 10
	partKey    string // pg_get_partkeydef, 10
	identity   string // pg_attribute.attidentity, 10
	generated  string // pg_attribute.attgenerated, 12
	keyColumns string // pg_index.indnkeyatts, 11: before INCLUDE every column is a key column
}

// postgresCatalogFor takes server_version_num; 0 means it could not be read
// and is taken for a current server.
func postgresCatalogFor(version int) postgresCatalog {
	c := postgresCatalog{
		partition: "c.relispartition", partKey: "pg_get_partkeydef(c.oid)",
		identity: "a.attidentity::text", generated: "a.attgenerated::text",
		keyColumns: "ix.indnkeyatts",
	}
	if version == 0 {
		return c
	}
	if version < 120000 {
		c.generated = "''::text"
	}
	if version < 110000 {
		c.keyColumns = "ix.indnatts"
	}
	if version < 100000 {
		c.partition, c.partKey, c.identity = "false", "''::text", "''::text"
	}
	return c
}

func (l *ormLoader) postgresRelations(f *ormFacts, schemas []string) {
	var version string
	if err := l.db.QueryRowContext(l.ctx, `SELECT version()`).Scan(&version); err == nil &&
		strings.Contains(version, "CockroachDB") {
		f.flavor = "cockroachdb"
	}
	// Scanned as text: an integer everywhere, but not worth failing over.
	var num string
	if err := l.db.QueryRowContext(l.ctx, `SHOW server_version_num`).Scan(&num); err == nil {
		l.pgVersion, _ = strconv.Atoi(strings.TrimSpace(num))
	}
	cat := postgresCatalogFor(l.pgVersion)

	for _, schema := range schemas {
		// Partitions, the definitions PostgreSQL will print for a view or a
		// partitioned table, and the table's comment asked for by catalogue:
		// the one-argument obj_description the table listing uses can answer
		// with another object's comment that happens to share the OID, which
		// on CockroachDB it does for every table that has none of its own.
		l.each("partitions, view definitions and comments in "+schema, `
		  SELECT c.relname, c.relkind::text, COALESCE(`+cat.partition+`, false),
		         CASE WHEN c.relkind = 'p' THEN COALESCE(`+cat.partKey+`, '') ELSE '' END,
		         CASE WHEN c.relkind IN ('v','m') THEN COALESCE(pg_get_viewdef(c.oid), '') ELSE '' END,
		         COALESCE(obj_description(c.oid, 'pg_class'), '')
		  FROM pg_class c
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		  WHERE n.nspname = $1 AND c.relkind IN ('r','p','v','m','f')`, []any{schema}, func(rows *sql.Rows) error {
			var name, relkind, partKey, viewDef, comment string
			var partition bool
			if err := rows.Scan(&name, &relkind, &partition, &partKey, &viewDef, &comment); err != nil {
				return err
			}
			key := ormTableKey(schema, name)
			f.comment[key] = &comment
			switch {
			case partition:
				f.partition[key] = true
			case relkind == "p":
				f.kind[key] = ORMKindPartitioned
				f.partKey[key] = partKey
			case relkind == "m":
				f.kind[key] = ORMKindMatView
			}
			if viewDef != "" {
				f.viewSQL[key] = viewDef
			}
			return nil
		})
	}
}

func (l *ormLoader) postgresFacts(f *ormFacts, schemas []string) {
	cat := postgresCatalogFor(l.pgVersion)

	// Enum types, whichever schema they live in: a column may use one from
	// outside the schemas being read.
	byName := map[string]int{}
	l.each("enum types", `
	  SELECT n.nspname, t.typname, e.enumlabel
	  FROM pg_type t
	  JOIN pg_namespace n ON n.oid = t.typnamespace
	  JOIN pg_enum e ON e.enumtypid = t.oid
	  ORDER BY n.nspname, t.typname, e.enumsortorder`, nil, func(rows *sql.Rows) error {
		var schema, name, label string
		if err := rows.Scan(&schema, &name, &label); err != nil {
			return err
		}
		key := ormTableKey(schema, name)
		i, ok := byName[key]
		if !ok {
			i = len(f.enums)
			byName[key] = i
			f.enums = append(f.enums, ORMEnum{Schema: schema, Name: name})
		}
		f.enums[i].Values = append(f.enums[i].Values, label)
		return nil
	})

	for _, schema := range schemas {
		// A column's type as PostgreSQL writes it, with a domain resolved to
		// what it is a domain over, and the enum named when it is one.
		// information_schema says only "USER-DEFINED" and "ARRAY" for these.
		l.each("column types in "+schema, `
		  SELECT c.relname, a.attname,
		         CASE WHEN el.typtype = 'd'
		              THEN format_type(el.typbasetype, el.typtypmod) ||
		                   CASE WHEN t.typcategory = 'A' THEN '[]' ELSE '' END
		              ELSE format_type(a.atttypid, a.atttypmod) END,
		         b.typtype = 'e', bn.nspname, b.typname,
		         `+cat.identity+`, `+cat.generated+`,
		         COALESCE(pg_get_expr(d.adbin, d.adrelid), ''),
		         COALESCE(col_description(c.oid, a.attnum), ''),
		         NOT a.attnotnull
		  FROM pg_attribute a
		  JOIN pg_class c ON c.oid = a.attrelid
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		  JOIN pg_type t ON t.oid = a.atttypid
		  JOIN pg_type el ON el.oid = CASE WHEN t.typcategory = 'A' THEN t.typelem ELSE t.oid END
		  JOIN pg_type b ON b.oid = CASE WHEN el.typtype = 'd' THEN el.typbasetype ELSE el.oid END
		  JOIN pg_namespace bn ON bn.oid = b.typnamespace
		  LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		  WHERE n.nspname = $1 AND a.attnum > 0 AND NOT a.attisdropped
		    AND c.relkind IN ('r','p','v','m','f')
		  ORDER BY c.relname, a.attnum`, []any{schema}, func(rows *sql.Rows) error {
			var table, column, typ, typeSchema, typeName, identity, generated, expr, comment string
			var enum, nullable bool
			if err := rows.Scan(&table, &column, &typ, &enum, &typeSchema, &typeName,
				&identity, &generated, &expr, &comment, &nullable); err != nil {
				return err
			}
			fact := ormColumnFact{typ: typ, comment: comment, hasDefault: true, def: expr, nullable: nullable}
			if enum {
				fact.enumSchema, fact.enumName = typeSchema, typeName
			}
			switch identity {
			case "a":
				fact.identity = "always"
			case "d":
				fact.identity = "by default"
			}
			if generated != "" {
				fact.generated, fact.generatedExpr, fact.def = true, expr, ""
			}
			f.setColumn(ormTableKey(schema, table), column, fact)
			return nil
		})

		// What an index really is. The shared introspection lists an index's
		// plain columns and whether it is unique; whether it is partial, over
		// an expression, on another access method or descending is here.
		l.each("indexes in "+schema, `
		  SELECT t.relname, i.relname, ix.indisunique, ix.indisprimary,
		         ix.indpred IS NOT NULL, ix.indexprs IS NOT NULL, am.amname,
		         `+cat.keyColumns+`, ix.indoption::text, pg_get_indexdef(ix.indexrelid)
		  FROM pg_index ix
		  JOIN pg_class i ON i.oid = ix.indexrelid
		  JOIN pg_class t ON t.oid = ix.indrelid
		  JOIN pg_namespace n ON n.oid = t.relnamespace
		  JOIN pg_am am ON am.oid = i.relam
		  WHERE n.nspname = $1
		  ORDER BY t.relname, i.relname`, []any{schema}, func(rows *sql.Rows) error {
			var table, name, method, options, definition string
			var fact ormIndexFact
			if err := rows.Scan(&table, &name, &fact.unique, &fact.primary, &fact.partial, &fact.expression,
				&method, &fact.keyColumns, &options, &definition); err != nil {
				return err
			}
			// CockroachDB calls its ordinary index "prefix" and its GIN "inverted".
			switch method {
			case "btree", "prefix":
			case "inverted":
				fact.method = "gin"
			default:
				fact.method = method
			}
			fact.definition = definition
			// indoption is one small integer per key column; bit 0 is DESC.
			for _, opt := range strings.Fields(options) {
				n, _ := strconv.Atoi(opt)
				fact.desc = append(fact.desc, n&1 == 1)
			}
			f.setIndex(ormTableKey(schema, table), name, fact)
			return nil
		})

		l.each("check constraints in "+schema, `
		  SELECT c.relname, con.conname, pg_get_constraintdef(con.oid)
		  FROM pg_constraint con
		  JOIN pg_class c ON c.oid = con.conrelid
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		  WHERE n.nspname = $1 AND con.contype = 'c'
		  ORDER BY c.relname, con.conname`, []any{schema}, func(rows *sql.Rows) error {
			var table, name, definition string
			if err := rows.Scan(&table, &name, &definition); err != nil {
				return err
			}
			key := ormTableKey(schema, table)
			f.checks[key] = append(f.checks[key], ORMCheck{Name: name, Definition: definition})
			return nil
		})
	}
}

// --- MySQL and MariaDB ------------------------------------------------------

func (l *ormLoader) mysqlFacts(f *ormFacts, schemas []string) {
	var version string
	if err := l.db.QueryRowContext(l.ctx, `SELECT VERSION()`).Scan(&version); err == nil &&
		strings.Contains(strings.ToLower(version), "mariadb") {
		f.flavor = "mariadb"
	}
	for _, schema := range schemas {
		// EXTRA is where MySQL keeps what the column list does not: that a
		// column numbers itself, that its default is an expression and not a
		// literal, that it is touched on every update, that it is computed.
		ok := l.each("auto-increment and generated columns in "+schema, `
		  SELECT TABLE_NAME, COLUMN_NAME, COALESCE(EXTRA, ''), COALESCE(COLUMN_COMMENT, ''),
		         COALESCE(GENERATION_EXPRESSION, '')
		  FROM information_schema.COLUMNS
		  WHERE TABLE_SCHEMA = ?`, []any{schema}, func(rows *sql.Rows) error {
			var table, column, extra, comment, expr string
			if err := rows.Scan(&table, &column, &extra, &comment, &expr); err != nil {
				return err
			}
			lower := strings.ToLower(extra)
			f.setColumn(ormTableKey(schema, table), column, ormColumnFact{
				auto:          strings.Contains(lower, "auto_increment"),
				defaultExpr:   strings.Contains(lower, "default_generated"),
				onUpdateNow:   strings.Contains(lower, "on update current_timestamp"),
				generated:     strings.Contains(lower, "generated") && !strings.Contains(lower, "default_generated"),
				generatedExpr: expr,
				comment:       comment,
			})
			return nil
		})
		if !ok {
			l.failed = true
		}

		l.each("index types in "+schema, `
		  SELECT TABLE_NAME, INDEX_NAME, INDEX_TYPE, MAX(CASE WHEN SUB_PART IS NULL THEN 0 ELSE 1 END)
		  FROM information_schema.STATISTICS
		  WHERE TABLE_SCHEMA = ?
		  GROUP BY TABLE_NAME, INDEX_NAME, INDEX_TYPE`, []any{schema}, func(rows *sql.Rows) error {
			var table, name, kind string
			var prefix int
			if err := rows.Scan(&table, &name, &kind, &prefix); err != nil {
				return err
			}
			fact := ormIndexFact{}
			switch strings.ToUpper(kind) {
			case "FULLTEXT":
				fact.method = "fulltext"
			case "SPATIAL":
				fact.method = "spatial"
			}
			// An index on the first N characters of a column is not an index
			// on the column, and a unique one does not make the column unique.
			fact.expression = prefix == 1
			f.setIndex(ormTableKey(schema, table), name, fact)
			return nil
		})

		l.each("view definitions in "+schema, `
		  SELECT TABLE_NAME, COALESCE(VIEW_DEFINITION, '')
		  FROM information_schema.VIEWS
		  WHERE TABLE_SCHEMA = ?`, []any{schema}, func(rows *sql.Rows) error {
			var name, definition string
			if err := rows.Scan(&name, &definition); err != nil {
				return err
			}
			if q, err := quoteBacktick(name); err == nil && definition != "" {
				f.viewSQL[ormTableKey(schema, name)] = "CREATE VIEW " + q + " AS " + definition
			}
			return nil
		})
	}
}

// --- SQLite ---------------------------------------------------------------

func (l *ormLoader) sqliteFacts(f *ormFacts) {
	var tables []string
	l.each("tables", `SELECT name FROM sqlite_master WHERE type = 'table'`, nil, func(rows *sql.Rows) error {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		tables = append(tables, name)
		return nil
	})
	for _, table := range tables {
		key := ormTableKey("main", table)
		list, err := pragmaRows(l.ctx, l.db, "index_list", table)
		if err != nil {
			continue
		}
		type entry struct {
			name    string
			partial bool
		}
		var entries []entry
		for list.Next() {
			var seq, unique, partial int
			var name, origin string
			if err := list.Scan(&seq, &name, &unique, &origin, &partial); err == nil {
				entries = append(entries, entry{name: name, partial: partial == 1})
			}
		}
		list.Close()
		for _, e := range entries {
			fact := ormIndexFact{partial: e.partial}
			// A key column with no table column behind it (cid -2) is an
			// expression.
			if info, err := pragmaRows(l.ctx, l.db, "index_info", e.name); err == nil {
				for info.Next() {
					var seqno, cid int
					var name sql.NullString
					if err := info.Scan(&seqno, &cid, &name); err == nil && cid == -2 {
						fact.expression = true
					}
				}
				info.Close()
			}
			f.setIndex(key, e.name, fact)
		}
	}
}

// --- SQL Server -----------------------------------------------------------

func (l *ormLoader) mssqlFacts(f *ormFacts, schemas []string) {
	for _, schema := range schemas {
		ok := l.each("identity and computed columns in "+schema, `
		  SELECT o.name, c.name, c.is_identity, c.is_computed,
		         ISNULL(cc.definition, ''), ISNULL(CAST(ep.value AS NVARCHAR(4000)), '')
		  FROM sys.columns c
		  JOIN sys.objects o ON o.object_id = c.object_id
		  JOIN sys.schemas s ON s.schema_id = o.schema_id
		  LEFT JOIN sys.computed_columns cc ON cc.object_id = c.object_id AND cc.column_id = c.column_id
		  LEFT JOIN sys.extended_properties ep
		         ON ep.class = 1 AND ep.major_id = c.object_id AND ep.minor_id = c.column_id
		        AND ep.name = 'MS_Description'
		  WHERE s.name = @p1 AND o.type IN ('U','V')`, []any{schema}, func(rows *sql.Rows) error {
			var table, column, expr, comment string
			var identity, computed bool
			if err := rows.Scan(&table, &column, &identity, &computed, &expr, &comment); err != nil {
				return err
			}
			f.setColumn(ormTableKey(schema, table), column, ormColumnFact{
				auto: identity, generated: computed, generatedExpr: expr, comment: comment,
			})
			return nil
		})
		if !ok {
			l.failed = true
		}

		l.each("filtered indexes in "+schema, `
		  SELECT o.name, i.name, i.has_filter, i.type_desc
		  FROM sys.indexes i
		  JOIN sys.objects o ON o.object_id = i.object_id
		  JOIN sys.schemas s ON s.schema_id = o.schema_id
		  WHERE s.name = @p1 AND i.name IS NOT NULL AND o.type = 'U'`, []any{schema}, func(rows *sql.Rows) error {
			var table, name, kind string
			var filtered bool
			if err := rows.Scan(&table, &name, &filtered, &kind); err != nil {
				return err
			}
			fact := ormIndexFact{partial: filtered, included: map[string]bool{}}
			if strings.Contains(strings.ToUpper(kind), "COLUMNSTORE") || strings.EqualFold(kind, "SPATIAL") ||
				strings.EqualFold(kind, "XML") {
				fact.method = strings.ToLower(kind)
			}
			f.setIndex(ormTableKey(schema, table), name, fact)
			return nil
		})

		// The shared introspection lists an index's INCLUDE columns among its
		// key columns. They are named here so they can be taken back out.
		l.each("included index columns in "+schema, `
		  SELECT o.name, i.name, c.name
		  FROM sys.index_columns ic
		  JOIN sys.indexes i ON i.object_id = ic.object_id AND i.index_id = ic.index_id
		  JOIN sys.objects o ON o.object_id = i.object_id
		  JOIN sys.schemas s ON s.schema_id = o.schema_id
		  JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
		  WHERE s.name = @p1 AND ic.is_included_column = 1 AND i.name IS NOT NULL`, []any{schema}, func(rows *sql.Rows) error {
			var table, name, column string
			if err := rows.Scan(&table, &name, &column); err != nil {
				return err
			}
			if fact, ok := f.indexes[ormTableKey(schema, table)][name]; ok && fact.included != nil {
				fact.included[column] = true
			}
			return nil
		})
	}
}

// --- Oracle ---------------------------------------------------------------

func (l *ormLoader) oracleFacts(f *ormFacts, schemas []string) {
	for _, schema := range schemas {
		// A character column's length in characters. The shared introspection
		// reads DATA_LENGTH, which is bytes: an NVARCHAR2(200) reports 400, and
		// a VARCHAR2(50 CHAR) reports 200 in a UTF-8 database.
		l.each("column lengths in "+schema, `
		  SELECT table_name, column_name, data_type, char_length, identity_column
		  FROM all_tab_columns
		  WHERE owner = NVL(:1, SYS_CONTEXT('USERENV','CURRENT_SCHEMA'))`, []any{oracleSchemaArg(schema)}, func(rows *sql.Rows) error {
			var table, column, dataType string
			var identity sql.NullString
			var length sql.NullInt64
			if err := rows.Scan(&table, &column, &dataType, &length, &identity); err != nil {
				return err
			}
			fact := ormColumnFact{auto: identity.String == "YES"}
			switch dataType {
			case "VARCHAR2", "NVARCHAR2", "CHAR", "NCHAR":
				if length.Valid && length.Int64 > 0 {
					fact.typ = dataType + "(" + strconv.FormatInt(length.Int64, 10) + ")"
				}
			}
			f.setColumn(ormTableKey(schema, table), column, fact)
			return nil
		})
	}
}
