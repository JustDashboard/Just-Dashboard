package dbx

import (
	"context"
	"strings"
	"testing"
)

// The comparison and the order of the migration are pure functions of two
// snapshots, so they are proved here without a server. What an engine accepts
// of the statements is proved against real servers in schema_diff_live_test.go.

func snapshot(driver Driver, schema string, tables ...*TableDetail) *SchemaSnapshot {
	s := &SchemaSnapshot{
		Driver: driver, Flavor: string(driver), Schema: schema,
		Tables: map[string]*TableDetail{}, Views: map[string]SnapshotView{},
		Sequences: map[string]SnapshotSequence{}, Enums: map[string][]string{},
		Routines: map[string]SnapshotRoutine{}, Unread: map[string]string{},
	}
	for _, t := range tables {
		t.Schema = schema
		s.Tables[t.Name] = t
	}
	return s
}

func table(name string, columns ...Column) *TableDetail {
	return &TableDetail{Name: name, Columns: columns, PrimaryKey: []string{}, Indexes: []Index{},
		ForeignKeys: []ForeignKey{}, Constraints: []Constraint{}}
}

func col(name, typ string, nullable bool) Column {
	return Column{Name: name, Type: typ, Nullable: nullable}
}

func withPrimaryKey(t *TableDetail, columns ...string) *TableDetail {
	t.PrimaryKey = columns
	t.Indexes = append(t.Indexes, Index{Name: t.Name + "_pkey", Columns: columns, Unique: true, Primary: true, Constraint: t.Name + "_pkey"})
	return t
}

func withForeignKey(t *TableDetail, name, refTable, column string) *TableDetail {
	t.ForeignKeys = append(t.ForeignKeys, ForeignKey{Name: name, Columns: []string{column}, RefTable: refTable, RefColumns: []string{"id"}})
	return t
}

func planFor(t *testing.T, from, to *SchemaSnapshot) (*SchemaDiff, *Migration) {
	t.Helper()
	diff := DiffSchemas(from, to)
	return diff, PlanMigration(context.Background(), nil, from, to, diff)
}

// before reports that the first text comes ahead of the second in the script.
func before(t *testing.T, script, first, second string) {
	t.Helper()
	i, j := strings.Index(script, first), strings.Index(script, second)
	switch {
	case i < 0:
		t.Errorf("the script has no %q:\n%s", first, script)
	case j < 0:
		t.Errorf("the script has no %q:\n%s", second, script)
	case i > j:
		t.Errorf("%q must come before %q:\n%s", first, second, script)
	}
}

func TestIdenticalSchemasHaveNoDifference(t *testing.T) {
	users := func() *TableDetail {
		return withForeignKey(withPrimaryKey(table("orders", col("id", "integer", false), col("user_id", "integer", true)), "id"), "orders_user_fkey", "users", "user_id")
	}
	diff := DiffSchemas(snapshot(DriverPostgres, "public", users()), snapshot(DriverPostgres, "public", users()))
	if !diff.Empty() {
		t.Fatalf("identical schemas differ: %+v", diff.Objects)
	}
}

func TestADifferenceIsNamedPartByPart(t *testing.T) {
	from := table("users", col("id", "integer", false), col("email", "text", true), col("age", "integer", true))
	to := table("users", col("id", "integer", false), col("email", "varchar(120)", false), col("created", "timestamp with time zone", true))
	to.Columns[2].Default = "now()"
	to.Indexes = []Index{{Name: "users_email_idx", Columns: []string{"email"}, Unique: true, Method: "btree"}}
	to.Constraints = []Constraint{{Name: "users_email_key", Type: ConstraintUnique, Columns: []string{"email"}}}
	diff := DiffSchemas(snapshot(DriverPostgres, "public", from), snapshot(DriverPostgres, "public", to))
	if len(diff.Objects) != 1 || diff.Objects[0].Kind != DiffTable || diff.Objects[0].Change != ChangeChanged {
		t.Fatalf("objects = %+v", diff.Objects)
	}
	got := map[string]PartDiff{}
	for _, p := range diff.Objects[0].Parts {
		got[p.Part+" "+p.Name] = p
	}
	for key, want := range map[string][3]string{
		"column email":                      {ChangeChanged, "text", "varchar(120) NOT NULL"},
		"column age":                        {ChangeRemoved, "integer", ""},
		"column created":                    {ChangeAdded, "", "timestamp with time zone DEFAULT now()"},
		"index users_email_idx":             {ChangeAdded, "", "UNIQUE btree (email)"},
		"unique constraint users_email_key": {ChangeAdded, "", "UNIQUE (email)"},
	} {
		p, ok := got[key]
		if !ok {
			t.Errorf("no %q among %v", key, got)
			continue
		}
		if p.Change != want[0] || p.From != want[1] || p.To != want[2] {
			t.Errorf("%s = %+v, want %v", key, p, want)
		}
	}
	if _, ok := got["column id"]; ok {
		t.Error("a column that is the same is reported")
	}
}

func TestAnIndexThatDiffersOnlyByNameIsNotADifference(t *testing.T) {
	a, b := table("t", col("id", "integer", false), col("x", "integer", true)), table("t", col("id", "integer", false), col("x", "integer", true))
	a.Indexes = []Index{{Name: "t_x_idx", Columns: []string{"x"}}}
	b.Indexes = []Index{{Name: "idx_9f2", Columns: []string{"x"}}}
	a.ForeignKeys = []ForeignKey{{Name: "t_ibfk_1", Columns: []string{"x"}, RefTable: "u", RefColumns: []string{"id"}}}
	b.ForeignKeys = []ForeignKey{{Name: "t_x_fkey", Columns: []string{"x"}, RefTable: "u", RefColumns: []string{"id"}, OnDelete: "NO ACTION"}}
	if diff := DiffSchemas(snapshot(DriverMySQL, "s", a), snapshot(DriverMySQL, "s", b)); !diff.Empty() {
		t.Fatalf("generated names are reported: %+v", diff.Objects)
	}
	// The same name with another definition is a change.
	b.Indexes[0].Name = "t_x_idx"
	b.Indexes[0].Unique = true
	diff := DiffSchemas(snapshot(DriverMySQL, "s", a), snapshot(DriverMySQL, "s", b))
	if len(diff.Objects) != 1 || diff.Objects[0].Parts[0].Change != ChangeChanged {
		t.Fatalf("objects = %+v", diff.Objects)
	}
}

func TestTheSchemasOwnNameIsNotADifference(t *testing.T) {
	a := table("orders", col("id", "integer", false), col("mood", `"alpha".mood`, true))
	a.Columns[0].Default = `nextval('alpha.orders_id_seq'::regclass)`
	b := table("orders", col("id", "integer", false), col("mood", `"beta".mood`, true))
	b.Columns[0].Default = `nextval('beta.orders_id_seq'::regclass)`
	a.ForeignKeys = []ForeignKey{{Name: "fk", Columns: []string{"id"}, RefSchema: "alpha", RefTable: "u", RefColumns: []string{"id"}}}
	b.ForeignKeys = []ForeignKey{{Name: "fk", Columns: []string{"id"}, RefSchema: "beta", RefTable: "u", RefColumns: []string{"id"}}}
	from, to := snapshot(DriverPostgres, "alpha", a), snapshot(DriverPostgres, "beta", b)
	from.Views["v"] = SnapshotView{Name: "v", Definition: "SELECT id FROM alpha.orders"}
	to.Views["v"] = SnapshotView{Name: "v", Definition: "SELECT id FROM beta.orders"}
	if diff := DiffSchemas(from, to); !diff.Empty() {
		t.Fatalf("the same schema under two names differs: %+v", diff.Objects)
	}
	// A bare name goes at a word boundary only.
	if got := stripSchemaName("x_alpha.t alpha.t `alpha`.t", "alpha", false); got != "x_alpha.t t t" {
		t.Errorf("stripSchemaName = %q", got)
	}
}

func TestAKindOfObjectThatCouldNotBeReadIsNotCompared(t *testing.T) {
	from, to := snapshot(DriverPostgres, "public"), snapshot(DriverPostgres, "public")
	to.Views["v"] = SnapshotView{Name: "v", Definition: "SELECT 1"}
	to.Routines[routineKey(KindFunction, "f", "")] = SnapshotRoutine{Kind: KindFunction, Name: "f", Definition: "CREATE FUNCTION f()"}
	from.Unread[GroupViews] = "permission denied for view definitions"
	diff := DiffSchemas(from, to)
	if len(diff.Objects) != 1 || diff.Objects[0].Kind != DiffFunction {
		t.Fatalf("a view the other side could not be read for is reported: %+v", diff.Objects)
	}
	notes := from.UnreadNotes("staging")
	if len(notes) != 1 || !strings.Contains(notes[0], "views of staging were not compared") {
		t.Errorf("notes = %q", notes)
	}
}

func TestANewTableIsCreatedBeforeTheKeysThatPointAtIt(t *testing.T) {
	users := withPrimaryKey(table("users", col("id", "integer", false)), "id")
	payments := withForeignKey(withPrimaryKey(table("payments", col("id", "integer", false), col("order_id", "integer", true)), "id"), "payments_order_fkey", "orders", "order_id")
	orders := withForeignKey(withPrimaryKey(table("orders", col("id", "integer", false), col("user_id", "integer", true)), "id"), "orders_user_fkey", "users", "user_id")
	diff, m := planFor(t, snapshot(DriverPostgres, "public", users), snapshot(DriverPostgres, "public", users, orders, payments))
	script := m.Script()
	if len(diff.Objects) != 2 {
		t.Fatalf("objects = %+v", diff.Objects)
	}
	// Every table exists before any key is added, whatever order they are named in.
	before(t, script, `CREATE TABLE "public"."payments"`, `ADD CONSTRAINT "orders_user_fkey"`)
	before(t, script, `CREATE TABLE "public"."orders"`, `ADD CONSTRAINT "payments_order_fkey"`)
	before(t, script, `CREATE TABLE "public"."orders"`, `ADD CONSTRAINT "payments_order_fkey"`)
	if strings.Contains(script, "DESTRUCTIVE") {
		t.Errorf("creating tables is marked destructive:\n%s", script)
	}
}

func TestAKeyIsDroppedBeforeTheTableItPointsAtGoes(t *testing.T) {
	users := withPrimaryKey(table("users", col("id", "integer", false)), "id")
	orders := withForeignKey(withPrimaryKey(table("orders", col("id", "integer", false), col("user_id", "integer", true)), "id"), "orders_user_fkey", "users", "user_id")
	ordersWithout := withPrimaryKey(table("orders", col("id", "integer", false), col("user_id", "integer", true)), "id")
	_, m := planFor(t, snapshot(DriverPostgres, "public", users, orders), snapshot(DriverPostgres, "public", ordersWithout))
	script := m.Script()
	before(t, script, `DROP CONSTRAINT "orders_user_fkey"`, `DROP TABLE "public"."users"`)
	steps, destructive, manual := m.Counts()
	if steps != 2 || destructive != 2 || manual != 0 {
		t.Errorf("steps %d, destructive %d, manual %d:\n%s", steps, destructive, manual, script)
	}
}

func TestTablesThatPointAtEachOtherCanBeDropped(t *testing.T) {
	a := withForeignKey(withPrimaryKey(table("a", col("id", "integer", false), col("b_id", "integer", true)), "id"), "a_b_fkey", "b", "b_id")
	b := withForeignKey(withPrimaryKey(table("b", col("id", "integer", false), col("a_id", "integer", true)), "id"), "b_a_fkey", "a", "a_id")
	c := withForeignKey(withPrimaryKey(table("c", col("id", "integer", false), col("a_id", "integer", true)), "id"), "c_a_fkey", "a", "a_id")
	_, m := planFor(t, snapshot(DriverPostgres, "public", a, b, c), snapshot(DriverPostgres, "public"))
	script := m.Script()
	// c points at a, so c goes first; a and b point at each other, so one key
	// is dropped to let either of them go.
	before(t, script, `DROP TABLE "public"."c"`, `DROP TABLE "public"."a"`)
	if got := strings.Count(script, "DROP CONSTRAINT"); got != 1 {
		t.Errorf("%d keys dropped, want the one that breaks the cycle:\n%s", got, script)
	}
	before(t, script, "DROP CONSTRAINT", `DROP TABLE "public"."a"`)
}

func TestAViewIsDroppedBeforeTheColumnItReadsAndMadeAgainAfter(t *testing.T) {
	from := snapshot(DriverPostgres, "public", table("users", col("id", "integer", false), col("legacy", "text", true)))
	to := snapshot(DriverPostgres, "public", table("users", col("id", "integer", false), col("email", "text", true)))
	from.Views["v_users"] = SnapshotView{Name: "v_users", Definition: "CREATE VIEW v_users AS SELECT id, legacy FROM users"}
	to.Views["v_users"] = SnapshotView{Name: "v_users", Definition: "CREATE VIEW v_users AS SELECT id, email FROM users"}
	_, m := planFor(t, from, to)
	script := m.Script()
	before(t, script, `DROP VIEW "public"."v_users"`, `DROP COLUMN "legacy"`)
	before(t, script, `ADD COLUMN "email"`, "CREATE VIEW v_users AS SELECT id, email")
	if strings.Count(script, "DESTRUCTIVE") != 2 {
		t.Errorf("the view and the column are both drops:\n%s", script)
	}
}

func TestViewsAreOrderedByWhatTheyRead(t *testing.T) {
	views := []viewDiff{
		{name: "report", to: &SnapshotView{Definition: "select * from summary join users using (id)"}},
		{name: "summary", to: &SnapshotView{Definition: "select id from totals"}},
		{name: "totals", to: &SnapshotView{Definition: "select id from users"}},
		{name: "unrelated", to: &SnapshotView{Definition: "select 1 as summarised"}},
	}
	read := func(v viewDiff) (string, string) { return v.name, v.to.Definition }
	names := func(order []viewDiff) []string {
		out := make([]string, len(order))
		for i, v := range order {
			out[i] = v.name
		}
		return out
	}
	created := names(orderViews(views, read, true))
	for i, want := range []string{"totals", "summary", "report"} {
		found := -1
		for j, name := range created {
			if name == want {
				found = j
			}
		}
		if found < 0 || (i > 0 && found < positionOf(created, []string{"totals", "summary"}[i-1])) {
			t.Errorf("create order = %v", created)
		}
	}
	dropped := names(orderViews(views, read, false))
	if positionOf(dropped, "report") > positionOf(dropped, "summary") || positionOf(dropped, "summary") > positionOf(dropped, "totals") {
		t.Errorf("drop order = %v: a view goes before the ones it reads", dropped)
	}
	// A cycle is cut rather than looped on.
	loop := []viewDiff{
		{name: "a", to: &SnapshotView{Definition: "select * from b"}},
		{name: "b", to: &SnapshotView{Definition: "select * from a"}},
	}
	if got := orderViews(loop, read, true); len(got) != 2 {
		t.Errorf("a cycle lost a view: %v", names(got))
	}
}

func positionOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

func TestAnEnumTypeExistsBeforeTheColumnThatUsesIt(t *testing.T) {
	from := snapshot(DriverPostgres, "public", table("users", col("id", "integer", false)))
	to := snapshot(DriverPostgres, "public", table("users", col("id", "integer", false), col("mood", "mood", true)))
	to.Enums["mood"] = []string{"sad", "ok"}
	_, m := planFor(t, from, to)
	script := m.Script()
	before(t, script, `CREATE TYPE "public"."mood" AS ENUM ('sad', 'ok')`, `ADD COLUMN "mood"`)
}

func TestAnEnumGainsLabelsInPlaceAndASmallerOneNeedsYou(t *testing.T) {
	from := snapshot(DriverPostgres, "public")
	to := snapshot(DriverPostgres, "public")
	from.Enums["mood"], to.Enums["mood"] = []string{"sad", "ok"}, []string{"sad", "meh", "ok", "great"}
	_, m := planFor(t, from, to)
	script := m.Script()
	for _, want := range []string{`ADD VALUE 'meh' AFTER 'sad'`, `ADD VALUE 'great' AFTER 'ok'`} {
		if !strings.Contains(script, want) {
			t.Errorf("no %q:\n%s", want, script)
		}
	}
	to.Enums["mood"] = []string{"ok"}
	_, m = planFor(t, from, to)
	if _, _, manual := m.Counts(); manual != 1 || !strings.Contains(m.Script(), "NEEDS YOUR ATTENTION: Change enum type mood") {
		t.Errorf("removing a label is not left to the operator:\n%s", m.Script())
	}
}

func TestADroppedColumnTakesItsSequenceWithIt(t *testing.T) {
	users := withPrimaryKey(table("users", col("id", "integer", false), col("n", "integer", true)), "id")
	bare := withPrimaryKey(table("users", col("id", "integer", false)), "id")
	from, to := snapshot(DriverPostgres, "public", users), snapshot(DriverPostgres, "public", bare)
	from.Sequences["users_n_seq"] = SnapshotSequence{Name: "users_n_seq", Owner: "users.n", Definition: "CREATE SEQUENCE users_n_seq"}
	from.Sequences["spare_seq"] = SnapshotSequence{Name: "spare_seq", Definition: "CREATE SEQUENCE spare_seq"}
	_, m := planFor(t, from, to)
	script := m.Script()
	if strings.Contains(script, "users_n_seq") {
		t.Errorf("the sequence the column owned is dropped a second time:\n%s", script)
	}
	if !strings.Contains(script, `DROP SEQUENCE "public"."spare_seq"`) {
		t.Errorf("a sequence nobody owns is kept:\n%s", script)
	}
	before(t, script, "DROP COLUMN", "DROP SEQUENCE")
}

func TestASequenceIsMadeBeforeTheTableThatDefaultsToItAndHandedToItAfter(t *testing.T) {
	from := snapshot(DriverPostgres, "public")
	users := withPrimaryKey(table("users", col("id", "integer", false)), "id")
	users.Columns[0].Default = "nextval('users_id_seq'::regclass)"
	to := snapshot(DriverPostgres, "public", users)
	to.Sequences["users_id_seq"] = SnapshotSequence{Name: "users_id_seq", Owner: "users.id", Definition: "CREATE SEQUENCE public.users_id_seq"}
	_, m := planFor(t, from, to)
	script := m.Script()
	before(t, script, "CREATE SEQUENCE public.users_id_seq", `CREATE TABLE "public"."users"`)
	before(t, script, `CREATE TABLE "public"."users"`, `ALTER SEQUENCE "public"."users_id_seq" OWNED BY "public"."users"."id"`)
}

func TestAChangedColumnIsAlteredAndALossyOneIsMarked(t *testing.T) {
	from := snapshot(DriverPostgres, "public", table("t", col("id", "integer", false), col("a", "varchar(10)", true), col("b", "text", true), col("c", "text", true)))
	toTable := table("t", col("id", "integer", false), col("a", "varchar(10)", false), col("b", "text", true), col("c", "integer", true))
	toTable.Columns[2].Default = "'x'::text"
	_, m := planFor(t, from, snapshot(DriverPostgres, "public", toTable))
	byTitle := map[string]MigrationStep{}
	for _, s := range m.Steps {
		byTitle[s.Title] = s
	}
	if s := byTitle["Change column a of t"]; s.Destructive || len(s.Statements) != 1 || !strings.Contains(s.Statements[0], "SET NOT NULL") {
		t.Errorf("a nullability change = %+v", s)
	}
	if s := byTitle["Change column b of t"]; s.Destructive || !strings.Contains(strings.Join(s.Statements, ";"), "SET DEFAULT") {
		t.Errorf("a default change = %+v", s)
	}
	if s := byTitle["Change column c of t"]; !s.Destructive || !strings.Contains(strings.Join(s.Statements, ";"), "TYPE integer") {
		t.Errorf("a type change is not marked destructive: %+v", s)
	}
}

func TestWhatTheGeneratorCannotWriteIsLeftToTheOperatorAndSaysSo(t *testing.T) {
	// A NOT NULL column with no default cannot be added to a table that has rows.
	from := snapshot(DriverPostgres, "public", table("t", col("id", "integer", false)))
	to := snapshot(DriverPostgres, "public", table("t", col("id", "integer", false), col("name", "text", false)))
	_, m := planFor(t, from, to)
	if len(m.Steps) != 1 || !m.Steps[0].Manual || len(m.Steps[0].Statements) != 0 || !strings.Contains(m.Steps[0].Note, "needs a default") {
		t.Fatalf("steps = %+v", m.Steps)
	}
	if script := m.Script(); !strings.Contains(script, "-- NEEDS YOUR ATTENTION: Add column name to t\n-- a NOT NULL column") {
		t.Errorf("script:\n%s", script)
	}
	// An identity column is not one the add-column form writes.
	to.Tables["t"].Columns[1] = Column{Name: "seq", Type: "bigint", Identity: "always"}
	_, m = planFor(t, from, to)
	if len(m.Steps) != 1 || !m.Steps[0].Manual {
		t.Errorf("identity steps = %+v", m.Steps)
	}
}

func TestSQLiteCannotAlterAColumnAndSaysWhy(t *testing.T) {
	from := snapshot(DriverSQLite, "main", table("t", col("id", "INTEGER", false), col("a", "TEXT", true)))
	to := snapshot(DriverSQLite, "main", table("t", col("id", "INTEGER", false), col("a", "INTEGER", true)))
	_, m := planFor(t, from, to)
	if len(m.Steps) != 1 || !m.Steps[0].Manual || !strings.Contains(m.Steps[0].Note, "SQLite cannot change a column") {
		t.Fatalf("steps = %+v", m.Steps)
	}
	if !m.Steps[0].Destructive {
		t.Error("a type change is destructive even when it is left to the operator")
	}
}

func TestANewSQLiteTableIsItsOwnCreateTextAndHoldsItsKeys(t *testing.T) {
	orders := withForeignKey(table("orders", col("id", "INTEGER", false), col("user_id", "INTEGER", true)), "fk", "users", "user_id")
	orders.CreateSQL = "CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INTEGER REFERENCES users(id))"
	_, m := planFor(t, snapshot(DriverSQLite, "main"), snapshot(DriverSQLite, "main", orders))
	if len(m.Steps) != 1 || m.Steps[0].Statements[0] != orders.CreateSQL {
		t.Fatalf("steps = %+v", m.Steps)
	}
}

func TestAMySQLTableKeepsItsEngineTextWithoutItsForeignKeys(t *testing.T) {
	orders := withForeignKey(table("orders", col("id", "int", false), col("user_id", "int", true)), "orders_ibfk_1", "users", "user_id")
	orders.CreateSQL = "CREATE TABLE `orders` (\n  `id` int NOT NULL AUTO_INCREMENT,\n  `user_id` int DEFAULT NULL,\n  PRIMARY KEY (`id`),\n" +
		"  KEY `user_id` (`user_id`),\n  CONSTRAINT `orders_ibfk_1` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`)\n" +
		") ENGINE=InnoDB AUTO_INCREMENT=42 DEFAULT CHARSET=utf8mb4"
	from, to := snapshot(DriverMySQL, "shop"), snapshot(DriverMySQL, "shop_copy", orders)
	_, m := planFor(t, from, to)
	if len(m.Steps) < 2 {
		t.Fatalf("steps = %+v", m.Steps)
	}
	create := m.Steps[0].Statements[0]
	want := "CREATE TABLE `shop`.`orders` (\n  `id` int NOT NULL AUTO_INCREMENT,\n  `user_id` int DEFAULT NULL,\n  PRIMARY KEY (`id`),\n  KEY `user_id` (`user_id`)\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"
	if create != want {
		t.Errorf("create =\n%s\nwant\n%s", create, want)
	}
	last := m.Steps[len(m.Steps)-1]
	if !strings.Contains(last.Statements[0], "ADD CONSTRAINT `orders_ibfk_1` FOREIGN KEY (`user_id`) REFERENCES `shop`.`users` (`id`)") {
		t.Errorf("the key is added once the tables exist, in the schema being changed: %+v", last)
	}
}

func TestADefinerIsNotCarriedToAnotherServer(t *testing.T) {
	got := stripDefiner(DriverMySQL, "CREATE DEFINER=`root`@`%` FUNCTION `f`() RETURNS int RETURN 1")
	if got != "CREATE FUNCTION `f`() RETURNS int RETURN 1" {
		t.Errorf("definer kept: %q", got)
	}
	if got := stripDefiner(DriverPostgres, "CREATE FUNCTION f() RETURNS int AS 'SELECT 1'"); !strings.HasPrefix(got, "CREATE FUNCTION f()") {
		t.Errorf("another engine's text changed: %q", got)
	}
}

func TestACheckConditionComesOutOfItsClause(t *testing.T) {
	for in, want := range map[string]string{
		"CHECK ((age >= 0))":          "age >= 0",
		"CHECK (price > 0)":           "price > 0",
		"check   (a = 1 AND (b = 2))": "a = 1 AND (b = 2)",
		// Parentheses that do not enclose the whole are not a pair to strip.
		"CHECK (a > 0) AND (b > 0)": "(a > 0) AND (b > 0)",
	} {
		if got := checkExpression(in); got != want {
			t.Errorf("checkExpression(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestACastOnALiteralDefaultIsTakenOff(t *testing.T) {
	for in, want := range map[string]string{
		"'ok'::mood":                   "'ok'",
		"'pending'::character varying": "'pending'",
		"(0)::numeric":                 "0",
		"'{}'::jsonb":                  "'{}'",
		"now()":                        "now()",
		"nextval('s'::regclass)":       "nextval('s'::regclass)",
		"lower('A'::text)":             "lower('A'::text)",
	} {
		if got := plainDefault(DriverPostgres, in); got != want {
			t.Errorf("plainDefault(%q) = %q, want %q", in, got, want)
		}
	}
	if got := plainDefault(DriverMySQL, "'a'::b"); got != "'a'::b" {
		t.Errorf("another engine's default changed: %q", got)
	}
}

func TestRebasingNamesTheSchemaBeingChanged(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{`nextval('alpha.t_id_seq'::regclass)`, `nextval('beta.t_id_seq'::regclass)`},
		{`"alpha"."mood"`, `"beta"."mood"`},
		{"`alpha`.`t`", "`beta`.`t`"},
		{"[alpha].[t]", "[beta].[t]"},
		{`x_alpha.t`, `x_alpha.t`},
	} {
		if got := rebaseSchemaName(c.in, "alpha", "beta", false); got != c.want {
			t.Errorf("rebase(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTheScriptMarksWhatNeedsAttentionAndEndsEveryStatement(t *testing.T) {
	m := &Migration{Steps: []MigrationStep{
		{Title: "Create table a", Statements: []string{"CREATE TABLE a (id int);"}},
		{Title: "Drop table b", Statements: []string{"DROP TABLE b"}, Destructive: true},
		{Title: "Change sequence s", Manual: true, Note: "First line\nSecond line"},
	}}
	want := "-- Create table a\nCREATE TABLE a (id int);\n\n" +
		"-- Drop table b\n-- DESTRUCTIVE: this removes data or definitions that cannot be restored from this script\nDROP TABLE b;\n\n" +
		"-- NEEDS YOUR ATTENTION: Change sequence s\n-- First line\n-- Second line\n"
	if got := m.Script(); got != want {
		t.Errorf("script =\n%s\nwant\n%s", got, want)
	}
}

func TestTheSchemaComparisonIsOfferedToTheEnginesThatCanDoIt(t *testing.T) {
	want := map[string]bool{
		"postgres/postgres": true, "postgres/timescaledb": false, "postgres/cockroachdb": false, "postgres/yugabytedb": false,
		"mysql/mysql": true, "mysql/mariadb": true, "mysql/percona": true, "mysql/tidb": false,
		"sqlserver/sqlserver": true, "sqlserver/azure-sql-edge": false, "sqlite/sqlite": true,
		"clickhouse/clickhouse": false, "oracle/oracle": false, "mongodb/mongodb": false, "redis/redis": false,
	}
	for _, d := range Drivers() {
		for _, flavor := range Flavors(d) {
			key := string(d) + "/" + flavor
			expected, listed := want[key]
			if !listed {
				if Capable(d, flavor, "schemaDiff") {
					t.Errorf("%s is offered the schema comparison and is not in this list", key)
				}
				continue
			}
			if got := Capable(d, flavor, "schemaDiff"); got != expected {
				t.Errorf("%s schemaDiff = %v, want %v", key, got, expected)
			}
		}
	}
}

func TestSQLServerDefaultsLoseTheirParentheses(t *testing.T) {
	for in, want := range map[string]string{
		"((0))":              "0",
		"('anon')":           "'anon'",
		"(sysutcdatetime())": "sysutcdatetime()",
		"(len('(x)')+(1))":   "len('(x)')+(1)",
		"(1)+(2)":            "(1)+(2)",
		"( ( 'a' ) )":        "'a'",
		"('it''s (here)')":   "'it''s (here)'",
	} {
		if got := plainDefault(DriverMSSQL, in); got != want {
			t.Errorf("plainDefault(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSQLServerReplacesARoutineInPlace(t *testing.T) {
	for in, want := range map[string]string{
		"CREATE FUNCTION s.f() RETURNS int AS BEGIN RETURN 1 END": "CREATE OR ALTER FUNCTION s.f() RETURNS int AS BEGIN RETURN 1 END",
		"create procedure s.p as select 1":                        "create or alter procedure s.p as select 1",
		"CREATE PROC s.p AS SELECT 1":                             "CREATE OR ALTER PROC s.p AS SELECT 1",
	} {
		if got := createOrAlter(in); got != want {
			t.Errorf("createOrAlter(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestASequenceBehindAnIdentityColumnIsTheColumnsOwn(t *testing.T) {
	payments := table("payments", Column{Name: "id", Type: "bigint", Identity: "always"})
	snap := snapshot(DriverPostgres, "public", payments, table("users", col("id", "integer", false)))
	snap.Sequences["payments_id_seq"] = SnapshotSequence{Name: "payments_id_seq", Owner: "payments.id"}
	snap.Sequences["users_id_seq"] = SnapshotSequence{Name: "users_id_seq", Owner: "users.id"}
	snap.Sequences["spare_seq"] = SnapshotSequence{Name: "spare_seq"}
	snap.dropIdentitySequences()
	if _, ok := snap.Sequences["payments_id_seq"]; ok {
		t.Error("the sequence an identity column makes is compared on its own")
	}
	if len(snap.Sequences) != 2 {
		t.Errorf("sequences = %v", snap.Sequences)
	}
}

func TestANewSequenceIsMadeWithoutAnOwnerItsColumnCannotYetHave(t *testing.T) {
	from := snapshot(DriverPostgres, "public")
	users := withPrimaryKey(table("users", col("id", "integer", false)), "id")
	to := snapshot(DriverPostgres, "public", users)
	to.Sequences["users_id_seq"] = SnapshotSequence{Name: "users_id_seq", Owner: "users.id",
		Definition: "CREATE SEQUENCE \"public\".\"users_id_seq\"\n  AS integer\n  NO CYCLE\n  OWNED BY \"public\".\"users\".\"id\";"}
	_, m := planFor(t, from, to)
	script := m.Script()
	if strings.Contains(script, "NO CYCLE\n  OWNED BY") || !strings.Contains(script, "NO CYCLE;") {
		t.Errorf("the CREATE still names an owner:\n%s", script)
	}
	before(t, script, "CREATE SEQUENCE", `CREATE TABLE "public"."users"`)
	before(t, script, `CREATE TABLE "public"."users"`, "OWNED BY")
}

func TestAMySQLUniqueKeyIsOneDifferenceNotTwo(t *testing.T) {
	a, b := table("users", col("id", "int", false), col("email", "varchar(120)", false)), table("users", col("id", "int", false), col("email", "varchar(120)", false))
	b.Indexes = []Index{{Name: "users_email_key", Columns: []string{"email"}, Unique: true, Method: "BTREE"}}
	b.Constraints = []Constraint{{Name: "users_email_key", Type: ConstraintUnique, Columns: []string{"email"}}}
	diff, m := planFor(t, snapshot(DriverMySQL, "s", a), snapshot(DriverMySQL, "s", b))
	if len(diff.Objects) != 1 || len(diff.Objects[0].Parts) != 1 || diff.Objects[0].Parts[0].Part != PartUnique {
		t.Fatalf("objects = %+v", diff.Objects)
	}
	if got := strings.Count(m.Script(), "users_email_key"); got != 2 {
		t.Errorf("the key is written %d times, want its title and one statement:\n%s", got, m.Script())
	}
}

func TestAMySQLRoutineIsNamedInTheSchemaBeingChanged(t *testing.T) {
	from, to := snapshot(DriverMySQL, "live"), snapshot(DriverMySQL, "copy")
	to.Routines[routineKey(KindFunction, "add_two", "x int")] = SnapshotRoutine{Kind: KindFunction, Name: "add_two", Signature: "x int",
		Definition: "CREATE FUNCTION `add_two`(x int) RETURNS int\n    DETERMINISTIC\nRETURN x + 2"}
	_, m := planFor(t, from, to)
	want := "CREATE FUNCTION `live`.`add_two`(x int) RETURNS int\n    DETERMINISTIC\nRETURN x + 2"
	if len(m.Steps) != 1 || m.Steps[0].Statements[0] != want {
		t.Errorf("steps = %+v", m.Steps)
	}
}
