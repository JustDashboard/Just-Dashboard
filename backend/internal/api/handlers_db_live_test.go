package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// End-to-end tests: real HTTP handlers, real dialects, real servers.
//
// The dbx live tests prove the engine layer works. These prove the layer above
// it is wired to the right half of it — which is a different failure. Routing
// Mongo through the SQL export path, or forgetting that Redis has no pool, are
// both bugs that every lower-level test passes straight through.
//
// Like the dbx ones, each engine skips when it is not reachable.

func liveAPIRouter(t *testing.T, driver dbx.Driver, dsn string) (*Server, http.Handler, int64) {
	t.Helper()
	s := testServer(t)
	t.Cleanup(s.Shutdown)

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{
				User: &auth.User{ID: 1, Username: "tester"},
				Role: auth.RoleAdmin, Kind: "session", IP: "127.0.0.1",
			}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	s.mountDatabaseRoutes(r)

	sealed, err := s.Sealer.Seal(dsn)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Store.DB.Exec(
		`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`,
		"live-"+string(driver), string(driver), sealed, 0)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()

	// Reachability is checked through the product's own ping route, so a
	// skipped test means the engine is down rather than that the route is.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, pathf("/databases/%d/ping", id), nil))
	var ping struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &ping)
	if !ping.OK {
		t.Skipf("%s unreachable: %s", driver, ping.Error)
	}
	return s, r, id
}

func pathf(format string, id int64) string {
	return strings.Replace(format, "%d", strconv.FormatInt(id, 10), 1)
}

func do(t *testing.T, r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// TestLiveAPIMongo drives the Mongo surface through HTTP, including the export
// and import routes that branch by driver inside a handler shared with SQL.
func TestLiveAPIMongo(t *testing.T) {
	dsn := envOr("JD_TEST_MONGO_DSN", "mongodb://127.0.0.1:27017/jdtest")
	_, r, id := liveAPIRouter(t, dbx.DriverMongo, dsn)
	const coll = "jd_api_docs"

	// Start clean, then seed through the product's own insert route.
	do(t, r, http.MethodDelete, pathf("/databases/%d/collections", id),
		`{"collection":"`+coll+`"}`)
	t.Cleanup(func() {
		do(t, r, http.MethodDelete, pathf("/databases/%d/collections", id),
			`{"collection":"`+coll+`"}`)
	})

	// The document travels as a JSON string, which is what the editor produces.
	for _, doc := range []string{`{\"name\":\"Ann\",\"age\":31}`, `{\"name\":\"Bo\",\"age\":24}`} {
		rec := do(t, r, http.MethodPost, pathf("/databases/%d/documents", id),
			`{"collection":"`+coll+`","document":"`+doc+`"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("insert: %d %s", rec.Code, rec.Body.String())
		}
	}

	t.Run("export_csv_routes_to_mongo", func(t *testing.T) {
		rec := do(t, r, http.MethodGet,
			pathf("/databases/%d/export", id)+"?table="+coll+"&format=csv", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("export: %d %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.HasPrefix(body, "_id") {
			t.Errorf("csv header = %q, want _id first", strings.SplitN(body, "\n", 2)[0])
		}
		if !strings.Contains(body, "Ann") {
			t.Errorf("export missing seeded data:\n%s", body)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "text/csv" {
			t.Errorf("content-type = %q", ct)
		}
	})

	t.Run("export_honours_filter", func(t *testing.T) {
		rec := do(t, r, http.MethodGet,
			pathf("/databases/%d/export", id)+"?table="+coll+"&format=json&filter="+
				`%7B%22name%22%3A%22Ann%22%7D`, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("filtered export: %d %s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "Bo") {
			t.Errorf("filtered export leaked a non-matching document:\n%s", rec.Body.String())
		}
	})

	t.Run("import_routes_to_mongo", func(t *testing.T) {
		rec := do(t, r, http.MethodPost, pathf("/databases/%d/import", id),
			`{"table":"`+coll+`","format":"json","data":"[{\"name\":\"Cy\"}]"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
		}
		var res struct {
			Inserted int `json:"inserted"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res.Inserted != 1 {
			t.Errorf("imported %d, want 1: %s", res.Inserted, rec.Body.String())
		}
	})

	t.Run("browse_and_indexes", func(t *testing.T) {
		// The database is the one the connection string names, which is where
		// the inserts above put their documents.
		database := "jdtest"
		if info, err := dbx.ParseDSN(dbx.DriverMongo, dsn); err == nil && info.Database != "" {
			database = info.Database
		}
		rec := do(t, r, http.MethodGet,
			pathf("/databases/%d/browse", id)+"?schema="+database+"&table="+coll+"&limit=10", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("browse: %d %s", rec.Code, rec.Body.String())
		}
		rec = do(t, r, http.MethodGet,
			pathf("/databases/%d/collections/indexes", id)+"?schema="+database+"&table="+coll, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("indexes: %d %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "_id_") {
			t.Errorf("index listing missing the _id index: %s", rec.Body.String())
		}
	})

	// Both Mongo deletions use ordinary confirmation.
	t.Run("document_and_collection_delete_do_not_need_phrases", func(t *testing.T) {
		rec := do(t, r, http.MethodDelete, pathf("/databases/%d/documents", id),
			`{"collection":"`+coll+`","filter":"{\"name\":\"Cy\"}"}`)
		if strings.Contains(rec.Body.String(), "confirmation") {
			t.Errorf("deleting a document asked for a phrase: %s", rec.Body.String())
		}
		drop := do(t, r, http.MethodDelete, pathf("/databases/%d/collections", id),
			`{"collection":"`+coll+`"}`)
		if strings.Contains(drop.Body.String(), "confirmation") {
			t.Errorf("dropping a collection asked for a phrase: %d %s",
				drop.Code, drop.Body.String())
		}
	})

	t.Run("sql_only_routes_refuse_mongo", func(t *testing.T) {
		// The SQL surface must decline rather than half-work.
		rec := do(t, r, http.MethodGet, pathf("/databases/%d/outline", id), "")
		if rec.Code == http.StatusOK {
			t.Errorf("outline answered for a Mongo connection: %s", rec.Body.String())
		}
	})
}

// TestLiveAPIRedis drives the key surface through HTTP, including the member
// operations added for collection editing.
func TestLiveAPIRedis(t *testing.T) {
	dsn := envOr("JD_TEST_REDIS_DSN", "redis://127.0.0.1:6379/0")
	_, r, id := liveAPIRouter(t, dbx.DriverRedis, dsn)
	const key = "jdapi:hash"

	do(t, r, http.MethodDelete, pathf("/databases/%d/keys", id), `{"key":"`+key+`"}`)

	t.Run("create_and_read", func(t *testing.T) {
		rec := do(t, r, http.MethodPost, pathf("/databases/%d/keys/value", id),
			`{"key":"`+key+`","type":"hash","field":"name","value":"Ann","create":true}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
		}
		rec = do(t, r, http.MethodPost, pathf("/databases/%d/keys/value", id),
			`{"key":"`+key+`","type":"hash","field":"city","value":"Oslo"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("add field: %d %s", rec.Code, rec.Body.String())
		}
		rec = do(t, r, http.MethodGet, pathf("/databases/%d/keys/value", id)+"?key="+key, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("read: %d %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "Oslo") {
			t.Errorf("value missing the field just written: %s", rec.Body.String())
		}
	})

	// Removing one member of a collection takes no typed phrase: it is the
	// everyday edit of a key browser, and the earlier rule asked for the member
	// name every time. What it must still do is remove exactly that member.
	t.Run("member_delete_removes_only_that_member", func(t *testing.T) {
		rec := do(t, r, http.MethodDelete, pathf("/databases/%d/keys", id),
			`{"key":"`+key+`","type":"hash","member":"city"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("member delete: %d %s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "confirmation") {
			t.Fatalf("member delete asked for a phrase: %s", rec.Body.String())
		}
		rec3 := do(t, r, http.MethodGet, pathf("/databases/%d/keys/value", id)+"?key="+key, "")
		if strings.Contains(rec3.Body.String(), "Oslo") {
			t.Error("member survived its deletion")
		}
		if !strings.Contains(rec3.Body.String(), "Ann") {
			t.Errorf("deleting one member took the rest with it: %s", rec3.Body.String())
		}
	})

	t.Run("rename_and_scan", func(t *testing.T) {
		target := "jdapi:renamed"
		do(t, r, http.MethodDelete, pathf("/databases/%d/keys", id), `{"key":"`+target+`"}`)
		rec := do(t, r, http.MethodPost, pathf("/databases/%d/keys/rename", id),
			`{"key":"`+key+`","to":"`+target+`"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("rename: %d %s", rec.Code, rec.Body.String())
		}
		rec = do(t, r, http.MethodGet, pathf("/databases/%d/keys", id)+"?pattern=jdapi:*", "")
		if !strings.Contains(rec.Body.String(), target) {
			t.Errorf("scan did not find the renamed key: %s", rec.Body.String())
		}
		// Clean up through the product's own route, which no longer asks for a
		// phrase to delete a key.
		do(t, r, http.MethodDelete, pathf("/databases/%d/keys", id), `{"key":"`+target+`"}`)
	})
}

// TestLiveAPIPostgres exercises the SQL surface end to end on the one engine
// most installs will actually point at.
func TestLiveAPIPostgres(t *testing.T) {
	dsn := envOr("JD_TEST_POSTGRES_DSN", "postgres://jdtest:jdtest@127.0.0.1:5432/jdtest?sslmode=disable")
	_, r, id := liveAPIRouter(t, dbx.DriverPostgres, dsn)
	const table = "jd_api_rows"

	drop := func() {
		req := httptest.NewRequest(http.MethodDelete, pathf("/databases/%d/ddl/table", id),
			strings.NewReader(`{"schema":"public","table":"`+table+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Confirm", table)
		r.ServeHTTP(httptest.NewRecorder(), req)
	}
	drop()
	t.Cleanup(drop)

	t.Run("create_table_then_use_it", func(t *testing.T) {
		rec := do(t, r, http.MethodPost, pathf("/databases/%d/ddl/table", id),
			`{"schema":"public","table":"`+table+`","columns":[
				{"name":"id","type":"integer","primaryKey":true,"notNull":true},
				{"name":"label","type":"text"}]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("create table: %d %s", rec.Code, rec.Body.String())
		}

		rec = do(t, r, http.MethodPost, pathf("/databases/%d/rows", id),
			`{"schema":"public","table":"`+table+`","values":{"id":1,"label":"one"}}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("insert row: %d %s", rec.Code, rec.Body.String())
		}

		rec = do(t, r, http.MethodPost, pathf("/databases/%d/import", id),
			`{"schema":"public","table":"`+table+`","format":"csv","hasHeader":true,"data":"id,label\n2,two\n3,three\n"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
		}

		rec = do(t, r, http.MethodGet,
			pathf("/databases/%d/count", id)+"?schema=public&table="+table, "")
		if !strings.Contains(rec.Body.String(), `"count":3`) {
			t.Errorf("count after import = %s, want 3", rec.Body.String())
		}

		// Sorted, filtered browse through the query string the grid builds.
		rec = do(t, r, http.MethodGet,
			pathf("/databases/%d/browse", id)+"?schema=public&table="+table+
				"&limit=10&orderBy=id&dir=desc&filters="+
				`%5B%7B%22column%22%3A%22label%22%2C%22op%22%3A%22contains%22%2C%22value%22%3A%22t%22%7D%5D`, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("browse: %d %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "three") || strings.Contains(rec.Body.String(), "one") {
			t.Errorf("filtered browse returned the wrong rows: %s", rec.Body.String())
		}

		rec = do(t, r, http.MethodGet,
			pathf("/databases/%d/export", id)+"?schema=public&table="+table+"&format=csv", "")
		if !strings.Contains(rec.Body.String(), "id,label") {
			t.Errorf("export: %s", rec.Body.String())
		}

		rec = do(t, r, http.MethodGet, pathf("/databases/%d/outline", id)+"?schema=public", "")
		if !strings.Contains(rec.Body.String(), table) {
			t.Errorf("outline missing the new table: %s", rec.Body.String())
		}
		rec = do(t, r, http.MethodPost, pathf("/databases/%d/orm", id), `{"target":"prisma"}`)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "model") {
			t.Errorf("orm generation: %d %s", rec.Code, rec.Body.String())
		}
	})
}

// TestLiveAPIDeveloperSurface drives the activity, search, storage and
// copy-as-SQL routes over HTTP against a real server.
//
// Each has a way of failing that only appears here rather than in dbx: search
// takes its needle from a query parameter, kill takes its confirmation from a
// header, and the SQL renderer must answer without ever opening the pool.
func TestLiveAPIDeveloperSurface(t *testing.T) {
	dsn := envOr("JD_TEST_POSTGRES_DSN", "postgres://jdtest:jdtest@127.0.0.1:5432/jdtest?sslmode=disable")
	_, r, id := liveAPIRouter(t, dbx.DriverPostgres, dsn)
	const table = "jd_api_devx"

	drop := func() {
		req := httptest.NewRequest(http.MethodDelete, pathf("/databases/%d/ddl/table", id),
			strings.NewReader(`{"schema":"public","table":"`+table+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Confirm", table)
		r.ServeHTTP(httptest.NewRecorder(), req)
	}
	drop()
	t.Cleanup(drop)

	if rec := do(t, r, http.MethodPost, pathf("/databases/%d/ddl/table", id),
		`{"schema":"public","table":"`+table+`","columns":[
			{"name":"id","type":"integer","primaryKey":true,"notNull":true},
			{"name":"label","type":"text"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("create table: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, r, http.MethodPost, pathf("/databases/%d/rows", id),
		`{"schema":"public","table":"`+table+`","values":{"id":1,"label":"needle-xyz"}}`); rec.Code != http.StatusOK {
		t.Fatalf("insert row: %d %s", rec.Code, rec.Body.String())
	}

	t.Run("activity_lists_this_connection", func(t *testing.T) {
		rec := do(t, r, http.MethodGet, pathf("/databases/%d/activity", id), "")
		if rec.Code != http.StatusOK {
			t.Fatalf("activity: %d %s", rec.Code, rec.Body.String())
		}
		var res struct {
			Sessions []struct {
				PID  string `json:"pid"`
				Self bool   `json:"self"`
			} `json:"sessions"`
			Supported bool `json:"supported"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !res.Supported {
			t.Fatalf("postgres reported no session list: %s", rec.Body.String())
		}
		// Exactly one row must be marked self, and the request that asked is
		// necessarily in the list — an empty one means the query matched
		// nothing at all.
		selves := 0
		for _, s := range res.Sessions {
			if s.Self {
				selves++
			}
		}
		if selves != 1 {
			t.Errorf("sessions marked self = %d, want 1: %s", selves, rec.Body.String())
		}
	})

	// Stopping a session takes no typed phrase — it is pressed repeatedly under
	// time pressure and nothing is lost that was not already rolling back — but
	// the pid it is given is still validated rather than pasted into a
	// statement, which is the guard that actually matters on this route.
	t.Run("kill_validates_its_pid_without_a_phrase", func(t *testing.T) {
		rec := do(t, r, http.MethodPost, pathf("/databases/%d/activity/kill", id), `{"pid":"99999"}`)
		if strings.Contains(rec.Body.String(), "confirmation") {
			t.Errorf("kill asked for a phrase: %d %s", rec.Code, rec.Body.String())
		}
		for _, bad := range []string{`1; DROP TABLE jd_api_devx`, `1 OR 1=1`, ``} {
			b, _ := json.Marshal(map[string]string{"pid": bad})
			out := do(t, r, http.MethodPost, pathf("/databases/%d/activity/kill", id), string(b))
			if out.Code == http.StatusOK {
				t.Errorf("kill accepted %q: %s", bad, out.Body.String())
			}
		}
		// The table the injected fragment named is still there.
		count := do(t, r, http.MethodGet,
			pathf("/databases/%d/count", id)+"?schema=public&table=jd_api_devx", "")
		if count.Code != http.StatusOK {
			t.Fatalf("table is gone after the rejected kills: %d %s", count.Code, count.Body.String())
		}
	})

	t.Run("search_finds_the_seeded_value", func(t *testing.T) {
		rec := do(t, r, http.MethodGet,
			pathf("/databases/%d/search", id)+"?schema=public&q=needle-xyz", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("search: %d %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, table) || !strings.Contains(body, "label") {
			t.Errorf("search did not name the table and column: %s", body)
		}
	})

	t.Run("overview_reports_the_table", func(t *testing.T) {
		rec := do(t, r, http.MethodGet, pathf("/databases/%d/overview", id)+"?schema=public", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("overview: %d %s", rec.Code, rec.Body.String())
		}
		var res struct {
			Tables []struct {
				Table string `json:"table"`
				Bytes int64  `json:"bytes"`
			} `json:"tables"`
			Pool struct {
				Open int `json:"open"`
			} `json:"pool"`
			SizesKnown bool `json:"sizesKnown"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatalf("decode: %v", err)
		}
		found := false
		for _, tb := range res.Tables {
			if tb.Table == table {
				found = true
				if tb.Bytes <= 0 {
					t.Errorf("%s reported %d bytes", table, tb.Bytes)
				}
			}
		}
		if !found {
			t.Errorf("overview missing %s: %s", table, rec.Body.String())
		}
		if !res.SizesKnown {
			t.Error("postgres reported sizes as unknown")
		}
		if res.Pool.Open == 0 {
			t.Error("pool reports no connections while serving this request")
		}
	})

	t.Run("rows_render_as_a_runnable_insert", func(t *testing.T) {
		rec := do(t, r, http.MethodPost, pathf("/databases/%d/rows/sql", id),
			`{"schema":"public","table":"`+table+`","rows":[{"id":42,"label":"it's fine"}]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("rows/sql: %d %s", rec.Code, rec.Body.String())
		}
		var res struct {
			SQL string `json:"sql"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !strings.Contains(res.SQL, "'it''s fine'") {
			t.Errorf("apostrophe not doubled: %s", res.SQL)
		}
		// Hand it straight back to the server through the query route, which is
		// what the operator will do with it.
		run := do(t, r, http.MethodPost, pathf("/databases/%d/query", id),
			`{"query":`+jsonString(res.SQL)+`}`)
		if run.Code != http.StatusOK {
			t.Fatalf("the rendered INSERT was rejected: %d %s\n%s", run.Code, run.Body.String(), res.SQL)
		}
	})

	t.Run("generators_all_answer", func(t *testing.T) {
		rec := do(t, r, http.MethodGet, "/databases/orm/targets", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("targets: %d %s", rec.Code, rec.Body.String())
		}
		for _, tg := range dbx.ORMTargets() {
			out := do(t, r, http.MethodPost, pathf("/databases/%d/orm", id),
				`{"target":"`+string(tg)+`","schema":"public"}`)
			if out.Code != http.StatusOK {
				t.Errorf("generate %s: %d %s", tg, out.Code, out.Body.String())
				continue
			}
			var res struct {
				Schema   string `json:"schema"`
				Filename string `json:"filename"`
			}
			_ = json.Unmarshal(out.Body.Bytes(), &res)
			if res.Schema == "" {
				t.Errorf("generate %s produced nothing", tg)
			}
			if res.Filename == "" || res.Filename == "schema.txt" {
				t.Errorf("generate %s has no filename of its own: %q", tg, res.Filename)
			}
		}
	})
}

// jsonString quotes a string for embedding in a request body. The rendered SQL
// contains quotes and newlines, so it cannot be concatenated in raw.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
