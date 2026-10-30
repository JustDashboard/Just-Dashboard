package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// The deployments' exposition is what an alerting stack reads about every
// environment, so it has to be a valid text-format body with the right
// figures, behind the same key as the databases', and nothing a session or a
// key for another job can pull.

func scrapePath(s *Server, method, path, secret string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = "127.0.0.1:5555"
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	return w
}

// seedLiveRelease records a release live in the environment since `age` ago
// and returns its id.
func seedLiveRelease(t *testing.T, s *Server, projectID, environmentID, number int64, age time.Duration) int64 {
	t.Helper()
	activated := time.Now().Add(-age).Unix()
	res, err := s.Store.DB.ExecContext(t.Context(), `INSERT INTO deploy_releases(project_id, environment_id, release_number, state, created_at, activated_at)
		VALUES(?, ?, ?, 'live', ?, ?)`, projectID, environmentID, number, activated, activated)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if _, err := s.Store.DB.ExecContext(t.Context(), `UPDATE deploy_environments SET live_release_id = ? WHERE id = ?`, id, environmentID); err != nil {
		t.Fatal(err)
	}
	return id
}

func seedRun(t *testing.T, s *Server, projectID, environmentID int64, state string) {
	t.Helper()
	now := time.Now().Unix()
	if _, err := s.Store.DB.ExecContext(t.Context(), `INSERT INTO deploy_runs(project_id, environment_id, started_at, ended_at, status, state, requested_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)`, projectID, environmentID, now-60, now, state, state, now-60); err != nil {
		t.Fatal(err)
	}
}

func seedContainerSample(t *testing.T, s *Server, name string, age time.Duration, cpuTotal, memBytes, memLimit int64, limited bool) {
	t.Helper()
	if _, err := s.Store.DB.ExecContext(t.Context(), `INSERT INTO metric_container_samples(ts, name, cpu_percent, mem_bytes, mem_limit, mem_percent, net_rx, net_tx, pids, container_id, mem_limited, cpu_total)
		VALUES(?, ?, 1.5, ?, ?, 0, 0, 0, 1, 'c0ffee', ?, ?)`, time.Now().Add(-age).Unix(), name, memBytes, memLimit, boolValue(limited), cpuTotal); err != nil {
		t.Fatal(err)
	}
}

func recordDeploymentEvent(t *testing.T, s *Server, at time.Time, action, name, id string, environmentID, releaseID int64, exitCode string) {
	t.Helper()
	ev := dockerx.Event{
		Time: at, Type: "container", Action: action, Name: name, ID: id, ExitCode: exitCode, Source: "daemon",
		Owner: map[string]string{
			"managed": "true", "environment-id": strconv.FormatInt(environmentID, 10),
			"release-id": strconv.FormatInt(releaseID, 10), "release-number": "3", "run-id": "42",
		},
	}
	ev.Level, ev.Message = "notice", name+" "+action
	kept, err := s.modules.deployEvents.Record(t.Context(), ev)
	if err != nil || !kept {
		t.Fatalf("event not kept: %v %v", kept, err)
	}
}

func TestDeploymentMetricsExportSpeaksForEveryEnvironment(t *testing.T) {
	s := testServer(t)
	projectID, environmentID := seedDeployment(t, s, "shop")
	releaseID := seedLiveRelease(t, s, projectID, environmentID, 3, 2*time.Hour)
	for _, state := range []string{"succeeded", "succeeded", "failed", "cancelled", "running"} {
		seedRun(t, s, projectID, environmentID, state)
	}
	// Staging beside production: live release none, runs none.
	stagingRes, err := s.Store.DB.ExecContext(t.Context(), `INSERT INTO deploy_environments(project_id, name, slug, kind, strategy, created_at, updated_at)
		VALUES(?, 'staging', 'staging', 'staging', 'recreate', 1, 1)`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	stagingID, _ := stagingRes.LastInsertId()
	// An archived deployment is nobody's series, and a preview environment
	// comes and goes with its pull request.
	archivedID, archivedEnv := seedDeployment(t, s, "old-shop")
	if _, err := s.Store.DB.ExecContext(t.Context(), `UPDATE deploy_projects SET archived_at = 1 WHERE id = ?`, archivedID); err != nil {
		t.Fatal(err)
	}
	_ = archivedEnv
	if _, err := s.Store.DB.ExecContext(t.Context(), `INSERT INTO deploy_environments(project_id, name, slug, kind, strategy, created_at, updated_at)
		VALUES(?, 'pr-12', 'pr-12', 'preview', 'recreate', 1, 1)`, projectID); err != nil {
		t.Fatal(err)
	}

	// The release's one container: started with the release, OOM-killed and
	// back a minute later, sampled five seconds ago.
	now := time.Now()
	recordDeploymentEvent(t, s, now.Add(-2*time.Hour), "start", "shop-production-r3", "c0ffee", environmentID, releaseID, "")
	recordDeploymentEvent(t, s, now.Add(-time.Hour), "oom", "shop-production-r3", "c0ffee", environmentID, releaseID, "")
	recordDeploymentEvent(t, s, now.Add(-time.Hour), "die", "shop-production-r3", "c0ffee", environmentID, releaseID, "137")
	recordDeploymentEvent(t, s, now.Add(-time.Hour+2*time.Minute), "start", "shop-production-r3", "c0ffee", environmentID, releaseID, "")
	seedContainerSample(t, s, "shop-production-r3", 5*time.Second, 1_500_000_000, 100<<20, 512<<20, true)
	// A container whose newest sample is old says when it was sampled and
	// nothing else: an hour-old figure would be charted as now.
	recordDeploymentEvent(t, s, now.Add(-2*time.Hour), "start", "shop-production-r3-worker", "beef", environmentID, releaseID, "")
	seedContainerSample(t, s, "shop-production-r3-worker", 10*time.Minute, 9_000_000_000, 50<<20, 0, false)

	reply := scrapePath(s, http.MethodGet, deploymentMetricsEndpoint, testMetricsKey(t, s, true))
	if reply.Code != http.StatusOK {
		t.Fatalf("scrape = %d %s", reply.Code, reply.Body.String())
	}
	if got := reply.Header().Get("Content-Type"); got != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
	if reply.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("a scrape was cacheable")
	}
	body := reply.Body.String()
	samples := checkExposition(t, body)
	// JD_TEST_EXPOSITION_OUT writes the body for Prometheus' own linter:
	// `promtool check metrics < file`, which holds the conventions this
	// checker does not (unit suffixes, counter names, help text).
	if out := os.Getenv("JD_TEST_EXPOSITION_OUT"); out != "" {
		if err := os.WriteFile(out, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	base := fmt.Sprintf(`deployment="shop",deployment_id="%d",environment="production"`, projectID)
	withRelease := base + `,release="3"`
	for series, want := range map[string]string{
		fmt.Sprintf(`jd_deployment_info{%s,environment_id="%d",kind="production",release="3"}`, base, environmentID):                                                      "1",
		fmt.Sprintf(`jd_deployment_info{deployment="shop",deployment_id="%d",environment="staging",environment_id="%d",kind="staging",release=""}`, projectID, stagingID): "1",
		`jd_deployment_runs_total{` + base + `,result="succeeded"}`:                                                                                                       "2",
		`jd_deployment_runs_total{` + base + `,result="failed"}`:                                                                                                          "1",
		`jd_deployment_runs_total{` + base + `,result="cancelled"}`:                                                                                                       "1",
		`jd_deployment_runs_total{` + base + `,result="rolled_back"}`:                                                                                                     "0",
		`jd_deployment_runs_total{` + base + `,result="superseded"}`:                                                                                                      "0",
		fmt.Sprintf(`jd_deployment_runs_total{deployment="shop",deployment_id="%d",environment="staging",result="succeeded"}`, projectID):                                 "0",
		`jd_deployment_container_restarts_total{` + withRelease + `}`:                                                                                                     "1",
		`jd_deployment_container_oom_kills_total{` + withRelease + `}`:                                                                                                    "1",
		`jd_deployment_container_exits_total{` + base + `,exit_code="137",release="3"}`:                                                                                   "1",
		`jd_deployment_container_cpu_seconds_total{container="shop-production-r3",` + withRelease + `}`:                                                                   "1.5",
		`jd_deployment_container_memory_bytes{container="shop-production-r3",` + withRelease + `}`:                                                                        strconv.Itoa(100 << 20),
		`jd_deployment_container_memory_limit_bytes{container="shop-production-r3",` + withRelease + `}`:                                                                  strconv.Itoa(512 << 20),
	} {
		if samples[series] != want {
			t.Errorf("%s = %q, want %q", series, samples[series], want)
		}
	}
	// A run still going is no result yet.
	if strings.Contains(body, `result="running"`) {
		t.Error("a running run was counted as a result")
	}
	age, err := strconv.ParseFloat(samples[`jd_deployment_release_age_seconds{`+withRelease+`}`], 64)
	if err != nil || age < 7190 || age > 7300 {
		t.Errorf("release age = %q, want about two hours", samples[`jd_deployment_release_age_seconds{`+withRelease+`}`])
	}
	// The stale worker: its timestamp and nothing current.
	if samples[`jd_deployment_container_last_sample_timestamp_seconds{container="shop-production-r3-worker",`+withRelease+`}`] == "" {
		t.Error("the stale container's sample time is missing")
	}
	for series := range samples {
		if strings.Contains(series, `container="shop-production-r3-worker"`) && !strings.HasPrefix(series, "jd_deployment_container_last_sample_timestamp_seconds") {
			t.Errorf("a stale sample was exported as current: %s", series)
		}
	}
	// Up and running come from Docker, and are either absent (no daemon
	// answered) or say no container of the release is up, never a guess.
	if up, ok := samples[`jd_deployment_up{`+withRelease+`}`]; ok && up != "0" {
		t.Errorf("up = %q with no container running", up)
	}
	// Availability: observed since the first event two hours ago, down for
	// the two minutes between the exit and the start.
	ratio, err := strconv.ParseFloat(samples[`jd_deployment_availability_ratio{`+base+`,window="24h"}`], 64)
	if err != nil {
		t.Fatalf("no 24h availability: %v\n%s", err, body)
	}
	if want := 1 - 120.0/7200; ratio < want-0.002 || ratio > want+0.002 {
		t.Errorf("24h availability = %f, want about %f", ratio, want)
	}
	if samples[`jd_deployment_availability_downtime_seconds{`+base+`,window="24h"}`] != "120" {
		t.Errorf("24h downtime = %q, want 120", samples[`jd_deployment_availability_downtime_seconds{`+base+`,window="24h"}`])
	}
	coverage, _ := strconv.ParseFloat(samples[`jd_deployment_availability_coverage_ratio{`+base+`,window="30d"}`], 64)
	if coverage <= 0 || coverage > 0.01 {
		t.Errorf("30d coverage = %f, want two hours of thirty days", coverage)
	}
	// Staging has no release and no events: no figure, and no series that
	// would read as one.
	if strings.Contains(body, `environment="staging",window=`) && strings.Contains(body, `jd_deployment_availability_ratio{deployment="shop",deployment_id="`+strconv.FormatInt(projectID, 10)+`",environment="staging"`) {
		t.Error("staging exported an availability ratio with nothing observed")
	}
	// Nothing about what was archived or what is a preview.
	if strings.Contains(body, "old-shop") || strings.Contains(body, `environment="pr-12"`) {
		t.Errorf("archived or preview environments were exported:\n%s", body)
	}
	// Every family is explained.
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "# HELP ") && len(strings.Fields(line)) < 4 {
			t.Errorf("a family has no help text: %s", line)
		}
	}
}

func TestDeploymentMetricsExportAnswersNothingToTheWrongCaller(t *testing.T) {
	s := testServer(t)
	scoped := testMetricsKey(t, s, true)

	if w := scrapePath(s, http.MethodGet, deploymentMetricsEndpoint, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no credential = %d", w.Code)
	}
	if w := scrapePath(s, http.MethodGet, deploymentMetricsEndpoint, "vpsd_"+strings.Repeat("0", 43)); w.Code != http.StatusUnauthorized {
		t.Fatalf("unknown key = %d", w.Code)
	}
	if w := scrapePath(s, http.MethodPost, deploymentMetricsEndpoint, scoped); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d", w.Code)
	}
	// A signed-in browser is not an API key.
	viewer := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "browser", auth.RoleAdmin)}
	if w := viewer.do(http.MethodGet, deploymentMetricsEndpoint, "", nil); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "token_required") {
		t.Fatalf("session = %d %s", w.Code, w.Body.String())
	}
	// A key for another job — a deploy key, an MCP key — is the wrong scope.
	admin, err := s.Auth.CreateUser(t.Context(), "minter", "Correct-Horse-Battery-9", auth.RoleAdmin, false)
	if err != nil {
		t.Fatal(err)
	}
	_, deployKey, err := s.Auth.CreateDeployToken(t.Context(), admin, "github-actions", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	if w := scrapePath(s, http.MethodGet, deploymentMetricsEndpoint, deployKey); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "key_scope") {
		t.Fatalf("deploy key = %d %s", w.Code, w.Body.String())
	}
	_, mcpKey, err := s.Auth.CreateMCPToken(t.Context(), admin, "claude-code", "", time.Hour, nil, auth.DeployRun, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if w := scrapePath(s, http.MethodGet, deploymentMetricsEndpoint, mcpKey); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "key_scope") {
		t.Fatalf("MCP key = %d %s", w.Code, w.Body.String())
	}
	// The metrics key, and an ordinary key holding the capability through
	// its role, both read it — with no deployments, an empty but valid body.
	for name, key := range map[string]string{"scoped": scoped, "ordinary": testMetricsKey(t, s, false)} {
		w := scrapePath(s, http.MethodGet, deploymentMetricsEndpoint, key)
		if w.Code != http.StatusOK {
			t.Fatalf("%s key = %d %s", name, w.Code, w.Body.String())
		}
		if body := w.Body.String(); body != "" {
			checkExposition(t, body)
		}
	}
}
