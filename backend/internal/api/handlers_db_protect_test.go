package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// What a protected connection does with a route that is not a read.
const (
	// staysOpen: let through whatever it carries. It does not change the
	// database.
	staysOpen = "open"
	// byContent: a read or a write by what the body carries; the check reads
	// it and lets the read through.
	byContent = "by content"
	// previewOnly: refused, and let through with ?preview=1, which shows the
	// statement and runs nothing.
	previewOnly = "preview only"
	// isRefused: changes data, schema, accounts or the server. Refused.
	isRefused = "refused"
)

// Every route under /databases/{id} that is not a read, and what a protected
// connection does with it, written out by hand a second time. A test that
// read the answers from protectedRoutesAllowed would pass just as happily the
// day somebody added the wrong route to it; this way the list in the handler
// and the list here have to be changed together, by somebody who then has to
// say in a review why a route that changes a database belongs on it. And a
// route that is on neither fails the test below: it is refused all the same,
// and somebody still has to decide that it should be.
var protectedRouteVerdicts = map[string]string{
	// The connection's own record, its server's reach and its power.
	"PUT ":          staysOpen,
	"DELETE ":       staysOpen,
	"PUT /access":   staysOpen,
	"POST /power":   staysOpen,
	"PUT /settings": isRefused,
	// Dumps are files on this machine: taking one, adding one made elsewhere
	// and deleting one. Loading one back, importing a file and copying into a
	// new database write to a server.
	"POST /backup":         staysOpen,
	"POST /backups/upload": staysOpen,
	"DELETE /backups":      staysOpen,
	"POST /restore":        isRefused,
	"POST /copy":           isRefused,
	"POST /import":         isRefused,
	"POST /import/upload":  isRefused,
	"DELETE /database":     isRefused,
	// The dashboard's own state about the connection.
	"POST /queries":         staysOpen,
	"PUT /queries/{qid}":    staysOpen,
	"DELETE /queries/{qid}": staysOpen,
	"PUT /diagram":          staysOpen,
	"DELETE /diagram":       staysOpen,
	// The query runner and what reads through a POST.
	"POST /classify":     staysOpen,
	"POST /orm":          staysOpen,
	"POST /rows/sql":     staysOpen,
	"POST /query/cancel": staysOpen,
	"POST /explain":      byContent,
	"POST /query":        byContent,
	"POST /script":       byContent,
	"POST /export/query": byContent,
	// Rows: a change set can be shown as its statements and not applied.
	"POST /changes": byContent,
	"POST /rows":    isRefused,
	"PATCH /rows":   isRefused,
	"DELETE /rows":  isRefused,
	// Structure: every form can show its statement and run none.
	"POST /ddl/table":         previewOnly,
	"DELETE /ddl/table":       previewOnly,
	"POST /ddl/rename":        previewOnly,
	"POST /ddl/truncate":      previewOnly,
	"POST /ddl/column":        previewOnly,
	"PATCH /ddl/column":       previewOnly,
	"DELETE /ddl/column":      previewOnly,
	"POST /ddl/index":         previewOnly,
	"DELETE /ddl/index":       previewOnly,
	"POST /ddl/foreign-key":   previewOnly,
	"DELETE /ddl/foreign-key": previewOnly,
	"POST /ddl/constraint":    previewOnly,
	"DELETE /ddl/constraint":  previewOnly,
	"POST /ddl/view":          previewOnly,
	"DELETE /ddl/view":        previewOnly,
	"POST /ddl/schema":        previewOnly,
	"DELETE /ddl/schema":      previewOnly,
	"POST /ddl/comment":       previewOnly,
	"POST /ddl/enum":          previewOnly,
	"POST /ddl/enum/value":    previewOnly,
	// Watching a server: stopping work in flight stays possible, and a
	// consistency check; everything that changes the server does not.
	"POST /activity/cancel":  staysOpen,
	"POST /activity/kill":    staysOpen,
	"POST /maintenance":      byContent,
	"POST /statements/reset": isRefused,
	// Accounts, databases and extensions on the server.
	"POST /server/roles":                          isRefused,
	"PUT /server/roles/{name}":                    isRefused,
	"DELETE /server/roles/{name}":                 isRefused,
	"POST /server/roles/{name}/grant":             isRefused,
	"POST /server/roles/{name}/privileges":        isRefused,
	"POST /server/roles/{name}/privileges/revoke": isRefused,
	"POST /server/databases":                      isRefused,
	"POST /server/databases/connect":              isRefused,
	"POST /server/extensions":                     isRefused,
	"DELETE /server/extensions/{name}":            isRefused,
	// Redis: the console runs a read, the bulk action counts, a client can be
	// disconnected. Keys, streams, accounts and configuration are writes.
	"POST /redis/classify":       staysOpen,
	"POST /redis/command":        byContent,
	"POST /keys/bulk":            byContent,
	"POST /redis/clients/kill":   staysOpen,
	"POST /keys/value":           isRefused,
	"POST /keys/expire":          isRefused,
	"POST /keys/persist":         isRefused,
	"POST /keys/rename":          isRefused,
	"POST /keys/copy":            isRefused,
	"DELETE /keys":               isRefused,
	"POST /keys/stream/groups":   isRefused,
	"DELETE /keys/stream/groups": isRefused,
	"POST /keys/stream/ack":      isRefused,
	"POST /keys/stream/trim":     isRefused,
	"POST /redis/publish":        isRefused,
	"POST /redis/save":           isRefused,
	"POST /redis/slowlog/reset":  isRefused,
	"PUT /redis/config":          isRefused,
	"PUT /redis/acl/{name}":      isRefused,
	"DELETE /redis/acl/{name}":   isRefused,
	// MongoDB: the reads that are POSTs because a filter is a document, the
	// console and the pipeline when they read, an update or a delete that
	// only counts, and stopping an operation.
	"POST /mongo/find":               staysOpen,
	"POST /mongo/count":              staysOpen,
	"POST /mongo/document":           staysOpen,
	"POST /mongo/explain":            staysOpen,
	"POST /mongo/aggregate/preview":  staysOpen,
	"POST /mongo/schema":             staysOpen,
	"POST /mongo/validation/check":   staysOpen,
	"POST /mongo/command/classify":   staysOpen,
	"POST /mongo/killop":             staysOpen,
	"POST /aggregate":                byContent,
	"POST /mongo/command":            byContent,
	"PATCH /mongo/documents":         byContent,
	"DELETE /mongo/documents":        byContent,
	"POST /mongo/documents":          isRefused,
	"PUT /mongo/documents":           isRefused,
	"POST /mongo/documents/clone":    isRefused,
	"POST /mongo/collections":        isRefused,
	"PATCH /mongo/collections":       isRefused,
	"DELETE /mongo/collections":      isRefused,
	"POST /mongo/collections/rename": isRefused,
	"POST /mongo/indexes":            isRefused,
	"PATCH /mongo/indexes":           isRefused,
	"DELETE /mongo/indexes":          isRefused,
	"PUT /mongo/validation":          isRefused,
	"PUT /mongo/profiler":            isRefused,
	"POST /mongo/users":              isRefused,
	"PUT /mongo/users":               isRefused,
	"DELETE /mongo/users":            isRefused,
	"POST /mongo/users/grant":        isRefused,
	"POST /mongo/users/revoke":       isRefused,
	// The first document browser's writes.
	"POST /documents":     isRefused,
	"PATCH /documents":    isRefused,
	"DELETE /documents":   isRefused,
	"POST /collections":   isRefused,
	"DELETE /collections": isRefused,
}

// letsThrough reports whether a route can be reached at all on a protected
// connection without a flag in its address.
func letsThrough(verdict string) bool { return verdict == staysOpen || verdict == byContent }

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
// that is not a read has an answer in the hand-written list above, and the
// middleware gives that answer — including for the routes nobody has written
// yet, which is the property the middleware exists for: a mutating route
// added to any database file tomorrow is refused, and fails this test until
// somebody has written down that it should be.
func TestProtectionCoversEveryMutatingRoute(t *testing.T) {
	s := testServer(t)
	protected := protectTestConnection(t, s, "protected", true)
	open := protectTestConnection(t, s, "open", false)

	seen := map[string]bool{}
	for _, rt := range connectionRoutes(t, s) {
		key := rt.method + " " + strings.TrimPrefix(rt.pattern, connectionRoutePrefix)
		seen[key] = true
		rest := strings.TrimPrefix(rt.path, "/api/v1/databases/1")
		verdict, decided := protectedRouteVerdicts[key]
		if !decided {
			t.Errorf("%s is a route that changes something and is not in this test's list: decide what a protected connection does with it", key)
			continue
		}

		rule, allowed := protectedRule(rt.method, rest)
		if allowed != letsThrough(verdict) {
			t.Errorf("%s: allowed on a protected connection = %v, the list in this test says %q", key, allowed, verdict)
		}
		if allowed && (rule.check != nil) != (verdict == byContent) {
			t.Errorf("%s: has a check of its body = %v, the list in this test says %q", key, rule.check != nil, verdict)
		}
		// The same answer from the middleware's own entry point, for a route
		// with nothing to read in its body.
		path := pathf("/api/v1/databases/%d", protected) + rest
		if rule.check == nil {
			refused := refusal(t, s, rt.method, path, `{}`) != ""
			if refused == allowed {
				t.Errorf("%s: refused = %v on a protected connection", key, refused)
			}
		}
		// Asking to be shown the statement opens a structure form and nothing
		// else: no other route is let through for a flag in its address.
		if !allowed {
			shown := refusal(t, s, rt.method, path+"?preview=1", `{}`) == ""
			if shown != (verdict == previewOnly) {
				t.Errorf("%s?preview=1: let through = %v on a protected connection, the list in this test says %q", key, shown, verdict)
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
	for key := range protectedRouteVerdicts {
		if !seen[key] {
			t.Errorf("%q is on the test's list and is not a route", key)
		}
	}
	var listed []string
	for _, rule := range protectedRoutesAllowed {
		key := rule.method + " " + rule.pattern
		listed = append(listed, key)
		if !seen[key] {
			t.Errorf("%q is allowed on a protected connection and is not a route", key)
		}
	}
	sort.Strings(listed)
	for i := 1; i < len(listed); i++ {
		if listed[i] == listed[i-1] {
			t.Errorf("%q is listed twice", listed[i])
		}
	}
}

// A schema form may be shown on a protected connection and may not be run.
func TestProtectedConnectionShowsASchemaChangeAndRunsNone(t *testing.T) {
	s := testServer(t)
	protected := protectTestConnection(t, s, "protected", true)
	forms := 0
	for _, rt := range connectionRoutes(t, s) {
		rest := strings.TrimPrefix(rt.path, "/api/v1/databases/1")
		if !strings.HasPrefix(rest, "/ddl/") {
			continue
		}
		forms++
		path := pathf("/api/v1/databases/%d", protected) + rest
		for _, query := range []string{"?preview=1", "?preview=true"} {
			if why := refusal(t, s, rt.method, path+query, `{}`); why != "" {
				t.Errorf("%s %s%s was refused on a protected connection: %s", rt.method, rest, query, why)
			}
		}
		for _, query := range []string{"", "?preview=0", "?preview=yes", "?preview=", "?Preview=1"} {
			if why := refusal(t, s, rt.method, path+query, `{}`); why == "" {
				t.Errorf("%s %s%s was let through on a protected connection", rt.method, rest, query)
			}
		}
	}
	if forms < 15 {
		t.Fatalf("only %d structure routes found", forms)
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
		if letsThrough(protectedRouteVerdicts[key]) {
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
		// Statements that start as a read and write all the same.
		"select 1 as id into copied",
		"select * into outfile '/tmp/orders.csv' from t",
		"with s as (select 1 as id) merge into t using s on t.id = s.id when not matched then insert (id) values (s.id)",
		"with s as (select 1 as id) insert t select id from s",
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
		{"a SELECT that creates a table", "/query", `{"query":"SELECT * INTO backup_users FROM users"}`, true},
		{"a SELECT that writes a file on the database host", "/query", `{"query":"SELECT 1 INTO OUTFILE '/tmp/x'"}`, true},
		{"a WITH that leads into a MERGE", "/query", `{"query":"WITH s AS (SELECT 1 AS id) MERGE INTO t USING s ON t.id = s.id WHEN NOT MATCHED THEN INSERT (id) VALUES (s.id)"}`, true},
		{"a WITH that leads into a MERGE without INTO", "/query", `{"query":"WITH s AS (SELECT 1 AS id) MERGE t USING s ON t.id = s.id WHEN NOT MATCHED THEN INSERT (id) VALUES (s.id);"}`, true},
		{"a WITH that leads into an INSERT without INTO", "/query", `{"query":"WITH s AS (SELECT 1 AS id) INSERT t SELECT id FROM s"}`, true},
		{"a plan that runs a SELECT INTO", "/query", `{"query":"EXPLAIN ANALYZE SELECT * INTO x FROM t"}`, true},
		{"a read that only mentions one of those words is refused too", "/query", `{"query":"select * from notes where body like '%into%'"}`, true},
		{"a column that merely starts with one is not", "/query", `{"query":"select inserted_at, merged, intolerance from audit"}`, false},
		{"the statement under a name in another case", "/query", `{"QUERY":"select 1"}`, false},
		{"a write under a name in another case", "/query", `{"Query":"drop table orders"}`, true},
		{"the same field twice, in two cases", "/query", `{"query":"select 1","Query":"drop table orders"}`, true},
		{"the same field twice", "/query", `{"query":"drop table orders","query":"select 1"}`, true},
		{"a name the decoder folds to a field's from outside ASCII", "/query", `{"query":"select 1","\u017fql":"drop table orders"}`, true},
		{"an option under a name outside ASCII", "/query", `{"query":"select 1","max\u212aows":5}`, true},
		{"two JSON values", "/query", `{"query":"select 1"} {"query":"drop table orders"}`, true},
		{"a list where an object belongs", "/query", `["select 1"]`, true},
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
		{"a script entry with text beside its statement", "/script", `{"statements":[{"sql":"select 1","then":"drop table orders"}]}`, true},
		{"a script entry that names its statement twice", "/script", `{"statements":[{"sql":"select 1","SQL":"drop table orders"}]}`, true},
		{"a script entry that writes through INTO", "/script", `{"statements":["select 1",{"sql":"select 1 into t"}]}`, true},

		{"a plan that is only planned", "/explain", `{"query":"delete from orders"}`, false},
		{"a plan with analyze off", "/explain", `{"query":"delete from orders","analyze":false,"format":"json"}`, false},
		{"a plan that runs a read", "/explain", `{"query":"select * from orders","analyze":true}`, false},
		{"a plan that runs a write", "/explain", `{"query":"delete from orders","analyze":true}`, true},
		{"analyze spelled as anything but false", "/explain", `{"query":"delete from orders","analyze":"yes"}`, true},
		{"analyze under a name in another case", "/explain", `{"query":"delete from orders","Analyze":true}`, true},
		{"analyze off and then on under another case", "/explain", `{"query":"delete from orders","analyze":false,"ANALYZE":true}`, true},
		{"analyze off twice", "/explain", `{"query":"delete from orders","analyze":false,"analyze":false}`, true},
		{"a plan with an option nobody has decided about", "/explain", `{"query":"delete from orders","execute":true}`, true},
		{"a plan with the options the route takes", "/explain", `{"query":"select 1","maxRows":10,"analyze":false,"format":"text"}`, false},

		{"a pipeline that reads", "/aggregate", `{"collection":"orders","pipeline":"[{\"$match\":{\"paid\":true}},{\"$group\":{\"_id\":\"$city\",\"n\":{\"$sum\":1}}}]"}`, false},
		{"$mergeObjects is an expression, not a stage that writes", "/aggregate", `{"collection":"o","pipeline":"[{\"$replaceRoot\":{\"newRoot\":{\"$mergeObjects\":[\"$a\",\"$b\"]}}}]"}`, false},
		{"a pipeline that writes with $out", "/aggregate", `{"collection":"orders","pipeline":"[{\"$match\":{}},{\"$out\":\"copy\"}]"}`, true},
		{"a pipeline that writes with $merge", "/aggregate", `{"collection":"orders","pipeline":"[{\"$merge\":{\"into\":\"copy\"}}]"}`, true},
		{"a stage name spelled with an escape", "/aggregate", `{"collection":"orders","pipeline":"[{\"\\u0024out\":\"copy\"}]"}`, true},
		{"a writing stage inside a sub-pipeline", "/aggregate", `{"collection":"o","pipeline":"[{\"$facet\":{\"a\":[{\"$out\":\"copy\"}]}}]"}`, true},
		{"a pipeline given as a value rather than as text", "/aggregate", `{"collection":"o","pipeline":[{"$out":"copy"}]}`, true},
		{"a pipeline that is not JSON", "/aggregate", `{"collection":"o","pipeline":"[{$out: 'copy'}]"}`, true},
		{"no pipeline", "/aggregate", `{"collection":"orders"}`, true},
		{"a reading pipeline and then a writing one under another case", "/aggregate", `{"collection":"src","pipeline":"[]","Pipeline":"[{\"$out\":\"copy\"}]"}`, true},
		{"a writing pipeline under a name in capitals", "/aggregate", `{"collection":"src","PIPELINE":"[{\"$merge\":{\"into\":\"copy\"}}]"}`, true},
		{"a reading pipeline under a name in another case", "/aggregate", `{"collection":"src","Pipeline":"[{\"$match\":{}}]"}`, false},
		{"a writing pipeline and then a reading one", "/aggregate", `{"collection":"src","pipeline":"[{\"$out\":\"copy\"}]","pipeline":"[]"}`, true},
		{"a writing stage behind a second key of the same name", "/aggregate", `{"collection":"o","pipeline":"[{\"$lookup\":{\"pipeline\":[{\"$merge\":{\"into\":\"copy\"}}],\"pipeline\":[]}}]"}`, true},
		{"a pipeline wrapped in a second string", "/aggregate", `{"collection":"o","pipeline":"\"[]\" [{\"$out\":\"copy\"}]"}`, true},
		{"a pipeline in shell syntax that reads", "/aggregate", `{"collection":"o","pipeline":"[ { $match: { paid: true } }, { $limit: 5 } ]"}`, false},
		{"a pipeline with a stage nobody knows to be a read", "/aggregate", `{"collection":"o","pipeline":"[{\"$inventedStage\":{}}]"}`, true},

		{"a statement's result as a file", "/export/query", `{"sql":"select * from orders","format":"csv","columns":["id","total"],"filename":"orders","exportId":"export-0001","limit":100,"schema":"main","table":"orders"}`, false},
		{"a write asked for as a file", "/export/query", `{"sql":"delete from orders returning *","format":"csv"}`, true},
		{"an export that stores its result", "/export/query", `{"sql":"select * into copied from orders","format":"csv"}`, true},
		{"an export of two statements, one a write", "/export/query", `{"sql":"select 1; drop table orders","format":"json"}`, true},
		{"an export's statement under a name in another case", "/export/query", `{"SQL":"drop table orders","format":"csv"}`, true},
		{"an export's statement given twice", "/export/query", `{"sql":"select 1","Sql":"drop table orders"}`, true},
		{"an export with no statement", "/export/query", `{"format":"csv"}`, true},
		{"an export with a field the handler does not have", "/export/query", `{"sql":"select 1","then":"drop table orders"}`, true},

		{"a change set that is only shown", "/changes", `{"schema":"main","table":"orders","changes":[{"op":"update","key":{"id":7},"values":{"paid":true}}],"dryRun":true}`, false},
		{"a change set that is applied", "/changes", `{"schema":"main","table":"orders","changes":[{"op":"update","key":{"id":7},"values":{"paid":true}}]}`, true},
		{"a change set with the dry run turned off again under another case", "/changes", `{"table":"orders","changes":[{"op":"delete","key":{"id":7}}],"dryRun":true,"DryRun":false}`, true},
		{"a change set whose dry run is not a yes or a no", "/changes", `{"table":"orders","changes":[],"dryRun":"yes"}`, true},
		{"a change set with a field the handler does not have", "/changes", `{"table":"orders","changes":[],"dryRun":true,"apply":true}`, true},
		{"no change set at all", "/changes", ``, true},

		{"a consistency check", "/maintenance", `{"action":"integrity_check"}`, false},
		{"a check of one table's foreign keys", "/maintenance", `{"action":"foreign_key_check","table":"orders"}`, false},
		{"maintenance that rewrites the file", "/maintenance", `{"action":"vacuum"}`, true},
		{"maintenance that changes statistics", "/maintenance", `{"action":"analyze"}`, true},
		{"a check and then a rebuild under another case", "/maintenance", `{"action":"integrity_check","Action":"vacuum"}`, true},
		{"an action the engine does not have", "/maintenance", `{"action":"check"}`, true},
		{"no action", "/maintenance", `{}`, true},

		{"a console line that reads", "/redis/command", `{"command":"GET session:1"}`, false},
		{"a console line that writes", "/redis/command", `{"command":"SET session:1 x"}`, true},
		{"a console line the dashboard does not know", "/redis/command", `{"command":"MODULE.READ k"}`, true},
		{"a console read and then a write under another case", "/redis/command", `{"command":"GET k","Command":"FLUSHALL"}`, true},
		{"a bulk action that only counts", "/keys/bulk", `{"pattern":"session:*","action":"delete","dryRun":true}`, false},
		{"a bulk action that runs", "/keys/bulk", `{"pattern":"session:*","action":"delete"}`, true},
		{"a bulk dry run turned off again", "/keys/bulk", `{"pattern":"session:*","action":"delete","dryRun":true,"DRYRUN":false}`, true},

		{"a command that reads", "/mongo/command", `{"command":"{\"find\":\"orders\",\"limit\":1}"}`, false},
		{"a command in shell syntax that reads", "/mongo/command", `{"database":"shop","command":"{ ping: 1 }"}`, false},
		{"a command that writes", "/mongo/command", `{"command":"{\"drop\":\"orders\"}"}`, true},
		{"a command nobody listed", "/mongo/command", `{"command":"{\"inventedCommand\":1}"}`, true},
		{"a command given twice", "/mongo/command", `{"command":"{\"ping\":1}","Command":"{\"dropDatabase\":1}"}`, true},
		{"no command", "/mongo/command", `{}`, true},
	} {
		why := refusal(t, s, http.MethodPost, base+c.path, c.body)
		if (why != "") != c.refused {
			t.Errorf("%s (%s %s): refused = %v (%q), want %v", c.name, c.path, c.body, why != "", why, c.refused)
		}
	}

	// An update or a delete of documents that only counts what it would reach.
	for _, method := range []string{http.MethodPatch, http.MethodDelete} {
		for body, refused := range map[string]bool{
			`{"collection":"orders","filter":"{}","dryRun":true}`:                false,
			`{"collection":"orders","filter":"{}"}`:                              true,
			`{"collection":"orders","filter":"{}","dryRun":false}`:               true,
			`{"collection":"orders","filter":"{}","dryRun":true,"DryRun":false}`: true,
			`{"collection":"orders","filter":"{}","dryRun":"true"}`:              true,
			``: true,
		} {
			if why := refusal(t, s, method, base+"/mongo/documents", body); (why != "") != refused {
				t.Errorf("%s /mongo/documents %s: refused = %v (%q), want %v", method, body, why != "", why, refused)
			}
		}
	}
}

// The checks added for the other routes that read or write by their body
// judge the value the handler will decode: its own request type, through its
// own decoder.
func TestProtectedChecksDecodeAsTheirHandlersDo(t *testing.T) {
	decode := func(body string, into any, exact bool) error {
		return protectedBody([]byte(body), into, exact)
	}
	for _, body := range []string{
		`{"table":"t","changes":[],"dryRun":true}`,
		`{"table":"t","changes":[],"dryRun":true,"DryRun":false}`,
		`{"table":"t","changes":[],"DRYRUN":true}`,
		`{"table":"t","changes":[],"dryRun":false,"dryrun":true}`,
		`{"table":"t","changes":[]}`,
	} {
		var req changesRequest
		if err := decode(body, &req, true); err != nil {
			t.Fatalf("%s does not decode: %v", body, err)
		}
		if refused := protectedChanges(dbx.DriverSQLite, []byte(body)) != nil; refused == req.DryRun {
			t.Errorf("%s: refused = %v, and the handler decodes dryRun = %v", body, refused, req.DryRun)
		}
	}
	for _, body := range []string{
		`{"sql":"select 1","SQL":"drop table orders"}`,
		`{"Sql":"drop table orders","sql":"select 1"}`,
		`{"SQL":"select 1"}`,
		`{"sql":"drop table orders"}`,
	} {
		var req exportQueryRequest
		if err := decode(body, &req, false); err != nil {
			t.Fatalf("%s does not decode: %v", body, err)
		}
		writes := strings.Contains(req.SQL, "drop")
		if refused := protectedExportQuery(dbx.DriverSQLite, []byte(body)) != nil; refused != writes {
			t.Errorf("%s: refused = %v, and the handler decodes the statement %q", body, refused, req.SQL)
		}
	}
	for _, body := range []string{
		`{"action":"integrity_check","Action":"vacuum"}`,
		`{"ACTION":"vacuum","action":"integrity_check"}`,
		`{"Action":"quick_check"}`,
		`{"action":"reindex"}`,
	} {
		var req maintenanceRequest
		if err := decode(body, &req, false); err != nil {
			t.Fatalf("%s does not decode: %v", body, err)
		}
		action, _ := dbx.MaintenanceActionFor(dbx.DriverSQLite, req.Action)
		if refused := protectedMaintenance(dbx.DriverSQLite, []byte(body)) != nil; refused == action.ReadOnly {
			t.Errorf("%s: refused = %v, and the handler decodes the action %q (read-only = %v)", body, refused, req.Action, action.ReadOnly)
		}
	}
	// What an engine reads as a string decides what a statement is, so the
	// check asks the engine the connection is to. A dollar-quoted body is one
	// value on Postgres, where it can hold anything and is refused, and three
	// words on the engines that have no such quoting.
	for driver, refused := range map[dbx.Driver]bool{dbx.DriverPostgres: true, dbx.DriverSQLite: false} {
		if why := protectedStatementRefusal(driver, "select $$x$$ as body"); (why != "") != refused {
			t.Errorf("%s: a dollar-quoted body refused = %v (%q), want %v", driver, why != "", why, refused)
		}
	}
	// MySQL opens a comment with -- only before a space; everywhere else the
	// rest of the line is a comment and the statement a read.
	for driver, refused := range map[dbx.Driver]bool{dbx.DriverMySQL: true, dbx.DriverPostgres: false} {
		if why := protectedStatementRefusal(driver, "select 1 --note"); (why != "") != refused {
			t.Errorf("%s: a comment with no space refused = %v (%q), want %v", driver, why != "", why, refused)
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

// What the handler decodes is what the check judged. encoding/json fills a
// field from a key in any case and from the last of two that name it, so
// every body the check lets through has to decode, in the handler's own
// struct and with the handler's own decoder, to the value that was judged.
func TestProtectionJudgesWhatTheHandlerDecodes(t *testing.T) {
	decode := func(body string, into any) error {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		return httpx.DecodeJSON(req, into)
	}
	for _, body := range []string{
		`{"collection":"src","pipeline":"[]","Pipeline":"[{\"$out\":\"copy\"}]"}`,
		`{"collection":"src","pipeline":"[]","PIPELINE":"[{\"$out\":\"copy\"}]"}`,
		`{"collection":"src","Pipeline":"[{\"$out\":\"copy\"}]","pipeline":"[]"}`,
		`{"collection":"src","pipeline":"[{\"$match\":{}}]"}`,
		`{"collection":"src","pIpElInE":"[{\"$match\":{}}]"}`,
	} {
		var req mongoAggregateRequest
		if err := decode(body, &req); err != nil {
			t.Fatalf("%s does not decode: %v", body, err)
		}
		writes := strings.Contains(req.Pipeline, "$out")
		refused := protectedMongo(mongoReadOnlyPipeline)(dbx.DriverMongo, []byte(body)) != nil
		if writes && !refused {
			t.Errorf("%s was let through, and the handler decodes the pipeline %s", body, req.Pipeline)
		}
		// A body that names the pipeline once and reads is not refused for
		// the case it is spelled in.
		if !writes && refused && strings.Count(strings.ToLower(body), `"pipeline"`) == 1 {
			t.Errorf("%s was refused, and the handler decodes the reading pipeline %s", body, req.Pipeline)
		}
	}
	for _, body := range []string{
		`{"query":"select 1","Query":"drop table orders"}`,
		`{"Query":"drop table orders","query":"select 1"}`,
		`{"QUERY":"drop table orders"}`,
		`{"query":"select 1"}`,
	} {
		var req queryRequest
		if err := decode(body, &req); err != nil {
			t.Fatalf("%s does not decode: %v", body, err)
		}
		writes := strings.Contains(req.Query, "drop")
		refused := protectedStatements(dbx.DriverSQLite, []byte(body)) != nil
		if writes && !refused {
			t.Errorf("%s was let through, and the handler decodes the statement %q", body, req.Query)
		}
	}
}

// The Backups page has its own route for loading a dump into a connection.
// It is outside /databases, where the middleware stands, and is held to the
// same answer.
func TestBackupsRestoreIntoAProtectedConnectionIsRefused(t *testing.T) {
	s := testServer(t)
	protected := protectTestConnection(t, s, "protected", true)
	open := protectTestConnection(t, s, "open", false)
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}

	rec := c.do(http.MethodPost, "/api/v1/backups/runs/1/restore-database", pathf(`{"connectionId":%d}`, protected), nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "connection_read_only") {
		t.Errorf("restore into a protected connection = %d %s, want 409 connection_read_only", rec.Code, rec.Body.String())
	}
	rec = c.do(http.MethodPost, "/api/v1/backups/runs/1/restore-database", pathf(`{"connectionId":%d}`, open), nil)
	if strings.Contains(rec.Body.String(), "connection_read_only") {
		t.Errorf("restore into a connection that is not protected = %d %s", rec.Code, rec.Body.String())
	}
}

// Behind that route the Backups module restores through its own adapter. The
// adapter refuses a protected connection as well, so the answer does not
// depend on every caller it ever gets remembering to ask first.
func TestTheBackupsAdapterDoesNotRestoreIntoAProtectedConnection(t *testing.T) {
	s := testServer(t)
	protected := protectTestConnection(t, s, "protected", true)
	dumper := &backupDatabaseDumper{server: s}
	_, err := dumper.RestoreDatabase(t.Context(), protected, "", "/nowhere/dump.sql")
	if err == nil || !strings.Contains(err.Error(), "is protected") {
		t.Errorf("restoring into a protected connection through the adapter = %v, want a refusal that says it is protected", err)
	}
}

// The section's own routes concern no connection, and a protected one
// elsewhere in the table must not get in their way.
func TestProtectionLeavesSectionRoutesAlone(t *testing.T) {
	s := testServer(t)
	protectTestConnection(t, s, "protected", true)
	for _, path := range []string{
		"/", "/test", "/sync", "/adopt", "/host", "/host/grant", "/provision",
		"/inventory/scan", "/inventory/connect", "/inventory/ignore",
	} {
		if why := refusal(t, s, http.MethodPost, "/api/v1/databases"+path, `{}`); why != "" {
			t.Errorf("POST /databases%s was refused: %s", path, why)
		}
	}
}
