package dbx

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A stored program is code the engine runs later, so these tests run it: each
// engine makes a function, a procedure and a trigger through the plans the
// forms use, reads them back as the editor's fields, calls them, edits them,
// is given an edit it refuses and still holds the old one, and drops them.
// Every object is named jdt_routine_editor_* and dropped afterwards.

type programCase struct {
	driver Driver
	db     *sql.DB
	schema string
	// setup makes the two tables the programs work on; cleanup removes
	// everything the case made, and runs first so a failed run leaves nothing.
	setup, cleanup []string
	// quoted names the engine's spelling of the tables for the checks below.
	table, log string

	fn, fnEdited, fnBroken RoutineSpec
	call                   string // a query that returns the function's value
	want, wantEdited       int
	// fnWider changes the parameters, which Postgres can only do by dropping.
	fnWider  *RoutineSpec
	callWide string

	proc, procEdited RoutineSpec
	callProc         string

	// helpers are routines the trigger needs first (a Postgres trigger function).
	helpers                      []RoutineSpec
	trig, trigEdited, trigBroken TriggerSpec
	insert                       func(id int) string
}

func (c programCase) exec(t *testing.T, statements ...string) {
	t.Helper()
	for _, s := range statements {
		if _, err := c.db.ExecContext(context.Background(), s); err != nil {
			t.Fatalf("%v\n%s", err, s)
		}
	}
}

func (c programCase) number(t *testing.T, query string) int {
	t.Helper()
	var n sql.NullInt64
	if err := c.db.QueryRowContext(context.Background(), query).Scan(&n); err != nil {
		t.Fatalf("%v\n%s", err, query)
	}
	return int(n.Int64)
}

func (c programCase) save(t *testing.T, spec RoutineSpec, edit *RoutineRef) *DDLPlan {
	t.Helper()
	plan, err := PlanSaveRoutine(context.Background(), c.db, c.driver, spec, edit)
	if err != nil {
		t.Fatalf("plan %s: %v", spec.Name, err)
	}
	if err := plan.Exec(context.Background(), c.db); err != nil {
		t.Fatalf("%v\n%s", err, plan)
	}
	return plan
}

// signature is the catalogue's name for one overload, which is what the page
// hands back when it asks to edit or drop one.
func (c programCase) signature(t *testing.T, kind, name string) string {
	t.Helper()
	catalog, err := ReadCatalog(context.Background(), c.db, c.driver, CatalogOptions{Schema: c.schema})
	if err != nil {
		t.Fatal(err)
	}
	group := GroupFunctions
	if kind == KindProcedure {
		group = GroupProcedures
	}
	for _, o := range catalog.Objects[group] {
		if o.Name == name {
			return o.Signature
		}
	}
	t.Fatalf("the catalogue lists no %s %s: %v", kind, name, catalog.Objects[group])
	return ""
}

func (c programCase) read(t *testing.T, ref ObjectRef) *Program {
	t.Helper()
	ref.Schema = c.schema
	p, err := ReadProgram(context.Background(), c.db, c.driver, ref)
	if err != nil {
		t.Fatalf("read %+v: %v", ref, err)
	}
	if !p.Editable {
		t.Fatalf("%s %s is not editable: %s", ref.Kind, ref.Name, p.Reason)
	}
	return p
}

func (c programCase) run(t *testing.T) {
	ctx := context.Background()
	clean := func() {
		for _, s := range c.cleanup {
			_, _ = c.db.ExecContext(ctx, s)
		}
	}
	clean()
	t.Cleanup(clean)
	c.exec(t, c.setup...)

	if c.driver != DriverSQLite {
		c.functions(t)
		c.procedures(t)
	} else {
		for _, kind := range []string{KindFunction, KindProcedure} {
			_, err := PlanSaveRoutine(ctx, c.db, c.driver, RoutineSpec{Kind: kind, Name: "x", Body: "x"}, nil)
			if err == nil || !strings.Contains(err.Error(), "SQLite has no stored") {
				t.Errorf("SQLite %s: %v", kind, err)
			}
		}
	}
	c.triggers(t)
}

func (c programCase) functions(t *testing.T) {
	ctx := context.Background()
	c.save(t, c.fn, nil)
	if got := c.number(t, c.call); got != c.want {
		t.Fatalf("the new function returns %d, want %d", got, c.want)
	}
	// Asked for again by the same name, the engine refuses: a new function is
	// not a replacement.
	if plan, err := PlanSaveRoutine(ctx, c.db, c.driver, c.fn, nil); err == nil {
		if err := plan.Exec(ctx, c.db); err == nil && c.driver != DriverPostgres {
			t.Errorf("creating %s twice succeeded", c.fn.Name)
		}
	}

	sig := c.signature(t, KindFunction, c.fn.Name)
	read := c.read(t, ObjectRef{Kind: KindFunction, Name: c.fn.Name, Signature: sig}).Routine
	if len(read.Args) != len(c.fn.Args) {
		t.Fatalf("read %d parameters, made %d: %+v", len(read.Args), len(c.fn.Args), read.Args)
	}
	for i, a := range read.Args {
		// MariaDB reads an int back with its display width.
		if !strings.EqualFold(a.Name, c.fn.Args[i].Name) || !strings.HasPrefix(strings.ToLower(a.Type), strings.ToLower(c.fn.Args[i].Type)) ||
			a.Default != c.fn.Args[i].Default {
			t.Errorf("parameter %d read as %+v, made as %+v", i, a, c.fn.Args[i])
		}
	}
	if !strings.HasPrefix(strings.ToLower(read.Returns), strings.ToLower(c.fn.Returns)) || !strings.Contains(read.Body, strings.TrimRight(c.fn.Body, "; \n")) {
		t.Errorf("read returns %q body %q; made returns %q body %q", read.Returns, read.Body, c.fn.Returns, c.fn.Body)
	}

	ref := &RoutineRef{Schema: c.schema, Name: c.fn.Name, Signature: sig}
	plan := c.save(t, c.fnEdited, ref)
	if got := c.number(t, c.call); got != c.wantEdited {
		t.Fatalf("the edited function returns %d, want %d\n%s", got, c.wantEdited, plan)
	}
	t.Logf("%s edit: %s%s", c.driver, strings.ReplaceAll(plan.String(), "\n", " "), plan.Note)
	if c.driver == DriverMySQL && !strings.Contains(plan.Note, "OR REPLACE") && len(plan.Statements) == 4 {
		// MySQL (not MariaDB) tries the new definition under another name first.
		if !strings.Contains(plan.Statements[0], "jd_check_"+c.fn.Name) || !strings.HasPrefix(plan.Statements[1], "DROP FUNCTION") ||
			!strings.Contains(plan.Statements[1], "jd_check_") || plan.guard != 2 {
			t.Errorf("the MySQL edit does not try the definition first: %v (guard %d)", plan.Statements, plan.guard)
		}
	}

	// An edit the engine refuses leaves the edited function where it was.
	broken, err := PlanSaveRoutine(ctx, c.db, c.driver, c.fnBroken, ref)
	if err != nil {
		t.Fatalf("plan of a broken body: %v", err)
	}
	err = broken.Exec(ctx, c.db)
	if err == nil {
		t.Fatalf("the engine accepted a broken body:\n%s", broken)
	}
	t.Logf("%s refusal: %v", c.driver, err)
	if got := c.number(t, c.call); got != c.wantEdited {
		t.Errorf("after a refused edit the function returns %d, want %d: the old one was lost", got, c.wantEdited)
	}
	if after := c.read(t, ObjectRef{Kind: KindFunction, Name: c.fn.Name, Signature: sig}).Routine; !strings.Contains(after.Body, strings.TrimRight(c.fnEdited.Body, "; \n")) {
		t.Errorf("after a refused edit the body reads %q", after.Body)
	}

	// A new routine the engine refuses is not left behind, whatever the engine
	// does with one that does not compile (Oracle keeps it, invalid).
	bad := c.fnBroken
	bad.Name += "_new"
	newPlan, err := PlanSaveRoutine(ctx, c.db, c.driver, bad, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := newPlan.Exec(ctx, c.db); err == nil {
		t.Fatalf("the engine accepted a new broken routine:\n%s", newPlan)
	}
	if _, err := ReadProgram(ctx, c.db, c.driver, ObjectRef{Kind: KindFunction, Schema: c.schema, Name: bad.Name}); !errors.Is(err, ErrObjectNotFound) {
		t.Errorf("a refused new routine was left behind: %v", err)
	}

	if c.fnWider != nil {
		wide := c.save(t, *c.fnWider, ref)
		if wide.Note == "" || !wide.atomic {
			t.Errorf("a changed signature should say it drops and creates in one transaction: %+v", wide)
		}
		if got := c.number(t, c.callWide); got == 0 {
			t.Errorf("the widened function returned %d", got)
		}
		ref.Signature = c.signature(t, KindFunction, c.fn.Name)
	}

	drop, err := PlanDropRoutine(ctx, c.db, c.driver, KindFunction, c.schema, c.fn.Name, ref.Signature)
	if err != nil {
		t.Fatal(err)
	}
	if err := drop.Exec(ctx, c.db); err != nil {
		t.Fatalf("%v\n%s", err, drop)
	}
	if _, err := ReadProgram(ctx, c.db, c.driver, ObjectRef{Kind: KindFunction, Schema: c.schema, Name: c.fn.Name}); !errors.Is(err, ErrObjectNotFound) {
		t.Errorf("a dropped function is read as: %v", err)
	}
}

func (c programCase) procedures(t *testing.T) {
	ctx := context.Background()
	c.save(t, c.proc, nil)
	c.exec(t, c.callProc)
	if got := c.number(t, "SELECT COUNT(*) FROM "+c.log); got != 1 {
		t.Fatalf("the procedure wrote %d rows, want 1", got)
	}
	sig := c.signature(t, KindProcedure, c.proc.Name)
	read := c.read(t, ObjectRef{Kind: KindProcedure, Name: c.proc.Name, Signature: sig}).Routine
	if len(read.Args) != 1 || !strings.EqualFold(read.Args[0].Name, c.proc.Args[0].Name) {
		t.Errorf("procedure parameters read as %+v", read.Args)
	}
	ref := &RoutineRef{Schema: c.schema, Name: c.proc.Name, Signature: sig}
	c.save(t, c.procEdited, ref)
	c.exec(t, c.callProc)
	if got := c.number(t, "SELECT COUNT(*) FROM "+c.log); got != 3 {
		t.Fatalf("the edited procedure wrote %d rows in all, want 3", got)
	}
	drop, err := PlanDropRoutine(ctx, c.db, c.driver, KindProcedure, c.schema, c.proc.Name, sig)
	if err != nil {
		t.Fatal(err)
	}
	if err := drop.Exec(ctx, c.db); err != nil {
		t.Fatalf("%v\n%s", err, drop)
	}
	c.exec(t, "DELETE FROM "+c.log)
}

func (c programCase) triggers(t *testing.T) {
	ctx := context.Background()
	for _, h := range c.helpers {
		c.save(t, h, nil)
	}
	plan, err := PlanSaveTrigger(ctx, c.db, c.driver, c.trig, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Exec(ctx, c.db); err != nil {
		t.Fatalf("%v\n%s", err, plan)
	}
	c.exec(t, c.insert(1))
	if got := c.number(t, "SELECT COALESCE(SUM(id), 0) FROM "+c.log); got != 10 {
		t.Fatalf("the trigger wrote %d, want 10", got)
	}

	ref := ObjectRef{Kind: KindTrigger, Name: c.trig.Name, Table: c.trig.Table}
	read := c.read(t, ref).Trigger
	if !strings.EqualFold(read.Table, c.trig.Table) || read.Timing != c.trig.Timing || read.ForEach != c.trig.ForEach ||
		strings.Join(read.Events, ",") != strings.Join(c.trig.Events, ",") {
		t.Errorf("trigger read as %+v, made as %+v", read, c.trig)
	}
	edit := &TriggerRef{Schema: c.schema, Name: c.trig.Name, Table: c.trig.Table}
	edited, err := PlanSaveTrigger(ctx, c.db, c.driver, c.trigEdited, edit)
	if err != nil {
		t.Fatal(err)
	}
	if err := edited.Exec(ctx, c.db); err != nil {
		t.Fatalf("%v\n%s", err, edited)
	}
	t.Logf("%s trigger edit: %s%s", c.driver, strings.ReplaceAll(edited.String(), "\n", " "), edited.Note)
	c.exec(t, c.insert(2))
	if got := c.number(t, "SELECT COALESCE(SUM(id), 0) FROM "+c.log); got != 210 {
		t.Fatalf("the edited trigger left %d in all, want 210", got)
	}

	broken, err := PlanSaveTrigger(ctx, c.db, c.driver, c.trigBroken, edit)
	if err != nil {
		t.Fatal(err)
	}
	if err := broken.Exec(ctx, c.db); err == nil {
		t.Fatalf("the engine accepted a broken trigger:\n%s", broken)
	} else {
		t.Logf("%s trigger refusal: %v", c.driver, err)
	}
	// A new trigger the engine refuses is not left behind either: Oracle would
	// keep it enabled, and every write to the table would fail with ORA-04098.
	stray := c.trigBroken
	stray.Name += "_new"
	strayPlan, err := PlanSaveTrigger(ctx, c.db, c.driver, stray, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := strayPlan.Exec(ctx, c.db); err == nil {
		t.Fatalf("the engine accepted a new broken trigger:\n%s", strayPlan)
	}
	if _, err := ReadProgram(ctx, c.db, c.driver, ObjectRef{Kind: KindTrigger, Schema: c.schema, Name: stray.Name}); !errors.Is(err, ErrObjectNotFound) {
		t.Errorf("a refused new trigger was left behind: %v", err)
	}
	c.exec(t, c.insert(3))
	if got := c.number(t, "SELECT COALESCE(SUM(id), 0) FROM "+c.log); got != 510 {
		t.Errorf("after a refused edit the trigger left %d in all, want 510: the old one was lost", got)
	}

	drop, err := PlanDropTrigger(c.driver, c.schema, c.trig.Name, c.trig.Table)
	if err != nil {
		t.Fatal(err)
	}
	if err := drop.Exec(ctx, c.db); err != nil {
		t.Fatalf("%v\n%s", err, drop)
	}
	c.exec(t, c.insert(4))
	if got := c.number(t, "SELECT COALESCE(SUM(id), 0) FROM "+c.log); got != 510 {
		t.Errorf("a dropped trigger still fires: %d", got)
	}
	if _, err := ReadProgram(ctx, c.db, c.driver, ObjectRef{Kind: KindTrigger, Schema: c.schema, Name: c.trig.Name}); !errors.Is(err, ErrObjectNotFound) {
		t.Errorf("a dropped trigger is read as: %v", err)
	}
}

func TestLivePostgresStoredPrograms(t *testing.T) {
	db := catalogLive(t, DriverPostgres, "JD_TEST_POSTGRES_DSN")
	const t1, lg = "jdt_routine_editor_t", "jdt_routine_editor_log"
	trigFn := func(name string, times int) RoutineSpec {
		return RoutineSpec{Kind: KindFunction, Name: name, Language: "plpgsql", Returns: "trigger",
			Body: "BEGIN INSERT INTO " + lg + "(id) VALUES (NEW.id * " + itoa(times) + "); RETURN NEW; END;"}
	}
	args := []RoutineArg{{Name: "a", Mode: "in", Type: "integer"}, {Name: "b", Mode: "in", Type: "integer", Default: "1"}}
	fn := RoutineSpec{Kind: KindFunction, Name: "jdt_routine_editor_add", Language: "sql", Returns: "integer",
		Volatility: "immutable", Args: args, Body: "SELECT a + b"}
	edited, broken, wide := fn, fn, fn
	edited.Body, edited.Security = "SELECT a + b + 1", "definer"
	broken.Body = "SELEC a +"
	wide.Args = append(append([]RoutineArg{}, args...), RoutineArg{Name: "c", Mode: "in", Type: "integer", Default: "0"})
	wide.Body = "SELECT a + b + c"
	proc := RoutineSpec{Kind: KindProcedure, Name: "jdt_routine_editor_put", Language: "plpgsql",
		Args: []RoutineArg{{Name: "n", Mode: "in", Type: "integer"}}, Body: "BEGIN INSERT INTO " + lg + "(id) VALUES (n); END;"}
	procEdited := proc
	procEdited.Body = "BEGIN INSERT INTO " + lg + "(id) VALUES (n); INSERT INTO " + lg + "(id) VALUES (n + 1); END;"
	trig := TriggerSpec{Name: "jdt_routine_editor_trg", Table: t1, Timing: "after", Events: []string{"insert"}, ForEach: "row",
		Function: "jdt_routine_editor_trg_10"}
	trigEdited := trig
	trigEdited.Function = "jdt_routine_editor_trg_100"
	trigEdited.Condition = "new.id > 1"
	trigBroken := trig
	trigBroken.Condition = "new.no_such_column > 1"
	programCase{
		driver: DriverPostgres, db: db, schema: "public", table: t1, log: lg,
		setup: []string{"CREATE TABLE " + t1 + " (id integer)", "CREATE TABLE " + lg + " (id integer)"},
		cleanup: []string{
			"DROP TABLE IF EXISTS " + t1 + " CASCADE", "DROP TABLE IF EXISTS " + lg,
			"DROP FUNCTION IF EXISTS jdt_routine_editor_add(integer, integer)",
			"DROP FUNCTION IF EXISTS jdt_routine_editor_add(integer, integer, integer)",
			"DROP PROCEDURE IF EXISTS jdt_routine_editor_put(integer)",
			"DROP FUNCTION IF EXISTS jdt_routine_editor_trg_10()", "DROP FUNCTION IF EXISTS jdt_routine_editor_trg_100()",
		},
		fn: fn, fnEdited: edited, fnBroken: broken, fnWider: &wide,
		call: "SELECT jdt_routine_editor_add(1, 2)", want: 3, wantEdited: 4,
		callWide: "SELECT jdt_routine_editor_add(1, 2, 3)",
		proc:     proc, procEdited: procEdited, callProc: "CALL jdt_routine_editor_put(5)",
		helpers: []RoutineSpec{trigFn("jdt_routine_editor_trg_10", 10), trigFn("jdt_routine_editor_trg_100", 100)},
		trig:    trig, trigEdited: trigEdited, trigBroken: trigBroken,
		insert: func(id int) string { return "INSERT INTO " + t1 + " (id) VALUES (" + itoa(id) + ")" },
	}.run(t)
}

// The wider function above changes the call's arity, so the case after it
// checks the plan for a changed signature on its own terms.
func TestLivePostgresKeepsACommentWhenItDropsAndCreates(t *testing.T) {
	db := catalogLive(t, DriverPostgres, "JD_TEST_POSTGRES_DSN")
	ctx := context.Background()
	clean := func() { _, _ = db.ExecContext(ctx, "DROP FUNCTION IF EXISTS jdt_routine_editor_note(integer)") }
	clean()
	t.Cleanup(clean)
	spec := RoutineSpec{Kind: KindFunction, Name: "jdt_routine_editor_note", Language: "sql", Returns: "integer",
		Args: []RoutineArg{{Name: "a", Type: "integer"}}, Body: "SELECT a"}
	if _, err := db.ExecContext(ctx, "CREATE FUNCTION jdt_routine_editor_note(a integer) RETURNS integer LANGUAGE sql AS 'SELECT a'"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "COMMENT ON FUNCTION jdt_routine_editor_note(integer) IS 'it''s kept'"); err != nil {
		t.Fatal(err)
	}
	spec.Returns = "bigint"
	plan, err := PlanSaveRoutine(ctx, db, DriverPostgres, spec, &RoutineRef{Schema: "public", Name: spec.Name, Signature: "a integer"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Statements) != 3 || !strings.HasPrefix(plan.Statements[0], "DROP FUNCTION") || !strings.HasPrefix(plan.Statements[2], "COMMENT ON FUNCTION") {
		t.Fatalf("plan = %v", plan.Statements)
	}
	if err := plan.Exec(ctx, db); err != nil {
		t.Fatalf("%v\n%s", err, plan)
	}
	var comment string
	if err := db.QueryRowContext(ctx, "SELECT obj_description('jdt_routine_editor_note(integer)'::regprocedure, 'pg_proc')").Scan(&comment); err != nil || comment != "it's kept" {
		t.Errorf("comment = %q, %v", comment, err)
	}
}

func TestLiveMySQLStoredPrograms(t *testing.T) {
	for _, c := range []struct{ name, admin string }{{"mysql8", "JD_TEST_MYSQL8_ADMIN_DSN"}, {"mariadb", "JD_TEST_MYSQL_ADMIN_DSN"}} {
		t.Run(c.name, func(t *testing.T) {
			admin := catalogLive(t, DriverMySQL, c.admin)
			const database = "jdt_routine_editor"
			if _, err := admin.Exec("DROP DATABASE IF EXISTS " + database); err != nil {
				t.Fatal(err)
			}
			if _, err := admin.Exec("CREATE DATABASE " + database); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE IF EXISTS " + database) })
			d, _ := DialectFor(DriverMySQL)
			db, err := sql.Open("mysql", d.NormaliseDSN(strings.TrimSuffix(os.Getenv(c.admin), "/")+"/"+database))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })

			args := []RoutineArg{{Name: "a", Mode: "in", Type: "int"}, {Name: "b", Mode: "in", Type: "int"}}
			fn := RoutineSpec{Kind: KindFunction, Name: "jdt_routine_editor_add", Returns: "int", Deterministic: true,
				DataAccess: "no sql", Security: "invoker", Args: args, Body: "RETURN a + b"}
			edited, broken := fn, fn
			edited.Body = "RETURN a + b + 1"
			broken.Body = "RETURN a +"
			proc := RoutineSpec{Kind: KindProcedure, Name: "jdt_routine_editor_put",
				Args: []RoutineArg{{Name: "n", Mode: "in", Type: "int"}}, Body: "INSERT INTO jdt_routine_editor_log(id) VALUES (n)"}
			procEdited := proc
			procEdited.Body = "BEGIN\n  INSERT INTO jdt_routine_editor_log(id) VALUES (n);\n  INSERT INTO jdt_routine_editor_log(id) VALUES (n + 1);\nEND"
			trig := func(times int) TriggerSpec {
				return TriggerSpec{Name: "jdt_routine_editor_trg", Table: "jdt_routine_editor_t", Timing: "after",
					Events: []string{"insert"}, ForEach: "row",
					Body: "INSERT INTO jdt_routine_editor_log(id) VALUES (NEW.id * " + itoa(times) + ")"}
			}
			trigBroken := trig(100)
			trigBroken.Body = "INSERT INTO"
			programCase{
				driver: DriverMySQL, db: db, schema: database, table: "jdt_routine_editor_t", log: "jdt_routine_editor_log",
				setup: []string{"CREATE TABLE jdt_routine_editor_t (id int)", "CREATE TABLE jdt_routine_editor_log (id int)"},
				fn:    fn, fnEdited: edited, fnBroken: broken,
				call: "SELECT jdt_routine_editor_add(1, 2)", want: 3, wantEdited: 4,
				proc: proc, procEdited: procEdited, callProc: "CALL jdt_routine_editor_put(5)",
				trig: trig(10), trigEdited: trig(100), trigBroken: trigBroken,
				insert: func(id int) string { return "INSERT INTO jdt_routine_editor_t (id) VALUES (" + itoa(id) + ")" },
			}.run(t)
		})
	}
}

func TestLiveSQLServerStoredPrograms(t *testing.T) {
	db := catalogLive(t, DriverMSSQL, "JD_TEST_MSSQL_DSN")
	const t1, lg = "dbo.jdt_routine_editor_t", "dbo.jdt_routine_editor_log"
	args := []RoutineArg{{Name: "a", Mode: "in", Type: "int"}, {Name: "b", Mode: "in", Type: "int", Default: "1"}}
	fn := RoutineSpec{Kind: KindFunction, Schema: "dbo", Name: "jdt_routine_editor_add", Returns: "int", Security: "invoker",
		Args: args, Body: "BEGIN\n  RETURN @a + @b\nEND"}
	edited, broken := fn, fn
	edited.Body = "BEGIN\n  RETURN @a + @b + 1\nEND"
	broken.Body = "BEGIN\n  RETURN @a +\nEND"
	proc := RoutineSpec{Kind: KindProcedure, Schema: "dbo", Name: "jdt_routine_editor_put",
		Args: []RoutineArg{{Name: "n", Mode: "in", Type: "int"}}, Body: "INSERT INTO " + lg + "(id) VALUES (@n)"}
	procEdited := proc
	procEdited.Body = "INSERT INTO " + lg + "(id) VALUES (@n);\nINSERT INTO " + lg + "(id) VALUES (@n + 1)"
	trig := func(times int) TriggerSpec {
		return TriggerSpec{Schema: "dbo", Name: "jdt_routine_editor_trg", Table: "jdt_routine_editor_t", Timing: "after",
			Events: []string{"insert"}, ForEach: "statement", Body: "INSERT INTO " + lg + "(id) SELECT id * " + itoa(times) + " FROM inserted"}
	}
	trigBroken := trig(100)
	trigBroken.Body = "INSERT INTO"
	programCase{
		driver: DriverMSSQL, db: db, schema: "dbo", table: t1, log: lg,
		setup: []string{"CREATE TABLE " + t1 + " (id int)", "CREATE TABLE " + lg + " (id int)"},
		cleanup: []string{
			"DROP TABLE IF EXISTS " + t1, "DROP TABLE IF EXISTS " + lg,
			"DROP FUNCTION IF EXISTS dbo.jdt_routine_editor_add", "DROP PROCEDURE IF EXISTS dbo.jdt_routine_editor_put",
		},
		fn: fn, fnEdited: edited, fnBroken: broken,
		call: "SELECT dbo.jdt_routine_editor_add(1, 2)", want: 3, wantEdited: 4,
		proc: proc, procEdited: procEdited, callProc: "EXEC dbo.jdt_routine_editor_put 5",
		trig: trig(10), trigEdited: trig(100), trigBroken: trigBroken,
		insert: func(id int) string { return "INSERT INTO " + t1 + " (id) VALUES (" + itoa(id) + ")" },
	}.run(t)
}

func TestLiveOracleStoredPrograms(t *testing.T) {
	db := catalogLive(t, DriverOracle, "JD_TEST_ORACLE_DSN")
	const t1, lg = `"JDT_ROUTINE_EDITOR_T"`, `"JDT_ROUTINE_EDITOR_LOG"`
	args := []RoutineArg{{Name: "A", Mode: "in", Type: "NUMBER"}, {Name: "B", Mode: "in", Type: "NUMBER", Default: "1"}}
	fn := RoutineSpec{Kind: KindFunction, Name: "JDT_ROUTINE_EDITOR_ADD", Returns: "NUMBER", Deterministic: true,
		Security: "invoker", Args: args, Body: "BEGIN\n  RETURN a + b;\nEND;"}
	edited, broken := fn, fn
	edited.Body = "BEGIN\n  RETURN a + b + 1;\nEND;"
	broken.Body = "BEGIN\n  RETURN a + ;\nEND;"
	proc := RoutineSpec{Kind: KindProcedure, Name: "JDT_ROUTINE_EDITOR_PUT",
		Args: []RoutineArg{{Name: "N", Mode: "in", Type: "NUMBER"}}, Body: "BEGIN\n  INSERT INTO " + lg + "(\"ID\") VALUES (n);\nEND;"}
	procEdited := proc
	procEdited.Body = "BEGIN\n  INSERT INTO " + lg + "(\"ID\") VALUES (n);\n  INSERT INTO " + lg + "(\"ID\") VALUES (n + 1);\nEND;"
	trig := func(times int) TriggerSpec {
		return TriggerSpec{Name: "JDT_ROUTINE_EDITOR_TRG", Table: "JDT_ROUTINE_EDITOR_T", Timing: "after",
			Events: []string{"insert"}, ForEach: "row",
			Body: "BEGIN\n  INSERT INTO " + lg + "(\"ID\") VALUES (:NEW.\"ID\" * " + itoa(times) + ");\nEND;"}
	}
	trigBroken := trig(100)
	trigBroken.Body = "BEGIN\n  INSERT INTO ;\nEND;"
	for _, s := range []*RoutineSpec{&fn, &edited, &broken, &proc, &procEdited} {
		s.Schema = "JDTEST"
	}
	programCase{
		driver: DriverOracle, db: db, schema: "JDTEST", table: t1, log: lg,
		setup: []string{"CREATE TABLE " + t1 + ` ("ID" NUMBER)`, "CREATE TABLE " + lg + ` ("ID" NUMBER)`},
		cleanup: []string{
			"DROP TABLE " + t1 + " PURGE", "DROP TABLE " + lg + " PURGE",
			`DROP FUNCTION "JDT_ROUTINE_EDITOR_ADD"`, `DROP PROCEDURE "JDT_ROUTINE_EDITOR_PUT"`,
		},
		fn: fn, fnEdited: edited, fnBroken: broken,
		call: `SELECT "JDT_ROUTINE_EDITOR_ADD"(1, 2) FROM dual`, want: 3, wantEdited: 4,
		proc: proc, procEdited: procEdited, callProc: `BEGIN "JDT_ROUTINE_EDITOR_PUT"(5); END;`,
		trig: withSchema(trig(10), "JDTEST"), trigEdited: withSchema(trig(100), "JDTEST"), trigBroken: withSchema(trigBroken, "JDTEST"),
		insert: func(id int) string { return "INSERT INTO " + t1 + ` ("ID") VALUES (` + itoa(id) + ")" },
	}.run(t)
}

func withSchema(t TriggerSpec, schema string) TriggerSpec {
	t.Schema = schema
	return t
}

func TestLiveSQLiteStoredPrograms(t *testing.T) {
	d, _ := DialectFor(DriverSQLite)
	db, err := sql.Open(d.SQLDriverName(), d.NormaliseDSN(filepath.Join(t.TempDir(), "programs.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	trig := func(times int) TriggerSpec {
		return TriggerSpec{Name: "jdt_routine_editor_trg", Table: "jdt_routine_editor_t", Timing: "after",
			Events: []string{"insert"}, ForEach: "row",
			Body: "INSERT INTO jdt_routine_editor_log(id) VALUES (new.id * " + itoa(times) + ")"}
	}
	trigBroken := trig(100)
	trigBroken.Body = "INSERT INTO"
	programCase{
		driver: DriverSQLite, db: db, schema: "", table: "jdt_routine_editor_t", log: "jdt_routine_editor_log",
		setup: []string{"CREATE TABLE jdt_routine_editor_t (id integer)", "CREATE TABLE jdt_routine_editor_log (id integer)"},
		trig:  trig(10), trigEdited: trig(100), trigBroken: trigBroken,
		insert: func(id int) string { return "INSERT INTO jdt_routine_editor_t (id) VALUES (" + itoa(id) + ")" },
	}.run(t)
}

func TestLivePostgresKeepsTheOwnerAndPrivilegesWhenItDropsAndCreates(t *testing.T) {
	db := catalogLive(t, DriverPostgres, "JD_TEST_POSTGRES_DSN")
	ctx := context.Background()
	const role = "jdt_routine_editor_r"
	clean := func() {
		_, _ = db.ExecContext(ctx, "DROP FUNCTION IF EXISTS jdt_routine_editor_acl(integer)")
		_, _ = db.ExecContext(ctx, "DROP FUNCTION IF EXISTS jdt_routine_editor_acl(bigint)")
		_, _ = db.ExecContext(ctx, "DROP ROLE IF EXISTS "+role)
	}
	clean()
	t.Cleanup(clean)
	for _, stmt := range []string{
		"CREATE ROLE " + role + " NOLOGIN",
		"GRANT " + role + " TO CURRENT_USER",
		"CREATE FUNCTION jdt_routine_editor_acl(a integer) RETURNS integer LANGUAGE sql SECURITY DEFINER AS 'SELECT a'",
		"ALTER FUNCTION jdt_routine_editor_acl(integer) OWNER TO " + role,
		"REVOKE ALL ON FUNCTION jdt_routine_editor_acl(integer) FROM PUBLIC",
		"GRANT EXECUTE ON FUNCTION jdt_routine_editor_acl(integer) TO CURRENT_USER WITH GRANT OPTION",
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Skipf("this login cannot set up the case (%v): %s", err, stmt)
		}
	}
	rights := func() (owner, acl string, definer bool) {
		t.Helper()
		if err := db.QueryRowContext(ctx, `
		  SELECT pg_get_userbyid(p.proowner), COALESCE(p.proacl::text, ''), p.prosecdef
		  FROM pg_proc p WHERE p.proname = 'jdt_routine_editor_acl'`).Scan(&owner, &acl, &definer); err != nil {
			t.Fatal(err)
		}
		return
	}
	owner, before, _ := rights()
	spec := RoutineSpec{Kind: KindFunction, Name: "jdt_routine_editor_acl", Language: "sql", Returns: "bigint",
		Security: "definer", Args: []RoutineArg{{Name: "a", Mode: "in", Type: "bigint"}}, Body: "SELECT a"}
	plan, err := PlanSaveRoutine(ctx, db, DriverPostgres, spec, &RoutineRef{Schema: "public", Name: spec.Name, Signature: "a integer"})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.atomic || len(plan.Statements) < 4 || !strings.Contains(plan.Note, "owner and privileges") {
		t.Fatalf("plan = %v (atomic %v)\n%s", plan.Statements, plan.atomic, plan.Note)
	}
	if err := plan.Exec(ctx, db); err != nil {
		t.Fatalf("%v\n%s", err, plan)
	}
	if got, _, _ := rights(); got != role || got != owner {
		t.Errorf("owner = %q after the edit, was %q", got, owner)
	}
	if _, after, definer := rights(); !definer || !sameACL(after, before) {
		t.Errorf("privileges after the edit: %q, before: %q (definer %v)", after, before, definer)
	}
	var executable bool
	if err := db.QueryRowContext(ctx, `SELECT has_function_privilege('public', 'jdt_routine_editor_acl(bigint)', 'EXECUTE')`).Scan(&executable); err != nil || executable {
		t.Errorf("PUBLIC may execute the function again after the edit: %v, %v", executable, err)
	}
	if got := plan.Signature(ctx, db); got != "a bigint" {
		t.Errorf("signature = %q", got)
	}
}

func TestLiveMySQLKeepsTheSettingsAndTheOrderItWasMadeUnder(t *testing.T) {
	for _, c := range []struct{ name, admin string }{{"mysql8", "JD_TEST_MYSQL8_ADMIN_DSN"}, {"mariadb", "JD_TEST_MYSQL_ADMIN_DSN"}} {
		t.Run(c.name, func(t *testing.T) {
			admin := catalogLive(t, DriverMySQL, c.admin)
			const database = "jdt_routine_editor_settings"
			if _, err := admin.Exec("DROP DATABASE IF EXISTS " + database); err != nil {
				t.Fatal(err)
			}
			if _, err := admin.Exec("CREATE DATABASE " + database); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE IF EXISTS " + database) })
			d, _ := DialectFor(DriverMySQL)
			db, err := sql.Open("mysql", d.NormaliseDSN(strings.TrimSuffix(os.Getenv(c.admin), "/")+"/"+database))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			ctx := context.Background()
			conn, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, stmt := range []string{
				"CREATE TABLE t (id int)", "CREATE TABLE log (id int)",
				"SET SESSION sql_mode = CONCAT(@@SESSION.sql_mode, ',PIPES_AS_CONCAT')",
				"CREATE FUNCTION quoted(a int) RETURNS int DETERMINISTIC NO SQL RETURN a + 1",
				"SET SESSION sql_mode = DEFAULT",
				"CREATE TRIGGER first_one AFTER INSERT ON t FOR EACH ROW INSERT INTO log(id) VALUES (1)",
				"CREATE TRIGGER second_one AFTER INSERT ON t FOR EACH ROW INSERT INTO log(id) VALUES (2)",
				"CREATE TRIGGER lonely BEFORE DELETE ON t FOR EACH ROW SET @x = 1",
			} {
				if _, err := conn.ExecContext(ctx, stmt); err != nil {
					t.Fatalf("%v\n%s", err, stmt)
				}
			}
			conn.Close()

			// A routine made under another sql_mode is saved under it.
			spec := RoutineSpec{Kind: KindFunction, Name: "quoted", Returns: "int", Deterministic: true, DataAccess: "no sql",
				Args: []RoutineArg{{Name: "a", Mode: "in", Type: "int"}}, Body: "RETURN a + 2"}
			plan, err := PlanSaveRoutine(ctx, db, DriverMySQL, spec, &RoutineRef{Schema: database, Name: "quoted"})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(plan.Statements[0], "SET SESSION sql_mode = '") || !plan.discard {
				t.Fatalf("the plan does not restate the sql_mode: %v", plan.Statements)
			}
			if err := plan.Exec(ctx, db); err != nil {
				t.Fatalf("%v\n%s", err, plan)
			}
			var mode string
			if err := db.QueryRow("SELECT SQL_MODE FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = ? AND ROUTINE_NAME = 'quoted'", database).Scan(&mode); err != nil ||
				!strings.Contains(mode, "PIPES_AS_CONCAT") {
				t.Errorf("sql_mode after the edit = %q, %v", mode, err)
			}
			var here string
			if err := db.QueryRow("SELECT @@SESSION.sql_mode").Scan(&here); err != nil || strings.Contains(here, "PIPES_AS_CONCAT") {
				t.Errorf("the changed connection went back to the pool: %q, %v", here, err)
			}

			// Two triggers on one event: saving either would change which runs first.
			for _, name := range []string{"first_one", "second_one"} {
				p, err := ReadProgram(ctx, db, DriverMySQL, ObjectRef{Kind: KindTrigger, Schema: database, Name: name})
				if err != nil {
					t.Fatal(err)
				}
				if p.Editable || !strings.Contains(p.Reason, "which runs first") {
					t.Errorf("%s: editable %v, reason %q", name, p.Editable, p.Reason)
				}
			}
			if p, err := ReadProgram(ctx, db, DriverMySQL, ObjectRef{Kind: KindTrigger, Schema: database, Name: "lonely"}); err != nil || !p.Editable {
				t.Errorf("a trigger alone on its event is not editable: %v %+v", err, p)
			}
		})
	}
}

func TestLiveSQLServerKeepsTheSettingsAndTheOrderItWasMadeUnder(t *testing.T) {
	db := catalogLive(t, DriverMSSQL, "JD_TEST_MSSQL_DSN")
	ctx := context.Background()
	clean := func() {
		for _, s := range []string{
			"DROP TRIGGER IF EXISTS dbo.jdt_routine_editor_first", "DROP TRIGGER IF EXISTS dbo.jdt_routine_editor_off",
			"DROP PROCEDURE IF EXISTS dbo.jdt_routine_editor_q", "DROP TABLE IF EXISTS dbo.jdt_routine_editor_o",
		} {
			_, _ = db.ExecContext(ctx, s)
		}
	}
	clean()
	t.Cleanup(clean)
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"SET QUOTED_IDENTIFIER OFF",
		"CREATE PROCEDURE dbo.jdt_routine_editor_q AS SELECT \"a\"",
		"SET QUOTED_IDENTIFIER ON",
		"CREATE TABLE dbo.jdt_routine_editor_o (id int)",
	} {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%v\n%s", err, stmt)
		}
	}
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	conn.Close()
	for _, stmt := range []string{
		"CREATE TRIGGER dbo.jdt_routine_editor_first ON dbo.jdt_routine_editor_o AFTER INSERT AS SELECT 1",
		"EXEC sp_settriggerorder @triggername = 'dbo.jdt_routine_editor_first', @order = 'First', @stmttype = 'INSERT'",
		"CREATE TRIGGER dbo.jdt_routine_editor_off ON dbo.jdt_routine_editor_o AFTER DELETE AS SELECT 1",
		"DISABLE TRIGGER dbo.jdt_routine_editor_off ON dbo.jdt_routine_editor_o",
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%v\n%s", err, stmt)
		}
	}

	spec := RoutineSpec{Kind: KindProcedure, Schema: "dbo", Name: "jdt_routine_editor_q", Body: "SELECT 'b'"}
	plan, err := PlanSaveRoutine(ctx, db, DriverMSSQL, spec, &RoutineRef{Schema: "dbo", Name: spec.Name})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Statements[0] != "SET QUOTED_IDENTIFIER OFF" || !plan.discard {
		t.Fatalf("the plan does not restate the settings: %v", plan.Statements)
	}
	if err := plan.Exec(ctx, db); err != nil {
		t.Fatalf("%v\n%s", err, plan)
	}
	var quoted bool
	if err := db.QueryRowContext(ctx, `SELECT uses_quoted_identifier FROM sys.sql_modules WHERE object_id = OBJECT_ID('dbo.jdt_routine_editor_q')`).Scan(&quoted); err != nil || quoted {
		t.Errorf("QUOTED_IDENTIFIER after the edit = %v, %v", quoted, err)
	}

	for name, want := range map[string]string{"jdt_routine_editor_first": "sp_settriggerorder", "jdt_routine_editor_off": "disabled"} {
		p, err := ReadProgram(ctx, db, DriverMSSQL, ObjectRef{Kind: KindTrigger, Schema: "dbo", Name: name})
		if err != nil {
			t.Fatal(err)
		}
		if p.Editable || !strings.Contains(p.Reason, want) {
			t.Errorf("%s: editable %v, reason %q", name, p.Editable, p.Reason)
		}
	}
}

func TestLiveOracleRefusesATriggerWhoseHeaderTheFormCannotState(t *testing.T) {
	db := catalogLive(t, DriverOracle, "JD_TEST_ORACLE_DSN")
	ctx := context.Background()
	clean := func() {
		for _, s := range []string{
			`DROP TRIGGER "JDT_ROUTINE_EDITOR_B"`, `DROP TRIGGER "JDT_ROUTINE_EDITOR_A"`, `DROP TRIGGER "JDT_ROUTINE_EDITOR_U"`,
			`DROP TABLE "JDT_ROUTINE_EDITOR_X" PURGE`,
		} {
			_, _ = db.ExecContext(ctx, s)
		}
	}
	clean()
	t.Cleanup(clean)
	for _, stmt := range []string{
		`CREATE TABLE "JDT_ROUTINE_EDITOR_X" ("ID" NUMBER)`,
		`CREATE TRIGGER "JDT_ROUTINE_EDITOR_A" AFTER INSERT ON "JDT_ROUTINE_EDITOR_X" FOR EACH ROW BEGIN NULL; END;`,
		`CREATE TRIGGER "JDT_ROUTINE_EDITOR_B" AFTER INSERT ON "JDT_ROUTINE_EDITOR_X" FOR EACH ROW FOLLOWS "JDT_ROUTINE_EDITOR_A" BEGIN NULL; END;`,
		`CREATE TRIGGER "JDT_ROUTINE_EDITOR_U" BEFORE UPDATE OF "ID" ON "JDT_ROUTINE_EDITOR_X" FOR EACH ROW BEGIN NULL; END;`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%v\n%s", err, stmt)
		}
	}
	for name, want := range map[string]string{"JDT_ROUTINE_EDITOR_B": "FOLLOWS", "JDT_ROUTINE_EDITOR_U": "UPDATE OF"} {
		p, err := ReadProgram(ctx, db, DriverOracle, ObjectRef{Kind: KindTrigger, Schema: "JDTEST", Name: name})
		if err != nil {
			t.Fatal(err)
		}
		if p.Editable || !strings.Contains(strings.ToUpper(p.Reason), want) {
			t.Errorf("%s: editable %v, reason %q", name, p.Editable, p.Reason)
		}
	}
	if p, err := ReadProgram(ctx, db, DriverOracle, ObjectRef{Kind: KindTrigger, Schema: "JDTEST", Name: "JDT_ROUTINE_EDITOR_A"}); err != nil || !p.Editable {
		t.Errorf("a plain trigger is not editable: %v %+v", err, p)
	}
}

// sameACL compares two aclitem arrays as sets: their order is not meaningful.
func sameACL(a, b string) bool {
	items := func(text string) []string {
		out := strings.Split(strings.Trim(text, "{}"), ",")
		slices.Sort(out)
		return out
	}
	return slices.Equal(items(a), items(b))
}
