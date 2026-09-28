package deploy

import (
	"context"
	"strings"
	"testing"
)

// A JavaScript framework's static output goes from detection to the image
// through two readings: the framework's catalogue entry (frameworks_node.go)
// decides the output directory, the build and the page a single-page site
// falls back to, and the static server (build_static_serving.go) serves
// every output with clean URLs, the site's 404 page, the sub-path its
// configuration exports and the rules another host's files declare. These
// cases follow a repository through both, with the plan quick setup saves.

// prepareDetectedNode detects files and prepares detection's own plan.
func prepareDetectedNode(t *testing.T, files map[string]string) (DetectionResult, DetectedCandidate, PreparedBuild) {
	t.Helper()
	result, candidate := detectNodeTree(t, files)
	if candidate.Recipe != "node" {
		t.Fatalf("candidate = %+v", candidate)
	}
	config := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", PackageManager: candidate.PackageManager,
		BuildCommand: candidate.BuildCommand, StartCommand: candidate.StartCommand,
		OutputDirectory: candidate.OutputDirectory, SPAFallback: candidate.SPAFallback}
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), writeNodeTree(t, files), config, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	return result, candidate, prepared
}

func TestNextExportIsServedByTheStaticServerUnderItsBasePath(t *testing.T) {
	t.Parallel()
	files := withLockfile(map[string]string{
		"package.json": `{"scripts":{"build":"next build"},"dependencies":{"next":"16.0.0","react":"19.2.0","react-dom":"19.2.0"}}`,
		// The base is a key of the exported object; the sidebar's is not.
		"next.config.mjs":   "const nav = { basePath: '/wrong' }\nexport default { output: 'export', basePath: '/docs', images: { unoptimized: true } }\n",
		"app/page.jsx":      "export default function Page() { return <h1>home</h1> }",
		"public/_redirects": "/old  /about  301\n",
		"public/_headers":   "/*\n  X-Frame-Options: DENY\n",
	})
	result, candidate, prepared := prepareDetectedNode(t, files)
	if candidate.Framework != "nextjs" || candidate.OutputDirectory != "out" || candidate.StartCommand != "" || candidate.SPAFallback ||
		candidate.StaticSite == nil || candidate.StaticSite.BasePath != "/docs" || candidate.StaticSite.HostingRules != 2 {
		t.Fatalf("candidate = %+v / %+v", candidate, candidate.StaticSite)
	}
	assertDockerfile(t, prepared.DockerfilePreview, []string{
		"RUN npm run build",
		"COPY --from=build /app/out/ /usr/share/nginx/html/docs/",
		// /about is out/about.html, which Next.js writes beside out/about/.
		"        try_files $uri $uri.html $uri/ =404;",
		"    location = / {'",
		"        return 302 /docs/;",
		"    error_page 404 /docs/404.html;",
		// The rules were written for the site's root and move under its base.
		"    location = /docs/old {'",
		"        return 301 /docs/about$is_args$args;",
		`    add_header X-Frame-Options "DENY" always;`,
		"    absolute_redirect off;",
	}, []string{"next start", "/wrong", "/__spa-fallback.html"})

	findings := preflightFindings(nodeDraft(result), nodeTestConfiguration(BuildPlanConfig{Method: BuildRecipe, Recipe: "node",
		BuildCommand: candidate.BuildCommand, OutputDirectory: candidate.OutputDirectory}), dockerHost, false)
	if base := findingByCode(findings, "static_base_path"); base == nil || !strings.Contains(base.Measured, "/docs") {
		t.Fatalf("static_base_path = %+v", base)
	}
	for _, code := range []string{"next_export_images", "start_command_missing", "recipe_unsupported"} {
		if item := findingByCode(findings, code); item != nil {
			t.Fatalf("%s = %+v", code, item)
		}
	}
}

func TestSvelteKitStaticAdapterFallsBackToItsOwnPageUnderItsBase(t *testing.T) {
	t.Parallel()
	files := withLockfile(map[string]string{
		"package.json":     `{"type":"module","scripts":{"build":"vite build"},"devDependencies":{"@sveltejs/kit":"^2.20.0","@sveltejs/adapter-static":"^3.0.0","vite":"^6.0.0"}}`,
		"svelte.config.js": "import adapter from '@sveltejs/adapter-static';\n// adapter({ fallback: 'index.html' })\nexport default { kit: { adapter: adapter({ fallback: '200.html' }), paths: { base: '/app' } } };\n",
		"api/hello.js":     "export default function handler(req, res) { res.end('hi') }",
	})
	_, candidate, prepared := prepareDetectedNode(t, files)
	if candidate.Framework != "sveltekit" || candidate.OutputDirectory != "build" || !candidate.SPAFallback ||
		candidate.StaticSite == nil || candidate.StaticSite.BasePath != "/app" || !evidenceMentions(&candidate, "200.html") {
		t.Fatalf("candidate = %+v / %+v", candidate, candidate.StaticSite)
	}
	assertDockerfile(t, prepared.DockerfilePreview, []string{
		"COPY --from=build /app/build/ /usr/share/nginx/html/app/",
		// adapter-static's page is tried before index.html, both under the base.
		"        try_files $uri $uri.html $uri/ /app/200.html /app/index.html;",
		"        return 302 /app/;",
		// A function another host ran answers 404, not the application's page.
		"    location ~ ^/api(?:/|$) {'",
	}, []string{"node build"})

	// A host's rule that rewrites every path to one page is what the site
	// was served with there, so it wins over the framework's.
	files["static/_redirects"] = "/*  /shell.html  200\n"
	_, candidate, prepared = prepareDetectedNode(t, files)
	assertDockerfile(t, prepared.DockerfilePreview, []string{"        try_files $uri $uri.html $uri/ /app/shell.html;"}, []string{"200.html"})
}

func TestReactRouterSPAModeIsServedFromItsShell(t *testing.T) {
	t.Parallel()
	_, candidate, prepared := prepareDetectedNode(t, withLockfile(map[string]string{
		"package.json":           `{"scripts":{"build":"react-router build"},"dependencies":{"react-router":"^7.6.0"},"devDependencies":{"@react-router/dev":"^7.6.0","vite":"^6.0.0"}}`,
		"react-router.config.ts": "export default { ssr: false, prerender: ['/'] }",
	}))
	if candidate.Framework != "react-router" || candidate.OutputDirectory != "build/client" || !candidate.SPAFallback {
		t.Fatalf("candidate = %+v", candidate)
	}
	// The shell exists only when "/" was prerendered, so index.html follows it.
	assertDockerfile(t, prepared.DockerfilePreview, []string{
		"COPY --from=build /app/build/client/ /usr/share/nginx/html/",
		"        try_files $uri $uri.html $uri/ /__spa-fallback.html /index.html;",
	}, []string{"react-router-serve"})
}

func TestSiteGeneratorsOnTheNodeCatalogueBuildWithTheirOwnBinary(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, lockfile, build, output string
		files                         map[string]string
	}{
		{name: "eleventy on npm", lockfile: "package-lock.json", build: "npx eleventy", output: "dist",
			files: map[string]string{"package.json": `{"devDependencies":{"@11ty/eleventy":"3.1.2"}}`,
				"eleventy.config.js": "export default function () { return { dir: { input: 'src', output: 'dist' } } }", "src/index.md": "# home"}},
		{name: "eleventy on pnpm", lockfile: "pnpm-lock.yaml", build: "pnpm exec eleventy", output: "_site",
			files: map[string]string{"package.json": `{"devDependencies":{"@11ty/eleventy":"3.1.2"}}`, "index.md": "# home"}},
		{name: "hexo on yarn", lockfile: "yarn.lock", build: "yarn hexo generate", output: "public",
			files: map[string]string{"package.json": `{"dependencies":{"hexo":"^7.3.0"}}`, "_config.yml": "title: blog\n", "source/_posts/a.md": ""}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			files := test.files
			if _, ok := files[test.lockfile]; !ok {
				files[test.lockfile] = ""
			}
			_, candidate := detectNodeTree(t, files)
			if candidate.BuildCommand != test.build || candidate.OutputDirectory != test.output || candidate.StartCommand != "" ||
				len(candidate.NeedsDecision) != 0 {
				t.Fatalf("candidate = %+v", candidate)
			}
		})
	}
	// The binary runs in the build stage and nginx serves what it wrote.
	_, candidate, prepared := prepareDetectedNode(t, withLockfile(map[string]string{
		"package.json": `{"devDependencies":{"@11ty/eleventy":"3.1.2"}}`, "index.md": "# home", "404.md": "missing",
	}))
	assertDockerfile(t, prepared.DockerfilePreview, []string{
		"RUN npx eleventy\n",
		"COPY --from=build /app/_site/ /usr/share/nginx/html/",
		"        try_files $uri $uri.html $uri/ =404;",
		"    error_page 404 /404.html;",
	}, nil)
	if candidate.Framework != "eleventy" {
		t.Fatalf("candidate = %+v", candidate)
	}
}
