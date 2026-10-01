package dbx

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
)

// sqlServerLiveDatabase is where these tests build their schemas when the DSN
// names none of its own. A SQL Server fixture's DSN names master, and master
// is what every suite pointed at that server shares: schemas made and dropped
// there are made and dropped beside whatever else is using it. So the tests
// work in a database of their own, created the first time and kept — it is
// empty once a test has cleaned up after itself, and creating it is the slow
// part. A DSN that names any other database is taken at its word.
const sqlServerLiveDatabase = "jd_schema_live"

func sqlServerLive(t *testing.T) *sql.DB {
	t.Helper()
	server := catalogLive(t, DriverMSSQL, "JD_TEST_MSSQL_DSN")
	dsn, err := url.Parse(os.Getenv("JD_TEST_MSSQL_DSN"))
	if err != nil {
		t.Fatalf("JD_TEST_MSSQL_DSN is not a sqlserver:// address: %v", err)
	}
	query := dsn.Query()
	if named := query.Get("database"); named != "" && !strings.EqualFold(named, "master") {
		return server
	}
	ctx := context.Background()
	if _, err := server.ExecContext(ctx, `IF DB_ID(N'`+sqlServerLiveDatabase+`') IS NULL CREATE DATABASE `+sqlServerLiveDatabase); err != nil {
		// Another package's tests may have found it missing in the same moment
		// and created it first.
		var id sql.NullInt64
		if scanErr := server.QueryRowContext(ctx, `SELECT DB_ID(N'`+sqlServerLiveDatabase+`')`).Scan(&id); scanErr != nil || !id.Valid {
			t.Fatalf("could not create %s: %v", sqlServerLiveDatabase, err)
		}
	}
	query.Set("database", sqlServerLiveDatabase)
	dsn.RawQuery = query.Encode()
	db, err := sql.Open("sqlserver", dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("%s: %v", sqlServerLiveDatabase, err)
	}
	return db
}

// SQL Server keeps a default as a constraint object, a comment as an extended
// property, renames behind a stored procedure and no CREATE text for a table,
// so nearly every statement this package writes for it is its own. None of
// that is proved by comparing strings: only the server says whether it parses,
// and only its catalogue says what it did.
func TestLiveSQLServerCatalogAndStructureChanges(t *testing.T) {
	db := sqlServerLive(t)
	ctx := context.Background()
	const d = DriverMSSQL
	const schema = "jd_cat"
	// In the order the objects can go: what refers to a table before the table.
	drop := []string{
		`DROP TABLE IF EXISTS jd_cat2.users`,
		`DROP VIEW IF EXISTS jd_cat.active_users`, `DROP VIEW IF EXISTS jd_cat.titles`,
		`DROP PROCEDURE IF EXISTS jd_cat.noop`, `DROP FUNCTION IF EXISTS jd_cat.add_two`,
		`DROP FUNCTION IF EXISTS jd_cat.posts_of`, `DROP SYNONYM IF EXISTS jd_cat.people`,
		`DROP TABLE IF EXISTS jd_cat.posts`, `DROP TABLE IF EXISTS jd_cat.articles`,
		`DROP TABLE IF EXISTS jd_cat.users`, `DROP TABLE IF EXISTS jd_cat.made`,
		`DROP SEQUENCE IF EXISTS jd_cat.ticket_seq`, `DROP TYPE IF EXISTS jd_cat.phone`,
		`DROP SCHEMA IF EXISTS jd_cat`, `DROP SCHEMA IF EXISTS jd_cat2`, `DROP SCHEMA IF EXISTS jd_cat_made`,
	}
	catalogExec(t, db, drop...)
	t.Cleanup(func() {
		for _, s := range drop {
			_, _ = db.ExecContext(context.Background(), s)
		}
	})
	catalogExec(t, db,
		`CREATE SCHEMA jd_cat`, `CREATE SCHEMA jd_cat2`,
		`CREATE TYPE jd_cat.phone FROM varchar(20) NOT NULL`,
		`CREATE SEQUENCE jd_cat.ticket_seq AS bigint START WITH 100 INCREMENT BY 5`,
		`CREATE TABLE jd_cat.users (
		  id bigint IDENTITY(10,2) NOT NULL CONSTRAINT users_pk PRIMARY KEY,
		  email nvarchar(255) NOT NULL,
		  code varchar(20) COLLATE Latin1_General_BIN2 NOT NULL CONSTRAINT users_code_df DEFAULT 'none',
		  name nvarchar(100) NULL,
		  nick nvarchar(50) NULL,
		  shout AS (upper(name)) PERSISTED,
		  score int NULL CONSTRAINT users_score_df DEFAULT 5,
		  phone jd_cat.phone NULL,
		  created datetime2(3) NOT NULL CONSTRAINT users_created_df DEFAULT sysutcdatetime(),
		  CONSTRAINT users_email_key UNIQUE (email),
		  CONSTRAINT users_score_positive CHECK (score > 0))`,
		`EXEC sys.sp_addextendedproperty @name = N'MS_Description', @value = N'People',
		   @level0type = N'SCHEMA', @level0name = N'jd_cat', @level1type = N'TABLE', @level1name = N'users'`,
		`EXEC sys.sp_addextendedproperty @name = N'MS_Description', @value = N'Login address',
		   @level0type = N'SCHEMA', @level0name = N'jd_cat', @level1type = N'TABLE', @level1name = N'users',
		   @level2type = N'COLUMN', @level2name = N'email'`,
		`CREATE TABLE jd_cat.posts (
		  id int NOT NULL PRIMARY KEY,
		  author_id bigint NOT NULL CONSTRAINT posts_author FOREIGN KEY REFERENCES jd_cat.users(id) ON DELETE CASCADE,
		  title nvarchar(255) NOT NULL, body nvarchar(max) NULL, published bit NOT NULL DEFAULT 0)`,
		`CREATE INDEX posts_author_idx ON jd_cat.posts(author_id)`,
		`CREATE UNIQUE INDEX posts_published_title ON jd_cat.posts(title) INCLUDE (author_id) WHERE published = 1`,
		`CREATE VIEW jd_cat.active_users AS SELECT id, email FROM jd_cat.users WHERE name IS NOT NULL`,
		`CREATE PROCEDURE jd_cat.noop @x int, @y int OUTPUT AS SET @y = @x`,
		`CREATE FUNCTION jd_cat.add_two(@a int, @b int) RETURNS int AS BEGIN RETURN @a + @b END`,
		`CREATE FUNCTION jd_cat.posts_of(@author bigint) RETURNS TABLE AS RETURN (SELECT id FROM jd_cat.posts WHERE author_id = @author)`,
		`CREATE TRIGGER jd_cat.users_touch ON jd_cat.users AFTER INSERT, UPDATE AS SET NOCOUNT ON`,
		`CREATE SYNONYM jd_cat.people FOR jd_cat.users`,
		// The same name in a second schema, pointing back at the first.
		`CREATE TABLE jd_cat2.users (id int NOT NULL PRIMARY KEY, boss bigint NULL REFERENCES jd_cat.users(id))`,
		`INSERT INTO jd_cat.users(email, code, name, score) VALUES (N'a@x.io', 'A', N'Ann', 3), (N'b@x.io', 'b', NULL, NULL)`,
		`INSERT INTO jd_cat.posts(id, author_id, title) VALUES (1, 10, N'Hello')`,
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
		catalog, err := ReadCatalog(ctx, db, d, CatalogOptions{Schema: schema})
		if err != nil {
			t.Fatal(err)
		}
		if catalog.Errors != nil {
			t.Errorf("groups failed: %v", catalog.Errors)
		}
		if catalog.Schema != schema || catalog.DefaultSchema != "dbo" {
			t.Errorf("schema = %q, default = %q", catalog.Schema, catalog.DefaultSchema)
		}
		groups := []string{}
		for group := range catalog.Objects {
			groups = append(groups, group)
		}
		if advertised := CatalogGroups(d, ""); len(advertised) != len(groups) {
			t.Errorf("SQL Server advertises %v and read %v", advertised, groups)
		}
		var fixture, system *CatalogSchema
		for i := range catalog.Schemas {
			switch catalog.Schemas[i].Name {
			case schema:
				fixture = &catalog.Schemas[i]
			case "sys":
				system = &catalog.Schemas[i]
			}
		}
		if fixture == nil || fixture.System || fixture.Tables != 3 || system == nil || !system.System {
			t.Errorf("schemas: fixture = %+v, sys = %+v", fixture, system)
		}
		if users := catObject(catalog.Objects[GroupTables], "users"); users == nil || users.Comment != "People" ||
			users.Rows == nil || *users.Rows != 2 || users.Schema != schema {
			t.Errorf("users = %+v", users)
		}
		if len(catalog.Objects[GroupTables]) != 2 || catObject(catalog.Objects[GroupTables], "active_users") != nil {
			t.Errorf("tables = %+v", catalog.Objects[GroupTables])
		}
		if catObject(catalog.Objects[GroupViews], "active_users") == nil {
			t.Errorf("views = %+v", catalog.Objects[GroupViews])
		}
		if fn := catObject(catalog.Objects[GroupFunctions], "add_two"); fn == nil || fn.Signature != "@a int, @b int" || fn.Returns != "int" {
			t.Errorf("scalar function = %+v", fn)
		}
		if fn := catObject(catalog.Objects[GroupFunctions], "posts_of"); fn == nil || fn.Detail != "table-valued" || fn.Returns != "TABLE" {
			t.Errorf("table-valued function = %+v", fn)
		}
		if proc := catObject(catalog.Objects[GroupProcedures], "noop"); proc == nil || proc.Signature != "@x int, @y int OUTPUT" {
			t.Errorf("procedure = %+v", proc)
		}
		if tr := catObject(catalog.Objects[GroupTriggers], "users_touch"); tr == nil || tr.Table != "users" ||
			tr.Detail != "AFTER INSERT, UPDATE, each statement" {
			t.Errorf("trigger = %+v", tr)
		}
		if seq := catObject(catalog.Objects[GroupSequences], "ticket_seq"); seq == nil || seq.Detail != "bigint, step 5" {
			t.Errorf("sequence = %+v", seq)
		}
		if typ := catObject(catalog.Objects[GroupTypes], "phone"); typ == nil || typ.Kind != KindType || typ.Detail != "varchar(20)" {
			t.Errorf("type = %+v", typ)
		}
		if syn := catObject(catalog.Objects[GroupSynonyms], "people"); syn == nil || !strings.Contains(syn.Detail, "users") {
			t.Errorf("synonym = %+v", syn)
		}
		// Every schema at once keeps the two tables called users apart.
		all, err := ReadCatalog(ctx, db, d, CatalogOptions{All: true})
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, o := range all.Objects[GroupTables] {
			if o.Name == "users" {
				seen[o.Schema] = true
			}
		}
		if !seen["jd_cat"] || !seen["jd_cat2"] || all.Schema != "" {
			t.Errorf("all schemas: users in %v, schema = %q", seen, all.Schema)
		}
	})

	t.Run("table", func(t *testing.T) {
		users := describe("users")
		if users.Type != TableTypeTable || users.Comment != "People" || users.Schema != schema || users.Rows != 2 ||
			users.CreateSQLSource != DefinitionGenerated || !reflect.DeepEqual(users.PrimaryKey, []string{"id"}) {
			t.Errorf("users = %+v", users)
		}
		if c := catColumn(t, users, "id"); c.Identity != "identity(10,2)" || c.Key != "PRI" || c.Type != "bigint" {
			t.Errorf("id = %+v", c)
		}
		if c := catColumn(t, users, "email"); c.Comment != "Login address" || c.Type != "nvarchar(255)" || c.Nullable {
			t.Errorf("email = %+v", c)
		}
		if c := catColumn(t, users, "code"); c.Default != "('none')" || c.Type != "varchar(20)" {
			t.Errorf("code = %+v", c)
		}
		if c := catColumn(t, users, "shout"); c.GeneratedKind != "stored" || !strings.Contains(strings.ToLower(c.Generated), "upper") || c.Default != "" {
			t.Errorf("shout = %+v", c)
		}
		// An alias type outside dbo is not found by its bare name.
		if c := catColumn(t, users, "phone"); c.TypeKind != "domain" || c.Type != "jd_cat.phone" {
			t.Errorf("phone = %+v", c)
		}
		if c := catColumn(t, users, "created"); c.Type != "datetime2(3)" || !strings.Contains(c.Default, "sysutcdatetime") {
			t.Errorf("created = %+v", c)
		}
		if c := catConstraint(users, "users_score_positive"); c == nil || c.Type != ConstraintCheck ||
			!strings.Contains(c.Definition, "[score]>(0)") || !reflect.DeepEqual(c.Columns, []string{"score"}) {
			t.Errorf("check = %+v in %+v", c, users.Constraints)
		}
		if c := catConstraint(users, "users_email_key"); c == nil || c.Type != ConstraintUnique || c.Definition != "UNIQUE ([email])" {
			t.Errorf("unique = %+v", c)
		}
		referencing := map[string]bool{}
		for _, in := range users.ReferencedBy {
			referencing[in.Schema+"."+in.Table] = true
			if in.Table == "posts" && (in.OnDelete != "CASCADE" || !reflect.DeepEqual(in.RefColumns, []string{"id"})) {
				t.Errorf("incoming key from posts = %+v", in)
			}
		}
		if !referencing["jd_cat.posts"] || !referencing["jd_cat2.users"] {
			t.Errorf("referencedBy = %+v", users.ReferencedBy)
		}
		for _, want := range []string{
			`CREATE TABLE [jd_cat].[users]`, `IDENTITY(10,2)`, `CONSTRAINT [users_email_key] UNIQUE ([email])`,
			`CONSTRAINT [users_score_positive] CHECK`, `PERSISTED`,
		} {
			if !strings.Contains(users.CreateSQL, want) {
				t.Errorf("the generated definition is missing %q:\n%s", want, users.CreateSQL)
			}
		}

		posts := describe("posts")
		if ix := catIndex(posts, "posts_published_title"); ix == nil || !ix.Unique || ix.Method != "NONCLUSTERED" ||
			!strings.Contains(ix.Predicate, "[published]=(1)") || !reflect.DeepEqual(ix.Include, []string{"author_id"}) ||
			!strings.Contains(ix.Definition, "INCLUDE ([author_id]) WHERE") {
			t.Errorf("filtered index = %+v", ix)
		}
		if len(posts.ForeignKeys) != 1 || posts.ForeignKeys[0].Name != "posts_author" || posts.ForeignKeys[0].OnDelete != "CASCADE" ||
			posts.ForeignKeys[0].RefSchema != schema {
			t.Errorf("foreign keys = %+v", posts.ForeignKeys)
		}
		if !strings.Contains(posts.CreateSQL, "CREATE UNIQUE NONCLUSTERED INDEX [posts_published_title]") {
			t.Errorf("the generated definition has no index:\n%s", posts.CreateSQL)
		}
		if view := describe("active_users"); view.Type != TableTypeView || !strings.Contains(view.CreateSQL, "CREATE VIEW jd_cat.active_users") ||
			view.CreateSQLSource != DefinitionFromEngine {
			t.Errorf("view = %+v", view)
		}
	})

	t.Run("definitions", func(t *testing.T) {
		for kind, want := range map[string]struct{ name, text, source string }{
			KindView:      {"active_users", "CREATE VIEW jd_cat.active_users", DefinitionFromEngine},
			KindProcedure: {"noop", "CREATE PROCEDURE jd_cat.noop", DefinitionFromEngine},
			KindFunction:  {"add_two", "CREATE FUNCTION jd_cat.add_two", DefinitionFromEngine},
			KindTrigger:   {"users_touch", "CREATE TRIGGER jd_cat.users_touch", DefinitionFromEngine},
			KindSequence:  {"ticket_seq", "INCREMENT BY 5", DefinitionGenerated},
			KindType:      {"phone", "CREATE TYPE [jd_cat].[phone] FROM varchar(20) NOT NULL", DefinitionGenerated},
			KindSynonym:   {"people", "CREATE SYNONYM [jd_cat].[people] FOR", DefinitionGenerated},
			KindTable:     {"posts", "CREATE TABLE [jd_cat].[posts]", DefinitionGenerated},
		} {
			def, err := ReadObjectDefinition(ctx, db, d, ObjectRef{Kind: kind, Schema: schema, Name: want.name})
			if err != nil || !strings.Contains(def.Definition, want.text) || def.Source != want.source {
				t.Errorf("%s = %+v, %v", kind, def, err)
			}
		}
		tr, err := ReadObjectDefinition(ctx, db, d, ObjectRef{Kind: KindTrigger, Schema: schema, Name: "users_touch"})
		if err != nil || tr.Table != "users" {
			t.Errorf("trigger = %+v, %v", tr, err)
		}
		if _, err := ReadObjectDefinition(ctx, db, d, ObjectRef{Kind: KindView, Schema: schema, Name: "users"}); !errors.Is(err, ErrObjectNotFound) {
			t.Errorf("a table asked for as a view: %v", err)
		}
		if _, err := ReadObjectDefinition(ctx, db, d, ObjectRef{Kind: KindEnum, Schema: schema, Name: "x"}); !errors.Is(err, ErrNoDefinition) {
			t.Errorf("an enum type on SQL Server: %v", err)
		}
	})

	// ALTER COLUMN restates the type, the collation and the nullability
	// together, and a default is an object of its own.
	t.Run("alter_column", func(t *testing.T) {
		collation := func(column string) string {
			t.Helper()
			var name string
			if err := db.QueryRowContext(ctx, `SELECT ISNULL(c.collation_name, '') FROM sys.columns c
			  WHERE c.object_id = OBJECT_ID(N'jd_cat.users') AND c.name = @p1`, column).Scan(&name); err != nil {
				t.Fatal(err)
			}
			return name
		}
		// A nullability change on a column declared with its own collation
		// used to restate the type alone, and SQL Server re-collated the
		// column to the database's default without a word.
		stmt := run(PlanAlterColumn(ctx, db, d, ColumnChange{Schema: schema, Table: "users", Column: "code", Nullable: ddlBool(true)}))
		if stmt != "ALTER TABLE [jd_cat].[users] ALTER COLUMN [code] varchar(20) COLLATE Latin1_General_BIN2 NULL" {
			t.Errorf("statement = %s", stmt)
		}
		if c := catColumn(t, describe("users"), "code"); !c.Nullable || c.Default != "('none')" || collation("code") != "Latin1_General_BIN2" {
			t.Errorf("code after relaxing it = %+v, collation %s", c, collation("code"))
		}
		// A wider type of the same kind keeps it too; a column that follows
		// the database's collation is restated without one.
		run(PlanAlterColumn(ctx, db, d, ColumnChange{Schema: schema, Table: "users", Column: "code", Type: "varchar(40)", Nullable: ddlBool(false)}))
		if c := catColumn(t, describe("users"), "code"); c.Type != "varchar(40)" || c.Nullable || collation("code") != "Latin1_General_BIN2" {
			t.Errorf("code after widening it = %+v, collation %s", c, collation("code"))
		}
		stmt = run(PlanAlterColumn(ctx, db, d, ColumnChange{Schema: schema, Table: "users", Column: "nick", Nullable: ddlBool(true)}))
		if stmt != "ALTER TABLE [jd_cat].[users] ALTER COLUMN [nick] nvarchar(50) NULL" {
			t.Errorf("statement = %s", stmt)
		}
		// The server refuses to change the data type of a column while a
		// default's constraint references it, so the default is taken off and
		// put back as it was, under its own name, around the change.
		stmt = run(PlanAlterColumn(ctx, db, d, ColumnChange{Schema: schema, Table: "users", Column: "created", Type: "datetimeoffset(3)"}))
		for _, want := range []string{
			"BEGIN TRANSACTION", "DROP CONSTRAINT [users_created_df];\n",
			"ALTER COLUMN [created] datetimeoffset(3) NOT NULL;\n",
			"ADD CONSTRAINT [users_created_df] DEFAULT (sysutcdatetime()) FOR [created]",
		} {
			if !strings.Contains(stmt, want) {
				t.Errorf("retyping a column with a default: %q is not in\n%s", want, stmt)
			}
		}
		if c := catColumn(t, describe("users"), "created"); c.Type != "datetimeoffset(3)" || c.Nullable || !strings.Contains(c.Default, "sysutcdatetime") {
			t.Errorf("created after its type changed = %+v", c)
		}
		// A new type and a new default at once: the old default goes before
		// the type changes, and the new one arrives after.
		stmt = run(PlanAlterColumn(ctx, db, d, ColumnChange{Schema: schema, Table: "users", Column: "code", Type: "nvarchar(40)", Default: ddlString("'x'")}))
		dropped, altered, added := strings.Index(stmt, "DROP CONSTRAINT [users_code_df]"),
			strings.Index(stmt, "ALTER COLUMN [code] nvarchar(40) COLLATE Latin1_General_BIN2 NOT NULL"), strings.Index(stmt, "DEFAULT 'x' FOR [code]")
		if dropped < 0 || altered < dropped || added < altered {
			t.Errorf("statement = %s", stmt)
		}
		if c := catColumn(t, describe("users"), "code"); c.Type != "nvarchar(40)" || c.Default != "('x')" || collation("code") != "Latin1_General_BIN2" {
			t.Errorf("code after a new type and default = %+v, collation %s", c, collation("code"))
		}
		// What cannot be moved out of the way is the server's to refuse, and
		// its refusal is passed on whole: which object is in the way, not only
		// that one is.
		plan, err := PlanAlterColumn(ctx, db, d, ColumnChange{Schema: schema, Table: "users", Column: "score", Type: "bigint"})
		if err != nil {
			t.Fatal(err)
		}
		if err := plan.Exec(ctx, db); err == nil || !strings.Contains(err.Error(), "users_score_positive") {
			t.Errorf("retyping a column under a check constraint: %v", err)
		}
		if c := catColumn(t, describe("users"), "score"); c.Type != "int" || c.Default != "((5))" {
			t.Errorf("the refused change left the column as %+v", c)
		}
		// An alias type is restated by its own quoted, schema-qualified name.
		stmt = run(PlanAlterColumn(ctx, db, d, ColumnChange{Schema: schema, Table: "users", Column: "phone", Nullable: ddlBool(true)}))
		if stmt != "ALTER TABLE [jd_cat].[users] ALTER COLUMN [phone] [jd_cat].[phone] NULL" {
			t.Errorf("statement = %s", stmt)
		}
		// Replacing a default drops the constraint that held the old one and
		// adds another, in one batch.
		stmt = run(PlanAlterColumn(ctx, db, d, ColumnChange{Schema: schema, Table: "users", Column: "score", Default: ddlString("9")}))
		if !strings.Contains(stmt, "DROP CONSTRAINT [users_score_df]") || !strings.Contains(stmt, "BEGIN TRANSACTION") ||
			catColumn(t, describe("users"), "score").Default != "((9))" {
			t.Errorf("after %s: %+v", stmt, catColumn(t, describe("users"), "score"))
		}
		run(PlanAlterColumn(ctx, db, d, ColumnChange{Schema: schema, Table: "users", Column: "score", DropDefault: true}))
		if c := catColumn(t, describe("users"), "score"); c.Default != "" {
			t.Errorf("score after dropping its default = %+v", c)
		}
		if _, err := PlanAlterColumn(ctx, db, d, ColumnChange{Schema: schema, Table: "users", Column: "score", DropDefault: true}); err == nil ||
			!strings.Contains(err.Error(), "no default to drop") {
			t.Errorf("dropping a default that is not there: %v", err)
		}
		// Type, nullability and default at once: all or nothing.
		plan, err = PlanAlterColumn(ctx, db, d, ColumnChange{
			Schema: schema, Table: "users", Column: "score", Type: "bigint", Nullable: ddlBool(false), Default: ddlString("1"),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := plan.Exec(ctx, db); err == nil {
			t.Fatal("NOT NULL was set over a NULL")
		}
		if c := catColumn(t, describe("users"), "score"); c.Type != "int" || c.Default != "" {
			t.Errorf("a failed batch left part of itself behind: %+v", c)
		}
		if _, err := PlanAlterColumn(ctx, db, d, ColumnChange{Schema: schema, Table: "users", Column: "shout", Nullable: ddlBool(true)}); err == nil ||
			!strings.Contains(err.Error(), "computed column") {
			t.Errorf("a computed column was restated as a plain one: %v", err)
		}
		if _, err := PlanAlterColumn(ctx, db, d, ColumnChange{Schema: schema, Table: "users", Column: "nope", Nullable: ddlBool(true)}); err == nil ||
			errors.Is(err, ErrPlanRead) {
			t.Errorf("a column that does not exist: %v", err)
		}
	})

	t.Run("comments", func(t *testing.T) {
		// One that exists is updated, one that does not is added, and an
		// apostrophe in either is a character.
		run(PlanComment(ctx, db, d, CommentSpec{Schema: schema, Table: "users", Comment: "People's accounts"}))
		run(PlanComment(ctx, db, d, CommentSpec{Schema: schema, Table: "users", Column: "name", Comment: "Shown name"}))
		run(PlanComment(ctx, db, d, CommentSpec{Schema: schema, Table: "users", Column: "name", Comment: "Display name"}))
		users := describe("users")
		if users.Comment != "People's accounts" || catColumn(t, users, "name").Comment != "Display name" ||
			catColumn(t, users, "email").Comment != "Login address" {
			t.Errorf("comments = %q / %q", users.Comment, catColumn(t, users, "name").Comment)
		}
	})

	t.Run("foreign_keys_and_constraints", func(t *testing.T) {
		run(PlanDropForeignKey(d, schema, "posts", "posts_author"))
		if posts := describe("posts"); len(posts.ForeignKeys) != 0 {
			t.Errorf("the dropped key is still there: %+v", posts.ForeignKeys)
		}
		run(PlanAddForeignKey(d, ForeignKeySpec{
			Schema: schema, Table: "posts", Name: "posts_author", Columns: []string{"author_id"},
			RefTable: "users", RefColumns: []string{"id"}, OnDelete: "cascade", OnUpdate: "no action",
		}))
		if posts := describe("posts"); len(posts.ForeignKeys) != 1 || posts.ForeignKeys[0].OnDelete != "CASCADE" {
			t.Errorf("the added key = %+v", posts.ForeignKeys)
		}
		run(PlanAddConstraint(d, ConstraintSpec{Schema: schema, Table: "posts", Type: "unique", Columns: []string{"author_id", "title"}}))
		run(PlanAddConstraint(d, ConstraintSpec{Schema: schema, Table: "posts", Type: "check", Name: "posts_title_len", Expression: "len([title]) > 0"}))
		posts := describe("posts")
		if catConstraint(posts, "posts_author_id_title_key") == nil || catConstraint(posts, "posts_title_len") == nil {
			t.Errorf("constraints = %+v", posts.Constraints)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO jd_cat.posts(id, author_id, title) VALUES (9, 10, N'')`); err == nil {
			t.Error("the check constraint is not enforced")
		}
		// The index behind a constraint is named as one, so a page knows to
		// drop the constraint and not the index.
		if ix := catIndex(posts, "posts_author_id_title_key"); ix == nil || ix.Constraint != "posts_author_id_title_key" {
			t.Errorf("the constraint's index = %+v", ix)
		}
		run(PlanDropConstraint(ctx, db, d, schema, "posts", "posts_author_id_title_key", ""))
		run(PlanDropConstraint(ctx, db, d, schema, "posts", "posts_title_len", "check"))
		if posts := describe("posts"); len(posts.Constraints) != 0 {
			t.Errorf("dropped constraints are still there: %+v", posts.Constraints)
		}
	})

	t.Run("indexes_views_and_schemas", func(t *testing.T) {
		stmt := run(PlanCreateIndex(ctx, db, d, IndexSpec{
			Schema: schema, Table: "posts", Name: "posts_live", Columns: []string{"title"}, Unique: true,
			Method: "nonclustered", Where: "[published] = 1 AND [title] <> N'' AND [id] IN (1, 2)", Concurrently: true,
		}))
		if !strings.HasSuffix(stmt, "WITH (ONLINE = ON)") {
			t.Errorf("statement = %s", stmt)
		}
		if ix := catIndex(describe("posts"), "posts_live"); ix == nil || !ix.Unique || ix.Predicate == "" {
			t.Errorf("created index = %+v", ix)
		}
		run(PlanDropIndex(d, schema, "posts", "posts_live"))
		if catIndex(describe("posts"), "posts_live") != nil {
			t.Error("the dropped index is still there")
		}

		run(PlanCreateView(d, ViewSpec{Schema: schema, Name: "titles", Query: "SELECT title FROM jd_cat.posts"}))
		stmt = run(PlanCreateView(d, ViewSpec{Schema: schema, Name: "titles", Query: "SELECT id, title FROM jd_cat.posts", Replace: true}))
		if !strings.HasPrefix(stmt, "CREATE OR ALTER VIEW [jd_cat].[titles] AS") {
			t.Errorf("statement = %s", stmt)
		}
		if view := describe("titles"); view.Type != TableTypeView || len(view.Columns) != 2 {
			t.Errorf("replaced view = %+v", view)
		}
		run(PlanDropView(d, schema, "titles", false))

		run(PlanCreateSchema(d, "jd_cat_made"))
		catalogExec(t, db, `CREATE TABLE jd_cat_made.t (id int)`)
		// Never CASCADE: a schema that still holds a table is refused.
		plan, err := PlanDropSchema(d, "jd_cat_made")
		if err != nil {
			t.Fatal(err)
		}
		if err := plan.Exec(ctx, db); err == nil {
			t.Fatal("a schema holding a table was dropped")
		}
		catalogExec(t, db, `DROP TABLE jd_cat_made.t`)
		run(PlanDropSchema(d, "jd_cat_made"))
	})

	t.Run("tables_and_columns", func(t *testing.T) {
		run(PlanCreateTable(d, schema, "made", []NewColumn{
			{Name: "id", Type: "int", PrimaryKey: true, NotNull: true},
			{Name: "label", Type: "nvarchar(max)"},
			{Name: "price", Type: "decimal(18, 2)", NotNull: true, Default: "0"},
		}))
		run(PlanAddColumn(d, schema, "made", NewColumn{Name: "seen", Type: "datetime2(3)", NotNull: true, Default: "sysutcdatetime()"}))
		made := describe("made")
		if c := catColumn(t, made, "price"); c.Type != "decimal(18,2)" || c.Nullable || c.Default != "((0))" {
			t.Errorf("price = %+v", c)
		}
		if c := catColumn(t, made, "seen"); c.Type != "datetime2(3)" || c.Default == "" {
			t.Errorf("seen = %+v", c)
		}
		// sp_rename takes the old name as one quoted, dotted string and the
		// new one bare.
		stmt := run(PlanRenameColumn(d, schema, "made", "label", "it's"))
		if stmt != "EXEC sp_rename N'[jd_cat].[made].[label]', N'it''s', 'COLUMN'" {
			t.Errorf("statement = %s", stmt)
		}
		if c := catColumn(t, describe("made"), "it's"); c.Type != "nvarchar(MAX)" {
			t.Errorf("the renamed column = %+v", c)
		}
		// The column's default constraint goes with it, in one batch: the
		// column used to be left without its default when the drop then failed.
		stmt = run(PlanDropColumn(ctx, db, d, schema, "made", "seen"))
		if !strings.Contains(stmt, "DROP CONSTRAINT") || !strings.Contains(stmt, "DROP COLUMN [seen]") {
			t.Errorf("statement = %s", stmt)
		}
		run(PlanDropColumn(ctx, db, d, schema, "made", "it's"))
		run(PlanRenameTable(d, schema, "made", "made2"))
		if made := describe("made2"); made.Schema != schema || len(made.Columns) != 2 {
			t.Errorf("renamed table = %+v", made)
		}
		run(PlanRenameTable(d, schema, "made2", "made"))
		catalogExec(t, db, `INSERT INTO jd_cat.made(id) VALUES (1)`)
		run(PlanTruncate(d, schema, "made"))
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM jd_cat.made`).Scan(&n); err != nil || n != 0 {
			t.Errorf("rows after truncate = %d, %v", n, err)
		}
		run(PlanDropTable(d, schema, "made"))
		if gone := describe("made"); len(gone.Columns) != 0 || gone.Type != "" {
			t.Errorf("the dropped table is still there: %+v", gone)
		}
	})

	// The dump replays the generated CREATE TABLE ahead of the rows, so it has
	// to be a statement SQL Server accepts, constraints and indexes included.
	t.Run("generated_ddl_replays", func(t *testing.T) {
		detail, err := Detail(ctx, db, d, schema, "posts")
		if err != nil {
			t.Fatal(err)
		}
		statements := strings.Split(strings.TrimSuffix(detail.CreateSQL, ";"), ";\n\n")
		if len(statements) != 3 {
			t.Fatalf("the replay form is %d statements, want the table and two indexes:\n%s", len(statements), detail.CreateSQL)
		}
		catalogExec(t, db, `DROP TABLE jd_cat.posts`)
		catalogExec(t, db, statements...)
		posts := describe("posts")
		if len(posts.ForeignKeys) != 1 || posts.ForeignKeys[0].OnDelete != "CASCADE" ||
			catIndex(posts, "posts_author_idx") == nil || catIndex(posts, "posts_published_title") == nil ||
			!reflect.DeepEqual(posts.PrimaryKey, []string{"id"}) || catColumn(t, posts, "published").Default != "((0))" {
			t.Errorf("posts after the replay = %+v", posts)
		}
		// users carries an alias type, an identity and a computed column; the
		// replay form declares the first by a name that resolves and leaves
		// the other two as plain columns a row can be inserted into.
		detail, err = Detail(ctx, db, d, schema, "users")
		if err != nil {
			t.Fatal(err)
		}
		catalogExec(t, db, `DROP TABLE jd_cat2.users`, `DROP VIEW jd_cat.active_users`, `DROP SYNONYM jd_cat.people`,
			`DROP FUNCTION jd_cat.posts_of`, `DROP TABLE jd_cat.posts`, `DROP TABLE jd_cat.users`)
		catalogExec(t, db, strings.Split(strings.TrimSuffix(detail.CreateSQL, ";"), ";\n\n")...)
		catalogExec(t, db, `INSERT INTO jd_cat.users(id, email, code, shout) VALUES (1, N'a@x.io', 'A', N'ANN')`)
		users := describe("users")
		if catConstraint(users, "users_email_key") == nil || catConstraint(users, "users_score_positive") == nil ||
			catColumn(t, users, "phone").Type != "jd_cat.phone" || !reflect.DeepEqual(users.PrimaryKey, []string{"id"}) {
			t.Errorf("users after the replay = %+v", users)
		}
	})
}

// The diagram and the relations map are read in bulk from the same catalogue.
func TestLiveSQLServerGraph(t *testing.T) {
	db := sqlServerLive(t)
	ctx := context.Background()
	const d = DriverMSSQL
	const schema = "jd_graph"
	drop := []string{
		`DROP TABLE IF EXISTS jd_graph2.users`, `DROP TABLE IF EXISTS jd_graph.posts`, `DROP TABLE IF EXISTS jd_graph.users`,
		`DROP SCHEMA IF EXISTS jd_graph`, `DROP SCHEMA IF EXISTS jd_graph2`,
	}
	catalogExec(t, db, drop...)
	t.Cleanup(func() {
		for _, s := range drop {
			_, _ = db.ExecContext(context.Background(), s)
		}
	})
	catalogExec(t, db,
		`CREATE SCHEMA jd_graph`, `CREATE SCHEMA jd_graph2`,
		`CREATE TABLE jd_graph.users (id bigint NOT NULL PRIMARY KEY, email nvarchar(255) NOT NULL UNIQUE)`,
		`CREATE TABLE jd_graph.posts (id int NOT NULL PRIMARY KEY,
		  author_id bigint NOT NULL CONSTRAINT posts_author FOREIGN KEY REFERENCES jd_graph.users(id) ON DELETE CASCADE)`,
		// The same name in a second schema, pointing back at the first.
		`CREATE TABLE jd_graph2.users (id int NOT NULL PRIMARY KEY, boss bigint NULL REFERENCES jd_graph.users(id))`,
	)

	t.Run("graph", func(t *testing.T) {
		graph, err := BuildSchemaGraph(ctx, db, d, schema)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, e := range graph.Edges {
			if e.From == TableKey(schema, "posts") && e.To == TableKey(schema, "users") {
				found = true
			}
		}
		if !found || graph.Truncated {
			t.Errorf("graph = %+v", graph)
		}
		relations, err := Relations(ctx, db, d, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(relations["jd_graph.posts"]) != 1 || len(relations["jd_graph2.users"]) != 1 ||
			relations["jd_graph2.users"][0].RefSchema != "jd_graph" || len(relations["jd_graph.users"]) != 0 {
			t.Errorf("relations = %+v", relations)
		}
		outline, err := OutlineWithLimit(ctx, db, d, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(outline.Tables["jd_graph.users"], []string{"id", "email"}) ||
			!reflect.DeepEqual(outline.Tables["jd_graph2.users"], []string{"id", "boss"}) {
			t.Errorf("outline = %+v", outline.Tables)
		}
	})
}
