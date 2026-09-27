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

	"github.com/docker/docker/client"
)

func readFixture(t *testing.T) (*Client, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var lists, inspections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1.47/containers/json":
			lists.Add(1)
			fmt.Fprint(w, `[{"Id":"db","Names":["/db"],"Image":"postgres:16","State":"running"}]`)
		case "/v1.47/containers/db/json":
			inspections.Add(1)
			fmt.Fprint(w, `{"Id":"db","Name":"/db","Config":{},"State":{"StartedAt":"2026-09-01T00:00:00Z"},"HostConfig":{}}`)
		case "/v1.47/containers/db/stats":
			fmt.Fprint(w, `{"read":"2026-09-27T00:00:00Z","cpu_stats":{"cpu_usage":{"total_usage":100},"system_cpu_usage":1000,"online_cpus":4}}`)
		case "/v1.47/info":
			fmt.Fprint(w, `{"MemTotal":8589934592,"NCPU":4}`)
		case "/v1.47/system/df":
			fmt.Fprint(w, `{"Containers":[]}`)
		default:
			t.Errorf("unexpected daemon request: %s", r.URL)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	api, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	owner := &Client{cli: api}
	t.Cleanup(func() { _ = owner.Close() })
	return owner, &lists, &inspections
}

func TestRequestSnapshotSharesDiscoveryAndStartsFreshNextRequest(t *testing.T) {
	c, lists, inspections := readFixture(t)
	ctx := c.WithReadSnapshot(t.Context())
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			items, err := c.ListContainers(ctx, false)
			if err != nil || len(items) != 1 {
				t.Errorf("list: %v, %v", items, err)
				return
			}
			detail, err := c.Inspect(ctx, items[0].ID)
			if err != nil || detail.Name != "db" {
				t.Errorf("inspect: %v, %v", detail, err)
			}
		})
	}
	wg.Wait()
	if lists.Load() != 1 || inspections.Load() != 1 {
		t.Fatalf("lists=%d, inspections=%d", lists.Load(), inspections.Load())
	}
	if _, err := c.ListContainers(c.WithReadSnapshot(t.Context()), false); err != nil {
		t.Fatal(err)
	}
	if lists.Load() != 2 || inspections.Load() != 2 {
		t.Fatal("a new request reused stale discovery")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.ListContainers(ctx, false); err == nil {
		t.Fatal("canceled reader ignored cancellation")
	}
}

func TestContainerViewersShareCollectionAndReleaseIt(t *testing.T) {
	c, lists, inspections := readFixture(t)
	var cancels []func()
	for range 5 {
		updates, cancel := c.SubscribeContainers()
		cancels = append(cancels, cancel)
		defer cancel()
		for _, kind := range []string{"containers", "stats"} {
			select {
			case update := <-updates:
				if update.Err != nil || update.Kind != kind {
					t.Fatalf("update: %+v", update)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("stream did not deliver")
			}
		}
	}
	if lists.Load() != 1 || inspections.Load() != 1 {
		t.Fatalf("five viewers collected %d inventories / %d inspections", lists.Load(), inspections.Load())
	}
	for _, cancel := range cancels {
		cancel()
	}
	c.feedMu.Lock()
	active := c.feed != nil
	c.feedMu.Unlock()
	if active {
		t.Fatal("sampler retained with no viewers")
	}
	updates, cancel := c.SubscribeContainers()
	defer cancel()
	select {
	case <-updates:
	case <-time.After(5 * time.Second):
		t.Fatal("replacement stream did not start")
	}
	if lists.Load() != 2 {
		t.Fatal("replacement stream replayed a previous visit")
	}
}
