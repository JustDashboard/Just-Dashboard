package logsx

import (
	"maps"
	"strings"
	"testing"
	"time"
)

// dbWant is what one line must read as through a database lens. Every part
// is compared, so a line that gains an attr or loses its stamp fails as
// surely as one that loses its event.
type dbWant struct {
	level string
	// at is "" for no stamp, "2006-01-02 15:04:05.000Z" for an instant in
	// UTC, and the same without the Z for a naive stamp in time.Local —
	// which is how the lenses must read one, whatever zone runs the test.
	at    string
	event string
	attrs map[string]string
	cont  bool
}

// dbCase is lines read in order through one reader, and what each becomes.
type dbCase struct {
	name  string
	lines []string
	want  []dbWant
}

// dbRun reads each case through the lens and checks every line, then that
// every event the lens declares was produced by at least one of them: an
// event no fixture reaches is a rule nobody has seen work.
func dbRun(t *testing.T, id string, cases []dbCase) {
	t.Helper()
	seen := map[string]bool{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if len(c.lines) != len(c.want) {
				t.Fatalf("%d lines, %d wants", len(c.lines), len(c.want))
			}
			got := readThrough(t, id, c.lines...)
			for i, l := range got {
				seen[l.Event] = true
				dbCheckLine(t, i, l, c.want[i])
			}
		})
	}
	lens, _ := LensByID(id)
	for _, event := range lens.Events {
		if !seen[event] {
			t.Errorf("no fixture line reads as %q", event)
		}
	}
}

func dbCheckLine(t *testing.T, i int, l Line, w dbWant) {
	t.Helper()
	name := l.Text
	if len(name) > 60 {
		name = name[:60] + "…"
	}
	if l.Level != w.level {
		t.Errorf("line %d %q: level %q, want %q", i, name, l.Level, w.level)
	}
	if l.Event != w.event {
		t.Errorf("line %d %q: event %q, want %q", i, name, l.Event, w.event)
	}
	if l.Cont != w.cont {
		t.Errorf("line %d %q: cont %v, want %v", i, name, l.Cont, w.cont)
	}
	want := w.attrs
	if want == nil {
		want = map[string]string{}
	}
	got := l.Attrs
	if got == nil {
		got = map[string]string{}
	}
	if !maps.Equal(got, want) {
		t.Errorf("line %d %q:\n attrs %v\n want  %v", i, name, got, want)
	}
	switch {
	case w.at == "" && l.Timestamp != nil:
		t.Errorf("line %d %q: stamped %v, want none", i, name, l.Timestamp)
	case w.at != "":
		at := dbWantTime(t, w.at)
		if l.Timestamp == nil || !l.Timestamp.Equal(at) {
			t.Errorf("line %d %q: stamped %v, want %v", i, name, l.Timestamp, at)
		}
	}
}

func dbWantTime(t *testing.T, s string) time.Time {
	t.Helper()
	const layout = "2006-01-02 15:04:05.999999999"
	var (
		at  time.Time
		err error
	)
	if naive, utc := strings.CutSuffix(s, "Z"); utc {
		at, err = time.ParseInLocation(layout, naive, time.UTC)
	} else {
		at, err = time.ParseInLocation(layout, s, time.Local)
	}
	if err != nil {
		t.Fatalf("bad want stamp %q: %v", s, err)
	}
	return at
}

// dbBench reads the lines through one reader of the lens, over and over, and
// reports the cost per line. The lines are parsed once outside the loop:
// ParseLine has its own benchmark, and this one is the lens's share.
func dbBench(b *testing.B, id string, texts []string) {
	lens, err := LensByID(id)
	if err != nil || lens == nil {
		b.Fatalf("lens %q not registered: %v", id, err)
	}
	parsed := make([]Line, len(texts))
	for i, text := range texts {
		parsed[i] = ParseLine(text, "")
	}
	r := lens.New()
	var l Line
	b.ReportAllocs()
	for b.Loop() {
		for i := range parsed {
			l = parsed[i]
			r.Read(&l)
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(parsed)), "ns/line")
}
