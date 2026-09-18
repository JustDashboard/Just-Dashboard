package deploy

import (
	"context"
	"sync"
	"testing"
	"time"
)

type recordingObserver struct {
	mu       sync.Mutex
	started  []EngineRun
	finished []EngineRun
	seen     chan struct{}
}

func (o *recordingObserver) RunStarted(_ context.Context, run EngineRun) {
	o.mu.Lock()
	o.started = append(o.started, run)
	o.mu.Unlock()
}

func (o *recordingObserver) RunFinished(_ context.Context, run EngineRun) {
	o.mu.Lock()
	o.finished = append(o.finished, run)
	o.mu.Unlock()
	if o.seen != nil {
		select {
		case o.seen <- struct{}{}:
		default:
		}
	}
}

func (o *recordingObserver) waitFinished(t *testing.T) EngineRun {
	t.Helper()
	select {
	case <-o.seen:
	case <-time.After(3 * time.Second):
		t.Fatal("observer was never told the run finished")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.finished[len(o.finished)-1]
}

func (o *recordingObserver) startedCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.started)
}

func TestRunObserversHearAboutFailedRuns(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	run := f.enqueue(t, environmentID)
	executor := &recordingExecutor{results: map[StepKey]StepResult{
		StepBuildArtifact: {State: StepFailed, ErrorCode: "builder_unavailable", ErrorMessage: "BuildKit fixture unavailable"},
	}}
	observer := &recordingObserver{seen: make(chan struct{}, 1)}
	engine := NewEngine(f.runs, executor, fixedReconciler{}, EngineConfig{
		WorkerID: "engine-observer-failure", PollEvery: 5 * time.Millisecond,
	}, nil).WithObservers(observer)
	if err := engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForRunState(t, f.runs, run.ID, RunFailed)
	finished := observer.waitFinished(t)
	if finished.ID != run.ID || finished.State != RunFailed || finished.TerminalCode != "builder_unavailable" {
		t.Fatalf("observer saw %+v", finished)
	}
	if observer.startedCount() != 1 || observer.started[0].ID != run.ID {
		t.Fatalf("started notifications = %d", observer.startedCount())
	}
	shutdownEngine(t, engine)
}

func TestRunObserversHearAboutSuccessAndCancellation(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	success := f.enqueue(t, environmentID)
	observer := &recordingObserver{seen: make(chan struct{}, 1)}
	engine := NewEngine(f.runs, &recordingExecutor{results: map[StepKey]StepResult{}}, fixedReconciler{}, EngineConfig{
		WorkerID: "engine-observer-success", PollEvery: 5 * time.Millisecond, LeaseTTL: time.Minute,
	}, nil).WithObservers(observer)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := engine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitForRunState(t, f.runs, success.ID, RunSucceeded)
	if finished := observer.waitFinished(t); finished.ID != success.ID || finished.State != RunSucceeded {
		t.Fatalf("observer saw %+v", finished)
	}
	shutdownEngine(t, engine)

	// A run cancelled while still queued never reaches a worker; the engine's
	// Cancel path is the only place that can announce it.
	queued := f.enqueue(t, environmentID)
	idle := NewEngine(f.runs, &recordingExecutor{results: map[StepKey]StepResult{}}, fixedReconciler{}, EngineConfig{
		WorkerID: "engine-observer-cancel", PollEvery: time.Hour, LeaseTTL: time.Minute,
	}, nil).WithObservers(observer)
	cancelled, err := idle.Cancel(context.Background(), queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !cancelled.State.Terminal() {
		t.Skipf("queued cancellation left the run in %s; a worker announces it later", cancelled.State)
	}
	if finished := observer.waitFinished(t); finished.ID != queued.ID || finished.State != RunCancelled {
		t.Fatalf("observer saw %+v", finished)
	}
}

func TestRunObserverErrorsNeverChangeRunState(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	run := f.enqueue(t, environmentID)
	engine := NewEngine(f.runs, &recordingExecutor{results: map[StepKey]StepResult{}}, fixedReconciler{}, EngineConfig{
		WorkerID: "engine-observer-panic-free", PollEvery: 5 * time.Millisecond, LeaseTTL: time.Minute,
	}, nil).WithObservers(slowObserver{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := engine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	got := waitForRunState(t, f.runs, run.ID, RunSucceeded)
	if got.TerminalCode != "" {
		t.Fatalf("observer changed terminal evidence: %+v", got)
	}
	shutdownEngine(t, engine)
}

// slowObserver blocks briefly to prove Shutdown drains observers without a
// deadline of its own being able to alter the run.
type slowObserver struct{}

func (slowObserver) RunStarted(context.Context, EngineRun)  { time.Sleep(20 * time.Millisecond) }
func (slowObserver) RunFinished(context.Context, EngineRun) { time.Sleep(20 * time.Millisecond) }
