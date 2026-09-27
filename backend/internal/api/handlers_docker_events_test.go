package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/gorilla/websocket"
)

const (
	eventsWebID = "aaaaaaaaaaaa1111aaaaaaaaaaaa1111aaaaaaaaaaaa1111aaaaaaaaaaaa1111"
	eventsDBID  = "bbbbbbbbbbbb2222bbbbbbbbbbbb2222bbbbbbbbbbbb2222bbbbbbbbbbbb2222"
)

// engineEvent is one message as the Engine's /events stream writes it: the
// object's labels are in the actor's attributes, which is where the stack
// is read from.
func engineEvent(at time.Time, kind, action, id string, attrs map[string]string) map[string]any {
	return map[string]any{
		"Type": kind, "Action": action, "scope": "local",
		"Actor":    map[string]any{"ID": id, "Attributes": attrs},
		"time":     at.Unix(),
		"timeNano": at.UnixNano(),
	}
}

func shopContainerEvent(at time.Time, action, id, name string, extra ...string) map[string]any {
	attrs := map[string]string{"name": name, "image": "postgres:17", "com.docker.compose.project": "shop"}
	for i := 0; i+1 < len(extra); i += 2 {
		attrs[extra[i]] = extra[i+1]
	}
	return engineEvent(at, "container", action, id, attrs)
}

// serveFakeEventEngine points the server at an Engine whose event stream
// opens with `past` and then writes whatever is sent on the returned
// channel, and starts the recorder on it as the server does at boot.
func serveFakeEventEngine(t *testing.T, s *Server, past ...map[string]any) chan<- map[string]any {
	t.Helper()
	more := make(chan map[string]any, 8)
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_ping" {
			w.Header().Set("API-Version", "1.47")
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/events") {
			t.Errorf("unexpected Docker request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		for _, ev := range past {
			_ = enc.Encode(ev)
		}
		w.(http.Flusher).Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case ev := <-more:
				_ = enc.Encode(ev)
				w.(http.Flusher).Flush()
			}
		}
	}))
	t.Cleanup(engine.Close)
	s.modules.docker = dockerx.New(engine.URL)
	t.Cleanup(func() { _ = s.modules.docker.Close() })
	s.modules.dockerEvents = s.modules.docker.NewEventLog(s.Log)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s.modules.dockerEvents.Start(ctx)
	deadline := time.Now().Add(10 * time.Second)
	for len(s.modules.dockerEvents.Recent(len(past)+1, nil, "")) < len(past) {
		if time.Now().After(deadline) {
			t.Fatal("the recorder never read the engine's events")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return more
}

func shopEventHistory(base time.Time) []map[string]any {
	at := func(s int) time.Time { return base.Add(time.Duration(s) * time.Second) }
	return []map[string]any{
		shopContainerEvent(at(0), "start", eventsWebID, "shop-web-1"),
		shopContainerEvent(at(1), "start", eventsDBID, "shop-db-1"),
		shopContainerEvent(at(2), "die", eventsDBID, "shop-db-1", "exitCode", "1"),
		shopContainerEvent(at(3), "start", eventsDBID, "shop-db-1"),
		engineEvent(at(4), "container", "start", "cccc", map[string]string{"name": "blog-web-1", "com.docker.compose.project": "blog"}),
		engineEvent(at(5), "image", "pull", "nginx:1.27", map[string]string{"name": "nginx:1.27"}),
	}
}

func eventNames(t *testing.T, body []byte) []string {
	t.Helper()
	var feed struct {
		Events []dockerx.Event `json:"events"`
	}
	if err := json.Unmarshal(body, &feed); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	out := []string{}
	for _, ev := range feed.Events {
		out = append(out, ev.Name+" "+ev.Action)
	}
	return out
}

// A container's page reads its own events and a stack's page its project's,
// out of the host's one buffer, and a parameter that could not name either is
// refused rather than read as "everything".
func TestDockerEventsNarrowToAContainerOrAStack(t *testing.T) {
	s := testServer(t)
	serveFakeEventEngine(t, s, shopEventHistory(time.Now().Add(-time.Minute))...)
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}

	cases := []struct {
		query string
		want  []string
	}{
		{"container=" + eventsDBID, []string{"shop-db-1 start", "shop-db-1 die", "shop-db-1 start"}},
		{"container=" + eventsDBID[:12], []string{"shop-db-1 start", "shop-db-1 die", "shop-db-1 start"}},
		{"container=shop-web-1", []string{"shop-web-1 start"}},
		{"stack=shop&limit=2", []string{"shop-db-1 start", "shop-db-1 die"}},
		{"stack=shop&search=exited", []string{"shop-db-1 die"}},
		{"stack=blog", []string{"blog-web-1 start"}},
		// Unchanged for the host feed, which asks neither.
		{"kinds=image", []string{"nginx:1.27 pull"}},
	}
	for _, tc := range cases {
		rec := c.do(http.MethodGet, "/api/v1/docker/events?"+tc.query, "", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", tc.query, rec.Code, rec.Body)
		}
		if got := eventNames(t, rec.Body.Bytes()); !slices.Equal(got, tc.want) {
			t.Errorf("%s = %v, want %v", tc.query, got, tc.want)
		}
	}

	for _, query := range []string{"container=../etc", "container=" + url.QueryEscape("a b"), "stack=Shop", "stack=" + url.QueryEscape("-x")} {
		if rec := c.do(http.MethodGet, "/api/v1/docker/events?"+query, "", nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", query, rec.Code)
		}
	}
}

// The stream answers the same question: the buffered past of one container,
// then only what happens to it — another container's exit a moment earlier
// never reaches the page.
func TestDockerEventStreamFollowsOneContainer(t *testing.T) {
	s := testServer(t)
	more := serveFakeEventEngine(t, s, shopEventHistory(time.Now().Add(-time.Minute))...)
	cookie := signIn(t, s)
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()
	base := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/docker/events/stream?"

	if _, resp, err := websocket.DefaultDialer.Dial(base+"stack=Not_A_Project", http.Header{"Cookie": {cookie}}); err == nil || resp == nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a bad stack was not refused before the upgrade: %v %v", resp, err)
	}

	conn, resp, err := websocket.DefaultDialer.Dial(base+"container="+eventsDBID[:12], http.Header{"Cookie": {cookie}})
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("could not open the stream (%d): %v", status, err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	read := func() []string {
		kind, data := readFrame(t, conn)
		if kind != "events" {
			t.Fatalf("frame %s %s", kind, data)
		}
		var batch []dockerx.Event
		if err := json.Unmarshal(data, &batch); err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, ev := range batch {
			out = append(out, ev.Name+" "+ev.Action)
		}
		return out
	}
	if got := read(); !slices.Equal(got, []string{"shop-db-1 start", "shop-db-1 die", "shop-db-1 start"}) {
		t.Fatalf("the buffered past = %v", got)
	}

	// The subscription is taken after the past is sent; give it a moment so
	// the live events below are not recorded before anybody is listening.
	time.Sleep(100 * time.Millisecond)
	now := time.Now()
	more <- shopContainerEvent(now, "die", eventsWebID, "shop-web-1", "exitCode", "137")
	more <- shopContainerEvent(now.Add(time.Second), "oom", eventsDBID, "shop-db-1")
	if got := read(); !slices.Equal(got, []string{"shop-db-1 oom"}) {
		t.Fatalf("the first live frame = %v, want only the database's", got)
	}
}
