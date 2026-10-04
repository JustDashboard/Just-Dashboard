package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
)

type fakeNativePM2 struct {
	capture  *procs.HostWorkloadCapture
	actions  []string
	failStop bool
}

type nativeDockerDelegationFixture struct{ calls []string }

func (f *nativeDockerDelegationFixture) StartCandidate(context.Context, CandidateRuntimeRequest, func(BuildLog) error) (StartedRuntime, error) {
	f.calls = append(f.calls, "candidate")
	return StartedRuntime{}, nil
}
func (f *nativeDockerDelegationFixture) StartExisting(context.Context, ReleaseRuntime, map[string]string, func(BuildLog) error) error {
	f.calls = append(f.calls, "start")
	return nil
}
func (f *nativeDockerDelegationFixture) Stop(context.Context, ReleaseRuntime, RuntimePlanConfig, map[string]string, bool, func(BuildLog) error) (RuntimeStopEvidence, error) {
	f.calls = append(f.calls, "stop")
	return RuntimeStopEvidence{}, nil
}
func (f *nativeDockerDelegationFixture) RunReleaseTask(context.Context, ReleaseTaskRuntimeRequest, func(BuildLog) error) (int, bool, error) {
	f.calls = append(f.calls, "release-task")
	return 0, true, nil
}
func (f *nativeDockerDelegationFixture) RemovePreviewResources(context.Context, int64) error {
	f.calls = append(f.calls, "preview-remove")
	return nil
}
func (f *nativeDockerDelegationFixture) QuarantinePreview(context.Context, PreviewQuarantineTarget) error {
	f.calls = append(f.calls, "preview-quarantine")
	return nil
}
func (f *nativeDockerDelegationFixture) PersistentSources(context.Context, CandidateRuntimeRequest) ([]string, error) {
	f.calls = append(f.calls, "storage")
	return []string{"volume"}, nil
}
func (f *nativeDockerDelegationFixture) DiagnoseRuntime(context.Context, ReleaseRuntime) (RuntimeDiagnostics, error) {
	f.calls = append(f.calls, "diagnostics")
	return RuntimeDiagnostics{}, nil
}
func (f *nativeDockerDelegationFixture) ListContainersWithLabels(context.Context, map[string]string) ([]dockerx.Container, error) {
	f.calls = append(f.calls, "observe")
	return []dockerx.Container{}, nil
}

func TestNativeBaselineWrapperPreservesEveryDockerFeatureOwner(t *testing.T) {
	delegate := &nativeDockerDelegationFixture{}
	owner := &NativeRuntimeOwner{docker: delegate}
	ctx := context.Background()
	_, _ = owner.StartCandidate(ctx, CandidateRuntimeRequest{}, nil)
	_ = owner.StartExisting(ctx, ReleaseRuntime{Kind: "container"}, nil, nil)
	_, _ = owner.Stop(ctx, ReleaseRuntime{Kind: "compose"}, RuntimePlanConfig{}, nil, false, nil)
	_, _, _ = owner.RunReleaseTask(ctx, ReleaseTaskRuntimeRequest{}, nil)
	_ = owner.RemovePreviewResources(ctx, 1)
	_ = owner.QuarantinePreview(ctx, PreviewQuarantineTarget{})
	_, _ = owner.PersistentSources(ctx, CandidateRuntimeRequest{})
	_, _ = owner.DiagnoseRuntime(ctx, ReleaseRuntime{Kind: "container"})
	_, _ = owner.ListContainersWithLabels(ctx, nil)
	if len(delegate.calls) != 9 {
		t.Fatalf("Docker feature owner was lost: %v", delegate.calls)
	}
}

func TestNativeBaselineObservesOriginalLogsWithoutPretendingContainerIdentity(t *testing.T) {
	owner, manager, runtime := nativeFixture(t)
	manager.capture.Processes[0].State = "online"
	manager.capture.Processes[0].PID = 123
	result := owner.ObserveNativeBaseline(context.Background(), runtime)
	if result.Status != "available" || len(result.Services) != 1 {
		t.Fatalf("native observation unavailable: %+v", result)
	}
	service := result.Services[0]
	if service.State != "running" || service.Manager != "pm2" || service.PID != 123 || service.ContainerID != "" || service.LogSource != "pm2:alice/7/api" {
		t.Fatalf("native service identity lost: %+v", service)
	}
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
