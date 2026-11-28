package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks/media"
)

// seedOlderStack is a Complete stack deployed from version 1.1.0 of its
// template, from before FlareSolverr came with it: its source names 1.1.0,
// its Compose file has no flaresolverr service, and a release of it is live.
func seedOlderStack(t *testing.T, s *Server) (int64, int64, int64) {
	t.Helper()
	projectID, environmentID := seedStackDeploymentFrom(t, s, "media", media.TemplateComplete, "complete",
		func(deploy.PlannedDomain) bool { return true }, func(source *deploy.DraftSourceConfig) {
			source.BlueprintVersion = "1.1.0"
			for index, document := range source.ComposeFiles {
				var file map[string]any
				if err := yaml.Unmarshal([]byte(document.Content), &file); err != nil {
					t.Fatal(err)
				}
				services, _ := file["services"].(map[string]any)
				if _, found := services["flaresolverr"]; !found {
					t.Fatalf("the shipped template's file has no flaresolverr service: %v", services)
				}
				delete(services, "flaresolverr")
				content, err := yaml.Marshal(file)
				if err != nil {
					t.Fatal(err)
				}
				source.ComposeFiles[index].Content = string(content)
			}
		})
	releaseID := seedStackLiveRelease(t, s, projectID, environmentID, 1, "media-complete@1.1.0")
	return projectID, environmentID, releaseID
}

// seedStackLiveRelease makes a release of the source at revision live, as a run
// that succeeded leaves it.
func seedStackLiveRelease(t *testing.T, s *Server, projectID, environmentID int64, revision int, template string) int64 {
	t.Helper()
	db := s.Store.DB
	var sourceID, buildID, runtimeID int64
	if err := db.QueryRow(`SELECT (SELECT id FROM deploy_sources WHERE environment_id = ? AND revision = ?),
		(SELECT id FROM deploy_build_plans WHERE environment_id = ? ORDER BY revision DESC LIMIT 1),
		(SELECT id FROM deploy_runtime_plans WHERE environment_id = ? ORDER BY revision DESC LIMIT 1)`,
		environmentID, revision, environmentID, environmentID).Scan(&sourceID, &buildID, &runtimeID); err != nil {
		t.Fatal(err)
	}
	deployed, err := db.Exec(`INSERT INTO deploy_runs(project_id, environment_id, started_at, ended_at, status, state) VALUES(?, ?, 1, 2, 'success', 'succeeded')`, projectID, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	runID, _ := deployed.LastInsertId()
	if _, err := db.Exec(`INSERT INTO deploy_run_plan_snapshots(run_id, environment_id, digest, created_at) VALUES(?, ?, ?, 1)`, runID, environmentID, fmt.Sprintf("sha256:plan-%d", runID)); err != nil {
		t.Fatal(err)
	}
	var number int
	_ = db.QueryRow(`SELECT COUNT(*) + 1 FROM deploy_releases WHERE environment_id = ?`, environmentID).Scan(&number)
	release, err := db.Exec(`INSERT INTO deploy_releases(project_id, environment_id, release_number, plan_revision, run_id, source_id, build_plan_id, runtime_plan_id, source_identity_json, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`, projectID, environmentID, number, revision, runID, sourceID, buildID, runtimeID, fmt.Sprintf(`{"template":%q}`, template))
	if err != nil {
		t.Fatal(err)
	}
	releaseID, _ := release.LastInsertId()
	for _, statement := range []string{
		`INSERT INTO deploy_release_runtimes(release_id, environment_id, kind, runtime_id, created_at, updated_at) VALUES(?, ?, 'compose', 'jd-media', 1, 1)`,
		`UPDATE deploy_environments SET live_release_id = ? WHERE id = ?`,
		`UPDATE deploy_stacks SET live_release_id = ? WHERE environment_id = ?`,
	} {
		if _, err := db.Exec(statement, releaseID, environmentID); err != nil {
			t.Fatal(err)
		}
	}
	return releaseID
}

// A stack deployed before FlareSolverr came with it is not shown a broken
// FlareSolverr: it is not among the apps or the steps, and the state names it
// as new. "Add FlareSolverr" saves the source at the shipped version, as the
// Source settings do (audited as a source update), and starts the release;
// until that release is live the same press deploys again, and once a
// release that runs it is live there is nothing to add.
func TestStackUpgradeAddsTheAppsTheTemplateGained(t *testing.T) {
	s := testServer(t)
	wireDeployPlanningAndSources(t, s, []string{t.TempDir()})
	s.modules.stackStore = stacks.NewStore(s.Store.DB, s.Sealer)
	s.modules.stacks = s.newStackService()
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	projectID, environmentID, _ := seedOlderStack(t, s)
	path := fmt.Sprintf("/api/v1/deploy/%d/stack", projectID)
	read := func() stackState {
		t.Helper()
		response := c.do(http.MethodGet, path, "", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("state = %d %s", response.Code, response.Body.String())
		}
		var state stackState
		if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		return state
	}

	state := read()
	if state.Upgrade == nil || state.Upgrade.Version != "1.2.0" || state.Upgrade.From != "1.1.0" || state.Upgrade.Saved ||
		len(state.Upgrade.Added) != 1 || state.Upgrade.Added[0].Service != "flaresolverr" || state.Upgrade.Added[0].Title != "FlareSolverr" {
		t.Fatalf("upgrade = %+v", state.Upgrade)
	}
	for _, service := range state.Services {
		if service.Name == "flaresolverr" {
			t.Fatal("FlareSolverr is listed among the apps of a release that does not run it")
		}
	}
	for _, step := range state.Steps {
		if step.Service == "flaresolverr" {
			t.Fatalf("a step for FlareSolverr is listed: %+v", step)
		}
	}
	if len(state.Services) != 8 {
		t.Fatalf("%d apps listed", len(state.Services))
	}

	response := c.do(http.MethodPost, path+"/upgrade", "", nil)
	if response.Code != http.StatusAccepted {
		t.Fatalf("upgrade = %d %s", response.Code, response.Body.String())
	}
	var answer stackUpgradeAnswer
	if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil || answer.RunID == 0 || answer.Version != "1.2.0" ||
		!slices.Equal(answer.Added, []string{"flaresolverr"}) {
		t.Fatalf("answer = %+v %v", answer, err)
	}
	configuration, err := s.modules.deployPlanning.EnvironmentConfiguration(t.Context(), projectID, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Revision != 2 || configuration.Source == nil || configuration.Source.BlueprintVersion != "1.2.0" ||
		configuration.Source.BlueprintInputs["domain"] != "media.example.test" {
		t.Fatalf("configuration revision %d source %+v", configuration.Revision, configuration.Source)
	}
	if len(configuration.Source.ComposeFiles) != 1 || !strings.Contains(configuration.Source.ComposeFiles[0].Content, "flaresolverr") {
		t.Fatal("the saved source is not the shipped version's render")
	}
	if len(configuration.Domains) != 7 {
		t.Fatalf("the domains changed: %+v", configuration.Domains)
	}
	run, err := s.modules.deployRuns.Run(t.Context(), answer.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Operation != deploy.OperationDeploy || run.EnvironmentID != environmentID || run.PlanRevision != 2 {
		t.Fatalf("run = %+v", run)
	}
	for _, action := range []string{"deploy.source.update", "deploy.run.request", "deploy.stack.upgrade"} {
		var audited int
		if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = ?`, action).Scan(&audited); err != nil || audited != 1 {
			t.Fatalf("%s audited %d times: %v", action, audited, err)
		}
	}

	// Saved, but the live release is still the old one: the same press
	// deploys again, without another source revision.
	state = read()
	if state.Upgrade == nil || !state.Upgrade.Saved || state.Upgrade.From != "1.1.0" || len(state.Upgrade.Added) != 1 {
		t.Fatalf("upgrade while the release runs = %+v", state.Upgrade)
	}
	again := c.do(http.MethodPost, path+"/upgrade", "", nil)
	if again.Code != http.StatusAccepted {
		t.Fatalf("deploy again = %d %s", again.Code, again.Body.String())
	}
	var revisions int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_sources WHERE environment_id = ?`, environmentID).Scan(&revisions); err != nil || revisions != 2 {
		t.Fatalf("%d source revisions after deploying again: %v", revisions, err)
	}

	// A release of the shipped version is live: FlareSolverr is an app of
	// the stack, and nothing is left to add.
	seedStackLiveRelease(t, s, projectID, environmentID, 2, "media-complete@1.2.0")
	state = read()
	if state.Upgrade != nil || len(state.Services) != 9 || !slices.ContainsFunc(state.Steps, func(step stackStepState) bool {
		return step.ID == "prowlarr.flaresolverr"
	}) {
		t.Fatalf("after the release: upgrade %+v, %d apps", state.Upgrade, len(state.Services))
	}
	refused := c.do(http.MethodPost, path+"/upgrade", "", nil)
	if refused.Code != http.StatusConflict || !strings.Contains(refused.Body.String(), "stack_upgrade_none") {
		t.Fatalf("a press with nothing to add = %d %s", refused.Code, refused.Body.String())
	}
}

// The stack loop leaves out the steps of a service the live release does not
// run, and runs them once a release that does is live.
func TestStackReleaseServicesAreTheReleasesComposeFile(t *testing.T) {
	s := testServer(t)
	wireDeployPlanningAndSources(t, s, []string{t.TempDir()})
	s.modules.stackStore = stacks.NewStore(s.Store.DB, s.Sealer)
	projectID, environmentID, releaseID := seedOlderStack(t, s)
	manifest := media.Manifest()
	preset, _ := manifest.PresetByID("complete")
	live := stacks.Live{ReleaseID: releaseID, Kind: "compose", RuntimeID: "jd-media", TemplateID: media.TemplateComplete, TemplateVersion: "1.1.0"}
	running, err := s.stackReleaseServices(t.Context(), preset, live)
	if err != nil {
		t.Fatal(err)
	}
	if len(running) != 8 || running["flaresolverr"] || !running["prowlarr"] {
		t.Fatalf("running = %v", running)
	}

	source := deploy.DraftSourceConfig{Kind: deploy.SourceCompose, Mode: deploy.SourceModeBlueprint,
		BlueprintID: media.TemplateComplete, BlueprintVersion: "1.2.0", BlueprintInputs: map[string]string{"domain": "media.example.test"}}
	source, err = source.WithTemplateDocuments()
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(source)
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_sources(environment_id, revision, kind, config_json, credential_id, identity_json, digest, created_at)
		VALUES(?, 2, 'compose', ?, 0, '{}', 'sha256:upgraded', 1)`, environmentID, string(encoded)); err != nil {
		t.Fatal(err)
	}
	live.ReleaseID = seedStackLiveRelease(t, s, projectID, environmentID, 2, "media-complete@1.2.0")
	if running, err := s.stackReleaseServices(t.Context(), preset, live); err != nil || running != nil {
		t.Fatalf("a release of the shipped version: %v %v", running, err)
	}
}
