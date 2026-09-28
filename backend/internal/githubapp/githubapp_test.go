package githubapp

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	basestore "github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

func testKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

// fakeGitHub is the handful of endpoints the App uses, with the bookkeeping
// the tests assert on.
type fakeGitHub struct {
	mu            sync.Mutex
	tokenRequests int
	statuses      []map[string]any
	comments      []map[string]any
	patched       map[int64]string
	authorization []string
	installations []int64
	repositories  map[int64][]string
	existingBody  string
	// pullQueries records the query string of every pull request listing;
	// checksForbidden answers the check-runs read the way GitHub does for an
	// App without checks:read.
	pullQueries     []string
	checksForbidden bool
}

// fakePull is a pull request in GitHub's REST shape. A nil head repository
// is a fork that was deleted after the pull request was opened.
func fakePull(number int, head, base string, headRepo any, extra map[string]any) map[string]any {
	item := map[string]any{
		"number": number, "title": fmt.Sprintf("Change %d", number), "html_url": fmt.Sprintf("https://github.com/acme/shop/pull/%d", number),
		"state": "open", "draft": number == 2, "merged_at": nil,
		"head": map[string]any{"ref": head, "sha": strings.Repeat(fmt.Sprint(number), 40)[:40], "repo": headRepo},
		"base": map[string]any{"ref": base, "sha": strings.Repeat("b", 40), "repo": map[string]any{"full_name": "acme/shop"}},
		"user": map[string]any{"login": "zed"}, "created_at": "2026-09-18T11:00:00Z", "updated_at": "2026-09-19T11:00:00Z",
		"labels": []map[string]any{{"name": "bug"}, {"name": ""}}, "body": nil,
	}
	for key, value := range extra {
		item[key] = value
	}
	return item
}

func (f *fakeGitHub) handler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	record := func(r *http.Request) {
		f.mu.Lock()
		f.authorization = append(f.authorization, r.Header.Get("Authorization"))
		f.mu.Unlock()
	}
	mux.HandleFunc("POST /app-manifests/{code}/conversions", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("code") != "one-time-code" {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		_, pemKey := testKey(t)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 4242, "slug": "just-dashboard-test", "name": "Just Dashboard test", "client_id": "Iv1.client",
			"client_secret": "client-secret", "webhook_secret": "hook-secret", "pem": pemKey,
			"html_url": "https://github.com/apps/just-dashboard-test", "owner": map[string]any{"login": "acme"},
		})
	})
	mux.HandleFunc("GET /app/installations", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		page := r.URL.Query().Get("page")
		if page == "" {
			w.Header().Set("Link", fmt.Sprintf(`<http://%s/app/installations?per_page=100&page=2>; rel="next"`, r.Host))
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": f.installations[0], "account": map[string]any{"login": "zed", "type": "User", "html_url": "https://github.com/zed"}, "html_url": "https://github.com/settings/installations/1", "repository_selection": "selected"}})
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": f.installations[1], "account": map[string]any{"login": "acme", "type": "Organization"}, "html_url": "https://github.com/organizations/acme/settings/installations/2", "repository_selection": "all"}})
	})
	mux.HandleFunc("POST /app/installations/{id}/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		f.mu.Lock()
		f.tokenRequests++
		n := f.tokenRequests
		f.mu.Unlock()
		if r.PathValue("id") == "404" {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"token": fmt.Sprintf("ghs_token_%s_%d", r.PathValue("id"), n), "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	})
	mux.HandleFunc("GET /installation/repositories", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ghs_token_")
		id := strings.Split(token, "_")[0]
		var installation int64
		_, _ = fmt.Sscan(id, &installation)
		items := []map[string]any{}
		for _, name := range f.repositories[installation] {
			items = append(items, map[string]any{"full_name": name, "name": strings.Split(name, "/")[1], "private": true, "default_branch": "main", "clone_url": "https://github.com/" + name + ".git", "html_url": "https://github.com/" + name, "language": "Go", "pushed_at": "2026-09-18T11:00:00Z", "fork": name == "acme/api", "archived": false})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"total_count": len(items), "repositories": items})
	})
	mux.HandleFunc("GET /repos/{owner}/{repo}/installation", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		for id, names := range f.repositories {
			for _, name := range names {
				if name == r.PathValue("owner")+"/"+r.PathValue("repo") {
					_ = json.NewEncoder(w).Encode(map[string]any{"id": id})
					return
				}
			}
		}
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	})
	mux.HandleFunc("GET /repos/{owner}/{repo}/pulls", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		f.mu.Lock()
		f.pullQueries = append(f.pullQueries, r.URL.RawQuery)
		f.mu.Unlock()
		if r.URL.Query().Get("page") == "" {
			w.Header().Set("Link", fmt.Sprintf(`<http://%s/repos/%s/%s/pulls?state=%s&per_page=100&page=2>; rel="next"`, r.Host, r.PathValue("owner"), r.PathValue("repo"), r.URL.Query().Get("state")))
			_ = json.NewEncoder(w).Encode([]map[string]any{
				fakePull(1, "feature", "main", map[string]any{"full_name": "ACME/shop"}, nil),
				fakePull(2, "patch-1", "main", map[string]any{"full_name": "zed/shop"}, nil),
			})
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			fakePull(3, "gone", "main", nil, map[string]any{"state": "closed", "merged_at": "2026-09-19T12:00:00Z"}),
		})
	})
	mux.HandleFunc("GET /repos/{owner}/{repo}/pulls/{number}", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		switch r.PathValue("number") {
		case "1":
			_ = json.NewEncoder(w).Encode(fakePull(1, "feature", "main", map[string]any{"full_name": "acme/shop"}, map[string]any{"mergeable": true, "merged": false, "comments": 4, "additions": 10, "deletions": 2, "changed_files": 3, "body": "Adds the thing."}))
		case "2":
			_ = json.NewEncoder(w).Encode(fakePull(2, "patch-1", "main", map[string]any{"full_name": "zed/shop"}, map[string]any{"mergeable": nil, "merged": false}))
		case "3":
			_ = json.NewEncoder(w).Encode(fakePull(3, "gone", "main", nil, map[string]any{"state": "closed", "mergeable": false, "merged": true, "merged_at": "2026-09-19T12:00:00Z"}))
		default:
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		}
	})
	mux.HandleFunc("GET /repos/{owner}/{repo}/commits/{sha}/check-runs", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if f.checksForbidden {
			http.Error(w, `{"message":"Resource not accessible by integration"}`, http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 3, "check_runs": []map[string]any{
			{"name": "unit", "status": "completed", "conclusion": "success", "details_url": "https://github.com/acme/shop/runs/1", "html_url": "https://github.com/acme/shop/runs/1", "started_at": "2026-09-19T11:00:00Z", "completed_at": "2026-09-19T11:05:00Z", "app": map[string]any{"slug": "github-actions", "name": "GitHub Actions"}},
			{"name": "lint", "status": "in_progress", "conclusion": nil, "details_url": "javascript:alert(1)", "html_url": "https://github.com/acme/shop/runs/2", "started_at": "2026-09-19T11:00:00Z", "completed_at": nil, "app": map[string]any{"slug": "linter", "name": ""}},
			{"name": "e2e", "status": "waiting", "conclusion": nil, "details_url": "not a url", "html_url": "", "app": nil},
		}})
	})
	mux.HandleFunc("GET /repos/{owner}/{repo}/commits/{sha}/status", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{"state": "failure", "statuses": []map[string]any{
			{"state": "failure", "context": "ci/build", "target_url": "https://ci.example.test/1", "created_at": "2026-09-19T11:00:00Z", "updated_at": "2026-09-19T11:02:00Z"},
			{"state": "pending", "context": "deploy/preview", "target_url": "", "created_at": "2026-09-19T11:00:00Z", "updated_at": "2026-09-19T11:00:00Z"},
			{"state": "success", "context": "ci/audit", "target_url": "ftp://ci.example.test/2", "created_at": "2026-09-19T11:00:00Z", "updated_at": "2026-09-19T11:03:00Z"},
		}})
	})
	mux.HandleFunc("POST /repos/{owner}/{repo}/statuses/{sha}", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["sha"], body["repo"] = r.PathValue("sha"), r.PathValue("owner")+"/"+r.PathValue("repo")
		f.mu.Lock()
		f.statuses = append(f.statuses, body)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":1}`)
	})
	mux.HandleFunc("GET /repos/{owner}/{repo}/issues/{number}/comments", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		items := []map[string]any{{"id": 1, "body": "unrelated comment"}}
		if f.existingBody != "" {
			items = append(items, map[string]any{"id": 77, "body": f.existingBody})
		}
		_ = json.NewEncoder(w).Encode(items)
	})
	mux.HandleFunc("POST /repos/{owner}/{repo}/issues/{number}/comments", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["number"] = r.PathValue("number")
		f.mu.Lock()
		f.comments = append(f.comments, body)
		f.existingBody = body["body"].(string)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":78}`)
	})
	mux.HandleFunc("PATCH /repos/{owner}/{repo}/issues/comments/{id}", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		if f.patched == nil {
			f.patched = map[int64]string{}
		}
		var id int64
		_, _ = fmt.Sscan(r.PathValue("id"), &id)
		f.patched[id] = body["body"].(string)
		f.mu.Unlock()
		_, _ = io.WriteString(w, `{"id":77}`)
	})
	return mux
}

func newFake(t *testing.T) (*fakeGitHub, *httptest.Server) {
	t.Helper()
	fake := &fakeGitHub{installations: []int64{1, 2}, repositories: map[int64][]string{1: {"zed/blog"}, 2: {"acme/shop", "acme/api"}}}
	server := httptest.NewServer(fake.handler(t))
	t.Cleanup(server.Close)
	return fake, server
}

func newTestClient(t *testing.T, server *httptest.Server) (*Client, *rsa.PrivateKey) {
	t.Helper()
	key, pemKey := testKey(t)
	client, err := NewClient(Credentials{ID: 4242, ClientID: "Iv1.client", PrivateKey: pemKey})
	if err != nil {
		t.Fatal(err)
	}
	client.APIURL = server.URL
	return client, key
}

// The JWT is what GitHub trusts the App by: RS256 over the App's own key,
// issued a minute early, good for nine more.
func TestJWTIsSignedWithTheAppKeyAndBounded(t *testing.T) {
	_, pemKey := testKey(t)
	client, err := NewClient(Credentials{ID: 4242, ClientID: "Iv1.client", PrivateKey: pemKey})
	if err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	client.now = func() time.Time { return fixed }
	token, err := client.JWT()
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token = %q", token)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&client.key.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
		t.Fatalf("signature does not verify: %v", err)
	}
	claims := map[string]any{}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	if claims["iss"] != "Iv1.client" || int64(claims["iat"].(float64)) != fixed.Unix()-60 || int64(claims["exp"].(float64)) != fixed.Unix()+540 {
		t.Fatalf("claims = %v", claims)
	}
	if _, err := NewClient(Credentials{PrivateKey: "not a key"}); err == nil {
		t.Fatal("a non-PEM key was accepted")
	}
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(client.key)
	if _, err := NewClient(Credentials{PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}))}); err != nil {
		t.Fatalf("PKCS#8 refused: %v", err)
	}
}

func TestInstallationTokensAreMintedOncePerHourAndListingsFollowPages(t *testing.T) {
	fake, server := newFake(t)
	client, _ := newTestClient(t, server)
	now := time.Now()
	client.now = func() time.Time { return now }
	ctx := context.Background()
	first, err := client.InstallationToken(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	again, err := client.InstallationToken(ctx, 2)
	if err != nil || again != first || fake.tokenRequests != 1 {
		t.Fatalf("second call minted again: %q %q %d %v", first, again, fake.tokenRequests, err)
	}
	// Two minutes before expiry the token is replaced, not handed out.
	now = now.Add(59 * time.Minute)
	refreshed, err := client.InstallationToken(ctx, 2)
	if err != nil || refreshed == first || fake.tokenRequests != 2 {
		t.Fatalf("token was not refreshed near expiry: %q %d %v", refreshed, fake.tokenRequests, err)
	}
	if _, err := client.InstallationToken(ctx, 404); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("missing installation: %v", err)
	}
	installations, err := client.Installations(ctx)
	if err != nil || len(installations) != 2 || installations[0].Account != "zed" || installations[1].Account != "acme" || installations[1].AccountType != "Organization" {
		t.Fatalf("installations = %+v, %v", installations, err)
	}
	repositories, err := client.InstallationRepositories(ctx, installations[1])
	if err != nil || len(repositories) != 2 || repositories[0].NameWithOwner != "acme/shop" || !repositories[0].Private || repositories[0].CloneURL != "https://github.com/acme/shop.git" || repositories[0].InstallationID != 2 {
		t.Fatalf("repositories = %+v, %v", repositories, err)
	}
	// The import picker sorts by the last push and marks a fork, so both
	// travel with the listing rather than being dropped on the way through.
	if repositories[0].PushedAt != "2026-09-18T11:00:00Z" || repositories[0].Fork || !repositories[1].Fork || repositories[0].Archived {
		t.Fatalf("recency and fork were not carried: %+v", repositories)
	}
	// Listing installations authenticates as the App; listing what one grants
	// authenticates as the installation.
	if !strings.HasPrefix(fake.authorization[len(fake.authorization)-1], "Bearer ghs_token_2_") {
		t.Fatalf("repositories were listed with %q", fake.authorization[len(fake.authorization)-1])
	}
}

func TestStatusesAndPullRequestCommentsGoThroughTheGrantingInstallation(t *testing.T) {
	fake, server := newFake(t)
	client, _ := newTestClient(t, server)
	ctx := context.Background()
	installation, err := client.RepositoryInstallation(ctx, "acme/shop")
	if err != nil || installation != 2 {
		t.Fatalf("installation = %d, %v", installation, err)
	}
	if _, err := client.RepositoryInstallation(ctx, "nobody/nothing"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("ungranted repository: %v", err)
	}
	if _, err := client.RepositoryInstallation(ctx, "../etc"); err == nil {
		t.Fatal("a path-shaped repository was accepted")
	}
	sha := strings.Repeat("a", 40)
	if err := client.PostCommitStatus(ctx, installation, "acme/shop", sha, CommitStatus{State: "success", Context: "just-dashboard/production", Description: strings.Repeat("d", 150), TargetURL: "https://dash.example.test/deploy/1/runs/2"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.statuses) != 1 || fake.statuses[0]["state"] != "success" || fake.statuses[0]["sha"] != sha || len(fake.statuses[0]["description"].(string)) != 140 || fake.statuses[0]["target_url"] != "https://dash.example.test/deploy/1/runs/2" {
		t.Fatalf("status = %+v", fake.statuses)
	}
	if err := client.PostCommitStatus(ctx, installation, "acme/shop", sha, CommitStatus{State: "unknown"}); err == nil {
		t.Fatal("an invalid state was posted")
	}
	if err := client.PostCommitStatus(ctx, installation, "acme/shop", "short", CommitStatus{State: "success"}); err == nil {
		t.Fatal("a short sha was posted")
	}
	marker := "<!-- just-dashboard:preview:9 -->"
	if err := client.UpsertPullRequestComment(ctx, installation, "acme/shop", 12, marker, "Preview is building."); err != nil {
		t.Fatal(err)
	}
	if len(fake.comments) != 1 || fake.comments[0]["number"] != "12" || !strings.HasPrefix(fake.comments[0]["body"].(string), marker+"\n") {
		t.Fatalf("comment = %+v", fake.comments)
	}
	if err := client.UpsertPullRequestComment(ctx, installation, "acme/shop", 12, marker, "Preview is ready."); err != nil {
		t.Fatal(err)
	}
	if len(fake.comments) != 1 || fake.patched[77] != marker+"\nPreview is ready." {
		t.Fatalf("second report did not edit the first comment: %+v %+v", fake.comments, fake.patched)
	}
	if err := client.UpsertPullRequestComment(ctx, installation, "acme/shop", 0, marker, "x"); err == nil {
		t.Fatal("a comment without a pull request number was accepted")
	}
}

type credentialSyncFake struct {
	ensured map[int64]string
	removed bool
}

func (f *credentialSyncFake) EnsureGitHubAppCredential(_ context.Context, installationID int64, account string) (int64, error) {
	if f.ensured == nil {
		f.ensured = map[int64]string{}
	}
	f.ensured[installationID] = account
	return installationID + 100, nil
}

func (f *credentialSyncFake) RemoveGitHubAppCredentials(context.Context) error {
	f.removed = true
	return nil
}

func newTestService(t *testing.T, server *httptest.Server) (*Service, *credentialSyncFake) {
	t.Helper()
	st, err := basestore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sealer, err := auth.NewSealer(strings.Repeat("c3", 32))
	if err != nil {
		t.Fatal(err)
	}
	sync := &credentialSyncFake{}
	service := New(NewStore(st.DB, sealer), sync)
	service.APIURL, service.WebURL = server.URL, "https://github.example.test"
	return service, sync
}

// The manifest flow: a state the dashboard issued, a code exchanged once, the
// App stored sealed and immediately usable, and the installations' deploy
// credentials kept in step.
func TestServiceManifestFlowStoresTheAppSealedAndKeepsCredentialsInStep(t *testing.T) {
	fake, server := newFake(t)
	service, sync := newTestService(t, server)
	ctx := context.Background()
	if status, err := service.Status(ctx, "https://dash.example.test"); err != nil || status.Configured || len(status.Installations) != 0 {
		t.Fatalf("empty status = %+v, %v", status, err)
	}
	if _, err := service.BeginManifest("http://dash.example.test", ManifestRequest{}); err == nil {
		t.Fatal("a plain-HTTP dashboard address was accepted")
	}
	if _, err := service.BeginManifest("https://dash.example.test", ManifestRequest{Name: "bad/name"}); err == nil {
		t.Fatal("an invalid App name was accepted")
	}
	start, err := service.BeginManifest("https://dash.example.test/", ManifestRequest{Organization: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if start.Action != "https://github.example.test/organizations/acme/settings/apps/new?state="+start.State ||
		start.Manifest.HookAttributes["url"] != "https://dash.example.test/api/v1/hooks/github-app" ||
		start.Manifest.RedirectURL != "https://dash.example.test/deploy/credentials" ||
		start.Manifest.DefaultPermissions["pull_requests"] != "write" || start.Manifest.DefaultPermissions["contents"] != "read" ||
		start.Manifest.DefaultPermissions["checks"] != "read" ||
		!strings.HasPrefix(start.Manifest.Name, "Just Dashboard ") || start.Manifest.Public {
		t.Fatalf("manifest start = %+v", start)
	}
	if _, err := service.CompleteManifest(ctx, "one-time-code", "not-issued"); !errors.Is(err, ErrBadState) {
		t.Fatalf("foreign state: %v", err)
	}
	if _, err := service.CompleteManifest(ctx, "wrong-code", start.State); err == nil {
		t.Fatal("a bad code was accepted")
	}
	// The state was spent by the failed exchange; start again.
	start, err = service.BeginManifest("https://dash.example.test", ManifestRequest{})
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := service.CompleteManifest(ctx, "one-time-code", start.State)
	if err != nil {
		t.Fatal(err)
	}
	if credentials.ID != 4242 || credentials.Slug != "just-dashboard-test" || credentials.Owner != "acme" || credentials.WebhookSecret != "hook-secret" {
		t.Fatalf("credentials = %+v", credentials)
	}
	if secret, err := service.WebhookSecret(ctx); err != nil || secret != "hook-secret" {
		t.Fatalf("webhook secret = %q, %v", secret, err)
	}
	// Sealed at rest: the private key and secrets never appear in the row.
	var stored string
	if err := service.store.db.QueryRow(`SELECT secret_enc FROM github_app`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "PRIVATE KEY") || strings.Contains(stored, "hook-secret") {
		t.Fatal("secrets stored in clear")
	}
	status, err := service.Status(ctx, "https://dash.example.test")
	if err != nil || !status.Configured || status.App.Slug != "just-dashboard-test" || status.Error != "" ||
		status.InstallURL != "https://github.example.test/apps/just-dashboard-test/installations/new" ||
		status.WebhookURL != "https://dash.example.test/api/v1/hooks/github-app" ||
		len(status.Installations) != 2 || status.Installations[0].Account != "acme" || status.Installations[0].CredentialID != 102 {
		t.Fatalf("status = %+v, %v", status, err)
	}
	if sync.ensured[1] != "zed" || sync.ensured[2] != "acme" {
		t.Fatalf("credentials ensured = %v", sync.ensured)
	}
	repositories, err := service.Repositories(ctx)
	if err != nil || len(repositories) != 3 || repositories[0].NameWithOwner != "acme/api" || repositories[0].CredentialID != 102 || repositories[2].NameWithOwner != "zed/blog" || repositories[2].CredentialID != 101 {
		t.Fatalf("repositories = %+v, %v", repositories, err)
	}
	// The status page polls; GitHub is asked for installations once a minute.
	listed := len(fake.authorization)
	if _, err := service.Status(ctx, "https://dash.example.test"); err != nil {
		t.Fatal(err)
	}
	if len(fake.authorization) != listed {
		t.Fatal("a second status read went to GitHub inside the cache window")
	}
	if err := service.PostCommitStatus(ctx, "acme/shop", strings.Repeat("b", 40), CommitStatus{State: "pending", Context: "just-dashboard/staging"}); err != nil {
		t.Fatal(err)
	}
	if err := service.UpsertPullRequestComment(ctx, "zed/blog", 3, "<!-- m -->", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := service.PostCommitStatus(ctx, "nobody/nothing", strings.Repeat("b", 40), CommitStatus{State: "pending"}); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("ungranted repository status: %v", err)
	}
	if err := service.Disconnect(ctx); err != nil || !sync.removed {
		t.Fatalf("disconnect: %v removed=%v", err, sync.removed)
	}
	if _, err := service.Client(ctx); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("client after disconnect: %v", err)
	}
	if status, err := service.Status(ctx, ""); err != nil || status.Configured {
		t.Fatalf("status after disconnect = %+v, %v", status, err)
	}
}

// A configured App whose GitHub side is broken still reads as configured,
// with the failure beside it, so the page can offer "reconnect" rather than
// pretending nothing was ever set up.
func TestStatusReportsAGitHubFailureBesideAConfiguredApp(t *testing.T) {
	_, server := newFake(t)
	service, _ := newTestService(t, server)
	_, pemKey := testKey(t)
	if err := service.store.Save(context.Background(), Credentials{ID: 1, Slug: "gone", Name: "gone", PrivateKey: pemKey, WebhookSecret: "s"}); err != nil {
		t.Fatal(err)
	}
	service.APIURL = "http://127.0.0.1:1"
	status, err := service.Status(context.Background(), "https://dash.example.test")
	if err != nil || !status.Configured || status.Error == "" || len(status.Installations) != 0 {
		t.Fatalf("status = %+v, %v", status, err)
	}
	if next := nextLink(`<https://api.github.com/app/installations?per_page=100&page=2>; rel="next", <https://api.github.com/app/installations?per_page=100&page=9>; rel="last"`); next != "/app/installations?per_page=100&page=2" {
		t.Fatalf("next link = %q", next)
	}
	if next := nextLink(`<https://api.github.com/x?page=1>; rel="prev"`); next != "" {
		t.Fatalf("prev-only link = %q", next)
	}
}

// Pull requests are read with the installation's token, page by page, and
// carry what a preview needs to decide with: the head commit, whether the
// head lives in another repository, and whether the change already landed.
func TestPullRequestsAreReadThroughTheInstallation(t *testing.T) {
	fake, server := newFake(t)
	client, _ := newTestClient(t, server)
	ctx := context.Background()
	pulls, err := client.ListPullRequests(ctx, 2, "acme/shop", "open")
	if err != nil || len(pulls) != 3 {
		t.Fatalf("pulls = %+v, %v", pulls, err)
	}
	if !strings.HasPrefix(fake.pullQueries[0], "state=open&per_page=100&sort=updated&direction=desc") || len(fake.pullQueries) != 2 {
		t.Fatalf("queries = %v", fake.pullQueries)
	}
	if !strings.HasPrefix(fake.authorization[len(fake.authorization)-1], "Bearer ghs_token_2_") {
		t.Fatalf("pull requests were listed with %q", fake.authorization[len(fake.authorization)-1])
	}
	// Same repository under a different case is not a fork; another owner is.
	same, fork, gone := pulls[0], pulls[1], pulls[2]
	if same.Number != 1 || same.Fork || same.HeadRepository != "ACME/shop" || same.HeadSHA != strings.Repeat("1", 40) || same.Head != "feature" || same.Base != "main" || same.Author != "zed" || same.State != "open" || same.URL != "https://github.com/acme/shop/pull/1" {
		t.Fatalf("same-repository pull = %+v", same)
	}
	if !fork.Fork || fork.HeadRepository != "zed/shop" || !fork.Draft || len(fork.Labels) != 1 || fork.Labels[0] != "bug" || fork.Mergeable != "" || fork.Merged {
		t.Fatalf("fork pull = %+v", fork)
	}
	// A deleted fork is still somebody else's code, and a merged pull request
	// reads as merged rather than closed, as gh reports it.
	if !gone.Fork || gone.HeadRepository != "" || !gone.Merged || gone.State != "merged" || gone.UpdatedAt.IsZero() {
		t.Fatalf("deleted-fork pull = %+v", gone)
	}
	if _, err := client.ListPullRequests(ctx, 2, "acme/shop", "open&per_page=1"); err == nil || len(fake.pullQueries) != 2 {
		t.Fatalf("a state carrying query syntax reached GitHub: %v %v", err, fake.pullQueries)
	}
	if _, err := client.ListPullRequests(ctx, 2, "acme/../shop", "open"); err == nil {
		t.Fatal("a path-shaped repository was accepted")
	}
	one, err := client.GetPullRequest(ctx, 2, "acme/shop", 1)
	if err != nil || one.Mergeable != "mergeable" || one.Comments != 4 || one.Additions != 10 || one.Deletions != 2 || one.Files != 3 || one.Body != "Adds the thing." || one.Fork {
		t.Fatalf("pull 1 = %+v, %v", one, err)
	}
	if two, err := client.GetPullRequest(ctx, 2, "acme/shop", 2); err != nil || two.Mergeable != "unknown" || !two.Fork {
		t.Fatalf("pull 2 = %+v, %v", two, err)
	}
	if three, err := client.GetPullRequest(ctx, 2, "acme/shop", 3); err != nil || three.Mergeable != "conflicting" || !three.Merged || three.State != "merged" {
		t.Fatalf("pull 3 = %+v, %v", three, err)
	}
	if _, err := client.GetPullRequest(ctx, 2, "acme/shop", 404); !errors.Is(err, ErrPullRequestNotFound) {
		t.Fatalf("missing pull request: %v", err)
	}
	if _, err := client.GetPullRequest(ctx, 2, "acme/shop", 0); err == nil {
		t.Fatal("a zero pull request number was accepted")
	}
	// The service resolves the installation itself and refuses a repository
	// nobody granted.
	service, _ := newTestService(t, server)
	if service.Installed(ctx, "acme/shop") {
		t.Fatal("installed before any App was connected")
	}
	_, pemKey := testKey(t)
	if err := service.store.Save(ctx, Credentials{ID: 4242, ClientID: "Iv1.client", PrivateKey: pemKey, WebhookSecret: "s"}); err != nil {
		t.Fatal(err)
	}
	if !service.Installed(ctx, "acme/shop") || service.Installed(ctx, "nobody/nothing") {
		t.Fatal("Installed does not follow the repository's installation")
	}
	if pulls, err := service.PullRequests(ctx, "zed/blog", "all"); err != nil || len(pulls) != 3 {
		t.Fatalf("service pulls = %+v, %v", pulls, err)
	}
	if pr, err := service.PullRequest(ctx, "zed/blog", 1); err != nil || pr.Number != 1 {
		t.Fatalf("service pull = %+v, %v", pr, err)
	}
	if _, err := service.PullRequests(ctx, "nobody/nothing", "open"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("ungranted repository pulls: %v", err)
	}
}

// Check runs and commit statuses fold into one list: failures first, then
// what is still running, then what passed, and only web links survive. An
// App without checks:read still gets the statuses, with the refusal beside
// them.
func TestCheckRunsAndStatusesFoldIntoOneList(t *testing.T) {
	fake, server := newFake(t)
	client, _ := newTestClient(t, server)
	ctx := context.Background()
	sha := strings.Repeat("1", 40)
	runs, err := client.ListCheckRuns(ctx, 2, "acme/shop", strings.ToUpper(sha))
	if err != nil || len(runs) != 3 {
		t.Fatalf("check runs = %+v, %v", runs, err)
	}
	if runs[0].Name != "unit" || runs[0].Status != "completed" || runs[0].Conclusion != "success" || runs[0].URL != "https://github.com/acme/shop/runs/1" || runs[0].App != "GitHub Actions" || runs[0].StartedAt == nil || runs[0].CompletedAt == nil {
		t.Fatalf("unit = %+v", runs[0])
	}
	// A details link that is not a web address falls back to GitHub's own
	// page; an App without a name is known by its slug.
	if runs[1].Status != "in_progress" || runs[1].Conclusion != "" || runs[1].URL != "https://github.com/acme/shop/runs/2" || runs[1].App != "linter" || runs[1].CompletedAt != nil {
		t.Fatalf("lint = %+v", runs[1])
	}
	if runs[2].Status != "queued" || runs[2].URL != "" || runs[2].App != "" || runs[2].StartedAt != nil {
		t.Fatalf("e2e = %+v", runs[2])
	}
	if _, err := client.ListCheckRuns(ctx, 2, "acme/shop", "short"); err == nil {
		t.Fatal("a short sha was accepted")
	}
	statuses, err := client.CombinedStatus(ctx, 2, "acme/shop", sha)
	if err != nil || len(statuses) != 3 {
		t.Fatalf("statuses = %+v, %v", statuses, err)
	}
	if statuses[0].Name != "ci/build" || statuses[0].Status != "completed" || statuses[0].Conclusion != "failure" || statuses[0].URL != "https://ci.example.test/1" || statuses[0].CompletedAt == nil {
		t.Fatalf("build status = %+v", statuses[0])
	}
	if statuses[1].Status != "in_progress" || statuses[1].Conclusion != "pending" || statuses[1].CompletedAt != nil {
		t.Fatalf("pending status = %+v", statuses[1])
	}
	if statuses[2].Conclusion != "success" || statuses[2].URL != "" {
		t.Fatalf("audit status = %+v", statuses[2])
	}
	service, _ := newTestService(t, server)
	_, pemKey := testKey(t)
	if err := service.store.Save(ctx, Credentials{ID: 4242, ClientID: "Iv1.client", PrivateKey: pemKey, WebhookSecret: "s"}); err != nil {
		t.Fatal(err)
	}
	checks, err := service.PullRequestChecks(ctx, "acme/shop", sha)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, check := range checks {
		names = append(names, check.Name)
	}
	if strings.Join(names, ",") != "ci/build,deploy/preview,e2e,lint,ci/audit,unit" {
		t.Fatalf("order = %v", names)
	}
	fake.checksForbidden = true
	checks, err = service.PullRequestChecks(ctx, "acme/shop", sha)
	if !errors.Is(err, ErrPermission) || len(checks) != 3 || checks[0].Name != "ci/build" {
		t.Fatalf("without checks:read = %+v, %v", checks, err)
	}
	if _, err := client.ListCheckRuns(ctx, 2, "acme/shop", sha); !errors.Is(err, ErrPermission) || !strings.Contains(err.Error(), "Resource not accessible") {
		t.Fatalf("forbidden check runs: %v", err)
	}
	if _, err := service.PullRequestChecks(ctx, "nobody/nothing", sha); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("ungranted repository checks: %v", err)
	}
}
