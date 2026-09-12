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

var (
	ErrRemovalPlanChanged = errors.New("deployment removal plan changed")
	ErrRemovalTarget      = errors.New("invalid deployment removal target")
	ErrDeploymentActive   = errors.New("deployment must be archived before managed resources are removed")
)

type RemovalTarget struct {
	ID                 string        `json:"id"`
	Kind               string        `json:"kind"`
	ResourceID         string        `json:"resourceId"`
	DisplayName        string        `json:"displayName"`
	Owner              string        `json:"owner"`
	Ownership          OwnershipMode `json:"ownership"`
	Data               bool          `json:"data"`
	RequiresAdmin      bool          `json:"requiresAdmin"`
	ConfirmationType   string        `json:"confirmationType"`
	ConfirmationPhrase string        `json:"confirmationPhrase,omitempty"`
	DeepLink           string        `json:"deepLink,omitempty"`
	WorkingDirectory   string        `json:"workingDirectory,omitempty"`
}

type RemovalPlan struct {
	DeploymentID int64           `json:"deploymentId"`
	Archived     bool            `json:"archived"`
	Targets      []RemovalTarget `json:"targets"`
	Digest       string          `json:"digest"`
	GeneratedAt  time.Time       `json:"generatedAt"`
}

type RemoveManagedRequest struct {
	PlanDigest string   `json:"planDigest"`
	TargetIDs  []string `json:"targetIds"`
}

type RemovalExecution struct {
	DeploymentID int64           `json:"deploymentId"`
	Removed      []RemovalTarget `json:"removed"`
	Remaining    []RemovalTarget `json:"remaining"`
}

type ManagedResourceRemover interface {
	RemoveManagedResource(context.Context, RemovalTarget) error
}

func (s *PlanningStore) RemovalPlan(ctx context.Context, projectID int64) (*RemovalPlan, error) {
	var archived int64
	if err := s.db.QueryRowContext(ctx, `SELECT archived_at FROM deploy_projects WHERE id = ?`, projectID).Scan(&archived); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	targets := []RemovalTarget{}
	seen := map[string]bool{}
	add := func(target RemovalTarget) {
		target.Ownership = OwnershipManaged
		if target.ConfirmationType == "" {
			target.ConfirmationType = "ordinary"
		}
		target.ID = removalTargetID(target.Kind, target.ResourceID)
		if target.ResourceID == "" || seen[target.ID] {
			return
		}
		seen[target.ID] = true
		targets = append(targets, target)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT rr.kind, rr.runtime_id, rr.name, rr.working_directory
		  FROM deploy_release_runtimes rr
		  JOIN deploy_releases r ON r.id = rr.release_id
		 WHERE r.project_id = ? AND rr.state <> 'removed'
		 ORDER BY rr.release_id DESC`, projectID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var kind, runtimeID, name, workingDirectory string
		if err := rows.Scan(&kind, &runtimeID, &name, &workingDirectory); err != nil {
			rows.Close()
			return nil, err
		}
		targetKind, owner, deepLink := "docker_container", "docker", "/docker/containers/"+runtimeID
		if kind == "compose" {
			targetKind, deepLink = "compose_stack", "/docker/compose"
		}
		if name == "" {
			name = runtimeID
		}
		add(RemovalTarget{Kind: targetKind, ResourceID: runtimeID, DisplayName: name,
			Owner: owner, DeepLink: deepLink, WorkingDirectory: workingDirectory})
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	rows, err = s.db.QueryContext(ctx, `
		SELECT DISTINCT e.id FROM deploy_dependencies d
		  JOIN deploy_environments e ON e.id = d.environment_id
		 WHERE e.project_id = ? AND d.release_id = 0 AND d.kind = 'domain' AND d.ownership = 'managed'
		 ORDER BY e.id`, projectID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var environmentID int64
		if err := rows.Scan(&environmentID); err != nil {
			rows.Close()
			return nil, err
		}
		name := deploymentRouteName(environmentID)
		add(RemovalTarget{Kind: "proxy_site", ResourceID: name, DisplayName: name,
			Owner: "proxy", DeepLink: "/proxy/sites"})
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	rows, err = s.db.QueryContext(ctx, `
		SELECT d.resource_kind, d.resource_id
		  FROM deploy_dependencies d JOIN deploy_environments e ON e.id = d.environment_id
		 WHERE e.project_id = ? AND d.release_id = 0 AND d.ownership = 'managed' AND d.kind <> 'domain'
		 ORDER BY d.resource_kind, d.resource_id`, projectID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var kind, resourceID string
		if err := rows.Scan(&kind, &resourceID); err != nil {
			rows.Close()
			return nil, err
		}
		target := RemovalTarget{Kind: kind, ResourceID: resourceID, DisplayName: resourceID,
			Owner: removalOwner(kind), DeepLink: removalDeepLink(kind, resourceID)}
		if kind == "docker_volume" || kind == "bind_path" {
			target.Data, target.RequiresAdmin = true, true
			target.ConfirmationType, target.ConfirmationPhrase = "typed", resourceID
		}
		add(target)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	rows, err = s.db.QueryContext(ctx, `
		SELECT rp.config_json FROM deploy_runtime_plans rp
		  JOIN deploy_environments e ON e.id = rp.environment_id AND e.desired_revision = rp.revision
		 WHERE e.project_id = ? ORDER BY e.id`, projectID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		var runtime RuntimePlanConfig
		if json.Unmarshal([]byte(raw), &runtime) != nil {
			rows.Close()
			return nil, fmt.Errorf("%w: runtime plan is malformed", ErrInvalidPlan)
		}
		for _, mount := range runtime.Mounts {
			if mount.Ownership != OwnershipManaged || mount.ReadOnly {
				continue
			}
			kind := "docker_volume"
			if strings.HasPrefix(mount.Source, "/") {
				kind = "bind_path"
			}
			add(RemovalTarget{Kind: kind, ResourceID: mount.Source, DisplayName: mount.Source,
				Owner: removalOwner(kind), Data: true, RequiresAdmin: true,
				ConfirmationType: "typed", ConfirmationPhrase: mount.Source,
				DeepLink: removalDeepLink(kind, mount.Source)})
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	rows, err = s.db.QueryContext(ctx, `
		SELECT a.kind, a.digest, a.reference
		  FROM deploy_release_artifacts a JOIN deploy_releases r ON r.id = a.release_id
		 WHERE r.project_id = ? AND a.state = 'available'
		 ORDER BY a.kind, a.digest, a.reference`, projectID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var kind ArtifactKind
		var digest, reference string
		if err := rows.Scan(&kind, &digest, &reference); err != nil {
			rows.Close()
			return nil, err
		}
		if kind == ArtifactImage {
			add(RemovalTarget{Kind: "docker_image", ResourceID: digest, DisplayName: reference + " · " + digest,
				Owner: "docker", DeepLink: "/docker/images"})
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	removedRows, err := s.db.QueryContext(ctx, `SELECT target_id FROM deploy_resource_removals WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, err
	}
	removed := map[string]bool{}
	for removedRows.Next() {
		var id string
		if err := removedRows.Scan(&id); err != nil {
			removedRows.Close()
			return nil, err
		}
		removed[id] = true
	}
	if err := removedRows.Close(); err != nil {
		return nil, err
	}
	filtered := targets[:0]
	for _, target := range targets {
		if !removed[target.ID] {
			filtered = append(filtered, target)
		}
	}
	targets = filtered
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Kind != targets[j].Kind {
			return targets[i].Kind < targets[j].Kind
		}
		return targets[i].ResourceID < targets[j].ResourceID
	})
	plan := &RemovalPlan{DeploymentID: projectID, Archived: archived != 0, Targets: targets, GeneratedAt: s.now().UTC()}
	plan.Digest = removalPlanDigest(plan)
	return plan, nil
}

func (s *PlanningStore) RemoveManaged(
	ctx context.Context,
	projectID int64,
	actor string,
	request RemoveManagedRequest,
	remover ManagedResourceRemover,
) (*RemovalExecution, error) {
	plan, err := s.RemovalPlan(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if !plan.Archived {
		return nil, ErrDeploymentActive
	}
	if request.PlanDigest == "" || request.PlanDigest != plan.Digest {
		return nil, ErrRemovalPlanChanged
	}
	if remover == nil || len(request.TargetIDs) == 0 {
		return nil, ErrRemovalTarget
	}
	byID := make(map[string]RemovalTarget, len(plan.Targets))
	for _, target := range plan.Targets {
		byID[target.ID] = target
	}
	selected := make([]RemovalTarget, 0, len(request.TargetIDs))
	seen := map[string]bool{}
	for _, id := range request.TargetIDs {
		target, ok := byID[id]
		if !ok || seen[id] {
			return nil, fmt.Errorf("%w: %s", ErrRemovalTarget, id)
		}
		seen[id] = true
		selected = append(selected, target)
	}
	execution := &RemovalExecution{DeploymentID: projectID, Removed: []RemovalTarget{}, Remaining: []RemovalTarget{}}
	for _, target := range selected {
		if err := remover.RemoveManagedResource(ctx, target); err != nil {
			return execution, fmt.Errorf("remove %s %s: %w", target.Kind, target.ResourceID, err)
		}
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO deploy_resource_removals(project_id, target_id, target_kind, resource_id, removed_by, removed_at)
			VALUES(?, ?, ?, ?, ?, ?)
			ON CONFLICT(project_id, target_id) DO NOTHING`, projectID, target.ID, target.Kind,
			target.ResourceID, actor, s.now().UTC().Unix()); err != nil {
			return execution, err
		}
		execution.Removed = append(execution.Removed, target)
	}
	remaining, err := s.RemovalPlan(ctx, projectID)
	if err != nil {
		return execution, err
	}
	execution.Remaining = remaining.Targets
	return execution, nil
}

func removalTargetID(kind, resourceID string) string {
	digest := strings.TrimPrefix(digestBytes([]byte(kind), []byte(resourceID)), "sha256:")
	return kind + ":" + digest[:20]
}

func removalPlanDigest(plan *RemovalPlan) string {
	copy := *plan
	copy.Digest = ""
	copy.GeneratedAt = time.Time{}
	raw, _ := json.Marshal(copy)
	return digestBytes(raw)
}

func removalOwner(kind string) string {
	switch kind {
	case "backup_job":
		return "backups"
	case "database_connection":
		return "databases"
	case "proxy_site":
		return "proxy"
	case "docker_volume", "bind_path", "docker_image", "docker_container", "compose_stack":
		return "docker"
	default:
		return kind
	}
}

func removalDeepLink(kind, resourceID string) string {
	switch kind {
	case "backup_job":
		return "/backups"
	case "database_connection":
		return "/databases/" + resourceID
	case "docker_volume":
		return "/docker/volumes/" + resourceID
	case "bind_path":
		return "/files?path=" + resourceID
	default:
		return ""
	}
}
