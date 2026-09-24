package deploy

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The JavaScript install planner (build_node_*.go) and the per-root passes
// that run after packageCandidate — state, readiness, network, variables,
// ranking — describe one candidate together. These tests hold the places
// where one reads or rewrites what the other wrote.

// incidentWithStateTree is the incident repository on SQLite through Prisma
// 7's prisma.config, with a health route, Auth.js and a documented .env.
func incidentWithStateTree(t *testing.T) map[string]string {
	files := incidentTree(t)
	files["prisma/schema.prisma"] = "generator client {\n  provider = \"prisma-client\"\n  output = \"../src/generated/prisma\"\n}\ndatasource db {\n  provider = \"sqlite\"\n}\n"
	files["prisma.config.ts"] = "import { defineConfig, env } from 'prisma/config'\nexport default defineConfig({ schema: 'prisma/schema.prisma', datasource: { url: env('DATABASE_URL') } })\n"
	files[".env.example"] = "DATABASE_URL=\"file:./dev.db\"\nAUTH_SECRET=\"\"\nNEXTAUTH_URL=\"http://localhost:3000\"\n"
	files["app/api/health/route.ts"] = "export function GET() { return Response.json({ ok: true }) }\n"
	files["auth.ts"] = "import NextAuth from 'next-auth'\nexport const { handlers } = NextAuth({ secret: process.env.AUTH_SECRET })\n"
	return files
}

func TestIncidentRepositoryResolvesToBunWithItsReadinessVariablesAndVolume(t *testing.T) {
	t.Parallel()
	root := writeNodeTree(t, incidentWithStateTree(t))
	result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	candidate := selectedDetectionCandidate(&result)
	if candidate == nil || candidate.PackageManager != "bun" || candidate.Framework != "nextjs" || candidate.Confidence != ConfidenceHigh ||
		len(candidate.NeedsDecision) != 0 {
		t.Fatalf("candidate = %+v", candidate)
	}
	// Readiness reads the route handler and budgets the schema step the
	// state pass chained into the Bun start command.
	if candidate.Readiness == nil || candidate.Readiness.Path != "/api/health" || candidate.Readiness.SlowStart == "" ||
		!strings.HasPrefix(candidate.StartCommand, "bunx prisma ") {
		t.Fatalf("readiness %+v for start %q", candidate.Readiness, candidate.StartCommand)
	}
	sqlite := slices.IndexFunc(candidate.PersistentPaths, func(path DetectedPersistentPath) bool {
		return path.Kind == "sqlite" && path.Target == "/data" && path.Variable == "DATABASE_URL" && path.Value == "file:/data/dev.db"
	})
	if sqlite < 0 {
		t.Fatalf("persistent paths = %+v", candidate.PersistentPaths)
	}
	setup := map[string]string{}
	for _, variable := range candidate.Variables {
		setup[variable.Name] = variable.Setup
	}
	if setup["AUTH_SECRET"] != "generate" || setup["NEXTAUTH_URL"] != "domain" || setup["AUTH_TRUST_HOST"] != "default" {
		t.Fatalf("variables = %+v", candidate.Variables)
	}
	if !slices.ContainsFunc(candidate.NetworkVariables, func(variable DetectedNetworkVariable) bool { return variable.Name == "AUTH_TRUST_HOST" }) {
		t.Fatalf("network variables = %+v", candidate.NetworkVariables)
	}
	// Each manager's commands carry the chained schema step on its own runner.
	for manager, prefix := range map[string]string{"bun": "bunx prisma ", "npm": "npx prisma ", "pnpm": "pnpm exec prisma "} {
		install := slices.IndexFunc(candidate.NodeInstalls, func(install DetectedNodeInstall) bool { return install.Manager == manager })
		if install < 0 || !strings.HasPrefix(candidate.NodeInstalls[install].StartCommand, prefix) {
			t.Fatalf("%s install = %+v", manager, candidate.NodeInstalls)
		}
	}
	if candidate.NodeBuild == nil || !slices.Equal(candidate.NodeBuild.PrismaEnv, []string{"DATABASE_URL"}) {
		t.Fatalf("node build = %+v", candidate.NodeBuild)
	}

	// prisma.config's env('DATABASE_URL') is a build-time read, but the
	// recipe gives it a placeholder while prisma generate runs, so the
	// environment check does not demand the database URL in the build.
	configuration := nodeTestConfiguration(BuildPlanConfig{Method: BuildRecipe, Recipe: "node", PackageManager: "",
		BuildCommand: candidate.BuildCommand, StartCommand: candidate.StartCommand})
	configuration.Runtime.Mounts = []RuntimeMount{{Source: "shop-data", Target: "/data"}}
	configuration.Variables = []PlannedVariable{{Name: "DATABASE_URL", Sensitivity: "plain", Scopes: []string{"runtime"}, Value: "file:/data/dev.db"}}
	findings := preflightFindings(nodeDraft(result), configuration, dockerHost, false)
	if item := findingByCode(findings, "build_variable_missing_database_url"); item != nil {
		t.Fatalf("the Prisma placeholder's variable was refused: %+v", item)
	}
	if item := findingByCode(findings, "package_manager_resolved"); item == nil || item.Severity != PreflightPass {
		t.Fatalf("package_manager_resolved = %+v", item)
	}
	if item := findingByCode(findings, "persistent_state_kept"); item == nil {
		t.Fatalf("the SQLite volume was not recognised: %+v", findings)
	}
	// The same read still needs a value in a build that migrates, which
	// connects.
	migrating := configuration
	migrating.Build.BuildCommand = "bunx prisma migrate deploy && bun run build"
	if record := prismaBuildPlaceholders(readNodeInstallFactsAt(t, root), migrating.Build.BuildCommand); record != nil {
		t.Fatalf("a migrating build was given a placeholder: %v", record)
	}

	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root, configuration.Build, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	assertDockerfile(t, prepared.DockerfilePreview, []string{
		"bun install --frozen-lockfile",
		`DATABASE_URL="${DATABASE_URL:-file:./prisma-generate.db}"`,
		"ENV HOST=0.0.0.0\nCOPY --from=build /app /app",
	}, []string{"npm ci"})
}

func readNodeInstallFactsAt(t *testing.T, root string) nodeInstallFacts {
	t.Helper()
	source, err := readNodeInstallSource(root, "", "x64", newNodeReadBudget())
	if err != nil {
		t.Fatal(err)
	}
	return source.facts
}

// A pnpm server runs from the toolchain stage Corepack installed pnpm into;
// the runtime stage built on it still carries the framework's proxy trust
// and the HOST every Node server binds, and the runtime can still withdraw
// that trust.
func TestToolchainStageRuntimeKeepsProxyTrust(t *testing.T) {
	t.Parallel()
	root := writeNodeTree(t, map[string]string{
		"package.json":     `{"name":"kit","type":"module","scripts":{"build":"vite build"},"devDependencies":{"@sveltejs/kit":"2.20.0","@sveltejs/adapter-node":"5.2.0","vite":"6.3.0"}}`,
		"svelte.config.js": "import adapter from '@sveltejs/adapter-node'\nexport default { kit: { adapter: adapter() } }\n",
		"pnpm-lock.yaml":   "lockfileVersion: '9.0'\n\nimporters:\n\n  .:\n    devDependencies:\n      '@sveltejs/adapter-node':\n        specifier: 5.2.0\n        version: 5.2.0\n      '@sveltejs/kit':\n        specifier: 2.20.0\n        version: 2.20.0\n      vite:\n        specifier: 6.3.0\n        version: 6.3.0\n",
	})
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root,
		BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "pnpm run build", StartCommand: "pnpm exec node build", Secrets: []BuildSecretConfig{}}, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := prepared.DockerfilePreview
	runtime := dockerfile[strings.LastIndex(dockerfile, "\nFROM "):]
	if !strings.HasPrefix(runtime, "\nFROM toolchain\n") {
		t.Fatalf("the server does not start from the toolchain stage:\n%s", dockerfile)
	}
	assertDockerfile(t, runtime, []string{
		"ENV COREPACK_ENABLE_NETWORK=0", "ENV HOST=0.0.0.0", "ENV PROTOCOL_HEADER=x-forwarded-proto",
		"ENV HOST_HEADER=x-forwarded-host", "ENV ADDRESS_HEADER=x-forwarded-for", "ENV XFF_DEPTH=1",
	}, nil)
	if got := strings.Join(imageProxyTrust(prepared), " "); got != "ADDRESS_HEADER HOST_HEADER PROTOCOL_HEADER XFF_DEPTH" {
		t.Fatalf("image trust = %q", got)
	}
}

// The network pass binds a preview server to every interface after
// packageCandidate recorded each manager's commands; choosing another
// manager keeps the flag instead of swapping back to the bare script.
func TestManagerChoiceKeepsACommandALaterPassRewrote(t *testing.T) {
	t.Parallel()
	_, candidate := detectNodeTree(t, map[string]string{
		"package.json":      `{"name":"api","scripts":{"build":"tsc","start":"vite preview"},"dependencies":{"express":"4.21.0"}}`,
		"package-lock.json": `{"name":"api","lockfileVersion":3,"packages":{"":{"name":"api","dependencies":{"express":"4.21.0"}},"node_modules/express":{"version":"4.21.0"}}}`,
	})
	if candidate.StartCommand != "npx vite preview --host 0.0.0.0" {
		t.Fatalf("start = %q", candidate.StartCommand)
	}
	for manager, start := range map[string]string{"npm": "npx vite preview --host 0.0.0.0", "bun": "bunx vite preview --host 0.0.0.0", "pnpm": "pnpm exec vite preview --host 0.0.0.0"} {
		index := slices.IndexFunc(candidate.NodeInstalls, func(install DetectedNodeInstall) bool { return install.Manager == manager })
		if index < 0 || candidate.NodeInstalls[index].StartCommand != start {
			t.Fatalf("%s = %+v", manager, candidate.NodeInstalls)
		}
	}
}

// A workspace member's build context is its workspace root, so the ignore
// file is written there and keeps the member's own manifest, which the
// repository's rules would leave out.
func TestWorkspaceMemberIgnoreFileIsWrittenAtTheInstallContext(t *testing.T) {
	t.Parallel()
	boundary := writeNodeTree(t, map[string]string{
		"package.json":          `{"name":"root","private":true}`,
		"pnpm-workspace.yaml":   "packages:\n  - apps/*\n",
		"apps/web/package.json": `{"name":"web","scripts":{"start":"node server.js"},"dependencies":{"express":"4.21.0"}}`,
		"pnpm-lock.yaml":        "lockfileVersion: '9.0'\n\nimporters:\n\n  .: {}\n\n  apps/web:\n    dependencies:\n      express:\n        specifier: 4.21.0\n        version: 4.21.0\n",
		".dockerignore":         "pnpm-*.yaml\napps/*/package.json\ndocs\n",
	})
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).PrepareWithin(context.Background(), boundary, filepath.Join(boundary, "apps", "web"),
		BuildPlanConfig{Method: BuildRecipe, Recipe: "node", RootDirectory: "apps/web", StartCommand: "pnpm run start", Secrets: []BuildSecretConfig{}}, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.ContextDirectory != "." {
		t.Fatalf("context = %q", prepared.ContextDirectory)
	}
	if _, err := os.Stat(filepath.Join(boundary, "apps", "web", ".just-dashboard")); !os.IsNotExist(err) {
		t.Fatalf("generated files were written in the member: %v", err)
	}
	ignore, err := os.ReadFile(filepath.Join(boundary, ".just-dashboard", "Dockerfile.dockerignore"))
	if err != nil {
		t.Fatal(err)
	}
	rules := parseDockerignore(ignore)
	for path, excluded := range map[string]bool{
		"pnpm-lock.yaml": false, "pnpm-workspace.yaml": false, "apps/web/package.json": false, "docs/index.md": true,
	} {
		if got, _ := dockerignoreExcludes(rules, path); got != excluded {
			t.Errorf("%s excluded = %v, want %v:\n%s", path, got, excluded, ignore)
		}
	}
	if !slices.ContainsFunc(prepared.Notes, func(note string) bool { return strings.HasPrefix(note, ".dockerignore excludes apps/web/package.json") }) {
		t.Fatalf("notes = %v", prepared.Notes)
	}
}

// Competing lockfiles nothing settles keep the recipe at low confidence, as
// the install planner requires, and still leave the root application
// selected over a helper image elsewhere: which manager installs is a
// question of building it, not of what the repository is for.
func TestUnsettledManagerStaysLowYetOutranksAHelperElsewhere(t *testing.T) {
	t.Parallel()
	result := detectFixture(t, map[string]string{
		"package.json": nextManifest, "bun.lock": "{}", "package-lock.json": "{}", "app/page.tsx": "",
		"worker/Dockerfile": "FROM node:22\nWORKDIR /app\nCOPY worker.js .\nCMD [\"node\", \"worker.js\"]\n", "worker/worker.js": "",
	})
	selected := selectedFixtureCandidate(t, result)
	if selected.BuildMethod != BuildRecipe || selected.Confidence != ConfidenceLow || selected.PackageManager != "" ||
		!strings.Contains(result.SelectionReason, "shallower root") {
		t.Fatalf("selected %s at %q confidence %s (%s)", selected.BuildMethod, selected.Root, selected.Confidence, result.SelectionReason)
	}
}
