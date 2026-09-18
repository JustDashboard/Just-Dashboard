package dockerx

import (
	"strings"
	"testing"
	"time"
)

func series(n int, step time.Duration, fill func(i int, p *MetricPoint)) []MetricPoint {
	base := time.Now().Add(-step * time.Duration(n))
	out := make([]MetricPoint, 0, n)
	for i := range n {
		p := MetricPoint{TS: base.Add(step * time.Duration(i))}
		fill(i, &p)
		out = append(out, p)
	}
	return out
}

func find(list []Anomaly, prefix string) *Anomaly {
	for i := range list {
		if strings.HasPrefix(list[i].ID, prefix) {
			return &list[i]
		}
	}
	return nil
}

// The whole point of these rules is that they do not fire on ordinary
// behaviour. A panel that cries wolf gets closed.
func TestQuietContainerProducesNothing(t *testing.T) {
	points := series(60, time.Minute, func(i int, p *MetricPoint) {
		p.CPU = 4
		p.MemBytes = 200 << 20
		p.MemLimit = 1 << 30
	})
	if got := DetectAnomalies("web", "abc", points); len(got) != 0 {
		t.Fatalf("a container doing nothing unusual produced %d anomalies: %+v", len(got), got)
	}
}

func TestTooLittleHistorySaysNothing(t *testing.T) {
	if got := DetectAnomalies("web", "abc", series(2, time.Minute, func(i int, p *MetricPoint) {
		p.CPU = 100
	})); len(got) != 0 {
		t.Errorf("two samples cannot establish a trend, got %+v", got)
	}
}

func TestSustainedCPUFiresButASpikeDoesNot(t *testing.T) {
	spike := series(40, time.Minute, func(i int, p *MetricPoint) {
		p.CPU = 5
		if i == 20 {
			p.CPU = 100
		}
	})
	if find(DetectAnomalies("web", "abc", spike), "anomaly.cpu") != nil {
		t.Error("one busy minute is not an anomaly")
	}

	held := series(40, time.Minute, func(i int, p *MetricPoint) {
		p.CPU = 5
		if i >= 15 {
			p.CPU = 97
		}
	})
	got := find(DetectAnomalies("web", "abc", held), "anomaly.cpu")
	if got == nil {
		t.Fatal("a core pinned for 24 minutes is worth saying")
	}
	if !got.Inferred {
		t.Error("a pattern read out of a series is inferred")
	}
	if got.Severity != SeverityWarning {
		t.Errorf("severity = %q", got.Severity)
	}
}

func TestMemoryNearLimitPredictsTheKill(t *testing.T) {
	points := series(20, time.Minute, func(i int, p *MetricPoint) {
		p.MemLimit = 512 << 20
		p.MemBytes = 460 << 20
	})
	got := find(DetectAnomalies("db", "abc", points), "anomaly.memory.limit")
	if got == nil {
		t.Fatal("90% of a memory limit is worth a warning")
	}
	if !strings.Contains(got.Detail, "OOM") {
		t.Errorf("say what happens next: %q", got.Detail)
	}
}

func TestNoMemoryLimitMeansNoLimitWarning(t *testing.T) {
	points := series(20, time.Minute, func(i int, p *MetricPoint) {
		p.MemBytes = 40 << 30
	})
	if find(DetectAnomalies("db", "abc", points), "anomaly.memory.limit") != nil {
		t.Error("a container with no limit cannot be near one")
	}
}

// A leak shows in the floor, not the peaks. Load that goes up and comes back
// down must not read as a leak.
func TestMemoryGrowthLooksAtTheFloorNotThePeaks(t *testing.T) {
	bursty := series(40, 5*time.Minute, func(i int, p *MetricPoint) {
		p.MemBytes = 300 << 20
		if i%3 == 0 {
			p.MemBytes = 900 << 20
		}
	})
	if find(DetectAnomalies("web", "abc", bursty), "anomaly.memory.growth") != nil {
		t.Error("peaks that come back down are load, not a leak")
	}

	leaking := series(40, 5*time.Minute, func(i int, p *MetricPoint) {
		p.MemBytes = uint64(200<<20) + uint64(i)*(20<<20)
	})
	got := find(DetectAnomalies("web", "abc", leaking), "anomaly.memory.growth")
	if got == nil {
		t.Fatal("a floor that only rises is worth saying")
	}
	if !strings.Contains(got.Detail, "rising floor") {
		t.Errorf("explain the distinction being drawn: %q", got.Detail)
	}
}

func TestSmallGrowthIsIgnored(t *testing.T) {
	points := series(40, 5*time.Minute, func(i int, p *MetricPoint) {
		p.MemBytes = uint64(4<<20) + uint64(i)*(1<<20)
	})
	if find(DetectAnomalies("tiny", "abc", points), "anomaly.memory.growth") != nil {
		t.Error("4 MB to 44 MB has grown tenfold and matters to nobody")
	}
}

func TestDescribeWritableGrowth(t *testing.T) {
	got := DescribeWritableGrowth(26<<30, 32<<30, 24*time.Hour, 100<<30)
	if got == nil {
		t.Fatal("growth over a day is describable")
	}
	if !strings.Contains(got.Summary, "up 6.0 GB") {
		t.Errorf("summary = %q", got.Summary)
	}
	// 6 GB a day into 100 GB of free space.
	if got.DaysLeft < 16 || got.DaysLeft > 17 {
		t.Errorf("daysLeft = %v", got.DaysLeft)
	}
	if DescribeWritableGrowth(30<<30, 30<<30, 24*time.Hour, 0) != nil {
		t.Error("no growth is not a growth report")
	}
	if DescribeWritableGrowth(10, 20, 0, 0) != nil {
		t.Error("no interval means no rate")
	}
}

func TestWritableGrowthFiresOnRateNotSize(t *testing.T) {
	// A large layer that has not moved is the size of the thing, not a
	// problem. This is the case the static figure could not tell apart.
	steady := series(48, time.Hour, func(i int, p *MetricPoint) {
		p.SizeRw = 38 << 30
	})
	if find(DetectAnomalies("app", "abc", steady), "anomaly.writable") != nil {
		t.Error("a layer that has not grown is not an anomaly however large it is")
	}

	growing := series(48, time.Hour, func(i int, p *MetricPoint) {
		p.SizeRw = uint64(26<<30) + uint64(i)*(300<<20)
	})
	got := find(DetectAnomalies("app", "abc", growing), "anomaly.writable")
	if got == nil {
		t.Fatal("a layer gaining gigabytes a day is worth saying")
	}
	if !strings.Contains(got.Detail, "destroyed the next time") {
		t.Errorf("say why it matters, not just that it grew: %q", got.Detail)
	}
	if !got.Inferred {
		t.Error("a rate read out of a series is inferred")
	}
}

func TestWritableGrowthIgnoresUnrecordedSamples(t *testing.T) {
	// Zero means the disk walk had not completed when the sample was taken.
	// Treating it as an empty layer would report every container as having
	// grown from nothing.
	points := series(48, time.Hour, func(i int, p *MetricPoint) {
		if i > 40 {
			p.SizeRw = 30 << 30
		}
	})
	if find(DetectAnomalies("app", "abc", points), "anomaly.writable") != nil {
		t.Error("an absent measurement is not growth from zero")
	}
}
