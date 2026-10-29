package deploy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/docker/docker/errdefs"
)

// RuntimeWarmer keeps a retired release's container in memory without
// letting it run, and brings it back. Only a runtime owner that implements it
// can keep a release warm; without it retirement removes the predecessor as
// it always did.
type RuntimeWarmer interface {
	PauseRuntime(context.Context, ReleaseRuntime) error
	ResumeRuntime(context.Context, ReleaseRuntime) error
	// WarmState is Docker's word for the container — running, paused,
	// exited — or "missing" when it is gone.
	WarmState(context.Context, ReleaseRuntime) (string, error)
}

var errWarmRuntimeLost = errors.New("the warm release's container is not there to resume")

// WarmState is the release's containers' state as one word. A replicated
// release is warm as a whole: any replica gone is the release gone, since it
// can no longer be switched back to as it served; any in another state than
// running or paused is that state; one still running among paused ones is
// running, which the watcher pauses again; and all paused is paused.
func (o *DockerRuntimeOwner) WarmState(ctx context.Context, runtime ReleaseRuntime) (string, error) {
	states, err := o.replicaStates(ctx, runtime)
	if err != nil {
		return "", err
	}
	return warmStateOf(states), nil
}

func warmStateOf(states []string) string {
	summary := "paused"
	for _, state := range states {
		switch state {
		case "missing":
			return "missing"
		case "paused":
		case "running":
			if summary == "paused" {
				summary = "running"
			}
		default:
			summary = state
		}
	}
	return summary
}

// replicaStates is Docker's word for each of the release's containers, in
// replica order, "missing" for one that is gone.
func (o *DockerRuntimeOwner) replicaStates(ctx context.Context, runtime ReleaseRuntime) ([]string, error) {
	if o == nil || o.client == nil {
		return nil, ErrRuntimeUnavailable
	}
	if runtime.Kind != "container" {
		return nil, fmt.Errorf("%w: a Compose release is never kept warm", ErrInvalidPlan)
	}
	ids := runtimeContainerIDs(runtime)
	states := make([]string, 0, len(ids))
	for _, id := range ids {
		detail, err := o.client.Inspect(ctx, id)
		if errdefs.IsNotFound(err) {
			states = append(states, "missing")
			continue
		}
		if err != nil {
			return nil, err
		}
		states = append(states, detail.State)
	}
	return states, nil
}

// PauseRuntime freezes every replica of a release that is still running.
func (o *DockerRuntimeOwner) PauseRuntime(ctx context.Context, runtime ReleaseRuntime) error {
	return o.warmEach(ctx, runtime, "running", "paused", dockerx.ActionPause)
}

// ResumeRuntime thaws every replica of a warm release.
func (o *DockerRuntimeOwner) ResumeRuntime(ctx context.Context, runtime ReleaseRuntime) error {
	return o.warmEach(ctx, runtime, "paused", "running", dockerx.ActionUnpause)
}

// warmEach takes each replica from one state to the other, leaving one
// already there as it is. A replica in any third state, or gone, fails the
// whole release before anything is changed, so a release is never left half
// paused by a refusal.
func (o *DockerRuntimeOwner) warmEach(ctx context.Context, runtime ReleaseRuntime, from, to string, action dockerx.LifecycleAction) error {
	states, err := o.replicaStates(ctx, runtime)
	if err != nil {
		return err
	}
	for _, state := range states {
		if state != from && state != to {
			return fmt.Errorf("%w (%s)", errWarmRuntimeLost, state)
		}
	}
	for index, id := range runtimeContainerIDs(runtime) {
		if states[index] == from {
			if err := o.client.Lifecycle(ctx, id, action, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// instantRollback is the metadata an instant rollback run carries: the
// window whose warm release it switches back to, and why.
type instantRollback struct {
	Instant   bool   `json:"instant"`
	WatchID   int64  `json:"watchId"`
	Automatic bool   `json:"automatic"`
	Reason    string `json:"reason"`
}

func instantRollbackOf(run EngineRun) (instantRollback, bool) {
	var request instantRollback
	if run.Operation != OperationRollback || json.Unmarshal(run.Metadata, &request) != nil || !request.Instant || request.WatchID <= 0 {
		return instantRollback{}, false
	}
	return request, true
}

// InstantRollbackSteps are the only steps an instant rollback takes: resume
// the warm container, check it answers, move the route and the pointer back,
// remove the release it replaced, record, notify. Nothing is built or
// started anew.
var InstantRollbackSteps = []StepKey{
	StepStartCandidate, StepVerifyReadiness, StepActivate, StepRetirePrevious, StepRecordRelease, StepNotify,
}

// hostMemoryTotals reads what a warm container would hold memory against.
func hostMemoryTotals() (available, total int64) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, _ := strconv.ParseInt(fields[1], 10, 64)
		switch fields[0] {
		case "MemAvailable:":
			available = value * 1024
		case "MemTotal:":
			total = value * 1024
		}
	}
	return available, total
}

// warmMemoryFloor is the memory the host must still have free for a release
// to stay warm: a quarter of a gigabyte, or a tenth of the machine on a
// bigger one. A paused container holds what it held when it ran, so on a
// server with no room to spare the new release, a build or the database
// needs it more than an instant rollback does.
func warmMemoryFloor(total int64) int64 {
	floor := int64(256 << 20)
	if total/10 > floor {
		floor = total / 10
	}
	return floor
}

func (e *NormalizedStepExecutor) memory() (int64, int64) {
	if e.hostMemory != nil {
		return e.hostMemory()
	}
	return hostMemoryTotals()
}

// WithHostMemory replaces the host memory reading, for tests.
func (e *NormalizedStepExecutor) WithHostMemory(read func() (available, total int64)) *NormalizedStepExecutor {
	e.hostMemory = read
	return e
}

// keepWarm decides whether retirement keeps the predecessor and, if so,
// pauses it and opens the window. It answers why not when it does not,
// so the run says so beside the retirement it did instead.
func (e *NormalizedStepExecutor) keepWarm(
	ctx context.Context,
	execution StepExecution,
	release Release,
	snapshot runtimeReleaseSnapshot,
	previous ReleaseRuntime,
	cutoverAt time.Time,
) (*RollbackWatch, string) {
	warmer, ok := e.runtime.(RuntimeWarmer)
	if !ok {
		return nil, "this runtime cannot keep a release warm"
	}
	var kind EnvironmentKind
	if err := e.store.db.QueryRowContext(ctx, `SELECT kind FROM deploy_environments WHERE id = ?`, release.EnvironmentID).Scan(&kind); err != nil {
		return nil, "the environment could not be read"
	}
	if kind == EnvironmentPreview {
		return nil, "a preview is never kept warm"
	}
	if !snapshot.Plan.Strategy.startsBeside() || previous.Kind != "container" {
		return nil, "only a blue/green release's own containers are kept warm, never a Compose stack's"
	}
	if previous.State != "draining" {
		return nil, "the previous release was not running when this one went live"
	}
	policy, err := e.store.RollbackPolicy(ctx, release.EnvironmentID)
	if err != nil {
		return nil, "the environment's rollback terms could not be read"
	}
	if !policy.KeepWarm {
		return nil, "keeping the previous release warm is turned off for this environment"
	}
	if available, total := e.memory(); total > 0 && available < warmMemoryFloor(total) {
		return nil, fmt.Sprintf("the host had %d MB available, below the %d MB a warm release may not take",
			available>>20, warmMemoryFloor(total)>>20)
	}
	watch, err := e.store.recordWarmRelease(ctx, execution.Run, execution.ClaimToken, warmReleaseInput{
		ReleaseID: release.ID, WarmReleaseID: previous.ReleaseID, WarmRuntimeID: previous.RuntimeID,
		Policy: policy, CutoverAt: cutoverAt,
	})
	if err != nil {
		return nil, "the window could not be recorded: " + err.Error()
	}
	// The window is recorded first: a process killed right here leaves a
	// running container the watcher pauses or removes, never one nobody
	// owns.
	if err := warmer.PauseRuntime(ctx, previous); err != nil {
		_ = e.closeWarmWindow(context.WithoutCancel(ctx), *watch, RollbackEndWarmLost,
			"The previous release's container could not be paused, so it was removed as usual.")
		return nil, "its container could not be paused"
	}
	return watch, ""
}

// cutoverMoment is when this run's activation completed: the first instant
// the new release served, which is where its reading starts.
func (e *NormalizedStepExecutor) cutoverMoment(ctx context.Context, runID int64) time.Time {
	steps, err := e.store.Steps(ctx, runID)
	if err == nil {
		for index := len(steps) - 1; index >= 0; index-- {
			if steps[index].Key == StepActivate && steps[index].EndedAt != nil {
				return *steps[index].EndedAt
			}
		}
	}
	return e.store.now().UTC()
}

// endWarmWindow closes the environment's open window when another run
// starts: the release it judged is no longer the one about to be live, and
// two warm releases are never kept. A failure to remove the container is
// left to the watcher; it is not this run's to fail on.
func (e *NormalizedStepExecutor) endWarmWindow(ctx context.Context, execution StepExecution) {
	if execution.Run.Operation == OperationScheduled {
		return
	}
	watch, err := e.store.OpenRollbackWatch(ctx, execution.Run.EnvironmentID)
	if err != nil || watch.RunID == execution.Run.ID {
		return
	}
	if watch.State == RollbackWatchRollingBack {
		holder, err := e.store.Run(ctx, watch.RollbackRunID)
		if err != nil || !holder.State.Terminal() {
			return
		}
		if err := e.repairRollbackWatch(ctx, *watch); err != nil {
			_ = stepLog(execution, "stderr", "An interrupted instant rollback could not be settled: "+err.Error())
			return
		}
		if watch, err = e.store.OpenRollbackWatch(ctx, execution.Run.EnvironmentID); err != nil {
			return
		}
	}
	number := execution.Run.RunNumber
	if number == 0 {
		number = execution.Run.ID
	}
	message := fmt.Sprintf("Run #%d started, so release #%d is no longer kept warm.", number, watch.WarmReleaseNum)
	if err := e.closeWarmWindow(ctx, *watch, RollbackEndSuperseded, message); err != nil {
		_ = stepLog(execution, "stderr", fmt.Sprintf("Release #%d's warm container is left for the watcher to remove: %v", watch.WarmReleaseNum, err))
		return
	}
	_ = stepLog(execution, "status", fmt.Sprintf("Removed release #%d's warm container: this run replaces the window it was kept for", watch.WarmReleaseNum))
}

// KeepRelease ends a window early on an operator's word: the watched release
// stays, and the warm one gives its memory back now.
func (e *NormalizedStepExecutor) KeepRelease(ctx context.Context, watch RollbackWatch, actor string) error {
	message := fmt.Sprintf("%s kept release #%d, so release #%d was removed before its window passed.", actor, watch.ReleaseNumber, watch.WarmReleaseNum)
	return e.closeWarmWindow(ctx, watch, RollbackEndKept, message)
}

// closeWarmWindow removes the warm container and closes the window. A
// watching window is claimed for closing first, so an instant rollback that
// claims it in the same moment wins or loses as a whole.
func (e *NormalizedStepExecutor) closeWarmWindow(ctx context.Context, watch RollbackWatch, reason, message string) error {
	if watch.State == RollbackWatchWatching {
		if err := e.store.beginCloseRollbackWatch(ctx, watch.ID, reason, message); err != nil {
			return err
		}
		watch.State = RollbackWatchClosing
	}
	if watch.State != RollbackWatchClosing {
		return ErrRollbackWatchUnchanged
	}
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	runtime, err := e.store.RuntimeForRelease(closeCtx, watch.WarmReleaseID)
	if err == nil && runtime.State == "warm" {
		live, liveErr := e.store.LiveRelease(closeCtx, watch.EnvironmentID)
		if liveErr == nil && live.Release.ID == watch.WarmReleaseID {
			return fmt.Errorf("%w: the warm release is live", ErrInvalidPlan)
		}
		if err := e.removeWarmRuntime(closeCtx, *runtime); err != nil {
			return err
		}
	} else if err != nil && !errors.Is(err, ErrArtifactMissing) {
		return err
	}
	return e.store.finishCloseRollbackWatch(closeCtx, watch)
}

// removeWarmRuntime stops and removes a warm container. It is resumed first
// so the stop signal reaches a process that can act on it.
func (e *NormalizedStepExecutor) removeWarmRuntime(ctx context.Context, runtime ReleaseRuntime) error {
	if warmer, ok := e.runtime.(RuntimeWarmer); ok {
		_ = warmer.ResumeRuntime(ctx, runtime)
	}
	plan := RuntimePlanConfig{}
	if _, snapshot, err := e.releaseSnapshot(ctx, runtime.ReleaseID); err == nil {
		plan = snapshot.Plan
	}
	_, err := e.runtime.Stop(ctx, runtime, plan, nil, true, nil)
	return err
}

func warmReason(err error) string {
	return strings.TrimPrefix(err.Error(), ErrNoRollbackWatch.Error()+": ")
}

// claimedWatch reads the window this instant rollback already claimed.
func (e *NormalizedStepExecutor) claimedWatch(ctx context.Context, run EngineRun, request instantRollback) (*RollbackWatch, *StepResult) {
	watch, err := e.store.RollbackWatch(ctx, request.WatchID)
	if err != nil {
		failure := normalizedStepFailure(err)
		return nil, &failure
	}
	if watch.State != RollbackWatchRollingBack || watch.RollbackRunID != run.ID {
		failure := StepResult{State: StepFailed, ErrorCode: "warm_release_unavailable", ErrorMessage: warmReason(warmUnavailable(watch))}
		return nil, &failure
	}
	return watch, nil
}

// resumeWarmRelease is start_candidate for an instant rollback.
func (e *NormalizedStepExecutor) resumeWarmRelease(ctx context.Context, execution StepExecution, request instantRollback) StepResult {
	warmer, ok := e.runtime.(RuntimeWarmer)
	if !ok || e.runtime == nil {
		return StepResult{State: StepUnavailable, ErrorCode: "runtime_unavailable", ErrorMessage: "this runtime cannot keep a release warm"}
	}
	watch, err := e.store.claimRollbackWatch(ctx, execution.Run, execution.ClaimToken, request.WatchID)
	if errors.Is(err, ErrNoRollbackWatch) {
		return StepResult{State: StepFailed, ErrorCode: "warm_release_unavailable", ErrorMessage: warmReason(err)}
	}
	if err != nil {
		return normalizedStepFailure(err)
	}
	_, snapshot, err := e.releaseSnapshot(ctx, watch.WarmReleaseID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	runtime, err := e.store.RuntimeForRelease(ctx, watch.WarmReleaseID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	if err := warmer.ResumeRuntime(ctx, *runtime); err != nil {
		cleanup := context.WithoutCancel(ctx)
		_ = e.removeWarmRuntime(cleanup, *runtime)
		message := fmt.Sprintf("Release #%d's warm container could not be resumed, so release #%d keeps serving.", watch.WarmReleaseNum, watch.ReleaseNumber)
		_ = e.store.failRollbackWatch(cleanup, *watch, execution.Run.ID, message)
		return StepResult{State: StepFailed, ErrorCode: "warm_release_lost", ErrorMessage: message}
	}
	_ = stepLog(execution, "status", fmt.Sprintf("Resumed release #%d's warm %s, paused since the cutover to release #%d",
		watch.WarmReleaseNum, warmContainersLabel(*runtime), watch.ReleaseNumber))
	return StepResult{State: StepPassed, Evidence: mustJSON(map[string]any{
		"releaseId": watch.WarmReleaseID, "runtime": runtime, "target": targetForRuntime(*runtime, snapshot),
		"watchId": watch.ID, "resumed": true,
	})}
}

// warmContainersLabel names the warm release's containers for a transcript:
// the one container's id, or each replica's.
func warmContainersLabel(runtime ReleaseRuntime) string {
	replicas := runtimeReplicas(runtime)
	if len(replicas) == 1 {
		return "container " + shortContainer(replicas[0].ContainerID)
	}
	ids := make([]string, 0, len(replicas))
	for _, replica := range replicas {
		ids = append(ids, shortContainer(replica.ContainerID))
	}
	return fmt.Sprintf("%d replicas %s", len(replicas), strings.Join(ids, ", "))
}

func shortContainer(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// verifyWarmRelease is verify_readiness for an instant rollback: the warm
// release's own readiness checks, against the container that is about to
// serve again. One that fails is not switched to; it is removed, and the
// release it would have replaced keeps serving.
func (e *NormalizedStepExecutor) verifyWarmRelease(ctx context.Context, execution StepExecution, request instantRollback) StepResult {
	watch, failure := e.claimedWatch(ctx, execution.Run, request)
	if failure != nil {
		return *failure
	}
	_, snapshot, err := e.releaseSnapshot(ctx, watch.WarmReleaseID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	runtime, err := e.store.RuntimeForRelease(ctx, watch.WarmReleaseID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	selected := []PlannedCheck{}
	for _, check := range snapshot.Checks {
		if check.Phase == "readiness" && check.Kind != string(CheckPublicRoute) {
			selected = append(selected, check)
		}
	}
	if len(selected) == 0 {
		return StepResult{State: StepSkipped, Evidence: mustJSON(checkStepEvidence{Phase: "readiness", Outcome: HealthDisabled, Checks: []CheckEvidence{}})}
	}
	if e.checks == nil {
		return StepResult{State: StepUnavailable, ErrorCode: "checks_unavailable", ErrorMessage: "runtime health checks are unavailable"}
	}
	checks := e.runReplicaChecks(ctx, execution, *runtime, snapshot, selected, func(check PlannedCheck, replica, replicas int) string {
		if replica == 0 {
			return fmt.Sprintf("Running readiness check %q (%s) against release #%d", check.Name, check.Kind, watch.WarmReleaseNum)
		}
		return fmt.Sprintf("Running readiness check %q (%s) against release #%d's replica %d of %d", check.Name, check.Kind, watch.WarmReleaseNum, replica, replicas)
	})
	outcome := summarizeChecks(checks)
	evidence := checkStepEvidence{Phase: "readiness", Outcome: outcome, Checks: checks}
	if requiredCheckFailed(checks) {
		cleanup := context.WithoutCancel(ctx)
		_ = e.removeWarmRuntime(cleanup, *runtime)
		message := fmt.Sprintf("Release #%d failed its readiness check after resuming, so release #%d keeps serving. %s",
			watch.WarmReleaseNum, watch.ReleaseNumber, checkFailureMessage("readiness", outcome, checks...))
		_ = e.store.failRollbackWatch(cleanup, *watch, execution.Run.ID, message)
		return StepResult{State: StepFailed, ErrorCode: "warm_release_unhealthy", ErrorMessage: message, Evidence: mustJSON(evidence)}
	}
	state := StepPassed
	if outcome == HealthWarning || outcome == HealthUnavailable || outcome == HealthDisabled {
		state = StepWarning
	}
	return StepResult{State: state, Evidence: mustJSON(evidence)}
}

// abandonInstantRollback puts a claimed window back as it was: the warm
// release paused again, the window watching. The release it would have
// replaced never stopped serving.
func (e *NormalizedStepExecutor) abandonInstantRollback(ctx context.Context, watch RollbackWatch, runID int64) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	if runtime, err := e.store.RuntimeForRelease(cleanup, watch.WarmReleaseID); err == nil {
		if warmer, ok := e.runtime.(RuntimeWarmer); ok {
			_ = warmer.PauseRuntime(cleanup, *runtime)
		}
	}
	_ = e.store.returnRollbackWatch(cleanup, watch.ID, runID)
}

func rollbackReasonFor(run EngineRun, request instantRollback) string {
	if request.Reason != "" {
		return request.Reason
	}
	if run.Actor != "" {
		return "Rolled back instantly by " + run.Actor + "."
	}
	return "Rolled back instantly."
}

// activateWarmRelease is activate for an instant rollback: the route goes
// back to the warm release's own container with the warm release's own
// frozen domains and rules, then the pointer moves in one transaction.
func (e *NormalizedStepExecutor) activateWarmRelease(ctx context.Context, execution StepExecution, request instantRollback) StepResult {
	watch, failure := e.claimedWatch(ctx, execution.Run, request)
	if failure != nil {
		return *failure
	}
	release, snapshot, err := e.releaseSnapshot(ctx, watch.WarmReleaseID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	runtime, err := e.store.RuntimeForRelease(ctx, watch.WarmReleaseID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	evidence := activationStepEvidence{ReleaseID: watch.WarmReleaseID, PublicChecks: []CheckEvidence{}}
	fail := func(code, message string, route *proxysvc.DeploymentRouteResult) StepResult {
		if route != nil && e.proxy != nil {
			restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			_ = e.proxy.RestoreDeploymentRoute(restoreCtx, route.Snapshot)
			cancel()
		}
		e.abandonInstantRollback(ctx, *watch, execution.Run.ID)
		return StepResult{State: StepFailed, ErrorCode: code, Evidence: mustJSON(evidence),
			ErrorMessage: fmt.Sprintf("%s Release #%d keeps serving and release #%d stays warm.", message, watch.ReleaseNumber, watch.WarmReleaseNum)}
	}
	var applied *proxysvc.DeploymentRouteResult
	var route *proxysvc.DeploymentRoute
	if len(snapshot.Domains) > 0 {
		if runtime.Port == 0 || e.proxy == nil {
			return fail("proxy_unavailable", "A managed domain needs an available HTTP proxy.", nil)
		}
		value := publicRoute(release.Release.EnvironmentID, snapshot, *runtime)
		if value.TLS {
			resolver, ok := e.proxy.(interface {
				ResolveDeploymentCertificate(context.Context, []string) (string, string, error)
			})
			if !ok {
				return fail("certificate_unavailable", "TLS route activation cannot resolve an existing certificate.", nil)
			}
			certPath, keyPath, resolveErr := resolver.ResolveDeploymentCertificate(ctx, value.TLSHostnames())
			if resolveErr != nil {
				return fail("certificate_unavailable", "No existing certificate covers every HTTPS domain.", nil)
			}
			value.CertPath, value.KeyPath = certPath, keyPath
		}
		route = &value
		result, applyErr := e.proxy.ApplyDeploymentRoute(ctx, value)
		routeEvidence := publicRouteActivationEvidence(result)
		evidence.Route = &routeEvidence
		if applyErr != nil {
			// The proxy restored its own prior route when it could.
			return fail("proxy_cutover_failed", "The proxy refused the route back to the warm release.", nil)
		}
		applied = &result
	}
	for _, check := range snapshot.Checks {
		if check.Phase != "smoke" || check.Kind != string(CheckPublicRoute) || e.checks == nil {
			continue
		}
		target := targetForRuntime(*runtime, snapshot)
		target.PublicURLs = publicRouteURLs(snapshot.Domains)
		evidence.PublicChecks = append(evidence.PublicChecks, e.checks.Run(ctx, check, target))
	}
	if requiredCheckFailed(evidence.PublicChecks) {
		return fail("public_route_failed", "The warm release failed its public-route check.", applied)
	}
	if route != nil {
		if err := e.proxy.VerifyDeploymentRoute(ctx, *route); err != nil {
			return fail("activation_verification_failed", "The route back could not be verified.", applied)
		}
	}
	reason := rollbackReasonFor(execution.Run, request)
	if err := e.store.activateWarmRelease(ctx, execution.Run, execution.ClaimToken, *watch, reason); err != nil {
		return fail("activation_commit_failed", "The release pointer could not be moved back.", applied)
	}
	_ = stepLog(execution, "status", fmt.Sprintf("Release #%d is live again from its warm %s, with its own frozen configuration",
		watch.WarmReleaseNum, warmContainersLabel(*runtime)))
	return StepResult{State: StepPassed, Evidence: mustJSON(evidence)}
}

// retireRolledBackRelease is retire_previous for an instant rollback: the
// release traffic was taken from is drained and removed, and the window is
// over.
func (e *NormalizedStepExecutor) retireRolledBackRelease(ctx context.Context, execution StepExecution, request instantRollback) StepResult {
	watch, failure := e.claimedWatch(ctx, execution.Run, request)
	if failure != nil {
		return *failure
	}
	retireCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 11*time.Minute)
	defer cancel()
	stop, err := e.retireRolledBackRuntime(retireCtx, *watch, func(line BuildLog) error {
		return stepLog(execution, line.Stream, line.Text)
	})
	if err != nil {
		return runtimeStepFailure(err, "retire_failed", fmt.Sprintf("release #%d could not be removed", watch.ReleaseNumber), nil)
	}
	message := fmt.Sprintf("Traffic went back to release #%d. %s", watch.WarmReleaseNum, rollbackReasonFor(execution.Run, request))
	if err := e.store.finishRollbackWatch(retireCtx, *watch, execution.Run.ID, message); err != nil {
		return normalizedStepFailure(err)
	}
	return StepResult{State: StepPassed, Evidence: mustJSON(map[string]any{
		"releaseId": watch.ReleaseID, "stop": stop, "rolledBack": true, "watchId": watch.ID,
	})}
}

func (e *NormalizedStepExecutor) retireRolledBackRuntime(ctx context.Context, watch RollbackWatch, emit func(BuildLog) error) (*RuntimeStopEvidence, error) {
	runtime, err := e.store.RuntimeForRelease(ctx, watch.ReleaseID)
	if errors.Is(err, ErrArtifactMissing) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if runtime.State == "retired" || runtime.State == "stopped" {
		return nil, nil
	}
	_, snapshot, err := e.releaseSnapshot(ctx, watch.ReleaseID)
	if err != nil {
		return nil, err
	}
	if snapshot.Plan.DrainSeconds > 0 {
		timer := time.NewTimer(time.Duration(snapshot.Plan.DrainSeconds) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	stopped, err := e.runtime.Stop(ctx, *runtime, snapshot.Plan, nil, true, emit)
	return &stopped, err
}

// cleanupInstantRollback is cancellation for an instant rollback. Once the
// pointer moved, it finishes what the rollback started; before that, it puts
// the window back and the route on the release the pointer still names.
func (e *NormalizedStepExecutor) cleanupInstantRollback(ctx context.Context, run EngineRun, request instantRollback) (json.RawMessage, error) {
	watch, err := e.store.RollbackWatch(ctx, request.WatchID)
	if err != nil || watch.State != RollbackWatchRollingBack || watch.RollbackRunID != run.ID {
		return mustJSON(map[string]any{"completed": true, "reason": "the rollback never claimed its window"}), nil
	}
	if err := e.settleRollbackWatch(ctx, *watch, run.ID); err != nil {
		return mustJSON(map[string]any{"completed": false, "action": "settle_instant_rollback"}), err
	}
	return mustJSON(map[string]any{"completed": true, "action": "settle_instant_rollback"}), nil
}

// repairRollbackWatch settles a window whose instant rollback ended without
// settling it — a process killed in the middle, which the engine ends as
// restart_evidence_missing.
func (e *NormalizedStepExecutor) repairRollbackWatch(ctx context.Context, watch RollbackWatch) error {
	return e.settleRollbackWatch(ctx, watch, watch.RollbackRunID)
}

// settleRollbackWatch decides from the live pointer, which moves in one
// transaction, what the rollback had done. Pointing at the warm release, it
// committed: the release it replaced is removed and the window is over.
// Still pointing at the watched release, it did not: the route is put back
// on that release, whatever the interrupted step had applied, and the warm
// container is paused again for the window's remaining time.
func (e *NormalizedStepExecutor) settleRollbackWatch(ctx context.Context, watch RollbackWatch, runID int64) error {
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 11*time.Minute)
	defer cancel()
	live, err := e.store.LiveRelease(settleCtx, watch.EnvironmentID)
	if err != nil {
		return err
	}
	switch live.Release.ID {
	case watch.WarmReleaseID:
		if _, err := e.retireRolledBackRuntime(settleCtx, watch, nil); err != nil {
			return err
		}
		return e.store.finishRollbackWatch(settleCtx, watch, runID,
			fmt.Sprintf("Traffic went back to release #%d; the rollback was interrupted after it switched and was finished afterwards.", watch.WarmReleaseNum))
	case watch.ReleaseID:
		if err := e.restoreLiveRoute(settleCtx, watch.ReleaseID); err != nil {
			return err
		}
		e.abandonInstantRollback(settleCtx, watch, runID)
		return nil
	default:
		return e.store.failRollbackWatch(settleCtx, watch, runID, "Another release went live during the rollback.")
	}
}

// restoreLiveRoute applies the route of the release the pointer names, so a
// route an interrupted rollback moved can never outlive the rollback.
func (e *NormalizedStepExecutor) restoreLiveRoute(ctx context.Context, releaseID int64) error {
	release, snapshot, err := e.releaseSnapshot(ctx, releaseID)
	if err != nil {
		return err
	}
	if len(snapshot.Domains) == 0 || e.proxy == nil {
		return nil
	}
	runtime, err := e.store.RuntimeForRelease(ctx, releaseID)
	if err != nil {
		return err
	}
	route := publicRoute(release.Release.EnvironmentID, snapshot, *runtime)
	if route.TLS {
		if resolver, ok := e.proxy.(interface {
			ResolveDeploymentCertificate(context.Context, []string) (string, string, error)
		}); ok {
			certPath, keyPath, err := resolver.ResolveDeploymentCertificate(ctx, route.TLSHostnames())
			if err != nil {
				return err
			}
			route.CertPath, route.KeyPath = certPath, keyPath
		}
	}
	_, err = e.proxy.ApplyDeploymentRoute(ctx, route)
	return err
}
