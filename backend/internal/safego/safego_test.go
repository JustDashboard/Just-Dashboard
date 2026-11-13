package safego

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func capture() (*slog.Logger, *bytes.Buffer) {
	var out bytes.Buffer
	return slog.New(slog.NewTextHandler(&out, nil)), &out
}

func task(t *testing.T, name string) Task {
	t.Helper()
	for _, found := range Tasks() {
		if found.Name == name {
			return found
		}
	}
	t.Fatalf("no task named %q", name)
	return Task{}
}

func TestAPanickingLoopIsLoggedAndStartedAgain(t *testing.T) {
	log, out := capture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var runs atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		run(ctx, log, "test parser", func(ctx context.Context) {
			if runs.Add(1) <= 2 {
				var lines []string
				_ = lines[3] // the parser fault: an unusual line
			}
			<-ctx.Done()
		}, time.Millisecond, 5*time.Millisecond)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for runs.Load() < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("the loop ran %d times, want it started again after each panic", runs.Load())
		}
		time.Sleep(time.Millisecond)
	}
	if got := task(t, "test parser"); !got.Running || got.Panics != 2 || !strings.Contains(got.LastPanic, "index out of range") || got.LastPanicAt == nil {
		t.Fatalf("task = %+v, want it running after two panics", got)
	}
	logged := out.String()
	if strings.Count(logged, "background task panicked") != 2 || !strings.Contains(logged, `task="test parser"`) {
		t.Fatalf("log = %s, want each panic said once under the task's name", logged)
	}
	if !strings.Contains(logged, "safego_test.go") {
		t.Fatalf("log = %s, want the stack that leads to the fault", logged)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop did not return when its context ended")
	}
	if got := task(t, "test parser"); got.Running {
		t.Fatalf("task = %+v, want it stopped", got)
	}
}

// A loop's Stop waits on a channel the loop closed on its way out. Closed
// from inside a function that is called again, it would be closed twice.
func TestWhatALoopDoesOnceIsDoneWhenItHasReturnedForGood(t *testing.T) {
	log, _ := capture()
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	var runs atomic.Int32
	finished := launch(ctx, log, "test stop channel", func(ctx context.Context) {
		if runs.Add(1) == 1 {
			panic("fault")
		}
		<-ctx.Done()
	}, time.Millisecond, time.Second, []func(){func() { close(stopped) }})
	deadline := time.Now().Add(5 * time.Second)
	for runs.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("the loop was not started again")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-stopped:
		t.Fatal("the stop channel closed while the loop was running again")
	default:
	}
	cancel()
	<-finished
	select {
	case <-stopped:
	default:
		t.Fatal("the stop channel was not closed before Go's own")
	}
}

func TestALoopThatReturnsIsNotStartedAgain(t *testing.T) {
	log, _ := capture()
	var runs atomic.Int32
	<-Go(context.Background(), log, "test returns", func(context.Context) { runs.Add(1) })
	if runs.Load() != 1 {
		t.Fatalf("ran %d times, want once", runs.Load())
	}
	if got := task(t, "test returns"); got.Running || got.Panics != 0 {
		t.Fatalf("task = %+v", got)
	}
}

// A task's owner stops it by ending its context. One waiting out a pause
// after a panic must stop too, or Stop would hang for up to a minute.
func TestACancelledLoopIsNotStartedAgainNorWaitedOutItsPause(t *testing.T) {
	log, _ := capture()
	ctx, cancel := context.WithCancel(context.Background())
	var runs atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		run(ctx, log, "test cancelled", func(context.Context) {
			runs.Add(1)
			panic("fault")
		}, time.Hour, time.Hour)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for task0 := (Task{}); task0.RestartAt == nil; {
		if time.Now().After(deadline) {
			t.Fatal("the task never reached its pause")
		}
		time.Sleep(time.Millisecond)
		for _, found := range Tasks() {
			if found.Name == "test cancelled" {
				task0 = found
			}
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled task sat out its pause")
	}
	if runs.Load() != 1 {
		t.Fatalf("ran %d times, want once", runs.Load())
	}
}

func TestThePauseDoublesUpToItsLimit(t *testing.T) {
	log, _ := capture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var starts []time.Time
	done := make(chan struct{})
	go func() {
		defer close(done)
		run(ctx, log, "test pauses", func(context.Context) {
			starts = append(starts, time.Now())
			if len(starts) == 5 {
				cancel()
				return
			}
			panic("fault")
		}, 20*time.Millisecond, 50*time.Millisecond)
	}()
	<-done
	// 20, 40, then held at 50.
	want := []time.Duration{20, 40, 50, 50}
	for i, least := range want {
		if gap := starts[i+1].Sub(starts[i]); gap < least*time.Millisecond {
			t.Fatalf("pause %d was %s, want at least %dms", i+1, gap, least)
		}
	}
}

func TestOnceRunsOnceAndSurvivesItsPanic(t *testing.T) {
	log, out := capture()
	<-Once(log, "test once", func() { panic("reconcile fault") })
	if got := task(t, "test once"); got.Running || got.Panics != 1 || got.LastPanic != "reconcile fault" {
		t.Fatalf("task = %+v", got)
	}
	if !strings.Contains(out.String(), "reconcile fault") {
		t.Fatalf("log = %s", out.String())
	}
}

func TestRecoverEndsOnlyItsOwnGoroutine(t *testing.T) {
	log, out := capture()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer Recover(log, "test socket keepalive")
		panic("closed socket")
	}()
	<-done
	if got := task(t, "test socket keepalive"); got.Panics != 1 {
		t.Fatalf("task = %+v", got)
	}
	if !strings.Contains(out.String(), "closed socket") {
		t.Fatalf("log = %s", out.String())
	}
	found := false
	for _, f := range Faulted() {
		found = found || f.Name == "test socket keepalive"
	}
	if !found {
		t.Fatal("Faulted does not list a task that panicked")
	}
}

func TestTryTurnsAPanicIntoTheWorksError(t *testing.T) {
	log, out := capture()
	err := Try(log, "test job", func() error {
		var rows map[string]int
		rows["a"] = 1 // the fault
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "assignment to entry in nil map") {
		t.Fatalf("err = %v, want the panic as an error", err)
	}
	if got := task(t, "test job"); got.Panics != 1 {
		t.Fatalf("task = %+v", got)
	}
	if !strings.Contains(out.String(), "safego_test.go") {
		t.Fatalf("log = %s, want the stack", out.String())
	}
	want := errors.New("plain failure")
	if err := Try(log, "test job", func() error { return want }); err != want {
		t.Fatalf("err = %v, want fn's own error untouched", err)
	}
}

// A Stop that clears its done field before waiting still waits on the channel
// the loop was started with.
func TestCloseReadsItsChannelWhenTheTaskIsLaunched(t *testing.T) {
	log, _ := capture()
	type loop struct{ done chan struct{} }
	l := &loop{done: make(chan struct{})}
	started := l.done
	ctx, cancel := context.WithCancel(context.Background())
	finished := Go(ctx, log, "test close", func(ctx context.Context) { <-ctx.Done() }, Close(l.done))
	l.done = nil // what such a Stop does before it waits
	cancel()
	<-finished
	select {
	case <-started:
	default:
		t.Fatal("the channel the loop was started with was not closed")
	}
}
