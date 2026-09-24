package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

type startedStepEvidence struct {
	ReleaseID        int64                `json:"releaseId"`
	Runtime          ReleaseRuntime       `json:"runtime"`
	Target           CheckTarget          `json:"target"`
	PreviousStop     *RuntimeStopEvidence `json:"previousStop,omitempty"`
	ExpectedDowntime bool                 `json:"expectedDowntime"`
}

type checkStepEvidence struct {
	Phase   string          `json:"phase"`
	Outcome HealthOutcome   `json:"outcome"`
	Checks  []CheckEvidence `json:"checks"`
}

type recoveryEvidence struct {
	CandidateStopped bool                 `json:"candidateStopped"`
	RouteRestored    bool                 `json:"routeRestored"`
	RuntimeRestored  bool                 `json:"runtimeRestored"`
	Stop             *RuntimeStopEvidence `json:"stop,omitempty"`
}

type activationStepEvidence struct {
	ReleaseID    int64                    `json:"releaseId"`
	Route        *routeActivationEvidence `json:"route,omitempty"`
	PublicChecks []CheckEvidence          `json:"publicChecks"`
	Recovery     *recoveryEvidence        `json:"recovery,omitempty"`
}

type routeActivationEvidence struct {
	Name         string `json:"name"`
	PriorExisted bool   `json:"priorExisted"`
	PriorDigest  string `json:"priorDigest"`
	Applied      bool   `json:"applied"`
	Recovered    bool   `json:"recovered"`
	Verified     bool   `json:"verified"`
}

type cancellationCleanupOutput struct{}

func (cancellationCleanupOutput) Log(string, string) error { return nil }
func (cancellationCleanupOutput) Event(EventInput) error   { return nil }

func (e *NormalizedStepExecutor) CleanupCancelledRun(
	ctx context.Context,
	run EngineRun,
	claimToken string,
) (json.RawMessage, error) {
	if e == nil || e.store == nil || e.runtime == nil || operationTargetsLiveRelease(run.Operation) {
		return mustJSON(map[string]any{"completed": true, "reason": "no candidate runtime cleanup required"}), nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 11*time.Minute)
	defer cancel()
	release, snapshot, err := e.releaseSnapshotForRun(cleanupCtx, run.ID)
	if errors.Is(err, ErrArtifactMissing) {
		return mustJSON(map[string]any{"completed": true, "reason": "no candidate release exists"}), nil
	}
	if err != nil {
		return nil, err
	}
	execution := StepExecution{Run: run, ClaimToken: claimToken, Output: cancellationCleanupOutput{}}
	if release.Release.State == "live" {
		result := e.retirePrevious(cleanupCtx, execution, nil)
		completed := result.State == StepPassed || result.State == StepSkipped
		evidence := mustJSON(map[string]any{
			"completed": completed, "action": "finish_retirement", "result": result,
		})
		if !completed {
			return evidence, errors.New("post-activation retirement did not complete")
		}
		return evidence, nil
	}
	if release.Release.State != "candidate" {
		return mustJSON(map[string]any{"completed": true, "reason": "release is not active or candidate"}), nil
	}
	runtime, runtimeErr := e.store.RuntimeForRelease(cleanupCtx, release.Release.ID)
	if runtimeErr == nil {
		recovery := e.stopCandidateAndRestore(cleanupCtx, execution, release.Release, *runtime, snapshot.Plan, nil)
		completed := recoveryComplete(recovery, snapshot.Plan, release.Release)
		evidence := mustJSON(map[string]any{
			"completed": completed, "action": "remove_candidate", "recovery": recovery,
		})
		if !completed {
			return evidence, errors.New("candidate cancellation cleanup did not complete")
		}
		return evidence, nil
	}
	if !errors.Is(runtimeErr, ErrArtifactMissing) {
		return nil, runtimeErr
	}

	// A direct container has a deterministic deployment-owned name, allowing
	// cleanup of the narrow crash window between Docker create and runtime-row
	// persistence. A stop-first Compose retry restores the predecessor's exact
	// immutable project spec, replacing any unrecorded candidate in the stable
	// project identity.
	candidateStopped := false
	if snapshot.Compose == nil {
		orphan := ReleaseRuntime{
			ReleaseID: release.Release.ID, EnvironmentID: release.Release.EnvironmentID,
			Kind: "container", RuntimeID: fmt.Sprintf("jd-e%d-r%d", release.Release.EnvironmentID, release.Release.Number),
		}
		variables, _ := e.variablesForScope(cleanupCtx, run.ID, run.EnvironmentID, "runtime")
		_, stopErr := e.runtime.Stop(cleanupCtx, orphan, snapshot.Plan, variables, true, nil)
		candidateStopped = stopErr == nil
	}
	previous := e.restorePrevious(cleanupCtx, execution, release.Release, snapshot.Plan)
	runtimeRestored := previous == nil || previous.RuntimeRestored
	if snapshot.Compose != nil && release.Release.PredecessorReleaseID != 0 && snapshot.Plan.Strategy == StrategyStopFirst {
		candidateStopped = runtimeRestored
	}
	completed := candidateStopped && runtimeRestored
	evidence := mustJSON(map[string]any{
		"completed": completed, "action": "recover_unrecorded_candidate",
		"candidateStopped": candidateStopped, "runtimeRestored": runtimeRestored,
	})
	if !completed {
		return evidence, errors.New("unrecorded candidate cancellation cleanup could not be proven")
	}
	return evidence, nil
}

type BackupGateRequest struct {
	JobID                int64    `json:"jobId"`
	RequiredBeforeDeploy bool     `json:"requiredBeforeDeploy"`
	MaxAgeSeconds        int      `json:"maxAgeSeconds,omitempty"`
	RequireRestoreTest   bool     `json:"requireRestoreTest,omitempty"`
	PersistentSources    []string `json:"persistentSources"`
	DatabaseConnections  []int64  `json:"databaseConnections,omitempty"`
}

type BackupGateEvidence struct {
	JobID         int64      `json:"jobId"`
	RunID         int64      `json:"runId,omitempty"`
	Status        string     `json:"status"`
	StartedAt     time.Time  `json:"startedAt,omitempty"`
	EndedAt       *time.Time `json:"endedAt,omitempty"`
	Fresh         bool       `json:"fresh"`
	RestoreTested bool       `json:"restoreTested"`
	// DatabaseDumps lists linked database connections whose coverage came
	// from a native dump inside the archive rather than from its files.
	DatabaseDumps           []int64 `json:"databaseDumps,omitempty"`
	ManifestDigest          string  `json:"manifestDigest,omitempty"`
	RestoreVerificationID   int64   `json:"restoreVerificationId,omitempty"`
	RestoreApplicationImage string  `json:"restoreApplicationImage,omitempty"`
	RestoreSchemaVersion    string  `json:"restoreSchemaVersion,omitempty"`
	Detail                  string  `json:"detail,omitempty"`
}

type BackupGate interface {
	Evaluate(context.Context, BackupGateRequest) (BackupGateEvidence, error)
}

type backupDependencyConfig struct {
	RequiredBeforeDeploy bool `json:"requiredBeforeDeploy"`
	MaxAgeSeconds        int  `json:"maxAgeSeconds,omitempty"`
	RequireRestoreTest   bool `json:"requireRestoreTest,omitempty"`
}

func decodeBackupDependencyConfig(raw json.RawMessage) (backupDependencyConfig, error) {
	config := backupDependencyConfig{}
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, errors.New("backup dependency configuration has unsupported or malformed fields")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return config, errors.New("backup dependency configuration has trailing data")
	}
	if config.MaxAgeSeconds < 0 || config.MaxAgeSeconds > 365*24*60*60 {
		return config, errors.New("backup maximum age is outside the supported bounds")
	}
	return config, nil
}

func (e *NormalizedStepExecutor) backupGate(ctx context.Context, execution StepExecution, plan *StoredExecutionPlan) StepResult {
	dependencies, _, _, err := decodeStoredPlanInputs(plan)
	if err != nil {
		return normalizedStepFailure(err)
	}
	backupDependencies := []PlannedDependency{}
	for _, dependency := range dependencies {
		if dependency.Kind == "backup" || dependency.ResourceKind == "backup_job" {
			backupDependencies = append(backupDependencies, dependency)
		}
	}
	if len(backupDependencies) == 0 {
		return StepResult{State: StepSkipped, Evidence: mustJSON(map[string]any{"reason": "no backup gate configured"})}
	}
	if e.backups == nil {
		return StepResult{
			State: StepUnavailable, ErrorCode: "backup_unavailable",
			ErrorMessage: "a backup gate is configured, but the Backups feature is unavailable",
		}
	}
	persistent := make([]string, 0, len(plan.Runtime.Mounts))
	var databaseConnections []int64
	for _, dependency := range dependencies {
		if dependency.Kind == "database" || dependency.ResourceKind == "database_connection" {
			id, err := parsePositiveReferenceID(dependency.ResourceID)
			if err != nil {
				return StepResult{State: StepFailed, ErrorCode: "backup_required", ErrorMessage: "a linked database identity could not be resolved for backup coverage"}
			}
			databaseConnections = append(databaseConnections, id)
		}
		if dependency.Kind == "storage" {
			if (dependency.ResourceKind != "docker_volume" && dependency.ResourceKind != "bind_path") || dependency.ResourceID == "" {
				return StepResult{State: StepFailed, ErrorCode: "backup_required", ErrorMessage: "a linked storage identity could not be resolved for backup coverage"}
			}
			persistent = append(persistent, dependency.ResourceID)
		}
	}
	for _, mount := range plan.Runtime.Mounts {
		if !mount.ReadOnly {
			persistent = append(persistent, mount.Source)
		}
	}
	if plan.Build.Method == BuildCompose || plan.BuildEvidence.Compose != nil {
		sources, err := e.composeBackupSources(ctx, execution, plan)
		if err != nil {
			return StepResult{State: StepFailed, ErrorCode: "backup_required",
				ErrorMessage: "Compose persistent storage could not be resolved for backup coverage"}
		}
		persistent = append(persistent, sources...)
	}
	persistent = uniqueSorted(persistent)
	evidence := make([]BackupGateEvidence, 0, len(backupDependencies))
	for _, dependency := range backupDependencies {
		jobID, parseErr := parsePositiveReferenceID(dependency.ResourceID)
		if parseErr != nil {
			return StepResult{State: StepFailed, ErrorCode: "backup_required", ErrorMessage: "the backup policy does not name a valid backup job"}
		}
		config, configErr := decodeBackupDependencyConfig(dependency.Config)
		if configErr != nil {
			return StepResult{State: StepFailed, ErrorCode: "backup_required", ErrorMessage: "the backup policy configuration is invalid"}
		}
		observed, gateErr := e.backups.Evaluate(ctx, BackupGateRequest{
			JobID: jobID, RequiredBeforeDeploy: config.RequiredBeforeDeploy,
			MaxAgeSeconds: config.MaxAgeSeconds, RequireRestoreTest: config.RequireRestoreTest,
			PersistentSources:   append([]string(nil), persistent...),
			DatabaseConnections: append([]int64(nil), databaseConnections...),
		})
		evidence = append(evidence, observed)
		if gateErr != nil {
			return StepResult{
				State: StepFailed, ErrorCode: "backup_required",
				ErrorMessage: "the required backup did not complete successfully",
				Evidence:     mustJSON(map[string]any{"backups": evidence}),
			}
		}
		if config.RequireRestoreTest && !observed.RestoreTested {
			return StepResult{
				State: StepFailed, ErrorCode: "backup_required",
				ErrorMessage: "the backup policy requires restore-test evidence",
				Evidence:     mustJSON(map[string]any{"backups": evidence}),
			}
		}
		if config.MaxAgeSeconds > 0 && !observed.Fresh {
			return StepResult{
				State: StepFailed, ErrorCode: "backup_stale",
				ErrorMessage: "the latest successful backup is older than the allowed age",
				Evidence:     mustJSON(map[string]any{"backups": evidence}),
			}
		}
	}
	return StepResult{State: StepPassed, Evidence: mustJSON(map[string]any{"backups": evidence})}
}

func (e *NormalizedStepExecutor) composeBackupSources(ctx context.Context, execution StepExecution, plan *StoredExecutionPlan) ([]string, error) {
	owner, ok := e.runtime.(RuntimeStorageOwner)
	if !ok {
		return nil, ErrRuntimeUnavailable
	}
	release, snapshot, err := e.releaseSnapshotForExecution(ctx, execution.Run)
	if err != nil {
		return nil, err
	}
	source, _, err := e.materializedBuildRoot(ctx, execution.Run, plan)
	if err != nil {
		return nil, err
	}
	variables, err := e.variablesForScope(ctx, execution.Run.ID, execution.Run.EnvironmentID, "runtime")
	if err != nil {
		return nil, err
	}
	return owner.PersistentSources(ctx, CandidateRuntimeRequest{
		Run: execution.Run, Release: release.Release, Snapshot: snapshot,
		SourceRoot: source.Root, RuntimeVariables: variables,
	})
}

func (e *NormalizedStepExecutor) startCandidate(
	ctx context.Context,
	execution StepExecution,
	plan *StoredExecutionPlan,
) StepResult {
	if e.runtime == nil {
		return StepResult{State: StepUnavailable, ErrorCode: "runtime_unavailable", ErrorMessage: "Docker runtime activation is unavailable"}
	}
	switch execution.Run.Operation {
	case OperationRestart:
		return e.restartLiveRuntime(ctx, execution)
	case OperationStop:
		return e.stopLiveRuntime(ctx, execution)
	case OperationStart:
		return e.startLiveRuntime(ctx, execution)
	}
	release, snapshot, err := e.releaseSnapshotForExecution(ctx, execution.Run)
	if err != nil {
		return normalizedStepFailure(err)
	}
	if existing, runtimeErr := e.store.RuntimeForRelease(ctx, release.Release.ID); runtimeErr == nil {
		if existing.State == "failed" || existing.State == "stopped" || existing.State == "retired" {
			return StepResult{State: StepFailed, ErrorCode: "candidate_runtime_failed", ErrorMessage: "the recorded candidate runtime is not active"}
		}
		return StepResult{State: StepPassed, Evidence: mustJSON(startedStepEvidence{
			ReleaseID: release.Release.ID, Runtime: *existing, Target: targetForRuntime(*existing, snapshot),
			ExpectedDowntime: release.Release.ExpectedDowntime,
		})}
	} else if !errors.Is(runtimeErr, ErrArtifactMissing) {
		return normalizedStepFailure(runtimeErr)
	}
	if err := validateRuntimeActivationStrategy(snapshot); err != nil {
		return normalizedStepFailure(err)
	}

	var previousStop *RuntimeStopEvidence
	if snapshot.Plan.Strategy == StrategyStopFirst && release.Release.PredecessorReleaseID != 0 {
		previous, runtimeErr := e.store.RuntimeForRelease(ctx, release.Release.PredecessorReleaseID)
		if runtimeErr != nil {
			return StepResult{State: StepUnavailable, ErrorCode: "previous_runtime_unavailable", ErrorMessage: "the live release has no runtime ownership record; stop-first cannot proceed safely"}
		}
		previousVariables, variablesErr := e.runtimeVariablesForRelease(ctx, release.Release.PredecessorReleaseID, *previous)
		if variablesErr != nil {
			return runtimeStepFailure(variablesErr, "previous_variables_unavailable", "the live Compose release variables could not be opened", nil)
		}
		stopped, stopErr := e.runtime.Stop(ctx, *previous, snapshot.Plan, previousVariables, false,
			func(line BuildLog) error { return stepLog(execution, line.Stream, line.Text) })
		previousStop = &stopped
		if stopErr != nil {
			recovery := e.restorePrevious(ctx, execution, release.Release, snapshot.Plan)
			return runtimeStepFailure(stopErr, "previous_stop_failed", "the live release could not be stopped safely", recovery)
		}
		if err := e.store.SetRuntimeState(ctx, execution.Run.ID, execution.ClaimToken, previous.ReleaseID, "stopped"); err != nil {
			recovery := e.restorePrevious(ctx, execution, release.Release, snapshot.Plan)
			return runtimeStepFailure(err, "previous_state_failed", "the stopped live release could not be recorded", recovery)
		}
		_ = stepLog(execution, "status", "Stopped the previous release before creating the exclusive candidate")
	}

	host, port, leaseToken, err := e.candidateAddress(ctx, execution, snapshot.Plan)
	if err != nil {
		recovery := e.restorePrevious(ctx, execution, release.Release, snapshot.Plan)
		return runtimeStepFailure(err, "port_unavailable", "a candidate port could not be reserved", recovery)
	}
	if leaseToken != "" {
		defer e.store.ReleasePortLease(context.WithoutCancel(ctx), execution.Run.ID, leaseToken)
	}
	variables, err := e.variablesForScope(ctx, execution.Run.ID, execution.Run.EnvironmentID, "runtime")
	if err != nil {
		recovery := e.restorePrevious(ctx, execution, release.Release, snapshot.Plan)
		return runtimeStepFailure(err, "runtime_variables_unavailable", "the frozen runtime variables could not be opened", recovery)
	}
	sourceRoot := ""
	if snapshot.Compose != nil {
		source, _, materializeErr := e.materializedBuildRoot(ctx, execution.Run, plan)
		if materializeErr != nil {
			recovery := e.restorePrevious(ctx, execution, release.Release, snapshot.Plan)
			return runtimeStepFailure(materializeErr, "source_unavailable", "the Compose release source could not be rematerialized", recovery)
		}
		sourceRoot = source.Root
	}
	started, err := e.runtime.StartCandidate(ctx, CandidateRuntimeRequest{
		Run: execution.Run, Release: release.Release, Snapshot: snapshot, SourceRoot: sourceRoot,
		RuntimeVariables: variables, Host: host, Port: port, PortLeaseToken: leaseToken,
	}, func(line BuildLog) error { return stepLog(execution, line.Stream, line.Text) })
	if err != nil {
		recovery := e.restorePrevious(ctx, execution, release.Release, snapshot.Plan)
		code, message := candidateStartFailure(err)
		return runtimeStepFailure(err, code, message, recovery)
	}
	runtime, err := e.store.RecordCandidateRuntime(ctx, execution.Run, execution.ClaimToken, started.Input)
	if err != nil {
		temporary := runtimeFromInput(started.Input)
		recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(runtimeGrace(snapshot.Plan)+30)*time.Second)
		_, _ = e.runtime.Stop(recoveryCtx, temporary, snapshot.Plan, variables, true, nil)
		cancel()
		recovery := e.restorePrevious(ctx, execution, release.Release, snapshot.Plan)
		return runtimeStepFailure(err, "runtime_record_failed", "the candidate started but its identity could not be recorded", recovery)
	}
	_ = stepLog(execution, "status", fmt.Sprintf("Started immutable candidate release #%d", release.Release.Number))
	return StepResult{State: StepPassed, Evidence: mustJSON(startedStepEvidence{
		ReleaseID: release.Release.ID, Runtime: *runtime, Target: started.Target,
		PreviousStop: previousStop, ExpectedDowntime: release.Release.ExpectedDowntime,
	})}
}

func validateRuntimeActivationStrategy(snapshot runtimeReleaseSnapshot) error {
	plan := snapshot.Plan
	if plan.Strategy != StrategyBlueGreen {
		return nil
	}
	if snapshot.Compose != nil || plan.HostPort != 0 || len(plan.Ports) != 0 || plan.HostNetwork {
		return fmt.Errorf("%w: this runtime is not eligible for concurrent candidate activation", ErrInvalidPlan)
	}
	for _, mount := range plan.Mounts {
		if !mount.ReadOnly {
			return fmt.Errorf("%w: a writable mount prevents concurrent candidate activation", ErrInvalidPlan)
		}
	}
	return nil
}

func (e *NormalizedStepExecutor) candidateAddress(
	ctx context.Context,
	execution StepExecution,
	plan RuntimePlanConfig,
) (string, int, string, error) {
	host := plan.BindAddress
	if host == "" {
		host = "127.0.0.1"
	}
	if plan.HostNetwork {
		return runtimeCheckHost(host), plan.InternalPort, "", nil
	}
	if plan.HostPort > 0 {
		// A fixed-port stop-first plan may deliberately publish on every
		// interface. Preserve that reviewed choice in the Docker binding; only
		// probes and proxy upstreams translate wildcard binds to loopback.
		return host, plan.HostPort, "", nil
	}
	if plan.InternalPort == 0 {
		return runtimeCheckHost(host), 0, "", nil
	}
	host = runtimeCheckHost(host)
	lease, err := e.store.AcquirePortLease(ctx, execution.Run.ID, execution.Run.EnvironmentID,
		execution.ClaimToken, host, 0, 30*time.Minute)
	if err != nil {
		return "", 0, "", err
	}
	return lease.Address, lease.Port, lease.Token, nil
}

func (e *NormalizedStepExecutor) verifyChecks(
	ctx context.Context,
	execution StepExecution,
	plan *StoredExecutionPlan,
	phase string,
) StepResult {
	// A restart owns no release of its own; its checks run against the live
	// release it stopped and started. Looking the release up by run id here
	// used to fail every restart with artifact_missing right after the runtime
	// had been restarted.
	release, snapshot, err := e.releaseSnapshotForExecution(ctx, execution.Run)
	if err != nil {
		return normalizedStepFailure(err)
	}
	runtime, err := e.store.RuntimeForRelease(ctx, release.Release.ID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	selected := []PlannedCheck{}
	for _, check := range snapshot.Checks {
		if check.Phase == phase && check.Kind != string(CheckPublicRoute) {
			selected = append(selected, check)
		}
	}
	if len(selected) == 0 {
		if phase == "readiness" && !operationTargetsLiveRelease(execution.Run.Operation) {
			_ = e.store.SetRuntimeState(ctx, execution.Run.ID, execution.ClaimToken, release.Release.ID, "ready")
		}
		return StepResult{State: StepSkipped, Evidence: mustJSON(checkStepEvidence{
			Phase: phase, Outcome: HealthDisabled, Checks: []CheckEvidence{},
		})}
	}
	if e.checks == nil {
		return StepResult{State: StepUnavailable, ErrorCode: "checks_unavailable", ErrorMessage: "runtime health checks are unavailable"}
	}
	target := targetForRuntime(*runtime, snapshot)
	checks := make([]CheckEvidence, 0, len(selected))
	for _, check := range selected {
		_ = stepLog(execution, "status", fmt.Sprintf("Running %s check %q (%s)", phase, check.Name, check.Kind))
		checks = append(checks, e.checks.Run(ctx, check, target))
	}
	outcome := summarizeChecks(checks)
	evidence := checkStepEvidence{Phase: phase, Outcome: outcome, Checks: checks}
	if ctx.Err() != nil || requiredCheckFailed(checks) {
		_ = stepLog(execution, "stderr", checkFailureMessage(phase, outcome, checks...))
		// Read the candidate's own account of itself before compensation
		// removes it. This is the difference between "could not connect" and
		// "Error: DATABASE_URL is not set".
		diagnostics := e.captureRuntimeDiagnostics(ctx, execution, release.Release, *runtime, runtimeCauseContext{
			build: plan.Build, runtime: snapshot.Plan, variables: snapshot.Variables, compose: snapshot.Compose != nil,
		})
		message := checkFailureMessage(phase, outcome, checks...) + diagnosticsSuffix(diagnostics)
		// A cause the output proves is the run's terminal code, so every
		// surface that reads the code names it; the health evidence stays.
		failedCode := "health_gate_failed"
		if diagnostics != nil && diagnostics.Cause != nil {
			failedCode = diagnostics.Cause.Code
		}
		if operationTargetsLiveRelease(execution.Run.Operation) {
			state, code := StepFailed, failedCode
			if ctx.Err() != nil {
				state, code, message = StepCancelled, "cancelled", "health verification was cancelled"
			}
			return StepResult{State: state, ErrorCode: code, ErrorMessage: message,
				Evidence: mustJSON(map[string]any{"health": evidence, "diagnostics": diagnostics})}
		}
		recovery := e.stopCandidateAndRestore(ctx, execution, release.Release, *runtime, snapshot.Plan, nil)
		state, code := StepFailed, failedCode
		if ctx.Err() != nil {
			state, code, message = StepCancelled, "cancelled", "health verification was cancelled"
		}
		return StepResult{State: state, ErrorCode: code, ErrorMessage: message,
			Evidence: mustJSON(map[string]any{"health": evidence, "recovery": recovery, "diagnostics": diagnostics})}
	}
	if phase == "readiness" && !operationTargetsLiveRelease(execution.Run.Operation) {
		if err := e.store.SetRuntimeState(ctx, execution.Run.ID, execution.ClaimToken, release.Release.ID, "ready"); err != nil {
			return normalizedStepFailure(err)
		}
	}
	state := StepPassed
	if outcome == HealthWarning || outcome == HealthUnavailable || outcome == HealthDisabled {
		state = StepWarning
	}
	return StepResult{State: state, Evidence: mustJSON(evidence)}
}

func requiredCheckFailed(checks []CheckEvidence) bool {
	for _, check := range checks {
		if check.Required && check.Outcome != HealthPassed {
			return true
		}
	}
	return false
}

func (e *NormalizedStepExecutor) activate(
	ctx context.Context,
	execution StepExecution,
	_ *StoredExecutionPlan,
) StepResult {
	release, snapshot, err := e.releaseSnapshotForRun(ctx, execution.Run.ID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	runtime, err := e.store.RuntimeForRelease(ctx, release.Release.ID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	evidence := activationStepEvidence{ReleaseID: release.Release.ID, PublicChecks: []CheckEvidence{}}
	var route *proxysvc.DeploymentRoute
	var appliedRoute *proxysvc.DeploymentRouteResult
	if len(snapshot.Domains) > 0 {
		if runtime.Port == 0 || e.proxy == nil {
			recovery := e.stopCandidateAndRestore(ctx, execution, release.Release, *runtime, snapshot.Plan, nil)
			evidence.Recovery = &recovery
			return StepResult{State: StepUnavailable, ErrorCode: "proxy_unavailable", ErrorMessage: "a managed domain requires an available HTTP proxy", Evidence: mustJSON(evidence), Recovered: recoveryComplete(recovery, snapshot.Plan, release.Release)}
		}
		routeValue := deploymentRoute(release.Release.EnvironmentID, snapshot.Domains, runtime.Host, runtime.Port)
		if routeValue.TLS {
			resolver, ok := e.proxy.(interface {
				ResolveDeploymentCertificate(context.Context, []string) (string, string, error)
			})
			if !ok {
				recovery := e.stopCandidateAndRestore(ctx, execution, release.Release, *runtime, snapshot.Plan, nil)
				evidence.Recovery = &recovery
				return StepResult{State: StepUnavailable, ErrorCode: "certificate_unavailable", ErrorMessage: "TLS route activation cannot resolve an existing certificate", Evidence: mustJSON(evidence), Recovered: recoveryComplete(recovery, snapshot.Plan, release.Release)}
			}
			certPath, keyPath, resolveErr := resolver.ResolveDeploymentCertificate(ctx, routeValue.Domains)
			if resolveErr != nil {
				recovery := e.stopCandidateAndRestore(ctx, execution, release.Release, *runtime, snapshot.Plan, nil)
				evidence.Recovery = &recovery
				return StepResult{State: StepUnavailable, ErrorCode: "certificate_unavailable", ErrorMessage: "no existing certificate covers every HTTPS domain", Evidence: mustJSON(evidence), Recovered: recoveryComplete(recovery, snapshot.Plan, release.Release)}
			}
			routeValue.CertPath, routeValue.KeyPath = certPath, keyPath
		}
		route = &routeValue
		applied, applyErr := e.proxy.ApplyDeploymentRoute(ctx, routeValue)
		appliedRoute = &applied
		routeEvidence := publicRouteActivationEvidence(applied)
		evidence.Route = &routeEvidence
		if applyErr != nil {
			recovery := e.stopCandidateAndRestore(ctx, execution, release.Release, *runtime, snapshot.Plan, nil)
			recovery.RouteRestored = applied.Recovered
			evidence.Recovery = &recovery
			return StepResult{State: StepFailed, ErrorCode: "proxy_cutover_failed", ErrorMessage: "the proxy rejected the candidate and the prior route recovery was checked", Evidence: mustJSON(evidence), Recovered: recoveryComplete(recovery, snapshot.Plan, release.Release)}
		}
	}

	publicChecks := []PlannedCheck{}
	for _, check := range snapshot.Checks {
		if check.Phase == "smoke" && check.Kind == string(CheckPublicRoute) {
			publicChecks = append(publicChecks, check)
		}
	}
	if len(publicChecks) > 0 {
		if e.checks == nil {
			recovery := e.stopCandidateAndRestore(ctx, execution, release.Release, *runtime, snapshot.Plan, appliedRoute)
			evidence.Recovery = &recovery
			return StepResult{State: StepUnavailable, ErrorCode: "checks_unavailable", ErrorMessage: "public-route verification is unavailable", Evidence: mustJSON(evidence), Recovered: recoveryComplete(recovery, snapshot.Plan, release.Release)}
		}
		target := targetForRuntime(*runtime, snapshot)
		target.PublicURLs = publicRouteURLs(snapshot.Domains)
		for _, check := range publicChecks {
			evidence.PublicChecks = append(evidence.PublicChecks, e.checks.Run(ctx, check, target))
		}
		if requiredCheckFailed(evidence.PublicChecks) {
			recovery := e.stopCandidateAndRestore(ctx, execution, release.Release, *runtime, snapshot.Plan, appliedRoute)
			evidence.Recovery = &recovery
			return StepResult{State: StepFailed, ErrorCode: "public_route_failed", ErrorMessage: "the candidate failed public-route verification", Evidence: mustJSON(evidence), Recovered: recoveryComplete(recovery, snapshot.Plan, release.Release)}
		}
	}
	if route != nil {
		if err := e.proxy.VerifyDeploymentRoute(ctx, *route); err != nil {
			recovery := e.stopCandidateAndRestore(ctx, execution, release.Release, *runtime, snapshot.Plan, appliedRoute)
			evidence.Recovery = &recovery
			return StepResult{State: StepFailed, ErrorCode: "activation_verification_failed", ErrorMessage: "the proxy cutover could not be verified", Evidence: mustJSON(evidence), Recovered: recoveryComplete(recovery, snapshot.Plan, release.Release)}
		}
	}
	if _, err := e.store.ActivateCandidate(ctx, execution.Run.ID, execution.ClaimToken, release.Release.ID); err != nil {
		recovery := e.stopCandidateAndRestore(ctx, execution, release.Release, *runtime, snapshot.Plan, appliedRoute)
		evidence.Recovery = &recovery
		return StepResult{State: StepFailed, ErrorCode: "activation_commit_failed", ErrorMessage: "the release pointer could not be committed", Evidence: mustJSON(evidence), Recovered: recoveryComplete(recovery, snapshot.Plan, release.Release)}
	}
	_ = stepLog(execution, "status", fmt.Sprintf("Activated immutable release #%d", release.Release.Number))
	return StepResult{State: StepPassed, Evidence: mustJSON(evidence)}
}

func publicRouteActivationEvidence(result proxysvc.DeploymentRouteResult) routeActivationEvidence {
	return routeActivationEvidence{
		Name: result.Snapshot.Name, PriorExisted: result.Snapshot.Existed,
		PriorDigest: result.Snapshot.ContentDigest, Applied: result.Applied,
		Recovered: result.Recovered, Verified: result.Verified,
	}
}

func deploymentRoute(environmentID int64, domains []PlannedDomain, host string, port int) proxysvc.DeploymentRoute {
	names := make([]string, 0, len(domains))
	tls := false
	// A protected domain protects the deployment's whole route: the proxy
	// serves every domain from one site, so the credentials are its union.
	var users []proxysvc.BasicAuthUser
	seen := map[string]bool{}
	for _, domain := range domains {
		names = append(names, strings.ToLower(domain.Hostname))
		tls = tls || domain.HTTPS
		if domain.Protection != nil && !seen[domain.Protection.Username] {
			seen[domain.Protection.Username] = true
			users = append(users, proxysvc.BasicAuthUser{Username: domain.Protection.Username, Hash: domain.Protection.Hash})
		}
	}
	return proxysvc.DeploymentRoute{
		Name: deploymentRouteName(environmentID), Domains: names,
		Upstream: "http://" + net.JoinHostPort(runtimeCheckHost(host), fmt.Sprintf("%d", port)),
		TLS:      tls, ForceHTTPS: tls, BasicAuth: users,
		// The request record is what the project's Logs page reads. nginx has
		// always written one for these routes; asking Caddy for it too is what
		// makes the two drivers answer the same question.
		AccessLog: true,
	}
}

func publicRouteURLs(domains []PlannedDomain) []string {
	urls := make([]string, 0, len(domains))
	for _, domain := range domains {
		scheme := "http"
		if domain.HTTPS {
			scheme = "https"
		}
		urls = append(urls, scheme+"://"+domain.Hostname+"/")
	}
	return urls
}

func (e *NormalizedStepExecutor) stopCandidateAndRestore(
	ctx context.Context,
	execution StepExecution,
	release Release,
	runtime ReleaseRuntime,
	plan RuntimePlanConfig,
	route *proxysvc.DeploymentRouteResult,
) recoveryEvidence {
	recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(runtimeGrace(plan)+45)*time.Second)
	defer cancel()
	variables, _ := e.runtimeVariablesForRelease(recoveryCtx, release.ID, runtime)
	stop, stopErr := e.runtime.Stop(recoveryCtx, runtime, plan, variables, true, nil)
	recovery := recoveryEvidence{CandidateStopped: stopErr == nil, Stop: &stop, RouteRestored: route == nil}
	if route != nil && e.proxy != nil {
		recovery.RouteRestored = e.proxy.RestoreDeploymentRoute(recoveryCtx, route.Snapshot) == nil
	}
	previous := e.restorePrevious(recoveryCtx, execution, release, plan)
	if previous == nil {
		recovery.RuntimeRestored = release.PredecessorReleaseID == 0 || plan.Strategy == StrategyBlueGreen
	} else {
		recovery.RuntimeRestored = previous.RuntimeRestored
	}
	_ = e.store.SetRuntimeState(recoveryCtx, execution.Run.ID, execution.ClaimToken, release.ID, "failed")
	return recovery
}

func (e *NormalizedStepExecutor) restorePrevious(
	ctx context.Context,
	execution StepExecution,
	release Release,
	plan RuntimePlanConfig,
) *recoveryEvidence {
	if release.PredecessorReleaseID == 0 || plan.Strategy != StrategyStopFirst {
		return nil
	}
	recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(runtimeGrace(plan)+30)*time.Second)
	defer cancel()
	recovery := &recoveryEvidence{RouteRestored: true}
	previousRuntime, err := e.store.RuntimeForRelease(recoveryCtx, release.PredecessorReleaseID)
	if err != nil {
		return recovery
	}
	variables := map[string]string{}
	if previousRuntime.Kind == "compose" {
		previousRelease, releaseErr := e.store.Release(recoveryCtx, release.PredecessorReleaseID)
		if releaseErr != nil {
			return recovery
		}
		variables, err = e.variablesForScope(recoveryCtx, previousRelease.Release.RunID, previousRelease.Release.EnvironmentID, "runtime")
		if err != nil {
			return recovery
		}
	}
	if err := e.runtime.StartExisting(recoveryCtx, *previousRuntime, variables,
		func(line BuildLog) error { return stepLog(execution, line.Stream, line.Text) }); err != nil {
		return recovery
	}
	if err := e.store.SetRuntimeState(recoveryCtx, execution.Run.ID, execution.ClaimToken, previousRuntime.ReleaseID, "live"); err != nil {
		return recovery
	}
	recovery.RuntimeRestored = true
	return recovery
}

func recoveryComplete(recovery recoveryEvidence, plan RuntimePlanConfig, release Release) bool {
	runtimeOK := recovery.RuntimeRestored || release.PredecessorReleaseID == 0 || plan.Strategy == StrategyBlueGreen
	return recovery.CandidateStopped && recovery.RouteRestored && runtimeOK
}

func (e *NormalizedStepExecutor) retirePrevious(
	ctx context.Context,
	execution StepExecution,
	_ *StoredExecutionPlan,
) StepResult {
	// Once the environment pointer has moved, cancellation must not strand the
	// predecessor beside the new live runtime. Finish the bounded drain and
	// retirement independently of the request context.
	retireCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 11*time.Minute)
	defer cancel()
	release, snapshot, err := e.releaseSnapshotForRun(retireCtx, execution.Run.ID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	predecessorID := release.Release.PredecessorReleaseID
	if predecessorID == 0 {
		return StepResult{State: StepSkipped, Evidence: mustJSON(map[string]any{"reason": "first release has no predecessor"})}
	}
	previous, err := e.store.RuntimeForRelease(retireCtx, predecessorID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	current, err := e.store.RuntimeForRelease(retireCtx, release.Release.ID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	var stop *RuntimeStopEvidence
	// A stop-first Compose release reuses one stable project identity. Its old
	// containers were already replaced by `up`; stopping that identity here
	// would stop the newly activated release.
	sharedComposeProject := previous.Kind == "compose" && current.Kind == "compose" && previous.RuntimeID == current.RuntimeID
	if !sharedComposeProject {
		if snapshot.Plan.DrainSeconds > 0 {
			timer := time.NewTimer(time.Duration(snapshot.Plan.DrainSeconds) * time.Second)
			select {
			case <-retireCtx.Done():
				timer.Stop()
				return runtimeStepFailure(retireCtx.Err(), "drain_timeout", "the prior release drain did not complete", nil)
			case <-timer.C:
			}
		}
		variables, variablesErr := e.runtimeVariablesForRelease(retireCtx, predecessorID, *previous)
		if variablesErr != nil {
			return runtimeStepFailure(variablesErr, "retire_variables_unavailable", "the prior Compose release variables could not be opened", nil)
		}
		stopped, stopErr := e.runtime.Stop(retireCtx, *previous, snapshot.Plan, variables, true,
			func(line BuildLog) error { return stepLog(execution, line.Stream, line.Text) })
		stop = &stopped
		if stopErr != nil {
			return runtimeStepFailure(stopErr, "retire_failed", "the prior release could not be retired", nil)
		}
	}
	retiredID, err := e.store.MarkPreviousRetired(retireCtx, execution.Run.ID, execution.ClaimToken, release.Release.ID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	return StepResult{State: StepPassed, Evidence: mustJSON(map[string]any{
		"releaseId": retiredID, "stop": stop, "sharedComposeProject": sharedComposeProject,
	})}
}

func (e *NormalizedStepExecutor) removePreview(ctx context.Context, execution StepExecution) StepResult {
	var kind EnvironmentKind
	if err := e.store.db.QueryRowContext(ctx, `SELECT kind FROM deploy_environments WHERE id=?`, execution.Run.EnvironmentID).Scan(&kind); err != nil {
		return normalizedStepFailure(err)
	}
	if kind != EnvironmentPreview || execution.Run.Operation != OperationPreviewRemove {
		return normalizedStepFailure(ErrPreviewIsolation)
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 11*time.Minute)
	defer cancel()
	removeResources := func() error {
		owner, ok := e.runtime.(PreviewResourceOwner)
		if !ok {
			return ErrRuntimeUnavailable
		}
		return owner.RemovePreviewResources(cleanupCtx, execution.Run.EnvironmentID)
	}
	removeRoute := func() error {
		if remover, ok := e.proxy.(interface {
			RemoveDeploymentRoute(context.Context, string) error
		}); ok {
			return remover.RemoveDeploymentRoute(cleanupCtx, deploymentRouteName(execution.Run.EnvironmentID))
		}
		return nil
	}
	live, err := e.store.LiveRelease(cleanupCtx, execution.Run.EnvironmentID)
	if errors.Is(err, ErrArtifactMissing) {
		if err := removeRoute(); err != nil {
			return normalizedStepFailure(err)
		}
		if err := removeResources(); err != nil {
			return normalizedStepFailure(err)
		}
		if err := e.store.CompletePreviewRemoval(cleanupCtx, execution.Run.ID, execution.ClaimToken, 0); err != nil {
			return normalizedStepFailure(err)
		}
		return StepResult{State: StepSkipped, Evidence: mustJSON(map[string]any{"reason": "preview had no live release", "routeRemoved": true})}
	}
	if err != nil {
		return normalizedStepFailure(err)
	}
	_, snapshot, err := e.releaseSnapshot(cleanupCtx, live.Release.ID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	runtime, err := e.store.RuntimeForRelease(cleanupCtx, live.Release.ID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	variables, err := e.runtimeVariablesForRelease(cleanupCtx, live.Release.ID, *runtime)
	if err != nil {
		return normalizedStepFailure(err)
	}
	stopped, err := e.runtime.Stop(cleanupCtx, *runtime, snapshot.Plan, variables, true,
		func(line BuildLog) error { return stepLog(execution, line.Stream, line.Text) })
	if err != nil {
		return runtimeStepFailure(err, "preview_cleanup_failed", "the preview runtime could not be removed", nil)
	}
	if err = removeRoute(); err != nil {
		return normalizedStepFailure(err)
	}
	if err := removeResources(); err != nil {
		return normalizedStepFailure(err)
	}
	if err = e.store.CompletePreviewRemoval(cleanupCtx, execution.Run.ID, execution.ClaimToken, live.Release.ID); err != nil {
		return normalizedStepFailure(err)
	}
	return StepResult{State: StepPassed, Evidence: mustJSON(map[string]any{"releaseId": live.Release.ID, "runtimeId": runtime.RuntimeID, "stop": stopped, "routeRemoved": true})}
}

func (e *NormalizedStepExecutor) recordRelease(ctx context.Context, execution StepExecution) StepResult {
	var release *ReleaseWithArtifacts
	var err error
	if operationTargetsLiveRelease(execution.Run.Operation) {
		targetReleaseID, targetErr := operationTargetReleaseID(execution.Run)
		if targetErr != nil {
			return normalizedStepFailure(targetErr)
		}
		release, err = e.store.Release(ctx, targetReleaseID)
	} else {
		release, err = e.store.ReleaseForRun(ctx, execution.Run.ID)
	}
	if err != nil {
		return normalizedStepFailure(err)
	}
	if operationTargetsLiveRelease(execution.Run.Operation) {
		if err := e.store.LinkRunToLiveRelease(ctx, execution.Run.ID, execution.ClaimToken, release.Release.ID); err != nil {
			return normalizedStepFailure(err)
		}
	}
	live, err := e.store.LiveRelease(ctx, execution.Run.EnvironmentID)
	if err != nil || live.Release.ID != release.Release.ID || release.Release.State != "live" {
		return StepResult{State: StepFailed, ErrorCode: "release_pointer_mismatch", ErrorMessage: "the immutable release and live environment pointer do not agree"}
	}
	// Stop is the one operation that leaves its own runtime stopped on
	// success; every other operation that reaches this step demands live.
	wantRuntimeState := "live"
	if execution.Run.Operation == OperationStop {
		wantRuntimeState = "stopped"
	}
	runtime, err := e.store.RuntimeForRelease(ctx, release.Release.ID)
	if err != nil || runtime.State != wantRuntimeState {
		return StepResult{State: StepFailed, ErrorCode: "runtime_pointer_mismatch", ErrorMessage: "the release runtime is not recorded as " + wantRuntimeState}
	}
	return StepResult{State: StepPassed, Evidence: mustJSON(map[string]any{
		"releaseId": release.Release.ID, "releaseNumber": release.Release.Number,
		"configDigest": release.Release.ConfigDigest, "runtimeId": runtime.RuntimeID,
	})}
}

func (e *NormalizedStepExecutor) releaseSnapshotForExecution(
	ctx context.Context,
	run EngineRun,
) (*ReleaseWithArtifacts, runtimeReleaseSnapshot, error) {
	if !operationTargetsLiveRelease(run.Operation) {
		return e.releaseSnapshotForRun(ctx, run.ID)
	}
	targetReleaseID, err := operationTargetReleaseID(run)
	if err != nil {
		return nil, runtimeReleaseSnapshot{}, err
	}
	return e.releaseSnapshot(ctx, targetReleaseID)
}

func (e *NormalizedStepExecutor) releaseSnapshotForRun(
	ctx context.Context,
	runID int64,
) (*ReleaseWithArtifacts, runtimeReleaseSnapshot, error) {
	release, err := e.store.ReleaseForRun(ctx, runID)
	if err != nil {
		return nil, runtimeReleaseSnapshot{}, err
	}
	return e.decodeReleaseSnapshot(release)
}

func (e *NormalizedStepExecutor) releaseSnapshot(
	ctx context.Context,
	releaseID int64,
) (*ReleaseWithArtifacts, runtimeReleaseSnapshot, error) {
	release, err := e.store.Release(ctx, releaseID)
	if err != nil {
		return nil, runtimeReleaseSnapshot{}, err
	}
	return e.decodeReleaseSnapshot(release)
}

func (e *NormalizedStepExecutor) decodeReleaseSnapshot(
	release *ReleaseWithArtifacts,
) (*ReleaseWithArtifacts, runtimeReleaseSnapshot, error) {
	snapshot, err := decodeReleaseRuntimeSnapshot(release)
	if err != nil {
		return nil, runtimeReleaseSnapshot{}, err
	}
	return release, snapshot, nil
}

// decodeReleaseRuntimeSnapshot is the only reader of the runtime_config
// artifact. Operational summaries read the same verified snapshot the executor
// acts on rather than parsing release metadata a second time.
func decodeReleaseRuntimeSnapshot(release *ReleaseWithArtifacts) (runtimeReleaseSnapshot, error) {
	for _, artifact := range release.Artifacts {
		if artifact.Kind != ArtifactRuntimeConfig || artifact.State != "available" {
			continue
		}
		var envelope struct {
			Snapshot json.RawMessage `json:"snapshot"`
		}
		if json.Unmarshal(artifact.Metadata, &envelope) != nil || len(envelope.Snapshot) == 0 ||
			digestBytes(envelope.Snapshot) != artifact.Digest || artifact.Digest != release.Release.ConfigDigest {
			return runtimeReleaseSnapshot{}, fmt.Errorf("%w: runtime snapshot digest is inconsistent", ErrArtifactMissing)
		}
		var snapshot runtimeReleaseSnapshot
		if json.Unmarshal(envelope.Snapshot, &snapshot) != nil || snapshot.Version != 1 {
			return runtimeReleaseSnapshot{}, fmt.Errorf("%w: runtime snapshot is malformed", ErrArtifactMissing)
		}
		if snapshot.Plan.Strategy != release.Release.Strategy ||
			(release.Release.ImageDigest != "" && snapshot.Image.Digest != release.Release.ImageDigest) {
			return runtimeReleaseSnapshot{}, fmt.Errorf("%w: runtime snapshot does not match release identity", ErrInvalidPlan)
		}
		return snapshot, nil
	}
	return runtimeReleaseSnapshot{}, fmt.Errorf("%w: release runtime artifact is unavailable", ErrArtifactMissing)
}

// operationTargetsLiveRelease is true for the operations that act on the
// environment's current live release instead of a candidate release of their
// own: restart cycles it, stop and start move only its runtime state. All
// three resolve their target through operationTargetReleaseID rather than
// ReleaseForRun, and link the run to the live release in record_release.
func operationTargetsLiveRelease(op Operation) bool {
	switch op {
	case OperationRestart, OperationStop, OperationStart:
		return true
	default:
		return false
	}
}

func operationTargetReleaseID(run EngineRun) (int64, error) {
	var metadata struct {
		TargetReleaseID int64 `json:"targetReleaseId"`
	}
	if json.Unmarshal(run.Metadata, &metadata) != nil || metadata.TargetReleaseID <= 0 {
		return 0, fmt.Errorf("%w: operation has no immutable target release", ErrInvalidPlan)
	}
	return metadata.TargetReleaseID, nil
}

func (e *NormalizedStepExecutor) restartLiveRuntime(ctx context.Context, execution StepExecution) StepResult {
	releaseID, err := operationTargetReleaseID(execution.Run)
	if err != nil {
		return normalizedStepFailure(err)
	}
	release, snapshot, err := e.releaseSnapshot(ctx, releaseID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	live, err := e.store.LiveRelease(ctx, execution.Run.EnvironmentID)
	if err != nil || live.Release.ID != release.Release.ID {
		return StepResult{State: StepFailed, ErrorCode: "restart_target_changed", ErrorMessage: "the selected release is no longer live"}
	}
	runtime, err := e.store.RuntimeForRelease(ctx, releaseID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	variables, err := e.runtimeVariablesForRelease(ctx, releaseID, *runtime)
	if err != nil {
		return normalizedStepFailure(err)
	}
	stop, err := e.runtime.Stop(ctx, *runtime, snapshot.Plan, variables, false,
		func(line BuildLog) error { return stepLog(execution, line.Stream, line.Text) })
	if err != nil {
		return runtimeStepFailure(err, "restart_stop_failed", "the live runtime could not be stopped", nil)
	}
	if err := e.store.SetRuntimeState(ctx, execution.Run.ID, execution.ClaimToken, releaseID, "stopped"); err != nil {
		return normalizedStepFailure(err)
	}
	if err := e.runtime.StartExisting(ctx, *runtime, variables,
		func(line BuildLog) error { return stepLog(execution, line.Stream, line.Text) }); err != nil {
		recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(runtimeGrace(snapshot.Plan)+30)*time.Second)
		restoreErr := e.runtime.StartExisting(recoveryCtx, *runtime, variables, nil)
		if restoreErr == nil {
			_ = e.store.SetRuntimeState(recoveryCtx, execution.Run.ID, execution.ClaimToken, releaseID, "live")
		}
		cancel()
		return StepResult{
			State: StepFailed, ErrorCode: "restart_start_failed", ErrorMessage: "the live runtime did not restart",
			Evidence: mustJSON(map[string]any{"stop": stop, "restored": restoreErr == nil}), Recovered: restoreErr == nil,
		}
	}
	if err := e.store.SetRuntimeState(ctx, execution.Run.ID, execution.ClaimToken, releaseID, "live"); err != nil {
		return normalizedStepFailure(err)
	}
	return StepResult{State: StepPassed, Evidence: mustJSON(map[string]any{
		"releaseId": releaseID, "runtimeId": runtime.RuntimeID, "stop": stop,
	})}
}

// stopLiveRuntime is start_candidate for a stop run: it stops the live
// release's runtime in place, without removing it, and leaves the release
// pointer untouched. Nothing is restarted; that is the start operation's job.
func (e *NormalizedStepExecutor) stopLiveRuntime(ctx context.Context, execution StepExecution) StepResult {
	releaseID, err := operationTargetReleaseID(execution.Run)
	if err != nil {
		return normalizedStepFailure(err)
	}
	release, snapshot, err := e.releaseSnapshot(ctx, releaseID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	live, err := e.store.LiveRelease(ctx, execution.Run.EnvironmentID)
	if err != nil || live.Release.ID != release.Release.ID {
		return StepResult{State: StepFailed, ErrorCode: "stop_target_changed", ErrorMessage: "the selected release is no longer live"}
	}
	runtime, err := e.store.RuntimeForRelease(ctx, releaseID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	variables, err := e.runtimeVariablesForRelease(ctx, releaseID, *runtime)
	if err != nil {
		return normalizedStepFailure(err)
	}
	stop, err := e.runtime.Stop(ctx, *runtime, snapshot.Plan, variables, false,
		func(line BuildLog) error { return stepLog(execution, line.Stream, line.Text) })
	if err != nil {
		return runtimeStepFailure(err, "stop_failed", "the live runtime could not be stopped", nil)
	}
	if err := e.store.SetRuntimeState(ctx, execution.Run.ID, execution.ClaimToken, releaseID, "stopped"); err != nil {
		return normalizedStepFailure(err)
	}
	return StepResult{State: StepPassed, Evidence: mustJSON(map[string]any{
		"releaseId": releaseID, "runtimeId": runtime.RuntimeID, "stop": stop,
	})}
}

// startLiveRuntime is start_candidate for a start run: it starts a runtime
// this environment already recorded as stopped. There is no fresh candidate
// and, on failure, no compensation to attempt — the runtime is left exactly
// as stopped as it was found.
func (e *NormalizedStepExecutor) startLiveRuntime(ctx context.Context, execution StepExecution) StepResult {
	releaseID, err := operationTargetReleaseID(execution.Run)
	if err != nil {
		return normalizedStepFailure(err)
	}
	release, _, err := e.releaseSnapshot(ctx, releaseID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	live, err := e.store.LiveRelease(ctx, execution.Run.EnvironmentID)
	if err != nil || live.Release.ID != release.Release.ID {
		return StepResult{State: StepFailed, ErrorCode: "start_target_changed", ErrorMessage: "the selected release is no longer live"}
	}
	runtime, err := e.store.RuntimeForRelease(ctx, releaseID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	if runtime.State != "stopped" {
		return StepResult{State: StepFailed, ErrorCode: "runtime_not_stopped", ErrorMessage: "the live runtime is already running"}
	}
	variables, err := e.runtimeVariablesForRelease(ctx, releaseID, *runtime)
	if err != nil {
		return normalizedStepFailure(err)
	}
	if err := e.runtime.StartExisting(ctx, *runtime, variables,
		func(line BuildLog) error { return stepLog(execution, line.Stream, line.Text) }); err != nil {
		return runtimeStepFailure(err, "start_failed", "the live runtime did not start", nil)
	}
	if err := e.store.SetRuntimeState(ctx, execution.Run.ID, execution.ClaimToken, releaseID, "live"); err != nil {
		return normalizedStepFailure(err)
	}
	return StepResult{State: StepPassed, Evidence: mustJSON(map[string]any{
		"releaseId": releaseID, "runtimeId": runtime.RuntimeID,
	})}
}

func targetForRuntime(runtime ReleaseRuntime, snapshot runtimeReleaseSnapshot) CheckTarget {
	target := CheckTarget{Host: runtimeCheckHost(runtime.Host), Port: runtime.Port, OriginalPorts: []int{snapshot.Plan.InternalPort, snapshot.Plan.HostPort}}
	if runtime.Kind == "container" {
		target.ContainerID = runtime.RuntimeID
	} else if metadata, err := decodeDockerRuntimeMetadata(runtime.Metadata); err == nil {
		target.ContainerID = metadata.PrimaryContainerID
	}
	target.PublicURLs = publicRouteURLs(snapshot.Domains)
	return target
}

func runtimeCheckHost(host string) string {
	switch host {
	case "", "0.0.0.0":
		return "127.0.0.1"
	case "::":
		return "::1"
	default:
		return host
	}
}

func (e *NormalizedStepExecutor) runtimeVariablesForRelease(
	ctx context.Context,
	releaseID int64,
	runtime ReleaseRuntime,
) (map[string]string, error) {
	if runtime.Kind != "compose" {
		return map[string]string{}, nil
	}
	release, err := e.store.Release(ctx, releaseID)
	if err != nil {
		return nil, err
	}
	return e.variablesForScope(ctx, release.Release.RunID, release.Release.EnvironmentID, "runtime")
}

func runtimeFromInput(input ReleaseRuntimeInput) ReleaseRuntime {
	return ReleaseRuntime{
		ReleaseID: input.ReleaseID, Kind: input.Kind, RuntimeID: input.RuntimeID, Name: input.Name,
		WorkingDirectory: input.WorkingDirectory, Host: input.Host, Port: input.Port, State: "candidate", Metadata: input.Metadata,
	}
}

func runtimeStepFailure(err error, code, message string, recovery *recoveryEvidence) StepResult {
	state := StepFailed
	if errors.Is(err, ErrRuntimeUnavailable) {
		state = StepUnavailable
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		state, code, message = StepCancelled, "cancelled", "runtime operation was cancelled"
	}
	result := StepResult{State: state, ErrorCode: code, ErrorMessage: message}
	if recovery != nil {
		result.Evidence = mustJSON(map[string]any{"recovery": recovery})
	}
	return result
}

// runtimeDiagnosticsEvidence is what a failed gate persists about the
// candidate: state and counts only. The output lines themselves go to the
// transcript, redacted, where the operator reads them next to the failure.
type runtimeDiagnosticsEvidence struct {
	Available  bool                          `json:"available"`
	Error      string                        `json:"error,omitempty"`
	Lines      int                           `json:"lines"`
	Containers []runtimeContainerDiagnostics `json:"containers,omitempty"`
	Cause      *OutputCause                  `json:"cause,omitempty"`
}

type runtimeContainerDiagnostics struct {
	Name         string `json:"name"`
	State        string `json:"state"`
	ExitCode     int    `json:"exitCode"`
	OOMKilled    bool   `json:"oomKilled,omitempty"`
	RestartCount int    `json:"restartCount,omitempty"`
	Lines        int    `json:"lines"`
	Truncated    bool   `json:"truncated,omitempty"`
}

func (e *NormalizedStepExecutor) captureRuntimeDiagnostics(
	ctx context.Context,
	execution StepExecution,
	release Release,
	runtime ReleaseRuntime,
	causeContext runtimeCauseContext,
) *runtimeDiagnosticsEvidence {
	diagnoser, ok := e.runtime.(RuntimeDiagnoser)
	if !ok || runtime.RuntimeID == "" {
		return nil
	}
	// Bounded and detached from cancellation: a cancelled run still deserves
	// its diagnosis, and a hung Docker daemon must not hold the failure open.
	diagnoseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), diagnosticReadDeadline+7*time.Second)
	defer cancel()
	variables, _ := e.runtimeVariablesForRelease(diagnoseCtx, release.ID, runtime)
	redact := newSecretWriter(io.Discard, variables)
	result, err := diagnoser.DiagnoseRuntime(diagnoseCtx, runtime)
	if err != nil {
		_ = stepLog(execution, "status", "Application output could not be read: "+redact.sanitize(err.Error()))
		return &runtimeDiagnosticsEvidence{Available: false, Error: redact.sanitize(err.Error())}
	}
	evidence := &runtimeDiagnosticsEvidence{Available: true}
	for _, container := range result.Containers {
		summary := runtimeContainerDiagnostics{
			Name: container.Name, State: container.State, ExitCode: container.ExitCode,
			OOMKilled: container.OOMKilled, RestartCount: container.RestartCount,
			Lines: len(container.Lines), Truncated: container.Truncated,
		}
		evidence.Containers = append(evidence.Containers, summary)
		evidence.Lines += len(container.Lines)
		_ = stepLog(execution, "status", describeContainerDiagnostics(container))
		if container.Truncated {
			_ = stepLog(execution, "status", "… earlier output omitted")
		}
		for _, line := range container.Lines {
			stream := line.Stream
			if stream != "stderr" {
				stream = "stdout"
			}
			_ = stepLog(execution, stream, redact.sanitize(line.Text))
		}
	}
	if evidence.Lines == 0 {
		_ = stepLog(execution, "status", "The application printed no output before the check failed.")
	}
	if cause := applicationOutputCause(result.Containers, causeContext); cause != nil {
		cause.Table = redact.sanitize(cause.Table)
		for index, subject := range cause.Subjects {
			cause.Subjects[index] = redact.sanitize(subject)
		}
		evidence.Cause = cause
		_ = stepLog(execution, "status", "Diagnosis: "+cause.sentence())
	}
	return evidence
}

func describeContainerDiagnostics(container ContainerDiagnostics) string {
	name := container.Name
	if name == "" {
		name = container.ID
	}
	var detail strings.Builder
	fmt.Fprintf(&detail, "Application output from %s (%s", name, container.State)
	if container.State != "running" {
		fmt.Fprintf(&detail, ", exit code %d", container.ExitCode)
	}
	if container.OOMKilled {
		detail.WriteString(", killed by the kernel for exceeding its memory limit")
	}
	if container.RestartCount > 0 {
		fmt.Fprintf(&detail, ", restarted %d time(s)", container.RestartCount)
	}
	if container.Error != "" {
		fmt.Fprintf(&detail, ", runtime error: %s", container.Error)
	}
	fmt.Fprintf(&detail, "), last %d line(s):", len(container.Lines))
	return detail.String()
}
