package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"
)

// DeploymentSummary is the operator-facing read model. Health is the persisted
// activation-check outcome; current Docker state is a separate runtime observation.
type DeploymentSummary struct {
	ID               int64           `json:"id"`
	Name             string          `json:"name"`
	Profile          WorkloadProfile `json:"profile"`
	EnvironmentID    int64           `json:"environmentId"`
	EnvironmentName  string          `json:"environmentName"`
	EnvironmentKind  EnvironmentKind `json:"environmentKind"`
	DesiredRevision  int             `json:"desiredRevision"`
	LiveReleaseID    int64           `json:"liveReleaseId,omitempty"`
	LivePlanRevision int             `json:"livePlanRevision,omitempty"`
	Strategy         ReleaseStrategy `json:"strategy"`
	ExpectedDowntime bool            `json:"expectedDowntime"`
	SourceKind       SourceKind      `json:"sourceKind"`
	BuildMethod      BuildMethod     `json:"buildMethod"`
	SourceRef        string          `json:"sourceRef,omitempty"`
	SourceRevision   string          `json:"sourceRevision,omitempty"`
	Endpoint         string          `json:"endpoint,omitempty"`
	InternalPort     int             `json:"internalPort,omitempty"`
	HostPort         int             `json:"hostPort,omitempty"`
	Health           string          `json:"health"`
	// Stopped is true when the live release's runtime is recorded as stopped
	// by a stop operation. It is a distinct fact from Health: a stopped
	// runtime has no health outcome to report at all.
	Stopped bool `json:"stopped"`
	// ServiceCount is the number of Compose services the live release's
	// runtime snapshot recorded, or 1 for any release that is not a Compose
	// build (a single container or static bundle is one service). It is 0
	// only when there is no live release yet to read a snapshot from.
	ServiceCount   int        `json:"serviceCount,omitempty"`
	PendingChanges bool       `json:"pendingChanges"`
	LastRun        *EngineRun `json:"lastRun,omitempty"`
	ActiveRun      *EngineRun `json:"activeRun,omitempty"`
	UpdatedAt      time.Time  `json:"updatedAt"`
	// SourceRepository is the identity's repository: owner/name for Git, and
	// the reference itself for an image or template, whose Ref is empty.
	// SourceRemote is where a Git source is fetched from, with any userinfo
	// removed, so the fleet can name the host without seeing a credential.
	SourceRepository string `json:"sourceRepository,omitempty"`
	SourceRemote     string `json:"sourceRemote,omitempty"`
	// Recipe and Framework say what the desired build plan builds: the
	// automatic recipe, and the framework detection recognised in the source.
	Recipe    string `json:"recipe,omitempty"`
	Framework string `json:"framework,omitempty"`
	// Images are the image references the live release runs: the single
	// container's, or one per Compose service in the order the stack lists them.
	Images []string `json:"images,omitempty"`
	// RecentRuns are the project's newest runs, newest first and at most
	// recentRunLimit, so a card can draw its run history without a request
	// per project. The first one is LastRun.
	RecentRuns []RecentRun `json:"recentRuns,omitempty"`
}

// RecentRun is one run in a deployment's history strip: its outcome and
// where to open it, without the metadata a full run carries.
type RecentRun struct {
	ID          int64      `json:"id"`
	RunNumber   int64      `json:"runNumber"`
	State       RunState   `json:"state"`
	Operation   Operation  `json:"operation"`
	RequestedAt time.Time  `json:"requestedAt"`
	EndedAt     *time.Time `json:"endedAt,omitempty"`
}

// recentRunLimit is how many runs a history strip shows: two weeks of a
// daily deploy, and still one line on a phone-width card.
const recentRunLimit = 14

// CurrentStep is where a run in flight stands. Label is the step as the
// release path names it, so a list can say what a run is doing without
// loading its snapshot.
type CurrentStep struct {
	Key   StepKey   `json:"key"`
	Label string    `json:"label"`
	State StepState `json:"state"`
}

var stepLabels = map[StepKey]string{
	StepResolveSource:        "Resolve source",
	StepAcquireSource:        "Fetch source",
	StepAnalyzePlan:          "Check plan",
	StepPrepareContext:       "Prepare build context",
	StepBuildArtifact:        "Build",
	StepRenderRuntime:        "Render runtime",
	StepReleaseTask:          "Release task",
	StepBackupGate:           "Backup check",
	StepProvisionCertificate: "Certificate",
	StepStartCandidate:       "Start new release",
	StepVerifyReadiness:      "Readiness checks",
	StepVerifySmoke:          "Smoke checks",
	StepActivate:             "Switch traffic",
	StepRetirePrevious:       "Retire previous release",
	StepRecordRelease:        "Record release",
	StepNotify:               "Notify",
	StepLegacyPipeline:       "Compatibility pipeline",
}

// DeploymentFacts are the source and build facts an archived deployment's
// plan recorded, so the archived list can draw each one as what it deployed
// rather than as a generic workload.
type DeploymentFacts struct {
	SourceKind       SourceKind  `json:"sourceKind,omitempty"`
	SourceRef        string      `json:"sourceRef,omitempty"`
	SourceRepository string      `json:"sourceRepository,omitempty"`
	BuildMethod      BuildMethod `json:"buildMethod,omitempty"`
	Recipe           string      `json:"recipe,omitempty"`
	Framework        string      `json:"framework,omitempty"`
}

type ActiveWork struct {
	Run           EngineRun `json:"run"`
	ProjectName   string    `json:"projectName"`
	Environment   string    `json:"environment"`
	CurrentStep   StepKey   `json:"currentStep,omitempty"`
	CurrentStatus StepState `json:"currentStatus,omitempty"`
	QueuePosition int       `json:"queuePosition,omitempty"`
}

type FleetSlots struct {
	HeavyUsed     int `json:"heavyUsed"`
	HeavyCapacity int `json:"heavyCapacity"`
	LightUsed     int `json:"lightUsed"`
	LightCapacity int `json:"lightCapacity"`
}

type FleetReadModel struct {
	Deployments []DeploymentSummary `json:"deployments"`
	ActiveWork  []ActiveWork        `json:"activeWork"`
	Slots       FleetSlots          `json:"slots"`
}

const activeRunWhere = `state IN (
  'requested', 'validating', 'queued', 'preparing', 'running', 'verifying',
  'activating', 'failed_activation', 'restoring_previous', 'cancelling'
)`

// Fleet returns one production summary per unarchived deployment and the
// recoverable work currently occupying or waiting for host slots.
func (s *OrchestrationStore) Fleet(ctx context.Context, budget QueueBudget) (*FleetReadModel, error) {
	return s.fleet(ctx, budget, 0)
}

// fleet reads the whole fleet, or one deployment when projectID is set. Every
// per-deployment join below is batched over the rows this query returned, so
// opening one workspace costs the same number of statements as opening the
// fleet page.
func (s *OrchestrationStore) fleet(ctx context.Context, budget QueueBudget, projectID int64) (*FleetReadModel, error) {
	budget = budget.normalized()
	result := &FleetReadModel{
		Deployments: []DeploymentSummary{}, ActiveWork: []ActiveWork{},
		Slots: FleetSlots{HeavyCapacity: budget.Heavy, LightCapacity: budget.Light},
	}
	projectFilter, projectArgs := "p.archived_at = 0", []any{}
	if projectID > 0 {
		projectFilter, projectArgs = "p.id = ?", []any{projectID}
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id, CASE WHEN p.archived_name != '' THEN p.archived_name ELSE p.name END, p.profile, p.updated_at,
		       e.id, e.name, e.kind, e.desired_revision, e.live_release_id,
		       e.strategy, e.expected_downtime,
		       COALESCE(l.plan_revision, 0),
		       COALESCE(src.kind, ''), COALESCE(NULLIF(l.source_identity_json, '{}'), src.identity_json, '{}'),
		       COALESCE(build.method, 'none'), COALESCE(build.config_json, '{}'),
		       COALESCE(runtime.config_json, '{}'), COALESCE(live_runtime.port, 0),
		       COALESCE(live_runtime.state, ''),
		       COALESCE((
		         SELECT d.resource_id FROM deploy_dependencies d
		          WHERE d.environment_id = e.id AND d.kind = 'domain'
		          ORDER BY d.id LIMIT 1
		       ), '')
		  FROM deploy_projects p
		  JOIN deploy_environments e ON e.project_id = p.id
		   AND e.slug = 'production' AND e.archived_at = 0
		  LEFT JOIN deploy_releases l ON l.id = e.live_release_id
  LEFT JOIN deploy_release_runtimes live_runtime ON live_runtime.release_id = e.live_release_id
		  LEFT JOIN deploy_sources src ON src.environment_id = e.id
		   AND src.revision = e.desired_revision
		  LEFT JOIN deploy_build_plans build ON build.environment_id = e.id
		   AND build.revision = e.desired_revision
		  LEFT JOIN deploy_runtime_plans runtime ON runtime.environment_id = e.id
		   AND runtime.revision = e.desired_revision
		 WHERE `+projectFilter+`
		 ORDER BY p.name`, projectArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var summary DeploymentSummary
		var updated int64
		var expectedDowntime, livePort int
		var identityJSON, buildJSON, runtimeJSON, liveRuntimeState string
		if err := rows.Scan(
			&summary.ID, &summary.Name, &summary.Profile, &updated,
			&summary.EnvironmentID, &summary.EnvironmentName, &summary.EnvironmentKind,
			&summary.DesiredRevision, &summary.LiveReleaseID, &summary.Strategy,
			&expectedDowntime, &summary.LivePlanRevision, &summary.SourceKind,
			&identityJSON, &summary.BuildMethod, &buildJSON, &runtimeJSON, &livePort, &liveRuntimeState,
			&summary.Endpoint,
		); err != nil {
			return nil, err
		}
		summary.ExpectedDowntime = expectedDowntime != 0
		summary.Stopped = liveRuntimeState == "stopped"
		summary.UpdatedAt = unixTime(updated)
		if updated == 0 {
			summary.UpdatedAt = time.Time{}
		}
		var identity SourceIdentity
		if json.Unmarshal([]byte(identityJSON), &identity) == nil {
			summary.SourceRef = identity.Ref
			summary.SourceRevision = identity.Revision
			if summary.SourceRevision == "" {
				summary.SourceRevision = identity.Digest
			}
			summary.SourceRepository = identity.Repository
			summary.SourceRemote = displayRemote(identity.Remote)
		}
		var build BuildPlanConfig
		if json.Unmarshal([]byte(buildJSON), &build) == nil {
			summary.Recipe, summary.Framework = build.Recipe, build.Framework
		}
		var runtime RuntimePlanConfig
		if json.Unmarshal([]byte(runtimeJSON), &runtime) == nil {
			summary.InternalPort = runtime.InternalPort
			summary.HostPort = runtime.HostPort
		}
		if livePort > 0 {
			summary.HostPort = livePort
		}
		summary.PendingChanges = summary.LiveReleaseID == 0 || summary.LivePlanRevision != summary.DesiredRevision
		result.Deployments = append(result.Deployments, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	projectIDs := make([]int64, 0, len(result.Deployments))
	releaseIDs := make([]int64, 0, len(result.Deployments))
	for _, summary := range result.Deployments {
		projectIDs = append(projectIDs, summary.ID)
		if summary.LiveReleaseID > 0 {
			releaseIDs = append(releaseIDs, summary.LiveReleaseID)
		}
	}
	health, serviceCounts, images, err := s.liveReleaseFacts(ctx, releaseIDs)
	if err != nil {
		return nil, err
	}
	lastRuns, err := s.latestProjectRuns(ctx, projectIDs, false)
	if err != nil {
		return nil, err
	}
	activeRuns, err := s.latestProjectRuns(ctx, projectIDs, true)
	if err != nil {
		return nil, err
	}
	recentRuns, err := s.recentProjectRuns(ctx, projectIDs)
	if err != nil {
		return nil, err
	}
	for index := range result.Deployments {
		summary := &result.Deployments[index]
		outcome, assessed := health[summary.LiveReleaseID]
		if !assessed {
			outcome = HealthUnavailable
		}
		summary.Health = string(outcome)
		summary.ServiceCount = serviceCounts[summary.LiveReleaseID]
		summary.Images = images[summary.LiveReleaseID]
		summary.LastRun, summary.ActiveRun = lastRuns[summary.ID], activeRuns[summary.ID]
		summary.RecentRuns = recentRuns[summary.ID]
	}

	activeRows, err := s.db.QueryContext(ctx, `
		SELECT id FROM deploy_runs WHERE `+activeRunWhere+`
		 ORDER BY CASE WHEN state = 'queued' THEN 1 ELSE 0 END,
		          priority DESC, requested_at, id`)
	if err != nil {
		return nil, err
	}
	defer activeRows.Close()
	queuePosition := 0
	for activeRows.Next() {
		var runID int64
		if err := activeRows.Scan(&runID); err != nil {
			return nil, err
		}
		run, err := s.Run(ctx, runID)
		if err != nil {
			return nil, err
		}
		work := ActiveWork{Run: *run}
		if err := s.db.QueryRowContext(ctx, `
			SELECT CASE WHEN p.archived_name != '' THEN p.archived_name ELSE p.name END, e.name FROM deploy_projects p
			JOIN deploy_environments e ON e.project_id = p.id
			WHERE p.id = ? AND e.id = ?`, run.ProjectID, run.EnvironmentID).
			Scan(&work.ProjectName, &work.Environment); err != nil {
			return nil, err
		}
		if run.State == RunQueued {
			queuePosition++
			work.QueuePosition = queuePosition
		}
		result.ActiveWork = append(result.ActiveWork, work)
	}
	if err := activeRows.Err(); err != nil {
		return nil, err
	}
	// Every run a card shows in flight is one of the host's active runs, so
	// one batched read places both the in-progress block and each card.
	activeIDs := make([]int64, 0, len(result.ActiveWork))
	for _, work := range result.ActiveWork {
		activeIDs = append(activeIDs, work.Run.ID)
	}
	steps, err := s.currentSteps(ctx, activeIDs)
	if err != nil {
		return nil, err
	}
	for index := range result.ActiveWork {
		work := &result.ActiveWork[index]
		if step := steps[work.Run.ID]; step != nil {
			work.Run.CurrentStep = step
			work.CurrentStep, work.CurrentStatus = step.Key, step.State
		}
	}
	for index := range result.Deployments {
		summary := &result.Deployments[index]
		for _, run := range []*EngineRun{summary.LastRun, summary.ActiveRun} {
			if run != nil {
				run.CurrentStep = steps[run.ID]
			}
		}
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(CASE WHEN slot_class = 'heavy' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN slot_class = 'light' THEN 1 ELSE 0 END), 0)
		  FROM deploy_queue_leases`).Scan(&result.Slots.HeavyUsed, &result.Slots.LightUsed); err != nil {
		return nil, err
	}
	return result, nil
}

// liveReleaseFacts answers, in one fixed set of statements, the questions the
// fleet read needs about every summary's live release: its health, how many
// services its runtime snapshot describes, and which images those services
// run. A health outcome is only reported when the release's own recorded
// checks were all observed; anything less stays unavailable rather than being
// rounded up to healthy. The service count and images are read from the same
// snapshot regardless of whether the runtime is currently live or stopped, so
// a stopped Compose deployment still reports what it runs.
func (s *OrchestrationStore) liveReleaseFacts(
	ctx context.Context,
	releaseIDs []int64,
) (map[int64]HealthOutcome, map[int64]int, map[int64][]string, error) {
	result := map[int64]HealthOutcome{}
	serviceCounts := map[int64]int{}
	images := map[int64][]string{}
	wanted := []int64{}
	for _, releaseID := range releaseIDs {
		if releaseID > 0 {
			if _, seen := result[releaseID]; !seen {
				result[releaseID] = HealthUnavailable
				wanted = append(wanted, releaseID)
			}
		}
	}
	if len(wanted) == 0 {
		return result, serviceCounts, images, nil
	}
	placeholders, args := inPlaceholders(wanted)

	liveRuntimes := map[int64]bool{}
	rows, err := s.db.QueryContext(ctx,
		`SELECT release_id FROM deploy_release_runtimes WHERE state = 'live' AND release_id IN `+placeholders, args...)
	if err != nil {
		return nil, nil, nil, err
	}
	for rows.Next() {
		var releaseID int64
		if err := rows.Scan(&releaseID); err != nil {
			rows.Close()
			return nil, nil, nil, err
		}
		liveRuntimes[releaseID] = true
	}
	if err := rows.Close(); err != nil {
		return nil, nil, nil, err
	}
	live := []int64{}
	for _, releaseID := range wanted {
		if liveRuntimes[releaseID] {
			live = append(live, releaseID)
		}
	}

	// Scoped to every wanted release, not only the live-runtime subset: a
	// stopped release still has a runtime snapshot worth reading for its
	// service count, even though it contributes no health evidence below.
	releases := map[int64]*ReleaseWithArtifacts{}
	rows, err = s.db.QueryContext(ctx, `SELECT `+releaseColumns+` FROM deploy_releases WHERE id IN `+placeholders, args...)
	if err != nil {
		return nil, nil, nil, err
	}
	for rows.Next() {
		release, scanErr := scanRelease(rows)
		if scanErr != nil {
			rows.Close()
			return nil, nil, nil, scanErr
		}
		releases[release.ID] = &ReleaseWithArtifacts{Release: *release, Artifacts: []ReleaseArtifact{}}
	}
	if err := rows.Close(); err != nil {
		return nil, nil, nil, err
	}
	rows, err = s.db.QueryContext(ctx, `
		SELECT id, release_id, kind, reference, digest, metadata_json, size_bytes,
		       retain_until, state, created_at
		  FROM deploy_release_artifacts WHERE release_id IN `+placeholders+` ORDER BY id`, args...)
	if err != nil {
		return nil, nil, nil, err
	}
	for rows.Next() {
		var artifact ReleaseArtifact
		var metadata string
		var retainUntil, createdAt int64
		if err := rows.Scan(&artifact.ID, &artifact.ReleaseID, &artifact.Kind, &artifact.Reference,
			&artifact.Digest, &metadata, &artifact.SizeBytes, &retainUntil, &artifact.State,
			&createdAt); err != nil {
			rows.Close()
			return nil, nil, nil, err
		}
		artifact.Metadata = json.RawMessage(metadata)
		artifact.RetainUntil, artifact.CreatedAt = unixTimePtr(retainUntil), unixTime(createdAt)
		if release, found := releases[artifact.ReleaseID]; found {
			release.Artifacts = append(release.Artifacts, artifact)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, nil, nil, err
	}

	for _, releaseID := range wanted {
		release, found := releases[releaseID]
		if !found {
			continue
		}
		snapshot, snapshotErr := decodeReleaseRuntimeSnapshot(release)
		if snapshotErr != nil {
			continue
		}
		count := 1
		if snapshot.Compose != nil && len(snapshot.Compose.Services) > 0 {
			count = len(snapshot.Compose.Services)
		}
		serviceCounts[releaseID] = count
		images[releaseID] = snapshotImages(snapshot)
	}

	if len(live) == 0 {
		return result, serviceCounts, images, nil
	}
	livePlaceholders, liveArgs := inPlaceholders(live)

	// The newest run of each release carries the evidence its checks produced.
	healthRuns := map[int64]int64{}
	runReleases := map[int64]int64{}
	rows, err = s.db.QueryContext(ctx, `
		SELECT release_id, id FROM (
		  SELECT release_id, id,
		         ROW_NUMBER() OVER (PARTITION BY release_id ORDER BY id DESC) AS rank
		    FROM deploy_runs WHERE release_id IN `+livePlaceholders+`
		) WHERE rank = 1`, liveArgs...)
	if err != nil {
		return nil, nil, nil, err
	}
	for rows.Next() {
		var releaseID, runID int64
		if err := rows.Scan(&releaseID, &runID); err != nil {
			rows.Close()
			return nil, nil, nil, err
		}
		healthRuns[releaseID], runReleases[runID] = runID, releaseID
	}
	if err := rows.Close(); err != nil {
		return nil, nil, nil, err
	}
	if len(runReleases) == 0 {
		return result, serviceCounts, images, nil
	}
	runIDs := make([]int64, 0, len(runReleases))
	for runID := range runReleases {
		runIDs = append(runIDs, runID)
	}
	sort.Slice(runIDs, func(i, j int) bool { return runIDs[i] < runIDs[j] })
	runPlaceholders, runArgs := inPlaceholders(runIDs)
	evidence := map[int64]map[StepKey]json.RawMessage{}
	rows, err = s.db.QueryContext(ctx, `
		SELECT run_id, step_key, evidence_json FROM deploy_steps
		 WHERE run_id IN `+runPlaceholders+`
		   AND step_key IN ('verify_readiness','verify_smoke','activate')
		   AND status IN ('passed','warning','skipped')
		 ORDER BY attempt DESC`, runArgs...)
	if err != nil {
		return nil, nil, nil, err
	}
	for rows.Next() {
		var runID int64
		var key StepKey
		var raw string
		if err := rows.Scan(&runID, &key, &raw); err != nil {
			rows.Close()
			return nil, nil, nil, err
		}
		if evidence[runID] == nil {
			evidence[runID] = map[StepKey]json.RawMessage{}
		}
		if _, exists := evidence[runID][key]; !exists {
			evidence[runID][key] = json.RawMessage(raw)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, nil, nil, err
	}

	for _, releaseID := range live {
		release, found := releases[releaseID]
		if !found {
			continue
		}
		snapshot, snapshotErr := decodeReleaseRuntimeSnapshot(release)
		if snapshotErr != nil {
			continue
		}
		if len(snapshot.Checks) == 0 {
			result[releaseID] = HealthDisabled
			continue
		}
		runID, hasRun := healthRuns[releaseID]
		if !hasRun {
			continue
		}
		result[releaseID] = healthFromEvidence(snapshot.Checks, evidence[runID])
	}
	return result, serviceCounts, images, nil
}

func healthFromEvidence(checks []PlannedCheck, latest map[StepKey]json.RawMessage) HealthOutcome {
	observed := []CheckEvidence{}
	for _, key := range []StepKey{StepVerifyReadiness, StepVerifySmoke} {
		if raw := latest[key]; len(raw) != 0 {
			var stepEvidence checkStepEvidence
			if json.Unmarshal(raw, &stepEvidence) != nil {
				return HealthUnavailable
			}
			observed = append(observed, stepEvidence.Checks...)
		}
	}
	if raw := latest[StepActivate]; len(raw) != 0 {
		var stepEvidence activationStepEvidence
		if json.Unmarshal(raw, &stepEvidence) != nil {
			return HealthUnavailable
		}
		observed = append(observed, stepEvidence.PublicChecks...)
	}
	seen := map[string]bool{}
	for _, check := range observed {
		seen[check.Phase+"\x00"+check.Kind+"\x00"+check.Name] = true
	}
	for _, check := range checks {
		if !seen[check.Phase+"\x00"+check.Kind+"\x00"+check.Name] {
			return HealthUnavailable
		}
	}
	return summarizeChecks(observed)
}

// latestProjectRuns reads the newest run, or newest recoverable run, for every
// named project in one statement.
func (s *OrchestrationStore) latestProjectRuns(
	ctx context.Context,
	projectIDs []int64,
	active bool,
) (map[int64]*EngineRun, error) {
	result := map[int64]*EngineRun{}
	if len(projectIDs) == 0 {
		return result, nil
	}
	placeholders, args := inPlaceholders(projectIDs)
	filter := ""
	if active {
		filter = " AND " + activeRunWhere
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+engineRunColumns+` FROM (
		  SELECT `+engineRunColumns+`,
		         ROW_NUMBER() OVER (PARTITION BY project_id ORDER BY requested_at DESC, id DESC) AS rank
		    FROM deploy_runs WHERE project_id IN `+placeholders+filter+`
		) WHERE rank = 1`, args...)
	if err != nil {
		return nil, fmt.Errorf("read deployment run: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		run, scanErr := scanEngineRun(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result[run.ProjectID] = run
	}
	return result, rows.Err()
}

// recentProjectRuns reads every named project's newest runs in one statement,
// ordered the way latestProjectRuns picks the last run so the two agree.
func (s *OrchestrationStore) recentProjectRuns(
	ctx context.Context,
	projectIDs []int64,
) (map[int64][]RecentRun, error) {
	result := map[int64][]RecentRun{}
	if len(projectIDs) == 0 {
		return result, nil
	}
	placeholders, args := inPlaceholders(projectIDs)
	rows, err := s.db.QueryContext(ctx, `
		SELECT project_id, id, run_number, state, operation, requested_at, ended_at FROM (
		  SELECT project_id, id, run_number, state, operation, requested_at, ended_at,
		         ROW_NUMBER() OVER (PARTITION BY project_id ORDER BY requested_at DESC, id DESC) AS rank
		    FROM deploy_runs WHERE project_id IN `+placeholders+`
		) WHERE rank <= ? ORDER BY project_id, rank`, append(args, recentRunLimit)...)
	if err != nil {
		return nil, fmt.Errorf("read recent deployment runs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var projectID, requested, ended int64
		var run RecentRun
		if err := rows.Scan(&projectID, &run.ID, &run.RunNumber, &run.State, &run.Operation, &requested, &ended); err != nil {
			return nil, err
		}
		run.RequestedAt, run.EndedAt = unixTime(requested), unixTimePtr(ended)
		result[projectID] = append(result[projectID], run)
	}
	return result, rows.Err()
}

// currentSteps reads where each named run stands in one statement: the step
// it is running, else the one blocking it, else a failed one, else the next
// one waiting. A run without steps is absent from the result.
func (s *OrchestrationStore) currentSteps(ctx context.Context, runIDs []int64) (map[int64]*CurrentStep, error) {
	result := map[int64]*CurrentStep{}
	if len(runIDs) == 0 {
		return result, nil
	}
	placeholders, args := inPlaceholders(runIDs)
	rows, err := s.db.QueryContext(ctx, `
		SELECT run_id, step_key, status FROM (
		  SELECT run_id, step_key, status,
		         ROW_NUMBER() OVER (
		           PARTITION BY run_id
		           ORDER BY CASE status WHEN 'running' THEN 0 WHEN 'blocked' THEN 1
		                    WHEN 'failed' THEN 2 ELSE 3 END, ordinal, attempt DESC
		         ) AS rank
		    FROM deploy_steps
		   WHERE run_id IN `+placeholders+` AND status IN ('running','blocked','failed','pending')
		) WHERE rank = 1`, args...)
	if err != nil {
		return nil, fmt.Errorf("read current deployment steps: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var runID int64
		step := &CurrentStep{}
		if err := rows.Scan(&runID, &step.Key, &step.State); err != nil {
			return nil, err
		}
		step.Label = stepLabels[step.Key]
		result[runID] = step
	}
	return result, rows.Err()
}

// snapshotImages lists the image references a release's runtime snapshot
// runs, without repeats.
func snapshotImages(snapshot runtimeReleaseSnapshot) []string {
	references := []string{snapshot.Image.Reference}
	if snapshot.Compose != nil {
		references = references[:0]
		for _, service := range snapshot.Compose.Services {
			references = append(references, service.Reference)
		}
	}
	result := []string{}
	for _, reference := range references {
		if reference != "" && !slices.Contains(result, reference) {
			result = append(result, reference)
		}
	}
	return result
}

// displayRemote is a Git remote as the fleet may show it: a URL reduced to
// scheme, host and path, or an SCP-style host:path. Userinfo is dropped — a
// remote recorded before credentials were refused in URLs could carry a token
// there — and anything that does not parse is left out rather than echoed.
func displayRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}
	if !strings.Contains(remote, "://") {
		host, path, ok := strings.Cut(remote, ":")
		user, afterUser, hasUser := strings.Cut(host, "@")
		if hasUser {
			host = afterUser
		}
		if !ok || !validRemoteHost(host) || path == "" {
			return ""
		}
		// git@ is the SSH service account every forge uses, not a secret, and
		// it is the spelling a host is recognised by.
		if user == "git" {
			return "git@" + strings.ToLower(host) + ":" + path
		}
		return strings.ToLower(host) + ":" + path
	}
	parsed, err := url.Parse(remote)
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	return (&url.URL{Scheme: parsed.Scheme, Host: strings.ToLower(parsed.Host), Path: parsed.Path}).String()
}

// ArchivedDeploymentFacts reads every archived project's source and build
// facts in one statement, keyed by project id. A legacy project without a
// production environment is absent.
func (s *OrchestrationStore) ArchivedDeploymentFacts(ctx context.Context) (map[int64]DeploymentFacts, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id, COALESCE(src.kind, ''),
		       COALESCE(NULLIF(l.source_identity_json, '{}'), src.identity_json, '{}'),
		       COALESCE(build.method, ''), COALESCE(build.config_json, '{}')
		  FROM deploy_projects p
		  JOIN deploy_environments e ON e.project_id = p.id
		   AND e.slug = 'production' AND e.archived_at = 0
		  LEFT JOIN deploy_releases l ON l.id = e.live_release_id
		  LEFT JOIN deploy_sources src ON src.environment_id = e.id
		   AND src.revision = e.desired_revision
		  LEFT JOIN deploy_build_plans build ON build.environment_id = e.id
		   AND build.revision = e.desired_revision
		 WHERE p.archived_at != 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[int64]DeploymentFacts{}
	for rows.Next() {
		var projectID int64
		var facts DeploymentFacts
		var identityJSON, buildJSON string
		if err := rows.Scan(&projectID, &facts.SourceKind, &identityJSON, &facts.BuildMethod, &buildJSON); err != nil {
			return nil, err
		}
		var identity SourceIdentity
		if json.Unmarshal([]byte(identityJSON), &identity) == nil {
			facts.SourceRef, facts.SourceRepository = identity.Ref, identity.Repository
		}
		var build BuildPlanConfig
		if json.Unmarshal([]byte(buildJSON), &build) == nil {
			facts.Recipe, facts.Framework = build.Recipe, build.Framework
		}
		result[projectID] = facts
	}
	return result, rows.Err()
}

// inPlaceholders keeps IN lists parameterized. Deployment counts are bounded by
// the fleet, so one statement per read stays well inside SQLite's variable cap.
func inPlaceholders(values []int64) (string, []any) {
	args := make([]any, 0, len(values))
	for _, value := range values {
		args = append(args, value)
	}
	return "(?" + strings.Repeat(",?", len(values)-1) + ")", args
}

func (s *OrchestrationStore) DeploymentBuildMethod(ctx context.Context, projectID int64) (BuildMethod, error) {
	var method BuildMethod
	err := s.db.QueryRowContext(ctx, `
		SELECT b.method FROM deploy_environments e
		JOIN deploy_build_plans b ON b.environment_id = e.id AND b.revision = e.desired_revision
		WHERE e.project_id = ? AND e.slug = 'production' AND e.archived_at = 0`, projectID).Scan(&method)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrEnvironmentNotFound
	}
	return method, err
}

func (s *OrchestrationStore) DeploymentSummary(ctx context.Context, projectID int64, budget QueueBudget) (*DeploymentSummary, error) {
	fleet, err := s.fleet(ctx, budget, projectID)
	if err != nil {
		return nil, err
	}
	if len(fleet.Deployments) == 0 {
		return nil, ErrNotFound
	}
	return &fleet.Deployments[0], nil
}

func (s *OrchestrationStore) ProjectRuns(ctx context.Context, projectID int64, limit int) ([]EngineRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+engineRunColumns+`
		FROM deploy_runs WHERE project_id = ? ORDER BY requested_at DESC, id DESC LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []EngineRun{}
	for rows.Next() {
		run, err := scanEngineRun(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *run)
	}
	return result, rows.Err()
}

// RunListFilter narrows ProjectRunsFiltered's result to one environment
// and/or operation and/or state, and pages it with a "before" run id cursor.
// State accepts a literal RunState, "terminal" (any of the five terminal
// states) or "active" (everything else); Limit is clamped to (0,200] the
// same way ProjectRuns clamps it, defaulting to 30.
type RunListFilter struct {
	Environment int64
	Operation   Operation
	State       string
	Before      int64
	Limit       int
}

// ProjectRunsFiltered lists a project's runs newest-first, optionally
// narrowed by RunListFilter and paginated by its Before cursor. nextBefore is
// 0 when no older row remains; otherwise it is the id a caller passes back as
// Before to fetch the next page.
func (s *OrchestrationStore) ProjectRunsFiltered(
	ctx context.Context, projectID int64, filter RunListFilter,
) ([]EngineRun, int64, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	where := []string{"project_id = ?"}
	args := []any{projectID}
	if filter.Environment > 0 {
		where = append(where, "environment_id = ?")
		args = append(args, filter.Environment)
	}
	if filter.Operation != "" {
		where = append(where, "operation = ?")
		args = append(args, filter.Operation)
	}
	switch filter.State {
	case "":
	// "active" is exactly activeRunWhere's own state list, and "terminal" is
	// its complement — derived from the same constant so this can't drift
	// into a second, inversely-spelled copy of the terminal state list.
	case "active":
		where = append(where, activeRunWhere)
	case "terminal":
		where = append(where, "NOT "+activeRunWhere)
	default:
		where = append(where, "state = ?")
		args = append(args, filter.State)
	}
	if filter.Before > 0 {
		where = append(where, "id < ?")
		args = append(args, filter.Before)
	}
	// One extra row reveals whether another page remains without a second
	// round trip.
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `SELECT `+engineRunColumns+`
		FROM deploy_runs WHERE `+strings.Join(where, " AND ")+`
		ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	result := []EngineRun{}
	for rows.Next() {
		run, err := scanEngineRun(rows)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, *run)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var nextBefore int64
	if len(result) > limit {
		nextBefore = result[limit-1].ID
		result = result[:limit]
	}
	inFlight := []int64{}
	for _, run := range result {
		if !run.State.Terminal() {
			inFlight = append(inFlight, run.ID)
		}
	}
	steps, err := s.currentSteps(ctx, inFlight)
	if err != nil {
		return nil, 0, err
	}
	for index := range result {
		result[index].CurrentStep = steps[result[index].ID]
	}
	return result, nextBefore, nil
}

func (s *OrchestrationStore) latestProjectRun(ctx context.Context, projectID int64, active bool) (*EngineRun, error) {
	query := `SELECT ` + engineRunColumns + ` FROM deploy_runs WHERE project_id = ?`
	if active {
		query += ` AND ` + activeRunWhere
	}
	query += ` ORDER BY requested_at DESC, id DESC LIMIT 1`
	run, err := scanEngineRun(s.db.QueryRowContext(ctx, query, projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read deployment run: %w", err)
	}
	return run, nil
}
