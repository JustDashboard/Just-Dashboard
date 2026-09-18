package api

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// githubFake is the handful of GitHub endpoints the App flow touches, enough
// to drive the manifest exchange, an installation and a delivery end to end.
type githubFake struct {
	mu       sync.Mutex
	statuses int
}

func (f *githubFake) handler(t *testing.T) http.Handler {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	mux := http.NewServeMux()
	mux.HandleFunc("POST /app-manifests/{code}/conversions", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("code") != "one-time-code" {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 77, "slug": "just-dashboard-acme", "name": "Just Dashboard acme", "client_id": "Iv1.acme",
			"client_secret": "client-secret", "webhook_secret": "hook-secret", "pem": pemKey,
			"html_url": "https://github.com/apps/just-dashboard-acme", "owner": map[string]any{"login": "acme"},
		})
	})
	mux.HandleFunc("GET /app/installations", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 9, "account": map[string]any{"login": "acme", "type": "Organization"}, "html_url": "https://github.com/organizations/acme/settings/installations/9", "repository_selection": "selected"}})
	})
	mux.HandleFunc("POST /app/installations/{id}/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "ghs_installation_9", "expires_at": "2099-01-01T00:00:00Z"})
	})
	mux.HandleFunc("GET /installation/repositories", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 1, "repositories": []map[string]any{{"full_name": "acme/app", "name": "app", "private": true, "default_branch": "main", "clone_url": "https://github.com/acme/app.git", "html_url": "https://github.com/acme/app"}}})
	})
	mux.HandleFunc("GET /repos/{owner}/{repo}/installation", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("owner") != "acme" {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 9})
	})
	mux.HandleFunc("POST /repos/{owner}/{repo}/statuses/{sha}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.statuses++
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	})
	return mux
}

// The whole App lifecycle through the routes: manifest, GitHub's redirect,
// the installation's credential, the import list, a delivery routed to a
// trigger that asked for App delivery, and disconnection.
func TestGitHubAppRoutesConnectRouteDeliveriesAndDisconnect(t *testing.T) {
	s := testServer(t)
	fake := &githubFake{}
	server := httptest.NewServer(fake.handler(t))
	t.Cleanup(server.Close)
	s.modules.githubApp.APIURL, s.modules.githubApp.WebURL = server.URL, "https://github.example.test"
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	routes := s.Routes()
	admin := &client{t: t, h: routes, cookie: signInAs(t, s, "app-admin", auth.RoleAdmin)}
	reader := &client{t: t, h: routes, cookie: signInAs(t, s, "app-reader", auth.RoleReadOnly)}
	anonymous := &client{t: t, h: routes}

	if response := reader.do(http.MethodGet, "/api/v1/deploy/github-app/", "", nil); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"configured":false`) {
		t.Fatalf("empty status=%d %s", response.Code, response.Body.String())
	}
	if response := anonymous.do(http.MethodPost, "/api/v1/hooks/github-app", `{}`, map[string]string{"X-GitHub-Event": "push"}); response.Code != http.StatusNotFound {
		t.Fatalf("webhook without an app=%d %s", response.Code, response.Body.String())
	}
	if response := reader.do(http.MethodPost, "/api/v1/deploy/github-app/manifest", `{}`, nil); response.Code != http.StatusForbidden {
		t.Fatalf("reader manifest=%d", response.Code)
	}
	started := admin.do(http.MethodPost, "/api/v1/deploy/github-app/manifest", `{"organization":"acme"}`, nil)
	if started.Code != http.StatusOK {
		t.Fatalf("manifest=%d %s", started.Code, started.Body.String())
	}
	var start struct {
		Action   string `json:"action"`
		State    string `json:"state"`
		Manifest struct {
			HookAttributes map[string]any `json:"hook_attributes"`
			RedirectURL    string         `json:"redirect_url"`
		} `json:"manifest"`
	}
	if err := json.Unmarshal(started.Body.Bytes(), &start); err != nil {
		t.Fatal(err)
	}
	endpoint := strings.TrimRight(s.dashboardEndpoint(), "/")
	if start.Action != "https://github.example.test/organizations/acme/settings/apps/new?state="+start.State ||
		start.Manifest.HookAttributes["url"] != endpoint+"/api/v1/hooks/github-app" ||
		start.Manifest.RedirectURL != endpoint+"/api/v1/deploy/github-app/callback" {
		t.Fatalf("manifest start = %+v (endpoint %s)", start, endpoint)
	}
	if response := admin.do(http.MethodGet, "/api/v1/deploy/github-app/callback?code=one-time-code&state=forged", "", nil); response.Code != http.StatusSeeOther || !strings.Contains(response.Header().Get("Location"), "github-app=failed") {
		t.Fatalf("forged state=%d %s", response.Code, response.Header().Get("Location"))
	}
	started = admin.do(http.MethodPost, "/api/v1/deploy/github-app/manifest", `{}`, nil)
	if err := json.Unmarshal(started.Body.Bytes(), &start); err != nil {
		t.Fatal(err)
	}
	callback := admin.do(http.MethodGet, "/api/v1/deploy/github-app/callback?code=one-time-code&state="+start.State, "", nil)
	if callback.Code != http.StatusSeeOther || callback.Header().Get("Location") != "/deploy/credentials?github-app=connected" {
		t.Fatalf("callback=%d %s %s", callback.Code, callback.Header().Get("Location"), callback.Body.String())
	}
	status := reader.do(http.MethodGet, "/api/v1/deploy/github-app/", "", nil)
	var parsed struct {
		Configured    bool `json:"configured"`
		Installations []struct {
			Account      string `json:"account"`
			CredentialID int64  `json:"credentialId"`
		} `json:"installations"`
		InstallURL string `json:"installUrl"`
		WebhookURL string `json:"webhookUrl"`
	}
	if err := json.Unmarshal(status.Body.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if !parsed.Configured || len(parsed.Installations) != 1 || parsed.Installations[0].Account != "acme" || parsed.Installations[0].CredentialID == 0 ||
		parsed.InstallURL != "https://github.example.test/apps/just-dashboard-acme/installations/new" || parsed.WebhookURL != endpoint+"/api/v1/hooks/github-app" {
		t.Fatalf("status = %s", status.Body.String())
	}
	if strings.Contains(status.Body.String(), "hook-secret") || strings.Contains(status.Body.String(), "PRIVATE KEY") {
		t.Fatal("status leaked a secret")
	}
	repositories := reader.do(http.MethodGet, "/api/v1/deploy/github-app/repositories", "", nil)
	if repositories.Code != http.StatusOK || !strings.Contains(repositories.Body.String(), `"nameWithOwner":"acme/app"`) || !strings.Contains(repositories.Body.String(), fmt.Sprintf(`"credentialId":%d`, parsed.Installations[0].CredentialID)) {
		t.Fatalf("repositories=%d %s", repositories.Code, repositories.Body.String())
	}
	credentials := admin.do(http.MethodGet, "/api/v1/deploy/credentials", "", nil)
	if !strings.Contains(credentials.Body.String(), `"kind":"github_app"`) || !strings.Contains(credentials.Body.String(), `"name":"GitHub-App-acme"`) {
		t.Fatalf("credential list = %s", credentials.Body.String())
	}

	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, environmentID)
	created := admin.do(http.MethodPost, base+"/triggers", `{"name":"App push","kind":"github","provider":"github","enabled":true,"config":{"repository":"acme/app","ref":"main","delivery":"app"}}`, nil)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"delivery":"app"`) {
		t.Fatalf("trigger create=%d %s", created.Code, created.Body.String())
	}
	payload := []byte(`{"ref":"refs/heads/main","after":"abc123","repository":{"full_name":"acme/app"},"commits":[{"modified":["main.go"]}]}`)
	headers := map[string]string{"X-GitHub-Event": "push", "X-GitHub-Delivery": "app-delivery-1", "X-Hub-Signature-256": signProviderPayload(payload, "hook-secret")}
	if response := anonymous.do(http.MethodPost, "/api/v1/hooks/github-app", string(payload), map[string]string{"X-GitHub-Event": "push", "X-GitHub-Delivery": "bad", "X-Hub-Signature-256": "sha256=00"}); response.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature=%d %s", response.Code, response.Body.String())
	}
	installEvent := []byte(`{"action":"created","installation":{"id":9}}`)
	if response := anonymous.do(http.MethodPost, "/api/v1/hooks/github-app", string(installEvent), map[string]string{"X-GitHub-Event": "installation", "X-GitHub-Delivery": "install-1", "X-Hub-Signature-256": signProviderPayload(installEvent, "hook-secret")}); response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), "event_ignored") {
		t.Fatalf("installation event=%d %s", response.Code, response.Body.String())
	}
	foreign := []byte(`{"ref":"refs/heads/main","after":"abc123","repository":{"full_name":"other/app"}}`)
	if response := anonymous.do(http.MethodPost, "/api/v1/hooks/github-app", string(foreign), map[string]string{"X-GitHub-Event": "push", "X-GitHub-Delivery": "foreign-1", "X-Hub-Signature-256": signProviderPayload(foreign, "hook-secret")}); response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"triggers":[]`) {
		t.Fatalf("foreign repository=%d %s", response.Code, response.Body.String())
	}
	delivered := anonymous.do(http.MethodPost, "/api/v1/hooks/github-app", string(payload), headers)
	if delivered.Code != http.StatusAccepted || !strings.Contains(delivered.Body.String(), `"accepted":true`) || !strings.Contains(delivered.Body.String(), `"runId":`) {
		t.Fatalf("delivery=%d %s", delivered.Code, delivered.Body.String())
	}
	var runs int
	_ = s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_runs WHERE project_id=?`, projectID).Scan(&runs)
	if runs != 1 {
		t.Fatalf("delivery created %d runs", runs)
	}
	replay := anonymous.do(http.MethodPost, "/api/v1/hooks/github-app", string(payload), headers)
	if replay.Code != http.StatusAccepted || !strings.Contains(replay.Body.String(), "delivery_replayed") {
		t.Fatalf("replay=%d %s", replay.Code, replay.Body.String())
	}
	// The per-trigger hook path keeps working for triggers that never opted in.
	if response := anonymous.do(http.MethodPost, "/api/v1/hooks/github-app", string(payload), map[string]string{"X-GitHub-Event": "push", "X-GitHub-Delivery": "app-delivery-2", "X-Hub-Signature-256": signProviderPayload(payload, "hook-secret")}); response.Code != http.StatusAccepted {
		t.Fatalf("second delivery=%d %s", response.Code, response.Body.String())
	}
	// Statuses posted by the deploy observers go through the App when it is
	// installed on the repository.
	poster := githubStatusPoster{app: s.modules.githubApp, github: s.modules.github}
	if err := poster.PostCommitStatus(t.Context(), "acme/app", strings.Repeat("a", 40), deploy.CommitStatus{State: "pending", Context: "just-dashboard/production"}); err != nil || fake.statuses != 1 {
		t.Fatalf("status through the app: %v (%d)", err, fake.statuses)
	}

	if response := reader.do(http.MethodDelete, "/api/v1/deploy/github-app/", "", nil); response.Code != http.StatusForbidden {
		t.Fatalf("reader disconnect=%d", response.Code)
	}
	if response := admin.do(http.MethodDelete, "/api/v1/deploy/github-app/", "", nil); response.Code != http.StatusNoContent {
		t.Fatalf("disconnect=%d %s", response.Code, response.Body.String())
	}
	if response := reader.do(http.MethodGet, "/api/v1/deploy/github-app/", "", nil); !strings.Contains(response.Body.String(), `"configured":false`) {
		t.Fatalf("status after disconnect = %s", response.Body.String())
	}
	if response := admin.do(http.MethodGet, "/api/v1/deploy/credentials", "", nil); strings.Contains(response.Body.String(), `"kind":"github_app"`) {
		t.Fatalf("unused app credential survived: %s", response.Body.String())
	}
	if response := anonymous.do(http.MethodPost, "/api/v1/hooks/github-app", string(payload), headers); response.Code != http.StatusNotFound {
		t.Fatalf("webhook after disconnect=%d", response.Code)
	}
}
