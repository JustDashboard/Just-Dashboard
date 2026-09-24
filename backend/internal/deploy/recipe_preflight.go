package deploy

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
)

// A recipe's refusals are decided by reading the tree — which lockfiles sit
// at the root, what the manifest declares, which packages are main — and a
// refusal knowable from the tree must not wait for a build slot to be said.
// Preflight therefore asks the recipe itself: Prepare runs over the checkout
// exactly as prepare_context would, except that every reviewed base resolves
// to a placeholder digest and the generated Dockerfile is never written.
// Nothing is pulled, built or executed, so the security model is the one
// detection already has: repository files are read as bounded data.

// placeholderBaseDigest stands in for every reviewed base during a dry run.
// It is never written anywhere a build reads: the rendered Dockerfile is
// discarded, and the real preparation resolves each base to its digest.
const placeholderBaseDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

type placeholderBases struct{}

func (placeholderBases) ResolveImage(_ context.Context, reference, _ string) (ResolvedImage, error) {
	return ResolvedImage{Reference: reference, Digest: placeholderBaseDigest, Platforms: []string{}}, nil
}

func (placeholderBases) BuildImage(context.Context, BuildInvocation, func(BuildLog) error) (ResolvedImage, error) {
	return ResolvedImage{}, ErrBuilderUnavailable
}

func (placeholderBases) PullImage(context.Context, string, string, func(BuildLog) error) (ResolvedImage, error) {
	return ResolvedImage{}, ErrBuilderUnavailable
}

func (placeholderBases) InspectImage(context.Context, string) (ResolvedImage, error) {
	return ResolvedImage{}, ErrBuilderUnavailable
}

func (placeholderBases) RemoveImage(context.Context, string) error { return ErrBuilderUnavailable }

// errBuildRootMissing is a dry run's verdict on a root directory the
// checkout does not have, which prepare_context would fail on as well.
var errBuildRootMissing = errors.New("the build root directory does not exist in this commit")

// dryRunBuild asks the recipe whether it would prepare this plan from the
// checkout at sourceRoot. The error is the one prepare_context would return.
func dryRunBuild(ctx context.Context, sourceRoot string, build BuildPlanConfig, buildVariableNames []string) error {
	buildRoot := sourceRoot
	if build.RootDirectory != "" {
		var err error
		if buildRoot, err = containedSubdirectory(sourceRoot, build.RootDirectory); err != nil {
			return errBuildRootMissing
		}
	}
	builder := &ArtifactBuilder{backend: placeholderBases{}, dryRun: true}
	_, err := builder.Prepare(ctx, buildRoot, build, false, "just-dashboard/preflight:dry-run", buildVariableNames...)
	return err
}

// recipeRefusalText is a refusal as the operator reads it: without the
// sentinel's prefix, without the host path of the inspected checkout, and
// bounded like any other detection text.
func recipeRefusalText(err error, root string) string {
	text := withoutCheckoutPath(strings.TrimPrefix(err.Error(), ErrUnsupportedBuilder.Error()+": "), root)
	if len(text) > 500 {
		text = text[:497] + "..."
	}
	if rejectPlanSecretLiteral("recipe refusal", text) != nil {
		return "the recipe refuses this source (details withheld because they resemble credential material)"
	}
	return text
}

// withoutCheckoutPath takes the host path of an inspected checkout out of
// text, as the checkout's root reads it and as its resolved path does: the
// recipe opens a root directory through its resolved path, so a checkout
// under a linked directory names the link's target in its errors.
func withoutCheckoutPath(text, root string) string {
	if root == "" {
		return text
	}
	paths := []string{root}
	if resolved, err := filepath.EvalSymlinks(root); err == nil && resolved != root {
		paths = append(paths, resolved)
	}
	// The longer path first, so one that contains the other is not left
	// half replaced.
	sort.Slice(paths, func(i, j int) bool { return len(paths[i]) > len(paths[j]) })
	for _, path := range paths {
		text = strings.ReplaceAll(text, path, ".")
	}
	return text
}

// checkoutRefusalFor is a dry run's error as a finding may quote it.
func checkoutRefusalFor(err error, root string) error {
	return checkoutRefusal{cause: err, text: withoutCheckoutPath(err.Error(), root)}
}

// recipeRefusalField points a refusal at the setting that decides it, so the
// finding opens the field rather than the build page's top.
func recipeRefusalField(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "main package"):
		return "configuration.build.goPackage"
	case strings.Contains(lower, "lockfile") || strings.Contains(lower, "package manager") || strings.Contains(lower, "the build uses"):
		return "configuration.build.packageManager"
	case strings.Contains(lower, "start command"):
		return "configuration.build.startCommand"
	case strings.Contains(lower, "output"):
		return "configuration.build.outputDirectory"
	case strings.Contains(lower, "go toolchain") || strings.Contains(lower, "go recipe supports") || strings.Contains(lower, "go.mod"):
		return "configuration.build.goVersion"
	case strings.Contains(lower, "python recipe supports"):
		return "configuration.build.pythonVersion"
	case strings.Contains(lower, "dockerfile"):
		return "configuration.build.dockerfile"
	}
	return "configuration.build"
}

// dryRunFinding is the finding a dry run's error becomes. Errors that are not
// the tree's verdict — no builder, a variable name — are left to the checks
// that own them and report nothing here.
func dryRunFinding(err error, build BuildPlanConfig) (PreflightFinding, bool) {
	switch {
	case err == nil:
		return PreflightFinding{}, false
	case errors.Is(err, errBuildRootMissing):
		return finding("build_root_missing", PreflightBlocked,
			"The root directory is not in this commit", build.RootDirectory,
			"The build starts in the configured root directory, and the commit being deployed has no directory there.",
			"Correct the root directory in Build settings, or deploy a commit that has it.",
			"deploy", "configuration.build.rootDirectory"), true
	case errors.Is(err, ErrUnsupportedBuilder):
		text := recipeRefusalText(err, "")
		return finding("recipe_unsupported", PreflightBlocked,
			"The build would be refused", text,
			"The recipe reads this commit and refuses the plan before any image is built, so the deployment would stop at prepare_context.",
			"Change the setting it names in Build settings or the file it names in the repository, or build from a Dockerfile.",
			"deploy", recipeRefusalField(text)), true
	case errors.Is(err, ErrBuilderUnavailable), errors.Is(err, ErrInvalidVariable), errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		return PreflightFinding{}, false
	}
	return finding("recipe_check_incomplete", PreflightWarning,
		"The build plan could not be checked against this commit", recipeRefusalText(err, ""),
		"Reading the files the recipe needs failed, so preparing the build may fail the same way.",
		"Check that the files the recipe reads are regular files the checkout contains.",
		"deploy", "configuration.build"), true
}

// buildRefusalCodes are the findings that already name why the build plan
// cannot be prepared. When one is present, a dry run's refusal is the same
// problem told less precisely, and adding it would be the second of two
// findings about one cause.
var buildRefusalCodes = map[string]bool{
	"package_manager_lockfile_missing": true, "package_manager_ambiguous": true,
	"go_version_unsupported": true, "go_main_missing": true, "go_main_ambiguous": true,
	"python_version_unsupported": true, "start_command_missing": true,
}

// applyDryRunVerdict merges a dry run into findings computed from detection.
// A candidate's RecipeIssue was decided with detection's proposed settings,
// not the plan's, so once the recipe has spoken for the plan that relay is
// dropped: either the plan prepares, or the dry run says why it does not.
func applyDryRunVerdict(findings []PreflightFinding, err error, build BuildPlanConfig) []PreflightFinding {
	kept := findings[:0:0]
	named := false
	for _, item := range findings {
		if item.Code == "recipe_unsupported" {
			continue
		}
		if (item.Severity == PreflightBlocked || item.Severity == PreflightDecision) && buildRefusalCodes[item.Code] {
			named = true
		}
		kept = append(kept, item)
	}
	refusal, ok := dryRunFinding(err, build)
	if !ok || (named && refusal.Code == "recipe_unsupported") {
		return kept
	}
	return append(kept, refusal)
}

// applyDetectedRecipeIssues asks the recipe about each recipe candidate with
// the settings detection proposes, so a candidate the recipe would refuse is
// marked before anything is chosen. Settings the operator supplies are not
// held against a candidate: a missing start command is asked for by
// preflight, and competing lockfiles are a choice, not a refusal, while any
// one manager prepares.
func applyDetectedRecipeIssues(ctx context.Context, root string, candidates []DetectedCandidate) {
	for index := range candidates {
		candidate := &candidates[index]
		// Go is judged from the facts detection already holds: main packages,
		// cgo files and the toolchain, each against the plan's own setting.
		if candidate.BuildMethod != BuildRecipe || candidate.Recipe == "" || candidate.Recipe == "go" ||
			candidate.RecipeIssue != "" {
			continue
		}
		if issue := detectedRecipeIssue(ctx, root, *candidate); issue != "" {
			candidate.RecipeIssue = issue
			candidate.Confidence = ConfidenceLow
		}
	}
}

func detectedRecipeIssue(ctx context.Context, root string, candidate DetectedCandidate) string {
	config := BuildPlanConfig{
		Method: BuildRecipe, Recipe: candidate.Recipe, RootDirectory: candidate.Root,
		PackageManager: candidate.PackageManager, BuildCommand: candidate.BuildCommand,
		StartCommand: candidate.StartCommand, OutputDirectory: candidate.OutputDirectory,
		SPAFallback: candidate.SPAFallback,
	}
	if strings.TrimSpace(config.StartCommand) == "" {
		config.StartCommand = "true"
	}
	err := dryRunBuild(ctx, root, config, nil)
	if err != nil && candidate.Recipe == "node" && candidate.PackageManager == "" {
		for _, manager := range candidate.PackageManagers {
			config.PackageManager = manager
			if dryRunBuild(ctx, root, config, nil) == nil {
				return ""
			}
		}
	}
	if err == nil || !errors.Is(err, ErrUnsupportedBuilder) {
		return ""
	}
	return recipeRefusalText(err, root)
}

// withDraftSourceChecks reads the commit a draft was reviewed against. The
// recipe's own verdict on the plan replaces what detection could say about
// it without the tree, and a root edited to a directory detection found
// nothing in is only a warning once the recipe has prepared it. A remote
// branch that has moved on since is named, since the first deployment builds
// the reviewed commit and every later one follows the branch.
func withDraftSourceChecks(
	ctx context.Context,
	draft *Draft,
	configuration PlanConfiguration,
	inspector SourceInspector,
	findings []PreflightFinding,
) []PreflightFinding {
	source, identity := *draft.Data.Source, draft.Data.Detection.Source
	if inspector == nil || !sourceHasTree(source) || identity.Revision == "" {
		return findings
	}
	ran := false
	var refusal error
	err := inspector.InspectRevision(ctx, source, identity, func(root string, _ SourceIdentity) error {
		switch configuration.Build.Method {
		case BuildRecipe, BuildDockerfile, BuildStatic:
			ran = true
			if err := dryRunBuild(ctx, root, configuration.Build, nil); err != nil {
				refusal = checkoutRefusalFor(err, root)
			}
		}
		return nil
	})
	switch {
	case err != nil:
		findings = append(findings, finding("source_inspection_unavailable", PreflightUnavailable,
			"The reviewed commit could not be read again", sourceInspectionUnavailable(err),
			"The build plan was checked against detection alone; the deployment reads the commit before it builds.",
			"", "git", "source"))
	case ran:
		findings = applyDryRunVerdict(findings, refusal, configuration.Build)
		if refusal == nil {
			for index := range findings {
				if findings[index].Code == "detection_root_mismatch" {
					findings[index].Severity = PreflightWarning
					findings[index].Means = "Detection found no " + string(configuration.Build.Method) +
						" build there, so its framework checks did not run; the recipe prepares the plan from that directory."
				}
			}
		}
	}
	if IsRemoteGitSource(source) {
		if head, err := inspector.ResolveGitRevision(ctx, source); err == nil && validGitObjectID(head) && head != identity.Revision {
			findings = append(findings, finding("source_moved", PreflightWarning,
				"The branch moved since it was inspected", shortRevision(identity.Revision)+" → "+shortRevision(head),
				"Review checked commit "+shortRevision(identity.Revision)+", and the first deployment builds that commit; later deployments follow the branch, which now points at "+shortRevision(head)+".",
				"Inspect again to review the newer commit, or deploy the reviewed one.", "git", "source.ref"))
		}
	}
	return findings
}

func shortRevision(revision string) string {
	if len(revision) > 7 {
		return revision[:7]
	}
	return revision
}
