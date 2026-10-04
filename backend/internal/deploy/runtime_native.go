package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
)

// NativeBaselineMetadata contains identities, never original environment or
// command values. The original manager remains the rollback authority until a
// later release replaces the baseline with a normal managed Docker runtime.
type NativeBaselineMetadata struct {
	Version             int               `json:"version"`
	Manager             string            `json:"manager"`
	DaemonID            string            `json:"daemonId,omitempty"`
	Namespace           string            `json:"namespace,omitempty"`
	Name                string            `json:"name,omitempty"`
	ProcessIDs          []int             `json:"processIds,omitempty"`
	RunningProcessIDs   []int             `json:"runningProcessIds,omitempty"`
	WasActive           bool              `json:"wasActive"`
	Unit                string            `json:"unit,omitempty"`
	ConfigurationDigest string            `json:"configurationDigest"`
	LogSources          []string          `json:"logSources"`
	SourceFiles         map[string]string `json:"sourceFiles,omitempty"`
}

func NativeBaselineRuntimeInput(capture *procs.HostWorkloadCapture, releaseID int64) (ReleaseRuntimeInput, error) {
	if capture == nil || (capture.Manager != "pm2" && capture.Manager != "systemd") || capture.ConfigurationDigest == "" {
		return ReleaseRuntimeInput{}, fmt.Errorf("the native workload has no verified restart authority")
	}
	metadata := NativeBaselineMetadata{Version: 1, Manager: capture.Manager, Name: capture.Name, ConfigurationDigest: capture.ConfigurationDigest, LogSources: []string{}, SourceFiles: capture.SourceFiles}
	if capture.Manager == "pm2" {
		parts := strings.Split(capture.ResourceID, "/")
		if len(parts) != 3 {
			return ReleaseRuntimeInput{}, ErrInvalidPlan
		}
		var err error
		metadata.DaemonID, err = url.PathUnescape(parts[0])
		if err != nil {
			return ReleaseRuntimeInput{}, ErrInvalidPlan
		}
		metadata.Namespace, err = url.PathUnescape(parts[1])
		if err != nil {
			return ReleaseRuntimeInput{}, ErrInvalidPlan
		}
		for _, process := range capture.Processes {
			metadata.ProcessIDs = append(metadata.ProcessIDs, process.ID)
			if process.State == "online" || process.State == "launching" {
				metadata.RunningProcessIDs = append(metadata.RunningProcessIDs, process.ID)
			}
			metadata.LogSources = append(metadata.LogSources, process.LogSources...)
		}
		sort.Ints(metadata.ProcessIDs)
		sort.Ints(metadata.RunningProcessIDs)
	} else {
		metadata.Unit = capture.ResourceID
		metadata.LogSources = []string{"journal:" + capture.ResourceID}
		for _, process := range capture.Processes {
			if process.State == "active" {
				metadata.WasActive = true
			}
		}
	}
	encoded, _ := json.Marshal(metadata)
	return ReleaseRuntimeInput{ReleaseID: releaseID, Kind: capture.Manager, RuntimeID: capture.ResourceID, Name: capture.Name, WorkingDirectory: capture.SourceDirectory, Metadata: encoded}, nil
}

type nativePM2 interface {
	CaptureExisting(context.Context, string, string, string) (*procs.HostWorkloadCapture, error)
	ControlCaptured(context.Context, *procs.HostWorkloadCapture, string, string) error
	ControlCapturedProcesses(context.Context, *procs.HostWorkloadCapture, string, string, []int) error
}
type nativeSystemd interface {
	CaptureExisting(context.Context, string) (*procs.HostWorkloadCapture, error)
	Control(context.Context, string, procs.UnitAction) (*procs.CommandResult, error)
}

type NativeRuntimeOwner struct {
	docker   RuntimeOwner
	pm2      nativePM2
	systemd  nativeSystemd
	recorded RuntimeObserver
}

func (o *NativeRuntimeOwner) WithRecordedRuntimeObserver(observer RuntimeObserver) *NativeRuntimeOwner {
	o.recorded = observer
	return o
}

func NewNativeRuntimeOwner(docker RuntimeOwner, pm2 *procs.PM2, systemd *procs.Systemd) *NativeRuntimeOwner {
	owner := &NativeRuntimeOwner{docker: docker}
	if pm2 != nil {
		owner.pm2 = pm2
	}
	if systemd != nil {
		owner.systemd = systemd
	}
	return owner
}

func (o *NativeRuntimeOwner) StartCandidate(ctx context.Context, request CandidateRuntimeRequest, emit func(BuildLog) error) (StartedRuntime, error) {
	if baseline := request.Snapshot.NativeBaseline; baseline != nil {
		if o == nil || (baseline.Kind != "pm2" && baseline.Kind != "systemd") || request.Release.ID <= 0 || request.Release.EnvironmentID != request.Run.EnvironmentID || request.Release.RunID != request.Run.ID {
			return StartedRuntime{}, ErrInvalidPlan
		}
		runtime := ReleaseRuntime{ReleaseID: baseline.ReleaseID, EnvironmentID: request.Release.EnvironmentID, Kind: baseline.Kind, RuntimeID: baseline.RuntimeID, Name: baseline.Name, WorkingDirectory: baseline.WorkingDirectory, Host: baseline.Host, Port: baseline.Port, Metadata: baseline.Metadata}
		if err := o.StartExisting(ctx, runtime, nil, emit); err != nil {
			return StartedRuntime{}, err
		}
		input := *baseline
		input.ReleaseID = request.Release.ID
		return StartedRuntime{Input: input, Target: CheckTarget{Host: runtimeCheckHost(input.Host), Port: input.Port}}, nil
	}
	if o == nil || o.docker == nil {
		return StartedRuntime{}, ErrRuntimeUnavailable
	}
	return o.docker.StartCandidate(ctx, request, emit)
}

func (o *NativeRuntimeOwner) capture(ctx context.Context, runtime ReleaseRuntime) (*procs.HostWorkloadCapture, NativeBaselineMetadata, error) {
	var metadata NativeBaselineMetadata
	if json.Unmarshal(runtime.Metadata, &metadata) != nil || metadata.Version != 1 || metadata.Manager != runtime.Kind || metadata.ConfigurationDigest == "" {
		return nil, metadata, fmt.Errorf("the original native runtime identity is unavailable")
	}
	var capture *procs.HostWorkloadCapture
	var err error
	switch runtime.Kind {
	case "pm2":
		if o.pm2 == nil {
			return nil, metadata, ErrRuntimeUnavailable
		}
		capture, err = o.pm2.CaptureExisting(ctx, metadata.DaemonID, metadata.Namespace, metadata.Name)
	case "systemd":
		if o.systemd == nil {
			return nil, metadata, ErrRuntimeUnavailable
		}
		if metadata.Unit != runtime.RuntimeID {
			return nil, metadata, ErrInvalidPlan
		}
		capture, err = o.systemd.CaptureExisting(ctx, metadata.Unit)
	default:
		return nil, metadata, ErrInvalidPlan
	}
	if err != nil {
		return nil, metadata, err
	}
	if capture.ResourceID != runtime.RuntimeID || capture.ConfigurationDigest != metadata.ConfigurationDigest {
		return nil, metadata, procs.ErrHostWorkloadChanged
	}
	if err := procs.VerifyHostSourceFiles(metadata.SourceFiles); err != nil {
		return nil, metadata, err
	}
	if runtime.Kind == "pm2" {
		ids := make([]int, 0, len(capture.Processes))
		for _, process := range capture.Processes {
			ids = append(ids, process.ID)
		}
		sort.Ints(ids)
		if len(ids) != len(metadata.ProcessIDs) {
			return nil, metadata, procs.ErrHostWorkloadChanged
		}
		for i, id := range ids {
			if id != metadata.ProcessIDs[i] {
				return nil, metadata, procs.ErrHostWorkloadChanged
			}
		}
	}
	return capture, metadata, nil
}

func (o *NativeRuntimeOwner) StartExisting(ctx context.Context, runtime ReleaseRuntime, variables map[string]string, emit func(BuildLog) error) error {
	if runtime.Kind != "pm2" && runtime.Kind != "systemd" {
		if o == nil || o.docker == nil {
			return ErrRuntimeUnavailable
		}
		return o.docker.StartExisting(ctx, runtime, variables, emit)
	}
	capture, metadata, err := o.capture(ctx, runtime)
	if err != nil {
		return err
	}
	if runtime.Kind == "pm2" {
		return o.pm2.ControlCapturedProcesses(ctx, capture, metadata.Namespace, "start", metadata.RunningProcessIDs)
	}
	if !metadata.WasActive {
		return nil
	}
	_, err = o.systemd.Control(ctx, metadata.Unit, procs.UnitStart)
	if err != nil {
		return fmt.Errorf("the original systemd service could not be restored")
	}
	return nil
}

func (o *NativeRuntimeOwner) Stop(ctx context.Context, runtime ReleaseRuntime, plan RuntimePlanConfig, variables map[string]string, remove bool, emit func(BuildLog) error) (RuntimeStopEvidence, error) {
	if runtime.Kind != "pm2" && runtime.Kind != "systemd" {
		if o == nil || o.docker == nil {
			return RuntimeStopEvidence{}, ErrRuntimeUnavailable
		}
		return o.docker.Stop(ctx, runtime, plan, variables, remove, emit)
	}
	evidence := RuntimeStopEvidence{RuntimeID: runtime.RuntimeID, Signal: "original manager", StartedAt: time.Now().UTC()}
	capture, metadata, err := o.capture(ctx, runtime)
	if err != nil {
		return evidence, err
	}
	if runtime.Kind == "pm2" {
		err = o.pm2.ControlCaptured(ctx, capture, metadata.Namespace, "stop")
	} else {
		_, err = o.systemd.Control(ctx, metadata.Unit, procs.UnitStop)
		if err != nil {
			err = fmt.Errorf("the original systemd service could not be stopped")
		}
	}
	if err != nil {
		// A failed manager action may already have stopped part of an app.
		// Restore under a fresh bounded context before reporting failure.
		restoreCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if restoreErr := o.StartExisting(restoreCtx, runtime, variables, emit); restoreErr != nil {
			return evidence, fmt.Errorf("the original native stop failed and restoration requires operator attention")
		}
		return evidence, err
	}
	evidence.CompletedAt = time.Now().UTC()
	return evidence, nil
}

func (o *NativeRuntimeOwner) PersistentSources(ctx context.Context, request CandidateRuntimeRequest) ([]string, error) {
	owner, ok := o.docker.(RuntimeStorageOwner)
	if !ok {
		return []string{}, nil
	}
	return owner.PersistentSources(ctx, request)
}
func (o *NativeRuntimeOwner) DiagnoseRuntime(ctx context.Context, runtime ReleaseRuntime) (RuntimeDiagnostics, error) {
	if runtime.Kind == "pm2" || runtime.Kind == "systemd" {
		return RuntimeDiagnostics{}, fmt.Errorf("native runtime diagnostics are available through the original manager logs")
	}
	owner, ok := o.docker.(RuntimeDiagnoser)
	if !ok {
		return RuntimeDiagnostics{}, ErrRuntimeUnavailable
	}
	return owner.DiagnoseRuntime(ctx, runtime)
}
func (o *NativeRuntimeOwner) ListContainersWithLabels(ctx context.Context, labels map[string]string) ([]dockerx.Container, error) {
	if o.recorded != nil {
		return o.recorded.ListContainersWithLabels(ctx, labels)
	}
	owner, ok := o.docker.(RuntimeObserver)
	if !ok {
		return nil, ErrRuntimeUnavailable
	}
	return owner.ListContainersWithLabels(ctx, labels)
}

func (o *NativeRuntimeOwner) RecordedRuntimeServices(ctx context.Context, environmentID, liveReleaseID int64) RuntimeServices {
	return observeRuntimeServices(ctx, o, environmentID, liveReleaseID, 0)
}

func (o *NativeRuntimeOwner) RunReleaseTask(ctx context.Context, request ReleaseTaskRuntimeRequest, emit func(BuildLog) error) (int, bool, error) {
	owner, ok := o.docker.(ReleaseTaskRuntime)
	if !ok {
		return 0, false, ErrRuntimeUnavailable
	}
	return owner.RunReleaseTask(ctx, request, emit)
}

func (o *NativeRuntimeOwner) RemovePreviewResources(ctx context.Context, environmentID int64) error {
	owner, ok := o.docker.(PreviewResourceOwner)
	if !ok {
		return ErrRuntimeUnavailable
	}
	return owner.RemovePreviewResources(ctx, environmentID)
}

func (o *NativeRuntimeOwner) QuarantinePreview(ctx context.Context, target PreviewQuarantineTarget) error {
	owner, ok := o.docker.(PreviewQuarantineOwner)
	if !ok {
		return ErrRuntimeUnavailable
	}
	return owner.QuarantinePreview(ctx, target)
}

func (o *NativeRuntimeOwner) CaptureRuntime(ctx context.Context, runtime ReleaseRuntime) (*procs.HostWorkloadCapture, error) {
	capture, _, err := o.capture(ctx, runtime)
	return capture, err
}

func (o *NativeRuntimeOwner) ObserveNativeBaseline(ctx context.Context, runtime ReleaseRuntime) RuntimeServices {
	result := RuntimeServices{Status: "unavailable", Services: []RuntimeService{}}
	capture, err := o.CaptureRuntime(ctx, runtime)
	if err != nil {
		result.Reason = "The original manager or captured configuration is unavailable or changed. Refresh import review before changing this workload."
		return result
	}
	result.Status = "available"
	result.ObservedAt = time.Now().UTC()
	for _, process := range capture.Processes {
		state := process.State
		if state == "online" || state == "active" {
			state = "running"
		}
		item := RuntimeService{Name: capture.Name, ReleaseID: runtime.ReleaseID, LiveRelease: true, State: state, Health: "unavailable", Manager: capture.Manager, ResourceID: capture.ResourceID, PID: process.PID}
		if len(process.LogSources) > 0 {
			item.LogSource = process.LogSources[0]
		}
		result.Services = append(result.Services, item)
	}
	return result
}
