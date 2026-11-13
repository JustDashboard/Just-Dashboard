package netsec

import (
	"testing"
	"time"
)

// The rows below are copied from `who -u` on Ubuntu 25.04 with OpenSSH 9.9. The
// first is the shape that broke the positional parser: the line column is two
// words, so the terminal, the login time and — the one that matters — the PID
// all landed a place to the left of where they were read.

func TestParseWhoLineFindsTheColumnsAroundTheDate(t *testing.T) {
	sess, ok := parseWhoLine("ubuntu   sshd pts/3   2026-08-27 02:07   .       2392157 (77.89.203.46)")
	if !ok {
		t.Fatal("row not read")
	}
	if sess.User != "ubuntu" {
		t.Errorf("user = %q", sess.User)
	}
	if sess.TTY != "pts/3" {
		t.Errorf("tty = %q, want the terminal rather than what wrote the entry", sess.TTY)
	}
	if sess.LoginTime == nil || sess.LoginTime.Format("2006-01-02 15:04") != "2026-08-27 02:07" {
		t.Errorf("loginTime = %v", sess.LoginTime)
	}
	// The PID is the whole point: without it there is no session to end, and
	// an SSH login is the only kind anybody would want to.
	if sess.PID != 2392157 {
		t.Errorf("pid = %d, want 2392157", sess.PID)
	}
	if sess.From != "77.89.203.46" {
		t.Errorf("from = %q", sess.From)
	}
	if sess.Idle != "active" {
		t.Errorf("idle = %q, want who's full stop said in words", sess.Idle)
	}
}

func TestParseWhoLineStillReadsTheClassicShape(t *testing.T) {
	sess, ok := parseWhoLine("ubuntu   pts/2        2026-08-26 21:33 03:03     2262305")
	if !ok {
		t.Fatal("row not read")
	}
	if sess.TTY != "pts/2" || sess.PID != 2262305 || sess.Idle != "03:03" {
		t.Fatalf("session = %+v", sess)
	}
	if sess.From != "" {
		t.Errorf("from = %q, want empty for a local login", sess.From)
	}
}

func TestParseWhoLineRefusesARowWithNoDate(t *testing.T) {
	for _, line := range []string{"", "ubuntu", "NAME LINE TIME COMMENT", "ubuntu pts/0 not-a-date 10:00"} {
		if _, ok := parseWhoLine(line); ok {
			t.Errorf("read a session out of %q", line)
		}
	}
}

// A comment column holding a hostname rather than an address still identifies
// where somebody came from.
func TestParseWhoLineKeepsAHostnameComment(t *testing.T) {
	sess, ok := parseWhoLine("root     pts/0        2026-08-26 09:12 00:41     1180 (workstation.local)")
	if !ok {
		t.Fatal("row not read")
	}
	if sess.From != "workstation.local" {
		t.Errorf("from = %q", sess.From)
	}
}

// The C locale prints `Aug 27` where C.UTF-8 prints `2026-08-27`, and LC_ALL=C
// is what this code sets — so the shape it asked for has to be the shape it
// reads. Getting this backwards emptied the session list completely, which
// looks exactly like a machine nobody is logged into.
func TestParseWhoLineReadsTheCLocaleDate(t *testing.T) {
	sess, ok := parseWhoLine("ubuntu   sshd pts/3   Aug 27 02:07 00:01     2392157 (77.89.203.46)")
	if !ok {
		t.Fatal("row not read")
	}
	if sess.TTY != "pts/3" || sess.PID != 2392157 || sess.From != "77.89.203.46" {
		t.Fatalf("session = %+v", sess)
	}
	if sess.LoginTime == nil {
		t.Fatal("no login time")
	}
	if got := sess.LoginTime.Format("01-02 15:04"); got != "08-27 02:07" {
		t.Errorf("loginTime = %s", got)
	}
	if sess.Idle != "00:01" {
		t.Errorf("idle = %q", sess.Idle)
	}
}

// The C form carries no year, so the current one is assumed — and rolled back
// when that puts the login in the future, which is what a session from
// December looks like in January.
func TestWhoTimeRollsBackAFutureLogin(t *testing.T) {
	now := time.Date(2026, time.January, 5, 12, 0, 0, 0, time.UTC)
	ts, ok := whoTime([]string{"root", "pts/0", "Dec", "28", "09:12"}, 2, 2, now)
	if !ok {
		t.Fatal("not parsed")
	}
	if ts.Year() != 2025 || ts.Month() != time.December {
		t.Fatalf("ts = %s, want December 2025", ts)
	}
}

func TestWhoTimeKeepsAnOrdinaryDate(t *testing.T) {
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	ts, ok := whoTime([]string{"root", "pts/0", "Aug", "26", "09:12"}, 2, 2, now)
	if !ok {
		t.Fatal("not parsed")
	}
	if ts.Year() != 2026 || ts.Month() != time.August || ts.Day() != 26 {
		t.Fatalf("ts = %s", ts)
	}
}
