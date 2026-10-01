package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// Who may do what on the Mongo routes, and what each route decides from the
// request before it dials anything.
//
// None of these tests needs a MongoDB. The one saved connection has a
// connection string the driver rejects on sight, so dialling it fails at once
// with connect_failed. That failure is the tests' signal: a request that got
// as far as dialling passed every check in front of it, and a request that
// must be refused by its capability or its content has to be refused without
// getting there.

const mongoConn = "1"

func mongoRouterAs(t *testing.T, role auth.Role) (*Server, http.Handler) {
	t.Helper()
	s := testServer(t)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{
				User: &auth.User{ID: 1, Username: "tester"},
				Role: role, Kind: "session", IP: "127.0.0.1",
			}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	r.Use(httpx.AuditMutations(s.Audit))
	s.mountDatabaseRoutes(r)
	sealed, err := s.Sealer.Seal("mongodb://127.0.0.1:1/jdtest?connectTimeoutMS=never")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(
		`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`,
		"unreachable", string(dbx.DriverMongo), sealed, 0); err != nil {
		t.Fatal(err)
	}
	return s, r
}

func mongoDo(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func mongoErrorCode(rec *httptest.ResponseRecorder) string {
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Error.Code
}

// passedTheGate reports whether a request got through everything in front of
// the dial: it reached the server that is not there, or it is one of the two
// routes that answer without one.
func passedTheGate(rec *httptest.ResponseRecorder) bool {
	return rec.Code == http.StatusOK || (rec.Code == http.StatusBadGateway && mongoErrorCode(rec) == "connect_failed")
}

type mongoRoute struct{ method, path, body string }

var (
	// Written out by hand, like the lists in handlers_db_test.go: a test that
	// derived them from the router would pass just as happily if a route
	// moved from one to another.
	mongoReadSurface = []mongoRoute{
		{http.MethodPost, "/mongo/find", `{"collection":"c"}`},
		{http.MethodPost, "/mongo/count", `{"collection":"c"}`},
		{http.MethodPost, "/mongo/document", `{"collection":"c","id":"1"}`},
		{http.MethodPost, "/mongo/explain", `{"collection":"c"}`},
		{http.MethodPost, "/mongo/aggregate/preview", `{"collection":"c","pipeline":"[]","stage":-1}`},
		{http.MethodPost, "/mongo/schema", `{"collection":"c"}`},
		{http.MethodGet, "/mongo/export?collection=c", ``},
		{http.MethodGet, "/mongo/collections", ``},
		{http.MethodGet, "/mongo/collection?collection=c", ``},
		{http.MethodGet, "/mongo/indexes?collection=c", ``},
		{http.MethodGet, "/mongo/validation?collection=c", ``},
		{http.MethodPost, "/mongo/validation/check", `{"collection":"c"}`},
		{http.MethodGet, "/mongo/server", ``},
		{http.MethodGet, "/mongo/databases", ``},
		{http.MethodGet, "/mongo/ops", ``},
		{http.MethodGet, "/mongo/profiler", ``},
		{http.MethodGet, "/mongo/replication", ``},
		{http.MethodGet, "/mongo/users", ``},
		{http.MethodGet, "/mongo/roles", ``},
		{http.MethodPost, "/mongo/command/classify", `{"command":"{\"ping\":1}"}`},
		{http.MethodGet, "/mongo/commands", ``},
		{http.MethodGet, "/collections/indexes?schema=d&table=c", ``},
	}
	mongoServiceControl = []mongoRoute{
		{http.MethodPost, "/mongo/documents", `{"collection":"c","documents":"{}"}`},
		{http.MethodPut, "/mongo/documents", `{"collection":"c","id":"1","document":"{}"}`},
		{http.MethodPatch, "/mongo/documents", `{"collection":"c","filter":"{\"a\":1}","update":"{\"$set\":{\"b\":1}}"}`},
		{http.MethodPost, "/mongo/documents/clone", `{"collection":"c","id":"1"}`},
		{http.MethodPost, "/mongo/collections", `{"collection":"c"}`},
		{http.MethodPost, "/mongo/collections/rename", `{"collection":"c","to":"d"}`},
		{http.MethodPatch, "/mongo/collections", `{"collection":"c","validationLevel":"off"}`},
		{http.MethodPost, "/mongo/indexes", `{"collection":"c","keys":[{"field":"a","type":"asc"}]}`},
		{http.MethodPatch, "/mongo/indexes", `{"collection":"c","name":"a_1","hidden":true}`},
		{http.MethodPut, "/mongo/validation", `{"collection":"c","level":"off"}`},
		{http.MethodPost, "/mongo/command", `{"command":"{\"ping\":1}"}`},
		{http.MethodPost, "/aggregate", `{"collection":"c","pipeline":"[]"}`},
		{http.MethodPost, "/documents", `{"collection":"c","document":"{}"}`},
		{http.MethodPatch, "/documents", `{"collection":"c","filter":"{}","document":"{}"}`},
		{http.MethodPost, "/collections", `{"collection":"c"}`},
	}
	mongoDestructive = []mongoRoute{
		{http.MethodDelete, "/mongo/documents", `{"collection":"c","id":"1"}`},
		{http.MethodDelete, "/mongo/collections", `{"collection":"c"}`},
		{http.MethodDelete, "/mongo/indexes", `{"collection":"c","name":"a_1"}`},
		{http.MethodPost, "/mongo/killop", `{"opId":"1"}`},
		{http.MethodDelete, "/documents", `{"collection":"c","filter":"{\"a\":1}"}`},
		{http.MethodDelete, "/collections", `{"collection":"c"}`},
	}
	mongoSystemAdmin = []mongoRoute{
		{http.MethodPut, "/mongo/profiler", `{"level":1}`},
		{http.MethodPost, "/mongo/users", `{"user":"u","password":"p"}`},
		{http.MethodPut, "/mongo/users", `{"user":"u","password":"p"}`},
		{http.MethodPost, "/mongo/users/grant", `{"user":"u","roles":[{"role":"read","db":"d"}]}`},
		{http.MethodPost, "/mongo/users/revoke", `{"user":"u","roles":[{"role":"read","db":"d"}]}`},
		{http.MethodDelete, "/mongo/users", `{"user":"u"}`},
	}
)

func TestMongoRoutesAskForTheirCapability(t *testing.T) {
	check := func(role auth.Role, routes []mongoRoute, allowed bool) {
		t.Helper()
		_, router := mongoRouterAs(t, role)
		for _, rt := range routes {
			rec := mongoDo(router, rt.method, "/databases/"+mongoConn+rt.path, rt.body)
			if allowed && !passedTheGate(rec) {
				t.Errorf("as %s: %s %s was stopped: %d %s", role, rt.method, rt.path, rec.Code, strings.TrimSpace(rec.Body.String()))
			}
			if !allowed && (rec.Code != http.StatusForbidden || mongoErrorCode(rec) != "forbidden") {
				t.Errorf("as %s: %s %s was not refused: %d %s", role, rt.method, rt.path, rec.Code, strings.TrimSpace(rec.Body.String()))
			}
		}
	}
	// Any role reads.
	check(auth.RoleReadOnly, mongoReadSurface, true)
	// A read-only role writes nothing.
	check(auth.RoleReadOnly, mongoServiceControl, false)
	check(auth.RoleReadOnly, mongoDestructive, false)
	check(auth.RoleReadOnly, mongoSystemAdmin, false)
	// A limited role makes routine writes and nothing that cannot be undone.
	check(auth.RoleLimited, mongoServiceControl, true)
	check(auth.RoleLimited, mongoDestructive, false)
	check(auth.RoleLimited, mongoSystemAdmin, false)
	// An administrator does all of it.
	check(auth.RoleAdmin, mongoDestructive, true)
	check(auth.RoleAdmin, mongoSystemAdmin, true)
}

// The four lists above are every Mongo route. A route added to the router and
// to none of them has had no capability decided for it.
func TestEveryMongoRouteIsInACapabilityList(t *testing.T) {
	listed := map[string]bool{}
	for _, list := range [][]mongoRoute{mongoReadSurface, mongoServiceControl, mongoDestructive, mongoSystemAdmin} {
		for _, rt := range list {
			path, _, _ := strings.Cut(rt.path, "?")
			listed[rt.method+" "+path] = true
		}
	}
	_, router := mongoRouterAs(t, auth.RoleAdmin)
	found := 0
	err := chi.Walk(router.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		path, ok := strings.CutPrefix(route, "/databases/{id}")
		if !ok || !(strings.HasPrefix(path, "/mongo/") || path == "/documents" || path == "/collections" ||
			path == "/collections/indexes" || path == "/aggregate") {
			return nil
		}
		found++
		if !listed[method+" "+path] {
			t.Errorf("%s %s is mounted and is in no capability list", method, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found != len(listed) {
		t.Errorf("%d Mongo routes are mounted and %d are listed", found, len(listed))
	}
}

// No Mongo route asks for a typed phrase. Deleting documents, dropping a
// collection or an index, killing an operation and removing an account all
// take an ordinary confirmation in the page.
func TestMongoRoutesDoNotAskForAPhrase(t *testing.T) {
	_, router := mongoRouterAs(t, auth.RoleAdmin)
	for _, list := range [][]mongoRoute{mongoServiceControl, mongoDestructive, mongoSystemAdmin} {
		for _, rt := range list {
			rec := mongoDo(router, rt.method, "/databases/"+mongoConn+rt.path, rt.body)
			if strings.Contains(rec.Body.String(), "confirmation") {
				t.Errorf("%s %s asked for a phrase: %d %s", rt.method, rt.path, rec.Code, strings.TrimSpace(rec.Body.String()))
			}
		}
	}
}

// Routes that are routine by default and destructive by one option decide
// from the request, before anything is dialled, and refuse a role without
// the destructive capability.
func TestMongoContentDecidesDestructiveness(t *testing.T) {
	type request struct {
		name, method, path, body string
	}
	destructive := []request{
		{"update every document", http.MethodPatch, "/mongo/documents",
			`{"collection":"c","filter":"{}","update":"{\"$set\":{\"a\":1}}","many":true,"all":true}`},
		{"update every document, filter left out", http.MethodPatch, "/mongo/documents",
			`{"collection":"c","update":"{\"$set\":{\"a\":1}}","many":true,"all":true}`},
		{"update every document, shell syntax", http.MethodPatch, "/mongo/documents",
			`{"collection":"c","filter":"{ /* all */ }","update":"{ $unset: { a: 1 } }","many":true,"all":true}`},
		{"rename over an existing collection", http.MethodPost, "/mongo/collections/rename",
			`{"collection":"c","to":"d","dropTarget":true}`},
		{"shrink a capped collection", http.MethodPatch, "/mongo/collections", `{"collection":"c","cappedSize":1024}`},
		{"cap a collection's documents", http.MethodPatch, "/mongo/collections", `{"collection":"c","cappedMax":10}`},
		{"expire a collection's documents", http.MethodPatch, "/mongo/collections", `{"collection":"c","expireAfterSeconds":60}`},
		{"a TTL index", http.MethodPost, "/mongo/indexes",
			`{"collection":"c","keys":[{"field":"at","type":"asc"}],"expireAfterSeconds":60}`},
		{"a TTL index that expires at once", http.MethodPost, "/mongo/indexes",
			`{"collection":"c","keys":[{"field":"at","type":"asc"}],"expireAfterSeconds":0}`},
		{"a new TTL limit", http.MethodPatch, "/mongo/indexes", `{"collection":"c","name":"at_1","expireAfterSeconds":60}`},
		{"a pipeline with $out", http.MethodPost, "/aggregate", `{"collection":"c","pipeline":"[{\"$out\":\"x\"}]"}`},
		{"a pipeline with $merge", http.MethodPost, "/aggregate", `{"collection":"c","pipeline":"[{\"$match\":{}},{\"$merge\":{\"into\":\"x\"}}]"}`},
		{"$out behind an escape", http.MethodPost, "/aggregate", `{"collection":"c","pipeline":"[{\"\\u0024out\":\"x\"}]"}`},
		{"$out in shell syntax", http.MethodPost, "/aggregate", `{"collection":"c","pipeline":"[ { $out: 'x' } ]"}`},
		{"$out inside a $lookup", http.MethodPost, "/aggregate",
			`{"collection":"c","pipeline":"[{\"$lookup\":{\"from\":\"b\",\"as\":\"x\",\"pipeline\":[{\"$out\":\"y\"}]}}]"}`},
		{"a stage nobody listed", http.MethodPost, "/aggregate", `{"collection":"c","pipeline":"[{\"$inventedStage\":{}}]"}`},
		{"console: delete", http.MethodPost, "/mongo/command", `{"command":"{ delete: 'c', deletes: [ { q: {}, limit: 0 } ] }"}`},
		{"console: drop", http.MethodPost, "/mongo/command", `{"command":"{ drop: 'c' }"}`},
		{"console: dropIndexes", http.MethodPost, "/mongo/command", `{"command":"{ dropIndexes: 'c', index: '*' }"}`},
		{"console: killOp", http.MethodPost, "/mongo/command", `{"command":"{ killOp: 1, op: 5 }"}`},
		{"console: an aggregation that writes", http.MethodPost, "/mongo/command",
			`{"command":"{ aggregate: 'c', pipeline: [ { $out: 'x' } ], cursor: {} }"}`},
		{"console: update every document", http.MethodPost, "/mongo/command",
			`{"command":"{ update: 'c', updates: [ { q: {}, u: { $set: { a: 1 } }, multi: true } ] }"}`},
		{"console: findAndModify that removes", http.MethodPost, "/mongo/command",
			`{"command":"{ findAndModify: 'c', query: { a: 1 }, remove: true }"}`},
		{"console: a TTL index", http.MethodPost, "/mongo/command",
			`{"command":"{ createIndexes: 'c', indexes: [ { key: { at: 1 }, name: 't', expireAfterSeconds: 1 } ] }"}`},
		{"console: a command nobody listed", http.MethodPost, "/mongo/command", `{"command":"{ someNewCommand: 1 }"}`},
		{"console: compact", http.MethodPost, "/mongo/command", `{"command":"{ compact: 'c' }"}`},
	}
	_, limited := mongoRouterAs(t, auth.RoleLimited)
	for _, rq := range destructive {
		rec := mongoDo(limited, rq.method, "/databases/"+mongoConn+rq.path, rq.body)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "destructive") {
			t.Errorf("%s: a limited role was not refused: %d %s", rq.name, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
	// The same routes without the option are routine, and the same role is
	// let through.
	routine := []request{
		{"update by a filter", http.MethodPatch, "/mongo/documents",
			`{"collection":"c","filter":"{\"a\":1}","update":"{\"$set\":{\"b\":1}}","many":true}`},
		{"count what an update of everything would reach", http.MethodPatch, "/mongo/documents",
			`{"collection":"c","filter":"{}","update":"{\"$set\":{\"a\":1}}","many":true,"dryRun":true}`},
		{"a plain rename", http.MethodPost, "/mongo/collections/rename", `{"collection":"c","to":"d"}`},
		{"a new validator", http.MethodPatch, "/mongo/collections", `{"collection":"c","validator":"{}"}`},
		{"turning an expiry off", http.MethodPatch, "/mongo/collections", `{"collection":"c","expireAfterSeconds":-1}`},
		{"an ordinary index", http.MethodPost, "/mongo/indexes", `{"collection":"c","keys":[{"field":"a","type":"asc"}],"unique":true}`},
		{"hiding an index", http.MethodPatch, "/mongo/indexes", `{"collection":"c","name":"a_1","hidden":true}`},
		{"a pipeline using $mergeObjects", http.MethodPost, "/aggregate",
			`{"collection":"c","pipeline":"[{\"$group\":{\"_id\":null,\"doc\":{\"$mergeObjects\":\"$$ROOT\"}}}]"}`},
		{"a pipeline sent in the body the first page used", http.MethodPost, "/aggregate",
			`{"database":"d","collection":"c","pipeline":"[]","limit":200,"filter":"","document":"","many":false}`},
		{"console: find", http.MethodPost, "/mongo/command", `{"command":"{ find: 'c' }"}`},
		{"console: insert", http.MethodPost, "/mongo/command", `{"command":"{ insert: 'c', documents: [ { a: 1 } ] }"}`},
		{"console: update by a filter", http.MethodPost, "/mongo/command",
			`{"command":"{ update: 'c', updates: [ { q: { a: 1 }, u: { $set: { b: 1 } }, multi: true } ] }"}`},
	}
	for _, rq := range routine {
		rec := mongoDo(limited, rq.method, "/databases/"+mongoConn+rq.path, rq.body)
		if !passedTheGate(rec) {
			t.Errorf("%s: a limited role was stopped: %d %s", rq.name, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
	// And an administrator is let through the destructive ones.
	_, admin := mongoRouterAs(t, auth.RoleAdmin)
	for _, rq := range destructive[:6] {
		rec := mongoDo(admin, rq.method, "/databases/"+mongoConn+rq.path, rq.body)
		if !passedTheGate(rec) {
			t.Errorf("%s: an administrator was stopped: %d %s", rq.name, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
}

// An empty filter with many means every document. It has to be said twice —
// the empty filter and all — so that a filter lost on the way to the server
// does not become "all of them".
func TestMongoEmptyFilterMustBeMeant(t *testing.T) {
	_, admin := mongoRouterAs(t, auth.RoleAdmin)
	for name, rq := range map[string]mongoRoute{
		"update": {http.MethodPatch, "/mongo/documents", `{"collection":"c","filter":"","update":"{\"$set\":{\"a\":1}}","many":true}`},
		"delete": {http.MethodDelete, "/mongo/documents", `{"collection":"c","filter":"{}","many":true}`},
	} {
		rec := mongoDo(admin, rq.method, "/databases/"+mongoConn+rq.path, rq.body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "all: true") {
			t.Errorf("%s of everything without all: %d %s", name, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
		meant := strings.Replace(rq.body, `"many":true`, `"many":true,"all":true`, 1)
		if rec := mongoDo(admin, rq.method, "/databases/"+mongoConn+rq.path, meant); !passedTheGate(rec) {
			t.Errorf("%s of everything with all: %d %s", name, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
	// A filter that does not parse is not treated as empty or as anything.
	rec := mongoDo(admin, http.MethodDelete, "/databases/"+mongoConn+"/mongo/documents", `{"collection":"c","filter":"{ a: ","many":true,"all":true}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "filter") {
		t.Errorf("a broken filter: %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}

func TestMongoConsoleGates(t *testing.T) {
	command := func(text string) string {
		b, _ := json.Marshal(map[string]string{"command": text})
		return string(b)
	}
	_, admin := mongoRouterAs(t, auth.RoleAdmin)
	_, limited := mongoRouterAs(t, auth.RoleLimited)

	// Never run, whoever asks.
	for _, text := range []string{
		`{ shutdown: 1 }`, `{ replSetReconfig: {} }`, `{ replSetStepDown: 60 }`, `{ eval: "1" }`,
		`{ mapReduce: "c", map: "f", reduce: "g", out: "x" }`, `{ applyOps: [] }`,
		`{ usersInfo: 1, showCredentials: true }`, `{ _internalCommand: 1 }`,
		`{ find: "c", lsid: { id: 1 } }`,
	} {
		rec := mongoDo(admin, http.MethodPost, "/databases/"+mongoConn+"/mongo/command", command(text))
		if rec.Code != http.StatusBadRequest || mongoErrorCode(rec) != "command_blocked" {
			t.Errorf("%s was not blocked: %d %s", text, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
	// Accounts and server settings: what their forms keep to administrators.
	for _, text := range []string{
		`{ createUser: "u", pwd: "p", roles: [] }`, `{ grantRolesToUser: "u", roles: ["root"] }`,
		`{ profile: 2 }`, `{ dropUser: "u" }`, `{ dropDatabase: 1 }`, `{ setParameter: 1, logLevel: 5 }`,
	} {
		rec := mongoDo(limited, http.MethodPost, "/databases/"+mongoConn+"/mongo/command", command(text))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s as a limited role: %d %s", text, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
		if rec := mongoDo(admin, http.MethodPost, "/databases/"+mongoConn+"/mongo/command", command(text)); !passedTheGate(rec) {
			t.Errorf("%s as an administrator: %d %s", text, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
	// The collections that hold credentials, in the database that has them.
	for _, text := range []string{`{ find: "system.users" }`, `{ aggregate: "c", pipeline: [ { $unionWith: "system.users" } ], cursor: {} }`} {
		body, _ := json.Marshal(map[string]string{"command": text, "database": "admin"})
		rec := mongoDo(admin, http.MethodPost, "/databases/"+mongoConn+"/mongo/command", string(body))
		if rec.Code != http.StatusBadRequest || mongoErrorCode(rec) != "command_blocked" || !strings.Contains(rec.Body.String(), "credentials") {
			t.Errorf("%s in admin was not blocked: %d %s", text, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
	// What cannot be read is refused as a request, not run as a guess.
	for text, want := range map[string]string{
		``: "required", `{}`: "empty", `[1]`: "document", `{ Find: "c" }`: "did you mean",
		`{ find: "c", $db: "admin" }`: "choose the database",
	} {
		rec := mongoDo(admin, http.MethodPost, "/databases/"+mongoConn+"/mongo/command", command(text))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("command %q: %d %s; want a 400 mentioning %q", text, rec.Code, strings.TrimSpace(rec.Body.String()), want)
		}
	}
}

// The destructive capability checked by hand comes with the destructive
// budget, as it does on the routes that s.destructive wraps.
func TestMongoConsoleDestructiveCommandsAreRateLimited(t *testing.T) {
	_, admin := mongoRouterAs(t, auth.RoleAdmin)
	limitedAt := 0
	for i := 1; i <= 40 && limitedAt == 0; i++ {
		rec := mongoDo(admin, http.MethodPost, "/databases/"+mongoConn+"/mongo/command", `{"command":"{ drop: 'c' }"}`)
		switch {
		case rec.Code == http.StatusTooManyRequests:
			limitedAt = i
		case !passedTheGate(rec):
			t.Fatalf("request %d: %d %s", i, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
	if limitedAt == 0 {
		t.Error("40 destructive console commands in a row were never rate limited")
	}
	// A read is not counted against that budget.
	if rec := mongoDo(admin, http.MethodPost, "/databases/"+mongoConn+"/mongo/command", `{"command":"{ ping: 1 }"}`); !passedTheGate(rec) {
		t.Errorf("a read after the budget ran out: %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}

func TestMongoClassifyRoute(t *testing.T) {
	type answer struct {
		Verdict  dbx.MongoVerdict  `json:"verdict"`
		Requires []auth.Capability `json:"requires"`
		Allowed  bool              `json:"allowed"`
		Database string            `json:"database"`
	}
	ask := func(router http.Handler, text, database string) answer {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"command": text, "database": database})
		rec := mongoDo(router, http.MethodPost, "/databases/"+mongoConn+"/mongo/command/classify", string(body))
		if rec.Code != http.StatusOK {
			t.Fatalf("classify %s: %d %s", text, rec.Code, rec.Body.String())
		}
		var a answer
		if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	requires := func(a answer) string {
		out := []string{}
		for _, c := range a.Requires {
			out = append(out, string(c))
		}
		return strings.Join(out, "+")
	}
	_, readonly := mongoRouterAs(t, auth.RoleReadOnly)
	_, limited := mongoRouterAs(t, auth.RoleLimited)
	_, admin := mongoRouterAs(t, auth.RoleAdmin)

	if a := ask(readonly, `{ find: "c" }`, ""); a.Verdict.Class != dbx.MongoClassRead || requires(a) != "service.control" || a.Allowed || a.Database != "jdtest" {
		t.Errorf("find, read-only role: %+v", a)
	}
	if a := ask(limited, `{ find: "c" }`, "other"); !a.Allowed || a.Database != "other" || a.Verdict.Target != "c" {
		t.Errorf("find, limited role: %+v", a)
	}
	if a := ask(limited, `{ drop: "c" }`, ""); a.Allowed || requires(a) != "service.control+destructive" || a.Verdict.Reason == "" {
		t.Errorf("drop, limited role: %+v", a)
	}
	if a := ask(admin, `{ dropUser: "u" }`, ""); !a.Allowed || requires(a) != "service.control+system.admin+destructive" {
		t.Errorf("dropUser, administrator: %+v", a)
	}
	if a := ask(admin, `{ shutdown: 1 }`, ""); a.Allowed || a.Verdict.Class != dbx.MongoClassBlocked {
		t.Errorf("shutdown, administrator: %+v", a)
	}
	if a := ask(admin, `{ brandNewCommand: 1 }`, ""); a.Verdict.Known || a.Verdict.Class != dbx.MongoClassDestructive {
		t.Errorf("an unknown command: %+v", a)
	}
	rec := mongoDo(admin, http.MethodPost, "/databases/"+mongoConn+"/mongo/command/classify", `{"command":"{ ping: "}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("classifying a broken command: %d %s", rec.Code, rec.Body.String())
	}
	if rec := mongoDo(admin, http.MethodPost, "/databases/99/mongo/command/classify", `{"command":"{\"ping\":1}"}`); rec.Code != http.StatusNotFound {
		t.Errorf("classifying on a connection that does not exist: %d", rec.Code)
	}

	list := mongoDo(readonly, http.MethodGet, "/databases/"+mongoConn+"/mongo/commands", "")
	var commands struct {
		Commands []dbx.MongoVerdict `json:"commands"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &commands); err != nil || len(commands.Commands) < 100 {
		t.Errorf("command list: %d entries, %v", len(commands.Commands), err)
	}
}

// Unknown fields are refused on every route, so a client cannot believe it
// sent an option the server never read — "dryRun" misspelt would otherwise be
// a real delete.
func TestMongoRoutesRefuseUnknownFields(t *testing.T) {
	_, admin := mongoRouterAs(t, auth.RoleAdmin)
	for _, rq := range []mongoRoute{
		{http.MethodDelete, "/mongo/documents", `{"collection":"c","filter":"{\"a\":1}","many":true,"dry-run":true}`},
		{http.MethodPatch, "/mongo/documents", `{"collection":"c","filter":"{\"a\":1}","update":"{}","dry_run":true}`},
		{http.MethodPost, "/mongo/find", `{"collection":"c","where":"{}"}`},
		{http.MethodPost, "/mongo/command", `{"command":"{\"ping\":1}","db":"admin"}`},
		{http.MethodPost, "/mongo/indexes", `{"collection":"c","keys":[{"field":"a","type":"asc","order":1}]}`},
	} {
		rec := mongoDo(admin, rq.method, "/databases/"+mongoConn+rq.path, rq.body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "unknown field") {
			t.Errorf("%s %s with an unknown field: %d %s", rq.method, rq.path, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
}

// A refused request is on the audit trail under the name of what it tried,
// and a read is not on it at all.
func TestMongoRefusalsAreAudited(t *testing.T) {
	s, limited := mongoRouterAs(t, auth.RoleLimited)
	mongoDo(limited, http.MethodPost, "/databases/"+mongoConn+"/mongo/command", `{"command":"{ drop: 'orders' }"}`)
	mongoDo(limited, http.MethodPost, "/databases/"+mongoConn+"/mongo/command/classify", `{"command":"{ drop: 'orders' }"}`)
	var (
		entries, refused int
		action, detail   string
	)
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*), COALESCE(SUM(success = 0), 0), COALESCE(MAX(action), ''), COALESCE(MAX(detail), '') FROM audit_log`).
		Scan(&entries, &refused, &action, &detail); err != nil {
		t.Fatal(err)
	}
	if entries != 1 || refused != 1 {
		t.Fatalf("%d audit entries, %d of them refusals; want one refusal and nothing for the classify", entries, refused)
	}
	if action != "database.mongo.command" || !strings.Contains(detail, `"command":"drop"`) || !strings.Contains(detail, `"target":"orders"`) {
		t.Errorf("the refusal is recorded as %s %s", action, detail)
	}
}

// A server that cannot be reached is an error on the export route, with no
// file announced: the route this one supersedes answered an empty 200 that a
// browser saved as the collection.
func TestMongoExportConnectFailureIsAnError(t *testing.T) {
	_, router := mongoRouterAs(t, auth.RoleReadOnly)
	rec := mongoDo(router, http.MethodGet, "/databases/"+mongoConn+"/mongo/export?collection=orders&format=csv", "")
	if rec.Code != http.StatusBadGateway || mongoErrorCode(rec) != "connect_failed" {
		t.Errorf("export from an unreachable server: %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if rec.Header().Get("Content-Disposition") != "" || strings.Contains(rec.Header().Get("Content-Type"), "csv") {
		t.Errorf("the failed export still announced a file: %v", rec.Header())
	}
}

// The checks a protected connection applies to the Mongo routes that read or
// write by what they carry.
func TestMongoReadOnlyChecks(t *testing.T) {
	body := func(field, text string) []byte {
		b, _ := json.Marshal(map[string]string{"collection": "c", field: text})
		return b
	}
	for text, reads := range map[string]bool{
		`[{"$match":{"a":1}},{"$group":{"_id":null,"d":{"$mergeObjects":"$$ROOT"}}}]`: true,
		`[ { $match: { a: 1 } }, { $limit: 5 } ]`:                                     true,
		`[]`:                            true,
		`[{"$out":"x"}]`:                false,
		`[ { $merge: { into: 'x' } } ]`: false,
		`[{"\u0024out":"x"}]`:           false,
		`[{"$inventedStage":{}}]`:       false,
		`[{"$match":`:                   false,
		``:                              false,
	} {
		if err := mongoReadOnlyPipeline(body("pipeline", text)); (err == nil) != reads {
			t.Errorf("pipeline %s: %v, want reads=%v", text, err, reads)
		}
	}
	for text, reads := range map[string]bool{
		`{ find: "c" }`: true, `{ ping: 1 }`: true, `{ explain: { find: "c" } }`: true,
		`{ aggregate: "c", pipeline: [ { $match: {} } ], cursor: {} }`: true,
		`{ aggregate: "c", pipeline: [ { $out: "x" } ], cursor: {} }`:  false,
		`{ insert: "c", documents: [] }`:                               false,
		`{ drop: "c" }`:                                                false,
		`{ profile: 1 }`:                                               false,
		`{ shutdown: 1 }`:                                              false,
		`{ somethingNew: 1 }`:                                          false,
		`{ find: `:                                                     false,
		``:                                                             false,
	} {
		if err := mongoReadOnlyCommand(body("command", text)); (err == nil) != reads {
			t.Errorf("command %s: %v, want reads=%v", text, err, reads)
		}
	}
	for raw, reads := range map[string]bool{
		`{"collection":"c","filter":"{}","many":true,"dryRun":true}`: true,
		`{"collection":"c","filter":"{}","many":true}`:               false,
		`{"collection":"c","dryRun":false}`:                          false,
		`{"collection":"c","dryRun":"yes"}`:                          false,
		`not json`:                                                   false,
	} {
		if err := mongoReadOnlyDryRun([]byte(raw)); (err == nil) != reads {
			t.Errorf("dry run check of %s: %v, want reads=%v", raw, err, reads)
		}
	}
	// A body that is not an object, or carries the field as something other
	// than text, is not a read.
	for _, raw := range []string{`[]`, `{"pipeline":[{"$match":{}}]}`, `{"pipeline":5}`} {
		if err := mongoReadOnlyPipeline([]byte(raw)); err == nil {
			t.Errorf("pipeline body %s was allowed", raw)
		}
	}
}

func TestMongoAuditTextIsBounded(t *testing.T) {
	if got := mongoAuditText("  { a: 1 }  "); got != "{ a: 1 }" {
		t.Errorf("short text = %q", got)
	}
	long := mongoAuditText(strings.Repeat("ţ", 1000))
	if len(long) > 510 || !strings.HasSuffix(long, "…") || strings.ContainsRune(long, '\uFFFD') {
		t.Errorf("long text is %d bytes and ends %q", len(long), long[len(long)-6:])
	}
}
