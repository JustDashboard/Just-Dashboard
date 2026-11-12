package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/safego"
)

var errOperatorCancellation = errors.New("deployment cancellation requested by operator")

type StepExecution struct {
	Run        EngineRun
	Step       RunStep
	ClaimToken string
	Output     StepOutput
}

type StepOutput interface {
	Log(stream, text string) error
	Event(EventInput) error
}

type StepResult struct {
	State        StepState
	Evidence     json.RawMessage
	Cleanup      json.RawMessage
	ErrorCode    string
	ErrorMessage string
	FromCommit   string
	ToCommit     string
	// Recovered is true only when an activation adapter restored and verified
	// the exact prior runtime/route before returning a failure.
	Recovered bool
}

// StepExecutor is the orchestration boundary. Implementations delegate to the
// feature package that owns Git, Docker, proxy, backups or checks; the engine
// owns only ordering, persistence, cancellation and recovery.
type StepExecutor interface {
	Execute(context.Context, StepExecution) StepResult
}

// RunCancellationCleaner is implemented by executors that own live side
// effects which may exist between two persisted steps. Cleanup runs while the
// lease is still held and receives a non-cancelled, executor-bounded context.
type RunCancellationCleaner interface {
	CleanupCancelledRun(context.Context, EngineRun, string) (json.RawMessage, error)
}

type ReconcileAction string

const (
	ReconcileResume   ReconcileAction = "resume"
	ReconcileComplete ReconcileAction = "complete"
	ReconcileFail     ReconcileAction = "fail"
)

type ReconcileRequest struct {
	Run   EngineRun
	Step  *RunStep
	Lease QueueLease
}

type ReconcileResult struct {
	Action       ReconcileAction
	StepResult   StepResult
	TerminalCode string
	Reason       string
}

// StepReconciler decides from persisted and owning-feature evidence; it must
// never answer resume for an unknown non-idempotent side effect.
type StepReconciler interface {
	Reconcile(context.Context, ReconcileRequest) ReconcileResult
}

type EngineConfig struct {
	WorkerID    string
	Budget      QueueBudget
	LeaseTTL    time.Duration
	PollEvery   time.Duration
	HotLogLimit int
}

func (c EngineConfig) normalized() EngineConfig {
	if c.WorkerID == "" {
		hostname, err := os.Hostname()
		if err != nil || hostname == "" {
			hostname = "localhost"
		}
		c.WorkerID = fmt.Sprintf("%s:%d", hostname, os.Getpid())
	}
	c.Budget = c.Budget.normalized()
	if c.LeaseTTL <= 0 {
		c.LeaseTTL = defaultLeaseTTL
	}
	if c.PollEvery <= 0 {
		c.PollEvery = 250 * time.Millisecond
	}
	if c.HotLogLimit <= 0 {
		c.HotLogLimit = 10_000
	}
	return c
}

type Engine struct {
	store      *OrchestrationStore
	executor   StepExecutor
	reconciler StepReconciler
	config     EngineConfig
	log        *slog.Logger

	mu      sync.Mutex
	started bool
	stop    context.CancelFunc
	wake    chan struct{}
	running map[int64]context.CancelCauseFunc
	// waiting are the runs inside a resumable wait, which Shutdown ends
	// rather than waits out.
	waiting  map[int64]bool
	workerWG sync.WaitGroup
	loopWG   sync.WaitGroup

	observers  RunObservers
	observerWG sync.WaitGroup
}

func NewEngine(
	store *OrchestrationStore,
	executor StepExecutor,
	reconciler StepReconciler,
	config EngineConfig,
	log *slog.Logger,
) *Engine {
	if executor == nil {
		executor = unavailableExecutor{}
	}
	if reconciler == nil {
		reconciler = conservativeReconciler{}
	}
	return &Engine{
		store: store, executor: executor, reconciler: reconciler,
		config: config.normalized(), log: log,
		wake: make(chan struct{}, 1), running: make(map[int64]context.CancelCauseFunc),
		waiting: map[int64]bool{},
	}
}

// errEngineStopping is how Shutdown ends a step that only waits — a
// canary's window — instead of waiting out the rest of it. The step is left
// running and the lease given up at once, so the next process reclaims the
// run straight away and the reconciler resumes the step where it was.
var errEngineStopping = errors.New("the deployment engine is stopping")

// resumableWaits are the steps that hold a run without doing work the
// engine would have to see finished, and that the reconciler resumes after a
// restart: a canary holds its window, and its state is rows and a route.
var resumableWaits = map[StepKey]bool{StepCanary: true}

// Start reconciles every expired claim before the scheduler is allowed to
// claim fresh work. Startup either establishes a recovery decision for old
// side effects or returns an error; it never silently leaves them competing
// with new work.
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return nil
	}
	loopCtx, stop := context.WithCancel(ctx)
	e.stop = stop
	e.started = true
	e.mu.Unlock()

	if err := e.reconcileExpired(loopCtx); err != nil {
		stop()
		e.mu.Lock()
		e.started = false
		e.stop = nil
		e.mu.Unlock()
		return err
	}
	e.loopWG.Add(1)
	safego.Go(loopCtx, e.log, "deployment engine schedule", e.schedule, e.loopWG.Done)
	return nil
}

// Shutdown stops new claims and waits for currently executing adapters. It
// does not cancel them: activation and restoration must reach a converged
// state. A user cancellation goes through Cancel and remains explicit. A run
// inside a resumable wait is the exception: it is stopped where it is, for
// the next process to resume.
func (e *Engine) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	stop := e.stop
	e.stop = nil
	e.started = false
	for runID := range e.waiting {
		if cancel := e.running[runID]; cancel != nil {
			cancel(errEngineStopping)
		}
	}
	e.mu.Unlock()
	if stop != nil {
		stop()
	}
	done := make(chan struct{})
	go func() {
		e.loopWG.Wait()
		e.workerWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return e.waitObservers(ctx)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Engine) Notify() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *Engine) Cancel(ctx context.Context, runID int64) (*EngineRun, error) {
	run, err := e.store.RequestCancellation(ctx, runID)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	cancel := e.running[runID]
	e.mu.Unlock()
	if cancel != nil && run.State == RunCancelling {
		cancel(errOperatorCancellation)
	}
	// A run cancelled before any worker claimed it ends here, with no worker
	// goroutine to announce it.
	if run.State.Terminal() {
		e.observeFinished(run.ID)
	}
	e.Notify()
	return run, nil
}

func (e *Engine) schedule(ctx context.Context) {
	ticker := time.NewTicker(e.config.PollEvery)
	defer ticker.Stop()
	for {
		// A worker that dies mid-run stops heartbeating but leaves the run
		// `running`. Reconciling only at startup meant the run stayed that way,
		// with no transcript and no terminal state, until the process was
		// restarted; a live worker keeps its own lease fresh, so sweeping here
		// reclaims exactly the abandoned ones.
		if err := e.reconcileExpired(ctx); err != nil && !errors.Is(err, context.Canceled) && e.log != nil {
			e.log.Error("deployment lease recovery failed", "err", err)
		}
		if err := e.claimAvailable(ctx); err != nil && !errors.Is(err, context.Canceled) && e.log != nil {
			e.log.Error("deployment queue claim failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-e.wake:
		}
	}
}

func (e *Engine) claimAvailable(ctx context.Context) error {
	for {
		lease, err := e.store.ClaimNext(ctx, e.config.WorkerID, e.config.Budget, e.config.LeaseTTL)
		if err != nil {
			return err
		}
		if lease == nil {
			return nil
		}
		e.startWorker(*lease, nil)
	}
}

func (e *Engine) startWorker(lease QueueLease, prepared func(context.Context, QueueLease) error) {
	workerCtx, cancel := context.WithCancelCause(context.Background())
	e.mu.Lock()
	if prior := e.running[lease.RunID]; prior != nil {
		e.mu.Unlock()
		cancel(context.Canceled)
		return
	}
	e.running[lease.RunID] = cancel
	e.mu.Unlock()
	e.workerWG.Add(1)
	go func() {
		// A worker that panics ends as one that died: its lease stops being
		// renewed and the schedule's sweep reclaims the run. It is not the
		// end of the dashboard and of every other run with it.
		defer safego.Recover(e.log, "deployment worker")
		defer e.workerWG.Done()
		defer func() {
			cancel(context.Canceled)
			e.mu.Lock()
			delete(e.running, lease.RunID)
			e.mu.Unlock()
			// Every path out of a worker — success, failure, cancellation or
			// a reconciliation decision — passes here, so this is the one
			// place a terminal run is announced.
			e.observeFinished(lease.RunID)
			e.Notify()
		}()
		heartbeatDone := make(chan struct{})
		go e.heartbeat(workerCtx, lease, cancel, heartbeatDone)
		if prepared == nil {
			e.observeStarted(lease.RunID)
		}
		if prepared != nil {
			if err := prepared(workerCtx, lease); err != nil {
				e.logFailure(lease.RunID, err)
				cancel(err)
				<-heartbeatDone
				return
			}
		}
		if err := e.execute(workerCtx, lease); err != nil &&
			!errors.Is(err, context.Canceled) && !errors.Is(err, ErrLeaseLost) {
			e.logFailure(lease.RunID, err)
		}
		cancel(context.Canceled)
		<-heartbeatDone
	}()
}

func (e *Engine) heartbeat(
	ctx context.Context,
	lease QueueLease,
	cancel context.CancelCauseFunc,
	done chan<- struct{},
) {
	defer safego.Recover(e.log, "deployment lease heartbeat")
	defer close(done)
	interval := e.config.LeaseTTL / 3
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := e.store.Heartbeat(context.Background(), lease.RunID, lease.Token, e.config.LeaseTTL); err != nil {
				cancel(ErrLeaseLost)
				return
			}
		}
	}
}

func (e *Engine) execute(ctx context.Context, lease QueueLease) error {
	snapshot, err := e.store.Snapshot(ctx, lease.RunID)
	if err != nil {
		return err
	}
	if err := RequireSupportedProject(ctx, e.store.db, snapshot.Run.ProjectID); err != nil {
		return e.failRun(ctx, lease, "import_removed", err.Error(), false)
	}
	latest := latestAttempts(snapshot.Steps)
	for _, key := range orderedKeys(snapshot.Steps) {
		run, err := e.store.Run(ctx, lease.RunID)
		if err != nil {
			return err
		}
		if run.State.Terminal() {
			return nil
		}
		if run.State == RunCancelling || run.CancelRequested || ctx.Err() != nil {
			return e.finishCancellation(lease)
		}
		if err := e.advanceRunForStep(ctx, lease, run.State, key); err != nil {
			return err
		}
		step := latest[key]
		if stepAttemptTerminal(step.State) {
			continue
		}
		if step.State == StepRunning {
			return fmt.Errorf("run %d step %s is still running without reconciliation", lease.RunID, key)
		}
		stepPtr, err := e.store.TransitionStep(ctx, lease.RunID, key, lease.Token,
			StepTransition{State: StepRunning})
		if err != nil {
			return err
		}
		run, err = e.store.Run(ctx, lease.RunID)
		if err != nil {
			return err
		}
		output := newStoreStepOutput(ctx, e.store, lease.RunID, stepPtr.ID, lease.Token)
		if resumableWaits[key] {
			e.mu.Lock()
			e.waiting[lease.RunID] = true
			e.mu.Unlock()
		}
		result := e.executor.Execute(ctx, StepExecution{
			Run: *run, Step: *stepPtr, ClaimToken: lease.Token, Output: output,
		})
		e.mu.Lock()
		delete(e.waiting, lease.RunID)
		e.mu.Unlock()
		// What the step printed is in the transcript before anything records
		// how the step ended, so its last lines precede its own closing event.
		_ = output.Flush()
		if ctx.Err() != nil {
			if errors.Is(context.Cause(ctx), ErrLeaseLost) {
				return ErrLeaseLost
			}
			if errors.Is(context.Cause(ctx), errEngineStopping) {
				return e.store.surrenderLease(context.Background(), lease.RunID, lease.Token)
			}
			if result.State != StepCancelled {
				result.State = StepCancelled
			}
			if len(result.Cleanup) == 0 {
				result.Cleanup = mustJSON(map[string]any{
					"context": "cancelled", "cause": context.Cause(ctx).Error(),
				})
			}
			if _, err := e.store.TransitionStep(context.Background(), lease.RunID, key, lease.Token, StepTransition{
				State: StepCancelled, Cleanup: result.Cleanup,
				ErrorCode: result.ErrorCode, ErrorMessage: result.ErrorMessage,
			}); err != nil {
				return err
			}
			return e.finishCancellation(lease)
		}
		if err := output.failure(); err != nil {
			result = StepResult{
				State: StepFailed, ErrorCode: "event_persist_failed",
				ErrorMessage: err.Error(),
			}
		}
		if result.State == "" {
			result.State = StepPassed
		}
		if !allowedExecutorResult(result.State) {
			result = StepResult{
				State: StepFailed, ErrorCode: "internal_error",
				ErrorMessage: fmt.Sprintf("adapter returned invalid terminal state %q", result.State),
			}
		}
		if result.FromCommit != "" || result.ToCommit != "" {
			if err := e.store.SetRunCommits(ctx, lease.RunID, lease.Token,
				result.FromCommit, result.ToCommit); err != nil {
				return err
			}
		}
		updated, err := e.store.TransitionStep(ctx, lease.RunID, key, lease.Token, StepTransition{
			State: result.State, Evidence: result.Evidence, Cleanup: result.Cleanup,
			ErrorCode: result.ErrorCode, ErrorMessage: result.ErrorMessage,
		})
		if err != nil {
			return err
		}
		latest[key] = *updated
		if result.State == StepFailed || result.State == StepUnavailable {
			return e.failRun(ctx, lease, result.ErrorCode, result.ErrorMessage, result.Recovered)
		}
		if result.State == StepCancelled {
			return e.finishCancellation(lease)
		}
	}
	if err := e.finishSuccess(ctx, lease); err != nil {
		return err
	}
	return e.store.CompactRunLogs(context.Background(), lease.RunID, e.config.HotLogLimit)
}

// A step's output is written to the transcript in batches: when this many
// lines are waiting, when they weigh this much, or this long after the first
// of them, whichever comes first. The delay is short enough that a page
// following the run still reads as live.
const (
	stepOutputBatchLines = 64
	stepOutputBatchBytes = 256 << 10
	stepOutputBatchDelay = 100 * time.Millisecond
)

// storeStepOutput is a running step's way into the transcript. Log collects
// lines and writes them a batch at a time; Event and Flush write what is
// waiting first, so nothing reaches the transcript ahead of a line that was
// printed before it.
//
// The zero batch size writes every line through as it arrives, which is what
// a test that builds one by hand and then reads the table gets.
type storeStepOutput struct {
	ctx        context.Context
	store      *OrchestrationStore
	runID      int64
	stepID     int64
	claimToken string
	batchLines int
	batchDelay time.Duration

	mu           sync.Mutex
	err          error
	pending      []LogLine
	pendingBytes int
	timer        *time.Timer
	// logged counts the lines Log has taken. A line's mark is its count, and
	// spans says which sequence each written line received, so a step can
	// still point at the one line of its output that proves why it failed
	// although the line has no sequence until its batch is written.
	logged int64
	spans  []outputSpan
}

// outputSpan is a run of lines written together: marks first..first+count-1
// received sequences seq..seq+count-1.
type outputSpan struct {
	first, seq, count int64
}

func newStoreStepOutput(ctx context.Context, store *OrchestrationStore, runID, stepID int64, claimToken string) *storeStepOutput {
	return &storeStepOutput{
		ctx: ctx, store: store, runID: runID, stepID: stepID, claimToken: claimToken,
		batchLines: stepOutputBatchLines, batchDelay: stepOutputBatchDelay,
	}
}

// LastMark names the line Log last took. SeqOf turns it into the line's
// sequence in the transcript.
func (o *storeStepOutput) LastMark() int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.logged
}

// SeqOf is the transcript sequence of the line a mark names, writing what is
// still waiting if that line is among it. It is 0 for a line that was never
// written.
func (o *storeStepOutput) SeqOf(mark int64) int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	if mark <= 0 || mark > o.logged {
		return 0
	}
	o.flushLocked()
	// Newest first: a failure is named from the end of the output.
	for index := len(o.spans) - 1; index >= 0; index-- {
		span := o.spans[index]
		if mark >= span.first && mark < span.first+span.count {
			return span.seq + (mark - span.first)
		}
	}
	return 0
}

func (o *storeStepOutput) Log(stream, text string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.err != nil {
		return o.err
	}
	o.logged++
	o.pending = append(o.pending, LogLine{Stream: stream, Text: text})
	o.pendingBytes += len(text)
	if len(o.pending) >= o.batchLines || o.pendingBytes >= stepOutputBatchBytes {
		o.flushLocked()
		return o.err
	}
	if o.timer == nil {
		o.timer = time.AfterFunc(o.batchDelay, func() { _ = o.Flush() })
	}
	return nil
}

// Flush writes the lines that are waiting. The engine calls it when the step
// returns; a line that could not be written fails the step, as it always did.
func (o *storeStepOutput) Flush() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.flushLocked()
	return o.err
}

func (o *storeStepOutput) flushLocked() {
	if o.timer != nil {
		o.timer.Stop()
		o.timer = nil
	}
	if len(o.pending) == 0 || o.err != nil {
		o.pending, o.pendingBytes = nil, 0
		return
	}
	lines := o.pending
	o.pending, o.pendingBytes = nil, 0
	events, err := o.store.AppendLogs(o.writeContext(), o.runID, o.stepID, o.claimToken, lines)
	if err != nil {
		o.err = err
		return
	}
	first := o.logged - int64(len(lines)) + 1
	seq := events[0].Seq
	if last := len(o.spans) - 1; last >= 0 &&
		o.spans[last].first+o.spans[last].count == first && o.spans[last].seq+o.spans[last].count == seq {
		o.spans[last].count += int64(len(lines))
		return
	}
	o.spans = append(o.spans, outputSpan{first: first, seq: seq, count: int64(len(lines))})
}

// writeContext outlives a cancelled step: what it printed on its way out is
// still its transcript.
func (o *storeStepOutput) writeContext() context.Context {
	if o.ctx.Err() != nil {
		return context.WithoutCancel(o.ctx)
	}
	return o.ctx
}

func (o *storeStepOutput) Event(input EventInput) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.flushLocked()
	if o.err != nil {
		return o.err
	}
	input.StepID = o.stepID
	_, o.err = o.store.AppendEvent(o.writeContext(), o.runID, o.claimToken, input)
	return o.err
}

// failure is the first line or event the transcript refused.
func (o *storeStepOutput) failure() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.err
}

func orderedKeys(steps []RunStep) []StepKey {
	seen := make(map[StepKey]bool, len(steps))
	result := make([]StepKey, 0, len(steps))
	for _, step := range steps {
		if !seen[step.Key] {
			seen[step.Key] = true
			result = append(result, step.Key)
		}
	}
	return result
}

func latestAttempts(steps []RunStep) map[StepKey]RunStep {
	result := make(map[StepKey]RunStep, len(steps))
	for _, step := range steps {
		if prior, ok := result[step.Key]; !ok || step.Attempt > prior.Attempt {
			result[step.Key] = step
		}
	}
	return result
}

func allowedExecutorResult(state StepState) bool {
	switch state {
	case StepPassed, StepWarning, StepFailed, StepSkipped, StepCancelled, StepUnavailable:
		return true
	default:
		return false
	}
}

func (e *Engine) advanceRunForStep(
	ctx context.Context,
	lease QueueLease,
	state RunState,
	key StepKey,
) error {
	target := state
	switch key {
	case StepBuildArtifact:
		if state == RunPreparing {
			target = RunRunning
		}
	case StepVerifyReadiness:
		if state == RunPreparing {
			if _, err := e.store.TransitionRun(ctx, lease.RunID, lease.Token, RunRunning, TransitionDetail{}); err != nil {
				return err
			}
			state = RunRunning
		}
		if state == RunRunning {
			target = RunVerifying
		}
	case StepActivate:
		if state == RunPreparing {
			if _, err := e.store.TransitionRun(ctx, lease.RunID, lease.Token, RunRunning, TransitionDetail{}); err != nil {
				return err
			}
			state = RunRunning
		}
		if state == RunRunning {
			if _, err := e.store.TransitionRun(ctx, lease.RunID, lease.Token, RunVerifying, TransitionDetail{}); err != nil {
				return err
			}
			state = RunVerifying
		}
		if state == RunVerifying {
			target = RunActivating
		}
	}
	if target != state {
		_, err := e.store.TransitionRun(ctx, lease.RunID, lease.Token, target, TransitionDetail{})
		return err
	}
	return nil
}

func (e *Engine) finishSuccess(ctx context.Context, lease QueueLease) error {
	run, err := e.store.Run(ctx, lease.RunID)
	if err != nil {
		return err
	}
	for _, next := range []RunState{RunRunning, RunVerifying, RunActivating, RunSucceeded} {
		if run.State == next {
			continue
		}
		if !CanTransitionRun(run.State, next) {
			continue
		}
		run, err = e.store.TransitionRun(ctx, lease.RunID, lease.Token, next, TransitionDetail{})
		if err != nil {
			return err
		}
	}
	if !run.State.Terminal() {
		return fmt.Errorf("run %d could not converge from %s to success", run.ID, run.State)
	}
	return nil
}

func (e *Engine) failRun(ctx context.Context, lease QueueLease, code, reason string, recovered bool) error {
	run, err := e.store.Run(ctx, lease.RunID)
	if err != nil {
		return err
	}
	if run.State == RunActivating {
		run, err = e.store.TransitionRun(ctx, lease.RunID, lease.Token,
			RunFailedActivation, TransitionDetail{Code: code, Reason: reason})
		if err != nil {
			return err
		}
	}
	if run.State == RunFailedActivation {
		if recovered {
			run, err = e.store.TransitionRun(ctx, lease.RunID, lease.Token,
				RunRestoringPrevious, TransitionDetail{Code: code, Reason: reason})
			if err != nil {
				return err
			}
			_, err = e.store.TransitionRun(ctx, lease.RunID, lease.Token,
				RunRolledBack, TransitionDetail{Code: code, Reason: reason})
			return err
		}
		_, err = e.store.TransitionRun(ctx, lease.RunID, lease.Token,
			RunFailed, TransitionDetail{Code: code, Reason: reason})
		return err
	}
	_, err = e.store.TransitionRun(ctx, lease.RunID, lease.Token,
		RunFailed, TransitionDetail{Code: code, Reason: reason})
	return err
}

func (e *Engine) finishCancellation(lease QueueLease) error {
	ctx := context.Background()
	run, err := e.store.Run(ctx, lease.RunID)
	if err != nil {
		return err
	}
	if run.State.Terminal() {
		return nil
	}
	if run.State != RunCancelling {
		if _, err := e.store.RequestCancellation(ctx, lease.RunID); err != nil {
			return err
		}
	}
	if cleaner, ok := e.executor.(RunCancellationCleaner); ok {
		cleanup, cleanupErr := cleaner.CleanupCancelledRun(ctx, *run, lease.Token)
		if len(cleanup) == 0 {
			cleanup = mustJSON(map[string]any{"completed": cleanupErr == nil})
		}
		if cleanupErr != nil {
			var decoded map[string]any
			if json.Unmarshal(cleanup, &decoded) != nil {
				decoded = map[string]any{}
			}
			decoded["completed"] = false
			decoded["error"] = "cancellation cleanup incomplete"
			cleanup = mustJSON(decoded)
		}
		if _, appendErr := e.store.AppendEvent(ctx, lease.RunID, lease.Token, EventInput{
			Type: EventStepEvidence, Data: mustJSON(map[string]any{
				"phase": "cancellation_cleanup", "cleanup": json.RawMessage(cleanup),
			}),
		}); appendErr != nil {
			return appendErr
		}
	}
	if err := e.store.CancelOpenSteps(ctx, lease.RunID, lease.Token); err != nil {
		return err
	}
	_, err = e.store.TransitionRun(ctx, lease.RunID, lease.Token, RunCancelled,
		TransitionDetail{Code: "cancelled", Reason: "Cancelled by operator"})
	return err
}

func (e *Engine) reconcileExpired(ctx context.Context) error {
	expired, err := e.store.ExpiredLeases(ctx)
	if err != nil {
		return err
	}
	for _, stale := range expired {
		lease, err := e.store.ReclaimExpiredLease(ctx, stale, e.config.WorkerID, e.config.LeaseTTL)
		if errors.Is(err, ErrLeaseLost) {
			continue
		}
		if err != nil {
			return err
		}
		snapshot, err := e.store.Snapshot(ctx, lease.RunID)
		if err != nil {
			return err
		}
		var active *RunStep
		for _, step := range snapshot.Steps {
			if step.State == StepRunning && (active == nil || step.Ordinal > active.Ordinal || step.Attempt > active.Attempt) {
				copy := step
				active = &copy
			}
		}
		decision := e.reconciler.Reconcile(ctx, ReconcileRequest{
			Run: snapshot.Run, Step: active, Lease: *lease,
		})
		prepared := e.reconcilePreparation(active, decision)
		e.startWorker(*lease, prepared)
	}
	return nil
}

func (e *Engine) reconcilePreparation(
	active *RunStep,
	decision ReconcileResult,
) func(context.Context, QueueLease) error {
	return func(ctx context.Context, lease QueueLease) error {
		if active == nil {
			if decision.Action == ReconcileFail {
				return e.failRun(ctx, lease, decision.TerminalCode, decision.Reason, decision.StepResult.Recovered)
			}
			return nil
		}
		switch decision.Action {
		case ReconcileComplete:
			result := decision.StepResult
			if result.State == "" {
				result.State = StepPassed
			}
			_, err := e.store.TransitionStep(ctx, lease.RunID, active.Key, lease.Token,
				StepTransition{
					State: result.State, Evidence: result.Evidence, Cleanup: result.Cleanup,
					ErrorCode: result.ErrorCode, ErrorMessage: result.ErrorMessage,
				})
			return err
		case ReconcileResume:
			if _, err := e.store.TransitionStep(ctx, lease.RunID, active.Key, lease.Token,
				StepTransition{State: StepFailed, ErrorCode: "interrupted", ErrorMessage: "Backend restarted during step"}); err != nil {
				return err
			}
			_, err := e.store.TransitionStep(ctx, lease.RunID, active.Key, lease.Token,
				StepTransition{State: StepPending, Retry: true})
			return err
		case ReconcileFail:
			result := decision.StepResult
			if result.State != StepFailed && result.State != StepUnavailable {
				result.State = StepFailed
			}
			if _, err := e.store.TransitionStep(ctx, lease.RunID, active.Key, lease.Token,
				StepTransition{
					State: result.State, Evidence: result.Evidence, Cleanup: result.Cleanup,
					ErrorCode: decision.TerminalCode, ErrorMessage: decision.Reason,
				}); err != nil {
				return err
			}
			return e.failRun(ctx, lease, decision.TerminalCode, decision.Reason, result.Recovered)
		default:
			return fmt.Errorf("invalid reconciliation action %q", decision.Action)
		}
	}
}

func (e *Engine) logFailure(runID int64, err error) {
	if e.log != nil {
		e.log.Error("deployment worker failed", "run", runID, "err", err)
	}
}

type unavailableExecutor struct{}

func (unavailableExecutor) Execute(_ context.Context, execution StepExecution) StepResult {
	return StepResult{
		State: StepUnavailable, ErrorCode: "adapter_unavailable",
		ErrorMessage: fmt.Sprintf("no adapter owns deployment step %s", execution.Step.Key),
	}
}

type conservativeReconciler struct{}

func (conservativeReconciler) Reconcile(_ context.Context, request ReconcileRequest) ReconcileResult {
	step := "unknown side effect"
	if request.Step != nil {
		step = string(request.Step.Key)
	}
	return ReconcileResult{
		Action: ReconcileFail, TerminalCode: "restart_evidence_missing",
		Reason: fmt.Sprintf("Cannot prove the outcome of interrupted step %s", step),
	}
}
