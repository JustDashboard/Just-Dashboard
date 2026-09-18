package deploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// These closed vocabularies are shared by persistence, handlers and stream
// clients. Adding a value is an API and recovery decision, not a free-form
// string accepted at one boundary.
type WorkloadProfile string

const (
	ProfileWeb      WorkloadProfile = "web"
	ProfileStatic   WorkloadProfile = "static"
	ProfileWorker   WorkloadProfile = "worker"
	ProfileImage    WorkloadProfile = "image"
	ProfileCompose  WorkloadProfile = "compose"
	ProfileService  WorkloadProfile = "service"
	ProfileGame     WorkloadProfile = "game"
	ProfileImported WorkloadProfile = "imported"
)

type EnvironmentKind string

const (
	EnvironmentProduction EnvironmentKind = "production"
	EnvironmentStaging    EnvironmentKind = "staging"
	EnvironmentPreview    EnvironmentKind = "preview"
)

type OwnershipMode string

const (
	OwnershipManaged  OwnershipMode = "managed"
	OwnershipLinked   OwnershipMode = "linked"
	OwnershipObserved OwnershipMode = "observed"
)

type SourceKind string

const (
	SourceGit       SourceKind = "git"
	SourceLocal     SourceKind = "local"
	SourceImage     SourceKind = "image"
	SourceCompose   SourceKind = "compose"
	SourceBlueprint SourceKind = "blueprint"
	SourceImport    SourceKind = "import"
)

type BuildMethod string

const (
	BuildRecipe        BuildMethod = "recipe"
	BuildDockerfile    BuildMethod = "dockerfile"
	BuildStatic        BuildMethod = "static"
	BuildImage         BuildMethod = "image"
	BuildCompose       BuildMethod = "compose"
	BuildNone          BuildMethod = "none"
	BuildLegacyCompose BuildMethod = "legacy_compose"
)

type ReleaseStrategy string

const (
	StrategyBlueGreen ReleaseStrategy = "blue_green"
	StrategyStopFirst ReleaseStrategy = "stop_first"
)

type Operation string

const (
	OperationDeploy        Operation = "deploy"
	OperationRedeploy      Operation = "redeploy"
	OperationRestart       Operation = "restart"
	OperationStop          Operation = "stop"
	OperationStart         Operation = "start"
	OperationForceBuild    Operation = "force_build"
	OperationRollback      Operation = "rollback"
	OperationPreviewCreate Operation = "preview_create"
	OperationPreviewUpdate Operation = "preview_update"
	OperationPreviewRemove Operation = "preview_remove"
	OperationScheduled     Operation = "scheduled_action"
	OperationImportAdopt   Operation = "import_adopt"
	OperationRemoveManaged Operation = "remove_managed"
)

type TriggerKind string

const (
	TriggerManual      TriggerKind = "manual"
	TriggerGitPush     TriggerKind = "git_push"
	TriggerLegacyHook  TriggerKind = "legacy_hook"
	TriggerGenericHook TriggerKind = "generic_hook"
	TriggerGitHub      TriggerKind = "github"
	TriggerGitLab      TriggerKind = "gitlab"
	TriggerBitbucket   TriggerKind = "bitbucket"
	TriggerGitea       TriggerKind = "gitea"
	TriggerAPI         TriggerKind = "api"
	TriggerSchedule    TriggerKind = "schedule"
	TriggerPreview     TriggerKind = "preview"
	TriggerRollback    TriggerKind = "rollback"
	TriggerMigration   TriggerKind = "migration"
)

type RunState string

const (
	RunRequested         RunState = "requested"
	RunValidating        RunState = "validating"
	RunQueued            RunState = "queued"
	RunPreparing         RunState = "preparing"
	RunRunning           RunState = "running"
	RunVerifying         RunState = "verifying"
	RunActivating        RunState = "activating"
	RunFailedActivation  RunState = "failed_activation"
	RunRestoringPrevious RunState = "restoring_previous"
	RunCancelling        RunState = "cancelling"
	RunSucceeded         RunState = "succeeded"
	RunFailed            RunState = "failed"
	RunCancelled         RunState = "cancelled"
	RunRolledBack        RunState = "rolled_back"
	RunSuperseded        RunState = "superseded"
)

var runTransitions = map[RunState]map[RunState]struct{}{
	RunRequested:         stateSet(RunValidating, RunCancelling, RunFailed),
	RunValidating:        stateSet(RunQueued, RunCancelling, RunFailed),
	RunQueued:            stateSet(RunPreparing, RunCancelling, RunSuperseded, RunFailed),
	RunPreparing:         stateSet(RunRunning, RunCancelling, RunFailed),
	RunRunning:           stateSet(RunVerifying, RunCancelling, RunFailed),
	RunVerifying:         stateSet(RunActivating, RunCancelling, RunFailed),
	RunActivating:        stateSet(RunSucceeded, RunFailedActivation),
	RunFailedActivation:  stateSet(RunRestoringPrevious, RunFailed),
	RunRestoringPrevious: stateSet(RunRolledBack, RunFailed),
	RunCancelling:        stateSet(RunCancelled, RunFailed),
}

var allRunStates = []RunState{
	RunRequested, RunValidating, RunQueued, RunPreparing, RunRunning, RunVerifying,
	RunActivating, RunFailedActivation, RunRestoringPrevious, RunCancelling,
	RunSucceeded, RunFailed, RunCancelled, RunRolledBack, RunSuperseded,
}

func (s RunState) Terminal() bool {
	switch s {
	case RunSucceeded, RunFailed, RunCancelled, RunRolledBack, RunSuperseded:
		return true
	default:
		return false
	}
}

// ValidRunState reports whether s is one of the closed run-state vocabulary,
// for callers (a run-history filter, for instance) that accept one from a
// client and must reject anything else rather than silently matching nothing.
func ValidRunState(s RunState) bool {
	for _, state := range allRunStates {
		if s == state {
			return true
		}
	}
	return false
}

// candidateFailureStates are the run states after which a run's candidate
// release can never become live. render_runtime leaves a fresh release row at
// state "candidate"; TransitionRun moves it to "failed" the moment the run
// reaches one of these, so ArtifactRetentionPlan's seven-day diagnostic
// window can apply and the releases list stops showing it as still in
// progress. failed_activation is included even though it is not itself
// terminal: activation has already failed by then, independent of whether
// the run goes on to roll the predecessor back successfully or fails doing
// that too, so the candidate is already dead when this state is reached.
var candidateFailureStates = stateSet(RunFailed, RunFailedActivation, RunRolledBack, RunCancelled)

func CanTransitionRun(from, to RunState) bool {
	_, ok := runTransitions[from][to]
	return ok
}

var ErrInvalidTransition = errors.New("invalid deployment state transition")

func ValidateRunTransition(from, to RunState) error {
	if !CanTransitionRun(from, to) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, to)
	}
	return nil
}

type StepKey string

const (
	StepResolveSource  StepKey = "resolve_source"
	StepAcquireSource  StepKey = "acquire_source"
	StepAnalyzePlan    StepKey = "analyze_plan"
	StepPrepareContext StepKey = "prepare_context"
	StepBuildArtifact  StepKey = "build_artifact"
	StepRenderRuntime  StepKey = "render_runtime"
	StepReleaseTask    StepKey = "release_task"
	StepBackupGate     StepKey = "backup_gate"
	// StepProvisionCertificate runs before anything is started, so a run that
	// cannot get the certificate its own plan asked for fails with nothing
	// left behind — and so that activation, which only ever resolves an
	// existing pair, finds one there.
	StepProvisionCertificate StepKey = "provision_certificate"
	StepStartCandidate       StepKey = "start_candidate"
	StepVerifyReadiness      StepKey = "verify_readiness"
	StepVerifySmoke          StepKey = "verify_smoke"
	StepActivate             StepKey = "activate"
	StepRetirePrevious       StepKey = "retire_previous"
	StepRecordRelease        StepKey = "record_release"
	StepNotify               StepKey = "notify"
	StepLegacyPipeline       StepKey = "legacy_pipeline"
)

var DefaultStepKeys = []StepKey{
	StepResolveSource, StepAcquireSource, StepAnalyzePlan, StepPrepareContext,
	StepBuildArtifact, StepRenderRuntime, StepReleaseTask, StepBackupGate,
	StepProvisionCertificate, StepStartCandidate, StepVerifyReadiness, StepVerifySmoke,
	StepActivate, StepRetirePrevious, StepRecordRelease, StepNotify,
}

type StepState string

const (
	StepPending     StepState = "pending"
	StepBlocked     StepState = "blocked"
	StepRunning     StepState = "running"
	StepPassed      StepState = "passed"
	StepWarning     StepState = "warning"
	StepFailed      StepState = "failed"
	StepSkipped     StepState = "skipped"
	StepCancelled   StepState = "cancelled"
	StepUnavailable StepState = "unavailable"
)

var stepTransitions = map[StepState]map[StepState]struct{}{
	StepPending: stateSet(StepBlocked, StepRunning, StepSkipped, StepCancelled, StepUnavailable),
	StepBlocked: stateSet(StepPending, StepRunning, StepFailed, StepCancelled, StepUnavailable),
	// `skipped` is reachable from `running` as well as from `pending`: a step
	// that has to read the plan before it knows there is nothing to do — no
	// release task, no backup gate, no predecessor to retire, no channel to
	// notify — can only discover that after it has started. Without the edge
	// those executors returned a state the store refused, the worker died
	// mid-run, and the step stayed `running` with no transcript for ever.
	StepRunning: stateSet(StepPassed, StepWarning, StepFailed, StepSkipped, StepCancelled, StepUnavailable),
	// A failed attempt may return to pending only when the store increments
	// attempt in the same transaction. The transition vocabulary remains one
	// closed edge; the persistence method enforces the attempt condition.
	StepFailed: stateSet(StepPending),
}

var allStepStates = []StepState{
	StepPending, StepBlocked, StepRunning, StepPassed, StepWarning,
	StepFailed, StepSkipped, StepCancelled, StepUnavailable,
}

func CanTransitionStep(from, to StepState) bool {
	_, ok := stepTransitions[from][to]
	return ok
}

func ValidateStepTransition(from, to StepState) error {
	if !CanTransitionStep(from, to) {
		return fmt.Errorf("%w: step %s -> %s", ErrInvalidTransition, from, to)
	}
	return nil
}

type CheckKind string

const (
	CheckHTTP            CheckKind = "http"
	CheckTCP             CheckKind = "tcp"
	CheckDockerHealth    CheckKind = "docker_health"
	CheckCommand         CheckKind = "command"
	CheckPublicRoute     CheckKind = "public_route"
	CheckDNS             CheckKind = "dns"
	CheckTLS             CheckKind = "tls"
	CheckGameHandshake   CheckKind = "game_handshake"
	CheckBackupFreshness CheckKind = "backup_freshness"
)

type EventType string

const (
	EventRunState      EventType = "run.state"
	EventStepState     EventType = "step.state"
	EventStepLog       EventType = "step.log"
	EventStepEvidence  EventType = "step.evidence"
	EventQueuePosition EventType = "queue.position"
	EventFinding       EventType = "finding"
	EventArtifact      EventType = "artifact"
	EventHeartbeat     EventType = "heartbeat"
	EventResync        EventType = "resync"
)

type RunEvent struct {
	Seq    int64           `json:"seq"`
	Type   EventType       `json:"type"`
	RunID  int64           `json:"runId"`
	StepID int64           `json:"stepId,omitempty"`
	TS     time.Time       `json:"ts"`
	Data   json.RawMessage `json:"data"`
}

type PreflightSeverity string

const (
	PreflightPass        PreflightSeverity = "pass"
	PreflightDecision    PreflightSeverity = "decision"
	PreflightWarning     PreflightSeverity = "warning"
	PreflightBlocked     PreflightSeverity = "blocked"
	PreflightUnavailable PreflightSeverity = "unavailable"
)

type PreflightFinding struct {
	Code     string            `json:"code"`
	Severity PreflightSeverity `json:"severity"`
	Title    string            `json:"title"`
	Measured string            `json:"measured,omitempty"`
	Means    string            `json:"means,omitempty"`
	Action   string            `json:"action,omitempty"`
	Owner    string            `json:"owner,omitempty"`
	FieldID  string            `json:"fieldId,omitempty"`
	DeepLink string            `json:"deepLink,omitempty"`
}

func stateSet[T ~string](states ...T) map[T]struct{} {
	out := make(map[T]struct{}, len(states))
	for _, state := range states {
		out[state] = struct{}{}
	}
	return out
}

// ValidOperation reports whether value is one of the closed operation
// vocabulary, for callers (a run-history filter, for instance) that accept
// one from a client and must reject anything else rather than silently
// matching nothing.
func ValidOperation(value Operation) bool {
	switch value {
	case OperationDeploy, OperationRedeploy, OperationRestart, OperationStop, OperationStart,
		OperationForceBuild, OperationRollback, OperationPreviewCreate, OperationPreviewUpdate,
		OperationPreviewRemove, OperationScheduled, OperationImportAdopt,
		OperationRemoveManaged:
		return true
	default:
		return false
	}
}

func validTrigger(value TriggerKind) bool {
	switch value {
	case TriggerManual, TriggerGitPush, TriggerLegacyHook, TriggerGenericHook, TriggerGitHub,
		TriggerGitLab, TriggerBitbucket, TriggerGitea, TriggerAPI, TriggerSchedule,
		TriggerPreview, TriggerRollback, TriggerMigration:
		return true
	default:
		return false
	}
}

func validStepKey(value StepKey) bool {
	for _, key := range DefaultStepKeys {
		if value == key {
			return true
		}
	}
	return value == StepLegacyPipeline
}

func validEventType(value EventType) bool {
	switch value {
	case EventRunState, EventStepState, EventStepLog, EventStepEvidence,
		EventQueuePosition, EventFinding, EventArtifact, EventHeartbeat, EventResync:
		return true
	default:
		return false
	}
}
