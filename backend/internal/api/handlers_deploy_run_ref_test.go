package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// insertRefDeployFixture creates a normalized project and production
// environment whose source is exactly the given config, so a test can pick
// the source kind that matters to it instead of the fixed Git source
// insertDeploymentConfigurationAPI hands back.
func insertRefDeployFixture(
	t *testing.T, s *Server, name string, source deploy.DraftSourceConfig, buildMethod deploy.BuildMethod,
) (projectID, environmentID int64) {
	t.Helper()
	now := time.Now().UTC().Unix()
	project, err := s.Store.DB.Exec(`
		INSERT INTO deploy_projects(name, profile, repo_path, branch, compose_file, hook_secret, hook_id, enabled, created_at, updated_at)
		VALUES(?, 'web', ?, 'main', 'compose.yml', '', ?, 1, ?, ?)`,
		name, "/srv/"+name, name+"-hook", now, now)
	if err != nil {
		t.Fatal(err)
	}
	projectID, err = project.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	environment, err := s.Store.DB.Exec(`
		INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, strategy, expected_downtime, protected, created_at, updated_at)
		VALUES(?, 'production', 'production', 'production', 1, 'stop_first', 1, 1, ?, ?)`,
		projectID, now, now)
	if err != nil {
		t.Fatal(err)
	}
	environmentID, err = environment.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	encodedSource, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := json.Marshal(deploy.SourceIdentity{Kind: source.Kind})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`
		INSERT INTO deploy_sources(environment_id, revision, kind, config_json, identity_json, digest, created_at)
		VALUES(?, 1, ?, ?, ?, ?, ?)`,
		environmentID, source.Kind, string(encodedSource), string(identity), "src-"+name, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`
		INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at)
		VALUES(?, 1, ?, '{}', '{}', '', ?, ?)`,
		environmentID, buildMethod, "build-"+name, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`
		INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at)
		VALUES(?, 1, '{}', '', ?, ?)`,
		environmentID, "runtime-"+name, now); err != nil {
		t.Fatal(err)
	}
	return projectID, environmentID
}

// fakeGitOnPath puts a script named "git" ahead of the real one on PATH for
// the life of the test, so ls-remote can be scripted without a network call —
// the same technique TestResolveGitRevisionOnlyReadsExactBranch already uses.
func fakeGitOnPath(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

const gitRemoteURL = "https://github.com/acme/ref-app.git"

func gitSourceFixture() deploy.DraftSourceConfig {
	return deploy.DraftSourceConfig{Kind: deploy.SourceGit, Mode: deploy.SourceModeGitURL, URL: gitRemoteURL, Ref: "main"}
}

func runCreatePath(projectID, environmentID int64) string {
	return fmt.Sprintf("/api/v1/deploy/%d/environments/%d/runs", projectID, environmentID)
}

// A manual deploy of an explicit commit needs no remote lookup at all — the
// object id is trusted and frozen exactly as the watcher's own pre-resolved
// revisions already are.
func TestDeploymentRunCreateAcceptsAnExplicitSourceRevision(t *testing.T) {
	s := testServer(t)
	projectID, environmentID := insertRefDeployFixture(t, s, "ref-sha-app", gitSourceFixture(), deploy.BuildDockerfile)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "ref-sha-admin", auth.RoleAdmin)}
	sha := strings.Repeat("a", 40)
	body, _ := json.Marshal(map[string]any{"operation": "deploy", "sourceRevision": sha})
	response := admin.do(http.MethodPost, runCreatePath(projectID, environmentID), string(body), nil)
	if response.Code != http.StatusAccepted {
		t.Fatalf("deploy with sha = %d %s", response.Code, response.Body.String())
	}
	var revision, metadata string
	if err := s.Store.DB.QueryRow(`SELECT source_revision, metadata_json FROM deploy_runs WHERE project_id=? ORDER BY id DESC LIMIT 1`, projectID).
		Scan(&revision, &metadata); err != nil {
		t.Fatal(err)
	}
	if revision != sha {
		t.Fatalf("run source_revision = %s, want %s", revision, sha)
	}
	if !strings.Contains(metadata, `"requestedRevision":"`+sha+`"`) {
		t.Fatalf("run metadata = %s, want requestedRevision recorded", metadata)
	}
	if strings.Contains(metadata, `"requestedRef"`) {
		t.Fatalf("run metadata = %s, want no requestedRef for a sha-only deploy", metadata)
	}
}

// A manual deploy of a branch or tag name is resolved through the same
// ls-remote path the configured branch uses, and the resolved commit — not
// just the name — is what gets frozen into the run.
func TestDeploymentRunCreateResolvesAndRecordsAnExplicitRef(t *testing.T) {
	s := testServer(t)
	tag := strings.Repeat("b", 40)
	fakeGitOnPath(t, "#!/bin/sh\n"+
		"[ \"$1\" = ls-remote ] || exit 1\n"+
		"case \"$5\" in\n"+
		"  refs/heads/v1.4.2) printf '%s\\t%s\\n' "+tag+" refs/heads/v1.4.2 ;;\n"+
		"  *) exit 2 ;;\n"+
		"esac\n")
	projectID, environmentID := insertRefDeployFixture(t, s, "ref-tag-app", gitSourceFixture(), deploy.BuildDockerfile)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "ref-tag-admin", auth.RoleAdmin)}
	body, _ := json.Marshal(map[string]any{"operation": "deploy", "ref": "v1.4.2"})
	response := admin.do(http.MethodPost, runCreatePath(projectID, environmentID), string(body), nil)
	if response.Code != http.StatusAccepted {
		t.Fatalf("deploy with ref = %d %s", response.Code, response.Body.String())
	}
	var revision, metadata string
	if err := s.Store.DB.QueryRow(`SELECT source_revision, metadata_json FROM deploy_runs WHERE project_id=? ORDER BY id DESC LIMIT 1`, projectID).
		Scan(&revision, &metadata); err != nil {
		t.Fatal(err)
	}
	if revision != tag {
		t.Fatalf("run source_revision = %s, want the resolved tag commit %s", revision, tag)
	}
	if !strings.Contains(metadata, `"requestedRef":"v1.4.2"`) || !strings.Contains(metadata, `"requestedRevision":"`+tag+`"`) {
		t.Fatalf("run metadata = %s, want requestedRef and requestedRevision recorded", metadata)
	}
}

// A ref override is meaningless against a source with no remote branch to
// resolve it from — image, Compose, blueprint and import projects all refuse
// it the same way.
func TestDeploymentRunCreateRefusesARefOverrideOnAnImageProject(t *testing.T) {
	s := testServer(t)
	source := deploy.DraftSourceConfig{Kind: deploy.SourceImage, Mode: deploy.SourceModeImageReference, Image: "alpine:3"}
	projectID, environmentID := insertRefDeployFixture(t, s, "ref-image-app", source, deploy.BuildImage)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "ref-image-admin", auth.RoleAdmin)}
	body, _ := json.Marshal(map[string]any{"operation": "deploy", "sourceRevision": strings.Repeat("a", 40)})
	response := admin.do(http.MethodPost, runCreatePath(projectID, environmentID), string(body), nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("sourceRevision on an image project = %d %s", response.Code, response.Body.String())
	}
	if got := decodedAPIError(t, response.Body.Bytes()); got.Code != "ref_not_applicable" {
		t.Fatalf("error code = %q, want ref_not_applicable", got.Code)
	}
}

// The two fields name the same target two different ways; sending both is a
// malformed request, not a preference to arbitrate.
func TestDeploymentRunCreateRefusesBothSourceRevisionAndRefTogether(t *testing.T) {
	s := testServer(t)
	projectID, environmentID := insertRefDeployFixture(t, s, "ref-both-app", gitSourceFixture(), deploy.BuildDockerfile)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "ref-both-admin", auth.RoleAdmin)}
	body, _ := json.Marshal(map[string]any{
		"operation": "deploy", "sourceRevision": strings.Repeat("a", 40), "ref": "v1.4.2",
	})
	response := admin.do(http.MethodPost, runCreatePath(projectID, environmentID), string(body), nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("both fields set = %d %s", response.Code, response.Body.String())
	}
	if got := decodedAPIError(t, response.Body.Bytes()); got.Code != "bad_request" {
		t.Fatalf("error code = %q, want bad_request", got.Code)
	}
}

// A legacy Compose project refuses the override too, through the same 400
// its unsupported-operation checks already answer with.
func TestDeploymentRunCreateRefusesARefOverrideOnALegacyComposeProject(t *testing.T) {
	s := testServer(t)
	project := createLegacyDeploymentFixture(t, s, "ref-legacy-app")
	environmentID, _, err := s.modules.deployRuns.ProductionEnvironment(t.Context(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "ref-legacy-admin", auth.RoleAdmin)}
	body, _ := json.Marshal(map[string]any{"operation": "deploy", "ref": "v1.4.2"})
	response := admin.do(http.MethodPost, runCreatePath(project.ID, environmentID), string(body), nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("ref on a legacy compose project = %d %s", response.Code, response.Body.String())
	}
}

// A manual override deploy of an older commit must not be mistaken by the
// git watcher for the branch having moved there: the watcher's own cursor
// (deploy_git_watches) is written only by its own poll/webhook path, never by
// enqueueing a run, so it is unaffected regardless of what revision a manual
// run freezes.
func TestDeploymentRunCreateWithAnExplicitRevisionDoesNotAdvanceTheGitWatchCursor(t *testing.T) {
	s := testServer(t)
	projectID, environmentID := insertRefDeployFixture(t, s, "ref-watch-app", gitSourceFixture(), deploy.BuildDockerfile)
	head := strings.Repeat("c", 40)
	if _, err := s.Store.DB.Exec(`
		INSERT INTO deploy_git_watches(environment_id, source_key, revision, status, run_id)
		VALUES(?, 'seed', ?, 'watching', 0)`, environmentID, head); err != nil {
		t.Fatal(err)
	}
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "ref-watch-admin", auth.RoleAdmin)}
	older := strings.Repeat("a", 40)
	body, _ := json.Marshal(map[string]any{"operation": "deploy", "sourceRevision": older})
	response := admin.do(http.MethodPost, runCreatePath(projectID, environmentID), string(body), nil)
	if response.Code != http.StatusAccepted {
		t.Fatalf("manual sha deploy = %d %s", response.Code, response.Body.String())
	}
	var cursorRevision, cursorStatus string
	if err := s.Store.DB.QueryRow(`SELECT revision, status FROM deploy_git_watches WHERE environment_id=?`, environmentID).
		Scan(&cursorRevision, &cursorStatus); err != nil {
		t.Fatal(err)
	}
	if cursorRevision != head || cursorStatus != "watching" {
		t.Fatalf("git watch cursor = revision=%s status=%s after a manual override deploy, want unchanged at %s/watching",
			cursorRevision, cursorStatus, head)
	}
}

// ref_not_found (git ls-remote --exit-code exits 2: it read the remote fine,
// there is just no such branch or tag) and source_unavailable (any other
// failure — the remote itself could not be read at all) are end-to-end,
// distinguishable outcomes of a manual ref override, not folded into the
// same generic error.
func TestDeploymentRunCreateRefNotFoundAndSourceUnavailable(t *testing.T) {
	s := testServer(t)
	projectID, environmentID := insertRefDeployFixture(t, s, "ref-missing-app", gitSourceFixture(), deploy.BuildDockerfile)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "ref-missing-admin", auth.RoleAdmin)}

	fakeGitOnPath(t, "#!/bin/sh\nexit 2\n")
	notFound, _ := json.Marshal(map[string]any{"operation": "deploy", "ref": "no-such-branch"})
	response := admin.do(http.MethodPost, runCreatePath(projectID, environmentID), string(notFound), nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing ref = %d %s, want 400", response.Code, response.Body.String())
	}
	if got := decodedAPIError(t, response.Body.Bytes()); got.Code != "ref_not_found" {
		t.Fatalf("missing ref error code = %q, want ref_not_found", got.Code)
	}

	fakeGitOnPath(t, "#!/bin/sh\nexit 1\n")
	unavailable, _ := json.Marshal(map[string]any{"operation": "deploy", "ref": "main"})
	response = admin.do(http.MethodPost, runCreatePath(projectID, environmentID), string(unavailable), nil)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("unreadable remote = %d %s, want 502", response.Code, response.Body.String())
	}
	if got := decodedAPIError(t, response.Body.Bytes()); got.Code != "source_unavailable" {
		t.Fatalf("unreadable remote error code = %q, want source_unavailable", got.Code)
	}
}

// A ref reaches git ls-remote as a bare argv element: one starting with "-"
// would be read as a flag rather than a ref, and one containing a glob
// character asks ls-remote to match a pattern instead of the exact name the
// operator typed. Both are refused with 400 before any git subprocess runs
// at all — the fake git script below records every invocation, so if it were
// ever run, the test would see evidence of that instead of an empty file.
func TestDeploymentRunCreateRefusesADangerousRefBeforeInvokingGit(t *testing.T) {
	s := testServer(t)
	invoked := filepath.Join(t.TempDir(), "git-invoked")
	fakeGitOnPath(t, "#!/bin/sh\necho \"$@\" >> "+invoked+"\nexit 2\n")
	projectID, environmentID := insertRefDeployFixture(t, s, "ref-danger-app", gitSourceFixture(), deploy.BuildDockerfile)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "ref-danger-admin", auth.RoleAdmin)}

	for _, ref := range []string{"-x", "ma*"} {
		body, _ := json.Marshal(map[string]any{"operation": "deploy", "ref": ref})
		response := admin.do(http.MethodPost, runCreatePath(projectID, environmentID), string(body), nil)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("ref %q = %d %s, want 400", ref, response.Code, response.Body.String())
		}
		if data, err := os.ReadFile(invoked); err == nil {
			t.Fatalf("ref %q reached git before validation: %s", ref, data)
		}
	}
}
