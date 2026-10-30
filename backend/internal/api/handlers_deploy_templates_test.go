package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/blueprint"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

const templateRouteCompose = `services:
  web:
    image: nginx:1.27-alpine
    ports: ["8080:80"]
    environment:
      CACHE_HOST: cache
      ADMIN_PASSWORD: written-out-admin-password
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://127.0.0.1/"]
    depends_on:
      cache: {condition: service_healthy}
  cache:
    image: redis:7.4-alpine
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
`

func templateAuditCount(t *testing.T, s *Server, action string) int {
	t.Helper()
	var count int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = ? AND success = 1`, action).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func templateBody[T any](t *testing.T, body []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return value
}

// Every template route, through real HTTP as the administrator's session: a
// dry run stores nothing, the catalogue lists built-ins and added templates
// together, each marked, a draft from an added one freezes its definition,
// and every write is audited as deploy.template.*.
func TestTemplateRoutesAddListDeployExportImportUpdateAndDelete(t *testing.T) {
	s := testServer(t)
	wireDeployPlanningAndSources(t, s, []string{t.TempDir()})
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw := ""
		switch typed := body.(type) {
		case nil:
		case string:
			raw = typed
		default:
			encoded, _ := json.Marshal(typed)
			raw = string(encoded)
		}
		response := c.do(method, "/api/v1"+path, raw, nil)
		if response.Code != want {
			t.Fatalf("%s %s = %d %s; want %d", method, path, response.Code, response.Body.String(), want)
		}
		return response.Body.Bytes()
	}

	preview := templateBody[deploy.TemplatePreview](t, call(http.MethodPost, "/deploy/templates",
		map[string]any{"name": "Cached site", "compose": templateRouteCompose, "dryRun": true}, http.StatusOK))
	if preview.Definition.ID != "user-cached-site" || preview.Exists || len(preview.Definition.Services) != 2 {
		t.Fatalf("preview = %+v", preview)
	}
	if listed := templateBody[[]deploy.UserTemplate](t, call(http.MethodGet, "/deploy/templates", nil, http.StatusOK)); len(listed) != 0 {
		t.Fatalf("a dry run stored a template: %+v", listed)
	}
	if templateAuditCount(t, s, "deploy.template.create") != 0 {
		t.Fatal("a dry run was audited as a creation")
	}

	createdBody := call(http.MethodPost, "/deploy/templates",
		map[string]any{"name": "Cached site", "compose": templateRouteCompose, "healthPath": "/"}, http.StatusCreated)
	if strings.Contains(string(createdBody), "written-out-admin-password") {
		t.Fatalf("a password from the file reached the template: %s", createdBody)
	}
	created := templateBody[deploy.UserTemplateDetail](t, createdBody)
	if created.ID != "user-cached-site" || created.Source != deploy.TemplateFromCompose || created.CreatedBy != "tester" {
		t.Fatalf("created = %+v", created.UserTemplate)
	}
	if templateAuditCount(t, s, "deploy.template.create") != 1 {
		t.Fatal("creating a template was not audited")
	}
	call(http.MethodPost, "/deploy/templates", map[string]any{"name": "Cached site", "compose": templateRouteCompose}, http.StatusConflict)
	if preview := templateBody[deploy.TemplatePreview](t, call(http.MethodPost, "/deploy/templates",
		map[string]any{"name": "Cached site", "compose": templateRouteCompose, "dryRun": true}, http.StatusOK)); !preview.Exists {
		t.Fatal("a dry run did not say the id is taken")
	}

	// The picker's catalogue: the built-ins, then this dashboard's own.
	catalogue := templateBody[[]blueprint.Summary](t, call(http.MethodGet, "/deploy/blueprints/", nil, http.StatusOK))
	origins := map[string]string{}
	for _, entry := range catalogue {
		origins[entry.ID] = entry.Origin
	}
	if origins["user-cached-site"] != blueprint.OriginUser || origins["whoami"] != blueprint.OriginBuiltIn ||
		catalogue[len(catalogue)-1].ID != "user-cached-site" {
		t.Fatalf("the catalogue does not list both, marked: %v", origins)
	}
	detail := templateBody[blueprintDetail](t, call(http.MethodGet, "/deploy/blueprints/user-cached-site", nil, http.StatusOK))
	if detail.ID != "user-cached-site" || !detail.DeploymentSupported {
		t.Fatalf("detail = %+v", detail)
	}
	rendered := templateBody[deploy.BlueprintPlan](t, call(http.MethodPost, "/deploy/blueprints/user-cached-site/render",
		map[string]any{"name": "site", "inputs": map[string]string{"domain": "site.example.test"}}, http.StatusOK))
	if len(rendered.ComposeFiles) != 1 || rendered.Configuration.Domains[0].Hostname != "site.example.test" {
		t.Fatalf("rendered = %+v", rendered.Configuration)
	}
	call(http.MethodGet, "/deploy/blueprints/user-nothing", nil, http.StatusNotFound)

	// A draft chooses it exactly as it chooses a built-in.
	draft := templateBody[map[string]any](t, call(http.MethodPost, "/deploy/drafts", "{}", http.StatusCreated))
	id := fmt.Sprint(draft["id"])
	draft = templateBody[map[string]any](t, call(http.MethodPut, "/deploy/drafts/"+id, fmt.Sprintf(
		`{"revision":%v,"step":"intent","intent":{"name":"site","profile":"compose"}}`, draft["revision"]), http.StatusOK))
	saved := call(http.MethodPut, "/deploy/drafts/"+id, fmt.Sprintf(
		`{"revision":%v,"step":"source","source":{"kind":"compose","mode":"blueprint","blueprintId":"user-cached-site","blueprintVersion":"1.0.0","blueprintInputs":{"domain":"site.example.test"}}}`,
		draft["revision"]), http.StatusOK)
	var withSource struct {
		Data struct {
			Source deploy.DraftSourceConfig `json:"source"`
		} `json:"data"`
	}
	if err := json.Unmarshal(saved, &withSource); err != nil || len(withSource.Data.Source.BlueprintDefinition) == 0 ||
		len(withSource.Data.Source.ComposeFiles) != 1 {
		t.Fatalf("the draft's source did not freeze the template: %s", saved)
	}

	listed := templateBody[[]deploy.UserTemplate](t, call(http.MethodGet, "/deploy/templates", nil, http.StatusOK))
	if len(listed) != 1 || listed[0].ID != "user-cached-site" {
		t.Fatalf("listed = %+v", listed)
	}
	one := templateBody[deploy.UserTemplateDetail](t, call(http.MethodGet, "/deploy/templates/user-cached-site", nil, http.StatusOK))
	if one.Definition == nil || one.Definition.ID != "user-cached-site" {
		t.Fatalf("one = %+v", one)
	}
	call(http.MethodGet, "/deploy/templates/whoami", nil, http.StatusNotFound)

	// Export: an added template's file and a built-in's, the second imported
	// as this dashboard's own.
	exported := c.do(http.MethodGet, "/api/v1/deploy/templates/user-cached-site/export", "", nil)
	if exported.Code != http.StatusOK || !strings.Contains(exported.Header().Get("Content-Disposition"), `filename="user-cached-site.json"`) {
		t.Fatalf("export = %d %v", exported.Code, exported.Header())
	}
	if _, err := blueprint.ParseUserTemplate(exported.Body.Bytes()); err != nil {
		t.Fatalf("the export is not a definition: %v", err)
	}
	builtIn := c.do(http.MethodGet, "/api/v1/deploy/templates/whoami/export", "", nil)
	if builtIn.Code != http.StatusOK {
		t.Fatalf("built-in export = %d %s", builtIn.Code, builtIn.Body.String())
	}
	imported := templateBody[deploy.UserTemplateDetail](t, call(http.MethodPost, "/deploy/templates/import", builtIn.Body.String(), http.StatusCreated))
	if imported.ID != "user-whoami" || imported.Source != deploy.TemplateImported || templateAuditCount(t, s, "deploy.template.import") != 1 {
		t.Fatalf("imported = %+v", imported.UserTemplate)
	}
	call(http.MethodPost, "/deploy/templates/import", `{"id":"user-x","name":"X"}`, http.StatusUnprocessableEntity)

	edited := *imported.Definition
	edited.Description = "The echo server, as this team runs it."
	updated := templateBody[deploy.UserTemplateDetail](t, call(http.MethodPut, "/deploy/templates/user-whoami", edited, http.StatusOK))
	if updated.Version != "1.0.1" || templateAuditCount(t, s, "deploy.template.update") != 1 {
		t.Fatalf("updated = %+v", updated.UserTemplate)
	}

	call(http.MethodDelete, "/deploy/templates/user-whoami", nil, http.StatusNoContent)
	call(http.MethodDelete, "/deploy/templates/user-whoami", nil, http.StatusNotFound)
	if templateAuditCount(t, s, "deploy.template.delete") != 1 {
		t.Fatal("deleting a template was not audited")
	}
	var leaked int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE detail LIKE '%written-out-admin-password%'`).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("the audit log carries the file's password: %d %v", leaked, err)
	}
}

// What the catalogue refuses in a built-in, the routes refuse in a template,
// by name and before anything is stored.
func TestTemplateRoutesRefuseWhatTheCatalogueRefuses(t *testing.T) {
	s := testServer(t)
	wireDeployPlanningAndSources(t, s, []string{t.TempDir()})
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	for name, document := range map[string]string{
		"builds its image": "services:\n  web:\n    build: .\n",
		"host path ./site": strings.Replace(templateRouteCompose, "    ports: [\"8080:80\"]\n", "    ports: [\"8080:80\"]\n    volumes: [\"./site:/usr/share/nginx/html\"]\n", 1),
		"has no healthcheck": "services:\n  web:\n    image: nginx:1.27\n    ports: [\"80:80\"]\n  cache:\n    image: redis:7\n" +
			"    healthcheck:\n      test: [\"CMD\", \"true\"]\n",
	} {
		body, _ := json.Marshal(map[string]any{"name": "Refused", "compose": document})
		response := c.do(http.MethodPost, "/api/v1/deploy/templates", string(body), nil)
		if response.Code != http.StatusUnprocessableEntity || keyErrorCode(response) != "invalid_template" || !strings.Contains(response.Body.String(), name) {
			t.Fatalf("%s: %d %s", name, response.Code, response.Body.String())
		}
	}
	var stored int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_templates`).Scan(&stored); err != nil || stored != 0 {
		t.Fatalf("%d refused templates were stored: %v", stored, err)
	}
}

// seedLiveImageDeployment is an image deployment with a secret, plain values
// and a domain, whose live release is the run that froze them.
func seedLiveImageDeployment(t *testing.T, s *Server, secret string) (int64, int64) {
	t.Helper()
	ctx := t.Context()
	exec := func(query string, args ...any) int64 {
		t.Helper()
		result, err := s.Store.DB.ExecContext(ctx, query, args...)
		if err != nil {
			t.Fatalf("%v: %s", err, query)
		}
		id, _ := result.LastInsertId()
		return id
	}
	now := time.Now().Unix()
	project := exec(`INSERT INTO deploy_projects(name, profile, repo_path, branch, compose_file, hook_secret, hook_id, enabled, created_at, updated_at)
		VALUES('shop', 'image', '/srv/shop', 'main', 'compose.yml', '', 'shop-hook', 1, ?, ?)`, now, now)
	environment := exec(`INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, strategy, expected_downtime, protected, created_at, updated_at)
		VALUES(?, 'Production', 'production', 'production', 1, 'stop_first', 1, 1, ?, ?)`, project, now, now)
	source, _ := json.Marshal(deploy.DraftSourceConfig{Kind: deploy.SourceImage, Mode: deploy.SourceModeImageReference, Image: "traefik/whoami:v1.12.0"})
	build, _ := json.Marshal(deploy.BuildPlanConfig{Method: deploy.BuildImage, Secrets: []deploy.BuildSecretConfig{}, ReleaseTasks: []deploy.ReleaseTaskConfig{}})
	runtime, _ := json.Marshal(deploy.RuntimePlanConfig{
		Image: "traefik/whoami:v1.12.0", InternalPort: 80, BindAddress: "127.0.0.1", Strategy: deploy.StrategyStopFirst,
		Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []deploy.RuntimeMount{},
	})
	exec(`INSERT INTO deploy_sources(environment_id, revision, kind, config_json, identity_json, digest, created_at)
		VALUES(?, 1, 'image', ?, '{"kind":"image"}', 'sha256:seed', ?)`, environment, string(source), now)
	exec(`INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at)
		VALUES(?, 1, 'image', ?, '{"candidates":[],"gitRequirements":{}}', '{}', 'sha256:build', ?)`, environment, string(build), now)
	exec(`INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at)
		VALUES(?, 1, ?, '{}', 'sha256:runtime', ?)`, environment, string(runtime), now)
	for _, variable := range []struct{ name, sensitivity, value string }{
		{"SIGNING_KEY", "secret", secret}, {"WHOAMI_NAME", "plain", "shop"}, {"PUBLIC_URL", "plain", "https://shop.example.com"},
	} {
		sealed, err := s.Sealer.Seal(variable.value)
		if err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO deploy_variable_revisions(environment_id, key, revision, sensitivity, scopes, value_enc, value_digest, value_mode, active, created_by, created_at)
			VALUES(?, ?, 1, ?, 'runtime', ?, ?, 'literal', 1, 'admin', ?)`, environment, variable.name, variable.sensitivity, sealed,
			"sha256:"+strings.Repeat(fmt.Sprintf("%x", len(variable.name)%16), 64), now)
	}
	exec(`INSERT INTO deploy_checks(environment_id, runtime_plan_id, name, kind, phase, config_json, required, ordinal, created_at)
		VALUES(?, 0, 'home', 'http', 'readiness', '{"path":"/","attempts":10,"intervalSeconds":2}', 1, 1, ?)`, environment, now)
	exec(`INSERT INTO deploy_dependencies(environment_id, release_id, kind, ownership, resource_kind, resource_id, config_json, created_at)
		VALUES(?, 0, 'domain', 'managed', 'proxy_site', 'shop.example.com', '{"hostname":"shop.example.com","https":true,"ownership":"managed"}', ?)`, environment, now)
	run, _, err := s.modules.deployRuns.Enqueue(ctx, deploy.RunRequest{
		ProjectID: project, EnvironmentID: environment, Operation: deploy.OperationDeploy, Trigger: deploy.TriggerManual,
		Actor: "admin", RequestDigest: "template-route", PlanRevision: 1, SlotClass: deploy.SlotHeavy,
	})
	if err != nil {
		t.Fatal(err)
	}
	release := exec(`INSERT INTO deploy_releases(project_id, environment_id, release_number, run_id, state, created_at, activated_at)
		VALUES(?, ?, 1, ?, 'live', ?, ?)`, project, environment, run.ID, now, now)
	exec(`UPDATE deploy_environments SET live_release_id = ? WHERE id = ?`, release, environment)
	return project, environment
}

// Save as template through the route: the answer, the stored definition, its
// export and the audit entry hold neither the deployment's secret nor its
// domain, and a deployment that never went live is told so.
func TestSaveAsTemplateNeverCarriesTheDeploymentsSecret(t *testing.T) {
	s := testServer(t)
	wireDeployPlanningAndSources(t, s, []string{t.TempDir()})
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	const secret = "the-shops-own-signing-key-value"
	project, environment := seedLiveImageDeployment(t, s, secret)

	body := fmt.Sprintf(`{"deploymentId":%d,"name":"Shop","dryRun":true}`, project)
	dry := c.do(http.MethodPost, "/api/v1/deploy/templates/from-deployment", body, nil)
	if dry.Code != http.StatusOK || strings.Contains(dry.Body.String(), secret) {
		t.Fatalf("dry run = %d %s", dry.Code, dry.Body.String())
	}
	body = fmt.Sprintf(`{"deploymentId":%d,"environmentId":%d,"name":"Shop"}`, project, environment)
	created := c.do(http.MethodPost, "/api/v1/deploy/templates/from-deployment", body, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("save = %d %s", created.Code, created.Body.String())
	}
	exported := c.do(http.MethodGet, "/api/v1/deploy/templates/user-shop/export", "", nil)
	var stored, auditDetail string
	if err := s.Store.DB.QueryRow(`SELECT definition_json FROM deploy_templates WHERE slug = 'user-shop'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.DB.QueryRow(`SELECT detail FROM audit_log WHERE action = 'deploy.template.create'`).Scan(&auditDetail); err != nil {
		t.Fatal(err)
	}
	for where, text := range map[string]string{
		"answer": created.Body.String(), "export": exported.Body.String(), "store": stored, "audit": auditDetail,
	} {
		if strings.Contains(text, secret) || strings.Contains(text, "shop.example.com") {
			t.Fatalf("the %s carries the deployment's own secret or domain:\n%s", where, text)
		}
	}
	saved := templateBody[deploy.UserTemplateDetail](t, created.Body.Bytes())
	secretInput := false
	for _, input := range saved.Definition.Inputs {
		secretInput = secretInput || (input.Variable == "SIGNING_KEY" && input.Kind == blueprint.InputSecret && input.Required)
	}
	if !secretInput || saved.Source != deploy.TemplateFromDeployment || saved.FromDeploymentID != project ||
		!strings.Contains(auditDetail, `"source":"deployment"`) {
		t.Fatalf("saved = %+v; audit %s", saved, auditDetail)
	}

	if _, err := s.Store.DB.Exec(`UPDATE deploy_environments SET live_release_id = 0 WHERE id = ?`, environment); err != nil {
		t.Fatal(err)
	}
	notLive := c.do(http.MethodPost, "/api/v1/deploy/templates/from-deployment",
		fmt.Sprintf(`{"deploymentId":%d,"name":"Shop two"}`, project), nil)
	if notLive.Code != http.StatusConflict || keyErrorCode(notLive) != "no_live_release" {
		t.Fatalf("not live = %d %s", notLive.Code, notLive.Body.String())
	}
	other := c.do(http.MethodPost, "/api/v1/deploy/templates/from-deployment",
		fmt.Sprintf(`{"deploymentId":%d,"environmentId":%d,"name":"Shop three"}`, project+1, environment), nil)
	if other.Code != http.StatusNotFound {
		t.Fatalf("another deployment's environment = %d %s", other.Code, other.Body.String())
	}
}

// A template is the operator's catalogue: a deploy key reads it and is
// refused every write in words, an ordinary key needs a session for them, and
// a limited account cannot make one at all.
func TestADeployKeyReadsTheTemplatesAndIsRefusedTheirWrites(t *testing.T) {
	b := newKeyBench(t)
	if created := b.inSession(http.MethodPost, "/api/v1/deploy/templates",
		map[string]any{"name": "Cached site", "compose": templateRouteCompose}); created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	for _, read := range []string{"/api/v1/deploy/templates", "/api/v1/deploy/templates/user-cached-site",
		"/api/v1/deploy/templates/user-cached-site/export", "/api/v1/deploy/blueprints/user-cached-site"} {
		if w := b.call(b.deploy, http.MethodGet, read, nil); w.Code != http.StatusOK {
			t.Fatalf("a deploy key reading %s = %d %s", read, w.Code, w.Body.String())
		}
	}
	writes := []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/deploy/templates", `{"name":"x","compose":"services: {}"}`},
		{http.MethodPost, "/api/v1/deploy/templates/from-deployment", `{"deploymentId":1,"name":"x"}`},
		{http.MethodPost, "/api/v1/deploy/templates/import", `{"name":"x"}`},
		{http.MethodPut, "/api/v1/deploy/templates/user-cached-site", `{"name":"x"}`},
		{http.MethodDelete, "/api/v1/deploy/templates/user-cached-site", ""},
	}
	for _, write := range writes {
		for name, key := range map[string]string{"deploy": b.deploy, "ordinary": b.ordinary} {
			w := b.call(key, write.method, write.path, write.body)
			if w.Code != http.StatusForbidden || keyErrorCode(w) != "session_required" {
				t.Fatalf("%s key %s %s = %d %s", name, write.method, write.path, w.Code, w.Body.String())
			}
			if name == "deploy" && !strings.Contains(w.Body.String(), "operator's catalogue") {
				t.Fatalf("the deploy key was not told why: %s", w.Body.String())
			}
		}
	}
	limited := &client{t: t, h: b.h, cookie: signInAs(t, b.s, "limited", auth.RoleLimited)}
	if w := limited.do(http.MethodPost, "/api/v1/deploy/templates", `{"name":"x","compose":"services: {}"}`, nil); w.Code != http.StatusForbidden {
		t.Fatalf("a limited account = %d %s", w.Code, w.Body.String())
	}
	if w := limited.do(http.MethodDelete, "/api/v1/deploy/templates/user-cached-site", "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("a limited account deleting = %d %s", w.Code, w.Body.String())
	}
	if w := limited.do(http.MethodGet, "/api/v1/deploy/templates", "", nil); w.Code != http.StatusOK {
		t.Fatalf("a limited account reading = %d %s", w.Code, w.Body.String())
	}
	var refused int
	if err := b.s.Store.DB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM audit_log WHERE action = 'auth.deploy_key.refused' AND target LIKE '%/deploy/templates%'`).Scan(&refused); err != nil || refused != len(writes) {
		t.Fatalf("%d of %d refusals were audited: %v", refused, len(writes), err)
	}
}
