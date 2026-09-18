package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLiveC5ActivationAdapters exercises the Docker and Compose owners that
// production activation actually calls. It is opt-in because it builds an
// image, publishes loopback ports, and deliberately waits for a SIGKILL
// escalation. Release CI runs it with JD_DEPLOY_LIVE=1.
func TestLiveC5ActivationAdapters(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker/Buildx release host to run the C5 activation matrix")
	}
	client := liveC4Docker(t)
	artifactBackend := NewDockerArtifactBackend(client)
	builder := NewArtifactBuilder(artifactBackend)
	owner := NewDockerRuntimeOwner(client)
	runner := NewCheckRunner(client)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	tag := "just-dashboard-c5:" + stamp
	t.Cleanup(func() { _, _ = liveDockerOutput(context.Background(), "image", "rm", "--force", tag) })

	root := t.TempDir()
	writeBuildFixture(t, root, "Dockerfile", `FROM caddy:2-alpine
COPY index.html /usr/share/caddy/index.html
HEALTHCHECK --interval=1s --timeout=1s --start-period=1s --retries=10 CMD wget -q -O /dev/null http://127.0.0.1:80/
`)
	writeBuildFixture(t, root, "index.html", "ready\n")
	result := livePrepareAndBuild(t, builder, root, tag, BuildPlanConfig{Method: BuildDockerfile}, nil, nil, nil)
	assertLiveImageResult(t, result)

	nextEnvironment := func(offset int64) int64 {
		return time.Now().UnixNano()%1_000_000_000 + 100 + offset
	}

	t.Run("immutable container and all C5 checks", func(t *testing.T) {
		port := liveC5LoopbackPort(t)
		environmentID := nextEnvironment(1)
		plan := RuntimePlanConfig{
			InternalPort: 80, HostPort: port, BindAddress: "127.0.0.1",
			Strategy: StrategyStopFirst, StopSignal: "SIGTERM", GracePeriodSeconds: 3,
			Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
		}
		run := EngineRun{ID: environmentID + 10, EnvironmentID: environmentID}
		release := Release{ID: environmentID + 20, EnvironmentID: environmentID, RunID: run.ID, Number: 1}
		started, err := owner.StartCandidate(context.Background(), CandidateRuntimeRequest{
			Run: run, Release: release, Snapshot: runtimeReleaseSnapshot{Version: 1, Plan: plan, Image: result.Image},
			RuntimeVariables: map[string]string{"JD_C5_VISIBLE": "fixture"}, Host: plan.BindAddress, Port: port,
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		runtime := runtimeFromInput(started.Input)
		t.Cleanup(func() { _, _ = owner.Stop(context.Background(), runtime, plan, nil, true, nil) })
		detail, err := client.Inspect(context.Background(), runtime.RuntimeID)
		if err != nil {
			t.Fatal(err)
		}
		if detail.Image != immutableRuntimeImage(result.Image) ||
			detail.Labels["io.just-dashboard.release-id"] != fmt.Sprintf("%d", release.ID) ||
			detail.Labels["io.just-dashboard.run-id"] != fmt.Sprintf("%d", run.ID) {
			t.Fatalf("runtime did not preserve immutable ownership: %#v", detail)
		}

		checks := []PlannedCheck{
			{Name: "http", Kind: string(CheckHTTP), Phase: "readiness", Required: true,
				Config: json.RawMessage(`{"path":"/","attempts":15,"timeoutSeconds":2,"intervalSeconds":1}`)},
			{Name: "tcp", Kind: string(CheckTCP), Phase: "readiness", Required: true,
				Config: json.RawMessage(`{"attempts":3,"timeoutSeconds":2,"intervalSeconds":1}`)},
			{Name: "docker", Kind: string(CheckDockerHealth), Phase: "readiness", Required: true,
				Config: json.RawMessage(`{"attempts":15,"timeoutSeconds":2,"intervalSeconds":1}`)},
			{Name: "command", Kind: string(CheckCommand), Phase: "smoke", Required: true,
				Config: json.RawMessage(`{"command":["test","-f","/etc/alpine-release"],"attempts":2,"timeoutSeconds":2}`)},
			{Name: "public", Kind: string(CheckPublicRoute), Phase: "smoke", Required: true,
				Config: json.RawMessage(`{"attempts":3,"timeoutSeconds":2,"intervalSeconds":1}`)},
		}
		publicTarget := started.Target
		publicTarget.PublicURLs = []string{fmt.Sprintf("http://127.0.0.1:%d/", port)}
		for _, check := range checks {
			target := started.Target
			if check.Kind == string(CheckPublicRoute) {
				target = publicTarget
			}
			evidence := runner.Run(context.Background(), check, target)
			if evidence.Outcome != HealthPassed || len(evidence.Attempts) == 0 {
				inspected, inspectErr := client.Inspect(context.Background(), runtime.RuntimeID)
				t.Fatalf("%s check evidence = %#v; runtime=%#v inspect_error=%v", check.Kind, evidence, inspected, inspectErr)
			}
		}

		evidence, err := owner.Stop(context.Background(), runtime, plan, nil, true, nil)
		if err != nil || evidence.Forced || !evidence.Removed || evidence.CompletedAt.Before(evidence.StartedAt) {
			t.Fatalf("graceful stop evidence = %#v, error=%v", evidence, err)
		}
	})

	t.Run("selected application port survives a host collision", func(t *testing.T) {
		busy, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer busy.Close()
		requested := busy.Addr().(*net.TCPAddr).Port
		environmentID := nextEnvironment(4)
		plan := RuntimePlanConfig{InternalPort: 3123, HostPort: requested, BindAddress: "127.0.0.1", Strategy: StrategyStopFirst, GracePeriodSeconds: 3,
			Command: []string{"sh", "-c", `exec caddy file-server --root /usr/share/caddy --listen ":$PORT"`}}
		run := EngineRun{ID: environmentID + 10, EnvironmentID: environmentID}
		release := Release{ID: environmentID + 20, EnvironmentID: environmentID, RunID: run.ID, Number: 1}
		snapshot := runtimeReleaseSnapshot{Version: 1, Plan: plan, Image: result.Image}
		started, err := owner.StartCandidate(context.Background(), CandidateRuntimeRequest{Run: run, Release: release, Snapshot: snapshot, Host: plan.BindAddress, Port: requested}, nil)
		if err != nil {
			t.Fatal(err)
		}
		runtime := runtimeFromInput(started.Input)
		t.Cleanup(func() { _, _ = owner.Stop(context.Background(), runtime, plan, nil, true, nil) })
		if runtime.Port == requested {
			t.Fatal("kept occupied host port")
		}
		evidence := runner.Run(context.Background(), PlannedCheck{Name: "HTTP readiness", Kind: "http", Phase: "readiness", Required: true, Config: json.RawMessage(`{"port":3123,"attempts":5,"intervalSeconds":1}`)}, targetForRuntime(runtime, snapshot))
		if evidence.Outcome != HealthPassed {
			t.Fatalf("allocated-port readiness: %+v", evidence)
		}
	})

	t.Run("SIGTERM escalates to bounded SIGKILL", func(t *testing.T) {
		environmentID := nextEnvironment(2)
		plan := RuntimePlanConfig{
			Strategy: StrategyStopFirst, StopSignal: "SIGTERM", GracePeriodSeconds: 1,
			Command:      []string{"/bin/sh", "-c", "trap '' TERM; while :; do sleep 1; done"},
			Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
		}
		run := EngineRun{ID: environmentID + 10, EnvironmentID: environmentID}
		release := Release{ID: environmentID + 20, EnvironmentID: environmentID, RunID: run.ID, Number: 1}
		started, err := owner.StartCandidate(context.Background(), CandidateRuntimeRequest{
			Run: run, Release: release, Snapshot: runtimeReleaseSnapshot{Version: 1, Plan: plan, Image: result.Image},
			RuntimeVariables: map[string]string{}, Host: "127.0.0.1",
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		runtime := runtimeFromInput(started.Input)
		t.Cleanup(func() { _ = client.RemoveContainer(context.Background(), runtime.RuntimeID, true, false) })
		startedAt := time.Now()
		stopCtx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		evidence, stopErr := owner.Stop(stopCtx, runtime, plan, nil, true, nil)
		cancel()
		if stopErr != nil || !evidence.Forced || !evidence.Removed || evidence.Signal != "SIGTERM" ||
			evidence.GraceSeconds != 1 || time.Since(startedAt) > 10*time.Second {
			t.Fatalf("forced stop evidence = %#v, elapsed=%s error=%v", evidence, time.Since(startedAt), stopErr)
		}
	})

	t.Run("Compose uses pinned override and removes private env file", func(t *testing.T) {
		if !client.ComposeAvailable(context.Background()) {
			t.Skip("Docker Compose plugin is unavailable")
		}
		sourceRoot := t.TempDir()
		port := liveC5LoopbackPort(t)
		compose := `services:
  web:
    image: alpine:3.22
    ports:
      - "127.0.0.1:${JD_C5_PORT}:80"
    environment:
      JD_C5_RUNTIME: "${JD_C5_RUNTIME:?required}"
`
		writeBuildFixture(t, sourceRoot, "compose.yml", compose)
		environmentID := nextEnvironment(3)
		plan := RuntimePlanConfig{
			InternalPort: 80, HostPort: port, BindAddress: "127.0.0.1",
			Strategy: StrategyStopFirst, StopSignal: "SIGTERM", GracePeriodSeconds: 3,
			Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
		}
		run := EngineRun{ID: environmentID + 10, EnvironmentID: environmentID}
		release := Release{ID: environmentID + 20, EnvironmentID: environmentID, RunID: run.ID, Number: 1}
		variables := map[string]string{"JD_C5_PORT": fmt.Sprintf("%d", port), "JD_C5_RUNTIME": "private-fixture-value"}
		composeLogs := []string{}
		started, err := owner.StartCandidate(context.Background(), CandidateRuntimeRequest{
			Run: run, Release: release, SourceRoot: sourceRoot, RuntimeVariables: variables,
			Host: plan.BindAddress, Port: port,
			Snapshot: runtimeReleaseSnapshot{Version: 1, Plan: plan, Compose: &ResolvedComposeSnapshot{
				SourceDigest: fakeContentDigest(compose), Files: []string{"compose.yml"},
				Services: []ResolvedComposeService{{
					Plan:      ComposeServicePlan{Name: "web", Image: "alpine:3.22", Ports: []string{}, Mounts: []string{}, Advanced: []string{}},
					Reference: result.Image.Reference, Digest: result.Image.Digest, ConfigDigest: result.Image.ConfigDigest, Source: "pull",
				}},
			}},
		}, func(line BuildLog) error {
			composeLogs = append(composeLogs, line.Stream+": "+line.Text)
			return nil
		})
		if err != nil {
			t.Fatalf("%v; compose output: %s", err, strings.Join(composeLogs, " | "))
		}
		runtime := runtimeFromInput(started.Input)
		t.Cleanup(func() { _, _ = owner.Stop(context.Background(), runtime, plan, variables, true, nil) })
		if strings.Contains(string(started.Input.Metadata), variables["JD_C5_RUNTIME"]) {
			t.Fatal("Compose runtime metadata contains a variable value")
		}
		health := runner.Run(context.Background(), PlannedCheck{
			Name: "compose-health", Kind: string(CheckDockerHealth), Phase: "readiness", Required: true,
			Config: json.RawMessage(`{"attempts":15,"timeoutSeconds":2,"intervalSeconds":1}`),
		}, started.Target)
		if health.Outcome != HealthPassed {
			t.Fatalf("Compose health evidence = %#v", health)
		}
		override := filepath.Join(sourceRoot, ".just-dashboard", "release.yml")
		contents, err := os.ReadFile(override)
		if err != nil || !strings.Contains(string(contents), immutableRuntimeImage(result.Image)) || strings.Contains(string(contents), "latest") {
			t.Fatalf("Compose release override = %q, error=%v", contents, err)
		}
		if info, statErr := os.Stat(override); statErr != nil {
			t.Fatalf("stat Compose override: %v", statErr)
		} else if info.Mode().Perm() != 0o600 {
			t.Fatalf("Compose override mode = %v", info.Mode().Perm())
		}
		if matches, _ := filepath.Glob(filepath.Join(sourceRoot, ".just-dashboard-env-*")); len(matches) != 0 {
			t.Fatalf("temporary Compose environment files remain: %v", matches)
		}
		stop, stopErr := owner.Stop(context.Background(), runtime, plan, variables, true, nil)
		if stopErr != nil || !stop.Removed {
			t.Fatalf("Compose stop evidence = %#v, error=%v", stop, stopErr)
		}
		if matches, _ := filepath.Glob(filepath.Join(sourceRoot, ".just-dashboard-env-*")); len(matches) != 0 {
			t.Fatalf("temporary Compose environment files remain after down: %v", matches)
		}
	})
}

func liveC5LoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}
