package logsx

import (
	"testing"
	"time"
)

// f2bAt is where fail2ban's zoneless stamp lands: in local time.
func f2bAt(year int, month time.Month, day, hour, minute, second, milli int) string {
	return time.Date(year, month, day, hour, minute, second, milli*int(time.Millisecond), time.Local).
		UTC().Format(time.RFC3339Nano)
}

// The file lines are the research's, taken from real fail2ban logs; the ones
// its sources name but it has no line for are written from the format
// strings in fail2ban's actions.py, filter.py and jail.py.
func TestLensFail2banReadsItsLog(t *testing.T) {
	sysInZone(t)
	sysRead(t, "fail2ban",
		sysWant{
			text:  "2023-08-04 09:11:46,777 fail2ban.jail           [2144579]: INFO    Jail 'sshd' started",
			event: "jail_started", level: "info", at: f2bAt(2023, time.August, 4, 9, 11, 46, 777),
			attrs: map[string]string{"jail": "sshd", "pid": "2144579"},
		},
		sysWant{
			text:  "2023-08-04 09:11:46,772 fail2ban.filter         [2144579]: INFO    Added logfile: '/var/log/auth.log' (pos = 0, hash = 74ba42c5)",
			level: "info", at: f2bAt(2023, time.August, 4, 9, 11, 46, 772),
		},
		sysWant{
			text:  "2020-10-15 13:41:45,244 fail2ban.filter         [700]: INFO    [sshd] Found 203.0.113.42 - 2020-10-15 13:41:45",
			event: "found", level: "info", at: f2bAt(2020, time.October, 15, 13, 41, 45, 244),
			attrs: map[string]string{"jail": "sshd", "client": "203.0.113.42", "pid": "700"},
		},
		sysWant{
			// A ban is the moment fail2ban acted, and reads as a warning.
			text:  "2023-02-17 23:44:17,037 fail2ban.actions        [992]: NOTICE  [apache-auth] Ban 203.0.113.228",
			event: "ban", level: "warn", at: f2bAt(2023, time.February, 17, 23, 44, 17, 37),
			attrs: map[string]string{"jail": "apache-auth", "client": "203.0.113.228", "pid": "992"},
		},
		sysWant{
			text:  "2023-02-17 23:44:26,259 fail2ban.actions        [992]: NOTICE  [apache-auth] Unban 203.0.113.27",
			event: "unban", level: "info", at: f2bAt(2023, time.February, 17, 23, 44, 26, 259),
			attrs: map[string]string{"jail": "apache-auth", "client": "203.0.113.27", "pid": "992"},
		},
		sysWant{
			text:  "2017-08-27 04:22:42,625 fail2ban.actions        [12655]: NOTICE  [sshd] Restore Ban 198.51.100.4",
			event: "restore_ban", level: "info", at: f2bAt(2017, time.August, 27, 4, 22, 42, 625),
			attrs: map[string]string{"jail": "sshd", "client": "198.51.100.4", "pid": "12655"},
		},
		sysWant{
			text:  "2024-01-01 00:00:00,000 fail2ban.observer       [1]: NOTICE  [sshd] Increase Ban 198.51.100.9 (3 # 4:00:00 -> 2024-01-01 04:00:00)",
			event: "increase", level: "info", at: f2bAt(2024, time.January, 1, 0, 0, 0, 0),
			attrs: map[string]string{"jail": "sshd", "client": "198.51.100.9", "pid": "1"},
		},
		sysWant{
			text:  "2024-01-01 00:00:00,000 fail2ban.actions        [1]: WARNING [sshd] 198.51.100.9 already banned",
			event: "already_banned", level: "warn", at: f2bAt(2024, time.January, 1, 0, 0, 0, 0),
			attrs: map[string]string{"jail": "sshd", "client": "198.51.100.9", "pid": "1"},
		},
		sysWant{
			text:  "2024-01-01 00:00:03,118 fail2ban.filter         [1]: INFO    [sshd] Ignore 192.0.2.10 by ip",
			event: "ignore", level: "info", at: f2bAt(2024, time.January, 1, 0, 0, 3, 118),
			attrs: map[string]string{"jail": "sshd", "client": "192.0.2.10", "pid": "1"},
		},
		sysWant{
			text:  "2024-01-01 00:10:12,402 fail2ban.actions        [1]: ERROR   Failed to execute ban jail 'sshd' action 'iptables-multiport' info 'ActionInfo({'ip': '198.51.100.9'})': Error banning 198.51.100.9",
			event: "error", level: "error", at: f2bAt(2024, time.January, 1, 0, 10, 12, 402),
			attrs: map[string]string{"pid": "1"},
		},
		sysWant{
			text:  "2024-01-01 00:11:00,006 fail2ban.jail           [1]: INFO    Jail 'sshd' stopped",
			event: "jail_stopped", level: "info", at: f2bAt(2024, time.January, 1, 0, 11, 0, 6),
			attrs: map[string]string{"jail": "sshd", "pid": "1"},
		},
	)
}

// Logged to syslog, fail2ban's logger becomes the tag and the stamp is the
// envelope's; to the journal, the priority stands in for a missing level.
func TestLensFail2banReadsSyslogAndTheJournal(t *testing.T) {
	sysRead(t, "fail2ban",
		sysWant{
			text:  "2026-09-27T00:30:11.402118+00:00 web-1 fail2ban.actions[992]: NOTICE [sshd] Ban 203.0.113.228",
			event: "ban", level: "warn", at: "2026-09-27T00:30:11.402118Z",
			attrs: map[string]string{"jail": "sshd", "client": "203.0.113.228", "pid": "992"},
		},
	)
	got := sysReadJournal(t, "fail2ban",
		sysEntry{"[sshd] Found 203.0.113.42 - 2026-09-27 00:30:10", 6, map[string]string{"program": "fail2ban-server", "pid": "992"}},
	)
	sysCheck(t, got, []sysWant{{
		text: "found", event: "found", level: "info",
		attrs: map[string]string{"program": "fail2ban-server", "jail": "sshd", "client": "203.0.113.42", "pid": "992"},
	}})
}

func BenchmarkLensFail2ban(b *testing.B) {
	benchmarkSysLens(b, "fail2ban", []string{
		"2020-10-15 13:41:45,244 fail2ban.filter         [700]: INFO    [sshd] Found 203.0.113.42 - 2020-10-15 13:41:45",
		"2023-02-17 23:44:17,037 fail2ban.actions        [992]: NOTICE  [apache-auth] Ban 203.0.113.228",
		"2023-08-04 09:11:46,772 fail2ban.filter         [2144579]: INFO    Added logfile: '/var/log/auth.log' (pos = 0, hash = 74ba42c5)",
	})
}
