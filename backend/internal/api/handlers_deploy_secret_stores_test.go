package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/secretstore/secretstoretest"
)

// fakeKV answers the KV version 2 read and the token lookup Vault and
// OpenBao document, for one token.
func fakeKV(token string, secrets map[string]map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != token {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errors":["permission denied"]}`))
			return
		}
		if r.URL.Path == "/v1/auth/token/lookup-self" {
			_, _ = w.Write([]byte(`{"data":{"display_name":"token","policies":["web"],"ttl":0}}`))
			return
		}
		data, ok := secrets[strings.TrimPrefix(r.URL.Path, "/v1/secret/data/")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errors":[]}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"data": data, "metadata": map[string]any{"version": 7}}})
	}))
}

// Secret stores through the API: anybody who may read variables lists them
// without a credential; an administrator in a session adds, changes and
// tests one, never an API token; a limited role removes nothing; a variable
// reads a store by reference; a store a variable reads cannot be removed;
// and every change and test is audited without a credential or a value.
func TestSecretStoreRoutesAreAdministeredAuditedAndNeverEchoACredential(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	routes := s.Routes()
	admin := &client{t: t, h: routes, cookie: signInAs(t, s, "store-admin", auth.RoleAdmin)}
	limited := &client{t: t, h: routes, cookie: signInAs(t, s, "store-limited", auth.RoleLimited)}
	reader := &client{t: t, h: routes, cookie: signInAs(t, s, "store-reader", auth.RoleReadOnly)}
	const token = "s.fixture-vault-token"
	const value = "postgres://app:fixture-db-password@db/app"
	vault := fakeKV(token, map[string]map[string]string{"apps/web": {"DATABASE_URL": value}})
	defer vault.Close()

	path := "/api/v1/deploy/secret-stores"
	create := fmt.Sprintf(`{"name":"vault-prod","kind":"vault","address":%q,"settings":{"auth":"token"},"credentials":{"token":%q}}`, vault.URL, token)
	if response := reader.do(http.MethodPost, path, create, nil); response.Code != http.StatusForbidden {
		t.Fatalf("a reader created a store: %d %s", response.Code, response.Body.String())
	}
	var adminID int64
	if err := s.Store.DB.QueryRow(`SELECT id FROM users WHERE username = 'store-admin'`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	adminUser, err := s.Auth.UserByID(t.Context(), adminID)
	if err != nil {
		t.Fatal(err)
	}
	_, apiToken, err := s.Auth.CreateAPIToken(t.Context(), adminUser, "store-token", auth.RoleAdmin, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	bearer := map[string]string{"Authorization": "Bearer " + apiToken}
	if response := (&client{t: t, h: routes}).do(http.MethodPost, path, create, bearer); response.Code != http.StatusForbidden ||
		!strings.Contains(response.Body.String(), `"code":"session_required"`) {
		t.Fatalf("an API token created a store: %d %s", response.Code, response.Body.String())
	}
	created := admin.do(http.MethodPost, path, create, nil)
	if created.Code != http.StatusCreated || strings.Contains(created.Body.String(), token) ||
		!strings.Contains(created.Body.String(), `"credentialNames":["token"]`) {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	var store struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &store); err != nil {
		t.Fatal(err)
	}
	item := fmt.Sprintf("%s/%d", path, store.ID)
	plain := `{"name":"plain","kind":"vault","address":"http://vault.internal:8200","credentials":{"token":"x"}}`
	if response := admin.do(http.MethodPost, path, plain, nil); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "plain http") {
		t.Fatalf("a token over plain http = %d %s", response.Code, response.Body.String())
	}

	// Test signs in and reads the probe without returning its value.
	tested := admin.do(http.MethodPost, item+"/test", `{"path":"apps/web","key":"DATABASE_URL"}`, nil)
	if tested.Code != http.StatusOK || !strings.Contains(tested.Body.String(), `"ok":true`) ||
		!strings.Contains(tested.Body.String(), "found apps/web#DATABASE_URL, version 7") || strings.Contains(tested.Body.String(), "fixture-db-password") {
		t.Fatalf("test = %d %s", tested.Code, tested.Body.String())
	}
	if response := limited.do(http.MethodPost, item+"/test", "", nil); response.Code != http.StatusForbidden {
		t.Fatalf("a limited role tested a store: %d %s", response.Code, response.Body.String())
	}

	// An environment reads it through a secret variable of its own.
	variables := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/variables", projectID, environmentID)
	if response := admin.do(http.MethodPut, variables+"/DATABASE_URL",
		`{"revision":1,"reference":"${{store.vault-prod/apps/web#DATABASE_URL}}","sensitivity":"plain","scopes":["runtime"]}`, nil); response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), "must be secret") {
		t.Fatalf("a plain variable reading a store = %d %s", response.Code, response.Body.String())
	}
	if response := admin.do(http.MethodPut, variables+"/DATABASE_URL",
		`{"revision":1,"reference":"${{store.vault-prod/apps/web#DATABASE_URL}}","sensitivity":"secret","scopes":["runtime"]}`, nil); response.Code != http.StatusOK {
		t.Fatalf("a variable reading the store = %d %s", response.Code, response.Body.String())
	}
	if response := admin.do(http.MethodPut, variables+"/OTHER",
		`{"revision":2,"reference":"${{store.nowhere/apps/web#X}}","sensitivity":"secret","scopes":["runtime"]}`, nil); response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), "no secret store named nowhere") {
		t.Fatalf("a variable reading a missing store = %d %s", response.Code, response.Body.String())
	}
	revealed := admin.do(http.MethodGet, variables+"/DATABASE_URL/reveal", "", nil)
	if revealed.Code != http.StatusOK || strings.Contains(revealed.Body.String(), "fixture-db-password") ||
		!strings.Contains(revealed.Body.String(), "vault-prod/apps/web#DATABASE_URL") {
		t.Fatalf("reveal shows the reference, never the store's value: %d %s", revealed.Code, revealed.Body.String())
	}
	listed := reader.do(http.MethodGet, path, "", nil)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), token) || strings.Contains(listed.Body.String(), "fixture-db-password") ||
		!strings.Contains(listed.Body.String(), `"variable":"DATABASE_URL"`) || !strings.Contains(listed.Body.String(), `"ok":true`) {
		t.Fatalf("list = %d %s", listed.Code, listed.Body.String())
	}

	// An edit without credentials keeps them; a wrong token is a failed test
	// in the store's own words.
	if response := admin.do(http.MethodPut, item, fmt.Sprintf(`{"address":%q,"settings":{"auth":"token"},"allowPreviews":true}`, vault.URL), nil); response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"credentialNames":["token"]`) || !strings.Contains(response.Body.String(), `"allowPreviews":true`) {
		t.Fatalf("an edit without credentials = %d %s", response.Code, response.Body.String())
	}
	if response := admin.do(http.MethodPut, item, fmt.Sprintf(`{"address":%q,"settings":{"auth":"token"},"credentials":{"token":"s.wrong"}}`, vault.URL), nil); response.Code != http.StatusOK {
		t.Fatalf("replace the token = %d %s", response.Code, response.Body.String())
	}
	refused := admin.do(http.MethodPost, item+"/test", "", nil)
	if refused.Code != http.StatusOK || !strings.Contains(refused.Body.String(), `"ok":false`) || !strings.Contains(refused.Body.String(), "permission denied (403)") {
		t.Fatalf("a refused test = %d %s", refused.Code, refused.Body.String())
	}

	if response := limited.do(http.MethodDelete, item, "", nil); response.Code != http.StatusForbidden {
		t.Fatalf("a limited role removed a store: %d %s", response.Code, response.Body.String())
	}
	removed := admin.do(http.MethodDelete, item, "", nil)
	if removed.Code != http.StatusConflict || !strings.Contains(removed.Body.String(), "secret_store_in_use") ||
		!strings.Contains(removed.Body.String(), "api-config-app (production) as DATABASE_URL") {
		t.Fatalf("remove while read = %d %s", removed.Code, removed.Body.String())
	}
	if response := admin.do(http.MethodDelete, variables+"/DATABASE_URL", `{"revision":2}`, nil); response.Code != http.StatusOK {
		t.Fatalf("remove the variable = %d %s", response.Code, response.Body.String())
	}
	if response := admin.do(http.MethodDelete, item, "", nil); response.Code != http.StatusNoContent {
		t.Fatalf("remove = %d %s", response.Code, response.Body.String())
	}

	var audits string
	if err := s.Store.DB.QueryRow(`
		SELECT COALESCE(group_concat(action || ' ' || target || ' ' || success || ' ' || detail, char(10)), '')
		  FROM audit_log WHERE action LIKE 'deploy.secret_store.%'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"deploy.secret_store.create vault-prod 1", "deploy.secret_store.test vault-prod 1", "deploy.secret_store.test vault-prod 0",
		"deploy.secret_store.update vault-prod 1", `"signInReplaced":true`, `"signInFields":["token"]`, "deploy.secret_store.delete vault-prod 0",
		"deploy.secret_store.delete vault-prod 1", `"probe":"apps/web#DATABASE_URL"`,
	} {
		if !strings.Contains(audits, want) {
			t.Errorf("audit log lacks %q:\n%s", want, audits)
		}
	}
	if strings.Contains(audits, token) || strings.Contains(audits, "s.wrong") || strings.Contains(audits, "fixture-db-password") {
		t.Fatalf("audit log holds a credential or a value:\n%s", audits)
	}
}

// The two hosted kinds through the API: an AWS connection takes its region
// and key pair, an Azure one its vault name, tenant and application; each
// answers with its derived endpoint and the names of its credentials, Test
// speaks in the service's words, and the audit entries record the kind and
// the sign-in fields without a key or a secret.
func TestSecretStoreRoutesTakeSecretsManagerAndKeyVaultConnections(t *testing.T) {
	s := testServer(t)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "cloud-admin", auth.RoleAdmin)}
	aws := &secretstoretest.AWS{Region: "eu-west-1", AccessKeyID: "AKIAFIXTUREKEY", SecretAccessKey: "fixture-secret-access-key",
		Secrets: map[string][]secretstoretest.AWSVersion{"apps/web": {{ID: "22222222-2222-4222-8222-222222222222", Stages: []string{"AWSCURRENT"}, String: `{"DATABASE_URL":"postgres://app:aws-db-password@db/app"}`}}}}
	awsServer := httptest.NewServer(aws)
	defer awsServer.Close()
	azure := &secretstoretest.Azure{TenantID: "0a1b2c3d-0000-4000-8000-000000000001", ClientID: "9f8e7d6c-0000-4000-8000-000000000002", ClientSecret: "fixture-client-secret",
		Secrets: map[string][]secretstoretest.AzureVersion{"database-url": {{ID: "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2", Value: "postgres://app:kv-db-password@db/app"}}}}
	azureServer := httptest.NewServer(azure)
	defer azureServer.Close()

	path := "/api/v1/deploy/secret-stores"
	created := admin.do(http.MethodPost, path, fmt.Sprintf(`{"name":"aws","kind":"aws","address":%q,"settings":{"region":"eu-west-1"},
		"credentials":{"accessKeyId":%q,"secretAccessKey":%q}}`, awsServer.URL, aws.AccessKeyID, aws.SecretAccessKey), nil)
	if created.Code != http.StatusCreated || strings.Contains(created.Body.String(), aws.SecretAccessKey) ||
		!strings.Contains(created.Body.String(), `"credentialNames":["accessKeyId","secretAccessKey"]`) || !strings.Contains(created.Body.String(), fmt.Sprintf(`"endpoint":%q`, awsServer.URL)) {
		t.Fatalf("create aws = %d %s", created.Code, created.Body.String())
	}
	var store struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(created.Body.Bytes(), &store)
	tested := admin.do(http.MethodPost, fmt.Sprintf("%s/%d/test", path, store.ID), `{"path":"apps/web","key":"DATABASE_URL"}`, nil)
	if tested.Code != http.StatusOK || !strings.Contains(tested.Body.String(), `"ok":true`) ||
		!strings.Contains(tested.Body.String(), "Signed in to Secrets Manager in eu-west-1; the first secret listed is apps/web; found apps/web#DATABASE_URL, version 22222222-2222-4222-8222-222222222222") ||
		strings.Contains(tested.Body.String(), "aws-db-password") {
		t.Fatalf("test aws = %d %s", tested.Code, tested.Body.String())
	}
	if response := admin.do(http.MethodPost, path, `{"name":"aws-bad","kind":"aws","settings":{"region":"us east"},"credentials":{"accessKeyId":"a","secretAccessKey":"b"}}`, nil); response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), "AWS region") {
		t.Fatalf("a region that is not = %d %s", response.Code, response.Body.String())
	}
	if response := admin.do(http.MethodPost, path, `{"name":"aws-derived","kind":"aws","settings":{"region":"us-east-1"},"credentials":{"accessKeyId":"a","secretAccessKey":"b"}}`, nil); response.Code != http.StatusCreated ||
		!strings.Contains(response.Body.String(), `"endpoint":"https://secretsmanager.us-east-1.amazonaws.com"`) || !strings.Contains(response.Body.String(), `"address":""`) {
		t.Fatalf("a derived endpoint = %d %s", response.Code, response.Body.String())
	}

	created = admin.do(http.MethodPost, path, fmt.Sprintf(`{"name":"kv","kind":"azure","address":%q,"settings":{"vaultName":"jdtest","tenantId":%q,"authority":%q},
		"credentials":{"clientId":%q,"clientSecret":%q}}`, azureServer.URL, azure.TenantID, azureServer.URL, azure.ClientID, azure.ClientSecret), nil)
	if created.Code != http.StatusCreated || strings.Contains(created.Body.String(), azure.ClientSecret) ||
		!strings.Contains(created.Body.String(), `"credentialNames":["clientId","clientSecret"]`) || !strings.Contains(created.Body.String(), `"cloud":"public"`) {
		t.Fatalf("create azure = %d %s", created.Code, created.Body.String())
	}
	_ = json.Unmarshal(created.Body.Bytes(), &store)
	tested = admin.do(http.MethodPost, fmt.Sprintf("%s/%d/test", path, store.ID), `{"path":"database-url"}`, nil)
	if tested.Code != http.StatusOK || !strings.Contains(tested.Body.String(), `"ok":true`) ||
		!strings.Contains(tested.Body.String(), "found database-url, version b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2") || strings.Contains(tested.Body.String(), "kv-db-password") {
		t.Fatalf("test azure = %d %s", tested.Code, tested.Body.String())
	}
	if response := admin.do(http.MethodPost, path, `{"name":"kv-bad","kind":"azure","settings":{"vaultName":"my.vault","tenantId":"t"},"credentials":{"clientId":"a","clientSecret":"b"}}`, nil); response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), "Key Vault name") {
		t.Fatalf("a vault name that is not = %d %s", response.Code, response.Body.String())
	}

	var audits string
	if err := s.Store.DB.QueryRow(`
		SELECT COALESCE(group_concat(action || ' ' || target || ' ' || success || ' ' || detail, char(10)), '')
		  FROM audit_log WHERE action LIKE 'deploy.secret_store.%'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"deploy.secret_store.create aws 1", `"kind":"aws"`, `"signInFields":["accessKeyId","secretAccessKey"]`, `"address":"https://secretsmanager.us-east-1.amazonaws.com"`,
		"deploy.secret_store.create kv 1", `"kind":"azure"`, `"signInFields":["clientId","clientSecret"]`,
		"deploy.secret_store.test aws 1", `"probe":"apps/web#DATABASE_URL"`, "deploy.secret_store.test kv 1", `"probe":"database-url"`,
	} {
		if !strings.Contains(audits, want) {
			t.Errorf("audit log lacks %q:\n%s", want, audits)
		}
	}
	for _, never := range []string{aws.SecretAccessKey, azure.ClientSecret, "aws-db-password", "kv-db-password"} {
		if strings.Contains(audits, never) {
			t.Fatalf("audit log holds a credential or a value:\n%s", audits)
		}
	}
}
