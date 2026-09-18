package dockerx

import (
	"context"
	"os"
	"testing"
	"time"

	dtypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
)

// The live disk tests follow the same bargain as dbx's: they drive a real
// daemon and skip when there is not one, so `go test ./...` stays green on a
// bare machine and gets stricter on an equipped one.
//
// They exist because the bug they pin could not be caught any other way. The
// reclaimable figure this dashboard puts on screen is a promise about what a
// button will free, and the only authority on whether it matches is the
// daemon's own accounting. A fake would have agreed with whatever formula was
// written here, including the one that over-reported an unused image by every
// layer it shared with a running one.
func liveClient(t *testing.T) (*Client, *dtypes.DiskUsage) {
	t.Helper()
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	c := New(host)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cli, err := c.api()
	if err != nil {
		t.Skipf("no docker on this host (set DOCKER_HOST to point at one): %v", err)
	}
	if _, err := cli.Ping(ctx); err != nil {
		t.Skipf("docker is not answering on %s: %v", host, err)
	}
	du, err := cli.DiskUsage(ctx, dtypes.DiskUsageOptions{})
	if err != nil {
		t.Skipf("docker refused a disk-usage walk: %v", err)
	}
	return c, &du
}

func requireDisposablePruneDaemon(t *testing.T) {
	t.Helper()
	// A reachable development daemon may be a real server. Preflight checks
	// and reserved-space calculations cannot exclude concurrent changes.
	if os.Getenv("JD_DOCKER_PRUNE_LIVE") != "1" || os.Getenv("DOCKER_HOST") == "" {
		t.Skip("set JD_DOCKER_PRUNE_LIVE=1 and DOCKER_HOST to an isolated disposable daemon")
	}
}

// TestLiveDiskUsageMatchesDockerSystemDF pins every line of the breakdown to
// the arithmetic `docker system df` performs, because the numbers are the
// product's claim and getting one backwards is invisible until somebody
// presses the button beside it.
func TestLiveDiskUsageMatchesDockerSystemDF(t *testing.T) {
	c, raw := liveClient(t)
	got, err := c.DiskUsage(context.Background())
	if err != nil {
		t.Fatalf("DiskUsage: %v", err)
	}

	// Images: Docker's reclaimable is the unused images' *unshared* bytes.
	// Summing their full size is the tempting version and is wrong — a layer
	// an unused image has in common with a running one is not freed when the
	// unused image goes.
	var wantImages int64
	wantImagesActive := 0
	for _, i := range raw.Images {
		if i.Containers > 0 {
			wantImagesActive++
			continue
		}
		if i.Size >= 0 && i.SharedSize >= 0 {
			wantImages += i.Size - i.SharedSize
		}
	}
	if got.ImagesLine.Reclaimable != wantImages {
		t.Errorf("images reclaimable = %d, docker says %d", got.ImagesLine.Reclaimable, wantImages)
	}
	if got.ImagesLine.Active != wantImagesActive {
		t.Errorf("images active = %d, want %d", got.ImagesLine.Active, wantImagesActive)
	}
	if got.ImagesLine.Size != raw.LayersSize {
		t.Errorf("images size = %d, docker's LayersSize is %d", got.ImagesLine.Size, raw.LayersSize)
	}

	// Build cache: a shared record's bytes belong to the record it is shared
	// with, so the size line must skip them or the total exceeds the disk.
	var wantSize, inUse int64
	for _, b := range raw.BuildCache {
		if !b.Shared {
			wantSize += b.Size
		}
		if b.InUse {
			inUse += b.Size
		}
	}
	if got.BuildCacheLine.Size != wantSize {
		t.Errorf("build cache size = %d, docker says %d", got.BuildCacheLine.Size, wantSize)
	}
	if want := wantSize - inUse; got.BuildCacheLine.Reclaimable != want {
		t.Errorf("build cache reclaimable = %d, docker says %d", got.BuildCacheLine.Reclaimable, want)
	}

	// The headline figure must never quietly include volumes: it is offered
	// with "this is safe", and a volume is the one thing here that is not.
	if got.Reclaimable() != got.ImagesLine.Reclaimable+got.ContainersLine.Reclaimable+got.BuildCacheLine.Reclaimable {
		t.Error("the headline reclaimable figure is not the sum of the safe lines")
	}
	t.Logf("images %d B reclaimable of %d, build cache %d B of %d, containers %d B, volumes %d B (excluded from the headline)",
		got.ImagesLine.Reclaimable, got.ImagesLine.Size,
		got.BuildCacheLine.Reclaimable, got.BuildCacheLine.Size,
		got.ContainersLine.Reclaimable, got.VolumesLine.Reclaimable)
}

// TestLiveBuildCachePruneRoundTrips proves the call this product could not
// make until 0.6.4 — the helper existed and nothing called it, so the
// dashboard reported tens of gigabytes of reclaimable cache with no route able
// to touch any of it.
//
// It reserves more space than the cache occupies, so BuildKit keeps every
// entry. That is deliberate: the point under test is that the request reaches
// the daemon and comes back as a well-formed report, and a test that proved it
// by deleting the operator's build cache would be a test nobody dares run.
func TestLiveBuildCachePruneRoundTrips(t *testing.T) {
	requireDisposablePruneDaemon(t)
	c, raw := liveClient(t)
	cli, err := c.api()
	if err != nil {
		t.Skip(err)
	}
	var total int64
	for _, b := range raw.BuildCache {
		if !b.Shared {
			total += b.Size
		}
	}
	rep, err := cli.BuildCachePrune(context.Background(), build.CachePruneOptions{
		All:           true,
		ReservedSpace: total + (1 << 30),
	})
	if err != nil {
		t.Fatalf("the daemon refused a build-cache prune: %v", err)
	}
	if rep.SpaceReclaimed != 0 {
		t.Errorf("a prune reserving %d bytes freed %d; the reservation was ignored",
			total+(1<<30), rep.SpaceReclaimed)
	}
	t.Logf("build cache prune round-tripped against a live daemon; %d B of cache left in place", total)
}

// TestLiveDiskUsageCacheIsDroppedByAPrune is the other half of the complaint.
// The reading is cached for a minute and served stale-while-revalidating, so a
// prune that does not drop it leaves the page showing the figure it had before
// — which looks exactly like a button that did nothing.
func TestLiveDiskUsageCacheIsDroppedByAPrune(t *testing.T) {
	c, _ := liveClient(t)
	ctx := context.Background()
	if _, err := c.DiskUsage(ctx); err != nil {
		t.Fatalf("DiskUsage: %v", err)
	}
	c.duMu.Lock()
	cached := c.duVal != nil
	c.duMu.Unlock()
	if !cached {
		t.Fatal("a successful read left nothing cached")
	}
	c.forgetDiskUsage()
	c.duMu.Lock()
	stillThere := c.duVal != nil
	c.duMu.Unlock()
	if stillThere {
		t.Fatal("forgetDiskUsage left the stale reading in place; the next page load would redraw the pre-prune figure")
	}
}

// TestLivePruneAllActuallyDeletes is the assertion the whole 0.6.4 fix rests
// on: that the sweep behind the reclaim buttons reaches the daemon and removes
// things, rather than returning a tidy report of four zeroes.
//
// It brings its own rubbish — an unattached network and a container that has
// never run — and refuses to run at all if the host already has objects of
// either kind, because a test that quietly prunes an operator's stopped
// container to prove a point is worse than no test. Build cache and unused
// images are left out of the options for the same reason: this proves deletion
// works, and it can do that with a few kilobytes.
func TestLivePruneAllActuallyDeletes(t *testing.T) {
	requireDisposablePruneDaemon(t)
	c, _ := liveClient(t)
	ctx := context.Background()
	cli, err := c.api()
	if err != nil {
		t.Skip(err)
	}

	stopped, err := c.ListContainers(ctx, true)
	if err != nil {
		t.Skipf("could not list containers: %v", err)
	}
	for _, ct := range stopped {
		if ct.State != "running" {
			t.Skipf("this host has a stopped container (%s) that the sweep would remove; "+
				"the test refuses to prune anything it did not create", ct.Name)
		}
	}
	nets, err := c.ListNetworks(ctx)
	if err != nil {
		t.Skipf("could not list networks: %v", err)
	}
	for _, n := range nets {
		switch n.Name {
		case "bridge", "host", "none":
			continue
		}
		if n.Containers == 0 {
			t.Skipf("this host has an unused network (%s) that the sweep would remove; "+
				"the test refuses to prune anything it did not create", n.Name)
		}
	}

	const probe = "jd-prune-probe"
	netRes, err := c.CreateNetwork(ctx, NetworkSpec{Name: probe + "-net", Driver: "bridge"})
	if err != nil {
		t.Skipf("could not create a probe network: %v", err)
	}
	t.Cleanup(func() { _ = c.RemoveNetwork(context.Background(), netRes.ID) })

	// Any image already on the host will do; the container is never started.
	imgs, err := c.ListImages(ctx, false)
	if err != nil || len(imgs) == 0 {
		t.Skipf("no image on this host to build a probe container from: %v", err)
	}
	ref := ""
	for _, i := range imgs {
		if len(i.RepoTags) > 0 && i.RepoTags[0] != "<none>:<none>" {
			ref = i.RepoTags[0]
			break
		}
	}
	if ref == "" {
		t.Skip("no tagged image on this host to build a probe container from")
	}
	// Start is left false: a container that has never run is exactly the kind
	// the sweep is meant to collect, and nothing here needs it to execute.
	created, err := c.Create(ctx, ContainerSpec{
		Name: probe, Image: ref, Command: []string{"true"}, Start: false,
	}, nil)
	if err != nil {
		t.Skipf("could not create a probe container: %v", err)
	}
	t.Cleanup(func() { _ = c.RemoveContainer(context.Background(), created.ID, true, false) })

	before, err := c.DiskUsage(ctx)
	if err != nil {
		t.Fatalf("DiskUsage before: %v", err)
	}

	reports, err := c.PruneAll(ctx, PruneOptions{})
	if err != nil {
		t.Fatalf("PruneAll: %v", err)
	}
	kinds := map[string]PruneReport{}
	for _, rep := range reports {
		kinds[rep.Kind] = rep
		if rep.Error != "" {
			t.Errorf("the %s step failed: %s", rep.Kind, rep.Error)
		}
	}
	for _, want := range []string{"containers", "images", "networks"} {
		if _, ok := kinds[want]; !ok {
			t.Errorf("the sweep reported no %s line at all", want)
		}
	}

	if _, err := cli.ContainerInspect(ctx, probe); err == nil {
		t.Error("the probe container survived a sweep that reported success")
	}
	if _, err := c.InspectNetwork(ctx, netRes.ID); err == nil {
		t.Error("the probe network survived a sweep that reported success")
	}

	// The conservative sweep must leave the expensive things exactly where they
	// were: it is offered with a dialog that says so.
	after, err := c.DiskUsage(ctx)
	if err != nil {
		t.Fatalf("DiskUsage after: %v", err)
	}
	if after.BuildCacheLine.Size != before.BuildCacheLine.Size {
		t.Errorf("a sweep with BuildCache off changed the cache: %d -> %d",
			before.BuildCacheLine.Size, after.BuildCacheLine.Size)
	}
	if after.VolumesLine.Size != before.VolumesLine.Size {
		t.Errorf("a sweep with Volumes off changed the volumes: %d -> %d",
			before.VolumesLine.Size, after.VolumesLine.Size)
	}
	t.Logf("sweep removed the probe container and network; build cache (%d B) and volumes (%d B) untouched",
		after.BuildCacheLine.Size, after.VolumesLine.Size)
}

// TestLiveWritableLayerFindingFires pins a rule that had never once run.
//
// The check reads SizeRw, and Docker's container listing omits that field
// unless it is explicitly asked for — so the value was always zero and the
// finding was unreachable on every host this has ever run on, while the panel
// went on advertising that it catches data written outside a volume. It is
// asserted against the daemon rather than a fixture because a fixture is what
// let it pass for so long: a hand-built Container with SizeRw set proves the
// comparison, not that anything ever sets it.
func TestLiveWritableLayerFindingFires(t *testing.T) {
	c, raw := liveClient(t)

	var biggest int64
	var name string
	for _, ct := range raw.Containers {
		if ct.SizeRw > biggest {
			biggest, name = ct.SizeRw, ct.ID
		}
	}
	if biggest <= writableLayerWarnBytes {
		t.Skipf("no container on this host has written more than %d bytes to its own layer, "+
			"so there is nothing for the rule to find", writableLayerWarnBytes)
	}

	d, err := c.Diagnose(context.Background())
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	for _, f := range d.Findings {
		if f.ID == "container.writablelayer."+name {
			t.Logf("reported %q (%d B in its writable layer)", f.Title, biggest)
			return
		}
	}
	t.Errorf("a container with %d bytes in its writable layer produced no finding; "+
		"SizeRw is reaching the rule as zero again", biggest)
}
