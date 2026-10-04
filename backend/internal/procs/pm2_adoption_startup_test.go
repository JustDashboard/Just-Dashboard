package procs

import (
	"encoding/json"
	"os"
	"os/user"
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

func TestPM2MigrationBlocksKnownManagerEnvironmentDriftAndWarnsForOpaqueArgv(t *testing.T) {
	configuration := json.RawMessage(`[{"pm_id":7,"exec_interpreter":"node","TOKEN":"original-private-value","env":{"TOKEN":"original-private-value","PORT":"3000","metadata":{"not":"an environment value"}}}]`)
	for _, fixture := range []struct {
		environment map[string]string
		drift       bool
	}{
		{map[string]string{"TOKEN": "original-private-value", "PORT": "3000", "exec_interpreter": "node"}, false},
		{map[string]string{"TOKEN": "changed-private-value", "PORT": "3000", "exec_interpreter": "node"}, true},
		{map[string]string{"TOKEN": "original-private-value", "exec_interpreter": "node"}, true},
		{map[string]string{"TOKEN": "original-private-value", "PORT": "3000", "exec_interpreter": "different-node"}, true},
	} {
		capture := &HostWorkloadCapture{Manager: "pm2", OriginalConfig: configuration, Blockers: []string{}, Processes: []HostProcessCapture{{ID: 7, PID: 314, CreateTime: 1234, State: "online"}}}
		blockPM2ManagerRuntimeDrift(capture, 7, fixture.environment)
		if (len(capture.Blockers) > 0) != fixture.drift || capture.Processes[0].PID != 314 {
			t.Fatal("known manager/live startup mismatch was not handled without process mutation")
		}
		if strings.Contains(strings.Join(capture.Blockers, " "), "private-value") {
			t.Fatal("private environment values entered the blocker")
		}
	}
	capture, err := parsePM2Capture([]byte(`[{"pm_id":7,"name":"owned","pm2_env":{"namespace":"default","pm_exec_path":"/srv/owned/server.js","pm_cwd":"/srv/owned","exec_interpreter":"node","exec_mode":"fork_mode","env":{}}}]`), &user.User{Username: "ubuntu", Uid: "1000", Gid: "1000"}, "default", "owned")
	if err != nil || !strings.Contains(strings.Join(capture.Warnings, " "), "cannot universally attest the running argument boundaries") {
		t.Fatal("PM2 manager definition was presented as attested running argv")
	}
}
