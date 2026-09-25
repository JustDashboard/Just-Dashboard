package deploy

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// What the framework's configuration made the recipe do is said before
// Deploy, against the plan as it now stands: a substituted adapter while the
// plan still serves the server, an image loader only while the plan exports,
// a start command that runs a development server whichever way it was written.
func TestPreflightJudgesFrameworkFactsAgainstThePlan(t *testing.T) {
	t.Parallel()
	result, candidate := detectNodeTree(t, withLockfile(map[string]string{
		"package.json":     `{"type":"module","scripts":{"build":"vite build","dev":"vite dev"},"devDependencies":{"@sveltejs/kit":"^2.20.0","@sveltejs/adapter-auto":"^6.0.0","vite":"^6.0.0"}}`,
		"svelte.config.js": sveltekitAuto,
	}))
	plan := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: candidate.BuildCommand, StartCommand: candidate.StartCommand}
	findings := preflightFindings(nodeDraft(result), nodeTestConfiguration(plan), dockerHost, false)
	substituted := findingByCode(findings, "sveltekit_adapter_substituted")
	if substituted == nil || substituted.Severity != PreflightWarning || !strings.Contains(substituted.Action, "@sveltejs/adapter-node") {
		t.Fatalf("substituted = %+v", substituted)
	}
	if item := findingByCode(findings, "start_command_dev_server"); item != nil {
		t.Fatalf("the detected start is not a dev server: %+v", item)
	}

	plan.StartCommand = "npm run dev"
	findings = preflightFindings(nodeDraft(result), nodeTestConfiguration(plan), dockerHost, false)
	dev := findingByCode(findings, "start_command_dev_server")
	if dev == nil || dev.Severity != PreflightWarning || dev.Measured != "vite: npm run dev" || dev.FieldID != "configuration.build.startCommand" {
		t.Fatalf("dev server = %+v", dev)
	}
	plan.StartCommand = "npx prisma migrate deploy && npx vite --host 0.0.0.0"
	if dev := findingByCode(preflightFindings(nodeDraft(result), nodeTestConfiguration(plan), dockerHost, false), "start_command_dev_server"); dev == nil ||
		!strings.HasPrefix(dev.Measured, "vite: ") {
		t.Fatalf("direct dev server = %+v", dev)
	}

	result, candidate = detectNodeTree(t, withLockfile(map[string]string{
		"package.json":    `{"scripts":{"build":"next build"},"dependencies":{"next":"16.0.0"}}`,
		"next.config.mjs": "export default { output: 'export' }",
		"app/page.tsx":    "import Image from \"next/image\"\nexport default function Page() { return null }",
	}))
	exported := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: candidate.BuildCommand, OutputDirectory: candidate.OutputDirectory}
	if item := findingByCode(preflightFindings(nodeDraft(result), nodeTestConfiguration(exported), dockerHost, false), "next_export_images"); item == nil ||
		!strings.Contains(item.Measured, "app/page.tsx") {
		t.Fatalf("next_export_images = %+v", item)
	}
	served := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: candidate.BuildCommand, StartCommand: "npx next start"}
	if item := findingByCode(preflightFindings(nodeDraft(result), nodeTestConfiguration(served), dockerHost, false), "next_export_images"); item != nil {
		t.Fatalf("a served plan was warned about the export: %+v", item)
	}

	result, candidate = detectNodeTree(t, withLockfile(map[string]string{
		"package.json":      `{"scripts":{"build":"nest build","start:prod":"node dist/main"},"dependencies":{"@nestjs/core":"^11.0.0"}}`,
		"drizzle.config.ts": "export default {}",
		"src/main.ts":       "bootstrap()",
	}))
	nest := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: candidate.BuildCommand, StartCommand: candidate.StartCommand}
	if item := findingByCode(preflightFindings(nodeDraft(result), nodeTestConfiguration(nest), dockerHost, false), "nest_output_layout"); item == nil ||
		!strings.Contains(item.Action, `"include": ["src"]`) {
		t.Fatalf("nest_output_layout = %+v", item)
	}
	nest.StartCommand = "node dist/src/main.js"
	if item := findingByCode(preflightFindings(nodeDraft(result), nodeTestConfiguration(nest), dockerHost, false), "nest_output_layout"); item != nil {
		t.Fatalf("the operator's own start command was judged by start:prod: %+v", item)
	}
}

// A question detection left open about the framework is asked before every
// Deploy, on the setting that answers it, until that setting no longer
// holds detection's guess; the questions one setting answers are one
// finding.
func TestPreflightAsksTheFrameworksOpenQuestions(t *testing.T) {
	t.Parallel()
	result, candidate := detectNodeTree(t, withLockfile(map[string]string{
		"package.json":   `{"scripts":{"build":"next build"},"dependencies":{"next":"16.0.0"}}`,
		"next.config.js": "module.exports = { output: process.env.STATIC ? 'export' : undefined }",
	}))
	plan := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: candidate.BuildCommand, StartCommand: candidate.StartCommand}
	item := findingByCode(preflightFindings(nodeDraft(result), nodeTestConfiguration(plan), dockerHost, false), "node_decision_open")
	if item == nil || item.Severity != PreflightWarning || item.FieldID != "configuration.build.outputDirectory" || !strings.Contains(item.Measured, "sets output from an expression") {
		t.Fatalf("node_decision_open = %+v", item)
	}
	plan.StartCommand, plan.OutputDirectory = "", "out"
	if item := findingByCode(preflightFindings(nodeDraft(result), nodeTestConfiguration(plan), dockerHost, false), "node_decision_open"); item != nil {
		t.Fatalf("an answered question was asked again: %+v", item)
	}

	result, candidate = detectNodeTree(t, withLockfile(map[string]string{
		"package.json": `{"scripts":{"dev":"tsx watch src/index.ts"},"dependencies":{"hono":"^4.7.0"},"devDependencies":{"tsx":"^4.19.0"}}`,
		"src/index.ts": "import { Hono } from 'hono'\nconst app = new Hono()\nexport default app\n",
	}))
	plan = BuildPlanConfig{Method: BuildRecipe, Recipe: "node", PackageManager: "npm", BuildCommand: candidate.BuildCommand, StartCommand: candidate.StartCommand}
	item = findingByCode(preflightFindings(nodeDraft(result), nodeTestConfiguration(plan), dockerHost, false), "node_decision_open")
	if item == nil || item.FieldID != "configuration.build.packageManager" || !strings.Contains(item.Measured, "only Bun serves") {
		t.Fatalf("bun-only entry = %+v", item)
	}
	plan.PackageManager, plan.StartCommand = "bun", "bun src/index.ts"
	if item := findingByCode(preflightFindings(nodeDraft(result), nodeTestConfiguration(plan), dockerHost, false), "node_decision_open"); item != nil {
		t.Fatalf("choosing Bun still asked: %+v", item)
	}

	merged := nodeFrameworkFindings(&DetectedCandidate{StartCommand: "node src/index.ts", NodeBuild: &DetectedNodeBuild{
		Findings: nodeDecisionFindings([]string{"confirm the start command of the express server", "src/index.ts is TypeScript; install tsx and start that"}),
	}}, nodeTestConfiguration(BuildPlanConfig{Method: BuildRecipe, Recipe: "node", StartCommand: "node src/index.ts"}))
	if len(merged) != 1 || merged[0].Measured != "confirm the start command of the express server; src/index.ts is TypeScript; install tsx and start that" {
		t.Fatalf("questions on one setting = %+v", merged)
	}
}

// The Node major chosen in Build settings replaces what detection read from
// the repository, and the recipe builds on it.
func TestNodeVersionSettingOutranksTheRepository(t *testing.T) {
	t.Parallel()
	result, candidate := detectNodeTree(t, withLockfile(map[string]string{
		"package.json": `{"scripts":{"start":"node server.js"},"dependencies":{"express":"^4.21.0"}}`,
		".nvmrc":       "22\n",
	}))
	if candidate.NodeVersion != "22 (.nvmrc)" {
		t.Fatalf("detected version = %q", candidate.NodeVersion)
	}
	plan := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", StartCommand: candidate.StartCommand, NodeVersion: "20"}
	findings := preflightFindings(nodeDraft(result), nodeTestConfiguration(plan), dockerHost, false)
	selected := slices.IndexFunc(findings, func(item PreflightFinding) bool { return item.Code == "node_version_selected" })
	if selected < 0 || findings[selected].Measured != "20 (Build settings)" || findings[selected].FieldID != "configuration.build.nodeVersion" {
		t.Fatalf("node_version_selected = %+v", findings)
	}
	if strings.Count(codesOf(findings), "node_version_selected") != 1 {
		t.Fatalf("node_version_selected reported twice: %s", codesOf(findings))
	}
	if eol := findingByCode(findings, "node_version_eol"); eol == nil || eol.Severity != PreflightWarning {
		t.Fatalf("node_version_eol = %+v", eol)
	}

	configuration := nodeTestConfiguration(plan)
	if err := configuration.Validate(); err != nil {
		t.Fatalf("valid Node version refused: %v", err)
	}
	for _, invalid := range []BuildPlanConfig{
		{Method: BuildRecipe, Recipe: "node", NodeVersion: "18"},
		{Method: BuildRecipe, Recipe: "node", NodeVersion: "22.1"},
		{Method: BuildRecipe, Recipe: "java", NodeVersion: "22"},
		{Method: BuildDockerfile, NodeVersion: "22"},
	} {
		if err := nodeTestConfiguration(invalid).Validate(); err == nil {
			t.Fatalf("Node version %+v was accepted", invalid)
		}
	}
	// Every recipe that installs assets through the Node install takes the
	// same choice as the package manager.
	for _, recipe := range nodeInstallRecipes {
		if err := nodeTestConfiguration(BuildPlanConfig{Method: BuildRecipe, Recipe: recipe, NodeVersion: "22"}).Validate(); err != nil {
			t.Fatalf("Node version refused for the %s recipe: %v", recipe, err)
		}
	}

	// Yarn 1 checks engines.node against the Node it runs on, so a chosen
	// release the package excludes is refused before the build.
	root := writeNodeTree(t, map[string]string{
		"package.json": `{"engines":{"node":">=22"},"scripts":{"start":"node server.js"},"dependencies":{"express":"^4.21.0"}}`,
		"yarn.lock":    "# yarn lockfile v1\n",
	})
	_, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root,
		BuildPlanConfig{Method: BuildRecipe, Recipe: "node", StartCommand: "yarn run start", NodeVersion: "20"}, false, "t:1")
	if !errors.Is(err, ErrUnsupportedBuilder) || recipeRefusalField(recipeRefusalText(err, root)) != "configuration.build.nodeVersion" {
		t.Fatalf("refusal = %v (field %s)", err, recipeRefusalField(recipeRefusalText(err, root)))
	}
}

func codesOf(findings []PreflightFinding) string {
	codes := []string{}
	for _, item := range findings {
		codes = append(codes, item.Code)
	}
	return strings.Join(codes, " ")
}

// A saved command that calls another language's toolchain is refused before
// Deploy on the field that runs it, not after the install.
func TestPreflightRefusesAForeignRunnerInASavedCommand(t *testing.T) {
	t.Parallel()
	result, candidate := detectNodeTree(t, withLockfile(map[string]string{
		"package.json": `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"^6.0.0"}}`,
		"index.html":   "<div id=app></div>",
	}))
	plan := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: candidate.BuildCommand + " && php artisan optimize", OutputDirectory: "dist"}
	item := findingByCode(preflightFindings(nodeDraft(result), nodeTestConfiguration(plan), dockerHost, false), "command_runner_missing")
	if item == nil || item.Severity != PreflightBlocked || item.Measured != "php" || item.FieldID != "configuration.build.buildCommand" {
		t.Fatalf("command_runner_missing = %+v", item)
	}
}
