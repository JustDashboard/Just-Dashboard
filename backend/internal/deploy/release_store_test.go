package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	basestore "github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

type releaseStoreFixture struct {
	base      *basestore.Store
	runs      *OrchestrationStore
	variables *PlanningStore
	projectID int64
	envID     int64
	now       time.Time
}

func newReleaseStoreFixture(t *testing.T) *releaseStoreFixture {
	t.Helper()
	base, err := basestore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	sealer, err := auth.NewSealer(strings.Repeat("9a", 32))
	if err != nil {
		t.Fatal(err)
	}
	fixture := &releaseStoreFixture{base: base, now: time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)}
	result, err := base.DB.Exec(
		"INSERT INTO deploy_projects(name, profile, repo_path, branch, compose_file, pre_command, post_command, hook_secret, hook_id, enabled, created_at, updated_at) VALUES('release-fixture', 'web', '/srv/release-fixture', 'main', 'compose.yml', '', '', 'sealed', 'release-fixture-hook', 1, ?, ?)",
		fixture.now.Unix(), fixture.now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	fixture.projectID, _ = result.LastInsertId()
	result, err = base.DB.Exec(
		"INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, strategy, expected_downtime, protected, created_at, updated_at) VALUES(?, 'Production', 'production', 'production', 1, 'blue_green', 0, 1, ?, ?)",
		fixture.projectID, fixture.now.Unix(), fixture.now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	fixture.envID, _ = result.LastInsertId()
	fixture.runs = NewOrchestrationStore(base)
	fixture.runs.now = func() time.Time { return fixture.now }
	fixture.variables = NewPlanningStore(base, sealer, []string{"/srv"})
	fixture.variables.now = func() time.Time { return fixture.now }
	return fixture
}

func (f *releaseStoreFixture) addPlan(t *testing.T, revision int, sourceRevision string) {
	t.Helper()
	f.addPlanWithRuntime(t, revision, sourceRevision, RuntimePlanConfig{
		InternalPort: 3000, BindAddress: "127.0.0.1", Strategy: StrategyBlueGreen,
		Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
	})
}

func (f *releaseStoreFixture) addPlanWithRuntime(
	t *testing.T,
	revision int,
	sourceRevision string,
	runtime RuntimePlanConfig,
) {
	t.Helper()
	source := DraftSourceConfig{Kind: SourceLocal, Mode: SourceModeLocalCheckout, LocalPath: "/srv/release-fixture"}
	identity := SourceIdentity{Kind: SourceLocal, LocalPath: "/srv/release-fixture", Revision: sourceRevision}
	build := BuildPlanConfig{
		Method: BuildDockerfile, Dockerfile: "Dockerfile",
		Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{},
	}
	sourceJSON, _ := json.Marshal(source)
	identityJSON, _ := json.Marshal(identity)
	buildJSON, _ := json.Marshal(build)
	runtimeJSON, _ := json.Marshal(runtime)
	if _, err := f.base.DB.Exec(
		"INSERT INTO deploy_sources(environment_id, revision, kind, config_json, identity_json, digest, created_at) VALUES(?, ?, 'local', ?, ?, ?, ?)",
		f.envID, revision, string(sourceJSON), string(identityJSON), digestBytes(sourceJSON, identityJSON), f.now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.base.DB.Exec(
		"INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at) VALUES(?, ?, 'dockerfile', ?, '{\"candidates\":[],\"gitRequirements\":{}}', ?, ?, ?)",
		f.envID, revision, string(buildJSON), string(buildJSON), digestBytes(buildJSON), f.now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.base.DB.Exec(
		"INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at) VALUES(?, ?, ?, ?, ?, ?)",
		f.envID, revision, string(runtimeJSON), string(runtimeJSON), digestBytes(runtimeJSON), f.now.Unix()); err != nil {
		t.Fatal(err)
	}
	if revision == 1 {
		sealed, err := f.variables.sealer.Seal("release-store-super-secret")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.base.DB.Exec(
			"INSERT INTO deploy_variable_revisions(environment_id, key, revision, sensitivity, scopes, value_enc, value_digest, active, created_by, created_at) VALUES(?, 'TOKEN', 1, 'secret', 'runtime,release_task', ?, ?, 1, 'admin', ?)",
			f.envID, sealed, fakeContentDigest("release-store-super-secret"), f.now.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.base.DB.Exec("UPDATE deploy_environments SET desired_revision = ? WHERE id = ?", revision, f.envID); err != nil {
		t.Fatal(err)
	}
}

func (f *releaseStoreFixture) claimedRun(t *testing.T, revision int) (*EngineRun, QueueLease) {
	t.Helper()
	run, _, err := f.runs.Enqueue(context.Background(), RunRequest{
		ProjectID: f.projectID, EnvironmentID: f.envID,
		Operation: OperationDeploy, Trigger: TriggerManual, Actor: "admin",
		RequestDigest: fmt.Sprintf("release-%d-%d", revision, f.now.UnixNano()),
		PlanRevision:  revision, SlotClass: SlotHeavy,
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := f.runs.ClaimNext(context.Background(), "release-test-worker", QueueBudget{Heavy: 1, Light: 1}, time.Minute)
	if err != nil || lease == nil {
		t.Fatalf("claim = %#v, %v", lease, err)
	}
	claimed, err := f.runs.Run(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return claimed, *lease
}

func (f *releaseStoreFixture) candidate(t *testing.T, run EngineRun, lease QueueLease, imageDigest string) *ReleaseWithArtifacts {
	t.Helper()
	runtimeSnapshot := json.RawMessage("{\"version\":1,\"plan\":{\"strategy\":\"blue_green\"}}")
	release, err := f.runs.CreateCandidateRelease(context.Background(), run, lease.Token, CandidateReleaseInput{
		Artifacts: []ReleaseArtifactInput{{
			Kind: ArtifactImage, Reference: "example.test/app:current", Digest: imageDigest,
			Metadata: json.RawMessage("{\"os\":\"linux\",\"architecture\":\"amd64\"}"), SizeBytes: 2048,
		}},
		Prepared: PreparedBuild{
			Method: BuildDockerfile, Dockerfile: "Dockerfile",
			DockerfileDigest: fakeContentDigest("Dockerfile"),
			BuildArgv:        []string{"docker", "buildx", "build", "."},
			BaseImages:       []ResolvedImage{}, CachePolicy: "reuse", SecretIDs: []string{},
		},
		RuntimeSnapshot: runtimeSnapshot,
		RuntimeDigest:   digestBytes(runtimeSnapshot),
	})
	if err != nil {
		t.Fatal(err)
	}
	return release
}

func TestCandidateReleaseIsAtomicIdempotentImmutableAndSecretFree(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	fixture.addPlan(t, 1, strings.Repeat("a", 40))
	if _, err := fixture.base.DB.Exec(`
		INSERT INTO deploy_dependencies(
		  environment_id, release_id, kind, ownership, resource_kind, resource_id, config_json, created_at)
		VALUES(?, 0, 'cache', 'linked', 'redis', 'cache-v1', '{}', ?)`, fixture.envID, fixture.now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.base.DB.Exec(`
		INSERT INTO deploy_checks(
		  environment_id, runtime_plan_id, name, kind, phase, config_json, required, ordinal, created_at)
		VALUES(?, 0, 'ready', 'http', 'readiness', '{"path":"/v1"}', 1, 1, ?)`, fixture.envID, fixture.now.Unix()); err != nil {
		t.Fatal(err)
	}
	run, lease := fixture.claimedRun(t, 1)
	imageDigest := fakeContentDigest("image-one")
	release := fixture.candidate(t, *run, lease, imageDigest)
	if release.Release.State != "candidate" || release.Release.Number != 1 || release.Release.ExpectedDowntime ||
		release.Release.ImageDigest != imageDigest || len(release.Artifacts) != 2 {
		t.Fatalf("candidate release = %#v", release)
	}
	var runtimeArtifact *ReleaseArtifact
	for index := range release.Artifacts {
		if release.Artifacts[index].Kind == ArtifactRuntimeConfig {
			runtimeArtifact = &release.Artifacts[index]
		}
	}
	if runtimeArtifact == nil || runtimeArtifact.SizeBytes == 0 ||
		!strings.Contains(string(runtimeArtifact.Metadata), `"snapshot":{"version":1`) {
		t.Fatalf("runtime snapshot artifact = %#v", runtimeArtifact)
	}
	replayed := fixture.candidate(t, *run, lease, imageDigest)
	if replayed.Release.ID != release.Release.ID || len(replayed.Artifacts) != len(release.Artifacts) {
		t.Fatalf("idempotent release = %#v, want id %d", replayed, release.Release.ID)
	}
	if _, err := fixture.runs.CreateCandidateRelease(context.Background(), *run, lease.Token, CandidateReleaseInput{
		Artifacts: []ReleaseArtifactInput{{
			Kind: ArtifactImage, Reference: "example.test/app:current", Digest: imageDigest,
			Metadata: json.RawMessage("{\"os\":\"linux\",\"architecture\":\"arm64\"}"), SizeBytes: 2048,
		}},
		Prepared: PreparedBuild{
			Method: BuildDockerfile, Dockerfile: "Dockerfile", DockerfileDigest: fakeContentDigest("Dockerfile"),
			BuildArgv: []string{"docker", "buildx", "build", "."}, BaseImages: []ResolvedImage{},
			CachePolicy: "reuse", SecretIDs: []string{},
		},
		RuntimeSnapshot: json.RawMessage("{\"version\":1,\"plan\":{\"strategy\":\"blue_green\"}}"),
	}); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("idempotent release accepted changed artifact metadata: %v", err)
	}
	var persisted string
	if err := fixture.base.DB.QueryRow(
		"SELECT source_identity_json || provenance_json || COALESCE((SELECT group_concat(metadata_json, '') FROM deploy_release_artifacts WHERE release_id = r.id), '') FROM deploy_releases r WHERE id = ?",
		release.Release.ID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(persisted, "release-store-super-secret") {
		t.Fatalf("release metadata contains secret plaintext: %s", persisted)
	}
	if !strings.Contains(persisted, `"expectedDowntime":false`) ||
		!strings.Contains(persisted, `"planInputsDigest":"sha256:`) {
		t.Fatalf("release provenance omitted frozen runtime policy/input digest: %s", persisted)
	}
	if _, err := fixture.base.DB.Exec("UPDATE deploy_releases SET image_digest = ? WHERE id = ?",
		fakeContentDigest("rewrite"), release.Release.ID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("release identity rewrite error = %v", err)
	}
	if _, err := fixture.base.DB.Exec("UPDATE deploy_release_artifacts SET digest = ? WHERE release_id = ?",
		fakeContentDigest("rewrite"), release.Release.ID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("artifact identity rewrite error = %v", err)
	}
	if _, err := fixture.base.DB.Exec("UPDATE deploy_build_plans SET preview = 'rewritten' WHERE id = ?",
		release.Release.BuildPlanID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("build plan revision rewrite error = %v", err)
	}
	if _, err := fixture.base.DB.Exec("UPDATE deploy_variable_revisions SET value_digest = ? WHERE environment_id = ? AND key = 'TOKEN'",
		fakeContentDigest("rewrite"), fixture.envID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("variable revision rewrite error = %v", err)
	}
	if _, err := fixture.base.DB.Exec("UPDATE deploy_variable_revisions SET active = 0 WHERE environment_id = ? AND key = 'TOKEN'",
		fixture.envID); err != nil {
		t.Fatalf("allowed variable lifecycle update failed: %v", err)
	}
	if _, err := fixture.base.DB.Exec("UPDATE deploy_releases SET state = 'retained' WHERE id = ?", release.Release.ID); err != nil {
		t.Fatalf("allowed lifecycle update failed: %v", err)
	}
}

func TestQueuedRunAndRetryKeepExactVariableRevisionsAcrossRotation(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	fixture.addPlan(t, 1, strings.Repeat("a", 40))
	if _, err := fixture.base.DB.Exec(`
		INSERT INTO deploy_dependencies(
		  environment_id, release_id, kind, ownership, resource_kind, resource_id, config_json, created_at)
		VALUES(?, 0, 'cache', 'linked', 'redis', 'cache-v1', '{}', ?)`, fixture.envID, fixture.now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.base.DB.Exec(`
		INSERT INTO deploy_checks(
		  environment_id, runtime_plan_id, name, kind, phase, config_json, required, ordinal, created_at)
		VALUES(?, 0, 'ready', 'http', 'readiness', '{"path":"/v1"}', 1, 1, ?)`, fixture.envID, fixture.now.Unix()); err != nil {
		t.Fatal(err)
	}
	first, _, err := fixture.runs.Enqueue(context.Background(), RunRequest{
		ProjectID: fixture.projectID, EnvironmentID: fixture.envID,
		Operation: OperationDeploy, Trigger: TriggerManual, Actor: "admin",
		RequestDigest: "variable-snapshot-v1", PlanRevision: 1, SlotClass: SlotHeavy,
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := fixture.runs.RequestCancellation(context.Background(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	var firstRevisionID int64
	if err := fixture.base.DB.QueryRow(
		"SELECT variable_revision_id FROM deploy_run_variable_revisions WHERE run_id = ?", first.ID,
	).Scan(&firstRevisionID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.base.DB.Exec(
		"UPDATE deploy_variable_revisions SET active = 0 WHERE id = ?", firstRevisionID,
	); err != nil {
		t.Fatal(err)
	}
	secondSecret := "release-store-rotated-secret"
	sealed, err := fixture.variables.sealer.Seal(secondSecret)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.base.DB.Exec(`
		INSERT INTO deploy_variable_revisions(
		  environment_id, key, revision, sensitivity, scopes, value_enc,
		  value_digest, active, created_by, created_at)
		VALUES(?, 'TOKEN', 2, 'secret', 'runtime,release_task', ?, ?, 1, 'admin', ?)`,
		fixture.envID, sealed, digestBytes([]byte(secondSecret)), fixture.now.Add(time.Minute).Unix())
	if err != nil {
		t.Fatal(err)
	}
	secondRevisionID, _ := result.LastInsertId()
	if _, err := fixture.base.DB.Exec(
		"UPDATE deploy_dependencies SET resource_id = 'cache-v2' WHERE environment_id = ?", fixture.envID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.base.DB.Exec(
		`UPDATE deploy_checks SET config_json = '{"path":"/v2"}' WHERE environment_id = ?`, fixture.envID,
	); err != nil {
		t.Fatal(err)
	}

	retry, created, err := fixture.runs.Retry(
		context.Background(), cancelled.ID, "admin", "variable-snapshot-retry",
	)
	if err != nil || !created {
		t.Fatalf("retry = %#v, created=%v, error=%v", retry, created, err)
	}
	newRun, _, err := fixture.runs.Enqueue(context.Background(), RunRequest{
		ProjectID: fixture.projectID, EnvironmentID: fixture.envID,
		Operation: OperationDeploy, Trigger: TriggerManual, Actor: "admin",
		RequestDigest: "variable-snapshot-v2", PlanRevision: 1, SlotClass: SlotHeavy,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		runID      int64
		wantRef    int64
		wantSecret string
		wantPlanV2 bool
	}{
		{first.ID, firstRevisionID, "release-store-super-secret", false},
		{retry.ID, firstRevisionID, "release-store-super-secret", false},
		{newRun.ID, secondRevisionID, secondSecret, true},
	} {
		var refID int64
		if err := fixture.base.DB.QueryRow(
			"SELECT variable_revision_id FROM deploy_run_variable_revisions WHERE run_id = ?", check.runID,
		).Scan(&refID); err != nil {
			t.Fatal(err)
		}
		if refID != check.wantRef {
			t.Fatalf("run %d variable revision = %d, want %d", check.runID, refID, check.wantRef)
		}
		values, err := fixture.variables.OpenRunScopedVariables(
			context.Background(), check.runID, fixture.envID, "runtime",
		)
		if err != nil || len(values) != 1 || values[0].Value != check.wantSecret {
			t.Fatalf("run %d variables = %#v, error=%v", check.runID, values, err)
		}
		plan, err := fixture.runs.ExecutionPlan(context.Background(), EngineRun{
			ID: check.runID, EnvironmentID: fixture.envID, PlanRevision: 1,
		})
		if err != nil || len(plan.Variables) != 1 || plan.VariablesDigest != digestReleaseVariables(plan.Variables) {
			t.Fatalf("run %d execution variable snapshot = %#v, error=%v", check.runID, plan, err)
		}
		planJSON := string(append(append([]byte{}, plan.Dependencies[0]...), plan.Checks[0]...))
		if strings.Contains(planJSON, "v2") != check.wantPlanV2 || validateStoredExecutionPlan(plan) != nil {
			t.Fatalf("run %d dependency/check snapshot = %s, want v2=%v", check.runID, planJSON, check.wantPlanV2)
		}
	}
	if _, err := fixture.base.DB.Exec(
		"UPDATE deploy_run_variable_snapshots SET digest = ? WHERE run_id = ?",
		fakeContentDigest("rewrite"), first.ID,
	); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("run variable snapshot rewrite error = %v", err)
	}
	if _, err := fixture.base.DB.Exec(
		"UPDATE deploy_run_variable_revisions SET variable_revision_id = ? WHERE run_id = ?",
		secondRevisionID, first.ID,
	); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("run variable reference rewrite error = %v", err)
	}
	if _, err := fixture.base.DB.Exec(
		"UPDATE deploy_run_plan_snapshots SET checks_json = '[]' WHERE run_id = ?", first.ID,
	); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("run plan snapshot rewrite error = %v", err)
	}
}

func TestMovingImageIdentityCreatesDistinctReleaseAndComparison(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	fixture.addPlan(t, 1, strings.Repeat("a", 40))
	runOne, leaseOne := fixture.claimedRun(t, 1)
	first := fixture.candidate(t, *runOne, leaseOne, fakeContentDigest("tag-v1"))
	fixture.finishCandidate(t, first.Release.ID, runOne.ID, leaseOne.Token)
	fixture.now = fixture.now.Add(time.Minute)
	fixture.addPlan(t, 2, strings.Repeat("b", 40))
	runTwo, leaseTwo := fixture.claimedRun(t, 2)
	second := fixture.candidate(t, *runTwo, leaseTwo, fakeContentDigest("tag-v2"))
	if first.Release.ImageDigest == second.Release.ImageDigest || first.Release.ID == second.Release.ID || second.Release.Number != 2 {
		t.Fatalf("moving identity reused a release: first=%#v second=%#v", first.Release, second.Release)
	}
	comparison, err := fixture.runs.CompareReleases(context.Background(), first.Release.ID, second.Release.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !comparison.Changes["source"] || !comparison.Changes["build"] || comparison.Changes["runtime"] {
		t.Fatalf("release comparison = %#v", comparison.Changes)
	}
}

func TestCandidateRuntimeActivationAndRetirementAreAtomicAndImmutable(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	fixture.addPlan(t, 1, strings.Repeat("a", 40))
	firstRun, firstLease := fixture.claimedRun(t, 1)
	firstRelease := fixture.candidate(t, *firstRun, firstLease, fakeContentDigest("runtime-image-one"))
	firstRuntimeInput := ReleaseRuntimeInput{
		ReleaseID: firstRelease.Release.ID, Kind: "container", RuntimeID: "container-one",
		Name: "jd-env-1-release-1", Host: "127.0.0.1", Port: 31001,
		Metadata: json.RawMessage(`{"configDigest":"` + fakeContentDigest("runtime-config-one") + `"}`),
	}
	firstRuntime, err := fixture.runs.RecordCandidateRuntime(
		context.Background(), *firstRun, firstLease.Token, firstRuntimeInput,
	)
	if err != nil || firstRuntime.State != "candidate" {
		t.Fatalf("first candidate runtime = %#v, error=%v", firstRuntime, err)
	}
	replayed, err := fixture.runs.RecordCandidateRuntime(
		context.Background(), *firstRun, firstLease.Token, firstRuntimeInput,
	)
	if err != nil || replayed.RuntimeID != firstRuntime.RuntimeID {
		t.Fatalf("idempotent runtime = %#v, error=%v", replayed, err)
	}
	if err := fixture.runs.SetRuntimeState(context.Background(), firstRun.ID, firstLease.Token,
		firstRelease.Release.ID, "ready"); err != nil {
		t.Fatal(err)
	}
	activatedFirst, err := fixture.runs.ActivateCandidate(
		context.Background(), firstRun.ID, firstLease.Token, firstRelease.Release.ID,
	)
	if err != nil || activatedFirst.State != "live" {
		t.Fatalf("first activation = %#v, error=%v", activatedFirst, err)
	}
	fixture.finishActivatedRun(t, firstRun.ID, firstRelease.Release.ID, firstLease.Token)

	fixture.now = fixture.now.Add(time.Minute)
	fixture.addPlan(t, 2, strings.Repeat("b", 40))
	secondRun, secondLease := fixture.claimedRun(t, 2)
	secondRelease := fixture.candidate(t, *secondRun, secondLease, fakeContentDigest("runtime-image-two"))
	if secondRelease.Release.PredecessorReleaseID != firstRelease.Release.ID {
		t.Fatalf("second predecessor = %d, want %d", secondRelease.Release.PredecessorReleaseID, firstRelease.Release.ID)
	}
	if _, err := fixture.runs.RecordCandidateRuntime(context.Background(), *secondRun, secondLease.Token,
		ReleaseRuntimeInput{
			ReleaseID: secondRelease.Release.ID, Kind: "container", RuntimeID: "container-two",
			Name: "jd-env-1-release-2", Host: "127.0.0.1", Port: 31002,
			Metadata: json.RawMessage(`{"configDigest":"` + fakeContentDigest("runtime-config-two") + `"}`),
		}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.runs.SetRuntimeState(context.Background(), secondRun.ID, secondLease.Token,
		secondRelease.Release.ID, "ready"); err != nil {
		t.Fatal(err)
	}
	activatedSecond, err := fixture.runs.ActivateCandidate(
		context.Background(), secondRun.ID, secondLease.Token, secondRelease.Release.ID,
	)
	if err != nil || activatedSecond.State != "live" {
		t.Fatalf("second activation = %#v, error=%v", activatedSecond, err)
	}
	live, err := fixture.runs.LiveRelease(context.Background(), fixture.envID)
	if err != nil || live.Release.ID != secondRelease.Release.ID {
		t.Fatalf("live release = %#v, error=%v", live, err)
	}
	priorRuntime, err := fixture.runs.RuntimeForRelease(context.Background(), firstRelease.Release.ID)
	if err != nil || priorRuntime.State != "draining" {
		t.Fatalf("prior runtime after cutover = %#v, error=%v", priorRuntime, err)
	}
	retiredID, err := fixture.runs.MarkPreviousRetired(
		context.Background(), secondRun.ID, secondLease.Token, secondRelease.Release.ID,
	)
	if err != nil || retiredID != firstRelease.Release.ID {
		t.Fatalf("retired predecessor = %d, error=%v", retiredID, err)
	}
	priorRuntime, err = fixture.runs.RuntimeForRelease(context.Background(), firstRelease.Release.ID)
	if err != nil || priorRuntime.State != "retired" {
		t.Fatalf("retired runtime = %#v, error=%v", priorRuntime, err)
	}
	if _, err := fixture.base.DB.Exec(
		"UPDATE deploy_release_runtimes SET runtime_id = 'rewritten' WHERE release_id = ?", firstRelease.Release.ID,
	); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("runtime identity rewrite error = %v", err)
	}
}

func TestCloneCandidateReleaseReusesOnlyAvailableImmutableArtifacts(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	fixture.addPlan(t, 1, strings.Repeat("a", 40))
	firstRun, firstLease := fixture.claimedRun(t, 1)
	first := fixture.candidate(t, *firstRun, firstLease, fakeContentDigest("clone-image"))
	if _, err := fixture.runs.RecordCandidateRuntime(context.Background(), *firstRun, firstLease.Token,
		ReleaseRuntimeInput{
			ReleaseID: first.Release.ID, Kind: "container", RuntimeID: "clone-source-runtime",
			Host: "127.0.0.1", Port: 31111, Metadata: json.RawMessage(`{"version":1}`),
		}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.runs.ActivateCandidate(context.Background(), firstRun.ID, firstLease.Token, first.Release.ID); err != nil {
		t.Fatal(err)
	}
	fixture.finishActivatedRun(t, firstRun.ID, first.Release.ID, firstLease.Token)

	fixture.now = fixture.now.Add(time.Minute)
	cloneRun, _, err := fixture.runs.Enqueue(context.Background(), RunRequest{
		ProjectID: fixture.projectID, EnvironmentID: fixture.envID,
		Operation: OperationRedeploy, Trigger: TriggerManual, Actor: "admin",
		RequestDigest: "clone-release-run", PlanRevision: 1,
		VariableSnapshotRunID: firstRun.ID, SlotClass: SlotHeavy,
	})
	if err != nil {
		t.Fatal(err)
	}
	cloneLease, err := fixture.runs.ClaimNext(context.Background(), "clone-worker", QueueBudget{}, time.Minute)
	if err != nil || cloneLease == nil {
		t.Fatalf("clone lease = %#v, error=%v", cloneLease, err)
	}
	claimed, err := fixture.runs.Run(context.Background(), cloneRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	clone, err := fixture.runs.CloneCandidateRelease(
		context.Background(), *claimed, cloneLease.Token, first.Release.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if clone.Release.ID == first.Release.ID || clone.Release.PredecessorReleaseID != first.Release.ID ||
		clone.Release.ImageDigest != first.Release.ImageDigest || clone.Release.ConfigDigest != first.Release.ConfigDigest ||
		len(clone.Artifacts) != len(first.Artifacts) {
		t.Fatalf("cloned release = %#v; source = %#v", clone, first)
	}
	if _, err := fixture.base.DB.Exec(
		`UPDATE deploy_release_artifacts SET state = 'pruned' WHERE release_id = ? AND kind = 'image'`, first.Release.ID,
	); err != nil {
		t.Fatal(err)
	}
	fixture.now = fixture.now.Add(time.Minute)
	otherRun, _, err := fixture.runs.Enqueue(context.Background(), RunRequest{
		ProjectID: fixture.projectID, EnvironmentID: fixture.envID,
		Operation: OperationRedeploy, Trigger: TriggerManual, Actor: "admin",
		RequestDigest: "clone-pruned-run", PlanRevision: 1,
		VariableSnapshotRunID: firstRun.ID, SlotClass: SlotHeavy,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Finish the synthetic clone run so environment serialization permits the
	// next claim; activation itself is not relevant to the artifact guard.
	fixture.finishActivatedRun(t, cloneRun.ID, clone.Release.ID, cloneLease.Token)
	otherLease, err := fixture.runs.ClaimNext(context.Background(), "clone-pruned-worker", QueueBudget{}, time.Minute)
	if err != nil || otherLease == nil || otherLease.RunID != otherRun.ID {
		t.Fatalf("pruned clone lease = %#v, error=%v", otherLease, err)
	}
	otherClaimed, _ := fixture.runs.Run(context.Background(), otherRun.ID)
	if _, err := fixture.runs.CloneCandidateRelease(context.Background(), *otherClaimed, otherLease.Token, first.Release.ID); !errors.Is(err, ErrArtifactMissing) {
		t.Fatalf("clone with pruned image error = %v", err)
	}
}

func (f *releaseStoreFixture) finishActivatedRun(t *testing.T, runID, releaseID int64, claimToken string) {
	t.Helper()
	if _, err := f.base.DB.Exec(
		"UPDATE deploy_runs SET state = 'succeeded', status = 'success', ended_at = ?, release_id = ? WHERE id = ?",
		f.now.Unix(), releaseID, runID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := f.base.DB.Exec(
		"DELETE FROM deploy_queue_leases WHERE run_id = ? AND claim_token = ?", runID, claimToken,
	); err != nil {
		t.Fatal(err)
	}
}

func TestCandidatePortLeaseIsLoopbackScopedAndReleasedByToken(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	fixture.addPlan(t, 1, strings.Repeat("a", 40))
	run, lease := fixture.claimedRun(t, 1)
	portLease, err := fixture.runs.AcquirePortLease(
		context.Background(), run.ID, fixture.envID, lease.Token, "127.0.0.1", 0, time.Minute,
	)
	if err != nil || portLease.Port == 0 || portLease.Token == "" {
		t.Fatalf("port lease = %#v, error=%v", portLease, err)
	}
	if _, err := fixture.runs.AcquirePortLease(
		context.Background(), run.ID, fixture.envID, lease.Token, "0.0.0.0", 0, time.Minute,
	); !errors.Is(err, ErrPortUnavailable) {
		t.Fatalf("public candidate lease error = %v", err)
	}
	if err := fixture.runs.ReleasePortLease(context.Background(), run.ID, portLease.Token); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := fixture.base.DB.QueryRow(
		"SELECT COUNT(*) FROM deploy_port_leases WHERE id = ?", portLease.ID,
	).Scan(&count); err != nil || count != 0 {
		t.Fatalf("released port lease count = %d, error=%v", count, err)
	}
}

func TestReleaseBuildFingerprintIncludesEveryComposeService(t *testing.T) {
	t.Parallel()
	first := []ReleaseArtifact{
		{Kind: ArtifactImage, Reference: "example/api:current", Digest: fakeContentDigest("api-v1")},
		{Kind: ArtifactImage, Reference: "example/worker:current", Digest: fakeContentDigest("worker-v1")},
		{Kind: ArtifactCompose, Reference: "compose", Digest: fakeContentDigest("compose-v1")},
	}
	second := append([]ReleaseArtifact(nil), first...)
	second[1].Digest = fakeContentDigest("worker-v2")
	if releaseBuildFingerprint(first) == releaseBuildFingerprint(second) {
		t.Fatal("a moving non-primary Compose service tag was omitted from release comparison")
	}
}

func (f *releaseStoreFixture) finishCandidate(t *testing.T, releaseID, runID int64, claimToken string) {
	t.Helper()
	if _, err := f.base.DB.Exec("UPDATE deploy_releases SET state = 'live', activated_at = ? WHERE id = ?", f.now.Unix(), releaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.base.DB.Exec("UPDATE deploy_environments SET live_release_id = ? WHERE id = ?", releaseID, f.envID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.base.DB.Exec("UPDATE deploy_runs SET state = 'succeeded', status = 'success', ended_at = ?, release_id = ? WHERE id = ?",
		f.now.Unix(), releaseID, runID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.base.DB.Exec("DELETE FROM deploy_queue_leases WHERE run_id = ? AND claim_token = ?", runID, claimToken); err != nil {
		t.Fatal(err)
	}
}

type retentionRemover struct{ removed []int64 }

func (r *retentionRemover) RemoveArtifact(_ context.Context, artifact ReleaseArtifact) error {
	r.removed = append(r.removed, artifact.ID)
	return nil
}

func TestArtifactRetentionProtectsRollbackSetPinsSharedDigestsAndLeases(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	sharedDigest := fakeContentDigest("shared")
	releaseIDs := []int64{}
	for number := 1; number <= 9; number++ {
		digest := fakeContentDigest(fmt.Sprintf("image-%d", number))
		reference := fmt.Sprintf("example.test/app:release-%d", number)
		if number == 1 || number == 9 {
			digest = sharedDigest
		}
		result, err := fixture.base.DB.Exec(
			"INSERT INTO deploy_releases(project_id, environment_id, release_number, state, plan_revision, source_revision, source_identity_json, image_digest, config_digest, variables_digest, strategy, provenance_json, created_at, pinned) VALUES(?, ?, ?, 'retained', 1, ?, '{}', ?, ?, ?, 'blue_green', '{}', ?, ?)",
			fixture.projectID, fixture.envID, number, strings.Repeat(fmt.Sprint(number%10), 40),
			digest, fakeContentDigest("config"), fakeContentDigest("variables"),
			fixture.now.Add(time.Duration(number-9)*time.Hour).Unix(), boolInt(number == 2))
		if err != nil {
			t.Fatal(err)
		}
		releaseID, _ := result.LastInsertId()
		releaseIDs = append(releaseIDs, releaseID)
		if _, err := fixture.base.DB.Exec(
			"INSERT INTO deploy_release_artifacts(release_id, kind, reference, digest, metadata_json, size_bytes, state, created_at) VALUES(?, 'image', ?, ?, '{}', 100, 'available', ?)",
			releaseID, reference, digest, fixture.now.Add(time.Duration(number-9)*time.Hour).Unix()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.base.DB.Exec("UPDATE deploy_releases SET state = 'live' WHERE id = ?", releaseIDs[8]); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.base.DB.Exec("UPDATE deploy_environments SET live_release_id = ? WHERE id = ?", releaseIDs[8], fixture.envID); err != nil {
		t.Fatal(err)
	}
	decisions, err := fixture.runs.ArtifactRetentionPlan(context.Background(), fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	byRelease := map[int64]ArtifactRetentionDecision{}
	for _, decision := range decisions {
		byRelease[decision.Artifact.ReleaseID] = decision
	}
	if !byRelease[releaseIDs[8]].Retain || !strings.Contains(byRelease[releaseIDs[8]].Reason, "live") {
		t.Fatalf("live decision = %#v", byRelease[releaseIDs[8]])
	}
	if !byRelease[releaseIDs[1]].Retain || !strings.Contains(byRelease[releaseIDs[1]].Reason, "pin") {
		t.Fatalf("pin decision = %#v", byRelease[releaseIDs[1]])
	}
	if byRelease[releaseIDs[0]].Retain {
		t.Fatalf("old shared release should initially be a prune candidate: %#v", byRelease[releaseIDs[0]])
	}
	remover := &retentionRemover{}
	report, err := fixture.runs.PruneArtifacts(context.Background(), remover)
	if err != nil {
		t.Fatal(err)
	}
	if report.Pruned != 1 || report.Reclaimed != 100 || len(remover.removed) != 1 ||
		remover.removed[0] != byRelease[releaseIDs[2]].Artifact.ID {
		t.Fatalf("prune report = %#v / removed %#v", report, remover.removed)
	}
	protected := false
	for _, decision := range report.Decisions {
		if decision.Artifact.ReleaseID == releaseIDs[0] {
			protected = decision.Retain && strings.Contains(decision.Reason, "shared")
		}
	}
	if !protected {
		t.Fatalf("shared digest reason missing: %#v", report.Decisions)
	}
	var prunedState string
	if err := fixture.base.DB.QueryRow(
		"SELECT state FROM deploy_release_artifacts WHERE id = ?", byRelease[releaseIDs[2]].Artifact.ID,
	).Scan(&prunedState); err != nil || prunedState != "pruned" {
		t.Fatalf("old unshared artifact state = %q, error=%v", prunedState, err)
	}

	fixture.addPlan(t, 1, strings.Repeat("a", 40))
	_, lease := fixture.claimedRun(t, 1)
	if err := fixture.runs.reserveArtifactPrune(context.Background(), byRelease[releaseIDs[0]].Artifact.ID); !errors.Is(err, ErrArtifactRetained) {
		t.Fatalf("active lease prune error = %v (lease %#v)", err, lease)
	}
}

// SetReleasePinned is the only way to set deploy_releases.pinned; the
// retention planner already reads that column (see the pin case in
// ArtifactRetentionPlan above), so pinning through the store method is enough
// to make a release that would otherwise be pruned survive a prune plan.
func TestSetReleasePinnedSurvivesAPrunePlan(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	// A failed, month-old release is outside every other retention rule (not
	// live, not a rollback candidate, well past the seven-day diagnostic
	// window), so pinning is the only thing that can still save it.
	result, err := fixture.base.DB.Exec(
		"INSERT INTO deploy_releases(project_id, environment_id, release_number, state, plan_revision, config_digest, variables_digest, strategy, created_at) VALUES(?, ?, 1, 'failed', 1, ?, ?, 'blue_green', ?)",
		fixture.projectID, fixture.envID, fakeContentDigest("config"), fakeContentDigest("variables"),
		fixture.now.Add(-30*24*time.Hour).Unix())
	if err != nil {
		t.Fatal(err)
	}
	releaseID, _ := result.LastInsertId()
	if _, err := fixture.base.DB.Exec(
		"INSERT INTO deploy_release_artifacts(release_id, kind, reference, digest, metadata_json, size_bytes, state, created_at) VALUES(?, 'image', 'example.test/app:old', ?, '{}', 100, 'available', ?)",
		releaseID, fakeContentDigest("old-image"), fixture.now.Add(-30*24*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}

	unpinned, err := fixture.runs.ArtifactRetentionPlan(context.Background(), fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	if len(unpinned) != 1 || unpinned[0].Retain {
		t.Fatalf("old unpinned release retention = %#v, want a prune candidate", unpinned)
	}

	updated, err := fixture.runs.SetReleasePinned(context.Background(), fixture.projectID, fixture.envID, releaseID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Pinned {
		t.Fatalf("SetReleasePinned result = %#v, want Pinned", updated)
	}

	pinned, err := fixture.runs.ArtifactRetentionPlan(context.Background(), fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	if len(pinned) != 1 || !pinned[0].Retain || !strings.Contains(pinned[0].Reason, "pin") {
		t.Fatalf("pinned release retention = %#v, want retained for the operator pin", pinned)
	}

	// A release outside the project/environment it was asked for is refused,
	// not silently pinned.
	if _, err := fixture.runs.SetReleasePinned(context.Background(), fixture.projectID+1, fixture.envID, releaseID, true); !errors.Is(err, ErrArtifactMissing) {
		t.Fatalf("cross-project pin error = %v, want ErrArtifactMissing", err)
	}

	if _, err := fixture.runs.SetReleasePinned(context.Background(), fixture.projectID, fixture.envID, releaseID, false); err != nil {
		t.Fatal(err)
	}
	unpinnedAgain, err := fixture.runs.ArtifactRetentionPlan(context.Background(), fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	if len(unpinnedAgain) != 1 || unpinnedAgain[0].Retain {
		t.Fatalf("unpinned release retention = %#v, want a prune candidate again", unpinnedAgain)
	}
}

// reserveArtifactPrune is PruneArtifacts's live re-check, run once per
// artifact right before it is actually removed — the safety net for a release
// pinned in the window between ArtifactRetentionPlan's read and this
// artifact's own turn in the prune loop. Its retained EXISTS used to join the
// target release (aliased tr) without ever reading tr.pinned, checking only
// whether some *other* release sharing the same physical artifact was
// retained; a release pinned by itself, sharing its image with nothing, was
// not protected at all.
func TestReserveArtifactPruneProtectsTheTargetReleasesOwnPin(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	// A failed, month-old release is outside every other retention rule (not
	// live, not a rollback candidate, well past the seven-day diagnostic
	// window), so pinning is the only thing that can still save it — and
	// nothing else shares its digest, so the "shared release" branch of the
	// query can't be what saves it either.
	result, err := fixture.base.DB.Exec(
		"INSERT INTO deploy_releases(project_id, environment_id, release_number, state, plan_revision, config_digest, variables_digest, strategy, created_at) VALUES(?, ?, 1, 'failed', 1, ?, ?, 'blue_green', ?)",
		fixture.projectID, fixture.envID, fakeContentDigest("config"), fakeContentDigest("variables"),
		fixture.now.Add(-30*24*time.Hour).Unix())
	if err != nil {
		t.Fatal(err)
	}
	releaseID, _ := result.LastInsertId()
	artifact, err := fixture.base.DB.Exec(
		"INSERT INTO deploy_release_artifacts(release_id, kind, reference, digest, metadata_json, size_bytes, state, created_at) VALUES(?, 'image', 'example.test/app:solo', ?, '{}', 100, 'available', ?)",
		releaseID, fakeContentDigest("solo-image"), fixture.now.Add(-30*24*time.Hour).Unix())
	if err != nil {
		t.Fatal(err)
	}
	artifactID, _ := artifact.LastInsertId()

	before, err := fixture.runs.ArtifactRetentionPlan(context.Background(), fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 || before[0].Retain {
		t.Fatalf("unpinned release retention = %#v, want a prune candidate", before)
	}

	// The operator pins the release after that plan was read — a concurrent
	// request racing an in-progress prune — before this artifact's own turn
	// in the prune loop reaches reserveArtifactPrune.
	if _, err := fixture.runs.SetReleasePinned(context.Background(), fixture.projectID, fixture.envID, releaseID, true); err != nil {
		t.Fatal(err)
	}

	if err := fixture.runs.reserveArtifactPrune(context.Background(), artifactID); !errors.Is(err, ErrArtifactRetained) {
		t.Fatalf("reserveArtifactPrune on a now-pinned, unshared release = %v, want ErrArtifactRetained", err)
	}
	var state string
	if err := fixture.base.DB.QueryRow("SELECT state FROM deploy_release_artifacts WHERE id = ?", artifactID).Scan(&state); err != nil || state != "available" {
		t.Fatalf("pinned artifact state = %q, error=%v, want still available", state, err)
	}

	// PruneArtifacts itself now sees the pin directly at the plan level too
	// (ArtifactRetentionPlan reads pinned fresh), so the end-to-end call keeps
	// the artifact and says why.
	report, err := fixture.runs.PruneArtifacts(context.Background(), &retentionRemover{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Pruned != 0 || len(report.Decisions) != 1 || !report.Decisions[0].Retain ||
		!strings.Contains(report.Decisions[0].Reason, "pin") {
		t.Fatalf("prune report over a pinned solo release = %#v", report)
	}
}

// A run that fails after render_runtime must not leave its release at
// "candidate" forever: ArtifactRetentionPlan never applies its failed-artifact
// window to a candidate, so the release and its artifacts would be kept
// indefinitely and the releases list would show a stale in-progress build.
func TestFailedRunFailsItsCandidateReleaseAndRetentionKeepsItSevenDays(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	fixture.addPlan(t, 1, strings.Repeat("a", 40))
	run, lease := fixture.claimedRun(t, 1)
	release := fixture.candidate(t, *run, lease, fakeContentDigest("failed-image"))
	if release.Release.State != "candidate" {
		t.Fatalf("release state before failure = %s, want candidate", release.Release.State)
	}
	if _, err := fixture.runs.TransitionRun(context.Background(), run.ID, lease.Token,
		RunFailed, TransitionDetail{Code: "build_artifact_failed", Reason: "test failure"}); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := fixture.base.DB.QueryRow(
		"SELECT state FROM deploy_releases WHERE id = ?", release.Release.ID,
	).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "failed" {
		t.Fatalf("release state after failed run = %s, want failed", state)
	}

	decisions, err := fixture.runs.ArtifactRetentionPlan(context.Background(), fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, decision := range decisions {
		if decision.Artifact.ReleaseID != release.Release.ID {
			continue
		}
		found = true
		if !decision.Retain || !strings.Contains(decision.Reason, "seven-day") {
			t.Fatalf("failed-release retention decision = %#v", decision)
		}
	}
	if !found {
		t.Fatal("failed release produced no retention decision")
	}
	// Eight days later the same failed artifact has aged out of the window.
	later, err := fixture.runs.ArtifactRetentionPlan(context.Background(), fixture.now.Add(8*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, decision := range later {
		if decision.Artifact.ReleaseID == release.Release.ID && decision.Retain {
			t.Fatalf("failed release retained past its seven-day window: %#v", decision)
		}
	}
}
