package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/blueprint"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks/media"
)

// A stack is an ordinary deployment of its preset's template: a draft takes
// the preset as it takes any template, committing it registers the stack, its page waits for a live release,
// Retry says so in words, and the accounts it creates are revealed only by
// name, audited, never an internal token.
func TestStackRoutesRegisterAWaitingStackAndRevealOnlyItsAccounts(t *testing.T) {
	s := testServer(t)
	wireDeployPlanningAndSources(t, s, []string{t.TempDir()})
	s.modules.stackStore = stacks.NewStore(s.Store.DB, s.Sealer)
	s.modules.stacks = s.newStackService()
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	call := func(method, path, body string, want int) []byte {
		t.Helper()
		response := c.do(method, "/api/v1"+path, body, nil)
		if response.Code != want {
			t.Fatalf("%s %s = %d %s; want %d", method, path, response.Code, response.Body.String(), want)
		}
		return response.Body.Bytes()
	}

	var catalogue []stackCatalogueEntry
	if err := json.Unmarshal(call(http.MethodGet, "/deploy/stacks", "", http.StatusOK), &catalogue); err != nil {
		t.Fatal(err)
	}
	if len(catalogue) != 1 || catalogue[0].ID != media.ID || len(catalogue[0].Presets) != 2 {
		t.Fatalf("catalogue = %+v", catalogue)
	}
	complete := catalogue[0].Presets[0]
	if complete.Template != media.TemplateComplete || !complete.Recommended || complete.MemoryMB <= 0 ||
		complete.TemplateVersion == "" || len(complete.Limitations) == 0 {
		t.Fatalf("complete preset = %+v", complete)
	}
	labels := map[string]string{}
	for _, service := range catalogue[0].Services {
		if service.Image == "" {
			t.Fatalf("service %s has no image", service.Name)
		}
		labels[service.Name] = service.Address
		if service.Primary != (service.Name == "jellyfin") {
			t.Fatalf("service %s primary = %v", service.Name, service.Primary)
		}
	}
	if labels["seerr"] != "requests" || labels["qbittorrent"] != "downloads" || labels["bazarr"] != "subtitles" ||
		labels["jellyfin"] != "" || labels["recyclarr"] != "" {
		t.Fatalf("address labels = %v", labels)
	}

	// The Template tab is told which templates are a stack's.
	var summaries []blueprint.Summary
	if err := json.Unmarshal(call(http.MethodGet, "/deploy/blueprints/", "", http.StatusOK), &summaries); err != nil {
		t.Fatal(err)
	}
	marked := 0
	for _, summary := range summaries {
		if summary.Stack == media.ID {
			marked++
		}
	}
	if marked != 2 {
		t.Fatalf("%d templates are marked as the media stack's", marked)
	}

	// Commit a draft of the Essential preset as the Stacks tab does.
	var draft deploy.Draft
	_ = json.Unmarshal(call(http.MethodPost, "/deploy/drafts", "{}", http.StatusCreated), &draft)
	_ = json.Unmarshal(call(http.MethodPut, "/deploy/drafts/"+draft.ID, fmt.Sprintf(
		`{"revision":%d,"step":"intent","intent":{"name":"media","profile":"compose"}}`, draft.Revision), http.StatusOK), &draft)
	_ = json.Unmarshal(call(http.MethodPut, "/deploy/drafts/"+draft.ID, fmt.Sprintf(
		`{"revision":%d,"step":"source","source":{"kind":"compose","mode":"blueprint","blueprintId":%q,"blueprintVersion":"1.1.0","blueprintInputs":{"domain":"media.example.test","request-approval":"admin"}}}`,
		draft.Revision, media.TemplateEssential), http.StatusOK), &draft)
	if draft.Data.Source == nil || draft.Data.Source.BlueprintID != media.TemplateEssential || len(draft.Data.Source.ComposeFiles) != 1 {
		t.Fatalf("the draft did not take the preset's template: %+v", draft.Data.Source)
	}
	// Committing runs preflight, which needs Docker; the hook the commit
	// calls is what registers the stack, so it is called as the commit calls
	// it, for a project as the commit makes one.
	now := time.Now().Unix()
	inserted, err := s.Store.DB.Exec(`INSERT INTO deploy_projects (name, repo_path, hook_secret, hook_id, created_at) VALUES ('media', '', 'x', 'hook-media', ?)`, now)
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := inserted.LastInsertId()
	result := &deploy.DraftCommitResult{ProjectID: projectID, EnvironmentID: 424242, Created: true}
	request := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(httpx.WithPrincipal(context.Background(),
		&httpx.Principal{User: &auth.User{Username: "tester"}, Kind: "session"}))
	s.registerStack(request, &draft, result)
	s.registerStack(request, &draft, result) // a replayed commit is one stack
	other := draft
	otherSource := *draft.Data.Source
	otherSource.BlueprintID = "whoami"
	other.Data.Source = &otherSource
	s.registerStack(request, &other, &deploy.DraftCommitResult{ProjectID: projectID + 1000, EnvironmentID: 1})
	var registered int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_stacks`).Scan(&registered); err != nil || registered != 1 {
		t.Fatalf("%d stacks registered: %v", registered, err)
	}
	path := fmt.Sprintf("/deploy/%d/stack", result.ProjectID)

	var state stackState
	if err := json.Unmarshal(call(http.MethodGet, path, "", http.StatusOK), &state); err != nil {
		t.Fatal(err)
	}
	if state.StackID != media.ID || state.Preset != "essential" || state.Status != stacks.StatusWaiting ||
		state.Readiness.Installed || len(state.Services) != 2 || !strings.Contains(state.Summary, "still bringing") {
		t.Fatalf("state = %+v", state)
	}
	for _, step := range state.Steps {
		if step.Service != "jellyfin" && step.Service != "seerr" {
			t.Fatalf("an Essential stack lists %s's step %s", step.Service, step.ID)
		}
	}
	// One login for every app, and nothing else a person signs in with.
	if len(state.Credentials) != 1 || state.Credentials[0].Name != media.CredentialLogin || state.Credentials[0].Service != "" ||
		state.Credentials[0].Username != "admin" || state.Credentials[0].Stored || state.Login.Username != "admin" || state.Login.Stored {
		t.Fatalf("credential listing = %+v, login = %+v", state.Credentials, state.Login)
	}

	// Retry before there is anything to configure says why.
	refused := c.do(http.MethodPost, "/api/v1"+path+"/configure", "{}", nil)
	if refused.Code != http.StatusConflict || !strings.Contains(refused.Body.String(), "stack_waiting") {
		t.Fatalf("configure before live = %d %s", refused.Code, refused.Body.String())
	}

	// Reveal: an account once its step stored it; never an internal token.
	store := s.modules.stackStore.Credentials(result.ProjectID)
	if err := store.Put(context.Background(), media.CredentialLogin, "admin-password-value"); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), media.CredentialJellyfinAPIKey, "internal-token-value"); err != nil {
		t.Fatal(err)
	}
	revealed := call(http.MethodGet, path+"/credentials/"+media.CredentialLogin+"/reveal", "", http.StatusOK)
	if !strings.Contains(string(revealed), "admin-password-value") || !strings.Contains(string(revealed), `"username":"admin"`) {
		t.Fatalf("reveal = %s", revealed)
	}
	call(http.MethodGet, path+"/credentials/"+media.CredentialJellyfinAPIKey+"/reveal", "", http.StatusNotFound)
	call(http.MethodGet, path+"/credentials/"+media.CredentialLoginPrevious+"/reveal", "", http.StatusNotFound)
	call(http.MethodGet, path+"/credentials/nothing/reveal", "", http.StatusNotFound)
	var audited int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'deploy.stack.credential.reveal'`).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("reveals audited = %d %v", audited, err)
	}
	var leaked int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE detail LIKE '%admin-password-value%'`).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("the audit log carries the password: %d %v", leaked, err)
	}
	var sealed string
	if err := s.Store.DB.QueryRow(`SELECT sealed FROM deploy_stack_credentials WHERE name = ?`, media.CredentialLogin).Scan(&sealed); err != nil ||
		strings.Contains(sealed, "admin-password-value") {
		t.Fatalf("the credential is stored in the clear: %q %v", sealed, err)
	}
	listed := string(call(http.MethodGet, path, "", http.StatusOK))
	if strings.Contains(listed, "admin-password-value") || strings.Contains(listed, "internal-token-value") {
		t.Fatalf("the state carries a secret: %s", listed)
	}

	// A project that is not a stack has no stack page.
	call(http.MethodGet, "/deploy/999999/stack", "", http.StatusNotFound)
}

// The stack's public service is reached at the deployment's own address, as
// the Overview opens it; a private one never is, and nothing is offered
// before the release is live.
func TestStackStateGivesThePublicServiceTheDeploymentsAddress(t *testing.T) {
	s := testServer(t)
	s.modules.stackStore = stacks.NewStore(s.Store.DB, s.Sealer)
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	projectID, environmentID := seedStackProject(t, s)
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_dependencies(environment_id, kind, ownership, resource_kind, resource_id, created_at) VALUES(?, 'domain', 'managed', 'proxy_site', 'media.example.test', 1)`, environmentID); err != nil {
		t.Fatal(err)
	}
	read := func() map[string]stackServiceState {
		t.Helper()
		response := c.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/stack", projectID), "", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("stack = %d %s", response.Code, response.Body.String())
		}
		var state stackState
		if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		services := map[string]stackServiceState{}
		for _, service := range state.Services {
			services[service.Name] = service
		}
		return services
	}

	if waiting := read(); waiting["jellyfin"].URL != "" {
		t.Fatalf("a stack with no live release offers %q", waiting["jellyfin"].URL)
	}
	if _, err := s.Store.DB.Exec(`UPDATE deploy_environments SET live_release_id = 99 WHERE id = ?`, environmentID); err != nil {
		t.Fatal(err)
	}
	live := read()
	if live["jellyfin"].URL != "https://media.example.test" {
		t.Fatalf("jellyfin = %+v; want the deployment's address", live["jellyfin"])
	}
	if live["seerr"].URL != "" {
		t.Fatalf("a private service is offered at %q", live["seerr"].URL)
	}
}

// seedStackProject is a project with a production environment that serves
// nothing yet, registered as an Essential media stack.
func seedStackProject(t *testing.T, s *Server) (projectID, environmentID int64) {
	t.Helper()
	project, err := s.Store.DB.Exec(`INSERT INTO deploy_projects (name, repo_path, hook_secret, hook_id, created_at) VALUES ('media', '', 'x', 'hook-media', 1)`)
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ = project.LastInsertId()
	environment, err := s.Store.DB.Exec(`INSERT INTO deploy_environments(project_id, name, slug, kind, created_at, updated_at) VALUES(?, 'production', 'production', 'production', 1, 1)`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	environmentID, _ = environment.LastInsertId()
	if err := s.modules.stackStore.Register(context.Background(), projectID, environmentID, media.ID, "essential", "tester"); err != nil {
		t.Fatal(err)
	}
	return projectID, environmentID
}

// Retry on a stack whose deployment now serves something that is not one of
// its presets is refused with the reason, not accepted for a pass that would
// never run.
func TestStackConfigureRefusesALiveReleaseThatIsNotAPreset(t *testing.T) {
	s := testServer(t)
	s.modules.stackStore = stacks.NewStore(s.Store.DB, s.Sealer)
	s.modules.stacks = s.newStackService()
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	projectID, environmentID := seedStackProject(t, s)
	// A live Compose release of another template.
	release, err := s.Store.DB.Exec(`INSERT INTO deploy_releases(project_id, environment_id, release_number, source_identity_json, created_at) VALUES(?, ?, 1, '{"template":"whoami@1.0.0"}', 1)`, projectID, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	releaseID, _ := release.LastInsertId()
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_release_runtimes(release_id, environment_id, kind, runtime_id, created_at, updated_at) VALUES(?, ?, 'compose', 'jd-media', 1, 1)`, releaseID, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`UPDATE deploy_environments SET live_release_id = ? WHERE id = ?`, releaseID, environmentID); err != nil {
		t.Fatal(err)
	}
	refused := c.do(http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/stack/configure", projectID), "{}", nil)
	if refused.Code != http.StatusConflict || !strings.Contains(refused.Body.String(), "stack_not_preset") {
		t.Fatalf("configure on a release that is not a preset = %d %s", refused.Code, refused.Body.String())
	}
	var requested int64
	if err := s.Store.DB.QueryRow(`SELECT requested_at FROM deploy_stacks WHERE project_id = ?`, projectID).Scan(&requested); err != nil || requested != 0 {
		t.Fatalf("requested_at = %d %v", requested, err)
	}
}
