package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// Who may ask for what, on a connection that has no row-level security to
// edit. The refusal of the engine is the same for every route, and none of
// them is let through for a role that may not make the change.
func TestRowSecurityRoutesNeedTheirCapabilityAndRefuseOtherEngines(t *testing.T) {
	h := newOpsHarness(t)
	for _, c := range []struct {
		method, path, body string
		// who may reach the handler: the viewer reads, a limited account
		// also runs a test, an administrator changes policies.
		viewer, limited bool
	}{
		{http.MethodGet, "/rls?table=child", "", true, true},
		{http.MethodPost, "/rls/test", `{"role":"web","query":"select 1"}`, false, true},
		{http.MethodPost, "/ddl/policy", `{"table":"child","name":"p","using":"true"}`, false, false},
		{http.MethodPatch, "/ddl/policy", `{"table":"child","name":"p","using":"true"}`, false, false},
		{http.MethodDelete, "/ddl/policy", `{"table":"child","name":"p"}`, false, false},
		{http.MethodPatch, "/ddl/row-security", `{"table":"child","enabled":true}`, false, false},
	} {
		for who, client := range map[string]*client{"viewer": h.viewer, "limited": h.worker, "admin": h.admin} {
			rec := client.do(c.method, h.base+c.path, c.body, nil)
			may := who == "admin" || (who == "limited" && c.limited) || (who == "viewer" && c.viewer)
			if refused := rec.Code == http.StatusForbidden; refused == may {
				t.Errorf("%s's %s %s = %d %s, may reach it = %v", who, c.method, c.path, rec.Code, rec.Body.String(), may)
			}
			if may && (rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "SQLite")) {
				t.Errorf("%s's %s %s on SQLite = %d %s, want a 400 that names the engine", who, c.method, c.path, rec.Code, rec.Body.String())
			}
		}
	}
}

// rlsAPIHarness puts a PostgreSQL connection behind the whole router, so that
// what is audited and what a protected connection refuses are the product's
// own. The harness's id is the connection; protected is a second one to the
// same database, marked read-only.
func rlsAPIHarness(t *testing.T) (h *opsHarness, schema string, protected int64) {
	t.Helper()
	dsn := os.Getenv("JD_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set JD_TEST_POSTGRES_DSN to run this")
	}
	h = newOpsHarness(t)
	db := liveOpen(t, dbx.DriverPostgres, dsn)
	var super bool
	if err := db.QueryRow(`SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&super); err != nil || !super {
		t.Skipf("this login is not a superuser (%v)", err)
	}
	const alice = "jdt_rls_editor_api_alice"
	schema = "jdt_rls_editor_api"
	cleanup := func() {
		db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`)
		db.Exec(`DROP OWNED BY ` + alice)
		db.Exec(`DROP ROLE IF EXISTS ` + alice)
	}
	cleanup()
	t.Cleanup(cleanup)
	liveExec(t, db,
		`CREATE ROLE `+alice,
		`CREATE SCHEMA `+schema,
		`CREATE TABLE `+schema+`.notes (id serial PRIMARY KEY, owner text NOT NULL, body text)`,
		`INSERT INTO `+schema+`.notes (owner, body) VALUES ('`+alice+`', 'mine'), ('somebody_else', 'theirs')`,
		`GRANT USAGE ON SCHEMA `+schema+` TO `+alice,
		`GRANT SELECT ON `+schema+`.notes TO `+alice,
	)
	h.id = saveOpsConnection(t, h.s, "rlsdb", dbx.DriverPostgres, dsn)
	h.base = pathf("/api/v1/databases/%d", h.id)
	protected = saveOpsConnection(t, h.s, "rlsdb-protected", dbx.DriverPostgres, dsn)
	if _, err := h.s.Store.DB.Exec(`UPDATE db_connections SET read_only = 1 WHERE id = ?`, protected); err != nil {
		t.Fatal(err)
	}
	return h, schema, protected
}

func jsonBody(t *testing.T, body string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("%v in %s", err, body)
	}
	return out
}

func TestLiveAPIRowSecurityFormsAndTheRoleTester(t *testing.T) {
	h, schema, protected := rlsAPIHarness(t)
	const alice = "jdt_rls_editor_api_alice"
	table := `"schema":"` + schema + `","table":"notes"`

	// Shown, then run: the statement is the same text.
	shownThenRun := func(method, path, body string) map[string]any {
		t.Helper()
		shown := h.admin.do(method, h.base+path+"?preview=1", body, nil)
		if shown.Code != http.StatusOK {
			t.Fatalf("%s %s?preview=1: %d %s", method, path, shown.Code, shown.Body.String())
		}
		ran := h.admin.do(method, h.base+path, body, nil)
		if ran.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, path, ran.Code, ran.Body.String())
		}
		preview, run := jsonBody(t, shown.Body.String()), jsonBody(t, ran.Body.String())
		if preview["preview"] != true || run["preview"] != nil || preview["statement"] != run["statement"] {
			t.Errorf("%s %s previewed\n%v\nand ran\n%v", method, path, preview["statement"], run["statement"])
		}
		return run
	}

	var state dbx.RowSecurity
	read := "/rls?schema=" + schema + "&table=notes"
	if rec := h.admin.do(http.MethodGet, h.base+read, "", nil); rec.Code != http.StatusOK {
		t.Fatalf("GET /rls = %d %s", rec.Code, rec.Body.String())
	} else {
		decodeOps(t, rec, &state)
	}
	if state.Enabled || len(state.Policies) != 0 || state.Table != "notes" {
		t.Errorf("a new table: %+v", state)
	}
	if rec := h.admin.do(http.MethodGet, h.base+"/rls?schema="+schema+"&table=no_such_table", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("a table that is not there = %d %s", rec.Code, rec.Body.String())
	}
	if rec := h.admin.do(http.MethodGet, h.base+"/rls", "", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("no table named = %d %s", rec.Code, rec.Body.String())
	}

	out := shownThenRun(http.MethodPatch, "/ddl/row-security", `{`+table+`,"enabled":true}`)
	if out["statement"] != `ALTER TABLE "`+schema+`"."notes" ENABLE ROW LEVEL SECURITY` {
		t.Errorf("enable ran %v", out["statement"])
	}
	out = shownThenRun(http.MethodPost, "/ddl/policy",
		`{`+table+`,"name":"mine","roles":["`+alice+`"],"using":"owner = current_user","withCheck":"owner = current_user"}`)
	if !strings.HasPrefix(out["statement"].(string), `CREATE POLICY "mine" ON "`+schema+`"."notes"`) {
		t.Errorf("create ran %v", out["statement"])
	}
	if rec := h.admin.do(http.MethodPost, h.base+"/ddl/policy", `{`+table+`,"name":"bad","command":"insert","using":"true"}`, nil); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "INSERT policy has no USING") {
		t.Errorf("an INSERT policy with USING = %d %s", rec.Code, rec.Body.String())
	}
	// A condition that calls something the form cannot vouch for is the
	// destructive group's, and the answer names the call.
	rec := h.admin.do(http.MethodPost, h.base+"/ddl/policy?preview=1", `{`+table+`,"name":"calls","using":"owner = pg_catalog.current_user"}`, nil)
	if reply := jsonBody(t, rec.Body.String()); rec.Code != http.StatusOK || len(reply["calls"].([]any)) != 1 {
		t.Errorf("a qualified name in a condition = %d %s", rec.Code, rec.Body.String())
	}

	decodeOps(t, h.admin.do(http.MethodGet, h.base+read, "", nil), &state)
	if !state.Enabled || len(state.Policies) != 1 || state.Policies[0].Name != "mine" || state.Policies[0].Roles[0] != alice {
		t.Errorf("after the policy: %+v", state)
	}

	// The tester.
	test := func(body string) *httptestResult {
		rec := h.admin.do(http.MethodPost, h.base+"/rls/test", body, nil)
		return &httptestResult{rec.Code, rec.Body.String()}
	}
	res := test(`{"role":"` + alice + `","schema":"` + schema + `","table":"notes","query":"SELECT body FROM ` + schema + `.notes ORDER BY id"}`)
	if res.code != http.StatusOK {
		t.Fatalf("test as alice = %d %s", res.code, res.body)
	}
	var seen dbx.RoleTestResult
	if err := json.Unmarshal([]byte(res.body), &seen); err != nil || seen.Role != alice || seen.Result.RowCount != 1 || seen.Result.Rows[0][0] != "mine" {
		t.Errorf("alice saw %s", res.body)
	}
	target, detail, status := auditEntry(t, h.s, "database.rls.test")
	if target != "rlsdb" || status != http.StatusOK || !strings.Contains(detail, alice) || !strings.Contains(detail, `"rowCount":1`) {
		t.Errorf("audit of the test: target %q status %d detail %s", target, status, detail)
	}
	for name, body := range map[string]string{
		"a delete":       `{"role":"` + alice + `","query":"DELETE FROM ` + schema + `.notes"}`,
		"two statements": `{"role":"` + alice + `","query":"SELECT 1; DELETE FROM ` + schema + `.notes"}`,
		"no role":        `{"query":"SELECT 1"}`,
		"the role knob":  `{"role":"` + alice + `","query":"SELECT 1","claims":[{"name":"role","value":"postgres"}]}`,
	} {
		if res := test(body); res.code != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", name, res.code, res.body)
		}
	}
	var rows int
	db := liveOpen(t, dbx.DriverPostgres, os.Getenv("JD_TEST_POSTGRES_DSN"))
	if err := db.QueryRow(`SELECT count(*) FROM ` + schema + `.notes`).Scan(&rows); err != nil || rows != 2 {
		t.Errorf("the table holds %d rows after the refused tests (%v)", rows, err)
	}

	// A protected connection still shows what a role sees, and still refuses
	// every change to its policies.
	guarded := pathf("/api/v1/databases/%d", protected)
	testOn := func(base, body string) *httptestResult {
		rec := h.admin.do(http.MethodPost, base+"/rls/test", body, nil)
		return &httptestResult{rec.Code, rec.Body.String()}
	}
	if res := testOn(guarded, `{"role":"`+alice+`","query":"SELECT body FROM `+schema+`.notes"}`); res.code != http.StatusOK {
		t.Errorf("a test on a protected connection = %d %s", res.code, res.body)
	}
	if res := testOn(guarded, `{"role":"`+alice+`","query":"DELETE FROM `+schema+`.notes"}`); res.code != http.StatusConflict {
		t.Errorf("a write as a test on a protected connection = %d %s, want 409", res.code, res.body)
	}
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/ddl/policy", `{` + table + `,"name":"again","using":"true"}`},
		{http.MethodDelete, "/ddl/policy", `{` + table + `,"name":"mine"}`},
		{http.MethodPatch, "/ddl/row-security", `{` + table + `,"enabled":false}`},
	} {
		if rec := h.admin.do(c.method, guarded+c.path, c.body, nil); rec.Code != http.StatusConflict {
			t.Errorf("%s %s on a protected connection = %d %s, want 409", c.method, c.path, rec.Code, rec.Body.String())
		}
		if rec := h.admin.do(c.method, guarded+c.path+"?preview=1", c.body, nil); rec.Code != http.StatusOK {
			t.Errorf("%s %s?preview=1 on a protected connection = %d %s, want the statement", c.method, c.path, rec.Code, rec.Body.String())
		}
	}

	// The audit trail names the change and the statement that ran.
	for action, want := range map[string]string{
		"database.ddl.row_security":  "ENABLE ROW LEVEL SECURITY",
		"database.ddl.create_policy": `CREATE POLICY \"mine\"`,
	} {
		_, detail, status := auditEntry(t, h.s, action)
		if status != http.StatusOK || !strings.Contains(detail, want) {
			t.Errorf("audit of %s: status %d detail %s", action, status, detail)
		}
	}

	// Changing and dropping.
	shownThenRun(http.MethodPatch, "/ddl/policy", `{`+table+`,"name":"mine","using":"owner = current_user AND body <> 'x'"}`)
	shownThenRun(http.MethodDelete, "/ddl/policy", `{`+table+`,"name":"mine"}`)
	shownThenRun(http.MethodPatch, "/ddl/row-security", `{`+table+`,"enabled":false,"force":false}`)
	decodeOps(t, h.admin.do(http.MethodGet, h.base+read, "", nil), &state)
	if state.Enabled || len(state.Policies) != 0 {
		t.Errorf("after taking it apart: %+v", state)
	}
	if _, detail, _ := auditEntry(t, h.s, "database.ddl.drop_policy"); !strings.Contains(detail, "DROP POLICY") {
		t.Errorf("audit of the drop: %s", detail)
	}
}

type httptestResult struct {
	code int
	body string
}

// The grants a role holds on functions and columns are listed with the names
// that tell them apart, and changed by the privilege routes that were already
// there — the closed set they publish grows two levels, for PostgreSQL only.
func TestLiveAPIFunctionAndColumnGrants(t *testing.T) {
	h, schema, _ := rlsAPIHarness(t)
	const alice = "jdt_rls_editor_api_alice"
	db := liveOpen(t, dbx.DriverPostgres, os.Getenv("JD_TEST_POSTGRES_DSN"))
	liveExec(t, db, `CREATE FUNCTION `+schema+`.twice(n int) RETURNS int LANGUAGE sql AS 'SELECT n * 2'`,
		`REVOKE EXECUTE ON FUNCTION `+schema+`.twice(int) FROM PUBLIC`)

	var levels struct {
		Levels []dbx.PrivilegeLevel `json:"levels"`
	}
	decodeOps(t, h.admin.do(http.MethodGet, h.base+"/server/privileges", "", nil), &levels)
	names := map[string][]string{}
	for _, level := range levels.Levels {
		names[level.Level] = level.Needs
	}
	if strings.Join(names["function"], ",") != "schema,routine" || strings.Join(names["column"], ",") != "schema,table,columns" {
		t.Errorf("the published levels: %v", names)
	}

	post := func(path, body string) map[string]any {
		t.Helper()
		rec := h.admin.do(http.MethodPost, h.base+"/server/roles/"+alice+path, body, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %s %s = %d %s", path, body, rec.Code, rec.Body.String())
		}
		return jsonBody(t, rec.Body.String())
	}
	grant := `{"level":"function","schema":"` + schema + `","routine":"twice","arguments":"n integer","privileges":["EXECUTE"]}`
	shown := post("/privileges?preview=1", grant)
	if got := shown["statements"].([]any)[0]; got != `GRANT EXECUTE ON ROUTINE "`+schema+`"."twice"(n integer) TO "`+alice+`"` {
		t.Errorf("previewed %v", got)
	}
	var can bool
	if err := db.QueryRow(`SELECT has_function_privilege($1, $2, 'EXECUTE')`, alice, schema+".twice(integer)").Scan(&can); err != nil || can {
		t.Fatalf("a preview granted EXECUTE (%v)", err)
	}
	post("/privileges", grant)
	if err := db.QueryRow(`SELECT has_function_privilege($1, $2, 'EXECUTE')`, alice, schema+".twice(integer)").Scan(&can); err != nil || !can {
		t.Errorf("the grant did not give EXECUTE (%v)", err)
	}
	if rec := h.admin.do(http.MethodPost, h.base+"/server/roles/"+alice+"/privileges?preview=1",
		`{"level":"function","schema":"`+schema+`","routine":"twice","arguments":"n integer) TO PUBLIC; --","privileges":["EXECUTE"]}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("an argument list that leaves its parentheses = %d %s", rec.Code, rec.Body.String())
	}
	post("/privileges", `{"level":"column","schema":"`+schema+`","table":"notes","columns":["id","body"],"privileges":["UPDATE"]}`)
	if err := db.QueryRow(`SELECT has_column_privilege($1, $2, 'body', 'UPDATE')`, alice, schema+".notes").Scan(&can); err != nil || !can {
		t.Errorf("the column grant did not give UPDATE (%v)", err)
	}

	var detail dbx.RoleDetail
	decodeOps(t, h.admin.do(http.MethodGet, h.base+"/server/roles/"+alice, "", nil), &detail)
	functions, columns := 0, 0
	for _, g := range detail.Grants {
		switch {
		case g.Level == dbx.GrantOnFunction && g.Routine == "twice" && g.Arguments == "n integer":
			functions++
		case g.Level == dbx.GrantOnColumn && g.Table == "notes" && (g.Column == "id" || g.Column == "body"):
			columns++
		}
	}
	if functions != 1 || columns != 2 {
		t.Errorf("the role's grants hold %d function and %d column rows: %+v", functions, columns, detail.Grants)
	}
	target, auditDetail, _ := auditEntry(t, h.s, "database.role.grant")
	if target != "rlsdb" || !strings.Contains(auditDetail, `"routine"`) && !strings.Contains(auditDetail, `"columns"`) {
		t.Errorf("audit of the grant: %s %s", target, auditDetail)
	}
}
