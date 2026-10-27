package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/blueprint"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

func composeTemplateSource(t *testing.T, id string, inputs map[string]string) DraftSourceConfig {
	t.Helper()
	definition, err := blueprint.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return DraftSourceConfig{
		Kind: SourceCompose, Mode: SourceModeBlueprint,
		BlueprintID: definition.ID, BlueprintVersion: definition.Version, BlueprintInputs: inputs,
	}
}

// Every multi-service template is planned the way a pasted Compose file is:
// Compose evidence, a Compose build, the public service as the release's
// primary service and route target. What it adds is what the definition
// knows — every variable the document interpolates is planned, generated
// secrets by shape only — so preflight has nothing left to ask the operator.
func TestEveryComposeTemplatePlansAsAComposeSourceWithNothingLeftToAsk(t *testing.T) {
	t.Parallel()
	definitions, err := blueprint.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, definition := range definitions {
		if !definition.IsCompose() {
			continue
		}
		checked++
		t.Run(definition.ID, func(t *testing.T) {
			t.Parallel()
			source, err := composeTemplateSource(t, definition.ID, definition.Fixtures[0].Inputs).WithTemplateDocuments()
			if err != nil {
				t.Fatal(err)
			}
			if err := source.validateForNewDeployment(); err != nil {
				t.Fatalf("a fresh template source is refused: %v", err)
			}
			plan, err := RenderBlueprintPlan(source, definition.ID)
			if err != nil {
				t.Fatal(err)
			}
			detection, configuration, public := plan.Detection, plan.Configuration, definition.PublicService()
			identity := detection.Source
			if identity.Kind != SourceCompose || identity.Template != definition.ID+"@"+definition.Version ||
				identity.Revision != plan.Rendered.Digest || detection.Compose == nil || identity.Digest != detection.Compose.Digest {
				t.Fatalf("identity = %#v", identity)
			}
			if err := validateDetectionResult(&source, detection); err != nil {
				t.Fatalf("the template's own detection is refused: %v", err)
			}
			if len(detection.Compose.Unsupported) > 0 || len(detection.Compose.Warnings) > 0 {
				t.Fatalf("the planner flags the reviewed stack: %q %q", detection.Compose.Unsupported, detection.Compose.Warnings)
			}
			if detection.Compose.PrimaryService != public.Name || configuration.Build.PrimaryService != public.Name ||
				configuration.Build.Method != BuildCompose || configuration.Runtime.Strategy != StrategyStopFirst {
				t.Fatalf("build %#v runtime strategy %s", configuration.Build, configuration.Runtime.Strategy)
			}
			for _, port := range public.Ports {
				if port.Exposure == "proxy" && configuration.Runtime.InternalPort != port.Internal {
					t.Fatalf("the route follows port %d, the public service serves %d", configuration.Runtime.InternalPort, port.Internal)
				}
			}
			if err := configuration.Validate(); err != nil {
				t.Fatalf("the template cannot become a saved configuration: %v", err)
			}
			planned := map[string]PlannedVariable{}
			for _, variable := range configuration.Variables {
				planned[variable.Name] = variable
			}
			for _, variable := range detection.Compose.Variables {
				if _, found := planned[variable]; !found {
					t.Fatalf("the document interpolates %s, which the plan does not carry", variable)
				}
			}
			for _, secret := range definition.Secrets {
				variable := planned[secret.Variable]
				if variable.Sensitivity != "secret" || variable.Generate != secret.Length || variable.Value != "" || !variable.Required {
					t.Fatalf("generated secret %s planned as %#v", secret.Variable, variable)
				}
				if secret.Alphabet == "hex" && variable.GenerateFormat != "hex" {
					t.Fatalf("%s lost its hexadecimal shape", secret.Variable)
				}
			}
			draft := &Draft{Data: DraftData{
				Intent: &DraftIntentConfig{Name: definition.ID, Profile: ProfileCompose},
				Source: &source, Detection: &detection, Configuration: &configuration,
			}}
			observation := HostObservation{Facilities: map[string]FacilityObservation{
				"docker": {Available: true}, "compose": {Available: true}, "buildx": {Available: true}, "git": {Available: true},
			}}
			for _, finding := range preflightFindings(draft, configuration, observation, true) {
				if finding.Severity == PreflightBlocked || finding.Severity == PreflightDecision {
					t.Fatalf("the reviewed defaults stop at preflight: %+v", finding)
				}
			}
		})
	}
	if checked == 0 {
		t.Fatal("the catalogue ships no multi-service template")
	}
}

// The wizard's journey for a stack: the source is saved as the server's own
// render whatever the browser sent, detection hands back the planned stack,
// preflight passes, and commit generates the shared secrets on this host and
// seals them — the same value for every service that references it, revealed
// only through the Variables tab, never in the plan, the source or the
// document.
func TestComposeTemplateDraftCommitsOneSealedSecretSharedByItsServices(t *testing.T) {
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	fake := &planningDockerFake{composeAvailable: true, composeValid: true, image: &dockerx.DistributionImage{
		Digest: "sha256:" + strings.Repeat("7", 64), Platforms: []string{"linux/amd64"},
	}}
	analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(), fake, nil)
	draft, err := fixture.plans.Create(ctx, 41, "admin")
	if err != nil {
		t.Fatal(err)
	}
	draft, err = fixture.plans.Save(ctx, draft.ID, 41, true, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftIntent, Intent: &DraftIntentConfig{Name: "blog", Profile: ProfileCompose},
	})
	if err != nil {
		t.Fatal(err)
	}
	// A browser that sends a document of its own under the template's name
	// gets the reviewed render stored instead.
	sent := composeTemplateSource(t, "wordpress", nil)
	sent.ComposeFiles = []ComposeDocument{{Path: "compose.yaml", Content: "services:\n  evil:\n    image: alpine:3.20\n    privileged: true\n"}}
	draft, err = fixture.plans.Save(ctx, draft.ID, 41, true, DraftSaveRequest{Revision: draft.Revision, Step: DraftSource, Source: &sent})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := composeTemplateSource(t, "wordpress", nil).WithTemplateDocuments()
	if err != nil {
		t.Fatal(err)
	}
	stored := *draft.Data.Source
	if !slices.Equal(stored.ComposeFiles, rendered.ComposeFiles) || strings.Contains(stored.ComposeFiles[0].Content, "evil") {
		t.Fatalf("stored document = %+v", stored.ComposeFiles)
	}
	detection, err := analyzer.Analyze(ctx, stored)
	if err != nil {
		t.Fatal(err)
	}
	if fake.validateFilesCalls == 0 || len(fake.validatedInputs) != 1 || fake.validatedInputs[0].Content != rendered.ComposeFiles[0].Content {
		t.Fatal("Docker Compose did not validate the reviewed render")
	}
	draft, err = fixture.plans.SaveDetection(ctx, draft.ID, 41, true, draft.Revision, detection)
	if err != nil {
		t.Fatal(err)
	}
	configuration := draft.Data.Configuration
	if configuration == nil || configuration.Build.Method != BuildCompose || configuration.Build.PrimaryService != "wordpress" ||
		configuration.Runtime.InternalPort != 80 {
		t.Fatalf("configuration = %#v", configuration)
	}
	preflight, err := PreflightDraft(ctx, draft, &preflightObserverFake{observation: HostObservation{
		Facilities: map[string]FacilityObservation{"docker": {Available: true}, "compose": {Available: true}},
		Paths:      []PathObservation{}, Ports: []PortObservation{},
	}}, true)
	if err != nil {
		t.Fatal(err)
	}
	acknowledged := []string{}
	for _, finding := range preflight.Findings {
		switch finding.Severity {
		case PreflightBlocked, PreflightDecision:
			t.Fatalf("template preflight blocked: %+v", finding)
		case PreflightWarning:
			acknowledged = append(acknowledged, finding.Code)
		}
	}
	if !strings.Contains(preflight.Preview, "WORDPRESS_DB_PASSWORD") {
		t.Fatal("the plan preview does not name the shared password")
	}
	draft, err = fixture.plans.SavePreflight(ctx, draft.ID, 41, true, draft.Revision, preflight)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.plans.Commit(ctx, draft.ID, 41, true, DraftCommitRequest{Revision: draft.Revision, AcknowledgedWarnings: acknowledged})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := fixture.store.DB.Query(`SELECT key, sensitivity, value_enc FROM deploy_variable_revisions WHERE environment_id=? AND active=1`, result.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	opened := map[string]string{}
	for rows.Next() {
		var key, kind, sealed string
		if err := rows.Scan(&key, &kind, &sealed); err != nil {
			t.Fatal(err)
		}
		if kind != "secret" {
			t.Fatalf("%s is stored %s", key, kind)
		}
		if opened[key], err = fixture.sealer.Open(sealed); err != nil {
			t.Fatal(err)
		}
	}
	password := opened["WORDPRESS_DB_PASSWORD"]
	if !regexp.MustCompile(`^[A-Za-z0-9]{40}$`).MatchString(password) || opened["MARIADB_ROOT_PASSWORD"] == "" || opened["MARIADB_ROOT_PASSWORD"] == password {
		t.Fatal("the stack's secrets were not each generated once")
	}
	var kind, config, identity, preview string
	if err := fixture.store.DB.QueryRow(`SELECT kind, config_json, identity_json FROM deploy_sources WHERE environment_id=?`, result.EnvironmentID).Scan(&kind, &config, &identity); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.DB.QueryRow(`SELECT preview FROM deploy_runtime_plans WHERE environment_id=?`, result.EnvironmentID).Scan(&preview); err != nil {
		t.Fatal(err)
	}
	if kind != string(SourceCompose) || !strings.Contains(identity, `"template":"wordpress@1.0.0"`) {
		t.Fatalf("stored source = %s %s", kind, identity)
	}
	for _, text := range []string{config, identity, preview, draft.PlanPreview} {
		if strings.Contains(text, password) || strings.Contains(text, opened["MARIADB_ROOT_PASSWORD"]) {
			t.Fatal("a generated secret reached a stored plan or source")
		}
	}
	// Both services interpolate the one sealed password.
	var source DraftSourceConfig
	if err := json.Unmarshal([]byte(config), &source); err != nil {
		t.Fatal(err)
	}
	if strings.Count(source.ComposeFiles[0].Content, "${WORDPRESS_DB_PASSWORD}") != 2 {
		t.Fatalf("the shared password is not referenced by both services:\n%s", source.ComposeFiles[0].Content)
	}
	runs := NewOrchestrationStore(fixture.store)
	if _, _, err := runs.Enqueue(ctx, RunRequest{
		ProjectID: result.ProjectID, EnvironmentID: result.EnvironmentID, Operation: OperationDeploy, Trigger: TriggerManual,
		Actor: "admin", RequestDigest: "template-deploy", PlanRevision: 1, SlotClass: SlotHeavy, Steps: DefaultStepKeys,
	}); err != nil {
		t.Fatalf("a committed template is refused by the queue: %v", err)
	}
}

// A template's release is the document its source stored. A later dashboard
// that no longer ships the version it was rendered from still redeploys it,
// byte for byte; only a new deployment, or a move to another version, needs
// the version to be shipped — and a move re-renders.
func TestComposeTemplateSourceOutlivesItsVersionAndRefusesTampering(t *testing.T) {
	ctx := context.Background()
	current, err := composeTemplateSource(t, "wordpress", nil).WithTemplateDocuments()
	if err != nil {
		t.Fatal(err)
	}
	retired := current
	retired.BlueprintVersion = "0.9.0"
	if err := retired.ValidateForDeployment(); err != nil {
		t.Fatalf("a stored stack from an unshipped version cannot redeploy: %v", err)
	}
	if err := retired.validateForNewDeployment(); !errors.Is(err, ErrUnsupportedSource) {
		t.Fatalf("a new deployment of an unshipped version: %v", err)
	}
	if _, err := retired.WithTemplateDocuments(); err == nil {
		t.Fatal("an unshipped version re-rendered")
	}
	analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(), nil, nil)
	workspace := t.TempDir()
	materialized, err := analyzer.Materialize(ctx, retired, SourceIdentity{Kind: SourceCompose, Digest: "sha256:" + strings.Repeat("a", 64)}, 7, workspace)
	if err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(filepath.Join(materialized.Root, blueprint.ComposeFile))
	if err != nil || string(written) != current.ComposeFiles[0].Content {
		t.Fatalf("materialized document = %q, %v", written, err)
	}

	tampered := current
	tampered.ComposeFiles = []ComposeDocument{{Path: "compose.yaml", Content: strings.Replace(current.ComposeFiles[0].Content, "mariadb:11.8.9", "mariadb:10.6", 1)}}
	if err := tampered.validateForNewDeployment(); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("a document that is not the reviewed render: %v", err)
	}
	if err := (DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeBlueprint, BlueprintID: "wordpress", BlueprintVersion: "1.0.0"}).ValidateForDeployment(); !errors.Is(err, ErrUnsupportedSource) {
		t.Fatalf("an unrendered template source: %v", err)
	}
	// A stack is never planned as one container, nor an image as a stack.
	if _, err := RenderBlueprintPlan(DraftSourceConfig{Kind: SourceBlueprint, Mode: SourceModeBlueprint, BlueprintID: "wordpress", BlueprintVersion: "1.0.0"}, "x"); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("a stack planned as a blueprint source: %v", err)
	}
	if _, err := RenderBlueprintPlan(DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeBlueprint, BlueprintID: "postgresql", BlueprintVersion: "1.0.0"}, "x"); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("an image planned as a Compose source: %v", err)
	}
	if err := (DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeBlueprint, BlueprintID: "postgresql", BlueprintVersion: "1.0.0",
		ComposeFiles: current.ComposeFiles}).ValidateForDeployment(); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("a single-image template saved as a Compose source: %v", err)
	}
	// A detection may name the template only for the source that renders it.
	plan, err := RenderBlueprintPlan(current, "")
	if err != nil {
		t.Fatal(err)
	}
	other := current
	other.BlueprintVersion = "2.0.0"
	if err := validateDetectionResult(&other, plan.Detection); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("a template identity for another version: %v", err)
	}
}
