package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	basestore "github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

type orchestrationFixture struct {
	store     *basestore.Store
	runs      *OrchestrationStore
	projectID int64
	now       time.Time
	mu        sync.RWMutex
}

func newOrchestrationFixture(t *testing.T) *orchestrationFixture {
	t.Helper()
	st, err := basestore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	f := &orchestrationFixture{
		store: st,
		now:   time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC),
	}
	result, err := st.DB.Exec(`
		INSERT INTO deploy_projects(
		  name, repo_path, branch, compose_file, pre_command, post_command,
		  hook_secret, hook_id, enabled, created_at, profile, updated_at)
		VALUES('orch-test', '/srv/orch-test', 'main', 'compose.yml', '', '',
		       'sealed', 'orch-test-hook', 1, ?, 'compose', ?)`, f.now.Unix(), f.now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	f.projectID, err = result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	f.runs = NewOrchestrationStore(st)
	f.runs.now = func() time.Time {
		f.mu.RLock()
		defer f.mu.RUnlock()
		return f.now
	}
	return f
}

func (f *orchestrationFixture) advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
}

func (f *orchestrationFixture) addEnvironment(t *testing.T, slug string, kind EnvironmentKind) int64 {
	t.Helper()
	result, err := f.store.DB.Exec(`
		INSERT INTO deploy_environments(
		  project_id, name, slug, kind, desired_revision, strategy,
		  expected_downtime, protected, created_at, updated_at)
		VALUES(?, ?, ?, ?, 1, 'stop_first', 1, 1, ?, ?)`,
		f.projectID, slug, slug, kind, f.now.Unix(), f.now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *orchestrationFixture) enqueue(
	t *testing.T,
	environmentID int64,
	mutate ...func(*RunRequest),
) *EngineRun {
	t.Helper()
	req := RunRequest{
		ProjectID: f.projectID, EnvironmentID: environmentID,
		Operation: OperationDeploy, Trigger: TriggerManual, Actor: "admin",
		RequestDigest: fmt.Sprintf("digest-%d-%d", environmentID, time.Now().UnixNano()),
		PlanRevision:  1, SlotClass: SlotLight,
	}
	for _, fn := range mutate {
		fn(&req)
	}
	run, created, err := f.runs.Enqueue(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("test enqueue unexpectedly joined an existing run")
	}
	return run
}

func TestEnqueuePersistsRequestStepsEventsAndIdempotency(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	req := RunRequest{
		ProjectID: f.projectID, EnvironmentID: environmentID,
		Operation: OperationDeploy, Trigger: TriggerAPI, Actor: "token:ci",
		IdempotencyKey: "delivery-7", RequestDigest: "sha256:request-a",
		PlanRevision: 3, SlotClass: SlotHeavy,
	}
	run, created, err := f.runs.Enqueue(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !created || run.State != RunQueued || run.QueuedAt == nil {
		t.Fatalf("enqueue = %#v, created=%v", run, created)
	}
	steps, err := f.runs.Steps(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != len(DefaultStepKeys) {
		t.Fatalf("steps = %d, want %d", len(steps), len(DefaultStepKeys))
	}
	for i, step := range steps {
		if step.Key != DefaultStepKeys[i] || step.Ordinal != i+1 || step.State != StepPending {
			t.Fatalf("step %d = %#v", i, step)
		}
	}
	events, err := f.runs.EventsAfter(context.Background(), run.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %d, want requested/validating/queued", len(events))
	}
	for i, event := range events {
		if event.Seq != int64(i+1) || event.Type != EventRunState {
			t.Fatalf("event %d = %#v", i, event)
		}
	}

	replay, created, err := f.runs.Enqueue(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if created || replay.ID != run.ID {
		t.Fatalf("idempotent replay = %#v, created=%v; want run %d", replay, created, run.ID)
	}
	req.RequestDigest = "sha256:different"
	if _, _, err := f.runs.Enqueue(context.Background(), req); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting idempotency key error = %v", err)
	}

	reopened := NewOrchestrationStore(f.store)
	got, err := reopened.Snapshot(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Run.State != RunQueued || len(got.Steps) != len(DefaultStepKeys) {
		t.Fatalf("reopened snapshot = %#v", got)
	}
}

func TestClaimFencesRunAndStepTransitions(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	run := f.enqueue(t, environmentID, func(req *RunRequest) {
		req.Steps = []StepKey{StepResolveSource}
	})
	lease, err := f.runs.ClaimNext(context.Background(), "worker-a", QueueBudget{}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if lease == nil || lease.RunID != run.ID {
		t.Fatalf("lease = %#v, want run %d", lease, run.ID)
	}
	if _, err := f.runs.AppendLog(context.Background(), run.ID, 0, "stale-token", "stdout", "no"); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale append error = %v", err)
	}
	if _, err := f.runs.TransitionRun(context.Background(), run.ID, lease.Token, RunVerifying, TransitionDetail{}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("forbidden run transition error = %v", err)
	}
	if _, err := f.runs.TransitionStep(context.Background(), run.ID, StepResolveSource,
		lease.Token, StepTransition{State: StepPassed}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("forbidden step transition error = %v", err)
	}
	step, err := f.runs.TransitionStep(context.Background(), run.ID, StepResolveSource,
		lease.Token, StepTransition{State: StepRunning})
	if err != nil {
		t.Fatal(err)
	}
	large := string(make([]byte, maxLogTextBytes+37)) + "é"
	event, err := f.runs.AppendLog(context.Background(), run.ID, step.ID, lease.Token, "stdout", large)
	if err != nil {
		t.Fatal(err)
	}
	var logData struct {
		Text      string `json:"text"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal(event.Data, &logData); err != nil {
		t.Fatal(err)
	}
	if !logData.Truncated || len(logData.Text) > maxLogTextBytes || !utf8.ValidString(logData.Text) {
		t.Fatalf("log truncation = len %d, truncated %v, utf8 %v",
			len(logData.Text), logData.Truncated, utf8.ValidString(logData.Text))
	}
	if _, err := f.runs.TransitionStep(context.Background(), run.ID, StepResolveSource,
		lease.Token, StepTransition{State: StepFailed, ErrorCode: "git_unavailable"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runs.TransitionStep(context.Background(), run.ID, StepResolveSource,
		lease.Token, StepTransition{State: StepPending}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("retry without attempt increment error = %v", err)
	}
	retry, err := f.runs.TransitionStep(context.Background(), run.ID, StepResolveSource,
		lease.Token, StepTransition{State: StepPending, Retry: true})
	if err != nil {
		t.Fatal(err)
	}
	if retry.Attempt != 2 || retry.ID == step.ID {
		t.Fatalf("retry step = %#v", retry)
	}
	for _, state := range []StepState{StepRunning, StepPassed} {
		if _, err := f.runs.TransitionStep(context.Background(), run.ID, StepResolveSource,
			lease.Token, StepTransition{State: state}); err != nil {
			t.Fatal(err)
		}
	}
	for _, state := range []RunState{RunRunning, RunVerifying, RunActivating, RunSucceeded} {
		if _, err := f.runs.TransitionRun(context.Background(), run.ID, lease.Token, state,
			TransitionDetail{}); err != nil {
			t.Fatalf("transition to %s: %v", state, err)
		}
	}
	if _, err := f.runs.ActiveLease(context.Background(), run.ID); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("terminal run retained lease: %v", err)
	}
	events, err := f.runs.EventsAfter(context.Background(), run.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for i, event := range events {
		if event.Seq != int64(i+1) {
			t.Fatalf("event sequence[%d] = %d", i, event.Seq)
		}
	}
}

func TestQueueEnforcesPriorityEnvironmentAndHostBudgets(t *testing.T) {
	f := newOrchestrationFixture(t)
	environments := make([]int64, 5)
	for i := range environments {
		environments[i] = f.addEnvironment(t, fmt.Sprintf("env-%d", i), EnvironmentStaging)
	}
	firstHeavy := f.enqueue(t, environments[0], func(req *RunRequest) {
		req.SlotClass, req.Priority = SlotHeavy, 10
	})
	highHeavy := f.enqueue(t, environments[1], func(req *RunRequest) {
		req.SlotClass, req.Priority = SlotHeavy, 1000
	})
	lightA := f.enqueue(t, environments[2], func(req *RunRequest) { req.Priority = 900 })
	lightB := f.enqueue(t, environments[3], func(req *RunRequest) { req.Priority = 800 })
	_ = f.enqueue(t, environments[4], func(req *RunRequest) { req.Priority = 700 })

	budget := QueueBudget{Heavy: 1, Light: 2}
	lease1, err := f.runs.ClaimNext(context.Background(), "worker-1", budget, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if lease1.RunID != highHeavy.ID {
		t.Fatalf("first claim = run %d, want highest-priority heavy %d", lease1.RunID, highHeavy.ID)
	}
	// This run is now the highest priority in the whole queue but shares an
	// environment with lease1, so the environment fence must beat priority.
	_ = f.enqueue(t, environments[1], func(req *RunRequest) { req.Priority = 2000 })
	lease2, err := f.runs.ClaimNext(context.Background(), "worker-2", budget, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	lease3, err := f.runs.ClaimNext(context.Background(), "worker-3", budget, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if lease2.RunID != lightA.ID || lease3.RunID != lightB.ID {
		t.Fatalf("light claims = %d,%d, want %d,%d", lease2.RunID, lease3.RunID, lightA.ID, lightB.ID)
	}
	lease4, err := f.runs.ClaimNext(context.Background(), "worker-4", budget, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if lease4 != nil {
		t.Fatalf("claim exceeded host budget: %#v", lease4)
	}
	queued, err := f.runs.Run(context.Background(), firstHeavy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if queued.State != RunQueued {
		t.Fatalf("unclaimed heavy state = %s", queued.State)
	}
}

func TestConcurrentClaimsCannotExceedHeavyBudget(t *testing.T) {
	f := newOrchestrationFixture(t)
	for i := 0; i < 8; i++ {
		environmentID := f.addEnvironment(t, fmt.Sprintf("race-%d", i), EnvironmentStaging)
		f.enqueue(t, environmentID, func(req *RunRequest) { req.SlotClass = SlotHeavy })
	}
	start := make(chan struct{})
	results := make(chan *QueueLease, 16)
	errs := make(chan error, 16)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-start
			lease, err := f.runs.ClaimNext(context.Background(), fmt.Sprintf("race-worker-%d", worker),
				QueueBudget{Heavy: 1, Light: 2}, time.Minute)
			if err != nil {
				errs <- err
				return
			}
			results <- lease
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Errorf("claim race returned error: %v", err)
	}
	claimed := 0
	for lease := range results {
		if lease != nil {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("concurrent heavy claims = %d, want 1", claimed)
	}
}

func TestCancellationAndCompletionRaceConvergesToOneFencedOutcome(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	run := f.enqueue(t, environmentID)
	lease, err := f.runs.ClaimNext(context.Background(), "race-worker", QueueBudget{}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []RunState{RunRunning, RunVerifying} {
		if _, err := f.runs.TransitionRun(context.Background(), run.ID, lease.Token, state, TransitionDetail{}); err != nil {
			t.Fatal(err)
		}
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() {
		<-start
		_, err := f.runs.RequestCancellation(context.Background(), run.ID)
		errs <- err
	}()
	go func() {
		<-start
		_, err := f.runs.TransitionRun(context.Background(), run.ID, lease.Token,
			RunActivating, TransitionDetail{})
		errs <- err
	}()
	close(start)
	first, second := <-errs, <-errs
	for _, err := range []error{first, second} {
		if err != nil && !errors.Is(err, ErrRunNotCancellable) &&
			!errors.Is(err, ErrInvalidTransition) && !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("race leaked storage error: %v", err)
		}
	}

	current, err := f.runs.Run(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	switch current.State {
	case RunCancelling:
		if err := f.runs.CancelOpenSteps(context.Background(), run.ID, lease.Token); err != nil {
			t.Fatal(err)
		}
		current, err = f.runs.TransitionRun(context.Background(), run.ID, lease.Token,
			RunCancelled, TransitionDetail{Code: "cancelled"})
	case RunActivating:
		current, err = f.runs.TransitionRun(context.Background(), run.ID, lease.Token,
			RunSucceeded, TransitionDetail{})
	default:
		t.Fatalf("race left run in %s", current.State)
	}
	if err != nil {
		t.Fatal(err)
	}
	if current.State != RunCancelled && current.State != RunSucceeded {
		t.Fatalf("race terminal state = %s", current.State)
	}
	if _, err := f.runs.ActiveLease(context.Background(), run.ID); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("terminal race retained lease: %v", err)
	}
	events, err := f.runs.EventsAfter(context.Background(), run.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for i, event := range events {
		if event.Seq != int64(i+1) {
			t.Fatalf("event sequence after race = %#v", events)
		}
	}
}

func TestAutomaticEnqueueSupersedesOnlyCoveredUnclaimedRuns(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	old := f.enqueue(t, environmentID, func(req *RunRequest) {
		req.Trigger = TriggerGitHub
		req.Metadata = mustJSON(map[string]any{"changedPaths": []string{"app/a.go"}})
	})
	newer := f.enqueue(t, environmentID, func(req *RunRequest) {
		req.Trigger = TriggerGitHub
		req.Metadata = mustJSON(map[string]any{"changedPaths": []string{"app/a.go", "app/b.go"}})
	})
	got, err := f.runs.Run(context.Background(), old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != RunSuperseded || got.SupersededBy != newer.ID {
		t.Fatalf("superseded run = %#v", got)
	}
	uncovered := f.enqueue(t, environmentID, func(req *RunRequest) {
		req.Trigger = TriggerGitHub
		req.Metadata = mustJSON(map[string]any{"changedPaths": []string{"other/c.go"}})
	})
	got, err = f.runs.Run(context.Background(), newer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != RunQueued {
		t.Fatalf("uncovered run was superseded by %d: %s", uncovered.ID, got.State)
	}
	manual := f.enqueue(t, environmentID)
	_ = f.enqueue(t, environmentID, func(req *RunRequest) {
		req.Trigger = TriggerGitHub
		req.Metadata = mustJSON(map[string]any{"changedPaths": []string{}})
	})
	got, err = f.runs.Run(context.Background(), manual.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != RunQueued {
		t.Fatalf("manual run was superseded: %s", got.State)
	}
}

func TestQueuedAndClaimedCancellationHaveDistinctCleanup(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	queued := f.enqueue(t, environmentID)
	cancelled, err := f.runs.RequestCancellation(context.Background(), queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.State != RunCancelled || cancelled.EndedAt == nil {
		t.Fatalf("queued cancellation = %#v", cancelled)
	}

	active := f.enqueue(t, environmentID)
	lease, err := f.runs.ClaimNext(context.Background(), "worker", QueueBudget{}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if lease == nil || lease.RunID != active.ID {
		t.Fatalf("active lease = %#v", lease)
	}
	cancelling, err := f.runs.RequestCancellation(context.Background(), active.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelling.State != RunCancelling {
		t.Fatalf("claimed cancellation state = %s", cancelling.State)
	}
	if _, err := f.runs.ActiveLease(context.Background(), active.ID); err != nil {
		t.Fatalf("claimed cancellation dropped cleanup lease: %v", err)
	}
	if err := f.runs.CancelOpenSteps(context.Background(), active.ID, lease.Token); err != nil {
		t.Fatal(err)
	}
	finished, err := f.runs.TransitionRun(context.Background(), active.ID, lease.Token,
		RunCancelled, TransitionDetail{Code: "cancelled", Reason: "Cancelled by admin"})
	if err != nil {
		t.Fatal(err)
	}
	if finished.State != RunCancelled {
		t.Fatalf("finished cancellation state = %s", finished.State)
	}
}

func TestRetryCreatesAJoinedIdempotentRunOnlyForFailedOrCancelledInput(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	nonterminal := f.enqueue(t, environmentID)
	if _, _, err := f.runs.Retry(context.Background(), nonterminal.ID, "admin", "not-yet"); !errors.Is(err, ErrRunNotRetryable) {
		t.Fatalf("queued retry error = %v, want ErrRunNotRetryable", err)
	}

	cancelled, err := f.runs.RequestCancellation(context.Background(), nonterminal.ID)
	if err != nil {
		t.Fatal(err)
	}
	retry, created, err := f.runs.Retry(context.Background(), cancelled.ID, "admin", "retry-cancelled")
	if err != nil {
		t.Fatal(err)
	}
	if !created || retry.RetryOfRunID != cancelled.ID || retry.State != RunQueued {
		t.Fatalf("cancelled retry = %#v, created=%v", retry, created)
	}
	replay, created, err := f.runs.Retry(context.Background(), cancelled.ID, "admin", "retry-cancelled")
	if err != nil {
		t.Fatal(err)
	}
	if created || replay.ID != retry.ID {
		t.Fatalf("idempotent retry replay = %#v, created=%v; want run %d", replay, created, retry.ID)
	}

	failedSource := f.enqueue(t, environmentID, func(req *RunRequest) {
		req.Priority = 999
	})
	lease, err := f.runs.ClaimNext(context.Background(), "retry-worker", QueueBudget{}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if lease == nil || lease.RunID != failedSource.ID {
		t.Fatalf("lease = %#v, want failed source %d", lease, failedSource.ID)
	}
	failed, err := f.runs.TransitionRun(context.Background(), failedSource.ID, lease.Token,
		RunFailed, TransitionDetail{Code: "fixture_failed", Reason: "test failure"})
	if err != nil {
		t.Fatal(err)
	}
	failedRetry, created, err := f.runs.Retry(context.Background(), failed.ID, "admin", "retry-failed")
	if err != nil {
		t.Fatal(err)
	}
	if !created || failedRetry.RetryOfRunID != failed.ID {
		t.Fatalf("failed retry = %#v, created=%v", failedRetry, created)
	}
}

func TestSubscriptionReconnectIsSequencedWithoutCommitPublishDuplicates(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	run := f.enqueue(t, environmentID, func(req *RunRequest) {
		req.Steps = []StepKey{StepResolveSource}
	})
	backlog, live, unsubscribe, err := f.runs.Subscribe(context.Background(), run.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	if len(backlog) != 3 || backlog[2].Seq != 3 {
		t.Fatalf("initial backlog = %#v", backlog)
	}
	lease, err := f.runs.ClaimNext(context.Background(), "worker", QueueBudget{}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-live:
		if event.Seq != 4 || event.Type != EventRunState {
			t.Fatalf("first live event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for claimed event")
	}
	unsubscribe()

	backlog, live, unsubscribe, err = f.runs.Subscribe(context.Background(), run.ID, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	if len(backlog) != 0 {
		t.Fatalf("reconnect replayed events: %#v", backlog)
	}
	if _, err := f.runs.AppendEvent(context.Background(), run.ID, lease.Token,
		EventInput{Type: EventHeartbeat, Data: mustJSON(map[string]any{"alive": true})}); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-live:
		if event.Seq != 5 {
			t.Fatalf("reconnected live sequence = %d, want 5", event.Seq)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reconnect event")
	}
}

func TestSlowSubscriberIsDroppedWithoutStallingAppend(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	run := f.enqueue(t, environmentID)
	lease, err := f.runs.ClaimNext(context.Background(), "worker", QueueBudget{}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, live, _, err := f.runs.Subscribe(context.Background(), run.ID, 4)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= liveEventBuffer; i++ {
		if _, err := f.runs.AppendEvent(context.Background(), run.ID, lease.Token,
			EventInput{Type: EventHeartbeat, Data: mustJSON(map[string]any{"n": i})}); err != nil {
			t.Fatal(err)
		}
	}
	count := 0
	for range live {
		count++
	}
	if count != liveEventBuffer {
		t.Fatalf("slow subscriber buffered %d events, want %d before drop", count, liveEventBuffer)
	}
}

func TestTerminalLogCompactionForcesResyncAndBoundsPayloads(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	run := f.enqueue(t, environmentID, func(req *RunRequest) {
		req.Steps = []StepKey{StepResolveSource}
	})
	lease, err := f.runs.ClaimNext(context.Background(), "worker", QueueBudget{}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	step, err := f.runs.TransitionStep(context.Background(), run.ID, StepResolveSource,
		lease.Token, StepTransition{State: StepRunning})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := f.runs.AppendLog(context.Background(), run.ID, step.ID, lease.Token,
			"stdout", fmt.Sprintf("line-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.runs.TransitionStep(context.Background(), run.ID, StepResolveSource,
		lease.Token, StepTransition{State: StepPassed}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []RunState{RunRunning, RunVerifying, RunActivating, RunSucceeded} {
		if _, err := f.runs.TransitionRun(context.Background(), run.ID, lease.Token, state,
			TransitionDetail{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.runs.CompactRunLogs(context.Background(), run.ID, 2); err != nil {
		t.Fatal(err)
	}
	var retained, tombstones int
	if err := f.store.DB.QueryRow(`
		SELECT SUM(CASE WHEN compacted = 0 THEN 1 ELSE 0 END),
		       SUM(CASE WHEN compacted = 1 THEN 1 ELSE 0 END)
		  FROM deploy_log_chunks WHERE run_id = ? AND event_type = ?`,
		run.ID, EventStepLog).Scan(&retained, &tombstones); err != nil {
		t.Fatal(err)
	}
	if retained != 2 || tombstones != 1 {
		t.Fatalf("retained logs=%d tombstones=%d, want 2/1", retained, tombstones)
	}
	events, err := f.runs.EventsAfter(context.Background(), run.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 4 || events[0].Type != EventResync {
		t.Fatalf("compacted reconnect did not begin with resync: %#v", events)
	}
	for i := 2; i < len(events); i++ {
		if events[i].Seq <= events[i-1].Seq {
			t.Fatalf("events not ascending after resync: %#v", events)
		}
	}
}

func TestSavedReadinessFailureExplainsRetainedAttempt(t *testing.T) {
	fixture := newOrchestrationFixture(t)
	evidence := `{"health":{"phase":"readiness","outcome":"failed","checks":[{"name":"HTTP readiness","required":true,"outcome":"failed","attempts":[{"code":"connection_failed","address":"http://127.0.0.1:3123/"}]}]}}`
	row := fixture.store.DB.QueryRow(`SELECT 1, 13, 'verify_readiness', 1, 'failed', 1, 60, 0, 0, ?, 'health_gate_failed', 'readiness checks failed', '{}', 0`, evidence)
	step, err := scanRunStep(row)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(step.ErrorMessage, "could not connect") || !strings.Contains(step.ErrorMessage, "127.0.0.1:3123") {
		t.Fatalf("historical failure remains opaque: %s", step.ErrorMessage)
	}
}
