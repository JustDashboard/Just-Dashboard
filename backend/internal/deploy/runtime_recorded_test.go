package deploy

import (
	"context"
	"strings"
	"testing"
	"time"
)

type delayedNativeObservation struct{ delay time.Duration }

type countingNativeInstances struct{ releaseIDs []int64 }

func (f *countingNativeInstances) ObserveNativeBaseline(_ context.Context, runtime ReleaseRuntime) RuntimeServices {
	f.releaseIDs = append(f.releaseIDs, runtime.ReleaseID)
	return RuntimeServices{Status: "available", Services: []RuntimeService{
		{Manager: runtime.Kind, ResourceID: runtime.RuntimeID + ":0"},
		{Manager: runtime.Kind, ResourceID: runtime.RuntimeID + ":1"},
	}}
}

func TestRecordedNativeObservationPrefersLiveRollbackAndKeepsEveryInstance(t *testing.T) {
	fixture, original := liveOperationsFixture(t)
	if _, err := fixture.base.DB.Exec(`UPDATE deploy_release_runtimes SET kind='pm2',runtime_id='owned-app',state='stopped' WHERE release_id=?`, original.Release.ID); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.base.DB.Exec(`INSERT INTO deploy_releases(project_id,environment_id,release_number,state,created_at) VALUES(?,?,2,'live',?)`, fixture.projectID, fixture.envID, fixture.now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	liveID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.base.DB.Exec(`INSERT INTO deploy_release_runtimes(release_id,environment_id,kind,runtime_id,name,state,metadata_json,created_at,updated_at) VALUES(?,?,'pm2','owned-app','owned-app','live','{}',?,?)`, liveID, fixture.envID, fixture.now.Unix(), fixture.now.Unix()); err != nil {
		t.Fatal(err)
	}
	native := &countingNativeInstances{}
	observer := NewRecordedRuntimeObserver(fixture.runs, &nativeDockerDelegationFixture{}, nil, native)
	observed := observer.RecordedRuntimeServices(t.Context(), fixture.envID, liveID, 0)
	if observed.Status != "available" || len(observed.Services) != 2 || len(native.releaseIDs) != 1 || native.releaseIDs[0] != liveID {
		t.Fatalf("rollback duplicated an original native manager: services=%+v captures=%v", observed.Services, native.releaseIDs)
	}
	for _, service := range observed.Services {
		if !service.LiveRelease || service.ReleaseID != liveID {
			t.Fatal("the original stopped row displaced the current native release")
		}
	}
	native.releaseIDs = nil
	observed = observer.RecordedRuntimeServices(t.Context(), fixture.envID, liveID, original.Release.ID)
	if observed.Status != "available" || len(observed.Services) != 2 || len(native.releaseIDs) != 1 || native.releaseIDs[0] != original.Release.ID || observed.Services[0].LiveRelease {
		t.Fatal("a release-scoped history read silently used the live rollback row")
	}
}

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
