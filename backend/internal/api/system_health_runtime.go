package api

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/metrics"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/sysinfo"
)

// All Health consumers receive the same runtime evidence. A bounded read keeps
// an unavailable optional manager from holding the host verdict indefinitely.
func (s *Server) runtimeHealth(ctx context.Context, health metrics.Health, snap *sysinfo.Snapshot) metrics.Health {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	var units []procs.Unit
	var containers []dockerx.Container
	var unitErr, dockerErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); units, unitErr = s.modules.systemd.List(ctx) }()
	go func() { defer wg.Done(); containers, dockerErr = s.modules.docker.ListContainers(ctx, true) }()
	wg.Wait()
	if !snap.Pressure.Supported {
		health.Silences = append(health.Silences, "Kernel pressure readings (PSI) are unavailable.")
	}
	if len(snap.Sensors) == 0 {
		health.Silences = append(health.Silences, "Temperature sensors are unavailable.")
	}
	return mergeRuntimeHealth(health, units, unitErr, containers, dockerErr)
}

func mergeRuntimeHealth(health metrics.Health, units []procs.Unit, unitErr error, containers []dockerx.Container, dockerErr error) metrics.Health {
	if unitErr != nil {
		health.Silences = append(health.Silences, "Service manager could not be read; failed services were not assessed.")
	} else {
		names := []string{}
		for _, unit := range units {
			if unit.ActiveState == "failed" {
				names = append(names, unit.Name)
			}
		}
		if len(names) > 0 {
			sort.Strings(names)
			health.Findings = append(health.Findings, metrics.Finding{ID: "systemd.failed", Level: "warning", Title: fmt.Sprintf("%d services have failed", len(names)), Detail: strings.Join(names, ", "), Advice: "Open Processes → Services, read the named unit's journal and review its start/restart controls. Resetting the failed marker does not repair the service.", Value: float64(len(names))})
		}
	}
	if dockerErr != nil {
		health.Silences = append(health.Silences, "Docker could not be read; container runtime was not assessed.")
	} else {
		groups := map[string][]string{}
		for _, ct := range containers {
			switch ct.State {
			case "restarting", "dead", "paused":
				groups[ct.State] = append(groups[ct.State], ct.Name)
			case "running":
				if !ct.Inspected {
					health.Silences = append(health.Silences, "Unread container health: "+ct.Name)
				} else if ct.Health == "unhealthy" {
					groups["unhealthy"] = append(groups["unhealthy"], ct.Name)
				}
			}
		}
		for _, state := range []string{"unhealthy", "dead", "restarting", "paused"} {
			names := groups[state]
			if len(names) == 0 {
				continue
			}
			sort.Strings(names)
			level := "warning"
			if state == "unhealthy" || state == "dead" {
				level = "critical"
			}
			health.Findings = append(health.Findings, metrics.Finding{ID: "docker." + state, Level: level, Title: fmt.Sprintf("%d containers are %s", len(names), state), Detail: strings.Join(names, ", "), Advice: "Open the named container in Docker for its failed check, logs and reviewed lifecycle controls. An unhealthy check alone does not trigger a Docker restart.", Value: float64(len(names))})
		}
	}
	rank := map[string]int{"ok": 0, "notice": 1, "warning": 2, "critical": 3}
	sort.SliceStable(health.Findings, func(i, j int) bool { return rank[health.Findings[i].Level] > rank[health.Findings[j].Level] })
	if len(health.Findings) > 0 {
		health.Status = health.Findings[0].Level
	}
	return health
}
