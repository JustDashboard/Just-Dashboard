package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// The workbench routes over HTTP against real servers. The dbx live tests
// prove the engine layer; these prove the handlers hand it the right half of
// the request — the engine a statement is read for, the context a cancel has
// to reach, the plan as a document rather than a cell.
//
// A fixture is named by its environment variable or the engine is skipped;
// nothing here falls back to a standard port.

func liveWorkbenchRouter(t *testing.T, driver dbx.Driver, env string) (http.Handler, int64) {
	t.Helper()
	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skipf("set %s to run this", env)
	}
	_, r, id := liveAPIRouter(t, driver, dsn)
	return r, id
}

func TestLiveAPIWorkbenchPostgres(t *testing.T) {
	r, id := liveWorkbenchRouter(t, dbx.DriverPostgres, "JD_TEST_POSTGRES_DSN")
	const table = "jd_api_workbench"
	run := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		return do(t, r, http.MethodPost, pathf("/databases/%d/script", id), body)
	}
	cleanup := func() { run(`{"script":"DROP TABLE IF EXISTS ` + table + `"}`) }
	cleanup()
	t.Cleanup(cleanup)

	// A script: DDL, a dollar-quoted body, session state carried between
	// statements, one result each.
	rec := run(`{"script":"CREATE TABLE ` + table + ` (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, label TEXT NOT NULL, doc JSONB);\nDO $body$ BEGIN INSERT INTO ` + table + `(label) VALUES ('from; a body'); END $body$;\nSET application_name = 'jd-api-script';\nSHOW application_name;\nANALYZE ` + table + `"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("script = %d: %s", rec.Code, rec.Body.String())
	}
	script := decodeJSONBody[scriptJSON](t, rec)
	if script.Failed != -1 || len(script.Statements) != 5 || !script.Risk.Destructive {
		t.Fatalf("script = %+v", script)
	}
	if script.Statements[1].Risk.Level != "high" || script.Statements[3].Result.Rows[0][0] != "jd-api-script" {
		t.Errorf("statements = %+v", script.Statements)
	}

	// The grid's edits, with the generated key and the document coming back.
	rec = do(t, r, http.MethodPost, pathf("/databases/%d/changes", id), `{"schema":"public","table":"`+table+`","changes":[
		{"op":"insert","values":{"label":"second","doc":{"n":9007199254740993}}},
		{"op":"update","key":{"id":1},"values":{"label":"first"}}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("changes = %d: %s", rec.Code, rec.Body.String())
	}
	changes := decodeJSONBody[changesResponse](t, rec)
	if row := changes.Results[0].Row; row["id"] != "2" || !strings.Contains(row["doc"].(string), "9007199254740993") {
		t.Errorf("inserted row = %v", row)
	}
	if changes.Results[1].Row["label"] != "first" {
		t.Errorf("updated row = %v", changes.Results[1].Row)
	}

	// The page carries the planner's estimate beside the rows.
	rec = do(t, r, http.MethodGet, pathf("/databases/%d/browse", id)+"?schema=public&table="+table+
		"&filters="+url.QueryEscape(`[{"column":"label","op":"regex","value":"^fi"}]`), "")
	page := decodeJSONBody[browseJSON](t, rec)
	if rec.Code != http.StatusOK || page.RowCount != 1 || page.PrimaryKey[0] != "id" || page.EstimatedRows == nil {
		t.Errorf("browse = %d %+v", rec.Code, page)
	}

	// A plan as a document; an analysed DELETE measured and rolled back.
	type planJSON struct {
		Plan       any    `json:"plan"`
		Format     string `json:"format"`
		Analyzed   bool   `json:"analyzed"`
		RolledBack bool   `json:"rolledBack"`
	}
	rec = do(t, r, http.MethodPost, pathf("/databases/%d/explain", id),
		`{"query":"DELETE FROM `+table+`","analyze":true,"format":"json"}`)
	plan := decodeJSONBody[planJSON](t, rec)
	if rec.Code != http.StatusOK || !plan.Analyzed || !plan.RolledBack || plan.Format != "json" {
		t.Fatalf("analysed plan = %d %+v", rec.Code, plan)
	}
	if nodes, ok := plan.Plan.([]any); !ok || len(nodes) != 1 {
		t.Errorf("the plan is not PostgreSQL's array of one: %T", plan.Plan)
	}
	rec = do(t, r, http.MethodGet, pathf("/databases/%d/count", id)+"?schema=public&table="+table, "")
	if !strings.Contains(rec.Body.String(), `"count":2`) {
		t.Errorf("rows after an analysed DELETE = %s, want both", rec.Body.String())
	}

	// A named run is stopped on request, and says it was.
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- do(t, r, http.MethodPost, pathf("/databases/%d/query", id), `{"queryId":"live-sleep","query":"SELECT pg_sleep(60)"}`)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		rec := do(t, r, http.MethodPost, pathf("/databases/%d/query/cancel", id), `{"queryId":"live-sleep"}`)
		if strings.Contains(rec.Body.String(), `"cancelled":true`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the run never became cancellable")
		}
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case rec := <-done:
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "query_cancelled") {
			t.Errorf("the cancelled run answered %d: %s", rec.Code, rec.Body.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the run did not stop")
	}
}

func TestLiveAPIWorkbenchMySQL(t *testing.T) {
	for _, env := range []string{"JD_TEST_MYSQL_DSN", "JD_TEST_MYSQL8_DSN"} {
		t.Run(env, func(t *testing.T) {
			r, id := liveWorkbenchRouter(t, dbx.DriverMySQL, env)
			info, err := dbx.ParseDSN(dbx.DriverMySQL, os.Getenv(env))
			if err != nil {
				t.Fatal(err)
			}
			const table = "jd_api_workbench"
			script := func(body string) *httptest.ResponseRecorder {
				t.Helper()
				return do(t, r, http.MethodPost, pathf("/databases/%d/script", id), body)
			}
			cleanup := func() { script(`{"script":"DROP TABLE IF EXISTS ` + table + `"}`) }
			cleanup()
			t.Cleanup(cleanup)

			rec := script(`{"script":"CREATE TABLE ` + table + ` (id BIGINT AUTO_INCREMENT PRIMARY KEY, label VARCHAR(50) NOT NULL); # a MySQL comment\nINSERT INTO ` + table + `(label) VALUES ('a'), ('b');\nSET @n = 41;\nSELECT @n + 1"}`)
			res := decodeJSONBody[scriptJSON](t, rec)
			if rec.Code != http.StatusOK || res.Failed != -1 || len(res.Statements) != 4 {
				t.Fatalf("script = %d %+v", rec.Code, res)
			}
			if got := res.Statements[3].Result.Rows[0][0]; got != "42" {
				t.Errorf("the session variable did not carry: %v", got)
			}

			// The row with its generated key comes back though MySQL has no
			// RETURNING, and an edit that writes the same value is one row.
			rec = do(t, r, http.MethodPost, pathf("/databases/%d/changes", id), `{"schema":"`+info.Database+`","table":"`+table+`","changes":[
				{"op":"insert","values":{"label":"c"}},
				{"op":"update","key":{"id":1},"values":{"label":"a"}}]}`)
			if rec.Code != http.StatusOK {
				t.Fatalf("changes = %d: %s", rec.Code, rec.Body.String())
			}
			changes := decodeJSONBody[changesResponse](t, rec)
			if row := changes.Results[0].Row; row == nil || row["id"] != "3" || row["label"] != "c" {
				t.Errorf("inserted row = %v", changes.Results[0].Row)
			}
			if changes.Results[1].Affected != 1 {
				t.Errorf("an unchanged update = %+v", changes.Results[1])
			}

			rec = do(t, r, http.MethodPost, pathf("/databases/%d/explain", id),
				`{"query":"SELECT * FROM `+table+` WHERE id > 1","format":"json"}`)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"plan":{`) {
				t.Errorf("json plan = %d: %.300s", rec.Code, rec.Body.String())
			}
		})
	}
}

// SQL Server and Oracle over the same routes. What differs is what the two
// engines made hard: a date goes back to Oracle in the form the grid showed
// it, a regex filter is refused where there is none, the only plan is text,
// and a cancelled statement stops on a server that has no SLEEP to cancel.
func TestLiveAPIWorkbenchSQLServerAndOracle(t *testing.T) {
	type engine struct {
		driver dbx.Driver
		env    string
		schema string
		// name is an identifier as the catalogue holds it.
		name func(string) string
		// create, slow and dual spell the fixture's table, a statement that
		// runs for minutes, and the tail a SELECT of a constant needs.
		create, slow, dual string
		regex              bool
	}
	same := func(s string) string { return s }
	for _, e := range []engine{
		{
			driver: dbx.DriverMSSQL, env: "JD_TEST_MSSQL_DSN", schema: "dbo", name: same,
			create: `CREATE TABLE jdwb_api_workbench (id INT PRIMARY KEY, label NVARCHAR(50) NOT NULL, seen DATETIME2)`,
			slow:   `SELECT COUNT_BIG(*) FROM sys.all_columns a CROSS JOIN sys.all_columns b CROSS JOIN sys.all_columns c`,
		},
		{
			driver: dbx.DriverOracle, env: "JD_TEST_ORACLE_DSN", name: strings.ToUpper,
			create: `CREATE TABLE jdwb_api_workbench (id NUMBER(10) PRIMARY KEY, label VARCHAR2(50) NOT NULL, seen DATE)`,
			slow:   `SELECT COUNT(*) FROM all_objects a, all_objects b, all_objects c`,
			dual:   " FROM dual", regex: true,
		},
	} {
		t.Run(string(e.driver), func(t *testing.T) {
			r, id := liveWorkbenchRouter(t, e.driver, e.env)
			table := e.name("jdwb_api_workbench")
			post := func(route, body string) *httptest.ResponseRecorder {
				t.Helper()
				return do(t, r, http.MethodPost, pathf("/databases/%d/"+route, id), body)
			}
			get := func(route string, query url.Values) *httptest.ResponseRecorder {
				t.Helper()
				query.Set("schema", e.schema)
				query.Set("table", table)
				return do(t, r, http.MethodGet, pathf("/databases/%d/"+route, id)+"?"+query.Encode(), "")
			}
			cleanup := func() { post("script", `{"script":"DROP TABLE jdwb_api_workbench"}`) }
			cleanup()
			t.Cleanup(cleanup)

			// A script that makes a table, fills it and reads it back.
			rec := post("script", `{"script":"`+e.create+`;\nINSERT INTO jdwb_api_workbench (id, label) VALUES (1, 'a');\nINSERT INTO jdwb_api_workbench (id, label) VALUES (2, 'b');\nSELECT COUNT(*) FROM jdwb_api_workbench"}`)
			script := decodeJSONBody[scriptJSON](t, rec)
			if rec.Code != http.StatusOK || script.Failed != -1 || len(script.Statements) != 4 {
				t.Fatalf("script = %d %s", rec.Code, rec.Body.String())
			}
			if got := script.Statements[3].Result.Rows[0][0]; got != "2" {
				t.Errorf("the script's read of the table it made = %v", got)
			}

			// The grid's edits: a date written as the grid shows one, a row
			// whose key the request supplied coming back as stored.
			rec = post("changes", `{"schema":"`+e.schema+`","table":"`+table+`","changes":[
				{"op":"update","key":{"`+e.name("id")+`":1},"values":{"`+e.name("seen")+`":"2024-05-06T07:08:09Z"}},
				{"op":"insert","values":{"`+e.name("id")+`":3,"`+e.name("label")+`":"c"}}]}`)
			if rec.Code != http.StatusOK {
				t.Fatalf("changes = %d: %s", rec.Code, rec.Body.String())
			}
			changes := decodeJSONBody[changesResponse](t, rec)
			if row := changes.Results[0].Row; row == nil || row[e.name("seen")] != "2024-05-06T07:08:09Z" {
				t.Errorf("the row after its date was edited = %v", changes.Results[0].Row)
			}
			if row := changes.Results[1].Row; row == nil || row[e.name("id")] != "3" || row[e.name("label")] != "c" {
				t.Errorf("the inserted row = %v", changes.Results[1].Row)
			}
			// The same date is what finds the row again, and a key that
			// finds none undoes the set and says which change it was.
			rec = post("changes", `{"schema":"`+e.schema+`","table":"`+table+`","changes":[
				{"op":"update","key":{"`+e.name("id")+`":1,"`+e.name("seen")+`":"2024-05-06T07:08:09Z"},"values":{"`+e.name("label")+`":"first"}},
				{"op":"delete","key":{"`+e.name("id")+`":99}}]}`)
			conflict := decodeJSONBody[errorEnvelope](t, rec).Error
			if rec.Code != http.StatusConflict || conflict.Code != "change_conflict" || conflict.Field != "changes[1]" || conflict.Reason != "matched 0 rows" {
				t.Errorf("a set with a stale delete = %d %s", rec.Code, rec.Body.String())
			}
			rec = post("changes", `{"schema":"`+e.schema+`","table":"`+table+`","changes":[
				{"op":"update","key":{"`+e.name("id")+`":1,"`+e.name("seen")+`":"2024-05-06T07:08:09Z"},"values":{"`+e.name("label")+`":"first"}}]}`)
			if rec.Code != http.StatusOK {
				t.Errorf("an edit keyed by the row's date = %d: %s", rec.Code, rec.Body.String())
			}

			// The page: key, kinds, estimate; and one cell of it, whole.
			rec = get("browse", url.Values{"filters": {`[{"column":"` + e.name("label") + `","op":"icontains","value":"FIR"}]`}})
			page := decodeJSONBody[browseJSON](t, rec)
			if rec.Code != http.StatusOK || page.RowCount != 1 || len(page.PrimaryKey) != 1 || page.PrimaryKey[0] != e.name("id") {
				t.Errorf("browse = %d %s", rec.Code, rec.Body.String())
			}
			rec = get("browse", url.Values{"filters": {`[{"column":"` + e.name("label") + `","op":"regex","value":"^fi"}]`}})
			if e.regex && (rec.Code != http.StatusOK || decodeJSONBody[browseJSON](t, rec).RowCount != 1) {
				t.Errorf("a regex filter = %d %s", rec.Code, rec.Body.String())
			}
			if !e.regex && rec.Code != http.StatusBadRequest {
				t.Errorf("a regex filter on an engine with none = %d %s", rec.Code, rec.Body.String())
			}
			rec = get("cell", url.Values{"column": {e.name("label")}, "key": {`{"` + e.name("id") + `":1}`}})
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"value":"first"`) {
				t.Errorf("cell = %d %s", rec.Code, rec.Body.String())
			}

			// The plan is text here, and the other forms say they are not offered.
			rec = post("explain", `{"query":"SELECT * FROM jdwb_api_workbench WHERE id > 1"}`)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"format":"text"`) {
				t.Errorf("text plan = %d %.300s", rec.Code, rec.Body.String())
			}
			for _, body := range []string{
				`{"query":"SELECT * FROM jdwb_api_workbench","format":"json"}`,
				`{"query":"DELETE FROM jdwb_api_workbench","analyze":true}`,
			} {
				rec = post("explain", body)
				if rec.Code != http.StatusBadRequest || decodeJSONBody[errorEnvelope](t, rec).Error.Code != "unsupported" {
					t.Errorf("explain %s = %d %s", body, rec.Code, rec.Body.String())
				}
			}
			rec = get("count", url.Values{})
			if !strings.Contains(rec.Body.String(), `"count":3`) {
				t.Errorf("rows after the refused plans = %s, want all three", rec.Body.String())
			}

			// A read is a query. What Oracle would run as something else is
			// refused before it is sent, in words the editor can show.
			rec = post("query", `{"query":"SELECT label FROM jdwb_api_workbench WHERE id = 3"}`)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"rows":[["c"]]`) {
				t.Errorf("query = %d %s", rec.Code, rec.Body.String())
			}
			if e.driver == dbx.DriverOracle {
				rec = post("query", `{"query":"DESCRIBE jdwb_api_workbench"}`)
				if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "does not begin with SELECT or WITH") {
					t.Errorf("a statement called a read that is not a query = %d %s", rec.Code, rec.Body.String())
				}
				// EXPLAIN PLAN writes to the session's plan table, so it is not
				// called a read, and the script's next line reads the plan.
				rec = post("script", `{"script":"EXPLAIN PLAN FOR SELECT * FROM jdwb_api_workbench;\nSELECT plan_table_output FROM TABLE(DBMS_XPLAN.DISPLAY())"}`)
				plan := decodeJSONBody[scriptJSON](t, rec)
				if rec.Code != http.StatusOK || plan.Failed != -1 || plan.Risk.Level != "medium" || len(plan.Statements[1].Result.Rows) == 0 {
					t.Errorf("EXPLAIN PLAN and its plan in one script = %d %s", rec.Code, rec.Body.String())
				}
			}

			// A named run is stopped on request, says it was, and leaves the
			// connection pool fit for the next statement.
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- post("query", `{"queryId":"live-slow","query":"`+e.slow+`"}`) }()
			deadline := time.Now().Add(10 * time.Second)
			for {
				rec := post("query/cancel", `{"queryId":"live-slow"}`)
				if strings.Contains(rec.Body.String(), `"cancelled":true`) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the run never became cancellable")
				}
				time.Sleep(20 * time.Millisecond)
			}
			select {
			case rec := <-done:
				if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "query_cancelled") {
					t.Errorf("the cancelled run answered %d: %s", rec.Code, rec.Body.String())
				}
			case <-time.After(20 * time.Second):
				t.Fatal("the run did not stop")
			}
			for i := 0; i < 3; i++ {
				rec = post("query", `{"query":"SELECT 1`+e.dual+`"}`)
				if rec.Code != http.StatusOK {
					t.Errorf("a query after the cancelled one = %d %s", rec.Code, rec.Body.String())
				}
			}
		})
	}
}
