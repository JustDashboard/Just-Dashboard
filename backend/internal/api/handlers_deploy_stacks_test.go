package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/blueprint"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks/media"
)

// A stack is an ordinary deployment of its preset's template: committing a
// draft made from one registers the stack, its page waits for a live release,
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
	for _, service := range catalogue[0].Services {
		if service.Image == "" {
			t.Fatalf("service %s has no image", service.Name)
		}
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
		`{"revision":%d,"step":"source","source":{"kind":"compose","mode":"blueprint","blueprintId":%q,"blueprintVersion":"1.0.0","blueprintInputs":{"request-approval":"admin"}}}`,
		draft.Revision, media.TemplateEssential), http.StatusOK), &draft)
	_ = json.Unmarshal(call(http.MethodPost, "/deploy/drafts/"+draft.ID+"/detect", fmt.Sprintf(`{"revision":%d}`, draft.Revision), http.StatusOK), &draft)
	if draft.Data.Configuration == nil {
		t.Fatalf("detection left no configuration: %+v", draft.Data)
	}
	configuration, _ := json.Marshal(draft.Data.Configuration)
	_ = json.Unmarshal(call(http.MethodPut, "/deploy/drafts/"+draft.ID, fmt.Sprintf(
		`{"revision":%d,"step":"configuration","configuration":%s}`, draft.Revision, configuration), http.StatusOK), &draft)
	var preflighted struct {
		Draft deploy.Draft `json:"draft"`
	}
	_ = json.Unmarshal(call(http.MethodPost, "/deploy/drafts/"+draft.ID+"/preflight", fmt.Sprintf(`{"revision":%d}`, draft.Revision), http.StatusOK), &preflighted)
	draft = preflighted.Draft
	var result deploy.DraftCommitResult
	committed := c.do(http.MethodPost, "/api/v1/deploy/drafts/"+draft.ID+"/commit",
		fmt.Sprintf(`{"revision":%d,"acknowledgedWarnings":[]}`, draft.Revision), nil)
	if committed.Code != http.StatusCreated {
		// Preflight may ask for warnings to be acknowledged; acknowledge
		// whatever it named, as Review does.
		var refusal struct {
			Error struct {
				Details struct {
					Warnings []string `json:"warnings"`
				} `json:"details"`
			} `json:"error"`
		}
		_ = json.Unmarshal(committed.Body.Bytes(), &refusal)
		acknowledged, _ := json.Marshal(refusal.Error.Details.Warnings)
		committed = c.do(http.MethodPost, "/api/v1/deploy/drafts/"+draft.ID+"/commit",
			fmt.Sprintf(`{"revision":%d,"acknowledgedWarnings":%s}`, draft.Revision, acknowledged), nil)
	}
	if committed.Code != http.StatusCreated {
		t.Fatalf("commit = %d %s", committed.Code, committed.Body.String())
	}
	_ = json.Unmarshal(committed.Body.Bytes(), &result)
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
	for _, credential := range state.Credentials {
		if credential.Name == media.CredentialJellyfinAPIKey || credential.Stored {
			t.Fatalf("credential listing = %+v", state.Credentials)
		}
	}

	// Retry before there is anything to configure says why.
	refused := c.do(http.MethodPost, "/api/v1"+path+"/configure", "{}", nil)
	if refused.Code != http.StatusConflict || !strings.Contains(refused.Body.String(), "stack_waiting") {
		t.Fatalf("configure before live = %d %s", refused.Code, refused.Body.String())
	}

	// Reveal: an account once its step stored it; never an internal token.
	store := s.modules.stackStore.Credentials(result.ProjectID)
	if err := store.Put(context.Background(), media.CredentialJellyfinAdmin, "admin-password-value"); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), media.CredentialJellyfinAPIKey, "internal-token-value"); err != nil {
		t.Fatal(err)
	}
	revealed := call(http.MethodGet, path+"/credentials/"+media.CredentialJellyfinAdmin+"/reveal", "", http.StatusOK)
	if !strings.Contains(string(revealed), "admin-password-value") || !strings.Contains(string(revealed), `"username":"admin"`) {
		t.Fatalf("reveal = %s", revealed)
	}
	call(http.MethodGet, path+"/credentials/"+media.CredentialJellyfinAPIKey+"/reveal", "", http.StatusNotFound)
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
	if err := s.Store.DB.QueryRow(`SELECT sealed FROM deploy_stack_credentials WHERE name = ?`, media.CredentialJellyfinAdmin).Scan(&sealed); err != nil ||
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
