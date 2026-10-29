package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/secretstore"
)

// The images the live fixture runs, pinned: OpenBao's dev server (KV
// version 2 at secret/, AppRole enabled by the fixture) and a self-hosted
// Infisical with the PostgreSQL and Redis it needs.
const (
	liveOpenBaoImage   = "openbao/openbao:2.7.2"
	liveInfisicalImage = "infisical/infisical:v0.167.4"
	livePostgresImage  = "postgres:17-alpine"
	liveRedisImage     = "redis:7-alpine"
)

// TestLiveSecretStoreReferencesReachTheContainer reads a secret from a real
// OpenBao through AppRole and from a real self-hosted Infisical through a
// Universal Auth machine identity, the way a run does — the first step's
// read, then the runtime variables activation opens — starts a real
// container with them and reads them back from inside it. A version pinned
// in a reference reads that version. Then OpenBao is stopped, and the next
// run's first step fails in the store's own words with nothing started.
// Opt-in: it pulls nothing that is not already pulled, but starts five
// containers, builds an image, and removes all of it.
func TestLiveSecretStoreReferencesReachTheContainer(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker/Buildx host")
	}
	ctx := context.Background()
	client := liveC4Docker(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	network := "jdtest-stores-" + stamp
	if _, err := liveDockerOutput(ctx, "network", "create", network); err != nil {
		t.Fatal(err)
	}
	containers := []string{}
	t.Cleanup(func() {
		for _, name := range containers {
			_, _ = liveDockerOutput(context.Background(), "rm", "--force", "--volumes", name)
		}
		_, _ = liveDockerOutput(context.Background(), "network", "rm", network)
	})
	run := func(name string, args ...string) {
		t.Helper()
		containers = append(containers, name)
		if _, err := liveDockerOutput(ctx, append([]string{"run", "-d", "--name", name, "--network", network}, args...)...); err != nil {
			t.Fatal(err)
		}
	}

	baoPort := freeLoopbackPort(t)
	bao := "jdtest-openbao-" + stamp
	run(bao, "-p", fmt.Sprintf("127.0.0.1:%d:8200", baoPort), "-e", "BAO_DEV_ROOT_TOKEN_ID=jd-root",
		"-e", "BAO_DEV_LISTEN_ADDRESS=0.0.0.0:8200", liveOpenBaoImage, "server", "-dev")
	pg := "jdtest-infisical-pg-" + stamp
	run(pg, "-e", "POSTGRES_USER=infisical", "-e", "POSTGRES_PASSWORD=infisical", "-e", "POSTGRES_DB=infisical", livePostgresImage)
	redis := "jdtest-infisical-redis-" + stamp
	run(redis, liveRedisImage)
	infisicalPort := freeLoopbackPort(t)
	infisical := "jdtest-infisical-" + stamp
	run(infisical, "-p", fmt.Sprintf("127.0.0.1:%d:8080", infisicalPort),
		"-e", "ENCRYPTION_KEY=6c1fe4e407b8911c104518103505b218", "-e", "AUTH_SECRET=5lrMXKKWCVocS/uerPsl7V+TX/aaUaI7iDkgl3tSmLE=",
		"-e", "DB_CONNECTION_URI=postgres://infisical:infisical@"+pg+":5432/infisical", "-e", "REDIS_URL=redis://"+redis+":6379",
		"-e", fmt.Sprintf("SITE_URL=http://127.0.0.1:%d", infisicalPort), "-e", "TELEMETRY_ENABLED=false", liveInfisicalImage)

	baoURL := fmt.Sprintf("http://127.0.0.1:%d", baoPort)
	roleID, secretID := openBaoFixture(t, baoURL)
	infisicalURL := fmt.Sprintf("http://127.0.0.1:%d", infisicalPort)
	projectID, clientID, clientSecret := infisicalFixture(t, infisicalURL)

	fixture := newPlanningStoreFixture(t)
	repo := secretstore.NewRepository(fixture.store, fixture.sealer)
	plans := fixture.plans.WithSecretStores(repo)
	if _, err := repo.Create(ctx, secretstore.Write{
		Name: "bao", Kind: secretstore.KindOpenBao, Address: baoURL, Settings: secretstore.Settings{Auth: secretstore.AuthAppRole},
		Credentials: &map[string]string{"roleId": roleID, "secretId": secretID},
	}, "operator"); err != nil {
		t.Fatal(err)
	}
	infisicalStore, err := repo.Create(ctx, secretstore.Write{
		Name: "infisical", Kind: secretstore.KindInfisical, Address: infisicalURL,
		Settings:    secretstore.Settings{Project: projectID, Environment: "prod"},
		Credentials: &map[string]string{"clientId": clientID, "clientSecret": clientSecret},
	}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	tested, err := repo.Test(ctx, infisicalStore.ID, &secretstore.Probe{Path: "backend", Key: "SESSION_SECRET"})
	if err != nil || !tested.OK {
		t.Fatalf("Infisical test = %+v, %v", tested, err)
	}
	t.Logf("Infisical test: %s", tested.Message)

	projectRow, environmentID := insertConfigurationFixture(t, fixture)
	revision := 1
	for _, variable := range []struct{ name, reference string }{
		{"DATABASE_URL", "${{store.bao/apps/web#DATABASE_URL}}"},
		{"FIRST_DATABASE_URL", "${{store.bao/apps/web#DATABASE_URL@1}}"},
		{"API_KEY", "${{store.infisical#API_KEY}}"},
		{"SESSION_SECRET", "${{store.infisical/backend#SESSION_SECRET}}"},
	} {
		result, err := plans.PutVariable(ctx, projectRow, environmentID, variable.name, "operator", VariableWriteRequest{
			Revision: revision, Reference: variable.reference, Sensitivity: "secret", Scopes: []string{"runtime"},
		})
		if err != nil {
			t.Fatal(err)
		}
		revision = result.DesiredRevision
	}

	builder := NewArtifactBuilder(NewDockerArtifactBackend(client))
	owner := NewDockerRuntimeOwner(client)
	tag := "jdtest-stores:" + stamp
	t.Cleanup(func() { _, _ = liveDockerOutput(context.Background(), "image", "rm", "--force", tag) })
	root := t.TempDir()
	writeBuildFixture(t, root, "Dockerfile", "FROM alpine:3.22\nCMD [\"sleep\", \"600\"]\n")
	image := livePrepareAndBuild(t, builder, root, tag, BuildPlanConfig{Method: BuildDockerfile}, nil, nil, nil).Image

	runs := NewOrchestrationStore(fixture.store)
	executor := &NormalizedStepExecutor{store: runs, variables: plans}
	enqueue := func(label string) *EngineRun {
		t.Helper()
		queued, _, err := runs.Enqueue(ctx, RunRequest{
			ProjectID: projectRow, EnvironmentID: environmentID, Operation: OperationDeploy,
			Trigger: TriggerManual, Actor: "operator", RequestDigest: "stores-live-" + label, PlanRevision: revision,
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
	t.Logf("first step's transcript:\n%s", output.joined())
	values, err := plans.OpenRunScopedVariables(ctx, first.ID, environmentID, "runtime")
	if err != nil {
		t.Fatal(err)
	}
	variables := map[string]string{}
	for _, value := range values {
		variables[value.Name] = value.Value
	}
	containerEnvironment := time.Now().UnixNano()%1_000_000_000 + 800_000_000
	plan := RuntimePlanConfig{
		Strategy: StrategyStopFirst, StopSignal: "SIGTERM", GracePeriodSeconds: 1,
		Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
	}
	engineRun := EngineRun{ID: containerEnvironment + 1, EnvironmentID: containerEnvironment}
	release := Release{ID: containerEnvironment + 101, EnvironmentID: containerEnvironment, RunID: engineRun.ID, Number: 1}
	started, err := owner.StartCandidate(ctx, CandidateRuntimeRequest{
		Run: engineRun, Release: release, Snapshot: runtimeReleaseSnapshot{Version: 1, Plan: plan, Image: image},
		RuntimeVariables: variables, Host: "127.0.0.1",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime := runtimeFromInput(started.Input)
	defer func() { _, _ = owner.Stop(context.Background(), runtime, plan, nil, true, nil) }()
	inside, err := liveDockerOutput(ctx, "exec", runtime.RuntimeID, "sh", "-c",
		`printf 'DATABASE_URL=%s\nFIRST_DATABASE_URL=%s\nAPI_KEY=%s\nSESSION_SECRET=%s\n' "$DATABASE_URL" "$FIRST_DATABASE_URL" "$API_KEY" "$SESSION_SECRET"`)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("container %s read:\n%s", runtime.RuntimeID[:12], inside)
	for _, want := range []string{
		"DATABASE_URL=postgres://app:bao-v2@db/app\n", "FIRST_DATABASE_URL=postgres://app:bao-v1@db/app\n",
		"API_KEY=infisical-api-key\n", "SESSION_SECRET=infisical-session-v2\n",
	} {
		if !strings.Contains(string(inside), want) {
			t.Fatalf("the container read\n%s\nwithout %q", inside, want)
		}
	}
	for _, want := range []string{
		"Read DATABASE_URL from secret store bao: apps/web#DATABASE_URL, version 2",
		"Read FIRST_DATABASE_URL from secret store bao: apps/web#DATABASE_URL@1, version 1",
		"Read API_KEY from secret store infisical: API_KEY, version 1",
		"Read SESSION_SECRET from secret store infisical: backend#SESSION_SECRET, version 2",
	} {
		if !strings.Contains(output.joined(), want) {
			t.Fatalf("the transcript\n%s\nlacks %q", output.joined(), want)
		}
	}
	for _, value := range []string{"bao-v2", "bao-v1", "infisical-api-key", "infisical-session"} {
		if strings.Contains(output.joined(), value) {
			t.Fatalf("the transcript holds a value:\n%s", output.joined())
		}
	}
	if _, err := fixture.store.DB.Exec(`UPDATE deploy_runs SET status = 'success', state = 'succeeded', ended_at = 1 WHERE id = ?`, first.ID); err != nil {
		t.Fatal(err)
	}
	plans.SecretStoreRunObserver().RunFinished(ctx, *first)

	// The store goes down: the next run's first step fails in its words, and
	// nothing is started.
	if _, err := liveDockerOutput(ctx, "stop", "--time", "1", bao); err != nil {
		t.Fatal(err)
	}
	second := enqueue("second")
	output = &recordingStepOutput{}
	failure := executor.readSecretStores(ctx, StepExecution{Run: *second, Step: RunStep{Key: StepResolveSource}, Output: output})
	if failure == nil || failure.State != StepFailed || failure.ErrorCode != "secret_store_unreadable" ||
		!strings.Contains(failure.ErrorMessage, "secret store bao: AppRole sign-in: could not reach "+baoURL) {
		t.Fatalf("with the store down = %+v\n%s", failure, output.joined())
	}
	t.Logf("with OpenBao stopped, the first step failed: %s: %s", failure.ErrorCode, failure.ErrorMessage)
	stored, err := repo.GetByName(ctx, "bao")
	if err != nil || stored.LastRead == nil || stored.LastRead.OK {
		t.Fatalf("the store's last read = %+v, %v", stored, err)
	}
}

func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// liveCall sends one JSON request and decodes the answer, failing the test on
// any status but the one wanted.
func liveCall(t *testing.T, method, address string, headers map[string]string, body any, want int, into any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, address, reader)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != want {
		t.Fatalf("%s %s = %d %s", method, address, response.StatusCode, raw)
	}
	if into != nil {
		if err := json.Unmarshal(raw, into); err != nil {
			t.Fatalf("%s %s: %v: %s", method, address, err, raw)
		}
	}
}

func waitForHTTP(t *testing.T, address string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		response, err := http.Get(address)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not answer within %s: %v", address, timeout, err)
		}
		time.Sleep(time.Second)
	}
}

// openBaoFixture writes two versions of secret/apps/web with the dev
// server's root token, and makes an AppRole whose policy reads only that
// path: the role ID and a secret ID are what the connection signs in with.
func openBaoFixture(t *testing.T, address string) (string, string) {
	t.Helper()
	waitForHTTP(t, address+"/v1/sys/health", 60*time.Second)
	root := map[string]string{"X-Vault-Token": "jd-root"}
	for _, value := range []string{"postgres://app:bao-v1@db/app", "postgres://app:bao-v2@db/app"} {
		liveCall(t, http.MethodPost, address+"/v1/secret/data/apps/web", root, map[string]any{"data": map[string]string{"DATABASE_URL": value}}, http.StatusOK, nil)
	}
	liveCall(t, http.MethodPost, address+"/v1/sys/auth/approle", root, map[string]string{"type": "approle"}, http.StatusNoContent, nil)
	liveCall(t, http.MethodPut, address+"/v1/sys/policies/acl/web", root,
		map[string]string{"policy": `path "secret/data/apps/web" { capabilities = ["read"] }`}, http.StatusNoContent, nil)
	liveCall(t, http.MethodPost, address+"/v1/auth/approle/role/just-dashboard", root,
		map[string]any{"token_policies": []string{"web"}, "token_ttl": "10m"}, http.StatusNoContent, nil)
	var role struct {
		Data struct {
			RoleID string `json:"role_id"`
		} `json:"data"`
	}
	liveCall(t, http.MethodGet, address+"/v1/auth/approle/role/just-dashboard/role-id", root, nil, http.StatusOK, &role)
	var secret struct {
		Data struct {
			SecretID string `json:"secret_id"`
		} `json:"data"`
	}
	liveCall(t, http.MethodPost, address+"/v1/auth/approle/role/just-dashboard/secret-id", root, map[string]any{}, http.StatusOK, &secret)
	return role.Data.RoleID, secret.Data.SecretID
}

// infisicalFixture bootstraps the instance, makes a project with a secret at
// / and a twice-written one in /backend, and a machine identity with
// Universal Auth that may view the project.
func infisicalFixture(t *testing.T, address string) (string, string, string) {
	t.Helper()
	waitForHTTP(t, address+"/api/status", 4*time.Minute)
	var boot struct {
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
		Identity struct {
			Credentials struct {
				Token string `json:"token"`
			} `json:"credentials"`
		} `json:"identity"`
	}
	liveCall(t, http.MethodPost, address+"/api/v1/admin/bootstrap", nil,
		map[string]string{"email": "admin@jd.test", "password": "Jd-test-password-1!", "organization": "jdtest"}, http.StatusOK, &boot)
	admin := map[string]string{"Authorization": "Bearer " + boot.Identity.Credentials.Token}
	var project struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	liveCall(t, http.MethodPost, address+"/api/v1/projects", admin,
		map[string]string{"projectName": "webapp", "slug": "webapp", "type": "secret-manager"}, http.StatusOK, &project)
	id := project.Project.ID
	liveCall(t, http.MethodPost, address+"/api/v1/folders", admin,
		map[string]string{"workspaceId": id, "environment": "prod", "name": "backend", "path": "/"}, http.StatusOK, nil)
	secret := func(method, path, name, value string) {
		liveCall(t, method, address+"/api/v3/secrets/raw/"+name, admin,
			map[string]string{"workspaceId": id, "environment": "prod", "secretPath": path, "secretValue": value, "type": "shared"}, http.StatusOK, nil)
	}
	secret(http.MethodPost, "/", "API_KEY", "infisical-api-key")
	secret(http.MethodPost, "/backend", "SESSION_SECRET", "infisical-session-v1")
	secret(http.MethodPatch, "/backend", "SESSION_SECRET", "infisical-session-v2")
	var identity struct {
		Identity struct {
			ID string `json:"id"`
		} `json:"identity"`
	}
	liveCall(t, http.MethodPost, address+"/api/v1/identities", admin,
		map[string]string{"name": "just-dashboard", "organizationId": boot.Organization.ID, "role": "no-access"}, http.StatusOK, &identity)
	var universal struct {
		IdentityUniversalAuth struct {
			ClientID string `json:"clientId"`
		} `json:"identityUniversalAuth"`
	}
	liveCall(t, http.MethodPost, address+"/api/v1/auth/universal-auth/identities/"+identity.Identity.ID, admin, map[string]any{}, http.StatusOK, &universal)
	var clientSecret struct {
		ClientSecret string `json:"clientSecret"`
	}
	liveCall(t, http.MethodPost, address+"/api/v1/auth/universal-auth/identities/"+identity.Identity.ID+"/client-secrets", admin,
		map[string]string{"description": "just-dashboard"}, http.StatusOK, &clientSecret)
	liveCall(t, http.MethodPost, address+"/api/v2/workspace/"+id+"/identity-memberships/"+identity.Identity.ID, admin,
		map[string]string{"role": "viewer"}, http.StatusOK, nil)
	return id, universal.IdentityUniversalAuth.ClientID, clientSecret.ClientSecret
}

// liveLocalStackImage stands in for AWS Secrets Manager: LocalStack serves
// its JSON 1.1 protocol with versions and staging labels, but accepts any
// signature, so the signing itself is proved by the documented vectors in
// secretstore_test.go and secretstoretest's verifying fake, not here. 4.14.0
// is the last community release; the 2026 images quit without a licence
// token.
const liveLocalStackImage = "localstack/localstack:4.14.0"

// liveSecretsManagerCall sends one Secrets Manager operation to a stand-in
// that reads the key and region out of the Authorization header without
// checking the signature, which is how the fixture writes its secrets
// without an AWS CLI in the container.
func liveSecretsManagerCall(t *testing.T, endpoint, operation string, body any) {
	t.Helper()
	liveCall(t, http.MethodPost, endpoint+"/", map[string]string{
		"Content-Type": "application/x-amz-json-1.1", "X-Amz-Target": "secretsmanager." + operation,
		"X-Amz-Date":    time.Now().UTC().Format("20060102T150405Z"),
		"Authorization": "AWS4-HMAC-SHA256 Credential=test/" + time.Now().UTC().Format("20060102") + "/us-east-1/secretsmanager/aws4_request, SignedHeaders=host;x-amz-date, Signature=0",
	}, body, http.StatusOK, nil)
}

// TestLiveSecretsManagerReferenceReachesTheContainer reads a JSON secret's
// field, its previous staging label and a whole plain secret from a real
// LocalStack Secrets Manager through the dashboard's own SigV4 client, the
// way a run does — the first step's read, then the runtime variables
// activation opens — starts a real container with them and reads them back.
// Then LocalStack is stopped, and the next run's first step fails in the
// store's words with nothing started. Opt-in; it removes what it made.
func TestLiveSecretsManagerReferenceReachesTheContainer(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker/Buildx host")
	}
	ctx := context.Background()
	client := liveC4Docker(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	name := "jdtest-localstack-" + stamp
	port := freeLoopbackPort(t)
	if _, err := liveDockerOutput(ctx, "run", "-d", "--name", name, "-p", fmt.Sprintf("127.0.0.1:%d:4566", port),
		"-e", "SERVICES=secretsmanager", "-e", "EAGER_SERVICE_LOADING=1", liveLocalStackImage); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = liveDockerOutput(context.Background(), "rm", "--force", "--volumes", name) })
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitForHTTP(t, endpoint+"/_localstack/health", 3*time.Minute)
	// A ClientRequestToken is what the CLI adds for you: the new version's
	// id, which has to be unique per version.
	liveSecretsManagerCall(t, endpoint, "CreateSecret", map[string]string{"Name": "apps/web", "ClientRequestToken": "a1111111-1111-4111-8111-111111111111",
		"SecretString": `{"DATABASE_URL":"postgres://app:aws-v1@db/app","PORT":5432}`})
	liveSecretsManagerCall(t, endpoint, "PutSecretValue", map[string]string{"SecretId": "apps/web", "ClientRequestToken": "a2222222-2222-4222-8222-222222222222",
		"SecretString": `{"DATABASE_URL":"postgres://app:aws-v2@db/app","PORT":5432}`})
	liveSecretsManagerCall(t, endpoint, "CreateSecret", map[string]string{"Name": "stripe-key", "ClientRequestToken": "a3333333-3333-4333-8333-333333333333",
		"SecretString": "sk_live_localstack"})

	fixture := newPlanningStoreFixture(t)
	repo := secretstore.NewRepository(fixture.store, fixture.sealer)
	plans := fixture.plans.WithSecretStores(repo)
	aws, err := repo.Create(ctx, secretstore.Write{
		Name: "aws", Kind: secretstore.KindAWS, Address: endpoint, Settings: secretstore.Settings{Region: "us-east-1"},
		Credentials: &map[string]string{"accessKeyId": "test", "secretAccessKey": "test"},
	}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	tested, err := repo.Test(ctx, aws.ID, &secretstore.Probe{Path: "apps/web", Key: "DATABASE_URL"})
	if err != nil || !tested.OK || !strings.Contains(tested.Message, "found apps/web#DATABASE_URL, version ") {
		t.Fatalf("Secrets Manager test = %+v, %v", tested, err)
	}
	t.Logf("Secrets Manager test: %s", tested.Message)

	projectRow, environmentID := insertConfigurationFixture(t, fixture)
	revision := 1
	for _, variable := range []struct{ name, reference string }{
		{"DATABASE_URL", "${{store.aws/apps/web#DATABASE_URL}}"},
		{"FIRST_DATABASE_URL", "${{store.aws/apps/web#DATABASE_URL@AWSPREVIOUS}}"},
		{"STRIPE_KEY", "${{store.aws/stripe-key}}"},
	} {
		result, err := plans.PutVariable(ctx, projectRow, environmentID, variable.name, "operator", VariableWriteRequest{
			Revision: revision, Reference: variable.reference, Sensitivity: "secret", Scopes: []string{"runtime"},
		})
		if err != nil {
			t.Fatal(err)
		}
		revision = result.DesiredRevision
	}

	builder := NewArtifactBuilder(NewDockerArtifactBackend(client))
	owner := NewDockerRuntimeOwner(client)
	tag := "jdtest-aws-stores:" + stamp
	t.Cleanup(func() { _, _ = liveDockerOutput(context.Background(), "image", "rm", "--force", tag) })
	root := t.TempDir()
	writeBuildFixture(t, root, "Dockerfile", "FROM alpine:3.22\nCMD [\"sleep\", \"600\"]\n")
	image := livePrepareAndBuild(t, builder, root, tag, BuildPlanConfig{Method: BuildDockerfile}, nil, nil, nil).Image

	runs := NewOrchestrationStore(fixture.store)
	executor := &NormalizedStepExecutor{store: runs, variables: plans}
	enqueue := func(label string) *EngineRun {
		t.Helper()
		queued, _, err := runs.Enqueue(ctx, RunRequest{
			ProjectID: projectRow, EnvironmentID: environmentID, Operation: OperationDeploy,
			Trigger: TriggerManual, Actor: "operator", RequestDigest: "aws-stores-live-" + label, PlanRevision: revision,
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
	t.Logf("first step's transcript:\n%s", output.joined())
	values, err := plans.OpenRunScopedVariables(ctx, first.ID, environmentID, "runtime")
	if err != nil {
		t.Fatal(err)
	}
	variables := map[string]string{}
	for _, value := range values {
		variables[value.Name] = value.Value
	}
	containerEnvironment := time.Now().UnixNano()%1_000_000_000 + 800_000_000
	plan := RuntimePlanConfig{
		Strategy: StrategyStopFirst, StopSignal: "SIGTERM", GracePeriodSeconds: 1,
		Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
	}
	engineRun := EngineRun{ID: containerEnvironment + 1, EnvironmentID: containerEnvironment}
	release := Release{ID: containerEnvironment + 101, EnvironmentID: containerEnvironment, RunID: engineRun.ID, Number: 1}
	started, err := owner.StartCandidate(ctx, CandidateRuntimeRequest{
		Run: engineRun, Release: release, Snapshot: runtimeReleaseSnapshot{Version: 1, Plan: plan, Image: image},
		RuntimeVariables: variables, Host: "127.0.0.1",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime := runtimeFromInput(started.Input)
	defer func() { _, _ = owner.Stop(context.Background(), runtime, plan, nil, true, nil) }()
	inside, err := liveDockerOutput(ctx, "exec", runtime.RuntimeID, "sh", "-c",
		`printf 'DATABASE_URL=%s\nFIRST_DATABASE_URL=%s\nSTRIPE_KEY=%s\n' "$DATABASE_URL" "$FIRST_DATABASE_URL" "$STRIPE_KEY"`)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("container %s read:\n%s", runtime.RuntimeID[:12], inside)
	for _, want := range []string{
		"DATABASE_URL=postgres://app:aws-v2@db/app\n", "FIRST_DATABASE_URL=postgres://app:aws-v1@db/app\n", "STRIPE_KEY=sk_live_localstack\n",
	} {
		if !strings.Contains(string(inside), want) {
			t.Fatalf("the container read\n%s\nwithout %q", inside, want)
		}
	}
	for _, want := range []string{
		"Read DATABASE_URL from secret store aws: apps/web#DATABASE_URL, version ",
		"Read FIRST_DATABASE_URL from secret store aws: apps/web#DATABASE_URL@AWSPREVIOUS, version ",
		"Read STRIPE_KEY from secret store aws: stripe-key, version ",
	} {
		if !strings.Contains(output.joined(), want) {
			t.Fatalf("the transcript\n%s\nlacks %q", output.joined(), want)
		}
	}
	for _, value := range []string{"aws-v1", "aws-v2", "sk_live"} {
		if strings.Contains(output.joined(), value) {
			t.Fatalf("the transcript holds a value:\n%s", output.joined())
		}
	}
	if _, err := fixture.store.DB.Exec(`UPDATE deploy_runs SET status = 'success', state = 'succeeded', ended_at = 1 WHERE id = ?`, first.ID); err != nil {
		t.Fatal(err)
	}
	plans.SecretStoreRunObserver().RunFinished(ctx, *first)

	if _, err := liveDockerOutput(ctx, "stop", "--time", "1", name); err != nil {
		t.Fatal(err)
	}
	second := enqueue("second")
	output = &recordingStepOutput{}
	failure := executor.readSecretStores(ctx, StepExecution{Run: *second, Step: RunStep{Key: StepResolveSource}, Output: output})
	if failure == nil || failure.State != StepFailed || failure.ErrorCode != "secret_store_unreadable" ||
		!strings.Contains(failure.ErrorMessage, "secret store aws, apps/web#DATABASE_URL: could not reach "+endpoint) {
		t.Fatalf("with the store down = %+v\n%s", failure, output.joined())
	}
	t.Logf("with LocalStack stopped, the first step failed: %s: %s", failure.ErrorCode, failure.ErrorMessage)
}
