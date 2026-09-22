package deploy

import (
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/blueprint"
)

func renderGamePlan(t *testing.T, name string, inputs map[string]string) *BlueprintPlan {
	t.Helper()
	plan, err := RenderBlueprintPlan(DraftSourceConfig{
		Kind: SourceBlueprint, Mode: SourceModeBlueprint,
		BlueprintID: "minecraft-java", BlueprintVersion: "1.0.0", BlueprintInputs: inputs,
	}, name)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

// Updating the server software must never move the world. The volume a
// deployment's data lives in is derived from the deployment, not from the
// release, so every release of it — including a rollback — mounts the same one.
func TestChangingServerSoftwareOrVersionNeverMovesTheWorldVolume(t *testing.T) {
	t.Parallel()
	before := renderGamePlan(t, "family survival", map[string]string{"eula": "true"})
	after := renderGamePlan(t, "family survival", map[string]string{
		"eula": "true", "server-type": "PAPER", "version": "1.21.4", "memory": "6144",
	})
	if before.Rendered.Digest == after.Rendered.Digest {
		t.Fatal("two different server builds rendered the same plan")
	}
	if len(before.Configuration.Runtime.Mounts) != 1 || len(after.Configuration.Runtime.Mounts) != 1 {
		t.Fatalf("mounts = %#v / %#v", before.Configuration.Runtime.Mounts, after.Configuration.Runtime.Mounts)
	}
	first, second := before.Configuration.Runtime.Mounts[0], after.Configuration.Runtime.Mounts[0]
	if first.Source != second.Source || first.Target != second.Target {
		t.Fatalf("the world moved between releases: %#v -> %#v", first, second)
	}
	if first.ReadOnly || second.ReadOnly {
		t.Fatal("the world volume was mounted read-only")
	}
	// The data volume is a declared managed dependency, so removing it is a
	// separately previewed and separately confirmed destructive action rather
	// than something a redeploy can do.
	storage := 0
	for _, dependency := range after.Configuration.Dependencies {
		if dependency.Kind == "storage" && dependency.ResourceID == second.Source {
			storage++
			if dependency.Ownership != OwnershipManaged {
				t.Fatalf("the world volume is owned as %q", dependency.Ownership)
			}
		}
	}
	if storage != 1 {
		t.Fatalf("the world volume is not a declared dependency: %#v", after.Configuration.Dependencies)
	}
}

// Two deployments of the same blueprint on one host must not share a world.
func TestTwoDeploymentsOfOneBlueprintGetSeparateData(t *testing.T) {
	t.Parallel()
	survival := renderGamePlan(t, "family survival", map[string]string{"eula": "true"})
	creative := renderGamePlan(t, "creative build", map[string]string{"eula": "true"})
	if survival.Configuration.Runtime.Mounts[0].Source == creative.Configuration.Runtime.Mounts[0].Source {
		t.Fatalf("both deployments mount %q", survival.Configuration.Runtime.Mounts[0].Source)
	}
	for _, plan := range []*BlueprintPlan{survival, creative} {
		source := plan.Configuration.Runtime.Mounts[0].Source
		if strings.ContainsAny(source, " /\\") || source == "" {
			t.Fatalf("volume name %q is not a plain Docker volume name", source)
		}
	}
}

// The EULA is consent to one specific document. The plan records which one, so
// the audit trail says what was agreed rather than that a box was ticked.
func TestMinecraftPlanRecordsTheExactAcceptedAgreement(t *testing.T) {
	t.Parallel()
	plan := renderGamePlan(t, "audited", map[string]string{"eula": "true"})
	if len(plan.Rendered.Acceptances) != 1 {
		t.Fatalf("acceptances = %#v", plan.Rendered.Acceptances)
	}
	acceptance := plan.Rendered.Acceptances[0]
	if acceptance.Input != "eula" || acceptance.URL != "https://aka.ms/MinecraftEULA" ||
		strings.TrimSpace(acceptance.Label) == "" {
		t.Fatalf("acceptance = %#v", acceptance)
	}
	// And nothing starts without it.
	if _, err := RenderBlueprintPlan(DraftSourceConfig{
		Kind: SourceBlueprint, Mode: SourceModeBlueprint,
		BlueprintID: "minecraft-java", BlueprintVersion: "1.0.0",
		BlueprintInputs: map[string]string{"eula": "false"},
	}, "unaccepted"); err == nil {
		t.Fatal("an unaccepted EULA produced a deployable plan")
	}
}

// A game server is stop-first because it holds exclusive local data and a
// fixed UDP/TCP port. Promising a zero-downtime cutover would be a lie the
// activation machinery could not keep.
func TestGameBlueprintsAreHonestlyStopFirstAndPublishTheirOwnPort(t *testing.T) {
	t.Parallel()
	plan := renderGamePlan(t, "honest", map[string]string{"eula": "true"})
	if plan.Configuration.Runtime.Strategy != StrategyStopFirst {
		t.Fatalf("strategy = %q", plan.Configuration.Runtime.Strategy)
	}
	if plan.Configuration.Runtime.HostPort != 25565 || plan.Configuration.Runtime.BindAddress != "0.0.0.0" {
		t.Fatalf("runtime = %#v", plan.Configuration.Runtime)
	}
	// The RCON port is internal and must never reach the host.
	for _, port := range plan.Rendered.Ports {
		if port.Name == "rcon" && port.Exposure != "internal" {
			t.Fatalf("rcon is exposed as %q", port.Exposure)
		}
	}
}

// A database blueprint must reach the plan with no published port at all.
func TestDatabaseBlueprintsNeverPublishAPortIntoThePlan(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"postgresql", "mariadb", "redis", "mongodb"} {
		plan, err := RenderBlueprintPlan(DraftSourceConfig{
			Kind: SourceBlueprint, Mode: SourceModeBlueprint,
			BlueprintID: id, BlueprintVersion: "1.0.0",
		}, "db")
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if plan.Configuration.Runtime.HostPort != 0 {
			t.Fatalf("%s publishes host port %d", id, plan.Configuration.Runtime.HostPort)
		}
		if plan.Configuration.Runtime.BindAddress != "127.0.0.1" {
			t.Fatalf("%s binds %q", id, plan.Configuration.Runtime.BindAddress)
		}
		if len(plan.Configuration.Domains) != 0 {
			t.Fatalf("%s asks for a public domain", id)
		}
		generated := 0
		for _, variable := range plan.Configuration.Variables {
			if variable.Sensitivity == "secret" {
				generated++
			}
		}
		definition, err := blueprint.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if generated == 0 && definition.Access.Kind != blueprint.AccessOpen {
			t.Fatalf("%s declares no generated credential and does not say it is open", id)
		}
	}
}

// Rendering must not leak a generated secret into the plan the operator
// previews, the release snapshot, or anything derived from either.
func TestBlueprintPlansCarryGeneratedSecretsByNameOnly(t *testing.T) {
	t.Parallel()
	entries, err := blueprint.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		// The reviewed fixture is the definition's own example of a complete
		// set of answers; the loop below only fills what it leaves open.
		inputs := map[string]string{}
		if len(entry.Fixtures) > 0 {
			for name, value := range entry.Fixtures[0].Inputs {
				inputs[name] = value
			}
		}
		for _, input := range entry.Inputs {
			if input.Kind == blueprint.InputAccept {
				inputs[input.Name] = "true"
			}
			if input.Required && input.Default == "" && input.Kind == blueprint.InputDomain {
				inputs[input.Name] = "example.test"
			}
		}
		plan, renderErr := RenderBlueprintPlan(DraftSourceConfig{
			Kind: SourceBlueprint, Mode: SourceModeBlueprint,
			BlueprintID: entry.ID, BlueprintVersion: entry.Version, BlueprintInputs: inputs,
		}, "secret-probe")
		if renderErr != nil {
			t.Fatalf("%s: %v", entry.ID, renderErr)
		}
		for _, variable := range plan.Rendered.Variables {
			if variable.Generated && variable.Value != "" {
				t.Fatalf("%s rendered a value for %s", entry.ID, variable.Name)
			}
		}
		for _, planned := range plan.Configuration.Variables {
			for _, secret := range entry.Secrets {
				if planned.Name == secret.Variable && planned.Sensitivity != "secret" {
					t.Fatalf("%s carries %s as %q", entry.ID, planned.Name, planned.Sensitivity)
				}
			}
		}
	}
}

// A blueprint's default schedules must translate exactly into the scheduler's
// own closed vocabulary, or not at all.
func TestBlueprintDefaultSchedulesTranslateExactlyOrNotAtAll(t *testing.T) {
	t.Parallel()
	plan := renderGamePlan(t, "scheduled", map[string]string{"eula": "true"})
	writes := BlueprintSchedules(plan.Rendered)
	if len(writes) != 1 {
		t.Fatalf("schedules = %#v, want only the preset marked default", writes)
	}
	nightly := writes[0]
	// No backup job is linked at creation, so the preset arrives paused rather
	// than failing every night with an empty backup step.
	if nightly.Name != "Nightly world backup" || nightly.Expression != "0 4 * * *" ||
		nightly.Timezone != "UTC" || nightly.Enabled {
		t.Fatalf("schedule = %#v", nightly)
	}
	// Saving before backing up is the whole point of the preset; a backup of an
	// unsaved world is a backup of the last autosave.
	if len(nightly.Steps) != 2 || nightly.Steps[0].Action != "game_command" ||
		nightly.Steps[1].Action != "backup" {
		t.Fatalf("steps = %#v", nightly.Steps)
	}
	if !strings.Contains(string(nightly.Steps[0].Config), "save-all flush") {
		t.Fatalf("save step = %s", nightly.Steps[0].Config)
	}
	for _, step := range nightly.Steps {
		if !validScheduleAction(step.Action) {
			t.Fatalf("step %q is outside the scheduler's vocabulary", step.Action)
		}
	}

	// An action the scheduler cannot express drops the whole preset rather than
	// running an approximation of it.
	untranslatable := *plan.Rendered
	untranslatable.Automation = []blueprint.Automation{{
		Name: "Impossible", Cron: "0 4 * * *", Actions: []string{"backup", "teleport"}, Default: true,
	}}
	if got := BlueprintSchedules(&untranslatable); len(got) != 0 {
		t.Fatalf("an untranslatable preset became %#v", got)
	}
}

// A definition with a routed web port and a second port the proxy cannot carry
// (Gitea's SSH, Syncthing's sync protocol) keeps the web port as the routed
// one and publishes the other on its own number. Before this, the second port
// silently replaced the first: the proxy would have sent browsers to SSH.
func TestSecondaryDirectPortsArePublishedNextToTheRoutedPort(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		id        string
		routed    int
		published int
	}{{"gitea", 3000, 2222}, {"syncthing", 8384, 22000}} {
		plan, err := RenderBlueprintPlan(DraftSourceConfig{
			Kind: SourceBlueprint, Mode: SourceModeBlueprint, BlueprintID: fixture.id, BlueprintVersion: "1.0.0",
			BlueprintInputs: map[string]string{"domain": fixture.id + ".example.test"},
		}, fixture.id)
		if err != nil {
			t.Fatalf("%s: %v", fixture.id, err)
		}
		runtime := plan.Configuration.Runtime
		if runtime.InternalPort != fixture.routed || runtime.HostPort != 0 || runtime.BindAddress != "127.0.0.1" ||
			runtime.Strategy != StrategyStopFirst || runtime.Protocol != "tcp" {
			t.Fatalf("%s runtime = %#v", fixture.id, runtime)
		}
		if len(runtime.Ports) != 1 || runtime.Ports[0] != (PublishedPort{HostPort: fixture.published, ContainerPort: fixture.published, Protocol: "tcp"}) {
			t.Fatalf("%s published ports = %#v", fixture.id, runtime.Ports)
		}
		if err := plan.Configuration.Validate(); err != nil {
			t.Fatalf("%s: %v", fixture.id, err)
		}
	}
	// A game server's single direct port is still the fixed host port, not a
	// second publication.
	game := renderGamePlan(t, "solo", map[string]string{"eula": "true"})
	if game.Configuration.Runtime.HostPort != 25565 || len(game.Configuration.Runtime.Ports) != 0 {
		t.Fatalf("game runtime = %#v", game.Configuration.Runtime)
	}
}

// What `/deploy/new` can answer on the operator's behalf, and what it cannot.
//
// The template panel sends the inputs it has: a definition's own defaults, and
// — for a `domain` input — the hostname `/deploy/hostname` suggests, which
// resolves to this server with no DNS record to create. Everything else is a
// value only the operator holds, and the panel marks it Required and refuses
// the press rather than letting `Render` refuse the draft.
//
// Without this, eight reviewed definitions declared a required `domain`, the
// panel sent none, and choosing n8n, Nextcloud, Vaultwarden, Wallabag, Shlink,
// Directus, Healthchecks or Gitea ended in `"domain" is required` — the server
// naming its own field, after a draft had already been created. The live
// catalogue sweep never saw it because it renders from each definition's
// fixtures, which supply every input.
func TestEveryTemplateDeploysFromWhatTheNewProjectPageCanFillIn(t *testing.T) {
	t.Parallel()
	// Deployments that genuinely cannot start without a value nobody but the
	// operator has. Each entry is a template that costs one typed field, not a
	// template that is broken — but the list is short on purpose: a new
	// definition joining it is a new template nobody can deploy in one press.
	typed := map[string][]string{
		"mongo-express": {"mongodb-url"},
		"pgadmin":       {"admin-email"},
		"directus":      {"admin-email"},
		"nocodb":        {"admin-email"},
		"open-webui":    {"admin-email"},
	}
	definitions, err := blueprint.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range definitions {
		if supported, _ := blueprint.DeploymentSupport(definition); !supported {
			continue
		}
		t.Run(definition.ID, func(t *testing.T) {
			inputs := map[string]string{}
			for _, input := range definition.Inputs {
				if input.Kind == blueprint.InputDomain {
					inputs[input.Name] = "app-2f9c1a.203-0-113-7.sslip.io"
				}
			}
			for _, name := range typed[definition.ID] {
				// Stood in for here so the rest of the render is still checked;
				// the panel asks the operator for exactly these.
				inputs[name] = "operator@example.com"
				if name == "mongodb-url" {
					inputs[name] = "mongodb://root:secret@10.0.0.2:27017/"
				}
			}
			if _, err := RenderBlueprintPlan(DraftSourceConfig{
				Kind: SourceBlueprint, Mode: SourceModeBlueprint,
				BlueprintID: definition.ID, BlueprintVersion: definition.Version,
				BlueprintInputs: inputs,
			}, definition.Name); err != nil {
				t.Fatalf("a chosen template refused the page's own inputs: %v", err)
			}
			// Anything the page has to ask for must be declared required, which
			// is what makes it a marked field and a refusal before the press
			// rather than the server's own error string afterwards.
			for _, name := range typed[definition.ID] {
				input, found := "", false
				for _, declared := range definition.Inputs {
					if declared.Name == name {
						input, found = declared.Name, declared.Required
					}
				}
				if input == "" || !found {
					t.Fatalf("%s must declare %q required so the page can ask for it", definition.ID, name)
				}
			}
		})
	}
}
