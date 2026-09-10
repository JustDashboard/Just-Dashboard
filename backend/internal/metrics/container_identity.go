package metrics

import (
	"context"
	"errors"
	"strings"
	"time"
)

var ErrContainerIdentityWindow = errors.New("container identity history requires 1..64 distinct identities and a window of at most 24 hours")

// Identity history is deliberately separate from name-continuous Docker charts.
// A replacement or a renamed container must not acquire another release's data.
type ContainerIdentityPoint struct {
	TS           time.Time `json:"ts"`
	Samples      int       `json:"samples"`
	CPU          float64   `json:"cpu"`
	CPUPeak      float64   `json:"cpuPeak"`
	Mem          float64   `json:"mem"`
	MemPeak      float64   `json:"memPeak"`
	MemBytes     uint64    `json:"memBytes"`
	MemBytesPeak uint64    `json:"memBytesPeak"`
	MemLimit     uint64    `json:"memLimit"`
}

type ContainerIdentitySeries struct {
	ContainerID string                   `json:"containerId"`
	Points      []ContainerIdentityPoint `json:"points"`
}

type ContainerIdentityHistory struct {
	Window
	Series []ContainerIdentitySeries `json:"series"`
}

// ContainerIdentityRange uses one bounded aggregation for the entire release.
// Empty series are retained so callers cannot confuse absent history with zero
// utilization. Pre-upgrade rows have no identity and cannot enter these series.
func (r *Recorder) ContainerIdentityRange(ctx context.Context, ids []string, from, to time.Time, maxPoints int) (*ContainerIdentityHistory, error) {
	if len(ids) == 0 || len(ids) > 64 || !to.After(from) || to.Sub(from) > 24*time.Hour {
		return nil, ErrContainerIdentityWindow
	}
	indexes := make(map[string]int, len(ids))
	args := make([]any, 0, len(ids))
	marks := make([]string, len(ids))
	for i, id := range ids {
		if _, exists := indexes[id]; exists || id == "" || len(id) > 128 {
			return nil, ErrContainerIdentityWindow
		}
		indexes[id], marks[i] = i, "?"
		args = append(args, id)
	}
	if maxPoints < 1 {
		maxPoints = 1
	}
	if maxPoints > 600 {
		maxPoints = 600
	}
	where := "container_id IN (" + strings.Join(marks, ",") + ")"
	window, err := r.window(ctx, from, to, maxPoints,
		"SELECT MIN(ts) FROM metric_container_samples WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	result := &ContainerIdentityHistory{Window: window, Series: make([]ContainerIdentitySeries, len(ids))}
	for i, id := range ids {
		result.Series[i] = ContainerIdentitySeries{ContainerID: id, Points: []ContainerIdentityPoint{}}
	}
	if !r.Enabled() {
		return result, nil
	}
	queryArgs := []any{window.StepSeconds, window.StepSeconds}
	queryArgs = append(queryArgs, args...)
	// Samples are whole-second instants. Rounding either boundary down would
	// misclassify a sample when activation evidence contains fractional seconds.
	fromSecond, toSecond := from.Unix(), to.Unix()
	if from.Nanosecond() != 0 {
		fromSecond++
	}
	if to.Nanosecond() != 0 {
		toSecond++
	}
	queryArgs = append(queryArgs, fromSecond, toSecond)
	rows, err := r.db.QueryContext(ctx, `SELECT container_id, (ts / ?) * ? AS bucket,
		COUNT(*), AVG(cpu_percent), MAX(cpu_percent), AVG(mem_percent), MAX(mem_percent),
		AVG(mem_bytes), MAX(mem_bytes), MAX(mem_limit)
		FROM metric_container_samples WHERE `+where+` AND ts >= ? AND ts < ?
		GROUP BY container_id, bucket ORDER BY container_id, bucket`, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var bucket int64
		var point ContainerIdentityPoint
		var bytes, peak, limit float64
		if err := rows.Scan(&id, &bucket, &point.Samples, &point.CPU, &point.CPUPeak,
			&point.Mem, &point.MemPeak, &bytes, &peak, &limit); err != nil {
			return nil, err
		}
		point.TS = time.Unix(bucket, 0).UTC()
		point.CPU, point.CPUPeak = round2(point.CPU), round2(point.CPUPeak)
		point.Mem, point.MemPeak = round1(point.Mem), round1(point.MemPeak)
		point.MemBytes, point.MemBytesPeak, point.MemLimit = uint64(bytes), uint64(peak), uint64(limit)
		index := indexes[id]
		result.Series[index].Points = append(result.Series[index].Points, point)
	}
	return result, rows.Err()
}
