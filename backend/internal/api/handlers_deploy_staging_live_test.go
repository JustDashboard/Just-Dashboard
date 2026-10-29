package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/config"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

// Staging to production against a real Docker daemon, through the real
// routes and the real engine: a Git checkout is built in staging, the staging
// release is promoted, and production is shown to run the very image staging
// built — the same image id, with no build step in its run — under
// production's variables and not staging's. Then a second staging release is
// promoted and production is rolled back to the first promoted release.
// Finally a pull request's preview is built through the webhook and approval
// routes and its image promoted into a staging environment: staging runs the
// very image the preview built, with staging's variable and its release task
// run with staging's value, and production refuses the preview outright.
//
// It creates only its own projects, containers, networks and image tags,
// binds no public port and removes everything it made.
func TestLiveStagingReleaseIsPromotedAsTheSameImageAndRolledBack(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker and Buildx host")
	}
	root := t.TempDir()
	repository := filepath.Join(root, "app")
	live := newLiveStagingRepository(t, repository)
	s := liveDeployServer(t, root)
	// Environment and run ids name containers and image tags on a host other
	// tests share; starting them far from 1 keeps this test's names its own.
	base := time.Now().UnixNano()%1_000_000_000 + 1_000_000_000
	for _, table := range []string{"deploy_environments", "deploy_runs"} {
		if _, err := s.Store.DB.Exec(`INSERT INTO sqlite_sequence(name, seq) VALUES(?, ?)`, table, base); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := s.startDeployEngine(ctx); err != nil {
		t.Fatal(err)
	}
	c := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "live-staging", auth.RoleAdmin)}

	projectID, production := livePlanLocalProject(t, c, repository, fmt.Sprintf("live-staging-%d", time.Now().UnixNano()%1_000_000))
	t.Cleanup(func() { removeLiveEnvironment(s, production) })
	// Plain, so the transcript prints it rather than redacting it: what a
	// release task prints is then proof of whose variables it was given.
	putLiveVariable(t, c, projectID, production, "GREETING", "production")

	added := c.do(http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/environments", projectID), `{"kind":"staging"}`, nil)
	if added.Code != http.StatusCreated {
		t.Fatalf("add staging = %d %s", added.Code, added.Body.String())
	}
	var copied deploy.StagingCopy
	if err := json.Unmarshal(added.Body.Bytes(), &copied); err != nil {
		t.Fatal(err)
	}
	staging := copied.Environment.ID
	t.Cleanup(func() { removeLiveEnvironment(s, staging) })
	if !strings.Contains(strings.Join(copied.Copied, ","), "GREETING") {
		t.Fatalf("a plain variable was not copied into staging: %#v", copied)
	}
	// Staging starts with production's plain value and is given its own:
	// the two must never be confused in what follows.
	putLiveVariable(t, c, projectID, staging, "GREETING", "staging")

	first := liveRunToSuccess(t, c, s, projectID, staging, "/runs", `{"operation":"deploy"}`)
	stagingRelease := liveLiveRelease(t, s, staging)
	stagingImage := liveContainerImage(t, s, stagingRelease)
	if got := liveServe(t, s, stagingRelease); got != "staging v1" {
		t.Fatalf("staging serves %q", got)
	}
	if !liveRunHasStep(t, s, first, deploy.StepBuildArtifact) {
		t.Fatal("staging's deploy did not build")
	}
	if transcript := liveTranscript(t, s, first); !strings.Contains(transcript, "migrated with GREETING=staging") {
		t.Fatalf("staging's release task did not run with staging's variables:\n%s", transcript)
	}
	t.Logf("staging release %d runs image %s", stagingRelease.Number, stagingImage)

	promotion := liveRunToSuccess(t, c, s, projectID, production, "/promote", fmt.Sprintf(`{"releaseId":%d}`, stagingRelease.ID))
	for _, key := range []deploy.StepKey{deploy.StepResolveSource, deploy.StepAcquireSource, deploy.StepAnalyzePlan, deploy.StepPrepareContext, deploy.StepBuildArtifact} {
		if liveRunHasStep(t, s, promotion, key) {
			t.Fatalf("the promotion run has a %s step", key)
		}
	}
	transcript := liveTranscript(t, s, promotion)
	if !strings.Contains(transcript, "no build") || !strings.Contains(transcript, "migrated with GREETING=production") {
		t.Fatalf("promotion transcript:\n%s", transcript)
	}
	if strings.Contains(transcript, "GREETING=staging") {
		t.Fatalf("production's release task ran with staging's variables:\n%s", transcript)
	}
	promoted := liveLiveRelease(t, s, production)
	productionImage := liveContainerImage(t, s, promoted)
	if productionImage != stagingImage {
		t.Fatalf("production runs %s, staging ran %s", productionImage, stagingImage)
	}
	if promoted.Promotion == nil || promoted.Promotion.ReleaseID != stagingRelease.ID || promoted.Promotion.ImageID != stagingImage {
		t.Fatalf("promoted release provenance = %#v", promoted.Promotion)
	}
	if got := liveServe(t, s, promoted); got != "production v1" {
		t.Fatalf("production serves %q after the promotion", got)
	}
	if env := liveContainerEnv(t, s, promoted); !strings.Contains(env, "GREETING=production") || strings.Contains(env, "GREETING=staging") {
		t.Fatalf("production container environment: %s", env)
	}
	t.Logf("production release %d runs the same image %s, promoted with no build", promoted.Number, productionImage)

	// A second commit, built in staging and promoted, then rolled back.
	live.commit(t, "v2")
	reloadLiveSource(t, c, projectID, staging, repository)
	liveRunToSuccess(t, c, s, projectID, staging, "/runs", `{"operation":"deploy"}`)
	secondStaging := liveLiveRelease(t, s, staging)
	secondImage := liveContainerImage(t, s, secondStaging)
	if secondImage == stagingImage {
		t.Fatal("the second staging build produced the first image")
	}
	liveRunToSuccess(t, c, s, projectID, production, "/promote", fmt.Sprintf(`{"releaseId":%d}`, secondStaging.ID))
	secondPromoted := liveLiveRelease(t, s, production)
	if got := liveContainerImage(t, s, secondPromoted); got != secondImage {
		t.Fatalf("second promotion runs %s, staging ran %s", got, secondImage)
	}
	if got := liveServe(t, s, secondPromoted); got != "production v2" {
		t.Fatalf("production serves %q after the second promotion", got)
	}

	rollback := liveRunToSuccess(t, c, s, projectID, production, "/rollback", fmt.Sprintf(`{"releaseId":%d}`, promoted.ID))
	restored := liveLiveRelease(t, s, production)
	if got := liveContainerImage(t, s, restored); got != stagingImage {
		t.Fatalf("rollback runs %s, want the first promoted image %s", got, stagingImage)
	}
	if got := liveServe(t, s, restored); got != "production v1" {
		t.Fatalf("production serves %q after the rollback", got)
	}
	if restored.Promotion == nil || restored.Promotion.ReleaseID != stagingRelease.ID {
		t.Fatalf("the rollback lost the promotion's provenance: %#v", restored.Promotion)
	}
	if liveRunHasStep(t, s, rollback, deploy.StepBuildArtifact) {
		t.Fatal("the rollback built")
	}
	t.Logf("rolled production back to release %d: image %s again", restored.Number, stagingImage)

	livePromotePreviewIntoStaging(t, s, c, root)
}

// livePromotePreviewIntoStaging builds pull request 41's preview through a
// signed delivery and the approval route, then promotes the preview's image
// into staging. A preview fetches refs/pull/N/head from a remote, which this
// host cannot reach, so the project's source is saved as a remote Git URL
// whose release mirror is pointed at a local origin by `insteadOf` — the
// fetch, build and every route after it are the real ones. The URL's host is
// under .invalid and commit statuses are off, so nothing leaves the host.
func livePromotePreviewIntoStaging(t *testing.T, s *Server, c *client, root string) {
	nonce := time.Now().UnixNano() % 1_000_000
	origin := newLiveStagingRepository(t, filepath.Join(root, "preview-origin"))
	origin.git(t, "checkout", "-q", "-b", "feature")
	origin.commit(t, "pr-41")
	head := strings.TrimSpace(origin.output(t, "rev-parse", "HEAD"))
	origin.git(t, "update-ref", "refs/pull/41/head", head)
	origin.git(t, "checkout", "-q", "main")
	mainHead := strings.TrimSpace(origin.output(t, "rev-parse", "HEAD"))

	projectID, production := livePlanLocalProject(t, c, origin.dir, fmt.Sprintf("live-preview-%d", nonce))
	t.Cleanup(func() { removeLiveEnvironment(s, production) })
	repository := fmt.Sprintf("jd-live/preview-%d", nonce)
	remote := "https://git.jd-live.invalid/" + repository + ".git"
	liveUseRemoteSource(t, s, production, remote, repository, mainHead)
	mirror := filepath.Join(s.Cfg.DataDir, "deployment-detection", "git-release-mirrors", liveDigest(remote)+".git")
	if err := os.MkdirAll(filepath.Dir(mirror), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "--bare", "--", mirror}, {"-C", mirror, "config", "url." + origin.dir + ".insteadOf", remote}} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	quiet := `{"automatic":false,"watchInclude":[],"watchExclude":[],"commitStatuses":false,"revision":0}`
	if response := c.do(http.MethodPut, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/git-policy", projectID, production), quiet, nil); response.Code != http.StatusOK {
		t.Fatalf("production git policy = %d %s", response.Code, response.Body.String())
	}

	added := c.do(http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/environments", projectID), `{"kind":"staging"}`, nil)
	var copied deploy.StagingCopy
	if added.Code != http.StatusCreated || json.Unmarshal(added.Body.Bytes(), &copied) != nil {
		t.Fatalf("add staging = %d %s", added.Code, added.Body.String())
	}
	staging := copied.Environment.ID
	t.Cleanup(func() { removeLiveEnvironment(s, staging) })
	putLiveVariable(t, c, projectID, staging, "GREETING", "staging")

	created := c.do(http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/triggers", projectID, production),
		fmt.Sprintf(`{"name":"Previews","kind":"github","provider":"github","enabled":true,"config":{"repository":%q,"ref":"main","events":["pull_request"],"preview":true}}`, repository), nil)
	var trigger deploy.TriggerCreated
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &trigger) != nil {
		t.Fatalf("create trigger = %d %s", created.Code, created.Body.String())
	}
	payload := fmt.Sprintf(`{"action":"opened","number":41,"repository":{"full_name":%q},"pull_request":{"title":"Serve pr-41","user":{"login":"contributor"},"head":{"sha":%q,"ref":"feature","repo":{"full_name":%q}}}}`,
		repository, head, repository)
	if response := (&client{t: t, h: s.Routes()}).do(http.MethodPost, "/api/v1/hooks/providers/github/"+trigger.Trigger.HookID, payload, map[string]string{
		"X-GitHub-Event": "pull_request", "X-GitHub-Delivery": fmt.Sprintf("opened-%d", nonce),
		"X-Hub-Signature-256": signProviderPayload([]byte(payload), trigger.Secret),
	}); response.Code != http.StatusAccepted {
		t.Fatalf("opened delivery = %d %s", response.Code, response.Body.String())
	}
	listed := c.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/previews/approvals", projectID), "", nil)
	var approvals []deploy.PreviewApproval
	if err := json.Unmarshal(listed.Body.Bytes(), &approvals); err != nil || len(approvals) != 1 {
		t.Fatalf("approvals = %s, %v", listed.Body.String(), err)
	}
	approve := fmt.Sprintf("/api/v1/deploy/%d/previews/approvals/%d/approve", projectID, approvals[0].ID)
	configured := c.do(http.MethodPost, approve, fmt.Sprintf(`{"revision":%q}`, head), nil)
	var approved struct {
		Preview deploy.PreviewRef `json:"preview"`
		RunID   int64             `json:"runId"`
	}
	if configured.Code >= 300 || json.Unmarshal(configured.Body.Bytes(), &approved) != nil || approved.Preview.EnvironmentID == 0 {
		t.Fatalf("approve = %d %s", configured.Code, configured.Body.String())
	}
	preview := approved.Preview.EnvironmentID
	t.Cleanup(func() {
		removeLiveEnvironment(s, preview)
		_ = exec.Command("docker", "network", "rm", fmt.Sprintf("jd-preview-e%d", preview)).Run()
	})
	// The preview's own value, and a secret only it has: staging must run
	// with neither.
	putLiveVariable(t, c, projectID, preview, "GREETING", "preview")
	putLiveVariable(t, c, projectID, preview, "PREVIEW_ONLY", "preview-only")
	deployed := c.do(http.MethodPost, approve, fmt.Sprintf(`{"revision":%q,"deploy":true}`, head), nil)
	if deployed.Code != http.StatusAccepted || json.Unmarshal(deployed.Body.Bytes(), &approved) != nil || approved.RunID == 0 {
		t.Fatalf("approve and deploy = %d %s", deployed.Code, deployed.Body.String())
	}
	liveAwaitSuccess(t, s, approved.RunID)
	previewRelease := liveLiveRelease(t, s, preview)
	previewImage := liveContainerImage(t, s, previewRelease)
	if got := liveServe(t, s, previewRelease); got != "preview pr-41" {
		t.Fatalf("the preview serves %q", got)
	}
	t.Logf("PR #41's preview release %d runs image %s at %s", previewRelease.Number, previewImage, head)

	refused := c.do(http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/promote", projectID, production), `{"source":"preview","preview":41}`, nil)
	if refused.Code != http.StatusUnprocessableEntity || !strings.Contains(refused.Body.String(), "preview_promotion_target") {
		t.Fatalf("a preview straight into production = %d %s", refused.Code, refused.Body.String())
	}
	promotion := liveRunToSuccess(t, c, s, projectID, staging, "/promote", `{"source":"preview","preview":41}`)
	for _, key := range []deploy.StepKey{deploy.StepResolveSource, deploy.StepAcquireSource, deploy.StepAnalyzePlan, deploy.StepPrepareContext, deploy.StepBuildArtifact} {
		if liveRunHasStep(t, s, promotion, key) {
			t.Fatalf("the preview's promotion has a %s step", key)
		}
	}
	transcript := liveTranscript(t, s, promotion)
	if !strings.Contains(transcript, "PR #41 ("+head[:7]+"), preview release #") || !strings.Contains(transcript, "no build") ||
		!strings.Contains(transcript, "migrated with GREETING=staging") || strings.Contains(transcript, "GREETING=preview") {
		t.Fatalf("promotion transcript:\n%s", transcript)
	}
	promoted := liveLiveRelease(t, s, staging)
	if got := liveContainerImage(t, s, promoted); got != previewImage {
		t.Fatalf("staging runs %s, the preview built %s", got, previewImage)
	}
	if got := liveServe(t, s, promoted); got != "staging pr-41" {
		t.Fatalf("staging serves %q after the promotion", got)
	}
	if env := liveContainerEnv(t, s, promoted); !strings.Contains(env, "GREETING=staging") || strings.Contains(env, "GREETING=preview") || strings.Contains(env, "PREVIEW_ONLY") {
		t.Fatalf("staging container environment: %s", env)
	}
	record := promoted.Promotion
	if !record.FromPreview() || record.Preview == nil || record.Preview.Number != 41 || record.Preview.ApprovedBy != "live-staging" ||
		record.SourceRevision != head || record.ImageID != previewImage || record.ReleaseID != previewRelease.ID ||
		record.Preview.URL != "https://git.jd-live.invalid/"+repository+"/pull/41" {
		t.Fatalf("staging's release does not say where it came from: %#v %#v", record, record.Preview)
	}
	t.Logf("staging release %d runs PR #41's image %s with staging's variables, promoted with no build", promoted.Number, previewImage)

	// The preview's container, its network and its tag of the image go, as
	// closing the pull request and retention take them: staging keeps
	// serving from its own tag of the very image. (The engine's preview_remove
	// also withdraws the preview's route, which needs a proxy adapter this
	// fixture's server has not got; preview removal has its own fixtures.)
	removeLiveEnvironment(s, preview)
	_ = exec.Command("docker", "network", "rm", fmt.Sprintf("jd-preview-e%d", preview)).Run()
	tag := fmt.Sprintf("just-dashboard/deployment-%d:run-%d", staging, promotion)
	if output, err := exec.Command("docker", "image", "inspect", "--format", "{{.Id}}", tag).Output(); err != nil || strings.TrimSpace(string(output)) != previewImage {
		t.Fatalf("staging's tag %s = %q, %v; want the preview's image %s", tag, output, err, previewImage)
	}
	if got := liveServe(t, s, promoted); got != "staging pr-41" {
		t.Fatalf("staging serves %q once the preview is gone", got)
	}
}

func (r *liveStagingRepository) output(t *testing.T, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = r.dir
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return string(output)
}

// liveUseRemoteSource saves an environment's desired source as a remote Git
// URL. There is no route for that offline: saving a source resolves it on
// the remote first.
func liveUseRemoteSource(t *testing.T, s *Server, environmentID int64, remote, repository, revision string) {
	t.Helper()
	var desired int
	if err := s.Store.DB.QueryRow(`SELECT desired_revision FROM deploy_environments WHERE id = ?`, environmentID).Scan(&desired); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(deploy.DraftSourceConfig{Kind: deploy.SourceGit, Mode: deploy.SourceModeGitURL, URL: remote, Ref: "main"})
	identity, _ := json.Marshal(deploy.SourceIdentity{Kind: deploy.SourceGit, Remote: remote, Repository: repository, Ref: "main", Revision: revision})
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM deploy_sources WHERE environment_id = ? AND revision = ?`, []any{environmentID, desired}},
		{`INSERT INTO deploy_sources(environment_id, revision, kind, config_json, identity_json, digest, created_at) VALUES(?, ?, 'git', ?, ?, ?, ?)`,
			[]any{environmentID, desired, string(config), string(identity), liveDigest(string(config) + string(identity)), time.Now().Unix()}},
	} {
		if _, err := s.Store.DB.Exec(statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}

// liveDigest is the store's own digest of one value: sha256 over it and a
// zero byte, hex, which also names a remote's release mirror.
func liveDigest(value string) string {
	sum := sha256.Sum256(append([]byte(value), 0))
	return hex.EncodeToString(sum[:])
}

type liveStagingRepository struct{ dir string }

func newLiveStagingRepository(t *testing.T, dir string) *liveStagingRepository {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	repository := &liveStagingRepository{dir: dir}
	repository.git(t, "init", "-q", "-b", "main")
	repository.git(t, "config", "user.email", "live@example.test")
	repository.git(t, "config", "user.name", "Live Test")
	repository.commit(t, "v1")
	return repository
}

// commit writes a Dockerfile whose image says which version it is and serves
// the GREETING it was started with, so a response names both the build and
// the environment's variables.
func (r *liveStagingRepository) commit(t *testing.T, version string) {
	t.Helper()
	dockerfile := fmt.Sprintf(`FROM busybox:1.36
RUN mkdir -p /www && printf '%s' > /version
CMD ["sh", "-c", "printf '%%s %%s' \"$GREETING\" \"$(cat /version)\" > /www/index.html && exec httpd -f -p 8080 -h /www"]
`, version)
	if err := os.WriteFile(filepath.Join(r.dir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatal(err)
	}
	r.git(t, "add", "Dockerfile")
	r.git(t, "commit", "-q", "-m", "Serve "+version)
}

func (r *liveStagingRepository) git(t *testing.T, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = r.dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

// liveDeployServer is testServer with deployment roots, so a checkout under
// root can be deployed.
func liveDeployServer(t *testing.T, root string) *Server {
	t.Helper()
	st, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sealer, err := auth.NewSealer(strings.Repeat("cd", 32))
	if err != nil {
		t.Fatal(err)
	}
	_, loopback, _ := net.ParseCIDR("127.0.0.1/32")
	cfg := &config.Config{
		Addr: "127.0.0.1:8080", DataDir: t.TempDir(), AllowedCIDRs: []*net.IPNet{loopback},
		SessionTTL: time.Hour, IdleTTL: time.Minute, FileRoots: []string{root}, LogRoots: []string{t.TempDir()},
		DeployRoots: []string{root}, DeployHeavySlots: 1, DeployLightSlots: 2, DeployLeaseTTL: 30 * time.Second,
		DockerHost: "unix:///var/run/docker.sock",
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := auth.NewService(st, sealer, cfg.SessionTTL, cfg.IdleTTL, cfg.Require2FA)
	s := New(cfg, log, st, svc, sealer, audit.New(st, log), nil)
	t.Cleanup(s.Shutdown)
	return s
}

func livePlanLocalProject(t *testing.T, c *client, repository, name string) (int64, int64) {
	t.Helper()
	call := func(method, path, body string) map[string]any {
		t.Helper()
		response := c.do(method, "/api/v1"+path, body, nil)
		if response.Code >= 300 {
			t.Fatalf("%s %s = %d %s", method, path, response.Code, response.Body.String())
		}
		var decoded map[string]any
		_ = json.Unmarshal(response.Body.Bytes(), &decoded)
		return decoded
	}
	draft := call(http.MethodPost, "/deploy/drafts", `{}`)
	id := fmt.Sprint(draft["id"])
	revision := func(d map[string]any) int { return int(d["revision"].(float64)) }
	draft = call(http.MethodPut, fmt.Sprintf("/deploy/drafts/%s", id), fmt.Sprintf(`{"revision":%d,"step":"intent","intent":{"name":%q,"profile":"web"}}`, revision(draft), name))
	draft = call(http.MethodPut, fmt.Sprintf("/deploy/drafts/%s", id), fmt.Sprintf(`{"revision":%d,"step":"source","source":{"kind":"local","mode":"local_checkout","localPath":%q}}`, revision(draft), repository))
	draft = call(http.MethodPost, fmt.Sprintf("/deploy/drafts/%s/detect", id), fmt.Sprintf(`{"revision":%d}`, revision(draft)))
	configuration := map[string]any{
		"build": map[string]any{
			"method": "dockerfile", "dockerfile": "Dockerfile", "noCache": false, "secrets": []any{},
			// The task runs in the promoted image with the environment's
			// own variables: which GREETING it prints says whose
			// configuration a promotion's release tasks get.
			"releaseTasks": []any{map[string]any{
				"name": "migrate", "command": `echo "migrated with GREETING=$GREETING"`, "timeoutSeconds": 60, "env": []any{}, "runner": "image",
			}},
		},
		"runtime": map[string]any{
			"internalPort": 8080, "hostPort": 0, "bindAddress": "127.0.0.1", "strategy": "blue_green",
			"command": []any{}, "privileged": false, "hostNetwork": false, "capabilities": []any{}, "devices": []any{}, "mounts": []any{},
		},
		"variables":    []any{map[string]any{"name": "GREETING", "sensitivity": "secret", "scopes": []any{"runtime"}, "required": true}},
		"dependencies": []any{},
		"checks": []any{map[string]any{
			"name": "home", "kind": "http", "phase": "readiness", "required": true,
			"config": map[string]any{"path": "/", "expectedStatus": []any{200}, "attempts": 30, "intervalSeconds": 1},
		}},
		"domains": []any{},
	}
	encoded, _ := json.Marshal(map[string]any{
		"revision": revision(draft), "step": "configuration", "configuration": configuration, "dotenv": "GREETING=production",
	})
	draft = call(http.MethodPut, fmt.Sprintf("/deploy/drafts/%s", id), string(encoded))
	preflight := call(http.MethodPost, fmt.Sprintf("/deploy/drafts/%s/preflight", id), fmt.Sprintf(`{"revision":%d}`, revision(draft)))
	planned := preflight["draft"].(map[string]any)
	warnings := []string{}
	for _, raw := range preflight["preflight"].(map[string]any)["findings"].([]any) {
		finding := raw.(map[string]any)
		switch finding["severity"] {
		case "blocked", "decision":
			t.Fatalf("preflight refused the plan: %v", finding)
		case "warning":
			warnings = append(warnings, finding["code"].(string))
		}
	}
	acknowledged, _ := json.Marshal(warnings)
	committed := call(http.MethodPost, fmt.Sprintf("/deploy/drafts/%s/commit", id),
		fmt.Sprintf(`{"revision":%d,"acknowledgedWarnings":%s}`, revision(planned), acknowledged))
	return int64(committed["projectId"].(float64)), int64(committed["environmentId"].(float64))
}

func liveDesiredRevision(t *testing.T, c *client, projectID, environmentID int64) int {
	t.Helper()
	response := c.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/configuration", projectID, environmentID), "", nil)
	var configuration struct {
		Revision int `json:"revision"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &configuration) != nil {
		t.Fatalf("configuration = %d %s", response.Code, response.Body.String())
	}
	return configuration.Revision
}

func putLiveVariable(t *testing.T, c *client, projectID, environmentID int64, name, value string) {
	t.Helper()
	body := fmt.Sprintf(`{"revision":%d,"value":%q,"sensitivity":"plain","scopes":["runtime"]}`, liveDesiredRevision(t, c, projectID, environmentID), value)
	if response := c.do(http.MethodPut, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/variables/%s", projectID, environmentID, name), body, nil); response.Code != http.StatusOK {
		t.Fatalf("put %s = %d %s", name, response.Code, response.Body.String())
	}
}

func reloadLiveSource(t *testing.T, c *client, projectID, environmentID int64, repository string) {
	t.Helper()
	body := fmt.Sprintf(`{"revision":%d,"kind":"local","mode":"local_checkout","localPath":%q}`, liveDesiredRevision(t, c, projectID, environmentID), repository)
	if response := c.do(http.MethodPut, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/source", projectID, environmentID), body, nil); response.Code != http.StatusOK {
		t.Fatalf("reload source = %d %s", response.Code, response.Body.String())
	}
}

func liveRunToSuccess(t *testing.T, c *client, s *Server, projectID, environmentID int64, route, body string) int64 {
	t.Helper()
	response := c.do(http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/environments/%d%s", projectID, environmentID, route), body, nil)
	var run deploy.EngineRun
	if response.Code != http.StatusAccepted || json.Unmarshal(response.Body.Bytes(), &run) != nil {
		t.Fatalf("POST %s = %d %s", route, response.Code, response.Body.String())
	}
	liveAwaitSuccess(t, s, run.ID)
	return run.ID
}

// liveAwaitSuccess waits for a run to end and fails the test unless it
// succeeded.
func liveAwaitSuccess(t *testing.T, s *Server, runID int64) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Minute)
	for time.Now().Before(deadline) {
		current, err := s.modules.deployRuns.Run(t.Context(), runID)
		if err != nil {
			t.Fatal(err)
		}
		if current.State.Terminal() {
			if current.State != deploy.RunSucceeded {
				t.Fatalf("run %d (%s) ended %s: %s %s\n%s", runID, current.Operation, current.State, current.TerminalCode, current.TerminalReason, liveTranscript(t, s, runID))
			}
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("run %d did not finish", runID)
}

func liveLiveRelease(t *testing.T, s *Server, environmentID int64) deploy.Release {
	t.Helper()
	release, err := s.modules.deployRuns.LiveRelease(t.Context(), environmentID)
	if err != nil {
		t.Fatal(err)
	}
	return release.Release
}

func liveRuntime(t *testing.T, s *Server, release deploy.Release) deploy.ReleaseRuntime {
	t.Helper()
	runtime, err := s.modules.deployRuns.RuntimeForRelease(t.Context(), release.ID)
	if err != nil {
		t.Fatal(err)
	}
	return *runtime
}

func liveContainerImage(t *testing.T, s *Server, release deploy.Release) string {
	t.Helper()
	output, err := exec.Command("docker", "inspect", "--format", "{{.Image}}", liveRuntime(t, s, release).RuntimeID).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(output))
}

func liveContainerEnv(t *testing.T, s *Server, release deploy.Release) string {
	t.Helper()
	output, err := exec.Command("docker", "inspect", "--format", "{{json .Config.Env}}", liveRuntime(t, s, release).RuntimeID).Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(output)
}

func liveServe(t *testing.T, s *Server, release deploy.Release) string {
	t.Helper()
	runtime := liveRuntime(t, s, release)
	response, err := (&http.Client{Timeout: 5 * time.Second}).Get(fmt.Sprintf("http://127.0.0.1:%d/", runtime.Port))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
	return strings.TrimSpace(string(body))
}

func liveRunHasStep(t *testing.T, s *Server, runID int64, key deploy.StepKey) bool {
	t.Helper()
	steps, err := s.modules.deployRuns.Steps(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		if step.Key == key {
			return true
		}
	}
	return false
}

func liveTranscript(t *testing.T, s *Server, runID int64) string {
	t.Helper()
	rows, err := s.Store.DB.Query(`SELECT text FROM deploy_log_chunks WHERE run_id = ? AND event_type = 'step.log' ORDER BY seq`, runID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		_ = rows.Scan(&line)
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// removeLiveEnvironment removes exactly the containers and release image
// tags this test's own store recorded for an environment, and a tag only
// while it still names the image the release recorded; nothing else on the
// host is touched.
func removeLiveEnvironment(s *Server, environmentID int64) {
	rows, err := s.Store.DB.Query(`SELECT runtime_id FROM deploy_release_runtimes WHERE environment_id = ?`, environmentID)
	if err == nil {
		var ids []string
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		for _, id := range ids {
			_ = exec.Command("docker", "rm", "-f", id).Run()
		}
	}
	rows, err = s.Store.DB.Query(`SELECT a.reference, COALESCE(json_extract(a.metadata_json, '$.configDigest'), a.digest)
		FROM deploy_release_artifacts a JOIN deploy_releases r ON r.id = a.release_id
		WHERE r.environment_id = ? AND a.kind = 'image'`, environmentID)
	if err != nil {
		return
	}
	type tag struct{ reference, image string }
	var tags []tag
	for rows.Next() {
		var item tag
		if rows.Scan(&item.reference, &item.image) == nil {
			tags = append(tags, item)
		}
	}
	rows.Close()
	for _, item := range tags {
		// Only a tag a run of this store gave the image: an image source
		// that was pulled records the registry's own reference, which is
		// not this test's to remove.
		if !strings.HasPrefix(item.reference, "just-dashboard/deployment-") {
			continue
		}
		output, err := exec.Command("docker", "image", "inspect", "--format", "{{.Id}}", item.reference).Output()
		if err == nil && strings.TrimSpace(string(output)) == item.image {
			_ = exec.Command("docker", "rmi", item.reference).Run()
		}
	}
}
