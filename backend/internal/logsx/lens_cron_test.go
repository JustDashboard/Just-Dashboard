package logsx

import (
	"testing"
	"time"
)

// The CMD and PAM lines are this host's. The "(CRON)" follow-ups are the
// research's, under the pid of a real run, which is where cron writes them.
func TestLensCronReadsRunsAndWhatTheyLost(t *testing.T) {
	sysInZone(t)
	const hourly = "cd / && run-parts --report /etc/cron.hourly"
	sysRead(t, "cron",
		sysWant{
			text:  "2026-09-20T00:55:01.237076+00:00 web-1 CRON[1123364]: (root) CMD (command -v debian-sa1 > /dev/null && debian-sa1 1 1)",
			event: "run", at: "2026-09-20T00:55:01.237076Z",
			attrs: map[string]string{"user": "root", "command": "command -v debian-sa1 > /dev/null && debian-sa1 1 1", "pid": "1123364"},
		},
		sysWant{
			text:  "2026-09-20T01:17:01.279883+00:00 web-1 CRON[1212947]: (root) CMD (cd / && run-parts --report /etc/cron.hourly)",
			event: "run", at: "2026-09-20T01:17:01.279883Z",
			attrs: map[string]string{"user": "root", "command": hourly, "pid": "1212947"},
		},
		sysWant{
			text:  "2026-09-20T01:17:01.614402+00:00 web-1 CRON[1212947]: (CRON) info (No MTA installed, discarding output)",
			event: "output_discarded", level: "warn", at: "2026-09-20T01:17:01.614402Z",
			attrs: map[string]string{"user": "root", "command": hourly, "pid": "1212947"},
		},
		sysWant{
			text:  "2026-09-20T01:17:01.618730+00:00 web-1 CRON[1212947]: (CRON) error (grandchild #1212949 failed with exit status 1)",
			event: "error", level: "error", at: "2026-09-20T01:17:01.61873Z",
			attrs: map[string]string{"user": "root", "command": hourly, "pid": "1212947"},
		},
		sysWant{
			text:  "2026-09-20T00:55:01.236375+00:00 web-1 CRON[1123363]: pam_unix(cron:session): session opened for user root(uid=0) by root(uid=0)",
			event: "session", level: "debug", at: "2026-09-20T00:55:01.236375Z",
			attrs: map[string]string{"user": "root", "pid": "1123363"},
		},
		sysWant{
			text:  "2026-09-20T00:55:01.240479+00:00 web-1 CRON[1123363]: pam_unix(cron:session): session closed for user root",
			event: "session", level: "debug", at: "2026-09-20T00:55:01.240479Z",
			attrs: map[string]string{"user": "root", "pid": "1123363"},
		},
		sysWant{
			// The daemon's own start-up line names no job.
			text:  "2026-09-20T00:00:02.118211+00:00 web-1 cron[611]: (CRON) INFO (pidfile fd = 3)",
			level: "info", at: "2026-09-20T00:00:02.118211Z",
		},
		sysWant{
			// cronie's /var/log/cron on a RHEL host: BSD stamps, local time.
			text:  "Sep  7 03:01:01 web-1 CROND[12345]: (root) CMD (run-parts /etc/cron.hourly)",
			event: "run", at: sysBSDWant(time.September, 7, 3, 1, 1),
			attrs: map[string]string{"user": "root", "command": "run-parts /etc/cron.hourly", "pid": "12345"},
		},
	)
}

func TestLensCronReadsTheJournal(t *testing.T) {
	got := sysReadJournal(t, "cron",
		sysEntry{"(root) CMD (test -x /usr/sbin/anacron || { cd / && run-parts --report /etc/cron.daily; })", 6,
			map[string]string{"program": "CRON", "pid": "1402217"}},
		sysEntry{"(CRON) info (No MTA installed, discarding output)", 6,
			map[string]string{"program": "CRON", "pid": "1402217"}},
	)
	const daily = "test -x /usr/sbin/anacron || { cd / && run-parts --report /etc/cron.daily; }"
	sysCheck(t, got, []sysWant{
		{text: "run", event: "run", level: "info", attrs: map[string]string{"program": "CRON", "pid": "1402217", "user": "root", "command": daily}},
		{text: "discarded", event: "output_discarded", level: "warn", attrs: map[string]string{"program": "CRON", "pid": "1402217", "user": "root", "command": daily}},
	})
}

func BenchmarkLensCron(b *testing.B) {
	benchmarkSysLens(b, "cron", []string{
		"2026-09-20T00:55:01.237076+00:00 web-1 CRON[1123364]: (root) CMD (command -v debian-sa1 > /dev/null && debian-sa1 1 1)",
		"2026-09-20T00:55:01.236375+00:00 web-1 CRON[1123363]: pam_unix(cron:session): session opened for user root(uid=0) by root(uid=0)",
		"2026-09-20T01:17:01.614402+00:00 web-1 CRON[1212947]: (CRON) info (No MTA installed, discarding output)",
	})
}
