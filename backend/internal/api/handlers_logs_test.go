package api

import (
	"compress/gzip"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/logsx"
)

func dockerxLogLine(text, stream string) dockerx.LogLine {
	return dockerx.LogLine{Text: text, Stream: stream}
}

// These drive the log routes through the whole chain with a real signed-in
// admin. The unit tests in logsx cover what each rule decides; this covers what
// they cannot — whether the route is mounted, whether the filter the UI sends
// actually reaches the code that applies it, and whether a host with no Docker
// and no PM2 (which is every developer machine) still answers.

func logClient(t *testing.T) (*client, string) {
	t.Helper()
	s := testServer(t)
	cookie := signIn(t, s)
	return &client{t: t, h: s.Routes(), cookie: cookie}, s.Cfg.LogRoots[0]
}

func writeLog(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding %s: %v", rec.Body.String(), err)
	}
	return out
}

func TestLogSourcesListWhatCanActuallyBeOpened(t *testing.T) {
	c, root := logClient(t)
	writeLog(t, filepath.Join(root, "app.log"), "hello")

	index := decode[logSourceIndex](t, c.do("GET", "/api/v1/logs/sources", "", nil))

	var found *logsx.Source
	for i := range index.Sources {
		if strings.HasSuffix(index.Sources[i].Path, "app.log") {
			found = &index.Sources[i]
		}
	}
	if found == nil {
		t.Fatalf("app.log was not discovered: %+v", index.Sources)
	}
	if found.Size == 0 || found.Modified == nil {
		t.Errorf("a discovered file carries its size and mtime: %+v", found)
	}
	// A host's own /var/log is outside this install's roots, so offering it
	// would be a rail full of sources that refuse to open.
	for _, src := range index.Sources {
		if src.Path != "" && !strings.HasPrefix(src.Path, root) {
			t.Errorf("source %q sits outside the configured roots", src.Path)
		}
	}
	if index.Missing["pm2"] == "" {
		t.Error("an absent source kind must explain itself rather than simply not appearing")
	}
	if len(index.Roots) == 0 {
		t.Error("the roots are what the UI names when it has to explain an empty list")
	}
}

func TestLogSearchReturnsContextAndAHistogram(t *testing.T) {
	c, root := logClient(t)
	writeLog(t, filepath.Join(root, "app.log"),
		"2024-06-12 10:00:00 info starting",
		"2024-06-12 10:00:01 info ready",
		"2024-06-12 10:00:02 error boom",
		"2024-06-12 10:00:03 info recovered",
	)

	res := decode[logsx.SearchResult](t, c.do("GET",
		"/api/v1/logs/search?source="+filepath.Join(root, "app.log")+"&q=boom&before=1&after=1", "", nil))

	if res.Matched != 1 {
		t.Fatalf("matched = %d, want 1", res.Matched)
	}
	if len(res.Lines) != 3 {
		t.Fatalf("lines = %d, want the match plus one either side", len(res.Lines))
	}
	if res.Lines[1].No != 3 {
		t.Errorf("the match is line 3 of the file, got %d", res.Lines[1].No)
	}
	if len(res.Lines[1].Match) == 0 {
		t.Error("a match carries the ranges the browser paints, which it cannot compute itself")
	}
	if !res.Lines[0].Context || res.Lines[1].Context {
		t.Error("context lines are marked and the match is not")
	}
	total := 0
	for _, b := range res.Histogram {
		total += b.Total
	}
	if total != 1 {
		t.Errorf("histogram counted %d matches, want 1", total)
	}
}

// The whole point of reading archives is answering "when did this start", and
// last night's logrotate run is exactly where that answer lives.
func TestLogSearchReadsRotatedArchives(t *testing.T) {
	c, root := logClient(t)
	live := filepath.Join(root, "app.log")
	writeLog(t, live, "today is quiet")

	gz := filepath.Join(root, "app.log.1.gz")
	f, err := os.Create(gz)
	if err != nil {
		t.Fatal(err)
	}
	zw := gzip.NewWriter(f)
	zw.Write([]byte("yesterday: boom\n"))
	zw.Close()
	f.Close()
	if err := os.Chtimes(gz, time.Now(), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	without := decode[logsx.SearchResult](t, c.do("GET",
		"/api/v1/logs/search?source="+live+"&q=boom", "", nil))
	if without.Matched != 0 {
		t.Fatalf("the live file alone should hold no match, got %d", without.Matched)
	}

	with := decode[logsx.SearchResult](t, c.do("GET",
		"/api/v1/logs/search?source="+live+"&q=boom&archives=true", "", nil))
	if with.Matched != 1 {
		t.Fatalf("matched = %d across the rotated set, want 1", with.Matched)
	}
	if len(with.Files) != 2 || !with.Files[0].Archive {
		t.Errorf("the archive is read first and reported separately: %+v", with.Files)
	}
}

// The export used to ignore the filter and hand back the whole file, so
// narrowing the view and pressing Export produced two different logs.
func TestLogExportCarriesTheFilter(t *testing.T) {
	c, root := logClient(t)
	path := filepath.Join(root, "app.log")
	writeLog(t, path, "keep boom", "drop this", "keep boom again")

	rec := c.do("GET", "/api/v1/logs/download?source="+path+"&q=boom", "", nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "drop this") {
		t.Errorf("the export kept a line the filter rejects:\n%s", body)
	}
	if strings.Count(body, "boom") != 2 {
		t.Errorf("the export lost a matching line:\n%s", body)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q", cd)
	}
}

// A bad expression is refused where the operator can see it, rather than
// opening a socket that then streams nothing and looks broken.
func TestLogRoutesRefuseABadExpressionUpFront(t *testing.T) {
	c, root := logClient(t)
	path := filepath.Join(root, "app.log")
	writeLog(t, path, "anything")

	for _, route := range []string{"/api/v1/logs/search", "/api/v1/logs/stream", "/api/v1/logs/download"} {
		rec := c.do("GET", route+"?source="+path+"&q=%28%5B&regex=true", "", nil)
		if rec.Code != 400 {
			t.Errorf("%s: status %d, want 400 — got %s", route, rec.Code, rec.Body.String())
		}
	}
}

// Containment is the same on every entry point. A log viewer that could open
// /etc/shadow would be a privilege escalation dressed up as a feature.
func TestLogRoutesRefusePathsOutsideTheRoots(t *testing.T) {
	c, _ := logClient(t)
	for _, route := range []string{"/api/v1/logs/search", "/api/v1/logs/retention"} {
		rec := c.do("GET", route+"?source=/etc/shadow", "", nil)
		if rec.Code != 400 {
			t.Errorf("%s: status %d, want a refusal", route, rec.Code)
		}
	}
	if rec := c.do("GET", "/api/v1/logs/search?source=etc/passwd", "", nil); rec.Code != 400 {
		t.Errorf("a relative path is not a source: status %d", rec.Code)
	}
}

func TestLogRetentionAnswersForAFile(t *testing.T) {
	c, root := logClient(t)
	path := filepath.Join(root, "app.log")
	writeLog(t, path, "hello")

	got := decode[logsx.Retention](t, c.do("GET", "/api/v1/logs/retention?source="+path, "", nil))
	if got.Summary == "" || got.Level == "" {
		t.Fatalf("retention must carry a verdict and a sentence: %+v", got)
	}
	// A check that could not run is not a pass.
	if !got.Available && got.Level == "ok" {
		t.Error("without logrotate the verdict cannot be ok")
	}
	if rec := c.do("GET", "/api/v1/logs/retention?source=journal:", "", nil); rec.Code != 400 {
		t.Errorf("retention applies to files; a journal source should say so, got %d", rec.Code)
	}
}

// Every source kind resolves through one parser, which is what lets the
// stream, the search and the export agree about what "this source" means.
func TestLogTargetParsing(t *testing.T) {
	cases := []struct {
		raw   string
		kind  logsx.SourceKind
		id    string
		path  string
		fails bool
	}{
		{raw: "docker:abc123", kind: logsx.KindDocker, id: "abc123"},
		{raw: "pm2:api", kind: logsx.KindPM2, id: "api"},
		{raw: "journal:", kind: logsx.KindJournal},
		{raw: "journal:nginx.service", kind: logsx.KindJournal, id: "nginx.service"},
		{raw: "/var/log/syslog", kind: logsx.KindSystem, path: "/var/log/syslog"},
		{raw: "file:/var/log/syslog", kind: logsx.KindSystem, path: "/var/log/syslog"},
		{raw: "", fails: true},
		{raw: "relative.log", fails: true},
		{raw: "docker:", fails: true},
	}
	for _, tc := range cases {
		got, err := parseLogTarget(tc.raw)
		if tc.fails {
			if err == nil {
				t.Errorf("%q should be refused", tc.raw)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", tc.raw, err)
			continue
		}
		if got.kind != tc.kind || got.id != tc.id || got.path != tc.path {
			t.Errorf("%q = %+v, want kind %q id %q path %q", tc.raw, got, tc.kind, tc.id, tc.path)
		}
	}
}

// The journal takes a priority range, and the chips are a set. Only the
// maximum is pushed down, and a chip that has no priority at all must stop the
// narrowing rather than silently drop the lines it asked for.
func TestJournalPriorityNarrowing(t *testing.T) {
	cases := []struct {
		levels []string
		want   int
	}{
		{nil, -1},
		{[]string{"error"}, 3},
		{[]string{"critical"}, 2},
		{[]string{"error", "warn"}, 4},
		{[]string{"debug"}, 7},
		{[]string{"error", logsx.LevelUnknown}, -1},
	}
	for _, tc := range cases {
		if got := maxJournalPriority(tc.levels); got != tc.want {
			t.Errorf("maxJournalPriority(%v) = %d, want %d", tc.levels, got, tc.want)
		}
	}
}

// Docker prefixes each line with an RFC3339Nano stamp when timestamps are on.
// Leaving it in the text draws the timestamp twice, and the file parser cannot
// read it — nanoseconds are longer than any layout it knows.
func TestDockerLineSplitsTheTimestampOut(t *testing.T) {
	got := dockerLine(dockerxLogLine("2024-06-12T10:00:02.123456789Z ERROR upstream refused", "stderr"))
	if got.Text != "ERROR upstream refused" {
		t.Errorf("text = %q, want the line without its timestamp", got.Text)
	}
	if got.Timestamp == nil || got.Timestamp.Second() != 2 {
		t.Errorf("timestamp = %v", got.Timestamp)
	}
	if got.Level != "error" {
		t.Errorf("level = %q, want error", got.Level)
	}

	// A container logging normally to stderr — which many do — must not be
	// painted as an error when its own text said otherwise.
	info := dockerLine(dockerxLogLine("2024-06-12T10:00:02Z INFO listening on 3000", "stderr"))
	if info.Level != "info" {
		t.Errorf("level = %q, want the level the line itself claims", info.Level)
	}
	// With nothing in the text to go on, the line claims no level and the page
	// must not invent one. stderr is where npm writes its notices, where
	// Prisma writes "Update available" and where half the CLI world writes
	// anything that is not the program's output — a freshly deployed, healthy
	// project opened its Logs tab reading "13 errors", every one of them a
	// version banner. The stream is recorded on the line and rendered; that is
	// the honest signal.
	bare := dockerLine(dockerxLogLine("2024-06-12T10:00:02Z something happened", "stderr"))
	if bare.Level != "" {
		t.Errorf("level = %q, want no level for a line that claims none", bare.Level)
	}
	if bare.Stream != "stderr" {
		t.Errorf("stream = %q, want it kept so the viewer can mark it", bare.Stream)
	}
}

// A build tool's progress spinner writes cursor moves and colour into the same
// stream as its output. Leaving them in draws "B[2KB[1AB[2KB[G" on the page and
// — worse — feeds the escape bytes to the level scan and the operator's search.
func TestDockerLineStripsTerminalControl(t *testing.T) {
	got := dockerLine(dockerxLogLine("2024-06-12T10:00:02Z \x1b[2K\x1b[1A\x1b[32mGenerated Prisma Client\x1b[0m", "stdout"))
	if got.Text != "Generated Prisma Client" {
		t.Errorf("text = %q, want the escape sequences resolved away", got.Text)
	}
}

// An application logging JSON is the common case for anything written this
// decade, and the word scan finds either nothing or the wrong thing in one.
func TestDockerLineReadsStructuredOutput(t *testing.T) {
	got := dockerLine(dockerxLogLine(`2024-06-12T10:00:02Z {"level":"error","msg":"upstream timeout","requestId":"r-1"}`, "stdout"))
	if got.Level != "error" {
		t.Errorf("level = %q, want the level the JSON claims", got.Level)
	}
	if got.Message != "upstream timeout" {
		t.Errorf("message = %q", got.Message)
	}
	if got.Fields["requestId"] != "r-1" {
		t.Errorf("fields = %v, want the context that is the whole point of logging JSON", got.Fields)
	}
}
