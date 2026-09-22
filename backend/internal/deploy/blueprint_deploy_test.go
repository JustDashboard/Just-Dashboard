package deploy

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// A blueprint is deployed as an image release with reviewed defaults. The
// journey below is the one the wizard drives: source, detection (which resolves
// the image digest and hands back the rendered configuration), preflight and
// commit, where the declared secrets are generated on this host and the input
// values become plain variables.
func TestBlueprintDraftCommitsGeneratedSecretsAndInputValues(t *testing.T) {
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	fake := &planningDockerFake{image: &dockerx.DistributionImage{
		Digest: "sha256:" + strings.Repeat("7", 64), Platforms: []string{"linux/amd64"},
	}}
	analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(), fake, nil)
	draft, err := fixture.plans.Create(ctx, 41, "admin")
	if err != nil {
		t.Fatal(err)
	}
	draft, err = fixture.plans.Save(ctx, draft.ID, 41, true, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftIntent,
		Intent: &DraftIntentConfig{Name: "shop-db", Profile: ProfileService},
	})
	if err != nil {
		t.Fatal(err)
	}
	source := DraftSourceConfig{
		Kind: SourceBlueprint, Mode: SourceModeBlueprint, BlueprintID: "postgresql", BlueprintVersion: "1.0.0",
		BlueprintInputs: map[string]string{"database": "shop", "username": "shop_rw"},
	}
	draft, err = fixture.plans.Save(ctx, draft.ID, 41, true, DraftSaveRequest{Revision: draft.Revision, Step: DraftSource, Source: &source})
	if err != nil {
		t.Fatal(err)
	}
	detection, err := analyzer.Analyze(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if detection.Source.Kind != SourceBlueprint || detection.Source.Repository != "docker.io/library/postgres:16-alpine" ||
		detection.Source.Ref != "postgresql@1.0.0" || detection.Source.Digest != fake.image.Digest ||
		!strings.HasPrefix(detection.Source.Revision, "sha256:") {
		t.Fatalf("blueprint identity = %#v", detection.Source)
	}
	draft, err = fixture.plans.SaveDetection(ctx, draft.ID, 41, true, draft.Revision, detection)
	if err != nil {
		t.Fatal(err)
	}
	configuration := draft.Data.Configuration
	if configuration == nil || configuration.Build.Method != BuildImage || configuration.Runtime.Image != "postgres:16-alpine" ||
		configuration.Runtime.Strategy != StrategyStopFirst || configuration.Runtime.MemoryMB != 512 || configuration.Runtime.InternalPort != 5432 {
		t.Fatalf("rendered configuration = %#v", configuration)
	}
	if len(configuration.Runtime.Mounts) != 1 || configuration.Runtime.Mounts[0].Target != "/var/lib/postgresql/data" ||
		configuration.Runtime.Mounts[0].Ownership != OwnershipManaged || !strings.HasPrefix(configuration.Runtime.Mounts[0].Source, "shop-db-") {
		t.Fatalf("mounts = %#v", configuration.Runtime.Mounts)
	}
	variables := map[string]PlannedVariable{}
	for _, variable := range configuration.Variables {
		variables[variable.Name] = variable
	}
	if variables["POSTGRES_DB"].Value != "shop" || variables["POSTGRES_USER"].Value != "shop_rw" ||
		variables["POSTGRES_PASSWORD"].Generate != 40 || variables["POSTGRES_PASSWORD"].Sensitivity != "secret" ||
		variables["POSTGRES_PASSWORD"].Value != "" {
		t.Fatalf("variables = %#v", configuration.Variables)
	}
	if len(configuration.Checks) != 1 || configuration.Checks[0].Kind != "command" || !configuration.Checks[0].Required {
		t.Fatalf("checks = %#v", configuration.Checks)
	}
	// Reinspection renders the reviewed defaults again, but must keep the
	// metadata for sealed user inputs that the next commit will transfer.
	staged := "EXTRA=staged-secret-survives-blueprint-redetection"
	draft, err = fixture.plans.Save(ctx, draft.ID, 41, true, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftConfiguration, Configuration: configuration, Dotenv: &staged,
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err = fixture.plans.SaveDetection(ctx, draft.ID, 41, true, draft.Revision, detection)
	if err != nil {
		t.Fatal(err)
	}

	preflight, err := PreflightDraft(ctx, draft, &preflightObserverFake{observation: HostObservation{
		Facilities: map[string]FacilityObservation{"docker": {Available: true}}, Paths: []PathObservation{}, Ports: []PortObservation{},
	}}, true)
	if err != nil {
		t.Fatal(err)
	}
	acknowledged := []string{}
	for _, finding := range preflight.Findings {
		switch finding.Severity {
		case PreflightBlocked, PreflightDecision:
			t.Fatalf("blueprint preflight blocked: %+v", finding)
		case PreflightWarning:
			acknowledged = append(acknowledged, finding.Code)
		}
	}
	draft, err = fixture.plans.SavePreflight(ctx, draft.ID, 41, true, draft.Revision, preflight)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.plans.Commit(ctx, draft.ID, 41, true, DraftCommitRequest{Revision: draft.Revision, AcknowledgedWarnings: acknowledged})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created {
		t.Fatalf("commit = %#v", result)
	}
	rows, err := fixture.store.DB.Query(`SELECT key, sensitivity, value_enc FROM deploy_variable_revisions WHERE environment_id=? AND active=1`, result.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	opened := map[string]string{}
	sensitivity := map[string]string{}
	for rows.Next() {
		var key, kind, sealed string
		if err := rows.Scan(&key, &kind, &sealed); err != nil {
			t.Fatal(err)
		}
		value, err := fixture.sealer.Open(sealed)
		if err != nil {
			t.Fatal(err)
		}
		opened[key], sensitivity[key] = value, kind
	}
	if opened["POSTGRES_DB"] != "shop" || opened["POSTGRES_USER"] != "shop_rw" {
		t.Fatalf("input values were not stored: %#v", opened)
	}
	if opened["EXTRA"] != "staged-secret-survives-blueprint-redetection" || sensitivity["EXTRA"] != "secret" {
		t.Fatal("blueprint redetection dropped a sealed input before commit")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9]{40}$`).MatchString(opened["POSTGRES_PASSWORD"]) || sensitivity["POSTGRES_PASSWORD"] != "secret" {
		t.Fatalf("generated password = %q (%s)", opened["POSTGRES_PASSWORD"], sensitivity["POSTGRES_PASSWORD"])
	}
	var kind, identity string
	if err := fixture.store.DB.QueryRow(`SELECT kind, identity_json FROM deploy_sources WHERE environment_id=?`, result.EnvironmentID).Scan(&kind, &identity); err != nil {
		t.Fatal(err)
	}
	if kind != string(SourceBlueprint) || !strings.Contains(identity, fake.image.Digest) {
		t.Fatalf("stored source = %s %s", kind, identity)
	}

	// The queue admits a supported blueprint like any image deployment.
	runs := NewOrchestrationStore(fixture.store)
	if _, _, err := runs.Enqueue(ctx, RunRequest{
		ProjectID: result.ProjectID, EnvironmentID: result.EnvironmentID, Operation: OperationDeploy, Trigger: TriggerManual,
		Actor: "admin", RequestDigest: "blueprint-deploy", PlanRevision: 1, SlotClass: SlotHeavy, Steps: DefaultStepKeys,
	}); err != nil {
		t.Fatalf("supported blueprint refused by the queue: %v", err)
	}
}

// Blueprints the release path cannot run are refused at the first step with
// the catalogue's reason, before a draft holds them as a source.
func TestUnsupportedBlueprintsAreRefusedBeforeAnyResourceExists(t *testing.T) {
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	draft, err := fixture.plans.Create(ctx, 41, "admin")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"minecraft-java", "minecraft-bedrock"} {
		inputs := map[string]string{"eula": "true"}
		_, err := fixture.plans.Save(ctx, draft.ID, 41, true, DraftSaveRequest{
			Revision: draft.Revision, Step: DraftSource,
			Source: &DraftSourceConfig{Kind: SourceBlueprint, Mode: SourceModeBlueprint, BlueprintID: id, BlueprintVersion: "1.0.0", BlueprintInputs: inputs},
		})
		if !errors.Is(err, ErrUnsupportedSource) {
			t.Fatalf("%s: err = %v, want unsupported source", id, err)
		}
	}
	// A preview-only blueprint still renders for the catalogue, without a
	// registry lookup and therefore without a deployable digest.
	analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(), &planningDockerFake{}, nil)
	detection, err := analyzer.Analyze(ctx, DraftSourceConfig{Kind: SourceBlueprint, Mode: SourceModeBlueprint,
		BlueprintID: "minecraft-java", BlueprintVersion: "1.0.0", BlueprintInputs: map[string]string{"eula": "true"}})
	if err != nil || detection.Source.Digest != "" {
		t.Fatalf("preview render = %#v, %v", detection.Source, err)
	}
}

// A plan may carry a plain literal value or ask for a generated secret, but a
// secret literal never enters a plan.
func TestPlannedVariableValuesAndGenerationAreBounded(t *testing.T) {
	base := PlanConfiguration{Build: BuildPlanConfig{Method: BuildNone}, Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst}}
	cases := []struct {
		name     string
		variable PlannedVariable
		wantErr  bool
	}{
		{"plain value", PlannedVariable{Name: "POSTGRES_DB", Sensitivity: "plain", Scopes: []string{"runtime"}, Value: "shop"}, false},
		{"secret literal", PlannedVariable{Name: "TOKEN", Sensitivity: "secret", Scopes: []string{"runtime"}, Value: "hunter2"}, true},
		{"value and reference", PlannedVariable{Name: "X", Sensitivity: "plain", Scopes: []string{"runtime"}, Value: "a", Reference: "${{credential.one}}"}, true},
		{"reference-looking value", PlannedVariable{Name: "X", Sensitivity: "plain", Scopes: []string{"runtime"}, Value: "${{credential.one}}"}, true},
		{"generated secret", PlannedVariable{Name: "PASSWORD", Sensitivity: "secret", Scopes: []string{"runtime"}, Generate: 40}, false},
		{"generated plain", PlannedVariable{Name: "PASSWORD", Sensitivity: "plain", Scopes: []string{"runtime"}, Generate: 40}, true},
		{"generated too short", PlannedVariable{Name: "PASSWORD", Sensitivity: "secret", Scopes: []string{"runtime"}, Generate: 8}, true},
		{"generated and referenced", PlannedVariable{Name: "PASSWORD", Sensitivity: "secret", Scopes: []string{"runtime"}, Generate: 40, Reference: "${{credential.one}}"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configuration := base
			configuration.Variables = []PlannedVariable{tc.variable}
			err := canonicalConfiguration(configuration).Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
	secret, err := generatedSecret(40)
	if err != nil || !regexp.MustCompile(`^[A-Za-z0-9]{40}$`).MatchString(secret) {
		t.Fatalf("generatedSecret = %q, %v", secret, err)
	}
}
