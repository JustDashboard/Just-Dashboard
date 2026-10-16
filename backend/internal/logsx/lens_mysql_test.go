package logsx

import "testing"

// myAttrs is a map from pairs, for the want tables.
func myAttrs(pairs ...string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return m
}

// Lines from the MySQL and MariaDB error log documentation and bug reports
// the research collected; MySQL's own stamps are UTC, MariaDB's are the
// host's local time.
var mysqlCases = []dbCase{
	{
		name: "MySQL 8 starts, refuses a login, drops a client and shuts down",
		lines: []string{
			"2026-09-27T10:00:00.123456Z 0 [System] [MY-010116] [Server] /usr/sbin/mysqld (mysqld 8.4.2) starting as process 1",
			"2026-09-27T10:00:01.002345Z 0 [Warning] [MY-010068] [Server] CA certificate ca.pem is self signed.",
			"2026-09-27T10:00:01.500000Z 0 [System] [MY-010931] [Server] /usr/sbin/mysqld: ready for connections. Version: '8.4.2'  socket: '/var/run/mysqld/mysqld.sock'  port: 3306  MySQL Community Server - GPL.",
			"2026-09-27T10:05:00.000000Z 113 [Note] [MY-010926] [Server] Access denied for user 'root'@'172.18.0.1' (using password: YES)",
			"2026-09-27T10:06:00.000000Z 12 [Note] [MY-010914] [Server] Aborted connection 12 to db: 'shop' user: 'app' host: '172.18.0.5' (Got an error reading communication packets).",
			"2026-09-27T11:00:00.000000Z 0 [System] [MY-013172] [Server] Received SHUTDOWN from user <via user signal>. Shutting down mysqld (Version: 8.4.2).",
			"2026-09-27T11:00:02.000000Z 0 [System] [MY-010910] [Server] /usr/sbin/mysqld: Shutdown complete (mysqld 8.4.2)  MySQL Community Server - GPL.",
		},
		want: []dbWant{
			{level: "info", at: "2026-09-27 10:00:00.123456Z", event: "startup", attrs: myAttrs("code", "MY-010116", "component", "Server")},
			{level: "warn", at: "2026-09-27 10:00:01.002345Z", event: "config_warning", attrs: myAttrs("code", "MY-010068", "component", "Server")},
			{level: "info", at: "2026-09-27 10:00:01.5Z", event: "ready", attrs: myAttrs("code", "MY-010931", "component", "Server")},
			{level: "info", at: "2026-09-27 10:05:00Z", event: "auth_failed", attrs: myAttrs("thread", "113",
				"code", "MY-010926", "component", "Server", "user", "root", "client", "172.18.0.1")},
			{level: "info", at: "2026-09-27 10:06:00Z", event: "aborted_connection", attrs: myAttrs("thread", "12",
				"code", "MY-010914", "component", "Server", "db", "shop", "user", "app", "client", "172.18.0.5",
				"reason", "Got an error reading communication packets")},
			{level: "info", at: "2026-09-27 11:00:00Z", event: "shutdown", attrs: myAttrs("code", "MY-013172", "component", "Server")},
			{level: "info", at: "2026-09-27 11:00:02Z", event: "shutdown", attrs: myAttrs("code", "MY-010910", "component", "Server")},
		},
	},
	{
		name: "MariaDB's local stamps, its storage engine prefix and its Version line",
		lines: []string{
			"2026-09-27 10:00:00 0 [Note] Starting MariaDB 11.4.3-MariaDB-ubu2404 source revision ... as process 1",
			"2026-09-27 10:00:00 0 [Note] InnoDB: The InnoDB memory heap is disabled",
			"2026-09-27 10:00:01 0 [Note] mariadbd: ready for connections.",
			"Version: '11.4.3-MariaDB-ubu2404'  socket: '/run/mysqld/mysqld.sock'  port: 3306  mariadb.org binary distribution",
			"2026-09-27 10:05:00 7 [Warning] Access denied for user 'root'@'172.18.0.1' (using password: YES)",
			"2026-09-27 10:06:00 35 [Warning] Aborted connection 35 to db: 'unconnected' user: 'user1' host: '192.168.1.40' (Got an error writing communication packets)",
			"2026-09-27 10:06:30 53 [Warning] Aborted connection 53 to db: 'db1' user: 'user2' host: '192.168.1.50' (KILLED)",
			"2024-01-01  0:00:00 0 [Note] mariadbd: ready for connections.",
		},
		want: []dbWant{
			{level: "info", at: "2026-09-27 10:00:00", event: "startup"},
			{level: "info", at: "2026-09-27 10:00:00", event: "innodb", attrs: myAttrs("component", "InnoDB")},
			{level: "info", at: "2026-09-27 10:00:01", event: "ready"},
			{level: "info", cont: true},
			{level: "warn", at: "2026-09-27 10:05:00", event: "auth_failed", attrs: myAttrs("thread", "7",
				"user", "root", "client", "172.18.0.1")},
			// 'unconnected' is MariaDB's word for no database, not a name.
			{level: "warn", at: "2026-09-27 10:06:00", event: "aborted_connection", attrs: myAttrs("thread", "35",
				"user", "user1", "client", "192.168.1.40", "reason", "Got an error writing communication packets")},
			{level: "warn", at: "2026-09-27 10:06:30", event: "aborted_connection", attrs: myAttrs("thread", "53",
				"db", "db1", "user", "user2", "client", "192.168.1.50", "reason", "KILLED")},
			// The hour padded with a space: the generic parser drops this stamp.
			{level: "info", at: "2024-01-01 00:00:00", event: "ready"},
		},
	},
	{
		name: "the Docker entrypoint's own lines",
		lines: []string{
			"2026-09-27 10:00:00+00:00 [Note] [Entrypoint]: Entrypoint script for MySQL Server 8.4.2-1.el9 started.",
			"2026-09-27 10:00:00+00:00 [Warn] [Entrypoint]: MYSQL_PASSWORD specified, but missing MYSQL_USER; MYSQL_PASSWORD will be ignored",
		},
		want: []dbWant{
			{level: "info", at: "2026-09-27 10:00:00Z", attrs: myAttrs("component", "Entrypoint")},
			{level: "warn", at: "2026-09-27 10:00:00Z", attrs: myAttrs("component", "Entrypoint")},
		},
	},
	{
		name: "an InnoDB deadlock dump folds under its head",
		lines: []string{
			"2026-09-27T10:07:00.000000Z 20 [Note] [MY-012468] [InnoDB] Transactions deadlock detected, dumping detailed information.",
			"*** (1) TRANSACTION:",
			"TRANSACTION 43260, ACTIVE 186 sec starting index read",
			"mysql tables in use 1, locked 1",
			"LOCK WAIT 4 lock struct(s), heap size 1128, 2 row lock(s)",
			"MySQL thread id 19, OS thread handle 139815619204864, query id 143 localhost u2 updating",
			"UPDATE Animals SET value=30 WHERE name='Aardvark'",
			"2026-09-27T10:07:00.000000Z 20 [Note] [MY-012469] [InnoDB] *** (2) TRANSACTION:",
			"*** WE ROLL BACK TRANSACTION (2)",
			"2026-09-27T10:08:00.000000Z 0 [Note] InnoDB: page_cleaner: 1000ms intended loop took 4013ms. The settings might not be optimal. (flushed=121 and evicted=0, during the time.)",
		},
		want: []dbWant{
			{level: "info", at: "2026-09-27 10:07:00Z", event: "deadlock", attrs: myAttrs("thread", "20",
				"code", "MY-012468", "component", "InnoDB")},
			{level: "info", cont: true},
			{level: "info", cont: true},
			{level: "info", cont: true},
			{level: "info", cont: true},
			{level: "info", cont: true},
			{level: "info", cont: true},
			// MySQL 8 writes each section of the dump as a line of its own.
			{level: "info", at: "2026-09-27 10:07:00Z", cont: true},
			{level: "info", cont: true},
			// The page cleaner falling behind is the disk, not InnoDB news.
			{level: "warn", at: "2026-09-27 10:08:00Z", event: "io_slow", attrs: myAttrs("component", "InnoDB", "duration_ms", "4013")},
		},
	},
	{
		name: "crashes and their traces",
		lines: []string{
			"2023-06-26T08:20:10Z UTC - mysqld got signal 11 ;",
			"Most likely, you have hit a bug, but this error can also be caused by malfunctioning hardware.",
			"Thread pointer: 0x7f2a8c000b60",
			"231015  9:57:23 [ERROR] mysqld got signal 11 ;",
		},
		want: []dbWant{
			{level: "critical", at: "2023-06-26 08:20:10Z", event: "crash"},
			{level: "critical", cont: true},
			{level: "critical", cont: true},
			{level: "critical", at: "2023-10-15 09:57:23", event: "crash"},
		},
	},
	{
		name: "limits, replication and plain errors",
		lines: []string{
			"2026-09-27 10:06:40 0 [Warning] Aborted connection 60 to db: 'unconnected' user: 'unauthenticated' host: '172.18.0.5' (Too many connections)",
			"2026-09-27T10:10:00.000000Z 5 [System] [MY-010562] [Repl] Replica I/O thread for channel '': connected to source 'repl@172.18.0.2:3306',replication started in log 'binlog.000002' at position 157",
			"2026-09-27 10:10:00 5 [Note] Slave I/O thread: connected to master 'repl@172.18.0.2:3306',replication started in log 'mysql-bin.000002' at position 342",
			"2026-09-27T10:11:00.000000Z 0 [ERROR] [MY-010119] [Server] Aborting",
			"2026-09-27 10:11:00 0 [ERROR] Can't open and lock privilege tables: Table 'mysql.user' doesn't exist",
		},
		want: []dbWant{
			{level: "warn", at: "2026-09-27 10:06:40", event: "too_many_connections", attrs: myAttrs("client", "172.18.0.5")},
			{level: "info", at: "2026-09-27 10:10:00Z", event: "replication", attrs: myAttrs("thread", "5",
				"code", "MY-010562", "component", "Repl")},
			{level: "info", at: "2026-09-27 10:10:00", event: "replication", attrs: myAttrs("thread", "5")},
			{level: "critical", at: "2026-09-27 10:11:00Z", event: "startup_failed", attrs: myAttrs("code", "MY-010119", "component", "Server")},
			{level: "error", at: "2026-09-27 10:11:00", event: "error"},
		},
	},
	{
		name: "a restart nothing shut down, and the server that will not come back",
		lines: []string{
			"2026-09-27T10:00:00.000000Z 0 [Note] InnoDB: Database was not shutdown normally!",
			"2026-09-27T10:00:00.000100Z 0 [Note] InnoDB: Starting crash recovery.",
			"2026-09-27 10:00:01 0 [Note] InnoDB: Starting crash recovery from checkpoint LSN=1630816",
			"2026-09-27 10:02:00 0 [ERROR] InnoDB: mmap(137428992 bytes) failed; errno 12",
			"2026-09-27 10:02:00 0 [ERROR] InnoDB: Cannot allocate memory for the buffer pool",
			"2026-09-27 10:02:01 0 [ERROR] mysqld: Out of memory (Needed 128917504 bytes)",
			"2026-09-27 10:02:02 0 [ERROR] Aborting",
		},
		want: []dbWant{
			{level: "warn", at: "2026-09-27 10:00:00Z", event: "recovery", attrs: myAttrs("component", "InnoDB")},
			{level: "warn", at: "2026-09-27 10:00:00.0001Z", event: "recovery", attrs: myAttrs("component", "InnoDB")},
			{level: "warn", at: "2026-09-27 10:00:01", event: "recovery", attrs: myAttrs("component", "InnoDB")},
			{level: "critical", at: "2026-09-27 10:02:00", event: "oom", attrs: myAttrs("component", "InnoDB")},
			{level: "critical", at: "2026-09-27 10:02:00", event: "oom", attrs: myAttrs("component", "InnoDB")},
			{level: "critical", at: "2026-09-27 10:02:01", event: "oom"},
			{level: "critical", at: "2026-09-27 10:02:02", event: "startup_failed"},
		},
	},
	{
		name: "a full disk, full and crashed tables, and a disk that cannot keep up",
		lines: []string{
			"2026-09-27 11:00:00 0 [ERROR] Disk is full writing './binlog.000012' (OS errno 28 - No space left on device). Waiting for someone to free space... (Expect up to 60 secs delay for server to continue after freeing disk space)",
			"2026-09-27 11:00:01 0 [ERROR] InnoDB: Operating system error number 28 in a file operation.",
			"2026-09-27 11:00:02 5 [ERROR] mysqld: The table '/tmp/#sql1_2a_0' is full",
			"2026-09-27 11:00:03 6 [ERROR] mysqld: Table './app/sessions' is marked as crashed and should be repaired",
			"2026-09-27T11:00:04.000000Z 0 [Note] InnoDB: page_cleaner: 1000ms intended loop took 5585ms. The settings might not be optimal. (flushed=0 and evicted=0, during the time.)",
			"2026-09-27 11:00:05 0 [Warning] InnoDB: A long semaphore wait:",
			"--Thread 139 has waited at btr0sea.cc line 1145 for 241 seconds the semaphore:",
			"2026-09-27 11:10:05 0 [ERROR] [FATAL] InnoDB: Semaphore wait has lasted > 600 seconds. We intentionally crash the server because it appears to be hung.",
		},
		want: []dbWant{
			{level: "critical", at: "2026-09-27 11:00:00", event: "disk_full"},
			{level: "critical", at: "2026-09-27 11:00:01", event: "disk_full", attrs: myAttrs("component", "InnoDB")},
			{level: "error", at: "2026-09-27 11:00:02", event: "table_full", attrs: myAttrs("thread", "5", "table", "/tmp/#sql1_2a_0")},
			{level: "error", at: "2026-09-27 11:00:03", event: "table_crashed", attrs: myAttrs("thread", "6", "table", "./app/sessions")},
			{level: "warn", at: "2026-09-27 11:00:04Z", event: "io_slow", attrs: myAttrs("component", "InnoDB", "duration_ms", "5585")},
			{level: "warn", at: "2026-09-27 11:00:05", event: "io_slow", attrs: myAttrs("component", "InnoDB")},
			{level: "warn", cont: true},
			{level: "critical", at: "2026-09-27 11:10:05", event: "crash"},
		},
	},
	{
		name: "replication that stopped or lost its source",
		lines: []string{
			"2026-09-27T12:00:00.000000Z 8 [ERROR] [MY-010584] [Repl] Replica SQL for channel '': Worker 1 failed executing transaction 'ANONYMOUS' at source log binlog.000004, end_log_pos 523; Could not execute Write_rows event on table app.users; Duplicate entry '1' for key 'users.PRIMARY', Error_code: MY-001062; handler error HA_ERR_FOUND_DUP_KEY",
			"2026-09-27 12:01:00 9 [ERROR] Slave SQL: Error 'Duplicate entry '1' for key 'PRIMARY'' on query. Default database: 'app'. Query: 'insert into users values (1)', Error_code: 1062",
			"2026-09-27T12:02:00.000000Z 10 [ERROR] [MY-010584] [Repl] Replica I/O for channel '': Error reconnecting to source 'repl@10.0.0.1:3306'. This was attempt 3/10, with a delay of 5 seconds between attempts. Message: Can't connect to MySQL server on '10.0.0.1:3306' (111)",
		},
		want: []dbWant{
			{level: "error", at: "2026-09-27 12:00:00Z", event: "replication_failed", attrs: myAttrs("thread", "8",
				"code", "MY-010584", "component", "Repl")},
			{level: "error", at: "2026-09-27 12:01:00", event: "replication_failed", attrs: myAttrs("thread", "9", "code", "1062")},
			{level: "error", at: "2026-09-27 12:02:00Z", event: "replication_failed", attrs: myAttrs("thread", "10",
				"code", "MY-010584", "component", "Repl", "client", "10.0.0.1")},
		},
	},
}

func TestMysqlLens(t *testing.T) {
	dbRun(t, "mysql", mysqlCases)
}

func BenchmarkLensMysql(b *testing.B) {
	var texts []string
	for _, c := range mysqlCases {
		texts = append(texts, c.lines...)
	}
	dbBench(b, "mysql", texts)
}

// The viewer draws a MySQL line from its message: the stamp, thread, label,
// code and subsystem are the time, the level and the attrs beside it.
func TestMySQLLensMarksWhereTheMessageStarts(t *testing.T) {
	l := readThrough(t, "mysql",
		"2026-09-27T10:00:00.123456Z 8 [Warning] [MY-013360] [Server] Plugin mysql_native_password reported: ''mysql_native_password' is deprecated'")[0]
	if got := l.Text[l.Body:]; got != "Plugin mysql_native_password reported: ''mysql_native_password' is deprecated'" {
		t.Fatalf("drawn from %q", got)
	}
}

// A replication source written as an IPv6 address keeps its brackets in the
// message; the address inside them is the client.
func TestMySQLLensReadsAnIPv6ReplicationSource(t *testing.T) {
	l := readThrough(t, "mysql",
		"2026-09-27T12:02:00.000000Z 10 [ERROR] [MY-010584] [Repl] Replica I/O for channel '': Error reconnecting to source 'repl@[2001:db8::1]:3306'. This was attempt 3/10, with a delay of 5 seconds between attempts.")[0]
	if l.Event != "replication_failed" || l.Attrs["client"] != "2001:db8::1" {
		t.Fatalf("read as %q with client %q", l.Event, l.Attrs["client"])
	}
}
