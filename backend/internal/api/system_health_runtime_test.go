package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/metrics"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
)

func TestRuntimeHealthOrdersNamedFailuresBeforeHostWarnings(t *testing.T) {
	report := mergeRuntimeHealth(metrics.Health{Status: "warning", Findings: []metrics.Finding{{ID: "swap", Level: "warning"}}}, []procs.Unit{{Name: "worker.service", ActiveState: "failed"}, {Name: "web.service", ActiveState: "active"}}, nil, []dockerx.Container{{Name: "db", State: "running", Inspected: true, Health: "unhealthy"}, {Name: "one-shot", State: "exited"}, {Name: "unknown", State: "running"}, {Name: "paused-worker", State: "paused"}}, nil)
	if report.Status != "critical" || len(report.Findings) != 4 || report.Findings[0].ID != "docker.unhealthy" || report.Findings[0].Detail != "db" {
		t.Fatalf("runtime evidence/order: %+v", report)
	}
	if len(report.Silences) != 1 || report.Silences[0] != "Unread container health: unknown" {
		t.Fatal(report.Silences)
	}
	for _, finding := range report.Findings {
		if finding.ID == "docker.exited" {
			t.Fatal("completed jobs are not runtime failures")
		}
	}
}

func TestRuntimeHealthUnavailableManagersNeverCountAsHealthy(t *testing.T) {
	report := mergeRuntimeHealth(metrics.Health{Status: "ok"}, nil, errors.New("not installed"), nil, errors.New("no daemon"))
	if report.Status != "ok" || len(report.Findings) != 0 || len(report.Silences) != 2 {
		t.Fatalf("unavailable reads: %+v", report)
	}
}

// A failed unit is offered for repair by name, with why and when it failed,
// and the containers that are not doing their job by id.
func TestRuntimeHealthNamesSubjectsForEveryRemedy(t *testing.T) {
	failedAt := time.Now().Add(-3 * time.Hour).Unix()
	report := mergeRuntimeHealth(metrics.Health{Status: "ok"},
		[]procs.Unit{
			{Name: "nordvpnd-killswitch.service", Description: "Nordvpn Daemon launched in killswitch mode", ActiveState: "failed", Result: "exit-code", ChangedAt: failedAt},
			{Name: "web.service", ActiveState: "active"},
		}, nil,
		[]dockerx.Container{{ID: "abc123", Name: "db", State: "running", Inspected: true, Health: "unhealthy"}, {ID: "def456", Name: "api", State: "running", Inspected: true}}, nil)

	var units, containers metrics.Finding
	for _, f := range report.Findings {
		switch f.ID {
		case "systemd.failed":
			units = f
		case "docker.unhealthy":
			containers = f
		}
	}
	if units.Title != "nordvpnd-killswitch has failed" || units.Area != metrics.AreaServices || len(units.Subjects) != 1 {
		t.Fatalf("unit finding = %+v", units)
	}
	subject := units.Subjects[0]
	if subject.Kind != "unit" || subject.ID != "nordvpnd-killswitch.service" || subject.Detail != "exit-code" ||
		subject.Since == nil || subject.Since.Unix() != failedAt || subject.Name != "Nordvpn Daemon launched in killswitch mode" {
		t.Fatalf("unit subject = %+v", subject)
	}
	if containers.Title != "db is unhealthy" || len(containers.Subjects) != 1 || containers.Subjects[0].ID != "abc123" {
		t.Fatalf("container finding = %+v", containers)
	}

	areas := map[string]metrics.AreaVerdict{}
	for _, area := range report.Areas {
		areas[area.ID] = area
	}
	if areas[metrics.AreaServices] != (metrics.AreaVerdict{ID: metrics.AreaServices, Status: "warning", Summary: "1 failed"}) {
		t.Fatalf("services area = %+v", areas[metrics.AreaServices])
	}
	if areas[metrics.AreaContainers] != (metrics.AreaVerdict{ID: metrics.AreaContainers, Status: "critical", Summary: "1 unhealthy"}) {
		t.Fatalf("containers area = %+v", areas[metrics.AreaContainers])
	}
}

func TestRuntimeHealthMarksUnreadManagersUnknown(t *testing.T) {
	report := mergeRuntimeHealth(metrics.Health{Status: "ok"}, nil, errors.New("not installed"), nil, errors.New("no daemon"))
	for _, area := range report.Areas {
		if area.Status != "unknown" {
			t.Fatalf("unread manager area = %+v, want unknown", area)
		}
	}
	quiet := mergeRuntimeHealth(metrics.Health{Status: "ok"}, []procs.Unit{{Name: "a.service", ActiveState: "active"}, {Name: "b.service", ActiveState: "active"}}, nil, []dockerx.Container{{Name: "x", State: "running", Inspected: true}}, nil)
	for _, area := range quiet.Areas {
		if area.Status != "ok" || (area.ID == metrics.AreaServices && area.Summary != "2 running") || (area.ID == metrics.AreaContainers && area.Summary != "1 running") {
			t.Fatalf("healthy runtime area = %+v", area)
		}
	}
}

// Every client reads one verdict, so the route answers with all seven areas
// and never calls a virtual machine's missing sensors a gap.
func TestSystemHealthRouteDrawsEveryArea(t *testing.T) {
	c, _ := newClient(t)
	response := c.do(http.MethodGet, "/api/v1/system/health", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("health: %d %s", response.Code, response.Body.String())
	}
	var health metrics.Health
	if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	want := []string{metrics.AreaCPU, metrics.AreaMemory, metrics.AreaStorage, metrics.AreaNetwork, metrics.AreaServices, metrics.AreaContainers, metrics.AreaHardware}
	if len(health.Areas) != len(want) {
		t.Fatalf("areas = %+v", health.Areas)
	}
	for i, area := range health.Areas {
		if area.ID != want[i] || area.Status == "" || area.Summary == "" {
			t.Fatalf("area %d = %+v, want %s with a status and a reading", i, area, want[i])
		}
	}
	for _, silence := range health.Silences {
		if strings.Contains(silence, "Temperature") {
			t.Fatalf("missing sensors reported as a gap: %q", silence)
		}
	}
	for _, finding := range health.Findings {
		if finding.Area == "" {
			t.Fatalf("finding %q has no area", finding.ID)
		}
	}
}
