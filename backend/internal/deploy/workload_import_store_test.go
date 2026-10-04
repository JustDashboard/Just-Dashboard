package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

func observedWorkloadFixture() ObservedWorkloadRegistration {
	return ObservedWorkloadRegistration{
		Name: "bet-bot", ResourceKind: "compose_stack", ResourceID: "bet-bot",
		SourceMode: SourceModeExistingStack, OwnerUsername: "tester",
		Observed: json.RawMessage(`{"key":"stack:bet-bot","kind":"stack","resourceId":"bet-bot","total":4,"running":2}`),
	}
}

func TestRegisterObservedWorkloadRecordsOnlyObservation(t *testing.T) {
	t.Parallel()
	fixture := newPlanningStoreFixture(t)
	registration := observedWorkloadFixture()
	registration.Name = "  bet-bot  "
	result, err := fixture.plans.RegisterObservedWorkload(context.Background(), registration)
	if err != nil {
		t.Fatal(err)
	}
	if result.ProjectID == 0 || result.EnvironmentID == 0 || result.PlanRevision != 1 || !result.Created {
		t.Fatalf("registration result = %#v", result)
	}
	var name, profile, repository, branch, compose, pre, post, sealedHook string
	var enabled int
	err = fixture.store.DB.QueryRow(`
		SELECT name, profile, repo_path, branch, compose_file, pre_command, post_command, enabled, hook_secret
		  FROM deploy_projects WHERE id = ?`, result.ProjectID).
		Scan(&name, &profile, &repository, &branch, &compose, &pre, &post, &enabled, &sealedHook)
	if err != nil {
		t.Fatal(err)
	}
	if name != "bet-bot" || profile != string(ProfileImported) || enabled != 0 ||
		repository != "" || branch != "" || compose != "" || pre != "" || post != "" {
		t.Fatalf("registration invented execution settings: %q %q %q %q %q %q %q enabled=%d",
			name, profile, repository, branch, compose, pre, post, enabled)
	}
	if hook, err := fixture.sealer.Open(sealedHook); err != nil || hook == "" {
		t.Fatalf("disabled hook secret was not sealed: err=%v", err)
	}
	var sourceJSON, identityJSON string
	if err := fixture.store.DB.QueryRow(`
		SELECT config_json, identity_json FROM deploy_sources WHERE environment_id = ?`, result.EnvironmentID).
		Scan(&sourceJSON, &identityJSON); err != nil {
		t.Fatal(err)
	}
	var source DraftSourceConfig
	var identity SourceIdentity
	if err := json.Unmarshal([]byte(sourceJSON), &source); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(identityJSON), &identity); err != nil {
		t.Fatal(err)
	}
	if source.Kind != SourceImport || source.Mode != SourceModeExistingStack || source.ResourceID != "bet-bot" ||
		source.LocalPath != "" || source.ManagedInPlace || source.CredentialID != 0 || len(source.ComposeFiles) != 0 {
		t.Fatalf("registration source = %#v", source)
	}
	if identity.Kind != SourceImport || identity.Repository != "bet-bot" || string(identity.Observed) != string(registration.Observed) {
		t.Fatalf("registration identity = %#v", identity)
	}
	var kind, ownership, resourceKind, resourceID, observed string
	var releaseID int64
	if err := fixture.store.DB.QueryRow(`
		SELECT kind, ownership, resource_kind, resource_id, config_json, release_id
		  FROM deploy_dependencies WHERE environment_id = ?`, result.EnvironmentID).
		Scan(&kind, &ownership, &resourceKind, &resourceID, &observed, &releaseID); err != nil {
		t.Fatal(err)
	}
	if kind != "runtime" || ownership != "observed" || resourceKind != registration.ResourceKind ||
		resourceID != registration.ResourceID || observed != string(registration.Observed) || releaseID != 0 {
		t.Fatalf("runtime dependency = %q %q %q %q %q release=%d", kind, ownership, resourceKind, resourceID, observed, releaseID)
	}
	var desired int
	var live int64
	if err := fixture.store.DB.QueryRow(`
		SELECT desired_revision, live_release_id FROM deploy_environments WHERE id = ?`, result.EnvironmentID).
		Scan(&desired, &live); err != nil || desired != 1 || live != 0 {
		t.Fatalf("environment revision=%d live=%d err=%v", desired, live, err)
	}
	var buildJSON, runtimeJSON string
	if err := fixture.store.DB.QueryRow(`SELECT config_json FROM deploy_build_plans WHERE environment_id = ?`, result.EnvironmentID).
		Scan(&buildJSON); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.DB.QueryRow(`SELECT config_json FROM deploy_runtime_plans WHERE environment_id = ?`, result.EnvironmentID).
		Scan(&runtimeJSON); err != nil {
		t.Fatal(err)
	}
	var build BuildPlanConfig
	var runtime RuntimePlanConfig
	if err := json.Unmarshal([]byte(buildJSON), &build); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(runtimeJSON), &runtime); err != nil {
		t.Fatal(err)
	}
	if build.Method != BuildNone || build.BuildCommand != "" || build.StartCommand != "" || len(build.ReleaseTasks) != 0 ||
		runtime.Image != "" || len(runtime.Command) != 0 || len(runtime.Mounts) != 0 || runtime.HostPort != 0 {
		t.Fatalf("registration invented a build or runtime: %#v %#v", build, runtime)
	}
	for _, table := range []string{
		"deploy_runs", "deploy_releases", "deploy_variable_revisions", "deploy_env", "deploy_checks",
		"deploy_triggers", "deploy_schedules", "deploy_git_watches", "deploy_credentials",
	} {
		assertObservedWorkloadRows(t, fixture, table, 0)
	}
}

func TestRegisterObservedWorkloadAcceptsEachExternalRuntimeKind(t *testing.T) {
	t.Parallel()
	for _, entry := range []struct {
		resource string
		kind     string
		mode     SourceMode
	}{
		{"compose_stack", "stack", SourceModeExistingStack}, {"docker_container", "container", SourceModeExistingContainer},
		{"pm2_process", "pm2", "existing_pm2"}, {"systemd_unit", "systemd", "existing_systemd"},
		{"host_process", "process", "existing_process"},
	} {
		t.Run(entry.kind, func(t *testing.T) {
			fixture := newPlanningStoreFixture(t)
			registration := observedWorkloadFixture()
			registration.ResourceKind, registration.SourceMode = entry.resource, entry.mode
			registration.Observed = json.RawMessage(`{"key":"` + entry.kind + `:bet-bot","kind":"` + entry.kind + `","resourceId":"bet-bot"}`)
			if _, err := fixture.plans.RegisterObservedWorkload(context.Background(), registration); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRegisterObservedWorkloadRejectsInvalidRegistrationBeforeWriting(t *testing.T) {
	t.Parallel()
	for _, entry := range []struct {
		name   string
		change func(*ObservedWorkloadRegistration)
	}{
		{"name", func(r *ObservedWorkloadRegistration) { r.Name = "../project" }},
		{"resource kind", func(r *ObservedWorkloadRegistration) { r.ResourceKind = "docker_volume" }},
		{"mode mismatch", func(r *ObservedWorkloadRegistration) { r.SourceMode = SourceModeExistingContainer }},
		{"missing resource", func(r *ObservedWorkloadRegistration) { r.ResourceID = "" }},
		{"blank resource", func(r *ObservedWorkloadRegistration) { r.ResourceID = "  " }},
		{"resource control", func(r *ObservedWorkloadRegistration) { r.ResourceID = "bet\x00bot" }},
		{"resource encoding", func(r *ObservedWorkloadRegistration) { r.ResourceID = string([]byte{0xff}) }},
		{"resource size", func(r *ObservedWorkloadRegistration) { r.ResourceID = strings.Repeat("a", 4097) }},
		{"missing metadata", func(r *ObservedWorkloadRegistration) { r.Observed = nil }},
		{"null metadata", func(r *ObservedWorkloadRegistration) { r.Observed = json.RawMessage(`null`) }},
		{"array metadata", func(r *ObservedWorkloadRegistration) { r.Observed = json.RawMessage(`[]`) }},
		{"empty metadata", func(r *ObservedWorkloadRegistration) { r.Observed = json.RawMessage(`{}`) }},
		{"malformed metadata", func(r *ObservedWorkloadRegistration) { r.Observed = json.RawMessage(`{"key":`) }},
		{"metadata encoding", func(r *ObservedWorkloadRegistration) {
			r.Observed = json.RawMessage{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}
		}},
		{"missing metadata key", func(r *ObservedWorkloadRegistration) {
			r.Observed = json.RawMessage(`{"kind":"stack","resourceId":"bet-bot"}`)
		}},
		{"metadata kind mismatch", func(r *ObservedWorkloadRegistration) {
			r.Observed = json.RawMessage(`{"key":"container:bet-bot","kind":"container","resourceId":"bet-bot"}`)
		}},
		{"metadata resource mismatch", func(r *ObservedWorkloadRegistration) {
			r.Observed = json.RawMessage(`{"key":"stack:other","kind":"stack","resourceId":"other"}`)
		}},
		{"metadata size", func(r *ObservedWorkloadRegistration) {
			r.Observed = json.RawMessage(`{"key":"` + strings.Repeat("a", maxEventBytes) + `"}`)
		}},
	} {
		t.Run(entry.name, func(t *testing.T) {
			fixture := newPlanningStoreFixture(t)
			registration := observedWorkloadFixture()
			entry.change(&registration)
			if _, err := fixture.plans.RegisterObservedWorkload(context.Background(), registration); !errors.Is(err, ErrInvalidPlan) {
				t.Fatalf("registration error=%v, want ErrInvalidPlan", err)
			}
			assertObservedWorkloadRows(t, fixture, "deploy_projects", 0)
		})
	}
}

func TestRegisterObservedWorkloadRejectsDuplicateAndTakenNameWithoutPartialRows(t *testing.T) {
	t.Parallel()
	fixture := newPlanningStoreFixture(t)
	ctx := context.Background()
	registration := observedWorkloadFixture()
	if _, err := fixture.plans.RegisterObservedWorkload(ctx, registration); err != nil {
		t.Fatal(err)
	}
	duplicate := registration
	duplicate.Name = "another-name"
	if _, err := fixture.plans.RegisterObservedWorkload(ctx, duplicate); !errors.Is(err, ErrWorkloadAlreadyImported) {
		t.Fatalf("duplicate error=%v, want ErrWorkloadAlreadyImported", err)
	}
	nameConflict := registration
	nameConflict.ResourceID = "different-stack"
	nameConflict.Observed = json.RawMessage(`{"key":"stack:different-stack","kind":"stack","resourceId":"different-stack"}`)
	if _, err := fixture.plans.RegisterObservedWorkload(ctx, nameConflict); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("name collision error=%v, want ErrNameTaken", err)
	}
	for _, table := range []string{
		"deploy_projects", "deploy_environments", "deploy_sources", "deploy_build_plans", "deploy_runtime_plans", "deploy_dependencies",
	} {
		assertObservedWorkloadRows(t, fixture, table, 1)
	}
}

func TestRegisterObservedWorkloadRollsBackOnLateWriteFailure(t *testing.T) {
	t.Parallel()
	fixture := newPlanningStoreFixture(t)
	if _, err := fixture.store.DB.Exec(`
		CREATE TRIGGER refuse_workload_observation BEFORE INSERT ON deploy_dependencies
		BEGIN SELECT RAISE(ABORT, 'fixture observation failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.plans.RegisterObservedWorkload(context.Background(), observedWorkloadFixture()); err == nil {
		t.Fatal("registration succeeded despite failing dependency insert")
	}
	for _, table := range []string{
		"deploy_projects", "deploy_environments", "deploy_sources", "deploy_build_plans", "deploy_runtime_plans", "deploy_dependencies",
	} {
		assertObservedWorkloadRows(t, fixture, table, 0)
	}
}

func TestRegisterObservedWorkloadSerializesCompetingStoreInstances(t *testing.T) {
	t.Parallel()
	fixture := newPlanningStoreFixture(t)
	other := NewPlanningStore(fixture.store, fixture.sealer, nil)
	var wait sync.WaitGroup
	errorsByStore := make([]error, 2)
	start := make(chan struct{})
	for index, plans := range []*PlanningStore{fixture.plans, other} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			registration := observedWorkloadFixture()
			if index == 1 {
				registration.Name = "bet-bot-other"
			}
			_, errorsByStore[index] = plans.RegisterObservedWorkload(context.Background(), registration)
		}()
	}
	close(start)
	wait.Wait()
	var created, duplicate int
	for _, err := range errorsByStore {
		if err == nil {
			created++
		} else if errors.Is(err, ErrWorkloadAlreadyImported) {
			duplicate++
		} else {
			t.Fatalf("concurrent registration error=%v", err)
		}
	}
	if created != 1 || duplicate != 1 {
		t.Fatalf("competing registrations created=%d duplicate=%d", created, duplicate)
	}
	assertObservedWorkloadRows(t, fixture, "deploy_projects", 1)
	assertObservedWorkloadRows(t, fixture, "deploy_dependencies", 1)
}

func TestRegisterObservedWorkloadPreservesArchivedRuntimeReservation(t *testing.T) {
	t.Parallel()
	fixture := newPlanningStoreFixture(t)
	ctx := context.Background()
	registration := observedWorkloadFixture()
	result, err := fixture.plans.RegisterObservedWorkload(ctx, registration)
	if err != nil {
		t.Fatal(err)
	}
	projects := NewStore(fixture.store, fixture.sealer, nil)
	if _, err := projects.Archive(ctx, result.ProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.plans.RegisterObservedWorkload(ctx, registration); !errors.Is(err, ErrWorkloadAlreadyImported) {
		t.Fatalf("archived duplicate error=%v, want ErrWorkloadAlreadyImported", err)
	}
	if _, err := projects.PurgeArchived(ctx, result.ProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.plans.RegisterObservedWorkload(ctx, registration); err != nil {
		t.Fatalf("registration after forgetting archived record: %v", err)
	}
}

func TestRegisterObservedWorkloadKeepsConfigurationWithOriginalManager(t *testing.T) {
	t.Parallel()
	fixture := newPlanningStoreFixture(t)
	ctx := context.Background()
	result, err := fixture.plans.RegisterObservedWorkload(ctx, observedWorkloadFixture())
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := fixture.plans.EnvironmentConfiguration(ctx, result.ProjectID, result.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.plans.SaveEnvironmentConfiguration(ctx, result.ProjectID, result.EnvironmentID, ConfigurationWriteRequest{
		Revision: configuration.Revision, Build: configuration.Build,
		Runtime: RuntimePlanConfig{Image: "alpine:3", Strategy: StrategyStopFirst},
		Domains: []PlannedDomain{{Hostname: "bot.example.test", Ownership: OwnershipManaged}},
	})
	if !errors.Is(err, ErrInvalidPlan) || !strings.Contains(err.Error(), "existing manager") {
		t.Fatalf("configuration mutation error=%v, want refusal by original manager policy", err)
	}
	changedSource := *configuration.Source
	changedSource.ResourceID = "different-stack"
	_, err = fixture.plans.SaveEnvironmentSource(ctx, result.ProjectID, result.EnvironmentID, configuration.Revision,
		changedSource, SourceIdentity{Kind: SourceImport, Repository: "different-stack"})
	if !errors.Is(err, ErrInvalidPlan) || !strings.Contains(err.Error(), "existing manager") {
		t.Fatalf("source mutation error=%v, want refusal by original manager policy", err)
	}
	_, err = fixture.plans.Duplicate(ctx, result.ProjectID, 7, "tester", "duplicated-bot")
	if !errors.Is(err, ErrInvalidPlan) || !strings.Contains(err.Error(), "existing manager") {
		t.Fatalf("duplicate error=%v, want refusal by original manager policy", err)
	}
	unchanged, err := fixture.plans.EnvironmentConfiguration(ctx, result.ProjectID, result.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Revision != 1 || unchanged.Source.ResourceID != "bet-bot" || len(unchanged.Domains) != 0 ||
		unchanged.Runtime.Image != "" || len(unchanged.Dependencies) != 1 || unchanged.Dependencies[0].Ownership != OwnershipObserved {
		t.Fatalf("refused mutation changed imported workload: %#v", unchanged)
	}
	assertObservedWorkloadRows(t, fixture, "deploy_drafts", 0)
	assertObservedWorkloadRows(t, fixture, "deploy_sources", 1)
}

func assertObservedWorkloadRows(t *testing.T, fixture *planningStoreFixture, table string, expected int) {
	t.Helper()
	var count int
	if err := fixture.store.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != expected {
		t.Fatalf("%s rows=%d, want %d", table, count, expected)
	}
}
