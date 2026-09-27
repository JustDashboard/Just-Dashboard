package dockerx

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dtypes "github.com/docker/docker/api/types"
)

func registryTestDigest(letter string) string { return "sha256:" + strings.Repeat(letter, 64) }

func TestImageUpdatesCacheRemoteFactsAndRecheckLocalImages(t *testing.T) {
	var local, remote atomic.Value
	local.Store(registryTestDigest("a"))
	remote.Store(registryTestDigest("b"))
	var inspections, manifests atomic.Int32
	c := cacheTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1.47/images/"):
			inspections.Add(1)
			fmt.Fprintf(w, `{"Id":"image","RepoDigests":["example/app@%s"]}`, local.Load())
		case strings.HasPrefix(r.URL.Path, "/v1.47/distribution/"):
			manifests.Add(1)
			fmt.Fprintf(w, `{"Descriptor":{"digest":%q}}`, remote.Load())
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	ctx := cacheTestContext(t)
	first := c.CheckUpdate(ctx, "example/app:latest", false)
	second := c.CheckUpdate(ctx, "example/app:latest", false)
	if first.State != "outdated" || second.State != "outdated" || !first.CheckedAt.Equal(second.CheckedAt) ||
		inspections.Load() != 2 || manifests.Load() != 1 {
		t.Fatalf("first=%+v second=%+v inspections=%d manifests=%d", first, second, inspections.Load(), manifests.Load())
	}
	// Another operator pulled outside the dashboard, after the registry moved
	// again. Reusing either the old verdict or the old manifest would be wrong.
	local.Store(registryTestDigest("c"))
	remote.Store(registryTestDigest("c"))
	if got := c.CheckUpdate(ctx, "example/app:latest", false); got.State != "current" || manifests.Load() != 2 {
		t.Fatalf("out-of-band pull did not refresh the comparison: %+v", got)
	}
	remote.Store(registryTestDigest("d"))
	if got := c.CheckUpdate(ctx, "example/app:latest", true); got.State != "outdated" || manifests.Load() != 3 {
		t.Fatalf("forced check reused a manifest: %+v", got)
	}
}

func TestImageUpdateCachesAreScopedToTheDockerClient(t *testing.T) {
	for _, letter := range []string{"a", "b"} {
		c := cacheTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/distribution/") {
				fmt.Fprintf(w, `{"Descriptor":{"digest":%q}}`, registryTestDigest(letter))
			} else {
				fmt.Fprintf(w, `{"Id":"image","RepoDigests":["example/app@%s"]}`, registryTestDigest("a"))
			}
		})
		if got := c.CheckUpdate(cacheTestContext(t), "example/app:latest", false); got.RemoteDigest != registryTestDigest(letter) {
			t.Fatalf("another daemon's answer leaked into this client: %+v", got)
		}
	}
}

func TestImageUpdateFailuresAreNotCached(t *testing.T) {
	var manifests atomic.Int32
	c := cacheTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/distribution/") {
			if manifests.Add(1) == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, `{"message":"registry unavailable"}`)
				return
			}
			fmt.Fprintf(w, `{"Descriptor":{"digest":%q}}`, registryTestDigest("a"))
		} else {
			fmt.Fprintf(w, `{"Id":"image","RepoDigests":["example/app@%s"]}`, registryTestDigest("a"))
		}
	})
	ctx := cacheTestContext(t)
	if got := c.CheckUpdate(ctx, "example/app:latest", false); got.State != "unknown" {
		t.Fatalf("failed registry check = %+v", got)
	}
	if got := c.CheckUpdate(ctx, "example/app:latest", false); got.State != "current" || manifests.Load() != 2 {
		t.Fatalf("cached failure hid registry recovery: %+v", got)
	}
}

func TestForcedImageCheckDiscardsAnOlderManifestRead(t *testing.T) {
	entered, hold := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(hold) })
	defer release()
	var manifests atomic.Int32
	c := cacheTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/distribution/") {
			digest := registryTestDigest("b")
			if manifests.Add(1) == 1 {
				digest = registryTestDigest("a")
				close(entered)
				<-hold
			}
			fmt.Fprintf(w, `{"Descriptor":{"digest":%q}}`, digest)
		} else {
			fmt.Fprintf(w, `{"Id":"image","RepoDigests":["example/app@%s"]}`, registryTestDigest("a"))
		}
	})
	ctx := cacheTestContext(t)
	old := make(chan UpdateStatus, 1)
	go func() { old <- c.CheckUpdate(ctx, "example/app:latest", false) }()
	awaitCacheSignal(t, entered)
	if got := c.CheckUpdate(ctx, "example/app:latest", true); got.RemoteDigest != registryTestDigest("b") {
		t.Fatalf("forced check joined the old request: %+v", got)
	}
	release()
	if got := <-old; got.RemoteDigest != registryTestDigest("b") {
		t.Fatalf("old manifest escaped invalidation: %+v", got)
	}
	if manifests.Load() != 2 {
		t.Fatalf("invalidated reader did not reuse the fresh result: %d requests", manifests.Load())
	}
}

func TestOrdinaryImagePullInvalidatesRegistryAndDiskCaches(t *testing.T) {
	c := cacheTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1.47/images/create" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `{"status":"done"}`)
	})
	c.duVal, c.duAt = &dtypes.DiskUsage{LayersSize: 42}, time.Now()
	c.updates = map[string]updateEntry{
		"example/app:latest\x00old":   {digest: registryTestDigest("a"), at: time.Now()},
		"example/other:latest\x00old": {digest: registryTestDigest("b"), at: time.Now()},
	}
	if err := c.PullImage(cacheTestContext(t), "example/app:latest", make(chan PullProgress, 1)); err != nil {
		t.Fatal(err)
	}
	if c.duVal != nil || len(c.updates) != 1 || c.updates["example/other:latest\x00old"].digest == "" {
		t.Fatalf("pull retained stale data or evicted an unrelated tag: disk=%+v manifests=%+v", c.duVal, c.updates)
	}
}
