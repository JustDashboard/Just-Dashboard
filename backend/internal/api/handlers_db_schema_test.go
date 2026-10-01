package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// The schema surface over HTTP: which role reaches what, what a preview does
// and does not do, and the shapes the catalogue routes answer in. The engine
// behind it is a real SQLite file, so a statement that reaches the database
// here actually runs.

func schemaRouter(t *testing.T, role auth.Role) (*Server, http.Handler, *sql.DB) {
	t.Helper()
	s := testServer(t)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{User: &auth.User{ID: 1, Username: "operator"}, Role: role, Kind: "session", IP: "127.0.0.1"}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	// The audit middleware is mounted as it is in the real router, so what a
	// handler records — and what a preview does not — can be read back.
	r.Use(httpx.AuditMutations(s.Audit))
	s.mountDatabaseRoutes(r)

	path := filepath.Join(s.Cfg.FileRoots[0], "shop.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, stmt := range []string{
		`CREATE TABLE customers (id INTEGER PRIMARY KEY AUTOINCREMENT, email TEXT NOT NULL UNIQUE, name TEXT)`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, customer_id INTEGER NOT NULL REFERENCES customers(id) ON DELETE CASCADE, total REAL)`,
		`CREATE INDEX orders_customer ON orders(customer_id)`,
		`CREATE VIEW big_orders AS SELECT id, total FROM orders WHERE total > 100`,
		`CREATE TRIGGER orders_touch AFTER INSERT ON orders BEGIN UPDATE orders SET total = total WHERE id = NEW.id; END`,
		`INSERT INTO customers(email, name) VALUES ('a@x.io', 'Ann')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%v\n%s", err, stmt)
		}
	}
	sealed, err := s.Sealer.Seal(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,0)`,
		"shop", string(dbx.DriverSQLite), sealed); err != nil {
		t.Fatal(err)
	}
	return s, r, db
}

func schemaSend(t *testing.T, router http.Handler, method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func schemaErrCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func schemaErrMessage(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	msg, _ := e["message"].(string)
	return msg
}

func schemaHas(t *testing.T, db *sql.DB, kind, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = ? AND name = ?`, kind, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

// Reading the catalogue is reading the schema, which any role that may open a
// table may do.
func TestCatalogueRoutesAreReadSurface(t *testing.T) {
	_, router, _ := schemaRouter(t, auth.RoleReadOnly)

	var catalog dbx.Catalog
	if code := getJSON(t, router, "/databases/1/catalog", &catalog); code != http.StatusOK {
		t.Fatalf("a read-only account got %d for the catalogue", code)
	}
	if catalog.Schema != "main" || catalog.DefaultSchema != "main" || len(catalog.Schemas) != 1 ||
		len(catalog.Objects[dbx.GroupTables]) != 2 || len(catalog.Objects[dbx.GroupViews]) != 1 ||
		len(catalog.Objects[dbx.GroupTriggers]) != 1 || catalog.Truncated == nil || catalog.Limit != dbx.DefaultCatalogLimit {
		t.Errorf("catalogue = %+v", catalog)
	}
	// SQLite has no sequences, functions or types, and says so by leaving the
	// groups out.
	for _, absent := range []string{dbx.GroupSequences, dbx.GroupFunctions, dbx.GroupTypes} {
		if _, ok := catalog.Objects[absent]; ok {
			t.Errorf("SQLite answers with a %s group", absent)
		}
	}
	var bounded dbx.Catalog
	if code := getJSON(t, router, "/databases/1/catalog?limit=1&all=1", &bounded); code != http.StatusOK ||
		len(bounded.Objects[dbx.GroupTables]) != 1 || len(bounded.Truncated) != 1 || bounded.Schema != "" {
		t.Errorf("bounded catalogue = %d %+v", code, bounded)
	}

	var view dbx.ObjectDefinition
	if code := getJSON(t, router, "/databases/1/object?kind=view&name=big_orders", &view); code != http.StatusOK ||
		!strings.HasPrefix(view.Definition, "CREATE VIEW big_orders") || view.Source != dbx.DefinitionFromEngine {
		t.Errorf("view = %d %+v", code, view)
	}
	var trigger dbx.ObjectDefinition
	if code := getJSON(t, router, "/databases/1/object?kind=trigger&schema=main&name=orders_touch", &trigger); code != http.StatusOK ||
		trigger.Table != "orders" {
		t.Errorf("trigger = %d %+v", code, trigger)
	}
	for path, want := range map[string]struct {
		status int
		code   string
	}{
		"/databases/1/object?kind=view&name=nope":      {http.StatusNotFound, "not_found"},
		"/databases/1/object?kind=view":                {http.StatusBadRequest, "bad_request"},
		"/databases/1/object?name=big_orders":          {http.StatusBadRequest, "bad_request"},
		"/databases/1/object?kind=sequence&name=x":     {http.StatusBadRequest, "bad_request"},
		"/databases/1/object?kind=table&name=nope":     {http.StatusNotFound, "not_found"},
		"/databases/1/table?schema=main&table=nope":    {http.StatusNotFound, "not_found"},
		"/databases/9/catalog":                         {http.StatusNotFound, "not_found"},
		"/databases/1/object?kind=view&name=a%00b":     {http.StatusBadRequest, "bad_request"},
		"/databases/x/object?kind=view&name=big_order": {http.StatusBadRequest, "bad_request"},
	} {
		rec, body := schemaSend(t, router, http.MethodGet, path, "")
		if rec.Code != want.status || schemaErrCode(body) != want.code {
			t.Errorf("%s = %d %s, want %d %s", path, rec.Code, strings.TrimSpace(rec.Body.String()), want.status, want.code)
		}
	}

	// The table route answers with the whole declaration.
	var detail dbx.TableDetail
	if code := getJSON(t, router, "/databases/1/table?table=customers", &detail); code != http.StatusOK {
		t.Fatalf("table = %d", code)
	}
	if detail.Schema != "main" || detail.Type != dbx.TableTypeTable || detail.Rows != -1 ||
		len(detail.Constraints) != 1 || detail.Constraints[0].Type != dbx.ConstraintUnique ||
		len(detail.ReferencedBy) != 1 || detail.ReferencedBy[0].Table != "orders" ||
		detail.Columns[0].Identity != "autoincrement" || detail.CreateSQLSource != dbx.DefinitionFromEngine || detail.Facts == nil {
		t.Errorf("table = %+v", detail)
	}

	// And the maps and node ids name a table by schema and name together.
	var outline dbx.SchemaOutline
	if code := getJSON(t, router, "/databases/1/outline?limit=2", &outline); code != http.StatusOK ||
		!outline.Truncated || outline.Total != 3 || outline.Limit != 2 || len(outline.Entries) != 2 ||
		outline.Tables["main.big_orders"] == nil {
		t.Errorf("outline = %d %+v", code, outline)
	}
	var relations map[string][]dbx.ForeignKey
	if code := getJSON(t, router, "/databases/1/relations", &relations); code != http.StatusOK ||
		len(relations["main.orders"]) != 1 || relations["main.orders"][0].RefSchema != "main" {
		t.Errorf("relations = %d %+v", code, relations)
	}
	var graph dbx.SchemaGraph
	if code := getJSON(t, router, "/databases/1/graph?limit=2", &graph); code != http.StatusOK ||
		!graph.Truncated || graph.Total != 3 || graph.Limit != 2 || len(graph.Tables) != 2 ||
		graph.Tables[0].ID != "main.customers" || len(graph.Edges) != 1 || graph.Edges[0].From != "main.orders" ||
		graph.Edges[0].To != "main.customers" {
		t.Errorf("graph = %d %+v", code, graph)
	}
	var tables []dbx.Table
	if code := getJSON(t, router, "/databases/1/tables", &tables); code != http.StatusOK || len(tables) != 3 {
		t.Errorf("tables = %d %+v", code, tables)
	}

	// A read-only account reads all of that and changes none of it.
	for _, c := range []confirmCase{
		{http.MethodPost, "/databases/1/ddl/view", `{"name":"v","query":"SELECT 1"}`, ""},
		{http.MethodPost, "/databases/1/ddl/view?preview=1", `{"name":"v","query":"SELECT 1"}`, ""},
		{http.MethodPatch, "/databases/1/ddl/column", `{"table":"customers","name":"name","nullable":false}`, ""},
		{http.MethodPost, "/databases/1/ddl/comment", `{"table":"customers","comment":"x"}`, ""},
		{http.MethodDelete, "/databases/1/ddl/view", `{"name":"big_orders"}`, ""},
	} {
		rec, body := schemaSend(t, router, c.method, c.path, c.body)
		if rec.Code != http.StatusForbidden || schemaErrCode(body) != "forbidden" {
			t.Errorf("a read-only account got %d for %s %s", rec.Code, c.method, c.path)
		}
	}
}

// A change that adds needs service.control; one that removes needs the
// destructive capability; and one whose cost depends on what it is asked for
// is checked against what it is asked for.
func TestStructureChangesAskForTheCapabilityTheirEffectNeeds(t *testing.T) {
	_, router, db := schemaRouter(t, auth.RoleLimited)

	rec, body := schemaSend(t, router, http.MethodPost, "/databases/1/ddl/view",
		`{"schema":"main","name":"named","query":"SELECT id, name FROM customers"}`)
	if rec.Code != http.StatusOK || body["statement"] != "CREATE VIEW \"named\" AS\nSELECT id, name FROM customers" ||
		!schemaHas(t, db, "view", "named") {
		t.Fatalf("create view = %d %s", rec.Code, rec.Body.String())
	}
	// Every drop is in the destructive group, whatever it drops.
	for _, c := range []confirmCase{
		{http.MethodDelete, "/databases/1/ddl/view", `{"name":"named"}`, ""},
		{http.MethodDelete, "/databases/1/ddl/view?preview=1", `{"name":"named"}`, ""},
		{http.MethodDelete, "/databases/1/ddl/constraint", `{"table":"customers","name":"c"}`, ""},
		{http.MethodDelete, "/databases/1/ddl/foreign-key", `{"table":"orders","name":"fk_0"}`, ""},
		{http.MethodDelete, "/databases/1/ddl/schema", `{"name":"reporting"}`, ""},
		{http.MethodDelete, "/databases/1/ddl/table?preview=1", `{"table":"orders"}`, ""},
	} {
		rec, body := schemaSend(t, router, c.method, c.path, c.body)
		if rec.Code != http.StatusForbidden || schemaErrCode(body) != "forbidden" {
			t.Errorf("an account without the destructive capability got %d for %s %s: %s",
				rec.Code, c.method, c.path, strings.TrimSpace(rec.Body.String()))
		}
	}
	if !schemaHas(t, db, "view", "named") {
		t.Fatal("the view was dropped by an account that may not drop")
	}

	// A new type rewrites the column's values. The route cannot know that from
	// its path, so the handler checks the body — and checks it before the
	// engine is asked anything, whether or not the request is only a preview.
	for _, path := range []string{"/databases/1/ddl/column", "/databases/1/ddl/column?preview=1"} {
		rec, body := schemaSend(t, router, http.MethodPatch, path, `{"table":"customers","name":"name","type":"INTEGER"}`)
		if rec.Code != http.StatusForbidden || schemaErrCode(body) != "forbidden" || !strings.Contains(schemaErrMessage(body), "rewrites its values") {
			t.Errorf("a change of type without the destructive capability = %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
	// The same route without a type is not destructive, so it gets as far as
	// the engine — which, being SQLite, says what it cannot do.
	rec, body = schemaSend(t, router, http.MethodPatch, "/databases/1/ddl/column", `{"table":"customers","name":"name","nullable":false}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(schemaErrMessage(body), "SQLite cannot change a column") {
		t.Errorf("a nullability change = %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	for path, c := range map[string]struct{ body, says string }{
		"/databases/1/ddl/foreign-key": {`{"table":"orders","columns":["total"],"refTable":"customers","refColumns":["id"]}`, "SQLite cannot add or drop a foreign key"},
		"/databases/1/ddl/constraint":  {`{"table":"customers","type":"unique","columns":["name"]}`, "A unique index does the same job"},
		"/databases/1/ddl/comment":     {`{"table":"customers","comment":"c"}`, "SQLite stores no comments"},
		"/databases/1/ddl/schema":      {`{"name":"reporting"}`, "SQLite has no schemas"},
		"/databases/1/ddl/enum":        {`{"name":"mood","values":["a"]}`, "SQLite has no enum types"},
		"/databases/1/ddl/enum/value":  {`{"name":"mood","value":"a"}`, "SQLite has no enum types"},
	} {
		rec, body := schemaSend(t, router, http.MethodPost, path, c.body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(schemaErrMessage(body), c.says) {
			t.Errorf("%s = %d %s, want a refusal saying %q", path, rec.Code, strings.TrimSpace(rec.Body.String()), c.says)
		}
	}
}

// SQL the operator writes into a condition or a default is run by the engine
// against rows. A call in it that the form cannot vouch for is arbitrary SQL
// arriving by a cheaper route than the console, so it needs what the console
// would ask for — decided from the body, and decided closed.
func TestSQLInAFormIsHeldToTheConsolesRule(t *testing.T) {
	for _, c := range []struct {
		role   auth.Role
		method string
		path   string
		body   string
		status int
	}{
		// What only computes is an ordinary additive change.
		{auth.RoleLimited, http.MethodPost, "/databases/1/ddl/index?preview=1",
			`{"table":"orders","fields":["customer_id"],"where":"total > abs(0) AND length(total) > 0"}`, http.StatusOK},
		{auth.RoleLimited, http.MethodPost, "/databases/1/ddl/column?preview=1",
			`{"table":"customers","column":{"name":"seen","type":"TEXT","default":"CURRENT_TIMESTAMP"}}`, http.StatusOK},
		// What calls anything else is not, shown or run.
		{auth.RoleLimited, http.MethodPost, "/databases/1/ddl/index?preview=1",
			`{"table":"orders","fields":["customer_id"],"where":"load_extension(total) IS NULL"}`, http.StatusForbidden},
		{auth.RoleLimited, http.MethodPost, "/databases/1/ddl/index",
			`{"table":"orders","fields":["customer_id"],"where":"load_extension(total) IS NULL"}`, http.StatusForbidden},
		{auth.RoleLimited, http.MethodPost, "/databases/1/ddl/column?preview=1",
			`{"table":"customers","column":{"name":"n","type":"TEXT","default":"anything()"}}`, http.StatusForbidden},
		{auth.RoleLimited, http.MethodPost, "/databases/1/ddl/table?preview=1",
			`{"table":"t","columns":[{"name":"n","type":"TEXT","default":"anything()"}]}`, http.StatusForbidden},
		// An account that may run arbitrary SQL may run this too.
		{auth.RoleAdmin, http.MethodPost, "/databases/1/ddl/index?preview=1",
			`{"table":"orders","fields":["customer_id"],"where":"load_extension(total) IS NULL"}`, http.StatusOK},
		{auth.RoleAdmin, http.MethodPost, "/databases/1/ddl/column?preview=1",
			`{"table":"customers","column":{"name":"n","type":"TEXT","default":"anything()"}}`, http.StatusOK},
	} {
		_, router, db := schemaRouter(t, c.role)
		before := schemaText(t, db)
		rec, body := schemaSend(t, router, c.method, c.path, c.body)
		if rec.Code != c.status {
			t.Errorf("%s %s %s %s = %d %s, want %d", c.role, c.method, c.path, c.body, rec.Code,
				strings.TrimSpace(rec.Body.String()), c.status)
		}
		if c.status == http.StatusForbidden && (schemaErrCode(body) != "forbidden" ||
			!strings.Contains(schemaErrMessage(body), "cannot vouch for")) {
			t.Errorf("the refusal does not say why: %s", strings.TrimSpace(rec.Body.String()))
		}
		if after := schemaText(t, db); after != before {
			t.Errorf("%s %s changed the database", c.method, c.path)
		}
		// An account allowed to run it is told what it calls, so the dialog
		// can say so.
		calls, _ := body["calls"].([]any)
		wantCalls := c.role == auth.RoleAdmin
		if c.status == http.StatusOK && (len(calls) == 1) != wantCalls {
			t.Errorf("%s %s %s: calls = %v", c.role, c.method, c.path, body["calls"])
		}
	}
}

// A column type is written into the statement as it is given, so whatever a
// "type" carries after the type is the rest of the statement: `integer, DROP
// COLUMN email` on the add-column form was an ALTER TABLE that added one
// column and dropped another, for an account that is refused the drop on its
// own route and in the console. The form now takes a type and nothing else.
func TestATypeCannotCarryASecondChange(t *testing.T) {
	_, router, db := schemaRouter(t, auth.RoleLimited)
	before := schemaText(t, db)

	// What this account is refused directly …
	rec, body := schemaSend(t, router, http.MethodDelete, "/databases/1/ddl/column", `{"table":"customers","name":"name"}`)
	if rec.Code != http.StatusForbidden || schemaErrCode(body) != "forbidden" {
		t.Fatalf("dropping a column without the destructive capability = %d %s", rec.Code, rec.Body.String())
	}
	// … it is refused inside a type as well, shown or run.
	for _, typ := range []string{
		"TEXT, DROP COLUMN name", "INTEGER, RENAME TO gone", "TEXT DROP TABLE orders", "TEXT) ; DROP TABLE orders",
		"TEXT EXEC('DROP TABLE orders')", "TEXT REFERENCES orders", "TEXT DEFAULT (load_extension('x'))", "check(1)",
	} {
		quoted, err := json.Marshal(typ)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct{ path, body string }{
			{"/databases/1/ddl/column", `{"table":"customers","column":{"name":"x","type":` + string(quoted) + `}}`},
			{"/databases/1/ddl/table", `{"table":"made","columns":[{"name":"x","type":` + string(quoted) + `}]}`},
		} {
			for _, suffix := range []string{"", "?preview=1"} {
				rec, body := schemaSend(t, router, http.MethodPost, c.path+suffix, c.body)
				if rec.Code != http.StatusBadRequest || !strings.Contains(schemaErrMessage(body), "not one this form can build") {
					t.Errorf("POST %s%s with the type %q = %d %s", c.path, suffix, typ, rec.Code, strings.TrimSpace(rec.Body.String()))
				}
			}
		}
	}
	if after := schemaText(t, db); after != before {
		t.Fatalf("a refused type changed the database:\n%s", after)
	}
	// A type that is only a type still goes through.
	rec, body = schemaSend(t, router, http.MethodPost, "/databases/1/ddl/column",
		`{"table":"customers","column":{"name":"x","type":"VARCHAR(40)"}}`)
	if rec.Code != http.StatusOK || body["statement"] != `ALTER TABLE "customers" ADD COLUMN "x" VARCHAR(40)` {
		t.Errorf("a plain add-column = %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}

// A dialog previews as the operator types, and a preview the server could not
// draw up is neither a change nor an attempt at one: it leaves the audit trail
// alone. The same request sent to run is on it, and so is a preview that was
// refused for who asked rather than for what was asked.
func TestARefusedPreviewIsOnTheTrailOnlyWhenItWasRefusedForTheRole(t *testing.T) {
	s, router, _ := schemaRouter(t, auth.RoleLimited)
	audited := func() (n int, last string) {
		t.Helper()
		if err := s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			var action, detail string
			if err := s.Store.DB.QueryRow(`SELECT action, detail FROM audit_log ORDER BY id DESC LIMIT 1`).Scan(&action, &detail); err != nil {
				t.Fatal(err)
			}
			last = action + " " + detail
		}
		return n, last
	}
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/databases/1/ddl/index", `{"table":"orders","fields":["total"],"where":"(total > 0"}`},
		{http.MethodPost, "/databases/1/ddl/index", `{"table":"orders","fields":["total"],"where":"total > 0;"}`},
		{http.MethodPost, "/databases/1/ddl/column", `{"table":"customers","column":{"name":"x","type":"TEXT, DROP COLUMN name"}}`},
		{http.MethodPost, "/databases/1/ddl/view", `{"query":"SELECT 1"}`},
		{http.MethodPost, "/databases/1/ddl/view", `{"name":"v","query":"SELECT 1","cascade":true}`},
		{http.MethodPost, "/databases/1/ddl/rename", `{"table":"orders"}`},
		{http.MethodPatch, "/databases/1/ddl/column", `{"table":"customers","name":"name","nullable":false}`},
		{http.MethodPost, "/databases/1/ddl/comment", `{"comment":"x"}`},
	} {
		rec, _ := schemaSend(t, router, c.method, c.path+"?preview=1", c.body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s?preview=1 %s = %d %s", c.method, c.path, c.body, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
	if n, last := audited(); n != 0 {
		t.Errorf("refused previews left %d audit entries, the last %s", n, last)
	}

	// Run rather than previewed, the same refused request is recorded.
	rec, _ := schemaSend(t, router, http.MethodPost, "/databases/1/ddl/index", `{"table":"orders","fields":["total"],"where":"(total > 0"}`)
	if n, _ := audited(); rec.Code != http.StatusBadRequest || n != 1 {
		t.Errorf("a refused change = %d, %d audit entries", rec.Code, n)
	}

	// A preview refused for the capability it would need is recorded under
	// the change's own name, whether the route or the body decided it.
	for i, c := range []struct{ method, path, body, action, says string }{
		{http.MethodPatch, "/databases/1/ddl/column?preview=1", `{"table":"customers","name":"name","type":"INTEGER"}`,
			"database.ddl.alter_column", `"preview":true`},
		{http.MethodPost, "/databases/1/ddl/index?preview=1", `{"table":"orders","fields":["total"],"where":"load_extension(total) IS NULL"}`,
			"database.ddl.create_index", `"calls":["load_extension"]`},
	} {
		rec, body := schemaSend(t, router, c.method, c.path, c.body)
		n, last := audited()
		if rec.Code != http.StatusForbidden || schemaErrCode(body) != "forbidden" || n != i+2 ||
			!strings.HasPrefix(last, c.action+" ") || !strings.Contains(last, c.says) {
			t.Errorf("%s %s = %d; %d audit entries, the last %s", c.method, c.path, rec.Code, n, last)
		}
	}
}

// Some plans read the catalogue to restate what they do not change. When the
// engine refuses that read the request was not wrong, and answering 400 would
// have a form blame the operator's input for it.
func TestAPlanTheEngineWouldNotBeReadForIsNotABadRequest(t *testing.T) {
	s, _, _ := schemaRouter(t, auth.RoleAdmin)
	conn, _, err := s.dbConnRow(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	principal := &httpx.Principal{User: &auth.User{ID: 1, Username: "operator"}, Role: auth.RoleAdmin, Kind: "session", IP: "127.0.0.1"}
	for _, suffix := range []string{"", "?preview=1"} {
		req := httptest.NewRequest(http.MethodPost, "/databases/1/ddl/comment"+suffix, nil)
		req = req.WithContext(httpx.WithPrincipal(req.Context(), principal))
		err := s.runDDL(httptest.NewRecorder(), req, conn, time.Second, "database.ddl.comment", nil, "",
			func(context.Context, *sql.DB) (*dbx.DDLPlan, error) {
				return nil, fmt.Errorf("could not read the column as it is now: %w", dbx.ErrPlanRead)
			})
		var refused *httpx.APIError
		if !errors.As(err, &refused) || refused.Status != http.StatusBadGateway || refused.Code != "query_failed" {
			t.Errorf("an unreadable catalogue%s = %v", suffix, err)
		}
		err = s.runDDL(httptest.NewRecorder(), req, conn, time.Second, "database.ddl.comment", nil, "",
			func(context.Context, *sql.DB) (*dbx.DDLPlan, error) { return nil, errors.New("no column c on t") })
		if !errors.As(err, &refused) || refused.Status != http.StatusBadRequest {
			t.Errorf("a refused request%s = %v", suffix, err)
		}
	}
}

// With ?preview=1 every schema route answers with the statement it would run
// and runs nothing. That is what lets a confirmation dialog show the server's
// SQL rather than the page's guess at it.
func TestPreviewShowsTheStatementAndRunsNothing(t *testing.T) {
	s, router, db := schemaRouter(t, auth.RoleAdmin)

	cases := []struct {
		method, path, body, statement string
	}{
		{http.MethodPost, "/databases/1/ddl/table", `{"table":"notes","columns":[{"name":"id","type":"INTEGER","primaryKey":true},{"name":"body","type":"TEXT","notNull":true,"default":"''"}]}`,
			"CREATE TABLE \"notes\" (\n  \"id\" INTEGER PRIMARY KEY,\n  \"body\" TEXT DEFAULT '' NOT NULL\n)"},
		{http.MethodPost, "/databases/1/ddl/column", `{"table":"customers","column":{"name":"phone","type":"TEXT"}}`,
			`ALTER TABLE "customers" ADD COLUMN "phone" TEXT`},
		{http.MethodPost, "/databases/1/ddl/index", `{"table":"orders","fields":["total"],"unique":true,"where":"total > 0","ifNotExists":true}`,
			`CREATE UNIQUE INDEX IF NOT EXISTS "orders_total_idx" ON "orders" ("total") WHERE (total > 0)`},
		{http.MethodPost, "/databases/1/ddl/rename", `{"table":"orders","to":"purchases"}`,
			`ALTER TABLE "orders" RENAME TO "purchases"`},
		{http.MethodPost, "/databases/1/ddl/rename", `{"table":"orders","kind":"column","name":"total","to":"amount"}`,
			`ALTER TABLE "orders" RENAME COLUMN "total" TO "amount"`},
		{http.MethodPost, "/databases/1/ddl/view", `{"name":"named","query":"SELECT id FROM customers"}`,
			"CREATE VIEW \"named\" AS\nSELECT id FROM customers"},
		{http.MethodDelete, "/databases/1/ddl/table", `{"table":"orders"}`, `DROP TABLE "orders"`},
		{http.MethodDelete, "/databases/1/ddl/column", `{"table":"customers","name":"name"}`, `ALTER TABLE "customers" DROP COLUMN "name"`},
		{http.MethodDelete, "/databases/1/ddl/index", `{"table":"orders","name":"orders_customer"}`, `DROP INDEX "orders_customer"`},
		{http.MethodPost, "/databases/1/ddl/truncate", `{"table":"customers"}`, `DELETE FROM "customers"`},
		{http.MethodDelete, "/databases/1/ddl/view", `{"name":"big_orders"}`, `DROP VIEW "big_orders"`},
	}
	before := schemaText(t, db)
	for _, c := range cases {
		rec, body := schemaSend(t, router, c.method, c.path+"?preview=1", c.body)
		if rec.Code != http.StatusOK || body["statement"] != c.statement || body["preview"] != true {
			t.Errorf("%s %s previewed as %d %s\nwant %s", c.method, c.path, rec.Code, strings.TrimSpace(rec.Body.String()), c.statement)
			continue
		}
		if list, _ := body["statements"].([]any); len(list) != 1 || list[0] != c.statement {
			t.Errorf("%s %s: statements = %v", c.method, c.path, body["statements"])
		}
	}
	if after := schemaText(t, db); after != before {
		t.Fatalf("a preview changed the database:\n%s\n---\n%s", before, after)
	}
	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM customers`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("a preview emptied a table: %d, %v", rows, err)
	}
	// A preview is not a change, and is not on the trail as one.
	var audited int
	if err := s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&audited); err != nil || audited != 0 {
		t.Errorf("previews left %d audit entries, %v", audited, err)
	}

	// What is shown is what runs: the same request without the flag answers
	// with the same statement, and this time it happened and was recorded.
	for i, c := range []int{0, 1, 2, 5, 10, 8} {
		rec, body := schemaSend(t, router, cases[c].method, cases[c].path, cases[c].body)
		if rec.Code != http.StatusOK || body["statement"] != cases[c].statement || body["preview"] != nil {
			t.Fatalf("%s %s ran as %d %s", cases[c].method, cases[c].path, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
		if err := s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action LIKE 'database.ddl.%' AND success = 1
		  AND target = 'shop' AND detail LIKE '%"statement"%'`).Scan(&audited); err != nil || audited != i+1 {
			t.Errorf("after %s %s the trail holds %d schema changes, %v", cases[c].method, cases[c].path, audited, err)
		}
	}
	if !schemaHas(t, db, "table", "notes") || !schemaHas(t, db, "view", "named") ||
		schemaHas(t, db, "view", "big_orders") || schemaHas(t, db, "index", "orders_customer") ||
		!schemaHas(t, db, "index", "orders_total_idx") {
		t.Errorf("the schema after running:\n%s", schemaText(t, db))
	}

	// A statement the engine refuses is on the trail too, with what was tried.
	rec, _ := schemaSend(t, router, http.MethodDelete, "/databases/1/ddl/table", `{"table":"never_was"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("dropping a table that is not there = %d %s", rec.Code, rec.Body.String())
	}
	var detail string
	if err := s.Store.DB.QueryRow(`SELECT detail FROM audit_log WHERE action = 'database.ddl.drop_table' AND success = 0`).Scan(&detail); err != nil ||
		!strings.Contains(detail, `DROP TABLE \"never_was\"`) {
		t.Errorf("the refused statement on the trail = %q, %v", detail, err)
	}
}

func schemaText(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`SELECT type || ' ' || name || ': ' || COALESCE(sql, '') FROM sqlite_master ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// The new routes follow the rules the old ones are held to: a body with a
// field the server does not know is refused, what a form may build is
// validated before the engine sees it, and none of them asks for a typed
// phrase — that is reserved for dropping a whole database.
func TestStructureRoutesValidateAndDoNotAskForAPhrase(t *testing.T) {
	_, router, db := schemaRouter(t, auth.RoleAdmin)
	before := schemaText(t, db)

	for _, c := range []struct{ method, path, body, says string }{
		{http.MethodPost, "/databases/1/ddl/view", `{"name":"v","query":"SELECT 1","cascade":true}`, "unknown field"},
		{http.MethodPatch, "/databases/1/ddl/column", `{"table":"customers","name":"name","collation":"x"}`, "unknown field"},
		{http.MethodPost, "/databases/1/ddl/view", `{"query":"SELECT 1"}`, "name is required"},
		{http.MethodPost, "/databases/1/ddl/view", `{"name":"v","query":"SELECT 1; DROP TABLE customers"}`, "one SELECT"},
		{http.MethodPost, "/databases/1/ddl/view", `{"name":"v","query":"DELETE FROM customers"}`, "defined by a SELECT"},
		{http.MethodPost, "/databases/1/ddl/view", `{"name":"v\u0000","query":"SELECT 1"}`, "control character"},
		{http.MethodPost, "/databases/1/ddl/index", `{"table":"orders","fields":["total"],"where":"total > 0); DROP TABLE customers; --"}`, "closes a parenthesis"},
		{http.MethodPost, "/databases/1/ddl/index", `{"table":"orders","fields":["total"],"concurrently":true}`, "no CONCURRENTLY"},
		{http.MethodPost, "/databases/1/ddl/index", `{"table":"orders","fields":[]}`, "at least one column"},
		{http.MethodPost, "/databases/1/ddl/column", `{"table":"customers","column":{"name":"x","type":"TEXT REFERENCES orders"}}`, "not one this form can build"},
		// SQLite statements are written unqualified, so one aimed at an
		// attached file would land on main's table of the same name.
		{http.MethodDelete, "/databases/1/ddl/table", `{"schema":"other","table":"orders"}`, "main database only"},
		{http.MethodPost, "/databases/1/ddl/column", `{"schema":"other","table":"customers","column":{"name":"x","type":"TEXT"}}`, "main database only"},
		{http.MethodPatch, "/databases/1/ddl/column", `{"table":"customers"}`, "name are required"},
		{http.MethodDelete, "/databases/1/ddl/constraint", `{"table":"customers"}`, "name are required"},
		{http.MethodDelete, "/databases/1/ddl/foreign-key", `{"name":"fk_0"}`, "name are required"},
		{http.MethodPost, "/databases/1/ddl/comment", `{"comment":"x"}`, "table is required"},
		{http.MethodDelete, "/databases/1/ddl/schema", `{}`, "name is required"},
	} {
		for _, suffix := range []string{"", "?preview=1"} {
			rec, body := schemaSend(t, router, c.method, c.path+suffix, c.body)
			if rec.Code != http.StatusBadRequest || !strings.Contains(schemaErrMessage(body), c.says) {
				t.Errorf("%s %s%s %s = %d %s, want a 400 saying %q", c.method, c.path, suffix, c.body,
					rec.Code, strings.TrimSpace(rec.Body.String()), c.says)
			}
		}
	}
	if after := schemaText(t, db); after != before {
		t.Fatalf("a refused request changed the database:\n%s", after)
	}

	for _, c := range []confirmCase{
		{http.MethodPatch, "/databases/1/ddl/column", `{"table":"customers","name":"name","type":"INTEGER"}`, "a column's type is changed back the way it was changed"},
		{http.MethodDelete, "/databases/1/ddl/foreign-key", `{"table":"orders","name":"fk_0"}`, "a constraint is re-added from its definition"},
		{http.MethodDelete, "/databases/1/ddl/constraint", `{"table":"customers","name":"c"}`, "a constraint is re-added from its definition"},
		{http.MethodDelete, "/databases/1/ddl/view", `{"name":"big_orders"}`, "a view holds no rows"},
		{http.MethodDelete, "/databases/1/ddl/schema", `{"name":"reporting"}`, "only an empty schema can be dropped"},
		{http.MethodPost, "/databases/1/ddl/view", `{"name":"big_orders","query":"SELECT 1","replace":true}`, "replacing a view discards a definition, not data"},
	} {
		rec := driveWithoutConfirmation(t, router, c)
		if strings.Contains(rec.Body.String(), "confirmation") || rec.Code == http.StatusPreconditionRequired {
			t.Errorf("%s %s asked for a phrase, but %s: %d %s", c.method, c.path, c.why, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
}

// An engine with no schema to edit is refused by name, on the reads and the
// writes alike, without being dialled.
func TestSchemaRoutesAreForSQLEngines(t *testing.T) {
	s, router, _ := schemaRouter(t, auth.RoleAdmin)
	for _, driver := range []dbx.Driver{dbx.DriverRedis, dbx.DriverMongo} {
		sealed, err := s.Sealer.Seal(string(driver) + "://127.0.0.1:1/0")
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,0)`,
			"other-"+string(driver), string(driver), sealed)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		for _, c := range []confirmCase{
			{http.MethodGet, "/databases/%d/catalog", "", ""},
			{http.MethodGet, "/databases/%d/object?kind=view&name=v", "", ""},
			{http.MethodPost, "/databases/%d/ddl/view", `{"name":"v","query":"SELECT 1"}`, ""},
			{http.MethodPost, "/databases/%d/ddl/view?preview=1", `{"name":"v","query":"SELECT 1"}`, ""},
			{http.MethodDelete, "/databases/%d/ddl/schema", `{"name":"s"}`, ""},
			{http.MethodPatch, "/databases/%d/ddl/column", `{"table":"t","name":"c","nullable":true}`, ""},
		} {
			rec, body := schemaSend(t, router, c.method, pathf(c.path, id), c.body)
			if rec.Code != http.StatusBadRequest || !strings.Contains(schemaErrMessage(body), "SQL engines") {
				t.Errorf("%s %s %s = %d %s", driver, c.method, c.path, rec.Code, strings.TrimSpace(rec.Body.String()))
			}
		}
	}
}

// TestLiveAPISchemaSurface drives the schema routes end to end against a real
// Postgres: a schema is built through the forms, read back through the
// catalogue, and changed with the statement the preview showed.
func TestLiveAPISchemaSurface(t *testing.T) {
	// It creates and drops a schema, so it runs only against a server it was
	// pointed at and never against whatever answers on the default port.
	dsn := os.Getenv("JD_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set JD_TEST_POSTGRES_DSN to run this")
	}
	_, r, id := liveAPIRouter(t, dbx.DriverPostgres, dsn)
	const schema = "jd_api_schema"
	path := func(suffix string) string { return pathf("/databases/%d", id) + suffix }
	reset := func() {
		do(t, r, http.MethodPost, path("/query"), `{"query":"DROP SCHEMA IF EXISTS `+schema+` CASCADE"}`)
	}
	reset()
	t.Cleanup(reset)
	send := func(method, suffix, body string) map[string]any {
		t.Helper()
		rec := do(t, r, method, path(suffix), body)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, suffix, rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s %s: %v in %s", method, suffix, err, rec.Body.String())
		}
		return out
	}

	send(http.MethodPost, "/ddl/schema", `{"name":"`+schema+`"}`)
	send(http.MethodPost, "/ddl/enum", `{"schema":"`+schema+`","name":"state","values":["new","done"]}`)
	send(http.MethodPost, "/ddl/table", `{"schema":"`+schema+`","table":"owners","columns":[
		{"name":"id","type":"integer","primaryKey":true,"notNull":true},{"name":"name","type":"text"}]}`)
	send(http.MethodPost, "/ddl/table", `{"schema":"`+schema+`","table":"tasks","columns":[
		{"name":"id","type":"integer","primaryKey":true,"notNull":true},
		{"name":"owner_id","type":"integer"},
		{"name":"state","type":"`+schema+`.state","notNull":true,"default":"'new'"},
		{"name":"effort","type":"text"}]}`)
	fk := send(http.MethodPost, "/ddl/foreign-key", `{"schema":"`+schema+`","table":"tasks","columns":["owner_id"],
		"refTable":"owners","refColumns":["id"],"onDelete":"CASCADE"}`)
	if !strings.Contains(fk["statement"].(string), `ADD CONSTRAINT "tasks_owner_id_fkey" FOREIGN KEY ("owner_id")`) {
		t.Errorf("foreign key statement = %v", fk["statement"])
	}
	send(http.MethodPost, "/ddl/constraint", `{"schema":"`+schema+`","table":"tasks","type":"check","name":"effort_known","expression":"id > 0"}`)
	send(http.MethodPost, "/ddl/comment", `{"schema":"`+schema+`","table":"tasks","comment":"What is left to do"}`)
	send(http.MethodPost, "/ddl/comment", `{"schema":"`+schema+`","table":"tasks","column":"effort","comment":"In hours"}`)
	send(http.MethodPost, "/ddl/view", `{"schema":"`+schema+`","name":"open_tasks","query":"SELECT id FROM `+schema+`.tasks WHERE state = 'new'"}`)
	send(http.MethodPost, "/ddl/index", `{"schema":"`+schema+`","table":"tasks","fields":["owner_id"],"method":"hash","ifNotExists":true}`)
	send(http.MethodPost, "/ddl/enum/value", `{"schema":"`+schema+`","name":"state","value":"doing","before":"done"}`)

	t.Run("catalogue", func(t *testing.T) {
		var catalog dbx.Catalog
		if code := getJSON(t, r, path("/catalog?schema="+schema), &catalog); code != http.StatusOK {
			t.Fatalf("catalogue = %d", code)
		}
		if catalog.Schema != schema || catalog.DefaultSchema != "public" || len(catalog.Objects[dbx.GroupTables]) != 2 ||
			len(catalog.Objects[dbx.GroupViews]) != 1 || len(catalog.Objects[dbx.GroupTypes]) != 1 ||
			len(catalog.Objects[dbx.GroupTypes][0].Values) != 3 || catalog.Errors != nil {
			t.Errorf("catalogue = %+v", catalog)
		}
		var enum dbx.ObjectDefinition
		if code := getJSON(t, r, path("/object?kind=enum&schema="+schema+"&name=state"), &enum); code != http.StatusOK ||
			strings.Join(enum.Values, ",") != "new,doing,done" || enum.Source != dbx.DefinitionGenerated {
			t.Errorf("enum = %d %+v", code, enum)
		}
		var view dbx.ObjectDefinition
		if code := getJSON(t, r, path("/object?kind=view&schema="+schema+"&name=open_tasks"), &view); code != http.StatusOK ||
			!strings.Contains(view.Definition, "CREATE OR REPLACE VIEW") {
			t.Errorf("view = %d %+v", code, view)
		}
		rec := do(t, r, http.MethodGet, path("/object?kind=function&schema="+schema+"&name=nope"), "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("a function that is not there = %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("table", func(t *testing.T) {
		var detail dbx.TableDetail
		if code := getJSON(t, r, path("/table?schema="+schema+"&table=tasks"), &detail); code != http.StatusOK {
			t.Fatalf("table = %d", code)
		}
		byName := map[string]dbx.Column{}
		for _, c := range detail.Columns {
			byName[c.Name] = c
		}
		if detail.Comment != "What is left to do" || byName["effort"].Comment != "In hours" ||
			byName["state"].TypeKind != "enum" || strings.Join(byName["state"].EnumValues, ",") != "new,doing,done" ||
			len(detail.Constraints) != 1 || detail.Constraints[0].Name != "effort_known" ||
			len(detail.ForeignKeys) != 1 || detail.CreateSQLSource != dbx.DefinitionGenerated ||
			!strings.Contains(detail.CreateSQL, "USING hash (owner_id)") {
			t.Errorf("table = %+v", detail)
		}
		var owners dbx.TableDetail
		if code := getJSON(t, r, path("/table?schema="+schema+"&table=owners"), &owners); code != http.StatusOK ||
			len(owners.ReferencedBy) != 1 || owners.ReferencedBy[0].Table != "tasks" || owners.ReferencedBy[0].OnDelete != "CASCADE" {
			t.Errorf("owners = %d %+v", code, owners)
		}
	})

	t.Run("alter_column_runs_what_the_preview_showed", func(t *testing.T) {
		body := `{"schema":"` + schema + `","table":"tasks","name":"effort","type":"integer","using":"effort::integer","nullable":false,"default":"1"}`
		do(t, r, http.MethodPost, path("/query"), `{"query":"INSERT INTO `+schema+`.owners VALUES (1, 'Ann')"}`)
		do(t, r, http.MethodPost, path("/query"), `{"query":"INSERT INTO `+schema+`.tasks(id, owner_id, effort) VALUES (1, 1, '3')"}`)
		preview := send(http.MethodPatch, "/ddl/column?preview=1", body)
		want := `ALTER TABLE "` + schema + `"."tasks" ALTER COLUMN "effort" TYPE integer USING (effort::integer), ` +
			`ALTER COLUMN "effort" SET DEFAULT 1, ALTER COLUMN "effort" SET NOT NULL`
		if preview["statement"] != want || preview["preview"] != true {
			t.Fatalf("preview = %v", preview)
		}
		var before dbx.TableDetail
		getJSON(t, r, path("/table?schema="+schema+"&table=tasks"), &before)
		for _, c := range before.Columns {
			if c.Name == "effort" && c.Type != "text" {
				t.Fatalf("the preview changed the column: %+v", c)
			}
		}
		ran := send(http.MethodPatch, "/ddl/column", body)
		if ran["statement"] != want {
			t.Errorf("ran %v, previewed %v", ran["statement"], want)
		}
		var after dbx.TableDetail
		getJSON(t, r, path("/table?schema="+schema+"&table=tasks"), &after)
		for _, c := range after.Columns {
			if c.Name == "effort" && (c.Type != "integer" || c.Nullable || c.Default != "1") {
				t.Errorf("effort after the change = %+v", c)
			}
		}
	})

	// On Postgres a comma after the type starts another action of the same
	// ALTER TABLE, so this is the request that used to drop a column from the
	// add-column form.
	t.Run("a_type_carries_no_second_action", func(t *testing.T) {
		for _, suffix := range []string{"?preview=1", ""} {
			rec := do(t, r, http.MethodPost, path("/ddl/column"+suffix),
				`{"schema":"`+schema+`","table":"owners","column":{"name":"x","type":"integer, DROP COLUMN name"}}`)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "not one this form can build") {
				t.Errorf("add column%s with a second action in its type = %d %s", suffix, rec.Code, rec.Body.String())
			}
		}
		var owners dbx.TableDetail
		if code := getJSON(t, r, path("/table?schema="+schema+"&table=owners"), &owners); code != http.StatusOK || len(owners.Columns) != 2 {
			t.Errorf("owners after the refused requests = %d %+v", code, owners.Columns)
		}
	})

	t.Run("drops", func(t *testing.T) {
		send(http.MethodDelete, "/ddl/constraint", `{"schema":"`+schema+`","table":"tasks","name":"effort_known"}`)
		send(http.MethodDelete, "/ddl/foreign-key", `{"schema":"`+schema+`","table":"tasks","name":"tasks_owner_id_fkey"}`)
		send(http.MethodDelete, "/ddl/view", `{"schema":"`+schema+`","name":"open_tasks"}`)
		// A schema that still holds tables is the engine's to refuse.
		rec := do(t, r, http.MethodDelete, path("/ddl/schema"), `{"name":"`+schema+`"}`)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "depend") {
			t.Errorf("dropping a schema that holds tables = %d %s", rec.Code, rec.Body.String())
		}
		var graph dbx.SchemaGraph
		if code := getJSON(t, r, path("/graph?schema="+schema), &graph); code != http.StatusOK ||
			len(graph.Tables) != 2 || len(graph.Edges) != 0 || graph.Tables[0].ID != schema+".owners" {
			t.Errorf("graph = %d %+v", code, graph)
		}
	})
}
