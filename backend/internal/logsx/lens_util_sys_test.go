package logsx

import (
	"maps"
	"testing"
	"time"
)

// The fixtures in the system lens tests are real lines: this host's auth.log,
// kern.log, ufw.log and syslog, and the examples in the research behind the
// lenses. The host's name and every address are masked to documentation
// ranges; everything else is verbatim, including sshd's double space before
// an empty username and the trailing space ufw leaves after URGP=0.

// sysWant is what one line should read as, field for field. at is RFC 3339
// in UTC, or empty for a line with no timestamp.
type sysWant struct {
	text  string
	event string
	level string
	at    string
	attrs map[string]string
	cont  bool
	lens  string
}

func sysRead(t *testing.T, id string, want ...sysWant) {
	t.Helper()
	texts := make([]string, len(want))
	for i, w := range want {
		texts[i] = w.text
	}
	sysCheck(t, readThrough(t, id, texts...), want)
}

func sysCheck(t *testing.T, got []Line, want []sysWant) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("read %d lines, want %d", len(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		if g.Event != w.event {
			t.Errorf("line %d %q: event %q, want %q", i, w.text, g.Event, w.event)
		}
		if g.Level != w.level {
			t.Errorf("line %d %q: level %q, want %q", i, w.text, g.Level, w.level)
		}
		at := ""
		if g.Timestamp != nil {
			at = g.Timestamp.UTC().Format(time.RFC3339Nano)
		}
		if at != w.at {
			t.Errorf("line %d %q: timestamp %q, want %q", i, w.text, at, w.at)
		}
		if len(g.Attrs) != 0 || len(w.attrs) != 0 {
			if !maps.Equal(g.Attrs, w.attrs) {
				t.Errorf("line %d %q: attrs\n got %v\nwant %v", i, w.text, g.Attrs, w.attrs)
			}
		}
		if g.Cont != w.cont {
			t.Errorf("line %d %q: cont %v, want %v", i, w.text, g.Cont, w.cont)
		}
		if g.Lens != w.lens {
			t.Errorf("line %d %q: lens %q, want %q", i, w.text, g.Lens, w.lens)
		}
	}
}

// sysEntry is a journal entry as the engine hands it to a lens: MESSAGE
// parsed on its own, PRIORITY as the level, the kept fields in Attrs.
type sysEntry struct {
	message  string
	priority int
	fields   map[string]string
}

func sysReadJournal(t *testing.T, id string, entries ...sysEntry) []Line {
	t.Helper()
	lens, err := LensByID(id)
	if err != nil || lens == nil {
		t.Fatalf("lens %q not registered: %v", id, err)
	}
	r := lens.New()
	out := make([]Line, 0, len(entries))
	for _, e := range entries {
		l := ParseLine(e.message, "")
		if l.levelFrom != levelFromStructured {
			l.Level, l.levelFrom = LevelFromPriority(e.priority), levelFromPriority
		}
		given := make([]string, 0, len(e.fields))
		for k, v := range e.fields {
			l.SetAttr(k, v)
			given = append(given, k)
		}
		r.Read(&l)
		checkDeclared(t, id, &l, given...)
		out = append(out, l)
	}
	return out
}

// sysInZone runs a test with the host's zone set to one that is not UTC, so
// a stamp read as UTC instead of local time is caught on a UTC test machine.
func sysInZone(t *testing.T) {
	t.Helper()
	saved := time.Local
	time.Local = time.FixedZone("EET", 3*60*60)
	t.Cleanup(func() { time.Local = saved })
}

// sysBSDWant is where "Sep  7 03:12:01" lands: in local time, in the most
// recent year that does not put it in the future.
func sysBSDWant(month time.Month, day, hour, minute, second int) string {
	now := time.Now()
	at := time.Date(now.Year(), month, day, hour, minute, second, 0, time.Local)
	if at.After(now.Add(24 * time.Hour)) {
		at = time.Date(now.Year()-1, month, day, hour, minute, second, 0, time.Local)
	}
	return at.UTC().Format(time.RFC3339Nano)
}

func TestSysPrefixReadsBothSyslogStamps(t *testing.T) {
	cases := []struct {
		text, program, pid, msg, bsd string
		ok                           bool
	}{
		{"2026-09-27T00:21:06.443346+00:00 web-1 sshd-session[3110301]: Invalid user ekala from 203.0.113.42 port 30358",
			"sshd-session", "3110301", "Invalid user ekala from 203.0.113.42 port 30358", "", true},
		{"2026-09-20T03:16:40.973808+00:00 web-1 sudo:   ubuntu : PWD=/home/ubuntu/Just-Dashboard ; USER=root ; COMMAND=/usr/bin/ss -tlnp",
			"sudo", "", "  ubuntu : PWD=/home/ubuntu/Just-Dashboard ; USER=root ; COMMAND=/usr/bin/ss -tlnp", "", true},
		{"Sep  7 03:12:01 web-1 CRON[3113693]: (root) CMD (command -v debian-sa1 > /dev/null && debian-sa1 1 1)",
			"CRON", "3113693", "(root) CMD (command -v debian-sa1 > /dev/null && debian-sa1 1 1)", "Sep  7 03:12:01", true},
		{"Sep 17 03:12:01 web-1 kernel: [UFW BLOCK] IN=ens3", "kernel", "", "[UFW BLOCK] IN=ens3", "Sep 17 03:12:01", true},
		// fail2ban's own file: a date, but not syslog's shape.
		{"2023-02-17 23:44:17,037 fail2ban.actions        [992]: NOTICE  [apache-auth] Ban 203.0.113.228", "", "", "", "", false},
		// A stamp and a host, then prose rather than a tag.
		{"Sep  7 03:12:01 web-1 -- MARK --", "", "", "", "", false},
		{"Starting nordvpnd.socket - NordVPN Daemon Socket...", "", "", "", "", false},
		{"2026-09-27T00:21:06.443346+00:00 web-1 sshd-session[31a]: x", "", "", "", "", false},
	}
	for _, c := range cases {
		program, pid, msg, bsd, ok := sysPrefix(c.text)
		if ok != c.ok || program != c.program || pid != c.pid || msg != c.msg || bsd != c.bsd {
			t.Errorf("sysPrefix(%q) = %q %q %q %q %v, want %q %q %q %q %v",
				c.text, program, pid, msg, bsd, ok, c.program, c.pid, c.msg, c.bsd, c.ok)
		}
	}
}

func TestSysBSDStampIsLocalAndNeverInTheFuture(t *testing.T) {
	sysInZone(t)
	now := time.Date(2027, time.January, 3, 12, 0, 0, 0, time.Local)
	cases := []struct {
		stamp string
		want  time.Time
	}{
		{"Jan  2 23:59:58", time.Date(2027, time.January, 2, 23, 59, 58, 0, time.Local)},
		// December read in January is last year's.
		{"Dec 31 23:59:58", time.Date(2026, time.December, 31, 23, 59, 58, 0, time.Local)},
		// 2027 has no 29 February; the last year that had one is 2024.
		{"Feb 29 10:00:00", time.Date(2024, time.February, 29, 10, 0, 0, 0, time.Local)},
	}
	for _, c := range cases {
		got, ok := sysBSDStamp(c.stamp, now)
		if !ok || !got.Equal(c.want) || got.Location() != time.Local {
			t.Errorf("sysBSDStamp(%q) = %v %v, want %v", c.stamp, got, ok, c.want)
		}
	}
	for _, bad := range []string{"Foo  2 23:59:58", "Jan 32 10:00:00", "Jan  2 25:00:00", "Jan  2 1a:00:00"} {
		if _, ok := sysBSDStamp(bad, now); ok {
			t.Errorf("sysBSDStamp(%q) read a time", bad)
		}
	}
}

func TestSysUnrepeatAndUptime(t *testing.T) {
	if got := sysUnrepeat("message repeated 2 times: [ Failed password for root from 203.0.113.53 port 9046 ssh2]"); got !=
		"Failed password for root from 203.0.113.53 port 9046 ssh2" {
		t.Errorf("sysUnrepeat = %q", got)
	}
	if got := sysStripUptime("[ 1234.567890] ens3: Link is Down"); got != "ens3: Link is Down" {
		t.Errorf("sysStripUptime = %q", got)
	}
	if got := sysStripUptime("[UFW BLOCK] IN=ens3"); got != "[UFW BLOCK] IN=ens3" {
		t.Errorf("sysStripUptime took a UFW prefix: %q", got)
	}
}

// benchmarkSysLens reads a mix of real lines through one reader, one line
// per iteration. ParseLine runs outside the timer: what is measured is what
// the lens adds to every line of a search.
func benchmarkSysLens(b *testing.B, id string, texts []string) {
	lens, err := LensByID(id)
	if err != nil || lens == nil {
		b.Fatalf("lens %q not registered: %v", id, err)
	}
	parsed := make([]Line, len(texts))
	for i, text := range texts {
		parsed[i] = ParseLine(text, "")
	}
	r := lens.New()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l := parsed[i%len(parsed)]
		r.Read(&l)
	}
}
