package safego

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPassesSayTheFirstFailureAndTheRecoveryAndNothingBetween(t *testing.T) {
	log, out := capture()
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	passes := NewPasses(log, "git branch watch")
	passes.now = func() time.Time { return now }
	ctx := context.Background()

	passes.Report(ctx, nil)
	if out.Len() != 0 {
		t.Fatalf("log = %s, want nothing for a pass that worked", out.String())
	}
	busy := errors.New("database is locked")
	for range 12 { // a minute of five-second passes
		passes.Report(ctx, busy)
		now = now.Add(5 * time.Second)
	}
	logged := out.String()
	if strings.Count(logged, "background task failed") != 1 || strings.Count(logged, "\n") != 1 {
		t.Fatalf("log = %s, want the failure said once", logged)
	}
	if !strings.Contains(logged, `task="git branch watch"`) || !strings.Contains(logged, "database is locked") {
		t.Fatalf("log = %s, want the task and its error", logged)
	}

	passes.Report(ctx, nil)
	logged = out.String()
	if !strings.Contains(logged, "background task recovered") || !strings.Contains(logged, "after=1m0s") || !strings.Contains(logged, "failed_passes=12") {
		t.Fatalf("log = %s, want the recovery with how long it took", logged)
	}
	passes.Report(ctx, nil)
	if strings.Count(out.String(), "\n") != 2 {
		t.Fatalf("log = %s, want nothing more once it works", out.String())
	}
	// The next failure is a first failure again.
	passes.Report(ctx, busy)
	if strings.Count(out.String(), "background task failed") != 2 {
		t.Fatalf("log = %s, want a later failure said", out.String())
	}
}

func TestPassesSayAChangedFailureAndRemindEachHour(t *testing.T) {
	log, out := capture()
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	passes := NewPasses(log, "preview reconciler")
	passes.now = func() time.Time { return now }
	ctx := context.Background()

	passes.Report(ctx, errors.New("database is locked"))
	// A different error at once is held back: one whose text moves with
	// every pass must not become a line a pass.
	now = now.Add(5 * time.Second)
	passes.Report(ctx, errors.New("no such table: deploy_previews"))
	if strings.Contains(out.String(), "no such table") {
		t.Fatalf("log = %s, want a changed error held for a minute", out.String())
	}
	now = now.Add(time.Minute)
	passes.Report(ctx, errors.New("no such table: deploy_previews"))
	if strings.Count(out.String(), "background task is still failing") != 1 || !strings.Contains(out.String(), "no such table") {
		t.Fatalf("log = %s, want the changed error said", out.String())
	}
	// The same error is not said again until an hour has passed.
	now = now.Add(30 * time.Minute)
	passes.Report(ctx, errors.New("no such table: deploy_previews"))
	if strings.Count(out.String(), "background task is still failing") != 1 {
		t.Fatalf("log = %s, want silence within the hour", out.String())
	}
	now = now.Add(31 * time.Minute)
	passes.Report(ctx, errors.New("no such table: deploy_previews"))
	if strings.Count(out.String(), "background task is still failing") != 2 || !strings.Contains(out.String(), "failed_passes=5") {
		t.Fatalf("log = %s, want an hourly reminder with the count", out.String())
	}
}

// Stop and shutdown end a pass in the middle. That is not a failure, and not
// a recovery either.
func TestPassesIgnoreAPassCutShortByItsContext(t *testing.T) {
	log, out := capture()
	passes := NewPasses(log, "database network reconciler")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	passes.Report(ctx, context.Canceled)
	passes.Report(ctx, nil)
	if out.Len() != 0 {
		t.Fatalf("log = %s, want nothing", out.String())
	}
	var none *Passes
	none.Report(context.Background(), errors.New("no reporter")) // must not panic
}

// What a loop's passes returned is kept where it can be read, not only said
// in the log by change of state: the last pass, whether the loop is failing
// and since when, and the last failure after it has recovered.
func TestPassesAreRememberedForAReader(t *testing.T) {
	log, _ := capture()
	const name = "test loop whose passes are remembered"
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	passes := NewPasses(log, name)
	passes.now = func() time.Time { return now }
	ctx := context.Background()

	if _, known := PassStates()[name]; known {
		t.Fatal("a loop that has reported nothing has a state")
	}
	passes.Report(ctx, nil)
	state := PassStates()[name]
	if state.Passes != 1 || !state.LastPassAt.Equal(now) || state.FailingSince != nil || state.LastError != "" {
		t.Fatalf("after one pass that worked: %+v", state)
	}

	first := now.Add(5 * time.Second)
	now = first
	passes.Report(ctx, errors.New("database is locked"))
	now = now.Add(5 * time.Second)
	passes.Report(ctx, errors.New("no route to host"))
	state = PassStates()[name]
	if state.Passes != 3 || state.FailedPasses != 2 || state.FailingSince == nil || !state.FailingSince.Equal(first) ||
		state.LastError != "no route to host" || state.LastErrorAt == nil || !state.LastErrorAt.Equal(now) {
		t.Fatalf("while failing: %+v", state)
	}

	failedAt := now
	now = now.Add(5 * time.Second)
	passes.Report(ctx, nil)
	state = PassStates()[name]
	if state.FailingSince != nil || state.FailedPasses != 0 || !state.LastPassAt.Equal(now) {
		t.Fatalf("after the recovery: %+v", state)
	}
	if state.LastError != "no route to host" || !state.LastErrorAt.Equal(failedAt) {
		t.Fatalf("the last failure was forgotten with the recovery: %+v", state)
	}

	// A pass cut short by its context is no pass.
	stopped, cancel := context.WithCancel(ctx)
	cancel()
	passes.Report(stopped, errors.New("context canceled"))
	if got := PassStates()[name]; got.Passes != 4 || got.LastError != "no route to host" {
		t.Fatalf("a pass that was cut short was counted: %+v", got)
	}
}
