package dockerx

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dtypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
)

func cacheTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return &Client{cli: cli, host: server.URL}
}

func cacheTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func awaitCacheSignal(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for cache read")
	}
}

func TestDiskUsageSharesColdReadWithoutSharingCancellation(t *testing.T) {
	entered, hold := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(hold) })
	defer release()
	var calls atomic.Int32
	c := cacheTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-hold
		fmt.Fprint(w, `{"LayersSize":42}`)
	})
	ctx := cacheTestContext(t)
	firstCtx, cancel := context.WithCancel(ctx)
	first := make(chan *dtypes.DiskUsage, 1)
	go func() { first <- c.diskUsage(firstCtx) }()
	awaitCacheSignal(t, entered)
	second := make(chan *dtypes.DiskUsage, 1)
	go func() { second <- c.diskUsage(ctx) }()
	cancel()
	if got := <-first; got != nil {
		t.Fatal("cancelled reader received a snapshot")
	}
	release()
	if got := <-second; got == nil || got.LayersSize != 42 {
		t.Fatalf("remaining reader = %+v", got)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("cold readers started %d walks, want one", got)
	}
}

func TestDiskUsageInvalidationRejectsInFlightRead(t *testing.T) {
	entered, hold := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(hold) })
	defer release()
	var calls atomic.Int32
	c := cacheTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		read := calls.Add(1)
		if read == 1 {
			close(entered)
			<-hold
		}
		fmt.Fprintf(w, `{"LayersSize":%d}`, read)
	})
	ctx := cacheTestContext(t)
	old := make(chan *dtypes.DiskUsage, 1)
	go func() { old <- c.diskUsage(ctx) }()
	awaitCacheSignal(t, entered)
	c.forgetDiskUsage()
	if got := c.diskUsage(ctx); got == nil || got.LayersSize != 2 {
		t.Fatalf("post-mutation reader joined the obsolete walk: %+v", got)
	}
	release()
	if got := <-old; got == nil || got.LayersSize != 2 {
		t.Fatalf("obsolete read escaped invalidation: %+v", got)
	}
	if got := c.diskUsage(ctx); got == nil || got.LayersSize != 2 || calls.Load() != 2 {
		t.Fatalf("old fill replaced the current cache: %+v, calls=%d", got, calls.Load())
	}
}

func TestDiskUsageKeepsBackgroundRefreshButBoundsStaleAnswers(t *testing.T) {
	entered, hold := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(hold) })
	defer release()
	var calls atomic.Int32
	c := cacheTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
			<-hold
		}
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"message":"disk unavailable"}`)
	})
	c.duVal, c.duAt = &dtypes.DiskUsage{LayersSize: 42}, time.Now().Add(-diskUsageTTL-time.Second)
	ctx := cacheTestContext(t)
	if got := c.diskUsage(ctx); got == nil || got.LayersSize != 42 {
		t.Fatalf("background refresh discarded a usable snapshot: %+v", got)
	}
	awaitCacheSignal(t, entered)
	c.duMu.Lock()
	flight := c.duFlight
	c.duMu.Unlock()
	release()
	awaitCacheSignal(t, flight.done)
	c.duMu.Lock()
	c.duAt = time.Now().Add(-diskUsageMaxAge - time.Second)
	c.duMu.Unlock()
	if got := c.diskUsage(ctx); got != nil {
		t.Fatalf("expired snapshot survived another failed refresh: %+v", got)
	}
}

func TestDiskUsageInvalidatesAfterFailedMutations(t *testing.T) {
	actions := map[string]func(*Client, context.Context){
		"remove image":      func(c *Client, ctx context.Context) { _, _ = c.RemoveImage(ctx, "image", false, false) },
		"remove container":  func(c *Client, ctx context.Context) { _ = c.RemoveContainer(ctx, "container", false, false) },
		"prune images":      func(c *Client, ctx context.Context) { _, _ = c.PruneImages(ctx, false) },
		"prune containers":  func(c *Client, ctx context.Context) { _, _, _ = c.PruneContainers(ctx) },
		"prune volumes":     func(c *Client, ctx context.Context) { _, _ = c.PruneVolumes(ctx) },
		"prune build cache": func(c *Client, ctx context.Context) { _, _ = c.PruneBuildCache(ctx, false) },
		"pull": func(c *Client, ctx context.Context) {
			_ = c.PullImage(ctx, "example/app:latest", make(chan PullProgress, 1))
		},
	}
	for name, action := range actions {
		t.Run(name, func(t *testing.T) {
			c := cacheTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, `{"message":"failed after partial progress"}`)
			})
			c.duVal, c.duAt = &dtypes.DiskUsage{LayersSize: 42}, time.Now()
			action(c, cacheTestContext(t))
			if c.duVal != nil {
				t.Fatal("mutation retained a snapshot from before partial progress")
			}
		})
	}
}
