package stackports

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReconcileMovesForeignPortsAndPreservesOwnPorts(t *testing.T) {
	dir := t.TempDir()
	original := "# operator comment\nJD_SITE=localhost\nJD_PORT=8443\nJD_BACKEND_PORT=8080\nJD_FRONTEND_PORT=3000\nJD_MASTER_KEY=private-test-value\n"
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	observe := func(context.Context, string, string) (map[int]string, map[int]bool, error) {
		return map[int]string{8080: "backend", 3000: "frontend"}, map[int]bool{8443: true, 8444: true}, nil
	}
	var out bytes.Buffer
	selected, err := reconcile(context.Background(), dir, "", &out, observe, func(address, protocol string, port int) error {
		if address != "127.0.0.1" || protocol != "tcp" {
			t.Fatalf("unexpected binding %s/%s", address, protocol)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Port != 8445 || selected.BackendPort != 8080 || selected.FrontendPort != 3000 {
		t.Fatalf("selection: %+v", selected)
	}
	if selected.Endpoint != "https://localhost:8445" || selected.Health() != "http://127.0.0.1:8080/healthz" {
		t.Fatalf("consumers: %+v", selected)
	}
	saved, _ := os.ReadFile(path)
	if string(saved) != strings.Replace(original, "JD_PORT=8443", "JD_PORT=8445", 1) {
		t.Fatalf("unrelated configuration changed")
	}
	backup, _ := os.ReadFile(selected.Backup)
	if string(backup) != original {
		t.Fatal("original configuration not backed up")
	}
	if strings.Contains(out.String(), "private-test-value") {
		t.Fatal("secret in transcript")
	}
	stat, _ := os.Stat(path)
	if stat.Mode().Perm() != 0600 {
		t.Fatal("configuration permissions")
	}
}

func TestReconcileReservesAllThreeChoicesBeforeStartup(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("JD_PORT=3000\nJD_BACKEND_PORT=3000\nJD_FRONTEND_PORT=3000\n"), 0600); err != nil {
		t.Fatal(err)
	}
	observe := func(context.Context, string, string) (map[int]string, map[int]bool, error) { return nil, nil, nil }
	selected, err := reconcile(context.Background(), dir, "", &bytes.Buffer{}, observe, func(string, string, int) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if selected.Port != 3000 || selected.BackendPort != 3001 || selected.FrontendPort != 3002 {
		t.Fatalf("duplicate choices: %+v", selected)
	}
}

func TestReconcileFailsBeforeWritingWhenOwnershipIsUnavailable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	raw := []byte("JD_PORT=8443\n")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	_, err := reconcile(context.Background(), dir, "", &bytes.Buffer{}, func(context.Context, string, string) (map[int]string, map[int]bool, error) {
		return nil, nil, fmt.Errorf("Docker unavailable")
	}, nil)
	if err == nil {
		t.Fatal("unknown ownership accepted")
	}
	saved, _ := os.ReadFile(path)
	if !bytes.Equal(saved, raw) {
		t.Fatal("configuration changed on failed observation")
	}
}

func TestDockerOwnershipRequiresThisCheckoutAndComposeFile(t *testing.T) {
	dir := t.TempDir()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	script := `#!/bin/sh
case "$1" in
ps) echo own-container ;;
inspect) cat "$JD_STACKPORT_TEST_FIXTURE/inspect.json" ;;
top) echo PID; echo "$JD_STACKPORT_TEST_PID" ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("JD_STACKPORT_TEST_FIXTURE", dir)
	t.Setenv("JD_STACKPORT_TEST_PID", fmt.Sprint(os.Getpid()))
	writeEvidence := func(checkout, compose string) {
		t.Helper()
		raw, _ := json.Marshal([]any{map[string]any{"Id": "own-container", "State": map[string]any{"Running": true}, "Config": map[string]any{"Labels": map[string]string{"com.docker.compose.service": "backend", "com.docker.compose.project.working_dir": checkout, "com.docker.compose.project.config_files": compose}}, "HostConfig": map[string]any{"NetworkMode": "host"}}})
		if err := os.WriteFile(filepath.Join(dir, "inspect.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	compose := filepath.Join(dir, "docker-compose.yml")
	writeEvidence(dir, compose)
	owned, foreign, err := observe(context.Background(), dir, compose)
	if err != nil {
		t.Fatal(err)
	}
	if owned[port] != "backend" || foreign[port] {
		t.Fatalf("own listener misidentified: owner=%q foreign=%v", owned[port], foreign[port])
	}
	writeEvidence(dir, filepath.Join(dir, "another-compose.yml"))
	owned, foreign, err = observe(context.Background(), dir, compose)
	if err != nil {
		t.Fatal(err)
	}
	if owned[port] != "" || !foreign[port] {
		t.Fatalf("another project borrowed ownership: owner=%q foreign=%v", owned[port], foreign[port])
	}
}

func TestStartRecoversACompetingBindAndKeepsOriginalBackup(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("JD_PORT=38443\nJD_BACKEND_PORT=38080\nJD_FRONTEND_PORT=33000\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var selections []Selection
	attempts := 0
	var competitor net.Listener
	defer func() {
		if competitor != nil {
			competitor.Close()
		}
	}()
	err := Start(context.Background(), dir, "", &bytes.Buffer{}, func() error {
		attempts++
		if attempts == 1 {
			var err error
			competitor, err = net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", selections[len(selections)-1].BackendPort))
			if err != nil {
				return err
			}
			return fmt.Errorf("port claimed during startup")
		}
		if selections[len(selections)-1].BackendPort == selections[0].BackendPort {
			return fmt.Errorf("port was not relocated")
		}
		return nil
	}, func(selected Selection) error { selections = append(selections, selected); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || len(selections) != 2 || !selections[1].Changed {
		t.Fatalf("attempts=%d selections=%+v", attempts, selections)
	}
	raw, err := os.ReadFile(selections[1].Backup)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), fmt.Sprintf("JD_BACKEND_PORT=%d", selections[0].BackendPort)) {
		t.Fatal("retry backup lost original binding")
	}
}
