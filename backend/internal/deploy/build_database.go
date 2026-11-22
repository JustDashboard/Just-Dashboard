package deploy

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// A build that reads its database — a page Next.js prerenders, a Nuxt or
// Astro route generated at build time, Rails or Django loading its models —
// needs the same database the application has. A linked database lives on
// its environment's own network under db-N.jd.internal, which only the
// containers joined to it reach, and Docker's builder cannot join a
// user-defined network. So a build handed a linked database runs on a builder
// of its own on that network (dockerx.StartNetworkedBuilder), and a build
// that then reads a table the migrations have not created yet gets the
// schema applied first, from its own layers, and builds again. A build that
// then needs rows — Next.js with Cache Components refuses a
// generateStaticParams that returns none, a page reads a settings row — on a
// database that has none yet gets the project's own seed, once, and builds
// again; a database that holds any row is never seeded.

// BuildNetwork is the environment networks a build joins to reach the
// databases its build values name.
type BuildNetwork struct {
	EnvironmentID int64
	Networks      []string
	// Hosts are the database hosts the build values name, for the step's
	// own account of why it joined.
	Hosts []string
}

// networkedBuildBackend is a build backend that can run a build on its
// environment's networks and run a schema step in the build's own layers.
type networkedBuildBackend interface {
	StartNetworkedBuilder(context.Context, BuildNetwork) (string, func(context.Context), error)
	RunCacheOnly(context.Context, BuildInvocation, func(BuildLog) error) error
}

// BuildNetworkOwner joins an environment's linked databases to its network
// and names the networks a build has to join to reach the ones its values
// name, under the same isolation rules a release's containers are held to.
type BuildNetworkOwner interface {
	NetworksForBuild(ctx context.Context, environmentID int64, values map[string]string) ([]string, error)
}

// DatabaseContents says whether a linked database already holds any row of
// the application's: migration bookkeeping does not count, since a schema
// step writes it. An engine it cannot judge holds rows, so nothing that
// depends on a database being new ever runs against one it cannot read.
type DatabaseContents interface {
	DatabaseHasRows(ctx context.Context, connectionID int64) (bool, error)
}

// WithDatabaseContents is the executor judging whether a database is new
// before a build loads the project's seed into it.
func (e *NormalizedStepExecutor) WithDatabaseContents(contents DatabaseContents) *NormalizedStepExecutor {
	e.databases = contents
	return e
}

// NetworksForBuild is NetworksForRuntime for a build: the same bindings and
// refusals, with no runtime of its own to consider.
func (o *DockerRuntimeOwner) NetworksForBuild(ctx context.Context, environmentID int64, values map[string]string) ([]string, error) {
	if o == nil || o.networks == nil {
		return nil, nil
	}
	return o.networks.NetworksForRuntime(ctx, environmentID, RuntimePlanConfig{}, values)
}

// StartNetworkedBuilder is the builder running builds on the network's
// builder, and the function that removes that builder again.
func (b *ArtifactBuilder) StartNetworkedBuilder(ctx context.Context, network BuildNetwork) (*ArtifactBuilder, func(context.Context), error) {
	if b == nil || b.backend == nil {
		return nil, nil, ErrBuilderUnavailable
	}
	backend, ok := b.backend.(networkedBuildBackend)
	if !ok {
		return nil, nil, fmt.Errorf("%w: this server's builder cannot join a database network", ErrBuilderUnavailable)
	}
	name, closeBuilder, err := backend.StartNetworkedBuilder(ctx, network)
	if err != nil {
		return nil, nil, err
	}
	return b.withBuilder(name), closeBuilder, nil
}

// runSchemaStep runs the schema Dockerfile for its side effect on the
// database, on the same builder the build runs on.
func (b *ArtifactBuilder) runSchemaStep(ctx context.Context, invocation BuildInvocation, emit func(BuildLog) error) error {
	if b == nil || b.backend == nil {
		return ErrBuilderUnavailable
	}
	backend, ok := b.backend.(networkedBuildBackend)
	if !ok {
		return fmt.Errorf("%w: this server's builder cannot run a schema step", ErrBuilderUnavailable)
	}
	invocation.Builder = b.builderName
	return backend.RunCacheOnly(ctx, invocation, emit)
}

// buildNetwork is the network a build joins: the environment's database
// network when a build value names a linked database, nil otherwise.
func (e *NormalizedStepExecutor) buildNetwork(ctx context.Context, run EngineRun, values map[string]string) (*BuildNetwork, error) {
	hosts := buildDatabaseHosts(values)
	if len(hosts) == 0 {
		return nil, nil
	}
	owner := e.buildNetworks
	if owner == nil {
		runtime, ok := e.runtime.(BuildNetworkOwner)
		if !ok || runtime == nil {
			return nil, nil
		}
		owner = runtime
	}
	networks, err := owner.NetworksForBuild(ctx, run.EnvironmentID, values)
	if err != nil || len(networks) == 0 {
		return nil, err
	}
	return &BuildNetwork{EnvironmentID: run.EnvironmentID, Networks: networks, Hosts: hosts}, nil
}

// buildDatabaseHosts are the dashboard's own database hosts the values name:
// a linked database, its pooler or a preview's branch, never anything else.
func buildDatabaseHosts(values map[string]string) []string {
	hosts := []string{}
	for _, value := range values {
		if !strings.Contains(value, "://") {
			continue
		}
		parsed, err := url.Parse(strings.TrimSpace(value))
		if err != nil {
			continue
		}
		host := strings.ToLower(parsed.Hostname())
		if strings.HasSuffix(host, ".jd.internal") && !slices.Contains(hosts, host) {
			hosts = append(hosts, host)
		}
	}
	sort.Strings(hosts)
	return hosts
}

// buildNetworkFailure is a build that could not join its database's network:
// refused for the reasons a release would be (a staging environment sharing
// production's database, a data copy), or a builder that did not start.
func buildNetworkFailure(err error) StepResult {
	if errors.Is(err, context.Canceled) {
		return normalizedStepFailure(err)
	}
	return StepResult{
		State: StepFailed, ErrorCode: "build_database_network",
		ErrorMessage: truncateUTF8Prefix("The build reads a linked database, and could not join the environment's network to reach it: "+err.Error(), causeSentenceLength),
	}
}

// buildSchemaStep is the command the plan applies its schema with, where the
// plan runs it and the variables it runs with there.
type buildSchemaStep struct {
	Command string
	// Scope is "runtime" for a step the start command runs first,
	// "release_task" for a release task's.
	Scope string
	// Names are a release task's own release_task variables.
	Names []string
}

// planSchemaStep is the schema step the plan already runs after the build:
// the start command's leading migration, or a release task that runs one in
// the release image. A step that reads what the build writes (Adonis's
// build/ace.js, Medusa's .medusa/server) cannot run before the build.
func planSchemaStep(build BuildPlanConfig) (buildSchemaStep, bool) {
	segments := strings.Split(build.StartCommand, "&&")
	// The last segment is the server itself.
	for _, segment := range segments[:max(len(segments)-1, 0)] {
		command := strings.TrimSpace(segment)
		if appliesSchemaBeforeBuild(command) {
			return buildSchemaStep{Command: command, Scope: "runtime"}, true
		}
	}
	for _, task := range build.ReleaseTasks {
		if task.Runner != ReleaseTaskRunnerImage || task.WorkingDirectory != "" {
			continue
		}
		if appliesSchemaBeforeBuild(task.Command) {
			return buildSchemaStep{Command: strings.TrimSpace(task.Command), Scope: "release_task", Names: task.Env}, true
		}
	}
	return buildSchemaStep{}, false
}

// appliesSchemaBeforeBuild says a command applies a schema tool's migrations
// and none of the tools it could be is one that migrates from what the build
// writes: `migration:run` is TypeORM's as well as Adonis's build/ace.js.
func appliesSchemaBeforeBuild(command string) bool {
	if strings.TrimSpace(command) == "" {
		return false
	}
	applied := false
	for _, tools := range [][]schemaTool{schemaTools, runtimeSchemaTools} {
		for index := range tools {
			if tools[index].applied == nil || !tools[index].applied(command) {
				continue
			}
			if tools[index].direct {
				return false
			}
			applied = true
		}
	}
	return applied
}

// schemaRunArg is the build argument that keeps a schema step from ever
// being served from the cache: its value is new each time, and a migration
// that was skipped because an earlier run's step was cached would leave the
// build reading the same missing table.
const schemaRunArg = "JD_SCHEMA_RUN"

// schemaDockerfile is the recipe's Dockerfile cut at the build command's
// step, with the schema step in its place: everything before it is the
// build's own instructions, so BuildKit serves them from the cache the build
// just filled and the schema command runs where the build command would,
// with the dependencies the install left. It is false when the build
// command's step is not one line of the file.
func schemaDockerfile(content, buildCommand, schemaCommand string, secretIDs []string) (string, bool) {
	buildCommand, schemaCommand = strings.TrimSpace(buildCommand), strings.TrimSpace(schemaCommand)
	if buildCommand == "" || schemaCommand == "" || strings.ContainsAny(schemaCommand, "\n\r") {
		return "", false
	}
	lines := strings.Split(content, "\n")
	cut := -1
	for index, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "RUN ") && strings.HasSuffix(line, buildCommand) && !strings.HasSuffix(line, "\\") {
			cut = index
		}
	}
	if cut <= 0 {
		return "", false
	}
	ids := append([]string(nil), secretIDs...)
	sort.Strings(ids)
	mounts := ""
	for _, id := range ids {
		mounts += "--mount=type=secret,id=" + id + ",env=" + id + ",required=true "
	}
	schema := append(append([]string(nil), lines[:cut]...), "ARG "+schemaRunArg, "RUN "+mounts+schemaCommand)
	return strings.Join(schema, "\n") + "\n", true
}

// schemaDockerfileName is where the schema Dockerfile is written, beside the
// recipe's own.
const schemaDockerfileName = "Dockerfile.schema"

// schemaRetry is what running a database step before the build decided.
type schemaRetry struct {
	// applied says the step ran and the build should run again.
	applied bool
	// failure ends the step: the backup check refused, or the database step
	// itself failed.
	failure *StepResult
	// reason is why the step was not run, for the step's log.
	reason string
	// step is the step that ran, for the build's evidence.
	step builtDatabaseStep
}

// builtDatabaseStep is a database step that ran before a build, recorded in
// the build's evidence.
type builtDatabaseStep struct {
	Kind    string `json:"kind"`
	Command string `json:"command"`
}

// applySchemaBeforeBuild applies the plan's schema step before the build is
// run again, when the build failed reading a table or column the database
// does not have yet. It runs the command the plan would run after the build
// anyway — the start command's leading migration or the release task — in
// the build's own layers, with the variables that step gets, on the same
// builder, after the backup the plan requires. A plan that rehearses its
// migrations first keeps that order, and the build's failure stands.
func (e *NormalizedStepExecutor) applySchemaBeforeBuild(
	ctx context.Context,
	execution StepExecution,
	plan *StoredExecutionPlan,
	prepared preparedStepEvidence,
	builder *ArtifactBuilder,
	redacted map[string]string,
) schemaRetry {
	if plan.Build.Method != BuildRecipe {
		return schemaRetry{reason: "only a recipe build can apply the schema before its build command; run the migration in the build command before the build"}
	}
	step, ok := planSchemaStep(plan.Build)
	if !ok {
		return schemaRetry{reason: "the plan runs no migration before the application starts that the build could run first"}
	}
	if len(plan.Build.ReleaseTasks) > 0 && e.store != nil {
		policy, err := e.store.RehearsalPolicy(ctx, execution.Run.EnvironmentID)
		if err != nil {
			return schemaRetry{failure: ptr(normalizedStepFailure(err))}
		}
		if policy.Enabled {
			return schemaRetry{reason: "this environment rehearses its migrations on a copy before they run, which needs the built image"}
		}
	}
	return e.runDatabaseStep(ctx, execution, plan, prepared, builder, redacted, databaseStepRun{
		step: step, kind: "schema", gate: hasBackupGate(plan),
		intro:   "The build read a table its migrations create, so the schema is applied first with `" + step.Command + "`, then the build runs again",
		failure: "The build reads tables its migrations create, and applying them first with `" + boundedCauseCommand(step.Command) + "` failed",
		code:    "build_schema_step_failed",
	})
}

// seedBeforeBuild runs the project's seed before the build is run again, when
// the build failed for want of rows and every database it reaches has none:
// a new database, which the seed's own reset cannot cost anything. A
// database holding any row of the application's is never seeded.
func (e *NormalizedStepExecutor) seedBeforeBuild(
	ctx context.Context,
	execution StepExecution,
	plan *StoredExecutionPlan,
	prepared preparedStepEvidence,
	builder *ArtifactBuilder,
	redacted map[string]string,
	network *BuildNetwork,
) schemaRetry {
	if plan.Build.Method != BuildRecipe {
		return schemaRetry{reason: "only a recipe build can load a seed before its build command"}
	}
	candidate := e.causeCandidate(ctx, execution.Run.ID, plan)
	if candidate == nil || strings.TrimSpace(candidate.SeedCommand) == "" {
		return schemaRetry{reason: "the project declares no seed that loads a new database's first rows"}
	}
	if e.databases == nil || network == nil {
		return schemaRetry{reason: "the database's rows could not be counted, so its seed is not run"}
	}
	ids := databaseConnectionIDs(network.Hosts)
	if len(ids) == 0 {
		return schemaRetry{reason: "the build reaches no database of this server's that the seed could be judged against"}
	}
	for _, id := range ids {
		hasRows, err := e.databases.DatabaseHasRows(ctx, id)
		if err != nil {
			return schemaRetry{reason: "the database's rows could not be counted (" + truncateUTF8Prefix(err.Error(), 160) + "), so its seed is not run"}
		}
		if hasRows {
			return schemaRetry{reason: "the database already holds rows, and a seed only ever runs on a new one"}
		}
	}
	command := strings.TrimSpace(candidate.SeedCommand)
	return e.runDatabaseStep(ctx, execution, plan, prepared, builder, redacted, databaseStepRun{
		step: buildSchemaStep{Command: command, Scope: "runtime"}, kind: "seed", gate: hasBackupGate(plan),
		intro:   "The build needs rows the database does not have yet, and the database is new with none at all, so the project's seed runs once with `" + command + "`, then the build runs again",
		failure: "The build needs the rows the project's seed loads, and running it with `" + boundedCauseCommand(command) + "` failed",
		code:    "build_seed_step_failed",
	})
}

// databaseConnectionIDs are the saved connections a build's database hosts
// name: db-N.jd.internal and its pooler. A preview's branch or a data copy
// is no connection a seed could be judged against.
func databaseConnectionIDs(hosts []string) []int64 {
	ids := []int64{}
	for _, host := range hosts {
		name := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(host, "db-"), ".jd.internal"), "-pool")
		id, err := strconv.ParseInt(name, 10, 64)
		if err != nil || id <= 0 || slices.Contains(ids, id) {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

// databaseStepRun is one database step: what it runs, what it is called in
// the step's log and how its failure is named.
type databaseStepRun struct {
	step                 buildSchemaStep
	kind, intro, failure string
	code                 string
	gate                 bool
}

// runDatabaseStep runs a command against the build's databases from the
// build's own layers: the recipe's Dockerfile cut at the build command, with
// the command in its place, on the builder the build runs on.
func (e *NormalizedStepExecutor) runDatabaseStep(
	ctx context.Context,
	execution StepExecution,
	plan *StoredExecutionPlan,
	prepared preparedStepEvidence,
	builder *ArtifactBuilder,
	redacted map[string]string,
	run databaseStepRun,
) schemaRetry {
	step := run.step
	content, err := readContainedRegular(prepared.BuildRoot, prepared.Prepared.Dockerfile, 2<<20)
	if err != nil {
		return schemaRetry{reason: "the recipe's Dockerfile could not be read"}
	}
	values, secret, err := e.schemaStepValues(ctx, execution.Run, step)
	if err != nil {
		return schemaRetry{failure: ptr(normalizedStepFailure(err))}
	}
	for name, value := range buildLogRedactions(values, secret) {
		redacted[name] = value
	}
	ids := make([]string, 0, len(values))
	secrets := make([]BuildSecretValue, 0, len(values))
	for name, value := range values {
		// BuildKit refuses an empty secret, and a name it cannot carry is no
		// variable the step could have read.
		if value == "" || ValidateEnvKey(name) != nil {
			continue
		}
		ids = append(ids, name)
		secrets = append(secrets, BuildSecretValue{ID: name, Step: "build", Value: value})
	}
	dockerfile, ok := schemaDockerfile(string(content), plan.Build.BuildCommand, step.Command, ids)
	if !ok {
		return schemaRetry{reason: "the build command is not one step of the recipe's Dockerfile"}
	}
	// The recipe writes its Dockerfile into .just-dashboard, which is where
	// writeGeneratedFile writes, so the step's Dockerfile sits beside it.
	if path.Dir(path.Clean(prepared.Prepared.Dockerfile)) != ".just-dashboard" {
		return schemaRetry{reason: "the recipe's Dockerfile is not where the dashboard writes one"}
	}
	schemaPath := ".just-dashboard/" + schemaDockerfileName
	if err := writeGeneratedFile(prepared.BuildRoot, schemaDockerfileName, dockerfile); err != nil {
		return schemaRetry{failure: ptr(normalizedStepFailure(err))}
	}
	if run.gate {
		_ = stepLog(execution, "status", "Checking the backup this environment requires before its data changes")
		gate := e.backupGate(ctx, execution, plan)
		if gate.State != StepPassed && gate.State != StepSkipped {
			gate.ErrorMessage = run.failure + ": the backup required before its data changes did not pass: " + gate.ErrorMessage
			return schemaRetry{failure: &gate}
		}
	}
	if err := stepLog(execution, "status", run.intro); err != nil {
		return schemaRetry{failure: ptr(normalizedStepFailure(err))}
	}
	output := newLineRing(outputRingLines, outputRingBytes)
	emit := redactBuildEmitter(redacted, func(line BuildLog) error {
		if err := stepLog(execution, line.Stream, line.Text); err != nil {
			return err
		}
		output.add(collectedLine{text: truncateUTF8Prefix(line.Text, collectedLineBytes)})
		return nil
	})
	buildValues, _, err := e.buildScopeVariables(ctx, execution.Run)
	if err != nil {
		return schemaRetry{failure: ptr(normalizedStepFailure(err))}
	}
	buildArgs := append(builder.declaredBuildValueGuardArgs(prepared.Prepared, buildValues, dockerfile),
		BuildArgValue{Name: schemaRunArg, Value: strconv.FormatInt(execution.Run.ID, 10) + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)})
	err = builder.runSchemaStep(ctx, BuildInvocation{
		ContextDir: prepared.BuildRoot, Dockerfile: schemaPath, Platform: prepared.Prepared.TargetPlatform,
		Secrets: secrets, BuildArgs: buildArgs,
	}, emit)
	if err != nil {
		if ctx.Err() != nil {
			return schemaRetry{failure: ptr(normalizedStepFailure(ctx.Err()))}
		}
		message := run.failure
		var exit *dockerx.BuildError
		if errors.As(err, &exit) && exit.ExitCode > 0 {
			message += fmt.Sprintf(" (exit code %d)", exit.ExitCode)
		}
		message += "; its output is in the build log above"
		if line := lastMeaningfulLine(output); line != "" {
			message += ": " + line
		}
		message = truncateUTF8Prefix(buildRedactor(redacted)(message), causeSentenceLength)
		_ = stepLog(execution, "status", "Diagnosis: "+message)
		return schemaRetry{failure: &StepResult{
			State: StepFailed, ErrorCode: run.code, ErrorMessage: message,
			Evidence: mustJSON(map[string]any{"databaseStep": builtDatabaseStep{Kind: run.kind, Command: step.Command}, "scope": step.Scope}),
		}}
	}
	_ = stepLog(execution, "status", "The "+run.kind+" step finished; building again")
	return schemaRetry{applied: true, step: builtDatabaseStep{Kind: run.kind, Command: step.Command}}
}

// schemaStepValues are the values the schema step gets where the plan runs
// it — the application's runtime values, with a release task's own
// release_task values over them — and which of them are secret.
func (e *NormalizedStepExecutor) schemaStepValues(ctx context.Context, run EngineRun, step buildSchemaStep) (map[string]string, map[string]bool, error) {
	if e.variables == nil {
		return nil, nil, fmt.Errorf("%w: variable store is unavailable", ErrArtifactMissing)
	}
	values, secret := map[string]string{}, map[string]bool{}
	scoped, err := e.variables.OpenRunScopedVariables(ctx, run.ID, run.EnvironmentID, "runtime")
	if err != nil {
		return nil, nil, err
	}
	for _, value := range scoped {
		values[value.Name], secret[value.Name] = value.Value, value.Sensitivity != "plain"
	}
	if step.Scope == "release_task" && len(step.Names) > 0 {
		task, err := e.variables.OpenRunScopedVariables(ctx, run.ID, run.EnvironmentID, "release_task")
		if err != nil {
			return nil, nil, err
		}
		for _, value := range task {
			if slices.Contains(step.Names, value.Name) {
				values[value.Name], secret[value.Name] = value.Value, value.Sensitivity != "plain"
			}
		}
	}
	return values, secret, nil
}

// hasBackupGate says the plan requires a backup before a release changes its
// data, which applying the schema before the build would otherwise run ahead
// of.
func hasBackupGate(plan *StoredExecutionPlan) bool {
	dependencies, _, _, err := decodeStoredPlanInputs(plan)
	if err != nil {
		return true
	}
	for _, dependency := range dependencies {
		if dependency.Kind == "backup" || dependency.ResourceKind == "backup_job" {
			return true
		}
	}
	return false
}

// lastMeaningfulLine is the last line of a step's output that says
// something, bounded for a sentence.
func lastMeaningfulLine(output *lineRing) string {
	for index := len(output.lines) - 1; index >= 0; index-- {
		text := strings.TrimSpace(output.lines[index].text)
		if text == "" || strings.HasPrefix(text, "#") && len(strings.Fields(text)) <= 3 {
			continue
		}
		return truncateUTF8Prefix(text, 200)
	}
	return ""
}

func ptr[T any](value T) *T { return &value }
