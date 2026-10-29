package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/mcp"
)

// The deployment tools over the real router, against deployments written the
// way the deployment route tests write them: a run is queued and never built,
// and a release is activated by hand where a rollback needs one.

func (b *mcpBench) listTools(key string) string {
	b.t.Helper()
	return toolNames(b.rpc(b.post(key, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, legacy)).Result)
}

// listHandWritten is the hand-written tools a key is offered, in order: the
// ones generated from the deployment API follow them (handlers_mcp_operations_test.go).
func (b *mcpBench) listHandWritten(key string) string {
	b.t.Helper()
	var names []string
	for _, name := range strings.Fields(b.listTools(key)) {
		if mcp.Known(name) {
			names = append(names, name)
		}
	}
	return strings.Join(names, " ")
}

func (b *mcpBench) runs(projectID int64) int {
	b.t.Helper()
	var n int
	if err := b.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_runs WHERE project_id = ?`, projectID).Scan(&n); err != nil {
		b.t.Fatal(err)
	}
	return n
}

// A key reaches the deployments only when it was given them: a key made for
// databases is offered nothing about them and is refused when it asks, and a
// key that may read them is refused a deploy.
func TestTheDeploymentToolsNeedTheirOwnGrant(t *testing.T) {
	b := newMCPBench(t)
	project := createLegacyDeploymentFixture(t, b.s, "shop-api")
	databases := b.key(b.admin, read(b.shop))
	reader := b.deployKey(b.admin, auth.DeployRead)
	deployer := b.deployKey(b.admin, auth.DeployRun, read(b.shop))

	if names := b.listTools(databases); strings.Contains(names, "deploy") {
		t.Errorf("a key for databases is offered %s", names)
	}
	if text := b.refused(databases, mcp.ToolListDeployments, map[string]any{}); !strings.Contains(text, "not given the deployments") {
		t.Errorf("a key for databases listed the deployments: %s", text)
	}
	if entry := b.lastAudit("deploy.mcp.list_deployments"); entry.Success || !strings.Contains(entry.Detail, `"reason":"not_granted"`) {
		t.Errorf("the refusal was audited as %+v", entry)
	}

	if names := b.listHandWritten(reader); names != "list_deployments list_runs read_run_log list_releases" {
		t.Errorf("a key that reads the deployments alone is offered %s", names)
	}
	init := b.rpc(b.post(reader, `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18",
		"capabilities":{},"clientInfo":{"name":"claude-code","version":"2.4.0"}}}`, nil))
	if text, _ := init.Result["instructions"].(string); !strings.Contains(text, "list_deployments") || strings.Contains(text, "list_connections") {
		t.Errorf("a key for the deployments is told %q", text)
	}
	listed := b.ok(reader, mcp.ToolListDeployments, map[string]any{})
	if got := fmt.Sprint(listed["deployments"]); !strings.Contains(got, "deployment:shop-api") || !strings.Contains(got, "build:legacy_compose") {
		t.Errorf("list_deployments: %s", got)
	}
	if text := b.refused(reader, mcp.ToolDeploy, map[string]any{"deployment": "shop-api"}); !strings.Contains(text, "not deploy them") {
		t.Errorf("a key that reads deployed: %s", text)
	}
	if n := b.runs(project.ID); n != 0 {
		t.Fatalf("a refused deploy queued %d runs", n)
	}

	if names := b.listHandWritten(deployer); !strings.Contains(names, "run_query list_deployments list_runs read_run_log list_releases deploy rollback") {
		t.Errorf("an administrator's key that deploys is offered %s", names)
	}
	if text := b.refused(deployer, mcp.ToolListRuns, map[string]any{"deployment": "billing"}); !strings.Contains(text, "list_deployments names them") {
		t.Errorf("an unknown deployment: %s", text)
	}
	// A database tool still reads only the databases, whatever else the key
	// may do.
	if text := fmt.Sprint(b.ok(deployer, mcp.ToolListConnections, map[string]any{})); !strings.Contains(text, "shop") || strings.Contains(text, "notes") {
		t.Errorf("list_connections: %s", text)
	}
}

// deploy queues the run the deployment page's Deploy would, as the key's
// owner, and is held to the same refusals; list_runs and read_run_log read it
// back as the run page does.
func TestADeployThroughMCPQueuesTheRunTheRouteWould(t *testing.T) {
	b := newMCPBench(t)
	project := createLegacyDeploymentFixture(t, b.s, "shop-api")
	convertToNormalizedDeploymentFixture(t, b.s, project)
	key := b.deployKey(b.admin, auth.DeployRun)

	queued := b.ok(key, mcp.ToolDeploy, map[string]any{"deployment": "Shop-API"})
	run, _ := queued["queued"].(map[string]any)
	if queued["deployment"] != "shop-api" || run["run"] != float64(1) || run["state"] != string(deploy.RunQueued) ||
		run["operation"] != "deploy" || run["actor"] != "ion" || run["trigger"] != "manual" {
		t.Fatalf("deploy: %+v", queued)
	}
	if entry := b.lastAudit("deploy.mcp.deploy"); !entry.Success || entry.Target != "shop-api" || entry.Actor != "token" ||
		!strings.Contains(entry.Detail, `"run":1`) || !strings.Contains(entry.Detail, `"prefix":"vpsd_`) {
		t.Errorf("the deploy was audited as %+v", entry)
	}

	// A local checkout has no branch to name, as the route says.
	if text := b.refused(key, mcp.ToolDeploy, map[string]any{"deployment": "shop-api", "ref": "v2"}); !strings.Contains(text, "ref") {
		t.Errorf("a ref on a local source: %s", text)
	}
	if text := b.refused(key, mcp.ToolDeploy, map[string]any{"deployment": "shop-api", "ref": "main", "revision": strings.Repeat("b", 40)}); !strings.Contains(text, "not both") {
		t.Errorf("a ref and a revision: %s", text)
	}
	if entry := b.lastAudit("deploy.mcp.deploy"); entry.Success {
		t.Errorf("a refused deploy was audited as a success: %+v", entry)
	}

	runs := b.ok(key, mcp.ToolListRuns, map[string]any{"deployment": "shop-api", "limit": 5})
	if listed, _ := runs["runs"].([]any); len(listed) != 1 || listed[0].(map[string]any)["run"] != float64(1) {
		t.Fatalf("list_runs: %+v", runs)
	}

	runID := int64(0)
	if err := b.s.Store.DB.QueryRow(`SELECT id FROM deploy_runs WHERE project_id = ? AND run_number = 1`, project.ID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	snapshot, err := b.s.modules.deployRuns.Snapshot(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	var build int64
	for _, step := range snapshot.Steps {
		if step.Key == deploy.StepBuildArtifact {
			build = step.ID
		}
	}
	for i, text := range []string{"#1 load build definition", "#2 FROM node:22", "#3 RUN npm ci", "#4 RUN npm run build", "error: build failed"} {
		if _, err := b.s.Store.DB.Exec(`INSERT INTO deploy_log_chunks(run_id, step_id, seq, event_type, stream, ts, text, data_json)
			VALUES(?, ?, ?, 'step.log', 'stdout', ?, ?, '{}')`, runID, build, 1000+i, time.Now().Unix(), text); err != nil {
			t.Fatal(err)
		}
	}
	log := b.ok(key, mcp.ToolReadRunLog, map[string]any{"deployment": "shop-api", "lines": 3})
	lines, _ := log["lines"].([]any)
	if len(lines) != 3 || log["earlierLines"] != true || lines[2].(map[string]any)["text"] != "error: build failed" ||
		lines[0].(map[string]any)["step"] != "Build" {
		t.Fatalf("read_run_log: %+v", log)
	}
	if steps, _ := log["steps"].([]any); len(steps) != len(snapshot.Steps) {
		t.Errorf("read_run_log gave %d steps of %d", len(steps), len(snapshot.Steps))
	}
	if text := b.refused(key, mcp.ToolReadRunLog, map[string]any{"deployment": "shop-api", "run": 7}); !strings.Contains(text, "no run #7") {
		t.Errorf("a run that is not there: %s", text)
	}

	// An owner demoted since keeps a key that may deploy, and the key is
	// held to the owner's role now.
	if _, err := b.s.Store.DB.Exec(`UPDATE users SET role = 'readonly' WHERE id = ?`, b.admin.ID); err != nil {
		t.Fatal(err)
	}
	if names := b.listHandWritten(key); strings.Contains(names, "deploy ") || strings.HasSuffix(names, "deploy") {
		t.Errorf("a key whose owner may no longer deploy is offered %s", names)
	}
	if text := b.refused(key, mcp.ToolDeploy, map[string]any{"deployment": "shop-api"}); !strings.Contains(text, "readonly account") {
		t.Errorf("a demoted owner's key deployed: %s", text)
	}
	if n := b.runs(project.ID); n != 1 {
		t.Errorf("%d runs after the refusals, want the one deploy", n)
	}
}

// rollback goes back to the release before the live one unless it is told
// which, and is held to the rollback route's destructive capability.
func TestARollbackThroughMCPGoesToTheReleaseBeforeTheLiveOne(t *testing.T) {
	b := newMCPBench(t)
	project := createLegacyDeploymentFixture(t, b.s, "shop-api")
	convertToNormalizedDeploymentFixture(t, b.s, project)
	key := b.deployKey(b.admin, auth.DeployRun)

	release := func(identity string) *deploy.ReleaseWithArtifacts {
		queued := b.ok(key, mcp.ToolDeploy, map[string]any{"deployment": "shop-api"})
		number := int64(queued["queued"].(map[string]any)["run"].(float64))
		run, err := b.s.modules.deployRuns.ProjectRunByNumber(t.Context(), project.ID, number)
		if err != nil {
			t.Fatal(err)
		}
		return activateSyntheticNormalizedRelease(t, b.s, *run, identity)
	}
	if text := b.refused(key, mcp.ToolRollback, map[string]any{"deployment": "shop-api"}); !strings.Contains(text, "no retained release") {
		t.Errorf("a rollback with nothing to go back to: %s", text)
	}
	first := release("v1")
	second := release("v2")

	releases := b.ok(key, mcp.ToolListReleases, map[string]any{"deployment": "shop-api"})
	if listed, _ := releases["releases"].([]any); len(listed) != 2 ||
		listed[0].(map[string]any)["state"] != "live" || listed[1].(map[string]any)["state"] != "retained" {
		t.Fatalf("list_releases: %+v", releases)
	}
	if text := b.refused(key, mcp.ToolRollback, map[string]any{"deployment": "shop-api", "release": second.Release.ID}); !strings.Contains(text, "non-live release") {
		t.Errorf("a rollback to the live release: %s", text)
	}

	limited, err := b.s.Auth.CreateUser(t.Context(), "lena", "Correct-Horse-Battery-9", auth.RoleLimited, false)
	if err != nil {
		t.Fatal(err)
	}
	operator := b.deployKey(limited, auth.DeployRun)
	if names := b.listHandWritten(operator); !strings.HasSuffix(names, "list_releases deploy") {
		t.Errorf("a limited account's key is offered %s", names)
	}
	if text := b.refused(operator, mcp.ToolRollback, map[string]any{"deployment": "shop-api"}); !strings.Contains(text, "limited account") {
		t.Errorf("a limited account rolled back: %s", text)
	}

	rolled := b.ok(key, mcp.ToolRollback, map[string]any{"deployment": "shop-api"})
	run, _ := rolled["queued"].(map[string]any)
	if rolled["release"] != float64(first.Release.ID) || run["operation"] != "rollback" || run["trigger"] != "rollback" {
		t.Fatalf("rollback: %+v", rolled)
	}
	queued, err := b.s.modules.deployRuns.ProjectRunByNumber(t.Context(), project.ID, int64(run["run"].(float64)))
	if err != nil || queued.Priority != 1000 || !strings.Contains(string(queued.Metadata), fmt.Sprintf(`"targetReleaseId":%d`, first.Release.ID)) {
		t.Fatalf("the queued rollback = %+v %v", queued, err)
	}
	if entry := b.lastAudit("deploy.mcp.rollback"); !entry.Success || entry.Target != "shop-api" ||
		!strings.Contains(entry.Detail, fmt.Sprintf(`"release":%d`, first.Release.ID)) {
		t.Errorf("the rollback was audited as %+v", entry)
	}

	legacy := createLegacyDeploymentFixture(t, b.s, "old-stack")
	if text := b.refused(key, mcp.ToolRollback, map[string]any{"deployment": "old-stack"}); !strings.Contains(text, "from its page") {
		t.Errorf("a legacy rollback: %s", text)
	}
	if queued := b.ok(key, mcp.ToolDeploy, map[string]any{"deployment": "old-stack"}); queued["queued"].(map[string]any)["state"] != string(deploy.RunQueued) {
		t.Errorf("a legacy deploy: %+v", queued)
	}
	if n := b.runs(legacy.ID); n != 1 {
		t.Errorf("the legacy deployment has %d runs", n)
	}
}

// Making a key for the deployments, and changing what it may do with them,
// go through the key routes and their audit like its databases.
func TestMCPKeysAreMadeForTheDeployments(t *testing.T) {
	b := newMCPBench(t)
	c := &client{t: t, h: b.h, cookie: signIn(t, b.s)}
	raw, _ := json.Marshal(map[string]any{"name": "cursor", "ttlDays": 30, "connections": []auth.TokenConnection{}, "deployments": "deploy"})
	w := c.do(http.MethodPost, "/api/v1/tokens/mcp", string(raw), nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	created := decodeJSONBody[struct {
		Token  auth.APIToken `json:"token"`
		Secret string        `json:"secret"`
	}](t, w)
	if created.Token.Deployments != auth.DeployRun || len(created.Token.Connections) != 0 {
		t.Fatalf("created %+v", created.Token)
	}
	if entry := b.lastAudit("dashboard.token.create"); !strings.Contains(entry.Detail, `"deployments":"deploy"`) {
		t.Errorf("creation was audited as %+v", entry)
	}

	path := fmt.Sprintf("/api/v1/tokens/%d/connections", created.Token.ID)
	keep, _ := json.Marshal(map[string]any{"connections": []auth.TokenConnection{read(b.shop)}})
	if w := c.do(http.MethodPut, path, string(keep), nil); w.Code != http.StatusOK ||
		decodeJSONBody[auth.APIToken](t, w).Deployments != auth.DeployRun {
		t.Fatalf("re-pointing the databases: %d %s", w.Code, w.Body.String())
	}
	narrow, _ := json.Marshal(map[string]any{"connections": []auth.TokenConnection{read(b.shop)}, "deployments": "read"})
	if w := c.do(http.MethodPut, path, string(narrow), nil); w.Code != http.StatusOK {
		t.Fatalf("narrowing: %d %s", w.Code, w.Body.String())
	}
	if entry := b.lastAudit("dashboard.token.connections"); !strings.Contains(entry.Detail, `"deployments":"read"`) {
		t.Errorf("narrowing was audited as %+v", entry)
	}
	if text := b.refused(created.Secret, mcp.ToolDeploy, map[string]any{"deployment": "anything"}); !strings.Contains(text, "not deploy") {
		t.Errorf("a narrowed key deployed: %s", text)
	}

	for name, body := range map[string]map[string]any{
		"nothing at all":        {"name": "x", "connections": []auth.TokenConnection{}},
		"a grant nobody spells": {"name": "x", "connections": []auth.TokenConnection{}, "deployments": "admin"},
	} {
		raw, _ := json.Marshal(body)
		if w := c.do(http.MethodPost, "/api/v1/tokens/mcp", string(raw), nil); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	viewer := &client{t: t, h: b.h, cookie: signInAs(t, b.s, "reader", auth.RoleReadOnly)}
	raw, _ = json.Marshal(map[string]any{"name": "x", "connections": []auth.TokenConnection{}, "deployments": "deploy"})
	if w := viewer.do(http.MethodPost, "/api/v1/tokens/mcp", string(raw), nil); w.Code != http.StatusBadRequest {
		t.Errorf("a read-only account made a key that deploys: %d %s", w.Code, w.Body.String())
	}
}
