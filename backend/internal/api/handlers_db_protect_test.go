package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// The routes that stay open on a protected connection, written out by hand a
// second time. A test that read them from protectedRoutesAllowed would pass
// just as happily the day somebody added the wrong one to it; this way the
// list in the handler and the list here have to be changed together, by
// somebody who then has to say in a review why a route that changes a
// database belongs on it.
var protectedStaysOpen = map[string]bool{
	"PUT ":                  true,
	"DELETE ":               true,
	"PUT /access":           true,
	"POST /power":           true,
	"POST /backup":          true,
	"POST /queries":         true,
	"DELETE /queries/{qid}": true,
	"PUT /diagram":          true,
	"DELETE /diagram":       true,
	"POST /classify":        true,
	"POST /orm":             true,
	"POST /rows/sql":        true,
	"POST /explain":         true,
	"POST /query":           true,
	"POST /aggregate":       true,
}

// protectedNotYetRouted are entries the allowlist carries for routes the
// workbench adds — editing a saved query, cancelling one's own running
// query, a script of statements. Each is removed from here when its route
// arrives, at which point the test above starts holding it to the list.
var protectedNotYetRouted = map[string]bool{
	"PUT /queries/{qid}": true,
	"POST /query/cancel": true,
	"POST /script":       true,
}

const connectionRoutePrefix = "/api/v1/databases/{id}"

// connectionRoutes is every route under /databases/{id} that is not a read,
// taken from the real router.
func connectionRoutes(t *testing.T, s *Server) []route {
	t.Helper()
	var out []route
	for _, rt := range apiRoutes(t, s.Routes()) {
		if !strings.HasPrefix(rt.pattern, connectionRoutePrefix) {
			continue
		}
		rest := strings.TrimPrefix(rt.pattern, connectionRoutePrefix)
		if rest != "" && !strings.HasPrefix(rest, "/") {
			continue
		}
		switch rt.method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			continue
		}
		out = append(out, rt)
	}
	if len(out) < 40 {
		t.Fatalf("only %d mutating routes found under /databases/{id}; the walk is not seeing the real router", len(out))
	}
	return out
}

func protectTestConnection(t *testing.T, s *Server, name string, readOnly bool) int64 {
	t.Helper()
	sealed, err := s.Sealer.Seal(s.Cfg.FileRoots[0] + "/" + name + ".db")
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Store.DB.Exec(
		`INSERT INTO db_connections(name, driver, dsn_enc, created_at, read_only) VALUES(?,?,?,?,?)`,
		name, "sqlite", sealed, 0, readOnly)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

// refusal is why the protection refuses a request, or "" when it lets it by.
func refusal(t *testing.T, s *Server, method, path, body string) string {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	err := s.refuseOnProtected(req)
	if err == nil {
		return ""
	}
	var apiErr *httpx.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict || apiErr.Code != "connection_read_only" {
		t.Fatalf("%s %s was refused with something other than 409 connection_read_only: %v", method, path, err)
	}
	return apiErr.Message
}

// TestProtectionCoversEveryMutatingRoute walks the real router. Every route
// that is not a read is either on the hand-written list above or refused on a
// protected connection — including the ones nobody has written yet, which is
// the property the middleware exists for: a mutating route added to any
// database file tomorrow is refused until it is put on both lists.
func TestProtectionCoversEveryMutatingRoute(t *testing.T) {
	s := testServer(t)
	protected := protectTestConnection(t, s, "protected", true)
	open := protectTestConnection(t, s, "open", false)

	seen := map[string]bool{}
	for _, rt := range connectionRoutes(t, s) {
		key := rt.method + " " + strings.TrimPrefix(rt.pattern, connectionRoutePrefix)
		seen[key] = true
		rest := strings.TrimPrefix(rt.path, "/api/v1/databases/1")

		_, allowed := protectedRule(rt.method, rest)
		if allowed != protectedStaysOpen[key] {
			t.Errorf("%s: allowed on a protected connection = %v, the list in this test says %v", key, allowed, protectedStaysOpen[key])
		}
		// The same answer from the middleware's own entry point, for a route
		// with nothing to read in its body.
		if rule, _ := protectedRule(rt.method, rest); rule.check == nil {
			refused := refusal(t, s, rt.method, pathf("/api/v1/databases/%d", protected)+rest, `{}`) != ""
			if refused == allowed {
				t.Errorf("%s: refused = %v on a protected connection", key, refused)
			}
		}
		// And protection is the connection's, not the route's.
		if why := refusal(t, s, rt.method, pathf("/api/v1/databases/%d", open)+rest, `{}`); why != "" {
			t.Errorf("%s was refused on a connection that is not protected: %s", key, why)
		}
		if why := refusal(t, s, rt.method, "/api/v1/databases/999"+rest, `{}`); why != "" {
			t.Errorf("%s was refused for a connection that does not exist: %s", key, why)
		}
	}

	// Neither list may outlive the routes it names.
	for key := range protectedStaysOpen {
		if !seen[key] {
			t.Errorf("%q is on the test's list and is not a route", key)
		}
	}
	var listed []string
	for _, rule := range protectedRoutesAllowed {
		key := rule.method + " " + rule.pattern
		listed = append(listed, key)
		switch {
		case protectedStaysOpen[key] && protectedNotYetRouted[key]:
			t.Errorf("%q is routed now; take it off protectedNotYetRouted", key)
		case !protectedStaysOpen[key] && !protectedNotYetRouted[key]:
			t.Errorf("%q is allowed on a protected connection and is on neither list in this test", key)
		case protectedNotYetRouted[key] && seen[key]:
			t.Errorf("%q has a route now; move it to protectedStaysOpen", key)
		}
	}
	sort.Strings(listed)
	for i := 1; i < len(listed); i++ {
		if listed[i] == listed[i-1] {
			t.Errorf("%q is listed twice", listed[i])
		}
	}
}

// The same promise through the whole stack — allowlist, session, CSRF, audit
// — rather than through the function: every route the list does not name
// answers 409 on a protected connection, for an administrator, before its
// handler or its capability check runs. And the refusal is on the audit
// trail, like any other mutation that was turned away.
func TestProtectedConnectionRefusesWritesOverHTTP(t *testing.T) {
	s := testServer(t)
	id := protectTestConnection(t, s, "protected", true)
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}

	refused := 0
	for _, rt := range connectionRoutes(t, s) {
		key := rt.method + " " + strings.TrimPrefix(rt.pattern, connectionRoutePrefix)
		if protectedStaysOpen[key] {
			continue
		}
		path := pathf("/api/v1/databases/%d", id) + strings.TrimPrefix(rt.path, "/api/v1/databases/1")
		rec := c.do(rt.method, path, `{}`, nil)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "connection_read_only") {
			t.Errorf("%s = %d %s, want 409 connection_read_only", key, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
		refused++
	}
	if refused < 30 {
		t.Fatalf("only %d routes were exercised", refused)
	}
	var audited int
	if err := s.Store.DB.QueryRow(
		`SELECT COUNT(*) FROM audit_log WHERE status = 409 AND detail LIKE '%protected%'`).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != refused {
		t.Errorf("%d refusals, %d audit entries saying why", refused, audited)
	}
}

// A statement route is a read or a write by what it carries, and the body the
// check read is the body the handler then decodes.
func TestProtectedConnectionRunsOnlyReads(t *testing.T) {
	s, router := dbTestRouter(t)
	if _, err := s.Store.DB.Exec(`UPDATE db_connections SET read_only = 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}

	rec := do(t, router, http.MethodPost, "/databases/1/query", `{"query":"select 41 + 1 as answer"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "answer") {
		t.Fatalf("a read on a protected connection = %d %s", rec.Code, rec.Body.String())
	}
	for _, statement := range []string{
		"create table t (id integer)",
		"insert into t values (1)",
		"update t set id = 2 where id = 1",
		"delete from t where id = 1",
		"drop table t",
		"vacuum",
		"pragma journal_mode = wal",
		"select 1; delete from t",
		"with gone as (delete from t returning *) select * from gone",
	} {
		rec := do(t, router, http.MethodPost, "/databases/1/query", `{"query":"`+statement+`"}`)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "connection_read_only") {
			t.Errorf("%q on a protected connection = %d %s", statement, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}

	// Taking the protection off is the one edit a protected connection takes,
	// and it is what lets the write through afterwards.
	if rec := do(t, router, http.MethodPut, "/databases/1", `{"readOnly":false}`); rec.Code != http.StatusOK {
		t.Fatalf("turning protection off = %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, router, http.MethodPost, "/databases/1/query", `{"query":"create table t (id integer)"}`); rec.Code != http.StatusOK {
		t.Errorf("a write after protection was turned off = %d %s", rec.Code, rec.Body.String())
	}
}

func TestProtectedBodiesAreReadAndFailClosed(t *testing.T) {
	s := testServer(t)
	id := protectTestConnection(t, s, "protected", true)
	base := pathf("/api/v1/databases/%d", id)

	for _, c := range []struct {
		name, path, body string
		refused          bool
	}{
		{"a read", "/query", `{"query":"select * from orders where id = 7","maxRows":50}`, false},
		{"a read with the options the runner takes", "/query", `{"query":"select 1","maxRows":10,"queryId":"q-1"}`, false},
		{"a write", "/query", `{"query":"update orders set paid = true where id = 7"}`, true},
		{"a write hidden behind a comment", "/query", `{"query":"select 1 /* */ ; drop table orders"}`, true},
		{"no statement at all", "/query", `{"maxRows":10}`, true},
		{"a statement that is not text", "/query", `{"query":["select 1"],"sql":7}`, true},
		{"text under a name the check does not know", "/query", `{"query":"select 1","then":"drop table orders"}`, true},
		{"a list under a name the check does not know", "/query", `{"query":"select 1","also":["drop table orders"]}`, true},
		{"an object under a name the check does not know", "/query", `{"query":"select 1","next":{"sql":"drop table orders"}}`, true},
		{"not JSON", "/query", `select 1`, true},
		{"an empty body", "/query", ``, true},

		{"a script of reads", "/script", `{"statements":["select 1","select 2"],"transaction":true}`, false},
		{"a script of reads given as objects", "/script", `{"statements":[{"sql":"select 1"},{"query":"show tables"}]}`, false},
		{"a script as one text", "/script", `{"sql":"select 1; select 2"}`, false},
		{"a script with one write in it", "/script", `{"statements":["select 1","delete from orders where id = 1"]}`, true},
		{"a script as one text with a write", "/script", `{"sql":"select 1; truncate orders"}`, true},
		{"a script entry that names no statement", "/script", `{"statements":[{"text":"select 1"}]}`, true},

		{"a plan that is only planned", "/explain", `{"query":"delete from orders"}`, false},
		{"a plan with analyze off", "/explain", `{"query":"delete from orders","analyze":false,"format":"json"}`, false},
		{"a plan that runs a read", "/explain", `{"query":"select * from orders","analyze":true}`, false},
		{"a plan that runs a write", "/explain", `{"query":"delete from orders","analyze":true}`, true},
		{"analyze spelled as anything but false", "/explain", `{"query":"delete from orders","analyze":"yes"}`, true},

		{"a pipeline that reads", "/aggregate", `{"collection":"orders","pipeline":"[{\"$match\":{\"paid\":true}},{\"$group\":{\"_id\":\"$city\",\"n\":{\"$sum\":1}}}]"}`, false},
		{"$mergeObjects is an expression, not a stage that writes", "/aggregate", `{"collection":"o","pipeline":"[{\"$replaceRoot\":{\"newRoot\":{\"$mergeObjects\":[\"$a\",\"$b\"]}}}]"}`, false},
		{"a pipeline that writes with $out", "/aggregate", `{"collection":"orders","pipeline":"[{\"$match\":{}},{\"$out\":\"copy\"}]"}`, true},
		{"a pipeline that writes with $merge", "/aggregate", `{"collection":"orders","pipeline":"[{\"$merge\":{\"into\":\"copy\"}}]"}`, true},
		{"a stage name spelled with an escape", "/aggregate", `{"collection":"orders","pipeline":"[{\"\\u0024out\":\"copy\"}]"}`, true},
		{"a writing stage inside a sub-pipeline", "/aggregate", `{"collection":"o","pipeline":"[{\"$facet\":{\"a\":[{\"$out\":\"copy\"}]}}]"}`, true},
		{"a pipeline given as a value rather than as text", "/aggregate", `{"collection":"o","pipeline":[{"$out":"copy"}]}`, true},
		{"a pipeline that is not JSON", "/aggregate", `{"collection":"o","pipeline":"[{$out: 'copy'}]"}`, true},
		{"no pipeline", "/aggregate", `{"collection":"orders"}`, true},
	} {
		why := refusal(t, s, http.MethodPost, base+c.path, c.body)
		if (why != "") != c.refused {
			t.Errorf("%s (%s %s): refused = %v (%q), want %v", c.name, c.path, c.body, why != "", why, c.refused)
		}
	}
}

// What the protection reads as "which connection, asked what" has to be what
// the router reads, or a path the two disagree about is a way past it.
func TestConnectionRouteReadsAPathAsTheRouterDoes(t *testing.T) {
	for path, want := range map[string]struct {
		id   int64
		rest string
		ok   bool
	}{
		"/12":                         {12, "", true},
		"/12/":                        {12, "/", true},
		"/12/query":                   {12, "/query", true},
		"/12/queries/5":               {12, "/queries/5", true},
		"/+12/query":                  {12, "/query", true},
		"/012/query":                  {12, "/query", true},
		"/":                           {0, "", false},
		"/fleet":                      {0, "", false},
		"/provision":                  {0, "", false},
		"/%31%32/query":               {0, "", false},
		"/12abc/query":                {0, "", false},
		"/host/grant":                 {0, "", false},
		"/1.0/query":                  {0, "", false},
		"/99999999999999999999/query": {0, "", false},
	} {
		id, rest, ok := connectionRoute(path)
		if id != want.id || rest != want.rest || ok != want.ok {
			t.Errorf("connectionRoute(%q) = %d, %q, %v; want %d, %q, %v", path, id, rest, ok, want.id, want.rest, want.ok)
		}
	}
	for _, c := range []struct {
		pattern, path string
		match         bool
	}{
		{"", "", true},
		{"", "/", false},
		{"/query", "/query", true},
		{"/query", "/query/", false},
		{"/query", "/query/cancel", false},
		{"/queries/{qid}", "/queries/5", true},
		{"/queries/{qid}", "/queries/", false},
		{"/queries/{qid}", "/queries/5/run", false},
		{"/diagram", "/Diagram", false},
	} {
		if got := routeMatches(c.pattern, c.path); got != c.match {
			t.Errorf("routeMatches(%q, %q) = %v, want %v", c.pattern, c.path, got, c.match)
		}
	}
}

// The section's own routes concern no connection, and a protected one
// elsewhere in the table must not get in their way.
func TestProtectionLeavesSectionRoutesAlone(t *testing.T) {
	s := testServer(t)
	protectTestConnection(t, s, "protected", true)
	for _, path := range []string{"/", "/test", "/sync", "/adopt", "/host", "/host/grant", "/provision"} {
		if why := refusal(t, s, http.MethodPost, "/api/v1/databases"+path, `{}`); why != "" {
			t.Errorf("POST /databases%s was refused: %s", path, why)
		}
	}
}
