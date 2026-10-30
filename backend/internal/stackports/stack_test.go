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
	"time"

	procnet "github.com/shirou/gopsutil/v4/net"
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

func listening(pid int32, port uint32) procnet.ConnectionStat {
	return procnet.ConnectionStat{Status: "LISTEN", Pid: pid, Laddr: procnet.Addr{IP: "127.0.0.1", Port: port}}
}

// An unprivileged lifecycle container cannot see which process holds the
// privileged backend's socket. That must not read as somebody else's port.
func TestListenerWithUnknownOwnerOnADeclaredPortBelongsToItsService(t *testing.T) {
	owned, foreign := map[int]string{}, map[int]bool{}
	classifyListeners([]procnet.ConnectionStat{
		listening(0, 8080),  // the backend, unreadable
		listening(0, 9999),  // unreadable and declared by nobody
		listening(77, 8443), // visibly the proxy
		listening(55, 7000), // visibly somebody else
	}, map[int32]string{77: "proxy"}, map[int]string{8080: "backend"}, owned, foreign)
	if owned[8080] != "backend" || foreign[8080] {
		t.Fatalf("backend: owner=%q foreign=%v", owned[8080], foreign[8080])
	}
	if owned[8443] != "proxy" || foreign[8443] {
		t.Fatalf("proxy: owner=%q foreign=%v", owned[8443], foreign[8443])
	}
	if !foreign[9999] || !foreign[7000] {
		t.Fatalf("foreign listeners lost: %v", foreign)
	}
}

// The declaration is only a fallback for a socket whose owner cannot be read.
func TestVisibleOwnerOnADeclaredPortStaysForeign(t *testing.T) {
	owned, foreign := map[int]string{}, map[int]bool{}
	classifyListeners([]procnet.ConnectionStat{listening(55, 8080)}, map[int32]string{},
		map[int]string{8080: "backend"}, owned, foreign)
	if owned[8080] != "" || !foreign[8080] {
		t.Fatalf("owner=%q foreign=%v", owned[8080], foreign[8080])
	}
}

func TestDeclaredPortReadsOnlyTheServicesOwnVariable(t *testing.T) {
	container := func(env ...string) inspectedContainer {
		var c inspectedContainer
		c.Config.Env = env
		return c
	}
	restarting := container("JD_ADDR=127.0.0.1:8080")
	restarting.State.Restarting = true
	for _, test := range []struct {
		name    string
		c       inspectedContainer
		service string
		want    int
	}{
		{"backend", container("JD_MASTER_KEY=x", "JD_ADDR=127.0.0.1:8080"), "backend", 8080},
		{"proxy", container("JD_BACKEND_PORT=8080", "JD_PORT=8443"), "proxy", 8443},
		{"proxy ignores the backend's address", container("JD_ADDR=127.0.0.1:8080"), "proxy", 0},
		{"backend ignores the proxy's port", container("JD_PORT=8443"), "backend", 0},
		{"frontend has no declaration", container("JD_PORT=8443"), "frontend", 0},
		{"malformed address", container("JD_ADDR=8080"), "backend", 0},
		{"out of range", container("JD_PORT=70000"), "proxy", 0},
		{"last assignment wins", container("JD_PORT=1", "JD_PORT=8443"), "proxy", 8443},
		{"restarting", restarting, "backend", 0},
	} {
		if got := declaredPort(test.c, test.service); got != test.want {
			t.Errorf("%s: got %d, want %d", test.name, got, test.want)
		}
	}
}

// Through Docker's own JSON, and only for this checkout: the listener here has a
// visible owner, so it stays foreign even though the backend declares it.
func TestObserveDecodesTheEnvironmentWithoutOverrulingAVisibleOwner(t *testing.T) {
	dir := t.TempDir()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	script := "#!/bin/sh\ncase \"$1\" in\nps) echo own ;;\ninspect) cat \"$JD_STACKPORT_TEST_FIXTURE/inspect.json\" ;;\ntop) echo PID; echo 1 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("JD_STACKPORT_TEST_FIXTURE", dir)
	compose := filepath.Join(dir, "docker-compose.yml")
	raw, _ := json.Marshal([]any{map[string]any{"Id": "own", "State": map[string]any{"Running": true}, "Config": map[string]any{
		"Env":    []string{fmt.Sprintf("JD_ADDR=127.0.0.1:%d", port)},
		"Labels": map[string]string{"com.docker.compose.service": "backend", "com.docker.compose.project.working_dir": dir, "com.docker.compose.project.config_files": compose},
	}, "HostConfig": map[string]any{"NetworkMode": "host"}}})
	if err := os.WriteFile(filepath.Join(dir, "inspect.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	owned, foreign, err := observe(context.Background(), dir, compose)
	if err != nil {
		t.Fatal(err)
	}
	if owned[port] != "" || !foreign[port] {
		t.Fatalf("owner=%q foreign=%v", owned[port], foreign[port])
	}
}

func backupsIn(t *testing.T, dir string) []string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(dir, backupPrefix+"*"))
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func oldBackups(t *testing.T, dir string, count int) []string {
	t.Helper()
	var paths []string
	for i := range count {
		path := filepath.Join(dir, fmt.Sprintf("%sold%02d", backupPrefix, i))
		if err := os.WriteFile(path, []byte("JD_MASTER_KEY=old\n"), 0600); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(-time.Hour - time.Duration(count-i)*time.Minute)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	return paths
}

func TestReconcilePrunesToTheNewestBackups(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("JD_PORT=8443\nJD_BACKEND_PORT=8080\nJD_FRONTEND_PORT=3000\n"), 0600); err != nil {
		t.Fatal(err)
	}
	old := oldBackups(t, dir, 8)
	unrelated := filepath.Join(dir, ".env.jd-previous")
	if err := os.WriteFile(unrelated, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	observe := func(context.Context, string, string) (map[int]string, map[int]bool, error) {
		return nil, map[int]bool{8443: true}, nil
	}
	selected, err := reconcile(context.Background(), dir, "", &bytes.Buffer{}, observe, func(string, string, int) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	found := backupsIn(t, dir)
	if len(found) != keptBackups {
		t.Fatalf("kept %d backups: %v", len(found), found)
	}
	if _, err := os.Stat(selected.Backup); err != nil {
		t.Fatal("the backup just written was pruned")
	}
	for _, path := range old[len(old)-keptBackups+1:] {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("a newer backup was pruned: %s", path)
		}
	}
	for _, path := range old[:len(old)-keptBackups+1] {
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("an older backup was kept: %s", path)
		}
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatal("another kind of backup was removed")
	}
}

// Backups left by earlier moves go even once the ports have stopped moving.
func TestReconcilePrunesWhenNothingMoved(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("JD_PORT=8443\nJD_BACKEND_PORT=8080\nJD_FRONTEND_PORT=3000\n"), 0600); err != nil {
		t.Fatal(err)
	}
	oldBackups(t, dir, 8)
	observe := func(context.Context, string, string) (map[int]string, map[int]bool, error) { return nil, nil, nil }
	selected, err := reconcile(context.Background(), dir, "", &bytes.Buffer{}, observe, func(string, string, int) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if selected.Changed || selected.Backup != "" {
		t.Fatalf("selection: %+v", selected)
	}
	if found := backupsIn(t, dir); len(found) != keptBackups {
		t.Fatalf("kept %d backups", len(found))
	}
}

// selfcfg keeps the first backup of a Start as its rollback file. Even with the
// directory already full, the most moves a Start can make leave that file alone.
func TestEveryMoveOfAStartKeepsTheFirstBackup(t *testing.T) {
	if keptBackups < 3 {
		t.Fatalf("keptBackups = %d; a Start reconciles three times", keptBackups)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("JD_PORT=8443\nJD_BACKEND_PORT=8080\nJD_FRONTEND_PORT=3000\n"), 0600); err != nil {
		t.Fatal(err)
	}
	oldBackups(t, dir, 10)
	busy := 8443
	first := ""
	for attempt := range 3 {
		observe := func(context.Context, string, string) (map[int]string, map[int]bool, error) {
			return nil, map[int]bool{busy: true}, nil
		}
		selected, err := reconcile(context.Background(), dir, "", &bytes.Buffer{}, observe, func(string, string, int) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		if !selected.Changed {
			t.Fatalf("attempt %d changed nothing", attempt)
		}
		if first == "" {
			first = selected.Backup
		}
		busy = selected.Port
		if _, err := os.Stat(first); err != nil {
			t.Fatalf("attempt %d removed the rollback file", attempt)
		}
	}
	raw, err := os.ReadFile(first)
	if err != nil || !strings.Contains(string(raw), "JD_PORT=8443") {
		t.Fatalf("rollback file = %q, %v", raw, err)
	}
}
