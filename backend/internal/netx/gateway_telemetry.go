package netx

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

// Counters that outlive a table replacement, and the Protection page's
// history.
//
// Loading the gateway file replaces the table whole, and a new table starts
// every counter at zero. The recorder keeps, per rule comment, what earlier
// generations counted and the last reading of the current one. A generation
// is the table's kernel handle, which every replacement changes; a counter
// that falls without a new handle (a rule replaced in place) is a reset too.
// What a generation counted between the recorder's last reading and a
// replacement it did not make is not recoverable, and the page says so: the
// dashboard's own replacements are read immediately before they load.

const (
	protectionSampleEvery = time.Minute
	protectionRetention   = 7 * 24 * time.Hour
	// protectionSeriesLimit bounds one history read.
	protectionSeriesLimit = 2000
)

// counterRow is one rule comment's persistent state.
type counterRow struct {
	Packets, Bytes         uint64
	LivePackets, LiveBytes uint64
	Generation             int64
	Since, ObservedAt      time.Time
	Resets                 int
}

// CounterTotal is an entry's count across table generations.
type CounterTotal struct {
	Packets uint64 `json:"packets"`
	Bytes   uint64 `json:"bytes"`
	// Since is when the recorder first saw this rule; earlier traffic is
	// not included.
	Since *time.Time `json:"since"`
	// Resets is how many replacements the total has been carried across.
	Resets int `json:"resets"`
}

// CounterEvidence labels the counters a view shows: which table generation
// the live figures belong to, and how far the totals reach.
type CounterEvidence struct {
	// Generation is the loaded table's kernel handle; zero when not loaded.
	Generation int64 `json:"generation"`
	// ObservedAt is the recorder's last reading; nil before its first.
	ObservedAt *time.Time `json:"observedAt"`
	// Persistent is false where there is no store to keep totals in.
	Persistent bool `json:"persistent"`
	// Gap explains what the totals cannot include.
	Gap string `json:"gap"`
}

const counterGap = "Traffic counted between the last reading and a table replacement made outside the dashboard (a reboot, nft run by hand) is not included."

type gatewayTelemetry struct {
	db  *sql.DB
	log *slog.Logger

	// obs serializes whole readings, so two persists never land out of
	// order; mu guards the rows themselves for the views.
	obs      sync.Mutex
	mu       sync.Mutex
	loaded   bool
	rows     map[string]counterRow
	last     time.Time
	ctPrev   *ctStats
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

func newGatewayTelemetry(db *sql.DB, log *slog.Logger) *gatewayTelemetry {
	return &gatewayTelemetry{db: db, log: log, rows: map[string]counterRow{}}
}

// load reads the persisted rows once.
func (t *gatewayTelemetry) load(ctx context.Context) {
	if t.loaded || t.db == nil {
		t.loaded = true
		return
	}
	t.loaded = true
	rows, err := t.db.QueryContext(ctx, `SELECT key, packets, bytes, live_packets, live_bytes, generation, since, observed_at, resets FROM network_gateway_counters`)
	if err != nil {
		t.log.Warn("reading gateway counter totals", "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var r counterRow
		var since, observed int64
		if rows.Scan(&key, &r.Packets, &r.Bytes, &r.LivePackets, &r.LiveBytes, &r.Generation, &since, &observed, &r.Resets) != nil {
			continue
		}
		r.Since, r.ObservedAt = time.UnixMilli(since).UTC(), time.UnixMilli(observed).UTC()
		t.rows[key] = r
		if r.ObservedAt.After(t.last) {
			t.last = r.ObservedAt
		}
	}
}

// liveCounters is one reading of the loaded table.
type liveCounters struct {
	Loaded     bool
	Generation int64
	Counters   map[string]RuleCounter
	// Rules counts the rules carrying each comment.
	Rules map[string]int
}

// readLiveCounters reads the table's counters, rule counts and handle.
func readLiveCounters(ctx context.Context) liveCounters {
	out, err := run(ctx, "nft", "-t", "-j", "list", "table", "inet", gatewayTable)
	if err != nil {
		return liveCounters{Counters: map[string]RuleCounter{}, Rules: map[string]int{}}
	}
	live, err := parseLiveCounters(out)
	if err != nil {
		return liveCounters{Counters: map[string]RuleCounter{}, Rules: map[string]int{}}
	}
	return live
}

func parseLiveCounters(out string) (liveCounters, error) {
	var listing struct {
		Nftables []struct {
			Table *struct {
				Name   string `json:"name"`
				Handle int64  `json:"handle"`
			} `json:"table"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal([]byte(out), &listing); err != nil {
		return liveCounters{}, err
	}
	counters, err := parseGatewayCounters(out)
	if err != nil {
		return liveCounters{}, err
	}
	live := liveCounters{Loaded: true, Counters: counters, Rules: map[string]int{}}
	for _, o := range listing.Nftables {
		if o.Table != nil && o.Table.Name == gatewayTable {
			live.Generation = o.Table.Handle
		}
	}
	var rules nftRules
	if json.Unmarshal([]byte(out), &rules) == nil {
		for _, o := range rules.Nftables {
			if o.Rule != nil && o.Rule.Comment != "" {
				live.Rules[o.Rule.Comment]++
			}
		}
	}
	return live, nil
}

// fold advances the persistent rows by one reading and returns the deltas
// since the previous one. A rule that vanished, or a whole table that did,
// has its last reading carried into its total.
func (t *gatewayTelemetry) fold(live liveCounters, now time.Time) map[string]RuleCounter {
	deltas := map[string]RuleCounter{}
	for key, cur := range live.Counters {
		r, seen := t.rows[key]
		switch {
		case !seen:
			r = counterRow{Since: now, Generation: live.Generation}
			deltas[key] = cur
		case r.Generation != live.Generation || cur.Packets < r.LivePackets || cur.Bytes < r.LiveBytes:
			r.Packets += r.LivePackets
			r.Bytes += r.LiveBytes
			r.Resets++
			r.Generation = live.Generation
			deltas[key] = cur
		default:
			deltas[key] = RuleCounter{Packets: cur.Packets - r.LivePackets, Bytes: cur.Bytes - r.LiveBytes}
		}
		r.LivePackets, r.LiveBytes, r.ObservedAt = cur.Packets, cur.Bytes, now
		t.rows[key] = r
	}
	for key, r := range t.rows {
		if _, present := live.Counters[key]; present || (r.LivePackets == 0 && r.LiveBytes == 0 && r.Generation == 0) {
			continue
		}
		r.Packets += r.LivePackets
		r.Bytes += r.LiveBytes
		r.LivePackets, r.LiveBytes, r.Generation, r.ObservedAt = 0, 0, 0, now
		t.rows[key] = r
	}
	t.last = now
	return deltas
}

// total is a key's count across generations for a reading the recorder has
// not folded: a newer generation or a fallen counter means the last reading
// of the old one is already part of what was lost or carried.
func (t *gatewayTelemetry) total(key string, live liveCounters) CounterTotal {
	cur := live.Counters[key]
	r, seen := t.rows[key]
	if !seen {
		return CounterTotal{Packets: cur.Packets, Bytes: cur.Bytes}
	}
	total := CounterTotal{Packets: r.Packets, Bytes: r.Bytes, Resets: r.Resets}
	if !r.Since.IsZero() {
		since := r.Since
		total.Since = &since
	}
	if live.Loaded && r.Generation == live.Generation && cur.Packets >= r.LivePackets && cur.Bytes >= r.LiveBytes {
		total.Packets += cur.Packets
		total.Bytes += cur.Bytes
		return total
	}
	total.Packets += r.LivePackets + cur.Packets
	total.Bytes += r.LiveBytes + cur.Bytes
	if live.Loaded && (r.Generation != live.Generation || cur.Packets < r.LivePackets) {
		total.Resets++
	}
	return total
}

// observeCounters folds the table's counters in, for a reload the dashboard
// is about to make: the old generation is read just before it goes.
func (t *gatewayTelemetry) observeCounters(ctx context.Context) {
	t.record(ctx, false)
}

// observe reads the table and conntrack, folds the reading in and records
// the interval's deltas and gauges.
func (t *gatewayTelemetry) observe(ctx context.Context) {
	t.record(ctx, true)
}

func (t *gatewayTelemetry) record(ctx context.Context, withConntrack bool) {
	if t == nil {
		return
	}
	t.obs.Lock()
	defer t.obs.Unlock()
	live := readLiveCounters(ctx)
	now := gatewayNow().UTC()
	t.mu.Lock()
	t.load(ctx)
	deltas := t.fold(live, now)
	rows := make(map[string]counterRow, len(t.rows))
	for k, v := range t.rows {
		rows[k] = v
	}
	t.mu.Unlock()
	samples := map[string][2]uint64{}
	for key, d := range deltas {
		if d.Packets > 0 || d.Bytes > 0 {
			samples[key] = [2]uint64{d.Packets, d.Bytes}
		}
	}
	if !withConntrack {
		t.persist(ctx, rows, samples, now)
		return
	}
	if c := readConntrack(); c.Available {
		samples["conntrack:count"] = [2]uint64{uint64(c.Count), 0}
		samples["conntrack:max"] = [2]uint64{uint64(c.Max), 0}
	}
	if stats, err := conntrackStats(ctx); err == nil {
		t.mu.Lock()
		prev := t.ctPrev
		t.ctPrev = &stats
		t.mu.Unlock()
		if prev != nil {
			for key, pair := range map[string][2]uint64{
				"conntrack:drop":          {stats.Drop, prev.Drop},
				"conntrack:early_drop":    {stats.EarlyDrop, prev.EarlyDrop},
				"conntrack:insert_failed": {stats.InsertFailed, prev.InsertFailed},
				"conntrack:error":         {stats.Error, prev.Error},
			} {
				if pair[0] > pair[1] {
					samples[key] = [2]uint64{pair[0] - pair[1], 0}
				}
			}
		}
	}
	t.persist(ctx, rows, samples, now)
}

func (t *gatewayTelemetry) persist(ctx context.Context, rows map[string]counterRow, samples map[string][2]uint64, now time.Time) {
	if t.db == nil {
		return
	}
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		t.log.Warn("recording gateway counters", "err", err)
		return
	}
	defer tx.Rollback()
	for key, r := range rows {
		if _, err := tx.ExecContext(ctx, `INSERT INTO network_gateway_counters(key, packets, bytes, live_packets, live_bytes, generation, since, observed_at, resets)
			VALUES(?,?,?,?,?,?,?,?,?)
			ON CONFLICT(key) DO UPDATE SET packets=excluded.packets, bytes=excluded.bytes, live_packets=excluded.live_packets,
			live_bytes=excluded.live_bytes, generation=excluded.generation, observed_at=excluded.observed_at, resets=excluded.resets`,
			key, r.Packets, r.Bytes, r.LivePackets, r.LiveBytes, r.Generation, r.Since.UnixMilli(), r.ObservedAt.UnixMilli(), r.Resets); err != nil {
			t.log.Warn("recording gateway counters", "err", err)
			return
		}
	}
	ts := now.Truncate(protectionSampleEvery).Unix()
	for key, v := range samples {
		// Gauges keep the interval's latest reading; deltas add up when the
		// dashboard's own reload reads twice inside one interval.
		upsert := `value = value + excluded.value, bytes = bytes + excluded.bytes`
		if gaugeKey(key) {
			upsert = `value = excluded.value, bytes = excluded.bytes`
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO network_protection_samples(ts, key, value, bytes) VALUES(?,?,?,?)
			ON CONFLICT(ts, key) DO UPDATE SET `+upsert, ts, key, v[0], v[1]); err != nil {
			t.log.Warn("recording protection samples", "err", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		t.log.Warn("recording gateway counters", "err", err)
	}
}

func gaugeKey(key string) bool { return key == "conntrack:count" || key == "conntrack:max" }

func (t *gatewayTelemetry) prune(ctx context.Context) {
	if t.db == nil {
		return
	}
	cutoff := gatewayNow().Add(-protectionRetention).Unix()
	if _, err := t.db.ExecContext(ctx, `DELETE FROM network_protection_samples WHERE ts < ?`, cutoff); err != nil {
		t.log.Warn("pruning protection samples", "err", err)
	}
}

// start records every protectionSampleEvery until stop.
func (t *gatewayTelemetry) start(parent context.Context) {
	t.mu.Lock()
	if t.stop != nil {
		t.mu.Unlock()
		return
	}
	t.stop, t.done = make(chan struct{}), make(chan struct{})
	t.mu.Unlock()
	go func() {
		defer close(t.done)
		tick := time.NewTicker(protectionSampleEvery)
		defer tick.Stop()
		prune := time.NewTicker(time.Hour)
		defer prune.Stop()
		first := time.NewTimer(5 * time.Second)
		defer first.Stop()
		for {
			select {
			case <-parent.Done():
				return
			case <-t.stop:
				return
			case <-first.C:
				t.observe(parent)
			case <-tick.C:
				t.observe(parent)
			case <-prune.C:
				t.prune(parent)
			}
		}
	}()
}

func (t *gatewayTelemetry) halt() {
	t.mu.Lock()
	stop, done := t.stop, t.done
	t.mu.Unlock()
	if stop == nil {
		return
	}
	t.stopOnce.Do(func() { close(stop) })
	<-done
}

// evidence labels a reading.
func (t *gatewayTelemetry) evidence(live liveCounters) CounterEvidence {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := CounterEvidence{Generation: live.Generation, Persistent: t.db != nil, Gap: counterGap}
	if !t.last.IsZero() {
		last := t.last
		e.ObservedAt = &last
	}
	return e
}

// totals reads the totals of the given keys against one live reading.
func (t *gatewayTelemetry) totals(ctx context.Context, live liveCounters, keys []string) map[string]CounterTotal {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.load(ctx)
	out := make(map[string]CounterTotal, len(keys))
	for _, k := range keys {
		out[k] = t.total(k, live)
	}
	return out
}

// SeriesPoint is one recorded interval of one key.
type SeriesPoint struct {
	TS    int64  `json:"t"`
	Value uint64 `json:"value"`
	Bytes uint64 `json:"bytes,omitempty"`
}

// series reads recorded samples for the keys since a time, oldest first.
func (t *gatewayTelemetry) series(ctx context.Context, keys []string, since time.Time) (map[string][]SeriesPoint, error) {
	out := map[string][]SeriesPoint{}
	if t == nil || t.db == nil || len(keys) == 0 {
		return out, nil
	}
	sort.Strings(keys)
	holders := strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")
	args := []any{since.Unix()}
	for _, k := range keys {
		args = append(args, k)
	}
	args = append(args, protectionSeriesLimit*len(keys))
	rows, err := t.db.QueryContext(ctx, fmt.Sprintf(`SELECT ts, key, value, bytes FROM network_protection_samples WHERE ts >= ? AND key IN (%s) ORDER BY ts LIMIT ?`, holders), args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var p SeriesPoint
		var key string
		if err := rows.Scan(&p.TS, &key, &p.Value, &p.Bytes); err != nil {
			return out, err
		}
		out[key] = append(out[key], p)
	}
	return out, rows.Err()
}
