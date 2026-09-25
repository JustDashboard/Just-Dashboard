package deploy

import (
	"slices"
	"strings"
	"testing"
)

// A workspace member builds after the workspace packages it depends on that
// build themselves, through each manager's own workspace command; the root
// that only declares the workspace is offered but never chosen.
func TestWorkspaceMembersBuildTheirWorkspaceDependenciesFirst(t *testing.T) {
	t.Parallel()
	members := func(root map[string]string, spec string) map[string]string {
		files := map[string]string{
			"apps/web/package.json":        `{"name":"web","scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16.0.0","@repo/ui":"` + spec + `","@repo/config":"` + spec + `"}}`,
			"packages/ui/package.json":     `{"name":"@repo/ui","scripts":{"build":"tsup"},"main":"dist/index.js","types":"dist/index.d.ts","exports":{".":"./dist/index.js"},"dependencies":{"@repo/tokens":"` + spec + `"}}`,
			"packages/tokens/package.json": `{"name":"@repo/tokens","scripts":{"build":"node build.js"},"main":"dist/index.js","types":"dist/index.d.ts","exports":{".":"./dist/index.js"}}`,
			"packages/config/package.json": `{"name":"@repo/config","main":"index.js","files":["index.js"],"exports":{".":"./index.js"}}`,
		}
		for name, content := range root {
			files[name] = content
		}
		return files
	}
	for _, test := range []struct {
		name  string
		files map[string]string
		build string
	}{
		{"pnpm filters the member and its dependencies", members(map[string]string{
			"package.json":        `{"name":"mono","private":true,"packageManager":"pnpm@10.12.1","scripts":{"dev":"pnpm -r dev"}}`,
			"pnpm-workspace.yaml": "packages:\n  - apps/*\n  - packages/*\n",
			"pnpm-lock.yaml":      "lockfileVersion: '9.0'\n",
		}, "workspace:*"), "pnpm --filter web... run build"},
		{"npm names each dependency, dependencies first", members(map[string]string{
			"package.json":      `{"name":"mono","private":true,"workspaces":["apps/*","packages/*"]}`,
			"package-lock.json": "{}",
		}, "*"), "npm run build --workspace=@repo/tokens && npm run build --workspace=@repo/ui && npm run build"},
		{"yarn runs each workspace", members(map[string]string{
			"package.json": `{"name":"mono","private":true,"workspaces":["apps/*","packages/*"]}`,
			"yarn.lock":    "# yarn lockfile v1\n",
		}, "*"), "yarn workspace @repo/tokens run build && yarn workspace @repo/ui run build && yarn run build"},
		{"bun filters each dependency", members(map[string]string{
			"package.json": `{"name":"mono","private":true,"workspaces":["apps/*","packages/*"]}`,
			"bun.lock":     `{"lockfileVersion":1,"workspaces":{"":{"name":"mono"}},"packages":{}}`,
		}, "workspace:*"), "bun run --filter @repo/tokens build && bun run --filter @repo/ui build && bun run build"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := detectFixture(t, test.files)
			var web, root *DetectedCandidate
			for index := range result.Candidates {
				switch result.Candidates[index].Root {
				case "apps/web":
					web = &result.Candidates[index]
				case "":
					root = &result.Candidates[index]
				}
			}
			if web == nil || web.BuildCommand != test.build || web.Framework != "nextjs" || result.SelectedID != web.ID {
				t.Fatalf("web = %+v, selected %q (%s)", web, result.SelectedID, result.SelectionReason)
			}
			if root == nil || root.Demotion == "" {
				t.Fatalf("workspace root = %+v", root)
			}
			// Every manager's command is recorded, so choosing another one
			// swaps the whole build.
			if install := slices.IndexFunc(web.NodeInstalls, func(i DetectedNodeInstall) bool { return i.Manager == "pnpm" }); install < 0 ||
				web.NodeInstalls[install].BuildCommand != "pnpm --filter web... run build" {
				t.Fatalf("pnpm install record = %+v", web.NodeInstalls)
			}
		})
	}
}

const nxJSON = `{
  "$schema": "./node_modules/nx/schemas/nx-schema.json",
  // inferred targets
  "plugins": [{"plugin": "@nx/next/plugin", "options": {"buildTargetName": "build"}}, "@nx/vite/plugin"],
  "namedInputs": {"default": ["{projectRoot}/**/*"]},
}`

// An Nx integrated repository's applications live in project.json files
// under apps/, with no package.json of their own: each one whose build Nx
// runs with a known executor, or infers from its framework's configuration,
// is a candidate at the workspace root, and the root itself is not.
func TestNxApplicationProjectsAreTheCandidates(t *testing.T) {
	t.Parallel()
	result := detectFixture(t, map[string]string{
		"package.json":              `{"name":"@org/source","private":true,"dependencies":{"next":"15.2.0","react":"19.0.0","express":"^4.21.0"},"devDependencies":{"nx":"20.4.0","@nx/next":"20.4.0","@nx/esbuild":"20.4.0","@nx/vite":"20.4.0"}}`,
		"package-lock.json":         "{}",
		"nx.json":                   nxJSON,
		"apps/web/project.json":     `{"name":"web","$schema":"../../node_modules/nx/schemas/project-schema.json","sourceRoot":"apps/web","projectType":"application","tags":[],"targets":{}}`,
		"apps/web/next.config.js":   "module.exports = { nx: {} }",
		"apps/admin/project.json":   `{"name":"admin","projectType":"application","targets":{}}`,
		"apps/admin/vite.config.ts": "export default defineConfig({ build: { outDir: '../../dist/apps/admin' } })",
		"apps/api/project.json":     `{"name":"api","projectType":"application","targets":{"build":{"executor":"@nx/esbuild:esbuild","options":{"outputPath":"dist/apps/api","main":"apps/api/src/main.ts"},"configurations":{"development":{},"production":{}}},"serve":{"executor":"@nx/js:node"}}}`,
		"apps/legacy/project.json":  `{"name":"legacy","projectType":"application","targets":{"build":{"executor":"@acme/custom:build"}}}`,
		"apps/gateway/project.json": `{"name":"gateway","projectType":"application","targets":{"build":{"executor":"@nx/esbuild:esbuild","options":{"outputPath":"dist/apps/gateway","main":"apps/gateway/src/server.ts"}}}}`,
		"apps/shop/project.json":    `{"name":"shop","projectType":"application","targets":{"build":{"executor":"@angular-devkit/build-angular:application","options":{"outputPath":"dist/apps/shop","browser":"apps/shop/src/main.ts","server":"apps/shop/src/main.server.ts","ssr":{"entry":"apps/shop/src/server.ts"}}}}}`,
		"libs/ui/project.json":      `{"name":"ui","projectType":"library"}`,
	})
	byName := map[string]DetectedCandidate{}
	for _, candidate := range result.Candidates {
		if candidate.Root != "" {
			t.Fatalf("candidate outside the workspace root: %+v", candidate)
		}
		byName[candidate.Name] = candidate
	}
	if _, ok := byName["@org/source"]; ok || len(byName) != 6 {
		t.Fatalf("candidates = %+v", result.Candidates)
	}
	for name, want := range map[string]struct{ framework, build, start, output string }{
		"web":    {"nextjs", "NX_DAEMON=false NX_NO_CLOUD=true npx nx run web:build", "npx next start apps/web", ""},
		"admin":  {"vite", "NX_DAEMON=false NX_NO_CLOUD=true npx nx run admin:build", "", "dist/apps/admin"},
		"api":    {"express", "NX_DAEMON=false NX_NO_CLOUD=true npx nx run api:build --configuration=production", "node dist/apps/api/main.js", ""},
		"legacy": {"", "NX_DAEMON=false NX_NO_CLOUD=true npx nx run legacy:build", "", ""},
		// esbuild names the bundle after the main file; an Angular
		// application with ssr writes its server beside the browser files.
		"gateway": {"express", "NX_DAEMON=false NX_NO_CLOUD=true npx nx run gateway:build", "node dist/apps/gateway/server.js", ""},
		"shop":    {"angular", "NX_DAEMON=false NX_NO_CLOUD=true npx nx run shop:build", "node dist/apps/shop/server/server.mjs", ""},
	} {
		got := byName[name]
		if got.Framework != want.framework || got.BuildCommand != want.build || got.StartCommand != want.start || got.OutputDirectory != want.output {
			t.Fatalf("%s = framework %q build %q start %q output %q", name, got.Framework, got.BuildCommand, got.StartCommand, got.OutputDirectory)
		}
	}
	if legacy := byName["legacy"]; !slices.ContainsFunc(legacy.NeedsDecision, func(d string) bool { return strings.Contains(d, "@acme/custom:build") }) {
		t.Fatalf("legacy decisions = %q", legacy.NeedsDecision)
	}
	if api := byName["api"]; api.Profile != ProfileWeb || api.Port != 3000 || api.Confidence != ConfidenceHigh {
		t.Fatalf("api = %+v", api)
	}
	if admin := byName["admin"]; admin.Profile != ProfileStatic || !admin.SPAFallback {
		t.Fatalf("admin = %+v", admin)
	}
	if shop := byName["shop"]; shop.Profile != ProfileWeb || shop.Port != 4000 {
		t.Fatalf("shop = %+v", shop)
	}
	// Every application shares the workspace root and the method, so a
	// saved plan, which carries no selection, is matched to its
	// application by the commands it runs, then by its build command.
	for _, name := range []string{"web", "api", "admin"} {
		want := byName[name]
		build := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: want.BuildCommand, StartCommand: want.StartCommand}
		for _, stored := range []BuildPlanConfig{build, {Method: BuildRecipe, Recipe: "node", BuildCommand: want.BuildCommand, StartCommand: "node custom.js"}} {
			if planned := plannedDetectionCandidate(&DetectionResult{Candidates: result.Candidates}, stored); planned == nil || planned.Name != name {
				t.Fatalf("the plan for %s was judged by %+v", name, planned)
			}
		}
	}
	// The recipe prepares each of them from the workspace root.
	for _, name := range []string{"web", "api", "admin", "gateway", "shop"} {
		candidate := byName[name]
		if candidate.RecipeIssue != "" {
			t.Fatalf("%s recipe issue: %s", name, candidate.RecipeIssue)
		}
	}
}
