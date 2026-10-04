package procs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPM2MigrationChecksBothStartupListsByLogicalApplication(t *testing.T) {
	for _, filename := range []string{"dump.pm2", "dump.pm2.bak"} {
		t.Run(filename, func(t *testing.T) {
			root := t.TempDir()
			home := pm2Home{home: root, daemonDir: root}
			before, blockers := capturePM2Startup(home, "default", "app")
			if len(blockers) != 0 {
				t.Fatal("absent saved startup authority blocked a private daemon")
			}
			content := `[{"name":"other","namespace":"default","pm_id":7},{"name":"app","namespace":"default","pm_id":987,"env":{"TOKEN":"owned-private-startup-value"}}]`
			path := filepath.Join(root, filename)
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			after, blockers := capturePM2Startup(home, "default", "app")
			if len(blockers) == 0 || string(before) == string(after) {
				t.Fatal("saved startup entry with a changed numeric ID was not fenced")
			}
			capture := &HostWorkloadCapture{StartupEvidence: after, ConfigurationDigest: captureDigest(after)}
			public, _ := json.Marshal(capture)
			if strings.Contains(string(public), "owned-private-startup-value") || strings.Contains(string(public), "startupEvidence") {
				t.Fatal("private saved startup settings entered the public capture")
			}
			unchanged, err := os.ReadFile(path)
			if err != nil || string(unchanged) != content {
				t.Fatal("startup inspection changed the existing saved list")
			}
		})
	}
}

func TestPM2MigrationAllowsVerifiedUnrelatedStartupEntries(t *testing.T) {
	root := t.TempDir()
	home := pm2Home{home: root, daemonDir: root}
	before, _ := capturePM2Startup(home, "default", "app")
	content := `[{"name":"other","namespace":"default"},{"name":"app","namespace":"other"}]`
	if err := os.WriteFile(filepath.Join(root, "dump.pm2"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	after, blockers := capturePM2Startup(home, "default", "app")
	if len(blockers) != 0 || string(before) != string(after) {
		t.Fatal("another application's valid saved startup entry changed this app's authority")
	}
}

func TestPM2MigrationRefusesUnknownOrUnreadableStartupAuthority(t *testing.T) {
	for _, kind := range []string{"malformed", "unknown row", "nonarray", "unreadable", "symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			home := pm2Home{home: root, daemonDir: root}
			path := filepath.Join(root, "dump.pm2.bak")
			var err error
			switch kind {
			case "malformed":
				err = os.WriteFile(path, []byte("not valid JSON"), 0600)
			case "unknown row":
				err = os.WriteFile(path, []byte(`[{}]`), 0600)
			case "nonarray":
				err = os.WriteFile(path, []byte(`null`), 0600)
			case "unreadable":
				err = os.WriteFile(path, []byte(`[]`), 0000)
			case "symlink":
				err = os.Symlink("unverified-dump", path)
			case "directory":
				err = os.Mkdir(path, 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			_, blockers := capturePM2Startup(home, "default", "app")
			if len(blockers) == 0 {
				t.Fatal("unknown saved startup authority was treated as absent")
			}
		})
	}
}
