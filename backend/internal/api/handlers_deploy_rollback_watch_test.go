package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// warmOwner is a runtime owner whose containers pause, so the routes can be
// driven without a Docker daemon.
type warmOwner struct {
	mu      sync.Mutex
	stopped []string
}

func (o *warmOwner) StartCandidate(context.Context, deploy.CandidateRuntimeRequest, func(deploy.BuildLog) error) (deploy.StartedRuntime, error) {
	return deploy.StartedRuntime{}, deploy.ErrRuntimeUnavailable
}
func (o *warmOwner) StartExisting(context.Context, deploy.ReleaseRuntime, map[string]string, func(deploy.BuildLog) error) error {
	return nil
}
func (o *warmOwner) Stop(_ context.Context, runtime deploy.ReleaseRuntime, _ deploy.RuntimePlanConfig, _ map[string]string, remove bool, _ func(deploy.BuildLog) error) (deploy.RuntimeStopEvidence, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stopped = append(o.stopped, runtime.RuntimeID)
	return deploy.RuntimeStopEvidence{RuntimeID: runtime.RuntimeID, Removed: remove}, nil
}
func (o *warmOwner) PauseRuntime(context.Context, deploy.ReleaseRuntime) error  { return nil }
func (o *warmOwner) ResumeRuntime(context.Context, deploy.ReleaseRuntime) error { return nil }
func (o *warmOwner) WarmState(context.Context, deploy.ReleaseRuntime) (string, error) {
	return "paused", nil
}

// warmWindowFixture is an environment with two releases, the second live and
// the first kept warm in an open window.
func warmWindowFixture(t *testing.T, s *Server, c *client, name string, open bool) (projectID, environmentID int64, first, second *deploy.ReleaseWithArtifacts) {
	t.Helper()
	project := createLegacyDeploymentFixture(t, s, name)
	environmentID = convertToNormalizedDeploymentFixture(t, s, project)
	path := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/runs", project.ID, environmentID)
	var run deploy.EngineRun
	response := c.do(http.MethodPost, path, `{"operation":"deploy"}`, map[string]string{"Idempotency-Key": name + "-deploy"})
	if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil || response.Code != http.StatusAccepted {
		t.Fatalf("deploy = %d %s", response.Code, response.Body.String())
	}
	first = activateSyntheticNormalizedRelease(t, s, run, name+"-v1")
	response = c.do(http.MethodPost, path, `{"operation":"redeploy"}`, map[string]string{"Idempotency-Key": name + "-redeploy"})
	if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil || response.Code != http.StatusAccepted {
		t.Fatalf("redeploy = %d %s", response.Code, response.Body.String())
	}
	second = cloneAndActivateSyntheticRelease(t, s, run, first.Release.ID, name+"-v2")
	if !open {
		return project.ID, environmentID, first, second
	}
	now := time.Now().Unix()
	if _, err := s.Store.DB.Exec(`UPDATE deploy_release_runtimes SET state = 'warm' WHERE release_id = ?`, first.Release.ID); err != nil {
		t.Fatal(err)
	}
	policy, _ := json.Marshal(deploy.DefaultRollbackPolicy())
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_rollback_watches(project_id, environment_id, run_id, release_id, warm_release_id,
		warm_runtime_id, state, policy_json, cutover_at, expires_at, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, 'runtime-`+name+`-v1', 'watching', ?, ?, ?, ?, ?)`,
		project.ID, environmentID, run.ID, second.Release.ID, first.Release.ID, string(policy), now, now+600, now, now); err != nil {
		t.Fatal(err)
	}
	return project.ID, environmentID, first, second
}

func rollbackAuditCount(t *testing.T, s *Server, action string) int {
	t.Helper()
	var count int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = ? AND success = 1`, action).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestInstantRollbackRouteIsDestructiveAuditedAndRefusedWithoutAWarmRelease(t *testing.T) {
	s := testServer(t)
	routes := s.Routes()
	admin := &client{t: t, h: routes, cookie: signInAs(t, s, "rollback-admin", auth.RoleAdmin)}
	limited := &client{t: t, h: routes, cookie: signInAs(t, s, "rollback-operator", auth.RoleLimited)}
	reader := &client{t: t, h: routes, cookie: signInAs(t, s, "rollback-reader", auth.RoleReadOnly)}

	projectID, environmentID, _, _ := warmWindowFixture(t, s, admin, "no-warm", false)
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/rollback-watch", projectID, environmentID)
	if res := admin.do(http.MethodPost, base+"/rollback", "", nil); res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "no_warm_release") {
		t.Fatalf("instant rollback with nothing warm = %d %s", res.Code, res.Body.String())
	}
	if res := admin.do(http.MethodPost, base+"/keep", "", nil); res.Code != http.StatusConflict {
		t.Fatalf("keep with nothing warm = %d %s", res.Code, res.Body.String())
	}
	if rollbackAuditCount(t, s, "deploy.release.instant_rollback") != 0 {
		t.Fatal("a refused instant rollback was audited as done")
	}

	projectID, environmentID, first, second := warmWindowFixture(t, s, admin, "warm", true)
	base = fmt.Sprintf("/api/v1/deploy/%d/environments/%d/rollback-watch", projectID, environmentID)
	read := reader.do(http.MethodGet, base, "", nil)
	var state struct {
		Policy deploy.RollbackPolicy `json:"policy"`
		Watch  *deploy.RollbackWatch `json:"watch"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &state); err != nil || read.Code != http.StatusOK || state.Watch == nil ||
		state.Watch.State != deploy.RollbackWatchWatching || state.Watch.WarmReleaseID != first.Release.ID ||
		state.Watch.ReleaseNumber != second.Release.Number || state.Policy != deploy.DefaultRollbackPolicy() {
		t.Fatalf("read = %d %s", read.Code, read.Body.String())
	}
	for _, who := range []*client{reader, limited} {
		if res := who.do(http.MethodPost, base+"/rollback", "", nil); res.Code != http.StatusForbidden {
			t.Fatalf("instant rollback without the destructive capability = %d %s", res.Code, res.Body.String())
		}
	}
	res := admin.do(http.MethodPost, base+"/rollback", "", nil)
	if res.Code != http.StatusAccepted {
		t.Fatalf("instant rollback = %d %s", res.Code, res.Body.String())
	}
	var run deploy.EngineRun
	if err := json.Unmarshal(res.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	steps, _ := s.modules.deployRuns.Steps(t.Context(), run.ID)
	keys := []string{}
	for _, step := range steps {
		keys = append(keys, string(step.Key))
	}
	if run.Operation != deploy.OperationRollback || run.Trigger != deploy.TriggerRollback || run.SlotClass != deploy.SlotLight ||
		!strings.Contains(string(run.Metadata), `"instant":true`) || !strings.Contains(string(run.Metadata), fmt.Sprintf(`"targetReleaseId":%d`, first.Release.ID)) ||
		strings.Join(keys, ",") != "start_candidate,verify_readiness,activate,retire_previous,record_release,notify" {
		t.Fatalf("instant rollback run = %+v steps %v", run, keys)
	}
	watch, _ := s.modules.deployRuns.OpenRollbackWatch(t.Context(), environmentID)
	if watch.RollbackRunID != run.ID {
		t.Fatalf("the window does not hold the run it asked for: %+v", watch)
	}
	var detail string
	if err := s.Store.DB.QueryRow(`SELECT detail FROM audit_log WHERE action = 'deploy.release.instant_rollback' AND success = 1`).Scan(&detail); err != nil ||
		!strings.Contains(detail, fmt.Sprintf(`"runId":%d`, run.ID)) || !strings.Contains(detail, fmt.Sprintf(`"fromReleaseId":%d`, second.Release.ID)) {
		t.Fatalf("instant rollback audit = %q, %v", detail, err)
	}
	if res := admin.do(http.MethodPost, base+"/rollback", "", nil); res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "rollback_in_progress") {
		t.Fatalf("a second press = %d %s", res.Code, res.Body.String())
	}
}

func TestKeepReleaseEndsTheWindowForAnOperator(t *testing.T) {
	s := testServer(t)
	routes := s.Routes()
	admin := &client{t: t, h: routes, cookie: signInAs(t, s, "keep-admin", auth.RoleAdmin)}
	limited := &client{t: t, h: routes, cookie: signInAs(t, s, "keep-operator", auth.RoleLimited)}
	reader := &client{t: t, h: routes, cookie: signInAs(t, s, "keep-reader", auth.RoleReadOnly)}
	owner := &warmOwner{}
	s.modules.deployExecutor = deploy.NewNormalizedStepExecutor(s.modules.deployRuns, s.modules.deployPlanning,
		s.modules.deploySources, s.modules.deployArtifacts, owner, nil, nil, t.TempDir())

	projectID, environmentID, first, _ := warmWindowFixture(t, s, admin, "keep", true)
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/rollback-watch", projectID, environmentID)
	if res := reader.do(http.MethodPost, base+"/keep", "", nil); res.Code != http.StatusForbidden {
		t.Fatalf("keep by a reader = %d %s", res.Code, res.Body.String())
	}
	res := limited.do(http.MethodPost, base+"/keep", "", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("keep = %d %s", res.Code, res.Body.String())
	}
	var closed deploy.RollbackWatch
	if err := json.Unmarshal(res.Body.Bytes(), &closed); err != nil || closed.State != deploy.RollbackWatchClosed ||
		closed.EndReason != deploy.RollbackEndKept || !strings.Contains(closed.EndMessage, "keep-operator kept release") {
		t.Fatalf("closed window = %s", res.Body.String())
	}
	if len(owner.stopped) != 1 || owner.stopped[0] != "runtime-keep-v1" {
		t.Fatalf("stopped = %v", owner.stopped)
	}
	runtime, _ := s.modules.deployRuns.RuntimeForRelease(t.Context(), first.Release.ID)
	if runtime.State != "retired" || rollbackAuditCount(t, s, "deploy.release.keep") != 1 {
		t.Fatalf("after keep: runtime %s, audits %d", runtime.State, rollbackAuditCount(t, s, "deploy.release.keep"))
	}
	if res := limited.do(http.MethodPost, base+"/keep", "", nil); res.Code != http.StatusConflict {
		t.Fatalf("keep twice = %d %s", res.Code, res.Body.String())
	}
}

func TestRollbackPolicyRouteIsAdministrativeAndAudited(t *testing.T) {
	s := testServer(t)
	routes := s.Routes()
	admin := &client{t: t, h: routes, cookie: signInAs(t, s, "policy-admin", auth.RoleAdmin)}
	limited := &client{t: t, h: routes, cookie: signInAs(t, s, "policy-operator", auth.RoleLimited)}
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	path := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/rollback-policy", projectID, environmentID)
	body := `{"keepWarm":true,"windowSeconds":300,"automatic":false,"errorRateMargin":3,"latencyFactor":1.5,"minRequests":40}`
	if res := limited.do(http.MethodPut, path, body, nil); res.Code != http.StatusForbidden {
		t.Fatalf("policy by an operator = %d %s", res.Code, res.Body.String())
	}
	if res := admin.do(http.MethodPut, path, `{"keepWarm":true,"windowSeconds":5,"errorRateMargin":3,"latencyFactor":1.5,"minRequests":40}`, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a five-second window = %d %s", res.Code, res.Body.String())
	}
	res := admin.do(http.MethodPut, path, body, nil)
	var saved deploy.RollbackPolicy
	if err := json.Unmarshal(res.Body.Bytes(), &saved); err != nil || res.Code != http.StatusOK || saved.WindowSeconds != 300 || saved.Automatic || saved.UpdatedBy != "policy-admin" {
		t.Fatalf("policy save = %d %s", res.Code, res.Body.String())
	}
	var detail string
	if err := s.Store.DB.QueryRow(`SELECT detail FROM audit_log WHERE action = 'deploy.rollback_policy.update' AND success = 1`).Scan(&detail); err != nil || !strings.Contains(detail, `"windowSeconds":300`) {
		t.Fatalf("policy audit = %q, %v", detail, err)
	}
	read := admin.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/rollback-watch", projectID, environmentID), "", nil)
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"windowSeconds":300`) || !strings.Contains(read.Body.String(), `"watch":null`) {
		t.Fatalf("read = %d %s", read.Code, read.Body.String())
	}
}
