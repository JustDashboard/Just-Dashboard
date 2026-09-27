package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/logsx"
	"github.com/gorilla/websocket"
)

// fakeLogContainer is one container the fake Engine below serves: its list
// entry, its inspect answer and its log, as "<RFC3339Nano stamp> <text>".
// followLines are served only to a follow request — what the container
// printed after the opening window was read.
type fakeLogContainer struct {
	id, name, project, service, state string
	// image is what the list reports; configImage what inspect's Config has.
	image, imageID, configImage string
	lines, followLines          []string
}

type fakeLogEngine struct {
	mu       sync.Mutex
	inspects map[string]int
}

// serveFakeLogEngine points the server at an Engine that knows only these
// containers, each with a TTY so its log is plain lines rather than Docker's
// multiplexed frames. It honours tail, since and until the way the daemon
// does: to the nanosecond the client sent.
func serveFakeLogEngine(t *testing.T, s *Server, containers ...fakeLogContainer) *fakeLogEngine {
	t.Helper()
	fake := &fakeLogEngine{inspects: map[string]int{}}
	find := func(ref string) *fakeLogContainer {
		for i := range containers {
			if containers[i].id == ref || containers[i].name == ref {
				return &containers[i]
			}
		}
		return nil
	}
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_ping" {
			w.Header().Set("API-Version", "1.47")
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 3 || parts[1] != "containers" {
			t.Errorf("unexpected Docker request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if parts[2] == "json" {
			list := []map[string]any{}
			for _, c := range containers {
				list = append(list, map[string]any{
					"Id": c.id, "Names": []string{"/" + c.name}, "Image": c.image, "ImageID": c.imageID, "State": c.state,
					"Labels": map[string]string{"com.docker.compose.project": c.project, "com.docker.compose.service": c.service},
				})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(list)
			return
		}
		c := find(parts[2])
		if c == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "No such container: " + parts[2]})
			return
		}
		switch parts[len(parts)-1] {
		case "json":
			fake.mu.Lock()
			fake.inspects[c.id]++
			fake.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Id": c.id, "Name": "/" + c.name, "Image": c.imageID,
				"Config": map[string]any{"Image": c.configImage, "Tty": true, "Labels": map[string]string{
					"com.docker.compose.project": c.project, "com.docker.compose.service": c.service,
				}},
				"State": map[string]any{"Status": c.state, "Running": c.state == "running"},
			})
		case "logs":
			q := r.URL.Query()
			lines := c.lines
			if q.Get("follow") == "1" || q.Get("follow") == "true" {
				lines = append(slices.Clone(lines), c.followLines...)
			}
			bound := func(v string) time.Time {
				secs, nanos, _ := strings.Cut(v, ".")
				sec, _ := strconv.ParseInt(secs, 10, 64)
				nsec, _ := strconv.ParseInt((nanos + "000000000")[:9], 10, 64)
				return time.Unix(sec, nsec)
			}
			for _, b := range []struct {
				value string
				keep  func(at, bound time.Time) bool
			}{
				{q.Get("since"), func(at, from time.Time) bool { return !at.Before(from) }},
				{q.Get("until"), func(at, until time.Time) bool { return !at.After(until) }},
			} {
				if b.value == "" {
					continue
				}
				edge := bound(b.value)
				kept := []string{}
				for _, l := range lines {
					stamp, _, _ := strings.Cut(l, " ")
					if at, err := time.Parse(time.RFC3339Nano, stamp); err == nil && b.keep(at, edge) {
						kept = append(kept, l)
					}
				}
				lines = kept
			}
			if tail, err := strconv.Atoi(q.Get("tail")); err == nil && tail < len(lines) {
				lines = lines[len(lines)-tail:]
			}
			for _, l := range lines {
				fmt.Fprintln(w, l)
			}
		default:
			t.Errorf("unexpected Docker request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(engine.Close)
	s.modules.docker = dockerx.New(engine.URL)
	t.Cleanup(func() { _ = s.modules.docker.Close() })
	return fake
}

func stamped(at time.Time, text string) string {
	return at.UTC().Format(time.RFC3339Nano) + " " + text
}

// shopStack is a compose project of three containers: a database whose list
// entry names its image only by id (a moved tag, as on the operator's host),
// an app, and a worker that has exited. A container of another project sits
// beside them.
func shopStack(base time.Time) []fakeLogContainer {
	at := func(ms int) time.Time { return base.Add(time.Duration(ms) * time.Millisecond) }
	return []fakeLogContainer{
		{
			id: "aaaaaaaaaaaa1111", name: "shop-db-1", project: "shop", service: "db", state: "running",
			image: "sha256:" + strings.Repeat("d", 64), imageID: "sha256:" + strings.Repeat("d", 64), configImage: "postgres:17",
			lines: []string{
				stamped(at(0), "2026-09-27 10:00:00.000 UTC [1] LOG:  database system is ready to accept connections"),
				stamped(at(30), "2026-09-27 10:00:00.030 UTC [812] ERROR:  relation \"userz\" does not exist at character 15"),
				stamped(at(50), "2026-09-27 10:00:00.030 UTC [812] STATEMENT:  select * from userz"),
			},
		},
		{
			id: "bbbbbbbbbbbb2222", name: "shop-web-1", project: "shop", service: "web", state: "running",
			image: "ghcr.io/acme/shop-web:1", imageID: "sha256:" + strings.Repeat("e", 64), configImage: "ghcr.io/acme/shop-web:1",
			lines: []string{
				stamped(at(10), "listening on :3000"),
				stamped(at(20), "GET /orders 200"),
				stamped(at(40), "GET /orders 500 relation userz does not exist"),
			},
			followLines: []string{stamped(at(70), "GET /health 200")},
		},
		{
			id: "cccccccccccc3333", name: "shop-worker-1", project: "shop", service: "worker", state: "exited",
			image: "ghcr.io/acme/shop-worker:1", imageID: "sha256:" + strings.Repeat("f", 64), configImage: "ghcr.io/acme/shop-worker:1",
			lines: []string{stamped(at(5), "worker started"), stamped(at(60), "worker exited 1")},
		},
		{
			id: "dddddddddddd4444", name: "blog-web-1", project: "blog", service: "web", state: "running",
			image: "nginx:1.27", imageID: "sha256:" + strings.Repeat("a", 64), configImage: "nginx:1.27",
			lines: []string{stamped(at(15), "blog says hello")},
		},
	}
}

// A stack's search is one answer: every container's lines in one time order,
// each carrying the service it came from, numbered after the merge.
func TestStackSearchMergesContainersByTime(t *testing.T) {
	s := testServer(t)
	base := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	serveFakeLogEngine(t, s, shopStack(base)...)
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}

	// Read with no lens, so this is about the merge alone; how each
	// container's lens reads its own lines is TestContainerReadsKeepRecordsTogether's.
	res := decode[logsx.SearchResult](t, c.do("GET", "/api/v1/logs/search?source=stack:shop&lens=none", "", nil))
	want := []string{
		"db:2026-09-27 10:00:00.000 UTC [1] LOG:  database system is ready to accept connections",
		"worker:worker started",
		"web:listening on :3000",
		"web:GET /orders 200",
		"db:2026-09-27 10:00:00.030 UTC [812] ERROR:  relation \"userz\" does not exist at character 15",
		"web:GET /orders 500 relation userz does not exist",
		"db:2026-09-27 10:00:00.030 UTC [812] STATEMENT:  select * from userz",
		"worker:worker exited 1",
	}
	got := []string{}
	for i, l := range res.Lines {
		got = append(got, l.Source+":"+l.Text)
		if l.No != i+1 {
			t.Errorf("line %d numbered %d; numbers count the merged lines", i, l.No)
		}
		if l.Attrs["service"] != l.Source || len(l.Attrs["container"]) != 12 {
			t.Errorf("line %q attrs = %v", l.Text, l.Attrs)
		}
		if i > 0 && l.Timestamp.Before(*res.Lines[i-1].Timestamp) {
			t.Errorf("line %d is older than the one before it", i)
		}
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if res.Matched != 8 || len(res.Files) != 1 || res.Files[0].Name != "stack:shop" {
		t.Errorf("matched %d, files %+v", res.Matched, res.Files)
	}

	filtered := decode[logsx.SearchResult](t, c.do("GET", "/api/v1/logs/search?source=stack:shop&lens=none&f=service:db&q=userz", "", nil))
	if filtered.Matched != 2 || filtered.Lines[0].Source != "db" {
		t.Errorf("service predicate: matched %d, lines %+v", filtered.Matched, filtered.Lines)
	}

	export := c.do("GET", "/api/v1/logs/download?source=stack:shop&q=orders", "", nil)
	if strings.Count(export.Body.String(), "\n") != 2 {
		t.Errorf("stack export:\n%s", export.Body.String())
	}
	if rec := c.do("GET", "/api/v1/logs/search?source=stack:nothing", "", nil); rec.Code != 404 {
		t.Errorf("a project with no containers: status %d, want 404", rec.Code)
	}
}

// The source list has one entry per compose project, describing it by its
// services and images — the database by the name it was created from, not
// the bare id the Engine reports for a moved tag.
func TestLogSourcesListStacks(t *testing.T) {
	s := testServer(t)
	fake := serveFakeLogEngine(t, s, shopStack(time.Now())...)
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}

	index := decode[logSourceIndex](t, c.do("GET", "/api/v1/logs/sources", "", nil))
	var shop *logsx.Source
	containers := 0
	for i, src := range index.Sources {
		switch src.Kind {
		case logsx.KindStack:
			if src.ID == "stack:shop" {
				shop = &index.Sources[i]
			}
		case logsx.KindDocker:
			containers++
		}
	}
	if containers != 4 {
		t.Errorf("containers listed = %d; a stack's containers stay listed on their own", containers)
	}
	if shop == nil {
		t.Fatalf("no stack:shop in %+v", index.Sources)
	}
	if shop.Detail != "3 services" || shop.Status != "running" {
		t.Errorf("stack = %+v", shop)
	}
	if !slices.Equal(shop.Images, []string{"postgres:17", "ghcr.io/acme/shop-web:1", "ghcr.io/acme/shop-worker:1"}) {
		t.Errorf("images = %v", shop.Images)
	}

	one := decode[logsx.Source](t, c.do("GET", "/api/v1/logs/source?source=stack:shop", "", nil))
	if one.ID != "stack:shop" || one.Detail != "3 services" || len(one.Images) != 3 {
		t.Errorf("/logs/source = %+v", one)
	}
	container := decode[logsx.Source](t, c.do("GET", "/api/v1/logs/source?source=docker:shop-db-1", "", nil))
	if container.ID != "docker:shop-db-1" || container.Label != "shop-db-1" || container.Status != "running" {
		t.Errorf("container source = %+v", container)
	}

	// The list itself trades a moved tag's sha256 id for the config's name
	// (the images above). An id printed bare, with no prefix, it passes on,
	// and that is asked of Inspect once per image, not on every poll of the
	// source list.
	bare := dockerx.Container{ID: "aaaaaaaaaaaa1111", Image: strings.Repeat("d", 12), ImageID: "sha256:" + strings.Repeat("d", 64)}
	before := fake.inspects["aaaaaaaaaaaa1111"]
	for range 3 {
		if got := s.containerImage(t.Context(), bare); got != "postgres:17" {
			t.Fatalf("image = %q", got)
		}
	}
	if asked := fake.inspects["aaaaaaaaaaaa1111"] - before; asked != 1 {
		t.Errorf("inspected %d times for one bare image id, want once", asked)
	}
}

// The live stack opens on the last n lines of the merged log, in time order,
// then follows each running container from its own last stamp: a line is
// neither lost between the two phases nor sent twice.
func TestStackStreamOpensInTimeOrderAndFollows(t *testing.T) {
	s := testServer(t)
	base := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	serveFakeLogEngine(t, s, shopStack(base)...)
	cookie := signIn(t, s)
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()

	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+
		"/api/v1/logs/stream?source=stack:shop&lines=3", http.Header{"Cookie": {cookie}})
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("could not open the stream (%d): %v", status, err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	kind, data := readFrame(t, conn)
	var meta streamMeta
	if err := json.Unmarshal(data, &meta); err != nil || kind != "meta" || meta.Kind != logsx.KindStack || meta.Label != "shop" {
		t.Fatalf("first frame = %s %s", kind, data)
	}
	got := []string{}
	for {
		kind, data := readFrame(t, conn)
		if kind == "eof" {
			break
		}
		var batch []logsx.Line
		if err := json.Unmarshal(data, &batch); err != nil {
			t.Fatal(err)
		}
		for _, l := range batch {
			got = append(got, l.Source+":"+l.Text)
		}
	}
	want := []string{
		// The opening window: the last three lines of the merged log.
		"web:GET /orders 500 relation userz does not exist",
		"db:2026-09-27 10:00:00.030 UTC [812] STATEMENT:  select * from userz",
		"worker:worker exited 1",
		// Then what the running containers printed after their last line.
		"web:GET /health 200",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("stream:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A container's search and its live tail read through the same record gate as
// a file: the query pre-reject cannot drop the STATEMENT a kept ERROR still
// claims, and the lens is the one the image was created from even when the
// list names it by id. This needs a lens that knows continuation lines, so it
// runs once the Postgres lens is in the build.
func TestContainerReadsKeepRecordsTogether(t *testing.T) {
	if lens, _ := logsx.LensByID("postgres"); lens == nil {
		t.Skip("the postgres lens is not in this build")
	}
	s := testServer(t)
	base := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	serveFakeLogEngine(t, s, shopStack(base)...)
	cookie := signIn(t, s)
	c := &client{t: t, h: s.Routes(), cookie: cookie}
	res := decode[logsx.SearchResult](t, c.do("GET", "/api/v1/logs/search?source=docker:shop-db-1&q=relation", "", nil))
	if res.Lens != "postgres" || res.Matched != 1 || len(res.Lines) != 2 || !res.Lines[1].Cont {
		t.Fatalf("search: lens %q matched %d lines %+v", res.Lens, res.Matched, res.Lines)
	}
	if !res.Lines[1].Timestamp.Equal(base.Add(50 * time.Millisecond)) {
		t.Errorf("the continuation's stamp is %v; Docker's stamp is authoritative", res.Lines[1].Timestamp)
	}

	srv := httptest.NewServer(s.Routes())
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+
		"/api/v1/logs/stream?source=docker:shop-db-1&q=relation", http.Header{"Cookie": {cookie}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if kind, data := readFrame(t, conn); kind != "meta" || !strings.Contains(string(data), `"lens":"postgres"`) {
		t.Fatalf("meta = %s %s", kind, data)
	}
	got := []logsx.Line{}
	for {
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
	if len(got) != 2 || got[0].Event == "" || !got[1].Cont || got[1].Level != got[0].Level {
		t.Fatalf("follow: %+v", got)
	}

	// In a stack each container keeps its own lens, and a line whose event
	// comes from a lens that is not the stack's says which.
	stack := decode[logsx.SearchResult](t, c.do("GET", "/api/v1/logs/search?source=stack:shop&q=userz", "", nil))
	var head, cont *logsx.Line
	for i, l := range stack.Lines {
		switch {
		case strings.Contains(l.Text, "ERROR:"):
			head = &stack.Lines[i]
		case strings.Contains(l.Text, "STATEMENT:"):
			cont = &stack.Lines[i]
		}
	}
	if head == nil || cont == nil || head.Lens != "postgres" || head.Event == "" || !cont.Cont || cont.Lens != "" {
		t.Fatalf("stack lines = %+v", stack.Lines)
	}
}

// A window ends where it was asked to, to the nanosecond, for a container and
// for a stack. Handed to Docker in whole seconds, an until inside a second
// was cut back to that second's start: the minute before a crash lost the
// part of a second the crash was written in, and the pages asked for the
// whole second instead and trimmed off the restart that followed.
func TestContainerSearchEndsInsideASecond(t *testing.T) {
	s := testServer(t)
	exit := time.Date(2026, 9, 27, 10, 0, 5, 310456789, time.UTC)
	base := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	stack := shopStack(base)
	stack = append(stack, fakeLogContainer{
		id: "eeeeeeeeeeee5555", name: "crash-db-1", project: "crash", service: "db", state: "running",
		image: "postgres:17", imageID: "sha256:" + strings.Repeat("b", 64), configImage: "postgres:17",
		lines: []string{
			stamped(exit.Add(-2*time.Second), "2026-09-27 10:00:03.310 UTC [1] LOG:  database system is ready to accept connections"),
			stamped(exit.Add(-10*time.Millisecond), `2026-09-27 10:00:05.300 UTC [1] FATAL:  could not write to file "pg_wal/xlogtemp.1": No space left on device`),
			// The restart policy's next attempt, a tenth of a second on.
			stamped(exit.Add(100*time.Millisecond), "2026-09-27 10:00:05.410 UTC [1] LOG:  starting PostgreSQL 17.2"),
		},
	})
	serveFakeLogEngine(t, s, stack...)
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}

	window := "&since=" + exit.Add(-time.Minute).Format(time.RFC3339Nano) + "&until=" + exit.Format(time.RFC3339Nano)
	res := decode[logsx.SearchResult](t, c.do("GET", "/api/v1/logs/search?source=docker:eeeeeeeeeeee5555&limit=20"+window, "", nil))
	if len(res.Lines) != 2 || res.Lines[1].Event != "disk_full" {
		t.Fatalf("the minute before the exit = %+v; want the start and the FATAL, not the next attempt", res.Lines)
	}

	until := base.Add(35 * time.Millisecond).Format(time.RFC3339Nano)
	merged := decode[logsx.SearchResult](t, c.do("GET", "/api/v1/logs/search?source=stack:shop&lens=none&until="+until, "", nil))
	got := []string{}
	for _, l := range merged.Lines {
		got = append(got, l.Source)
	}
	if strings.Join(got, ",") != "db,worker,web,web,db" {
		t.Errorf("the stack up to 35 ms in = %v; every container is cut at the same instant", got)
	}
}
