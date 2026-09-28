package accesslog

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestSlowExportDoesNotBlockRefreshAndKeepsItsSnapshot(t *testing.T) {
	reader, clock := newFake()
	reader.append(line(clock.Now().Add(-time.Minute), "/first", 200))
	reader.append(line(clock.Now(), "/second", 200))
	store := newTestStore(reader, clock)
	started, finish, exported := make(chan struct{}), make(chan struct{}), make(chan int, 1)
	var resume sync.Once
	defer resume.Do(func() { close(finish) })
	go func() {
		first := true
		n, err := store.Export(t.Context(), "r", Filter{}, 10, func(Entry) {
			if first {
				first = false
				close(started)
				<-finish
			}
		})
		if err != nil {
			t.Error(err)
		}
		exported <- n
	}()
	<-started
	clock.Advance(time.Second)
	reader.append(line(clock.Now(), "/new", 200))
	window := make(chan Window, 1)
	go func() {
		got, err := store.Window(t.Context(), "r", Filter{})
		if err != nil {
			t.Error(err)
		}
		window <- got
	}()
	select {
	case got := <-window:
		if got.Result.Summary.Total != 3 {
			t.Fatalf("new request missing from refresh: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("slow download held the route lock")
	}
	// Retention must allocate a new backing array while this export reads the old one.
	rec := store.record("r", clock.Now())
	rec.mu.Lock()
	rec.trim(clock.Now().Add(keepFor + time.Hour))
	rec.mu.Unlock()
	resume.Do(func() { close(finish) })
	if count := <-exported; count != 2 {
		t.Fatalf("export crossed its snapshot: %d", count)
	}
}

func BenchmarkStoreWindowConcurrent(b *testing.B) {
	reader, clock := newFake()
	store := newTestStore(reader, clock)
	rec := store.record("r", clock.Now())
	for i := range 150_000 {
		rec.feed(line(clock.Now().Add(time.Duration(i-150_000)*time.Millisecond), "/api", 200))
	}
	rec.seeded, rec.exists, rec.complete, rec.refreshed = true, true, true, clock.Now()
	filter := Filter{Since: clock.Now().Add(-time.Hour), Until: clock.Now(), Limit: 200}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := store.Cached(context.Background(), "r", filter, time.Hour); err != nil {
				b.Error(err)
			}
		}
	})
}
