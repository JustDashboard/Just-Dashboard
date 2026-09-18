package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

func wireDeployPlanningAndSources(t *testing.T, s *Server, roots []string) {
	t.Helper()
	s.modules.deployPlanning = deploy.NewPlanningStore(s.Store, s.Sealer, roots)
	s.modules.deploySources = deploy.NewHostSourceAnalyzer(
		roots, roots, filepath.Join(s.Cfg.DataDir, "planning-test-cache"), nil, s.modules.deployPlanning,
	)
}

// TestDeploymentCredentialsCRUDJourneyNeverLeaksTheSecret drives every new
// credential route through real HTTP: create, list, update (keeping the
// secret), test (against a connection nothing answers, since a reachable
// Git host is not available in this sandbox), and delete. At every step the
// response body is checked for the literal secret string.
func TestDeploymentCredentialsCRUDJourneyNeverLeaksTheSecret(t *testing.T) {
	s := testServer(t)
	wireDeployPlanningAndSources(t, s, []string{t.TempDir()})
	client := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	const secret = "ghp_the_real_token_value"

	created := doDeployJSON(t, client, http.MethodPost, "/api/v1/deploy/credentials", map[string]any{
		"name": "github-bearer", "kind": "git_bearer", "target": "github.com", "secret": secret,
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create credential = %d %s", created.Code, created.Body.String())
	}
	if strings.Contains(created.Body.String(), secret) {
		t.Fatalf("create response leaked the secret: %s", created.Body.String())
	}
	var credential deploy.CredentialSummary
	decodeDeployResponse(t, created.Body.Bytes(), &credential)
	if credential.Target != "github.com" || credential.Kind != "git_bearer" || credential.UsedBy != 0 {
		t.Fatalf("created credential = %#v", credential)
	}
	idPath := "/api/v1/deploy/credentials/" + strconv.FormatInt(credential.ID, 10)

	list := client.do(http.MethodGet, "/api/v1/deploy/credentials", "", nil)
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), secret) || !strings.Contains(list.Body.String(), "github-bearer") {
		t.Fatalf("list credentials = %d %s", list.Code, list.Body.String())
	}

	updated := doDeployJSON(t, client, http.MethodPut, idPath, map[string]any{"name": "github-bearer-renamed"})
	if updated.Code != http.StatusOK || strings.Contains(updated.Body.String(), secret) {
		t.Fatalf("update credential = %d %s", updated.Code, updated.Body.String())
	}
	var renamed deploy.CredentialSummary
	decodeDeployResponse(t, updated.Body.Bytes(), &renamed)
	if renamed.Name != "github-bearer-renamed" || renamed.Target != "github.com" {
		t.Fatalf("renamed credential = %#v, want target preserved", renamed)
	}

	// A reachable HTTPS Git host is not available in this sandbox; port 1 on
	// loopback refuses the connection immediately, which is enough to prove
	// the route ran a real probe and reported failure without the secret.
	tested := doDeployJSON(t, client, http.MethodPost, idPath+"/test", map[string]any{
		"repository": "https://127.0.0.1:1/owner/repo.git",
	})
	if tested.Code != http.StatusOK {
		t.Fatalf("test credential = %d %s", tested.Code, tested.Body.String())
	}
	if strings.Contains(tested.Body.String(), secret) {
		t.Fatalf("test response leaked the secret: %s", tested.Body.String())
	}
	var result deploy.CredentialTestResult
	decodeDeployResponse(t, tested.Body.Bytes(), &result)
	if result.OK {
		t.Fatalf("test against a refused connection reported ok:true: %#v", result)
	}

	deleted := client.do(http.MethodDelete, idPath, "", nil)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete credential = %d %s", deleted.Code, deleted.Body.String())
	}
	afterDelete := doDeployJSON(t, client, http.MethodPut, idPath, map[string]any{"name": "x"})
	if afterDelete.Code != http.StatusNotFound {
		t.Fatalf("update after delete = %d %s, want 404", afterDelete.Code, afterDelete.Body.String())
	}
}

// TestDeploymentCredentialCreateValidatesKindShapeAndInUseDelete checks the
// per-kind shape refusals reach the client as 400 invalid_credential and
// that deleting a referenced credential answers 409 credential_in_use.
func TestDeploymentCredentialCreateValidatesKindShapeAndInUseDelete(t *testing.T) {
	s := testServer(t)
	wireDeployPlanningAndSources(t, s, []string{t.TempDir()})
	client := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}

	badKind := doDeployJSON(t, client, http.MethodPost, "/api/v1/deploy/credentials", map[string]any{
		"name": "bad-registry", "kind": "registry", "secret": "pw",
	})
	if badKind.Code != http.StatusBadRequest || !strings.Contains(badKind.Body.String(), `"code":"invalid_credential"`) {
		t.Fatalf("registry with no target/username = %d %s", badKind.Code, badKind.Body.String())
	}

	created := doDeployJSON(t, client, http.MethodPost, "/api/v1/deploy/credentials", map[string]any{
		"name": "reused-cred", "kind": "git_bearer", "target": "gitea.example.test", "secret": "tok-en",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create credential = %d %s", created.Code, created.Body.String())
	}
	var credential deploy.CredentialSummary
	decodeDeployResponse(t, created.Body.Bytes(), &credential)

	if _, err := s.Store.DB.Exec(
		`INSERT INTO deploy_projects(name, profile, repo_path, branch, compose_file, hook_secret, hook_id, enabled, created_at, updated_at)
		 VALUES('cred-user', 'worker', '/srv/cred-user', 'main', 'compose.yml', '', 'cred-user-hook', 1, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	var projectID int64
	if err := s.Store.DB.QueryRow(`SELECT id FROM deploy_projects WHERE name = 'cred-user'`).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(
		`INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, strategy, expected_downtime, protected, created_at, updated_at)
		 VALUES(?, 'production', 'production', 'production', 1, 'stop_first', 1, 1, 0, 0)`, projectID); err != nil {
		t.Fatal(err)
	}
	var environmentID int64
	if err := s.Store.DB.QueryRow(`SELECT id FROM deploy_environments WHERE project_id = ?`, projectID).Scan(&environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(
		`INSERT INTO deploy_sources(environment_id, revision, kind, config_json, credential_id, identity_json, digest, created_at)
		 VALUES(?, 1, 'git', '{}', ?, '{}', 'sha256:x', 0)`, environmentID, credential.ID); err != nil {
		t.Fatal(err)
	}

	deleteInUse := client.do(http.MethodDelete, "/api/v1/deploy/credentials/"+strconv.FormatInt(credential.ID, 10), "", nil)
	if deleteInUse.Code != http.StatusConflict || !strings.Contains(deleteInUse.Body.String(), `"code":"credential_in_use"`) {
		t.Fatalf("delete in-use credential = %d %s", deleteInUse.Code, deleteInUse.Body.String())
	}
}

func TestDeploymentCredentialRoutesRequireSystemAdmin(t *testing.T) {
	s := testServer(t)
	wireDeployPlanningAndSources(t, s, []string{t.TempDir()})
	readonly := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "reader", auth.RoleReadOnly)}
	resp := doDeployJSON(t, readonly, http.MethodPost, "/api/v1/deploy/credentials", map[string]any{
		"name": "nope", "kind": "git_bearer", "secret": "tok-en",
	})
	if resp.Code != http.StatusForbidden {
		t.Fatalf("read-only credential create = %d %s, want 403", resp.Code, resp.Body.String())
	}
	list := readonly.do(http.MethodGet, "/api/v1/deploy/credentials", "", nil)
	if list.Code != http.StatusForbidden {
		t.Fatalf("read-only credential list = %d %s, want 403", list.Code, list.Body.String())
	}
}

func doDeployJSON(t *testing.T, client *client, method, path string, value any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return client.do(method, path, string(body), nil)
}

func decodeDeployResponse(t *testing.T, body []byte, destination any) {
	t.Helper()
	if err := json.Unmarshal(body, destination); err != nil {
		t.Fatalf("decode response: %v: %s", err, body)
	}
}
