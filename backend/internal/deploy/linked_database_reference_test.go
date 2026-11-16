package deploy

import (
	"encoding/json"
	"testing"
)

// linkedDatabaseRow is the dotenv line the new-deployment page sends for a
// linked database: environmentText writes each row as NAME=JSON-quoted value.
func linkedDatabaseRow(value string) string {
	encoded, _ := json.Marshal(value)
	return "DATABASE_URL=" + string(encoded)
}

// commitLinkedDatabaseDraft saves a draft the way the configure step does —
// the variable's row in the dotenv, the database as a dependency — runs
// preflight and commits it.
func commitLinkedDatabaseDraft(t *testing.T, fixture *planningStoreFixture, value string, linked ...string) (*Draft, int64, int64) {
	t.Helper()
	draft, err := fixture.plans.Create(t.Context(), 41, "operator")
	if err != nil {
		t.Fatal(err)
	}
	draft = saveCompletePlanningDraft(t, fixture.plans, draft)
	configuration := PlanConfiguration{
		Build: BuildPlanConfig{Method: BuildNone}, Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst},
		Variables: []PlannedVariable{{Name: "DATABASE_URL", Sensitivity: "secret", Required: true, Scopes: []string{"runtime", "build"}}},
	}
	for _, id := range linked {
		configuration.Dependencies = append(configuration.Dependencies, PlannedDependency{
			Kind: "database", Ownership: "linked", ResourceKind: "database_connection", ResourceID: id, Config: json.RawMessage(`{}`),
		})
	}
	dotenv := linkedDatabaseRow(value)
	draft, err = fixture.plans.Save(t.Context(), draft.ID, 41, false, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftConfiguration, Configuration: &configuration, Dotenv: &dotenv,
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err = fixture.plans.SavePreflight(t.Context(), draft.ID, 41, false, draft.Revision, environmentPreflight(t, draft))
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.plans.Commit(t.Context(), draft.ID, 41, true, DraftCommitRequest{Revision: draft.Revision})
	if err != nil {
		t.Fatal(err)
	}
	return draft, result.ProjectID, result.EnvironmentID
}

// committedDatabaseURL is DATABASE_URL's active revision as stored.
func committedDatabaseURL(t *testing.T, fixture *planningStoreFixture, environmentID int64) (value, mode, createdBy string, revision int) {
	t.Helper()
	var sealed string
	if err := fixture.store.DB.QueryRow(`
		SELECT value_enc, value_mode, created_by, revision FROM deploy_variable_revisions
		 WHERE environment_id = ? AND key = 'DATABASE_URL' AND active = 1`, environmentID).Scan(&sealed, &mode, &createdBy, &revision); err != nil {
		t.Fatal(err)
	}
	value, err := fixture.sealer.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	return value, mode, createdBy, revision
}

// resolvesThroughDatabase says whether a run would hand the application the
// database's address for the value and mode, rather than the text.
func resolvesThroughDatabase(t *testing.T, value, mode string) bool {
	t.Helper()
	resolved, _, err := ResolveVariableGraphWithModes(map[string]string{"DATABASE_URL": value}, map[string]string{"DATABASE_URL": mode},
		func(reference VariableReference) (string, bool, error) {
			return "postgres://app@db-" + reference.Target + ".jd.internal:5432/app", true, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	return resolved["DATABASE_URL"] != value
}

// The failure this was found with: the page linked database 2 and the application was
// handed the characters ${{database.2}}, with no network to the database.
func TestNewDeploymentDatabaseLinkCommitsAsAReference(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"${{database.2}}", "${{database.2.jdbc}}", "${{database.2.pooled}}", "${{database.2.url.app_cache}}"} {
		fixture := newPlanningStoreFixture(t)
		draft, _, environmentID := commitLinkedDatabaseDraft(t, fixture, value, "2")
		stored, mode, _, _ := committedDatabaseURL(t, fixture, environmentID)
		if stored != value || mode != "reference" || !resolvesThroughDatabase(t, stored, mode) {
			t.Fatalf("%s committed as %q (%s), which a run reads as the text", value, stored, mode)
		}
		// The commit handler refuses a deploy key a database reference by
		// reading this; read as literal, a key's draft slipped past it.
		if reference, ok := draft.VariableReferences()["DATABASE_URL"]; !ok || reference.Kind != "database" {
			t.Fatalf("%s is not among the draft's references: %+v", value, draft.VariableReferences())
		}
	}
}

func TestSuppliedValueWithoutItsDatabaseLinkStaysLiteral(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		value  string
		linked []string
	}{
		// A value captured from somewhere else that happens to read like a
		// reference is kept as the text it is.
		"no database linked":           {"${{database.2}}", nil},
		"another database linked":      {"${{database.2}}", []string{"3"}},
		"a reference inside more text": {"${{database.2}}?sslmode=require", []string{"2"}},
		"a database named, not by id":  {"${{database.orders}}", []string{"2"}},
	} {
		fixture := newPlanningStoreFixture(t)
		_, _, environmentID := commitLinkedDatabaseDraft(t, fixture, test.value, test.linked...)
		if stored, mode, _, _ := committedDatabaseURL(t, fixture, environmentID); stored != test.value || mode != "literal" {
			t.Fatalf("%s: committed %q as %s", name, stored, mode)
		}
	}
}

// brokenLinkedDatabaseProject is what a commit before this fix left: the
// draft linked database 2 and staged its reference, and the variable was
// stored literal. The dependency is added after commit so today's commit,
// which would read the link, writes the old footprint.
func brokenLinkedDatabaseProject(t *testing.T) (*planningStoreFixture, int64, int64) {
	t.Helper()
	fixture := newPlanningStoreFixture(t)
	draft, projectID, environmentID := commitLinkedDatabaseDraft(t, fixture, "${{database.2}}")
	data := draft.Data
	data.Configuration.Dependencies = []PlannedDependency{{
		Kind: "database", Ownership: "linked", ResourceKind: "database_connection", ResourceID: "2", Config: json.RawMessage(`{}`),
	}}
	encoded, _ := json.Marshal(data)
	if _, err := fixture.store.DB.Exec(`UPDATE deploy_drafts SET data_json = ? WHERE id = ?`, string(encoded), draft.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB.Exec(`
		INSERT INTO deploy_dependencies(environment_id, release_id, kind, ownership, resource_kind, resource_id, config_json, created_at)
		VALUES(?, 0, 'database', 'linked', 'database_connection', '2', '{}', 1)`, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, mode, _, _ := committedDatabaseURL(t, fixture, environmentID); mode != "literal" {
		t.Fatalf("the fixture did not reproduce the old commit: %s", mode)
	}
	return fixture, projectID, environmentID
}

func TestRepairGivesACommittedDatabaseLinkBackOnce(t *testing.T) {
	t.Parallel()
	fixture, projectID, environmentID := brokenLinkedDatabaseProject(t)
	repaired, err := fixture.plans.RepairLinkedDatabaseLiterals(t.Context())
	if err != nil || len(repaired) != 1 ||
		repaired[0] != (LinkedDatabaseRepair{ProjectID: projectID, EnvironmentID: environmentID, Name: "DATABASE_URL"}) {
		t.Fatalf("repaired = %+v, %v", repaired, err)
	}
	value, mode, createdBy, revision := committedDatabaseURL(t, fixture, environmentID)
	if value != "${{database.2}}" || mode != "reference" || createdBy != "migration" || revision != 2 || !resolvesThroughDatabase(t, value, mode) {
		t.Fatalf("after the repair: %q %s by %s, revision %d", value, mode, createdBy, revision)
	}
	again, err := fixture.plans.RepairLinkedDatabaseLiterals(t.Context())
	if err != nil || len(again) != 0 {
		t.Fatalf("a second pass repaired %+v, %v", again, err)
	}
}

func TestRepairLeavesWhatAPersonDidNotLinkAlone(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]func(*testing.T, *planningStoreFixture, int64, int64){
		// A deploy key may never point a variable at a saved database.
		"a deploy key's draft": func(t *testing.T, fixture *planningStoreFixture, projectID, _ int64) {
			if _, err := fixture.store.DB.Exec(`UPDATE deploy_drafts SET key_id = 9 WHERE committed_project_id = ?`, projectID); err != nil {
				t.Fatal(err)
			}
		},
		"a link removed since": func(t *testing.T, fixture *planningStoreFixture, _, environmentID int64) {
			if _, err := fixture.store.DB.Exec(`DELETE FROM deploy_dependencies WHERE environment_id = ?`, environmentID); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.store.DB.Exec(`
				INSERT INTO deploy_dependencies(environment_id, release_id, kind, ownership, resource_kind, resource_id, config_json, created_at)
				VALUES(?, 0, 'database', 'linked', 'database_connection', '3', '{}', 1)`, environmentID); err != nil {
				t.Fatal(err)
			}
		},
		// The operator set the value themselves after the commit.
		"a value set since": func(t *testing.T, fixture *planningStoreFixture, projectID, environmentID int64) {
			value := "postgres://app@db.example.test/app"
			var revision int
			if err := fixture.store.DB.QueryRow(`SELECT desired_revision FROM deploy_environments WHERE id = ?`, environmentID).Scan(&revision); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.plans.PutVariable(t.Context(), projectID, environmentID, "DATABASE_URL", "operator", VariableWriteRequest{
				Revision: revision, Value: &value, Sensitivity: "secret", Scopes: []string{"runtime"},
			}); err != nil {
				t.Fatal(err)
			}
		},
		"an archived project": func(t *testing.T, fixture *planningStoreFixture, projectID, _ int64) {
			if _, err := fixture.store.DB.Exec(`UPDATE deploy_projects SET archived_at = 1 WHERE id = ?`, projectID); err != nil {
				t.Fatal(err)
			}
		},
	} {
		fixture, projectID, environmentID := brokenLinkedDatabaseProject(t)
		change(t, fixture, projectID, environmentID)
		repaired, err := fixture.plans.RepairLinkedDatabaseLiterals(t.Context())
		if err != nil || len(repaired) != 0 {
			t.Fatalf("%s: repaired %+v, %v", name, repaired, err)
		}
	}
}
