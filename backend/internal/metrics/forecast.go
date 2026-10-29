package metrics

import (
	"context"
	"math"
	"sort"
	"time"
)

// The capacity forecast: a straight line through what the record already
// holds — each filesystem's used bytes, each deployment volume's size — and
// where it meets the ceiling. A line is the honest model for a week of
// samples: it says "at the rate it has been growing", which is what an
// operator can check against what they know, and its fit says how much the
// samples agree with it. Anything cleverer would claim to know about next
// month's traffic.

// Forecast statuses.
const (
	ForecastFilling      = "filling"
	ForecastSteady       = "steady"
	ForecastShrinking    = "shrinking"
	ForecastInsufficient = "insufficient_history"
)

// Confidence of a fit.
const (
	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
	ConfidenceLow    = "low"
)

const (
	// forecastBucket is the width the samples are averaged over before the
	// line is fitted: an hour of a fifteen-second record is 240 points that
	// say one thing, and a week is 168 hours a fit can weigh evenly.
	forecastBucket = time.Hour
	// minForecastSpan and minForecastPoints are the least a line rests on.
	minForecastSpan   = 6 * time.Hour
	minForecastPoints = 3
	// steadyBytesPerDay is growth too small to call growth: a log rotated
	// daily moves a disk by more than this and back.
	steadyBytesPerDay = 1 << 20
)

// FitPoint is one point of a series: X in days, Y in bytes.
type FitPoint struct{ X, Y float64 }

// Fit is an ordinary least-squares line and how well the points agree with it.
type Fit struct {
	Slope, Intercept float64
	// R2 is the coefficient of determination: 1 when every point is on the
	// line, near 0 when the line explains none of how they vary. A series
	// that never varied is on its flat line exactly, and is 1.
	R2 float64
	N  int
	// Span is the days between the first point and the last.
	Span float64
}

// LinearFit fits y = Slope·x + Intercept by least squares. It needs two
// points at different x; ok is false otherwise.
func LinearFit(points []FitPoint) (Fit, bool) {
	n := float64(len(points))
	if len(points) < 2 {
		return Fit{N: len(points)}, false
	}
	var sx, sy float64
	minX, maxX := math.Inf(1), math.Inf(-1)
	for _, p := range points {
		sx, sy = sx+p.X, sy+p.Y
		minX, maxX = math.Min(minX, p.X), math.Max(maxX, p.X)
	}
	mx, my := sx/n, sy/n
	var sxx, sxy, syy float64
	for _, p := range points {
		dx, dy := p.X-mx, p.Y-my
		sxx, sxy, syy = sxx+dx*dx, sxy+dx*dy, syy+dy*dy
	}
	if sxx == 0 {
		return Fit{N: len(points)}, false
	}
	fit := Fit{Slope: sxy / sxx, N: len(points), Span: maxX - minX}
	fit.Intercept = my - fit.Slope*mx
	fit.R2 = 1
	if syy > 0 {
		var residual float64
		for _, p := range points {
			e := p.Y - (fit.Slope*p.X + fit.Intercept)
			residual += e * e
		}
		fit.R2 = math.Max(0, 1-residual/syy)
	}
	return fit, true
}

// FitConfidence grades a fit by how well the points agree with the line and
// how long they span: three days that lie on the line is high, a day that
// mostly does is medium, anything else is low.
func FitConfidence(fit Fit) string {
	switch {
	case fit.R2 >= 0.8 && fit.Span >= 3:
		return ConfidenceHigh
	case fit.R2 >= 0.5 && fit.Span >= 1:
		return ConfidenceMedium
	default:
		return ConfidenceLow
	}
}

// DaysToFull is how many days until used reaches total at slope bytes a day,
// or false when it is not growing. Already full is zero days.
func DaysToFull(used, total, slopePerDay float64) (float64, bool) {
	if total <= 0 || slopePerDay <= 0 {
		return 0, false
	}
	return math.Max(0, total-used) / slopePerDay, true
}

// growthStatus names a fitted slope.
func growthStatus(slope float64) string {
	switch {
	case slope >= steadyBytesPerDay:
		return ForecastFilling
	case slope <= -steadyBytesPerDay:
		return ForecastShrinking
	default:
		return ForecastSteady
	}
}

// fitSeries fits the points if they span enough, answering the status, the
// fit and whether there is one.
func fitSeries(points []FitPoint) (Fit, string, bool) {
	fit, ok := LinearFit(points)
	if !ok || len(points) < minForecastPoints || fit.Span*24 < minForecastSpan.Hours() {
		return fit, ForecastInsufficient, false
	}
	return fit, growthStatus(fit.Slope), true
}

// MountForecast is one filesystem's line.
type MountForecast struct {
	Mountpoint  string  `json:"mountpoint"`
	UsedBytes   uint64  `json:"usedBytes"`
	TotalBytes  uint64  `json:"totalBytes"`
	UsedPercent float64 `json:"usedPercent"`
	Status      string  `json:"status"`
	// Growth is the fitted slope in bytes a day; DaysToFull and FullAt are
	// set only while it is filling.
	GrowthBytesPerDay *float64   `json:"growthBytesPerDay"`
	DaysToFull        *float64   `json:"daysToFull"`
	FullAt            *time.Time `json:"fullAt"`
	R2                *float64   `json:"r2"`
	Confidence        string     `json:"confidence,omitempty"`
	// The history the line rests on: hourly means from From to To.
	From   *time.Time `json:"from"`
	To     *time.Time `json:"to"`
	Points int        `json:"points"`
}

// MountForecasts fits a line through each filesystem's used bytes over the
// retained record, in hourly means, and says when each fills at that rate.
// Only the mounts in the newest sample are forecast — one missing from it has
// been unmounted — and the newest sample, not the line, is where each starts.
func (r *Recorder) MountForecasts(ctx context.Context, now time.Time) ([]MountForecast, error) {
	out := []MountForecast{}
	if !r.Enabled() {
		return out, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT mountpoint, used_bytes, total_bytes, used_percent
		  FROM metric_mount_samples WHERE ts = (SELECT MAX(ts) FROM metric_mount_samples)
		 ORDER BY mountpoint`)
	if err != nil {
		return nil, err
	}
	index := map[string]int{}
	for rows.Next() {
		var m MountForecast
		var used, total int64
		if err := rows.Scan(&m.Mountpoint, &used, &total, &m.UsedPercent); err != nil {
			rows.Close()
			return nil, err
		}
		m.UsedBytes, m.TotalBytes, m.UsedPercent = uint64(max(used, 0)), uint64(max(total, 0)), round1(m.UsedPercent)
		index[m.Mountpoint] = len(out)
		out = append(out, m)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	series, err := r.hourlySeries(ctx, `SELECT mountpoint, ts / ?2, AVG(ts), AVG(used_bytes)
		  FROM metric_mount_samples WHERE ts >= ?1 GROUP BY mountpoint, 2 ORDER BY mountpoint, 2`, now)
	if err != nil {
		return nil, err
	}
	for name, i := range index {
		m := &out[i]
		points := series[name]
		m.Points = len(points.points)
		m.From, m.To = points.from, points.to
		fit, status, ok := fitSeries(points.points)
		m.Status = status
		if !ok {
			continue
		}
		m.GrowthBytesPerDay, m.R2 = ptrf(math.Round(fit.Slope)), ptrf(round2(fit.R2))
		m.Confidence = FitConfidence(fit)
		if status != ForecastFilling {
			continue
		}
		if days, ok := DaysToFull(float64(m.UsedBytes), float64(m.TotalBytes), fit.Slope); ok {
			at := now.Add(time.Duration(days * 24 * float64(time.Hour))).UTC().Truncate(time.Hour)
			m.DaysToFull, m.FullAt = ptrf(round1(days)), &at
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return forecastBefore(out[i], out[j]) })
	return out, nil
}

// forecastBefore puts the mount that fills first first, then the filling
// ones without a date, then the rest by how full they are.
func forecastBefore(a, b MountForecast) bool {
	switch {
	case a.DaysToFull != nil && b.DaysToFull != nil:
		return *a.DaysToFull < *b.DaysToFull
	case a.DaysToFull != nil:
		return true
	case b.DaysToFull != nil:
		return false
	}
	return a.UsedPercent > b.UsedPercent
}

// VolumeGrowth is one volume's line.
type VolumeGrowth struct {
	Name              string     `json:"name"`
	SizeBytes         uint64     `json:"sizeBytes"`
	Status            string     `json:"status"`
	GrowthBytesPerDay *float64   `json:"growthBytesPerDay"`
	R2                *float64   `json:"r2"`
	Confidence        string     `json:"confidence,omitempty"`
	From              *time.Time `json:"from"`
	To                *time.Time `json:"to"`
	Points            int        `json:"points"`
	// Measured says SizeBytes is a measurement: the newest sample's here,
	// or Docker's at the moment where the caller read it. False is a volume
	// whose size nothing has read yet, and its zero is not a size.
	Measured bool `json:"measured"`
}

// VolumeGrowths fits a line through each named volume's recorded size, in
// hourly means over the retained record. A volume with no sample is returned
// unmeasured, so a caller can still say how big it is now.
func (r *Recorder) VolumeGrowths(ctx context.Context, names []string, now time.Time) (map[string]VolumeGrowth, error) {
	out := make(map[string]VolumeGrowth, len(names))
	for _, name := range names {
		out[name] = VolumeGrowth{Name: name, Status: ForecastInsufficient}
	}
	if !r.Enabled() || len(names) == 0 {
		return out, nil
	}
	series, err := r.hourlySeries(ctx, `SELECT name, ts / ?2, AVG(ts), AVG(size_bytes)
		  FROM metric_volume_samples WHERE ts >= ?1 GROUP BY name, 2 ORDER BY name, 2`, now)
	if err != nil {
		return nil, err
	}
	latest, err := r.db.QueryContext(ctx, `
		SELECT v.name, v.size_bytes FROM metric_volume_samples v
		  JOIN (SELECT name, MAX(ts) AS ts FROM metric_volume_samples GROUP BY name) n
		    ON n.name = v.name AND n.ts = v.ts`)
	if err != nil {
		return nil, err
	}
	sizes := map[string]int64{}
	for latest.Next() {
		var name string
		var size int64
		if err := latest.Scan(&name, &size); err != nil {
			latest.Close()
			return nil, err
		}
		sizes[name] = size
	}
	if err := latest.Close(); err != nil {
		return nil, err
	}
	for _, name := range names {
		growth := out[name]
		size, measured := sizes[name]
		growth.SizeBytes, growth.Measured = uint64(max(size, 0)), measured
		points := series[name]
		growth.Points, growth.From, growth.To = len(points.points), points.from, points.to
		fit, status, ok := fitSeries(points.points)
		growth.Status = status
		if ok {
			growth.GrowthBytesPerDay, growth.R2 = ptrf(math.Round(fit.Slope)), ptrf(round2(fit.R2))
			growth.Confidence = FitConfidence(fit)
		}
		out[name] = growth
	}
	return out, nil
}

type hourly struct {
	points   []FitPoint
	from, to *time.Time
}

// hourlySeries runs a query answering (key, bucket, mean ts, mean value) over
// the retained window and groups it by key, x in days from the window's
// start. ?1 is the window's start and ?2 the bucket width in seconds.
func (r *Recorder) hourlySeries(ctx context.Context, query string, now time.Time) (map[string]hourly, error) {
	start := now.Add(-r.retention)
	rows, err := r.db.QueryContext(ctx, query, start.Unix(), int64(forecastBucket/time.Second))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]hourly{}
	for rows.Next() {
		var key string
		var bucket int64
		var ts, value float64
		if err := rows.Scan(&key, &bucket, &ts, &value); err != nil {
			return nil, err
		}
		series := out[key]
		at := time.Unix(int64(ts), 0).UTC()
		if series.from == nil {
			series.from = &at
		}
		series.to = &at
		series.points = append(series.points, FitPoint{X: ts / 86400, Y: value})
		out[key] = series
	}
	return out, rows.Err()
}
