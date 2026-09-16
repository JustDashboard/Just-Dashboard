package deploy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The audit's fault requirement is an ownership invariant, not a log message:
// whatever dies at a transition, exactly one release must be live afterwards
// and it must be the one the product claims. These cases kill real containers
// at the two points where a second live release would otherwise be possible —
// the blue/green window, and a live release that dies on its own.
func TestLiveRuntimeLossKeepsExactlyOneReleaseLive(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to kill real release containers")
	}
	client := liveC4Docker(t)
	owner := NewDockerRuntimeOwner(client)
	builder := NewArtifactBuilder(NewDockerArtifactBackend(client))
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	tag := "just-dashboard-fault:" + stamp
	t.Cleanup(func() { _, _ = liveDockerOutput(context.Background(), "image", "rm", "--force", tag) })

	root := t.TempDir()
	writeBuildFixture(t, root, "Dockerfile", `FROM caddy:2-alpine
COPY index.html /usr/share/caddy/index.html
`)
	writeBuildFixture(t, root, "index.html", "ready\n")
	image := livePrepareAndBuild(t, builder, root, tag, BuildPlanConfig{Method: BuildDockerfile}, nil, nil, nil)
	assertLiveImageResult(t, image)

	environmentID := time.Now().UnixNano()%1_000_000_000 + 4_000
	plan := RuntimePlanConfig{
		InternalPort: 80, BindAddress: "127.0.0.1",
		Strategy: StrategyStopFirst, StopSignal: "SIGTERM", GracePeriodSeconds: 3,
		Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
	}
	start := func(releaseID int64, number int64, port int) ReleaseRuntime {
		t.Helper()
		release := Release{ID: releaseID, EnvironmentID: environmentID, RunID: releaseID + 1, Number: number}
		runPlan := plan
		runPlan.HostPort = port
		started, err := owner.StartCandidate(context.Background(), CandidateRuntimeRequest{
			Run:      EngineRun{ID: release.RunID, EnvironmentID: environmentID},
			Release:  release,
			Snapshot: runtimeReleaseSnapshot{Version: 1, Plan: runPlan, Image: image.Image},
			Host:     runPlan.BindAddress, Port: port,
		}, nil)
		if err != nil {
			t.Fatalf("start release %d: %v", number, err)
		}
		runtime := runtimeFromInput(started.Input)
		t.Cleanup(func() { _, _ = owner.Stop(context.Background(), runtime, runPlan, nil, true, nil) })
		return runtime
	}

	livePort := liveC5LoopbackPort(t)
	live := start(environmentID+10, 1, livePort)
	waitFaultRelease(t, livePort, "ready", 60*time.Second)
	assertOwnedRuntimes(t, environmentID, live.RuntimeID)

	// The blue/green window is the only moment two releases are legitimately
	// running, so it is the moment a crash could leave two behind.
	candidatePort := liveC5LoopbackPort(t)
	candidate := start(environmentID+20, 2, candidatePort)
	waitFaultRelease(t, candidatePort, "ready", 60*time.Second)
	assertOwnedRuntimes(t, environmentID, live.RuntimeID, candidate.RuntimeID)

	if _, err := liveDockerOutput(context.Background(), "kill", "--signal", "SIGKILL", candidate.RuntimeID); err != nil {
		t.Fatalf("kill candidate: %v", err)
	}
	// The failure path stops and removes the candidate. A container that is
	// already dead must not turn recovery into an error, or a crashed
	// candidate would block every later deployment.
	stop, err := owner.Stop(context.Background(), candidate, plan, nil, true, nil)
	if err != nil {
		t.Fatalf("stop killed candidate: %v", err)
	}
	if !stop.Removed {
		t.Fatalf("killed candidate was not removed: %+v", stop)
	}
	assertOwnedRuntimes(t, environmentID, live.RuntimeID)
	waitFaultRelease(t, livePort, "ready", 30*time.Second)

	// A live release that dies is restarted in place rather than replaced, so
	// the release the product reports stays the release that serves.
	if _, err := liveDockerOutput(context.Background(), "kill", "--signal", "SIGKILL", live.RuntimeID); err != nil {
		t.Fatalf("kill live release: %v", err)
	}
	assertOwnedRuntimes(t, environmentID)
	if err := owner.StartExisting(context.Background(), live, nil, nil); err != nil {
		t.Fatalf("restart live release: %v", err)
	}
	assertOwnedRuntimes(t, environmentID, live.RuntimeID)
	waitFaultRelease(t, livePort, "ready", 60*time.Second)
}

// assertOwnedRuntimes compares the running containers this environment owns
// against the exact set expected. Docker is the authority here rather than the
// deployment tables: a record that disagrees with the host is the defect this
// is looking for.
func assertOwnedRuntimes(t *testing.T, environmentID int64, want ...string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var running []string
	for {
		raw, err := liveDockerOutput(context.Background(), "ps",
			"--filter", "label=io.just-dashboard.managed=true",
			"--filter", "label=io.just-dashboard.environment-id="+strconv.FormatInt(environmentID, 10),
			"--filter", "status=running", "--format", "{{.ID}}")
		if err != nil {
			t.Fatalf("list owned runtimes: %v", err)
		}
		running = running[:0]
		for _, line := range strings.Fields(string(raw)) {
			running = append(running, line)
		}
		if matchesRuntimeSet(running, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("environment %d owns %v, want exactly %v", environmentID, running, want)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// Docker's short IDs are a prefix of the full ID the owner records, so the
// comparison is by prefix in one direction only.
func matchesRuntimeSet(running, want []string) bool {
	if len(running) != len(want) {
		return false
	}
	for _, short := range running {
		found := false
		for _, full := range want {
			if strings.HasPrefix(full, short) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func waitFaultRelease(t *testing.T, port int, want string, timeout time.Duration) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/", port)
	deadline := time.Now().Add(timeout)
	last := ""
	for {
		response, err := client.Get(endpoint)
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr == nil {
				last = strings.TrimSpace(string(body))
				if last == want && response.StatusCode == http.StatusOK {
					return
				}
			}
		} else {
			last = err.Error()
		}
		if time.Now().After(deadline) {
			t.Fatalf("release on port %d never served %q, last %q", port, want, last)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
