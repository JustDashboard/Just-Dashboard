package dockerx

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A build that reaches a database runs on a builder of its own: one per
// environment, on that environment's network, its steps in the builder's
// network namespace and resolving through Docker's embedded DNS, from a
// BuildKit image pinned by digest.
func TestNetworkedBuilderIsCreatedOnItsNetwork(t *testing.T) {
	t.Parallel()
	spec := NetworkedBuilderSpec{Name: NetworkedBuilderName(22), Networks: []string{"jd-e22-db-abc123", "jd-preview-22"}}
	args, err := NetworkedBuilderCreateArgs(spec, "/var/lib/just-dashboard/buildkit/networked-buildkitd.toml")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"buildx", "create", "--name", "jd-build-e22", "--driver", "docker-container",
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
	if NetworkedBuilderStateVolume("jd-build-e22") != "buildx_buildkit_jd-build-e220_state" {
		t.Fatal(NetworkedBuilderStateVolume("jd-build-e22"))
	}
	for _, bad := range []NetworkedBuilderSpec{
		{Name: "default", Networks: []string{"jd-e22-db-abc123"}},
		{Name: "jd-build-e22 --driver-opt", Networks: []string{"jd-e22-db-abc123"}},
		{Name: "jd-build-e22"},
		{Name: "jd-build-e22", Networks: []string{"net --privileged"}},
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
	argv, _, err := BuildxCommand(ImmutableBuildOptions{Dockerfile: ".just-dashboard/Dockerfile", Tag: "app:1", Builder: "jd-build-e22"}, "/tmp/m.json")
	if err != nil || !slices.Equal(argv[:4], []string{"buildx", "build", "--builder", "jd-build-e22"}) || !slices.Contains(argv, "--load") {
		t.Fatalf("argv = %q, %v", argv, err)
	}
	argv, _, err = BuildxCommand(ImmutableBuildOptions{Dockerfile: ".just-dashboard/Dockerfile.schema", Builder: "jd-build-e22", CacheOnly: true}, "")
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
