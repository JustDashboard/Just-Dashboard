package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseDotenvSupportsMultilineAndRejectsDuplicates(t *testing.T) {
	parsed, err := ParseDotenv("# comment\nPLAIN=value\nMULTI=\"first\\nsecond\"\nBLOCK='one\ntwo'\nEMPTY=\n")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"PLAIN": "value", "MULTI": "first\nsecond", "BLOCK": "one\ntwo", "EMPTY": "",
	}
	if !reflect.DeepEqual(parsed, want) {
		t.Fatalf("ParseDotenv = %#v, want %#v", parsed, want)
	}
	if _, err := ParseDotenv("A=one\nA=two\n"); !errors.Is(err, ErrInvalidVariable) || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate dotenv error = %v", err)
	}
	if _, err := ParseDotenv("FIRST=ok\nBROKEN='never\ncloses"); !errors.Is(err, ErrInvalidVariable) || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("unterminated dotenv error = %v", err)
	}
}

func TestVariableReferenceGraphDetectsMissingAndCycles(t *testing.T) {
	resolved, secret, err := ResolveVariableGraph(map[string]string{
		"A": "${{variable.B}}", "B": "literal",
	}, nil)
	if err != nil || resolved["A"] != "literal" || secret["A"] {
		t.Fatalf("resolved graph = %#v/%#v, error=%v", resolved, secret, err)
	}
	if _, _, err := ResolveVariableGraph(map[string]string{"A": "${{variable.MISSING}}"}, nil); !errors.Is(err, ErrInvalidVariable) {
		t.Fatalf("missing reference error = %v", err)
	}
	if _, _, err := ResolveVariableGraph(map[string]string{
		"A": "${{variable.B}}", "B": "${{variable.A}}",
	}, nil); !errors.Is(err, ErrVariableCycle) {
		t.Fatalf("cycle error = %v", err)
	}
	resolved, secret, err = ResolveVariableGraph(map[string]string{
		"TOKEN": "${{credential.production-token}}",
	}, func(reference VariableReference) (string, bool, error) {
		return "opened-secret", reference.Kind == "credential", nil
	})
	if err != nil || resolved["TOKEN"] != "opened-secret" || !secret["TOKEN"] {
		t.Fatalf("external secret leaf = %#v/%#v, error=%v", resolved, secret, err)
	}
}

func TestScopedVariablesResolveFeatureOwnerReferencesAndFreezeRunDomains(t *testing.T) {
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	evidence, err := json.Marshal(StoredBuildEvidence{Compose: &ComposeAnalysis{Services: []ComposeServicePlan{{
		Name: "web", Ports: []string{"8080:80"}, Mounts: []string{}, Advanced: []string{},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	projectID, environmentID := insertConfigurationFixtureWithEvidence(t, fixture, string(evidence))

	credential, err := fixture.sealer.Seal("credential-value")
	if err != nil {
		t.Fatal(err)
	}
	database, err := fixture.sealer.Seal("postgres://app:secret@database.internal/app")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB.Exec(`
		INSERT INTO deploy_credentials(name, kind, config_json, secret_enc, created_at, updated_at)
		VALUES('production-token', 'provider_token', '{}', ?, 1, 1)`, credential); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB.Exec(`
		INSERT INTO db_connections(name, driver, dsn_enc, created_at)
		VALUES('primary', 'postgres', ?, 1)`, database); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB.Exec(`
		INSERT INTO deploy_dependencies(
		  environment_id, release_id, kind, ownership, resource_kind,
		  resource_id, config_json, created_at)
		VALUES(?, 0, 'domain', 'managed', 'proxy_site', 'app.example.test',
		       '{"hostname":"app.example.test","https":true}', 1)`, environmentID); err != nil {
		t.Fatal(err)
	}
	revision := 1
	for _, variable := range []struct {
		name, reference, sensitivity string
	}{
		{"TOKEN", "${{credential.production-token}}", "secret"},
		{"DATABASE_URL", "${{database.primary.url}}", "secret"},
		{"PUBLIC_URL", "${{domain.app.example.test}}", "plain"},
		{"SERVICE_HOST", "${{service.web.host}}", "plain"},
		{"SERVICE_PORT", "${{service.web.port}}", "plain"},
		{"TOKEN_ALIAS", "${{variable.TOKEN}}", "secret"},
	} {
		result, putErr := fixture.plans.PutVariable(ctx, projectID, environmentID, variable.name, "operator", VariableWriteRequest{
			Revision: revision, Reference: variable.reference, Sensitivity: variable.sensitivity, Scopes: []string{"runtime"},
		})
		if putErr != nil {
			t.Fatalf("put %s: %v", variable.name, putErr)
		}
		revision = result.DesiredRevision
	}
	if _, err := fixture.plans.PutVariable(ctx, projectID, environmentID, "UNSAFE", "operator", VariableWriteRequest{
		Revision: revision, Reference: "${{credential.production-token}}", Sensitivity: "plain", Scopes: []string{"runtime"},
	}); !errors.Is(err, ErrInvalidVariable) {
		t.Fatalf("plain credential reference error = %v", err)
	}

	assertValues := func(label string, values []ScopedVariableValue, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		got := map[string]ScopedVariableValue{}
		for _, value := range values {
			got[value.Name] = value
		}
		want := map[string]string{
			"TOKEN": "credential-value", "DATABASE_URL": "postgres://app:secret@database.internal/app",
			"PUBLIC_URL": "https://app.example.test", "SERVICE_HOST": "web", "SERVICE_PORT": "80",
			"TOKEN_ALIAS": "credential-value",
		}
		for name, expected := range want {
			if got[name].Value != expected {
				t.Errorf("%s %s = %q, want %q", label, name, got[name].Value, expected)
			}
		}
		if got["TOKEN_ALIAS"].Sensitivity != "secret" {
			t.Errorf("%s secret reference leaf was not classified secret", label)
		}
	}
	values, openErr := fixture.plans.OpenScopedVariables(ctx, environmentID, "runtime")
	assertValues("active values", values, openErr)

	runs := NewOrchestrationStore(fixture.store)
	run, _, err := runs.Enqueue(ctx, RunRequest{
		ProjectID: projectID, EnvironmentID: environmentID, Operation: OperationDeploy,
		Trigger: TriggerManual, Actor: "operator", RequestDigest: "feature-owner-references",
		PlanRevision: revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB.Exec(`
		UPDATE deploy_dependencies
		   SET config_json = '{"hostname":"app.example.test","https":false}'
		 WHERE environment_id = ? AND kind = 'domain'`, environmentID); err != nil {
		t.Fatal(err)
	}
	values, openErr = fixture.plans.OpenRunScopedVariables(ctx, run.ID, environmentID, "runtime")
	assertValues("run values", values, openErr)

	missing := "${{credential.missing}}"
	if _, err := fixture.plans.PutVariable(ctx, projectID, environmentID, "MISSING", "operator", VariableWriteRequest{
		Revision: revision, Reference: missing, Sensitivity: "secret", Scopes: []string{"runtime"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.plans.OpenScopedVariables(ctx, environmentID, "runtime"); !errors.Is(err, ErrInvalidVariable) {
		t.Fatalf("missing external reference error = %v", err)
	}
}

func TestVariableMutationAdvancesDesiredWithoutChangingLiveAndFreezesRuns(t *testing.T) {
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	projectID, environmentID := insertConfigurationFixture(t, fixture)
	first := "alpha"
	created, err := fixture.plans.PutVariable(ctx, projectID, environmentID, "TOKEN", "operator", VariableWriteRequest{
		Revision: 1, Value: &first, Sensitivity: "secret", Scopes: []string{"runtime"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.DesiredRevision != 2 || created.GeneratedValue != "" || created.Variable.Masked != "••••••••" || !created.Variable.Pending {
		t.Fatalf("created variable = %#v", created)
	}
	var desired int
	var live int64
	if err := fixture.store.DB.QueryRow(`SELECT desired_revision, live_release_id FROM deploy_environments WHERE id = ?`, environmentID).Scan(&desired, &live); err != nil {
		t.Fatal(err)
	}
	if desired != 2 || live != 0 {
		t.Fatalf("environment desired/live = %d/%d", desired, live)
	}
	for _, table := range []string{"deploy_sources", "deploy_build_plans", "deploy_runtime_plans"} {
		var count int
		if err := fixture.store.DB.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE environment_id = ?`, environmentID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 2 {
			t.Errorf("%s revisions = %d, want 2", table, count)
		}
	}
	if _, err := fixture.plans.PutVariable(ctx, projectID, environmentID, "BROKEN", "operator", VariableWriteRequest{
		Revision: 2, Reference: "${{variable.MISSING}}", Sensitivity: "secret", Scopes: []string{"runtime"},
	}); !errors.Is(err, ErrInvalidVariable) {
		t.Fatalf("missing mutation error = %v", err)
	}
	if err := fixture.store.DB.QueryRow(`SELECT desired_revision FROM deploy_environments WHERE id = ?`, environmentID).Scan(&desired); err != nil || desired != 2 {
		t.Fatalf("failed mutation changed desired revision to %d, error=%v", desired, err)
	}
	generated, err := fixture.plans.RotateVariable(ctx, projectID, environmentID, "TOKEN", "operator", 2)
	if err != nil {
		t.Fatal(err)
	}
	if generated.DesiredRevision != 3 || len(generated.GeneratedValue) < 40 || generated.GeneratedValue == first {
		t.Fatalf("rotated variable = %#v", generated)
	}
	revealed, err := fixture.plans.RevealVariable(ctx, projectID, environmentID, "TOKEN")
	if err != nil || revealed.Value != generated.GeneratedValue {
		t.Fatalf("revealed variable = %#v, error=%v", revealed, err)
	}
	if _, err := fixture.plans.RevealVariable(ctx, projectID, environmentID, "NOT_A_VARIABLE"); !errors.Is(err, ErrVariableNotFound) {
		t.Fatalf("reveal of an unknown variable = %v, want ErrVariableNotFound", err)
	}
	state, err := fixture.plans.PendingState(ctx, projectID, environmentID)
	if err != nil || !state.Pending || state.DesiredRevision != 3 || len(state.Changes) == 0 {
		t.Fatalf("pending state = %#v, error=%v", state, err)
	}
}

func TestSavingEnvironmentConfigurationCreatesDesiredRevisionOnly(t *testing.T) {
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	projectID, environmentID := insertConfigurationFixture(t, fixture)
	before, err := fixture.plans.EnvironmentConfiguration(ctx, projectID, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	after, err := fixture.plans.SaveEnvironmentConfiguration(ctx, projectID, environmentID, ConfigurationWriteRequest{
		Revision: before.Revision,
		Build:    before.Build,
		Runtime: RuntimePlanConfig{
			Image: "alpine:3", InternalPort: 8080, HostPort: 18080,
			BindAddress: "127.0.0.1", Strategy: StrategyStopFirst,
		},
		Dependencies: []PlannedDependency{{
			Kind: "storage", Ownership: OwnershipLinked, ResourceKind: "bind_path", ResourceID: root,
		}},
		Checks: []PlannedCheck{{
			Name: "ready", Kind: "tcp", Phase: "readiness", Required: true,
			Config: json.RawMessage(`{"port":8080}`),
		}},
		Domains: []PlannedDomain{{Hostname: "app.example.test", Ownership: OwnershipManaged}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != 2 || after.Runtime.HostPort != 18080 || len(after.Dependencies) != 1 ||
		len(after.Checks) != 1 || len(after.Domains) != 1 || after.Pending == nil || !after.Pending.Pending {
		t.Fatalf("saved configuration = %#v", after)
	}
	var live int64
	if err := fixture.store.DB.QueryRow(`SELECT live_release_id FROM deploy_environments WHERE id = ?`, environmentID).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 0 {
		t.Fatalf("saving configuration changed live release to %d", live)
	}
	if _, err := fixture.plans.SaveEnvironmentConfiguration(ctx, projectID, environmentID, ConfigurationWriteRequest{
		Revision: 1, Build: after.Build, Runtime: after.Runtime,
	}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale configuration error = %v", err)
	}
}

func TestPendingStateClearsAppliedRevisionButKeepsChangeSavedAfterEnqueue(t *testing.T) {
	ctx := context.Background()
	fixture := newReleaseStoreFixture(t)
	fixture.addPlan(t, 1, strings.Repeat("a", 40))
	first := "first-secret"
	if _, err := fixture.variables.PutVariable(ctx, fixture.projectID, fixture.envID, "TOKEN", "admin", VariableWriteRequest{
		Revision: 1, Value: &first, Sensitivity: "secret", Scopes: []string{"runtime"},
	}); err != nil {
		t.Fatal(err)
	}
	run, lease := fixture.claimedRun(t, 2)
	rotated, err := fixture.variables.RotateVariable(ctx, fixture.projectID, fixture.envID, "TOKEN", "admin", 2)
	if err != nil {
		t.Fatal(err)
	}
	release := fixture.candidate(t, *run, lease, fakeContentDigest("applied-revision-two"))
	fixture.finishCandidate(t, release.Release.ID, run.ID, lease.Token)

	state, err := fixture.variables.PendingState(ctx, fixture.projectID, fixture.envID)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Pending || state.LivePlanRevision != 2 || state.DesiredRevision != 3 || len(state.Changes) != 1 ||
		state.Changes[0].Kind != "variable" || state.Changes[0].Name != "TOKEN" || state.Changes[0].Change != "changed" {
		t.Fatalf("pending state after older queued revision activated = %#v", state)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), first) || strings.Contains(string(raw), rotated.GeneratedValue) {
		t.Fatalf("pending diff leaked a secret: %s", raw)
	}
}

func insertConfigurationFixture(t *testing.T, fixture *planningStoreFixture) (int64, int64) {
	t.Helper()
	return insertConfigurationFixtureWithEvidence(t, fixture, `{}`)
}

func insertConfigurationFixtureWithEvidence(
	t *testing.T,
	fixture *planningStoreFixture,
	evidence string,
) (int64, int64) {
	t.Helper()
	now := fixture.now.Unix()
	project, err := fixture.store.DB.Exec(`
		INSERT INTO deploy_projects(name, profile, repo_path, branch, compose_file, hook_secret, hook_id, enabled, created_at, updated_at)
		VALUES('config-app', 'worker', '/srv/config-app', 'main', 'compose.yml', '', 'config-hook', 1, ?, ?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := project.LastInsertId()
	environment, err := fixture.store.DB.Exec(`
		INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, strategy, expected_downtime, protected, created_at, updated_at)
		VALUES(?, 'production', 'production', 'production', 1, 'stop_first', 1, 1, ?, ?)`, projectID, now, now)
	if err != nil {
		t.Fatal(err)
	}
	environmentID, _ := environment.LastInsertId()
	if _, err := fixture.store.DB.Exec(`
		INSERT INTO deploy_sources(environment_id, revision, kind, config_json, identity_json, digest, created_at)
		VALUES(?, 1, 'git', '{}', '{}', ?, ?)`, environmentID, fakeContentDigest("source"), now); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB.Exec(`
		INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at)
		VALUES(?, 1, 'none', '{"method":"none"}', ?, '{}', ?, ?)`, environmentID, evidence, fakeContentDigest("build"), now); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB.Exec(`
		INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at)
		VALUES(?, 1, '{"strategy":"stop_first"}', '{}', ?, ?)`, environmentID, fakeContentDigest("runtime"), now); err != nil {
		t.Fatal(err)
	}
	return projectID, environmentID
}
