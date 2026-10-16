package logsx

import (
	"testing"
	"time"
	"unicode/utf16"
)

// pgAttrs is what every Postgres head carries — its pid and severity — and
// the pairs given.
func pgAttrs(pid, severity string, pairs ...string) map[string]string {
	m := map[string]string{"pid": pid, "severity": severity}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return m
}

// The lines are the ones the Postgres containers and the host cluster on the
// machine this was written on actually logged, and where a message never
// occurred there, the examples from the research (pganalyze's and the
// PostgreSQL sources' own wording) behind the Docker image's prefix.
var postgresCases = []dbCase{
	{
		name: "the entrypoint's temporary server, which starts mid-line",
		lines: []string{
			"sh: locale: not found",
			"2026-09-18 09:25:53.211 UTC [35] WARNING:  no usable system locales were found",
			`initdb: warning: enabling "trust" authentication for local connections`,
			"waiting for server to start....2026-09-18 09:25:53.958 UTC [41] LOG:  starting PostgreSQL 16.15 on x86_64-pc-linux-musl, compiled by gcc (Alpine 15.2.0) 15.2.0, 64-bit",
			`2026-09-18 09:25:53.959 UTC [41] LOG:  listening on Unix socket "/var/run/postgresql/.s.PGSQL.5432"`,
			"2026-09-18 09:25:53.968 UTC [41] LOG:  database system is ready to accept connections",
			" done",
			"PostgreSQL init process complete; ready for start up.",
		},
		want: []dbWant{
			{},
			{level: "warn", at: "2026-09-18 09:25:53.211Z", attrs: pgAttrs("35", "WARNING")},
			// Entrypoint prose is not Postgres's and keeps the generic reading.
			{level: "warn"},
			{level: "info", at: "2026-09-18 09:25:53.958Z", event: "startup", attrs: pgAttrs("41", "LOG")},
			{level: "info", at: "2026-09-18 09:25:53.959Z", attrs: pgAttrs("41", "LOG")},
			{level: "info", at: "2026-09-18 09:25:53.968Z", event: "ready", attrs: pgAttrs("41", "LOG")},
			{},
			{},
		},
	},
	{
		name: "the entrypoint stopping its temporary server, pg_ctl's dots on both lines",
		lines: []string{
			"waiting for server to shut down...2026-08-30 19:11:58.676 UTC [41] LOG:  received fast shutdown request",
			".2026-08-30 19:11:58.677 UTC [41] LOG:  aborting any active transactions",
			"2026-08-30 19:11:58.737 UTC [41] LOG:  database system is shut down",
		},
		want: []dbWant{
			{level: "info", at: "2026-08-30 19:11:58.676Z", event: "shutdown", attrs: pgAttrs("41", "LOG")},
			{level: "info", at: "2026-08-30 19:11:58.677Z", attrs: pgAttrs("41", "LOG")},
			{level: "info", at: "2026-08-30 19:11:58.737Z", event: "shutdown", attrs: pgAttrs("41", "LOG")},
		},
	},
	{
		name: "a fast shutdown with sessions still open",
		lines: []string{
			"2026-09-18 09:47:43.282 UTC [1] LOG:  received fast shutdown request",
			"2026-09-18 09:47:43.284 UTC [1] LOG:  aborting any active transactions",
			"2026-09-18 09:47:43.284 UTC [82] FATAL:  terminating connection due to administrator command",
			`2026-09-18 09:47:43.325 UTC [1] LOG:  background worker "logical replication launcher" (PID 60) exited with exit code 1`,
			"2026-09-18 09:47:43.326 UTC [55] LOG:  shutting down",
			"2026-09-18 09:47:43.328 UTC [55] LOG:  checkpoint starting: shutdown immediate",
			"2026-09-18 09:47:43.335 UTC [55] LOG:  checkpoint complete: wrote 0 buffers (0.0%); 0 WAL file(s) added, 0 removed, 0 recycled; write=0.002 s, sync=0.001 s, total=0.009 s; sync files=0, longest=0.000 s, average=0.000 s; distance=0 kB, estimate=1255 kB; lsn=0/1A82B58, redo lsn=0/1A82B58",
			"2026-09-18 09:47:43.342 UTC [1] LOG:  database system is shut down",
		},
		want: []dbWant{
			{level: "info", at: "2026-09-18 09:47:43.282Z", event: "shutdown", attrs: pgAttrs("1", "LOG")},
			{level: "info", at: "2026-09-18 09:47:43.284Z", attrs: pgAttrs("1", "LOG")},
			{level: "error", at: "2026-09-18 09:47:43.284Z", event: "terminated", attrs: pgAttrs("82", "FATAL",
				"code", "57P01", "error_class", "operator", "reason", "administrator command")},
			{level: "info", at: "2026-09-18 09:47:43.325Z", attrs: pgAttrs("1", "LOG")},
			{level: "info", at: "2026-09-18 09:47:43.326Z", attrs: pgAttrs("55", "LOG")},
			{level: "info", at: "2026-09-18 09:47:43.328Z", event: "checkpoint", attrs: pgAttrs("55", "LOG",
				"reason", "shutdown immediate")},
			{level: "info", at: "2026-09-18 09:47:43.335Z", event: "checkpoint", attrs: pgAttrs("55", "LOG",
				"buffers", "0", "write_s", "0.002", "sync_s", "0.001", "total_s", "0.009")},
			{level: "info", at: "2026-09-18 09:47:43.342Z", event: "shutdown", attrs: pgAttrs("1", "LOG")},
		},
	},
	{
		name: "the host cluster's Debian log, background lines without user@db",
		lines: []string{
			"2026-09-25 12:31:56.078 UTC [3516736] LOG:  starting PostgreSQL 17.7 (Ubuntu 17.7-0ubuntu0.25.04.1) on x86_64-pc-linux-gnu, compiled by gcc (Ubuntu 14.2.0-19ubuntu2) 14.2.0, 64-bit",
			"2026-09-25 12:37:00.633 UTC [3516738] LOG:  checkpoint complete: wrote 48 buffers (0.3%); 0 WAL file(s) added, 0 removed, 0 recycled; write=4.516 s, sync=0.003 s, total=4.522 s; sync files=15, longest=0.001 s, average=0.001 s; distance=263 kB, estimate=263 kB; lsn=0/152EAF8, redo lsn=0/152EA68",
		},
		want: []dbWant{
			{level: "info", at: "2026-09-25 12:31:56.078Z", event: "startup", attrs: pgAttrs("3516736", "LOG")},
			{level: "info", at: "2026-09-25 12:37:00.633Z", event: "checkpoint", attrs: pgAttrs("3516738", "LOG",
				"buffers", "48", "write_s", "4.516", "sync_s", "0.003", "total_s", "4.522")},
		},
	},
	{
		name: "a cancelled statement that spans lines",
		lines: []string{
			"2026-09-17 13:34:41.385 UTC [56569] ERROR:  canceling statement due to user request",
			"2026-09-17 13:34:41.385 UTC [56569] STATEMENT:  SELECT n.nspname, c.relname,",
			"\t\t                CASE c.relkind WHEN 'r' THEN 'table' WHEN 'v' THEN 'view'",
			"\t\t         ORDER BY 1, 2",
			"2026-09-17 13:34:41.385 UTC [56623] ERROR:  canceling statement due to user request",
			"2026-09-17 13:34:41.385 UTC [56623] STATEMENT:  ",
			"\t\t\tSELECT n.nspname, c.relname,",
		},
		want: []dbWant{
			{level: "error", at: "2026-09-17 13:34:41.385Z", event: "cancel", attrs: pgAttrs("56569", "ERROR",
				"code", "57014", "error_class", "operator", "reason", "user request")},
			{level: "error", at: "2026-09-17 13:34:41.385Z", cont: true},
			{level: "error", cont: true},
			{level: "error", cont: true},
			{level: "error", at: "2026-09-17 13:34:41.385Z", event: "cancel", attrs: pgAttrs("56623", "ERROR",
				"code", "57014", "error_class", "operator", "reason", "user request")},
			{level: "error", at: "2026-09-17 13:34:41.385Z", cont: true},
			{level: "error", cont: true},
		},
	},
	{
		name: "an application error, a lost client",
		lines: []string{
			`2026-09-25 08:54:17.942 UTC [79] ERROR:  relation "_prisma_migrations" does not exist at character 126`,
			`2026-09-25 08:54:17.942 UTC [79] STATEMENT:  SELECT "id", "checksum", "finished_at", "migration_name", "logs", "rolled_back_at", "started_at", "applied_steps_count" FROM "_prisma_migrations" ORDER BY "started_at" ASC`,
			"2026-09-27 12:15:27.955 UTC [4392] LOG:  could not send data to client: Broken pipe",
			"2026-09-27 12:15:27.963 UTC [4392] FATAL:  connection to client lost",
		},
		want: []dbWant{
			{level: "error", at: "2026-09-25 08:54:17.942Z", event: "error", attrs: pgAttrs("79", "ERROR",
				"code", "42P01", "error_class", "syntax or access")},
			{level: "error", at: "2026-09-25 08:54:17.942Z", cont: true},
			// The client went away; the server did not fail.
			{level: "warn", at: "2026-09-27 12:15:27.955Z", event: "client_lost", attrs: pgAttrs("4392", "LOG")},
			{level: "warn", at: "2026-09-27 12:15:27.963Z", event: "client_lost", attrs: pgAttrs("4392", "FATAL",
				"code", "08006", "error_class", "connection")},
		},
	},
	{
		name: "Debian's prefix names the user and database",
		lines: []string{
			`2026-01-01 00:00:00.000 UTC [123] app@shop FATAL:  password authentication failed for user "app"`,
			`2026-01-01 00:00:00.000 UTC [123] app@shop DETAIL:  Connection matched file "/etc/postgresql/17/main/pg_hba.conf" line 118: "host    all             all             127.0.0.1/32            scram-sha-256"`,
			"2026-01-01 00:00:01.000 UTC [124] [unknown]@[unknown] LOG:  connection received: host=[local]",
		},
		want: []dbWant{
			{level: "error", at: "2026-01-01 00:00:00Z", event: "auth_failed", attrs: pgAttrs("123", "FATAL",
				"user", "app", "db", "shop", "code", "28P01", "error_class", "auth")},
			{level: "error", at: "2026-01-01 00:00:00Z", cont: true},
			// A socket connection has no address to offer.
			{level: "info", at: "2026-01-01 00:00:01Z", event: "connection", attrs: pgAttrs("124", "LOG")},
		},
	},
	{
		name: "a session with log_connections and log_disconnections on",
		lines: []string{
			"2026-09-27 10:00:05.100 UTC [812] LOG:  connection received: host=172.18.0.5 port=51234",
			`2026-09-27 10:00:05.104 UTC [812] LOG:  connection authenticated: user="app" method=scram-sha-256 (/var/lib/postgresql/data/pg_hba.conf:128)`,
			"2026-09-27 10:00:05.105 UTC [812] LOG:  connection authorized: user=app database=shop application_name=psql SSL enabled (protocol=TLSv1.3, cipher=TLS_AES_256_GCM_SHA384, bits=256)",
			"2026-09-27 10:01:07.450 UTC [812] LOG:  disconnection: session time: 0:01:02.345 user=app database=shop host=172.18.0.5 port=51234",
			"2026-09-27 10:02:00.000 UTC [813] LOG:  connection received: host=203.0.113.7 port=40022",
			`2026-09-27 10:02:00.010 UTC [813] FATAL:  password authentication failed for user "admin"`,
			`2026-09-27 10:02:00.010 UTC [813] DETAIL:  Role "admin" does not exist.`,
			"2026-09-27 10:02:05.000 UTC [813] LOG:  checkpoint starting: time",
		},
		want: []dbWant{
			{level: "info", at: "2026-09-27 10:00:05.1Z", event: "connection", attrs: pgAttrs("812", "LOG",
				"client", "172.18.0.5", "port", "51234")},
			{level: "info", at: "2026-09-27 10:00:05.104Z", event: "authenticated", attrs: pgAttrs("812", "LOG",
				"user", "app", "auth_method", "scram-sha-256", "client", "172.18.0.5")},
			{level: "info", at: "2026-09-27 10:00:05.105Z", event: "authorized", attrs: pgAttrs("812", "LOG",
				"user", "app", "db", "shop", "app", "psql", "client", "172.18.0.5")},
			{level: "info", at: "2026-09-27 10:01:07.45Z", event: "disconnection", attrs: pgAttrs("812", "LOG",
				"session_ms", "62345", "user", "app", "db", "shop", "client", "172.18.0.5", "port", "51234")},
			{level: "info", at: "2026-09-27 10:02:00Z", event: "connection", attrs: pgAttrs("813", "LOG",
				"client", "203.0.113.7", "port", "40022")},
			// The failure does not repeat the address; the session's
			// "connection received" did, and the lens carried it.
			{level: "error", at: "2026-09-27 10:02:00.01Z", event: "auth_failed", attrs: pgAttrs("813", "FATAL",
				"user", "admin", "code", "28P01", "error_class", "auth", "client", "203.0.113.7")},
			{level: "error", at: "2026-09-27 10:02:00.01Z", cont: true},
			// The FATAL ended that backend: a later process with the pid
			// is not that client.
			{level: "info", at: "2026-09-27 10:02:05Z", event: "checkpoint", attrs: pgAttrs("813", "LOG", "reason", "time")},
		},
	},
	{
		name: "refused before a session exists",
		lines: []string{
			`2026-09-27 10:03:00.000 UTC [900] FATAL:  no pg_hba.conf entry for host "8.8.8.8", user "postgres", database "postgres", SSL off`,
			`2026-09-27 10:03:01.000 UTC [901] FATAL:  role "root" does not exist`,
			`2026-09-27 10:03:02.000 UTC [902] FATAL:  database "root" does not exist`,
			"2026-09-27 10:03:03.000 UTC [903] FATAL:  sorry, too many clients already",
			"2026-09-27 10:03:04.000 UTC [904] FATAL:  remaining connection slots are reserved for roles with the SUPERUSER attribute",
		},
		want: []dbWant{
			{level: "error", at: "2026-09-27 10:03:00Z", event: "auth_failed", attrs: pgAttrs("900", "FATAL",
				"client", "8.8.8.8", "user", "postgres", "db", "postgres", "code", "28000", "error_class", "auth")},
			{level: "error", at: "2026-09-27 10:03:01Z", event: "auth_failed", attrs: pgAttrs("901", "FATAL",
				"user", "root", "code", "28000", "error_class", "auth")},
			{level: "error", at: "2026-09-27 10:03:02Z", event: "auth_failed", attrs: pgAttrs("902", "FATAL",
				"db", "root", "code", "3D000", "error_class", "catalog")},
			{level: "error", at: "2026-09-27 10:03:03Z", event: "too_many_clients", attrs: pgAttrs("903", "FATAL",
				"code", "53300", "error_class", "resources")},
			{level: "error", at: "2026-09-27 10:03:04Z", event: "too_many_clients", attrs: pgAttrs("904", "FATAL",
				"code", "53300", "error_class", "resources")},
		},
	},
	{
		name: "statement and duration logging",
		lines: []string{
			"2026-09-27 10:04:00.000 UTC [812] LOG:  duration: 1523.412 ms  statement: SELECT * FROM orders WHERE customer_id = 42",
			"2026-09-27 10:04:01.000 UTC [812] LOG:  duration: 812.004 ms  execute <unnamed>: SELECT * FROM orders WHERE id = $1",
			"2026-09-27 10:04:01.000 UTC [812] DETAIL:  parameters: $1 = '42'",
			"2026-09-27 10:04:02.000 UTC [812] LOG:  duration: 2012.771 ms  plan:",
			"\tQuery Text: SELECT * FROM orders",
			"\tSeq Scan on orders  (cost=0.00..4312.00 rows=100000 width=64) (actual time=0.01..1990.0 rows=100000 loops=1)",
			"2026-09-27 10:04:03.000 UTC [812] LOG:  duration: 0.210 ms",
			"2026-09-27 10:04:04.000 UTC [812] LOG:  statement: UPDATE accounts SET balance = balance - 1 WHERE id = 1",
		},
		want: []dbWant{
			{level: "info", at: "2026-09-27 10:04:00Z", event: "slow", attrs: pgAttrs("812", "LOG",
				"duration_ms", "1523.412", "query", "SELECT * FROM orders WHERE customer_id = 42",
				"fp", Fingerprint("SELECT * FROM orders WHERE customer_id = 42"))},
			{level: "info", at: "2026-09-27 10:04:01Z", event: "slow", attrs: pgAttrs("812", "LOG",
				"duration_ms", "812.004", "query", "SELECT * FROM orders WHERE id = $1",
				"fp", Fingerprint("SELECT * FROM orders WHERE id = $1"))},
			{level: "info", at: "2026-09-27 10:04:01Z", cont: true},
			{level: "info", at: "2026-09-27 10:04:02Z", event: "slow", attrs: pgAttrs("812", "LOG", "duration_ms", "2012.771")},
			{level: "info", cont: true},
			{level: "info", cont: true},
			{level: "info", at: "2026-09-27 10:04:03Z", event: "duration", attrs: pgAttrs("812", "LOG", "duration_ms", "0.21")},
			{level: "info", at: "2026-09-27 10:04:04Z", event: "statement", attrs: pgAttrs("812", "LOG",
				"query", "UPDATE accounts SET balance = balance - 1 WHERE id = 1",
				"fp", Fingerprint("UPDATE accounts SET balance = balance - 1 WHERE id = 1"))},
		},
	},
	{
		name: "a lock wait and a deadlock with their details",
		lines: []string{
			"2026-09-27 10:05:00.000 UTC [2078] LOG:  process 2078 still waiting for ShareLock on transaction 1045207414 after 1000.100 ms",
			"2026-09-27 10:05:00.000 UTC [2078] DETAIL:  Process holding the lock: 583. Wait queue: 2078, 456.",
			`2026-09-27 10:05:00.000 UTC [2078] CONTEXT:  while updating tuple (0,5) in relation "accounts"`,
			"2026-09-27 10:05:00.000 UTC [2078] STATEMENT:  UPDATE accounts SET balance = balance - 1 WHERE id = 1",
			"2026-09-27 10:05:01.000 UTC [9788] ERROR:  deadlock detected",
			"2026-09-27 10:05:01.000 UTC [9788] DETAIL:  Process 9788 waits for ShareLock on transaction 1035; blocked by process 91.",
			"\tProcess 91 waits for ShareLock on transaction 1045; blocked by process 98.",
			"2026-09-27 10:05:01.000 UTC [9788] HINT:  See server log for query details.",
			`2026-09-27 10:05:01.000 UTC [9788] CONTEXT:  while inserting index tuple (1,42) in relation "x"`,
		},
		want: []dbWant{
			// Time spent waiting is not a statement's time: it is wait_ms, so
			// the statement time Insights ranks is statements alone.
			{level: "info", at: "2026-09-27 10:05:00Z", event: "lock_wait", attrs: pgAttrs("2078", "LOG",
				"lock", "ShareLock", "wait_ms", "1000.1")},
			{level: "info", at: "2026-09-27 10:05:00Z", cont: true},
			{level: "info", at: "2026-09-27 10:05:00Z", cont: true},
			{level: "info", at: "2026-09-27 10:05:00Z", cont: true},
			{level: "error", at: "2026-09-27 10:05:01Z", event: "deadlock", attrs: pgAttrs("9788", "ERROR",
				"code", "40P01", "error_class", "rollback")},
			{level: "error", at: "2026-09-27 10:05:01Z", cont: true},
			{level: "error", cont: true},
			{level: "error", at: "2026-09-27 10:05:01Z", cont: true},
			{level: "error", at: "2026-09-27 10:05:01Z", cont: true},
		},
	},
	{
		name: "maintenance",
		lines: []string{
			`2026-09-27 10:06:00.000 UTC [4242] LOG:  automatic vacuum of table "shop.public.orders": index scans: 1`,
			"\tpages: 0 removed, 1234 remain, 1234 scanned (100.00% of total)",
			"\tsystem usage: CPU: user: 0.05 s, system: 0.01 s, elapsed: 0.12 s",
			`2026-09-27 10:06:01.000 UTC [4242] LOG:  automatic analyze of table "shop.public.orders"`,
			`2026-09-27 10:06:02.000 UTC [812] LOG:  temporary file: path "base/pgsql_tmp/pgsql_tmp812.0", size 104857600`,
			"2026-09-27 10:06:02.000 UTC [812] STATEMENT:  SELECT * FROM orders ORDER BY created_at",
			"2026-09-27 10:06:03.000 UTC [27] LOG:  checkpoints are occurring too frequently (12 seconds apart)",
			`2026-09-27 10:06:03.000 UTC [27] HINT:  Consider increasing the configuration parameter "max_wal_size".`,
		},
		want: []dbWant{
			{level: "info", at: "2026-09-27 10:06:00Z", event: "autovacuum", attrs: pgAttrs("4242", "LOG", "table", "shop.public.orders")},
			{level: "info", cont: true},
			{level: "info", cont: true},
			{level: "info", at: "2026-09-27 10:06:01Z", event: "autoanalyze", attrs: pgAttrs("4242", "LOG", "table", "shop.public.orders")},
			{level: "info", at: "2026-09-27 10:06:02Z", event: "temp_file", attrs: pgAttrs("812", "LOG", "bytes", "104857600")},
			{level: "info", at: "2026-09-27 10:06:02Z", cont: true},
			{level: "warn", at: "2026-09-27 10:06:03Z", event: "checkpoint_pressure", attrs: pgAttrs("27", "LOG",
				"interval_s", "12")},
			{level: "warn", at: "2026-09-27 10:06:03Z", cont: true},
		},
	},
	{
		name: "crashes, the OOM killer and a full disk",
		lines: []string{
			"2026-09-27 10:07:00.000 UTC [1] LOG:  server process (PID 660) was terminated by signal 6: Aborted",
			"2026-09-27 10:07:00.000 UTC [1] DETAIL:  Failed process was running: SELECT pg_sleep(10)",
			"2026-09-27 10:07:00.001 UTC [1] LOG:  terminating any other active server processes",
			"2026-09-27 10:08:00.000 UTC [1] LOG:  server process (PID 661) was terminated by signal 9: Killed",
			"2026-09-27 10:09:00.000 UTC [700] ERROR:  out of memory",
			"2026-09-27 10:09:00.000 UTC [700] DETAIL:  Failed on request of size 1048576.",
			`2026-09-27 10:10:00.000 UTC [29] PANIC:  could not write to file "pg_wal/xlogtemp.29": No space left on device`,
			"2026-09-27 10:11:00.000 UTC [30] PANIC:  could not locate a valid checkpoint record",
		},
		want: []dbWant{
			{level: "critical", at: "2026-09-27 10:07:00Z", event: "crash", attrs: pgAttrs("1", "LOG")},
			{level: "critical", at: "2026-09-27 10:07:00Z", cont: true},
			{level: "warn", at: "2026-09-27 10:07:00.001Z", event: "recovery", attrs: pgAttrs("1", "LOG")},
			{level: "critical", at: "2026-09-27 10:08:00Z", event: "oom", attrs: pgAttrs("1", "LOG")},
			{level: "critical", at: "2026-09-27 10:09:00Z", event: "oom", attrs: pgAttrs("700", "ERROR",
				"code", "53200", "error_class", "resources")},
			{level: "critical", at: "2026-09-27 10:09:00Z", cont: true},
			{level: "critical", at: "2026-09-27 10:10:00Z", event: "disk_full", attrs: pgAttrs("29", "PANIC",
				"code", "53100", "error_class", "resources")},
			{level: "critical", at: "2026-09-27 10:11:00Z", event: "crash", attrs: pgAttrs("30", "PANIC")},
		},
	},
	{
		name: "configuration and archiving",
		lines: []string{
			"2026-09-27 10:12:00.000 UTC [1] LOG:  received SIGHUP, reloading configuration files",
			`2026-09-27 10:12:00.001 UTC [1] LOG:  parameter "work_mem" changed to "64MB"`,
			"2026-09-27 10:13:00.000 UTC [40] LOG:  archive command failed with exit code 1",
			"2026-09-27 10:13:00.000 UTC [40] DETAIL:  The failed archive command was: cp pg_wal/000000010000000000000003 /mnt/archive/000000010000000000000003",
		},
		want: []dbWant{
			{level: "info", at: "2026-09-27 10:12:00Z", event: "config", attrs: pgAttrs("1", "LOG")},
			{level: "info", at: "2026-09-27 10:12:00.001Z", event: "config", attrs: pgAttrs("1", "LOG")},
			{level: "info", at: "2026-09-27 10:13:00Z", event: "archive_failed", attrs: pgAttrs("40", "LOG")},
			{level: "info", at: "2026-09-27 10:13:00Z", cont: true},
		},
	},
	{
		name: "a standby",
		lines: []string{
			"2026-09-27 10:14:00.000 UTC [29] LOG:  entering standby mode",
			"2026-09-27 10:14:00.100 UTC [35] LOG:  started streaming WAL from primary at 0/3000000 on timeline 1",
			"2026-09-27 10:14:00.200 UTC [1] LOG:  database system is ready to accept read-only connections",
			"2026-09-27 10:15:00.000 UTC [35] FATAL:  could not receive data from WAL stream: server closed the connection unexpectedly",
			"2026-09-27 10:16:00.000 UTC [880] ERROR:  canceling statement due to conflict with recovery",
			"2026-09-27 10:16:00.000 UTC [880] DETAIL:  User query might have needed to see row versions that must be removed.",
		},
		want: []dbWant{
			{level: "info", at: "2026-09-27 10:14:00Z", event: "replication", attrs: pgAttrs("29", "LOG")},
			{level: "info", at: "2026-09-27 10:14:00.1Z", event: "replication", attrs: pgAttrs("35", "LOG")},
			{level: "info", at: "2026-09-27 10:14:00.2Z", event: "ready", attrs: pgAttrs("1", "LOG")},
			{level: "error", at: "2026-09-27 10:15:00Z", event: "replication", attrs: pgAttrs("35", "FATAL")},
			{level: "error", at: "2026-09-27 10:16:00Z", event: "cancel", attrs: pgAttrs("880", "ERROR",
				"code", "40001", "error_class", "rollback", "reason", "conflict with recovery")},
			{level: "error", at: "2026-09-27 10:16:00Z", cont: true},
		},
	},
	{
		name: "a part written by another pid does not continue the line above",
		lines: []string{
			`2026-09-27 10:06:01.001 UTC [812] ERROR:  relation "userz" does not exist at character 15`,
			"2026-09-27 10:06:01.002 UTC [27] LOG:  checkpoint starting: time",
			"2026-09-27 10:06:01.003 UTC [812] STATEMENT:  select * from userz",
		},
		want: []dbWant{
			{level: "error", at: "2026-09-27 10:06:01.001Z", event: "error", attrs: pgAttrs("812", "ERROR",
				"code", "42P01", "error_class", "syntax or access")},
			{level: "info", at: "2026-09-27 10:06:01.002Z", event: "checkpoint", attrs: pgAttrs("27", "LOG", "reason", "time")},
			{at: "2026-09-27 10:06:01.003Z"},
		},
	},
	{
		name: "other prefixes and zones",
		lines: []string{
			// pgBadger's recommended prefix, '%t [%p]: user=%u,db=%d,app=%a,client=%h '.
			"2026-09-27 10:00:00 UTC [812]: user=app,db=shop,app=psql,client=172.18.0.5 LOG:  statement: SELECT 1",
			// The research's recommended '%m [%p] %q%u@%d/%a %h %e '.
			`2026-09-27 10:00:00.500 UTC [813] app@shop/psql 172.18.0.5 42P01 ERROR:  relation "userz" does not exist at character 15`,
			// A log_timezone with no abbreviation is written as an offset.
			"2026-09-27 13:00:00.123 +03 [1] LOG:  database system is ready to accept connections",
		},
		want: []dbWant{
			{level: "info", at: "2026-09-27 10:00:00Z", event: "statement", attrs: pgAttrs("812", "LOG",
				"user", "app", "db", "shop", "app", "psql", "client", "172.18.0.5",
				"query", "SELECT 1", "fp", Fingerprint("SELECT 1"))},
			{level: "error", at: "2026-09-27 10:00:00.5Z", event: "error", attrs: pgAttrs("813", "ERROR",
				"user", "app", "db", "shop", "app", "psql", "client", "172.18.0.5", "code", "42P01",
				"error_class", "syntax or access")},
			{level: "info", at: "2026-09-27 10:00:00.123Z", event: "ready", attrs: pgAttrs("1", "LOG")},
		},
	},
	{
		name: "jsonlog carries the statement in the record",
		lines: []string{
			`{"timestamp":"2026-09-27 10:06:01.001 UTC","user":"app","dbname":"shop","pid":812,"remote_host":"172.18.0.5","remote_port":51234,"session_id":"66f6a2c5.32c","line_num":3,"ps":"SELECT","session_start":"2026-09-27 10:05:57 UTC","vxid":"3/12","txid":0,"error_severity":"ERROR","state_code":"42P01","message":"relation \"userz\" does not exist","statement":"select * from userz","cursor_position":15,"application_name":"psql","backend_type":"client backend","query_id":0}`,
		},
		want: []dbWant{
			{level: "error", at: "2026-09-27 10:06:01.001Z", event: "error", attrs: pgAttrs("812", "ERROR",
				"user", "app", "db", "shop", "app", "psql", "client", "172.18.0.5", "port", "51234",
				"code", "42P01", "error_class", "syntax or access", "query", "select * from userz",
				"fp", Fingerprint("select * from userz"))},
		},
	},
	{
		name: "an exposed port: scanners, a lost client, a restart after a crash",
		lines: []string{
			"2026-10-16 03:12:00.000 UTC [7001] LOG:  incomplete startup packet",
			"2026-10-16 03:12:01.000 UTC [7002] FATAL:  unsupported frontend protocol 0.0: server supports 3.0 to 3.0",
			"2026-10-16 03:12:02.000 UTC [7003] LOG:  could not accept SSL connection: wrong version number",
			"2026-10-16 03:12:03.000 UTC [7004] LOG:  could not receive data from client: Connection reset by peer",
			"2026-10-16 03:12:04.000 UTC [7005] LOG:  unexpected EOF on client connection with an open transaction",
			"2026-10-16 03:13:00.000 UTC [1] LOG:  all server processes terminated; reinitializing",
			"2026-10-16 03:13:00.100 UTC [7010] LOG:  database system was interrupted; last known up at 2026-10-16 03:10:00 UTC",
			"2026-10-16 03:13:00.200 UTC [7010] LOG:  database system was not properly shut down; automatic recovery in progress",
			"2026-10-16 03:13:00.300 UTC [7010] LOG:  redo starts at 0/1A82B58",
			"2026-10-16 03:13:00.400 UTC [7011] FATAL:  the database system is starting up",
		},
		want: []dbWant{
			{level: "warn", at: "2026-10-16 03:12:00Z", event: "probe", attrs: pgAttrs("7001", "LOG")},
			// A FATAL to Postgres, but a scanner's, not the server failing.
			{level: "warn", at: "2026-10-16 03:12:01Z", event: "probe", attrs: pgAttrs("7002", "FATAL",
				"code", "08P01", "error_class", "connection")},
			{level: "warn", at: "2026-10-16 03:12:02Z", event: "probe", attrs: pgAttrs("7003", "LOG")},
			{level: "warn", at: "2026-10-16 03:12:03Z", event: "client_lost", attrs: pgAttrs("7004", "LOG")},
			{level: "warn", at: "2026-10-16 03:12:04Z", event: "client_lost", attrs: pgAttrs("7005", "LOG")},
			{level: "warn", at: "2026-10-16 03:13:00Z", event: "recovery", attrs: pgAttrs("1", "LOG")},
			{level: "warn", at: "2026-10-16 03:13:00.1Z", event: "recovery", attrs: pgAttrs("7010", "LOG")},
			{level: "warn", at: "2026-10-16 03:13:00.2Z", event: "recovery", attrs: pgAttrs("7010", "LOG")},
			{level: "warn", at: "2026-10-16 03:13:00.3Z", event: "recovery", attrs: pgAttrs("7010", "LOG")},
			{level: "error", at: "2026-10-16 03:13:00.4Z", event: "fatal", attrs: pgAttrs("7011", "FATAL",
				"code", "57P03", "error_class", "operator")},
		},
	},
	{
		name: "the waits and the limits Docker sets",
		lines: []string{
			"2026-10-16 04:00:00.000 UTC [2078] LOG:  process 2078 acquired AccessExclusiveLock on relation 16384 of database 16385 after 4567.890 ms",
			`2026-10-16 04:00:01.000 UTC [2079] ERROR:  could not resize shared memory segment "/PostgreSQL.1197429420" to 12615680 bytes: No space left on device`,
			"2026-10-16 04:00:02.000 UTC [2080] ERROR:  canceling statement due to statement timeout",
			`2026-10-16 04:00:03.000 UTC [2081] WARNING:  database "shop" must be vacuumed within 9999999 transactions`,
			`2026-10-16 04:00:04.000 UTC [2082] ERROR:  database is not accepting commands to avoid wraparound data loss in database "shop"`,
			"2026-10-16 04:00:05.000 UTC [2083] LOG:  checkpoint starting: wal",
		},
		want: []dbWant{
			{level: "info", at: "2026-10-16 04:00:00Z", event: "lock_acquired", attrs: pgAttrs("2078", "LOG",
				"lock", "AccessExclusiveLock", "wait_ms", "4567.89")},
			// /dev/shm, not the disk: a different fix.
			{level: "error", at: "2026-10-16 04:00:01Z", event: "shm_full", attrs: pgAttrs("2079", "ERROR",
				"code", "53100", "error_class", "resources")},
			{level: "error", at: "2026-10-16 04:00:02Z", event: "cancel", attrs: pgAttrs("2080", "ERROR",
				"code", "57014", "error_class", "operator", "reason", "statement timeout")},
			{level: "warn", at: "2026-10-16 04:00:03Z", event: "wraparound", attrs: pgAttrs("2081", "WARNING", "db", "shop")},
			{level: "critical", at: "2026-10-16 04:00:04Z", event: "wraparound", attrs: pgAttrs("2082", "ERROR",
				"db", "shop", "code", "54000", "error_class", "limits")},
			{level: "info", at: "2026-10-16 04:00:05Z", event: "checkpoint", attrs: pgAttrs("2083", "LOG", "reason", "wal")},
		},
	},
}

func TestPostgresLens(t *testing.T) {
	dbRun(t, "postgres", postgresCases)
}

// A zone abbreviation other than UTC is resolved in the host's zone, as
// time.ParseInLocation resolves it: right when log_timezone is the host's,
// which is what Debian's cluster and the Docker image both default to.
func TestPostgresLensReadsZoneAbbreviationsInTheHostZone(t *testing.T) {
	const stamp = "2026-01-15 12:00:00.250 EET"
	got := readThrough(t, "postgres", stamp+" [1] LOG:  database system is ready to accept connections")
	want, err := time.ParseInLocation("2006-01-02 15:04:05 MST", stamp, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Timestamp == nil || !got[0].Timestamp.Equal(want) {
		t.Fatalf("stamped %v, want %v", got[0].Timestamp, want)
	}
}

// The viewer draws a line from its body: the prefix the lens read into the
// columns is not drawn twice, a part keeps the word that says what it is, and
// a line the lens read from a copy — jsonlog — is drawn whole.
func TestPostgresLensMarksWhereTheMessageStarts(t *testing.T) {
	lines := readThrough(t, "postgres",
		`2026-01-01 00:00:00.000 UTC [123] app@shop FATAL:  password authentication failed for user "app"`,
		`2026-01-01 00:00:00.000 UTC [123] app@shop DETAIL:  Role "app" does not exist.`,
		"\tConnection matched file \"/var/lib/postgresql/data/pg_hba.conf\" line 128",
		"waiting for server to start....2026-09-18 09:25:53.958 UTC [41] LOG:  starting PostgreSQL 16.15",
		`{"timestamp":"2026-09-27 10:06:01.001 UTC","pid":812,"error_severity":"ERROR","message":"relation \"userz\" does not exist"}`,
	)
	want := []string{
		`password authentication failed for user "app"`,
		`DETAIL:  Role "app" does not exist.`,
		"",
		"starting PostgreSQL 16.15",
		"",
	}
	for i, l := range lines {
		got := ""
		if l.Body > 0 {
			got = l.Text[l.Body:]
		}
		if got != want[i] {
			t.Errorf("line %d drawn from %q, want %q", i, got, want[i])
		}
	}
}

// The browser indexes the text in UTF-16 units, so a name outside ASCII in
// the prefix moves the body by its units, not its bytes.
func TestPostgresLensCountsTheBodyInUTF16(t *testing.T) {
	const prefix = "2026-01-01 00:00:00.000 UTC [123] ionuț@magazin LOG:  "
	l := readThrough(t, "postgres", prefix+"statement: SELECT 1")[0]
	if want := len(utf16.Encode([]rune(prefix))); l.Body != want {
		t.Fatalf("body at %d, want %d", l.Body, want)
	}
}

func BenchmarkLensPostgres(b *testing.B) {
	var texts []string
	for _, c := range postgresCases {
		texts = append(texts, c.lines...)
	}
	dbBench(b, "postgres", texts)
}
