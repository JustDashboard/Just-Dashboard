package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// A managed Docker volume belongs to exactly one project: the project's
// removal plan offers to delete it, and its releases stop one another around
// it. Two plans that both manage one name would read and write the same
// SQLite file and uploads, and deleting either project would take the other's
// data with it. The form names detected volumes per draft, so this is the
// backstop for a name reused by hand, a template's fixed name, or a race
// between two drafts.

// plannedManagedVolumes are the Docker volume names a plan would own: its
// managed mounts whose source is a volume name rather than a host path, and
// its managed storage dependencies.
func plannedManagedVolumes(configuration PlanConfiguration) []string {
	var names []string
	for _, mount := range configuration.Runtime.Mounts {
		if mount.Ownership == OwnershipManaged && !pathShapedMountSource(mount.Source) {
			names = append(names, mount.Source)
		}
	}
	for _, dependency := range configuration.Dependencies {
		if dependency.Kind == "storage" && dependency.ResourceKind == "docker_volume" &&
			dependency.Ownership == OwnershipManaged && dependency.ResourceID != "" {
			names = append(names, dependency.ResourceID)
		}
	}
	return uniqueSorted(names)
}

// managedVolumeOwners names, for each of the given volumes another project's
// current plan already manages, that project. An archived project still
// owns its volumes until it is deleted.
func managedVolumeOwners(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, names []string) (map[string]string, error) {
	owners := map[string]string{}
	if len(names) == 0 {
		return owners, nil
	}
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	const project = `CASE WHEN p.archived_name != '' THEN p.archived_name ELSE p.name END`
	rows, err := queryer.QueryContext(ctx, `
		SELECT `+project+`, rp.config_json FROM deploy_runtime_plans rp
		  JOIN deploy_environments e ON e.id = rp.environment_id AND e.desired_revision = rp.revision
		  JOIN deploy_projects p ON p.id = e.project_id
		 ORDER BY p.id, e.id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name, raw string
		if err := rows.Scan(&name, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		var runtime RuntimePlanConfig
		if json.Unmarshal([]byte(raw), &runtime) != nil {
			continue
		}
		for _, mount := range runtime.Mounts {
			if mount.Ownership == OwnershipManaged && wanted[mount.Source] && owners[mount.Source] == "" {
				owners[mount.Source] = name
			}
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(names)), ",")
	args := make([]any, 0, len(names))
	for _, name := range names {
		args = append(args, name)
	}
	rows, err = queryer.QueryContext(ctx, `
		SELECT `+project+`, d.resource_id FROM deploy_dependencies d
		  JOIN deploy_environments e ON e.id = d.environment_id
		  JOIN deploy_projects p ON p.id = e.project_id
		 WHERE d.release_id = 0 AND d.kind = 'storage' AND d.resource_kind = 'docker_volume'
		   AND d.ownership = 'managed' AND d.resource_id IN (`+placeholders+`)
		 ORDER BY p.id, e.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, volume string
		if err := rows.Scan(&name, &volume); err != nil {
			return nil, err
		}
		if owners[volume] == "" {
			owners[volume] = name
		}
	}
	return owners, rows.Err()
}

// managedVolumeOwnerFindings refuses each volume another project owns.
func managedVolumeOwnerFindings(owners map[string]string) []PreflightFinding {
	volumes := make([]string, 0, len(owners))
	for volume := range owners {
		volumes = append(volumes, volume)
	}
	sort.Strings(volumes)
	findings := make([]PreflightFinding, 0, len(volumes))
	for _, volume := range volumes {
		findings = append(findings, finding("storage_owned_by_other_project", PreflightBlocked,
			"Another project already owns this volume", volume+" — managed by "+owners[volume],
			"Both projects would read and write the same data, and removing "+owners[volume]+" offers to delete the volume this project uses.",
			"Give this project's mount a volume name of its own, or mark it Linked to share "+owners[volume]+"'s data on purpose without owning it.",
			"deploy", "runtime.mounts"))
	}
	return findings
}

// errManagedVolumeOwned is Commit's refusal when another project took a
// volume between preflight and commit.
func errManagedVolumeOwned(owners map[string]string) error {
	if len(owners) == 0 {
		return nil
	}
	return fmt.Errorf("%w: managed volume %s", ErrInvalidPlan, managedVolumeOwnerFindings(owners)[0].Measured)
}
