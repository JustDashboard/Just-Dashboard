package logsx

import (
	"maps"
	"testing"
	"time"
)

// appLensWant is what one line should read as through a lens. at is the
// timestamp as RFC 3339 in UTC, empty for none.
type appLensWant struct {
	event string
	level string
	at    string
	attrs map[string]string
	cont  bool
}

// appLensCheck compares every line read through a lens with what it should
// be, all fields exactly: a lens that sets an attr it was not asked for is as
// wrong as one that misses it.
func appLensCheck(t *testing.T, id string, texts []string, want []appLensWant) {
	t.Helper()
	if len(texts) != len(want) {
		t.Fatalf("%d lines, %d expectations", len(texts), len(want))
	}
	for i, line := range readThrough(t, id, texts...) {
		w := want[i]
		at := ""
		if line.Timestamp != nil {
			at = line.Timestamp.UTC().Format(time.RFC3339Nano)
		}
		if line.Event != w.event || line.Level != w.level || at != w.at || line.Cont != w.cont || !maps.Equal(line.Attrs, w.attrs) {
			t.Errorf("line %d %q\n got event=%q level=%q at=%q cont=%v attrs=%v\nwant event=%q level=%q at=%q cont=%v attrs=%v",
				i, line.Text, line.Event, line.Level, at, line.Cont, line.Attrs, w.event, w.level, w.at, w.cont, w.attrs)
		}
	}
}

// appLensZone runs a test in a zone three hours east of UTC, so a stamp read
// as UTC where it is the host's local time shows as the wrong hour.
func appLensZone(t *testing.T) {
	t.Helper()
	local := time.Local
	time.Local = time.FixedZone("EEST", 3*60*60)
	t.Cleanup(func() { time.Local = local })
}

// Next.js 16 as two deployments on this host print it: `docker logs` of
// jd-e175-r4 and jd-e211-r1.
func TestLensAppNextJS(t *testing.T) {
	appLensCheck(t, "app", []string{
		"$ next start",
		"▲ Next.js 16.3.5",
		"- Local:         http://localhost:3000",
		"- Network:       http://10.0.0.6:3000",
		"✓ Ready in 303ms",
		"✓ Running next.config.mjs took 152ms",
		`Error: The Server Reference ID did not match the expected format. Received "y".`,
		"Read more: https://nextjs.org/docs/messages/failed-to-find-server-action",
		"    at ignore-listed frames",
		`Error: Failed to find Server Action "x". This request might be from an older or newer deployment.`,
		"Read more: https://nextjs.org/docs/messages/failed-to-find-server-action",
		"    at ignore-listed frames",
		`Datasource "db": PostgreSQL database "app", schema "public" at "db-9.jd.internal:5432"`,
		"🚀  Your database is now in sync with your Prisma schema. Done in 485ms",
	}, []appLensWant{
		{},
		{},
		{},
		{},
		{event: "startup", attrs: map[string]string{"port": "3000", "duration_ms": "303"}},
		{},
		{event: "exception", level: "error", attrs: map[string]string{"error": `Error: The Server Reference ID did not match the expected format. Received "y".`}},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{event: "exception", level: "error", attrs: map[string]string{"error": `Error: Failed to find Server Action "x". This request might be from an older or newer deployment.`}},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{},
		{},
	})
}

func TestLensAppRequests(t *testing.T) {
	appLensZone(t)
	appLensCheck(t, "app", []string{
		// morgan's combined, dev, short and tiny formats.
		`::1 - - [27/Nov/2024:06:21:42 +0000] "GET /combined HTTP/1.1" 200 2 "-" "curl/8.7.1"`,
		"GET /dev 200 0.224 ms - 2",
		"::1 - GET /short HTTP/1.1 200 2 - 0.283 ms",
		"GET /tiny 200 2 - 0.188 ms",
		// A level-less format takes its level from the status, not from a
		// word in the path.
		`::ffff:10.0.0.4 - - [27/Sep/2026:10:00:00 +0000] "GET /api/error?x=1 HTTP/1.1" 502 0 "-" "curl/8.7.1"`,
		// Next.js 16's dev request line.
		" GET /admin/user-info 200 in 9.7s (compile: 6.2s, render: 3.5s)",
		// uvicorn keeps its own INFO whatever the status.
		`INFO:     127.0.0.1:54321 - "GET /docs HTTP/1.1" 200 OK`,
		`INFO:     172.18.0.1:40612 - "POST /api/items HTTP/1.1" 500 Internal Server Error`,
		// Django's runserver and Werkzeug write the host's local time.
		`[27/Sep/2026 10:00:00] "GET / HTTP/1.1" 200 10697`,
		`127.0.0.1 - - [27/Sep/2026 10:00:00] "GET /missing HTTP/1.1" 404 -`,
		// PHP-FPM's default access.format, as the official image writes it to stderr.
		`172.18.0.3 -  27/Sep/2026:10:00:00 +0000 "GET /index.php" 200`,
		// gin and Hono.
		`[GIN] 2018/12/07 - 09:11:42 | 200 |            5s |     20.20.20.20 | GET      "/"`,
		"--> GET /api/items 200 3ms",
		"<-- GET /api/items",
	}, []appLensWant{
		{event: "request", level: "info", at: "2024-11-27T06:21:42Z", attrs: map[string]string{"method": "GET", "path": "/combined", "status": "200", "class": "2xx", "client": "::1"}},
		{event: "request", level: "info", attrs: map[string]string{"method": "GET", "path": "/dev", "status": "200", "class": "2xx", "duration_ms": "0.224"}},
		{event: "request", level: "info", attrs: map[string]string{"method": "GET", "path": "/short", "status": "200", "class": "2xx", "duration_ms": "0.283", "client": "::1"}},
		{event: "request", level: "info", attrs: map[string]string{"method": "GET", "path": "/tiny", "status": "200", "class": "2xx", "duration_ms": "0.188"}},
		{event: "request", level: "error", at: "2026-09-27T10:00:00Z", attrs: map[string]string{"method": "GET", "path": "/api/error", "status": "502", "class": "5xx", "client": "10.0.0.4"}},
		{event: "request", level: "info", attrs: map[string]string{"method": "GET", "path": "/admin/user-info", "status": "200", "class": "2xx", "duration_ms": "9700"}},
		{event: "request", level: "info", attrs: map[string]string{"method": "GET", "path": "/docs", "status": "200", "class": "2xx", "client": "127.0.0.1"}},
		{event: "request", level: "info", attrs: map[string]string{"method": "POST", "path": "/api/items", "status": "500", "class": "5xx", "client": "172.18.0.1"}},
		{event: "request", level: "info", at: "2026-09-27T07:00:00Z", attrs: map[string]string{"method": "GET", "path": "/", "status": "200", "class": "2xx"}},
		{event: "request", level: "warn", at: "2026-09-27T07:00:00Z", attrs: map[string]string{"method": "GET", "path": "/missing", "status": "404", "class": "4xx", "client": "127.0.0.1"}},
		{event: "request", level: "info", at: "2026-09-27T10:00:00Z", attrs: map[string]string{"method": "GET", "path": "/index.php", "status": "200", "class": "2xx", "client": "172.18.0.3"}},
		{event: "request", level: "info", at: "2018-12-07T06:11:42Z", attrs: map[string]string{"method": "GET", "path": "/", "status": "200", "class": "2xx", "duration_ms": "5000", "client": "20.20.20.20"}},
		{event: "request", level: "info", attrs: map[string]string{"method": "GET", "path": "/api/items", "status": "200", "class": "2xx", "duration_ms": "3"}},
		{},
	})
}

// Rails and Phoenix state the method on one line and the status on a later
// one; the request goes on the line that closes it, matched by request id.
func TestLensAppRequestsAcrossLines(t *testing.T) {
	appLensZone(t)
	appLensCheck(t, "app", []string{
		`I, [2026-09-27T10:00:00.123456 #1]  INFO -- : [c6034478-4026-4ded-9e3c-088c76d056f1] Started GET "/" for 1.2.3.4 at 2026-09-27 10:00:00 +0000`,
		`I, [2026-09-27T10:00:00.124000 #1]  INFO -- : [c6034478-4026-4ded-9e3c-088c76d056f1] Processing by ArticlesController#index as HTML`,
		`I, [2026-09-27T10:00:00.209000 #1]  INFO -- : [c6034478-4026-4ded-9e3c-088c76d056f1] Completed 200 OK in 86ms (Views: 83.6ms | ActiveRecord: 1.3ms | Allocations: 29347)`,
		`[9f3e2a10-1111-4ded-9e3c-088c76d056f1] Started POST "/articles" for 1.2.3.4 at 2026-09-27 10:00:01 +0000`,
		`[9f3e2a10-1111-4ded-9e3c-088c76d056f1] Completed 500 Internal Server Error in 5ms (ActiveRecord: 0.0ms | Allocations: 1200)`,
		"10:00:00.123 request_id=F4l2Zu [info] GET /",
		"10:00:00.125 request_id=F4l2Zu [info] Sent 200 in 409µs",
	}, []appLensWant{
		{level: "info", at: "2026-09-27T10:00:00Z"},
		{level: "info", at: "2026-09-27T07:00:00.124Z"},
		{event: "request", level: "info", at: "2026-09-27T07:00:00.209Z", attrs: map[string]string{"method": "GET", "path": "/", "status": "200", "class": "2xx", "duration_ms": "86", "client": "1.2.3.4"}},
		{at: "2026-09-27T10:00:01Z"},
		{event: "request", level: "error", attrs: map[string]string{"method": "POST", "path": "/articles", "status": "500", "class": "5xx", "duration_ms": "5", "client": "1.2.3.4"}},
		{level: "info"},
		{event: "request", level: "info", attrs: map[string]string{"method": "GET", "path": "/", "status": "200", "class": "2xx", "duration_ms": "0.409"}},
	})
}

func TestLensAppStructured(t *testing.T) {
	appLensCheck(t, "app", []string{
		// pino-http's request line, and pino's error with a stack.
		`{"level":30,"time":1591195061437,"pid":16012,"hostname":"x","msg":"request completed","req":{"id":1,"method":"GET","url":"/api?x=1","remoteAddress":"::ffff:10.0.0.4","remotePort":64386},"res":{"statusCode":200},"responseTime":10}`,
		`{"level":50,"time":1531257618044,"msg":"test","err":{"type":"Error","message":"test","stack":"Error: test\n    at Object.<anonymous> (/app/x.js:1:1)"}}`,
		// zap's request line with a Go duration.
		`{"level":"info","ts":1787469741.8156157,"caller":"main.go:14","msg":"request completed","method":"GET","status":200,"path":"/health","latency":"1.234ms"}`,
		// Fastify announcing where it listens.
		`{"level":30,"time":"2026-09-27T10:00:00Z","msg":"Server listening at http://0.0.0.0:3000"}`,
		// This dashboard's own backend, from `docker logs just-dashboard-backend-1`:
		// a request that failed because its database refused the connection.
		"{\"time\":\"2026-09-27T11:20:10.607928756Z\",\"level\":\"ERROR\",\"msg\":\"request failed\",\"method\":\"GET\",\"path\":\"/api/v1/databases/8/overview\",\"err\":\"failed to connect to `user=jd database=app`: 127.0.0.1:5434 (127.0.0.1): dial error: dial tcp 127.0.0.1:5434: connect: connection refused\"}",
		`{"time":"2026-09-27T12:32:11.363613596Z","level":"INFO","msg":"audit","user":"wayy","role":"admin","ip":"100.64.0.2","actor":"session","action":"terminal.attach"}`,
	}, []appLensWant{
		{event: "request", level: "info", at: "2020-06-03T14:37:41.437Z", attrs: map[string]string{"method": "GET", "path": "/api", "status": "200", "class": "2xx", "duration_ms": "10", "client": "10.0.0.4"}},
		{event: "exception", level: "error", at: "2018-07-10T21:20:18.044Z", attrs: map[string]string{"error": "Error: test"}},
		{event: "request", level: "info", at: "2026-08-23T07:22:21.815616Z", attrs: map[string]string{"method": "GET", "path": "/health", "status": "200", "class": "2xx", "duration_ms": "1.234"}},
		{event: "startup", level: "info", at: "2026-09-27T10:00:00Z", attrs: map[string]string{"port": "3000"}},
		{event: "db_unreachable", level: "error", at: "2026-09-27T11:20:10.607928756Z"},
		{level: "info", at: "2026-09-27T12:32:11.363613596Z"},
	})
}

func TestLensAppJavaScriptRecords(t *testing.T) {
	appLensCheck(t, "app", []string{
		" ⨯ Error: Invalid email or password",
		"    at authorize (/app/.next/server/chunks/123.js:1:2345) {",
		"  digest: '740006343'",
		"}",
		"Error: Cannot find module '/app/dist/main.js'",
		"    at Module._resolveFilename (node:internal/modules/cjs/loader:1145:15)",
		"  code: 'MODULE_NOT_FOUND',",
		"  requireStack: []",
		"}",
		"",
		"Node.js v20.11.0",
		// Prisma prints its class, then the message on the lines below it.
		"PrismaClientKnownRequestError: ",
		"Invalid `prisma.user.create()` invocation:",
		"",
		"Unique constraint failed on the fields: (`email`)",
		"    at Rn.handleRequestError (/app/node_modules/@prisma/client/runtime/library.js:121:7315)",
		"Server listening on port 3000",
		// Nest logs the handler's complaint, then the error with its stack.
		"[Nest] 1  - 09/27/2026, 10:00:01 AM   ERROR [ExceptionsHandler] Cannot read properties of undefined (reading 'id')",
		"TypeError: Cannot read properties of undefined (reading 'id')",
		"    at UsersService.find (/app/dist/users/users.service.js:20:31)",
		// A frame continues whatever came before it.
		"Failed to load settings: Error: boom",
		"    at load (/app/settings.js:4:9)",
	}, []appLensWant{
		{event: "exception", level: "error", attrs: map[string]string{"error": "Error: Invalid email or password"}},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{event: "exception", level: "error", attrs: map[string]string{"error": "Error: Cannot find module '/app/dist/main.js'"}},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{event: "exception", level: "error", attrs: map[string]string{"error": "PrismaClientKnownRequestError"}},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{event: "startup", attrs: map[string]string{"port": "3000"}},
		{level: "error", at: "2026-09-27T10:00:01Z", attrs: map[string]string{"component": "ExceptionsHandler"}},
		{event: "exception", level: "error", attrs: map[string]string{"error": "TypeError: Cannot read properties of undefined (reading 'id')"}},
		{level: "error", cont: true},
		{level: "error"},
		{level: "error", cont: true},
	})
}

func TestLensAppPythonRecords(t *testing.T) {
	appLensCheck(t, "app", []string{
		"ERROR:    Exception in ASGI application",
		"Traceback (most recent call last):",
		`  File "/usr/local/lib/python3.12/site-packages/uvicorn/protocols/http/h11_impl.py", line 403, in run_asgi`,
		"    result = await app(  # type: ignore[func-returns-value]",
		`  File "/app/main.py", line 12, in read_item`,
		"    return 1 / 0",
		"           ~~^~~",
		"ZeroDivisionError: division by zero",
		// A KeyError out of os.environ is a missing variable; a chained
		// exception folds under the one it started with.
		"Traceback (most recent call last):",
		`  File "/app/settings.py", line 3, in <module>`,
		`    SECRET = os.environ["SECRET_KEY"]`,
		`  File "<frozen os>", line 714, in __getitem__`,
		"KeyError: 'SECRET_KEY'",
		"",
		"During handling of the above exception, another exception occurred:",
		"",
		"Traceback (most recent call last):",
		`  File "/app/main.py", line 1, in <module>`,
		"ValueError: bad",
		"INFO:     Shutting down",
		// Python's default basicConfig format names its logger.
		"WARNING:django.request:Not Found: /favicon.ico",
	}, []appLensWant{
		{level: "error"},
		{level: "error"},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{event: "exception", level: "error", attrs: map[string]string{"error": "ZeroDivisionError: division by zero"}},
		{level: "error"},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{event: "env_missing", level: "error", attrs: map[string]string{"error": "KeyError: 'SECRET_KEY'"}},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{event: "shutdown", level: "info"},
		{level: "warn", attrs: map[string]string{"component": "django.request"}},
	})
}

func TestLensAppOtherRuntimes(t *testing.T) {
	appLensZone(t)
	appLensCheck(t, "app", []string{
		// Go: net/http's recovered panic after the log package's local time,
		// and a panic that ends the process.
		"2024/01/01 12:00:00 http: panic serving 10.0.0.1:12345: runtime error: invalid memory address or nil pointer dereference",
		"goroutine 7 [running]:",
		"net/http.(*conn).serve.func1()",
		"	/usr/local/go/src/net/http/server.go:1868 +0xb9",
		"panic: runtime error: index out of range [5] with length 3",
		"",
		"goroutine 1 [running]:",
		"main.main()",
		"	/app/main.go:12 +0x1d",
		"exit status 2",
		// A dump on SIGQUIT has no panic line; its goroutines still continue
		// the line before them.
		"SIGQUIT: quit",
		"goroutine 1 [select]:",
		"main.main()",
		"	/app/main.go:30 +0x9c",
		// Java.
		`Exception in thread "main" java.lang.IllegalStateException: Failed to execute CommandLineRunner`,
		"	at org.springframework.boot.SpringApplication.callRunner(SpringApplication.java:798)",
		"Caused by: java.net.ConnectException: Connection refused",
		"	... 5 more",
		// Ruby and Rails.
		"app/models/user.rb:12:in `name': undefined method `upcase' for nil (NoMethodError)",
		"	from app/main.rb:3:in `<main>'",
		`[c6034478-4026-4ded-9e3c-088c76d056f1] ActionController::RoutingError (No route matches [GET] "/x"):`,
		// PHP and Laravel: the error is grouped without the file it was raised in.
		"PHP Fatal error:  Uncaught Exception: boom in /var/www/html/index.php:3",
		"Stack trace:",
		"#0 {main}",
		"  thrown in /var/www/html/index.php on line 3",
		`[2026-09-27 12:00:00] production.ERROR: Undefined variable $x {"exception":"[object] (ErrorException(code: 0): Undefined variable $x at /var/www/html/app/Http/Controllers/HomeController.php:12)`,
		"[stacktrace]",
		`#0 /var/www/html/vendor/laravel/framework/src/Illuminate/Foundation/Bootstrap/HandleExceptions.php(255): Illuminate\Foundation\Bootstrap\HandleExceptions->handleError()`,
		`"} `,
	}, []appLensWant{
		{event: "exception", level: "error", at: "2024-01-01T09:00:00Z", attrs: map[string]string{"error": "panic: runtime error: invalid memory address or nil pointer dereference"}},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{event: "exception", level: "error", attrs: map[string]string{"error": "panic: runtime error: index out of range [5] with length 3"}},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{},
		{cont: true},
		{cont: true},
		{cont: true},
		{event: "exception", level: "error", attrs: map[string]string{"error": "java.lang.IllegalStateException: Failed to execute CommandLineRunner"}},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{event: "exception", level: "error", attrs: map[string]string{"error": "NoMethodError: undefined method `upcase' for nil"}},
		{level: "error", cont: true},
		{event: "exception", level: "error", attrs: map[string]string{"error": `ActionController::RoutingError: No route matches [GET] "/x"`}},
		{event: "exception", level: "critical", attrs: map[string]string{"error": "Exception: boom"}},
		{level: "critical", cont: true},
		{level: "critical", cont: true},
		{level: "critical", cont: true},
		{event: "exception", level: "error", at: "2026-09-27T09:00:00Z", attrs: map[string]string{"error": "ErrorException: Undefined variable $x"}},
		{level: "error", cont: true},
		{level: "error", cont: true},
		{level: "error", cont: true},
	})
}

// The failures a deployment most often dies of, in the sentences
// deploy/runtime_output_cause.go was proven against.
func TestLensAppFailures(t *testing.T) {
	appLensCheck(t, "app", []string{
		"FATAL ERROR: Reached heap limit Allocation failed - JavaScript heap out of memory",
		"[2017-03-30 15:44:26 +0000] [1] [ERROR] Worker (pid:13) was sent SIGKILL! Perhaps out of memory?",
		"Error: listen EADDRINUSE: address already in use :::3000",
		"ERROR:    [Errno 98] error while attempting to bind on address ('0.0.0.0', 8000): address already in use",
		"Web server failed to start. Port 8080 was already in use.",
		"Error: P1001: Can't reach database server at `db-4.jd.internal:5432`",
		"Error: connect ECONNREFUSED 10.0.0.2:5432",
		// A refused connection to something that is not a database is not one.
		"Error: connect ECONNREFUSED 10.0.0.2:8080",
		"The table `public.products` does not exist in the current database.",
		`ERROR: relation "orders" does not exist`,
		"SqliteError: no such table: sessions",
		"You have 3 unapplied migration(s). Your project may not work properly until you apply the migrations for app(s): auth.",
		"** (RuntimeError) environment variable DATABASE_URL is missing.",
		"Error: Missing required environment variable: DATABASE_URL",
		// A program that says it is only warning carries on.
		"warn: SENTRY_DSN is not set, error reporting disabled",
		"(node:1) [DEP0040] DeprecationWarning: The `punycode` module is deprecated. Please use a userland alternative instead.",
	}, []appLensWant{
		{event: "oom", level: "critical"},
		{event: "oom", level: "error", at: "2017-03-30T15:44:26Z"},
		{event: "port_in_use", level: "error", attrs: map[string]string{"port": "3000", "error": "Error: listen EADDRINUSE: address already in use :::3000"}},
		{event: "port_in_use", level: "error", attrs: map[string]string{"port": "8000"}},
		{event: "port_in_use", level: "error", attrs: map[string]string{"port": "8080"}},
		{event: "db_unreachable", level: "error", attrs: map[string]string{"error": "Error: P1001: Can't reach database server at `db-4.jd.internal:5432`"}},
		{event: "db_unreachable", level: "error", attrs: map[string]string{"error": "Error: connect ECONNREFUSED 10.0.0.2:5432"}},
		{event: "exception", level: "error", attrs: map[string]string{"error": "Error: connect ECONNREFUSED 10.0.0.2:8080"}},
		{event: "schema_missing", level: "error", attrs: map[string]string{"table": "public.products"}},
		{event: "schema_missing", level: "error", attrs: map[string]string{"table": "orders"}},
		{event: "schema_missing", level: "error", attrs: map[string]string{"table": "sessions", "error": "SqliteError: no such table: sessions"}},
		{event: "schema_missing", level: "error"},
		{event: "env_missing"},
		{event: "env_missing", level: "error", attrs: map[string]string{"error": "Error: Missing required environment variable: DATABASE_URL"}},
		{level: "warn"},
		{event: "deprecation", level: "warn"},
	})
}

func TestLensAppLifecycle(t *testing.T) {
	appLensZone(t)
	appLensCheck(t, "app", []string{
		"INFO:     Started server process [1]",
		"INFO:     Application startup complete.",
		"INFO:     Uvicorn running on http://0.0.0.0:8000 (Press CTRL+C to quit)",
		"[2019-05-29 07:06:23 +0000] [1] [INFO] Starting gunicorn 19.9.0",
		"[2019-05-29 07:06:23 +0000] [1] [INFO] Listening at: http://0.0.0.0:8080 (1)",
		"[2019-05-29 07:10:23 +0000] [1] [INFO] Handling signal: term",
		"[2019-05-29 07:10:24 +0000] [1] [INFO] Shutting down: Master",
		// Puma prints a line per bound address; one start.
		"* Listening on http://0.0.0.0:3000",
		"* Listening on http://[::]:3000",
		// Spring names the port a line before the one that says it started.
		"2026-08-17T10:51:33.118Z  INFO 11390 --- [myapp] [           main] o.s.boot.tomcat.TomcatWebServer          : Tomcat started on port 8080 (http) with context path '/'",
		"2026-08-17T10:51:33.130Z  INFO 11390 --- [myapp] [           main] o.s.b.d.f.logexample.MyApplication       : Started MyApplication in 3.456 seconds (process running for 4.0)",
		// Prose that merely mentions a time or a count is not a start.
		"Backup started at 10:30",
		"[Nest] 1  - 09/27/2026, 10:00:00 AM     LOG [NestApplication] Nest application successfully started +3ms",
		"Server running on 16 cores",
		"[27-Sep-2026 10:00:00] NOTICE: ready to handle connections",
	}, []appLensWant{
		{level: "info"},
		{level: "info"},
		{event: "startup", level: "info", attrs: map[string]string{"port": "8000"}},
		{level: "info", at: "2019-05-29T07:06:23Z"},
		{event: "startup", level: "info", at: "2019-05-29T07:06:23Z", attrs: map[string]string{"port": "8080"}},
		{event: "shutdown", level: "info", at: "2019-05-29T07:10:23Z"},
		{level: "info", at: "2019-05-29T07:10:24Z"},
		{event: "startup", attrs: map[string]string{"port": "3000"}},
		{},
		{level: "info", at: "2026-08-17T10:51:33.118Z", attrs: map[string]string{"component": "o.s.boot.tomcat.TomcatWebServer"}},
		{event: "startup", level: "info", at: "2026-08-17T10:51:33.13Z", attrs: map[string]string{"component": "o.s.b.d.f.logexample.MyApplication", "port": "8080", "duration_ms": "3456"}},
		{},
		{event: "startup", level: "info", at: "2026-09-27T07:00:00Z", attrs: map[string]string{"component": "NestApplication"}},
		{},
		{event: "startup", level: "info", at: "2026-09-27T07:00:00Z"},
	})
}

// A logger's own time is the host's local time when it names no zone.
func TestLensAppLocalStamps(t *testing.T) {
	appLensZone(t)
	appLensCheck(t, "app", []string{
		// `docker logs high-market-tracker` on this host.
		"2026-09-27 13:38:16 | INFO     | high_market_tracker.scraper | Total tennis matches: 108",
		"2026-09-27 10:00:00,123 - app.worker - ERROR - job failed",
		"2024-01-01 00:00:00.123 | ERROR    | __main__:main:12 - boom",
	}, []appLensWant{
		{level: "info", at: "2026-09-27T10:38:16Z", attrs: map[string]string{"component": "high_market_tracker.scraper"}},
		{level: "error", at: "2026-09-27T07:00:00.123Z", attrs: map[string]string{"component": "app.worker"}},
		{level: "error", at: "2023-12-31T21:00:00.123Z", attrs: map[string]string{"component": "__main__"}},
	})
}

// A PM2 error file on this host (~/.pm2/logs/just-dashboard-web-error.log):
// bun reporting each stop of `next start`, which is not an error.
func TestLensAppPackageManagerStops(t *testing.T) {
	appLensCheck(t, "app", []string{
		`$ next start --hostname "100.110.34.31" --port "3400"`,
		`error: script "start" exited with code 143`,
		`$ next start --hostname "100.110.34.31" --port "3400"`,
		`error: script "start" exited with code 130`,
		"npm error signal SIGTERM",
	}, []appLensWant{
		{},
		{event: "shutdown", level: "info"},
		{},
		{event: "shutdown", level: "info"},
		{event: "shutdown", level: "info"},
	})
}

// BenchmarkLensApp reads a mix weighted the way container output is: mostly
// prose and requests, some structured lines, now and then a stack.
func BenchmarkLensApp(b *testing.B) {
	appLensBenchmark(b, "app", []string{
		"2026-09-27 13:38:16 | INFO     | high_market_tracker.scraper | Total tennis matches: 108",
		`::1 - - [27/Nov/2024:06:21:42 +0000] "GET /combined HTTP/1.1" 200 2 "-" "curl/8.7.1"`,
		`INFO:     127.0.0.1:54321 - "GET /docs HTTP/1.1" 200 OK`,
		"Prisma schema loaded from prisma/schema.prisma",
		`{"time":"2026-09-27T12:32:11.363613596Z","level":"INFO","msg":"audit","user":"wayy","role":"admin","ip":"100.64.0.2","actor":"session","action":"terminal.attach"}`,
		"✓ Ready in 303ms",
		`Error: Failed to find Server Action "x". This request might be from an older or newer deployment.`,
		"Read more: https://nextjs.org/docs/messages/failed-to-find-server-action",
		"    at ignore-listed frames",
		"Fetching page 3 of 12 from the upstream API",
	})
}

// appLensBenchmark parses the lines once and then reads them through a fresh
// reader per pass, so what is measured is the lens and not ParseLine.
func appLensBenchmark(b *testing.B, id string, texts []string) {
	lens, err := LensByID(id)
	if err != nil || lens == nil {
		b.Fatalf("lens %q not registered: %v", id, err)
	}
	parsed := make([]Line, len(texts))
	for i, text := range texts {
		parsed[i] = ParseLine(text, "")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		r := lens.New()
		for _, line := range parsed {
			l := line
			r.Read(&l)
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(texts)), "ns/line")
}
