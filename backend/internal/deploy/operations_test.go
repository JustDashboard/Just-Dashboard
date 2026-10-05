package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

type runtimeObservationFake struct {
	items    []dockerx.Container
	err      error
	calls    int
	labels   map[string]string
	deadline time.Time
}

func (f *runtimeObservationFake) ListContainersWithLabels(ctx context.Context, labels map[string]string) ([]dockerx.Container, error) {
	f.calls++
	f.labels = labels
	f.deadline, _ = ctx.Deadline()
	return f.items, f.err
}

func TestRuntimeServicesScopeAvailabilityAndSecretFreeProjection(t *testing.T) {
	container := func(id, environment, release string) dockerx.Container {
		return dockerx.Container{ID: id, Name: id, State: "running", Command: "secret-command",
			Labels: map[string]string{"io.just-dashboard.managed": "true",
				"io.just-dashboard.environment-id": environment, "io.just-dashboard.release-id": release,
				"private": "secret-label"}}
	}
	live := container("web", "7", "10")
	live.Health, live.ComposeStack, live.ComposeSvc = "healthy", "jd-e7", "web"
	task := container("jd-e7-run3-task1", "7", "11")
	task.Labels[releaseTaskLabel] = "migrate"
	owner := &runtimeObservationFake{items: []dockerx.Container{
		live, container("candidate", "7", "11"), container("other", "8", "10"),
		container("invalid", "7", "oops"), task,
	}}
	result := ObserveRuntimeServices(t.Context(), owner, 7, 10)
	if result.Status != "available" || result.ObservedAt.IsZero() || len(result.Services) != 2 {
		t.Fatalf("runtime evidence = %+v", result)
	}
	if owner.calls != 1 || owner.labels["io.just-dashboard.environment-id"] != "7" ||
		owner.labels["io.just-dashboard.managed"] != "true" || owner.deadline.IsZero() || time.Until(owner.deadline) > 5*time.Second {
		t.Fatalf("unbounded or unscoped observation: %+v", owner)
	}
	if result.Services[0].LiveRelease || result.Services[0].Health != "unavailable" ||
		!result.Services[1].LiveRelease || result.Services[1].Stack != "jd-e7" || result.Services[1].Health != "healthy" {
		t.Fatalf("runtime identities/health = %+v", result.Services)
	}
	raw, err := json.Marshal(result)
	if err != nil || strings.Contains(string(raw), "secret-") {
		t.Fatalf("unsafe runtime projection: %s (%v)", raw, err)
	}
	owner.items = nil
	empty := ObserveRuntimeServices(t.Context(), owner, 7, 10)
	if empty.Status != "available" || len(empty.Services) != 0 {
		t.Fatalf("empty observation = %+v", empty)
	}
	owner.err = errors.New("secret-transport-address")
	failed := ObserveRuntimeServices(t.Context(), owner, 7, 10)
	if failed.Status != "unavailable" || failed.Reason == "" || strings.Contains(failed.Reason, "secret-") {
		t.Fatalf("failed observation = %+v", failed)
	}
	if absent := ObserveRuntimeServices(t.Context(), nil, 7, 10); absent.Status != "unavailable" {
		t.Fatalf("absent Docker = %+v", absent)
	}
	before := owner.calls
	ObserveRuntimeServices(t.Context(), owner, 0, 0)
	if owner.calls != before {
		t.Fatal("invalid environment queried Docker")
	}
}

func TestReleaseRuntimeDownNeedsEveryObservedContainerDown(t *testing.T) {
	container := func(release, state string) dockerx.Container {
		return dockerx.Container{ID: release + "-" + state, State: state, Labels: map[string]string{
			"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": "7",
			"io.just-dashboard.release-id": release,
		}}
	}
	runtime := ReleaseRuntime{ReleaseID: 10, EnvironmentID: 7}
	for _, test := range []struct {
		name  string
		owner RuntimeObserver
		want  bool
	}{
		{"exited", &runtimeObservationFake{items: []dockerx.Container{container("10", "exited")}}, true},
		{"one service still running", &runtimeObservationFake{items: []dockerx.Container{
			container("10", "exited"), container("10", "running")}}, false},
		{"only another release running", &runtimeObservationFake{items: []dockerx.Container{
			container("10", "exited"), container("11", "running")}}, true},
		{"no containers", &runtimeObservationFake{}, false},
		{"Docker unreadable", &runtimeObservationFake{err: errors.New("down")}, false},
		{"no Docker", nil, false},
	} {
		if got := ReleaseRuntimeDown(t.Context(), test.owner, runtime); got != test.want {
			t.Errorf("%s: down = %v, want %v", test.name, got, test.want)
		}
	}
}

func TestRuntimeServicesCarryLastRunFacts(t *testing.T) {
	exit := 137
	labels := map[string]string{"io.just-dashboard.managed": "true",
		"io.just-dashboard.environment-id": "7", "io.just-dashboard.release-id": "10"}
	owner := &runtimeObservationFake{items: []dockerx.Container{
		{ID: "down", Name: "down", State: "exited", Labels: labels, Restarts: 4, Exited: &exit, WasOOMKilled: true},
		{ID: "up", Name: "up", State: "running", Labels: labels},
	}}
	result := ObserveRuntimeServices(t.Context(), owner, 7, 10)
	if len(result.Services) != 2 {
		t.Fatalf("services = %+v", result.Services)
	}
	down, up := result.Services[0], result.Services[1]
	if down.RestartCount != 4 || down.ExitCode == nil || *down.ExitCode != 137 || !down.OOMKilled {
		t.Fatalf("stopped service = %+v", down)
	}
	raw, err := json.Marshal(up)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"restartCount", "exitCode", "oomKilled"} {
		if strings.Contains(string(raw), field) {
			t.Fatalf("unknown %s should be omitted: %s", field, raw)
		}
	}
	raw, err = json.Marshal(down)
	if err != nil || !strings.Contains(string(raw), `"exitCode":137`) || !strings.Contains(string(raw), `"restartCount":4`) {
		t.Fatalf("stopped service JSON = %s (%v)", raw, err)
	}
}
