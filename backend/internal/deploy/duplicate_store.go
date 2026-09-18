package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// Duplicate builds a fresh, resumable draft pre-filled from projectID's own
// production environment: the same source and desired build/runtime/
// dependency/check configuration an operator would see on the Settings
// pages, so the new-project wizard opens already configured instead of
// blank. It never touches the source project — a duplicate is additive, not
// destructive, and needs no capability beyond the one the route already
// requires.
//
// Three things are deliberately not copied byte for byte:
//
//   - Domains are dropped entirely: a hostname belongs to one project, and
//     carrying one into a second draft would offer to attach it a second
//     time.
//   - Variables are flattened to name, sensitivity and scopes with every
//     value cleared and Required set: a secret's plaintext is never
//     available to copy, and a reference (a credential, a database, another
//     variable) names something scoped to the source project, so resolving
//     it unexamined in a new one would silently wire the duplicate to the
//     original's own database or leaked secret.
//   - A managed Docker volume's literal name is re-derived rather than
//     copied, because that name is the resource itself: committing the
//     duplicate unchanged would hand it the source project's live volume
//     instead of one of its own.
func (s *PlanningStore) Duplicate(
	ctx context.Context,
	sourceProjectID, ownerUserID int64,
	ownerUsername, name string,
) (*Draft, error) {
	intent := DraftIntentConfig{Name: strings.TrimSpace(name)}
	var environmentID int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT e.id, p.profile
		  FROM deploy_environments e
		  JOIN deploy_projects p ON p.id = e.project_id
		 WHERE e.project_id = ? AND e.slug = 'production' AND e.archived_at = 0`, sourceProjectID).
		Scan(&environmentID, &intent.Profile); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if err := intent.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}

	var sourceJSON string
	if err := s.db.QueryRowContext(ctx, `
		SELECT config_json FROM deploy_sources
		 WHERE environment_id = ? AND revision = (SELECT desired_revision FROM deploy_environments WHERE id = ?)`,
		environmentID, environmentID).Scan(&sourceJSON); err != nil {
		return nil, err
	}
	var source DraftSourceConfig
	if json.Unmarshal([]byte(sourceJSON), &source) != nil {
		return nil, fmt.Errorf("%w: source configuration is malformed", ErrInvalidSource)
	}
	source = canonicalSourceConfig(source)

	configuration, err := s.EnvironmentConfiguration(ctx, sourceProjectID, environmentID)
	if err != nil {
		return nil, err
	}
	plan := canonicalConfiguration(PlanConfiguration{
		Build:        configuration.Build,
		Runtime:      configuration.Runtime,
		Dependencies: configuration.Dependencies,
		Checks:       configuration.Checks,
		Domains:      []PlannedDomain{}, // a hostname belongs to one project
	})
	plan.Runtime, plan.Dependencies = rederiveManagedVolumeNames(name, plan.Runtime, plan.Dependencies)
	plan.Variables = make([]PlannedVariable, 0, len(configuration.Variables))
	for _, variable := range configuration.Variables {
		plan.Variables = append(plan.Variables, PlannedVariable{
			Name: variable.Name, Sensitivity: variable.Sensitivity,
			Scopes: variable.Scopes, Required: true,
		})
	}

	now := s.now().UTC()
	draft := &Draft{
		ID: auth.RandomToken(18), OwnerUserID: ownerUserID, OwnerUsername: ownerUsername,
		CurrentStep: DraftConfiguration, Revision: 1,
		Data:      DraftData{Intent: &intent, Source: &source, Configuration: &plan},
		Findings:  []PreflightFinding{},
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(draftTTL),
	}
	data, err := json.Marshal(draft.Data)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO deploy_drafts(
		  id, owner_user_id, owner_username, current_step, revision, data_json,
		  findings_json, plan_preview, created_at, updated_at, expires_at)
		VALUES(?, ?, ?, ?, ?, ?, '[]', '', ?, ?, ?)`,
		draft.ID, draft.OwnerUserID, draft.OwnerUsername, draft.CurrentStep, draft.Revision,
		string(data), now.Unix(), now.Unix(), draft.ExpiresAt.Unix()); err != nil {
		return nil, err
	}
	return draft, nil
}

// rederiveManagedVolumeNames replaces every dashboard-managed Docker
// volume's literal name with one freshly derived from the new project's own
// name, using the exact <slug>-<hash> shape a blueprint's own volumes
// already get (blueprintVolumePrefix), keyed here on the old literal name
// instead of a blueprint id so two different mounts never collide with each
// other. A mount whose ownership is linked or observed, or whose source is a
// bind path rather than a bare volume name, points at something this
// project does not own the lifecycle of and is left exactly as saved.
func rederiveManagedVolumeNames(
	newName string, runtime RuntimePlanConfig, dependencies []PlannedDependency,
) (RuntimePlanConfig, []PlannedDependency) {
	renamed := make(map[string]string)
	fresh := func(old string) string {
		if next, ok := renamed[old]; ok {
			return next
		}
		next := blueprintVolumePrefix(newName, old)
		renamed[old] = next
		return next
	}
	runtime.Mounts = append([]RuntimeMount(nil), runtime.Mounts...)
	for i, mount := range runtime.Mounts {
		if mount.Ownership == OwnershipManaged && !pathShapedMountSource(mount.Source) {
			runtime.Mounts[i].Source = fresh(mount.Source)
		}
	}
	dependencies = append([]PlannedDependency(nil), dependencies...)
	for i, dependency := range dependencies {
		if dependency.Kind == "storage" && dependency.ResourceKind == "docker_volume" && dependency.Ownership == OwnershipManaged {
			dependencies[i].ResourceID = fresh(dependency.ResourceID)
		}
	}
	return runtime, dependencies
}

// pathShapedMountSource mirrors PlanConfiguration.Validate's own test for a
// bind-mount path versus a bare Docker volume name, so re-derivation only
// ever touches the latter.
func pathShapedMountSource(source string) bool {
	return source == "" || strings.HasPrefix(source, "/") || strings.HasPrefix(source, ".") || strings.Contains(source, "/")
}
