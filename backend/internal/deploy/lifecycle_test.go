package deploy

import (
	"context"
	"errors"
	"testing"
)

type managedRemovalFake struct{ removed []RemovalTarget }

func (f *managedRemovalFake) RemoveManagedResource(_ context.Context, target RemovalTarget) error {
	f.removed = append(f.removed, target)
	return nil
}

// failingRemovalFake succeeds for every target except one kind, so a test can
// see both what already succeeded and what the remover said about the one
// that did not.
type failingRemovalFake struct {
	removed  []RemovalTarget
	failKind string
	err      error
}

func (f *failingRemovalFake) RemoveManagedResource(_ context.Context, target RemovalTarget) error {
	if target.Kind == f.failKind {
		return f.err
	}
	f.removed = append(f.removed, target)
	return nil
}

func TestArchivePreservesResourcesAndRemovalPlanNamesOnlyManagedTargets(t *testing.T) {
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	projectID, environmentID := insertConfigurationFixture(t, fixture)
	config, err := fixture.plans.EnvironmentConfiguration(ctx, projectID, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	config, err = fixture.plans.SaveEnvironmentConfiguration(ctx, projectID, environmentID, ConfigurationWriteRequest{
		Revision: config.Revision, Build: config.Build,
		Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst, Mounts: []RuntimeMount{
			{Source: "config-app-data", Target: "/data", Ownership: OwnershipManaged},
		}},
		Dependencies: []PlannedDependency{
			{Kind: "cache", Ownership: OwnershipObserved, ResourceKind: "redis", ResourceID: "observed-cache"},
			{Kind: "backup", Ownership: OwnershipLinked, ResourceKind: "backup_job", ResourceID: "9"},
		},
		Domains: []PlannedDomain{{Hostname: "app.example.test", Ownership: OwnershipManaged}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB.Exec(`
		INSERT INTO deploy_triggers(environment_id, name, kind, enabled, created_at, updated_at)
		VALUES(?, 'push', 'generic_hook', 1, ?, ?)`, environmentID, fixture.now.Unix(), fixture.now.Unix()); err != nil {
		t.Fatal(err)
	}
	legacy := NewStore(fixture.store, fixture.sealer, []string{t.TempDir()})
	archived, err := legacy.Archive(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if archived.ArchivedAt == nil || archived.Enabled {
		t.Fatalf("archived deployment = %#v", archived)
	}
	if _, err := legacy.Archive(ctx, projectID); err != nil {
		t.Fatal(err)
	}
	var reserved string
	if err := fixture.store.DB.QueryRow(`SELECT name FROM deploy_projects WHERE id = ?`, projectID).Scan(&reserved); err != nil {
		t.Fatal(err)
	}
	if reserved == archived.Name {
		t.Fatal("archived project still reserves its display name")
	}
	if _, err := fixture.store.DB.Exec(`INSERT INTO deploy_projects(name, repo_path, hook_secret, hook_id, created_at) VALUES(?, '/srv/new', 'sealed', 'new-project-hook', 1)`, archived.Name); err != nil {
		t.Fatalf("could not reuse deleted project name: %v", err)
	}
	again, err := legacy.Get(ctx, projectID)
	if err != nil || again.Name != archived.Name {
		t.Fatalf("archive lost historical name: %+v %v", again, err)
	}
	var dependencies, enabled int
	if err := fixture.store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_dependencies WHERE environment_id = ?`, environmentID).Scan(&dependencies); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.DB.QueryRow(`SELECT enabled FROM deploy_triggers WHERE environment_id = ?`, environmentID).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if dependencies != 3 || enabled != 0 {
		t.Fatalf("archive changed resources: dependencies=%d trigger enabled=%d", dependencies, enabled)
	}
	plan, err := fixture.plans.RemovalPlan(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Archived || plan.Digest == "" || len(plan.Targets) != 2 {
		t.Fatalf("removal plan = %#v", plan)
	}
	var route, volume *RemovalTarget
	for index := range plan.Targets {
		target := &plan.Targets[index]
		switch target.Kind {
		case "proxy_site":
			route = target
		case "docker_volume":
			volume = target
		}
		if target.ResourceID == "observed-cache" || target.ResourceID == "9" {
			t.Fatalf("non-managed target leaked into plan: %#v", target)
		}
	}
	if route == nil || volume == nil || volume.ConfirmationType != "typed" ||
		volume.ConfirmationPhrase != "config-app-data" || !volume.Data {
		t.Fatalf("removal target semantics route=%#v volume=%#v", route, volume)
	}
	remover := &managedRemovalFake{}
	execution, err := fixture.plans.RemoveManaged(ctx, projectID, "operator", RemoveManagedRequest{
		PlanDigest: plan.Digest, TargetIDs: []string{route.ID},
	}, remover)
	if err != nil {
		t.Fatal(err)
	}
	if len(execution.Removed) != 1 || len(remover.removed) != 1 || len(execution.Remaining) != 1 {
		t.Fatalf("removal execution = %#v, calls=%#v", execution, remover.removed)
	}

	// The first removal changed the plan (one fewer target); re-read it for a
	// current digest before removing more.
	plan, err = fixture.plans.RemovalPlan(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}

	// A remover failure must not collapse to a bare internal error: its own
	// sentence travels with the failure, and whatever already succeeded stays
	// visible in the partial execution instead of being lost.
	failing := &failingRemovalFake{failKind: "docker_volume", err: errors.New("Docker is unavailable")}
	execution, err = fixture.plans.RemoveManaged(ctx, projectID, "operator", RemoveManagedRequest{
		PlanDigest: plan.Digest, TargetIDs: []string{volume.ID},
	}, failing)
	var failure *RemovalFailure
	if !errors.As(err, &failure) {
		t.Fatalf("RemoveManaged error = %v (%T), want *RemovalFailure", err, err)
	}
	if failure.Error() != "Docker is unavailable" {
		t.Fatalf("failure message = %q, want the remover's own sentence verbatim", failure.Error())
	}
	if failure.Unavailable {
		t.Fatal("a plain remover error was classified Unavailable")
	}
	if len(execution.Removed) != 0 {
		t.Fatalf("execution.Removed = %#v, want nothing removed before the only target failed", execution.Removed)
	}
	if len(execution.Remaining) != 1 || execution.Remaining[0].Kind != "docker_volume" {
		t.Fatalf("execution.Remaining = %#v, want the volume that failed still listed", execution.Remaining)
	}

	// A missing owner (Docker not configured on this host, say) is a
	// different situation from the resource itself refusing removal.
	unavailable := &failingRemovalFake{failKind: "docker_volume", err: Unavailable(errors.New("Docker is unavailable"))}
	if _, err = fixture.plans.RemoveManaged(ctx, projectID, "operator", RemoveManagedRequest{
		PlanDigest: plan.Digest, TargetIDs: []string{volume.ID},
	}, unavailable); !errors.As(err, &failure) || !failure.Unavailable {
		t.Fatalf("owner-unavailable error = %v, want *RemovalFailure{Unavailable: true}", err)
	}
}

// A removal batch that fails partway through must keep everything the
// earlier targets in the same batch already removed — the response is not
// all-or-nothing, and the caller needs to know exactly what still needs
// attention.
func TestRemoveManagedKeepsEarlierSuccessesWhenALaterTargetInTheSameBatchFails(t *testing.T) {
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	projectID, environmentID := insertConfigurationFixture(t, fixture)
	config, err := fixture.plans.EnvironmentConfiguration(ctx, projectID, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.plans.SaveEnvironmentConfiguration(ctx, projectID, environmentID, ConfigurationWriteRequest{
		Revision: config.Revision, Build: config.Build,
		Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst, Mounts: []RuntimeMount{
			{Source: "batch-data", Target: "/data", Ownership: OwnershipManaged},
		}},
		Domains: []PlannedDomain{{Hostname: "batch.example.test", Ownership: OwnershipManaged}},
	}); err != nil {
		t.Fatal(err)
	}
	legacy := NewStore(fixture.store, fixture.sealer, []string{t.TempDir()})
	if _, err := legacy.Archive(ctx, projectID); err != nil {
		t.Fatal(err)
	}
	plan, err := fixture.plans.RemovalPlan(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	var route, volume *RemovalTarget
	for index := range plan.Targets {
		target := &plan.Targets[index]
		switch target.Kind {
		case "proxy_site":
			route = target
		case "docker_volume":
			volume = target
		}
	}
	if route == nil || volume == nil {
		t.Fatalf("removal plan = %#v", plan)
	}
	failing := &failingRemovalFake{failKind: "docker_volume", err: errors.New("Docker is unavailable")}
	execution, err := fixture.plans.RemoveManaged(ctx, projectID, "operator", RemoveManagedRequest{
		PlanDigest: plan.Digest, TargetIDs: []string{route.ID, volume.ID},
	}, failing)
	var failure *RemovalFailure
	if !errors.As(err, &failure) {
		t.Fatalf("RemoveManaged error = %v, want *RemovalFailure", err)
	}
	if len(execution.Removed) != 1 || execution.Removed[0].Kind != "proxy_site" {
		t.Fatalf("execution.Removed = %#v, want the route that succeeded before the volume failed", execution.Removed)
	}
	if len(execution.Remaining) != 1 || execution.Remaining[0].Kind != "docker_volume" {
		t.Fatalf("execution.Remaining = %#v, want the volume that failed still listed", execution.Remaining)
	}
	var removedRows int
	if err := fixture.store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_resource_removals WHERE project_id=?`, projectID).Scan(&removedRows); err != nil {
		t.Fatal(err)
	}
	if removedRows != 1 {
		t.Fatalf("recorded removals = %d, want exactly the route that succeeded", removedRows)
	}
}
