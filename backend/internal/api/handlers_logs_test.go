package api

import (
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/logsx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
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
		{raw: "stack:shop", kind: logsx.KindStack, id: "shop"},
		{raw: "stack:my_app-2", kind: logsx.KindStack, id: "my_app-2"},
		{raw: "journal-id:sshd,sshd-session,sudo", kind: logsx.KindJournalID},
		{raw: "kernel:", kind: logsx.KindKernel},
		{raw: "", fails: true},
		{raw: "relative.log", fails: true},
		{raw: "docker:", fails: true},
		{raw: "stack:", fails: true},
		{raw: "stack:Shop", fails: true},
		{raw: "stack:../etc", fails: true},
		{raw: "journal-id:", fails: true},
		{raw: "journal-id:--output=cat", fails: true},
		{raw: "kernel:ring", fails: true},
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
	got := dockerLine(dockerxLogLine("2024-06-12T10:00:02.123456789Z ERROR upstream refused", "stderr"), nil)
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
	info := dockerLine(dockerxLogLine("2024-06-12T10:00:02Z INFO listening on 3000", "stderr"), nil)
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
	bare := dockerLine(dockerxLogLine("2024-06-12T10:00:02Z something happened", "stderr"), nil)
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
	got := dockerLine(dockerxLogLine("2024-06-12T10:00:02Z \x1b[2K\x1b[1A\x1b[32mGenerated Prisma Client\x1b[0m", "stdout"), nil)
	if got.Text != "Generated Prisma Client" {
		t.Errorf("text = %q, want the escape sequences resolved away", got.Text)
	}
}

// An application logging JSON is the common case for anything written this
// decade, and the word scan finds either nothing or the wrong thing in one.
func TestDockerLineReadsStructuredOutput(t *testing.T) {
	got := dockerLine(dockerxLogLine(`2024-06-12T10:00:02Z {"level":"error","msg":"upstream timeout","requestId":"r-1"}`, "stdout"), nil)
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

// Failed logins can hold passwords typed into the username prompt, which is
// why /logins/failed needs system.admin. The same lines in auth.log, sshd's
// journal and the sshd identifiers need it too, on every route that reads a
// source, and they are not offered to anyone who could not open them.
func TestAuthLogsNeedAnAdministrator(t *testing.T) {
	s := testServer(t)
	root := s.Cfg.LogRoots[0]
	admin := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "reader", auth.RoleReadOnly)}
	authLog := filepath.Join(root, "auth.log")
	writeLog(t, authLog, "2026-09-27T00:21:02.476539+00:00 vps sshd-session[78923]: Invalid user hunter2 from 203.0.113.7 port 30358")
	writeLog(t, filepath.Join(root, "auth.log.1"), "yesterday")
	writeLog(t, filepath.Join(root, "app.log"), "hello")
	if err := os.Symlink(authLog, filepath.Join(root, "innocent.log")); err != nil {
		t.Fatal(err)
	}

	gated := []string{
		authLog, filepath.Join(root, "auth.log.1"), filepath.Join(root, "innocent.log"),
		"journal:ssh.service", "journal:sshd.service", "journal:sshd@0-10.0.0.1:22-203.0.113.7:4040.service",
		"journal-id:sshd", "journal-id:CRON,sshd-session", "journal-id:sudo", "journal-id:systemd-logind",
	}
	for _, source := range gated {
		for _, route := range []string{"search", "stream", "download", "retention", "source"} {
			rec := reader.do("GET", "/api/v1/logs/"+route+"?source="+url.QueryEscape(source), "", nil)
			if rec.Code != http.StatusForbidden {
				t.Errorf("reader %s %s: status %d, want 403", route, source, rec.Code)
			}
		}
	}
	for _, route := range []string{"search", "download", "retention", "source"} {
		if rec := admin.do("GET", "/api/v1/logs/"+route+"?source="+url.QueryEscape(authLog), "", nil); rec.Code != 200 {
			t.Errorf("admin %s: status %d: %s", route, rec.Code, rec.Body.String())
		}
	}
	// What is not auth data stays readable, including the whole journal and
	// the cron identifier.
	for _, source := range []string{filepath.Join(root, "app.log"), "journal:cron.service", "journal-id:CRON", "kernel:", "journal:"} {
		if rec := reader.do("GET", "/api/v1/logs/source?source="+url.QueryEscape(source), "", nil); rec.Code == http.StatusForbidden {
			t.Errorf("reader %s was refused", source)
		}
	}

	listed := func(c *client) map[string]bool {
		index := decode[logSourceIndex](t, c.do("GET", "/api/v1/logs/sources", "", nil))
		out := map[string]bool{}
		for _, src := range index.Sources {
			out[filepath.Base(src.Path)] = true
		}
		return out
	}
	if got := listed(reader); got["auth.log"] || !got["app.log"] {
		t.Errorf("reader's sources = %v, want app.log and not auth.log", got)
	}
	if got := listed(admin); !got["auth.log"] {
		t.Errorf("admin's sources = %v, want auth.log", got)
	}
}

// Everything a search can be refused for is refused as a 400 before the
// source is read — for a container that would otherwise surface as a daemon
// error — and the stream refuses before the upgrade.
func TestLogRoutesRefuseBadFieldsAndLensesUpFront(t *testing.T) {
	c, root := logClient(t)
	path := filepath.Join(root, "app.log")
	writeLog(t, path, "anything")
	bad := []string{
		"f=nokey", "f=level:error", "f=status:>=five", "lens=no-such-lens",
		"facets=" + strings.Repeat("a,", 12) + "a", "facetLimit=0", "facetLimit=51", "facetLimit=x",
		"sample=user", "measure=a:b", "histogramValues=slow",
	}
	for _, query := range bad {
		for _, route := range []string{"/api/v1/logs/search", "/api/v1/logs/download"} {
			if rec := c.do("GET", route+"?source="+path+"&"+query, "", nil); rec.Code != 400 {
				t.Errorf("%s?%s: status %d, want 400 — %s", route, query, rec.Code, rec.Body.String())
			}
		}
	}
	for _, query := range []string{"f=nokey", "lens=no-such-lens"} {
		for _, source := range []string{path, "docker:web", "stack:shop", "journal:x.service"} {
			if rec := c.do("GET", "/api/v1/logs/stream?source="+url.QueryEscape(source)+"&"+query, "", nil); rec.Code != 400 {
				t.Errorf("stream %s?%s: status %d, want 400", source, query, rec.Code)
			}
		}
	}
	if rec := c.do("GET", "/api/v1/logs/search?source=docker:web&f=nokey", "", nil); rec.Code != 400 {
		t.Errorf("a container search with a bad predicate: status %d, want 400", rec.Code)
	}
}

// The predicates, the facets and the measure reach the collector from the
// query string, and a predicate-only filter is reported as a filter.
func TestLogSearchAppliesFieldsAndFacets(t *testing.T) {
	c, root := logClient(t)
	path := filepath.Join(root, "app.log")
	writeLog(t, path,
		`{"time":"2026-09-27T10:00:00Z","level":"info","msg":"login","user":"alice","duration_ms":10}`,
		`{"time":"2026-09-27T10:01:00Z","level":"error","msg":"login","user":"bob","duration_ms":250}`,
		`{"time":"2026-09-27T10:02:00Z","level":"info","msg":"login","user":"bob","duration_ms":40}`,
		`time="2026-09-27T10:03:00Z" level=warning msg="slow login" user=bob duration_ms=900`,
	)
	res := decode[logsx.SearchResult](t, c.do("GET",
		"/api/v1/logs/search?source="+path+"&f=user:bob&f=duration_ms:>=100&facets=level,user&measure=duration_ms&sample=user&histogramBy=level&lens=none", "", nil))
	if res.Matched != 2 {
		t.Fatalf("matched = %d, want bob's two slow logins", res.Matched)
	}
	levels := res.Facets["level"]
	if levels == nil || len(levels.Values) != 2 || levels.Values[0].Samples["user"] != "bob" {
		t.Errorf("level facet = %+v", levels)
	}
	if res.Measure == nil || res.Measure.Count != 2 || res.Measure.Max != 900 {
		t.Errorf("measure = %+v", res.Measure)
	}
	if res.Lens != "" {
		t.Errorf("lens = %q, want none", res.Lens)
	}
	var out strings.Builder
	rec := c.do("GET", "/api/v1/logs/download?source="+path+"&f=user:alice", "", nil)
	out.WriteString(rec.Body.String())
	if strings.Count(out.String(), "\n") != 1 || !strings.Contains(out.String(), "alice") {
		t.Errorf("the export ignored the predicate:\n%s", out.String())
	}
}

func TestLogSourceDescribesOneFile(t *testing.T) {
	c, root := logClient(t)
	path := filepath.Join(root, "app.log")
	writeLog(t, path, "hello")
	writeLog(t, path+".1", "yesterday")
	src := decode[logsx.Source](t, c.do("GET", "/api/v1/logs/source?source=file:"+path, "", nil))
	if src.Path != path || src.Size == 0 || src.Modified == nil || src.Archives != 1 || src.Label != "app.log" {
		t.Errorf("source = %+v", src)
	}
	if rec := c.do("GET", "/api/v1/logs/source?source="+filepath.Join(root, "missing.log"), "", nil); rec.Code != 404 {
		t.Errorf("a missing file: status %d, want 404", rec.Code)
	}
	if rec := c.do("GET", "/api/v1/logs/source?source=/etc/shadow", "", nil); rec.Code != 400 {
		t.Errorf("outside the roots: status %d, want 400", rec.Code)
	}
	unit := decode[logsx.Source](t, c.do("GET", "/api/v1/logs/source?source=journal-id:sshd-session,sudo", "", nil))
	if unit.Kind != logsx.KindJournalID || unit.ID != "journal-id:sshd-session,sudo" || unit.Label != "sshd-session, sudo" {
		t.Errorf("journal-id source = %+v", unit)
	}
}

// The journal's fields become attrs, the manager's UNIT and INVOCATION_ID win
// over the fields that name PID 1, and the level follows its precedence: a
// structured level key over the priority, the priority over the word scan.
func TestJournalLineFieldsAndLevel(t *testing.T) {
	stamp := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	manager := journalLine(procs.JournalEntry{
		Timestamp: stamp, Priority: 4, Unit: "init.scope", Syslog: "systemd", PID: "1",
		About: "nordvpnd-killswitch.service", Invocation: "bce17d", MessageID: "98e322203f7a4ed290d09fe03c09fe15",
		ExitCode: "exited", ExitStatus: "1",
		Message: "nordvpnd-killswitch.service: Main process exited, code=exited, status=1/FAILURE",
	}, nil)
	want := map[string]string{
		"unit": "nordvpnd-killswitch.service", "program": "systemd", "pid": "1", "invocation": "bce17d",
		"message_id": "98e322203f7a4ed290d09fe03c09fe15", "exit_code": "exited", "exit_status": "1",
	}
	for k, v := range want {
		if manager.Attrs[k] != v {
			t.Errorf("attr %s = %q, want %q", k, manager.Attrs[k], v)
		}
	}
	if manager.Level != "warn" || manager.Source != "systemd[1]" || manager.Fields != nil {
		t.Errorf("manager line = %+v", manager)
	}

	prose := journalLine(procs.JournalEntry{Timestamp: stamp, Priority: 6, Unit: "api.service", Comm: "node",
		Message: "error-reporting enabled"}, nil)
	if prose.Level != "info" || prose.Attrs["program"] != "node" || prose.Attrs["unit"] != "api.service" {
		t.Errorf("a priority-6 line with the word error in it = %q %v", prose.Level, prose.Attrs)
	}
	structured := journalLine(procs.JournalEntry{Timestamp: stamp, Priority: 6,
		Message: "\x1b[31m" + `{"level":"error","msg":"upstream timeout","time":"2020-01-01T00:00:00Z"}`}, nil)
	if structured.Level != "error" || structured.Message != "upstream timeout" {
		t.Errorf("a JSON line under systemd = %q %q", structured.Level, structured.Message)
	}
	// The journal's stamp is when it happened; the one in the message is
	// only when the program thought it was.
	if structured.Timestamp == nil || !structured.Timestamp.Equal(stamp) {
		t.Errorf("timestamp = %v, want the journal's", structured.Timestamp)
	}
	if plain := journalLine(procs.JournalEntry{Priority: 3, Message: "something broke"}, nil); plain.Level != "error" {
		t.Errorf("priority 3 = %q", plain.Level)
	}
}

// The manager's lines always go to the systemd lens; a unit's other programs
// go to theirs only when the lens was detected rather than forced; the whole
// journal leaves the rest to its own composite lens.
func TestJournalRouting(t *testing.T) {
	line := func(program string) *logsx.Line {
		l := &logsx.Line{}
		l.SetAttr("program", program)
		return l
	}
	unit := logTarget{kind: logsx.KindJournal, id: "ssh.service"}
	whole := logTarget{kind: logsx.KindJournal}
	cases := []struct {
		target  logTarget
		forced  bool
		program string
		want    string
	}{
		{unit, false, "systemd", "systemd"},
		{unit, false, "systemd-coredump", "systemd"},
		{unit, false, "sshd-session", "auth"},
		{unit, false, "some-helper", ""},
		{unit, true, "sshd-session", ""},
		{unit, true, "systemd", "systemd"},
		{whole, false, "sshd-session", ""},
		{whole, false, "systemd", "systemd"},
		{logTarget{kind: logsx.KindJournalID, idents: []string{"CRON"}}, false, "CRON", "cron"},
	}
	for _, tc := range cases {
		if got := journalRoute(tc.target, tc.forced)(line(tc.program)); got != tc.want {
			t.Errorf("%+v forced=%v %s = %q, want %q", tc.target, tc.forced, tc.program, got, tc.want)
		}
	}
}

// With a lens reading the lines the chips' priority is not pushed below 6: a
// lens raises what the program filed at info, and the exact test still runs.
func TestJournalPriorityWithALens(t *testing.T) {
	cases := []struct {
		spec    logsx.Filter
		want    int
		clamped bool
	}{
		{logsx.Filter{Levels: []string{"error"}}, 3, false},
		{logsx.Filter{Levels: []string{"error"}, Lens: "postgres"}, 6, true},
		{logsx.Filter{Levels: []string{"error"}, Lens: logsx.LensNone}, 3, false},
		{logsx.Filter{Levels: []string{"debug"}, Lens: "postgres"}, 7, false},
		{logsx.Filter{Lens: "postgres"}, -1, false},
	}
	for _, tc := range cases {
		if got, clamped := journalPriority(tc.spec); got != tc.want || clamped != tc.clamped {
			t.Errorf("%+v = %d %v, want %d %v", tc.spec, got, clamped, tc.want, tc.clamped)
		}
	}
}
