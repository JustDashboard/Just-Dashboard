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
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// scriptedBuildBackend prints a BuildKit transcript and fails the way buildx
// does, so the executor reads a failed build exactly as it would a real one.
type scriptedBuildBackend struct {
	artifactBackendFake
	lines []string
	err   error
}

func (b *scriptedBuildBackend) BuildImage(_ context.Context, _ BuildInvocation, emit func(BuildLog) error) (ResolvedImage, error) {
	for _, line := range b.lines {
		if err := emit(BuildLog{Stream: "stderr", Text: line}); err != nil {
			return ResolvedImage{}, err
		}
	}
	return ResolvedImage{}, b.err
}

// failedBuildFixture is a claimed run whose plan builds with the Node recipe
// and whose analyze_plan recorded the candidate it detected, with the
// executor's steps up to the build already passed.
func failedBuildFixture(t *testing.T, backend BuildBackend, candidate map[string]any) (*NormalizedStepExecutor, StepExecution, *StoredExecutionPlan, *releaseStoreFixture) {
	t.Helper()
	fixture := newReleaseStoreFixture(t)
	fixture.addPlan(t, 1, strings.Repeat("e", 40))
	build := BuildPlanConfig{
		Method: BuildRecipe, Recipe: "node", PackageManager: "npm", BuildCommand: "npm run build",
		StartCommand: "npm run start", Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{},
	}
	// Plan revisions are immutable, so the recipe plan is the next one.
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
	sealed, err := fixture.variables.sealer.Seal("postgres://app@db/app")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.base.DB.Exec(
		`INSERT INTO deploy_variable_revisions(environment_id, key, revision, sensitivity, scopes, value_enc, value_digest, active, created_by, created_at) VALUES(?, 'DATABASE_URL', 1, 'secret', 'runtime', ?, ?, 1, 'admin', ?)`,
		fixture.envID, sealed, fakeContentDigest("postgres://app@db/app"), fixture.now.Unix()); err != nil {
		t.Fatal(err)
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
	workspace := filepath.Join(t.TempDir(), fmt.Sprintf("run-%d", run.ID))
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	pass(StepAnalyzePlan, map[string]any{"planRevision": 1, "findings": []any{}, "candidate": candidate})
	pass(StepPrepareContext, preparedStepEvidence{
		Source: MaterializedSource{Workspace: workspace, Root: workspace}, BuildRoot: workspace,
		Prepared: PreparedBuild{Method: BuildRecipe, Recipe: "node", DockerfilePreview: "FROM node@sha256:1 AS build\nRUN npm ci\nRUN npm run build\n", CachePolicy: "reuse"},
	})
	step, err := fixture.runs.TransitionStep(ctx, run.ID, StepBuildArtifact, lease.Token, StepTransition{State: StepRunning})
	if err != nil {
		t.Fatal(err)
	}
	output := &storeStepOutput{ctx: ctx, store: fixture.runs, runID: run.ID, stepID: step.ID, claimToken: lease.Token}
	executor := &NormalizedStepExecutor{store: fixture.runs, variables: fixture.variables, builder: NewArtifactBuilder(backend)}
	plan := mustExecutionPlan(t, fixture, *run)
	return executor, StepExecution{Run: *run, Step: *step, ClaimToken: lease.Token, Output: output}, plan, fixture
}

func TestBuildArtifactNamesTheFailureAndItsRemedy(t *testing.T) {
	t.Parallel()
	backend := &scriptedBuildBackend{
		lines: []string{
			"#10 [4/5] RUN npm ci",
			"#10 1.303 npm error code EUSAGE",
			"#10 1.303 npm error `npm ci` can only install packages when your package.json and package-lock.json or npm-shrinkwrap.json are in sync.",
			"#10 1.303 npm error Missing: @prisma/adapter-pg@7.0.0 from lock file",
			`#10 ERROR: process "/bin/sh -c npm ci" did not complete successfully: exit code: 1`,
		},
		err: &dockerx.BuildError{Step: 10, Instruction: "RUN npm ci", Command: "npm ci", ExitCode: 1, BuildxExit: 1},
	}
	candidate := map[string]any{
		"recipe": "node",
		"lockfiles": []map[string]any{
			{"path": "bun.lock", "manager": "bun", "state": "in_sync"},
			{"path": "package-lock.json", "manager": "npm", "state": "stale", "missing": []string{"@prisma/adapter-pg"}},
		},
	}
	executor, execution, plan, fixture := failedBuildFixture(t, backend, candidate)
	result := executor.buildArtifact(context.Background(), execution, plan)
	if result.State != StepFailed || result.ErrorCode != "build_lockfile_out_of_sync" {
		t.Fatalf("result = %+v", result)
	}
	for _, want := range []string{
		"The install step (`npm ci`) exited with code 1",
		"package-lock.json is out of sync with package.json (missing @prisma/adapter-pg)",
		"Build with Bun, whose bun.lock matches package.json",
	} {
		if !strings.Contains(result.ErrorMessage, want) {
			t.Fatalf("message lacks %q: %q", want, result.ErrorMessage)
		}
	}
	if result.ErrorMessage == "exit status 1" || result.ErrorCode == "internal_error" {
		t.Fatal("the incident's reading survived")
	}
	var evidence struct {
		Cause BuildCause `json:"cause"`
	}
	if err := json.Unmarshal(result.Evidence, &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.Cause.Fix == nil || evidence.Cause.Fix.Value != "bun" || evidence.Cause.Fix.Field != "configuration.build.packageManager" {
		t.Fatalf("fix = %+v", evidence.Cause.Fix)
	}
	var seq int64
	if err := fixture.base.DB.QueryRow(`SELECT seq FROM deploy_log_chunks WHERE run_id=? AND text LIKE '%can only install packages%'`, execution.Run.ID).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	if evidence.Cause.LineSeq != seq {
		t.Fatalf("cause points at line %d, the decisive line is %d", evidence.Cause.LineSeq, seq)
	}
	var diagnosis int
	if err := fixture.base.DB.QueryRow(`SELECT COUNT(*) FROM deploy_log_chunks WHERE run_id=? AND stream='status' AND text LIKE 'Diagnosis: The install step%'`, execution.Run.ID).Scan(&diagnosis); err != nil || diagnosis != 1 {
		t.Fatalf("transcript diagnosis lines = %d, %v", diagnosis, err)
	}
	var cleanup struct {
		WorkspaceRemoved bool `json:"workspaceRemoved"`
	}
	if json.Unmarshal(result.Cleanup, &cleanup) != nil || !cleanup.WorkspaceRemoved {
		t.Fatalf("cleanup = %s", result.Cleanup)
	}
}

func TestBuildArtifactReadsTheStoredCandidateWithoutAnalyzeEvidence(t *testing.T) {
	t.Parallel()
	backend := &scriptedBuildBackend{
		lines: []string{"#12 [4/4] RUN npm run build", "#12 3.2 Error: PrismaConfigEnvError: Missing required environment variable: DATABASE_URL"},
		err:   &dockerx.BuildError{Step: 12, Command: "npm run build", ExitCode: 1, BuildxExit: 1},
	}
	executor, execution, plan, _ := failedBuildFixture(t, backend, nil)
	result := executor.buildArtifact(context.Background(), execution, plan)
	var evidence struct {
		Cause BuildCause `json:"cause"`
	}
	_ = json.Unmarshal(result.Evidence, &evidence)
	if result.ErrorCode != "build_env_missing" || evidence.Cause.Fix == nil ||
		evidence.Cause.Fix.Kind != fixVariableScope || evidence.Cause.Fix.Field != "variables.DATABASE_URL" {
		t.Fatalf("result = %+v, cause %+v", result, evidence.Cause)
	}
	if !strings.Contains(result.ErrorMessage, "Give DATABASE_URL the build scope in Variables") {
		t.Fatalf("message = %q", result.ErrorMessage)
	}
}

func TestBuildArtifactTimeoutIsAFailureNotACancellation(t *testing.T) {
	t.Parallel()
	backend := &scriptedBuildBackend{
		lines: []string{"#9 [4/5] RUN npm run build", "#9 1790.1 compiling"},
		err:   &dockerx.BuildTimeoutError{Result: hostexec.GroupResult{TERMSent: true, ExitCode: -1}},
	}
	executor, execution, plan, _ := failedBuildFixture(t, backend, nil)
	result := executor.buildArtifact(context.Background(), execution, plan)
	if result.State != StepFailed || result.ErrorCode != "build_timeout" {
		t.Fatalf("result = %+v", result)
	}
	if strings.Contains(strings.ToLower(result.ErrorMessage), "cancel") || !strings.Contains(result.ErrorMessage, "`npm run build`") {
		t.Fatalf("message = %q", result.ErrorMessage)
	}
	var cleanup struct {
		ProcessGroup *hostexec.GroupResult `json:"processGroup"`
	}
	if json.Unmarshal(result.Cleanup, &cleanup) != nil || cleanup.ProcessGroup == nil || !cleanup.ProcessGroup.TERMSent {
		t.Fatalf("cleanup = %s", result.Cleanup)
	}
}

func TestNormalizedStepFailureNamesWhatUsedToBeAnInternalError(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		err   error
		state StepState
		code  string
	}{
		"build error without a collector": {&dockerx.BuildError{Command: "npm ci", ExitCode: 1}, StepFailed, "build_failed"},
		"build timeout":                   {&dockerx.BuildTimeoutError{}, StepFailed, "build_timeout"},
		"an internal deadline":            {fmt.Errorf("inspect: %w", context.DeadlineExceeded), StepFailed, "step_timeout"},
		"a cancellation":                  {context.Canceled, StepCancelled, "cancelled"},
		"a source the remote refused":     {&SourceFailure{Code: "source_auth_failed", Message: "refused"}, StepFailed, "source_auth_failed"},
		"a rate-limited base image": {fmt.Errorf("%w: resolve reviewed base image node:22-alpine: toomanyrequests: You have reached your pull rate limit", ErrBuilderUnavailable),
			StepUnavailable, "registry_rate_limited"},
	} {
		result := normalizedStepFailure(test.err)
		if result.State != test.state || result.ErrorCode != test.code {
			t.Fatalf("%s: %+v", name, result)
		}
		if strings.Contains(result.ErrorMessage, "npm ci") {
			t.Fatalf("%s: an unredacted command reached the message: %q", name, result.ErrorMessage)
		}
	}
}

func TestBuilderUnavailableFailureKeepsItsCause(t *testing.T) {
	t.Parallel()
	for text, want := range map[string]string{
		"toomanyrequests: You have reached your pull rate limit":                                        "registry_rate_limited",
		"Get \"https://registry-1.docker.io/v2/\": dial tcp: lookup registry-1.docker.io: no such host": "registry_unreachable",
		"manifest unknown: manifest unknown":                                                            "base_image_missing",
		"unauthorized: incorrect username or password":                                                  "registry_auth_failed",
	} {
		err := fmt.Errorf("%w: resolve reviewed base image gradle:8-jdk25: %s", ErrBuilderUnavailable, text)
		result := builderUnavailableFailure(err, nil)
		if result.ErrorCode != want || result.State != StepUnavailable || !strings.Contains(result.ErrorMessage, "gradle:8-jdk25") {
			t.Fatalf("%q = %+v", text, result)
		}
	}
	if result := builderUnavailableFailure(ErrBuilderUnavailable, nil); result.ErrorCode != "builder_missing" {
		t.Fatalf("no backend = %+v", result)
	}
	detail := builderUnavailableDetail(fmt.Errorf("%w: resolve reviewed base image x: %s token-value", ErrBuilderUnavailable, strings.Repeat("y", 500)),
		buildRedactor(map[string]string{"TOKEN": "token-value"}))
	if len(detail) > builderDetailLength || strings.Contains(detail, "token-value") {
		t.Fatalf("detail = %q", detail)
	}
}

func TestCandidateStartFailureNamesDockerRefusals(t *testing.T) {
	t.Parallel()
	for text, want := range map[string]string{
		"Error response from daemon: driver failed programming external connectivity on endpoint x: Bind for 0.0.0.0:8080 failed: port is already allocated": "runtime_port_in_use",
		"Error response from daemon: No such image: sha256:abc":                                                          "image_missing",
		"Error response from daemon: invalid mount config for type \"bind\": bind source path does not exist: /srv/data": "mount_invalid",
		"Error response from daemon: something else entirely":                                                            "candidate_start_failed",
	} {
		code, message := candidateStartFailure(errors.New(text))
		if code != want {
			t.Fatalf("%q = %s", text, code)
		}
		if strings.Contains(message, "daemon") || strings.Contains(message, "/srv/data") {
			t.Fatalf("message carries the daemon's text: %q", message)
		}
	}
	if _, message := candidateStartFailure(errors.New("Bind for 0.0.0.0:8080 failed: port is already allocated")); !strings.Contains(message, "port 8080") {
		t.Fatalf("port message = %q", message)
	}
}

func TestReleaseTaskFailureNamesTimeoutsAndCauses(t *testing.T) {
	t.Parallel()
	task := ReleaseTaskConfig{Name: "migrate", Command: "npx prisma migrate deploy"}
	code, message, cause := releaseTaskFailure(task, ReleaseTaskEvidence{ExitCode: 127}, errors.New("release task exited with code 127"),
		[]collectedLine{{text: "/bin/sh: npx: not found", seq: 9}}, nil)
	if code != "release_task_command_not_found" || cause == nil || !strings.Contains(message, "dashboard's own shell") {
		t.Fatalf("code = %s, message = %q", code, message)
	}
	code, message, _ = releaseTaskFailure(task, ReleaseTaskEvidence{ExitCode: -1}, errors.New("release task migrate timed out after 60 seconds"), nil, nil)
	if code != "release_task_timeout" || !strings.Contains(message, "time limit") {
		t.Fatalf("timeout = %s %q", code, message)
	}
	code, _, cause = releaseTaskFailure(task, ReleaseTaskEvidence{ExitCode: 1}, errors.New("release task exited with code 1"), []collectedLine{{text: "done"}}, nil)
	if code != "release_task_failed" || cause != nil {
		t.Fatalf("plain failure = %s %+v", code, cause)
	}
	if evidence := releaseTaskStepEvidence(nil, nil); evidence["cause"] != nil {
		t.Fatalf("evidence without a cause = %+v", evidence)
	}
}
