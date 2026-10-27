package deploy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

// RollbackEnqueuer asks for the instant rollback a reading decided on. It is
// the API's enqueue, so the run is admitted, ordered and audited exactly as
// one an operator pressed for.
type RollbackEnqueuer func(ctx context.Context, watch RollbackWatch, verdict RollbackVerdict, attempt int) (*EngineRun, error)

// RollbackAudit records what the watcher did on nobody's request: asking for
// a rollback, and removing a warm container.
type RollbackAudit func(ctx context.Context, action string, watch RollbackWatch, detail map[string]any, success bool)

// RollbackWatcher reads every open window's request record and acts on it.
type RollbackWatcher struct {
	executor *NormalizedStepExecutor
	record   RequestRecord
	enqueue  RollbackEnqueuer
	audit    RollbackAudit
	now      func() time.Time
	interval time.Duration
	log      *slog.Logger
}

func NewRollbackWatcher(executor *NormalizedStepExecutor, record RequestRecord, enqueue RollbackEnqueuer, audit RollbackAudit, log *slog.Logger) *RollbackWatcher {
	return &RollbackWatcher{executor: executor, record: record, enqueue: enqueue, audit: audit, now: time.Now, interval: 10 * time.Second, log: log}
}

// WithInterval sets how often the windows are read. The default is ten
// seconds: often enough that a release failing every request is caught
// within a few of them, rarely enough that a cached record answers.
func (w *RollbackWatcher) WithInterval(interval time.Duration) *RollbackWatcher {
	if interval > 0 {
		w.interval = interval
	}
	return w
}

// Start runs the watcher until the context ends. The first pass runs at
// once, so a window a restart interrupted is looked at before anything else
// has a chance to happen in it.
func (w *RollbackWatcher) Start(ctx context.Context) {
	if w == nil || w.executor == nil || w.executor.store == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()
		for {
			pass, cancel := context.WithTimeout(ctx, 5*time.Minute)
			w.Tick(pass)
			cancel()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// Tick is one pass over every open window.
func (w *RollbackWatcher) Tick(ctx context.Context) {
	watches, err := w.executor.store.OpenRollbackWatches(ctx)
	if err != nil {
		w.warn("rollback windows could not be read", err)
		return
	}
	for _, watch := range watches {
		if ctx.Err() != nil {
			return
		}
		switch watch.State {
		case RollbackWatchClosing:
			if err := w.executor.closeWarmWindow(ctx, watch, watch.EndReason, watch.EndMessage); err != nil {
				w.warn(fmt.Sprintf("release %d's warm container could not be removed yet", watch.WarmReleaseID), err)
			}
		case RollbackWatchRollingBack:
			holder, err := w.executor.store.Run(ctx, watch.RollbackRunID)
			if err != nil || !holder.State.Terminal() {
				continue
			}
			if err := w.executor.repairRollbackWatch(ctx, watch); err != nil {
				w.warn("an interrupted instant rollback could not be settled", err)
			}
		case RollbackWatchWatching:
			w.watch(ctx, watch)
		}
	}
}

func (w *RollbackWatcher) watch(ctx context.Context, watch RollbackWatch) {
	store := w.executor.store
	if watch.RollbackRunID != 0 {
		run, err := store.Run(ctx, watch.RollbackRunID)
		if err != nil || !run.State.Terminal() {
			// The run it asked for has not claimed the window yet.
			return
		}
		_ = store.forgetRollbackRun(ctx, watch.ID, watch.RollbackRunID)
		watch.RollbackRunID = 0
	}
	runtime, err := store.RuntimeForRelease(ctx, watch.WarmReleaseID)
	if err != nil {
		w.close(ctx, watch, RollbackEndWarmLost, fmt.Sprintf("Release #%d's runtime record is gone, so it can no longer be switched back to.", watch.WarmReleaseNum))
		return
	}
	if warmer, ok := w.executor.runtime.(RuntimeWarmer); ok {
		state, err := warmer.WarmState(ctx, *runtime)
		switch {
		case err != nil:
			w.warn("a warm container could not be inspected", err)
		case state == "running":
			// A Docker restart brings a paused container back running; it
			// is frozen again before it does any work nobody routes to it.
			if err := warmer.PauseRuntime(ctx, *runtime); err != nil {
				w.warn("a warm container could not be paused again", err)
			}
		case state != "paused":
			w.close(ctx, watch, RollbackEndWarmLost, fmt.Sprintf("Release #%d's warm container is %s, so it can no longer be switched back to instantly.", watch.WarmReleaseNum, state))
			return
		}
	}
	if available, total := w.executor.memory(); total > 0 && available < warmMemoryFloor(total) {
		w.close(ctx, watch, RollbackEndMemory, fmt.Sprintf("The host had %d MB available, below the %d MB floor, so release #%d gave its memory back.",
			available>>20, warmMemoryFloor(total)>>20, watch.WarmReleaseNum))
		return
	}
	now := w.now().UTC()
	baseline, observed, verdict := w.read(ctx, watch, now)
	if err := store.recordRollbackReading(ctx, watch.ID, baseline, observed, verdict); err != nil {
		w.warn("a rollback reading could not be recorded", err)
	}
	if verdict.Tripped && watch.Policy.Automatic && watch.Attempts < maxAutomaticRollbackRuns && w.enqueue != nil {
		w.rollback(ctx, watch, verdict)
		return
	}
	if !now.Before(watch.ExpiresAt) {
		message := fmt.Sprintf("Release #%d's window passed, so release #%d was removed.", watch.ReleaseNumber, watch.WarmReleaseNum)
		if verdict.Code == RollbackVerdictWithinBaseline {
			message = fmt.Sprintf("Release #%d stayed within the previous release's baseline for the whole window, so release #%d was removed.", watch.ReleaseNumber, watch.WarmReleaseNum)
		}
		w.close(ctx, watch, RollbackEndWindowPassed, message)
	}
}

// read takes the warm release's baseline the first time it can, and the
// watched release's reading since the cutover every time.
func (w *RollbackWatcher) read(ctx context.Context, watch RollbackWatch, now time.Time) (*RollbackReading, *RollbackReading, RollbackVerdict) {
	store := w.executor.store
	_, snapshot, err := w.executor.releaseSnapshot(ctx, watch.ReleaseID)
	if err == nil && len(publicRouteURLs(snapshot.Domains)) == 0 {
		return nil, nil, RollbackVerdict{Code: RollbackVerdictNoRoute,
			Reason: fmt.Sprintf("Release #%d has no domain, so no request reaches it through the ingress and nothing is judged automatically. Roll back instantly still works.", watch.ReleaseNumber)}
	}
	if w.record == nil {
		return nil, nil, RollbackVerdict{Code: RollbackVerdictNoRecord, Reason: "The request record is unavailable, so nothing is judged automatically."}
	}
	route := deploymentRouteName(watch.EnvironmentID)
	var baseline *RollbackReading
	if watch.baselineMissing {
		from := watch.CutoverAt.Add(-rollbackBaselineWindow)
		if warm, err := store.Release(ctx, watch.WarmReleaseID); err == nil && warm.Release.ActivatedAt != nil && warm.Release.ActivatedAt.After(from) {
			from = *warm.Release.ActivatedAt
		}
		before, err := w.record.Window(ctx, route, accesslog.Filter{Since: from, Until: watch.CutoverAt.Add(-time.Nanosecond), Limit: 1})
		if err == nil {
			reading := rollbackReading(before, from, watch.CutoverAt)
			baseline = &reading
			watch.Baseline = baseline
		}
	}
	after, err := w.record.Window(ctx, route, accesslog.Filter{Since: watch.CutoverAt, Until: now, Limit: 1})
	if err != nil {
		return baseline, nil, RollbackVerdict{Code: RollbackVerdictNoRecord,
			Reason: "The request record could not be read, so nothing is judged until it can be."}
	}
	observed := rollbackReading(after, watch.CutoverAt, now)
	verdict := JudgeRollback(watch.Policy, watch.Baseline, observed, after.Facts.Latency)
	return baseline, &observed, verdict
}

func (w *RollbackWatcher) rollback(ctx context.Context, watch RollbackWatch, verdict RollbackVerdict) {
	run, err := w.enqueue(ctx, watch, verdict, watch.Attempts+1)
	if err == nil {
		err = w.executor.store.RecordRollbackRun(ctx, watch.ID, run.ID)
	}
	detail := map[string]any{
		"environmentId": watch.EnvironmentID, "releaseId": watch.ReleaseID, "warmReleaseId": watch.WarmReleaseID,
		"watchId": watch.ID, "rule": verdict.Code, "reason": verdict.Reason,
	}
	if run != nil {
		detail["runId"] = run.ID
	}
	if w.audit != nil {
		w.audit(ctx, "deploy.release.auto_rollback", watch, detail, err == nil)
	}
	if err != nil {
		w.warn("an automatic rollback could not be asked for", err)
	}
}

func (w *RollbackWatcher) close(ctx context.Context, watch RollbackWatch, reason, message string) {
	err := w.executor.closeWarmWindow(ctx, watch, reason, message)
	if errors.Is(err, ErrRollbackWatchUnchanged) {
		return
	}
	if w.audit != nil {
		w.audit(ctx, "deploy.release.warm_retired", watch, map[string]any{
			"environmentId": watch.EnvironmentID, "releaseId": watch.WarmReleaseID, "watchId": watch.ID,
			"reason": reason, "message": message,
		}, err == nil)
	}
	if err != nil {
		w.warn("a warm container could not be removed", err)
	}
}

func (w *RollbackWatcher) warn(message string, err error) {
	if w.log != nil {
		w.log.Warn(message, "err", err)
	}
}
