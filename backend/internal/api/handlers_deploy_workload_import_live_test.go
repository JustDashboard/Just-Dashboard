package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// This fixture owns only its unique Compose project and the temporary source
// directory. Import is checked against a separate test database, never the
// installed dashboard's records or any pre-existing workload.
func TestLiveWorkloadAdoptionKeepsFourContainerStackAndHTTPServiceUnchanged(t *testing.T) {
	if os.Getenv("JD_WORKLOAD_IMPORT_LIVE") != "1" {
		t.Skip("set JD_WORKLOAD_IMPORT_LIVE=1 on a Docker host with caddy:2-alpine already pulled")
	}
	root := t.TempDir()
	project := "jd-import-proof-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	command := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		defer cancel()
		cmd := hostexec.CommandInDir(ctx, root, "docker", args...)
		return cmd.CombinedOutput()
	}
	if output, err := command("image", "inspect", "caddy:2-alpine", "--format", "{{.Id}}"); err != nil {
		t.Fatalf("fixture needs the already-pulled image: %v %s", err, output)
	}
	composePath := filepath.Join(root, "compose.yaml")
	webRoot := filepath.Join(root, "web")
	if err := os.Mkdir(webRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webRoot, "index.html"), []byte("import fixture is serving\n"), 0644); err != nil {
		t.Fatal(err)
	}
	compose := `services:
  web:
    image: caddy:2-alpine
    command: [caddy, file-server, --root, /www, --listen, ":8080"]
    ports: ["127.0.0.1::8080"]
    volumes: ["./web:/www:ro", "caddy_data:/data", "caddy_config:/config"]
    environment:
      FIXTURE_PASSWORD: import-fixture-secret-retained-in-place
    restart: unless-stopped
  worker:
    image: caddy:2-alpine
    command: [sleep, "300"]
    volumes: ["persistent:/fixture-data"]
    restart: unless-stopped
  paused_one:
    image: caddy:2-alpine
    command: [sleep, "300"]
  paused_two:
    image: caddy:2-alpine
    command: [sleep, "300"]
volumes:
  persistent: {}
  caddy_data: {}
  caddy_config: {}
`
	if err := os.WriteFile(composePath, []byte(compose), 0600); err != nil {
		t.Fatal(err)
	}
	composeArgs := []string{"compose", "--project-name", project, "--file", composePath}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := hostexec.CommandInDir(ctx, root, "docker", append(composeArgs, "down", "--volumes", "--remove-orphans")...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("clean fixture %s: %v %s", project, err, output)
		}
	})
	if output, err := command(append(composeArgs, "up", "-d", "--pull", "never")...); err != nil {
		t.Fatalf("create fixture: %v %s", err, output)
	}
	if output, err := command(append(composeArgs, "stop", "--timeout", "1", "paused_one", "paused_two")...); err != nil {
		t.Fatalf("stop fixture services: %v %s", err, output)
	}

	s := testServer(t)
	s.modules.docker = dockerx.New("unix:///var/run/docker.sock")
	s.modules.pm2, s.modules.systemd = nil, nil
	s.Cfg.ComposeRoots, s.Cfg.DeployRoots = []string{root}, []string{root}
	s.modules.deployPlanning = deploy.NewPlanningStore(s.Store, s.Sealer, []string{root})
	s.modules.deploySources = deploy.NewHostSourceAnalyzer([]string{root}, []string{root}, filepath.Join(s.Cfg.DataDir, "detect"), s.modules.docker, s.modules.deployPlanning)
	s.modules.deployPreflight = deploy.NewHostPreflightObserver([]string{root}, s.Cfg.DataDir, s.modules.docker)
	c := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "workload-live-proof", auth.RoleAdmin)}
	containers, err := s.modules.docker.ListContainersWithLabels(t.Context(), map[string]string{"com.docker.compose.project": project})
	if err != nil || len(containers) != 4 {
		t.Fatalf("fixture containers=%#v err=%v", containers, err)
	}
	ids := []string{}
	address := ""
	workerID := ""
	for _, container := range containers {
		ids = append(ids, container.ID)
		if container.ComposeSvc == "worker" {
			workerID = container.ID
		}
		if container.ComposeSvc == "web" {
			for _, port := range container.Ports {
				if port.PublicPort > 0 {
					address = "http://127.0.0.1:" + strconv.Itoa(int(port.PublicPort)) + "/"
				}
			}
		}
	}
	if address == "" {
		t.Fatalf("fixture web port was not published on loopback: %#v", containers)
	}
	if workerID == "" {
		t.Fatal("fixture worker was not found")
	}
	dataCommand := hostexec.CommandInDir(t.Context(), root, "docker", "exec", "-i", workerID, "busybox", "tee", "/fixture-data/retained.txt")
	dataCommand.Stdin = strings.NewReader("persistent fixture data remains\n")
	if output, err := dataCommand.CombinedOutput(); err != nil {
		t.Fatalf("write owned fixture data: %v %s", err, output)
	}
	snapshot := func() []byte {
		t.Helper()
		output, err := command(append([]string{"inspect"}, ids...)...)
		if err != nil {
			t.Fatalf("inspect fixture: %v %s", err, output)
		}
		var entries []map[string]json.RawMessage
		if err := json.Unmarshal(output, &entries); err != nil {
			t.Fatal(err)
		}
		stable := []map[string]json.RawMessage{}
		for _, entry := range entries {
			fields := map[string]json.RawMessage{}
			for _, key := range []string{"Id", "Name", "Config", "HostConfig", "Mounts", "NetworkSettings", "State", "RestartCount"} {
				fields[key] = entry[key]
			}
			stable = append(stable, fields)
		}
		encoded, err := json.Marshal(stable)
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	before := snapshot()
	beforeFile, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{Timeout: time.Second}
	checkHTTP := func() bool {
		response, err := httpClient.Get(address)
		if err != nil {
			return false
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		return err == nil && response.StatusCode == http.StatusOK && string(body) == "import fixture is serving\n"
	}
	if !checkHTTP() {
		t.Fatal("fixture HTTP service is not ready")
	}
	var requests, failures atomic.Int32
	stop, finished := make(chan struct{}), make(chan struct{})
	var stopOnce sync.Once
	stopRequests := func() { stopOnce.Do(func() { close(stop) }); <-finished }
	go func() {
		defer close(finished)
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				requests.Add(1)
				if !checkHTTP() {
					failures.Add(1)
				}
			}
		}
	}()
	t.Cleanup(stopRequests)
	inspected := doPlanningJSON(t, c, http.MethodPost, "/api/v1/deploy/import/inspect", map[string]any{"key": "stack:" + project})
	if inspected.Code != http.StatusOK {
		t.Fatalf("inspect = %d %s", inspected.Code, inspected.Body.String())
	}
	var candidate deploy.WorkloadCandidate
	decodePlanningResponse(t, inspected.Body.Bytes(), &candidate)
	if candidate.Total != 4 || candidate.Running != 2 || len(candidate.Services) != 4 || !candidate.ConfigurationAvailable {
		t.Fatalf("fixture not detected accurately: %#v", candidate)
	}
	if strings.Contains(inspected.Body.String(), "import-fixture-secret-retained-in-place") {
		t.Fatal("source environment leaked into import response")
	}
	recovered := doPlanningJSON(t, c, http.MethodPost, "/api/v1/deploy/import/recover", map[string]any{"key": candidate.Key, "name": project, "digest": candidate.Digest})
	if recovered.Code != http.StatusCreated {
		for _, id := range ids {
			if captured, err := s.modules.docker.CaptureAdoptionContainer(t.Context(), id); err == nil {
				t.Logf("fixture writable changes %s: %+v", captured.Inspection.Name, captured.Changes)
			}
		}
		t.Fatalf("recover = %d %s", recovered.Code, recovered.Body.String())
	}
	if strings.Contains(recovered.Body.String(), "import-fixture-secret-retained-in-place") {
		t.Fatal("capture leaked into recovered draft")
	}
	var draft deploy.Draft
	decodePlanningResponse(t, recovered.Body.Bytes(), &draft)
	checked := doPlanningJSON(t, c, http.MethodPost, "/api/v1/deploy/drafts/"+draft.ID+"/preflight", map[string]any{"revision": draft.Revision})
	if checked.Code != http.StatusOK {
		t.Fatalf("preflight = %d %s", checked.Code, checked.Body.String())
	}
	var review struct {
		Draft deploy.Draft `json:"draft"`
	}
	decodePlanningResponse(t, checked.Body.Bytes(), &review)
	draft = review.Draft
	ack := []string{}
	for _, finding := range draft.Findings {
		if finding.Severity == deploy.PreflightWarning {
			ack = append(ack, finding.Code)
		}
	}
	adopted := doPlanningJSON(t, c, http.MethodPost, "/api/v1/deploy/import/adopt", importAdoptRequest{DraftID: draft.ID, Revision: draft.Revision, AcknowledgedWarnings: ack})
	if adopted.Code != http.StatusCreated {
		t.Fatalf("adopt = %d %s", adopted.Code, adopted.Body.String())
	}
	var result deploy.DraftCommitResult
	decodePlanningResponse(t, adopted.Body.Bytes(), &result)
	stopRequests()
	after := snapshot()
	if !bytes.Equal(before, after) {
		t.Fatal("import changed container identity, configuration, mounts, networks, ports, restart counters or start times")
	}
	afterFile, err := os.ReadFile(composePath)
	if err != nil || !bytes.Equal(beforeFile, afterFile) {
		t.Fatal("import changed source Compose configuration")
	}
	if output, err := command("exec", workerID, "busybox", "cat", "/fixture-data/retained.txt"); err != nil || string(output) != "persistent fixture data remains\n" {
		t.Fatalf("persistent fixture data changed: %v", err)
	}
	if !checkHTTP() || failures.Load() != 0 {
		t.Fatalf("HTTP interruptions during import: %d/%d", failures.Load(), requests.Load())
	}
	for _, table := range []string{"deploy_runs", "deploy_releases", "deploy_release_runtimes", "deploy_variable_revisions"} {
		var count int
		if err := s.Store.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count == 0 {
			t.Fatalf("adoption lacks %s rows=%d err=%v", table, count, err)
		}
	}
	var queued int
	if err := s.Store.DB.QueryRow(`SELECT count(*) FROM deploy_runs WHERE state NOT IN ('succeeded','failed','cancelled')`).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("adoption enqueued a runtime change: %d %v", queued, err)
	}
	proof := map[string]any{
		"project": project, "importedProjectId": result.ProjectID, "containers": 4, "running": 2,
		"containerIdentityConfigurationMountNetworkPortStartTimesUnchanged": true,
		"sourceComposeUnchanged": true, "environmentValuesCopiedAndSealed": true, "completedMigrationRuns": 1, "deploymentRunsEnqueued": 0,
		"persistentVolumeBytesUnchanged": true,
		"httpRequestsDuringImport":       requests.Load(), "httpFailures": failures.Load(),
	}
	digest := sha256.Sum256(before)
	proof["beforeAndAfterDockerSnapshotSHA256"] = hex.EncodeToString(digest[:])
	encoded, _ := json.MarshalIndent(proof, "", "  ")
	t.Log(string(encoded))
	if destination := os.Getenv("JD_WORKLOAD_IMPORT_EVIDENCE_DIR"); destination != "" {
		if err := os.MkdirAll(destination, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(destination, "live-workload-adoption.json"), append(encoded, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
}
