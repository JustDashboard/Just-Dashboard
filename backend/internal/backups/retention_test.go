package backups

import (
	"testing"
	"time"
)

func TestPruneCandidatesKeepsByCountAndAgeButNeverTheNewest(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	runs := []*Run{
		{ID: 5, StartedAt: now.Add(-40 * day)},
		{ID: 4, StartedAt: now.Add(-41 * day)},
		{ID: 3, StartedAt: now.Add(-42 * day)},
		{ID: 2, StartedAt: now.Add(-43 * day)},
		{ID: 1, StartedAt: now.Add(-44 * day)},
	}
	ids := func(runs []*Run) []int64 {
		out := []int64{}
		for _, r := range runs {
			out = append(out, r.ID)
		}
		return out
	}
	equal := func(a, b []int64) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	if got := ids(pruneCandidates(runs, 3, 0, now)); !equal(got, []int64{2, 1}) {
		t.Fatalf("count only: %v", got)
	}
	// Every artifact is older than the age, and the newest still survives.
	if got := ids(pruneCandidates(runs, 0, 30, now)); !equal(got, []int64{4, 3, 2, 1}) {
		t.Fatalf("age only: %v", got)
	}
	if got := ids(pruneCandidates(runs, 4, 42, now)); !equal(got, []int64{2, 1}) {
		t.Fatalf("count and age: %v", got)
	}
	if got := ids(pruneCandidates(runs, 0, 0, now)); len(got) != 0 {
		t.Fatalf("no retention pruned %v", got)
	}
}

func TestJobIsOverdueAfterTwoMissedIntervals(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	yesterday := now.Add(-26 * time.Hour)
	threeDaysAgo := now.Add(-72 * time.Hour)
	cases := []struct {
		name string
		job  Job
		want bool
	}{
		{"nightly, ran last night", Job{Enabled: true, Schedule: "0 3 * * *", LastSuccessAt: &yesterday}, false},
		{"nightly, three days silent", Job{Enabled: true, Schedule: "0 3 * * *", LastSuccessAt: &threeDaysAgo}, true},
		{"nightly, never ran, created three days ago", Job{Enabled: true, Schedule: "0 3 * * *", CreatedAt: threeDaysAgo}, true},
		{"nightly, never ran, created today", Job{Enabled: true, Schedule: "0 3 * * *", CreatedAt: now.Add(-time.Hour)}, false},
		{"weekly, three days silent", Job{Enabled: true, Schedule: "0 3 * * 1", LastSuccessAt: &threeDaysAgo}, false},
		{"paused", Job{Enabled: false, Schedule: "0 3 * * *", LastSuccessAt: &threeDaysAgo}, false},
		{"manual only", Job{Enabled: true, Schedule: "", LastSuccessAt: &threeDaysAgo}, false},
	}
	for _, c := range cases {
		if got := c.job.IsOverdue(now); got != c.want {
			t.Errorf("%s: overdue=%v, want %v", c.name, got, c.want)
		}
	}
	if got := ScheduleInterval("0 3 * * *", now); got != 24*time.Hour {
		t.Fatalf("nightly interval %v", got)
	}
	if got := ScheduleInterval("@hourly", now); got != time.Hour {
		t.Fatalf("hourly interval %v", got)
	}
}

func TestValidateRejectsBadSchedulesAndContainerNames(t *testing.T) {
	job := Job{Name: "x", Sources: []string{"/srv"}, TargetKind: TargetLocal, Target: TargetConfig{Path: "/b"}}
	job.Schedule = "not cron"
	if err := job.Validate(); err == nil {
		t.Fatal("invalid schedule accepted")
	}
	job.Schedule = "0 3 * * *"
	job.PauseContainers = []string{"--rm"}
	if err := job.Validate(); err == nil {
		t.Fatal("option-shaped container name accepted")
	}
	job.PauseContainers = []string{" web "}
	if err := job.Validate(); err != nil {
		t.Fatal(err)
	}
	if job.PauseContainers[0] != "web" {
		t.Fatalf("container name not trimmed: %q", job.PauseContainers[0])
	}
}
