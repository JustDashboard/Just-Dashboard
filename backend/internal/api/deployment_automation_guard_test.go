package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

func TestObservedDeploymentAutomationRejectsScheduleAndTriggerWrites(t *testing.T) {
	c, s := newClient(t)
	project := observedAutomationProject(t, s)
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", project.ProjectID, project.EnvironmentID)
	schedule := observedAutomationSchedule()
	trigger := observedAutomationTrigger()
	for _, request := range []struct {
		path string
		body any
	}{
		{base + "/schedules", schedule}, {base + "/triggers", trigger},
	} {
		response := doPlanningJSON(t, c, http.MethodPost, request.path, request.body)
		if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "original manager") {
			t.Fatalf("create %s = %d %s", request.path, response.Code, response.Body.String())
		}
	}
	assertAutomationRows(t, s, "deploy_schedules", 0)
	assertAutomationRows(t, s, "deploy_schedule_steps", 0)
	assertAutomationRows(t, s, "deploy_triggers", 0)

	// Retained automation predating the observation contract must not be
	// enabled or rewritten through an imported project's API.
	schedule.Enabled, trigger.Enabled = false, false
	oldSchedule, err := s.modules.deployAutomation.CreateSchedule(t.Context(), project.ProjectID, project.EnvironmentID, schedule)
	if err != nil {
		t.Fatal(err)
	}
	oldTrigger, err := s.modules.deployAutomation.CreateTrigger(t.Context(), project.ProjectID, project.EnvironmentID, trigger)
	if err != nil {
		t.Fatal(err)
	}
	schedule.Enabled, trigger.Enabled = true, true
	for _, request := range []struct {
		path string
		body any
	}{
		{fmt.Sprintf("%s/schedules/%d", base, oldSchedule.ID), schedule},
		{fmt.Sprintf("%s/triggers/%d", base, oldTrigger.Trigger.ID), trigger},
	} {
		response := doPlanningJSON(t, c, http.MethodPut, request.path, request.body)
		if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "original manager") {
			t.Fatalf("update %s = %d %s", request.path, response.Code, response.Body.String())
		}
	}
	var enabledSchedules, enabledTriggers int
	if err := s.Store.DB.QueryRow(`SELECT count(*) FROM deploy_schedules WHERE enabled = 1`).Scan(&enabledSchedules); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.DB.QueryRow(`SELECT count(*) FROM deploy_triggers WHERE enabled = 1`).Scan(&enabledTriggers); err != nil {
		t.Fatal(err)
	}
	if enabledSchedules != 0 || enabledTriggers != 0 {
		t.Fatalf("refused automation became enabled: schedules=%d triggers=%d", enabledSchedules, enabledTriggers)
	}
	assertAutomationRows(t, s, "deploy_schedules", 1)
	assertAutomationRows(t, s, "deploy_triggers", 1)
	assertAutomationRows(t, s, "deploy_runs", 0)
}

func TestObservedDeploymentAutomationRefusesDispatchBeforeDockerExec(t *testing.T) {
	s := testServer(t)
	project := observedAutomationProject(t, s)
	var mutations atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("API-Version", "1.47")
		if strings.HasSuffix(r.URL.Path, "/_ping") {
			fmt.Fprint(w, "OK")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			mutations.Add(1)
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer daemon.Close()
	s.modules.docker = dockerx.New(daemon.URL)
	defer s.modules.docker.Close()
	item := deploy.ScheduleDispatch{
		Schedule: deploy.Schedule{
			ID: 1, ProjectID: project.ProjectID, EnvironmentID: project.EnvironmentID,
			Name: "retained command", Steps: observedAutomationSchedule().Steps,
		},
		DueAt: time.Now().UTC(),
	}
	if err := s.dispatchDeploymentSchedule(t.Context(), item); !errors.Is(err, deploy.ErrInvalidPlan) {
		t.Fatalf("observed schedule dispatch error=%v, want ErrInvalidPlan", err)
	}
	if mutations.Load() != 0 {
		t.Fatalf("observed schedule issued %d Docker mutations before refusing its run", mutations.Load())
	}
	assertAutomationRows(t, s, "deploy_runs", 0)
	assertAutomationRows(t, s, "deploy_schedules", 0)
}

func TestObservedDeploymentAutomationRefusesProviderBeforePreviewCreation(t *testing.T) {
	s := testServer(t)
	project := observedAutomationProject(t, s)
	request := observedAutomationTrigger()
	request.Config.Preview = true
	request.Config.Events = []string{"pull_request"}
	created, err := s.modules.deployAutomation.CreateTrigger(t.Context(), project.ProjectID, project.EnvironmentID, request)
	if err != nil {
		t.Fatal(err)
	}
	event := deploy.ProviderEvent{
		DeliveryID: "observed-preview", Event: "pull_request", Repository: "acme/app", Action: "opened",
		Revision: strings.Repeat("a", 40), PreviewNumber: 7, PreviewRef: "refs/pull/7/head",
	}
	if _, err := s.dispatchAutomationEvent(t.Context(), &created.Trigger, event, []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "original manager") {
		t.Fatalf("observed provider dispatch error=%v, want original-manager refusal", err)
	}
	assertAutomationRows(t, s, "deploy_preview_approvals", 0)
	assertAutomationRows(t, s, "deploy_preview_refs", 0)
	assertAutomationRows(t, s, "deploy_environments", 1)
	assertAutomationRows(t, s, "deploy_runs", 0)
	var status string
	if err := s.Store.DB.QueryRow(`SELECT status FROM deploy_webhook_deliveries WHERE delivery_id = 'observed-preview'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "rejected" {
		t.Fatalf("observed webhook delivery status=%q", status)
	}
}

func TestObservedDeploymentAutomationKeepsExistingCheckoutSchedules(t *testing.T) {
	c, s := newClient(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	source, err := json.Marshal(deploy.DraftSourceConfig{
		Kind: deploy.SourceImport, Mode: deploy.SourceModeExistingCheckout, LocalPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := json.Marshal(deploy.SourceIdentity{
		Kind: deploy.SourceImport, Repository: "checkout-app", Revision: strings.Repeat("a", 40),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`
		INSERT INTO deploy_sources(environment_id, revision, kind, config_json, identity_json, digest, created_at)
		VALUES(?, 2, ?, ?, ?, ?, 1)`, environmentID, deploy.SourceImport, string(source), string(identity), "sha256:"+strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`
		INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at)
		 SELECT environment_id, 2, method, config_json, evidence_json, preview, digest, created_at
		   FROM deploy_build_plans WHERE environment_id = ? AND revision = 1`, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`
		INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at)
		 SELECT environment_id, 2, config_json, preview, digest, created_at
		   FROM deploy_runtime_plans WHERE environment_id = ? AND revision = 1`, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`UPDATE deploy_environments SET desired_revision = 2 WHERE id = ?`, environmentID); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, environmentID)
	request := observedAutomationSchedule()
	request.Enabled = false
	request.Steps = []deploy.ScheduleStep{{Action: "game_command", Config: json.RawMessage(`{"command":"save-all"}`), Required: true}}
	response := doPlanningJSON(t, c, http.MethodPost, base+"/schedules", request)
	if response.Code != http.StatusCreated {
		t.Fatalf("existing checkout schedule=%d %s", response.Code, response.Body.String())
	}
	var schedule deploy.Schedule
	if err := json.Unmarshal(response.Body.Bytes(), &schedule); err != nil {
		t.Fatal(err)
	}
	request.Name = "updated checkout schedule"
	response = doPlanningJSON(t, c, http.MethodPut, fmt.Sprintf("%s/schedules/%d", base, schedule.ID), request)
	if response.Code != http.StatusOK {
		t.Fatalf("existing checkout schedule update=%d %s", response.Code, response.Body.String())
	}
	schedule.Name = request.Name
	if err := s.dispatchDeploymentSchedule(t.Context(), deploy.ScheduleDispatch{Schedule: schedule, DueAt: time.Now().UTC()}); err != nil {
		t.Fatalf("existing checkout schedule dispatch: %v", err)
	}
	assertAutomationRows(t, s, "deploy_runs", 1)
}

func observedAutomationProject(t *testing.T, s *Server) *deploy.DraftCommitResult {
	t.Helper()
	project, err := s.modules.deployPlanning.RegisterObservedWorkload(t.Context(), deploy.ObservedWorkloadRegistration{
		Name: "external-app", ResourceKind: "docker_container", ResourceID: "external-container",
		SourceMode: deploy.SourceModeExistingContainer, OwnerUsername: "tester",
		Observed: json.RawMessage(`{"key":"container:external-container","kind":"container","resourceId":"external-container"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return project
}

func observedAutomationSchedule() deploy.ScheduleWrite {
	return deploy.ScheduleWrite{
		Name: "external maintenance", Expression: "0 3 * * *", Timezone: "UTC", Enabled: true,
		Steps: []deploy.ScheduleStep{{
			Action: "container_command", Required: true,
			Config: json.RawMessage(`{"containerId":"external-container","argv":["touch","/external-data/changed"]}`),
		}},
	}
}

func observedAutomationTrigger() deploy.TriggerWrite {
	return deploy.TriggerWrite{
		Name: "external provider", Kind: "github", Provider: "github", Enabled: true,
		Config: deploy.TriggerConfig{Repository: "acme/app", Ref: "main", Events: []string{"push"}},
	}
}

func assertAutomationRows(t *testing.T, s *Server, table string, expected int) {
	t.Helper()
	var count int
	if err := s.Store.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != expected {
		t.Fatalf("%s rows=%d, want %d", table, count, expected)
	}
}
