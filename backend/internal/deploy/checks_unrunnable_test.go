package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// unrunnableKinds are the closed check kinds the runner has no code for, with
// a word the refusal must carry so the reader knows what to use instead.
var unrunnableKinds = map[string]string{
	"dns":              "public_route",
	"tls":              "public_route",
	"backup_freshness": "storage dependency",
}

func TestSettingsCannotSaveACheckNothingRuns(t *testing.T) {
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	projectID, environmentID := insertConfigurationFixture(t, fixture)
	before, err := fixture.plans.EnvironmentConfiguration(ctx, projectID, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	write := func(check PlannedCheck) (*EnvironmentConfiguration, error) {
		return fixture.plans.SaveEnvironmentConfiguration(ctx, projectID, environmentID, ConfigurationWriteRequest{
			Revision: before.Revision,
			Build:    before.Build,
			Runtime: RuntimePlanConfig{
				Image: "alpine:3", InternalPort: 8080, HostPort: 18080,
				BindAddress: "127.0.0.1", Strategy: StrategyStopFirst,
			},
			Checks: []PlannedCheck{check},
		})
	}
	for kind, instead := range unrunnableKinds {
		_, err := write(PlannedCheck{Name: "edge", Kind: kind, Phase: "smoke"})
		if !errors.Is(err, ErrInvalidPlan) || !strings.Contains(err.Error(), "cannot run") ||
			!strings.Contains(err.Error(), instead) {
			t.Fatalf("saving a %s check = %v", kind, err)
		}
	}
	saved, err := write(PlannedCheck{Name: "edge", Kind: "public_route", Phase: "smoke"})
	if err != nil || len(saved.Checks) != 1 || saved.Checks[0].Kind != "public_route" {
		t.Fatalf("saving a public_route check = %#v, %v", saved, err)
	}
}

func TestADraftCannotAskForACheckNothingRuns(t *testing.T) {
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	draft, err := fixture.plans.Create(ctx, 41, "operator")
	if err != nil {
		t.Fatal(err)
	}
	draft = saveCompletePlanningDraft(t, fixture.plans, draft)
	configuration := *draft.Data.Configuration
	for kind, instead := range unrunnableKinds {
		configuration.Checks = []PlannedCheck{{Name: "edge", Kind: kind, Phase: "smoke"}}
		_, err := fixture.plans.Save(ctx, draft.ID, draft.OwnerUserID, true, DraftSaveRequest{
			Revision: draft.Revision, Step: DraftConfiguration, Configuration: &configuration,
		})
		if !errors.Is(err, ErrInvalidPlan) || !strings.Contains(err.Error(), "cannot run") ||
			!strings.Contains(err.Error(), instead) {
			t.Fatalf("a draft asking for a %s check = %v", kind, err)
		}
	}
}

// A plan stored before the refusal is re-validated before it runs. It must
// still pass, and the check must still be a warning when it is not required,
// so an upgrade does not stop a deployment that ran the day before.
func TestAStoredPlanWithACheckNothingRunsStillDeploys(t *testing.T) {
	t.Parallel()
	for kind := range unrunnableKinds {
		plan := PlanConfiguration{
			Build:     BuildPlanConfig{Method: BuildImage},
			Runtime:   RuntimePlanConfig{Image: "alpine:3", Strategy: StrategyStopFirst},
			Variables: []PlannedVariable{}, Dependencies: []PlannedDependency{}, Domains: []PlannedDomain{},
			Checks: []PlannedCheck{{Name: "edge", Kind: kind, Phase: "smoke", Config: json.RawMessage(`{}`)}},
		}
		if err := plan.Validate(); err != nil {
			t.Fatalf("a stored plan with a %s check no longer validates: %v", kind, err)
		}
		evidence := NewCheckRunner(nil).Run(context.Background(), plan.Checks[0], CheckTarget{})
		if evidence.Outcome != HealthWarning || len(evidence.Attempts) != 1 ||
			evidence.Attempts[0].Code != "check_owner_unavailable" {
			t.Fatalf("a %s check ran as %#v", kind, evidence)
		}
	}
	for _, kind := range []string{"http", "tcp", "docker_health", "command", "public_route", "game_handshake", "udp_probe"} {
		if reason := unrunnableCheck(kind); reason != "" {
			t.Fatalf("%s is a kind the runner runs, but is refused: %s", kind, reason)
		}
	}
}
