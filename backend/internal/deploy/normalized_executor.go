package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

type DeploymentStepExecutor struct {
	legacy     StepExecutor
	normalized StepExecutor
}

func NewDeploymentStepExecutor(legacy, normalized StepExecutor) *DeploymentStepExecutor {
	return &DeploymentStepExecutor{legacy: legacy, normalized: normalized}
}

func (e *DeploymentStepExecutor) Execute(ctx context.Context, execution StepExecution) StepResult {
	if execution.Step.Key == StepLegacyPipeline {
		if e.legacy == nil {
			return unavailableExecutor{}.Execute(ctx, execution)
		}
		return e.legacy.Execute(ctx, execution)
	}
	if e.normalized == nil {
		return unavailableExecutor{}.Execute(ctx, execution)
	}
	return e.normalized.Execute(ctx, execution)
}

func (e *DeploymentStepExecutor) CleanupCancelledRun(
	ctx context.Context,
	run EngineRun,
	claimToken string,
) (json.RawMessage, error) {
	if cleaner, ok := e.normalized.(RunCancellationCleaner); ok {
		return cleaner.CleanupCancelledRun(ctx, run, claimToken)
	}
	return mustJSON(map[string]any{"completed": true, "reason": "executor has no cancellation-owned runtime"}), nil
}

type NormalizedStepExecutor struct {
	store         *OrchestrationStore
	variables     *PlanningStore
	sources       *HostSourceAnalyzer
	builder       *ArtifactBuilder
	runtime       RuntimeOwner
	checks        *CheckRunner
	backups       BackupGate
	preflight     PreflightObserver
	proxy         ActivationProxy
	workspaceRoot string
	notifications *AutomationStore

	certificates CertificateIssuer
}

func (e *NormalizedStepExecutor) WithNotifications(store *AutomationStore) *NormalizedStepExecutor {
	e.notifications = store
	return e
}

// WithBackupGate attaches the existing Backups feature through its narrow
// deployment contract. The optional form preserves hosts/tests that do not
// have that module; configured required gates then report unavailable instead
// of being silently treated as successful.
func (e *NormalizedStepExecutor) WithBackupGate(backups BackupGate) *NormalizedStepExecutor {
	e.backups = backups
	return e
}

// WithPreflightObserver attaches the same read-only host observation boundary
// used by creation. Mutable desired configuration is checked again from the
// run's frozen snapshot, immediately before any build/runtime side effect.
func (e *NormalizedStepExecutor) WithPreflightObserver(observer PreflightObserver) *NormalizedStepExecutor {
	e.preflight = observer
	return e
}

type ActivationProxy interface {
	ApplyDeploymentRoute(context.Context, proxysvc.DeploymentRoute) (proxysvc.DeploymentRouteResult, error)
	RestoreDeploymentRoute(context.Context, proxysvc.DeploymentRouteSnapshot) error
	VerifyDeploymentRoute(context.Context, proxysvc.DeploymentRoute) error
}

func NewNormalizedStepExecutor(
	store *OrchestrationStore,
	variables *PlanningStore,
	sources *HostSourceAnalyzer,
	builder *ArtifactBuilder,
	runtime RuntimeOwner,
	checks *CheckRunner,
	proxy ActivationProxy,
	workspaceRoot string,
) *NormalizedStepExecutor {
	return &NormalizedStepExecutor{
		store: store, variables: variables, sources: sources,
		builder: builder, runtime: runtime, checks: checks, proxy: proxy,
		workspaceRoot: workspaceRoot,
	}
}

type preparedStepEvidence struct {
	Source    MaterializedSource `json:"source"`
	BuildRoot string             `json:"buildRoot"`
	Prepared  PreparedBuild      `json:"prepared"`
}

type builtStepEvidence struct {
	Result BuildArtifactResult `json:"result"`
}

type renderedStepEvidence struct {
	ReleaseID     int64  `json:"releaseId"`
	ReleaseNumber int64  `json:"releaseNumber"`
	ConfigDigest  string `json:"configDigest"`
}

type runtimeReleaseSnapshot struct {
	Version        int                       `json:"version"`
	Plan           RuntimePlanConfig         `json:"plan"`
	Image          ResolvedImage             `json:"image,omitempty"`
	Compose        *ResolvedComposeSnapshot  `json:"compose,omitempty"`
	Variables      []ReleaseVariableSnapshot `json:"variables"`
	Dependencies   []PlannedDependency       `json:"dependencies"`
	Checks         []PlannedCheck            `json:"checks"`
	Domains        []PlannedDomain           `json:"domains"`
	PlanInputsHash string                    `json:"planInputsDigest"`
	SourceIdentity SourceIdentity            `json:"sourceIdentity"`
}

func (e *NormalizedStepExecutor) Execute(ctx context.Context, execution StepExecution) StepResult {
	if e == nil || e.store == nil || e.sources == nil || e.builder == nil {
		return StepResult{State: StepUnavailable, ErrorCode: "builder_unavailable", ErrorMessage: "the normalized deployment executor is unavailable"}
	}
	plan, err := e.store.ExecutionPlan(ctx, execution.Run)
	if err != nil {
		return normalizedStepFailure(err)
	}
	var environmentKind EnvironmentKind
	if err := e.store.db.QueryRowContext(ctx, `SELECT kind FROM deploy_environments WHERE id=?`, execution.Run.EnvironmentID).Scan(&environmentKind); err != nil {
		return normalizedStepFailure(err)
	}
	if environmentKind == EnvironmentPreview && execution.Run.Operation != OperationPreviewRemove {
		var quarantined int
		if err := e.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_preview_quarantines WHERE environment_id=? AND status<>'cleared'`, execution.Run.EnvironmentID).Scan(&quarantined); err != nil {
			return normalizedStepFailure(err)
		}
		if quarantined != 0 {
			return normalizedStepFailure(ErrPreviewIsolation)
		}
		if err := validatePreviewPlan(execution.Run.EnvironmentID, plan.Build, plan.Runtime); err != nil {
			return normalizedStepFailure(err)
		}
		approved, err := reviewedPreviewRevisionTx(ctx, e.store.db, execution.Run.EnvironmentID, plan.SourceIdentity.Revision)
		if err != nil {
			return normalizedStepFailure(err)
		}
		if !approved {
			return normalizedStepFailure(ErrPreviewApproval)
		}
	}
	switch execution.Step.Key {
	case StepResolveSource:
		if err := validateImmutableExecutionSource(plan); err != nil {
			return normalizedStepFailure(err)
		}
		_ = stepLog(execution, "status", "Using recorded source identity "+shortIdentity(plan.SourceIdentity))
		return StepResult{State: StepPassed, Evidence: mustJSON(map[string]any{
			"kind": plan.SourceKind, "revision": immutableSourceRevision(plan.SourceIdentity),
			"sourceDigest": plan.SourceDigest,
		})}
	case StepAcquireSource:
		return e.acquireSource(ctx, execution, plan)
	case StepAnalyzePlan:
		if err := validateStoredExecutionPlan(plan); err != nil {
			return normalizedStepFailure(err)
		}
		return e.analyzePlan(ctx, execution, plan)
	case StepPrepareContext:
		return e.prepareContext(ctx, execution, plan)
	case StepBuildArtifact:
		return e.buildArtifact(ctx, execution, plan)
	case StepRenderRuntime:
		return e.renderRuntime(ctx, execution, plan)
	case StepReleaseTask:
		return e.runReleaseTasks(ctx, execution, plan)
	case StepBackupGate:
		return e.backupGate(ctx, execution, plan)
	case StepProvisionCertificate:
		return e.provisionCertificate(ctx, execution)
	case StepStartCandidate:
		return e.startCandidate(ctx, execution, plan)
	case StepVerifyReadiness:
		return e.verifyChecks(ctx, execution, plan, "readiness")
	case StepVerifySmoke:
		return e.verifyChecks(ctx, execution, plan, "smoke")
	case StepActivate:
		return e.activate(ctx, execution, plan)
	case StepRetirePrevious:
		if execution.Run.Operation == OperationPreviewRemove {
			return e.removePreview(ctx, execution)
		}
		return e.retirePrevious(ctx, execution, plan)
	case StepRecordRelease:
		return e.recordRelease(ctx, execution)
	case StepNotify:
		return e.notify(ctx, execution)
	default:
		return StepResult{
			State: StepUnavailable, ErrorCode: "unsupported_runtime",
			ErrorMessage: fmt.Sprintf("normalized runtime step %s is not implemented yet", execution.Step.Key),
		}
	}
}

// acquireSource materializes the frozen source identity into a private
// workspace and, for a Git source, records the commit it resolved to on the
// run's metadata — the same subject/author/date a Vercel-style history would
// show next to the sha the run already carries. Reading that commit is
// best-effort: an unreadable history logs a transcript line and the step
// still passes, since acquiring the source itself already succeeded.
func (e *NormalizedStepExecutor) acquireSource(
	ctx context.Context,
	execution StepExecution,
	plan *StoredExecutionPlan,
) StepResult {
	source, err := e.sources.Materialize(ctx, plan.SourceConfig, plan.SourceIdentity, execution.Run.ID, e.workspaceRoot)
	if err != nil {
		return normalizedStepFailure(err)
	}
	_ = stepLog(execution, "status", "Materialized the recorded source in a private release workspace")
	// A missing commit summary is a warning on stderr: the transcript has
	// only stdout, stderr and status, and a stream it refuses would fail the
	// append and silence everything this step logs after it.
	if gitSourceMode(plan.SourceConfig.Mode) {
		if source.Commit != nil {
			if _, mergeErr := e.store.MergeRunMetadata(ctx, execution.Run.ID,
				map[string]any{"commit": source.Commit}); mergeErr != nil {
				_ = stepLog(execution, "stderr", "Could not record commit metadata: "+mergeErr.Error())
			}
		} else {
			_ = stepLog(execution, "stderr",
				"Could not read commit metadata for the recorded source; continuing without it")
		}
	}
	return StepResult{State: StepPassed, Evidence: mustJSON(source)}
}

func (e *NormalizedStepExecutor) notify(ctx context.Context, execution StepExecution) StepResult {
	var schedule struct {
		Marker bool   `json:"scheduleMarker"`
		Status string `json:"chainStatus"`
	}
	_ = json.Unmarshal(execution.Run.Metadata, &schedule)
	terminalState := string(RunSucceeded)
	if schedule.Marker && schedule.Status == "failed" {
		terminalState = string(RunFailed)
	}
	if e.notifications == nil {
		if schedule.Marker && schedule.Status == "failed" {
			return StepResult{State: StepFailed, ErrorCode: "schedule_chain_failed", ErrorMessage: "a required scheduled action failed", Evidence: mustJSON(map[string]any{"reason": "no notification service configured"})}
		}
		return StepResult{State: StepSkipped, Evidence: mustJSON(map[string]any{"reason": "no notification service configured"})}
	}
	channels, err := e.notifications.ListNotificationChannels(ctx)
	if err != nil {
		if schedule.Marker && schedule.Status == "failed" {
			return StepResult{State: StepFailed, ErrorCode: "schedule_chain_failed", ErrorMessage: "a required scheduled action failed", Evidence: mustJSON(map[string]any{"reason": "notification channels unavailable"})}
		}
		return StepResult{State: StepWarning, Evidence: mustJSON(map[string]any{"reason": "notification channels unavailable"})}
	}
	// Delivery itself happens after the run reaches its terminal state, from
	// the engine's run observers, so failed and cancelled runs are announced
	// through the same channels as successful ones. This step records which
	// channels will hear about the outcome and keeps the scheduled-chain gate.
	selected := 0
	for _, channel := range channels {
		if channel.Enabled && notificationEventSelected(channel.Events, notificationEventForState(RunState(terminalState))) {
			selected++
		}
	}
	if schedule.Marker && schedule.Status == "failed" {
		return StepResult{State: StepFailed, ErrorCode: "schedule_chain_failed", ErrorMessage: "a required scheduled action failed", Evidence: mustJSON(map[string]any{"channels": selected, "deliveredBy": "run observers"})}
	}
	if selected == 0 {
		return StepResult{State: StepSkipped, Evidence: mustJSON(map[string]any{"channels": 0, "reason": "no enabled channel selects this outcome"})}
	}
	return StepResult{State: StepPassed, Evidence: mustJSON(map[string]any{"channels": selected, "deliveredBy": "run observers"})}
}

// notificationEventSelected decides whether a channel's event list wants an
// event. An empty list means everything. The historical "run.finished" value
// stays valid as "every terminal outcome", so channels created before failure
// notifications existed start receiving them without being edited.
func notificationEventSelected(events []string, want string) bool {
	if len(events) == 0 {
		return true
	}
	for _, event := range events {
		if event == want {
			return true
		}
		if event == NotificationEventFinished && want != NotificationEventStarted {
			return true
		}
	}
	return false
}

func (e *NormalizedStepExecutor) analyzePlan(
	ctx context.Context,
	execution StepExecution,
	plan *StoredExecutionPlan,
) StepResult {
	if e.preflight == nil {
		return StepResult{State: StepUnavailable, ErrorCode: "preflight_unavailable", ErrorMessage: "deployment host preflight is unavailable"}
	}
	configuration, err := storedExecutionConfiguration(plan)
	if err != nil {
		return normalizedStepFailure(err)
	}
	profile, err := e.store.deploymentProfile(ctx, execution.Run.ProjectID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	candidate := newDetectedCandidate(plan.Build.RootDirectory, plan.Build.Method, DetectedCandidate{
		Name: "recorded-plan", Profile: profile, Confidence: ConfidenceHigh,
		Evidence: []DetectionEvidence{}, NeedsDecision: []string{},
	})
	source := plan.SourceConfig
	draft := &Draft{Data: DraftData{
		Intent: &DraftIntentConfig{Name: "recorded-plan", Profile: profile},
		Source: &source,
		Detection: &DetectionResult{
			Source: plan.SourceIdentity, Candidates: []DetectedCandidate{candidate}, SelectedID: candidate.ID,
			Compose: plan.BuildEvidence.Compose, GitRequirements: plan.BuildEvidence.GitRequirements,
		},
		Configuration: &configuration,
	}}
	request := preflightObservationRequest(draft, configuration)
	request.ExistingProxySite = deploymentRouteName(execution.Run.EnvironmentID)
	live, liveErr := e.store.LiveRelease(ctx, execution.Run.EnvironmentID)
	if liveErr == nil {
		runtime, runtimeErr := e.store.RuntimeForRelease(ctx, live.Release.ID)
		if runtimeErr != nil {
			return normalizedStepFailure(runtimeErr)
		}
		request.ExistingRuntimeID, request.ExistingRuntimeKind = runtime.RuntimeID, runtime.Kind
	} else if !errors.Is(liveErr, ErrArtifactMissing) {
		return normalizedStepFailure(liveErr)
	}
	observation, err := e.preflight.Observe(ctx, request)
	if err != nil {
		return StepResult{State: StepUnavailable, ErrorCode: "preflight_unavailable", ErrorMessage: "deployment host evidence could not be refreshed"}
	}
	findings := preflightFindings(draft, configuration, observation, true)
	evidence := mustJSON(map[string]any{
		"sourceDigest": plan.SourceDigest, "buildDigest": plan.BuildDigest,
		"runtimeDigest": plan.RuntimeDigest, "planRevision": execution.Run.PlanRevision,
		"findings": findings,
	})
	state := StepPassed
	for _, finding := range findings {
		if finding.Severity == PreflightBlocked ||
			(finding.Severity == PreflightDecision && executionDecisionMustBlock(finding.Code)) {
			return StepResult{State: StepFailed, ErrorCode: finding.Code, ErrorMessage: finding.Title, Evidence: evidence}
		}
		if finding.Severity != PreflightPass {
			state = StepWarning
		}
	}
	return StepResult{State: state, Evidence: evidence}
}

func executionDecisionMustBlock(code string) bool {
	return code == "domain_link_missing" || code == "readiness_missing"
}

func (e *NormalizedStepExecutor) prepareContext(
	ctx context.Context,
	execution StepExecution,
	plan *StoredExecutionPlan,
) StepResult {
	buildVariables, err := e.variablesForScope(ctx, execution.Run.ID, execution.Run.EnvironmentID, "build")
	if err != nil {
		return normalizedStepFailure(err)
	}
	source, buildRoot, err := e.materializedBuildRoot(ctx, execution.Run, plan)
	if err != nil {
		return normalizedStepFailure(err)
	}
	tag := releaseImageTag(execution.Run.EnvironmentID, execution.Run.ID)
	prepared, err := e.builder.Prepare(ctx, buildRoot, plan.Build, execution.Run.Operation == OperationForceBuild, tag, buildVariableNames(buildVariables)...)
	if err != nil {
		cleaned, cleanupErr := source.Cleanup()
		result := normalizedStepFailure(err)
		if errors.Is(err, ErrBuilderUnavailable) {
			result = builderUnavailableFailure(err, buildRedactor(buildVariables))
			// The daemon's own words are the only record of why, and nothing
			// else writes them down before the step ends.
			_ = stepLog(execution, "stderr", builderUnavailableDetail(err, buildRedactor(buildVariables)))
		}
		result.Cleanup = mustJSON(map[string]any{"workspaceRemoved": cleaned, "error": safeCleanupError(cleanupErr)})
		return result
	}
	if err := stepLog(execution, "status", "Prepared "+string(plan.Build.Method)+" artifact plan with "+prepared.CachePolicy+" cache policy"); err != nil {
		return normalizedStepFailure(err)
	}
	return StepResult{State: StepPassed, Evidence: mustJSON(preparedStepEvidence{
		Source: *source, BuildRoot: buildRoot, Prepared: prepared,
	})}
}

func (e *NormalizedStepExecutor) buildArtifact(
	ctx context.Context,
	execution StepExecution,
	plan *StoredExecutionPlan,
) StepResult {
	var preparedEvidence preparedStepEvidence
	if err := e.latestStepEvidence(ctx, execution.Run.ID, StepPrepareContext, &preparedEvidence); err != nil {
		return normalizedStepFailure(err)
	}
	buildVariables, err := e.variablesForScope(ctx, execution.Run.ID, execution.Run.EnvironmentID, "build")
	if err != nil {
		return normalizedStepFailure(err)
	}
	registryAuth := ""
	if plan.SourceKind == SourceImage || plan.SourceKind == SourceBlueprint {
		registryAuth, err = e.sources.registryAuth(ctx, plan.SourceConfig.CredentialID, plan.SourceIdentity.Repository)
		if err != nil {
			return normalizedStepFailure(err)
		}
	}
	// The collector reads the transcript as it is persisted, after the
	// builder has redacted it, so a failure can be named from its own output.
	collector := newBuildOutputCollector(func() int64 { return lastLogSeq(execution.Output) })
	result, err := e.builder.Build(
		ctx, preparedEvidence.BuildRoot,
		releaseImageTag(execution.Run.EnvironmentID, execution.Run.ID),
		plan.Build, preparedEvidence.Prepared, buildVariables, registryAuth,
		plan.SourceIdentity, plan.BuildEvidence.Compose,
		func(line BuildLog) error {
			if err := stepLog(execution, line.Stream, line.Text); err != nil {
				return err
			}
			collector.observe(line)
			return nil
		},
	)
	if err != nil {
		cleaned, cleanupErr := preparedEvidence.Source.Cleanup()
		cleanup := map[string]any{"workspaceRemoved": cleaned, "error": safeCleanupError(cleanupErr)}
		if ctx.Err() != nil {
			return StepResult{
				State: StepCancelled, ErrorCode: "cancelled", ErrorMessage: "artifact build cancelled",
				Cleanup: mustJSON(cleanup),
			}
		}
		if failure := e.buildFailure(ctx, execution, plan, preparedEvidence.Prepared, buildVariables, collector, err); failure != nil {
			failure.Cleanup = mustJSON(buildFailureCleanup(cleanup, err))
			return *failure
		}
		failure := normalizedStepFailure(err)
		failure.Cleanup = mustJSON(cleanup)
		return failure
	}
	return StepResult{State: StepPassed, Evidence: mustJSON(builtStepEvidence{Result: result})}
}

func (e *NormalizedStepExecutor) renderRuntime(
	ctx context.Context,
	execution StepExecution,
	plan *StoredExecutionPlan,
) StepResult {
	if execution.Run.Operation == OperationRedeploy || execution.Run.Operation == OperationRollback {
		targetReleaseID, err := operationTargetReleaseID(execution.Run)
		if err != nil {
			return normalizedStepFailure(err)
		}
		release, err := e.store.CloneCandidateRelease(ctx, execution.Run, execution.ClaimToken, targetReleaseID)
		if err != nil {
			return normalizedStepFailure(err)
		}
		_ = stepLog(execution, "status", fmt.Sprintf("Cloned immutable release #%d as candidate #%d", targetReleaseID, release.Release.Number))
		return StepResult{State: StepPassed, Evidence: mustJSON(renderedStepEvidence{
			ReleaseID: release.Release.ID, ReleaseNumber: release.Release.Number,
			ConfigDigest: release.Release.ConfigDigest,
		})}
	}
	var built builtStepEvidence
	if err := e.latestStepEvidence(ctx, execution.Run.ID, StepBuildArtifact, &built); err != nil {
		return normalizedStepFailure(err)
	}
	dependencies, checks, domains, err := decodeStoredPlanInputs(plan)
	if err != nil {
		return normalizedStepFailure(err)
	}
	snapshot := runtimeReleaseSnapshot{
		Version: 1, Plan: plan.Runtime, Image: built.Result.Image,
		Compose:        built.Result.Compose,
		Variables:      append([]ReleaseVariableSnapshot(nil), plan.Variables...),
		Dependencies:   dependencies,
		Checks:         checks,
		Domains:        domains,
		PlanInputsHash: plan.PlanInputsDigest,
		SourceIdentity: plan.SourceIdentity,
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return normalizedStepFailure(err)
	}
	configDigest := digestBytes(raw)
	release, err := e.store.CreateCandidateRelease(ctx, execution.Run, execution.ClaimToken, CandidateReleaseInput{
		Artifacts: built.Result.Artifacts, Prepared: built.Result.Prepared,
		RuntimeSnapshot: raw, RuntimeDigest: configDigest,
	})
	if err != nil {
		return normalizedStepFailure(err)
	}
	_ = stepLog(execution, "status", fmt.Sprintf("Recorded immutable candidate release #%d", release.Release.Number))
	return StepResult{State: StepPassed, Evidence: mustJSON(renderedStepEvidence{
		ReleaseID: release.Release.ID, ReleaseNumber: release.Release.Number, ConfigDigest: configDigest,
	})}
}

func (e *NormalizedStepExecutor) runReleaseTasks(
	ctx context.Context,
	execution StepExecution,
	plan *StoredExecutionPlan,
) StepResult {
	source, _, err := e.materializedBuildRoot(ctx, execution.Run, plan)
	if err != nil {
		return normalizedStepFailure(err)
	}
	if len(plan.Build.ReleaseTasks) == 0 {
		cleaned, cleanupErr := source.Cleanup()
		if cleanupErr != nil {
			return StepResult{
				State: StepFailed, ErrorCode: "cleanup_incomplete", ErrorMessage: "release workspace cleanup failed",
				Cleanup: mustJSON(map[string]any{"workspaceRemoved": cleaned}),
			}
		}
		return StepResult{
			State: StepSkipped, Evidence: mustJSON(map[string]any{"reason": "no release tasks configured"}),
			Cleanup: mustJSON(map[string]any{"workspaceRemoved": true}),
		}
	}
	values, err := e.variablesForScope(ctx, execution.Run.ID, execution.Run.EnvironmentID, "release_task")
	if err != nil {
		return normalizedStepFailure(err)
	}
	evidence := []ReleaseTaskEvidence{}
	var cleanupResults []any
	for _, task := range plan.Build.ReleaseTasks {
		if err := stepLog(execution, "status", "Running release task "+task.Name); err != nil {
			return normalizedStepFailure(err)
		}
		output := newLineRing(outputRingLines, outputRingBytes)
		taskEvidence, group, taskErr := runStoredReleaseTask(
			ctx, source.Root, task, values,
			func(line BuildLog) error {
				if err := stepLog(execution, line.Stream, line.Text); err != nil {
					return err
				}
				output.add(collectedLine{text: truncateUTF8Prefix(line.Text, collectedLineBytes), seq: lastLogSeq(execution.Output)})
				return nil
			},
		)
		evidence = append(evidence, taskEvidence)
		cleanupResults = append(cleanupResults, group)
		if taskErr != nil {
			cleaned, cleanupErr := source.Cleanup()
			state := StepFailed
			code, message, cause := releaseTaskFailure(task, taskEvidence, taskErr, output.lines, plan.Variables)
			if ctx.Err() != nil {
				state, code, message, cause = StepCancelled, "cancelled", "release task "+task.Name+" did not complete", nil
			}
			if cause != nil {
				_ = stepLog(execution, "status", "Diagnosis: "+cause.releaseSentence())
			}
			return StepResult{
				State: state, ErrorCode: code,
				ErrorMessage: message,
				Evidence:     mustJSON(releaseTaskStepEvidence(evidence, cause)),
				Cleanup: mustJSON(map[string]any{
					"processGroups": cleanupResults, "workspaceRemoved": cleaned,
					"error": safeCleanupError(cleanupErr),
				}),
			}
		}
	}
	cleaned, cleanupErr := source.Cleanup()
	if cleanupErr != nil {
		return StepResult{
			State: StepFailed, ErrorCode: "cleanup_incomplete", ErrorMessage: "release tasks passed but workspace cleanup failed",
			Evidence: mustJSON(map[string]any{"tasks": evidence}),
			Cleanup:  mustJSON(map[string]any{"processGroups": cleanupResults, "workspaceRemoved": cleaned}),
		}
	}
	return StepResult{
		State: StepPassed, Evidence: mustJSON(map[string]any{"tasks": evidence}),
		Cleanup: mustJSON(map[string]any{"processGroups": cleanupResults, "workspaceRemoved": true}),
	}
}

func (e *NormalizedStepExecutor) materializedBuildRoot(
	ctx context.Context,
	run EngineRun,
	plan *StoredExecutionPlan,
) (*MaterializedSource, string, error) {
	source, err := e.sources.Materialize(ctx, plan.SourceConfig, plan.SourceIdentity, run.ID, e.workspaceRoot)
	if err != nil {
		return nil, "", err
	}
	root := source.Root
	if plan.Build.RootDirectory != "" {
		root, err = containedSubdirectory(root, plan.Build.RootDirectory)
		if err != nil {
			return source, "", err
		}
	}
	return source, root, nil
}

func (e *NormalizedStepExecutor) latestStepEvidence(
	ctx context.Context,
	runID int64,
	key StepKey,
	target any,
) error {
	snapshot, err := e.store.Snapshot(ctx, runID)
	if err != nil {
		return err
	}
	var latest *RunStep
	for index := range snapshot.Steps {
		step := &snapshot.Steps[index]
		if step.Key == key && (latest == nil || step.Attempt > latest.Attempt) {
			latest = step
		}
	}
	if latest == nil || (latest.State != StepPassed && latest.State != StepWarning && latest.State != StepSkipped) {
		return fmt.Errorf("%w: prerequisite step %s has no successful evidence", ErrArtifactMissing, key)
	}
	if err := json.Unmarshal(latest.Evidence, target); err != nil {
		return fmt.Errorf("%w: prerequisite step %s evidence is malformed", ErrArtifactMissing, key)
	}
	return nil
}

func (e *NormalizedStepExecutor) variablesForScope(
	ctx context.Context,
	runID, environmentID int64,
	scope string,
) (map[string]string, error) {
	if e.variables == nil {
		return nil, fmt.Errorf("%w: variable store is unavailable", ErrArtifactMissing)
	}
	values, err := e.variables.OpenRunScopedVariables(ctx, runID, environmentID, scope)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(values))
	for _, value := range values {
		result[value.Name] = value.Value
	}
	return result, nil
}

func validateImmutableExecutionSource(plan *StoredExecutionPlan) error {
	if plan == nil || plan.SourceKind != plan.SourceIdentity.Kind {
		return fmt.Errorf("%w: source identity does not match its plan", ErrInvalidPlan)
	}
	switch plan.SourceKind {
	case SourceGit, SourceLocal:
		if !validGitObjectID(plan.SourceIdentity.Revision) {
			return fmt.Errorf("%w: Git source has no immutable object id", ErrInvalidPlan)
		}
	case SourceImage:
		if !contentDigestRE.MatchString(plan.SourceIdentity.Digest) {
			return fmt.Errorf("%w: image source has no immutable digest", ErrInvalidPlan)
		}
	case SourceCompose:
		if !contentDigestRE.MatchString(plan.SourceIdentity.Digest) && plan.SourceIdentity.Revision == "" {
			return fmt.Errorf("%w: Compose source has no immutable identity", ErrInvalidPlan)
		}
	case SourceBlueprint:
		if !contentDigestRE.MatchString(plan.SourceIdentity.Digest) || plan.SourceIdentity.Repository == "" ||
			!contentDigestRE.MatchString(plan.SourceIdentity.Revision) {
			return fmt.Errorf("%w: blueprint source has no immutable image digest and render digest", ErrInvalidPlan)
		}
	case SourceImport:
		return fmt.Errorf("%w: source must be adopted before execution", ErrUnsupportedSource)
	default:
		return ErrUnsupportedSource
	}
	return nil
}

func validateStoredExecutionPlan(plan *StoredExecutionPlan) error {
	if err := validateImmutableExecutionSource(plan); err != nil {
		return err
	}
	if !contentDigestRE.MatchString(plan.SourceDigest) || !contentDigestRE.MatchString(plan.BuildDigest) ||
		!contentDigestRE.MatchString(plan.RuntimeDigest) {
		return fmt.Errorf("%w: persisted plan digest is missing or malformed", ErrInvalidPlan)
	}
	_, err := storedExecutionConfiguration(plan)
	return err
}

func storedExecutionConfiguration(plan *StoredExecutionPlan) (PlanConfiguration, error) {
	variables := make([]PlannedVariable, 0, len(plan.Variables))
	for _, variable := range plan.Variables {
		variables = append(variables, PlannedVariable{
			Name: variable.Name, Sensitivity: variable.Sensitivity,
			Scopes: strings.Split(variable.Scopes, ","),
		})
	}
	dependencies, checks, domains, err := decodeStoredPlanInputs(plan)
	if err != nil {
		return PlanConfiguration{}, err
	}
	configuration := PlanConfiguration{
		Build: plan.Build, Runtime: plan.Runtime, Variables: variables,
		Dependencies: dependencies, Checks: checks, Domains: domains,
	}
	if err := configuration.Validate(); err != nil {
		return PlanConfiguration{}, err
	}
	return configuration, nil
}

func decodeStoredPlanInputs(
	plan *StoredExecutionPlan,
) ([]PlannedDependency, []PlannedCheck, []PlannedDomain, error) {
	dependencies := make([]PlannedDependency, 0, len(plan.Dependencies))
	domains := []PlannedDomain{}
	for _, raw := range plan.Dependencies {
		var stored struct {
			Kind         string          `json:"kind"`
			Ownership    OwnershipMode   `json:"ownership"`
			ResourceKind string          `json:"resourceKind"`
			ResourceID   string          `json:"resourceId"`
			Config       json.RawMessage `json:"config"`
		}
		if err := json.Unmarshal(raw, &stored); err != nil {
			return nil, nil, nil, fmt.Errorf("%w: dependency snapshot is malformed", ErrInvalidPlan)
		}
		if stored.Kind != "domain" {
			dependencies = append(dependencies, PlannedDependency{
				Kind: stored.Kind, Ownership: stored.Ownership,
				ResourceKind: stored.ResourceKind, ResourceID: stored.ResourceID,
				Config: append(json.RawMessage(nil), stored.Config...),
			})
			continue
		}
		var config struct {
			Hostname   string            `json:"hostname"`
			HTTPS      bool              `json:"https"`
			Protection *DomainProtection `json:"protection"`
		}
		if stored.ResourceKind != "proxy_site" || json.Unmarshal(stored.Config, &config) != nil ||
			!strings.EqualFold(strings.TrimSpace(config.Hostname), strings.TrimSpace(stored.ResourceID)) {
			return nil, nil, nil, fmt.Errorf("%w: domain dependency snapshot is inconsistent", ErrInvalidPlan)
		}
		domains = append(domains, PlannedDomain{
			Hostname: config.Hostname, HTTPS: config.HTTPS, Ownership: stored.Ownership,
			Protection: canonicalDomainProtection(config.Protection),
		})
	}
	checks := make([]PlannedCheck, 0, len(plan.Checks))
	for _, raw := range plan.Checks {
		var check PlannedCheck
		if err := json.Unmarshal(raw, &check); err != nil {
			return nil, nil, nil, fmt.Errorf("%w: check snapshot is malformed", ErrInvalidPlan)
		}
		checks = append(checks, check)
	}
	return dependencies, checks, domains, nil
}

func stepLog(execution StepExecution, stream, text string) error {
	if execution.Output == nil {
		return fmt.Errorf("persistent deployment output is unavailable")
	}
	return execution.Output.Log(stream, text)
}

func (s *OrchestrationStore) deploymentProfile(ctx context.Context, projectID int64) (WorkloadProfile, error) {
	var profile WorkloadProfile
	if err := s.db.QueryRowContext(ctx, `SELECT profile FROM deploy_projects WHERE id = ?`, projectID).Scan(&profile); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	if !validProfile(profile) {
		return "", fmt.Errorf("%w: deployment profile is invalid", ErrInvalidPlan)
	}
	return profile, nil
}

func normalizedStepFailure(err error) StepResult {
	result := StepResult{State: StepFailed, ErrorCode: "internal_error", ErrorMessage: "deployment step failed"}
	var sourceFailure *SourceFailure
	var buildFailure *dockerx.BuildError
	switch {
	case errors.As(err, &sourceFailure):
		result.ErrorCode, result.ErrorMessage = sourceFailure.Code, sourceFailure.Error()
	case errors.Is(err, dockerx.ErrBuildTimeout):
		result.ErrorCode, result.ErrorMessage = "build_timeout", "the build exceeded its 30-minute limit"
	case errors.As(err, &buildFailure):
		// The command is left out: without the run's variables at hand it
		// cannot be redacted, and buildFailure names it where they are.
		result.ErrorCode = "build_failed"
		result.ErrorMessage = "a build step failed; its output is in the build log"
		if buildFailure.ExitCode >= 0 {
			result.ErrorMessage = fmt.Sprintf("a build step exited with code %d; its output is in the build log", buildFailure.ExitCode)
		}
	case errors.Is(err, ErrUnsupportedBuilder):
		result.ErrorCode, result.ErrorMessage = "unsupported_builder", err.Error()
	case errors.Is(err, ErrBuilderUnavailable):
		result = builderUnavailableFailure(err, nil)
	case errors.Is(err, ErrArtifactMissing):
		result.ErrorCode, result.ErrorMessage = "artifact_missing", err.Error()
	case errors.Is(err, ErrInvalidPlan):
		result.ErrorCode, result.ErrorMessage = "invalid_plan", err.Error()
	case errors.Is(err, ErrInvalidSource), errors.Is(err, ErrInvalidRef), errors.Is(err, ErrSourceUnavailable):
		result.ErrorCode, result.ErrorMessage = "invalid_source", err.Error()
	case errors.Is(err, context.Canceled):
		result.State, result.ErrorCode, result.ErrorMessage = StepCancelled, "cancelled", "deployment step cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		// The engine turns a step into a cancellation itself whenever the run's
		// own context ended; a deadline that reaches here expired while the run
		// was alive, which nobody cancelled.
		result.ErrorCode, result.ErrorMessage = "step_timeout", "an operation in this step ran out of time"
	default:
		result.ErrorMessage = err.Error()
	}
	return result
}

func shortIdentity(identity SourceIdentity) string {
	value := immutableSourceRevision(identity)
	if len(value) > 16 {
		return value[:16]
	}
	return value
}

func safeCleanupError(err error) string {
	if err == nil {
		return ""
	}
	return "cleanup incomplete"
}

type DeploymentReconciler struct {
	normalized *NormalizedStepReconciler
}

func NewDeploymentReconciler(store *OrchestrationStore) *DeploymentReconciler {
	return &DeploymentReconciler{normalized: &NormalizedStepReconciler{store: store}}
}

func (r *DeploymentReconciler) Reconcile(ctx context.Context, request ReconcileRequest) ReconcileResult {
	if request.Step == nil || request.Step.Key == StepLegacyPipeline {
		return conservativeReconciler{}.Reconcile(ctx, request)
	}
	return r.normalized.Reconcile(ctx, request)
}

type NormalizedStepReconciler struct{ store *OrchestrationStore }

func (r *NormalizedStepReconciler) Reconcile(ctx context.Context, request ReconcileRequest) ReconcileResult {
	if request.Step == nil {
		return ReconcileResult{Action: ReconcileResume}
	}
	switch request.Step.Key {
	case StepResolveSource, StepAcquireSource, StepAnalyzePlan, StepPrepareContext, StepBuildArtifact:
		// These steps affect only a deterministic private workspace or a
		// deterministic image tag built from pinned inputs. Repeating them
		// cannot touch the live runtime or route.
		return ReconcileResult{Action: ReconcileResume}
	case StepRenderRuntime:
		release, err := r.store.ReleaseForRun(ctx, request.Run.ID)
		if err == nil {
			return ReconcileResult{
				Action: ReconcileComplete,
				StepResult: StepResult{State: StepPassed, Evidence: mustJSON(renderedStepEvidence{
					ReleaseID: release.Release.ID, ReleaseNumber: release.Release.Number,
					ConfigDigest: release.Release.ConfigDigest,
				})},
			}
		}
		if errors.Is(err, ErrArtifactMissing) {
			return ReconcileResult{Action: ReconcileResume}
		}
		return ReconcileResult{Action: ReconcileFail, TerminalCode: "restart_evidence_missing", Reason: "candidate release evidence could not be read after restart"}
	case StepReleaseTask:
		return ReconcileResult{
			Action: ReconcileFail, TerminalCode: "restart_evidence_missing",
			Reason: "A release task was interrupted; its non-idempotent outcome requires operator review before retry",
		}
	default:
		return conservativeReconciler{}.Reconcile(ctx, request)
	}
}

func (s *OrchestrationStore) ReleaseForRun(ctx context.Context, runID int64) (*ReleaseWithArtifacts, error) {
	release, err := releaseByRunTx(ctx, s.db, runID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrArtifactMissing
	}
	if err != nil {
		return nil, err
	}
	artifacts, err := artifactsForReleaseTx(ctx, s.db, release.ID)
	if err != nil {
		return nil, err
	}
	return &ReleaseWithArtifacts{Release: *release, Artifacts: artifacts}, nil
}
