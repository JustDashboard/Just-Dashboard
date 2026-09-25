package deploy

import (
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
