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

// What the Redis routes decide before they reach a server: who may call
// them, what a body may contain, and what goes on the audit trail. The
// connection these run against points at a port nothing listens on, so a
// request that gets past its checks fails to connect — which is how a test
// here tells "allowed" from "refused".

// redisRouter mounts the database routes for a principal of the given role,
// behind the same audit middleware the real router has, with one Redis
// connection whose connection string is dsn.
func redisRouter(t *testing.T, role auth.Role, dsn string) (*Server, http.Handler, int64) {
	t.Helper()
	s := testServer(t)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{
				User: &auth.User{ID: 7, Username: "tester-" + string(role)},
				Role: role, Kind: "session", IP: "127.0.0.1",
			}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	r.Use(httpx.AuditMutations(s.Audit))
	s.mountDatabaseRoutes(r)
	sealed, err := s.Sealer.Seal(dsn)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Store.DB.Exec(
		`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`,
		"cache", string(dbx.DriverRedis), sealed, 0)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return s, r, id
}

// nowhere is a Redis address nothing listens on.
const nowhere = "redis://127.0.0.1:1/0"

type redisRoute struct{ method, path, body string }

// redisMutations is every Redis route that changes something, with a body
// that passes its validation, by the capability its route asks for. It is
// written out by hand: derived from the router, it would agree with the
// router whatever the router said.
var redisMutations = map[auth.Capability][]redisRoute{
	auth.CapServiceControl: {
		{http.MethodPost, "/keys/value", `{"key":"k","value":"v"}`},
		{http.MethodPost, "/keys/expire", `{"key":"k","ttl":60}`},
		{http.MethodPost, "/keys/persist", `{"key":"k"}`},
		{http.MethodPost, "/keys/rename", `{"key":"k","to":"k2"}`},
		{http.MethodPost, "/keys/copy", `{"key":"k","to":"k2"}`},
		{http.MethodPost, "/keys/bulk", `{"pattern":"k*","action":"persist"}`},
		{http.MethodPost, "/keys/bulk", `{"pattern":"k*","action":"delete","dryRun":true}`},
		{http.MethodPost, "/keys/stream/groups", `{"key":"s","group":"g"}`},
		{http.MethodPost, "/keys/stream/ack", `{"key":"s","group":"g","ids":["1-0"]}`},
		{http.MethodPost, "/redis/command", `{"command":"SET k v"}`},
		{http.MethodPost, "/redis/command", `{"command":"GET k"}`},
		{http.MethodPost, "/redis/save", `{"mode":"bgsave"}`},
		{http.MethodPost, "/redis/publish", `{"channel":"c","message":"m"}`},
		// An expiry that is still to come is a write, however soon.
		{http.MethodPost, "/keys/expire", `{"key":"k","ttlMs":1}`},
		{http.MethodPost, "/redis/command", `{"command":"PEXPIRE k 1"}`},
	},
	auth.CapDestructive: {
		{http.MethodDelete, "/keys", `{"key":"k"}`},
		{http.MethodDelete, "/keys", `{"key":"k","member":"m"}`},
		{http.MethodPost, "/keys/stream/trim", `{"key":"s","maxLen":10}`},
		{http.MethodDelete, "/keys/stream/groups", `{"key":"s","group":"g"}`},
		{http.MethodPost, "/redis/clients/kill", `{"id":12}`},
		{http.MethodPost, "/redis/slowlog/reset", ``},
		// The same path as routine requests above; the body is what makes
		// these destructive.
		{http.MethodPost, "/keys/rename", `{"key":"k","to":"k2","overwrite":true}`},
		{http.MethodPost, "/keys/copy", `{"key":"k","to":"k2","overwrite":true}`},
		{http.MethodPost, "/keys/bulk", `{"pattern":"k*","action":"delete"}`},
		{http.MethodPost, "/keys/bulk", `{"pattern":"k*","action":"expire","ttl":5}`},
		{http.MethodPost, "/redis/command", `{"command":"DEL k"}`},
		{http.MethodPost, "/redis/command", `{"command":"HDEL h f"}`},
		{http.MethodPost, "/redis/command", `{"command":"FLUSHALL"}`},
		{http.MethodPost, "/redis/command", `{"command":"EVAL \"return 1\" 0"}`},
		{http.MethodPost, "/redis/command", `{"command":"EXPIRE k 0"}`},
		// Storing over a key deletes it when there is nothing to store, and
		// an expiry that has passed is a delete whichever command sets it.
		{http.MethodPost, "/redis/command", `{"command":"SINTERSTORE victim nosuch"}`},
		{http.MethodPost, "/redis/command", `{"command":"ZRANGESTORE victim nosuch 0 -1"}`},
		{http.MethodPost, "/redis/command", `{"command":"SORT nosuch STORE victim"}`},
		{http.MethodPost, "/redis/command", `{"command":"BITOP AND victim nosuch"}`},
		{http.MethodPost, "/redis/command", `{"command":"SET k v PXAT 1"}`},
		{http.MethodPost, "/redis/command", `{"command":"GETEX k PXAT 1"}`},
		{http.MethodPost, "/redis/command", `{"command":"HEXPIRE h 0 FIELDS 1 f"}`},
	},
	auth.CapSystemAdmin: {
		{http.MethodPut, "/redis/config", `{"name":"maxmemory","value":"1gb"}`},
		{http.MethodPut, "/redis/acl/app", `{"create":true,"password":"s3cret-password"}`},
		{http.MethodDelete, "/redis/acl/app", ``},
		{http.MethodGet, "/redis/monitor", ``},
		{http.MethodGet, "/redis/subscribe?channel=c", ``},
		{http.MethodGet, "/redis/subscribe?pattern=*", ``},
		{http.MethodPost, "/redis/command", `{"command":"CONFIG SET maxmemory 1gb"}`},
		{http.MethodPost, "/redis/command", `{"command":"CONFIG GET requirepass"}`},
		{http.MethodPost, "/redis/command", `{"command":"ACL LIST"}`},
		{http.MethodPost, "/redis/command", `{"command":"ACL SETUSER app on"}`},
		{http.MethodPost, "/redis/command", `{"command":"DEBUG OBJECT k"}`},
	},
}

// redisReads is every route on the read surface.
var redisReads = []redisRoute{
	{http.MethodGet, "/keys", ``},
	{http.MethodGet, "/keys/value?key=k", ``},
	{http.MethodGet, "/keys/tree", ``},
	{http.MethodGet, "/keys/meta?key=k", ``},
	{http.MethodGet, "/keys/members?key=k", ``},
	{http.MethodGet, "/keys/raw?key=k", ``},
	{http.MethodGet, "/keys/stream?key=k", ``},
	{http.MethodGet, "/keys/stream/pending?key=k&group=g", ``},
	{http.MethodGet, "/redis/server", ``},
	{http.MethodGet, "/redis/commandstats", ``},
	{http.MethodGet, "/redis/latency", ``},
	{http.MethodGet, "/redis/clients", ``},
	{http.MethodGet, "/redis/config", ``},
	{http.MethodGet, "/redis/slowlog", ``},
	{http.MethodGet, "/redis/analysis", ``},
	{http.MethodGet, "/redis/commands", ``},
	{http.MethodGet, "/redis/acl", ``},
	{http.MethodGet, "/redis/pubsub", ``},
}

func redisDo(t *testing.T, h http.Handler, id int64, rt redisRoute) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, h, rt.method, pathf("/databases/%d", id)+rt.path, rt.body)
}

// redisErrorCode reads the code out of an error reply; a reply that is not
// an error has none.
func redisErrorCode(rec *httptest.ResponseRecorder) string {
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Error.Code
}

// Capability lives on the route — or, where the body decides, in the handler
// — and never in the page alone. Every mutation is tried as each role.
func TestRedisRoutesAskForTheirCapability(t *testing.T) {
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited, auth.RoleAdmin} {
		_, h, id := redisRouter(t, role, nowhere)
		for capability, routes := range redisMutations {
			for _, rt := range routes {
				if role.Can(auth.CapDestructive) {
					// Each allowed destructive request spends the destructive
					// budget, and there are more of them here than it holds.
					// A server of its own per request keeps this test about
					// capabilities; the budget has its own below.
					_, h, id = redisRouter(t, role, nowhere)
				}
				rec := redisDo(t, h, id, rt)
				code := redisErrorCode(rec)
				if role.Can(capability) && role.Can(auth.CapServiceControl) {
					// Past every check, and stopped only by the server that
					// is not there.
					if rec.Code != http.StatusBadGateway || code != "connect_failed" {
						t.Errorf("%s may %s %s %s, but got %d %s", role, rt.method, rt.path, rt.body, rec.Code, strings.TrimSpace(rec.Body.String()))
					}
					continue
				}
				if rec.Code != http.StatusForbidden || code != "forbidden" {
					t.Errorf("%s lacks %s, but %s %s %s answered %d %s", role, capability, rt.method, rt.path, rt.body, rec.Code, strings.TrimSpace(rec.Body.String()))
				}
			}
		}
		// Reading is open to every role.
		for _, rt := range redisReads {
			if rec := redisDo(t, h, id, rt); rec.Code == http.StatusForbidden {
				t.Errorf("%s was refused the read %s: %s", role, rt.path, strings.TrimSpace(rec.Body.String()))
			}
		}
	}
}

// A refused command never reaches the server. The connection here would
// fail, so a 400 can only have come from the classifier.
func TestRedisConsoleRefusesBlockedCommandsBeforeDialling(t *testing.T) {
	_, h, id := redisRouter(t, auth.RoleAdmin, nowhere)
	for _, command := range []string{
		"SUBSCRIBE ch", "PSUBSCRIBE *", "MONITOR", "SYNC", "PSYNC ? -1", "SHUTDOWN", "SHUTDOWN NOSAVE",
		"MIGRATE h 6379 k 0 100", "CLIENT PAUSE 1000", "BLPOP q 0", "BLPOP q 3600", "BRPOP q 0",
		"XREAD BLOCK 0 STREAMS s $", "MULTI", "EXEC", "SELECT 2", "AUTH x", "DEBUG SEGFAULT", "DEBUG SLEEP 60",
	} {
		body, _ := json.Marshal(map[string]string{"command": command})
		rec := redisDo(t, h, id, redisRoute{http.MethodPost, "/redis/command", string(body)})
		if rec.Code != http.StatusBadRequest || redisErrorCode(rec) != "command_blocked" {
			t.Errorf("%q answered %d %s", command, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
	for _, body := range []string{`{"command":""}`, `{"command":"   "}`, `{"command":"SET k \"unterminated"}`, `{}`} {
		if rec := redisDo(t, h, id, redisRoute{http.MethodPost, "/redis/command", body}); rec.Code != http.StatusBadRequest {
			t.Errorf("%s answered %d %s", body, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
}

// Saying what a command would need is open to anyone and runs nothing, so a
// page can decide whether to offer it — and the answer is the one the console
// would act on.
func TestRedisClassifyAnswersForTheCallersRole(t *testing.T) {
	type verdict struct {
		Name     string   `json:"name"`
		Class    string   `json:"class"`
		Admin    bool     `json:"admin"`
		Slow     bool     `json:"slow"`
		Known    bool     `json:"known"`
		Reasons  []string `json:"reasons"`
		Requires []string `json:"requires"`
		Allowed  bool     `json:"allowed"`
	}
	cases := []struct {
		command  string
		class    string
		requires string
		allowed  map[auth.Role]bool
	}{
		{"GET k", "read", "service.control",
			map[auth.Role]bool{auth.RoleReadOnly: false, auth.RoleLimited: true, auth.RoleAdmin: true}},
		{"SET k v", "write", "service.control",
			map[auth.Role]bool{auth.RoleReadOnly: false, auth.RoleLimited: true, auth.RoleAdmin: true}},
		{"DEL k", "dangerous", "service.control destructive",
			map[auth.Role]bool{auth.RoleReadOnly: false, auth.RoleLimited: false, auth.RoleAdmin: true}},
		{"CONFIG GET maxmemory", "read", "service.control system.admin",
			map[auth.Role]bool{auth.RoleReadOnly: false, auth.RoleLimited: false, auth.RoleAdmin: true}},
		{"CONFIG SET maxmemory 1", "dangerous", "service.control system.admin destructive",
			map[auth.Role]bool{auth.RoleReadOnly: false, auth.RoleLimited: false, auth.RoleAdmin: true}},
		{"SUBSCRIBE c", "blocked", "service.control",
			map[auth.Role]bool{auth.RoleReadOnly: false, auth.RoleLimited: false, auth.RoleAdmin: false}},
	}
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited, auth.RoleAdmin} {
		s, h, id := redisRouter(t, role, nowhere)
		for _, c := range cases {
			body, _ := json.Marshal(map[string]string{"command": c.command})
			rec := redisDo(t, h, id, redisRoute{http.MethodPost, "/redis/classify", string(body)})
			if rec.Code != http.StatusOK {
				t.Fatalf("%s classifying %q: %d %s", role, c.command, rec.Code, rec.Body.String())
			}
			var v verdict
			if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
				t.Fatal(err)
			}
			if v.Class != c.class || strings.Join(v.Requires, " ") != c.requires || v.Allowed != c.allowed[role] || !v.Known || v.Reasons == nil {
				t.Errorf("%s classifying %q = %+v, want %s needing %q allowed=%v", role, c.command, v, c.class, c.requires, c.allowed[role])
			}
		}
		// A question, not an action: nothing is recorded.
		var audited int
		if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log`).Scan(&audited); err != nil || audited != 0 {
			t.Errorf("classifying wrote %d audit entries, %v", audited, err)
		}
	}
}

// The routes whose destructiveness is in the body spend the destructive
// budget the same as the ones behind s.destructive, so a scripted loop of
// FLUSHALL is throttled like a scripted loop of DELETE.
func TestRedisContentDependentRoutesSpendTheDestructiveBudget(t *testing.T) {
	for _, rt := range []redisRoute{
		{http.MethodPost, "/redis/command", `{"command":"FLUSHALL"}`},
		{http.MethodPost, "/keys/bulk", `{"pattern":"k*","action":"delete"}`},
		{http.MethodPost, "/keys/rename", `{"key":"a","to":"b","overwrite":true}`},
	} {
		_, h, id := redisRouter(t, auth.RoleAdmin, nowhere)
		limited := false
		for i := 0; i < 40 && !limited; i++ {
			rec := redisDo(t, h, id, rt)
			limited = rec.Code == http.StatusTooManyRequests && redisErrorCode(rec) == "rate_limited"
		}
		if !limited {
			t.Errorf("forty of %s %s in a row were never rate limited", rt.path, rt.body)
		}
		// The routine form of the same route is not on that budget.
		routine := redisRoute{http.MethodPost, "/redis/command", `{"command":"GET k"}`}
		if rec := redisDo(t, h, id, routine); rec.Code == http.StatusTooManyRequests {
			t.Error("a read was refused on the destructive budget")
		}
	}
}

// No Redis route asks for a typed phrase. The set of operations that do is
// closed, and a key or a consumer group is not a database.
func TestRedisRoutesDoNotAskForAPhrase(t *testing.T) {
	_, h, id := redisRouter(t, auth.RoleAdmin, nowhere)
	for _, routes := range redisMutations {
		for _, rt := range routes {
			rec := redisDo(t, h, id, rt)
			if rec.Code == http.StatusPreconditionRequired || rec.Code == http.StatusPreconditionFailed ||
				strings.Contains(rec.Body.String(), "confirmation") {
				t.Errorf("%s %s %s asked for a phrase: %d %s", rt.method, rt.path, rt.body, rec.Code, strings.TrimSpace(rec.Body.String()))
			}
		}
	}
}

func TestRedisRoutesRefuseOtherEngines(t *testing.T) {
	_, h := dbTestRouter(t)
	all := append([]redisRoute{}, redisReads...)
	all = append(all, redisRoute{http.MethodPost, "/redis/classify", `{"command":"NOTACOMMAND"}`})
	for _, routes := range redisMutations {
		all = append(all, routes...)
	}
	for _, rt := range all {
		rec := redisDo(t, h, 1, rt)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Redis connections") {
			t.Errorf("%s %s against a SQLite connection answered %d %s", rt.method, rt.path, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
}

func TestRedisRequestsAreValidatedBeforeDialling(t *testing.T) {
	_, h, id := redisRouter(t, auth.RoleAdmin, nowhere)
	for _, rt := range []redisRoute{
		// A database is a number, and "no number" is not "zero".
		{http.MethodGet, "/keys?db=first", ``},
		{http.MethodGet, "/keys?db=-1", ``},
		{http.MethodGet, "/keys?cursor=abc", ``},
		{http.MethodGet, "/keys/meta", ``},
		{http.MethodGet, "/keys/members", ``},
		{http.MethodGet, "/keys/members?keyB64=***", ``},
		{http.MethodGet, "/keys/members?key=k&order=sideways", ``},
		{http.MethodGet, "/keys/raw", ``},
		{http.MethodGet, "/keys/raw?key=k&index=first", ``},
		{http.MethodGet, "/keys/raw?key=k&fieldB64=***", ``},
		{http.MethodGet, "/keys/stream/pending?key=k", ``},
		{http.MethodGet, "/keys/tree?cursor=abc", ``},
		// The decoder refuses a field it does not know, on every route.
		{http.MethodPost, "/keys/value", `{"key":"k","value":"v","nope":1}`},
		{http.MethodPost, "/keys/value", `{"key":"k","value":{"base64":"AA==","extra":1}}`},
		{http.MethodPost, "/keys/value", `{"key":"k","value":{"base64":"not base64"}}`},
		{http.MethodPost, "/keys/value", `{"value":"v"}`},
		{http.MethodPost, "/keys/value", `{"key":"k","type":"list","value":"v","position":"middle"}`},
		{http.MethodPost, "/keys/rename", `{"key":"k"}`},
		{http.MethodPost, "/keys/copy", `{"to":"k2"}`},
		{http.MethodPost, "/keys/bulk", `{"pattern":"*","action":"flush"}`},
		{http.MethodPost, "/keys/bulk", `{"pattern":"*","action":"delete","cursor":"x"}`},
		{http.MethodDelete, "/keys", `{}`},
		{http.MethodDelete, "/keys", `{"keys":["a","b"],"member":"m"}`},
		{http.MethodDelete, "/keys", `{"key":"l","type":"list","member":"first"}`},
		// Every key there is: that is the database, and it has its own route.
		{http.MethodPost, "/keys/bulk", `{"pattern":"*","action":"delete"}`},
		{http.MethodPost, "/keys/bulk", `{"pattern":"**","action":"expire","ttl":5}`},
		{http.MethodPost, "/keys/stream/trim", `{"maxLen":5}`},
		{http.MethodPost, "/redis/save", `{"mode":"save"}`},
		{http.MethodPost, "/redis/publish", `{"message":"m"}`},
		{http.MethodPut, "/redis/config", `{"value":"1"}`},
		{http.MethodPut, "/redis/config", `{"name":"maxmemory","value":"1","secret":true}`},
		{http.MethodPost, "/redis/command", `{"command":"GET k","db":-2}`},
		{http.MethodGet, "/redis/subscribe", ``},
		{http.MethodGet, "/redis/subscribe?channel=", ``},
	} {
		rec := redisDo(t, h, id, rt)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s %s answered %d %s", rt.method, rt.path, rt.body, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
	// A body that is not JSON is refused as one, as everywhere.
	req := httptest.NewRequest(http.MethodPost, pathf("/databases/%d/keys/value", id), strings.NewReader(`{"key":"k","value":"v"}`))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("a text/plain body answered %d", rec.Code)
	}
}

func TestRedisDBParameter(t *testing.T) {
	for raw, want := range map[string]int{"": dbx.RedisDSNDatabase, "0": 0, "3": 3, "15": 15} {
		got, err := redisDB(raw)
		if err != nil || got != want {
			t.Errorf("redisDB(%q) = %d, %v; want %d", raw, got, err, want)
		}
	}
	// "No database named" and "database 0" are different requests.
	if none, _ := redisDB(""); none == 0 {
		t.Error("no ?db= reads as database 0")
	}
	for _, raw := range []string{"-1", "abc", "1.5", "99999999"} {
		if got, err := redisDB(raw); err == nil {
			t.Errorf("redisDB(%q) = %d, want an error", raw, got)
		}
	}
}

func TestRedisKeyParam(t *testing.T) {
	q := func(raw string) (dbx.RedisBytes, error) {
		req := httptest.NewRequest(http.MethodGet, "/x?"+raw, nil)
		return redisKeyParam(req.URL.Query())
	}
	if k, err := q("key=user%3A1"); err != nil || k != "user:1" {
		t.Errorf("key = %q, %v", k, err)
	}
	// Bytes that are not text have no other way into a query string.
	if k, err := q("keyB64=%2FwD%2B"); err != nil || string(k) != "\xff\x00\xfe" {
		t.Errorf("keyB64 = %q, %v", k, err)
	}
	// Nor does the key whose name is empty.
	if k, err := q("keyB64="); err != nil || k != "" {
		t.Errorf("the empty key = %q, %v", k, err)
	}
	for _, raw := range []string{"", "key=", "keyB64=%%%"} {
		if k, err := q(raw); err == nil {
			t.Errorf("%q was accepted as %q", raw, k)
		}
	}
}

// A request that addresses the inside of a key never reaches the whole of
// it. Which of the two a request is comes from the fields it carries, and one
// that carries a member field naming nothing is refused: the same bodies used
// to be read as "delete the key".
func TestRedisDeleteReadsPresenceNotEmptiness(t *testing.T) {
	_, h, id := redisRouter(t, auth.RoleAdmin, nowhere)
	for body, want := range map[string]string{
		`{"key":"k","members":[]}`:                       "at least one member",
		`{"key":"k","type":"set","members":[]}`:          "at least one member",
		`{"key":"k","expect":"x"}`:                       "at least one member",
		`{"keys":["a","b"],"members":[]}`:                "one key at a time",
		`{"keys":["a","b"],"path":"$.user.email"}`:       "one key at a time",
		`{"key":"k","type":"set","path":"$.user.email"}`: "JSON document",
		`{"key":"k","path":"$.user.email","member":"m"}`: "not both",
		// An empty path is the root, and the root is the whole document.
		`{"key":"k","path":""}`:               "name the path",
		`{"key":"k","type":"json","path":""}`: "name the path",
	} {
		rec := redisDo(t, h, id, redisRoute{http.MethodDelete, "/keys", body})
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s answered %d %s", body, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
	// These get past validation, so here they stop at the server that is not
	// there — on their way to remove a member, not a key.
	_, h, id = redisRouter(t, auth.RoleAdmin, nowhere)
	for _, body := range []string{
		`{"key":"k","member":""}`,
		`{"key":"k","members":[""]}`,
		`{"key":"k","index":0}`,
		`{"key":"k","path":"$.user.email"}`,
		`{"key":"k","type":"json","path":"$"}`,
		// null is how a client spells "not sent".
		`{"key":"k","members":null,"member":null,"path":null}`,
	} {
		rec := redisDo(t, h, id, redisRoute{http.MethodDelete, "/keys", body})
		if rec.Code != http.StatusBadGateway || redisErrorCode(rec) != "connect_failed" {
			t.Errorf("%s answered %d %s", body, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
}

// Every key in the database is the database. Emptying one asks for its name
// on a route of its own, and the pattern box is not that route.
func TestRedisBulkRefusesTheWholeDatabase(t *testing.T) {
	_, h, id := redisRouter(t, auth.RoleAdmin, nowhere)
	for body, whole := range map[string]bool{
		`{"pattern":"*","action":"delete"}`:               true,
		`{"pattern":"***","action":"delete"}`:             true,
		`{"pattern":"*","action":"expire","ttl":60}`:      true,
		`{"pattern":"*","action":"delete","dryRun":true}`: false,
		`{"pattern":"*","action":"persist"}`:              false,
		`{"pattern":"*","type":"hash","action":"delete"}`: false,
		`{"pattern":"session:*","action":"delete"}`:       false,
		`{"pattern":"\\*","action":"delete"}`:             false,
	} {
		rec := redisDo(t, h, id, redisRoute{http.MethodPost, "/keys/bulk", body})
		if got := rec.Code == http.StatusBadRequest && redisErrorCode(rec) == "whole_database"; got != whole {
			t.Errorf("%s answered %d %s", body, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
}

// One quoted word that spells "CONFIG GET" used to be read as that command
// and index past the end of its arguments. It is a command nobody knows.
func TestRedisConsoleSurvivesAQuotedCommandName(t *testing.T) {
	for _, role := range []auth.Role{auth.RoleLimited, auth.RoleAdmin} {
		s, h, id := redisRouter(t, role, nowhere)
		for _, command := range []string{`"CONFIG GET"`, `'acl deluser'`, `"CONFIG SET" a b`, `"ACL DELUSER"`, `"XINFO STREAM" s`} {
			body, _ := json.Marshal(map[string]string{"command": command})
			for _, path := range []string{"/redis/command", "/redis/classify"} {
				rec := redisDo(t, h, id, redisRoute{http.MethodPost, path, string(body)})
				// Unknown to the table, so the server is asked, and it is not
				// there. Anything but a 500.
				if rec.Code != http.StatusBadGateway || redisErrorCode(rec) != "connect_failed" {
					t.Errorf("%s %s as %s answered %d %s", path, command, role, rec.Code, strings.TrimSpace(rec.Body.String()))
				}
			}
		}
		// The attempt is on the trail, which a panic used to unwind past.
		var recorded int
		if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'database.redis.command'`).Scan(&recorded); err != nil || recorded != 5 {
			t.Errorf("%d of 5 console attempts as %s were recorded, %v", recorded, role, err)
		}
	}
}

// The guard for protected connections judges a body before its handler sees
// it. These are what it is given to judge with, and they read the body as
// the handler will.
func TestRedisReadOnlyChecksJudgeWhatTheHandlerDecodes(t *testing.T) {
	commands := map[string]bool{
		`{"command":"GET k"}`:                            true,
		`{"command":"hgetall h","db":3}`:                 true,
		`{"command":"SCAN 0 MATCH x*"}`:                  true,
		`{"command":"XREAD COUNT 5 STREAMS s 0"}`:        true,
		`{"command":"SET k v"}`:                          false,
		`{"command":"DEL k"}`:                            false,
		`{"command":"FLUSHALL"}`:                         false,
		`{"command":"CONFIG SET maxmemory 1"}`:           false,
		`{"command":"SUBSCRIBE c"}`:                      false,
		`{"command":"EVAL_RO \"return 1\" 0"}`:           false,
		`{"command":"XREADGROUP GROUP g c STREAMS s >"}`: false,
		// Not in the table: the server would have to vouch for it, and a
		// guard does not dial.
		`{"command":"MODULE.READ k"}`:  false,
		`{"command":"\"CONFIG GET\""}`: false,
		// The decoder takes a field in any case and the last of two, so the
		// judgement is made on what it yields, not on a lookup by name.
		`{"command":"GET k","Command":"FLUSHALL"}`: false,
		`{"COMMAND":"FLUSHALL"}`:                   false,
		`{"Command":"GET k"}`:                      true,
		// Nothing to judge is not a read.
		`{}`:                               false,
		`{"command":""}`:                   false,
		`{"command":"GET k","extra":true}`: false,
		`[{"command":"GET k"}]`:            false,
		`{"command":"GET k"} {}`:           false,
		`not json`:                         false,
	}
	for body, read := range commands {
		why := redisCommandWrites([]byte(body))
		if (why == "") != read {
			t.Errorf("redisCommandWrites(%s) = %q, want read=%v", body, why, read)
		}
	}
	// The reason is recorded with the refusal, so it carries the command's
	// name and never what was typed after it or in its place.
	for _, body := range []string{`{"command":"SET session:9 S3CRET-token"}`, `{"command":"S3CRET-token"}`} {
		if why := redisCommandWrites([]byte(body)); why == "" || strings.Contains(why, "S3CRET") {
			t.Errorf("redisCommandWrites(%s) = %q", body, why)
		}
	}

	bulk := map[string]bool{
		`{"pattern":"k*","action":"delete","dryRun":true}`:                 true,
		`{"pattern":"k*","action":"expire","ttl":5,"dryRun":true}`:         true,
		`{"pattern":"k*","action":"delete"}`:                               false,
		`{"pattern":"k*","action":"persist"}`:                              false,
		`{"pattern":"k*","action":"delete","dryRun":false}`:                false,
		`{"pattern":"k*","action":"delete","dryRun":true,"DryRun":false}`:  false,
		`{"pattern":"k*","action":"delete","dryrun":true}`:                 true,
		`{"pattern":"k*","action":"delete","dryRun":true,"confirm":"yes"}`: false,
		`{"pattern":"k*","action":"delete","dryRun":"true"}`:               false,
		``: false,
	}
	for body, harmless := range bulk {
		why := redisBulkWrites([]byte(body))
		if (why == "") != harmless {
			t.Errorf("redisBulkWrites(%s) = %q, want harmless=%v", body, why, harmless)
		}
	}
}

// Redis has no database to grant an account: its permissions are the rules of
// an ACL user. Asking for the three-level grant is the request's mistake and
// is answered as one, with or without a preview — not as a read of the server
// that failed, which it used to be, logged as an error for a request that
// dialled nothing.
func TestRedisGrantIsARefusalOfTheRequestNotAFailedRead(t *testing.T) {
	_, r, id := redisRouter(t, auth.RoleAdmin, nowhere)
	for _, path := range []string{"/server/roles/app/grant", "/server/roles/app/grant?preview=1"} {
		rec := do(t, r, http.MethodPost, pathf("/databases/%d", id)+path, `{"level":"read"}`)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "bad_request") ||
			!strings.Contains(rec.Body.String(), "ACL rules") {
			t.Errorf("POST %s = %d %s, want 400 bad_request naming the ACL rules", path, rec.Code, rec.Body.String())
		}
	}
}
