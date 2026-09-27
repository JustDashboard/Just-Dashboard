package metrics

import (
	"math"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

func TestContainerUsageRatesCrossBucketBoundaries(t *testing.T) {
	r := testRecorder(t, 5*time.Second, time.Hour)
	base := time.Unix(1_800_000_000, 0).UTC()
	for i := range 4 {
		st := dockerx.ContainerStats{ID: "id", Name: "web", TS: base.Add(time.Duration(i) * 5 * time.Second),
			NetworkAvailable: true, BlockAvailable: true, NetRx: uint64(i * 500), NetTx: uint64(i * 250),
			BlockRead: uint64(i * 1000), BlockWrite: uint64(i * 2000)}
		if err := r.writeContainers(t.Context(), base.Add(time.Hour), []dockerx.ContainerStats{st}); err != nil {
			t.Fatal(err)
		}
	}
	// The predecessor lies outside the requested range; each bucket has just
	// one sample. Neither condition should discard a measurable interval.
	series, err := r.ContainerRange(t.Context(), "web", base.Add(5*time.Second), base.Add(15*time.Second), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(series.Points) != 3 {
		t.Fatalf("points = %d", len(series.Points))
	}
	for _, p := range series.Points {
		for _, pair := range []struct {
			got  *float64
			want float64
		}{{p.NetRx, 100}, {p.NetTx, 50}, {p.BlockRead, 200}, {p.BlockWrite, 400}, {p.NetRxPeak, 100}} {
			if pair.got == nil || *pair.got != pair.want {
				t.Fatalf("rate = %v, want %v: %+v", pair.got, pair.want, p)
			}
		}
	}
}

func TestContainerUsageKeepsFractionalSampleTime(t *testing.T) {
	r := testRecorder(t, 5*time.Second, time.Hour)
	base := time.Unix(1_800_000_000, 900_000_000).UTC()
	for i := range 2 {
		st := dockerx.ContainerStats{ID: "id", Name: "web", TS: base.Add(time.Duration(i) * 5100 * time.Millisecond),
			NetworkAvailable: true, NetRx: uint64(i * 510)}
		if err := r.writeContainers(t.Context(), base, []dockerx.ContainerStats{st}); err != nil {
			t.Fatal(err)
		}
	}
	s, err := r.ContainerRange(t.Context(), "web", base, base.Add(time.Minute), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Points) != 2 {
		t.Fatalf("points = %d", len(s.Points))
	}
	rate := s.Points[1].NetRx
	if rate == nil || math.Abs(*rate-100) > 0.001 {
		t.Fatalf("rate = %v; truncating the 5.1s interval to 6s understates traffic", rate)
	}
}

func TestContainerUsageUsesElapsedTimeAndPreservesPeaks(t *testing.T) {
	r := testRecorder(t, 5*time.Second, time.Hour)
	base := time.Unix(1_800_000_000, 0).UTC()
	for i, second := range []int{0, 6, 15} {
		st := dockerx.ContainerStats{ID: "id", Name: "web", NetworkAvailable: true, NetRx: []uint64{0, 600, 2400}[i]}
		if err := r.writeContainers(t.Context(), base.Add(time.Duration(second)*time.Second), []dockerx.ContainerStats{st}); err != nil {
			t.Fatal(err)
		}
	}
	series, err := r.ContainerRange(t.Context(), "web", base, base.Add(60*time.Second), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(series.Points) != 1 {
		t.Fatalf("points = %d", len(series.Points))
	}
	p := series.Points[0]
	if p.NetRx == nil || math.Abs(*p.NetRx-160) > 0.001 || p.NetRxPeak == nil || *p.NetRxPeak != 200 {
		t.Fatalf("weighted rate/peak = %+v", p)
	}
}

func TestContainerUsageRejectsResetsGapsAndAbsentCounters(t *testing.T) {
	for _, kind := range []string{"first", "identity", "cpu reset", "counter reset", "gap", "unavailable", "idle"} {
		t.Run(kind, func(t *testing.T) {
			r := testRecorder(t, 5*time.Second, time.Hour)
			base := time.Unix(1_800_000_000, 0).UTC()
			before := dockerx.ContainerStats{ID: "old", Name: "web", CPUTotal: 100, NetworkAvailable: true, NetRx: 1000}
			after := before
			after.NetRx = 1500
			elapsed := 5 * time.Second
			switch kind {
			case "identity":
				after.ID = "new"
			case "cpu reset":
				after.CPUTotal = 1
			case "counter reset":
				after.NetRx = 1
			case "gap":
				elapsed = time.Minute
			case "unavailable":
				after.NetworkAvailable = false
			case "idle":
				before.NetRx = 0
				after.NetRx = 0
			}
			if kind != "first" {
				if err := r.writeContainers(t.Context(), base, []dockerx.ContainerStats{before}); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.writeContainers(t.Context(), base.Add(elapsed), []dockerx.ContainerStats{after}); err != nil {
				t.Fatal(err)
			}
			series, err := r.ContainerRange(t.Context(), "web", base.Add(elapsed), base.Add(elapsed+time.Second), 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(series.Points) != 1 {
				t.Fatalf("points = %d", len(series.Points))
			}
			rx := series.Points[0].NetRx
			if kind == "idle" {
				if rx == nil || *rx != 0 {
					t.Fatalf("idle rate = %v", rx)
				}
			} else if rx != nil {
				t.Fatalf("%s fabricated a rate: %v", kind, *rx)
			}
		})
	}
}

func TestContainerUsageDoesNotInventLegacyAvailabilityOrLimits(t *testing.T) {
	r := testRecorder(t, 5*time.Second, time.Hour)
	base := time.Unix(1_800_000_000, 0).UTC()
	for i := range 2 {
		_, err := r.db.Exec(`INSERT INTO metric_container_samples(ts,name,mem_limit) VALUES(?,?,?)`, base.Unix()+int64(i*5), "old", 8<<30)
		if err != nil {
			t.Fatal(err)
		}
	}
	series, err := r.ContainerRange(t.Context(), "old", base, base.Add(time.Minute), 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range series.Points {
		if p.NetRx != nil || p.BlockRead != nil || p.MemLimit != 0 {
			t.Fatalf("legacy absence inferred as measurement: %+v", p)
		}
	}
	for _, limited := range []bool{false, true} {
		st := dockerx.ContainerStats{Name: "new", MemLimit: 512 << 20, MemLimited: limited}
		if err := r.writeContainers(t.Context(), base, []dockerx.ContainerStats{st}); err != nil {
			t.Fatal(err)
		}
		s, err := r.ContainerRange(t.Context(), "new", base, base.Add(time.Second), 1)
		if err != nil {
			t.Fatal(err)
		}
		if (s.Points[0].MemLimit > 0) != limited {
			t.Fatalf("limited=%v but limit=%v", limited, s.Points[0].MemLimit)
		}
	}
}
