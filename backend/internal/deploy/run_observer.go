package deploy

import (
	"context"
	"time"
)

// RunObserver is told about a run after its lifecycle transition has been
// persisted. Observers exist for the audience outside the transcript — a chat
// channel, a commit status — and are therefore called for every outcome, not
// only the successful path that reaches the `notify` release step. A failed
// deployment is the one somebody most needs to hear about.
//
// Observers must never influence a run: they receive a copy of the persisted
// row, run detached from the worker's context with their own deadline, and
// their errors are logged rather than returned.
type RunObserver interface {
	RunStarted(context.Context, EngineRun)
	RunFinished(context.Context, EngineRun)
}

// RunObservers fans one transition out to several observers.
type RunObservers []RunObserver

func (o RunObservers) RunStarted(ctx context.Context, run EngineRun) {
	for _, observer := range o {
		if observer != nil {
			observer.RunStarted(ctx, run)
		}
	}
}

func (o RunObservers) RunFinished(ctx context.Context, run EngineRun) {
	for _, observer := range o {
		if observer != nil {
			observer.RunFinished(ctx, run)
		}
	}
}

// observerDeadline bounds one observer notification. Long enough for an SMTP
// handshake or a slow chat provider, short enough that Shutdown is not held
// hostage by an unreachable endpoint.
const observerDeadline = 30 * time.Second

// WithObservers registers the observers the engine notifies. It must be called
// before Start.
func (e *Engine) WithObservers(observers ...RunObserver) *Engine {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, observer := range observers {
		if observer != nil {
			e.observers = append(e.observers, observer)
		}
	}
	return e
}

// observeStarted announces a run whose first claim has just been taken.
func (e *Engine) observeStarted(runID int64) {
	e.observe(runID, func(ctx context.Context, run EngineRun) {
		if run.State.Terminal() {
			return
		}
		e.observers.RunStarted(ctx, run)
	})
}

// observeFinished announces a run that has reached a terminal state through
// any path: success, failure, verified rollback, cancellation or reconciliation.
func (e *Engine) observeFinished(runID int64) {
	e.observe(runID, func(ctx context.Context, run EngineRun) {
		if !run.State.Terminal() {
			return
		}
		e.observers.RunFinished(ctx, run)
	})
}

func (e *Engine) observe(runID int64, deliver func(context.Context, EngineRun)) {
	e.mu.Lock()
	observers := e.observers
	e.mu.Unlock()
	if len(observers) == 0 {
		return
	}
	e.observerWG.Add(1)
	go func() {
		defer e.observerWG.Done()
		ctx, cancel := context.WithTimeout(context.Background(), observerDeadline)
		defer cancel()
		run, err := e.store.Run(ctx, runID)
		if err != nil {
			e.logFailure(runID, err)
			return
		}
		deliver(ctx, *run)
	}()
}

// waitObservers lets Shutdown drain in-flight notifications so a deployment
// that finished a moment before the process stopped still reaches its audience.
func (e *Engine) waitObservers(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		e.observerWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
