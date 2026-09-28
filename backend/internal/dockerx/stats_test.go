package dockerx

import (
	"testing"

	"github.com/docker/docker/api/types/container"
)

// Docker's one-shot stats endpoint returns precpu_stats zeroed. Subtracting
// zero turns the usual arithmetic into "this container's whole lifetime over
// the host's whole uptime" — an average since start, presented as the reading
// now. A long-lived container that is working hard reads as nearly idle, which
// is the failure this guards.
func TestOneShotSampleClaimsNoPercentageOfItsOwn(t *testing.T) {
	raw := container.StatsResponse{}
	raw.CPUStats.CPUUsage.TotalUsage = 900_000_000_000 // 900s of CPU since start
	raw.CPUStats.SystemUsage = 90_000_000_000_000      // host has been up far longer
	raw.CPUStats.OnlineCPUs = 4

	st := convertStats("abc", raw)
	if st.CPUPercent != 0 {
		t.Fatalf("cpu = %v from a sample with no predecessor; it should report none", st.CPUPercent)
	}
	if st.CPUTotal != raw.CPUStats.CPUUsage.TotalUsage || st.SystemCPU != raw.CPUStats.SystemUsage {
		t.Fatal("the cumulative counters a caller needs to difference were not carried through")
	}
}

// The streaming endpoint does fill precpu_stats, and that sample is complete
// on its own.
func TestStreamedSampleUsesItsOwnPredecessor(t *testing.T) {
	raw := container.StatsResponse{}
	raw.PreCPUStats.CPUUsage.TotalUsage = 100_000_000_000
	raw.PreCPUStats.SystemUsage = 1_000_000_000_000
	raw.CPUStats.CPUUsage.TotalUsage = 110_000_000_000 // 10s of CPU
	raw.CPUStats.SystemUsage = 1_040_000_000_000       // over 40s of host CPU
	raw.CPUStats.OnlineCPUs = 4

	// 10/40 of the host's four cores is one whole core: 100%.
	if got := convertStats("abc", raw).CPUPercent; got != 100 {
		t.Fatalf("cpu = %v, want 100", got)
	}
}

func TestSamplerDerivesPercentFromTwoSamples(t *testing.T) {
	s := (&Client{}).NewStatsSampler()

	first := ContainerStats{ID: "abc", CPUTotal: 100_000_000_000, SystemCPU: 1_000_000_000_000}
	s.fillCPU("abc", &first, 4)
	if first.CPUPercent != 0 {
		t.Fatalf("first sample reported %v%%; there is nothing to difference against yet", first.CPUPercent)
	}

	second := ContainerStats{ID: "abc", CPUTotal: 110_000_000_000, SystemCPU: 1_040_000_000_000}
	s.fillCPU("abc", &second, 4)
	if second.CPUPercent != 100 {
		t.Fatalf("cpu = %v, want 100", second.CPUPercent)
	}
}

// A counter that goes backwards means the host rebooted or the id was reused.
// Reporting the resulting enormous delta as a spike would be worse than
// reporting nothing, and it would be stored for a week.
func TestCounterResetIsNotASpike(t *testing.T) {
	s := (&Client{}).NewStatsSampler()
	first := ContainerStats{ID: "abc", CPUTotal: 500_000_000_000, SystemCPU: 9_000_000_000_000}
	s.fillCPU("abc", &first, 4)

	after := ContainerStats{ID: "abc", CPUTotal: 1_000_000, SystemCPU: 2_000_000}
	s.fillCPU("abc", &after, 4)
	if after.CPUPercent != 0 {
		t.Fatalf("cpu = %v after a counter reset, want 0", after.CPUPercent)
	}
}

// A host that recreates containers often would otherwise carry every dead
// container's counters for the life of the process.
func TestSamplerForgetsContainersItNoLongerSees(t *testing.T) {
	s := (&Client{}).NewStatsSampler()
	for _, id := range []string{"a", "b", "c"} {
		st := ContainerStats{ID: id, CPUTotal: 1, SystemCPU: 1}
		s.fillCPU(id, &st, 1)
	}
	s.forget(map[string]struct{}{"b": {}})

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.prev) != 1 {
		t.Fatalf("holding %d containers, want only the one still running", len(s.prev))
	}
	if _, ok := s.prev["b"]; !ok {
		t.Fatal("forgot the container that is still running")
	}
}

// The one-shot stats endpoint returns no PreCPUStats, so convertStats gives up
// on the CPU percentage and lets StatsSampler derive it from two samples. That
// early return used to sit above the counter loops, which meant every caller
// that samples rather than streams — the container table and the metrics
// recorder — recorded a permanent zero for block I/O that Docker was reporting
// perfectly well.
func TestConvertStatsReadsCountersOnTheOneShotPath(t *testing.T) {
	raw := container.StatsResponse{
		MemoryStats: container.MemoryStats{Usage: 100, Limit: 1000},
		CPUStats: container.CPUStats{
			CPUUsage:    container.CPUUsage{TotalUsage: 5_000},
			SystemUsage: 9_000,
		},
		// Zero, exactly as the one-shot endpoint leaves it.
		PreCPUStats: container.CPUStats{},
		BlkioStats: container.BlkioStats{
			IoServiceBytesRecursive: []container.BlkioStatEntry{
				{Op: "read", Value: 4096},
				{Op: "Write", Value: 8192},
			},
		},
		Networks: map[string]container.NetworkStats{
			"eth0": {RxBytes: 111, TxBytes: 222},
		},
	}

	s := convertStats("abc", raw)

	if s.BlockRead != 4096 || s.BlockWrite != 8192 {
		t.Errorf("block io = %d/%d, want 4096/8192", s.BlockRead, s.BlockWrite)
	}
	if s.NetRx != 111 || s.NetTx != 222 {
		t.Errorf("network = %d/%d, want 111/222", s.NetRx, s.NetTx)
	}
	// The percentage is still withheld: one reading of a cumulative counter is
	// not a rate, and claiming otherwise is what the early return prevents.
	if s.CPUPercent != 0 {
		t.Errorf("cpu percent = %v, want 0 without a predecessor sample", s.CPUPercent)
	}
	if s.CPUTotal != 5_000 || s.SystemCPU != 9_000 {
		t.Errorf("cumulative cpu = %d/%d, want the raw counters passed through", s.CPUTotal, s.SystemCPU)
	}
}

// A container on the host's network namespace has no per-container interface,
// so Docker omits `networks` entirely. That is an absence of the thing, not a
// missing reading, and must not be confused with the bug above.
func TestConvertStatsToleratesHostNetworking(t *testing.T) {
	s := convertStats("abc", container.StatsResponse{
		MemoryStats: container.MemoryStats{Usage: 10, Limit: 100},
		BlkioStats: container.BlkioStats{
			IoServiceBytesRecursive: []container.BlkioStatEntry{{Op: "read", Value: 7}},
		},
	})
	if s.NetRx != 0 || s.NetTx != 0 {
		t.Errorf("network = %d/%d, want 0 when Docker reports no interfaces", s.NetRx, s.NetTx)
	}
	if s.BlockRead != 7 {
		t.Errorf("block read = %d, want 7 — the other counters still apply", s.BlockRead)
	}
}

func TestConvertStatsSeparatesIdleAndMissingCounters(t *testing.T) {
	raw := container.StatsResponse{Networks: map[string]container.NetworkStats{
		"eth0": {RxPackets: 3, TxErrors: 2, RxDropped: 1},
	}}
	idle := convertStats("abc", raw)
	if !idle.NetworkAvailable || idle.NetRx != 0 || idle.BlockAvailable {
		t.Fatalf("availability = %+v", idle)
	}
	if n := idle.Networks["eth0"]; n.RxPackets != 3 || n.TxErrors != 2 || n.RxDropped != 1 {
		t.Fatalf("interface counters = %+v", n)
	}
	missing := convertStats("abc", container.StatsResponse{})
	if missing.NetworkAvailable || missing.MemRSS != nil || missing.MemSwap != nil {
		t.Fatalf("missing counters were fabricated: %+v", missing)
	}
}

func TestConvertStatsMemoryMatchesDockerCLI(t *testing.T) {
	for _, tc := range []struct {
		name           string
		stats          map[string]uint64
		working, cache uint64
	}{
		{"cgroup v1 hierarchy wins", map[string]uint64{"total_inactive_file": 300, "inactive_file": 100}, 700, 300},
		{"cgroup v2", map[string]uint64{"inactive_file": 200}, 800, 200},
		{"invalid cache cannot underflow", map[string]uint64{"inactive_file": 2000}, 1000, 0},
		{"no cache", nil, 1000, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := convertStats("abc", container.StatsResponse{MemoryStats: container.MemoryStats{Usage: 1000, Stats: tc.stats}})
			if got.MemRaw != 1000 || got.MemUsage != tc.working || got.MemCache != tc.cache {
				t.Fatalf("raw/working/cache = %d/%d/%d", got.MemRaw, got.MemUsage, got.MemCache)
			}
		})
	}
	got := convertStats("abc", container.StatsResponse{MemoryStats: container.MemoryStats{
		Stats: map[string]uint64{"anon": 400, "swap": 0},
	}})
	if got.MemRSS == nil || *got.MemRSS != 400 || got.MemSwap == nil || *got.MemSwap != 0 {
		t.Fatalf("reported zero must be distinct from missing: %+v", got)
	}
}

func TestSamplerDistinguishesIdleCPUFromUnmeasuredCPU(t *testing.T) {
	s := (&Client{}).NewStatsSampler()
	first := ContainerStats{CPUTotal: 100, SystemCPU: 1000}
	s.fillCPU("abc", &first, 4)
	if first.CPUReady {
		t.Fatal("first sample is not an interval")
	}
	idle := ContainerStats{CPUTotal: 100, SystemCPU: 2000}
	s.fillCPU("abc", &idle, 4)
	if !idle.CPUReady || idle.CPUPercent != 0 {
		t.Fatalf("idle CPU = %+v", idle)
	}
	reset := ContainerStats{CPUTotal: 1, SystemCPU: 3000}
	s.fillCPU("abc", &reset, 4)
	if reset.CPUReady {
		t.Fatal("a reset is not an idle interval")
	}
}
