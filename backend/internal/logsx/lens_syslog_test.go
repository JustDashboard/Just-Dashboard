package logsx

import (
	"maps"
	"testing"
	"time"
)

// A stretch of this host's syslog: the service manager, cron, a daemon no
// lens knows, the firewall and an OOM report, each read by its own lens.
func TestLensSyslogHandsEachProgramToItsLens(t *testing.T) {
	sysInZone(t)
	sysRead(t, "syslog",
		sysWant{
			text:  "2026-09-20T00:54:38.653921+00:00 web-1 systemd[1]: nordvpnd.service: Scheduled restart job, restart counter is at 48215.",
			event: "restart_scheduled", level: "warn", at: "2026-09-20T00:54:38.653921Z", lens: "systemd",
			attrs: map[string]string{"program": "systemd", "pid": "1", "unit": "nordvpnd.service", "restarts": "48215"},
		},
		sysWant{
			text:  "2026-09-19T17:27:56.004880+00:00 web-1 nordvpnd[3964418]: 2026/09/19 17:27:56.004773 main.go:176: [Error] failed to cleanup config: Empty config read, aborting load of config",
			level: "error", at: "2026-09-19T17:27:56.00488Z",
			attrs: map[string]string{"program": "nordvpnd", "pid": "3964418"},
		},
		sysWant{
			text:  "2026-09-20T00:55:01.237076+00:00 web-1 CRON[1123364]: (root) CMD (command -v debian-sa1 > /dev/null && debian-sa1 1 1)",
			event: "run", at: "2026-09-20T00:55:01.237076Z", lens: "cron",
			attrs: map[string]string{"program": "CRON", "pid": "1123364", "user": "root", "command": "command -v debian-sa1 > /dev/null && debian-sa1 1 1"},
		},
		sysWant{
			// The kernel lens hands its packet lines on, and says so.
			text:  "2026-09-19T17:27:55.138263+00:00 web-1 kernel: [UFW BLOCK] IN=ens3 OUT= " + fwMAC + " SRC=203.0.113.28 DST=198.51.100.87 LEN=40 TOS=0x00 PREC=0x00 TTL=236 ID=42169 PROTO=TCP SPT=58084 DPT=3313 WINDOW=1024 RES=0x00 SYN URGP=0 ",
			event: "block", level: "info", at: "2026-09-19T17:27:55.138263Z", lens: "firewall",
			attrs: map[string]string{"program": "kernel", "client": "203.0.113.28", "dst": "198.51.100.87", "len": "40",
				"proto": "TCP", "spt": "58084", "dpt": "3313", "iface": "ens3", "flags": "SYN"},
		},
		sysWant{
			text:  "2026-09-19T17:27:56.876962+00:00 web-1 kernel: V8Worker invoked oom-killer: gfp_mask=0xcc0(GFP_KERNEL), order=0, oom_score_adj=0",
			event: "oom", level: "error", at: "2026-09-19T17:27:56.876962Z", lens: "kernel",
			attrs: map[string]string{"program": "V8Worker"},
		},
		sysWant{
			text: "2026-09-19T17:27:56.876995+00:00 web-1 kernel: CPU: 1 UID: 1000 PID: 3964373 Comm: V8Worker Tainted: G        W          6.14.0-37-generic #37-Ubuntu",
			at:   "2026-09-19T17:27:56.876995Z", level: "error", cont: true, lens: "kernel",
			attrs: map[string]string{"program": "kernel"},
		},
		sysWant{
			text:  "2026-09-19T17:27:56.879498+00:00 web-1 systemd[1]: user@1000.service: A process of this unit has been killed by the OOM killer.",
			event: "oom", level: "error", at: "2026-09-19T17:27:56.879498Z", lens: "systemd",
			attrs: map[string]string{"program": "systemd", "pid": "1", "unit": "user@1000.service"},
		},
		sysWant{
			// Still the kernel's report, but a systemd line now sits between
			// it and its head: folded there it would read as systemd's.
			text: "2026-09-19T17:27:56.879601+00:00 web-1 kernel: Tasks state (memory values in pages):",
			at:   "2026-09-19T17:27:56.879601Z", lens: "kernel",
			attrs: map[string]string{"program": "kernel"},
		},
		sysWant{
			text:  "2026-09-19T17:27:56.879702+00:00 web-1 kernel: Memory cgroup out of memory: Killed process 3963629 (next-build (v16) total-vm:22165088kB, anon-rss:1615396kB, file-rss:112520kB, shmem-rss:0kB, UID:1000 pgtables:7384kB oom_score_adj:0",
			event: "oom_kill", level: "error", at: "2026-09-19T17:27:56.879702Z", lens: "kernel",
			attrs: map[string]string{"program": "next-build (v16", "pid": "3963629", "memory": "1769385984"},
		},
		sysWant{
			text:  "2026-09-19T17:27:57.024620+00:00 web-1 systemd[546363]: run-p3963628-i54286731.scope: Failed with result 'oom-kill'.",
			event: "failed", level: "error", at: "2026-09-19T17:27:57.02462Z", lens: "systemd",
			attrs: map[string]string{"program": "systemd", "pid": "546363", "unit": "run-p3963628-i54286731.scope", "result": "oom-kill"},
		},
		sysWant{
			text:  "2026-09-19T17:27:57.024937+00:00 web-1 systemd[546363]: run-p3963628-i54286731.scope: Consumed 1min 37.388s CPU time, 2G memory peak.",
			event: "resources", at: "2026-09-19T17:27:57.024937Z", lens: "systemd",
			attrs: map[string]string{"program": "systemd", "pid": "546363", "unit": "run-p3963628-i54286731.scope", "cpu": "97388", "memory": "2147483648"},
		},
		sysWant{
			text:  "2026-09-13T05:13:26.234415+00:00 web-1 kernel: message repeated 2 times: [ [UFW BLOCK] IN=ens3 OUT= " + fwMAC + " SRC=203.0.113.76 DST=198.51.100.87 LEN=40 TOS=0x00 PREC=0x00 TTL=40 ID=54337 PROTO=TCP SPT=28282 DPT=23 WINDOW=41099 RES=0x00 SYN URGP=0 ]",
			event: "block", level: "info", at: "2026-09-13T05:13:26.234415Z", lens: "firewall",
			attrs: map[string]string{"program": "kernel", "client": "203.0.113.76", "dst": "198.51.100.87", "len": "40",
				"proto": "TCP", "spt": "28282", "dpt": "23", "iface": "ens3", "flags": "SYN"},
		},
		sysWant{
			text:  "2026-09-13T06:13:46.560774+00:00 web-1 dockerd[178972]: time=\"2026-09-13T06:13:46.560270609Z\" level=warning msg=\"failed to resolve container image\" containerID=79ae49c3e9cd error=\"Canceled: context canceled\" image=bet-bot-doubles-games-tracker",
			level: "warn", at: "2026-09-13T06:13:46.560774Z",
			attrs: map[string]string{"program": "dockerd", "pid": "178972"},
		},
		sysWant{
			// A BSD envelope is read in local time before the lens sees it.
			text:  "Sep  7 03:12:01 web-1 CRON[3113693]: (root) CMD (command -v debian-sa1 > /dev/null && debian-sa1 1 1)",
			event: "run", at: sysBSDWant(time.September, 7, 3, 12, 1), lens: "cron",
			attrs: map[string]string{"program": "CRON", "pid": "3113693", "user": "root", "command": "command -v debian-sa1 > /dev/null && debian-sa1 1 1"},
		},
		sysWant{
			text: "  at a line with no envelope at all",
		},
	)
}

// The whole journal: no envelope, the program already in Attrs, and each
// lens reading the entry exactly as it would from its own source.
func TestLensSyslogDispatchesTheJournal(t *testing.T) {
	got := sysReadJournal(t, "syslog",
		sysEntry{"Invalid user ekala from 203.0.113.42 port 30358", 6,
			map[string]string{"program": "sshd-session", "pid": "3110301", "unit": "ssh.service"}},
		sysEntry{"nordvpnd.service: Scheduled restart job, restart counter is at 171911.", 6,
			map[string]string{"program": "systemd", "pid": "1", "unit": "nordvpnd.service",
				"message_id": "5eb03494b6584870a536b337290809b3", "invocation": "9eca31f99c2c43209614dd099a3d6d28"}},
		sysEntry{"[UFW BLOCK] IN=ens3 OUT= " + fwMAC + " SRC=203.0.113.11 DST=198.51.100.87 LEN=40 TOS=0x00 PREC=0x00 TTL=238 ID=50812 PROTO=TCP SPT=54703 DPT=4448 WINDOW=1024 RES=0x00 SYN URGP=0 ", 4,
			map[string]string{"program": "kernel"}},
		sysEntry{"[sshd] Ban 203.0.113.228", 5, map[string]string{"program": "fail2ban-server", "pid": "992"}},
		sysEntry{"2026/09/19 17:27:56.000174 main.go:155: [Info] Daemon has started", 6,
			map[string]string{"program": "nordvpnd", "pid": "3964418"}},
	)
	sysCheck(t, got, []sysWant{
		{text: "sshd", event: "ssh_invalid_user", level: "info", lens: "auth",
			attrs: map[string]string{"program": "sshd-session", "pid": "3110301", "unit": "ssh.service", "user": "ekala", "client": "203.0.113.42", "port": "30358"}},
		{text: "systemd", event: "restart_scheduled", level: "warn", lens: "systemd",
			attrs: map[string]string{"program": "systemd", "pid": "1", "unit": "nordvpnd.service", "message_id": "5eb03494b6584870a536b337290809b3",
				"invocation": "9eca31f99c2c43209614dd099a3d6d28", "restarts": "171911"}},
		{text: "ufw", event: "block", level: "info", lens: "firewall",
			attrs: map[string]string{"program": "kernel", "client": "203.0.113.11", "dst": "198.51.100.87", "len": "40",
				"proto": "TCP", "spt": "54703", "dpt": "4448", "iface": "ens3", "flags": "SYN"}},
		{text: "ban", event: "ban", level: "warn", lens: "fail2ban",
			attrs: map[string]string{"program": "fail2ban-server", "pid": "992", "jail": "sshd", "client": "203.0.113.228"}},
		{text: "nordvpnd", level: "info", attrs: map[string]string{"program": "nordvpnd", "pid": "3964418"}},
	})
}

// The servers a host runs log through syslog or the journal too, and the
// composite must reach their lenses and not only the system's: Postgres with
// log_destination=syslog (its "[seq-part]" in front, and no prefix at all when
// log_line_prefix is left empty), MariaDB and Redis under systemd, nginx's
// error_log syslog: target, fail2ban and certbot. The lines are the research's
// and this host's, put behind the envelope rsyslog writes.
func TestLensSyslogReachesTheServersItNames(t *testing.T) {
	sysInZone(t)
	sysRead(t, "syslog",
		sysWant{
			text:  "2026-09-27T10:00:00.000100+00:00 web-1 postgres[1234]: [5-1] 2026-09-27 10:00:00.123 UTC [1234] LOG:  checkpoint starting: time",
			event: "checkpoint", level: "info", at: "2026-09-27T10:00:00.0001Z", lens: "postgres",
			attrs: map[string]string{"program": "postgres", "pid": "1234", "severity": "LOG"},
		},
		sysWant{
			text:  `2026-09-27T10:00:00.000200+00:00 web-1 postgres[1234]: [6-1] 2026-09-27 10:00:00.200 UTC [1234] postgres@shop ERROR:  relation "orders" does not exist at character 15`,
			event: "error", level: "error", at: "2026-09-27T10:00:00.0002Z", lens: "postgres",
			attrs: map[string]string{"program": "postgres", "pid": "1234", "severity": "ERROR", "code": "42P01", "user": "postgres", "db": "shop"},
		},
		sysWant{
			text:  "2026-09-27T10:00:00.000300+00:00 web-1 postgres[1234]: [7-1] 2026-09-27 10:00:00.200 UTC [1234] postgres@shop STATEMENT:  select * from orders",
			level: "error", cont: true, at: "2026-09-27T10:00:00.0003Z", lens: "postgres",
			attrs: map[string]string{"program": "postgres", "pid": "1234"},
		},
		sysWant{
			text:  `2026-09-27T10:00:00.000400+00:00 web-1 postgres[1234]: [8-1] FATAL:  password authentication failed for user "bob"`,
			event: "auth_failed", level: "error", at: "2026-09-27T10:00:00.0004Z", lens: "postgres",
			attrs: map[string]string{"program": "postgres", "pid": "1234", "severity": "FATAL", "code": "28P01", "user": "bob"},
		},
		sysWant{
			text:  "2026-09-27T10:00:01.000000+00:00 web-1 mariadbd[999]: 2026-09-27 10:00:01 0 [Note] mariadbd: ready for connections.",
			event: "ready", level: "info", at: "2026-09-27T10:00:01Z", lens: "mysql",
			attrs: map[string]string{"program": "mariadbd", "pid": "999"},
		},
		sysWant{
			text:  "2026-09-27T10:05:00.000000+00:00 web-1 mariadbd[999]: 2026-09-27 10:05:00 7 [Warning] Access denied for user 'root'@'172.18.0.1' (using password: YES)",
			event: "auth_failed", level: "warn", at: "2026-09-27T10:05:00Z", lens: "mysql",
			attrs: map[string]string{"program": "mariadbd", "pid": "999", "thread": "7", "user": "root", "client": "172.18.0.1"},
		},
		sysWant{
			text:  "2026-09-27T11:00:01.001000+00:00 web-1 redis-server[1]: 1:M 27 Sep 2026 11:00:01.001 * Background saving started by pid 42",
			event: "bgsave", level: "info", at: "2026-09-27T11:00:01.001Z", lens: "redis",
			attrs: map[string]string{"program": "redis-server", "pid": "1", "role": "primary"},
		},
		sysWant{
			text:  `2026-09-27T10:00:04.000000+00:00 web-1 nginx: 2026/09/27 10:00:04 [error] 12#12: *7 connect() failed (111: Connection refused) while connecting to upstream, client: 203.0.113.9, server: example.com, request: "GET / HTTP/1.1", upstream: "http://127.0.0.1:3000/", host: "example.com"`,
			event: "upstream_refused", level: "error", at: "2026-09-27T10:00:04Z", lens: "nginx-error",
			attrs: map[string]string{"program": "nginx", "pid": "12", "conn": "7", "code": "111", "client": "203.0.113.9",
				"method": "GET", "path": "/", "upstream": "127.0.0.1:3000", "host": "example.com"},
		},
		sysWant{
			text:  "2026-09-27T10:00:05.000000+00:00 web-1 fail2ban-server[55]: 2026-09-27 10:00:05,123 fail2ban.actions        [55]: NOTICE  [sshd] Ban 203.0.113.9",
			event: "ban", level: "warn", at: "2026-09-27T10:00:05Z", lens: "fail2ban",
			attrs: map[string]string{"program": "fail2ban-server", "pid": "55", "jail": "sshd", "client": "203.0.113.9"},
		},
		sysWant{
			text:  "2026-09-27T10:00:06.000000+00:00 web-1 certbot[66]: Certificate not yet due for renewal",
			event: "not_due", level: "info", at: "2026-09-27T10:00:06Z", lens: "certbot",
			attrs: map[string]string{"program": "certbot", "pid": "66"},
		},
	)

	// The same servers from the journal, where the program is already known
	// and the message is the server's own line.
	unit := func(program, pid, unit string) map[string]string {
		return map[string]string{"program": program, "pid": pid, "unit": unit}
	}
	with := func(m map[string]string, kv ...string) map[string]string {
		out := maps.Clone(m)
		for i := 0; i < len(kv); i += 2 {
			out[kv[i]] = kv[i+1]
		}
		return out
	}
	pg := unit("postgres", "1234", "postgresql@17-main.service")
	maria := unit("mariadbd", "999", "mariadb.service")
	redis := unit("redis-server", "1", "redis-server.service")
	got := sysReadJournal(t, "syslog",
		sysEntry{`2026-09-27 10:00:00.200 UTC [1234] postgres@shop ERROR:  relation "orders" does not exist at character 15`, 6, pg},
		sysEntry{"2026-09-27 10:00:00.200 UTC [1234] postgres@shop STATEMENT:  select * from orders", 6, pg},
		sysEntry{"2026-09-27 10:05:00 7 [Warning] Access denied for user 'root'@'172.18.0.1' (using password: YES)", 4, maria},
		sysEntry{"1:M 27 Sep 2026 11:00:01.001 * Background saving started by pid 42", 6, redis},
	)
	// The stamps are the lines' own; the engine puts the journal's back over
	// them, so only what the lenses name is compared here.
	for i := range got {
		got[i].Timestamp = nil
	}
	sysCheck(t, got, []sysWant{
		{text: "pg error", event: "error", level: "error", lens: "postgres",
			attrs: with(pg, "severity", "ERROR", "code", "42P01", "user", "postgres", "db", "shop")},
		{text: "pg statement", level: "error", cont: true, lens: "postgres", attrs: pg},
		{text: "mariadb denied", event: "auth_failed", level: "warn", lens: "mysql",
			attrs: with(maria, "thread", "7", "user", "root", "client", "172.18.0.1")},
		{text: "redis bgsave", event: "bgsave", level: "info", lens: "redis", attrs: with(redis, "role", "primary")},
	})
}

// Every program the composite names a lens for must reach a lens this build
// registers: a name missing from the registry reads as nothing at all.
func TestProgramLensNamesOnlyRegisteredLenses(t *testing.T) {
	for _, program := range []string{
		"sshd", "sshd-session", "sshd-auth", "sudo", "su", "login", "systemd-logind", "useradd",
		"usermod", "userdel", "groupadd", "groupmod", "groupdel", "passwd", "chpasswd", "gpasswd",
		"chage", "CRON", "crond", "kernel", "systemd", "systemd-coredump", "certbot", "mysqld",
		"mariadbd", "nginx", "fail2ban-server", "fail2ban.actions", "postgres", "postgresql@17-main",
		"redis-server", "valkey-server",
	} {
		id := ProgramLens(program)
		if lens, _ := LensByID(id); lens == nil {
			t.Errorf("%s: lens %q is not registered", program, id)
		}
	}
	if id := ProgramLens("nordvpnd"); id != "" {
		t.Errorf("a program with no lens of its own got %q", id)
	}
}

func BenchmarkLensSyslog(b *testing.B) {
	benchmarkSysLens(b, "syslog", []string{
		"2026-09-20T00:54:38.653921+00:00 web-1 systemd[1]: nordvpnd.service: Scheduled restart job, restart counter is at 48215.",
		"2026-09-19T17:27:56.004880+00:00 web-1 nordvpnd[3964418]: 2026/09/19 17:27:56.004773 main.go:176: [Error] failed to cleanup config: Empty config read, aborting load of config",
		"2026-09-20T00:54:38.656060+00:00 web-1 systemd[1]: Starting nordvpnd.socket - NordVPN Daemon Socket...",
		"2026-09-13T06:13:46.560774+00:00 web-1 dockerd[178972]: time=\"2026-09-13T06:13:46.560270609Z\" level=warning msg=\"failed to resolve container image\" containerID=79ae49c3e9cd error=\"Canceled: context canceled\" image=bet-bot-doubles-games-tracker",
		"2026-09-20T00:54:38.704611+00:00 web-1 systemd[1]: nordvpnd-killswitch.service: Failed with result 'exit-code'.",
		"2026-09-20T01:06:07.297757+00:00 web-1 kernel: docker0: port 6(veth63d8774) entered blocking state",
	})
}
