package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// planWithManagedVolume re-saves a complete draft's configuration with one
// managed volume mounted and declared as its storage.
func planWithManagedVolume(t *testing.T, plans *PlanningStore, draft *Draft, volume string) *Draft {
	t.Helper()
	configuration := *draft.Data.Configuration
	configuration.Runtime.Mounts = []RuntimeMount{{Source: volume, Target: "/data", Ownership: OwnershipManaged}}
	configuration.Dependencies = append(append([]PlannedDependency(nil), configuration.Dependencies...), PlannedDependency{
		Kind: "storage", Ownership: OwnershipManaged, ResourceKind: "docker_volume", ResourceID: volume,
	})
	saved, err := plans.Save(context.Background(), draft.ID, draft.OwnerUserID, true, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftConfiguration, Configuration: &configuration,
	})
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func preflightAndSave(t *testing.T, plans *PlanningStore, draft *Draft) (*Draft, *PreflightResult) {
	t.Helper()
	ctx := context.Background()
	preflight, err := PreflightDraft(ctx, draft, &preflightObserverFake{observation: HostObservation{
		Facilities: map[string]FacilityObservation{"git": {Available: true}}, Paths: []PathObservation{}, Ports: []PortObservation{},
	}}, true)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := plans.SavePreflight(ctx, draft.ID, draft.OwnerUserID, true, draft.Revision, preflight)
	if err != nil {
		t.Fatal(err)
	}
	return saved, preflight
}

func warningCodes(findings []PreflightFinding) []string {
	var codes []string
	for _, item := range findings {
		if item.Severity == PreflightWarning {
			codes = append(codes, item.Code)
		}
	}
	return codes
}

func TestAManagedVolumeBelongsToOneProject(t *testing.T) {
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	create := func() *Draft {
		draft, err := fixture.plans.Create(ctx, 41, "operator")
		if err != nil {
			t.Fatal(err)
		}
		return planWithManagedVolume(t, fixture.plans, saveCompletePlanningDraft(t, fixture.plans, draft), "shop-1a2b3c4d-data")
	}

	first, racing := create(), create()
	first, preflight := preflightAndSave(t, fixture.plans, first)
	if findingSeverity(preflight.Findings, "storage_owned_by_other_project") != "" {
		t.Fatalf("a volume nobody owns was refused: %+v", preflight.Findings)
	}
	// The second draft's preflight ran before the first project existed.
	racing, racingPreflight := preflightAndSave(t, fixture.plans, racing)
	if _, err := fixture.plans.Commit(ctx, first.ID, 41, true, DraftCommitRequest{
		Revision: first.Revision, AcknowledgedWarnings: warningCodes(preflight.Findings),
	}); err != nil {
		t.Fatal(err)
	}

	// A later draft reusing the name is refused before Deploy, naming the owner.
	later := create()
	_, refused := preflightAndSave(t, fixture.plans, later)
	var owned PreflightFinding
	for _, item := range refused.Findings {
		if item.Code == "storage_owned_by_other_project" {
			owned = item
		}
	}
	if owned.Severity != PreflightBlocked || !strings.Contains(owned.Measured, "shop-1a2b3c4d-data — managed by planned-app") ||
		owned.FieldID != "runtime.mounts" {
		t.Fatalf("owned volume finding = %+v", owned)
	}
	// The draft whose preflight predates the owner is refused at commit.
	if _, err := fixture.plans.Commit(ctx, racing.ID, 41, true, DraftCommitRequest{
		Revision: racing.Revision, AcknowledgedWarnings: warningCodes(racingPreflight.Findings),
	}); !errors.Is(err, ErrInvalidPlan) || !strings.Contains(err.Error(), "managed volume shop-1a2b3c4d-data") {
		t.Fatalf("racing commit error = %v", err)
	}

	// Linking the volume shares it on purpose without owning it.
	if owners, err := managedVolumeOwners(ctx, fixture.store.DB, plannedManagedVolumes(PlanConfiguration{
		Runtime: RuntimePlanConfig{Mounts: []RuntimeMount{
			{Source: "shop-1a2b3c4d-data", Target: "/data", Ownership: OwnershipLinked},
			{Source: "/srv/shop", Target: "/srv", Ownership: OwnershipManaged},
			{Source: "fresh-volume", Target: "/cache", Ownership: OwnershipManaged},
		}},
	})); err != nil || len(owners) != 0 {
		t.Fatalf("linked, bind and unowned mounts: owners = %v, %v", owners, err)
	}
}
