package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/metrics"
)

type metricObservationFake struct {
	disabled    bool
	fail        bool
	ids         [][]string
	hostWindows [][2]time.Time
}

func (f *metricObservationFake) Enabled() bool { return !f.disabled }
func (f *metricObservationFake) ContainerIdentityRange(_ context.Context, ids []string, from, to time.Time, _ int) (*metrics.ContainerIdentityHistory, error) {
	f.ids = append(f.ids, append([]string(nil), ids...))
	if f.fail {
		return nil, errors.New("private-owner-error")
	}
	result := &metrics.ContainerIdentityHistory{Window: metrics.Window{From: from, To: to, IntervalSeconds: 15}}
	for _, id := range ids {
		result.Series = append(result.Series, metrics.ContainerIdentitySeries{ContainerID: id, Points: []metrics.ContainerIdentityPoint{{TS: from, Samples: 40, CPU: 10, CPUPeak: 20}}})
	}
	return result, nil
}
func (f *metricObservationFake) Range(_ context.Context, from, to time.Time, _ int) (*metrics.Series, error) {
	f.hostWindows = append(f.hostWindows, [2]time.Time{from, to})
	if f.fail {
		return nil, errors.New("private-owner-error")
	}
	return &metrics.Series{Points: []metrics.Point{{TS: from, Samples: 40, CPU: 20}}}, nil
}

func TestRunMetricsUsesRetainedIdentitiesAndDisjointActivationWindows(t *testing.T) {
	f := newReleaseStoreFixture(t)
	f.addPlan(t, 1, strings.Repeat("a", 40))
	first, lease := f.claimedRun(t, 1)
	prior := f.candidate(t, *first, lease, fakeContentDigest("prior"))
	_, err := f.runs.RecordCandidateRuntime(t.Context(), *first, lease.Token, ReleaseRuntimeInput{
		ReleaseID: prior.Release.ID, Kind: "container", RuntimeID: "retired-id", Name: "jd-e1-r1",
		Metadata: json.RawMessage(`{"version":1,"image":"example.test/app:v1"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.runs.SetRuntimeState(t.Context(), first.ID, lease.Token, prior.Release.ID, "ready"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runs.ActivateCandidate(t.Context(), first.ID, lease.Token, prior.Release.ID); err != nil {
		t.Fatal(err)
	}
	f.finishActivatedRun(t, first.ID, prior.Release.ID, lease.Token)
	f.now = f.now.Add(time.Hour)
	f.addPlan(t, 2, strings.Repeat("b", 40))
	run, lease := f.claimedRun(t, 2)
	candidate := f.candidate(t, *run, lease, fakeContentDigest("candidate"))
	_, err = f.runs.RecordCandidateRuntime(t.Context(), *run, lease.Token, ReleaseRuntimeInput{ReleaseID: candidate.Release.ID, Kind: "compose", RuntimeID: "jd-e1", Metadata: json.RawMessage(`{"version":1,"containerIds":["worker-id","web-id"]}`)})
	if err != nil {
		t.Fatal(err)
	}
	anchor := f.now.Add(-20 * time.Minute)
	run.CandidateReleaseID = candidate.Release.ID
	snapshot := RunSnapshot{Run: *run, Steps: []RunStep{{Key: StepActivate, State: StepPassed, EndedAt: &anchor, Evidence: mustJSON(activationStepEvidence{ReleaseID: candidate.Release.ID})}}}
	owner := &metricObservationFake{}
	result, err := f.runs.RunMetrics(t.Context(), owner, snapshot)
	if err != nil || result.Status != "available" || result.Before.Status != "available" || result.After.Status != "available" {
		t.Fatalf("comparison=%+v err=%v", result, err)
	}
	if !reflect.DeepEqual(owner.ids, [][]string{{"retired-id"}, {"web-id", "worker-id"}}) || len(owner.hostWindows) != 2 {
		t.Fatalf("wrong identities/call budget: %+v", owner)
	}
	if !owner.hostWindows[0][1].Before(owner.hostWindows[1][0]) || !result.After.History.From.Equal(anchor) || !result.Before.History.To.Equal(anchor) {
		t.Fatalf("overlapping windows: %+v", owner.hostWindows)
	}
	// A container release recorded its name and image; a Compose release
	// recorded only ids, so its series stay unnamed rather than guessed.
	if !reflect.DeepEqual(result.Before.Sources, []RunMetricSource{{ContainerID: "retired-id", Name: "jd-e1-r1", Image: "example.test/app:v1"}}) ||
		result.After.Sources != nil {
		t.Fatalf("metric sources before=%+v after=%+v", result.Before.Sources, result.After.Sources)
	}
	owner.fail = true
	result, err = f.runs.RunMetrics(t.Context(), owner, snapshot)
	raw, _ := json.Marshal(result)
	if err != nil || result.Before.Status != "unavailable" || result.HostReason == "" || strings.Contains(string(raw), "private-owner-error") {
		t.Fatalf("owner failures misreported: %s %v", raw, err)
	}
	owner.disabled = true
	before := len(owner.ids)
	result, err = f.runs.RunMetrics(t.Context(), owner, snapshot)
	if err != nil || result.Status != "unavailable" || len(owner.ids) != before {
		t.Fatalf("disabled owner queried: %+v %v", result, err)
	}
	owner.disabled, owner.fail = false, false
	snapshot.Steps[0].State = StepFailed
	result, err = f.runs.RunMetrics(t.Context(), owner, snapshot)
	if err != nil || result.Status != "unavailable" || len(owner.ids) != before {
		t.Fatalf("failed activation was compared: %+v %v", result, err)
	}
}

func TestComposeMetricIdentityCaptureExcludesOtherReleasesAndOneoffs(t *testing.T) {
	makeContainer := func(id, release, service string) dockerx.Container {
		return dockerx.Container{ID: id, Labels: map[string]string{
			"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": "7", "io.just-dashboard.release-id": release,
			"com.docker.compose.project": "jd-e7", "com.docker.compose.service": service}}
	}
	oneoff := makeContainer("oneoff", "11", "web")
	oneoff.Labels["com.docker.compose.oneoff"] = "True"
	ids, primary := composeRuntimeIdentities([]dockerx.Container{makeContainer("web-2", "11", "web"), makeContainer("worker", "11", "worker"), makeContainer("old", "10", "web"), oneoff, makeContainer("web-1", "11", "web")}, 7, 11, "jd-e7", "web")
	if !reflect.DeepEqual(ids, []string{"web-1", "web-2", "worker"}) || primary != "web-1" {
		t.Fatalf("capture=%v primary=%s", ids, primary)
	}
	for _, raw := range []string{`{"version":1,"primaryContainerId":"old-primary"}`, `{"version":1,"containerIds":["a","a"]}`, `{"version":9,"containerIds":["a"]}`} {
		if ids := runtimeMetricIDs(ReleaseRuntime{Kind: "compose", Metadata: json.RawMessage(raw)}); len(ids) != 0 {
			t.Fatalf("incomplete identity accepted: %s", raw)
		}
	}
}
