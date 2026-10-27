package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// newStagingFixture is a production environment shaped like the ones staging
// has to be careful with: a secret, a plain value, a reference to production's
// database, aliases to both, a linked database, a domain, a fixed host port,
// a writable volume, a read-only bind and a sign-in for previews only.
func newStagingFixture(t *testing.T) *releaseStoreFixture {
	t.Helper()
	f := newReleaseStoreFixture(t)
	f.addPlanWithRuntime(t, 1, strings.Repeat("a", 40), RuntimePlanConfig{
		InternalPort: 3000, BindAddress: "127.0.0.1", Strategy: StrategyStopFirst, HostPort: 8080,
		Command: []string{}, Capabilities: []string{}, Devices: []string{},
		Mounts: []RuntimeMount{
			{Source: "app-data", Target: "/data"},
			{Source: "/srv/release-fixture/config", Target: "/config", ReadOnly: true},
		},
		ForwardAuth: &RouteForwardAuth{Provider: "authelia", Verify: "http://authelia:9091/api/verify", SignIn: "https://auth.example.com", PreviewsOnly: true},
	})
	for _, variable := range []struct{ name, value, sensitivity, mode string }{
		{"PLAIN", "plain-value", "plain", "literal"},
		{"DATABASE_URL", "${{database.main}}", "secret", "reference"},
		{"ALIAS", "${{variable.PLAIN}}", "plain", "reference"},
		{"TOKEN_ALIAS", "${{variable.TOKEN}}", "secret", "reference"},
	} {
		sealed, err := f.variables.sealer.Seal(variable.value)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.base.DB.Exec(
			"INSERT INTO deploy_variable_revisions(environment_id, key, revision, sensitivity, scopes, value_enc, value_digest, active, created_by, created_at, value_mode) VALUES(?, ?, 1, ?, 'runtime', ?, ?, 1, 'admin', ?, ?)",
			f.envID, variable.name, variable.sensitivity, sealed, digestBytes([]byte(variable.value)), f.now.Unix(), variable.mode); err != nil {
			t.Fatal(err)
		}
	}
	for _, dependency := range []struct{ kind, resourceKind, resourceID, config string }{
		{"database", "database_connection", "main", `{}`},
		{"domain", "proxy_site", "app.example.com", `{"hostname":"app.example.com","https":true}`},
	} {
		if _, err := f.base.DB.Exec(
			"INSERT INTO deploy_dependencies(environment_id, release_id, kind, ownership, resource_kind, resource_id, config_json, created_at) VALUES(?, 0, ?, 'managed', ?, ?, ?, ?)",
			f.envID, dependency.kind, dependency.resourceKind, dependency.resourceID, dependency.config, f.now.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.base.DB.Exec(
		"INSERT INTO deploy_checks(environment_id, runtime_plan_id, name, kind, phase, config_json, required, ordinal, created_at) VALUES(?, 0, 'home', 'http', 'readiness', ?, 1, 0, ?)",
		f.envID, `{"url":"https://app.example.com/healthz","expectedStatus":[200]}`, f.now.Unix()); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestStagingCopiesConfigurationButNotSecretsDatabaseLinksOrProductionStorage(t *testing.T) {
	t.Parallel()
	f := newStagingFixture(t)
	ctx := context.Background()

	if _, err := f.variables.CreateStagingEnvironment(ctx, f.projectID, StagingRequest{Domain: "app.example.com", Actor: "admin"}); !errors.Is(err, ErrStagingDomainTaken) {
		t.Fatalf("production's own domain error = %v", err)
	}
	if _, err := f.variables.CreateStagingEnvironment(ctx, f.projectID, StagingRequest{Ref: "staging", Revision: strings.Repeat("b", 40), Actor: "admin"}); !errors.Is(err, ErrRefNotApplicable) {
		t.Fatalf("a branch for a local checkout error = %v", err)
	}
	copied, err := f.variables.CreateStagingEnvironment(ctx, f.projectID, StagingRequest{Domain: "Staging.Example.com", Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	staging := copied.Environment.ID
	if copied.Environment.Kind != EnvironmentStaging || copied.Environment.Slug != "staging" {
		t.Fatalf("environment = %#v", copied.Environment)
	}
	if !slices.Equal(copied.Copied, []string{"PLAIN", "ALIAS"}) {
		t.Fatalf("copied = %v", copied.Copied)
	}
	skipped := map[string]string{}
	for _, item := range copied.Skipped {
		skipped[item.Name] = item.Reason
	}
	for _, name := range []string{"TOKEN", "DATABASE_URL", "TOKEN_ALIAS", "database_connection main"} {
		if skipped[name] == "" {
			t.Errorf("%s should have been left behind, skipped = %#v", name, copied.Skipped)
		}
	}
	if _, err := f.variables.CreateStagingEnvironment(ctx, f.projectID, StagingRequest{Actor: "admin"}); !errors.Is(err, ErrStagingExists) {
		t.Fatalf("second staging error = %v", err)
	}

	var runtimeJSON, sourceJSON string
	if err := f.base.DB.QueryRow("SELECT r.config_json, s.config_json FROM deploy_runtime_plans r JOIN deploy_sources s ON s.environment_id = r.environment_id AND s.revision = r.revision WHERE r.environment_id = ? AND r.revision = 1", staging).
		Scan(&runtimeJSON, &sourceJSON); err != nil {
		t.Fatal(err)
	}
	var runtime RuntimePlanConfig
	if err := json.Unmarshal([]byte(runtimeJSON), &runtime); err != nil {
		t.Fatal(err)
	}
	if runtime.HostPort != 0 || len(runtime.Ports) != 0 {
		t.Errorf("staging kept production's host port: %#v", runtime)
	}
	for _, mount := range runtime.Mounts {
		if !strings.HasPrefix(mount.Source, stagingVolumePrefix(staging)) || mount.Ownership != OwnershipManaged {
			t.Errorf("staging mounts production's storage: %#v", mount)
		}
	}
	if runtime.ForwardAuth == nil || runtime.ForwardAuth.PreviewsOnly || runtime.SignInGate() == nil {
		t.Errorf("a sign-in for previews only should gate staging: %#v", runtime.ForwardAuth)
	}
	if runtime.InternalPort != 3000 || runtime.Strategy != StrategyStopFirst {
		t.Errorf("runtime plan was not copied: %#v", runtime)
	}
	var domains []string
	rows, err := f.base.DB.Query("SELECT kind || ':' || resource_id FROM deploy_dependencies WHERE environment_id = ? ORDER BY id", staging)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var value string
		_ = rows.Scan(&value)
		domains = append(domains, value)
	}
	rows.Close()
	if !slices.Equal(domains, []string{"domain:staging.example.com"}) {
		t.Fatalf("staging dependencies = %v", domains)
	}
	var checkConfig string
	if err := f.base.DB.QueryRow("SELECT config_json FROM deploy_checks WHERE environment_id = ?", staging).Scan(&checkConfig); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(checkConfig, "app.example.com") || !strings.Contains(checkConfig, "/healthz") {
		t.Fatalf("staging's check still exercises production: %s", checkConfig)
	}
	policy, err := f.runs.GitDeploymentPolicy(ctx, f.projectID, staging)
	if err != nil || !policy.Automatic || policy.PromotionOnly || policy.Revision != 1 {
		t.Fatalf("staging policy = %#v, %v", policy, err)
	}
	values, err := f.variables.OpenRunScopedVariables(ctx, 0, staging, "runtime")
	if err == nil {
		for _, value := range values {
			if value.Value == "release-store-super-secret" {
				t.Fatal("production's secret reached staging")
			}
		}
	}
	environments, err := f.runs.ProjectEnvironments(ctx, f.projectID)
	if err != nil || len(environments) != 2 || environments[0].Kind != EnvironmentProduction || environments[1].ID != staging {
		t.Fatalf("environments = %#v, %v", environments, err)
	}
}

func TestStagingCopiesSecretsOnlyWhenAskedAndRefusesWhatCannotRunTwice(t *testing.T) {
	t.Parallel()
	f := newStagingFixture(t)
	copied, err := f.variables.CreateStagingEnvironment(context.Background(), f.projectID, StagingRequest{CopySecrets: true, Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(copied.Copied, []string{"PLAIN", "TOKEN", "ALIAS", "TOKEN_ALIAS"}) {
		t.Fatalf("copied with secrets = %v", copied.Copied)
	}
	for _, item := range copied.Skipped {
		if item.Name == "TOKEN" || item.Name == "TOKEN_ALIAS" {
			t.Fatalf("an asked-for secret was skipped: %#v", item)
		}
	}

	compose := newReleaseStoreFixture(t)
	compose.addPlan(t, 1, strings.Repeat("a", 40))
	for _, statement := range []string{
		"INSERT INTO deploy_sources(environment_id, revision, kind, config_json, credential_id, identity_json, digest, created_at) SELECT environment_id, 2, kind, config_json, credential_id, identity_json, digest, created_at FROM deploy_sources WHERE environment_id = ? AND revision = 1",
		"INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at) SELECT environment_id, 2, 'compose', config_json, evidence_json, preview, digest, created_at FROM deploy_build_plans WHERE environment_id = ? AND revision = 1",
		"INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at) SELECT environment_id, 2, config_json, preview, digest, created_at FROM deploy_runtime_plans WHERE environment_id = ? AND revision = 1",
		"UPDATE deploy_environments SET desired_revision = 2 WHERE id = ?",
	} {
		if _, err := compose.base.DB.Exec(statement, compose.envID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := compose.variables.CreateStagingEnvironment(context.Background(), compose.projectID, StagingRequest{Actor: "admin"}); !errors.Is(err, ErrStagingUnsupported) {
		t.Fatalf("Compose staging error = %v", err)
	}
}

// stagedRelease builds and activates one staging release whose image is
// imageID, the way a successful staging run leaves it.
func stagedRelease(t *testing.T, f *releaseStoreFixture, staging int64, imageID string) *ReleaseWithArtifacts {
	t.Helper()
	ctx := context.Background()
	run, _, err := f.runs.Enqueue(ctx, RunRequest{
		ProjectID: f.projectID, EnvironmentID: staging, Operation: OperationDeploy, Trigger: TriggerManual,
		Actor: "admin", RequestDigest: fmt.Sprintf("staging-%s", imageID), PlanRevision: 1, SlotClass: SlotHeavy,
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := f.runs.ClaimNext(ctx, "staging-worker", QueueBudget{Heavy: 1, Light: 1}, time.Minute)
	if err != nil || lease == nil || lease.RunID != run.ID {
		t.Fatalf("claim = %#v, %v", lease, err)
	}
	claimed, _ := f.runs.Run(ctx, run.ID)
	plan, err := f.runs.ExecutionPlan(ctx, *claimed)
	if err != nil {
		t.Fatal(err)
	}
	tag := releaseImageTag(staging, run.ID)
	image := ResolvedImage{Reference: tag, Digest: imageID, ConfigDigest: imageID, OS: "linux", Architecture: "amd64", SizeBytes: 4096}
	snapshot, _ := json.Marshal(runtimeReleaseSnapshot{
		Version: 1, Plan: plan.Runtime, Image: image, Variables: plan.Variables,
		Dependencies: []PlannedDependency{}, Checks: []PlannedCheck{}, Domains: []PlannedDomain{},
		SourceIdentity: plan.SourceIdentity, ProxyTrust: []string{"FORWARDED_ALLOW_IPS"}, WebConcurrency: true,
	})
	release, err := f.runs.CreateCandidateRelease(ctx, *claimed, lease.Token, CandidateReleaseInput{
		Artifacts:       []ReleaseArtifactInput{imageArtifact(image, PreparedBuild{Method: BuildDockerfile})},
		Prepared:        PreparedBuild{Method: BuildDockerfile, Dockerfile: "Dockerfile"},
		RuntimeSnapshot: snapshot, RuntimeDigest: digestBytes(snapshot),
	})
	if err != nil {
		t.Fatal(err)
	}
	activateFixtureRelease(t, f, staging, release.Release.ID, run.ID, lease.Token)
	release, err = f.runs.Release(ctx, release.Release.ID)
	if err != nil {
		t.Fatal(err)
	}
	return release
}

func activateFixtureRelease(t *testing.T, f *releaseStoreFixture, environmentID, releaseID, runID int64, claimToken string) {
	t.Helper()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{"UPDATE deploy_releases SET state = 'retained' WHERE environment_id = ? AND state = 'live'", []any{environmentID}},
		{"UPDATE deploy_releases SET state = 'live', activated_at = ? WHERE id = ?", []any{f.now.Unix(), releaseID}},
		{"UPDATE deploy_environments SET live_release_id = ? WHERE id = ?", []any{releaseID, environmentID}},
		{"UPDATE deploy_runs SET state = 'succeeded', status = 'success', ended_at = ?, release_id = ? WHERE id = ?", []any{f.now.Unix(), releaseID, runID}},
		{"DELETE FROM deploy_queue_leases WHERE run_id = ? AND claim_token = ?", []any{runID, claimToken}},
	} {
		if _, err := f.base.DB.Exec(statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}

// enqueuePromotion queues what POST …/promote queues.
func enqueuePromotion(t *testing.T, f *releaseStoreFixture, releaseID int64) (*EngineRun, error) {
	t.Helper()
	source, err := f.runs.PromotionSource(context.Background(), f.projectID, releaseID)
	if err != nil {
		return nil, err
	}
	metadata, _ := json.Marshal(map[string]any{"targetReleaseId": 0, "promotion": source.Promotion})
	run, _, err := f.runs.Enqueue(context.Background(), RunRequest{
		ProjectID: f.projectID, EnvironmentID: f.envID, Operation: OperationPromote, Trigger: TriggerManual,
		Actor: "admin", RequestDigest: fmt.Sprintf("promote-%d-%d", releaseID, time.Now().UnixNano()),
		PlanRevision: 1, SlotClass: SlotLight, Metadata: metadata, Steps: PromotionStepKeys,
	})
	return run, err
}

func TestPromotionRendersStagingsExactImageWithProductionsConfiguration(t *testing.T) {
	t.Parallel()
	f := newStagingFixture(t)
	ctx := context.Background()
	created, err := f.variables.CreateStagingEnvironment(ctx, f.projectID, StagingRequest{Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	staging := created.Environment.ID
	imageID := fakeContentDigest("staging-image")
	source := stagedRelease(t, f, staging, imageID)

	run, err := enqueuePromotion(t, f, source.Release.ID)
	if err != nil {
		t.Fatal(err)
	}
	steps, _ := f.runs.Steps(ctx, run.ID)
	for _, step := range steps {
		switch step.Key {
		case StepResolveSource, StepAcquireSource, StepAnalyzePlan, StepPrepareContext, StepBuildArtifact:
			t.Fatalf("a promotion has a build step: %s", step.Key)
		}
	}
	if len(steps) != len(PromotionStepKeys) || steps[0].Key != StepRenderRuntime {
		t.Fatalf("promotion steps = %#v", steps)
	}
	lease, err := f.runs.ClaimNext(ctx, "production-worker", QueueBudget{Heavy: 1, Light: 1}, time.Minute)
	if err != nil || lease == nil || lease.RunID != run.ID {
		t.Fatalf("claim = %#v, %v", lease, err)
	}
	claimed, _ := f.runs.Run(ctx, run.ID)
	plan, err := f.runs.ExecutionPlan(ctx, *claimed)
	if err != nil {
		t.Fatal(err)
	}
	var stagingIdentity SourceIdentity
	_ = json.Unmarshal(source.Release.SourceIdentity, &stagingIdentity)
	if plan.SourceIdentity.Revision != stagingIdentity.Revision || plan.SourceIdentity.LocalPath != stagingIdentity.LocalPath {
		t.Fatalf("promotion executes production's identity: %#v", plan.SourceIdentity)
	}

	backend := &artifactBackendFake{inspected: map[string]ResolvedImage{}, local: map[string]string{imageID: imageID}}
	executor := &NormalizedStepExecutor{store: f.runs, variables: f.variables, builder: NewArtifactBuilder(backend)}
	output := &recordingStepOutput{}
	missing := executor.renderRuntime(ctx, StepExecution{Run: *claimed, ClaimToken: lease.Token, Output: output}, plan)
	if missing.State != StepFailed || missing.ErrorCode != "promotion_image_missing" {
		t.Fatalf("a missing image = %#v", missing)
	}
	backend.inspected[imageID] = ResolvedImage{Reference: imageID, Digest: imageID, ConfigDigest: imageID}
	result := executor.renderRuntime(ctx, StepExecution{Run: *claimed, ClaimToken: lease.Token, Output: output}, plan)
	if result.State != StepPassed {
		t.Fatalf("render = %#v\n%s", result, output.joined())
	}
	if len(backend.builds) != 0 || len(backend.pulls) != 0 {
		t.Fatalf("a promotion built or pulled: %#v %#v", backend.builds, backend.pulls)
	}
	tag := releaseImageTag(f.envID, run.ID)
	if len(backend.tags) != 1 || backend.tags[0] != [2]string{imageID, tag} {
		t.Fatalf("tags = %#v", backend.tags)
	}
	promoted, err := f.runs.ReleaseForRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Release.EnvironmentID != f.envID || promoted.Release.ImageDigest != source.Release.ImageDigest {
		t.Fatalf("promoted release = %#v", promoted.Release)
	}
	if promoted.Release.Promotion == nil || promoted.Release.Promotion.ReleaseID != source.Release.ID ||
		promoted.Release.Promotion.ImageID != imageID || promoted.Release.Promotion.EnvironmentName != "Staging" {
		t.Fatalf("provenance = %#v", promoted.Release.Promotion)
	}
	if promoted.Release.SourceRevision != source.Release.SourceRevision {
		t.Fatalf("source revision = %q, want staging's %q", promoted.Release.SourceRevision, source.Release.SourceRevision)
	}
	snapshot, err := decodeReleaseRuntimeSnapshot(promoted)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Image.ConfigDigest != imageID || snapshot.Image.Reference != tag {
		t.Fatalf("snapshot image = %#v", snapshot.Image)
	}
	names := []string{}
	for _, variable := range snapshot.Variables {
		names = append(names, variable.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"ALIAS", "DATABASE_URL", "PLAIN", "TOKEN", "TOKEN_ALIAS"}) {
		t.Fatalf("promotion runs with %v, not production's variables", names)
	}
	if snapshot.Plan.HostPort != 8080 || len(snapshot.Domains) != 1 || snapshot.Domains[0].Hostname != "app.example.com" {
		t.Fatalf("promotion runs with staging's plan: %#v / %#v", snapshot.Plan, snapshot.Domains)
	}
	if !slices.Equal(snapshot.ProxyTrust, []string{"FORWARDED_ALLOW_IPS"}) || !snapshot.WebConcurrency {
		t.Fatalf("the image's own facts were lost: %#v", snapshot)
	}
	for _, artifact := range promoted.Artifacts {
		if artifact.Kind == ArtifactImage && (artifact.Reference != tag || artifact.Digest != imageID) {
			t.Fatalf("image artifact = %#v", artifact)
		}
	}
	if !strings.Contains(output.joined(), "no build") {
		t.Fatalf("transcript does not say nothing was built:\n%s", output.joined())
	}
}

func TestPromotionAdmissionAndRetentionHoldTheStagingImage(t *testing.T) {
	t.Parallel()
	f := newStagingFixture(t)
	ctx := context.Background()
	created, err := f.variables.CreateStagingEnvironment(ctx, f.projectID, StagingRequest{Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	staging := created.Environment.ID
	source := stagedRelease(t, f, staging, fakeContentDigest("first"))

	metadata, _ := json.Marshal(map[string]any{"promotion": map[string]any{"releaseId": source.Release.ID}})
	if _, _, err := f.runs.Enqueue(ctx, RunRequest{
		ProjectID: f.projectID, EnvironmentID: staging, Operation: OperationPromote, Trigger: TriggerManual,
		Actor: "admin", RequestDigest: "promote-into-staging", PlanRevision: 1, Metadata: metadata,
	}); !errors.Is(err, ErrPromotionTarget) {
		t.Fatalf("promoting into staging error = %v", err)
	}
	if _, _, err := f.runs.Enqueue(ctx, RunRequest{
		ProjectID: f.projectID, EnvironmentID: f.envID, Operation: OperationDeploy, Trigger: TriggerManual,
		Actor: "admin", RequestDigest: "deploy-carrying-promotion", PlanRevision: 1, Metadata: metadata,
	}); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("a deploy carrying a promotion error = %v", err)
	}

	// Six newer staging releases push the source out of staging's rollback
	// set; only the unfinished promotion still holds it.
	for number := 0; number < 6; number++ {
		stagedRelease(t, f, staging, fakeContentDigest(fmt.Sprintf("newer-%d", number)))
	}
	run, err := enqueuePromotion(t, f, source.Release.ID)
	if err != nil {
		t.Fatal(err)
	}
	decisions, err := f.runs.ArtifactRetentionPlan(ctx, f.now)
	if err != nil {
		t.Fatal(err)
	}
	held := false
	for _, decision := range decisions {
		if decision.Artifact.ReleaseID == source.Release.ID && decision.Artifact.Kind == ArtifactImage {
			held = decision.Retain && decision.Reason == "source of an unfinished promotion to production"
		}
	}
	if !held {
		t.Fatalf("an unfinished promotion's source is not retained: %#v", decisions)
	}
	if err := f.runs.reserveArtifactPrune(ctx, source.Artifacts[0].ID); !errors.Is(err, ErrArtifactRetained) {
		t.Fatalf("prune reservation of a promotion's source = %v", err)
	}
	if _, err := f.base.DB.Exec("UPDATE deploy_runs SET state = 'cancelled', status = 'cancelled', ended_at = ? WHERE id = ?", f.now.Unix(), run.ID); err != nil {
		t.Fatal(err)
	}
	decisions, _ = f.runs.ArtifactRetentionPlan(ctx, f.now)
	for _, decision := range decisions {
		if decision.Artifact.ReleaseID == source.Release.ID && decision.Retain {
			t.Fatalf("a finished promotion still holds its source: %#v", decision)
		}
	}

	if _, err := f.base.DB.Exec("UPDATE deploy_release_artifacts SET state = 'pruned' WHERE release_id = ?", source.Release.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := enqueuePromotion(t, f, source.Release.ID); !errors.Is(err, ErrPromotionImageMissing) {
		t.Fatalf("promoting a pruned image error = %v", err)
	}
	productionRun := f.enqueueProductionDeploy(t)
	if _, err := enqueuePromotion(t, f, productionRun); !errors.Is(err, ErrPromotionSource) {
		t.Fatalf("promoting a production release error = %v", err)
	}
}

// enqueueProductionDeploy leaves one live production release and returns
// its id, for the cases that need production to have one.
func (f *releaseStoreFixture) enqueueProductionDeploy(t *testing.T) int64 {
	t.Helper()
	run, lease := f.claimedRun(t, 1)
	release := f.candidate(t, *run, lease, fakeContentDigest(fmt.Sprintf("production-%d", run.ID)))
	activateFixtureRelease(t, f, f.envID, release.Release.ID, run.ID, lease.Token)
	return release.Release.ID
}

func TestPromotionOnlyRefusesEveryBuildButNotPromotionsOrRollback(t *testing.T) {
	t.Parallel()
	f := newStagingFixture(t)
	ctx := context.Background()
	policy, err := f.runs.GitDeploymentPolicy(ctx, f.projectID, f.envID)
	if err != nil {
		t.Fatal(err)
	}
	policy.PromotionOnly = true
	if _, err := f.runs.SetGitDeploymentPolicy(ctx, f.projectID, f.envID, policy); !errors.Is(err, ErrStagingRequired) {
		t.Fatalf("promotion-only without staging error = %v", err)
	}
	created, err := f.variables.CreateStagingEnvironment(ctx, f.projectID, StagingRequest{Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	staging := created.Environment.ID
	stagingPolicy, _ := f.runs.GitDeploymentPolicy(ctx, f.projectID, staging)
	stagingPolicy.PromotionOnly = true
	if _, err := f.runs.SetGitDeploymentPolicy(ctx, f.projectID, staging, stagingPolicy); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("promotion-only on staging error = %v", err)
	}
	saved, err := f.runs.SetGitDeploymentPolicy(ctx, f.projectID, f.envID, policy)
	if err != nil || !saved.PromotionOnly {
		t.Fatalf("promotion-only = %#v, %v", saved, err)
	}
	for _, operation := range []Operation{OperationDeploy, OperationForceBuild} {
		for _, trigger := range []TriggerKind{TriggerManual, TriggerGitPush, TriggerSchedule, TriggerAPI} {
			if _, _, err := f.runs.Enqueue(ctx, RunRequest{
				ProjectID: f.projectID, EnvironmentID: f.envID, Operation: operation, Trigger: trigger,
				Actor: "admin", RequestDigest: string(operation) + string(trigger), PlanRevision: 1,
			}); !errors.Is(err, ErrPromotionOnly) {
				t.Fatalf("%s by %s error = %v", operation, trigger, err)
			}
		}
	}
	status, err := f.runs.GitWatchStatus(ctx, f.projectID, f.envID)
	if err != nil || status.Automatic || !status.Policy.PromotionOnly {
		t.Fatalf("watch status = %#v, %v", status, err)
	}
	source := stagedRelease(t, f, staging, fakeContentDigest("promoted"))
	if _, err := enqueuePromotion(t, f, source.Release.ID); err != nil {
		t.Fatalf("a promotion was refused under promotion-only: %v", err)
	}
	if _, _, err := f.runs.Enqueue(ctx, RunRequest{
		ProjectID: f.projectID, EnvironmentID: f.envID, Operation: OperationRollback, Trigger: TriggerRollback,
		Actor: "admin", RequestDigest: "rollback", PlanRevision: 1, Metadata: json.RawMessage(`{"targetReleaseId":1}`),
	}); err != nil {
		t.Fatalf("rollback refused under promotion-only: %v", err)
	}
	if _, err := f.variables.BeginStagingRemoval(ctx, f.projectID, staging, true); !errors.Is(err, ErrPromotionOnlyNeedsStaging) {
		t.Fatalf("removing staging under promotion-only error = %v", err)
	}
	// Staging still builds: only production is promotion-only.
	if _, _, err := f.runs.Enqueue(ctx, RunRequest{
		ProjectID: f.projectID, EnvironmentID: staging, Operation: OperationDeploy, Trigger: TriggerGitPush,
		Actor: "git-monitor", RequestDigest: "staging-push", PlanRevision: 1,
	}); err != nil {
		t.Fatalf("staging build refused: %v", err)
	}
}

func TestRollbackToAPromotedReleaseKeepsItsStagingProvenance(t *testing.T) {
	t.Parallel()
	f := newStagingFixture(t)
	ctx := context.Background()
	created, err := f.variables.CreateStagingEnvironment(ctx, f.projectID, StagingRequest{Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	imageID := fakeContentDigest("promoted-image")
	source := stagedRelease(t, f, created.Environment.ID, imageID)
	run, err := enqueuePromotion(t, f, source.Release.ID)
	if err != nil {
		t.Fatal(err)
	}
	lease, _ := f.runs.ClaimNext(ctx, "worker", QueueBudget{Heavy: 1, Light: 1}, time.Minute)
	claimed, _ := f.runs.Run(ctx, run.ID)
	plan, _ := f.runs.ExecutionPlan(ctx, *claimed)
	backend := &artifactBackendFake{
		inspected: map[string]ResolvedImage{imageID: {Digest: imageID, ConfigDigest: imageID}},
		local:     map[string]string{imageID: imageID},
	}
	executor := &NormalizedStepExecutor{store: f.runs, variables: f.variables, builder: NewArtifactBuilder(backend)}
	if result := executor.renderRuntime(ctx, StepExecution{Run: *claimed, ClaimToken: lease.Token, Output: &recordingStepOutput{}}, plan); result.State != StepPassed {
		t.Fatalf("render = %#v", result)
	}
	promoted, _ := f.runs.ReleaseForRun(ctx, run.ID)
	activateFixtureRelease(t, f, f.envID, promoted.Release.ID, run.ID, lease.Token)
	f.enqueueProductionDeploy(t)

	rollback, _, err := f.runs.Enqueue(ctx, RunRequest{
		ProjectID: f.projectID, EnvironmentID: f.envID, Operation: OperationRollback, Trigger: TriggerRollback,
		Actor: "admin", RequestDigest: "rollback-to-promoted", PlanRevision: promoted.Release.PlanRevision,
		VariableSnapshotRunID: run.ID, Metadata: json.RawMessage(fmt.Sprintf(`{"targetReleaseId":%d}`, promoted.Release.ID)),
	})
	if err != nil {
		t.Fatal(err)
	}
	rollbackLease, _ := f.runs.ClaimNext(ctx, "worker", QueueBudget{Heavy: 1, Light: 1}, time.Minute)
	rollbackRun, _ := f.runs.Run(ctx, rollback.ID)
	clone, err := f.runs.CloneCandidateRelease(ctx, *rollbackRun, rollbackLease.Token, promoted.Release.ID)
	if err != nil {
		t.Fatal(err)
	}
	if clone.Release.ImageDigest != imageID || clone.Release.SourceRevision != source.Release.SourceRevision ||
		clone.Release.Promotion == nil || clone.Release.Promotion.ReleaseID != source.Release.ID {
		t.Fatalf("rollback clone = %#v", clone.Release)
	}
	snapshot, err := decodeReleaseRuntimeSnapshot(clone)
	if err != nil || snapshot.Image.ConfigDigest != imageID {
		t.Fatalf("rollback runs %#v, %v", snapshot.Image, err)
	}
}

func TestProductionSummaryLeavesStagingRunsOutAndStagingHasItsOwn(t *testing.T) {
	t.Parallel()
	f := newStagingFixture(t)
	ctx := context.Background()
	created, err := f.variables.CreateStagingEnvironment(ctx, f.projectID, StagingRequest{Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	staging := created.Environment.ID
	run, _, err := f.runs.Enqueue(ctx, RunRequest{
		ProjectID: f.projectID, EnvironmentID: staging, Operation: OperationDeploy, Trigger: TriggerManual,
		Actor: "admin", RequestDigest: "staging-only", PlanRevision: 1, SlotClass: SlotHeavy,
	})
	if err != nil {
		t.Fatal(err)
	}
	production, err := f.runs.DeploymentSummary(ctx, f.projectID, QueueBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if production.EnvironmentID != f.envID || production.ActiveRun != nil || production.LastRun != nil {
		t.Fatalf("production summary shows staging's run: %#v", production)
	}
	summary, err := f.runs.EnvironmentDeploymentSummary(ctx, f.projectID, staging, QueueBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if summary.EnvironmentID != staging || summary.EnvironmentKind != EnvironmentStaging ||
		summary.ActiveRun == nil || summary.ActiveRun.ID != run.ID {
		t.Fatalf("staging summary = %#v", summary)
	}
	if _, err := f.runs.EnvironmentDeploymentSummary(ctx, f.projectID+1, staging, QueueBudget{}); err == nil {
		t.Fatal("another project's environment was summarised")
	}
	insights, err := f.runs.ProjectInsights(ctx, f.projectID, 30)
	if err != nil || insights.Runs != 0 {
		t.Fatalf("project insights = %#v, %v", insights, err)
	}
}

func TestStagingRemovalNamesOnlyItsOwnResourcesAndFreesTheName(t *testing.T) {
	t.Parallel()
	f := newStagingFixture(t)
	ctx := context.Background()
	created, err := f.variables.CreateStagingEnvironment(ctx, f.projectID, StagingRequest{Domain: "staging.example.com", Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	staging := created.Environment.ID
	if _, err := f.variables.BeginStagingRemoval(ctx, f.projectID, f.envID, true); !errors.Is(err, ErrNotStaging) {
		t.Fatalf("removing production through staging removal error = %v", err)
	}
	run, _, err := f.runs.Enqueue(ctx, RunRequest{
		ProjectID: f.projectID, EnvironmentID: staging, Operation: OperationDeploy, Trigger: TriggerManual,
		Actor: "admin", RequestDigest: "busy", PlanRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.variables.BeginStagingRemoval(ctx, f.projectID, staging, true); !errors.Is(err, ErrAlreadyDeploying) {
		t.Fatalf("removal during a run error = %v", err)
	}
	if _, err := f.base.DB.Exec("UPDATE deploy_runs SET state = 'cancelled', status = 'cancelled' WHERE id = ?", run.ID); err != nil {
		t.Fatal(err)
	}
	// A mount the operator pointed back at production's volume is never
	// staging's to delete.
	if _, err := f.base.DB.Exec(`INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at)
		SELECT environment_id, 2, json_insert(config_json, '$.mounts[#]', json('{"source":"app-data","target":"/shared","ownership":"managed"}')), preview, digest, created_at
		  FROM deploy_runtime_plans WHERE environment_id = ? AND revision = 1`, staging); err != nil {
		t.Fatal(err)
	}
	removal, err := f.variables.BeginStagingRemoval(ctx, f.projectID, staging, true)
	if err != nil {
		t.Fatal(err)
	}
	if removal.ProxySite != deploymentRouteName(staging) || len(removal.Volumes) != 2 {
		t.Fatalf("removal = %#v", removal)
	}
	for _, volume := range removal.Volumes {
		if !strings.HasPrefix(volume, stagingVolumePrefix(staging)) {
			t.Fatalf("staging removal would delete %s", volume)
		}
	}
	if _, _, err := f.runs.Enqueue(ctx, RunRequest{
		ProjectID: f.projectID, EnvironmentID: staging, Operation: OperationDeploy, Trigger: TriggerManual,
		Actor: "admin", RequestDigest: "after-archive", PlanRevision: 1,
	}); !errors.Is(err, ErrEnvironmentNotFound) {
		t.Fatalf("a run started in staging during its removal: %v", err)
	}
	if err := f.variables.FinishStagingRemoval(ctx, staging); err != nil {
		t.Fatal(err)
	}
	again, err := f.variables.CreateStagingEnvironment(ctx, f.projectID, StagingRequest{Domain: "staging.example.com", Actor: "admin"})
	if err != nil || again.Environment.ID == staging {
		t.Fatalf("staging could not be added again: %#v, %v", again, err)
	}
}
