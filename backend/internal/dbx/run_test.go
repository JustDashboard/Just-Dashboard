package dbx

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func mustStatement(t *testing.T, driver Driver, query string) *SQLStatement {
	t.Helper()
	st, err := SingleStatementFor(driver, query)
	if err != nil {
		t.Fatalf("SingleStatementFor(%q): %v", query, err)
	}
	return st
}

// misread is a statement the classifier is made to be wrong about: it writes,
// and is labelled a read. It stands for every way the classifier could be
// wrong that nobody has thought of yet.
func misread(query string) *SQLStatement {
	return &SQLStatement{SQL: query, Risk: Risk{Level: "read", Reasons: []string{}}}
}

// The classifier decides who may run a statement. The read-only scope decides
// what happens when the classifier is wrong.
func TestAStatementCalledAReadCannotWrite(t *testing.T) {
	db := changeDB(t)
	ctx := context.Background()
	for _, query := range []string{
		`INSERT INTO people(name) VALUES ('smuggled')`,
		`UPDATE people SET name = 'smuggled'`,
		`DELETE FROM people`,
		`CREATE TABLE smuggled (a)`,
		`DROP TABLE notes`,
	} {
		if _, err := RunStatement(ctx, db, DriverSQLite, misread(query), 10); err == nil {
			t.Errorf("%q ran inside the read-only scope", query)
		}
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM people WHERE name <> 'smuggled'`); n != 3 {
		t.Errorf("rows untouched = %d, want 3", n)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE name IN ('smuggled', 'notes')`); n != 1 {
		t.Error("a schema change got through the read-only scope")
	}
	// And the scope ends with the statement: the connection writes again.
	res, err := RunStatement(ctx, db, DriverSQLite, mustStatement(t, DriverSQLite,
		`INSERT INTO people(name) VALUES ('honest')`), 10)
	if err != nil {
		t.Fatalf("a write after a read was refused: %v", err)
	}
	if res.Affected != 1 {
		t.Errorf("affected = %d", res.Affected)
	}
}

func TestRunStatementReturnsTypedRows(t *testing.T) {
	db := changeDB(t)
	if _, err := db.Exec(`UPDATE people SET data = x'00ff', big = 9223372036854775807 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	res, err := RunStatement(context.Background(), db, DriverSQLite,
		mustStatement(t, DriverSQLite, `SELECT id, name, big, data FROM people ORDER BY id`), 2)
	if err != nil {
		t.Fatal(err)
	}
	if res.RowCount != 2 || !res.Truncated {
		t.Errorf("rows = %d truncated = %v, want 2 and a flag for the third", res.RowCount, res.Truncated)
	}
	if got := res.Rows[0]; got[0] != "1" || got[1] != "Ann" || got[2] != "9223372036854775807" || got[3] != `\x00ff` {
		t.Errorf("row = %#v", got)
	}
	if len(res.Kinds) != 4 || res.Kinds[0] != KindInteger || res.Kinds[1] != KindText || res.Kinds[3] != KindBinary {
		t.Errorf("kinds = %v", res.Kinds)
	}
	exact, err := RunStatement(context.Background(), db, DriverSQLite,
		mustStatement(t, DriverSQLite, `SELECT id FROM people ORDER BY id`), 3)
	if err != nil || exact.Truncated {
		t.Errorf("a result that fits was flagged truncated: %+v %v", exact, err)
	}
}

// Asking for more rows than the cap gets the cap, not the default.
func TestRowCapsClampRatherThanReset(t *testing.T) {
	for _, c := range []struct{ asked, def, max, want int }{
		{0, 500, 5000, 500}, {-3, 500, 5000, 500}, {10, 500, 5000, 10},
		{5000, 500, 5000, 5000}, {6000, 500, 5000, 5000}, {1 << 30, 100, 1000, 1000},
	} {
		if got := clampRows(c.asked, c.def, c.max); got != c.want {
			t.Errorf("clampRows(%d) = %d, want %d", c.asked, got, c.want)
		}
	}
}

// A statement that is not a read leaves nothing on the connection for the next
// request: the connection is closed, not pooled.
func TestWritesDoNotLeaveSessionStateBehind(t *testing.T) {
	db := changeDB(t)
	ctx := context.Background()
	if _, err := RunStatement(ctx, db, DriverSQLite,
		mustStatement(t, DriverSQLite, `CREATE TEMP TABLE scratch (a)`), 10); err != nil {
		t.Fatal(err)
	}
	if open := db.Stats().OpenConnections; open != 0 {
		t.Errorf("open connections after a write = %d, want the session closed", open)
	}
	if _, err := RunStatement(ctx, db, DriverSQLite,
		mustStatement(t, DriverSQLite, `SELECT * FROM scratch`), 10); err == nil {
		t.Error("a temporary table from one request was visible to the next")
	}
	// A read goes back to the pool.
	if _, err := RunStatement(ctx, db, DriverSQLite, mustStatement(t, DriverSQLite, `SELECT 1`), 10); err != nil {
		t.Fatal(err)
	}
	if open := db.Stats().OpenConnections; open != 1 {
		t.Errorf("open connections after a read = %d, want it pooled", open)
	}
}

func TestRunStatementStopsWhenItsContextEnds(t *testing.T) {
	db := changeDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := RunStatement(ctx, db, DriverSQLite, mustStatement(t, DriverSQLite,
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n) SELECT COUNT(*) FROM n`), 10)
	if err == nil {
		t.Fatal("an endless query returned")
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the query ran %s after its context ended", took)
	}
	// The pool is usable afterwards, and the read-only scope the cancelled
	// statement ran in did not outlive it.
	if n := rowCount(t, db, `SELECT COUNT(*) FROM people`); n != 3 {
		t.Errorf("rows = %d", n)
	}
	if _, err := RunStatement(context.Background(), db, DriverSQLite,
		mustStatement(t, DriverSQLite, `INSERT INTO people(name) VALUES ('after')`), 10); err != nil {
		t.Errorf("a write after a cancelled read was refused: %v", err)
	}
}

func parseScript(t *testing.T, driver Driver, script string) []SQLStatement {
	t.Helper()
	statements, err := ParseScript(driver, script)
	if err != nil {
		t.Fatalf("ParseScript: %v", err)
	}
	return statements
}

func TestRunScriptUsesOneSessionAndStopsAtTheFirstError(t *testing.T) {
	db := changeDB(t)
	ctx := context.Background()

	// A temporary table made on line one exists on line three only if all
	// three ran on the same connection.
	res, err := RunScript(ctx, db, DriverSQLite, parseScript(t, DriverSQLite, `
		CREATE TEMP TABLE scratch (a);
		INSERT INTO scratch VALUES (1), (2);
		SELECT COUNT(*) FROM scratch;
		INSERT INTO missing VALUES (1);
		INSERT INTO people(name) VALUES ('never');
		SELECT 2`), ScriptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 3 || res.Transaction != ScriptNone {
		t.Fatalf("failed = %d transaction = %s", res.Failed, res.Transaction)
	}
	var status []string
	for _, step := range res.Steps {
		status = append(status, step.Status)
	}
	if strings.Join(status, ",") != "ok,ok,ok,error,skipped,skipped" {
		t.Errorf("statuses = %v", status)
	}
	if got := res.Steps[2].Result; got == nil || got.Rows[0][0] != "2" {
		t.Errorf("the third statement did not see the first two: %+v", got)
	}
	if res.Steps[1].Result.Affected != 2 || res.Steps[3].Error == "" || res.Steps[3].Line != 5 {
		t.Errorf("steps = %+v", res.Steps)
	}
	if open := db.Stats().OpenConnections; open != 0 {
		t.Errorf("the script's connection was returned to the pool (%d open)", open)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM people WHERE name = 'never'`); n != 0 {
		t.Error("a statement after the failure ran")
	}
}

func TestRunScriptInOneTransaction(t *testing.T) {
	db := changeDB(t)
	ctx := context.Background()

	res, err := RunScript(ctx, db, DriverSQLite, parseScript(t, DriverSQLite, `
		INSERT INTO people(name) VALUES ('first');
		INSERT INTO missing VALUES (1);
		INSERT INTO people(name) VALUES ('third')`), ScriptOptions{Transaction: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 1 || res.Transaction != ScriptRolledBack {
		t.Fatalf("failed = %d transaction = %s", res.Failed, res.Transaction)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM people`); n != 3 {
		t.Errorf("rows = %d: the statement before the failure was kept", n)
	}

	res, err = RunScript(ctx, db, DriverSQLite, parseScript(t, DriverSQLite, `
		INSERT INTO people(name) VALUES ('first');
		INSERT INTO people(name) VALUES ('second')`), ScriptOptions{Transaction: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != -1 || res.Transaction != ScriptCommitted {
		t.Fatalf("failed = %d transaction = %s", res.Failed, res.Transaction)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM people`); n != 5 {
		t.Errorf("rows = %d, want the two inserts committed", n)
	}

	// A script that opens its own transaction cannot also be run inside one.
	if _, err := RunScript(ctx, db, DriverSQLite, parseScript(t, DriverSQLite,
		`BEGIN; DELETE FROM people WHERE id = 1; COMMIT`), ScriptOptions{Transaction: true}); err == nil {
		t.Error("a script with its own BEGIN was wrapped in a second transaction")
	}
	// Run as written, it works, and its statements see each other.
	res, err = RunScript(ctx, db, DriverSQLite, parseScript(t, DriverSQLite,
		`BEGIN; DELETE FROM people WHERE id = 1; ROLLBACK`), ScriptOptions{})
	if err != nil || res.Failed != -1 {
		t.Fatalf("a script managing its own transaction: %+v %v", res, err)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM people WHERE id = 1`); n != 1 {
		t.Error("the script's own ROLLBACK did not undo its DELETE")
	}
}

// A script of reads runs inside the read-only scope as a whole, and a read
// inside a script that also inserts still cannot write.
func TestRunScriptKeepsReadsReadOnly(t *testing.T) {
	db := changeDB(t)
	ctx := context.Background()

	res, err := RunScript(ctx, db, DriverSQLite, []SQLStatement{
		*mustStatement(t, DriverSQLite, `SELECT 1`),
		*misread(`DELETE FROM people`),
	}, ScriptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Transaction != ScriptReadOnly || res.Failed != 1 {
		t.Errorf("transaction = %s failed = %d", res.Transaction, res.Failed)
	}
	if open := db.Stats().OpenConnections; open != 1 {
		t.Errorf("a script of reads did not return its connection (%d open)", open)
	}

	res, err = RunScript(ctx, db, DriverSQLite, []SQLStatement{
		*mustStatement(t, DriverSQLite, `INSERT INTO people(name) VALUES ('kept')`),
		*misread(`DELETE FROM people`),
	}, ScriptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 1 {
		t.Errorf("a write labelled a read ran inside a script: %+v", res.Steps)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM people`); n != 4 {
		t.Errorf("rows = %d, want the insert kept and the delete refused", n)
	}
	if _, err := RunScript(ctx, db, DriverSQLite, nil, ScriptOptions{}); err == nil {
		t.Error("an empty script ran")
	}
}

// The results of a script are all held at once, so its rows are budgeted as a
// whole. Past the budget a statement still runs and its result says it was cut.
func TestRunScriptBudgetsRowsAcrossStatements(t *testing.T) {
	db := changeDB(t)
	const many = `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 6000) SELECT i FROM n`
	script := strings.Repeat(many+";\n", 6)
	res, err := RunScript(context.Background(), db, DriverSQLite, parseScript(t, DriverSQLite, script),
		ScriptOptions{MaxRows: MaxResultRows})
	if err != nil || res.Failed != -1 {
		t.Fatalf("script: %+v %v", res, err)
	}
	total := 0
	for i, step := range res.Steps {
		total += step.Result.RowCount
		if !step.Result.Truncated {
			t.Errorf("statement %d returned %d of 6000 rows and was not flagged", i, step.Result.RowCount)
		}
	}
	if res.Steps[0].Result.RowCount != MaxResultRows || res.Steps[5].Result.RowCount != 1 {
		t.Errorf("first = %d last = %d rows", res.Steps[0].Result.RowCount, res.Steps[5].Result.RowCount)
	}
	if total > ScriptRowBudget+len(res.Steps) {
		t.Errorf("the script returned %d rows, past its budget of %d", total, ScriptRowBudget)
	}
}

func TestReadCell(t *testing.T) {
	db := changeDB(t)
	ctx := context.Background()
	long := strings.Repeat("long text ", 2000)
	if _, err := db.Exec(`UPDATE people SET doc = ?, data = x'00ff10' WHERE id = 1`, long); err != nil {
		t.Fatal(err)
	}

	cell, err := ReadCell(ctx, db, DriverSQLite, "", "people", "doc", map[string]any{"id": 1})
	if err != nil {
		t.Fatal(err)
	}
	if cell.Encoding != "text" || cell.Value != long || cell.Size != int64(len(long)) {
		t.Errorf("text cell = %s size %d", cell.Encoding, cell.Size)
	}
	cell, err = ReadCell(ctx, db, DriverSQLite, "", "people", "data", map[string]any{"id": 1})
	if err != nil {
		t.Fatal(err)
	}
	if cell.Encoding != "base64" || cell.Value != base64.StdEncoding.EncodeToString([]byte{0x00, 0xff, 0x10}) || cell.Size != 3 {
		t.Errorf("binary cell = %+v", cell)
	}
	cell, err = ReadCell(ctx, db, DriverSQLite, "", "people", "big", map[string]any{"id": 1})
	if err != nil || cell.Encoding != "null" || cell.Value != nil {
		t.Errorf("null cell = %+v %v", cell, err)
	}

	if _, err := ReadCell(ctx, db, DriverSQLite, "", "people", "doc", map[string]any{"id": 99}); !errors.Is(err, ErrRowNotFound) {
		t.Errorf("a missing row = %v", err)
	}
	if _, err := ReadCell(ctx, db, DriverSQLite, "", "notes", "msg", map[string]any{"msg": "dup"}); !errors.Is(err, ErrRowAmbiguous) {
		t.Errorf("a key matching two rows = %v", err)
	}
	if _, err := ReadCell(ctx, db, DriverSQLite, "", "people", "nope", map[string]any{"id": 1}); err == nil {
		t.Error("an unknown column was read")
	}
	if _, err := ReadCell(ctx, db, DriverSQLite, "", "people", "doc", map[string]any{"name": "Ann"}); err == nil {
		t.Error("a key that is not the primary key was accepted")
	}

	// A value past the bound is measured on the server and refused with its
	// size, without being fetched.
	if _, err := db.Exec(`UPDATE people SET data = zeroblob(?) WHERE id = 2`, MaxCellBytes+1); err != nil {
		t.Fatal(err)
	}
	var tooLarge *CellTooLargeError
	if _, err := ReadCell(ctx, db, DriverSQLite, "", "people", "data", map[string]any{"id": 2}); !errors.As(err, &tooLarge) || tooLarge.Size != MaxCellBytes+1 {
		t.Errorf("a value past the bound = %v", err)
	}
}

// Oracle is the engine with values it can only count in characters, and one it
// cannot measure at all. Neither may be guessed around: a floor is reported as
// a floor, and a LONG is not read to find out how large it was.
func TestOracleMeasuresACellBeforeReadingIt(t *testing.T) {
	d := oracleDialect{}
	for _, c := range []struct {
		columnType, want string
		floor            bool
	}{
		{"BLOB", `DBMS_LOB.GETLENGTH("c")`, false},
		{"BFILE", `DBMS_LOB.GETLENGTH("c")`, false},
		{"CLOB", `DBMS_LOB.GETLENGTH("c")`, true},
		{"nclob", `DBMS_LOB.GETLENGTH("c")`, true},
		{"JSON", `DBMS_LOB.GETLENGTH(JSON_SERIALIZE("c" RETURNING BLOB))`, false},
		{"XMLTYPE", `DBMS_LOB.GETLENGTH(XMLSERIALIZE(CONTENT "c" AS CLOB NO INDENT))`, true},
		{"VARCHAR2(4000)", `VSIZE("c")`, false},
		{"NUMBER(18,2)", `VSIZE("c")`, false},
		{"RAW(16)", `VSIZE("c")`, false},
		{"TIMESTAMP(6) WITH TIME ZONE", `VSIZE("c")`, false},
	} {
		got, floor, err := d.byteLength(Column{Name: "c", Type: c.columnType}, `"c"`)
		if err != nil || got != c.want || floor != c.floor {
			t.Errorf("%s measured with %q floor %v (%v), want %q floor %v", c.columnType, got, floor, err, c.want, c.floor)
		}
	}
	for _, columnType := range []string{"LONG", "LONG RAW", "long"} {
		if got, _, err := d.byteLength(Column{Name: "c", Type: columnType}, `"c"`); err == nil {
			t.Errorf("%s was given the measure %q; nothing can measure one", columnType, got)
		}
	}

	exact := (&CellTooLargeError{Size: MaxCellBytes + 1}).Error()
	floor := (&CellTooLargeError{Size: MaxCellBytes + 1, AtLeast: true}).Error()
	if !strings.Contains(exact, "is 8388609 bytes") || !strings.Contains(floor, "is at least 8388609 bytes") {
		t.Errorf("exact: %q\nfloor: %q", exact, floor)
	}
}

// Oracle's scope is that only a query is sent. The check reads the characters
// of what is sent and nothing else, so it holds when the classifier's reading
// of the statement was the thing that was wrong.
func TestOnlyAQueryIsRunAsAReadOnOracle(t *testing.T) {
	for query, want := range map[string]bool{
		"SELECT 1 FROM dual":                                   true,
		"select\n1 from dual":                                  true,
		"  (SELECT 1 FROM dual) UNION SELECT 2 FROM dual":      true,
		"WITH q AS (SELECT 1 n FROM dual) SELECT n FROM q":     true,
		"with/* c */q AS (SELECT 1 FROM dual) SELECT * FROM q": true,
		"SELECT(1) FROM dual":                                  true,
		"SELECT":                                               false,
		"SELECTED FROM t":                                      false,
		"WITH$ AS x":                                           false,
		"select_all()":                                         false,
		"CREATE TABLE t (id INT)":                              false,
		"DROP TABLE t":                                         false,
		"INSERT INTO t SELECT 1 FROM dual":                     false,
		"EXPLAIN PLAN FOR SELECT 1 FROM dual":                  false,
		"BEGIN EXECUTE IMMEDIATE 'DROP TABLE t'; END":          false,
		"/* SELECT */ DROP TABLE t":                            false,
		"-- SELECT\nDROP TABLE t":                              false,
		"":                                                     false,
	} {
		if got := beginsAsQuery(query); got != want {
			t.Errorf("beginsAsQuery(%q) = %v, want %v", query, got, want)
		}
	}
	if s := (oracleDialect{}).readScope(); s != readScopeQuery {
		t.Errorf("Oracle's read scope = %v", s)
	}
	// So nothing that is called a read there may need more than a query:
	// EXPLAIN PLAN is run as the write to the plan table that it is.
	if st := mustStatement(t, DriverOracle, "EXPLAIN PLAN FOR SELECT 1 FROM dual"); st.Risk.Level != "medium" || st.returnsRows {
		t.Errorf("Oracle's EXPLAIN PLAN is classified %s, returning rows %v", st.Risk.Level, st.returnsRows)
	}
	if st := mustStatement(t, DriverPostgres, "EXPLAIN SELECT 1"); st.Risk.Level != "read" || !st.returnsRows {
		t.Errorf("PostgreSQL's EXPLAIN is classified %s, returning rows %v", st.Risk.Level, st.returnsRows)
	}
	// What the scope hands a statement to refuses before it reaches the engine:
	// the queryer underneath is nil, and is never called.
	guard := queryOnly{}
	if _, err := guard.ExecContext(context.Background(), "CREATE TABLE smuggled (id INT)"); !errors.Is(err, ErrNotAQuery) {
		t.Errorf("a schema change inside the scope = %v", err)
	}
	if _, err := guard.QueryContext(context.Background(), "DELETE FROM t RETURNING id"); !errors.Is(err, ErrNotAQuery) {
		t.Errorf("a write that answers in rows inside the scope = %v", err)
	}
}

// A table on an engine whose driver misreads some column types is selected
// column by column and typed from the catalogue. Every other engine is asked
// for * as before.
func TestProjectionSelectsWhatTheDriverCannotRead(t *testing.T) {
	cols := []Column{
		{Name: "ID", Type: "NUMBER(10,0)"}, {Name: "DOC", Type: "JSON"}, {Name: "X", Type: "XMLTYPE"},
		{Name: "FLAG", Type: "BOOLEAN"}, {Name: "BODY", Type: "BLOB"}, {Name: `odd"name`, Type: "VARCHAR2(10)"},
	}
	p, err := projectionFor(oracleDialect{}, cols, nil)
	if err != nil || p == nil {
		t.Fatalf("projection = %v %v", p, err)
	}
	if want := `"ID", "DOC", XMLSERIALIZE(CONTENT "X" AS CLOB NO INDENT) AS "X", "FLAG", "BODY", "odd""name"`; p.list != want {
		t.Errorf("select list = %s\nwant          %s", p.list, want)
	}
	for name, want := range map[string]string{"ID": KindInteger, "DOC": KindJSON, "X": KindText, "FLAG": KindBoolean, "BODY": KindBinary} {
		if p.kinds[name] != want {
			t.Errorf("%s is typed %q, want %q", name, p.kinds[name], want)
		}
	}
	// The grid's own choice of columns keeps its order, and a name the
	// catalogue does not have is left for the engine to refuse.
	p, err = projectionFor(oracleDialect{}, cols, []string{"X", "ID", "NOPE"})
	if err != nil || p.list != `XMLSERIALIZE(CONTENT "X" AS CLOB NO INDENT) AS "X", "ID", "NOPE"` {
		t.Errorf("narrowed select list = %v %v", p, err)
	}
	if _, err := projectionFor(oracleDialect{}, cols, []string{"bad\x00name"}); err == nil {
		t.Error("a column name with a NUL in it was quoted")
	}
	for _, driver := range []Driver{DriverPostgres, DriverMySQL, DriverSQLite, DriverMSSQL, DriverClickHouse} {
		if p, err := projectionFor(mustDialect(t, driver), cols, nil); p != nil || err != nil {
			t.Errorf("%s was given a projection: %v %v", driver, p, err)
		}
	}
	sel, err := browseSelect(oracleDialect{}, BrowseOptions{Schema: "APP", Table: "T", Columns: []string{"X", "ID"}, catalog: cols})
	if err != nil || sel.query != `SELECT XMLSERIALIZE(CONTENT "X" AS CLOB NO INDENT) AS "X", "ID" FROM "APP"."T"` || sel.kinds["X"] != KindText {
		t.Errorf("select = %v %v", sel, err)
	}
	// With no catalogue to go by, it is the statement every engine gets.
	sel, err = browseSelect(oracleDialect{}, BrowseOptions{Schema: "APP", Table: "T"})
	if err != nil || sel.query != `SELECT * FROM "APP"."T"` || sel.kinds != nil {
		t.Errorf("select with no catalogue = %v %v", sel, err)
	}
}

// Oracle compares a date with text by the session's own format, so a filter on
// a date column is given the mask the grid's text is written in. Only where the
// catalogue says the column is a date: everything else is bound as it came.
func TestFiltersOnOracleDatesSayHowToReadThem(t *testing.T) {
	catalog := []Column{
		{Name: "D", Type: "DATE"}, {Name: "TS", Type: "TIMESTAMP(6)"},
		{Name: "TZ", Type: "TIMESTAMP(6) WITH TIME ZONE"}, {Name: "NAME", Type: "VARCHAR2(50)"},
	}
	clause, args, err := buildWhereMatch(oracleDialect{}, []Filter{
		{Column: "D", Op: "gte", Value: "2024-02-01"},
		{Column: "TS", Op: "between", Values: []string{"2024-01-15T00:00:00Z", "2024-02-15 00:00:00.25"}},
		{Column: "TZ", Op: "in", Values: []string{"2024-01-02T03:04:05+02:00"}},
		{Column: "NAME", Op: "eq", Value: "2024-02-01"},
		{Column: "D", Op: "eq", Value: "last week"},
		{Column: "TS", Op: "contains", Value: "2024-02"},
		{Column: "D", Op: "regex", Value: "^2024"},
	}, false, 1, catalog)
	if err != nil {
		t.Fatal(err)
	}
	want := ` WHERE "D" >= TO_DATE(:1, 'YYYY-MM-DD"T"HH24:MI:SS')` +
		` AND "TS" BETWEEN TO_TIMESTAMP(:2, 'YYYY-MM-DD"T"HH24:MI:SS.FF9') AND TO_TIMESTAMP(:3, 'YYYY-MM-DD"T"HH24:MI:SS.FF9')` +
		` AND "TZ" IN (TO_TIMESTAMP_TZ(:4, 'YYYY-MM-DD"T"HH24:MI:SS.FF9TZH:TZM'))` +
		` AND "NAME" = :5 AND "D" = :6` +
		` AND TO_CHAR(TO_CHAR("TS", 'YYYY-MM-DD"T"HH24:MI:SS.FF"Z"')) LIKE :7 ESCAPE '!'` +
		` AND REGEXP_LIKE(TO_CHAR(TO_CHAR("D", 'YYYY-MM-DD"T"HH24:MI:SS"Z"')), :8)`
	if clause != want {
		t.Errorf("where = %s\nwant    %s", clause, want)
	}
	wantArgs := []any{
		"2024-02-01T00:00:00", "2024-01-15T00:00:00.000000000", "2024-02-15T00:00:00.250000000",
		"2024-01-02T03:04:05.000000000+02:00", "2024-02-01", "last week", "%2024-02%", "^2024",
	}
	if fmt.Sprint(args) != fmt.Sprint(wantArgs) {
		t.Errorf("args = %v\nwant   %v", args, wantArgs)
	}
	// With no catalogue the filter is what it always was, on Oracle too.
	clause, args, err = buildWhere(oracleDialect{}, []Filter{{Column: "D", Op: "gte", Value: "2024-02-01"}}, 1)
	if err != nil || clause != ` WHERE "D" >= :1` || len(args) != 1 || args[0] != "2024-02-01" {
		t.Errorf("without a catalogue = %s %v %v", clause, args, err)
	}
	// And no other engine is told anything: they read the text themselves.
	clause, _, err = buildWhereMatch(mustDialect(t, DriverPostgres), []Filter{{Column: "D", Op: "gte", Value: "2024-02-01"}}, false, 1, catalog)
	if err != nil || clause != ` WHERE "D" >= $1` {
		t.Errorf("postgres = %s %v", clause, err)
	}
	for _, driver := range []Driver{DriverPostgres, DriverMySQL, DriverSQLite, DriverMSSQL, DriverClickHouse} {
		if needsCatalog(mustDialect(t, driver)) {
			t.Errorf("%s is said to need the catalogue for a page", driver)
		}
	}
	if !needsCatalog(oracleDialect{}) {
		t.Error("Oracle is not said to need the catalogue")
	}
}

func browseDB(t *testing.T) *sql.DB {
	t.Helper()
	db := changeDB(t)
	for _, s := range []string{
		`CREATE TABLE items (id INTEGER PRIMARY KEY, label TEXT, price REAL, grp TEXT)`,
		`INSERT INTO items VALUES
			(1, '50% off', 5.0, 'a'), (2, '50 percent', 7.5, 'a'), (3, 'under_score', 1.0, 'b'),
			(4, 'underXscore', 2.0, 'b'), (5, 'MiXeD Case', 9.0, NULL), (6, 'tail!', 3.0, 'c')`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func browseIDs(t *testing.T, db *sql.DB, opts BrowseOptions) string {
	t.Helper()
	opts.Table = "items"
	if opts.Limit == 0 {
		opts.Limit = 50
	}
	res, err := Browse(context.Background(), db, DriverSQLite, opts)
	if err != nil {
		t.Fatalf("Browse(%+v): %v", opts, err)
	}
	at := indexOf(res.Columns, "id")
	var ids []string
	for _, row := range res.Rows {
		ids = append(ids, row[at].(string))
	}
	return strings.Join(ids, ",")
}

func TestBrowseFilterOperators(t *testing.T) {
	db := browseDB(t)
	byID := []SortKey{{Column: "id"}}
	for _, c := range []struct {
		name   string
		filter Filter
		want   string
	}{
		// A wildcard in the operator's text is that character.
		{"contains a percent sign", Filter{Column: "label", Op: "contains", Value: "50%"}, "1"},
		{"contains an underscore", Filter{Column: "label", Op: "contains", Value: "r_s"}, "3"},
		{"contains the escape character", Filter{Column: "label", Op: "suffix", Value: "l!"}, "6"},
		{"prefix", Filter{Column: "label", Op: "prefix", Value: "50"}, "1,2"},
		{"suffix", Filter{Column: "label", Op: "suffix", Value: "score"}, "3,4"},
		{"icontains", Filter{Column: "label", Op: "icontains", Value: "mixed case"}, "5"},
		{"not_contains", Filter{Column: "label", Op: "not_contains", Value: "score"}, "1,2,5,6"},
		{"in", Filter{Column: "id", Op: "in", Values: []string{"2", "4", "9"}}, "2,4"},
		{"in, one value in the old field", Filter{Column: "id", Op: "in", Value: "3"}, "3"},
		{"not_in", Filter{Column: "id", Op: "not_in", Values: []string{"1", "2", "3", "4"}}, "5,6"},
		{"between", Filter{Column: "price", Op: "between", Values: []string{"2", "5"}}, "1,4,6"},
		{"is_null", Filter{Column: "grp", Op: "is_null"}, "5"},
		{"ne", Filter{Column: "grp", Op: "ne", Value: "a"}, "3,4,6"},
	} {
		if got := browseIDs(t, db, BrowseOptions{Filters: []Filter{c.filter}, Sort: byID}); got != c.want {
			t.Errorf("%s: ids = %q, want %q", c.name, got, c.want)
		}
	}
	for _, bad := range []Filter{
		{Column: "id", Op: "between", Values: []string{"1"}},
		{Column: "id", Op: "regex", Value: "."}, // SQLite has no regular expressions
		{Column: "id", Op: "in", Values: make([]string, MaxFilterValues+1)},
		{Column: "id", Op: "like", Value: "%"},
	} {
		opts := BrowseOptions{Table: "items", Filters: []Filter{bad}}
		if _, err := Browse(context.Background(), db, DriverSQLite, opts); err == nil {
			t.Errorf("filter %+v was accepted", bad)
		}
	}
}

func TestFilterOpsAreAdvertisedPerEngine(t *testing.T) {
	has := func(ops []string, op string) bool { return indexOf(ops, op) >= 0 }
	for _, op := range []string{"in", "not_in", "between", "icontains", "suffix", "contains", "eq"} {
		if !has(FilterOps(), op) {
			t.Errorf("the shared operator list is missing %q", op)
		}
	}
	if has(FilterOps(), "regex") {
		t.Error("regex is in the shared list, but not every engine has it")
	}
	for driver, want := range map[Driver]bool{
		DriverPostgres: true, DriverMySQL: true, DriverOracle: true, DriverClickHouse: true,
		DriverSQLite: false, DriverMSSQL: false,
	} {
		if got := has(FilterOpsFor(driver), "regex"); got != want {
			t.Errorf("FilterOpsFor(%s) offers regex = %v, want %v", driver, got, want)
		}
	}
	// Every operator advertised renders, on every engine that advertises it.
	for _, driver := range Drivers() {
		d, err := DialectFor(driver)
		if err != nil {
			continue
		}
		for _, op := range FilterOpsFor(driver) {
			f := Filter{Column: "c", Op: op, Value: "v", Values: []string{"1", "2"}}
			if _, _, err := buildWhere(d, []Filter{f}, 1); err != nil {
				t.Errorf("%s advertises %q and cannot render it: %v", driver, op, err)
			}
		}
	}
}

func TestSubstringFiltersRenderForEachEngine(t *testing.T) {
	cases := []struct {
		driver  Driver
		op      string
		clause  string
		pattern string
	}{
		{DriverPostgres, "icontains", ` WHERE CAST("c" AS TEXT) ILIKE $1 ESCAPE '!'`, "%5!%!_!!%"},
		{DriverMySQL, "icontains", " WHERE LOWER(CAST(`c` AS CHAR)) LIKE LOWER(?) ESCAPE '!'", "%5!%!_!!%"},
		// SQL Server's LIKE reads [..] as a character class.
		{DriverMSSQL, "contains", " WHERE CAST([c] AS NVARCHAR(MAX)) LIKE @p1 ESCAPE '!'", "%5!%!_!!%"},
		{DriverOracle, "suffix", ` WHERE TO_CHAR("c") LIKE :1 ESCAPE '!'`, "%5!%!_!!"},
		// ClickHouse has no ESCAPE, so it does not use LIKE at all.
		{DriverClickHouse, "contains", " WHERE position(toString(`c`), ?) > 0", "5%_!"},
		{DriverClickHouse, "icontains", " WHERE position(lowerUTF8(toString(`c`)), lowerUTF8(?)) > 0", "5%_!"},
		{DriverClickHouse, "prefix", " WHERE startsWith(toString(`c`), ?)", "5%_!"},
	}
	for _, c := range cases {
		clause, args, err := buildWhere(mustDialect(t, c.driver), []Filter{{Column: "c", Op: c.op, Value: "5%_!"}}, 1)
		if err != nil {
			t.Fatalf("%s %s: %v", c.driver, c.op, err)
		}
		if clause != c.clause || len(args) != 1 || args[0] != c.pattern {
			t.Errorf("%s %s:\n got %s %v\nwant %s [%s]", c.driver, c.op, clause, args, c.clause, c.pattern)
		}
	}
	clause, args, err := buildWhere(mustDialect(t, DriverMSSQL), []Filter{{Column: "c", Op: "contains", Value: "[a-z]"}}, 1)
	if err != nil || args[0] != "%![a-z]%" {
		t.Errorf("a bracket on SQL Server: %s %v %v", clause, args, err)
	}
}

func TestBrowseSortProjectionAndMatch(t *testing.T) {
	db := browseDB(t)

	// Several sort columns, in order, each with its own direction.
	if got := browseIDs(t, db, BrowseOptions{
		Sort: []SortKey{{Column: "grp", Desc: true}, {Column: "price"}},
	}); got != "6,3,4,1,2,5" {
		t.Errorf("multi-column sort = %q", got)
	}
	// The single-column form still works on its own.
	if got := browseIDs(t, db, BrowseOptions{OrderBy: "price", Desc: true}); got != "5,2,1,6,4,3" {
		t.Errorf("single-column sort = %q", got)
	}
	// Either filter is enough.
	any := BrowseOptions{
		MatchAny: true, Sort: []SortKey{{Column: "id"}},
		Filters: []Filter{
			{Column: "grp", Op: "eq", Value: "c"},
			{Column: "price", Op: "between", Values: []string{"7", "8"}},
		},
	}
	if got := browseIDs(t, db, any); got != "2,6" {
		t.Errorf("match any = %q", got)
	}
	any.MatchAny = false
	if got := browseIDs(t, db, any); got != "" {
		t.Errorf("match all = %q, want nothing", got)
	}
	n, err := Count(context.Background(), db, DriverSQLite, BrowseOptions{
		Table: "items", MatchAny: true, Filters: any.Filters,
	})
	if err != nil || n != 2 {
		t.Errorf("count with match any = %d %v", n, err)
	}

	res, err := Browse(context.Background(), db, DriverSQLite, BrowseOptions{
		Table: "items", Columns: []string{"label", "id"}, Sort: []SortKey{{Column: "id"}}, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sameStrings(res.Columns, []string{"label", "id"}) || res.Rows[0][0] != "50% off" {
		t.Errorf("projection = %v %v", res.Columns, res.Rows)
	}
	// A projected name is quoted like every other identifier, so one that is
	// not a column is a name that matches nothing — never a second statement.
	// (SQLite reads an unknown quoted name as a string, so this is not an error
	// there; what matters is what it did not do.)
	_, _ = Browse(context.Background(), db, DriverSQLite, BrowseOptions{
		Table: "items", Columns: []string{`id" FROM items; DROP TABLE items; --`},
	})
	if n := rowCount(t, db, `SELECT COUNT(*) FROM items`); n != 6 {
		t.Error("the table did not survive the projection")
	}
	if sel, err := browseSelect(mustDialect(t, DriverPostgres), BrowseOptions{
		Table: "items", Columns: []string{`id" FROM items; DROP TABLE items; --`},
	}); err != nil || sel.query != `SELECT "id"" FROM items; DROP TABLE items; --" FROM "items"` {
		t.Errorf("projection = %v %v", sel, err)
	}
}

// A page says whether another follows it, is ordered so that the next page is
// the next rows, and carries what the grid shows around the rows.
func TestBrowseTablePage(t *testing.T) {
	db := browseDB(t)
	ctx := context.Background()

	page, err := BrowseTablePage(ctx, db, DriverSQLite, BrowseOptions{Table: "items", Limit: 4})
	if err != nil {
		t.Fatal(err)
	}
	if page.RowCount != 4 || !page.Truncated || page.Limit != 4 {
		t.Errorf("first page: rows = %d truncated = %v limit = %d", page.RowCount, page.Truncated, page.Limit)
	}
	if !sameStrings(page.PrimaryKey, []string{"id"}) || len(page.Sort) != 1 || page.Sort[0].Column != "id" {
		t.Errorf("primary key = %v sort = %v", page.PrimaryKey, page.Sort)
	}
	if page.EstimatedRows != nil {
		t.Errorf("SQLite keeps no row estimate, got %d", *page.EstimatedRows)
	}
	last, err := BrowseTablePage(ctx, db, DriverSQLite, BrowseOptions{Table: "items", Limit: 4, Offset: 4})
	if err != nil {
		t.Fatal(err)
	}
	if last.RowCount != 2 || last.Truncated {
		t.Errorf("last page: rows = %d truncated = %v", last.RowCount, last.Truncated)
	}

	// The key breaks ties in whatever order was asked for, so equal values
	// cannot shuffle between pages.
	tied, err := BrowseTablePage(ctx, db, DriverSQLite, BrowseOptions{
		Table: "items", Sort: []SortKey{{Column: "grp"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tied.Sort) != 2 || tied.Sort[1].Column != "id" || !strings.HasSuffix(tied.Statement, `ORDER BY "grp" ASC, "id" ASC LIMIT ? OFFSET ?`) {
		t.Errorf("sort = %v statement = %s", tied.Sort, tied.Statement)
	}

	// The page size is clamped to the cap, not reset to the default.
	big, err := BrowseTablePage(ctx, db, DriverSQLite, BrowseOptions{Table: "items", Limit: 1 << 20})
	if err != nil || big.Limit != MaxBrowseRows {
		t.Errorf("limit = %d, want %d (%v)", big.Limit, MaxBrowseRows, err)
	}

	// A table with no key is paged as the engine returns it.
	keyless, err := BrowseTablePage(ctx, db, DriverSQLite, BrowseOptions{Table: "notes"})
	if err != nil || len(keyless.PrimaryKey) != 0 || len(keyless.Sort) != 0 {
		t.Errorf("keyless page = %+v %v", keyless, err)
	}
}

func TestBrowseClipsLongCells(t *testing.T) {
	db := browseDB(t)
	long := strings.Repeat("é", 3000)
	if _, err := db.Exec(`UPDATE items SET label = ? WHERE id = 2`, long); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE people SET data = zeroblob(1000) WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	res, err := Browse(context.Background(), db, DriverSQLite, BrowseOptions{
		Table: "items", Sort: []SortKey{{Column: "id"}}, ClipText: 1001,
	})
	if err != nil {
		t.Fatal(err)
	}
	at := indexOf(res.Columns, "label")
	if got := res.Rows[1][at].(string); got != strings.Repeat("é", 500) {
		t.Errorf("clipped cell is %d bytes", len(got))
	}
	if len(res.Clipped) != 1 || res.Clipped[0] != (ClippedCell{Row: 1, Column: at, Size: 6000}) {
		t.Errorf("clipped = %+v", res.Clipped)
	}
	blobs, err := Browse(context.Background(), db, DriverSQLite, BrowseOptions{Table: "people", Sort: []SortKey{{Column: "id"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(blobs.Clipped) != 1 || blobs.Clipped[0].Size != 1000 || blobs.Clipped[0].Row != 0 {
		t.Errorf("a blob past the preview was not flagged: %+v", blobs.Clipped)
	}
}

func TestExplainFormsPerEngine(t *testing.T) {
	for driver, want := range map[Driver][2]bool{
		DriverPostgres: {true, true}, DriverMySQL: {true, true},
		DriverSQLite: {false, false}, DriverMSSQL: {false, false},
		DriverOracle: {false, false}, DriverClickHouse: {false, false},
	} {
		jsonPlan, analyze := ExplainForms(driver)
		if jsonPlan != want[0] || analyze != want[1] {
			t.Errorf("ExplainForms(%s) = %v %v, want %v", driver, jsonPlan, analyze, want)
		}
	}
	db := changeDB(t)
	st, err := ExplainTarget(DriverSQLite, "DELETE FROM people WHERE name = 'Ann'")
	if err != nil {
		t.Fatal(err)
	}
	for _, opts := range []ExplainOptions{{Analyze: true}, {Format: ExplainJSON}} {
		if _, err := Explain(context.Background(), db, DriverSQLite, st, opts); !errors.Is(err, ErrExplainUnsupported) {
			t.Errorf("Explain(%+v) on SQLite = %v", opts, err)
		}
	}
	if _, err := Explain(context.Background(), db, DriverSQLite, st, ExplainOptions{Format: "xml"}); err == nil {
		t.Error("an unknown format was accepted")
	}
	plan, err := Explain(context.Background(), db, DriverSQLite, st, ExplainOptions{})
	if err != nil || plan.Analyzed || plan.Format != ExplainText || plan.Result.RowCount == 0 {
		t.Errorf("plain plan = %+v %v", plan, err)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM people`); n != 3 {
		t.Error("explaining a DELETE deleted")
	}

	// The statement each engine runs for each form.
	for _, c := range []struct {
		driver  Driver
		version string
		opts    ExplainOptions
		want    string
	}{
		{DriverPostgres, "", ExplainOptions{Format: ExplainJSON}, "EXPLAIN (FORMAT JSON) SELECT 1"},
		{DriverPostgres, "", ExplainOptions{Analyze: true}, "EXPLAIN (ANALYZE, BUFFERS) SELECT 1"},
		{DriverPostgres, "", ExplainOptions{Analyze: true, Format: ExplainJSON}, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) SELECT 1"},
		{DriverMySQL, "8.0.36", ExplainOptions{Format: ExplainJSON}, "EXPLAIN FORMAT=JSON SELECT 1"},
		{DriverMySQL, "8.0.36", ExplainOptions{Analyze: true}, "EXPLAIN ANALYZE SELECT 1"},
		{DriverMySQL, "11.4.2-MariaDB-ubu2404", ExplainOptions{Analyze: true}, "ANALYZE SELECT 1"},
		{DriverMySQL, "11.4.2-MariaDB-ubu2404", ExplainOptions{Analyze: true, Format: ExplainJSON}, "ANALYZE FORMAT=JSON SELECT 1"},
	} {
		got, err := mustDialect(t, c.driver).(planner).explainSQL(c.version, "SELECT 1", c.opts)
		if err != nil || got != c.want {
			t.Errorf("%s %q %+v = %q %v, want %q", c.driver, c.version, c.opts, got, err, c.want)
		}
	}
}

func TestSearchMatchesTheNeedleAsText(t *testing.T) {
	db := browseDB(t)
	res, err := Search(context.Background(), db, DriverSQLite, "", "50%")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 1 || res.Matches[0].Table != "items" || res.Matches[0].Column != "label" {
		t.Errorf("a search for 50%% matched %+v", res.Matches)
	}
	// Case does not matter, as the feature has always said.
	res, err = Search(context.Background(), db, DriverSQLite, "", "MIXED case")
	if err != nil || len(res.Matches) != 1 {
		t.Errorf("a case-insensitive search matched %+v %v", res, err)
	}
}
