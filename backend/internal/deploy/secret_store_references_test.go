package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/secretstore"
)

// fakeStores stands in for the connections: it knows which stores exist and
// which serve previews, answers each reference with a value and a version,
// and counts how often it was asked.
type fakeStores struct {
	mu       sync.Mutex
	previews map[string]bool // store → allows previews; absent = no such store
	values   map[string]secretstore.Read
	fail     error
	resolves int
	asked    []secretstore.ResolveOptions
}

func (f *fakeStores) Check(_ context.Context, reference secretstore.Reference, preview bool) error {
	allows, ok := f.previews[reference.Store]
	if !ok {
		return fmt.Errorf("%w: there is no secret store named %s", secretstore.ErrInvalid, reference.Store)
	}
	if preview && !allows {
		return fmt.Errorf("%w: %s does not serve previews", secretstore.ErrInvalid, reference.Store)
	}
	return nil
}

func (f *fakeStores) Resolve(_ context.Context, references []secretstore.Reference, options secretstore.ResolveOptions) (map[string]secretstore.Read, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolves++
	f.asked = append(f.asked, options)
	if f.fail != nil {
		return nil, f.fail
	}
	out := map[string]secretstore.Read{}
	for _, reference := range references {
		read, ok := f.values[reference.String()]
		if !ok {
			return nil, &secretstore.Failure{Store: reference.Store, Reference: reference.Location(), Message: "no such key"}
		}
		out[reference.String()] = read
	}
	return out, nil
}

func TestStoreReferencesParseOnlyInTheirOwnKind(t *testing.T) {
	t.Parallel()
	for value, want := range map[string]VariableReference{
		"${{store.vault-prod/apps/web#DATABASE_URL}}": {Kind: "store", Target: "vault-prod/apps/web#DATABASE_URL"},
		"${{store.infisical#API_KEY@3}}":              {Kind: "store", Target: "infisical#API_KEY@3"},
	} {
		got, err := ParseVariableReference(value)
		if err != nil || got != want {
			t.Errorf("ParseVariableReference(%q) = %#v, %v; want %#v", value, got, err, want)
		}
	}
	// The store grammar's slashes, # and @ belong to it alone: no older kind
	// gains them, and a malformed store reference is refused, not literal.
	for _, value := range []string{
		"${{store.vault}}", "${{store.vault/../x#K}}", "${{store.vault/a#K@0}}", "${{store.Vault/a#K}}",
		"${{credential.a/b}}", "${{credential.a#b}}", "${{database.1@2}}", "${{variable.A#B}}", "${{server.A/B}}",
	} {
		if _, err := ParseVariableReference(value); !errors.Is(err, ErrInvalidVariable) {
			t.Errorf("ParseVariableReference(%q) error = %v, want ErrInvalidVariable", value, err)
		}
	}
	// A value saved before modes were recorded was literal text in a kind
	// that did not exist yet, and stays so.
	for _, test := range []struct {
		mode      string
		reference bool
	}{{"reference", true}, {"", false}, {"literal", false}} {
		if _, ok := parseStoredReference("${{store.vault/a#K}}", test.mode); ok != test.reference {
			t.Errorf("parseStoredReference(store, %q) = %v, want %v", test.mode, ok, test.reference)
		}
	}
}

// The graph follows a variable alias into a store reference and makes the
// alias a secret leaf; a value read from a store is a literal, so text in it
// shaped like a reference is never followed; and a cycle among the
// environment's own variables is still found with store references present.
func TestTheVariableGraphReadsStoreReferencesAsSecretLiterals(t *testing.T) {
	t.Parallel()
	values := map[string]string{
		"DATABASE_URL": "${{store.vault/apps/web#DATABASE_URL}}",
		"ALIAS":        "${{variable.DATABASE_URL}}",
		"TRICK":        "${{store.vault/apps/web#TRICK}}",
		"PLAIN":        "hello",
	}
	modes := map[string]string{"DATABASE_URL": "reference", "ALIAS": "reference", "TRICK": "reference", "PLAIN": "literal"}
	reads := map[string]secretstore.Read{
		"vault/apps/web#DATABASE_URL": {Value: "postgres://from-vault", Version: "3"},
		"vault/apps/web#TRICK":        {Value: "${{variable.PLAIN}}", Version: "1"},
	}
	resolved, secret, err := ResolveVariableGraphWithModes(values, modes, func(reference VariableReference) (string, bool, error) {
		if reference.Kind != StoreReferenceKind {
			t.Fatalf("an unexpected external reference %#v", reference)
		}
		return storeValue(reads, reference)
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved["ALIAS"] != "postgres://from-vault" || !secret["ALIAS"] || !secret["DATABASE_URL"] {
		t.Fatalf("resolved %#v, secret leaves %#v", resolved, secret)
	}
	if resolved["TRICK"] != "${{variable.PLAIN}}" {
		t.Fatalf("a store's value was followed as a reference: %q", resolved["TRICK"])
	}
	values["A"], values["B"] = "${{variable.B}}", "${{variable.A}}"
	modes["A"], modes["B"] = "reference", "reference"
	if _, _, err := ResolveVariableGraphWithModes(values, modes, nil); !errors.Is(err, ErrVariableCycle) {
		t.Fatalf("a cycle beside store references = %v", err)
	}
	if _, _, err := ResolveVariableGraphWithModes(values, modes, func(reference VariableReference) (string, bool, error) {
		return storeValue(reads, reference)
	}); !errors.Is(err, ErrVariableCycle) {
		t.Fatalf("a cycle at run time = %v", err)
	}
	if _, _, err := storeValue(reads, VariableReference{Kind: StoreReferenceKind, Target: "vault/apps/web#UNREAD"}); !errors.Is(err, ErrInvalidVariable) {
		t.Fatalf("a reference that was never read = %v", err)
	}
}

// Saving a store reference checks what the run will need: the variable is
// secret, the store exists, and a preview's variable names only a store that
// serves previews. Without connections at all the reference is refused.
func TestSavingAStoreReferenceChecksTheStoreAndThePreviewRule(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	plans := fixture.plans
	projectID, environmentID := insertConfigurationFixture(t, fixture)
	put := func(environment int64, name, reference, sensitivity string) error {
		_, err := plans.PutVariable(ctx, projectID, environment, name, "operator", VariableWriteRequest{
			Revision: 1, Reference: reference, Sensitivity: sensitivity, Scopes: []string{"runtime"},
		})
		return err
	}
	if err := put(environmentID, "TOKEN", "${{store.vault/apps/web#TOKEN}}", "secret"); err == nil || !strings.Contains(err.Error(), "secret stores are unavailable") {
		t.Fatalf("without connections = %v", err)
	}
	plans.WithSecretStores(&fakeStores{previews: map[string]bool{"vault": false, "preview-vault": true}})
	if err := put(environmentID, "TOKEN", "${{store.vault/apps/web#TOKEN}}", "plain"); !errors.Is(err, ErrInvalidVariable) || !strings.Contains(err.Error(), "must be secret") {
		t.Fatalf("a plain variable reading a store = %v", err)
	}
	if err := put(environmentID, "TOKEN", "${{store.nobody/apps/web#TOKEN}}", "secret"); !errors.Is(err, ErrInvalidVariable) || !strings.Contains(err.Error(), "no secret store named nobody") {
		t.Fatalf("a missing store = %v", err)
	}
	if err := put(environmentID, "TOKEN", "${{store.vault/apps/web#TOKEN}}", "secret"); err != nil {
		t.Fatalf("a production variable reading the store = %v", err)
	}
	preview, err := fixture.store.DB.Exec(`
		INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, strategy, expected_downtime, protected, created_at, updated_at)
		VALUES(?, 'pr-7', 'pr-7', 'preview', 1, 'blue_green', 0, 0, 1, 1)`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	previewID, _ := preview.LastInsertId()
	if err := put(previewID, "TOKEN", "${{store.vault/apps/web#TOKEN}}", "secret"); !errors.Is(err, ErrInvalidVariable) || !strings.Contains(err.Error(), "does not serve previews") {
		t.Fatalf("a preview reading a production store = %v", err)
	}
	if problem, err := plans.storeReferenceProblem(ctx, true, "TOKEN", "secret", VariableReference{Kind: StoreReferenceKind, Target: "preview-vault/pr#TOKEN"}); err != nil || problem != "" {
		t.Fatalf("a preview reading a store that serves previews = %q, %v", problem, err)
	}
	uses, err := plans.SecretStoreUses(ctx)
	if err != nil || len(uses["vault"]) != 1 || uses["vault"][0].Variable != "TOKEN" || uses["vault"][0].Reference != "vault/apps/web#TOKEN" {
		t.Fatalf("uses = %#v, %v", uses, err)
	}
	if err := plans.SecretStoreInUse(ctx, "vault"); !errors.Is(err, ErrSecretStoreInUse) || !strings.Contains(err.Error(), "config-app (production) as TOKEN") {
		t.Fatalf("removing a store in use = %v", err)
	}
	if err := plans.SecretStoreInUse(ctx, "preview-vault"); err != nil {
		t.Fatalf("removing a store nothing reads = %v", err)
	}
}

// A run reads its stores once, fresh, at its first step: the read is kept
// for the run's later openings of every scope, the transcript and metadata
// say what was read and which version — never the value — and the next run
// reads the stores again. A preview's run asks as a preview.
func TestARunReadsItsStoresOnceAtItsFirstStepAndRecordsTheVersions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	plans := fixture.plans
	stores := &fakeStores{previews: map[string]bool{"vault": false}, values: map[string]secretstore.Read{
		"vault/apps/web#DATABASE_URL": {Value: "postgres://fixture-secret-value", Version: "3"},
		"vault/apps/web#API_KEY@2":    {Value: "pinned-fixture-value", Version: "2"},
	}}
	plans.WithSecretStores(stores)
	projectID, environmentID := insertConfigurationFixture(t, fixture)
	revision := 1
	for _, variable := range []struct{ name, reference string }{
		{"DATABASE_URL", "${{store.vault/apps/web#DATABASE_URL}}"},
		{"API_KEY", "${{store.vault/apps/web#API_KEY@2}}"},
		{"DB_ALIAS", "${{variable.DATABASE_URL}}"},
	} {
		result, err := plans.PutVariable(ctx, projectID, environmentID, variable.name, "operator", VariableWriteRequest{
			Revision: revision, Reference: variable.reference, Sensitivity: "secret", Scopes: []string{"runtime", "build"},
		})
		if err != nil {
			t.Fatal(err)
		}
		revision = result.DesiredRevision
	}
	runs := NewOrchestrationStore(fixture.store)
	enqueue := func(label string) *EngineRun {
		run, _, err := runs.Enqueue(ctx, RunRequest{
			ProjectID: projectID, EnvironmentID: environmentID, Operation: OperationDeploy,
			Trigger: TriggerManual, Actor: "operator", RequestDigest: "store-" + label, PlanRevision: revision,
		})
		if err != nil {
			t.Fatal(err)
		}
		return run
	}
	run := enqueue("first")
	executor := &NormalizedStepExecutor{store: runs, variables: plans}
	output := &recordingStepOutput{}
	if failure := executor.readSecretStores(ctx, StepExecution{Run: *run, Step: RunStep{Key: StepResolveSource}, Output: output}); failure != nil {
		t.Fatalf("the read failed: %+v", failure)
	}
	transcript := output.joined()
	for _, want := range []string{
		"status: Read API_KEY from secret store vault: apps/web#API_KEY@2, version 2",
		"status: Read DATABASE_URL from secret store vault: apps/web#DATABASE_URL, version 3",
	} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("the transcript\n%s\nlacks %q", transcript, want)
		}
	}
	if strings.Contains(transcript, "fixture-secret-value") || strings.Contains(transcript, "pinned-fixture-value") {
		t.Fatalf("the transcript holds a value:\n%s", transcript)
	}
	for _, scope := range []string{"build", "runtime", "build"} {
		values, err := plans.OpenRunScopedVariables(ctx, run.ID, environmentID, scope)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]ScopedVariableValue{}
		for _, value := range values {
			got[value.Name] = value
		}
		if got["DATABASE_URL"].Value != "postgres://fixture-secret-value" || got["DB_ALIAS"].Value != "postgres://fixture-secret-value" ||
			got["API_KEY"].Value != "pinned-fixture-value" || got["DB_ALIAS"].Sensitivity != "secret" {
			t.Fatalf("%s values = %#v", scope, got)
		}
	}
	if stores.resolves != 1 {
		t.Fatalf("the stores were asked %d times for one run; want once", stores.resolves)
	}
	// A later step of the same run reads nothing again.
	if failure := executor.readSecretStores(ctx, StepExecution{Run: *run, Step: RunStep{Key: StepStartCandidate}, Output: output}); failure != nil || stores.resolves != 1 {
		t.Fatalf("a later step read again: %+v, %d", failure, stores.resolves)
	}
	snapshot, err := runs.Run(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		Reads []StoreRead `json:"secretStoreReads"`
	}
	if err := json.Unmarshal(snapshot.Metadata, &metadata); err != nil || len(metadata.Reads) != 2 ||
		metadata.Reads[1].Reference != "vault/apps/web#DATABASE_URL" || metadata.Reads[1].Version != "3" ||
		!metadata.Reads[0].Pinned || strings.Join(metadata.Reads[1].Variables, ",") != "DATABASE_URL" {
		t.Fatalf("metadata = %s (%v)", snapshot.Metadata, err)
	}
	if strings.Contains(string(snapshot.Metadata), "fixture-secret-value") {
		t.Fatalf("the run's metadata holds a value: %s", snapshot.Metadata)
	}

	// The run finishing forgets the values; the next run reads afresh.
	plans.SecretStoreRunObserver().RunFinished(ctx, *run)
	if plans.HasRunStoreReads(run.ID) {
		t.Fatal("a finished run's reads were kept")
	}
	stores.values["vault/apps/web#DATABASE_URL"] = secretstore.Read{Value: "postgres://rotated", Version: "4"}
	second := enqueue("second")
	values, err := plans.OpenRunScopedVariables(ctx, second.ID, environmentID, "runtime")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		if value.Name == "DATABASE_URL" && value.Value != "postgres://rotated" {
			t.Fatalf("the next run read %q", value.Value)
		}
	}
	if stores.resolves != 2 || stores.asked[1].Preview {
		t.Fatalf("resolves %d, asked %+v", stores.resolves, stores.asked)
	}
}

// A store that does not answer fails the run's first step in its own words,
// with nothing acquired or started; a stop never reads a store, so a store
// that is down keeps nobody from stopping a deployment.
func TestAStoreThatDoesNotAnswerFailsTheFirstStepAndNeverAStop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	plans := fixture.plans
	stores := &fakeStores{previews: map[string]bool{"vault": false},
		fail: &secretstore.Failure{Store: "vault", Message: "could not reach https://vault.example.test: connection refused"}}
	plans.WithSecretStores(stores)
	projectID, environmentID := insertConfigurationFixture(t, fixture)
	result, err := plans.PutVariable(ctx, projectID, environmentID, "TOKEN", "operator", VariableWriteRequest{
		Revision: 1, Reference: "${{store.vault/apps/web#TOKEN}}", Sensitivity: "secret", Scopes: []string{"runtime"},
	})
	if err != nil {
		t.Fatal(err)
	}
	runs := NewOrchestrationStore(fixture.store)
	run, _, err := runs.Enqueue(ctx, RunRequest{
		ProjectID: projectID, EnvironmentID: environmentID, Operation: OperationDeploy,
		Trigger: TriggerManual, Actor: "operator", RequestDigest: "store-down", PlanRevision: result.DesiredRevision,
	})
	if err != nil {
		t.Fatal(err)
	}
	executor := &NormalizedStepExecutor{store: runs, variables: plans}
	output := &recordingStepOutput{}
	failure := executor.readSecretStores(ctx, StepExecution{Run: *run, Step: RunStep{Key: StepResolveSource}, Output: output})
	if failure == nil || failure.State != StepFailed || failure.ErrorCode != "secret_store_unreadable" ||
		failure.ErrorMessage != "secret store vault: could not reach https://vault.example.test: connection refused" {
		t.Fatalf("failure = %+v", failure)
	}
	if !strings.Contains(output.joined(), "stderr: Could not read secret store vault: could not reach") {
		t.Fatalf("transcript = %s", output.joined())
	}
	// The same answer reaches a later opening, as the step's own failure.
	if _, err := plans.OpenRunScopedVariables(ctx, run.ID, environmentID, "runtime"); err == nil {
		t.Fatal("an opening after a failed read succeeded")
	} else if step := normalizedStepFailure(err); step.ErrorCode != "secret_store_unreadable" {
		t.Fatalf("a later opening's failure = %+v", step)
	}
	stop := *run
	stop.Operation = OperationStop
	if failure := executor.readSecretStores(ctx, StepExecution{Run: stop, Step: RunStep{Key: StepStartCandidate}, Output: output}); failure != nil {
		t.Fatalf("a stop read the stores: %+v", failure)
	}
	for _, key := range []StepKey{StepAcquireSource, StepBuildArtifact, StepActivate, StepNotify} {
		if readsSecretStoresAt(StepExecution{Run: *run, Step: RunStep{Key: key}}) {
			t.Fatalf("%s reads the stores", key)
		}
	}
}

// A rollback re-reads the store as it is now and says so when the store
// holds another version than the release it restores read.
func TestARollbackSaysWhichVersionTheRestoredReleaseRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	plans := fixture.plans
	stores := &fakeStores{previews: map[string]bool{"vault": false}, values: map[string]secretstore.Read{
		"vault/apps/web#TOKEN": {Value: "v5", Version: "5"},
	}}
	plans.WithSecretStores(stores)
	projectID, environmentID := insertConfigurationFixture(t, fixture)
	result, err := plans.PutVariable(ctx, projectID, environmentID, "TOKEN", "operator", VariableWriteRequest{
		Revision: 1, Reference: "${{store.vault/apps/web#TOKEN}}", Sensitivity: "secret", Scopes: []string{"runtime"},
	})
	if err != nil {
		t.Fatal(err)
	}
	runs := NewOrchestrationStore(fixture.store)
	original, _, err := runs.Enqueue(ctx, RunRequest{
		ProjectID: projectID, EnvironmentID: environmentID, Operation: OperationDeploy,
		Trigger: TriggerManual, Actor: "operator", RequestDigest: "store-original", PlanRevision: result.DesiredRevision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runs.MergeRunMetadata(ctx, original.ID, map[string]any{"secretStoreReads": []StoreRead{{
		Reference: "vault/apps/web#TOKEN", Store: "vault", Location: "apps/web#TOKEN", Version: "3", Variables: []string{"TOKEN"},
	}}}); err != nil {
		t.Fatal(err)
	}
	release, err := fixture.store.DB.Exec(`INSERT INTO deploy_releases(project_id, environment_id, release_number, run_id, created_at)
		VALUES(?, ?, 41, ?, 1)`, projectID, environmentID, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	releaseID, _ := release.LastInsertId()
	rollback, _, err := runs.Enqueue(ctx, RunRequest{
		ProjectID: projectID, EnvironmentID: environmentID, Operation: OperationRollback,
		Trigger: TriggerManual, Actor: "operator", RequestDigest: "store-rollback", PlanRevision: result.DesiredRevision,
		Metadata: json.RawMessage(fmt.Sprintf(`{"targetReleaseId":%d}`, releaseID)),
		Steps:    []StepKey{StepRenderRuntime, StepStartCandidate, StepRecordRelease, StepNotify},
	})
	if err != nil {
		t.Fatal(err)
	}
	output := &recordingStepOutput{}
	executor := &NormalizedStepExecutor{store: runs, variables: plans}
	if failure := executor.readSecretStores(ctx, StepExecution{Run: *rollback, Step: RunStep{Key: StepRenderRuntime}, Output: output}); failure != nil {
		t.Fatalf("the rollback's read failed: %+v", failure)
	}
	want := "status: Read TOKEN from secret store vault: apps/web#TOKEN, version 5 (release #41 read version 3; pin a version in the reference to keep one)"
	if !strings.Contains(output.joined(), want) {
		t.Fatalf("the transcript\n%s\nlacks %q", output.joined(), want)
	}
}
