package dbx

import (
	"strings"
	"testing"
)

func mustRender(t *testing.T, driver Driver, spec RoutineSpec, create string) string {
	t.Helper()
	got, err := renderRoutine(driver, spec, create)
	if err != nil {
		t.Fatalf("%s: %v", driver, err)
	}
	return got
}

func TestRenderPostgresFunction(t *testing.T) {
	got := mustRender(t, DriverPostgres, RoutineSpec{
		Kind: KindFunction, Schema: "shop", Name: "add_tax", Language: "sql", Volatility: "immutable", Strict: true,
		Security: "definer", Returns: "numeric",
		Args: []RoutineArg{{Name: "price", Type: "numeric"}, {Name: "rate", Mode: "in", Type: "numeric", Default: "0.2"}},
		Body: "SELECT price * (1 + rate)",
	}, "CREATE OR REPLACE")
	want := `CREATE OR REPLACE FUNCTION "shop"."add_tax"(price numeric, rate numeric DEFAULT 0.2)
RETURNS numeric
LANGUAGE sql
IMMUTABLE
STRICT
SECURITY DEFINER
AS $jd$SELECT price * (1 + rate)$jd$`
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestPostgresBodyKeepsItsQuotingApart(t *testing.T) {
	body := "SELECT '$jd$ and $jd1$' AS x"
	got := mustRender(t, DriverPostgres, RoutineSpec{
		Kind: KindProcedure, Name: "p", Language: "sql", Body: body,
	}, "CREATE")
	if !strings.Contains(got, "AS $jd2$"+body+"$jd2$") {
		t.Errorf("the tag did not move clear of the body:\n%s", got)
	}
	// A body that ends in the tag's opening still closes where it should.
	got = mustRender(t, DriverPostgres, RoutineSpec{Kind: KindProcedure, Name: "p", Language: "sql", Body: "SELECT 1 $jd"}, "CREATE")
	if !strings.Contains(got, "$jd1$SELECT 1 $jd$jd1$") {
		t.Errorf("a body ending in $jd: %s", got)
	}
}

func TestPostgresOutParametersDoNotNeedAReturnType(t *testing.T) {
	got := mustRender(t, DriverPostgres, RoutineSpec{
		Kind: KindFunction, Name: "split", Language: "sql",
		Args: []RoutineArg{{Name: "s", Type: "text"}, {Name: "head", Mode: "out", Type: "text"}},
		Body: "SELECT left(s, 1)",
	}, "CREATE")
	if strings.Contains(got, "RETURNS") || !strings.Contains(got, "(s text, OUT head text)") {
		t.Errorf("got %s", got)
	}
}

func TestPostgresReturnTypes(t *testing.T) {
	for _, ok := range []string{"integer", "SETOF text", "setof public.thing", "TABLE(a integer, b text)", "trigger", "void", "text[]"} {
		d, _ := DialectFor(DriverPostgres)
		if _, err := returnsClause(DriverPostgres, d, ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "integer; DROP TABLE x", "TABLE()", "TABLE(a)", "setof", "int)) AS $$ x $$ --"} {
		d, _ := DialectFor(DriverPostgres)
		if _, err := returnsClause(DriverPostgres, d, bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestRoutineHeadersAreHeldToWhatTheyAre(t *testing.T) {
	cases := []struct {
		name   string
		driver Driver
		spec   RoutineSpec
		want   string
	}{
		{"a type that continues the statement", DriverPostgres, RoutineSpec{Kind: KindFunction, Name: "f", Language: "sql", Returns: "int", Body: "SELECT 1",
			Args: []RoutineArg{{Name: "a", Type: "integer) AS $$ select 1 $$; DROP TABLE t; --"}}}, "can build"},
		{"a default that closes the list", DriverPostgres, RoutineSpec{Kind: KindFunction, Name: "f", Language: "sql", Returns: "int", Body: "SELECT 1",
			Args: []RoutineArg{{Name: "a", Type: "integer", Default: "1) AS $$ x"}}}, "closes a parenthesis"},
		{"a language that is a clause", DriverPostgres, RoutineSpec{Kind: KindFunction, Name: "f", Language: "sql SECURITY DEFINER", Returns: "int", Body: "SELECT 1"}, "not a language"},
		{"an empty body", DriverPostgres, RoutineSpec{Kind: KindFunction, Name: "f", Language: "sql", Returns: "int", Body: "  "}, "body is required"},
		{"a function with no return type", DriverMySQL, RoutineSpec{Kind: KindFunction, Name: "f", Body: "RETURN 1"}, "return type is required"},
		{"a procedure that returns", DriverMySQL, RoutineSpec{Kind: KindProcedure, Name: "p", Returns: "int", Body: "SELECT 1"}, "returns nothing"},
		{"an out parameter on a MySQL function", DriverMySQL, RoutineSpec{Kind: KindFunction, Name: "f", Returns: "int", Body: "RETURN 1",
			Args: []RoutineArg{{Name: "a", Mode: "out", Type: "int"}}}, "input parameters only"},
		{"a default on MySQL", DriverMySQL, RoutineSpec{Kind: KindProcedure, Name: "p", Body: "SELECT 1",
			Args: []RoutineArg{{Name: "a", Type: "int", Default: "1"}}}, "no default"},
		{"a bad data access", DriverMySQL, RoutineSpec{Kind: KindProcedure, Name: "p", Body: "SELECT 1", DataAccess: "reads sql data; drop"}, "data access"},
		{"a parameter with no name on SQL Server", DriverMSSQL, RoutineSpec{Kind: KindProcedure, Name: "p", Body: "SELECT 1",
			Args: []RoutineArg{{Type: "int"}}}, "needs a name"},
		{"a parameter name that is a statement", DriverMSSQL, RoutineSpec{Kind: KindProcedure, Name: "p", Body: "SELECT 1",
			Args: []RoutineArg{{Name: "a int; DROP TABLE t", Type: "int"}}}, "letters, digits"},
		{"a deterministic procedure", DriverOracle, RoutineSpec{Kind: KindProcedure, Name: "p", Deterministic: true, Body: "BEGIN NULL; END;"}, "only a function"},
		{"SQLite functions", DriverSQLite, RoutineSpec{Kind: KindFunction, Name: "f", Body: "x"}, "SQLite has no stored functions"},
		{"ClickHouse procedures", DriverClickHouse, RoutineSpec{Kind: KindProcedure, Name: "p", Body: "x"}, "no stored procedures"},
		{"a kind that is not a routine", DriverPostgres, RoutineSpec{Kind: "trigger", Name: "p", Body: "x"}, "function or a procedure"},
		{"a bad security", DriverPostgres, RoutineSpec{Kind: KindProcedure, Name: "p", Language: "sql", Security: "owner", Body: "x"}, "definer or invoker"},
	}
	for _, c := range cases {
		_, err := renderRoutine(c.driver, c.spec, "CREATE")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to mention %q", c.name, err, c.want)
		}
	}
}

func TestRenderRoutinesPerEngine(t *testing.T) {
	cases := []struct {
		name   string
		driver Driver
		spec   RoutineSpec
		create string
		want   string
	}{
		{"MySQL function", DriverMySQL, RoutineSpec{Kind: KindFunction, Schema: "shop", Name: "add_one", Returns: "int",
			Deterministic: true, DataAccess: "no sql", Security: "invoker", Comment: "it's one",
			Args: []RoutineArg{{Name: "n", Type: "int"}}, Body: "RETURN n + 1;"}, "CREATE",
			"CREATE FUNCTION `shop`.`add_one`(`n` int) RETURNS int\nDETERMINISTIC\nNO SQL\nSQL SECURITY INVOKER\nCOMMENT 'it''s one'\nRETURN n + 1"},
		{"MySQL procedure", DriverMySQL, RoutineSpec{Kind: KindProcedure, Name: "bump",
			Args: []RoutineArg{{Name: "n", Type: "int"}, {Name: "total", Mode: "out", Type: "bigint"}},
			Body: "BEGIN\n  SET total = n;\nEND"}, "CREATE OR REPLACE",
			"CREATE OR REPLACE PROCEDURE `bump`(IN `n` int, OUT `total` bigint)\nBEGIN\n  SET total = n;\nEND"},
		{"SQL Server procedure", DriverMSSQL, RoutineSpec{Kind: KindProcedure, Schema: "dbo", Name: "bump", Security: "definer",
			Args: []RoutineArg{{Name: "n", Type: "int", Default: "1"}, {Name: "total", Mode: "out", Type: "int"}},
			Body: "SET @total = @n;"}, "CREATE OR ALTER",
			"CREATE OR ALTER PROCEDURE [dbo].[bump] @n int = 1, @total int OUTPUT\nWITH EXECUTE AS OWNER\nAS\nSET @total = @n"},
		{"SQL Server function", DriverMSSQL, RoutineSpec{Kind: KindFunction, Schema: "dbo", Name: "add_one", Returns: "int",
			Args: []RoutineArg{{Name: "n", Type: "int"}}, Body: "BEGIN RETURN @n + 1 END"}, "CREATE",
			"CREATE FUNCTION [dbo].[add_one](@n int)\nRETURNS int\nAS\nBEGIN RETURN @n + 1 END"},
		{"SQL Server inline table function", DriverMSSQL, RoutineSpec{Kind: KindFunction, Schema: "dbo", Name: "recent", Returns: "table",
			Body: "RETURN (SELECT 1 AS one)"}, "CREATE",
			"CREATE FUNCTION [dbo].[recent]()\nRETURNS TABLE\nAS\nRETURN (SELECT 1 AS one)"},
		{"Oracle function", DriverOracle, RoutineSpec{Kind: KindFunction, Schema: "APP", Name: "ADD_ONE", Returns: "NUMBER",
			Deterministic: true, Security: "invoker", Args: []RoutineArg{{Name: "n", Type: "NUMBER", Default: "1"}, {Name: "m", Mode: "inout", Type: "NUMBER"}},
			Body: "BEGIN RETURN n + 1; END;"}, "CREATE OR REPLACE",
			"CREATE OR REPLACE FUNCTION \"APP\".\"ADD_ONE\"(n IN NUMBER DEFAULT 1, m IN OUT NUMBER) RETURN NUMBER\nAUTHID CURRENT_USER\nDETERMINISTIC\nIS\nBEGIN RETURN n + 1; END;"},
		{"Oracle procedure with no parameters", DriverOracle, RoutineSpec{Kind: KindProcedure, Name: "NOOP", Body: "BEGIN NULL; END;"}, "CREATE",
			"CREATE PROCEDURE \"NOOP\"\nIS\nBEGIN NULL; END;"},
	}
	for _, c := range cases {
		if got := mustRender(t, c.driver, c.spec, c.create); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestRenderTriggers(t *testing.T) {
	cases := []struct {
		name   string
		driver Driver
		spec   TriggerSpec
		create string
		want   string
	}{
		{"Postgres", DriverPostgres, TriggerSpec{Schema: "shop", Name: "stamp", Table: "orders", Timing: "before",
			Events: []string{"update", "insert"}, ForEach: "row", Condition: "old.total IS DISTINCT FROM new.total",
			FunctionSchema: "shop", Function: "touch", FunctionArgs: []string{"updated_at", "it's"}}, "CREATE",
			"CREATE TRIGGER \"stamp\"\nBEFORE INSERT OR UPDATE ON \"shop\".\"orders\"\nFOR EACH ROW\nWHEN (old.total IS DISTINCT FROM new.total)\nEXECUTE FUNCTION \"shop\".\"touch\"('updated_at', 'it''s')"},
		{"MySQL", DriverMySQL, TriggerSpec{Schema: "shop", Name: "stamp", Table: "orders", Timing: "after",
			Events: []string{"insert"}, ForEach: "row", Body: "BEGIN\n  SET @n = 1;\nEND;"}, "CREATE",
			"CREATE TRIGGER `shop`.`stamp` AFTER INSERT ON `shop`.`orders`\nFOR EACH ROW\nBEGIN\n  SET @n = 1;\nEND"},
		{"SQL Server", DriverMSSQL, TriggerSpec{Schema: "dbo", Name: "stamp", Table: "orders", Timing: "after",
			Events: []string{"delete", "insert"}, ForEach: "statement", Body: "SELECT 1"}, "CREATE OR ALTER",
			"CREATE OR ALTER TRIGGER [dbo].[stamp] ON [dbo].[orders]\nAFTER INSERT, DELETE\nAS\nSELECT 1"},
		{"Oracle", DriverOracle, TriggerSpec{Schema: "APP", Name: "STAMP", Table: "ORDERS", Timing: "before",
			Events: []string{"insert", "update"}, ForEach: "row", Condition: "new.total > 0", Body: "BEGIN NULL; END;"}, "CREATE OR REPLACE",
			"CREATE OR REPLACE TRIGGER \"APP\".\"STAMP\"\nBEFORE INSERT OR UPDATE ON \"APP\".\"ORDERS\"\nFOR EACH ROW\nWHEN (new.total > 0)\nBEGIN NULL; END;"},
		{"SQLite", DriverSQLite, TriggerSpec{Name: "stamp", Table: "orders", Timing: "after", Events: []string{"update"},
			UpdateOf: []string{"total"}, ForEach: "row", Condition: "new.total > 0", Body: "UPDATE orders SET note = 'x' WHERE id = new.id"}, "CREATE",
			"CREATE TRIGGER \"stamp\"\nAFTER UPDATE OF \"total\" ON \"orders\"\nFOR EACH ROW\nWHEN new.total > 0\nBEGIN\nUPDATE orders SET note = 'x' WHERE id = new.id;\nEND"},
	}
	for _, c := range cases {
		got, err := renderTrigger(c.driver, c.spec, c.create)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestTriggersAreHeldToWhatTheEngineHas(t *testing.T) {
	base := TriggerSpec{Schema: "s", Name: "t", Table: "tbl", Timing: "after", Events: []string{"insert"}, ForEach: "row", Body: "SELECT 1", Function: "f"}
	with := func(f func(*TriggerSpec)) TriggerSpec {
		s := base
		s.Events = append([]string{}, s.Events...)
		f(&s)
		return s
	}
	cases := []struct {
		name   string
		driver Driver
		spec   TriggerSpec
		want   string
	}{
		{"no event", DriverPostgres, with(func(s *TriggerSpec) { s.Events = nil }), "at least one event"},
		{"two MySQL events", DriverMySQL, with(func(s *TriggerSpec) { s.Events = []string{"insert", "update"} }), "one event"},
		{"truncate on Oracle", DriverOracle, with(func(s *TriggerSpec) { s.Events = []string{"truncate"} }), "TRUNCATE"},
		{"before on SQL Server", DriverMSSQL, with(func(s *TriggerSpec) { s.Timing = "before"; s.ForEach = "statement" }), "no BEFORE"},
		{"row on SQL Server", DriverMSSQL, with(func(s *TriggerSpec) {}), "once for each statement"},
		{"instead of on MySQL", DriverMySQL, with(func(s *TriggerSpec) { s.Timing = "instead of" }), "no INSTEAD OF"},
		{"statement on SQLite", DriverSQLite, with(func(s *TriggerSpec) { s.Schema = ""; s.ForEach = "statement" }), "for each row"},
		{"condition on MySQL", DriverMySQL, with(func(s *TriggerSpec) { s.Condition = "new.a > 1" }), "no WHEN"},
		{"a condition that ends the clause", DriverPostgres, with(func(s *TriggerSpec) { s.Condition = "true) EXECUTE FUNCTION evil() --" }), "closes a parenthesis"},
		{"a timing that is a clause", DriverPostgres, with(func(s *TriggerSpec) { s.Timing = "after delete" }), "timing"},
		{"UPDATE OF on Postgres", DriverPostgres, with(func(s *TriggerSpec) { s.Events = []string{"update"}; s.UpdateOf = []string{"a"} }), "SQLite's"},
		{"no function on Postgres", DriverPostgres, with(func(s *TriggerSpec) { s.Function = "" }), "choose the function"},
		{"ClickHouse", DriverClickHouse, base, "no triggers"},
		{"empty body on Oracle", DriverOracle, with(func(s *TriggerSpec) { s.Body = "" }), "body is required"},
	}
	for _, c := range cases {
		_, err := renderTrigger(c.driver, c.spec, "CREATE")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to mention %q", c.name, err, c.want)
		}
	}
}

func TestSQLiteTriggersStayInMain(t *testing.T) {
	_, err := renderTrigger(DriverSQLite, TriggerSpec{Schema: "other", Name: "t", Table: "tbl", Timing: "after",
		Events: []string{"insert"}, ForEach: "row", Body: "SELECT 1"}, "CREATE")
	if err == nil || !strings.Contains(err.Error(), "attached") {
		t.Errorf("a trigger aimed at an attached file: %v", err)
	}
}

func TestStoredProgramsPerEngineAndFlavour(t *testing.T) {
	has := func(driver Driver, flavor, op string) bool {
		for _, got := range DDLOperations(driver, flavor) {
			if got == op {
				return true
			}
		}
		return false
	}
	for _, c := range []struct {
		driver  Driver
		flavor  string
		ops     []string
		missing []string
	}{
		{DriverPostgres, "", []string{OpFunctions, OpProcedures, OpTriggers}, nil},
		{DriverPostgres, FlavorTimescaleDB, []string{OpFunctions, OpProcedures, OpTriggers}, nil},
		{DriverPostgres, FlavorCockroachDB, nil, []string{OpFunctions, OpProcedures, OpTriggers}},
		{DriverMySQL, FlavorMySQL, []string{OpFunctions, OpProcedures, OpTriggers}, nil},
		{DriverMySQL, FlavorMariaDB, []string{OpFunctions, OpProcedures, OpTriggers}, nil},
		{DriverMySQL, FlavorTiDB, nil, []string{OpFunctions, OpProcedures, OpTriggers}},
		{DriverMSSQL, "", []string{OpFunctions, OpProcedures, OpTriggers}, nil},
		{DriverOracle, "", []string{OpFunctions, OpProcedures, OpTriggers}, nil},
		{DriverSQLite, "", []string{OpTriggers}, []string{OpFunctions, OpProcedures}},
		{DriverClickHouse, "", nil, []string{OpFunctions, OpProcedures, OpTriggers}},
	} {
		for _, op := range c.ops {
			if !has(c.driver, c.flavor, op) {
				t.Errorf("%s/%s should offer %s", c.driver, c.flavor, op)
			}
		}
		for _, op := range c.missing {
			if has(c.driver, c.flavor, op) {
				t.Errorf("%s/%s should not offer %s", c.driver, c.flavor, op)
			}
		}
	}
}

func TestSQLServerModulesAreReadBackAsTheirFields(t *testing.T) {
	p := parseMSSQLModule("CREATE PROCEDURE [dbo].[bump] @n int = 1, @label varchar(10) = 'a, b' , @total AS int OUTPUT\n"+
		"WITH EXECUTE AS OWNER\nAS\nBEGIN\n  SET @total = @n;\nEND", KindProcedure, "dbo", "bump", "", "")
	if p.Reason != "" || p.Routine == nil {
		t.Fatalf("not read: %+v", p)
	}
	r := p.Routine
	if len(r.Args) != 3 || r.Args[0] != (RoutineArg{Name: "n", Mode: "in", Type: "int", Default: "1"}) ||
		r.Args[1] != (RoutineArg{Name: "label", Mode: "in", Type: "varchar(10)", Default: "'a, b'"}) ||
		r.Args[2] != (RoutineArg{Name: "total", Mode: "out", Type: "int"}) {
		t.Errorf("args = %+v", r.Args)
	}
	if r.Security != "definer" || r.Body != "BEGIN\n  SET @total = @n;\nEND" {
		t.Errorf("security %q, body %q", r.Security, r.Body)
	}

	f := parseMSSQLModule("CREATE OR ALTER FUNCTION dbo.add_one (@n int)\nRETURNS int\nAS\nBEGIN\nRETURN @n + 1\nEND", KindFunction, "dbo", "add_one", "", "")
	if f.Reason != "" || f.Routine.Returns != "int" || len(f.Routine.Args) != 1 || f.Routine.Body != "BEGIN\nRETURN @n + 1\nEND" {
		t.Errorf("function: %+v %+v", f, f.Routine)
	}

	for text, reason := range map[string]string{
		"CREATE PROCEDURE p WITH ENCRYPTION AS SELECT 1":                   "WITH ENCRYPTION",
		"CREATE PROCEDURE p @a int READONLY AS SELECT 1":                   "",
		"CREATE FUNCTION f() RETURNS @t TABLE (a int) AS BEGIN RETURN END": "",
		"CREATE PROCEDURE p WITH RECOMPILE, EXECUTE AS OWNER AS SELECT 1":  "WITH RECOMPILE, EXECUTE AS OWNER",
		"CREATE PROCEDURE p; 2 AS SELECT 1":                                "",
	} {
		kind := KindProcedure
		if strings.Contains(text, "FUNCTION") {
			kind = KindFunction
		}
		got := parseMSSQLModule(text, kind, "dbo", "p", "", "")
		if got.Reason == "" {
			// The header reads; the planner must still refuse what the form cannot write.
			if err := checkProgram(DriverMSSQL, got); err == nil && reason != "" {
				t.Errorf("%q was read as editable", text)
			}
			continue
		}
		if reason != "" && !strings.Contains(strings.ToUpper(got.Reason), reason) {
			t.Errorf("%q: reason %q, want %q", text, got.Reason, reason)
		}
	}
}

func TestSQLServerTriggersAreReadBack(t *testing.T) {
	p := parseMSSQLModule("CREATE TRIGGER [dbo].[stamp] ON [dbo].[orders]\nAFTER INSERT, UPDATE\nAS\nBEGIN SELECT 1 END", KindTrigger, "dbo", "stamp", "orders", "dbo")
	if p.Reason != "" || p.Trigger == nil {
		t.Fatalf("not read: %+v", p)
	}
	tr := p.Trigger
	if tr.Timing != "after" || strings.Join(tr.Events, ",") != "insert,update" || tr.Table != "orders" || tr.Body != "BEGIN SELECT 1 END" {
		t.Errorf("trigger = %+v", tr)
	}
	if got := parseMSSQLModule("CREATE TRIGGER t ON o AFTER INSERT NOT FOR REPLICATION AS SELECT 1", KindTrigger, "dbo", "t", "o", "dbo"); got.Reason == "" {
		t.Error("NOT FOR REPLICATION was dropped")
	}
	if got := parseMSSQLModule("CREATE TRIGGER t ON o WITH ENCRYPTION AFTER INSERT AS SELECT 1", KindTrigger, "dbo", "t", "o", "dbo"); got.Reason == "" {
		t.Error("WITH ENCRYPTION was dropped")
	}
}

func TestOracleSourceIsReadBackAsItsFields(t *testing.T) {
	p := parseOracleRoutine("FUNCTION \"ADD_ONE\" (n IN NUMBER DEFAULT 1, label VARCHAR2 := 'a,b', m IN OUT NUMBER)\nRETURN NUMBER\nAUTHID CURRENT_USER\nDETERMINISTIC\nIS\nBEGIN\n  RETURN n + 1;\nEND;\n",
		KindFunction, "APP", "ADD_ONE")
	if p.Reason != "" || p.Routine == nil {
		t.Fatalf("not read: %+v", p)
	}
	r := p.Routine
	if len(r.Args) != 3 || r.Args[0] != (RoutineArg{Name: "n", Mode: "in", Type: "NUMBER", Default: "1"}) ||
		r.Args[1] != (RoutineArg{Name: "label", Mode: "in", Type: "VARCHAR2", Default: "'a,b'"}) ||
		r.Args[2] != (RoutineArg{Name: "m", Mode: "inout", Type: "NUMBER"}) {
		t.Errorf("args = %+v", r.Args)
	}
	if r.Returns != "NUMBER" || r.Security != "invoker" || !r.Deterministic || r.Body != "BEGIN\n  RETURN n + 1;\nEND;" {
		t.Errorf("returns %q security %q det %v body %q", r.Returns, r.Security, r.Deterministic, r.Body)
	}

	proc := parseOracleRoutine("PROCEDURE noop IS BEGIN NULL; END;", KindProcedure, "APP", "NOOP")
	if proc.Reason != "" || len(proc.Routine.Args) != 0 || proc.Routine.Body != "BEGIN NULL; END;" {
		t.Errorf("procedure: %+v", proc)
	}
	for text, want := range map[string]string{
		"FUNCTION f RETURN NUMBER PIPELINED IS BEGIN NULL; END;":             "PIPELINED",
		"FUNCTION f RETURN NUMBER RESULT_CACHE IS BEGIN NULL; END;":          "RESULT_CACHE",
		"FUNCTION f (a OUT NOCOPY NUMBER) RETURN NUMBER IS BEGIN NULL; END;": "NOCOPY",
		"FUNCTION f (\"a\" IN NUMBER) RETURN NUMBER IS BEGIN NULL; END;":     "lower-case",
	} {
		got := parseOracleRoutine(text, KindFunction, "APP", "F")
		if !strings.Contains(strings.ToUpper(got.Reason), strings.ToUpper(want)) {
			t.Errorf("%q: reason %q, want %q", text, got.Reason, want)
		}
	}
}

func TestOracleTriggerBodyStartsAtTheBlock(t *testing.T) {
	spec := &TriggerSpec{Name: "T", Table: "O"}
	p := parseOracleTriggerBody("TRIGGER \"T\"\nBEFORE INSERT ON \"O\"\nFOR EACH ROW\nWHEN (new.a > begin_at)\nDECLARE x NUMBER;\nBEGIN\n  x := 1;\nEND;", spec)
	if p.Reason != "" || p.Trigger.Body != "DECLARE x NUMBER;\nBEGIN\n  x := 1;\nEND;" {
		t.Errorf("body = %q (%s)", p.Trigger.Body, p.Reason)
	}
}

func TestSQLiteTriggersAreReadBack(t *testing.T) {
	p := parseSQLiteTrigger("CREATE TRIGGER IF NOT EXISTS \"stamp\" AFTER UPDATE OF \"total\", note ON orders FOR EACH ROW WHEN new.total > 0\nBEGIN\n  UPDATE orders SET note = 'END'; \nEND", "main", "stamp", "orders")
	if p.Reason != "" || p.Trigger == nil {
		t.Fatalf("not read: %+v", p)
	}
	tr := p.Trigger
	if tr.Timing != "after" || len(tr.Events) != 1 || tr.Events[0] != "update" || strings.Join(tr.UpdateOf, ",") != "total,note" ||
		tr.Condition != "new.total > 0" || tr.Body != "UPDATE orders SET note = 'END';" {
		t.Errorf("trigger = %+v", tr)
	}
	if got := parseSQLiteTrigger("CREATE TEMP TRIGGER t AFTER INSERT ON o BEGIN SELECT 1; END", "main", "t", "o"); got.Reason == "" {
		t.Error("a temporary trigger was offered")
	}
}

func TestIndexTopWordIgnoresQuotesAndBrackets(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int
	}{
		{"a integer DEFAULT 5", 10},
		{"a text DEFAULT 'default'", 7},
		{"a text", -1},
		{"a text DEFAULT f(1, DEFAULT)", 7},
		{"defaulted integer", -1},
		{"a \"default\" integer", -1},
	} {
		if got := indexTopWord(c.in, "default"); got != c.want {
			t.Errorf("%q: %d, want %d", c.in, got, c.want)
		}
	}
}

func TestPostgresRoutinesAreReplacedInPlaceOnlyWhenTheirParametersAre(t *testing.T) {
	old := RoutineSpec{Kind: KindFunction, Name: "f", Returns: "integer",
		Args: []RoutineArg{{Name: "a", Mode: "in", Type: "integer"}, {Name: "b", Type: "text", Default: "'x'"}}}
	same := old
	same.Returns = " INTEGER "
	if !pgReplaceable(old, same) {
		t.Error("a respelled return type forced a drop")
	}
	for name, change := range map[string]func(*RoutineSpec){
		"return type": func(s *RoutineSpec) { s.Returns = "bigint" },
		"a type":      func(s *RoutineSpec) { s.Args = []RoutineArg{{Name: "a", Type: "bigint"}, old.Args[1]} },
		"a name":      func(s *RoutineSpec) { s.Args = []RoutineArg{{Name: "z", Type: "integer"}, old.Args[1]} },
		"a default":   func(s *RoutineSpec) { s.Args = []RoutineArg{old.Args[0], {Name: "b", Type: "text"}} },
		"a parameter": func(s *RoutineSpec) { s.Args = old.Args[:1] },
		"a mode":      func(s *RoutineSpec) { s.Args = []RoutineArg{{Name: "a", Mode: "inout", Type: "integer"}, old.Args[1]} },
	} {
		next := old
		change(&next)
		if pgReplaceable(old, next) {
			t.Errorf("a changed %s was replaced in place", name)
		}
	}
}

func TestDropStatements(t *testing.T) {
	for _, c := range []struct {
		driver Driver
		kind   string
		want   string
	}{
		{DriverMySQL, KindProcedure, "DROP PROCEDURE `s`.`n`"},
		{DriverMSSQL, KindFunction, "DROP FUNCTION [s].[n]"},
		{DriverOracle, KindFunction, `DROP FUNCTION "s"."n"`},
	} {
		got, err := dropRoutineStatement(c.driver, c.kind, "s", "n")
		if err != nil || got != c.want {
			t.Errorf("%s: %q, %v", c.driver, got, err)
		}
	}
	for _, c := range []struct {
		driver Driver
		want   string
	}{
		{DriverPostgres, `DROP TRIGGER "t" ON "s"."tbl"`},
		{DriverMySQL, "DROP TRIGGER `s`.`t`"},
		{DriverMSSQL, "DROP TRIGGER [s].[t]"},
		{DriverOracle, `DROP TRIGGER "s"."t"`},
		{DriverSQLite, `DROP TRIGGER "t"`},
	} {
		plan, err := PlanDropTrigger(c.driver, "s", "t", "tbl")
		if c.driver == DriverSQLite {
			plan, err = PlanDropTrigger(c.driver, "", "t", "tbl")
		}
		if err != nil || plan.String() != c.want {
			t.Errorf("%s: %v, %v", c.driver, plan, err)
		}
	}
	if _, err := PlanDropTrigger(DriverClickHouse, "", "t", "tbl"); err == nil {
		t.Error("ClickHouse trigger dropped")
	}
}
