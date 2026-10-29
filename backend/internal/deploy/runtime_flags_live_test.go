package deploy

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// A release container is started by the runtime owner with the process
// flags of its plan, on the local daemon, and each is read back from inside
// it: the user it runs as, a root that refuses a write while its tmpfs paths
// take one at the size the plan gave, a nofile limit, and a kernel parameter
// of its own network namespace. A GPU request on a Docker with no NVIDIA
// runtime fails the start with its named reason and leaves no container.
func TestLiveRuntimeFlagsReachTheReleaseContainer(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to start a release container with its process flags")
	}
	client := liveC4Docker(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	const image = "alpine:3.20"
	id, err := liveDockerOutput(ctx, "image", "inspect", "--format", "{{.Id}}", image)
	if err != nil {
		t.Skipf("%s is not on this host: %v", image, err)
	}
	stamp := time.Now().UnixNano()
	container := fmt.Sprintf("jd-e%d-r1", stamp)
	refused := fmt.Sprintf("jd-e%d-r2", stamp)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = liveDockerOutput(cleanup, "rm", "--force", "--volumes", container, refused)
	})
	plan := RuntimePlanConfig{
		Strategy: StrategyStopFirst, Command: []string{"sleep", "3600"},
		User: "1000:1000", ReadOnlyRootfs: true,
		Tmpfs:   []RuntimeTmpfs{{Path: "/tmp", SizeMB: 16}, {Path: "/scratch"}},
		Ulimits: []RuntimeUlimit{{Name: UlimitNofile, Soft: 4096, Hard: 8192}},
		Sysctls: []RuntimeSysctl{{Name: "net.core.somaxconn", Value: "2048"}},
	}
	configuration := canonicalConfiguration(PlanConfiguration{Build: BuildPlanConfig{Method: BuildImage}, Runtime: plan})
	if err := configuration.Validate(); err != nil {
		t.Fatal(err)
	}
	plan = configuration.Runtime
	owner := NewDockerRuntimeOwner(client)
	request := func(number int64, plan RuntimePlanConfig) CandidateRuntimeRequest {
		return CandidateRuntimeRequest{
			Run: EngineRun{ID: stamp, EnvironmentID: stamp}, Release: Release{ID: stamp + number, EnvironmentID: stamp, RunID: stamp, Number: number},
			Snapshot: runtimeReleaseSnapshot{Version: 1, Plan: plan, Image: ResolvedImage{Reference: image, ConfigDigest: strings.TrimSpace(string(id))}},
			Host:     "127.0.0.1",
		}
	}
	started, err := owner.StartCandidate(ctx, request(1, plan), nil)
	if err != nil {
		t.Fatal(err)
	}
	if started.Input.Name != container {
		t.Fatalf("started %q, want %q", started.Input.Name, container)
	}
	inside := func(user, script string) (string, error) {
		output, err := liveDockerOutput(ctx, "exec", "--user", user, container, "sh", "-c", script)
		return strings.TrimSpace(string(output)), err
	}
	read := func(script string) string {
		t.Helper()
		output, err := inside("1000:1000", script)
		if err != nil {
			t.Fatalf("%s: %v", script, err)
		}
		t.Logf("%s → %s", script, output)
		return output
	}
	// The exec runs as the container's own user unless told otherwise; the
	// plan's is the one the process was started as, read from /proc/1.
	if got := read("id -u; id -g"); got != "1000\n1000" {
		t.Fatalf("id = %q", got)
	}
	if got := read("awk '/^Uid:/{print $2} /^Gid:/{print $2}' /proc/1/status"); got != "1000\n1000" {
		t.Fatalf("the release process runs as %q", got)
	}
	if got := read("ulimit -n; ulimit -Hn"); got != "4096\n8192" {
		t.Fatalf("nofile = %q", got)
	}
	if got := read("cat /proc/sys/net/core/somaxconn"); got != "2048" {
		t.Fatalf("somaxconn = %q", got)
	}
	// Root inside the container is refused by the filesystem, not by a
	// permission: the root is mounted read-only.
	if output, err := inside("0", "touch /written-to-the-root"); err == nil || !strings.Contains(output+err.Error(), "Read-only file system") {
		t.Fatalf("a write to / as root = %q, %v", output, err)
	}
	if got := read("touch /tmp/written /scratch/written && ls /scratch/written /tmp/written"); got != "/scratch/written\n/tmp/written" {
		t.Fatalf("tmpfs writes = %q", got)
	}
	mounts := read("awk '$2 == \"/\" || $2 == \"/tmp\" || $2 == \"/scratch\" {print $2, $3, $4}' /proc/mounts")
	for _, want := range []string{"/ overlay ro,", "/tmp tmpfs rw,", "/scratch tmpfs rw,"} {
		if !strings.Contains(mounts, want) {
			t.Fatalf("mounts = %q, want %q", mounts, want)
		}
	}
	if !strings.Contains(mounts, "size=16384k") || !strings.Contains(mounts, fmt.Sprintf("size=%dk", DefaultTmpfsSizeMB*1024)) {
		t.Fatalf("tmpfs sizes = %q", mounts)
	}

	runtimes, err := client.ContainerRuntimes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(runtimes, "nvidia") {
		t.Logf("this Docker has an NVIDIA runtime (%v); the GPU refusal is not reproducible here", runtimes)
		return
	}
	gpu := plan
	gpu.GPUs = &RuntimeGPUs{Mode: GPUAll}
	_, err = owner.StartCandidate(ctx, request(2, gpu), nil)
	if err == nil {
		t.Fatal("a GPU request started on a Docker with no NVIDIA runtime")
	}
	code, message := candidateStartFailure(err)
	t.Logf("GPU start refused: %v → %s: %s", err, code, message)
	if code != "runtime_gpu_unavailable" {
		t.Fatalf("the refusal = %s (%v)", code, err)
	}
	if output, _ := liveDockerOutput(ctx, "ps", "-a", "--filter", "name=^"+refused+"$", "--format", "{{.ID}}"); strings.TrimSpace(string(output)) != "" {
		t.Fatalf("the refused candidate was left behind: %s", output)
	}
}
