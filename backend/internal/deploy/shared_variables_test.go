package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSharedReferencesParseInTheirOwnKinds(t *testing.T) {
	t.Parallel()
	for value, want := range map[string]VariableReference{
		"${{server.API_URL}}":     {Kind: "server", Target: "API_URL"},
		"${{project.SENTRY_DSN}}": {Kind: "project", Target: "SENTRY_DSN"},
		"${{server.a1_b}}":        {Kind: "server", Target: "a1_b"},
	} {
		got, err := ParseVariableReference(value)
		if err != nil || got != want {
			t.Errorf("ParseVariableReference(%q) = %#v, %v; want %#v", value, got, err, want)
		}
	}
	// A shared target is a variable name: not a path, a dotted name or one
	// that starts with anything but a letter.
	for _, value := range []string{
		"${{server._HIDDEN}}", "${{server.1ST}}", "${{project.A.B}}", "${{server.a-b}}", "${{shared.API_URL}}",
	} {
		if _, err := ParseVariableReference(value); !errors.Is(err, ErrInvalidVariable) {
			t.Errorf("ParseVariableReference(%q) error = %v, want ErrInvalidVariable", value, err)
		}
	}
	if got := SharedReference(0, "API_URL"); got != "${{server.API_URL}}" {
		t.Errorf("server reference = %q", got)
	}
	if got := SharedReference(7, "API_URL"); got != "${{project.API_URL}}" {
		t.Errorf("project reference = %q", got)
	}
}

// A value saved before modes were recorded was a reference only in the kinds
// that existed then; ${{server.X}} saved that way was literal text, and stays
// so. A value saved as a reference since is one.
func TestStoredSharedReferenceNeedsAnExplicitMode(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		value, mode string
		reference   bool
	}{
		{"${{server.API_URL}}", "reference", true},
		{"${{server.API_URL}}", "", false},
		{"${{server.API_URL}}", "literal", false},
		{"${{credential.token}}", "", true},
		{"${{credential.token}}", "literal", false},
	} {
		if _, ok := parseStoredReference(test.value, test.mode); ok != test.reference {
			t.Errorf("parseStoredReference(%q, %q) = %v, want %v", test.value, test.mode, ok, test.reference)
		}
	}
	values := map[string]string{"LEGACY": "${{server.API_URL}}", "NEW": "${{server.API_URL}}"}
	modes := map[string]string{"LEGACY": "", "NEW": "reference"}
	resolved, _, err := ResolveVariableGraphWithModes(values, modes, func(VariableReference) (string, bool, error) {
		return "https://api.example.test", false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved["LEGACY"] != "${{server.API_URL}}" || resolved["NEW"] != "https://api.example.test" {
		t.Fatalf("resolved = %#v", resolved)
	}
}

// Server and project variables are written once and read by an environment
// only through its own variables. Each resolves its current value when the
// values are opened, a secret one makes the variable reading it a secret
// leaf, and a reference to one that is gone fails closed rather than reaching
// the workload as its own text.
func TestSharedVariablesResolveThroughTheEnvironmentsOwnVariables(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	projectID, environmentID := insertConfigurationFixture(t, fixture)
	plans := fixture.plans
	put := func(project int64, name, value, sensitivity string) {
		t.Helper()
		if _, _, err := plans.PutSharedVariable(ctx, project, name, "operator", SharedVariableWrite{
			Value: &value, Sensitivity: sensitivity,
		}); err != nil {
			t.Fatalf("put shared %s: %v", name, err)
		}
	}
	put(0, "API_URL", "https://api.example.test", "plain")
	put(0, "SENTRY_DSN", "https://key@sentry.example.test/1", "secret")
	put(projectID, "API_URL", "https://project-api.example.test", "plain")

	revision := 1
	for _, variable := range []struct{ name, reference, sensitivity string }{
		{"SERVER_API", "${{server.API_URL}}", "plain"},
		{"PROJECT_API", "${{project.API_URL}}", "plain"},
		{"SENTRY_DSN", "${{server.SENTRY_DSN}}", "secret"},
		{"API_ALIAS", "${{variable.SERVER_API}}", "plain"},
	} {
		result, err := plans.PutVariable(ctx, projectID, environmentID, variable.name, "operator", VariableWriteRequest{
			Revision: revision, Reference: variable.reference, Sensitivity: variable.sensitivity, Scopes: []string{"runtime"},
		})
		if err != nil {
			t.Fatalf("put %s: %v", variable.name, err)
		}
		revision = result.DesiredRevision
	}
	open := func() map[string]ScopedVariableValue {
		t.Helper()
		values, err := plans.OpenScopedVariables(ctx, environmentID, "runtime")
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]ScopedVariableValue{}
		for _, value := range values {
			got[value.Name] = value
		}
		return got
	}
	got := open()
	for name, want := range map[string]string{
		"SERVER_API": "https://api.example.test", "PROJECT_API": "https://project-api.example.test",
		"SENTRY_DSN": "https://key@sentry.example.test/1", "API_ALIAS": "https://api.example.test",
	} {
		if got[name].Value != want {
			t.Errorf("%s = %q, want %q", name, got[name].Value, want)
		}
	}
	if got["SENTRY_DSN"].Sensitivity != "secret" || got["SERVER_API"].Sensitivity != "plain" {
		t.Errorf("sensitivities = %q, %q", got["SENTRY_DSN"].Sensitivity, got["SERVER_API"].Sensitivity)
	}

	// The reference is what is saved, so a change reaches the next opening
	// without touching the environment's own variables.
	put(0, "API_URL", "https://api-v2.example.test", "plain")
	if got := open(); got["SERVER_API"].Value != "https://api-v2.example.test" || got["API_ALIAS"].Value != "https://api-v2.example.test" {
		t.Fatalf("after the change: %q, %q", got["SERVER_API"].Value, got["API_ALIAS"].Value)
	}
	// A run freezes the environment's variables, which hold the reference,
	// so it reads the shared value as it is when the run opens them.
	run, _, err := NewOrchestrationStore(fixture.store).Enqueue(ctx, RunRequest{
		ProjectID: projectID, EnvironmentID: environmentID, Operation: OperationDeploy,
		Trigger: TriggerManual, Actor: "operator", RequestDigest: "shared-references", PlanRevision: revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	put(projectID, "API_URL", "https://project-api-v2.example.test", "plain")
	values, err := plans.OpenRunScopedVariables(ctx, run.ID, environmentID, "runtime")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		if value.Name == "PROJECT_API" && value.Value != "https://project-api-v2.example.test" {
			t.Fatalf("the run read %q", value.Value)
		}
	}

	// Going around the refusal to remove one that is read, as an archived
	// project's references would: the next run fails closed.
	if _, err := fixture.store.DB.Exec(`DELETE FROM deploy_shared_variables WHERE project_id IS NULL AND name = 'API_URL'`); err != nil {
		t.Fatal(err)
	}
	if _, err := plans.OpenScopedVariables(ctx, environmentID, "runtime"); !errors.Is(err, ErrInvalidVariable) ||
		!strings.Contains(err.Error(), "server.API_URL") {
		t.Fatalf("a missing shared variable opened with error %v", err)
	}
}

// A reference is checked when it is saved: the variable exists in the scope
// it names — a project's are its own — and a variable reading a secret is
// secret itself.
func TestPutVariableRefusesASharedReferenceItCannotRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	projectID, environmentID := insertConfigurationFixture(t, fixture)
	other, err := fixture.store.DB.Exec(`
		INSERT INTO deploy_projects(name, profile, repo_path, branch, compose_file, hook_secret, hook_id, enabled, created_at, updated_at)
		VALUES('other-app', 'worker', '/srv/other-app', 'main', 'compose.yml', '', 'other-hook', 1, 1, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	otherProject, _ := other.LastInsertId()
	secret := "sk_live_value"
	if _, _, err := fixture.plans.PutSharedVariable(ctx, 0, "STRIPE_KEY", "operator", SharedVariableWrite{Value: &secret, Sensitivity: "secret"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.plans.PutSharedVariable(ctx, otherProject, "ONLY_THERE", "operator", SharedVariableWrite{Value: &secret, Sensitivity: "plain"}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ reference, sensitivity, want string }{
		{"${{server.NOT_SAVED}}", "secret", "this server has no shared variable NOT_SAVED"},
		{"${{project.ONLY_THERE}}", "plain", "this project has no shared variable ONLY_THERE"},
		{"${{server.STRIPE_KEY}}", "plain", "has to be secret too"},
	} {
		_, err := fixture.plans.PutVariable(ctx, projectID, environmentID, "READER", "operator", VariableWriteRequest{
			Revision: 1, Reference: test.reference, Sensitivity: test.sensitivity, Scopes: []string{"runtime"},
		})
		if !errors.Is(err, ErrInvalidVariable) || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s as %s: error = %v, want one saying %q", test.reference, test.sensitivity, err, test.want)
		}
	}
}

// The list never carries a value, names every environment variable reading
// each one, and says which of those run an earlier value until they deploy
// again. Removing one is refused while it is read and names the readers.
func TestSharedVariableListAndRemovalFollowTheirReaders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	projectID, environmentID := insertConfigurationFixture(t, fixture)
	plans := fixture.plans
	value := "https://hooks.example.test/very-secret-path"
	created, change, err := plans.PutSharedVariable(ctx, 0, "HOOK_URL", "alice", SharedVariableWrite{Value: &value, Sensitivity: "secret"})
	if err != nil || !change.Created {
		t.Fatalf("create = %#v, %#v, %v", created, change, err)
	}
	if created.Reference != "${{server.HOOK_URL}}" || created.Masked != "••••••••" || created.UpdatedBy != "alice" || len(created.UsedBy) != 0 {
		t.Fatalf("created = %#v", created)
	}
	if _, err := plans.PutVariable(ctx, projectID, environmentID, "WEBHOOK", "operator", VariableWriteRequest{
		Revision: 1, Reference: "${{server.HOOK_URL}}", Sensitivity: "secret", Scopes: []string{"runtime"},
	}); err != nil {
		t.Fatal(err)
	}

	// The environment's live release started now; nothing has changed since.
	started := fixture.now.Unix()
	run, err := fixture.store.DB.Exec(`INSERT INTO deploy_runs(project_id, environment_id, started_at, status) VALUES(?, ?, ?, 'success')`, projectID, environmentID, started)
	if err != nil {
		t.Fatal(err)
	}
	runID, _ := run.LastInsertId()
	release, err := fixture.store.DB.Exec(`
		INSERT INTO deploy_releases(project_id, environment_id, release_number, run_id, state, plan_revision, config_digest, variables_digest, strategy, created_at)
		VALUES(?, ?, 1, ?, 'live', 2, 'c', 'v', 'stop_first', ?)`, projectID, environmentID, runID, started)
	if err != nil {
		t.Fatal(err)
	}
	releaseID, _ := release.LastInsertId()
	if _, err := fixture.store.DB.Exec(`UPDATE deploy_environments SET live_release_id = ? WHERE id = ?`, releaseID, environmentID); err != nil {
		t.Fatal(err)
	}
	listed := func() SharedVariable {
		t.Helper()
		variables, err := plans.ListSharedVariables(ctx, 0)
		if err != nil || len(variables) != 1 {
			t.Fatalf("list = %#v, %v", variables, err)
		}
		return variables[0]
	}
	use := listed().UsedBy
	if len(use) != 1 || use[0].ProjectName != "config-app" || use[0].EnvironmentName != "production" ||
		use[0].Variable != "WEBHOOK" || use[0].Waiting {
		t.Fatalf("used by = %#v", use)
	}

	// The same value again moves nothing; a new one leaves the release
	// waiting for its next deploy.
	fixture.now = fixture.now.Add(time.Minute)
	if _, change, err := plans.PutSharedVariable(ctx, 0, "HOOK_URL", "bob", SharedVariableWrite{Value: &value, Sensitivity: "secret"}); err != nil || change.ValueChanged || change.SensitivityChanged {
		t.Fatalf("same value = %#v, %v", change, err)
	}
	if got := listed(); got.UpdatedBy != "alice" || got.UsedBy[0].Waiting {
		t.Fatalf("after an unchanged save: %#v", got)
	}
	next := "https://hooks.example.test/rotated"
	if _, change, err := plans.PutSharedVariable(ctx, 0, "HOOK_URL", "bob", SharedVariableWrite{Value: &next, Sensitivity: "secret"}); err != nil || !change.ValueChanged {
		t.Fatalf("new value = %#v, %v", change, err)
	}
	got := listed()
	if got.UpdatedBy != "bob" || !got.UsedBy[0].Waiting {
		t.Fatalf("after a new value: %#v", got)
	}
	// Changing the type alone keeps the sealed value.
	if _, _, err := plans.PutSharedVariable(ctx, 0, "HOOK_URL", "bob", SharedVariableWrite{Sensitivity: "plain"}); err != nil {
		t.Fatal(err)
	}
	revealed, err := plans.RevealSharedVariable(ctx, 0, "HOOK_URL")
	if err != nil || revealed.Value != next {
		t.Fatalf("reveal = %#v, %v", revealed, err)
	}

	err = plans.DeleteSharedVariable(ctx, 0, "HOOK_URL")
	if !errors.Is(err, ErrSharedVariableInUse) || !strings.Contains(err.Error(), "config-app (production) as WEBHOOK") {
		t.Fatalf("delete while read = %v", err)
	}
	if _, err := plans.DeleteVariable(ctx, projectID, environmentID, "WEBHOOK", 2); err != nil {
		t.Fatal(err)
	}
	if err := plans.DeleteSharedVariable(ctx, 0, "HOOK_URL"); err != nil {
		t.Fatal(err)
	}
	if err := plans.DeleteSharedVariable(ctx, 0, "HOOK_URL"); !errors.Is(err, ErrSharedVariableNotFound) {
		t.Fatalf("second delete = %v", err)
	}
	if _, err := plans.RevealSharedVariable(ctx, 0, "HOOK_URL"); !errors.Is(err, ErrSharedVariableNotFound) {
		t.Fatalf("reveal after delete = %v", err)
	}
}

// Names are references' targets and values are bounded; a new variable needs
// a value, and an archived project takes no new ones.
func TestPutSharedVariableRefusesWhatCannotBeRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	projectID, _ := insertConfigurationFixture(t, fixture)
	value := "x"
	huge := strings.Repeat("x", maxDeploymentVariableValue+1)
	for label, test := range map[string]struct {
		project int64
		name    string
		write   SharedVariableWrite
		want    error
	}{
		"leading underscore": {0, "_X", SharedVariableWrite{Value: &value, Sensitivity: "plain"}, ErrInvalidVariable},
		"dash":               {0, "A-B", SharedVariableWrite{Value: &value, Sensitivity: "plain"}, ErrInvalidVariable},
		"no type":            {0, "A", SharedVariableWrite{Value: &value}, ErrInvalidVariable},
		"no value":           {0, "A", SharedVariableWrite{Sensitivity: "plain"}, ErrInvalidVariable},
		"too long":           {0, "A", SharedVariableWrite{Value: &huge, Sensitivity: "plain"}, ErrInvalidVariable},
		"no such project":    {projectID + 99, "A", SharedVariableWrite{Value: &value, Sensitivity: "plain"}, ErrNotFound},
	} {
		if _, _, err := fixture.plans.PutSharedVariable(ctx, test.project, test.name, "operator", test.write); !errors.Is(err, test.want) {
			t.Errorf("%s: error = %v, want %v", label, err, test.want)
		}
	}
	if _, err := fixture.store.DB.Exec(`UPDATE deploy_projects SET archived_at = 5 WHERE id = ?`, projectID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.plans.PutSharedVariable(ctx, projectID, "A", "operator", SharedVariableWrite{Value: &value, Sensitivity: "plain"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archived project write = %v", err)
	}
}

// A new project's wizard can read a server variable that exists, and never a
// project one: the project it would belong to does not exist yet.
func TestDraftSharedReferencesAreCheckedBeforeCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	value := "https://api.example.test"
	if _, _, err := fixture.plans.PutSharedVariable(ctx, 0, "API_URL", "operator", SharedVariableWrite{Value: &value, Sensitivity: "plain"}); err != nil {
		t.Fatal(err)
	}
	draft := func(reference string, staged bool) *Draft {
		d := &Draft{Data: DraftData{Configuration: &PlanConfiguration{Variables: []PlannedVariable{
			{Name: "API", Sensitivity: "plain", Scopes: []string{"runtime"}, Reference: reference},
		}}}}
		if staged {
			d.environment = map[string]string{"API": "typed in the wizard"}
		}
		return d
	}
	if err := fixture.plans.checkDraftSharedReferences(ctx, draft("${{server.API_URL}}", false)); err != nil {
		t.Fatalf("an existing server variable: %v", err)
	}
	for reference, want := range map[string]string{
		"${{server.MISSING}}":  "no shared variable MISSING",
		"${{project.API_URL}}": "a new project has no project variables yet",
	} {
		err := fixture.plans.checkDraftSharedReferences(ctx, draft(reference, false))
		var validation *ValidationError
		if !errors.Is(err, ErrInvalidPlan) || !errors.As(err, &validation) || validation.Field != "API" ||
			!strings.Contains(err.Error(), want) {
			t.Errorf("%s: error = %v, want a field error saying %q", reference, err, want)
		}
		if err := fixture.plans.checkDraftSharedReferences(ctx, draft(reference, true)); err != nil {
			t.Errorf("%s replaced by a staged value: %v", reference, err)
		}
	}
}
