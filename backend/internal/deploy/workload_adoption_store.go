package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// CreateRecoveredDraft is deliberately not a client-saved step. Only the
// authenticated recovery reader can attach ownership and a live baseline.
func (s *PlanningStore) CreateRecoveredDraft(ctx context.Context, ownerID int64, owner string, intent DraftIntentConfig, recovered *RecoveredWorkload) (*Draft, error) {
	if recovered == nil || recovered.Adoption == nil || len(recovered.Adoption.Blockers) != 0 {
		return nil, fmt.Errorf("%w: migration has unresolved compatibility blockers", ErrPreflightBlocked)
	}
	if err := intent.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}
	if err := recovered.Source.validateForNewDeployment(); err != nil {
		return nil, err
	}
	if err := validateDetectionResult(&recovered.Source, recovered.Detection); err != nil {
		return nil, err
	}
	configuration := canonicalConfiguration(recovered.Configuration)
	if err := configuration.Validate(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := assertAdoptionUnownedTx(ctx, tx, recovered.Adoption); err != nil {
		return nil, err
	}
	now := s.now().UTC()
	draft := &Draft{
		ID: auth.RandomToken(18), OwnerUserID: ownerID, OwnerUsername: owner,
		CurrentStep: DraftConfiguration, Revision: 1, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(draftTTL),
		Data:     DraftData{Intent: &intent, Source: &recovered.Source, Detection: &recovered.Detection, Configuration: &configuration, Adoption: recovered.Adoption},
		Findings: []PreflightFinding{}, environment: recovered.Environment, adoptionEnvironment: recovered.Environment,
	}
	plaintext, err := json.Marshal(recovered.Environment)
	if err != nil {
		return nil, err
	}
	draft.environmentEnc, err = s.sealer.Seal(string(plaintext))
	if err != nil {
		return nil, err
	}
	draft.adoptionEnc = draft.environmentEnc
	configuration = draft.withEnvironmentMetadata(configuration)
	draft.Data.Configuration = &configuration
	draft.refreshEnvironmentKeys()
	data, err := json.Marshal(draft.Data)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO deploy_drafts(
	  id,owner_user_id,owner_username,current_step,revision,data_json,environment_enc,adoption_enc,
	  findings_json,plan_preview,created_at,updated_at,expires_at)
	  VALUES(?,?,?,?,?,?,?,?,'[]','',?,?,?)`, draft.ID, ownerID, owner, draft.CurrentStep, draft.Revision,
		string(data), draft.environmentEnc, draft.adoptionEnc, now.Unix(), now.Unix(), draft.ExpiresAt.Unix()); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return draft, nil
}

func adoptionResourceKind(kind string) string {
	return map[string]string{"stack": "compose_stack", "container": "docker_container", "pm2": "pm2_process", "systemd": "systemd_unit", "process": "host_process"}[kind]
}

func assertAdoptionUnownedTx(ctx context.Context, tx *sql.Tx, adoption *WorkloadAdoption) error {
	kind := adoptionResourceKind(adoption.Kind)
	if kind == "" || adoption.Key == "" || adoption.ResourceID == "" || adoption.Runtime.RuntimeID == "" {
		return fmt.Errorf("%w: recovered runtime identity is incomplete", ErrInvalidPlan)
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM deploy_dependencies
	  WHERE kind='runtime' AND resource_kind=? AND resource_id=?`, kind, adoption.ResourceID).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return ErrWorkloadAlreadyImported
	}
	if kind != "compose_stack" && kind != "docker_container" {
		return nil
	}
	metadata := adoptionRuntimeCandidate(adoption)
	containers := observedContainerIDs(kind, adoption.ResourceID, mustJSON(metadata))
	rows, err := tx.QueryContext(ctx, `SELECT resource_kind,resource_id,config_json FROM deploy_dependencies
	  WHERE kind='runtime' AND resource_kind IN ('compose_stack','docker_container')`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var existingKind, id, config string
		if err := rows.Scan(&existingKind, &id, &config); err != nil {
			return err
		}
		for existing := range observedContainerIDs(existingKind, id, json.RawMessage(config)) {
			if containers[existing] {
				return ErrWorkloadAlreadyImported
			}
		}
	}
	return rows.Err()
}

func adoptionRuntimeCandidate(adoption *WorkloadAdoption) WorkloadCandidate {
	candidate := WorkloadCandidate{Key: adoption.Key, Kind: adoption.Kind, ResourceID: adoption.ResourceID, Name: adoption.Name, Services: []WorkloadService{}}
	var metadata dockerReleaseRuntimeMetadata
	if json.Unmarshal(adoption.Runtime.Metadata, &metadata) == nil {
		for _, id := range metadata.ContainerIDs {
			candidate.Services = append(candidate.Services, WorkloadService{ResourceID: id})
		}
		if metadata.PrimaryContainerID != "" {
			candidate.Services = append(candidate.Services, WorkloadService{ResourceID: metadata.PrimaryContainerID})
		}
	}
	return candidate
}

func (s *PlanningStore) commitAdoptionBaselineTx(ctx context.Context, tx *sql.Tx, draft *Draft, projectID, environmentID int64, now time.Time) error {
	adoption := draft.Data.Adoption
	configuration := canonicalConfiguration(adoption.BaselineConfiguration)
	var snapshot runtimeReleaseSnapshot
	if json.Unmarshal(adoption.Snapshot, &snapshot) != nil || snapshot.Version != 1 {
		return fmt.Errorf("%w: recovered baseline snapshot is invalid", ErrInvalidPlan)
	}
	snapshot.Plan = configuration.Runtime
	identity := draft.Data.Detection.Source
	snapshot.SourceIdentity = identity
	snapshot.Dependencies = append([]PlannedDependency(nil), configuration.Dependencies...)
	snapshot.Checks = append([]PlannedCheck(nil), configuration.Checks...)
	snapshot.Domains = append([]PlannedDomain(nil), configuration.Domains...)
	sourceJSON, identityJSON := mustJSON(adoption.BaselineSource), mustJSON(identity)
	result, err := tx.ExecContext(ctx, `INSERT INTO deploy_sources(environment_id,revision,kind,config_json,credential_id,identity_json,digest,created_at)
	 VALUES(?,1,?,?,?,?,?,?)`, environmentID, adoption.BaselineSource.Kind, string(sourceJSON), adoption.BaselineSource.CredentialID,
		string(identityJSON), digestBytes(sourceJSON, identityJSON), now.Unix())
	if err != nil {
		return err
	}
	sourceID, err := result.LastInsertId()
	if err != nil {
		return err
	}
	buildJSON := mustJSON(configuration.Build)
	evidence := mustJSON(StoredBuildEvidence{Candidates: draft.Data.Detection.Candidates, Compose: draft.Data.Detection.Compose, GitRequirements: draft.Data.Detection.GitRequirements})
	result, err = tx.ExecContext(ctx, `INSERT INTO deploy_build_plans(environment_id,revision,method,config_json,evidence_json,preview,digest,created_at)
	 VALUES(?,1,?,?,?,?,?,?)`, environmentID, configuration.Build.Method, string(buildJSON), string(evidence), renderBuildPreview(configuration.Build), buildPlanDigest(configuration.Build), now.Unix())
	if err != nil {
		return err
	}
	buildID, err := result.LastInsertId()
	if err != nil {
		return err
	}
	runtimeJSON := mustJSON(configuration.Runtime)
	result, err = tx.ExecContext(ctx, `INSERT INTO deploy_runtime_plans(environment_id,revision,config_json,preview,digest,created_at)
	 VALUES(?,1,?,?,?,?)`, environmentID, string(runtimeJSON), renderRuntimePreview(configuration.Runtime), digestBytes(runtimeJSON), now.Unix())
	if err != nil {
		return err
	}
	runtimeID, err := result.LastInsertId()
	if err != nil {
		return err
	}
	result, err = tx.ExecContext(ctx, `INSERT INTO deploy_runs(project_id,environment_id,run_number,started_at,ended_at,status,state,operation,trigger,actor,requested_at,plan_revision,terminal_code,terminal_reason,metadata_json)
	 VALUES(?,?,1,?,?,'success','succeeded','import_adopt','migration',?,?,1,'workload_adopted','Registered the current runtime without restarting or recreating it.',?)`,
		projectID, environmentID, now.Unix(), now.Unix(), draft.OwnerUsername, now.Unix(), string(mustJSON(map[string]any{"adopted": true, "workloadKey": adoption.Key})))
	if err != nil {
		return err
	}
	runID, err := result.LastInsertId()
	if err != nil {
		return err
	}
	if err := s.snapshotAdoptionVariablesTx(ctx, tx, draft, runID, environmentID, now); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT v.key,v.sensitivity,v.scopes,v.value_digest FROM deploy_run_variable_revisions rv
	 JOIN deploy_variable_revisions v ON v.id=rv.variable_revision_id WHERE rv.run_id=? ORDER BY rv.ordinal`, runID)
	if err != nil {
		return err
	}
	snapshot.Variables = []ReleaseVariableSnapshot{}
	for rows.Next() {
		var variable ReleaseVariableSnapshot
		if err := rows.Scan(&variable.Name, &variable.Sensitivity, &variable.Scopes, &variable.ValueDigest); err != nil {
			rows.Close()
			return err
		}
		snapshot.Variables = append(snapshot.Variables, variable)
	}
	readErr := rows.Err()
	rows.Close()
	if readErr != nil {
		return readErr
	}
	variablesDigest := digestReleaseVariables(snapshot.Variables)
	dependency := PlannedDependency{Kind: "runtime", Ownership: OwnershipManaged, ResourceKind: adoptionResourceKind(adoption.Kind), ResourceID: adoption.ResourceID, Config: mustJSON(adoptionRuntimeCandidate(adoption))}
	snapshot.Dependencies = append(snapshot.Dependencies, dependency)
	if _, err := tx.ExecContext(ctx, `INSERT INTO deploy_dependencies(environment_id,release_id,kind,ownership,resource_kind,resource_id,config_json,created_at)
	 VALUES(?,0,'runtime','managed',?,?,?,?)`, environmentID, dependency.ResourceKind, dependency.ResourceID, string(dependency.Config), now.Unix()); err != nil {
		return err
	}
	if err := snapshotRunPlanInputsTx(ctx, tx, runID, environmentID, 0, now); err != nil {
		return err
	}
	var planDigest string
	if err := tx.QueryRowContext(ctx, `SELECT digest FROM deploy_run_plan_snapshots WHERE run_id=?`, runID).Scan(&planDigest); err != nil {
		return err
	}
	snapshot.PlanInputsHash = planDigest
	raw := mustJSON(snapshot)
	if err := rejectPlanConfigSecrets("adoption runtime snapshot", raw); err != nil {
		return err
	}
	configDigest := digestBytes(raw)
	provenance := mustJSON(map[string]any{"adopted": true, "workloadKey": adoption.Key, "baselineDigest": adoption.BaselineDigest, "builder": PreparedBuild{Method: configuration.Build.Method}})
	result, err = tx.ExecContext(ctx, `INSERT INTO deploy_releases(project_id,environment_id,release_number,run_id,state,plan_revision,source_id,build_plan_id,runtime_plan_id,source_identity_json,image_digest,config_digest,variables_digest,strategy,expected_downtime,provenance_json,created_at,activated_at,pinned)
	 VALUES(?,?,1,?,'live',1,?,?,?,?,?,?,?,?,1,?,?,?,1)`, projectID, environmentID, runID, sourceID, buildID, runtimeID, string(identityJSON), snapshot.Image.Digest, configDigest, variablesDigest, configuration.Runtime.Strategy, string(provenance), now.Unix(), now.Unix())
	if err != nil {
		return err
	}
	releaseID, err := result.LastInsertId()
	if err != nil {
		return err
	}
	artifacts := []ReleaseArtifactInput{{Kind: ArtifactRuntimeConfig, Reference: "runtime-plan.json", Digest: configDigest, Metadata: mustJSON(map[string]any{"snapshot": json.RawMessage(raw)}), SizeBytes: int64(len(raw))}}
	if snapshot.Compose != nil {
		compose := mustJSON(snapshot.Compose)
		artifacts = append(artifacts, ReleaseArtifactInput{Kind: ArtifactCompose, Reference: "compose", Digest: digestBytes(compose), Metadata: compose, SizeBytes: int64(len(compose))})
		for _, service := range snapshot.Compose.Services {
			artifacts = append(artifacts, ReleaseArtifactInput{Kind: ArtifactImage, Reference: service.Reference, Digest: service.Digest, Metadata: mustJSON(service)})
		}
	} else if snapshot.Image.Digest != "" {
		artifacts = append(artifacts, ReleaseArtifactInput{Kind: ArtifactImage, Reference: snapshot.Image.Reference, Digest: snapshot.Image.Digest, Metadata: mustJSON(snapshot.Image)})
	}
	for _, artifact := range artifacts {
		if _, err := tx.ExecContext(ctx, `INSERT INTO deploy_release_artifacts(release_id,kind,reference,digest,metadata_json,size_bytes,state,created_at)
		 VALUES(?,?,?,?,?,?,'available',?)`, releaseID, artifact.Kind, artifact.Reference, artifact.Digest, string(artifact.Metadata), artifact.SizeBytes, now.Unix()); err != nil {
			return err
		}
	}
	runtime := adoption.Runtime
	if _, err := tx.ExecContext(ctx, `INSERT INTO deploy_release_runtimes(release_id,environment_id,kind,runtime_id,name,working_directory,host,port,state,metadata_json,created_at,updated_at)
	 VALUES(?,?,?,?,?,?,?,?,'live',?,?,?)`, releaseID, environmentID, runtime.Kind, runtime.RuntimeID, runtime.Name, runtime.WorkingDirectory, runtime.Host, runtime.Port, string(runtime.Metadata), now.Unix(), now.Unix()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deploy_environments SET live_release_id=? WHERE id=?`, releaseID, environmentID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deploy_runs SET release_id=? WHERE id=?`, releaseID, runID); err != nil {
		return err
	}
	_, err = appendEventTx(ctx, tx, now, runID, 0, EventRunState, "status", "Current workload imported without a runtime change.", mustJSON(map[string]any{"state": RunSucceeded, "releaseId": releaseID, "adopted": true}))
	return err
}

func (s *PlanningStore) snapshotAdoptionVariablesTx(ctx context.Context, tx *sql.Tx, draft *Draft, runID, environmentID int64, now time.Time) error {
	// Desired values may be edited during Review. Freeze the privately captured
	// original values separately so a later rollback never substitutes edits.
	if _, err := tx.ExecContext(ctx, `UPDATE deploy_variable_revisions SET active=0 WHERE environment_id=? AND active=1`, environmentID); err != nil {
		return err
	}
	variables := append([]PlannedVariable(nil), draft.Data.Adoption.BaselineConfiguration.Variables...)
	sort.Slice(variables, func(i, j int) bool { return variables[i].Name < variables[j].Name })
	for _, variable := range variables {
		value, ok := draft.adoptionEnvironment[variable.Name]
		if !ok {
			value = variable.Value
		}
		if variable.Required && !ok && variable.Value == "" {
			return fmt.Errorf("%w: missing captured baseline variable", ErrInvalidVariable)
		}
		sealed, err := s.sealer.Seal(value)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO deploy_variable_revisions(environment_id,key,revision,sensitivity,scopes,value_enc,value_digest,active,created_by,created_at)
		 VALUES(?,?,0,?,?,?,?,1,?,?)`, environmentID, variable.Name, suppliedVariableSensitivity(variable), strings.Join(variable.Scopes, ","), sealed, digestBytes([]byte(value)), draft.OwnerUsername, now.Unix()); err != nil {
			return err
		}
	}
	if err := snapshotRunVariablesTx(ctx, tx, runID, environmentID, 0, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deploy_variable_revisions SET active=0 WHERE environment_id=? AND revision=0`, environmentID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE deploy_variable_revisions SET active=1 WHERE environment_id=? AND revision=1`, environmentID)
	return err
}
