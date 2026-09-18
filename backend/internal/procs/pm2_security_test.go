package procs

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPM2IdentityRefusesAmbiguousNames(t *testing.T) {
	processes := []PM2Process{{Name: "app", DaemonID: "alice", ID: 0}, {Name: "app", DaemonID: "bob", ID: 0}, {Name: "app", DaemonID: "bob", ID: 1}}
	if _, err := selectPM2Target(processes, "app", "", -1); err == nil {
		t.Fatal("ambiguous daemon accepted")
	}
	if _, err := selectPM2Target(processes, "app", "bob", -1); err == nil {
		t.Fatal("ambiguous process id accepted")
	}
	target, err := selectPM2Target(processes, "app", "bob", 1)
	if err != nil || target.DaemonID != "bob" || target.ID != 1 {
		t.Fatalf("target %#v: %v", target, err)
	}
	if _, err := selectPM2Target(processes, "different", "bob", 1); err == nil {
		t.Fatal("name/id mismatch accepted")
	}
}
func TestPM2EnvironmentNeverInheritsDashboardSecrets(t *testing.T) {
	t.Setenv("JD_AUDIT_SECRET", "must-not-pass")
	for _, entry := range pm2Env(pm2Home{home: "/home/alice", bin: "/home/alice/.local/bin/pm2"}) {
		if strings.Contains(entry, "JD_AUDIT_SECRET") || strings.Contains(entry, "must-not-pass") {
			t.Fatal("inherited backend environment")
		}
	}
}
func TestPM2ChoosesNumericallyNewestNode(t *testing.T) {
	home := t.TempDir()
	for _, version := range []string{"v9.0.0", "v22.9.0", "v22.10.0"} {
		bin := filepath.Join(home, ".nvm", "versions", "node", version, "bin", "pm2")
		if err := os.MkdirAll(filepath.Dir(bin), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(bin, []byte("fixture"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if got := findPM2Bin(home); !strings.Contains(got, "v22.10.0") {
		t.Fatalf("selected %s", got)
	}
}

func TestCommandCaptureIsBoundedEvenThroughIOCopy(t *testing.T) {
	var output commandBuffer
	if _, err := io.Copy(&output, strings.NewReader(strings.Repeat("x", 5<<20))); err != nil {
		t.Fatal(err)
	}
	if !output.truncated || len(output.String()) != 4<<20 {
		t.Fatalf("output length %d, truncated %v", len(output.String()), output.truncated)
	}
}

func TestPM2DaemonCannotChooseItsReportedExecutionAccount(t *testing.T) {
	processes, err := parsePM2List([]byte(`[{"pm_id":1,"name":"app","pm2_env":{"username":"root","status":"online"}}]`), 100, "alice")
	if err != nil || len(processes) != 1 || processes[0].User != "alice" || processes[0].DaemonID != "alice" {
		t.Fatalf("untrusted daemon identity %#v: %v", processes, err)
	}
}
