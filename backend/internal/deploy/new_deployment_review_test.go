package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/blueprint"
)

func TestEveryDeployableBlueprintPassesConfigurationAndRuntimePreflight(t *testing.T) {
	t.Parallel()
	definitions, err := blueprint.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range definitions {
		if supported, _ := blueprint.DeploymentSupport(definition); !supported {
			continue
		}
		t.Run(definition.ID, func(t *testing.T) {
			plan, err := RenderBlueprintPlan(DraftSourceConfig{
				Kind: SourceBlueprint, Mode: SourceModeBlueprint, BlueprintID: definition.ID,
				BlueprintVersion: definition.Version, BlueprintInputs: definition.Fixtures[0].Inputs,
			}, definition.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := plan.Configuration.Validate(); err != nil {
				t.Fatalf("template cannot become a saved configuration: %v", err)
			}
			// Host availability and required operator values have their own
			// checks; a catalogue default must never conflict with its profile.
			draft := &Draft{Data: DraftData{
				Intent: &DraftIntentConfig{Name: definition.ID, Profile: plan.Detection.Candidates[0].Profile},
				Source: &DraftSourceConfig{Kind: SourceBlueprint, Mode: SourceModeBlueprint,
					BlueprintID: definition.ID, BlueprintVersion: definition.Version},
				Detection: &plan.Detection, Configuration: &plan.Configuration,
			}}
			for _, finding := range preflightFindings(draft, plan.Configuration, HostObservation{}, true) {
				if finding.Code == "strategy_ineligible" || finding.Code == "readiness_missing" {
					t.Fatalf("template defaults block deployment: %+v", finding)
				}
			}
			for _, declared := range definition.Secrets {
				found := false
				for _, variable := range plan.Configuration.Variables {
					if variable.Name == declared.Variable {
						found = variable.Sensitivity == "secret" && variable.Generate == declared.Length && variable.Value == "" && variable.Required
					}
				}
				if !found {
					t.Fatalf("generated credential %s was lost in normalization", declared.Variable)
				}
			}
		})
	}
}

func TestRetiredBlueprintsCannotStartNewDraftsButRemainRedeployable(t *testing.T) {
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	draft, err := fixture.plans.Create(ctx, 41, "admin")
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := blueprint.All()
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range definitions {
		if definition.Retired == "" {
			continue
		}
		source := DraftSourceConfig{Kind: SourceBlueprint, Mode: SourceModeBlueprint,
			BlueprintID: definition.ID, BlueprintVersion: definition.Version, BlueprintInputs: definition.Fixtures[0].Inputs}
		if err := source.ValidateForDeployment(); err != nil {
			t.Fatalf("existing %s deployment cannot redeploy: %v", definition.ID, err)
		}
		if _, err := fixture.plans.Save(ctx, draft.ID, 41, true, DraftSaveRequest{
			Revision: draft.Revision, Step: DraftSource, Source: &source,
		}); !errors.Is(err, ErrUnsupportedSource) || !strings.Contains(err.Error(), definition.Retired) {
			t.Fatalf("retired %s accepted as a new source: %v", definition.ID, err)
		}
		plan, err := RenderBlueprintPlan(source, definition.ID)
		if err != nil {
			t.Fatal(err)
		}
		stale := &Draft{CurrentStep: DraftPreflight, PlanPreview: "saved before retirement", Data: DraftData{
			Intent: &DraftIntentConfig{Name: definition.ID, Profile: plan.Detection.Candidates[0].Profile},
			Source: &source, Detection: &plan.Detection, Configuration: &plan.Configuration,
		}}
		if _, err := PreflightDraft(ctx, stale, nil, true); !errors.Is(err, ErrUnsupportedSource) {
			t.Fatalf("retired %s passed new-draft preflight: %v", definition.ID, err)
		}
		if err := validateDraftComplete(stale); !errors.Is(err, ErrUnsupportedSource) {
			t.Fatalf("retired %s passed stale draft commit: %v", definition.ID, err)
		}
	}
}

func TestMongoExpressRequiresAnEncryptedConnectionInsteadOfAPlainBlueprintInput(t *testing.T) {
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	draft, err := fixture.plans.Create(ctx, 41, "admin")
	if err != nil {
		t.Fatal(err)
	}
	source := DraftSourceConfig{Kind: SourceBlueprint, Mode: SourceModeBlueprint,
		BlueprintID: "mongo-express", BlueprintVersion: "1.0.0"}
	unsafe := source
	unsafe.BlueprintInputs = map[string]string{"mongodb-url": "mongodb://root:private-password@db:27017/app"}
	if _, err := fixture.plans.Save(ctx, draft.ID, 41, true, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftSource, Source: &unsafe,
	}); !errors.Is(err, ErrInvalidSource) || strings.Contains(err.Error(), "private-password") {
		t.Fatalf("plaintext connection was accepted or leaked: %v", err)
	}
	if _, err := RenderBlueprintPlan(unsafe, "mongo-admin"); err == nil || strings.Contains(err.Error(), "private-password") {
		t.Fatalf("plaintext connection was rendered or leaked: %v", err)
	}
	plan, err := RenderBlueprintPlan(source, "mongo-admin")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Detection.Candidates[0].Databases) != 1 || plan.Detection.Candidates[0].Databases[0].Variable != "ME_CONFIG_MONGODB_URL" {
		t.Fatal("MongoDB connection suggestion missing")
	}
	probe := &Draft{Data: DraftData{
		Intent: &DraftIntentConfig{Name: "mongo-admin", Profile: ProfileWeb},
		Source: &source, Detection: &plan.Detection, Configuration: &plan.Configuration,
	}}
	missing := func() bool {
		for _, finding := range preflightFindings(probe, plan.Configuration, HostObservation{}, true) {
			if finding.Code == "variable_required_me_config_mongodb_url" {
				return true
			}
		}
		return false
	}
	if !missing() {
		t.Fatal("unset MongoDB connection passed preflight")
	}
	for index := range plan.Configuration.Variables {
		variable := &plan.Configuration.Variables[index]
		if variable.Name == "ME_CONFIG_MONGODB_URL" {
			if variable.Sensitivity != "secret" || !variable.Required || variable.Value != "" || variable.Generate != 0 {
				t.Fatalf("external credential contract = %+v", variable)
			}
			variable.Reference = "${{database.7}}"
		}
	}
	if missing() {
		t.Fatal("typed database reference did not satisfy required connection")
	}
	encoded, _ := json.Marshal(plan)
	if strings.Contains(string(encoded), "private-password") {
		t.Fatal("rendered plan contains the rejected secret")
	}
}

func TestDenoJSONCPreservesTaskStringsAndInlineComments(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "deno.jsonc", `{
  "tasks": {
    "build": "deno task generate", // build output
    "start": {"command": "deno run -A server.ts --url https://example.test/a/*/b --literal ',}' --quote \"ok\"",},
  },
  /* comments can appear between tokens */
  "imports": {"fresh": "jsr:@fresh/core@^2",},
}`)
	content, err := readContainedRegular(root, "deno.jsonc", 4096)
	if err != nil {
		t.Fatal(err)
	}
	config := parseDenoConfig(content)
	if got := config.task("start"); got != `deno run -A server.ts --url https://example.test/a/*/b --literal ',}' --quote "ok"` {
		t.Fatalf("task string was rewritten: %q", got)
	}
	result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
	if err != nil || len(result.Candidates) != 1 {
		t.Fatalf("detect: %+v, %v", result, err)
	}
	candidate := result.Candidates[0]
	if candidate.Framework != "fresh" || candidate.StartCommand != "deno task start" || candidate.BuildCommand != "deno task build" {
		t.Fatalf("valid JSONC was not detected: %+v", candidate)
	}
	for _, malformed := range []string{`{"tasks":{"start":"deno task unsafe"}, "imports": 7}`, `{"tasks":{"start":"deno task unsafe"}} /* unfinished`} {
		if config := parseDenoConfig([]byte(malformed)); config.task("start") != "" {
			t.Fatal("malformed configuration yielded a partial task")
		}
	}
}

func TestBlueprintDomainDefaultsRetainTheirBindingAfterDetection(t *testing.T) {
	t.Parallel()
	definitions, err := blueprint.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range definitions {
		inputs := map[string]string{}
		for key, value := range definition.Fixtures[0].Inputs {
			inputs[key] = value
		}
		hasDomain := false
		for _, input := range definition.Inputs {
			if input.Kind == blueprint.InputDomain {
				inputs[input.Name] = "APP.Example.TEST"
				hasDomain = true
				break
			}
		}
		if !hasDomain {
			continue
		}
		t.Run(definition.ID, func(t *testing.T) {
			plan, err := RenderBlueprintPlan(DraftSourceConfig{Kind: SourceBlueprint, Mode: SourceModeBlueprint,
				BlueprintID: definition.ID, BlueprintVersion: definition.Version, BlueprintInputs: inputs}, definition.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Configuration.Domains) == 0 || plan.Configuration.Domains[0].Hostname != "app.example.test" {
				t.Fatal("hostname was not canonicalized")
			}
			for _, variable := range plan.Configuration.Variables {
				if !strings.Contains(variable.Value, "app.example.test") {
					continue
				}
				if variable.DomainTemplate == "" || !variable.Required {
					t.Fatalf("%s lost its required primary-address binding: %+v", variable.Name, variable)
				}
				value := strings.NewReplacer("{{hostname}}", "app.example.test", "{{scheme}}", "https").Replace(variable.DomainTemplate)
				if variable.Value != value {
					t.Fatalf("%s binding expands to %q, actual %q", variable.Name, value, variable.Value)
				}
			}
		})
	}
}

func TestLegacyMongoExpressSourceKeepsRuntimeCompatibilityWithoutCopyingPlainInputs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	source := DraftSourceConfig{Kind: SourceBlueprint, Mode: SourceModeBlueprint,
		BlueprintID: "mongo-express", BlueprintVersion: "1.0.0",
		BlueprintInputs: map[string]string{"mongodb-url": "mongodb://root:legacy-private@db:27017/app", "admin-user": "operator"}}
	if err := source.ValidateForDeployment(); err != nil {
		t.Fatalf("previously committed source cannot run: %v", err)
	}
	if err := source.validateForNewDeployment(); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("legacy input can enter a new draft: %v", err)
	}
	if err := source.validateBlueprintSecretInputs(); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("legacy input can enter a new source revision: %v", err)
	}
	sanitized, err := source.withoutBlueprintSecretInputs()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := RenderBlueprintPlan(sanitized, "mongo-admin")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(plan)
	if strings.Contains(string(encoded), "legacy-private") || strings.Contains(string(encoded), "mongodb://") {
		t.Fatal("legacy input was copied to the rendered configuration")
	}
	analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(), nil, nil)
	materialized, err := analyzer.Materialize(ctx, source, SourceIdentity{Kind: SourceBlueprint,
		Repository: "mongo-express:1.0.2-20-alpine3.19", Digest: testImageDigest}, 1, t.TempDir())
	if err != nil {
		t.Fatalf("previously committed source cannot materialize: %v", err)
	}
	marker, err := readMaterializedMarker(filepath.Join(materialized.Workspace, "source.json"))
	if err != nil {
		t.Fatal(err)
	}
	if marker.Source.BlueprintInputs["mongodb-url"] != "" || marker.Source.BlueprintInputs["admin-user"] != "operator" {
		t.Fatalf("source marker did not keep only non-secret inputs: %#v", marker.Source.BlueprintInputs)
	}
	if source.BlueprintInputs["mongodb-url"] != "mongodb://root:legacy-private@db:27017/app" {
		t.Fatal("execution mutated the stored source instead of a local copy")
	}
}
