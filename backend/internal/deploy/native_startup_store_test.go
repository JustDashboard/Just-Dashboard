package deploy

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
)

type startupNativePM2Fixture struct {
	fakeNativePM2
	directory string
}

func (f *startupNativePM2Fixture) CaptureExisting(context.Context, string, string, string) (*procs.HostWorkloadCapture, error) {
	capture := *f.capture
	evidence := map[string][]json.RawMessage{}
	for _, name := range []string{"dump.pm2", "dump.pm2.bak"} {
		raw, err := os.ReadFile(filepath.Join(f.directory, name))
		if err != nil {
			return nil, err
		}
		var rows []json.RawMessage
		if err := json.Unmarshal(raw, &rows); err != nil {
			return nil, err
		}
		evidence[name] = []json.RawMessage{}
		for _, row := range rows {
			var entry struct{ Name, Namespace string }
			if json.Unmarshal(row, &entry) != nil {
				return nil, ErrInvalidPlan
			}
			if entry.Name == "api" && entry.Namespace == "production" {
				evidence[name] = append(evidence[name], row)
			}
		}
	}
	capture.StartupEvidence = mustJSON(evidence)
	capture.ConfigurationDigest = digestBytes([]byte(capture.RuntimeConfigurationDigest), capture.StartupEvidence)
	return &capture, nil
}

func nativeStartupFixture(t *testing.T) (*NativeRuntimeOwner, *startupNativePM2Fixture, ReleaseRuntime, *NativeStartupStore) {
	t.Helper()
	fixture := newPlanningStoreFixture(t)
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{
		{"name": "api", "namespace": "production", "pm_exec_path": filepath.Join(directory, "server.js"), "pm_cwd": directory, "env": map[string]string{"TOKEN": "selected-startup-private"}},
		{"name": "api", "namespace": "unrelated", "env": map[string]string{"TOKEN": "unrelated-startup-private"}},
	}
	for _, name := range []string{"dump.pm2", "dump.pm2.bak"} {
		if err := os.WriteFile(filepath.Join(directory, name), mustJSON(rows), 0600); err != nil {
			t.Fatal(err)
		}
	}
	manager := &startupNativePM2Fixture{directory: directory}
	manager.capture = &procs.HostWorkloadCapture{Manager: "pm2", ResourceID: "alice/production/api", Name: "api", Account: "alice", UID: uint32(os.Getuid()), GID: uint32(os.Getgid()), SourceDirectory: directory, SourcePath: filepath.Join(directory, "server.js"), RuntimeConfigurationDigest: "original-runtime", Processes: []procs.HostProcessCapture{{ID: 7, State: "online"}}}
	capture, err := manager.CaptureExisting(t.Context(), "alice", "production", "api")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := procs.PreparePM2StartupHandoff(capture, directory, "production")
	if err != nil {
		t.Fatal(err)
	}
	store := NewNativeStartupStore(t.TempDir(), fixture.sealer)
	if err := store.Stage(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	input, err := NativeBaselineRuntimeInput(capture, 10)
	if err != nil {
		t.Fatal(err)
	}
	var metadata NativeBaselineMetadata
	_ = json.Unmarshal(input.Metadata, &metadata)
	metadata.StartupPlanDigest, metadata.RuntimeConfigurationDigest = plan.Digest, capture.RuntimeConfigurationDigest
	runtime := ReleaseRuntime{ReleaseID: 10, EnvironmentID: 2, Kind: input.Kind, RuntimeID: input.RuntimeID, Metadata: mustJSON(metadata)}
	owner := (&NativeRuntimeOwner{pm2: manager}).WithStartupStore(store)
	private, err := store.directory(plan.Digest)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := os.ReadFile(filepath.Join(private, "plan.enc"))
	if err != nil || strings.Contains(string(sealed), "selected-startup-private") {
		t.Fatal("selected startup authority was not encrypted")
	}
	plain, err := store.sealer.Open(string(sealed))
	if err != nil || strings.Contains(plain, "unrelated-startup-private") {
		t.Fatal("unrelated saved application entered the private plan")
	}
	return owner, manager, runtime, store
}

func TestNativeStartupOwnerRetiresOnDeployAndRestoresAfterRestart(t *testing.T) {
	owner, manager, runtime, store := nativeStartupFixture(t)
	before, _ := os.ReadFile(filepath.Join(manager.directory, "dump.pm2"))
	if _, err := owner.CaptureRuntime(t.Context(), runtime); err != nil {
		t.Fatal(err)
	}
	afterCapture, _ := os.ReadFile(filepath.Join(manager.directory, "dump.pm2"))
	if string(before) != string(afterCapture) || len(manager.actions) != 0 {
		t.Fatal("read-only capture changed startup authority")
	}
	if _, err := owner.Stop(t.Context(), runtime, RuntimePlanConfig{}, nil, false, nil); err != nil {
		t.Fatal(err)
	}
	retired, _ := os.ReadFile(filepath.Join(manager.directory, "dump.pm2"))
	if strings.Contains(string(retired), "selected-startup-private") || !strings.Contains(string(retired), "unrelated-startup-private") {
		t.Fatal("handoff did not retire only the selected application")
	}
	reopened := NewNativeStartupStore(filepath.Dir(store.root), store.sealer)
	owner.WithStartupStore(reopened)
	if err := owner.StartExisting(t.Context(), runtime, nil, nil); err != nil {
		t.Fatal(err)
	}
	restored, _ := os.ReadFile(filepath.Join(manager.directory, "dump.pm2"))
	if !strings.Contains(string(restored), "selected-startup-private") || !strings.Contains(string(restored), "unrelated-startup-private") || len(manager.actions) != 2 {
		t.Fatal("restart recovery did not restore original startup and manager")
	}
}

func TestNativeStartupOwnerCompensatesFailedStop(t *testing.T) {
	owner, manager, runtime, _ := nativeStartupFixture(t)
	manager.failStop = true
	if _, err := owner.Stop(t.Context(), runtime, RuntimePlanConfig{}, nil, false, nil); err == nil {
		t.Fatal("failed manager action succeeded")
	}
	restored, _ := os.ReadFile(filepath.Join(manager.directory, "dump.pm2"))
	if !strings.Contains(string(restored), "selected-startup-private") || len(manager.actions) != 2 || manager.actions[1] != "production:start" {
		t.Fatal("failed stop did not compensate startup authority before restart")
	}
}

func TestNativeStartupOwnerBlocksForeignSavedListBeforeStop(t *testing.T) {
	owner, manager, runtime, _ := nativeStartupFixture(t)
	path := filepath.Join(manager.directory, "dump.pm2")
	raw, _ := os.ReadFile(path)
	var rows []map[string]any
	_ = json.Unmarshal(raw, &rows)
	rows = append(rows, map[string]any{"name": "foreign-app", "namespace": "default"})
	if err := os.WriteFile(path, mustJSON(rows), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Stop(t.Context(), runtime, RuntimePlanConfig{}, nil, false, nil); err == nil || len(manager.actions) != 0 {
		t.Fatal("changed startup authority reached a native stop")
	}
}
