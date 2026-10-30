package secretstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/secretstore/secretstoretest"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

func newRepository(t *testing.T) *Repository {
	t.Helper()
	st, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sealer, err := auth.NewSealer(strings.Repeat("5e", 32))
	if err != nil {
		t.Fatal(err)
	}
	return NewRepository(st, sealer)
}

func credentials(values map[string]string) *map[string]string { return &values }

func TestParseReferenceReadsStorePathKeyAndVersion(t *testing.T) {
	for target, want := range map[string]Reference{
		"vault-prod/apps/web#DATABASE_URL":                 {Store: "vault-prod", Path: "apps/web", Key: "DATABASE_URL"},
		"infisical#API_KEY":                                {Store: "infisical", Key: "API_KEY"},
		"bao/a/b.c/d_e#key.with-dots@12":                   {Store: "bao", Path: "a/b.c/d_e", Key: "key.with-dots", Version: "12"},
		"doppler#STRIPE_KEY":                               {Store: "doppler", Key: "STRIPE_KEY"},
		"aws/prod/db#DATABASE_URL@AWSPREVIOUS":             {Store: "aws", Path: "prod/db", Key: "DATABASE_URL", Version: "AWSPREVIOUS"},
		"aws/prod/db":                                      {Store: "aws", Path: "prod/db"},
		"aws/prod/db@6f1a2b3c-4d5e-4f60-8a9b-0c1d2e3f4a5b": {Store: "aws", Path: "prod/db", Version: "6f1a2b3c-4d5e-4f60-8a9b-0c1d2e3f4a5b"},
		"kv/database-url@0123456789abcdef0123456789abcdef": {Store: "kv", Path: "database-url", Version: "0123456789abcdef0123456789abcdef"},
		"kv/app-config#API_KEY":                            {Store: "kv", Path: "app-config", Key: "API_KEY"},
	} {
		got, err := ParseReference(target)
		if err != nil || got != want {
			t.Fatalf("ParseReference(%q) = %+v, %v; want %+v", target, got, err, want)
		}
		if got.String() != target {
			t.Fatalf("%q reads back as %q", target, got.String())
		}
	}
	for _, target := range []string{
		"vault", "vault@3", "vault/apps/web#", "#KEY", "Vault/apps#KEY", "vault/../etc#KEY", "vault/apps/./web#KEY",
		"vault//apps#KEY", "vault/apps/#KEY", "vault/apps#K#Y", "vault/apps#KEY@", "vault/apps@", "vault/apps#KEY@a b",
		"vault/apps#KEY@a@b", "vault/apps#KE Y", "vault/ap ps#KEY", "-vault#KEY", strings.Repeat("a", 301),
	} {
		if _, err := ParseReference(target); err == nil {
			t.Fatalf("ParseReference(%q) was accepted", target)
		}
	}
	for _, test := range []struct {
		reference Reference
		location  string
	}{
		{Reference{Store: "v", Path: "apps/web", Key: "K", Version: "2"}, "apps/web#K@2"},
		{Reference{Store: "d", Key: "K"}, "K"},
		{Reference{Store: "aws", Path: "prod/db"}, "prod/db"},
		{Reference{Store: "aws", Path: "prod/db", Version: "AWSPREVIOUS"}, "prod/db@AWSPREVIOUS"},
	} {
		if got := test.reference.Location(); got != test.location {
			t.Fatalf("Location(%+v) = %q; want %q", test.reference, got, test.location)
		}
	}
}

// What a path, a key and a version mean depends on the store, so the grammar
// admits every store's and checkFits holds each reference to its store's.
func TestCheckFitsHoldsEachKindToItsOwnShape(t *testing.T) {
	parse := func(target string) Reference {
		t.Helper()
		reference, err := ParseReference(target)
		if err != nil {
			t.Fatal(err)
		}
		return reference
	}
	for kind, targets := range map[string][]string{
		KindVault:     {"s/apps/web#K", "s/apps/web#K@3"},
		KindOpenBao:   {"s/apps/web#K@12"},
		KindInfisical: {"s#K", "s/backend#K@2"},
		KindDoppler:   {"s#K"},
		KindAWS:       {"s/apps/web", "s/apps/web#K", "s/apps/web#K@AWSPREVIOUS", "s/apps/web@6f1a2b3c-4d5e-4f60-8a9b-0c1d2e3f4a5b", "s/prod/db@custom-label"},
		KindAzure:     {"s/database-url", "s/app-config#K", "s/database-url@0123456789abcdef0123456789abcdef"},
	} {
		for _, target := range targets {
			if err := checkFits(kind, parse(target)); err != nil {
				t.Errorf("%s refused %q: %v", kind, target, err)
			}
		}
	}
	for kind, refused := range map[string]map[string]string{
		KindVault:     {"s#K": "at a path", "s/apps/web": "names a key", "s/apps/web#K@0": "whole number", "s/apps/web#K@01": "whole number", "s/apps/web#K@x": "whole number", "s/apps/web#K@-1": "whole number"},
		KindInfisical: {"s/backend": "names a key", "s/backend#K@AWSPREVIOUS": "whole number"},
		KindDoppler:   {"s/a#K": "without a path", "s#K@2": "cannot pin", "s/a": "names a key"},
		KindAWS:       {"s#K": "names the secret"},
		KindAzure:     {"s#K": "names the secret", "s/a/b": "letters, digits and dashes", "s/a_b": "letters, digits and dashes", "s/a@3": "32 hexadecimal", "s/a@0123456789ABCDEF0123456789ABCDEF": "32 hexadecimal"},
	} {
		for target, want := range refused {
			err := checkFits(kind, parse(target))
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), want) {
				t.Errorf("%s with %q = %v; want %q", kind, target, err, want)
			}
		}
	}
}

func TestConnectionsSealTheirCredentialsAndReadBackOnlyNames(t *testing.T) {
	repo := newRepository(t)
	ctx := t.Context()
	item, err := repo.Create(ctx, Write{
		Name: "vault-prod", Kind: KindVault, Address: "https://vault.example.test:8200/",
		Settings:    Settings{Auth: AuthAppRole, Namespace: "team-a"},
		Credentials: credentials(map[string]string{"roleId": "fixture-role-id", "secretId": "fixture-secret-id"}),
	}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if item.Address != "https://vault.example.test:8200" || item.Settings.Mount != "secret" || item.Settings.AuthMount != "approle" {
		t.Fatalf("defaults were not applied: %+v", item)
	}
	if strings.Join(item.CredentialNames, ",") != "roleId,secretId" {
		t.Fatalf("credential names = %v", item.CredentialNames)
	}
	encoded, _ := json.Marshal(item)
	if strings.Contains(string(encoded), "fixture-") {
		t.Fatalf("the read model carries a credential: %s", encoded)
	}
	var sealed string
	if err := repo.db.QueryRow(`SELECT credentials_enc FROM deploy_secret_stores WHERE id = ?`, item.ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if sealed == "" || strings.Contains(sealed, "fixture-") {
		t.Fatalf("credentials are not sealed at rest: %q", sealed)
	}

	// An edit that leaves credentials out keeps them; one that changes how
	// the store signs in needs the new way's.
	updated, replaced, err := repo.Update(ctx, item.ID, Write{Address: "https://vault2.example.test", Settings: Settings{Auth: AuthAppRole}, AllowPreviews: true}, "operator")
	if err != nil || replaced || strings.Join(updated.CredentialNames, ",") != "roleId,secretId" || !updated.AllowPreviews {
		t.Fatalf("update = %+v %v %v", updated, replaced, err)
	}
	if _, _, err := repo.Update(ctx, item.ID, Write{Address: "https://vault2.example.test", Settings: Settings{Auth: AuthToken}}, "operator"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("switching to a token without one = %v", err)
	}
	if _, _, err := repo.Update(ctx, item.ID, Write{Name: "renamed", Address: "https://vault2.example.test", Settings: Settings{Auth: AuthAppRole}}, "operator"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a rename = %v", err)
	}
	if _, err := repo.Create(ctx, Write{Name: "vault-prod", Kind: KindVault, Address: "https://x.example.test",
		Credentials: credentials(map[string]string{"token": "t"})}, "operator"); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("a second store of the same name = %v", err)
	}
}

func TestConnectionsRefuseCredentialsOverPlainHTTPAndBadSettings(t *testing.T) {
	repo := newRepository(t)
	token := credentials(map[string]string{"token": "s.fixture"})
	for name, write := range map[string]Write{
		"plain http off this machine": {Name: "a", Kind: KindVault, Address: "http://vault.internal:8200", Credentials: token},
		"a sign-in in the address":    {Name: "b", Kind: KindVault, Address: "https://user:pw@vault.example.test", Credentials: token},
		"no address for vault":        {Name: "c", Kind: KindVault, Credentials: token},
		"an unknown kind":             {Name: "d", Kind: "keyvault", Address: "https://x.example.test", Credentials: token},
		"an uppercase name":           {Name: "Prod", Kind: KindVault, Address: "https://x.example.test", Credentials: token},
		"no token":                    {Name: "e", Kind: KindVault, Address: "https://x.example.test"},
		"a credential it never takes": {Name: "f", Kind: KindDoppler, Credentials: credentials(map[string]string{"token": "dp.st.x", "clientId": "x"})},
		"infisical without a project": {Name: "g", Kind: KindInfisical, Settings: Settings{Environment: "prod"}, Credentials: credentials(map[string]string{"clientId": "a", "clientSecret": "b"})},
		"a certificate that is not":   {Name: "h", Kind: KindVault, Address: "https://x.example.test", Settings: Settings{CACert: "-----BEGIN CERTIFICATE-----\nnope\n-----END CERTIFICATE-----"}, Credentials: token},
		"a token with a line break":   {Name: "i", Kind: KindVault, Address: "https://x.example.test", Credentials: credentials(map[string]string{"token": "a\nb"})},
		"aws without a region":        {Name: "j", Kind: KindAWS, Credentials: credentials(map[string]string{"accessKeyId": "AKIA", "secretAccessKey": "s"})},
		"aws with a region that is not": {Name: "k", Kind: KindAWS, Settings: Settings{Region: "us_east.amazonaws.com"},
			Credentials: credentials(map[string]string{"accessKeyId": "AKIA", "secretAccessKey": "s"})},
		"aws without a secret key": {Name: "l", Kind: KindAWS, Settings: Settings{Region: "us-east-1"}, Credentials: credentials(map[string]string{"accessKeyId": "AKIA"})},
		"azure with a vault name that is not": {Name: "m", Kind: KindAzure, Settings: Settings{VaultName: "my.vault", TenantID: "t"},
			Credentials: credentials(map[string]string{"clientId": "c", "clientSecret": "s"})},
		"azure with two dashes": {Name: "n", Kind: KindAzure, Settings: Settings{VaultName: "my--vault", TenantID: "t"},
			Credentials: credentials(map[string]string{"clientId": "c", "clientSecret": "s"})},
		"azure without a tenant": {Name: "o", Kind: KindAzure, Settings: Settings{VaultName: "myvault"},
			Credentials: credentials(map[string]string{"clientId": "c", "clientSecret": "s"})},
		"azure in a cloud that is not": {Name: "p", Kind: KindAzure, Settings: Settings{VaultName: "myvault", TenantID: "t", Cloud: "germany"},
			Credentials: credentials(map[string]string{"clientId": "c", "clientSecret": "s"})},
		"azure with an authority over plain http": {Name: "q", Kind: KindAzure, Settings: Settings{VaultName: "myvault", TenantID: "t", Authority: "http://login.lan"},
			Credentials: credentials(map[string]string{"clientId": "c", "clientSecret": "s"})},
	} {
		if _, err := repo.Create(t.Context(), write, "operator"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for _, address := range []string{"http://127.0.0.1:8200", "http://localhost:8200", "http://[::1]:8200"} {
		if _, err := normalizeAddress(KindVault, address); err != nil {
			t.Fatalf("loopback %s refused: %v", address, err)
		}
	}
	item, err := repo.Create(t.Context(), Write{Name: "doppler", Kind: KindDoppler, Credentials: credentials(map[string]string{"token": "dp.st.x"})}, "operator")
	if err != nil || item.Address != DopplerAPI {
		t.Fatalf("Doppler's default address = %+v %v", item, err)
	}
	item, err = repo.Create(t.Context(), Write{Name: "infisical", Kind: KindInfisical, Settings: Settings{Project: "p-1", Environment: "prod"},
		Credentials: credentials(map[string]string{"clientId": "a", "clientSecret": "b"})}, "operator")
	if err != nil || item.Address != InfisicalCloud {
		t.Fatalf("Infisical's default address = %+v %v", item, err)
	}
	// AWS and Azure need no address: the region and the vault name derive
	// the endpoint, and an address given overrides it.
	item, err = repo.Create(t.Context(), Write{Name: "aws", Kind: KindAWS, Settings: Settings{Region: "EU-West-2"},
		Credentials: credentials(map[string]string{"accessKeyId": "AKIAFIXTURE", "secretAccessKey": "fixture"})}, "operator")
	if err != nil || item.Address != "" || item.Endpoint != "https://secretsmanager.eu-west-2.amazonaws.com" || item.Settings.Region != "eu-west-2" ||
		strings.Join(item.CredentialNames, ",") != "accessKeyId,secretAccessKey" {
		t.Fatalf("an AWS store = %+v %v", item, err)
	}
	item, err = repo.Create(t.Context(), Write{Name: "aws-cn", Kind: KindAWS, Settings: Settings{Region: "cn-north-1"}, Address: "http://127.0.0.1:4566/",
		Credentials: credentials(map[string]string{"accessKeyId": "AKIAFIXTURE", "secretAccessKey": "fixture", "sessionToken": "session"})}, "operator")
	if err != nil || item.Endpoint != "http://127.0.0.1:4566" || strings.Join(item.CredentialNames, ",") != "accessKeyId,secretAccessKey,sessionToken" {
		t.Fatalf("an AWS store with an endpoint = %+v %v", item, err)
	}
	if endpoint := endpointOf(KindAWS, "", Settings{Region: "cn-north-1"}); endpoint != "https://secretsmanager.cn-north-1.amazonaws.com.cn" {
		t.Fatalf("China's endpoint = %s", endpoint)
	}
	item, err = repo.Create(t.Context(), Write{Name: "kv", Kind: KindAzure, Settings: Settings{VaultName: "Shop-Prod", TenantID: "0a1b2c3d-0000-4000-8000-000000000001"},
		Credentials: credentials(map[string]string{"clientId": "c", "clientSecret": "s"})}, "operator")
	if err != nil || item.Endpoint != "https://shop-prod.vault.azure.net" || item.Settings.Cloud != AzurePublic || azureAuthority(item.Settings) != "https://login.microsoftonline.com" {
		t.Fatalf("an Azure store = %+v %v", item, err)
	}
	if endpoint := endpointOf(KindAzure, "", Settings{VaultName: "gov", Cloud: AzureUSGov}); endpoint != "https://gov.vault.usgovcloudapi.net" ||
		azureAuthority(Settings{Cloud: AzureUSGov}) != "https://login.microsoftonline.us" {
		t.Fatalf("the US Government cloud = %s", endpoint)
	}
}

// fakeVault answers the KV version 2, AppRole and token APIs Vault and
// OpenBao document, for one mount at "secret" and one namespace.
type fakeVault struct {
	mu        sync.Mutex
	token     string
	namespace string
	versions  map[string][]map[string]any // path → versions, 1-based by index
	deleted   map[string]int              // path → a deleted version
	requests  []string
	revoked   []string
}

func (f *fakeVault) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.RequestURI())
	if f.namespace != "" && r.Header.Get("X-Vault-Namespace") != f.namespace {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":["1 error occurred:\n\t* permission denied\n\n"]}`))
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/approle/login":
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["role_id"] != "role-1" || body["secret_id"] != "secret-1" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errors":["invalid role or secret ID"]}`))
			return
		}
		_, _ = w.Write([]byte(`{"auth":{"client_token":"` + f.token + `","policies":["default","web"],"lease_duration":3600}}`))
		return
	case r.URL.Path == "/v1/auth/token/revoke-self":
		f.revoked = append(f.revoked, r.Header.Get("X-Vault-Token"))
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Header.Get("X-Vault-Token") != f.token {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":["permission denied"]}`))
		return
	}
	if r.URL.Path == "/v1/auth/token/lookup-self" {
		_, _ = w.Write([]byte(`{"data":{"display_name":"approle","policies":["default","web"],"ttl":3600}}`))
		return
	}
	path, ok := strings.CutPrefix(r.URL.Path, "/v1/secret/data/")
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errors":[]}`))
		return
	}
	versions := f.versions[path]
	version := len(versions)
	if asked := r.URL.Query().Get("version"); asked != "" {
		_, _ = fmt.Sscanf(asked, "%d", &version)
	}
	if version == 0 || version > len(versions) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errors":[]}`))
		return
	}
	if f.deleted[path] == version {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprintf(w, `{"data":{"data":null,"metadata":{"version":%d,"deletion_time":"2026-10-27T00:00:00Z","destroyed":false}}}`, version)
		return
	}
	encoded, _ := json.Marshal(map[string]any{"data": map[string]any{
		"data": versions[version-1], "metadata": map[string]any{"version": version, "deletion_time": "", "destroyed": false},
	}})
	_, _ = w.Write(encoded)
}

func TestResolveReadsVaultThroughAppRoleOncePerPathAndRevokesTheToken(t *testing.T) {
	vault := &fakeVault{token: "s.minted", namespace: "team-a", versions: map[string][]map[string]any{
		"apps/web": {
			{"DATABASE_URL": "postgres://old", "API_KEY": "key-1"},
			{"DATABASE_URL": "postgres://new", "API_KEY": "key-2", "PORT": 8080, "LIMITS": map[string]any{"rps": 5}},
		},
	}}
	server := httptest.NewServer(vault)
	defer server.Close()
	repo := newRepository(t)
	item, err := repo.Create(t.Context(), Write{Name: "bao", Kind: KindOpenBao, Address: server.URL,
		Settings:    Settings{Auth: AuthAppRole, Namespace: "team-a"},
		Credentials: credentials(map[string]string{"roleId": "role-1", "secretId": "secret-1"})}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	references := []Reference{
		{Store: "bao", Path: "apps/web", Key: "DATABASE_URL"},
		{Store: "bao", Path: "apps/web", Key: "API_KEY"},
		{Store: "bao", Path: "apps/web", Key: "PORT"},
		{Store: "bao", Path: "apps/web", Key: "LIMITS"},
		{Store: "bao", Path: "apps/web", Key: "DATABASE_URL", Version: "1"},
	}
	reads, err := repo.Resolve(t.Context(), references, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Read{
		"bao/apps/web#DATABASE_URL":   {Value: "postgres://new", Version: "2"},
		"bao/apps/web#API_KEY":        {Value: "key-2", Version: "2"},
		"bao/apps/web#PORT":           {Value: "8080", Version: "2"},
		"bao/apps/web#LIMITS":         {Value: `{"rps":5}`, Version: "2"},
		"bao/apps/web#DATABASE_URL@1": {Value: "postgres://old", Version: "1"},
	}
	for key, value := range want {
		if reads[key] != value {
			t.Fatalf("%s = %+v; want %+v (all %+v)", key, reads[key], value, reads)
		}
	}
	reading := 0
	for _, request := range vault.requests {
		if strings.HasPrefix(request, "GET /v1/secret/data/apps/web") {
			reading++
		}
	}
	// One read of the path for every unpinned key, one for the pinned one.
	if reading != 2 || len(vault.revoked) != 1 || vault.revoked[0] != "s.minted" {
		t.Fatalf("requests %v, revoked %v", vault.requests, vault.revoked)
	}
	saved, _ := repo.Get(t.Context(), item.ID)
	if saved.LastRead == nil || !saved.LastRead.OK {
		t.Fatalf("the read was not recorded: %+v", saved.LastRead)
	}
}

func TestResolveFailsInTheStoresOwnWordsWithoutAValue(t *testing.T) {
	vault := &fakeVault{token: "s.fixture-token", versions: map[string][]map[string]any{
		"apps/web": {{"A": "first"}, {"A": "second-secret-value"}},
	}, deleted: map[string]int{"apps/web": 2}}
	server := httptest.NewServer(vault)
	defer server.Close()
	repo := newRepository(t)
	create := func(name, token string, previews bool) {
		t.Helper()
		if _, err := repo.Create(t.Context(), Write{Name: name, Kind: KindVault, Address: server.URL, AllowPreviews: previews,
			Credentials: credentials(map[string]string{"token": token})}, "operator"); err != nil {
			t.Fatal(err)
		}
	}
	create("good", "s.fixture-token", false)
	create("refused", "s.wrong-token", true)
	cases := map[string]struct {
		reference Reference
		options   ResolveOptions
		want      string
	}{
		"a refused token":         {Reference{Store: "refused", Path: "apps/web", Key: "A"}, ResolveOptions{}, "secret store refused, apps/web#A: permission denied (403)"},
		"a deleted version":       {Reference{Store: "good", Path: "apps/web", Key: "A"}, ResolveOptions{}, "version 2 of secret/apps/web is deleted"},
		"a missing path":          {Reference{Store: "good", Path: "apps/none", Key: "A"}, ResolveOptions{}, "secret/apps/none holds no secret (404)"},
		"a missing key":           {Reference{Store: "good", Path: "apps/web", Key: "B", Version: "1"}, ResolveOptions{}, "version 1 of secret/apps/web has no key B"},
		"a preview, not allowed":  {Reference{Store: "good", Path: "apps/web", Key: "A"}, ResolveOptions{Preview: true}, "does not serve previews"},
		"no such store":           {Reference{Store: "nobody", Path: "apps/web", Key: "A"}, ResolveOptions{}, "there is no secret store by that name"},
		"vault without a path":    {Reference{Store: "good", Key: "A"}, ResolveOptions{}, "a Vault secret is at a path"},
		"a store that is offline": {Reference{Store: "offline", Path: "apps/web", Key: "A"}, ResolveOptions{}, "could not reach"},
	}
	offline := httptest.NewServer(http.NotFoundHandler())
	offlineURL := offline.URL
	offline.Close()
	if _, err := repo.Create(t.Context(), Write{Name: "offline", Kind: KindVault, Address: offlineURL,
		Credentials: credentials(map[string]string{"token": "s.fixture-token"})}, "operator"); err != nil {
		t.Fatal(err)
	}
	for name, tc := range cases {
		_, err := repo.Resolve(t.Context(), []Reference{tc.reference}, tc.options)
		var failure *Failure
		if !errors.As(err, &failure) || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: %v; want %q", name, err, tc.want)
		}
		if strings.Contains(err.Error(), "second-secret-value") || strings.Contains(err.Error(), "s.fixture-token") || strings.Contains(err.Error(), "s.wrong-token") {
			t.Fatalf("%s: the failure carries a value or a credential: %v", name, err)
		}
	}
	saved, err := repo.GetByName(t.Context(), "refused")
	if err != nil || saved.LastRead == nil || saved.LastRead.OK || !strings.Contains(saved.LastRead.Message, "permission denied") {
		t.Fatalf("the refusal was not recorded on the store: %+v %v", saved, err)
	}
}

func TestStoresNeverFollowARedirectWithTheToken(t *testing.T) {
	var leaked []string
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = append(leaked, r.Header.Get("X-Vault-Token"))
	}))
	defer elsewhere.Close()
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer redirecting.Close()
	repo := newRepository(t)
	if _, err := repo.Create(t.Context(), Write{Name: "standby", Kind: KindVault, Address: redirecting.URL,
		Credentials: credentials(map[string]string{"token": "s.fixture"})}, "operator"); err != nil {
		t.Fatal(err)
	}
	_, err := repo.Resolve(t.Context(), []Reference{{Store: "standby", Path: "apps/web", Key: "A"}}, ResolveOptions{})
	if err == nil || !strings.Contains(err.Error(), "redirected") || len(leaked) != 0 {
		t.Fatalf("a redirect was followed: %v, the other server saw %v", err, leaked)
	}
}

// fakeInfisical answers the Universal Auth login and v3 raw secrets API, for
// one project with a root folder and a /backend folder.
type fakeInfisical struct {
	mu       sync.Mutex
	requests []string
	revoked  bool
	secrets  map[string][]map[string]any // folder → secrets, each with its versions' values
}

func (f *fakeInfisical) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	answer := func(status int, body any) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	switch r.URL.Path {
	case "/api/v1/auth/universal-auth/login":
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["clientId"] != "client-1" || body["clientSecret"] != "fixture-client-secret" {
			answer(http.StatusUnauthorized, map[string]any{"statusCode": 401, "message": "Invalid credentials", "error": "UnauthorizedError"})
			return
		}
		answer(http.StatusOK, map[string]any{"accessToken": "fixture-access-token", "expiresIn": 7200, "tokenType": "Bearer"})
		return
	case "/api/v1/auth/token/revoke":
		f.revoked = true
		answer(http.StatusOK, map[string]any{"message": "revoked"})
		return
	}
	if r.Header.Get("Authorization") != "Bearer fixture-access-token" {
		answer(http.StatusUnauthorized, map[string]any{"statusCode": 401, "message": "Invalid token", "error": "UnauthorizedError"})
		return
	}
	query := r.URL.Query()
	if query.Get("workspaceId") != "project-1" || query.Get("environment") != "prod" {
		answer(http.StatusNotFound, map[string]any{"statusCode": 404, "message": "Folder with path '/' in environment 'nope' was not found.", "error": "SecretPathNotFound"})
		return
	}
	folder := f.secrets[query.Get("secretPath")]
	if r.URL.Path == "/api/v3/secrets/raw" {
		list := []map[string]any{}
		for _, secret := range folder {
			versions := secret["versions"].([]string)
			list = append(list, map[string]any{"secretKey": secret["key"], "secretValue": versions[len(versions)-1], "version": len(versions)})
		}
		answer(http.StatusOK, map[string]any{"secrets": list, "imports": []any{}})
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/v3/secrets/raw/")
	for _, secret := range folder {
		if secret["key"] != name {
			continue
		}
		versions := secret["versions"].([]string)
		version := len(versions)
		_, _ = fmt.Sscanf(query.Get("version"), "%d", &version)
		answer(http.StatusOK, map[string]any{"secret": map[string]any{"secretKey": name, "secretValue": versions[version-1], "version": version}})
		return
	}
	answer(http.StatusNotFound, map[string]any{"statusCode": 404, "message": "Secret with name '" + name + "' not found", "error": "NotFound"})
}

func TestResolveReadsInfisicalFoldersAndPinnedVersions(t *testing.T) {
	infisical := &fakeInfisical{secrets: map[string][]map[string]any{
		"/":        {{"key": "API_KEY", "versions": []string{"key-1"}}},
		"/backend": {{"key": "SESSION_SECRET", "versions": []string{"sess-1", "sess-2"}}, {"key": "OTHER", "versions": []string{"x"}}},
	}}
	server := httptest.NewServer(infisical)
	defer server.Close()
	repo := newRepository(t)
	if _, err := repo.Create(t.Context(), Write{Name: "infisical", Kind: KindInfisical, Address: server.URL,
		Settings:    Settings{Project: "project-1", Environment: "prod"},
		Credentials: credentials(map[string]string{"clientId": "client-1", "clientSecret": "fixture-client-secret"})}, "operator"); err != nil {
		t.Fatal(err)
	}
	reads, err := repo.Resolve(t.Context(), []Reference{
		{Store: "infisical", Key: "API_KEY"},
		{Store: "infisical", Path: "backend", Key: "SESSION_SECRET"},
		{Store: "infisical", Path: "backend", Key: "OTHER"},
		{Store: "infisical", Path: "backend", Key: "SESSION_SECRET", Version: "1"},
	}, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if reads["infisical#API_KEY"] != (Read{Value: "key-1", Version: "1"}) ||
		reads["infisical/backend#SESSION_SECRET"] != (Read{Value: "sess-2", Version: "2"}) ||
		reads["infisical/backend#SESSION_SECRET@1"] != (Read{Value: "sess-1", Version: "1"}) ||
		reads["infisical/backend#OTHER"].Value != "x" {
		t.Fatalf("reads = %+v", reads)
	}
	logins, lists := 0, 0
	for _, request := range infisical.requests {
		switch request {
		case "POST /api/v1/auth/universal-auth/login":
			logins++
		case "GET /api/v3/secrets/raw":
			lists++
		}
	}
	if logins != 1 || lists != 2 || !infisical.revoked {
		t.Fatalf("one sign-in, one list per folder and a revoked token; got %v (revoked %v)", infisical.requests, infisical.revoked)
	}
	_, err = repo.Resolve(t.Context(), []Reference{{Store: "infisical", Key: "NOPE"}}, ResolveOptions{})
	if err == nil || !strings.Contains(err.Error(), "/ has no key NOPE") {
		t.Fatalf("a missing key = %v", err)
	}
	if _, _, err := repo.Update(t.Context(), 1, Write{Address: server.URL, Settings: Settings{Project: "project-1", Environment: "prod"},
		Credentials: credentials(map[string]string{"clientId": "client-1", "clientSecret": "rotated-wrong"})}, "operator"); err != nil {
		t.Fatal(err)
	}
	_, err = repo.Resolve(t.Context(), []Reference{{Store: "infisical", Key: "API_KEY"}}, ResolveOptions{})
	if err == nil || !strings.Contains(err.Error(), "Universal Auth sign-in refused: Invalid credentials (401)") || strings.Contains(err.Error(), "rotated-wrong") {
		t.Fatalf("a refused sign-in = %v", err)
	}
}

// fakeDoppler follows Doppler's documented API for a service token: the
// config download answers every secret as one JSON object, including the
// DOPPLER_PROJECT, DOPPLER_CONFIG and DOPPLER_ENVIRONMENT meta keys, and a
// token it does not know is answered 401 with the messages list.
func fakeDoppler(t *testing.T, secrets map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer dp.st.prd.fixture" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"messages":["Invalid Service token"],"success":false}`))
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/v3/configs/config/secrets/download" || r.URL.Query().Get("format") != "json" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"messages":["Not found"],"success":false}`))
			return
		}
		body := map[string]string{"DOPPLER_PROJECT": "backend", "DOPPLER_CONFIG": "prd", "DOPPLER_ENVIRONMENT": "prd"}
		for key, value := range secrets {
			body[key] = value
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
}

func TestResolveReadsADopplerConfigAndTestNamesIt(t *testing.T) {
	server := fakeDoppler(t, map[string]string{"STRIPE_KEY": "sk_live_fixture", "SENTRY_DSN": "https://k@sentry.example.test/1"})
	defer server.Close()
	repo := newRepository(t)
	item, err := repo.Create(t.Context(), Write{Name: "doppler", Kind: KindDoppler, Address: server.URL,
		Credentials: credentials(map[string]string{"token": "dp.st.prd.fixture"})}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	reads, err := repo.Resolve(t.Context(), []Reference{{Store: "doppler", Key: "STRIPE_KEY"}, {Store: "doppler", Key: "SENTRY_DSN"}}, ResolveOptions{})
	if err != nil || reads["doppler#STRIPE_KEY"] != (Read{Value: "sk_live_fixture"}) || reads["doppler#SENTRY_DSN"].Value == "" {
		t.Fatalf("reads = %+v, %v", reads, err)
	}
	result, err := repo.Test(t.Context(), item.ID, nil)
	if err != nil || !result.OK || result.Message != "Read backend / prd; 2 secrets" {
		t.Fatalf("test = %+v, %v", result, err)
	}
	if err := repo.Check(t.Context(), Reference{Store: "doppler", Path: "a", Key: "B"}, false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a Doppler reference with a path = %v", err)
	}
	if err := repo.Check(t.Context(), Reference{Store: "doppler", Key: "B", Version: "2"}, false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a pinned Doppler reference = %v", err)
	}
	if _, _, err := repo.Update(t.Context(), item.ID, Write{Address: server.URL, Credentials: credentials(map[string]string{"token": "dp.st.other"})}, "operator"); err != nil {
		t.Fatal(err)
	}
	result, err = repo.Test(t.Context(), item.ID, nil)
	if err != nil || result.OK || result.Message != "Invalid Service token (401)" {
		t.Fatalf("a refused test = %+v, %v", result, err)
	}
	saved, _ := repo.Get(t.Context(), item.ID)
	if saved.Test == nil || saved.Test.OK || saved.Test.Message != "Invalid Service token (401)" {
		t.Fatalf("the test was not recorded: %+v", saved.Test)
	}
}

func TestTestSignsInNamesTheIdentityAndReadsAProbe(t *testing.T) {
	vault := &fakeVault{token: "s.fixture", versions: map[string][]map[string]any{"apps/web": {{"A": "1"}, {"A": "2"}, {"A": "3"}}}}
	server := httptest.NewServer(vault)
	defer server.Close()
	repo := newRepository(t)
	item, err := repo.Create(t.Context(), Write{Name: "vault", Kind: KindVault, Address: server.URL,
		Credentials: credentials(map[string]string{"token": "s.fixture"})}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	result, err := repo.Test(t.Context(), item.ID, &Probe{Path: "apps/web", Key: "A"})
	if err != nil || !result.OK || result.Version != "3" ||
		result.Message != "Signed in as approle with default, web; the token expires in 1h0m0s; found apps/web#A, version 3" {
		t.Fatalf("test = %+v, %v", result, err)
	}
	result, err = repo.Test(t.Context(), item.ID, &Probe{Path: "apps/web", Key: "MISSING"})
	if err != nil || result.OK || !strings.HasSuffix(result.Message, "apps/web has no key MISSING") {
		t.Fatalf("a probe for a missing key = %+v, %v", result, err)
	}
	if _, err := repo.Test(t.Context(), item.ID, &Probe{Path: "../etc", Key: "A"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a probe that climbs = %v", err)
	}
}

// A self-hosted store with a certificate of its own CA is trusted with that
// CA, and refused without it.
func TestACustomCertificateAuthorityIsTrustedOnlyWhereGiven(t *testing.T) {
	vault := &fakeVault{token: "s.fixture", versions: map[string][]map[string]any{"apps/web": {{"A": "tls-value"}}}}
	server := httptest.NewTLSServer(vault)
	defer server.Close()
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
	repo := newRepository(t)
	token := credentials(map[string]string{"token": "s.fixture"})
	if _, err := repo.Create(t.Context(), Write{Name: "with-ca", Kind: KindVault, Address: server.URL, Settings: Settings{CACert: caPEM}, Credentials: token}, "operator"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(t.Context(), Write{Name: "without-ca", Kind: KindVault, Address: server.URL, Credentials: token}, "operator"); err != nil {
		t.Fatal(err)
	}
	reads, err := repo.Resolve(t.Context(), []Reference{{Store: "with-ca", Path: "apps/web", Key: "A"}}, ResolveOptions{})
	if err != nil || reads["with-ca/apps/web#A"].Value != "tls-value" {
		t.Fatalf("with the CA: %+v %v", reads, err)
	}
	if _, err := repo.Resolve(t.Context(), []Reference{{Store: "without-ca", Path: "apps/web", Key: "A"}}, ResolveOptions{}); err == nil ||
		!strings.Contains(err.Error(), "certificate") {
		t.Fatalf("without the CA: %v", err)
	}
	if !validCACert(selfSignedPEM(t)) {
		t.Fatal("a generated certificate was not read as one")
	}
}

func selfSignedPEM(t *testing.T) string {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "jd test CA"},
		NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	raw, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw}))
}

func TestAStoreThatNeverAnswersTimesOut(t *testing.T) {
	release := make(chan struct{})
	hanging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer hanging.Close()
	defer close(release)
	repo := newRepository(t)
	if _, err := repo.Create(t.Context(), Write{Name: "hanging", Kind: KindVault, Address: hanging.URL,
		Credentials: credentials(map[string]string{"token": "s.fixture"})}, "operator"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := repo.Resolve(ctx, []Reference{{Store: "hanging", Path: "apps/web", Key: "A"}}, ResolveOptions{})
	if err == nil || time.Since(started) > 5*time.Second {
		t.Fatalf("a store that never answers: %v after %s", err, time.Since(started))
	}
}

func TestAnswerMessageReadsEachStoresErrorShape(t *testing.T) {
	for raw, want := range map[string]string{
		`{"errors":["1 error occurred:\n\t* permission denied\n\n"]}`:                                                                             "permission denied (403)",
		`{"messages":["Invalid Service token"],"success":false}`:                                                                                  "Invalid Service token (403)",
		`{"statusCode":403,"message":"You are not allowed","error":"PermissionDenied"}`:                                                           "You are not allowed (403)",
		`{"message":[{"message":"Slug field must be at least 5"}],"error":"Validation"}`:                                                          "Slug field must be at least 5 (403)",
		`{"__type":"AccessDeniedException","Message":"User: arn:aws:iam::1:user/jd is not authorized to perform: secretsmanager:GetSecretValue"}`: "AccessDeniedException: User: arn:aws:iam::1:user/jd is not authorized to perform: secretsmanager:GetSecretValue (403)",
		`{"__type":"com.amazonaws.secretsmanager#ResourceNotFoundException","message":"Secrets Manager can't find the specified secret."}`:        "ResourceNotFoundException: Secrets Manager can't find the specified secret. (403)",
		`{"__type":"InvalidSignatureException"}`:                                                                                                  "InvalidSignatureException (403)",
		`{"error":{"code":"Forbidden","message":"The user, group or application 'appid=1' does not have secrets get permission on key vault 'kv;location=westeurope'. For help resolving this issue, please see https://go.microsoft.com/fwlink/?linkid=2125287","innererror":{"code":"AccessDenied"}}}`:                                                "Forbidden: The user, group or application 'appid=1' does not have secrets get permission on key vault 'kv;location=westeurope'. (403)",
		`{"error":"invalid_client","error_description":"AADSTS7000215: Invalid client secret provided. Ensure the secret being sent in the request is the client secret value, not the client secret ID, for a secret added to app '1'.\r\nTrace ID: 0ff1ce00\r\nCorrelation ID: c0ffee00\r\nTimestamp: 2026-10-28 09:15:42Z","error_codes":[7000215]}`: "invalid_client: AADSTS7000215: Invalid client secret provided. Ensure the secret being sent in the request is the client secret value, not the client secret ID, for a secret added to app '1'. (403)",
		`<html>gateway</html>`: "<html>gateway</html> (403)",
		``:                     "Forbidden (403)",
	} {
		if got := answerMessage(http.StatusForbidden, []byte(raw)); got != want {
			t.Fatalf("answerMessage(%q) = %q; want %q", raw, got, want)
		}
	}
}

// AWS's documented example of a signed request, from "Create a signed AWS
// API request" in the IAM User Guide: GET iam.amazonaws.com/?Action=ListUsers
// &Version=2010-05-08 at 2015-08-30T12:36:00Z with the example key pair, and
// its documented canonical request hash, string to sign, signing key and
// signature; and the signing key example of "Deriving the signing key". A
// Secrets Manager call with a session token is checked against the signature
// the AWS SDK for Go computes for the same request.
func TestSigV4MatchesAWSsDocumentedExample(t *testing.T) {
	credentials := awsCredentials{accessKeyID: "AKIDEXAMPLE", secretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"}
	request, _ := http.NewRequest(http.MethodGet, "https://iam.amazonaws.com/?Action=ListUsers&Version=2010-05-08", nil)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	signed := signAWSV4(request, nil, credentials, "us-east-1", "iam", time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC))
	wantCanonical := "GET\n/\nAction=ListUsers&Version=2010-05-08\ncontent-type:application/x-www-form-urlencoded; charset=utf-8\nhost:iam.amazonaws.com\nx-amz-date:20150830T123600Z\n\ncontent-type;host;x-amz-date\ne3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if signed.canonicalRequest != wantCanonical {
		t.Fatalf("canonical request:\n%s\nwant:\n%s", signed.canonicalRequest, wantCanonical)
	}
	wantStringToSign := "AWS4-HMAC-SHA256\n20150830T123600Z\n20150830/us-east-1/iam/aws4_request\nf536975d06c0309214f805bb90ccff089219ecd68b2577efef23edd43b7e1a59"
	if signed.stringToSign != wantStringToSign {
		t.Fatalf("string to sign:\n%s\nwant:\n%s", signed.stringToSign, wantStringToSign)
	}
	if hex.EncodeToString(signed.signingKey) != "c4afb1cc5771d871763a393e44b703571b55cc28424d1a5e86da6ed3c154a4b9" {
		t.Fatalf("signing key = %x", signed.signingKey)
	}
	if signed.signature != "5d672d79c15b13162d9279b0855cfba6789a8edb4c82c400e06b5924a6f2b5d7" {
		t.Fatalf("signature = %s", signed.signature)
	}
	if got := request.Header.Get("Authorization"); got != "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/iam/aws4_request, SignedHeaders=content-type;host;x-amz-date, Signature=5d672d79c15b13162d9279b0855cfba6789a8edb4c82c400e06b5924a6f2b5d7" {
		t.Fatalf("Authorization = %s", got)
	}
	if got := hex.EncodeToString(signingKey(credentials.secretAccessKey, "20120215", "us-east-1", "iam")); got != "f4780e2d9f65fa895f9c67b32ce1baf0b0d8a43505a000a1a9e090d414db404d" {
		t.Fatalf("the 2012-02-15 signing key = %s", got)
	}

	body := []byte(`{"SecretId":"apps/web"}`)
	request, _ = http.NewRequest(http.MethodPost, "https://secretsmanager.eu-west-1.amazonaws.com/", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/x-amz-json-1.1")
	request.Header.Set("X-Amz-Target", "secretsmanager.GetSecretValue")
	credentials.sessionToken = "FwoGZXIvYXdzEXAMPLE"
	signed = signAWSV4(request, body, credentials, "eu-west-1", "secretsmanager", time.Date(2026, 10, 28, 9, 15, 42, 0, time.UTC))
	if signed.signedHeaders != "content-length;content-type;host;x-amz-date;x-amz-security-token;x-amz-target" ||
		signed.signature != "943259be30de3af1e86a5c9d15aa365dbab80251acfd392253d6cdd7510d7f11" ||
		request.Header.Get("X-Amz-Security-Token") != "FwoGZXIvYXdzEXAMPLE" || request.Header.Get("X-Amz-Date") != "20261028T091542Z" {
		t.Fatalf("the Secrets Manager request: headers %s, signature %s", signed.signedHeaders, signed.signature)
	}
	for text, want := range map[string]string{"a b": "a%20b", "é": "%C3%A9", "~-_.": "~-_.", "/": "%2F", "A=1&b": "A%3D1%26b"} {
		if got := uriEncode(text); got != want {
			t.Errorf("uriEncode(%q) = %q; want %q", text, got, want)
		}
	}
	if got := canonicalURI("/a b/c"); got != "/a%2520b/c" {
		t.Errorf("canonicalURI = %q", got)
	}
}

func awsFixture() *secretstoretest.AWS {
	return &secretstoretest.AWS{
		Region: "us-east-1", AccessKeyID: "AKIAFIXTUREKEY", SecretAccessKey: "fixture-secret-access-key",
		Secrets: map[string][]secretstoretest.AWSVersion{
			"apps/web": {
				{ID: "11111111-1111-4111-8111-111111111111", Stages: []string{"AWSPREVIOUS"}, String: `{"DATABASE_URL":"postgres://app:old@db/app","PORT":5432}`},
				{ID: "22222222-2222-4222-8222-222222222222", Stages: []string{"AWSCURRENT"}, String: `{"DATABASE_URL":"postgres://app:new@db/app","PORT":5433,"LIMITS":{"rps":5}}`},
			},
			"plain": {{ID: "33333333-3333-4333-8333-333333333333", Stages: []string{"AWSCURRENT"}, String: "just-a-string"}},
			"blob":  {{ID: "44444444-4444-4444-8444-444444444444", Stages: []string{"AWSCURRENT"}, Binary: []byte("bytes\x00here")}},
		},
	}
}

// Secrets Manager through an httptest server that follows its JSON 1.1
// protocol and checks every Signature Version 4: a JSON secret's fields, a
// whole secret, a staging label and a version id, a binary secret, one
// GetSecretValue per secret however many fields are referenced, and its own
// words for a missing secret, a wrong secret key and a wrong key id — with
// no credential in them.
func TestResolveReadsSecretsManagerFieldsWholeValuesStagesAndVersionIds(t *testing.T) {
	aws := awsFixture()
	server := httptest.NewServer(aws)
	defer server.Close()
	repo := newRepository(t)
	item, err := repo.Create(t.Context(), Write{Name: "aws", Kind: KindAWS, Address: server.URL, Settings: Settings{Region: "us-east-1"},
		Credentials: credentials(map[string]string{"accessKeyId": aws.AccessKeyID, "secretAccessKey": aws.SecretAccessKey})}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	reads, err := repo.Resolve(t.Context(), []Reference{
		{Store: "aws", Path: "apps/web", Key: "DATABASE_URL"},
		{Store: "aws", Path: "apps/web", Key: "PORT"},
		{Store: "aws", Path: "apps/web", Key: "LIMITS"},
		{Store: "aws", Path: "apps/web"},
		{Store: "aws", Path: "apps/web", Key: "DATABASE_URL", Version: "AWSPREVIOUS"},
		{Store: "aws", Path: "apps/web", Key: "PORT", Version: "11111111-1111-4111-8111-111111111111"},
		{Store: "aws", Path: "plain"},
		{Store: "aws", Path: "blob"},
	}, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	current, previous := "22222222-2222-4222-8222-222222222222", "11111111-1111-4111-8111-111111111111"
	for key, want := range map[string]Read{
		"aws/apps/web#DATABASE_URL":                              {Value: "postgres://app:new@db/app", Version: current},
		"aws/apps/web#PORT":                                      {Value: "5433", Version: current},
		"aws/apps/web#LIMITS":                                    {Value: `{"rps":5}`, Version: current},
		"aws/apps/web":                                           {Value: `{"DATABASE_URL":"postgres://app:new@db/app","PORT":5433,"LIMITS":{"rps":5}}`, Version: current},
		"aws/apps/web#DATABASE_URL@AWSPREVIOUS":                  {Value: "postgres://app:old@db/app", Version: previous},
		"aws/apps/web#PORT@11111111-1111-4111-8111-111111111111": {Value: "5432", Version: previous},
		"aws/plain":                                              {Value: "just-a-string", Version: "33333333-3333-4333-8333-333333333333"},
		"aws/blob":                                               {Value: "bytes\x00here", Version: "44444444-4444-4444-8444-444444444444"},
	} {
		if reads[key] != want {
			t.Fatalf("%s = %+v; want %+v", key, reads[key], want)
		}
	}
	// apps/web once for its three fields and the whole, once per pinned
	// version, plain and blob once each.
	if requests := strings.Join(aws.Requests(), ","); requests != "GetSecretValue apps/web,GetSecretValue apps/web,GetSecretValue apps/web,GetSecretValue plain,GetSecretValue blob" {
		t.Fatalf("requests = %v", requests)
	}
	for name, test := range map[string]struct {
		reference Reference
		want      string
	}{
		"a secret that is not":     {Reference{Store: "aws", Path: "apps/none"}, "secret store aws, apps/none: ResourceNotFoundException: Secrets Manager can't find the specified secret. (400)"},
		"a label that is not":      {Reference{Store: "aws", Path: "apps/web", Version: "AWSPENDING"}, "Secrets Manager can't find the specified secret value for staging label: AWSPENDING (400)"},
		"a field of a plain value": {Reference{Store: "aws", Path: "plain", Key: "value"}, "plain has no field value: its value is not a JSON object with that key"},
		"a field that is not":      {Reference{Store: "aws", Path: "apps/web", Key: "NOPE", Version: "AWSPREVIOUS"}, "version AWSPREVIOUS of apps/web has no field NOPE"},
	} {
		_, err := repo.Resolve(t.Context(), []Reference{test.reference}, ResolveOptions{})
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("%s = %v; want %q", name, err, test.want)
		}
	}
	result, err := repo.Test(t.Context(), item.ID, &Probe{Path: "apps/web", Key: "DATABASE_URL"})
	if err != nil || !result.OK || result.Version != current ||
		result.Message != "Signed in to Secrets Manager in us-east-1; the first secret listed is apps/web; found apps/web#DATABASE_URL, version "+current {
		t.Fatalf("test = %+v, %v", result, err)
	}
	result, err = repo.Test(t.Context(), item.ID, &Probe{Path: "plain"})
	if err != nil || !result.OK || !strings.HasSuffix(result.Message, "; found plain, version 33333333-3333-4333-8333-333333333333") {
		t.Fatalf("a probe without a key = %+v, %v", result, err)
	}
	// An identity allowed only to read named secrets is signed in all the
	// same, and the probe shows what it can read.
	aws.ListDenied = true
	result, err = repo.Test(t.Context(), item.ID, &Probe{Path: "plain"})
	if err != nil || !result.OK || !strings.Contains(result.Message, "this identity may not list secrets (AccessDeniedException: User: arn:aws:iam::123456789012:user/just-dashboard is not authorized to perform: secretsmanager:ListSecrets") ||
		!strings.HasSuffix(result.Message, "found plain, version 33333333-3333-4333-8333-333333333333") {
		t.Fatalf("a test without the list permission = %+v, %v", result, err)
	}
	aws.ListDenied = false

	// A wrong secret key is a signature that does not match; a wrong key id
	// is a client the service does not know. Neither answer carries the key.
	if _, _, err := repo.Update(t.Context(), item.ID, Write{Address: server.URL, Settings: Settings{Region: "us-east-1"},
		Credentials: credentials(map[string]string{"accessKeyId": aws.AccessKeyID, "secretAccessKey": "wrong-secret-access-key"})}, "operator"); err != nil {
		t.Fatal(err)
	}
	_, err = repo.Resolve(t.Context(), []Reference{{Store: "aws", Path: "plain"}}, ResolveOptions{})
	if err == nil || !strings.Contains(err.Error(), "InvalidSignatureException: The request signature we calculated does not match the signature you provided.") ||
		!strings.Contains(err.Error(), "(403)") || strings.Contains(err.Error(), "wrong-secret") || strings.Contains(err.Error(), aws.AccessKeyID) {
		t.Fatalf("a wrong secret key = %v", err)
	}
	if _, _, err := repo.Update(t.Context(), item.ID, Write{Address: server.URL, Settings: Settings{Region: "us-east-1"},
		Credentials: credentials(map[string]string{"accessKeyId": "AKIANOBODY", "secretAccessKey": aws.SecretAccessKey})}, "operator"); err != nil {
		t.Fatal(err)
	}
	result, err = repo.Test(t.Context(), item.ID, nil)
	if err != nil || result.OK || result.Message != "UnrecognizedClientException: The security token included in the request is invalid. (403)" {
		t.Fatalf("a wrong key id = %+v, %v", result, err)
	}

	// Temporary credentials carry their session token, signed.
	aws.SessionToken = "FwoGZXIvYXdzEXAMPLE-session"
	if _, _, err := repo.Update(t.Context(), item.ID, Write{Address: server.URL, Settings: Settings{Region: "us-east-1"},
		Credentials: credentials(map[string]string{"accessKeyId": aws.AccessKeyID, "secretAccessKey": aws.SecretAccessKey})}, "operator"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Resolve(t.Context(), []Reference{{Store: "aws", Path: "plain"}}, ResolveOptions{}); err == nil || !strings.Contains(err.Error(), "UnrecognizedClientException") {
		t.Fatalf("without the session token = %v", err)
	}
	if _, _, err := repo.Update(t.Context(), item.ID, Write{Address: server.URL, Settings: Settings{Region: "us-east-1"},
		Credentials: credentials(map[string]string{"accessKeyId": aws.AccessKeyID, "secretAccessKey": aws.SecretAccessKey, "sessionToken": aws.SessionToken})}, "operator"); err != nil {
		t.Fatal(err)
	}
	reads, err = repo.Resolve(t.Context(), []Reference{{Store: "aws", Path: "plain"}}, ResolveOptions{})
	if err != nil || reads["aws/plain"].Value != "just-a-string" {
		t.Fatalf("with the session token = %+v, %v", reads, err)
	}
	saved, _ := repo.Get(t.Context(), item.ID)
	if strings.Join(saved.CredentialNames, ",") != "accessKeyId,secretAccessKey,sessionToken" {
		t.Fatalf("credential names = %v", saved.CredentialNames)
	}
}

func azureFixture() *secretstoretest.Azure {
	return &secretstoretest.Azure{
		TenantID: "0a1b2c3d-0000-4000-8000-000000000001", ClientID: "9f8e7d6c-0000-4000-8000-000000000002", ClientSecret: "fixture-client-secret",
		Secrets: map[string][]secretstoretest.AzureVersion{
			"database-url": {{ID: "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1", Value: "postgres://app:old@db/app"}, {ID: "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2", Value: "postgres://app:new@db/app"}},
			"app-config":   {{ID: "c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3", Value: `{"API_KEY":"key-1","PORT":8080}`}},
		},
	}
}

// Key Vault through an httptest server that follows its REST API and Entra's
// token endpoint: a whole secret, a pinned version, a field of a JSON value,
// one token for every read of a run and the next run, a new token when the
// client secret is rotated or the token is about to expire, and its own
// words — Entra's error and description without the trace ids, the vault's
// code and message without the help link — for refusals.
func TestResolveReadsKeyVaultSecretsAndCachesTheTokenUntilItExpires(t *testing.T) {
	azure := azureFixture()
	server := httptest.NewServer(azure)
	defer server.Close()
	repo := newRepository(t)
	settings := Settings{VaultName: "jdtest", TenantID: azure.TenantID, Authority: server.URL}
	item, err := repo.Create(t.Context(), Write{Name: "kv", Kind: KindAzure, Address: server.URL, Settings: settings,
		Credentials: credentials(map[string]string{"clientId": azure.ClientID, "clientSecret": azure.ClientSecret})}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	references := []Reference{
		{Store: "kv", Path: "database-url"},
		{Store: "kv", Path: "database-url", Version: "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1"},
		{Store: "kv", Path: "app-config", Key: "API_KEY"},
		{Store: "kv", Path: "app-config", Key: "PORT"},
		{Store: "kv", Path: "app-config"},
	}
	reads, err := repo.Resolve(t.Context(), references, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]Read{
		"kv/database-url": {Value: "postgres://app:new@db/app", Version: "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2"},
		"kv/database-url@a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1": {Value: "postgres://app:old@db/app", Version: "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1"},
		"kv/app-config#API_KEY":                            {Value: "key-1", Version: "c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3"},
		"kv/app-config#PORT":                               {Value: "8080", Version: "c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3"},
		"kv/app-config":                                    {Value: `{"API_KEY":"key-1","PORT":8080}`, Version: "c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3"},
	} {
		if reads[key] != want {
			t.Fatalf("%s = %+v; want %+v", key, reads[key], want)
		}
	}
	if requests := azure.Requests(); len(requests) != 3 || azure.Tokens() != 1 {
		t.Fatalf("one read per secret and version and one token; got %v, %d tokens", requests, azure.Tokens())
	}
	// The next run reads its values afresh with the same token.
	if _, err := repo.Resolve(t.Context(), references[:1], ResolveOptions{}); err != nil || azure.Tokens() != 1 {
		t.Fatalf("the second run: %v, %d tokens", err, azure.Tokens())
	}
	// A rotated client secret is another sign-in; and a token with less than
	// a minute left is not reused, so Entra's short-lived one here is asked
	// for again at every run.
	azure.ClientSecret = "rotated-client-secret"
	azure.ExpiresIn = 30
	if _, _, err := repo.Update(t.Context(), item.ID, Write{Address: server.URL, Settings: settings,
		Credentials: credentials(map[string]string{"clientId": azure.ClientID, "clientSecret": "rotated-client-secret"})}, "operator"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Resolve(t.Context(), references[:1], ResolveOptions{}); err != nil || azure.Tokens() != 2 {
		t.Fatalf("after a rotation: %v, %d tokens", err, azure.Tokens())
	}
	for round := 3; round <= 4; round++ {
		if _, err := repo.Resolve(t.Context(), references[:1], ResolveOptions{}); err != nil || azure.Tokens() != round {
			t.Fatalf("a short-lived token, round %d: %v, %d tokens", round, err, azure.Tokens())
		}
	}
	azure.ExpiresIn = 0

	for name, test := range map[string]struct {
		reference Reference
		want      string
	}{
		"a secret that is not":      {Reference{Store: "kv", Path: "nope"}, "secret store kv, nope: SecretNotFound: A secret with (name/id) nope was not found in this key vault. If you recently deleted this secret you may be able to recover it using the correct recovery command. (404)"},
		"a version that is not":     {Reference{Store: "kv", Path: "database-url", Version: "d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4"}, "SecretNotFound: A secret with (name/id) database-url/d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4 was not found"},
		"a field of a plain value":  {Reference{Store: "kv", Path: "database-url", Key: "host"}, "database-url has no field host: its value is not a JSON object with that key"},
		"a version in Vault's form": {Reference{Store: "kv", Path: "database-url", Version: "3"}, "a Key Vault version is its 32 hexadecimal characters"},
	} {
		_, err := repo.Resolve(t.Context(), []Reference{test.reference}, ResolveOptions{})
		if err == nil || !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), "linkid=") {
			t.Fatalf("%s = %v; want %q", name, err, test.want)
		}
	}
	result, err := repo.Test(t.Context(), item.ID, &Probe{Path: "database-url"})
	if err != nil || !result.OK || result.Version != "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2" ||
		!strings.HasPrefix(result.Message, "Signed in to Key Vault jdtest in tenant "+azure.TenantID+"; the first secret listed is ") ||
		!strings.HasSuffix(result.Message, "; found database-url, version b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2") {
		t.Fatalf("test = %+v, %v", result, err)
	}
	azure.ListDenied = true
	result, err = repo.Test(t.Context(), item.ID, &Probe{Path: "app-config", Key: "API_KEY"})
	if err != nil || !result.OK || !strings.Contains(result.Message, "this application may not list secrets (Forbidden: The user, group or application 'appid="+azure.ClientID+"' does not have secrets list permission on key vault 'jdtest;location=westeurope'. (403))") ||
		!strings.HasSuffix(result.Message, "found app-config#API_KEY, version c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3") || strings.Contains(result.Message, "linkid=") {
		t.Fatalf("a test without the list permission = %+v, %v", result, err)
	}
	azure.ListDenied = false

	// A wrong client secret is refused by Entra in its words, without the
	// trace ids and without the secret.
	if _, _, err := repo.Update(t.Context(), item.ID, Write{Address: server.URL, Settings: settings,
		Credentials: credentials(map[string]string{"clientId": azure.ClientID, "clientSecret": "wrong-client-secret"})}, "operator"); err != nil {
		t.Fatal(err)
	}
	// Entra names the application in its answer; the client ID is one of the
	// connection's credentials, so it is masked like any other.
	result, err = repo.Test(t.Context(), item.ID, nil)
	if err != nil || result.OK || result.Message != "Entra sign-in refused: invalid_client: AADSTS7000215: Invalid client secret provided. Ensure the secret being sent in the request is the client secret value, not the client secret ID, for a secret added to app '••••'. (401)" {
		t.Fatalf("a wrong client secret = %+v, %v", result, err)
	}
	if _, _, err := repo.Update(t.Context(), item.ID, Write{Address: server.URL, Settings: Settings{VaultName: "jdtest", TenantID: "other-tenant", Authority: server.URL},
		Credentials: credentials(map[string]string{"clientId": azure.ClientID, "clientSecret": azure.ClientSecret})}, "operator"); err != nil {
		t.Fatal(err)
	}
	_, err = repo.Resolve(t.Context(), references[:1], ResolveOptions{})
	if err == nil || !strings.Contains(err.Error(), "Entra sign-in refused: invalid_request: AADSTS90002: Tenant 'other-tenant' not found.") || strings.Contains(err.Error(), "Trace ID") {
		t.Fatalf("a wrong tenant = %v", err)
	}
	saved, _ := repo.Get(t.Context(), item.ID)
	if saved.LastRead == nil || saved.LastRead.OK || !strings.Contains(saved.LastRead.Message, "AADSTS90002") {
		t.Fatalf("the refusal was not recorded: %+v", saved.LastRead)
	}
}
