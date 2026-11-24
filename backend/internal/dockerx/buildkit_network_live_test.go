package dockerx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

// The builder a build closes waits for the environment's next build, which
// starts on it at once, taken off a network that build may no longer reach;
// removing the environment's builder takes it off every network.
func TestLiveNetworkedBuilderWaitsForTheNextBuild(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to start a BuildKit builder on networks of the test's own")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	client := New("unix:///var/run/docker.sock")
	t.Cleanup(func() { client.Close() })
	var nonce [6]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	suffix := hex.EncodeToString(nonce[:])
	first, second := "jd-e900000077-db-"+suffix, "jd-e900000077-copy-"+suffix
	for _, network := range []string{first, second} {
		if output, err := exec.CommandContext(ctx, "docker", "network", "create", network).CombinedOutput(); err != nil {
			t.Fatalf("create %s: %v %s", network, err, output)
		}
	}
	name := NetworkedBuilderName(first)
	t.Cleanup(func() {
		_ = client.RemoveNetworkedBuilderState(context.Background(), name)
		for _, network := range []string{first, second} {
			_ = exec.Command("docker", "network", "rm", network).Run()
		}
	})
	dir := t.TempDir()
	builder, err := client.StartNetworkedBuilder(ctx, NetworkedBuilderSpec{Name: name, Networks: []string{first, second}, ConfigDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if builder.Reused {
		t.Fatal("the first build started on a builder nobody left waiting")
	}
	builder.Close(ctx)
	if got := liveNetworkContainers(t, ctx, second); !slices.Contains(got, networkedBuilderContainer(name)) {
		t.Fatalf("the closed builder is not waiting on %s: %v", second, got)
	}
	started := time.Now()
	again, err := client.StartNetworkedBuilder(ctx, NetworkedBuilderSpec{Name: name, Networks: []string{first}, ConfigDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !again.Reused {
		t.Fatal("the next build did not start on the waiting builder")
	}
	t.Logf("the next build started on the waiting builder in %s", time.Since(started).Round(time.Millisecond))
	if got := liveNetworkContainers(t, ctx, second); slices.Contains(got, networkedBuilderContainer(name)) {
		t.Fatalf("the builder still reaches %s, which the next build may not: %v", second, got)
	}
	again.Close(ctx)
	if err := client.RemoveNetworkedBuilderState(ctx, name); err != nil {
		t.Fatal(err)
	}
	if got := liveNetworkContainers(t, ctx, first); len(got) != 0 {
		t.Fatalf("the removed builder is still on %s: %v", first, got)
	}
	if _, err := client.Inspect(ctx, networkedBuilderContainer(name)); err == nil {
		t.Fatal("the removed builder's container is still there")
	}
}

func liveNetworkContainers(t *testing.T, ctx context.Context, network string) []string {
	t.Helper()
	output, err := exec.CommandContext(ctx, "docker", "network", "inspect", network, "--format", "{{range .Containers}}{{.Name}} {{end}}").CombinedOutput()
	if err != nil {
		t.Fatalf("inspect %s: %v %s", network, err, output)
	}
	return strings.Fields(string(output))
}
