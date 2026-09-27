package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/logsx"
	"github.com/gorilla/websocket"
)

// These hold the log routes to the lenses themselves: each way a line is read
// — the file search, the export, the file's live tail, the journal's search
// and follow, a stack's follow — with a real line of the log it names, so a
// path that parses without reading the lens, or reads it after the filter,
// fails here rather than on a service page. The container search and follow
// are TestContainerReadsKeepRecordsTogether's.

// f2bLog is fail2ban's own log, which the route detects by its name.
var f2bLog = []string{
	"2026-09-27 10:00:01,101 fail2ban.filter         [992]: INFO    [sshd] Found 203.0.113.9 - 2026-09-27 10:00:01",
	"2026-09-27 10:00:02,202 fail2ban.filter         [992]: INFO    [sshd] Found 203.0.113.9 - 2026-09-27 10:00:02",
	"2026-09-27 10:00:03,303 fail2ban.actions        [992]: NOTICE  [sshd] Ban 203.0.113.9",
	"2026-09-27 10:05:00,404 fail2ban.actions        [992]: NOTICE  [recidive] Ban 198.51.100.7",
	"2026-09-27 11:00:03,505 fail2ban.actions        [992]: NOTICE  [sshd] Unban 203.0.113.9",
}

func TestFileSearchAndExportReadThroughTheDetectedLens(t *testing.T) {
	c, root := logClient(t)
	path := filepath.Join(root, "fail2ban.log")
	writeLog(t, path, f2bLog...)

	res := decode[logsx.SearchResult](t, c.do("GET",
		"/api/v1/logs/search?source="+url.QueryEscape("file:"+path)+"&f=event:ban&facets=jail,client&histogramBy=event", "", nil))
	if res.Lens != "fail2ban" || res.Matched != 2 {
		t.Fatalf("lens %q matched %d; fail2ban.log reads as fail2ban and has two bans", res.Lens, res.Matched)
	}
	for _, l := range res.Lines {
		if l.Event != "ban" || l.Attrs["jail"] == "" || l.Attrs["client"] == "" || l.Level != "warn" {
			t.Errorf("line %q read as %q %q %v", l.Text, l.Event, l.Level, l.Attrs)
		}
	}
	jails := res.Facets["jail"]
	if jails == nil || len(jails.Values) != 2 {
		t.Fatalf("jail facet = %+v", jails)
	}
	if res.HistogramBy != "event" {
		t.Errorf("histogramBy = %q", res.HistogramBy)
	}

	// The export reads the same lens and applies the same predicate.
	export := c.do("GET", "/api/v1/logs/download?source="+url.QueryEscape("file:"+path)+"&f=jail:recidive", "", nil)
	if export.Code != 200 || strings.Count(export.Body.String(), "\n") != 1 || !strings.Contains(export.Body.String(), "198.51.100.7") {
		t.Errorf("export (%d):\n%s", export.Code, export.Body.String())
	}

	// lens=none reads nothing, so no line has an event to match.
	none := decode[logsx.SearchResult](t, c.do("GET",
		"/api/v1/logs/search?source="+url.QueryEscape("file:"+path)+"&f=event:ban&lens=none", "", nil))
	if none.Matched != 0 || none.Lens != "" {
		t.Errorf("lens=none: lens %q matched %d", none.Lens, none.Matched)
	}
}

func dialLogStream(t *testing.T, s *Server, cookie, query string) *websocket.Conn {
	t.Helper()
	srv := httptest.NewServer(s.Routes())
	t.Cleanup(srv.Close)
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+
		"/api/v1/logs/stream?"+query, http.Header{"Cookie": {cookie}})
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("could not open the stream (%d): %v", status, err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	return conn
}

// readLines reads batches until want lines have arrived or the stream ends.
func readLines(t *testing.T, conn *websocket.Conn, want int) []logsx.Line {
	t.Helper()
	got := []logsx.Line{}
	for len(got) < want {
		kind, data := readFrame(t, conn)
		if kind == "eof" {
			break
		}
		var batch []logsx.Line
		if err := json.Unmarshal(data, &batch); err != nil {
			t.Fatal(err)
		}
		got = append(got, batch...)
	}
	return got
}

// The live tail of a file reads its opening window and what is appended after
// it through one lens, and a kept record's continuation follows its head past
// the text filter in both.
func TestFileFollowReadsThroughTheLens(t *testing.T) {
	s := testServer(t)
	cookie := signIn(t, s)
	path := filepath.Join(s.Cfg.LogRoots[0], "postgresql-17-main.log")
	writeLog(t, path,
		"2026-09-27 10:00:00.000 UTC [1] LOG:  database system is ready to accept connections",
		`2026-09-27 10:00:00.030 UTC [812] postgres@shop ERROR:  relation "userz" does not exist at character 15`,
		"2026-09-27 10:00:00.030 UTC [812] postgres@shop STATEMENT:  select * from userz",
		"2026-09-27 10:00:01.000 UTC [813] postgres@shop LOG:  duration: 1.2 ms",
	)
	conn := dialLogStream(t, s, cookie, "source="+url.QueryEscape("file:"+path)+"&lens=postgres&q=relation&lines=50")
	kind, data := readFrame(t, conn)
	var meta streamMeta
	if err := json.Unmarshal(data, &meta); err != nil || kind != "meta" || meta.Lens != "postgres" || !meta.Filtered {
		t.Fatalf("meta = %s %s", kind, data)
	}
	check := func(got []logsx.Line, when string) {
		t.Helper()
		if len(got) != 2 || got[0].Event != "error" || got[0].Attrs["code"] != "42P01" ||
			!got[1].Cont || got[1].Level != got[0].Level || got[1].Event != "" {
			t.Fatalf("%s: %+v", when, got)
		}
	}
	check(readLines(t, conn, 2), "opening window")

	// The follow picks up where the window stopped, with the same reader.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(f, `2026-09-27 10:00:02.000 UTC [814] postgres@shop ERROR:  relation "orderz" does not exist at character 15`)
	fmt.Fprintln(f, "2026-09-27 10:00:02.000 UTC [814] postgres@shop STATEMENT:  select * from orderz")
	fmt.Fprintln(f, "2026-09-27 10:00:03.000 UTC [815] postgres@shop LOG:  duration: 2.5 ms")
	f.Close()
	check(readLines(t, conn, 2), "follow")
}

// fakeJournalctl puts a journalctl on PATH that prints these records as
// `--output=json` does and exits, which is also how a follow ends. It records
// its argv, so the test can see what was pushed down to it.
func fakeJournalctl(t *testing.T, records ...map[string]string) (argv func() string) {
	t.Helper()
	if os.Geteuid() == 0 {
		// As root the command crosses into the host's namespaces and runs
		// the real journalctl, not this one.
		t.Skip("journalctl is the host's when running as root")
	}
	bin := t.TempDir()
	var out strings.Builder
	for _, r := range records {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		out.Write(b)
		out.WriteByte('\n')
	}
	data := filepath.Join(bin, "records.json")
	args := filepath.Join(bin, "argv")
	if err := os.WriteFile(data, []byte(out.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"$@\" >> '" + args + "'\ncat '" + data + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "journalctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() string {
		b, _ := os.ReadFile(args)
		return string(b)
	}
}

// ssh.service as the journal holds it: sshd-session's refusals and the
// manager's lines about the unit, in one run.
func sshJournal(base time.Time) []map[string]string {
	at := func(s int) string { return fmt.Sprint(base.Add(time.Duration(s) * time.Second).UnixMicro()) }
	return []map[string]string{
		{"__REALTIME_TIMESTAMP": at(0), "PRIORITY": "6", "SYSLOG_IDENTIFIER": "sshd-session", "_PID": "3110301",
			"_SYSTEMD_UNIT": "ssh.service", "MESSAGE": "Invalid user ekala from 203.0.113.42 port 30358"},
		{"__REALTIME_TIMESTAMP": at(1), "PRIORITY": "6", "SYSLOG_IDENTIFIER": "sshd-session", "_PID": "3110301",
			"_SYSTEMD_UNIT": "ssh.service", "MESSAGE": "Connection closed by invalid user ekala 203.0.113.42 port 30358 [preauth]"},
		{"__REALTIME_TIMESTAMP": at(2), "PRIORITY": "6", "SYSLOG_IDENTIFIER": "sshd-session", "_PID": "3110400",
			"_SYSTEMD_UNIT": "ssh.service", "MESSAGE": "Accepted publickey for ubuntu from 198.51.100.20 port 50112 ssh2: ED25519 SHA256:abc"},
		{"__REALTIME_TIMESTAMP": at(3), "PRIORITY": "6", "SYSLOG_IDENTIFIER": "systemd", "_PID": "1",
			"_SYSTEMD_UNIT": "init.scope", "UNIT": "ssh.service", "MESSAGE_ID": "7ad2d189f7e94e70a38c781354912448",
			"MESSAGE": "ssh.service: Deactivated successfully."},
	}
}

// The journal's search and its follow read each record through the unit's
// lens, and hand the manager's lines about the unit to the systemd lens.
func TestJournalReadsThroughTheLens(t *testing.T) {
	base := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	argv := fakeJournalctl(t, sshJournal(base)...)
	s := testServer(t)
	cookie := signIn(t, s)
	c := &client{t: t, h: s.Routes(), cookie: cookie}

	res := decode[logsx.SearchResult](t, c.do("GET",
		"/api/v1/logs/search?source=journal:ssh.service&since=2026-09-27T09:00:00Z&facets=event", "", nil))
	events := []string{}
	for _, l := range res.Lines {
		events = append(events, l.Event+"/"+l.Lens)
	}
	want := "ssh_invalid_user/,ssh_preauth_closed/,ssh_accepted/,deactivated/systemd"
	if res.Lens != "auth" || strings.Join(events, ",") != want {
		t.Fatalf("lens %q events %v, want auth and %s", res.Lens, events, want)
	}
	if got := res.Lines[3].Attrs["unit"]; got != "ssh.service" {
		t.Errorf("the manager's line is about unit %q", got)
	}
	failed := decode[logsx.SearchResult](t, c.do("GET",
		"/api/v1/logs/search?source=journal:ssh.service&since=2026-09-27T09:00:00Z&f=event:ssh_invalid_user&f=event:ssh_failed", "", nil))
	if failed.Matched != 1 || failed.Lines[0].Attrs["user"] != "ekala" || failed.Lines[0].Attrs["client"] != "203.0.113.42" {
		t.Errorf("predicate over the journal: %+v", failed.Lines)
	}

	conn := dialLogStream(t, s, cookie, "source=journal:ssh.service&levels=error")
	kind, data := readFrame(t, conn)
	var meta streamMeta
	if err := json.Unmarshal(data, &meta); err != nil || kind != "meta" || meta.Lens != "auth" {
		t.Fatalf("meta = %s %s", kind, data)
	}
	if got := readLines(t, conn, 1); len(got) != 0 {
		t.Errorf("an error filter kept %+v; nothing in this run is an error", got)
	}
	// With a lens reading the lines, the error chip is not pushed down as
	// -p 0..3: the lens may raise what the program filed at info.
	if last := argv(); !strings.Contains(last, "-p 0..6") || !strings.Contains(last, "-f") {
		t.Errorf("journalctl was run as:\n%s", last)
	}

	conn = dialLogStream(t, s, cookie, "source=journal:ssh.service&f=event:ssh_accepted")
	readFrame(t, conn)
	if got := readLines(t, conn, 1); len(got) != 1 || got[0].Event != "ssh_accepted" || got[0].Attrs["user"] != "ubuntu" {
		t.Errorf("follow with a predicate: %+v", got)
	}
}

// A stack's live tail reads each container through the lens of its own
// image, and a line named by a lens that is not the stack's says which.
func TestStackFollowReadsEachContainerThroughItsLens(t *testing.T) {
	s := testServer(t)
	base := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	serveFakeLogEngine(t, s, shopStack(base)...)
	conn := dialLogStream(t, s, signIn(t, s), "source=stack:shop&lines=50")
	kind, data := readFrame(t, conn)
	var meta streamMeta
	if err := json.Unmarshal(data, &meta); err != nil || kind != "meta" || meta.Lens != "" {
		t.Fatalf("meta = %s %s; containers with different lenses leave the stack without one", kind, data)
	}
	got := readLines(t, conn, 9)
	var ready, errLine, statement *logsx.Line
	for i, l := range got {
		switch {
		case strings.Contains(l.Text, "ready to accept"):
			ready = &got[i]
		case strings.Contains(l.Text, "ERROR:"):
			errLine = &got[i]
		case strings.Contains(l.Text, "STATEMENT:"):
			statement = &got[i]
		}
	}
	if ready == nil || ready.Event != "ready" || ready.Lens != "postgres" || ready.Attrs["service"] != "db" {
		t.Errorf("ready = %+v", ready)
	}
	if errLine == nil || errLine.Event != "error" || errLine.Lens != "postgres" {
		t.Errorf("error = %+v", errLine)
	}
	if statement == nil || !statement.Cont || statement.Level != "error" {
		t.Errorf("statement = %+v", statement)
	}
}
