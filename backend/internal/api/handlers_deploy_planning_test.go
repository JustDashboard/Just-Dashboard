package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

func TestDeploymentPlanningSignedInJourneyPersistsWithoutDeploying(t *testing.T) {
	s := testServer(t)
	checkout := planningGitCheckout(t)
	s.modules.deployPlanning = deploy.NewPlanningStore(s.Store, s.Sealer, []string{checkout})
	s.modules.deploySources = deploy.NewHostSourceAnalyzer(
		[]string{checkout}, []string{checkout}, filepath.Join(s.Cfg.DataDir, "planning-test-cache"),
		nil, s.modules.deployPlanning,
	)
	s.modules.deployPreflight = deploy.NewHostPreflightObserver([]string{checkout}, s.Cfg.DataDir, nil)
	client := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}

	created := client.do(http.MethodPost, "/api/v1/deploy/drafts", `{}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create draft = %d %s", created.Code, created.Body.String())
	}
	var draft deploy.Draft
	decodePlanningResponse(t, created.Body.Bytes(), &draft)
	if draft.ID == "" || draft.Revision != 1 {
		t.Fatalf("created draft = %#v", draft)
	}

	draft = saveDraftThroughAPI(t, client, draft, deploy.DraftSaveRequest{
		Revision: draft.Revision, Step: deploy.DraftIntent,
		Intent: &deploy.DraftIntentConfig{Name: "api-planned-worker", Profile: deploy.ProfileWorker},
	})
	draft = saveDraftThroughAPI(t, client, draft, deploy.DraftSaveRequest{
		Revision: draft.Revision, Step: deploy.DraftSource,
		Source: &deploy.DraftSourceConfig{
			Kind: deploy.SourceGit, Mode: deploy.SourceModeLocalCheckout,
			LocalPath: checkout, Subdirectory: "worker",
		},
	})
	unknownSelection := doPlanningJSON(t, client, http.MethodPost,
		"/api/v1/deploy/drafts/"+draft.ID+"/detect", map[string]any{
			"revision": draft.Revision, "selectedId": "candidate-that-was-not-detected",
		})
	if unknownSelection.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(unknownSelection.Body.String(), `"code":"invalid_plan"`) {
		t.Fatalf("unknown detection selection = %d %s",
			unknownSelection.Code, unknownSelection.Body.String())
	}

	stale := doPlanningJSON(t, client, http.MethodPut, "/api/v1/deploy/drafts/"+draft.ID, deploy.DraftSaveRequest{
		Revision: draft.Revision - 1, Step: deploy.DraftIntent,
		Intent: &deploy.DraftIntentConfig{Name: "stale", Profile: deploy.ProfileWorker},
	})
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), `"code":"draft_revision_conflict"`) {
		t.Fatalf("stale draft save = %d %s", stale.Code, stale.Body.String())
	}

	detected := doPlanningJSON(t, client, http.MethodPost,
		"/api/v1/deploy/drafts/"+draft.ID+"/detect", map[string]int{"revision": draft.Revision})
	if detected.Code != http.StatusOK {
		t.Fatalf("detect draft = %d %s", detected.Code, detected.Body.String())
	}
	decodePlanningResponse(t, detected.Body.Bytes(), &draft)
	if draft.Data.Detection == nil || draft.Data.Detection.Source.Revision == "" ||
		len(draft.Data.Detection.Candidates) != 1 || draft.Data.Detection.SelectedID == "" {
		t.Fatalf("detected draft = %#v", draft.Data.Detection)
	}

	configuration := deploy.PlanConfiguration{
		Build:   deploy.BuildPlanConfig{Method: deploy.BuildNone},
		Runtime: deploy.RuntimePlanConfig{Strategy: deploy.StrategyStopFirst},
		Variables: []deploy.PlannedVariable{{
			Name: "API_TOKEN", Sensitivity: "secret", Scopes: []string{"runtime"},
			Required: true, Reference: "${{credential.api-token}}",
		}},
		Dependencies: []deploy.PlannedDependency{{
			Kind: "cache", Ownership: deploy.OwnershipLinked, ResourceKind: "redis", ResourceID: "cache-1",
		}},
		Checks: []deploy.PlannedCheck{{
			Name: "worker-smoke", Kind: "command", Phase: "smoke", Required: true,
			Config: json.RawMessage(`{"command":["worker","check"]}`),
		}},
		AutoDeploy: true,
	}
	draft = saveDraftThroughAPI(t, client, draft, deploy.DraftSaveRequest{
		Revision: draft.Revision, Step: deploy.DraftConfiguration, Configuration: &configuration,
	})

	preflighted := doPlanningJSON(t, client, http.MethodPost,
		"/api/v1/deploy/drafts/"+draft.ID+"/preflight", map[string]int{"revision": draft.Revision})
	if preflighted.Code != http.StatusOK {
		t.Fatalf("preflight draft = %d %s", preflighted.Code, preflighted.Body.String())
	}
	var preflightBody struct {
		Draft     deploy.Draft           `json:"draft"`
		Preflight deploy.PreflightResult `json:"preflight"`
	}
	decodePlanningResponse(t, preflighted.Body.Bytes(), &preflightBody)
	draft = preflightBody.Draft
	if preflightBody.Preflight.Digest == "" || len(preflightBody.Preflight.Plan.Actions) != 16 || draft.PlanPreview == "" {
		t.Fatalf("preflight response = %#v", preflightBody)
	}
	for _, finding := range preflightBody.Preflight.Findings {
		if finding.Severity == deploy.PreflightBlocked || finding.Severity == deploy.PreflightDecision {
			t.Fatalf("journey preflight unexpectedly blocked: %#v", finding)
		}
	}

	committed := doPlanningJSON(t, client, http.MethodPost,
		"/api/v1/deploy/drafts/"+draft.ID+"/commit", deploy.DraftCommitRequest{Revision: draft.Revision})
	if committed.Code != http.StatusCreated {
		t.Fatalf("commit draft = %d %s", committed.Code, committed.Body.String())
	}
	var result deploy.DraftCommitResult
	decodePlanningResponse(t, committed.Body.Bytes(), &result)
	if !result.Created || result.ProjectID == 0 || result.EnvironmentID == 0 {
		t.Fatalf("commit result = %#v", result)
	}
	replay := doPlanningJSON(t, client, http.MethodPost,
		"/api/v1/deploy/drafts/"+draft.ID+"/commit", deploy.DraftCommitRequest{Revision: draft.Revision})
	if replay.Code != http.StatusOK {
		t.Fatalf("commit replay = %d %s", replay.Code, replay.Body.String())
	}
	var replayResult deploy.DraftCommitResult
	decodePlanningResponse(t, replay.Body.Bytes(), &replayResult)
	if replayResult.Created || replayResult.ProjectID != result.ProjectID {
		t.Fatalf("commit replay result = %#v", replayResult)
	}

	for _, path := range []string{
		"/api/v1/deploy/" + fmt.Sprint(result.ProjectID) + "/run",
		"/api/v1/deploy/" + fmt.Sprint(result.ProjectID) + "/environments/" +
			fmt.Sprint(result.EnvironmentID) + "/runs",
	} {
		body := ""
		if strings.Contains(path, "/environments/") {
			body = `{"operation":"deploy"}`
		}
		response := client.do(http.MethodPost, path, body, nil)
		if response.Code != http.StatusAccepted {
			t.Fatalf("normalized execution start %s = %d %s", path, response.Code, response.Body.String())
		}
		var queued struct {
			ID    int64           `json:"id"`
			RunID int64           `json:"runId"`
			State deploy.RunState `json:"state"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &queued); err != nil ||
			(queued.ID == 0 && queued.RunID == 0) || queued.State != deploy.RunQueued {
			t.Fatalf("normalized queued run %s = %#v, error=%v", path, queued, err)
		}
	}

	preview := doPlanningJSON(t, client, http.MethodPost, "/api/v1/deploy/import/preview", deploy.DraftSourceConfig{
		Kind: deploy.SourceImport, Mode: deploy.SourceModeExistingCheckout, LocalPath: checkout,
	})
	if preview.Code != http.StatusOK {
		t.Fatalf("checkout import preview = %d %s", preview.Code, preview.Body.String())
	}
	var importResult deploy.ImportPreview
	decodePlanningResponse(t, preview.Body.Bytes(), &importResult)
	if len(importResult.WouldChange) != 0 || importResult.Kind != "checkout" {
		t.Fatalf("checkout import preview = %#v", importResult)
	}

	for table, want := range map[string]int{
		"deploy_projects": 1, "deploy_environments": 1, "deploy_sources": 1,
		"deploy_build_plans": 1, "deploy_runtime_plans": 1, "deploy_runs": 2,
	} {
		var count int
		if err := s.Store.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Errorf("%s rows = %d, want %d", table, count, want)
		}
	}

	expectedAudits := map[string]int{
		"deploy.draft.create": 1, "deploy.draft.save": 3, "deploy.draft.detect": 1,
		"deploy.draft.preflight": 1, "deploy.create": 2, "deploy.import.preview": 1,
		"deploy.run": 1, "deploy.run.request": 1,
	}
	for action, want := range expectedAudits {
		var count int
		if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = ?`, action).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Errorf("audit %s count = %d, want %d", action, count, want)
		}
	}
	var combined string
	if err := s.Store.DB.QueryRow(`
		SELECT COALESCE(group_concat(detail || ' ' || target), '')
		  FROM audit_log WHERE action LIKE 'deploy.%'`).Scan(&combined); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(combined, "Correct-Horse") || strings.Contains(combined, "plain-secret") {
		t.Fatalf("deployment audits contain credential material: %s", combined)
	}
}

func TestDeploymentDraftRoutesRequireSessionsAndHideOtherOwners(t *testing.T) {
	s := testServer(t)
	adminCookie := signInAs(t, s, "planning-admin", auth.RoleAdmin)
	readonlyCookie := signInAs(t, s, "planning-reader", auth.RoleReadOnly)
	routes := s.Routes()
	admin := &client{t: t, h: routes, cookie: adminCookie}
	reader := &client{t: t, h: routes, cookie: readonlyCookie}

	created := admin.do(http.MethodPost, "/api/v1/deploy/drafts", `{}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("admin create = %d %s", created.Code, created.Body.String())
	}
	var adminDraft deploy.Draft
	decodePlanningResponse(t, created.Body.Bytes(), &adminDraft)
	other := reader.do(http.MethodGet, "/api/v1/deploy/drafts/"+adminDraft.ID, "", nil)
	if other.Code != http.StatusNotFound {
		t.Fatalf("other-owner read = %d %s", other.Code, other.Body.String())
	}

	var readonlyID int64
	if err := s.Store.DB.QueryRow(`SELECT id FROM users WHERE username = 'planning-reader'`).Scan(&readonlyID); err != nil {
		t.Fatal(err)
	}
	owned, err := s.modules.deployPlanning.Create(t.Context(), readonlyID, "planning-reader")
	if err != nil {
		t.Fatal(err)
	}
	ownRead := reader.do(http.MethodGet, "/api/v1/deploy/drafts/"+owned.ID, "", nil)
	if ownRead.Code != http.StatusOK {
		t.Fatalf("owner read = %d %s", ownRead.Code, ownRead.Body.String())
	}
	adminRead := admin.do(http.MethodGet, "/api/v1/deploy/drafts/"+owned.ID, "", nil)
	if adminRead.Code != http.StatusOK {
		t.Fatalf("admin read other draft = %d %s", adminRead.Code, adminRead.Body.String())
	}

	var adminID int64
	if err := s.Store.DB.QueryRow(`SELECT id FROM users WHERE username = 'planning-admin'`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	adminUser, err := s.Auth.UserByID(t.Context(), adminID)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.Auth.CreateAPIToken(t.Context(), adminUser, "planning-test", auth.RoleAdmin, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tokenClient := &client{t: t, h: routes}
	for _, request := range []struct {
		method, path, body string
	}{
		{http.MethodPost, "/api/v1/deploy/drafts", `{}`},
		{http.MethodGet, "/api/v1/deploy/drafts/" + adminDraft.ID, ""},
		{http.MethodPost, "/api/v1/deploy/import/preview", `{}`},
		{http.MethodPost, "/api/v1/deploy/import/adopt", `{}`},
	} {
		response := tokenClient.do(request.method, request.path, request.body,
			map[string]string{"Authorization": "Bearer " + token})
		if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"session_required"`) {
			t.Errorf("token %s %s = %d %s", request.method, request.path, response.Code, response.Body.String())
		}
	}
}

func TestDeploymentImportAdoptionUsesDedicatedConfirmedDraftPathWithoutRuntimeSideEffects(t *testing.T) {
	s := testServer(t)
	checkout := planningGitCheckout(t)
	s.modules.deployPlanning = deploy.NewPlanningStore(s.Store, s.Sealer, []string{checkout})
	s.modules.deploySources = deploy.NewHostSourceAnalyzer(
		[]string{checkout}, []string{checkout}, filepath.Join(s.Cfg.DataDir, "adoption-test-cache"),
		nil, s.modules.deployPlanning,
	)
	s.modules.deployPreflight = deploy.NewHostPreflightObserver([]string{checkout}, s.Cfg.DataDir, nil)
	client := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}

	created := client.do(http.MethodPost, "/api/v1/deploy/drafts", `{}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create import draft = %d %s", created.Code, created.Body.String())
	}
	var draft deploy.Draft
	decodePlanningResponse(t, created.Body.Bytes(), &draft)
	draft = saveDraftThroughAPI(t, client, draft, deploy.DraftSaveRequest{
		Revision: draft.Revision, Step: deploy.DraftIntent,
		Intent: &deploy.DraftIntentConfig{Name: "adopted-checkout", Profile: deploy.ProfileImported},
	})
	source := deploy.DraftSourceConfig{
		Kind: deploy.SourceImport, Mode: deploy.SourceModeExistingCheckout, LocalPath: checkout,
	}
	previewResponse := doPlanningJSON(t, client, http.MethodPost, "/api/v1/deploy/import/preview", source)
	if previewResponse.Code != http.StatusOK {
		t.Fatalf("import preview = %d %s", previewResponse.Code, previewResponse.Body.String())
	}
	var preview deploy.ImportPreview
	decodePlanningResponse(t, previewResponse.Body.Bytes(), &preview)
	if len(preview.WouldChange) != 0 {
		t.Fatalf("read-only preview promised changes: %#v", preview.WouldChange)
	}
	draft = saveDraftThroughAPI(t, client, draft, deploy.DraftSaveRequest{
		Revision: draft.Revision, Step: deploy.DraftSource, Source: &source,
	})
	detected := doPlanningJSON(t, client, http.MethodPost,
		"/api/v1/deploy/drafts/"+draft.ID+"/detect", map[string]int{"revision": draft.Revision})
	if detected.Code != http.StatusOK {
		t.Fatalf("detect import = %d %s", detected.Code, detected.Body.String())
	}
	decodePlanningResponse(t, detected.Body.Bytes(), &draft)
	// Adoption records the observed checkout without claiming it can already be
	// built by this Docker-less API fixture.
	preview.Configuration.Build = deploy.BuildPlanConfig{Method: deploy.BuildNone}
	draft = saveDraftThroughAPI(t, client, draft, deploy.DraftSaveRequest{
		Revision: draft.Revision, Step: deploy.DraftConfiguration, Configuration: &preview.Configuration,
	})
	preflighted := doPlanningJSON(t, client, http.MethodPost,
		"/api/v1/deploy/drafts/"+draft.ID+"/preflight", map[string]int{"revision": draft.Revision})
	if preflighted.Code != http.StatusOK {
		t.Fatalf("preflight import = %d %s", preflighted.Code, preflighted.Body.String())
	}
	var checked struct {
		Draft     deploy.Draft           `json:"draft"`
		Preflight deploy.PreflightResult `json:"preflight"`
	}
	decodePlanningResponse(t, preflighted.Body.Bytes(), &checked)
	draft = checked.Draft
	warnings := []string{}
	for _, finding := range checked.Preflight.Findings {
		if finding.Severity == deploy.PreflightBlocked || finding.Severity == deploy.PreflightDecision {
			t.Fatalf("import adoption unexpectedly blocked: %#v", finding)
		}
		if finding.Severity == deploy.PreflightWarning {
			warnings = append(warnings, finding.Code)
		}
	}
	generic := doPlanningJSON(t, client, http.MethodPost,
		"/api/v1/deploy/drafts/"+draft.ID+"/commit", deploy.DraftCommitRequest{
			Revision: draft.Revision, AcknowledgedWarnings: warnings,
		})
	if generic.Code != http.StatusBadRequest || !strings.Contains(generic.Body.String(), `"code":"import_adopt_required"`) {
		t.Fatalf("generic import commit = %d %s", generic.Code, generic.Body.String())
	}
	adopted := doPlanningJSON(t, client, http.MethodPost, "/api/v1/deploy/import/adopt", importAdoptRequest{
		DraftID: draft.ID, Revision: draft.Revision, AcknowledgedWarnings: warnings,
		AcknowledgedUnsupported: preview.Unsupported,
	})
	if adopted.Code != http.StatusCreated {
		t.Fatalf("adopt import = %d %s", adopted.Code, adopted.Body.String())
	}
	var result deploy.DraftCommitResult
	decodePlanningResponse(t, adopted.Body.Bytes(), &result)
	if !result.Created || result.ProjectID == 0 {
		t.Fatalf("adoption result = %#v", result)
	}
	for _, table := range []string{"deploy_runs", "deploy_releases", "deploy_release_runtimes"} {
		var count int
		if err := s.Store.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("adoption changed %s: count=%d err=%v", table, count, err)
		}
	}
	if _, err := os.Stat(filepath.Join(checkout, "worker", "go.mod")); err != nil {
		t.Fatalf("adoption changed observed checkout: %v", err)
	}
	var sourceKind string
	if err := s.Store.DB.QueryRow(`SELECT kind FROM deploy_sources WHERE environment_id = ?`, result.EnvironmentID).Scan(&sourceKind); err != nil || sourceKind != "import" {
		t.Fatalf("adopted source kind=%q err=%v", sourceKind, err)
	}
	var audits int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'deploy.import.adopt' AND target = ?`, preview.ResourceID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("adoption audits=%d err=%v", audits, err)
	}
}

func TestDeploymentPlanningValidationUsesClosedSecretSafeErrors(t *testing.T) {
	s := testServer(t)
	client := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	created := client.do(http.MethodPost, "/api/v1/deploy/drafts", `{}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create draft = %d %s", created.Code, created.Body.String())
	}
	var draft deploy.Draft
	decodePlanningResponse(t, created.Body.Bytes(), &draft)

	tests := []struct {
		name string
		body deploy.DraftSaveRequest
		code string
	}{
		{
			name: "git credentials", code: "invalid_source",
			body: deploy.DraftSaveRequest{
				Revision: draft.Revision, Step: deploy.DraftSource,
				Source: &deploy.DraftSourceConfig{
					Kind: deploy.SourceGit, Mode: deploy.SourceModeGitURL,
					URL: "https://token:do-not-return@example.test/owner/repo.git",
				},
			},
		},
		{
			name: "image", code: "invalid_image",
			body: deploy.DraftSaveRequest{
				Revision: draft.Revision, Step: deploy.DraftSource,
				Source: &deploy.DraftSourceConfig{
					Kind: deploy.SourceImage, Mode: deploy.SourceModeImageReference,
					Image: "INVALID IMAGE do-not-return",
				},
			},
		},
		{
			name: "plan secret", code: "invalid_plan",
			body: deploy.DraftSaveRequest{
				Revision: draft.Revision, Step: deploy.DraftConfiguration,
				Configuration: &deploy.PlanConfiguration{
					Build:   deploy.BuildPlanConfig{Method: deploy.BuildNone, BuildCommand: "API_TOKEN=do-not-return run"},
					Runtime: deploy.RuntimePlanConfig{Strategy: deploy.StrategyStopFirst},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := doPlanningJSON(t, client, http.MethodPut, "/api/v1/deploy/drafts/"+draft.ID, test.body)
			if response.Code == http.StatusInternalServerError || !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) {
				t.Fatalf("validation response = %d %s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "do-not-return") {
				t.Fatalf("validation response leaked rejected material: %s", response.Body.String())
			}
		})
	}
}

func saveDraftThroughAPI(t *testing.T, client *client, draft deploy.Draft, request deploy.DraftSaveRequest) deploy.Draft {
	t.Helper()
	response := doPlanningJSON(t, client, http.MethodPut, "/api/v1/deploy/drafts/"+draft.ID, request)
	if response.Code != http.StatusOK {
		t.Fatalf("save %s = %d %s", request.Step, response.Code, response.Body.String())
	}
	var saved deploy.Draft
	decodePlanningResponse(t, response.Body.Bytes(), &saved)
	if saved.Revision != draft.Revision+1 || saved.CurrentStep != request.Step {
		t.Fatalf("saved %s draft = %#v", request.Step, saved)
	}
	return saved
}

func doPlanningJSON(t *testing.T, client *client, method, path string, value any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return client.do(method, path, string(body), nil)
}

func decodePlanningResponse(t *testing.T, body []byte, destination any) {
	t.Helper()
	if err := json.Unmarshal(body, destination); err != nil {
		t.Fatalf("decode response: %v: %s", err, body)
	}
}

func planningGitCheckout(t *testing.T) string {
	t.Helper()
	checkout := t.TempDir()
	if err := os.MkdirAll(filepath.Join(checkout, "worker"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "worker", "go.mod"), []byte("module example.test/api-worker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "api-test@example.test"},
		{"config", "user.name", "API Planning Test"},
		{"remote", "add", "origin", "https://example.test/owner/api-worker.git"},
		{"add", "."},
		{"commit", "-q", "-m", "fixture"},
	} {
		command := exec.Command("git", append([]string{"-C", checkout}, args...)...)
		command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
		}
	}
	return checkout
}
