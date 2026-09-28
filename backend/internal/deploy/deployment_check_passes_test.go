package deploy

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The deploy-time evaluation (deployment_check.go) runs the same detector
// as the wizard, so every finding a detection pass supports is raised
// against the commit a run builds. These hold that for the passes that were
// written beside it: the repository Dockerfile's validator, where the
// server listens, the JavaScript install and the environment check.

// analyzeCommit runs analyze_plan for a plan saved without detection
// evidence over a commit of files. A service profile needs no readiness
// check, whose absence would otherwise stop the run first.
func analyzeCommit(t *testing.T, files map[string]string, build BuildPlanConfig, profile WorkloadProfile) StepResult {
	t.Helper()
	repository, revision := gitTree(t, files)
	fixture := newReleaseStoreFixture(t)
	fixture.setProfile(t, profile)
	fixture.addSourcePlan(t, 1, repository, revision, build, StoredBuildEvidence{})
	run, _ := fixture.claimedRun(t, 1)
	plan, err := fixture.runs.ExecutionPlan(context.Background(), *run)
	if err != nil {
		t.Fatal(err)
	}
	executor := &NormalizedStepExecutor{
		store: fixture.runs, preflight: readyObserver(), workspaceRoot: t.TempDir(),
		sources: NewHostSourceAnalyzer([]string{repository}, nil, t.TempDir(), nil, nil),
	}
	return executor.analyzePlan(context.Background(), StepExecution{Run: *run}, plan)
}

func analyzeEvidence(t *testing.T, result StepResult) (DetectedCandidate, string, []PreflightFinding) {
	t.Helper()
	var evidence struct {
		Candidate       DetectedCandidate  `json:"candidate"`
		CandidateSource string             `json:"candidateSource"`
		Findings        []PreflightFinding `json:"findings"`
	}
	if err := json.Unmarshal(result.Evidence, &evidence); err != nil {
		t.Fatal(err)
	}
	return evidence.Candidate, evidence.CandidateSource, evidence.Findings
}

// A secret written into the repository's Dockerfile after the plan was
// saved stops the run on the validator's own finding, named once: the dry
// run refuses the same line, and that refusal is not repeated.
func TestAnalyzePlanRefusesTheCommitsDockerfileOnItsOwnFinding(t *testing.T) {
	result := analyzeCommit(t, map[string]string{
		"Dockerfile": "FROM node:22\nENV API_TOKEN=ghp_abcdef0123456789\nCMD [\"node\", \"server.js\"]\n",
		"server.js":  "require('http').createServer(() => {}).listen(process.env.PORT)\n",
	}, BuildPlanConfig{Method: BuildDockerfile, Dockerfile: "Dockerfile"}, ProfileService)
	if result.State != StepFailed || result.ErrorCode != "dockerfile_refused" || !strings.Contains(result.ErrorMessage, "API_TOKEN") {
		t.Fatalf("analyze_plan = %s %q", result.ErrorCode, result.ErrorMessage)
	}
	candidate, source, findings := analyzeEvidence(t, result)
	if source != "detected" || candidate.BuildMethod != BuildDockerfile {
		t.Fatalf("candidate %s from %s", candidate.BuildMethod, source)
	}
	if item := findingByCode(findings, "recipe_unsupported"); item != nil {
		t.Fatalf("the refusal was reported twice: %#v", item)
	}
}

// A server the commit binds to loopback is refused before the build: the
// listener is read from the commit being built, not the one the plan was
// saved from.
func TestAnalyzePlanRefusesALoopbackListenerInTheCommit(t *testing.T) {
	result := analyzeCommit(t, map[string]string{
		"package.json":      `{"name":"api","scripts":{"start":"node server.js"},"dependencies":{"express":"4.21.0"}}`,
		"package-lock.json": `{"name":"api","lockfileVersion":3,"packages":{"":{"name":"api","dependencies":{"express":"4.21.0"}},"node_modules/express":{"version":"4.21.0"}}}`,
		"server.js":         "const app = require('express')()\napp.listen(3000, '127.0.0.1')\n",
	}, BuildPlanConfig{Method: BuildRecipe, Recipe: "node", StartCommand: "npm run start"}, ProfileService)
	if result.State != StepFailed || result.ErrorCode != "listen_loopback" || !strings.Contains(result.ErrorMessage, "127.0.0.1") {
		t.Fatalf("analyze_plan = %s %q", result.ErrorCode, result.ErrorMessage)
	}
}

// The incident at deploy time: the plan forces npm, and the commit's
// package-lock.json is fifteen dependencies behind while bun.lock matches.
// The run is not stopped — the recipe installs unfrozen — but the finding
// says so and offers the lockfile that matches, and prisma.config's
// DATABASE_URL, which the recipe gives a placeholder while generating the
// client, is not demanded in the build.
func TestDeploymentEvaluationNamesTheIncidentsStaleLockfile(t *testing.T) {
	files := incidentTree(t)
	files["prisma.config.ts"] = "import { defineConfig, env } from 'prisma/config'\nexport default defineConfig({ schema: 'prisma/schema.prisma', datasource: { url: env('DATABASE_URL') } })\n"
	root := writeNodeTree(t, files)
	build := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", PackageManager: "npm", BuildCommand: "npm run build", StartCommand: "npm run start"}
	plan := deploymentPlan{
		Source:   DraftSourceConfig{Kind: SourceLocal, Mode: SourceModeLocalCheckout, LocalPath: root},
		Identity: SourceIdentity{Kind: SourceLocal, LocalPath: root, Revision: strings.Repeat("a", 40)},
		Profile:  ProfileWeb, Configuration: nodeTestConfiguration(build),
	}
	plan.Configuration.Runtime.InternalPort = 3000
	plan.Configuration.Variables = []PlannedVariable{{Name: "DATABASE_URL", Sensitivity: "secret", Scopes: []string{"runtime"}}}
	reading := readDeploymentSource(context.Background(), root, plan)
	if reading.refusal != nil {
		t.Fatalf("the dry run refused: %v", reading.refusal)
	}
	evaluation, err := evaluateDeployment(context.Background(), plan, reading, readyObserver())
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.CandidateSource != "detected" || evaluation.Candidate.PackageManager != "bun" {
		t.Fatalf("candidate %q from %s", evaluation.Candidate.PackageManager, evaluation.CandidateSource)
	}
	stale := findingByCode(evaluation.Findings, "lockfile_out_of_sync")
	if stale == nil || stale.Severity != PreflightWarning || !strings.Contains(stale.Measured, "package-lock.json") {
		t.Fatalf("lockfile_out_of_sync = %#v", stale)
	}
	for _, item := range evaluation.Findings {
		if item.Severity == PreflightBlocked || strings.HasPrefix(item.Code, "build_variable_missing_") {
			t.Fatalf("the deployment was refused: %#v", item)
		}
	}
}

// A stage the plan names that the commit's Dockerfile no longer has is the
// validator's finding; the dry run, which refuses the same stage, adds
// nothing. A dry run prepares a container candidate's Dockerfile from its
// build context and writes nothing into the checkout.
func TestDryRunChecksTheCommitsDockerfileAndWritesNothing(t *testing.T) {
	root := writeNodeTree(t, map[string]string{
		"docker/Dockerfile": "FROM node:22 AS build\nWORKDIR /app\nCOPY package.json .\nFROM node:22-slim AS runner\nCOPY --from=build /app /app\nCMD [\"node\", \"/app/server.js\"]\n",
		"package.json":      `{"name":"api"}`,
	})
	build := BuildPlanConfig{Method: BuildDockerfile, Dockerfile: "docker/Dockerfile", Target: "production"}
	if err := dryRunBuild(context.Background(), root, build, nil); err == nil || !strings.Contains(err.Error(), "production") {
		t.Fatalf("dry run = %v", err)
	}
	plan := deploymentPlan{
		Source:   DraftSourceConfig{Kind: SourceLocal, Mode: SourceModeLocalCheckout, LocalPath: root},
		Identity: SourceIdentity{Kind: SourceLocal, LocalPath: root, Revision: strings.Repeat("b", 40)},
		Profile:  ProfileWeb, Configuration: nodeTestConfiguration(build),
	}
	plan.Configuration.Runtime.InternalPort = 3000
	evaluation, err := evaluateDeployment(context.Background(), plan, readDeploymentSource(context.Background(), root, plan), readyObserver())
	if err != nil {
		t.Fatal(err)
	}
	if item := findingByCode(evaluation.Findings, "dockerfile_target_missing"); item == nil || item.Severity != PreflightBlocked {
		t.Fatalf("dockerfile_target_missing = %#v in %#v", item, evaluation.Findings)
	}
	if item := findingByCode(evaluation.Findings, "recipe_unsupported"); item != nil {
		t.Fatalf("the stage was refused twice: %#v", item)
	}
	build.Target = "runner"
	if err := dryRunBuild(context.Background(), root, build, nil); err != nil {
		t.Fatalf("dry run of the runner stage = %v", err)
	}

	// A workspace member prepares from its workspace root, as the build does,
	// and neither directory gains generated files.
	workspace := writeNodeTree(t, map[string]string{
		"package.json":          `{"name":"root","private":true}`,
		"pnpm-workspace.yaml":   "packages:\n  - apps/*\n",
		"apps/web/package.json": `{"name":"web","scripts":{"start":"node server.js"},"dependencies":{"express":"4.21.0"}}`,
		"pnpm-lock.yaml":        "lockfileVersion: '9.0'\n\nimporters:\n\n  .: {}\n\n  apps/web:\n    dependencies:\n      express:\n        specifier: 4.21.0\n        version: 4.21.0\n",
	})
	member := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", RootDirectory: "apps/web", StartCommand: "pnpm run start"}
	if err := dryRunBuild(context.Background(), workspace, member, nil); err != nil {
		t.Fatalf("dry run of the member = %v", err)
	}
	for _, directory := range []string{workspace, filepath.Join(workspace, "apps", "web"), root} {
		if _, err := os.Stat(filepath.Join(directory, ".just-dashboard")); !os.IsNotExist(err) {
			t.Fatalf("the dry run wrote into %s: %v", directory, err)
		}
	}
}

// What the repository's shape says about choosing a candidate — a frontend
// whose API is another project, an example ranked below the application —
// is the wizard's question; a saved plan made that choice, so a run and the
// check before Deploy do not ask it again.
func TestDeploymentEvaluationDoesNotAskTheShapesChoiceAgain(t *testing.T) {
	t.Parallel()
	build := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", RootDirectory: "web", BuildCommand: "npm run build", OutputDirectory: "dist"}
	stored := newDetectedCandidate("web", BuildRecipe, DetectedCandidate{
		Name: "web", Recipe: "node", Profile: ProfileStatic, Confidence: ConfidenceHigh, OutputDirectory: "dist",
		Companions: []string{"api"}, Demotion: "the frontend of the API in api",
	})
	plan := deploymentPlan{
		Source:   DraftSourceConfig{Kind: SourceImage, Mode: SourceModeImageReference, Image: "example.test/web:1"},
		Identity: SourceIdentity{Kind: SourceImage},
		Profile:  ProfileStatic, Configuration: nodeTestConfiguration(build),
		Evidence: StoredBuildEvidence{Candidates: []DetectedCandidate{stored}},
	}
	draft := &Draft{Data: DraftData{
		Intent: &DraftIntentConfig{Name: "web", Profile: ProfileStatic}, Source: &plan.Source,
		Detection: &DetectionResult{Candidates: []DetectedCandidate{stored}, SelectedID: stored.ID}, Configuration: &plan.Configuration,
	}}
	if item := findingByCode(preflightFindings(draft, plan.Configuration, HostObservation{}, true), "companion_service_not_deployed"); item == nil {
		t.Fatal("the wizard no longer names the companion")
	}
	evaluation, err := evaluateDeployment(context.Background(), plan, sourceReading{}, readyObserver())
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"companion_service_not_deployed", "selected_candidate_demoted"} {
		if item := findingByCode(evaluation.Findings, code); item != nil {
			t.Fatalf("a run asks the wizard's question: %#v", item)
		}
	}
}
