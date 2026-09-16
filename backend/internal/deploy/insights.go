package deploy

import (
	"context"
	"sort"
	"time"
)

// DeploymentInsights are the delivery figures a team asks about a project:
// how often it ships, how often that fails, how long a release takes and how
// quickly a failure is followed by a working release. They are computed from
// the persisted run history alone, so they describe what the engine actually
// did rather than what a dashboard estimated.
type DeploymentInsights struct {
	ProjectID     int64     `json:"projectId"`
	WindowDays    int       `json:"windowDays"`
	GeneratedAt   time.Time `json:"generatedAt"`
	Runs          int       `json:"runs"`
	Succeeded     int       `json:"succeeded"`
	Failed        int       `json:"failed"`
	RolledBack    int       `json:"rolledBack"`
	Cancelled     int       `json:"cancelled"`
	SuccessRate   float64   `json:"successRate"`
	FailureStreak int       `json:"failureStreak"`
	// Durations are for release operations that succeeded, measured from the
	// worker's claim to the terminal state; queue time is not the release.
	MedianDurationSeconds int64 `json:"medianDurationSeconds"`
	P95DurationSeconds    int64 `json:"p95DurationSeconds"`
	// DeploysPerWeek is the succeeded release rate across the window.
	DeploysPerWeek float64 `json:"deploysPerWeek"`
	// MeanRecoverySeconds averages the time from a failed release's end to the
	// end of the next successful one. Zero with RecoveredFailures zero means
	// no failure was ever recovered inside the window.
	MeanRecoverySeconds int64      `json:"meanRecoverySeconds"`
	RecoveredFailures   int        `json:"recoveredFailures"`
	LastSuccessAt       *time.Time `json:"lastSuccessAt,omitempty"`
	LastFailureAt       *time.Time `json:"lastFailureAt,omitempty"`
	// Daily is one row per day of the window, oldest first, with days without
	// activity present as zeros so a chart has an even axis.
	Daily []InsightsDay `json:"daily"`
	// TopFailures counts terminal codes so the most common way a deployment
	// fails is named rather than inferred from the transcript.
	TopFailures []InsightsFailure `json:"topFailures"`
}

type InsightsDay struct {
	Date       string `json:"date"`
	Succeeded  int    `json:"succeeded"`
	Failed     int    `json:"failed"`
	Cancelled  int    `json:"cancelled"`
	DurationSe int64  `json:"medianDurationSeconds"`
}

type InsightsFailure struct {
	Code  string `json:"code"`
	Count int    `json:"count"`
}

// Release operations are the ones that ship or restore code; a restart or a
// scheduler marker says nothing about delivery frequency.
func insightsReleaseOperation(operation Operation) bool {
	switch operation {
	case OperationDeploy, OperationRedeploy, OperationForceBuild, OperationRollback:
		return true
	}
	return false
}

const insightsMaxRuns = 2000

// ProjectInsights computes the figures over the project's runs requested in
// the last `windowDays` days (bounded to 7..365, default 30).
func (s *OrchestrationStore) ProjectInsights(ctx context.Context, projectID int64, windowDays int) (*DeploymentInsights, error) {
	if windowDays <= 0 {
		windowDays = 30
	}
	windowDays = max(7, min(365, windowDays))
	now := s.now().UTC()
	since := now.AddDate(0, 0, -windowDays).Truncate(24 * time.Hour)
	rows, err := s.db.QueryContext(ctx, `SELECT `+engineRunColumns+` FROM deploy_runs
		WHERE project_id = ? AND requested_at >= ? ORDER BY requested_at, id LIMIT ?`, projectID, since.Unix(), insightsMaxRuns)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []EngineRun{}
	for rows.Next() {
		run, err := scanEngineRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, *run)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return computeInsights(projectID, windowDays, since, now, runs), nil
}

func computeInsights(projectID int64, windowDays int, since, now time.Time, runs []EngineRun) *DeploymentInsights {
	insights := &DeploymentInsights{ProjectID: projectID, WindowDays: windowDays, GeneratedAt: now, Daily: []InsightsDay{}, TopFailures: []InsightsFailure{}}
	days := map[string]*InsightsDay{}
	dayDurations := map[string][]int64{}
	for day := since; !day.After(now); day = day.AddDate(0, 0, 1) {
		key := day.Format("2006-01-02")
		days[key] = &InsightsDay{Date: key}
		insights.Daily = append(insights.Daily, InsightsDay{Date: key})
	}
	durations := []int64{}
	failures := map[string]int{}
	var pendingFailures []time.Time
	var recovery []int64
	streak := 0
	for _, run := range runs {
		if !insightsReleaseOperation(run.Operation) || !run.State.Terminal() || run.EndedAt == nil {
			continue
		}
		insights.Runs++
		ended := run.EndedAt.UTC()
		key := ended.Format("2006-01-02")
		day := days[key]
		switch run.State {
		case RunSucceeded:
			insights.Succeeded++
			insights.LastSuccessAt = &ended
			streak = 0
			started := run.RequestedAt
			if run.ClaimedAt != nil {
				started = *run.ClaimedAt
			}
			if seconds := int64(ended.Sub(started).Seconds()); seconds >= 0 {
				durations = append(durations, seconds)
				if day != nil {
					dayDurations[key] = append(dayDurations[key], seconds)
				}
			}
			for _, failedAt := range pendingFailures {
				recovery = append(recovery, int64(ended.Sub(failedAt).Seconds()))
			}
			pendingFailures = nil
			if day != nil {
				day.Succeeded++
			}
		case RunFailed, RunFailedActivation, RunRolledBack:
			insights.Failed++
			if run.State == RunRolledBack {
				insights.RolledBack++
			}
			insights.LastFailureAt = &ended
			streak++
			code := run.TerminalCode
			if code == "" {
				code = "failed"
			}
			failures[code]++
			pendingFailures = append(pendingFailures, ended)
			if day != nil {
				day.Failed++
			}
		case RunCancelled, RunSuperseded:
			insights.Cancelled++
			if day != nil {
				day.Cancelled++
			}
		}
	}
	insights.FailureStreak = streak
	if decided := insights.Succeeded + insights.Failed; decided > 0 {
		insights.SuccessRate = float64(insights.Succeeded) / float64(decided)
	}
	insights.MedianDurationSeconds = insightsPercentile(durations, 0.5)
	insights.P95DurationSeconds = insightsPercentile(durations, 0.95)
	insights.DeploysPerWeek = float64(insights.Succeeded) / (float64(windowDays) / 7)
	insights.RecoveredFailures = len(recovery)
	if len(recovery) > 0 {
		var total int64
		for _, seconds := range recovery {
			total += seconds
		}
		insights.MeanRecoverySeconds = total / int64(len(recovery))
	}
	for index := range insights.Daily {
		key := insights.Daily[index].Date
		insights.Daily[index] = *days[key]
		insights.Daily[index].DurationSe = insightsPercentile(dayDurations[key], 0.5)
	}
	for code, count := range failures {
		insights.TopFailures = append(insights.TopFailures, InsightsFailure{Code: code, Count: count})
	}
	sort.Slice(insights.TopFailures, func(i, j int) bool {
		if insights.TopFailures[i].Count != insights.TopFailures[j].Count {
			return insights.TopFailures[i].Count > insights.TopFailures[j].Count
		}
		return insights.TopFailures[i].Code < insights.TopFailures[j].Code
	})
	if len(insights.TopFailures) > 5 {
		insights.TopFailures = insights.TopFailures[:5]
	}
	return insights
}

// insightsPercentile uses the nearest-rank method over a copy; an empty set is zero.
func insightsPercentile(values []int64, p float64) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rank := int(float64(len(sorted))*p+0.999999) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}
