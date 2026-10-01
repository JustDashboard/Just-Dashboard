package dbx

import (
	"context"
	"strings"
	"testing"
)

// The plans below never touch a database: every one of them is planned with a
// nil connection, which is itself the assertion that drawing up a statement
// for these engines reads nothing.

func ddlBool(v bool) *bool       { return &v }
func ddlString(v string) *string { return &v }

// planText returns a function that takes a planner's two results and yields
// the statement, failing the test on a plan that was refused.
func planText(t *testing.T) func(*DDLPlan, error) string {
	return func(plan *DDLPlan, err error) string {
		t.Helper()
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		return plan.String()
	}
}

func TestTypeValidationAcceptsTypesAndRefusesClauses(t *testing.T) {
	for _, typ := range []string{
		"text[]", "integer[3][]", "numeric(10,2)[]", "timestamp(3) with time zone",
		"Array(Nullable(String))", "LowCardinality(String)", "DateTime64(3, 'UTC')",
		"Enum8('a' = 1, 'b' = 2)", "public.citext", "character varying(64)", "bigint unsigned",
		"Map(String, UInt64)", "double precision",
	} {
		if err := validateType(typ); err != nil {
			t.Errorf("rejected legitimate type %q: %v", typ, err)
		}
	}
	for _, typ := range []string{
		// A type is a run of words, and these are where a type ends and a
		// constraint begins.
		"int primary key", "int references users", "int not null", "int default 5",
		"int unique", "int check (1)", "int auto_increment", "int generated always",
		// And these are where a type ends and another statement begins.
		"text[]; DROP TABLE x", "text[1", "int)", "int(", "enum('a\\')", "enum('a';)",
		"int'", "'a'", "text[x]", "varchar(255))", "a(((((1)))))",
		"int\x00", "", "1int", "int`", `int"`,
	} {
		if err := validateType(typ); err == nil {
			t.Errorf("accepted %q as a column type", typ)
		}
	}
}

func TestDefaultValidationAllowsACallAndNothingWider(t *testing.T) {
	for _, def := range []string{"gen_random_uuid()", "now()", "NEWID()", "uuid()", " 0 ", "CURRENT_USER"} {
		if err := validateDefault(def); err != nil {
			t.Errorf("rejected legitimate default %q: %v", def, err)
		}
	}
	for _, def := range []string{
		`'a\'`, `'\'`, "now(); DROP TABLE x", "f(1)", "f(g())", "a.b()", "now() --", "(now())", "'a' 'b'",
	} {
		if err := validateDefault(def); err == nil {
			t.Errorf("accepted %q as a default", def)
		}
	}
	// MySQL takes a function default only in parentheses, NOW() excepted.
	got := planText(t)(PlanAddColumn(DriverMySQL, "app", "t", NewColumn{Name: "id", Type: "char(36)", Default: "uuid()"}))
	if got != "ALTER TABLE `app`.`t` ADD COLUMN `id` char(36) DEFAULT (uuid())" {
		t.Errorf("mysql function default = %s", got)
	}
	got = planText(t)(PlanAddColumn(DriverMySQL, "app", "t", NewColumn{Name: "at", Type: "datetime", Default: "now()"}))
	if !strings.HasSuffix(got, "DEFAULT now()") {
		t.Errorf("mysql NOW() default = %s", got)
	}
	// A default of spaces is no default, not `DEFAULT` with nothing after it.
	got = planText(t)(PlanAddColumn(DriverPostgres, "public", "t", NewColumn{Name: "c", Type: "text", Default: "  "}))
	if strings.Contains(got, "DEFAULT") {
		t.Errorf("a blank default rendered a clause: %s", got)
	}
}

func TestFragmentCannotLeaveItsParentheses(t *testing.T) {
	for _, driver := range []Driver{DriverPostgres, DriverMySQL, DriverSQLite, DriverMSSQL, DriverOracle, DriverClickHouse} {
		for _, ok := range []string{
			"price >= 0", "(a > 0) AND (b < 10)", "status IN ('a', 'b;c', 'it''s')", `"odd;name" > 0`,
			"length(name) < 100",
		} {
			if _, err := validateFragment(driver, "the check condition", ok); err != nil {
				t.Errorf("%s refused %q: %v", driver, ok, err)
			}
		}
		for _, bad := range []string{
			"x > 0); DROP TABLE users; --", "x > 0) DROP TABLE y", "x > 0 ; DROP TABLE y",
			"x > 0 -- trailing", "x > 0 /* c */", "x */ 0", "(x > 0", "x > 0)", "'unterminated",
			`x = 'a\'`, `x = E'\''`, "x\x00", "", strings.Repeat("x", 4001),
		} {
			if _, err := validateFragment(driver, "the check condition", bad); err == nil {
				t.Errorf("%s accepted %q as a check condition", driver, bad)
			}
		}
	}
}

// What opens a quoted region differs by engine, and a scanner that quoted the
// same characters everywhere would let text hide from one of them.
func TestFragmentQuotingIsTheEnginesOwn(t *testing.T) {
	cases := []struct {
		driver Driver
		text   string
		ok     bool
	}{
		// A bracket quotes an identifier on SQL Server, so the apostrophe in it
		// is not a string and the parenthesis after it is real.
		{DriverMSSQL, "[a'b] > 0) DROP TABLE y -- '", false},
		{DriverMSSQL, "[a;b] > 0", true},
		{DriverMSSQL, "[a]]b] > 0", true},
		// On Postgres a bracket is a subscript and quotes nothing.
		{DriverPostgres, "a[1 ; DROP TABLE y; -- ]", false},
		{DriverPostgres, "tags[1] = 'x'", true},
		{DriverPostgres, "data ? 'key'", true},
		{DriverPostgres, "a::text = 'x'", true},
		// A backtick quotes on MySQL and is an operator character on Postgres.
		{DriverMySQL, "`a;b` > 0", true},
		{DriverPostgres, "` ; DROP TABLE y; -- `", false},
		// A dollar sign opens a string on Postgres that this scanner does not
		// follow, so it is refused rather than misread.
		{DriverPostgres, "$a$ ' $a$ ) ; drop table x; -- '", false},
		{DriverPostgres, "x = $$a$$", false},
		{DriverOracle, "v$x > 0", true},
		// Oracle's q-quoting ends where this scanner would not expect.
		{DriverOracle, "x = q'[it's]'", false},
		{DriverOracle, "x = :1", false},
		// A hash is a comment on MySQL and ClickHouse and an operator elsewhere.
		{DriverMySQL, "a # b", false},
		{DriverClickHouse, "a # b", false},
		{DriverPostgres, "a # b", true},
		// A question mark is a bind marker to the drivers that use them.
		{DriverMySQL, "a = ?", false},
		{DriverSQLite, "a = ?", false},
		{DriverSQLite, "[a'b] > 0", true},
	}
	for _, c := range cases {
		_, err := validateFragment(c.driver, "the expression", c.text)
		if (err == nil) != c.ok {
			t.Errorf("%s %q: accepted=%v, want %v (%v)", c.driver, c.text, err == nil, c.ok, err)
		}
	}
}

func TestLiteralsCannotCloseThemselves(t *testing.T) {
	cases := []struct {
		driver Driver
		in     string
		want   string
	}{
		{DriverPostgres, "it's", "'it''s'"},
		{DriverPostgres, `a\b`, `E'a\\b'`},
		{DriverPostgres, `a\'b`, `E'a\\''b'`},
		{DriverMySQL, `a\'b`, `'a\\''b'`},
		{DriverClickHouse, `a\`, `'a\\'`},
		{DriverMSSQL, "it's", "N'it''s'"},
		{DriverOracle, "it's", "'it''s'"},
		{DriverSQLite, `a\'b`, `'a\''b'`},
	}
	for _, c := range cases {
		got, err := ddlLiteral(c.driver, "the comment", c.in)
		if err != nil || got != c.want {
			t.Errorf("%s literal of %q = %q, %v; want %q", c.driver, c.in, got, err, c.want)
		}
	}
	if _, err := ddlLiteral(DriverPostgres, "the comment", "a\x00b"); err == nil {
		t.Error("a NUL was accepted inside a literal")
	}
}

func TestAlterColumnPlans(t *testing.T) {
	ctx := context.Background()
	got := planText(t)(PlanAlterColumn(ctx, nil, DriverPostgres, ColumnChange{
		Schema: "public", Table: "t", Column: "c", Type: "integer", Using: `"c"::integer`,
		Nullable: ddlBool(false), Default: ddlString("0"),
	}))
	want := `ALTER TABLE "public"."t" ALTER COLUMN "c" TYPE integer USING ("c"::integer), ` +
		`ALTER COLUMN "c" SET DEFAULT 0, ALTER COLUMN "c" SET NOT NULL`
	if got != want {
		t.Errorf("postgres alter =\n%s\nwant\n%s", got, want)
	}
	got = planText(t)(PlanAlterColumn(ctx, nil, DriverPostgres, ColumnChange{
		Schema: "public", Table: "t", Column: "c", Nullable: ddlBool(true), DropDefault: true,
	}))
	if got != `ALTER TABLE "public"."t" ALTER COLUMN "c" DROP DEFAULT, ALTER COLUMN "c" DROP NOT NULL` {
		t.Errorf("postgres relax = %s", got)
	}
	got = planText(t)(PlanAlterColumn(ctx, nil, DriverOracle, ColumnChange{
		Schema: "APP", Table: "T", Column: "C", Type: "NUMBER(10)", Nullable: ddlBool(false), DropDefault: true,
	}))
	if got != `ALTER TABLE "APP"."T" MODIFY ("C" NUMBER(10) DEFAULT NULL NOT NULL)` {
		t.Errorf("oracle alter = %s", got)
	}
	got = planText(t)(PlanAlterColumn(ctx, nil, DriverClickHouse, ColumnChange{
		Schema: "db", Table: "t", Column: "c", Type: "Nullable(String)", DropDefault: true,
	}))
	if got != "ALTER TABLE `db`.`t` MODIFY COLUMN `c` Nullable(String);\nALTER TABLE `db`.`t` MODIFY COLUMN `c` REMOVE DEFAULT" {
		t.Errorf("clickhouse alter = %s", got)
	}
	// MySQL changes a default alone without restating the column, so it needs
	// no catalogue read.
	got = planText(t)(PlanAlterColumn(ctx, nil, DriverMySQL, ColumnChange{
		Schema: "app", Table: "t", Column: "c", Default: ddlString("'x'"),
	}))
	if got != "ALTER TABLE `app`.`t` ALTER COLUMN `c` SET DEFAULT 'x'" {
		t.Errorf("mysql default = %s", got)
	}

	refused := []struct {
		driver Driver
		change ColumnChange
		says   string
	}{
		{DriverSQLite, ColumnChange{Table: "t", Column: "c", Type: "TEXT"}, "SQLite cannot change a column"},
		{DriverClickHouse, ColumnChange{Table: "t", Column: "c", Nullable: ddlBool(true)}, "Nullable(T)"},
		{DriverPostgres, ColumnChange{Table: "t", Column: "c"}, "nothing to change"},
		{DriverPostgres, ColumnChange{Table: "t", Column: "c", Using: "c::int"}, "no type was given"},
		{DriverMySQL, ColumnChange{Table: "t", Column: "c", Type: "int", Using: "c"}, "Postgres's"},
		{DriverPostgres, ColumnChange{Table: "t", Column: "c", Type: "int", Using: "c::int); DROP TABLE x; --"}, "USING expression"},
		{DriverPostgres, ColumnChange{Table: "t", Column: "c", Type: "int; DROP TABLE x"}, "not one this form can build"},
		{DriverPostgres, ColumnChange{Table: "t", Column: "c", Default: ddlString("1"), DropDefault: true}, "set and dropped"},
		{DriverPostgres, ColumnChange{Table: "t", Column: "c", Default: ddlString("(select 1)")}, "not a plain literal"},
	}
	for _, c := range refused {
		_, err := PlanAlterColumn(ctx, nil, c.driver, c.change)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s %+v: error = %v, want one saying %q", c.driver, c.change, err, c.says)
		}
	}
	if !(ColumnChange{Type: "int"}).ChangesType() || (ColumnChange{Nullable: ddlBool(true)}).ChangesType() {
		t.Error("ChangesType does not single out a change of type")
	}
}

func TestForeignKeyPlans(t *testing.T) {
	spec := ForeignKeySpec{
		Schema: "public", Table: "posts", Columns: []string{"author_id"},
		RefTable: "users", RefColumns: []string{"id"}, OnDelete: "cascade", OnUpdate: "no action",
	}
	got := planText(t)(PlanAddForeignKey(DriverPostgres, spec))
	want := `ALTER TABLE "public"."posts" ADD CONSTRAINT "posts_author_id_fkey" FOREIGN KEY ("author_id") ` +
		`REFERENCES "public"."users" ("id") ON DELETE CASCADE`
	if got != want {
		t.Errorf("postgres add fk =\n%s\nwant\n%s", got, want)
	}
	spec.RefSchema, spec.Name, spec.OnUpdate = "auth", "fk_author", "Set  Null"
	got = planText(t)(PlanAddForeignKey(DriverMySQL, spec))
	want = "ALTER TABLE `public`.`posts` ADD CONSTRAINT `fk_author` FOREIGN KEY (`author_id`) " +
		"REFERENCES `auth`.`users` (`id`) ON DELETE CASCADE ON UPDATE SET NULL"
	if got != want {
		t.Errorf("mysql add fk =\n%s\nwant\n%s", got, want)
	}
	if got := planText(t)(PlanDropForeignKey(DriverMySQL, "app", "posts", "fk_author")); got != "ALTER TABLE `app`.`posts` DROP FOREIGN KEY `fk_author`" {
		t.Errorf("mysql drop fk = %s", got)
	}
	if got := planText(t)(PlanDropForeignKey(DriverMSSQL, "dbo", "posts", "fk_author")); got != "ALTER TABLE [dbo].[posts] DROP CONSTRAINT [fk_author]" {
		t.Errorf("mssql drop fk = %s", got)
	}

	for _, c := range []struct {
		driver Driver
		mutate func(*ForeignKeySpec)
		says   string
	}{
		{DriverPostgres, func(s *ForeignKeySpec) { s.OnDelete = "CASCADE; DROP TABLE x" }, "not a referential action"},
		{DriverPostgres, func(s *ForeignKeySpec) { s.RefColumns = []string{"id", "tenant"} }, "pairs each column"},
		{DriverPostgres, func(s *ForeignKeySpec) { s.RefTable = "" }, "references is required"},
		{DriverPostgres, func(s *ForeignKeySpec) { s.Columns, s.RefColumns = nil, nil }, "at least one column"},
		{DriverMSSQL, func(s *ForeignKeySpec) { s.OnDelete = "RESTRICT" }, "no RESTRICT"},
		{DriverOracle, func(s *ForeignKeySpec) { s.OnUpdate = "CASCADE" }, "no ON UPDATE"},
		{DriverOracle, func(s *ForeignKeySpec) { s.OnDelete = "SET DEFAULT" }, "CASCADE or SET NULL"},
		{DriverSQLite, func(*ForeignKeySpec) {}, "SQLite cannot add or drop a foreign key"},
		{DriverClickHouse, func(*ForeignKeySpec) {}, "no foreign keys"},
	} {
		s := ForeignKeySpec{Table: "posts", Columns: []string{"a"}, RefTable: "users", RefColumns: []string{"id"}}
		c.mutate(&s)
		_, err := PlanAddForeignKey(c.driver, s)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: error = %v, want one saying %q", c.driver, err, c.says)
		}
	}
}

func TestConstraintPlans(t *testing.T) {
	ctx := context.Background()
	got := planText(t)(PlanAddConstraint(DriverPostgres, ConstraintSpec{
		Schema: "public", Table: "users", Type: "unique", Columns: []string{"tenant", "email"},
	}))
	if got != `ALTER TABLE "public"."users" ADD CONSTRAINT "users_tenant_email_key" UNIQUE ("tenant", "email")` {
		t.Errorf("unique = %s", got)
	}
	got = planText(t)(PlanAddConstraint(DriverMSSQL, ConstraintSpec{
		Schema: "dbo", Table: "items", Type: "check", Name: "price_positive", Expression: " [price] >= 0 ",
	}))
	if got != "ALTER TABLE [dbo].[items] ADD CONSTRAINT [price_positive] CHECK ([price] >= 0)" {
		t.Errorf("check = %s", got)
	}
	got = planText(t)(PlanAddConstraint(DriverClickHouse, ConstraintSpec{
		Schema: "db", Table: "t", Type: "check", Expression: "n > 0",
	}))
	if got != "ALTER TABLE `db`.`t` ADD CONSTRAINT `t_check` CHECK (n > 0)" {
		t.Errorf("clickhouse check = %s", got)
	}
	// Every engine but MySQL drops a constraint the same way and needs no
	// catalogue to know how.
	got = planText(t)(PlanDropConstraint(ctx, nil, DriverPostgres, "public", "users", "users_email_key", ""))
	if got != `ALTER TABLE "public"."users" DROP CONSTRAINT "users_email_key"` {
		t.Errorf("drop = %s", got)
	}
	got = planText(t)(PlanDropConstraint(ctx, nil, DriverMySQL, "app", "users", "email", "unique"))
	if got != "ALTER TABLE `app`.`users` DROP INDEX `email`" {
		t.Errorf("mysql drop unique = %s", got)
	}
	got = planText(t)(PlanDropConstraint(ctx, nil, DriverMySQL, "app", "users", "age_ok", "check"))
	if got != "ALTER TABLE `app`.`users` DROP CONSTRAINT `age_ok`" {
		t.Errorf("mysql drop check = %s", got)
	}

	for _, c := range []struct {
		driver Driver
		spec   ConstraintSpec
		says   string
	}{
		{DriverPostgres, ConstraintSpec{Table: "t", Type: "check", Expression: "x > 0); DROP TABLE users; --"}, "closes a parenthesis"},
		{DriverPostgres, ConstraintSpec{Table: "t", Type: "check"}, "is required"},
		{DriverPostgres, ConstraintSpec{Table: "t", Type: "primary"}, `"unique" or "check"`},
		{DriverPostgres, ConstraintSpec{Table: "t", Type: "unique"}, "at least one column"},
		{DriverSQLite, ConstraintSpec{Table: "t", Type: "unique", Columns: []string{"a"}}, "A unique index does the same job"},
		{DriverSQLite, ConstraintSpec{Table: "t", Type: "check", Expression: "a > 0"}, "SQLite cannot add or drop a check"},
		{DriverClickHouse, ConstraintSpec{Table: "t", Type: "unique", Columns: []string{"a"}}, "no unique constraints"},
	} {
		_, err := PlanAddConstraint(c.driver, c.spec)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s %+v: error = %v, want one saying %q", c.driver, c.spec, err, c.says)
		}
	}
	if _, err := PlanDropConstraint(ctx, nil, DriverSQLite, "", "t", "c", ""); err == nil {
		t.Error("SQLite accepted a constraint drop it cannot perform")
	}
}

func TestViewPlans(t *testing.T) {
	const query = "SELECT id, email FROM users WHERE active"
	for _, c := range []struct {
		driver  Driver
		replace bool
		want    string
	}{
		{DriverPostgres, false, `CREATE VIEW "public"."v" AS` + "\n" + query},
		{DriverPostgres, true, `CREATE OR REPLACE VIEW "public"."v" AS` + "\n" + query},
		{DriverMySQL, true, "CREATE OR REPLACE VIEW `public`.`v` AS\n" + query},
		{DriverMSSQL, true, "CREATE OR ALTER VIEW [public].[v] AS\n" + query},
		{DriverSQLite, false, `CREATE VIEW "v" AS` + "\n" + query},
		{DriverClickHouse, true, "CREATE OR REPLACE VIEW `public`.`v` AS\n" + query},
		{DriverOracle, true, `CREATE OR REPLACE VIEW "public"."v" AS` + "\n" + query},
	} {
		got := planText(t)(PlanCreateView(c.driver, ViewSpec{Schema: "public", Name: "v", Query: query + " ; ", Replace: c.replace}))
		if got != c.want {
			t.Errorf("%s view =\n%s\nwant\n%s", c.driver, got, c.want)
		}
	}
	got := planText(t)(PlanCreateView(DriverPostgres, ViewSpec{Schema: "public", Name: "m", Query: query, Materialized: true}))
	if got != `CREATE MATERIALIZED VIEW "public"."m" AS`+"\n"+query {
		t.Errorf("materialized view = %s", got)
	}
	if got := planText(t)(PlanDropView(DriverPostgres, "public", "m", true)); got != `DROP MATERIALIZED VIEW "public"."m"` {
		t.Errorf("drop materialized = %s", got)
	}
	if got := planText(t)(PlanDropView(DriverMySQL, "app", "v", false)); got != "DROP VIEW `app`.`v`" {
		t.Errorf("drop view = %s", got)
	}

	// A view form must not be a way to run anything but the one SELECT that
	// defines the view.
	for _, bad := range []string{
		"SELECT 1; DROP TABLE users",
		"DROP TABLE users",
		"INSERT INTO t VALUES (1)",
		"WITH gone AS (DELETE FROM users RETURNING *) SELECT * FROM gone",
		"UPDATE users SET name = 'x'",
		"SHOW TABLES",
		"EXPLAIN SELECT 1",
		"/* c */ CALL refresh()",
		"SELECT $$a$$",
		"",
	} {
		if _, err := PlanCreateView(DriverPostgres, ViewSpec{Name: "v", Query: bad}); err == nil {
			t.Errorf("accepted %q as a view's query", bad)
		}
	}
	if _, err := PlanCreateView(DriverPostgres, ViewSpec{Name: "v", Query: "-- newest first\n(SELECT 1)"}); err != nil {
		t.Errorf("refused a commented, parenthesised SELECT: %v", err)
	}
	for _, c := range []struct {
		driver Driver
		spec   ViewSpec
		says   string
	}{
		{DriverSQLite, ViewSpec{Name: "v", Query: query, Replace: true}, "no CREATE OR REPLACE VIEW"},
		{DriverMySQL, ViewSpec{Name: "v", Query: query, Materialized: true}, "no materialized views"},
		{DriverClickHouse, ViewSpec{Name: "v", Query: query, Materialized: true}, "target table"},
		{DriverPostgres, ViewSpec{Name: "v", Query: query, Materialized: true, Replace: true}, "cannot replace a materialized view"},
	} {
		_, err := PlanCreateView(c.driver, c.spec)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s %+v: error = %v, want one saying %q", c.driver, c.spec, err, c.says)
		}
	}
}

func TestSchemaAndEnumPlans(t *testing.T) {
	if got := planText(t)(PlanCreateSchema(DriverPostgres, "reporting")); got != `CREATE SCHEMA "reporting"` {
		t.Errorf("create schema = %s", got)
	}
	// Never CASCADE: the engine's refusal of a schema that still holds
	// something is the guard.
	if got := planText(t)(PlanDropSchema(DriverMSSQL, "reporting")); got != "DROP SCHEMA [reporting]" {
		t.Errorf("drop schema = %s", got)
	}
	for _, driver := range []Driver{DriverMySQL, DriverSQLite, DriverOracle, DriverClickHouse} {
		if _, err := PlanCreateSchema(driver, "x"); err == nil {
			t.Errorf("%s accepted CREATE SCHEMA, which there is a database, a user or nothing", driver)
		}
		if _, err := PlanDropSchema(driver, "x"); err == nil {
			t.Errorf("%s accepted DROP SCHEMA", driver)
		}
	}

	got := planText(t)(PlanCreateEnum(DriverPostgres, "public", "mood", []string{"sad", "it's ok", "happy"}))
	if got != `CREATE TYPE "public"."mood" AS ENUM ('sad', 'it''s ok', 'happy')` {
		t.Errorf("create enum = %s", got)
	}
	got = planText(t)(PlanAddEnumValue(DriverPostgres, EnumValueSpec{
		Schema: "public", Name: "mood", Value: "meh", After: "sad", IfNotExists: true,
	}))
	if got != `ALTER TYPE "public"."mood" ADD VALUE IF NOT EXISTS 'meh' AFTER 'sad'` {
		t.Errorf("add value = %s", got)
	}
	for _, c := range []struct {
		err  error
		says string
	}{
		{planErr(PlanCreateEnum(DriverPostgres, "", "mood", nil)), "at least one label"},
		{planErr(PlanCreateEnum(DriverPostgres, "", "mood", []string{"a", "a"})), "listed twice"},
		{planErr(PlanCreateEnum(DriverPostgres, "", "mood", []string{""})), "cannot be empty"},
		{planErr(PlanCreateEnum(DriverPostgres, "", "mood", []string{strings.Repeat("x", 64)})), "at most 63"},
		{planErr(PlanCreateEnum(DriverMySQL, "", "mood", []string{"a"})), "declared on the column"},
		{planErr(PlanAddEnumValue(DriverPostgres, EnumValueSpec{Name: "mood", Value: "x", Before: "a", After: "b"})), "not both"},
	} {
		if c.err == nil || !strings.Contains(c.err.Error(), c.says) {
			t.Errorf("error = %v, want one saying %q", c.err, c.says)
		}
	}
}

func planErr(_ *DDLPlan, err error) error { return err }

func TestCommentPlans(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		driver Driver
		spec   CommentSpec
		want   string
	}{
		{DriverPostgres, CommentSpec{Schema: "public", Table: "users", Comment: "People's accounts"},
			`COMMENT ON TABLE "public"."users" IS 'People''s accounts'`},
		{DriverPostgres, CommentSpec{Schema: "public", Table: "users", Column: "email"},
			`COMMENT ON COLUMN "public"."users"."email" IS NULL`},
		{DriverOracle, CommentSpec{Schema: "APP", Table: "USERS", Column: "EMAIL", Comment: "login"},
			`COMMENT ON COLUMN "APP"."USERS"."EMAIL" IS 'login'`},
		{DriverMySQL, CommentSpec{Schema: "app", Table: "users", Comment: `a\b`},
			"ALTER TABLE `app`.`users` COMMENT = 'a\\\\b'"},
		{DriverClickHouse, CommentSpec{Schema: "db", Table: "t", Comment: "x"},
			"ALTER TABLE `db`.`t` MODIFY COMMENT 'x'"},
		{DriverClickHouse, CommentSpec{Schema: "db", Table: "t", Column: "c", Comment: "x"},
			"ALTER TABLE `db`.`t` COMMENT COLUMN `c` 'x'"},
	} {
		if got := planText(t)(PlanComment(ctx, nil, c.driver, c.spec)); got != c.want {
			t.Errorf("%s comment =\n%s\nwant\n%s", c.driver, got, c.want)
		}
	}
	got := planText(t)(PlanComment(ctx, nil, DriverMSSQL, CommentSpec{Table: "it's", Column: "c]d", Comment: "x'y"}))
	for _, want := range []string{
		"sp_updateextendedproperty", "sp_addextendedproperty",
		"OBJECT_ID(N'[dbo].[it''s]')", "@level1name = N'it''s'", "@level2name = N'c]d'", "@value = N'x''y'",
		"@level0name = N'dbo'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("mssql comment is missing %q in:\n%s", want, got)
		}
	}
	if _, err := PlanComment(ctx, nil, DriverSQLite, CommentSpec{Table: "t", Comment: "x"}); err == nil ||
		!strings.Contains(err.Error(), "SQLite stores no comments") {
		t.Errorf("sqlite comment error = %v", err)
	}
	if _, err := PlanComment(ctx, nil, DriverPostgres, CommentSpec{Table: "t", Comment: strings.Repeat("x", 4001)}); err == nil {
		t.Error("an oversized comment was accepted")
	}
}

func TestIndexPlans(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		driver Driver
		spec   IndexSpec
		want   string
	}{
		{DriverPostgres, IndexSpec{Schema: "public", Table: "docs", Name: "docs_body_idx", Columns: []string{"body"},
			Unique: true, Method: "GIN", Where: "deleted_at IS NULL", IfNotExists: true, Concurrently: true},
			`CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS "docs_body_idx" ON "public"."docs" USING gin ("body") WHERE (deleted_at IS NULL)`},
		{DriverPostgres, IndexSpec{Schema: "public", Table: "docs", Columns: []string{"a", "b"}},
			`CREATE INDEX "docs_a_b_idx" ON "public"."docs" ("a", "b")`},
		{DriverMySQL, IndexSpec{Schema: "app", Table: "docs", Name: "ft", Columns: []string{"body"}, Method: "fulltext"},
			"CREATE FULLTEXT INDEX `ft` ON `app`.`docs` (`body`)"},
		{DriverMySQL, IndexSpec{Schema: "app", Table: "docs", Name: "h", Columns: []string{"k"}, Unique: true, Method: "hash"},
			"CREATE UNIQUE INDEX `h` ON `app`.`docs` (`k`) USING HASH"},
		{DriverSQLite, IndexSpec{Schema: "main", Table: "docs", Name: "live", Columns: []string{"k"}, Where: "live = 1", IfNotExists: true},
			`CREATE INDEX IF NOT EXISTS "live" ON "docs" ("k") WHERE (live = 1)`},
		{DriverMSSQL, IndexSpec{Schema: "dbo", Table: "docs", Name: "live", Columns: []string{"k"}, Method: "nonclustered",
			Where: "[live] = 1", Concurrently: true},
			"CREATE NONCLUSTERED INDEX [live] ON [dbo].[docs] ([k]) WHERE ([live] = 1) WITH (ONLINE = ON)"},
		{DriverOracle, IndexSpec{Schema: "APP", Table: "DOCS", Name: "DOCS_K", Columns: []string{"K"}, Concurrently: true},
			`CREATE INDEX "APP"."DOCS_K" ON "APP"."DOCS" ("K") ONLINE`},
		{DriverOracle, IndexSpec{Schema: "APP", Table: "DOCS", Name: "DOCS_B", Columns: []string{"K"}, Method: "bitmap"},
			`CREATE BITMAP INDEX "APP"."DOCS_B" ON "APP"."DOCS" ("K")`},
	} {
		if got := planText(t)(PlanCreateIndex(ctx, nil, c.driver, c.spec)); got != c.want {
			t.Errorf("%s index =\n%s\nwant\n%s", c.driver, got, c.want)
		}
	}
	for _, c := range []struct {
		driver Driver
		spec   IndexSpec
		says   string
	}{
		{DriverPostgres, IndexSpec{Table: "t", Columns: []string{"a"}, Method: "btree) ; DROP TABLE x; --"}, "not an index method name"},
		{DriverPostgres, IndexSpec{Table: "t", Columns: []string{"a"}, Where: "a > 0); DROP TABLE x; --"}, "closes a parenthesis"},
		{DriverPostgres, IndexSpec{Table: "t"}, "at least one column"},
		{DriverMySQL, IndexSpec{Table: "t", Columns: []string{"a"}, Where: "a > 0"}, "no partial indexes"},
		{DriverMySQL, IndexSpec{Table: "t", Columns: []string{"a"}, Concurrently: true}, "no CONCURRENTLY"},
		{DriverMySQL, IndexSpec{Table: "t", Columns: []string{"a"}, Method: "gin"}, "not an index method this form offers"},
		{DriverMySQL, IndexSpec{Table: "t", Columns: []string{"a"}, Method: "fulltext", Unique: true}, "cannot be unique"},
		{DriverSQLite, IndexSpec{Table: "t", Columns: []string{"a"}, Method: "btree"}, "one kind of index"},
		{DriverSQLite, IndexSpec{Table: "t", Columns: []string{"a"}, Concurrently: true}, "no CONCURRENTLY"},
		{DriverMSSQL, IndexSpec{Table: "t", Columns: []string{"a"}, IfNotExists: true}, "no CREATE INDEX IF NOT EXISTS"},
		{DriverOracle, IndexSpec{Table: "T", Columns: []string{"A"}, Where: "A > 0"}, "no partial indexes"},
		{DriverClickHouse, IndexSpec{Table: "t", Columns: []string{"a"}}, "data-skipping index"},
	} {
		_, err := PlanCreateIndex(ctx, nil, c.driver, c.spec)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s %+v: error = %v, want one saying %q", c.driver, c.spec, err, c.says)
		}
	}
	for _, c := range []struct {
		driver Driver
		want   string
	}{
		{DriverPostgres, `DROP INDEX "s"."i"`},
		{DriverSQLite, `DROP INDEX "i"`},
		{DriverOracle, `DROP INDEX "s"."i"`},
		{DriverMySQL, "DROP INDEX `i` ON `s`.`t`"},
		{DriverMSSQL, "DROP INDEX [i] ON [s].[t]"},
		{DriverClickHouse, "ALTER TABLE `s`.`t` DROP INDEX `i`"},
	} {
		if got := planText(t)(PlanDropIndex(c.driver, "s", "t", "i")); got != c.want {
			t.Errorf("%s drop index = %s, want %s", c.driver, got, c.want)
		}
	}
}

// A rename must leave the table where it was. MySQL reads an unqualified
// target against the connection's database, so the old statement moved the
// table into whichever database the pool was using.
func TestRenamePlansKeepTheTableInItsSchema(t *testing.T) {
	for _, c := range []struct {
		driver Driver
		want   string
	}{
		{DriverMySQL, "RENAME TABLE `shop`.`orders` TO `shop`.`purchases`"},
		{DriverClickHouse, "RENAME TABLE `shop`.`orders` TO `shop`.`purchases`"},
		{DriverPostgres, `ALTER TABLE "shop"."orders" RENAME TO "purchases"`},
		{DriverSQLite, `ALTER TABLE "orders" RENAME TO "purchases"`},
		{DriverOracle, `ALTER TABLE "shop"."orders" RENAME TO "purchases"`},
		{DriverMSSQL, "EXEC sp_rename N'[shop].[orders]', N'purchases'"},
	} {
		if got := planText(t)(PlanRenameTable(c.driver, "shop", "orders", "purchases")); got != c.want {
			t.Errorf("%s rename table = %s, want %s", c.driver, got, c.want)
		}
	}
	got := planText(t)(PlanRenameColumn(DriverMSSQL, "dbo", "it's", "a.b", "c'd"))
	if got != "EXEC sp_rename N'[dbo].[it''s].[a.b]', N'c''d', 'COLUMN'" {
		t.Errorf("mssql rename column = %s", got)
	}
	got = planText(t)(PlanRenameColumn(DriverClickHouse, "db", "t", "a", "b"))
	if got != "ALTER TABLE `db`.`t` RENAME COLUMN `a` TO `b`" {
		t.Errorf("clickhouse rename column = %s", got)
	}
}

// ClickHouse is the engine a generic statement is most likely to be wrong for,
// and its create-table form was the only one that used to say so. Every
// operation now either refuses it by name or sends ClickHouse's own form.
func TestClickHouseGetsItsOwnStatementsOrARefusal(t *testing.T) {
	ctx := context.Background()
	const d = DriverClickHouse
	plans := map[string]func() (*DDLPlan, error){
		OpCreateTable: func() (*DDLPlan, error) {
			return PlanCreateTable(d, "db", "t", []NewColumn{{Name: "c", Type: "String"}})
		},
		OpCreateIndex: func() (*DDLPlan, error) {
			return PlanCreateIndex(ctx, nil, d, IndexSpec{Schema: "db", Table: "t", Columns: []string{"c"}})
		},
		OpForeignKeys: func() (*DDLPlan, error) { return PlanDropForeignKey(d, "db", "t", "fk") },
		OpUnique: func() (*DDLPlan, error) {
			return PlanAddConstraint(d, ConstraintSpec{Table: "t", Type: "unique", Columns: []string{"c"}})
		},
		OpSchemas:   func() (*DDLPlan, error) { return PlanCreateSchema(d, "x") },
		OpEnumTypes: func() (*DDLPlan, error) { return PlanCreateEnum(d, "db", "e", []string{"a"}) },
	}
	for op, plan := range plans {
		if SupportsOperation(d, op) {
			t.Errorf("clickhouse advertises %s", op)
		}
		if p, err := plan(); err == nil {
			t.Errorf("clickhouse was sent a statement for %s: %s", op, p)
		}
	}
	for op, c := range map[string]struct {
		plan func() (*DDLPlan, error)
		want string
	}{
		OpDropTable: {func() (*DDLPlan, error) { return PlanDropTable(d, "db", "t") }, "DROP TABLE `db`.`t`"},
		OpTruncate:  {func() (*DDLPlan, error) { return PlanTruncate(d, "db", "t") }, "TRUNCATE TABLE `db`.`t`"},
		// No NOT NULL: a ClickHouse column refuses NULL unless its type says
		// otherwise, and a new one needs no default to satisfy existing rows.
		OpAddColumn: {func() (*DDLPlan, error) {
			return PlanAddColumn(d, "db", "t", NewColumn{Name: "n", Type: "UInt32", NotNull: true})
		}, "ALTER TABLE `db`.`t` ADD COLUMN `n` UInt32"},
		OpDropColumn: {func() (*DDLPlan, error) { return PlanDropColumn(ctx, nil, d, "db", "t", "n") },
			"ALTER TABLE `db`.`t` DROP COLUMN `n`"},
	} {
		if !SupportsOperation(d, op) {
			t.Errorf("clickhouse does not advertise %s", op)
		}
		if got := planText(t)(c.plan()); got != c.want {
			t.Errorf("clickhouse %s = %s, want %s", op, got, c.want)
		}
	}
}

func TestDDLOperationsDescribeEachEngine(t *testing.T) {
	has := func(driver Driver, op string) bool {
		for _, o := range DDLOperations(driver) {
			if o == op {
				return true
			}
		}
		return false
	}
	for _, c := range []struct {
		driver Driver
		op     string
		want   bool
	}{
		{DriverPostgres, OpEnumTypes, true}, {DriverPostgres, OpSchemas, true},
		{DriverPostgres, OpMaterialized, true}, {DriverPostgres, OpIndexOnline, true},
		{DriverMySQL, OpForeignKeys, true}, {DriverMySQL, OpSchemas, false}, {DriverMySQL, OpIndexPartial, false},
		{DriverSQLite, OpAlterColumn, false}, {DriverSQLite, OpForeignKeys, false},
		{DriverSQLite, OpCreateIndex, true}, {DriverSQLite, OpIndexPartial, true}, {DriverSQLite, OpViews, true},
		{DriverMSSQL, OpSchemas, true}, {DriverMSSQL, OpIndexIfMissing, false},
		{DriverOracle, OpAlterColumn, true}, {DriverOracle, OpSchemas, false},
		{DriverClickHouse, OpCreateTable, false}, {DriverClickHouse, OpCheck, true},
		{DriverClickHouse, OpAlterColumn, true}, {DriverClickHouse, OpViews, true},
		{DriverMongo, OpCreateTable, false}, {DriverRedis, OpViews, false},
	} {
		if got := has(c.driver, c.op); got != c.want || got != SupportsOperation(c.driver, c.op) {
			t.Errorf("%s %s: listed=%v supported=%v, want %v", c.driver, c.op, got, SupportsOperation(c.driver, c.op), c.want)
		}
	}
	// A refusal is worded for the operator: it names the engine.
	for driver, refusals := range ddlRefusals {
		for op, reason := range refusals {
			if len(reason) < 20 {
				t.Errorf("%s %s: the refusal %q explains nothing", driver, op, reason)
			}
		}
	}
}

func TestGeneratedNamesFitTheShortestLimit(t *testing.T) {
	long := strings.Repeat("ünïcode_", 12)
	name := generatedName(long, []string{"column_one", "column_two"}, "fkey")
	if len(name) > 63 || !strings.HasSuffix(name, "_fkey") {
		t.Errorf("generated name %q is %d bytes", name, len(name))
	}
	if name := generatedName("posts", []string{"author_id"}, "fkey"); name != "posts_author_id_fkey" {
		t.Errorf("generated name = %q", name)
	}
}

// A plan with several statements stops at the first that fails and says which.
func TestPlanStopsAtTheFirstFailure(t *testing.T) {
	db, _ := openTestDB(t)
	plan := planOf(`CREATE TABLE plan_a (id INTEGER)`, `CREATE TABLE plan_a (id INTEGER)`, `CREATE TABLE plan_b (id INTEGER)`)
	err := plan.Exec(context.Background(), db)
	if err == nil || !strings.Contains(err.Error(), "statement 2 of 3") {
		t.Fatalf("error = %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'plan_b'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("a statement after the failure ran: %d, %v", n, err)
	}
	if err := (&DDLPlan{}).Exec(context.Background(), db); err == nil {
		t.Error("an empty plan ran")
	}
}
