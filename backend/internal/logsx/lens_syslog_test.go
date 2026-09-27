package logsx

import (
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
