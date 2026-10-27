package api

import (
	"context"
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
//
// It creates only its own project, containers and image tags, binds no
// public port and removes everything it made.
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
	deadline := time.Now().Add(8 * time.Minute)
	for time.Now().Before(deadline) {
		current, err := s.modules.deployRuns.Run(t.Context(), run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.State.Terminal() {
			if current.State != deploy.RunSucceeded {
				t.Fatalf("run %d (%s) ended %s: %s %s\n%s", run.ID, run.Operation, current.State, current.TerminalCode, current.TerminalReason, liveTranscript(t, s, run.ID))
			}
			return run.ID
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("run %d did not finish", run.ID)
	return 0
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
		output, err := exec.Command("docker", "image", "inspect", "--format", "{{.Id}}", item.reference).Output()
		if err == nil && strings.TrimSpace(string(output)) == item.image {
			_ = exec.Command("docker", "rmi", item.reference).Run()
		}
	}
}
