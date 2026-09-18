package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
)

func TestPM2MetadataCannotAuthorizeAnExternalLogPath(t *testing.T) {
	server := testServer(t)
	private := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(private, []byte("synthetic-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := server.checkPM2LogPaths(private); err == nil {
		t.Fatal("daemon metadata authorized external secret")
	}
	if err := server.modules.logs.Allow(private); err == nil {
		t.Fatal("failed request widened future readers' roots")
	}
	link := filepath.Join(server.Cfg.LogRoots[0], "linked.log")
	if err := os.Symlink(private, link); err != nil {
		t.Fatal(err)
	}
	if err := server.checkPM2LogPaths(link); err == nil {
		t.Fatal("symlink escaped configured log root")
	}
	valid := filepath.Join(server.Cfg.LogRoots[0], "valid.log")
	if err := os.WriteFile(valid, []byte("ordinary log\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := server.checkPM2LogPaths(valid, "/dev/null"); err != nil {
		t.Fatal(err)
	}
}
func TestUnifiedPM2LogIdentityPreservesDaemonAndProcess(t *testing.T) {
	first := procs.PM2Process{Name: "api:worker", DaemonID: "alice", ID: 4}
	second := procs.PM2Process{Name: "api:worker", DaemonID: "bob", ID: 4}
	if pm2LogIdentity(first) == pm2LogIdentity(second) {
		t.Fatal("source id aliases another daemon")
	}
	name, daemon, id, err := parsePM2LogIdentity(pm2LogIdentity(first))
	if err != nil || name != first.Name || daemon != first.DaemonID || id != first.ID {
		t.Fatalf("identity = %s %s %d: %v", name, daemon, id, err)
	}
	name, daemon, id, err = parsePM2LogIdentity("legacy-name")
	if err != nil || name != "legacy-name" || daemon != "" || id != -1 {
		t.Fatal("legacy identity changed")
	}
	for _, value := range []string{"alice/-1/app", "alice/no-id/app", "/4/app", "alice/4/"} {
		if _, _, _, err := parsePM2LogIdentity(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
}
