package deploy

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// The run page reads a run's snapshot once and follows its events, so every
// instant it draws after that has to arrive on an event: when the run was
// claimed and ended, when each step attempt started and ended.
func TestStateEventsCarryTheTimesTheyRecord(t *testing.T) {
	ctx := context.Background()
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	run := f.enqueue(t, environmentID, func(req *RunRequest) {
		req.Steps = []StepKey{StepResolveSource}
	})
	claimedAt := f.now
	lease, err := f.runs.ClaimNext(ctx, "worker-a", QueueBudget{}, time.Minute)
	if err != nil || lease == nil {
		t.Fatalf("claim = %#v, %v", lease, err)
	}
	f.advance(3 * time.Second)
	startedAt := f.now
	if _, err := f.runs.TransitionStep(ctx, run.ID, StepResolveSource, lease.Token,
		StepTransition{State: StepRunning}); err != nil {
		t.Fatal(err)
	}
	f.advance(40 * time.Second)
	stepEnded := f.now
	if _, err := f.runs.TransitionStep(ctx, run.ID, StepResolveSource, lease.Token,
		StepTransition{State: StepPassed}); err != nil {
		t.Fatal(err)
	}
	f.advance(2 * time.Second)
	runEnded := f.now
	for _, state := range []RunState{RunRunning, RunFailed} {
		if _, err := f.runs.TransitionRun(ctx, run.ID, lease.Token, state, TransitionDetail{Code: "test"}); err != nil {
			t.Fatalf("transition to %s: %v", state, err)
		}
	}

	events, err := f.runs.EventsAfter(ctx, run.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	type payload struct {
		State     string     `json:"state"`
		ClaimedAt *time.Time `json:"claimedAt"`
		StartedAt *time.Time `json:"startedAt"`
		EndedAt   *time.Time `json:"endedAt"`
	}
	var runStates, stepStates []payload
	for _, event := range events {
		var data payload
		if err := json.Unmarshal(event.Data, &data); err != nil {
			t.Fatal(err)
		}
		switch event.Type {
		case EventRunState:
			runStates = append(runStates, data)
		case EventStepState:
			stepStates = append(stepStates, data)
		}
	}
	same := func(at *time.Time, want time.Time) bool { return at != nil && at.Equal(want) }

	byState := map[string]payload{}
	for _, data := range runStates {
		byState[data.State] = data
	}
	if got := byState[string(RunPreparing)]; !same(got.ClaimedAt, claimedAt) || got.EndedAt != nil {
		t.Fatalf("claim event = %#v, want claimedAt %s and no end", got, claimedAt)
	}
	if got := byState[string(RunRunning)]; got.EndedAt != nil || got.ClaimedAt != nil {
		t.Fatalf("a run moving on is not an end: %#v", got)
	}
	if got := byState[string(RunFailed)]; !same(got.EndedAt, runEnded) {
		t.Fatalf("terminal event = %#v, want endedAt %s", got, runEnded)
	}

	if len(stepStates) != 2 {
		t.Fatalf("step events = %#v", stepStates)
	}
	if running := stepStates[0]; running.State != string(StepRunning) ||
		!same(running.StartedAt, startedAt) || running.EndedAt != nil {
		t.Fatalf("running step event = %#v, want startedAt %s and no end", running, startedAt)
	}
	if passed := stepStates[1]; !same(passed.StartedAt, startedAt) || !same(passed.EndedAt, stepEnded) {
		t.Fatalf("passed step event = %#v, want %s → %s", passed, startedAt, stepEnded)
	}
}

// A run cancelled before a slot took it ends in the same transaction, and its
// last event says when.
func TestACancelledQueuedRunSaysWhenItEnded(t *testing.T) {
	ctx := context.Background()
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	run := f.enqueue(t, environmentID, func(req *RunRequest) {
		req.Steps = []StepKey{StepResolveSource}
	})
	f.advance(5 * time.Second)
	if _, err := f.runs.RequestCancellation(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	events, err := f.runs.EventsAfter(ctx, run.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	var data struct {
		State   string     `json:"state"`
		EndedAt *time.Time `json:"endedAt"`
	}
	if err := json.Unmarshal(last.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.State != string(RunCancelled) || data.EndedAt == nil || !data.EndedAt.Equal(f.now) {
		t.Fatalf("cancelled event = %s, want endedAt %s", last.Data, f.now)
	}
}

func TestWithEventTimesLeavesOutTimesNotRecorded(t *testing.T) {
	at := time.Date(2026, 9, 2, 12, 0, 0, 0, time.FixedZone("x", 3600))
	got := withEventTimes(map[string]any{"state": "running"},
		"startedAt", &at, "endedAt", (*time.Time)(nil), "claimedAt", &time.Time{})
	if _, ok := got["endedAt"]; ok {
		t.Fatalf("a nil time was sent: %#v", got)
	}
	if _, ok := got["claimedAt"]; ok {
		t.Fatalf("a zero time was sent: %#v", got)
	}
	if got["startedAt"] != at.UTC() {
		t.Fatalf("startedAt = %#v, want it in UTC", got["startedAt"])
	}
}
