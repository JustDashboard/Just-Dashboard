// Package metrics keeps a durable record of host utilisation.
//
// The dashboard's charts were, until this package existed, drawn entirely from
// a WebSocket the browser opened when the Overview page was mounted. That made
// the graph a view of "since you arrived" rather than a view of the server:
// close the tab, and the history went with it; open it ten hours after the
// last visit, and the load spike that woke the box at 04:00 had never been
// recorded by anything.
//
// So the sampler runs in the backend, on its own timer, whether or not a
// browser is connected. The live socket is still what feeds the two-second
// chart — it is far finer-grained than anything worth storing — but it is now
// the tail of a series that starts at boot instead of the whole of it.
package metrics

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
	"github.com/Wayy01/Just-Dashboard/backend/internal/sysinfo"
)

// Bounds on the sampling interval. Below the floor the write rate stops being
// negligible for no visible gain — the live socket already covers fine detail;
// above the ceiling a short spike falls between two samples and the record
// says nothing happened.
const (
	MinInterval = 5 * time.Second
	MaxInterval = 5 * time.Minute

	// DefaultInterval is a compromise between resolution and rows: at 15s a
	// day costs 5,760 rows, so the default week of retention is ~40k rows and
	// a few megabytes of SQLite.
	DefaultInterval = 15 * time.Second

	// DefaultRetention is how far back the charts can look by default.
	DefaultRetention = 7 * 24 * time.Hour

	// MinRetention keeps a misconfigured tiny value from making the feature
	// pointless without disabling it outright, which is what 0 is for.
	MinRetention = time.Hour
)

// pruneEvery is how often expired rows are deleted. Pruning on every sample
// would run a DELETE every 15 seconds to remove, on average, one row.
const pruneEvery = time.Hour

// Sample is one recorded moment. Rates are per second, averaged over the
// interval since the previous sample, exactly as the live socket reports them.
type Sample struct {
	TS          time.Time `json:"ts"`
	CPUPercent  float64   `json:"cpuPercent"`
	Load1       float64   `json:"load1"`
	MemPercent  float64   `json:"memPercent"`
	MemUsed     uint64    `json:"memUsed"`
	MemTotal    uint64    `json:"memTotal"`
	SwapPercent float64   `json:"swapPercent"`
	NetRx       float64   `json:"netRx"`
	NetTx       float64   `json:"netTx"`
	DiskRead    float64   `json:"diskRead"`
	DiskWrite   float64   `json:"diskWrite"`
	DiskPercent float64   `json:"diskPercent"`
	Uptime      uint64    `json:"uptimeSeconds"`

	// Everything below arrived after the first version of this table, and is
	// what separates "the box was busy" from "here is what it was busy with".
	CPUUser     float64 `json:"cpuUser"`
	CPUSystem   float64 `json:"cpuSystem"`
	CPUIOWait   float64 `json:"cpuIowait"`
	CPUSteal    float64 `json:"cpuSteal"`
	PSICPU      float64 `json:"psiCpu"`
	PSIMem      float64 `json:"psiMem"`
	PSIIO       float64 `json:"psiIo"`
	DiskReads   float64 `json:"diskReads"`
	DiskWrites  float64 `json:"diskWrites"`
	DiskAwait   float64 `json:"diskAwait"`
	DiskBusy    float64 `json:"diskBusy"`
	TCPConns    int     `json:"tcpConns"`
	TCPTimeWait int     `json:"tcpTimeWait"`
	Load5       float64 `json:"load5"`
	Load15      float64 `json:"load15"`
	MemAvail    uint64  `json:"memAvailable"`
	Procs       int     `json:"procs"`
}

// Point is one bucket of the aggregated series.
//
// Every value carries its peak as well as its mean, because the mean is what
// hides the thing an operator is looking for: a 100% CPU second inside a
// ten-minute bucket averages down to nothing, and a chart that only draws
// means reports a quiet night. The peaks are what make a downsampled week
// still worth reading.
type Point struct {
	TS      time.Time `json:"ts"`
	Samples int       `json:"samples"`

	CPU     float64 `json:"cpu"`
	CPUPeak float64 `json:"cpuPeak"`

	Mem     float64 `json:"mem"`
	MemPeak float64 `json:"memPeak"`

	Swap     float64 `json:"swap"`
	SwapPeak float64 `json:"swapPeak"`

	RX     float64 `json:"rx"`
	RXPeak float64 `json:"rxPeak"`
	TX     float64 `json:"tx"`
	TXPeak float64 `json:"txPeak"`

	DiskRead      float64 `json:"diskRead"`
	DiskReadPeak  float64 `json:"diskReadPeak"`
	DiskWrite     float64 `json:"diskWrite"`
	DiskWritePeak float64 `json:"diskWritePeak"`

	Load1     float64 `json:"load1"`
	Load1Peak float64 `json:"load1Peak"`

	// DiskPercent is the fullest filesystem at that moment — a summary, kept
	// because it is one cheap number for "how close is this host to full".
	// Which filesystem it was, and what the others were doing, is
	// StorageRange's answer rather than this one's.
	DiskPercent float64 `json:"diskPercent"`
	MemUsed     uint64  `json:"memUsed"`

	// The CPU breakdown, stacked by the chart into one bar of 100%. Only the
	// mean is kept for each: a stack of peaks would sum past 100 and stop
	// being a breakdown of anything.
	CPUUser   float64 `json:"cpuUser"`
	CPUSystem float64 `json:"cpuSystem"`
	CPUIOWait float64 `json:"cpuIowait"`
	CPUSteal  float64 `json:"cpuSteal"`

	// Pressure carries peaks: a ten-second stall inside a ten-minute bucket
	// is exactly the event the mean erases, and it is the event worth seeing.
	PSICPU     float64 `json:"psiCpu"`
	PSICPUPeak float64 `json:"psiCpuPeak"`
	PSIMem     float64 `json:"psiMem"`
	PSIMemPeak float64 `json:"psiMemPeak"`
	PSIIO      float64 `json:"psiIo"`
	PSIIOPeak  float64 `json:"psiIoPeak"`

	DiskReads      float64 `json:"diskReads"`
	DiskReadsPeak  float64 `json:"diskReadsPeak"`
	DiskWrites     float64 `json:"diskWrites"`
	DiskWritesPeak float64 `json:"diskWritesPeak"`
	DiskAwait      float64 `json:"diskAwait"`
	DiskAwaitPeak  float64 `json:"diskAwaitPeak"`
	DiskBusy       float64 `json:"diskBusy"`
	DiskBusyPeak   float64 `json:"diskBusyPeak"`

	TCPConns     float64 `json:"tcpConns"`
	TCPConnsPeak float64 `json:"tcpConnsPeak"`
	TCPTimeWait  float64 `json:"tcpTimeWait"`

	Load5     float64 `json:"load5"`
	Load15    float64 `json:"load15"`
	MemAvail  uint64  `json:"memAvailable"`
	Procs     float64 `json:"procs"`
	ProcsPeak float64 `json:"procsPeak"`
}

// Window is the frame around any recorded series: the facts a client needs to
// draw it honestly. How wide each bucket is, so a gap in the data can be told
// from a flat line; and how far back the record actually goes, so "nothing
// before here" can be labelled as such rather than looking like an idle
// server.
type Window struct {
	From             time.Time  `json:"from"`
	To               time.Time  `json:"to"`
	StepSeconds      int        `json:"stepSeconds"`
	IntervalSeconds  int        `json:"sampleIntervalSeconds"`
	RetentionSeconds int        `json:"retentionSeconds"`
	Earliest         *time.Time `json:"earliest"`
}

// Series is a window of host history.
type Series struct {
	Window
	Points []Point `json:"points"`
}

// ContainerPoint is one bucket of a single container's history. It carries
// peaks for the same reason the host series does: a container that briefly
// pinned a core, or climbed to its memory limit and was killed, leaves nothing
// behind in a mean.
type ContainerPoint struct {
	TS      time.Time `json:"ts"`
	Samples int       `json:"samples"`

	CPU     float64 `json:"cpu"`
	CPUPeak float64 `json:"cpuPeak"`

	Mem     float64 `json:"mem"`
	MemPeak float64 `json:"memPeak"`

	MemBytes     uint64 `json:"memBytes"`
	MemBytesPeak uint64 `json:"memBytesPeak"`
	MemLimit     uint64 `json:"memLimit"`

	// SizeRw is the writable layer at the end of the bucket — the maximum
	// rather than the mean, because what is wanted from it is the shape of the
	// growth and an average across an hour blunts exactly that.
	SizeRw uint64 `json:"sizeRw"`

	PIDs float64 `json:"pids"`

	// Bytes per second, derived in SQL from the cumulative counters Docker
	// reports. They are recorded as the raw totals rather than as rates
	// because a total can be differenced later at any bucket width, whereas a
	// rate recorded against one interval cannot be re-bucketed without
	// pretending it was measured over the wider one.
	NetRx      float64 `json:"netRx"`
	NetTx      float64 `json:"netTx"`
	BlockRead  float64 `json:"blockRead"`
	BlockWrite float64 `json:"blockWrite"`
}

// ContainerSeries is a window of one container's history.
type ContainerSeries struct {
	Window
	Name   string           `json:"name"`
	Points []ContainerPoint `json:"points"`
}

// MountPoint is one bucket of one filesystem's capacity.
type MountPoint struct {
	TS      time.Time `json:"ts"`
	Samples int       `json:"samples"`

	UsedPercent     float64 `json:"usedPercent"`
	UsedPercentPeak float64 `json:"usedPercentPeak"`

	Used  uint64 `json:"used"`
	Total uint64 `json:"total"`

	// InodesPercent fills up independently of the bytes. A build server that
	// writes millions of tiny files hits this ceiling first, and does so on a
	// filesystem the capacity chart shows as half empty.
	InodesPercent float64 `json:"inodesPercent"`
}

// MountSeries is one filesystem's history.
type MountSeries struct {
	Mountpoint string       `json:"mountpoint"`
	Points     []MountPoint `json:"points"`
}

// StorageSeries is every filesystem's history over one window.
//
// Per mount rather than one line for "the disk": which filesystem grew is the
// question an operator is actually asking, and a single worst-of line cannot
// answer it — nor even keep its own meaning, since it silently changes which
// filesystem it is describing.
type StorageSeries struct {
	Window
	Mounts []MountSeries `json:"mounts"`
}

// ContainerSampler is the part of the Docker client the recorder needs. It is
// an interface so a host without a Docker socket is simply a nil sampler
// rather than a special case threaded through the recorder.
type ContainerSampler interface {
	SampleAll(ctx context.Context) ([]dockerx.ContainerStats, error)
}

// Recorder samples the host and answers questions about the past.
type Recorder struct {
	db        *sql.DB
	log       *slog.Logger
	interval  time.Duration
	retention time.Duration

	// The recorder keeps its own collector rather than sharing the one the
	// request handlers use. Rates are deltas against the previous call, so a
	// shared collector would have every one-shot GET /system/metrics silently
	// shorten the interval the next recorded rate is divided by.
	col *sysinfo.Collector

	// containers is nil on a host with no Docker socket, which is the whole
	// of the handling that case needs.
	containers ContainerSampler

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	// Whether the last container sample failed, so a host without Docker says
	// so once instead of every interval for as long as it runs.
	dockerQuiet bool
}

// New builds a recorder. An interval or retention outside the supported band
// is clamped rather than rejected: this is a monitoring nicety, and refusing
// to boot the whole dashboard over it would be out of proportion. A retention
// of zero disables recording entirely, which is the supported way to opt out.
func New(st *store.Store, log *slog.Logger, interval, retention time.Duration) *Recorder {
	if interval <= 0 {
		interval = DefaultInterval
	}
	interval = clamp(interval, MinInterval, MaxInterval)
	if retention > 0 && retention < MinRetention {
		retention = MinRetention
	}
	return &Recorder{
		db:        st.DB,
		log:       log,
		interval:  interval,
		retention: retention,
		col:       sysinfo.NewCollector(),
	}
}

// WithContainers attaches per-container recording. Called after New because
// the Docker client is built alongside the recorder rather than before it.
func (r *Recorder) WithContainers(sampler ContainerSampler) *Recorder {
	r.containers = sampler
	return r
}

// Enabled reports whether history is being recorded at all.
func (r *Recorder) Enabled() bool { return r.retention > 0 }

func (r *Recorder) Interval() time.Duration  { return r.interval }
func (r *Recorder) Retention() time.Duration { return r.retention }

// Start begins sampling. It returns as soon as the loop is running; a failure
// to write one sample is logged and retried on the next tick rather than
// killing the dashboard, since metrics history is not what the process is for.
func (r *Recorder) Start(parent context.Context) error {
	if !r.Enabled() {
		r.log.Info("metrics history disabled", "reason", "JD_METRICS_RETENTION=0")
		return nil
	}
	r.mu.Lock()
	if r.cancel != nil {
		r.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	r.cancel = cancel
	done := make(chan struct{})
	r.done = done
	r.mu.Unlock()

	// Prime the collector so the first stored sample carries real rates
	// instead of the zeroes a first delta always produces.
	if _, err := r.col.Collect(ctx); err != nil {
		r.log.Warn("metrics recorder could not prime counters", "error", err)
	}
	if err := r.prune(ctx); err != nil {
		r.log.Warn("metrics prune failed", "error", err)
	}

	go r.loop(ctx, done)
	r.log.Info("recording metrics history",
		"interval", r.interval.String(), "retention", r.retention.String())
	return nil
}

// Stop ends sampling and waits for the loop to leave, so a shutdown does not
// race a write against the closing database handle.
func (r *Recorder) Stop() {
	r.mu.Lock()
	cancel, done := r.cancel, r.done
	r.cancel, r.done = nil, nil
	r.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (r *Recorder) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	t := time.NewTicker(r.interval)
	defer t.Stop()
	lastPrune := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := r.sample(ctx); err != nil && ctx.Err() == nil {
				r.log.Warn("metrics sample failed", "error", err)
			}
			if time.Since(lastPrune) >= pruneEvery {
				lastPrune = time.Now()
				if err := r.prune(ctx); err != nil && ctx.Err() == nil {
					r.log.Warn("metrics prune failed", "error", err)
				}
			}
		}
	}
}

func (r *Recorder) sample(ctx context.Context) error {
	snap, err := r.col.Collect(ctx)
	if err != nil {
		return err
	}
	if err := r.record(ctx, Reduce(snap)); err != nil {
		return err
	}
	if err := r.recordMounts(ctx, snap.TS, snap.Mounts); err != nil && ctx.Err() == nil {
		r.log.Warn("filesystem metrics write failed", "error", err)
	}
	// Containers are recorded against the host sample's timestamp rather than
	// each container's own read time, so a row of them lines up in a query
	// instead of scattering across a few hundred milliseconds.
	r.recordContainers(ctx, snap.TS)
	return nil
}

// recordMounts stores one row per real filesystem. The snapshot has already
// dropped the pseudo filesystems, which is what keeps this to a handful of
// rows a sample rather than one per cgroup mount.
func (r *Recorder) recordMounts(ctx context.Context, at time.Time, mounts []sysinfo.MountStats) error {
	if len(mounts) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO metric_mount_samples (ts, mountpoint, used_percent, used_bytes, total_bytes, inodes_percent)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(mountpoint, ts) DO UPDATE SET
		  used_percent   = excluded.used_percent,
		  used_bytes     = excluded.used_bytes,
		  total_bytes    = excluded.total_bytes,
		  inodes_percent = excluded.inodes_percent`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	ts := at.Unix()
	for _, m := range mounts {
		if m.Mountpoint == "" {
			continue
		}
		// Inodes are recorded as a percentage rather than a pair of counts:
		// a filesystem that has run out of them reports free space and
		// refuses to create a file, and the only thing an operator needs from
		// the chart is how close to the ceiling it is.
		var inodes float64
		if m.InodesTotal > 0 {
			inodes = float64(m.InodesUsed) / float64(m.InodesTotal) * 100
		}
		if _, err := stmt.ExecContext(ctx, ts, m.Mountpoint, m.UsedPercent,
			int64(m.Used), int64(m.Total), inodes); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// StorageRange returns every filesystem's capacity over a window, bucketed the
// same way the host series is.
//
// One query for all of them rather than one per mount: the number of mounts is
// small but the number of round trips is what costs, and the rows come back
// already grouped by mountpoint.
func (r *Recorder) StorageRange(ctx context.Context, from, to time.Time, maxPoints int) (*StorageSeries, error) {
	if to.Before(from) {
		from, to = to, from
	}
	if maxPoints < 1 {
		maxPoints = 1
	}
	window, err := r.window(ctx, from, to, maxPoints, `SELECT MIN(ts) FROM metric_mount_samples`)
	if err != nil {
		return nil, err
	}
	series := &StorageSeries{Window: window, Mounts: []MountSeries{}}

	secs := int64(window.StepSeconds)
	rows, err := r.db.QueryContext(ctx, `
		SELECT mountpoint,
		       (ts / ?) * ? AS bucket,
		       COUNT(*),
		       AVG(used_percent), MAX(used_percent),
		       AVG(used_bytes),   MAX(total_bytes),
		       MAX(inodes_percent)
		  FROM metric_mount_samples
		 WHERE ts >= ? AND ts <= ?
		 GROUP BY mountpoint, bucket
		 ORDER BY mountpoint, bucket`,
		secs, secs, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var mountpoint string
		var bucket int64
		var p MountPoint
		var used, total float64
		if err := rows.Scan(&mountpoint, &bucket, &p.Samples,
			&p.UsedPercent, &p.UsedPercentPeak, &used, &total,
			&p.InodesPercent); err != nil {
			return nil, err
		}
		p.TS = time.Unix(bucket, 0).UTC()
		p.UsedPercent, p.UsedPercentPeak = round1(p.UsedPercent), round1(p.UsedPercentPeak)
		p.InodesPercent = round1(p.InodesPercent)
		p.Used, p.Total = uint64(used), uint64(total)

		if n := len(series.Mounts); n > 0 && series.Mounts[n-1].Mountpoint == mountpoint {
			series.Mounts[n-1].Points = append(series.Mounts[n-1].Points, p)
			continue
		}
		series.Mounts = append(series.Mounts, MountSeries{Mountpoint: mountpoint, Points: []MountPoint{p}})
	}
	return series, rows.Err()
}

// recordContainers stores one sample per running container.
//
// Failures here are reported but never returned: a host with no Docker socket,
// or one where the daemon is restarting, must not stop the host metrics from
// being recorded. It is also deliberately quiet after the first complaint —
// at one sample every fifteen seconds, a permanent condition logged every time
// is 5,760 identical lines a day.
func (r *Recorder) recordContainers(ctx context.Context, at time.Time) {
	if r.containers == nil {
		return
	}
	stats, err := r.containers.SampleAll(ctx)
	if err != nil {
		if !r.dockerQuiet && ctx.Err() == nil {
			r.dockerQuiet = true
			r.log.Info("not recording container metrics", "error", err)
		}
		return
	}
	r.dockerQuiet = false
	r.recordContainersAt(ctx, at, stats)
}

// recordContainersAt writes one batch at a chosen instant, logging rather than
// returning a failure for the same reason recordContainers does.
func (r *Recorder) recordContainersAt(ctx context.Context, at time.Time, stats []dockerx.ContainerStats) {
	if len(stats) == 0 {
		return
	}
	if err := r.writeContainers(ctx, at, stats); err != nil && ctx.Err() == nil {
		r.log.Warn("container metrics write failed", "error", err)
	}
}

func (r *Recorder) writeContainers(ctx context.Context, at time.Time, stats []dockerx.ContainerStats) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO metric_container_samples
		  (ts, name, cpu_percent, mem_bytes, mem_limit, mem_percent, net_rx, net_tx, pids,
		   block_read, block_write, container_id, size_rw)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(name, ts) DO UPDATE SET
		  cpu_percent = excluded.cpu_percent,
		  mem_bytes   = excluded.mem_bytes,
		  mem_limit   = excluded.mem_limit,
		  mem_percent = excluded.mem_percent,
		  net_rx      = excluded.net_rx,
		  net_tx      = excluded.net_tx,
		  pids        = excluded.pids,
		  block_read  = excluded.block_read,
		  block_write = excluded.block_write,
		  container_id = excluded.container_id,
		  size_rw     = excluded.size_rw`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	ts := at.Unix()
	for _, st := range stats {
		if st.Name == "" {
			continue // nothing to key the series on, and nothing to look it up by
		}
		if _, err := stmt.ExecContext(ctx, ts, st.Name, st.CPUPercent,
			int64(st.MemUsage), int64(st.MemLimit), st.MemPercent,
			int64(st.NetRx), int64(st.NetTx), int64(st.PIDs),
			int64(st.BlockRead), int64(st.BlockWrite), st.ID, st.SizeRw); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Reduce flattens a full snapshot into the handful of series worth keeping for
// months. Per-core percentages, per-interface counters and per-mount inode
// counts are all in the live feed and none of them survive a downsampled
// chart, so storing them would cost rows for a view nothing renders.
func Reduce(snap *sysinfo.Snapshot) Sample {
	s := Sample{
		TS:          snap.TS,
		CPUPercent:  snap.CPU.TotalPercent,
		Load1:       snap.CPU.LoadAvg1,
		MemPercent:  snap.Memory.UsedPercent,
		MemUsed:     snap.Memory.Used,
		MemTotal:    snap.Memory.Total,
		SwapPercent: snap.Swap.UsedPercent,
		Uptime:      snap.Uptime,

		CPUUser:     snap.CPU.Modes.User,
		CPUSystem:   snap.CPU.Modes.System + snap.CPU.Modes.IRQ + snap.CPU.Modes.SoftIRQ,
		CPUIOWait:   snap.CPU.Modes.IOWait,
		CPUSteal:    snap.CPU.Modes.Steal,
		PSICPU:      snap.Pressure.CPUSome,
		PSIMem:      snap.Pressure.MemSome,
		PSIIO:       snap.Pressure.IOSome,
		TCPConns:    snap.Sockets.TCPInUse,
		TCPTimeWait: snap.Sockets.TCPTimeWait,
		Load5:       snap.CPU.LoadAvg5,
		Load15:      snap.CPU.LoadAvg15,
		MemAvail:    snap.Memory.Available,
		Procs:       snap.Procs.Total,
	}
	for _, n := range snap.Net {
		s.NetRx += n.RecvRate
		s.NetTx += n.SendRate
	}
	for _, m := range snap.Mounts {
		s.DiskRead += m.ReadRate
		s.DiskWrite += m.WriteRate
		s.DiskReads += m.ReadOps
		s.DiskWrites += m.WriteOps
		// The worst device rather than the mean of all of them, for the same
		// reason the capacity figure is the fullest mount: one saturated disk
		// is the problem, and averaging it with three idle ones hides it.
		if m.BusyPercent > s.DiskBusy {
			s.DiskBusy = m.BusyPercent
		}
		if await := maxf(m.ReadLatency, m.WriteLatency); await > s.DiskAwait {
			s.DiskAwait = await
		}
		// The fullest mount, not the sum: "is a disk about to fill up" is the
		// question, and averaging a full /boot with an empty /srv answers it
		// wrongly in the reassuring direction.
		if m.UsedPercent > s.DiskPercent {
			s.DiskPercent = m.UsedPercent
		}
	}
	return s
}

// record stores one sample. A second sample landing in the same second (a
// restart, an interval below the resolution of the key) replaces the earlier
// one rather than failing the insert.
func (r *Recorder) record(ctx context.Context, s Sample) error {
	ts := s.TS
	if ts.IsZero() {
		ts = time.Now()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO metric_samples
		  (ts, cpu_percent, load1, mem_percent, mem_used, mem_total, swap_percent,
		   net_rx, net_tx, disk_read, disk_write, disk_percent, uptime_seconds,
		   cpu_user, cpu_system, cpu_iowait, cpu_steal,
		   psi_cpu, psi_mem, psi_io,
		   disk_reads, disk_writes, disk_await, disk_busy,
		   tcp_conns, tcp_timewait, load5, load15, mem_available, procs)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(ts) DO UPDATE SET
		  cpu_percent    = excluded.cpu_percent,
		  load1          = excluded.load1,
		  mem_percent    = excluded.mem_percent,
		  mem_used       = excluded.mem_used,
		  mem_total      = excluded.mem_total,
		  swap_percent   = excluded.swap_percent,
		  net_rx         = excluded.net_rx,
		  net_tx         = excluded.net_tx,
		  disk_read      = excluded.disk_read,
		  disk_write     = excluded.disk_write,
		  disk_percent   = excluded.disk_percent,
		  uptime_seconds = excluded.uptime_seconds,
		  cpu_user       = excluded.cpu_user,
		  cpu_system     = excluded.cpu_system,
		  cpu_iowait     = excluded.cpu_iowait,
		  cpu_steal      = excluded.cpu_steal,
		  psi_cpu        = excluded.psi_cpu,
		  psi_mem        = excluded.psi_mem,
		  psi_io         = excluded.psi_io,
		  disk_reads     = excluded.disk_reads,
		  disk_writes    = excluded.disk_writes,
		  disk_await     = excluded.disk_await,
		  disk_busy      = excluded.disk_busy,
		  tcp_conns      = excluded.tcp_conns,
		  tcp_timewait   = excluded.tcp_timewait,
		  load5          = excluded.load5,
		  load15         = excluded.load15,
		  mem_available  = excluded.mem_available,
		  procs          = excluded.procs`,
		ts.Unix(), s.CPUPercent, s.Load1, s.MemPercent, int64(s.MemUsed), int64(s.MemTotal),
		s.SwapPercent, s.NetRx, s.NetTx, s.DiskRead, s.DiskWrite, s.DiskPercent, int64(s.Uptime),
		s.CPUUser, s.CPUSystem, s.CPUIOWait, s.CPUSteal,
		s.PSICPU, s.PSIMem, s.PSIIO,
		s.DiskReads, s.DiskWrites, s.DiskAwait, s.DiskBusy,
		s.TCPConns, s.TCPTimeWait, s.Load5, s.Load15, int64(s.MemAvail), s.Procs)
	return err
}

func (r *Recorder) prune(ctx context.Context) error {
	if !r.Enabled() {
		return nil
	}
	cutoff := time.Now().Add(-r.retention).Unix()
	if _, err := r.db.ExecContext(ctx, `DELETE FROM metric_samples WHERE ts < ?`, cutoff); err != nil {
		return err
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM metric_container_samples WHERE ts < ?`, cutoff); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM metric_mount_samples WHERE ts < ?`, cutoff)
	return err
}

// Range returns the recorded window [from, to] reduced to at most maxPoints
// buckets.
//
// The reduction happens in SQL rather than by shipping every row to the
// client: a week at 15s is 40,000 samples, and no chart 900 pixels wide has
// anything to do with them.
func (r *Recorder) Range(ctx context.Context, from, to time.Time, maxPoints int) (*Series, error) {
	if to.Before(from) {
		from, to = to, from
	}
	if maxPoints < 1 {
		maxPoints = 1
	}
	window, err := r.window(ctx, from, to, maxPoints,
		`SELECT MIN(ts) FROM metric_samples`)
	if err != nil {
		return nil, err
	}
	series := &Series{Window: window, Points: []Point{}}

	secs := int64(window.StepSeconds)
	rows, err := r.db.QueryContext(ctx, `
		SELECT (ts / ?) * ?                          AS bucket,
		       COUNT(*),
		       AVG(cpu_percent),  MAX(cpu_percent),
		       AVG(mem_percent),  MAX(mem_percent),
		       AVG(swap_percent), MAX(swap_percent),
		       AVG(net_rx),       MAX(net_rx),
		       AVG(net_tx),       MAX(net_tx),
		       AVG(disk_read),    MAX(disk_read),
		       AVG(disk_write),   MAX(disk_write),
		       AVG(load1),        MAX(load1),
		       AVG(disk_percent), AVG(mem_used),
		       AVG(cpu_user),    AVG(cpu_system), AVG(cpu_iowait), AVG(cpu_steal),
		       AVG(psi_cpu),     MAX(psi_cpu),
		       AVG(psi_mem),     MAX(psi_mem),
		       AVG(psi_io),      MAX(psi_io),
		       AVG(disk_reads),  MAX(disk_reads),
		       AVG(disk_writes), MAX(disk_writes),
		       AVG(disk_await),  MAX(disk_await),
		       AVG(disk_busy),   MAX(disk_busy),
		       AVG(tcp_conns),   MAX(tcp_conns), AVG(tcp_timewait),
		       AVG(load5),       AVG(load15), AVG(mem_available),
		       AVG(procs),       MAX(procs)
		  FROM metric_samples
		 WHERE ts >= ? AND ts <= ?
		 GROUP BY bucket
		 ORDER BY bucket`,
		secs, secs, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var bucket int64
		var p Point
		var memUsed, memAvail float64
		if err := rows.Scan(&bucket, &p.Samples,
			&p.CPU, &p.CPUPeak,
			&p.Mem, &p.MemPeak,
			&p.Swap, &p.SwapPeak,
			&p.RX, &p.RXPeak,
			&p.TX, &p.TXPeak,
			&p.DiskRead, &p.DiskReadPeak,
			&p.DiskWrite, &p.DiskWritePeak,
			&p.Load1, &p.Load1Peak,
			&p.DiskPercent, &memUsed,
			&p.CPUUser, &p.CPUSystem, &p.CPUIOWait, &p.CPUSteal,
			&p.PSICPU, &p.PSICPUPeak,
			&p.PSIMem, &p.PSIMemPeak,
			&p.PSIIO, &p.PSIIOPeak,
			&p.DiskReads, &p.DiskReadsPeak,
			&p.DiskWrites, &p.DiskWritesPeak,
			&p.DiskAwait, &p.DiskAwaitPeak,
			&p.DiskBusy, &p.DiskBusyPeak,
			&p.TCPConns, &p.TCPConnsPeak, &p.TCPTimeWait,
			&p.Load5, &p.Load15, &memAvail,
			&p.Procs, &p.ProcsPeak); err != nil {
			return nil, err
		}
		p.TS = time.Unix(bucket, 0).UTC()
		p.MemUsed, p.MemAvail = uint64(memUsed), uint64(memAvail)
		round(&p)
		series.Points = append(series.Points, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return series, nil
}

// window fills in everything about a series except its points. earliestQuery
// answers "how far back does this particular record go", which differs per
// series: a container started an hour ago has an hour of history on a host
// that has a week of its own.
func (r *Recorder) window(ctx context.Context, from, to time.Time, maxPoints int, earliestQuery string, args ...any) (Window, error) {
	step := bucketFor(to.Sub(from), maxPoints, r.interval)
	w := Window{
		From:             from.UTC(),
		To:               to.UTC(),
		StepSeconds:      int(step / time.Second),
		IntervalSeconds:  int(r.interval / time.Second),
		RetentionSeconds: int(r.retention / time.Second),
	}
	var ts sql.NullInt64
	if err := r.db.QueryRowContext(ctx, earliestQuery, args...).Scan(&ts); err != nil {
		return Window{}, err
	}
	if ts.Valid {
		earliest := time.Unix(ts.Int64, 0).UTC()
		w.Earliest = &earliest
	}
	return w, nil
}

// ContainerRange is Range for one container, keyed by name so the series
// survives the container being recreated under a new id.
func (r *Recorder) ContainerRange(ctx context.Context, name string, from, to time.Time, maxPoints int) (*ContainerSeries, error) {
	if to.Before(from) {
		from, to = to, from
	}
	if maxPoints < 1 {
		maxPoints = 1
	}
	window, err := r.window(ctx, from, to, maxPoints,
		`SELECT MIN(ts) FROM metric_container_samples WHERE name = ?`, name)
	if err != nil {
		return nil, err
	}
	series := &ContainerSeries{Window: window, Name: name, Points: []ContainerPoint{}}

	secs := int64(window.StepSeconds)
	rows, err := r.db.QueryContext(ctx, `
		SELECT (ts / ?) * ?                          AS bucket,
		       COUNT(*),
		       AVG(cpu_percent), MAX(cpu_percent),
		       AVG(mem_percent), MAX(mem_percent),
		       AVG(mem_bytes),   MAX(mem_bytes),
		       MAX(mem_limit),
		       AVG(pids),
		       MAX(net_rx) - MIN(net_rx), MAX(net_tx) - MIN(net_tx),
		       MAX(block_read) - MIN(block_read), MAX(block_write) - MIN(block_write),
		       MAX(size_rw)
		  FROM metric_container_samples
		 WHERE name = ? AND ts >= ? AND ts <= ?
		 GROUP BY bucket
		 ORDER BY bucket`,
		secs, secs, name, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var bucket int64
		var p ContainerPoint
		var memBytes, memBytesPeak, memLimit float64
		var rxSpan, txSpan, readSpan, writeSpan float64
		var sizeRw float64
		if err := rows.Scan(&bucket, &p.Samples,
			&p.CPU, &p.CPUPeak,
			&p.Mem, &p.MemPeak,
			&memBytes, &memBytesPeak, &memLimit,
			&p.PIDs,
			&rxSpan, &txSpan, &readSpan, &writeSpan, &sizeRw); err != nil {
			return nil, err
		}
		p.TS = time.Unix(bucket, 0).UTC()
		p.MemBytes, p.MemBytesPeak, p.MemLimit = uint64(memBytes), uint64(memBytesPeak), uint64(memLimit)
		p.SizeRw = uint64(sizeRw)
		p.CPU, p.CPUPeak = round2(p.CPU), round2(p.CPUPeak)
		p.Mem, p.MemPeak = round1(p.Mem), round1(p.MemPeak)
		p.PIDs = round1(p.PIDs)
		// The span within the bucket over the bucket's own width. A bucket
		// holding a single sample has no span and reports zero, which is
		// honest: one reading of a cumulative counter is not a rate.
		p.NetRx, p.NetTx = spanRate(rxSpan, secs), spanRate(txSpan, secs)
		p.BlockRead, p.BlockWrite = spanRate(readSpan, secs), spanRate(writeSpan, secs)
		series.Points = append(series.Points, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return series, nil
}

// Sparkline is one container's recent shape, cheap enough to fetch for every
// container at once.
//
// Two series and nothing else: a table row has space for a trend, not for a
// chart. The point is to answer "which of these forty containers is the one
// that spiked" at a glance, and then to be clicked through to the full series.
type Sparkline struct {
	Name string    `json:"name"`
	CPU  []float64 `json:"cpu"`
	Mem  []float64 `json:"mem"`
	// Peak carries the worst moment in the window, since a sparkline drawn
	// from means at this width would flatten exactly what is being looked for.
	CPUPeak float64 `json:"cpuPeak"`
	MemPeak uint64  `json:"memPeak"`
}

// Sparklines returns a short recent series for every container with history in
// the window.
//
// One query for all of them rather than one request per row. A page listing
// forty containers would otherwise open forty connections to draw forty tiny
// charts, which is how a monitoring feature becomes the load it is monitoring.
func (r *Recorder) Sparklines(ctx context.Context, from, to time.Time, maxPoints int) ([]Sparkline, error) {
	if to.Before(from) {
		from, to = to, from
	}
	if maxPoints < 2 {
		maxPoints = 2
	}
	step := bucketFor(to.Sub(from), maxPoints, r.interval)
	secs := int64(step / time.Second)

	rows, err := r.db.QueryContext(ctx, `
		SELECT name,
		       (ts / ?) * ? AS bucket,
		       MAX(cpu_percent), MAX(mem_bytes)
		  FROM metric_container_samples
		 WHERE ts >= ? AND ts <= ?
		 GROUP BY name, bucket
		 ORDER BY name, bucket`,
		secs, secs, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Sparkline{}
	for rows.Next() {
		var name string
		var bucket int64
		var cpu, mem float64
		if err := rows.Scan(&name, &bucket, &cpu, &mem); err != nil {
			return nil, err
		}
		// Peaks rather than means for the buckets themselves: at twenty pixels
		// wide a sparkline has one job, which is to show that something
		// happened.
		if n := len(out); n == 0 || out[n-1].Name != name {
			out = append(out, Sparkline{Name: name, CPU: []float64{}, Mem: []float64{}})
		}
		s := &out[len(out)-1]
		s.CPU = append(s.CPU, round2(cpu))
		s.Mem = append(s.Mem, mem)
		if cpu > s.CPUPeak {
			s.CPUPeak = round2(cpu)
		}
		if uint64(mem) > s.MemPeak {
			s.MemPeak = uint64(mem)
		}
	}
	return out, rows.Err()
}

// RecordedContainers lists the containers with history in the window, so the
// UI can offer a series for one that is no longer running — which is exactly
// the container an operator is usually looking for.
func (r *Recorder) RecordedContainers(ctx context.Context, from, to time.Time) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT name FROM metric_container_samples
		 WHERE ts >= ? AND ts <= ?
		 GROUP BY name ORDER BY name`, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// bucketFor picks a bucket width that fits the window into maxPoints without
// ever going below the sampling interval — a bucket finer than the data is a
// chart with holes in it that mean nothing.
func bucketFor(window time.Duration, maxPoints int, interval time.Duration) time.Duration {
	if window <= 0 {
		return interval
	}
	step := window / time.Duration(maxPoints)
	if step < interval {
		step = interval
	}
	// Round up to a whole second: the key is unix seconds, so a fractional
	// bucket would silently truncate to something narrower than requested.
	if step%time.Second != 0 {
		step = (step/time.Second + 1) * time.Second
	}
	if step < time.Second {
		step = time.Second
	}
	return step
}

func round(p *Point) {
	p.CPU, p.CPUPeak = round1(p.CPU), round1(p.CPUPeak)
	p.Mem, p.MemPeak = round1(p.Mem), round1(p.MemPeak)
	p.Swap, p.SwapPeak = round1(p.Swap), round1(p.SwapPeak)
	p.RX, p.RXPeak = round1(p.RX), round1(p.RXPeak)
	p.TX, p.TXPeak = round1(p.TX), round1(p.TXPeak)
	p.DiskRead, p.DiskReadPeak = round1(p.DiskRead), round1(p.DiskReadPeak)
	p.DiskWrite, p.DiskWritePeak = round1(p.DiskWrite), round1(p.DiskWritePeak)
	p.Load1, p.Load1Peak = round2(p.Load1), round2(p.Load1Peak)
	p.Load5, p.Load15 = round2(p.Load5), round2(p.Load15)
	p.DiskPercent = round1(p.DiskPercent)
	p.CPUUser, p.CPUSystem = round1(p.CPUUser), round1(p.CPUSystem)
	p.CPUIOWait, p.CPUSteal = round1(p.CPUIOWait), round1(p.CPUSteal)
	p.PSICPU, p.PSICPUPeak = round2(p.PSICPU), round2(p.PSICPUPeak)
	p.PSIMem, p.PSIMemPeak = round2(p.PSIMem), round2(p.PSIMemPeak)
	p.PSIIO, p.PSIIOPeak = round2(p.PSIIO), round2(p.PSIIOPeak)
	p.DiskReads, p.DiskReadsPeak = round1(p.DiskReads), round1(p.DiskReadsPeak)
	p.DiskWrites, p.DiskWritesPeak = round1(p.DiskWrites), round1(p.DiskWritesPeak)
	p.DiskAwait, p.DiskAwaitPeak = round2(p.DiskAwait), round2(p.DiskAwaitPeak)
	p.DiskBusy, p.DiskBusyPeak = round1(p.DiskBusy), round1(p.DiskBusyPeak)
	p.TCPConns, p.TCPConnsPeak = round1(p.TCPConns), round1(p.TCPConnsPeak)
	p.TCPTimeWait = round1(p.TCPTimeWait)
	p.Procs, p.ProcsPeak = round1(p.Procs), round1(p.ProcsPeak)
}

// spanRate turns the growth of a cumulative counter across one bucket into a
// per-second rate.
//
// A negative span means the counter restarted — a container recreated under
// the same name, which is precisely the case this series is keyed by name to
// survive — and is reported as zero rather than as a negative throughput.
func spanRate(span float64, seconds int64) float64 {
	if span <= 0 || seconds <= 0 {
		return 0
	}
	return round1(span / float64(seconds))
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }
func round2(f float64) float64 { return math.Round(f*100) / 100 }

func clamp(v, lo, hi time.Duration) time.Duration {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ParseWindow reads the range shorthand the UI sends. time.ParseDuration knows
// nothing longer than an hour, and a dashboard whose retention is measured in
// days has to be able to say "7d" without spelling it "168h".
func ParseWindow(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	unit := s[len(s)-1]
	var mult time.Duration
	switch unit {
	case 'd':
		mult = 24 * time.Hour
	case 'w':
		mult = 7 * 24 * time.Hour
	default:
		return time.ParseDuration(s)
	}
	// strconv rather than Sscanf: Sscanf stops at the first character it
	// cannot use and reports success, so "7 d" would parse as seven days.
	n, err := strconv.ParseFloat(s[:len(s)-1], 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return time.Duration(n * float64(mult)), nil
}
