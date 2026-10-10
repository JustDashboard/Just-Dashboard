package netx

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/netip"
	"sort"
	"sync"
	"time"
)

// The WireGuard record: what the kernel's counters, handshakes and endpoints
// were over time, what was done to each tunnel and what came of it, and the
// usage budgets an administrator set. `wg show` answers only "now", so a
// trend, the moment a site went quiet and the addresses a phone dialled from
// exist only if something wrote them down while nobody was looking. Rows hold
// a peer's public key at most; private and preshared keys never reach them.

const (
	// wgSampleEvery is the grain of the record: fine enough to see an evening
	// of traffic, coarse enough that a week of fifty peers stays small.
	wgSampleEvery = 5 * time.Minute
	// wgSampleRetention bounds the per-sample rows; daily usage outlives them.
	wgSampleRetention = 7 * 24 * time.Hour
	wgUsageDays       = 400
	// wgEndpointsPerPeer keeps the addresses a peer was most recently seen
	// from; a phone that roams all day would otherwise grow without bound.
	wgEndpointsPerPeer = 20
	wgEventsPerTunnel  = 500
	wgEventRetention   = 365 * 24 * time.Hour
	// wgTransportChecks bounds the endpoint routes read per pass and per
	// guarded change. A host with more roaming peers keeps the rest unchecked
	// rather than paying a route lookup per peer on every change.
	wgTransportChecks = 32
	wgEventDetailMax  = 400
)

// Lifecycle outcomes. Failed means the operation did not happen and the host
// was put back; degraded means something was left that needs attention.
const (
	wgOutcomeOK        = "ok"
	wgOutcomeFailed    = "failed"
	wgOutcomeDegraded  = "degraded"
	wgOutcomeRecovered = "recovered"
)

// WGEvent is one entry in a tunnel's lifecycle history.
type WGEvent struct {
	ID       int64  `json:"id"`
	Iface    string `json:"iface"`
	At       int64  `json:"at"`
	Kind     string `json:"kind"`
	Outcome  string `json:"outcome"`
	Peer     string `json:"peer,omitempty"`
	PeerName string `json:"peerName,omitempty"`
	Actor    string `json:"actor,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// WGTransport is where the kernel sends a peer's encrypted packets. A route
// into a WireGuard device is a loop: the tunnel would carry its own carrier.
type WGTransport struct {
	Endpoint string `json:"endpoint"`
	// State is native (leaves through a non-tunnel device), captured (routed
	// into a WireGuard device), unroutable (no route) or unknown.
	State     string `json:"state"`
	Device    string `json:"device,omitempty"`
	Reason    string `json:"reason,omitempty"`
	CheckedAt int64  `json:"checkedAt"`
}

type wgPeerRef struct{ iface, key string }

// wgLastSample is the previous observation of a peer, for deltas and for
// noticing the moment a handshake went stale.
type wgLastSample struct {
	ts, handshake int64
	rx, tx        uint64
}

type wgRecord struct {
	db        *sql.DB
	log       *slog.Logger
	retention time.Duration

	mu         sync.Mutex
	last       map[wgPeerRef]wgLastSample
	transports map[wgPeerRef]WGTransport
	stop, done chan struct{}
	stopOnce   sync.Once
}

// newWGRecord keeps samples no longer than the metrics recorder keeps its own
// (a retention of zero records no traffic history at all), and never longer
// than a week. Lifecycle events are kept either way: they are what was done,
// not a measurement.
func newWGRecord(db *sql.DB, log *slog.Logger, retention time.Duration) *wgRecord {
	if retention > wgSampleRetention {
		retention = wgSampleRetention
	}
	return &wgRecord{
		db: db, log: log, retention: retention,
		last: map[wgPeerRef]wgLastSample{}, transports: map[wgPeerRef]WGTransport{},
	}
}

// note writes a lifecycle event. The host change it describes already
// happened, so a canceled request does not drop it and a failed write is
// logged rather than turned into a failure of the change.
func (r *wgRecord) note(ctx context.Context, e WGEvent) {
	if e.At == 0 {
		e.At = wgNow().Unix()
	}
	if e.Outcome == "" {
		e.Outcome = wgOutcomeOK
	}
	if len(e.Detail) > wgEventDetailMax {
		e.Detail = e.Detail[:wgEventDetailMax]
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO network_wg_events (iface, ts, kind, outcome, peer_key, peer_name, actor, detail) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Iface, e.At, e.Kind, e.Outcome, e.Peer, e.PeerName, e.Actor, e.Detail); err != nil {
		r.log.Warn("the WireGuard lifecycle record could not be written", "iface", e.Iface, "kind", e.Kind, "err", err)
		return
	}
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM network_wg_events WHERE iface = ? AND id NOT IN
		   (SELECT id FROM network_wg_events WHERE iface = ? ORDER BY id DESC LIMIT ?)`,
		e.Iface, e.Iface, wgEventsPerTunnel); err != nil {
		r.log.Warn("old WireGuard lifecycle records could not be trimmed", "iface", e.Iface, "err", err)
	}
}

// events is a tunnel's history, newest first, optionally one peer's.
func (r *wgRecord) events(ctx context.Context, iface, peer string, limit int) ([]WGEvent, error) {
	q := `SELECT id, iface, ts, kind, outcome, peer_key, peer_name, actor, detail FROM network_wg_events WHERE iface = ?`
	args := []any{iface}
	if peer != "" {
		q += ` AND peer_key = ?`
		args = append(args, peer)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("reading the tunnel's history: %w", err)
	}
	defer rows.Close()
	out := []WGEvent{}
	for rows.Next() {
		var e WGEvent
		if err := rows.Scan(&e.ID, &e.Iface, &e.At, &e.Kind, &e.Outcome, &e.Peer, &e.PeerName, &e.Actor, &e.Detail); err != nil {
			return nil, fmt.Errorf("reading the tunnel's history: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// start takes the first reading at once and then one every wgSampleEvery.
func (r *wgRecord) start(ctx context.Context) {
	r.mu.Lock()
	if r.stop != nil {
		r.mu.Unlock()
		return
	}
	r.stop, r.done = make(chan struct{}), make(chan struct{})
	r.mu.Unlock()
	go r.loop(ctx)
}

func (r *wgRecord) stopLoop() {
	r.mu.Lock()
	stop, done := r.stop, r.done
	r.mu.Unlock()
	if stop == nil {
		return
	}
	r.stopOnce.Do(func() { close(stop) })
	<-done
}

func (r *wgRecord) loop(ctx context.Context) {
	defer close(r.done)
	r.tick(ctx, wgNow())
	t := time.NewTicker(wgSampleEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stop:
			return
		case <-t.C:
			r.tick(ctx, wgNow())
		}
	}
}

// tick reads every running tunnel once. A host without WireGuard, or a dump
// that cannot be read, records nothing rather than a run of zeros.
func (r *wgRecord) tick(ctx context.Context, now time.Time) {
	if !has("wg") {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	out, err := run(ctx, "wg", "show", "all", "dump")
	if err != nil {
		r.log.Debug("WireGuard could not be read for its record", "err", err)
		return
	}
	live, err := wgCheckedDump(out)
	if err != nil {
		r.log.Warn("WireGuard printed an unreadable dump; nothing was recorded", "err", err)
		return
	}
	if err := r.observe(ctx, now, live); err != nil {
		r.log.Warn("the WireGuard record could not be written", "err", err)
	}
	r.checkTransports(ctx, now, live)
	if err := r.prune(ctx, now); err != nil {
		r.log.Warn("the WireGuard record could not be pruned", "err", err)
	}
}

// observe writes one reading of every peer: its counters as deltas since the
// last reading, the day's usage, the endpoint it was seen from, and a
// lifecycle event when an always-on peer's handshake goes stale or comes back.
// A counter that went down is an interface that restarted; its new value is
// what moved since. A peer's first reading has no baseline and records none.
func (r *wgRecord) observe(ctx context.Context, now time.Time, live map[string]*wgLiveIface) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // a committed transaction makes this a no-op
	type transition struct {
		ref    wgPeerRef
		kind   string
		detail string
	}
	var changes []transition
	day := now.UTC().Unix() / 86400
	names := make([]string, 0, len(live))
	for name := range live {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, p := range live[name].peers {
			ref := wgPeerRef{name, p.publicKey}
			prev, ok := r.previous(ctx, tx, ref)
			var rxd, txd uint64
			var span int64
			if ok && now.Unix() > prev.ts {
				rxd, txd, span = wgCounterDelta(prev.rx, p.rx), wgCounterDelta(prev.tx, p.tx), now.Unix()-prev.ts
			}
			expects := p.keepalive > 0
			if ok && expects {
				before := wgHandshakeState(prev.handshake, prev.ts, expects)
				after := wgHandshakeState(p.handshake, now.Unix(), expects)
				switch {
				case before == "online" && after == "stale":
					changes = append(changes, transition{ref, "handshake_stale", "no handshake since " + time.Unix(p.handshake, 0).UTC().Format(time.RFC3339)})
				case before == "stale" && after == "online":
					changes = append(changes, transition{ref, "handshake_recovered", "handshake at " + time.Unix(p.handshake, 0).UTC().Format(time.RFC3339)})
				}
			}
			r.mu.Lock()
			r.last[ref] = wgLastSample{ts: now.Unix(), handshake: p.handshake, rx: p.rx, tx: p.tx}
			r.mu.Unlock()
			if r.retention <= 0 {
				continue
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT OR REPLACE INTO network_wg_samples (iface, public_key, ts, rx, tx, rx_delta, tx_delta, span, handshake) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				name, p.publicKey, now.Unix(), int64(p.rx), int64(p.tx), int64(rxd), int64(txd), span, p.handshake); err != nil {
				return err
			}
			if rxd+txd > 0 {
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO network_wg_usage (iface, public_key, day, rx, tx) VALUES (?, ?, ?, ?, ?)
					 ON CONFLICT(iface, public_key, day) DO UPDATE SET rx = rx + excluded.rx, tx = tx + excluded.tx`,
					name, p.publicKey, day, int64(rxd), int64(txd)); err != nil {
					return err
				}
			}
			if p.endpoint != "" {
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO network_wg_endpoints (iface, public_key, endpoint, first_seen, last_seen, observations) VALUES (?, ?, ?, ?, ?, 1)
					 ON CONFLICT(iface, public_key, endpoint) DO UPDATE SET last_seen = excluded.last_seen, observations = observations + 1`,
					name, p.publicKey, p.endpoint, now.Unix(), now.Unix()); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx,
					`DELETE FROM network_wg_endpoints WHERE iface = ? AND public_key = ? AND endpoint NOT IN
					   (SELECT endpoint FROM network_wg_endpoints WHERE iface = ? AND public_key = ? ORDER BY last_seen DESC LIMIT ?)`,
					name, p.publicKey, name, p.publicKey, wgEndpointsPerPeer); err != nil {
					return err
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, c := range changes {
		outcome := wgOutcomeDegraded
		if c.kind == "handshake_recovered" {
			outcome = wgOutcomeRecovered
		}
		r.note(ctx, WGEvent{Iface: c.ref.iface, At: now.Unix(), Kind: c.kind, Outcome: outcome, Peer: c.ref.key, Detail: c.detail})
	}
	return nil
}

// previous is the last reading of a peer: this process's own, or, after a
// restart, the newest recorded sample, so a dashboard upgrade does not drop
// the traffic that moved while it restarted from the day's usage.
func (r *wgRecord) previous(ctx context.Context, q *sql.Tx, ref wgPeerRef) (wgLastSample, bool) {
	r.mu.Lock()
	prev, ok := r.last[ref]
	r.mu.Unlock()
	if ok || r.retention <= 0 {
		return prev, ok
	}
	var rx, tx int64
	err := q.QueryRowContext(ctx,
		`SELECT ts, rx, tx, handshake FROM network_wg_samples WHERE iface = ? AND public_key = ? ORDER BY ts DESC LIMIT 1`,
		ref.iface, ref.key).Scan(&prev.ts, &rx, &tx, &prev.handshake)
	if err != nil {
		return wgLastSample{}, false
	}
	prev.rx, prev.tx = uint64(rx), uint64(tx)
	return prev, true
}

func wgCounterDelta(prev, cur uint64) uint64 {
	if cur >= prev {
		return cur - prev
	}
	return cur
}

// wgHandshakeState names a peer's handshake at a moment. A peer that keeps
// its session alive (a persistent keepalive on this side) renews it about
// every two minutes, so one quiet past wgOnlineWithin is stale; one that does
// not is a phone with its tunnel switched off, which is idle, not a fault.
func wgHandshakeState(handshake, at int64, expects bool) string {
	switch {
	case handshake == 0:
		return "never"
	case at-handshake <= wgOnlineWithin:
		return "online"
	case expects:
		return "stale"
	default:
		return "idle"
	}
}

func (r *wgRecord) prune(ctx context.Context, now time.Time) error {
	if r.retention > 0 {
		if _, err := r.db.ExecContext(ctx, `DELETE FROM network_wg_samples WHERE ts < ?`, now.Add(-r.retention).Unix()); err != nil {
			return err
		}
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM network_wg_usage WHERE day < ?`, now.UTC().Unix()/86400-wgUsageDays); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM network_wg_events WHERE ts < ?`, now.Add(-wgEventRetention).Unix())
	return err
}

// checkTransports asks the kernel where each peer's encrypted packets go, the
// way WireGuard's own socket asks: with the interface's fwmark when it has
// one. It runs on the record's clock, so a route added outside the dashboard
// that captures a roaming peer's transport is noticed within one interval.
func (r *wgRecord) checkTransports(ctx context.Context, now time.Time, live map[string]*wgLiveIface) {
	checked := map[wgPeerRef]WGTransport{}
	for _, t := range wgTransportTargets(live) {
		state := wgTransportOf(ctx, t, live)
		state.CheckedAt = now.Unix()
		checked[t.ref] = state
	}
	r.mu.Lock()
	before := r.transports
	r.transports = checked
	r.mu.Unlock()
	for ref, state := range checked {
		prior, seen := before[ref]
		switch {
		case state.State == "captured" && (!seen || prior.State != "captured"):
			r.note(ctx, WGEvent{Iface: ref.iface, At: now.Unix(), Kind: "transport_captured", Outcome: wgOutcomeDegraded, Peer: ref.key, Detail: state.Reason})
		case seen && prior.State == "captured" && state.State == "native":
			r.note(ctx, WGEvent{Iface: ref.iface, At: now.Unix(), Kind: "transport_restored", Outcome: wgOutcomeRecovered, Peer: ref.key, Detail: "leaves through " + state.Device})
		}
	}
}

// transport is the last check of one peer's transport, if there was one.
func (r *wgRecord) transport(iface, key string) (WGTransport, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.transports[wgPeerRef{iface, key}]
	return t, ok
}

type wgTransportTarget struct {
	ref      wgPeerRef
	endpoint string
	addr     netip.Addr
	fwmark   string
}

// wgTransportTargets are the peers with an endpoint, in a stable order and
// bounded, each with the route question its packets would ask.
func wgTransportTargets(live map[string]*wgLiveIface) []wgTransportTarget {
	var out []wgTransportTarget
	for name, ifc := range live {
		for _, p := range ifc.peers {
			if p.endpoint == "" {
				continue
			}
			a := wgEndpointAddress(p.endpoint)
			if !a.IsValid() {
				continue
			}
			out = append(out, wgTransportTarget{ref: wgPeerRef{name, p.publicKey}, endpoint: p.endpoint, addr: a, fwmark: ifc.fwmark})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ref.iface != out[j].ref.iface {
			return out[i].ref.iface < out[j].ref.iface
		}
		return out[i].ref.key < out[j].ref.key
	})
	if len(out) > wgTransportChecks {
		out = out[:wgTransportChecks]
	}
	return out
}

// wgTransportArgs is the `ip route get` a peer's encrypted packets ask.
func wgTransportArgs(t wgTransportTarget) []string {
	args := []string{"-j"}
	if t.addr.Is6() {
		args = append(args, "-6")
	}
	args = append(args, "route", "get", t.addr.String())
	if t.fwmark != "" {
		args = append(args, "mark", t.fwmark)
	}
	return args
}

func wgTransportOf(ctx context.Context, t wgTransportTarget, live map[string]*wgLiveIface) WGTransport {
	out, err := run(ctx, "ip", wgTransportArgs(t)...)
	state := WGTransport{Endpoint: t.endpoint}
	if err != nil {
		state.State, state.Reason = "unroutable", "the kernel has no route to "+t.addr.String()
		return state
	}
	p, err := parseRouteGet(out, Path{Address: t.addr.String()})
	if err != nil {
		state.State, state.Reason = "unknown", err.Error()
		return state
	}
	state.Device = p.Device
	if _, tunnel := live[p.Device]; tunnel {
		state.State = "captured"
		state.Reason = fmt.Sprintf("the route to %s goes into the WireGuard device %s, so the tunnel would carry its own transport", t.addr, p.Device)
		return state
	}
	state.State = "native"
	return state
}

// WGTrendPoint is one bucket of a trend: mean bytes per second each way.
type WGTrendPoint struct {
	T  int64   `json:"t"`
	Rx float64 `json:"rx"`
	Tx float64 `json:"tx"`
}

// WGEndpointSeen is one address a peer was seen dialling from.
type WGEndpointSeen struct {
	Endpoint     string `json:"endpoint"`
	FirstSeen    int64  `json:"firstSeen"`
	LastSeen     int64  `json:"lastSeen"`
	Observations int    `json:"observations"`
}

// WGHistory is a tunnel's, or one peer's, record over a window.
type WGHistory struct {
	Iface string `json:"iface"`
	Peer  string `json:"peer,omitempty"`
	// Recording is false when the metrics retention keeps no history, so an
	// empty trend is not mistaken for an idle tunnel.
	Recording bool             `json:"recording"`
	Window    int64            `json:"window"`
	Bucket    int64            `json:"bucket"`
	Points    []WGTrendPoint   `json:"points"`
	Endpoints []WGEndpointSeen `json:"endpoints"`
	Events    []WGEvent        `json:"events"`
	// Usage is the peer's recorded bytes per UTC day, newest last.
	Usage []WGUsageDay `json:"usage"`
}

// WGUsageDay is one UTC day of a peer's recorded traffic.
type WGUsageDay struct {
	Day int64  `json:"day"`
	Rx  uint64 `json:"rx"`
	Tx  uint64 `json:"tx"`
}

// wgHistoryWindows are the windows a trend is read over, and the bucket each
// is drawn in: the sample grain for a day, coarser for a week.
var wgHistoryWindows = map[string][2]time.Duration{
	"6h":  {6 * time.Hour, wgSampleEvery},
	"24h": {24 * time.Hour, wgSampleEvery},
	"7d":  {7 * 24 * time.Hour, time.Hour},
}

// WireGuardHistory reads the record of a tunnel, or of one of its peers named
// by public key, over a window. It never touches the host.
func (s *Service) WireGuardHistory(ctx context.Context, iface, peer, window string) (*WGHistory, error) {
	if err := validWGName(iface); err != nil {
		return nil, err
	}
	w, ok := wgHistoryWindows[window]
	if !ok {
		return nil, fmt.Errorf("the window is one of 6h, 24h or 7d")
	}
	if peer != "" {
		if err := wgValidPublicKey(peer); err != nil {
			return nil, err
		}
	}
	span, bucket := int64(w[0].Seconds()), int64(w[1].Seconds())
	now := wgNow().Unix()
	from := now - span
	h := &WGHistory{
		Iface: iface, Peer: peer, Recording: s.wg.retention > 0, Window: span, Bucket: bucket,
		Points: []WGTrendPoint{}, Endpoints: []WGEndpointSeen{}, Usage: []WGUsageDay{},
	}
	q := `SELECT (ts / ?) * ?, sum(rx_delta), sum(tx_delta) FROM network_wg_samples WHERE iface = ? AND ts > ?`
	args := []any{bucket, bucket, iface, from}
	if peer != "" {
		q += ` AND public_key = ?`
		args = append(args, peer)
	}
	q += ` GROUP BY 1 ORDER BY 1`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("reading the tunnel's trend: %w", err)
	}
	for rows.Next() {
		var t, rx, tx int64
		if err := rows.Scan(&t, &rx, &tx); err != nil {
			rows.Close()
			return nil, fmt.Errorf("reading the tunnel's trend: %w", err)
		}
		h.Points = append(h.Points, WGTrendPoint{T: t, Rx: float64(rx) / float64(bucket), Tx: float64(tx) / float64(bucket)})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if peer != "" {
		erows, err := s.db.QueryContext(ctx,
			`SELECT endpoint, first_seen, last_seen, observations FROM network_wg_endpoints WHERE iface = ? AND public_key = ? ORDER BY last_seen DESC`,
			iface, peer)
		if err != nil {
			return nil, fmt.Errorf("reading the peer's endpoints: %w", err)
		}
		for erows.Next() {
			var e WGEndpointSeen
			if err := erows.Scan(&e.Endpoint, &e.FirstSeen, &e.LastSeen, &e.Observations); err != nil {
				erows.Close()
				return nil, err
			}
			h.Endpoints = append(h.Endpoints, e)
		}
		erows.Close()
		urows, err := s.db.QueryContext(ctx,
			`SELECT day, rx, tx FROM network_wg_usage WHERE iface = ? AND public_key = ? AND day > ? ORDER BY day`,
			iface, peer, now/86400-31)
		if err != nil {
			return nil, fmt.Errorf("reading the peer's usage: %w", err)
		}
		for urows.Next() {
			var d WGUsageDay
			var rx, tx int64
			if err := urows.Scan(&d.Day, &rx, &tx); err != nil {
				urows.Close()
				return nil, err
			}
			d.Rx, d.Tx = uint64(rx), uint64(tx)
			h.Usage = append(h.Usage, d)
		}
		urows.Close()
	}
	if h.Events, err = s.wg.events(ctx, iface, peer, 100); err != nil {
		return nil, err
	}
	return h, nil
}

// wgValidPublicKey is a WireGuard key's shape: 32 bytes of base64. The record
// is queried with it and the page puts it in a URL, so nothing else passes.
func wgValidPublicKey(key string) error {
	if !validWGKey(key) {
		return fmt.Errorf("a peer is named by its 44-character public key")
	}
	return nil
}

// WGQuota is a peer's usage budget and how much of it the record has
// measured in the current period. It is an alert threshold, never enforced:
// cutting a peer off from a background loop could cut the operator off too.
type WGQuota struct {
	Period      string `json:"period"`
	LimitBytes  uint64 `json:"limitBytes"`
	UsedBytes   uint64 `json:"usedBytes"`
	PeriodStart int64  `json:"periodStart"`
	// State is ok, warning (80% used), exceeded, or unmeasured when the
	// metrics retention keeps no history to count against.
	State     string `json:"state"`
	Enforced  bool   `json:"enforced"`
	CreatedBy string `json:"createdBy,omitempty"`
}

// WGQuotaRequest sets a peer's budget.
type WGQuotaRequest struct {
	Period     string `json:"period"`
	LimitBytes uint64 `json:"limitBytes"`
}

const wgQuotaWarnPercent = 80

// wgPeriodStart is the UTC start of the calendar day, ISO week or month that
// holds now. Usage is kept per UTC day, so a period always covers whole days.
func wgPeriodStart(period string, now time.Time) (time.Time, error) {
	d := now.UTC()
	day := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	switch period {
	case "day":
		return day, nil
	case "week":
		offset := (int(day.Weekday()) + 6) % 7
		return day.AddDate(0, 0, -offset), nil
	case "month":
		return time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC), nil
	}
	return time.Time{}, fmt.Errorf("the period is day, week or month")
}

// quotas reads every budget of a tunnel with the usage measured against it.
func (r *wgRecord) quotas(ctx context.Context, iface string) (map[string]WGQuota, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT public_key, period, limit_bytes, created_by FROM network_wg_quotas WHERE iface = ?`, iface)
	if err != nil {
		return nil, fmt.Errorf("reading the usage budgets: %w", err)
	}
	type row struct {
		key, period, by string
		limit           int64
	}
	var list []row
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.key, &x.period, &x.limit, &x.by); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := map[string]WGQuota{}
	now := wgNow()
	for _, x := range list {
		start, err := wgPeriodStart(x.period, now)
		if err != nil {
			continue
		}
		q := WGQuota{Period: x.period, LimitBytes: uint64(x.limit), PeriodStart: start.Unix(), CreatedBy: x.by, State: "unmeasured"}
		if r.retention > 0 {
			var used sql.NullInt64
			if err := r.db.QueryRowContext(ctx,
				`SELECT sum(rx + tx) FROM network_wg_usage WHERE iface = ? AND public_key = ? AND day >= ?`,
				iface, x.key, start.Unix()/86400).Scan(&used); err != nil {
				return nil, err
			}
			q.UsedBytes = uint64(used.Int64)
			q.State = "ok"
			switch {
			case q.UsedBytes >= q.LimitBytes:
				q.State = "exceeded"
			case q.UsedBytes*100 >= q.LimitBytes*wgQuotaWarnPercent:
				q.State = "warning"
			}
		}
		out[x.key] = q
	}
	return out, nil
}

// SetWireGuardPeerQuota sets or replaces a peer's usage budget. The peer must
// be one the dashboard made on a tunnel it manages.
func (s *Service) SetWireGuardPeerQuota(ctx context.Context, iface string, id int, req WGQuotaRequest, actor string) (*WGQuota, error) {
	if _, err := wgPeriodStart(req.Period, wgNow()); err != nil {
		return nil, err
	}
	if req.LimitBytes < 1<<20 || req.LimitBytes > 1<<50 {
		return nil, fmt.Errorf("the budget is between 1 MiB and 1 PiB")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	peer, err := s.managedPeer(iface, id)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO network_wg_quotas (iface, public_key, period, limit_bytes, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(iface, public_key) DO UPDATE SET period = excluded.period, limit_bytes = excluded.limit_bytes,
		   created_by = excluded.created_by, created_at = excluded.created_at`,
		iface, peer.publicKey, req.Period, int64(req.LimitBytes), actor, wgNow().Unix()); err != nil {
		return nil, fmt.Errorf("storing the usage budget: %w", err)
	}
	s.wg.note(ctx, WGEvent{Iface: iface, Kind: "quota_set", Peer: peer.publicKey, PeerName: peer.name, Actor: actor,
		Detail: fmt.Sprintf("%d bytes per %s", req.LimitBytes, req.Period)})
	quotas, err := s.wg.quotas(ctx, iface)
	if err != nil {
		return nil, err
	}
	q := quotas[peer.publicKey]
	return &q, nil
}

// ClearWireGuardPeerQuota removes a peer's usage budget. Its recorded usage
// stays: it is the history the next budget will be measured against.
func (s *Service) ClearWireGuardPeerQuota(ctx context.Context, iface string, id int, actor string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	peer, err := s.managedPeer(iface, id)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM network_wg_quotas WHERE iface = ? AND public_key = ?`, iface, peer.publicKey)
	if err != nil {
		return fmt.Errorf("removing the usage budget: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%s has no usage budget: %w", peer.name, ErrNotFound)
	}
	s.wg.note(ctx, WGEvent{Iface: iface, Kind: "quota_cleared", Peer: peer.publicKey, PeerName: peer.name, Actor: actor})
	return nil
}

// managedPeer finds a peer the dashboard made, by its id, in a tunnel it
// manages. The caller holds s.mu.
func (s *Service) managedPeer(iface string, id int) (wgPeerConf, error) {
	conf, err := s.managedWG(iface)
	if err != nil {
		return wgPeerConf{}, err
	}
	for _, sec := range conf.peers() {
		if p := wgPeerOf(sec); p.id == id && id != 0 {
			return p, nil
		}
	}
	return wgPeerConf{}, fmt.Errorf("peer %d of %s: %w", id, iface, ErrNotFound)
}

// WGAlert is something about a tunnel that needs a look: an always-on peer
// whose handshake went stale, a transport routed into a tunnel, a budget
// passed. Alerts are read from the record and the kernel; none acts.
type WGAlert struct {
	Kind     string `json:"kind"`
	Peer     string `json:"peer,omitempty"`
	PeerName string `json:"peerName,omitempty"`
	Since    int64  `json:"since,omitempty"`
	Message  string `json:"message"`
}

// wgAlerts derives a tunnel's alerts from its peers as just read.
func wgAlerts(ifc *WGInterface) []WGAlert {
	alerts := []WGAlert{}
	for _, p := range ifc.Peers {
		label := firstNonEmpty(p.Name, p.Address, p.PublicKey)
		if ifc.Up && p.HandshakeState == "stale" {
			alerts = append(alerts, WGAlert{Kind: "stale_handshake", Peer: p.PublicKey, PeerName: p.Name, Since: p.LatestHandshake,
				Message: fmt.Sprintf("%s keeps its session alive but has not completed a handshake for over %d minutes", label, wgOnlineWithin/60)})
		}
		if p.Transport != nil && p.Transport.State == "captured" {
			alerts = append(alerts, WGAlert{Kind: "transport_captured", Peer: p.PublicKey, PeerName: p.Name, Since: p.Transport.CheckedAt,
				Message: fmt.Sprintf("%s: %s", label, p.Transport.Reason)})
		}
		if p.Quota != nil && (p.Quota.State == "exceeded" || p.Quota.State == "warning") {
			verb := "has used over 80% of"
			if p.Quota.State == "exceeded" {
				verb = "has passed"
			}
			alerts = append(alerts, WGAlert{Kind: "quota_" + p.Quota.State, Peer: p.PublicKey, PeerName: p.Name, Since: p.Quota.PeriodStart,
				Message: fmt.Sprintf("%s %s its %s budget; the budget alerts and does not disconnect", label, verb, p.Quota.Period)})
		}
	}
	return alerts
}

// fillRecord adds to a tunnel just read what the record knows of it: each
// peer's usage budget and last transport check, and the alerts they raise.
func (s *Service) fillRecord(ctx context.Context, ifc *WGInterface) error {
	quotas, err := s.wg.quotas(ctx, ifc.Name)
	if err != nil {
		return err
	}
	for i := range ifc.Peers {
		p := &ifc.Peers[i]
		if q, ok := quotas[p.PublicKey]; ok {
			p.Quota = &q
		}
		if t, ok := s.wg.transport(ifc.Name, p.PublicKey); ok {
			p.Transport = &t
		}
	}
	ifc.Alerts = wgAlerts(ifc)
	return nil
}
