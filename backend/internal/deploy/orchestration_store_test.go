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
	if !created || run.State != RunQueued || run.QueuedAt == nil || run.RunNumber != 1 {
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
	if created || replay.ID != run.ID || replay.RunNumber != run.RunNumber {
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

// A routine scheduled action queued behind a real push must not cancel it: a
// schedule's changedPaths are empty (unscoped, not "nothing changed"), and
// coversPaths only supersedes an older run whose own changed paths are a
// subset of the newer one's — an empty newer set covers nothing but another
// empty set. The reverse direction still works: a push queued behind a stale
// schedule does supersede it, since any set covers the empty one.
func TestScheduleQueuedBehindAPushDoesNotSupersedeIt(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	push := f.enqueue(t, environmentID, func(req *RunRequest) {
		req.Trigger = TriggerGitHub
		req.Metadata = mustJSON(map[string]any{"changedPaths": []string{"app/main.go"}})
	})
	f.enqueue(t, environmentID, func(req *RunRequest) {
		req.Trigger = TriggerSchedule
		req.Metadata = mustJSON(map[string]any{"changedPaths": []string{}})
	})
	got, err := f.runs.Run(context.Background(), push.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != RunQueued {
		t.Fatalf("push was superseded by an unscoped schedule queued behind it: %s", got.State)
	}

	stale := f.enqueue(t, environmentID, func(req *RunRequest) {
		req.Trigger = TriggerSchedule
		req.Metadata = mustJSON(map[string]any{"changedPaths": []string{}})
	})
	later := f.enqueue(t, environmentID, func(req *RunRequest) {
		req.Trigger = TriggerGitHub
		req.Metadata = mustJSON(map[string]any{"changedPaths": []string{"app/main.go"}})
	})
	got, err = f.runs.Run(context.Background(), stale.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != RunSuperseded || got.SupersededBy != later.ID {
		t.Fatalf("stale schedule survived a push queued behind it: %#v", got)
	}
}

// ProjectRunsFiltered must keep the byte-compatible default (no filters, no
// cursor) while adding environment/operation/state filtering and before-id
// pagination with an honest nextBefore cursor.
func TestProjectRunsFilteredNarrowsAndPaginates(t *testing.T) {
	f := newOrchestrationFixture(t)
	production := f.addEnvironment(t, "production", EnvironmentProduction)
	staging := f.addEnvironment(t, "staging", EnvironmentStaging)
	var deploys []*EngineRun
	for i := 0; i < 3; i++ {
		deploys = append(deploys, f.enqueue(t, production, func(req *RunRequest) {
			req.Operation = OperationDeploy
		}))
	}
	restart := f.enqueue(t, production, func(req *RunRequest) { req.Operation = OperationRestart })
	stagingRun := f.enqueue(t, staging, func(req *RunRequest) { req.Operation = OperationDeploy })

	// The byte-compatible default: no filters, same rows ProjectRuns returns.
	def, nextBefore, err := f.runs.ProjectRunsFiltered(context.Background(), f.projectID, RunListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(def) != 5 || nextBefore != 0 {
		t.Fatalf("unfiltered runs = %d (next=%d), want all 5 rows and no cursor", len(def), nextBefore)
	}
	plain, err := f.runs.ProjectRuns(context.Background(), f.projectID, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != len(def) {
		t.Fatalf("ProjectRunsFiltered default diverged from ProjectRuns: %d vs %d", len(def), len(plain))
	}
	for i := range plain {
		if plain[i].ID != def[i].ID {
			t.Fatalf("row %d = %d, want %d (same order as ProjectRuns)", i, def[i].ID, plain[i].ID)
		}
	}

	// environment narrows to just that environment's run.
	byEnvironment, _, err := f.runs.ProjectRunsFiltered(context.Background(), f.projectID, RunListFilter{Environment: staging})
	if err != nil {
		t.Fatal(err)
	}
	if len(byEnvironment) != 1 || byEnvironment[0].ID != stagingRun.ID {
		t.Fatalf("environment filter = %#v, want only %d", byEnvironment, stagingRun.ID)
	}

	// operation narrows to the restart.
	byOperation, _, err := f.runs.ProjectRunsFiltered(context.Background(), f.projectID, RunListFilter{Operation: OperationRestart})
	if err != nil {
		t.Fatal(err)
	}
	if len(byOperation) != 1 || byOperation[0].ID != restart.ID {
		t.Fatalf("operation filter = %#v, want only %d", byOperation, restart.ID)
	}

	// state accepts a literal state, and the "active"/"terminal" keywords.
	lease, err := f.runs.ClaimNext(context.Background(), "worker", QueueBudget{Heavy: 1, Light: 1}, time.Minute)
	if err != nil || lease == nil {
		t.Fatalf("claim = %#v, %v", lease, err)
	}
	if _, err := f.runs.TransitionRun(context.Background(), lease.RunID, lease.Token,
		RunFailed, TransitionDetail{Code: "fixture_failed", Reason: "test"}); err != nil {
		t.Fatal(err)
	}
	active, _, err := f.runs.ProjectRunsFiltered(context.Background(), f.projectID, RunListFilter{State: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != len(def)-1 {
		t.Fatalf("active state filter = %d rows, want %d still non-terminal", len(active), len(def)-1)
	}
	terminal, _, err := f.runs.ProjectRunsFiltered(context.Background(), f.projectID, RunListFilter{State: "terminal"})
	if err != nil {
		t.Fatal(err)
	}
	if len(terminal) != 1 || terminal[0].ID != lease.RunID {
		t.Fatalf("terminal state filter = %#v, want exactly the failed run", terminal)
	}
	failed, _, err := f.runs.ProjectRunsFiltered(context.Background(), f.projectID, RunListFilter{State: "failed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(failed) != 1 || failed[0].ID != lease.RunID {
		t.Fatalf("literal-state filter = %#v, want exactly the failed run", failed)
	}

	// before/limit paginate newest-first with an honest cursor.
	page, next, err := f.runs.ProjectRunsFiltered(context.Background(), f.projectID, RunListFilter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || next != page[1].ID {
		t.Fatalf("first page = %#v (next=%d), want 2 rows and nextBefore = the second row's id", page, next)
	}
	rest, next, err := f.runs.ProjectRunsFiltered(context.Background(), f.projectID, RunListFilter{Limit: 2, Before: next})
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 2 || rest[0].ID == page[0].ID || rest[0].ID == page[1].ID {
		t.Fatalf("second page = %#v, want the next 2 distinct rows", rest)
	}
	last, next, err := f.runs.ProjectRunsFiltered(context.Background(), f.projectID, RunListFilter{Limit: 2, Before: rest[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(last) != 1 || next != 0 {
		t.Fatalf("final page = %#v (next=%d), want the last row and no further cursor", last, next)
	}

	if _, _, err := f.runs.ProjectRunsFiltered(context.Background(), f.projectID, RunListFilter{State: "not-a-state"}); err != nil {
		t.Fatalf("an unrecognised literal state should filter to nothing, not error: %v", err)
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
	if !created || retry.RetryOfRunID != cancelled.ID || retry.State != RunQueued || retry.RunNumber != 2 {
		t.Fatalf("cancelled retry = %#v, created=%v", retry, created)
	}
	replay, created, err := f.runs.Retry(context.Background(), cancelled.ID, "admin", "retry-cancelled")
	if err != nil {
		t.Fatal(err)
	}
	if created || replay.ID != retry.ID || replay.RunNumber != retry.RunNumber {
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

func TestSavedReadinessFailureKeepsTheDiagnosedCause(t *testing.T) {
	fixture := newOrchestrationFixture(t)
	evidence := `{"health":{"phase":"readiness","outcome":"failed","checks":[{"name":"HTTP readiness","required":true,"outcome":"failed","attempts":[{"code":"unexpected_status","statusCode":500,"address":"http://127.0.0.1:3123/"}]}]},"diagnostics":{"available":true,"lines":200,"cause":{"code":"schema_missing","table":"public.products"}}}`
	row := fixture.store.DB.QueryRow(`SELECT 1, 13, 'verify_readiness', 1, 'failed', 1, 60, 0, 0, ?, 'health_gate_failed', 'readiness checks failed', '{}', 0`, evidence)
	step, err := scanRunStep(row)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"HTTP 500", "table public.products does not exist", "last output is in the build log"} {
		if !strings.Contains(step.ErrorMessage, want) {
			t.Fatalf("saved failure lacks %q: %s", want, step.ErrorMessage)
		}
	}
}

func TestMergeRunMetadataMergesWithoutClobberingEnqueuedKeys(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	run := f.enqueue(t, environmentID, func(req *RunRequest) {
		req.Metadata = json.RawMessage(`{"targetReleaseId":5,"compatibility":false}`)
	})

	commit := map[string]any{
		"sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "subject": "Fix checkout flow",
		"author": "Dev", "authoredAt": "2026-01-01T00:00:00Z",
	}
	updated, err := f.runs.MergeRunMetadata(context.Background(), run.ID, map[string]any{"commit": commit})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		TargetReleaseID int64          `json:"targetReleaseId"`
		Compatibility   bool           `json:"compatibility"`
		Commit          map[string]any `json:"commit"`
	}
	if err := json.Unmarshal(updated.Metadata, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.TargetReleaseID != 5 || decoded.Commit["sha"] != commit["sha"] {
		t.Fatalf("merged metadata = %s", updated.Metadata)
	}
	reloaded, err := f.runs.Run(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(reloaded.Metadata) != string(updated.Metadata) {
		t.Fatalf("persisted metadata = %s, want %s", reloaded.Metadata, updated.Metadata)
	}

	// A second merge overwrites only the key it names; targetReleaseId and
	// compatibility, which the run was enqueued with, survive untouched.
	second, err := f.runs.MergeRunMetadata(context.Background(), run.ID,
		map[string]any{"commit": map[string]any{"sha": "b", "subject": "Second pass"}})
	if err != nil {
		t.Fatal(err)
	}
	decoded = struct {
		TargetReleaseID int64          `json:"targetReleaseId"`
		Compatibility   bool           `json:"compatibility"`
		Commit          map[string]any `json:"commit"`
	}{}
	if err := json.Unmarshal(second.Metadata, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.TargetReleaseID != 5 || decoded.Commit["sha"] != "b" || decoded.Commit["subject"] != "Second pass" {
		t.Fatalf("re-merged metadata = %s", second.Metadata)
	}
}

func TestMergeRunMetadataOnUnknownRunFailsWithRunNotFound(t *testing.T) {
	f := newOrchestrationFixture(t)
	if _, err := f.runs.MergeRunMetadata(context.Background(), 9999, map[string]any{"commit": "x"}); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("merge on unknown run = %v, want ErrRunNotFound", err)
	}
}

// A retry copies the run row it retries verbatim, so metadata this store
// merged into the original run after enqueue — a commit summary read only
// once acquire_source materializes the source — still reaches the retry.
func TestRetryCarriesForwardMergedRunMetadata(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	run := f.enqueue(t, environmentID)
	if _, err := f.runs.MergeRunMetadata(context.Background(), run.ID,
		map[string]any{"commit": map[string]any{"sha": "deadbeef", "subject": "Ship it"}}); err != nil {
		t.Fatal(err)
	}
	lease, err := f.runs.ClaimNext(context.Background(), "retry-metadata-worker", QueueBudget{}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if lease == nil || lease.RunID != run.ID {
		t.Fatalf("lease = %#v, want run %d", lease, run.ID)
	}
	failed, err := f.runs.TransitionRun(context.Background(), run.ID, lease.Token,
		RunFailed, TransitionDetail{Code: "fixture_failed", Reason: "test failure"})
	if err != nil {
		t.Fatal(err)
	}
	retry, created, err := f.runs.Retry(context.Background(), failed.ID, "admin", "retry-carries-metadata")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("retry unexpectedly joined an existing run")
	}
	var decoded struct {
		Commit map[string]any `json:"commit"`
	}
	if err := json.Unmarshal(retry.Metadata, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Commit["sha"] != "deadbeef" {
		t.Fatalf("retried run metadata = %s, want the original commit carried forward", retry.Metadata)
	}
}
