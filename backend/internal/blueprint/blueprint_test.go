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
	entries, err := All()
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
	entries, err := All()
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
	entries, err := All()
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
	entries, err := All()
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
	entries, err := All()
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
	entries, err := All()
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

// Every offered template answers the question the operator asks the moment it
// is running: how do I get in? The catalogue used to answer it in prose, or
// not at all — File Browser generated a random admin password, printed it into
// its own log and left the operator at a login form.
func TestEveryBuiltInSaysHowTheFirstSignInWorks(t *testing.T) {
	t.Parallel()
	entries, err := All()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		access := entry.Access
		if access.Kind == "" || strings.TrimSpace(access.Note) == "" {
			t.Fatalf("%s does not say how its first sign-in works", entry.ID)
		}
		// A credential the dashboard cannot show is a credential the operator
		// has to go digging for, which is the defect this field exists to end.
		if access.SecretVariable != "" {
			generated := false
			for _, secret := range entry.Secrets {
				if secret.Variable == access.SecretVariable {
					generated = true
				}
			}
			if !generated {
				t.Fatalf("%s signs in with %s, which it does not generate", entry.ID, access.SecretVariable)
			}
		}
		if access.Kind == AccessUnavailable && entry.Retired == "" {
			t.Fatalf("%s is offered even though its first credential cannot be handed over", entry.ID)
		}
		switch access.Kind {
		case AccessCredentials:
			if access.SecretVariable == "" || (access.Username == "" && access.UsernameVariable == "") {
				t.Fatalf("%s claims a credentials sign-in without naming both halves of it", entry.ID)
			}
		case AccessToken:
			if access.SecretVariable == "" {
				t.Fatalf("%s claims a token sign-in without naming the token", entry.ID)
			}
		}
	}
}

// A retired definition stays resolvable. A deployment already running one
// re-resolves its definition on every redeploy, so deleting the file would
// turn a working service into an unredeployable one.
func TestRetiredBuiltInsAreNotOfferedButStayResolvable(t *testing.T) {
	t.Parallel()
	offered, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range offered {
		if entry.Retired != "" {
			t.Fatalf("retired blueprint %q is still offered in the catalogue", entry.ID)
		}
	}
	retired := 0
	for _, entry := range all {
		if entry.Retired == "" {
			continue
		}
		retired++
		found, getErr := Get(entry.ID)
		if getErr != nil || found.ID != entry.ID {
			t.Fatalf("retired blueprint %q is no longer resolvable: %v", entry.ID, getErr)
		}
		if _, versionErr := GetVersion(entry.ID, entry.Version); versionErr != nil {
			t.Fatalf("retired blueprint %q cannot be redeployed at its own version: %v", entry.ID, versionErr)
		}
	}
	if len(all)-retired != len(offered) {
		t.Fatalf("catalogue offers %d of %d shipped definitions but %d are retired", len(offered), len(all), retired)
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
		Access:  Access{Kind: AccessOpen, Note: "Anyone who reaches the probe can use it."},
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

func TestExternalSecretInputsCannotBecomeRenderedValuesOrOperations(t *testing.T) {
	t.Parallel()
	definition, err := Get("mongo-express")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Render(definition, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, variable := range plan.Variables {
		if variable.Name == "ME_CONFIG_MONGODB_URL" {
			found = variable.Sensitivity == "secret" && variable.Required && !variable.Generated && variable.Value == ""
		}
	}
	if !found {
		t.Fatal("required external secret was not represented in the plan")
	}
	if _, err := Render(definition, map[string]string{"mongodb-url": "mongodb://user:private@host/db"}); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatalf("secret input was accepted or echoed: %v", err)
	}
	copy := *definition
	copy.Operations = definition.Operations
	copy.Operations.Startup = append([]Operation(nil), definition.Operations.Startup...)
	copy.Operations.Startup = append(copy.Operations.Startup, Operation{Kind: OperationSetVariable, Name: "CONNECTION", Value: "{{input.mongodb-url}}"})
	if err := Validate(&copy); err == nil || !strings.Contains(err.Error(), "templates secret input") {
		t.Fatalf("secret operation interpolation accepted: %v", err)
	}
}

func TestBlueprintDomainsHaveDNSLengthLimits(t *testing.T) {
	t.Parallel()
	definition, err := Get("n8n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Render(definition, map[string]string{"domain": strings.Repeat("a.", 126) + "aa"}); err == nil {
		t.Fatal("a 254-byte hostname was accepted")
	}
}
