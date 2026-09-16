package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type ArtifactKind string

const (
	ArtifactImage         ArtifactKind = "image"
	ArtifactCompose       ArtifactKind = "compose"
	ArtifactRuntimeConfig ArtifactKind = "runtime_config"
	ArtifactStaticBundle  ArtifactKind = "static_bundle"
	ArtifactSourceArchive ArtifactKind = "source_archive"
	ArtifactBackup        ArtifactKind = "backup"
	ArtifactDiagnostic    ArtifactKind = "diagnostic"
	ArtifactExport        ArtifactKind = "export"
)

type Release struct {
	ID                   int64           `json:"id"`
	ProjectID            int64           `json:"projectId"`
	EnvironmentID        int64           `json:"environmentId"`
	Number               int64           `json:"number"`
	RunID                int64           `json:"runId"`
	PredecessorReleaseID int64           `json:"predecessorReleaseId,omitempty"`
	State                string          `json:"state"`
	PlanRevision         int             `json:"planRevision"`
	SourceID             int64           `json:"sourceId"`
	BuildPlanID          int64           `json:"buildPlanId"`
	RuntimePlanID        int64           `json:"runtimePlanId"`
	SourceRevision       string          `json:"sourceRevision,omitempty"`
	SourceIdentity       json.RawMessage `json:"sourceIdentity"`
	ImageDigest          string          `json:"imageDigest,omitempty"`
	ConfigDigest         string          `json:"configDigest"`
	VariablesDigest      string          `json:"variablesDigest"`
	Strategy             ReleaseStrategy `json:"strategy"`
	ExpectedDowntime     bool            `json:"expectedDowntime"`
	Provenance           json.RawMessage `json:"provenance"`
	BlueprintID          string          `json:"blueprintId,omitempty"`
	BlueprintVersion     string          `json:"blueprintVersion,omitempty"`
	CreatedAt            time.Time       `json:"createdAt"`
	ActivatedAt          *time.Time      `json:"activatedAt,omitempty"`
	RetiredAt            *time.Time      `json:"retiredAt,omitempty"`
	Pinned               bool            `json:"pinned"`
}

type ReleaseArtifact struct {
	ID          int64           `json:"id"`
	ReleaseID   int64           `json:"releaseId"`
	Kind        ArtifactKind    `json:"kind"`
	Reference   string          `json:"reference"`
	Digest      string          `json:"digest"`
	Metadata    json.RawMessage `json:"metadata"`
	SizeBytes   int64           `json:"sizeBytes"`
	RetainUntil *time.Time      `json:"retainUntil,omitempty"`
	State       string          `json:"state"`
	CreatedAt   time.Time       `json:"createdAt"`
}

type ReleaseArtifactInput struct {
	Kind        ArtifactKind    `json:"kind"`
	Reference   string          `json:"reference"`
	Digest      string          `json:"digest"`
	Metadata    json.RawMessage `json:"metadata"`
	SizeBytes   int64           `json:"sizeBytes"`
	RetainUntil time.Time       `json:"retainUntil,omitempty"`
}

type StoredBuildEvidence struct {
	Candidates      []DetectedCandidate `json:"candidates"`
	Compose         *ComposeAnalysis    `json:"compose,omitempty"`
	GitRequirements GitRequirements     `json:"gitRequirements"`
}

type ReleaseVariableSnapshot struct {
	Name        string `json:"name"`
	Sensitivity string `json:"sensitivity"`
	Scopes      string `json:"scopes"`
	ValueDigest string `json:"valueDigest"`
}

type StoredExecutionPlan struct {
	SourceID         int64
	SourceKind       SourceKind
	SourceConfig     DraftSourceConfig
	SourceIdentity   SourceIdentity
	SourceDigest     string
	BuildPlanID      int64
	Build            BuildPlanConfig
	BuildEvidence    StoredBuildEvidence
	BuildPreview     string
	BuildDigest      string
	RuntimePlanID    int64
	Runtime          RuntimePlanConfig
	RuntimePreview   string
	RuntimeDigest    string
	ExpectedDowntime bool
	Variables        []ReleaseVariableSnapshot
	VariablesDigest  string
	Dependencies     []json.RawMessage
	Checks           []json.RawMessage
	PlanInputsDigest string
}

type CandidateReleaseInput struct {
	Artifacts        []ReleaseArtifactInput
	Prepared         PreparedBuild
	RuntimeSnapshot  json.RawMessage
	RuntimeDigest    string
	BlueprintID      string
	BlueprintVersion string
}

type ReleaseWithArtifacts struct {
	Release   Release           `json:"release"`
	Artifacts []ReleaseArtifact `json:"artifacts"`
}

type EnvironmentExecutionTarget struct {
	Kind            EnvironmentKind
	ProjectID       int64
	EnvironmentID   int64
	DesiredRevision int
	BuildMethod     BuildMethod
	LiveReleaseID   int64
}

func (s *OrchestrationStore) EnvironmentExecutionTarget(
	ctx context.Context,
	projectID, environmentID int64,
) (*EnvironmentExecutionTarget, error) {
	var target EnvironmentExecutionTarget
	err := s.db.QueryRowContext(ctx, `
		SELECT e.project_id, e.id, e.desired_revision, b.method, e.live_release_id, e.kind
		  FROM deploy_environments e
		  JOIN deploy_build_plans b ON b.environment_id = e.id AND b.revision = e.desired_revision
		 WHERE e.id = ? AND e.project_id = ? AND e.archived_at = 0`, environmentID, projectID).
		Scan(&target.ProjectID, &target.EnvironmentID, &target.DesiredRevision, &target.BuildMethod, &target.LiveReleaseID, &target.Kind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrEnvironmentNotFound
	}
	return &target, err
}

func (s *OrchestrationStore) EnvironmentReleases(
	ctx context.Context,
	projectID, environmentID int64,
	limit int,
) ([]Release, error) {
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	if _, err := s.EnvironmentExecutionTarget(ctx, projectID, environmentID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+releaseColumns+`
		FROM deploy_releases WHERE project_id = ? AND environment_id = ?
		ORDER BY release_number DESC LIMIT ?`, projectID, environmentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	releases := []Release{}
	for rows.Next() {
		release, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		releases = append(releases, *release)
	}
	return releases, rows.Err()
}

func (s *OrchestrationStore) ExecutionPlan(ctx context.Context, run EngineRun) (*StoredExecutionPlan, error) {
	plan := &StoredExecutionPlan{Variables: []ReleaseVariableSnapshot{}, Dependencies: []json.RawMessage{}, Checks: []json.RawMessage{}}
	var sourceConfig, sourceIdentity, buildConfig, buildEvidence, runtimeConfig string
	var expectedDowntime int
	err := s.db.QueryRowContext(ctx, `
		SELECT s.id, s.kind, s.config_json, s.identity_json, s.digest,
		       b.id, b.config_json, b.evidence_json, b.preview, b.digest,
		       rp.id, rp.config_json, rp.preview, rp.digest, e.expected_downtime
		  FROM deploy_sources s
		  JOIN deploy_environments e ON e.id = s.environment_id
		  JOIN deploy_build_plans b
		    ON b.environment_id = s.environment_id AND b.revision = s.revision
		  JOIN deploy_runtime_plans rp
		    ON rp.environment_id = s.environment_id AND rp.revision = s.revision
		 WHERE s.environment_id = ? AND s.revision = ?`, run.EnvironmentID, run.PlanRevision).
		Scan(&plan.SourceID, &plan.SourceKind, &sourceConfig, &sourceIdentity, &plan.SourceDigest,
			&plan.BuildPlanID, &buildConfig, &buildEvidence, &plan.BuildPreview, &plan.BuildDigest,
			&plan.RuntimePlanID, &runtimeConfig, &plan.RuntimePreview, &plan.RuntimeDigest, &expectedDowntime)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: revision %d", ErrInvalidPlan, run.PlanRevision)
	}
	if err != nil {
		return nil, err
	}
	for raw, target := range map[string]any{
		sourceConfig: &plan.SourceConfig, sourceIdentity: &plan.SourceIdentity,
		buildConfig: &plan.Build, buildEvidence: &plan.BuildEvidence, runtimeConfig: &plan.Runtime,
	} {
		if json.Unmarshal([]byte(raw), target) != nil {
			return nil, fmt.Errorf("%w: persisted plan JSON is malformed", ErrInvalidPlan)
		}
	}
	if err := plan.SourceConfig.Validate(); err != nil {
		return nil, err
	}
	if run.SourceRevision != "" {
		if !IsRemoteGitSource(plan.SourceConfig) || !validGitObjectID(run.SourceRevision) {
			return nil, fmt.Errorf("%w: run source revision does not match a remote Git source", ErrInvalidPlan)
		}
		plan.SourceIdentity.Revision = run.SourceRevision
		plan.SourceDigest = digestBytes([]byte(sourceConfig), mustJSON(plan.SourceIdentity))
	}
	if !validBuildMethod(plan.Build.Method) || !validStrategy(plan.Runtime.Strategy) {
		return nil, fmt.Errorf("%w: persisted build/runtime method is invalid", ErrInvalidPlan)
	}
	plan.ExpectedDowntime = expectedDowntime != 0

	if err := s.db.QueryRowContext(ctx, `
		SELECT digest FROM deploy_run_variable_snapshots
		 WHERE run_id = ? AND environment_id = ?`, run.ID, run.EnvironmentID).
		Scan(&plan.VariablesDigest); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: run has no variable snapshot", ErrInvalidPlan)
		}
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT v.key, v.sensitivity, v.scopes, v.value_digest
		  FROM deploy_run_variable_revisions rv
		  JOIN deploy_variable_revisions v ON v.id = rv.variable_revision_id
		 WHERE rv.run_id = ? AND v.environment_id = ?
		 ORDER BY rv.ordinal`, run.ID, run.EnvironmentID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var variable ReleaseVariableSnapshot
		if err := rows.Scan(&variable.Name, &variable.Sensitivity, &variable.Scopes, &variable.ValueDigest); err != nil {
			rows.Close()
			return nil, err
		}
		plan.Variables = append(plan.Variables, variable)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if digestReleaseVariables(plan.Variables) != plan.VariablesDigest {
		return nil, fmt.Errorf("%w: run variable snapshot digest is inconsistent", ErrInvalidPlan)
	}

	var dependenciesJSON, checksJSON string
	if err := s.db.QueryRowContext(ctx, `
		SELECT dependencies_json, checks_json, digest
		  FROM deploy_run_plan_snapshots
		 WHERE run_id = ? AND environment_id = ?`, run.ID, run.EnvironmentID).
		Scan(&dependenciesJSON, &checksJSON, &plan.PlanInputsDigest); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: run has no dependency/check snapshot", ErrInvalidPlan)
		}
		return nil, err
	}
	if err := json.Unmarshal([]byte(dependenciesJSON), &plan.Dependencies); err != nil {
		return nil, fmt.Errorf("%w: dependency snapshot is malformed", ErrInvalidPlan)
	}
	if err := json.Unmarshal([]byte(checksJSON), &plan.Checks); err != nil {
		return nil, fmt.Errorf("%w: check snapshot is malformed", ErrInvalidPlan)
	}
	if digestBytes([]byte(dependenciesJSON), []byte(checksJSON)) != plan.PlanInputsDigest {
		return nil, fmt.Errorf("%w: dependency/check snapshot digest is inconsistent", ErrInvalidPlan)
	}
	return plan, nil
}

func (s *OrchestrationStore) CreateCandidateRelease(
	ctx context.Context,
	run EngineRun,
	claimToken string,
	input CandidateReleaseInput,
) (*ReleaseWithArtifacts, error) {
	plan, err := s.ExecutionPlan(ctx, run)
	if err != nil {
		return nil, err
	}
	if len(input.RuntimeSnapshot) == 0 || !json.Valid(input.RuntimeSnapshot) {
		return nil, fmt.Errorf("%w: runtime snapshot is missing", ErrArtifactMissing)
	}
	if err := rejectPlanConfigSecrets("runtime snapshot", input.RuntimeSnapshot); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}
	computedRuntimeDigest := digestBytes(input.RuntimeSnapshot)
	if input.RuntimeDigest == "" {
		input.RuntimeDigest = computedRuntimeDigest
	} else if input.RuntimeDigest != computedRuntimeDigest {
		return nil, fmt.Errorf("%w: runtime snapshot digest does not match its bytes", ErrInvalidPlan)
	}
	artifacts := append([]ReleaseArtifactInput(nil), input.Artifacts...)
	artifacts = append(artifacts, ReleaseArtifactInput{
		Kind: ArtifactRuntimeConfig, Reference: "runtime-plan.json", Digest: input.RuntimeDigest,
		Metadata: mustJSON(map[string]any{
			"secretFreePreview": plan.RuntimePreview,
			"snapshot":          json.RawMessage(input.RuntimeSnapshot),
		}),
		SizeBytes: int64(len(input.RuntimeSnapshot)),
	})
	for _, artifact := range artifacts {
		if !validArtifactKind(artifact.Kind) || artifact.Reference == "" ||
			!contentDigestRE.MatchString(artifact.Digest) || artifact.SizeBytes < 0 ||
			len(artifact.Metadata) > maxEventBytes || (len(artifact.Metadata) != 0 && !json.Valid(artifact.Metadata)) {
			return nil, fmt.Errorf("%w: invalid %s artifact", ErrArtifactMissing, artifact.Kind)
		}
	}
	variablesDigest := plan.VariablesDigest
	imageDigest := ""
	for _, artifact := range artifacts {
		if artifact.Kind == ArtifactImage && imageDigest == "" {
			imageDigest = artifact.Digest
		}
	}
	sourceIdentity, _ := json.Marshal(plan.SourceIdentity)
	provenance := mustJSON(map[string]any{
		"actor": run.Actor, "operation": run.Operation, "trigger": run.Trigger,
		"sourceDigest": plan.SourceDigest, "buildDigest": plan.BuildDigest,
		"runtimeDigest": input.RuntimeDigest, "builder": input.Prepared,
		"variables": plan.Variables, "dependencies": plan.Dependencies, "checks": plan.Checks,
		"detection": plan.BuildEvidence, "expectedDowntime": plan.ExpectedDowntime,
		"planInputsDigest": plan.PlanInputsDigest,
	})

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	if err := assertLeaseTx(ctx, tx, run.ID, claimToken, now); err != nil {
		return nil, err
	}
	if existing, err := releaseByRunTx(ctx, tx, run.ID); err == nil {
		if existing.PlanRevision != run.PlanRevision || existing.ConfigDigest != input.RuntimeDigest ||
			existing.VariablesDigest != variablesDigest || existing.ImageDigest != imageDigest {
			return nil, fmt.Errorf("%w: creating run already owns a different release snapshot", ErrInvalidPlan)
		}
		stored, err := artifactsForReleaseTx(ctx, tx, existing.ID)
		if err != nil {
			return nil, err
		}
		if releaseArtifactFingerprint(stored) != artifactInputFingerprint(artifacts) {
			return nil, fmt.Errorf("%w: creating run already owns different immutable artifacts", ErrInvalidPlan)
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &ReleaseWithArtifacts{Release: *existing, Artifacts: stored}, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var releaseNumber, predecessor int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(release_number), 0) + 1,
		       COALESCE((SELECT live_release_id FROM deploy_environments WHERE id = ?), 0)
		  FROM deploy_releases WHERE environment_id = ?`, run.EnvironmentID, run.EnvironmentID).
		Scan(&releaseNumber, &predecessor); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_releases(
		  project_id, environment_id, release_number, run_id, predecessor_release_id,
		  state, plan_revision, source_id, build_plan_id, runtime_plan_id,
		  source_revision, source_identity_json, image_digest, config_digest,
		  variables_digest, strategy, expected_downtime, provenance_json, blueprint_id, blueprint_version, created_at)
		VALUES(?, ?, ?, ?, ?, 'candidate', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ProjectID, run.EnvironmentID, releaseNumber, run.ID, predecessor,
		run.PlanRevision, plan.SourceID, plan.BuildPlanID, plan.RuntimePlanID,
		immutableSourceRevision(plan.SourceIdentity), string(sourceIdentity), imageDigest,
		input.RuntimeDigest, variablesDigest, plan.Runtime.Strategy, boolInt(plan.ExpectedDowntime), string(provenance),
		input.BlueprintID, input.BlueprintVersion, now.Unix())
	if err != nil {
		return nil, err
	}
	releaseID, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	storedArtifacts := make([]ReleaseArtifact, 0, len(artifacts))
	events := make([]RunEvent, 0, len(artifacts))
	for _, artifact := range artifacts {
		metadata := artifact.Metadata
		if len(metadata) == 0 {
			metadata = json.RawMessage(`{}`)
		}
		retainUntil := int64(0)
		if !artifact.RetainUntil.IsZero() {
			retainUntil = artifact.RetainUntil.UTC().Unix()
		}
		result, err := tx.ExecContext(ctx, `
			INSERT INTO deploy_release_artifacts(
			  release_id, kind, reference, digest, metadata_json, size_bytes,
			  retain_until, state, created_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, 'available', ?)`, releaseID, artifact.Kind,
			artifact.Reference, artifact.Digest, string(metadata), artifact.SizeBytes,
			retainUntil, now.Unix())
		if err != nil {
			return nil, err
		}
		artifactID, _ := result.LastInsertId()
		storedArtifacts = append(storedArtifacts, ReleaseArtifact{
			ID: artifactID, ReleaseID: releaseID, Kind: artifact.Kind,
			Reference: artifact.Reference, Digest: artifact.Digest, Metadata: metadata,
			SizeBytes: artifact.SizeBytes, RetainUntil: unixTimePtr(retainUntil),
			State: "available", CreatedAt: now,
		})
		event, err := appendEventTx(ctx, tx, now, run.ID, 0, EventArtifact, "status", "",
			mustJSON(map[string]any{"releaseId": releaseID, "kind": artifact.Kind, "reference": artifact.Reference, "digest": artifact.Digest}))
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE deploy_runs SET candidate_release_id = ? WHERE id = ?`, releaseID, run.ID); err != nil {
		return nil, err
	}
	release, err := releaseByIDTx(ctx, tx, releaseID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.publish(events)
	return &ReleaseWithArtifacts{Release: *release, Artifacts: storedArtifacts}, nil
}

// CloneCandidateRelease creates a new immutable release record over an exact
// retained release. No source is fetched and no image tag is resolved: every
// artifact byte/digest and the runtime snapshot are copied from the selected
// release, while the new run supplies fenced activation and provenance.
func (s *OrchestrationStore) CloneCandidateRelease(
	ctx context.Context,
	run EngineRun,
	claimToken string,
	targetReleaseID int64,
) (*ReleaseWithArtifacts, error) {
	target, err := s.Release(ctx, targetReleaseID)
	if err != nil {
		return nil, err
	}
	if target.Release.ProjectID != run.ProjectID || target.Release.EnvironmentID != run.EnvironmentID ||
		target.Release.PlanRevision != run.PlanRevision ||
		(target.Release.State != "live" && target.Release.State != "retained") {
		return nil, fmt.Errorf("%w: selected release is not an activatable target", ErrInvalidPlan)
	}
	var runtimeSnapshot json.RawMessage
	artifacts := make([]ReleaseArtifactInput, 0, len(target.Artifacts))
	for _, artifact := range target.Artifacts {
		if artifact.State != "available" {
			return nil, fmt.Errorf("%w: selected release artifact %s is %s", ErrArtifactMissing, artifact.Kind, artifact.State)
		}
		if artifact.Kind == ArtifactRuntimeConfig {
			var envelope struct {
				Snapshot json.RawMessage `json:"snapshot"`
			}
			if json.Unmarshal(artifact.Metadata, &envelope) != nil || len(envelope.Snapshot) == 0 ||
				digestBytes(envelope.Snapshot) != artifact.Digest {
				return nil, fmt.Errorf("%w: selected release runtime snapshot is inconsistent", ErrArtifactMissing)
			}
			runtimeSnapshot = append(json.RawMessage(nil), envelope.Snapshot...)
			continue
		}
		input := ReleaseArtifactInput{
			Kind: artifact.Kind, Reference: artifact.Reference, Digest: artifact.Digest,
			Metadata: append(json.RawMessage(nil), artifact.Metadata...), SizeBytes: artifact.SizeBytes,
		}
		if artifact.RetainUntil != nil {
			input.RetainUntil = *artifact.RetainUntil
		}
		artifacts = append(artifacts, input)
	}
	if len(runtimeSnapshot) == 0 {
		return nil, fmt.Errorf("%w: selected release has no runtime snapshot", ErrArtifactMissing)
	}
	var provenance struct {
		Builder PreparedBuild `json:"builder"`
	}
	if err := json.Unmarshal(target.Release.Provenance, &provenance); err != nil {
		return nil, fmt.Errorf("%w: selected release provenance is malformed", ErrArtifactMissing)
	}
	return s.CreateCandidateRelease(ctx, run, claimToken, CandidateReleaseInput{
		Artifacts: artifacts, Prepared: provenance.Builder,
		RuntimeSnapshot: runtimeSnapshot, RuntimeDigest: target.Release.ConfigDigest,
		BlueprintID: target.Release.BlueprintID, BlueprintVersion: target.Release.BlueprintVersion,
	})
}

func (s *OrchestrationStore) Release(ctx context.Context, releaseID int64) (*ReleaseWithArtifacts, error) {
	release, err := releaseByIDTx(ctx, s.db, releaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrArtifactMissing
	}
	if err != nil {
		return nil, err
	}
	artifacts, err := artifactsForReleaseTx(ctx, s.db, releaseID)
	if err != nil {
		return nil, err
	}
	return &ReleaseWithArtifacts{Release: *release, Artifacts: artifacts}, nil
}

type ReleaseComparison struct {
	FromReleaseID int64                   `json:"fromReleaseId"`
	ToReleaseID   int64                   `json:"toReleaseId"`
	Changes       map[string]bool         `json:"changes"`
	Detail        ReleaseDetailComparison `json:"detail"`
	Artifacts     []ReleaseArtifactStatus `json:"artifacts"`
}

func (s *OrchestrationStore) CompareReleases(ctx context.Context, fromID, toID int64) (*ReleaseComparison, error) {
	from, err := s.Release(ctx, fromID)
	if err != nil {
		return nil, err
	}
	to, err := s.Release(ctx, toID)
	if err != nil {
		return nil, err
	}
	if from.Release.EnvironmentID != to.Release.EnvironmentID {
		return nil, fmt.Errorf("%w: releases belong to different environments", ErrInvalidPlan)
	}
	return &ReleaseComparison{FromReleaseID: fromID, ToReleaseID: toID, Changes: map[string]bool{
		"source":    from.Release.SourceRevision != to.Release.SourceRevision,
		"build":     releaseBuildFingerprint(from.Artifacts) != releaseBuildFingerprint(to.Artifacts),
		"runtime":   from.Release.ConfigDigest != to.Release.ConfigDigest,
		"variables": from.Release.VariablesDigest != to.Release.VariablesDigest,
		"strategy":  from.Release.Strategy != to.Release.Strategy,
		"downtime":  from.Release.ExpectedDowntime != to.Release.ExpectedDowntime,
	}}, nil
}

func artifactInputFingerprint(artifacts []ReleaseArtifactInput) string {
	identities := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		metadata := artifact.Metadata
		if len(metadata) == 0 {
			metadata = json.RawMessage(`{}`)
		}
		identities = append(identities, fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%s",
			artifact.Kind, artifact.Reference, artifact.Digest, artifact.SizeBytes, metadata))
	}
	sort.Strings(identities)
	return digestBytes([]byte(strings.Join(identities, "\x00")))
}

func releaseArtifactFingerprint(artifacts []ReleaseArtifact) string {
	inputs := make([]ReleaseArtifactInput, 0, len(artifacts))
	for _, artifact := range artifacts {
		inputs = append(inputs, ReleaseArtifactInput{
			Kind: artifact.Kind, Reference: artifact.Reference, Digest: artifact.Digest,
			Metadata: artifact.Metadata, SizeBytes: artifact.SizeBytes,
		})
	}
	return artifactInputFingerprint(inputs)
}

func releaseBuildFingerprint(artifacts []ReleaseArtifact) string {
	inputs := make([]ReleaseArtifactInput, 0, len(artifacts))
	for _, artifact := range artifacts {
		switch artifact.Kind {
		case ArtifactImage, ArtifactCompose, ArtifactStaticBundle:
			inputs = append(inputs, ReleaseArtifactInput{
				Kind: artifact.Kind, Reference: artifact.Reference, Digest: artifact.Digest,
				Metadata: artifact.Metadata, SizeBytes: artifact.SizeBytes,
			})
		}
	}
	return artifactInputFingerprint(inputs)
}

type ArtifactRetentionDecision struct {
	Artifact ReleaseArtifact `json:"artifact"`
	Retain   bool            `json:"retain"`
	Reason   string          `json:"reason"`
}

type ArtifactRetentionReport struct {
	Decisions []ArtifactRetentionDecision `json:"decisions"`
	Pruned    int                         `json:"pruned"`
	Reclaimed int64                       `json:"reclaimedBytes"`
}

type ArtifactRemover interface {
	RemoveArtifact(context.Context, ReleaseArtifact) error
}

func (s *OrchestrationStore) ArtifactRetentionPlan(ctx context.Context, now time.Time) ([]ArtifactRetentionDecision, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.release_id, a.kind, a.reference, a.digest, a.metadata_json,
		       a.size_bytes, a.retain_until, a.state, a.created_at,
		       r.environment_id, r.release_number, r.state, r.pinned,
		       e.live_release_id,
		       EXISTS(SELECT 1 FROM deploy_queue_leases q JOIN deploy_runs dr ON dr.id = q.run_id
		               WHERE dr.environment_id = r.environment_id)
		  FROM deploy_release_artifacts a
		  JOIN deploy_releases r ON r.id = a.release_id
		  JOIN deploy_environments e ON e.id = r.environment_id
		 WHERE a.state IN ('available', 'prune_failed')
		 ORDER BY r.environment_id, r.release_number DESC, a.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type item struct {
		artifact                             ReleaseArtifact
		environmentID, number, liveReleaseID int64
		releaseState                         string
		pinned, active                       bool
	}
	items := []item{}
	for rows.Next() {
		var current item
		var metadata string
		var retainUntil, createdAt int64
		var pinned, active int
		if err := rows.Scan(&current.artifact.ID, &current.artifact.ReleaseID, &current.artifact.Kind,
			&current.artifact.Reference, &current.artifact.Digest, &metadata, &current.artifact.SizeBytes,
			&retainUntil, &current.artifact.State, &createdAt, &current.environmentID, &current.number,
			&current.releaseState, &pinned, &current.liveReleaseID, &active); err != nil {
			return nil, err
		}
		current.artifact.Metadata = json.RawMessage(metadata)
		current.artifact.RetainUntil = unixTimePtr(retainUntil)
		current.artifact.CreatedAt = unixTime(createdAt)
		current.pinned, current.active = pinned != 0, active != 0
		items = append(items, current)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	successRank := map[int64]map[int64]int{}
	for _, current := range items {
		if current.releaseState != "live" && current.releaseState != "retained" {
			continue
		}
		if successRank[current.environmentID] == nil {
			successRank[current.environmentID] = map[int64]int{}
		}
		if _, exists := successRank[current.environmentID][current.artifact.ReleaseID]; !exists {
			successRank[current.environmentID][current.artifact.ReleaseID] = len(successRank[current.environmentID]) + 1
		}
	}
	decisions := make([]ArtifactRetentionDecision, 0, len(items))
	for _, current := range items {
		decision := ArtifactRetentionDecision{Artifact: current.artifact}
		switch {
		case current.active:
			decision.Retain, decision.Reason = true, "environment has an active build or activation lease"
		case current.artifact.ReleaseID == current.liveReleaseID || current.releaseState == "live":
			decision.Retain, decision.Reason = true, "current live release"
		case current.pinned:
			decision.Retain, decision.Reason = true, "operator pin"
		case current.releaseState == "candidate":
			decision.Retain, decision.Reason = true, "candidate release"
		case current.artifact.RetainUntil != nil && current.artifact.RetainUntil.After(now):
			decision.Retain, decision.Reason = true, "artifact retain-until policy"
		case successRank[current.environmentID][current.artifact.ReleaseID] > 0 && successRank[current.environmentID][current.artifact.ReleaseID] <= 6:
			decision.Retain, decision.Reason = true, "current plus five prior successful rollback releases"
		case current.releaseState == "failed" && current.artifact.CreatedAt.Add(7*24*time.Hour).After(now):
			decision.Retain, decision.Reason = true, "failed artifact seven-day diagnostic window"
		default:
			decision.Reason = "outside configured release retention"
		}
		decisions = append(decisions, decision)
	}
	return decisions, nil
}

func (s *OrchestrationStore) PruneArtifacts(ctx context.Context, remover ArtifactRemover) (*ArtifactRetentionReport, error) {
	if remover == nil {
		return nil, fmt.Errorf("artifact remover is required")
	}
	decisions, err := s.ArtifactRetentionPlan(ctx, s.now().UTC())
	if err != nil {
		return nil, err
	}
	report := &ArtifactRetentionReport{Decisions: decisions}
	retainedIdentity := map[string]bool{}
	for _, decision := range decisions {
		if decision.Retain {
			retainedIdentity[artifactPhysicalKey(decision.Artifact)] = true
		}
	}
	for index := range report.Decisions {
		decision := &report.Decisions[index]
		if decision.Retain {
			continue
		}
		identity := artifactPhysicalKey(decision.Artifact)
		if retainedIdentity[identity] {
			decision.Retain, decision.Reason = true, "physical artifact is shared by a retained release"
			continue
		}
		if err := s.reserveArtifactPrune(ctx, decision.Artifact.ID); err != nil {
			if errors.Is(err, ErrArtifactRetained) {
				decision.Retain, decision.Reason = true, "a deployment lease or policy retained the artifact during cleanup"
				continue
			}
			return report, err
		}
		if err := remover.RemoveArtifact(ctx, decision.Artifact); err != nil {
			_, _ = s.db.ExecContext(context.WithoutCancel(ctx),
				`UPDATE deploy_release_artifacts SET state = 'prune_failed' WHERE id = ? AND state = 'pruning'`, decision.Artifact.ID)
			continue
		}
		if _, err := s.db.ExecContext(ctx,
			`UPDATE deploy_release_artifacts SET state = 'pruned' WHERE id = ? AND state = 'pruning'`, decision.Artifact.ID); err != nil {
			return report, err
		}
		report.Pruned++
		report.Reclaimed += decision.Artifact.SizeBytes
	}
	return report, nil
}

func (s *OrchestrationStore) reserveArtifactPrune(ctx context.Context, artifactID int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var active, retained int
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM deploy_queue_leases),
		       EXISTS(
		         SELECT 1 FROM deploy_release_artifacts target
		         JOIN deploy_releases tr ON tr.id = target.release_id
		         JOIN deploy_release_artifacts shared
		           ON shared.kind = target.kind AND shared.digest = target.digest
		          AND (target.kind = 'image' OR shared.reference = target.reference)
		         JOIN deploy_releases sr ON sr.id = shared.release_id
		         JOIN deploy_environments e ON e.id = sr.environment_id
		        WHERE target.id = ? AND shared.id <> target.id AND shared.state = 'available'
		          AND (sr.pinned = 1 OR sr.id = e.live_release_id OR sr.state = 'candidate')
		       )`, artifactID).Scan(&active, &retained); err != nil {
		return err
	}
	if active != 0 || retained != 0 {
		return ErrArtifactRetained
	}
	result, err := tx.ExecContext(ctx,
		`UPDATE deploy_release_artifacts SET state = 'pruning' WHERE id = ? AND state IN ('available', 'prune_failed')`, artifactID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrArtifactRetained
	}
	return tx.Commit()
}

func artifactPhysicalKey(artifact ReleaseArtifact) string {
	if artifact.Kind == ArtifactImage {
		return string(artifact.Kind) + "\x00" + artifact.Digest
	}
	return string(artifact.Kind) + "\x00" + artifact.Reference + "\x00" + artifact.Digest
}

const releaseColumns = `
id, project_id, environment_id, release_number, run_id, predecessor_release_id,
state, plan_revision, source_id, build_plan_id, runtime_plan_id, source_revision,
source_identity_json, image_digest, config_digest, variables_digest, strategy, expected_downtime,
provenance_json, blueprint_id, blueprint_version, created_at, activated_at,
retired_at, pinned`

func scanRelease(row scanner) (*Release, error) {
	var release Release
	var sourceIdentity, provenance string
	var createdAt, activatedAt, retiredAt int64
	var pinned, expectedDowntime int
	if err := row.Scan(&release.ID, &release.ProjectID, &release.EnvironmentID, &release.Number,
		&release.RunID, &release.PredecessorReleaseID, &release.State, &release.PlanRevision,
		&release.SourceID, &release.BuildPlanID, &release.RuntimePlanID, &release.SourceRevision,
		&sourceIdentity, &release.ImageDigest, &release.ConfigDigest, &release.VariablesDigest,
		&release.Strategy, &expectedDowntime, &provenance, &release.BlueprintID, &release.BlueprintVersion,
		&createdAt, &activatedAt, &retiredAt, &pinned); err != nil {
		return nil, err
	}
	release.SourceIdentity, release.Provenance = json.RawMessage(sourceIdentity), json.RawMessage(provenance)
	release.CreatedAt, release.ActivatedAt, release.RetiredAt = unixTime(createdAt), unixTimePtr(activatedAt), unixTimePtr(retiredAt)
	release.Pinned = pinned != 0
	release.ExpectedDowntime = expectedDowntime != 0
	return &release, nil
}

func releaseByRunTx(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, runID int64) (*Release, error) {
	return scanRelease(queryer.QueryRowContext(ctx, `SELECT `+releaseColumns+` FROM deploy_releases WHERE run_id = ?`, runID))
}

func releaseByIDTx(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, releaseID int64) (*Release, error) {
	return scanRelease(queryer.QueryRowContext(ctx, `SELECT `+releaseColumns+` FROM deploy_releases WHERE id = ?`, releaseID))
}

func artifactsForReleaseTx(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, releaseID int64) ([]ReleaseArtifact, error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT id, release_id, kind, reference, digest, metadata_json, size_bytes,
		       retain_until, state, created_at
		  FROM deploy_release_artifacts WHERE release_id = ? ORDER BY id`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ReleaseArtifact{}
	for rows.Next() {
		var artifact ReleaseArtifact
		var metadata string
		var retainUntil, createdAt int64
		if err := rows.Scan(&artifact.ID, &artifact.ReleaseID, &artifact.Kind, &artifact.Reference,
			&artifact.Digest, &metadata, &artifact.SizeBytes, &retainUntil, &artifact.State,
			&createdAt); err != nil {
			return nil, err
		}
		artifact.Metadata = json.RawMessage(metadata)
		artifact.RetainUntil, artifact.CreatedAt = unixTimePtr(retainUntil), unixTime(createdAt)
		result = append(result, artifact)
	}
	return result, rows.Err()
}

func validArtifactKind(kind ArtifactKind) bool {
	switch kind {
	case ArtifactImage, ArtifactCompose, ArtifactRuntimeConfig, ArtifactStaticBundle,
		ArtifactSourceArchive, ArtifactBackup, ArtifactDiagnostic, ArtifactExport:
		return true
	default:
		return false
	}
}

func digestReleaseVariables(variables []ReleaseVariableSnapshot) string {
	copy := append([]ReleaseVariableSnapshot(nil), variables...)
	sort.Slice(copy, func(i, j int) bool { return copy[i].Name < copy[j].Name })
	raw, _ := json.Marshal(copy)
	return digestBytes(raw)
}

func immutableSourceRevision(identity SourceIdentity) string {
	if identity.Revision != "" {
		return identity.Revision
	}
	return identity.Digest
}
