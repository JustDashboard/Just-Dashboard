package deploy

import (
	"context"
	"errors"
	"testing"
)

// seedSizingEnvironment writes an environment at revision 1 with the given
// build method and runtime plan, the way a commit leaves one.
func seedSizingEnvironment(t *testing.T, fixture *planningStoreFixture, project, kind, method, runtime string) (int64, int64) {
	t.Helper()
	db := fixture.store.DB
	result, err := db.Exec(`INSERT INTO deploy_projects(name, profile, repo_path, hook_secret, hook_id, created_at)
		VALUES(?, 'web', '', '', ?, 0)`, project, project+"-hook")
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := result.LastInsertId()
	result, err = db.Exec(`INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, created_at, updated_at)
		VALUES(?, ?, ?, ?, 1, 0, 0)`, projectID, kind, kind, kind)
	if err != nil {
		t.Fatal(err)
	}
	environmentID, _ := result.LastInsertId()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at)
		  VALUES(?, 1, ?, '{}', '{}', '{}', 'sha256:b', 0)`, []any{environmentID, method}},
		{`INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at)
		  VALUES(?, 1, ?, '{}', 'sha256:r', 0)`, []any{environmentID, runtime}},
	} {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	return projectID, environmentID
}

// seedSizingRelease records a release of the environment and the runtime it
// ran as, live when said.
func seedSizingRelease(t *testing.T, fixture *planningStoreFixture, environmentID int64, number int, kind, runtimeID string, live bool) {
	t.Helper()
	db := fixture.store.DB
	var projectID, planID int64
	if err := db.QueryRow(`SELECT e.project_id, r.id FROM deploy_environments e JOIN deploy_runtime_plans r
		ON r.environment_id = e.id AND r.revision = e.desired_revision WHERE e.id = ?`, environmentID).Scan(&projectID, &planID); err != nil {
		t.Fatal(err)
	}
	result, err := db.Exec(`INSERT INTO deploy_releases(project_id, environment_id, release_number, runtime_plan_id, created_at)
		VALUES(?, ?, ?, ?, 0)`, projectID, environmentID, number, planID)
	if err != nil {
		t.Fatal(err)
	}
	releaseID, _ := result.LastInsertId()
	if _, err := db.Exec(`INSERT INTO deploy_release_runtimes(release_id, environment_id, kind, runtime_id, created_at, updated_at)
		VALUES(?, ?, ?, ?, 0, 0)`, releaseID, environmentID, kind, runtimeID); err != nil {
		t.Fatal(err)
	}
	if live {
		if _, err := db.Exec(`UPDATE deploy_environments SET live_release_id = ? WHERE id = ?`, releaseID, environmentID); err != nil {
			t.Fatal(err)
		}
	}
}

// The targets carry the saved and the live limits, every container release
// newest first with the live one apart, the managed volumes (mounts and
// storage dependencies, never a host path or a linked volume) and a live
// Compose release's project; the fleet is production and staging only.
func TestSizingTargetsReadThePlansAndTheReleases(t *testing.T) {
	fixture := newPlanningStoreFixture(t)
	ctx := context.Background()
	shop, shopEnv := seedSizingEnvironment(t, fixture, "shop", "production", "image",
		`{"strategy":"stop_first","memoryMb":512,"cpus":1.5,"mounts":[
		  {"source":"shop-uploads","target":"/data","ownership":"managed"},
		  {"source":"/srv/shop","target":"/srv","ownership":"managed"},
		  {"source":"shared-cache","target":"/cache","ownership":"linked"}]}`)
	seedSizingRelease(t, fixture, shopEnv, 1, "container", "aaa", false)
	seedSizingRelease(t, fixture, shopEnv, 2, "container", "bbb", true)
	if _, err := fixture.store.DB.Exec(`INSERT INTO deploy_dependencies(environment_id, release_id, kind, ownership, resource_kind, resource_id, config_json, created_at)
		VALUES(?, 0, 'storage', 'managed', 'docker_volume', 'shop-media', '{}', 0)`, shopEnv); err != nil {
		t.Fatal(err)
	}
	_, stackEnv := seedSizingEnvironment(t, fixture, "stack", "production", "compose", `{"strategy":"stop_first"}`)
	seedSizingRelease(t, fixture, stackEnv, 1, "compose", "jd-stack", true)
	preview, previewEnv := seedSizingEnvironment(t, fixture, "pr", "preview", "image", `{"strategy":"stop_first"}`)

	targets, err := fixture.plans.SizingTargets(ctx, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || targets[0].ProjectName != "shop" || targets[1].ProjectName != "stack" {
		t.Fatalf("targets = %+v", targets)
	}
	target := targets[0]
	if target.MemoryMB != 512 || target.CPUs != 1.5 || target.LiveMemoryMB != 512 || target.Compose() {
		t.Errorf("limits = %+v", target)
	}
	if len(target.ContainerIDs) != 2 || target.ContainerIDs[0] != "bbb" || len(target.LiveContainerIDs) != 1 || target.LiveContainerIDs[0] != "bbb" {
		t.Errorf("containers = %v live %v", target.ContainerIDs, target.LiveContainerIDs)
	}
	if len(target.Volumes) != 2 || target.Volumes[0] != "shop-media" || target.Volumes[1] != "shop-uploads" {
		t.Errorf("volumes = %v", target.Volumes)
	}
	if stack := targets[1]; !stack.Compose() || stack.ComposeProject != "jd-stack" || len(stack.ContainerIDs) != 0 {
		t.Errorf("stack = %+v", stack)
	}

	one, err := fixture.plans.SizingTargets(ctx, preview, previewEnv)
	if err != nil || len(one) != 1 || one[0].EnvironmentKind != EnvironmentPreview {
		t.Errorf("a preview by id = %+v %v", one, err)
	}
	if _, err := fixture.plans.SizingTargets(ctx, shop, previewEnv); !errors.Is(err, ErrEnvironmentNotFound) {
		t.Errorf("another deployment's environment = %v", err)
	}
}
