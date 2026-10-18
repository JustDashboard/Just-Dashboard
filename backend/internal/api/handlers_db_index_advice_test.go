package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// Index advice is asked for from the Explain view. It plans a statement and
// runs none, so the routes sit on the read surface with /explain: every role
// may ask, and an engine that cannot say so in a body the page can print.
func TestIndexAdviceRoutesAreOnTheReadSurface(t *testing.T) {
	h := newOpsHarness(t)

	for name, c := range map[string]*client{"viewer": h.viewer, "worker": h.worker, "admin": h.admin} {
		t.Run(name, func(t *testing.T) {
			rec := c.do(http.MethodGet, h.base+"/missing-indexes", "", nil)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"supported":false`) ||
				!strings.Contains(rec.Body.String(), `"indexes":[]`) {
				t.Errorf("GET /missing-indexes = %d %s", rec.Code, rec.Body.String())
			}
			for path, body := range map[string]string{
				"/explain/index-candidates": `{"query":"select * from child where v = 'x'"}`,
				"/explain/what-if":          `{"query":"select * from child where v = 'x'","table":"child","columns":["v"]}`,
			} {
				rec := c.do(http.MethodPost, h.base+path, body, nil)
				if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"unsupported"`) ||
					!strings.Contains(rec.Body.String(), "PostgreSQL only") {
					t.Errorf("POST %s on SQLite = %d %s", path, rec.Code, rec.Body.String())
				}
			}
		})
	}

	for _, c := range []struct{ path, body, want string }{
		{"/explain/index-candidates", `{"query":"  "}`, "query is required"},
		{"/explain/what-if", `{"query":"select 1"}`, "a table and at least one column"},
		{"/explain/what-if", `{"query":"select 1","table":"child"}`, "a table and at least one column"},
		{"/explain/index-candidates", `{"query":"drop table child"}`, "cannot be explained"},
		{"/explain/what-if", `{"query":"select 1; select 2","table":"child","columns":["v"]}`, ""},
	} {
		rec := h.viewer.do(http.MethodPost, h.base+c.path, c.body, nil)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("POST %s %s = %d %s, want 400 with %q", c.path, c.body, rec.Code, rec.Body.String(), c.want)
		}
	}
}

// Against PostgreSQL with HypoPG: the plan under a hypothetical index comes
// back, both calls leave an audit entry of their own, and a protected
// connection and a read-only role may ask because nothing is made.
func TestLiveIndexAdviceThroughTheRouter(t *testing.T) {
	const env = "JD_TEST_POSTGRES_HYPOPG_DSN"
	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skipf("set %s to run this", env)
	}
	s := testServer(t)
	id := saveOpsConnection(t, s, "advice", dbx.DriverPostgres, dsn)
	routes := s.Routes()
	admin := &client{t: t, h: routes, cookie: signIn(t, s)}
	viewer := &client{t: t, h: routes, cookie: signInAs(t, s, "advice-viewer", auth.RoleReadOnly)}
	base := pathf("/api/v1/databases/%d", id)

	drop := []string{`DROP TABLE IF EXISTS jdt_index_advice_api`}
	run := func(statements ...string) {
		t.Helper()
		for _, statement := range statements {
			body := adviceBody(map[string]any{"query": statement})
			if rec := admin.do(http.MethodPost, base+"/query", body, nil); rec.Code != http.StatusOK {
				t.Fatalf("%s = %d %s", statement, rec.Code, rec.Body.String())
			}
		}
	}
	run(drop...)
	t.Cleanup(func() {
		for _, statement := range drop {
			body := adviceBody(map[string]any{"query": statement})
			admin.do(http.MethodPost, base+"/query", body, nil)
		}
	})
	run(
		`CREATE TABLE jdt_index_advice_api (id serial PRIMARY KEY, status text NOT NULL, total numeric NOT NULL)`,
		`INSERT INTO jdt_index_advice_api (status, total) SELECT (ARRAY['open','paid','void'])[1 + g % 3], g % 400 FROM generate_series(1, 30000) g`,
		`ANALYZE jdt_index_advice_api`,
	)
	query := `select id from jdt_index_advice_api where status = 'open' and total > 300`
	asked := adviceBody(map[string]any{"query": query})

	rec := viewer.do(http.MethodPost, base+"/explain/index-candidates", asked, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"state":"ready"`) ||
		!strings.Contains(rec.Body.String(), `"columns":["status","total"]`) {
		t.Fatalf("candidates = %d %s", rec.Code, rec.Body.String())
	}
	target, detail, _ := auditEntry(t, s, "database.explain.index_candidates")
	if target != "advice" || !strings.Contains(detail, "jdt_index_advice_api") {
		t.Errorf("audit of the candidates: %s %s", target, detail)
	}

	tried := adviceBody(map[string]any{"query": query, "schema": "public", "table": "jdt_index_advice_api", "columns": []string{"status", "total"}})
	for _, protected := range []bool{false, true} {
		if protected {
			if _, err := s.Store.DB.Exec(`UPDATE db_connections SET read_only = 1 WHERE id = ?`, id); err != nil {
				t.Fatal(err)
			}
		}
		rec = viewer.do(http.MethodPost, base+"/explain/what-if", tried, nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"used":true`) ||
			!strings.Contains(rec.Body.String(), `"beforeCost"`) {
			t.Fatalf("what-if (protected=%v) = %d %s", protected, rec.Code, rec.Body.String())
		}
	}
	_, detail, _ = auditEntry(t, s, "database.explain.what_if")
	if !strings.Contains(detail, `"used":true`) || !strings.Contains(detail, "CREATE INDEX") {
		t.Errorf("audit of the what-if: %s", detail)
	}

	// The engine's own words for a column that is not there.
	bad := adviceBody(map[string]any{"query": query, "schema": "public", "table": "jdt_index_advice_api", "columns": []string{"nope"}})
	if rec = viewer.do(http.MethodPost, base+"/explain/what-if", bad, nil); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "nope") {
		t.Errorf("what-if of a column that is not there = %d %s", rec.Code, rec.Body.String())
	}
}

// A server without the extension is told so, in a sentence that names it.
func TestLiveWhatIfWithoutHypoPGSaysSo(t *testing.T) {
	const env = "JD_TEST_POSTGRES_DSN"
	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skipf("set %s to run this", env)
	}
	s := testServer(t)
	id := saveOpsConnection(t, s, "advice-plain", dbx.DriverPostgres, dsn)
	admin := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	// CI points the DSN at a closed port, so an unreachable server is a skip,
	// checked through the product's own ping route as liveAPIRouter does.
	if rec := admin.do(http.MethodGet, pathf("/api/v1/databases/%d", id)+"/ping", "", nil); !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Skipf("postgres unreachable: %s", rec.Body.String())
	}
	body := adviceBody(map[string]any{"query": "select 1", "table": "t", "columns": []string{"a"}})
	rec := admin.do(http.MethodPost, pathf("/api/v1/databases/%d", id)+"/explain/what-if", body, nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "the hypopg extension") ||
		!strings.Contains(rec.Body.String(), `"unsupported"`) {
		t.Errorf("what-if without hypopg = %d %s", rec.Code, rec.Body.String())
	}
}

func adviceBody(fields map[string]any) string {
	out, err := json.Marshal(fields)
	if err != nil {
		panic(err)
	}
	return string(out)
}
