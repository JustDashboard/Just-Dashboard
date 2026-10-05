package deploy

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

type nativeCaptureReader interface {
	CaptureRuntime(context.Context, ReleaseRuntime) (*procs.HostWorkloadCapture, error)
}

// NativePreflightObserver reuses a port only after its current process identity
// and the original manager configuration have both been verified.
type NativePreflightObserver struct {
	base      PreflightObserver
	native    nativeCaptureReader
	runs      *OrchestrationStore
	listeners func(context.Context) ([]proxysvc.Listener, error)
}

func NewNativePreflightObserver(base PreflightObserver, native *NativeRuntimeOwner, runs *OrchestrationStore) *NativePreflightObserver {
	return &NativePreflightObserver{base: base, native: native, runs: runs, listeners: proxysvc.ListListeners}
}

func (o *NativePreflightObserver) Observe(ctx context.Context, request ObservationRequest) (HostObservation, error) {
	observation, err := o.base.Observe(ctx, request)
	if err != nil || (request.ExistingRuntimeKind != "pm2" && request.ExistingRuntimeKind != "systemd") {
		return observation, err
	}
	if observation.Facilities == nil {
		observation.Facilities = map[string]FacilityObservation{}
	}
	unverified := func() (HostObservation, error) {
		observation.Facilities["native-runtime"] = FacilityObservation{Detail: "The captured manager configuration or process identity changed or could not be read."}
		return observation, nil
	}
	runtime := ReleaseRuntime{Kind: request.ExistingRuntimeKind, RuntimeID: request.ExistingRuntimeID, Metadata: request.ExistingRuntimeMetadata}
	if len(runtime.Metadata) == 0 && o.runs != nil {
		recorded, readErr := o.runs.RuntimeByIdentity(ctx, runtime.Kind, runtime.RuntimeID)
		if readErr != nil || recorded == nil {
			return unverified()
		}
		runtime = *recorded
	}
	if o.native == nil || !json.Valid(runtime.Metadata) {
		return unverified()
	}
	capture, err := o.native.CaptureRuntime(ctx, runtime)
	if err != nil || capture == nil {
		return unverified()
	}
	listeners, err := o.listeners(ctx)
	if err != nil {
		return unverified()
	}
	owned := nativeOwnedPorts(capture, listeners)
	for index := range observation.Ports {
		port := &observation.Ports[index]
		if port.InUse && owned[portObservationKey(port.Port, port.Protocol)] {
			port.InUse, port.OwnedByDeployment = false, true
			port.Detail = "the verified original application owns this host port"
		}
	}
	observation.Facilities["native-runtime"] = FacilityObservation{Available: true}
	return observation, nil
}

func nativeOwnedPorts(capture *procs.HostWorkloadCapture, listeners []proxysvc.Listener) map[string]bool {
	processes := map[int32]int64{}
	for _, process := range capture.Processes {
		if process.PID > 1 && process.CreateTime > 0 {
			processes[process.PID] = process.CreateTime
		}
	}
	owned := map[string]bool{}
	for _, listener := range listeners {
		if listener.StartedAt == nil || processes[listener.PID] != listener.StartedAt.UnixMilli() {
			continue
		}
		known := true
		for _, pid := range listener.PIDs {
			known = known && processes[pid] != 0
		}
		if known {
			protocol := strings.ToLower(listener.Protocol)
			if protocol == "" {
				protocol = "tcp"
			}
			owned[portObservationKey(int(listener.Port), protocol)] = true
		}
	}
	return owned
}
