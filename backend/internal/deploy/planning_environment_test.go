package deploy

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func environmentDraft(t *testing.T) (*planningStoreFixture, *Draft) {
	t.Helper()
	fixture := newPlanningStoreFixture(t)
	draft, err := fixture.plans.Create(t.Context(), 41, "operator")
	if err != nil {
		t.Fatal(err)
	}
	draft = saveCompletePlanningDraft(t, fixture.plans, draft)
	configuration := PlanConfiguration{
		Build: BuildPlanConfig{Method: BuildNone}, Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst},
		Variables: []PlannedVariable{
			{Name: "API_TOKEN", Sensitivity: "secret", Required: true, Scopes: []string{"runtime", "release_task"}},
			{Name: "GENERATED", Sensitivity: "secret", Generate: 32, Scopes: []string{"runtime"}},
		},
	}
	draft, err = fixture.plans.Save(t.Context(), draft.ID, 41, false, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftConfiguration, Configuration: &configuration,
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixture, draft
}

func environmentPreflight(t *testing.T, draft *Draft) *PreflightResult {
	t.Helper()
	result, err := PreflightDraft(t.Context(), draft, &preflightObserverFake{observation: HostObservation{
		Facilities: map[string]FacilityObservation{"git": {Available: true}},
	}}, true)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestDraftEnvironmentIsSealedAndCommitsAtomicallyWithDeclaredScopes(t *testing.T) {
	fixture, draft := environmentDraft(t)
	missing := environmentPreflight(t, draft)
	found := false
	for _, finding := range missing.Findings {
		found = found || finding.Code == "variable_required_api_token"
	}
	if !found {
		t.Fatal("required input passed before any value was staged")
	}
	const secret = "unique-draft-secret-must-remain-sealed"
	draft.Data.Configuration.Variables = append(draft.Data.Configuration.Variables, PlannedVariable{
		Name: "PUBLIC_DEFAULT", Sensitivity: "plain", Scopes: []string{"runtime"}, Value: "public-default",
	})
	dotenv := "API_TOKEN=" + secret + "\nGENERATED=operator-override\nEXTRA=extra-secret\nPUBLIC_DEFAULT=private-override"
	draft, err := fixture.plans.Save(t.Context(), draft.ID, 41, false, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftConfiguration, Configuration: draft.Data.Configuration, Dotenv: &dotenv,
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err = fixture.plans.Get(t.Context(), draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(draft.EnvironmentKeys, []string{"API_TOKEN", "EXTRA", "GENERATED", "PUBLIC_DEFAULT"}) {
		t.Fatalf("saved metadata = %#v", draft.EnvironmentKeys)
	}
	var data, sealed string
	if err := fixture.store.DB.QueryRow(`SELECT data_json,environment_enc FROM deploy_drafts WHERE id=?`, draft.ID).Scan(&data, &sealed); err != nil {
		t.Fatal(err)
	}
	response, _ := json.Marshal(draft)
	if sealed == "" || strings.Contains(data+sealed+string(response), secret) || strings.Contains(string(response), sealed) {
		t.Fatal("draft input leaked into public JSON or plaintext storage")
	}
	checked := environmentPreflight(t, draft)
	for _, finding := range checked.Findings {
		if finding.Severity == PreflightBlocked || finding.Severity == PreflightDecision {
			t.Fatalf("staged input did not satisfy preflight: %#v", finding)
		}
	}
	if strings.Contains(checked.Preview, secret) || strings.Contains(checked.Preview, "operator-override") {
		t.Fatal("preflight preview contains secret values")
	}
	draft, err = fixture.plans.SavePreflight(t.Context(), draft.ID, 41, false, draft.Revision, checked)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.plans.Commit(t.Context(), draft.ID, 41, true, DraftCommitRequest{Revision: draft.Revision})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, value, scopes string }{
		{"API_TOKEN", secret, "runtime,release_task"},
		{"GENERATED", "operator-override", "runtime"},
		{"EXTRA", "extra-secret", "runtime,build"},
		{"PUBLIC_DEFAULT", "private-override", "runtime"},
	} {
		var stored, sensitivity, scopes string
		if err := fixture.store.DB.QueryRow(`SELECT value_enc,sensitivity,scopes FROM deploy_variable_revisions WHERE environment_id=? AND key=?`, result.EnvironmentID, tc.name).Scan(&stored, &sensitivity, &scopes); err != nil {
			t.Fatal(err)
		}
		value, err := fixture.sealer.Open(stored)
		if err != nil || value != tc.value || sensitivity != "secret" || scopes != tc.scopes {
			t.Fatalf("%s commit did not preserve its value, sensitivity and scopes: %v, %s, %s", tc.name, err, sensitivity, scopes)
		}
	}
	replay, err := fixture.plans.Commit(t.Context(), draft.ID, 41, true, DraftCommitRequest{Revision: draft.Revision})
	if err != nil || replay.Created || replay.ProjectID != result.ProjectID || planningTableCount(t, fixture.store, "deploy_variable_revisions") != 4 {
		t.Fatalf("commit replay was not idempotent: %#v, %v", replay, err)
	}
}

func TestRemovingDraftOverridesRestoresDeclaredDefaultsAndGenerators(t *testing.T) {
	fixture, draft := environmentDraft(t)
	draft.Data.Configuration.Variables = append(draft.Data.Configuration.Variables,
		PlannedVariable{Name: "PUBLIC_DEFAULT", Sensitivity: "plain", Scopes: []string{"runtime"}, Value: "public-default"},
		PlannedVariable{Name: "ALIAS", Sensitivity: "secret", Scopes: []string{"runtime"}, Reference: "${{variable.API_TOKEN}}"},
	)
	text := "API_TOKEN=retained-secret\nGENERATED=operator-override\nPUBLIC_DEFAULT=private-override\nALIAS=temporary-alias"
	draft, err := fixture.plans.Save(t.Context(), draft.ID, 41, false, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftConfiguration, Configuration: draft.Data.Configuration, Dotenv: &text,
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err = fixture.plans.Get(t.Context(), draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	blank := ""
	draft, err = fixture.plans.Save(t.Context(), draft.ID, 41, false, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftConfiguration, Configuration: draft.Data.Configuration, Dotenv: &blank,
		RetainEnvironmentKeys: []string{"API_TOKEN"},
	})
	if err != nil {
		t.Fatal(err)
	}
	checked := environmentPreflight(t, draft)
	draft, err = fixture.plans.SavePreflight(t.Context(), draft.ID, 41, false, draft.Revision, checked)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.plans.Commit(t.Context(), draft.ID, 41, true, DraftCommitRequest{Revision: draft.Revision})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, expected, sensitivity string }{
		{"GENERATED", "", "secret"},
		{"PUBLIC_DEFAULT", "public-default", "plain"},
		{"ALIAS", "${{variable.API_TOKEN}}", "secret"},
	} {
		var sealed, sensitivity string
		if err := fixture.store.DB.QueryRow(`SELECT value_enc,sensitivity FROM deploy_variable_revisions WHERE environment_id=? AND key=?`, result.EnvironmentID, tc.name).Scan(&sealed, &sensitivity); err != nil {
			t.Fatal(err)
		}
		value, err := fixture.sealer.Open(sealed)
		if err != nil || sensitivity != tc.sensitivity {
			t.Fatalf("%s sensitivity/decryption = %s, %v", tc.name, sensitivity, err)
		}
		if tc.name == "GENERATED" {
			if len(value) != 32 || value == "operator-override" {
				t.Fatal("removing an override did not restore password generation")
			}
		} else if value != tc.expected {
			t.Fatalf("%s fallback was not restored", tc.name)
		}
	}
}

func TestDraftEnvironmentRejectsInvalidOrStaleChangesAndPreservesMaskedInputs(t *testing.T) {
	fixture, draft := environmentDraft(t)
	text := "API_TOKEN=keep-this-secret"
	var err error
	draft, err = fixture.plans.Save(t.Context(), draft.ID, 41, false, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftConfiguration, Configuration: draft.Data.Configuration, Dotenv: &text,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, text string
		revision   int
		owner      int64
		want       error
	}{
		{"duplicate", "API_TOKEN=x\nAPI_TOKEN=y", draft.Revision, 41, ErrInvalidVariable},
		{"cyclic", "API_TOKEN=${{variable.EXTRA}}\nEXTRA=${{variable.API_TOKEN}}", draft.Revision, 41, ErrVariableCycle},
		{"stale", "API_TOKEN=wrong", draft.Revision - 1, 41, ErrDraftRevision},
		{"other owner", "API_TOKEN=wrong", draft.Revision, 42, ErrDraftForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := fixture.plans.Save(t.Context(), draft.ID, tc.owner, false, DraftSaveRequest{
				Revision: tc.revision, Step: DraftConfiguration, Configuration: draft.Data.Configuration, Dotenv: &tc.text,
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			fresh, err := fixture.plans.Get(t.Context(), draft.ID)
			if err != nil || fresh.Revision != draft.Revision || fresh.environment["API_TOKEN"] != "keep-this-secret" {
				t.Fatal("refused save changed the draft")
			}
		})
	}
	more := "EXTRA=another-secret"
	draft, err = fixture.plans.Save(t.Context(), draft.ID, 41, false, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftConfiguration, Configuration: draft.Data.Configuration, Dotenv: &more,
		RetainEnvironmentKeys: []string{"API_TOKEN"},
	})
	if err != nil || draft.environment["API_TOKEN"] != "keep-this-secret" || draft.environment["EXTRA"] != "another-secret" {
		t.Fatalf("resumed masked input was not retained: %v", err)
	}
	draft, err = fixture.plans.Save(t.Context(), draft.ID, 41, false, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftConfiguration, Configuration: draft.Data.Configuration,
	})
	if err != nil || len(draft.environment) != 2 {
		t.Fatalf("omitting dotenv discarded staged input: %v", err)
	}
	if _, err := fixture.plans.Save(t.Context(), draft.ID, 41, false, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftConfiguration, Configuration: draft.Data.Configuration, Dotenv: &more,
		RetainEnvironmentKeys: []string{"NOT_SAVED"},
	}); !errors.Is(err, ErrInvalidVariable) {
		t.Fatalf("unknown retained key was accepted: %v", err)
	}
	branch := *draft.Data.Source
	branch.Ref = "another-branch"
	draft, err = fixture.plans.Save(t.Context(), draft.ID, 41, false, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftSource, Source: &branch,
	})
	if err != nil || len(draft.environment) != 2 {
		t.Fatalf("same repository reinspection discarded staged input: %v", err)
	}
	draft, err = fixture.plans.Get(t.Context(), draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	blank := ""
	draft, err = fixture.plans.Save(t.Context(), draft.ID, 41, false, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftConfiguration, Configuration: draft.Data.Configuration, Dotenv: &blank,
		RetainEnvironmentKeys: draft.EnvironmentKeys,
	})
	if err != nil || draft.environment["API_TOKEN"] != "keep-this-secret" || len(draft.environment) != 2 {
		t.Fatalf("masked inputs could not be retained after branch change and reload: %v", err)
	}
	otherSource := *draft.Data.Source
	otherSource.URL = "https://example.test/another/repo.git"
	draft, err = fixture.plans.Save(t.Context(), draft.ID, 41, false, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftSource, Source: &otherSource,
	})
	if err != nil || len(draft.environment) != 0 || len(draft.EnvironmentKeys) != 0 || draft.environmentEnc != "" {
		t.Fatalf("changed source retained secret inputs: %v", err)
	}
}

func TestDraftEmptyInputCannotBypassRequiredGenerationOrCommit(t *testing.T) {
	fixture, draft := environmentDraft(t)
	text := "API_TOKEN=\nGENERATED="
	draft.Data.Configuration.Variables[0].Generate = 32
	draft, err := fixture.plans.Save(t.Context(), draft.ID, 41, false, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftConfiguration, Configuration: draft.Data.Configuration, Dotenv: &text,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := environmentPreflight(t, draft)
	found := false
	for _, finding := range result.Findings {
		found = found || finding.Code == "variable_required_api_token"
	}
	if !found || !reflect.DeepEqual(draft.EnvironmentKeys, []string{"API_TOKEN", "GENERATED"}) {
		t.Fatal("empty value overrode a generator without requiring a usable input")
	}
	// Commit verifies real staged values again, even if findings were damaged.
	result.Findings = nil
	draft, err = fixture.plans.SavePreflight(t.Context(), draft.ID, 41, false, draft.Revision, result)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.plans.Commit(t.Context(), draft.ID, 41, true, DraftCommitRequest{Revision: draft.Revision}); !errors.Is(err, ErrPreflightBlocked) {
		t.Fatalf("empty required value committed: %v", err)
	}
	if planningTableCount(t, fixture.store, "deploy_projects") != 0 {
		t.Fatal("refused commit left a project behind")
	}
	clear := ""
	draft, err = fixture.plans.Save(t.Context(), draft.ID, 41, false, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftConfiguration, Configuration: draft.Data.Configuration, Dotenv: &clear,
	})
	if err != nil || len(draft.environment) != 0 || draft.environmentEnc != "" {
		t.Fatalf("explicit empty dotenv did not clear staged inputs: %v", err)
	}
}

func TestDraftEmptyOverrideSurvivesReloadAndRetainWithoutGeneratingDefault(t *testing.T) {
	fixture, draft := environmentDraft(t)
	draft.Data.Configuration.Variables = append(draft.Data.Configuration.Variables, PlannedVariable{
		Name: "PUBLIC_DEFAULT", Sensitivity: "plain", Scopes: []string{"runtime"}, Value: "public-default",
	})
	dotenv := "API_TOKEN=required-secret\nGENERATED=\nPUBLIC_DEFAULT="
	draft, err := fixture.plans.Save(t.Context(), draft.ID, 41, false, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftConfiguration, Configuration: draft.Data.Configuration, Dotenv: &dotenv,
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err = fixture.plans.Get(t.Context(), draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(draft.EnvironmentKeys, []string{"API_TOKEN", "GENERATED", "PUBLIC_DEFAULT"}) {
		t.Fatalf("empty saved overrides omitted from reload metadata: %#v", draft.EnvironmentKeys)
	}
	blank := ""
	draft, err = fixture.plans.Save(t.Context(), draft.ID, 41, false, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftConfiguration, Configuration: draft.Data.Configuration,
		Dotenv: &blank, RetainEnvironmentKeys: draft.EnvironmentKeys,
	})
	if err != nil {
		t.Fatal(err)
	}
	checked := environmentPreflight(t, draft)
	draft, err = fixture.plans.SavePreflight(t.Context(), draft.ID, 41, false, draft.Revision, checked)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.plans.Commit(t.Context(), draft.ID, 41, true, DraftCommitRequest{Revision: draft.Revision})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"GENERATED", "PUBLIC_DEFAULT"} {
		var sealed, sensitivity string
		if err := fixture.store.DB.QueryRow(`SELECT value_enc,sensitivity FROM deploy_variable_revisions WHERE environment_id=? AND key=?`, result.EnvironmentID, name).Scan(&sealed, &sensitivity); err != nil {
			t.Fatal(err)
		}
		value, err := fixture.sealer.Open(sealed)
		if err != nil || value != "" || sensitivity != "secret" {
			t.Fatalf("%s empty override was not preserved: sensitivity=%s err=%v", name, sensitivity, err)
		}
	}
}
