package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// seedCommittedDeployment inserts a project/environment/source/build/runtime
// row set directly, the way a committed draft would leave them, so the
// source-update route can be exercised without driving the whole wizard
// through HTTP first.
func seedCommittedDeployment(t *testing.T, s *Server, name string, source deploy.DraftSourceConfig, identity deploy.SourceIdentity) (int64, int64) {
	t.Helper()
	sourceJSON, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	identityJSON, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Store.DB.Exec(
		`INSERT INTO deploy_projects(name, profile, repo_path, branch, compose_file, hook_secret, hook_id, enabled, created_at, updated_at)
		 VALUES(?, 'worker', '/srv/'||?, 'main', 'compose.yml', '', ?, 1, 0, 0)`, name, name, name+"-hook")
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := result.LastInsertId()
	result, err = s.Store.DB.Exec(
		`INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, strategy, expected_downtime, protected, created_at, updated_at)
		 VALUES(?, 'production', 'production', 'production', 1, 'stop_first', 1, 1, 0, 0)`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	environmentID, _ := result.LastInsertId()
	if _, err := s.Store.DB.Exec(
		`INSERT INTO deploy_sources(environment_id, revision, kind, config_json, credential_id, identity_json, digest, created_at)
		 VALUES(?, 1, ?, ?, 0, ?, 'sha256:seed', 0)`, environmentID, source.Kind, string(sourceJSON), string(identityJSON)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(
		`INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at)
		 VALUES(?, 1, 'none', '{"method":"none"}', '{}', '{}', 'sha256:build-seed', 0)`, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(
		`INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at)
		 VALUES(?, 1, '{"strategy":"stop_first"}', '{}', 'sha256:runtime-seed', 0)`, environmentID); err != nil {
		t.Fatal(err)
	}
	return projectID, environmentID
}

// localCheckoutFixture builds a real git checkout with two subdirectories, so
// the source-update route's read-only inspection (HostSourceAnalyzer.Analyze)
// runs against real filesystem/git state without any network.
func localCheckoutFixture(t *testing.T) string {
	t.Helper()
	checkout := t.TempDir()
	for _, dir := range []string{"api", "worker"} {
		if err := os.MkdirAll(filepath.Join(checkout, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(checkout, dir, "go.mod"), []byte("module example.test/"+dir+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "source-route-test@example.test"},
		{"config", "user.name", "Source Route Test"},
		{"add", "."},
		{"commit", "-q", "-m", "fixture"},
	} {
		command := exec.Command("git", append([]string{"-C", checkout}, args...)...)
		command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
		}
	}
	return checkout
}

func TestDeploymentSourceUpdateChangesSubdirectoryAndRefusesAKindChange(t *testing.T) {
	s := testServer(t)
	checkout := localCheckoutFixture(t)
	// The analyzer is rooted at checkout's parent so local_checkout sources
	// under it resolve through files.Service the same way a draft's would.
	wireDeployPlanningAndSources(t, s, []string{filepath.Dir(checkout)})
	client := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}

	projectID, environmentID := seedCommittedDeployment(t, s, "local-src-app",
		deploy.DraftSourceConfig{Kind: deploy.SourceGit, Mode: deploy.SourceModeLocalCheckout, LocalPath: checkout, Subdirectory: "api"},
		deploy.SourceIdentity{Kind: deploy.SourceGit, LocalPath: checkout},
	)
	path := "/api/v1/deploy/" + strconv.FormatInt(projectID, 10) + "/environments/" + strconv.FormatInt(environmentID, 10) + "/source"

	kindChange := doDeployJSON(t, client, http.MethodPut, path, map[string]any{
		"revision": 1, "kind": "image", "mode": "image_reference", "image": "alpine:3",
	})
	if kindChange.Code != http.StatusBadRequest || !strings.Contains(kindChange.Body.String(), `"code":"invalid_source"`) {
		t.Fatalf("kind change = %d %s, want 400 invalid_source", kindChange.Code, kindChange.Body.String())
	}

	badSubdirectory := doDeployJSON(t, client, http.MethodPut, path, map[string]any{
		"revision": 1, "kind": "git", "mode": "local_checkout", "localPath": checkout, "subdirectory": "does-not-exist",
	})
	if badSubdirectory.Code == http.StatusOK {
		t.Fatalf("a nonexistent subdirectory was accepted: %s", badSubdirectory.Body.String())
	}
	var stillRevisionOne int
	if err := s.Store.DB.QueryRow(`SELECT desired_revision FROM deploy_environments WHERE id = ?`, environmentID).Scan(&stillRevisionOne); err != nil {
		t.Fatal(err)
	}
	if stillRevisionOne != 1 {
		t.Fatalf("desired_revision advanced to %d despite the refused source", stillRevisionOne)
	}

	changed := doDeployJSON(t, client, http.MethodPut, path, map[string]any{
		"revision": 1, "kind": "git", "mode": "local_checkout", "localPath": checkout, "subdirectory": "worker",
	})
	if changed.Code != http.StatusOK {
		t.Fatalf("subdirectory change = %d %s", changed.Code, changed.Body.String())
	}
	var result struct {
		Revision int                      `json:"revision"`
		Source   deploy.DraftSourceConfig `json:"source"`
	}
	decodeDeployResponse(t, changed.Body.Bytes(), &result)
	if result.Revision != 2 || result.Source.Subdirectory != "worker" {
		t.Fatalf("changed source result = %#v", result)
	}
	var storedSubdirectory string
	if err := s.Store.DB.QueryRow(`SELECT json_extract(config_json,'$.subdirectory') FROM deploy_sources WHERE environment_id = ? AND revision = 2`, environmentID).
		Scan(&storedSubdirectory); err != nil {
		t.Fatal(err)
	}
	if storedSubdirectory != "worker" {
		t.Fatalf("stored subdirectory = %q", storedSubdirectory)
	}

	stale := doDeployJSON(t, client, http.MethodPut, path, map[string]any{
		"revision": 1, "kind": "git", "mode": "local_checkout", "localPath": checkout, "subdirectory": "api",
	})
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), `"code":"revision_conflict"`) {
		t.Fatalf("stale revision = %d %s, want 409 revision_conflict", stale.Code, stale.Body.String())
	}
}

type fakePlanningDocker struct {
	image *dockerx.DistributionImage
}

func (f *fakePlanningDocker) Ping(context.Context) dockerx.Availability {
	return dockerx.Availability{Available: true}
}
func (f *fakePlanningDocker) ResolveDistributionImage(context.Context, string, string) (*dockerx.DistributionImage, error) {
	copy := *f.image
	return &copy, nil
}
func (f *fakePlanningDocker) InspectImage(context.Context, string) (*dockerx.ImageDetail, error) {
	return nil, errors.New("image is not present on this host")
}
func (f *fakePlanningDocker) SpecOf(context.Context, string) (*dockerx.ContainerSpec, error) {
	return nil, nil
}
func (f *fakePlanningDocker) ListStacks(context.Context, []string) ([]dockerx.ComposeStack, error) {
	return nil, nil
}
func (f *fakePlanningDocker) ComposeAvailable(context.Context) bool { return false }
func (f *fakePlanningDocker) ValidateComposePlan(context.Context, string, []dockerx.ComposeInput, []string) (*dockerx.ComposeValidation, error) {
	return nil, nil
}

// TestDeploymentSourceUpdateReResolvesTheImageDigest proves an image source
// change re-runs registry resolution (through the same PlanningDocker seam
// analyzeImage uses) rather than trusting the operator-supplied reference.
func TestDeploymentSourceUpdateReResolvesTheImageDigest(t *testing.T) {
	s := testServer(t)
	fakeDocker := &fakePlanningDocker{image: &dockerx.DistributionImage{Digest: "sha256:" + strings.Repeat("a", 64), Platforms: []string{"linux/amd64"}}}
	s.modules.deployPlanning = deploy.NewPlanningStore(s.Store, s.Sealer, []string{t.TempDir()})
	s.modules.deploySources = deploy.NewHostSourceAnalyzer(nil, nil, filepath.Join(s.Cfg.DataDir, "planning-test-cache"), fakeDocker, s.modules.deployPlanning)
	client := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}

	projectID, environmentID := seedCommittedDeployment(t, s, "image-src-app",
		deploy.DraftSourceConfig{Kind: deploy.SourceImage, Mode: deploy.SourceModeImageReference, Image: "alpine:3"},
		deploy.SourceIdentity{Kind: deploy.SourceImage, Repository: "docker.io/library/alpine:3", Digest: "sha256:" + strings.Repeat("0", 64)},
	)
	path := "/api/v1/deploy/" + strconv.FormatInt(projectID, 10) + "/environments/" + strconv.FormatInt(environmentID, 10) + "/source"

	fakeDocker.image = &dockerx.DistributionImage{Digest: "sha256:" + strings.Repeat("b", 64), Platforms: []string{"linux/amd64"}}
	changed := doDeployJSON(t, client, http.MethodPut, path, map[string]any{
		"revision": 1, "kind": "image", "mode": "image_reference", "image": "alpine:3.20",
	})
	if changed.Code != http.StatusOK {
		t.Fatalf("image source change = %d %s", changed.Code, changed.Body.String())
	}
	var result struct {
		Identity deploy.SourceIdentity `json:"identity"`
	}
	decodeDeployResponse(t, changed.Body.Bytes(), &result)
	if result.Identity.Digest != "sha256:"+strings.Repeat("b", 64) {
		t.Fatalf("resolved digest = %q, want the freshly resolved one", result.Identity.Digest)
	}
	var storedDigest string
	if err := s.Store.DB.QueryRow(`SELECT json_extract(identity_json,'$.digest') FROM deploy_sources WHERE environment_id = ? AND revision = 2`, environmentID).
		Scan(&storedDigest); err != nil {
		t.Fatal(err)
	}
	if storedDigest != "sha256:"+strings.Repeat("b", 64) {
		t.Fatalf("stored digest = %q", storedDigest)
	}
}

// GET .../configuration carries no source field of its own before this: an
// operator opening Settings after a commit, or after using PUT .../source,
// could see build/runtime/variables/dependencies/checks/domains/pending but
// nothing about which source produced them, so the Source card had nothing to
// prefill from.
func TestDeploymentConfigurationRouteReturnsSourceAfterCommitAndSourceUpdate(t *testing.T) {
	s := testServer(t)
	fakeDocker := &fakePlanningDocker{image: &dockerx.DistributionImage{Digest: "sha256:" + strings.Repeat("a", 64), Platforms: []string{"linux/amd64"}}}
	s.modules.deployPlanning = deploy.NewPlanningStore(s.Store, s.Sealer, []string{t.TempDir()})
	s.modules.deploySources = deploy.NewHostSourceAnalyzer(nil, nil, filepath.Join(s.Cfg.DataDir, "planning-test-cache"), fakeDocker, s.modules.deployPlanning)
	client := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}

	projectID, environmentID := seedCommittedDeployment(t, s, "config-source-app",
		deploy.DraftSourceConfig{Kind: deploy.SourceImage, Mode: deploy.SourceModeImageReference, Image: "alpine:3"},
		deploy.SourceIdentity{Kind: deploy.SourceImage, Repository: "docker.io/library/alpine:3", Digest: "sha256:" + strings.Repeat("0", 64)},
	)
	base := "/api/v1/deploy/" + strconv.FormatInt(projectID, 10) + "/environments/" + strconv.FormatInt(environmentID, 10)

	type configurationSource struct {
		Source   *deploy.DraftSourceConfig `json:"source"`
		Identity *deploy.SourceIdentity    `json:"identity"`
	}

	committed := client.do(http.MethodGet, base+"/configuration", "", nil)
	if committed.Code != http.StatusOK {
		t.Fatalf("configuration after commit = %d %s", committed.Code, committed.Body.String())
	}
	var afterCommit configurationSource
	decodeDeployResponse(t, committed.Body.Bytes(), &afterCommit)
	if afterCommit.Source == nil || afterCommit.Source.Image != "alpine:3" ||
		afterCommit.Identity == nil || afterCommit.Identity.Digest != "sha256:"+strings.Repeat("0", 64) {
		t.Fatalf("configuration source after commit = %#v", afterCommit)
	}

	fakeDocker.image = &dockerx.DistributionImage{Digest: "sha256:" + strings.Repeat("b", 64), Platforms: []string{"linux/amd64"}}
	changed := doDeployJSON(t, client, http.MethodPut, base+"/source", map[string]any{
		"revision": 1, "kind": "image", "mode": "image_reference", "image": "alpine:3.20",
	})
	if changed.Code != http.StatusOK {
		t.Fatalf("source update = %d %s", changed.Code, changed.Body.String())
	}

	afterUpdate := client.do(http.MethodGet, base+"/configuration", "", nil)
	if afterUpdate.Code != http.StatusOK {
		t.Fatalf("configuration after source update = %d %s", afterUpdate.Code, afterUpdate.Body.String())
	}
	var afterUpdateResult configurationSource
	decodeDeployResponse(t, afterUpdate.Body.Bytes(), &afterUpdateResult)
	// canonicalSourceConfig qualifies a bare image reference against Docker
	// Hub before it is stored, the same as any other source save.
	if afterUpdateResult.Source == nil || afterUpdateResult.Source.Image != "docker.io/library/alpine:3.20" ||
		afterUpdateResult.Identity == nil || afterUpdateResult.Identity.Digest != "sha256:"+strings.Repeat("b", 64) {
		t.Fatalf("configuration source after update raw body = %s", afterUpdate.Body.String())
	}
}
