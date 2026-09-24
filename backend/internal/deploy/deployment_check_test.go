package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// gitTree commits files into a fresh local repository and returns its path
// and the commit.
func gitTree(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	repository := t.TempDir()
	runPlanningGitFixture(t, repository, "init", "-q")
	runPlanningGitFixture(t, repository, "config", "user.email", "fixture@example.test")
	runPlanningGitFixture(t, repository, "config", "user.name", "Fixture")
	for path, content := range files {
		writeBuildFixture(t, repository, path, content)
	}
	runPlanningGitFixture(t, repository, "add", "-A")
	runPlanningGitFixture(t, repository, "commit", "-q", "-m", "fixture")
	return repository, strings.TrimSpace(runPlanningGitOutput(t, repository, "rev-parse", "HEAD"))
}

// addSourcePlan stores a plan revision for a local Git checkout with the
// given build and the evidence detection saved with it.
func (f *releaseStoreFixture) addSourcePlan(
	t *testing.T,
	revision int,
	repository, sourceRevision string,
	build BuildPlanConfig,
	evidence StoredBuildEvidence,
) {
	t.Helper()
	source := DraftSourceConfig{Kind: SourceLocal, Mode: SourceModeLocalCheckout, LocalPath: repository}
	identity := SourceIdentity{Kind: SourceLocal, LocalPath: repository, Revision: sourceRevision}
	runtime := RuntimePlanConfig{InternalPort: 3000, Strategy: StrategyStopFirst, Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{}}
	build = canonicalConfiguration(PlanConfiguration{Build: build}).Build
	sourceJSON, _ := json.Marshal(source)
	identityJSON, _ := json.Marshal(identity)
	buildJSON, _ := json.Marshal(build)
	runtimeJSON, _ := json.Marshal(runtime)
	evidenceJSON, _ := json.Marshal(evidence)
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO deploy_sources(environment_id, revision, kind, config_json, identity_json, digest, created_at) VALUES(?, ?, 'local', ?, ?, ?, ?)",
			[]any{f.envID, revision, string(sourceJSON), string(identityJSON), digestBytes(sourceJSON, identityJSON), f.now.Unix()}},
		{"INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?)",
			[]any{f.envID, revision, build.Method, string(buildJSON), string(evidenceJSON), string(buildJSON), buildPlanDigest(build), f.now.Unix()}},
		{"INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at) VALUES(?, ?, ?, ?, ?, ?)",
			[]any{f.envID, revision, string(runtimeJSON), string(runtimeJSON), digestBytes(runtimeJSON), f.now.Unix()}},
		{"UPDATE deploy_environments SET desired_revision = ? WHERE id = ?", []any{revision, f.envID}},
	} {
		if _, err := f.base.DB.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}

func readyObserver() *preflightObserverFake {
	return &preflightObserverFake{observation: HostObservation{Facilities: map[string]FacilityObservation{
		"git": {Available: true}, "docker": {Available: true}, "buildx": {Available: true},
	}}}
}

// analyze_plan judges the plan against the commit acquire_source
// materialized, not against a candidate made up from the plan: the Go module
// grew a second command after the plan was saved, so the run stops before a
// build slot with the choice named, and the step keeps the candidate it
// used and its findings as evidence.
func TestAnalyzePlanReadsTheCommitBeingBuilt(t *testing.T) {
	repository, revision := gitTree(t, map[string]string{
		"go.mod":              "module example.test/app\n\ngo 1.26\n",
		"cmd/gateway/main.go": "package main\n\nfunc main() {}\n",
		"cmd/worker/main.go":  "package main\n\nfunc main() {}\n",
	})
	stored := newDetectedCandidate("", BuildRecipe, DetectedCandidate{
		Name: "Go service in .", Recipe: "go", Framework: "go", Confidence: ConfidenceHigh, Profile: ProfileService,
		GoMainPackages: []string{"cmd/gateway"}, GoPackage: "cmd/gateway",
	})
	fixture := newReleaseStoreFixture(t)
	fixture.setProfile(t, ProfileService)
	fixture.addSourcePlan(t, 1, repository, revision, BuildPlanConfig{Method: BuildRecipe, Recipe: "go"},
		StoredBuildEvidence{Candidates: []DetectedCandidate{stored}})
	run, _ := fixture.claimedRun(t, 1)
	plan, err := fixture.runs.ExecutionPlan(context.Background(), *run)
	if err != nil {
		t.Fatal(err)
	}
	executor := &NormalizedStepExecutor{
		store: fixture.runs, preflight: readyObserver(), workspaceRoot: t.TempDir(),
		sources: NewHostSourceAnalyzer([]string{repository}, nil, t.TempDir(), nil, nil),
	}
	result := executor.analyzePlan(context.Background(), StepExecution{Run: *run}, plan)
	if result.State != StepFailed || result.ErrorCode != "go_main_ambiguous" ||
		result.ErrorMessage != "Choose the Go main package to build: ./cmd/gateway, ./cmd/worker. Choose the main package in the build settings." {
		t.Fatalf("analyze_plan = %s %q", result.ErrorCode, result.ErrorMessage)
	}
	var evidence struct {
		Candidate       DetectedCandidate  `json:"candidate"`
		CandidateSource string             `json:"candidateSource"`
		Findings        []PreflightFinding `json:"findings"`
	}
	if err := json.Unmarshal(result.Evidence, &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.CandidateSource != "detected" || len(evidence.Candidate.GoMainPackages) != 2 ||
		findingByCode(evidence.Findings, "go_main_ambiguous") == nil {
		t.Fatalf("evidence = %#v", evidence)
	}
	for _, item := range evidence.Findings {
		if item.Code == "detection_selected" || item.Code == "build_method_changed" {
			t.Fatalf("a wizard-only finding reached a run: %#v", item)
		}
	}

	fixture = newReleaseStoreFixture(t)
	fixture.setProfile(t, ProfileService)
	fixture.addSourcePlan(t, 1, repository, revision, BuildPlanConfig{Method: BuildRecipe, Recipe: "go", GoPackage: "cmd/worker"},
		StoredBuildEvidence{Candidates: []DetectedCandidate{stored}})
	executor.store = fixture.runs
	run, _ = fixture.claimedRun(t, 1)
	if plan, err = fixture.runs.ExecutionPlan(context.Background(), *run); err != nil {
		t.Fatal(err)
	}
	result = executor.analyzePlan(context.Background(), StepExecution{Run: *run}, plan)
	if result.State == StepFailed {
		t.Fatalf("a decided plan was stopped: %s %s", result.ErrorCode, result.ErrorMessage)
	}
}

func (f *releaseStoreFixture) setProfile(t *testing.T, profile WorkloadProfile) {
	t.Helper()
	if _, err := f.base.DB.Exec("UPDATE deploy_projects SET profile = ? WHERE id = ?", profile, f.projectID); err != nil {
		t.Fatal(err)
	}
}

// The incident's shape: the plan chose npm, the commit being built has only
// bun.lock. The recipe would refuse at prepare_context; analyze_plan says so
// first, in words, with the finding's code as the run's terminal code.
func TestAnalyzePlanStopsARecipeRefusalWithAReadableReason(t *testing.T) {
	repository, revision := gitTree(t, map[string]string{
		"package.json": `{"name":"web","scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16.0.0"}}`,
		"bun.lock":     "{}",
	})
	fixture := newReleaseStoreFixture(t)
	fixture.addSourcePlan(t, 1, repository, revision, BuildPlanConfig{
		Method: BuildRecipe, Recipe: "node", PackageManager: "npm", BuildCommand: "npm run build", StartCommand: "npm run start",
	}, StoredBuildEvidence{})
	run, _ := fixture.claimedRun(t, 1)
	plan, err := fixture.runs.ExecutionPlan(context.Background(), *run)
	if err != nil {
		t.Fatal(err)
	}
	executor := &NormalizedStepExecutor{
		store: fixture.runs, preflight: readyObserver(), workspaceRoot: t.TempDir(),
		sources: NewHostSourceAnalyzer([]string{repository}, nil, t.TempDir(), nil, nil),
	}
	result := executor.analyzePlan(context.Background(), StepExecution{Run: *run}, plan)
	if result.State != StepFailed || result.ErrorCode != "package_manager_lockfile_missing" ||
		!strings.HasPrefix(result.ErrorMessage, "Selected package manager has no lockfile: npm; lockfiles for bun.") {
		t.Fatalf("analyze_plan = %s %q", result.ErrorCode, result.ErrorMessage)
	}
}

func TestDeploymentEvaluationPrefersTheCommitThenTheStoredEvidence(t *testing.T) {
	build := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", PackageManager: "", BuildCommand: "bun run build", OutputDirectory: "build"}
	stored := newDetectedCandidate("", BuildRecipe, DetectedCandidate{
		Name: "web", Recipe: "node", Framework: "create-react-app", Confidence: ConfidenceHigh,
		PackageManager: "bun", PackageManagers: []string{"bun"}, OutputDirectory: "build",
		Variables: []DetectedVariable{{Name: "API_URL", Sources: []string{".env.example"}}},
	})
	fresh := stored
	fresh.Framework, fresh.OutputDirectory = "vite", "dist"
	fresh.PackageManagers = []string{"bun", "npm"}
	fresh.Variables = append(fresh.Variables, DetectedVariable{Name: "SENTRY_DSN", Sources: []string{"src/main.ts"}})
	plan := deploymentPlan{
		Source:        DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/o/r.git"},
		Identity:      SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("c", 40)},
		Profile:       ProfileStatic,
		Configuration: canonicalConfiguration(PlanConfiguration{Build: build, Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst}}),
		Evidence:      StoredBuildEvidence{Candidates: []DetectedCandidate{stored}},
	}
	reading := sourceReading{detection: &DetectionResult{Candidates: []DetectedCandidate{fresh}, SelectedID: fresh.ID}}
	evaluation, err := evaluateDeployment(context.Background(), plan, reading, readyObserver())
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.CandidateSource != "detected" || evaluation.Candidate.Framework != "vite" {
		t.Fatalf("evaluation used %s %#v", evaluation.CandidateSource, evaluation.Candidate)
	}
	for code, measured := range map[string]string{
		"plan_drift_package_manager":  "lockfiles bun → bun, npm",
		"plan_drift_framework":        "create-react-app → vite",
		"plan_drift_output_directory": "build → dist",
		"plan_drift_variables":        "SENTRY_DSN",
	} {
		if item := findingByCode(evaluation.Findings, code); item == nil || item.Severity != PreflightWarning || item.Measured != measured {
			t.Errorf("%s = %#v", code, item)
		}
	}
	// Following the new output settles that drift; the rest still stands.
	plan.Configuration.Build.OutputDirectory = "dist"
	evaluation, _ = evaluateDeployment(context.Background(), plan, reading, readyObserver())
	if findingByCode(evaluation.Findings, "plan_drift_output_directory") != nil {
		t.Fatal("an applied output directory still drifted")
	}

	unread := sourceReading{unavailable: "the commit could not be fetched for inspection"}
	evaluation, _ = evaluateDeployment(context.Background(), plan, unread, readyObserver())
	if evaluation.CandidateSource != "recorded" || findingByCode(evaluation.Findings, "source_inspection_unavailable") == nil {
		t.Fatalf("unreadable commit = %s %#v", evaluation.CandidateSource, evaluation.Findings)
	}

	image := plan
	image.Source = DraftSourceConfig{Kind: SourceImage, Mode: SourceModeImageReference, Image: "nginx:1"}
	image.Configuration = canonicalConfiguration(PlanConfiguration{Build: BuildPlanConfig{Method: BuildImage}, Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst}})
	image.Evidence = StoredBuildEvidence{}
	evaluation, _ = evaluateDeployment(context.Background(), image, sourceReading{}, readyObserver())
	if evaluation.CandidateSource != "plan" || evaluation.Candidate.BuildMethod != BuildImage {
		t.Fatalf("image evaluation = %#v", evaluation)
	}
}

// The advisory check runs the evaluation analyze_plan runs, for the desired
// plan and the commit a deployment would build, and answers a repeat from
// memory while the plan revision and commit are unchanged.
func TestDeploymentCheckEvaluatesTheDesiredPlanAndCachesIt(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "package.json", `{"name":"web","scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16.0.0"}}`)
	writeBuildFixture(t, root, "bun.lock", "{}")
	fixture := newReleaseStoreFixture(t)
	revision := strings.Repeat("d", 40)
	fixture.addSourcePlan(t, 1, "/srv/release-fixture", revision, BuildPlanConfig{
		Method: BuildRecipe, Recipe: "node", PackageManager: "npm", BuildCommand: "npm run build", StartCommand: "npm run start",
	}, StoredBuildEvidence{})
	inspector := &treeInspectorFake{root: root}
	observer := readyObserver()
	checker := &DeploymentChecker{
		runs: fixture.runs, planning: fixture.variables, sources: inspector, observer: observer,
		now: func() time.Time { return fixture.now }, cache: map[deploymentCheckKey]deploymentCheckEntry{},
	}
	result, err := checker.Check(context.Background(), fixture.projectID, fixture.envID, DeploymentCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if result.PlanRevision != 1 || result.SourceRevision != revision || inspector.calls != 1 {
		t.Fatalf("check = %#v, inspections %d", result, inspector.calls)
	}
	if item := findingByCode(result.Findings, "package_manager_lockfile_missing"); item == nil || item.Severity != PreflightBlocked {
		t.Fatalf("the check missed what analyze_plan would stop: %#v", result.Findings)
	}
	if _, err := checker.Check(context.Background(), fixture.projectID, fixture.envID, DeploymentCheckRequest{}); err != nil || inspector.calls != 1 {
		t.Fatalf("a repeat was not answered from memory: %v, inspections %d", err, inspector.calls)
	}
	fixture.now = fixture.now.Add(deploymentCheckTTL + time.Second)
	if _, err := checker.Check(context.Background(), fixture.projectID, fixture.envID, DeploymentCheckRequest{}); err != nil || inspector.calls != 2 {
		t.Fatalf("an expired answer was reused: %v, inspections %d", err, inspector.calls)
	}
	if _, err := checker.Check(context.Background(), fixture.projectID, fixture.envID, DeploymentCheckRequest{Ref: "main"}); err == nil {
		t.Fatal("a ref was accepted for a local checkout")
	}

	proposal, err := checker.Detect(context.Background(), fixture.projectID, fixture.envID)
	if err != nil {
		t.Fatal(err)
	}
	var manager *DetectionChange
	for index := range proposal.Changes {
		if proposal.Changes[index].Field == "build.packageManager" {
			manager = &proposal.Changes[index]
		}
	}
	if proposal.Revision != 1 || manager == nil || manager.Saved != "npm" || manager.Detected != "bun" || !manager.Changed {
		t.Fatalf("detect = %#v", proposal)
	}
}

// A source change saves what detection read at the new source as the plan's
// evidence and compares it with the plan, instead of carrying the old
// source's evidence forward.
func TestSourceChangeSavesFreshDetectionAndProposesWhatChanged(t *testing.T) {
	fixture := newReleaseStoreFixture(t)
	old := newDetectedCandidate("", BuildRecipe, DetectedCandidate{
		Name: "web", Recipe: "node", Framework: "create-react-app", Confidence: ConfidenceHigh, Profile: ProfileStatic,
		PackageManager: "npm", PackageManagers: []string{"npm"}, BuildCommand: "npm run build", OutputDirectory: "build", Port: 80,
	})
	fixture.addSourcePlan(t, 1, "/srv/release-fixture", strings.Repeat("e", 40), BuildPlanConfig{
		Method: BuildRecipe, Recipe: "node", PackageManager: "npm", BuildCommand: "npm run build", OutputDirectory: "build",
		Framework: "create-react-app",
	}, StoredBuildEvidence{Candidates: []DetectedCandidate{old}})
	fresh := old
	fresh.Framework, fresh.PackageManager, fresh.PackageManagers = "vite", "bun", []string{"bun"}
	fresh.BuildCommand, fresh.OutputDirectory = "bun run build", "dist"
	fresh.Variables = []DetectedVariable{{Name: "VITE_API_URL", Sources: []string{".env.example"}}}
	source := DraftSourceConfig{Kind: SourceLocal, Mode: SourceModeLocalCheckout, LocalPath: "/srv/release-fixture", Subdirectory: "web"}
	detection := DetectionResult{
		Source:     SourceIdentity{Kind: SourceLocal, LocalPath: "/srv/release-fixture", Revision: strings.Repeat("f", 40)},
		Candidates: []DetectedCandidate{fresh}, SelectedID: fresh.ID,
	}
	configuration, proposal, err := fixture.variables.SaveEnvironmentSourceDetection(
		context.Background(), fixture.projectID, fixture.envID, 1, source, detection)
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Revision != 2 || configuration.Build.Framework != "vite" || configuration.Build.OutputDirectory != "build" {
		t.Fatalf("saved build = %#v", configuration.Build)
	}
	evidence, err := fixture.variables.storedBuildEvidence(context.Background(), fixture.envID, 2)
	if err != nil || len(evidence.Candidates) != 1 || evidence.Candidates[0].Framework != "vite" {
		t.Fatalf("stored evidence = %#v, %v", evidence, err)
	}
	fields := map[string]DetectionChange{}
	for _, change := range proposal.Changes {
		fields[change.Field] = change
	}
	if change := fields["build.packageManager"]; change.Saved != "npm" || change.Detected != "bun" || change.Previous != "npm" || !change.Changed {
		t.Fatalf("package manager change = %#v", change)
	}
	if change := fields["build.outputDirectory"]; change.Saved != "build" || change.Detected != "dist" || !change.Changed {
		t.Fatalf("output change = %#v", change)
	}
	// The port was saved differently from what detection proposed both times:
	// an edit on purpose, offered but not flagged as detection's change.
	if change, ok := fields["runtime.internalPort"]; !ok || change.Changed || change.Saved != "3000" || change.Detected != "80" {
		t.Fatalf("port = %#v", change)
	}
	if proposal.Revision != 2 || len(proposal.Variables) != 1 || len(proposal.NewVariables) != 1 || proposal.NewVariables[0] != "VITE_API_URL" {
		t.Fatalf("proposal = %#v", proposal)
	}
}

// A checker built without its source or host modules reads them as absent
// — the check and Detect again say so — rather than holding typed nil
// pointers that panic on first use.
func TestDeploymentCheckerWithoutItsModulesSaysSo(t *testing.T) {
	fixture := newReleaseStoreFixture(t)
	fixture.addSourcePlan(t, 1, "/srv/release-fixture", strings.Repeat("d", 40), BuildPlanConfig{
		Method: BuildRecipe, Recipe: "node", PackageManager: "npm", BuildCommand: "npm run build", StartCommand: "npm run start",
	}, StoredBuildEvidence{})
	checker := NewDeploymentChecker(fixture.runs, fixture.variables, nil, nil)
	if _, err := checker.Check(context.Background(), fixture.projectID, fixture.envID, DeploymentCheckRequest{}); !errors.Is(err, ErrBuilderUnavailable) {
		t.Fatalf("check without a host observer = %v", err)
	}
	if _, err := checker.Detect(context.Background(), fixture.projectID, fixture.envID); !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("detect without a source analyzer = %v", err)
	}
}

// A proposal compares the plan with what detection finds at the plan's own
// root building the plan's way. Another directory's candidate is named for
// information with nothing offered, and a command detection could not tell
// is never proposed as clearing the plan's.
func TestDetectionProposalComparesOnlyThePlansOwnRoot(t *testing.T) {
	build := BuildPlanConfig{
		Method: BuildRecipe, Recipe: "node", RootDirectory: "apps/web",
		BuildCommand: "npm run build", StartCommand: "npm start",
	}
	runtime := RuntimePlanConfig{InternalPort: 3000}
	web := newDetectedCandidate("apps/web", BuildRecipe, DetectedCandidate{
		Name: "web", Recipe: "node", Confidence: ConfidenceHigh, Port: 3000,
		BuildCommand: "npm run build", NeedsDecision: []string{"choose a start command"},
		Variables: []DetectedVariable{{Name: "API_URL", Sources: []string{"apps/web/src/api.ts"}}},
	})
	docs := newDetectedCandidate("apps/docs", BuildRecipe, DetectedCandidate{
		Name: "docs", Recipe: "node", Confidence: ConfidenceHigh, Port: 4321,
		BuildCommand: "pnpm run build", StartCommand: "pnpm start", PackageManager: "pnpm",
		Variables: []DetectedVariable{{Name: "DOCS_TOKEN", Sources: []string{"apps/docs/src/a.ts"}}},
	})

	proposal := detectionProposal(nil, &DetectionResult{Candidates: []DetectedCandidate{web, docs}, SelectedID: docs.ID},
		build, runtime, map[string]bool{})
	if proposal.Elsewhere || proposal.Candidate == nil || proposal.Candidate.ID != web.ID {
		t.Fatalf("the plan's own root was not compared: %#v", proposal)
	}
	if len(proposal.Changes) != 0 {
		t.Fatalf("an unknown start command was proposed as clearing the plan's: %#v", proposal.Changes)
	}
	if len(proposal.Variables) != 1 || proposal.Variables[0].Name != "API_URL" {
		t.Fatalf("variables = %#v", proposal.Variables)
	}

	proposal = detectionProposal(nil, &DetectionResult{Candidates: []DetectedCandidate{docs}, SelectedID: docs.ID},
		build, runtime, map[string]bool{})
	if !proposal.Elsewhere || proposal.Candidate == nil || proposal.Candidate.ID != docs.ID ||
		len(proposal.Changes) != 0 || len(proposal.Variables) != 0 || len(proposal.Databases) != 0 {
		t.Fatalf("another directory's settings were offered for the plan: %#v", proposal)
	}

	// The same directory built another way still reads the same variables.
	dockerfile := newDetectedCandidate("apps/web", BuildDockerfile, DetectedCandidate{
		Name: "web image", Confidence: ConfidenceHigh, Port: 8080,
		Variables: []DetectedVariable{{Name: "API_URL", Sources: []string{"apps/web/src/api.ts"}}},
	})
	proposal = detectionProposal(nil, &DetectionResult{Candidates: []DetectedCandidate{dockerfile}, SelectedID: dockerfile.ID},
		build, runtime, map[string]bool{})
	if !proposal.Elsewhere || len(proposal.Changes) != 0 || len(proposal.Variables) != 1 {
		t.Fatalf("a Dockerfile at the plan's root = %#v", proposal)
	}
}
