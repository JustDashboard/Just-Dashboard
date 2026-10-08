package api

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
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
	go func() {
		defer wg.Done()
		if units, unitErr = s.modules.systemd.List(ctx); unitErr == nil {
			// Why a unit failed is the first thing its fix needs, and the
			// listing alone does not say.
			s.modules.systemd.DescribeFailed(ctx, units)
		}
	}()
	go func() { defer wg.Done(); containers, dockerErr = s.modules.docker.ListContainers(ctx, true) }()
	wg.Wait()
	if !snap.Pressure.Supported {
		health.Silences = append(health.Silences, "Kernel pressure readings (PSI) are unavailable; CPU and disk are judged on load and I/O wait instead.")
	}
	return mergeRuntimeHealth(health, units, unitErr, containers, dockerErr)
}

func mergeRuntimeHealth(health metrics.Health, units []procs.Unit, unitErr error, containers []dockerx.Container, dockerErr error) metrics.Health {
	if unitErr != nil {
		health.Silences = append(health.Silences, "Service manager could not be read; failed services were not assessed.")
		summary := "not readable"
		if errors.Is(unitErr, procs.ErrNotInstalled) {
			summary = "no systemd"
		}
		health.SetArea(metrics.AreaServices, summary, false)
	} else {
		health.Findings = append(health.Findings, failedUnits(units)...)
		failed, running := 0, 0
		for _, unit := range units {
			switch unit.ActiveState {
			case "failed":
				failed++
			case "active":
				running++
			}
		}
		if failed > 0 {
			health.SetArea(metrics.AreaServices, fmt.Sprintf("%d failed", failed), true)
		} else {
			health.SetArea(metrics.AreaServices, fmt.Sprintf("%d running", running), true)
		}
	}
	if dockerErr != nil {
		health.Silences = append(health.Silences, "Docker could not be read; container runtime was not assessed.")
		health.SetArea(metrics.AreaContainers, "not available", false)
	} else {
		findings, running, broken := containerRuntime(containers, &health)
		health.Findings = append(health.Findings, findings...)
		if broken != "" {
			health.SetArea(metrics.AreaContainers, broken, true)
		} else {
			health.SetArea(metrics.AreaContainers, fmt.Sprintf("%d running", running), true)
		}
	}
	health.Settle()
	return health
}

// failedUnits is one finding naming every failed unit, each a subject its
// remedies — restart, clear, disable — act on by name.
func failedUnits(units []procs.Unit) []metrics.Finding {
	var failed []procs.Unit
	for _, unit := range units {
		if unit.ActiveState == "failed" {
			failed = append(failed, unit)
		}
	}
	if len(failed) == 0 {
		return nil
	}
	sort.Slice(failed, func(i, j int) bool { return failed[i].Name < failed[j].Name })
	names := make([]string, 0, len(failed))
	subjects := make([]metrics.Subject, 0, len(failed))
	var latest *time.Time
	for _, unit := range failed {
		names = append(names, unit.Name)
		subject := metrics.Subject{Kind: "unit", ID: unit.Name, Name: unit.Description, Detail: unit.Result}
		if subject.Name == "" {
			subject.Name = unit.Name
		}
		if unit.ChangedAt > 0 {
			at := time.Unix(unit.ChangedAt, 0).UTC()
			subject.Since = &at
			if latest == nil || at.After(*latest) {
				latest = &at
			}
		}
		subjects = append(subjects, subject)
	}
	title := fmt.Sprintf("%d services have failed", len(failed))
	if len(failed) == 1 {
		title = fmt.Sprintf("%s has failed", strings.TrimSuffix(failed[0].Name, ".service"))
	}
	evidence := []metrics.Fact{{Label: "Failed", Value: strconv.Itoa(len(failed))}}
	if latest != nil {
		evidence = append(evidence, metrics.Fact{Label: "Most recent", Value: agoWords(time.Since(*latest))})
	}
	return []metrics.Finding{{
		ID: "systemd.failed", Level: "warning", Title: title,
		Detail:   strings.Join(names, ", "),
		Advice:   "Read why it stopped in its journal, then restart it — or clear the failure, or disable it, if this server no longer needs it. Clearing the failed marker alone repairs nothing.",
		Value:    float64(len(failed)),
		Area:     metrics.AreaServices,
		Evidence: evidence,
		Subjects: subjects,
		Since:    latest,
	}}
}

// containerRuntime groups the containers that are not doing their job by
// state, and says how many are running. broken is the area's summary when
// anything is wrong.
func containerRuntime(containers []dockerx.Container, health *metrics.Health) (findings []metrics.Finding, running int, broken string) {
	groups := map[string][]dockerx.Container{}
	for _, ct := range containers {
		switch ct.State {
		case "restarting", "dead", "paused":
			groups[ct.State] = append(groups[ct.State], ct)
		case "running":
			running++
			if !ct.Inspected {
				health.Silences = append(health.Silences, "Unread container health: "+ct.Name)
			} else if ct.Health == "unhealthy" {
				groups["unhealthy"] = append(groups["unhealthy"], ct)
			}
		}
	}
	for _, state := range []string{"unhealthy", "dead", "restarting", "paused"} {
		group := groups[state]
		if len(group) == 0 {
			continue
		}
		sort.Slice(group, func(i, j int) bool { return group[i].Name < group[j].Name })
		names := make([]string, 0, len(group))
		subjects := make([]metrics.Subject, 0, len(group))
		for _, ct := range group {
			names = append(names, ct.Name)
			subjects = append(subjects, metrics.Subject{Kind: "container", ID: ct.ID, Name: ct.Name, Detail: state})
		}
		level := "warning"
		if state == "unhealthy" || state == "dead" {
			level = "critical"
		}
		title := fmt.Sprintf("%d containers are %s", len(group), state)
		if len(group) == 1 {
			title = fmt.Sprintf("%s is %s", group[0].Name, state)
		}
		if broken == "" {
			broken = fmt.Sprintf("%d %s", len(group), state)
		}
		findings = append(findings, metrics.Finding{
			ID: "docker." + state, Level: level, Title: title,
			Detail:   strings.Join(names, ", "),
			Advice:   containerAdvice[state],
			Value:    float64(len(group)),
			Area:     metrics.AreaContainers,
			Evidence: []metrics.Fact{{Label: "Affected", Value: strconv.Itoa(len(group))}, {Label: "Running", Value: strconv.Itoa(running)}},
			Subjects: subjects,
		})
	}
	return findings, running, broken
}

var containerAdvice = map[string]string{
	"unhealthy":  "Read the failing check and the logs, then restart it. An unhealthy check alone does not make Docker restart a container.",
	"dead":       "Docker could not stop or remove it cleanly. Read its logs, then remove and recreate it.",
	"restarting": "It is exiting and Docker keeps starting it again. Its logs say why it exits.",
	"paused":     "Its processes are frozen and it answers nothing until it is resumed.",
}

// agoWords is a duration as a reader says it, for evidence worded on the
// server.
func agoWords(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d/time.Hour))
	default:
		return fmt.Sprintf("%d days ago", int(d/(24*time.Hour)))
	}
}
