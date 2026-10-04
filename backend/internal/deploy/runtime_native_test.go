package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
)

type fakeNativePM2 struct {
	capture  *procs.HostWorkloadCapture
	actions  []string
	failStop bool
}

func (f *fakeNativePM2) CaptureExisting(context.Context, string, string, string) (*procs.HostWorkloadCapture, error) {
	return f.capture, nil
}
func (f *fakeNativePM2) ControlCaptured(_ context.Context, _ *procs.HostWorkloadCapture, namespace, action string) error {
	f.actions = append(f.actions, namespace+":"+action)
	if action == "stop" && f.failStop {
		return errors.New("stop failed")
	}
	return nil
}

func nativeFixture(t *testing.T) (*NativeRuntimeOwner, *fakeNativePM2, ReleaseRuntime) {
	t.Helper()
	capture := &procs.HostWorkloadCapture{Manager: "pm2", ResourceID: "alice/production/api", Name: "api", Account: "alice", ConfigurationDigest: "original-config", Processes: []procs.HostProcessCapture{{ID: 7, LogSources: []string{"pm2:alice/7/api"}}}}
	input, err := NativeBaselineRuntimeInput(capture, 10)
	if err != nil {
		t.Fatal(err)
	}
	manager := &fakeNativePM2{capture: capture}
	owner := &NativeRuntimeOwner{pm2: manager}
	runtime := ReleaseRuntime{ReleaseID: 10, EnvironmentID: 2, Kind: input.Kind, RuntimeID: input.RuntimeID, Metadata: input.Metadata}
	return owner, manager, runtime
}

func TestNativeBaselineUsesOriginalManagerAndRetainsRestartAuthority(t *testing.T) {
	owner, manager, runtime := nativeFixture(t)
	result, err := owner.Stop(context.Background(), runtime, RuntimePlanConfig{}, nil, true, nil)
	if err != nil || result.Removed || result.CompletedAt.IsZero() {
		t.Fatalf("native stop=%+v %v", result, err)
	}
	if err := owner.StartExisting(context.Background(), runtime, map[string]string{"EDITED": "must not overwrite native environment"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(manager.actions) != 2 || manager.actions[0] != "production:stop" || manager.actions[1] != "production:start" {
		t.Fatalf("wrong manager lifecycle: %v", manager.actions)
	}
}

func TestNativeBaselineRefusesChangedConfigAndReusedProcessIDs(t *testing.T) {
	owner, manager, runtime := nativeFixture(t)
	manager.capture.ConfigurationDigest = "changed-config"
	if _, err := owner.Stop(context.Background(), runtime, RuntimePlanConfig{}, nil, false, nil); !errors.Is(err, procs.ErrHostWorkloadChanged) {
		t.Fatalf("changed config accepted: %v", err)
	}
	manager.capture.ConfigurationDigest = "original-config"
	manager.capture.Processes[0].ID = 8
	if err := owner.StartExisting(context.Background(), runtime, nil, nil); !errors.Is(err, procs.ErrHostWorkloadChanged) {
		t.Fatalf("reassigned process accepted: %v", err)
	}
	if len(manager.actions) != 0 {
		t.Fatal("changed original manager was mutated")
	}
}

func TestNativeBaselineRestoresAfterFailedStop(t *testing.T) {
	owner, manager, runtime := nativeFixture(t)
	manager.failStop = true
	if _, err := owner.Stop(context.Background(), runtime, RuntimePlanConfig{}, nil, false, nil); err == nil {
		t.Fatal("failed stop reported success")
	}
	if len(manager.actions) != 2 || manager.actions[1] != "production:start" {
		t.Fatal("failed partial stop was not compensated")
	}
}

func TestNativeBaselineCandidateRestartsExactBaselineForRollback(t *testing.T) {
	owner, manager, runtime := nativeFixture(t)
	baseline := ReleaseRuntimeInput{ReleaseID: runtime.ReleaseID, Kind: runtime.Kind, RuntimeID: runtime.RuntimeID, Host: "127.0.0.1", Port: 3000, Metadata: runtime.Metadata}
	result, err := owner.StartCandidate(context.Background(), CandidateRuntimeRequest{Run: EngineRun{ID: 20, EnvironmentID: 2}, Release: Release{ID: 30, EnvironmentID: 2, RunID: 20}, Snapshot: runtimeReleaseSnapshot{NativeBaseline: &baseline}}, nil)
	if err != nil || result.Input.ReleaseID != 30 || result.Input.RuntimeID != runtime.RuntimeID || result.Target.Host != "127.0.0.1" || result.Target.Port != 3000 {
		t.Fatalf("native rollback candidate=%+v %v", result, err)
	}
	if len(manager.actions) != 1 || manager.actions[0] != "production:start" {
		t.Fatal("baseline rollback did not use original manager")
	}
	encoded, _ := json.Marshal(result.Input)
	if string(encoded) == "" {
		t.Fatal("native input unavailable")
	}
}
