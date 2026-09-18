package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// TestLiveRuntimeDiagnoserReadsExitedContainer proves the Docker owner's
// diagnosis against a real daemon: a container that prints on both streams
// and exits non-zero is reported with its state, exit code and output, and the
// resource limits handed to creation are what the daemon actually applied.
func TestLiveRuntimeDiagnoserReadsExitedContainer(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker release host to run the live diagnoser check")
	}
	client := liveC4Docker(t)
	owner := NewDockerRuntimeOwner(client)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	name := fmt.Sprintf("jd-diagnose-%d", time.Now().UnixNano())
	created, err := client.Create(ctx, dockerx.ContainerSpec{
		Name: name, Image: "busybox:1.36",
		Command:       []string{"sh", "-c", "echo hello from stdout; echo oops from stderr >&2; exit 3"},
		Limits:        dockerx.ResourceLimits{MemoryMB: 64, PidsLimit: 32},
		RestartPolicy: "no", Logging: dockerx.CappedLogging(), Pull: "missing", Start: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.RemoveContainer(context.Background(), created.ID, true, true)
	})
	deadline := time.Now().Add(30 * time.Second)
	for {
		detail, err := client.Inspect(ctx, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if detail.State == "exited" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("container did not exit; state %s", detail.State)
		}
		time.Sleep(200 * time.Millisecond)
	}
	metadata := mustJSON(dockerReleaseRuntimeMetadata{Version: 1, PrimaryContainerID: created.ID})
	result, err := owner.DiagnoseRuntime(ctx, ReleaseRuntime{Kind: "container", RuntimeID: created.ID, Metadata: metadata})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Containers) != 1 {
		t.Fatalf("containers = %+v", result.Containers)
	}
	container := result.Containers[0]
	if container.State != "exited" || container.ExitCode != 3 || container.Name != name {
		t.Fatalf("diagnosis = %+v", container)
	}
	var streams []string
	for _, line := range container.Lines {
		streams = append(streams, line.Stream+":"+line.Text)
	}
	joined := strings.Join(streams, "\n")
	if !strings.Contains(joined, "stdout:hello from stdout") || !strings.Contains(joined, "stderr:oops from stderr") {
		t.Fatalf("captured output = %q", joined)
	}
	raw, err := client.InspectRaw(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	hostConfig, _ := raw["HostConfig"].(map[string]any)
	encoded, _ := json.Marshal(hostConfig)
	var limits struct {
		Memory    int64 `json:"Memory"`
		PidsLimit int64 `json:"PidsLimit"`
	}
	_ = json.Unmarshal(encoded, &limits)
	if limits.Memory != 64<<20 || limits.PidsLimit != 32 {
		t.Fatalf("daemon applied memory=%d pids=%d", limits.Memory, limits.PidsLimit)
	}
}
