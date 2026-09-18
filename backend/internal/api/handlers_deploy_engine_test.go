package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/gorilla/websocket"
)

func TestDeploymentRunRoutesPersistBeforeAcceptedAndResumeByID(t *testing.T) {
	c, s := newClient(t)
	project := createLegacyDeploymentFixture(t, s, "persistent-route")
	environmentID, _, err := s.modules.deployRuns.ProductionEnvironment(t.Context(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/runs", project.ID, environmentID)
	response := c.do(http.MethodPost, path, `{"operation":"deploy"}`,
		map[string]string{"Idempotency-Key": "browser-retry-1"})
	if response.Code != http.StatusAccepted {
		t.Fatalf("create run = %d %s", response.Code, response.Body.String())
	}
	var run deploy.EngineRun
	if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if run.ID == 0 || run.State != deploy.RunQueued {
		t.Fatalf("accepted run = %#v", run)
	}

	get := c.do(http.MethodGet,
		fmt.Sprintf("/api/v1/deploy/%d/runs/%d", project.ID, run.ID), "", nil)
	if get.Code != http.StatusOK {
		t.Fatalf("get run = %d %s", get.Code, get.Body.String())
	}
	var snapshot deploy.RunSnapshot
	if err := json.Unmarshal(get.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Run.ID != run.ID || len(snapshot.Steps) != 1 ||
		snapshot.Steps[0].Key != deploy.StepLegacyPipeline {
		t.Fatalf("persisted snapshot = %#v", snapshot)
	}
	logs := c.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/runs/%d/logs", project.ID, run.ID), "", nil)
	var handoff deploy.RunLogs
	if logs.Code != http.StatusOK || json.Unmarshal(logs.Body.Bytes(), &handoff) != nil || handoff.Status != "unavailable" || handoff.Reason == "" {
		t.Fatalf("unavailable run logs = %d %s", logs.Code, logs.Body.String())
	}
	wrongProject := c.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/runs/%d/logs", project.ID+1, run.ID), "", nil)
	if wrongProject.Code != http.StatusNotFound {
		t.Fatalf("cross-project log handoff = %d", wrongProject.Code)
	}
	logPath := fmt.Sprintf("/api/v1/deploy/%d/runs/%d/logs", project.ID, run.ID)
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "run-log-reader", auth.RoleReadOnly)}
	if response := reader.do(http.MethodGet, logPath, "", nil); response.Code != http.StatusOK {
		t.Fatalf("read-only log handoff = %d %s", response.Code, response.Body.String())
	}
	anonymous := &client{t: t, h: s.Routes()}
	if response := anonymous.do(http.MethodGet, logPath, "", nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous log handoff = %d", response.Code)
	}
	metricsPath := fmt.Sprintf("/api/v1/deploy/%d/runs/%d/metrics", project.ID, run.ID)
	if response := reader.do(http.MethodGet, metricsPath, "", nil); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"unavailable"`) {
		t.Fatalf("read-only bare-host metrics = %d %s", response.Code, response.Body.String())
	}
	if response := anonymous.do(http.MethodGet, metricsPath, "", nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous metrics = %d", response.Code)
	}
	if response := reader.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/runs/%d/metrics", project.ID+1, run.ID), "", nil); response.Code != http.StatusNotFound {
		t.Fatalf("cross-project metrics = %d", response.Code)
	}

	conflict := c.do(http.MethodPost, path, `{"operation":"deploy"}`,
		map[string]string{"Idempotency-Key": "browser-retry-1"})
	if conflict.Code != http.StatusAccepted {
		t.Fatalf("idempotent replay = %d %s", conflict.Code, conflict.Body.String())
	}
	var replay deploy.EngineRun
	if err := json.Unmarshal(conflict.Body.Bytes(), &replay); err != nil {
		t.Fatal(err)
	}
	if replay.ID != run.ID {
		t.Fatalf("idempotent replay created run %d, want %d", replay.ID, run.ID)
	}

	cancel := c.do(http.MethodPost,
		fmt.Sprintf("/api/v1/deploy/%d/runs/%d/cancel", project.ID, run.ID), "", nil)
	if cancel.Code != http.StatusAccepted {
		t.Fatalf("cancel run = %d %s", cancel.Code, cancel.Body.String())
	}
	retry := c.do(http.MethodPost,
		fmt.Sprintf("/api/v1/deploy/%d/runs/%d/retry", project.ID, run.ID), "",
		map[string]string{"Idempotency-Key": "retry-cancelled-1"})
	if retry.Code != http.StatusAccepted {
		t.Fatalf("retry run = %d %s", retry.Code, retry.Body.String())
	}
	var retried deploy.EngineRun
	if err := json.Unmarshal(retry.Body.Bytes(), &retried); err != nil {
		t.Fatal(err)
	}
	if retried.RetryOfRunID != run.ID || retried.State != deploy.RunQueued {
		t.Fatalf("retried run = %#v", retried)
	}
}

func TestDeploymentRunMutationsRequireServiceControl(t *testing.T) {
	s := testServer(t)
	project := createLegacyDeploymentFixture(t, s, "capability-route")
	environmentID, _, err := s.modules.deployRuns.ProductionEnvironment(t.Context(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	readonly := &client{
		t: t, h: s.Routes(), cookie: signInAs(t, s, "readonly-deploy", auth.RoleReadOnly),
	}
	response := readonly.do(http.MethodPost,
		fmt.Sprintf("/api/v1/deploy/%d/environments/%d/runs", project.ID, environmentID),
		`{"operation":"deploy"}`, nil)
	if response.Code != http.StatusForbidden {
		t.Fatalf("readonly run request = %d %s", response.Code, response.Body.String())
	}
}

func TestLegacyRunRouteQueuesPersistentCompatibilityStep(t *testing.T) {
	c, s := newClient(t)
	project := createLegacyDeploymentFixture(t, s, "legacy-route")
	response := c.do(http.MethodPost,
		fmt.Sprintf("/api/v1/deploy/%d/run", project.ID), "", nil)
	if response.Code != http.StatusAccepted {
		t.Fatalf("legacy run = %d %s", response.Code, response.Body.String())
	}
	var body struct {
		RunID int64           `json:"runId"`
		State deploy.RunState `json:"state"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.RunID == 0 || body.State != deploy.RunQueued {
		t.Fatalf("legacy response = %#v", body)
	}
	snapshot, err := s.modules.deployRuns.Snapshot(t.Context(), body.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Steps) != 1 || snapshot.Steps[0].Key != deploy.StepLegacyPipeline {
		t.Fatalf("legacy run steps = %#v", snapshot.Steps)
	}
}

func TestNormalizedActionsUseDistinctImmutableReleaseSemantics(t *testing.T) {
	c, s := newClient(t)
	project := createLegacyDeploymentFixture(t, s, "normalized-actions")
	environmentID := convertToNormalizedDeploymentFixture(t, s, project)
	path := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/runs", project.ID, environmentID)

	deployResponse := c.do(http.MethodPost, path, `{"operation":"deploy"}`,
		map[string]string{"Idempotency-Key": "normalized-deploy"})
	if deployResponse.Code != http.StatusAccepted {
		t.Fatalf("deploy action = %d %s", deployResponse.Code, deployResponse.Body.String())
	}
	var firstRun deploy.EngineRun
	if err := json.Unmarshal(deployResponse.Body.Bytes(), &firstRun); err != nil {
		t.Fatal(err)
	}
	firstRelease := activateSyntheticNormalizedRelease(t, s, firstRun, "v1")

	redeployResponse := c.do(http.MethodPost, path, `{"operation":"redeploy"}`,
		map[string]string{"Idempotency-Key": "normalized-redeploy"})
	if redeployResponse.Code != http.StatusAccepted {
		t.Fatalf("redeploy action = %d %s", redeployResponse.Code, redeployResponse.Body.String())
	}
	var redeployRun deploy.EngineRun
	if err := json.Unmarshal(redeployResponse.Body.Bytes(), &redeployRun); err != nil {
		t.Fatal(err)
	}
	if redeployRun.Operation != deploy.OperationRedeploy || redeployRun.PlanRevision != firstRelease.Release.PlanRevision {
		t.Fatalf("redeploy run = %#v", redeployRun)
	}
	redeploySteps, err := s.modules.deployRuns.Steps(t.Context(), redeployRun.ID)
	if err != nil || len(redeploySteps) != 10 || redeploySteps[0].Key != deploy.StepRenderRuntime {
		t.Fatalf("redeploy steps = %#v, error=%v", redeploySteps, err)
	}
	secondRelease := cloneAndActivateSyntheticRelease(t, s, redeployRun, firstRelease.Release.ID, "v2")

	restartResponse := c.do(http.MethodPost, path, `{"operation":"restart"}`,
		map[string]string{"Idempotency-Key": "normalized-restart"})
	if restartResponse.Code != http.StatusAccepted {
		t.Fatalf("restart action = %d %s", restartResponse.Code, restartResponse.Body.String())
	}
	var restartRun deploy.EngineRun
	if err := json.Unmarshal(restartResponse.Body.Bytes(), &restartRun); err != nil {
		t.Fatal(err)
	}
	restartSteps, _ := s.modules.deployRuns.Steps(t.Context(), restartRun.ID)
	if restartRun.Operation != deploy.OperationRestart || restartRun.SlotClass != deploy.SlotLight ||
		len(restartSteps) != 5 || restartSteps[0].Key != deploy.StepStartCandidate ||
		restartRun.Metadata == nil || !strings.Contains(string(restartRun.Metadata), fmt.Sprintf(`"targetReleaseId":%d`, secondRelease.Release.ID)) {
		t.Fatalf("restart run/steps = %#v / %#v", restartRun, restartSteps)
	}
	if _, err := s.Store.DB.Exec("UPDATE deploy_runs SET state = 'cancelled', status = 'cancelled', ended_at = ? WHERE id = ?", time.Now().Unix(), restartRun.ID); err != nil {
		t.Fatal(err)
	}

	forceResponse := c.do(http.MethodPost, path, `{"operation":"force_build"}`,
		map[string]string{"Idempotency-Key": "normalized-force"})
	if forceResponse.Code != http.StatusAccepted {
		t.Fatalf("force-build action = %d %s", forceResponse.Code, forceResponse.Body.String())
	}
	var forceRun deploy.EngineRun
	if err := json.Unmarshal(forceResponse.Body.Bytes(), &forceRun); err != nil {
		t.Fatal(err)
	}
	forceSteps, _ := s.modules.deployRuns.Steps(t.Context(), forceRun.ID)
	if forceRun.Operation != deploy.OperationForceBuild || len(forceSteps) != len(deploy.DefaultStepKeys) ||
		forceSteps[4].Key != deploy.StepBuildArtifact {
		t.Fatalf("force-build run/steps = %#v / %#v", forceRun, forceSteps)
	}
	if _, err := s.Store.DB.Exec("UPDATE deploy_runs SET state = 'cancelled', status = 'cancelled', ended_at = ? WHERE id = ?", time.Now().Unix(), forceRun.ID); err != nil {
		t.Fatal(err)
	}

	releasesResponse := c.do(http.MethodGet,
		fmt.Sprintf("/api/v1/deploy/%d/environments/%d/releases", project.ID, environmentID), "", nil)
	if releasesResponse.Code != http.StatusOK {
		t.Fatalf("release list = %d %s", releasesResponse.Code, releasesResponse.Body.String())
	}
	var releases []deploy.Release
	if err := json.Unmarshal(releasesResponse.Body.Bytes(), &releases); err != nil || len(releases) != 2 ||
		releases[0].ID != secondRelease.Release.ID || releases[1].State != "retained" {
		t.Fatalf("release list = %#v, error=%v", releases, err)
	}

	rollbackResponse := c.do(http.MethodPost,
		fmt.Sprintf("/api/v1/deploy/%d/environments/%d/rollback", project.ID, environmentID),
		fmt.Sprintf(`{"releaseId":%d}`, firstRelease.Release.ID), nil)
	if rollbackResponse.Code != http.StatusAccepted {
		t.Fatalf("ordinary-confirmation rollback = %d %s", rollbackResponse.Code, rollbackResponse.Body.String())
	}
	var rollbackRun deploy.EngineRun
	if err := json.Unmarshal(rollbackResponse.Body.Bytes(), &rollbackRun); err != nil {
		t.Fatal(err)
	}
	rollbackSteps, _ := s.modules.deployRuns.Steps(t.Context(), rollbackRun.ID)
	if rollbackRun.Operation != deploy.OperationRollback || rollbackRun.Trigger != deploy.TriggerRollback ||
		rollbackRun.Priority != 1000 || rollbackRun.PlanRevision != firstRelease.Release.PlanRevision ||
		len(rollbackSteps) != 10 || rollbackSteps[0].Key != deploy.StepRenderRuntime {
		t.Fatalf("rollback run/steps = %#v / %#v", rollbackRun, rollbackSteps)
	}
	wrongRoute := c.do(http.MethodPost, path, fmt.Sprintf(`{"operation":"rollback","releaseId":%d}`, firstRelease.Release.ID), nil)
	if wrongRoute.Code != http.StatusBadRequest {
		t.Fatalf("rollback through generic route = %d %s", wrongRoute.Code, wrongRoute.Body.String())
	}
}

// Stop and start are manual-route actions shaped like restart: light slot,
// reduced steps, and admission refusals keyed to the recorded runtime state
// rather than the immutable release.
func TestNormalizedStopAndStartActionsUseTheLiveRuntimeState(t *testing.T) {
	c, s := newClient(t)
	project := createLegacyDeploymentFixture(t, s, "normalized-stop-start")
	environmentID := convertToNormalizedDeploymentFixture(t, s, project)
	path := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/runs", project.ID, environmentID)

	deployResponse := c.do(http.MethodPost, path, `{"operation":"deploy"}`,
		map[string]string{"Idempotency-Key": "stop-start-deploy"})
	if deployResponse.Code != http.StatusAccepted {
		t.Fatalf("deploy action = %d %s", deployResponse.Code, deployResponse.Body.String())
	}
	var firstRun deploy.EngineRun
	if err := json.Unmarshal(deployResponse.Body.Bytes(), &firstRun); err != nil {
		t.Fatal(err)
	}
	live := activateSyntheticNormalizedRelease(t, s, firstRun, "stop-start-v1")

	stopResponse := c.do(http.MethodPost, path, `{"operation":"stop"}`,
		map[string]string{"Idempotency-Key": "stop-start-stop"})
	if stopResponse.Code != http.StatusAccepted {
		t.Fatalf("stop action = %d %s", stopResponse.Code, stopResponse.Body.String())
	}
	var stopRun deploy.EngineRun
	if err := json.Unmarshal(stopResponse.Body.Bytes(), &stopRun); err != nil {
		t.Fatal(err)
	}
	stopSteps, err := s.modules.deployRuns.Steps(t.Context(), stopRun.ID)
	if err != nil || stopRun.Operation != deploy.OperationStop || stopRun.SlotClass != deploy.SlotLight ||
		len(stopSteps) != 3 || stopSteps[0].Key != deploy.StepStartCandidate ||
		stopSteps[1].Key != deploy.StepRecordRelease || stopSteps[2].Key != deploy.StepNotify ||
		!strings.Contains(string(stopRun.Metadata), fmt.Sprintf(`"targetReleaseId":%d`, live.Release.ID)) {
		t.Fatalf("stop run/steps = %#v / %#v, error=%v", stopRun, stopSteps, err)
	}
	// This test only exercises the enqueue shape and admission refusals, not
	// execution, the same way the restart case above is only ever cancelled.
	if _, err := s.Store.DB.Exec("UPDATE deploy_runs SET state = 'cancelled', status = 'cancelled', ended_at = ? WHERE id = ?", time.Now().Unix(), stopRun.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Store.DB.Exec("UPDATE deploy_release_runtimes SET state='stopped' WHERE release_id=?", live.Release.ID); err != nil {
		t.Fatal(err)
	}
	alreadyStoppedResponse := c.do(http.MethodPost, path, `{"operation":"stop"}`,
		map[string]string{"Idempotency-Key": "stop-start-already-stopped"})
	if alreadyStoppedResponse.Code != http.StatusConflict || !strings.Contains(alreadyStoppedResponse.Body.String(), "already_stopped") {
		t.Fatalf("stop over a stopped runtime = %d %s", alreadyStoppedResponse.Code, alreadyStoppedResponse.Body.String())
	}

	startResponse := c.do(http.MethodPost, path, `{"operation":"start"}`,
		map[string]string{"Idempotency-Key": "stop-start-start"})
	if startResponse.Code != http.StatusAccepted {
		t.Fatalf("start action = %d %s", startResponse.Code, startResponse.Body.String())
	}
	var startRun deploy.EngineRun
	if err := json.Unmarshal(startResponse.Body.Bytes(), &startRun); err != nil {
		t.Fatal(err)
	}
	startSteps, err := s.modules.deployRuns.Steps(t.Context(), startRun.ID)
	if err != nil || startRun.Operation != deploy.OperationStart || startRun.SlotClass != deploy.SlotLight ||
		len(startSteps) != 5 || startSteps[0].Key != deploy.StepStartCandidate {
		t.Fatalf("start run/steps = %#v / %#v, error=%v", startRun, startSteps, err)
	}
	if _, err := s.Store.DB.Exec("UPDATE deploy_runs SET state = 'cancelled', status = 'cancelled', ended_at = ? WHERE id = ?", time.Now().Unix(), startRun.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Store.DB.Exec("UPDATE deploy_release_runtimes SET state='live' WHERE release_id=?", live.Release.ID); err != nil {
		t.Fatal(err)
	}
	notStoppedResponse := c.do(http.MethodPost, path, `{"operation":"start"}`,
		map[string]string{"Idempotency-Key": "stop-start-not-stopped"})
	if notStoppedResponse.Code != http.StatusConflict || !strings.Contains(notStoppedResponse.Body.String(), "not_stopped") {
		t.Fatalf("start over a running runtime = %d %s", notStoppedResponse.Code, notStoppedResponse.Body.String())
	}

	legacyProject := createLegacyDeploymentFixture(t, s, "stop-start-legacy")
	legacyEnvironmentID, _, err := s.modules.deployRuns.ProductionEnvironment(t.Context(), legacyProject.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/runs", legacyProject.ID, legacyEnvironmentID)
	for _, operation := range []string{"stop", "start"} {
		response := c.do(http.MethodPost, legacyPath, fmt.Sprintf(`{"operation":%q}`, operation), nil)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("legacy %s = %d %s", operation, response.Code, response.Body.String())
		}
	}
}

// Stop and restart interrupt the live runtime exactly like
// POST /docker/containers/{id}/stop|restart, so they need the destructive
// capability on top of service.control even though this route also serves
// deploy and redeploy, which stay at service.control alone.
func TestDeploymentStopAndRestartRequireDestructiveCapability(t *testing.T) {
	s := testServer(t)
	project := createLegacyDeploymentFixture(t, s, "destructive-stop-restart")
	environmentID := convertToNormalizedDeploymentFixture(t, s, project)
	path := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/runs", project.ID, environmentID)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "destructive-admin", auth.RoleAdmin)}
	limited := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "destructive-limited", auth.RoleLimited)}

	deployResponse := admin.do(http.MethodPost, path, `{"operation":"deploy"}`,
		map[string]string{"Idempotency-Key": "destructive-deploy"})
	if deployResponse.Code != http.StatusAccepted {
		t.Fatalf("deploy action = %d %s", deployResponse.Code, deployResponse.Body.String())
	}
	var firstRun deploy.EngineRun
	if err := json.Unmarshal(deployResponse.Body.Bytes(), &firstRun); err != nil {
		t.Fatal(err)
	}
	activateSyntheticNormalizedRelease(t, s, firstRun, "destructive-v1")

	for _, operation := range []string{"stop", "restart"} {
		response := limited.do(http.MethodPost, path, fmt.Sprintf(`{"operation":%q}`, operation),
			map[string]string{"Idempotency-Key": "destructive-" + operation})
		if response.Code != http.StatusForbidden {
			t.Fatalf("service.control %s = %d %s, want 403", operation, response.Code, response.Body.String())
		}
		apiErr := decodedAPIError(t, response.Body.Bytes())
		if apiErr.Code != "forbidden" || !strings.Contains(apiErr.Message, "destructive") {
			t.Fatalf("%s error = %#v, want the capability middleware's forbidden shape naming destructive", operation, apiErr)
		}
	}

	deployAgain := limited.do(http.MethodPost, path, `{"operation":"deploy"}`,
		map[string]string{"Idempotency-Key": "destructive-deploy-again"})
	if deployAgain.Code != http.StatusAccepted {
		t.Fatalf("service.control deploy = %d %s, want 202", deployAgain.Code, deployAgain.Body.String())
	}
}

func convertToNormalizedDeploymentFixture(t *testing.T, s *Server, project *deploy.Project) int64 {
	t.Helper()
	environmentID, _, err := s.modules.deployRuns.ProductionEnvironment(t.Context(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	source := deploy.DraftSourceConfig{Kind: deploy.SourceLocal, Mode: deploy.SourceModeLocalCheckout, LocalPath: project.RepoPath}
	identity := deploy.SourceIdentity{Kind: deploy.SourceLocal, LocalPath: project.RepoPath, Revision: strings.Repeat("a", 40)}
	build := deploy.BuildPlanConfig{Method: deploy.BuildDockerfile, Dockerfile: "Dockerfile", Secrets: []deploy.BuildSecretConfig{}, ReleaseTasks: []deploy.ReleaseTaskConfig{}}
	runtime := deploy.RuntimePlanConfig{InternalPort: 3000, BindAddress: "127.0.0.1", Strategy: deploy.StrategyBlueGreen, Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []deploy.RuntimeMount{}}
	sourceJSON, _ := json.Marshal(source)
	identityJSON, _ := json.Marshal(identity)
	buildJSON, _ := json.Marshal(build)
	runtimeJSON, _ := json.Marshal(runtime)
	digest := "sha256:" + strings.Repeat("a", 64)
	now := time.Now().Unix()
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_sources(environment_id, revision, kind, config_json, identity_json, digest, created_at) VALUES(?, 2, 'local', ?, ?, ?, ?)`, environmentID, sourceJSON, identityJSON, digest, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at) VALUES(?, 2, 'dockerfile', ?, '{"candidates":[],"gitRequirements":{}}', '', ?, ?)`, environmentID, buildJSON, digest, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at) VALUES(?, 2, ?, '', ?, ?)`, environmentID, runtimeJSON, digest, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`UPDATE deploy_environments SET desired_revision = 2, strategy = 'blue_green', expected_downtime = 0 WHERE id = ?`, environmentID); err != nil {
		t.Fatal(err)
	}
	return environmentID
}

func activateSyntheticNormalizedRelease(t *testing.T, s *Server, run deploy.EngineRun, identity string) *deploy.ReleaseWithArtifacts {
	t.Helper()
	lease, err := s.modules.deployRuns.ClaimNext(t.Context(), "api-action-worker", deploy.QueueBudget{}, time.Minute)
	if err != nil || lease == nil || lease.RunID != run.ID {
		t.Fatalf("release claim = %#v, error=%v", lease, err)
	}
	claimed, err := s.modules.deployRuns.Run(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	imageDigest := "sha256:" + strings.Repeat("1", 64)
	configDigest := "sha256:" + strings.Repeat("c", 64)
	snapshot := json.RawMessage(fmt.Sprintf(`{"version":1,"plan":{"internalPort":3000,"bindAddress":"127.0.0.1","strategy":"blue_green","command":[],"capabilities":[],"devices":[],"mounts":[]},"image":{"reference":"example.test/api:current","digest":"%s","configDigest":"%s","platforms":["linux/amd64"]},"variables":[],"dependencies":[],"checks":[],"domains":[],"planInputsDigest":"","sourceIdentity":{"kind":"local","localPath":"%s","revision":"%s"}}`, imageDigest, configDigest, projectPathForRun(t, s, run.ProjectID), strings.Repeat("a", 40)))
	release, err := s.modules.deployRuns.CreateCandidateRelease(t.Context(), *claimed, lease.Token, deploy.CandidateReleaseInput{
		Artifacts:       []deploy.ReleaseArtifactInput{{Kind: deploy.ArtifactImage, Reference: "example.test/api:current", Digest: imageDigest, Metadata: json.RawMessage(`{"os":"linux","architecture":"amd64"}`), SizeBytes: 1024}},
		Prepared:        deploy.PreparedBuild{Method: deploy.BuildDockerfile, Dockerfile: "Dockerfile", DockerfileDigest: configDigest, BuildArgv: []string{"docker", "buildx", "build", "."}, BaseImages: []deploy.ResolvedImage{}, CachePolicy: "reuse", SecretIDs: []string{}},
		RuntimeSnapshot: snapshot,
	})
	if err != nil {
		t.Fatal(err)
	}
	finishSyntheticRelease(t, s, *claimed, *lease, release, "runtime-"+identity)
	return release
}

func cloneAndActivateSyntheticRelease(t *testing.T, s *Server, run deploy.EngineRun, targetID int64, identity string) *deploy.ReleaseWithArtifacts {
	t.Helper()
	lease, err := s.modules.deployRuns.ClaimNext(t.Context(), "api-redeploy-worker", deploy.QueueBudget{}, time.Minute)
	if err != nil || lease == nil || lease.RunID != run.ID {
		t.Fatalf("redeploy claim = %#v, error=%v", lease, err)
	}
	claimed, _ := s.modules.deployRuns.Run(t.Context(), run.ID)
	release, err := s.modules.deployRuns.CloneCandidateRelease(t.Context(), *claimed, lease.Token, targetID)
	if err != nil {
		t.Fatal(err)
	}
	finishSyntheticRelease(t, s, *claimed, *lease, release, "runtime-"+identity)
	return release
}

func finishSyntheticRelease(t *testing.T, s *Server, run deploy.EngineRun, lease deploy.QueueLease, release *deploy.ReleaseWithArtifacts, runtimeID string) {
	t.Helper()
	if _, err := s.modules.deployRuns.RecordCandidateRuntime(t.Context(), run, lease.Token, deploy.ReleaseRuntimeInput{
		ReleaseID: release.Release.ID, Kind: "container", RuntimeID: runtimeID,
		Host: "127.0.0.1", Port: 32001, Metadata: json.RawMessage(`{"version":1}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.modules.deployRuns.ActivateCandidate(t.Context(), run.ID, lease.Token, release.Release.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec("UPDATE deploy_runs SET state = 'succeeded', status = 'success', ended_at = ?, release_id = ? WHERE id = ?", time.Now().Unix(), release.Release.ID, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec("DELETE FROM deploy_queue_leases WHERE run_id = ?", run.ID); err != nil {
		t.Fatal(err)
	}
}

func projectPathForRun(t *testing.T, s *Server, projectID int64) string {
	t.Helper()
	project, err := s.modules.deployStore.Get(t.Context(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	return project.RepoPath
}

func TestDeploymentReadModelsExposeFleetDetailAndEngineRuns(t *testing.T) {
	c, s := newClient(t)
	project := createLegacyDeploymentFixture(t, s, "read-model-route")
	environmentID, revision, err := s.modules.deployRuns.ProductionEnvironment(t.Context(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.modules.deployRuns.Enqueue(t.Context(), deploy.RunRequest{
		ProjectID: project.ID, EnvironmentID: environmentID,
		Operation: deploy.OperationDeploy, Trigger: deploy.TriggerManual, Actor: "reader",
		RequestDigest: "read-model-route-request", PlanRevision: revision,
		SlotClass: deploy.SlotHeavy, Steps: []deploy.StepKey{deploy.StepLegacyPipeline},
	})
	if err != nil {
		t.Fatal(err)
	}

	fleetResponse := c.do(http.MethodGet, "/api/v1/deploy/?view=fleet", "", nil)
	if fleetResponse.Code != http.StatusOK {
		t.Fatalf("fleet = %d %s", fleetResponse.Code, fleetResponse.Body.String())
	}
	var fleet deploy.FleetReadModel
	if err := json.Unmarshal(fleetResponse.Body.Bytes(), &fleet); err != nil {
		t.Fatal(err)
	}
	if len(fleet.Deployments) != 1 || len(fleet.ActiveWork) != 1 ||
		fleet.Deployments[0].BuildMethod != deploy.BuildLegacyCompose ||
		fleet.Deployments[0].Health != "unavailable" || !fleet.Deployments[0].PendingChanges ||
		fleet.ActiveWork[0].Run.ID != run.ID {
		t.Fatalf("fleet read model = %#v", fleet)
	}

	detailResponse := c.do(http.MethodGet,
		fmt.Sprintf("/api/v1/deploy/%d", project.ID), "", nil)
	if detailResponse.Code != http.StatusOK {
		t.Fatalf("detail = %d %s", detailResponse.Code, detailResponse.Body.String())
	}
	var detail struct {
		Project    deploy.Project           `json:"project"`
		Running    bool                     `json:"running"`
		Deployment deploy.DeploymentSummary `json:"deployment"`
		Runtime    deploy.RuntimeServices   `json:"runtime"`
	}
	if err := json.Unmarshal(detailResponse.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Project.ID != project.ID || !detail.Running ||
		detail.Deployment.EnvironmentID != environmentID ||
		detail.Deployment.ActiveRun == nil || detail.Deployment.ActiveRun.ID != run.ID {
		t.Fatalf("deployment detail = %#v", detail)
	}
	if detail.Runtime.Status != "unavailable" || detail.Runtime.Reason == "" || detail.Runtime.Services == nil {
		t.Fatalf("missing Docker must remain unavailable: %+v", detail.Runtime)
	}

	runsResponse := c.do(http.MethodGet,
		fmt.Sprintf("/api/v1/deploy/%d/runs?view=engine", project.ID), "", nil)
	if runsResponse.Code != http.StatusOK {
		t.Fatalf("engine runs = %d %s", runsResponse.Code, runsResponse.Body.String())
	}
	var runs struct {
		Runs    []deploy.EngineRun `json:"runs"`
		Running bool               `json:"running"`
	}
	if err := json.Unmarshal(runsResponse.Body.Bytes(), &runs); err != nil {
		t.Fatal(err)
	}
	if len(runs.Runs) != 1 || runs.Runs[0].ID != run.ID || !runs.Running {
		t.Fatalf("engine runs read model = %#v", runs)
	}
}

// The engine run list's query params are a closed vocabulary: a real filter
// narrows the rows, and a garbage state value is refused rather than quietly
// matching nothing.
func TestDeploymentRunsEngineViewFiltersAndRejectsAnUnknownState(t *testing.T) {
	s := testServer(t)
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	project := createLegacyDeploymentFixture(t, s, "runs-filter")
	environmentID, revision, err := s.modules.deployRuns.ProductionEnvironment(t.Context(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	var oldestRunID int64
	for i := 0; i < 3; i++ {
		run, _, err := s.modules.deployRuns.Enqueue(t.Context(), deploy.RunRequest{
			ProjectID: project.ID, EnvironmentID: environmentID,
			Operation: deploy.OperationDeploy, Trigger: deploy.TriggerManual, Actor: "tester",
			RequestDigest: fmt.Sprintf("runs-filter-%d", i), PlanRevision: revision,
			SlotClass: deploy.SlotLight, Steps: []deploy.StepKey{deploy.StepLegacyPipeline},
		})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			oldestRunID = run.ID
		}
	}
	path := fmt.Sprintf("/api/v1/deploy/%d/runs?view=engine", project.ID)

	bad := c.do(http.MethodGet, path+"&state=not-a-state", "", nil)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("garbage state = %d %s", bad.Code, bad.Body.String())
	}

	badOperation := c.do(http.MethodGet, path+"&operation=not-an-operation", "", nil)
	if badOperation.Code != http.StatusBadRequest {
		t.Fatalf("garbage operation = %d %s", badOperation.Code, badOperation.Body.String())
	}

	limited := c.do(http.MethodGet, path+"&limit=2", "", nil)
	if limited.Code != http.StatusOK {
		t.Fatalf("limited runs = %d %s", limited.Code, limited.Body.String())
	}
	var page struct {
		Runs       []deploy.EngineRun `json:"runs"`
		NextBefore int64              `json:"nextBefore"`
	}
	if err := json.Unmarshal(limited.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Runs) != 2 || page.NextBefore == 0 {
		t.Fatalf("limited page = %#v, want 2 rows and a cursor for the third", page)
	}

	rest := c.do(http.MethodGet, fmt.Sprintf("%s&limit=2&before=%d", path, page.NextBefore), "", nil)
	if rest.Code != http.StatusOK {
		t.Fatalf("next page = %d %s", rest.Code, rest.Body.String())
	}
	var restPage struct {
		Runs       []deploy.EngineRun `json:"runs"`
		NextBefore int64              `json:"nextBefore"`
	}
	if err := json.Unmarshal(rest.Body.Bytes(), &restPage); err != nil {
		t.Fatal(err)
	}
	if len(restPage.Runs) != 1 || restPage.Runs[0].ID != oldestRunID || restPage.NextBefore != 0 {
		t.Fatalf("final page = %#v, want the oldest run and no further cursor", restPage)
	}

	byState := c.do(http.MethodGet, path+"&state=queued", "", nil)
	if byState.Code != http.StatusOK {
		t.Fatalf("state filter = %d %s", byState.Code, byState.Body.String())
	}
	var stateBody struct {
		Runs []deploy.EngineRun `json:"runs"`
	}
	if err := json.Unmarshal(byState.Body.Bytes(), &stateBody); err != nil {
		t.Fatal(err)
	}
	if len(stateBody.Runs) != 3 {
		t.Fatalf("queued-state filter = %#v, want all 3 still-queued runs", stateBody.Runs)
	}
}

func TestDeploymentRunStreamSendsSnapshotBacklogLiveTerminalAndAuditsOpen(t *testing.T) {
	s := testServer(t)
	cookie := signIn(t, s)
	project := createLegacyDeploymentFixture(t, s, "stream-route")
	environmentID, revision, err := s.modules.deployRuns.ProductionEnvironment(t.Context(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.modules.deployRuns.Enqueue(t.Context(), deploy.RunRequest{
		ProjectID: project.ID, EnvironmentID: environmentID,
		Operation: deploy.OperationDeploy, Trigger: deploy.TriggerManual, Actor: "tester",
		RequestDigest: "stream-route-request", PlanRevision: revision,
		SlotClass: deploy.SlotHeavy, Steps: []deploy.StepKey{deploy.StepLegacyPipeline},
	})
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(s.Routes())
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + fmt.Sprintf(
		"/api/v1/deploy/%d/runs/%d/stream?after=0", project.ID, run.ID)
	conn, response, err := websocket.DefaultDialer.Dial(url, http.Header{"Cookie": {cookie}})
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("open deployment stream (%d): %v", status, err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	kind, payload := readFrame(t, conn)
	if kind != "snapshot" {
		t.Fatalf("first frame = %q, want snapshot", kind)
	}
	var initial deploy.RunSnapshot
	if err := json.Unmarshal(payload, &initial); err != nil {
		t.Fatal(err)
	}
	if initial.Run.ID != run.ID || initial.Run.State != deploy.RunQueued {
		t.Fatalf("initial snapshot = %#v", initial)
	}
	kind, payload = readFrame(t, conn)
	if kind != "events" {
		t.Fatalf("second frame = %q, want events", kind)
	}
	var backlog []deploy.RunEvent
	if err := json.Unmarshal(payload, &backlog); err != nil {
		t.Fatal(err)
	}
	if len(backlog) != 3 || backlog[0].Seq != 1 || backlog[2].Seq != 3 {
		t.Fatalf("initial event backlog = %#v", backlog)
	}

	if _, err := s.modules.deployRuns.RequestCancellation(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	kind, payload = readFrame(t, conn)
	if kind != "events" {
		t.Fatalf("terminal event frame = %q", kind)
	}
	var terminalEvents []deploy.RunEvent
	if err := json.Unmarshal(payload, &terminalEvents); err != nil {
		t.Fatal(err)
	}
	if len(terminalEvents) != 2 || terminalEvents[0].Seq != 4 || terminalEvents[1].Seq != 5 {
		t.Fatalf("terminal events = %#v", terminalEvents)
	}
	kind, payload = readFrame(t, conn)
	if kind != "snapshot" {
		t.Fatalf("final frame = %q, want snapshot", kind)
	}
	var final deploy.RunSnapshot
	if err := json.Unmarshal(payload, &final); err != nil {
		t.Fatal(err)
	}
	if final.Run.State != deploy.RunCancelled {
		t.Fatalf("final stream state = %s", final.Run.State)
	}

	var auditCount int
	if err := s.Store.DB.QueryRow(`
		SELECT COUNT(*) FROM audit_log WHERE action = ? AND target = ?`,
		"deploy.run.stream.open", strconv.FormatInt(run.ID, 10)).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("stream open audit count = %d, want 1", auditCount)
	}
}

func createLegacyDeploymentFixture(t *testing.T, s *Server, name string) *deploy.Project {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	project, _, err := s.modules.deployStore.Create(t.Context(), &deploy.Project{
		Name: name, RepoPath: repo, Branch: "main", ComposeFile: "compose.yml", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return project
}

// Preview and schedule lifecycle operations have their own authorized entry
// points; the manual route must not become a side door for them.
func TestManualRunRouteRefusesLifecycleOperations(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "lifecycle-admin", auth.RoleAdmin)}
	path := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/runs", projectID, environmentID)
	for _, operation := range []string{"preview_create", "preview_update", "preview_remove", "scheduled", "bogus"} {
		response := admin.do(http.MethodPost, path, fmt.Sprintf(`{"operation":%q}`, operation), nil)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d %s", operation, response.Code, response.Body.String())
		}
	}
	var count int
	_ = s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_runs WHERE project_id=?`, projectID).Scan(&count)
	if count != 0 {
		t.Fatalf("refused operations persisted %d run(s)", count)
	}
}

// A rolled-back deployment kept the previous release serving, but the chain's
// deploy step did not happen; the chain must stop rather than restart what the
// operator meant to replace.
func TestScheduledChainTreatsRollbackAsFailure(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "chain-admin", auth.RoleAdmin)}
	path := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/runs", projectID, environmentID)
	response := admin.do(http.MethodPost, path, `{"operation":"deploy"}`, nil)
	if response.Code != http.StatusAccepted {
		t.Fatalf("create run = %d %s", response.Code, response.Body.String())
	}
	var run deploy.EngineRun
	if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`UPDATE deploy_runs SET state='rolled_back', status='failed', ended_at=? WHERE id=?`, time.Now().Unix(), run.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.waitForScheduledRun(t.Context(), run.ID); err == nil || !strings.Contains(err.Error(), "rolled_back") {
		t.Fatalf("rolled back chain step = %v, want failure", err)
	}
	if _, err := s.Store.DB.Exec(`UPDATE deploy_runs SET state='succeeded', status='success' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.waitForScheduledRun(t.Context(), run.ID); err != nil {
		t.Fatalf("succeeded chain step = %v", err)
	}
}

func TestDeploymentInsightsRouteReadsProjectHistory(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "insights-admin", auth.RoleAdmin)}
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "insights-reader", auth.RoleReadOnly)}
	response := admin.do(http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/runs", projectID, environmentID), `{"operation":"deploy"}`, nil)
	if response.Code != http.StatusAccepted {
		t.Fatalf("create run = %d %s", response.Code, response.Body.String())
	}
	var run deploy.EngineRun
	_ = json.Unmarshal(response.Body.Bytes(), &run)
	now := time.Now().Unix()
	if _, err := s.Store.DB.Exec(`UPDATE deploy_runs SET state='failed', status='failed', claimed_at=?, ended_at=?, terminal_code='build_failed' WHERE id=?`, now-90, now, run.ID); err != nil {
		t.Fatal(err)
	}
	insights := reader.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/insights?days=14", projectID), "", nil)
	if insights.Code != http.StatusOK {
		t.Fatalf("insights = %d %s", insights.Code, insights.Body.String())
	}
	var body deploy.DeploymentInsights
	if err := json.Unmarshal(insights.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.WindowDays != 14 || body.Runs != 1 || body.Failed != 1 || body.FailureStreak != 1 || len(body.Daily) != 15 || len(body.TopFailures) != 1 || body.TopFailures[0].Code != "build_failed" {
		t.Fatalf("insights body = %+v", body)
	}
	if missing := reader.do(http.MethodGet, "/api/v1/deploy/999999/insights", "", nil); missing.Code != http.StatusNotFound {
		t.Fatalf("unknown project insights = %d", missing.Code)
	}
}
