package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// A stored program written in the console reaches the engine as one
// statement, and costs what a statement holding a body costs: the routes
// that read SQL all take where it ends from dbx, and none of them is cheaper
// than the destructive capability for one.

func jsonText(t *testing.T, v string) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

const consoleTrigger = `CREATE TRIGGER notes_counted AFTER INSERT ON notes
BEGIN
  UPDATE notes SET n = CASE WHEN NEW.n IS NULL THEN 0 ELSE NEW.n END WHERE rowid = NEW.rowid;
  INSERT INTO people(name, age) VALUES ('note ' || NEW.msg, 0);
END;
INSERT INTO notes(msg) VALUES ('fresh');
SELECT COUNT(*) FROM people WHERE name = 'note fresh'`

func TestScriptRouteRunsATriggerWrittenInTheConsole(t *testing.T) {
	w := newWorkbench(t)

	// Its body holds statements of its own, so a role that may not destroy is
	// refused it before anything runs.
	limited := w.as(auth.RoleLimited, "limited")
	if rec := do(t, limited, http.MethodPost, w.url("/script"), `{"script":`+jsonText(t, consoleTrigger)+`}`); rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), "destructive") {
		t.Fatalf("a trigger from a role that may not destroy = %d %s", rec.Code, rec.Body.String())
	}

	admin := w.as(auth.RoleAdmin, "admin")
	rec := do(t, admin, http.MethodPost, w.url("/script"), `{"script":`+jsonText(t, consoleTrigger)+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("script = %d: %s", rec.Code, rec.Body.String())
	}
	res := decodeJSONBody[scriptJSON](t, rec)
	if res.Failed != -1 || len(res.Statements) != 3 || res.Risk.Level != "high" {
		t.Fatalf("script = %+v", res)
	}
	if body := res.Statements[0].SQL; !strings.HasPrefix(body, "CREATE TRIGGER") || !strings.HasSuffix(body, "END") {
		t.Errorf("the trigger was sent as %q", body)
	}
	if res.Statements[1].Line != 6 || res.Statements[2].Result.Rows[0][0] != "1" {
		t.Errorf("steps = %+v", res.Statements)
	}
	if n := w.count(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = 'notes_counted'`); n != 1 {
		t.Errorf("the trigger is there %d times", n)
	}
	if detail := w.audit("database.script"); !strings.Contains(detail, `"statements":3`) || !strings.Contains(detail, `"risk":"high"`) {
		t.Errorf("audit detail = %s", detail)
	}
}

// The verdict the editor is shown cuts each engine's script where that engine
// does, and calls every statement with a body something other than a read.
func TestClassifyCutsAScriptWhereItsEngineDoes(t *testing.T) {
	w := newWorkbench(t)
	admin := w.as(auth.RoleAdmin, "admin")
	type classifyJSON struct {
		dbx.Risk
		Statements []struct {
			SQL  string   `json:"sql"`
			Line int      `json:"line"`
			Risk dbx.Risk `json:"risk"`
		} `json:"statements"`
	}
	for _, c := range []struct {
		driver dbx.Driver
		script string
		first  string
		count  int
	}{
		{dbx.DriverMySQL, "DELIMITER $$\nCREATE PROCEDURE p() BEGIN SELECT 1; END$$\nDELIMITER ;\nCALL p();",
			"CREATE PROCEDURE p() BEGIN SELECT 1; END", 2},
		{dbx.DriverMSSQL, "CREATE PROCEDURE p AS BEGIN SELECT 1; END\nGO 2\nEXEC p", "CREATE PROCEDURE p AS BEGIN SELECT 1; END", 3},
		{dbx.DriverOracle, "BEGIN\n  NULL;\nEND;\n/\nSELECT 1 FROM dual", "BEGIN\n  NULL;\nEND;", 2},
		{dbx.DriverPostgres, "CREATE FUNCTION f() RETURNS int LANGUAGE sql BEGIN ATOMIC SELECT 1; END; SELECT f()",
			"CREATE FUNCTION f() RETURNS int LANGUAGE sql BEGIN ATOMIC SELECT 1; END", 2},
	} {
		sealed, err := w.server.Sealer.Seal("never-dialled")
		if err != nil {
			t.Fatal(err)
		}
		row, err := w.server.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`,
			"classify-"+string(c.driver), string(c.driver), sealed, 0)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := row.LastInsertId()
		rec := do(t, admin, http.MethodPost, pathf("/databases/%d/classify", id), `{"query":`+jsonText(t, c.script)+`}`)
		verdict := decodeJSONBody[classifyJSON](t, rec)
		if rec.Code != http.StatusOK || len(verdict.Statements) != c.count || verdict.Statements[0].SQL != c.first ||
			!verdict.Statements[0].Risk.Destructive || !verdict.Destructive {
			t.Errorf("%s: classify = %d %+v", c.driver, rec.Code, verdict)
		}
	}
}

// A protected connection runs only what reads, and a stored program — made,
// or run as a block — is never that, on any engine and whatever separator it
// is written with.
func TestAProtectedConnectionRefusesAStoredProgram(t *testing.T) {
	s := testServer(t)
	for _, c := range []struct {
		driver  dbx.Driver
		scripts []string
	}{
		{dbx.DriverMySQL, []string{
			"CREATE PROCEDURE p() BEGIN SELECT 1; SELECT 2; END",
			"DELIMITER $$\nCREATE PROCEDURE p() BEGIN SELECT 1; END$$",
			"DELIMITER $$\nSELECT 1; SELECT 2$$",
			"BEGIN NOT ATOMIC SELECT 1; END",
		}},
		{dbx.DriverMSSQL, []string{
			"CREATE PROCEDURE p AS SELECT 1",
			"CREATE PROCEDURE p AS BEGIN SELECT 1; END\nGO",
			"BEGIN SELECT 1 END",
		}},
		{dbx.DriverOracle, []string{
			"BEGIN NULL; END;",
			"CREATE OR REPLACE PROCEDURE p AS BEGIN NULL; END;\n/",
			"WITH FUNCTION f RETURN NUMBER IS BEGIN RETURN 1; END;\nSELECT f FROM dual",
		}},
		{dbx.DriverPostgres, []string{"CREATE FUNCTION f() RETURNS int LANGUAGE sql BEGIN ATOMIC SELECT 1; END"}},
		{dbx.DriverSQLite, []string{"CREATE TRIGGER t AFTER INSERT ON x BEGIN SELECT 1; END"}},
	} {
		sealed, err := s.Sealer.Seal("never-dialled")
		if err != nil {
			t.Fatal(err)
		}
		row, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at, read_only) VALUES(?,?,?,?,1)`,
			"protected-"+string(c.driver), string(c.driver), sealed, 0)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := row.LastInsertId()
		base := pathf("/api/v1/databases/%d", id)
		for _, script := range c.scripts {
			for _, call := range []struct{ path, field string }{{"/query", "query"}, {"/script", "script"}} {
				if why := refusal(t, s, http.MethodPost, base+call.path, `{"`+call.field+`":`+jsonText(t, script)+`}`); why == "" {
					t.Errorf("%s %s %q ran on a protected connection", c.driver, call.path, script)
				}
			}
		}
		if why := refusal(t, s, http.MethodPost, base+"/query", `{"query":"SELECT 1"}`); why != "" {
			t.Errorf("%s: a read was refused: %s", c.driver, why)
		}
	}
}
