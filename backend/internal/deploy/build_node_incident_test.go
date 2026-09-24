package deploy

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// The incident this test keeps fixed: an imported Next.js 16 + Prisma 7
// repository committed a bun.lock that matches package.json (with
// trustedDependencies and bunx in its scripts) next to the package-lock.json
// create-next-app wrote before fifteen dependencies were added. Detection
// only said "choose the package manager"; the operator chose npm, the recipe
// ran `npm ci`, and the build died with EUSAGE as internal_error "exit
// status 1".
const incidentManifest = `{
  "name": "barbershop",
  "version": "0.1.0",
  "private": true,
  "scripts": {
    "dev": "next dev --turbopack",
    "build": "bunx prisma generate && next build",
    "start": "next start",
    "db:push": "bunx prisma db push"
  },
  "dependencies": {
    "@prisma/adapter-pg": "^7.0.0",
    "@prisma/client": "^7.0.0",
    "@radix-ui/react-dialog": "^1.1.14",
    "@tanstack/react-query": "^5.85.0",
    "bcryptjs": "^3.0.2",
    "clsx": "^2.1.1",
    "date-fns": "^4.1.0",
    "lucide-react": "^0.540.0",
    "next": "16.1.3",
    "next-auth": "5.0.0-beta.29",
    "pg": "^8.16.3",
    "react": "19.2.0",
    "react-dom": "19.2.0",
    "react-hook-form": "^7.62.0",
    "sonner": "^2.0.7",
    "tailwind-merge": "^3.3.1",
    "zod": "^4.1.0"
  },
  "devDependencies": {
    "@tailwindcss/postcss": "^4",
    "@types/node": "^20",
    "@types/react": "^19",
    "@types/react-dom": "^19",
    "eslint": "^9",
    "eslint-config-next": "16.1.3",
    "prisma": "^7.0.0",
    "tailwindcss": "^4",
    "typescript": "^5"
  },
  "trustedDependencies": ["@prisma/client", "@prisma/engines", "prisma"]
}`

// incidentBunLock is bun.lock as Bun 1.2 writes it: every range package.json
// asks for, trailing commas and all.
func incidentBunLock(t *testing.T) string {
	t.Helper()
	var manifest struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal([]byte(incidentManifest), &manifest); err != nil {
		t.Fatal(err)
	}
	var lock strings.Builder
	lock.WriteString("{\n  \"lockfileVersion\": 1,\n  \"workspaces\": {\n    \"\": {\n      \"name\": \"barbershop\",\n")
	for _, kind := range []struct {
		key   string
		specs map[string]string
	}{{"dependencies", manifest.Dependencies}, {"devDependencies", manifest.DevDependencies}} {
		lock.WriteString("      \"" + kind.key + "\": {\n")
		names := make([]string, 0, len(kind.specs))
		for name := range kind.specs {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			lock.WriteString("        \"" + name + "\": \"" + kind.specs[name] + "\",\n")
		}
		lock.WriteString("      },\n")
	}
	lock.WriteString("    },\n  },\n  \"trustedDependencies\": [\n    \"@prisma/engines\",\n  ],\n  \"packages\": {\n")
	lock.WriteString("    \"next\": [\"next@16.1.3\", \"\", {}, \"sha512-x\"],\n    \"prisma\": [\"prisma@7.0.1\", \"\", {}, \"sha512-y\"],\n  }\n}\n")
	return lock.String()
}

// incidentPackageLock is create-next-app's lock, before the project grew:
// it knows next, react and the tooling, and none of the fifteen packages
// added since.
const incidentPackageLock = `{
  "name": "barbershop",
  "version": "0.1.0",
  "lockfileVersion": 3,
  "requires": true,
  "packages": {
    "": {
      "name": "barbershop",
      "version": "0.1.0",
      "dependencies": {"next": "16.1.3", "react": "19.2.0", "react-dom": "19.2.0"},
      "devDependencies": {
        "@tailwindcss/postcss": "^4", "@types/node": "^20", "@types/react": "^19", "@types/react-dom": "^19",
        "eslint": "^9", "eslint-config-next": "16.1.3", "tailwindcss": "^4", "typescript": "^5"
      }
    },
    "node_modules/next": {"version": "16.1.3"},
    "node_modules/react": {"version": "19.2.0"},
    "node_modules/react-dom": {"version": "19.2.0"}
  }
}`

func incidentTree(t *testing.T) map[string]string {
	return map[string]string{
		"package.json":         incidentManifest,
		"bun.lock":             incidentBunLock(t),
		"package-lock.json":    incidentPackageLock,
		"prisma/schema.prisma": "datasource db {\n  provider = \"postgresql\"\n}\n",
		"app/page.tsx":         "export default function Page() { return null }\n",
	}
}

func TestIncidentRepositoryResolvesToBunAndWarnsWhenNPMIsForced(t *testing.T) {
	t.Parallel()
	root := writeNodeTree(t, incidentTree(t))

	// Detection settles the manager from the lockfiles alone: bun.lock
	// matches, package-lock.json provably does not. No decision is asked.
	result, err := (Detector{}).DetectPath(context.Background(), root,
		SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Candidates[0]
	if candidate.PackageManager != "bun" || candidate.Framework != "nextjs" || candidate.Confidence != ConfidenceHigh ||
		slices.ContainsFunc(candidate.NeedsDecision, func(decision string) bool { return strings.Contains(decision, "package manager") }) {
		t.Fatalf("candidate = manager %q framework %q confidence %q decisions %v", candidate.PackageManager, candidate.Framework, candidate.Confidence, candidate.NeedsDecision)
	}
	if candidate.BuildCommand != "bun run build" || candidate.StartCommand != "bunx prisma db push && bun run start" {
		t.Fatalf("commands = %q / %q", candidate.BuildCommand, candidate.StartCommand)
	}
	byPath := map[string]DetectedLockfile{}
	for _, lockfile := range candidate.Lockfiles {
		byPath[lockfile.Path] = lockfile
	}
	npmLock := byPath["package-lock.json"]
	if byPath["bun.lock"].State != LockfileInSync || npmLock.State != LockfileStale ||
		!strings.Contains(npmLock.Note, "is missing 15 dependencies (@prisma/adapter-pg, @prisma/client, @radix-ui/react-dialog and 12 more)") {
		t.Fatalf("lockfiles = %+v", candidate.Lockfiles)
	}
	if !slices.ContainsFunc(candidate.Evidence, func(evidence DetectionEvidence) bool {
		return strings.Contains(evidence.Reason, "bun.lock matches package.json; package-lock.json is missing 15 dependencies")
	}) {
		t.Fatalf("evidence = %+v", candidate.Evidence)
	}

	// Preflight, lockfile mode: a pass that names why, and nothing to decide.
	draft := &Draft{Data: DraftData{
		Intent: &DraftIntentConfig{Name: "barbershop", Profile: ProfileWeb},
		Source: &DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/o/r.git", Ref: "main"},
		Detection: &DetectionResult{
			Source: SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)}, Candidates: []DetectedCandidate{candidate}, SelectedID: candidate.ID,
		},
	}}
	configuration := nodeTestConfiguration(BuildPlanConfig{
		Method: BuildRecipe, Recipe: "node", BuildCommand: candidate.BuildCommand, StartCommand: candidate.StartCommand,
	})
	findings := preflightFindings(draft, configuration, HostObservation{Facilities: map[string]FacilityObservation{"docker": {Available: true}}}, false)
	resolved := findingByCode(findings, "package_manager_resolved")
	if resolved == nil || resolved.Severity != PreflightPass || resolved.Action != "Delete package-lock.json so the repository names one manager." {
		t.Fatalf("resolved finding = %+v", resolved)
	}
	if blocking := findingsAtLeast(findings, PreflightWarning); len(blocking) != 0 {
		t.Fatalf("lockfile mode raised %+v", blocking)
	}
	commands := findingByCode(findings, "build_commands")
	if commands == nil || !strings.HasPrefix(commands.Measured, "bun install --frozen-lockfile · bun run build · start: bunx prisma") {
		t.Fatalf("build commands = %+v", commands)
	}

	// The operator forces npm, as in the incident: preflight warns before
	// Deploy, names the drift and offers the lockfile that matches.
	configuration.Build.PackageManager = "npm"
	configuration.Build.BuildCommand, configuration.Build.StartCommand = "npm run build", "npx prisma migrate deploy && npm run start"
	findings = preflightFindings(draft, configuration, HostObservation{Facilities: map[string]FacilityObservation{"docker": {Available: true}}}, false)
	outOfSync := findingByCode(findings, "lockfile_out_of_sync")
	if outOfSync == nil || outOfSync.Severity != PreflightWarning || outOfSync.FieldID != "configuration.build.packageManager" ||
		!strings.Contains(outOfSync.Measured, "package-lock.json is missing 15 dependencies (@prisma/adapter-pg, @prisma/client, @radix-ui/react-dialog and 12 more)") ||
		!strings.Contains(outOfSync.Means, "npm install --no-audit --no-fund") ||
		outOfSync.Action != "Choose Bun, whose bun.lock matches package.json, or commit an updated package-lock.json." {
		t.Fatalf("out of sync = %+v", outOfSync)
	}
	if added := findingByCode(findings, "script_runtime_added"); added == nil {
		t.Fatalf("bunx in the build script was not provided for: %+v", findings)
	}

	// The recipe agrees with both: automatic builds with Bun frozen; forced
	// npm installs non-frozen, says so, and still finds bunx.
	builder := NewArtifactBuilder(&artifactBackendFake{})
	automatic, err := builder.Prepare(context.Background(), root, BuildPlanConfig{
		Method: BuildRecipe, Recipe: "node", BuildCommand: candidate.BuildCommand, StartCommand: candidate.StartCommand,
	}, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FROM node:22-alpine@sha256:", "COPY --from=oven/bun:1-alpine@sha256:", "RUN bun install --frozen-lockfile", "RUN bun run build"} {
		if !strings.Contains(automatic.DockerfilePreview, want) {
			t.Fatalf("automatic Dockerfile missing %q:\n%s", want, automatic.DockerfilePreview)
		}
	}
	forced, err := builder.Prepare(context.Background(), root, configuration.Build, false, "t:2")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"RUN npm install --no-audit --no-fund", "COPY --from=oven/bun:1-alpine@sha256:", "RUN npm run build"} {
		if !strings.Contains(forced.DockerfilePreview, want) {
			t.Fatalf("forced npm Dockerfile missing %q:\n%s", want, forced.DockerfilePreview)
		}
	}
	if strings.Contains(forced.DockerfilePreview, "npm ci") || forced.Install != "npm install --no-audit --no-fund" ||
		!slices.ContainsFunc(forced.Notes, func(note string) bool {
			return strings.HasPrefix(note, "Installing with npm install --no-audit --no-fund: package-lock.json is missing 15 dependencies")
		}) {
		t.Fatalf("forced npm prepared = install %q notes %v", forced.Install, forced.Notes)
	}
}

func nodeTestConfiguration(build BuildPlanConfig) PlanConfiguration {
	if build.Secrets == nil {
		build.Secrets = []BuildSecretConfig{}
	}
	if build.ReleaseTasks == nil {
		build.ReleaseTasks = []ReleaseTaskConfig{}
	}
	return PlanConfiguration{
		Build:   build,
		Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst, Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{}},
		Domains: []PlannedDomain{}, Variables: []PlannedVariable{}, Dependencies: []PlannedDependency{},
		Checks: []PlannedCheck{{
			Name: "readiness", Kind: string(CheckHTTP), Phase: "readiness", Required: true,
			Config: json.RawMessage(`{"path":"/"}`),
		}},
	}
}

func findingByCode(findings []PreflightFinding, code string) *PreflightFinding {
	for index := range findings {
		if findings[index].Code == code {
			return &findings[index]
		}
	}
	return nil
}

// findingsAtLeast lists the Node install findings an operator must act on
// or acknowledge; the rest of preflight is outside these tests.
func findingsAtLeast(findings []PreflightFinding, severity PreflightSeverity) []PreflightFinding {
	nodeCodes := map[string]bool{
		"package_manager_ambiguous": true, "package_manager_lockfile_missing": true, "lockfile_out_of_sync": true,
		"dependencies_unpinned": true, "runner_mismatch": true, "command_runner_missing": true, "registry_token_missing": true,
		"install_in_build_command": true, "package_manager_declaration_conflict": true, "package_manager_lockfile_incompatible": true,
		"pnpm_build_policy_ignored": true, "install_scripts_blocked": true, "yarn_version_inferred": true,
		"registry_token_committed": true, "registry_host_private": true,
	}
	result := []PreflightFinding{}
	for _, item := range findings {
		if nodeCodes[item.Code] && (item.Severity == severity || item.Severity == PreflightBlocked) {
			result = append(result, item)
		}
	}
	return result
}
