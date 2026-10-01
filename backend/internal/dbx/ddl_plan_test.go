package dbx

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
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

var allSQLDrivers = []Driver{DriverPostgres, DriverMySQL, DriverSQLite, DriverMSSQL, DriverOracle, DriverClickHouse}

func TestTypeValidationAcceptsTypesAndRefusesClauses(t *testing.T) {
	for driver, types := range map[Driver][]string{
		DriverPostgres: {
			"text[]", "integer[3][]", "numeric(10,2)[]", "timestamp(3) with time zone", "time without time zone",
			"public.citext", "character varying(64)", "double precision", "geometry(Point, 4326)", "vector(1536)",
			"interval day to second", "interval day to second(3)", "bit varying(8)", "serial", "shop.status[]",
		},
		DriverMySQL: {
			"bigint unsigned", "int(11) unsigned zerofill", "enum('a','b')", "set('x', 'y')", "enum('it''s')",
			"varchar(64) character set utf8mb4", "text charset latin1", "datetime(6)", "double precision",
			"national varchar(10)", "json",
		},
		DriverSQLite: {"INTEGER", "VARCHAR(255)", "UNSIGNED BIG INT", "DECIMAL(10,5)", "DOUBLE PRECISION"},
		DriverMSSQL:  {"nvarchar(max)", "decimal(18, 2)", "datetime2(7)", "uniqueidentifier", "dbo.PhoneNumber"},
		DriverOracle: {
			"VARCHAR2(100 CHAR)", "NUMBER(18,2)", "TIMESTAMP(6) WITH LOCAL TIME ZONE", "LONG RAW",
			"INTERVAL YEAR(2) TO MONTH", "INTERVAL DAY(2) TO SECOND(6)", "HR.ADDRESS_T",
		},
		DriverClickHouse: {
			"Array(Nullable(String))", "LowCardinality(String)", "DateTime64(3, 'UTC')", "Enum8('a' = 1, 'b' = -2)",
			"Map(String, UInt64)", "Tuple(a String, b Array(UInt8))", "Decimal(10, 2)", "FixedString(16)",
			"AggregateFunction(uniq, UInt64)",
		},
	} {
		for _, typ := range types {
			if err := validateType(driver, typ); err != nil {
				t.Errorf("%s rejected legitimate type %q: %v", driver, typ, err)
			}
		}
	}
	for _, driver := range allSQLDrivers {
		for _, typ := range []string{
			// A type is followed by the rest of the statement. Whatever a
			// "type" carries after its name is read as that rest: another
			// action of the same ALTER TABLE …
			"integer, DROP COLUMN email", "integer, DROP COLUMN email CASCADE", "integer, DISABLE ROW LEVEL SECURITY",
			"integer, DISABLE TRIGGER ALL", "integer, OWNER TO postgres", "int, CONVERT TO CHARACTER SET latin1",
			"int, RENAME TO gone", "number, DROP COLUMN email", "UInt8, DROP PARTITION ID 'all'",
			"int, ADD COLUMN y int", "int ,", "int,",
			// … another statement, on the engine that needs nothing between two …
			"int DROP TABLE users", "int EXEC('DROP TABLE dbo.users')", "int TRUNCATE TABLE users",
			"int) DROP TABLE users", "EXEC('DROP TABLE dbo.users')", "int EXEC sp_who", "int GO",
			"exec", "drop", "truncate", "int with", "int to", "set", "int set",
			// … a constraint or a column option …
			"int primary key", "int references users", "int not null", "int default 5", "default(5)",
			"int unique", "unique", "int check (1)", "check(1)", "int auto_increment", "int generated always",
			"int collate x", "int comment 'x'", "int first", "int after id", "int codec(ZSTD)", "int ttl d",
			"int identity(1,1)", "int as (1)", "references(users)",
			// … or what a type never is.
			"text[]; DROP TABLE x", "text[1", "int)", "int(", "enum('a\\')", "enum('a';)", "enum('a'", "int()",
			"int'", "'a'", "text[x]", "varchar(255))", "a(((((1)))))", "varchar(255)(1)", "a.b.c", "a.", ".a",
			"int\x00", "", " ", "1int", "int`", `int"`, "int--", "int/**/", "int;", "int = 1", "a=b",
			"varchar(10) with time zone(3)", "int\tunsigned", "int\nunsigned", strings.Repeat("a", 201),
		} {
			if err := validateType(driver, typ); err == nil {
				t.Errorf("%s accepted %q as a column type", driver, typ)
			}
		}
	}
	// What one engine's types take is not what another's do.
	for _, c := range []struct {
		driver Driver
		typ    string
	}{
		{DriverPostgres, "enum('a','b')"}, {DriverMSSQL, "varchar('max')"}, {DriverOracle, "set('a')"},
		{DriverPostgres, "Array(Nullable(String))"}, {DriverMSSQL, "Enum8('a' = 1)"},
		{DriverMySQL, "text[]"}, {DriverMSSQL, "int[]"}, {DriverClickHouse, "String[]"},
		{DriverMySQL, "app.status"}, {DriverSQLite, "main.thing"}, {DriverClickHouse, "db.Thing"},
		{DriverPostgres, "varchar(10) character set utf8"}, {DriverPostgres, "set('a')"},
		{DriverPostgres, "public.citext unsigned"}, {DriverMySQL, "varchar(10) character set utf8; x"},
	} {
		if err := validateType(c.driver, c.typ); err == nil {
			t.Errorf("%s accepted %q as a column type", c.driver, c.typ)
		}
	}
	// And the planners that write a type refuse with it: no statement is drawn
	// up at all, so there is nothing to run and nothing to preview.
	for _, driver := range allSQLDrivers {
		const smuggled = "integer, DROP COLUMN email"
		if p, err := PlanAddColumn(driver, "", "users", NewColumn{Name: "x", Type: smuggled}); err == nil {
			t.Errorf("%s planned an add-column carrying a second action: %s", driver, p)
		}
		if p, err := PlanCreateTable(driver, "", "t", []NewColumn{{Name: "x", Type: "int) DROP TABLE users"}}); err == nil {
			t.Errorf("%s planned a create-table carrying a second statement: %s", driver, p)
		}
		if driver == DriverSQLite {
			continue
		}
		if p, err := PlanAlterColumn(context.Background(), nil, driver,
			ColumnChange{Table: "users", Column: "x", Type: smuggled}); err == nil {
			t.Errorf("%s planned an alter-column carrying a second action: %s", driver, p)
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
	for _, driver := range allSQLDrivers {
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

// A condition or a default is SQL the engine runs against rows, so a call in
// one is a call that happens. The form vouches for a short list of functions
// that only compute; a plan that calls anything else says so, and the route
// treats it as the arbitrary SQL it is.
func TestPlansNameTheCallsTheyCannotVouchFor(t *testing.T) {
	for _, c := range []struct {
		driver Driver
		text   string
		want   []string
	}{
		{DriverPostgres, "price >= 0 AND length(name) < 100", nil},
		{DriverPostgres, "status IN ('a', 'b') AND NOT (qty < 0)", nil},
		{DriverPostgres, "CAST(code AS varchar(10)) <> '' AND coalesce(lower(trim(name)), '') <> ''", nil},
		{DriverPostgres, "CASE WHEN (a > 0) THEN b ELSE c END > 0", nil},
		{DriverPostgres, "x = 'lo_unlink(1)'", nil},
		{DriverPostgres, "lo_unlink(oid_col) = 1", []string{"lo_unlink"}},
		{DriverPostgres, "pg_terminate_backend ( pid )", []string{"pg_terminate_backend"}},
		{DriverPostgres, `"lo_unlink"(1) = 1`, []string{`"lo_unlink"`}},
		// A qualified name is never the function the list means.
		{DriverPostgres, "public.lower(name) = 'x'", []string{"public.lower"}},
		{DriverPostgres, "pg_catalog.length(name) > 0", []string{"pg_catalog.length"}},
		{DriverPostgres, "f(g(x)) AND f(y)", []string{"f", "g"}},
		{DriverPostgres, "étiquette(x) AND é(y)", []string{"étiquette", "é"}},
		{DriverPostgres, "EXISTS (SELECT dblink_exec('c', 'q'))", []string{"dblink_exec"}},
		{DriverMSSQL, "[dbo].[audit](id) = 1", []string{"[dbo].[audit]"}},
		{DriverMSSQL, "len([name]) > 0", nil},
		{DriverMySQL, "`audit`(id) = 1", []string{"`audit`"}},
		{DriverMySQL, "sleep (5) = 0", []string{"sleep"}},
		{DriverClickHouse, "notEmpty(toString(id))", nil},
		// A name is the engine's name however it is spaced: the dot may stand
		// apart from both parts, and the parenthesis from the name.
		{DriverPostgres, "public . lower (x) IS NOT NULL", []string{"public.lower"}},
		{DriverPostgres, "pg_catalog\n.\nlower\t(x) = 'a'", []string{"pg_catalog.lower"}},
		{DriverPostgres, `"public" . "lower"(x) = 'a'`, []string{`"public"."lower"`}},
		{DriverPostgres, "a.b.lower(x) = 'a'", []string{"a.b.lower"}},
		// A character that is part of an identifier on one engine does not end
		// the name there, so the vouched word after it is not a call of its own.
		{DriverMSSQL, "dbo.evil#lower(x) = 1", []string{"dbo.evil#lower"}},
		{DriverMSSQL, "dbo.evil@lower(x) = 1", []string{"dbo.evil@lower"}},
		{DriverMSSQL, "evil$lower(x) = 1", []string{"evil$lower"}},
		{DriverMSSQL, "#lower(x) = 1", []string{"#lower"}},
		{DriverOracle, "evil#lower(x) = 1", []string{"evil#lower"}},
		{DriverOracle, "evil$lower(x) = 1", []string{"evil$lower"}},
		{DriverPostgres, "a #lower(x) = 1", nil},
		// A word that is syntax in one position is a function name in every
		// other, and Postgres lets a function be named it.
		{DriverPostgres, "filter(x)", []string{"filter"}},
		{DriverPostgres, "over(x) > 0", []string{"over"}},
		{DriverPostgres, "a > 0 AND like(a, b)", []string{"like"}},
		{DriverPostgres, "ilike(a, b)", []string{"ilike"}},
		{DriverPostgres, "similar(a)", []string{"similar"}},
		{DriverPostgres, "is(a)", []string{"is"}},
		{DriverPostgres, "myschema . filter (x)", []string{"myschema.filter"}},
		{DriverPostgres, "name LIKE ('a' || '%') AND name NOT ILIKE ('b%')", nil},
		{DriverPostgres, "\"name\" like ('a%') AND tags[1] like ('b%')", nil},
		{DriverPostgres, "x = any(array[1,2]) AND NOT (y IN (1, 2))", nil},
		{DriverPostgres, "EXISTS (SELECT count(*) FILTER (WHERE a > 0) FROM t)", []string{"count"}},
		{DriverPostgres, "EXISTS (SELECT abs(a) FILTER (WHERE a > 0), abs(b) OVER (PARTITION BY c) FROM t)", nil},
		// Postgres calls total(t) for `t.total` when t has no such column, so a
		// dotted name is named whether or not a parenthesis follows it.
		{DriverPostgres, "t.evil > 0", []string{"t.evil"}},
		{DriverPostgres, "(t).evil", []string{"evil"}},
		{DriverPostgres, "(t.*).evil IS NULL", []string{"evil"}},
		{DriverPostgres, "status = 'new'::shop.status AND CAST(x AS shop.status) IS NOT NULL", nil},
		{DriverPostgres, "price > 1.5 AND qty < 2e3 AND 1evil(x)", []string{"evil"}},
		{DriverMySQL, "t.total > 0", nil},
		// Past ASCII, a byte is part of the name: a no-break space does not
		// separate a vouched name from its parenthesis.
		{DriverPostgres, "lower\u00a0(x) is null", []string{"lower\u00a0"}},
	} {
		got := unvouchedCalls(c.driver, c.text)
		if len(got) != len(c.want) {
			t.Errorf("%s %q calls %q, want %q", c.driver, c.text, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s %q calls %q, want %q", c.driver, c.text, got, c.want)
			}
		}
	}

	ctx := context.Background()
	for name, c := range map[string]struct {
		plan *DDLPlan
		err  error
		want int
	}{
		"a plain check": plannedAs(PlanAddConstraint(DriverPostgres, ConstraintSpec{
			Table: "t", Type: "check", Expression: "qty >= 0 AND char_length(sku) = 8"}))(0),
		"a check that calls out": plannedAs(PlanAddConstraint(DriverPostgres, ConstraintSpec{
			Table: "t", Type: "check", Expression: "audit(qty)"}))(1),
		"a plain predicate": plannedAs(PlanCreateIndex(ctx, nil, DriverPostgres, IndexSpec{
			Table: "t", Columns: []string{"a"}, Where: "deleted_at IS NULL"}))(0),
		"a predicate that calls out": plannedAs(PlanCreateIndex(ctx, nil, DriverPostgres, IndexSpec{
			Table: "t", Columns: []string{"a"}, Where: "is_live(a)"}))(1),
		"the clock as a default": plannedAs(PlanAddColumn(DriverPostgres, "", "t",
			NewColumn{Name: "at", Type: "timestamptz", Default: "now()"}))(0),
		"a fresh identifier as a default": plannedAs(PlanCreateTable(DriverPostgres, "", "t",
			[]NewColumn{{Name: "id", Type: "uuid", Default: "GEN_RANDOM_UUID()"}}))(0),
		"a literal default": plannedAs(PlanAlterColumn(ctx, nil, DriverPostgres,
			ColumnChange{Table: "t", Column: "c", Default: ddlString("'x'")}))(0),
		"any other function as a default": plannedAs(PlanAddColumn(DriverPostgres, "", "t",
			NewColumn{Name: "n", Type: "integer", Default: "pg_reload_conf()"}))(1),
		"any other function as a new default": plannedAs(PlanAlterColumn(ctx, nil, DriverPostgres,
			ColumnChange{Table: "t", Column: "c", Default: ddlString("next_ticket()")}))(1),
		"a conversion that calls out": plannedAs(PlanAlterColumn(ctx, nil, DriverPostgres,
			ColumnChange{Table: "t", Column: "c", Type: "integer", Using: "parse_qty(c)"}))(1),
		"a change with no SQL of the operator's": plannedAs(PlanDropTable(DriverPostgres, "", "t"))(0),
	} {
		if c.err != nil {
			t.Errorf("%s: %v", name, c.err)
			continue
		}
		if len(c.plan.Unvouched) != c.want {
			t.Errorf("%s: unvouched calls = %q, want %d", name, c.plan.Unvouched, c.want)
		}
	}
}

// plannedAs pairs a planner's two results with the number of unvouched calls
// expected of it.
func plannedAs(plan *DDLPlan, err error) func(int) struct {
	plan *DDLPlan
	err  error
	want int
} {
	return func(want int) struct {
		plan *DDLPlan
		err  error
		want int
	} {
		return struct {
			plan *DDLPlan
			err  error
			want int
		}{plan, err, want}
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
	// MySQL's primary key is named PRIMARY on every table and has a statement
	// of its own; no catalogue read is needed to know it.
	for _, name := range []string{"PRIMARY", "primary"} {
		got = planText(t)(PlanDropConstraint(ctx, nil, DriverMySQL, "app", "users", name, ""))
		if got != "ALTER TABLE `app`.`users` DROP PRIMARY KEY" {
			t.Errorf("mysql drop primary key = %s", got)
		}
	}
	got = planText(t)(PlanDropConstraint(ctx, nil, DriverPostgres, "public", "users", "PRIMARY", ""))
	if got != `ALTER TABLE "public"."users" DROP CONSTRAINT "PRIMARY"` {
		t.Errorf("a constraint that happens to be called PRIMARY elsewhere = %s", got)
	}
}

// A plan that failed because the engine would not be read is told apart from
// one that failed because of what it was asked for.
func TestPlanReadFailuresAreNotTheRequestsFault(t *testing.T) {
	cause := errors.New("connection reset")
	err := planRead("the column as it is now", cause)
	if !errors.Is(err, ErrPlanRead) || !errors.Is(err, cause) ||
		err.Error() != "could not read the column as it is now: connection reset" {
		t.Errorf("plan read error = %v", err)
	}
	if _, err := PlanAddColumn(DriverPostgres, "", "t", NewColumn{Name: "c", Type: "int, x"}); err == nil || errors.Is(err, ErrPlanRead) {
		t.Errorf("a refused request reads as an unreadable catalogue: %v", err)
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
		// SQLite's statements carry no schema: there is main, and no other is
		// changed from a form (ddlRel).
		{DriverSQLite, false, `CREATE VIEW "v" AS` + "\n" + query},
		{DriverClickHouse, true, "CREATE OR REPLACE VIEW `public`.`v` AS\n" + query},
		{DriverOracle, true, `CREATE OR REPLACE VIEW "public"."v" AS` + "\n" + query},
	} {
		schema := "public"
		if c.driver == DriverSQLite {
			schema = "main"
		}
		got := planText(t)(PlanCreateView(c.driver, ViewSpec{Schema: schema, Name: "v", Query: query + " ; ", Replace: c.replace}))
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
		{DriverOracle, `DROP INDEX "s"."i"`},
		{DriverMySQL, "DROP INDEX `i` ON `s`.`t`"},
		{DriverMSSQL, "DROP INDEX [i] ON [s].[t]"},
		{DriverClickHouse, "ALTER TABLE `s`.`t` DROP INDEX `i`"},
	} {
		if got := planText(t)(PlanDropIndex(c.driver, "s", "t", "i")); got != c.want {
			t.Errorf("%s drop index = %s, want %s", c.driver, got, c.want)
		}
	}
	if got := planText(t)(PlanDropIndex(DriverSQLite, "main", "t", "i")); got != `DROP INDEX "i"` {
		t.Errorf("sqlite drop index = %s", got)
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
		{DriverOracle, `ALTER TABLE "shop"."orders" RENAME TO "purchases"`},
		{DriverMSSQL, "EXEC sp_rename N'[shop].[orders]', N'purchases'"},
	} {
		if got := planText(t)(PlanRenameTable(c.driver, "shop", "orders", "purchases")); got != c.want {
			t.Errorf("%s rename table = %s, want %s", c.driver, got, c.want)
		}
	}
	if got := planText(t)(PlanRenameTable(DriverSQLite, "", "orders", "purchases")); got != `ALTER TABLE "orders" RENAME TO "purchases"` {
		t.Errorf("sqlite rename table = %s", got)
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
		return contains(DDLOperations(driver, ""), op)
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
	// CREATE INDEX IF NOT EXISTS is MariaDB's, and the planner asks the server
	// which product it is; the catalogue has to say the same without one.
	if contains(DDLOperations(DriverMySQL, "mysql"), OpIndexIfMissing) || !contains(DDLOperations(DriverMySQL, "mariadb"), OpIndexIfMissing) {
		t.Error("index IF NOT EXISTS is advertised for the wrong MySQL product")
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

// SQL Server refuses some changes in two messages and its driver reports the
// second, which says a change failed and not what stood in its way.
func TestSQLServerRefusalsSayEverythingTheServerSaid(t *testing.T) {
	why := mssql.Error{Number: 5074, Message: "The object 'orders_qty_check' is dependent on column 'qty'."}
	what := mssql.Error{Number: 4922, Message: "ALTER TABLE ALTER COLUMN qty failed because one or more objects access this column."}
	refused := what
	refused.All = []mssql.Error{why, what}

	got := engineRefusal(fmt.Errorf("exec: %w", refused))
	if got.Error() != "mssql: "+why.Message+" "+what.Message {
		t.Errorf("refusal = %q", got)
	}
	var underneath mssql.Error
	if !errors.As(got, &underneath) || underneath.Number != 4922 {
		t.Errorf("the server's error is no longer underneath: %v", got)
	}
	// One message is the driver's own to report, and so is anyone else's error.
	single := what
	single.All = []mssql.Error{what}
	if got := engineRefusal(single); got.Error() != "mssql: "+what.Message {
		t.Errorf("a single message = %q", got)
	}
	other := errors.New("pq: relation does not exist")
	if got := engineRefusal(other); got != other {
		t.Errorf("another engine's error = %v", got)
	}
	if engineRefusal(nil) != nil {
		t.Error("no error became one")
	}
}
