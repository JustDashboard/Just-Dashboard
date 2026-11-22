package dockerx

import (
	"context"
	"sync"

	"github.com/docker/docker/api/types/system"
)

// containerdImageExport is how a build reaches the daemon's image store when
// that store is containerd's. `--load` there compresses every new layer with
// gzip, writes the blob, and unpacks it again into a snapshot: on a 1.1 GB
// node_modules layer, 80 s exporting and 53 s unpacking. zstd at level 3
// took that to 35 s and 20 s with a smaller blob (303 MB against 336 MB).
//
//   - Level 1 was no faster: the export of a layer that size is bound by
//     reading and hashing it, not by the compressor.
//   - Uncompressed blobs were slower to write and three times the disk.
//   - force-compression is left off: base layers keep the blobs they were
//     pulled with instead of being stored a second time as zstd.
//
// unpack=true is what makes the image runnable at once, as `--load` is, and
// the tag comes from `--tag`. A zstd layer needs OCI media types, which
// BuildKit would choose anyway; saying so keeps the manifest's shape fixed.
//
// Everything that reads these images reads them through the daemon (run,
// tag, inspect) or through `docker save`, which the vulnerability scanner's
// Trivy reads, mixed gzip and zstd layers included.
const containerdImageExport = "type=image,unpack=true,compression=zstd,compression-level=3,oci-mediatypes=true"

// imageStoreReading remembers whether the daemon's images live in
// containerd's store. Switching stores needs a daemon restart, which this
// process's container does not outlive, so one answer holds for its life.
// A failed read is not remembered and the build keeps `--load`, which works
// on both stores.
type imageStoreReading struct {
	mu         sync.Mutex
	known      bool
	containerd bool
}

// containerdImageStore reports whether the daemon keeps images in
// containerd's content store (the containerd snapshotter) rather than in a
// classic graph driver, which BuildKit writes into directly with nothing to
// compress.
func (c *Client) containerdImageStore(ctx context.Context) bool {
	if c == nil {
		return false
	}
	c.imageStore.mu.Lock()
	defer c.imageStore.mu.Unlock()
	if c.imageStore.known {
		return c.imageStore.containerd
	}
	info, err := c.Info(ctx)
	if err != nil {
		return false
	}
	c.imageStore.known, c.imageStore.containerd = true, usesContainerdSnapshotter(info)
	return c.imageStore.containerd
}

// usesContainerdSnapshotter reads `docker info`'s driver status, where the
// containerd store says ["driver-type", "io.containerd.snapshotter.v1"].
func usesContainerdSnapshotter(info system.Info) bool {
	for _, pair := range info.DriverStatus {
		if len(pair) == 2 && pair[0] == "driver-type" && pair[1] == "io.containerd.snapshotter.v1" {
			return true
		}
	}
	return false
}
