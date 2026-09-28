package logsx

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withLens registers a lens for the length of one test or benchmark. It is
// registered here rather than from an init, so the golden vocabulary the
// frontend is checked against never contains it.
func withLens(tb testing.TB, l *Lens) {
	tb.Helper()
	if _, dup := lenses[l.ID]; dup {
		tb.Fatalf("lens %q is already registered", l.ID)
	}
	lenses[l.ID] = l
	tb.Cleanup(func() { delete(lenses, l.ID) })
}

// recordLens reads just enough of Postgres's stderr format to exercise the
// engine: an ERROR is a head with an event and its own level, and a DETAIL,
// a STATEMENT or a tab-indented line continues the record above it.
var recordLens = &Lens{
	ID: "test-records",
	New: func() Reader {
		return ReaderFunc(func(l *Line) {
			body := l.Text
			if i := strings.Index(body, "] "); i >= 0 {
				body = body[i+2:]
			}
			switch {
			case strings.HasPrefix(l.Text, "\t"), strings.HasPrefix(body, "DETAIL:"),
				strings.HasPrefix(body, "STATEMENT:"), strings.HasPrefix(body, "CONTEXT:"):
				l.Cont = true
			case strings.HasPrefix(body, "ERROR:"):
				l.SetLevel("error")
				l.Event = "error"
				if strings.Contains(body, "deadlock detected") {
					l.Event = "deadlock"
				}
			case strings.HasPrefix(body, "LOG:"):
				l.SetLevel("info")
				if strings.Contains(body, "checkpoint") {
					l.Event = "checkpoint"
				}
			}
			if i := strings.Index(l.Text, " ["); i >= 0 {
				if j := strings.IndexByte(l.Text[i:], ']'); j > 2 {
					l.SetAttr("pid", l.Text[i+2:i+j])
				}
			}
		})
	},
}

// Real lines from a Postgres 17 host (research-db-logs.md §1): a deadlock
// report whose DETAIL names the processes, followed by an unrelated error.
var deadlockLog = []string{
	"2026-09-27 10:05:00.000 UTC [27] LOG:  checkpoint starting: time",
	"2026-09-27 10:06:00.500 UTC [9788] ERROR:  deadlock detected",
	"2026-09-27 10:06:00.500 UTC [9788] DETAIL:  Process 9788 waits for ShareLock on transaction 1035; blocked by process 91.",
	"\tProcess 91 waits for ShareLock on transaction 1045; blocked by process 98.",
	"\tProcess 98: INSERT INTO x ...",
	"2026-09-27 10:06:00.500 UTC [9788] STATEMENT:  UPDATE accounts SET balance = balance - 1 WHERE id = 1",
	"2026-09-27 10:06:01.001 UTC [812] ERROR:  relation \"userz\" does not exist at character 15",
	"2026-09-27 10:06:01.001 UTC [812] STATEMENT:  select * from userz",
	"2026-09-27 10:07:00.000 UTC [27] LOG:  checkpoint complete: wrote 42 buffers (0.3%)",
}

func texts(lines []Line) string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.Text
		if l.Cont {
			out[i] = "+" + out[i]
		}
	}
	return strings.Join(out, "\n")
}

func TestPredicateVectors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "predicates.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Name string `json:"name"`
		Line struct {
			Event  string            `json:"event"`
			Stream string            `json:"stream"`
			Source string            `json:"source"`
			Attrs  map[string]string `json:"attrs"`
			Fields map[string]string `json:"fields"`
		} `json:"line"`
		F     []string `json:"f"`
		Match bool     `json:"match"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors {
		f, err := NewFilter(Filter{Fields: v.F})
		if err != nil {
			t.Errorf("%s: %v", v.Name, err)
			continue
		}
		l := Line{Event: v.Line.Event, Stream: v.Line.Stream, Source: v.Line.Source, Attrs: v.Line.Attrs, Fields: v.Line.Fields}
		if got := f.MatchFields(&l); got != v.Match {
			t.Errorf("%s: %v on %+v = %v, want %v", v.Name, v.F, v.Line, got, v.Match)
		}
	}
}

// A predicate the grammar cannot read is refused when the filter is built, so
// the route answers 400 with a sentence rather than filtering on a guess.
func TestPredicateGrammarRefusals(t *testing.T) {
	for _, raw := range []string{
		"user",                          // no value
		":postgres",                     // no key
		"us er:postgres",                // not a key
		"level:error",                   // levels have their own chips
		"status:>=five",                 // not a number
		"status:>NaN",                   // not a finite number
		"path:*admin",                   // * is only presence; the escape is =*admin
		"user:",                         // nothing to compare
		"user:!",                        // nothing to compare
		"path:~",                        // nothing to compare
		"k:" + strings.Repeat("x", 257), // too long
		strings.Repeat("k", 65) + ":v",  // key too long
	} {
		if _, err := NewFilter(Filter{Fields: []string{raw}}); err == nil {
			t.Errorf("%q was accepted", raw)
		}
	}
	many := make([]string, 33)
	for i := range many {
		many[i] = fmt.Sprintf("k%d:v", i)
	}
	if _, err := NewFilter(Filter{Fields: many}); err == nil {
		t.Error("33 predicates were accepted")
	}
	if _, err := NewFilter(Filter{Fields: []string{"path:=*admin", "client:2001:db8::1"}}); err != nil {
		t.Errorf("the escape and an IPv6 value must parse: %v", err)
	}
	if _, err := NewFilter(Filter{Lens: "no-such-lens"}); err == nil {
		t.Error("an unknown lens must be refused when the filter is built")
	}
}

// A predicate-only filter is a filter: the tail must take the filtered
// prefill, and the stream must say it is filtered.
func TestPredicatesMakeAFilterNonEmpty(t *testing.T) {
	f, err := NewFilter(Filter{Fields: []string{"event:slow"}})
	if err != nil {
		t.Fatal(err)
	}
	if f.Empty() {
		t.Error("a filter with a predicate reported itself empty")
	}
	lensOnly, _ := NewFilter(Filter{Lens: LensNone})
	if !lensOnly.Empty() {
		t.Error("a lens names lines; it does not filter them")
	}
}

func searchLines(t *testing.T, opts SearchOptions, lines ...string) *SearchResult {
	t.Helper()
	svc, dir := service(t)
	path := filepath.Join(dir, "postgresql.log")
	write(t, path, lines...)
	res, err := svc.Search(context.Background(), path, opts)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// The DETAIL of a deadlock is the part that names the processes, and it does
// not contain the word the operator searched for. It must arrive with its
// head, uncounted, drawn at the head's level.
func TestContinuationLinesFollowTheirHead(t *testing.T) {
	withLens(t, recordLens)
	res := searchLines(t, SearchOptions{Filter: Filter{Query: "deadlock", Lens: recordLens.ID}}, deadlockLog...)
	if res.Matched != 1 {
		t.Fatalf("matched = %d, want the head only", res.Matched)
	}
	want := strings.Join([]string{
		deadlockLog[1], "+" + deadlockLog[2], "+" + deadlockLog[3], "+" + deadlockLog[4], "+" + deadlockLog[5],
	}, "\n")
	if got := texts(res.Lines); got != want {
		t.Fatalf("lines:\n%s\nwant:\n%s", got, want)
	}
	for _, l := range res.Lines[1:] {
		if l.Level != "error" {
			t.Errorf("continuation %q drawn at %q, want its head's level", l.Text, l.Level)
		}
		if l.Event != "" || len(l.Match) > 0 {
			t.Errorf("continuation %q carries an event or highlights: %q %v", l.Text, l.Event, l.Match)
		}
	}
	total := 0
	for _, b := range res.Histogram {
		total += b.Total
	}
	if total != 1 {
		t.Errorf("histogram counted %d, want the head alone", total)
	}
}

func TestContinuationLinesFollowALevelOrEventFilter(t *testing.T) {
	withLens(t, recordLens)
	errorsOnly := searchLines(t, SearchOptions{Filter: Filter{Levels: []string{"error"}, Lens: recordLens.ID}}, deadlockLog...)
	if errorsOnly.Matched != 2 || len(errorsOnly.Lines) != 7 {
		t.Fatalf("levels=error: matched %d, lines:\n%s", errorsOnly.Matched, texts(errorsOnly.Lines))
	}
	byEvent := searchLines(t, SearchOptions{Filter: Filter{Fields: []string{"event:deadlock"}, Lens: recordLens.ID}}, deadlockLog...)
	if byEvent.Matched != 1 || len(byEvent.Lines) != 5 {
		t.Fatalf("event:deadlock: matched %d, lines:\n%s", byEvent.Matched, texts(byEvent.Lines))
	}
	// Without the lens nothing is a continuation, and the search is the one
	// it always was.
	plain := searchLines(t, SearchOptions{Filter: Filter{Query: "deadlock"}}, deadlockLog...)
	if plain.Matched != 1 || len(plain.Lines) != 1 {
		t.Fatalf("no lens: matched %d, lines:\n%s", plain.Matched, texts(plain.Lines))
	}
}

// A line at the top of a file that continues a record nobody saw stands on
// its own, and the gate does not bridge files: an archive's last record does
// not adopt the live file's first line.
func TestOrphanContinuationStandsAlone(t *testing.T) {
	withLens(t, recordLens)
	svc, dir := service(t)
	live := filepath.Join(dir, "postgresql.log")
	write(t, live, "\tProcess 91 waits for ShareLock", deadlockLog[0])
	archive := live + ".1"
	write(t, archive, deadlockLog[1])
	if err := os.Chtimes(archive, time.Now(), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	res, err := svc.Search(context.Background(), live, SearchOptions{
		Filter: Filter{Query: "Process 91", Lens: recordLens.ID}, Archives: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Matched != 1 || len(res.Lines) != 1 || res.Lines[0].File != "postgresql.log" {
		t.Fatalf("matched %d, lines %+v", res.Matched, res.Lines)
	}
	kept := searchLines(t, SearchOptions{Filter: Filter{Query: "deadlock detected", Lens: recordLens.ID}},
		deadlockLog[1])
	if kept.Matched != 1 {
		t.Fatalf("head alone: matched %d", kept.Matched)
	}
}

// A lens carries values from one line to the next, so it has to read every
// line — the ones a query rejects too. Otherwise the same line reads one way
// with a query and another without, and one with context lines a third way:
// an Upgrade line that never saw its Start-Date has no stamp, and a line with
// no stamp is inside every window.
func TestALensReadsTheLinesAQueryRejects(t *testing.T) {
	appLensZone(t)
	history := []string{
		"Start-Date: 2024-06-10  10:00:00",
		"Commandline: apt-get upgrade -y",
		"Requested-By: ubuntu (1000)",
		"Upgrade: nginx:amd64 (1.24.0-1, 1.24.0-2)",
		"End-Date: 2024-06-10  10:00:30",
		"",
		"Start-Date: 2024-06-12  09:00:00",
		"Commandline: apt install curl",
		"Requested-By: ubuntu (1000)",
		"Install: curl:amd64 (8.5.0-2)",
		"End-Date: 2024-06-12  09:00:05",
	}
	since := time.Date(2024, 6, 11, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name  string
		opts  SearchOptions
		match int
	}{
		{"the old transaction by field", SearchOptions{Filter: Filter{Fields: []string{"package:nginx"}}, Since: since}, 0},
		{"the old transaction by text", SearchOptions{Filter: Filter{Query: "nginx"}, Since: since}, 0},
		{"the old transaction with context", SearchOptions{Filter: Filter{Query: "nginx"}, Since: since, Before: 1}, 0},
		{"the new transaction by text", SearchOptions{Filter: Filter{Query: "curl"}, Since: since}, 2},
		{"the new transaction with context", SearchOptions{Filter: Filter{Query: "curl"}, Since: since, Before: 1}, 2},
	} {
		c.opts.Filter.Lens = "packages"
		res := searchLines(t, c.opts, history...)
		if res.Matched != c.match {
			t.Errorf("%s: matched %d, want %d:\n%s", c.name, res.Matched, c.match, texts(res.Lines))
		}
		for _, l := range res.Lines {
			if l.Context {
				continue
			}
			if l.Timestamp == nil || !l.Timestamp.Equal(time.Date(2024, 6, 12, 6, 0, 0, 0, time.UTC)) {
				t.Errorf("%s: %q stamped %v, want its transaction's start", c.name, l.Text, l.Timestamp)
			}
			if l.Attrs["command"] != "apt install curl" {
				t.Errorf("%s: %q has attrs %v, want its transaction's command", c.name, l.Text, l.Attrs)
			}
		}
	}

	// The address is on "connection received"; the FATAL that ends the
	// session does not repeat it.
	session := []string{
		"2026-09-27 10:02:00.000 UTC [4242] LOG:  connection received: host=203.0.113.9 port=40022",
		`2026-09-27 10:02:00.010 UTC [4242] FATAL:  password authentication failed for user "admin"`,
	}
	fields := []string{"event:auth_failed", "client:203.0.113.9"}
	for _, c := range []struct {
		name string
		opts SearchOptions
	}{
		{"no query", SearchOptions{}},
		{"a query", SearchOptions{Filter: Filter{Query: "password"}}},
		{"a query with context", SearchOptions{Filter: Filter{Query: "password"}, Before: 1}},
	} {
		c.opts.Filter.Fields, c.opts.Filter.Lens = fields, "postgres"
		if res := searchLines(t, c.opts, session...); res.Matched != 1 {
			t.Errorf("postgres, %s: matched %d, want 1", c.name, res.Matched)
		}
	}
}

func TestExportKeepsContinuationLines(t *testing.T) {
	withLens(t, recordLens)
	svc, dir := service(t)
	path := filepath.Join(dir, "postgresql.log")
	write(t, path, deadlockLog...)
	var out strings.Builder
	n, err := svc.Range(context.Background(), path, SearchOptions{Filter: Filter{Query: "userz", Lens: recordLens.ID}}, &out)
	if err != nil {
		t.Fatal(err)
	}
	// "userz" appears on both lines of the record, so this also checks the
	// STATEMENT is written once.
	if n != 2 || !strings.Contains(out.String(), "STATEMENT:  select * from userz") {
		t.Fatalf("wrote %d:\n%s", n, out.String())
	}
	out.Reset()
	if n, _ := svc.Range(context.Background(), path, SearchOptions{Filter: Filter{Fields: []string{"event:deadlock"}, Lens: recordLens.ID}}, &out); n != 5 {
		t.Fatalf("event:deadlock exported %d lines:\n%s", n, out.String())
	}
}

// The follow keeps the record together across the prefill and the live part:
// a DETAIL written after the socket opened still follows its ERROR.
func TestFollowKeepsRecordsTogether(t *testing.T) {
	withLens(t, recordLens)
	svc, dir := service(t)
	path := filepath.Join(dir, "postgresql.log")
	write(t, path, deadlockLog...)
	f, err := NewFilter(Filter{Query: "deadlock", Lens: recordLens.ID})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, pre, err := svc.TailLines(ctx, path, 100, f)
	if err != nil {
		t.Fatal(err)
	}
	if pre.Lines != 1 {
		t.Errorf("prefill counted %d matches, want 1", pre.Lines)
	}
	got := []Line{}
	for len(got) < 5 {
		select {
		case l := <-ch:
			got = append(got, l)
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out after:\n%s", texts(got))
		}
	}
	if !got[1].Cont || !got[4].Cont || got[4].Level != "error" {
		t.Fatalf("prefill:\n%s", texts(got))
	}

	// Appended on a retry loop for the reason TestFilteredTailDoesNotRepeatThePrefill gives.
	tick := time.NewTicker(400 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(20 * time.Second)
	appended := 0
	for {
		select {
		case l := <-ch:
			if strings.Contains(l.Text, "deadlock detected") {
				next := <-ch
				if !next.Cont || !strings.Contains(next.Text, "DETAIL") {
					t.Fatalf("the appended DETAIL did not follow its head: %+v", next)
				}
				return
			}
			if !l.Cont {
				t.Fatalf("an unmatched head reached the tail: %q", l.Text)
			}
		case <-tick.C:
			appended++
			file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(file, "%s\n%s\n", deadlockLog[1], deadlockLog[2])
			file.Close()
		case <-deadline:
			t.Fatal("timed out waiting for an appended record")
		}
	}
}

func jsonLine(at time.Time, level, msg string, kv ...string) string {
	m := map[string]string{"time": at.UTC().Format(time.RFC3339Nano), "level": level, "msg": msg}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// Facets count every match in the window, not the lines returned: Insights
// asks with limit=1.
func TestFacetsCountEveryMatch(t *testing.T) {
	base := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	lines := []string{}
	for i := range 100 {
		user, level := "alice", "info"
		if i%4 == 0 {
			user, level = "bob", "error"
		}
		client := fmt.Sprintf("203.0.113.%d", i%7)
		lines = append(lines, jsonLine(base.Add(time.Duration(i)*time.Minute), level, fmt.Sprintf("request %d took %dms", i, i+1),
			"user", user, "client", client, "duration_ms", fmt.Sprint(i+1)))
	}
	lines = append(lines, jsonLine(base.Add(200*time.Minute), "info", "no user here"))
	res := searchLines(t, SearchOptions{
		Limit: 1, Facets: []string{"user", "level", "pattern"}, Measure: "duration_ms",
		Sample: []string{"client"},
	}, lines...)
	if len(res.Lines) != 1 {
		t.Fatalf("lines = %d, want the limit", len(res.Lines))
	}
	users := res.Facets["user"]
	if users == nil || len(users.Values) != 2 || users.Missing != 1 || users.Distinct != 2 {
		t.Fatalf("user facet = %+v", users)
	}
	alice, bob := users.Values[0], users.Values[1]
	if alice.Value != "alice" || alice.Count != 75 || alice.Errors != 0 {
		t.Errorf("alice = %+v", alice)
	}
	if bob.Value != "bob" || bob.Count != 25 || bob.Errors != 25 {
		t.Errorf("bob = %+v", bob)
	}
	// bob's durations are 1, 5, …, 97: sum 1225, max 97.
	if bob.Sum == nil || *bob.Sum != 1225 || bob.Max == nil || *bob.Max != 97 {
		t.Errorf("bob sum/max = %v/%v", bob.Sum, bob.Max)
	}
	if bob.First == nil || !bob.First.Equal(base) || bob.Last == nil || !bob.Last.Equal(base.Add(96*time.Minute)) {
		t.Errorf("bob first/last = %v/%v", bob.First, bob.Last)
	}
	if bob.Samples["client"] != "203.0.113.5" {
		t.Errorf("bob's last client = %q, want the last one seen", bob.Samples["client"])
	}
	levels := res.Facets["level"]
	if levels.Values[0].Value != "info" || levels.Values[0].Count != 76 || levels.Missing != 0 {
		t.Errorf("level facet = %+v", levels)
	}
	patterns := res.Facets["pattern"]
	if len(patterns.Values) != 2 || patterns.Values[0].Value != "request <*> took <*>" || patterns.Values[0].Count != 100 {
		t.Errorf("pattern facet = %+v", patterns.Values)
	}
	m := res.Measure
	if m == nil || m.Count != 100 || m.P50 != 50 || m.P90 != 90 || m.P99 != 99 || m.Max != 100 || m.Mean != 50.5 {
		t.Errorf("measure = %+v", m)
	}
}

func TestFacetLimitFoldsTheRestIntoOther(t *testing.T) {
	lines := []string{}
	for i := range 30 {
		for range i + 1 {
			lines = append(lines, fmt.Sprintf(`{"msg":"x","path":"/p%02d"}`, i))
		}
	}
	res := searchLines(t, SearchOptions{Limit: 1, Facets: []string{"path"}, FacetLimit: 3}, lines...)
	f := res.Facets["path"]
	if len(f.Values) != 3 || f.Values[0].Value != "/p29" || f.Values[2].Value != "/p27" || f.Distinct != 30 {
		t.Fatalf("facet = %+v", f)
	}
	if want := len(lines) - 30 - 29 - 28; f.Other != want {
		t.Errorf("other = %d, want %d", f.Other, want)
	}
	if f.Values[0].Sum != nil {
		t.Error("sum is only reported when a measure was asked for")
	}
}

// Past the budget, new values fold into Other and the facet says it was
// capped; the values already counted keep counting.
func TestFacetBudgets(t *testing.T) {
	lines := make([]string, 0, facetDistinctBudget+11)
	for i := range facetDistinctBudget + 10 {
		lines = append(lines, fmt.Sprintf(`{"msg":"x","id":"%d"}`, i))
	}
	lines = append(lines, `{"msg":"x","id":"0"}`)
	res := searchLines(t, SearchOptions{Limit: 1, Facets: []string{"id"}, FacetLimit: 1}, lines...)
	f := res.Facets["id"]
	if !f.DistinctCapped || f.Distinct != facetDistinctBudget {
		t.Fatalf("distinct = %d capped %v", f.Distinct, f.DistinctCapped)
	}
	if f.Values[0].Value != "0" || f.Values[0].Count != 2 {
		t.Errorf("a value counted before the cap stops counting: %+v", f.Values[0])
	}
	if f.Other != len(lines)-2 {
		t.Errorf("other = %d, want %d", f.Other, len(lines)-2)
	}

	long := strings.Repeat("q", 400)
	lines = lines[:0]
	for i := range 11000 {
		lines = append(lines, fmt.Sprintf(`{"msg":"x","query":"%s%d"}`, long, i))
	}
	res = searchLines(t, SearchOptions{Limit: 1, Facets: []string{"query"}}, lines...)
	if f := res.Facets["query"]; !f.DistinctCapped || f.Distinct >= 11000 || f.Distinct*400 > facetBytesBudget {
		t.Errorf("the byte budget did not hold: distinct %d capped %v", f.Distinct, f.DistinctCapped)
	}
}

func TestHistogramByKey(t *testing.T) {
	withLens(t, recordLens)
	withoutEvent := append(append([]string(nil), deadlockLog...), "2026-09-27 10:08:00.000 UTC [27] WARNING:  something unnamed")
	res := searchLines(t, SearchOptions{Filter: Filter{Lens: recordLens.ID}, HistogramBy: "event"}, withoutEvent...)
	counts, total := map[string]int{}, 0
	for _, b := range res.Histogram {
		total += b.Total
		for k, n := range b.Counts {
			counts[k] += n
		}
	}
	// Four heads carry an event; the fifth head has none and counts only in
	// the total, and the continuation lines are not matches at all.
	if total != 5 || counts["checkpoint"] != 2 || counts["deadlock"] != 1 || counts["error"] != 1 || len(counts) != 3 {
		t.Fatalf("total %d, counts %v", total, counts)
	}
	if res.HistogramBy != "event" {
		t.Errorf("histogramBy = %q", res.HistogramBy)
	}

	pinned := searchLines(t, SearchOptions{Filter: Filter{Lens: recordLens.ID}, HistogramBy: "event", HistogramValues: []string{"deadlock"}}, withoutEvent...)
	counts = map[string]int{}
	for _, b := range pinned.Histogram {
		for k, n := range b.Counts {
			counts[k] += n
		}
	}
	if counts["deadlock"] != 1 || counts["*"] != 3 || len(counts) != 2 {
		t.Fatalf("pinned counts %v", counts)
	}
}

func TestHistogramByKeepsTheTopEight(t *testing.T) {
	base := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	lines := []string{}
	for i := range 12 {
		for k := range 12 - i {
			lines = append(lines, jsonLine(base.Add(time.Duration(k)*time.Second), "info", "x", "status", fmt.Sprint(200+i)))
		}
	}
	res := searchLines(t, SearchOptions{HistogramBy: "status"}, lines...)
	counts := map[string]int{}
	for _, b := range res.Histogram {
		for k, n := range b.Counts {
			counts[k] += n
		}
	}
	if len(counts) != 9 || counts["200"] != 12 || counts["207"] != 5 || counts["*"] != 4+3+2+1 {
		t.Fatalf("counts %v", counts)
	}
}

func TestSearchOptionsValidate(t *testing.T) {
	good := SearchOptions{Facets: []string{"user", "level", "pattern"}, FacetLimit: 50, Measure: "duration_ms",
		Sample: []string{"query"}, HistogramBy: "event", HistogramValues: []string{"slow"}}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid options refused: %v", err)
	}
	for name, o := range map[string]SearchOptions{
		"too many facets":        {Facets: strings.Split("a,b,c,d,e,f,g,h,i,j,k,l,m", ",")},
		"bad facet":              {Facets: []string{"a b"}},
		"limit too high":         {Facets: []string{"a"}, FacetLimit: 51},
		"bad measure":            {Measure: "a:b"},
		"sample without facet":   {Sample: []string{"a"}},
		"too many samples":       {Facets: []string{"a"}, Sample: []string{"a", "b", "c", "d"}},
		"values without a key":   {HistogramValues: []string{"a"}},
		"bad regex":              {Filter: Filter{Query: "([", Regex: true}},
		"bad predicate":          {Filter: Filter{Fields: []string{"nokey"}}},
		"too many series values": {HistogramBy: "event", HistogramValues: strings.Split("a,b,c,d,e,f,g,h,i,j,k,l,m", ",")},
	} {
		if err := o.Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestPattern(t *testing.T) {
	cases := map[string]string{
		"connection from 10.0.0.4 port 51202":             "connection from <*> port <*>",
		`relation "userz" does not exist at character 15`: "relation <*> does not exist at character <*>",
		"can't connect to 'db' as user42 over sha256":     "can't connect to <*> as user42 over sha256",
		"id 550e8400-e29b-41d4-a716-446655440000 done":    "id <*> done",
		"request req_8f3a2b at 2026-09-27T10:00:00Z":      "request <*> at <*>",
		"fe80::1 said hello":                              "<*> said hello",
	}
	for in, want := range cases {
		if got := patternOf(in); got != want {
			t.Errorf("patternOf(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Pattern(&Line{Text: `{"msg":"x 1"}`, Message: "took 12ms"}); got != "took <*>" {
		t.Errorf("a structured line's pattern reads its message: %q", got)
	}
	if got := patternOf(strings.Repeat("word ", 400)); len(got) > patternCap+len(wildcard) {
		t.Errorf("pattern is %d bytes", len(got))
	}
}

func TestFieldPredicateOnPattern(t *testing.T) {
	res := searchLines(t, SearchOptions{Filter: Filter{Fields: []string{"pattern:=connection from <*> port <*>"}}},
		"connection from 10.0.0.4 port 51202", "connection from 10.0.0.5 port 1", "disconnected")
	if res.Matched != 2 {
		t.Fatalf("matched %d", res.Matched)
	}
}

// The strict logfmt rule: dockerd's lines are structure; a UFW line and a
// systemd exit line, which happen to contain key=value pairs, are prose.
func TestLogfmtIsStrict(t *testing.T) {
	dockerd := ParseLine(`time="2026-09-27T10:00:00.123456789Z" level=warning msg="Health check for container 3f2a error: context deadline exceeded" container=3f2a`, "dockerd")
	if dockerd.Level != "warn" || dockerd.Message != "Health check for container 3f2a error: context deadline exceeded" {
		t.Fatalf("dockerd = %q %q", dockerd.Level, dockerd.Message)
	}
	if dockerd.Fields["container"] != "3f2a" || dockerd.Timestamp == nil || dockerd.Timestamp.Nanosecond() != 123456789 {
		t.Errorf("dockerd fields %v at %v", dockerd.Fields, dockerd.Timestamp)
	}
	if !dockerd.HasOwnLevel() {
		t.Error("a logfmt level is the line's own")
	}
	heroku := ParseLine(`at=error code=H12 desc="Request timeout" method=GET path="/"`, "router")
	if heroku.Level != "error" || heroku.Fields["code"] != "H12" || heroku.Fields["desc"] != "Request timeout" {
		t.Errorf("heroku = %q %v", heroku.Level, heroku.Fields)
	}
	for _, text := range []string{
		"[UFW BLOCK] IN=ens3 OUT= MAC=fa:16:3e SRC=203.0.113.11 DST=198.51.100.87 LEN=40 PROTO=TCP SPT=54703 DPT=4448",
		"IN=ens3 OUT= SRC=203.0.113.11 DST=198.51.100.87 LEN=40 PROTO=TCP",
		"nordvpnd.service: Main process exited, code=exited, status=1/FAILURE",
		"method=GET path=/ status=200 duration=12ms",
		`level=info msg="unterminated`,
		`level=info and then prose`,
	} {
		if l := ParseLine(text, ""); l.Message != "" || l.Fields != nil {
			t.Errorf("%q was read as structure: %+v", text, l)
		}
	}
}

// The journal's priority is the level unless the message's own structure says
// otherwise; the free-text word scan never overrules it.
func TestApplyPriority(t *testing.T) {
	prose := ParseLine("error-reporting enabled for this run", "")
	prose.ApplyPriority(6)
	if prose.Level != "info" || prose.HasOwnLevel() {
		t.Errorf("prose = %q", prose.Level)
	}
	structured := ParseLine(`{"level":"error","msg":"boom"}`, "")
	structured.ApplyPriority(6)
	if structured.Level != "error" {
		t.Errorf("structured = %q", structured.Level)
	}
	lens := ParseLine("anything", "")
	lens.SetLevel("critical")
	lens.ApplyPriority(6)
	if lens.Level != "critical" {
		t.Errorf("a lens's level was overruled: %q", lens.Level)
	}
}

// Naive stamps are the host's local time; zoned ones are what they say.
func TestNaiveTimestampsAreLocal(t *testing.T) {
	loc := time.FixedZone("EET", 2*3600)
	saved := time.Local
	time.Local = loc
	defer func() { time.Local = saved }()
	for _, text := range []string{"2026-09-27 12:00:00 boot", "2026-09-27T12:00:00 boot", "2026-09-27T12:00:00.5 boot"} {
		got, ok := parseTimestamp(text)
		if !ok || got.Hour() != 10 {
			t.Errorf("%q = %v, want 10:00 UTC", text, got)
		}
	}
	if got, _ := parseTimestamp("2026-09-27T12:00:00+00:00 boot"); got.Hour() != 12 {
		t.Errorf("a zoned stamp moved: %v", got)
	}
	if got, ok := parseTimestamp("Sep 27 12:00:00 host sshd[1]: x"); !ok || got.Hour() != 10 {
		t.Errorf("syslog stamp = %v", got)
	}
}

// Routing hands one line to another lens and marks it, so the viewer can find
// its event's label; a lens this build does not have reads nothing.
func TestStreamRoutesLinesToAnotherLens(t *testing.T) {
	withLens(t, recordLens)
	withLens(t, &Lens{ID: "test-manager", New: func() Reader {
		return ReaderFunc(func(l *Line) {
			if strings.HasPrefix(l.Text, "Started ") {
				l.Event = "started"
			}
		})
	}})
	f, _ := NewFilter(Filter{})
	st := f.Stream(recordLens.ID)
	st.Route(func(l *Line) string {
		switch l.Source {
		case "systemd":
			return "test-manager"
		case "sudo":
			return "not-built"
		}
		return ""
	})
	read := func(text, source string) Line {
		l := ParseLine(text, source)
		st.Read(&l)
		return l
	}
	if l := read("Started postgresql.service.", "systemd"); l.Event != "started" || l.Lens != "test-manager" {
		t.Errorf("manager line = %q %q", l.Event, l.Lens)
	}
	if l := read(deadlockLog[1], "postgres"); l.Event != "deadlock" || l.Lens != "" {
		t.Errorf("own line = %q %q, want no lens mark on the stream's own lens", l.Event, l.Lens)
	}
	if l := read("ERROR: looks like a head", "sudo"); l.Event != "" {
		t.Errorf("a missing lens produced an event: %q", l.Event)
	}
	off, _ := NewFilter(Filter{Lens: LensNone})
	none := off.Stream(recordLens.ID)
	none.Route(func(*Line) string { return "test-manager" })
	l := ParseLine("Started x", "systemd")
	none.Read(&l)
	if l.Event != "" || none.Lens() != "" {
		t.Error("lens=none must read no lens, routed or not")
	}
}

// DetectLens answers only lenses this build registers, so every id here is
// also held to the registry: a detection table naming "postgresql" where the
// lens is "postgres" would read the source through nothing.
func TestDetectLens(t *testing.T) {
	files := map[string]string{
		"/var/log/postgresql/postgresql-17-main.log":                "postgres",
		"/var/log/mysql/error.log":                                  "mysql",
		"/var/log/mariadb/mariadb.log":                              "mysql",
		"/var/log/redis/redis-server.log":                           "redis",
		"/var/log/mongodb/mongod.log":                               "mongodb",
		"/var/log/clickhouse-server/clickhouse-server.err.log":      "clickhouse",
		"/var/log/nginx/access.log":                                 "http-access",
		"/var/log/nginx/shop.access.log":                            "http-access",
		"/var/log/nginx/error.log":                                  "nginx-error",
		"/var/log/apache2/other_vhosts_access.log":                  "http-access",
		"/var/log/httpd/error_log":                                  "nginx-error",
		"/var/log/caddy/access.log":                                 "http-access",
		"/var/log/caddy/caddy.log":                                  "caddy",
		"/var/log/auth.log":                                         "auth",
		"/var/log/secure":                                           "auth",
		"/var/log/ufw.log":                                          "firewall",
		"/var/log/kern.log":                                         "kernel",
		"/var/log/fail2ban.log":                                     "fail2ban",
		"/var/log/apt/history.log":                                  "packages",
		"/var/log/apt/term.log":                                     "packages",
		"/var/log/dpkg.log":                                         "packages",
		"/var/log/unattended-upgrades/unattended-upgrades-dpkg.log": "packages",
		"/var/log/dnf.rpm.log":                                      "packages",
		"/var/log/letsencrypt/letsencrypt.log":                      "certbot",
		"/var/log/syslog":                                           "syslog",
		"/var/log/messages":                                         "syslog",
		"/var/log/cron":                                             "cron",
		"/var/log/cron.log":                                         "cron",
		"/var/log/myapp/cronjobs.log":                               "app",
		"/var/log/myapp/secure-api.log":                             "app",
		"/var/log/cloud-init.log":                                   "app",
	}
	for path, want := range files {
		if got := DetectLens(LensTarget{Kind: KindSystem, Path: path}); got != want {
			t.Errorf("file %s = %q, want %q", path, got, want)
		}
	}
	images := map[string]string{
		"postgres:17":                                "postgres",
		"docker.io/library/postgres@sha256:abc":      "postgres",
		"postgis/postgis:16-3.4":                     "postgres",
		"pgvector/pgvector:pg16":                     "postgres",
		"timescale/timescaledb-ha:pg16":              "postgres",
		"supabase/postgres:15.1.0.147":               "postgres",
		"bitnami/postgresql:16":                      "postgres",
		"mysql:8.4":                                  "mysql",
		"mariadb:11":                                 "mysql",
		"percona/percona-server:8.0":                 "mysql",
		"redis:7-alpine":                             "redis",
		"valkey/valkey:8":                            "redis",
		"eqalpha/keydb":                              "redis",
		"mongo:7":                                    "mongodb",
		"clickhouse/clickhouse-server:24.8":          "clickhouse",
		"mcr.microsoft.com/mssql/server:2022-latest": "mssql",
		"mcr.microsoft.com/azure-sql-edge":           "mssql",
		"nginx:1.27":                                 "nginx",
		"openresty/openresty":                        "nginx",
		"jc21/nginx-proxy-manager:latest":            "nginx",
		"caddy:2":                                    "caddy",
		"localhost:5000/api:latest":                  "app",
		"ghcr.io/acme/shop-web:main":                 "app",
	}
	for image, want := range images {
		if got := DetectLens(LensTarget{Kind: KindDocker, Image: image}); got != want {
			t.Errorf("image %s = %q, want %q", image, got, want)
		}
	}
	units := map[string]string{
		"ssh.service":                 "auth",
		"sshd.service":                "auth",
		"fail2ban.service":            "fail2ban",
		"cron.service":                "cron",
		"crond.service":               "cron",
		"nginx.service":               "nginx-error",
		"caddy.service":               "caddy",
		"postgresql@17-main.service":  "postgres",
		"mariadb.service":             "mysql",
		"redis-server.service":        "redis",
		"mongod.service":              "mongodb",
		"clickhouse-server.service":   "clickhouse",
		"mssql-server.service":        "mssql",
		"certbot.service":             "certbot",
		"apt-daily-upgrade.service":   "packages",
		"unattended-upgrades.service": "packages",
		"api.service":                 "app",
	}
	for unit, want := range units {
		if got := DetectLens(LensTarget{Kind: KindJournal, Unit: unit}); got != want {
			t.Errorf("unit %s = %q, want %q", unit, got, want)
		}
	}
	for target, want := range map[LensTarget]string{
		{Kind: KindJournal}: "syslog",
		{Kind: KindKernel}:  "kernel",
		{Kind: KindPM2}:     "pm2",
		{Kind: KindStack}:   "",
		{Kind: KindJournalID, Unit: "sshd-session"}: "auth",
		{Kind: KindJournalID, Unit: "sudo"}:         "auth",
		{Kind: KindJournalID, Unit: "CRON"}:         "cron",
		{Kind: KindJournalID, Unit: "my-worker"}:    "app",
	} {
		if got := DetectLens(target); got != want {
			t.Errorf("%+v = %q, want %q", target, got, want)
		}
	}
}

// Before scanning a large live file for "the last hour", the search bisects to
// where the hour begins. The answer, the counts and the line numbers must be
// exactly those of a full scan.
func TestSinceBisectMatchesAFullScan(t *testing.T) {
	svc, dir := service(t)
	path := filepath.Join(dir, "big.log")
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	lines := make([]string, 0, 60000)
	for i := range 60000 {
		lines = append(lines, fmt.Sprintf("%s worker %d finished a job of some length", base.Add(time.Duration(i)*time.Minute).Format(time.RFC3339), i))
	}
	write(t, path, lines...)
	since := base.Add(59000 * time.Minute)

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	offset, before := sinceOffset(file, since, nil)
	file.Close()
	if offset == 0 || before == 0 || before > 59000 {
		t.Fatalf("offset %d, %d lines before it", offset, before)
	}

	res, err := svc.Search(context.Background(), path, SearchOptions{Since: since, Limit: 5000})
	if err != nil {
		t.Fatal(err)
	}
	if res.Matched != 1000 || res.Lines[0].No != 59001 || !strings.Contains(res.Lines[0].Text, "worker 59000 ") {
		t.Fatalf("matched %d, first %+v", res.Matched, res.Lines[0])
	}
	if res.Scanned >= 60000 {
		t.Errorf("scanned %d lines: the bisect did not skip anything", res.Scanned)
	}
	var out strings.Builder
	if n, _ := svc.Range(context.Background(), path, SearchOptions{Since: since}, &out); n != 1000 {
		t.Errorf("export wrote %d lines", n)
	}
}

func TestSinceBisectFallsBackOnDisorder(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	check := func(name string, line func(i int) string) {
		path := filepath.Join(dir, name)
		lines := make([]string, 0, 60000)
		for i := range 60000 {
			lines = append(lines, line(i))
		}
		write(t, path, lines...)
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if offset, _ := sinceOffset(file, base.Add(59000*time.Minute), nil); offset != 0 {
			t.Errorf("%s: bisected to %d", name, offset)
		}
	}
	check("backwards.log", func(i int) string {
		return fmt.Sprintf("%s worker %d finished a job of some length", base.Add(time.Duration(90000-i)*time.Minute).Format(time.RFC3339), i)
	})
	check("unstamped.log", func(i int) string {
		return fmt.Sprintf("worker %d finished a job of some length, with no stamp at all", i)
	})
}

func TestArchivesOlderThanTheWindowAreSkipped(t *testing.T) {
	svc, dir := service(t)
	live := filepath.Join(dir, "app.log")
	write(t, live, time.Now().UTC().Format(time.RFC3339)+" today")
	archive := live + ".1"
	write(t, archive, time.Now().Add(-48*time.Hour).UTC().Format(time.RFC3339)+" long ago")
	if err := os.Chtimes(archive, time.Now(), time.Now().Add(-47*time.Hour)); err != nil {
		t.Fatal(err)
	}
	res, err := svc.Search(context.Background(), live, SearchOptions{Archives: true, Since: time.Now().Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 1 || res.Files[0].Archive || res.Matched != 1 {
		t.Fatalf("files %+v matched %d", res.Files, res.Matched)
	}
}

// benchLines is roughly a million lines in the proportions of a real host:
// sshd's refusals, a Postgres log with multi-line records, and web traffic.
func benchLines(b *testing.B) string {
	b.Helper()
	path := filepath.Join(b.TempDir(), "bench.log")
	file, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	defer file.Close()
	base := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	for i := range 125_000 {
		at := base.Add(time.Duration(i) * 600 * time.Millisecond).Format("2006-01-02T15:04:05.000000-07:00")
		fmt.Fprintf(file, "%s vps-07749119 sshd-session[%d]: Failed password for invalid user admin from 203.0.113.%d port %d ssh2\n", at, 70000+i%900, i%250, 30000+i%30000)
		fmt.Fprintf(file, "%s vps-07749119 sshd-session[%d]: Invalid user ekala from 203.0.113.%d port %d\n", at, 70000+i%900, i%250, 30000+i%30000)
		fmt.Fprintf(file, "2026-09-27 10:06:01.001 UTC [%d] ERROR:  relation \"userz\" does not exist at character 15\n", 800+i%50)
		fmt.Fprintf(file, "2026-09-27 10:06:01.001 UTC [%d] STATEMENT:  select * from userz where id = %d\n", 800+i%50, i)
		fmt.Fprintf(file, "\tProcess 91 waits for ShareLock on transaction %d; blocked by process 98.\n", i)
		fmt.Fprintf(file, "2026-09-27 10:05:04.210 UTC [27] LOG:  checkpoint complete: wrote %d buffers (0.3%%)\n", i%400)
		fmt.Fprintf(file, "::1 - - [27/Nov/2024:06:21:42 +0000] \"GET /api/orders/%d HTTP/1.1\" %d 2 \"-\" \"curl/8.7.1\"\n", i, 200+(i%5)*100)
		fmt.Fprintf(file, "{\"level\":\"info\",\"msg\":\"request handled\",\"path\":\"/api/orders/%d\",\"duration_ms\":%d}\n", i, i%900)
	}
	return path
}

// BenchmarkSearchLens holds the lens's cost to the target: a search read
// through a lens should take at most twice the time of the same search with
// none. The lens here is a stand-in with the shape of a real one — prefix
// gates, an attr or two on recognised lines, continuation lines — so the
// engine's own overhead is what is measured.
func BenchmarkSearchLens(b *testing.B) {
	withLens(b, recordLens)
	path := benchLines(b)
	svc := New([]string{filepath.Dir(path)})
	for _, run := range []struct {
		name string
		opts SearchOptions
	}{
		{"none", SearchOptions{Limit: 1}},
		{"lens", SearchOptions{Limit: 1, Filter: Filter{Lens: recordLens.ID}}},
		{"lens+query", SearchOptions{Limit: 1, Filter: Filter{Lens: recordLens.ID, Query: "deadlock"}}},
		{"lens+facets", SearchOptions{Limit: 1, Filter: Filter{Lens: recordLens.ID}, Facets: []string{"event", "pid", "level"}}},
		// The shipped lenses over the same mixed lines, each recognising its
		// own share and passing over the rest, which is the cost a wrong
		// detection or a forced lens puts on a search.
		{"postgres", SearchOptions{Limit: 1, Filter: Filter{Lens: "postgres"}}},
		{"auth", SearchOptions{Limit: 1, Filter: Filter{Lens: "auth"}}},
		{"syslog", SearchOptions{Limit: 1, Filter: Filter{Lens: "syslog"}}},
		{"http-access", SearchOptions{Limit: 1, Filter: Filter{Lens: "http-access"}}},
		{"app", SearchOptions{Limit: 1, Filter: Filter{Lens: "app"}}},
	} {
		if lens, _ := LensByID(run.opts.Filter.Lens); lens == nil && run.opts.Filter.Lens != "" {
			continue
		}
		b.Run(run.name, func(b *testing.B) {
			for b.Loop() {
				if _, err := svc.Search(context.Background(), path, run.opts); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
