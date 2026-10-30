package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/blueprint"
	basestore "github.com/Wayy01/Just-Dashboard/backend/internal/store"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

const templateTestCompose = `services:
  web:
    image: nginx:1.27-alpine
    ports: ["8080:80"]
    environment:
      CACHE_HOST: cache
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://127.0.0.1/"]
      interval: 5s
    depends_on:
      cache: {condition: service_healthy}
  cache:
    image: redis:7.4-alpine
    volumes: ["cache:/data"]
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
`

type templateFixture struct {
	base     *basestore.Store
	planning *PlanningStore
	runs     *OrchestrationStore
	now      time.Time
}

func newTemplateFixture(t *testing.T) *templateFixture {
	t.Helper()
	base, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	sealer, err := auth.NewSealer(strings.Repeat("7c", 32))
	if err != nil {
		t.Fatal(err)
	}
	fixture := &templateFixture{base: base, now: time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)}
	fixture.planning = NewPlanningStore(base, sealer, []string{"/srv"})
	fixture.planning.now = func() time.Time { return fixture.now }
	fixture.runs = NewOrchestrationStore(base)
	fixture.runs.now = func() time.Time { return fixture.now }
	return fixture
}

// imageDeployment is a committed image deployment whose desired plan has a
// sealed secret, plain values (one naming the domain), a reference, a fixed
// host port beside the routed one, a managed volume, a check and a domain —
// and the run that froze it, as a deploy would.
func (f *templateFixture) imageDeployment(t *testing.T, secret string) (int64, int64, *EngineRun) {
	t.Helper()
	ctx := context.Background()
	exec := func(query string, args ...any) int64 {
		t.Helper()
		result, err := f.base.DB.ExecContext(ctx, query, args...)
		if err != nil {
			t.Fatalf("%s: %v", strings.Fields(query)[0:3], err)
		}
		id, _ := result.LastInsertId()
		return id
	}
	now := f.now.Unix()
	project := exec(`INSERT INTO deploy_projects(name, profile, repo_path, branch, compose_file, hook_secret, hook_id, enabled, created_at, updated_at)
		VALUES('shop', 'image', '/srv/shop', 'main', 'compose.yml', '', 'shop-hook', 1, ?, ?)`, now, now)
	environment := exec(`INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, strategy, expected_downtime, protected, created_at, updated_at)
		VALUES(?, 'Production', 'production', 'production', 1, 'stop_first', 1, 1, ?, ?)`, project, now, now)
	source := DraftSourceConfig{Kind: SourceImage, Mode: SourceModeImageReference, Image: "docker.io/library/nginx:1.27-alpine"}
	identity := SourceIdentity{Kind: SourceImage, Repository: "docker.io/library/nginx", Ref: "1.27-alpine"}
	build := BuildPlanConfig{Method: BuildImage, Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{}}
	runtime := RuntimePlanConfig{
		Image: "docker.io/library/nginx:1.27-alpine", InternalPort: 80, BindAddress: "127.0.0.1", Strategy: StrategyStopFirst,
		Command: []string{"nginx", "-g", "daemon off;"}, MemoryMB: 256, Capabilities: []string{}, Devices: []string{},
		Mounts: []RuntimeMount{{Source: "shop-a1b2c3-html", Target: "/usr/share/nginx/html", Ownership: OwnershipManaged}},
		Ports:  []PublishedPort{{HostPort: 2222, ContainerPort: 22, Protocol: "tcp"}},
	}
	sourceJSON, _ := json.Marshal(source)
	identityJSON, _ := json.Marshal(identity)
	buildJSON, _ := json.Marshal(build)
	runtimeJSON, _ := json.Marshal(runtime)
	exec(`INSERT INTO deploy_sources(environment_id, revision, kind, config_json, identity_json, digest, created_at) VALUES(?, 1, 'image', ?, ?, ?, ?)`,
		environment, string(sourceJSON), string(identityJSON), digestBytes(sourceJSON, identityJSON), now)
	exec(`INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at)
		VALUES(?, 1, 'image', ?, '{"candidates":[],"gitRequirements":{}}', ?, ?, ?)`, environment, string(buildJSON), string(buildJSON), digestBytes(buildJSON), now)
	exec(`INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at) VALUES(?, 1, ?, ?, ?, ?)`,
		environment, string(runtimeJSON), string(runtimeJSON), digestBytes(runtimeJSON), now)
	for _, variable := range []struct{ name, sensitivity, value, mode string }{
		{"DB_PASSWORD", "secret", secret, "literal"},
		// Marked plain by mistake, and credential-shaped: still never a default.
		{"API_TOKEN", "plain", "plain-but-a-token-" + secret, "literal"},
		{"TZ", "plain", "Europe/Paris", "literal"},
		{"PUBLIC_URL", "plain", "https://Shop.Example.com/store", "literal"},
		{"SHARED_URL", "plain", "${{server.SHARED_URL}}", "reference"},
	} {
		sealed, err := f.planning.sealer.Seal(variable.value)
		if err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO deploy_variable_revisions(environment_id, key, revision, sensitivity, scopes, value_enc, value_digest, value_mode, active, created_by, created_at)
			VALUES(?, ?, 1, ?, 'runtime', ?, ?, ?, 1, 'admin', ?)`, environment, variable.name, variable.sensitivity, sealed,
			fakeContentDigest(variable.value), variable.mode, now)
	}
	exec(`INSERT INTO deploy_checks(environment_id, runtime_plan_id, name, kind, phase, config_json, required, ordinal, created_at)
		VALUES(?, 0, 'home', 'http', 'readiness', '{"path":"/health","port":80,"attempts":30,"intervalSeconds":2}', 1, 1, ?)`, environment, now)
	exec(`INSERT INTO deploy_checks(environment_id, runtime_plan_id, name, kind, phase, config_json, required, ordinal, created_at)
		VALUES(?, 0, 'certificate', 'tls', 'public', '{}', 0, 2, ?)`, environment, now)
	exec(`INSERT INTO deploy_dependencies(environment_id, release_id, kind, ownership, resource_kind, resource_id, config_json, created_at)
		VALUES(?, 0, 'domain', 'managed', 'proxy_site', 'shop.example.com', '{"hostname":"shop.example.com","https":true,"ownership":"managed"}', ?)`, environment, now)
	run, _, err := f.runs.Enqueue(ctx, RunRequest{
		ProjectID: project, EnvironmentID: environment, Operation: OperationDeploy, Trigger: TriggerManual,
		Actor: "admin", RequestDigest: "template-fixture", PlanRevision: 1, SlotClass: SlotHeavy,
	})
	if err != nil {
		t.Fatal(err)
	}
	return project, environment, run
}

// Save as template, from a real frozen plan: nothing this deployment holds
// that is its own reaches the definition — not a secret, not a value marked
// plain whose name is a credential's, not its domain, not its volume's name
// — and the next deployment answers each of those for itself.
func TestSavingADeploymentAsATemplateKeepsNothingOfItsOwn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newTemplateFixture(t)
	const secret = "s3cr3t-value-only-the-shop-has"
	project, environment, run := fixture.imageDeployment(t, secret)
	plan, err := fixture.runs.ExecutionPlan(ctx, *run)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := fixture.planning.FrozenPlainValues(ctx, run.ID, environment)
	if err != nil {
		t.Fatal(err)
	}
	if _, opened := plain["DB_PASSWORD"]; opened {
		t.Fatal("a secret's value was opened for the template")
	}
	if _, opened := plain["SHARED_URL"]; opened {
		t.Fatal("a reference was read as a literal")
	}
	definition, raw, err := fixture.planning.DeploymentTemplate(ctx, plan, run.ID, environment,
		DeploymentTemplateRequest{DeploymentID: project, Name: "Shop front"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	// The host port is not one of them: it is an input whose default is the
	// number this deployment chose, which the next one may keep or change.
	for _, leaked := range []string{secret, "shop.example.com", "Shop.Example.com", "shop-a1b2c3"} {
		if strings.Contains(string(raw), leaked) {
			t.Fatalf("the definition carries %q:\n%s", leaked, raw)
		}
	}
	inputs := map[string]blueprint.Input{}
	for _, input := range definition.Inputs {
		inputs[input.Variable+"|"+input.Name] = input
	}
	if input := inputs["DB_PASSWORD|db-password"]; input.Kind != blueprint.InputSecret || !input.Required || input.Default != "" {
		t.Fatalf("DB_PASSWORD = %+v", input)
	}
	if input := inputs["API_TOKEN|api-token"]; input.Kind != blueprint.InputSecret || input.Default != "" {
		t.Fatalf("API_TOKEN = %+v", input)
	}
	if input := inputs["TZ|tz"]; input.Default != "Europe/Paris" {
		t.Fatalf("TZ = %+v", input)
	}
	if input := inputs["SHARED_URL|shared-url"]; input.Required || input.Default != "" {
		t.Fatalf("SHARED_URL = %+v", input)
	}
	if input := inputs["|domain"]; input.Kind != blueprint.InputDomain || !input.Required {
		t.Fatalf("the domain is not a required input: %+v", definition.Inputs)
	}
	hostInput := ""
	for _, port := range definition.Ports {
		if port.Internal == 22 {
			hostInput = port.HostInput
		}
	}
	if input := inputs["|"+hostInput]; input.Kind != blueprint.InputNumber || input.Default != "2222" {
		t.Fatalf("the host port is not an input defaulting to it: %q %+v", hostInput, definition.Ports)
	}
	if definition.ID != "user-shop-front" || definition.Image.Reference != "nginx:1.27-alpine" ||
		definition.Volumes[0].Name != "html" || definition.Checks[0].Path != "/health" || len(definition.Checks) != 1 {
		t.Fatalf("definition = %s %s %+v %+v", definition.ID, definition.Image.Reference, definition.Volumes, definition.Checks)
	}

	// Stored, chosen and planned as a draft would plan it, for another
	// deployment at another address on another port.
	saved, err := fixture.planning.SaveDeploymentTemplate(ctx, definition, raw, project, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Source != TemplateFromDeployment || saved.FromDeploymentID != project {
		t.Fatalf("saved = %+v", saved.UserTemplate)
	}
	source, err := fixture.planning.WithUserTemplate(ctx, DraftSourceConfig{
		Kind: SourceBlueprint, Mode: SourceModeBlueprint, BlueprintID: saved.ID, BlueprintVersion: saved.Version,
		BlueprintInputs: map[string]string{"domain": "next.example.org", hostInput: "2200"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := source.validateForNewDeployment(); err != nil {
		t.Fatal(err)
	}
	planned, err := RenderBlueprintPlan(source, "next")
	if err != nil {
		t.Fatal(err)
	}
	configuration := planned.Configuration
	values := map[string]PlannedVariable{}
	for _, variable := range configuration.Variables {
		values[variable.Name] = variable
	}
	if values["PUBLIC_URL"].Value != "https://next.example.org/store" || values["DB_PASSWORD"].Sensitivity != "secret" ||
		!values["DB_PASSWORD"].Required || values["DB_PASSWORD"].Value != "" {
		t.Fatalf("variables = %+v", configuration.Variables)
	}
	if len(configuration.Domains) != 1 || configuration.Domains[0].Hostname != "next.example.org" ||
		configuration.Runtime.InternalPort != 80 || len(configuration.Runtime.Ports) != 1 ||
		configuration.Runtime.Ports[0].HostPort != 2200 || configuration.Runtime.Ports[0].ContainerPort != 22 {
		t.Fatalf("runtime = %+v, domains = %+v", configuration.Runtime, configuration.Domains)
	}
	if !strings.HasPrefix(configuration.Runtime.Mounts[0].Source, "next-") {
		t.Fatalf("the volume is not the new deployment's own: %+v", configuration.Runtime.Mounts)
	}
}

func TestATemplateIsFrozenIntoTheSourceAndOutlivesTheTemplate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newTemplateFixture(t)
	created, err := fixture.planning.CreateComposeTemplate(ctx, ComposeTemplateRequest{Name: "Cached site", Compose: templateTestCompose}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "user-cached-site" || created.Source != TemplateFromCompose || created.Summary.Origin != blueprint.OriginUser ||
		created.Summary.SourceKind != "compose" || created.CreatedBy != "admin" {
		t.Fatalf("created = %+v", created.UserTemplate)
	}
	chosen := DraftSourceConfig{
		Kind: SourceCompose, Mode: SourceModeBlueprint, BlueprintID: created.ID, BlueprintVersion: created.Version,
		BlueprintInputs: map[string]string{"domain": "site.example.test"},
		// What a client sends is never what is kept.
		BlueprintDefinition: json.RawMessage(`{"id":"user-cached-site","forged":true}`),
	}
	if err := chosen.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := chosen.definition(); err == nil {
		t.Fatal("a forged definition was read")
	}
	source, err := fixture.planning.WithUserTemplate(ctx, chosen)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source.BlueprintDefinition), "forged") {
		t.Fatal("the definition the client sent survived the save")
	}
	source, err = source.WithTemplateDocuments()
	if err != nil {
		t.Fatal(err)
	}
	if err := source.validateForNewDeployment(); err != nil {
		t.Fatal(err)
	}
	if len(source.ComposeFiles) != 1 || !strings.Contains(source.ComposeFiles[0].Content, "this dashboard's own user-cached-site@1.0.0") {
		t.Fatalf("compose files = %+v", source.ComposeFiles)
	}

	// Deleting the template stops nothing made from it: the source renders
	// and redeploys from its own copy, and only choosing it again is refused.
	if _, err := fixture.planning.DeleteTemplate(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := source.ValidateForDeployment(); err != nil {
		t.Fatalf("a deployment of a removed template cannot redeploy: %v", err)
	}
	if _, err := RenderBlueprintPlan(source, "site"); err != nil {
		t.Fatalf("a deployment of a removed template cannot render: %v", err)
	}
	if _, err := fixture.planning.WithUserTemplate(ctx, chosen); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("a removed template was chosen again: %v", err)
	}

	// A user template's source holds its definition; a built-in's never does.
	unsaved := chosen
	unsaved.BlueprintDefinition = nil
	if err := unsaved.Validate(); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("a user template source without its definition validated: %v", err)
	}
	builtIn := DraftSourceConfig{Kind: SourceBlueprint, Mode: SourceModeBlueprint, BlueprintID: "whoami", BlueprintVersion: "1.0.0",
		BlueprintDefinition: source.BlueprintDefinition}
	if err := builtIn.Validate(); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("a built-in source carried a definition: %v", err)
	}
	cleared, err := fixture.planning.WithUserTemplate(ctx, builtIn)
	if err != nil || len(cleared.BlueprintDefinition) != 0 {
		t.Fatalf("a built-in's save kept a definition: %v", err)
	}
}

func TestTemplatesAreImportedUpdatedAndResolvedAfterTheBuiltIns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newTemplateFixture(t)
	whoami, err := blueprint.Get("whoami")
	if err != nil {
		t.Fatal(err)
	}
	exported, _ := json.Marshal(whoami)
	imported, err := fixture.planning.ImportTemplate(ctx, exported, "admin")
	if err != nil {
		t.Fatal(err)
	}
	// An exported built-in becomes this dashboard's own beside it.
	if imported.ID != "user-whoami" || imported.Source != TemplateImported || imported.Definition.Fixtures[0].Digest == whoami.Fixtures[0].Digest {
		t.Fatalf("imported = %+v", imported.UserTemplate)
	}
	if _, err := fixture.planning.ImportTemplate(ctx, exported, "admin"); !errors.Is(err, ErrTemplateExists) {
		t.Fatalf("a second import under the same id = %v", err)
	}
	builtIn, err := fixture.planning.ResolveTemplate(ctx, "whoami", "")
	if err != nil || builtIn != whoami {
		t.Fatalf("the built-in did not resolve to itself: %v", err)
	}
	added, err := fixture.planning.ResolveTemplate(ctx, "user-whoami", "1.0.0")
	if err != nil || added.ID != "user-whoami" {
		t.Fatalf("the added template did not resolve: %v", err)
	}
	if _, err := fixture.planning.ResolveTemplate(ctx, "user-nothing", ""); !errors.Is(err, blueprint.ErrNotFound) {
		t.Fatalf("an unknown template = %v", err)
	}

	// Updating: the same definition is no change; a changed one at the same
	// version is the next patch; the id never changes.
	unchanged, _ := json.Marshal(imported.Definition)
	same, err := fixture.planning.UpdateTemplate(ctx, imported.ID, unchanged, "editor")
	if err != nil || same.Version != "1.0.0" || same.UpdatedBy != "admin" {
		t.Fatalf("an unchanged update = %+v, %v", same, err)
	}
	edited := *imported.Definition
	edited.Description = "The echo server, as this team runs it."
	encoded, _ := json.Marshal(edited)
	fixture.now = fixture.now.Add(time.Hour)
	updated, err := fixture.planning.UpdateTemplate(ctx, imported.ID, encoded, "editor")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != "1.0.1" || updated.Definition.Description != edited.Description || updated.UpdatedBy != "editor" ||
		!updated.UpdatedAt.After(updated.CreatedAt) {
		t.Fatalf("updated = %+v", updated.UserTemplate)
	}
	renamed := edited
	renamed.ID = "user-other"
	encoded, _ = json.Marshal(renamed)
	if _, err := fixture.planning.UpdateTemplate(ctx, imported.ID, encoded, "editor"); !errors.Is(err, ErrInvalidTemplate) {
		t.Fatalf("an update that renames = %v", err)
	}
	// A draft that chose the old version is told to choose again.
	if _, err := fixture.planning.WithUserTemplate(ctx, DraftSourceConfig{
		Kind: SourceBlueprint, Mode: SourceModeBlueprint, BlueprintID: imported.ID, BlueprintVersion: "1.0.0",
	}); !errors.Is(err, ErrInvalidSource) || !strings.Contains(err.Error(), "1.0.1") {
		t.Fatalf("a stale version = %v", err)
	}
	listed, err := fixture.planning.UserTemplates(ctx)
	if err != nil || len(listed) != 1 || listed[0].Summary.Origin != blueprint.OriginUser {
		t.Fatalf("listed = %+v, %v", listed, err)
	}
	if _, err := fixture.planning.ImportTemplate(ctx, []byte(`{"id":"user-x","surprise":1}`), "admin"); !errors.Is(err, ErrInvalidTemplate) {
		t.Fatalf("an unknown field = %v", err)
	}
	if _, err := fixture.planning.DeleteTemplate(ctx, "user-nothing"); !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("deleting nothing = %v", err)
	}
}

// The planner holds a template to its rules at save, not after a deployment
// was started from it.
func TestATemplateThePlannerRefusesIsNotStored(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newTemplateFixture(t)
	for name, document := range map[string]string{
		"a host path":            strings.Replace(templateTestCompose, `volumes: ["cache:/data"]`, `volumes: ["/srv/cache:/data"]`, 1),
		"a privileged container": strings.Replace(templateTestCompose, "    image: redis:7.4-alpine\n", "    image: redis:7.4-alpine\n    privileged: true\n", 1),
	} {
		_, err := fixture.planning.CreateComposeTemplate(ctx, ComposeTemplateRequest{Name: name, Compose: document}, "admin")
		if !errors.Is(err, ErrInvalidTemplate) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	listed, err := fixture.planning.UserTemplates(ctx)
	if err != nil || len(listed) != 0 {
		t.Fatalf("refused templates were stored: %+v %v", listed, err)
	}
}

func TestTemplateFromPlanRefusesWhatATemplateCannotRun(t *testing.T) {
	t.Parallel()
	meta := blueprint.TemplateMeta{ID: "user-x", Name: "X"}
	image := DraftSourceConfig{Kind: SourceImage, Mode: SourceModeImageReference, Image: "nginx:1.27"}
	for name, plan := range map[string]*StoredExecutionPlan{
		"a Git source":           {SourceConfig: DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/x.git"}},
		"a privileged container": {SourceConfig: image, Runtime: RuntimePlanConfig{InternalPort: 80, Privileged: true}},
		"a host path": {SourceConfig: image, Runtime: RuntimePlanConfig{InternalPort: 80,
			Mounts: []RuntimeMount{{Source: "/srv/data", Target: "/data", Ownership: OwnershipLinked}}}},
		"two Compose files": {SourceConfig: DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposePaste,
			ComposeFiles: []ComposeDocument{{Path: "a.yml"}, {Path: "b.yml"}}}},
	} {
		if _, err := TemplateFromPlan(plan, nil, meta); !errors.Is(err, ErrInvalidTemplate) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

// A pasted Compose deployment saved as a template: each secret variable the
// file reads is a secret input, a plain one keeps its value as the default,
// a variable the file never reads is left out, and the domain is the input.
func TestAComposeDeploymentBecomesAStackTemplate(t *testing.T) {
	t.Parallel()
	document := `services:
  app:
    image: ghcr.io/example/app:2.1.0
    ports: ["127.0.0.1:3000:3000"]
    environment:
      DATABASE_PASSWORD: ${DATABASE_PASSWORD}
      APP_URL: ${APP_URL}
      WORKERS: ${WORKERS}
    healthcheck: {test: ["CMD", "true"]}
  db:
    image: postgres:17-alpine
    environment:
      POSTGRES_PASSWORD: ${DATABASE_PASSWORD}
    volumes: ["database:/var/lib/postgresql/data"]
    healthcheck: {test: ["CMD", "pg_isready"]}
`
	plan := &StoredExecutionPlan{
		SourceConfig: DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposePaste,
			ComposeFiles: []ComposeDocument{{Path: "compose.yaml", Content: document}}},
		Build: BuildPlanConfig{Method: BuildCompose, PrimaryService: "app"},
		Variables: []ReleaseVariableSnapshot{
			{Name: "DATABASE_PASSWORD", Sensitivity: "secret", Scopes: "runtime"},
			{Name: "APP_URL", Sensitivity: "plain", Scopes: "runtime"},
			{Name: "WORKERS", Sensitivity: "plain", Scopes: "runtime"},
			{Name: "UNUSED", Sensitivity: "plain", Scopes: "runtime"},
			{Name: "BUILD_ONLY", Sensitivity: "plain", Scopes: "build"},
		},
		Dependencies: []json.RawMessage{json.RawMessage(`{"kind":"domain","ownership":"managed","resourceKind":"proxy_site","resourceId":"app.example.com","config":{"hostname":"app.example.com","https":true}}`)},
	}
	plain := map[string]string{
		// A secret's value in the map by mistake is still never read.
		"DATABASE_PASSWORD": "never-this", "APP_URL": "https://app.example.com/", "WORKERS": "4", "UNUSED": "x",
	}
	definition, err := TemplateFromPlan(plan, plain, blueprint.TemplateMeta{ID: "user-app", Name: "App", MinimumDashboard: "0.8.2"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := prepareTemplate(definition)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"never-this", "app.example.com", "UNUSED", "BUILD_ONLY"} {
		if strings.Contains(string(raw), leaked) {
			t.Fatalf("the definition carries %q:\n%s", leaked, raw)
		}
	}
	kinds := map[string]string{}
	for _, input := range definition.Inputs {
		kinds[input.Name] = fmt.Sprintf("%s:%s:%v", input.Kind, input.Default, input.Required)
	}
	if kinds["database-password"] != "secret::true" || kinds["workers"] != "text:4:false" || kinds["domain"] != "domain::true" {
		t.Fatalf("inputs = %v", kinds)
	}
	if len(definition.Operations.Startup) != 1 || definition.Operations.Startup[0].Value != "https://{{input.domain}}/" {
		t.Fatalf("startup = %+v", definition.Operations.Startup)
	}
}
