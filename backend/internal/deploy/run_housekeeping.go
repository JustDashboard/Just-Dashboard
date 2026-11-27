package deploy

import "context"

// HousekeepingObserver is the run observer that lets go of what a run held
// once it has finished: the certificate attempt its build step started
// (certificate_prewarm.go), cancelled with a cancelled run and otherwise
// left to finish its order for the next run to reuse, and its source
// workspace, which a run that ended before release_task left behind
// (workspace_sweep.go).
//
// Like every observer it never influences the run, which is terminal by the
// time it is told.
func (e *NormalizedStepExecutor) HousekeepingObserver() RunObserver {
	return runHousekeeping{executor: e}
}

type runHousekeeping struct{ executor *NormalizedStepExecutor }

func (runHousekeeping) RunStarted(context.Context, EngineRun) {}

func (h runHousekeeping) RunFinished(ctx context.Context, run EngineRun) {
	if h.executor == nil {
		return
	}
	h.executor.prewarms.finish(run.ID, run.State == RunCancelled)
	if run.State.Terminal() {
		// A failure is the start-up sweep's to retry.
		_, _ = h.executor.removeRunWorkspace(ctx, run.ID)
	}
}
