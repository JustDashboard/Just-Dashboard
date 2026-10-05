package procs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pm2StartupPlanFixture(t *testing.T) (*NativeStartupPlan, *HostWorkloadCapture, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	content := `[{"name":"other","namespace":"default","env":{"TOKEN":"unrelated-private-secret"}}, {"name":"owned","namespace":"default","pm_exec_path":"/srv/owned/server.js","pm_cwd":"/srv/owned","env":{"TOKEN":"selected-private-secret"}}, {"name":"owned","namespace":"other","env":{"TOKEN":"other-namespace-secret"}}]`
	for _, file := range []string{"dump.pm2", "dump.pm2.bak"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte(content), 0640); err != nil {
			t.Fatal(err)
		}
	}
	evidence, _ := capturePM2Startup(pm2Home{daemonDir: root}, "default", "owned")
	capture := &HostWorkloadCapture{Manager: "pm2", ResourceID: "fixture/default/owned", Name: "owned", Account: "fixture", UID: uint32(os.Getuid()), GID: uint32(os.Getgid()), SourcePath: "/srv/owned/server.js", SourceDirectory: "/srv/owned", StartupEvidence: evidence}
	plan, err := PreparePM2StartupHandoff(capture, root, "default")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"dump.pm2", "dump.pm2.bak"} {
		data, _ := os.ReadFile(filepath.Join(root, file))
		if string(data) != content {
			t.Fatal("read-only preparation changed startup authority")
		}
	}
	return plan, capture, root
}

func saveStartupJournal(journal NativeStartupJournal) NativeStartupJournal {
	data, _ := json.Marshal(journal)
	var copy NativeStartupJournal
	_ = json.Unmarshal(data, &copy)
	return copy
}

func TestNativePM2StartupHandoffRetiresAndRestoresOnlySelectedRows(t *testing.T) {
	plan, capture, root := pm2StartupPlanFixture(t)
	private, _ := json.Marshal(plan)
	if strings.Contains(string(private), "unrelated-private-secret") || strings.Contains(string(private), "other-namespace-secret") {
		t.Fatal("plan retained unrelated saved configuration")
	}
	summary, _ := json.Marshal(plan.Summary())
	if strings.Contains(string(summary), "selected-private-secret") || strings.Contains(string(summary), root) {
		t.Fatal("public startup summary contains private authority")
	}
	var journal NativeStartupJournal
	writes := 0
	persist := func(value NativeStartupJournal) error { writes++; return nil }
	if err := RetireStartupHandoff(t.Context(), plan, &journal, persist); err != nil {
		t.Fatal(err)
	}
	if writes < 6 || journal.Phase != "retired" {
		t.Fatal("handoff did not publish durable intent and outcomes")
	}
	for _, file := range []string{"dump.pm2", "dump.pm2.bak"} {
		path := filepath.Join(root, file)
		data, _ := os.ReadFile(path)
		rows, err := parseStartupRows(data)
		if err != nil || len(rows) != 2 {
			t.Fatal("selected row removal is incomplete")
		}
		if !strings.Contains(string(data), "unrelated-private-secret") || !strings.Contains(string(data), "other-namespace-secret") || strings.Contains(string(data), "selected-private-secret") {
			t.Fatal("another application's saved entry changed")
		}
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0640 {
			t.Fatal("original saved-list permissions lost")
		}
	}
	fresh := *capture
	fresh.StartupEvidence, _ = capturePM2Startup(pm2Home{daemonDir: root}, "default", "owned")
	if err := VerifyCapturedStartup(&fresh, plan, journal); err != nil {
		t.Fatal("verified retirement was rejected", err)
	}
	if err := RetireStartupHandoff(t.Context(), plan, &journal, persist); err != nil {
		t.Fatal("retirement retry is not idempotent", err)
	}
	if err := RestoreStartupHandoff(t.Context(), plan, &journal, persist); err != nil {
		t.Fatal(err)
	}
	fresh.StartupEvidence, _ = capturePM2Startup(pm2Home{daemonDir: root}, "default", "owned")
	if err := VerifyCapturedStartup(&fresh, plan, journal); err != nil {
		t.Fatal("restored authority rejected", err)
	}
	if !equalStartupJSON(fresh.StartupEvidence, capture.StartupEvidence) {
		t.Fatal("rollback changed selected startup settings")
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 2 {
		t.Fatal("completed handoff left shared startup backup artifacts")
	}
}

func TestNativePM2StartupHandoffResumesInterruptedPublishFromDurableJournal(t *testing.T) {
	plan, _, root := pm2StartupPlanFixture(t)
	var journal, saved NativeStartupJournal
	failed := false
	persist := func(value NativeStartupJournal) error {
		if !failed && len(value.Actions) > 0 && value.Actions[0].Phase == "retired" {
			failed = true
			return errors.New("owned journal write failure")
		}
		saved = saveStartupJournal(value)
		return nil
	}
	if err := RetireStartupHandoff(t.Context(), plan, &journal, persist); err == nil {
		t.Fatal("injected persistence failure was ignored")
	}
	if saved.Actions[0].Phase != "retiring" {
		t.Fatal("side effect occurred without durable before/after fingerprints")
	}
	if data, _ := os.ReadFile(filepath.Join(root, "dump.pm2")); strings.Contains(string(data), "selected-private-secret") {
		t.Fatal("fixture did not reach atomic publish")
	}
	journal = saved
	if err := RetireStartupHandoff(t.Context(), plan, &journal, persist); err != nil {
		t.Fatal("durable retry failed", err)
	}
	if err := RestoreStartupHandoff(t.Context(), plan, &journal, persist); err != nil {
		t.Fatal("rollback after interrupted publish failed", err)
	}
}

func TestNativeStartupHandoffRejectsConcurrentForeignWritesWithoutClobbering(t *testing.T) {
	t.Run("before publish", func(t *testing.T) {
		plan, _, root := pm2StartupPlanFixture(t)
		var journal NativeStartupJournal
		changed := false
		foreign := `[{"name":"other","env":{"TOKEN":"concurrent-private-value"}}]`
		persist := func(value NativeStartupJournal) error {
			if !changed && value.Actions[0].Phase == "retiring" {
				changed = true
				return os.WriteFile(filepath.Join(root, "dump.pm2"), []byte(foreign), 0640)
			}
			return nil
		}
		if err := RetireStartupHandoff(t.Context(), plan, &journal, persist); !errors.Is(err, ErrHostWorkloadChanged) {
			t.Fatal("changed saved list was accepted", err)
		}
		data, _ := os.ReadFile(filepath.Join(root, "dump.pm2"))
		if string(data) != foreign {
			t.Fatal("foreign startup settings were overwritten")
		}
	})
	t.Run("before rollback", func(t *testing.T) {
		plan, _, root := pm2StartupPlanFixture(t)
		var journal NativeStartupJournal
		persist := func(NativeStartupJournal) error { return nil }
		if err := RetireStartupHandoff(t.Context(), plan, &journal, persist); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "dump.pm2")
		foreign := []byte(`[{"name":"other","name2":"concurrent-change"}]`)
		if err := os.WriteFile(path, foreign, 0640); err != nil {
			t.Fatal(err)
		}
		if err := RestoreStartupHandoff(t.Context(), plan, &journal, persist); !errors.Is(err, ErrHostWorkloadChanged) {
			t.Fatal("rollback accepted changed shared settings", err)
		}
		data, _ := os.ReadFile(path)
		if string(data) != string(foreign) {
			t.Fatal("rollback clobbered an unrelated change")
		}
	})
}

func systemdStartupPlanFixture(t *testing.T) (*NativeStartupPlan, *HostWorkloadCapture, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "multi-user.target.wants")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	unit := filepath.Join(root, "owned.service")
	target := filepath.Join(root, "multi-user.target")
	targetContent := []byte("[Unit]\nDescription=Shared boot target\n")
	if err := os.WriteFile(target, targetContent, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte("[Service]\nExecStart=/srv/owned/server\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../owned.service", filepath.Join(directory, "owned.service")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../other.service", filepath.Join(directory, "other.service")); err != nil {
		t.Fatal(err)
	}
	properties := map[string]string{"UnitFileState": "enabled", "Names": "owned.service", "WantedBy": "multi-user.target", "FragmentPath": unit}
	original, _ := json.Marshal(struct {
		Properties map[string]string
		Files      map[string]string
	}{properties, map[string]string{unit: "private unit"}})
	evidence, _ := json.Marshal(map[string]startupUnitProof{"multi-user.target": {State: "static", Properties: map[string]string{"Id": "multi-user.target", "Wants": "owned.service other.service"}, Files: map[string]string{target: captureDigest(targetContent)}}})
	capture := &HostWorkloadCapture{Manager: "systemd", ResourceID: "owned.service", Name: "owned.service", Account: "fixture", UID: uint32(os.Getuid()), GID: uint32(os.Getgid()), OriginalConfig: original, StartupEvidence: evidence}
	plan, err := prepareSystemdStartupHandoffOwned(capture, []string{root}, uint32(os.Getuid()))
	if err != nil {
		t.Fatal(err)
	}
	return plan, capture, root
}

func TestNativeSystemdStartupHandoffRetiresExactDirectLinks(t *testing.T) {
	plan, capture, root := systemdStartupPlanFixture(t)
	var journal NativeStartupJournal
	persist := func(NativeStartupJournal) error { return nil }
	if plan.Summary().ActionCount != 1 {
		t.Fatal("unrelated links included in plan")
	}
	if err := RetireStartupHandoff(t.Context(), plan, &journal, persist); err != nil {
		t.Fatal(err)
	}
	selected := filepath.Join(root, "multi-user.target.wants", "owned.service")
	if _, err := os.Lstat(selected); !os.IsNotExist(err) {
		t.Fatal("selected enablement survives cutover")
	}
	if target, err := os.Readlink(filepath.Join(root, "multi-user.target.wants", "other.service")); err != nil || target != "../other.service" {
		t.Fatal("unrelated startup authority changed")
	}
	fresh := *capture
	if err := VerifyCapturedStartup(&fresh, plan, journal); err != nil {
		t.Fatal("manager's cached direct target proof rejected", err)
	}
	fresh.StartupEvidence = json.RawMessage(`{}`)
	if err := VerifyCapturedStartup(&fresh, plan, journal); err != nil {
		t.Fatal("reloaded retired direct target proof rejected", err)
	}
	fresh.StartupEvidence = json.RawMessage(`{"other.timer":{"State":"enabled","Properties":{"Unit":"owned.service"},"Files":{}}}`)
	if err := VerifyCapturedStartup(&fresh, plan, journal); !errors.Is(err, ErrHostWorkloadChanged) {
		t.Fatal("new foreign launcher accepted", err)
	}
	if err := RestoreStartupHandoff(t.Context(), plan, &journal, persist); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(selected); err != nil || target != "../owned.service" {
		t.Fatal("original exact link target not restored")
	}
	if err := VerifyCapturedStartup(capture, plan, journal); err != nil {
		t.Fatal(err)
	}
	// Restoring links does not globally reload the manager. A target whose
	// relationships were refreshed while retired can remain cached without
	// the selected dependency until its next reload. Exact restored links
	// still supply the original reboot authority.
	fresh.StartupEvidence = json.RawMessage(`{}`)
	if err := VerifyCapturedStartup(&fresh, plan, journal); err != nil {
		t.Fatal("verified restored links with cached retired relationships rejected", err)
	}
}

func TestNativeSystemdStartupHandoffRefusesSharedOrAmbiguousAuthority(t *testing.T) {
	for _, kind := range []string{"timer", "alias", "explicit target dependency"} {
		t.Run(kind, func(t *testing.T) {
			_, capture, root := systemdStartupPlanFixture(t)
			var original struct {
				Properties map[string]string
				Files      map[string]string
			}
			_ = json.Unmarshal(capture.OriginalConfig, &original)
			var evidence map[string]startupUnitProof
			_ = json.Unmarshal(capture.StartupEvidence, &evidence)
			switch kind {
			case "timer":
				evidence["foreign.timer"] = startupUnitProof{State: "enabled", Properties: map[string]string{"Unit": "owned.service"}, Files: map[string]string{}}
			case "alias":
				original.Properties["Names"] = "owned.service alias.service"
			case "explicit target dependency":
				target := filepath.Join(root, "multi-user.target")
				data := []byte("[Unit]\nWants=owned.service\n")
				if err := os.WriteFile(target, data, 0600); err != nil {
					t.Fatal(err)
				}
				proof := evidence["multi-user.target"]
				proof.Files[target] = captureDigest(data)
				evidence["multi-user.target"] = proof
			}
			capture.OriginalConfig, _ = json.Marshal(original)
			capture.StartupEvidence, _ = json.Marshal(evidence)
			if _, err := prepareSystemdStartupHandoffOwned(capture, []string{root}, uint32(os.Getuid())); err == nil {
				t.Fatal("unsafe shared startup handoff prepared")
			}
		})
	}
}

func TestNativeStartupHandoffFencesAbsentListsAndParentLinks(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	capture := &HostWorkloadCapture{Manager: "pm2", ResourceID: "fixture/default/owned", Name: "owned", UID: uint32(os.Getuid()), GID: uint32(os.Getgid()), StartupEvidence: json.RawMessage(`{"dump.pm2":[],"dump.pm2.bak":[]}`)}
	plan, err := PreparePM2StartupHandoff(capture, root, "default")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Summary().ActionCount != 0 {
		t.Fatal("absent list reported as mutation")
	}
	if err := os.WriteFile(filepath.Join(root, "dump.pm2.bak"), []byte(`[]`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyStartupHandoff(context.Background(), plan, NativeStartupJournal{}); !errors.Is(err, ErrHostWorkloadChanged) {
		t.Fatal("new startup authority not fenced", err)
	}
	link := filepath.Join(t.TempDir(), "linked-daemon")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if _, err := PreparePM2StartupHandoff(capture, link, "default"); err == nil {
		t.Fatal("symlinked authority parent accepted")
	}
}

func TestNativeStartupHandoffRejectedIntentLeavesAuthorityUntouched(t *testing.T) {
	plan, _, root := pm2StartupPlanFixture(t)
	var journal NativeStartupJournal
	persist := func(value NativeStartupJournal) error {
		if value.Actions[0].Phase == "retiring" {
			return errors.New("intent not stored")
		}
		return nil
	}
	if err := RetireStartupHandoff(t.Context(), plan, &journal, persist); err == nil {
		t.Fatal("rejected intent ignored")
	}
	for _, name := range []string{"dump.pm2", "dump.pm2.bak"} {
		data, _ := os.ReadFile(filepath.Join(root, name))
		if !strings.Contains(string(data), "selected-private-secret") {
			t.Fatal("authority changed before durable intent")
		}
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 2 {
		t.Fatal("rejected intent leaked shared dump temporary")
	}
	if err := RetireStartupHandoff(t.Context(), plan, &journal, func(NativeStartupJournal) error { return nil }); err != nil {
		t.Fatal("retained intent did not rebuild prepared temporary", err)
	}
}

func TestNativeStartupHandoffCompensatesInterruptedRetirement(t *testing.T) {
	plan, _, _ := pm2StartupPlanFixture(t)
	var journal, saved NativeStartupJournal
	persist := func(value NativeStartupJournal) error {
		if value.Actions[0].Phase == "retired" {
			return errors.New("observed publish not stored")
		}
		saved = saveStartupJournal(value)
		return nil
	}
	if err := RetireStartupHandoff(t.Context(), plan, &journal, persist); err == nil {
		t.Fatal("injected failure ignored")
	}
	journal = saved
	if err := RestoreStartupHandoff(t.Context(), plan, &journal, func(NativeStartupJournal) error { return nil }); err != nil {
		t.Fatal("interrupted retirement compensation failed", err)
	}
	if journal.Phase != "restored" || journal.Actions[0].Phase != "restored" || journal.Actions[1].Phase != "restored" {
		t.Fatal("compensation journal incomplete")
	}
}
