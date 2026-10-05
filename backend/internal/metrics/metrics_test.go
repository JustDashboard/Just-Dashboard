package metrics

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
	"github.com/Wayy01/Just-Dashboard/backend/internal/sysinfo"
)

func testRecorder(t *testing.T, interval, retention time.Duration) *Recorder {
	t.Helper()
	st, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st, slog.New(slog.NewTextHandler(io.Discard, nil)), interval, retention)
}

func TestParseWindow(t *testing.T) {
	cases := map[string]time.Duration{
		"30s":  30 * time.Second,
		"15m":  15 * time.Minute,
		"6h":   6 * time.Hour,
		"1d":   24 * time.Hour,
		"7d":   7 * 24 * time.Hour,
		"0.5d": 12 * time.Hour,
		"2w":   14 * 24 * time.Hour,
	}
	for in, want := range cases {
		got, err := ParseWindow(in)
		if err != nil {
			t.Fatalf("ParseWindow(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("ParseWindow(%q) = %v, want %v", in, got, want)
		}
	}
	for _, in := range []string{"", "d", "-1d", "0d", "banana", "7 d"} {
		if _, err := ParseWindow(in); err == nil {
			t.Errorf("ParseWindow(%q) accepted a value it should have refused", in)
		}
	}
}

// A bucket narrower than the sampling interval would produce empty buckets
// between real ones, which a chart draws as a gap that never happened.
func TestBucketNeverFinerThanTheData(t *testing.T) {
	step := bucketFor(time.Minute, 600, 15*time.Second)
	if step != 15*time.Second {
		t.Fatalf("bucket = %v, want the 15s sampling interval", step)
	}
	if step := bucketFor(24*time.Hour, 240, 15*time.Second); step != 6*time.Minute {
		t.Fatalf("bucket for a day at 240 points = %v, want 6m", step)
	}
	// A window that does not divide evenly must round up: rounding down would
	// hand back more points than the caller asked for.
	if step := bucketFor(100*time.Second, 3, time.Second); step != 34*time.Second {
		t.Fatalf("bucket = %v, want 34s", step)
	}
}

// The whole point of storing peaks alongside means: a one-sample spike inside
// a wide bucket has to survive downsampling, or the record says the night was
// quiet when it was not.
func TestRangeKeepsPeaksThroughDownsampling(t *testing.T) {
	r := testRecorder(t, 15*time.Second, 24*time.Hour)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour).Truncate(time.Second)

	for i := range 60 {
		cpu := 5.0
		if i == 37 {
			cpu = 99.0 // the spike nobody was watching
		}
		if err := r.record(ctx, Sample{
			TS:         base.Add(time.Duration(i) * 15 * time.Second),
			CPUPercent: cpu,
			NetRx:      float64(i),
		}); err != nil {
			t.Fatal(err)
		}
	}

	series, err := r.Range(ctx, base.Add(-time.Minute), time.Now(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(series.Points) == 0 {
		t.Fatal("no points returned")
	}
	if len(series.Points) > 4 {
		t.Fatalf("got %d points, asked for at most 4", len(series.Points))
	}
	var peak, mean float64
	var samples int
	for _, p := range series.Points {
		samples += p.Samples
		if p.CPUPeak > peak {
			peak = p.CPUPeak
		}
		if p.CPU > mean {
			mean = p.CPU
		}
	}
	if samples != 60 {
		t.Fatalf("aggregated %d samples, want 60", samples)
	}
	if peak != 99 {
		t.Fatalf("peak CPU = %v, want the 99%% spike to survive the bucket", peak)
	}
	if mean >= 99 {
		t.Fatalf("mean CPU = %v; the average should have flattened the spike, not carried it", mean)
	}
	if series.Earliest == nil || !series.Earliest.Equal(base.UTC()) {
		t.Fatalf("earliest = %v, want %v", series.Earliest, base.UTC())
	}
	if series.RetentionSeconds != int(24*time.Hour/time.Second) {
		t.Fatalf("retention = %ds", series.RetentionSeconds)
	}
}

func TestRangeIsEmptyRatherThanNilWithoutData(t *testing.T) {
	r := testRecorder(t, 15*time.Second, time.Hour)
	series, err := r.Range(context.Background(), time.Now().Add(-time.Hour), time.Now(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if series.Points == nil {
		t.Fatal("Points is nil; the frontend distinguishes an empty series from a failed one")
	}
	if series.Earliest != nil {
		t.Fatalf("earliest = %v with no rows stored", series.Earliest)
	}
}

func TestPruneDropsExpiredSamples(t *testing.T) {
	r := testRecorder(t, 15*time.Second, time.Hour)
	ctx := context.Background()
	now := time.Now()
	for _, age := range []time.Duration{3 * time.Hour, 2 * time.Hour, 30 * time.Minute, time.Minute} {
		if err := r.record(ctx, Sample{TS: now.Add(-age), CPUPercent: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.prune(ctx); err != nil {
		t.Fatal(err)
	}
	series, err := r.Range(ctx, now.Add(-24*time.Hour), now, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if len(series.Points) != 2 {
		t.Fatalf("kept %d points, want the 2 inside the retention window", len(series.Points))
	}
}

// Recording twice in one second is what a restart looks like; it must not
// fail the insert, because the sampler would then log an error every tick.
func TestRecordReplacesWithinTheSameSecond(t *testing.T) {
	r := testRecorder(t, 15*time.Second, time.Hour)
	ctx := context.Background()
	at := time.Now().Truncate(time.Second)
	if err := r.record(ctx, Sample{TS: at, CPUPercent: 10}); err != nil {
		t.Fatal(err)
	}
	if err := r.record(ctx, Sample{TS: at, CPUPercent: 80}); err != nil {
		t.Fatal(err)
	}
	series, err := r.Range(ctx, at.Add(-time.Minute), at.Add(time.Minute), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(series.Points) != 1 || series.Points[0].Samples != 1 {
		t.Fatalf("got %d points; the second write should have replaced the first", len(series.Points))
	}
	if series.Points[0].CPU != 80 {
		t.Fatalf("cpu = %v, want the later value", series.Points[0].CPU)
	}
}

func TestReduceSummarisesTheSnapshot(t *testing.T) {
	s := Reduce(&sysinfo.Snapshot{
		CPU:    sysinfo.CPUStats{TotalPercent: 42, LoadAvg1: 1.5},
		Memory: sysinfo.MemoryStats{UsedPercent: 60, Used: 100, Total: 200},
		Net: []sysinfo.NetStats{
			{RecvRate: 10, SendRate: 1},
			{RecvRate: 5, SendRate: 2},
		},
		Mounts: []sysinfo.MountStats{
			{UsedPercent: 12, ReadRate: 100, WriteRate: 10},
			{UsedPercent: 91, ReadRate: 50, WriteRate: 5},
		},
	})
	if s.NetRx != 15 || s.NetTx != 3 {
		t.Fatalf("network rates = %v/%v, want the sum across interfaces", s.NetRx, s.NetTx)
	}
	if s.DiskRead != 150 || s.DiskWrite != 15 {
		t.Fatalf("disk rates = %v/%v", s.DiskRead, s.DiskWrite)
	}
	// The fullest mount, not the mean: an almost-full disk is the thing worth
	// noticing, and averaging it with an empty one hides it.
	if s.DiskPercent != 91 {
		t.Fatalf("disk percent = %v, want the fullest mount", s.DiskPercent)
	}
}

func TestDisabledRecorderRecordsNothing(t *testing.T) {
	r := testRecorder(t, 15*time.Second, 0)
	if r.Enabled() {
		t.Fatal("retention 0 should disable recording")
	}
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.Stop() // must be a no-op rather than a nil-channel receive
}

func TestNewClampsAbsurdIntervals(t *testing.T) {
	if got := testRecorder(t, time.Millisecond, time.Hour).Interval(); got != MinInterval {
		t.Fatalf("interval = %v, want it clamped to %v", got, MinInterval)
	}
	if got := testRecorder(t, time.Hour, time.Hour).Interval(); got != MaxInterval {
		t.Fatalf("interval = %v, want it clamped to %v", got, MaxInterval)
	}
	if got := testRecorder(t, 15*time.Second, time.Minute).Retention(); got != MinRetention {
		t.Fatalf("retention = %v, want it clamped to %v", got, MinRetention)
	}
}

// fakeContainers stands in for the Docker client so the recorder's container
// path can be exercised on a machine with no Docker socket.
type fakeContainers struct {
	batches [][]dockerx.ContainerStats
	err     error
	calls   int
}

func (f *fakeContainers) SampleAll(context.Context) ([]dockerx.ContainerStats, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.calls >= len(f.batches) {
		return nil, nil
	}
	batch := f.batches[f.calls]
	f.calls++
	return batch, nil
}

// The series is keyed by name rather than id precisely so a compose redeploy
// does not start a fresh empty chart: the container comes back with a new id
// under the same name and the history has to continue through it.
func TestContainerHistorySurvivesARecreate(t *testing.T) {
	r := testRecorder(t, 15*time.Second, 24*time.Hour)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour).Truncate(time.Second)

	for i := range 40 {
		id := "old-id"
		if i >= 20 {
			id = "new-id-after-redeploy"
		}
		cpu := 3.0
		if i == 25 {
			cpu = 180.0 // pinned nearly two cores for one sample
		}
		r.recordContainersAt(ctx, base.Add(time.Duration(i)*15*time.Second), []dockerx.ContainerStats{
			{ID: id, Name: "api", CPUPercent: cpu, MemUsage: 100 << 20, MemLimit: 512 << 20, MemPercent: 19.5, PIDs: 12},
		})
	}

	series, err := r.ContainerRange(ctx, "api", base.Add(-time.Minute), time.Now(), 4)
	if err != nil {
		t.Fatal(err)
	}
	var samples int
	var peak float64
	for _, p := range series.Points {
		samples += p.Samples
		if p.CPUPeak > peak {
			peak = p.CPUPeak
		}
	}
	if samples != 40 {
		t.Fatalf("aggregated %d samples across the redeploy, want 40", samples)
	}
	if peak != 180 {
		t.Fatalf("peak CPU = %v, want the 180%% burst to survive the bucket", peak)
	}
	if series.Name != "api" {
		t.Fatalf("series name = %q", series.Name)
	}
	if series.Earliest == nil || !series.Earliest.Equal(base.UTC()) {
		t.Fatalf("earliest = %v, want this container's first sample at %v", series.Earliest, base.UTC())
	}
}

func TestContainerHistoryIsPerContainer(t *testing.T) {
	r := testRecorder(t, 15*time.Second, 24*time.Hour)
	ctx := context.Background()
	at := time.Now().Add(-time.Minute).Truncate(time.Second)
	r.recordContainersAt(ctx, at, []dockerx.ContainerStats{
		{Name: "api", CPUPercent: 10},
		{Name: "db", CPUPercent: 90},
		{Name: "", CPUPercent: 50}, // nothing to key a series on
	})

	names, err := r.RecordedContainers(ctx, at.Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "api" || names[1] != "db" {
		t.Fatalf("recorded containers = %v, want [api db] and no nameless row", names)
	}

	db, err := r.ContainerRange(ctx, "db", at.Add(-time.Hour), time.Now(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(db.Points) != 1 || db.Points[0].CPU != 90 {
		t.Fatalf("db series = %+v, want only db's own samples", db.Points)
	}
}

func TestPruneDropsExpiredContainerSamples(t *testing.T) {
	r := testRecorder(t, 15*time.Second, time.Hour)
	ctx := context.Background()
	now := time.Now()
	for _, age := range []time.Duration{3 * time.Hour, 30 * time.Minute} {
		r.recordContainersAt(ctx, now.Add(-age), []dockerx.ContainerStats{{Name: "api", CPUPercent: 1}})
	}
	if err := r.prune(ctx); err != nil {
		t.Fatal(err)
	}
	series, err := r.ContainerRange(ctx, "api", now.Add(-24*time.Hour), now, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if len(series.Points) != 1 {
		t.Fatalf("kept %d container points, want the 1 inside retention", len(series.Points))
	}
}

// A host with no Docker socket must keep recording its own metrics, and must
// not fill the log with one identical line every sampling interval.
func TestContainerFailuresDoNotStopHostRecording(t *testing.T) {
	r := testRecorder(t, 15*time.Second, time.Hour)
	r.WithContainers(&fakeContainers{err: errors.New("cannot reach the docker daemon")})
	ctx := context.Background()

	for range 3 {
		if err := r.sample(ctx); err != nil {
			t.Fatalf("a Docker failure stopped the host sample: %v", err)
		}
	}
	if !r.dockerQuiet {
		t.Error("the recorder is still ready to log the same Docker failure again")
	}

	series, err := r.Range(ctx, time.Now().Add(-time.Hour), time.Now(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(series.Points) == 0 {
		t.Fatal("no host samples recorded while Docker was unreachable")
	}
}

// Capacity is recorded per filesystem because a single worst-of line cannot
// keep its own meaning: when the fullest mount stops being the fullest, it
// drops to whatever the runner-up was and reads as space freed on a disk that
// never changed.
func TestStorageRangeIsPerFilesystem(t *testing.T) {
	r := testRecorder(t, 15*time.Second, 24*time.Hour)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour).Truncate(time.Second)

	for i := range 20 {
		// /boot starts nearly full and is cleared halfway through; / creeps up.
		boot := 92.0
		if i >= 10 {
			boot = 20.0
		}
		if err := r.recordMounts(ctx, base.Add(time.Duration(i)*15*time.Second), []sysinfo.MountStats{
			{Mountpoint: "/", UsedPercent: 60 + float64(i)/10, Used: 600, Total: 1000},
			{Mountpoint: "/boot", UsedPercent: boot, Used: 92, Total: 100},
			{Mountpoint: "", UsedPercent: 50}, // nothing to key a series on
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Asked for more points than there are samples, so the buckets cannot be
	// coarser than the sampling interval and each sample stands alone. What is
	// under test here is that the two filesystems stay separate series, not
	// the downsampling — that has its own test.
	series, err := r.StorageRange(ctx, base.Add(-time.Second), base.Add(5*time.Minute), 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(series.Mounts) != 2 {
		t.Fatalf("got %d filesystems, want / and /boot and no nameless row: %+v", len(series.Mounts), series.Mounts)
	}

	byMount := map[string][]MountPoint{}
	for _, m := range series.Mounts {
		byMount[m.Mountpoint] = m.Points
		if len(m.Points) != 20 {
			t.Fatalf("%s has %d points, want one per sample", m.Mountpoint, len(m.Points))
		}
	}

	// The clear-out has to show as /boot's own line falling, and must leave
	// the root filesystem's line untouched.
	boot := byMount["/boot"]
	if boot[0].UsedPercentPeak != 92 {
		t.Errorf("/boot opened at %v%%, want 92", boot[0].UsedPercentPeak)
	}
	if last := boot[len(boot)-1]; last.UsedPercent != 20 {
		t.Errorf("/boot ended at %v%%, want 20 after being cleared", last.UsedPercent)
	}

	// The root filesystem's own line must be unaffected by /boot's swing.
	root := byMount["/"]
	if root[0].UsedPercent < 59 || root[len(root)-1].UsedPercent < root[0].UsedPercent {
		t.Errorf("/ series = %+v, want a slow climb of its own", root)
	}
	if root[0].Total != 1000 {
		t.Errorf("/ total = %d, want the recorded capacity", root[0].Total)
	}
}

func TestPruneDropsExpiredMountSamples(t *testing.T) {
	r := testRecorder(t, 15*time.Second, time.Hour)
	ctx := context.Background()
	now := time.Now()
	for _, age := range []time.Duration{3 * time.Hour, 30 * time.Minute} {
		if err := r.recordMounts(ctx, now.Add(-age), []sysinfo.MountStats{
			{Mountpoint: "/", UsedPercent: 50},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.prune(ctx); err != nil {
		t.Fatal(err)
	}
	series, err := r.StorageRange(ctx, now.Add(-24*time.Hour), now, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if len(series.Mounts) != 1 || len(series.Mounts[0].Points) != 1 {
		t.Fatalf("kept %+v, want the single point inside retention", series.Mounts)
	}
}
