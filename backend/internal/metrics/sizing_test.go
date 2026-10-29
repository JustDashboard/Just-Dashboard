package metrics

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

func TestPercentileIsNearestRank(t *testing.T) {
	values := make([]float64, 0, 100)
	for i := 100; i >= 1; i-- { // unsorted on purpose
		values = append(values, float64(i))
	}
	for p, want := range map[float64]float64{95: 95, 50: 50, 100: 100, 0: 1, 99.5: 100, 1: 1} {
		if got := Percentile(values, p); got != want {
			t.Errorf("p%v = %v, want %v", p, got, want)
		}
	}
	if got := Percentile(nil, 95); got != 0 {
		t.Errorf("empty p95 = %v", got)
	}
	if values[0] != 100 {
		t.Error("Percentile sorted its caller's slice")
	}
}

// Bucketed samples answer with the upper edge of the bucket the rank lands
// in, which for one-wide buckets is the raw percentile itself.
func TestHistogramPercentileAgreesWithTheRawSamples(t *testing.T) {
	raw := []float64{}
	bins := []Bin{}
	for v := 10; v >= 1; v-- {
		for range v {
			raw = append(raw, float64(v))
		}
		bins = append(bins, Bin{Value: float64(v), Count: v})
	}
	bins = append(bins, Bin{Value: 99, Count: 0})
	for _, p := range []float64{5, 50, 80, 95, 100} {
		if got, want := HistogramPercentile(bins, p), Percentile(raw, p); got != want {
			t.Errorf("p%v: histogram %v, raw %v", p, got, want)
		}
	}
	if got := HistogramPercentile(nil, 95); got != 0 {
		t.Errorf("empty histogram p95 = %v", got)
	}
}

func TestSuggestionsRoundUpToAStepAPersonWouldType(t *testing.T) {
	for in, want := range map[float64]int64{
		1: 64, 64: 64, 64.01: 128, 1000: 1024, 1024: 1024, 1025: 1280, 8192: 8192, 8193: 9216, 1e9: 4 << 20,
	} {
		if got := RoundMemoryMB(in); got != want {
			t.Errorf("RoundMemoryMB(%v) = %d, want %d", in, got, want)
		}
	}
	for in, want := range map[float64]float64{
		0: 0.25, 0.01: 0.25, 0.3: 0.3, 0.31: 0.35, 0.35: 0.35, 0.96: 1, 1: 1, 1.01: 1.25, 2: 2, 2.6: 2.75,
	} {
		if got := RoundCPUs(in); math.Abs(got-want) > 1e-9 {
			t.Errorf("RoundCPUs(%v) = %v, want %v", in, got, want)
		}
	}
}

func mibs(n float64) uint64 { return uint64(n * mib) }

func TestMemoryIsSizedFromItsPercentileAndNeverUnderItsPeak(t *testing.T) {
	// The peak, not the percentile, decides: 500 MiB + 10% is 550, rounded
	// to 576, over the 512 limit.
	tight := SizeMemory(MemoryEvidence{P95: mibs(400), Peak: mibs(500), Limit: 512, LiveLimit: 512})
	if tight.Verdict != VerdictTooTight || tight.SuggestedMB != 576 || tight.OffBy != 1.13 {
		t.Fatalf("near the limit = %+v", tight)
	}
	if !strings.Contains(strings.Join(tight.Reasons, " "), "reached 98% of the limit") {
		t.Errorf("reasons = %q", tight.Reasons)
	}
	right := SizeMemory(MemoryEvidence{P95: mibs(300), Peak: mibs(380), Limit: 512})
	if right.Verdict != VerdictRight || right.SuggestedMB != 448 {
		t.Errorf("a fitting limit = %+v", right)
	}
	big := SizeMemory(MemoryEvidence{P95: mibs(100), Peak: mibs(120), Limit: 2048})
	if big.Verdict != VerdictOversized || big.SuggestedMB != 192 || big.OffBy != 10.67 {
		t.Errorf("a server's worth for nothing = %+v", big)
	}
	// Twice the need but under 256 MiB of difference is not worth a person.
	small := SizeMemory(MemoryEvidence{P95: mibs(40), Peak: mibs(50), Limit: 192})
	if small.Verdict != VerdictRight {
		t.Errorf("a small surplus = %+v", small)
	}
	none := SizeMemory(MemoryEvidence{P95: mibs(40), Peak: mibs(50)})
	if none.Verdict != VerdictUnlimited || none.SuggestedMB != 64 {
		t.Errorf("no limit = %+v", none)
	}
}

// An OOM kill is the spike the samples missed: the suggestion is half again
// the limit it happened under, and once the saved limit is that, the same
// kill does not raise it again.
func TestAnOOMKillRaisesTheSuggestionOnce(t *testing.T) {
	killed := SizeMemory(MemoryEvidence{P95: mibs(100), Peak: mibs(150), Limit: 256, LiveLimit: 256, OOMKills: 2})
	if killed.Verdict != VerdictTooTight || killed.SuggestedMB != 384 {
		t.Fatalf("killed = %+v", killed)
	}
	if !strings.Contains(killed.Reasons[0], "killed it for memory 2 times") {
		t.Errorf("reasons = %q", killed.Reasons)
	}
	applied := SizeMemory(MemoryEvidence{P95: mibs(100), Peak: mibs(150), Limit: 384, LiveLimit: 256, OOMKills: 2})
	if applied.Verdict != VerdictRight || applied.SuggestedMB != 384 {
		t.Errorf("after the suggestion is saved = %+v", applied)
	}
}

func TestCPUIsSizedFromItsPercentileWithAQuarterCoreFloor(t *testing.T) {
	busy := SizeCPU(CPUEvidence{P95: 0.9, Peak: 1, Limit: 1, LiveLimit: 1})
	if busy.Verdict != VerdictTooTight || busy.Suggested != 1.5 {
		t.Errorf("busy at its limit = %+v", busy)
	}
	idle := SizeCPU(CPUEvidence{P95: 0.02, Peak: 0.4, Limit: 4})
	if idle.Verdict != VerdictOversized || idle.Suggested != 0.25 || idle.OffBy != 16 {
		t.Errorf("four cores for an idle app = %+v", idle)
	}
	fits := SizeCPU(CPUEvidence{P95: 0.3, Peak: 0.8, Limit: 0.5})
	if fits.Verdict != VerdictRight || fits.Suggested != 0.45 {
		t.Errorf("a fitting limit = %+v", fits)
	}
	capped := SizeCPU(CPUEvidence{P95: 3.9, HostCPUs: 4})
	if capped.Verdict != VerdictUnlimited || capped.Suggested != 4 {
		t.Errorf("more than the host has = %+v", capped)
	}
}

// Throttling is a CPU limit too low for the bursts a mean does not show: the
// suggestion is half again the limit it happened under, however idle the
// percentile looks; a share under the line, or none known, changes nothing.
func TestThrottlingMakesACPULimitTooTight(t *testing.T) {
	share := 0.12
	throttled := SizeCPU(CPUEvidence{P95: 0.2, Peak: 0.5, Limit: 0.5, LiveLimit: 0.5, ThrottledShare: &share})
	if throttled.Verdict != VerdictTooTight || throttled.Suggested != 0.75 {
		t.Fatalf("throttled = %+v", throttled)
	}
	if !strings.Contains(throttled.Reasons[0], "throttled in 12% of its scheduler periods") {
		t.Errorf("reasons = %q", throttled.Reasons)
	}
	little := 0.01
	for _, s := range []*float64{nil, &little} {
		if got := SizeCPU(CPUEvidence{P95: 0.2, Peak: 0.5, Limit: 0.5, LiveLimit: 0.5, ThrottledShare: s}); got.Verdict != VerdictRight {
			t.Errorf("share %v = %+v", s, got)
		}
	}
}

func TestOverallIsTheVerdictThatAsksTheMost(t *testing.T) {
	right := MemorySizing{Verdict: VerdictRight, OffBy: 1}
	tight := CPUSizing{Verdict: VerdictTooTight, OffBy: 1.5}
	if v, off := Overall(right, tight); v != VerdictTooTight || off != 1.5 {
		t.Errorf("right and tight = %s %v", v, off)
	}
	if v, off := Overall(MemorySizing{Verdict: VerdictOversized, OffBy: 3}, CPUSizing{Verdict: VerdictOversized, OffBy: 5}); v != VerdictOversized || off != 5 {
		t.Errorf("both oversized = %s %v", v, off)
	}
	if v, _ := Overall(MemorySizing{Verdict: VerdictUnlimited}, CPUSizing{Verdict: VerdictRight}); v != VerdictUnlimited {
		t.Errorf("unlimited and right = %s", v)
	}
}

func usageOf(samples int, mem uint64, cpuPercent float64) SizingUsage {
	return SizingUsage{
		Samples: samples, Containers: 1,
		First: time.Unix(1_700_000_000, 0), Last: time.Unix(1_700_000_000+int64(samples)*15, 0),
		MemoryBins: []Bin{{Value: float64(mem), Count: samples}}, MemoryPeak: mem,
		CPUBins: []Bin{{Value: cpuPercent / 100, Count: samples}}, CPUPeak: cpuPercent / 100, CPUSamples: samples,
	}
}

// Under a day of samples there is no suggestion at all, only how much history
// there is; from a day on, only the limits that are off are applied.
func TestSizeNeedsADayAndAppliesOnlyWhatIsOff(t *testing.T) {
	young := Size(SizingInput{Usage: usageOf(2000, mibs(300), 20), Interval: 15 * time.Second, MemoryLimitMB: 512})
	if young.Verdict != VerdictInsufficient || young.Memory != nil || young.Apply != nil ||
		young.Window.CoveredSeconds != 30000 || young.Window.NeededSeconds != 86400 || young.Window.From == nil {
		t.Fatalf("eight hours = %+v", young)
	}
	day := Size(SizingInput{
		Usage: usageOf(5760, mibs(300), 20), Interval: 15 * time.Second,
		MemoryLimitMB: 512, CPULimit: 4,
	})
	if day.Verdict != VerdictOversized || day.Memory.Verdict != VerdictRight || day.CPU.Verdict != VerdictOversized {
		t.Fatalf("a day = %+v %+v %+v", day, day.Memory, day.CPU)
	}
	if day.Apply == nil || day.Apply.MemoryMB != nil || day.Apply.CPUs == nil || *day.Apply.CPUs != 0.3 {
		t.Errorf("apply = %+v", day.Apply)
	}
	fitting := Size(SizingInput{
		Usage: usageOf(5760, mibs(300), 20), Interval: 15 * time.Second, MemoryLimitMB: 512, CPULimit: 0.5,
	})
	if fitting.Verdict != VerdictRight || fitting.Apply != nil {
		t.Errorf("both right = %+v", fitting)
	}
}

func sizingSample(id string, at time.Time, cpu float64, mem uint64, cpuTotal, periods, throttled uint64) dockerx.ContainerStats {
	return dockerx.ContainerStats{
		ID: id, Name: "c-" + id, TS: at, CPUPercent: cpu, MemUsage: mem, CPUTotal: cpuTotal,
		CPUPeriods: periods, CPUThrottledPeriods: throttled,
	}
}

// The reader counts moments rather than rows, leaves the first sample of each
// container out of CPU, ignores containers it was not asked about and answers
// the percentiles from whole-MiB and half-percent buckets.
func TestSizingUsageReadsTheNamedContainers(t *testing.T) {
	r := testRecorder(t, 15*time.Second, 7*24*time.Hour)
	ctx := context.Background()
	base := time.Unix(1_700_000_000, 0)
	for i := range 40 {
		at := base.Add(time.Duration(i) * 15 * time.Second)
		stats := []dockerx.ContainerStats{
			sizingSample("aaa", at, float64(10+i), mibs(float64(100+i)), uint64(i)*1000, 0, 0),
			{ID: "zzz", Name: "other", TS: at, CPUPercent: 400, MemUsage: mibs(9000)},
		}
		if i >= 20 { // a second release beside the first for half the window
			stats = append(stats, dockerx.ContainerStats{ID: "bbb", Name: "jd-e1-r2", TS: at, CPUPercent: 50, MemUsage: mibs(200), CPUTotal: uint64(i) * 1000})
		}
		r.recordContainersAt(ctx, at, stats)
	}
	usage, err := r.SizingUsage(ctx, []string{"aaa", "bbb", "aaa", ""}, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if usage.Samples != 40 || usage.Containers != 2 || !usage.First.Equal(base) {
		t.Fatalf("usage = %+v", usage)
	}
	if usage.MemoryPeak != mibs(200) || usage.CPUPeak != 0.5 {
		t.Errorf("peaks = %d %v", usage.MemoryPeak, usage.CPUPeak)
	}
	// 40 + 20 rows, less each container's first sample for CPU.
	if usage.CPUSamples != 58 {
		t.Errorf("CPU samples = %d", usage.CPUSamples)
	}
	// 60 memory readings: 40 of 100..139 MiB and 20 of 200; the 57th is 200.
	if got := usage.MemoryP95(); got != mibs(201) {
		t.Errorf("memory p95 = %d MiB", got/mib)
	}
	if got := usage.CPUP95(); math.Abs(got-0.505) > 1e-9 {
		t.Errorf("cpu p95 = %v", got)
	}
	none, err := r.SizingUsage(ctx, nil, base, base.Add(time.Hour))
	if err != nil || none.Samples != 0 || len(none.MemoryBins) != 0 {
		t.Errorf("no ids = %+v %v", none, err)
	}
}

// Throttling is differenced sample to sample within a container; a restart's
// reset is left out, and a container without a CPU limit stores no counters.
func TestThrottledShareDifferencesTheCounters(t *testing.T) {
	r := testRecorder(t, 15*time.Second, 7*24*time.Hour)
	ctx := context.Background()
	base := time.Unix(1_700_000_000, 0)
	// periods/throttled, cumulative: +100/+10, +100/+30, reset, +50/+5.
	steps := [][2]uint64{{1000, 100}, {1100, 110}, {1200, 140}, {20, 1}, {70, 6}}
	for i, step := range steps {
		at := base.Add(time.Duration(i) * 15 * time.Second)
		r.recordContainersAt(ctx, at, []dockerx.ContainerStats{
			sizingSample("aaa", at, 10, mibs(10), 1, step[0], step[1]),
			sizingSample("free", at, 10, mibs(10), 1, 0, 0),
		})
	}
	usage, err := r.SizingUsage(ctx, []string{"aaa", "free"}, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if share := usage.ThrottledShare([]string{"aaa"}); share == nil || math.Abs(*share-45.0/250) > 1e-9 {
		t.Fatalf("share = %v", share)
	}
	var periods, throttled *int64
	if err := r.db.QueryRow(`SELECT cpu_periods, cpu_throttled FROM metric_container_samples WHERE container_id = 'free' LIMIT 1`).Scan(&periods, &throttled); err != nil || periods != nil || throttled != nil {
		t.Errorf("an unlimited container stored counters %v %v %v", periods, throttled, err)
	}
	if share := usage.ThrottledShare([]string{"free", "unknown"}); share != nil {
		t.Errorf("no counters = %v", *share)
	}
}

// A read is reused for five minutes per set of containers, and read afresh
// after them.
func TestRecentSizingUsageIsReusedForFiveMinutes(t *testing.T) {
	r := testRecorder(t, 15*time.Second, 7*24*time.Hour)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	r.recordContainersAt(ctx, now.Add(-time.Minute), []dockerx.ContainerStats{sizingSample("aaa", now.Add(-time.Minute), 10, mibs(100), 1, 0, 0)})
	first, err := r.RecentSizingUsage(ctx, []string{"aaa"}, now)
	if err != nil || first.Samples != 1 {
		t.Fatalf("first = %+v %v", first, err)
	}
	r.recordContainersAt(ctx, now, []dockerx.ContainerStats{sizingSample("aaa", now, 10, mibs(100), 2, 0, 0)})
	if again, _ := r.RecentSizingUsage(ctx, []string{"aaa"}, now.Add(time.Minute)); again.Samples != 1 {
		t.Errorf("within five minutes = %d samples", again.Samples)
	}
	if other, _ := r.RecentSizingUsage(ctx, []string{"aaa", "bbb"}, now.Add(time.Minute)); other.Samples != 2 {
		t.Errorf("another set of containers = %d samples", other.Samples)
	}
	if later, _ := r.RecentSizingUsage(ctx, []string{"aaa"}, now.Add(6*time.Minute)); later.Samples != 2 {
		t.Errorf("after five minutes = %d samples", later.Samples)
	}
}
