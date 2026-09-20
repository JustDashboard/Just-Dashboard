package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// planningServer wires the planning modules against a real Git checkout, the
// way the signed-in journey test does, so a test can drive the whole
// create → source → detect → configure → preflight → commit sequence the
// new-project page drives.
func planningServer(t *testing.T) (*Server, string) {
	t.Helper()
	s := testServer(t)
	checkout := planningGitCheckout(t)
	s.modules.deployPlanning = deploy.NewPlanningStore(s.Store, s.Sealer, []string{checkout})
	s.modules.deploySources = deploy.NewHostSourceAnalyzer(
		[]string{checkout}, []string{checkout}, filepath.Join(s.Cfg.DataDir, "planning-test-cache"),
		nil, s.modules.deployPlanning,
	)
	s.modules.deployPreflight = deploy.NewHostPreflightObserver([]string{checkout}, s.Cfg.DataDir, nil)
	return s, checkout
}

// commitWorkerDraft runs one complete setup for a worker, which is the profile
// that reaches commit with no blocking finding, and answers the created
// project and environment.
func commitWorkerDraft(
	t *testing.T,
	c *client,
	checkout, name string,
	request func(draft deploy.Draft) deploy.DraftCommitRequest,
) (deploy.DraftCommitResult, string) {
	t.Helper()
	created := c.do(http.MethodPost, "/api/v1/deploy/drafts", `{}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create draft = %d %s", created.Code, created.Body.String())
	}
	var draft deploy.Draft
	decodePlanningResponse(t, created.Body.Bytes(), &draft)

	draft = saveDraftThroughAPI(t, c, draft, deploy.DraftSaveRequest{
		Revision: draft.Revision, Step: deploy.DraftIntent,
		Intent: &deploy.DraftIntentConfig{Name: name, Profile: deploy.ProfileWorker},
	})
	draft = saveDraftThroughAPI(t, c, draft, deploy.DraftSaveRequest{
		Revision: draft.Revision, Step: deploy.DraftSource,
		Source: &deploy.DraftSourceConfig{
			Kind: deploy.SourceGit, Mode: deploy.SourceModeLocalCheckout,
			LocalPath: checkout, Subdirectory: "worker",
		},
	})
	detected := doPlanningJSON(t, c, http.MethodPost,
		"/api/v1/deploy/drafts/"+draft.ID+"/detect", map[string]int{"revision": draft.Revision})
	if detected.Code != http.StatusOK {
		t.Fatalf("detect draft = %d %s", detected.Code, detected.Body.String())
	}
	decodePlanningResponse(t, detected.Body.Bytes(), &draft)

	draft = saveDraftThroughAPI(t, c, draft, deploy.DraftSaveRequest{
		Revision: draft.Revision, Step: deploy.DraftConfiguration,
		Configuration: &deploy.PlanConfiguration{
			Build:   deploy.BuildPlanConfig{Method: deploy.BuildNone},
			Runtime: deploy.RuntimePlanConfig{Strategy: deploy.StrategyStopFirst},
		},
	})
	preflighted := doPlanningJSON(t, c, http.MethodPost,
		"/api/v1/deploy/drafts/"+draft.ID+"/preflight", map[string]int{"revision": draft.Revision})
	if preflighted.Code != http.StatusOK {
		t.Fatalf("preflight draft = %d %s", preflighted.Code, preflighted.Body.String())
	}
	var body struct {
		Draft deploy.Draft `json:"draft"`
	}
	decodePlanningResponse(t, preflighted.Body.Bytes(), &body)
	draft = body.Draft

	committed := doPlanningJSON(t, c, http.MethodPost,
		"/api/v1/deploy/drafts/"+draft.ID+"/commit", request(draft))
	if committed.Code != http.StatusCreated {
		t.Fatalf("commit draft = %d %s", committed.Code, committed.Body.String())
	}
	var result deploy.DraftCommitResult
	decodePlanningResponse(t, committed.Body.Bytes(), &result)
	return result, draft.ID
}

// A draft is created by every press of Import, so abandoning one has to be
// something the operator can undo. Only the owner's own uncommitted setup can
// go: a committed draft is the record a project was planned from.
func TestDeploymentDraftDiscardRemovesOnlyTheCallersUnfinishedSetup(t *testing.T) {
	s, checkout := planningServer(t)
	routes := s.Routes()
	admin := &client{t: t, h: routes, cookie: signInAs(t, s, "discard-admin", auth.RoleAdmin)}
	other := &client{t: t, h: routes, cookie: signInAs(t, s, "discard-other", auth.RoleReadOnly)}

	created := admin.do(http.MethodPost, "/api/v1/deploy/drafts", `{}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create draft = %d %s", created.Code, created.Body.String())
	}
	var draft deploy.Draft
	decodePlanningResponse(t, created.Body.Bytes(), &draft)

	refused := other.do(http.MethodDelete, "/api/v1/deploy/drafts/"+draft.ID, "", nil)
	if refused.Code != http.StatusForbidden ||
		!strings.Contains(refused.Body.String(), `"code":"draft_forbidden"`) {
		t.Fatalf("another owner's discard = %d %s, want 403 draft_forbidden",
			refused.Code, refused.Body.String())
	}

	discarded := admin.do(http.MethodDelete, "/api/v1/deploy/drafts/"+draft.ID, "", nil)
	if discarded.Code != http.StatusNoContent {
		t.Fatalf("discard draft = %d %s", discarded.Code, discarded.Body.String())
	}
	gone := admin.do(http.MethodGet, "/api/v1/deploy/drafts/"+draft.ID, "", nil)
	if gone.Code != http.StatusNotFound {
		t.Fatalf("discarded draft read = %d %s, want 404", gone.Code, gone.Body.String())
	}
	listed := admin.do(http.MethodGet, "/api/v1/deploy/drafts", "", nil)
	var summaries []deploy.DraftSummary
	if err := json.Unmarshal(listed.Body.Bytes(), &summaries); err != nil {
		t.Fatal(err)
	}
	for _, summary := range summaries {
		if summary.ID == draft.ID {
			t.Fatalf("discarded draft is still listed: %#v", summary)
		}
	}

	result, committedID := commitWorkerDraft(t, admin, checkout, "discard-committed",
		func(draft deploy.Draft) deploy.DraftCommitRequest {
			return deploy.DraftCommitRequest{Revision: draft.Revision}
		})
	if result.ProjectID == 0 {
		t.Fatalf("commit result = %#v", result)
	}
	keeping := admin.do(http.MethodDelete, "/api/v1/deploy/drafts/"+committedID, "", nil)
	if keeping.Code != http.StatusConflict ||
		!strings.Contains(keeping.Body.String(), `"code":"draft_committed"`) {
		t.Fatalf("committed draft discard = %d %s, want 409 draft_committed",
			keeping.Code, keeping.Body.String())
	}

	var audited int
	if err := s.Store.DB.QueryRow(
		`SELECT COUNT(*) FROM audit_log WHERE action = 'deploy.draft.discard'`).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != 1 {
		t.Fatalf("deploy.draft.discard audits = %d, want 1", audited)
	}
}

// The name is unique in the schema, so a collision used to be a refusal at
// commit — after the source, the detection, the configuration and the
// preflight. The hostname suggestion the page already asks for answers it
// while the name is still being typed.
func TestDeploymentHostnameReportsWhenTheProjectNameIsAlreadyTaken(t *testing.T) {
	s, checkout := planningServer(t)
	admin := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}

	// Read fresh each time: unmarshalling into a reused struct leaves an
	// absent field holding the previous response's answer.
	nameTaken := func(query string) *bool {
		t.Helper()
		response := admin.do(http.MethodGet, "/api/v1/deploy/hostname?"+query, "", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("hostname %s = %d %s", query, response.Code, response.Body.String())
		}
		var suggestion struct {
			NameTaken *bool `json:"nameTaken"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &suggestion); err != nil {
			t.Fatal(err)
		}
		return suggestion.NameTaken
	}

	if free := nameTaken("name=not-created-yet"); free == nil || *free {
		t.Fatalf("a name no project holds = %v, want an explicit no", free)
	}

	commitWorkerDraft(t, admin, checkout, "already-running",
		func(draft deploy.Draft) deploy.DraftCommitRequest {
			return deploy.DraftCommitRequest{Revision: draft.Revision}
		})

	if taken := nameTaken("name=already-running"); taken == nil || !*taken {
		t.Fatalf("a live project's name = %v, want taken", taken)
	}

	// The comparison matches the UNIQUE constraint exactly, so the answer here
	// and the refusal at commit can never disagree about case.
	if different := nameTaken("name=Already-Running"); different == nil || *different {
		t.Fatalf("a differently-cased name = %v, want free", different)
	}

	// Asking about a hostname rather than a name is a certificate question and
	// carries no claim about project names.
	if asked := nameTaken("hostname=already-running.example.com"); asked != nil {
		t.Fatalf("a hostname question answered about names: %v", *asked)
	}
}

// A Git deployment polls its branch from the moment it exists, and the default
// is to deploy every push. The decision now travels with the commit, so
// "manual only" is available before the first unintended release rather than
// after it.
func TestDeploymentCommitRecordsTheGitPolicyChosenAtCreation(t *testing.T) {
	s, checkout := planningServer(t)
	admin := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}

	manual, _ := commitWorkerDraft(t, admin, checkout, "manual-only-app",
		func(draft deploy.Draft) deploy.DraftCommitRequest {
			return deploy.DraftCommitRequest{
				Revision: draft.Revision,
				GitPolicy: &deploy.GitDeploymentPolicy{
					Automatic: false, CommitStatuses: true,
					WatchInclude: []string{"apps/web/**"},
				},
			}
		})
	policy := gitPolicyOf(t, admin, manual)
	if policy.Automatic {
		t.Fatalf("a manual-only choice at creation did not stick: %#v", policy)
	}
	if len(policy.WatchInclude) != 1 || policy.WatchInclude[0] != "apps/web/**" {
		t.Fatalf("watch paths chosen at creation = %#v", policy.WatchInclude)
	}
	if !policy.CommitStatuses || policy.Revision != 1 {
		t.Fatalf("stored policy = %#v, want commit statuses on at revision 1", policy)
	}

	// No decision writes no row, so every caller that does not ask keeps the
	// defaults exactly as they were.
	silent, _ := commitWorkerDraft(t, admin, checkout, "default-policy-app",
		func(draft deploy.Draft) deploy.DraftCommitRequest {
			return deploy.DraftCommitRequest{Revision: draft.Revision}
		})
	defaults := gitPolicyOf(t, admin, silent)
	if !defaults.Automatic || !defaults.CommitStatuses || defaults.Revision != 0 {
		t.Fatalf("unasked policy = %#v, want the automatic defaults at revision 0", defaults)
	}
	var rows int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_git_policies`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("deploy_git_policies rows = %d, want only the one decision that was taken", rows)
	}
}

func gitPolicyOf(t *testing.T, c *client, result deploy.DraftCommitResult) deploy.GitDeploymentPolicy {
	t.Helper()
	path := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/git-watch",
		result.ProjectID, result.EnvironmentID)
	response := c.do(http.MethodGet, path, "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("git watch = %d %s", response.Code, response.Body.String())
	}
	var status deploy.GitWatchStatus
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	return status.Policy
}
