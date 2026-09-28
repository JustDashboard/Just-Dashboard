package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const competingLockfilesManifest = `{"name":"barbershop","scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16.1.3"}%s}`

// A create-next-app repository that moved to bun keeps its original
// package-lock.json next to bun.lock. Neither file says which one is stale, so
// the builder refuses to pick until the operator or the manifest does.
func TestCompetingLockfilesBuildOnlyWithAnExplicitPackageManager(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		manifest string
		selected string
		want     []string
		refused  string
	}{
		{name: "unresolved", refused: "competing lockfiles bun.lock and package-lock.json"},
		{name: "selected bun", selected: "bun", want: []string{"FROM node:22-alpine@sha256:", "COPY --from=oven/bun:1-alpine@sha256:", "bun install --frozen-lockfile"}},
		{name: "selected npm", selected: "npm", want: []string{"FROM node:22-alpine@sha256:", "npm ci"}},
		{name: "selected without lockfile", selected: "pnpm", refused: "the build uses pnpm, but the source has no pnpm lockfile"},
		{name: "declared bun", manifest: `,"packageManager":"bun@1.2.21"`, want: []string{"COPY --from=oven/bun:1.2.21-alpine@sha256:", "bun install --frozen-lockfile"}},
		{name: "selection overrides declaration", manifest: `,"packageManager":"bun@1.2.21"`, selected: "npm", want: []string{"npm ci"}},
		{name: "declared without lockfile", manifest: `,"packageManager":"yarn@4.9.2"`, refused: "competing lockfiles"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeBuildFixture(t, root, "package.json", strings.Replace(competingLockfilesManifest, "%s", test.manifest, 1))
			writeBuildFixture(t, root, "bun.lock", "{}")
			writeBuildFixture(t, root, "package-lock.json", "{}")
			prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root,
				BuildPlanConfig{
					Method: BuildRecipe, Recipe: "node", PackageManager: test.selected,
					BuildCommand: "bun run build", StartCommand: "bun run start",
				}, false, "just-dashboard/test:run-1")
			if test.refused != "" {
				if !errors.Is(err, ErrUnsupportedBuilder) || !strings.Contains(err.Error(), test.refused) {
					t.Fatalf("error = %v, want unsupported builder naming %q", err, test.refused)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range test.want {
				if !strings.Contains(prepared.DockerfilePreview, want) {
					t.Fatalf("Dockerfile missing %q:\n%s", want, prepared.DockerfilePreview)
				}
			}
		})
	}
}

func TestDetectionReportsCompetingPackageManagers(t *testing.T) {
	for _, test := range []struct {
		name, manifest, manager, build string
		decision                       bool
	}{
		{name: "unresolved", build: "npm run build", decision: true},
		{name: "declared", manifest: `,"packageManager":"bun@1.2.21"`, manager: "bun", build: "bun run build"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writePlanningFixture(t, filepath.Join(root, "package.json"), strings.Replace(competingLockfilesManifest, "%s", test.manifest, 1))
			writePlanningFixture(t, filepath.Join(root, "bun.lock"), "{}")
			writePlanningFixture(t, filepath.Join(root, "package-lock.json"), "{}")
			result, err := (Detector{}).DetectPath(context.Background(), root,
				SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Candidates) != 1 {
				t.Fatalf("candidates = %#v", result.Candidates)
			}
			candidate := result.Candidates[0]
			if !slices.Equal(candidate.PackageManagers, []string{"bun", "npm"}) || candidate.PackageManager != test.manager {
				t.Fatalf("package managers = %v selected %q, want [bun npm] selected %q",
					candidate.PackageManagers, candidate.PackageManager, test.manager)
			}
			if candidate.BuildCommand != test.build {
				t.Fatalf("build command = %q, want %q", candidate.BuildCommand, test.build)
			}
			decision := strings.Contains(strings.Join(candidate.NeedsDecision, " "), "competing lockfiles bun.lock, package-lock.json")
			if decision != test.decision {
				t.Fatalf("needs decision = %v, want competing-lockfile decision %v", candidate.NeedsDecision, test.decision)
			}
			// Drafts store and revalidate detection, so the new fields must
			// survive the saved-draft bounds check.
			source := DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/o/r.git", Ref: "main"}
			result.Source.Remote, result.Source.Repository, err = remoteForSource(source)
			if err != nil {
				t.Fatal(err)
			}
			result.Source.Ref = canonicalSourceConfig(source).Ref
			if err := validateDetectionResult(&source, result); err != nil {
				t.Fatalf("detection does not validate: %v", err)
			}
		})
	}
}

func TestPackageManagerIsOnlyValidForJavaScriptRecipes(t *testing.T) {
	configuration := PlanConfiguration{
		Build:   BuildPlanConfig{Method: BuildRecipe, Recipe: "go", PackageManager: "bun", Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{}},
		Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst, Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{}},
		Domains: []PlannedDomain{}, Checks: []PlannedCheck{}, Variables: []PlannedVariable{}, Dependencies: []PlannedDependency{},
	}
	if err := configuration.Validate(); err == nil || !strings.Contains(err.Error(), "package manager") {
		t.Fatalf("Go recipe with a package manager = %v", err)
	}
	configuration.Build.Recipe, configuration.Build.PackageManager = "node", "deno"
	if err := configuration.Validate(); err == nil || !strings.Contains(err.Error(), "package manager") {
		t.Fatalf("unknown package manager = %v", err)
	}
	configuration.Build.PackageManager = "bun"
	if err := configuration.Validate(); err != nil && strings.Contains(err.Error(), "package manager") {
		t.Fatalf("JavaScript recipe rejected its package manager: %v", err)
	}
}

func TestPreflightBlocksUnresolvedPackageManager(t *testing.T) {
	candidate := DetectedCandidate{
		ID: "root", Name: "barbershop", Profile: ProfileWeb, BuildMethod: BuildRecipe, Recipe: "node",
		Confidence: ConfidenceLow, PackageManagers: []string{"bun", "npm"},
		Evidence: []DetectionEvidence{}, NeedsDecision: []string{},
	}
	draft := &Draft{Data: DraftData{
		Intent: &DraftIntentConfig{Name: "barbershop", Profile: ProfileWeb},
		Source: &DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/o/r.git", Ref: "main"},
		Detection: &DetectionResult{
			Source:     SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)},
			Candidates: []DetectedCandidate{candidate}, SelectedID: "root",
		},
	}}
	configuration := PlanConfiguration{
		Build:   BuildPlanConfig{Method: BuildRecipe, Recipe: "node", Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{}},
		Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst, Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{}},
		Domains: []PlannedDomain{}, Variables: []PlannedVariable{}, Dependencies: []PlannedDependency{},
		Checks: []PlannedCheck{{
			Name: "readiness", Kind: string(CheckHTTP), Phase: "readiness", Required: true,
			Config: json.RawMessage(`{"path":"/"}`),
		}},
	}
	observation := HostObservation{Facilities: map[string]FacilityObservation{"docker": {Available: true}}}
	has := func(findings []PreflightFinding, code string) bool {
		return slices.ContainsFunc(findings, func(item PreflightFinding) bool {
			return item.Code == code && item.Severity == PreflightBlocked
		})
	}

	if findings := preflightFindings(draft, configuration, observation, false); !has(findings, "package_manager_ambiguous") {
		t.Fatalf("unresolved lockfiles were not blocked: %#v", findings)
	}
	configuration.Build.PackageManager = "bun"
	if findings := preflightFindings(draft, configuration, observation, false); has(findings, "package_manager_ambiguous") || has(findings, "package_manager_lockfile_missing") {
		t.Fatalf("an explicit package manager was still blocked: %#v", findings)
	}
	configuration.Build.PackageManager = "pnpm"
	if findings := preflightFindings(draft, configuration, observation, false); !has(findings, "package_manager_lockfile_missing") {
		t.Fatalf("a manager without a lockfile was not blocked: %#v", findings)
	}
}
