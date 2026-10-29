package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// Scale to zero end to end, against a real daemon and a real Caddy ingress:
// a release that sleeps serves a request through the ingress, which records
// it; once the plan's idle minutes pass with nothing else, the sleeper's pass
// stops its container — stopped, not removed — and the next request through
// the ingress is held while the dashboard's own wake, over the socket in the
// ingress's volume, starts it and runs its readiness check, then answered
// with the application's page rather than a 502. The idle decision, the
// sleep and the wake are audited and are what the lifecycle feed attributes
// the stop and the start to. Then Wake now does the same by hand.
//
// The idle minutes are measured on a clock the test moves; the requests and
// the container's events are real.
func TestLiveSleepingDeploymentWakesOnTheFirstRequest(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker host")
	}
	stamp := time.Now().UnixNano()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	ingress, endpoint := liveRollbackIngress(t, stamp)
	s := liveRollbackServer(t, ingress)()
	if err := s.modules.deployEngine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	// The container events the lifecycle feed reads, as a running dashboard
	// follows them.
	s.modules.dockerEvents.Start(ctx)
	s.modules.deployEvents.Follow(ctx, s.modules.dockerEvents)
	t.Cleanup(func() {
		shutdown, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		_ = s.modules.deployEngine.Shutdown(shutdown)
	})
	environmentBase := stamp%100_000_000 + 900_000_000
	if _, err := s.Store.DB.Exec(`DELETE FROM sqlite_sequence WHERE name = 'deploy_environments'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO sqlite_sequence(name, seq) VALUES('deploy_environments', ?)`, environmentBase); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeEnvironmentContainers(t, environmentBase) })
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "sleep-live", auth.RoleAdmin)}

	domain := fmt.Sprintf("sleep-%d.example.test", stamp)
	command := []string{"caddy", "respond", "--listen", ":8080", "release A"}
	projectID, environmentID := liveRollbackProject(t, admin, fmt.Sprintf("sleep-live-%d", stamp%1_000_000), domain, command)
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, environmentID)
	read := admin.do(http.MethodGet, base+"/configuration", "", nil)
	var current deploy.EnvironmentConfiguration
	if err := json.Unmarshal(read.Body.Bytes(), &current); err != nil || read.Code != http.StatusOK {
		t.Fatalf("configuration = %d %s", read.Code, read.Body.String())
	}
	current.Runtime.Sleep = &deploy.SleepPlan{Enabled: true, IdleMinutes: 5, WakeTimeoutSeconds: 30}
	body, _ := json.Marshal(deploy.ConfigurationWriteRequest{
		Revision: current.Revision, Build: current.Build, Runtime: current.Runtime,
		Dependencies: current.Dependencies, Checks: current.Checks, Domains: current.Domains,
	})
	if res := admin.do(http.MethodPut, base+"/configuration", string(body), nil); res.Code != http.StatusOK {
		t.Fatalf("configuration save = %d %s", res.Code, res.Body.String())
	}
	liveRollbackDeploy(t, admin, projectID, environmentID)
	running := ownedRunning(t, environmentID)
	if len(running) != 1 {
		t.Fatalf("running release containers = %v", running)
	}
	container := running[0]

	// The real clock, moved on by what the test has skipped.
	var clockMu sync.Mutex
	skipped := time.Duration(0)
	sleeper := s.modules.sleeper.WithClock(func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return time.Now().Add(skipped)
	})
	advance := func(by time.Duration) {
		clockMu.Lock()
		skipped += by
		clockMu.Unlock()
	}
	sleeper.Listen(ctx)
	t.Cleanup(sleeper.Close)
	status := func() deploy.SleepStatus {
		t.Helper()
		res := admin.do(http.MethodGet, base+"/sleep", "", nil)
		var out deploy.SleepStatus
		if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil || res.Code != http.StatusOK {
			t.Fatalf("sleep = %d %s", res.Code, res.Body.String())
		}
		return out
	}
	get := func(path string) (int, string, time.Duration) {
		t.Helper()
		request, _ := http.NewRequest(http.MethodGet, endpoint+path, nil)
		request.Host = domain
		started := time.Now()
		response, err := (&http.Client{Timeout: 60 * time.Second}).Do(request)
		if err != nil {
			t.Fatalf("through the ingress: %v", err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		elapsed := time.Since(started)
		t.Logf("%-30s → %d in %5dms  %s", "GET "+path, response.StatusCode, elapsed.Milliseconds(), strings.TrimSpace(string(raw)))
		return response.StatusCode, strings.TrimSpace(string(raw)), elapsed
	}

	if code, body, _ := get("/pricing"); code != 200 || body != "release A" {
		t.Fatalf("before = %d %q", code, body)
	}
	// The record has to hold the request before the pass reads it.
	time.Sleep(2 * time.Second)
	sleeper.Tick(ctx)
	if state := status(); state.State != "awake" || state.SleepsAt == nil || state.LastRequestAt == nil {
		t.Fatalf("awake = %+v", state)
	}
	if !inspectContainer(t, container).State.Running {
		t.Fatal("a pass stopped an application with a recent request")
	}

	advance(6 * time.Minute)
	sleeper.Tick(ctx)
	asleep := status()
	t.Logf("asleep: %s", asleep.Reason)
	if asleep.State != "asleep" || asleep.Since == nil || asleep.Switch == nil || !asleep.Switch.On || !asleep.Switch.Ready {
		t.Fatalf("after the idle minutes = %+v", asleep)
	}
	if inspected := inspectContainer(t, container); inspected.State.Running {
		t.Fatal("the idle container is still running")
	}
	if !containerExists(t, container) {
		t.Fatal("the sleeping container was removed")
	}

	code, answer, elapsed := get("/first-after-sleep")
	if code != 200 || answer != "release A" {
		t.Fatalf("the first request after sleep = %d %q", code, answer)
	}
	t.Logf("the first request after sleep waited %s for the wake", elapsed.Round(100*time.Millisecond))
	if !inspectContainer(t, container).State.Running {
		t.Fatal("the request did not start the container")
	}
	awake := status()
	for deadline := time.Now().Add(15 * time.Second); awake.State == "waking" && time.Now().Before(deadline); awake = status() {
		time.Sleep(100 * time.Millisecond)
	}
	if awake.State != "awake" || len(awake.Recent) != 1 || awake.Recent[0].WakeTrigger != deploy.WakeByRequest ||
		awake.Recent[0].WakeDetail != domain || awake.Recent[0].WakeError != "" || awake.Switch == nil || awake.Switch.On {
		t.Fatalf("after the wake = %+v", awake)
	}
	if code, body, _ := get("/next"); code != 200 || body != "release A" {
		t.Fatalf("after the wake = %d %q", code, body)
	}

	entries, _, err := s.Audit.List(ctx, audit.Filter{ActionPrefixes: []string{"deploy.environment."}, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	actions := []string{}
	for index := len(entries) - 1; index >= 0; index-- {
		if entries[index].Success {
			actions = append(actions, entries[index].Action)
		}
	}
	if strings.Join(actions, ",") != "deploy.environment.idle,deploy.environment.sleep,deploy.environment.wake" {
		t.Fatalf("audited = %v", actions)
	}

	// The lifecycle feed says the dashboard stopped and started it, and why.
	feed := admin.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/lifecycle?limit=50", projectID), "", nil)
	var lifecycle struct {
		Events []dockerx.Event `json:"events"`
	}
	if err := json.Unmarshal(feed.Body.Bytes(), &lifecycle); err != nil || feed.Code != http.StatusOK {
		t.Fatalf("lifecycle = %d %s", feed.Code, feed.Body.String())
	}
	attributed := map[string]string{}
	for _, event := range lifecycle.Events {
		if strings.HasPrefix(event.ID, container) && event.Trigger != nil {
			attributed[event.Action] = event.Trigger.Action
		}
	}
	if attributed["kill"] != "deploy.environment.idle" && attributed["stop"] != "deploy.environment.idle" {
		t.Fatalf("the stop is not the idle decision's: %v", attributed)
	}
	if attributed["start"] != "deploy.environment.wake" {
		t.Fatalf("the start is not the wake's: %v", attributed)
	}

	// Wake now.
	advance(20 * time.Minute)
	sleeper.Tick(ctx)
	if state := status(); state.State != "asleep" || inspectContainer(t, container).State.Running {
		t.Fatalf("asleep again = %+v", state)
	}
	pressed := admin.do(http.MethodPost, base+"/sleep/wake", "{}", nil)
	var woke struct {
		Outcome deploy.WakeOutcome `json:"outcome"`
		Sleep   deploy.SleepStatus `json:"sleep"`
	}
	if err := json.Unmarshal(pressed.Body.Bytes(), &woke); err != nil || pressed.Code != http.StatusOK ||
		woke.Sleep.State != "awake" || woke.Sleep.Recent[0].WakeTrigger != deploy.WakeByHand || !inspectContainer(t, container).State.Running {
		t.Fatalf("wake now = %d %s", pressed.Code, pressed.Body.String())
	}
	t.Logf("wake now: %s", woke.Outcome.Reason)
	if again := admin.do(http.MethodPost, base+"/sleep/wake", "{}", nil); again.Code != http.StatusConflict {
		t.Fatalf("waking an awake environment = %d %s", again.Code, again.Body.String())
	}
}
