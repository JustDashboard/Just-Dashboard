package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// SizingTarget is one environment as right-sizing and the capacity forecast
// read it: the limits its saved plan sets and the ones its live release runs
// with, the containers its releases ran as, and the volumes it owns.
//
// It is read-only and reads only what the plans and the releases recorded:
// the containers are the release runtimes' own ids, so a container that
// merely carries the same name is never taken for one of this environment's.
type SizingTarget struct {
	ProjectID       int64
	ProjectName     string
	EnvironmentID   int64
	EnvironmentName string
	EnvironmentKind EnvironmentKind
	Revision        int
	Method          BuildMethod
	// The saved plan's limits (what applies with the next release) and the
	// live release's; zero for none.
	MemoryMB, LiveMemoryMB int64
	CPUs, LiveCPUs         float64
	LiveReleaseID          int64
	// ContainerIDs are the container releases' runtime ids, newest first;
	// LiveContainerIDs the live release's.
	ContainerIDs     []string
	LiveContainerIDs []string
	// Volumes are the Docker volumes the saved plan manages: its managed
	// named-volume mounts and managed storage dependencies.
	Volumes []string
	// ComposeProject is the live Compose release's project, whose volumes
	// Compose labels with it.
	ComposeProject string
}

// Compose reports whether the environment runs as a Compose stack, whose
// services keep the limits their file declares rather than the plan's.
func (t SizingTarget) Compose() bool {
	return t.Method == BuildCompose || t.Method == BuildLegacyCompose
}

// maxSizingReleases bounds the container ids read per environment: past it,
// releases are older than any retained sample.
const maxSizingReleases = 200

// SizingTargets reads the environments right-sizing judges. With both ids
// zero it is every production and staging environment of every unarchived
// deployment; with them, that one environment of that deployment, of any
// kind, or ErrEnvironmentNotFound.
func (s *PlanningStore) SizingTargets(ctx context.Context, projectID, environmentID int64) ([]SizingTarget, error) {
	query := `
		SELECT p.id, CASE WHEN p.archived_name != '' THEN p.archived_name ELSE p.name END,
		       e.id, e.name, e.kind, e.desired_revision, e.live_release_id, b.method, r.config_json,
		       COALESCE(lp.config_json, '')
		  FROM deploy_environments e
		  JOIN deploy_projects p ON p.id = e.project_id
		  JOIN deploy_build_plans b ON b.environment_id = e.id AND b.revision = e.desired_revision
		  JOIN deploy_runtime_plans r ON r.environment_id = e.id AND r.revision = e.desired_revision
		  LEFT JOIN deploy_releases lr ON lr.id = e.live_release_id AND lr.environment_id = e.id
		  LEFT JOIN deploy_runtime_plans lp ON lp.id = lr.runtime_plan_id
		 WHERE e.archived_at = 0 AND p.archived_at = 0`
	args := []any{}
	if environmentID > 0 {
		query += ` AND e.id = ? AND p.id = ?`
		args = append(args, environmentID, projectID)
	} else {
		query += ` AND e.kind IN ('production', 'staging')`
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY p.id, e.id`, args...)
	if err != nil {
		return nil, err
	}
	targets := []SizingTarget{}
	index := map[int64]int{}
	for rows.Next() {
		var target SizingTarget
		var desiredJSON, liveJSON string
		if err := rows.Scan(&target.ProjectID, &target.ProjectName, &target.EnvironmentID, &target.EnvironmentName,
			&target.EnvironmentKind, &target.Revision, &target.LiveReleaseID, &target.Method, &desiredJSON, &liveJSON); err != nil {
			rows.Close()
			return nil, err
		}
		var desired RuntimePlanConfig
		if json.Unmarshal([]byte(desiredJSON), &desired) != nil {
			continue // a malformed plan is the configuration page's to report
		}
		target.MemoryMB, target.CPUs = desired.MemoryMB, desired.CPUs
		var live RuntimePlanConfig
		if liveJSON != "" && json.Unmarshal([]byte(liveJSON), &live) == nil {
			target.LiveMemoryMB, target.LiveCPUs = live.MemoryMB, live.CPUs
		}
		for _, mount := range desired.Mounts {
			if mount.Ownership == OwnershipManaged && !pathShapedMountSource(mount.Source) {
				target.Volumes = append(target.Volumes, mount.Source)
			}
		}
		index[target.EnvironmentID] = len(targets)
		targets = append(targets, target)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if environmentID > 0 && len(targets) == 0 {
		return nil, ErrEnvironmentNotFound
	}
	if len(targets) == 0 {
		return targets, nil
	}
	ids := make([]string, 0, len(targets))
	idArgs := make([]any, 0, len(targets))
	for _, target := range targets {
		ids = append(ids, "?")
		idArgs = append(idArgs, target.EnvironmentID)
	}
	in := "(" + strings.Join(ids, ",") + ")"
	if err := s.scanSizingRows(ctx, `
		SELECT rt.environment_id, rt.kind, rt.runtime_id, rt.release_id = e.live_release_id
		  FROM deploy_release_runtimes rt JOIN deploy_environments e ON e.id = rt.environment_id
		 WHERE rt.environment_id IN `+in+` AND rt.runtime_id != ''
		 ORDER BY rt.environment_id, rt.release_id DESC`, idArgs, func(rows *sql.Rows) error {
		var environment int64
		var kind, runtimeID string
		var live bool
		if err := rows.Scan(&environment, &kind, &runtimeID, &live); err != nil {
			return err
		}
		target := &targets[index[environment]]
		switch kind {
		case "container":
			if len(target.ContainerIDs) < maxSizingReleases {
				target.ContainerIDs = append(target.ContainerIDs, runtimeID)
			}
			if live {
				target.LiveContainerIDs = append(target.LiveContainerIDs, runtimeID)
			}
		case "compose":
			if live {
				target.ComposeProject = runtimeID
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := s.scanSizingRows(ctx, `
		SELECT environment_id, resource_id FROM deploy_dependencies
		 WHERE environment_id IN `+in+` AND release_id = 0 AND kind = 'storage'
		   AND resource_kind = 'docker_volume' AND ownership = 'managed' AND resource_id != ''
		 ORDER BY environment_id, resource_id`, idArgs, func(rows *sql.Rows) error {
		var environment int64
		var name string
		if err := rows.Scan(&environment, &name); err != nil {
			return err
		}
		target := &targets[index[environment]]
		target.Volumes = append(target.Volumes, name)
		return nil
	}); err != nil {
		return nil, err
	}
	for i := range targets {
		targets[i].Volumes = uniqueSorted(targets[i].Volumes)
	}
	return targets, nil
}

func (s *PlanningStore) scanSizingRows(ctx context.Context, query string, args []any, each func(*sql.Rows) error) error {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := each(rows); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}
