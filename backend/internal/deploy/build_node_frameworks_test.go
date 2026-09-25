package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// What the recipe renders around a framework's build for the plan's own
// commands: an adapter substituted, a preset overridden, standalone assets
// copied, a site served under its base path, the entry a start command runs
// checked after the build.
func TestNodeRecipeRendersTheFrameworksServing(t *testing.T) {
	t.Parallel()
	lock := func(files map[string]string) map[string]string { return withLockfile(files) }
	for _, test := range []struct {
		name        string
		files       map[string]string
		config      BuildPlanConfig
		want, avoid []string
	}{
		{
			name: "next export under its basePath, with clean URLs",
			files: lock(map[string]string{
				"package.json":    `{"scripts":{"build":"next build"},"dependencies":{"next":"16.0.0"}}`,
				"next.config.mjs": "export default { output: 'export', basePath: '/docs', trailingSlash: false }",
			}),
			config: BuildPlanConfig{BuildCommand: "npm run build", OutputDirectory: "out"},
			want: []string{
				"COPY --from=build /app/out/ /usr/share/nginx/html/docs/",
				"try_files $uri $uri.html $uri/ =404;", "return 302 /docs/;", "absolute_redirect off;", `location ~ /\.(?!well-known/)`,
			},
			avoid: []string{"next start"},
		},
		{
			name: "an output directory the framework does not write keeps nginx's own configuration",
			files: lock(map[string]string{
				"package.json":    `{"scripts":{"build":"next build && cp -r out site"},"dependencies":{"next":"16.0.0"}}`,
				"next.config.mjs": "export default { output: 'export', basePath: '/docs' }",
			}),
			config: BuildPlanConfig{BuildCommand: "npm run build", OutputDirectory: "site"},
			want:   []string{"COPY --from=build /app/site/ /usr/share/nginx/html/"},
			avoid:  []string{"/docs"},
		},
		{
			name: "vite base path single-page site",
			files: lock(map[string]string{
				"package.json":   `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"^6.0.0"}}`,
				"vite.config.js": "export default { base: '/app/' }",
			}),
			config: BuildPlanConfig{BuildCommand: "npm run build", OutputDirectory: "dist", SPAFallback: true},
			want:   []string{"COPY --from=build /app/dist/ /usr/share/nginx/html/app/", "try_files $uri $uri/ /app/index.html;"},
		},
		{
			name: "sveltekit static adapter's 200.html fallback",
			files: lock(map[string]string{
				"package.json":     `{"type":"module","scripts":{"build":"vite build"},"devDependencies":{"@sveltejs/kit":"^2.20.0","@sveltejs/adapter-static":"^3.0.0"}}`,
				"svelte.config.js": "import adapter from '@sveltejs/adapter-static';\nexport default { kit: { adapter: adapter({ fallback: '200.html' }) } };",
			}),
			config: BuildPlanConfig{BuildCommand: "npm run build", OutputDirectory: "build", SPAFallback: true},
			want:   []string{"try_files $uri $uri.html $uri/ /200.html /index.html;"},
		},
		{
			name: "react router spa mode answers from its prerendered shell",
			files: lock(map[string]string{
				"package.json":           `{"scripts":{"build":"react-router build"},"dependencies":{"react-router":"^7.6.0"},"devDependencies":{"@react-router/dev":"^7.6.0"}}`,
				"react-router.config.ts": "export default { ssr: false, prerender: ['/'] }",
			}),
			config: BuildPlanConfig{BuildCommand: "npm run build", OutputDirectory: "build/client", SPAFallback: true},
			want:   []string{"try_files $uri $uri/ /__spa-fallback.html /index.html;"},
		},
		{
			name: "next standalone started from its server.js",
			files: lock(map[string]string{
				"package.json":   `{"scripts":{"build":"next build","start":"node .next/standalone/server.js"},"dependencies":{"next":"15.3.0"}}`,
				"next.config.js": "module.exports = { output: 'standalone' }",
			}),
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "npm run start"},
			want: []string{
				"RUN test -f /app/.next/standalone/server.js",
				"RUN mkdir -p .next/standalone/.next && cp -r .next/static .next/standalone/.next/ && if [ -d public ]; then cp -r public .next/standalone/; fi",
				"ENV HOSTNAME=0.0.0.0",
			},
		},
		{
			name: "sveltekit adapter-auto built with adapter-node",
			files: lock(map[string]string{
				"package.json":     `{"type":"module","scripts":{"build":"vite build"},"devDependencies":{"@sveltejs/kit":"^2.20.0","@sveltejs/adapter-auto":"^6.0.0"}}`,
				"svelte.config.js": sveltekitAuto,
			}),
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "node build"},
			want: []string{
				"RUN npm install --no-save --no-audit --no-fund --legacy-peer-deps @sveltejs/adapter-node@5.5.7",
				`RUN mv svelte.config.js svelte.config.user.js && printf '%s\n' "import config from './svelte.config.user.js';"`,
				"RUN test -f /app/build/index.js",
			},
		},
		{
			name: "sveltekit adapter-auto in svelte.config.mjs",
			files: lock(map[string]string{
				"package.json":      `{"type":"module","scripts":{"build":"vite build"},"devDependencies":{"@sveltejs/kit":"^2.20.0","@sveltejs/adapter-auto":"^6.0.0"}}`,
				"svelte.config.mjs": sveltekitAuto,
			}),
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "node build"},
			want: []string{
				`RUN mv svelte.config.mjs svelte.config.user.mjs && printf '%s\n' "import config from './svelte.config.user.mjs';"`,
				`> svelte.config.js`,
			},
		},
		{
			name: "sveltekit adapter-node's out directory is the entry checked",
			files: lock(map[string]string{
				"package.json":     `{"type":"module","scripts":{"build":"vite build"},"devDependencies":{"@sveltejs/kit":"^2.20.0","@sveltejs/adapter-node":"^5.2.0"}}`,
				"svelte.config.js": "import adapter from '@sveltejs/adapter-node';\nexport default { kit: { adapter: adapter({ out: 'server' }) } };",
			}),
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "node server"},
			want:   []string{"RUN test -f /app/server/index.js"},
		},
		{
			name: "sveltekit 1 with adapter-node installed needs only the wrapper",
			files: lock(map[string]string{
				"package.json":     `{"type":"module","scripts":{"build":"vite build"},"devDependencies":{"@sveltejs/kit":"^1.30.0","@sveltejs/adapter-auto":"^2.0.0","@sveltejs/adapter-node":"^1.3.1"}}`,
				"svelte.config.js": sveltekitAuto,
			}),
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "node build"},
			want:   []string{"RUN mv svelte.config.js svelte.config.user.js"},
			avoid:  []string{"npm install --no-save"},
		},
		{
			name: "nuxt provider preset built as a node server",
			files: lock(map[string]string{
				"package.json":   `{"scripts":{"build":"nuxt build"},"dependencies":{"nuxt":"^3.15.0"}}`,
				"nuxt.config.ts": "export default defineNuxtConfig({ nitro: { preset: 'netlify' } })",
			}),
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "node .output/server/index.mjs"},
			want:   []string{`RUN export NITRO_PRESET="${NITRO_PRESET:-node-server}" && npm run build`, "RUN test -f /app/.output/server/index.mjs"},
		},
		{
			name: "strapi builds its admin for production",
			files: lock(map[string]string{
				"package.json": `{"scripts":{"build":"strapi build","start":"strapi start"},"dependencies":{"@strapi/strapi":"5.12.0"}}`,
			}),
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "npm run start"},
			want:   []string{`RUN export NODE_ENV="${NODE_ENV:-production}" && npm run build`},
		},
		{
			name: "nest's entry is checked where tsc writes it",
			files: lock(map[string]string{
				"package.json":     `{"scripts":{"build":"nest build","start:prod":"node dist/main"},"dependencies":{"@nestjs/core":"^11.0.0"}}`,
				"prisma.config.ts": "export default {}",
				"src/main.ts":      "bootstrap()",
			}),
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "node dist/src/main"},
			want:   []string{"RUN test -f /app/dist/src/main.js"},
		},
		{
			name: "an entry run through a package script is still checked",
			files: lock(map[string]string{
				"package.json": `{"scripts":{"build":"ng build","serve:ssr:my-app":"node dist/my-app/server/server.mjs"},"dependencies":{"@angular/core":"^20.0.0","@angular/ssr":"^20.0.0"}}`,
				"angular.json": angularWorkspace,
			}),
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "npm run serve:ssr:my-app"},
			want:   []string{"RUN test -f /app/dist/my-app/server/server.mjs"},
		},
		{
			name: "adonisjs serves what node ace build writes",
			files: lock(map[string]string{
				"package.json": `{"type":"module","scripts":{"build":"node ace build"},"dependencies":{"@adonisjs/core":"^6.17.0"}}`,
			}),
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "node build/bin/server.js"},
			want:   []string{"RUN test -f /app/build/bin/server.js", `CMD ["/bin/sh","-c","node build/bin/server.js"]`},
		},
		{
			name: "medusa's server directory is checked",
			files: lock(map[string]string{
				"package.json":     `{"scripts":{"build":"medusa build"},"dependencies":{"@medusajs/medusa":"2.8.4"}}`,
				"medusa-config.ts": "export default {}",
			}),
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "cd .medusa/server && medusa db:migrate && medusa start"},
			want:   []string{"RUN test -f /app/.medusa/server/package.json"},
		},
		{
			name: "bash is installed where a command calls it",
			files: lock(map[string]string{
				"package.json": `{"scripts":{"build":"bash scripts/build.sh","start":"bash scripts/start.sh"},"dependencies":{"express":"^4.21.0"}}`,
			}),
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "npm run start"},
			want:   []string{"AS build\nRUN apk add --no-cache bash\nWORKDIR /app", "RUN npm run build\nFROM node:22-alpine@", "\nRUN apk add --no-cache bash\nWORKDIR /app\nENV NODE_ENV=production"},
		},
		{
			name: "the Node version chosen in Build settings",
			files: lock(map[string]string{
				"package.json": `{"engines":{"node":">=20"},"scripts":{"start":"node server.js"},"dependencies":{"express":"^4.21.0"}}`,
				".nvmrc":       "20\n",
			}),
			config: BuildPlanConfig{StartCommand: "npm run start", NodeVersion: "24"},
			want:   []string{"FROM node:24-alpine@"},
			avoid:  []string{"node:20-alpine"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			prepared := prepareNode(t, test.files, test.config)
			assertDockerfile(t, prepared.DockerfilePreview, test.want, test.avoid)
		})
	}
}

// A command that reaches another language's toolchain is refused before the
// build, naming the field that runs it, and a Bun command on a JavaScript
// file moves to node with the rest of the command.
func TestNodeRecipeRefusesRunnersNoStageProvides(t *testing.T) {
	t.Parallel()
	root := writeNodeTree(t, withLockfile(map[string]string{
		"package.json": `{"scripts":{"build":"python3 build.py","start":"node server.js"},"dependencies":{"express":"^4.21.0"}}`,
	}))
	_, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root,
		BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "npm run build", StartCommand: "npm run start"}, false, "t:1")
	if !errors.Is(err, ErrUnsupportedBuilder) || !strings.Contains(err.Error(), "python3") {
		t.Fatalf("err = %v", err)
	}
	// A native addon brings python3 into the build stage, so the build may
	// call it; the server's stage never has it.
	root = writeNodeTree(t, withLockfile(map[string]string{
		"package.json": `{"scripts":{"build":"python3 build.py","start":"php -S 0.0.0.0:3000"},"dependencies":{"bcrypt":"^5.1.0"}}`,
	}))
	source, err := readNodeInstallSource(root, "", "x64", newNodeReadBudget())
	if err != nil {
		t.Fatal(err)
	}
	if runner, field := nodeForeignRunner(source.facts, "npm run build", "npm run start"); runner != "php" || field != "configuration.build.startCommand" {
		t.Fatalf("foreign runner = %q %q", runner, field)
	}
	if runner, _ := nodeForeignRunner(source.facts, "npm run build", "node server.js"); runner != "" {
		t.Fatalf("python3 with compilers refused: %q", runner)
	}
	for _, test := range []struct{ command, manager, want string }{
		{"bun ./build/index.js", "npm", "node ./build/index.js"},
		{"bun run dist/server.mjs", "pnpm", "node dist/server.mjs"},
		{"bun src/index.ts", "npm", "bun src/index.ts"},
		{"bun run src/index.ts", "npm", "bun run src/index.ts"},
		{"bun ./build/index.js", "bun", "bun ./build/index.js"},
		{"npx prisma migrate deploy && bun build/index.js", "yarn", "yarn prisma migrate deploy && node build/index.js"},
	} {
		if got := nodeRunnerFor(test.command, test.manager); got != test.want {
			t.Errorf("nodeRunnerFor(%q, %q) = %q, want %q", test.command, test.manager, got, test.want)
		}
	}
}
