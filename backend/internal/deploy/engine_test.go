package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingExecutor struct {
	mu      sync.Mutex
	keys    []StepKey
	results map[StepKey]StepResult
	block   chan struct{}
	started chan StepKey
	cleanup int
}

func (e *recordingExecutor) CleanupCancelledRun(
	_ context.Context,
	_ EngineRun,
	_ string,
) (json.RawMessage, error) {
	e.mu.Lock()
	e.cleanup++
	e.mu.Unlock()
	return json.RawMessage(`{"completed":true,"fixture":"cleanup"}`), nil
}

func (e *recordingExecutor) Execute(ctx context.Context, execution StepExecution) StepResult {
	e.mu.Lock()
	e.keys = append(e.keys, execution.Step.Key)
	result, ok := e.results[execution.Step.Key]
	e.mu.Unlock()
	if e.started != nil {
		select {
		case e.started <- execution.Step.Key:
		default:
		}
	}
	if e.block != nil {
		select {
		case <-ctx.Done():
			return StepResult{State: StepCancelled}
		case <-e.block:
		}
	}
	if ok {
		return result
	}
	return StepResult{State: StepPassed}
}

func (e *recordingExecutor) executed() []StepKey {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]StepKey(nil), e.keys...)
}

type fixedReconciler struct {
	decision ReconcileResult
	seen     chan ReconcileRequest
}

func (r fixedReconciler) Reconcile(_ context.Context, request ReconcileRequest) ReconcileResult {
	if r.seen != nil {
		r.seen <- request
	}
	return r.decision
}

func TestEngineExecutesPersistentReleasePathToSuccess(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	run := f.enqueue(t, environmentID)
	executor := &recordingExecutor{results: map[StepKey]StepResult{}}
	engine := NewEngine(f.runs, executor, fixedReconciler{}, EngineConfig{
		WorkerID: "engine-success", PollEvery: 5 * time.Millisecond, LeaseTTL: time.Minute,
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := engine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	got := waitForRunState(t, f.runs, run.ID, RunSucceeded)
	if got.EndedAt == nil {
		t.Fatal("successful run has no terminal timestamp")
	}
	if keys := executor.executed(); !equalStepKeys(keys, DefaultStepKeys) {
		t.Fatalf("executed keys = %#v, want default release path %#v", keys, DefaultStepKeys)
	}
	steps, err := f.runs.Steps(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		if step.State != StepPassed {
			t.Fatalf("step %s state = %s", step.Key, step.State)
		}
	}
	shutdownEngine(t, engine)
}

func TestEngineFailureStopsBeforeLaterStepsAndPreservesTerminalEvidence(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	run := f.enqueue(t, environmentID)
	executor := &recordingExecutor{results: map[StepKey]StepResult{
		StepBuildArtifact: {
			State: StepFailed, ErrorCode: "builder_unavailable",
			ErrorMessage: "BuildKit fixture unavailable",
		},
	}}
	engine := NewEngine(f.runs, executor, fixedReconciler{}, EngineConfig{
		WorkerID: "engine-failure", PollEvery: 5 * time.Millisecond,
	}, nil)
	if err := engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := waitForRunState(t, f.runs, run.ID, RunFailed)
	if got.TerminalCode != "builder_unavailable" || got.TerminalReason != "BuildKit fixture unavailable" {
		t.Fatalf("terminal evidence = %q/%q", got.TerminalCode, got.TerminalReason)
	}
	want := DefaultStepKeys[:5]
	if keys := executor.executed(); !equalStepKeys(keys, want) {
		t.Fatalf("executed after failure = %#v, want %#v", keys, want)
	}
	shutdownEngine(t, engine)
}

func TestEngineMarksVerifiedActivationRecoveryAsRolledBack(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	run := f.enqueue(t, environmentID, func(req *RunRequest) {
		req.Steps = []StepKey{StepActivate}
	})
	executor := &recordingExecutor{results: map[StepKey]StepResult{
		StepActivate: {
			State: StepFailed, ErrorCode: "proxy_cutover_failed",
			ErrorMessage: "prior route restored and verified", Recovered: true,
		},
	}}
	engine := NewEngine(f.runs, executor, fixedReconciler{}, EngineConfig{
		WorkerID: "engine-recovered-activation", PollEvery: 5 * time.Millisecond,
	}, nil)
	if err := engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := waitForRunState(t, f.runs, run.ID, RunRolledBack)
	if got.TerminalCode != "proxy_cutover_failed" || got.TerminalReason != "prior route restored and verified" {
		t.Fatalf("rollback terminal evidence = %q/%q", got.TerminalCode, got.TerminalReason)
	}
	shutdownEngine(t, engine)
}

func TestEngineCancellationInterruptsAdapterAndRecordsStepCleanup(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	run := f.enqueue(t, environmentID, func(req *RunRequest) {
		req.Steps = []StepKey{StepBuildArtifact}
	})
	executor := &recordingExecutor{
		results: map[StepKey]StepResult{}, block: make(chan struct{}),
		started: make(chan StepKey, 1),
	}
	engine := NewEngine(f.runs, executor, fixedReconciler{}, EngineConfig{
		WorkerID: "engine-cancel", PollEvery: 5 * time.Millisecond,
	}, nil)
	if err := engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case key := <-executor.started:
		if key != StepBuildArtifact {
			t.Fatalf("started step = %s", key)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("adapter did not start")
	}
	if _, err := engine.Cancel(context.Background(), run.ID); err != nil {
		t.Fatal(err)
	}
	waitForRunState(t, f.runs, run.ID, RunCancelled)
	steps, err := f.runs.Steps(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].State != StepCancelled {
		t.Fatalf("cancelled steps = %#v", steps)
	}
	if string(steps[0].Cleanup) != `{"cause":"deployment cancellation requested by operator","context":"cancelled"}` {
		t.Fatalf("cancelled step cleanup = %s", steps[0].Cleanup)
	}
	executor.mu.Lock()
	cleanupCalls := executor.cleanup
	executor.mu.Unlock()
	if cleanupCalls != 1 {
		t.Fatalf("cancellation cleanup calls = %d, want 1", cleanupCalls)
	}
	events, err := f.runs.EventsAfter(context.Background(), run.ID, 0, 5000)
	if err != nil {
		t.Fatal(err)
	}
	foundCleanup := false
	for _, event := range events {
		if event.Type == EventStepEvidence && strings.Contains(string(event.Data), "cancellation_cleanup") {
			foundCleanup = true
		}
	}
	if !foundCleanup {
		t.Fatal("cancellation cleanup evidence was not persisted")
	}
	shutdownEngine(t, engine)
}

func TestRestartAtEveryStepUsesEvidenceWithoutDuplicatingSideEffects(t *testing.T) {
	pure := map[StepKey]bool{
		StepResolveSource: true, StepAnalyzePlan: true, StepRenderRuntime: true,
		StepVerifyReadiness: true, StepVerifySmoke: true,
	}
	for _, interrupted := range DefaultStepKeys {
		interrupted := interrupted
		t.Run(string(interrupted), func(t *testing.T) {
			f := newOrchestrationFixture(t)
			environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
			run := f.enqueue(t, environmentID)
			lease, err := f.runs.ClaimNext(context.Background(), "old-process", QueueBudget{}, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			setup := NewEngine(f.runs, &recordingExecutor{}, nil, EngineConfig{}, nil)
			for _, key := range DefaultStepKeys {
				current, err := f.runs.Run(context.Background(), run.ID)
				if err != nil {
					t.Fatal(err)
				}
				if err := setup.advanceRunForStep(context.Background(), *lease, current.State, key); err != nil {
					t.Fatal(err)
				}
				if _, err := f.runs.TransitionStep(context.Background(), run.ID, key, lease.Token,
					StepTransition{State: StepRunning}); err != nil {
					t.Fatal(err)
				}
				if key == interrupted {
					break
				}
				if _, err := f.runs.TransitionStep(context.Background(), run.ID, key, lease.Token,
					StepTransition{State: StepPassed}); err != nil {
					t.Fatal(err)
				}
			}
			f.advance(2 * time.Second)

			action := ReconcileComplete
			terminal := RunSucceeded
			if pure[interrupted] {
				action = ReconcileResume
			}
			if interrupted == StepReleaseTask {
				action = ReconcileFail
				terminal = RunFailed
			}
			seen := make(chan ReconcileRequest, 1)
			executor := &recordingExecutor{results: map[StepKey]StepResult{}}
			engine := NewEngine(f.runs, executor, fixedReconciler{
				decision: ReconcileResult{
					Action: action, StepResult: StepResult{State: StepPassed},
					TerminalCode: "interrupted_release_task",
					Reason:       "Completion token was not recorded; refusing replay",
				},
				seen: seen,
			}, EngineConfig{
				WorkerID: "new-process", PollEvery: 5 * time.Millisecond, LeaseTTL: time.Minute,
			}, nil)
			if err := engine.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case request := <-seen:
				if request.Step == nil || request.Step.Key != interrupted {
					t.Fatalf("reconciled step = %#v, want %s", request.Step, interrupted)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("expired run was not reconciled")
			}
			waitForRunState(t, f.runs, run.ID, terminal)
			executed := executor.executed()
			countInterrupted := 0
			for _, key := range executed {
				if key == interrupted {
					countInterrupted++
				}
			}
			switch {
			case action == ReconcileResume && countInterrupted != 1:
				t.Fatalf("safe resumed step executed %d times, want once", countInterrupted)
			case action != ReconcileResume && countInterrupted != 0:
				t.Fatalf("evidence-complete/non-idempotent step re-executed %d times", countInterrupted)
			}
			if interrupted == StepActivate {
				for _, key := range executed {
					if key == StepActivate {
						t.Fatal("activation was duplicated after restart")
					}
				}
			}
			shutdownEngine(t, engine)
		})
	}
}

func waitForRunState(
	t *testing.T,
	store *OrchestrationStore,
	runID int64,
	want RunState,
) *EngineRun {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last *EngineRun
	for time.Now().Before(deadline) {
		run, err := store.Run(context.Background(), runID)
		if err != nil {
			t.Fatal(err)
		}
		last = run
		if run.State == want {
			return run
		}
		if run.State.Terminal() && run.State != want {
			t.Fatalf("run reached %s, want %s (%s: %s)", run.State, want,
				run.TerminalCode, run.TerminalReason)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("run state = %#v after timeout, want %s", last, want)
	return nil
}

func shutdownEngine(t *testing.T, engine *Engine) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := engine.Shutdown(ctx); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func equalStepKeys(a, b []StepKey) bool {
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

// A step that only discovers there is nothing to do once it is running — no
// release task, no backup gate, no predecessor, no notification channel — is
// the ordinary shape of a first deployment. Returning `skipped` from `running`
// was refused by the transition table, which killed the worker and left the
// step running with no transcript and no terminal state.
func TestEngineCompletesWhenRunningStepsDiscoverNothingToDo(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	run := f.enqueue(t, environmentID)
	skipped := map[StepKey]StepResult{}
	for _, key := range []StepKey{
		StepReleaseTask, StepBackupGate, StepProvisionCertificate,
		StepVerifySmoke, StepRetirePrevious, StepNotify,
	} {
		skipped[key] = StepResult{State: StepSkipped}
	}
	executor := &recordingExecutor{results: skipped}
	engine := NewEngine(f.runs, executor, fixedReconciler{}, EngineConfig{
		WorkerID: "engine-skips", PollEvery: 5 * time.Millisecond, LeaseTTL: time.Minute,
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := engine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitForRunState(t, f.runs, run.ID, RunSucceeded)
	steps, err := f.runs.Steps(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		want := StepPassed
		if _, ok := skipped[step.Key]; ok {
			want = StepSkipped
		}
		if step.State != want {
			t.Fatalf("step %s state = %s, want %s", step.Key, step.State, want)
		}
		if step.EndedAt == nil {
			t.Fatalf("step %s has no terminal timestamp", step.Key)
		}
	}
	shutdownEngine(t, engine)
}
