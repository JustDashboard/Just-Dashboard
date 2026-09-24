package deploy

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	basestore "github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// seedFactsDeployment writes one production deployment with the given source
// identity and build plan and, when snapshot is set, a live release whose
// runtime snapshot it is. imageDigest is the release's own image digest, which
// a single-container snapshot must repeat and a Compose snapshot leaves empty.
func seedFactsDeployment(
	t *testing.T,
	base *basestore.Store,
	now time.Time,
	name, identity, build, snapshot, imageDigest string,
) int64 {
	t.Helper()
	result, err := base.DB.Exec(`
		INSERT INTO deploy_projects(
		  name, profile, repo_path, branch, compose_file, pre_command, post_command,
		  hook_secret, hook_id, enabled, created_at, updated_at)
		VALUES(?, 'web', ?, 'main', 'compose.yml', '', '', 'sealed', ?, 1, ?, ?)`,
		name, "/srv/"+name, name+"-hook", now.Unix(), now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := result.LastInsertId()
	result, err = base.DB.Exec(`
		INSERT INTO deploy_environments(
		  project_id, name, slug, kind, desired_revision, strategy, expected_downtime,
		  protected, created_at, updated_at)
		VALUES(?, 'Production', 'production', 'production', 1, 'blue_green', 0, 1, ?, ?)`,
		projectID, now.Unix(), now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	environmentID, _ := result.LastInsertId()
	var decodedIdentity SourceIdentity
	var decodedBuild BuildPlanConfig
	if json.Unmarshal([]byte(identity), &decodedIdentity) != nil || json.Unmarshal([]byte(build), &decodedBuild) != nil {
		t.Fatalf("fixture identity %s or build %s is not JSON", identity, build)
	}
	if _, err := base.DB.Exec(`
		INSERT INTO deploy_sources(environment_id, revision, kind, config_json, identity_json, digest, created_at)
		VALUES(?, 1, ?, '{}', ?, ?, ?)`, environmentID, decodedIdentity.Kind, identity,
		fakeContentDigest(name+"-source"), now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := base.DB.Exec(`
		INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at)
		VALUES(?, 1, ?, ?, '{"candidates":[],"gitRequirements":{}}', 'preview', ?, ?)`,
		environmentID, decodedBuild.Method, build, fakeContentDigest(name+"-build"), now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := base.DB.Exec(`
		INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at)
		VALUES(?, 1, '{"strategy":"blue_green","internalPort":3000}', 'preview', ?, ?)`,
		environmentID, fakeContentDigest(name+"-runtime"), now.Unix()); err != nil {
		t.Fatal(err)
	}
	if snapshot == "" {
		return projectID
	}
	result, err = base.DB.Exec(`
		INSERT INTO deploy_runs(
		  project_id, environment_id, started_at, state, status, operation, trigger, actor,
		  requested_at, queued_at, claimed_at, heartbeat_at, ended_at, cancel_requested,
		  superseded_by, retry_of_run_id, idempotency_key, request_digest, plan_revision,
		  release_id, candidate_release_id, terminal_code, terminal_reason, lease_until,
		  priority, slot_class, metadata_json)
		VALUES(?, ?, ?, 'succeeded', 'success', 'deploy', 'manual', 'admin', ?, ?, ?, ?, ?, 0, 0, 0,
		       '', ?, 1, 0, 0, 'success', '', 0, 0, 'heavy', '{}')`,
		projectID, environmentID, now.Unix(), now.Unix(), now.Unix(), now.Unix(), now.Unix(), now.Unix(),
		name+"-run")
	if err != nil {
		t.Fatal(err)
	}
	runID, _ := result.LastInsertId()
	configDigest := digestBytes([]byte(snapshot))
	result, err = base.DB.Exec(`
		INSERT INTO deploy_releases(
		  project_id, environment_id, release_number, run_id, predecessor_release_id, state,
		  plan_revision, source_id, build_plan_id, runtime_plan_id, source_revision,
		  source_identity_json, image_digest, config_digest, variables_digest, strategy,
		  expected_downtime, provenance_json, blueprint_id, blueprint_version, created_at, activated_at)
		VALUES(?, ?, 1, ?, 0, 'live', 1, 0, 0, 0, ?, '{}', ?, ?, ?, 'blue_green', 0, '{}', '', '', ?, ?)`,
		projectID, environmentID, runID, strings.Repeat("a", 40), imageDigest, configDigest,
		fakeContentDigest(name+"-vars"), now.Unix(), now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	releaseID, _ := result.LastInsertId()
	if _, err := base.DB.Exec(`
		INSERT INTO deploy_release_artifacts(
		  release_id, kind, reference, digest, metadata_json, size_bytes, retain_until, state, created_at)
		VALUES(?, 'runtime_config', 'runtime-plan.json', ?, ?, ?, 0, 'available', ?)`,
		releaseID, configDigest, `{"secretFreePreview":"preview","snapshot":`+snapshot+`}`,
		len(snapshot), now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := base.DB.Exec(`
		INSERT INTO deploy_release_runtimes(
		  release_id, environment_id, kind, runtime_id, name, working_directory,
		  host, port, state, metadata_json, created_at, updated_at)
		VALUES(?, ?, 'container', ?, ?, '', '127.0.0.1', 0, 'live', '{}', ?, ?)`,
		releaseID, environmentID, name+"-container", name, now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE deploy_runs SET release_id = ? WHERE id = ?`,
		`UPDATE deploy_environments SET live_release_id = ? WHERE id = ?`,
	} {
		target := runID
		if strings.Contains(statement, "deploy_environments") {
			target = environmentID
		}
		if _, err := base.DB.Exec(statement, releaseID, target); err != nil {
			t.Fatal(err)
		}
	}
	return projectID
}

// A card draws a deployment as what it is: the repository and host it comes
// from, the recipe and framework it builds with, and the images it runs. All
// of it is read by the statements the fleet already performs.
func TestFleetSummaryNamesSourceBuildAndImages(t *testing.T) {
	t.Parallel()
	base, err := basestore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	composeSnapshot, _ := json.Marshal(runtimeReleaseSnapshot{
		Version: 1, Plan: RuntimePlanConfig{Strategy: StrategyBlueGreen},
		Compose: &ResolvedComposeSnapshot{Services: []ResolvedComposeService{
			{Plan: ComposeServicePlan{Name: "api"}, Reference: "example.test/shop:v1", Digest: fakeContentDigest("api")},
			{Plan: ComposeServicePlan{Name: "worker"}, Reference: "example.test/shop:v1", Digest: fakeContentDigest("api")},
			{Plan: ComposeServicePlan{Name: "cache"}, Reference: "redis:7", Digest: fakeContentDigest("redis")},
		}},
	})
	seedFactsDeployment(t, base, now, "shop",
		`{"kind":"git","remote":"https://github.com/acme/shop.git","repository":"acme/shop","ref":"main","revision":"`+
			strings.Repeat("a", 40)+`"}`,
		`{"method":"recipe","recipe":"node","secrets":[],"releaseTasks":[],"framework":"nextjs"}`,
		string(composeSnapshot), "")
	imageDigest := fakeContentDigest("tool")
	imageSnapshot, _ := json.Marshal(runtimeReleaseSnapshot{
		Version: 1, Plan: RuntimePlanConfig{Strategy: StrategyBlueGreen},
		Image: ResolvedImage{Reference: "ghcr.io/acme/tool:1.2", Digest: imageDigest},
	})
	seedFactsDeployment(t, base, now, "tool",
		`{"kind":"image","repository":"ghcr.io/acme/tool:1.2","digest":"`+imageDigest+`"}`,
		`{"method":"image"}`, string(imageSnapshot), imageDigest)
	seedFactsDeployment(t, base, now, "unreleased",
		`{"kind":"git","remote":"https://x-access-token:hunter2@gitlab.example.test/acme/api.git","repository":"acme/api"}`,
		`{"method":"dockerfile"}`, "", "")

	fleet, err := NewOrchestrationStore(base).Fleet(context.Background(), QueueBudget{Heavy: 1, Light: 2})
	if err != nil {
		t.Fatal(err)
	}
	summaries := map[string]DeploymentSummary{}
	for _, summary := range fleet.Deployments {
		summaries[summary.Name] = summary
	}
	shop := summaries["shop"]
	if shop.SourceRepository != "acme/shop" || shop.SourceRemote != "https://github.com/acme/shop.git" ||
		shop.Recipe != "node" || shop.Framework != "nextjs" ||
		!reflect.DeepEqual(shop.Images, []string{"example.test/shop:v1", "redis:7"}) {
		t.Fatalf("git compose summary = %#v", shop)
	}
	tool := summaries["tool"]
	if tool.SourceRepository != "ghcr.io/acme/tool:1.2" || tool.SourceRef != "" || tool.SourceRemote != "" ||
		tool.Recipe != "" || tool.Framework != "" || !reflect.DeepEqual(tool.Images, []string{"ghcr.io/acme/tool:1.2"}) {
		t.Fatalf("image summary = %#v", tool)
	}
	unreleased := summaries["unreleased"]
	if unreleased.SourceRemote != "https://gitlab.example.test/acme/api.git" || unreleased.Images != nil {
		t.Fatalf("unreleased summary = %#v", unreleased)
	}
	raw, _ := json.Marshal(fleet)
	if strings.Contains(string(raw), "hunter2") || strings.Contains(string(raw), "x-access-token") {
		t.Fatalf("fleet read leaked remote userinfo: %s", raw)
	}
}

func TestDisplayRemoteKeepsTheHostAndDropsEverythingElse(t *testing.T) {
	t.Parallel()
	for remote, want := range map[string]string{
		"https://github.com/acme/shop.git":                            "https://github.com/acme/shop.git",
		"https://token:secret@GitHub.com/acme/shop.git?ref=x#readme":  "https://github.com/acme/shop.git",
		"ssh://git@gitlab.com:2222/acme/shop.git":                     "ssh://gitlab.com:2222/acme/shop.git",
		"git@GitHub.com:acme/shop.git":                                "git@github.com:acme/shop.git",
		"deploy@git.example.test:acme/shop.git":                       "git.example.test:acme/shop.git",
		"/srv/checkouts/shop":                                         "",
		"file:///srv/checkouts/shop":                                  "",
		"":                                                            "",
		"https://gitea.example.test/acme/shop.git\n":                  "https://gitea.example.test/acme/shop.git",
		"https://user@bitbucket.org/acme/shop.git":                    "https://bitbucket.org/acme/shop.git",
		"https://codeberg.org/acme/shop":                              "https://codeberg.org/acme/shop",
		"git@codeberg.org:":                                           "",
		"https://%zz":                                                 "",
		"https://dev.azure.com/acme/project/_git/shop":                "https://dev.azure.com/acme/project/_git/shop",
		"https://oauth2:glpat-secret@gitlab.example.test/a/b.git":     "https://gitlab.example.test/a/b.git",
		"ssh://deploy:pass@git.example.test/srv/git/shop.git":         "ssh://git.example.test/srv/git/shop.git",
		"https://github.com/acme/shop.git?access_token=ghp_leakyleak": "https://github.com/acme/shop.git",
	} {
		if got := displayRemote(remote); got != want {
			t.Errorf("displayRemote(%q) = %q, want %q", remote, got, want)
		}
	}
}

// The strip shows at most recentRunLimit runs per card, newest first, and its
// first run is the one the card already calls the last.
func TestFleetSummaryCarriesTheNewestRunsNewestFirst(t *testing.T) {
	store, _ := countingFleet(t, 2)
	now := store.now()
	for run := 0; run < 13; run++ {
		if _, err := store.db.Exec(`
			INSERT INTO deploy_runs(
			  project_id, environment_id, started_at, state, status, operation, trigger, actor,
			  requested_at, queued_at, claimed_at, heartbeat_at, ended_at, cancel_requested,
			  superseded_by, retry_of_run_id, idempotency_key, request_digest, plan_revision,
			  release_id, candidate_release_id, terminal_code, terminal_reason, lease_until,
			  priority, slot_class, metadata_json)
			VALUES(1, 1, ?, 'failed', 'failed', 'restart', 'manual', 'admin', ?, ?, ?, ?, ?, 0, 0, 0,
			       '', ?, 1, 0, 0, 'failed', '', 0, 0, 'light', '{}')`,
			now.Unix()+int64(10+run), now.Unix()+int64(10+run), now.Unix()+int64(10+run),
			now.Unix()+int64(10+run), now.Unix()+int64(10+run), now.Unix()+int64(11+run),
			strings.Repeat("r", run+1)); err != nil {
			t.Fatal(err)
		}
	}
	fleet, err := store.Fleet(context.Background(), QueueBudget{Heavy: 1, Light: 2})
	if err != nil {
		t.Fatal(err)
	}
	busy, quiet := fleet.Deployments[0], fleet.Deployments[1]
	if len(busy.RecentRuns) != recentRunLimit || busy.RecentRuns[0].ID != busy.LastRun.ID ||
		busy.RecentRuns[0].Operation != OperationRestart || busy.RecentRuns[0].State != RunFailed ||
		busy.RecentRuns[0].EndedAt == nil {
		t.Fatalf("busy recent runs = %#v", busy.RecentRuns)
	}
	for index := 1; index < len(busy.RecentRuns); index++ {
		if !busy.RecentRuns[index].RequestedAt.Before(busy.RecentRuns[index-1].RequestedAt) {
			t.Fatalf("recent runs are not newest first: %#v", busy.RecentRuns)
		}
	}
	if len(quiet.RecentRuns) != 3 || quiet.RecentRuns[0].ID != quiet.LastRun.ID {
		t.Fatalf("quiet recent runs = %#v", quiet.RecentRuns)
	}
}

// A run in flight carries the step it stands at wherever a list shows it: the
// fleet's in-progress block, the card's active run, and the project's run
// history. An ended run carries none.
func TestRunListsPlaceARunInFlightAtItsCurrentStep(t *testing.T) {
	t.Parallel()
	fixture := newOrchestrationFixture(t)
	environmentID := fixture.addEnvironment(t, "production", EnvironmentProduction)
	ended := fixture.enqueue(t, environmentID)
	if _, err := fixture.store.DB.Exec(`UPDATE deploy_runs SET state = 'succeeded', ended_at = ? WHERE id = ?`,
		fixture.now.Unix(), ended.ID); err != nil {
		t.Fatal(err)
	}
	fixture.advance(time.Second)
	run := fixture.enqueue(t, environmentID)

	runs, _, err := fixture.runs.ProjectRunsFiltered(context.Background(), fixture.projectID, RunListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].ID != run.ID || runs[1].CurrentStep != nil ||
		!reflect.DeepEqual(runs[0].CurrentStep, &CurrentStep{Key: StepResolveSource, Label: "Resolve source", State: StepPending}) {
		t.Fatalf("queued run list = %#v", runs)
	}

	if _, err := fixture.store.DB.Exec(`UPDATE deploy_steps SET status = 'passed' WHERE run_id = ? AND step_key = 'resolve_source'`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB.Exec(`UPDATE deploy_steps SET status = 'running' WHERE run_id = ? AND step_key = 'build_artifact'`, run.ID); err != nil {
		t.Fatal(err)
	}
	want := &CurrentStep{Key: StepBuildArtifact, Label: "Build", State: StepRunning}
	runs, _, err = fixture.runs.ProjectRunsFiltered(context.Background(), fixture.projectID, RunListFilter{State: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || !reflect.DeepEqual(runs[0].CurrentStep, want) {
		t.Fatalf("running run list = %#v", runs)
	}
	fleet, err := fixture.runs.Fleet(context.Background(), QueueBudget{Heavy: 1, Light: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(fleet.ActiveWork) != 1 || !reflect.DeepEqual(fleet.ActiveWork[0].Run.CurrentStep, want) ||
		fleet.ActiveWork[0].CurrentStep != StepBuildArtifact || fleet.ActiveWork[0].CurrentStatus != StepRunning {
		t.Fatalf("active work = %#v", fleet.ActiveWork)
	}
	if len(fleet.Deployments) != 1 || fleet.Deployments[0].ActiveRun == nil ||
		!reflect.DeepEqual(fleet.Deployments[0].ActiveRun.CurrentStep, want) ||
		!reflect.DeepEqual(fleet.Deployments[0].LastRun.CurrentStep, want) {
		t.Fatalf("summary active run = %#v", fleet.Deployments)
	}
}

func TestStepLabelsNameEveryStep(t *testing.T) {
	t.Parallel()
	for _, key := range append(append([]StepKey(nil), DefaultStepKeys...), StepLegacyPipeline) {
		if stepLabels[key] == "" {
			t.Errorf("step %s has no label", key)
		}
	}
}

// The archived list reads what each archived deployment was in one statement;
// a live deployment is not in it.
func TestArchivedDeploymentFactsReadEveryArchivedProject(t *testing.T) {
	t.Parallel()
	base, err := basestore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	archived := seedFactsDeployment(t, base, now, "old-shop",
		`{"kind":"git","remote":"https://github.com/acme/shop.git","repository":"acme/shop","ref":"main"}`,
		`{"method":"recipe","recipe":"python","framework":"django"}`, "", "")
	live := seedFactsDeployment(t, base, now, "live-shop",
		`{"kind":"image","repository":"nginx:1.29"}`, `{"method":"image"}`, "", "")
	if _, err := base.DB.Exec(`UPDATE deploy_projects SET archived_at = ? WHERE id = ?`, now.Unix(), archived); err != nil {
		t.Fatal(err)
	}
	facts, err := NewOrchestrationStore(base).ArchivedDeploymentFacts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := DeploymentFacts{
		SourceKind: SourceGit, SourceRef: "main", SourceRepository: "acme/shop",
		BuildMethod: BuildRecipe, Recipe: "python", Framework: "django",
	}
	if len(facts) != 1 || facts[archived] != want {
		t.Fatalf("archived facts = %#v, want only %d: %#v", facts, archived, want)
	}
	if _, found := facts[live]; found {
		t.Fatalf("live project %d was read as archived", live)
	}
}
