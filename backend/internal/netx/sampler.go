package netx

import (
	"bufio"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The sampler reads every device's counters every two seconds.
//
// Two grains, for two questions. The live figures and the topology's moving
// lines want "now", and two seconds is the step at which a figure reads as
// alive without being noise; that is a fifteen-minute ring in memory. The
// charts want "what happened at three this morning", which only something
// that was running at three can answer; that is a row per device every
// metrics interval in metric_interface_samples, carrying the interval's mean
// and its busiest two seconds — a one-second burst inside a fifteen-second
// mean is otherwise invisible, the same reason the host series keeps peaks.

const (
	liveStep = 2 * time.Second
	liveKeep = 450 // fifteen minutes
)

// Rate is bytes a second in each direction.
type Rate struct {
	Rx float64 `json:"rx"`
	Tx float64 `json:"tx"`
}

// LivePoint is one two-second reading.
type LivePoint struct {
	TS int64   `json:"t"`
	Rx float64 `json:"rx"`
	Tx float64 `json:"tx"`
}

// devCounters is one line of /proc/net/dev.
type devCounters struct {
	rxBytes, rxPackets, rxErrs, rxDrop uint64
	txBytes, txPackets, txErrs, txDrop uint64
}

// Sampler keeps the rings and writes the recorded history.
type Sampler struct {
	db        *sql.DB
	log       *slog.Logger
	every     time.Duration
	retention time.Duration

	mu    sync.RWMutex
	last  map[string]devCounters
	at    time.Time
	rings map[string][]LivePoint
	// pending accumulates the live points since the last recorded row, per
	// device, so the row's peak is the busiest two seconds in its interval.
	pending map[string][]LivePoint
	drops   map[string]devCounters

	stop chan struct{}
	done chan struct{}
}

// procNetDev is read from the namespace the dashboard runs in, which is the
// host's: the backend container shares the host network. A variable for
// tests.
var procNetDev = func() (io.ReadCloser, error) { return os.Open("/proc/net/dev") }

func newSampler(db *sql.DB, log *slog.Logger, every, retention time.Duration) *Sampler {
	if every <= 0 {
		every = 15 * time.Second
	}
	if every < liveStep {
		every = liveStep
	}
	return &Sampler{
		db: db, log: log, every: every, retention: retention,
		last: map[string]devCounters{}, rings: map[string][]LivePoint{},
		pending: map[string][]LivePoint{}, drops: map[string]devCounters{},
	}
}

// Start reads once, so the first rates exist one step later, and then keeps
// sampling until Stop.
func (s *Sampler) Start(parent context.Context) {
	s.mu.Lock()
	if s.stop != nil {
		s.mu.Unlock()
		return
	}
	s.stop, s.done = make(chan struct{}), make(chan struct{})
	s.mu.Unlock()
	s.tick(time.Now())
	go s.loop(parent)
}

// Stop ends sampling and waits for the loop to finish.
func (s *Sampler) Stop() {
	s.mu.Lock()
	stop, done := s.stop, s.done
	s.mu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	<-done
}

func (s *Sampler) loop(ctx context.Context) {
	defer close(s.done)
	live := time.NewTicker(liveStep)
	defer live.Stop()
	record := time.NewTicker(s.every)
	defer record.Stop()
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case now := <-live.C:
			s.tick(now)
		case now := <-record.C:
			s.record(ctx, now)
		case <-prune.C:
			s.prune(ctx)
		}
	}
}

// tick reads the counters and appends a rate per device.
func (s *Sampler) tick(now time.Time) {
	f, err := procNetDev()
	if err != nil {
		return
	}
	cur := parseProcNetDev(f)
	f.Close()
	s.observe(now, cur)
}

// observe is tick's arithmetic, separate so tests feed it counters.
func (s *Sampler) observe(now time.Time, cur map[string]devCounters) {
	s.mu.Lock()
	defer s.mu.Unlock()
	elapsed := now.Sub(s.at).Seconds()
	if !s.at.IsZero() && elapsed > 0 {
		for name, c := range cur {
			prev, ok := s.last[name]
			if !ok {
				continue
			}
			p := LivePoint{
				TS: now.Unix(),
				Rx: rate(prev.rxBytes, c.rxBytes, elapsed),
				Tx: rate(prev.txBytes, c.txBytes, elapsed),
			}
			ring := append(s.rings[name], p)
			if len(ring) > liveKeep {
				ring = ring[len(ring)-liveKeep:]
			}
			s.rings[name] = ring
			s.pending[name] = append(s.pending[name], p)
		}
	}
	// A device that went away takes its ring with it; Docker makes and
	// removes veths all day.
	for name := range s.rings {
		if _, ok := cur[name]; !ok {
			delete(s.rings, name)
			delete(s.pending, name)
		}
	}
	s.last, s.at = cur, now
}

// rate is a counter's change per second. A counter that went backwards was
// reset (the device was recreated); its new value is its whole history and
// would read as a burst, so the step is zero.
func rate(prev, cur uint64, seconds float64) float64 {
	if cur < prev {
		return 0
	}
	return math.Round(float64(cur-prev)/seconds*10) / 10
}

// Rates is every device's most recent rate.
func (s *Sampler) Rates() map[string]Rate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]Rate, len(s.rings))
	for name, ring := range s.rings {
		if n := len(ring); n > 0 {
			out[name] = Rate{Rx: ring[n-1].Rx, Tx: ring[n-1].Tx}
		}
	}
	return out
}

// Live is every device's ring since a moment, so a poll asks only for the
// points it has not drawn.
func (s *Sampler) Live(since int64) map[string][]LivePoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string][]LivePoint, len(s.rings))
	for name, ring := range s.rings {
		i := sort.Search(len(ring), func(i int) bool { return ring[i].TS > since })
		pts := make([]LivePoint, len(ring)-i)
		copy(pts, ring[i:])
		out[name] = pts
	}
	return out
}

// record writes one row per device for the interval just ended.
func (s *Sampler) record(ctx context.Context, now time.Time) {
	if s.db == nil || s.retention <= 0 {
		return
	}
	s.mu.Lock()
	pending := s.pending
	s.pending = map[string][]LivePoint{}
	last := s.last
	prevDrops := s.drops
	s.drops = last
	s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO metric_interface_samples
		(ts, iface, rx_rate, tx_rate, rx_peak, tx_peak, rx_errors, tx_errors, rx_dropped, tx_dropped)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return
	}
	defer stmt.Close()
	ts := now.Unix()
	for name, pts := range pending {
		// Docker's veths come and go with every container; each container's
		// own traffic is recorded with the container, and a row per veth per
		// interval would be most of this table.
		if strings.HasPrefix(name, "veth") || len(pts) == 0 {
			continue
		}
		var rx, tx, rxPeak, txPeak float64
		for _, p := range pts {
			rx += p.Rx
			tx += p.Tx
			rxPeak = math.Max(rxPeak, p.Rx)
			txPeak = math.Max(txPeak, p.Tx)
		}
		n := float64(len(pts))
		cur, prev := last[name], prevDrops[name]
		if _, err := stmt.ExecContext(ctx, ts, name,
			math.Round(rx/n*10)/10, math.Round(tx/n*10)/10, rxPeak, txPeak,
			delta(prev.rxErrs, cur.rxErrs), delta(prev.txErrs, cur.txErrs),
			delta(prev.rxDrop, cur.rxDrop), delta(prev.txDrop, cur.txDrop)); err != nil {
			s.log.Debug("recording interface sample", "iface", name, "err", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		s.log.Debug("recording interface samples", "err", err)
	}
}

func delta(prev, cur uint64) uint64 {
	if cur < prev || prev == 0 {
		return 0
	}
	return cur - prev
}

func (s *Sampler) prune(ctx context.Context) {
	if s.db == nil || s.retention <= 0 {
		return
	}
	cutoff := time.Now().Add(-s.retention).Unix()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM metric_interface_samples WHERE ts < ?`, cutoff); err != nil {
		s.log.Debug("pruning interface samples", "err", err)
	}
}

// HistoryPoint is one bucket of a device's recorded traffic.
type HistoryPoint struct {
	TS     int64   `json:"t"`
	Rx     float64 `json:"rx"`
	Tx     float64 `json:"tx"`
	RxPeak float64 `json:"rxPeak"`
	TxPeak float64 `json:"txPeak"`
	// Errors and Dropped are summed over the bucket, both directions.
	Errors  uint64 `json:"errors"`
	Dropped uint64 `json:"dropped"`
}

// History is every recorded device's traffic over a window, reduced to at
// most maxPoints buckets in SQL.
type History struct {
	From        int64                     `json:"from"`
	To          int64                     `json:"to"`
	StepSeconds int64                     `json:"stepSeconds"`
	Interfaces  map[string][]HistoryPoint `json:"interfaces"`
	// Recording is false where the metrics retention is zero, so nothing is
	// kept and the page says so instead of drawing an empty chart.
	Recording bool `json:"recording"`
}

// History reads the recorded window. iface narrows it to one device.
func (s *Sampler) History(ctx context.Context, window time.Duration, maxPoints int, iface string) (*History, error) {
	to := time.Now()
	from := to.Add(-window)
	if maxPoints < 2 {
		maxPoints = 2
	}
	step := int64(window.Seconds()) / int64(maxPoints)
	if min := int64(s.every.Seconds()); step < min {
		step = min
	}
	if step < 1 {
		step = 1
	}
	h := &History{From: from.Unix(), To: to.Unix(), StepSeconds: step,
		Interfaces: map[string][]HistoryPoint{}, Recording: s.db != nil && s.retention > 0}
	if !h.Recording {
		return h, nil
	}
	query := `SELECT iface, (ts / ?) * ? AS bucket,
		       AVG(rx_rate), AVG(tx_rate), MAX(rx_peak), MAX(tx_peak),
		       SUM(rx_errors + tx_errors), SUM(rx_dropped + tx_dropped)
		  FROM metric_interface_samples
		 WHERE ts >= ? AND ts <= ?`
	args := []any{step, step, from.Unix(), to.Unix()}
	if iface != "" {
		query += ` AND iface = ?`
		args = append(args, iface)
	}
	query += ` GROUP BY iface, bucket ORDER BY iface, bucket`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var p HistoryPoint
		if err := rows.Scan(&name, &p.TS, &p.Rx, &p.Tx, &p.RxPeak, &p.TxPeak, &p.Errors, &p.Dropped); err != nil {
			return nil, err
		}
		p.Rx, p.Tx = math.Round(p.Rx*10)/10, math.Round(p.Tx*10)/10
		h.Interfaces[name] = append(h.Interfaces[name], p)
	}
	return h, rows.Err()
}

// parseProcNetDev reads /proc/net/dev.
func parseProcNetDev(r io.Reader) map[string]devCounters {
	out := map[string]devCounters{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 16 {
			continue
		}
		n := func(i int) uint64 {
			v, _ := strconv.ParseUint(f[i], 10, 64)
			return v
		}
		out[strings.TrimSpace(name)] = devCounters{
			rxBytes: n(0), rxPackets: n(1), rxErrs: n(2), rxDrop: n(3),
			txBytes: n(8), txPackets: n(9), txErrs: n(10), txDrop: n(11),
		}
	}
	return out
}
