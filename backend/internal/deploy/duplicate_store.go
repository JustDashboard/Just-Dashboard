package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"gopkg.in/yaml.v3"
)

// Duplicate builds a fresh, resumable draft pre-filled from projectID's own
// production environment: the same source and desired build/runtime/
// dependency/check configuration an operator would see on the Settings
// pages, so the new-project wizard opens already configured instead of
// blank. It never touches the source project — a duplicate is additive, not
// destructive, and needs no capability beyond the one the route already
// requires.
//
// Runtime authority and Compose project identity are never copied. Sources
// with fixed container/network identities or shared writable storage require
// an independent configuration before duplication. Three other things are
// deliberately not copied byte for byte:
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
	if source.Kind == SourceImport && source.Mode != SourceModeExistingCheckout {
		return nil, fmt.Errorf("%w: imported workloads must be configured through their existing manager", ErrInvalidPlan)
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
	if err := validateDuplicateStorageAndCompose(source, plan); err != nil {
		return nil, err
	}
	// Runtime dependencies reserve another project's existing manager. A new
	// draft must earn its own runtime ownership when its first release starts.
	dependencies := make([]PlannedDependency, 0, len(plan.Dependencies))
	for _, dependency := range plan.Dependencies {
		if dependency.Kind != "runtime" {
			dependencies = append(dependencies, dependency)
		}
	}
	plan.Dependencies = dependencies
	plan.Runtime.ComposeProjectName = ""
	plan.Runtime.PreviewIsolation = false
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

// A draft has no authority over its source project's live resources. Keep
// ordinary project-scoped Compose resources, but do not guess a replacement
// identity for fixed names, shared networks or writable host data.
func validateDuplicateStorageAndCompose(source DraftSourceConfig, plan PlanConfiguration) error {
	for _, mount := range plan.Runtime.Mounts {
		if !mount.ReadOnly && (mount.Ownership != OwnershipManaged || pathShapedMountSource(mount.Source)) {
			return fmt.Errorf("%w: duplication would share writable storage; configure independent storage before duplicating this project", ErrInvalidPlan)
		}
	}
	if plan.Build.Method != BuildCompose && source.Kind != SourceCompose {
		return nil
	}
	if len(source.ComposeFiles) == 0 {
		return fmt.Errorf("%w: inspect the Compose files and give the copy independent container names, networks and storage before duplicating", ErrInvalidPlan)
	}
	analysis, err := analyzeComposeDocuments(source.ComposeFiles)
	if err != nil || len(analysis.Unsupported) > 0 {
		return fmt.Errorf("%w: Compose source could not be checked for safe duplication", ErrInvalidPlan)
	}
	models := make([]*yaml.Node, 0, len(source.ComposeFiles))
	volumeDefinitions := map[string]*yaml.Node{}
	for _, document := range source.ComposeFiles {
		var root yaml.Node
		if err := yaml.Unmarshal([]byte(document.Content), &root); err != nil {
			return fmt.Errorf("%w: Compose source could not be checked for safe duplication", ErrInvalidPlan)
		}
		model := documentMapping(&root)
		if duplicateComposeUsesMergeOrAlias(model) {
			return fmt.Errorf("%w: resolve Compose aliases and merge keys into an independent file before duplicating", ErrInvalidPlan)
		}
		models = append(models, model)
		if volumes := mappingValue(model, "volumes"); volumes != nil && volumes.Kind == yaml.MappingNode {
			for index := 0; index+1 < len(volumes.Content); index += 2 {
				name, definition := volumes.Content[index].Value, volumes.Content[index+1]
				if previous := volumeDefinitions[name]; previous == nil || composeResourceHasFixedIdentity(definition) {
					volumeDefinitions[name] = definition
				}
			}
		}
	}
	for _, model := range models {
		networks := mappingValue(model, "networks")
		if networks != nil && networks.Kind == yaml.MappingNode {
			for index := 1; index < len(networks.Content); index += 2 {
				definition := networks.Content[index]
				if composeResourceHasFixedIdentity(definition) {
					return fmt.Errorf("%w: duplication would join a shared Compose network and publish the original service aliases; configure independent networks first", ErrInvalidPlan)
				}
			}
		}
		services := mappingValue(model, "services")
		for index := 1; services != nil && index < len(services.Content); index += 2 {
			service := services.Content[index]
			for _, field := range []string{"container_name", "external_links", "volumes_from"} {
				if value := mappingValue(service, field); value != nil && value.Tag != "!!null" {
					return fmt.Errorf("%w: Compose %s identifies an existing runtime; remove it from an independent Compose copy before duplicating", ErrInvalidPlan, field)
				}
			}
			for _, field := range []string{"network_mode", "ipc", "pid"} {
				if value := mappingValue(service, field); value != nil && (value.Value == "host" || strings.HasPrefix(value.Value, "container:") || strings.Contains(value.Value, "${")) {
					return fmt.Errorf("%w: Compose %s shares a host or existing runtime namespace; configure isolation before duplicating", ErrInvalidPlan, field)
				}
			}
			volumes := mappingValue(service, "volumes")
			if volumes == nil {
				continue
			}
			if volumes.Kind != yaml.SequenceNode {
				return fmt.Errorf("%w: Compose storage cannot be checked for safe duplication", ErrInvalidPlan)
			}
			for _, mount := range volumes.Content {
				if duplicateComposeMountIsSharedWritable(volumeDefinitions, mount) {
					return fmt.Errorf("%w: duplication would share writable Compose storage; configure independent volumes or read-only mounts first", ErrInvalidPlan)
				}
			}
		}
	}
	return nil
}

func composeResourceHasFixedIdentity(node *yaml.Node) bool {
	if external := mappingValue(node, "external"); external != nil && external.Tag != "!!null" && external.Value != "false" {
		return true
	}
	if name := mappingValue(node, "name"); name != nil && name.Value != "" {
		return true
	}
	return mappingValue(node, "driver_opts") != nil
}

func duplicateComposeUsesMergeOrAlias(node *yaml.Node) bool {
	if node == nil {
		return false
	}
	if node.Kind == yaml.AliasNode || node.Value == "<<" {
		return true
	}
	for _, child := range node.Content {
		if duplicateComposeUsesMergeOrAlias(child) {
			return true
		}
	}
	return false
}

func duplicateComposeMountIsSharedWritable(volumeDefinitions map[string]*yaml.Node, mount *yaml.Node) bool {
	var source, kind string
	readOnly := false
	if mount.Kind == yaml.ScalarNode {
		parts := strings.Split(mount.Value, ":")
		if len(parts) == 1 {
			return false // a fresh anonymous volume
		}
		source = parts[0]
		if len(parts) > 2 {
			writable := false
			for _, option := range strings.Split(parts[len(parts)-1], ",") {
				if option == "ro" {
					readOnly = true
				}
				if option == "rw" {
					writable = true
				}
			}
			readOnly = readOnly && !writable
		}
	} else if mount.Kind == yaml.MappingNode {
		if value := mappingValue(mount, "read_only"); value != nil && value.Value == "true" {
			readOnly = true
		}
		if value := mappingValue(mount, "source"); value != nil {
			source = value.Value
		}
		if value := mappingValue(mount, "type"); value != nil {
			kind = value.Value
		}
	} else {
		return true
	}
	if source == "" && (kind == "volume" || kind == "tmpfs") {
		return false
	}
	if kind == "bind" || pathShapedMountSource(source) || strings.Contains(source, "$") {
		return !readOnly
	}
	definition := volumeDefinitions[source]
	// Named volumes absent from this file may come from another override. A
	// copy cannot assume they are private to its new Compose project.
	if definition == nil {
		return true
	}
	if readOnly {
		if external := mappingValue(definition, "external"); external != nil && external.Value != "false" && external.Tag != "!!null" {
			return false
		}
	}
	return composeResourceHasFixedIdentity(definition)
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
