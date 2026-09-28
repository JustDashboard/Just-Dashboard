package logsx

import (
	"testing"
	"time"
)

// redisAttrs is a Redis line's pid and the role its letter names.
func redisAttrs(pid, role string) map[string]string {
	if role == "" {
		return map[string]string{"pid": pid}
	}
	return map[string]string{"pid": pid, "role": role}
}

// redisYearless is the stamp a line without a year must read as: this year's
// date, or last year's when this year's would be in the future.
func redisYearless(month time.Month, day, hour, minute, sec, ms int) string {
	now := time.Now()
	at := time.Date(now.Year(), month, day, hour, minute, sec, ms*1e6, time.Local)
	if at.After(now.Add(24 * time.Hour)) {
		at = at.AddDate(-1, 0, 0)
	}
	return at.Format("2006-01-02 15:04:05.000")
}

// The messages are Redis's own, as its sources (server.c, rdb.c, aof.c,
// replication.c, networking.c) write them and the research collected them.
var redisCases = []dbCase{
	{
		name: "a start, a snapshot and a shutdown",
		lines: []string{
			"1:C 27 Sep 2026 10:00:00.001 # WARNING Memory overcommit must be enabled! Without it, a background save or replication may fail under low memory condition. ... To fix this issue add 'vm.overcommit_memory = 1' to /etc/sysctl.conf and then reboot or run the command 'sysctl vm.overcommit_memory=1' for this to take effect.",
			"1:C 27 Sep 2026 10:00:00.001 * oO0OoO0OoO0Oo Redis is starting oO0OoO0OoO0Oo",
			"1:C 27 Sep 2026 10:00:00.001 * Redis version=7.4.1, bits=64, commit=00000000, modified=0, pid=1, just started",
			"1:C 27 Sep 2026 10:00:00.001 # Warning: no config file specified, using the default config. In order to specify a config file use redis-server /path/to/redis.conf",
			"1:M 27 Sep 2026 10:00:00.003 * Running mode=standalone, port=6379.",
			"1:M 27 Sep 2026 10:00:00.004 * Loading RDB produced by version 7.4.1",
			"1:M 27 Sep 2026 10:00:00.005 * Done loading RDB, keys loaded: 1203, keys expired: 4.",
			"1:M 27 Sep 2026 10:00:00.005 * DB loaded from disk: 0.002 seconds",
			"1:M 27 Sep 2026 10:00:00.005 * Ready to accept connections tcp",
			"1:M 27 Sep 2026 11:00:01.000 * 1 changes in 3600 seconds. Saving...",
			"1:M 27 Sep 2026 11:00:01.001 * Background saving started by pid 42",
			"42:C 27 Sep 2026 11:00:01.020 * DB saved on disk",
			"42:C 27 Sep 2026 11:00:01.021 * Fork CoW for RDB: current 0 MB, peak 0 MB, average 0 MB",
			"1:M 27 Sep 2026 11:00:01.101 * Background saving terminated with success",
			"1:signal-handler (1790506800) Received SIGTERM scheduling shutdown...",
			"1:M 27 Sep 2026 12:00:00.100 * User requested shutdown...",
			"1:M 27 Sep 2026 12:00:00.101 * Saving the final RDB snapshot before exiting.",
			"1:M 27 Sep 2026 12:00:00.120 # Redis is now ready to exit, bye bye...",
		},
		want: []dbWant{
			{level: "warn", at: "2026-09-27 10:00:00.001", event: "memory_warning", attrs: redisAttrs("1", "child")},
			{level: "info", at: "2026-09-27 10:00:00.001", event: "startup", attrs: redisAttrs("1", "child")},
			{level: "info", at: "2026-09-27 10:00:00.001", attrs: redisAttrs("1", "child")},
			{level: "warn", at: "2026-09-27 10:00:00.001", attrs: redisAttrs("1", "child")},
			{level: "info", at: "2026-09-27 10:00:00.003", attrs: redisAttrs("1", "primary")},
			{level: "info", at: "2026-09-27 10:00:00.004", event: "loading", attrs: redisAttrs("1", "primary")},
			{level: "info", at: "2026-09-27 10:00:00.005", event: "loading", attrs: redisAttrs("1", "primary")},
			{level: "info", at: "2026-09-27 10:00:00.005", event: "loading", attrs: redisAttrs("1", "primary")},
			{level: "info", at: "2026-09-27 10:00:00.005", event: "ready", attrs: redisAttrs("1", "primary")},
			{level: "info", at: "2026-09-27 11:00:01", event: "bgsave", attrs: redisAttrs("1", "primary")},
			{level: "info", at: "2026-09-27 11:00:01.001", event: "bgsave", attrs: redisAttrs("1", "primary")},
			{level: "info", at: "2026-09-27 11:00:01.020", event: "saved", attrs: redisAttrs("42", "child")},
			{level: "info", at: "2026-09-27 11:00:01.021", event: "bgsave", attrs: redisAttrs("42", "child")},
			{level: "info", at: "2026-09-27 11:00:01.101", event: "bgsave", attrs: redisAttrs("1", "primary")},
			// The signal handler writes an epoch and no level.
			{at: "2026-09-27 11:00:00Z", event: "shutdown", attrs: redisAttrs("1", "")},
			{level: "info", at: "2026-09-27 12:00:00.100", event: "shutdown", attrs: redisAttrs("1", "primary")},
			{level: "info", at: "2026-09-27 12:00:00.101", attrs: redisAttrs("1", "primary")},
			{level: "warn", at: "2026-09-27 12:00:00.120", event: "shutdown", attrs: redisAttrs("1", "primary")},
		},
	},
	{
		name: "persistence that failed is an error, not one more warning",
		lines: []string{
			"1:M 27 Sep 2026 13:00:00.000 # Can't save in background: fork: Cannot allocate memory",
			"1:M 27 Sep 2026 13:00:05.000 # Background saving error",
			"1:M 27 Sep 2026 13:01:00.000 * Background append only file rewriting started by pid 77",
			"1:M 27 Sep 2026 13:01:02.000 * Asynchronous AOF fsync is taking too long (disk is busy?). Writing the AOF buffer without waiting for fsync to complete, this may slow down Redis.",
			"1:M 27 Sep 2026 13:01:03.000 * Background AOF rewrite terminated with success",
			"1:M 27 Sep 2026 13:02:00.000 # Background AOF rewrite terminated with error",
		},
		want: []dbWant{
			{level: "error", at: "2026-09-27 13:00:00", event: "persistence_failed", attrs: redisAttrs("1", "primary")},
			{level: "error", at: "2026-09-27 13:00:05", event: "persistence_failed", attrs: redisAttrs("1", "primary")},
			{level: "info", at: "2026-09-27 13:01:00", event: "aof", attrs: redisAttrs("1", "primary")},
			{level: "info", at: "2026-09-27 13:01:02", event: "aof", attrs: redisAttrs("1", "primary")},
			{level: "info", at: "2026-09-27 13:01:03", event: "aof", attrs: redisAttrs("1", "primary")},
			{level: "error", at: "2026-09-27 13:02:00", event: "persistence_failed", attrs: redisAttrs("1", "primary")},
		},
	},
	{
		name: "a replica and its primary",
		lines: []string{
			"1:S 27 Sep 2026 14:00:00.000 * Connecting to MASTER 10.0.0.5:6379",
			"1:S 27 Sep 2026 14:00:00.001 * MASTER <-> REPLICA sync started",
			"1:S 27 Sep 2026 14:30:00.000 * Connection with master lost.",
			"1:M 27 Sep 2026 14:00:00.002 * Replica 10.0.0.6:6379 asks for synchronization",
			"1:M 27 Sep 2026 14:00:01.000 * Synchronization with replica 10.0.0.6:6379 succeeded",
		},
		want: []dbWant{
			{level: "info", at: "2026-09-27 14:00:00", event: "replication", attrs: redisAttrs("1", "replica")},
			{level: "info", at: "2026-09-27 14:00:00.001", event: "replication", attrs: redisAttrs("1", "replica")},
			{level: "info", at: "2026-09-27 14:30:00", event: "replication", attrs: redisAttrs("1", "replica")},
			{level: "info", at: "2026-09-27 14:00:00.002", event: "replication", attrs: redisAttrs("1", "primary")},
			{level: "info", at: "2026-09-27 14:00:01", event: "replication", attrs: redisAttrs("1", "primary")},
		},
	},
	{
		name: "clients and an attack on the port",
		lines: []string{
			"1:M 27 Sep 2026 15:00:00.000 # Possible SECURITY ATTACK detected. It looks like somebody is sending POST or Host: commands to Redis. This is likely due to an attacker attempting to use Cross Protocol Scripting to compromise your Redis instance. Connection from 203.0.113.9:51514 aborted.",
			"1:M 27 Sep 2026 15:00:01.000 - Client closed connection id=12 addr=172.18.0.5:40112 laddr=172.18.0.3:6379 fd=9 name= age=3 idle=0 flags=N db=0 sub=0 psub=0 ssub=0 multi=-1 watch=0 qbuf=0 qbuf-free=0 argv-mem=0 multi-mem=0 rbs=1024 rbp=0 obl=0 oll=0 omem=0 tot-mem=1920 events=r cmd=quit user=default redir=-1 resp=2",
			"1:M 27 Sep 2026 15:00:02.000 # Client id=31 addr=172.18.0.7:50522 laddr=172.18.0.3:6379 fd=11 name= age=40 idle=0 flags=P db=0 sub=0 psub=1 ssub=0 multi=-1 watch=0 qbuf=0 qbuf-free=0 argv-mem=0 multi-mem=0 rbs=1024 rbp=0 obl=0 oll=16384 omem=33554432 tot-mem=33556480 events=rw cmd=psubscribe user=default redir=-1 resp=2 scheduled to be closed ASAP for overcoming of output buffer limits.",
		},
		want: []dbWant{
			{level: "warn", at: "2026-09-27 15:00:00", event: "security_attack", attrs: redisAttrs("1", "primary")},
			{level: "debug", at: "2026-09-27 15:00:01", event: "client_closed", attrs: redisAttrs("1", "primary")},
			{level: "warn", at: "2026-09-27 15:00:02", event: "client_closed", attrs: redisAttrs("1", "primary")},
		},
	},
	{
		name: "a crash report folds under its banner until the next start",
		lines: []string{
			"",
			"=== REDIS BUG REPORT START: Cut & paste starting from here ===",
			"1:M 27 Sep 2026 16:00:00.000 # Redis 7.4.1 crashed by signal: 11, si_code: 1",
			"1:M 27 Sep 2026 16:00:00.000 # Accessing address: 0x0",
			"------ STACK TRACE ------",
			"=== REDIS BUG REPORT END. Make sure to include from START to END. ===",
			"       Please report the crash by opening an issue on github:",
			"1:C 27 Sep 2026 16:00:05.000 * oO0OoO0OoO0Oo Redis is starting oO0OoO0OoO0Oo",
		},
		want: []dbWant{
			{},
			{level: "critical", event: "crash"},
			{level: "critical", at: "2026-09-27 16:00:00", cont: true},
			{level: "critical", at: "2026-09-27 16:00:00", cont: true},
			{level: "critical", cont: true},
			{level: "critical", cont: true},
			{},
			{level: "info", at: "2026-09-27 16:00:05", event: "startup", attrs: redisAttrs("1", "child")},
		},
	},
	{
		name: "older Redis without a year, and Valkey's JSON",
		lines: []string{
			"[4018] 14 Nov 07:01:22.119 # Server started, Redis version 2.8.4",
			"7297:M 16 Jan 10:34:45.393 * DB saved on disk",
			`{"pid":1,"role":"primary","timestamp":"2026-09-27T10:00:00.005+00:00","level":"notice","message":"Ready to accept connections tcp"}`,
		},
		want: []dbWant{
			{level: "warn", at: redisYearless(time.November, 14, 7, 1, 22, 119), event: "startup", attrs: redisAttrs("4018", "")},
			{level: "info", at: redisYearless(time.January, 16, 10, 34, 45, 393), event: "saved", attrs: redisAttrs("7297", "primary")},
			{level: "info", at: "2026-09-27 10:00:00.005Z", event: "ready", attrs: redisAttrs("1", "primary")},
		},
	},
}

func TestRedisLens(t *testing.T) {
	dbRun(t, "redis", redisCases)
}

func BenchmarkLensRedis(b *testing.B) {
	var texts []string
	for _, c := range redisCases {
		texts = append(texts, c.lines...)
	}
	dbBench(b, "redis", texts)
}
