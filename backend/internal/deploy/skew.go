package deploy

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// Skew protection (runtime.skewProtection) keeps each visitor on the release
// that first answered them for as long as that release is kept: the public
// route sets the jd_release cookie on a visitor's first answer, and after a
// cutover a request whose cookie names the previous release is still
// answered by it while it is warm. So a page loaded from one release never
// asks the next one for scripts and styles it does not have.
//
// The previous release stays RUNNING for the window rather than paused
// (rollback_executor.go): it is still serving. The window's memory floor
// applies to it as to a paused one, and its end — passed, kept, superseded,
// short of memory or lost — first takes it off the route, so its visitors
// are moved to the live release before its container is removed.
//
// A rollback is never pinned: a visitor is not kept on the release that is
// being rolled away from.

// releaseLabel is how a route and its access log name a release.
func releaseLabel(id int64) string { return strconv.FormatInt(id, 10) }

// validateSkewProtection is the plan's own rule, so a plan that saves is one
// whose route can keep a visitor on the release that answered them.
func validateSkewProtection(c PlanConfiguration) error {
	if !c.Runtime.SkewProtection {
		return nil
	}
	if !c.Runtime.Strategy.startsBeside() {
		return invalidField("runtime.skewProtection", "skew protection keeps visitors on the previous release while the next one is live beside it, and a stop-first release stops the previous one before the next starts")
	}
	if c.Build.Method == BuildCompose || c.Build.Method == BuildLegacyCompose {
		return invalidField("runtime.skewProtection", "skew protection keeps the previous release running for its visitors, and a Compose stack is never kept warm")
	}
	if len(publicRouteURLs(c.Domains)) == 0 {
		return invalidField("runtime.skewProtection", "skew protection is a cookie the public route sets, and this plan has no domain that serves the application")
	}
	return nil
}

// skewPins says whether a run's cutover keeps its predecessor for the
// visitors it answered: skew protection is on, and the run is not a rollback.
func skewPins(run EngineRun, plan RuntimePlanConfig) bool {
	return plan.SkewProtection && run.Operation != OperationRollback
}

// withSkew turns skew protection on for a route whose live release is
// releaseID: the cookie lasts the environment's warm window, and pinned, when
// there is one, is the release still answering the visitors it answered
// first.
func (e *NormalizedStepExecutor) withSkew(ctx context.Context, route *proxysvc.DeploymentRoute, environmentID, releaseID int64, pinned *ReleaseRuntime) {
	age := DefaultRollbackPolicy().WindowSeconds
	if policy, err := e.store.RollbackPolicy(ctx, environmentID); err == nil {
		age = policy.WindowSeconds
	}
	age = min(max(age, 1), proxysvc.MaxReleaseCookieAge)
	skew := &proxysvc.DeploymentSkew{Release: releaseLabel(releaseID), MaxAge: age}
	if pinned != nil && pinned.ReleaseID != releaseID {
		if upstreams := runtimeUpstreams(*pinned); len(upstreams) > 0 {
			skew.Pinned = []proxysvc.DeploymentPinned{{Release: releaseLabel(pinned.ReleaseID), Upstreams: upstreams}}
		}
	}
	route.Skew = skew
}

// warmPin is the release an open window keeps running for its visitors
// beside releaseID, the release it watches; nil when there is none. A
// window being closed pins nothing: closing it is taking it off the route.
func (e *NormalizedStepExecutor) warmPin(ctx context.Context, environmentID, releaseID int64) *ReleaseRuntime {
	watch, err := e.store.OpenRollbackWatch(ctx, environmentID)
	if err != nil || !watch.WarmRunning || watch.ReleaseID != releaseID {
		return nil
	}
	if watch.State != RollbackWatchWatching && watch.State != RollbackWatchRollingBack {
		return nil
	}
	runtime, err := e.store.RuntimeForRelease(ctx, watch.WarmReleaseID)
	if err != nil || runtime.State != "warm" {
		return nil
	}
	return runtime
}

// activationPin is the predecessor a cutover keeps for its visitors: the
// live release's own containers, when the run's plan asks for it.
func (e *NormalizedStepExecutor) activationPin(ctx context.Context, run EngineRun, release Release, plan RuntimePlanConfig) *ReleaseRuntime {
	if !skewPins(run, plan) || release.PredecessorReleaseID == 0 {
		return nil
	}
	previous, err := e.store.RuntimeForRelease(ctx, release.PredecessorReleaseID)
	if err != nil || previous.Kind != "container" || previous.State != "live" {
		return nil
	}
	return previous
}

// unpinPredecessor takes the release a cutover kept for its visitors off the
// route before it is removed, so none of them is sent to a container that is
// going away. A route that cannot be rewritten is said in the run and the
// retirement goes ahead: the release was going to be removed either way.
func (e *NormalizedStepExecutor) unpinPredecessor(ctx context.Context, execution StepExecution, release Release, snapshot runtimeReleaseSnapshot) {
	if !skewPins(execution.Run, snapshot.Plan) || len(snapshot.Domains) == 0 || e.proxy == nil {
		return
	}
	if err := e.applyLiveRoute(ctx, release.ID, false); err != nil {
		_ = stepLog(execution, "stderr", fmt.Sprintf("The route still sends the visitors release #%d answered first to it, and it is being removed: %v", release.Number, err))
		return
	}
	_ = stepLog(execution, "status", "The previous release's visitors are moved to the live release before it is removed")
}

// skewFinding is preflight's account of skew protection.
func skewFinding(configuration PlanConfiguration) (PreflightFinding, bool) {
	if !configuration.Runtime.SkewProtection {
		return PreflightFinding{}, false
	}
	return finding("skew_protection", PreflightPass,
		"Each visitor stays on the release that first answered them while it is kept warm",
		"jd_release cookie",
		"The public route tells each visitor which release answered them in an HttpOnly jd_release cookie that lasts the warm window. "+
			"After a cutover the previous release is kept running rather than paused, and answers the visitors whose cookie names it until its window ends; "+
			"everyone else, and everyone afterwards, gets the new release. It needs keeping the previous release warm on, under Releases.",
		"", "deploy", "runtime.skewProtection"), true
}

// releaseNumber is a release's number for the transcript, its id when it
// cannot be read.
func (e *NormalizedStepExecutor) releaseNumber(ctx context.Context, id int64) int64 {
	release, err := e.store.Release(ctx, id)
	if err != nil || release.Release.Number == 0 {
		return id
	}
	return release.Release.Number
}
