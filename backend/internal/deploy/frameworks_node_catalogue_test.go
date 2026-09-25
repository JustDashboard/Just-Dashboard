package deploy

import (
	"slices"
	"strings"
	"testing"
)

// nodeFixture is one repository shape and the candidate detection must make
// of it: what serves it, from where, and what is left for the operator.
type nodeFixture struct {
	name                   string
	files                  map[string]string
	framework, build       string
	start, output          string
	port                   int
	profile                WorkloadProfile
	confidence             DetectionConfidence
	spa                    bool
	decision, evidence     string
	finding, issue, notice string
}

func (fixture nodeFixture) check(t *testing.T, candidate DetectedCandidate) {
	t.Helper()
	if candidate.Framework != fixture.framework || candidate.StartCommand != fixture.start ||
		candidate.OutputDirectory != fixture.output || candidate.Port != fixture.port || candidate.Profile != fixture.profile ||
		candidate.Confidence != fixture.confidence || candidate.SPAFallback != fixture.spa ||
		(fixture.build != "" && candidate.BuildCommand != fixture.build) {
		t.Fatalf("candidate = framework %q build %q start %q output %q port %d profile %s confidence %s spa %v decisions %q issue %q",
			candidate.Framework, candidate.BuildCommand, candidate.StartCommand, candidate.OutputDirectory, candidate.Port,
			candidate.Profile, candidate.Confidence, candidate.SPAFallback, candidate.NeedsDecision, candidate.RecipeIssue)
	}
	if fixture.decision == "" && len(candidate.NeedsDecision) != 0 {
		t.Fatalf("unexpected decisions %q", candidate.NeedsDecision)
	}
	if fixture.decision != "" && !slices.ContainsFunc(candidate.NeedsDecision, func(d string) bool { return strings.Contains(d, fixture.decision) }) {
		t.Fatalf("decisions %q lack %q", candidate.NeedsDecision, fixture.decision)
	}
	if fixture.evidence != "" && !slices.ContainsFunc(candidate.Evidence, func(e DetectionEvidence) bool { return strings.Contains(e.Reason, fixture.evidence) }) {
		t.Fatalf("evidence %+v lacks %q", candidate.Evidence, fixture.evidence)
	}
	if fixture.finding != "" && (candidate.NodeBuild == nil || findingByCode(candidate.NodeBuild.Findings, fixture.finding) == nil) {
		t.Fatalf("framework findings %+v lack %s", candidate.NodeBuild, fixture.finding)
	}
	if (fixture.issue == "" && candidate.RecipeIssue != "") || !strings.Contains(candidate.RecipeIssue, fixture.issue) {
		t.Fatalf("recipe issue %q, want %q", candidate.RecipeIssue, fixture.issue)
	}
}

func withLockfile(files map[string]string) map[string]string {
	if _, ok := files["package-lock.json"]; !ok {
		files["package-lock.json"] = "{}"
	}
	return files
}

const sveltekitAuto = `import adapter from '@sveltejs/adapter-auto';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

/** @type {import('@sveltejs/kit').Config} */
const config = {
	preprocess: vitePreprocess(),
	kit: {
		// adapter-auto only supports some environments, see https://svelte.dev/docs/kit/adapter-auto
		adapter: adapter()
	}
};

export default config;
`

// Every framework the catalogue recognises reads its own configuration as
// data: the output a config file sets, the adapter it imports, the entry a
// build writes. Each row is a starter or a common real-world shape.
func TestNodeFrameworkConfigurationDecidesTheServingPlan(t *testing.T) {
	t.Parallel()
	nest := func(extra map[string]string) map[string]string {
		files := map[string]string{
			"package.json":        `{"scripts":{"build":"nest build","start":"nest start","start:dev":"nest start --watch","start:prod":"node dist/main"},"dependencies":{"@nestjs/core":"^11.0.0"}}`,
			"nest-cli.json":       `{"$schema":"https://json.schemastore.org/nest-cli","collection":"@nestjs/schematics","sourceRoot":"src","compilerOptions":{"deleteOutDir":true}}`,
			"tsconfig.json":       "{\n  // the Nest CLI's own\n  \"compilerOptions\": {\"module\": \"commonjs\", \"outDir\": \"./dist\", \"baseUrl\": \"./\",},\n}",
			"tsconfig.build.json": `{"extends":"./tsconfig.json","exclude":["node_modules","test","dist","**/*spec.ts"]}`,
			"src/main.ts":         "bootstrap()",
		}
		for name, content := range extra {
			files[name] = content
		}
		return files
	}
	for _, fixture := range []nodeFixture{
		{name: "next static export", files: map[string]string{
			"package.json":   `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16.0.0"}}`,
			"next.config.ts": "import type { NextConfig } from 'next'\nconst nextConfig: NextConfig = {\n  output: 'export',\n  images: { unoptimized: true },\n}\nexport default nextConfig\n",
		}, framework: "nextjs", output: "out", port: 80, profile: ProfileStatic, confidence: ConfidenceHigh, evidence: "output: 'export'"},
		{name: "next export to distDir with next/image", files: map[string]string{
			"package.json":       `{"scripts":{"build":"next build"},"dependencies":{"next":"15.2.0"}}`,
			"next.config.mjs":    "export default { output: \"export\", distDir: \"dist\" }",
			"app/page.tsx":       "import Image from 'next/image'\nexport default function Page() { return <Image src=\"/a.png\" alt=\"\" /> }",
			"app/about/page.tsx": "export default function About() { return null }",
		}, framework: "nextjs", output: "dist", port: 80, profile: ProfileStatic, confidence: ConfidenceHigh, finding: "next_export_images"},
		{name: "next export commented out", files: map[string]string{
			"package.json":    `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16.0.0"}}`,
			"next.config.mjs": "const nextConfig = {\n  // output: 'export',\n  /* output: 'standalone' */\n};\nexport default nextConfig;\n",
		}, framework: "nextjs", start: "npm run start", port: 3000, profile: ProfileWeb, confidence: ConfidenceHigh},
		{name: "next output from an expression", files: map[string]string{
			"package.json":   `{"scripts":{"build":"next build"},"dependencies":{"next":"16.0.0"}}`,
			"next.config.js": "module.exports = { output: process.env.STATIC ? 'export' : undefined }",
		}, framework: "nextjs", start: "npx next start", port: 3000, profile: ProfileWeb, confidence: ConfidenceMedium, decision: "sets output from an expression"},
		{name: "next dev as the start script", files: map[string]string{
			"package.json": `{"scripts":{"build":"next build","start":"next dev"},"dependencies":{"next":"16.0.0"}}`,
		}, framework: "nextjs", start: "npx next start", port: 3000, profile: ProfileWeb, confidence: ConfidenceHigh, evidence: "the start script runs next dev"},
		{name: "payload on next", files: map[string]string{
			"package.json": `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"15.4.0","payload":"3.40.0","@payloadcms/next":"3.40.0"}}`,
		}, framework: "nextjs", start: "npm run start", port: 3000, profile: ProfileWeb, confidence: ConfidenceHigh, evidence: "Payload runs inside Next.js"},
		{name: "sveltekit adapter-auto", files: map[string]string{
			"package.json":     `{"type":"module","scripts":{"build":"vite build","dev":"vite dev"},"devDependencies":{"@sveltejs/kit":"^2.20.0","@sveltejs/adapter-auto":"^6.0.0","vite":"^6.0.0"}}`,
			"svelte.config.js": sveltekitAuto,
		}, framework: "sveltekit", start: "node build", port: 3000, profile: ProfileWeb, confidence: ConfidenceHigh, finding: "sveltekit_adapter_substituted"},
		{name: "sveltekit provider adapter", files: map[string]string{
			"package.json":     `{"type":"module","scripts":{"build":"vite build"},"devDependencies":{"@sveltejs/kit":"^2.20.0","@sveltejs/adapter-vercel":"^5.0.0","vite":"^6.0.0"}}`,
			"svelte.config.js": "import adapter from '@sveltejs/adapter-vercel';\nexport default { kit: { adapter: adapter({ runtime: 'nodejs20.x' }) } };\n",
		}, framework: "sveltekit", start: "node build", port: 3000, profile: ProfileWeb, confidence: ConfidenceHigh, finding: "sveltekit_adapter_substituted"},
		{name: "sveltekit adapter-node installed, config still auto", files: map[string]string{
			"package.json":     `{"type":"module","scripts":{"build":"vite build"},"devDependencies":{"@sveltejs/kit":"^2.20.0","@sveltejs/adapter-auto":"^6.0.0","@sveltejs/adapter-node":"^5.2.0"}}`,
			"svelte.config.js": sveltekitAuto,
		}, framework: "sveltekit", start: "node build", port: 3000, profile: ProfileWeb, confidence: ConfidenceHigh, finding: "sveltekit_adapter_substituted"},
		{name: "sveltekit static spa fallback", files: map[string]string{
			"package.json":     `{"type":"module","scripts":{"build":"vite build"},"devDependencies":{"@sveltejs/kit":"^2.20.0","@sveltejs/adapter-static":"^3.0.0"}}`,
			"svelte.config.js": "import adapter from '@sveltejs/adapter-static';\nexport default { kit: { adapter: adapter({ pages: 'public', fallback: '200.html' }) } };\n",
		}, framework: "sveltekit", output: "public", port: 80, profile: ProfileStatic, confidence: ConfidenceHigh, spa: true, evidence: "fallback 200.html"},
		{name: "astro node middleware", files: map[string]string{
			"package.json":     `{"scripts":{"build":"astro build"},"dependencies":{"astro":"^5.4.0","@astrojs/node":"^9.0.0"}}`,
			"astro.config.mjs": "import node from '@astrojs/node';\nexport default defineConfig({ output: 'server', adapter: node({ mode: 'middleware' }) });\n",
		}, framework: "astro", start: "node ./dist/server/entry.mjs", port: 4321, profile: ProfileWeb, confidence: ConfidenceMedium, decision: "middleware mode"},
		{name: "astro server without an adapter", files: map[string]string{
			"package.json":     `{"scripts":{"build":"astro build"},"dependencies":{"astro":"^5.4.0"}}`,
			"astro.config.mjs": "export default defineConfig({ output: 'server' });\n",
		}, framework: "astro", output: "dist", port: 80, profile: ProfileStatic, confidence: ConfidenceLow, decision: "without an adapter"},
		{name: "react router spa mode", files: map[string]string{
			"package.json":           `{"scripts":{"build":"react-router build","dev":"react-router dev"},"dependencies":{"react-router":"^7.6.0"},"devDependencies":{"@react-router/dev":"^7.6.0","vite":"^6.0.0"}}`,
			"react-router.config.ts": "import type { Config } from '@react-router/dev/config';\nexport default { ssr: false } satisfies Config;\n",
		}, framework: "react-router", output: "build/client", port: 80, profile: ProfileStatic, confidence: ConfidenceHigh, spa: true},
		{name: "remix spa mode", files: map[string]string{
			"package.json":   `{"scripts":{"build":"remix vite:build"},"devDependencies":{"@remix-run/dev":"^2.15.0","vite":"^5.4.0"}}`,
			"vite.config.ts": "export default defineConfig({ plugins: [remix({ ssr: false })] });\n",
		}, framework: "remix", output: "build/client", port: 80, profile: ProfileStatic, confidence: ConfidenceHigh, spa: true},
		{name: "react router custom express server", files: map[string]string{
			"package.json": `{"scripts":{"build":"react-router build","start":"cross-env NODE_ENV=production node ./server.js","dev":"node ./server.js"},"dependencies":{"express":"^5.0.0","@react-router/express":"^7.6.0"},"devDependencies":{"@react-router/dev":"^7.6.0","vite":"^6.0.0"}}`,
		}, framework: "react-router", start: "npm run start", port: 3000, profile: ProfileWeb, confidence: ConfidenceHigh},
		{name: "angular ssr runs ng serve as its start script", files: map[string]string{
			"package.json": `{"scripts":{"ng":"ng","start":"ng serve","build":"ng build"},"dependencies":{"@angular/core":"^20.0.0","@angular/ssr":"^20.0.0"}}`,
			"angular.json": angularWorkspace,
		}, framework: "angular", start: "node dist/my-app/server/server.mjs", port: 4000, profile: ProfileWeb, confidence: ConfidenceHigh, evidence: "the start script runs ng serve"},
		{name: "angular ssr with its serve:ssr script", files: map[string]string{
			"package.json": `{"scripts":{"start":"ng serve","build":"ng build","serve:ssr:my-app":"node dist/my-app/server/server.mjs"},"dependencies":{"@angular/core":"^20.0.0","@angular/ssr":"^20.0.0"}}`,
			"angular.json": angularWorkspace,
		}, framework: "angular", start: "npm run serve:ssr:my-app", port: 4000, profile: ProfileWeb, confidence: ConfidenceHigh},
		{name: "angular static output mode", files: map[string]string{
			"package.json": `{"scripts":{"start":"ng serve","build":"ng build"},"dependencies":{"@angular/core":"^20.0.0","@angular/ssr":"^20.0.0"}}`,
			"angular.json": strings.Replace(angularWorkspace, `"outputPath": "dist/my-app"}`, `"outputPath": "dist/my-app", "outputMode": "static"}`, 1),
		}, framework: "angular", output: "dist/my-app/browser", port: 80, profile: ProfileStatic, confidence: ConfidenceHigh, spa: true, evidence: "outputMode static"},
		{name: "nuxt with a provider preset", files: map[string]string{
			"package.json":   `{"scripts":{"build":"nuxt build"},"dependencies":{"nuxt":"^3.15.0"}}`,
			"nuxt.config.ts": "export default defineNuxtConfig({ nitro: { preset: 'vercel' } })\n",
		}, framework: "nuxt", start: "node .output/server/index.mjs", port: 3000, profile: ProfileWeb, confidence: ConfidenceHigh, finding: "nitro_preset_overridden"},
		{name: "nuxt with the static preset", files: map[string]string{
			"package.json":   `{"scripts":{"build":"nuxt build"},"dependencies":{"nuxt":"^3.15.0"}}`,
			"nuxt.config.ts": "export default defineNuxtConfig({ nitro: { preset: 'static' } })\n",
		}, framework: "nuxt", output: ".output/public", port: 80, profile: ProfileStatic, confidence: ConfidenceHigh},
		{name: "vite outDir and base", files: map[string]string{
			"package.json":   `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"^6.0.0"}}`,
			"vite.config.ts": "export default defineConfig({ base: '/app/', build: { outDir: 'build' } })\n",
			"index.html":     "<div id=app></div>",
		}, framework: "vite", output: "build", port: 80, profile: ProfileStatic, confidence: ConfidenceHigh, spa: true},
		{name: "nest compiled beside prisma.config.ts", files: nest(map[string]string{"prisma.config.ts": "export default {}"}),
			framework: "nestjs", start: "node dist/src/main", port: 3000, profile: ProfileWeb, confidence: ConfidenceHigh, finding: "nest_output_layout", evidence: "dist/src/main.js"},
		{name: "nest with a seed under prisma/", files: nest(map[string]string{"prisma/seed.ts": "main()"}),
			framework: "nestjs", start: "node dist/src/main", port: 3000, profile: ProfileWeb, confidence: ConfidenceHigh, finding: "nest_output_layout"},
		{name: "nest whose build includes only src", files: nest(map[string]string{
			"prisma.config.ts":    "export default {}",
			"tsconfig.build.json": `{"extends":"./tsconfig.json","include":["src"],"exclude":["node_modules","test","dist"]}`,
		}), framework: "nestjs", start: "npm run start:prod", port: 3000, profile: ProfileWeb, confidence: ConfidenceHigh},
		{name: "nest whose build excludes the root config", files: nest(map[string]string{
			"prisma.config.ts":    "export default {}",
			"tsconfig.build.json": `{"extends":"./tsconfig.json","exclude":["node_modules","test","dist","**/*spec.ts","prisma.config.ts"]}`,
		}), framework: "nestjs", start: "npm run start:prod", port: 3000, profile: ProfileWeb, confidence: ConfidenceHigh},
		{name: "nest monorepo", files: nest(map[string]string{
			"nest-cli.json": `{"sourceRoot":"apps/api/src","monorepo":true,"root":"apps/api","compilerOptions":{"webpack":true}}`,
		}), framework: "nestjs", start: "node dist/apps/api/main", port: 3000, profile: ProfileWeb, confidence: ConfidenceHigh, finding: "nest_output_layout"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			result := detectFixture(t, withLockfile(fixture.files))
			fixture.check(t, fixtureCandidate(t, result, BuildRecipe))
		})
	}
}

// The stacks that were missing from the catalogue: headless CMSs and
// commerce, AdonisJS, the long-tail meta-frameworks and the site bundlers,
// each with the start, port and build its own documentation deploys with.
func TestNodeFrameworkCatalogueCoversTheLongTail(t *testing.T) {
	t.Parallel()
	for _, fixture := range []nodeFixture{
		{name: "strapi", files: map[string]string{
			"package.json": `{"scripts":{"develop":"strapi develop","start":"strapi start","build":"strapi build"},"dependencies":{"@strapi/strapi":"5.12.0","better-sqlite3":"11.3.0"}}`,
		}, framework: "strapi", start: "npm run start", port: 1337, profile: ProfileWeb, confidence: ConfidenceHigh},
		{name: "directus", files: map[string]string{
			"package.json": `{"dependencies":{"directus":"^11.5.0"}}`,
		}, framework: "directus", start: "npx directus bootstrap && npx directus start", port: 8055, profile: ProfileWeb, confidence: ConfidenceHigh},
		{name: "keystone", files: map[string]string{
			"package.json": `{"scripts":{"dev":"keystone dev","start":"keystone start","build":"keystone build"},"dependencies":{"@keystone-6/core":"^6.3.0"}}`,
		}, framework: "keystone", start: "npx keystone start --with-migrations", port: 3000, profile: ProfileWeb, confidence: ConfidenceHigh},
		{name: "keystone without a build script", files: map[string]string{
			"package.json": `{"dependencies":{"@keystone-6/core":"^6.3.0"}}`,
		}, framework: "keystone", build: "npx keystone build", start: "npx keystone start --with-migrations", port: 3000, profile: ProfileWeb, confidence: ConfidenceHigh},
		{name: "medusa", files: map[string]string{
			"package.json":     `{"scripts":{"build":"medusa build","start":"medusa start","dev":"medusa develop"},"dependencies":{"@medusajs/medusa":"2.8.4","@medusajs/framework":"2.8.4"}}`,
			"medusa-config.ts": "module.exports = defineConfig({})",
		}, framework: "medusa", start: "cd .medusa/server && medusa db:migrate && medusa start", port: 9000, profile: ProfileWeb, confidence: ConfidenceHigh},
		{name: "a medusa plugin is not the store", files: map[string]string{
			"package.json": `{"name":"medusa-plugin-x","main":"dist/index.js","devDependencies":{"@medusajs/medusa":"2.8.4","@medusajs/framework":"2.8.4"}}`,
		}, start: "node dist/index.js", profile: ProfileWorker, confidence: ConfidenceLow, decision: "serves HTTP"},
		{name: "adonisjs with lucid migrations", files: map[string]string{
			"package.json":                   `{"type":"module","scripts":{"start":"node bin/server.js","build":"node ace build","dev":"node ace serve --hmr"},"dependencies":{"@adonisjs/core":"^6.17.0","@adonisjs/lucid":"^21.6.0"}}`,
			"adonisrc.ts":                    "export default defineConfig({})",
			"database/migrations/1_users.ts": "export default class extends BaseSchema {}",
		}, framework: "adonisjs", start: "node build/ace.js migration:run --force && node build/bin/server.js", port: 3333, profile: ProfileWeb, confidence: ConfidenceHigh},
		{name: "adonisjs without a database", files: map[string]string{
			"package.json": `{"type":"module","scripts":{"start":"node bin/server.js","build":"node ace build"},"dependencies":{"@adonisjs/core":"^6.17.0"}}`,
		}, framework: "adonisjs", start: "node build/bin/server.js", port: 3333, profile: ProfileWeb, confidence: ConfidenceHigh},
		{name: "qwik city express adapter", files: map[string]string{
			"package.json": `{"scripts":{"build":"qwik build","build.server":"vite build -c adapters/express/vite.config.ts","serve":"node server/entry.express","start":"vite --open --mode ssr"},"dependencies":{"express":"^4.21.0"},"devDependencies":{"@builder.io/qwik":"^1.12.0","@builder.io/qwik-city":"^1.12.0","vite":"^5.4.0"}}`,
		}, framework: "qwik-city", start: "npm run serve", port: 3000, profile: ProfileWeb, confidence: ConfidenceHigh},
		{name: "qwik city without an adapter", files: map[string]string{
			"package.json": `{"scripts":{"build":"qwik build","start":"vite --open --mode ssr"},"devDependencies":{"@builder.io/qwik":"^1.12.0","@builder.io/qwik-city":"^1.12.0","vite":"^5.4.0"}}`,
		}, framework: "qwik-city", output: "dist", port: 80, profile: ProfileStatic, confidence: ConfidenceLow, decision: "no server adapter"},
		{name: "vike with its own server", files: map[string]string{
			"package.json": `{"scripts":{"dev":"vike dev","build":"vike build","prod":"cross-env NODE_ENV=production node ./dist/server/index.mjs"},"dependencies":{"vike":"^0.4.230","express":"^5.0.0"},"devDependencies":{"vite":"^6.0.0"}}`,
		}, framework: "vike", start: "npm run prod", port: 3000, profile: ProfileWeb, confidence: ConfidenceHigh},
		{name: "vike prerendered", files: map[string]string{
			"package.json":   `{"scripts":{"dev":"vike dev","build":"vike build"},"dependencies":{"vike":"^0.4.230"},"devDependencies":{"vite":"^6.0.0"}}`,
			"vite.config.ts": "export default { plugins: [vike({ prerender: true })] }",
		}, framework: "vike", output: "dist/client", port: 80, profile: ProfileStatic, confidence: ConfidenceHigh},
		{name: "analog", files: map[string]string{
			"package.json": `{"scripts":{"build":"ng build"},"dependencies":{"@analogjs/platform":"^1.15.0","@angular/core":"^19.0.0"}}`,
		}, framework: "analog", start: "node dist/analog/server/index.mjs", port: 3000, profile: ProfileWeb, confidence: ConfidenceHigh},
		{name: "waku", files: map[string]string{
			"package.json": `{"scripts":{"dev":"waku dev","build":"waku build","start":"waku start"},"dependencies":{"waku":"0.23.0"}}`,
		}, framework: "waku", start: "npm run start", port: 8080, profile: ProfileWeb, confidence: ConfidenceHigh},
		{name: "tanstack start with the nitro vite plugin", files: map[string]string{
			"package.json":   `{"scripts":{"build":"vite build","dev":"vite dev"},"dependencies":{"@tanstack/react-start":"^1.130.0"},"devDependencies":{"vite":"^7.0.0","nitro":"^3.0.0"}}`,
			"vite.config.ts": "import { tanstackStart } from '@tanstack/react-start/plugin/vite'\nimport { nitro } from 'nitro/vite'\nexport default defineConfig({ plugins: [tanstackStart(), nitro()] })\n",
		}, framework: "tanstack-start", start: "node .output/server/index.mjs", port: 3000, profile: ProfileWeb, confidence: ConfidenceHigh},
		{name: "meteor", files: map[string]string{
			"package.json":    `{"scripts":{"start":"meteor run"},"dependencies":{"meteor-node-stubs":"^1.2.5"}}`,
			".meteor/release": "METEOR@3.1\n",
		}, framework: "meteor", start: "npm run start", port: 3000, profile: ProfileWeb, confidence: ConfidenceLow, issue: "Meteor builds with its own toolchain"},
		{name: "vuepress docs", files: map[string]string{
			"package.json": `{"scripts":{"docs:build":"vuepress build docs","docs:dev":"vuepress dev docs"},"devDependencies":{"vuepress":"^2.0.0-rc.20"}}`,
		}, framework: "vuepress", output: "docs/.vuepress/dist", port: 80, profile: ProfileStatic, confidence: ConfidenceHigh},
		{name: "webpack site", files: map[string]string{
			"package.json":      `{"scripts":{"build":"webpack --mode production","start":"webpack serve --mode development"},"devDependencies":{"webpack":"^5.98.0","webpack-cli":"^6.0.0"}}`,
			"webpack.config.js": "const path = require('path')\nmodule.exports = { output: { path: path.resolve(__dirname, 'public/build'), filename: 'app.js' } }\n",
		}, framework: "webpack", output: "public/build", port: 80, profile: ProfileStatic, confidence: ConfidenceMedium, spa: true},
		{name: "webpack only as a tool is not a site", files: map[string]string{
			"package.json": `{"main":"server.js","scripts":{"start":"node server.js","build":"tsc"},"dependencies":{"express":"^4.21.0"},"devDependencies":{"webpack":"^5.98.0"}}`,
		}, framework: "express", start: "npm run start", port: 3000, profile: ProfileWeb, confidence: ConfidenceMedium},
		{name: "rsbuild site", files: map[string]string{
			"package.json":      `{"scripts":{"build":"rsbuild build","dev":"rsbuild dev"},"devDependencies":{"@rsbuild/core":"^1.3.0"}}`,
			"rsbuild.config.ts": "export default defineConfig({ output: { distPath: { root: 'out' } } })\n",
		}, framework: "rsbuild", output: "out", port: 80, profile: ProfileStatic, confidence: ConfidenceMedium, spa: true},
		{name: "farm site", files: map[string]string{
			"package.json": `{"scripts":{"build":"farm build","dev":"farm start"},"devDependencies":{"@farmfe/core":"^1.7.0"}}`,
		}, framework: "farm", output: "dist", port: 80, profile: ProfileStatic, confidence: ConfidenceMedium, spa: true},
		{name: "socket.io server", files: map[string]string{
			"package.json": `{"main":"index.js","dependencies":{"socket.io":"^4.8.0"}}`,
		}, framework: "socket.io", start: "node index.js", port: 3000, profile: ProfileWeb, confidence: ConfidenceMedium},
		{name: "graphql yoga server", files: map[string]string{
			"package.json": `{"main":"server.mjs","dependencies":{"graphql-yoga":"^5.13.0","graphql":"^16.10.0"}}`,
		}, framework: "graphql-yoga", start: "node server.mjs", port: 3000, profile: ProfileWeb, confidence: ConfidenceMedium},
		{name: "trpc standalone server", files: map[string]string{
			"package.json": `{"main":"dist/server.js","scripts":{"build":"tsc"},"dependencies":{"@trpc/server":"^11.0.0"}}`,
		}, framework: "trpc", start: "node dist/server.js", port: 3000, profile: ProfileWeb, confidence: ConfidenceMedium},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			result := detectFixture(t, withLockfile(fixture.files))
			fixture.check(t, fixtureCandidate(t, result, BuildRecipe))
		})
	}
}

// A start script is what the author runs, which is often a watcher or a
// development server; the start command is chosen past it.
func TestNodeStartScriptsThatRunADevelopmentServer(t *testing.T) {
	t.Parallel()
	for _, fixture := range []nodeFixture{
		{name: "nodemon is replaced by node", files: map[string]string{
			"package.json": `{"scripts":{"start":"nodemon index.js"},"dependencies":{"express":"^4.21.0"},"devDependencies":{"nodemon":"^3.1.0"}}`,
		}, framework: "express", start: "node index.js", port: 3000, profile: ProfileWeb, confidence: ConfidenceMedium, evidence: "the start script runs nodemon"},
		{name: "a start:prod script serves the build", files: map[string]string{
			"package.json": `{"scripts":{"build":"tsc","start":"nodemon src/index.ts","start:prod":"node dist/index.js"},"dependencies":{"express":"^4.21.0"},"devDependencies":{"nodemon":"^3.1.0"}}`,
		}, framework: "express", start: "npm run start:prod", port: 3000, profile: ProfileWeb, confidence: ConfidenceMedium},
		{name: "tsx watch runs tsx", files: map[string]string{
			"package.json": `{"scripts":{"start":"tsx watch --clear-screen=false src/index.ts"},"dependencies":{"fastify":"^5.0.0"},"devDependencies":{"tsx":"^4.19.0"}}`,
		}, framework: "fastify", start: "npx tsx src/index.ts", port: 3000, profile: ProfileWeb, confidence: ConfidenceMedium},
		{name: "node --watch runs node, after prestart", files: map[string]string{
			"package.json": `{"scripts":{"prestart":"node migrate.js","start":"node --watch server.js"},"dependencies":{"koa":"^2.15.0"}}`,
		}, framework: "koa", start: "npm run prestart && node server.js", port: 3000, profile: ProfileWeb, confidence: ConfidenceMedium},
		{name: "a start that runs the dev script", files: map[string]string{
			"package.json": `{"scripts":{"start":"npm run dev","dev":"nodemon app.js"},"dependencies":{"express":"^4.21.0"}}`,
		}, framework: "express", start: "npm run start", port: 3000, profile: ProfileWeb, confidence: ConfidenceMedium},
		{name: "bun hono template with only a dev script", files: map[string]string{
			"package.json": `{"name":"hono-app","scripts":{"dev":"bun run --hot src/index.ts"},"dependencies":{"hono":"^4.7.0"},"devDependencies":{"@types/bun":"latest"}}`,
			"bun.lock":     `{"lockfileVersion":1,"workspaces":{"":{"name":"hono-app","dependencies":{"hono":"^4.7.0"},"devDependencies":{"@types/bun":"latest"}}},"packages":{}}`,
			"src/index.ts": "import { Hono } from 'hono'\nconst app = new Hono()\napp.get('/', (c) => c.text('Hello Hono!'))\nexport default app\n",
		}, framework: "hono", start: "bun src/index.ts", port: 3000, profile: ProfileWeb, confidence: ConfidenceMedium, evidence: "start derived from the dev script"},
		{name: "elysia template with only a dev script", files: map[string]string{
			"package.json": `{"name":"app","scripts":{"dev":"bun run --watch src/index.ts"},"dependencies":{"elysia":"^1.3.0"}}`,
			"bun.lock":     `{"lockfileVersion":1,"workspaces":{"":{"name":"app","dependencies":{"elysia":"^1.3.0"}}},"packages":{}}`,
			"src/index.ts": "import { Elysia } from 'elysia'\nconst app = new Elysia().get('/', () => 'Hello').listen(3000)\n",
		}, framework: "elysia", start: "bun src/index.ts", port: 3000, profile: ProfileWeb, confidence: ConfidenceMedium},
		{name: "a bun-style entry installed with npm", files: map[string]string{
			"package.json": `{"scripts":{"dev":"tsx watch src/index.ts"},"dependencies":{"hono":"^4.7.0"},"devDependencies":{"tsx":"^4.19.0"}}`,
			"src/index.ts": "import { Hono } from 'hono'\nconst app = new Hono()\nexport default app\n",
		}, framework: "hono", start: "npx tsx src/index.ts", port: 3000, profile: ProfileWeb, confidence: ConfidenceMedium, decision: "only Bun serves"},
		{name: "a server entry found by its name", files: map[string]string{
			"package.json": `{"dependencies":{"express":"^4.21.0"}}`,
			"server.js":    "const app = require('express')()\napp.listen(process.env.PORT || 3000)\n",
		}, framework: "express", start: "node server.js", port: 3000, profile: ProfileWeb, confidence: ConfidenceMedium, evidence: "server entry server.js"},
		{name: "express serving a vite client", files: map[string]string{
			"package.json":   `{"type":"module","scripts":{"dev":"NODE_ENV=development tsx server/index.ts","build":"vite build && esbuild server/index.ts --platform=node --packages=external --bundle --format=esm --outdir=dist","start":"NODE_ENV=production node dist/index.js"},"dependencies":{"express":"^4.21.2"},"devDependencies":{"vite":"^5.4.14","esbuild":"^0.25.0","tsx":"^4.19.0"}}`,
			"vite.config.ts": "export default defineConfig({ root: path.resolve(import.meta.dirname, 'client'), build: { outDir: path.resolve(import.meta.dirname, 'dist/public') } })",
		}, framework: "express", start: "npm run start", port: 3000, profile: ProfileWeb, confidence: ConfidenceMedium, evidence: "vite builds the client"},
		{name: "vite only for tests beside a server", files: map[string]string{
			"package.json": `{"main":"src/server.js","scripts":{"test":"vitest"},"dependencies":{"fastify":"^5.0.0"},"devDependencies":{"vite":"^6.0.0","vitest":"^3.0.0"}}`,
		}, framework: "fastify", start: "node src/server.js", port: 3000, profile: ProfileWeb, confidence: ConfidenceMedium, evidence: "no index.html or vite.config"},
		{name: "a vite spa beside an api dependency stays a site", files: map[string]string{
			"package.json":   `{"scripts":{"build":"vite build","start":"vite preview"},"dependencies":{"express":"^4.21.0"},"devDependencies":{"vite":"^6.0.0"}}`,
			"vite.config.ts": "export default {}",
			"index.html":     "<div id=app></div>",
		}, framework: "vite", output: "dist", port: 80, profile: ProfileStatic, confidence: ConfidenceHigh, spa: true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			files := fixture.files
			if _, bun := files["bun.lock"]; !bun {
				files = withLockfile(files)
			}
			result := detectFixture(t, files)
			fixture.check(t, fixtureCandidate(t, result, BuildRecipe))
		})
	}
}

// A CMS or commerce stack is wired the way its own documentation deploys
// it: Payload's database adapter names its engine and the variable its
// configuration reads, and Medusa's admin and sign-in origins are the
// deployment's own domain.
func TestHeadlessStacksAreWiredToTheirDatabasesAndOrigins(t *testing.T) {
	t.Parallel()
	result := detectFixture(t, withLockfile(map[string]string{
		"package.json":          `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"15.4.0","payload":"3.40.0","@payloadcms/next":"3.40.0","@payloadcms/db-postgres":"3.40.0"}}`,
		"src/payload.config.ts": `export default buildConfig({ secret: process.env.PAYLOAD_SECRET || "", db: postgresAdapter({ pool: { connectionString: process.env.DATABASE_URI || "" } }) })`,
	}))
	payload := fixtureCandidate(t, result, BuildRecipe)
	if len(payload.Databases) != 1 || payload.Databases[0].Engine != "postgres" || payload.Databases[0].Variable != "DATABASE_URI" {
		t.Fatalf("payload databases = %+v", payload.Databases)
	}
	result = detectFixture(t, withLockfile(map[string]string{
		"package.json":     `{"scripts":{"build":"medusa build","start":"medusa start"},"dependencies":{"@medusajs/medusa":"2.8.4","@medusajs/framework":"2.8.4"}}`,
		"medusa-config.ts": "module.exports = defineConfig({ projectConfig: { databaseUrl: process.env.DATABASE_URL } })",
	}))
	medusa := fixtureCandidate(t, result, BuildRecipe)
	if len(medusa.Databases) == 0 || medusa.Databases[0].Engine != "postgres" {
		t.Fatalf("medusa databases = %+v", medusa.Databases)
	}
	for _, name := range []string{"ADMIN_CORS", "AUTH_CORS"} {
		index := slices.IndexFunc(medusa.Variables, func(variable DetectedVariable) bool { return variable.Name == name })
		if index < 0 || medusa.Variables[index].Setup != "domain" || medusa.Variables[index].DomainTemplate != "{{scheme}}://{{hostname}}" {
			t.Fatalf("%s = %+v", name, medusa.Variables)
		}
	}
}
