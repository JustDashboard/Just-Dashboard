package netx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// The recorded interface history beyond its charts: an explicit window,
// the percentiles a link is billed or sized by, transfer budgets measured
// against the exact counters, and the changes and incidents that explain a
// step in a line.

// maxHistorySpan is the longest window one history read covers; it is the
// longest the metrics retention is useful for and bounds the SQL.
const maxHistorySpan = 31 * 24 * time.Hour

// Percentiles are a device's recorded interval means at three ranks. The
// basis is the recording interval, not the chart's buckets: a 95th percentile
// of hour-long buckets would average away exactly the bursts it is asked for.
type Percentiles struct {
	Samples      int     `json:"samples"`
	BasisSeconds int64   `json:"basisSeconds"`
	RxP50        float64 `json:"rxP50"`
	RxP95        float64 `json:"rxP95"`
	RxP99        float64 `json:"rxP99"`
	TxP50        float64 `json:"txP50"`
	TxP95        float64 `json:"txP95"`
	TxP99        float64 `json:"txP99"`
}

// HistoryRange reads a recorded window between two instants, bucketed to at
// most maxPoints in SQL, with each device's percentiles over its raw rows.
func (s *Sampler) HistoryRange(ctx context.Context, from, to time.Time, maxPoints int, iface string) (*History, error) {
	if !to.After(from) {
		return nil, errors.New("the window ends before it starts")
	}
	if to.Sub(from) > maxHistorySpan {
		return nil, fmt.Errorf("a window is at most %d days", int(maxHistorySpan.Hours()/24))
	}
	h, err := s.history(ctx, from, to, maxPoints, iface)
	if err != nil {
		return nil, err
	}
	if !h.Recording {
		return h, nil
	}
	h.Percentiles, err = s.percentiles(ctx, from, to, iface)
	if err != nil {
		return nil, err
	}
	if s.retention > 0 {
		h.RetainedFrom = time.Now().Add(-s.retention).Unix()
	}
	return h, nil
}

// percentileGrain is how far a window's ends may move before its
// percentiles are ranked again. A chart polling a week every minute would
// otherwise read the week's rows every minute for figures that cannot change
// visibly in five.
const percentileGrain = 5 * 60

type percentileKey struct {
	iface    string
	from, to int64
}

// percentiles reads the raw interval means of the window per device and ranks
// them. A month at a fifteen-second interval is under two hundred thousand
// rows a device, which sorts in milliseconds; SQLite has no percentile
// function, and an OFFSET per rank would sort the rows six times.
func (s *Sampler) percentiles(ctx context.Context, from, to time.Time, iface string) (map[string]Percentiles, error) {
	key := percentileKey{iface, from.Unix() / percentileGrain, to.Unix() / percentileGrain}
	s.mu.Lock()
	if got, ok := s.ranked[key]; ok {
		s.mu.Unlock()
		return got, nil
	}
	s.mu.Unlock()
	out, err := s.rankPercentiles(ctx, from, to, iface)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.ranked == nil || len(s.ranked) >= 32 {
		s.ranked = map[percentileKey]map[string]Percentiles{}
	}
	s.ranked[key] = out
	s.mu.Unlock()
	return out, nil
}

func (s *Sampler) rankPercentiles(ctx context.Context, from, to time.Time, iface string) (map[string]Percentiles, error) {
	query := `SELECT iface, rx_rate, tx_rate FROM metric_interface_samples WHERE ts >= ? AND ts <= ?`
	args := []any{from.Unix(), to.Unix()}
	if iface != "" {
		query += ` AND iface = ?`
		args = append(args, iface)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rx, tx := map[string][]float64{}, map[string][]float64{}
	for rows.Next() {
		var name string
		var r, t float64
		if err := rows.Scan(&name, &r, &t); err != nil {
			return nil, err
		}
		rx[name] = append(rx[name], r)
		tx[name] = append(tx[name], t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]Percentiles, len(rx))
	for name, rs := range rx {
		ts := tx[name]
		sort.Float64s(rs)
		sort.Float64s(ts)
		out[name] = Percentiles{
			Samples: len(rs), BasisSeconds: int64(s.every.Seconds()),
			RxP50: quantile(rs, 0.5), RxP95: quantile(rs, 0.95), RxP99: quantile(rs, 0.99),
			TxP50: quantile(ts, 0.5), TxP95: quantile(ts, 0.95), TxP99: quantile(ts, 0.99),
		}
	}
	return out, nil
}

// InterfaceQuota is a device's transfer budget for a calendar period and what
// the recorder measured against it. Like a WireGuard peer's budget it only
// raises an alert: limiting a device from a background loop could cut the
// operator off with it.
type InterfaceQuota struct {
	Iface string `json:"iface"`
	// Period is day, week or month (UTC); Direction is both, rx or tx.
	Period      string `json:"period"`
	Direction   string `json:"direction"`
	LimitBytes  uint64 `json:"limitBytes"`
	PeriodStart int64  `json:"periodStart"`
	// UsedBytes is the exact counter growth of the recorded intervals;
	// EstimatedBytes is what intervals recorded before exact counters existed
	// add, from their mean rate. Both count toward State.
	UsedBytes      uint64 `json:"usedBytes"`
	EstimatedBytes uint64 `json:"estimatedBytes"`
	// Coverage is the share of the period so far that recorded intervals
	// cover. Time the dashboard was stopped is traffic nobody counted.
	Coverage float64 `json:"coverage"`
	// RetentionShort is true where the metrics retention keeps less than the
	// period, so its start has already been pruned.
	RetentionShort bool `json:"retentionShort"`
	// State is ok, warning (80% used), exceeded, or unmeasured where nothing
	// is recorded at all.
	State     string `json:"state"`
	Enforced  bool   `json:"enforced"`
	CreatedBy string `json:"createdBy,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

// InterfaceQuotaRequest sets a device's budget.
type InterfaceQuotaRequest struct {
	Period     string `json:"period"`
	Direction  string `json:"direction"`
	LimitBytes uint64 `json:"limitBytes"`
}

const quotaWarnPercent = 80

// InterfaceQuotas reads every budget with its measured usage.
func (s *Service) InterfaceQuotas(ctx context.Context) ([]InterfaceQuota, error) {
	return s.sampler.quotas(ctx, time.Now())
}

func (s *Sampler) quotas(ctx context.Context, now time.Time) ([]InterfaceQuota, error) {
	out := []InterfaceQuota{}
	if s.db == nil {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT iface, period, direction, limit_bytes, created_by, created_at
		  FROM network_interface_quotas ORDER BY iface`)
	if err != nil {
		return nil, fmt.Errorf("reading the transfer budgets: %w", err)
	}
	for rows.Next() {
		var q InterfaceQuota
		var limit int64
		if err := rows.Scan(&q.Iface, &q.Period, &q.Direction, &limit, &q.CreatedBy, &q.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		q.LimitBytes = uint64(limit)
		out = append(out, q)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.measureQuota(ctx, &out[i], now); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// measureQuota sums the period's recorded intervals. Rows with exact
// counters count their bytes; older rows count their mean rate over the
// recording interval and are reported apart, so an estimate never passes for
// a measurement.
func (s *Sampler) measureQuota(ctx context.Context, q *InterfaceQuota, now time.Time) error {
	start, err := wgPeriodStart(q.Period, now)
	if err != nil {
		return err
	}
	q.PeriodStart = start.Unix()
	q.State = "unmeasured"
	if s.db == nil || s.retention <= 0 {
		return nil
	}
	rxCol, txCol := "rx", "tx"
	switch q.Direction {
	case "rx":
		txCol = ""
	case "tx":
		rxCol = ""
	}
	exact, estimate := "0", "0"
	if rxCol != "" {
		exact += " + COALESCE(rx_bytes, 0)"
		estimate += " + rx_rate"
	}
	if txCol != "" {
		exact += " + COALESCE(tx_bytes, 0)"
		estimate += " + tx_rate"
	}
	var used, estimated sql.NullFloat64
	var covered sql.NullInt64
	var rows int
	err = s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT
		SUM(CASE WHEN span IS NOT NULL THEN %s ELSE 0 END),
		SUM(CASE WHEN span IS NULL THEN (%s) * ? ELSE 0 END),
		SUM(CASE WHEN span IS NOT NULL THEN span ELSE ? END),
		COUNT(*)
		  FROM metric_interface_samples WHERE iface = ? AND ts >= ? AND ts <= ?`, exact, estimate),
		s.every.Seconds(), int64(s.every.Seconds()), q.Iface, start.Unix(), now.Unix()).
		Scan(&used, &estimated, &covered, &rows)
	if err != nil {
		return fmt.Errorf("measuring the transfer budget of %s: %w", q.Iface, err)
	}
	q.UsedBytes = uint64(math.Max(0, used.Float64))
	q.EstimatedBytes = uint64(math.Max(0, math.Round(estimated.Float64)))
	if elapsed := now.Sub(start).Seconds(); elapsed > 0 {
		q.Coverage = math.Min(1, math.Round(float64(covered.Int64)/elapsed*1000)/1000)
	}
	q.RetentionShort = now.Add(-s.retention).After(start)
	if rows == 0 {
		return nil
	}
	total := q.UsedBytes + q.EstimatedBytes
	q.State = "ok"
	switch {
	case total >= q.LimitBytes:
		q.State = "exceeded"
	case total*100 >= q.LimitBytes*quotaWarnPercent:
		q.State = "warning"
	}
	return nil
}

// SetInterfaceQuota sets or replaces a device's transfer budget. The device
// must exist now; a budget on a name that later disappears simply stops
// accruing.
func (s *Service) SetInterfaceQuota(ctx context.Context, iface string, req InterfaceQuotaRequest, actor string) (*InterfaceQuota, error) {
	if err := ValidIfName(iface); err != nil {
		return nil, err
	}
	if _, err := wgPeriodStart(req.Period, time.Now()); err != nil {
		return nil, err
	}
	if req.Direction == "" {
		req.Direction = "both"
	}
	if req.Direction != "both" && req.Direction != "rx" && req.Direction != "tx" {
		return nil, errors.New("the direction is both, rx or tx")
	}
	if req.LimitBytes < 1<<20 || req.LimitBytes > 1<<50 {
		return nil, errors.New("the budget is between 1 MiB and 1 PiB")
	}
	if strings.HasPrefix(iface, "veth") {
		return nil, fmt.Errorf("%s is one end of a container's pair and is not recorded; set the budget on the device it leads to", iface)
	}
	if s.db == nil {
		return nil, errors.New("the dashboard has no database to keep a budget in")
	}
	if _, err := linkKind(ctx, iface); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO network_interface_quotas (iface, period, direction, limit_bytes, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(iface) DO UPDATE SET period = excluded.period, direction = excluded.direction,
		  limit_bytes = excluded.limit_bytes, created_by = excluded.created_by, created_at = excluded.created_at`,
		iface, req.Period, req.Direction, int64(req.LimitBytes), actor, time.Now().Unix()); err != nil {
		return nil, fmt.Errorf("storing the transfer budget: %w", err)
	}
	list, err := s.InterfaceQuotas(ctx)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Iface == iface {
			return &list[i], nil
		}
	}
	return nil, fmt.Errorf("budget of %s: %w", iface, ErrNotFound)
}

// ClearInterfaceQuota removes a device's budget; its recorded traffic stays.
func (s *Service) ClearInterfaceQuota(ctx context.Context, iface string) error {
	if err := ValidIfName(iface); err != nil {
		return err
	}
	if s.db == nil {
		return fmt.Errorf("budget of %s: %w", iface, ErrNotFound)
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM network_interface_quotas WHERE iface = ?`, iface)
	if err != nil {
		return fmt.Errorf("removing the transfer budget: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%s has no transfer budget: %w", iface, ErrNotFound)
	}
	return nil
}

// TrafficAnnotation is something that happened during a window, in the
// shape a chart marks: a network change made through the dashboard, or a
// saved diagnostic run an operator opened as an incident.
type TrafficAnnotation struct {
	TS       time.Time `json:"ts"`
	Kind     string    `json:"kind"`
	Title    string    `json:"title"`
	Detail   string    `json:"detail,omitempty"`
	Severity string    `json:"severity"`
	// Source is change or incident; Ref is the saved run's id.
	Source string `json:"source"`
	Ref    string `json:"ref,omitempty"`
}

// maxAnnotations bounds each source, so a busy audit log cannot crowd out
// the handful of incidents.
const maxAnnotations = 100

// TrafficAnnotations reads the network and firewall changes and the saved
// diagnostic runs between two instants, oldest first.
func (s *Service) TrafficAnnotations(ctx context.Context, from, to time.Time) ([]TrafficAnnotation, error) {
	out := []TrafficAnnotation{}
	if s.db == nil {
		return out, nil
	}
	if !to.After(from) || to.Sub(from) > maxHistorySpan {
		return nil, errors.New("the window is a positive span of at most 31 days")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT ts, action, target, username, success FROM audit_log
		 WHERE ts >= ? AND ts <= ? AND (action LIKE 'network.%' OR action LIKE 'firewall.%')
		   AND action NOT LIKE 'network.probe%' AND action NOT LIKE 'network.diagnostic.%'
		 ORDER BY ts DESC LIMIT ?`, from.Unix(), to.Unix(), maxAnnotations)
	if err != nil {
		return nil, fmt.Errorf("reading the network changes: %w", err)
	}
	for rows.Next() {
		var ts int64
		var action, target, user string
		var success bool
		if err := rows.Scan(&ts, &action, &target, &user, &success); err != nil {
			rows.Close()
			return nil, err
		}
		a := TrafficAnnotation{TS: time.Unix(ts, 0).UTC(), Kind: "action", Source: "change", Severity: "info",
			Title: strings.TrimSpace(action + " " + target)}
		if user != "" {
			a.Detail = "by " + user
		}
		if !success {
			a.Severity = "error"
			a.Detail = strings.TrimSpace("failed " + a.Detail)
		}
		out = append(out, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	runs, err := s.db.QueryContext(ctx, `SELECT id, name, status, created_at FROM network_diagnostic_runs
		 WHERE created_at >= ? AND created_at <= ? ORDER BY created_at DESC LIMIT ?`,
		from.UnixMilli(), to.UnixMilli(), maxAnnotations)
	if err != nil {
		// A build without saved diagnostics still has its change markers.
		return sortAnnotations(out), nil
	}
	defer runs.Close()
	for runs.Next() {
		var id, name, status string
		var created int64
		if err := runs.Scan(&id, &name, &status, &created); err != nil {
			return nil, err
		}
		out = append(out, TrafficAnnotation{TS: time.UnixMilli(created).UTC(), Kind: "action", Source: "incident",
			Severity: "warning", Title: "Incident: " + name, Detail: "saved diagnostic, " + status, Ref: id})
	}
	return sortAnnotations(out), runs.Err()
}

func sortAnnotations(a []TrafficAnnotation) []TrafficAnnotation {
	sort.SliceStable(a, func(i, j int) bool { return a[i].TS.Before(a[j].TS) })
	return a
}
