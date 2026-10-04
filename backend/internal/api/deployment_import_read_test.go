package api

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
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
	s.Cfg.DeployRoots = []string{"/opt", "/srv", "/home", "/tmp"}
	s.Cfg.ComposeRoots = []string{}
	if s.modules.docker != nil {
		s.modules.docker.Close()
	}
	s.modules.docker = dockerx.New("unix:///var/run/docker.sock")
	listener, err := net.Listen("tcp", "127.0.0.1:44119")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: s.Routes(), ReadHeaderTimeout: 5 * time.Second}
	t.Cleanup(func() { server.Close() })
	go server.Serve(listener)
	cookie := strings.SplitN(c.cookie, "=", 2)
	ready, _ := json.Marshal(map[string]string{
		"url": "http://" + listener.Addr().String(), "cookieName": cookie[0],
		"cookieValue": cookie[1], "stopFile": filepath.Join(directory, "stop"),
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
