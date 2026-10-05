package deploy

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"gopkg.in/yaml.v3"
)

func TestRecoveredInputsRetainServiceNamesAndLiteralValuesThroughAdoption(t *testing.T) {
	candidate, reader, paths, root := scopedComposeFixture(t)
	for _, capture := range reader.captures {
		capture.Inspection.Config.Env = []string{"TELEGRAM_CHAT_ID=" + capture.Inspection.ID[:1], "TELEGRAM_BOT_TOKEN=${{credential.unrelated}}", "EMPTY="}
		capture.Inspection.Config.Cmd = []string{"--literal", "$NAME", "${NAME}", "$$"}
	}
	recovered, err := RecoverDockerWorkloadWithScope(t.Context(), candidate, reader, paths, root, RecoveryExistingServices)
	if err != nil {
		t.Fatal(err, recovered.Adoption.Blockers)
	}
	if strings.Contains(recovered.Source.ComposeFiles[0].Content, "credential.unrelated") {
		t.Fatal("captured value leaked into source")
	}
	if !strings.Contains(recovered.Source.ComposeFiles[0].Content, "$$NAME") || !strings.Contains(recovered.Source.ComposeFiles[0].Content, "$${NAME}") {
		t.Fatal("runtime argv literals were not escaped")
	}
	ids := map[string]string{}
	for _, input := range recovered.Adoption.Inputs {
		if input.Kind == "log_option" && input.Service == "web" {
			ids["web-log"] = input.StorageKey
		}
		if input.Name == "TELEGRAM_CHAT_ID" {
			ids[input.Service] = input.StorageKey
			if !input.Retained || input.Sensitivity != "plain" {
				t.Fatal("incorrect display metadata")
			}
		}
		if input.Name == "TELEGRAM_BOT_TOKEN" && input.Sensitivity != "secret" {
			t.Fatal("credential classification lost")
		}
	}
	if ids["web"] == "" || ids["worker"] == "" || ids["web"] == ids["worker"] {
		t.Fatal("service scopes collapsed")
	}
	fixture := newPlanningStoreFixture(t)
	draft, err := fixture.plans.CreateRecoveredDraft(t.Context(), 41, "operator", DraftIntentConfig{Name: "input-proof", Profile: ProfileCompose}, recovered)
	if err != nil {
		t.Fatal(err)
	}
	draft = checkRecoveredDraft(t, fixture, draft)
	var ack []string
	for _, finding := range draft.Findings {
		if finding.Severity == PreflightWarning {
			ack = append(ack, finding.Code)
		}
	}
	adopted, err := fixture.plans.Commit(t.Context(), draft.ID, 41, false, DraftCommitRequest{Revision: draft.Revision, AcknowledgedWarnings: ack})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := fixture.plans.OpenScopedVariables(t.Context(), adopted.EnvironmentID, "runtime")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range opened {
		if value.ValueMode != "literal" {
			t.Fatal("captured value lost literal mode")
		}
	}
	views, err := fixture.plans.ListVariables(t.Context(), adopted.ProjectID, adopted.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range views {
		if view.Reference != nil {
			t.Fatal("captured literal was treated as reference")
		}
	}
	if ids["web-log"] == "" {
		t.Fatal("log option display metadata missing")
	}
	if _, err := fixture.plans.DeleteVariable(t.Context(), adopted.ProjectID, adopted.EnvironmentID, ids["web-log"], 2); !errors.Is(err, ErrInvalidVariable) {
		t.Fatal("bound runtime option deletion accepted", err)
	}
	if _, err := fixture.plans.DeleteVariable(t.Context(), adopted.ProjectID, adopted.EnvironmentID, ids["web"], 2); !errors.Is(err, ErrInvalidVariable) {
		t.Fatal("bound input deletion accepted", err)
	}
	importRequest := DotenvImportRequest{Revision: adopted.PlanRevision, Dotenv: ids["web"] + "=replacement", Sensitivity: "secret", Scopes: []string{"runtime", "build"}}
	if _, err := fixture.plans.PreviewDotenvImport(t.Context(), adopted.ProjectID, adopted.EnvironmentID, importRequest); !errors.Is(err, ErrInvalidVariable) {
		t.Fatal("dotenv preview accepted a bound input scope change", err)
	}
	if _, err := fixture.plans.ImportDotenv(t.Context(), adopted.ProjectID, adopted.EnvironmentID, "operator", importRequest); !errors.Is(err, ErrInvalidVariable) {
		t.Fatal("dotenv import accepted a bound input scope change", err)
	}
	importRequest.Scopes = []string{"runtime"}
	if _, err := fixture.plans.PreviewDotenvImport(t.Context(), adopted.ProjectID, adopted.EnvironmentID, importRequest); err != nil {
		t.Fatal("dotenv preview refused a literal replacement", err)
	}
	empty := ""
	if _, err := fixture.plans.PutVariable(t.Context(), adopted.ProjectID, adopted.EnvironmentID, ids["web"], "operator", VariableWriteRequest{Revision: 2, Value: &empty, Sensitivity: "secret", Scopes: []string{"runtime"}}); err != nil {
		t.Fatal("literal empty replacement rejected", err)
	}
	views, err = fixture.plans.ListVariables(t.Context(), adopted.ProjectID, adopted.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range views {
		if view.Name == ids["web"] && (view.RecoveredInput == nil || !view.RecoveredInput.Empty || !view.RecoveredInput.Retained) {
			t.Fatal("replacement metadata does not describe desired value")
		}
	}
	live, err := NewOrchestrationStore(fixture.store).LiveRelease(t.Context(), adopted.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := fixture.plans.OpenRunScopedVariables(t.Context(), live.Release.RunID, adopted.EnvironmentID, "runtime")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range baseline {
		if value.Name == ids["web"] && value.Value != "a" {
			t.Fatal("desired edit changed frozen baseline")
		}
	}
	desired, err := fixture.plans.EnvironmentConfiguration(t.Context(), adopted.ProjectID, adopted.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	source := *desired.Source
	source.ComposeFiles = append([]ComposeDocument{}, source.ComposeFiles...)
	source.ComposeFiles[0].Content = strings.ReplaceAll(source.ComposeFiles[0].Content, "${"+ids["web"]+"}", "replacement-without-a-binding")
	analysis, err := analyzeComposeDocuments(source.ComposeFiles)
	if err != nil {
		t.Fatal(err)
	}
	identity := recovered.Detection.Source
	identity.Digest = analysis.Digest
	desired, err = fixture.plans.SaveEnvironmentSource(t.Context(), adopted.ProjectID, adopted.EnvironmentID, desired.Revision, source, identity)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range desired.Variables {
		if view.Name == ids["web"] && (view.RecoveredInput == nil || view.RecoveredInput.Bound || view.RecoveredInput.Name != "TELEGRAM_CHAT_ID") {
			t.Fatal("removed source binding hid metadata or still protected its declaration")
		}
	}
	if _, err := fixture.plans.DeleteVariable(t.Context(), adopted.ProjectID, adopted.EnvironmentID, ids["web"], desired.Revision); err != nil {
		t.Fatal("unused captured input deletion refused", err)
	}
}

func TestLiteralVariableModeAndLegacyGraphRemainDistinct(t *testing.T) {
	values := map[string]string{"A": "${{variable.MISSING}}"}
	if _, _, err := ResolveVariableGraph(values, nil); !errors.Is(err, ErrInvalidVariable) {
		t.Fatal("legacy inference changed", err)
	}
	resolved, _, err := ResolveVariableGraphWithModes(values, map[string]string{"A": "literal"}, nil)
	if err != nil || resolved["A"] != values["A"] {
		t.Fatal("literal bytes interpreted", err)
	}
	if containsSourceInterpolation(map[string]any{"literal": "$${HOME}"}) || !containsSourceInterpolation(map[string]any{"expression": "${HOME}"}) {
		t.Fatal("literal and unresolved expressions were conflated")
	}
}

func TestVariableWritesPreserveLiteralAndReferenceIntentAcrossRuns(t *testing.T) {
	fixture := newPlanningStoreFixture(t)
	projectID, environmentID := insertConfigurationFixtureWithEvidence(t, fixture, `{}`)
	literal := "${{variable.MISSING}}"
	result, err := fixture.plans.PutVariable(t.Context(), projectID, environmentID, "LITERAL", "operator", VariableWriteRequest{Revision: 1, Value: &literal, Sensitivity: "secret", Scopes: []string{"runtime"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err = fixture.plans.PutVariable(t.Context(), projectID, environmentID, "ALIAS", "operator", VariableWriteRequest{Revision: result.DesiredRevision, Reference: "${{variable.LITERAL}}", Sensitivity: "secret", Scopes: []string{"runtime"}})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := fixture.plans.OpenScopedVariables(t.Context(), environmentID, "runtime")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range opened {
		if value.Value != literal {
			t.Fatal("literal/reference resolution changed bytes")
		}
	}
	if result.Variable.Reference == nil {
		t.Fatal("explicit reference lost its display identity")
	}
	for _, variable := range result.Variables {
		if variable.Name == "LITERAL" && variable.Reference != nil {
			t.Fatal("literal is presented as a reference")
		}
	}
	request := RunRequest{ProjectID: projectID, EnvironmentID: environmentID, Operation: "deploy", Trigger: TriggerManual, Actor: "operator", PlanRevision: result.DesiredRevision, SlotClass: SlotHeavy, RequestDigest: "literal-mode-proof"}
	run, _, err := NewOrchestrationStore(fixture.store).Enqueue(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	changed := "replacement"
	if _, err := fixture.plans.PutVariable(t.Context(), projectID, environmentID, "LITERAL", "operator", VariableWriteRequest{Revision: result.DesiredRevision, Value: &changed, Sensitivity: "secret", Scopes: []string{"runtime"}}); err != nil {
		t.Fatal(err)
	}
	frozen, err := fixture.plans.OpenRunScopedVariables(t.Context(), run.ID, environmentID, "runtime")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range frozen {
		if value.Value != literal {
			t.Fatal("new desired value changed frozen run semantics")
		}
	}
}

func TestRecoveryPreflightCountsWritableResourcesAndPrivateIPC(t *testing.T) {
	source := DraftSourceConfig{Kind: SourceCompose, ComposeFiles: []ComposeDocument{{Path: "compose.yml", Content: "services:\n  app:\n    image: nginx:alpine\n    ipc: private\n    volumes: [shared:/data, '/source:/app:ro']\n  worker:\n    image: nginx:alpine\n    volumes: [shared:/data]\nvolumes:\n  shared: {name: actual_shared, external: true}\n"}}}
	analysis, err := analyzeComposeDocuments(source.ComposeFiles)
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range analysis.Services {
		if len(service.Advanced) > 0 {
			t.Fatal("private IPC falsely requires host-equivalent authority")
		}
	}
	draft := &Draft{Data: DraftData{Source: &source}}
	if count := writablePersistentStorageCount(draft, PlanConfiguration{}); count != 1 {
		t.Fatalf("writable resources counted %d times", count)
	}
}

func TestLegacyRecoveredDraftRetainsInferenceMode(t *testing.T) {
	variable := PlannedVariable{Name: "JD_IMPORT_ENV_OLD", Sensitivity: "secret", Scopes: []string{"runtime"}}
	draft := &Draft{Data: DraftData{Source: &DraftSourceConfig{ComposeFiles: []ComposeDocument{{Content: "services:\n  app:\n    environment:\n      NAME: ${JD_IMPORT_ENV_OLD}\n"}}}, Adoption: &WorkloadAdoption{Inputs: []RecoveredInput{{StorageKey: variable.Name, Name: "NAME"}}, BaselineConfiguration: PlanConfiguration{Variables: []PlannedVariable{variable}}}}, environment: map[string]string{variable.Name: "legacy"}}
	if err := draft.validateRecoveredInputBindings(PlanConfiguration{Variables: []PlannedVariable{variable}}); err != nil {
		t.Fatal("shipped draft blocked", err)
	}
	variable.Scopes = []string{"runtime", "build"}
	if err := draft.validateRecoveredInputBindings(PlanConfiguration{Variables: []PlannedVariable{variable}}); !errors.Is(err, ErrInvalidVariable) {
		t.Fatal("scope expansion accepted", err)
	}
	variable.Scopes = []string{"runtime"}
	variable.ValueMode = "reference"
	if err := draft.validateRecoveredInputBindings(PlanConfiguration{Variables: []PlannedVariable{variable}}); !errors.Is(err, ErrInvalidVariable) {
		t.Fatal("reference conversion accepted", err)
	}
}

func TestRecoveredComposeRenderedLiteralsDecodeExactlyOnce(t *testing.T) {
	candidate, reader, paths, root := scopedComposeFixture(t)
	var model map[string]any
	if err := json.Unmarshal(reader.compose, &model); err != nil {
		t.Fatal(err)
	}
	rendered := "prefix$$UNSET-$${HOME}-$$$$"
	object(object(model["services"])["missing"])["environment"] = map[string]any{"LITERAL": rendered}
	model["x-literal"] = rendered
	reader.compose = mustJSON(model)
	originalDigest := digestBytes(mustJSON(model))
	for _, capture := range reader.captures {
		reader.images = map[string]*dockerx.ImageDetail{"example/unavailable:latest": capture.Image}
		break
	}
	recovered, err := RecoverDockerWorkloadWithScope(t.Context(), candidate, reader, paths, root, RecoveryAllServices)
	if err != nil {
		t.Fatal(err, recovered.Adoption.Blockers)
	}
	if recovered.Adoption.OriginalConfigurationDigest != originalDigest {
		t.Fatal("normalization changed original fresh-capture fence")
	}
	key := recoveryVariableName("missing", "env_LITERAL")
	if got := recovered.Environment[key]; got != "prefix$UNSET-${HOME}-$$" {
		t.Fatalf("already rendered env was captured as encoded bytes: %q", got)
	}
	var final map[string]any
	if err := yaml.Unmarshal([]byte(recovered.Source.ComposeFiles[0].Content), &final); err != nil {
		t.Fatal(err)
	}
	if final["x-literal"] != rendered {
		t.Fatalf("rendered literal accumulated escaping: %q", final["x-literal"])
	}
}

func TestFailedExistingContainerCaptureDoesNotPretendItIsAnAbsentDeclaration(t *testing.T) {
	candidate, reader, paths, root := scopedComposeFixture(t)
	candidate.Kind = "container"
	candidate.Services = []WorkloadService{{Name: "original", ResourceID: "unavailable"}}
	recovered, err := RecoverDockerWorkload(t.Context(), candidate, reader, paths, root)
	if !errors.Is(err, ErrRecoveryBlocked) {
		t.Fatal("failed existing capture accepted", err)
	}
	for _, issue := range recovered.Adoption.Issues {
		if issue.Code == "inactive_service_image_missing" || issue.Code == "container_replacement" {
			t.Fatal("capture failure produced an unrelated migration diagnosis", issue.Code)
		}
	}
}

func TestRecoveredInputsProtectNativeArgumentsAndReleaseUnboundVariables(t *testing.T) {
	fixture := newPlanningStoreFixture(t)
	recovered := recoveredStoreFixture(t)
	for _, name := range []string{"NATIVE_ENV", "JD_IMPORTED_ARG_0"} {
		recovered.Environment[name] = "original"
		recovered.Configuration.Variables = append(recovered.Configuration.Variables, PlannedVariable{Name: name, Sensitivity: "secret", Scopes: []string{"runtime"}, ValueMode: "literal"})
		kind := "environment"
		if name == "JD_IMPORTED_ARG_0" {
			kind = "argument"
		}
		AddRecoveredInput(recovered, name, name, "native-app", kind, "native", "application")
	}
	recovered.Configuration.Runtime.Command = []string{"/bin/sh", "-c", `exec "$JD_IMPORTED_ARG_0"`}
	recovered.Adoption.BaselineConfiguration = recovered.Configuration
	draft, err := fixture.plans.CreateRecoveredDraft(t.Context(), 41, "operator", DraftIntentConfig{Name: "native-input-binding", Profile: ProfileCompose}, recovered)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range draft.Data.Adoption.Inputs {
		if input.Bound != (input.Kind == "argument") {
			t.Fatal("draft bound state did not follow runtime", input.Name)
		}
	}
	configuration := *draft.Data.Configuration
	configuration.Variables = slices.DeleteFunc(append([]PlannedVariable{}, configuration.Variables...), func(variable PlannedVariable) bool { return variable.Name == "JD_IMPORTED_ARG_0" })
	if err := draft.validateRecoveredInputBindings(configuration); !errors.Is(err, ErrInvalidVariable) {
		t.Fatal("native command alias deletion accepted in draft", err)
	}
	configuration.Runtime.Command = nil
	if err := draft.validateRecoveredInputBindings(configuration); err != nil {
		t.Fatal("unused native alias remains protected", err)
	}
	draft = checkRecoveredDraft(t, fixture, draft)
	var ack []string
	for _, finding := range draft.Findings {
		if finding.Severity == PreflightWarning {
			ack = append(ack, finding.Code)
		}
	}
	adopted, err := fixture.plans.Commit(t.Context(), draft.ID, 41, false, DraftCommitRequest{Revision: draft.Revision, AcknowledgedWarnings: ack})
	if err != nil {
		t.Fatal(err)
	}
	views, err := fixture.plans.ListVariables(t.Context(), adopted.ProjectID, adopted.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range views {
		if view.RecoveredInput != nil && view.RecoveredInput.Bound != (view.Name == "JD_IMPORTED_ARG_0") {
			t.Fatal("settings bound state does not follow desired runtime", view.Name)
		}
	}
	revision := adopted.PlanRevision
	if _, err := fixture.plans.DeleteVariable(t.Context(), adopted.ProjectID, adopted.EnvironmentID, "JD_IMPORTED_ARG_0", revision); !errors.Is(err, ErrInvalidVariable) {
		t.Fatal("bound native argument deletion accepted", err)
	}
	replacement := "replacement"
	for _, request := range []VariableWriteRequest{
		{Revision: revision, Value: &replacement, Sensitivity: "secret", Scopes: []string{"build"}},
		{Revision: revision, Reference: "${{variable.NATIVE_ENV}}", Sensitivity: "secret", Scopes: []string{"runtime"}},
	} {
		if _, err := fixture.plans.PutVariable(t.Context(), adopted.ProjectID, adopted.EnvironmentID, "JD_IMPORTED_ARG_0", "operator", request); !errors.Is(err, ErrInvalidVariable) {
			t.Fatal("bound native argument declaration changed", err)
		}
	}
	result, err := fixture.plans.PutVariable(t.Context(), adopted.ProjectID, adopted.EnvironmentID, "NATIVE_ENV", "operator", VariableWriteRequest{Revision: revision, Value: &replacement, Sensitivity: "plain", Scopes: []string{"runtime", "build"}})
	if err != nil {
		t.Fatal("ordinary native environment variable edit refused", err)
	}
	_, err = fixture.plans.DeleteVariable(t.Context(), adopted.ProjectID, adopted.EnvironmentID, "NATIVE_ENV", result.DesiredRevision)
	if err != nil {
		t.Fatal("ordinary native environment variable deletion refused", err)
	}
	desired, err := fixture.plans.EnvironmentConfiguration(t.Context(), adopted.ProjectID, adopted.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	desired.Runtime.Command = nil
	desired, err = fixture.plans.SaveEnvironmentConfiguration(t.Context(), adopted.ProjectID, adopted.EnvironmentID, ConfigurationWriteRequest{Revision: desired.Revision, Build: desired.Build, Runtime: desired.Runtime, Checks: desired.Checks, Dependencies: desired.Dependencies, Domains: desired.Domains})
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range desired.Variables {
		if view.Name == "JD_IMPORTED_ARG_0" && (view.RecoveredInput == nil || view.RecoveredInput.Bound) {
			t.Fatal("removed native argument binding still protected its declaration")
		}
	}
	if _, err := fixture.plans.DeleteVariable(t.Context(), adopted.ProjectID, adopted.EnvironmentID, "JD_IMPORTED_ARG_0", desired.Revision); err != nil {
		t.Fatal("unused native command argument deletion refused", err)
	}
}

func TestRecoveredInputBindingIgnoresEscapedLiteralsAndTracksExactNames(t *testing.T) {
	name := "JD_IMPORT_ENV_NAME"
	for _, fixture := range []struct {
		content string
		bound   bool
	}{
		{"services: {app: {environment: {NAME: '${JD_IMPORT_ENV_NAME}'}}}", true},
		{"services: {app: {environment: {NAME: '$JD_IMPORT_ENV_NAME'}}}", true},
		{"services: {app: {environment: {NAME: '${JD_IMPORT_ENV_NAME:-fallback}'}}}", true},
		{"services: {app: {environment: {NAME: '$${JD_IMPORT_ENV_NAME}'}}}", false},
		{"services: {app: {labels: {'${JD_IMPORT_ENV_NAME}': ordinary}}}", false},
		{"services: {app: {environment: {NAME: '${JD_IMPORT_ENV_NAME_OTHER}'}}}", false},
		{"services: {app: {image: nginx}} # ${JD_IMPORT_ENV_NAME}\n", false},
	} {
		if got := recoveredInputBound(&DraftSourceConfig{ComposeFiles: []ComposeDocument{{Content: fixture.content}}}, RuntimePlanConfig{}, name); got != fixture.bound {
			t.Errorf("binding = %v, want %v for %s", got, fixture.bound, fixture.content)
		}
	}
	if !recoveredInputBound(nil, RuntimePlanConfig{Command: []string{"/bin/sh", "-c", `exec "$JD_IMPORTED_ARG_0"`}}, "JD_IMPORTED_ARG_0") || recoveredInputBound(nil, RuntimePlanConfig{Command: []string{"$JD_IMPORTED_ARG_01"}}, "JD_IMPORTED_ARG_0") {
		t.Fatal("native exact argument reference was not distinguished")
	}
}

func TestLegacyNativeArgumentMetadataUsesVerifiedBaselineModeAndInitialCommand(t *testing.T) {
	source := DraftSourceConfig{Kind: SourceImport, Mode: SourceModeExistingPM2}
	runtime := RuntimePlanConfig{Command: []string{"/bin/sh", "-c", `exec "$JD_IMPORTED_ARG_0" "$JD_IMPORTED_abcdef123456_ARG_1" "$ORIGINAL_ENV"`}}
	inputs := legacyNativeInputBindings(source, runtime)
	if len(inputs) != 2 || inputs[0].Name != "Argument 0" || inputs[1].Name != "Argument 1" {
		t.Fatal("old command aliases were not recovered", inputs)
	}
	if len(legacyNativeInputBindings(DraftSourceConfig{Kind: SourceLocal}, runtime)) != 0 {
		t.Fatal("ordinary source claimed native recovery provenance")
	}
	if len(legacyNativeInputBindings(source, RuntimePlanConfig{})) != 0 {
		t.Fatal("absent command inferred unrelated native variables")
	}
	variable := PlannedVariable{Name: "JD_IMPORTED_ARG_0", Sensitivity: "secret", Scopes: []string{"runtime"}}
	configuration := PlanConfiguration{Runtime: runtime, Variables: []PlannedVariable{variable}}
	draft := &Draft{Data: DraftData{Source: &DraftSourceConfig{Kind: SourceLocal}, Configuration: &configuration, Adoption: &WorkloadAdoption{Inputs: []RecoveredInput{inputs[0]}}}, environment: map[string]string{variable.Name: "node"}}
	if err := draft.validateRecoveredInputBindings(configuration); err != nil {
		t.Fatal("shipped native draft lost its inference mode", err)
	}
}
