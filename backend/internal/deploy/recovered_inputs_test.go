package deploy

import (
	"errors"
	"strings"
	"testing"
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
	if _, err := fixture.plans.DeleteVariable(t.Context(), adopted.ProjectID, adopted.EnvironmentID, ids["web"], 2); !errors.Is(err, ErrInvalidVariable) {
		t.Fatal("bound input deletion accepted", err)
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
	variable.ValueMode = "reference"
	if err := draft.validateRecoveredInputBindings(PlanConfiguration{Variables: []PlannedVariable{variable}}); !errors.Is(err, ErrInvalidVariable) {
		t.Fatal("reference conversion accepted", err)
	}
}
