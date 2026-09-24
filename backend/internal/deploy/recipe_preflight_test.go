package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// treeInspectorFake hands a fixture directory to the inspection callback the
// way the planning checkout hands its worktree, and reports a branch head.
type treeInspectorFake struct {
	root  string
	head  string
	err   error
	calls int
}

func (f *treeInspectorFake) InspectRevision(
	_ context.Context,
	_ DraftSourceConfig,
	identity SourceIdentity,
	inspect func(root string, identity SourceIdentity) error,
) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	return inspect(f.root, identity)
}

func (f *treeInspectorFake) ResolveGitRevision(context.Context, DraftSourceConfig) (string, error) {
	return f.head, nil
}

func (f *treeInspectorFake) ResolveGitRef(context.Context, DraftSourceConfig, string) (string, error) {
	return f.head, nil
}

// draftForTree is a remote Git draft whose detection is the tree's own.
func draftForTree(t *testing.T, root string, build BuildPlanConfig) (*Draft, PlanConfiguration) {
	t.Helper()
	identity := SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)}
	detection, err := (Detector{}).DetectPath(t.Context(), root, identity)
	if err != nil {
		t.Fatal(err)
	}
	configuration := canonicalConfiguration(PlanConfiguration{
		Build: build, Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst},
	})
	return &Draft{Data: DraftData{
		Intent:        &DraftIntentConfig{Name: "fixture", Profile: ProfileWeb},
		Source:        &DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/owner/app.git", Ref: "main"},
		Detection:     &detection,
		Configuration: &configuration,
	}}, configuration
}

func draftTreeFindings(t *testing.T, root string, build BuildPlanConfig, inspector SourceInspector) []PreflightFinding {
	t.Helper()
	draft, configuration := draftForTree(t, root, build)
	findings := preflightFindings(draft, configuration, HostObservation{}, true)
	return withDraftSourceChecks(t.Context(), draft, configuration, inspector, findings)
}

// Every refusal the Node and PHP recipes make from what a tree holds is a
// finding before Deploy: the dry run is the recipe's own preparation, so a
// refusal it makes and one prepare_context makes are the same refusal.
func TestRecipeRefusalsKnowableFromTheTreeAreFoundBeforeDeploy(t *testing.T) {
	next := `{"name":"web","scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16.0.0"}}`
	svelte := `{"name":"kit","scripts":{"build":"vite build"},"devDependencies":{"@sveltejs/kit":"2.0.0"}}`
	laravel := `{"require":{"php":"^8.3","laravel/framework":"^12.0"}}`
	assets := `{"scripts":{"build":"vite build"},"devDependencies":{"laravel-vite-plugin":"1.0.0","vite":"6.0.0"}}`
	for _, fixture := range []struct {
		name  string
		files map[string]string
		build BuildPlanConfig
	}{
		{"no lockfile", map[string]string{"package.json": next},
			BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "npm run build", StartCommand: "npm run start"}},
		{"chosen manager without its lockfile", map[string]string{"package.json": next, "bun.lock": "{}"},
			BuildPlanConfig{Method: BuildRecipe, Recipe: "node", PackageManager: "pnpm", BuildCommand: "pnpm run build", StartCommand: "pnpm run start"}},
		{"competing lockfiles", map[string]string{"package.json": next, "bun.lock": "{}", "package-lock.json": "{}"},
			BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "npm run build", StartCommand: "npm run start"}},
		{"bun.lock beside bun.lockb", map[string]string{"package.json": next, "bun.lock": "{}", "bun.lockb": "binary"},
			BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "bun run build", StartCommand: "bun run start"}},
		{"malformed manifest", map[string]string{"package.json": "{", "bun.lock": "{}"},
			BuildPlanConfig{Method: BuildRecipe, Recipe: "node", StartCommand: "bun run start"}},
		{"SvelteKit without an adapter", map[string]string{"package.json": svelte, "bun.lock": "{}"},
			BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "bun run build", OutputDirectory: "build"}},
		{"server with no start command", map[string]string{"package.json": next, "bun.lock": "{}"},
			BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "bun run build"}},
		{"Laravel assets without a lockfile", map[string]string{"composer.json": laravel, "composer.lock": "{}", "package.json": assets},
			BuildPlanConfig{Method: BuildRecipe, Recipe: "php", StartCommand: "frankenphp php-server --root public/"}},
		{"Laravel assets with competing lockfiles", map[string]string{"composer.json": laravel, "composer.lock": "{}", "package.json": assets, "yarn.lock": "", "package-lock.json": "{}"},
			BuildPlanConfig{Method: BuildRecipe, Recipe: "php", StartCommand: "frankenphp php-server --root public/"}},
		{"PHP with no start command", map[string]string{"composer.json": laravel, "composer.lock": "{}"},
			BuildPlanConfig{Method: BuildRecipe, Recipe: "php"}},
		{"root directory missing", map[string]string{"package.json": next, "bun.lock": "{}"},
			BuildPlanConfig{Method: BuildRecipe, Recipe: "node", RootDirectory: "apps/web", StartCommand: "bun run start"}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := t.TempDir()
			for path, content := range fixture.files {
				writeBuildFixture(t, root, path, content)
			}
			refusal := dryRunBuild(t.Context(), root, fixture.build, nil)
			if !errors.Is(refusal, ErrUnsupportedBuilder) && !errors.Is(refusal, errBuildRootMissing) {
				t.Fatalf("dry run did not refuse: %v", refusal)
			}
			if fixture.build.RootDirectory == "" {
				_, prepared := NewArtifactBuilder(&artifactBackendFake{}).Prepare(t.Context(), root, fixture.build, false, "fixture:refused")
				if prepared == nil || prepared.Error() != refusal.Error() {
					t.Fatalf("prepare_context refuses differently: %v, dry run: %v", prepared, refusal)
				}
			}
			findings := draftTreeFindings(t, root, fixture.build, &treeInspectorFake{root: root, head: strings.Repeat("a", 40)})
			found := false
			for _, item := range findings {
				if (item.Severity == PreflightBlocked || item.Severity == PreflightDecision) &&
					strings.HasPrefix(item.FieldID, "configuration.build") {
					found = true
				}
			}
			if !found {
				t.Fatalf("no finding before deploy for %v: %#v", refusal, findings)
			}
			for _, item := range findings {
				if strings.Contains(item.Measured, root) {
					t.Fatalf("a finding carries the checkout's host path: %#v", item)
				}
			}
		})
	}
}

func TestDryRunVerdictReplacesWhatDetectionCouldNotKnow(t *testing.T) {
	build := BuildPlanConfig{Method: BuildRecipe, Recipe: "node"}
	stale := finding("recipe_unsupported", PreflightBlocked, "Source needs a different build plan", "stale", "", "", "deploy", "configuration.build")
	named := finding("package_manager_ambiguous", PreflightBlocked, "Competing lockfiles need a package manager", "bun, npm", "", "", "deploy", "configuration.build.packageManager")
	refused := errors.Join(ErrUnsupportedBuilder, errors.New("the build uses pnpm, but the source has no pnpm lockfile"))

	if got := applyDryRunVerdict([]PreflightFinding{stale}, nil, build); len(got) != 0 {
		t.Fatalf("a plan the recipe prepares kept a stale refusal: %#v", got)
	}
	if got := applyDryRunVerdict([]PreflightFinding{stale, named}, refused, build); len(got) != 1 || got[0].Code != named.Code {
		t.Fatalf("a refusal already named was reported twice: %#v", got)
	}
	got := applyDryRunVerdict([]PreflightFinding{stale}, refused, build)
	if len(got) != 1 || got[0].Code != "recipe_unsupported" || got[0].Measured == "stale" ||
		got[0].FieldID != "configuration.build.packageManager" {
		t.Fatalf("dry run refusal = %#v", got)
	}
	if got := applyDryRunVerdict(nil, ErrBuilderUnavailable, build); len(got) != 0 {
		t.Fatalf("an unavailable builder was reported as the tree's verdict: %#v", got)
	}
	if got := applyDryRunVerdict(nil, errBuildRootMissing, build); len(got) != 1 || got[0].Code != "build_root_missing" {
		t.Fatalf("missing root = %#v", got)
	}
}

// A candidate the recipe would refuse is marked when it is detected, at low
// confidence, so it is neither selected over a buildable one nor offered as
// ready; a choice the operator can still make is not held against it.
func TestDetectionMarksCandidatesTheRecipeWouldRefuse(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "package.json", `{"name":"web","scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16.0.0"}}`)
	detection, err := (Detector{}).DetectPath(t.Context(), root, SourceIdentity{})
	if err != nil || len(detection.Candidates) != 1 {
		t.Fatalf("detect: %#v, %v", detection, err)
	}
	if candidate := detection.Candidates[0]; !strings.Contains(candidate.RecipeIssue, "require a lockfile") ||
		candidate.Confidence != ConfidenceLow {
		t.Fatalf("a Next.js app without a lockfile read as buildable: %#v", candidate)
	}

	writeBuildFixture(t, root, "Dockerfile", "FROM scratch\n")
	detection, _ = (Detector{}).DetectPath(t.Context(), root, SourceIdentity{})
	if selected := selectedDetectionCandidate(&detection); selected == nil || selected.BuildMethod != BuildDockerfile {
		t.Fatalf("a refused recipe outranked a buildable Dockerfile: %#v", detection)
	}

	competing := t.TempDir()
	writeBuildFixture(t, competing, "package.json", `{"name":"web","scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16.0.0"}}`)
	writeBuildFixture(t, competing, "bun.lock", "{}")
	writeBuildFixture(t, competing, "package-lock.json", "{}")
	detection, _ = (Detector{}).DetectPath(t.Context(), competing, SourceIdentity{})
	if candidate := detection.Candidates[0]; candidate.RecipeIssue != "" || candidate.Confidence != ConfidenceLow {
		t.Fatalf("competing lockfiles are a choice, not a refusal, and keep their doubt: %#v", candidate)
	}

	server := t.TempDir()
	writeBuildFixture(t, server, "requirements.txt", "requests==2.32.0\n")
	detection, _ = (Detector{}).DetectPath(t.Context(), server, SourceIdentity{})
	if candidate := detection.Candidates[0]; candidate.RecipeIssue != "" {
		t.Fatalf("a missing start command, which the operator supplies, was frozen into the candidate: %#v", candidate)
	}
}

func TestDraftPreflightNamesAMovedBranchAndAnEditedRoot(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "apps/web/package.json", `{"name":"web","scripts":{"build":"vite build"},"devDependencies":{"vite":"6.0.0"}}`)
	writeBuildFixture(t, root, "apps/web/bun.lock", "{}")
	writeBuildFixture(t, root, "apps/docs/site/index.html", "<h1>docs</h1>")
	build := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", RootDirectory: "apps/web", BuildCommand: "bun run build", OutputDirectory: "dist"}
	inspector := &treeInspectorFake{root: root, head: strings.Repeat("b", 40)}
	findings := draftTreeFindings(t, root, build, inspector)
	moved := findingByCode(findings, "source_moved")
	if moved == nil || moved.Severity != PreflightWarning || moved.Measured != "aaaaaaa → bbbbbbb" {
		t.Fatalf("a moved branch was not named: %#v", findings)
	}

	// The root is edited to a directory detection found nothing in: without
	// the tree nothing has checked it, with the tree the recipe has.
	edited := BuildPlanConfig{Method: BuildStatic, RootDirectory: "apps/docs", OutputDirectory: "site"}
	draft, configuration := draftForTree(t, root, edited)
	findings = preflightFindings(draft, configuration, HostObservation{}, true)
	if mismatch := findingByCode(findings, "detection_root_mismatch"); mismatch == nil || mismatch.Severity != PreflightDecision {
		t.Fatalf("an edited root borrowed another directory's checks: %#v", findings)
	}
	findings = withDraftSourceChecks(t.Context(), draft, configuration, &treeInspectorFake{root: root, head: strings.Repeat("a", 40)}, findings)
	if mismatch := findingByCode(findings, "detection_root_mismatch"); mismatch == nil || mismatch.Severity != PreflightWarning {
		t.Fatalf("a root the recipe prepares stayed a decision: %#v", findings)
	}
	if findingByCode(findings, "source_moved") != nil {
		t.Fatal("an unmoved branch was reported as moved")
	}

	unreachable := &treeInspectorFake{err: ErrSourceUnavailable}
	findings = draftTreeFindings(t, root, build, unreachable)
	if item := findingByCode(findings, "source_inspection_unavailable"); item == nil || item.Severity != PreflightUnavailable {
		t.Fatalf("an unreadable commit was not said: %#v", findings)
	}
}
