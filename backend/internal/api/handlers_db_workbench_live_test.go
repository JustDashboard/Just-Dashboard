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
