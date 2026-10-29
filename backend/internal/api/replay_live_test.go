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

// Traffic replay end to end, against a real daemon and a real Caddy ingress:
// a healthy release A serves thirty pages through the ingress, which records
// them in its own access log. Release B passes its readiness check and
// answers 404 for one of those pages; the run replays the record against B's
// container before the cutover and ends in replay_traffic, with A still
// serving every page and B's container gone. Release C answers like A and
// goes live. The replay never passes through the ingress: the record holds
// exactly the requests the test sent through it.
//
// The ingress is a Caddy of the test's own on a loopback port, named to the
// server (JD_INGRESS_CONTAINER), as the rollback watch's acceptance runs it.
func TestLiveTrafficReplayStopsACandidateThatBreaksARecordedRoute(t *testing.T) {
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
	t.Cleanup(func() {
		shutdown, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		_ = s.modules.deployEngine.Shutdown(shutdown)
	})
	// Numbered far from the rollback acceptance's environments and from a
	// fresh store's, so no release container name is another's.
	environmentBase := stamp%100_000_000 + 800_000_000
	if _, err := s.Store.DB.Exec(`DELETE FROM sqlite_sequence WHERE name = 'deploy_environments'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO sqlite_sequence(name, seq) VALUES('deploy_environments', ?)`, environmentBase); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeEnvironmentContainers(t, environmentBase) })
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "replay-live", auth.RoleAdmin)}

	domain := fmt.Sprintf("replay-%d.example.test", stamp)
	answering := func(body string) []string {
		return []string{"caddy", "respond", "--listen", ":8080", body}
	}
	projectID, environmentID := liveRollbackProject(t, admin, fmt.Sprintf("replay-live-%d", stamp%1_000_000), domain, answering("release A"))
	if environmentID <= environmentBase {
		t.Fatalf("environment %d was not numbered past %d", environmentID, environmentBase)
	}
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, environmentID)
	liveReplayConfigure(t, admin, base, answering("release A"), &deploy.ReplayPlan{Enabled: true})

	// The first release has nothing recorded to be replayed, and says so.
	first := liveReplayDeploy(t, admin, projectID, environmentID)
	if first.Run.State != deploy.RunSucceeded {
		t.Fatalf("release A = %s %s: %s", first.Run.State, first.Run.TerminalCode, first.Run.TerminalReason)
	}
	if step, report := liveReplayStep(t, first); step.State != deploy.StepSkipped || report.Replayed || report.Reason == "" {
		t.Fatalf("release A's replay = %s %+v", step.State, report)
	}

	// Thirty pages, /pricing among them, a HEAD and a write, all through the
	// ingress and so all in its record.
	paths := []string{"/", "/pricing"}
	for index := 1; index <= 28; index++ {
		paths = append(paths, fmt.Sprintf("/docs/%d", index))
	}
	for _, path := range paths {
		if status, body := ingressRequest(t, endpoint, domain, http.MethodGet, path); status != http.StatusOK || body != "release A" {
			t.Fatalf("release A through the ingress at %s = %d %q", path, status, body)
		}
	}
	for _, request := range [][2]string{{http.MethodHead, "/"}, {http.MethodPost, "/api/orders"}, {http.MethodGet, "/pricing"}} {
		if status, _ := ingressRequest(t, endpoint, domain, request[0], request[1]); status != http.StatusOK {
			t.Fatalf("release A through the ingress: %s %s = %d", request[0], request[1], status)
		}
	}
	sent := len(paths) + 3
	recorded := liveReplayRecordHolds(t, admin, projectID, environmentID, sent)

	// B passes its readiness check and lost /pricing.
	broken := []string{"sh", "-c", `printf ':8080 {\n\trespond /health 200\n\trespond /pricing "gone" 404\n\trespond "release B" 200\n}\n' > /tmp/Caddyfile && exec caddy run --config /tmp/Caddyfile --adapter caddyfile`}
	liveReplayConfigure(t, admin, base, broken, &deploy.ReplayPlan{Enabled: true})
	second := liveReplayDeploy(t, admin, projectID, environmentID)
	if second.Run.State != deploy.RunFailed || second.Run.TerminalCode != "replay_status_drift" {
		t.Fatalf("release B = %s %s: %s", second.Run.State, second.Run.TerminalCode, second.Run.TerminalReason)
	}
	step, report := liveReplayStep(t, second)
	t.Logf("release B: %s", step.ErrorMessage)
	if step.State != deploy.StepFailed || !report.Replayed || report.Requests != len(paths)+1 || report.Answered != report.Requests ||
		report.Drifted != 1 || len(report.Drifts) != 1 {
		t.Fatalf("release B's replay = %s %+v", step.State, report)
	}
	if drift := report.Drifts[0]; drift.Method != http.MethodGet || drift.Path != "/pricing" || drift.RecordedStatus != 200 || drift.CandidateStatus != 404 {
		t.Fatalf("drift = %+v", drift)
	}
	if report.Recovery == nil || !report.Recovery.CandidateStopped || !strings.Contains(step.ErrorMessage, "live release was not touched") {
		t.Fatalf("recovery = %+v: %s", report.Recovery, step.ErrorMessage)
	}
	for _, later := range second.Steps {
		if later.Key == deploy.StepActivate && later.State == deploy.StepPassed {
			t.Fatal("a release that failed its replay was cut over")
		}
	}
	// A still answers every page, /pricing included, and is the only
	// release running; B's container is gone.
	for _, path := range []string{"/", "/pricing", "/docs/7"} {
		if status, body := ingressRequest(t, endpoint, domain, http.MethodGet, path); status != http.StatusOK || body != "release A" {
			t.Fatalf("after release B's replay the ingress answered %s with %d %q", path, status, body)
		}
	}
	if running := ownedRunning(t, environmentID); len(running) != 1 {
		t.Fatalf("running release containers after the stopped release = %v", running)
	}
	if containerExists(t, fmt.Sprintf("jd-e%d-r2", environmentID)) {
		t.Fatal("release B's container outlived its replay")
	}
	// The replay went to B's own address: the ingress recorded nothing but
	// the test's own requests and the three just sent.
	if after := liveReplayRecordHolds(t, admin, projectID, environmentID, recorded+3); after != recorded+3 {
		t.Fatalf("the record holds %d requests after the replay, want %d: the replay went through the ingress", after, recorded+3)
	}

	// C answers like A, passes its replay and goes live.
	liveReplayConfigure(t, admin, base, answering("release C"), &deploy.ReplayPlan{Enabled: true})
	third := liveReplayDeploy(t, admin, projectID, environmentID)
	if third.Run.State != deploy.RunSucceeded {
		t.Fatalf("release C = %s %s: %s", third.Run.State, third.Run.TerminalCode, third.Run.TerminalReason)
	}
	step, report = liveReplayStep(t, third)
	t.Logf("release C: %s", report.Verdict.Reason)
	if step.State != deploy.StepPassed || report.Requests != len(paths)+1 || report.Drifted != 0 || report.Verdict.Tripped {
		t.Fatalf("release C's replay = %s %+v", step.State, report)
	}
	if status, body := ingressRequest(t, endpoint, domain, http.MethodGet, "/pricing"); status != http.StatusOK || body != "release C" {
		t.Fatalf("release C through the ingress = %d %q", status, body)
	}
}

// liveReplayConfigure saves the environment's next revision with the command
// and replay terms given, keeping everything else.
func liveReplayConfigure(t *testing.T, c *client, base string, command []string, replay *deploy.ReplayPlan) {
	t.Helper()
	read := c.do(http.MethodGet, base+"/configuration", "", nil)
	var current deploy.EnvironmentConfiguration
	if err := json.Unmarshal(read.Body.Bytes(), &current); err != nil || read.Code != http.StatusOK {
		t.Fatalf("configuration = %d %s", read.Code, read.Body.String())
	}
	current.Runtime.Command, current.Runtime.Replay = command, replay
	body, _ := json.Marshal(deploy.ConfigurationWriteRequest{
		Revision: current.Revision, Build: current.Build, Runtime: current.Runtime,
		Dependencies: current.Dependencies, Checks: current.Checks, Domains: current.Domains,
	})
	if res := c.do(http.MethodPut, base+"/configuration", string(body), nil); res.Code != http.StatusOK {
		t.Fatalf("configuration save = %d %s", res.Code, res.Body.String())
	}
}

// liveReplayDeploy runs a deploy to its end, however it ends.
func liveReplayDeploy(t *testing.T, c *client, projectID, environmentID int64) deploy.RunSnapshot {
	t.Helper()
	res := c.do(http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/runs", projectID, environmentID), `{"operation":"deploy"}`, nil)
	var run deploy.EngineRun
	if err := json.Unmarshal(res.Body.Bytes(), &run); err != nil || res.Code != http.StatusAccepted {
		t.Fatalf("deploy = %d %s", res.Code, res.Body.String())
	}
	deadline := time.Now().Add(4 * time.Minute)
	for {
		snapshot := liveRunSnapshot(t, c, projectID, run.ID)
		if snapshot.Run.State.Terminal() {
			return snapshot
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %d stayed %s", run.ID, snapshot.Run.State)
		}
		time.Sleep(time.Second)
	}
}

func liveReplayStep(t *testing.T, snapshot deploy.RunSnapshot) (deploy.RunStep, deploy.ReplayReport) {
	t.Helper()
	for _, step := range snapshot.Steps {
		if step.Key != deploy.StepReplayTraffic {
			continue
		}
		var report deploy.ReplayReport
		if err := json.Unmarshal(step.Evidence, &report); err != nil {
			t.Fatalf("replay evidence %s: %v", step.Evidence, err)
		}
		return step, report
	}
	t.Fatalf("run %d has no replay_traffic step: %+v", snapshot.Run.ID, snapshot.Steps)
	return deploy.RunStep{}, deploy.ReplayReport{}
}

// liveReplayRecordHolds waits for the environment's request record to hold
// at least want requests and returns how many it holds.
func liveReplayRecordHolds(t *testing.T, c *client, projectID, environmentID int64, want int) int {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		res := c.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/requests?environment=%d", projectID, environmentID), "", nil)
		var window struct {
			Status  string `json:"status"`
			Reason  string `json:"reason"`
			Summary struct {
				Total int `json:"total"`
			} `json:"summary"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &window); err != nil || res.Code != http.StatusOK {
			t.Fatalf("requests = %d %s", res.Code, res.Body.String())
		}
		if window.Summary.Total >= want {
			return window.Summary.Total
		}
		if time.Now().After(deadline) {
			t.Fatalf("the record holds %d requests, want %d (%s: %s)", window.Summary.Total, want, window.Status, window.Reason)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func ingressRequest(t *testing.T, endpoint, host, method, path string) (int, string) {
	t.Helper()
	request, _ := http.NewRequest(method, endpoint+path, nil)
	request.Host = host
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("through the ingress: %v", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response.StatusCode, strings.TrimSpace(string(body))
}
