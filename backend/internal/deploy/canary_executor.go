package deploy

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// WithCanaryTiming sets how often a canary looks for an operator's decision
// and how often it reads the two releases, for tests. The defaults are a
// second and five seconds.
func (e *NormalizedStepExecutor) WithCanaryTiming(poll, read time.Duration) *NormalizedStepExecutor {
	e.canaryPoll, e.canaryRead = poll, read
	return e
}

func (e *NormalizedStepExecutor) canaryIntervals() (time.Duration, time.Duration) {
	poll, read := e.canaryPoll, e.canaryRead
	if poll <= 0 {
		poll = time.Second
	}
	if read <= 0 {
		read = 5 * time.Second
	}
	return poll, read
}

// canaryParties is what one trial needs to read and route: the candidate and
// the live release it shares the route with.
type canaryParties struct {
	release          *ReleaseWithArtifacts
	snapshot         runtimeReleaseSnapshot
	runtime          *ReleaseRuntime
	live             *ReleaseWithArtifacts
	liveSnapshot     runtimeReleaseSnapshot
	liveRuntime      *ReleaseRuntime
	candidateLabel   string
	liveReleaseLabel string
}

// runCanary is the canary step. A plan with another strategy, a rollback and
// a first release skip it; a canary plan's candidate joins the live route
// for its share and is held there until the comparison or an operator
// decides. A restarted process resumes the window it finds open for the run
// rather than opening another, so the trial keeps its start and its end.
func (e *NormalizedStepExecutor) runCanary(ctx context.Context, execution StepExecution) StepResult {
	release, snapshot, err := e.releaseSnapshotForRun(ctx, execution.Run.ID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	terms := snapshot.Plan.Canary
	skip := func(reason string) StepResult {
		return StepResult{State: StepSkipped, Evidence: mustJSON(map[string]any{"reason": reason})}
	}
	if snapshot.Plan.Strategy != StrategyCanary || terms == nil {
		return skip("the plan cuts over without a canary")
	}
	if execution.Run.Operation == OperationRollback {
		_ = stepLog(execution, "status", "A rollback goes back at once, without a canary")
		return skip("a rollback goes back at once, without a canary")
	}
	if release.Release.PredecessorReleaseID == 0 {
		_ = stepLog(execution, "status", "No canary for the first release: there is no live release to share the route with")
		return skip("the first release has no live release to share the route with")
	}
	if len(publicRouteURLs(snapshot.Domains)) == 0 {
		return skip("the release has no domain whose requests a canary could share")
	}
	if e.proxy == nil {
		return StepResult{State: StepUnavailable, ErrorCode: "proxy_unavailable", ErrorMessage: "a canary shares the route through an HTTP proxy, and none is available"}
	}
	parties, failure := e.canaryParties(ctx, execution.Run, release, snapshot)
	if failure != nil {
		return *failure
	}
	if parties == nil {
		_ = stepLog(execution, "status", "No canary: the release it would share the route with is no longer live")
		return skip("the release it would share the route with is no longer live")
	}

	window, err := e.store.canaryForRun(ctx, execution.Run.ID)
	switch {
	case err == nil && window.Open():
		_ = stepLog(execution, "status", fmt.Sprintf("Resuming release #%d's canary: it takes %d%% of the requests until %s",
			window.ReleaseNumber, window.Percent, window.EndsAt.UTC().Format("15:04:05 UTC")))
	case err == nil:
		// The trial ended and the process stopped before the step did.
		return canaryEnded(*window)
	case errors.Is(err, ErrNoCanary):
		policy, policyErr := e.store.RollbackPolicy(ctx, execution.Run.EnvironmentID)
		if policyErr != nil {
			return normalizedStepFailure(policyErr)
		}
		window, err = e.store.openCanary(ctx, execution.Run, execution.ClaimToken, canaryInput{
			ReleaseID: release.Release.ID, LiveReleaseID: parties.live.Release.ID, Terms: *terms, Policy: policy,
		})
		if err != nil {
			return normalizedStepFailure(err)
		}
	default:
		return normalizedStepFailure(err)
	}

	route, err := e.canaryRoute(ctx, parties, window.Percent)
	if err == nil {
		_, err = e.proxy.ApplyDeploymentRoute(ctx, route)
	}
	if err != nil {
		// The proxy put its prior route back itself; the candidate never
		// served anyone.
		return e.abortCanary(ctx, execution, parties, *window, CanaryEndRouteFailed,
			fmt.Sprintf("The route could not be shared with release #%d, so it was removed and release #%d kept every request: %v",
				window.ReleaseNumber, window.LiveReleaseNum, err))
	}
	then := "then waits for you to promote or abort it"
	if window.AutoPromote {
		then = "then goes live if it compares well"
	}
	_ = stepLog(execution, "status", fmt.Sprintf("Release #%d takes %d%% of the requests beside release #%d until %s, judged against it side by side; it %s",
		window.ReleaseNumber, window.Percent, window.LiveReleaseNum, window.EndsAt.UTC().Format("15:04:05 UTC"), then))

	// Waiting builds nothing: the run gives the host's build slot back for
	// the window and takes its own class again once the trial is decided.
	_ = e.store.holdLeaseSlot(ctx, execution.Run.ID, execution.ClaimToken, true)
	defer func() {
		restore, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		_ = e.store.holdLeaseSlot(restore, execution.Run.ID, execution.ClaimToken, false)
		cancel()
	}()

	poll, read := e.canaryIntervals()
	nextRead := time.Time{}
	held := window.State == CanaryHolding
	for {
		select {
		case <-ctx.Done():
			// An operator's cancellation removes the candidate in the run's
			// cleanup; a stopping process leaves the trial as it is, for the
			// next one to resume.
			return StepResult{State: StepCancelled, ErrorCode: "cancelled", ErrorMessage: "the canary was interrupted"}
		case <-time.After(poll):
		}
		current, err := e.store.Canary(ctx, window.ID)
		if err != nil {
			continue
		}
		window = current
		if !window.Open() {
			return canaryEnded(*window)
		}
		now := e.store.now().UTC()
		if window.Decision == CanaryAbort {
			return e.abortCanary(ctx, execution, parties, *window, CanaryEndOperatorAborted,
				fmt.Sprintf("%s aborted release #%d's canary, so it was removed and release #%d kept every request.",
					orSomeone(window.DecidedBy), window.ReleaseNumber, window.LiveReleaseNum))
		}
		if window.Decision == CanaryPromote {
			return e.promoteCanary(ctx, execution, parties, *window, CanaryEndOperatorPromoted,
				fmt.Sprintf("%s promoted release #%d from its canary.", orSomeone(window.DecidedBy), window.ReleaseNumber))
		}
		if e.candidateDown(ctx, *parties.runtime) {
			return e.abortCanary(ctx, execution, parties, *window, CanaryEndCandidateLost,
				fmt.Sprintf("Release #%d's containers stopped during its canary, so it was removed and release #%d kept every request.",
					window.ReleaseNumber, window.LiveReleaseNum))
		}
		windowPassed := !now.Before(window.EndsAt)
		if now.After(nextRead) || (windowPassed && !held) {
			nextRead = now.Add(read)
			live, candidate, verdict := e.canaryReading(ctx, *window, parties, now)
			if err := e.store.recordCanaryReading(ctx, window.ID, live, candidate, verdict); err != nil {
				_ = stepLog(execution, "stderr", "The canary's reading could not be recorded: "+err.Error())
			}
			if verdict.Tripped {
				return e.abortCanary(ctx, execution, parties, *window, CanaryEndFailed,
					fmt.Sprintf("Release #%d did worse than release #%d during its canary, so it was removed and release #%d kept every request: %s",
						window.ReleaseNumber, window.LiveReleaseNum, window.LiveReleaseNum, verdict.Reason))
			}
			window.Verdict = &verdict
		}
		if windowPassed && !held {
			reason := ""
			if window.Verdict != nil {
				reason = " " + window.Verdict.Reason
			}
			if window.AutoPromote {
				return e.promoteCanary(ctx, execution, parties, *window, CanaryEndPassed,
					fmt.Sprintf("Release #%d's canary passed, so it is promoted.%s", window.ReleaseNumber, reason))
			}
			_ = e.store.holdCanary(ctx, window.ID)
			held = true
			_ = stepLog(execution, "status", fmt.Sprintf("Release #%d's canary window passed without doing worse than release #%d, and it keeps its %d%% until you promote or abort it.%s",
				window.ReleaseNumber, window.LiveReleaseNum, window.Percent, reason))
		}
		if held && !now.Before(window.EndsAt.Add(canaryHoldLimit)) {
			return e.abortCanary(ctx, execution, parties, *window, CanaryEndUnanswered,
				fmt.Sprintf("Nobody promoted release #%d within %s of its canary window, so it was removed and release #%d kept every request.",
					window.ReleaseNumber, canaryHoldLimit, window.LiveReleaseNum))
		}
	}
}

func orSomeone(actor string) string {
	if actor == "" {
		return "An operator"
	}
	return actor
}

// canaryEnded is the step's result for a trial that already ended.
func canaryEnded(window CanaryWindow) StepResult {
	evidence := mustJSON(canaryEvidence(window))
	if window.State == CanaryPromoted {
		return StepResult{State: StepPassed, Evidence: evidence}
	}
	return StepResult{State: StepFailed, ErrorCode: canaryFailureCode(window.EndReason), ErrorMessage: window.EndMessage, Evidence: evidence, Recovered: true}
}

func canaryFailureCode(reason string) string {
	switch reason {
	case CanaryEndFailed:
		return "canary_failed"
	case CanaryEndRouteFailed:
		return "canary_route_failed"
	case CanaryEndCandidateLost:
		return "canary_candidate_lost"
	default:
		return "canary_aborted"
	}
}

func canaryEvidence(window CanaryWindow) map[string]any {
	return map[string]any{
		"canaryId": window.ID, "state": window.State, "endReason": window.EndReason, "message": window.EndMessage,
		"percent": window.Percent, "durationSeconds": window.DurationSeconds, "autoPromote": window.AutoPromote,
		"decidedBy": window.DecidedBy, "live": window.Live, "candidate": window.Candidate, "verdict": window.Verdict,
	}
}

// canaryParties reads the two releases. Nil without a failure is a live
// release that is not the candidate's predecessor any more.
func (e *NormalizedStepExecutor) canaryParties(ctx context.Context, run EngineRun, release *ReleaseWithArtifacts, snapshot runtimeReleaseSnapshot) (*canaryParties, *StepResult) {
	fail := func(err error) (*canaryParties, *StepResult) {
		failure := normalizedStepFailure(err)
		return nil, &failure
	}
	live, err := e.store.LiveRelease(ctx, run.EnvironmentID)
	if errors.Is(err, ErrArtifactMissing) {
		return nil, nil
	}
	if err != nil {
		return fail(err)
	}
	if live.Release.ID != release.Release.PredecessorReleaseID {
		return nil, nil
	}
	_, liveSnapshot, err := e.releaseSnapshot(ctx, live.Release.ID)
	if err != nil {
		return fail(err)
	}
	liveRuntime, err := e.store.RuntimeForRelease(ctx, live.Release.ID)
	if err != nil {
		return fail(err)
	}
	runtime, err := e.store.RuntimeForRelease(ctx, release.Release.ID)
	if err != nil {
		return fail(err)
	}
	return &canaryParties{
		release: release, snapshot: snapshot, runtime: runtime,
		live: live, liveSnapshot: liveSnapshot, liveRuntime: liveRuntime,
		candidateLabel:   strconv.FormatInt(release.Release.ID, 10),
		liveReleaseLabel: strconv.FormatInt(live.Release.ID, 10),
	}, nil
}

// canaryRoute is the live release's own route — its domains and its frozen
// rules, which are what visitors are served under until the cutover — with
// the candidate's replicas taking their share beside its own.
func (e *NormalizedStepExecutor) canaryRoute(ctx context.Context, parties *canaryParties, percent int) (proxysvc.DeploymentRoute, error) {
	route, err := e.liveOnlyRoute(ctx, parties)
	if err != nil {
		return route, err
	}
	route.Canary = &proxysvc.DeploymentCanary{
		Percent: percent, Upstreams: runtimeUpstreams(*parties.runtime),
		Live: parties.liveReleaseLabel, Candidate: parties.candidateLabel,
	}
	return route, nil
}

// liveOnlyRoute is the live release's route with nobody beside it.
func (e *NormalizedStepExecutor) liveOnlyRoute(ctx context.Context, parties *canaryParties) (proxysvc.DeploymentRoute, error) {
	route := publicRoute(parties.live.Release.EnvironmentID, parties.liveSnapshot, *parties.liveRuntime)
	if route.TLS {
		resolver, ok := e.proxy.(interface {
			ResolveDeploymentCertificate(context.Context, []string) (string, string, error)
		})
		if !ok {
			return route, errors.New("TLS route activation cannot resolve an existing certificate")
		}
		certPath, keyPath, err := resolver.ResolveDeploymentCertificate(ctx, route.TLSHostnames())
		if err != nil {
			return route, err
		}
		route.CertPath, route.KeyPath = certPath, keyPath
	}
	return route, nil
}

// canaryReading reads both releases over the window so far and judges the
// candidate against the live release on the environment's margins.
func (e *NormalizedStepExecutor) canaryReading(ctx context.Context, window CanaryWindow, parties *canaryParties, now time.Time) (*RollbackReading, *RollbackReading, RollbackVerdict) {
	if e.requests == nil {
		return nil, nil, RollbackVerdict{Code: RollbackVerdictNoRecord, Reason: "The request record is unavailable, so the canary is not judged; it is decided by its window and by you."}
	}
	route := deploymentRouteName(window.EnvironmentID)
	read := func(label string) (*RollbackReading, accesslog.Window, error) {
		recorded, err := e.requests.Window(ctx, route, accesslog.Filter{Since: window.StartedAt, Until: now, Release: label, Limit: 1})
		if err != nil {
			return nil, recorded, err
		}
		reading := rollbackReading(recorded, window.StartedAt, now)
		return &reading, recorded, nil
	}
	live, _, err := read(parties.liveReleaseLabel)
	if err != nil {
		return nil, nil, RollbackVerdict{Code: RollbackVerdictNoRecord, Reason: "The request record could not be read, so nothing is judged until it can be."}
	}
	candidate, recorded, err := read(parties.candidateLabel)
	if err != nil {
		return live, nil, RollbackVerdict{Code: RollbackVerdictNoRecord, Reason: "The request record could not be read, so nothing is judged until it can be."}
	}
	return live, candidate, judgeCanary(window.Policy, live, *candidate, recorded.Facts.Latency)
}

// candidateDown says whether every one of the candidate's containers has
// stopped, which no share of traffic can be judged on.
func (e *NormalizedStepExecutor) candidateDown(ctx context.Context, runtime ReleaseRuntime) bool {
	observer, ok := e.runtime.(RuntimeObserver)
	return ok && ReleaseRuntimeDown(ctx, observer, runtime)
}

// promoteCanary ends a trial in the candidate's favour. The route goes back
// to the live release alone first, so the cutover that follows is the
// ordinary one — from a route that names one release to one that names the
// other — and a failed cutover restores a route without a canary in it.
func (e *NormalizedStepExecutor) promoteCanary(ctx context.Context, execution StepExecution, parties *canaryParties, window CanaryWindow, reason, message string) StepResult {
	settle, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	route, err := e.liveOnlyRoute(settle, parties)
	if err == nil {
		_, err = e.proxy.ApplyDeploymentRoute(settle, route)
	}
	if err != nil {
		_ = e.store.closeCanary(settle, window.ID, CanaryAborted, CanaryEndRouteFailed,
			fmt.Sprintf("Release #%d was to be promoted, but the route could not be written: %v. It keeps its share until the next deployment.", window.ReleaseNumber, err))
		ended, _ := e.store.Canary(settle, window.ID)
		result := canaryEnded(*ended)
		result.Recovered = false
		return result
	}
	if err := e.store.closeCanary(settle, window.ID, CanaryPromoted, reason, message); err != nil {
		return normalizedStepFailure(err)
	}
	_ = stepLog(execution, "status", message)
	ended, err := e.store.Canary(settle, window.ID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	return canaryEnded(*ended)
}

// abortCanary ends a trial against the candidate: the route goes back to the
// live release alone, then the candidate is removed. A route that cannot be
// put back leaves the candidate running, since removing it would fail the
// share of visitors still sent to it.
func (e *NormalizedStepExecutor) abortCanary(ctx context.Context, execution StepExecution, parties *canaryParties, window CanaryWindow, reason, message string) StepResult {
	settle, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	route, err := e.liveOnlyRoute(settle, parties)
	if err == nil {
		_, err = e.proxy.ApplyDeploymentRoute(settle, route)
	}
	if err != nil {
		message = fmt.Sprintf("%s The route could not be put back on release #%d alone (%v), so release #%d keeps its share until the next deployment.",
			message, window.LiveReleaseNum, err, window.ReleaseNumber)
		_ = e.store.closeCanary(settle, window.ID, CanaryAborted, CanaryEndRouteFailed, message)
		_ = stepLog(execution, "stderr", message)
		return StepResult{State: StepFailed, ErrorCode: "canary_route_failed", ErrorMessage: message, Evidence: mustJSON(canaryEvidence(window))}
	}
	recovery := e.stopCandidateAndRestore(settle, execution, parties.release.Release, *parties.runtime, parties.snapshot.Plan, nil)
	if err := e.store.closeCanary(settle, window.ID, CanaryAborted, reason, message); err != nil {
		return normalizedStepFailure(err)
	}
	_ = stepLog(execution, "stderr", message)
	ended, err := e.store.Canary(settle, window.ID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	result := canaryEnded(*ended)
	result.Recovered = recoveryComplete(recovery, parties.snapshot.Plan, parties.release.Release)
	return result
}

// endCanaryForCancellation takes a cancelled run's candidate off the route
// before the candidate is removed, so nobody is sent to a container that is
// going away.
func (e *NormalizedStepExecutor) endCanaryForCancellation(ctx context.Context, run EngineRun) error {
	window, err := e.store.canaryForRun(ctx, run.ID)
	if errors.Is(err, ErrNoCanary) {
		return nil
	}
	if err != nil || !window.Open() {
		return err
	}
	release, snapshot, err := e.releaseSnapshotForRun(ctx, run.ID)
	if err != nil {
		return err
	}
	parties, failure := e.canaryParties(ctx, run, release, snapshot)
	if failure != nil {
		return errors.New(failure.ErrorMessage)
	}
	if parties != nil && e.proxy != nil {
		route, err := e.liveOnlyRoute(ctx, parties)
		if err == nil {
			_, err = e.proxy.ApplyDeploymentRoute(ctx, route)
		}
		if err != nil {
			return err
		}
	}
	return e.store.closeCanary(ctx, window.ID, CanaryAborted, CanaryEndCancelled,
		fmt.Sprintf("The run was cancelled during release #%d's canary, so it was removed and release #%d kept every request.", window.ReleaseNumber, window.LiveReleaseNum))
}
