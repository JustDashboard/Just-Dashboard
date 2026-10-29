package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// Replicas end to end, against a real daemon and a real Caddy ingress: a
// release of two replicas answers through the ingress from both of them and
// reads as one release of two containers; a second release of two that
// answers 500 is cut over to only once both of its replicas pass readiness,
// keeps the first release's two replicas warm — both paused, and nothing of
// it running — and is rolled back automatically to those same two
// containers, resumed, with both of its own removed.
//
// The ingress is a Caddy of the test's own on a loopback port, named to the
// server (JD_INGRESS_CONTAINER), so nothing binds 80 or 443. Every container
// is the test's and is removed with it.
func TestLiveReplicatedReleaseServesAndRollsBackAsOne(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker host")
	}
	stamp := time.Now().UnixNano()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ingress, endpoint := liveRollbackIngress(t, stamp)
	s := liveRollbackServer(t, ingress)()
	processCtx, stopProcess := context.WithCancel(ctx)
	if err := s.modules.deployEngine.Start(processCtx); err != nil {
		t.Fatal(err)
	}
	s.modules.rollbackWatcher.WithInterval(2 * time.Second).Start(processCtx)
	t.Cleanup(func() {
		stopProcess()
		shutdown, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		_ = s.modules.deployEngine.Shutdown(shutdown)
	})
	environmentBase := stamp%100_000_000 + 800_000_000
	if _, err := s.Store.DB.Exec(`DELETE FROM sqlite_sequence WHERE name = 'deploy_environments'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO sqlite_sequence(name, seq) VALUES('deploy_environments', ?)`, environmentBase); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeEnvironmentContainers(t, environmentBase) })
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "replicas-live", auth.RoleAdmin)}

	domain := fmt.Sprintf("replicas-%d.example.test", stamp)
	// Each replica says which container it is, so the ingress can be seen
	// sharing requests between them.
	healthy := []string{"caddy", "respond", "--listen", ":8080", "--header", "X-Host: {system.hostname}", "release A"}
	broken := []string{"sh", "-c", `printf ':8080 {\n\trespond /health 200\n\trespond "release B is broken" 500\n}\n' > /tmp/Caddyfile && exec caddy run --config /tmp/Caddyfile --adapter caddyfile`}
	projectID, environmentID := liveRollbackProject(t, admin, fmt.Sprintf("replicas-live-%d", stamp%1_000_000), domain, healthy)
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, environmentID)
	liveReplicasConfigure(t, admin, base, healthy, 2)

	first := liveRollbackDeploy(t, admin, projectID, environmentID)
	if res := admin.do(http.MethodPut, base+"/rollback-policy", `{"keepWarm":true,"windowSeconds":60,"automatic":true,"errorRateMargin":5,"latencyFactor":2,"minRequests":20}`, nil); res.Code != http.StatusOK {
		t.Fatalf("rollback policy = %d %s", res.Code, res.Body.String())
	}
	firstIDs := liveReplicaIDs(t, environmentID, 1, 2)
	hosts := map[string]int{}
	for index := 0; index < 60; index++ {
		status, host := ingressGetHost(t, endpoint, domain, "release A")
		if status != http.StatusOK {
			t.Fatalf("release A through the ingress answered %d", status)
		}
		hosts[host]++
	}
	if len(hosts) != 2 {
		t.Fatalf("the ingress did not share release A's requests between its two replicas: %v", hosts)
	}
	t.Logf("release A's replicas answered %v", hosts)
	services := liveRuntimeServices(t, admin, projectID, environmentID)
	if len(services) != 2 || services[0].ReleaseID != first || services[1].ReleaseID != first ||
		services[0].Replicas != 2 || services[0].Replica == services[1].Replica || !services[0].LiveRelease {
		t.Fatalf("release A's inventory = %+v", services)
	}

	liveReplicasConfigure(t, admin, base, broken, 2)
	second := liveRollbackDeploy(t, admin, projectID, environmentID)
	secondSnapshot := liveRunSnapshot(t, admin, projectID, liveRunOf(t, admin, base, second))
	readiness := 0
	for _, step := range secondSnapshot.Steps {
		if step.Key != deploy.StepVerifyReadiness {
			continue
		}
		var evidence struct {
			Checks []deploy.CheckEvidence `json:"checks"`
		}
		_ = json.Unmarshal(step.Evidence, &evidence)
		for _, check := range evidence.Checks {
			if check.Outcome == deploy.HealthPassed && check.Replica > 0 {
				readiness++
			}
		}
	}
	if readiness != 2 {
		t.Fatalf("release B went live on %d replicas' readiness, want both", readiness)
	}
	watch := liveRollbackWatch(t, admin, base)
	if watch == nil || watch.State != deploy.RollbackWatchWatching || watch.WarmReleaseNum != 1 {
		t.Fatalf("after release B: window %+v", watch)
	}
	// Both of A's replicas are paused, the same containers, and what runs is
	// B's two replicas and nothing else: exactly one release live.
	started := map[string]string{}
	for _, id := range firstIDs {
		state := inspectContainer(t, id)
		if !state.State.Paused || !state.State.Running {
			t.Fatalf("release A's replica %s is not paused through B's window: %+v", id[:12], state.State)
		}
		started[id] = state.State.StartedAt
	}
	secondIDs := liveReplicaIDs(t, environmentID, 2, 2)
	if running := ownedRunning(t, environmentID); len(running) != 2 || !sameContainers(running, secondIDs) {
		t.Fatalf("running during the window = %v, want release B's %v", running, secondIDs)
	}

	for index := 0; index < 40; index++ {
		if status, _ := ingressGetHost(t, endpoint, domain, ""); status != http.StatusInternalServerError {
			t.Fatalf("release B through the ingress answered %d", status)
		}
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		watch = liveRollbackWatch(t, admin, base)
		if watch.State == deploy.RollbackWatchRolledBack {
			break
		}
		if time.Now().After(deadline) || watch.State == deploy.RollbackWatchClosed {
			t.Fatalf("no automatic rollback: window %+v", watch)
		}
		time.Sleep(time.Second)
	}
	t.Logf("verdict: %s", watch.Verdict.Reason)
	for _, id := range firstIDs {
		after := inspectContainer(t, id)
		if after.State.StartedAt != started[id] || after.State.Paused || !after.State.Running {
			t.Fatalf("release A's replica %s came back as a restart or stayed paused: %+v", id[:12], after.State)
		}
	}
	for _, id := range secondIDs {
		if containerExists(t, id) {
			t.Fatalf("release B's replica %s outlived the rollback", id[:12])
		}
	}
	hosts = map[string]int{}
	for index := 0; index < 40; index++ {
		status, host := ingressGetHost(t, endpoint, domain, "release A")
		if status != http.StatusOK {
			t.Fatalf("after the rollback the ingress answered %d", status)
		}
		hosts[host]++
	}
	if len(hosts) != 2 {
		t.Fatalf("after the rollback release A answered from %v, want both replicas", hosts)
	}
	if running := ownedRunning(t, environmentID); !sameContainers(running, firstIDs) {
		t.Fatalf("running after the rollback = %v, want release A's %v", running, firstIDs)
	}
}

// liveReplicasConfigure saves the environment's plan with a command and a
// replica count.
func liveReplicasConfigure(t *testing.T, c *client, base string, command []string, replicas int) {
	t.Helper()
	read := c.do(http.MethodGet, base+"/configuration", "", nil)
	var current deploy.EnvironmentConfiguration
	if err := json.Unmarshal(read.Body.Bytes(), &current); err != nil || read.Code != http.StatusOK {
		t.Fatalf("configuration = %d %s", read.Code, read.Body.String())
	}
	current.Runtime.Command, current.Runtime.Replicas = command, replicas
	body, _ := json.Marshal(deploy.ConfigurationWriteRequest{
		Revision: current.Revision, Build: current.Build, Runtime: current.Runtime,
		Dependencies: current.Dependencies, Checks: current.Checks, Domains: current.Domains,
	})
	if res := c.do(http.MethodPut, base+"/configuration", string(body), nil); res.Code != http.StatusOK {
		t.Fatalf("configuration save = %d %s", res.Code, res.Body.String())
	}
}

// liveReplicaIDs are a release's containers by their deterministic names.
func liveReplicaIDs(t *testing.T, environmentID, releaseNumber int64, replicas int) []string {
	t.Helper()
	ids := []string{}
	for replica := 1; replica <= replicas; replica++ {
		name := fmt.Sprintf("jd-e%d-r%d", environmentID, releaseNumber)
		if replica > 1 {
			name = fmt.Sprintf("%s-%d", name, replica)
		}
		ids = append(ids, inspectContainer(t, name).ID)
	}
	return ids
}

func sameContainers(short, full []string) bool {
	if len(short) != len(full) {
		return false
	}
	for _, id := range short {
		found := false
		for _, candidate := range full {
			found = found || strings.HasPrefix(candidate, id)
		}
		if !found {
			return false
		}
	}
	return true
}

// ingressGetHost asks the ingress for the domain and says which container
// answered; want, when set, is the body it must answer with.
func ingressGetHost(t *testing.T, endpoint, host, want string) (int, string) {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, endpoint+"/", nil)
	request.Host = host
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("through the ingress: %v", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if want != "" && strings.TrimSpace(string(body)) != want {
		t.Fatalf("the ingress answered %q, want %q", body, want)
	}
	return response.StatusCode, response.Header.Get("X-Host")
}

// liveRuntimeServices is the project page's runtime inventory.
func liveRuntimeServices(t *testing.T, c *client, projectID, environmentID int64) []deploy.RuntimeService {
	t.Helper()
	res := c.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d?environment=%d", projectID, environmentID), "", nil)
	var body struct {
		Runtime deploy.RuntimeServices `json:"runtime"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || res.Code != http.StatusOK {
		t.Fatalf("project detail = %d %s", res.Code, res.Body.String())
	}
	return body.Runtime.Services
}

// liveRunOf is the run that made a release.
func liveRunOf(t *testing.T, c *client, base string, releaseID int64) int64 {
	t.Helper()
	for _, release := range liveRollbackReleases(t, c, base) {
		if release.ID == releaseID {
			return release.RunID
		}
	}
	t.Fatalf("release %d is not listed", releaseID)
	return 0
}
