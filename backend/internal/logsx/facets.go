package logsx

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Facets are how a search answers "who" and "what" as well as "which lines":
// the addresses behind the failed logins, the statements behind the slow
// ones, over every match in the window. They are counted in the same pass
// that collects the lines, from the same lens reading, so the bars in
// Insights and the lines under them cannot disagree.

// Facet is the top values of one key across the matches.
type Facet struct {
	Values []FacetValue `json:"values"`
	// Distinct counts the values seen. Past the budget it stops growing and
	// DistinctCapped says so; the top values are then approximate.
	Distinct       int  `json:"distinct"`
	DistinctCapped bool `json:"distinctCapped,omitempty"`
	// Other counts the matches whose value is not among Values; Missing the
	// matches that had no value for the key at all.
	Other   int `json:"other"`
	Missing int `json:"missing"`
}

// FacetValue is one value's tally. Sum and Max are pointers because zero is a
// real answer — a statement that took 0 ms — and must not vanish from the
// wire the way an omitted float would.
type FacetValue struct {
	Value   string            `json:"value"`
	Count   int               `json:"count"`
	Errors  int               `json:"errors"`
	Sum     *float64          `json:"sum,omitempty"`
	Max     *float64          `json:"max,omitempty"`
	First   *time.Time        `json:"first,omitempty"`
	Last    *time.Time        `json:"last,omitempty"`
	Samples map[string]string `json:"samples,omitempty"`
}

// Measure summarises a numeric key over the matches that carry it.
type Measure struct {
	Key   string  `json:"key"`
	Count int     `json:"count"`
	P50   float64 `json:"p50"`
	P75   float64 `json:"p75"`
	P90   float64 `json:"p90"`
	P95   float64 `json:"p95"`
	P99   float64 `json:"p99"`
	Max   float64 `json:"max"`
	Mean  float64 `json:"mean"`
}

const (
	maxFacets          = 12
	defaultFacetLimit  = 12
	maxFacetLimit      = 50
	maxSamples         = 3
	maxSampleValue     = 512
	maxHistogramValues = 12

	// A facet over client addresses on a public SSH log meets tens of
	// thousands of values in a day. Both bounds apply: the count keeps the map
	// small, the bytes keep a facet over statements from holding a hundred
	// megabytes of SQL.
	facetDistinctBudget = 20_000
	facetBytesBudget    = 4 << 20

	// measureReservoir is how many values the percentiles are computed from.
	// The mean and the max are exact; the percentiles of a sample this size
	// are within a fraction of a percent, and the seed is fixed so asking
	// twice gives the same answer.
	measureReservoir = 100_000

	// histogramSeries bounds the values a split histogram tracks while it
	// scans, and histogramTop how many of them get their own series at the
	// end. Everything else is "*".
	histogramSeries = 64
	histogramTop    = 8
	histogramOther  = "*"
)

// Validate checks everything a search can be refused for, so a caller can
// answer 400 before choosing where the lines come from: a bad expression or
// facet that only surfaced after reading a container's log would arrive as a
// daemon error rather than as the operator's mistake.
func (o SearchOptions) Validate() error {
	if _, err := NewFilter(o.Filter); err != nil {
		return err
	}
	if len(o.Facets) > maxFacets {
		return fmt.Errorf("at most %d facets, got %d", maxFacets, len(o.Facets))
	}
	for _, key := range o.Facets {
		if !ValidKey(key) {
			return fmt.Errorf("facet %q is not a field name", key)
		}
	}
	if o.FacetLimit < 0 || o.FacetLimit > maxFacetLimit {
		return fmt.Errorf("facetLimit must be between 1 and %d", maxFacetLimit)
	}
	if o.Measure != "" && !ValidKey(o.Measure) {
		return fmt.Errorf("measure %q is not a field name", o.Measure)
	}
	if len(o.Sample) > 0 && len(o.Facets) == 0 {
		return fmt.Errorf("sample describes the first facet, and no facet was asked for")
	}
	if len(o.Sample) > maxSamples {
		return fmt.Errorf("at most %d sample keys, got %d", maxSamples, len(o.Sample))
	}
	for _, key := range o.Sample {
		if !ValidKey(key) {
			return fmt.Errorf("sample %q is not a field name", key)
		}
	}
	if o.HistogramBy != "" && !ValidKey(o.HistogramBy) {
		return fmt.Errorf("histogramBy %q is not a field name", o.HistogramBy)
	}
	if len(o.HistogramValues) > 0 && o.HistogramBy == "" {
		return fmt.Errorf("histogramValues needs histogramBy")
	}
	if len(o.HistogramValues) > maxHistogramValues {
		return fmt.Errorf("at most %d histogram values, got %d", maxHistogramValues, len(o.HistogramValues))
	}
	for _, v := range o.HistogramValues {
		if len(v) > maxPredicateValue {
			return fmt.Errorf("a histogram value is longer than %d bytes", maxPredicateValue)
		}
	}
	return nil
}

// insight is the per-search tally behind Facets, Measure and a split
// histogram. It sees only the lines that matched in their own right; a
// continuation line is part of its head's record, not a second match.
type insight struct {
	facets  []*facetTally
	limit   int
	sample  []string
	measure *measureTally

	// by is the histogram's split key, "" for level. series interns its
	// values so a stamp holds a shared string, and pinned is the exact set
	// HistogramValues asked for.
	by     string
	series map[string]string
	pinned map[string]bool
}

func newInsight(o SearchOptions) *insight {
	in := &insight{limit: o.FacetLimit, sample: o.Sample}
	if in.limit == 0 {
		in.limit = defaultFacetLimit
	}
	seen := map[string]bool{}
	for _, key := range o.Facets {
		if seen[key] {
			continue
		}
		seen[key] = true
		in.facets = append(in.facets, &facetTally{key: key, values: map[string]*facetCount{}})
	}
	if o.Measure != "" {
		in.measure = &measureTally{key: o.Measure, rng: rand.New(rand.NewPCG(0x6a64, 0x6c6f6773))}
	}
	if o.HistogramBy != "" && o.HistogramBy != "level" {
		in.by = o.HistogramBy
		in.series = map[string]string{}
	}
	if len(o.HistogramValues) > 0 {
		in.pinned = map[string]bool{}
		for _, v := range o.HistogramValues {
			in.pinned[v] = true
		}
	}
	return in
}

// lineValues reads a line's keys for one tally, computing the pattern at most
// once and only if something asked for it.
type lineValues struct {
	l       *Line
	pattern string
	done    bool
}

func (v *lineValues) get(key string) (string, bool) {
	if key == "pattern" {
		if !v.done {
			v.pattern, v.done = Pattern(v.l), true
		}
		return v.pattern, v.pattern != ""
	}
	return v.l.Value(key)
}

// seriesOf is the histogram series a match is counted in: its level by
// default, or the interned value of the split key. "" counts only in the
// column's total — a line with no status is traffic, not a status.
func (in *insight) seriesOf(values *lineValues) string {
	if in.by == "" {
		level := values.l.Level
		if level == "" {
			level = LevelUnknown
		}
		if in.pinned != nil && !in.pinned[level] {
			return histogramOther
		}
		return level
	}
	value, ok := values.get(in.by)
	if !ok {
		return ""
	}
	if in.pinned != nil {
		if in.pinned[value] {
			return in.intern(value)
		}
		return histogramOther
	}
	return in.intern(value)
}

func (in *insight) intern(value string) string {
	if s, ok := in.series[value]; ok {
		return s
	}
	if len(in.series) >= histogramSeries {
		return histogramOther
	}
	s := strings.Clone(value)
	in.series[s] = s
	return s
}

func (in *insight) add(values *lineValues) {
	l := values.l
	measured, number := false, 0.0
	if in.measure != nil {
		if v, ok := values.get(in.measure.key); ok {
			if n, err := strconv.ParseFloat(v, 64); err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) {
				measured, number = true, n
				in.measure.add(n)
			}
		}
	}
	failed := l.Level == "error" || l.Level == "critical"
	var at int64
	if l.Timestamp != nil {
		at = l.Timestamp.UnixNano()
	}
	for i, f := range in.facets {
		value, ok := values.get(f.key)
		if !ok {
			f.missing++
			continue
		}
		c := f.count(value)
		if c == nil {
			f.other++
			continue
		}
		c.count++
		if failed {
			c.errors++
		}
		if measured {
			if !c.measured || number > c.max {
				c.max = number
			}
			c.sum += number
			c.measured = true
		}
		if at != 0 {
			if c.first == 0 || at < c.first {
				c.first = at
			}
			if at > c.last {
				c.last = at
			}
		}
		if i == 0 {
			for k, key := range in.sample {
				if s, ok := values.get(key); ok {
					c.keepSample(k, s)
				}
			}
		}
	}
}

type facetTally struct {
	key     string
	values  map[string]*facetCount
	bytes   int
	capped  bool
	other   int
	missing int
}

type facetCount struct {
	count, errors int
	sum, max      float64
	measured      bool
	first, last   int64
	samples       [maxSamples]string
}

// count finds a value's tally, making one while the budget allows. Past it a
// new value is folded into Other: the values already counted keep counting,
// so the head of the list stays right on the long tail that caused the cap.
func (f *facetTally) count(value string) *facetCount {
	if c, ok := f.values[value]; ok {
		return c
	}
	if len(f.values) >= facetDistinctBudget || f.bytes+len(value) > facetBytesBudget {
		f.capped = true
		return nil
	}
	// A value can be a slice of a megabyte-long line; the tally keeps a few
	// bytes of it rather than the whole line alive.
	value = strings.Clone(value)
	c := &facetCount{}
	f.values[value] = c
	f.bytes += len(value)
	return c
}

func (c *facetCount) keepSample(i int, value string) {
	if len(value) > maxSampleValue {
		cut := maxSampleValue
		for cut > 0 && !utf8.RuneStart(value[cut]) {
			cut--
		}
		value = value[:cut]
	}
	if c.samples[i] != value {
		c.samples[i] = strings.Clone(value)
	}
}

func (f *facetTally) result(limit int, sample []string) *Facet {
	type entry struct {
		value string
		c     *facetCount
	}
	entries := make([]entry, 0, len(f.values))
	for v, c := range f.values {
		entries = append(entries, entry{v, c})
	}
	slices.SortFunc(entries, func(a, b entry) int {
		if a.c.count != b.c.count {
			return b.c.count - a.c.count
		}
		return strings.Compare(a.value, b.value)
	})
	out := &Facet{
		Values:         []FacetValue{},
		Distinct:       len(f.values),
		DistinctCapped: f.capped,
		Other:          f.other,
		Missing:        f.missing,
	}
	for i, e := range entries {
		if i >= limit {
			out.Other += e.c.count
			continue
		}
		v := FacetValue{Value: e.value, Count: e.c.count, Errors: e.c.errors}
		if e.c.measured {
			sum, max := e.c.sum, e.c.max
			v.Sum, v.Max = &sum, &max
		}
		if e.c.first != 0 {
			first, last := time.Unix(0, e.c.first).UTC(), time.Unix(0, e.c.last).UTC()
			v.First, v.Last = &first, &last
		}
		for k, key := range sample {
			if e.c.samples[k] == "" {
				continue
			}
			if v.Samples == nil {
				v.Samples = map[string]string{}
			}
			v.Samples[key] = e.c.samples[k]
		}
		out.Values = append(out.Values, v)
	}
	return out
}

type measureTally struct {
	key       string
	count     int
	sum, max  float64
	reservoir []float64
	rng       *rand.Rand
}

// add keeps a uniform sample of every value seen (Vitter's algorithm R), so
// the percentiles of a day of requests cost a hundred thousand floats rather
// than one per request.
func (m *measureTally) add(v float64) {
	m.count++
	m.sum += v
	if m.count == 1 || v > m.max {
		m.max = v
	}
	if len(m.reservoir) < measureReservoir {
		m.reservoir = append(m.reservoir, v)
		return
	}
	if j := m.rng.IntN(m.count); j < measureReservoir {
		m.reservoir[j] = v
	}
}

func (m *measureTally) result() *Measure {
	out := &Measure{Key: m.key, Count: m.count}
	if m.count == 0 {
		return out
	}
	sorted := slices.Clone(m.reservoir)
	slices.Sort(sorted)
	rank := func(p float64) float64 {
		i := int(math.Ceil(p*float64(len(sorted)))) - 1
		return sorted[max(0, min(i, len(sorted)-1))]
	}
	out.P50, out.P75, out.P90, out.P95, out.P99 = rank(0.50), rank(0.75), rank(0.90), rank(0.95), rank(0.99)
	out.Max, out.Mean = m.max, m.sum/float64(m.count)
	return out
}

// histogramFold decides the series a split histogram finally draws: the
// pinned values when some were asked for, otherwise the eight most frequent,
// with everything else counted as "*".
func (in *insight) histogramFold(stamps []stamp) map[string]string {
	if in.by == "" || in.pinned != nil {
		return nil
	}
	totals := map[string]int{}
	for _, s := range stamps {
		if s.series != "" && s.series != histogramOther {
			totals[s.series]++
		}
	}
	if len(totals) <= histogramTop {
		return nil
	}
	ranked := make([]string, 0, len(totals))
	for v := range totals {
		ranked = append(ranked, v)
	}
	slices.SortFunc(ranked, func(a, b string) int {
		if totals[a] != totals[b] {
			return totals[b] - totals[a]
		}
		return strings.Compare(a, b)
	})
	fold := map[string]string{}
	for i, v := range ranked {
		if i < histogramTop {
			fold[v] = v
		} else {
			fold[v] = histogramOther
		}
	}
	return fold
}
