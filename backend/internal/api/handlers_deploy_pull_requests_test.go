package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/ghx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/githubapp"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfcfg"
)

// These drive the pull request routes end to end against a faked gh binary:
// the fake answers every argv the routes build, records what it was asked and
// what arrived on its stdin, and holds the pull requests' state so a merge
// through it is what the trusted read sees afterwards.

type fakePull struct {
	Number  int
	Title   string
	State   string // OPEN, CLOSED or MERGED, as gh spells them
	Head    string
	HeadSHA string
	Owner   string
	Fork    bool
	Author  string
}

func (p *fakePull) body() map[string]any {
	var mergedAt any
	if p.State == "MERGED" {
		mergedAt = "2026-01-03T00:00:00Z"
	}
	return map[string]any{
		"number": p.Number, "title": p.Title, "url": fmt.Sprintf("https://github.com/acme/app/pull/%d", p.Number),
		"state": p.State, "isDraft": false, "headRefName": p.Head, "baseRefName": "main",
		"author": map[string]any{"login": p.Author}, "createdAt": "2026-01-01T00:00:00Z", "comments": []any{},
		"reviewDecision": "", "statusCheckRollup": []any{}, "headRefOid": p.HeadSHA,
		"headRepository": map[string]any{"name": "app"}, "headRepositoryOwner": map[string]any{"login": p.Owner},
		"isCrossRepository": p.Fork, "updatedAt": "2026-01-02T00:00:00Z", "labels": []any{}, "mergedAt": mergedAt,
		"mergeable": "MERGEABLE", "additions": 1, "deletions": 0, "changedFiles": 1, "body": "", "baseRefOid": strings.Repeat("b", 40),
	}
}

type fakeGitHub struct {
	mu       sync.Mutex
	loggedIn bool
	pulls    map[int]*fakePull
	calls    [][]string
	dirs     []string
	stdin    []string
}

func newFakeGitHub(pulls ...*fakePull) *fakeGitHub {
	f := &fakeGitHub{loggedIn: true, pulls: map[int]*fakePull{}}
	for _, pull := range pulls {
		f.pulls[pull.Number] = pull
	}
	return f
}

func (f *fakeGitHub) run(_ context.Context, dir, stdin string, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string(nil), args...))
	f.dirs = append(f.dirs, dir)
	if stdin != "" {
		f.stdin = append(f.stdin, stdin)
	}
	joined := strings.Join(args, " ")
	encode := func(v any) (string, error) {
		out, err := json.Marshal(v)
		return string(out), err
	}
	switch {
	case joined == "auth status":
		if !f.loggedIn {
			return "You are not logged into any GitHub hosts. To log in, run: gh auth login", fmt.Errorf("exit status 1")
		}
		return "github.com\n  ✓ Logged in to github.com account octo (keyring)\n  - Token scopes: 'repo'\n", nil
	case joined == "api user":
		return `{"login":"octo","name":"Octo","avatar_url":"","html_url":"https://github.com/octo"}`, nil
	case strings.HasPrefix(joined, "repo view"):
		return `{"nameWithOwner":"acme/app","defaultBranchRef":{"name":"main"},"url":"https://github.com/acme/app","isPrivate":false,"viewerPermission":"WRITE"}`, nil
	case strings.HasPrefix(joined, "pr list"):
		state := "open"
		for index, arg := range args {
			if arg == "--state" && index+1 < len(args) {
				state = args[index+1]
			}
		}
		listed := []map[string]any{}
		for number := 1; number < 100; number++ {
			pull, ok := f.pulls[number]
			if !ok || (state != "all" && !strings.EqualFold(pull.State, state)) {
				continue
			}
			listed = append(listed, pull.body())
		}
		return encode(listed)
	case strings.HasPrefix(joined, "pr view"):
		var number int
		fmt.Sscanf(args[2], "%d", &number)
		pull, ok := f.pulls[number]
		if !ok {
			return "GraphQL: Could not resolve to a PullRequest with the number of " + args[2] + ".", fmt.Errorf("exit status 1")
		}
		return encode(pull.body())
	case strings.HasPrefix(joined, "pr merge"):
		var number int
		fmt.Sscanf(args[2], "%d", &number)
		pull, ok := f.pulls[number]
		if !ok || pull.State != "OPEN" {
			return "Pull request is not mergeable", fmt.Errorf("exit status 1")
		}
		pull.State = "MERGED"
		return "", nil
	case strings.HasPrefix(joined, "issue list"):
		return `[{"number":3,"title":"Crash on start","url":"https://github.com/acme/app/issues/3","state":"OPEN","author":{"login":"octo"},"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z","comments":[],"labels":[{"name":"bug"}],"assignees":[]}]`, nil
	case len(args) > 3 && args[0] == "api" && args[1] == "--hostname":
		endpoint := args[3]
		switch {
		case strings.Contains(endpoint, "/check-runs"):
			return `{"check_runs":[{"name":"build","status":"completed","conclusion":"success","html_url":"https://github.com/acme/app/runs/1","app":{"name":"GitHub Actions","slug":"github-actions"}}]}`, nil
		case strings.Contains(endpoint, "/status"):
			return `{"statuses":[]}`, nil
		case strings.Contains(endpoint, "/comments"):
			return `{"id":1}`, nil
		}
	}
	return "", fmt.Errorf("unexpected gh call: %s", joined)
}

func (f *fakeGitHub) called(prefix string) [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]string
	for _, call := range f.calls {
		if strings.HasPrefix(strings.Join(call, " "), prefix) {
			out = append(out, call)
		}
	}
	return out
}

// fakeTailnetPublisher is tailscale's serve config as the routes and the
// sweep read it: the loopback upstream behind each served port, and 0 for a
// mapping that is somebody else's.
type fakeTailnetPublisher struct {
	mu     sync.Mutex
	served map[int]int
	// observe runs inside every read of the config, so a test can record
	// what else had already happened by the time the sweep looked.
	observe func()
}

func (p *fakeTailnetPublisher) PublishTailnet(_ context.Context, port, upstreamPort, _ int) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.served[port] = upstreamPort
	return fmt.Sprintf("https://srv.tail.ts.net:%d", port), nil
}

func (p *fakeTailnetPublisher) WithdrawTailnet(_ context.Context, port int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.served, port)
	return nil
}

func (p *fakeTailnetPublisher) ServedTailnetPorts(context.Context) (map[int]int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.observe != nil {
		p.observe()
	}
	out := map[int]int{}
	for port, upstream := range p.served {
		out[port] = upstream
	}
	return out, nil
}

func installFakeGitHub(s *Server, fake *fakeGitHub) {
	service := ghx.NewWithRunner(fake.run)
	s.modules.github = service
	s.modules.pullRequests.github = service
	s.modules.pullRequests.git = s.modules.git
}

func installFakeTailnet(s *Server, running bool) *fakeTailnetPublisher {
	tailnet := &fakeTailnetPublisher{served: map[int]int{}}
	s.modules.tailnet = tailnet
	s.modules.deployExecutor.WithTailnetPublisher(tailnet)
	s.modules.pullRequests.tailnetAt = time.Time{}
	s.modules.pullRequests.detectTailnet = func(context.Context) selfcfg.Identity {
		if !running {
			return selfcfg.Identity{Available: true, Detail: "Tailscale is installed but stopped."}
		}
		return selfcfg.Identity{Available: true, Running: true, State: "Running", Hostname: "srv.tail.ts.net", IP4: "100.64.0.1", HTTPSEnabled: true}
	}
	return tailnet
}

type gitHubDeploymentOptions struct {
	buildMethod   string
	localCheckout bool
	internalPort  int
}

// insertGitHubDeploymentAPI is a normalized project deploying a github.com
// repository: the shape "Test this pull request" needs, with the knobs the
// refusals are about.
func insertGitHubDeploymentAPI(t *testing.T, s *Server, name string, options gitHubDeploymentOptions) (int64, int64) {
	t.Helper()
	now := time.Now().UTC().Unix()
	project, err := s.Store.DB.Exec(`
		INSERT INTO deploy_projects(name, profile, repo_path, branch, compose_file, hook_secret, hook_id, enabled, created_at, updated_at)
		VALUES(?, 'worker', '', 'main', '', '', ?, 1, ?, ?)`, name, name+"-hook", now, now)
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := project.LastInsertId()
	environment, err := s.Store.DB.Exec(`
		INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, strategy, expected_downtime, protected, created_at, updated_at)
		VALUES(?, 'production', 'production', 'production', 1, 'blue_green', 0, 1, ?, ?)`, projectID, now, now)
	if err != nil {
		t.Fatal(err)
	}
	environmentID, _ := environment.LastInsertId()
	source := `{"kind":"git","mode":"git_url","url":"https://github.com/acme/app.git","ref":"main"}`
	if options.localCheckout {
		source = `{"kind":"git","mode":"local_checkout","localPath":"/srv/app","ref":"main"}`
	}
	identity := `{"kind":"git","remote":"https://github.com/acme/app.git","repository":"acme/app","ref":"main","revision":"` + strings.Repeat("c", 40) + `"}`
	method := options.buildMethod
	if method == "" {
		method = "none"
	}
	build := fmt.Sprintf(`{"method":%q,"releaseTasks":[{"name":"migrate","command":"npm run migrate","timeoutSeconds":60,"env":[]}]}`, method)
	runtime := `{"strategy":"blue_green"}`
	if options.internalPort > 0 {
		runtime = fmt.Sprintf(`{"strategy":"blue_green","internalPort":%d}`, options.internalPort)
	}
	digest := "sha256:" + strings.Repeat("d", 64)
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO deploy_sources(environment_id, revision, kind, config_json, identity_json, digest, created_at) VALUES(?, 1, 'git', ?, ?, ?, ?)`, []any{environmentID, source, identity, digest, now}},
		{`INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at) VALUES(?, 1, ?, ?, '{}', '{}', ?, ?)`, []any{environmentID, method, build, digest, now}},
		{`INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at) VALUES(?, 1, ?, '{}', ?, ?)`, []any{environmentID, runtime, digest, now}},
	} {
		if _, err := s.Store.DB.Exec(statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	return projectID, environmentID
}

func seedVariables(t *testing.T, s *Server, environmentID int64, values map[string]string) {
	t.Helper()
	for key, value := range values {
		sealed, err := s.Sealer.Seal(value)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Store.DB.Exec(`INSERT INTO deploy_variable_revisions(environment_id,key,revision,sensitivity,scopes,value_enc,value_digest,active,created_by,created_at) VALUES(?,?,1,'secret','runtime',?,'digest',1,'operator',1)`, environmentID, key, sealed); err != nil {
			t.Fatal(err)
		}
	}
}

func apiTokenFor(t *testing.T, s *Server, username string) string {
	t.Helper()
	var id int64
	if err := s.Store.DB.QueryRow(`SELECT id FROM users WHERE username = ?`, username).Scan(&id); err != nil {
		t.Fatal(err)
	}
	user, err := s.Auth.UserByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.Auth.CreateAPIToken(t.Context(), user, "pull-token", auth.RoleAdmin, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

type pullRequestFixture struct {
	s                        *Server
	routes                   http.Handler
	admin, limited, reader   *client
	token                    string
	fake                     *fakeGitHub
	tailnet                  *fakeTailnetPublisher
	projectID, environmentID int64
	head                     string
}

func newPullRequestFixture(t *testing.T, name string) *pullRequestFixture {
	t.Helper()
	s := testServer(t)
	head := strings.Repeat("a", 40)
	fake := newFakeGitHub(
		&fakePull{Number: 9, Title: "Add the thing", State: "OPEN", Head: "feature", HeadSHA: head, Owner: "acme", Author: "dev"},
		&fakePull{Number: 10, Title: "From a fork", State: "OPEN", Head: "fix", HeadSHA: strings.Repeat("f", 40), Owner: "someone", Fork: true, Author: "someone"},
	)
	installFakeGitHub(s, fake)
	tailnet := installFakeTailnet(s, true)
	projectID, environmentID := insertGitHubDeploymentAPI(t, s, name, gitHubDeploymentOptions{internalPort: 3000})
	routes := s.Routes()
	f := &pullRequestFixture{
		s: s, routes: routes, fake: fake, tailnet: tailnet, projectID: projectID, environmentID: environmentID, head: head,
		admin:   &client{t: t, h: routes, cookie: signInAs(t, s, name+"-admin", auth.RoleAdmin)},
		limited: &client{t: t, h: routes, cookie: signInAs(t, s, name+"-limited", auth.RoleLimited)},
		reader:  &client{t: t, h: routes, cookie: signInAs(t, s, name+"-reader", auth.RoleReadOnly)},
	}
	f.token = apiTokenFor(t, s, name+"-admin")
	return f
}

func (f *pullRequestFixture) path(number int, suffix string) string {
	return fmt.Sprintf("/api/v1/deploy/%d/pull-requests/%d%s", f.projectID, number, suffix)
}

func (f *pullRequestFixture) testPreview(t *testing.T, number int, body string) map[string]any {
	t.Helper()
	response := f.admin.do(http.MethodPost, f.path(number, "/preview"), body, nil)
	if response.Code != http.StatusAccepted {
		t.Fatalf("test preview #%d = %d %s", number, response.Code, response.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func decodeJSON(t *testing.T, body []byte, into any) {
	t.Helper()
	if err := json.Unmarshal(body, into); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
}

func auditCount(t *testing.T, s *Server, where string, args ...any) int {
	t.Helper()
	var count int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE `+where, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// GET /deploy/{id}/pull-requests reads through the dashboard's own login and
// joins each pull request to the preview built from it, with the facts the
// dialog needs beside them.
func TestDeploymentPullRequestsListJoinsPreviews(t *testing.T) {
	f := newPullRequestFixture(t, "pr-list")
	var listed projectPullRequests
	response := f.reader.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/pull-requests", f.projectID), "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("list = %d %s", response.Code, response.Body.String())
	}
	decodeJSON(t, response.Body.Bytes(), &listed)
	if !listed.Available || listed.Identity != "cli" || listed.Repository != "acme/app" || listed.Host != "github.com" {
		t.Fatalf("list = %+v", listed)
	}
	if len(listed.Pulls) != 2 || listed.Pulls[0].Number != 9 || listed.Pulls[0].Preview != nil || !listed.Pulls[1].Fork {
		t.Fatalf("pulls = %+v", listed.Pulls)
	}
	if listed.Compose || listed.LocalCheckout || listed.InternalPort != 3000 || listed.ReleaseTasks != 1 || !listed.Tailnet.Running {
		t.Fatalf("facts = %+v", listed)
	}
	if listed.Production.EnvironmentID != f.environmentID || !listed.Production.Automatic || !listed.Production.AwaitingFirstDeployment || listed.Production.IntervalSeconds == 0 {
		t.Fatalf("production = %+v", listed.Production)
	}
	if len(f.fake.called("pr list --repo acme/app --state open")) != 1 {
		t.Fatalf("list read through the wrong identity: %v", f.fake.calls)
	}
	for _, dir := range f.fake.dirs {
		if dir != "" {
			t.Fatalf("a trusted read ran in a checkout: %q", dir)
		}
	}
	// The list is cached, so a second page read costs no second gh call.
	f.reader.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/pull-requests", f.projectID), "", nil)
	if len(f.fake.called("pr list --repo acme/app --state open")) != 1 {
		t.Fatalf("cached list re-read gh: %v", f.fake.calls)
	}

	f.testPreview(t, 9, fmt.Sprintf(`{"revision":%q,"copyVariables":false,"acceptFork":false}`, f.head))
	response = f.reader.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/pull-requests", f.projectID), "", nil)
	decodeJSON(t, response.Body.Bytes(), &listed)
	preview := listed.Pulls[0].Preview
	if preview == nil || preview.Number != 9 || preview.Origin != deploy.PreviewOriginDashboard || preview.Title != "Add the thing" ||
		preview.Revision != f.head || preview.HeadRevision != f.head || preview.ApprovalState != "approved" || preview.State != "open" {
		t.Fatalf("joined preview = %+v", preview)
	}
	if preview.Address == nil || preview.Address.Kind != "tailnet" || preview.Address.Port != 21000 || preview.Address.Published {
		t.Fatalf("address = %+v", preview.Address)
	}
	if preview.LastRun == nil || preview.LastRun.Operation != deploy.OperationPreviewCreate {
		t.Fatalf("last run = %+v", preview.LastRun)
	}
	if len(listed.Previews) != 1 || listed.Previews[0].ID != preview.ID {
		t.Fatalf("previews = %+v", listed.Previews)
	}
	// Testing the pull request invalidated the cache, so this read asked again.
	if len(f.fake.called("pr list --repo acme/app --state open")) != 2 {
		t.Fatalf("list after a mutation did not re-read gh: %v", f.fake.calls)
	}

	plainID, _, _ := insertDeploymentConfigurationAPI(t, f.s)
	response = f.reader.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/pull-requests", plainID), "", nil)
	decodeJSON(t, response.Body.Bytes(), &listed)
	if listed.Available || listed.Reason != "not_github" || len(listed.Pulls) != 0 || listed.Pulls == nil || listed.Previews == nil {
		t.Fatalf("non-github project = %s", response.Body.String())
	}

	if response := f.reader.do(http.MethodGet, f.path(9, "/checks"), "", nil); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"build"`) {
		t.Fatalf("checks = %d %s", response.Code, response.Body.String())
	}
}

// POST .../preview approves the head the administrator looked at, builds
// the preview environment with production's variables copied in, gives it a
// tailnet port nothing else serves, and enqueues the run — once per head
// until that run fails, and again with a run of its own after it did.
func TestTestPullRequestPreviewApprovesBuildsAndCopiesVariables(t *testing.T) {
	f := newPullRequestFixture(t, "pr-test")
	seedVariables(t, f.s, f.environmentID, map[string]string{
		"PLAIN": "plain-value", "SECRET": "secret-value", "ALIAS": "${{variable.PLAIN}}",
	})
	f.tailnet.served[21000] = 0
	body := fmt.Sprintf(`{"revision":%q,"copyVariables":true,"acceptFork":false}`, f.head)

	if response := f.reader.do(http.MethodPost, f.path(9, "/preview"), body, nil); response.Code != http.StatusForbidden {
		t.Fatalf("reader test = %d", response.Code)
	}
	if response := f.limited.do(http.MethodPost, f.path(9, "/preview"), body, nil); response.Code != http.StatusForbidden {
		t.Fatalf("limited test = %d", response.Code)
	}
	if response := (&client{t: t, h: f.routes}).do(http.MethodPost, f.path(9, "/preview"), body, map[string]string{"Authorization": "Bearer " + f.token}); response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "session_required") {
		t.Fatalf("token test = %d %s", response.Code, response.Body.String())
	}
	if response := f.admin.do(http.MethodPost, f.path(9, "/preview"), `{"revision":"main"}`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("branch name as revision = %d %s", response.Code, response.Body.String())
	}

	result := f.testPreview(t, 9, body)
	runID := int64(result["runId"].(float64))
	if runID == 0 || result["variablesFromProduction"] != true {
		t.Fatalf("result = %v", result)
	}
	if copied := fmt.Sprint(result["copied"]); copied != "[PLAIN SECRET]" {
		t.Fatalf("copied = %s", copied)
	}
	if skipped := fmt.Sprint(result["skipped"]); skipped != "[ALIAS]" {
		t.Fatalf("skipped = %s", skipped)
	}
	address := result["address"].(map[string]any)
	if address["kind"] != "tailnet" || address["port"] != float64(21001) || address["published"] != false {
		t.Fatalf("address = %v (port 21000 is served by somebody else)", address)
	}
	preview := result["preview"].(map[string]any)
	if preview["origin"] != "dashboard" || preview["variablesCopiedRevision"] != f.head || preview["headRevision"] != f.head || preview["revision"] != f.head {
		t.Fatalf("preview = %v", preview)
	}
	environmentID := int64(preview["environmentId"].(float64))

	var state, approvedBy string
	if err := f.s.Store.DB.QueryRow(`SELECT state, approved_by FROM deploy_preview_approvals WHERE provider_ref='9' AND revision=?`, f.head).Scan(&state, &approvedBy); err != nil || state != "approved" || approvedBy != "pr-test-admin" {
		t.Fatalf("approval = %s by %s, %v", state, approvedBy, err)
	}
	var operation, trigger, key string
	if err := f.s.Store.DB.QueryRow(`SELECT operation, trigger, idempotency_key FROM deploy_runs WHERE id=?`, runID).Scan(&operation, &trigger, &key); err != nil || operation != "preview_create" || trigger != "preview" || !strings.HasPrefix(key, "preview-approval:") {
		t.Fatalf("run = %s %s %s, %v", operation, trigger, key, err)
	}
	var copies int
	if err := f.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_variable_revisions WHERE environment_id=? AND copied_from_environment=? AND active=1`, environmentID, f.environmentID).Scan(&copies); err != nil || copies != 2 {
		t.Fatalf("copied variables = %d, %v", copies, err)
	}
	var kind string
	if err := f.s.Store.DB.QueryRow(`SELECT kind FROM deploy_environments WHERE id=?`, environmentID).Scan(&kind); err != nil || kind != "preview" {
		t.Fatalf("environment kind = %s, %v", kind, err)
	}
	if got := auditCount(t, f.s, `action='deploy.preview.test' AND success=1`); got != 1 {
		t.Fatalf("test audit count = %d", got)
	}

	// The same head again answers with the same run.
	if again := f.testPreview(t, 9, body); int64(again["runId"].(float64)) != runID {
		t.Fatalf("retry created another run: %v", again)
	}
	var runs int
	_ = f.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_runs WHERE environment_id=?`, environmentID).Scan(&runs)
	if runs != 1 {
		t.Fatalf("retry left %d runs", runs)
	}

	// After the build failed, a retest gets a run of its own under a key
	// nobody used, so the environment is built again.
	if _, err := f.s.Store.DB.Exec(`UPDATE deploy_runs SET state='failed', status='failed', ended_at=? WHERE id=?`, time.Now().Unix(), runID); err != nil {
		t.Fatal(err)
	}
	retest := f.testPreview(t, 9, body)
	retestID := int64(retest["runId"].(float64))
	if retestID == runID || retestID == 0 {
		t.Fatalf("retest = %v", retest)
	}
	if err := f.s.Store.DB.QueryRow(`SELECT operation, idempotency_key FROM deploy_runs WHERE id=?`, retestID).Scan(&operation, &key); err != nil || operation != "preview_update" || !strings.HasSuffix(key, ":a1") {
		t.Fatalf("retest run = %s %s, %v", operation, key, err)
	}

	// Unchecking the box drops the copies and says so on the preview.
	unchecked := f.testPreview(t, 9, fmt.Sprintf(`{"revision":%q,"copyVariables":false,"acceptFork":false}`, f.head))
	if err := f.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_variable_revisions WHERE environment_id=? AND copied_from_environment<>0 AND active=1`, environmentID).Scan(&copies); err != nil || copies != 0 {
		t.Fatalf("copies after unchecking = %d, %v", copies, err)
	}
	if revision, _ := unchecked["preview"].(map[string]any)["variablesCopiedRevision"]; revision != nil {
		t.Fatalf("variablesCopiedRevision after unchecking = %v", revision)
	}
	if unchecked["variablesFromProduction"] != false {
		t.Fatalf("unchecked result = %v", unchecked)
	}
}

// Every refusal of "Test this pull request" comes before anything is
// written, in the order the dialog explains them.
func TestTestPullRequestPreviewRefusals(t *testing.T) {
	f := newPullRequestFixture(t, "pr-refuse")
	refuse := func(name string, projectID int64, number int, body string, status int, code string) {
		t.Helper()
		response := f.admin.do(http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/pull-requests/%d/preview", projectID, number), body, nil)
		if response.Code != status || !strings.Contains(response.Body.String(), `"code":"`+code+`"`) {
			t.Fatalf("%s = %d %s, want %d %s", name, response.Code, response.Body.String(), status, code)
		}
	}
	same := fmt.Sprintf(`{"revision":%q,"copyVariables":true,"acceptFork":false}`, f.head)
	stale := fmt.Sprintf(`{"revision":%q,"copyVariables":true,"acceptFork":false}`, strings.Repeat("e", 40))
	fork := fmt.Sprintf(`{"revision":%q,"copyVariables":false,"acceptFork":false}`, strings.Repeat("f", 40))
	forkCopy := fmt.Sprintf(`{"revision":%q,"copyVariables":true,"acceptFork":true}`, strings.Repeat("f", 40))

	plainID, _, _ := insertDeploymentConfigurationAPI(t, f.s)
	refuse("not github", plainID, 9, same, http.StatusNotFound, "not_github")
	localID, _ := insertGitHubDeploymentAPI(t, f.s, "pr-local", gitHubDeploymentOptions{localCheckout: true, internalPort: 3000})
	refuse("local checkout", localID, 9, same, http.StatusConflict, "not_remote_git")
	composeID, _ := insertGitHubDeploymentAPI(t, f.s, "pr-compose", gitHubDeploymentOptions{buildMethod: "compose", internalPort: 3000})
	refuse("compose", composeID, 9, same, http.StatusConflict, "preview_compose")
	portlessID, _ := insertGitHubDeploymentAPI(t, f.s, "pr-portless", gitHubDeploymentOptions{})
	refuse("no port", portlessID, 9, same, http.StatusConflict, "preview_no_port")

	f.fake.pulls[11] = &fakePull{Number: 11, Title: "Old", State: "CLOSED", Head: "old", HeadSHA: strings.Repeat("1", 40), Owner: "acme"}
	refuse("closed", f.projectID, 11, fmt.Sprintf(`{"revision":%q}`, strings.Repeat("1", 40)), http.StatusConflict, "pull_request_closed")
	response := f.admin.do(http.MethodPost, f.path(9, "/preview"), stale, nil)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "pull_request_head_changed") || !strings.Contains(response.Body.String(), f.head) {
		t.Fatalf("stale head = %d %s (the message names the current head)", response.Code, response.Body.String())
	}
	refuse("fork unconfirmed", f.projectID, 10, fork, http.StatusConflict, "pull_request_fork")
	refuse("fork with variables", f.projectID, 10, forkCopy, http.StatusBadRequest, "preview_fork_variables")
	refuse("unknown pull request", f.projectID, 42, fmt.Sprintf(`{"revision":%q}`, f.head), http.StatusBadRequest, "bad_request")
	// gh's answer names the number; a number is not a quota refusal.
	refuse("unknown pull request numbered like a status", f.projectID, 429, fmt.Sprintf(`{"revision":%q}`, f.head), http.StatusBadRequest, "bad_request")

	var written int
	_ = f.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_preview_refs`).Scan(&written)
	var approvals int
	_ = f.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_preview_approvals`).Scan(&approvals)
	if written != 0 || approvals != 0 {
		t.Fatalf("a refusal wrote %d previews and %d approvals", written, approvals)
	}

	installFakeTailnet(f.s, false)
	refuse("tailnet stopped", f.projectID, 9, same, http.StatusConflict, "tailnet_unavailable")
	installFakeTailnet(f.s, true)

	// A confirmed fork builds, without production's variables.
	result := f.testPreview(t, 10, fmt.Sprintf(`{"revision":%q,"copyVariables":false,"acceptFork":true}`, strings.Repeat("f", 40)))
	if preview := result["preview"].(map[string]any); preview["headRepository"] != "someone/app" || preview["variablesCopiedRevision"] != nil {
		t.Fatalf("fork preview = %v", preview)
	}
	if got := auditCount(t, f.s, `action='deploy.preview.test' AND success=1 AND detail LIKE '%"fork":true%'`); got != 1 {
		t.Fatalf("fork audit = %d", got)
	}
}

// POST .../preview/close is destructive-tier and session-only, closes once,
// answers with the removal run while it is in flight and with no run once
// it ended, and is audited every time.
func TestClosePullRequestPreviewIsDestructiveIdempotentAndAudited(t *testing.T) {
	f := newPullRequestFixture(t, "pr-close")
	created := f.testPreview(t, 9, fmt.Sprintf(`{"revision":%q,"copyVariables":false,"acceptFork":false}`, f.head))
	createRunID := int64(created["runId"].(float64))
	preview := created["preview"].(map[string]any)
	previewID, environmentID := int64(preview["id"].(float64)), int64(preview["environmentId"].(float64))

	if response := f.reader.do(http.MethodPost, f.path(9, "/preview/close"), "", nil); response.Code != http.StatusForbidden {
		t.Fatalf("reader close = %d", response.Code)
	}
	if response := f.limited.do(http.MethodPost, f.path(9, "/preview/close"), "", nil); response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "destructive") {
		t.Fatalf("limited close = %d %s", response.Code, response.Body.String())
	}
	if response := (&client{t: t, h: f.routes}).do(http.MethodPost, f.path(9, "/preview/close"), "", map[string]string{"Authorization": "Bearer " + f.token}); response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "session_required") {
		t.Fatalf("token close = %d %s", response.Code, response.Body.String())
	}
	if response := f.admin.do(http.MethodPost, f.path(12, "/preview/close"), "", nil); response.Code != http.StatusNotFound {
		t.Fatalf("close without a preview = %d %s", response.Code, response.Body.String())
	}

	closed := f.admin.do(http.MethodPost, f.path(9, "/preview/close"), "", nil)
	if closed.Code != http.StatusAccepted {
		t.Fatalf("close = %d %s", closed.Code, closed.Body.String())
	}
	var result struct {
		RunID int64 `json:"runId"`
	}
	decodeJSON(t, closed.Body.Bytes(), &result)
	if result.RunID == 0 {
		t.Fatalf("close = %s", closed.Body.String())
	}
	var operation, key, state string
	if err := f.s.Store.DB.QueryRow(`SELECT operation, idempotency_key, state FROM deploy_runs WHERE id=?`, result.RunID).Scan(&operation, &key, &state); err != nil || operation != "preview_remove" || key != fmt.Sprintf("pull-request-close:%d", previewID) {
		t.Fatalf("removal run = %s %s %s, %v", operation, key, state, err)
	}
	if err := f.s.Store.DB.QueryRow(`SELECT state FROM deploy_preview_refs WHERE id=?`, previewID).Scan(&state); err != nil || state != "closed" {
		t.Fatalf("ref state = %s, %v", state, err)
	}
	if err := f.s.Store.DB.QueryRow(`SELECT state FROM deploy_runs WHERE id=?`, createRunID).Scan(&state); err != nil || state != "cancelled" {
		t.Fatalf("queued build after close = %s, %v", state, err)
	}

	again := f.admin.do(http.MethodPost, f.path(9, "/preview/close"), "", nil)
	var repeated struct {
		RunID int64 `json:"runId"`
	}
	decodeJSON(t, again.Body.Bytes(), &repeated)
	if again.Code != http.StatusAccepted || repeated.RunID != result.RunID {
		t.Fatalf("second close = %d %s, want the same run", again.Code, again.Body.String())
	}
	var removals int
	_ = f.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_runs WHERE environment_id=? AND operation='preview_remove'`, environmentID).Scan(&removals)
	if removals != 1 {
		t.Fatalf("closing twice enqueued %d removals", removals)
	}

	if _, err := f.s.Store.DB.Exec(`UPDATE deploy_runs SET state='failed', status='failed', ended_at=? WHERE id=?`, time.Now().Unix(), result.RunID); err != nil {
		t.Fatal(err)
	}
	third := f.admin.do(http.MethodPost, f.path(9, "/preview/close"), "", nil)
	decodeJSON(t, third.Body.Bytes(), &repeated)
	if third.Code != http.StatusAccepted || repeated.RunID != 0 {
		t.Fatalf("close after a failed removal = %d %s, want runId 0 (the retry route owns it)", third.Code, third.Body.String())
	}
	if got := auditCount(t, f.s, `action='deploy.preview.close'`); got != 3 {
		t.Fatalf("close audit count = %d", got)
	}
	if got := auditCount(t, f.s, `action='deploy.preview.close' AND success=1 AND detail LIKE '%"reason":"closed"%'`); got != 3 {
		t.Fatalf("close audit detail count = %d", got)
	}
}

// POST .../merge merges as the dashboard's own account, pinned to the head
// the operator looked at, and then reads the repository back through the
// trusted identity: the merged pull request's preview closes right away.
func TestMergePullRequestFromDeployPageReconcilesAndRequiresSession(t *testing.T) {
	f := newPullRequestFixture(t, "pr-merge")
	f.testPreview(t, 9, fmt.Sprintf(`{"revision":%q,"copyVariables":false,"acceptFork":false}`, f.head))
	body := fmt.Sprintf(`{"method":"squash","deleteBranch":true,"headSha":%q}`, f.head)

	if response := f.reader.do(http.MethodPost, f.path(9, "/merge"), body, nil); response.Code != http.StatusForbidden {
		t.Fatalf("reader merge = %d", response.Code)
	}
	if response := (&client{t: t, h: f.routes}).do(http.MethodPost, f.path(9, "/merge"), body, map[string]string{"Authorization": "Bearer " + f.token}); response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "session_required") {
		t.Fatalf("token merge = %d %s", response.Code, response.Body.String())
	}
	f.fake.loggedIn = false
	f.s.modules.pullRequests.login = cachedLogin{}
	if response := f.limited.do(http.MethodPost, f.path(9, "/merge"), body, nil); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "github_login_required") {
		t.Fatalf("signed-out merge = %d %s", response.Code, response.Body.String())
	}
	f.fake.loggedIn = true
	f.s.modules.pullRequests.login = cachedLogin{}

	merged := f.limited.do(http.MethodPost, f.path(9, "/merge"), body, nil)
	if merged.Code != http.StatusOK {
		t.Fatalf("merge = %d %s", merged.Code, merged.Body.String())
	}
	var result struct {
		Merged       bool  `json:"merged"`
		PreviewRunID int64 `json:"previewRunId"`
		Production   struct {
			Automatic               bool `json:"automatic"`
			AwaitingFirstDeployment bool `json:"awaitingFirstDeployment"`
		} `json:"production"`
	}
	decodeJSON(t, merged.Body.Bytes(), &result)
	if !result.Merged || result.PreviewRunID == 0 || !result.Production.Automatic || !result.Production.AwaitingFirstDeployment {
		t.Fatalf("merge result = %s", merged.Body.String())
	}
	merges := f.fake.called("pr merge 9 --repo acme/app --squash --delete-branch --match-head-commit " + f.head)
	if len(merges) != 1 {
		t.Fatalf("merge argv: %v", f.fake.calls)
	}
	var operation, state string
	if err := f.s.Store.DB.QueryRow(`SELECT operation FROM deploy_runs WHERE id=?`, result.PreviewRunID).Scan(&operation); err != nil || operation != "preview_remove" {
		t.Fatalf("preview run = %s, %v", operation, err)
	}
	if err := f.s.Store.DB.QueryRow(`SELECT state FROM deploy_preview_refs WHERE provider_ref='9'`).Scan(&state); err != nil || state != "closed" {
		t.Fatalf("preview after merge = %s, %v", state, err)
	}
	if got := auditCount(t, f.s, `action='github.pull.merge' AND success=1`); got != 1 {
		t.Fatalf("merge audit = %d", got)
	}
	if got := auditCount(t, f.s, `action='deploy.preview.close' AND actor='reconciler' AND success=1 AND detail LIKE '%"reason":"merged"%'`); got != 1 {
		t.Fatalf("reconciler close audit = %d", got)
	}
	// The merged pull request is gone from the (invalidated) open list.
	listed := f.reader.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/pull-requests", f.projectID), "", nil)
	if strings.Contains(listed.Body.String(), `"number":9,"title"`) {
		t.Fatalf("merged pull request still listed: %s", listed.Body.String())
	}
}

// POST .../comment speaks as the dashboard's own account with the body on
// gh's stdin, never in its argv.
func TestCommentPullRequestFromDeployPagePostsBodyOnStdin(t *testing.T) {
	f := newPullRequestFixture(t, "pr-comment")
	if response := f.reader.do(http.MethodPost, f.path(9, "/comment"), `{"body":"Looks good"}`, nil); response.Code != http.StatusForbidden {
		t.Fatalf("reader comment = %d", response.Code)
	}
	if response := f.limited.do(http.MethodPost, f.path(9, "/comment"), `{"body":"  "}`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("empty comment = %d %s", response.Code, response.Body.String())
	}
	response := f.limited.do(http.MethodPost, f.path(9, "/comment"), `{"body":"Looks good --help"}`, nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ok":true`) {
		t.Fatalf("comment = %d %s", response.Code, response.Body.String())
	}
	calls := f.fake.called("api --hostname github.com repos/acme/app/issues/9/comments --method POST --input -")
	if len(calls) != 1 {
		t.Fatalf("comment argv: %v", f.fake.calls)
	}
	for _, arg := range calls[0] {
		if strings.Contains(arg, "Looks good") {
			t.Fatalf("the body reached argv: %v", calls[0])
		}
	}
	if len(f.fake.stdin) != 1 || f.fake.stdin[0] != `{"body":"Looks good --help"}` {
		t.Fatalf("stdin = %q", f.fake.stdin)
	}
	if got := auditCount(t, f.s, `action='github.pull.comment' AND success=1`); got != 1 {
		t.Fatalf("comment audit = %d", got)
	}
}

// GET /deploy/pull-requests counts open pull requests and previews per
// github project and leaves the others out.
func TestDeploymentPullRequestSummaryCountsPerProject(t *testing.T) {
	f := newPullRequestFixture(t, "pr-fleet")
	f.testPreview(t, 9, fmt.Sprintf(`{"revision":%q,"copyVariables":false,"acceptFork":false}`, f.head))
	plainID, _, _ := insertDeploymentConfigurationAPI(t, f.s)
	response := f.reader.do(http.MethodGet, "/api/v1/deploy/pull-requests", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("summary = %d %s", response.Code, response.Body.String())
	}
	var summary struct {
		Projects map[string]fleetPullRequestCounts `json:"projects"`
	}
	decodeJSON(t, response.Body.Bytes(), &summary)
	counts, ok := summary.Projects[fmt.Sprint(f.projectID)]
	if !ok || counts.Open != 2 || counts.Previews != 1 {
		t.Fatalf("counts = %+v", summary.Projects)
	}
	if _, listed := summary.Projects[fmt.Sprint(plainID)]; listed {
		t.Fatalf("a project without a GitHub repository was counted: %+v", summary.Projects)
	}
}

// GET /git/pull-requests reads each checkout as its owner, names the deploy
// projects of the same repository and joins their previews; the Git page's
// checks, issues and comment routes go through the checkout as well, and a
// merge there closes the preview through the trusted read.
func TestGitPullRequestRoutesReadEachCheckoutAsItsOwner(t *testing.T) {
	c, s, _, repo := gitFixture(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("remote", "add", "origin", "https://github.com/acme/app.git")
	head := strings.Repeat("a", 40)
	fake := newFakeGitHub(&fakePull{Number: 9, Title: "Add the thing", State: "OPEN", Head: "feature", HeadSHA: head, Owner: "acme", Author: "dev"})
	installFakeGitHub(s, fake)
	installFakeTailnet(s, true)
	projectID, environmentID := insertGitHubDeploymentAPI(t, s, "git-pr-app", gitHubDeploymentOptions{internalPort: 3000})
	created := c.do(http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/pull-requests/9/preview", projectID), fmt.Sprintf(`{"revision":%q}`, head), nil)
	if created.Code != http.StatusAccepted {
		t.Fatalf("test preview = %d %s", created.Code, created.Body.String())
	}

	response := c.do(http.MethodGet, "/api/v1/git/pull-requests", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("summary = %d %s", response.Code, response.Body.String())
	}
	var summary struct {
		Available bool                 `json:"available"`
		Repos     []gitPullRequestRepo `json:"repos"`
	}
	decodeJSON(t, response.Body.Bytes(), &summary)
	if !summary.Available || len(summary.Repos) != 1 || summary.Repos[0].Path != repo || summary.Repos[0].Repository != "acme/app" || summary.Repos[0].Error != "" {
		t.Fatalf("summary = %s", response.Body.String())
	}
	entry := summary.Repos[0]
	if len(entry.Pulls) != 1 || entry.Pulls[0].Number != 9 || entry.Pulls[0].Preview == nil || entry.Pulls[0].Preview.ProjectID != projectID {
		t.Fatalf("pulls = %+v", entry.Pulls)
	}
	if len(entry.Deployments) != 1 || entry.Deployments[0].ProjectID != projectID || entry.Deployments[0].EnvironmentID != environmentID || entry.Deployments[0].Name != "git-pr-app" {
		t.Fatalf("deployments = %+v", entry.Deployments)
	}
	lists := fake.called("pr list --state open")
	if len(lists) != 1 || fake.dirs[len(fake.dirs)-1] == "" {
		t.Fatalf("checkout list argv/dir: %v %v", fake.calls, fake.dirs)
	}
	for index, call := range fake.calls {
		if strings.HasPrefix(strings.Join(call, " "), "pr list --state open") && fake.dirs[index] != repo {
			t.Fatalf("the checkout's list ran in %q, want %q", fake.dirs[index], repo)
		}
	}
	if response := c.do(http.MethodGet, gitPath("/api/v1/git/pull-requests", repo), "", nil); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"repository":"acme/app"`) {
		t.Fatalf("summary for one path = %d %s", response.Code, response.Body.String())
	}

	if response := c.do(http.MethodGet, gitPath("/api/v1/git/github/pulls/9/checks", repo, "head", head), "", nil); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"build"`) {
		t.Fatalf("checks = %d %s", response.Code, response.Body.String())
	}
	if response := c.do(http.MethodGet, gitPath("/api/v1/git/github/pulls/9/checks", repo), "", nil); response.Code != http.StatusBadRequest {
		t.Fatalf("checks without a head = %d", response.Code)
	}
	if response := c.do(http.MethodGet, gitPath("/api/v1/git/github/issues", repo), "", nil); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"title":"Crash on start"`) {
		t.Fatalf("issues = %d %s", response.Code, response.Body.String())
	}
	if response := c.do(http.MethodPost, gitPath("/api/v1/git/github/pulls/9/comment", repo), `{"body":"From the Git page"}`, nil); response.Code != http.StatusOK {
		t.Fatalf("comment = %d %s", response.Code, response.Body.String())
	}
	if len(fake.stdin) != 1 || fake.stdin[0] != `{"body":"From the Git page"}` {
		t.Fatalf("comment stdin = %q", fake.stdin)
	}
	reader := &client{t: t, h: c.h, cookie: signInAs(t, s, "git-pr-reader", auth.RoleReadOnly)}
	if response := reader.do(http.MethodPost, gitPath("/api/v1/git/github/pulls/9/comment", repo), `{"body":"nope"}`, nil); response.Code != http.StatusForbidden {
		t.Fatalf("reader comment = %d", response.Code)
	}

	merged := c.do(http.MethodPost, gitPath("/api/v1/git/github/pulls/9/merge", repo), fmt.Sprintf(`{"method":"merge","headSha":%q}`, head), nil)
	if merged.Code != http.StatusOK {
		t.Fatalf("merge = %d %s", merged.Code, merged.Body.String())
	}
	if len(fake.called("pr merge 9 --merge --match-head-commit "+head)) != 1 {
		t.Fatalf("merge argv: %v", fake.calls)
	}
	var state string
	if err := s.Store.DB.QueryRow(`SELECT state FROM deploy_preview_refs WHERE provider_ref='9'`).Scan(&state); err != nil || state != "closed" {
		t.Fatalf("preview after a Git page merge = %s, %v", state, err)
	}
	if got := auditCount(t, s, `action='deploy.preview.close' AND actor='reconciler'`); got != 1 {
		t.Fatalf("reconciler close audit = %d", got)
	}
}

// A quota refusal is GitHub's words or its status, never a number that
// happens to appear in an error: pull request #429 is not a rate limit, and
// treating it as one would answer 429 here and back the reconciler off.
func TestPullRequestRateLimitIsAQuotaRefusalNotANumber(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{errors.New("GraphQL: Could not resolve to a PullRequest with the number of 429."), false},
		{errors.New("HTTP 404: Not Found (https://api.github.com/repos/acme/app/pulls/429)"), false},
		{errors.New("commit 4290abc not found"), false},
		{errors.New("HTTP 403: API rate limit exceeded for 203.0.113.9."), true},
		{errors.New("HTTP 429: Too Many Requests"), true},
		{errors.New("You have exceeded a secondary rate limit. Please wait a few minutes before you try again."), true},
		{&githubapp.APIError{Status: http.StatusTooManyRequests, Path: "/repos/acme/app/pulls/9"}, true},
		{&githubapp.APIError{Status: http.StatusForbidden, Path: "/repos/acme/app/pulls/9", Message: "API rate limit exceeded for installation ID 1."}, true},
		{&githubapp.APIError{Status: http.StatusForbidden, Path: "/repos/acme/app/pulls/9", Message: "Resource not accessible by integration"}, false},
		{&githubapp.APIError{Status: http.StatusNotFound, Path: "/repos/acme/app/pulls/429"}, false},
		{fmt.Errorf("read: %w", &githubapp.APIError{Status: http.StatusTooManyRequests, Path: "/repos/acme/app/pulls/9"}), true},
	} {
		if got := rateLimited(tc.err); got != tc.want {
			t.Errorf("rateLimited(%v) = %t, want %t", tc.err, got, tc.want)
		}
	}
}

// A succeeded run is the answer to a retried approval only while its preview
// still stands. Once the sweep found nothing serving the address, the same
// head gets a run of its own, which is what republishes the mapping.
func TestTestPullRequestPreviewRebuildsAfterItsAddressWentDead(t *testing.T) {
	f := newPullRequestFixture(t, "pr-dead")
	body := fmt.Sprintf(`{"revision":%q,"copyVariables":false,"acceptFork":false}`, f.head)
	created := f.testPreview(t, 9, body)
	runID := int64(created["runId"].(float64))
	environmentID := int64(created["preview"].(map[string]any)["environmentId"].(float64))
	if _, err := f.s.Store.DB.Exec(`UPDATE deploy_runs SET state='succeeded', status='succeeded', ended_at=? WHERE id=?`, time.Now().Unix(), runID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.modules.deployRuns.MarkPreviewAddressPublished(t.Context(), environmentID, "https://srv.tail.ts.net:21000", 3000, true); err != nil {
		t.Fatal(err)
	}
	if again := f.testPreview(t, 9, body); int64(again["runId"].(float64)) != runID {
		t.Fatalf("a standing preview was rebuilt: %v", again)
	}

	// The sweep found nothing serving the port and recorded the address dead.
	if err := f.s.modules.deployRuns.MarkPreviewAddressPublished(t.Context(), environmentID, "https://srv.tail.ts.net:21000", 0, false); err != nil {
		t.Fatal(err)
	}
	retest := f.testPreview(t, 9, body)
	retestID := int64(retest["runId"].(float64))
	if retestID == runID || retestID == 0 {
		t.Fatalf("retest after the address went dead answered with the old run: %v", retest)
	}
	var operation, key, state string
	if err := f.s.Store.DB.QueryRow(`SELECT operation, idempotency_key, state FROM deploy_runs WHERE id=?`, retestID).Scan(&operation, &key, &state); err != nil || operation != "preview_update" || !strings.HasSuffix(key, ":a1") || state != "queued" {
		t.Fatalf("retest run = %s %s %s, %v", operation, key, state, err)
	}
	if got := auditCount(t, f.s, `action='deploy.preview.test' AND success=1 AND detail LIKE '%"runId":`+fmt.Sprint(retestID)+`%'`); got != 1 {
		t.Fatalf("audit rows naming the new run = %d", got)
	}
}

// The quota and the port are checked before the approval is written: a test
// refused for either leaves no approval, no preview and no copied variables
// behind, while the preview that already holds a slot and a port keeps
// retesting.
func TestTestPullRequestPreviewRefusesQuotaAndPortsBeforeWriting(t *testing.T) {
	f := newPullRequestFixture(t, "pr-capacity")
	seedVariables(t, f.s, f.environmentID, map[string]string{"PLAIN": "plain-value"})
	f.fake.pulls[11] = &fakePull{Number: 11, Title: "Second", State: "OPEN", Head: "second", HeadSHA: strings.Repeat("2", 40), Owner: "acme", Author: "dev"}
	same := fmt.Sprintf(`{"revision":%q,"copyVariables":true,"acceptFork":false}`, f.head)
	second := fmt.Sprintf(`{"revision":%q,"copyVariables":true,"acceptFork":false}`, strings.Repeat("2", 40))
	first := f.testPreview(t, 9, same)
	firstRunID := int64(first["runId"].(float64))
	refuse := func(name string, status int, code string) {
		t.Helper()
		response := f.admin.do(http.MethodPost, f.path(11, "/preview"), second, nil)
		if response.Code != status || !strings.Contains(response.Body.String(), `"code":"`+code+`"`) {
			t.Fatalf("%s = %d %s, want %d %s", name, response.Code, response.Body.String(), status, code)
		}
		var approvals, refs, copies int
		_ = f.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_preview_approvals WHERE provider_ref='11'`).Scan(&approvals)
		_ = f.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_preview_refs WHERE provider_ref='11'`).Scan(&refs)
		_ = f.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_variable_revisions WHERE copied_from_environment<>0 AND active=1`).Scan(&copies)
		if approvals != 0 || refs != 0 || copies != 1 {
			t.Fatalf("%s wrote %d approvals, %d previews and left %d copied variables (want 0, 0 and the first preview's 1)", name, approvals, refs, copies)
		}
	}

	// One preview may be open at a time from here on.
	if _, err := f.s.Store.DB.Exec(`UPDATE deploy_triggers SET config_json=json_set(config_json,'$.previewQuota',1)`); err != nil {
		t.Fatal(err)
	}
	refuse("quota", http.StatusConflict, "preview_quota")
	if again := f.testPreview(t, 9, same); int64(again["runId"].(float64)) != firstRunID {
		t.Fatalf("the open preview was refused its own slot: %v", again)
	}
	if _, err := f.s.Store.DB.Exec(`UPDATE deploy_triggers SET config_json=json_set(config_json,'$.previewQuota',5)`); err != nil {
		t.Fatal(err)
	}

	// Every port of the range is served by something else.
	for port := selfcfg.TailnetPortMin; port <= selfcfg.TailnetPortMax; port++ {
		f.tailnet.served[port] = 0
	}
	refuse("ports", http.StatusConflict, "preview_address_exhausted")
	if again := f.testPreview(t, 9, same); int64(again["runId"].(float64)) != firstRunID {
		t.Fatalf("the preview holding its port was refused a retest: %v", again)
	}
}

// A refusal only the store can raise, after the approval is written, still
// leaves an audit row naming the pull request and the head it was for.
func TestTestPullRequestPreviewAuditsARefusalAfterTheApproval(t *testing.T) {
	f := newPullRequestFixture(t, "pr-late-refusal")
	body := fmt.Sprintf(`{"revision":%q,"copyVariables":false,"acceptFork":false}`, f.head)
	f.testPreview(t, 9, body)
	if response := f.admin.do(http.MethodPost, f.path(9, "/preview/close"), "", nil); response.Code != http.StatusAccepted {
		t.Fatalf("close = %d %s", response.Code, response.Body.String())
	}
	// The removal has not run, so the environment is not archived yet: only
	// EnsurePreview, after the approval, can say so.
	response := f.admin.do(http.MethodPost, f.path(9, "/preview"), body, nil)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "preview_cleanup_pending") {
		t.Fatalf("retest during cleanup = %d %s", response.Code, response.Body.String())
	}
	if got := auditCount(t, f.s, `action='deploy.preview.test' AND success=0 AND detail LIKE '%"number":9%' AND detail LIKE '%"revision":"`+f.head+`"%'`); got != 1 {
		t.Fatalf("audit rows for the refused retest = %d", got)
	}
}

// A preview whose cleanup finished is gone from the pull request it was
// built from on both lists, not only from the previews beside them: the join
// reads the same filtered list, so an open pull request does not carry a
// "Closed" preview around forever.
func TestPullRequestListsDropAPreviewWhoseCleanupSucceeded(t *testing.T) {
	c, s, _, repo := gitFixture(t)
	remote := exec.Command("git", "remote", "add", "origin", "https://github.com/acme/app.git")
	remote.Dir = repo
	remote.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := remote.CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v: %s", err, out)
	}
	head := strings.Repeat("a", 40)
	installFakeGitHub(s, newFakeGitHub(&fakePull{Number: 9, Title: "Add the thing", State: "OPEN", Head: "feature", HeadSHA: head, Owner: "acme", Author: "dev"}))
	installFakeTailnet(s, true)
	projectID, _ := insertGitHubDeploymentAPI(t, s, "git-pr-cleanup", gitHubDeploymentOptions{internalPort: 3000})
	if created := c.do(http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/pull-requests/9/preview", projectID), fmt.Sprintf(`{"revision":%q}`, head), nil); created.Code != http.StatusAccepted {
		t.Fatalf("test preview = %d %s", created.Code, created.Body.String())
	}
	closed := c.do(http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/pull-requests/9/preview/close", projectID), "", nil)
	if closed.Code != http.StatusAccepted {
		t.Fatalf("close = %d %s", closed.Code, closed.Body.String())
	}
	var removal struct {
		RunID int64 `json:"runId"`
	}
	decodeJSON(t, closed.Body.Bytes(), &removal)

	deployList := func() projectPullRequests {
		t.Helper()
		response := c.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/pull-requests", projectID), "", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("deploy list = %d %s", response.Code, response.Body.String())
		}
		var listed projectPullRequests
		decodeJSON(t, response.Body.Bytes(), &listed)
		if len(listed.Pulls) != 1 || listed.Pulls[0].Number != 9 {
			t.Fatalf("deploy pulls = %+v", listed.Pulls)
		}
		return listed
	}
	gitList := func() gitPullRequestRepo {
		t.Helper()
		response := c.do(http.MethodGet, gitPath("/api/v1/git/pull-requests", repo), "", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("git list = %d %s", response.Code, response.Body.String())
		}
		var summary struct {
			Repos []gitPullRequestRepo `json:"repos"`
		}
		decodeJSON(t, response.Body.Bytes(), &summary)
		if len(summary.Repos) != 1 || summary.Repos[0].Error != "" || len(summary.Repos[0].Pulls) != 1 {
			t.Fatalf("git repos = %s", response.Body.String())
		}
		return summary.Repos[0]
	}

	// While the removal is still queued the preview stays on both lists, closed.
	if listed := deployList(); listed.Pulls[0].Preview == nil || listed.Pulls[0].Preview.State != "closed" || len(listed.Previews) != 1 {
		t.Fatalf("deploy list during cleanup = %+v", listed)
	}
	if entry := gitList(); entry.Pulls[0].Preview == nil || entry.Pulls[0].Preview.State != "closed" {
		t.Fatalf("git list during cleanup = %+v", entry.Pulls)
	}

	if _, err := s.Store.DB.Exec(`UPDATE deploy_runs SET state='succeeded', status='succeeded', ended_at=? WHERE id=?`, time.Now().Unix(), removal.RunID); err != nil {
		t.Fatal(err)
	}
	if listed := deployList(); listed.Pulls[0].Preview != nil || len(listed.Previews) != 0 {
		t.Fatalf("deploy list after cleanup = %+v", listed)
	}
	if entry := gitList(); entry.Pulls[0].Preview != nil {
		t.Fatalf("git list after cleanup = %+v", entry.Pulls)
	}
}

// The tailnet sweep reads the serve config before the engine starts. A run
// the engine resumes at boot may publish a preview the moment it starts, and
// a sweep still reading the config would take that fresh mapping for a dead
// one. The engine reclaims an expired lease synchronously in Start, so
// whether the seeded lease was still expired when the sweep looked is the
// order the two ran in.
func TestStartSweepsPreviewAddressesBeforeTheEngineResumesRuns(t *testing.T) {
	s := testServer(t)
	tailnet := installFakeTailnet(s, true)
	projectID, environmentID := insertGitHubDeploymentAPI(t, s, "boot-order", gitHubDeploymentOptions{internalPort: 3000})
	now := time.Now().Unix()
	run, err := s.Store.DB.Exec(`INSERT INTO deploy_runs(project_id, environment_id, started_at, status, state, lease_token) VALUES(?, ?, ?, 'running', 'running', 'stale-token')`, projectID, environmentID, now-120)
	if err != nil {
		t.Fatal(err)
	}
	runID, _ := run.LastInsertId()
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_queue_leases(run_id, environment_id, claim_token, slot_class, claimed_by, expires_at, heartbeat_at, created_at) VALUES(?, ?, 'stale-token', 'heavy', 'previous-process', ?, ?, ?)`, runID, environmentID, now-60, now-60, now-120); err != nil {
		t.Fatal(err)
	}
	reads, stillExpired := 0, false
	tailnet.observe = func() {
		reads++
		var expires int64
		err := s.Store.DB.QueryRow(`SELECT expires_at FROM deploy_queue_leases WHERE run_id=?`, runID).Scan(&expires)
		stillExpired = err == nil && expires < time.Now().Unix()
	}
	if err := s.startDeployEngine(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reads != 1 {
		t.Fatalf("the sweep read the serve config %d times", reads)
	}
	if !stillExpired {
		t.Fatal("the engine had already resumed the stale run when the sweep read the serve config")
	}
}
