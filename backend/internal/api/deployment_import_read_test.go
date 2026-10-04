package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/wsx"
	"github.com/gorilla/websocket"
)

func TestImportedProjectReadsPreserveMissingWorkloadAndRefuseRuns(t *testing.T) {
	c, s := newClient(t)
	s.modules.docker = nil
	s.modules.pm2 = nil
	s.modules.systemd = nil
	result, err := s.modules.deployPlanning.RegisterObservedWorkload(t.Context(), deploy.ObservedWorkloadRegistration{
		Name: "missing-import", ResourceKind: "compose_stack", ResourceID: "missing-import",
		SourceMode: deploy.SourceModeExistingStack, OwnerUsername: "tester",
		Observed: json.RawMessage(`{"key":"stack:missing-import","kind":"stack","name":"missing-import","resourceId":"missing-import","state":"partial","running":2,"total":4,"services":[],"warnings":[],"managerUrl":"/docker/stacks/missing-import"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	response := c.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d", result.ProjectID), "", nil)
	var detail struct {
		Deployment deploy.DeploymentSummary `json:"deployment"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &detail) != nil {
		t.Fatalf("project read = %d %s", response.Code, response.Body.String())
	}
	if detail.Deployment.PendingChanges || detail.Deployment.LiveReleaseID != 0 || detail.Deployment.ImportedWorkload == nil {
		t.Fatalf("import invented a deployment: %+v", detail.Deployment)
	}
	observed := detail.Deployment.ImportedWorkload
	if observed.State != "unavailable" || observed.Total != 4 || len(observed.Warnings) == 0 {
		t.Fatalf("missing manager lost retained evidence: %+v", observed)
	}
	for _, operation := range []string{"deploy", "force_build", "start", "stop", "restart", "redeploy"} {
		response := c.do(http.MethodPost,
			fmt.Sprintf("/api/v1/deploy/%d/environments/%d/runs", result.ProjectID, result.EnvironmentID),
			fmt.Sprintf(`{"operation":%q}`, operation), nil)
		if response.Code < 400 || response.Code >= 500 {
			t.Fatalf("%s was not refused before enqueue: %d %s", operation, response.Code, response.Body.String())
		}
	}
	var count int
	if err := s.Store.DB.QueryRow(`SELECT count(*) FROM deploy_runs WHERE project_id = ?`, result.ProjectID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("imported project created a run: count=%d err=%v", count, err)
	}
}

// An isolated database and real authenticated routes make the recording a
// native import proof without changing the installed dashboard or its apps.
func TestWorkloadImportBrowserEvidenceServer(t *testing.T) {
	directory := os.Getenv("JD_IMPORT_BROWSER_EVIDENCE_DIR")
	if directory == "" {
		t.Skip("set JD_IMPORT_BROWSER_EVIDENCE_DIR for the isolated native discovery server")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	c, s := newClient(t)
	s.Auth = auth.NewService(s.Store, s.Sealer, time.Hour, time.Hour, false)
	s.Authn.Svc = s.Auth
	s.Cfg.DeployRoots = []string{"/opt", "/srv", "/home", "/tmp"}
	s.Cfg.ComposeRoots = []string{}
	if s.modules.docker != nil {
		s.modules.docker.Close()
	}
	s.modules.docker = dockerx.New("unix:///var/run/docker.sock")
	// Rebind the read-only consumers built with the test server's original
	// client. Start remains unused: no engine or reconciler may act on this host.
	s.modules.dockerStats = s.modules.docker.NewStatsSampler()
	s.modules.dockerEvents = s.modules.docker.NewEventLog(s.Log)
	s.modules.metrics.WithContainers(s.modules.docker.NewStatsSampler())
	// The proof router serves HTTP on loopback. Preserve the normal same-origin
	// guard with its actual scheme so stats and log sockets can upgrade.
	s.WS = wsx.NewUpgrader(nil, false)
	// An isolated database still shares the real daemon. Its runtime label
	// namespace must never overlap an installed deployment's environment.
	if _, err := s.Store.DB.Exec(`INSERT INTO sqlite_sequence(name,seq) SELECT 'deploy_environments',? WHERE NOT EXISTS(SELECT 1 FROM sqlite_sequence WHERE name='deploy_environments')`, time.Now().UnixMicro()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`UPDATE sqlite_sequence SET seq=? WHERE name='deploy_environments'`, time.Now().UnixMicro()); err != nil {
		t.Fatal(err)
	}
	initialImages, err := s.modules.docker.ListImages(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	existingImages := map[string]bool{}
	for _, image := range initialImages {
		existingImages[image.ID] = true
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// Discard only images created by this temporary capture database. A
		// production app, volume, original image or pre-existing cache is never
		// part of fixture cleanup, and removal is never forced.
		_ = filepath.WalkDir(filepath.Join(s.Cfg.DataDir, "deployment-recovery"), func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil || entry.Name() != "manifest.json" || !entry.Type().IsRegular() {
				return nil
			}
			var manifest struct {
				ID           string `json:"id"`
				SourceDigest string `json:"sourceDigest"`
			}
			content, err := os.ReadFile(path)
			if err != nil || len(content) > 8192 || json.Unmarshal(content, &manifest) != nil || existingImages[manifest.ID] {
				return nil
			}
			image, err := s.modules.docker.InspectImage(ctx, manifest.ID)
			if err == nil && image.ID == manifest.ID && manifest.SourceDigest != "" && image.Labels["io.just-dashboard.adoption-source"] == manifest.SourceDigest {
				if _, err := s.modules.docker.RemoveImage(ctx, manifest.ID, false, false); err != nil {
					t.Errorf("remove only the evidence server's recovered image: %v", err)
				}
			}
			return nil
		})
	})
	s.modules.deployPlanning = deploy.NewPlanningStore(s.Store, s.Sealer, s.Cfg.DeployRoots)
	s.modules.deploySources = deploy.NewHostSourceAnalyzer(s.Cfg.DeployRoots, s.Cfg.ComposeRoots, filepath.Join(s.Cfg.DataDir, "deployment-detection"), s.modules.docker, s.modules.deployPlanning)
	s.modules.deployPreflight = deploy.NewHostPreflightObserver(s.Cfg.DeployRoots, s.Cfg.DataDir, s.modules.docker)
	s.modules.deployRuntime = deploy.NewDockerRuntimeOwner(s.modules.docker)
	s.modules.deployNative = deploy.NewNativeRuntimeOwner(s.modules.deployRuntime, s.modules.pm2, s.modules.systemd)
	listener, err := net.Listen("tcp", "127.0.0.1:44119")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: s.Routes(), ReadHeaderTimeout: 5 * time.Second}
	t.Cleanup(func() { server.Close() })
	go server.Serve(listener)
	containers, err := s.modules.docker.ListContainers(t.Context(), false)
	if err != nil {
		t.Fatal("the proof's current containers could not be read")
	}
	var logContainerID string
	for _, container := range containers {
		if container.ComposeStack == "bet-bot" && container.State == "running" {
			logContainerID = container.ID
			break
		}
	}
	if logContainerID == "" {
		t.Fatal("the native proof requires a running bet-bot container")
	}
	// Exercise the ordinary authenticated log route with one tail line. Log
	// contents are discarded; only the successful Docker stream metadata is kept.
	logURL := "ws://" + listener.Addr().String() + "/api/v1/logs/stream?source=" + url.QueryEscape("docker:"+logContainerID) + "&lines=1"
	logHeaders := http.Header{"Cookie": []string{c.cookie}, "Origin": []string{"http://" + listener.Addr().String()}}
	logContext, cancelLogs := context.WithTimeout(t.Context(), 10*time.Second)
	logSocket, _, err := websocket.DefaultDialer.DialContext(logContext, logURL, logHeaders)
	cancelLogs()
	if err != nil {
		t.Fatal("the proof's authenticated Docker log stream could not open")
	}
	defer logSocket.Close()
	_ = logSocket.SetReadDeadline(time.Now().Add(10 * time.Second))
	var logMeta struct {
		Type string `json:"type"`
		Data struct {
			Kind string `json:"kind"`
		} `json:"data"`
	}
	if err := logSocket.ReadJSON(&logMeta); err != nil || logMeta.Type != "meta" || logMeta.Data.Kind != "docker" {
		t.Fatal("the proof's bounded Docker log read did not return stream metadata")
	}
	logSocket.Close()
	cookie := strings.SplitN(c.cookie, "=", 2)
	ready, _ := json.Marshal(map[string]any{
		"url": "http://" + listener.Addr().String(), "cookieName": cookie[0],
		"cookieValue": cookie[1], "stopFile": filepath.Join(directory, "stop"),
		"dockerLogTailRead": true,
	})
	if err := os.WriteFile(filepath.Join(directory, "ready.json"), ready, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("Native workload import browser server is ready on loopback with a temporary database")
	deadline := time.NewTimer(15 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if _, err := os.Stat(filepath.Join(directory, "stop")); err == nil {
				return
			}
		case <-deadline.C:
			t.Fatal("evidence server was not stopped within 15 minutes")
		}
	}
}
