package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/gorilla/websocket"
)

// The Events view against a real daemon, end to end: real containers and a real
// network, labelled the way the runtime owner labels what it creates, read back
// through the real routes with the real middleware in front of them.
//
// The unit tests beside this one hold the rules — the owner filter, the window,
// the directional correlation. What they cannot hold is the link between the
// two halves: Docker puts an object's labels in the event's actor attributes,
// and every reading on this page depends on that being true. Nothing in the
// dashboard's own code would notice if it stopped being.
//
// It creates only its own containers and its own network, and removes them.
func TestLiveDeploymentEventsAreReadFromDockerAndNamedWithTheirCause(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker host")
	}
	s := testServer(t)
	s.modules.docker = dockerx.New("unix:///var/run/docker.sock")
	s.modules.dockerEvents = s.modules.docker.NewEventLog(s.Log)
	c := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "lifecycle-live", auth.RoleAdmin)}

	projectID, environmentID := seedDeployment(t, s, "lifecycle-live-project")

	// Follow before anything happens: the buffer is only ever what arrived
	// while somebody was listening, which is the whole shape of this feature.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s.modules.dockerEvents.Start(ctx)
	waitFor(t, "the event stream to connect", func() bool {
		running, _, _ := s.modules.dockerEvents.Status()
		return running
	})

	// The audit entry goes in first and dated a minute back: a release is
	// written down when the button is pressed and its containers appear after
	// the build, and the correlation only accepts that order.
	s.Audit.Record(t.Context(), audit.Entry{
		TS: time.Now().Add(-time.Minute), Action: "deploy.run",
		Target: "lifecycle-live-project", Username: "wayy", Success: true,
	})

	stamp := time.Now().UnixNano()
	container := fmt.Sprintf("jd-live-events-%d", stamp)
	// Named the way the database-network owner names one, because that name is
	// the only thing a network event carries that identifies it: the daemon
	// sends `name` and `type` for a network and never its labels.
	network := fmt.Sprintf("jd-e%d-db-%012x", environmentID, stamp&0xffffffffffff)
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", container).Run()
		_ = exec.Command("docker", "network", "rm", network).Run()
	})

	label := func(key, value string) string { return "--label=io.just-dashboard." + key + "=" + value }
	env := label("environment-id", fmt.Sprint(environmentID))
	// Labelled as the owner labels it, which the feed must not depend on.
	run(t, "docker", "network", "create", label("managed", "true"), env, network)
	run(t, "docker", "network", "rm", network)
	// Exits 137 of its own accord, which is the reading the page exists for:
	// a container that is silent because it died, not because it logs nothing.
	run(t, "docker", "run", "-d", "--name", container,
		label("managed", "true"), env, label("release-id", "1"),
		label("release-number", "7"), label("run-id", "42"),
		"alpine:3.20", "sh", "-c", "sleep 1; exit 137")

	var feed deploymentLifecycle
	waitFor(t, "the exit to reach the record", func() bool {
		feed = readLifecycle(t, c, projectID, "")
		return hasAction(feed.Events, "die")
	})

	if feed.Status != "available" || !feed.Watching {
		t.Fatalf("the feed should report itself available and connected: %+v", feed)
	}

	died := find(t, feed.Events, "die")
	if died.ExitCode != "137" {
		t.Errorf("the exit code is the one fact that changes what you do next, got %q", died.ExitCode)
	}
	if !strings.Contains(died.Message, container) || !strings.Contains(died.Message, "137") {
		t.Errorf("the event should already be a sentence, got %q", died.Message)
	}
	if died.Owner["environment-id"] != fmt.Sprint(environmentID) || died.Owner["run-id"] != "42" {
		t.Fatalf("the dashboard's labels did not survive the daemon's event: %+v", died.Owner)
	}

	// Created by a release, so the row can say so and offer the entry.
	created := find(t, feed.Events, "create")
	if created.Trigger == nil {
		t.Fatal("a release a minute before its container was created explains it")
	}
	if created.Trigger.Action != "deploy.run" || created.Source != "dashboard" {
		t.Errorf("the wrong cause was offered: source %q, %+v", created.Source, created.Trigger)
	}
	// Nobody asked for the exit. It is the container falling over, and saying
	// the dashboard did it would send an operator to the audit log for nothing.
	if died.Trigger != nil {
		t.Errorf("an exit is not a release's doing: %+v", died.Trigger)
	}

	// A deployment owns more than its containers, and a network disappearing
	// under a running release is exactly this feed's business. It is also the
	// one kind that arrives with no labels at all, so this is the assertion
	// that would have caught the default kinds being widened to something the
	// owner filter could never match.
	destroyed := find(t, feed.Events, "destroy")
	if destroyed.Type != "network" || destroyed.Name != network {
		t.Fatalf("the deployment's own network is missing from the default reading: %+v", destroyed)
	}
	if len(destroyed.Owner) != 0 {
		t.Errorf("a network event is not expected to carry labels; if it now does, the name fallback can go: %+v", destroyed.Owner)
	}
	if containersOnly := readLifecycle(t, c, projectID, "kinds=container"); hasKind(containersOnly.Events, "network") {
		t.Error("kinds=container still returned a network event")
	}
	narrowed := readLifecycle(t, c, projectID, "search=exited")
	if len(narrowed.Events) == 0 || !hasAction(narrowed.Events, "die") {
		t.Errorf("the search should keep the exit, got %d events", len(narrowed.Events))
	}
	if hasAction(narrowed.Events, "create") {
		t.Error("the search should have dropped the creation")
	}
	if future := readLifecycle(t, c, projectID, "since="+time.Now().Add(time.Hour).Format(time.RFC3339)); len(future.Events) != 0 {
		t.Errorf("a window starting in an hour holds nothing, got %d", len(future.Events))
	}

	// And the same slice, followed rather than polled.
	live := httptest.NewServer(s.Routes())
	t.Cleanup(live.Close)
	conn := dial(t, live, c.cookie, fmt.Sprintf("/api/v1/deploy/%d/lifecycle/stream", projectID))
	defer conn.Close()

	second := fmt.Sprintf("jd-live-events-b-%d", stamp)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", second).Run() })
	run(t, "docker", "run", "-d", "--name", second,
		label("managed", "true"), env, "alpine:3.20", "sh", "-c", "sleep 1")

	deadline := time.Now().Add(45 * time.Second)
	seen := false
	for !seen && time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(deadline)
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("the lifecycle socket closed before the container it was opened for: %v", err)
		}
		var envelope struct {
			Type string          `json:"type"`
			Data []dockerx.Event `json:"data"`
		}
		if json.Unmarshal(raw, &envelope) != nil || envelope.Type != "events" {
			continue
		}
		for _, event := range envelope.Data {
			// Every frame this socket sends belongs to this environment: it is
			// the filter that makes it a deployment's feed rather than the
			// host's, and it is the same test the polled reading applies.
			if !ownsEvent(event, environmentID) {
				t.Fatalf("an event for another environment arrived on this socket: %s %s %q %+v",
					event.Type, event.Action, event.Name, event.Owner)
			}
			if event.Name == second {
				seen = true
			}
		}
	}
	if !seen {
		t.Fatal("the socket never carried the container started while it was open")
	}
}

// seedDeployment writes the two rows the read model needs to resolve a project
// to the environment its containers are labelled with. Everything else the
// fleet query reads is an outer join.
func seedDeployment(t *testing.T, s *Server, name string) (projectID, environmentID int64) {
	t.Helper()
	now := time.Now().Unix()
	project, err := s.Store.DB.ExecContext(t.Context(),
		`INSERT INTO deploy_projects(name, profile, repo_path, branch, compose_file, pre_command, post_command,
		                            hook_secret, hook_id, enabled, created_at, updated_at)
		 VALUES(?, 'web', '', 'main', '', '', '', '', '', 1, ?, ?)`, name, now, now)
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ = project.LastInsertId()
	environment, err := s.Store.DB.ExecContext(t.Context(),
		`INSERT INTO deploy_environments(project_id, name, slug, kind, strategy, created_at, updated_at)
		 VALUES(?, 'production', 'production', 'production', 'recreate', ?, ?)`, projectID, now, now)
	if err != nil {
		t.Fatal(err)
	}
	environmentID, _ = environment.LastInsertId()
	return projectID, environmentID
}

func readLifecycle(t *testing.T, c *client, projectID int64, query string) deploymentLifecycle {
	t.Helper()
	path := fmt.Sprintf("/api/v1/deploy/%d/lifecycle?limit=200", projectID)
	if query != "" {
		path += "&" + query
	}
	w := c.do(http.MethodGet, path, "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, w.Code, w.Body.String())
	}
	var feed deploymentLifecycle
	if err := json.Unmarshal(w.Body.Bytes(), &feed); err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return feed
}

func dial(t *testing.T, server *httptest.Server, cookie, path string) *websocket.Conn {
	t.Helper()
	// No Origin header: this is a script rather than a browser, which is the
	// one case the upgrader lets through without one.
	conn, resp, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http")+path,
		http.Header{"Cookie": {cookie}},
	)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial %s: %v (status %d)", path, err, status)
	}
	return conn
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func hasAction(events []dockerx.Event, action string) bool {
	for _, event := range events {
		if event.Action == action {
			return true
		}
	}
	return false
}

func hasKind(events []dockerx.Event, kind string) bool {
	for _, event := range events {
		if event.Type == kind {
			return true
		}
	}
	return false
}

func find(t *testing.T, events []dockerx.Event, action string) dockerx.Event {
	t.Helper()
	for _, event := range events {
		if event.Action == action {
			return event
		}
	}
	t.Fatalf("no %q event in the feed: %+v", action, events)
	return dockerx.Event{}
}
