package deploy

import (
	"context"
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
	for _, name := range []string{"vite", "next", "svelte-node", "svelte-static", "html", "containerfile", "go"} {
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
			result := livePrepareAndBuild(t, builder, filepath.Join(root, candidate.Root), tag, config, variables, nil, func(line BuildLog) error { logs = append(logs, line.Text); return nil })
			if config.Method == BuildRecipe {
				assertLiveArtifactSecretFree(t, tag, secret, result, logs)
			}
			plan := RuntimePlanConfig{InternalPort: candidate.Port, BindAddress: "127.0.0.1", Strategy: StrategyStopFirst, GracePeriodSeconds: 3}
			if plan.InternalPort == 0 {
				t.Fatal("detected HTTP application has no serving port")
			}
			run := EngineRun{ID: stamp, EnvironmentID: stamp}
			release := Release{ID: stamp, EnvironmentID: stamp, RunID: stamp, Number: 1}
			started, err := owner.StartCandidate(ctx, CandidateRuntimeRequest{Run: run, Release: release,
				Snapshot: runtimeReleaseSnapshot{Version: 1, Plan: plan, Image: result.Image}, Host: "127.0.0.1", Port: liveC5LoopbackPort(t)}, nil)
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
			for _, match := range regexp.MustCompile(`(?:src|href)="([^"?#]+\.js)(?:[^" ]*)"`).FindAllStringSubmatch(html, -1) {
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
