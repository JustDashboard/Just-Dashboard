package metrics

import (
	"context"
	"time"
)

// ContainerRange preserves name continuity across recreates, but rates never
// cross an identity change, counter reset or a gap longer than three samples.
func (r *Recorder) ContainerRange(ctx context.Context, name string, from, to time.Time, maxPoints int) (*ContainerSeries, error) {
	if to.Before(from) {
		from, to = to, from
	}
	window, err := r.window(ctx, from, to, max(maxPoints, 1),
		`SELECT MIN(ts) FROM metric_container_samples WHERE name = ?`, name)
	if err != nil {
		return nil, err
	}
	series := &ContainerSeries{Window: window, Name: name, Points: []ContainerPoint{}}
	gap := int64(3 * r.interval / time.Second)
	// Difference BEFORE bucketing. MAX(counter)-MIN(counter) inside a bucket
	// loses every boundary interval, turns single-sample buckets into zero,
	// and treats a counter reset as a spike. The bounded look-behind includes
	// the predecessor of the first visible sample without scanning all history.
	rows, err := r.db.QueryContext(ctx, `
		WITH previous AS (
		  SELECT *, COALESCE(sample_time, ts) - LAG(COALESCE(sample_time, ts)) OVER w AS elapsed,
		    LAG(container_id) OVER w AS prev_id, LAG(cpu_total) OVER w AS prev_cpu,
		    LAG(net_rx) OVER w AS prev_rx, LAG(net_tx) OVER w AS prev_tx,
		    LAG(block_read) OVER w AS prev_read, LAG(block_write) OVER w AS prev_write,
		    LAG(COALESCE(network_available, net_rx > 0 OR net_tx > 0)) OVER w AS prev_net,
		    LAG(COALESCE(block_available, block_read > 0 OR block_write > 0)) OVER w AS prev_block
		  FROM metric_container_samples WHERE name = ? AND ts >= ? AND ts <= ?
		  WINDOW w AS (ORDER BY ts)
		), deltas AS (
		  SELECT *,
		    CASE WHEN COALESCE(network_available, net_rx > 0 OR net_tx > 0) AND prev_net
		      AND net_rx >= prev_rx THEN net_rx - prev_rx END AS rx,
		    CASE WHEN COALESCE(network_available, net_rx > 0 OR net_tx > 0) AND prev_net
		      AND net_tx >= prev_tx THEN net_tx - prev_tx END AS tx,
		    CASE WHEN COALESCE(block_available, block_read > 0 OR block_write > 0) AND prev_block
		      AND block_read >= prev_read THEN block_read - prev_read END AS rd,
		    CASE WHEN COALESCE(block_available, block_read > 0 OR block_write > 0) AND prev_block
		      AND block_write >= prev_write THEN block_write - prev_write END AS wr,
		    CASE WHEN elapsed > 0 AND elapsed <= ? AND container_id = prev_id
		      AND (cpu_total IS NULL OR prev_cpu IS NULL OR cpu_total >= prev_cpu)
		      THEN elapsed END AS dt
		  FROM previous WHERE ts >= ?
		)
		SELECT (ts / ?) * ? AS bucket, COUNT(*),
		  AVG(cpu_percent), MAX(cpu_percent), AVG(mem_percent), MAX(mem_percent),
		  AVG(mem_bytes), MAX(mem_bytes), MAX(CASE WHEN mem_limited = 1 THEN mem_limit ELSE 0 END),
		  AVG(pids), MAX(size_rw),
		  1.0 * SUM(CASE WHEN dt IS NOT NULL THEN rx END) / SUM(CASE WHEN rx IS NOT NULL THEN dt END),
		  1.0 * SUM(CASE WHEN dt IS NOT NULL THEN tx END) / SUM(CASE WHEN tx IS NOT NULL THEN dt END),
		  1.0 * SUM(CASE WHEN dt IS NOT NULL THEN rd END) / SUM(CASE WHEN rd IS NOT NULL THEN dt END),
		  1.0 * SUM(CASE WHEN dt IS NOT NULL THEN wr END) / SUM(CASE WHEN wr IS NOT NULL THEN dt END),
		  MAX(1.0 * rx / dt), MAX(1.0 * tx / dt), MAX(1.0 * rd / dt), MAX(1.0 * wr / dt)
		FROM deltas GROUP BY bucket ORDER BY bucket`,
		name, from.Unix()-gap, to.Unix(), gap, from.Unix(), window.StepSeconds, window.StepSeconds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var bucket int64
		var p ContainerPoint
		var memBytes, memPeak, memLimit, sizeRw float64
		if err := rows.Scan(&bucket, &p.Samples, &p.CPU, &p.CPUPeak, &p.Mem, &p.MemPeak,
			&memBytes, &memPeak, &memLimit, &p.PIDs, &sizeRw,
			&p.NetRx, &p.NetTx, &p.BlockRead, &p.BlockWrite,
			&p.NetRxPeak, &p.NetTxPeak, &p.BlockReadPeak, &p.BlockWritePeak); err != nil {
			return nil, err
		}
		p.TS = time.Unix(bucket, 0).UTC()
		p.MemBytes, p.MemBytesPeak, p.MemLimit = uint64(memBytes), uint64(memPeak), uint64(memLimit)
		p.SizeRw = uint64(sizeRw)
		p.CPU, p.CPUPeak = round2(p.CPU), round2(p.CPUPeak)
		p.Mem, p.MemPeak, p.PIDs = round1(p.Mem), round1(p.MemPeak), round1(p.PIDs)
		series.Points = append(series.Points, p)
	}
	return series, rows.Err()
}
