package api

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/metrics"
)

// Right-sizing: what limits each deployment should have, read from the
// container history the metrics recorder already keeps (metrics/sizing.go
// holds the maths). Both routes are reads. Applying a suggestion is the
// configuration PUT the Runtime settings already make — the same audit, the
// same validation, the same deploy-key guard, and the same "applies with the
// next release" — so nothing here writes.

// environmentSizing is one environment's judgement as the routes answer it.
type environmentSizing struct {
	ProjectID       int64  `json:"projectId"`
	ProjectName     string `json:"projectName"`
	EnvironmentID   int64  `json:"environmentId"`
	EnvironmentName string `json:"environmentName"`
	EnvironmentKind string `json:"environmentKind"`
	// Status is "available"; "compose", a stack whose services keep the
	// limits its file declares; or "unavailable", no history on this host.
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	metrics.SizingResult
	// Revision is the saved plan's, which an apply writes over.
	Revision int `json:"revision"`
	// Pending says the saved limits differ from the live release's: they
	// apply with the next release.
	Pending bool       `json:"pending"`
	OOM     sizingOOMs `json:"oom"`
}

// sizingOOMs is what the Docker event log saw of the live release's memory.
type sizingOOMs struct {
	// Watching says the event log is following the daemon; Since is when it
	// began. Before then, and with Watching false, a kill would not be here.
	Watching bool       `json:"watching"`
	Since    *time.Time `json:"since"`
	Kills    int        `json:"kills"`
}

type fleetSizing struct {
	Status           string              `json:"status"`
	Reason           string              `json:"reason,omitempty"`
	RetentionSeconds int                 `json:"retentionSeconds"`
	Environments     []environmentSizing `json:"environments"`
}

func (s *Server) handleDeploymentSizing(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	targets, err := s.modules.deployPlanning.SizingTargets(r.Context(), projectID, environmentID)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	sizing, err := s.sizeEnvironment(r.Context(), targets[0], time.Now(), s.hostCPUs(r.Context()))
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, sizing)
	return nil
}

// handleDeploymentFleetSizing is every production and staging environment's
// judgement, the furthest off first, narrowed to a deploy key's deployments.
func (s *Server) handleDeploymentFleetSizing(w http.ResponseWriter, r *http.Request) error {
	targets, err := s.modules.deployPlanning.SizingTargets(r.Context(), 0, 0)
	if err != nil {
		return httpx.Internal(err)
	}
	principal := httpx.MustPrincipal(r)
	result := fleetSizing{Status: "available", Environments: []environmentSizing{}}
	if recorder := s.modules.metrics; recorder == nil || !recorder.Enabled() {
		result.Status, result.Reason = "unavailable", metricsOff
	} else {
		result.RetentionSeconds = int(recorder.Retention() / time.Second)
	}
	now, cpus := time.Now(), s.hostCPUs(r.Context())
	for _, target := range targets {
		if !keyReaches(principal, target.ProjectID) || target.Compose() {
			continue
		}
		sizing, err := s.sizeEnvironment(r.Context(), target, now, cpus)
		if err != nil {
			return httpx.Internal(err)
		}
		result.Environments = append(result.Environments, sizing)
	}
	sortSizing(result.Environments)
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

// sortSizing puts the environment that asks the most first: by verdict, then
// by how far off it is, then by name so the order holds still between polls.
func sortSizing(list []environmentSizing) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if ra, rb := metrics.VerdictRank(a.Verdict), metrics.VerdictRank(b.Verdict); ra != rb {
			return ra < rb
		}
		if a.OffBy != b.OffBy {
			return a.OffBy > b.OffBy
		}
		if a.ProjectName != b.ProjectName {
			return a.ProjectName < b.ProjectName
		}
		return a.EnvironmentID < b.EnvironmentID
	})
}

func (s *Server) hostCPUs(ctx context.Context) int {
	if s.modules.docker == nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, cpus := s.modules.docker.HostCapacity(ctx)
	return cpus
}

// sizeEnvironment judges one environment from the recorder's history and the
// event log's OOM kills.
func (s *Server) sizeEnvironment(ctx context.Context, target deploy.SizingTarget, now time.Time, hostCPUs int) (environmentSizing, error) {
	out := environmentSizing{
		ProjectID: target.ProjectID, ProjectName: target.ProjectName,
		EnvironmentID: target.EnvironmentID, EnvironmentName: target.EnvironmentName,
		EnvironmentKind: string(target.EnvironmentKind), Status: "available", Revision: target.Revision,
		Pending: target.LiveReleaseID > 0 && (target.MemoryMB != target.LiveMemoryMB || target.CPUs != target.LiveCPUs),
		SizingResult: metrics.SizingResult{Verdict: metrics.VerdictInsufficient, OffBy: 1,
			Window: metrics.SizingWindow{NeededSeconds: int64(metrics.SizingHistory / time.Second)}},
	}
	recorder := s.modules.metrics
	switch {
	case target.Compose():
		out.Status = "compose"
		out.Reason = "A Compose stack's services keep the limits its file declares, so there is nothing in the plan to size."
		return out, nil
	case recorder == nil || !recorder.Enabled():
		out.Status, out.Reason = "unavailable", metricsOff
		return out, nil
	}
	usage, err := recorder.RecentSizingUsage(ctx, target.ContainerIDs, now)
	if err != nil {
		return out, err
	}
	out.OOM = s.liveOOMKills(target, now.Add(-recorder.Retention()))
	out.SizingResult = metrics.Size(metrics.SizingInput{
		Usage: *usage, Interval: recorder.Interval(),
		MemoryLimitMB: target.MemoryMB, LiveMemoryLimitMB: target.LiveMemoryMB,
		CPULimit: target.CPUs, LiveCPULimit: target.LiveCPUs,
		OOMKills: out.OOM.Kills, ThrottledShare: usage.ThrottledShare(target.LiveContainerIDs), HostCPUs: hostCPUs,
	})
	if len(target.ContainerIDs) == 0 {
		out.Reason = "No release of this environment has run as a container yet."
	}
	return out, nil
}

// liveOOMKills counts the kernel's OOM kills of the live release's
// containers that the Docker event log holds since from. The log is the
// in-memory ring the Events feed reads, so it knows only what happened while
// it was listening; Since says from when.
func (s *Server) liveOOMKills(target deploy.SizingTarget, from time.Time) sizingOOMs {
	out := sizingOOMs{}
	if s.modules.dockerEvents == nil {
		return out
	}
	running, since, _ := s.modules.dockerEvents.Status()
	out.Watching = running
	if !since.IsZero() {
		out.Since = &since
	}
	events := s.modules.dockerEvents.Find(eventScanWidth, dockerx.EventFilter{Kinds: []string{"container"}})
	out.Kills = countOOMKills(events, target.LiveReleaseID, target.LiveContainerIDs, from)
	return out
}

// countOOMKills counts the "oom" events of a release's containers since from,
// by the release label the runtime owner stamps or, for a container whose
// labels the event did not carry, by its id.
func countOOMKills(events []dockerx.Event, releaseID int64, containerIDs []string, from time.Time) int {
	if releaseID <= 0 && len(containerIDs) == 0 {
		return 0
	}
	release := strconv.FormatInt(releaseID, 10)
	ids := map[string]bool{}
	for _, id := range containerIDs {
		ids[id] = true
	}
	kills := 0
	for _, event := range events {
		if event.Type != "container" || event.Action != "oom" || event.Time.Before(from) {
			continue
		}
		if (releaseID > 0 && event.Owner["release-id"] == release) || ids[event.ID] {
			kills++
		}
	}
	return kills
}
