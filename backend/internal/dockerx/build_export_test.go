package dockerx

import (
	"context"
	"slices"
	"testing"

	"github.com/docker/docker/api/types/system"
)

// On containerd's store the image is exported as zstd and unpacked; on a
// classic graph driver, which BuildKit writes into with nothing to compress,
// it is loaded as before. The tag still comes from --tag either way.
func TestBuildxCommandExportsZstdOnlyIntoContainerdsStore(t *testing.T) {
	for _, store := range []struct {
		containerd bool
		want, not  []string
	}{
		{containerd: true, want: []string{"--output", containerdImageExport}, not: []string{"--load"}},
		{containerd: false, want: []string{"--load"}, not: []string{"--output"}},
	} {
		argv, _, err := BuildxCommand(ImmutableBuildOptions{
			Dockerfile: "Dockerfile", Tag: "just-dashboard/test:run-1", containerdStore: store.containerd,
		}, "/tmp/m.json")
		if err != nil {
			t.Fatal(err)
		}
		at := slices.Index(argv, store.want[0])
		if at < 0 || !slices.Equal(argv[at:at+len(store.want)], store.want) {
			t.Fatalf("containerd=%v argv = %v, want %v", store.containerd, argv, store.want)
		}
		for _, absent := range store.not {
			if slices.Contains(argv, absent) {
				t.Fatalf("containerd=%v argv = %v carries %s", store.containerd, argv, absent)
			}
		}
		if tag := slices.Index(argv, "--tag"); tag < 0 || argv[tag+1] != "just-dashboard/test:run-1" ||
			!slices.Contains(argv, "--metadata-file") {
			t.Fatalf("containerd=%v argv = %v lost its tag or metadata file", store.containerd, argv)
		}
	}
	if containerdImageExport != "type=image,unpack=true,compression=zstd,compression-level=3,oci-mediatypes=true" {
		t.Fatalf("export = %s", containerdImageExport)
	}
}

// A networked builder is a container of its own, so an image export there
// would land in its store, not the daemon's: it loads on containerd's store
// too, and a cache-only step exports nothing at all.
func TestANetworkedBuildLoadsOnEitherStore(t *testing.T) {
	for _, containerd := range []bool{true, false} {
		argv, _, err := BuildxCommand(ImmutableBuildOptions{
			Dockerfile: "Dockerfile", Tag: "just-dashboard/test:run-1", Builder: "jd-build-e22-db-abc123", containerdStore: containerd,
		}, "/tmp/m.json")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(argv, "--load") || slices.Contains(argv, "--output") || !slices.Contains(argv, "--metadata-file") {
			t.Fatalf("containerd=%v argv = %v", containerd, argv)
		}
		argv, _, err = BuildxCommand(ImmutableBuildOptions{
			Dockerfile: "Dockerfile.schema", Builder: "jd-build-e22-db-abc123", CacheOnly: true, containerdStore: containerd,
		}, "")
		if err != nil {
			t.Fatal(err)
		}
		if at := slices.Index(argv, "--output"); at < 0 || argv[at+1] != "type=cacheonly" ||
			slices.Contains(argv, "--load") || slices.Contains(argv, containerdImageExport) || slices.Contains(argv, "--tag") {
			t.Fatalf("cache-only containerd=%v argv = %v", containerd, argv)
		}
	}
}

func TestContainerdSnapshotterIsReadFromDriverStatus(t *testing.T) {
	for name, test := range map[string]struct {
		status [][2]string
		want   bool
	}{
		"containerd": {status: [][2]string{{"driver-type", "io.containerd.snapshotter.v1"}}, want: true},
		"overlay2": {status: [][2]string{
			{"Backing Filesystem", "extfs"}, {"Supports d_type", "true"}, {"Native Overlay Diff", "true"},
		}},
		"empty": {},
	} {
		if got := usesContainerdSnapshotter(system.Info{DriverStatus: test.status}); got != test.want {
			t.Fatalf("%s: containerd = %v, want %v", name, got, test.want)
		}
	}
}

// A daemon that does not answer is not remembered as classic: the build
// loads (which works on both stores) and the next build asks again.
func TestAnUnansweredImageStoreReadIsNotRemembered(t *testing.T) {
	client := &Client{}
	if client.containerdImageStore(context.Background()) {
		t.Fatal("an unreachable daemon read as containerd's store")
	}
	if client.imageStore.known {
		t.Fatal("a failed read was remembered")
	}
	client.imageStore.known, client.imageStore.containerd = true, true
	if !client.containerdImageStore(context.Background()) {
		t.Fatal("a remembered answer was asked again")
	}
}
