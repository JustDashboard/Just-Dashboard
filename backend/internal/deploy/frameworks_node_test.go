package deploy

import (
	"context"
	"slices"
	"strings"
	"testing"
)

const angularWorkspace = `{
  "version": 1,
  "projects": {
    "my-app": {
      "projectType": "application",
      "architect": {
        "build": {"builder": "@angular/build:application", "options": {"outputPath": "dist/my-app"}}
      }
    },
    "shared": {"projectType": "library", "architect": {"build": {"builder": "@angular/build:ng-packagr"}}}
  }
}`

// Every framework the catalogue recognises, with the manifest a starter
// template of it ships, and what a deployment of that template has to be.
// The Remix and React Router rows are the regression: both list `vite` and
// used to be read as a static Vite site, which built cleanly and served a
// directory of client assets with no server behind it.
func TestNodeFrameworkCatalogueDetectsServingDefaults(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name, manifest, framework, output, start string
		port                                     int
		profile                                  WorkloadProfile
		confidence                               DetectionConfidence
		spa                                      bool
		files                                    map[string]string
		decision                                 string
	}{
		{name: "astro static", manifest: `{"scripts":{"build":"astro build"},"dependencies":{"astro":"^5.4.0"}}`,
			framework: "astro", output: "dist", port: 80, profile: ProfileStatic, confidence: ConfidenceHigh},
		{name: "astro node", manifest: `{"scripts":{"build":"astro build"},"dependencies":{"astro":"^5.4.0","@astrojs/node":"^9.0.0"}}`,
			framework: "astro", start: "node ./dist/server/entry.mjs", port: 4321, profile: ProfileWeb, confidence: ConfidenceHigh},
		{name: "astro provider adapter", manifest: `{"scripts":{"build":"astro build"},"dependencies":{"astro":"^5.4.0","@astrojs/vercel":"^8.0.0"}}`,
			framework: "astro", output: "dist", port: 80, profile: ProfileStatic, confidence: ConfidenceLow, decision: "use @astrojs/node"},
		{name: "nuxt", manifest: `{"scripts":{"build":"nuxt build","dev":"nuxt dev"},"dependencies":{"nuxt":"^3.15.0","vue":"^3.5.0"}}`,
			framework: "nuxt", start: "node .output/server/index.mjs", port: 3000, profile: ProfileWeb, confidence: ConfidenceHigh},
		{name: "nuxt generate", manifest: `{"scripts":{"build":"nuxt generate"},"dependencies":{"nuxt":"^3.15.0"}}`,
			framework: "nuxt", output: ".output/public", port: 80, profile: ProfileStatic, confidence: ConfidenceHigh},
		{name: "nuxt 2", manifest: `{"scripts":{"build":"nuxt build"},"dependencies":{"nuxt":"^2.17.0"}}`,
			framework: "nuxt", start: "npx nuxt start", port: 3000, profile: ProfileWeb, confidence: ConfidenceMedium},
		{name: "remix with its own start script", manifest: `{"scripts":{"build":"remix vite:build","start":"remix-serve ./build/server/index.js"},"dependencies":{"@remix-run/node":"^2.15.0","@remix-run/serve":"^2.15.0"},"devDependencies":{"@remix-run/dev":"^2.15.0","vite":"^5.4.0"}}`,
			framework: "remix", start: "npm run start", port: 3000, profile: ProfileWeb, confidence: ConfidenceHigh},
		{name: "react router framework mode", manifest: `{"scripts":{"build":"react-router build"},"dependencies":{"@react-router/serve":"^7.1.0","react-router":"^7.1.0"},"devDependencies":{"@react-router/dev":"^7.1.0","vite":"^6.0.0"}}`,
			framework: "react-router", start: "npx react-router-serve ./build/server/index.js", port: 3000, profile: ProfileWeb, confidence: ConfidenceHigh},
		{name: "react router library in a vite spa", manifest: `{"scripts":{"build":"vite build"},"dependencies":{"react-router":"^7.1.0"},"devDependencies":{"vite":"^6.0.0"}}`,
			framework: "vite", output: "dist", port: 80, profile: ProfileStatic, confidence: ConfidenceHigh, spa: true},
		{name: "remix without a server", manifest: `{"scripts":{"build":"remix vite:build"},"devDependencies":{"@remix-run/dev":"^2.15.0","vite":"^5.4.0"}}`,
			framework: "remix", port: 3000, profile: ProfileWeb, confidence: ConfidenceMedium, decision: "confirm the start command"},
		{name: "solid start", manifest: `{"scripts":{"build":"vinxi build"},"dependencies":{"@solidjs/start":"^1.1.0","vinxi":"^0.5.0"},"devDependencies":{"vite":"^6.0.0"}}`,
			framework: "solid-start", start: "node .output/server/index.mjs", port: 3000, profile: ProfileWeb, confidence: ConfidenceHigh},
		{name: "tanstack start", manifest: `{"scripts":{"build":"vite build"},"dependencies":{"@tanstack/react-start":"^1.120.0"},"devDependencies":{"vite":"^6.0.0"}}`,
			framework: "tanstack-start", start: "node .output/server/index.mjs", port: 3000, profile: ProfileWeb, confidence: ConfidenceMedium, decision: "confirm the server entry"},
		{name: "angular application builder", manifest: `{"scripts":{"build":"ng build","start":"ng serve"},"dependencies":{"@angular/core":"^19.0.0"}}`,
			files:     map[string]string{"angular.json": angularWorkspace},
			framework: "angular", output: "dist/my-app/browser", port: 80, profile: ProfileStatic, confidence: ConfidenceHigh, spa: true},
		// The Angular CLI writes "start": "ng serve", the development server,
		// into every SSR project; the production server is its entry.
		{name: "angular ssr", manifest: `{"scripts":{"build":"ng build","start":"ng serve"},"dependencies":{"@angular/core":"^19.0.0","@angular/ssr":"^19.0.0"}}`,
			files:     map[string]string{"angular.json": angularWorkspace},
			framework: "angular", start: "node dist/my-app/server/server.mjs", port: 4000, profile: ProfileWeb, confidence: ConfidenceHigh},
		{name: "angular without a workspace file", manifest: `{"scripts":{"build":"ng build"},"dependencies":{"@angular/core":"^19.0.0"}}`,
			framework: "angular", output: "dist", port: 80, profile: ProfileStatic, confidence: ConfidenceMedium, spa: true, decision: "angular.json"},
		{name: "nest with start:prod", manifest: `{"scripts":{"build":"nest build","start":"nest start","start:prod":"node dist/main"},"dependencies":{"@nestjs/core":"^11.0.0"}}`,
			framework: "nestjs", start: "npm run start:prod", port: 3000, profile: ProfileWeb, confidence: ConfidenceHigh},
		{name: "nest without start:prod", manifest: `{"scripts":{"build":"nest build","start":"nest start"},"dependencies":{"@nestjs/core":"^11.0.0"}}`,
			framework: "nestjs", start: "node dist/main", port: 3000, profile: ProfileWeb, confidence: ConfidenceHigh},
		{name: "gatsby", manifest: `{"scripts":{"build":"gatsby build"},"dependencies":{"gatsby":"^5.14.0"}}`,
			framework: "gatsby", output: "public", port: 80, profile: ProfileStatic, confidence: ConfidenceHigh},
		{name: "docusaurus", manifest: `{"scripts":{"build":"docusaurus build"},"dependencies":{"@docusaurus/core":"^3.7.0"}}`,
			framework: "docusaurus", output: "build", port: 80, profile: ProfileStatic, confidence: ConfidenceHigh},
		{name: "vitepress docs directory", manifest: `{"scripts":{"docs:build":"vitepress build docs","docs:dev":"vitepress dev docs"},"devDependencies":{"vitepress":"^1.6.0"}}`,
			framework: "vitepress", output: "docs/.vitepress/dist", port: 80, profile: ProfileStatic, confidence: ConfidenceHigh},
		{name: "eleventy", manifest: `{"scripts":{"build":"eleventy"},"devDependencies":{"@11ty/eleventy":"^3.0.0"}}`,
			framework: "eleventy", output: "_site", port: 80, profile: ProfileStatic, confidence: ConfidenceHigh},
		{name: "create react app", manifest: `{"scripts":{"build":"react-scripts build","start":"react-scripts start"},"dependencies":{"react-scripts":"5.0.1"}}`,
			framework: "create-react-app", output: "build", port: 80, profile: ProfileStatic, confidence: ConfidenceHigh, spa: true},
		{name: "vue cli", manifest: `{"scripts":{"build":"vue-cli-service build"},"devDependencies":{"@vue/cli-service":"~5.0.0"}}`,
			framework: "vue-cli", output: "dist", port: 80, profile: ProfileStatic, confidence: ConfidenceHigh, spa: true},
		{name: "vite spa", manifest: `{"scripts":{"build":"vite build","start":"vite --port 5173"},"devDependencies":{"vite":"^6.0.0"}}`,
			framework: "vite", output: "dist", port: 80, profile: ProfileStatic, confidence: ConfidenceHigh, spa: true},
		{name: "express service", manifest: `{"scripts":{"start":"node server.js"},"dependencies":{"express":"^4.21.0"}}`,
			framework: "express", start: "npm run start", port: 3000, profile: ProfileWeb, confidence: ConfidenceMedium},
		{name: "main entry with an http library", manifest: `{"main":"src/index.js","dependencies":{"fastify":"^5.0.0"}}`,
			framework: "fastify", start: "node src/index.js", port: 3000, profile: ProfileWeb, confidence: ConfidenceMedium},
		{name: "main entry alone", manifest: `{"main":"index.js","dependencies":{"lodash":"^4.17.21"}}`,
			start: "node index.js", profile: ProfileWorker, confidence: ConfidenceLow, decision: "web application) or runs as a worker"},
		{name: "main entry of a bot", manifest: `{"main":"bot.js","dependencies":{"discord.js":"^14.0.0"}}`,
			start: "node bot.js", profile: ProfileWorker, confidence: ConfidenceMedium},
		{name: "framework without its build script", manifest: `{"scripts":{"dev":"astro dev"},"dependencies":{"astro":"^5.4.0"}}`,
			framework: "astro", output: "dist", port: 80, profile: ProfileStatic, confidence: ConfidenceLow, decision: "add a build script"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeBuildFixture(t, root, "package.json", fixture.manifest)
			writeBuildFixture(t, root, "package-lock.json", `{}`)
			for path, content := range fixture.files {
				writeBuildFixture(t, root, path, content)
			}
			result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
			if err != nil || len(result.Candidates) != 1 {
				t.Fatalf("detect: %+v, %v", result, err)
			}
			candidate := result.Candidates[0]
			if candidate.Framework != fixture.framework || candidate.OutputDirectory != fixture.output ||
				candidate.StartCommand != fixture.start || candidate.Port != fixture.port ||
				candidate.Profile != fixture.profile || candidate.Confidence != fixture.confidence ||
				candidate.SPAFallback != fixture.spa {
				t.Fatalf("candidate = %+v", candidate)
			}
			if fixture.decision != "" && !slices.ContainsFunc(candidate.NeedsDecision, func(d string) bool { return strings.Contains(d, fixture.decision) }) {
				t.Fatalf("decisions %q lack %q", candidate.NeedsDecision, fixture.decision)
			}
			if fixture.decision == "" && fixture.confidence == ConfidenceHigh && len(candidate.NeedsDecision) != 0 {
				t.Fatalf("unexpected decisions: %q", candidate.NeedsDecision)
			}
		})
	}
}

// The lockfile still names the runner for a framework's own binary: a Bun
// project's Next.js is started through bunx, and its main entry through bun.
func TestNodeFrameworkDefaultsFollowTheLockfileRunner(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "package.json", `{"scripts":{"build":"next build"},"dependencies":{"next":"15.0.0"}}`)
	writeBuildFixture(t, root, "bun.lock", ``)
	result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
	if err != nil || len(result.Candidates) != 1 {
		t.Fatalf("detect: %+v, %v", result, err)
	}
	if got := result.Candidates[0]; got.StartCommand != "bunx next start" || got.BuildCommand != "bun run build" {
		t.Fatalf("bun runner: %+v", got)
	}
	root = t.TempDir()
	writeBuildFixture(t, root, "package.json", `{"main":"src/index.ts","dependencies":{"hono":"^4.6.0"}}`)
	writeBuildFixture(t, root, "bun.lock", ``)
	result, err = (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
	if err != nil || len(result.Candidates) != 1 {
		t.Fatalf("detect: %+v, %v", result, err)
	}
	if got := result.Candidates[0]; got.StartCommand != "bun src/index.ts" || got.Framework != "hono" || got.Port != 3000 {
		t.Fatalf("bun entry: %+v", got)
	}
}

// A Procfile is the repository saying how it is served; it outranks the
// start script and the framework's default, and never turns a site into a
// server.
func TestProcfileWebProcessOutranksGuessesButNotStaticOutput(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "package.json", `{"scripts":{"build":"nuxt build","start":"nuxt preview"},"dependencies":{"nuxt":"^3.15.0"}}`)
	writeBuildFixture(t, root, "package-lock.json", `{}`)
	writeBuildFixture(t, root, "Procfile", "# release: npm run migrate\nweb: node .output/server/index.mjs --port $PORT\nworker: node queue.js\n")
	result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
	if err != nil || len(result.Candidates) != 1 {
		t.Fatalf("detect: %+v, %v", result, err)
	}
	if got := result.Candidates[0]; got.StartCommand != "node .output/server/index.mjs --port $PORT" || got.Profile != ProfileWeb {
		t.Fatalf("procfile: %+v", got)
	}
	if !slices.ContainsFunc(result.Candidates[0].Evidence, func(e DetectionEvidence) bool { return e.Path == "Procfile" }) {
		t.Fatalf("no Procfile evidence: %+v", result.Candidates[0].Evidence)
	}

	root = t.TempDir()
	writeBuildFixture(t, root, "package.json", `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"^6.0.0"}}`)
	writeBuildFixture(t, root, "package-lock.json", `{}`)
	writeBuildFixture(t, root, "Procfile", "web: npx serve -s dist\n")
	result, err = (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
	if err != nil || len(result.Candidates) != 1 {
		t.Fatalf("detect: %+v, %v", result, err)
	}
	if got := result.Candidates[0]; got.StartCommand != "" || got.OutputDirectory != "dist" || got.Profile != ProfileStatic {
		t.Fatalf("static site kept its server: %+v", got)
	}
}

// The schema step chains in front of a framework's default start command
// exactly as it does in front of a start script.
func TestSchemaStepChainsBeforeAFrameworkDefaultStart(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "package.json", `{"scripts":{"build":"nuxt build"},"dependencies":{"nuxt":"^3.15.0","@prisma/client":"6"},"devDependencies":{"prisma":"6"}}`)
	writeBuildFixture(t, root, "pnpm-lock.yaml", `lockfileVersion: 9`)
	writeBuildFixture(t, root, "prisma/schema.prisma", "datasource db { provider = \"postgresql\" }")
	result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
	if err != nil || len(result.Candidates) != 1 {
		t.Fatalf("detect: %+v, %v", result, err)
	}
	if got := result.Candidates[0]; got.StartCommand != "pnpm exec prisma db push && node .output/server/index.mjs" || got.SchemaTool != "prisma" {
		t.Fatalf("chained start: %+v", got)
	}
}

func TestAngularOutputReadsTheWorkspace(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct{ name, workspace, want string }{
		{"application builder", angularWorkspace, "dist/my-app/browser"},
		{"browser builder", `{"projects":{"site":{"projectType":"application","architect":{"build":{"builder":"@angular-devkit/build-angular:browser","options":{"outputPath":"www"}}}}}}`, "www"},
		{"structured output path", `{"projects":{"site":{"projectType":"application","architect":{"build":{"builder":"@angular/build:application","options":{"outputPath":{"base":"out/site","browser":"public"}}}}}}}`, "out/site/public"},
		{"default project", `{"defaultProject":"b","projects":{"a":{"projectType":"application","architect":{"build":{"builder":"@angular/build:application"}}},"b":{"projectType":"application","architect":{"build":{"builder":"@angular/build:application"}}}}}`, "dist/b/browser"},
		{"escaping output path", `{"projects":{"site":{"projectType":"application","architect":{"build":{"builder":"@angular/build:application","options":{"outputPath":"../elsewhere"}}}}}}`, ""},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			_, output, ok := angularOutput([]byte(fixture.workspace))
			if output != fixture.want || ok != (fixture.want != "") {
				t.Fatalf("angularOutput = %q, %v", output, ok)
			}
		})
	}
}

func TestProcfileAndVitepressHelpers(t *testing.T) {
	t.Parallel()
	if got := procfileProcess([]byte("web:gunicorn app:app\nrelease: ./migrate\n"), "release"); got != "./migrate" {
		t.Fatal(got)
	}
	if got := procfileProcess([]byte("webhook: x\n"), "web"); got != "" {
		t.Fatal(got)
	}
	if script, dir := vitepressBuild(map[string]string{"build": "vitepress build"}); script != "build" || dir != "." {
		t.Fatal(script, dir)
	}
	if script, dir := vitepressBuild(map[string]string{"docs:build": "vitepress build docs --base /x/"}); script != "docs:build" || dir != "docs" {
		t.Fatal(script, dir)
	}
	if script, dir := vitepressBuild(map[string]string{"build": "vitepress build ../escape"}); script != "build" || dir != "." {
		t.Fatal(script, dir)
	}
	for version, want := range map[string]string{"^2.15.0": "2", "~3": "3", ">=4.0.0": "4", "latest": "", "": ""} {
		if got := semverMajor(version); got != want {
			t.Fatalf("semverMajor(%q) = %q", version, got)
		}
	}
	if !frameworkDefaultStart("npx prisma migrate deploy && node build", "node build") || frameworkDefaultStart("node server.js", "node build") || frameworkDefaultStart("node build", "") {
		t.Fatal("default start recognition")
	}
}
