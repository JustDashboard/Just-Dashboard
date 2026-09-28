package deploy

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLiveDetectedFrameworkBuildAndServing(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 for real framework builds and serving")
	}
	client := liveC4Docker(t)
	builder := NewArtifactBuilder(NewDockerArtifactBackend(client))
	owner := NewDockerRuntimeOwner(client)
	// The catalogue fixtures are each framework's own starter layout: Astro
	// static, Nuxt 4 and React Router 8 servers, and three Python shapes — a
	// FastAPI app on an unpinned requirements.txt with no server declared, a
	// Flask factory on a bare pyproject, and a Django project whose migrations
	// must run before its first request can be answered.
	// The compiled fixtures are an axum service, a plain Maven jar and a plain
	// Gradle jar (their ports confirmed the way the form would), an ASP.NET
	// Core minimal API and a Deno server, each read for its toolchain and
	// served from its own image. Streamlit and Gradio are the two data-app
	// shapes: a script the framework's own server runs, answering on the
	// framework's port with the framework's own page.
	// next-pnpm and express-yarn are the same kind of server installed by
	// pnpm (a toolchain release the start command runs offline) and by
	// Yarn 1 (its real frozen install), started through the manager.
	// The framework configurations detection reads: a Next.js static export
	// under a base path, a standalone server started from its server.js, a
	// SvelteKit app on adapter-auto built with adapter-node, Express serving
	// the Vite client its build writes, and a Hono starter with only a dev
	// script, on Bun. react-router-spa prerenders "/" in SPA mode, so every
	// other path must answer with the shell it writes beside index.html.
	// The site generators build on their own images — Hugo, Zola and mdBook
	// as themselves, Jekyll on Ruby, MkDocs on Python, Lume on Deno,
	// Eleventy from its own binary with no build script — and nginx serves
	// what they wrote with clean URLs and the site's own 404 page.
	// static-rules is a plain site whose _redirects, _headers and
	// netlify.toml repeat and overlap each other's rules, as a site moved
	// between hosts carries them: nginx has to start on what they become.
	// laravel-vite is Laravel 13 with Vite and Wayfinder, whose plugin runs
	// php artisan while the assets build, served behind a forwarded HTTPS;
	// symfony is an AssetMapper application whose committed .env says dev
	// and whose DebugBundle is require-dev.
	// The Python install shapes: a Litestar app on a pdm.lock started by its
	// [tool.pdm.scripts] task, Flask on a Pipfile.lock, FastAPI on a uv.lock
	// that asks for Python 3.14, a Django project created inside the
	// repository with a requirements/ folder, split settings and psycopg2
	// compiled against the libpq the recipe installs, and a Flask app whose
	// JavaScript a Node stage bundles.
	// java-reactor is a Spring Boot module of a Maven reactor on Java 17 and
	// gradle-multiproject an application-plugin project of a Gradle build
	// with a version catalog, each built from the build's root;
	// dotnet-solution a web project in a solution's src/ with shared props,
	// central package versions and a global.json pin; blazor-wasm a Blazor
	// WebAssembly app served as static files; fsharp an F# minimal API; and
	// dotnet-spa an ASP.NET Core project whose publish runs npm for its
	// front end. gradle-composite is a Gradle build whose settings root is
	// below the checkout's and includes a build beside it, and
	// dotnet-multitarget a web project with two target frameworks that
	// references a library with one.
	// The Go and Rust shapes beyond one crate or module (compiledLiveFixtures)
	// build a member of a go.work or a Cargo workspace from its workspace,
	// a Go server embedding its Vite build with cgo SQLite and templ, and the
	// Leptos and Trunk WebAssembly builds.
	for _, name := range append([]string{"vite", "next", "svelte-node", "svelte-static", "html", "containerfile", "go",
		"astro", "nuxt", "react-router", "fastapi", "flask", "django", "rust", "java", "gradle", "dotnet", "deno", "laravel", "php",
		"streamlit", "gradio", "next-pnpm", "express-yarn",
		"next-export", "next-standalone", "svelte-auto", "express-vite", "hono-bun", "react-router-spa",
		"hugo", "zola", "mdbook", "jekyll", "mkdocs", "lume", "eleventy", "static-rules", "laravel-vite", "symfony",
		"python-pdm", "python-pipenv", "python-uv", "django-nested", "flask-assets",
		"java-reactor", "gradle-multiproject", "dotnet-solution", "blazor-wasm", "fsharp", "dotnet-spa",
		"gradle-composite", "dotnet-multitarget", "rails", "sinatra", "phoenix", "play", "clojure", "gleam"},
		compiledLiveFixtures...) {
		t.Run(name, func(t *testing.T) {
			timeout := 10 * time.Minute
			if slices.Contains(compiledLiveFixtures, name) {
				// A WebAssembly build installs its tool from source first.
				timeout = 45 * time.Minute
			}
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			root := t.TempDir()
			value := "https://" + name + ".build-value.test"
			if name == "go" {
				writeBuildFixture(t, root, "go.mod", "module fixture\ngo 1.26.8\n")
				writeBuildFixture(t, root, "main.go", `package main
import ("fmt"; "log"; "net/http"; "os"; "runtime")
//go:generate go run ./cmd/generate
func main() { http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, generated + " " + os.Args[1] + " " + runtime.Version()) }); log.Fatal(http.ListenAndServe(":8080", nil)) }
`)
				writeBuildFixture(t, root, "cmd/generate/main.go", fmt.Sprintf("package main\nimport \"os\"\nfunc main() { if err := os.WriteFile(\"generated.go\", []byte(%q), 0600); err != nil { panic(err) } }\n", "package main\nconst generated = \""+value+"\"\n"))
			} else if name == "html" {
				writeBuildFixture(t, root, "public/index.html", "<h1>"+value+"</h1>")
			} else if name == "containerfile" {
				writeBuildFixture(t, root, "Containerfile", "FROM caddy:2-alpine\nCOPY index.html /srv/index.html\nEXPOSE 8080\nCMD [\"caddy\",\"file-server\",\"--listen\",\":8080\",\"--root\",\"/srv\"]\n")
				writeBuildFixture(t, root, "index.html", "<h1>"+value+"</h1>")
			} else {
				copyFrameworkFixture(t, name, root)
			}
			if name == "symfony" {
				// The .env the Symfony skeleton commits, which says dev; it is
				// written here because this repository never commits a .env.
				writeBuildFixture(t, root, ".env", "APP_ENV=dev\nAPP_SECRET=\n")
			}
			detection, err := (Detector{}).DetectPath(ctx, root, SourceIdentity{})
			if err != nil {
				t.Fatal(err)
			}
			candidate := selectedDetectionCandidate(&detection)
			if candidate == nil {
				t.Fatalf("no unambiguous quick-setup candidate: %+v", detection.Candidates)
			}
			config := BuildPlanConfig{Method: candidate.BuildMethod, Recipe: candidate.Recipe, Dockerfile: candidate.Dockerfile,
				BuildCommand: candidate.BuildCommand, StartCommand: candidate.StartCommand, OutputDirectory: candidate.OutputDirectory,
				SPAFallback: candidate.SPAFallback}
			if name == "go" {
				config.GoVersion, config.BuildCommand, config.StartCommand = "1.26.8", "go generate ./... && go build -o /out/app .", "/app custom-start"
				candidate.Port = 8080
			}
			if name == "java" || name == "gradle" || name == "gradle-multiproject" || name == "gradle-composite" {
				candidate.Port = 8080
			}
			variables := map[string]string{}
			secret := fmt.Sprintf("jd-framework-install-%d", time.Now().UnixNano())
			if config.Method == BuildRecipe {
				config.Secrets = []BuildSecretConfig{{Variable: "PACKAGE_TOKEN", Step: "install"}}
				variables = map[string]string{"PACKAGE_TOKEN": secret, "NEXT_PUBLIC_API_URL": value, "VITE_API_URL": value, "PUBLIC_API_URL": value}
				if name == "mdbook" {
					// mdBook reads its configuration from MDBOOK_ variables.
					variables["MDBOOK_BOOK__TITLE"] = value
				}
			}
			stamp := time.Now().UnixNano()
			tag := fmt.Sprintf("jd-framework-test:%s-%d", name, stamp)
			t.Cleanup(func() { _, _ = liveDockerOutput(context.Background(), "image", "rm", "--force", tag) })
			logs := []string{}
			// Within the checkout, as prepare_context prepares it: a module
			// or a solution's project builds from the root that owns it, and a
			// workspace member from its workspace.
			result := livePrepareAndBuildWithin(t, builder, root, candidate.Root, tag, config, variables, func(line BuildLog) error { logs = append(logs, line.Text); return nil })
			if config.Method == BuildRecipe {
				assertLiveArtifactSecretFree(t, tag, secret, result, logs)
			}
			plan := RuntimePlanConfig{InternalPort: candidate.Port, BindAddress: "127.0.0.1", Strategy: StrategyStopFirst, GracePeriodSeconds: 3}
			if plan.InternalPort == 0 {
				t.Fatal("detected HTTP application has no serving port")
			}
			run := EngineRun{ID: stamp, EnvironmentID: stamp}
			release := Release{ID: stamp, EnvironmentID: stamp, RunID: stamp, Number: 1}
			runtimeVariables := map[string]string{}
			// What the configure form supplies for a detected Python variable:
			// its default (the settings module a split Django project runs),
			// or a generated secret.
			for _, variable := range candidate.Variables {
				if candidate.Recipe != "python" {
					break
				}
				switch variable.Setup {
				case "default":
					runtimeVariables[variable.Name] = variable.DefaultValue
				case "generate":
					runtimeVariables[variable.Name] = fmt.Sprintf("generated-%d-%s", stamp, strings.Repeat("x", 40))
				}
			}
			if name == "hono-bun" {
				// A server with no build answers with what the runtime gives it.
				runtimeVariables = map[string]string{"API_URL": value}
			}
			if strings.HasPrefix(name, "laravel") {
				// What the configure form generates for a Laravel import: the
				// application key, and file-backed sessions since the fixture
				// ships no sessions table.
				runtimeVariables = map[string]string{"APP_KEY": "base64:" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))), "SESSION_DRIVER": "file", "CACHE_STORE": "file"}
			}
			// The secrets the configure form generates for these frameworks.
			switch name {
			case "rails", "phoenix":
				runtimeVariables = map[string]string{"SECRET_KEY_BASE": strings.Repeat("0123456789abcdef", 8)}
			case "play":
				runtimeVariables = map[string]string{"APPLICATION_SECRET": strings.Repeat("0123456789abcdef", 4)}
			}
			started, err := owner.StartCandidate(ctx, CandidateRuntimeRequest{Run: run, Release: release,
				Snapshot: runtimeReleaseSnapshot{Version: 1, Plan: plan, Image: result.Image}, RuntimeVariables: runtimeVariables, Host: "127.0.0.1", Port: liveC5LoopbackPort(t)}, nil)
			t.Cleanup(func() {
				_, _ = liveDockerOutput(context.Background(), "rm", "--force", "--volumes", fmt.Sprintf("jd-e%d-r1", stamp))
			})
			if err != nil {
				t.Fatal(err)
			}
			check := PlannedCheck{Name: "readiness", Kind: "http", Phase: "readiness", Required: true, Config: mustJSON(map[string]any{"path": "/", "attempts": 30, "timeoutSeconds": 2, "intervalSeconds": 1})}
			if evidence := NewCheckRunner(client).Run(ctx, check, started.Target); evidence.Outcome != HealthPassed {
				t.Fatalf("readiness: %+v", evidence)
			}
			base := fmt.Sprintf("http://127.0.0.1:%d", started.Input.Port)
			httpClient := &http.Client{Timeout: 5 * time.Second}
			fetch := func(path string) string {
				t.Helper()
				response, err := httpClient.Get(base + path)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				body, err := io.ReadAll(io.LimitReader(response.Body, 5<<20))
				if err != nil || response.StatusCode != http.StatusOK {
					t.Fatalf("serve %s: %d %v", path, response.StatusCode, err)
				}
				return string(body)
			}
			html := fetch("/")
			content := html
			// Gradio's page embeds the whole application config, the
			// configured value included, and declares a manifest script it
			// serves only to an installed app; the shell alone is the proof.
			walkAssets := name != "gradio" && name != "symfony"
			if name == "symfony" {
				// AssetMapper names its modules in an import map, beside a shim
				// served from a CDN.
				if match := regexp.MustCompile(`"app": "(/assets/app-[\w-]+\.js)"`).FindStringSubmatch(html); match != nil {
					content += fetch(match[1])
				}
			}
			if name == "streamlit" {
				// Streamlit renders its page over a websocket, so the shell
				// alone proves nothing about the script: the static file the
				// script's own configuration serves, and the health endpoint
				// the framework answers once the script loaded, do.
				if health := strings.TrimSpace(fetch("/_stcore/health")); health != "ok" {
					t.Fatalf("Streamlit health = %q", health)
				}
				content += fetch("/app/static/value.txt")
			}
			for _, match := range regexp.MustCompile(`(?:src|href)="([^"?#]+\.js)(?:[^" ]*)"`).FindAllStringSubmatch(html, -1) {
				if !walkAssets {
					break
				}
				path := strings.TrimPrefix(match[1], "./")
				if absolute, err := url.Parse(path); err == nil && absolute.Host != "" {
					// Laravel's @vite writes absolute URLs from the request.
					path = absolute.Path
				}
				if !strings.HasPrefix(path, "/") {
					path = "/" + path
				}
				if strings.HasPrefix(path, "//") {
					continue
				}
				content += fetch(path)
			}
			content += compiledLiveContent(t, name, html, fetch)
			if !strings.Contains(content, value) {
				t.Fatal("served application assets did not contain the configured build value")
			}
			// A site's other pages answer by their clean URLs, and a missing
			// one with the site's own 404 page.
			for _, path := range liveSitePaths[name] {
				fetch(path)
			}
			if page, ok := liveSiteMissingPages[name]; ok {
				response, err := httpClient.Get(base + "/no-such-page")
				if err != nil {
					t.Fatal(err)
				}
				body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
				_ = response.Body.Close()
				if response.StatusCode != http.StatusNotFound || !strings.Contains(string(body), page) {
					t.Fatalf("missing page: %d %q", response.StatusCode, body)
				}
			}
			if name == "static-rules" {
				assertLiveHostingRules(t, base)
			}
			if name == "laravel-vite" || name == "symfony" {
				// The platform proxy terminates TLS; behind it the page and its
				// asset URLs are https, and the client is the proxy's
				// last X-Forwarded-For hop.
				request, _ := http.NewRequest(http.MethodGet, base+"/", nil)
				request.Header.Set("X-Forwarded-Proto", "https")
				request.Header.Set("X-Forwarded-For", "203.0.113.7")
				response, err := httpClient.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
				response.Body.Close()
				want := []string{`<h1 id="api">https</h1>`, `https://127.0.0.1:`, "FROM vendor AS assets"}
				if name == "symfony" {
					want = []string{`<h1 id="api">prod https</h1>`, "asset-map:compile"}
				}
				if slices.ContainsFunc(want, func(text string) bool {
					return !strings.Contains(string(body), text) && !strings.Contains(result.Prepared.DockerfilePreview, text)
				}) {
					t.Fatalf("forwarded HTTPS was not honoured behind the proxy:\n%s", body)
				}
			}
			if name == "go" && (!strings.Contains(content, "custom-start go1.26.8") || result.Prepared.GoVersion != "1.26.8") {
				t.Fatalf("Go override/version behavior missing: %q", content)
			}
			if wantPython := map[string]string{"python-uv": "3.14", "python-pipenv": "3.12"}[name]; candidate.Recipe == "python" &&
				result.Prepared.PythonVersion != firstNonEmpty(wantPython, "3.13") {
				t.Fatalf("Python version not recorded: %+v", result.Prepared)
			}
			if name == "python-uv" && !strings.Contains(content, "<p>3.14</p>") {
				t.Fatalf("uv ran another interpreter than the image's: %q", content)
			}
			if name == "django-nested" && !strings.Contains(content, "users=0 psycopg2=2.9.10") {
				t.Fatalf("Django did not answer through its migrated database and compiled driver: %q", content)
			}
			if name == "django" && !strings.Contains(content, "users=0") {
				t.Fatalf("Django did not answer through its migrated database: %q", content)
			}
			if name == "astro" && candidate.Profile != ProfileStatic || name == "nuxt" && candidate.Port != 3000 || name == "react-router" && candidate.Framework != "react-router" ||
				name == "rust" && (candidate.Framework != "axum" || result.Prepared.Toolchain != "rust 1") ||
				name == "java" && result.Prepared.Toolchain != "java 21 (maven)" ||
				name == "gradle" && result.Prepared.Toolchain != "java 21 (gradle 8)" ||
				name == "java-reactor" && (candidate.Root != "app" || candidate.Framework != "spring-boot" || result.Prepared.Toolchain != "java 17 (maven)" || result.Prepared.ContextDirectory != ".") ||
				name == "gradle-multiproject" && (candidate.Root != "app" || result.Prepared.Toolchain != "java 21 (gradle 8)" || result.Prepared.ContextDirectory != ".") ||
				name == "gradle-composite" && (candidate.Root != "backend/app" || result.Prepared.Toolchain != "java 21 (gradle 8)" || result.Prepared.ContextDirectory != ".") ||
				name == "dotnet-multitarget" && (candidate.Root != "src/Api" || result.Prepared.Toolchain != "dotnet 9.0" || result.Prepared.ContextDirectory != "src") ||
				name == "dotnet-solution" && (candidate.Root != "src/Shop.Api" || candidate.Framework != "aspnet" || result.Prepared.Toolchain != "dotnet 10.0" || result.Prepared.ContextDirectory != ".") ||
				name == "blazor-wasm" && (candidate.Framework != "blazor-wasm" || candidate.Port != 80) ||
				name == "fsharp" && (candidate.Framework != "aspnet" || result.Prepared.Toolchain != "dotnet 10.0") ||
				name == "dotnet-spa" && (candidate.Framework != "aspnet" || !strings.Contains(result.Prepared.DockerfilePreview, "COPY --from=spa-node /usr/local/ /usr/local/")) ||
				name == "streamlit" && (candidate.Framework != "streamlit" || candidate.Port != 8501) ||
				name == "gradio" && (candidate.Framework != "gradio" || candidate.Port != 7860) ||
				name == "dotnet" && (candidate.Framework != "aspnet" || result.Prepared.Toolchain != "dotnet 10.0") ||
				name == "deno" && candidate.Port != 8000 ||
				name == "laravel" && (candidate.Framework != "laravel" || result.Prepared.Toolchain != "php 8.4") ||
				name == "php" && candidate.Framework != "php" ||
				name == "next-pnpm" && (candidate.StartCommand != "pnpm run start" || result.Prepared.Toolchain != "pnpm 10.34.5 (lockfileVersion 9.0)") ||
				name == "express-yarn" && (candidate.Framework != "express" || candidate.StartCommand != "yarn run start" ||
					result.Prepared.Install != "yarn install --frozen-lockfile") ||
				name == "hugo" && (candidate.Framework != "hugo" || result.Prepared.Toolchain != "hugo "+hugoDefaultVersion+" (extended)") ||
				name == "zola" && result.Prepared.Toolchain != "zola "+zolaDefaultVersion ||
				name == "mdbook" && (result.Prepared.Toolchain != "mdbook 0.5.4" || candidate.OutputDirectory != "book/html") ||
				name == "jekyll" && result.Prepared.Toolchain != "jekyll on ruby "+jekyllDefaultRuby ||
				name == "mkdocs" && (candidate.Recipe != "python" || candidate.Framework != "mkdocs") ||
				name == "lume" && (candidate.Recipe != "deno" || candidate.Framework != "lume") ||
				name == "eleventy" && (candidate.BuildCommand != "npx eleventy" || candidate.OutputDirectory != "dist") ||
				name == "svelte-static" && !candidate.SPAFallback ||
				name == "rails" && (candidate.Framework != "rails" || result.Prepared.Toolchain != "ruby 3.4.7" || candidate.Readiness == nil || candidate.Readiness.Path != "/up" ||
					!strings.Contains(content, "notes=0")) ||
				name == "sinatra" && (candidate.Framework != "sinatra" || candidate.Port != 4567) ||
				name == "phoenix" && (candidate.Framework != "phoenix" || candidate.Port != 4000 || !strings.HasPrefix(result.Prepared.Toolchain, "elixir 1.")) ||
				name == "play" && (candidate.Framework != "play" || candidate.Port != 9000 || !strings.HasPrefix(result.Prepared.Toolchain, "scala · sbt 1.x stage")) ||
				name == "clojure" && result.Prepared.Toolchain != "clojure · lein uberjar on java 21" ||
				name == "gleam" && (candidate.Recipe != "gleam" || result.Prepared.Toolchain != "gleam "+gleamRecipeVersion) {
				t.Fatalf("catalogue defaults for %s: %+v / %+v", name, candidate, result.Prepared)
			}
			switch {
			// React separates the value from the text beside it with a
			// comment, so the second page is recognised by both.
			case name == "next-export" && (candidate.OutputDirectory != "out" || !strings.Contains(fetch("/docs/about"), value) ||
				!strings.Contains(fetch("/docs/about"), "about</h1>")):
				t.Fatalf("static export: %+v", candidate)
			case name == "next-standalone" && strings.TrimSpace(fetch("/robots.txt")) != "standalone public file":
				t.Fatal("standalone server did not serve public/")
			case name == "svelte-auto" && (candidate.StartCommand != "node build" || candidate.NodeBuild == nil ||
				findingByCode(candidate.NodeBuild.Findings, "sveltekit_adapter_substituted") == nil):
				t.Fatalf("adapter-auto: %+v", candidate)
			case name == "express-vite" && (candidate.Framework != "express" || !strings.Contains(fetch("/api/health"), `"ok":true`)):
				t.Fatalf("express serving vite: %+v", candidate)
			case name == "hono-bun" && candidate.StartCommand != "bun src/index.ts":
				t.Fatalf("hono dev-only start: %+v", candidate)
			case name == "react-router-spa" && (candidate.OutputDirectory != "build/client" || !strings.Contains(html, "prerendered home") ||
				!strings.Contains(fetch("/about"), "spa shell loading") || strings.Contains(fetch("/about"), "prerendered home")):
				t.Fatalf("spa mode with a prerendered home: %+v", candidate)
			}
			if strings.Contains(content, secret) {
				t.Fatal("install credential escaped into served assets")
			}
		})
	}
}

// liveSitePaths are pages beyond the home page each static fixture serves:
// a generator's pretty URL, an about.html answered as /about, the fallback
// SvelteKit writes for routes it did not prerender.
var liveSitePaths = map[string][]string{
	"hugo": {"/about", "/robots.txt"}, "zola": {"/about"}, "mdbook": {"/chapter"}, "jekyll": {"/about"},
	"mkdocs": {"/guide"}, "lume": {"/about"}, "eleventy": {"/contact"}, "svelte-static": {"/about", "/deep/link"},
	"static-rules": {"/documentation", "/docs", "/old", "/deep/link", "/app/deep/link"},
}

// assertLiveHostingRules checks what the static-rules fixture's overlapping
// rules became: one answer per path, each where the first rule put it.
func assertLiveHostingRules(t *testing.T, base string) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string) (int, http.Header, string) {
		t.Helper()
		response, err := client.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return response.StatusCode, response.Header, string(body)
	}
	if status, header, _ := get("/docs"); status != http.StatusMovedPermanently || header.Get("Location") != "/documentation" ||
		header.Get("X-Robots-Tag") != "noindex" || header.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("/docs = %d %v", status, header)
	}
	if status, header, body := get("/deep/link"); status != http.StatusOK || !strings.Contains(body, "static-rules app page") ||
		len(header.Values("X-Frame-Options")) != 1 {
		t.Fatalf("/deep/link = %d %v %q", status, header, body)
	}
	if status, header, body := get("/app/deep/link"); status != http.StatusOK || !strings.Contains(body, "static-rules sub app") ||
		header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("/app/deep/link = %d %v %q", status, header, body)
	}
	if status, _, _ := get("/api/hello"); status != http.StatusNotFound {
		t.Fatalf("/api/hello = %d", status)
	}
	if status, header, _ := get("/404.html"); status != http.StatusOK || header.Get("Cache-Control") != "no-store" {
		t.Fatalf("/404.html = %d %v", status, header)
	}
	for _, private := range []string{"/.private/notes.txt", "/_redirects", "/_headers"} {
		if status, _, body := get(private); status == http.StatusOK || strings.Contains(body, "private notes") {
			t.Fatalf("%s = %d %q", private, status, body)
		}
	}
}

// liveSiteMissingPages are what each site's own 404 page says.
var liveSiteMissingPages = map[string]string{
	"hugo": "hugo not found page", "zola": "zola not found", "jekyll": "jekyll not found", "eleventy": "eleventy not found",
	"mdbook": "Page not found",
}

func copyFrameworkFixture(t *testing.T, name, destination string) {
	t.Helper()
	root := filepath.Join("testdata", "app-fixtures", name)
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		writeBuildFixture(t, destination, relative, string(content))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
