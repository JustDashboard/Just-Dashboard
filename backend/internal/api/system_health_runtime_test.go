package api

import (
	"errors"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/metrics"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"testing"
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
