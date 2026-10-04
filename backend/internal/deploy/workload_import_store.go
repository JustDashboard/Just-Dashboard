package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

var ErrWorkloadAlreadyImported = errors.New("this workload is already registered as a deployment")

// ObservedWorkloadRegistration accepts only metadata produced by discovery.
// Observed must exclude environment values, command arguments, and credentials.
type ObservedWorkloadRegistration struct {
	Name          string
	ResourceKind  string
	ResourceID    string
	SourceMode    SourceMode
	Observed      json.RawMessage
	OwnerUsername string
}

// RegisterObservedWorkload records an external runtime without adopting its
// lifecycle. Its empty plans describe no commands, credentials, or runtime
// changes; execution gates enforce that the original manager remains in control.
func (s *PlanningStore) RegisterObservedWorkload(
	ctx context.Context,
	registration ObservedWorkloadRegistration,
) (*DraftCommitResult, error) {
	registration.Name = strings.TrimSpace(registration.Name)
	if err := validateObservedWorkloadRegistration(registration); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Store opens SQLite with immediate transactions, so another PlanningStore
	// cannot pass this resource check before our registration commits.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var existingEnvironment, existingProject, archived int64
	var existingName, ownership string
	err = tx.QueryRowContext(ctx, `
		SELECT e.id, e.project_id, p.name, p.archived_at, d.ownership
		  FROM deploy_dependencies d JOIN deploy_environments e ON e.id = d.environment_id
		  JOIN deploy_projects p ON p.id = e.project_id
		 WHERE d.kind = 'runtime' AND d.resource_kind = ? AND d.resource_id = ?
		 LIMIT 1`, registration.ResourceKind, registration.ResourceID).
		Scan(&existingEnvironment, &existingProject, &existingName, &archived, &ownership)
	if err == nil {
		if existingName == registration.Name && archived == 0 && ownership == string(OwnershipObserved) {
			return &DraftCommitResult{ProjectID: existingProject, EnvironmentID: existingEnvironment,
				PlanRevision: 1, Created: false}, nil
		}
		return nil, ErrWorkloadAlreadyImported
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	// Project labels can be changed outside the dashboard. A stack and one
	// of its recorded containers must still not receive two project owners.
	if registration.ResourceKind == "compose_stack" || registration.ResourceKind == "docker_container" {
		containers := observedContainerIDs(registration.ResourceKind, registration.ResourceID, registration.Observed)
		rows, err := tx.QueryContext(ctx, `SELECT resource_kind, resource_id, config_json
		  FROM deploy_dependencies WHERE kind = 'runtime'
		  AND resource_kind IN ('compose_stack', 'docker_container')`)
		if err != nil {
			return nil, err
		}
		overlap := false
		for rows.Next() {
			var kind, id, metadata string
			if err := rows.Scan(&kind, &id, &metadata); err != nil {
				rows.Close()
				return nil, err
			}
			for id := range observedContainerIDs(kind, id, json.RawMessage(metadata)) {
				overlap = overlap || containers[id]
			}
		}
		readErr := rows.Err()
		rows.Close()
		if readErr != nil {
			return nil, readErr
		}
		if overlap {
			return nil, ErrWorkloadAlreadyImported
		}
	}

	sealedHook, err := s.sealer.Seal(auth.RandomToken(24))
	if err != nil {
		return nil, err
	}
	now := s.now().UTC().Unix()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_projects(
		  name, profile, repo_path, branch, compose_file, pre_command, post_command,
		  hook_secret, hook_id, enabled, created_at, updated_at)
		VALUES(?, ?, '', '', '', '', '', ?, ?, 0, ?, ?)`,
		registration.Name, ProfileImported, sealedHook, auth.RandomToken(12), now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "deploy_projects.name") {
			return nil, ErrNameTaken
		}
		return nil, err
	}
	projectID, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	result, err = tx.ExecContext(ctx, `
		INSERT INTO deploy_environments(
		  project_id, name, slug, kind, desired_revision, strategy,
		  expected_downtime, protected, created_at, updated_at)
		VALUES(?, 'production', 'production', ?, 1, ?, 0, 1, ?, ?)`,
		projectID, EnvironmentProduction, StrategyStopFirst, now, now)
	if err != nil {
		return nil, err
	}
	environmentID, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	sourceJSON, err := json.Marshal(DraftSourceConfig{
		Kind: SourceImport, Mode: registration.SourceMode, ResourceID: registration.ResourceID,
	})
	if err != nil {
		return nil, err
	}
	identityJSON, err := json.Marshal(SourceIdentity{
		Kind: SourceImport, Repository: registration.Name, Observed: registration.Observed,
	})
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_sources(
		  environment_id, revision, kind, config_json, identity_json, digest, created_at)
		VALUES(?, 1, ?, ?, ?, ?, ?)`, environmentID, SourceImport,
		string(sourceJSON), string(identityJSON), digestBytes(sourceJSON, identityJSON), now); err != nil {
		return nil, err
	}
	build := BuildPlanConfig{
		Method: BuildNone, Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{},
	}
	buildJSON, err := json.Marshal(build)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_build_plans(
		  environment_id, revision, method, config_json, preview, digest, created_at)
		VALUES(?, 1, ?, ?, ?, ?, ?)`, environmentID, BuildNone, string(buildJSON),
		"External workload; build remains with its current owner.", buildPlanDigest(build), now); err != nil {
		return nil, err
	}
	runtimeJSON, err := json.Marshal(RuntimePlanConfig{Strategy: StrategyStopFirst})
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_runtime_plans(
		  environment_id, revision, config_json, preview, digest, created_at)
		VALUES(?, 1, ?, ?, ?, ?)`, environmentID, string(runtimeJSON),
		"External workload; runtime remains with its current owner.", digestBytes(runtimeJSON), now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_dependencies(
		  environment_id, release_id, kind, ownership, resource_kind,
		  resource_id, config_json, created_at)
		VALUES(?, 0, 'runtime', 'observed', ?, ?, ?, ?)`, environmentID,
		registration.ResourceKind, registration.ResourceID, string(registration.Observed), now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &DraftCommitResult{
		ProjectID: projectID, EnvironmentID: environmentID, PlanRevision: 1, Created: true,
	}, nil
}

func observedContainerIDs(kind, resourceID string, metadata json.RawMessage) map[string]bool {
	result := map[string]bool{}
	if kind == "docker_container" {
		result[resourceID] = true
		return result
	}
	var candidate struct {
		Services []struct {
			ResourceID string `json:"resourceId"`
		} `json:"services"`
	}
	if json.Unmarshal(metadata, &candidate) == nil {
		for _, service := range candidate.Services {
			if service.ResourceID != "" {
				result[service.ResourceID] = true
			}
		}
	}
	return result
}

func validateObservedWorkloadRegistration(registration ObservedWorkloadRegistration) error {
	if err := (DraftIntentConfig{Name: registration.Name, Profile: ProfileImported}).Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}
	resources := map[string]struct {
		mode SourceMode
		kind string
	}{
		"compose_stack":    {SourceModeExistingStack, "stack"},
		"docker_container": {SourceModeExistingContainer, "container"},
		"pm2_process":      {"existing_pm2", "pm2"},
		"systemd_unit":     {"existing_systemd", "systemd"},
		"host_process":     {"existing_process", "process"},
	}
	resource, valid := resources[registration.ResourceKind]
	if !valid || registration.SourceMode != resource.mode {
		return fmt.Errorf("%w: unsupported workload resource and source mode", ErrInvalidPlan)
	}
	if strings.TrimSpace(registration.ResourceID) == "" || len(registration.ResourceID) > 4096 ||
		!utf8.ValidString(registration.ResourceID) ||
		strings.IndexFunc(registration.ResourceID, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: workload resource identity is malformed", ErrInvalidPlan)
	}
	var metadata struct {
		Key        string `json:"key"`
		Kind       string `json:"kind"`
		ResourceID string `json:"resourceId"`
	}
	if len(registration.Observed) > maxEventBytes ||
		!utf8.Valid(registration.Observed) || json.Unmarshal(registration.Observed, &metadata) != nil {
		return fmt.Errorf("%w: workload observation must be a bounded JSON object", ErrInvalidPlan)
	}
	if strings.TrimSpace(metadata.Key) == "" || len(metadata.Key) > 4096 ||
		strings.IndexFunc(metadata.Key, unicode.IsControl) >= 0 ||
		metadata.Kind != resource.kind || metadata.ResourceID != registration.ResourceID {
		return fmt.Errorf("%w: workload observation does not match its resource identity", ErrInvalidPlan)
	}
	return nil
}
