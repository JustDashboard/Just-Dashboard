package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// What the migration may not do whatever the databases hold: write a name into
// the script where it runs, rewrite somebody's data, or make an object before
// what it is made from.

// onlyComments reports the lines of a script that are not a comment and are not
// part of a statement the test expects.
func statementLines(script string) []string {
	var out []string
	for _, line := range strings.Split(script, "\n") {
		if line != "" && !strings.HasPrefix(line, "--") {
			out = append(out, line)
		}
	}
	return out
}

func TestANameWithALineBreakInATitleStaysAComment(t *testing.T) {
	label := "x\nDROP TABLE customers; --"
	from, to := schemaSnap(DriverPostgres, "public"), schemaSnap(DriverPostgres, "public")
	from.Enums["mood"], to.Enums["mood"] = []string{"sad"}, []string{"sad", label}
	_, m := planFor(t, from, to)
	script := m.Script()
	if len(m.Steps) != 1 || strings.ContainsAny(m.Steps[0].Title, "\n\r") {
		t.Fatalf("steps = %+v", m.Steps)
	}
	// The label is in the one statement that adds it, quoted, and in no line of its own.
	if !strings.HasPrefix(script, "-- Add label x DROP TABLE customers; -- to enum type mood\n") {
		t.Errorf("the title is not one comment line:\n%s", script)
	}
	got := strings.Join(statementLines(script), "\n")
	if want := "ALTER TYPE \"public\".\"mood\" ADD VALUE 'x\nDROP TABLE customers; --' AFTER 'sad';"; got != want {
		t.Errorf("statements =\n%s\nwant\n%s", got, want)
	}
}

func TestEveryControlCharacterEndsNoCommentEarly(t *testing.T) {
	m := &Migration{Steps: []MigrationStep{{
		Title:  "a\nb\rc d e\u0085f\x00g\x1bh",
		Manual: true,
		Note:   "one\r\nDROP TABLE x;\rDROP TABLE y; DROP TABLE z;",
	}}}
	script := m.Script()
	if lines := statementLines(script); len(lines) != 0 {
		t.Errorf("a name or a note became a statement: %q\n%s", lines, script)
	}
	for _, r := range script {
		if r != '\n' && (r < ' ' || r == 0x7f || r == 0x85 || r == ' ' || r == ' ') {
			t.Fatalf("a control character %U is in the script:\n%q", r, script)
		}
	}
}

func TestANameTheEngineWillNotQuoteIsLeftToTheOperator(t *testing.T) {
	bad := "a\nDROP TABLE customers"
	// A column, a table to create, a table to drop's enum, a sequence, a routine.
	from := schemaSnap(DriverPostgres, "public", table("users", col("id", "integer", false)))
	to := schemaSnap(DriverPostgres, "public",
		table("users", col("id", "integer", false), col(bad, "text", true)),
		table(bad, col("id", "integer", false)),
		table("fine", col("id", "integer", false), col(bad, "text", true)))
	from.Enums[bad] = []string{"x"}
	from.Sequences[bad] = SnapshotSequence{Name: bad}
	from.Routines[routineKey(KindFunction, bad, "")] = SnapshotRoutine{Kind: KindFunction, Name: bad, Definition: "CREATE FUNCTION f()"}
	from.Routines[routineKey(KindFunction, "g", "x\nDROP TABLE t")] = SnapshotRoutine{Kind: KindFunction, Name: "g", Signature: "x\nDROP TABLE t", Definition: "CREATE FUNCTION g()"}
	_, m := planFor(t, from, to)
	script := m.Script()
	if lines := statementLines(script); len(lines) != 0 {
		t.Errorf("a name that does not quote reached a statement: %q", lines)
	}
	for _, step := range m.Steps {
		if !step.Manual {
			t.Errorf("%q was written although its name does not quote", step.Title)
		}
	}
	if len(m.Steps) != 7 {
		t.Errorf("%d steps, want one left to the operator for each of the seven changes:\n%s", len(m.Steps), script)
	}
}

func TestAQuotedTextIsNotAMentionOfTheSchema(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{`DEFAULT 'noreply@shop.example'`, `DEFAULT 'noreply@shop.example'`},
		{`SELECT shop.orders.id, 'shop.' FROM shop.orders -- shop.orders`, `SELECT orders.id, 'shop.' FROM orders -- shop.orders`},
		{"/* shop.t */ SELECT \"shop\".t, `shop`.t, [shop].t", "/* shop.t */ SELECT t, t, t"},
		{`'it''s shop.' || shop.t`, `'it''s shop.' || t`},
		{`other.shop.t`, `other.shop.t`},
		{`nextval('shop.t_id_seq'::regclass)`, `nextval('t_id_seq'::regclass)`},
		{`nextval('"shop".t_id_seq')`, `nextval('t_id_seq')`},
		{`'shop.t'::regclass`, `'t'::regclass`},
		{`CASE WHEN x = 'shop.t' THEN 1 END`, `CASE WHEN x = 'shop.t' THEN 1 END`},
		{`"shop name".t`, `"shop name".t`},
	} {
		if got := stripSchemaName(c.in, "shop", false); got != c.want {
			t.Errorf("strip(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	for _, c := range []struct{ in, want string }{
		{`DEFAULT 'noreply@shop.example'`, `DEFAULT 'noreply@shop.example'`},
		{`FROM shop.t, "shop".u, 'shop.v'`, `FROM dev.t, "dev".u, 'shop.v'`},
		{`nextval('shop.t_id_seq'::regclass)`, `nextval('dev.t_id_seq'::regclass)`},
		{`-- from shop.t` + "\n" + `shop.t`, `-- from shop.t` + "\n" + `dev.t`},
	} {
		if got := rebaseSchemaName(c.in, "shop", "dev", false); got != c.want {
			t.Errorf("rebase(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// MySQL reads a backslash in a text as an escape: the quote it escapes does not end the text.
	if got := stripSchemaName(`'a\'shop.b' shop.t`, "shop", true); got != `'a\'shop.b' t` {
		t.Errorf("strip with a backslash = %q", got)
	}
}

func TestADefaultWithAnAddressInItIsTheSameDefaultInTwoSchemas(t *testing.T) {
	a := table("users", col("id", "integer", false), col("sender", "text", true))
	b := table("users", col("id", "integer", false), col("sender", "text", true))
	a.Columns[1].Default = `'noreply@alpha.example'::text`
	b.Columns[1].Default = `'noreply@beta.example'::text`
	diff := DiffSchemas(schemaSnap(DriverPostgres, "alpha", a), schemaSnap(DriverPostgres, "beta", b))
	if diff.Empty() {
		t.Fatal("two different addresses are called the same default because they contain the schema's name")
	}
	// And a new column is written with the address it has.
	from := schemaSnap(DriverPostgres, "alpha", table("users", col("id", "integer", false)))
	to := schemaSnap(DriverPostgres, "beta", b)
	_, m := planFor(t, from, to)
	if !strings.Contains(m.Script(), `DEFAULT 'noreply@beta.example'`) {
		t.Errorf("the default was rewritten:\n%s", m.Script())
	}
}

func TestADefinitionTheServerWillNotGiveIsNotAnEmptyOne(t *testing.T) {
	if _, err := usableDefinition(DriverMySQL, "v", &ObjectDefinition{Definition: ""}); err == nil {
		t.Error("an empty definition is taken as the view's text")
	}
	if _, err := usableDefinition(DriverMSSQL, "m", &ObjectDefinition{Definition: "  \n"}); err == nil {
		t.Error("a definition of nothing is taken as the module's text")
	}
	if _, err := usableDefinition(DriverMSSQL, "m", &ObjectDefinition{Definition: "CREATE VIEW m AS SELECT 1", Note: "the module is encrypted"}); err == nil ||
		!strings.Contains(err.Error(), "encrypted") {
		t.Errorf("a definition with a note is taken: %v", err)
	}
	text, err := usableDefinition(DriverMySQL, "v", &ObjectDefinition{Definition: " CREATE DEFINER=`root`@`%` VIEW v AS SELECT 1 "})
	if err != nil || text != "CREATE VIEW v AS SELECT 1" {
		t.Errorf("a definition = %q, %v", text, err)
	}
}

func TestAViewIsLeftOutOfTheComparisonWhenItsDefinitionCannotBeRead(t *testing.T) {
	// A view with no definition on one side, and a view on the other: if the
	// empty text were kept, the plan would drop it and make it again with nothing.
	from, to := schemaSnap(DriverMySQL, "s"), schemaSnap(DriverMySQL, "s")
	to.Views["v"] = SnapshotView{Name: "v", Definition: "CREATE VIEW v AS SELECT 1"}
	from.Unread[GroupViews] = "v: the server gave no definition"
	diff := DiffSchemas(from, to)
	if !diff.Empty() {
		t.Errorf("an unread view is compared: %+v", diff.Objects)
	}
	if _, m := planFor(t, from, to); len(m.Steps) != 0 {
		t.Errorf("steps = %+v", m.Steps)
	}
}

func TestTooManyObjectsOfAnyKindAreRefusedBeforeTheyAreRead(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "many.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxDiffObjects; i++ {
		if _, err := db.Exec(fmt.Sprintf(`CREATE VIEW v%d AS SELECT id FROM t`, i)); err != nil {
			t.Fatal(err)
		}
	}
	_, err = ReadSchemaSnapshot(context.Background(), db, DriverSQLite, "sqlite", "main")
	if err == nil || !strings.Contains(err.Error(), "too large") || !strings.Contains(err.Error(), fmt.Sprint(MaxDiffObjects+1)) {
		t.Errorf("err = %v", err)
	}
}

func TestAViewThatReadsAChangedColumnIsDroppedAndMadeAgain(t *testing.T) {
	from := schemaSnap(DriverPostgres, "public", table("orders", col("id", "integer", false), col("total", "numeric(10,2)", false)))
	to := schemaSnap(DriverPostgres, "public", table("orders", col("id", "integer", false), col("total", "numeric(12,2)", false)))
	// Neither changes: both read the column that does.
	for _, s := range []*SchemaSnapshot{from, to} {
		s.Views["v_totals"] = SnapshotView{Name: "v_totals", Definition: "CREATE OR REPLACE VIEW public.v_totals AS SELECT id, total FROM public.orders"}
		s.Views["v_other"] = SnapshotView{Name: "v_other", Definition: "CREATE OR REPLACE VIEW public.v_other AS SELECT 1 AS one"}
	}
	diff, m := planFor(t, from, to)
	if len(diff.Objects) != 1 {
		t.Fatalf("a view that did not change is reported: %+v", diff.Objects)
	}
	script := m.Script()
	before(t, script, `DROP VIEW "public"."v_totals"`, `ALTER COLUMN "total" TYPE numeric(12,2)`)
	before(t, script, `ALTER COLUMN "total" TYPE`, "CREATE OR REPLACE VIEW public.v_totals")
	if strings.Contains(script, "v_other") {
		t.Errorf("a view that reads nothing that changes is moved:\n%s", script)
	}
	for _, step := range m.Steps {
		if strings.HasPrefix(step.Title, "Drop view") && (step.Destructive || !strings.Contains(step.Note, "made again")) {
			t.Errorf("a view dropped to be made again = %+v", step)
		}
	}
}

func TestAViewThatReadsAChangedViewGoesBeforeItAndComesBackAfter(t *testing.T) {
	from, to := schemaSnap(DriverPostgres, "public"), schemaSnap(DriverPostgres, "public")
	from.Views["v1"] = SnapshotView{Name: "v1", Definition: "CREATE OR REPLACE VIEW public.v1 AS SELECT 1 AS a"}
	to.Views["v1"] = SnapshotView{Name: "v1", Definition: "CREATE OR REPLACE VIEW public.v1 AS SELECT 2 AS a"}
	for _, s := range []*SchemaSnapshot{from, to} {
		s.Views["v2"] = SnapshotView{Name: "v2", Definition: "CREATE OR REPLACE VIEW public.v2 AS SELECT a FROM public.v1"}
		s.Views["v3"] = SnapshotView{Name: "v3", Definition: "CREATE OR REPLACE VIEW public.v3 AS SELECT a FROM public.v2"}
	}
	_, m := planFor(t, from, to)
	script := m.Script()
	// Dependents first on the way out, dependencies first on the way back.
	before(t, script, `DROP VIEW "public"."v3"`, `DROP VIEW "public"."v2"`)
	before(t, script, `DROP VIEW "public"."v2"`, `DROP VIEW "public"."v1"`)
	before(t, script, "CREATE OR REPLACE VIEW public.v1", "CREATE OR REPLACE VIEW public.v2")
	before(t, script, "CREATE OR REPLACE VIEW public.v2", "CREATE OR REPLACE VIEW public.v3")
}

func TestAKeyThatUsesAColumnWhoseTypeChangesIsDroppedAndAddedAgain(t *testing.T) {
	users := func(typ string) *TableDetail { return withPrimaryKey(table("users", col("id", typ, false)), "id") }
	tickets := func(typ string) *TableDetail {
		return withForeignKey(table("tickets", col("id", "int", false), col("user_id", typ, true)), "tickets_user_fk", "users", "user_id")
	}
	for _, driver := range []Driver{DriverMySQL, DriverMSSQL} {
		t.Run(string(driver), func(t *testing.T) {
			from := schemaSnap(driver, "s", users("int"), tickets("int"))
			to := schemaSnap(driver, "s", users("bigint"), tickets("bigint"))
			diff := DiffSchemas(from, to)
			if len(diff.Objects) != 2 {
				t.Fatalf("objects = %+v", diff.Objects)
			}
			// Planned without the connection: a restating plan would read it.
			p := &migrationPlanner{driver: driver, schema: "s", toSchema: "s", from: from, to: to, diff: diff, settled: map[string]bool{}}
			cycled := p.cycledForeignKeys()
			if len(cycled) != 1 || cycled[0].table != "tickets" || cycled[0].key.Name != "tickets_user_fk" {
				t.Fatalf("cycled = %+v", cycled)
			}
			p.dropCycledForeignKeys(cycled)
			p.addCycledForeignKeys(cycled)
			script := (&Migration{Steps: p.steps}).Script()
			before(t, script, "Drop foreign key tickets_user_fk", "Add foreign key tickets_user_fk")
			if strings.Contains(script, "DESTRUCTIVE") {
				t.Errorf("a key put back is destructive:\n%s", script)
			}
		})
	}
	// Postgres converts the key along with the column.
	from := schemaSnap(DriverPostgres, "s", users("integer"), tickets("integer"))
	to := schemaSnap(DriverPostgres, "s", users("bigint"), tickets("bigint"))
	p := &migrationPlanner{driver: DriverPostgres, schema: "s", toSchema: "s", from: from, to: to, diff: DiffSchemas(from, to), settled: map[string]bool{}}
	if got := p.cycledForeignKeys(); len(got) != 0 {
		t.Errorf("Postgres keys are cycled: %+v", got)
	}
}

func TestARoutineIsMadeAfterTheColumnsItReadsUnlessAnEarlierStepCallsIt(t *testing.T) {
	from := schemaSnap(DriverPostgres, "public", table("users", col("id", "integer", false)))
	users := table("users", col("id", "integer", false), col("created_at", "timestamp with time zone", true), col("code", "text", true))
	users.Columns[2].Default = "make_code()"
	to := schemaSnap(DriverPostgres, "public", users)
	to.Routines[routineKey(KindFunction, "newest", "")] = SnapshotRoutine{Kind: KindFunction, Name: "newest",
		Definition: "CREATE OR REPLACE FUNCTION public.newest()\n RETURNS timestamptz\n LANGUAGE sql\nAS $function$select max(created_at) from public.users$function$"}
	to.Routines[routineKey(KindFunction, "make_code", "")] = SnapshotRoutine{Kind: KindFunction, Name: "make_code",
		Definition: "CREATE OR REPLACE FUNCTION public.make_code()\n RETURNS text\n LANGUAGE sql\nAS $function$select 'x'$function$"}
	_, m := planFor(t, from, to)
	script := m.Script()
	before(t, script, "SET check_function_bodies = off", "CREATE OR REPLACE FUNCTION public.make_code()")
	before(t, script, "CREATE OR REPLACE FUNCTION public.make_code()", `ADD COLUMN "code"`)
	before(t, script, `ADD COLUMN "created_at"`, "CREATE OR REPLACE FUNCTION public.newest()")
}

func TestAFunctionThatCannotBeReplacedInPlaceIsDroppedAndMade(t *testing.T) {
	head := func(returns string) string {
		return "CREATE OR REPLACE FUNCTION public.f(x integer)\n RETURNS " + returns + "\n LANGUAGE sql\nAS $function$select 1$function$"
	}
	from, to := schemaSnap(DriverPostgres, "public"), schemaSnap(DriverPostgres, "public")
	key := routineKey(KindFunction, "f", "x integer")
	from.Routines[key] = SnapshotRoutine{Kind: KindFunction, Name: "f", Signature: "x integer", Definition: head("integer")}
	to.Routines[key] = SnapshotRoutine{Kind: KindFunction, Name: "f", Signature: "x integer", Definition: head("bigint")}
	_, m := planFor(t, from, to)
	if _, destructive, _ := m.Counts(); destructive != 1 || !strings.Contains(m.Script(), `DROP FUNCTION "public"."f"(x integer);`) {
		t.Errorf("a changed return type is replaced in place:\n%s", m.Script())
	}
	to.Routines[key] = SnapshotRoutine{Kind: KindFunction, Name: "f", Signature: "x integer",
		Definition: strings.Replace(head("integer"), "select 1", "select 2", 1)}
	_, m = planFor(t, from, to)
	if _, destructive, _ := m.Counts(); destructive != 0 || strings.Contains(m.Script(), "DROP FUNCTION") {
		t.Errorf("a body change is not replaced in place:\n%s", m.Script())
	}
}

func TestAFunctionWithRenamedParametersReplacesTheOldOne(t *testing.T) {
	from, to := schemaSnap(DriverPostgres, "public"), schemaSnap(DriverPostgres, "public")
	from.Routines[routineKey(KindFunction, "shout", "a text")] = SnapshotRoutine{Kind: KindFunction, Name: "shout", Signature: "a text",
		Definition: "CREATE OR REPLACE FUNCTION public.shout(a text)\n RETURNS text\n LANGUAGE sql\nAS $function$select upper(a)$function$"}
	to.Routines[routineKey(KindFunction, "shout", "b text")] = SnapshotRoutine{Kind: KindFunction, Name: "shout", Signature: "b text",
		Definition: "CREATE OR REPLACE FUNCTION public.shout(b text)\n RETURNS text\n LANGUAGE sql\nAS $function$select upper(b)$function$"}
	_, m := planFor(t, from, to)
	script := m.Script()
	before(t, script, `DROP FUNCTION "public"."shout"(a text)`, "CREATE OR REPLACE FUNCTION public.shout(b text)")
	if strings.Count(script, "DROP FUNCTION") != 1 {
		t.Errorf("the old function is dropped twice:\n%s", script)
	}
}

func TestAMySQLViewAndASQLServerModuleAreNamedInTheSchemaBeingChanged(t *testing.T) {
	my := &migrationPlanner{driver: DriverMySQL, schema: "live", toSchema: "live"}
	for in, want := range map[string]string{
		"CREATE ALGORITHM=UNDEFINED SQL SECURITY DEFINER VIEW `v` AS select 1 AS `a`": "CREATE ALGORITHM=UNDEFINED SQL SECURITY DEFINER VIEW `live`.`v` AS select 1 AS `a`",
		"CREATE VIEW `v` (`a`) AS select 1":                                           "CREATE VIEW `live`.`v` (`a`) AS select 1",
		"CREATE ALGORITHM=UNDEFINED VIEW `other`.`v` AS select 1":                     "CREATE ALGORITHM=UNDEFINED VIEW `other`.`v` AS select 1",
		"CREATE FUNCTION `f`(x int) RETURNS int RETURN x":                             "CREATE FUNCTION `live`.`f`(x int) RETURNS int RETURN x",
	} {
		if got := my.qualifiedHead(in); got != want {
			t.Errorf("mysql %q =\n%q\nwant\n%q", in, got, want)
		}
	}
	ms := &migrationPlanner{driver: DriverMSSQL, schema: "live", toSchema: "live"}
	for in, want := range map[string]string{
		"CREATE VIEW v AS SELECT 1":                                   "CREATE VIEW [live].v AS SELECT 1",
		"create function f(@x int) returns int as begin return 1 end": "create function [live].f(@x int) returns int as begin return 1 end",
		"CREATE PROCEDURE [p] AS SELECT 1":                            "CREATE PROCEDURE [live].[p] AS SELECT 1",
		"CREATE VIEW dbo.v AS SELECT 1":                               "CREATE VIEW dbo.v AS SELECT 1",
		"CREATE VIEW [dbo].[v] AS SELECT 1":                           "CREATE VIEW [dbo].[v] AS SELECT 1",
	} {
		if got := ms.qualifiedHead(in); got != want {
			t.Errorf("sqlserver %q =\n%q\nwant\n%q", in, got, want)
		}
	}
}

func TestCreateOrAlterSkipsCommentsAndKeepsTheCase(t *testing.T) {
	for in, want := range map[string]string{
		"-- the old one\nCREATE FUNCTION s.f() RETURNS int AS BEGIN RETURN 1 END": "-- the old one\nCREATE OR ALTER FUNCTION s.f() RETURNS int AS BEGIN RETURN 1 END",
		"/* v2 */ Create Procedure s.p AS SELECT 1":                               "/* v2 */ Create OR ALTER Procedure s.p AS SELECT 1",
		"create   view s.v as select 1":                                           "create or alter   view s.v as select 1",
		"CREATE OR ALTER FUNCTION s.f() RETURNS int AS BEGIN RETURN 1 END":        "CREATE OR ALTER FUNCTION s.f() RETURNS int AS BEGIN RETURN 1 END",
	} {
		if got := createOrAlter(in); got != want {
			t.Errorf("createOrAlter(%q) =\n%q\nwant\n%q", in, got, want)
		}
	}
}

func TestATableWithAKeyOnItselfNeedsNoKeyDropped(t *testing.T) {
	tree := withForeignKey(withPrimaryKey(table("tree", col("id", "integer", false), col("parent_id", "integer", true)), "id"), "tree_parent_fkey", "tree", "parent_id")
	_, m := planFor(t, schemaSnap(DriverPostgres, "public", tree), schemaSnap(DriverPostgres, "public"))
	if script := m.Script(); strings.Contains(script, "DROP CONSTRAINT") || !strings.Contains(script, `DROP TABLE "public"."tree"`) {
		t.Errorf("script:\n%s", script)
	}
}
