package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/mcp"
	"github.com/Wayy01/Just-Dashboard/backend/internal/openapigen"
	"github.com/go-chi/chi/v5"
)

// The tools generated from the deployment API: which routes they are, read
// off the router and the deploy key table, and a read and a write through
// the protocol handler with an MCP key, as the key's grant and its chosen
// deployments allow.

// handWrittenTools is every hand-written tool, databases' and deployments'.
func handWrittenTools() []mcp.Tool {
	return mcp.Tools(mcp.Offer{Databases: true, Query: true, Write: true, Deployments: true, Deploy: true, Rollback: true})
}

// Every route a deploy key may call is a generated tool unless it is a
// hand-written one or cannot be a call at all, each said why; no route the
// table keeps from a deploy key is one; the names, the grants and the count
// are what the documentation says.
func TestEveryDeployKeyRouteIsAnMCPTool(t *testing.T) {
	s := testServer(t)
	root := s.Routes().(chi.Routes)
	cat := s.mcpCatalogue()
	if cat.err != nil {
		t.Fatalf("the generated tools cannot be built: %v", cat.err)
	}
	doc := s.deployOpenAPI.Load().document()
	var document map[string]any
	if err := json.Unmarshal(doc.JSON, &document); err != nil {
		t.Fatal(err)
	}
	paths := document["paths"].(map[string]any)
	tools := map[string]mcp.Operation{}
	for _, op := range cat.ops {
		tools[op.Method+" "+op.Pattern] = op
	}
	skipped := map[string]string{}
	for _, skip := range cat.skipped {
		skipped[skip.Route] = skip.Reason
	}
	handWritten := map[string]bool{}
	for _, tool := range handWrittenTools() {
		handWritten[tool.Name] = true
	}
	routes := 0
	_ = chi.Walk(root, func(method, pattern string, _ http.Handler, middlewares ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(pattern, "/api/v1/deploy/") {
			return nil
		}
		pattern = strings.TrimPrefix(pattern, "/api/v1")
		key := method + " " + pattern
		rule, listed := ruleForDeployKey(method, pattern)
		op, isTool := tools[key]
		if !listed || rule.session != "" || rule.deny != "" {
			if isTool {
				t.Errorf("%s is a tool, but no deploy key reaches it", key)
			}
			return nil
		}
		routes++
		if !isTool {
			if skipped[key] == "" {
				t.Errorf("%s is a deploy key route and neither a tool nor left out with a reason", key)
			}
			return nil
		}
		if skipped[key] != "" {
			t.Errorf("%s is a tool and also left out", key)
		}
		described := paths[pattern].(map[string]any)[strings.ToLower(method)].(map[string]any)
		if want := openapigen.Snake(described["operationId"].(string)); op.Tool.Name != want || op.ID != described["operationId"] {
			t.Errorf("%s is the tool %s, want %s, its operationId in snake case", key, op.Tool.Name, want)
		}
		if handWritten[op.Tool.Name] || !regexp.MustCompile(`^[a-z0-9_]{1,64}$`).MatchString(op.Tool.Name) {
			t.Errorf("%s: the tool name %q", key, op.Tool.Name)
		}
		want := mcp.GrantRead
		switch {
		case described["x-destructive"] == true:
			want = mcp.GrantDestructive
		case method != http.MethodGet:
			want = mcp.GrantWrite
		}
		if op.Grant != want || isDestructive(middlewares) != (op.Grant == mcp.GrantDestructive) {
			t.Errorf("%s asks the %s grant, want %s", key, op.Grant, want)
		}
		if op.Tool.Annotations.ReadOnlyHint != (method == http.MethodGet) ||
			(op.Grant == mcp.GrantDestructive || method == http.MethodDelete) && !op.Tool.Annotations.DestructiveHint {
			t.Errorf("%s: annotations %+v", key, op.Tool.Annotations)
		}
		// The input schema stands alone: every reference is to its own $defs.
		raw, _ := json.Marshal(op.Tool.InputSchema)
		defs, _ := op.Tool.InputSchema["$defs"].(map[string]any)
		for _, m := range regexp.MustCompile(`"\$ref":"([^"]+)"`).FindAllStringSubmatch(string(raw), -1) {
			if name := strings.TrimPrefix(m[1], "#/$defs/"); name == m[1] || defs[name] == nil {
				t.Errorf("%s: the input schema refers to %s outside itself", key, m[1])
			}
		}
		required, _ := op.Tool.InputSchema["required"].([]string)
		if len(required) != strings.Count(pattern, "{") {
			t.Errorf("%s requires %v, want its path parameters", key, required)
		}
		return nil
	})
	if len(cat.ops)+len(cat.skipped) != routes {
		t.Errorf("%d tools and %d left out of %d deploy key routes", len(cat.ops), len(cat.skipped), routes)
	}
	for route, tool := range mcpHandWritten {
		if skipped[route] == "" || !handWritten[tool] {
			t.Errorf("%s is the hand-written %s, which is not what the catalogue says", route, tool)
		}
	}
	if len(cat.ops) != MCPGeneratedTools {
		t.Errorf("%d tools are generated from the deployment API and MCPGeneratedTools says %d: a deploy key route was added or removed. "+
			"Set it to %d and say so in docs/internal/backend/databases-proxy-platform.md, docs/features/databases.md and docs/internal/deployments/implementation.md", len(cat.ops), MCPGeneratedTools, len(cat.ops))
	}
	if n := len(handWrittenTools()); n != 13 {
		t.Errorf("%d hand-written tools; the documentation says 13", n)
	}
	total := MCPGeneratedTools + len(handWrittenTools())
	for _, file := range []string{
		filepath.Join("..", "..", "..", "docs", "internal", "backend", "databases-proxy-platform.md"),
		filepath.Join("..", "..", "..", "docs", "features", "databases.md"),
		filepath.Join("..", "..", "..", "docs", "internal", "deployments", "implementation.md"),
	} {
		text, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(text), fmt.Sprintf("%d tools generated", MCPGeneratedTools)) || !strings.Contains(string(text), fmt.Sprintf("%d tools in all", total)) {
			t.Errorf("%s does not say %d tools generated and %d tools in all", file, MCPGeneratedTools, total)
		}
	}
}

type mcpAPIBench struct {
	*mcpBench
	shopID, shopEnv   int64
	otherID, otherEnv int64
}

func newMCPAPIBench(t *testing.T) *mcpAPIBench {
	t.Helper()
	s := testServer(t)
	s.apiLim, s.destrLim = httpx.NewLimiter(1<<20, 1<<20), httpx.NewLimiter(1<<20, 1<<20)
	wireDeployPlanningAndSources(t, s, []string{t.TempDir()})
	b := &mcpAPIBench{mcpBench: &mcpBench{t: t, s: s, h: s.Routes()}}
	var err error
	if b.admin, err = s.Auth.CreateUser(t.Context(), "ion", "Correct-Horse-Battery-9", auth.RoleAdmin, false); err != nil {
		t.Fatal(err)
	}
	if b.viewer, err = s.Auth.CreateUser(t.Context(), "viewer", "Correct-Horse-Battery-9", auth.RoleReadOnly, false); err != nil {
		t.Fatal(err)
	}
	image := deploy.DraftSourceConfig{Kind: deploy.SourceImage, Mode: deploy.SourceModeImageReference, Image: "nginx:1.27-alpine"}
	b.shopID, b.shopEnv = seedCommittedDeployment(t, s, "shop", image, deploy.SourceIdentity{Kind: deploy.SourceImage})
	b.otherID, b.otherEnv = seedCommittedDeployment(t, s, "billing", image, deploy.SourceIdentity{Kind: deploy.SourceImage})
	return b
}

// toolsKey is an administrator's key given the deployments as access says,
// with the generated tools turned on.
func (b *mcpAPIBench) toolsKey(access auth.DeployAccess) string {
	b.t.Helper()
	_, secret, err := b.s.Auth.CreateMCPToken(b.t.Context(), b.admin, "claude-desktop", "", time.Hour, nil, access, nil, true)
	if err != nil {
		b.t.Fatal(err)
	}
	return secret
}

func (b *mcpAPIBench) narrowedKey(access auth.DeployAccess, deployments ...int64) string {
	b.t.Helper()
	_, secret, err := b.s.Auth.CreateMCPToken(b.t.Context(), b.admin, "claude-narrowed", "", time.Hour, nil, access, deployments, true)
	if err != nil {
		b.t.Fatal(err)
	}
	return secret
}

// The generated read and write tools through the protocol handler: offered
// as the grant allows, made through the router as the key — the route's own
// answer and audit entry, a value written and never read back — and refused
// under the wrong grant and past a narrowed key's deployments, as the deploy
// key table and its guards refuse a deploy key.
func TestGeneratedToolsGoThroughTheRouterAsTheKey(t *testing.T) {
	b := newMCPAPIBench(t)
	reader := b.toolsKey(auth.DeployRead)
	deployer := b.toolsKey(auth.DeployRun)
	shop := map[string]any{"id": b.shopID, "env": b.shopEnv}

	// What each key is offered: the hand-written tools first, then the
	// generated ones its grant reaches.
	readerTools, deployerTools := b.listTools(reader), b.listTools(deployer)
	if !strings.HasPrefix(readerTools, "list_deployments list_runs read_run_log list_releases ") ||
		!strings.Contains(readerTools, " deployment_variables ") || strings.Contains(readerTools, "deployment_variable_put") ||
		strings.Contains(readerTools, "deployment_instant_rollback") {
		t.Errorf("a key that reads is offered %s", readerTools)
	}
	if !strings.Contains(deployerTools, " deployment_variable_put ") || !strings.Contains(deployerTools, " deployment_instant_rollback ") {
		t.Errorf("an administrator's key that deploys is offered %s", deployerTools)
	}
	if databases := b.listTools(b.key(b.admin, read(b.sqlite("notes", false)))); strings.Contains(databases, "deployment_") {
		t.Errorf("a key for databases is offered %s", databases)
	}

	// list_deployments gives the ids the generated tools take.
	listed := b.ok(reader, mcp.ToolListDeployments, map[string]any{})
	if got := fmt.Sprint(listed["deployments"]); !strings.Contains(got, fmt.Sprintf("id:%d", b.shopID)) || !strings.Contains(got, fmt.Sprintf("environmentId:%d", b.shopEnv)) {
		t.Errorf("list_deployments gives no ids: %s", got)
	}

	// A read: the route's own answer.
	environments := b.ok(reader, "deployment_environments", map[string]any{"id": b.shopID})
	if got := fmt.Sprint(environments); !strings.Contains(got, "production") {
		t.Errorf("deployment_environments: %s", got)
	}
	if entry := b.lastAudit("deploy.mcp.deployment_environments"); !entry.Success || entry.Actor != "token" ||
		entry.Target != fmt.Sprintf("/api/v1/deploy/%d/environments", b.shopID) || !strings.Contains(entry.Detail, `"status":200`) ||
		!strings.Contains(entry.Detail, `"operation":"deploymentEnvironments"`) {
		t.Errorf("the read was audited as %+v", entry)
	}

	// A write, under the reader's grant: refused before the route is called.
	const value = "s3cr3t-the-container-reads"
	put := map[string]any{"id": b.shopID, "env": b.shopEnv, "name": "STRIPE_KEY",
		"body": map[string]any{"revision": 1, "value": value, "sensitivity": "secret", "scopes": []string{"runtime"}}}
	if text := b.refused(reader, "deployment_variable_put", put); !strings.Contains(text, "not change them") {
		t.Errorf("a key that reads wrote: %s", text)
	}
	if entry := b.lastAudit("deploy.mcp.deployment_variable_put"); entry.Success || !strings.Contains(entry.Detail, `"reason":"not_granted"`) {
		t.Errorf("the refusal was audited as %+v", entry)
	}
	before := b.audits("deploy.variable.")

	// The same write, under the deployer's: made by the route, which audits
	// it as the key and never hands the value back.
	result, text := b.call(deployer, "deployment_variable_put", put)
	if result["isError"] != false || strings.Contains(text, value) || !strings.Contains(text, "STRIPE_KEY") {
		t.Fatalf("deployment_variable_put: %s", text)
	}
	if b.audits("deploy.variable.") != before+1 {
		t.Errorf("the route did not audit the write it made")
	}
	var keyName string
	var success bool
	if err := b.s.Store.DB.QueryRow(`SELECT key_name, success FROM audit_log WHERE action = 'deploy.variable.update' ORDER BY id DESC LIMIT 1`).Scan(&keyName, &success); err != nil || keyName != "claude-desktop" || !success {
		t.Errorf("the route's entry names key %q (%v, %v)", keyName, success, err)
	}
	if entry := b.lastAudit("deploy.mcp.deployment_variable_put"); !entry.Success || strings.Contains(entry.Detail, value) {
		t.Errorf("the write was audited as %+v", entry)
	}
	// A value the route would hand back is withheld from the key, as from a
	// deploy key: a generated secret is never in the result.
	var written struct {
		DesiredRevision int `json:"desiredRevision"`
	}
	if err := json.Unmarshal([]byte(text), &written); err != nil || written.DesiredRevision == 0 {
		t.Fatalf("deployment_variable_put answered %s", text)
	}
	generated, generatedText := b.call(deployer, "deployment_variable_generate", map[string]any{"id": b.shopID, "env": b.shopEnv,
		"name": "SESSION_SECRET", "body": map[string]any{"revision": written.DesiredRevision, "scopes": []string{"runtime"}}})
	if generated["isError"] != false || strings.Contains(generatedText, "generatedValue") || !strings.Contains(generatedText, "SESSION_SECRET") {
		t.Errorf("deployment_variable_generate: %s", generatedText)
	}
	listedVariables := b.ok(reader, "deployment_variables", shop)
	if got := fmt.Sprint(listedVariables); !strings.Contains(got, "STRIPE_KEY") || strings.Contains(got, value) {
		t.Errorf("deployment_variables after the write: %s", got)
	}

	// The handlers' deploy key rules hold: a variable is not pointed at a
	// saved credential, and a reveal is a session's (not even a tool).
	refer := map[string]any{"id": b.shopID, "env": b.shopEnv, "name": "DB_PASSWORD",
		"body": map[string]any{"revision": 2, "reference": "${{credential.registry}}", "scopes": []string{"runtime"}}}
	if text := b.refused(deployer, "deployment_variable_put", refer); !strings.Contains(text, "cannot point a variable") {
		t.Errorf("a reference to a saved credential: %s", text)
	}
	if strings.Contains(deployerTools, "reveal") {
		t.Errorf("a reveal is offered: %s", deployerTools)
	}

	// Arguments are the route's: an unknown one, a missing path parameter
	// and an id that is not one are the model's to fix.
	if text := b.refused(reader, "deployment_variables", map[string]any{"id": b.shopID, "env": b.shopEnv, "deployment": "shop"}); !strings.Contains(text, `no argument "deployment"`) {
		t.Errorf("an unknown argument: %s", text)
	}
	if text := b.refused(reader, "deployment_variables", map[string]any{"id": b.shopID}); !strings.Contains(text, `needs "env"`) {
		t.Errorf("a missing parameter: %s", text)
	}
	if text := b.refused(reader, "deployment_variables", map[string]any{"id": "shop", "env": b.shopEnv}); !strings.Contains(text, "must be an integer") {
		t.Errorf("an id that is not one: %s", text)
	}
	// The route's own refusal is the result: another deployment's
	// environment through this one's id.
	if text := b.refused(deployer, "deployment_variables", map[string]any{"id": b.shopID, "env": b.otherEnv}); text == "" {
		t.Error("another deployment's environment was read through this one's id")
	}
}

// The generated tools are each key's to turn on: a key made before them, or
// without them, is offered exactly the hand-written tools under its grant and
// is refused a generated one by name, so no key gains a tool with an upgrade.
func TestAnMCPKeyIsOfferedTheGeneratedToolsOnlyWhenTurnedOn(t *testing.T) {
	b := newMCPAPIBench(t)
	before := b.deployKey(b.admin, auth.DeployRun) // the flag is off, as on every key made before it
	if names := b.listTools(before); names != "list_deployments list_runs read_run_log list_releases deploy rollback" {
		t.Errorf("a key without the generated tools is offered %s", names)
	}
	init := b.rpc(b.post(before, `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18",
		"capabilities":{},"clientInfo":{"name":"claude-code","version":"2.4.0"}}}`, nil))
	if text, _ := init.Result["instructions"].(string); strings.Contains(text, "deployment_configuration") {
		t.Errorf("a key without the generated tools is told of them: %q", text)
	}
	if text := b.refused(before, "deployment_variables", map[string]any{"id": b.shopID, "env": b.shopEnv}); !strings.Contains(text, "generated tools are off") ||
		!strings.Contains(text, "Databases → MCP server") {
		t.Errorf("a key without the generated tools called one: %s", text)
	}
	if entry := b.lastAudit("deploy.mcp.deployment_variables"); entry.Success || !strings.Contains(entry.Detail, `"reason":"operations_off"`) {
		t.Errorf("the refusal was audited as %+v", entry)
	}
	// The hand-written tools are as they were.
	b.ok(before, mcp.ToolListDeployments, map[string]any{})

	on := b.toolsKey(auth.DeployRun)
	names := b.listTools(on)
	if !strings.HasPrefix(names, "list_deployments list_runs read_run_log list_releases deploy rollback ") ||
		len(strings.Fields(names)) != 6+MCPGeneratedTools {
		t.Errorf("a key with the generated tools is offered %d tools: %s", len(strings.Fields(names)), names)
	}
	b.ok(on, "deployment_variables", map[string]any{"id": b.shopID, "env": b.shopEnv})
}

// A key narrowed to chosen deployments reaches those through every tool and
// nothing else: the hand-written ones find no other deployment, a generated
// one is refused by the deploy key gate as a narrowed deploy key is, and a
// route about every deployment needs a key that reaches every one.
func TestANarrowedMCPKeyReachesOnlyItsDeployments(t *testing.T) {
	b := newMCPAPIBench(t)
	key := b.narrowedKey(auth.DeployRun, b.shopID)

	listed := fmt.Sprint(b.ok(key, mcp.ToolListDeployments, map[string]any{})["deployments"])
	if !strings.Contains(listed, "deployment:shop") || strings.Contains(listed, "billing") {
		t.Errorf("a narrowed key listed %s", listed)
	}
	if text := b.refused(key, mcp.ToolListRuns, map[string]any{"deployment": "billing"}); !strings.Contains(text, "No deployment is named") {
		t.Errorf("a narrowed key found another deployment: %s", text)
	}
	b.ok(key, "deployment_variables", map[string]any{"id": b.shopID, "env": b.shopEnv})
	if text := b.refused(key, "deployment_variables", map[string]any{"id": b.otherID, "env": b.otherEnv}); !strings.Contains(text, "not given this deployment") {
		t.Errorf("a narrowed key read another deployment: %s", text)
	}
	if entry := b.lastAudit("deploy.mcp.deployment_variables"); entry.Success || !strings.Contains(entry.Detail, `"reason":"key_scope"`) {
		t.Errorf("the refusal was audited as %+v", entry)
	}
	if text := b.refused(key, "deployment_variables", map[string]any{"id": b.shopID, "env": b.otherEnv}); !strings.Contains(text, "not given this deployment") {
		t.Errorf("a narrowed key reached another's environment through its own id: %s", text)
	}
	if text := b.refused(key, "shared_variables", map[string]any{}); !strings.Contains(text, "reaches every deployment") {
		t.Errorf("a narrowed key read what every deployment shares: %s", text)
	}
	// The key that reaches every deployment reads both.
	every := b.toolsKey(auth.DeployRead)
	b.ok(every, "deployment_variables", map[string]any{"id": b.otherID, "env": b.otherEnv})
	b.ok(every, "shared_variables", map[string]any{})
}

// A narrowed MCP key is made and re-pointed on the key routes as a deploy key
// is narrowed, and a key given no deployments is not narrowed to some.
func TestMCPKeysAreNarrowedOnTheKeyRoutes(t *testing.T) {
	b := newMCPAPIBench(t)
	cookie := signInAs(t, b.s, "keys-admin", auth.RoleAdmin)
	c := &client{t: t, h: b.h, cookie: cookie}
	made := c.do(http.MethodPost, "/api/v1/tokens/mcp", fmt.Sprintf(`{"name":"claude","ttlDays":30,"deployments":"read","deploymentIds":[%d],"operations":true}`, b.shopID), nil)
	if made.Code != http.StatusCreated || !strings.Contains(made.Body.String(), fmt.Sprintf(`"deploymentIds":[%d]`, b.shopID)) ||
		!strings.Contains(made.Body.String(), `"operations":true`) {
		t.Fatalf("make a narrowed key = %d %s", made.Code, made.Body.String())
	}
	var answer struct {
		Token  auth.APIToken `json:"token"`
		Secret string        `json:"secret"`
	}
	if err := json.Unmarshal(made.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	if entry := b.lastAudit("dashboard.token.create"); !strings.Contains(entry.Detail, `"reaches":["shop"]`) || !strings.Contains(entry.Detail, `"operations":true`) {
		t.Errorf("the key was audited as %+v", entry)
	}
	if w := c.do(http.MethodPost, "/api/v1/tokens/mcp", fmt.Sprintf(`{"name":"x","deploymentIds":[%d]}`, b.shopID), nil); w.Code != http.StatusBadRequest {
		t.Errorf("a key given no deployments narrowed to some = %d %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodPost, "/api/v1/tokens/mcp", `{"name":"x","deployments":"read","deploymentIds":[999]}`, nil); w.Code != http.StatusBadRequest {
		t.Errorf("a key narrowed to a deployment that is not there = %d %s", w.Code, w.Body.String())
	}
	// Re-pointed without deploymentIds, the narrowing is kept; with an empty
	// list, the key reaches every deployment.
	path := fmt.Sprintf("/api/v1/tokens/%d/connections", answer.Token.ID)
	if w := c.do(http.MethodPut, path, `{"connections":[],"deployments":"read"}`, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), fmt.Sprintf(`"deploymentIds":[%d]`, b.shopID)) {
		t.Errorf("re-pointed keeping the narrowing = %d %s", w.Code, w.Body.String())
	}
	if text := b.refused(answer.Secret, "deployment_variables", map[string]any{"id": b.otherID, "env": b.otherEnv}); !strings.Contains(text, "not given this deployment") {
		t.Errorf("a narrowed key read another deployment: %s", text)
	}
	if w := c.do(http.MethodPut, path, `{"connections":[],"deploymentIds":[]}`, nil); w.Code != http.StatusOK || strings.Contains(w.Body.String(), "deploymentIds") {
		t.Errorf("widened to every deployment = %d %s", w.Code, w.Body.String())
	}
	b.ok(answer.Secret, "deployment_variables", map[string]any{"id": b.otherID, "env": b.otherEnv})

	// Turned off, and on again, without a new secret; left out, it is kept.
	if w := c.do(http.MethodPut, path, `{"connections":[],"operations":false}`, nil); w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"operations"`) {
		t.Errorf("turned off = %d %s", w.Code, w.Body.String())
	}
	if entry := b.lastAudit("dashboard.token.connections"); !strings.Contains(entry.Detail, `"operations":false`) {
		t.Errorf("turning the generated tools off was audited as %+v", entry)
	}
	if text := b.refused(answer.Secret, "deployment_variables", map[string]any{"id": b.otherID, "env": b.otherEnv}); !strings.Contains(text, "generated tools are off") {
		t.Errorf("a key with its generated tools off called one: %s", text)
	}
	if w := c.do(http.MethodPut, path, `{"connections":[],"operations":true}`, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"operations":true`) {
		t.Errorf("turned on = %d %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodPut, path, `{"connections":[]}`, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"operations":true`) {
		t.Errorf("left out = %d %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodPost, "/api/v1/tokens/mcp", `{"name":"plain","deployments":"read"}`, nil); w.Code != http.StatusCreated || strings.Contains(w.Body.String(), `"operations"`) {
		t.Errorf("a key made without asking for the generated tools = %d %s", w.Code, w.Body.String())
	}
}
