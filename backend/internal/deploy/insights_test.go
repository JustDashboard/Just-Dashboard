package deploy

import (
	"testing"
	"time"
)

func TestInsightsSummariseDeliveryFrequencyFailuresAndRecovery(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	since := now.AddDate(0, 0, -30).Truncate(24 * time.Hour)
	at := func(daysAgo int, hour int) time.Time {
		return now.AddDate(0, 0, -daysAgo).Truncate(24 * time.Hour).Add(time.Duration(hour) * time.Hour)
	}
	run := func(operation Operation, state RunState, claimed time.Time, seconds int, code string) EngineRun {
		ended := claimed.Add(time.Duration(seconds) * time.Second)
		return EngineRun{Operation: operation, State: state, RequestedAt: claimed.Add(-time.Minute), ClaimedAt: &claimed, EndedAt: &ended, TerminalCode: code}
	}
	runs := []EngineRun{
		run(OperationDeploy, RunSucceeded, at(20, 9), 60, ""),
		run(OperationDeploy, RunFailed, at(10, 9), 30, "health_gate_failed"),
		run(OperationDeploy, RunRolledBack, at(10, 10), 45, "health_gate_failed"),
		run(OperationDeploy, RunSucceeded, at(10, 12), 120, ""), // recovers both failures: 3h and 2h
		run(OperationRestart, RunSucceeded, at(5, 9), 5, ""),    // not a release
		run(OperationDeploy, RunCancelled, at(3, 9), 10, "cancelled"),
		run(OperationForceBuild, RunSucceeded, at(2, 9), 300, ""),
		run(OperationDeploy, RunFailed, at(1, 9), 20, "build_failed"),
		{Operation: OperationDeploy, State: RunRunning, RequestedAt: now}, // in flight
	}
	insights := computeInsights(7, 30, since, now, runs)
	if insights.Runs != 7 || insights.Succeeded != 3 || insights.Failed != 3 || insights.RolledBack != 1 || insights.Cancelled != 1 {
		t.Fatalf("counts = %+v", insights)
	}
	if insights.SuccessRate != 0.5 || insights.FailureStreak != 1 {
		t.Fatalf("rate/streak = %v/%d", insights.SuccessRate, insights.FailureStreak)
	}
	if insights.MedianDurationSeconds != 120 || insights.P95DurationSeconds != 300 {
		t.Fatalf("durations = %d/%d", insights.MedianDurationSeconds, insights.P95DurationSeconds)
	}
	if insights.DeploysPerWeek < 0.699 || insights.DeploysPerWeek > 0.701 {
		t.Fatalf("deploys per week = %v", insights.DeploysPerWeek)
	}
	if insights.RecoveredFailures != 2 || insights.MeanRecoverySeconds != int64((3*3600-30+2*3600-45)/2)+int64(120) {
		// failures ended at 09:00:30 and 10:00:45; success ended at 12:02:00.
		t.Fatalf("recovery = %d over %d", insights.MeanRecoverySeconds, insights.RecoveredFailures)
	}
	if insights.LastFailureAt == nil || !insights.LastFailureAt.Equal(at(1, 9).Add(20*time.Second)) || insights.LastSuccessAt == nil {
		t.Fatalf("last markers = %v / %v", insights.LastFailureAt, insights.LastSuccessAt)
	}
	if len(insights.Daily) != 31 || insights.Daily[0].Date != since.Format("2006-01-02") || insights.Daily[30].Date != now.Format("2006-01-02") {
		t.Fatalf("daily axis = %d rows from %s", len(insights.Daily), insights.Daily[0].Date)
	}
	tenDaysAgo := insights.Daily[20]
	if tenDaysAgo.Date != at(10, 0).Format("2006-01-02") || tenDaysAgo.Succeeded != 1 || tenDaysAgo.Failed != 2 || tenDaysAgo.DurationSe != 120 {
		t.Fatalf("day = %+v", tenDaysAgo)
	}
	if len(insights.TopFailures) != 2 || insights.TopFailures[0] != (InsightsFailure{Code: "health_gate_failed", Count: 2}) || insights.TopFailures[1].Code != "build_failed" {
		t.Fatalf("top failures = %+v", insights.TopFailures)
	}
	empty := computeInsights(7, 30, since, now, nil)
	if empty.Runs != 0 || empty.SuccessRate != 0 || empty.MedianDurationSeconds != 0 || len(empty.Daily) != 31 || len(empty.TopFailures) != 0 {
		t.Fatalf("empty insights = %+v", empty)
	}
}

func TestProjectInsightsReadsPersistedRuns(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	run := f.enqueue(t, environmentID)
	ended := f.now.Add(2 * time.Minute)
	if _, err := f.store.DB.Exec(`UPDATE deploy_runs SET state='succeeded', status='success', claimed_at=?, ended_at=? WHERE id=?`, f.now.Unix(), ended.Unix(), run.ID); err != nil {
		t.Fatal(err)
	}
	f.runs.now = func() time.Time { return ended.Add(time.Hour) }
	insights, err := f.runs.ProjectInsights(t.Context(), f.projectID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if insights.WindowDays != 30 || insights.Runs != 1 || insights.Succeeded != 1 || insights.MedianDurationSeconds != 120 || insights.SuccessRate != 1 {
		t.Fatalf("insights = %+v", insights)
	}
	other, err := f.runs.ProjectInsights(t.Context(), f.projectID+99, 400)
	if err != nil || other.Runs != 0 || other.WindowDays != 365 {
		t.Fatalf("other project = %+v, %v", other, err)
	}
}
