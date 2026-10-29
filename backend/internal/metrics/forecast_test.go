package metrics

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/sysinfo"
)

func TestLinearFitFindsTheLineAndHowWellItFits(t *testing.T) {
	exact := []FitPoint{{0, 10}, {1, 30}, {2, 50}, {3, 70}}
	fit, ok := LinearFit(exact)
	if !ok || fit.Slope != 20 || fit.Intercept != 10 || fit.R2 != 1 || fit.Span != 3 || fit.N != 4 {
		t.Fatalf("exact line = %+v %v", fit, ok)
	}
	// Scatter about the same line lowers R² without moving the slope much.
	noisy, _ := LinearFit([]FitPoint{{0, 15}, {1, 25}, {2, 55}, {3, 65}})
	if math.Abs(noisy.Slope-18) > 1e-9 || noisy.R2 >= 1 || noisy.R2 < 0.9 {
		t.Errorf("noisy = %+v", noisy)
	}
	// Points with no trend at all explain nothing.
	none, _ := LinearFit([]FitPoint{{0, 10}, {1, 20}, {2, 10}, {3, 20}, {4, 10}})
	if none.R2 > 0.1 {
		t.Errorf("no trend R² = %v", none.R2)
	}
	flat, ok := LinearFit([]FitPoint{{0, 5}, {1, 5}, {2, 5}})
	if !ok || flat.Slope != 0 || flat.R2 != 1 {
		t.Errorf("flat = %+v", flat)
	}
	if _, ok := LinearFit([]FitPoint{{1, 5}, {1, 9}}); ok {
		t.Error("a vertical series fitted")
	}
	if _, ok := LinearFit([]FitPoint{{1, 5}}); ok {
		t.Error("one point fitted")
	}
}

func TestConfidenceNeedsAgreementAndDays(t *testing.T) {
	cases := []struct {
		fit  Fit
		want string
	}{
		{Fit{R2: 0.95, Span: 4}, ConfidenceHigh},
		{Fit{R2: 0.95, Span: 2}, ConfidenceMedium},
		{Fit{R2: 0.6, Span: 6}, ConfidenceMedium},
		{Fit{R2: 0.95, Span: 0.5}, ConfidenceLow},
		{Fit{R2: 0.3, Span: 6}, ConfidenceLow},
	}
	for _, c := range cases {
		if got := FitConfidence(c.fit); got != c.want {
			t.Errorf("%+v = %s, want %s", c.fit, got, c.want)
		}
	}
	if days, ok := DaysToFull(80, 100, 4); !ok || days != 5 {
		t.Errorf("filling = %v %v", days, ok)
	}
	if days, ok := DaysToFull(120, 100, 4); !ok || days != 0 {
		t.Errorf("already full = %v %v", days, ok)
	}
	for _, slope := range []float64{0, -3} {
		if _, ok := DaysToFull(80, 100, slope); ok {
			t.Errorf("slope %v forecast a date", slope)
		}
	}
}

const gib = 1 << 30

// Four days of a filesystem gaining 2 GiB a day, one standing still, and one
// that was unmounted before the newest sample: the first fills when the line
// says, the second never, and the third is not forecast.
func TestMountForecastsFitEachFilesystem(t *testing.T) {
	r := testRecorder(t, 5*time.Minute, 7*24*time.Hour)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Hour)
	start := now.Add(-4 * 24 * time.Hour)
	for at := start; !at.After(now); at = at.Add(30 * time.Minute) {
		days := at.Sub(start).Hours() / 24
		mounts := []sysinfo.MountStats{
			{Mountpoint: "/", Used: uint64((40 + 2*days) * gib), Total: 100 * gib, UsedPercent: 40 + 2*days},
			{Mountpoint: "/srv", Used: 10 * gib, Total: 500 * gib, UsedPercent: 2},
		}
		if at.Before(now) {
			mounts = append(mounts, sysinfo.MountStats{Mountpoint: "/mnt/usb", Used: gib, Total: 2 * gib, UsedPercent: 50})
		}
		if err := r.recordMounts(ctx, at, mounts); err != nil {
			t.Fatal(err)
		}
	}
	forecasts, err := r.MountForecasts(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(forecasts) != 2 || forecasts[0].Mountpoint != "/" || forecasts[1].Mountpoint != "/srv" {
		t.Fatalf("forecasts = %+v", forecasts)
	}
	root := forecasts[0]
	if root.Status != ForecastFilling || root.Confidence != ConfidenceHigh || root.R2 == nil || *root.R2 < 0.99 {
		t.Fatalf("/ = %+v", root)
	}
	if growth := *root.GrowthBytesPerDay / gib; math.Abs(growth-2) > 0.01 {
		t.Errorf("/ grows %v GiB a day", growth)
	}
	// 48 GiB used at the newest sample, 52 left at 2 a day.
	if root.DaysToFull == nil || math.Abs(*root.DaysToFull-26) > 0.2 || root.FullAt == nil || root.Points < 90 {
		t.Errorf("/ fills in %v days at %v from %d points", root.DaysToFull, root.FullAt, root.Points)
	}
	if srv := forecasts[1]; srv.Status != ForecastSteady || srv.DaysToFull != nil || *srv.GrowthBytesPerDay != 0 {
		t.Errorf("/srv = %+v", srv)
	}
}

func TestAShortRecordIsNotForecast(t *testing.T) {
	r := testRecorder(t, 5*time.Minute, 7*24*time.Hour)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Hour)
	for i := range 3 {
		at := now.Add(time.Duration(i-2) * time.Hour)
		if err := r.recordMounts(ctx, at, []sysinfo.MountStats{{Mountpoint: "/", Used: uint64(i+1) * gib, Total: 10 * gib}}); err != nil {
			t.Fatal(err)
		}
	}
	forecasts, err := r.MountForecasts(ctx, now)
	if err != nil || len(forecasts) != 1 {
		t.Fatalf("forecasts = %+v %v", forecasts, err)
	}
	if f := forecasts[0]; f.Status != ForecastInsufficient || f.DaysToFull != nil || f.GrowthBytesPerDay != nil || f.Points != 3 {
		t.Errorf("two hours = %+v", f)
	}
	off := testRecorder(t, 5*time.Minute, 0)
	if forecasts, err := off.MountForecasts(ctx, now); err != nil || len(forecasts) != 0 {
		t.Errorf("retention off = %+v %v", forecasts, err)
	}
}

type fakeVolumes struct {
	sizes []VolumeSize
	err   error
	calls int
}

func (f *fakeVolumes) SampleVolumes(context.Context) ([]VolumeSize, error) {
	f.calls++
	return f.sizes, f.err
}

// The sampler's sizes are kept per volume, a size Docker did not measure is
// not kept as an empty volume, a failing sampler records nothing, and the
// rows expire with the rest of the record.
func TestVolumeSizesAreRecordedAndExpire(t *testing.T) {
	r := testRecorder(t, 15*time.Second, 2*time.Hour)
	ctx := context.Background()
	volumes := &fakeVolumes{sizes: []VolumeSize{{"shop-uploads", 5 * gib}, {"shop-cache", -1}, {"", 3}}}
	r.WithVolumes(volumes)
	if r.VolumeSampling() != VolumeInterval {
		t.Errorf("sampling every %v", r.VolumeSampling())
	}
	old := time.Now().Add(-3 * time.Hour)
	r.recordVolumes(ctx, old)
	r.recordVolumes(ctx, time.Now())
	volumes.err = errors.New("docker is not running")
	r.recordVolumes(ctx, time.Now().Add(time.Second))
	var rows, unmeasured int
	if err := r.db.QueryRow(`SELECT COUNT(*), COUNT(CASE WHEN name != 'shop-uploads' THEN 1 END) FROM metric_volume_samples`).Scan(&rows, &unmeasured); err != nil {
		t.Fatal(err)
	}
	if rows != 2 || unmeasured != 0 || volumes.calls != 3 {
		t.Fatalf("rows = %d, unmeasured = %d, calls = %d", rows, unmeasured, volumes.calls)
	}
	if err := r.prune(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM metric_volume_samples`).Scan(&rows); err != nil || rows != 1 {
		t.Errorf("after prune %d rows, %v", rows, err)
	}
	if testRecorder(t, 15*time.Second, 0).WithVolumes(volumes).VolumeSampling() != 0 {
		t.Error("a disabled record claims to sample volumes")
	}
}

// A volume growing half a gigabyte a day for four days, one sampled once,
// and one never sampled at all.
func TestVolumeGrowthsFitEachVolume(t *testing.T) {
	r := testRecorder(t, 15*time.Second, 7*24*time.Hour)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Hour)
	start := now.Add(-4 * 24 * time.Hour)
	for at := start; !at.After(now); at = at.Add(VolumeInterval) {
		days := at.Sub(start).Hours() / 24
		sizes := []VolumeSize{{"uploads", int64((1 + 0.5*days) * gib)}}
		if at.Equal(now) {
			sizes = append(sizes, VolumeSize{"fresh", gib})
		}
		if err := r.writeVolumes(ctx, at, sizes); err != nil {
			t.Fatal(err)
		}
	}
	growths, err := r.VolumeGrowths(ctx, []string{"uploads", "fresh", "never"}, now)
	if err != nil {
		t.Fatal(err)
	}
	uploads := growths["uploads"]
	if uploads.Status != ForecastFilling || uploads.Confidence != ConfidenceHigh || !uploads.Measured ||
		math.Abs(*uploads.GrowthBytesPerDay/gib-0.5) > 0.01 || uploads.SizeBytes != uint64(3*gib) {
		t.Fatalf("uploads = %+v", uploads)
	}
	if fresh := growths["fresh"]; fresh.Status != ForecastInsufficient || !fresh.Measured || fresh.SizeBytes != gib || fresh.GrowthBytesPerDay != nil {
		t.Errorf("fresh = %+v", fresh)
	}
	if never := growths["never"]; never.Measured || never.Status != ForecastInsufficient || never.Points != 0 {
		t.Errorf("never = %+v", never)
	}
}
