package dockerx

import (
	"sort"
	"time"
)

// Rate of change, which is usually the more useful half of a measurement.
//
//	Writable layer: 38.7 GB
//
// is a number an operator can do nothing with. Either it has been 38.7 GB for
// six months, in which case it is the size of the thing and not a problem, or
// it was 26 GB yesterday, in which case the disk has about two days left. The
// static figure cannot tell those apart and the dashboard already records the
// history that can.
//
// The thresholds below are claims about what is worth interrupting somebody
// for, and are deliberately high. An alert that fires on ordinary behaviour
// trains people to close the panel, which costs more than the alert was ever
// worth — so nothing here fires on a spike, only on a level held long enough
// that it is a state rather than a moment.

const (
	// Sustained CPU worth mentioning, and for how long. One core pinned for
	// fifteen minutes is a process in a loop or a job that is genuinely
	// working; either way it is a fact about the server, not a blip.
	anomalyCPUPercent = 90
	anomalyCPUWindow  = 15 * time.Minute

	// How close to its memory limit a container has to get before the limit is
	// the story. Above this the next allocation spike is an OOM kill.
	anomalyMemoryPercent = 85

	// Memory growth over the window that reads as a leak rather than a load
	// change: usage climbing steadily and never coming back down.
	anomalyMemoryGrowth = 1.5

	// Writable-layer growth worth reporting, in bytes over 24 hours.
	anomalyWritableGrowth = 1 << 30
)

// MetricPoint is one bucket of one container's recorded history, in the shape
// this file needs. The recorder's own type is richer and lives in a package
// that imports this one, so the caller narrows it rather than the dependency
// being turned around.
type MetricPoint struct {
	TS       time.Time
	CPU      float64
	CPUPeak  float64
	MemBytes uint64
	MemLimit uint64
	MemPeak  float64
	// SizeRw is the writable layer at this point. Zero where it was not
	// recorded — the disk walk had not completed when the sample was taken —
	// which is an absence rather than an empty layer, and the rule below
	// treats it as one.
	SizeRw uint64
}

// Anomaly is a change in behaviour worth telling somebody about.
type Anomaly struct {
	ID       string   `json:"id"`
	Severity Severity `json:"severity"`
	Class    Class    `json:"class"`
	Title    string   `json:"title"`
	Detail   string   `json:"detail"`
	Advice   string   `json:"advice,omitempty"`
	// Metric names what moved, so a chart can be opened at the right series.
	Metric string `json:"metric"`
	// Window is how much history the claim is based on. A conclusion drawn
	// from twenty minutes of samples should not be presented like one drawn
	// from a week.
	Window string `json:"window"`
	// Inferred is always true here. Every one of these is a pattern read out
	// of a series rather than a state Docker reported, and the UI says so.
	Inferred bool `json:"inferred"`
}

// DetectAnomalies reads one container's history for changes worth reporting.
//
// Returns nothing at all for a container behaving normally, which is the
// common case and the reason this is safe to call for every row.
func DetectAnomalies(name, id string, points []MetricPoint) []Anomaly {
	out := []Anomaly{}
	if len(points) < 3 {
		// Too little history to say anything about a trend. Saying nothing is
		// the correct output, not a smaller claim.
		return out
	}
	sort.Slice(points, func(i, j int) bool { return points[i].TS.Before(points[j].TS) })
	span := points[len(points)-1].TS.Sub(points[0].TS)
	window := humanDuration(span)

	if a, ok := sustainedCPU(name, id, points, window); ok {
		out = append(out, a)
	}
	if a, ok := memoryNearLimit(name, id, points, window); ok {
		out = append(out, a)
	}
	if a, ok := memoryClimbing(name, id, points, window, span); ok {
		out = append(out, a)
	}
	if a, ok := writableGrowing(name, id, points, span); ok {
		out = append(out, a)
	}
	return out
}

// writableGrowing is the rate-of-change claim the static figure cannot make.
//
// A container holding 38.7 GB may have held it for six months, in which case
// it is the size of the thing; or it may have held 26 GB yesterday, in which
// case the disk has about two days left. Both look identical in a table, and
// only the second is worth waking somebody for.
func writableGrowing(name, id string, points []MetricPoint, span time.Duration) (Anomaly, bool) {
	if span < time.Hour {
		return Anomaly{}, false
	}
	var first, last uint64
	var firstAt, lastAt time.Time
	for _, p := range points {
		if p.SizeRw == 0 {
			continue
		}
		if first == 0 {
			first, firstAt = p.SizeRw, p.TS
		}
		last, lastAt = p.SizeRw, p.TS
	}
	if first == 0 || last <= first || lastAt.Sub(firstAt) < time.Hour {
		return Anomaly{}, false
	}
	delta := int64(last - first)
	measured := lastAt.Sub(firstAt)
	// Scaled to a day so two containers measured over different windows can be
	// compared, and so the threshold means one thing.
	perDay := float64(delta) / (float64(measured) / float64(24*time.Hour))
	if perDay < anomalyWritableGrowth {
		return Anomaly{}, false
	}
	return Anomaly{
		ID:       "anomaly.writable." + id,
		Severity: SeverityWarning,
		Class:    ClassStorage,
		Title:    name + " is writing into itself at " + humanBytes(int64(perDay)) + " a day",
		Detail: "Its writable layer went from " + humanBytes(int64(first)) + " to " +
			humanBytes(int64(last)) + " over " + humanDuration(measured) +
			". That data is not in a volume: it is not backed up, and it is destroyed the next time the container is recreated — which includes every image update.",
		Advice:   "Find out which directory is growing, then decide whether it is data that should be on a volume or output that should be rotated away. Both answers are better than the disk filling.",
		Metric:   "writableLayer",
		Window:   humanDuration(measured),
		Inferred: true,
	}, true
}

// sustainedCPU fires on a level held, not on a spike.
func sustainedCPU(name, id string, points []MetricPoint, window string) (Anomaly, bool) {
	var start, best = time.Time{}, time.Duration(0)
	for _, p := range points {
		if p.CPU < anomalyCPUPercent {
			start = time.Time{}
			continue
		}
		if start.IsZero() {
			start = p.TS
		}
		if held := p.TS.Sub(start); held > best {
			best = held
		}
	}
	if best < anomalyCPUWindow {
		return Anomaly{}, false
	}
	return Anomaly{
		ID:       "anomaly.cpu." + id,
		Severity: SeverityWarning,
		Class:    ClassRuntime,
		Title:    name + " has been at full CPU for " + humanDuration(best),
		Detail: "It has held at or above " + itoa(anomalyCPUPercent) +
			"% of a core continuously. One core fully used for this long is a loop or a long job, not a busy moment.",
		Advice:   "If it is a job, it will finish. If nothing was asked of it, something inside is spinning — the process list inside the container says which.",
		Metric:   "cpu",
		Window:   window,
		Inferred: true,
	}, true
}

// memoryNearLimit is the finding that predicts an OOM kill before it happens.
func memoryNearLimit(name, id string, points []MetricPoint, window string) (Anomaly, bool) {
	last := points[len(points)-1]
	if last.MemLimit == 0 || last.MemBytes == 0 {
		return Anomaly{}, false
	}
	pct := float64(last.MemBytes) / float64(last.MemLimit) * 100
	if pct < anomalyMemoryPercent {
		return Anomaly{}, false
	}
	return Anomaly{
		ID:       "anomaly.memory.limit." + id,
		Severity: SeverityWarning,
		Class:    ClassRuntime,
		Title:    name + " is close to its memory limit",
		Detail: "It is using " + humanBytes(int64(last.MemBytes)) + " of its " +
			humanBytes(int64(last.MemLimit)) + " limit. The next allocation it cannot satisfy is an OOM kill, and Docker will restart it as if nothing happened.",
		Advice:   "Either the limit is too low for what this does, or something inside is holding memory it should have released. The shape of the last day tells you which: a flat line near the limit is the first, a climb is the second.",
		Metric:   "memory",
		Window:   window,
		Inferred: true,
	}, true
}

// memoryClimbing looks for the shape of a leak: the floor rising, rather than
// the peaks getting higher.
//
// The *minimum* over each half of the window is compared, not the average. A
// container under increasing load has higher peaks and the same troughs; one
// that is leaking never returns to where it started, and the trough is what
// shows that.
func memoryClimbing(name, id string, points []MetricPoint, window string, span time.Duration) (Anomaly, bool) {
	if span < time.Hour || len(points) < 8 {
		return Anomaly{}, false
	}
	mid := len(points) / 2
	early, late := minMemory(points[:mid]), minMemory(points[mid:])
	if early == 0 || late == 0 {
		return Anomaly{}, false
	}
	// Ignore anything too small to be interesting however it grew: a container
	// going from 4 MB to 8 MB has doubled and is not a problem.
	if late < 128<<20 {
		return Anomaly{}, false
	}
	if float64(late)/float64(early) < anomalyMemoryGrowth {
		return Anomaly{}, false
	}
	return Anomaly{
		ID:       "anomaly.memory.growth." + id,
		Severity: SeverityWarning,
		Class:    ClassRuntime,
		Title:    name + " is using steadily more memory",
		Detail: "Its baseline — the lowest it drops to between requests — has gone from " +
			humanBytes(int64(early)) + " to " + humanBytes(int64(late)) + " over " + window +
			". Rising peaks are ordinary load; a rising floor is memory that is not being given back.",
		Advice:   "Left alone this ends in an OOM kill or in the server running out. A restart clears it and tells you nothing; the interesting question is what it is holding.",
		Metric:   "memory",
		Window:   window,
		Inferred: true,
	}, true
}

func minMemory(points []MetricPoint) uint64 {
	var out uint64
	for _, p := range points {
		if p.MemBytes == 0 {
			continue
		}
		if out == 0 || p.MemBytes < out {
			out = p.MemBytes
		}
	}
	return out
}

// WritableGrowth describes how fast a container's writable layer is filling.
//
// Kept separate from the metric anomalies because it is measured differently:
// there is no recorded series of writable-layer sizes, so this compares two
// readings the caller supplies and says over what interval.
type WritableGrowth struct {
	From     int64         `json:"from"`
	To       int64         `json:"to"`
	Delta    int64         `json:"delta"`
	Interval time.Duration `json:"-"`
	Summary  string        `json:"summary"`
	// DaysLeft is how long until the current free space runs out at this rate,
	// zero when it cannot be worked out. Always presented as a projection.
	DaysLeft float64 `json:"daysLeft,omitempty"`
}

// DescribeWritableGrowth turns two readings into a sentence.
func DescribeWritableGrowth(from, to int64, interval time.Duration, freeBytes int64) *WritableGrowth {
	if interval <= 0 || to <= from {
		return nil
	}
	delta := to - from
	perDay := float64(delta) / (float64(interval) / float64(24*time.Hour))
	g := &WritableGrowth{From: from, To: to, Delta: delta, Interval: interval}
	g.Summary = humanBytes(to) + ", up " + humanBytes(delta) + " in " + humanDuration(interval)
	if freeBytes > 0 && perDay > 0 {
		g.DaysLeft = float64(freeBytes) / perDay
	}
	return g
}
