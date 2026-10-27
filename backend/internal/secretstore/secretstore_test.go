package secretstore

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
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
		"vault-prod/apps/web#DATABASE_URL": {Store: "vault-prod", Path: "apps/web", Key: "DATABASE_URL"},
		"infisical#API_KEY":                {Store: "infisical", Key: "API_KEY"},
		"bao/a/b.c/d_e#key.with-dots@12":   {Store: "bao", Path: "a/b.c/d_e", Key: "key.with-dots", Version: 12},
		"doppler#STRIPE_KEY":               {Store: "doppler", Key: "STRIPE_KEY"},
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
		"vault/apps/web", "vault/apps/web#", "#KEY", "Vault/apps#KEY", "vault/../etc#KEY", "vault/apps/./web#KEY",
		"vault//apps#KEY", "vault/apps/#KEY", "vault/apps#K#Y", "vault/apps#KEY@0", "vault/apps#KEY@01", "vault/apps#KEY@x",
		"vault/apps#KE Y", "vault/ap ps#KEY", "vault/apps#KEY@-1", "-vault#KEY", strings.Repeat("a", 301),
	} {
		if _, err := ParseReference(target); err == nil {
			t.Fatalf("ParseReference(%q) was accepted", target)
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
		{Store: "bao", Path: "apps/web", Key: "DATABASE_URL", Version: 1},
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
		"a missing key":           {Reference{Store: "good", Path: "apps/web", Key: "B", Version: 1}, ResolveOptions{}, "version 1 of secret/apps/web has no key B"},
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
		{Store: "infisical", Path: "backend", Key: "SESSION_SECRET", Version: 1},
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
	if err := repo.Check(t.Context(), Reference{Store: "doppler", Key: "B", Version: 2}, false); !errors.Is(err, ErrInvalid) {
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
		`{"errors":["1 error occurred:\n\t* permission denied\n\n"]}`:                    "permission denied (403)",
		`{"messages":["Invalid Service token"],"success":false}`:                         "Invalid Service token (403)",
		`{"statusCode":403,"message":"You are not allowed","error":"PermissionDenied"}`:  "You are not allowed (403)",
		`{"message":[{"message":"Slug field must be at least 5"}],"error":"Validation"}`: "Slug field must be at least 5 (403)",
		`<html>gateway</html>`: "<html>gateway</html> (403)",
		``:                     "Forbidden (403)",
	} {
		if got := answerMessage(http.StatusForbidden, []byte(raw)); got != want {
			t.Fatalf("answerMessage(%q) = %q; want %q", raw, got, want)
		}
	}
}
