package blueprint

import (
	"encoding/json"
	"strings"
	"testing"
)

// Every built-in is parsed and validated at load. This is the gate that turns
// the supply-chain rules into something a reviewer cannot forget to apply.
func TestEveryBuiltInParsesAndValidates(t *testing.T) {
	t.Parallel()
	entries, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 10 {
		t.Fatalf("catalogue has %d blueprints; the reviewed set is meant to cover every category", len(entries))
	}
	categories := map[Category]int{}
	for _, entry := range entries {
		categories[entry.Category]++
	}
	for _, category := range []Category{CategoryHTTP, CategoryDatabase, CategoryTool, CategoryAutomation, CategoryGame} {
		if categories[category] == 0 {
			t.Fatalf("no blueprint proves the %q category", category)
		}
	}
}

// A fixture pins the exact plan a blueprint version renders. Changing a
// blueprint without changing its fixture digest is the review failure this
// catches.
func TestEveryFixtureRendersToItsRecordedDigest(t *testing.T) {
	t.Parallel()
	entries, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		for _, fixture := range entry.Fixtures {
			plan, renderErr := Render(entry, fixture.Inputs)
			if renderErr != nil {
				t.Fatalf("%s/%s: %v", entry.ID, fixture.Name, renderErr)
			}
			if fixture.Digest == "" {
				t.Fatalf("%s/%s has no recorded digest; it renders to %s", entry.ID, fixture.Name, plan.Digest)
			}
			if plan.Digest != fixture.Digest {
				t.Fatalf("%s/%s renders to %s, fixture records %s", entry.ID, fixture.Name, plan.Digest, fixture.Digest)
			}
		}
	}
}

func TestRenderingIsDeterministicAndSecretFree(t *testing.T) {
	t.Parallel()
	entries, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		for _, fixture := range entry.Fixtures {
			first, _ := Render(entry, fixture.Inputs)
			second, _ := Render(entry, fixture.Inputs)
			encodedFirst, _ := json.Marshal(first)
			encodedSecond, _ := json.Marshal(second)
			if string(encodedFirst) != string(encodedSecond) {
				t.Fatalf("%s/%s renders differently twice", entry.ID, fixture.Name)
			}
			// Generated secrets appear by name, length and sensitivity. A value
			// in a rendered plan would end up in the plan preview, the release
			// snapshot and the audit trail.
			for _, variable := range first.Variables {
				if variable.Generated && variable.Value != "" {
					t.Fatalf("%s/%s rendered a value for generated secret %s", entry.ID, fixture.Name, variable.Name)
				}
			}
		}
	}
}

// No blueprint may ship a working password. This is checked from the parsed
// data rather than from the validator, so a future loosening of the validator
// still fails here.
func TestNoBuiltInShipsADefaultCredential(t *testing.T) {
	t.Parallel()
	entries, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		for _, input := range entry.Inputs {
			if input.Default == "" {
				continue
			}
			upper := strings.ToUpper(input.Variable + " " + input.Name)
			for _, marker := range secretLookalikes {
				if strings.Contains(upper, marker) {
					t.Fatalf("%s input %q ships a default for a credential", entry.ID, input.Name)
				}
			}
		}
		for _, secret := range entry.Secrets {
			if secret.Length < 24 {
				t.Fatalf("%s secret %q is only %d characters", entry.ID, secret.Name, secret.Length)
			}
		}
	}
}

// A database reachable from the internet by default is the worst thing a
// catalogue can ship. A stateful workload with nowhere to keep state is second.
func TestBuiltInsNeverExposeADatabaseAndAlwaysDeclarePersistence(t *testing.T) {
	t.Parallel()
	entries, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Profile == ProfileDatabase {
			for _, port := range entry.Ports {
				if port.Exposure != "internal" {
					t.Fatalf("%s exposes database port %q as %q", entry.ID, port.Name, port.Exposure)
				}
			}
		}
		if entry.Profile != ProfileDatabase && entry.Profile != ProfileGame {
			continue
		}
		data := false
		for _, volume := range entry.Volumes {
			data = data || volume.Data
		}
		if !data {
			t.Fatalf("%s is stateful and declares no persistent data volume", entry.ID)
		}
	}
}

func TestBuiltInsDeclareAHealthCheckThatSeparatesStartedFromWorking(t *testing.T) {
	t.Parallel()
	entries, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		required := 0
		for _, check := range entry.Checks {
			if check.Required {
				required++
			}
		}
		if required == 0 {
			t.Fatalf("%s has no required readiness check", entry.ID)
		}
	}
}

// A privileged feature has to be asked for in writing. Nothing in the reviewed
// set currently needs one, and adding one silently should be impossible.
func TestNoBuiltInTakesPrivilegeWithoutDeclaringWhy(t *testing.T) {
	t.Parallel()
	entries, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		security := entry.Security
		privileged := security.Privileged || security.HostNetwork ||
			len(security.Capabilities) > 0 || len(security.Devices) > 0
		if privileged && strings.TrimSpace(security.Reason) == "" {
			t.Fatalf("%s takes privilege without a reason", entry.ID)
		}
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"id":"probe","version":"1.0.0","name":"Probe","surpriseField":true}`)
	if _, err := Parse(raw); err == nil || !strings.Contains(err.Error(), "unsupported field") {
		t.Fatalf("unknown field accepted: %v", err)
	}
}

func minimalBlueprint() *Blueprint {
	return &Blueprint{
		ID: "probe", Version: "1.0.0", Name: "Probe", Description: "A probe.",
		Category: CategoryTool, Profile: ProfileTool, IconID: "box",
		DocsURL: "https://example.test/docs",
		Provenance: Provenance{
			Maintainer: "Just Dashboard", License: "MIT", UpstreamURL: "https://example.test",
			ReviewedAt: "2026-09-11", MinimumDashboard: "0.6.7",
		},
		Image:     Image{Reference: "probe:1.0.0", TagPolicy: "pinned"},
		Ports:     []Port{{Name: "http", Internal: 8080, Protocol: "tcp", Purpose: "Interface", Exposure: "proxy", Primary: true}},
		Resources: Resources{MemoryMB: 128},
		Checks:    []Check{{Name: "Answers", Kind: CheckHTTP, Phase: "readiness", Required: true, Path: "/"}},
		Update:    Update{Detector: "none"},
		Fixtures:  []Fixture{{Name: "default"}},
	}
}

func TestValidationRejectsEachUnsafeShape(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name   string
		mutate func(*Blueprint)
		want   string
	}{
		{"a default password", func(b *Blueprint) {
			b.Inputs = []Input{{Name: "pass", Kind: InputText, Label: "Password", Variable: "APP_PASSWORD", Default: "changeme"}}
		}, "ships a default"},
		{"a published database port", func(b *Blueprint) {
			b.Profile = ProfileDatabase
			b.Volumes = []Volume{{Name: "data", Target: "/data", Purpose: "Tables", Data: true}}
			b.Ports[0].Exposure = "direct"
		}, "databases stay internal"},
		{"a stateful workload with no data volume", func(b *Blueprint) {
			b.Profile = ProfileGame
		}, "must declare a persistent data volume"},
		{"no required readiness check", func(b *Blueprint) {
			b.Checks[0].Required = false
		}, "required readiness check"},
		{"an unreviewed operation", func(b *Blueprint) {
			b.Operations.Startup = []Operation{{Kind: "run_shell", Value: "rm -rf /"}}
		}, "unsupported kind"},
		{"an artifact with no checksum or version source", func(b *Blueprint) {
			b.Operations.Install = []Operation{{Kind: OperationFetchArtifact, URL: "https://example.test/x.zip", MaxBytes: 1024}}
		}, "neither a checksum nor a version source"},
		{"an artifact with no size limit", func(b *Blueprint) {
			b.Operations.Install = []Operation{{Kind: OperationFetchArtifact, URL: "https://example.test/x.zip", Checksum: "sha256:" + strings.Repeat("a", 64)}}
		}, "without a size limit"},
		{"an artifact fetched over plain HTTP", func(b *Blueprint) {
			b.Operations.Install = []Operation{{Kind: OperationFetchArtifact, URL: "http://example.test/x.zip", MaxBytes: 1024, Checksum: "sha256:" + strings.Repeat("a", 64)}}
		}, "is not an https URL"},
		{"a file written outside the container root", func(b *Blueprint) {
			b.Operations.Install = []Operation{{Kind: OperationWriteFile, Path: "/data/../../etc/passwd"}}
		}, "uncontained path"},
		{"a volume mounted through a parent reference", func(b *Blueprint) {
			b.Volumes = []Volume{{Name: "data", Target: "/data/../etc", Purpose: "Escape", Data: true}}
		}, "not a contained absolute path"},
		{"privilege with no reason", func(b *Blueprint) {
			b.Security = Security{Privileged: true}
		}, "without a reason"},
		{"a mutable tag with no explanation", func(b *Blueprint) {
			b.Image.TagPolicy = "mutable"
		}, "must explain itself"},
		{"an automation action outside the closed set", func(b *Blueprint) {
			b.Automation = []Automation{{Name: "Nightly", Description: "x", Cron: "0 3 * * *", Actions: []string{"exec"}}}
		}, "outside the closed set"},
		{"an acceptance with no linked agreement", func(b *Blueprint) {
			b.Inputs = []Input{{Name: "eula", Kind: InputAccept, Label: "Accept", Required: true}}
		}, "accepts nothing in particular"},
		{"an acceptance that is pre-accepted", func(b *Blueprint) {
			b.Inputs = []Input{{Name: "eula", Kind: InputAccept, Label: "Accept", Required: true,
				AcceptURL: "https://example.test/eula", Default: "true"}}
		}, "pre-accepts an agreement"},
		{"a console command carrying a newline", func(b *Blueprint) {
			b.Operations.Stop = []Operation{{Kind: OperationConsole, Value: "stop\nrm -rf /"}}
		}, "containing a newline"},
		{"the HTTP proxy asked to carry UDP", func(b *Blueprint) {
			b.Ports[0].Protocol = "udp"
		}, "asks the HTTP proxy to carry udp"},
		{"a check naming a port that does not exist", func(b *Blueprint) {
			b.Checks = []Check{{Name: "Handshake", Kind: CheckTCP, Phase: "readiness", Required: true, Port: "ghost"}}
		}, "names undeclared port"},
		{"a fixture-free blueprint", func(b *Blueprint) {
			b.Fixtures = nil
		}, "render fixture is required"},
		{"a generated secret shorter than review allows", func(b *Blueprint) {
			b.Secrets = []Secret{{Name: "token", Variable: "APP_TOKEN", Label: "Token", Length: 8}}
		}, "generated secrets are 24-128"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			candidate := minimalBlueprint()
			testCase.mutate(candidate)
			err := Validate(candidate)
			if err == nil {
				t.Fatalf("accepted %s", testCase.name)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error %q does not mention %q", err, testCase.want)
			}
		})
	}
}

func TestRenderRejectsUndeclaredAndInvalidInput(t *testing.T) {
	t.Parallel()
	minecraft, err := Get("minecraft-java")
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name   string
		inputs map[string]string
		want   string
	}{
		{"an input the blueprint never declared", map[string]string{"eula": "true", "run-as-root": "true"}, "is not an input"},
		{"a choice that was never offered", map[string]string{"eula": "true", "server-type": "BUNGEECORD"}, "is not an offered option"},
		{"memory below the declared minimum", map[string]string{"eula": "true", "memory": "64"}, "must be at least"},
		{"a version that is not a version", map[string]string{"eula": "true", "version": "$(curl evil)"}, "does not match the required format"},
		{"a name carrying a newline", map[string]string{"eula": "true", "server-name": "hi\nstop"}, "control character"},
		{"the EULA left unaccepted", map[string]string{"eula": "false"}, "must be accepted"},
		{"the EULA left unanswered", map[string]string{}, "is required"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if _, renderErr := Render(minecraft, testCase.inputs); renderErr == nil ||
				!strings.Contains(renderErr.Error(), testCase.want) {
				t.Fatalf("render error = %v, want it to mention %q", renderErr, testCase.want)
			}
		})
	}
}

// Accepting the EULA is recorded as consent to one specific linked document.
func TestMinecraftRecordsTheExactAgreementThatWasAccepted(t *testing.T) {
	t.Parallel()
	minecraft, err := Get("minecraft-java")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Render(minecraft, map[string]string{"eula": "true"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Acceptances) != 1 || plan.Acceptances[0].Input != "eula" ||
		plan.Acceptances[0].URL != "https://aka.ms/MinecraftEULA" {
		t.Fatalf("acceptances = %#v", plan.Acceptances)
	}
	// The stop sequence saves before it stops. A game server killed without a
	// save loses whatever happened since the last autosave.
	if len(plan.StopCommands) < 2 || plan.StopCommands[len(plan.StopCommands)-1] != "stop" {
		t.Fatalf("stop commands = %#v", plan.StopCommands)
	}
	saves := false
	for _, command := range plan.StopCommands {
		saves = saves || strings.HasPrefix(command, "save")
	}
	if !saves {
		t.Fatal("the Minecraft stop sequence does not save first")
	}
}

func TestChoosingServerSoftwareChangesTheRenderedPlan(t *testing.T) {
	t.Parallel()
	minecraft, err := Get("minecraft-java")
	if err != nil {
		t.Fatal(err)
	}
	vanilla, err := Render(minecraft, map[string]string{"eula": "true"})
	if err != nil {
		t.Fatal(err)
	}
	fabric, err := Render(minecraft, map[string]string{"eula": "true", "server-type": "FABRIC", "memory": "6144"})
	if err != nil {
		t.Fatal(err)
	}
	if vanilla.Digest == fabric.Digest {
		t.Fatal("two different server builds rendered the same plan")
	}
	if fabric.MemoryMB != 6144 {
		t.Fatalf("memory = %d, want the chosen 6144", fabric.MemoryMB)
	}
	find := func(plan *Plan, name string) string {
		for _, variable := range plan.Variables {
			if variable.Name == name {
				return variable.Value
			}
		}
		return ""
	}
	if find(vanilla, "TYPE") != "VANILLA" || find(fabric, "TYPE") != "FABRIC" {
		t.Fatalf("server type did not reach the plan: %q / %q", find(vanilla, "TYPE"), find(fabric, "TYPE"))
	}
	if find(fabric, "MEMORY") != "6144M" {
		t.Fatalf("MEMORY = %q, want the chosen memory in the JVM flag", find(fabric, "MEMORY"))
	}
}

func TestGetVersionRefusesAVersionThisDashboardDoesNotShip(t *testing.T) {
	t.Parallel()
	if _, err := GetVersion("minecraft-java", "9.9.9"); err == nil {
		t.Fatal("served a blueprint version that is not shipped")
	}
	if _, err := GetVersion("minecraft-java", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := Get("definitely-not-a-blueprint"); err == nil {
		t.Fatal("served an unknown blueprint")
	}
}
