package deploy

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/secretstore"
	"github.com/Wayy01/Just-Dashboard/backend/internal/secretstore/secretstoretest"
)

// AWS Secrets Manager and Azure Key Vault have no server to run in a
// container, so this is the live fixture's shape without Docker: the real
// connections and clients against httptest servers that follow the two
// services' documented APIs (the AWS one checking every signature), read by
// a run's first step into the variables its runtime scope opens — a field of
// a JSON secret, a whole secret, a staging label, a pinned Key Vault version
// — with the versions in the transcript and none of the values; then one
// store gone and the next run's first step failing in its words.
func TestSecretsManagerAndKeyVaultReferencesReachARunsVariables(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	aws := &secretstoretest.AWS{
		Region: "eu-west-1", AccessKeyID: "AKIAFIXTUREKEY", SecretAccessKey: "fixture-secret-access-key",
		Secrets: map[string][]secretstoretest.AWSVersion{
			"apps/web": {
				{ID: "11111111-1111-4111-8111-111111111111", Stages: []string{"AWSPREVIOUS"}, String: `{"DATABASE_URL":"postgres://app:aws-v1@db/app"}`},
				{ID: "22222222-2222-4222-8222-222222222222", Stages: []string{"AWSCURRENT"}, String: `{"DATABASE_URL":"postgres://app:aws-v2@db/app"}`},
			},
			"stripe-key": {{ID: "33333333-3333-4333-8333-333333333333", Stages: []string{"AWSCURRENT"}, String: "sk_live_fixture"}},
		},
	}
	awsServer := httptest.NewServer(aws)
	defer awsServer.Close()
	azure := &secretstoretest.Azure{
		TenantID: "0a1b2c3d-0000-4000-8000-000000000001", ClientID: "9f8e7d6c-0000-4000-8000-000000000002", ClientSecret: "fixture-client-secret",
		Secrets: map[string][]secretstoretest.AzureVersion{
			"session-secret": {{ID: "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1", Value: "kv-session-v1"}, {ID: "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2", Value: "kv-session-v2"}},
			"app-config":     {{ID: "c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3", Value: `{"API_KEY":"kv-api-key"}`}},
		},
	}
	azureServer := httptest.NewServer(azure)
	defer azureServer.Close()

	fixture := newPlanningStoreFixture(t)
	repo := secretstore.NewRepository(fixture.store, fixture.sealer)
	plans := fixture.plans.WithSecretStores(repo)
	if _, err := repo.Create(ctx, secretstore.Write{
		Name: "aws", Kind: secretstore.KindAWS, Address: awsServer.URL, Settings: secretstore.Settings{Region: "eu-west-1"},
		Credentials: &map[string]string{"accessKeyId": aws.AccessKeyID, "secretAccessKey": aws.SecretAccessKey},
	}, "operator"); err != nil {
		t.Fatal(err)
	}
	kv, err := repo.Create(ctx, secretstore.Write{
		Name: "kv", Kind: secretstore.KindAzure, Address: azureServer.URL,
		Settings:    secretstore.Settings{VaultName: "jdtest", TenantID: azure.TenantID, Authority: azureServer.URL},
		Credentials: &map[string]string{"clientId": azure.ClientID, "clientSecret": azure.ClientSecret},
	}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	tested, err := repo.Test(ctx, kv.ID, &secretstore.Probe{Path: "session-secret"})
	if err != nil || !tested.OK || !strings.HasSuffix(tested.Message, "found session-secret, version b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2") {
		t.Fatalf("Key Vault test = %+v, %v", tested, err)
	}

	projectID, environmentID := insertConfigurationFixture(t, fixture)
	revision := 1
	for _, variable := range []struct{ name, reference string }{
		{"DATABASE_URL", "${{store.aws/apps/web#DATABASE_URL}}"},
		{"FIRST_DATABASE_URL", "${{store.aws/apps/web#DATABASE_URL@AWSPREVIOUS}}"},
		{"STRIPE_KEY", "${{store.aws/stripe-key}}"},
		{"SESSION_SECRET", "${{store.kv/session-secret}}"},
		{"FIRST_SESSION_SECRET", "${{store.kv/session-secret@a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1}}"},
		{"API_KEY", "${{store.kv/app-config#API_KEY}}"},
	} {
		result, err := plans.PutVariable(ctx, projectID, environmentID, variable.name, "operator", VariableWriteRequest{
			Revision: revision, Reference: variable.reference, Sensitivity: "secret", Scopes: []string{"runtime"},
		})
		if err != nil {
			t.Fatalf("%s: %v", variable.name, err)
		}
		revision = result.DesiredRevision
	}
	// A reference the store cannot answer is refused when saved.
	for reference, want := range map[string]string{
		"${{store.aws#DATABASE_URL}}":          "names the secret",
		"${{store.kv/session-secret@3}}":       "32 hexadecimal",
		"${{store.kv/a/b}}":                    "letters, digits and dashes",
		"${{store.aws/apps/web#DATABASE_URL}}": "must be secret",
	} {
		sensitivity := "secret"
		if want == "must be secret" {
			sensitivity = "plain"
		}
		_, err := plans.PutVariable(ctx, projectID, environmentID, "REFUSED", "operator", VariableWriteRequest{
			Revision: revision, Reference: reference, Sensitivity: sensitivity, Scopes: []string{"runtime"},
		})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s = %v; want %q", reference, err, want)
		}
	}

	runs := NewOrchestrationStore(fixture.store)
	executor := &NormalizedStepExecutor{store: runs, variables: plans}
	enqueue := func(label string) *EngineRun {
		t.Helper()
		queued, _, err := runs.Enqueue(ctx, RunRequest{
			ProjectID: projectID, EnvironmentID: environmentID, Operation: OperationDeploy,
			Trigger: TriggerManual, Actor: "operator", RequestDigest: "cloud-stores-" + label, PlanRevision: revision,
		})
		if err != nil {
			t.Fatal(err)
		}
		return queued
	}
	first := enqueue("first")
	output := &recordingStepOutput{}
	if failure := executor.readSecretStores(ctx, StepExecution{Run: *first, Step: RunStep{Key: StepResolveSource}, Output: output}); failure != nil {
		t.Fatalf("the first step's read failed: %+v\n%s", failure, output.joined())
	}
	values, err := plans.OpenRunScopedVariables(ctx, first.ID, environmentID, "runtime")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, value := range values {
		got[value.Name] = value.Value
	}
	for name, want := range map[string]string{
		"DATABASE_URL": "postgres://app:aws-v2@db/app", "FIRST_DATABASE_URL": "postgres://app:aws-v1@db/app", "STRIPE_KEY": "sk_live_fixture",
		"SESSION_SECRET": "kv-session-v2", "FIRST_SESSION_SECRET": "kv-session-v1", "API_KEY": "kv-api-key",
	} {
		if got[name] != want {
			t.Fatalf("%s = %q; want %q (all %v)", name, got[name], want, got)
		}
	}
	transcript := output.joined()
	for _, want := range []string{
		"Read DATABASE_URL from secret store aws: apps/web#DATABASE_URL, version 22222222-2222-4222-8222-222222222222",
		"Read FIRST_DATABASE_URL from secret store aws: apps/web#DATABASE_URL@AWSPREVIOUS, version 11111111-1111-4111-8111-111111111111",
		"Read STRIPE_KEY from secret store aws: stripe-key, version 33333333-3333-4333-8333-333333333333",
		"Read SESSION_SECRET from secret store kv: session-secret, version b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2",
		"Read FIRST_SESSION_SECRET from secret store kv: session-secret@a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1, version a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1",
		"Read API_KEY from secret store kv: app-config#API_KEY, version c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3",
	} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("the transcript\n%s\nlacks %q", transcript, want)
		}
	}
	for _, value := range []string{"aws-v1", "aws-v2", "sk_live", "kv-session", "kv-api-key", aws.SecretAccessKey, azure.ClientSecret} {
		if strings.Contains(transcript, value) {
			t.Fatalf("the transcript holds a value or a credential:\n%s", transcript)
		}
	}
	// One Secrets Manager read per secret and pinned version, one Key Vault
	// token for the run.
	if requests := strings.Join(aws.Requests(), ","); requests != "GetSecretValue apps/web,GetSecretValue apps/web,GetSecretValue stripe-key" {
		t.Fatalf("Secrets Manager requests = %s", requests)
	}
	if azure.Tokens() != 1 {
		t.Fatalf("Key Vault tokens = %d", azure.Tokens())
	}
	plans.SecretStoreRunObserver().RunFinished(ctx, *first)

	// Secrets Manager stops answering: the next run's first step fails in the
	// store's words with nothing read, and the store's card says so.
	awsServer.Close()
	second := enqueue("second")
	output = &recordingStepOutput{}
	failure := executor.readSecretStores(ctx, StepExecution{Run: *second, Step: RunStep{Key: StepResolveSource}, Output: output})
	if failure == nil || failure.State != StepFailed || failure.ErrorCode != "secret_store_unreadable" ||
		!strings.Contains(failure.ErrorMessage, "secret store aws, apps/web#DATABASE_URL: could not reach "+awsServer.URL) {
		t.Fatalf("with the store down = %+v\n%s", failure, output.joined())
	}
	stored, err := repo.GetByName(ctx, "aws")
	if err != nil || stored.LastRead == nil || stored.LastRead.OK || !strings.Contains(stored.LastRead.Message, "could not reach") {
		t.Fatalf("the store's last read = %+v, %v", stored, err)
	}
}
