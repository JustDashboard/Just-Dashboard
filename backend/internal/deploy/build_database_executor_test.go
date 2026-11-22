package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// databaseBuildBackend builds the way a build that reads its database does:
// each build prints the next scripted transcript, a networked builder can be
// started, and a schema or seed step runs in the build's own layers.
type databaseBuildBackend struct {
	artifactBackendFake
	attempts   []scriptedAttempt
	started    []BuildNetwork
	closed     int
	steps      []BuildInvocation
	stepFiles  []string
	stepErr    error
	startErr   error
	buildCalls int
}

type scriptedAttempt struct {
	lines []string
	err   error
}

func (b *databaseBuildBackend) BuildImage(ctx context.Context, invocation BuildInvocation, emit func(BuildLog) error) (ResolvedImage, error) {
	b.buildCalls++
	if attempt := b.buildCalls - 1; attempt < len(b.attempts) {
		for _, line := range b.attempts[attempt].lines {
			if err := emit(BuildLog{Stream: "stderr", Text: line}); err != nil {
				return ResolvedImage{}, err
			}
		}
		if b.attempts[attempt].err != nil {
			b.builds = append(b.builds, invocation)
			return ResolvedImage{}, b.attempts[attempt].err
		}
	}
	return b.artifactBackendFake.BuildImage(ctx, invocation, emit)
}

func (b *databaseBuildBackend) StartNetworkedBuilder(_ context.Context, network BuildNetwork) (string, func(context.Context), error) {
	if b.startErr != nil {
		return "", nil, b.startErr
	}
	b.started = append(b.started, network)
	return dockerx.NetworkedBuilderName(network.Networks[0]), func(context.Context) { b.closed++ }, nil
}

func (b *databaseBuildBackend) RunCacheOnly(_ context.Context, invocation BuildInvocation, emit func(BuildLog) error) error {
	b.steps = append(b.steps, invocation)
	content, err := os.ReadFile(filepath.Join(invocation.ContextDir, invocation.Dockerfile))
	if err != nil {
		return err
	}
	b.stepFiles = append(b.stepFiles, string(content))
	for _, secret := range invocation.Secrets {
		if err := emit(BuildLog{Stream: "stdout", Text: "step sees " + secret.ID + "=" + secret.Value}); err != nil {
			return err
		}
	}
	return b.stepErr
}

type fakeBuildNetworks struct {
	values []map[string]string
	err    error
}

func (f *fakeBuildNetworks) NetworksForBuild(_ context.Context, _ int64, values map[string]string) ([]string, error) {
	f.values = append(f.values, values)
	if f.err != nil {
		return nil, f.err
	}
	return []string{"jd-e1-db-test"}, nil
}

type fakeDatabaseContents struct {
	hasRows bool
	asked   []int64
}

func (f *fakeDatabaseContents) DatabaseHasRows(_ context.Context, id int64) (bool, error) {
	f.asked = append(f.asked, id)
	return f.hasRows, nil
}

var (
	schemaMissingAttempt = scriptedAttempt{
		lines: []string{
			"#14 [build 4/4] RUN --mount=type=secret,id=DATABASE_URL,env=DATABASE_URL,required=true bun run build",
			"#14 59.27   Collecting page data using 11 workers ...",
			`#14 62.76   [cause]: PostgresError: relation "posts" does not exist`,
			"#14 62.98 Error: Failed to collect page data for /[locale]/blog/[slug]",
			`#14 ERROR: process "/bin/sh -c bun run build" did not complete successfully: exit code: 1`,
		},
		err: &dockerx.BuildError{Step: 14, Instruction: "RUN bun run build", Command: "bun run build", ExitCode: 1, BuildxExit: 1},
	}
	emptyParamsAttempt = scriptedAttempt{
		lines: []string{
			"#14 [build 4/4] RUN --mount=type=secret,id=DATABASE_URL,env=DATABASE_URL,required=true bun run build",
			"#14 53.37 Error: When using Cache Components, all `generateStaticParams` functions must return at least one result.",
			"#14 53.56 Error: Failed to collect page data for /[locale]/blog/[slug]",
			`#14 ERROR: process "/bin/sh -c bun run build" did not complete successfully: exit code: 1`,
		},
		err: &dockerx.BuildError{Step: 14, Instruction: "RUN bun run build", Command: "bun run build", ExitCode: 1, BuildxExit: 1},
	}
)

const databaseBuildRecipe = `# syntax=docker/dockerfile:1.10
FROM node:22-alpine@sha256:1 AS build
WORKDIR /app
COPY . .
RUN bun install --frozen-lockfile
RUN --mount=type=secret,id=DATABASE_URL,env=DATABASE_URL,required=true bun run build
FROM node:22-alpine@sha256:1
COPY --from=build /app /app
CMD ["/bin/sh","-c","bunx drizzle-kit migrate && bun run start"]
`

// databaseBuildFixture is a claimed run of a Next.js plan whose build is
// handed a linked database (DATABASE_URL, build and runtime scoped) and whose
// start command migrates with Drizzle, with the steps before the build
// passed and the recipe's Dockerfile in the workspace.
func databaseBuildFixture(t *testing.T, backend BuildBackend, value string, build BuildPlanConfig) (*NormalizedStepExecutor, StepExecution, *StoredExecutionPlan) {
	t.Helper()
	return databaseBuildRun(t, databaseBuildOptions{backend: backend, value: value, build: build})
}

// databaseBuildOptions are what databaseBuildRun builds: a scripted backend
// and the fixture's recipe, or a real backend, checkout and preparation.
type databaseBuildOptions struct {
	backend   BuildBackend
	value     string
	build     BuildPlanConfig
	workspace string
	prepared  *PreparedBuild
	candidate map[string]any
	// environmentID is the environment's id, 0 for the next one.
	environmentID int64
}

func databaseBuildRun(t *testing.T, options databaseBuildOptions) (*NormalizedStepExecutor, StepExecution, *StoredExecutionPlan) {
	t.Helper()
	backend, value, build := options.backend, options.value, options.build
	fixture := newReleaseStoreFixtureAt(t, options.environmentID)
	fixture.addPlan(t, 1, strings.Repeat("e", 40))
	fixture.addPlan(t, 2, strings.Repeat("e", 40))
	buildJSON, _ := json.Marshal(build)
	if _, err := fixture.base.DB.Exec(`DELETE FROM deploy_build_plans WHERE environment_id=? AND revision=2`, fixture.envID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.base.DB.Exec(
		"INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at) VALUES(?, 2, 'recipe', ?, '{\"candidates\":[],\"gitRequirements\":{}}', ?, ?, ?)",
		fixture.envID, string(buildJSON), string(buildJSON), digestBytes(buildJSON), fixture.now.Unix()); err != nil {
		t.Fatal(err)
	}
	for _, variable := range []struct{ key, scopes, sensitivity, value string }{
		{"DATABASE_URL", "runtime,build", "secret", value},
		{"ADMIN_PASSWORD", "runtime", "secret", "admin-password-value"},
		{"SITE_URL", "runtime", "plain", "https://shop.example"},
	} {
		sealed, err := fixture.variables.sealer.Seal(variable.value)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.base.DB.Exec(
			`INSERT INTO deploy_variable_revisions(environment_id, key, revision, sensitivity, scopes, value_enc, value_digest, active, created_by, created_at) VALUES(?, ?, 1, ?, ?, ?, ?, 1, 'admin', ?)`,
			fixture.envID, variable.key, variable.sensitivity, variable.scopes, sealed, fakeContentDigest(variable.value), fixture.now.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	run, lease := fixture.claimedRun(t, 2)
	ctx := context.Background()
	pass := func(key StepKey, evidence any) {
		if _, err := fixture.runs.TransitionStep(ctx, run.ID, key, lease.Token, StepTransition{State: StepRunning}); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.runs.TransitionStep(ctx, run.ID, key, lease.Token, StepTransition{State: StepPassed, Evidence: mustJSON(evidence)}); err != nil {
			t.Fatal(err)
		}
	}
	workspace := options.workspace
	if workspace == "" {
		workspace = filepath.Join(t.TempDir(), fmt.Sprintf("run-%d", run.ID))
		if err := os.MkdirAll(filepath.Join(workspace, ".just-dashboard"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspace, ".just-dashboard", "Dockerfile"), []byte(databaseBuildRecipe), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	candidate := options.candidate
	if candidate == nil {
		candidate = map[string]any{
			"framework": "nextjs", "recipe": "node", "schemaTool": "drizzle", "seedCommand": "bun run db:seed", "seedResets": true,
			"nodeBuild": map[string]any{"cacheComponents": true},
		}
	}
	prepared := PreparedBuild{
		Method: BuildRecipe, Recipe: "node", Dockerfile: ".just-dashboard/Dockerfile", DockerfilePreview: databaseBuildRecipe,
		CachePolicy: "reuse", SecretBindings: []BuildSecretConfig{{Variable: "DATABASE_URL", Step: "build"}},
	}
	if options.prepared != nil {
		prepared = *options.prepared
	}
	pass(StepAnalyzePlan, map[string]any{"planRevision": 1, "findings": []any{}, "candidate": candidate})
	pass(StepPrepareContext, preparedStepEvidence{
		Source: MaterializedSource{Workspace: workspace, Root: workspace}, BuildRoot: workspace, Prepared: prepared,
	})
	step, err := fixture.runs.TransitionStep(ctx, run.ID, StepBuildArtifact, lease.Token, StepTransition{State: StepRunning})
	if err != nil {
		t.Fatal(err)
	}
	output := &storeStepOutput{ctx: ctx, store: fixture.runs, runID: run.ID, stepID: step.ID, claimToken: lease.Token}
	executor := &NormalizedStepExecutor{store: fixture.runs, variables: fixture.variables, builder: NewArtifactBuilder(backend)}
	plan := mustExecutionPlan(t, fixture, *run)
	return executor, StepExecution{Run: *run, Step: *step, ClaimToken: lease.Token, Output: output}, plan
}

func databaseBuildPlan() BuildPlanConfig {
	return BuildPlanConfig{
		Method: BuildRecipe, Recipe: "node", PackageManager: "bun", BuildCommand: "bun run build",
		StartCommand: "bunx drizzle-kit migrate && bun run start", Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{},
	}
}

func stepTranscript(t *testing.T, execution StepExecution) string {
	t.Helper()
	output := execution.Output.(*storeStepOutput)
	if err := output.Flush(); err != nil {
		t.Fatal(err)
	}
	rows, err := output.store.db.Query(`SELECT text FROM deploy_log_chunks WHERE run_id=? ORDER BY seq`, execution.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	lines := []string{}
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, text)
	}
	return strings.Join(lines, "\n")
}

// A Next.js site's first deploy: the build reaches its new database on the
// environment's network, reads a table its migration creates, gets the
// migration applied first, then needs rows only the seed loads into the
// still empty database, gets the seed, and builds.
func TestBuildReachesItsDatabaseAppliesTheSchemaAndSeedsANewDatabase(t *testing.T) {
	t.Parallel()
	backend := &databaseBuildBackend{attempts: []scriptedAttempt{schemaMissingAttempt, emptyParamsAttempt}}
	executor, execution, plan := databaseBuildFixture(t, backend, "postgres://app:hunter2@db-8.jd.internal:5432/app", databaseBuildPlan())
	networks, contents := &fakeBuildNetworks{}, &fakeDatabaseContents{}
	executor.buildNetworks, executor.databases = networks, contents
	result := executor.buildArtifact(context.Background(), execution, plan)
	if result.State != StepPassed {
		t.Fatalf("result = %+v\n%s", result, stepTranscript(t, execution))
	}
	if len(networks.values) != 1 || networks.values[0]["DATABASE_URL"] == "" {
		t.Fatalf("network asked with %+v", networks.values)
	}
	if len(backend.started) != 1 || backend.started[0].Networks[0] != "jd-e1-db-test" || backend.closed != 1 {
		t.Fatalf("builder started %+v, closed %d", backend.started, backend.closed)
	}
	for _, build := range backend.builds {
		if build.Builder != "jd-build-e1-db-test" {
			t.Fatalf("a build ran off the networked builder: %+v", build)
		}
	}
	if backend.buildCalls != 3 || len(backend.steps) != 2 {
		t.Fatalf("builds %d, steps %d", backend.buildCalls, len(backend.steps))
	}
	if !strings.HasSuffix(strings.TrimSpace(backend.stepFiles[0]), "bunx drizzle-kit migrate") ||
		!strings.HasSuffix(strings.TrimSpace(backend.stepFiles[1]), "bun run db:seed") {
		t.Fatalf("steps ran\n%s\n---\n%s", backend.stepFiles[0], backend.stepFiles[1])
	}
	for _, step := range backend.steps {
		if step.Builder == "" || step.Dockerfile != ".just-dashboard/"+schemaDockerfileName || len(step.BuildArgs) != 1 || step.BuildArgs[0].Name != schemaRunArg {
			t.Fatalf("step = %+v", step)
		}
		names := []string{}
		for _, secret := range step.Secrets {
			names = append(names, secret.ID)
		}
		// The step runs with what the start command would have: the
		// runtime values, not only the build's.
		if strings.Join(names, ",") == "" || !strings.Contains(","+strings.Join(names, ",")+",", ",ADMIN_PASSWORD,") {
			t.Fatalf("step secrets = %v", names)
		}
	}
	if len(contents.asked) != 1 || contents.asked[0] != 8 {
		t.Fatalf("rows asked of %v", contents.asked)
	}
	var evidence builtStepEvidence
	if err := json.Unmarshal(result.Evidence, &evidence); err != nil {
		t.Fatal(err)
	}
	if len(evidence.DatabaseSteps) != 2 || evidence.DatabaseSteps[0].Kind != "schema" || evidence.DatabaseSteps[1].Kind != "seed" {
		t.Fatalf("evidence = %+v", evidence.DatabaseSteps)
	}
	transcript := stepTranscript(t, execution)
	for _, want := range []string{
		"The build reaches db-8.jd.internal on the environment's network",
		"the schema is applied first with `bunx drizzle-kit migrate`",
		"the project's seed runs once with `bun run db:seed`",
	} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("transcript lacks %q:\n%s", want, transcript)
		}
	}
	for _, secret := range []string{"hunter2", "admin-password-value"} {
		if strings.Contains(transcript, secret) || strings.Contains(string(result.Evidence), secret) {
			t.Fatalf("%s reached the transcript or evidence", secret)
		}
	}
	// A plain value is the variable's own business and stays readable.
	if !strings.Contains(transcript, "SITE_URL=https://shop.example") {
		t.Fatalf("a plain value was redacted:\n%s", transcript)
	}
}

// guardedDatabaseBuildRecipe is databaseBuildRecipe as the recipes render it
// now: the install and a later stage each follow a build-value guard.
const guardedDatabaseBuildRecipe = `# syntax=docker/dockerfile:1.10
FROM node:22-alpine@sha256:1 AS build
WORKDIR /app
COPY package.json bun.lock ./
ARG JD_BUILD_VALUES_1
RUN --mount=type=secret,id=DATABASE_URL,env=DATABASE_URL,required=true bun install --frozen-lockfile
COPY . .
RUN --mount=type=secret,id=DATABASE_URL,env=DATABASE_URL,required=true bun run build
FROM node:22-alpine@sha256:1
ARG JD_BUILD_VALUES_2
RUN --mount=type=secret,id=DATABASE_URL,env=DATABASE_URL,required=true --mount=type=secret,id=SITE_URL,env=SITE_URL,required=true node prune.js
COPY --from=build /app /app
CMD ["/bin/sh","-c","bunx drizzle-kit migrate && bun run start"]
`

// The schema step runs the recipe's own instructions up to the build
// command, and BuildKit serves them from the build's cache only when each
// guard they follow carries the build's digest: the step is handed the
// guards its Dockerfile still declares, at the build's values, and no other.
func TestTheSchemaStepCarriesTheBuildsOwnGuards(t *testing.T) {
	t.Parallel()
	backend := &databaseBuildBackend{attempts: []scriptedAttempt{schemaMissingAttempt}}
	workspace := filepath.Join(t.TempDir(), "run-guarded")
	if err := os.MkdirAll(filepath.Join(workspace, ".just-dashboard"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".just-dashboard", "Dockerfile"), []byte(guardedDatabaseBuildRecipe), 0o600); err != nil {
		t.Fatal(err)
	}
	prepared := PreparedBuild{
		Method: BuildRecipe, Recipe: "node", Dockerfile: ".just-dashboard/Dockerfile", DockerfilePreview: guardedDatabaseBuildRecipe,
		CachePolicy: "reuse", SecretBindings: []BuildSecretConfig{{Variable: "DATABASE_URL", Step: "build"}},
		BuildValueGuards: []BuildValueGuard{
			{Arg: "JD_BUILD_VALUES_1", IDs: []string{"DATABASE_URL"}},
			{Arg: "JD_BUILD_VALUES_2", IDs: []string{"DATABASE_URL", "SITE_URL"}},
		},
	}
	executor, execution, plan := databaseBuildRun(t, databaseBuildOptions{
		backend: backend, value: "postgres://app:hunter2@db-8.jd.internal:5432/app", build: databaseBuildPlan(),
		workspace: workspace, prepared: &prepared,
	})
	executor.buildNetworks, executor.databases = &fakeBuildNetworks{}, &fakeDatabaseContents{}
	result := executor.buildArtifact(context.Background(), execution, plan)
	if result.State != StepPassed {
		t.Fatalf("result = %+v\n%s", result, stepTranscript(t, execution))
	}
	if len(backend.builds) == 0 || len(backend.steps) != 1 {
		t.Fatalf("builds %d, steps %d", len(backend.builds), len(backend.steps))
	}
	built := map[string]string{}
	for _, arg := range backend.builds[0].BuildArgs {
		built[arg.Name] = arg.Value
	}
	if built["JD_BUILD_VALUES_1"] == "" || built["JD_BUILD_VALUES_2"] == "" {
		t.Fatalf("the build's guards = %+v", backend.builds[0].BuildArgs)
	}
	step := backend.steps[0]
	if len(step.BuildArgs) != 2 || step.BuildArgs[0].Name != "JD_BUILD_VALUES_1" || step.BuildArgs[0].Value != built["JD_BUILD_VALUES_1"] ||
		step.BuildArgs[1].Name != schemaRunArg {
		t.Fatalf("the schema step's arguments = %+v, the build's = %+v", step.BuildArgs, backend.builds[0].BuildArgs)
	}
}

// A database that holds any row is never seeded: the build's own failure,
// named, is what the run ends with.
func TestBuildNeverSeedsADatabaseThatHoldsRows(t *testing.T) {
	t.Parallel()
	backend := &databaseBuildBackend{attempts: []scriptedAttempt{emptyParamsAttempt}}
	executor, execution, plan := databaseBuildFixture(t, backend, "postgres://app:hunter2@db-8.jd.internal:5432/app", databaseBuildPlan())
	executor.buildNetworks, executor.databases = &fakeBuildNetworks{}, &fakeDatabaseContents{hasRows: true}
	result := executor.buildArtifact(context.Background(), execution, plan)
	if result.State != StepFailed || result.ErrorCode != "build_static_params_empty" || len(backend.steps) != 0 {
		t.Fatalf("result = %+v, steps %d", result, len(backend.steps))
	}
	if !strings.Contains(result.ErrorMessage, "/[locale]/blog/[slug]") || strings.Contains(result.ErrorMessage, "force-dynamic") {
		t.Fatalf("message = %s", result.ErrorMessage)
	}
	if transcript := stepTranscript(t, execution); !strings.Contains(transcript, "a seed only ever runs on a new one") {
		t.Fatalf("transcript:\n%s", transcript)
	}
}

// A build given no database of the dashboard's stays on the server's own
// builder, and the schema still comes first when the database it reached —
// a hosted one — lacks a table.
func TestBuildOffTheNetworkStaysOnTheDefaultBuilder(t *testing.T) {
	t.Parallel()
	backend := &databaseBuildBackend{attempts: []scriptedAttempt{schemaMissingAttempt}}
	executor, execution, plan := databaseBuildFixture(t, backend, "postgres://app:hunter2@ep-cool.neon.tech/app", databaseBuildPlan())
	networks := &fakeBuildNetworks{}
	executor.buildNetworks = networks
	result := executor.buildArtifact(context.Background(), execution, plan)
	if result.State != StepPassed || len(networks.values) != 0 || len(backend.started) != 0 || len(backend.steps) != 1 {
		t.Fatalf("result = %+v, networks %d, started %d, steps %d", result, len(networks.values), len(backend.started), len(backend.steps))
	}
	for _, build := range backend.builds {
		if build.Builder != "" {
			t.Fatalf("a build left the default builder: %+v", build)
		}
	}
}

// A schema step that fails ends the build with its own name and the
// command's last words; a refusal to join the network ends it before any
// build runs.
func TestBuildDatabaseStepFailuresAreNamed(t *testing.T) {
	t.Parallel()
	backend := &databaseBuildBackend{attempts: []scriptedAttempt{schemaMissingAttempt}, stepErr: &dockerx.BuildError{Step: 9, Command: "bunx drizzle-kit migrate", ExitCode: 1}}
	executor, execution, plan := databaseBuildFixture(t, backend, "postgres://app:hunter2@db-8.jd.internal:5432/app", databaseBuildPlan())
	executor.buildNetworks = &fakeBuildNetworks{}
	result := executor.buildArtifact(context.Background(), execution, plan)
	if result.State != StepFailed || result.ErrorCode != "build_schema_step_failed" ||
		!strings.Contains(result.ErrorMessage, "`bunx drizzle-kit migrate` failed (exit code 1)") || strings.Contains(result.ErrorMessage, "hunter2") {
		t.Fatalf("result = %+v", result)
	}
	if backend.buildCalls != 1 || backend.closed != 1 {
		t.Fatalf("builds %d, closed %d", backend.buildCalls, backend.closed)
	}

	refused := &databaseBuildBackend{}
	executor, execution, plan = databaseBuildFixture(t, refused, "postgres://app:hunter2@db-8.jd.internal:5432/app", databaseBuildPlan())
	executor.buildNetworks = &fakeBuildNetworks{err: errors.New("a staging environment cannot share production's database")}
	result = executor.buildArtifact(context.Background(), execution, plan)
	if result.State != StepFailed || result.ErrorCode != "build_database_network" || !strings.Contains(result.ErrorMessage, "staging environment") || refused.buildCalls != 0 {
		t.Fatalf("result = %+v, builds %d", result, refused.buildCalls)
	}
}
