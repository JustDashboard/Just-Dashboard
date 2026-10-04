package api

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

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
