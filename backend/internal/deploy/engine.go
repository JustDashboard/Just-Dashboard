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

	mu       sync.Mutex
	started  bool
	stop     context.CancelFunc
	wake     chan struct{}
	running  map[int64]context.CancelCauseFunc
	workerWG sync.WaitGroup
	loopWG   sync.WaitGroup
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
	}
}

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
	go e.schedule(loopCtx)
	return nil
}

// Shutdown stops new claims and waits for currently executing adapters. It
// does not cancel them: activation and restoration must reach a converged
// state. A user cancellation goes through Cancel and remains explicit.
func (e *Engine) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	stop := e.stop
	e.stop = nil
	e.started = false
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
		return nil
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
	e.Notify()
	return run, nil
}

func (e *Engine) schedule(ctx context.Context) {
	defer e.loopWG.Done()
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
		defer e.workerWG.Done()
		defer func() {
			cancel(context.Canceled)
			e.mu.Lock()
			delete(e.running, lease.RunID)
			e.mu.Unlock()
			e.Notify()
		}()
		heartbeatDone := make(chan struct{})
		go e.heartbeat(workerCtx, lease, cancel, heartbeatDone)
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
		output := &storeStepOutput{
			ctx: ctx, store: e.store, runID: lease.RunID, stepID: stepPtr.ID,
			claimToken: lease.Token,
		}
		result := e.executor.Execute(ctx, StepExecution{
			Run: *run, Step: *stepPtr, ClaimToken: lease.Token, Output: output,
		})
		if ctx.Err() != nil {
			if errors.Is(context.Cause(ctx), ErrLeaseLost) {
				return ErrLeaseLost
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
		if output.err != nil {
			result = StepResult{
				State: StepFailed, ErrorCode: "event_persist_failed",
				ErrorMessage: output.err.Error(),
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

type storeStepOutput struct {
	ctx        context.Context
	store      *OrchestrationStore
	runID      int64
	stepID     int64
	claimToken string
	err        error
}

func (o *storeStepOutput) Log(stream, text string) error {
	if o.err != nil {
		return o.err
	}
	ctx := o.ctx
	if ctx.Err() != nil {
		ctx = context.WithoutCancel(ctx)
	}
	_, o.err = o.store.AppendLog(ctx, o.runID, o.stepID, o.claimToken, stream, text)
	return o.err
}

func (o *storeStepOutput) Event(input EventInput) error {
	if o.err != nil {
		return o.err
	}
	input.StepID = o.stepID
	ctx := o.ctx
	if ctx.Err() != nil {
		ctx = context.WithoutCancel(ctx)
	}
	_, o.err = o.store.AppendEvent(ctx, o.runID, o.claimToken, input)
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
