package dockerx

import (
	"context"
	"os"
	"testing"
	"time"
)

// Read-only acceptance against an operator-selected running container. No
// fixture is created and no workload on the shared Docker host is modified.
func TestLiveContainerUsageStream(t *testing.T) {
	id := os.Getenv("JD_DOCKER_STATS_CONTAINER")
	if id == "" {
		t.Skip("set JD_DOCKER_STATS_CONTAINER to a running container for read-only acceptance")
	}
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	c := New(host)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	frames := make(chan ContainerStats, 8)
	done := make(chan error, 1)
	go func() { done <- c.StatsStream(ctx, id, frames) }()
	var first *ContainerStats
	for {
		select {
		case <-ctx.Done():
			t.Fatal("no pair of Docker samples before timeout")
		case err := <-done:
			t.Fatalf("stream ended before measurements: %v", err)
		case st := <-frames:
			if st.MemRaw == 0 || st.MemUsage+st.MemCache != st.MemRaw {
				t.Fatalf("memory breakdown does not reconcile: %+v", st)
			}
			if st.NetworkAvailable != (len(st.Networks) > 0) {
				t.Fatal("network availability disagrees with actual interfaces")
			}
			var rx, tx uint64
			for _, n := range st.Networks {
				rx += n.RxBytes
				tx += n.TxBytes
			}
			if rx != st.NetRx || tx != st.NetTx {
				t.Fatal("network totals disagree with interfaces")
			}
			if first != nil && st.TS.After(first.TS) && st.CPUReady {
				t.Logf("Docker interval %s: CPU %.2f%%, working set %d B, %d interfaces, received %d B, sent %d B", st.TS.Sub(first.TS), st.CPUPercent, st.MemUsage, len(st.Networks), st.NetRx, st.NetTx)
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("stream did not stop after cancellation")
				}
				return
			}
			first = &st
		}
	}
}
