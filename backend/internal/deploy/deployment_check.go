package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Preflight before every deployment.
//
// A plan was reviewed against the commit detection read when it was saved.
// The commit a deployment builds can be a later one — a push, a branch
// change, a specific version — and the plan can have been edited since. So
// the checks that come from detection are run again, over a fresh, data-only
// detection of the commit about to be built, and the recipe itself is asked
// whether it would prepare the plan from that tree. analyze_plan runs this
// immediately before any build side effect, on every trigger; the advisory
// check endpoint runs the same evaluation when a project is opened and after
// every settings save, so what would stop or trouble a deployment is shown
// before Deploy is pressed.

// deploymentPlan is what a deploy-time evaluation reads: the plan about to
// run, the evidence stored when it was saved, and what already owns the
// environment's route and ports.
type deploymentPlan struct {
	Source         DraftSourceConfig
	Identity       SourceIdentity
	Profile        WorkloadProfile
	Configuration  PlanConfiguration
	Evidence       StoredBuildEvidence
	BuildVariables []string

	ProxySite   string
	RuntimeID   string
	RuntimeKind string
}

// sourceReading is what reading the commit's tree produced.
type sourceReading struct {
	detection *DetectionResult
	// dryRun says the recipe was asked; refusal is its answer.
	dryRun  bool
	refusal error
	// unavailable says why the tree could not be read at all.
	unavailable string
}

// deploymentEvaluation is a deploy-time preflight: the candidate it judged
// the plan against, where that candidate came from, and what it found.
type deploymentEvaluation struct {
	Candidate DetectedCandidate `json:"candidate"`
	// CandidateSource is "detected" (read from the commit being built),
	// "recorded" (stored when the plan was saved) or "plan" (the plan's own
	// method and root, for a source with no files to read).
	CandidateSource string             `json:"candidateSource"`
	Findings        []PreflightFinding `json:"findings"`
}

// readDeploymentSource detects the checkout at root, data only and bounded,
// and asks the recipe whether it would prepare the plan from it.
func readDeploymentSource(ctx context.Context, root string, plan deploymentPlan) sourceReading {
	reading := sourceReading{}
	if detection, err := (Detector{}).DetectPath(ctx, root, plan.Identity); err == nil {
		reading.detection = &detection
	}
	switch plan.Configuration.Build.Method {
	case BuildRecipe, BuildDockerfile, BuildStatic:
		reading.dryRun = true
		if err := dryRunBuild(ctx, root, plan.Configuration.Build, plan.BuildVariables); err != nil {
			reading.refusal = checkoutRefusalFor(err, root)
		}
	}
	return reading
}

// checkoutRefusal is a dry run's error with the checkout's host path taken
// out of its text: the checkout is private and temporary, and its location
// is nothing the operator can act on.
type checkoutRefusal struct {
	cause error
	text  string
}

func (e checkoutRefusal) Error() string { return e.text }
func (e checkoutRefusal) Unwrap() error { return e.cause }

// inspectDeploymentSource reads the commit plan.Identity names through a
// temporary planning checkout. A source without files reads as nothing; one
// that cannot be read says why, and the evaluation falls back to what was
// stored when the plan was saved.
func inspectDeploymentSource(ctx context.Context, inspector SourceInspector, plan deploymentPlan) (sourceReading, SourceIdentity) {
	if inspector == nil || !sourceHasTree(plan.Source) {
		return sourceReading{}, plan.Identity
	}
	var reading sourceReading
	identity := plan.Identity
	err := inspector.InspectRevision(ctx, plan.Source, plan.Identity, func(root string, inspected SourceIdentity) error {
		identity = inspected
		inspectedPlan := plan
		inspectedPlan.Identity = inspected
		reading = readDeploymentSource(ctx, root, inspectedPlan)
		return nil
	})
	if err != nil {
		reading = sourceReading{unavailable: sourceInspectionUnavailable(err)}
	}
	return reading, identity
}

// sourceInspectionUnavailable words why a commit could not be read, without
// Git's own output, which never leaves the planning boundary.
func sourceInspectionUnavailable(err error) string {
	switch {
	case errors.Is(err, errInspectionTooLarge):
		return fmt.Sprintf("the commit has more than %d files or %d MiB, more than a check copies",
			localInspectionMaxFiles, localInspectionMaxBytes>>20)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "reading the commit took too long"
	case errors.Is(err, ErrSourceUnavailable), errors.Is(err, ErrGitUnavailable):
		return "the commit could not be fetched for inspection"
	}
	return "the commit could not be read"
}

// evaluateDeployment is preflight for a saved plan and the commit it is
// about to build. The candidate is the one detection finds in that commit at
// the plan's root with the plan's method; lacking one, the candidate stored
// when the plan was saved; lacking that, the plan's own shape, which is all
// an image or Compose source has.
func evaluateDeployment(
	ctx context.Context,
	plan deploymentPlan,
	reading sourceReading,
	observer PreflightObserver,
) (deploymentEvaluation, error) {
	build := plan.Configuration.Build
	stored := plannedDetectionCandidate(&DetectionResult{Candidates: plan.Evidence.Candidates}, build)
	var fresh *DetectedCandidate
	if reading.detection != nil {
		fresh = plannedDetectionCandidate(reading.detection, build)
	}
	evaluation := deploymentEvaluation{}
	switch {
	case fresh != nil:
		evaluation.Candidate, evaluation.CandidateSource = *fresh, "detected"
	case stored != nil:
		evaluation.Candidate, evaluation.CandidateSource = *stored, "recorded"
	default:
		evaluation.Candidate = newDetectedCandidate(build.RootDirectory, build.Method, DetectedCandidate{
			Name: "recorded-plan", Profile: plan.Profile, Confidence: ConfidenceHigh,
			Evidence: []DetectionEvidence{}, NeedsDecision: []string{},
		})
		evaluation.CandidateSource = "plan"
	}
	gitRequirements := plan.Evidence.GitRequirements
	if reading.detection != nil {
		gitRequirements = reading.detection.GitRequirements
	}
	source := plan.Source
	configuration := plan.Configuration
	draft := &Draft{Data: DraftData{
		Intent: &DraftIntentConfig{Name: "deployment", Profile: plan.Profile},
		Source: &source,
		Detection: &DetectionResult{
			Source: plan.Identity, Candidates: []DetectedCandidate{evaluation.Candidate},
			SelectedID: evaluation.Candidate.ID, Compose: plan.Evidence.Compose, GitRequirements: gitRequirements,
		},
		Configuration: &configuration,
	}}
	request := preflightObservationRequest(draft, configuration)
	request.ExistingProxySite = plan.ProxySite
	request.ExistingRuntimeID, request.ExistingRuntimeKind = plan.RuntimeID, plan.RuntimeKind
	observation, err := observer.Observe(ctx, request)
	if err != nil {
		return deploymentEvaluation{}, err
	}
	// The plan was authorized when it was saved; a deployment of it needs no
	// second administrator review, which is what analyze_plan always assumed.
	findings := withoutWizardFindings(preflightFindings(draft, configuration, observation, true))
	if reading.dryRun {
		findings = applyDryRunVerdict(findings, reading.refusal, build)
	}
	if fresh != nil && stored != nil {
		findings = append(findings, planDriftFindings(stored, fresh, configuration)...)
	}
	if reading.unavailable != "" {
		findings = append(findings, finding("source_inspection_unavailable", PreflightUnavailable,
			"The commit could not be read before deploying", reading.unavailable,
			"Checks that read the source were judged against the evidence stored with the plan; the deployment reads the commit again before it builds.",
			"", "git", "source"))
	}
	evaluation.Findings = findings
	return evaluation, nil
}

// withoutWizardFindings drops what only means something while a plan is
// being chosen: which candidate detection selected, whether the choice was
// ambiguous, a scan bound, and the notice that the method was overridden.
// A saved plan has made those choices.
func withoutWizardFindings(findings []PreflightFinding) []PreflightFinding {
	kept := findings[:0:0]
	for _, item := range findings {
		switch item.Code {
		case "detection_ambiguous", "detection_selected", "detection_empty", "detection_truncated",
			"detection_root_mismatch":
			continue
		case "build_method_changed":
			if item.Severity == PreflightPass {
				continue
			}
		}
		kept = append(kept, item)
	}
	return kept
}

// planDriftFindings compares what detection said when the plan was saved
// with what it says about the commit being built, and warns where the plan
// still carries the old answer.
func planDriftFindings(stored, fresh *DetectedCandidate, configuration PlanConfiguration) []PreflightFinding {
	build := configuration.Build
	findings := []PreflightFinding{}
	if build.Method == BuildRecipe && fresh.Recipe == "node" {
		setChanged := !sameNames(stored.PackageManagers, fresh.PackageManagers)
		managerChanged := stored.PackageManager != fresh.PackageManager && stored.PackageManager != "" &&
			fresh.PackageManager != "" && (build.PackageManager == "" || build.PackageManager == stored.PackageManager)
		if setChanged || managerChanged {
			measured := "lockfiles " + namesOrNone(stored.PackageManagers) + " → " + namesOrNone(fresh.PackageManagers)
			if managerChanged {
				measured += "; installs with " + fresh.PackageManager + " instead of " + stored.PackageManager
			}
			findings = append(findings, finding("plan_drift_package_manager", PreflightWarning,
				"The commit's lockfiles changed since the plan was saved", measured,
				"The install runs from the lockfiles in this commit, which are not the ones the plan was reviewed with.",
				"Check the package manager in Build settings; Detect again proposes the current one.",
				"deploy", "configuration.build.packageManager"))
		}
	}
	if stored.Framework != "" && fresh.Framework != "" && stored.Framework != fresh.Framework {
		findings = append(findings, finding("plan_drift_framework", PreflightWarning,
			"The framework detected in this commit changed", stored.Framework+" → "+fresh.Framework,
			"The plan's commands and output were chosen for the framework detected when it was saved.",
			"Review Build settings; Detect again proposes the commands and output for "+fresh.Framework+".",
			"deploy", "configuration.build"))
	}
	if stored.OutputDirectory != fresh.OutputDirectory && build.OutputDirectory == stored.OutputDirectory &&
		build.OutputDirectory != "" {
		detected := fresh.OutputDirectory
		if detected == "" {
			detected = "a server, not a static output"
		}
		findings = append(findings, finding("plan_drift_output_directory", PreflightWarning,
			"The build output detected in this commit moved", build.OutputDirectory+" → "+detected,
			"The plan still copies the output directory detected when it was saved, which this commit may not produce.",
			"Set the output directory in Build settings to what this commit builds.",
			"deploy", "configuration.build.outputDirectory"))
	}
	known := map[string]bool{}
	for _, variable := range stored.Variables {
		known[variable.Name] = true
	}
	configured := configuredVariableNames(configuration, nil)
	added := []string{}
	for _, variable := range fresh.Variables {
		// What the environment check names on its own — a read without a
		// default, a value the dashboard generates or binds, a registry
		// credential the install needs — is not listed a second time.
		if !known[variable.Name] && !configured[variable.Name] && !variable.Required && !variable.RequiredRead &&
			variable.Setup == "" && variable.Step == "" {
			added = append(added, variable.Name)
		}
	}
	if len(added) > 0 {
		findings = append(findings, finding("plan_drift_variables", PreflightWarning,
			"This commit reads variables the plan does not set", strings.Join(added, ", "),
			"They were not in the source when the plan was saved, so nothing has given them a value.",
			"Add the ones the application needs in Variables.", "deploy", "variables"))
	}
	return findings
}

func sameNames(left, right []string) bool {
	a := append([]string(nil), left...)
	b := append([]string(nil), right...)
	sort.Strings(a)
	sort.Strings(b)
	return slices.Equal(a, b)
}

func namesOrNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// findingSentence is a blocked finding as one readable terminal reason.
func findingSentence(item PreflightFinding) string {
	text := item.Title
	if item.Measured != "" {
		text += ": " + item.Measured
	}
	if !strings.HasSuffix(text, ".") {
		text += "."
	}
	if item.Action != "" {
		text += " " + item.Action
	}
	return text
}

// buildScopedNames are the variables the build stage receives.
func buildScopedNames(variables []ReleaseVariableSnapshot) []string {
	names := []string{}
	for _, variable := range variables {
		if slices.Contains(strings.Split(variable.Scopes, ","), "build") {
			names = append(names, variable.Name)
		}
	}
	sort.Strings(names)
	return names
}

// DeploymentCheckRequest names the version an advisory check is for, the way
// a deployment request names the version it builds.
type DeploymentCheckRequest struct {
	SourceRevision string `json:"sourceRevision,omitempty"`
	Ref            string `json:"ref,omitempty"`
}

// DeploymentCheckResult is preflight for the environment's saved plan and
// the commit a deployment would build now.
type DeploymentCheckResult struct {
	Findings       []PreflightFinding `json:"findings"`
	PlanRevision   int                `json:"planRevision"`
	SourceRevision string             `json:"sourceRevision,omitempty"`
	CheckedAt      time.Time          `json:"checkedAt"`
}

type deploymentCheckSources interface {
	SourceInspector
	ResolveGitRef(ctx context.Context, source DraftSourceConfig, ref string) (string, error)
}

type deploymentCheckKey struct {
	environmentID  int64
	planRevision   int
	sourceRevision string
}

type deploymentCheckEntry struct {
	result  DeploymentCheckResult
	expires time.Time
}

// deploymentCheckTTL is how long an advisory check is answered from memory.
// A project page asks on arrival and after each save; the answer only moves
// with the plan revision or the commit, both of which are in the key, or
// with host facts such as a port or DNS, which a few minutes cannot mislead.
const deploymentCheckTTL = 3 * time.Minute

// DeploymentChecker answers the advisory check with the evaluation
// analyze_plan runs, for the environment's desired plan.
type DeploymentChecker struct {
	runs     *OrchestrationStore
	planning *PlanningStore
	sources  deploymentCheckSources
	observer PreflightObserver
	now      func() time.Time

	mu    sync.Mutex
	cache map[deploymentCheckKey]deploymentCheckEntry
}

func NewDeploymentChecker(
	runs *OrchestrationStore,
	planning *PlanningStore,
	sources *HostSourceAnalyzer,
	observer *HostPreflightObserver,
) *DeploymentChecker {
	checker := &DeploymentChecker{
		runs: runs, planning: planning,
		now: time.Now, cache: map[deploymentCheckKey]deploymentCheckEntry{},
	}
	// A module that is not wired stays a nil interface, which the checks
	// read as unavailable; a typed nil pointer would read as present and
	// fail on first use.
	if sources != nil {
		checker.sources = sources
	}
	if observer != nil {
		checker.observer = observer
	}
	return checker
}

// Check evaluates the environment's desired plan against the commit a
// deployment would build: the requested revision or ref, resolved exactly as
// a deployment request resolves it, or the branch head.
func (c *DeploymentChecker) Check(
	ctx context.Context,
	projectID, environmentID int64,
	request DeploymentCheckRequest,
) (*DeploymentCheckResult, error) {
	if request.SourceRevision != "" && request.Ref != "" {
		return nil, fmt.Errorf("%w: sourceRevision and ref cannot both be set", ErrInvalidPlan)
	}
	plan, revision, err := c.desiredPlan(ctx, projectID, environmentID)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		// The compatibility pipeline has no preflight of its own to share.
		return &DeploymentCheckResult{Findings: []PreflightFinding{}, PlanRevision: revision, CheckedAt: c.now().UTC()}, nil
	}
	if err := c.resolveRevision(ctx, plan, request); err != nil {
		return nil, err
	}
	key := deploymentCheckKey{environmentID: environmentID, planRevision: revision, sourceRevision: immutableSourceRevision(plan.Identity)}
	if cached, ok := c.cached(key); ok {
		return &cached, nil
	}
	reading, identity := inspectDeploymentSource(ctx, c.sources, *plan)
	plan.Identity = identity
	if c.observer == nil {
		return nil, fmt.Errorf("%w: deployment host preflight is unavailable", ErrBuilderUnavailable)
	}
	evaluation, err := evaluateDeployment(ctx, *plan, reading, c.observer)
	if err != nil {
		return nil, err
	}
	result := DeploymentCheckResult{
		Findings: evaluation.Findings, PlanRevision: revision,
		SourceRevision: identity.Revision, CheckedAt: c.now().UTC(),
	}
	c.store(key, result)
	if identity.Revision != key.sourceRevision {
		c.store(deploymentCheckKey{environmentID, revision, identity.Revision}, result)
	}
	return &result, nil
}

func (c *DeploymentChecker) cached(key deploymentCheckKey) (DeploymentCheckResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.cache[key]
	if !ok || !c.now().Before(entry.expires) || key.sourceRevision == "" {
		return DeploymentCheckResult{}, false
	}
	return entry.result, true
}

func (c *DeploymentChecker) store(key deploymentCheckKey, result DeploymentCheckResult) {
	if key.sourceRevision == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for existing, entry := range c.cache {
		if !now.Before(entry.expires) {
			delete(c.cache, existing)
		}
	}
	c.cache[key] = deploymentCheckEntry{result: result, expires: now.Add(deploymentCheckTTL)}
}

// resolveRevision pins the commit a deployment would build now: the same
// resolution the run request performs, so the check is about that commit.
func (c *DeploymentChecker) resolveRevision(ctx context.Context, plan *deploymentPlan, request DeploymentCheckRequest) error {
	if !IsRemoteGitSource(plan.Source) {
		if request.SourceRevision != "" || request.Ref != "" {
			return ErrRefNotApplicable
		}
		return nil
	}
	if c.sources == nil && request.SourceRevision == "" {
		return fmt.Errorf("%w: %w: source inspection is unavailable", ErrSourceUnavailable, ErrGitUnavailable)
	}
	switch {
	case request.SourceRevision != "":
		if !validGitObjectID(request.SourceRevision) {
			return fmt.Errorf("%w: sourceRevision is not a Git object id", ErrInvalidRef)
		}
		plan.Identity.Revision = request.SourceRevision
	case request.Ref != "":
		revision, err := c.sources.ResolveGitRef(ctx, plan.Source, request.Ref)
		if err != nil {
			return err
		}
		plan.Identity.Revision = revision
	default:
		revision, err := c.sources.ResolveGitRevision(ctx, plan.Source)
		if err != nil {
			return err
		}
		plan.Identity.Revision = revision
	}
	return nil
}

// desiredPlan reads the environment's saved plan as a deployment of it now
// would run it. A nil plan is a compatibility-pipeline project.
func (c *DeploymentChecker) desiredPlan(ctx context.Context, projectID, environmentID int64) (*deploymentPlan, int, error) {
	target, err := c.runs.EnvironmentExecutionTarget(ctx, projectID, environmentID)
	if err != nil {
		return nil, 0, err
	}
	if target.BuildMethod == BuildLegacyCompose {
		return nil, target.DesiredRevision, nil
	}
	desired, err := c.planning.EnvironmentConfiguration(ctx, projectID, environmentID)
	if err != nil {
		return nil, 0, err
	}
	if desired.Source == nil || desired.Identity == nil {
		return nil, desired.Revision, nil
	}
	evidence, err := c.planning.storedBuildEvidence(ctx, environmentID, desired.Revision)
	if err != nil {
		return nil, 0, err
	}
	profile, err := c.runs.deploymentProfile(ctx, projectID)
	if err != nil {
		return nil, 0, err
	}
	variables := make([]PlannedVariable, 0, len(desired.Variables))
	buildVariables := []string{}
	for _, variable := range desired.Variables {
		variables = append(variables, PlannedVariable{Name: variable.Name, Sensitivity: variable.Sensitivity, Scopes: variable.Scopes})
		if slices.Contains(variable.Scopes, "build") {
			buildVariables = append(buildVariables, variable.Name)
		}
	}
	sort.Strings(buildVariables)
	configuration := canonicalConfiguration(PlanConfiguration{
		Build: desired.Build, Runtime: desired.Runtime, Variables: variables,
		Dependencies: desired.Dependencies, Checks: desired.Checks, Domains: desired.Domains,
	})
	plan := &deploymentPlan{
		Source: canonicalSourceConfig(*desired.Source), Identity: *desired.Identity, Profile: profile,
		Configuration: configuration, Evidence: evidence, BuildVariables: buildVariables,
		ProxySite: deploymentRouteName(environmentID),
	}
	live, err := c.runs.LiveRelease(ctx, environmentID)
	switch {
	case err == nil:
		runtime, runtimeErr := c.runs.RuntimeForRelease(ctx, live.Release.ID)
		if runtimeErr != nil && !errors.Is(runtimeErr, ErrArtifactMissing) && !errors.Is(runtimeErr, ErrNotFound) {
			return nil, 0, runtimeErr
		}
		if runtime != nil {
			plan.RuntimeID, plan.RuntimeKind = runtime.RuntimeID, runtime.Kind
		}
	case !errors.Is(err, ErrArtifactMissing):
		return nil, 0, err
	}
	return plan, desired.Revision, nil
}

// storedBuildEvidence is the detection evidence saved with a plan revision.
func (s *PlanningStore) storedBuildEvidence(ctx context.Context, environmentID int64, revision int) (StoredBuildEvidence, error) {
	var encoded string
	if err := s.db.QueryRowContext(ctx, `
		SELECT evidence_json FROM deploy_build_plans WHERE environment_id = ? AND revision = ?`,
		environmentID, revision).Scan(&encoded); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StoredBuildEvidence{}, fmt.Errorf("%w: build plan revision %d is missing", ErrInvalidPlan, revision)
		}
		return StoredBuildEvidence{}, err
	}
	var evidence StoredBuildEvidence
	if encoded != "" && json.Unmarshal([]byte(encoded), &evidence) != nil {
		return StoredBuildEvidence{}, fmt.Errorf("%w: stored detection evidence is malformed", ErrInvalidPlan)
	}
	return evidence, nil
}
