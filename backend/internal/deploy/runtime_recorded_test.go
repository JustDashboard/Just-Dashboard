package deploy

import (
	"context"
	"strings"
	"testing"
	"time"
)

type delayedNativeObservation struct{ delay time.Duration }

func (f *delayedNativeObservation) ObserveNativeBaseline(ctx context.Context, runtime ReleaseRuntime) RuntimeServices {
	timer := time.NewTimer(f.delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return RuntimeServices{Status: "available", Services: []RuntimeService{{Manager: runtime.Kind, ResourceID: runtime.RuntimeID}}}
	case <-ctx.Done():
		return unavailableRecordedRuntime()
	}
}

func TestRecordedNativeObservationAllowsBoundedStartupAndSourceVerification(t *testing.T) {
	fixture := newReleaseStoreFixture(t)
	plan := RuntimePlanConfig{Strategy: StrategyStopFirst}
	fixture.addPlanWithRuntime(t, 1, strings.Repeat("a", 40), plan)
	run, lease := fixture.claimedRun(t, 1)
	baseline := ReleaseRuntimeInput{Kind: "systemd", RuntimeID: "owned-observation.service", Metadata: mustJSON(NativeBaselineMetadata{
		Version: 1, Manager: "systemd", ConfigurationDigest: strings.Repeat("a", 64),
	})}
	release, err := fixture.runs.CreateCandidateRelease(t.Context(), *run, lease.Token, CandidateReleaseInput{
		RuntimeSnapshot: mustJSON(runtimeReleaseSnapshot{Version: 1, Plan: plan, NativeBaseline: &baseline}),
	})
	if err != nil {
		t.Fatal(err)
	}
	baseline.ReleaseID = release.Release.ID
	if _, err := fixture.runs.RecordCandidateRuntime(t.Context(), *run, lease.Token, baseline); err != nil {
		t.Fatal(err)
	}
	observer := NewRecordedRuntimeObserver(fixture.runs, &nativeDockerDelegationFixture{}, nil, &delayedNativeObservation{delay: 9 * time.Second})
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	observed := observer.RecordedRuntimeServices(ctx, fixture.envID, release.Release.ID, release.Release.ID)
	if observed.Status != "available" || len(observed.Services) != 1 || observed.Services[0].ResourceID != baseline.RuntimeID {
		t.Fatalf("bounded source/startup verification was cut short: %+v", observed)
	}
	short, cancelShort := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancelShort()
	started := time.Now()
	observed = observer.RecordedRuntimeServices(short, fixture.envID, release.Release.ID, release.Release.ID)
	if observed.Status != "unavailable" || time.Since(started) > time.Second {
		t.Fatal("native observation extended the caller's cancellation deadline")
	}
}
