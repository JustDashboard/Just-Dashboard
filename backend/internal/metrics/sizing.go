package metrics

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// Right-sizing reads what a deployment's containers actually used over the
// retained history and says what its memory and CPU limits should be.
//
// The maths is deliberately small enough to say in two sentences on the page
// that shows it: memory is the 95th percentile of the samples plus a quarter,
// and never under the observed peak plus a tenth, because memory is not
// compressible and a limit under the peak is a kill; CPU is the 95th
// percentile plus half, and never under a quarter of a core, because CPU is
// throttled rather than killed and a start-up throttled into its readiness
// timeout fails the release. Both round up to a step a person would type.
// Everything here is a pure function of numbers except the readers,
// SizingUsage and RecentSizingUsage, so the judgement is tested without a
// database.

// SizingHistory is the least recorded history a suggestion rests on. A day
// holds a daily cycle — the nightly job, the morning peak — and anything
// shorter is a suggestion fitted to whatever hour it happened to see.
const SizingHistory = 24 * time.Hour

const (
	memoryHeadroom     = 1.25
	memoryPeakHeadroom = 1.10
	cpuHeadroom        = 1.5
	// MinSuggestedMemoryMB and MinSuggestedCPUs are the floors: below them
	// the figure is noise in the samples, and a runtime's own start-up needs
	// more than an idle sample shows.
	MinSuggestedMemoryMB = 64
	MinSuggestedCPUs     = 0.25
	// Pressure the live release was under — an OOM kill, throttling — means
	// the samples missed what mattered (a fifteen-second sampler does not
	// see the spike that killed it), so the suggestion is at least half
	// again the limit it happened under.
	pressureRaise = 1.5
	// ThrottledLine is the share of CFS periods throttled at which a CPU
	// limit is too tight.
	ThrottledLine = 0.05
	// A limit is oversized at twice what it needs, and only once the
	// difference is worth a person's time.
	oversizedFactor   = 2
	oversizedMemoryMB = 256
	oversizedCPUs     = 0.5
	// maxSizingIdentities bounds how many container ids one read names.
	maxSizingIdentities = 256
)

// Verdict is the judgement of one limit, or of an environment's two.
type Verdict string

const (
	VerdictTooTight     Verdict = "too_tight"
	VerdictOversized    Verdict = "oversized"
	VerdictUnlimited    Verdict = "unlimited"
	VerdictRight        Verdict = "right"
	VerdictInsufficient Verdict = "insufficient_history"
)

// VerdictRank orders verdicts by how much they ask of the operator: a limit
// that is killing the application first, a server's worth of memory set aside
// for nothing next, a container that can take the whole machine after that.
func VerdictRank(v Verdict) int {
	switch v {
	case VerdictTooTight:
		return 0
	case VerdictOversized:
		return 1
	case VerdictUnlimited:
		return 2
	case VerdictRight:
		return 3
	default:
		return 4
	}
}

// Percentile is the nearest-rank percentile of values: the smallest value at
// or under which p percent of them fall. Nearest-rank rather than an
// interpolation because it is always a value that was observed, and the
// histogram form below can be exact about it.
func Percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	return sorted[percentileRank(len(sorted), p)-1]
}

// Bin is one bucket of a histogram: Count samples at or under Value and above
// the bucket below.
type Bin struct {
	Value float64
	Count int
}

// HistogramPercentile is Percentile over bucketed samples, answering with the
// upper edge of the bucket the rank falls in — never under the true value.
func HistogramPercentile(bins []Bin, p float64) float64 {
	total := 0
	for _, b := range bins {
		total += max(b.Count, 0)
	}
	if total == 0 {
		return 0
	}
	sorted := slices.Clone(bins)
	sortBins(sorted)
	rank, seen := percentileRank(total, p), 0
	for _, b := range sorted {
		seen += max(b.Count, 0)
		if seen >= rank {
			return b.Value
		}
	}
	return sorted[len(sorted)-1].Value
}

func percentileRank(n int, p float64) int {
	rank := int(math.Ceil(p / 100 * float64(n)))
	return min(max(rank, 1), n)
}

// RoundMemoryMB rounds a memory figure up to a step a person would type: 64
// MiB to a gigabyte, 256 MiB to 8 GiB, a gigabyte beyond.
func RoundMemoryMB(mb float64) int64 {
	step := 64.0
	switch {
	case mb > 8192:
		step = 1024
	case mb > 1024:
		step = 256
	}
	rounded := int64(math.Ceil(mb/step-1e-9) * step)
	return min(max(rounded, MinSuggestedMemoryMB), 4<<20)
}

// RoundCPUs rounds a CPU figure up to a twentieth of a core under one core
// and a quarter above.
func RoundCPUs(cores float64) float64 {
	step := 0.05
	if cores > 1 {
		step = 0.25
	}
	rounded := math.Round(math.Ceil(cores/step-1e-9)*step*100) / 100
	return math.Max(rounded, MinSuggestedCPUs)
}

// MemoryEvidence is what memory's judgement rests on.
type MemoryEvidence struct {
	P95, Peak uint64 // bytes
	// Limit is the saved plan's (what applies with the next release) and
	// LiveLimit the live release's, which the OOM kills happened under; both
	// in MiB, zero for none.
	Limit, LiveLimit int64
	OOMKills         int
}

// MemorySizing is memory's judgement.
type MemorySizing struct {
	Verdict     Verdict  `json:"verdict"`
	LimitMB     int64    `json:"limitMb"`
	LiveLimitMB int64    `json:"liveLimitMb"`
	P95MB       float64  `json:"p95Mb"`
	PeakMB      float64  `json:"peakMb"`
	SuggestedMB int64    `json:"suggestedMb"`
	OOMKills    int      `json:"oomKills"`
	OffBy       float64  `json:"offBy"`
	Reasons     []string `json:"reasons"`
}

const mib = 1 << 20

// SizeMemory judges a memory limit. The suggestion is the larger of the 95th
// percentile plus 25% and the peak plus 10%, rounded up; after an OOM kill it
// is at least half again the limit the kill happened under. The limit is too
// tight when it is under the suggestion, oversized at twice the suggestion and
// 256 MiB more.
func SizeMemory(e MemoryEvidence) MemorySizing {
	p95, peak := float64(e.P95)/mib, float64(e.Peak)/mib
	suggested := RoundMemoryMB(math.Max(p95*memoryHeadroom, peak*memoryPeakHeadroom))
	if e.OOMKills > 0 && e.LiveLimit > 0 {
		suggested = max(suggested, RoundMemoryMB(float64(e.LiveLimit)*pressureRaise))
	}
	out := MemorySizing{
		LimitMB: e.Limit, LiveLimitMB: e.LiveLimit, P95MB: round1(p95), PeakMB: round1(peak),
		SuggestedMB: suggested, OOMKills: e.OOMKills, OffBy: 1, Reasons: []string{},
	}
	limit := float64(e.Limit)
	switch {
	case e.Limit <= 0:
		out.Verdict = VerdictUnlimited
		out.Reasons = append(out.Reasons, "No memory limit is set, so a leak can take the whole server's memory.")
	case float64(suggested) > limit:
		out.Verdict, out.OffBy = VerdictTooTight, round2(float64(suggested)/limit)
		if e.OOMKills > 0 {
			out.Reasons = append(out.Reasons, fmt.Sprintf("The kernel killed it for memory %s while the live release ran.", times(e.OOMKills)))
		}
		if peak >= 0.9*limit {
			out.Reasons = append(out.Reasons, fmt.Sprintf("Its peak, %s, reached %s of the limit.", mibText(peak), share(peak/limit)))
		}
		if p95 >= 0.8*limit {
			out.Reasons = append(out.Reasons, fmt.Sprintf("It used %s or more 5%% of the time, %s of the limit.", mibText(p95), share(p95/limit)))
		}
		if len(out.Reasons) == 0 {
			out.Reasons = append(out.Reasons, fmt.Sprintf("Its peak plus a tenth, %s, is over the limit.", mibText(peak*memoryPeakHeadroom)))
		}
	case limit >= oversizedFactor*float64(suggested) && limit-float64(suggested) >= oversizedMemoryMB:
		out.Verdict, out.OffBy = VerdictOversized, round2(limit/float64(suggested))
		out.Reasons = append(out.Reasons, fmt.Sprintf("The limit is %s what it needs: its peak was %s.", factor(out.OffBy), mibText(peak)))
	default:
		out.Verdict = VerdictRight
		out.Reasons = append(out.Reasons, fmt.Sprintf("The limit fits: its peak was %s of it.", share(peak/limit)))
	}
	return out
}

// CPUEvidence is what CPU's judgement rests on, in cores.
type CPUEvidence struct {
	P95, Peak        float64
	Limit, LiveLimit float64
	// ThrottledShare is the share of the live release's scheduler periods
	// in which it was throttled; nil when unknown (no CPU limit, or no
	// sample since the counters were kept).
	ThrottledShare *float64
	// HostCPUs caps the suggestion; Docker caps a limit there anyway.
	HostCPUs int
}

// CPUSizing is CPU's judgement.
type CPUSizing struct {
	Verdict        Verdict  `json:"verdict"`
	Limit          float64  `json:"limit"`
	LiveLimit      float64  `json:"liveLimit"`
	P95            float64  `json:"p95"`
	Peak           float64  `json:"peak"`
	Suggested      float64  `json:"suggested"`
	ThrottledShare *float64 `json:"throttledShare"`
	OffBy          float64  `json:"offBy"`
	Reasons        []string `json:"reasons"`
}

// SizeCPU judges a CPU limit. The suggestion is the 95th percentile plus 50%,
// at least a quarter core, rounded up and capped at the host's cores; after
// throttling in 5% of periods it is at least half again the limit it happened
// under. Too tight under the suggestion, oversized at twice it and half a core
// more.
func SizeCPU(e CPUEvidence) CPUSizing {
	suggested := RoundCPUs(e.P95 * cpuHeadroom)
	throttled := e.ThrottledShare != nil && *e.ThrottledShare >= ThrottledLine
	if throttled && e.LiveLimit > 0 {
		suggested = math.Max(suggested, RoundCPUs(e.LiveLimit*pressureRaise))
	}
	if e.HostCPUs > 0 {
		suggested = math.Min(suggested, float64(e.HostCPUs))
	}
	out := CPUSizing{
		Limit: e.Limit, LiveLimit: e.LiveLimit, P95: round2(e.P95), Peak: round2(e.Peak),
		Suggested: suggested, ThrottledShare: e.ThrottledShare, OffBy: 1, Reasons: []string{},
	}
	switch {
	case e.Limit <= 0:
		out.Verdict = VerdictUnlimited
		out.Reasons = append(out.Reasons, "No CPU limit is set, so a busy loop can take every core.")
	case suggested > e.Limit+1e-9:
		out.Verdict, out.OffBy = VerdictTooTight, round2(suggested/e.Limit)
		if throttled {
			out.Reasons = append(out.Reasons, fmt.Sprintf("It was throttled in %s of its scheduler periods.", share(*e.ThrottledShare)))
		}
		if e.P95 >= 0.8*e.Limit {
			out.Reasons = append(out.Reasons, fmt.Sprintf("It was busy at %s or more 5%% of the time, %s of the limit.", coresText(e.P95), share(e.P95/e.Limit)))
		}
		if len(out.Reasons) == 0 {
			out.Reasons = append(out.Reasons, "The limit is under the quarter core a start-up needs.")
		}
	case e.Limit >= oversizedFactor*suggested && e.Limit-suggested >= oversizedCPUs:
		out.Verdict, out.OffBy = VerdictOversized, round2(e.Limit/suggested)
		out.Reasons = append(out.Reasons, fmt.Sprintf("The limit is %s what it needs: it was busy at %s or less 95%% of the time.", factor(out.OffBy), coresText(e.P95)))
	default:
		out.Verdict = VerdictRight
		out.Reasons = append(out.Reasons, fmt.Sprintf("The limit fits: it was busy at %s of it or less 95%% of the time.", share(e.P95/e.Limit)))
	}
	return out
}

// Overall is an environment's verdict from its two: the one that asks the
// most of the operator, and how far off that one is.
func Overall(memory MemorySizing, cpu CPUSizing) (Verdict, float64) {
	if VerdictRank(cpu.Verdict) < VerdictRank(memory.Verdict) {
		return cpu.Verdict, cpu.OffBy
	}
	if cpu.Verdict == memory.Verdict {
		return memory.Verdict, math.Max(memory.OffBy, cpu.OffBy)
	}
	return memory.Verdict, memory.OffBy
}

func times(n int) string {
	if n == 1 {
		return "once"
	}
	return fmt.Sprintf("%d times", n)
}

func share(f float64) string { return fmt.Sprintf("%.0f%%", f*100) }

func factor(f float64) string {
	return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", f), "0"), ".") + "×"
}

func mibText(mb float64) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.1f GiB", mb/1024)
	}
	return fmt.Sprintf("%.0f MiB", mb)
}

func coresText(cores float64) string {
	if cores == 1 {
		return "1 core"
	}
	return fmt.Sprintf("%.2f cores", cores)
}

// SizingUsage is what the samples of some containers say about their memory
// and CPU over a window.
type SizingUsage struct {
	// Samples counts distinct sample instants, so two containers side by
	// side during a blue/green cutover do not count the same moment twice.
	Samples    int
	Containers int
	First      time.Time
	Last       time.Time
	// Memory in whole-MiB buckets and CPU in half-percent ones (Docker's
	// percent, a core being 100), each bucket at its upper edge.
	MemoryBins []Bin
	CPUBins    []Bin
	MemoryPeak uint64
	CPUPeak    float64 // cores
	CPUSamples int
	// throttle is each container's CFS periods and throttled periods over
	// the window, differenced sample to sample.
	throttle map[string][2]float64
}

// Covered is how much history the samples stand for.
func (u SizingUsage) Covered(interval time.Duration) time.Duration {
	return time.Duration(u.Samples) * interval
}

// MemoryP95 and CPUP95 are the 95th percentiles, in bytes and cores.
func (u SizingUsage) MemoryP95() uint64 { return uint64(HistogramPercentile(u.MemoryBins, 95)) }
func (u SizingUsage) CPUP95() float64   { return HistogramPercentile(u.CPUBins, 95) }

// ThrottledShare is the share of the given containers' CFS scheduler periods
// in which they were throttled. Nil when none of them carried the counters:
// no CPU limit, or nothing recorded since they were kept.
func (u SizingUsage) ThrottledShare(ids []string) *float64 {
	var periods, throttled float64
	for _, id := range ids {
		counted := u.throttle[id]
		periods, throttled = periods+counted[0], throttled+counted[1]
	}
	if periods <= 0 {
		return nil
	}
	share := throttled / periods
	return &share
}

// SizingUsage reads the samples of the given container ids over [from, to]
// in one ordered pass over the (container_id, ts) index — a week of one
// container is forty thousand rows, and folding them here costs a third of
// the window functions SQLite would run for the same answer.
//
// A sample whose CPU figure measured no interval — the first after a start, a
// counter reset, a gap of more than three intervals — stored 0 and is left out
// of CPU, as ContainerRange leaves it out of a chart; its memory is a reading
// like any other. Throttling is differenced within a container, and a
// restart's reset is left out rather than read as a negative.
func (r *Recorder) SizingUsage(ctx context.Context, ids []string, from, to time.Time) (*SizingUsage, error) {
	usage := &SizingUsage{MemoryBins: []Bin{}, CPUBins: []Bin{}, throttle: map[string][2]float64{}}
	where, args := identityFilter(ids, from.Unix(), to.Unix())
	if where == "" || !r.Enabled() {
		return usage, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT container_id, ts, COALESCE(sample_time, ts), cpu_percent, mem_bytes, cpu_total, cpu_periods, cpu_throttled
		  FROM metric_container_samples
		 WHERE `+where+` AND ts >= ?1 AND ts <= ?2
		 ORDER BY container_id, ts`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	gap := (3 * r.interval).Seconds()
	moments := map[int64]struct{}{}
	memory, cpu := map[int64]int{}, map[int64]int{}
	var previous struct {
		id                 string
		at                 float64
		total, periods, th sql.NullInt64
	}
	for rows.Next() {
		var id string
		var ts int64
		var at, percent float64
		var bytes int64
		var total, periods, throttled sql.NullInt64
		if err := rows.Scan(&id, &ts, &at, &percent, &bytes, &total, &periods, &throttled); err != nil {
			return nil, err
		}
		same := id == previous.id
		if !same {
			usage.Containers++
		}
		moments[ts] = struct{}{}
		if usage.First.IsZero() || ts < usage.First.Unix() {
			usage.First = time.Unix(ts, 0).UTC()
		}
		if ts > usage.Last.Unix() {
			usage.Last = time.Unix(ts, 0).UTC()
		}
		bytes = max(bytes, 0)
		memory[bytes/mib]++
		usage.MemoryPeak = max(usage.MemoryPeak, uint64(bytes))
		elapsed := at - previous.at
		if same && elapsed > 0 && elapsed <= gap &&
			(!total.Valid || !previous.total.Valid || total.Int64 >= previous.total.Int64) {
			cpu[int64(percent*2)]++
			usage.CPUSamples++
			usage.CPUPeak = math.Max(usage.CPUPeak, percent/100)
		}
		if same && periods.Valid && throttled.Valid && previous.periods.Valid && previous.th.Valid {
			dp, dt := periods.Int64-previous.periods.Int64, throttled.Int64-previous.th.Int64
			if dp > 0 && dt >= 0 {
				counted := usage.throttle[id]
				usage.throttle[id] = [2]float64{counted[0] + float64(dp), counted[1] + float64(min(dt, dp))}
			}
		}
		previous.id, previous.at, previous.total, previous.periods, previous.th = id, at, total, periods, throttled
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	usage.Samples = len(moments)
	for bucket, count := range memory {
		usage.MemoryBins = append(usage.MemoryBins, Bin{Value: float64(bucket+1) * mib, Count: count})
	}
	for bucket, count := range cpu {
		usage.CPUBins = append(usage.CPUBins, Bin{Value: float64(bucket+1) / 200, Count: count})
	}
	sortBins(usage.MemoryBins)
	sortBins(usage.CPUBins)
	return usage, nil
}

// sizingCacheTTL is how long a right-sizing read is reused.
const sizingCacheTTL = 5 * time.Minute

type sizingCached struct {
	at    time.Time
	usage *SizingUsage
}

// RecentSizingUsage is SizingUsage over the whole retained record up to now,
// reused for five minutes per set of containers.
func (r *Recorder) RecentSizingUsage(ctx context.Context, ids []string, now time.Time) (*SizingUsage, error) {
	key := strings.Join(ids, ",")
	r.sizingMu.Lock()
	cached, ok := r.sizingCache[key]
	r.sizingMu.Unlock()
	if ok && now.Sub(cached.at) >= 0 && now.Sub(cached.at) < sizingCacheTTL {
		return cached.usage, nil
	}
	usage, err := r.SizingUsage(ctx, ids, now.Add(-r.retention), now)
	if err != nil {
		return nil, err
	}
	r.sizingMu.Lock()
	defer r.sizingMu.Unlock()
	if r.sizingCache == nil {
		r.sizingCache = map[string]sizingCached{}
	}
	for k, entry := range r.sizingCache {
		if now.Sub(entry.at) >= sizingCacheTTL {
			delete(r.sizingCache, k)
		}
	}
	r.sizingCache[key] = sizingCached{at: now, usage: usage}
	return usage, nil
}

func sortBins(bins []Bin) {
	slices.SortFunc(bins, func(a, b Bin) int {
		switch {
		case a.Value < b.Value:
			return -1
		case a.Value > b.Value:
			return 1
		}
		return 0
	})
}

// identityFilter is the IN list for a set of container ids and the arguments
// it binds: the window's bounds as ?1 and ?2, whatever the number of ids, and
// the ids from ?3 on.
func identityFilter(ids []string, from, to int64) (string, []any) {
	seen := map[string]bool{}
	marks, args := []string{}, []any{from, to}
	for _, id := range ids {
		if id == "" || len(id) > 128 || seen[id] || len(marks) >= maxSizingIdentities {
			continue
		}
		seen[id] = true
		args = append(args, id)
		marks = append(marks, fmt.Sprintf("?%d", len(args)))
	}
	if len(marks) == 0 {
		return "", nil
	}
	return "container_id IN (" + strings.Join(marks, ",") + ")", args
}

// SizingInput is everything one environment's judgement rests on.
type SizingInput struct {
	Usage    SizingUsage
	Interval time.Duration
	// The saved plan's limits and the live release's (zero for none).
	MemoryLimitMB, LiveMemoryLimitMB int64
	CPULimit, LiveCPULimit           float64
	// Pressure while the live release ran.
	OOMKills       int
	ThrottledShare *float64
	HostCPUs       int
}

// SizingWindow is the history a judgement rests on.
type SizingWindow struct {
	From            *time.Time `json:"from"`
	To              *time.Time `json:"to"`
	Samples         int        `json:"samples"`
	Containers      int        `json:"containers"`
	IntervalSeconds int        `json:"intervalSeconds"`
	CoveredSeconds  int64      `json:"coveredSeconds"`
	NeededSeconds   int64      `json:"neededSeconds"`
}

// SizingApply is what "Apply suggested limits" writes into the plan: only
// the limits that are off, so a limit already judged right is left as the
// operator set it.
type SizingApply struct {
	MemoryMB *int64   `json:"memoryMb,omitempty"`
	CPUs     *float64 `json:"cpus,omitempty"`
}

// SizingResult is one environment's judgement.
type SizingResult struct {
	Verdict Verdict       `json:"verdict"`
	OffBy   float64       `json:"offBy"`
	Window  SizingWindow  `json:"window"`
	Memory  *MemorySizing `json:"memory"`
	CPU     *CPUSizing    `json:"cpu"`
	Apply   *SizingApply  `json:"apply"`
}

// Size judges an environment: not at all with less than SizingHistory of
// samples, and otherwise each limit by SizeMemory and SizeCPU and the two
// together by Overall.
func Size(in SizingInput) SizingResult {
	covered := in.Usage.Covered(in.Interval)
	result := SizingResult{
		Verdict: VerdictInsufficient, OffBy: 1,
		Window: SizingWindow{
			Samples: in.Usage.Samples, Containers: in.Usage.Containers,
			IntervalSeconds: int(in.Interval / time.Second),
			CoveredSeconds:  int64(covered / time.Second), NeededSeconds: int64(SizingHistory / time.Second),
		},
	}
	if !in.Usage.First.IsZero() {
		first, last := in.Usage.First, in.Usage.Last
		result.Window.From, result.Window.To = &first, &last
	}
	if covered < SizingHistory {
		return result
	}
	memory := SizeMemory(MemoryEvidence{
		P95: in.Usage.MemoryP95(), Peak: in.Usage.MemoryPeak,
		Limit: in.MemoryLimitMB, LiveLimit: in.LiveMemoryLimitMB, OOMKills: in.OOMKills,
	})
	cpu := SizeCPU(CPUEvidence{
		P95: in.Usage.CPUP95(), Peak: in.Usage.CPUPeak, Limit: in.CPULimit, LiveLimit: in.LiveCPULimit,
		ThrottledShare: in.ThrottledShare, HostCPUs: in.HostCPUs,
	})
	result.Memory, result.CPU = &memory, &cpu
	result.Verdict, result.OffBy = Overall(memory, cpu)
	apply := SizingApply{}
	if memory.Verdict != VerdictRight {
		apply.MemoryMB = &memory.SuggestedMB
	}
	if cpu.Verdict != VerdictRight {
		apply.CPUs = &cpu.Suggested
	}
	if apply.MemoryMB != nil || apply.CPUs != nil {
		result.Apply = &apply
	}
	return result
}
