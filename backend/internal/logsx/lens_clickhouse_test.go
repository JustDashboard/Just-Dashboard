package logsx

import "testing"

// chAttrs is a map from pairs, for the want tables.
func chAttrs(pairs ...string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return m
}

const (
	chQuery  = "5c4b3a04-840d-4fab-8f92-ba28bdfe65ab"
	chFailed = "179aabb7-2b5c-4f3e-9d1a-7e6f5a4b3c2d"
)

// The research's clickhouse-server.log lines, and the server's own wording
// for its memory, parts and startup warnings.
var clickhouseCases = []dbCase{
	{
		name: "start, a query from start to finish, and shutdown",
		lines: []string{
			"2026.09.27 10:00:00.123456 [ 1 ] {} <Information> Application: Starting ClickHouse 24.8.4.13 (revision: 54491, git hash: 1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b, build id: 0123456789ABCDEF), PID 1",
			`2026.09.27 10:00:00.200000 [ 1 ] {} <Warning> Context: Linux transparent hugepages are set to "always". Check /sys/kernel/mm/transparent_hugepage/enabled`,
			"2026.09.27 10:00:01.000000 [ 1 ] {} <Information> Application: Ready for connections.",
			"2026.09.27 10:01:02.345678 [ 812 ] {" + chQuery + "} <Debug> executeQuery: (from 172.18.0.5:54321, user: default) SELECT count() FROM events (stage: Complete)",
			"2026.09.27 10:01:02.345700 [ 812 ] {" + chQuery + "} <Trace> ContextAccess (default): Access granted: SELECT(n) ON default.events",
			"2026.09.27 10:01:02.412000 [ 812 ] {" + chQuery + "} <Debug> executeQuery: Read 1000000 rows, 7.63 MiB in 0.066 sec., 15151515 rows/sec., 115.61 MiB/sec.",
			"2026.09.27 10:01:02.412100 [ 812 ] {" + chQuery + "} <Debug> MemoryTracker: Peak memory usage (for query): 4.12 MiB.",
			"2026.09.27 11:00:00.000000 [ 1 ] {} <Information> Application: Received termination signal (Terminated)",
		},
		want: []dbWant{
			{level: "info", at: "2026-09-27 10:00:00.123456", event: "startup", attrs: chAttrs("thread", "1", "component", "Application")},
			{level: "warn", at: "2026-09-27 10:00:00.2", attrs: chAttrs("thread", "1", "component", "Context")},
			{level: "info", at: "2026-09-27 10:00:01", event: "ready", attrs: chAttrs("thread", "1", "component", "Application")},
			{level: "debug", at: "2026-09-27 10:01:02.345678", event: "query", attrs: chAttrs("thread", "812",
				"query_id", chQuery, "component", "executeQuery", "client", "172.18.0.5", "port", "54321",
				"user", "default", "query", "SELECT count() FROM events", "fp", Fingerprint("SELECT count() FROM events"))},
			{level: "debug", at: "2026-09-27 10:01:02.3457", attrs: chAttrs("thread", "812",
				"query_id", chQuery, "component", "ContextAccess (default)")},
			{level: "debug", at: "2026-09-27 10:01:02.412", event: "query", attrs: chAttrs("thread", "812",
				"query_id", chQuery, "component", "executeQuery", "rows", "1000000", "duration_ms", "66")},
			{level: "debug", at: "2026-09-27 10:01:02.4121", attrs: chAttrs("thread", "812",
				"query_id", chQuery, "component", "MemoryTracker")},
			{level: "info", at: "2026-09-27 11:00:00", event: "shutdown", attrs: chAttrs("thread", "1", "component", "Application")},
		},
	},
	{
		name: "an exception with its stack trace, a failed login",
		lines: []string{
			"2026.09.27 10:01:03.000000 [ 813 ] {" + chFailed + "} <Error> executeQuery: Code: 60. DB::Exception: Table default.nr does not exist. (UNKNOWN_TABLE) (version 24.8.4.13 (official build)) (from 172.18.0.5:54322) (in query: SELECT * FROM nr), Stack trace (when copying this message, always include the lines below):",
			"",
			"0. DB::Exception::Exception(DB::Exception::MessageMasked&&, int, bool) @ 0x000000000c4fd597",
			"2026.09.27 10:02:00.000000 [ 99 ] {} <Error> ServerErrorHandler: Code: 516. DB::Exception: default: Authentication failed: password is incorrect, or there is no user with such name. (AUTHENTICATION_FAILED)",
		},
		want: []dbWant{
			{level: "error", at: "2026-09-27 10:01:03", event: "exception", attrs: chAttrs("thread", "813",
				"query_id", chFailed, "component", "executeQuery", "code", "UNKNOWN_TABLE",
				"client", "172.18.0.5", "port", "54322")},
			{level: "error", cont: true},
			{level: "error", cont: true},
			{level: "error", at: "2026-09-27 10:02:00", event: "auth_failed", attrs: chAttrs("thread", "99",
				"component", "ServerErrorHandler", "code", "AUTHENTICATION_FAILED", "user", "default")},
		},
	},
	{
		name: "the limits an operator meets first, and background merges",
		lines: []string{
			"2026.09.27 10:04:00.000000 [ 814 ] {6a1f2b3c-4d5e-4f60-8a7b-9c0d1e2f3a4b} <Error> executeQuery: Code: 241. DB::Exception: Memory limit (for query) exceeded: would use 9.31 GiB (attempt to allocate chunk of 4194304 bytes), maximum: 9.31 GiB.: While executing AggregatingTransform. (MEMORY_LIMIT_EXCEEDED) (version 24.8.4.13 (official build)) (from 172.18.0.5:54400) (in query: SELECT user_id, count() FROM events GROUP BY user_id)",
			"2026.09.27 10:04:30.000000 [ 815 ] {7b2c3d4e-5f60-4a71-9b8c-0d1e2f3a4b5c} <Information> default.events (a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d): Delaying inserting block by 9.5 ms. because there are 150 parts and their average size is 1.02 MiB",
			"2026.09.27 10:05:00.000000 [ 815 ] {7b2c3d4e-5f60-4a71-9b8c-0d1e2f3a4b5c} <Error> executeQuery: Code: 252. DB::Exception: Too many parts (300 with average size of 1.02 MiB) in table 'default.events (a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d)'. Merges are processing significantly slower than inserts. (TOO_MANY_PARTS) (version 24.8.4.13 (official build)) (from 172.18.0.5:54401) (in query: INSERT INTO events FORMAT Values)",
			"2026.09.27 10:03:00.000000 [ 77 ] {} <Debug> default.events (a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d) (MergerMutator): Selected 6 parts from 202609_1_1_0 to 202609_6_6_0",
		},
		want: []dbWant{
			{level: "error", at: "2026-09-27 10:04:00", event: "memory_limit", attrs: chAttrs("thread", "814",
				"query_id", "6a1f2b3c-4d5e-4f60-8a7b-9c0d1e2f3a4b", "component", "executeQuery",
				"code", "MEMORY_LIMIT_EXCEEDED", "client", "172.18.0.5", "port", "54400")},
			// A table's logger names the table; with only its uuid after
			// it there is no kind of work to call the component.
			{level: "info", at: "2026-09-27 10:04:30", event: "too_many_parts", attrs: chAttrs("thread", "815",
				"query_id", "7b2c3d4e-5f60-4a71-9b8c-0d1e2f3a4b5c", "table", "default.events")},
			{level: "error", at: "2026-09-27 10:05:00", event: "too_many_parts", attrs: chAttrs("thread", "815",
				"query_id", "7b2c3d4e-5f60-4a71-9b8c-0d1e2f3a4b5c", "component", "executeQuery",
				"code", "TOO_MANY_PARTS", "client", "172.18.0.5", "port", "54401")},
			{level: "debug", at: "2026-09-27 10:03:00", event: "merge", attrs: chAttrs("thread", "77",
				"component", "MergerMutator", "table", "default.events")},
		},
	},
	{
		name: "a crash, and a server too old to write query ids",
		lines: []string{
			"2026.09.27 12:00:00.000000 [ 900 ] {} <Fatal> BaseDaemon: ########################################",
			"2026.09.27 12:00:00.000001 [ 900 ] {} <Fatal> BaseDaemon: (version 24.8.4.13 (official build), build id: 0123456789ABCDEF, git hash: 1a2b3c4d5e) (from thread 812) (query_id: " + chQuery + ") (query: SELECT count() FROM events) Received signal Segmentation fault (11)",
			"2019.06.01 10:00:00.123456 [ 1 ] <Information> Application: Ready for connections.",
		},
		want: []dbWant{
			{level: "critical", at: "2026-09-27 12:00:00", attrs: chAttrs("thread", "900", "component", "BaseDaemon")},
			{level: "critical", at: "2026-09-27 12:00:00.000001", attrs: chAttrs("thread", "900", "component", "BaseDaemon")},
			{level: "info", at: "2019-06-01 10:00:00.123456", event: "ready", attrs: chAttrs("thread", "1", "component", "Application")},
		},
	},
}

func TestClickhouseLens(t *testing.T) {
	dbRun(t, "clickhouse", clickhouseCases)
}

func BenchmarkLensClickhouse(b *testing.B) {
	var texts []string
	for _, c := range clickhouseCases {
		texts = append(texts, c.lines...)
	}
	dbBench(b, "clickhouse", texts)
}
