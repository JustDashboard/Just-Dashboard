package dockerx

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// A build that reaches a database runs on a builder of its own: one per
// environment, named after and created on that environment's network, its
// steps in the builder's network namespace and resolving through Docker's
// embedded DNS, from a BuildKit image pinned by digest.
func TestNetworkedBuilderIsCreatedOnItsNetwork(t *testing.T) {
	t.Parallel()
	spec := NetworkedBuilderSpec{Name: NetworkedBuilderName("jd-e22-db-ABC123"), Networks: []string{"jd-e22-db-abc123", "jd-preview-22"}}
	args, err := NetworkedBuilderCreateArgs(spec, "/var/lib/just-dashboard/buildkit/networked-buildkitd.toml")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"buildx", "create", "--name", "jd-build-e22-db-abc123", "--driver", "docker-container",
		"--driver-opt", "network=jd-e22-db-abc123",
		"--driver-opt", "image=" + NetworkedBuilderImage,
		"--driver-opt", "env.BUILDKIT_STEP_LOG_MAX_SIZE=-1",
		"--driver-opt", "env.BUILDKIT_STEP_LOG_MAX_SPEED=-1",
		"--buildkitd-flags", "--oci-worker-net=host",
		"--buildkitd-config", "/var/lib/just-dashboard/buildkit/networked-buildkitd.toml",
		"--bootstrap",
	}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %q", args)
	}
	if !strings.Contains(NetworkedBuilderImage, "@sha256:") {
		t.Fatal("the BuildKit image is not pinned by digest")
	}
	if NetworkedBuilderStateVolume("jd-build-e22-db-abc123") != "buildx_buildkit_jd-build-e22-db-abc1230_state" {
		t.Fatal(NetworkedBuilderStateVolume("jd-build-e22-db-abc123"))
	}
	for _, bad := range []NetworkedBuilderSpec{
		{Name: "default", Networks: []string{"jd-e22-db-abc123"}},
		{Name: "jd-build-e22 --driver-opt", Networks: []string{"jd-e22-db-abc123"}},
		{Name: "jd-build-e22-db-abc123"},
		{Name: "jd-build-e22-db-abc123", Networks: []string{"net --privileged"}},
	} {
		if _, err := NetworkedBuilderCreateArgs(bad, "/x.toml"); err == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
}

// The configuration names Docker's resolver, which BuildKit would otherwise
// drop from a step's resolv.conf, and is written once.
func TestNetworkedBuilderConfigNamesDockersResolver(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "buildkit")
	path := filepath.Join(dir, networkedBuilderConfigName)
	for range 2 {
		if err := writeNetworkedBuilderConfig(dir, path); err != nil {
			t.Fatal(err)
		}
	}
	content, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(content), `nameservers = ["127.0.0.11"]`) {
		t.Fatalf("config = %s, %v", content, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("left behind: %v", entries)
	}
}

// A build on the networked builder names it, and only one of the dashboard's
// own; a schema step keeps nothing but the cache.
func TestBuildxCommandNamesTheNetworkedBuilder(t *testing.T) {
	t.Parallel()
	argv, _, err := BuildxCommand(ImmutableBuildOptions{Dockerfile: ".just-dashboard/Dockerfile", Tag: "app:1", Builder: "jd-build-e22-db-abc123"}, "/tmp/m.json")
	if err != nil || !slices.Equal(argv[:4], []string{"buildx", "build", "--builder", "jd-build-e22-db-abc123"}) || !slices.Contains(argv, "--load") {
		t.Fatalf("argv = %q, %v", argv, err)
	}
	argv, _, err = BuildxCommand(ImmutableBuildOptions{Dockerfile: ".just-dashboard/Dockerfile.schema", Builder: "jd-build-e22-db-abc123", CacheOnly: true}, "")
	if err != nil || slices.Contains(argv, "--load") || slices.Contains(argv, "--tag") || slices.Contains(argv, "--metadata-file") ||
		!strings.Contains(strings.Join(argv, " "), "--output type=cacheonly") {
		t.Fatalf("argv = %q, %v", argv, err)
	}
	if _, _, err := BuildxCommand(ImmutableBuildOptions{Dockerfile: "Dockerfile", Tag: "app:1", Builder: "someone-elses"}, "/tmp/m.json"); err == nil {
		t.Fatal("a builder that is not the dashboard's was accepted")
	}
	argv, _, err = BuildxCommand(ImmutableBuildOptions{Dockerfile: "Dockerfile", Tag: "app:1"}, "/tmp/m.json")
	if err != nil || slices.Contains(argv, "--builder") {
		t.Fatalf("argv = %q, %v", argv, err)
	}
}

// A builder an earlier build left waiting is matched to the next build's
// networks before that build starts on it: joined to what it may reach now,
// and taken off what it may not, such as a database unlinked meanwhile.
func TestWaitingNetworkedBuilderIsMatchedToTheBuildsNetworks(t *testing.T) {
	t.Parallel()
	add, remove := networkedBuilderNetworkChanges(
		[]string{"jd-e22-db-abc123", "jd-preview-22"}, []string{"jd-e22-db-abc123", "jd-e22-db-def456"})
	if !slices.Equal(add, []string{"jd-e22-db-def456"}) || !slices.Equal(remove, []string{"jd-preview-22"}) {
		t.Fatalf("add = %v, remove = %v", add, remove)
	}
	if add, remove := networkedBuilderNetworkChanges([]string{"a"}, []string{"a"}); len(add)+len(remove) != 0 {
		t.Fatalf("an unchanged builder changes: add = %v, remove = %v", add, remove)
	}
}

// Only a builder this process left waiting is claimed, once, and claiming
// it stops the timer that would remove it.
func TestWaitingNetworkedBuilderIsClaimedOnce(t *testing.T) {
	t.Parallel()
	name := "jd-build-e900000001-db-claimonce"
	removed := make(chan struct{}, 1)
	waiting := &waitingBuilder{networks: []string{"jd-e900000001-db-claimonce"}}
	waitingBuilders.Lock()
	waiting.timer = time.AfterFunc(time.Hour, func() { removed <- struct{}{} })
	waitingBuilders.byName[name] = waiting
	waitingBuilders.Unlock()
	if got := takeWaitingBuilder(name); got != waiting {
		t.Fatalf("claimed %v, want the waiting builder", got)
	}
	if got := takeWaitingBuilder(name); got != nil {
		t.Fatalf("claimed twice: %v", got)
	}
	if waiting.timer.Stop() {
		t.Fatal("claiming the builder left its removal running")
	}
}

// A closed builder with no wait configured, or no Docker client, is removed
// at once, as every builder was before builders waited.
func TestNetworkedBuilderCloseWithoutAClientRemovesAtOnce(t *testing.T) {
	t.Parallel()
	released := false
	builder := &NetworkedBuilder{Name: "jd-build-e900000002-db-noclient", release: func() { released = true }}
	builder.Close(t.Context())
	if !released {
		t.Fatal("the builder's lock was not released")
	}
	waitingBuilders.Lock()
	_, waiting := waitingBuilders.byName[builder.Name]
	waitingBuilders.Unlock()
	if waiting {
		t.Fatal("a builder with no client was left waiting")
	}
}

// A dashboard that stops removes the builders its client left waiting, once
// each, and stops the timers that would have: those timers end with the
// process. Another client's builders are its own business.
func TestStoppingRemovesTheWaitingNetworkedBuilders(t *testing.T) {
	t.Parallel()
	stopping, other := &Client{}, &Client{}
	removed := make(chan string, 4)
	wait := func(client *Client, name string) *waitingBuilder {
		waiting := &waitingBuilder{client: client, networks: []string{strings.TrimPrefix(name, "jd-build-")},
			remove: func(context.Context) { removed <- name }}
		waitingBuilders.Lock()
		waiting.timer = time.AfterFunc(time.Hour, func() { removeWaitingBuilder(name, waiting) })
		waitingBuilders.byName[name] = waiting
		waitingBuilders.Unlock()
		return waiting
	}
	first := wait(stopping, "jd-build-e900000003-db-stopfirst")
	second := wait(stopping, "jd-build-e900000003-db-stopsecond")
	kept := wait(other, "jd-build-e900000004-db-stopother")
	t.Cleanup(func() {
		takeWaitingBuilder("jd-build-e900000004-db-stopother")
		waitingBuilders.Lock()
		delete(waitingBuilders.drained, stopping)
		waitingBuilders.Unlock()
	})
	stopping.RemoveWaitingNetworkedBuilders(t.Context())
	close(removed)
	got := []string{}
	for name := range removed {
		got = append(got, name)
	}
	if !slices.Equal(got, []string{"jd-build-e900000003-db-stopfirst", "jd-build-e900000003-db-stopsecond"}) {
		t.Fatalf("removed %v", got)
	}
	for _, waiting := range []*waitingBuilder{first, second} {
		if waiting.timer.Stop() {
			t.Fatal("a removed builder's timer is still running")
		}
	}
	if !kept.timer.Stop() {
		t.Fatal("another client's builder stopped waiting")
	}
	waitingBuilders.Lock()
	_, firstWaits := waitingBuilders.byName["jd-build-e900000003-db-stopfirst"]
	_, keptWaits := waitingBuilders.byName["jd-build-e900000004-db-stopother"]
	drained, otherDrained := waitingBuilders.drained[stopping], waitingBuilders.drained[other]
	waitingBuilders.Unlock()
	if firstWaits || !keptWaits || !drained || otherDrained {
		t.Fatalf("first waits %v, other waits %v, drained %v, other drained %v", firstWaits, keptWaits, drained, otherDrained)
	}
	// Stopping again finds nothing left to remove.
	stopping.RemoveWaitingNetworkedBuilders(t.Context())
	var none *Client
	none.RemoveWaitingNetworkedBuilders(t.Context())
}
