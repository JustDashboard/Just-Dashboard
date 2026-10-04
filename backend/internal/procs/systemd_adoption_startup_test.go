package procs

import (
	"context"
	"encoding/json"
	"os"
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
