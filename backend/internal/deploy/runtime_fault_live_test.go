package deploy

import (
	"context"
	"encoding/json"
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

	// "Exactly one release is live" counts releases, not containers: a
	// release of one container is one running container, a replicated one
	// is all of its replicas and nothing of any other release.
	t.Run("one container", func(t *testing.T) {
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
		assertOneReleaseLive(t, environmentID, live.ReleaseID, 1)
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
	})

	t.Run("replicas", func(t *testing.T) {
		const replicas = 3
		environmentID := time.Now().UnixNano()%1_000_000_000 + 5_000
		plan := RuntimePlanConfig{
			InternalPort: 80, BindAddress: "127.0.0.1", Replicas: replicas,
			Strategy: StrategyBlueGreen, StopSignal: "SIGTERM", GracePeriodSeconds: 3,
			Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
		}
		start := func(releaseID int64, number int64) (ReleaseRuntime, []runtimeReplica) {
			t.Helper()
			release := Release{ID: releaseID, EnvironmentID: environmentID, RunID: releaseID + 1, Number: number}
			ports := []int{}
			for len(ports) < replicas {
				ports = append(ports, liveC5LoopbackPort(t))
			}
			started, err := owner.StartCandidate(context.Background(), CandidateRuntimeRequest{
				Run:      EngineRun{ID: release.RunID, EnvironmentID: environmentID},
				Release:  release,
				Snapshot: runtimeReleaseSnapshot{Version: 1, Plan: plan, Image: image.Image},
				Host:     plan.BindAddress, Port: ports[0], ReplicaPorts: ports[1:],
			}, nil)
			if err != nil {
				t.Fatalf("start release %d: %v", number, err)
			}
			runtime := runtimeFromInput(started.Input)
			t.Cleanup(func() { _, _ = owner.Stop(context.Background(), runtime, plan, nil, true, nil) })
			members := runtimeReplicas(runtime)
			if len(members) != replicas || len(runtimeContainerIDs(runtime)) != replicas {
				t.Fatalf("release %d recorded %+v", number, members)
			}
			for index, member := range members {
				want := replicaContainerName(environmentID, number, index+1)
				if member.Index != index+1 || member.Name != want || member.Port != ports[index] {
					t.Fatalf("replica %d = %+v, want %s on %d", index+1, member, want, ports[index])
				}
				labels := liveContainerLabels(t, member.ContainerID)
				if labels["io.just-dashboard.release-id"] != strconv.FormatInt(releaseID, 10) ||
					labels[replicaLabel] != strconv.Itoa(index+1) || labels[replicasLabel] != strconv.Itoa(replicas) {
					t.Fatalf("replica %d labels = %v", index+1, labels)
				}
				waitFaultRelease(t, member.Port, "ready", 60*time.Second)
			}
			return runtime, members
		}
		ids := func(members []runtimeReplica) []string {
			out := []string{}
			for _, member := range members {
				out = append(out, member.ContainerID)
			}
			return out
		}

		live, liveReplicas := start(environmentID+10, 1)
		assertOwnedRuntimes(t, environmentID, ids(liveReplicas)...)
		assertOneReleaseLive(t, environmentID, live.ReleaseID, replicas)

		// The blue/green window: both releases, every replica of each.
		candidate, candidateReplicas := start(environmentID+20, 2)
		assertOwnedRuntimes(t, environmentID, append(ids(liveReplicas), ids(candidateReplicas)...)...)

		// One replica of the candidate dies in the window. Recovery removes
		// the whole candidate — the dead replica and the two still running —
		// so no part of a release that never went live is left serving.
		if _, err := liveDockerOutput(context.Background(), "kill", "--signal", "SIGKILL", candidateReplicas[1].ContainerID); err != nil {
			t.Fatalf("kill candidate replica: %v", err)
		}
		stop, err := owner.Stop(context.Background(), candidate, plan, nil, true, nil)
		if err != nil || !stop.Removed {
			t.Fatalf("stop candidate with a dead replica = %+v, %v", stop, err)
		}
		for _, member := range candidateReplicas {
			if liveContainerExists(t, member.ContainerID) {
				t.Fatalf("candidate replica %s outlived its release's removal", member.Name)
			}
		}
		assertOwnedRuntimes(t, environmentID, ids(liveReplicas)...)
		assertOneReleaseLive(t, environmentID, live.ReleaseID, replicas)

		// One replica of the live release dies on its own. The release is
		// brought back whole, in place: the same containers, never another
		// release's.
		if _, err := liveDockerOutput(context.Background(), "kill", "--signal", "SIGKILL", liveReplicas[2].ContainerID); err != nil {
			t.Fatalf("kill live replica: %v", err)
		}
		assertOwnedRuntimes(t, environmentID, ids(liveReplicas[:2])...)
		if err := owner.StartExisting(context.Background(), live, nil, nil); err != nil {
			t.Fatalf("restart live release: %v", err)
		}
		assertOwnedRuntimes(t, environmentID, ids(liveReplicas)...)
		assertOneReleaseLive(t, environmentID, live.ReleaseID, replicas)
		for _, member := range liveReplicas {
			waitFaultRelease(t, member.Port, "ready", 60*time.Second)
		}

		// The whole live release dies. Starting it again brings every
		// replica back, and stopping it stops every replica.
		for _, member := range liveReplicas {
			if _, err := liveDockerOutput(context.Background(), "kill", "--signal", "SIGKILL", member.ContainerID); err != nil {
				t.Fatalf("kill live replica: %v", err)
			}
		}
		assertOwnedRuntimes(t, environmentID)
		if err := owner.StartExisting(context.Background(), live, nil, nil); err != nil {
			t.Fatalf("restart live release: %v", err)
		}
		assertOneReleaseLive(t, environmentID, live.ReleaseID, replicas)
		if stop, err := owner.Stop(context.Background(), live, plan, nil, false, nil); err != nil || stop.Removed {
			t.Fatalf("stop live release = %+v, %v", stop, err)
		}
		assertOwnedRuntimes(t, environmentID)
	})
}

// assertOneReleaseLive requires that the environment's running containers
// are exactly one release's, and all of that release's replicas.
func assertOneReleaseLive(t *testing.T, environmentID, releaseID int64, replicas int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		raw, err := liveDockerOutput(context.Background(), "ps",
			"--filter", "label=io.just-dashboard.managed=true",
			"--filter", "label=io.just-dashboard.environment-id="+strconv.FormatInt(environmentID, 10),
			"--filter", "status=running", "--format", `{{.Label "io.just-dashboard.release-id"}}`)
		if err != nil {
			t.Fatalf("list owned runtimes: %v", err)
		}
		releases := strings.Fields(string(raw))
		matches := len(releases) == replicas
		for _, id := range releases {
			matches = matches && id == strconv.FormatInt(releaseID, 10)
		}
		if matches {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("environment %d runs releases %v, want release %d %d times", environmentID, releases, releaseID, replicas)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func liveContainerLabels(t *testing.T, id string) map[string]string {
	t.Helper()
	raw, err := liveDockerOutput(context.Background(), "inspect", "--format", "{{json .Config.Labels}}", id)
	if err != nil {
		t.Fatalf("inspect %s: %v", id, err)
	}
	labels := map[string]string{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &labels); err != nil {
		t.Fatalf("labels of %s: %v", id, err)
	}
	return labels
}

func liveContainerExists(t *testing.T, id string) bool {
	t.Helper()
	_, err := liveDockerOutput(context.Background(), "inspect", id)
	return err == nil
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
