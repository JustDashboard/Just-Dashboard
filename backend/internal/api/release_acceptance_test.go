package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// A host with no Docker, no nginx and no Backups must still serve every
// deployment read. "Degraded" means sections say what could not be read, not
// that the page fails or — worse — renders an empty success.
func TestDegradedInstallServesEveryDeploymentReadWithNamedUnavailability(t *testing.T) {
	s := testServer(t)
	s.modules.docker = nil
	s.modules.proxy = nil
	s.modules.backupStore = nil
	project := createLegacyDeploymentFixture(t, s, "degraded-install")
	client := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}

	for _, path := range []string{
		"/api/v1/deploy/",
		"/api/v1/deploy/blueprints/",
		"/api/v1/deploy/blueprints/postgresql",
		"/api/v1/docker/templates",
	} {
		response := client.do(http.MethodGet, path, "", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", path, response.Code, response.Body.String())
		}
	}

	operations := client.do(http.MethodGet,
		"/api/v1/deploy/"+strconv.FormatInt(project.ID, 10)+"/operations", "", nil)
	if operations.Code != http.StatusOK {
		t.Fatalf("operations = %d %s", operations.Code, operations.Body.String())
	}
	var summary deploy.OperationsSummary
	if err := json.Unmarshal(operations.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	for name, section := range map[string][2]string{
		"runtime":      {summary.Runtime.Status, summary.Runtime.Reason},
		"domains":      {summary.Domains.Status, summary.Domains.Reason},
		"storage":      {summary.Storage.Status, summary.Storage.Reason},
		"backups":      {summary.Backups.Status, summary.Backups.Reason},
		"dependencies": {summary.Dependencies.Status, summary.Dependencies.Reason},
	} {
		if section[0] != "unavailable" || strings.TrimSpace(section[1]) == "" {
			t.Fatalf("%s = status %q reason %q", name, section[0], section[1])
		}
	}
	// Nothing was claimed about a host this dashboard could not read.
	if len(summary.Diagnosis.Findings) != 0 {
		t.Fatalf("a degraded install produced claims: %#v", summary.Diagnosis.Findings)
	}
	if summary.Diagnosis.Status != "partial" || len(summary.Diagnosis.Silences) == 0 {
		t.Fatalf("diagnosis = %#v", summary.Diagnosis)
	}
}

// A 0.6.6 project keeps its identity, its webhook endpoint, its encrypted
// environment and its history across the upgrade. This is the compatibility
// promise the release makes to every existing install.
func TestUpgradedLegacyProjectKeepsItsHookEnvironmentAndHistory(t *testing.T) {
	s := testServer(t)
	repo := filepath.Join(t.TempDir(), "legacy-repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	project, secret, err := s.modules.deployStore.Create(t.Context(), &deploy.Project{
		Name: "legacy-upgraded", RepoPath: repo, Branch: "main",
		ComposeFile: "compose.yml", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if secret == "" {
		t.Fatal("a legacy project was created with no webhook secret")
	}
	client := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}

	// Its environment survives and stays masked on the way out.
	if response := client.do(http.MethodPut,
		"/api/v1/deploy/"+strconv.FormatInt(project.ID, 10)+"/env",
		`{"key":"API_TOKEN","value":"legacy-plaintext-secret"}`, nil); response.Code != http.StatusNoContent {
		t.Fatalf("set env = %d %s", response.Code, response.Body.String())
	}
	listed := client.do(http.MethodGet, "/api/v1/deploy/"+strconv.FormatInt(project.ID, 10)+"/env", "", nil)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), "legacy-plaintext-secret") {
		t.Fatalf("env list = %d %s", listed.Code, listed.Body.String())
	}

	// The normalized read model answers for it, and the hook endpoint it was
	// created with is still the endpoint it advertises.
	detail := client.do(http.MethodGet, "/api/v1/deploy/"+strconv.FormatInt(project.ID, 10), "", nil)
	if detail.Code != http.StatusOK {
		t.Fatalf("detail = %d %s", detail.Code, detail.Body.String())
	}
	var body struct {
		Project struct {
			ID      int64  `json:"id"`
			HookURL string `json:"hookUrl"`
			HookID  string `json:"hookId"`
		} `json:"project"`
		Deployment deploy.DeploymentSummary `json:"deployment"`
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Project.ID != project.ID || body.Project.HookID != project.HookID {
		t.Fatalf("project identity changed: %#v", body.Project)
	}
	if !strings.Contains(body.Project.HookURL, project.HookID) {
		t.Fatalf("hook url = %q", body.Project.HookURL)
	}
	if body.Deployment.ID != project.ID {
		t.Fatalf("the normalized summary has a different identity: %d", body.Deployment.ID)
	}
	// It reads as a legacy build method until it is replanned, which is what
	// keeps its existing deploy behaviour unchanged.
	if body.Deployment.BuildMethod != deploy.BuildLegacyCompose {
		t.Fatalf("build method = %q", body.Deployment.BuildMethod)
	}

	// Its run history is readable through the engine view.
	runs := client.do(http.MethodGet, "/api/v1/deploy/"+strconv.FormatInt(project.ID, 10)+"/runs?view=engine", "", nil)
	if runs.Code != http.StatusOK {
		t.Fatalf("runs = %d %s", runs.Code, runs.Body.String())
	}
}

// Read-only accounts can look at a deployment and change nothing about it.
// This is the capability matrix restated at the release boundary.
func TestReadOnlyAccountCannotChangeAnyDeploymentState(t *testing.T) {
	s := testServer(t)
	project := createLegacyDeploymentFixture(t, s, "capability-matrix")
	viewer := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "release-viewer", auth.RoleReadOnly)}
	id := strconv.FormatInt(project.ID, 10)

	for _, path := range []string{
		"/api/v1/deploy/", "/api/v1/deploy/" + id, "/api/v1/deploy/" + id + "/operations",
		"/api/v1/deploy/blueprints/",
	} {
		if response := viewer.do(http.MethodGet, path, "", nil); response.Code != http.StatusOK {
			t.Fatalf("read %s = %d %s", path, response.Code, response.Body.String())
		}
	}
	for _, mutation := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/deploy/" + id + "/run", `{}`},
		{http.MethodPost, "/api/v1/deploy/" + id + "/rollback", `{}`},
		{http.MethodPost, "/api/v1/deploy/" + id + "/archive", `{}`},
		{http.MethodDelete, "/api/v1/deploy/" + id, ""},
		{http.MethodPut, "/api/v1/deploy/" + id + "/env", `{"key":"A","value":"b"}`},
		{http.MethodPost, "/api/v1/deploy/drafts", `{}`},
		{http.MethodPost, "/api/v1/deploy/blueprints/postgresql/render", `{}`},
		{http.MethodGet, "/api/v1/deploy/" + id + "/env/reveal", ""},
	} {
		response := viewer.do(mutation.method, mutation.path, mutation.body, nil)
		if response.Code != http.StatusForbidden && response.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s = %d %s (a read-only account changed state)",
				mutation.method, mutation.path, response.Code, response.Body.String())
		}
	}
}

// The reviewed catalogue is the release's own supply-chain claim. If any
// built-in fails to load, the release does not ship.
func TestReleaseCatalogueLoadsAndEveryEntryNamesItsLicence(t *testing.T) {
	s := testServer(t)
	client := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	response := client.do(http.MethodGet, "/api/v1/deploy/blueprints/", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("catalogue = %d %s", response.Code, response.Body.String())
	}
	var summaries []struct {
		ID      string `json:"id"`
		License string `json:"license"`
		DocsURL string `json:"docsUrl"`
		Image   string `json:"image"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &summaries); err != nil {
		t.Fatal(err)
	}
	if len(summaries) < 16 {
		t.Fatalf("catalogue has %d entries", len(summaries))
	}
	for _, summary := range summaries {
		if summary.License == "" || !strings.HasPrefix(summary.DocsURL, "https://") || summary.Image == "" {
			t.Fatalf("%s ships without provenance: %#v", summary.ID, summary)
		}
	}
}
