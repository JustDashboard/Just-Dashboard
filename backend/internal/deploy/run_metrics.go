package deploy

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/metrics"
)

type MetricsObserver interface {
	Enabled() bool
	ContainerIdentityRange(context.Context, []string, time.Time, time.Time, int) (*metrics.ContainerIdentityHistory, error)
	Range(context.Context, time.Time, time.Time, int) (*metrics.Series, error)
}

type ReleaseMetricWindow struct {
	ReleaseID int64                             `json:"releaseId"`
	Status    string                            `json:"status"`
	Reason    string                            `json:"reason,omitempty"`
	History   *metrics.ContainerIdentityHistory `json:"history,omitempty"`
}

type RunMetricComparison struct {
	Status                string              `json:"status"`
	Reason                string              `json:"reason,omitempty"`
	ActivationCompletedAt *time.Time          `json:"activationCompletedAt,omitempty"`
	Before                ReleaseMetricWindow `json:"before"`
	After                 ReleaseMetricWindow `json:"after"`
	HostBefore            *metrics.Series     `json:"hostBefore,omitempty"`
	HostAfter             *metrics.Series     `json:"hostAfter,omitempty"`
	HostReason            string              `json:"hostReason,omitempty"`
}

func runtimeMetricIDs(runtime ReleaseRuntime) []string {
	if runtime.Kind == "container" && runtime.RuntimeID != "" {
		return []string{runtime.RuntimeID}
	}
	if runtime.Kind != "compose" {
		return nil
	}
	var metadata dockerReleaseRuntimeMetadata
	if json.Unmarshal(runtime.Metadata, &metadata) != nil || metadata.Version != 1 || len(metadata.ContainerIDs) == 0 {
		return nil
	}
	ids := append([]string(nil), metadata.ContainerIDs...)
	sort.Strings(ids)
	for i, id := range ids {
		if id == "" || (i > 0 && id == ids[i-1]) {
			return nil
		}
	}
	return ids
}

// RunMetrics joins persisted runtime identities through the Metrics owner. It
// does not ask today's Docker inventory to identify yesterday's release.
func (s *OrchestrationStore) RunMetrics(ctx context.Context, owner MetricsObserver, snapshot RunSnapshot) (*RunMetricComparison, error) {
	result := &RunMetricComparison{Status: "unavailable",
		Before: ReleaseMetricWindow{Status: "unavailable"}, After: ReleaseMetricWindow{Status: "unavailable"}}
	if owner == nil || !owner.Enabled() {
		result.Reason = "Metrics history is unavailable or retention is disabled. Enable history to record future release evidence."
		return result, nil
	}
	releaseID := snapshot.Run.CandidateReleaseID
	if releaseID == 0 {
		releaseID = snapshot.Run.ReleaseID
	}
	anchor := runActivationCompleted(snapshot, releaseID)
	if releaseID <= 0 || anchor == nil {
		result.Reason = "This run has no completed activation evidence. Review its deployment transcript."
		return result, nil
	}
	release, err := s.Release(ctx, releaseID)
	if err != nil {
		return nil, err
	}
	if release.Release.EnvironmentID != snapshot.Run.EnvironmentID || release.Release.ProjectID != snapshot.Run.ProjectID {
		return nil, ErrInvalidPlan
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result.Status, result.ActivationCompletedAt = "available", anchor
	result.Before = s.releaseMetricWindow(ctx, owner, release.Release.PredecessorReleaseID, snapshot.Run.EnvironmentID, anchor.Add(-10*time.Minute), *anchor)
	result.After = s.releaseMetricWindow(ctx, owner, releaseID, snapshot.Run.EnvironmentID, *anchor, anchor.Add(10*time.Minute))
	before, beforeErr := hostMetricWindow(ctx, owner, anchor.Add(-10*time.Minute), *anchor)
	after, afterErr := hostMetricWindow(ctx, owner, *anchor, anchor.Add(10*time.Minute))
	if beforeErr != nil || afterErr != nil || before == nil || after == nil || len(before.Points) == 0 || len(after.Points) == 0 {
		result.HostReason = "Host history is incomplete or unavailable for these windows. Open Metrics to inspect retained history."
	}
	if beforeErr == nil {
		result.HostBefore = before
	}
	if afterErr == nil {
		result.HostAfter = after
	}
	return result, nil
}

func (s *OrchestrationStore) releaseMetricWindow(ctx context.Context, owner MetricsObserver, releaseID, environmentID int64, from, to time.Time) ReleaseMetricWindow {
	result := ReleaseMetricWindow{ReleaseID: releaseID, Status: "unavailable"}
	if releaseID == 0 {
		result.Reason = "There is no predecessor release to compare."
		return result
	}
	runtime, err := s.RuntimeForRelease(ctx, releaseID)
	if err != nil || runtime.EnvironmentID != environmentID {
		result.Reason = "Retained runtime identity is unavailable. Review release history."
		return result
	}
	ids := runtimeMetricIDs(*runtime)
	if len(ids) == 0 || len(ids) > 64 {
		result.Reason = "Complete runtime identity evidence is unavailable or exceeds the 64-container comparison limit."
		return result
	}
	history, err := owner.ContainerIdentityRange(ctx, ids, from, to, 120)
	if err != nil || history == nil {
		result.Reason = "Container history could not be read. Open Metrics to inspect retained history."
		return result
	}
	result.History = history
	result.Status = "available"
	for _, series := range history.Series {
		samples := 0
		for _, point := range series.Points {
			samples += point.Samples
		}
		interval := history.IntervalSeconds
		if interval <= 0 || len(series.Points) == 0 || samples < int(to.Sub(from).Seconds())/max(interval, 1) {
			result.Status = "partial"
		}
	}
	if len(history.Series) != len(ids) {
		result.Status = "partial"
	}
	if s.now().Before(to) {
		result.Status = "partial"
	}
	if result.Status == "partial" {
		result.Reason = "Some container samples are missing or the comparison window has not finished. No regression conclusion can be drawn yet."
	}
	return result
}

func hostMetricWindow(ctx context.Context, owner MetricsObserver, from, to time.Time) (*metrics.Series, error) {
	ceil := func(at time.Time) time.Time {
		second := at.Unix()
		if at.Nanosecond() != 0 {
			second++
		}
		return time.Unix(second, 0).UTC()
	}
	// Host Range is inclusive at both ends; adjacent comparison windows must
	// not count the activation sample twice.
	return owner.Range(ctx, ceil(from), ceil(to).Add(-time.Second), 120)
}
