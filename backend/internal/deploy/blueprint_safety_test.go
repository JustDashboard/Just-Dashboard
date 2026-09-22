package deploy

import (
	"errors"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/blueprint"
)

func TestUnsupportedBlueprintStopsBeforeDraftSaveAndPreflight(t *testing.T) {
	fixture := newPlanningStoreFixture(t)
	draft, err := fixture.plans.Create(t.Context(), 41, "operator")
	if err != nil {
		t.Fatal(err)
	}
	source := DraftSourceConfig{Kind: SourceBlueprint, Mode: SourceModeBlueprint, BlueprintID: "minecraft-java", BlueprintVersion: "1.0.0", BlueprintInputs: map[string]string{"eula": "true"}}
	if _, err := fixture.plans.Save(t.Context(), draft.ID, 41, true, DraftSaveRequest{Revision: draft.Revision, Step: DraftSource, Source: &source}); !errors.Is(err, ErrUnsupportedSource) {
		t.Fatalf("save: %v", err)
	}
	persisted, err := fixture.plans.Get(t.Context(), draft.ID)
	if err != nil || persisted.Data.Source != nil || persisted.Revision != draft.Revision {
		t.Fatalf("failed save changed draft: %#v %v", persisted, err)
	}
	stale := completePlanningDraftModel()
	stale.Data.Source = &source
	if _, err := PreflightDraft(t.Context(), stale, nil, true); !errors.Is(err, ErrUnsupportedSource) {
		t.Fatalf("stale preflight: %v", err)
	}
	stale.CurrentStep, stale.PlanPreview = DraftPreflight, "old preview"
	if err := validateDraftComplete(stale); !errors.Is(err, ErrUnsupportedSource) {
		t.Fatalf("stale commit: %v", err)
	}
}
func TestBlueprintVolumeSlugsCannotCollide(t *testing.T) {
	for _, pair := range [][2]string{{"same_name", "same-name"}, {"App", "app"}, {strings.Repeat("a", 50) + "x", strings.Repeat("a", 50) + "y"}} {
		if blueprintVolumePrefix(pair[0], "minecraft-java") == blueprintVolumePrefix(pair[1], "minecraft-java") {
			t.Fatalf("colliding pair %q", pair)
		}
	}
}
func TestBlueprintPreviewKeepsSocketReadOnlyAndUDPExplicit(t *testing.T) {
	plan, err := RenderBlueprintPlan(DraftSourceConfig{BlueprintID: "dozzle", BlueprintVersion: "1.0.0"}, "logs")
	if err != nil {
		t.Fatal(err)
	}
	mount := plan.Configuration.Runtime.Mounts[0]
	if mount.Source != "/var/run/docker.sock" || !mount.ReadOnly || mount.Ownership != OwnershipLinked {
		t.Fatalf("socket is not a linked read-only mount: %#v", mount)
	}
	for _, dependency := range plan.Configuration.Dependencies {
		if strings.Contains(dependency.ResourceID, "docker.sock") {
			t.Fatalf("socket mapped to owned storage: %#v", dependency)
		}
	}
	bedrock, err := RenderBlueprintPlan(DraftSourceConfig{BlueprintID: "minecraft-bedrock", BlueprintVersion: "1.0.0", BlueprintInputs: map[string]string{"eula": "true"}}, "bedrock")
	if err != nil {
		t.Fatal(err)
	}
	if bedrock.Configuration.Runtime.Protocol != "udp" {
		t.Fatal("lost UDP protocol")
	}
	if err := bedrock.Configuration.Validate(); err == nil {
		t.Fatal("unsupported UDP plan accepted as runnable")
	}
	if schedules := BlueprintSchedules(bedrock.Rendered); len(schedules) != 0 {
		t.Fatalf("unsafe Bedrock save preset: %#v", schedules)
	}
	java := renderGamePlan(t, "java", map[string]string{"eula": "true"})
	for _, check := range java.Configuration.Checks {
		if err := validateCheckConfiguration(check.Kind, check.Config); err != nil {
			t.Fatalf("%s: %v", check.Name, err)
		}
	}
	// Blueprints the release path cannot run end to end say so in the
	// catalogue; the ones it can are offered without a caveat. No shipped
	// definition declares a pre-start configuration file any more — that is
	// what kept Gitea and Prometheus undeployable — so the refusal is proved
	// against the rule rather than against a definition that no longer breaks it.
	withFile := *mustBlueprint(t, "prometheus")
	withFile.Files = []blueprint.ConfigFile{{Path: "/etc/prometheus/prometheus.yml", Label: "Scrape configuration", Format: "yaml"}}
	if summary := blueprint.Summarize(&withFile); summary.DeploymentSupported || !strings.Contains(summary.UnavailableReason, "configuration files") {
		t.Fatalf("catalogue hides the config-file limitation: %+v", summary)
	}
	if summary := blueprint.Summarize(mustBlueprint(t, "minecraft-java")); summary.DeploymentSupported || !strings.Contains(summary.UnavailableReason, "game-server") {
		t.Fatalf("catalogue hides the game limitation: %+v", summary)
	}
	for _, id := range []string{"redis", "postgresql", "uptime-kuma", "dozzle", "prometheus", "gitea"} {
		if summary := blueprint.Summarize(mustBlueprint(t, id)); !summary.DeploymentSupported || summary.UnavailableReason != "" {
			t.Fatalf("%s should be deployable: %+v", id, summary)
		}
	}
}
func mustBlueprint(t *testing.T, id string) *blueprint.Blueprint {
	t.Helper()
	definition, err := blueprint.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return definition
}
