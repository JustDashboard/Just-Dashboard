package dbx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// catalogSQLite is a database with one of everything SQLite's catalogue can
// describe, and a second file attached beside it holding a table of the same
// name as one in the first.
func catalogSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, _ := openTestDB(t)
	for _, s := range []string{
		`CREATE TABLE cat_users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			email TEXT NOT NULL UNIQUE,
			name TEXT,
			shout TEXT GENERATED ALWAYS AS (upper(name)) STORED,
			score INTEGER CHECK (score > 0))`,
		`CREATE TABLE cat_posts (
			id INTEGER PRIMARY KEY,
			author_id INTEGER NOT NULL REFERENCES cat_users(id) ON DELETE CASCADE,
			title TEXT NOT NULL,
			published INTEGER NOT NULL DEFAULT 0) STRICT`,
		`CREATE INDEX cat_posts_lower ON cat_posts(lower(title))`,
		`CREATE UNIQUE INDEX cat_posts_pub ON cat_posts(title) WHERE published = 1`,
		`CREATE TABLE cat_kv (k TEXT, part INTEGER, v TEXT, PRIMARY KEY (part, k)) WITHOUT ROWID`,
		`CREATE VIEW cat_active AS SELECT id, email FROM cat_users WHERE name IS NOT NULL`,
		`CREATE TRIGGER cat_touch AFTER INSERT ON cat_users BEGIN UPDATE cat_users SET name = name WHERE id = NEW.id; END`,
		// The name the unescaped LIKE 'sqlite_%' used to hide.
		`CREATE TABLE sqliteXodd (a)`,
		`ATTACH DATABASE ':memory:' AS extra`,
		`CREATE TABLE extra.cat_users (id INTEGER PRIMARY KEY, boss INTEGER REFERENCES cat_users(id))`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%v\n%s", err, s)
		}
	}
	return db
}

func catObject(objects []CatalogObject, name string) *CatalogObject {
	for i := range objects {
		if objects[i].Name == name {
			return &objects[i]
		}
	}
	return nil
}

func catColumn(t *testing.T, detail *TableDetail, name string) Column {
	t.Helper()
	for _, c := range detail.Columns {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no column %s in %+v", name, detail.Columns)
	return Column{}
}

func catIndex(detail *TableDetail, name string) *Index {
	for i := range detail.Indexes {
		if detail.Indexes[i].Name == name {
			return &detail.Indexes[i]
		}
	}
	return nil
}

func TestEverySQLDialectHasACatalogue(t *testing.T) {
	for _, driver := range Drivers() {
		_, cd, err := catalogFor(driver)
		if driver.IsSQL() && (err != nil || cd == nil) {
			t.Errorf("%s has no catalogue: %v", driver, err)
		}
		if !driver.IsSQL() && !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: error = %v, want ErrUnsupported", driver, err)
		}
	}
}

// What the driver catalogue advertises and what a read returns are written in
// two places, and a page draws its tree from the first before it has the
// second.
func TestAdvertisedGroupsAreTheGroupsRead(t *testing.T) {
	for _, driver := range []Driver{DriverPostgres, DriverSQLite, DriverMSSQL, DriverOracle, DriverClickHouse} {
		_, cd, err := catalogFor(driver)
		if err != nil {
			t.Fatal(err)
		}
		read := []string{}
		// None of these dialects consults the connection to list its groups.
		for _, g := range cd.catalogGroups(context.Background(), nil) {
			read = append(read, g.name)
		}
		if advertised := CatalogGroups(driver, ""); !reflect.DeepEqual(advertised, read) {
			t.Errorf("%s advertises %v and reads %v", driver, advertised, read)
		}
	}
	if groups := CatalogGroups(DriverMySQL, "mariadb"); !contains(groups, GroupSequences) {
		t.Errorf("mariadb groups = %v", groups)
	}
	if groups := CatalogGroups(DriverMySQL, "mysql"); contains(groups, GroupSequences) || !contains(groups, GroupEvents) {
		t.Errorf("mysql groups = %v", groups)
	}
	if groups := CatalogGroups(DriverRedis, ""); groups == nil || len(groups) != 0 {
		t.Errorf("redis groups = %#v", groups)
	}
}

func TestSQLiteCatalogListsWhatTheFileHolds(t *testing.T) {
	db := catalogSQLite(t)
	ctx := context.Background()

	catalog, err := ReadCatalog(ctx, db, DriverSQLite, CatalogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Schema != "main" || catalog.DefaultSchema != "main" {
		t.Errorf("schema = %q, default = %q", catalog.Schema, catalog.DefaultSchema)
	}
	// SQLite has tables, views and triggers and nothing else; a group it does
	// not have is absent rather than empty.
	groups := []string{}
	for g := range catalog.Objects {
		groups = append(groups, g)
	}
	if len(groups) != 3 || catalog.Objects[GroupTables] == nil || catalog.Objects[GroupViews] == nil ||
		catalog.Objects[GroupTriggers] == nil {
		t.Errorf("groups = %v", groups)
	}
	tables := catalog.Objects[GroupTables]
	for name, detail := range map[string]string{
		"cat_users": "", "cat_posts": "strict", "cat_kv": "without rowid", "sqliteXodd": "",
	} {
		o := catObject(tables, name)
		if o == nil {
			t.Fatalf("table %s is missing from %+v", name, tables)
		}
		if o.Kind != KindTable || o.Schema != "main" || o.Detail != detail || o.Rows == nil || *o.Rows != -1 {
			t.Errorf("%s = %+v", name, *o)
		}
	}
	if catObject(tables, "sqlite_sequence") != nil {
		t.Error("the engine's own sqlite_sequence is listed as a table")
	}
	if v := catObject(catalog.Objects[GroupViews], "cat_active"); v == nil || v.Kind != KindView {
		t.Errorf("views = %+v", catalog.Objects[GroupViews])
	}
	if tr := catObject(catalog.Objects[GroupTriggers], "cat_touch"); tr == nil || tr.Table != "cat_users" {
		t.Errorf("triggers = %+v", catalog.Objects[GroupTriggers])
	}
	if len(catalog.Schemas) != 2 || catalog.Schemas[0].Name != "main" || !catalog.Schemas[0].Default ||
		catalog.Schemas[1].Name != "extra" || catalog.Schemas[1].Tables != 1 {
		t.Errorf("schemas = %+v", catalog.Schemas)
	}

	// One schema by name, and all of them at once.
	extra, err := ReadCatalog(ctx, db, DriverSQLite, CatalogOptions{Schema: "extra"})
	if err != nil || len(extra.Objects[GroupTables]) != 1 || extra.Objects[GroupTables][0].Schema != "extra" {
		t.Errorf("extra = %+v, %v", extra, err)
	}
	all, err := ReadCatalog(ctx, db, DriverSQLite, CatalogOptions{All: true})
	if err != nil || all.Schema != "" {
		t.Fatalf("all = %+v, %v", all, err)
	}
	both := 0
	for _, o := range all.Objects[GroupTables] {
		if o.Name == "cat_users" {
			both++
		}
	}
	if both != 2 {
		t.Errorf("reading every schema found cat_users %d times, want once per schema", both)
	}
}

func TestCatalogGroupsAreBounded(t *testing.T) {
	db, _ := openTestDB(t)
	for i := range 7 {
		if _, err := db.Exec(fmt.Sprintf(`CREATE TABLE bound_%d (id INTEGER)`, i)); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := ReadCatalog(context.Background(), db, DriverSQLite, CatalogOptions{Limit: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Objects[GroupTables]) != 4 || catalog.Limit != 4 ||
		!reflect.DeepEqual(catalog.Truncated, []string{GroupTables}) {
		t.Errorf("tables = %d, limit = %d, truncated = %v", len(catalog.Objects[GroupTables]), catalog.Limit, catalog.Truncated)
	}
	// A group that fits says nothing, and the reply always carries the list so
	// a caller need not test for its absence.
	whole, err := ReadCatalog(context.Background(), db, DriverSQLite, CatalogOptions{Limit: MaxCatalogLimit * 10})
	if err != nil || whole.Limit != MaxCatalogLimit || whole.Truncated == nil || len(whole.Truncated) != 0 {
		t.Errorf("limit = %d, truncated = %#v, %v", whole.Limit, whole.Truncated, err)
	}
}

func TestSQLiteTableDetailCarriesTheWholeDeclaration(t *testing.T) {
	db := catalogSQLite(t)
	ctx := context.Background()

	users, err := DescribeTable(ctx, db, DriverSQLite, "", "cat_users")
	if err != nil {
		t.Fatal(err)
	}
	if users.Schema != "main" || users.Type != TableTypeTable || users.Rows != -1 ||
		users.CreateSQLSource != DefinitionFromEngine || !strings.Contains(users.CreateSQL, "AUTOINCREMENT") {
		t.Errorf("users = %+v", users)
	}
	if id := catColumn(t, users, "id"); id.Identity != "autoincrement" || id.Key != "PRI" {
		t.Errorf("id = %+v", id)
	}
	// A generated column is a column: SELECT * returns it, and the plain
	// pragma leaves it out.
	if shout := catColumn(t, users, "shout"); shout.Generated != "upper(name)" || shout.GeneratedKind != "stored" {
		t.Errorf("shout = %+v", shout)
	}
	if len(users.Constraints) != 1 || users.Constraints[0].Type != ConstraintUnique ||
		!reflect.DeepEqual(users.Constraints[0].Columns, []string{"email"}) ||
		users.Constraints[0].Definition != `UNIQUE ("email")` {
		t.Errorf("constraints = %+v", users.Constraints)
	}
	if len(users.ReferencedBy) != 1 || users.ReferencedBy[0].Table != "cat_posts" ||
		users.ReferencedBy[0].Schema != "main" || users.ReferencedBy[0].OnDelete != "CASCADE" ||
		!reflect.DeepEqual(users.ReferencedBy[0].Columns, []string{"author_id"}) ||
		!reflect.DeepEqual(users.ReferencedBy[0].RefColumns, []string{"id"}) {
		t.Errorf("referencedBy = %+v", users.ReferencedBy)
	}

	posts, err := DescribeTable(ctx, db, DriverSQLite, "main", "cat_posts")
	if err != nil {
		t.Fatal(err)
	}
	if id := catColumn(t, posts, "id"); id.Identity != "rowid" {
		t.Errorf("an INTEGER PRIMARY KEY is the rowid: %+v", id)
	}
	partial := catIndex(posts, "cat_posts_pub")
	if partial == nil || !partial.Unique || partial.Predicate != "published = 1" ||
		!strings.HasPrefix(partial.Definition, "CREATE UNIQUE INDEX cat_posts_pub") {
		t.Errorf("partial index = %+v", partial)
	}
	expression := catIndex(posts, "cat_posts_lower")
	if expression == nil || !expression.Expression || len(expression.Columns) != 1 {
		t.Errorf("expression index = %+v", expression)
	}
	if len(posts.ForeignKeys) != 1 || posts.ForeignKeys[0].RefSchema != "main" || posts.ForeignKeys[0].RefTable != "cat_users" {
		t.Errorf("foreign keys = %+v", posts.ForeignKeys)
	}
	if !reflect.DeepEqual(posts.Facts, []ObjectFact{{"Strict", "yes"}}) {
		t.Errorf("facts = %+v", posts.Facts)
	}

	// A composite key keeps its declared order, which is not column order.
	kv, err := DescribeTable(ctx, db, DriverSQLite, "main", "cat_kv")
	if err != nil || !reflect.DeepEqual(kv.PrimaryKey, []string{"part", "k"}) {
		t.Errorf("primary key = %v, %v", kv.PrimaryKey, err)
	}
	if id := catColumn(t, kv, "k"); id.Identity != "" {
		t.Errorf("a WITHOUT ROWID key is not a rowid: %+v", id)
	}

	// The table of the same name in the attached file is its own table, not
	// main's answered twice.
	other, err := DescribeTable(ctx, db, DriverSQLite, "extra", "cat_users")
	if err != nil {
		t.Fatal(err)
	}
	if other.Schema != "extra" || len(other.Columns) != 2 || !strings.Contains(other.CreateSQL, "boss") ||
		strings.Contains(other.CreateSQL, "AUTOINCREMENT") || !reflect.DeepEqual(other.PrimaryKey, []string{"id"}) {
		t.Errorf("extra.cat_users = %+v", other)
	}

	view, err := DescribeTable(ctx, db, DriverSQLite, "main", "cat_active")
	if err != nil || view.Type != TableTypeView || !strings.HasPrefix(view.CreateSQL, "CREATE VIEW cat_active") ||
		len(view.Columns) != 2 {
		t.Errorf("view = %+v, %v", view, err)
	}
}

func TestSQLiteObjectDefinitions(t *testing.T) {
	db := catalogSQLite(t)
	ctx := context.Background()

	view, err := ReadObjectDefinition(ctx, db, DriverSQLite, ObjectRef{Kind: KindView, Name: "cat_active"})
	if err != nil || view.Source != DefinitionFromEngine || view.Schema != "main" || view.Details == nil ||
		view.Definition != "CREATE VIEW cat_active AS SELECT id, email FROM cat_users WHERE name IS NOT NULL" {
		t.Errorf("view = %+v, %v", view, err)
	}
	trigger, err := ReadObjectDefinition(ctx, db, DriverSQLite, ObjectRef{Kind: KindTrigger, Name: "cat_touch"})
	if err != nil || trigger.Table != "cat_users" || !strings.HasPrefix(trigger.Definition, "CREATE TRIGGER cat_touch") {
		t.Errorf("trigger = %+v, %v", trigger, err)
	}
	table, err := ReadObjectDefinition(ctx, db, DriverSQLite, ObjectRef{Kind: KindTable, Name: "cat_kv"})
	if err != nil || !strings.Contains(table.Definition, "WITHOUT ROWID") || table.Schema != "main" {
		t.Errorf("table = %+v, %v", table, err)
	}

	for _, c := range []struct {
		ref  ObjectRef
		want error
	}{
		{ObjectRef{Kind: KindView, Name: "nope"}, ErrObjectNotFound},
		{ObjectRef{Kind: KindTable, Name: "nope"}, ErrObjectNotFound},
		// A view asked for as a trigger is not found, rather than answered
		// with the wrong kind of object.
		{ObjectRef{Kind: KindTrigger, Name: "cat_active"}, ErrObjectNotFound},
		{ObjectRef{Kind: KindFunction, Name: "anything"}, ErrNoDefinition},
		{ObjectRef{Kind: KindSequence, Name: "anything"}, ErrNoDefinition},
	} {
		if _, err := ReadObjectDefinition(ctx, db, DriverSQLite, c.ref); !errors.Is(err, c.want) {
			t.Errorf("%+v: error = %v, want %v", c.ref, err, c.want)
		}
	}
	for _, ref := range []ObjectRef{{Name: "x"}, {Kind: KindView}, {Kind: KindView, Name: "a\x00b"}} {
		if _, err := ReadObjectDefinition(ctx, db, DriverSQLite, ref); !errors.Is(err, ErrInvalidObject) {
			t.Errorf("%+v: error = %v, want ErrInvalidObject", ref, err)
		}
	}
}

// Node ids, edge ends and the keys of the relations and outline maps all name
// a table by schema and name together. The bare name was the id until two
// schemas had a table called the same thing.
func TestGraphOutlineAndRelationsQualifyEveryName(t *testing.T) {
	db := catalogSQLite(t)
	ctx := context.Background()

	graph, err := BuildSchemaGraph(ctx, db, DriverSQLite, "main")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]GraphTable{}
	for _, table := range graph.Tables {
		if table.ID != TableKey(table.Schema, table.Name) {
			t.Errorf("node id %q is not the table's key", table.ID)
		}
		ids[table.ID] = table
	}
	if _, ok := ids["main.cat_posts"]; !ok || graph.Total != len(graph.Tables) || graph.Truncated || graph.Limit != DefaultGraphTables {
		t.Fatalf("graph = %+v", graph)
	}
	var edge *GraphEdge
	for i := range graph.Edges {
		if graph.Edges[i].From == "main.cat_posts" {
			edge = &graph.Edges[i]
		}
	}
	if edge == nil || edge.To != "main.cat_users" || edge.FromSchema != "main" || edge.ToSchema != "main" ||
		edge.FromTable != "cat_posts" || edge.ToTable != "cat_users" || edge.FromColumn != "author_id" ||
		edge.ToColumn != "id" || edge.OnDelete != "CASCADE" || edge.Cardinality != "many-to-one" {
		t.Errorf("edge = %+v", edge)
	}
	if _, ok := ids[edge.To]; !ok {
		t.Errorf("the edge points at %q, which is not a node", edge.To)
	}
	for _, c := range ids["main.cat_posts"].Columns {
		switch c.Name {
		case "author_id":
			if c.ForeignKey != "cat_users" || c.ForeignKeyID != "main.cat_users" {
				t.Errorf("author_id = %+v", c)
			}
		case "title":
			// Unique only among published rows, and unique only as lower():
			// neither index makes the column unique.
			if c.Unique {
				t.Errorf("a partial unique index made title unique: %+v", c)
			}
		}
	}

	relations, err := Relations(ctx, db, DriverSQLite, "")
	if err != nil {
		t.Fatal(err)
	}
	fks, ok := relations["main.cat_posts"]
	if !ok || len(fks) != 1 || TableKey(fks[0].RefSchema, fks[0].RefTable) != "main.cat_users" {
		t.Errorf("relations = %+v", relations)
	}
	if _, ok := relations["main.cat_active"]; ok {
		t.Error("a view was asked for its foreign keys")
	}

	outline, err := Outline(ctx, db, DriverSQLite, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(outline.Entries) != len(outline.Tables) || outline.Total != len(outline.Tables) || outline.Truncated {
		t.Fatalf("outline = %+v", outline)
	}
	for _, e := range outline.Entries {
		if e.ID != TableKey(e.Schema, e.Name) {
			t.Errorf("entry %+v does not name its own key", e)
		}
		if _, ok := outline.Tables[e.ID]; !ok {
			t.Errorf("entry %q has no columns", e.ID)
		}
	}
	// The generated column is completed like any other.
	if cols := outline.Tables["main.cat_users"]; !contains(cols, "shout") {
		t.Errorf("cat_users columns = %v", cols)
	}
}

func TestGraphAndOutlineSayHowMuchTheyLeftOut(t *testing.T) {
	db, _ := openTestDB(t)
	for i := range 128 {
		if _, err := db.Exec(fmt.Sprintf(`CREATE TABLE cap_%03d (id INTEGER PRIMARY KEY)`, i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`CREATE VIEW aaa_first AS SELECT 1 AS one`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const total = 128 + 2 + 1 // the fixture's own two tables and the view

	graph, err := BuildSchemaGraph(ctx, db, DriverSQLite, "main")
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Truncated || graph.Total != total || graph.Limit != DefaultGraphTables || len(graph.Tables) != DefaultGraphTables {
		t.Errorf("graph: truncated=%v total=%d limit=%d tables=%d", graph.Truncated, graph.Total, graph.Limit, len(graph.Tables))
	}
	// What is cut is what the picture can best do without: the view sorts
	// first by name and still gives its place to a table.
	for _, table := range graph.Tables {
		if table.Type == TableTypeView {
			t.Errorf("a view kept a place a table lost: %s", table.Name)
		}
	}
	wider, err := BuildSchemaGraphWithLimit(ctx, db, DriverSQLite, "main", 500)
	if err != nil || wider.Truncated || wider.Total != total || len(wider.Tables) != total || wider.Limit != 500 {
		t.Errorf("wider graph: %+v, %v", wider, err)
	}
	clamped, err := BuildSchemaGraphWithLimit(ctx, db, DriverSQLite, "main", MaxGraphTables*4)
	if err != nil || clamped.Limit != MaxGraphTables {
		t.Errorf("limit = %d, %v", clamped.Limit, err)
	}

	outline, err := OutlineWithLimit(ctx, db, DriverSQLite, "main", 10)
	if err != nil || !outline.Truncated || outline.Total != total || outline.Limit != 10 ||
		len(outline.Tables) != 10 || len(outline.Entries) != 10 {
		t.Errorf("outline: %+v, %v", outline, err)
	}
}

func TestTableKeyAndRelationKinds(t *testing.T) {
	if TableKey("", "t") != "t" || TableKey("s", "t") != "s.t" {
		t.Error("TableKey")
	}
	for kind, want := range map[string]bool{
		"table": true, "BASE TABLE": true, TableTypePartitioned: true,
		"view": false, TableTypeMaterializedView: false, TableTypePartition: false, "sequence": false,
	} {
		if holdsForeignKeys(kind) != want {
			t.Errorf("holdsForeignKeys(%q) = %v", kind, !want)
		}
	}
	for kind, want := range map[string]bool{
		"table": true, "view": true, TableTypeMaterializedView: true, TableTypePartitioned: true,
		TableTypePartition: false, "sequence": false, "dictionary": false,
	} {
		if drawable(kind) != want {
			t.Errorf("drawable(%q) = %v", kind, !want)
		}
	}
}

// The generated CREATE TABLE has two readers. A dump replays it ahead of the
// rows, so it must not declare a column the rows cannot be inserted into; a
// person reads it to learn how the table is declared, so it must.
func TestGeneratedCreateTableForReplayAndForReading(t *testing.T) {
	detail := &TableDetail{
		Schema: "shop", Name: "orders", Comment: "Customer's orders",
		Columns: []Column{
			{Name: "id", Type: "bigint", Identity: "always"},
			{Name: "status", Type: "shop.order_status", Default: "'new'::shop.order_status", Comment: "Where it is"},
			{Name: "total", Type: "numeric(12,2)", Nullable: true},
			{Name: "label", Type: "text", Nullable: true, Generated: "upper(status::text)", GeneratedKind: "stored"},
		},
		PrimaryKey: []string{"id"},
		Indexes: []Index{
			{Name: "orders_pkey", Columns: []string{"id"}, Unique: true, Primary: true, Constraint: "orders_pkey",
				Definition: "CREATE UNIQUE INDEX orders_pkey ON shop.orders USING btree (id)"},
			{Name: "orders_ref_key", Columns: []string{"total"}, Unique: true, Constraint: "orders_ref_key",
				Definition: "CREATE UNIQUE INDEX orders_ref_key ON shop.orders USING btree (total)"},
			{Name: "orders_open_idx", Columns: []string{"status"}, Predicate: "total > 0",
				Definition: "CREATE INDEX orders_open_idx ON shop.orders USING btree (status) WHERE (total > 0)"},
		},
		Constraints: []Constraint{
			{Name: "orders_ref_key", Type: ConstraintUnique, Columns: []string{"total"}, Definition: "UNIQUE (total)"},
			{Name: "orders_total_check", Type: ConstraintCheck, Definition: "CHECK (total >= 0)"},
		},
		ForeignKeys: []ForeignKey{{
			Name: "orders_customer_fkey", Columns: []string{"id"}, RefSchema: "shop", RefTable: "customers",
			RefColumns: []string{"id"}, OnDelete: "CASCADE", OnUpdate: "NO ACTION",
		}},
		Facts: []ObjectFact{{factPartitionKey, "RANGE (id)"}},
	}
	d := postgresDialect{}

	replay := synthCreateTable(d, "shop", "orders", detail)
	for _, want := range []string{
		`CREATE TABLE "shop"."orders" (`,
		`"id" bigint NOT NULL,`,
		`"status" shop.order_status NOT NULL DEFAULT 'new'::shop.order_status,`,
		`"label" text,`,
		`CONSTRAINT "orders_pkey" PRIMARY KEY ("id")`,
		`CONSTRAINT "orders_ref_key" UNIQUE (total)`,
		`CONSTRAINT "orders_total_check" CHECK (total >= 0)`,
		`CONSTRAINT "orders_customer_fkey" FOREIGN KEY ("id") REFERENCES "shop"."customers" ("id") ON DELETE CASCADE`,
		"\n);\n\nCREATE INDEX orders_open_idx ON shop.orders USING btree (status) WHERE (total > 0);",
	} {
		if !strings.Contains(replay, want) {
			t.Errorf("the replay form is missing %q in:\n%s", want, replay)
		}
	}
	for _, unwanted := range []string{
		// Neither accepts an inserted value.
		"IDENTITY", "GENERATED",
		// The constraint above already made these.
		"CREATE UNIQUE INDEX orders_pkey", "CREATE UNIQUE INDEX orders_ref_key",
		"COMMENT ON", "PARTITION BY", "ON UPDATE",
	} {
		if strings.Contains(replay, unwanted) {
			t.Errorf("the replay form contains %q:\n%s", unwanted, replay)
		}
	}

	shown := renderCreateTable(d, "shop", "orders", detail, true)
	for _, want := range []string{
		`"id" bigint GENERATED ALWAYS AS IDENTITY NOT NULL,`,
		`"label" text GENERATED ALWAYS AS (upper(status::text)) STORED,`,
		`) PARTITION BY RANGE (id);`,
		`COMMENT ON TABLE "shop"."orders" IS 'Customer''s orders';`,
		`COMMENT ON COLUMN "shop"."orders"."status" IS 'Where it is';`,
		"CREATE INDEX orders_open_idx",
	} {
		if !strings.Contains(shown, want) {
			t.Errorf("the shown form is missing %q in:\n%s", want, shown)
		}
	}

	// SQL Server spells both differently, and a computed column has no type.
	mssql := renderCreateTable(mssqlDialect{}, "dbo", "orders", &TableDetail{
		Columns: []Column{
			{Name: "id", Type: "int", Identity: "identity(1,1)"},
			{Name: "gross", Type: "decimal(18,2)", Generated: "([net]*(1.2))", GeneratedKind: "stored"},
			{Name: "note", Type: "nvarchar(MAX)", Nullable: true, Default: "(N'')"},
		},
	}, true)
	for _, want := range []string{
		"CREATE TABLE [dbo].[orders] (", "[id] int IDENTITY(1,1) NOT NULL,",
		"[gross] AS ([net]*(1.2)) PERSISTED,", "[note] nvarchar(MAX) NULL DEFAULT (N'')",
	} {
		if !strings.Contains(mssql, want) {
			t.Errorf("the SQL Server form is missing %q in:\n%s", want, mssql)
		}
	}
}

func TestPostgresTriggerTiming(t *testing.T) {
	for tgtype, want := range map[int]string{
		// ROW | BEFORE | INSERT | UPDATE
		1 | 2 | 4 | 16: "BEFORE INSERT OR UPDATE, each row",
		// AFTER DELETE, per statement
		8: "AFTER DELETE, each statement",
		// INSTEAD OF INSERT on a view, per row
		1 | 64 | 4: "INSTEAD OF INSERT, each row",
		32:         "AFTER TRUNCATE, each statement",
	} {
		if got := pgTriggerTiming(tgtype); got != want {
			t.Errorf("tgtype %d = %q, want %q", tgtype, got, want)
		}
	}
}

func TestEnumLabelsAreReadOutOfColumnTypes(t *testing.T) {
	kind, values, ok := mysqlEnumValues("enum('sad','ok','it''s','a,b','')")
	if !ok || kind != "enum" || !reflect.DeepEqual(values, []string{"sad", "ok", "it's", "a,b", ""}) {
		t.Errorf("enum = %q %q %v", kind, values, ok)
	}
	if kind, values, ok := mysqlEnumValues("set('r','w')"); !ok || kind != "set" || len(values) != 2 {
		t.Errorf("set = %q %q %v", kind, values, ok)
	}
	for _, typ := range []string{"varchar(255)", "int", "enumerated", "enum("} {
		if _, _, ok := mysqlEnumValues(typ); ok {
			t.Errorf("%q was read as an enum", typ)
		}
	}
	if got := clickhouseEnumValues(`Enum8('click' = 1, 'it\'s' = 2, 'a = b' = 3)`); !reflect.DeepEqual(got, []string{"click", "it's", "a = b"}) {
		t.Errorf("clickhouse enum = %q", got)
	}
}

// information_schema reports a MySQL default as the bare value and a MariaDB
// one as SQL text. Both come out as the text that follows DEFAULT.
func TestMySQLDefaultsBecomeSQLText(t *testing.T) {
	text := func(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
	for _, c := range []struct {
		typ   string
		raw   sql.NullString
		extra string
		maria bool
		want  string
	}{
		{"varchar(20)", sql.NullString{}, "", false, ""},
		{"varchar(20)", text("ok"), "", false, "'ok'"},
		{"varchar(20)", text(""), "", false, "''"},
		{"varchar(20)", text(`it's \ here`), "", false, `'it''s \\ here'`},
		{"int(11)", text("0"), "", false, "0"},
		{"decimal(10,2) unsigned", text("1.50"), "", false, "1.50"},
		{"timestamp", text("CURRENT_TIMESTAMP"), "default_generated", false, "CURRENT_TIMESTAMP"},
		{"datetime(3)", text("CURRENT_TIMESTAMP(3)"), "default_generated on update current_timestamp(3)", false, "CURRENT_TIMESTAMP(3)"},
		// A server from before 8.0.13 has no DEFAULT_GENERATED to mark the
		// clock with, and a quoted one is a string no timestamp accepts.
		{"timestamp", text("CURRENT_TIMESTAMP"), "", false, "CURRENT_TIMESTAMP"},
		{"datetime(6)", text("CURRENT_TIMESTAMP(6)"), "on update current_timestamp(6)", false, "CURRENT_TIMESTAMP(6)"},
		{"timestamp", text("2020-01-01 00:00:00"), "", false, "'2020-01-01 00:00:00'"},
		{"varchar(30)", text("CURRENT_TIMESTAMP"), "", false, "'CURRENT_TIMESTAMP'"},
		{"char(36)", text("uuid()"), "default_generated", false, "(uuid())"},
		// An expression is stored with its quotes and backslashes escaped once
		// more than the statement writes them.
		{"varchar(20)", text(`concat(_utf8mb4\'a\\\\\',_utf8mb4\'it\\\'s\')`), "default_generated", false,
			`(concat(_utf8mb4'a\\',_utf8mb4'it\'s'))`},
		{"enum('a','b')", text("a"), "", false, "'a'"},
		// MariaDB already wrote SQL.
		{"varchar(20)", text("'ok'"), "", true, "'ok'"},
		{"varchar(20)", text("NULL"), "", true, ""},
		{"timestamp", text("current_timestamp()"), "", true, "current_timestamp()"},
		{"int(11)", text("0"), "", true, "0"},
	} {
		if got := mysqlDefaultSQL(c.typ, c.raw, c.extra, c.maria); got != c.want {
			t.Errorf("default of %s %+v (maria=%v) = %q, want %q", c.typ, c.raw, c.maria, got, c.want)
		}
	}
}

// MODIFY COLUMN and ALTER COLUMN replace a whole declaration, so the one
// written back has to say everything the old one did.
func TestRestatedColumnsSayEverythingAgain(t *testing.T) {
	d, err := DialectFor(DriverMySQL)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		column mysqlColumn
		want   string
	}{
		{mysqlColumn{columnType: "int(11)", nullable: true}, "int(11) NULL"},
		{mysqlColumn{columnType: "bigint", autoIncrement: true}, "bigint NOT NULL AUTO_INCREMENT"},
		{mysqlColumn{columnType: "longtext", collation: "utf8mb4_bin", nullable: true, comment: "it's",
			checks: []string{"json_valid(`doc`)"}},
			"longtext COLLATE utf8mb4_bin NULL COMMENT 'it''s' CHECK (json_valid(`doc`))"},
		{mysqlColumn{columnType: "int(11)", nullable: true, dflt: "7", invisible: true, unversioned: true},
			"int(11) NULL DEFAULT 7 INVISIBLE WITHOUT SYSTEM VERSIONING"},
		{mysqlColumn{columnType: "timestamp", dflt: "CURRENT_TIMESTAMP", onUpdate: "CURRENT_TIMESTAMP"},
			"timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP"},
		{mysqlColumn{columnType: "point", srid: "SRID 4326"}, "point NOT NULL SRID 4326"},
		{mysqlColumn{columnType: "point", srid: "REF_SYSTEM_ID=4326"}, "point REF_SYSTEM_ID=4326 NOT NULL"},
	} {
		if got, err := c.column.render(d); err != nil || got != c.want {
			t.Errorf("restated as %q, %v; want %q", got, err, c.want)
		}
	}
	if _, err := (&mysqlColumn{columnType: "text", collation: "x; DROP TABLE y"}).render(d); err == nil {
		t.Error("a collation that is not a name was restated")
	}
	// An EXTRA entry nobody accounted for is an attribute about to be lost.
	for extra, want := range map[string]string{
		"":               "",
		"auto_increment": "",
		"default_generated on update current_timestamp(3)": "",
		"invisible, without system versioning":             "",
		"DEFAULT_GENERATED INVISIBLE":                      "",
		"invisible, some new attribute":                    "some new attribute",
		"not secondary":                                    "not secondary",
	} {
		if got := mysqlUnknownExtra(strings.ToLower(extra)); got != want {
			t.Errorf("unknown part of EXTRA %q = %q, want %q", extra, got, want)
		}
	}

	column := mssqlColumn{typ: "varchar(20)", collation: "Latin1_General_BIN2"}
	if got := column.render(); got != "varchar(20) COLLATE Latin1_General_BIN2 NOT NULL" {
		t.Errorf("sql server column = %s", got)
	}
	// A new character type keeps the collation the column was declared with;
	// a type that has none drops it.
	column.retype("nvarchar(max)")
	column.nullable = true
	if got := column.render(); got != "nvarchar(max) COLLATE Latin1_General_BIN2 NULL" {
		t.Errorf("sql server column retyped = %s", got)
	}
	column.retype("int")
	if got := column.render(); got != "int NULL" {
		t.Errorf("sql server column as a number = %s", got)
	}
}

func TestSQLServerTypeNames(t *testing.T) {
	for _, c := range []struct {
		name                        string
		maxLength, precision, scale int
		want                        string
	}{
		// The catalogue counts bytes, and nvarchar stores two per character.
		{"nvarchar", 510, 0, 0, "nvarchar(255)"},
		{"nvarchar", -1, 0, 0, "nvarchar(MAX)"},
		{"varchar", 40, 0, 0, "varchar(40)"},
		{"varbinary", -1, 0, 0, "varbinary(MAX)"},
		{"decimal", 9, 18, 2, "decimal(18,2)"},
		{"numeric", 5, 10, 0, "numeric(10)"},
		{"datetime2", 8, 27, 7, "datetime2"},
		{"datetime2", 7, 23, 3, "datetime2(3)"},
		{"int", 4, 10, 0, "int"},
		{"uniqueidentifier", 16, 0, 0, "uniqueidentifier"},
	} {
		if got := mssqlTypeName(c.name, c.maxLength, c.precision, c.scale); got != c.want {
			t.Errorf("%s(%d,%d,%d) = %q, want %q", c.name, c.maxLength, c.precision, c.scale, got, c.want)
		}
	}
	d := mssqlDialect{}
	got := mssqlIndexDefinition(d, "dbo", "orders",
		Index{Name: "ix_open", Unique: true, Method: "NONCLUSTERED", Predicate: "([closed]=(0))"},
		[]string{"[customer]", "[placed] DESC"}, []string{"[total]"})
	want := "CREATE UNIQUE NONCLUSTERED INDEX [ix_open] ON [dbo].[orders] ([customer], [placed] DESC) INCLUDE ([total]) WHERE ([closed]=(0))"
	if got != want {
		t.Errorf("index definition =\n%s\nwant\n%s", got, want)
	}
	// A columnstore index takes options this does not model, so it gets no
	// statement rather than a wrong one.
	if got := mssqlIndexDefinition(d, "dbo", "orders", Index{Name: "cs", Method: "CLUSTERED COLUMNSTORE"}, []string{"[a]"}, nil); got != "" {
		t.Errorf("columnstore definition = %q", got)
	}
	if got := mssqlTriggerTiming(true, true, false, true, true); got != "INSTEAD OF INSERT, DELETE, each statement, disabled" {
		t.Errorf("trigger timing = %q", got)
	}
}

func TestSQLServerAliasTypesCarryTheirSchema(t *testing.T) {
	for _, c := range []struct{ schema, name, want string }{
		{"dbo", "phone", "phone"},
		{"", "phone", "phone"},
		{"shop", "phone", "shop.phone"},
		{"shop", "phone number", "shop.[phone number]"},
		{"my shop", "a]b", "[my shop].[a]]b]"},
	} {
		if got := mssqlAliasTypeName(c.schema, c.name); got != c.want {
			t.Errorf("alias type %s.%s = %q, want %q", c.schema, c.name, got, c.want)
		}
	}
}

func TestOracleCatalogueHelpers(t *testing.T) {
	for _, c := range []struct{ triggerType, event, status, want string }{
		{"BEFORE EACH ROW", "INSERT OR UPDATE", "ENABLED", "BEFORE INSERT OR UPDATE, each row"},
		{"AFTER STATEMENT", "DELETE", "DISABLED", "AFTER DELETE, each statement, disabled"},
		{"INSTEAD OF", "INSERT", "ENABLED", "INSTEAD OF INSERT, each statement"},
	} {
		if got := oracleTriggerTiming(c.triggerType, c.event, c.status); got != c.want {
			t.Errorf("%q %q = %q, want %q", c.triggerType, c.event, got, c.want)
		}
	}
	if got := oracleSynonymTarget("HR", "EMPLOYEES", "PROD"); got != "HR.EMPLOYEES@PROD" {
		t.Errorf("synonym target = %q", got)
	}
	// The check Oracle writes for every NOT NULL column is the column's
	// nullability, not a constraint worth listing.
	if !oracleNotNullCheck.MatchString(`"EMAIL" IS NOT NULL`) || oracleNotNullCheck.MatchString(`"A" IS NOT NULL AND "B" > 0`) {
		t.Error("the NOT NULL check is not told apart from a real one")
	}
	// The driver counts every placeholder in the text, so the schema is bound
	// once or not at all, and what follows it keeps its place after it.
	cond, args := oracleSchemaFilter("t.owner", "APP", "FUNCTION", 5)
	if cond != "t.owner = :1" || !reflect.DeepEqual(args, []any{"APP", "FUNCTION", 5}) {
		t.Errorf("one schema: %s with %v", cond, args)
	}
	cond, args = oracleSchemaFilter("t.owner", " ", 5)
	if strings.Contains(cond, ":") || !strings.HasPrefix(cond, "t.owner NOT IN ('SYS',") || !reflect.DeepEqual(args, []any{5}) {
		t.Errorf("every schema: %s with %v", cond, args)
	}
}

func TestSQLiteDeclarationHelpers(t *testing.T) {
	const create = `CREATE TABLE "t" (
		id INTEGER PRIMARY KEY,
		"full name" TEXT GENERATED ALWAYS AS (first || ' ' || last) VIRTUAL,
		[total] REAL AS (round(net * (1 + rate), 2)) STORED,
		note TEXT DEFAULT 'a, b (c)'
	) STRICT, WITHOUT ROWID`
	for column, want := range map[string]string{
		"full name": "first || ' ' || last",
		"total":     "round(net * (1 + rate), 2)",
		"note":      "",
		"missing":   "",
	} {
		if got := sqliteGeneratedExpression(create, column); got != want {
			t.Errorf("expression of %q = %q, want %q", column, got, want)
		}
	}
	if !sqliteHasTableOption(create, "WITHOUT ROWID") || !sqliteHasTableOption(create, "STRICT") {
		t.Error("table options were not found after the closing parenthesis")
	}
	if sqliteHasTableOption(`CREATE TABLE t (strict TEXT)`, "STRICT") {
		t.Error("a column named strict was read as the STRICT option")
	}
	for create, want := range map[string]string{
		`CREATE UNIQUE INDEX i ON t(a) WHERE b = 1`:          "b = 1",
		`CREATE INDEX i ON t(a, lower(b)) WHERE (c > 0)`:     "(c > 0)",
		`CREATE INDEX i ON t(a)`:                             "",
		"CREATE INDEX i ON t(a)\nWHERE deleted_at IS NULL":   "deleted_at IS NULL",
		`CREATE INDEX i ON t(coalesce(nowhere, 1))`:          "",
		`CREATE INDEX "where" ON t(a) WHERE a IN ('x', 'y')`: "a IN ('x', 'y')",
	} {
		if got := sqlitePredicate(create); got != want {
			t.Errorf("predicate of %q = %q, want %q", create, got, want)
		}
	}
}

// What SQLite's ALTER TABLE cannot do is refused before anything is sent, and
// what it can do runs.
func TestSQLiteStructureChanges(t *testing.T) {
	db := catalogSQLite(t)
	ctx := context.Background()
	run := func(plan *DDLPlan, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		if err := plan.Exec(ctx, db); err != nil {
			t.Fatalf("%s: %v", plan, err)
		}
	}

	run(PlanCreateIndex(ctx, db, DriverSQLite, IndexSpec{
		Schema: "main", Table: "cat_posts", Name: "cat_posts_live", Columns: []string{"author_id"},
		Unique: true, Where: "published = 1", IfNotExists: true,
	}))
	// IF NOT EXISTS makes the second run a no-op rather than an error.
	run(PlanCreateIndex(ctx, db, DriverSQLite, IndexSpec{
		Schema: "main", Table: "cat_posts", Name: "cat_posts_live", Columns: []string{"author_id"},
		Unique: true, Where: "published = 1", IfNotExists: true,
	}))
	posts, err := DescribeTable(ctx, db, DriverSQLite, "main", "cat_posts")
	if err != nil {
		t.Fatal(err)
	}
	if ix := catIndex(posts, "cat_posts_live"); ix == nil || !ix.Unique || ix.Predicate != "(published = 1)" {
		t.Errorf("created index = %+v", ix)
	}
	run(PlanDropIndex(DriverSQLite, "main", "cat_posts", "cat_posts_live"))

	run(PlanCreateView(DriverSQLite, ViewSpec{Schema: "main", Name: "cat_titles", Query: "SELECT title FROM cat_posts"}))
	if def, err := ReadObjectDefinition(ctx, db, DriverSQLite, ObjectRef{Kind: KindView, Name: "cat_titles"}); err != nil ||
		def.Definition != `CREATE VIEW "cat_titles" AS`+"\nSELECT title FROM cat_posts" {
		t.Errorf("created view = %+v, %v", def, err)
	}
	run(PlanDropView(DriverSQLite, "main", "cat_titles", false))
	if _, err := ReadObjectDefinition(ctx, db, DriverSQLite, ObjectRef{Kind: KindView, Name: "cat_titles"}); !errors.Is(err, ErrObjectNotFound) {
		t.Errorf("the dropped view is still there: %v", err)
	}

	before := sqliteCreateText(ctx, db, "main", "cat_users")
	for name, err := range map[string]error{
		"alter column": planErr(PlanAlterColumn(ctx, db, DriverSQLite, ColumnChange{Table: "cat_users", Column: "name", Type: "INTEGER"})),
		"foreign key": planErr(PlanAddForeignKey(DriverSQLite, ForeignKeySpec{
			Table: "cat_users", Columns: []string{"score"}, RefTable: "cat_posts", RefColumns: []string{"id"}})),
		"unique":        planErr(PlanAddConstraint(DriverSQLite, ConstraintSpec{Table: "cat_users", Type: "unique", Columns: []string{"name"}})),
		"comment":       planErr(PlanComment(ctx, db, DriverSQLite, CommentSpec{Table: "cat_users", Comment: "x"})),
		"create schema": planErr(PlanCreateSchema(DriverSQLite, "x")),
		"enum":          planErr(PlanCreateEnum(DriverSQLite, "", "mood", []string{"a"})),
	} {
		if err == nil || !strings.Contains(err.Error(), "SQLite") {
			t.Errorf("%s: error = %v, want one that says what SQLite cannot do", name, err)
		}
	}
	if after := sqliteCreateText(ctx, db, "main", "cat_users"); after != before {
		t.Errorf("a refused change altered the table:\n%s", after)
	}

	// A statement is written unqualified, so one aimed at an attached file
	// would land on main's table of the same name. It is refused instead.
	for name, err := range map[string]error{
		"drop table":   planErr(PlanDropTable(DriverSQLite, "other", "cat_users")),
		"truncate":     planErr(PlanTruncate(DriverSQLite, "other", "cat_users")),
		"add column":   planErr(PlanAddColumn(DriverSQLite, "other", "cat_users", NewColumn{Name: "x", Type: "TEXT"})),
		"create table": planErr(PlanCreateTable(DriverSQLite, "other", "t", []NewColumn{{Name: "x", Type: "TEXT"}})),
		"drop column":  planErr(PlanDropColumn(ctx, db, DriverSQLite, "temp", "cat_users", "name")),
		"rename":       planErr(PlanRenameTable(DriverSQLite, "other", "cat_users", "people")),
		"create index": planErr(PlanCreateIndex(ctx, db, DriverSQLite, IndexSpec{Schema: "other", Table: "cat_users", Columns: []string{"name"}})),
		"drop index":   planErr(PlanDropIndex(DriverSQLite, "other", "cat_users", "i")),
		"create view":  planErr(PlanCreateView(DriverSQLite, ViewSpec{Schema: "other", Name: "v", Query: "SELECT 1"})),
		"drop view":    planErr(PlanDropView(DriverSQLite, "other", "v", false)),
	} {
		if err == nil || !strings.Contains(err.Error(), "main database only") {
			t.Errorf("%s in an attached database: error = %v", name, err)
		}
	}
	if _, err := PlanDropTable(DriverSQLite, "MAIN", "cat_users"); err != nil {
		t.Errorf("main by name was refused: %v", err)
	}
}
