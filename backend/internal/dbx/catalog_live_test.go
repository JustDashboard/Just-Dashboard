package dbx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

// inDSNSchema points the fixtures of the engines whose schema is a database at
// the database their DSN names. The fixture list spells the default — the
// database a developer's local server is seeded with — and a run that is given
// its own database through the environment creates its tables there, so a
// catalogue read still aimed at the default found nothing.
func inDSNSchema(fixtures []engineFixture) []engineFixture {
	for i, f := range fixtures {
		if f.driver != DriverMySQL && f.driver != DriverClickHouse {
			continue
		}
		dsn := os.Getenv(f.env)
		if dsn == "" {
			continue
		}
		if info, err := ParseDSN(f.driver, dsn); err == nil && info.Database != "" {
			fixtures[i].schema = info.Database
		}
	}
	return fixtures
}

// outlineColumns finds a table in an outline by its bare name, whichever
// schema the engine filed it under.
func outlineColumns(outline *SchemaOutline, table string) ([]string, bool) {
	for _, e := range outline.Entries {
		if e.Name == table {
			cols, ok := outline.Tables[e.ID]
			return cols, ok
		}
	}
	return nil, false
}

// --- live: the catalogue and structure changes against real servers ---------
//
// The unit tests prove a plan is the statement intended. Only a server proves
// it is a statement that server accepts, and that what it did can be read back
// through the catalogue — which is the round trip a schema editor makes.

func catalogExec(t *testing.T, db *sql.DB, statements ...string) {
	t.Helper()
	for _, s := range statements {
		if _, err := db.ExecContext(context.Background(), s); err != nil {
			t.Fatalf("%v\n%s", err, s)
		}
	}
}

func runPlan(t *testing.T, db *sql.DB) func(*DDLPlan, error) string {
	return func(plan *DDLPlan, err error) string {
		t.Helper()
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		if err := plan.Exec(context.Background(), db); err != nil {
			t.Fatalf("%v\n%s", err, plan)
		}
		return plan.String()
	}
}

func catFact(list []ObjectFact, name string) string {
	for _, f := range list {
		if f.Name == name {
			return f.Value
		}
	}
	return ""
}

func catConstraint(detail *TableDetail, name string) *Constraint {
	for i := range detail.Constraints {
		if detail.Constraints[i].Name == name {
			return &detail.Constraints[i]
		}
	}
	return nil
}

// postgresCatalogFixture builds two schemas with one of every kind of object,
// and a table called `users` in both.
func postgresCatalogFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	drop := []string{`DROP SCHEMA IF EXISTS jd_cat CASCADE`, `DROP SCHEMA IF EXISTS jd_cat2 CASCADE`}
	catalogExec(t, db, drop...)
	t.Cleanup(func() {
		for _, s := range drop {
			_, _ = db.ExecContext(context.Background(), s)
		}
	})
	catalogExec(t, db,
		`CREATE SCHEMA jd_cat`, `COMMENT ON SCHEMA jd_cat IS 'catalogue fixture'`, `CREATE SCHEMA jd_cat2`,
		`CREATE TYPE jd_cat.mood AS ENUM ('sad','ok','happy')`,
		`CREATE DOMAIN jd_cat.positive AS integer CHECK (VALUE > 0)`,
		`CREATE TYPE jd_cat.pair AS (a integer, b text)`,
		`CREATE SEQUENCE jd_cat.ticket_seq START 100 INCREMENT 5`,
		`CREATE TABLE jd_cat.users (
		  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		  email varchar(255) NOT NULL,
		  mood jd_cat.mood NOT NULL DEFAULT 'ok',
		  tags text[] NOT NULL DEFAULT '{}',
		  score jd_cat.positive,
		  name text,
		  shout text GENERATED ALWAYS AS (upper(name)) STORED,
		  created timestamptz NOT NULL DEFAULT now(),
		  CONSTRAINT users_email_key UNIQUE (email),
		  CONSTRAINT users_name_len CHECK (char_length(name) < 100))`,
		`COMMENT ON TABLE jd_cat.users IS 'People'`,
		`COMMENT ON COLUMN jd_cat.users.email IS 'Login address'`,
		`CREATE TABLE jd_cat.posts (
		  id serial PRIMARY KEY,
		  author_id bigint NOT NULL REFERENCES jd_cat.users(id) ON DELETE CASCADE,
		  title text NOT NULL, body text, published boolean NOT NULL DEFAULT false)`,
		`CREATE INDEX posts_author_idx ON jd_cat.posts(author_id)`,
		`CREATE INDEX posts_lower_title_idx ON jd_cat.posts(lower(title))`,
		`CREATE UNIQUE INDEX posts_published_title ON jd_cat.posts(title) INCLUDE (body) WHERE published`,
		`CREATE VIEW jd_cat.active_users AS SELECT id, email FROM jd_cat.users WHERE name IS NOT NULL`,
		`CREATE MATERIALIZED VIEW jd_cat.user_counts AS SELECT mood, count(*) AS n FROM jd_cat.users GROUP BY mood`,
		`CREATE FUNCTION jd_cat.add(a integer, b integer) RETURNS integer LANGUAGE sql IMMUTABLE AS 'SELECT a + b'`,
		`CREATE FUNCTION jd_cat.add(a numeric, b numeric) RETURNS numeric LANGUAGE sql IMMUTABLE AS 'SELECT a + b'`,
		`CREATE FUNCTION jd_cat.touch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.name := NEW.name; RETURN NEW; END $$`,
		`CREATE PROCEDURE jd_cat.noop() LANGUAGE sql AS 'SELECT 1'`,
		`CREATE TRIGGER users_touch BEFORE INSERT OR UPDATE ON jd_cat.users FOR EACH ROW EXECUTE FUNCTION jd_cat.touch()`,
		`CREATE TABLE jd_cat.events (id bigint NOT NULL, at date NOT NULL) PARTITION BY RANGE (at)`,
		`CREATE TABLE jd_cat.events_2025 PARTITION OF jd_cat.events FOR VALUES FROM ('2025-01-01') TO ('2026-01-01')`,
		// The same name in a second schema, pointing back at the first.
		`CREATE TABLE jd_cat2.users (id integer PRIMARY KEY, boss bigint UNIQUE REFERENCES jd_cat.users(id))`,
	)
}

func TestLivePostgresCatalog(t *testing.T) {
	db := liveSQL(t, DriverPostgres, "JD_TEST_POSTGRES_DSN", "postgres://jdtest:jdtest@127.0.0.1:5432/jdtest?sslmode=disable")
	postgresCatalogFixture(t, db)
	ctx := context.Background()

	t.Run("tree", func(t *testing.T) {
		catalog, err := ReadCatalog(ctx, db, DriverPostgres, CatalogOptions{Schema: "jd_cat"})
		if err != nil {
			t.Fatal(err)
		}
		if catalog.Errors != nil {
			t.Errorf("groups failed: %v", catalog.Errors)
		}
		if catalog.Schema != "jd_cat" || catalog.DefaultSchema != "public" {
			t.Errorf("schema = %q, default = %q", catalog.Schema, catalog.DefaultSchema)
		}
		var fixture, system *CatalogSchema
		for i := range catalog.Schemas {
			switch catalog.Schemas[i].Name {
			case "jd_cat":
				fixture = &catalog.Schemas[i]
			case "pg_catalog":
				system = &catalog.Schemas[i]
			}
		}
		if fixture == nil || fixture.Comment != "catalogue fixture" || fixture.Tables != 6 || fixture.System {
			t.Errorf("jd_cat = %+v", fixture)
		}
		if system == nil || !system.System {
			t.Errorf("pg_catalog = %+v", system)
		}
		for _, group := range []string{
			GroupTables, GroupViews, GroupMaterializedViews, GroupFunctions, GroupProcedures,
			GroupTriggers, GroupSequences, GroupTypes,
		} {
			if _, ok := catalog.Objects[group]; !ok {
				t.Errorf("group %s is missing", group)
			}
		}
		tables := catalog.Objects[GroupTables]
		if users := catObject(tables, "users"); users == nil || users.Comment != "People" || users.Owner == "" ||
			users.Rows == nil || users.Size == 0 {
			t.Errorf("users = %+v", users)
		}
		if parent := catObject(tables, "events"); parent == nil || parent.Detail != TableTypePartitioned {
			t.Errorf("events = %+v", parent)
		}
		if part := catObject(tables, "events_2025"); part == nil || part.Detail != TableTypePartition || part.Table != "events" {
			t.Errorf("events_2025 = %+v", part)
		}
		if catObject(catalog.Objects[GroupViews], "active_users") == nil ||
			catObject(catalog.Objects[GroupMaterializedViews], "user_counts") == nil ||
			catObject(catalog.Objects[GroupProcedures], "noop") == nil {
			t.Errorf("views = %+v, matviews = %+v, procedures = %+v", catalog.Objects[GroupViews],
				catalog.Objects[GroupMaterializedViews], catalog.Objects[GroupProcedures])
		}
		// Two functions of one name are two entries, told apart by signature.
		signatures := []string{}
		for _, f := range catalog.Objects[GroupFunctions] {
			if f.Name == "add" {
				signatures = append(signatures, f.Signature+" -> "+f.Returns)
			}
		}
		if !reflect.DeepEqual(signatures, []string{"a integer, b integer -> integer", "a numeric, b numeric -> numeric"}) {
			t.Errorf("add = %v", signatures)
		}
		if tr := catObject(catalog.Objects[GroupTriggers], "users_touch"); tr == nil || tr.Table != "users" ||
			tr.Detail != "BEFORE INSERT OR UPDATE, each row" {
			t.Errorf("trigger = %+v", tr)
		}
		if seq := catObject(catalog.Objects[GroupSequences], "posts_id_seq"); seq == nil || seq.Table != "posts.id" {
			t.Errorf("serial sequence = %+v", seq)
		}
		if seq := catObject(catalog.Objects[GroupSequences], "ticket_seq"); seq == nil || seq.Detail != "bigint, step 5" {
			t.Errorf("sequence = %+v", seq)
		}
		types := catalog.Objects[GroupTypes]
		if mood := catObject(types, "mood"); mood == nil || mood.Kind != KindEnum ||
			!reflect.DeepEqual(mood.Values, []string{"sad", "ok", "happy"}) {
			t.Errorf("enum = %+v", mood)
		}
		if dom := catObject(types, "positive"); dom == nil || dom.Kind != KindDomain || dom.Detail != "integer" {
			t.Errorf("domain = %+v", dom)
		}
		if pair := catObject(types, "pair"); pair == nil || pair.Kind != KindComposite {
			t.Errorf("composite = %+v", pair)
		}
		// The row type every table carries is not a type anyone declared.
		if catObject(types, "users") != nil {
			t.Error("a table's row type is listed as a composite type")
		}

		all, err := ReadCatalog(ctx, db, DriverPostgres, CatalogOptions{All: true})
		if err != nil || all.Schema != "" {
			t.Fatalf("all = %+v, %v", all, err)
		}
		schemas := map[string]bool{}
		for _, o := range all.Objects[GroupTables] {
			if o.Name == "users" {
				schemas[o.Schema] = true
			}
			if o.Schema == "pg_catalog" || o.Schema == "information_schema" {
				t.Fatalf("every schema includes the engine's own: %+v", o)
			}
		}
		if !schemas["jd_cat"] || !schemas["jd_cat2"] {
			t.Errorf("users found in %v", schemas)
		}
	})

	t.Run("table", func(t *testing.T) {
		users, err := DescribeTable(ctx, db, DriverPostgres, "jd_cat", "users")
		if err != nil {
			t.Fatal(err)
		}
		if users.Type != TableTypeTable || users.Comment != "People" || users.Owner == "" || users.Size == 0 ||
			users.IndexSize == 0 || users.CreateSQLSource != DefinitionGenerated {
			t.Errorf("users = %+v", users)
		}
		if c := catColumn(t, users, "id"); c.Type != "bigint" || c.Identity != "always" || c.Key != "PRI" || c.Nullable {
			t.Errorf("id = %+v", c)
		}
		if c := catColumn(t, users, "email"); c.Type != "character varying(255)" || c.Comment != "Login address" {
			t.Errorf("email = %+v", c)
		}
		// The enum by its name with its labels, not USER-DEFINED.
		if c := catColumn(t, users, "mood"); c.Type != "jd_cat.mood" || c.TypeKind != "enum" ||
			!reflect.DeepEqual(c.EnumValues, []string{"sad", "ok", "happy"}) || c.Default != "'ok'::jd_cat.mood" {
			t.Errorf("mood = %+v", c)
		}
		// The array as it was declared, not ARRAY.
		if c := catColumn(t, users, "tags"); c.Type != "text[]" || c.TypeKind != "array" {
			t.Errorf("tags = %+v", c)
		}
		if c := catColumn(t, users, "score"); c.Type != "jd_cat.positive" || c.TypeKind != "domain" {
			t.Errorf("score = %+v", c)
		}
		if c := catColumn(t, users, "shout"); c.Generated != "upper(name)" || c.GeneratedKind != "stored" || c.Default != "" {
			t.Errorf("shout = %+v", c)
		}
		if check := catConstraint(users, "users_name_len"); check == nil || check.Type != ConstraintCheck ||
			check.Definition != "CHECK (char_length(name) < 100)" || !reflect.DeepEqual(check.Columns, []string{"name"}) {
			t.Errorf("check = %+v", check)
		}
		if unique := catConstraint(users, "users_email_key"); unique == nil || unique.Type != ConstraintUnique ||
			unique.Definition != "UNIQUE (email)" {
			t.Errorf("unique = %+v", unique)
		}
		// Incoming keys, from both schemas.
		incoming := map[string]IncomingForeignKey{}
		for _, in := range users.ReferencedBy {
			incoming[TableKey(in.Schema, in.Table)] = in
		}
		if in := incoming["jd_cat.posts"]; in.OnDelete != "CASCADE" || !reflect.DeepEqual(in.Columns, []string{"author_id"}) ||
			!reflect.DeepEqual(in.RefColumns, []string{"id"}) {
			t.Errorf("posts -> users = %+v", in)
		}
		if _, ok := incoming["jd_cat2.users"]; !ok || len(incoming) != 2 {
			t.Errorf("referencedBy = %+v", users.ReferencedBy)
		}
		for _, want := range []string{
			`"id" bigint GENERATED ALWAYS AS IDENTITY NOT NULL`, `"tags" text[] NOT NULL DEFAULT '{}'::text[]`,
			`"shout" text GENERATED ALWAYS AS (upper(name)) STORED`,
			`CONSTRAINT "users_pkey" PRIMARY KEY ("id")`, `CONSTRAINT "users_email_key" UNIQUE (email)`,
			`CONSTRAINT "users_name_len" CHECK (char_length(name) < 100)`,
			`COMMENT ON TABLE "jd_cat"."users" IS 'People'`,
		} {
			if !strings.Contains(users.CreateSQL, want) {
				t.Errorf("the definition is missing %q in:\n%s", want, users.CreateSQL)
			}
		}

		posts, err := DescribeTable(ctx, db, DriverPostgres, "jd_cat", "posts")
		if err != nil {
			t.Fatal(err)
		}
		if ix := catIndex(posts, "posts_pkey"); ix == nil || !ix.Primary || ix.Constraint != "posts_pkey" || ix.Method != "btree" || ix.Size == 0 {
			t.Errorf("primary index = %+v", ix)
		}
		if ix := catIndex(posts, "posts_lower_title_idx"); ix == nil || !ix.Expression ||
			!reflect.DeepEqual(ix.Columns, []string{"lower(title)"}) {
			t.Errorf("expression index = %+v", ix)
		}
		// INCLUDE columns are stored, not part of the key.
		if ix := catIndex(posts, "posts_published_title"); ix == nil || !ix.Unique || ix.Predicate != "published" ||
			!reflect.DeepEqual(ix.Columns, []string{"title"}) || !reflect.DeepEqual(ix.Include, []string{"body"}) ||
			!strings.Contains(ix.Definition, "INCLUDE (body) WHERE published") {
			t.Errorf("partial index = %+v", ix)
		}
		for _, want := range []string{
			"CREATE INDEX posts_lower_title_idx ON jd_cat.posts USING btree (lower(title));",
			`CONSTRAINT "posts_author_id_fkey" FOREIGN KEY ("author_id") REFERENCES "jd_cat"."users" ("id") ON DELETE CASCADE`,
		} {
			if !strings.Contains(posts.CreateSQL, want) {
				t.Errorf("the definition is missing %q in:\n%s", want, posts.CreateSQL)
			}
		}
		if strings.Contains(posts.CreateSQL, "CREATE UNIQUE INDEX posts_pkey") {
			t.Errorf("the primary key's index is created twice:\n%s", posts.CreateSQL)
		}

		// A materialized view has columns, which information_schema denies.
		counts, err := DescribeTable(ctx, db, DriverPostgres, "jd_cat", "user_counts")
		if err != nil || counts.Type != TableTypeMaterializedView || len(counts.Columns) != 2 ||
			!strings.HasPrefix(counts.CreateSQL, `CREATE MATERIALIZED VIEW "jd_cat"."user_counts" AS`) {
			t.Errorf("materialized view = %+v, %v", counts, err)
		}
		view, err := DescribeTable(ctx, db, DriverPostgres, "jd_cat", "active_users")
		if err != nil || view.Type != TableTypeView || view.Rows != -1 || view.CreateSQLSource != DefinitionFromEngine ||
			!strings.HasPrefix(view.CreateSQL, `CREATE OR REPLACE VIEW "jd_cat"."active_users" AS`) {
			t.Errorf("view = %+v, %v", view, err)
		}
		events, err := DescribeTable(ctx, db, DriverPostgres, "jd_cat", "events")
		if err != nil || events.Type != TableTypePartitioned || catFact(events.Facts, factPartitionKey) != "RANGE (at)" ||
			!strings.Contains(events.CreateSQL, ") PARTITION BY RANGE (at);") {
			t.Errorf("partitioned table = %+v, %v", events, err)
		}
		part, err := DescribeTable(ctx, db, DriverPostgres, "jd_cat", "events_2025")
		if err != nil || part.Type != TableTypePartition || catFact(part.Facts, "Partition of") != "jd_cat.events" {
			t.Errorf("partition = %+v, %v", part, err)
		}

		// The replay form a dump uses leaves out what a row insert cannot
		// satisfy, and keeps the constraints and indexes it used to lose.
		replay, err := Detail(ctx, db, DriverPostgres, "jd_cat", "users")
		if err != nil || strings.Contains(replay.CreateSQL, "IDENTITY") || strings.Contains(replay.CreateSQL, "GENERATED") ||
			!strings.Contains(replay.CreateSQL, `CONSTRAINT "users_email_key" UNIQUE (email)`) {
			t.Errorf("replay form:\n%s\n%v", replay.CreateSQL, err)
		}
	})

	t.Run("definitions", func(t *testing.T) {
		define := func(ref ObjectRef) *ObjectDefinition {
			t.Helper()
			ref.Schema = "jd_cat"
			def, err := ReadObjectDefinition(ctx, db, DriverPostgres, ref)
			if err != nil {
				t.Fatalf("%+v: %v", ref, err)
			}
			return def
		}
		if def := define(ObjectRef{Kind: KindView, Name: "active_users"}); def.Source != DefinitionFromEngine ||
			!strings.Contains(def.Definition, "FROM jd_cat.users") || def.Owner == "" {
			t.Errorf("view = %+v", def)
		}
		if def := define(ObjectRef{Kind: KindMaterializedView, Name: "user_counts"}); !strings.HasSuffix(def.Definition, "WITH DATA;") ||
			catFact(def.Details, "Populated") != "yes" {
			t.Errorf("materialized view = %+v", def)
		}
		fn := define(ObjectRef{Kind: KindFunction, Name: "add", Signature: "a numeric, b numeric"})
		if !strings.HasPrefix(fn.Definition, "CREATE OR REPLACE FUNCTION jd_cat.add(a numeric, b numeric)") ||
			fn.Returns != "numeric" || fn.Language != "sql" || catFact(fn.Details, "Volatility") != "immutable" {
			t.Errorf("function = %+v", fn)
		}
		if def := define(ObjectRef{Kind: KindProcedure, Name: "noop"}); !strings.HasPrefix(def.Definition, "CREATE OR REPLACE PROCEDURE jd_cat.noop()") {
			t.Errorf("procedure = %+v", def)
		}
		if def := define(ObjectRef{Kind: KindFunction, Name: "touch"}); !strings.Contains(def.Definition, "RETURN NEW") {
			t.Errorf("trigger function = %+v", def)
		}
		if def := define(ObjectRef{Kind: KindTrigger, Name: "users_touch"}); def.Table != "users" ||
			!strings.HasPrefix(def.Definition, "CREATE TRIGGER users_touch BEFORE INSERT OR UPDATE ON jd_cat.users") ||
			catFact(def.Details, "Function") != "jd_cat.touch" || catFact(def.Details, "State") != "enabled" {
			t.Errorf("trigger = %+v", def)
		}
		seq := define(ObjectRef{Kind: KindSequence, Name: "ticket_seq"})
		if seq.Source != DefinitionGenerated || !strings.Contains(seq.Definition, "INCREMENT BY 5") ||
			!strings.Contains(seq.Definition, "START WITH 100") || catFact(seq.Details, "Last value") != "not used yet" {
			t.Errorf("sequence = %+v", seq)
		}
		if owned := define(ObjectRef{Kind: KindSequence, Name: "posts_id_seq"}); owned.Table != "posts.id" ||
			!strings.Contains(owned.Definition, `OWNED BY "jd_cat"."posts"."id"`) {
			t.Errorf("owned sequence = %+v", owned)
		}
		if enum := define(ObjectRef{Kind: KindEnum, Name: "mood"}); !reflect.DeepEqual(enum.Values, []string{"sad", "ok", "happy"}) ||
			enum.Definition != "CREATE TYPE \"jd_cat\".\"mood\" AS ENUM (\n  'sad',\n  'ok',\n  'happy'\n);" {
			t.Errorf("enum = %+v", enum)
		}
		if dom := define(ObjectRef{Kind: KindDomain, Name: "positive"}); !strings.Contains(dom.Definition, "AS integer") ||
			!strings.Contains(dom.Definition, "CHECK (VALUE > 0)") {
			t.Errorf("domain = %+v", dom)
		}
		if comp := define(ObjectRef{Kind: KindComposite, Name: "pair"}); !strings.Contains(comp.Definition, "a integer,\n  b text") {
			t.Errorf("composite = %+v", comp)
		}
		if table := define(ObjectRef{Kind: KindTable, Name: "posts"}); table.Source != DefinitionGenerated ||
			!strings.Contains(table.Definition, "CREATE INDEX posts_author_idx") {
			t.Errorf("table = %+v", table)
		}

		for _, c := range []struct {
			ref  ObjectRef
			want error
		}{
			// One name, two functions: a signature is needed to say which.
			{ObjectRef{Kind: KindFunction, Name: "add"}, ErrAmbiguousObject},
			{ObjectRef{Kind: KindFunction, Name: "add", Signature: "a text"}, ErrObjectNotFound},
			{ObjectRef{Kind: KindFunction, Name: "noop"}, ErrObjectNotFound},
			{ObjectRef{Kind: KindView, Name: "users"}, ErrObjectNotFound},
			{ObjectRef{Kind: KindEnum, Name: "positive"}, ErrObjectNotFound},
			{ObjectRef{Kind: KindTable, Name: "nope"}, ErrObjectNotFound},
			{ObjectRef{Kind: KindEvent, Name: "x"}, ErrNoDefinition},
		} {
			c.ref.Schema = "jd_cat"
			if _, err := ReadObjectDefinition(ctx, db, DriverPostgres, c.ref); !errors.Is(err, c.want) {
				t.Errorf("%+v: error = %v, want %v", c.ref, err, c.want)
			}
		}
	})

	// Two schemas each hold a table called `users`. Keyed by the bare name,
	// one of them took the other's place in every map and on the diagram.
	t.Run("same_name_in_two_schemas", func(t *testing.T) {
		graph, err := BuildSchemaGraph(ctx, db, DriverPostgres, "")
		if err != nil {
			t.Fatal(err)
		}
		nodes := map[string]GraphTable{}
		for _, table := range graph.Tables {
			if _, dup := nodes[table.ID]; dup {
				t.Errorf("node id %q is used twice", table.ID)
			}
			nodes[table.ID] = table
		}
		first, other := nodes["jd_cat.users"], nodes["jd_cat2.users"]
		if len(first.Columns) != 8 || len(other.Columns) != 2 {
			t.Fatalf("jd_cat.users has %d columns, jd_cat2.users has %d", len(first.Columns), len(other.Columns))
		}
		if _, ok := nodes["jd_cat.events_2025"]; ok {
			t.Error("a partition is drawn beside the table it is a partition of")
		}
		if node, ok := nodes["jd_cat.events"]; !ok || node.Type != TableTypePartitioned {
			t.Errorf("the partitioned table = %+v", node)
		}
		if node := nodes["jd_cat.user_counts"]; node.Type != TableTypeMaterializedView || len(node.Columns) != 2 {
			t.Errorf("the materialized view = %+v", node)
		}
		if tags := first.Columns[3]; tags.Name != "tags" || tags.Type != "text[]" {
			t.Errorf("tags = %+v", tags)
		}
		var crossSchema *GraphEdge
		for i := range graph.Edges {
			if graph.Edges[i].From == "jd_cat2.users" {
				crossSchema = &graph.Edges[i]
			}
		}
		// A unique referencing column makes the relation one-to-one.
		if crossSchema == nil || crossSchema.To != "jd_cat.users" || crossSchema.Cardinality != "one-to-one" {
			t.Errorf("jd_cat2.users -> jd_cat.users = %+v", crossSchema)
		}
		for _, e := range graph.Edges {
			if e.From == "jd_cat.posts" && e.Cardinality != "many-to-one" {
				t.Errorf("posts -> users = %+v", e)
			}
		}
		// title is unique only among published rows.
		for _, c := range nodes["jd_cat.posts"].Columns {
			if c.Name == "title" && c.Unique {
				t.Error("a partial unique index made title unique")
			}
		}

		relations, err := Relations(ctx, db, DriverPostgres, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(relations["jd_cat.posts"]) != 1 || len(relations["jd_cat2.users"]) != 1 || len(relations["jd_cat.users"]) != 0 {
			t.Errorf("relations = %+v", relations)
		}
		for key := range relations {
			if key == "jd_cat.events_2025" || key == "jd_cat.active_users" {
				t.Errorf("%s was asked for foreign keys", key)
			}
		}

		outline, err := Outline(ctx, db, DriverPostgres, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(outline.Tables["jd_cat.users"]) != 8 || len(outline.Tables["jd_cat2.users"]) != 2 {
			t.Errorf("outline = %v / %v", outline.Tables["jd_cat.users"], outline.Tables["jd_cat2.users"])
		}
		// A partition can be queried by name, so the editor completes it.
		if len(outline.Tables["jd_cat.events_2025"]) != 2 || len(outline.Tables["jd_cat.user_counts"]) != 2 {
			t.Errorf("outline partition = %v, matview = %v", outline.Tables["jd_cat.events_2025"], outline.Tables["jd_cat.user_counts"])
		}

		tables, err := ListTables(ctx, db, DriverPostgres, "jd_cat")
		if err != nil {
			t.Fatal(err)
		}
		types := map[string]string{}
		for _, table := range tables {
			types[table.Name] = table.Type
		}
		if types["events"] != TableTypePartitioned || types["events_2025"] != TableTypePartition ||
			types["user_counts"] != TableTypeMaterializedView || types["users"] != TableTypeTable {
			t.Errorf("table types = %v", types)
		}
	})

	// information_schema shows a constraint only to a login that owns the
	// table or holds more than SELECT on it, so a read-only account saw every
	// table as keyless.
	t.Run("read_only_login_sees_the_primary_key", func(t *testing.T) {
		const role = "jdcc_b2b_reader"
		dropRole := []string{
			`DROP OWNED BY ` + role, `DROP ROLE IF EXISTS ` + role,
		}
		for _, s := range dropRole {
			_, _ = db.ExecContext(ctx, s)
		}
		t.Cleanup(func() {
			for _, s := range dropRole {
				_, _ = db.ExecContext(context.Background(), s)
			}
		})
		catalogExec(t, db,
			`CREATE ROLE `+role+` LOGIN PASSWORD 'reader'`,
			`GRANT USAGE ON SCHEMA jd_cat TO `+role,
			`GRANT SELECT ON ALL TABLES IN SCHEMA jd_cat TO `+role,
		)
		info, err := ParseDSN(DriverPostgres, liveDSN(t, "JD_TEST_POSTGRES_DSN", ""))
		if err != nil {
			t.Fatal(err)
		}
		reader, err := sql.Open("pgx", fmt.Sprintf("postgres://%s:reader@%s:%s/%s?sslmode=disable",
			role, info.Host, info.Port, info.Database))
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		if err := reader.PingContext(ctx); err != nil {
			t.Skipf("the read-only login cannot connect: %v", err)
		}
		detail, err := DescribeTable(ctx, reader, DriverPostgres, "jd_cat", "posts")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(detail.PrimaryKey, []string{"id"}) || catColumn(t, detail, "id").Key != "PRI" {
			t.Errorf("primary key as a read-only login = %v", detail.PrimaryKey)
		}
		graph, err := BuildSchemaGraph(ctx, reader, DriverPostgres, "jd_cat")
		if err != nil {
			t.Fatal(err)
		}
		for _, table := range graph.Tables {
			if table.Name == "posts" && !table.Columns[0].PrimaryKey {
				t.Errorf("the diagram shows posts.id without its key: %+v", table.Columns[0])
			}
		}
	})
}

func TestLivePostgresStructureChanges(t *testing.T) {
	db := liveSQL(t, DriverPostgres, "JD_TEST_POSTGRES_DSN", "postgres://jdtest:jdtest@127.0.0.1:5432/jdtest?sslmode=disable")
	ctx := context.Background()
	const d = DriverPostgres
	drop := []string{`DROP SCHEMA IF EXISTS jd_ddl CASCADE`, `DROP SCHEMA IF EXISTS jd_ddl_made CASCADE`}
	catalogExec(t, db, drop...)
	t.Cleanup(func() {
		for _, s := range drop {
			_, _ = db.ExecContext(context.Background(), s)
		}
	})
	catalogExec(t, db,
		`CREATE SCHEMA jd_ddl`,
		`CREATE TABLE jd_ddl.owners (id integer PRIMARY KEY, name text)`,
		`CREATE TABLE jd_ddl.items (id integer PRIMARY KEY, owner_id integer, qty text, sku text, note text DEFAULT 'none')`,
		`INSERT INTO jd_ddl.owners VALUES (1, 'Ann')`,
		`INSERT INTO jd_ddl.items VALUES (1, 1, '12', 'a-1', NULL), (2, 1, '7', 'a-2', 'x')`,
	)
	run := runPlan(t, db)
	describe := func(table string) *TableDetail {
		t.Helper()
		detail, err := DescribeTable(ctx, db, d, "jd_ddl", table)
		if err != nil {
			t.Fatal(err)
		}
		return detail
	}

	t.Run("alter_column", func(t *testing.T) {
		// text does not cast to integer by itself, which is what USING is for.
		plan, err := PlanAlterColumn(ctx, db, d, ColumnChange{Schema: "jd_ddl", Table: "items", Column: "qty", Type: "integer"})
		if err != nil {
			t.Fatal(err)
		}
		if err := plan.Exec(ctx, db); err == nil {
			t.Fatal("text became integer with no conversion")
		}
		run(PlanAlterColumn(ctx, db, d, ColumnChange{
			Schema: "jd_ddl", Table: "items", Column: "qty", Type: "integer", Using: "qty::integer",
			Nullable: ddlBool(false), Default: ddlString("0"),
		}))
		if c := catColumn(t, describe("items"), "qty"); c.Type != "integer" || c.Nullable || c.Default != "0" {
			t.Errorf("qty after the change = %+v", c)
		}
		var total int
		if err := db.QueryRowContext(ctx, `SELECT sum(qty) FROM jd_ddl.items`).Scan(&total); err != nil || total != 19 {
			t.Errorf("the values did not survive the conversion: %d, %v", total, err)
		}
		run(PlanAlterColumn(ctx, db, d, ColumnChange{Schema: "jd_ddl", Table: "items", Column: "note", Nullable: ddlBool(true), DropDefault: true}))
		if c := catColumn(t, describe("items"), "note"); c.Default != "" || !c.Nullable {
			t.Errorf("note after dropping its default = %+v", c)
		}
		// One statement: a NOT NULL the rows cannot satisfy leaves the type
		// change undone as well.
		plan, err = PlanAlterColumn(ctx, db, d, ColumnChange{
			Schema: "jd_ddl", Table: "items", Column: "note", Type: "varchar(50)", Nullable: ddlBool(false),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := plan.Exec(ctx, db); err == nil {
			t.Fatal("NOT NULL was set over a NULL")
		}
		if c := catColumn(t, describe("items"), "note"); c.Type != "text" {
			t.Errorf("a failed change left the type altered: %+v", c)
		}
	})

	t.Run("foreign_key", func(t *testing.T) {
		stmt := run(PlanAddForeignKey(d, ForeignKeySpec{
			Schema: "jd_ddl", Table: "items", Columns: []string{"owner_id"},
			RefTable: "owners", RefColumns: []string{"id"}, OnDelete: "set null",
		}))
		items := describe("items")
		if len(items.ForeignKeys) != 1 || items.ForeignKeys[0].Name != "items_owner_id_fkey" ||
			items.ForeignKeys[0].OnDelete != "SET NULL" || items.ForeignKeys[0].RefSchema != "jd_ddl" {
			t.Errorf("after %s: %+v", stmt, items.ForeignKeys)
		}
		if owners := describe("owners"); len(owners.ReferencedBy) != 1 || owners.ReferencedBy[0].Table != "items" {
			t.Errorf("owners.referencedBy = %+v", owners.ReferencedBy)
		}
		run(PlanDropForeignKey(d, "jd_ddl", "items", "items_owner_id_fkey"))
		if items := describe("items"); len(items.ForeignKeys) != 0 {
			t.Errorf("the dropped key is still there: %+v", items.ForeignKeys)
		}
	})

	t.Run("constraints", func(t *testing.T) {
		run(PlanAddConstraint(d, ConstraintSpec{Schema: "jd_ddl", Table: "items", Type: "unique", Columns: []string{"sku"}}))
		run(PlanAddConstraint(d, ConstraintSpec{
			Schema: "jd_ddl", Table: "items", Type: "check", Name: "items_sku_shape", Expression: "sku LIKE 'a-%' AND length(sku) < 20",
		}))
		items := describe("items")
		if c := catConstraint(items, "items_sku_key"); c == nil || c.Type != ConstraintUnique {
			t.Errorf("unique = %+v in %+v", c, items.Constraints)
		}
		if c := catConstraint(items, "items_sku_shape"); c == nil || c.Type != ConstraintCheck || !strings.Contains(c.Definition, "length(sku) < 20") {
			t.Errorf("check = %+v", c)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO jd_ddl.items(id, qty, sku) VALUES (9, 1, 'b-1')`); err == nil {
			t.Error("the check constraint is not enforced")
		}
		run(PlanDropConstraint(ctx, db, d, "jd_ddl", "items", "items_sku_shape", ""))
		run(PlanDropConstraint(ctx, db, d, "jd_ddl", "items", "items_sku_key", "unique"))
		if items := describe("items"); len(items.Constraints) != 0 {
			t.Errorf("dropped constraints are still there: %+v", items.Constraints)
		}
	})

	t.Run("indexes", func(t *testing.T) {
		stmt := run(PlanCreateIndex(ctx, db, d, IndexSpec{
			Schema: "jd_ddl", Table: "items", Name: "items_sku_live", Columns: []string{"sku"},
			Unique: true, Method: "btree", Where: "qty > 0", IfNotExists: true, Concurrently: true,
		}))
		if !strings.Contains(stmt, "CONCURRENTLY IF NOT EXISTS") {
			t.Errorf("statement = %s", stmt)
		}
		ix := catIndex(describe("items"), "items_sku_live")
		if ix == nil || !ix.Unique || ix.Method != "btree" || ix.Predicate != "(qty > 0)" || ix.Invalid {
			t.Errorf("created index = %+v", ix)
		}
		run(PlanCreateIndex(ctx, db, d, IndexSpec{Schema: "jd_ddl", Table: "items", Columns: []string{"note"}, Method: "hash"}))
		if ix := catIndex(describe("items"), "items_note_idx"); ix == nil || ix.Method != "hash" {
			t.Errorf("hash index = %+v", ix)
		}
		run(PlanDropIndex(d, "jd_ddl", "items", "items_sku_live"))
		if catIndex(describe("items"), "items_sku_live") != nil {
			t.Error("the dropped index is still there")
		}
	})

	t.Run("views", func(t *testing.T) {
		run(PlanCreateView(d, ViewSpec{Schema: "jd_ddl", Name: "busy", Query: "SELECT id, qty FROM jd_ddl.items WHERE qty > 5"}))
		run(PlanCreateView(d, ViewSpec{Schema: "jd_ddl", Name: "busy", Query: "SELECT id, qty, sku FROM jd_ddl.items WHERE qty > 5", Replace: true}))
		if view := describe("busy"); view.Type != TableTypeView || len(view.Columns) != 3 {
			t.Errorf("replaced view = %+v", view)
		}
		run(PlanCreateView(d, ViewSpec{Schema: "jd_ddl", Name: "totals", Query: "SELECT owner_id, sum(qty) AS qty FROM jd_ddl.items GROUP BY owner_id", Materialized: true}))
		if view := describe("totals"); view.Type != TableTypeMaterializedView {
			t.Errorf("materialized view = %+v", view)
		}
		run(PlanDropView(d, "jd_ddl", "totals", true))
		run(PlanDropView(d, "jd_ddl", "busy", false))
		if view := describe("busy"); view.Type != "" || len(view.Columns) != 0 {
			t.Errorf("the dropped view is still there: %+v", view)
		}
	})

	t.Run("comments", func(t *testing.T) {
		run(PlanComment(ctx, db, d, CommentSpec{Schema: "jd_ddl", Table: "items", Comment: `What's in stock \ on hand`}))
		run(PlanComment(ctx, db, d, CommentSpec{Schema: "jd_ddl", Table: "items", Column: "sku", Comment: "Stock-keeping unit"}))
		items := describe("items")
		if items.Comment != `What's in stock \ on hand` || catColumn(t, items, "sku").Comment != "Stock-keeping unit" {
			t.Errorf("comments = %q / %q", items.Comment, catColumn(t, items, "sku").Comment)
		}
		run(PlanComment(ctx, db, d, CommentSpec{Schema: "jd_ddl", Table: "items", Column: "sku"}))
		if c := catColumn(t, describe("items"), "sku"); c.Comment != "" {
			t.Errorf("an empty comment did not remove it: %q", c.Comment)
		}
	})

	t.Run("enum_types", func(t *testing.T) {
		run(PlanCreateEnum(d, "jd_ddl", "state", []string{"new", "it's done"}))
		run(PlanAddEnumValue(d, EnumValueSpec{Schema: "jd_ddl", Name: "state", Value: "doing", Before: "it's done"}))
		run(PlanAddEnumValue(d, EnumValueSpec{Schema: "jd_ddl", Name: "state", Value: "doing", IfNotExists: true}))
		def, err := ReadObjectDefinition(ctx, db, d, ObjectRef{Kind: KindEnum, Schema: "jd_ddl", Name: "state"})
		if err != nil || !reflect.DeepEqual(def.Values, []string{"new", "doing", "it's done"}) {
			t.Errorf("enum = %+v, %v", def, err)
		}
		run(PlanAddColumn(d, "jd_ddl", "items", NewColumn{Name: "state", Type: "jd_ddl.state", Default: "'new'"}))
		if c := catColumn(t, describe("items"), "state"); c.TypeKind != "enum" || len(c.EnumValues) != 3 {
			t.Errorf("enum column = %+v", c)
		}
	})

	t.Run("schemas", func(t *testing.T) {
		run(PlanCreateSchema(d, "jd_ddl_made"))
		catalogExec(t, db, `CREATE TABLE jd_ddl_made.t (id integer)`)
		// Never CASCADE: a schema that still holds a table is refused.
		plan, err := PlanDropSchema(d, "jd_ddl_made")
		if err != nil {
			t.Fatal(err)
		}
		if err := plan.Exec(ctx, db); err == nil {
			t.Fatal("a schema holding a table was dropped")
		}
		catalogExec(t, db, `DROP TABLE jd_ddl_made.t`)
		run(PlanDropSchema(d, "jd_ddl_made"))
		catalog, err := ReadCatalog(ctx, db, d, CatalogOptions{Schema: "jd_ddl"})
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range catalog.Schemas {
			if s.Name == "jd_ddl_made" {
				t.Error("the dropped schema is still listed")
			}
		}
	})

	t.Run("rename_and_drop_column", func(t *testing.T) {
		run(PlanRenameColumn(d, "jd_ddl", "items", "note", "remark"))
		run(PlanDropColumn(ctx, db, d, "jd_ddl", "items", "remark"))
		run(PlanRenameTable(d, "jd_ddl", "items", "stock"))
		if stock := describe("stock"); stock.Type != TableTypeTable || len(stock.Columns) == 0 {
			t.Errorf("renamed table = %+v", stock)
		}
		run(PlanRenameTable(d, "jd_ddl", "stock", "items"))
	})
}

// The generic dump replays the generated CREATE TABLE ahead of the rows. It
// has to carry the constraints and indexes a restore used to lose, and it has
// to stay insertable: an identity or generated column refuses the value the
// dump is about to give it.
func TestLivePostgresGeneratedDDLReplays(t *testing.T) {
	db := liveSQL(t, DriverPostgres, "JD_TEST_POSTGRES_DSN", "postgres://jdtest:jdtest@127.0.0.1:5432/jdtest?sslmode=disable")
	ctx := context.Background()
	catalogExec(t, db, `DROP TABLE IF EXISTS public.jd_replay`)
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), `DROP TABLE IF EXISTS public.jd_replay`) })
	catalogExec(t, db,
		`CREATE TABLE public.jd_replay (
		  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		  email text NOT NULL,
		  tags text[] NOT NULL DEFAULT '{}',
		  qty integer NOT NULL DEFAULT 0,
		  double_qty integer GENERATED ALWAYS AS (qty * 2) STORED,
		  CONSTRAINT jd_replay_email_key UNIQUE (email),
		  CONSTRAINT jd_replay_qty_check CHECK (qty >= 0))`,
		`CREATE INDEX jd_replay_tags_idx ON public.jd_replay USING gin (tags)`,
		`CREATE UNIQUE INDEX jd_replay_big_idx ON public.jd_replay (email) WHERE qty > 10`,
		`INSERT INTO public.jd_replay(email, tags, qty) VALUES ('a@x.io', '{one,two}', 3), ('b@x.io', '{}', 40)`,
	)
	// Asked for with no schema, the table is found where an unqualified name
	// resolves, and every fact about it is read from there — not only the ones
	// whose query happens to default the schema.
	unqualified, err := DescribeTable(ctx, db, DriverPostgres, "", "jd_replay")
	if err != nil || unqualified.Schema != "public" || !reflect.DeepEqual(unqualified.PrimaryKey, []string{"id"}) ||
		len(unqualified.Constraints) != 2 || len(unqualified.Indexes) != 4 ||
		!strings.HasPrefix(unqualified.CreateSQL, `CREATE TABLE "public"."jd_replay"`) {
		t.Errorf("without a schema = %+v, %v", unqualified, err)
	}
	detail, err := Detail(ctx, db, DriverPostgres, "public", "jd_replay")
	if err != nil {
		t.Fatal(err)
	}
	// The generated form is one statement per paragraph.
	statements := strings.Split(strings.TrimSuffix(detail.CreateSQL, ";"), ";\n\n")
	if len(statements) != 3 {
		t.Fatalf("the replay form is %d statements, want the table and two indexes:\n%s", len(statements), detail.CreateSQL)
	}
	catalogExec(t, db, `ALTER TABLE public.jd_replay RENAME TO jd_replay_old`,
		`ALTER TABLE public.jd_replay_old RENAME CONSTRAINT jd_replay_pkey TO jd_replay_old_pkey`,
		`ALTER TABLE public.jd_replay_old RENAME CONSTRAINT jd_replay_email_key TO jd_replay_old_email_key`,
		`ALTER INDEX public.jd_replay_tags_idx RENAME TO jd_replay_old_tags_idx`,
		`ALTER INDEX public.jd_replay_big_idx RENAME TO jd_replay_old_big_idx`)
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), `DROP TABLE IF EXISTS public.jd_replay_old`) })
	catalogExec(t, db, statements...)
	// Every column's stored value goes back in, as a dump's INSERT writes it.
	catalogExec(t, db, `INSERT INTO public.jd_replay SELECT * FROM public.jd_replay_old`)

	restored, err := DescribeTable(ctx, db, DriverPostgres, "public", "jd_replay")
	if err != nil {
		t.Fatal(err)
	}
	if catConstraint(restored, "jd_replay_email_key") == nil || catConstraint(restored, "jd_replay_qty_check") == nil {
		t.Errorf("constraints after the replay = %+v", restored.Constraints)
	}
	if ix := catIndex(restored, "jd_replay_tags_idx"); ix == nil || ix.Method != "gin" {
		t.Errorf("the gin index after the replay = %+v", ix)
	}
	if ix := catIndex(restored, "jd_replay_big_idx"); ix == nil || !ix.Unique || ix.Predicate == "" {
		t.Errorf("the partial index after the replay = %+v", ix)
	}
	if c := catColumn(t, restored, "tags"); c.Type != "text[]" {
		t.Errorf("tags after the replay = %+v", c)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM public.jd_replay WHERE double_qty = qty * 2`).Scan(&n); err != nil || n != 2 {
		t.Errorf("rows after the replay = %d, %v", n, err)
	}
}

// mysqlFlavours are the two products behind the one driver. MariaDB is the
// fixture every MySQL live test uses; MySQL itself is reached through its own
// variable and skipped without it.
var mysqlFlavours = []struct{ name, env, adminEnv string }{
	{"mariadb", "JD_TEST_MYSQL_DSN", "JD_TEST_MYSQL_ADMIN_DSN"},
	{"mysql", "JD_TEST_MYSQL8_DSN", "JD_TEST_MYSQL8_ADMIN_DSN"},
}

func TestLiveMySQLCatalogAndStructureChanges(t *testing.T) {
	for _, flavour := range mysqlFlavours {
		t.Run(flavour.name, func(t *testing.T) {
			if os.Getenv(flavour.env) == "" {
				t.Skipf("set %s to run these", flavour.env)
			}
			db := liveSQL(t, DriverMySQL, flavour.env, "")
			ctx := context.Background()
			const d = DriverMySQL
			maria := isMariaDB(ctx, db)
			if maria != (flavour.name == "mariadb") {
				t.Fatalf("%s points at the other product (MariaDB: %v)", flavour.env, maria)
			}
			info, err := ParseDSN(d, os.Getenv(flavour.env))
			if err != nil || info.Database == "" {
				t.Fatalf("the DSN names no database: %v", err)
			}
			schema := info.Database
			drop := []string{
				`DROP VIEW IF EXISTS jd_cat_active`, `DROP VIEW IF EXISTS jd_cat_titles`,
				`DROP PROCEDURE IF EXISTS jd_cat_noop`, `DROP EVENT IF EXISTS jd_cat_tick`,
				`DROP TABLE IF EXISTS jd_cat_posts`, `DROP TABLE IF EXISTS jd_cat_articles`, `DROP TABLE IF EXISTS jd_cat_users`,
				`DROP TABLE IF EXISTS jd_cat_counters`,
			}
			if maria {
				drop = append(drop, `DROP SEQUENCE IF EXISTS jd_cat_seq`)
			}
			catalogExec(t, db, drop...)
			t.Cleanup(func() {
				for _, s := range drop {
					_, _ = db.ExecContext(context.Background(), s)
				}
			})
			catalogExec(t, db,
				`CREATE TABLE jd_cat_users (
				  id BIGINT AUTO_INCREMENT PRIMARY KEY,
				  email VARCHAR(255) NOT NULL COMMENT 'Login address',
				  mood ENUM('sad','ok','it''s') NOT NULL DEFAULT 'ok',
				  name VARCHAR(100),
				  shout VARCHAR(100) GENERATED ALWAYS AS (UPPER(name)) STORED,
				  score INT DEFAULT 5,
				  created TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
				  UNIQUE KEY jd_cat_users_email (email),
				  CONSTRAINT jd_cat_score_positive CHECK (score > 0)
				) COMMENT='People'`,
				`CREATE TABLE jd_cat_posts (
				  id INT PRIMARY KEY, author_id BIGINT NOT NULL, title VARCHAR(255) NOT NULL, body TEXT,
				  CONSTRAINT jd_cat_posts_author FOREIGN KEY (author_id) REFERENCES jd_cat_users(id) ON DELETE CASCADE,
				  KEY jd_cat_posts_title (title))`,
				`CREATE TABLE jd_cat_counters (id INT AUTO_INCREMENT PRIMARY KEY, n INT)`,
				`CREATE VIEW jd_cat_active AS SELECT id, email FROM jd_cat_users WHERE name IS NOT NULL`,
				`CREATE PROCEDURE jd_cat_noop(IN x INT, OUT y INT) SET y = x`,
				`CREATE EVENT jd_cat_tick ON SCHEDULE EVERY 1 DAY DISABLE DO SELECT 1`,
			)
			if maria {
				catalogExec(t, db, `CREATE SEQUENCE jd_cat_seq START WITH 100 INCREMENT BY 5`)
			}
			// A function and a trigger need SUPER on a MySQL that keeps a
			// binary log, so they are made by the administrator where one is
			// given and their assertions are skipped where the server refuses.
			privileged := db
			if adminDSN := os.Getenv(flavour.adminEnv); adminDSN != "" {
				if admin, err := sql.Open("mysql", adminDSN+schema); err == nil && admin.PingContext(ctx) == nil {
					t.Cleanup(func() { admin.Close() })
					privileged = admin
				}
			}
			routines := true
			for _, s := range []string{
				`DROP FUNCTION IF EXISTS jd_cat_add`,
				`CREATE FUNCTION jd_cat_add(a INT, b INT) RETURNS INT DETERMINISTIC RETURN a + b`,
				`CREATE TRIGGER jd_cat_touch BEFORE INSERT ON jd_cat_users FOR EACH ROW SET NEW.name = NEW.name`,
			} {
				if _, err := privileged.ExecContext(ctx, s); err != nil {
					t.Logf("routines are not asserted on this server: %v", err)
					routines = false
					break
				}
			}
			t.Cleanup(func() { _, _ = privileged.ExecContext(context.Background(), `DROP FUNCTION IF EXISTS jd_cat_add`) })
			run := runPlan(t, db)
			describe := func(table string) *TableDetail {
				t.Helper()
				detail, err := DescribeTable(ctx, db, d, schema, table)
				if err != nil {
					t.Fatal(err)
				}
				return detail
			}

			t.Run("tree", func(t *testing.T) {
				// No schema named: the database the connection is in.
				catalog, err := ReadCatalog(ctx, db, d, CatalogOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if catalog.Errors != nil {
					t.Errorf("groups failed: %v", catalog.Errors)
				}
				if catalog.Schema != schema || catalog.DefaultSchema != schema {
					t.Errorf("schema = %q, default = %q, want %q", catalog.Schema, catalog.DefaultSchema, schema)
				}
				groups := []string{}
				for group := range catalog.Objects {
					groups = append(groups, group)
				}
				if advertised := CatalogGroups(d, flavour.name); len(advertised) != len(groups) {
					t.Errorf("%s advertises %v and read %v", flavour.name, advertised, groups)
				}
				// MySQL has no sequences and no free-standing types, and the
				// tree must not draw an empty branch for either.
				if _, ok := catalog.Objects[GroupTypes]; ok {
					t.Error("MySQL lists a types group")
				}
				if _, ok := catalog.Objects[GroupSequences]; ok != maria {
					t.Errorf("sequences group present = %v on %s", ok, flavour.name)
				}
				if users := catObject(catalog.Objects[GroupTables], "jd_cat_users"); users == nil ||
					users.Comment != "People" || users.Detail != "InnoDB" || users.Rows == nil {
					t.Errorf("users = %+v", users)
				}
				if catObject(catalog.Objects[GroupTables], "jd_cat_active") != nil {
					t.Error("a view is listed among the tables")
				}
				if view := catObject(catalog.Objects[GroupViews], "jd_cat_active"); view == nil || view.Detail != "updatable" {
					t.Errorf("view = %+v", view)
				}
				if proc := catObject(catalog.Objects[GroupProcedures], "jd_cat_noop"); proc == nil ||
					!strings.Contains(proc.Signature, "OUT y int") {
					t.Errorf("procedure = %+v", proc)
				}
				if event := catObject(catalog.Objects[GroupEvents], "jd_cat_tick"); event == nil || event.Detail != "every 1 day, disabled" {
					t.Errorf("event = %+v", event)
				}
				if routines {
					if fn := catObject(catalog.Objects[GroupFunctions], "jd_cat_add"); fn == nil ||
						!strings.HasPrefix(fn.Signature, "a int") || !strings.HasPrefix(fn.Returns, "int") {
						t.Errorf("function = %+v", fn)
					}
					if tr := catObject(catalog.Objects[GroupTriggers], "jd_cat_touch"); tr == nil ||
						tr.Table != "jd_cat_users" || tr.Detail != "BEFORE INSERT, each row" {
						t.Errorf("trigger = %+v", tr)
					}
				}
				if maria && catObject(catalog.Objects[GroupSequences], "jd_cat_seq") == nil {
					t.Errorf("sequences = %+v", catalog.Objects[GroupSequences])
				}
				var own *CatalogSchema
				for i := range catalog.Schemas {
					if catalog.Schemas[i].Name == schema {
						own = &catalog.Schemas[i]
					}
					if catalog.Schemas[i].Name == "information_schema" && !catalog.Schemas[i].System {
						t.Error("information_schema is not marked as the engine's own")
					}
				}
				if own == nil || !own.Default || own.Tables < 4 || !strings.HasPrefix(own.Detail, "utf8") {
					t.Errorf("own schema = %+v", own)
				}
			})

			t.Run("table", func(t *testing.T) {
				users := describe("jd_cat_users")
				if users.Type != TableTypeTable || users.Comment != "People" || catFact(users.Facts, "Engine") != "InnoDB" ||
					users.CreateSQLSource != DefinitionFromEngine || !strings.HasPrefix(users.CreateSQL, "CREATE TABLE `jd_cat_users`") {
					t.Errorf("users = %+v", users)
				}
				if c := catColumn(t, users, "id"); c.Identity != "auto_increment" || c.Key != "PRI" {
					t.Errorf("id = %+v", c)
				}
				if c := catColumn(t, users, "email"); c.Comment != "Login address" || c.Default != "" {
					t.Errorf("email = %+v", c)
				}
				// The same SQL text on both products, though one stores `ok`
				// and the other `'ok'`.
				if c := catColumn(t, users, "mood"); c.TypeKind != "enum" || c.Default != "'ok'" ||
					!reflect.DeepEqual(c.EnumValues, []string{"sad", "ok", "it's"}) {
					t.Errorf("mood = %+v", c)
				}
				if c := catColumn(t, users, "name"); c.Default != "" || !c.Nullable {
					t.Errorf("name = %+v", c)
				}
				if c := catColumn(t, users, "score"); c.Default != "5" {
					t.Errorf("score = %+v", c)
				}
				if c := catColumn(t, users, "shout"); c.GeneratedKind != "stored" || !strings.Contains(strings.ToLower(c.Generated), "name") || c.Default != "" {
					t.Errorf("shout = %+v", c)
				}
				if c := catConstraint(users, "jd_cat_score_positive"); c == nil || c.Type != ConstraintCheck || !strings.Contains(c.Definition, "score") {
					t.Errorf("check = %+v in %+v", c, users.Constraints)
				}
				if c := catConstraint(users, "jd_cat_users_email"); c == nil || c.Type != ConstraintUnique || c.Definition != "UNIQUE (`email`)" {
					t.Errorf("unique = %+v", c)
				}
				if len(users.ReferencedBy) != 1 || users.ReferencedBy[0].Table != "jd_cat_posts" || users.ReferencedBy[0].OnDelete != "CASCADE" {
					t.Errorf("referencedBy = %+v", users.ReferencedBy)
				}
				if ix := catIndex(describe("jd_cat_posts"), "jd_cat_posts_title"); ix == nil || ix.Method != "BTREE" || ix.Unique {
					t.Errorf("index = %+v", ix)
				}
				// With no database named, the connection's own is read — keys
				// and all.
				unqualified, err := DescribeTable(ctx, db, d, "", "jd_cat_posts")
				if err != nil || unqualified.Schema != schema || !reflect.DeepEqual(unqualified.PrimaryKey, []string{"id"}) ||
					len(unqualified.ForeignKeys) != 1 || unqualified.ForeignKeys[0].RefSchema != schema {
					t.Errorf("without a schema = %+v, %v", unqualified, err)
				}
				// SHOW CREATE TABLE answers a view with four columns, and the
				// definition used to be lost to a two-column scan.
				if view := describe("jd_cat_active"); view.Type != TableTypeView || view.Comment != "" ||
					!strings.Contains(view.CreateSQL, "VIEW `jd_cat_active` AS") {
					t.Errorf("view = %+v", view)
				}
			})

			t.Run("definitions", func(t *testing.T) {
				for kind, want := range map[string]struct{ name, text string }{
					KindView:      {"jd_cat_active", "VIEW `jd_cat_active` AS"},
					KindProcedure: {"jd_cat_noop", "PROCEDURE `jd_cat_noop`(IN x INT, OUT y INT)"},
					KindEvent:     {"jd_cat_tick", "EVENT `jd_cat_tick` ON SCHEDULE EVERY 1 DAY"},
					KindTable:     {"jd_cat_posts", "CONSTRAINT `jd_cat_posts_author` FOREIGN KEY"},
				} {
					def, err := ReadObjectDefinition(ctx, db, d, ObjectRef{Kind: kind, Name: want.name})
					if err != nil || !strings.Contains(def.Definition, want.text) || def.Source != DefinitionFromEngine {
						t.Errorf("%s = %+v, %v", kind, def, err)
					}
				}
				if routines {
					fn, err := ReadObjectDefinition(ctx, db, d, ObjectRef{Kind: KindFunction, Name: "jd_cat_add"})
					if err != nil || fn.Language != "SQL" || catFact(fn.Details, "Deterministic") != "yes" ||
						(fn.Definition == "" && fn.Note == "") {
						t.Errorf("function = %+v, %v", fn, err)
					}
					tr, err := ReadObjectDefinition(ctx, db, d, ObjectRef{Kind: KindTrigger, Name: "jd_cat_touch"})
					if err != nil || tr.Table != "jd_cat_users" || catFact(tr.Details, "Fires") != "BEFORE INSERT, each row" {
						t.Errorf("trigger = %+v, %v", tr, err)
					}
				}
				if maria {
					def, err := ReadObjectDefinition(ctx, db, d, ObjectRef{Kind: KindSequence, Name: "jd_cat_seq"})
					if err != nil || !strings.Contains(def.Definition, "increment by 5") {
						t.Errorf("sequence = %+v, %v", def, err)
					}
				}
				if _, err := ReadObjectDefinition(ctx, db, d, ObjectRef{Kind: KindView, Name: "jd_cat_users"}); !errors.Is(err, ErrObjectNotFound) {
					t.Errorf("a table asked for as a view: %v", err)
				}
				if _, err := ReadObjectDefinition(ctx, db, d, ObjectRef{Kind: KindEnum, Name: "x"}); !errors.Is(err, ErrNoDefinition) {
					t.Errorf("an enum type on MySQL: %v", err)
				}
			})

			// MODIFY COLUMN replaces the whole declaration, so a change to one
			// property has to restate the rest or lose it.
			t.Run("alter_column_restates_what_it_does_not_change", func(t *testing.T) {
				stmt := run(PlanAlterColumn(ctx, db, d, ColumnChange{
					Schema: schema, Table: "jd_cat_users", Column: "email", Type: "varchar(320)",
				}))
				email := catColumn(t, describe("jd_cat_users"), "email")
				if email.Type != "varchar(320)" || email.Nullable || email.Comment != "Login address" {
					t.Errorf("after %s: %+v", stmt, email)
				}
				run(PlanAlterColumn(ctx, db, d, ColumnChange{Schema: schema, Table: "jd_cat_counters", Column: "id", Type: "bigint unsigned"}))
				if id := catColumn(t, describe("jd_cat_counters"), "id"); id.Identity != "auto_increment" || !strings.Contains(id.Type, "unsigned") {
					t.Errorf("AUTO_INCREMENT was lost: %+v", id)
				}
				run(PlanAlterColumn(ctx, db, d, ColumnChange{Schema: schema, Table: "jd_cat_users", Column: "mood", Nullable: ddlBool(true)}))
				if mood := catColumn(t, describe("jd_cat_users"), "mood"); !mood.Nullable || mood.Default != "'ok'" || len(mood.EnumValues) != 3 {
					t.Errorf("the default or the labels were lost: %+v", mood)
				}
				run(PlanAlterColumn(ctx, db, d, ColumnChange{Schema: schema, Table: "jd_cat_users", Column: "created", Nullable: ddlBool(false)}))
				if !strings.Contains(strings.ToLower(describe("jd_cat_users").CreateSQL), "on update current_timestamp") {
					t.Errorf("ON UPDATE was lost:\n%s", describe("jd_cat_users").CreateSQL)
				}
				// The default alone needs no restating.
				stmt = run(PlanAlterColumn(ctx, db, d, ColumnChange{Schema: schema, Table: "jd_cat_users", Column: "score", Default: ddlString("9")}))
				if !strings.Contains(stmt, "ALTER COLUMN `score` SET DEFAULT 9") || catColumn(t, describe("jd_cat_users"), "score").Default != "9" {
					t.Errorf("after %s: %+v", stmt, catColumn(t, describe("jd_cat_users"), "score"))
				}
				run(PlanAlterColumn(ctx, db, d, ColumnChange{Schema: schema, Table: "jd_cat_users", Column: "score", DropDefault: true}))
				if _, err := PlanAlterColumn(ctx, db, d, ColumnChange{Schema: schema, Table: "jd_cat_users", Column: "shout", Type: "text"}); err == nil ||
					!strings.Contains(err.Error(), "generated column") {
					t.Errorf("a generated column was restated as a plain one: %v", err)
				}
				if _, err := PlanAlterColumn(ctx, db, d, ColumnChange{Schema: schema, Table: "jd_cat_users", Column: "nope", Type: "text"}); err == nil {
					t.Error("a column that does not exist was planned")
				}
			})

			t.Run("comments", func(t *testing.T) {
				run(PlanComment(ctx, db, d, CommentSpec{Schema: schema, Table: "jd_cat_posts", Comment: `What's \ written`}))
				run(PlanComment(ctx, db, d, CommentSpec{Schema: schema, Table: "jd_cat_posts", Column: "title", Comment: "Headline"}))
				posts := describe("jd_cat_posts")
				title := catColumn(t, posts, "title")
				if posts.Comment != `What's \ written` || title.Comment != "Headline" || title.Type != "varchar(255)" || title.Nullable {
					t.Errorf("comment = %q, title = %+v", posts.Comment, title)
				}
			})

			t.Run("foreign_keys_and_constraints", func(t *testing.T) {
				run(PlanDropForeignKey(d, schema, "jd_cat_posts", "jd_cat_posts_author"))
				if posts := describe("jd_cat_posts"); len(posts.ForeignKeys) != 0 {
					t.Errorf("the dropped key is still there: %+v", posts.ForeignKeys)
				}
				run(PlanAddForeignKey(d, ForeignKeySpec{
					Schema: schema, Table: "jd_cat_posts", Name: "jd_cat_posts_author", Columns: []string{"author_id"},
					RefTable: "jd_cat_users", RefColumns: []string{"id"}, OnDelete: "cascade", OnUpdate: "restrict",
				}))
				if posts := describe("jd_cat_posts"); len(posts.ForeignKeys) != 1 || posts.ForeignKeys[0].OnDelete != "CASCADE" {
					t.Errorf("the added key = %+v", posts.ForeignKeys)
				}
				run(PlanAddConstraint(d, ConstraintSpec{Schema: schema, Table: "jd_cat_posts", Type: "unique", Columns: []string{"title"}}))
				run(PlanAddConstraint(d, ConstraintSpec{Schema: schema, Table: "jd_cat_posts", Type: "check", Name: "jd_cat_title_len", Expression: "char_length(title) > 0"}))
				posts := describe("jd_cat_posts")
				if catConstraint(posts, "jd_cat_posts_title_key") == nil || catConstraint(posts, "jd_cat_title_len") == nil {
					t.Errorf("constraints = %+v", posts.Constraints)
				}
				// With no type given, the catalogue says which of MySQL's two
				// statements drops it.
				stmt := run(PlanDropConstraint(ctx, db, d, schema, "jd_cat_posts", "jd_cat_posts_title_key", ""))
				if !strings.Contains(stmt, "DROP INDEX") {
					t.Errorf("a unique constraint was dropped with %s", stmt)
				}
				stmt = run(PlanDropConstraint(ctx, db, d, schema, "jd_cat_posts", "jd_cat_title_len", ""))
				if !strings.Contains(stmt, "DROP CONSTRAINT") {
					t.Errorf("a check constraint was dropped with %s", stmt)
				}
				if posts := describe("jd_cat_posts"); len(posts.Constraints) != 0 {
					t.Errorf("dropped constraints are still there: %+v", posts.Constraints)
				}
				if _, err := PlanDropConstraint(ctx, db, d, schema, "jd_cat_posts", "nope", ""); err == nil {
					t.Error("a constraint that does not exist was planned")
				}
			})

			t.Run("indexes_and_views", func(t *testing.T) {
				run(PlanCreateIndex(ctx, db, d, IndexSpec{Schema: schema, Table: "jd_cat_posts", Name: "jd_cat_body_ft", Columns: []string{"body"}, Method: "fulltext"}))
				if ix := catIndex(describe("jd_cat_posts"), "jd_cat_body_ft"); ix == nil || ix.Method != "FULLTEXT" {
					t.Errorf("fulltext index = %+v", ix)
				}
				_, err := PlanCreateIndex(ctx, db, d, IndexSpec{Schema: schema, Table: "jd_cat_posts", Name: "jd_cat_body_ft", Columns: []string{"body"}, Method: "fulltext", IfNotExists: true})
				if maria && err != nil {
					t.Errorf("MariaDB has CREATE INDEX IF NOT EXISTS: %v", err)
				}
				if !maria && (err == nil || !strings.Contains(err.Error(), "MySQL has no CREATE INDEX IF NOT EXISTS")) {
					t.Errorf("MySQL was sent IF NOT EXISTS: %v", err)
				}
				run(PlanDropIndex(d, schema, "jd_cat_posts", "jd_cat_body_ft"))

				run(PlanCreateView(d, ViewSpec{Schema: schema, Name: "jd_cat_titles", Query: "SELECT title FROM jd_cat_posts"}))
				run(PlanCreateView(d, ViewSpec{Schema: schema, Name: "jd_cat_titles", Query: "SELECT id, title FROM jd_cat_posts", Replace: true}))
				if view := describe("jd_cat_titles"); view.Type != TableTypeView || len(view.Columns) != 2 {
					t.Errorf("replaced view = %+v", view)
				}
				run(PlanDropView(d, schema, "jd_cat_titles", false))
			})

			// The target of a rename is qualified, so the table stays in its
			// database whatever the connection happens to be using.
			t.Run("rename_stays_in_its_database", func(t *testing.T) {
				run(PlanRenameTable(d, schema, "jd_cat_posts", "jd_cat_articles"))
				if articles := describe("jd_cat_articles"); articles.Schema != schema || len(articles.Columns) == 0 {
					t.Errorf("renamed table = %+v", articles)
				}
				run(PlanRenameColumn(d, schema, "jd_cat_articles", "body", "text_body"))
				run(PlanDropColumn(ctx, db, d, schema, "jd_cat_articles", "text_body"))
				run(PlanRenameTable(d, schema, "jd_cat_articles", "jd_cat_posts"))
			})

			t.Run("graph", func(t *testing.T) {
				graph, err := BuildSchemaGraph(ctx, db, d, schema)
				if err != nil {
					t.Fatal(err)
				}
				for _, table := range graph.Tables {
					if table.Type == "sequence" {
						t.Errorf("a sequence is drawn as a table: %+v", table)
					}
					if table.Name == "jd_cat_active" && table.Comment != "" {
						t.Errorf("a view's comment is the word %q", table.Comment)
					}
				}
				found := false
				for _, e := range graph.Edges {
					if e.From == TableKey(schema, "jd_cat_posts") && e.To == TableKey(schema, "jd_cat_users") {
						found = true
					}
				}
				if !found {
					t.Errorf("edges = %+v", graph.Edges)
				}
			})
		})
	}
}

func TestLiveClickHouseCatalogAndStructureChanges(t *testing.T) {
	db := liveSQL(t, DriverClickHouse, "JD_TEST_CLICKHOUSE_DSN", "clickhouse://default@127.0.0.1:9000/default")
	ctx := context.Background()
	const d = DriverClickHouse
	info, err := ParseDSN(d, liveDSN(t, "JD_TEST_CLICKHOUSE_DSN", "clickhouse://default@127.0.0.1:9000/default"))
	if err != nil || info.Database == "" {
		t.Fatalf("the DSN names no database: %v", err)
	}
	schema := info.Database
	// The function is server-wide, so it carries the database's name to stay
	// out of the way of another run.
	function := "jd_plus_" + schema
	drop := []string{
		`DROP VIEW IF EXISTS jd_cat_mv`, `DROP VIEW IF EXISTS jd_cat_view`, `DROP VIEW IF EXISTS jd_cat_recent`,
		`DROP TABLE IF EXISTS jd_cat_events`, `DROP TABLE IF EXISTS jd_cat_log`, `DROP TABLE IF EXISTS jd_cat_daily`,
		`DROP FUNCTION IF EXISTS ` + function,
	}
	catalogExec(t, db, drop...)
	t.Cleanup(func() {
		for _, s := range drop {
			_, _ = db.ExecContext(context.Background(), s)
		}
	})
	catalogExec(t, db,
		`CREATE TABLE jd_cat_events (
		  id UInt64, kind Enum8('click' = 1, 'view' = 2), at DateTime, tags Array(String),
		  note Nullable(String), day Date MATERIALIZED toDate(at),
		  INDEX kind_idx kind TYPE set(10) GRANULARITY 1
		) ENGINE = MergeTree PARTITION BY toYYYYMM(at) ORDER BY (kind, id) COMMENT 'Events'`,
		`CREATE TABLE jd_cat_daily (day Date, n UInt64) ENGINE = SummingMergeTree ORDER BY day`,
		`CREATE VIEW jd_cat_view AS SELECT id, kind FROM jd_cat_events`,
		`CREATE MATERIALIZED VIEW jd_cat_mv TO jd_cat_daily AS SELECT toDate(at) AS day, count() AS n FROM jd_cat_events GROUP BY day`,
		`CREATE FUNCTION `+function+` AS (a, b) -> a + b`,
	)
	run := runPlan(t, db)
	describe := func(table string) *TableDetail {
		t.Helper()
		detail, err := DescribeTable(ctx, db, d, schema, table)
		if err != nil {
			t.Fatal(err)
		}
		return detail
	}

	t.Run("tree", func(t *testing.T) {
		catalog, err := ReadCatalog(ctx, db, d, CatalogOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if catalog.Errors != nil {
			t.Errorf("groups failed: %v", catalog.Errors)
		}
		if catalog.Schema != schema || catalog.DefaultSchema != schema {
			t.Errorf("schema = %q, default = %q", catalog.Schema, catalog.DefaultSchema)
		}
		for _, absent := range []string{GroupTriggers, GroupSequences, GroupProcedures, GroupTypes} {
			if _, ok := catalog.Objects[absent]; ok {
				t.Errorf("ClickHouse lists a %s group", absent)
			}
		}
		if events := catObject(catalog.Objects[GroupTables], "jd_cat_events"); events == nil || events.Detail != "MergeTree" ||
			events.Comment != "Events" || events.Rows == nil {
			t.Errorf("events = %+v", events)
		}
		// What the engine calls a table is told apart by its engine.
		if catObject(catalog.Objects[GroupTables], "jd_cat_view") != nil || catObject(catalog.Objects[GroupTables], "jd_cat_mv") != nil {
			t.Error("a view is listed among the tables")
		}
		if catObject(catalog.Objects[GroupViews], "jd_cat_view") == nil ||
			catObject(catalog.Objects[GroupMaterializedViews], "jd_cat_mv") == nil {
			t.Errorf("views = %+v, matviews = %+v", catalog.Objects[GroupViews], catalog.Objects[GroupMaterializedViews])
		}
		if fn := catObject(catalog.Objects[GroupFunctions], function); fn == nil || fn.Schema != "" {
			t.Errorf("functions = %+v", catalog.Objects[GroupFunctions])
		}
	})

	t.Run("table", func(t *testing.T) {
		events := describe("jd_cat_events")
		if events.Type != TableTypeTable || events.Comment != "Events" || catFact(events.Facts, "Engine") != "MergeTree" ||
			catFact(events.Facts, "Partitioned by") != "toYYYYMM(at)" || catFact(events.Facts, "Ordered by") != "kind, id" ||
			events.CreateSQLSource != DefinitionFromEngine {
			t.Errorf("events = %+v", events)
		}
		if c := catColumn(t, events, "kind"); c.TypeKind != "enum" || !reflect.DeepEqual(c.EnumValues, []string{"click", "view"}) || c.Key != "PRI" {
			t.Errorf("kind = %+v", c)
		}
		if c := catColumn(t, events, "note"); !c.Nullable {
			t.Errorf("note = %+v", c)
		}
		if c := catColumn(t, events, "day"); c.Generated != "toDate(at)" || c.GeneratedKind != "stored" {
			t.Errorf("day = %+v", c)
		}
		if ix := catIndex(events, "kind_idx"); ix == nil || ix.Method != "set" || !ix.Expression {
			t.Errorf("skipping index = %+v", ix)
		}
		if ix := catIndex(events, "sorting key"); ix == nil || !ix.Primary || !reflect.DeepEqual(ix.Columns, []string{"kind", "id"}) {
			t.Errorf("sorting key = %+v", ix)
		}
		unqualified, err := DescribeTable(ctx, db, d, "", "jd_cat_events")
		if err != nil || unqualified.Schema != schema || len(unqualified.PrimaryKey) != 2 {
			t.Errorf("without a schema = %+v, %v", unqualified, err)
		}
		tables, err := ListTables(ctx, db, d, schema)
		if err != nil {
			t.Fatal(err)
		}
		for _, table := range tables {
			if table.Name == "jd_cat_mv" && table.Type != TableTypeMaterializedView {
				t.Errorf("the materialized view is listed as a %s", table.Type)
			}
		}
		for kind, name := range map[string]string{KindView: "jd_cat_view", KindMaterializedView: "jd_cat_mv", KindFunction: function} {
			def, err := ReadObjectDefinition(ctx, db, d, ObjectRef{Kind: kind, Schema: schema, Name: name})
			if err != nil || !strings.HasPrefix(def.Definition, "CREATE ") || def.Source != DefinitionFromEngine {
				t.Errorf("%s = %+v, %v", kind, def, err)
			}
		}
		if _, err := ReadObjectDefinition(ctx, db, d, ObjectRef{Kind: KindView, Schema: schema, Name: "jd_cat_events"}); !errors.Is(err, ErrObjectNotFound) {
			t.Errorf("a table asked for as a view: %v", err)
		}
	})

	// Each of these is ClickHouse's own statement; the generic ones the forms
	// used to send were refused by the server or, worse, were not.
	t.Run("structure_changes", func(t *testing.T) {
		run(PlanAddColumn(d, schema, "jd_cat_events", NewColumn{Name: "weight", Type: "UInt32", NotNull: true, Default: "1"}))
		run(PlanAlterColumn(ctx, db, d, ColumnChange{Schema: schema, Table: "jd_cat_events", Column: "weight", Type: "UInt64", Default: ddlString("2")}))
		if c := catColumn(t, describe("jd_cat_events"), "weight"); c.Type != "UInt64" || c.Default != "2" {
			t.Errorf("weight = %+v", c)
		}
		run(PlanAlterColumn(ctx, db, d, ColumnChange{Schema: schema, Table: "jd_cat_events", Column: "weight", DropDefault: true}))
		if c := catColumn(t, describe("jd_cat_events"), "weight"); c.Default != "" {
			t.Errorf("weight after REMOVE DEFAULT = %+v", c)
		}
		run(PlanRenameColumn(d, schema, "jd_cat_events", "weight", "mass"))
		run(PlanComment(ctx, db, d, CommentSpec{Schema: schema, Table: "jd_cat_events", Column: "mass", Comment: `It's \ heavy`}))
		run(PlanComment(ctx, db, d, CommentSpec{Schema: schema, Table: "jd_cat_events", Comment: "Everything that happened"}))
		events := describe("jd_cat_events")
		if events.Comment != "Everything that happened" || catColumn(t, events, "mass").Comment != `It's \ heavy` {
			t.Errorf("comments = %q / %q", events.Comment, catColumn(t, events, "mass").Comment)
		}
		run(PlanAddConstraint(d, ConstraintSpec{Schema: schema, Table: "jd_cat_events", Type: "check", Name: "mass_sane", Expression: "mass < 1000"}))
		if !strings.Contains(describe("jd_cat_events").CreateSQL, "CONSTRAINT mass_sane CHECK") {
			t.Errorf("the check is not in the definition:\n%s", describe("jd_cat_events").CreateSQL)
		}
		run(PlanDropConstraint(ctx, db, d, schema, "jd_cat_events", "mass_sane", ""))
		run(PlanDropIndex(d, schema, "jd_cat_events", "kind_idx"))
		if catIndex(describe("jd_cat_events"), "kind_idx") != nil {
			t.Error("the dropped index is still there")
		}
		run(PlanDropColumn(ctx, db, d, schema, "jd_cat_events", "mass"))

		run(PlanCreateView(d, ViewSpec{Schema: schema, Name: "jd_cat_recent", Query: "SELECT id FROM " + schema + ".jd_cat_events"}))
		run(PlanCreateView(d, ViewSpec{Schema: schema, Name: "jd_cat_recent", Query: "SELECT id, at FROM " + schema + ".jd_cat_events", Replace: true}))
		if view := describe("jd_cat_recent"); view.Type != TableTypeView || len(view.Columns) != 2 {
			t.Errorf("replaced view = %+v", view)
		}
		run(PlanDropView(d, schema, "jd_cat_recent", false))

		catalogExec(t, db, `DROP VIEW jd_cat_mv`, `DROP VIEW jd_cat_view`)
		run(PlanRenameTable(d, schema, "jd_cat_events", "jd_cat_log"))
		if log := describe("jd_cat_log"); log.Schema != schema || len(log.Columns) == 0 {
			t.Errorf("renamed table = %+v", log)
		}
		run(PlanTruncate(d, schema, "jd_cat_log"))
		run(PlanDropTable(d, schema, "jd_cat_log"))
		if gone := describe("jd_cat_log"); len(gone.Columns) != 0 || gone.Type != "" {
			t.Errorf("the dropped table is still there: %+v", gone)
		}
	})
}
