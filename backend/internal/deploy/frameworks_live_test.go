package deploy

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
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
	for _, name := range []string{"vite", "next", "svelte-node", "svelte-static", "html", "containerfile", "go",
		"astro", "nuxt", "react-router", "fastapi", "flask", "django", "rust", "java", "gradle", "dotnet", "deno", "laravel", "php",
		"streamlit", "gradio", "next-pnpm", "express-yarn",
		"java-reactor", "gradle-multiproject", "dotnet-solution", "blazor-wasm", "fsharp", "dotnet-spa",
		"gradle-composite", "dotnet-multitarget"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
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
			detection, err := (Detector{}).DetectPath(ctx, root, SourceIdentity{})
			if err != nil {
				t.Fatal(err)
			}
			candidate := selectedDetectionCandidate(&detection)
			if candidate == nil {
				t.Fatalf("no unambiguous quick-setup candidate: %+v", detection.Candidates)
			}
			config := BuildPlanConfig{Method: candidate.BuildMethod, Recipe: candidate.Recipe, Dockerfile: candidate.Dockerfile,
				BuildCommand: candidate.BuildCommand, StartCommand: candidate.StartCommand, OutputDirectory: candidate.OutputDirectory}
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
			}
			stamp := time.Now().UnixNano()
			tag := fmt.Sprintf("jd-framework-test:%s-%d", name, stamp)
			t.Cleanup(func() { _, _ = liveDockerOutput(context.Background(), "image", "rm", "--force", tag) })
			logs := []string{}
			// Within the checkout, as prepare_context prepares it: a module
			// or a solution's project builds from the root that owns it.
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
			if name == "laravel" {
				// What the configure form generates for a Laravel import: the
				// application key, and file-backed sessions since the fixture
				// ships no sessions table.
				runtimeVariables = map[string]string{"APP_KEY": "base64:" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))), "SESSION_DRIVER": "file", "CACHE_STORE": "file"}
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
			walkAssets := name != "gradio"
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
				if !strings.HasPrefix(path, "/") {
					path = "/" + path
				}
				if strings.HasPrefix(path, "//") {
					continue
				}
				content += fetch(path)
			}
			if !strings.Contains(content, value) {
				t.Fatal("served application assets did not contain the configured build value")
			}
			if name == "go" && (!strings.Contains(content, "custom-start go1.26.8") || result.Prepared.GoVersion != "1.26.8") {
				t.Fatalf("Go override/version behavior missing: %q", content)
			}
			if candidate.Recipe == "python" && result.Prepared.PythonVersion != "3.13" {
				t.Fatalf("Python version not recorded: %+v", result.Prepared)
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
					result.Prepared.Install != "yarn install --frozen-lockfile") {
				t.Fatalf("catalogue defaults for %s: %+v / %+v", name, candidate, result.Prepared)
			}
			if strings.Contains(content, secret) {
				t.Fatal("install credential escaped into served assets")
			}
		})
	}
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
