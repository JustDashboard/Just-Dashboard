package procs

import (
	"context"
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func simpleDisabledUnitProperties() map[string]string {
	return map[string]string{"Type": "exec", "User": "ubuntu", "Restart": "on-failure", "KillMode": "control-group", "KillSignal": "15", "TimeoutStopUSec": "90s", "UnitFileState": "disabled"}
}

func TestSystemdMigrationRefusesOriginalBootOrExternalActivationAuthority(t *testing.T) {
	unit := &Unit{Name: "app.service"}
	for _, state := range []string{"enabled", "enabled-runtime", "static", "alias", "indirect", "generated", "masked", ""} {
		t.Run("state "+state, func(t *testing.T) {
			properties := simpleDisabledUnitProperties()
			properties["UnitFileState"] = state
			capture := systemdCaptureProperties(unit, properties)
			if len(capture.Blockers) == 0 {
				t.Fatal("original reboot authority was silently left alongside Docker")
			}
		})
	}
	for _, field := range []string{"WantedBy", "RequiredBy", "TriggeredBy", "BoundBy", "UpheldBy", "ConsistsOf", "OnFailureOf", "OnSuccessOf"} {
		t.Run(field, func(t *testing.T) {
			properties := simpleDisabledUnitProperties()
			before, _ := json.Marshal(stableSystemdProperties(properties))
			properties[field] = "external-application.service"
			capture := systemdCaptureProperties(unit, properties)
			after, _ := json.Marshal(stableSystemdProperties(properties))
			if len(capture.Blockers) == 0 || string(before) == string(after) {
				t.Fatal("external activation authority was not blocked and fenced")
			}
		})
	}
}

func TestSystemdMigrationDirectiveReviewNeverDropsExplicitRuntimeSettings(t *testing.T) {
	for _, field := range []string{"MemoryMax", "CPUQuota", "TasksMax", "LimitNOFILE", "LimitMEMLOCK", "Nice", "CPUAffinity", "IOSchedulingPriority", "UMask", "RestartSec", "WatchdogSec", "UnknownFutureDirective"} {
		t.Run(field, func(t *testing.T) {
			blockers := systemdMigrationDirectiveBlockers(map[string]string{"unit": "[Service]\n" + field + "=owned-private-directive-value\n"})
			if len(blockers) == 0 {
				t.Fatal("explicit resource, scheduling or unknown unit setting was discarded")
			}
			if strings.Contains(strings.Join(blockers, "\n"), "owned-private-directive-value") {
				t.Fatal("private directive values entered the public blocker")
			}
		})
	}
	blockers := systemdMigrationDirectiveBlockers(map[string]string{"unit": "[Unit]\nDescription=Existing app\n[Service]\nType=exec\nUser=ubuntu\nGroup=1000\nWorkingDirectory=/srv/app\nExecStart=/usr/bin/node server.js\nEnvironment=TOKEN=owned-private-directive-value \\\n PORT=3000\nRestart=on-failure\nKillSignal=SIGTERM\nTimeoutStopSec=3s\n[Install]\nWantedBy=multi-user.target\n"})
	if len(blockers) != 0 {
		t.Fatal("the supported disabled Node unit recipe was rejected")
	}
}

func TestSystemdMigrationFindsInstalledUnloadedTriggersAndAliases(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "owned-startup.timer")
	if err := os.WriteFile(file, []byte("[Timer]\nUnit=owned-alias.service\n"), 0600); err != nil {
		t.Fatal(err)
	}
	aliasFile := filepath.Join(root, "owned.service")
	if err := os.WriteFile(aliasFile, []byte("[Service]\nExecStart=/usr/bin/node app.js\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"Triggers", "Unit", "Service", "Wants", "Requires", "BindsTo", "Upholds", "OnFailure", "OnSuccess", "default"} {
		t.Run(field, func(t *testing.T) {
			trigger := "owned-startup.timer"
			if field == "default" {
				trigger = "owned-alias.timer"
			}
			execute := func(_ context.Context, _ time.Duration, _ string, args ...string) (*CommandResult, error) {
				if args[0] == "list-unit-files" {
					return &CommandResult{Stdout: `[{"unit_file":"` + trigger + `","state":"enabled"},{"unit_file":"owned-alias.service","state":"alias"}]`}, nil
				}
				reference := ""
				if field != "default" {
					reference = field + "=owned-alias.service\n"
				}
				return &CommandResult{Stdout: "Id=" + trigger + "\nNames=" + trigger + "\nLoadState=loaded\nUnitFileState=enabled\nFragmentPath=" + file + "\n" + reference + "\nId=owned.service\nNames=owned.service owned-alias.service\nLoadState=loaded\nFragmentPath=" + aliasFile + "\n"}, nil
			}
			proof, blockers, err := captureInstalledSystemdStartup(context.Background(), execute, "owned.service", nil)
			if err != nil || len(blockers) == 0 || !strings.Contains(string(proof), trigger) {
				t.Fatal("installed unloaded startup reference was not blocked and fenced")
			}
		})
	}
}

func TestSystemdMigrationRefusesUnreadablePrivateStartupInventory(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "owned.timer")
	if err := os.WriteFile(file, []byte("[Timer]\nUnit=unrelated.service\nTOKEN=owned-private-startup-value\n"), 0000); err != nil {
		t.Fatal(err)
	}
	execute := func(_ context.Context, _ time.Duration, _ string, args ...string) (*CommandResult, error) {
		if args[0] == "list-unit-files" {
			return &CommandResult{Stdout: `[{"unit_file":"owned.timer","state":"enabled"}]`}, nil
		}
		return &CommandResult{Stdout: "Id=owned.timer\nNames=owned.timer\nLoadState=loaded\nFragmentPath=" + file + "\nUnit=unrelated.service\n"}, nil
	}
	proof, blockers, err := captureInstalledSystemdStartup(context.Background(), execute, "owned.service", nil)
	if err != nil || len(blockers) == 0 || string(proof) != `{"unverified":true}` {
		t.Fatal("unreadable startup authority was silently ignored")
	}
	if strings.Contains(strings.Join(blockers, " "), "owned-private-startup-value") {
		t.Fatal("private installed unit content entered public blockers")
	}
}

func TestSystemdMigrationInspectsOrdinaryServicesAndTemplateActivation(t *testing.T) {
	for _, fixture := range []struct {
		name, state, content string
		unverified           bool
	}{
		{"other.service", "enabled", "[Unit]\nWants=owned.service\n", false},
		{"other@.timer", "enabled", "[Timer]\nUnit=owned.service\n", false},
		{"other@.socket", "enabled", "[Socket]\nService=owned.service\n", false},
		{"other@.path", "disabled", "[Path]\nUnit=owned.service\n", false},
		{"other@.service", "enabled", "[Unit]\nRequires=owned.service\n", false},
		{"other@.timer", "enabled", "[Timer]\nUnit=%i.service\n", true},
	} {
		t.Run(fixture.name+fixture.state, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), fixture.name)
			if err := os.WriteFile(file, []byte(fixture.content), 0600); err != nil {
				t.Fatal(err)
			}
			execute := func(_ context.Context, _ time.Duration, _ string, args ...string) (*CommandResult, error) {
				switch args[0] {
				case "list-unit-files":
					return &CommandResult{Stdout: `[{"unit_file":"` + fixture.name + `","state":"` + fixture.state + `"}]`}, nil
				case "cat":
					return &CommandResult{Stdout: "# " + file + "\n" + fixture.content}, nil
				default:
					id, refs := fixture.name, "Wants=owned.service\n"
					if strings.Contains(fixture.name, "@.") {
						id, refs = "owned.service", ""
					}
					return &CommandResult{Stdout: "Id=" + id + "\nNames=" + id + "\nLoadState=loaded\nFragmentPath=" + file + "\n" + refs}, nil
				}
			}
			proof, blockers, err := captureInstalledSystemdStartup(context.Background(), execute, "owned.service", nil)
			if err != nil || len(blockers) == 0 {
				t.Fatal("installed service or template activation was ignored")
			}
			if fixture.unverified && string(proof) != `{"unverified":true}` {
				t.Fatal("ambiguous enabled template was not refused")
			}
			if !fixture.unverified && !strings.Contains(string(proof), fixture.name) {
				t.Fatal("activation evidence was not fenced")
			}
		})
	}
}

func TestSystemdStartupTemplateReferencesReserveInstanceFamilies(t *testing.T) {
	aliases := map[string]bool{"owned@production.service": true, "owned-alias@production.service": true}
	if !systemdStartupReferenceMatches("owned@.service", aliases) || !systemdStartupReferenceMatches("owned-alias@.service", aliases) || systemdStartupReferenceMatches("other@.service", aliases) {
		t.Fatal("socket or template authority lost its concrete service instance")
	}
}

func TestNativeCaptureWarnsAboutRetainedBaselineAuthorityAndBlocksVolatileUnits(t *testing.T) {
	pm2, err := parsePM2Capture([]byte(`[{"pm_id":7,"pid":123,"name":"owned","pm2_env":{"namespace":"default","status":"online","pm_exec_path":"/srv/owned/server.js","pm_cwd":"/srv/owned","exec_mode":"fork_mode","exec_interpreter":"node","env":{}}}]`), &user.User{Username: "ubuntu", Uid: "1000", Gid: "1000"}, "default", "owned")
	if err != nil || !strings.Contains(strings.Join(pm2.Warnings, " "), "daemon reset can lose unsaved application records") {
		t.Fatal("PM2 native record loss was not a required capture review warning")
	}
	for _, fixture := range []struct {
		fragment, dropins string
		volatile          bool
	}{
		{"/etc/systemd/system/owned.service", "", false},
		{"/run/systemd/system/owned.service", "", true},
		{"/var/run/systemd/system/owned.service", "", true},
		{"/etc/systemd/system/owned.service", "/run/systemd/system/owned.service.d/override.conf", true},
		{"/etc/systemd/system/owned.service", "/etc/systemd/system/owned.service.d/override.conf", false},
	} {
		properties := simpleDisabledUnitProperties()
		properties["DropInPaths"] = fixture.dropins
		capture := systemdCaptureProperties(&Unit{Name: "owned.service", Fragment: fixture.fragment}, properties)
		if (len(capture.Blockers) > 0) != fixture.volatile {
			t.Fatal("volatile manager authority was not refused independently of the Transient property")
		}
		if !strings.Contains(strings.Join(capture.Warnings, " "), "missing original manager authority causes restoration to refuse") {
			t.Fatal("systemd native authority retention was omitted from required capture review")
		}
	}
}

func TestSystemdMigrationRefusesNewerUnitWithAnUnchangedLivePID(t *testing.T) {
	root := t.TempDir()
	unit, dropin := filepath.Join(root, "owned.service"), filepath.Join(root, "override.conf")
	born := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	for _, file := range []string{unit, dropin} {
		if err := os.WriteFile(file, []byte("[Service]\nEnvironment=TOKEN=owned-private-value\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(file, born.Add(-time.Second), born.Add(-time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	process := HostProcessCapture{PID: 314, CreateTime: born.UnixMilli(), State: "active"}
	capture := &HostWorkloadCapture{Manager: "systemd", Processes: []HostProcessCapture{process}, Blockers: []string{}}
	blockKnownSystemdConfigurationDrift(capture, []string{unit, dropin})
	if len(capture.Blockers) != 0 {
		t.Fatal("configuration predating the running process was rejected")
	}
	if err := os.Chtimes(dropin, born.Add(2*time.Second), born.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	blockKnownSystemdConfigurationDrift(capture, []string{unit, dropin})
	if len(capture.Blockers) != 0 {
		t.Fatal("documented timestamp precision tolerance was rejected")
	}
	if err := os.Chtimes(dropin, born.Add(3*time.Second), born.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	blockKnownSystemdConfigurationDrift(capture, []string{unit, dropin})
	if len(capture.Blockers) == 0 || capture.Processes[0].PID != process.PID || capture.Processes[0].CreateTime != process.CreateTime {
		t.Fatal("newer manager configuration was not blocked without changing the live PID")
	}
	if strings.Contains(strings.Join(capture.Blockers, " "), "owned-private-value") {
		t.Fatal("unit private contents entered the blocker")
	}
}
