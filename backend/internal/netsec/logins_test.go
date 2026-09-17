package netsec

import (
	"testing"
	"time"
)

// Real `last -F -w` output from a util-linux host. The columns are padded
// rather than delimited and the "from" column is empty for a console login,
// which is what makes a fixed field count the wrong way to read this.
const lastOutput = `root     pts/1        203.0.113.9      Wed Aug 20 03:15:22 2026 - Wed Aug 20 04:02:11 2026  (00:46)
deploy   pts/0        10.8.0.4         Wed Aug 20 02:59:00 2026   still logged in
root     tty1                          Tue Aug 19 22:04:03 2026 - down                      (01:55)
ubuntu   pts/3        198.51.100.7     Tue Aug 19 20:00:00 2026 - crash                     (02:04)
reboot   system boot  6.1.0-18-amd64   Tue Aug 19 10:00:01 2026   still running

wtmp begins Tue Feb 17 02:02:53 2026
`

func TestParseLastReadsEveryShapeOfRecord(t *testing.T) {
	got := parseLast(lastOutput, 100)
	if len(got) != 5 {
		t.Fatalf("parsed %d records, want 5:\n%+v", len(got), got)
	}

	closed := got[0]
	if closed.User != "root" || closed.TTY != "pts/1" || closed.From != "203.0.113.9" {
		t.Errorf("closed session parsed as %+v", closed)
	}
	if closed.LoginTime == nil || closed.LoginTime.Format("2006-01-02 15:04:05") != "2026-08-20 03:15:22" {
		t.Errorf("login time = %v", closed.LoginTime)
	}
	if closed.EndTime == nil || closed.EndTime.Format("15:04:05") != "04:02:11" {
		t.Errorf("end time = %v", closed.EndTime)
	}
	if closed.Duration != "00:46" || closed.Active {
		t.Errorf("duration = %q active = %v", closed.Duration, closed.Active)
	}

	open := got[1]
	if !open.Active || open.EndTime != nil {
		t.Errorf("a session still logged in parsed as %+v", open)
	}

	// A console login has no "from" at all; reading the columns positionally
	// shifts the timestamp into it.
	console := got[2]
	if console.TTY != "tty1" || console.From != "" {
		t.Errorf("console login parsed as %+v", console)
	}
	if console.Ended != "down" {
		t.Errorf("ended = %q, want down", console.Ended)
	}

	if got[3].Ended != "crash" {
		t.Errorf("ended = %q, want crash", got[3].Ended)
	}

	boot := got[4]
	if boot.Kind != "boot" || boot.TTY != "" {
		t.Errorf("reboot parsed as %+v", boot)
	}
	if boot.From != "6.1.0-18-amd64" {
		t.Errorf("reboot from = %q, want the kernel version", boot.From)
	}
}

// A busybox `last` refuses -F, and its output carries no year. It still has to
// parse: the alternative is an empty table on those hosts.
func TestParseLastWithoutTheYear(t *testing.T) {
	got := parseLast("root     pts/0        10.0.0.2         Wed Aug 20 03:15 - 04:02  (00:46)\n", 10)
	if len(got) != 1 {
		t.Fatalf("parsed %d records, want 1", len(got))
	}
	if got[0].LoginTime == nil {
		t.Fatal("no login time")
	}
	if got[0].LoginTime.Year() != time.Now().Year() {
		t.Errorf("year = %d, want the current one", got[0].LoginTime.Year())
	}
	if got[0].LoginTime.Format("01-02 15:04") != "08-20 03:15" {
		t.Errorf("login time = %v", got[0].LoginTime)
	}
}

// Headers, footers and anything else without a timestamp are skipped rather
// than guessed at — a half-parsed row of a security log is worse than no row.
func TestParseLastSkipsWhatItCannotRead(t *testing.T) {
	got := parseLast("wtmp begins Tue Feb 17 02:02:53 2026\n\nnonsense\nroot\n", 10)
	if len(got) != 0 {
		t.Fatalf("invented %d records from junk: %+v", len(got), got)
	}
}

func TestParseLastHonoursTheLimit(t *testing.T) {
	if got := parseLast(lastOutput, 2); len(got) != 2 {
		t.Fatalf("returned %d records for a limit of 2", len(got))
	}
}

// The posture check counted failed logins as the length of a 500-record
// listing of the whole of btmp. Two things were wrong with that and they
// pointed in opposite directions: the "sustained attempts" threshold of 2000
// could never be reached by a number that stopped at 500, and the 200-attempt
// notice fired forever on any host whose btmp had ever accumulated that many —
// which is every host with a public SSH port, regardless of this week.
func TestCountWithinBoundsTheWindow(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }
	records := []LoginRecord{
		{LoginTime: at(time.Hour)},
		{LoginTime: at(48 * time.Hour)},
		{LoginTime: at(30 * 24 * time.Hour)},
		{LoginTime: nil}, // unparsed: not evidence of anything
	}
	vol := countWithin(records, 7*24*time.Hour, now)
	if vol.Count != 2 {
		t.Fatalf("counted %d, want the two inside the week", vol.Count)
	}
	if vol.Capped {
		t.Error("a short listing is not a capped one")
	}
	if vol.Window != 7*24*time.Hour {
		t.Errorf("window %v", vol.Window)
	}
}

// Running out of sample inside the window makes the count a floor, and a floor
// quoted as a total is the kind of number people go on to reason from.
func TestCountWithinReportsACappedSample(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-time.Hour)
	records := make([]LoginRecord, failedLoginSample)
	for i := range records {
		at := recent
		records[i] = LoginRecord{LoginTime: &at}
	}
	vol := countWithin(records, 7*24*time.Hour, now)
	if !vol.Capped || vol.Count != failedLoginSample {
		t.Fatalf("count=%d capped=%v", vol.Count, vol.Capped)
	}
	// A full sample that reaches back past the window is not capped: the
	// window closed before the sample did.
	old := now.Add(-30 * 24 * time.Hour)
	records[len(records)-1] = LoginRecord{LoginTime: &old}
	if vol := countWithin(records, 7*24*time.Hour, now); vol.Capped {
		t.Error("reported a cap where the window ran out first")
	}
}

// The listing answers what was tried; the summary answers who is trying, and
// the arithmetic is what a test can pin: folded by address, the account names
// most tried first, the console attempt counted but not listed, and a sample
// that ran out inside the window reported as a floor.
func TestSummariseFailedLoginsFoldsByAddress(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	at := func(hoursAgo int) *time.Time {
		v := now.Add(-time.Duration(hoursAgo) * time.Hour)
		return &v
	}
	records := []LoginRecord{
		{User: "root", From: "203.0.113.9", LoginTime: at(1)},
		{User: "admin", From: "203.0.113.9", LoginTime: at(2)},
		{User: "root", From: "203.0.113.9", LoginTime: at(3)},
		{User: "deploy", From: "198.51.100.4", LoginTime: at(4)},
		{User: "ubuntu", From: "", LoginTime: at(5)},
		{User: "root", From: "192.0.2.77", LoginTime: at(24 * 9)},
		{User: "root", From: "192.0.2.77", LoginTime: nil},
	}
	sum := SummariseFailedLogins(records, 7*24*time.Hour, now, 10)
	if sum.Attempts != 5 {
		t.Fatalf("attempts = %d, want 5 (the console attempt counts, the stale and unreadable ones do not)", sum.Attempts)
	}
	if sum.Addresses != 2 || len(sum.Attackers) != 2 {
		t.Fatalf("addresses = %d, attackers = %+v", sum.Addresses, sum.Attackers)
	}
	first := sum.Attackers[0]
	if first.Address != "203.0.113.9" || first.Attempts != 3 {
		t.Errorf("most persistent first: got %+v", first)
	}
	if len(first.Users) != 2 || first.Users[0] != "root" || first.Users[1] != "admin" {
		t.Errorf("users most tried first: got %v", first.Users)
	}
	if !first.First.Equal(*at(3)) || !first.Last.Equal(*at(1)) {
		t.Errorf("first/last = %v/%v", first.First, first.Last)
	}
	if sum.Since == nil || !sum.Since.Equal(*at(5)) {
		t.Errorf("since = %v, want the oldest record inside the window", sum.Since)
	}
	if sum.Capped {
		t.Error("seven records is not a capped sample")
	}
	if sum.WindowHours != 168 {
		t.Errorf("windowHours = %d", sum.WindowHours)
	}

	// Two attackers at the same count: the more recent one first.
	tie := SummariseFailedLogins([]LoginRecord{
		{User: "root", From: "10.0.0.1", LoginTime: at(6)},
		{User: "root", From: "10.0.0.2", LoginTime: at(1)},
	}, 24*time.Hour, now, 10)
	if tie.Attackers[0].Address != "10.0.0.2" {
		t.Errorf("tie broken by recency: got %v", tie.Attackers)
	}

	// topN trims the list, not the totals.
	trimmed := SummariseFailedLogins(records, 7*24*time.Hour, now, 1)
	if len(trimmed.Attackers) != 1 || trimmed.Addresses != 2 || trimmed.Attempts != 5 {
		t.Errorf("topN must trim the list only: %+v", trimmed)
	}

	// A full sample with every record inside the window is a floor.
	full := make([]LoginRecord, failedLoginSample)
	for i := range full {
		full[i] = LoginRecord{User: "root", From: "203.0.113.9", LoginTime: at(1)}
	}
	if !SummariseFailedLogins(full, 24*time.Hour, now, 10).Capped {
		t.Error("a sample that ran out inside the window must be reported as capped")
	}
}
