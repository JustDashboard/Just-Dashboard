package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// A canary end to end, against a real daemon and a real Caddy ingress: a
// healthy release A; a canary B that passes its readiness check and answers
// 500 to everything else, which takes half of the route's requests and is
// aborted automatically on the side-by-side reading with A's container
// untouched and still serving everything; then a healthy canary C, promoted
// on its own at the end of its window — with the dashboard restarted in the
// middle of that window, which the next process resumes rather than starts
// again — after which C is live and A is kept warm, paused.
//
// The ingress is a Caddy of the test's own on a loopback port, named to the
// server (JD_INGRESS_CONTAINER), so nothing binds 80 or 443. Every container
// is the test's and is removed with it.
func TestLiveCanaryAbortsAFailingReleaseAndPromotesAHealthyOne(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker host")
	}
	stamp := time.Now().UnixNano()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ingress, endpoint := liveRollbackIngress(t, stamp)
	boot := liveRollbackServer(t, ingress)
	start := func() (*Server, context.CancelFunc) {
		s := boot()
		processCtx, stopProcess := context.WithCancel(ctx)
		if err := s.modules.deployEngine.Start(processCtx); err != nil {
			t.Fatal(err)
		}
		s.modules.rollbackWatcher.WithInterval(2 * time.Second).Start(processCtx)
		return s, func() {
			stopProcess()
			shutdown, stop := context.WithTimeout(context.Background(), 30*time.Second)
			defer stop()
			if err := s.modules.deployEngine.Shutdown(shutdown); err != nil {
				t.Errorf("the dashboard did not stop within 30 s: %v", err)
			}
		}
	}
	s, stopFirst := start()
	t.Cleanup(stopFirst)
	environmentBase := stamp%100_000_000 + 700_000_000
	if _, err := s.Store.DB.Exec(`DELETE FROM sqlite_sequence WHERE name = 'deploy_environments'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO sqlite_sequence(name, seq) VALUES('deploy_environments', ?)`, environmentBase); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeEnvironmentContainers(t, environmentBase) })
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "canary-live", auth.RoleAdmin)}

	domain := fmt.Sprintf("canary-%d.example.test", stamp)
	healthy := func(body string) []string { return []string{"caddy", "respond", "--listen", ":8080", body} }
	broken := []string{"sh", "-c", `printf ':8080 {\n\trespond /health 200\n\trespond "release B is broken" 500\n}\n' > /tmp/Caddyfile && exec caddy run --config /tmp/Caddyfile --adapter caddyfile`}
	projectID, environmentID := liveRollbackProject(t, admin, fmt.Sprintf("canary-live-%d", stamp%1_000_000), domain, healthy("release A"))
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, environmentID)
	terms := deploy.CanaryTerms{Percent: 50, DurationSeconds: 60, AutoPromote: true}
	liveCanaryConfigure(t, admin, base, healthy("release A"), terms)

	// The first release has nothing to share the route with: no canary.
	first := liveRollbackDeploy(t, admin, projectID, environmentID)
	if res := admin.do(http.MethodPut, base+"/rollback-policy", `{"keepWarm":true,"windowSeconds":60,"automatic":true,"errorRateMargin":5,"latencyFactor":2,"minRequests":20}`, nil); res.Code != http.StatusOK {
		t.Fatalf("rollback policy = %d %s", res.Code, res.Body.String())
	}
	firstContainer := inspectContainer(t, fmt.Sprintf("jd-e%d-r1", environmentID))
	for index := 0; index < 30; index++ {
		if status, body := ingressGet(t, endpoint, domain); status != http.StatusOK || body != "release A" {
			t.Fatalf("release A through the ingress = %d %q", status, body)
		}
	}

	// B fails half the route's requests while it holds its share.
	liveCanaryConfigure(t, admin, base, broken, terms)
	runB := liveCanaryEnqueue(t, admin, projectID, environmentID)
	liveCanaryOpen(t, admin, base, runB)
	traffic := startCanaryTraffic(t, endpoint, domain)
	snapshotB := liveCanaryWaitRun(t, admin, projectID, runB, 3*time.Minute)
	served := traffic.stop()
	if snapshotB.Run.State != deploy.RunFailed || snapshotB.Run.TerminalCode != "canary_failed" {
		t.Fatalf("release B's run = %s %s: %s", snapshotB.Run.State, snapshotB.Run.TerminalCode, snapshotB.Run.TerminalReason)
	}
	t.Logf("B aborted: %s", snapshotB.Run.TerminalReason)
	if served["release A"] == 0 || served["release B is broken"] == 0 {
		t.Fatalf("the route did not share requests between A and B: %v", served)
	}
	canaryB := liveCanary(t, admin, base)
	if canaryB.State != deploy.CanaryAborted || canaryB.EndReason != deploy.CanaryEndFailed || canaryB.Candidate == nil ||
		canaryB.Candidate.Failures < 5 || canaryB.Live == nil || canaryB.Live.Failures != 0 {
		t.Fatalf("B's canary = %+v", canaryB)
	}
	// A was never touched: the same container, never restarted, serving
	// every request again; B's container is gone.
	if after := inspectContainer(t, firstContainer.ID); after.State.StartedAt != firstContainer.State.StartedAt || !after.State.Running || after.State.Paused {
		t.Fatalf("release A changed during B's canary: %+v / %+v", firstContainer.State, after.State)
	}
	if containerExists(t, fmt.Sprintf("jd-e%d-r2", environmentID)) {
		t.Fatal("release B's container outlived its aborted canary")
	}
	for index := 0; index < 30; index++ {
		if status, body := ingressGet(t, endpoint, domain); status != http.StatusOK || body != "release A" {
			t.Fatalf("after B's canary the ingress answered %d %q", status, body)
		}
	}
	if releases := liveRollbackReleases(t, admin, base); liveReleaseOf(releases) != first {
		t.Fatalf("release history after B = %+v", releases)
	}
	var audited int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_canaries WHERE run_id = ?`, runB).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("B's canaries = %d, %v", audited, err)
	}

	// C is healthy, and the dashboard restarts in the middle of its window.
	liveCanaryConfigure(t, admin, base, healthy("release C"), terms)
	runC := liveCanaryEnqueue(t, admin, projectID, environmentID)
	opened := liveCanaryOpen(t, admin, base, runC)
	traffic = startCanaryTraffic(t, endpoint, domain)
	time.Sleep(10 * time.Second)
	stopFirst()
	s, stopSecond := start()
	t.Cleanup(stopSecond)
	admin = &client{t: t, h: s.Routes(), cookie: admin.cookie}
	snapshotC := liveCanaryWaitRun(t, admin, projectID, runC, 4*time.Minute)
	served = traffic.stop()
	if snapshotC.Run.State != deploy.RunSucceeded {
		t.Fatalf("release C's run = %s %s: %s", snapshotC.Run.State, snapshotC.Run.TerminalCode, snapshotC.Run.TerminalReason)
	}
	canaryC := liveCanary(t, admin, base)
	if canaryC.ID != opened.ID || canaryC.State != deploy.CanaryPromoted || canaryC.EndReason != deploy.CanaryEndPassed ||
		!canaryC.StartedAt.Equal(opened.StartedAt) || !canaryC.EndsAt.Equal(opened.EndsAt) {
		t.Fatalf("C's canary = %+v (opened %+v)", canaryC, opened)
	}
	t.Logf("C promoted: %s; the ingress answered %v during its window", canaryC.EndMessage, served)
	if served["release C"] == 0 || served["release A"] == 0 {
		t.Fatalf("the route did not share requests between A and C: %v", served)
	}
	for index := 0; index < 30; index++ {
		if status, body := ingressGet(t, endpoint, domain); status != http.StatusOK || body != "release C" {
			t.Fatalf("after C's promotion the ingress answered %d %q", status, body)
		}
	}
	// A is kept warm behind C, as after any blue/green cutover.
	if warm := inspectContainer(t, firstContainer.ID); !warm.State.Paused {
		t.Fatalf("release A is not kept warm behind C: %+v", warm.State)
	}
	if watch := liveRollbackWatch(t, admin, base); watch == nil || watch.State != deploy.RollbackWatchWatching || watch.WarmReleaseNum != 1 || watch.ReleaseNumber != 3 {
		t.Fatalf("after C: window %+v", watch)
	}
}

func liveReleaseOf(releases []deploy.Release) int64 {
	for _, release := range releases {
		if release.State == "live" {
			return release.ID
		}
	}
	return 0
}

// liveCanaryConfigure saves the environment's plan with a command and the
// canary strategy on terms.
func liveCanaryConfigure(t *testing.T, c *client, base string, command []string, terms deploy.CanaryTerms) {
	t.Helper()
	read := c.do(http.MethodGet, base+"/configuration", "", nil)
	var current deploy.EnvironmentConfiguration
	if err := json.Unmarshal(read.Body.Bytes(), &current); err != nil || read.Code != http.StatusOK {
		t.Fatalf("configuration = %d %s", read.Code, read.Body.String())
	}
	current.Runtime.Command, current.Runtime.Strategy, current.Runtime.Canary = command, deploy.StrategyCanary, &terms
	body, _ := json.Marshal(deploy.ConfigurationWriteRequest{
		Revision: current.Revision, Build: current.Build, Runtime: current.Runtime,
		Dependencies: current.Dependencies, Checks: current.Checks, Domains: current.Domains,
	})
	if res := c.do(http.MethodPut, base+"/configuration", string(body), nil); res.Code != http.StatusOK {
		t.Fatalf("configuration save = %d %s", res.Code, res.Body.String())
	}
}

func liveCanaryEnqueue(t *testing.T, c *client, projectID, environmentID int64) int64 {
	t.Helper()
	res := c.do(http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/runs", projectID, environmentID), `{"operation":"deploy"}`, nil)
	var run deploy.EngineRun
	if err := json.Unmarshal(res.Body.Bytes(), &run); err != nil || res.Code != http.StatusAccepted {
		t.Fatalf("deploy = %d %s", res.Code, res.Body.String())
	}
	return run.ID
}

func liveCanaryWaitRun(t *testing.T, c *client, projectID, runID int64, limit time.Duration) deploy.RunSnapshot {
	t.Helper()
	deadline := time.Now().Add(limit)
	for {
		snapshot := liveRunSnapshot(t, c, projectID, runID)
		if snapshot.Run.State.Terminal() {
			return snapshot
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %d stayed %s", runID, snapshot.Run.State)
		}
		time.Sleep(time.Second)
	}
}

func liveCanary(t *testing.T, c *client, base string) *deploy.CanaryWindow {
	t.Helper()
	res := c.do(http.MethodGet, base+"/canary", "", nil)
	var body struct {
		Canary *deploy.CanaryWindow `json:"canary"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || res.Code != http.StatusOK {
		t.Fatalf("canary = %d %s", res.Code, res.Body.String())
	}
	if body.Canary == nil {
		t.Fatal("no canary")
	}
	return body.Canary
}

// liveCanaryOpen waits for the run's canary to hold its share of the route.
func liveCanaryOpen(t *testing.T, c *client, base string, runID int64) *deploy.CanaryWindow {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for {
		res := c.do(http.MethodGet, base+"/canary", "", nil)
		var body struct {
			Canary *deploy.CanaryWindow `json:"canary"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &body)
		if body.Canary != nil && body.Canary.RunID == runID && body.Canary.Open() {
			return body.Canary
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %d never opened its canary: %s", runID, res.Body.String())
		}
		time.Sleep(500 * time.Millisecond)
	}
}

type canaryTraffic struct {
	stopping atomic.Bool
	wait     sync.WaitGroup
	mu       sync.Mutex
	bodies   map[string]int
}

// startCanaryTraffic asks the ingress for the domain from four clients until
// stopped, counting what each answer said.
func startCanaryTraffic(t *testing.T, endpoint, domain string) *canaryTraffic {
	traffic := &canaryTraffic{bodies: map[string]int{}}
	for worker := 0; worker < 4; worker++ {
		traffic.wait.Add(1)
		go func() {
			defer traffic.wait.Done()
			client := &http.Client{Timeout: 5 * time.Second}
			for !traffic.stopping.Load() {
				request, _ := http.NewRequest(http.MethodGet, endpoint+"/", nil)
				request.Host = domain
				response, err := client.Do(request)
				body := "error"
				if err == nil {
					raw := make([]byte, 64)
					n, _ := response.Body.Read(raw)
					response.Body.Close()
					body = strings.TrimSpace(string(raw[:n]))
				}
				traffic.mu.Lock()
				traffic.bodies[body]++
				traffic.mu.Unlock()
				time.Sleep(50 * time.Millisecond)
			}
		}()
	}
	return traffic
}

func (c *canaryTraffic) stop() map[string]int {
	c.stopping.Store(true)
	c.wait.Wait()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bodies
}
