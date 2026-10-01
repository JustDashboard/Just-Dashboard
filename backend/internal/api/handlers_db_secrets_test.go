package api

import (
	"bytes"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// A connection string holds a password, and a driver that cannot use the
// string says so by quoting it. What a driver says is what a failed request
// answers with, what a refused mutation's audit entry records and what the
// process logs — three places a password must never be written.
//
// So every route under a connection is asked, for every engine family, with a
// connection string its driver refuses for the way it is written, and the
// password has to be in none of the three. A handler that opens a connection
// of its own and passes on what the driver said is caught here whichever file
// it is in, and so is one added tomorrow.
func TestNoRouteQuotesAConnectionStringsPassword(t *testing.T) {
	const secret = "s3cr3t-never-on-the-wire"
	// How many answers had a password taken out of them. None would mean no
	// driver here quotes a string any more, and that this test has stopped
	// proving anything.
	blanked := 0
	for _, c := range []struct {
		driver dbx.Driver
		dsn    string
	}{
		// A port that is not a number: net/url, which most drivers parse
		// with, answers by quoting the whole string.
		{dbx.DriverRedis, "redis://:" + secret + "@127.0.0.1:port/0"},
		{dbx.DriverMongo, "mongodb://app:" + secret + "@127.0.0.1:port/shop"},
		{dbx.DriverPostgres, "postgres://app:" + secret + "@127.0.0.1:port/shop"},
		{dbx.DriverClickHouse, "clickhouse://app:" + secret + "@127.0.0.1:port/shop"},
		{dbx.DriverMSSQL, "sqlserver://sa:" + secret + "@127.0.0.1:port?database=shop"},
		{dbx.DriverOracle, "oracle://app:" + secret + "@127.0.0.1:port/shop"},
		// An address nothing listens on: the string is read, and the dial
		// is what fails. (Not MongoDB, which waits eight seconds for a server
		// to appear before each of its seventy routes gives up.)
		{dbx.DriverRedis, "redis://:" + secret + "@127.0.0.1:1/0"},
		{dbx.DriverMySQL, "app:" + secret + "@tcp(127.0.0.1:1)/shop"},
	} {
		t.Run(string(c.driver)+" "+strings.SplitN(strings.SplitN(c.dsn, "@", 2)[1], "/", 2)[0], func(t *testing.T) {
			// The harness stands in for Docker, the machine's sockets and
			// units, and systemctl: every route is asked, and none of them is
			// to reach this machine.
			s := newConnHarness(t).s
			t.Cleanup(s.Shutdown)
			sealed, err := s.Sealer.Seal(c.dsn)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Store.DB.Exec(
				`INSERT INTO db_connections(id, name, driver, dsn_enc, created_at) VALUES(1,'leaky',?,?,0)`,
				string(c.driver), sealed); err != nil {
				t.Fatal(err)
			}
			// Everything the process would log, by either logger.
			var logged bytes.Buffer
			previousLog, previousSlog := log.Writer(), slog.Default()
			log.SetOutput(&logged)
			slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
			t.Cleanup(func() {
				log.SetOutput(previousLog)
				slog.SetDefault(previousSlog)
			})

			// Several hundred requests in a second from one account is what the
			// request budgets exist to stop, and a request turned away at the
			// door has tested nothing. They are lifted here and nowhere else.
			s.apiLim, s.destrLim = httpx.NewLimiter(1<<20, 1<<20), httpx.NewLimiter(1<<20, 1<<20)
			handler := s.Routes()
			client := &client{t: t, h: handler, cookie: signIn(t, s)}
			asked, failed, scrubbed, limited := 0, 0, 0, 0
			for _, rt := range apiRoutes(t, handler) {
				if !strings.HasPrefix(rt.pattern, connectionRoutePrefix+"/") {
					continue
				}
				rest := strings.TrimPrefix(rt.pattern, connectionRoutePrefix)
				switch {
				case rest == "/redis/monitor" || rest == "/redis/subscribe":
					// A WebSocket upgrade cannot be asked through a recorder.
					continue
				case rt.method != http.MethodGet && (rest == "/access" || rest == "/power"):
					// These act on the machine and not on the connection.
					continue
				}
				body := ""
				if rt.method != http.MethodGet {
					body = `{}`
				}
				rec := client.do(rt.method, rt.path, body, map[string]string{httpx.ConfirmHeader: "shop"})
				asked++
				if rec.Code >= http.StatusInternalServerError {
					failed++
				}
				if rec.Code == http.StatusTooManyRequests {
					limited++
				}
				if strings.Contains(rec.Body.String(), "***") {
					scrubbed++
				}
				if rest == "/url" && rt.method == http.MethodGet {
					// The one route that exists to hand the connection string
					// to an administrator who asks for it. What it leaves in
					// the audit trail and the log is still held to the rule.
					continue
				}
				if strings.Contains(rec.Body.String(), secret) {
					t.Errorf("%s %s answered with the password: %d %s", rt.method, rt.pattern, rec.Code, strings.TrimSpace(rec.Body.String()))
				}
			}
			if asked < 150 {
				t.Fatalf("only %d routes were asked; the walk is not seeing the real router", asked)
			}
			blanked += scrubbed
			if failed == 0 {
				t.Fatalf("no route failed to connect, so nothing was put to the test")
			}
			// An empty body stops at the door of every route that reads its
			// request before it dials, and the driver is never asked. Those
			// routes are asked again with requests they accept.
			known := map[string]bool{}
			for _, rt := range apiRoutes(t, handler) {
				known[rt.method+" "+rt.pattern] = true
			}
			dialled := 0
			for _, rq := range dialingRequests(c.driver) {
				path, _, _ := strings.Cut(rq.path, "?")
				if pattern := connectionRoutePrefix + dialingPattern(path); !known[rq.method+" "+pattern] {
					t.Errorf("%s %s is asked here and is no route: the table has fallen behind the router", rq.method, pattern)
					continue
				}
				rec := client.do(rq.method, "/api/v1/databases/1"+rq.path, rq.body, map[string]string{httpx.ConfirmHeader: "shop"})
				if rec.Code >= http.StatusInternalServerError {
					dialled++
				}
				if rec.Code == http.StatusTooManyRequests {
					limited++
				}
				if strings.Contains(rec.Body.String(), "***") {
					blanked++
				}
				if strings.Contains(rec.Body.String(), secret) {
					t.Errorf("%s %s with %s answered with the password: %d %s", rq.method, rq.path, rq.body, rec.Code, strings.TrimSpace(rec.Body.String()))
				}
			}
			if limited > 0 {
				t.Errorf("%d requests were turned away by a request budget and tested nothing", limited)
			}
			if dialled < 15 {
				t.Errorf("only %d of the requests written to pass validation reached the driver; their bodies no longer fit their routes", dialled)
			}
			// A dump, a copy and an import end in a job, which reports what
			// the driver said after the request has been answered.
			for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
				running := false
				for _, job := range s.modules.jobs.List() {
					running = running || job.EndedAt == nil
				}
				if !running {
					break
				}
			}
			for _, job := range s.modules.jobs.List() {
				_, lines, _ := s.modules.jobs.Get(job.ID)
				if text := fmt.Sprintf("%+v %+v", job, lines); strings.Contains(text, secret) {
					at := strings.Index(text, secret)
					t.Errorf("the job %s carries the password: …%s…", job.Title, text[max(0, at-200):min(len(text), at+60)])
				}
			}
			var leaked int
			if err := s.Store.DB.QueryRow(
				`SELECT COUNT(*) FROM audit_log WHERE detail LIKE '%' || ? || '%' OR target LIKE '%' || ? || '%'`,
				secret, secret).Scan(&leaked); err != nil {
				t.Fatal(err)
			}
			if leaked != 0 {
				t.Errorf("%d audit entries carry the password", leaked)
			}
			if text := logged.String(); strings.Contains(text, secret) {
				at := strings.Index(text, secret)
				t.Errorf("the process log carries the password: …%s…", text[max(0, at-200):min(len(text), at+60)])
			}
		})
	}
	if blanked == 0 {
		t.Error("no driver quoted its connection string, so no answer needed its password taken out: find one that does")
	}
}

type dialingRequest struct{ method, path, body string }

// dialingPattern is the route a request in the tables below is for: the two
// names they use are the only path parameters under a connection they reach.
func dialingPattern(path string) string {
	for from, to := range map[string]string{
		"/server/roles/r1": "/server/roles/{name}", "/server/extensions/pg_trgm": "/server/extensions/{name}",
		"/redis/acl/u1": "/redis/acl/{name}",
	} {
		if strings.HasPrefix(path, from) {
			return to + strings.TrimPrefix(path, from)
		}
	}
	return path
}

// dialingRequests are requests each route accepts as far as opening a
// connection, for the engine family a driver belongs to. They are written out
// rather than derived: what a route takes before it dials is its own
// business, and the two routes that quoted a password to an administrator's
// audit trail were the ones `{}` never got past the validation of.
func dialingRequests(driver dbx.Driver) []dialingRequest {
	switch driver {
	case dbx.DriverRedis:
		return []dialingRequest{
			{"GET", "/keys/value?key=a", ``},
			{"GET", "/keys/meta?key=a", ``},
			{"GET", "/keys/members?key=a", ``},
			{"GET", "/keys/raw?key=a", ``},
			{"GET", "/keys/stream?key=a", ``},
			{"GET", "/keys/stream/pending?key=a&group=g", ``},
			{"POST", "/keys/value", `{"key":"a","type":"string","value":"b"}`},
			{"POST", "/keys/expire", `{"key":"a","ttl":10}`},
			{"POST", "/keys/persist", `{"key":"a"}`},
			{"POST", "/keys/rename", `{"key":"a","to":"b"}`},
			{"POST", "/keys/copy", `{"key":"a","to":"b"}`},
			{"POST", "/keys/bulk", `{"pattern":"a*","action":"delete","dryRun":true}`},
			{"POST", "/keys/bulk", `{"pattern":"a*","action":"delete"}`},
			{"DELETE", "/keys", `{"key":"a"}`},
			{"DELETE", "/keys", `{"keys":["a"]}`},
			{"POST", "/redis/command", `{"command":"GET a"}`},
			{"POST", "/redis/command", `{"command":"FOO.BAR a"}`},
			{"POST", "/redis/classify", `{"command":"FOO.BAR a"}`},
			{"POST", "/redis/save", `{}`},
			{"POST", "/redis/publish", `{"channel":"c","message":"m"}`},
			{"POST", "/redis/clients/kill", `{"id":"1"}`},
			{"POST", "/redis/slowlog/reset", `{}`},
			{"PUT", "/redis/config", `{"name":"maxmemory","value":"1"}`},
			{"PUT", "/redis/acl/u1", `{"create":true,"password":"Passw0rd-x9","commands":["+@read"]}`},
			{"DELETE", "/redis/acl/u1", ``},
			{"POST", "/backup", `{}`},
			{"POST", "/copy", `{"name":"3"}`},
			{"DELETE", "/database", `{}`},
			{"GET", "/querylog", ``},
			{"GET", "/server/roles", ``},
		}
	case dbx.DriverMongo:
		return []dialingRequest{
			{"POST", "/mongo/find", `{"collection":"c"}`},
			{"POST", "/mongo/count", `{"collection":"c"}`},
			{"POST", "/mongo/document", `{"collection":"c","id":"1"}`},
			{"POST", "/mongo/explain", `{"collection":"c"}`},
			{"POST", "/mongo/aggregate/preview", `{"collection":"c","pipeline":"[]"}`},
			{"POST", "/mongo/schema", `{"collection":"c"}`},
			{"POST", "/mongo/validation/check", `{"collection":"c"}`},
			{"POST", "/aggregate", `{"collection":"c","pipeline":"[]"}`},
			{"POST", "/mongo/command", `{"command":"{ping:1}"}`},
			{"POST", "/mongo/command/classify", `{"command":"{ping:1}"}`},
			{"POST", "/documents", `{"collection":"c","document":"{\"a\":1}"}`},
			{"POST", "/mongo/documents", `{"collection":"c","documents":"[{\"a\":1}]"}`},
			{"PATCH", "/mongo/documents", `{"collection":"c","filter":"{\"a\":1}","update":"{\"$set\":{\"a\":2}}"}`},
			{"DELETE", "/mongo/documents", `{"collection":"c","filter":"{\"a\":1}"}`},
			{"POST", "/mongo/collections", `{"collection":"c2"}`},
			{"DELETE", "/mongo/collections", `{"collection":"c2"}`},
			{"POST", "/mongo/indexes", `{"collection":"c","keys":"{\"a\":1}"}`},
			{"POST", "/mongo/killop", `{"opid":"1"}`},
			{"POST", "/mongo/users", `{"user":"u1","password":"Passw0rd-x9","roles":[{"role":"read","db":"shop"}]}`},
			{"PUT", "/mongo/users", `{"user":"u1","password":"Passw0rd-x9"}`},
			{"PUT", "/mongo/profiler", `{"level":0}`},
			{"POST", "/import", `{"table":"c","data":"[{\"a\":1}]","format":"json"}`},
			{"POST", "/backup", `{}`},
			{"POST", "/copy", `{"name":"copydb"}`},
			{"DELETE", "/database", `{}`},
			{"POST", "/server/roles", `{"name":"r1","password":"Passw0rd-x9"}`},
			{"POST", "/server/roles/r1/grant", `{"database":"shop","level":"read"}`},
			{"POST", "/server/databases", `{"name":"newdb"}`},
			{"GET", "/mongo/collection?collection=c", ``},
			{"GET", "/mongo/indexes?collection=c", ``},
			{"GET", "/mongo/export?collection=c", ``},
			{"GET", "/mongo/validation?collection=c", ``},
			{"GET", "/collections/indexes?collection=c", ``},
			{"GET", "/browse?table=c", ``},
			{"GET", "/export?table=c", ``},
			{"GET", "/querylog", ``},
		}
	}
	return []dialingRequest{
		{"POST", "/query", `{"query":"select 1"}`},
		{"POST", "/script", `{"script":"select 1"}`},
		{"POST", "/explain", `{"query":"select 1"}`},
		{"POST", "/explain", `{"query":"select 1","analyze":true}`},
		{"POST", "/export/query", `{"sql":"select 1"}`},
		{"POST", "/changes", `{"table":"t","changes":[{"op":"insert","values":{"a":1}}]}`},
		{"POST", "/rows", `{"table":"t","values":{"a":1}}`},
		{"PATCH", "/rows", `{"table":"t","values":{"a":1},"key":{"id":1}}`},
		{"DELETE", "/rows", `{"table":"t","key":{"id":1}}`},
		{"POST", "/ddl/table", `{"table":"t","columns":[{"name":"id","type":"int"}]}`},
		{"POST", "/ddl/index", `{"table":"t","name":"i","fields":["id"]}`},
		{"DELETE", "/ddl/column", `{"table":"t","name":"c"}`},
		{"PATCH", "/ddl/column", `{"table":"t","name":"c","nullable":true}`},
		{"POST", "/ddl/comment", `{"table":"t","comment":"x"}`},
		{"DELETE", "/ddl/constraint", `{"table":"t","name":"c"}`},
		{"POST", "/import", `{"table":"t","data":"a\n1","format":"csv","hasHeader":true}`},
		{"POST", "/maintenance", `{"action":"analyze"}`},
		{"POST", "/maintenance", `{"action":"check"}`},
		{"POST", "/maintenance", `{"action":"vacuum"}`},
		{"POST", "/maintenance", `{"action":"update_statistics"}`},
		{"POST", "/maintenance", `{"action":"optimize"}`},
		{"PUT", "/settings", `{"name":"work_mem","value":"4MB"}`},
		{"PUT", "/settings", `{"name":"max_connections","value":"100"}`},
		{"POST", "/server/roles", `{"name":"r1","password":"Passw0rd-x9"}`},
		{"PUT", "/server/roles/r1", `{"password":"Passw0rd-x9"}`},
		{"POST", "/server/roles/r1/grant", `{"database":"shop","level":"read"}`},
		{"POST", "/server/roles/r1/grant", `{"database":"other","level":"read"}`},
		{"POST", "/server/roles/r1/grant?preview=1", `{"database":"other","level":"read"}`},
		{"POST", "/server/roles/r1/privileges", `{"level":"database","database":"shop","privileges":["ALL"]}`},
		{"POST", "/server/roles/r1/privileges", `{"level":"database","database":"other","privileges":["ALL"]}`},
		{"POST", "/server/roles/r1/privileges", `{"level":"table","schema":"public","database":"shop","table":"t","privileges":["SELECT"]}`},
		{"POST", "/server/roles/r1/privileges/revoke", `{"level":"database","database":"shop","privileges":["ALL"]}`},
		{"POST", "/server/roles/r1/privileges?preview=1", `{"level":"schema","schema":"public","database":"shop","privileges":["ALL"],"future":true}`},
		{"POST", "/server/roles/r1/privileges?preview=1", `{"level":"table","schema":"public","database":"shop","privileges":["ALL"],"future":true}`},
		{"POST", "/server/databases", `{"name":"newdb"}`},
		{"POST", "/server/databases/connect", `{"database":"otherdb"}`},
		{"POST", "/server/extensions", `{"name":"pg_trgm"}`},
		{"DELETE", "/server/roles/r1", ``},
		{"DELETE", "/server/extensions/pg_trgm", ``},
		{"POST", "/activity/kill", `{"pid":"1"}`},
		{"POST", "/activity/cancel", `{"pid":"1"}`},
		{"POST", "/statements/reset", `{}`},
		{"POST", "/orm", `{"target":"prisma"}`},
		{"POST", "/orm", `{"target":"sql"}`},
		{"POST", "/backup", `{}`},
		{"POST", "/copy", `{"name":"copydb"}`},
		{"DELETE", "/database", `{}`},
		{"DELETE", "/database", `{"database":"otherdb"}`},
		{"GET", "/browse?table=t", ``},
		{"GET", "/table?table=t", ``},
		{"GET", "/columns?table=t", ``},
		{"GET", "/count?table=t", ``},
		{"GET", "/export?table=t", ``},
		{"GET", "/cell?table=t&column=c&key=" + url.QueryEscape(`{"id":1}`), ``},
		{"GET", "/search?q=x", ``},
		{"GET", "/object?kind=view&name=v", ``},
		{"GET", "/server/roles/r1", ``},
		{"GET", "/server/grants?role=r1", ``},
		{"GET", "/querylog", ``},
		{"GET", "/querylog?source=table", ``},
		{"GET", "/settings?all=1", ``},
		{"GET", "/tablestats?table=t", ``},
		{"GET", "/indexstats?table=t", ``},
	}
}
