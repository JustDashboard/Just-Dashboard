package logsx

import (
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func service(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	return New([]string{dir}), dir
}

func write(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeGz(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := gzip.NewWriter(f)
	if _, err := zw.Write([]byte(strings.Join(lines, "\n") + "\n")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

// A level filter used to hide every line the parser could not classify, which
// on a typical log is most of them — so turning on "error" hid the stack trace
// under the error. The unknown chip is what makes those reachable, and it has
// to be reachable *by name*, not by leaving the filter off.
func TestUnknownLevelIsSelectable(t *testing.T) {
	f, err := NewFilter(Filter{Levels: []string{LevelUnknown}})
	if err != nil {
		t.Fatal(err)
	}
	if !f.MatchLevel("") {
		t.Error("an unclassified line must match the unknown chip")
	}
	if f.MatchLevel("error") {
		t.Error("the unknown chip must not admit classified lines")
	}

	errorsOnly, err := NewFilter(Filter{Levels: []string{"error"}})
	if err != nil {
		t.Fatal(err)
	}
	if errorsOnly.MatchLevel("") {
		t.Error("an unclassified line must not appear under an error filter")
	}
}

func TestExcludeHidesMatchingLines(t *testing.T) {
	f, err := NewFilter(Filter{Query: "GET", Exclude: "/healthz", IgnoreCase: true})
	if err != nil {
		t.Fatal(err)
	}
	if !f.MatchText("GET /orders 200") {
		t.Error("a line matching the query and not the exclusion must be kept")
	}
	if f.MatchText("GET /healthz 200") {
		t.Error("the exclusion must win over the query")
	}
}

func TestBadRegexIsReportedOnce(t *testing.T) {
	if _, err := NewFilter(Filter{Query: "([", Regex: true}); err == nil {
		t.Fatal("an unparseable expression must be refused when the filter is built")
	}
}

// The browser cannot re-run a Go regular expression, so the ranges it paints
// come from here. Getting them wrong is worse than not highlighting at all.
func TestHighlightsAreByteRanges(t *testing.T) {
	f, _ := NewFilter(Filter{Query: "err", IgnoreCase: true})
	got := f.Highlights("an ERR and an err")
	want := [][2]int{{3, 6}, {14, 17}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("highlights = %v, want %v", got, want)
	}
}

func TestSearchKeepsContextAroundMatches(t *testing.T) {
	svc, dir := service(t)
	path := filepath.Join(dir, "app.log")
	write(t, path, "one", "two", "boom", "four", "five", "six")

	res, err := svc.Search(context.Background(), path, SearchOptions{
		Filter: Filter{Query: "boom"}, Before: 1, After: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Matched != 1 {
		t.Fatalf("matched = %d, want 1", res.Matched)
	}
	var texts []string
	for _, l := range res.Lines {
		texts = append(texts, l.Text)
	}
	if got := strings.Join(texts, ","); got != "two,boom,four,five" {
		t.Errorf("context window = %q, want \"two,boom,four,five\"", got)
	}
	for _, l := range res.Lines {
		if (l.Text == "boom") == l.Context {
			t.Errorf("line %q: context flag is backwards", l.Text)
		}
	}
}

// Two matches close together must not produce the same line twice — once as
// the trailing context of the first and once as the leading context of the
// second.
func TestOverlappingContextIsNotDuplicated(t *testing.T) {
	svc, dir := service(t)
	path := filepath.Join(dir, "app.log")
	write(t, path, "a", "boom", "b", "boom", "c")

	res, err := svc.Search(context.Background(), path, SearchOptions{
		Filter: Filter{Query: "boom"}, Before: 2, After: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int]bool{}
	for _, l := range res.Lines {
		if seen[l.No] {
			t.Fatalf("line %d rendered twice", l.No)
		}
		seen[l.No] = true
	}
	if len(res.Lines) != 5 {
		t.Errorf("lines = %d, want 5", len(res.Lines))
	}
}

// The whole point of reading archives is answering "when did this start", and
// that answer is a lie if the files are concatenated in the wrong order. The
// two rotation schemes count in opposite directions, so the order comes from
// the modification times rather than from the names.
func TestArchivesAreOrderedOldestFirst(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "syslog")
	write(t, live, "now")
	for i, name := range []string{"syslog.3.gz", "syslog.2.gz", "syslog.1"} {
		p := filepath.Join(dir, name)
		if strings.HasSuffix(name, ".gz") {
			writeGz(t, p, "old")
		} else {
			write(t, p, "old")
		}
		if err := os.Chtimes(p, time.Now(), time.Now().Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	// A file that is not a rotation of this one must not be swept in.
	write(t, filepath.Join(dir, "syslog-other.log"), "unrelated")

	got := Archives(live)
	want := []string{"syslog.3.gz", "syslog.2.gz", "syslog.1"}
	if len(got) != len(want) {
		t.Fatalf("archives = %v, want %v", got, want)
	}
	for i := range want {
		if filepath.Base(got[i]) != want[i] {
			t.Errorf("archive %d = %s, want %s", i, filepath.Base(got[i]), want[i])
		}
	}
}

func TestSearchReadsCompressedArchives(t *testing.T) {
	svc, dir := service(t)
	live := filepath.Join(dir, "app.log")
	write(t, live, "today: fine")
	writeGz(t, filepath.Join(dir, "app.log.1.gz"), "yesterday: boom")
	if err := os.Chtimes(filepath.Join(dir, "app.log.1.gz"), time.Now(), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	res, err := svc.Search(context.Background(), live, SearchOptions{
		Filter: Filter{Query: "boom"}, Archives: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Matched != 1 {
		t.Fatalf("matched = %d across the rotated set, want 1", res.Matched)
	}
	if len(res.Files) != 2 {
		t.Fatalf("files = %d, want the archive and the live file", len(res.Files))
	}
	if !res.Files[0].Archive || res.Files[0].Matched != 1 {
		t.Errorf("the archive should be first and hold the match: %+v", res.Files[0])
	}
}

// A generation is read only when it is inside the roots itself. Anyone who can
// write beside a log can name a link app.log.1, and the live file passing the
// roots is no reason to follow it out of them — in a search or an export.
func TestArchivesLinkedOutOfTheRootsAreNotRead(t *testing.T) {
	svc, dir := service(t)
	secret := filepath.Join(t.TempDir(), "shadow")
	write(t, secret, "root:$6$SECRETHASH:19000")
	live := filepath.Join(dir, "app.log")
	write(t, live, "today: fine")
	if err := os.Symlink(secret, live+".1"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(secret, time.Now(), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	res, err := svc.Search(context.Background(), live, SearchOptions{Archives: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 1 || res.Files[0].Archive {
		t.Errorf("files = %+v, want the live file alone", res.Files)
	}
	var out strings.Builder
	if _, err := svc.Range(context.Background(), live, SearchOptions{Archives: true}, &out); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{out.String(), texts(res.Lines)} {
		if strings.Contains(text, "SECRETHASH") {
			t.Fatalf("read through a generation linked out of the roots: %q", text)
		}
	}
}

// A filtered tail cannot start n lines from the end: on a log where one line in
// a thousand is an error, that window is empty and the page looks broken.
func TestFilteredTailOpensWithMatches(t *testing.T) {
	svc, dir := service(t)
	path := filepath.Join(dir, "app.log")
	lines := make([]string, 0, 2000)
	for i := range 2000 {
		if i%500 == 0 {
			lines = append(lines, fmt.Sprintf("line %d error boom", i))
			continue
		}
		lines = append(lines, fmt.Sprintf("line %d ok", i))
	}
	write(t, path, lines...)

	f, _ := NewFilter(Filter{Query: "boom"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, pre, err := svc.TailLines(ctx, path, 100, f)
	if err != nil {
		t.Fatal(err)
	}
	if pre.Lines != 4 {
		t.Fatalf("prefill = %d matches, want 4", pre.Lines)
	}
	if !pre.Complete {
		t.Error("a small file should be scanned to its start")
	}
	for range 4 {
		select {
		case l := <-ch:
			if !strings.Contains(l.Text, "boom") {
				t.Fatalf("unfiltered line reached the tail: %q", l.Text)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for the prefilled matches")
		}
	}
}

// The follow has to resume exactly where the prefill scan stopped: a byte
// earlier duplicates the last line, a byte later loses it.
func TestFilteredTailDoesNotRepeatThePrefill(t *testing.T) {
	svc, dir := service(t)
	path := filepath.Join(dir, "app.log")
	write(t, path, "boom one")

	f, _ := NewFilter(Filter{Query: "boom"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, _, err := svc.TailLines(ctx, path, 100, f)
	if err != nil {
		t.Fatal(err)
	}
	if first := <-ch; first.Text != "boom one" {
		t.Fatalf("prefill = %q", first.Text)
	}

	// A follow that resumed even a byte early replays the prefilled line
	// immediately, with nothing appended.
	select {
	case l := <-ch:
		t.Fatalf("the follow replayed a line before anything was written: %q", l.Text)
	case <-time.After(300 * time.Millisecond):
	}

	// And a line written now arrives. It is appended on a retry loop because
	// the file watcher underneath is a process-wide singleton that drops an
	// event when a test binary churns through tails — a browser holding one
	// open for minutes never meets that, but `go test -count=20` does.
	appended := 0
	tick := time.NewTicker(400 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(20 * time.Second)
	for {
		select {
		case l := <-ch:
			if !strings.HasPrefix(l.Text, "boom appended") {
				t.Fatalf("second line = %q", l.Text)
			}
			return
		case <-tick.C:
			appended++
			file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(file, "boom appended %d\n", appended)
			file.Close()
		case <-deadline:
			t.Fatal("timed out waiting for an appended line")
		}
	}
}

// The level scanner replaced a regular expression that was most of the cost of
// a history search. It has to agree with it, including on the two cases a
// prefix match gets wrong.
func TestLevelDetection(t *testing.T) {
	cases := map[string]string{
		"kernel: [12345] WARNING: something": "warn",
		"2024-06-12 ERROR upstream refused":  "error",
		"nothing to see here":                "",
		"errors are not a level":             "",
		"criticality is not a level either":  "",
		"level=debug msg=\"connected\"":      "debug",
		"CRIT: disk failing":                 "critical",
		"an err and an error":                "error",
		"php_error_log rotated":              "",
		"Fehler beim Starten":                "",
	}
	for text, want := range cases {
		if got := detectLevel(text); got != want {
			t.Errorf("detectLevel(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestContainsFoldMatchesWithoutAllocating(t *testing.T) {
	if !containsFold("GET /Orders HTTP/1.1", "orders") {
		t.Error("a case-insensitive match must be found")
	}
	if containsFold("GET /orders", "ordering") {
		t.Error("a needle longer than what is there must not match")
	}
	if got := testing.AllocsPerRun(100, func() {
		containsFold("2024-06-12 some reasonably long log line about an order", "order")
	}); got != 0 {
		t.Errorf("containsFold allocated %v times per call", got)
	}
}

// Go counts bytes and the browser counts UTF-16 units. A line with one
// accented character before the match had every highlight shifted left, which
// paints the wrong run of text.
func TestHighlightsAreInBrowserOffsets(t *testing.T) {
	f, _ := NewFilter(Filter{Query: "boom", IgnoreCase: true})
	const text = "café BOOM"
	got := f.Highlights(text)
	if len(got) != 1 {
		t.Fatalf("highlights = %v", got)
	}
	// "café " is five characters and six bytes.
	if got[0] != [2]int{5, 9} {
		t.Errorf("range = %v, want [5 9] in UTF-16 units", got[0])
	}
	if [...]string{text[6:10]}[0] == "BOOM" && got[0][0] == 6 {
		t.Error("the range is still in byte offsets")
	}
}

func BenchmarkParseLine(b *testing.B) {
	const line = "2026-08-28T23:03:24.804642+00:00 vps-07749119 sshd-session[78923]: Failed password for invalid user admin from 1.2.3.4 port 40404 ssh2"
	for b.Loop() {
		ParseLine(line, "auth.log")
	}
}

func TestHistogramSnapsToRoundBuckets(t *testing.T) {
	svc, dir := service(t)
	path := filepath.Join(dir, "app.log")
	lines := []string{}
	// The lines carry no zone, and a stamp with no zone is the host's local
	// time — so the expected first stamp is built in time.Local too. Built in
	// UTC, this only passed on a host whose zone is UTC.
	base := time.Date(2024, 6, 12, 10, 0, 0, 0, time.Local)
	for i := range 120 {
		lines = append(lines, base.Add(time.Duration(i)*time.Minute).Format("2006-01-02 15:04:05")+" error boom")
	}
	write(t, path, lines...)

	res, err := svc.Search(context.Background(), path, SearchOptions{Filter: Filter{Query: "boom"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.BucketSeconds != 300 {
		t.Errorf("bucket width = %ds for a two-hour span, want 300", res.BucketSeconds)
	}
	total := 0
	for _, b := range res.Histogram {
		total += b.Total
		if b.Counts["error"] != b.Total {
			t.Errorf("bucket at %s counted %d error lines out of %d", b.Start, b.Counts["error"], b.Total)
		}
	}
	if total != 120 {
		t.Errorf("histogram total = %d, want 120", total)
	}
	if res.First == nil || !res.First.Equal(base) {
		t.Errorf("first = %v, want %v", res.First, base)
	}
}

// The export used to ignore the filter entirely and hand back the whole file,
// so narrowing the view and pressing Export produced two different logs.
func TestRangeAppliesTheSameFilter(t *testing.T) {
	svc, dir := service(t)
	path := filepath.Join(dir, "app.log")
	write(t, path, "keep boom", "drop me", "keep boom again")

	var out strings.Builder
	n, err := svc.Range(context.Background(), path, SearchOptions{Filter: Filter{Query: "boom"}}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("wrote %d lines, want 2", n)
	}
	if strings.Contains(out.String(), "drop me") {
		t.Error("the export kept a line the filter rejects")
	}
}

// The file nobody has a rotation rule for is the one that fills the disk, and
// it is precisely the entry a rule list cannot show.
func TestRetentionNamesTheUnmanagedFile(t *testing.T) {
	st := &RotateStatus{Available: true, Rules: []RotateRule{{
		Patterns: []string{"/var/log/nginx/*.log"}, Frequency: "daily", Rotate: "14", Compress: true,
	}}}

	managed := MatchRetention(st, "/var/log/nginx/access.log", 100<<20)
	if !managed.Managed || managed.Level != "ok" {
		t.Fatalf("a file inside a glob must be reported as managed: %+v", managed)
	}
	if !strings.Contains(managed.Summary, "14 kept") || !strings.Contains(managed.Summary, "compressed") {
		t.Errorf("summary = %q, want the frequency, retention and compression", managed.Summary)
	}

	orphan := MatchRetention(st, "/var/log/myapp/huge.log", 900<<20)
	if orphan.Managed || orphan.Level != "warn" {
		t.Errorf("a large unmanaged file must warn: %+v", orphan)
	}

	none := MatchRetention(&RotateStatus{}, "/var/log/anything.log", 1)
	if none.Level != "unknown" {
		t.Errorf("without logrotate the verdict is unknown, not a pass: %+v", none)
	}
}

// A logrotate rule that exists and has not run in weeks is the failure this
// verdict is for: the configuration reads correctly and the disk fills anyway.
func TestRetentionCatchesAStoppedTimer(t *testing.T) {
	stale := time.Now().Add(-40 * 24 * time.Hour)
	st := &RotateStatus{
		Available: true,
		LastRun:   &stale,
		Rules:     []RotateRule{{Patterns: []string{"/var/log/syslog"}, Frequency: "daily", Rotate: "7"}},
	}
	got := MatchRetention(st, "/var/log/syslog", 1)
	if got.Level != "warn" || !strings.Contains(got.Summary, "timer has stopped") {
		t.Errorf("a stale logrotate run must be reported: %+v", got)
	}
}

func TestLevelFromPriorityMatchesTheTextScale(t *testing.T) {
	for priority, want := range map[int]string{0: "critical", 2: "critical", 3: "error", 4: "warn", 6: "info", 7: "debug"} {
		if got := LevelFromPriority(priority); got != want {
			t.Errorf("priority %d = %q, want %q", priority, got, want)
		}
	}
}

// Reads are confined to the configured roots. The log viewer must never become
// a way to read /etc/shadow.
func TestAllowRefusesOutsideTheRoots(t *testing.T) {
	svc, dir := service(t)
	if err := svc.Allow(filepath.Join(dir, "app.log")); err != nil {
		t.Errorf("a path inside a root must be allowed: %v", err)
	}
	if err := svc.Allow("/etc/shadow"); err == nil {
		t.Error("a path outside every root must be refused")
	}
	if _, err := svc.Search(context.Background(), "/etc/shadow", SearchOptions{}); err == nil {
		t.Error("search must apply the same containment")
	}
	var out strings.Builder
	if _, err := svc.Range(context.Background(), "/etc/shadow", SearchOptions{}, &out); err == nil {
		t.Error("export must apply the same containment")
	}
}

// Three timestamp shapes, and the one that used to be read an hour wrong.
func TestTimestampParsing(t *testing.T) {
	iso, ok := parseTimestamp("2026-08-28T23:03:24.804642+02:00 sshd[1]: Failed password")
	if !ok {
		t.Fatal("an RFC3339 line with fractional seconds and an offset must parse")
	}
	// Slicing a fixed 25 characters chopped the zone off and read the rest as
	// UTC, filing every line on a non-UTC host under the wrong hour.
	if iso.Hour() != 21 {
		t.Errorf("hour = %d, want 21 — the +02:00 offset was dropped", iso.Hour())
	}

	nginx, ok := parseTimestamp(`1.2.3.4 - - [12/Jun/2026:10:00:00 +0000] "GET / HTTP/1.1" 200`)
	if !ok || nginx.Hour() != 10 || nginx.Day() != 12 {
		t.Errorf("nginx access line = %v, %v", nginx, ok)
	}

	// A pid in brackets is not a timestamp, and must not cost a parse attempt
	// or produce one.
	if _, ok := parseBracketed("Aug 28 23:03:24 host sshd[78923]: Failed password"); ok {
		t.Error("a bracketed pid was read as a timestamp")
	}

	syslog, ok := parseTimestamp("Aug 28 23:03:24 host sshd[1]: hello")
	if !ok || syslog.Month() != time.August || syslog.Year() == 0 {
		t.Errorf("syslog line = %v, %v — the year has to be filled in", syslog, ok)
	}

	if _, ok := parseTimestamp("no timestamp at all here"); ok {
		t.Error("a line with no timestamp must say so rather than inventing one")
	}
}

// Debian ships rsyslog's stanza with its six paths one per line before the
// brace. Reading only the brace line reported syslog, auth.log and kern.log as
// governed by nothing — a false "this grows until the disk is full" on the
// three logs an operator reads most.
func TestLogrotatePatternsMaySpanLines(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "rsyslog")
	write(t, conf,
		"# managed by the distribution",
		"/var/log/syslog",
		"/var/log/kern.log",
		"/var/log/auth.log",
		"{",
		"\trotate 4",
		"\tweekly",
		"\tmissingok",
		"\tcompress",
		"\tpostrotate",
		"\t\t/usr/lib/rsyslog/rsyslog-rotate",
		"\tendscript",
		"}",
	)

	rules := parseLogrotateFile(conf)
	if len(rules) != 1 {
		t.Fatalf("rules = %d, want 1", len(rules))
	}
	if len(rules[0].Patterns) != 3 {
		t.Fatalf("patterns = %v, want all three paths", rules[0].Patterns)
	}
	if rules[0].Frequency != "weekly" || rules[0].Rotate != "4" || !rules[0].Compress {
		t.Errorf("rule = %+v", rules[0])
	}
	st := &RotateStatus{Available: true, Rules: rules}
	for _, path := range []string{"/var/log/syslog", "/var/log/kern.log", "/var/log/auth.log"} {
		if got := MatchRetention(st, path, 1<<30); !got.Managed {
			t.Errorf("%s reported as unmanaged: %s", path, got.Summary)
		}
	}
	if got := MatchRetention(st, "/var/log/myapp.log", 1<<30); got.Managed {
		t.Error("a path the stanza does not name must not be claimed by it")
	}
}

// The global directives at the top of logrotate.conf sit at the same
// indentation as a stanza's paths and must not be mistaken for them.
func TestLogrotateGlobalDirectivesAreNotPatterns(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "logrotate.conf")
	write(t, conf,
		"weekly",
		"su root syslog",
		"rotate 4",
		"create",
		"include /etc/logrotate.d",
		"",
		"/var/log/wtmp {",
		"    missingok",
		"    monthly",
		"    rotate 1",
		"}",
	)
	rules := parseLogrotateFile(conf)
	if len(rules) != 1 || len(rules[0].Patterns) != 1 || rules[0].Patterns[0] != "/var/log/wtmp" {
		t.Fatalf("rules = %+v", rules)
	}
}
