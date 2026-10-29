package deploy

import (
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/blueprint"
)

// The three gates that once refused a game server — draft save, a stale
// draft's preflight and commit validation — now let it through, because the
// release path runs what it used to refuse. Each still re-validates the
// stored source, so the refusal for what remains unsupported is the same
// code path (DeploymentSupport, tested in internal/blueprint).
func TestGameBlueprintPassesTheGatesThatOnceRefusedIt(t *testing.T) {
	fixture := newPlanningStoreFixture(t)
	draft, err := fixture.plans.Create(t.Context(), 41, "operator")
	if err != nil {
		t.Fatal(err)
	}
	source := DraftSourceConfig{Kind: SourceBlueprint, Mode: SourceModeBlueprint, BlueprintID: "minecraft-java", BlueprintVersion: "1.0.0", BlueprintInputs: map[string]string{"eula": "true"}}
	saved, err := fixture.plans.Save(t.Context(), draft.ID, 41, true, DraftSaveRequest{Revision: draft.Revision, Step: DraftSource, Source: &source})
	if err != nil || saved.Data.Source == nil || saved.Data.Source.BlueprintID != "minecraft-java" {
		t.Fatalf("save: %#v %v", saved, err)
	}
	if err := source.ValidateForDeployment(); err != nil {
		t.Fatalf("a game source is refused at materialization and queue admission: %v", err)
	}
	// The EULA is still the gate it was: Render refuses an unaccepted
	// agreement, so no plan exists to deploy.
	unaccepted := source
	unaccepted.BlueprintInputs = map[string]string{"eula": "false"}
	if _, err := RenderBlueprintPlan(unaccepted, "x"); err == nil {
		t.Fatal("an unaccepted EULA renders a deployable plan")
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
	runtime := bedrock.Configuration.Runtime
	if runtime.Protocol != "tcp+udp" || runtime.HostPort != 19132 || runtime.InternalPort != 19132 ||
		runtime.BindAddress != "127.0.0.1" || len(runtime.Ports) != 0 {
		t.Fatalf("Bedrock's port is not one fixed loopback publication on TCP and UDP: %#v", runtime)
	}
	if err := bedrock.Configuration.Validate(); err != nil {
		t.Fatalf("a UDP plan the runtime can publish is refused: %v", err)
	}
	// Never silently converted to TCP, and never routed: a domain on a UDP
	// plan is a route the proxy could not carry.
	routed := bedrock.Configuration
	routed.Domains = []PlannedDomain{{Hostname: "mc.example.com", Ownership: OwnershipManaged}}
	if err := routed.Validate(); err == nil || !strings.Contains(err.Error(), "UDP") {
		t.Fatalf("a routed UDP plan = %v", err)
	}
	mappings := runtimePortMappings(runtime, runtime.BindAddress, runtime.HostPort)
	if len(mappings) != 2 || mappings[0].Protocol != "tcp" || mappings[1].Protocol != "udp" {
		t.Fatalf("Bedrock is published as %#v", mappings)
	}
	for _, mapping := range mappings {
		if mapping.ContainerPort != 19132 || mapping.HostPort != 19132 || mapping.HostIP != "127.0.0.1" {
			t.Fatalf("a publication left the plan's bind address: %#v", mapping)
		}
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
	// An editable file is the Server settings page's, so only a game may
	// declare one; a file an image needs before it starts is an install
	// write_file, which every single-image profile may ship.
	withFile := *mustBlueprint(t, "prometheus")
	withFile.Files = []blueprint.ConfigFile{{Path: "/etc/prometheus/prometheus.yml", Label: "Scrape configuration", Format: "yaml"}}
	if err := blueprint.Validate(&withFile); err == nil || !strings.Contains(err.Error(), "Server settings") {
		t.Fatalf("a web blueprint declared an editable file nothing edits: %v", err)
	}
	for _, id := range []string{"redis", "postgresql", "uptime-kuma", "dozzle", "prometheus", "gitea", "minecraft-java", "minecraft-bedrock"} {
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
