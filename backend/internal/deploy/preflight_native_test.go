package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

type preflightNativeCaptureFake struct {
	capture *procs.HostWorkloadCapture
	err     error
}

func (f preflightNativeCaptureFake) CaptureRuntime(context.Context, ReleaseRuntime) (*procs.HostWorkloadCapture, error) {
	return f.capture, f.err
}

func TestNativePreflightReusesOnlyVerifiedListeners(t *testing.T) {
	started := time.Unix(1000, 0)
	later := started.Add(time.Second)
	capture := &procs.HostWorkloadCapture{Processes: []procs.HostProcessCapture{{PID: 5000, CreateTime: started.UnixMilli()}}}
	for _, test := range []struct {
		name     string
		listener proxysvc.Listener
		owned    bool
	}{
		{"same process", proxysvc.Listener{PID: 5000, StartedAt: &started, Port: 3000, Protocol: "tcp"}, true},
		{"reused PID", proxysvc.Listener{PID: 5000, StartedAt: &later, Port: 3000, Protocol: "tcp"}, false},
		{"another app", proxysvc.Listener{PID: 6000, StartedAt: &started, Port: 3000, Protocol: "tcp"}, false},
		{"socket shared with another app", proxysvc.Listener{PID: 5000, PIDs: []int32{5000, 6000}, StartedAt: &started, Port: 3000, Protocol: "tcp"}, false},
		{"creation unavailable", proxysvc.Listener{PID: 5000, Port: 3000, Protocol: "tcp"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			observer := &NativePreflightObserver{
				base:      &preflightObserverFake{observation: HostObservation{Ports: []PortObservation{{Port: 3000, Protocol: "tcp", InUse: true}}}},
				native:    preflightNativeCaptureFake{capture: capture},
				listeners: func(context.Context) ([]proxysvc.Listener, error) { return []proxysvc.Listener{test.listener}, nil },
			}
			observed, err := observer.Observe(t.Context(), ObservationRequest{ExistingRuntimeKind: "pm2", ExistingRuntimeID: "daemon/default/app", ExistingRuntimeMetadata: json.RawMessage(`{"version":1}`)})
			if err != nil || observed.Ports[0].OwnedByDeployment != test.owned || observed.Ports[0].InUse == test.owned {
				t.Fatalf("ports=%+v error=%v", observed.Ports, err)
			}
		})
	}
}

func TestNativePreflightBlocksChangedManagerWithoutExposingError(t *testing.T) {
	observer := &NativePreflightObserver{base: &preflightObserverFake{observation: HostObservation{}}, native: preflightNativeCaptureFake{err: errors.New("secret-original-manager-value")}}
	observed, err := observer.Observe(t.Context(), ObservationRequest{ExistingRuntimeKind: "systemd", ExistingRuntimeID: "app.service", ExistingRuntimeMetadata: json.RawMessage(`{"version":1}`)})
	if err != nil || observed.Facilities["native-runtime"].Available {
		t.Fatalf("observation=%+v err=%v", observed, err)
	}
	draft := recoveredStoreFixture(t)
	findings := preflightFindings(&Draft{Data: DraftData{Intent: &DraftIntentConfig{Name: "app", Profile: ProfileCompose}, Source: &draft.Source, Detection: &draft.Detection}}, draft.Configuration, observed, true)
	for _, finding := range findings {
		if finding.Code == "native_runtime_changed" && finding.Severity == PreflightBlocked {
			return
		}
	}
	t.Fatal("changed manager did not block preflight")
}
