package deploy

import (
	"context"
	"slices"
	"testing"
)

// Every value a diagnosis has to mask is opened from the dashboard's own
// store: secret variables, plain ones that look like keys, what references
// point at, every credential, the trigger's secret and a carried-over
// project's old variables — and an ordinary setting is not.
func TestDiagnosisSecretsAreReadFromTheStoreAlone(t *testing.T) {
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	projectID, environmentID := insertConfigurationFixture(t, fixture)
	seal := func(value string) string {
		sealed, err := fixture.sealer.Seal(value)
		if err != nil {
			t.Fatal(err)
		}
		return sealed
	}
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO deploy_credentials(name, kind, config_json, secret_enc, created_at, updated_at) VALUES('production-token', 'token', '{}', ?, 0, 0)`, []any{seal("credential-secret-31")}},
		{`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES('primary', 'postgres', ?, 0)`, []any{seal("postgres://app:db-password-77@db:5432/app")}},
		{`INSERT INTO deploy_triggers(environment_id, name, kind, config_json, secret_enc, hook_id, enabled, created_at, updated_at) VALUES(?, 'push', 'webhook', '{}', ?, 'hook-1', 1, 0, 0)`, []any{environmentID, seal("trigger-hmac-secret")}},
		{`INSERT INTO deploy_env(project_id, key, value_enc, updated_at) VALUES(?, 'OLD_KEY', ?, 0)`, []any{projectID, seal("legacy-value-12")}},
	} {
		if _, err := fixture.store.DB.Exec(statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	revision := 1
	literal := func(value string) *string { return &value }
	for _, variable := range []struct {
		name    string
		request VariableWriteRequest
	}{
		{"API_SECRET", VariableWriteRequest{Value: literal("secret-literal-1"), Sensitivity: "secret"}},
		{"STRIPE_PUBLIC", VariableWriteRequest{Value: literal("pk_live_51HqLyjWDarjtT1zdp7dc"), Sensitivity: "plain"}},
		{"NODE_ENV", VariableWriteRequest{Value: literal("production"), Sensitivity: "plain"}},
		{"TOKEN", VariableWriteRequest{Reference: "${{credential.production-token}}", Sensitivity: "secret"}},
		{"DATABASE_URL", VariableWriteRequest{Reference: "${{database.primary.url}}", Sensitivity: "secret"}},
	} {
		request := variable.request
		request.Revision, request.Scopes = revision, []string{"runtime"}
		result, err := fixture.plans.PutVariable(ctx, projectID, environmentID, variable.name, "operator", request)
		if err != nil {
			t.Fatalf("put %s: %v", variable.name, err)
		}
		revision = result.DesiredRevision
	}

	values, err := fixture.plans.DiagnosisSecrets(ctx, EngineRun{ProjectID: projectID, EnvironmentID: environmentID})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"secret-literal-1", "pk_live_51HqLyjWDarjtT1zdp7dc", "credential-secret-31",
		"postgres://app:db-password-77@db:5432/app", "db-password-77", "trigger-hmac-secret", "legacy-value-12",
	} {
		if !slices.Contains(values, want) {
			t.Errorf("%q is not masked; masked %q", want, values)
		}
	}
	if slices.Contains(values, "production") {
		t.Error("an ordinary setting is masked")
	}

	// A variable this key cannot open fails the read rather than going out
	// unmasked.
	if _, err := fixture.store.DB.Exec(`INSERT INTO deploy_variable_revisions(environment_id, key, revision, sensitivity, scopes, value_enc, active, created_at)
		VALUES(?, 'BROKEN', 99, 'secret', 'runtime', 'not-sealed', 1, 0)`, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.plans.DiagnosisSecrets(ctx, EngineRun{ProjectID: projectID, EnvironmentID: environmentID}); err == nil {
		t.Error("an unreadable variable did not stop the diagnosis")
	}
}
