package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestRehearsalTargetIsTheOneDatabaseTheTaskReaches(t *testing.T) {
	t.Parallel()
	reached := rehearsalDatabaseIDs(map[string]string{
		"DATABASE_URL": "postgres://app:secret@db-7.jd.internal:5432/app",
		"POOLED_URL":   "postgres://app:secret@db-7-pool.jd.internal:6432/app",
		"CACHE_URL":    "redis://cache.example.test:6379",
		"OTHER_URL":    "mysql://shop:secret@db-12.jd.internal:3306/shop",
		"BROKEN":       "%zz://nope",
	})
	if fmt.Sprint(reached) != "[7 12]" {
		t.Fatalf("reached = %v", reached)
	}
	if id, why := chooseRehearsalTarget([]int64{7}, 0); id != 7 || why != "" {
		t.Fatalf("one database = %d %q", id, why)
	}
	if id, why := chooseRehearsalTarget(reached, 0); id != 0 || !strings.Contains(why, "reaches 2 databases (connections 7, 12)") {
		t.Fatalf("two databases, none chosen = %d %q", id, why)
	}
	if id, _ := chooseRehearsalTarget(reached, 12); id != 12 {
		t.Fatalf("two databases, one chosen = %d", id)
	}
	if id, why := chooseRehearsalTarget(reached, 99); id != 0 || !strings.Contains(why, "connection 99") {
		t.Fatalf("a chosen database the task does not reach = %d %q", id, why)
	}
	if id, why := chooseRehearsalTarget(nil, 0); id != 0 || !strings.Contains(why, "no database this dashboard manages") {
		t.Fatalf("no database = %d %q", id, why)
	}
}

func TestRehearsalPolicyIsOffByDefaultAndValidated(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	ctx := context.Background()
	policy, err := fixture.runs.RehearsalPolicy(ctx, fixture.envID)
	if err != nil || policy.Enabled || policy.WhenUnavailable != RehearsalWhenUnavailableBlock {
		t.Fatalf("an unsaved environment's terms = %+v, %v", policy, err)
	}
	for _, bad := range []RehearsalPolicy{
		{Enabled: true, WhenUnavailable: "ignore", RestoreTimeoutSeconds: 600, MaxBackupMB: 10},
		{Enabled: true, RestoreTimeoutSeconds: 5, MaxBackupMB: 10},
		{Enabled: true, RestoreTimeoutSeconds: 600, MaxBackupMB: 0},
		{Enabled: true, ConnectionID: -1, RestoreTimeoutSeconds: 600, MaxBackupMB: 10},
	} {
		if _, err := fixture.runs.PutRehearsalPolicy(ctx, fixture.projectID, fixture.envID, bad, "admin"); !errors.Is(err, ErrInvalidPlan) {
			t.Fatalf("%+v was accepted: %v", bad, err)
		}
	}
	saved, err := fixture.runs.PutRehearsalPolicy(ctx, fixture.projectID, fixture.envID, RehearsalPolicy{
		Enabled: true, ConnectionID: 7, WhenUnavailable: RehearsalWhenUnavailableWarn, RestoreTimeoutSeconds: 120, MaxBackupMB: 64,
	}, "admin")
	if err != nil || !saved.Enabled || saved.ConnectionID != 7 || saved.WhenUnavailable != "warn" || saved.UpdatedBy != "admin" {
		t.Fatalf("saved = %+v, %v", saved, err)
	}
}

// rehearsalRuntime runs release tasks by script: each task's exit code is
// decided by whether it runs as a rehearsal.
type rehearsalRuntime struct {
	orderingRuntimeOwner
	mu       sync.Mutex
	requests []ReleaseTaskRuntimeRequest
	fail     map[bool]bool
}

func (r *rehearsalRuntime) RunReleaseTask(_ context.Context, request ReleaseTaskRuntimeRequest, emit func(BuildLog) error) (int, bool, error) {
	r.mu.Lock()
	r.requests = append(r.requests, request)
	fail := r.fail[request.Rehearsal]
	r.mu.Unlock()
	if fail {
		_ = emit(BuildLog{Stream: "stderr", Text: `ERROR:  column "name" of relation "items" contains null values`})
		return 3, true, errors.New("release task exited with code 3")
	}
	_ = emit(BuildLog{Stream: "stdout", Text: "ALTER TABLE"})
	return 0, true, nil
}

type fakeRehearser struct {
	mu       sync.Mutex
	prepared []RehearsalCopyRequest
	removed  int
	err      error
	samples  []RehearsalLockSample
}

func (f *fakeRehearser) PrepareRehearsalCopy(_ context.Context, request RehearsalCopyRequest, _ func(BuildLog) error) (*RehearsalCopy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prepared = append(f.prepared, request)
	if f.err != nil {
		return nil, f.err
	}
	return &RehearsalCopy{
		Network: "jd-rehearsal-net", Container: "jd-rehearsal-db", RestoreMS: 1200,
		Database: RehearsalTarget{ConnectionID: request.ConnectionID, Name: "orders", Engine: "postgres", Database: "app"},
		Source:   RehearsalSource{Kind: "dump", BackupRunID: 4, Bytes: 4096},
	}, nil
}

func (f *fakeRehearser) SampleRehearsalLocks(context.Context, RehearsalCopy) ([]RehearsalLockSample, bool, error) {
	return f.samples, true, nil
}

func (f *fakeRehearser) RemoveRehearsalCopy(context.Context, EngineRun) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed++
	return nil
}

type rehearsalFixture struct {
	*releaseStoreFixture
	runtime  *rehearsalRuntime
	executor *NormalizedStepExecutor
	run      *EngineRun
	lease    QueueLease
}

// newRehearsalFixture is a run whose plan has one release task in the
// release image and a database variable that reaches managed database 7.
func newRehearsalFixture(t *testing.T, rehearser MigrationRehearser, tasks []ReleaseTaskConfig) *rehearsalFixture {
	t.Helper()
	fixture := newReleaseStoreFixture(t)
	plan := RuntimePlanConfig{
		Strategy: StrategyStopFirst, Command: []string{}, Capabilities: []string{}, Devices: []string{},
		Mounts: []RuntimeMount{{Source: "uploads", Target: "/data"}},
	}
	// The fixture's plan, with the release tasks in its build: a saved plan
	// is immutable, so it is written that way from the start.
	source := DraftSourceConfig{Kind: SourceLocal, Mode: SourceModeLocalCheckout, LocalPath: "/srv/release-fixture"}
	identity := SourceIdentity{Kind: SourceLocal, LocalPath: "/srv/release-fixture", Revision: strings.Repeat("a", 40)}
	build := BuildPlanConfig{Method: BuildDockerfile, Dockerfile: "Dockerfile", Secrets: []BuildSecretConfig{}, ReleaseTasks: tasks}
	sourceJSON, _ := json.Marshal(source)
	identityJSON, _ := json.Marshal(identity)
	buildJSON, _ := json.Marshal(build)
	runtimeJSON, _ := json.Marshal(plan)
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO deploy_sources(environment_id, revision, kind, config_json, identity_json, digest, created_at) VALUES(?, 1, 'local', ?, ?, ?, ?)",
			[]any{fixture.envID, string(sourceJSON), string(identityJSON), digestBytes(sourceJSON, identityJSON), fixture.now.Unix()}},
		{"INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at) VALUES(?, 1, 'dockerfile', ?, '{\"candidates\":[],\"gitRequirements\":{}}', ?, ?, ?)",
			[]any{fixture.envID, string(buildJSON), string(buildJSON), digestBytes(buildJSON), fixture.now.Unix()}},
		{"INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at) VALUES(?, 1, ?, ?, ?, ?)",
			[]any{fixture.envID, string(runtimeJSON), string(runtimeJSON), digestBytes(runtimeJSON), fixture.now.Unix()}},
	} {
		if _, err := fixture.base.DB.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	sealed, err := fixture.variables.sealer.Seal("postgres://app:secret@db-7.jd.internal:5432/app")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.base.DB.Exec(`INSERT INTO deploy_variable_revisions(environment_id, key, revision, sensitivity, scopes, value_enc, value_digest, active, created_by, created_at)
		VALUES(?, 'DATABASE_URL', 1, 'secret', 'runtime,release_task', ?, ?, 1, 'admin', ?)`,
		fixture.envID, sealed, fakeContentDigest("database-url"), fixture.now.Unix()); err != nil {
		t.Fatal(err)
	}
	run, lease := fixture.claimedRun(t, 1)
	createRuntimeCandidate(t, fixture, *run, lease, plan, "rehearsal-v1")
	runtime := &rehearsalRuntime{orderingRuntimeOwner: orderingRuntimeOwner{running: map[string]bool{}}, fail: map[bool]bool{}}
	executor := &NormalizedStepExecutor{store: fixture.runs, variables: fixture.variables, runtime: runtime, rehearser: rehearser}
	return &rehearsalFixture{releaseStoreFixture: fixture, runtime: runtime, executor: executor, run: run, lease: lease}
}

func (f *rehearsalFixture) rehearse(t *testing.T) StepResult {
	t.Helper()
	return f.executor.rehearseMigration(context.Background(), StepExecution{
		Run: *f.run, ClaimToken: f.lease.Token, Output: discardStepOutput{},
	}, mustExecutionPlan(t, f.releaseStoreFixture, *f.run))
}

func (f *rehearsalFixture) enable(t *testing.T, policy RehearsalPolicy) {
	t.Helper()
	if policy.RestoreTimeoutSeconds == 0 {
		policy.RestoreTimeoutSeconds, policy.MaxBackupMB = 600, 2048
	}
	policy.Enabled = true
	if _, err := f.runs.PutRehearsalPolicy(context.Background(), f.projectID, f.envID, policy, "admin"); err != nil {
		t.Fatal(err)
	}
}

var migrateTask = ReleaseTaskConfig{Name: "migrate", Command: "bin/migrate", TimeoutSeconds: 60, Env: []string{"DATABASE_URL"}, Runner: ReleaseTaskRunnerImage}

func TestRehearsalRunsTheTaskAgainstTheCopyAlone(t *testing.T) {
	t.Parallel()
	rehearser := &fakeRehearser{samples: []RehearsalLockSample{{Relation: "items", Mode: "AccessExclusiveLock"}}}
	f := newRehearsalFixture(t, rehearser, []ReleaseTaskConfig{migrateTask})

	// Off by default: nothing is restored and the step says why.
	if result := f.rehearse(t); result.State != StepSkipped || !strings.Contains(string(result.Evidence), "turned off") {
		t.Fatalf("off = %+v", result)
	}
	f.enable(t, RehearsalPolicy{})
	result := f.rehearse(t)
	if result.State != StepPassed {
		t.Fatalf("rehearsal = %+v", result)
	}
	var report RehearsalReport
	if err := json.Unmarshal(result.Evidence, &report); err != nil {
		t.Fatal(err)
	}
	if !report.Rehearsed || report.Database.ConnectionID != 7 || len(report.Tasks) != 1 || report.Tasks[0].ExitCode != 0 ||
		!report.CleanedUp || !report.Isolation.Internal || report.Isolation.VolumesMounted || report.Isolation.Published {
		t.Fatalf("report = %+v", report)
	}
	if !report.Locks.Read || len(report.Locks.Held) != 1 || report.Locks.Held[0].Relation != "items" {
		t.Fatalf("locks = %+v", report.Locks)
	}
	if len(f.runtime.requests) != 1 {
		t.Fatalf("task runs = %d", len(f.runtime.requests))
	}
	request := f.runtime.requests[0]
	if !request.Rehearsal || fmt.Sprint(request.Networks) != "[jd-rehearsal-net]" ||
		request.Variables["DATABASE_URL"] != "postgres://app:secret@db-7.jd.internal:5432/app" {
		t.Fatalf("the rehearsal task ran as %+v", request)
	}
	// Removed before it started, and again when it ended.
	if rehearser.removed < 2 || len(rehearser.prepared) != 1 || rehearser.prepared[0].ConnectionID != 7 {
		t.Fatalf("copies prepared %+v, removals %d", rehearser.prepared, rehearser.removed)
	}
}

func TestAFailedRehearsalStopsTheReleaseBeforeProduction(t *testing.T) {
	t.Parallel()
	rehearser := &fakeRehearser{}
	f := newRehearsalFixture(t, rehearser, []ReleaseTaskConfig{migrateTask})
	f.enable(t, RehearsalPolicy{})
	f.runtime.fail[true] = true
	result := f.rehearse(t)
	if result.State != StepFailed || result.ErrorCode != "rehearsal_failed" ||
		!strings.Contains(result.ErrorMessage, `contains null values`) || !strings.Contains(result.ErrorMessage, "before production was touched") {
		t.Fatalf("failed rehearsal = %+v", result)
	}
	for _, request := range f.runtime.requests {
		if !request.Rehearsal {
			t.Fatal("a task ran against production after its rehearsal failed")
		}
	}
	if rehearser.removed < 2 {
		t.Fatalf("a failed rehearsal left its copy: %d removals", rehearser.removed)
	}
}

func TestARehearsalThatCannotRunBlocksOrWarnsAsTheTermsSay(t *testing.T) {
	t.Parallel()
	rehearser := &fakeRehearser{err: fmt.Errorf("%w: no backup holds orders to restore a copy from", ErrRehearsalUnavailable)}
	f := newRehearsalFixture(t, rehearser, []ReleaseTaskConfig{migrateTask})
	f.enable(t, RehearsalPolicy{WhenUnavailable: RehearsalWhenUnavailableBlock})
	result := f.rehearse(t)
	if result.State != StepFailed || result.ErrorCode != "rehearsal_unavailable" || !strings.Contains(result.ErrorMessage, "no backup holds orders") {
		t.Fatalf("blocked = %+v", result)
	}
	f.enable(t, RehearsalPolicy{WhenUnavailable: RehearsalWhenUnavailableWarn})
	result = f.rehearse(t)
	if result.State != StepWarning || !strings.Contains(string(result.Evidence), "no backup holds orders") {
		t.Fatalf("warned = %+v", result)
	}
	if len(f.runtime.requests) != 0 {
		t.Fatal("a task ran with no copy to run against")
	}

	// A task in the dashboard's shell cannot be rehearsed, and no copy is made.
	g := newRehearsalFixture(t, &fakeRehearser{}, []ReleaseTaskConfig{{Name: "legacy", Command: "make migrate", TimeoutSeconds: 60, Env: []string{}}})
	g.enable(t, RehearsalPolicy{})
	if result := g.rehearse(t); result.State != StepFailed || !strings.Contains(result.ErrorMessage, "dashboard's shell") {
		t.Fatalf("shell task = %+v", result)
	}
	// Without the Databases owner a rehearsal turned on is unavailable,
	// never quietly skipped.
	h := newRehearsalFixture(t, nil, []ReleaseTaskConfig{migrateTask})
	h.executor.rehearser = nil
	h.enable(t, RehearsalPolicy{})
	if result := h.rehearse(t); result.State != StepFailed || result.ErrorCode != "rehearsal_unavailable" {
		t.Fatalf("no rehearser = %+v", result)
	}
}

func TestRehearsalStepIsSafeToResumeAfterARestart(t *testing.T) {
	t.Parallel()
	reconciler := &NormalizedStepReconciler{}
	decision := reconciler.Reconcile(context.Background(), ReconcileRequest{Step: &RunStep{Key: StepRehearseMigration}})
	if decision.Action != ReconcileResume {
		t.Fatalf("an interrupted rehearsal = %+v", decision)
	}
	if stepLabel(StepRehearseMigration, OperationDeploy) == "" {
		t.Fatal("the step has no label")
	}
	found := false
	for index, key := range DefaultStepKeys {
		if key == StepRehearseMigration {
			found = DefaultStepKeys[index-1] == StepBackupGate && DefaultStepKeys[index+1] == StepReleaseTask
		}
	}
	if !found {
		t.Fatalf("the rehearsal is not between the backup gate and the release task: %v", DefaultStepKeys)
	}
}

// The run's log reads one line per lock that says something: the SHARE a
// CREATE INDEX takes inside an ALTER TABLE's transaction is covered by the
// ALTER's ACCESS EXCLUSIVE; a SHARE that outlasted it is its own line.
func TestLockCoveredBySharperLockOnTheSameTable(t *testing.T) {
	held := []RehearsalHeldLock{
		{Relation: "orders", Mode: "AccessExclusiveLock", HeldMS: 480},
		{Relation: "orders", Mode: "ShareLock", HeldMS: 480},
		{Relation: "items", Mode: "ShareLock", HeldMS: 900},
		{Relation: "items", Mode: "AccessExclusiveLock", HeldMS: 100},
	}
	for _, c := range []struct {
		lock    RehearsalHeldLock
		covered bool
	}{
		{held[0], false},
		{held[1], true},
		{held[2], false},
		{held[3], false},
	} {
		if got := lockCovered(held, c.lock); got != c.covered {
			t.Errorf("%s on %s: covered = %v, want %v", c.lock.Mode, c.lock.Relation, got, c.covered)
		}
	}
}
