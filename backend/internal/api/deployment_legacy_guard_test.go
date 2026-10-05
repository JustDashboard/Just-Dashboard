package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

func TestObservedDeploymentLegacyRefusesConfigurationAndExecution(t *testing.T) {
	c, s := newClient(t)
	imported := observedAutomationProject(t, s)
	path := fmt.Sprintf("/api/v1/deploy/%d", imported.ProjectID)
	request := deployProjectRequest{
		Name: "renamed-external", RepoPath: t.TempDir(), Branch: "main", ComposeFile: "compose.yml",
		PreCommand: "touch /external-data/changed", PostCommand: "echo changed", Enabled: true,
	}
	response := doPlanningJSON(t, c, http.MethodPut, path, request)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "original manager") {
		t.Fatalf("legacy configuration update=%d %s", response.Code, response.Body.String())
	}
	project, err := s.modules.deployStore.Get(t.Context(), imported.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if project.Name != "external-app" || project.Enabled || project.RepoPath != "" || project.Branch != "" ||
		project.ComposeFile != "" || project.PreCommand != "" || project.PostCommand != "" {
		t.Fatalf("refused legacy configuration changed imported project: %#v", project)
	}
	response = c.do(http.MethodPut, path, `{"name":"renamed-external"}`, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("imported project rename=%d %s", response.Code, response.Body.String())
	}
	response = c.do(http.MethodPost, path+"/rollback", `{"commit":"aaaaaaaa"}`, nil)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "original manager") {
		t.Fatalf("legacy rollback=%d %s", response.Code, response.Body.String())
	}
	assertAutomationRows(t, s, "deploy_runs", 0)
	assertAutomationRows(t, s, "deploy_steps", 0)

	// Older records can retain enabled legacy hooks and commands. Refuse at
	// queue admission even when the hook authenticates and its fields are valid.
	if _, err := s.Store.DB.Exec(`
		UPDATE deploy_projects SET enabled = 1, repo_path = ?, branch = 'main', compose_file = 'compose.yml',
		 pre_command = 'touch /external-data/changed' WHERE id = ?`, request.RepoPath, imported.ProjectID); err != nil {
		t.Fatal(err)
	}
	secret, err := s.modules.deployStore.RotateSecret(t.Context(), imported.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	guest := &client{t: t, h: s.Routes()}
	payload := `{"ref":"refs/heads/main"}`
	response = guest.do(http.MethodPost, "/api/v1/hooks/deploy/"+project.HookID, payload, map[string]string{
		"X-Hub-Signature-256": signProviderPayload([]byte(payload), secret),
	})
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "original manager") {
		t.Fatalf("authenticated legacy hook=%d %s", response.Code, response.Body.String())
	}
	assertAutomationRows(t, s, "deploy_runs", 0)
	assertAutomationRows(t, s, "deploy_steps", 0)
}

func TestObservedDeploymentLegacyKeepsExistingCheckoutCompatibility(t *testing.T) {
	c, s := newClient(t)
	project := createLegacyDeploymentFixture(t, s, "checkout-legacy")
	environmentID, _, err := s.modules.deployRuns.ProductionEnvironment(t.Context(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	source, err := json.Marshal(deploy.DraftSourceConfig{
		Kind: deploy.SourceImport, Mode: deploy.SourceModeExistingCheckout, LocalPath: project.RepoPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`
		INSERT INTO deploy_sources(environment_id, revision, kind, config_json, identity_json, digest, created_at)
		VALUES(?, 2, ?, ?, '{}', ?, 1)`, environmentID, deploy.SourceImport, string(source), "sha256:"+strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`UPDATE deploy_environments SET desired_revision = 2 WHERE id = ?`, environmentID); err != nil {
		t.Fatal(err)
	}
	response := doPlanningJSON(t, c, http.MethodPut, fmt.Sprintf("/api/v1/deploy/%d", project.ID), deployProjectRequest{
		Name: project.Name, RepoPath: project.RepoPath, Branch: "develop", ComposeFile: "other.yml", Enabled: true,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("existing checkout legacy configuration=%d %s", response.Code, response.Body.String())
	}
	response = c.do(http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/rollback", project.ID), `{"commit":"aaaaaaaa"}`, nil)
	if response.Code != http.StatusAccepted {
		t.Fatalf("existing checkout legacy enqueue=%d %s", response.Code, response.Body.String())
	}
	assertAutomationRows(t, s, "deploy_runs", 1)
	assertAutomationRows(t, s, "deploy_steps", 1)
}

func TestObservedDeploymentLegacyRefusesRetryOfRetainedCompatibilityRun(t *testing.T) {
	c, s := newClient(t)
	imported := observedAutomationProject(t, s)
	prior, _, err := s.modules.deployRuns.Enqueue(t.Context(), deploy.RunRequest{
		ProjectID: imported.ProjectID, EnvironmentID: imported.EnvironmentID, PlanRevision: 1,
		Operation: deploy.OperationDeploy, Trigger: deploy.TriggerLegacyHook, Actor: "retained-fixture",
		RequestDigest: "retained-legacy-run", Priority: 500, SlotClass: deploy.SlotHeavy,
		Metadata: json.RawMessage(`{"compatibility":true}`), Steps: []deploy.StepKey{deploy.StepLegacyPipeline},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.modules.deployEngine.Cancel(t.Context(), prior.ID); err != nil {
		t.Fatal(err)
	}
	response := c.do(http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/runs/%d/retry", imported.ProjectID, prior.ID), `{}`, nil)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "original manager") {
		t.Fatalf("retained legacy run retry=%d %s", response.Code, response.Body.String())
	}
	assertAutomationRows(t, s, "deploy_runs", 1)
	assertAutomationRows(t, s, "deploy_steps", 1)
}
