package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/config"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

// Metric-gated rollback end to end, against a real daemon and a real Caddy
// ingress: a healthy release A, then a release B that passes its readiness
// check and answers 500 to everything else. Requests go through the
// ingress, the watcher reads them out of Caddy's own access log, and traffic
// goes back to A's container — the same container, paused through B's window
// and resumed, never recreated — with the outcome delivered to a webhook.
// The dashboard is restarted in the middle of B's window. Then the clean
// case: a healthy release C stays, and A's container is removed when the
// window passes.
//
// The ingress is a Caddy of the test's own, published on a loopback port and
// named to the server (JD_INGRESS_CONTAINER), so nothing binds 80 or 443 and
// the host's own proxy is never consulted. Every container is the test's and
// is removed with it.
func TestLiveMetricGatedRollbackSwitchesBackToTheWarmRelease(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker host")
	}
	stamp := time.Now().UnixNano()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	ingress, endpoint := liveRollbackIngress(t, stamp)
	hooks := &rollbackHookRecorder{}
	receiver := httptest.NewServer(hooks)
	t.Cleanup(receiver.Close)

	boot := liveRollbackServer(t, ingress)
	// start is a dashboard process over the store: its engine and its
	// watcher. Stopping one and starting another over the same store is a
	// restart, which is what a window has to survive.
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
			_ = s.modules.deployEngine.Shutdown(shutdown)
		}
	}
	s, stopFirst := start()
	t.Cleanup(stopFirst)
	// Release containers are named after their environment's id; a fresh
	// store would start at 1 and could take a name another test or the live
	// dashboard is using on this daemon.
	environmentBase := stamp%100_000_000 + 900_000_000
	if _, err := s.Store.DB.Exec(`DELETE FROM sqlite_sequence WHERE name = 'deploy_environments'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO sqlite_sequence(name, seq) VALUES('deploy_environments', ?)`, environmentBase); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeEnvironmentContainers(t, environmentBase) })
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "rollback-live", auth.RoleAdmin)}

	channel := admin.do(http.MethodPost, "/api/v1/deploy/notifications", fmt.Sprintf(
		`{"name":"rollback webhook","kind":"webhook","url":%q,"events":[],"enabled":true}`, receiver.URL+"/hook"), nil)
	if channel.Code != http.StatusCreated && channel.Code != http.StatusOK {
		t.Fatalf("webhook channel = %d %s", channel.Code, channel.Body.String())
	}

	domain := fmt.Sprintf("rollback-%d.example.test", stamp)
	healthy := func(body string) []string {
		return []string{"caddy", "respond", "--listen", ":8080", body}
	}
	// B answers its readiness path and fails everything a visitor asks for,
	// which is the release a readiness check alone lets through.
	broken := []string{"sh", "-c", `printf ':8080 {\n\trespond /health 200\n\trespond "release B is broken" 500\n}\n' > /tmp/Caddyfile && exec caddy run --config /tmp/Caddyfile --adapter caddyfile`}
	projectID, environmentID := liveRollbackProject(t, admin, fmt.Sprintf("rollback-live-%d", stamp%1_000_000), domain, healthy("release A"))
	if environmentID <= environmentBase {
		t.Fatalf("environment %d was not numbered past %d", environmentID, environmentBase)
	}
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, environmentID)

	first := liveRollbackDeploy(t, admin, projectID, environmentID)
	if res := admin.do(http.MethodPut, base+"/rollback-policy", `{"keepWarm":true,"windowSeconds":60,"automatic":true,"errorRateMargin":5,"latencyFactor":2,"minRequests":20}`, nil); res.Code != http.StatusOK {
		t.Fatalf("rollback policy = %d %s", res.Code, res.Body.String())
	}
	for index := 0; index < 30; index++ {
		if status, body := ingressGet(t, endpoint, domain); status != http.StatusOK || body != "release A" {
			t.Fatalf("release A through the ingress = %d %q", status, body)
		}
	}

	liveRollbackConfigure(t, admin, base, broken)
	second := liveRollbackDeploy(t, admin, projectID, environmentID)
	watch := liveRollbackWatch(t, admin, base)
	if watch == nil || watch.State != deploy.RollbackWatchWatching || watch.WarmReleaseNum != 1 || watch.ReleaseNumber != 2 {
		t.Fatalf("after release B: window %+v", watch)
	}
	warmID := watch.WarmRuntimeID
	before := inspectContainer(t, warmID)
	if !before.State.Paused || !before.State.Running {
		t.Fatalf("release A's container is not paused through B's window: %+v", before.State)
	}
	// Docker counts a paused container as not running: exactly one release
	// serves, as before.
	running := ownedRunning(t, environmentID)
	if len(running) != 1 || strings.HasPrefix(warmID, running[0]) {
		t.Fatalf("running release containers during the window = %v (warm %s)", running, warmID[:12])
	}
	// The dashboard restarts in the middle of B's window: the process that
	// opened it is gone, and the next one finds it in the store.
	stopFirst()
	s, stopSecond := start()
	t.Cleanup(stopSecond)
	admin = &client{t: t, h: s.Routes(), cookie: admin.cookie}
	for index := 0; index < 40; index++ {
		if status, _ := ingressGet(t, endpoint, domain); status != http.StatusInternalServerError {
			t.Fatalf("release B through the ingress answered %d", status)
		}
	}

	// The watcher reads the record every two seconds; the rollback is a run.
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
	after := inspectContainer(t, warmID)
	if after.ID != before.ID || after.State.StartedAt != before.State.StartedAt || after.State.Paused || !after.State.Running {
		t.Fatalf("release A came back as another container or a restart: before %+v / after %+v", before, after)
	}
	t.Logf("release A container %s: same id, started %s before and after", warmID[:12], after.State.StartedAt)
	if status, body := ingressGet(t, endpoint, domain); status != http.StatusOK || body != "release A" {
		t.Fatalf("after the rollback the ingress answered %d %q", status, body)
	}
	if containerExists(t, fmt.Sprintf("jd-e%d-r2", environmentID)) {
		t.Fatal("release B's container outlived the rollback")
	}
	releases := liveRollbackReleases(t, admin, base)
	if releases[0].ID != second || releases[0].RolledBackAt == nil || !strings.Contains(releases[0].RollbackReason, "5xx") ||
		releases[1].ID != first || releases[1].State != "live" {
		t.Fatalf("release history after the rollback = %+v", releases)
	}
	rollbackRun := liveRunSnapshot(t, admin, projectID, watch.RollbackRunID)
	if rollbackRun.Run.Trigger != deploy.TriggerAutoRollback || rollbackRun.Run.State != deploy.RunSucceeded || rollbackRun.Run.ReleaseID != first {
		t.Fatalf("automatic rollback run = %+v", rollbackRun.Run)
	}
	delivered := hooks.wait(t, deploy.NotificationEventRolledBack, watch.RollbackRunID)
	if delivered.Rollback == nil || !delivered.Rollback.Automatic || delivered.Rollback.ReleaseNumber != 2 ||
		delivered.Rollback.RestoredReleaseNumber != 1 || !strings.Contains(delivered.Rollback.Reason, "5xx") {
		t.Fatalf("webhook rollback envelope = %+v", delivered.Rollback)
	}
	t.Logf("webhook %s: %s", delivered.Event, delivered.Rollback.Reason)
	var audits int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'deploy.release.auto_rollback' AND actor = 'system' AND success = 1`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("automatic rollback audits = %d, %v", audits, err)
	}

	// The clean case: C stays healthy, and A gives its container back when
	// the window passes.
	liveRollbackConfigure(t, admin, base, healthy("release C"))
	liveRollbackDeploy(t, admin, projectID, environmentID)
	watch = liveRollbackWatch(t, admin, base)
	if watch.State != deploy.RollbackWatchWatching || watch.WarmReleaseNum != 1 || watch.WarmRuntimeID != warmID {
		t.Fatalf("after release C: window %+v", watch)
	}
	for index := 0; index < 40; index++ {
		if status, body := ingressGet(t, endpoint, domain); status != http.StatusOK || body != "release C" {
			t.Fatalf("release C through the ingress = %d %q", status, body)
		}
	}
	deadline = watch.ExpiresAt.Add(30 * time.Second)
	for {
		watch = liveRollbackWatch(t, admin, base)
		if watch.State == deploy.RollbackWatchClosed {
			break
		}
		if time.Now().After(deadline) || watch.State == deploy.RollbackWatchRolledBack {
			t.Fatalf("the clean window did not close: %+v", watch)
		}
		time.Sleep(time.Second)
	}
	if watch.EndReason != deploy.RollbackEndWindowPassed || containerExists(t, warmID) {
		t.Fatalf("after a clean window: %+v, warm container present %t", watch, containerExists(t, warmID))
	}
	t.Logf("clean window: %s", watch.EndMessage)
	if status, body := ingressGet(t, endpoint, domain); status != http.StatusOK || body != "release C" {
		t.Fatalf("release C after its window = %d %q", status, body)
	}
}

func liveRollbackServer(t *testing.T, ingress string) func() *Server {
	t.Helper()
	st, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sealer, err := auth.NewSealer(strings.Repeat("cd", 32))
	if err != nil {
		t.Fatal(err)
	}
	_, loopback, _ := net.ParseCIDR("127.0.0.1/32")
	root := t.TempDir()
	cfg := &config.Config{
		Addr: "127.0.0.1:8080", DataDir: t.TempDir(), AllowedCIDRs: []*net.IPNet{loopback},
		SessionTTL: time.Hour, IdleTTL: time.Hour, FileRoots: []string{root}, LogRoots: []string{root},
		DeployRoots: []string{root}, NginxDir: filepath.Join(root, "nginx"), CaddyFile: filepath.Join(root, "Caddyfile"),
		IngressContainer: ingress, DockerHost: "unix:///var/run/docker.sock",
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if testing.Verbose() {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	}
	return func() *Server {
		svc := auth.NewService(st, sealer, cfg.SessionTTL, cfg.IdleTTL, cfg.Require2FA)
		s := New(cfg, log, st, svc, sealer, audit.New(st, log), nil)
		t.Cleanup(s.Shutdown)
		return s
	}
}

func liveRollbackIngress(t *testing.T, stamp int64) (string, string) {
	t.Helper()
	caddyfile := filepath.Join(t.TempDir(), "Caddyfile")
	if err := os.WriteFile(caddyfile, []byte("# acceptance ingress\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	name := fmt.Sprintf("jdtest-rollback-ingress-%d", stamp)
	liveDocker(t, "run", "-d", "--name", name, "-p", fmt.Sprintf("127.0.0.1:%d:80", port),
		"-v", caddyfile+":/etc/caddy/Caddyfile", "-v", "/config", "-v", "/data", "caddy:2-alpine")
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", "-v", name).Run() })
	deadline := time.Now().Add(30 * time.Second)
	for exec.Command("docker", "exec", name, "wget", "-qO-", "http://127.0.0.1:2019/config/").Run() != nil {
		if time.Now().After(deadline) {
			t.Fatal("the acceptance ingress never answered its admin API")
		}
		time.Sleep(200 * time.Millisecond)
	}
	return name, fmt.Sprintf("http://127.0.0.1:%d", port)
}

func liveDocker(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func removeEnvironmentContainers(t *testing.T, above int64) {
	out, _ := exec.Command("docker", "ps", "-a", "--filter", "label=io.just-dashboard.managed=true",
		"--format", `{{.ID}} {{.Label "io.just-dashboard.environment-id"}}`).Output()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		var id int64
		if _, err := fmt.Sscan(fields[1], &id); err == nil && id > above && id < above+1000 {
			_ = exec.Command("docker", "rm", "-f", fields[0]).Run()
		}
	}
}

func liveRollbackProject(t *testing.T, c *client, name, domain string, command []string) (int64, int64) {
	t.Helper()
	step := func(method, path, body string) map[string]any {
		t.Helper()
		res := c.do(method, path, body, nil)
		if res.Code >= 300 {
			t.Fatalf("%s %s = %d %s", method, path, res.Code, res.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(res.Body.Bytes(), &out)
		return out
	}
	draft := step(http.MethodPost, "/api/v1/deploy/drafts", "{}")
	id := fmt.Sprint(draft["id"])
	revision := func(d map[string]any) int64 { return int64(d["revision"].(float64)) }
	draft = step(http.MethodPut, "/api/v1/deploy/drafts/"+id, fmt.Sprintf(`{"revision":%d,"step":"intent","intent":{"name":%q,"profile":"web"}}`, revision(draft), name))
	draft = step(http.MethodPut, "/api/v1/deploy/drafts/"+id, fmt.Sprintf(`{"revision":%d,"step":"source","source":{"kind":"image","mode":"image_reference","image":"caddy:2-alpine"}}`, revision(draft)))
	draft = step(http.MethodPost, "/api/v1/deploy/drafts/"+id+"/detect", fmt.Sprintf(`{"revision":%d}`, revision(draft)))
	configuration := map[string]any{
		"build":        map[string]any{"method": "image", "noCache": false, "secrets": []any{}, "releaseTasks": []any{}},
		"runtime":      liveRollbackRuntime(command),
		"variables":    []any{},
		"dependencies": []any{},
		"checks":       liveRollbackChecks(),
		"domains":      []any{map[string]any{"hostname": domain, "https": false, "ownership": "managed"}},
	}
	body, _ := json.Marshal(map[string]any{"revision": revision(draft), "step": "configuration", "configuration": configuration})
	draft = step(http.MethodPut, "/api/v1/deploy/drafts/"+id, string(body))
	preflight := step(http.MethodPost, "/api/v1/deploy/drafts/"+id+"/preflight", fmt.Sprintf(`{"revision":%d}`, revision(draft)))
	draft = preflight["draft"].(map[string]any)
	warnings := []string{}
	for _, raw := range preflight["preflight"].(map[string]any)["findings"].([]any) {
		finding := raw.(map[string]any)
		switch finding["severity"] {
		case "blocked", "decision":
			t.Fatalf("preflight refused the fixture: %v", finding)
		case "warning":
			warnings = append(warnings, finding["code"].(string))
		}
	}
	acknowledged, _ := json.Marshal(warnings)
	committed := step(http.MethodPost, "/api/v1/deploy/drafts/"+id+"/commit", fmt.Sprintf(`{"revision":%d,"acknowledgedWarnings":%s}`, revision(draft), acknowledged))
	number := func(value any) int64 {
		if float, ok := value.(float64); ok {
			return int64(float)
		}
		var parsed int64
		_, _ = fmt.Sscan(fmt.Sprint(value), &parsed)
		return parsed
	}
	return number(committed["projectId"]), number(committed["environmentId"])
}

func liveRollbackRuntime(command []string) map[string]any {
	return map[string]any{
		"image": "caddy:2-alpine", "command": command, "internalPort": 8080, "hostPort": 0,
		"bindAddress": "127.0.0.1", "strategy": "blue_green", "privileged": false, "hostNetwork": false,
		"capabilities": []any{}, "devices": []any{}, "mounts": []any{}, "stopSignal": "SIGTERM", "gracePeriodSeconds": 2,
	}
}

func liveRollbackChecks() []any {
	return []any{map[string]any{
		"name": "HTTP readiness", "kind": "http", "phase": "readiness", "required": true,
		"config": map[string]any{"path": "/health", "attempts": 30, "timeoutSeconds": 3, "intervalSeconds": 1},
	}}
}

func liveRollbackConfigure(t *testing.T, c *client, base string, command []string) {
	t.Helper()
	read := c.do(http.MethodGet, base+"/configuration", "", nil)
	var current deploy.EnvironmentConfiguration
	if err := json.Unmarshal(read.Body.Bytes(), &current); err != nil || read.Code != http.StatusOK {
		t.Fatalf("configuration = %d %s", read.Code, read.Body.String())
	}
	current.Runtime.Command = command
	body, _ := json.Marshal(deploy.ConfigurationWriteRequest{
		Revision: current.Revision, Build: current.Build, Runtime: current.Runtime,
		Dependencies: current.Dependencies, Checks: current.Checks, Domains: current.Domains,
	})
	if res := c.do(http.MethodPut, base+"/configuration", string(body), nil); res.Code != http.StatusOK {
		t.Fatalf("configuration save = %d %s", res.Code, res.Body.String())
	}
}

func liveRollbackDeploy(t *testing.T, c *client, projectID, environmentID int64) int64 {
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
			if snapshot.Run.State != deploy.RunSucceeded {
				t.Fatalf("run %d = %s %s: %s", run.ID, snapshot.Run.State, snapshot.Run.TerminalCode, snapshot.Run.TerminalReason)
			}
			return snapshot.Run.ReleaseID
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %d stayed %s", run.ID, snapshot.Run.State)
		}
		time.Sleep(time.Second)
	}
}

func liveRunSnapshot(t *testing.T, c *client, projectID, runID int64) deploy.RunSnapshot {
	t.Helper()
	res := c.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/runs/%d", projectID, runID), "", nil)
	var snapshot deploy.RunSnapshot
	if err := json.Unmarshal(res.Body.Bytes(), &snapshot); err != nil || res.Code != http.StatusOK {
		t.Fatalf("run %d = %d %s", runID, res.Code, res.Body.String())
	}
	return snapshot
}

func liveRollbackWatch(t *testing.T, c *client, base string) *deploy.RollbackWatch {
	t.Helper()
	res := c.do(http.MethodGet, base+"/rollback-watch", "", nil)
	var body struct {
		Watch *deploy.RollbackWatch `json:"watch"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || res.Code != http.StatusOK {
		t.Fatalf("rollback watch = %d %s", res.Code, res.Body.String())
	}
	return body.Watch
}

func liveRollbackReleases(t *testing.T, c *client, base string) []deploy.Release {
	t.Helper()
	res := c.do(http.MethodGet, base+"/releases", "", nil)
	var releases []deploy.Release
	if err := json.Unmarshal(res.Body.Bytes(), &releases); err != nil || res.Code != http.StatusOK {
		t.Fatalf("releases = %d %s", res.Code, res.Body.String())
	}
	return releases
}

func ingressGet(t *testing.T, endpoint, host string) (int, string) {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, endpoint+"/", nil)
	request.Host = host
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("through the ingress: %v", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response.StatusCode, strings.TrimSpace(string(body))
}

type liveContainer struct {
	ID    string
	State struct {
		Running   bool
		Paused    bool
		StartedAt string
	}
}

func inspectContainer(t *testing.T, id string) liveContainer {
	t.Helper()
	var containers []liveContainer
	if err := json.Unmarshal([]byte(liveDocker(t, "inspect", id)), &containers); err != nil || len(containers) != 1 {
		t.Fatalf("inspect %s: %v", id, err)
	}
	return containers[0]
}

func containerExists(t *testing.T, id string) bool {
	t.Helper()
	return exec.Command("docker", "inspect", id).Run() == nil
}

func ownedRunning(t *testing.T, environmentID int64) []string {
	t.Helper()
	out := liveDocker(t, "ps", "--filter", "label=io.just-dashboard.managed=true",
		"--filter", fmt.Sprintf("label=io.just-dashboard.environment-id=%d", environmentID),
		"--filter", "status=running", "--format", "{{.ID}}")
	return strings.Fields(out)
}

type rollbackHookRecorder struct {
	mu        sync.Mutex
	envelopes []deploy.NotificationEnvelope
}

func (r *rollbackHookRecorder) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	var envelope deploy.NotificationEnvelope
	if err := json.NewDecoder(request.Body).Decode(&envelope); err == nil {
		r.mu.Lock()
		r.envelopes = append(r.envelopes, envelope)
		r.mu.Unlock()
	}
	w.WriteHeader(http.StatusOK)
}

func (r *rollbackHookRecorder) wait(t *testing.T, event string, runID int64) deploy.NotificationEnvelope {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		r.mu.Lock()
		for _, envelope := range r.envelopes {
			if envelope.Event == event && envelope.RunID == runID {
				r.mu.Unlock()
				return envelope
			}
		}
		seen := len(r.envelopes)
		r.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("no %s for run %d among %d deliveries", event, runID, seen)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
