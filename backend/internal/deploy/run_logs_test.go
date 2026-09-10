package deploy

import (
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

func TestRunLogHandoffUsesRunActivationAndExactPreviewRelease(t *testing.T) {
	at := time.Date(2026, 11, 1, 6, 30, 30, 123000000, time.UTC)
	snapshot := RunSnapshot{Run: EngineRun{ID: 9, EnvironmentID: 7, CandidateReleaseID: 11, ReleaseID: 10},
		Steps: []RunStep{{ID: 1, Key: StepActivate, Attempt: 1, State: StepPassed, EndedAt: &at,
			Evidence: json.RawMessage(`{"releaseId":11}`)}}}
	container := func(id, environment, release string) dockerx.Container {
		return dockerx.Container{ID: id, Name: id, Labels: map[string]string{
			"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": environment,
			"io.just-dashboard.release-id": release}}
	}
	owner := &runtimeObservationFake{items: []dockerx.Container{
		container("preview", "7", "11"), container("prior", "7", "10"), container("production", "8", "11"),
	}}
	result := ObserveRunLogs(t.Context(), owner, snapshot)
	if result.Status != "available" || len(result.Sources) != 1 || result.Sources[0].Name != "preview" ||
		owner.calls != 1 || owner.labels["io.just-dashboard.release-id"] != "11" || owner.labels["io.just-dashboard.environment-id"] != "7" {
		t.Fatalf("unscoped runtime handoff: %+v owner=%+v", result, owner)
	}
	link, err := url.Parse(result.Sources[0].ActivationURL)
	if err != nil {
		t.Fatal(err)
	}
	if link.Query().Get("since") != "2026-11-01T06:25:30.123Z" ||
		link.Query().Get("until") != "2026-11-01T06:35:30.123Z" || link.Query().Get("source") != "docker:preview" || link.Query().Get("mode") != "search" {
		t.Fatalf("incorrect activation window: %s", link)
	}
	for _, state := range []StepState{StepRunning, StepFailed, StepSkipped, StepUnavailable, StepCancelled} {
		snapshot.Steps = append(snapshot.Steps[:1], RunStep{ID: 2, Key: StepActivate, Attempt: 2, State: state})
		result = ObserveRunLogs(t.Context(), owner, snapshot)
		if result.ActivationCompletedAt != nil || result.Sources[0].ActivationURL != "" || result.Sources[0].LiveURL == "" || result.WindowReason == "" {
			t.Fatalf("invented activation for %s: %+v", state, result)
		}
	}
	snapshot.Steps = snapshot.Steps[:1]
	for _, evidence := range []string{`{}`, `{"releaseId":10}`, `{"releaseId":11,"recovery":{}}`, `broken`} {
		snapshot.Steps[0].Evidence = json.RawMessage(evidence)
		if result := ObserveRunLogs(t.Context(), owner, snapshot); result.ActivationCompletedAt != nil {
			t.Fatalf("accepted invalid activation evidence %q", evidence)
		}
	}
	if result := ObserveRunLogs(t.Context(), nil, snapshot); result.Status != "unavailable" {
		t.Fatalf("absent Docker: %+v", result)
	}
	before := owner.calls
	if result := ObserveRunLogs(t.Context(), owner, RunSnapshot{}); result.Status != "unavailable" || owner.calls != before {
		t.Fatalf("run without release queried Docker: %+v", result)
	}
}
