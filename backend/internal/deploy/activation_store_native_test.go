package deploy

import (
	"errors"
	"strings"
	"testing"
)

func TestNativeCandidateRecordingRequiresTheImmutableCapturedIdentity(t *testing.T) {
	for _, manager := range []string{"pm2", "systemd"} {
		t.Run(manager, func(t *testing.T) {
			fixture := newReleaseStoreFixture(t)
			plan := RuntimePlanConfig{Strategy: StrategyStopFirst}
			fixture.addPlanWithRuntime(t, 1, strings.Repeat("a", 40), plan)
			run, lease := fixture.claimedRun(t, 1)
			metadata := NativeBaselineMetadata{Version: 1, Manager: manager, ConfigurationDigest: strings.Repeat("a", 64), LogSources: []string{}}
			baseline := ReleaseRuntimeInput{ReleaseID: 42, Kind: manager, RuntimeID: "original-app", Name: "original-app", WorkingDirectory: "/srv/original", Host: "127.0.0.1", Port: 3000, Metadata: mustJSON(metadata)}
			snapshot := runtimeReleaseSnapshot{Version: 1, Plan: plan, NativeBaseline: &baseline}
			release, err := fixture.runs.CreateCandidateRelease(t.Context(), *run, lease.Token, CandidateReleaseInput{RuntimeSnapshot: mustJSON(snapshot)})
			if err != nil {
				t.Fatal(err)
			}
			input := baseline
			input.ReleaseID = release.Release.ID
			wrong := input
			wrong.RuntimeID = "another-app"
			if _, err := fixture.runs.RecordCandidateRuntime(t.Context(), *run, lease.Token, wrong); !errors.Is(err, ErrInvalidPlan) {
				t.Fatalf("another manager identity was accepted: %v", err)
			}
			wrong = input
			metadata.ConfigurationDigest = fakeContentDigest("changed-configuration")
			wrong.Metadata = mustJSON(metadata)
			if _, err := fixture.runs.RecordCandidateRuntime(t.Context(), *run, lease.Token, wrong); !errors.Is(err, ErrInvalidPlan) {
				t.Fatalf("changed manager configuration was accepted: %v", err)
			}
			if _, err := fixture.runs.RecordCandidateRuntime(t.Context(), *run, "wrong-lease", input); err == nil {
				t.Fatal("native runtime bypassed the run lease")
			}
			recorded, err := fixture.runs.RecordCandidateRuntime(t.Context(), *run, lease.Token, input)
			if err != nil || recorded.Kind != manager || recorded.RuntimeID != input.RuntimeID {
				t.Fatalf("captured native rollback identity refused: %+v %v", recorded, err)
			}
			if _, err := fixture.runs.RecordCandidateRuntime(t.Context(), *run, lease.Token, input); err != nil {
				t.Fatalf("native recording was not idempotent: %v", err)
			}
		})
	}
}

func TestNativeCandidateRecordingRefusesAnOrdinaryDockerSnapshot(t *testing.T) {
	fixture := newReleaseStoreFixture(t)
	fixture.addPlan(t, 1, strings.Repeat("a", 40))
	run, lease := fixture.claimedRun(t, 1)
	release := fixture.candidate(t, *run, lease, fakeContentDigest("docker-image"))
	input := ReleaseRuntimeInput{ReleaseID: release.Release.ID, Kind: "systemd", RuntimeID: "foreign.service", Metadata: mustJSON(NativeBaselineMetadata{Version: 1, Manager: "systemd", ConfigurationDigest: fakeContentDigest("foreign")})}
	if _, err := fixture.runs.RecordCandidateRuntime(t.Context(), *run, lease.Token, input); err == nil {
		t.Fatal("an ordinary Docker release claimed native restart authority")
	}
}
