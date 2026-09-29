package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// Proxy alerts: the proxy pages' readings, watched while nobody reads them.
//
// A rule names a kind of trouble — a certificate running out, the engine
// stopped, an upstream refusing, a watched endpoint gone or untrusted, a site
// answering 5xx — and the channels to tell. Every few minutes a pass takes the
// same readings the pages show and follows each subject the rule covers: it
// tells the channels once when a subject starts firing and once when it comes
// back, and says nothing in between. The channels are the deployments'
// notification channels; there is no second delivery system here.
//
// A watched endpoint's grade dropping is not offered here: watched checks
// record the handshake and certificate, not a full TLS grade.

const (
	proxyAlertCertExpiring     = "cert_expiring"
	proxyAlertCertExpired      = "cert_expired"
	proxyAlertRenewalFailed    = "renewal_failed"
	proxyAlertServedDrift      = "served_drift"
	proxyAlertEngineDown       = "engine_down"
	proxyAlertUpstreamDown     = "upstream_down"
	proxyAlertWatchUnreachable = "watch_unreachable"
	proxyAlertWatchUntrusted   = "watch_untrusted"
	proxyAlertSiteErrors       = "site_errors"

	// proxyAlertFiringLevel is the one level of every kind but expiry,
	// which fires once per threshold crossed.
	proxyAlertFiringLevel = "firing"

	proxyAlertInterval = 5 * time.Minute
	proxyAlertHistory  = 500
)

var proxyAlertKinds = []string{
	proxyAlertCertExpiring, proxyAlertCertExpired, proxyAlertRenewalFailed, proxyAlertServedDrift,
	proxyAlertEngineDown, proxyAlertUpstreamDown,
	proxyAlertWatchUnreachable, proxyAlertWatchUntrusted, proxyAlertSiteErrors,
}

// proxyAlertExpiryDays are the thresholds a certificate expiry rule may tell
// at. 21 is a week after certbot starts renewing at 30, so a renewal that is
// failing quietly is told while there is still time to fix it.
var proxyAlertExpiryDays = []int{21, 7, 3, 1}

var (
	errProxyAlertNotFound = errors.New("proxy alert rule not found")
	errProxyAlertInvalid  = errors.New("invalid proxy alert rule")
)

func invalidProxyAlert(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errProxyAlertInvalid, fmt.Sprintf(format, args...))
}

// proxyAlertParams holds every kind's parameters; each kind reads its own
// and validation clears the rest.
type proxyAlertParams struct {
	// Days are the expiry thresholds, most distant first.
	Days []int `json:"days,omitempty"`
	// Minutes is how long an upstream must stay down, or a site's window.
	Minutes int `json:"minutes,omitempty"`
	// Threshold is a site's 5xx share, as a percentage.
	Threshold float64 `json:"threshold,omitempty"`
	// MinRequests keeps a site with three requests, one failed, from paging.
	MinRequests int `json:"minRequests,omitempty"`
}

type proxyAlertRule struct {
	ID     int64            `json:"id"`
	Kind   string           `json:"kind"`
	Params proxyAlertParams `json:"params"`
	// Channels names the notification channels the rule tells; empty tells
	// every enabled one.
	Channels  []int64   `json:"channels"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type proxyAlertWrite struct {
	Kind     string           `json:"kind"`
	Params   proxyAlertParams `json:"params"`
	Channels []int64          `json:"channels"`
	Enabled  *bool            `json:"enabled,omitempty"`
}

func validateProxyAlert(in proxyAlertWrite) (proxyAlertWrite, error) {
	in.Kind = strings.TrimSpace(strings.ToLower(in.Kind))
	p := in.Params
	out := proxyAlertParams{}
	switch in.Kind {
	case proxyAlertCertExpiring:
		days := p.Days
		if len(days) == 0 {
			days = proxyAlertExpiryDays
		}
		for _, allowed := range proxyAlertExpiryDays {
			for _, d := range days {
				if d == allowed {
					out.Days = append(out.Days, d)
					break
				}
			}
		}
		if len(out.Days) != len(uniqueInts(days)) {
			return in, invalidProxyAlert("expiry thresholds are chosen from 21, 7, 3 and 1 days")
		}
	case proxyAlertUpstreamDown:
		out.Minutes = p.Minutes
		if out.Minutes == 0 {
			out.Minutes = 5
		}
		if out.Minutes < 5 || out.Minutes > 1440 {
			return in, invalidProxyAlert("an upstream is told down after 5 minutes to a day")
		}
	case proxyAlertSiteErrors:
		out.Threshold, out.Minutes, out.MinRequests = p.Threshold, p.Minutes, p.MinRequests
		if out.Minutes == 0 {
			out.Minutes = 15
		}
		if out.MinRequests == 0 {
			out.MinRequests = 20
		}
		if out.Threshold < 0.1 || out.Threshold > 100 {
			return in, invalidProxyAlert("a 5xx share is a percentage between 0.1 and 100")
		}
		if out.Minutes < 5 || out.Minutes > 1440 {
			return in, invalidProxyAlert("the window is between 5 minutes and a day")
		}
		if out.MinRequests < 1 || out.MinRequests > 1_000_000 {
			return in, invalidProxyAlert("the fewest requests to judge is between 1 and 1,000,000")
		}
	case proxyAlertCertExpired, proxyAlertRenewalFailed, proxyAlertServedDrift,
		proxyAlertEngineDown, proxyAlertWatchUnreachable, proxyAlertWatchUntrusted:
	default:
		return in, invalidProxyAlert("kind must be one of %s", strings.Join(proxyAlertKinds, ", "))
	}
	in.Params = out
	in.Channels = uniquePositive(in.Channels)
	return in, nil
}

func uniqueInts(values []int) []int {
	seen := map[int]bool{}
	out := []int{}
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func uniquePositive(ids []int64) []int64 {
	seen := map[int64]bool{}
	out := []int64{}
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// proxyAlertSubject is one subject a rule follows, as the sheet lists it.
type proxyAlertSubject struct {
	RuleID  int64  `json:"ruleId"`
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	Level   string `json:"level"`
	Label   string `json:"label"`
	Detail  string `json:"detail"`
	// State is "pending" while a kind that must hold has not held long
	// enough to be told, "firing" once told, and "quiet" otherwise.
	State       string     `json:"state"`
	Since       *time.Time `json:"since,omitempty"`
	NotifiedAt  *time.Time `json:"notifiedAt,omitempty"`
	RecoveredAt *time.Time `json:"recoveredAt,omitempty"`
	Muted       bool       `json:"muted"`
}

type proxyAlertEvent struct {
	ID        int64     `json:"id"`
	RuleID    int64     `json:"ruleId"`
	Kind      string    `json:"kind"`
	Subject   string    `json:"subject"`
	Level     string    `json:"level"`
	Label     string    `json:"label"`
	Event     string    `json:"event"`
	Detail    string    `json:"detail"`
	Delivered int       `json:"delivered"`
	Failed    int       `json:"failed"`
	Muted     bool      `json:"muted"`
	CreatedAt time.Time `json:"createdAt"`
}

// proxyAlertFinding is one subject a reading found firing at one level.
type proxyAlertFinding struct {
	Subject, Level, Label, Detail string
}

// proxyAlertReading is what one pass saw for one rule. A rule whose source
// could not be read is not judged at all — an unreadable source is not a
// recovery — and a subject in Unknown keeps whatever state it had.
type proxyAlertReading struct {
	Judged  bool
	Firing  []proxyAlertFinding
	Unknown map[string]bool
	// Now is each healthy subject's reading, for the recovery message.
	Now map[string]string
}

// proxyAlertObserver takes every rule's reading at `now`; the server's reads
// the proxy's own sources, a test's returns what it is told to.
type proxyAlertObserver func(ctx context.Context, rules []proxyAlertRule, now time.Time) map[int64]proxyAlertReading

// proxyAlertDeliverer sends one envelope to one channel.
type proxyAlertDeliverer func(ctx context.Context, channelID int64, envelope deploy.NotificationEnvelope) error

type proxyAlerts struct {
	db       *sql.DB
	observe  proxyAlertObserver
	deliver  proxyAlertDeliverer
	channels func(ctx context.Context) ([]deploy.NotificationChannel, error)
	url      func() string
	now      func() time.Time
	interval time.Duration
	log      *slog.Logger

	// mu keeps a pass on demand and the scheduled one from both telling the
	// same transition. Rule edits do not take it: a pass can take a minute,
	// and a form is not held that long.
	mu       sync.Mutex
	lastPass atomic.Int64
}

// Start runs a pass every interval until the context ends.
func (a *proxyAlerts) Start(ctx context.Context) {
	if a == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(a.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			pass, cancel := context.WithTimeout(ctx, a.interval-10*time.Second)
			a.Evaluate(pass)
			cancel()
		}
	}()
}

// LastPass is when the last pass finished; zero before the first.
func (a *proxyAlerts) LastPass() *time.Time {
	return unixTime(a.lastPass.Load())
}

// Evaluate is one pass over every enabled rule.
func (a *proxyAlerts) Evaluate(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	rules, err := a.Rules(ctx, true)
	if err != nil {
		a.warn("proxy alert rules unavailable", err)
		return
	}
	now := a.now().UTC()
	readings := map[int64]proxyAlertReading{}
	if len(rules) > 0 {
		readings = a.observe(ctx, rules, now)
	}
	for _, rule := range rules {
		if ctx.Err() != nil {
			break
		}
		if reading, ok := readings[rule.ID]; ok && reading.Judged {
			if err := a.evaluateRule(ctx, rule, reading, now); err != nil {
				a.warn(fmt.Sprintf("proxy alert rule %d not evaluated", rule.ID), err)
			}
		}
	}
	a.lastPass.Store(now.Unix())
	_, _ = a.db.ExecContext(context.WithoutCancel(ctx),
		`DELETE FROM proxy_alert_events WHERE id <= (SELECT id FROM proxy_alert_events ORDER BY id DESC LIMIT 1 OFFSET ?)`, proxyAlertHistory)
}

// proxyAlertHold is how many passes in a row, and how long, a kind must be
// found firing before it is told. The engine is often down for a moment
// during a restart, and a remote endpoint misses a handshake now and then;
// an upstream is told only once it has been down for the rule's minutes.
func proxyAlertHold(rule proxyAlertRule) (int, time.Duration) {
	switch rule.Kind {
	case proxyAlertEngineDown, proxyAlertWatchUnreachable:
		return 2, time.Minute
	case proxyAlertUpstreamDown:
		return 2, time.Duration(rule.Params.Minutes) * time.Minute
	}
	return 1, 0
}

// proxyAlertSeverity orders a subject's levels: an expiry level's days, and
// every other kind's one level at zero. The smallest is the most severe.
func proxyAlertSeverity(level string) int {
	if days, err := strconv.Atoi(strings.TrimSuffix(level, "d")); err == nil && strings.HasSuffix(level, "d") {
		return days
	}
	return 0
}

type proxyAlertStateRow struct {
	subject, level, label, detail        string
	seen                                 int
	firingSince, notifiedAt, recoveredAt int64
	muted                                bool
}

func (a *proxyAlerts) evaluateRule(ctx context.Context, rule proxyAlertRule, reading proxyAlertReading, now time.Time) error {
	rows, err := a.db.QueryContext(ctx, `SELECT subject, level, label, detail, seen, firing_since, notified_at, recovered_at, muted FROM proxy_alert_state WHERE rule_id=?`, rule.ID)
	if err != nil {
		return err
	}
	states := map[string]*proxyAlertStateRow{}
	for rows.Next() {
		var row proxyAlertStateRow
		var muted int
		if err := rows.Scan(&row.subject, &row.level, &row.label, &row.detail, &row.seen, &row.firingSince, &row.notifiedAt, &row.recoveredAt, &muted); err != nil {
			rows.Close()
			return err
		}
		row.muted = muted == 1
		states[row.subject+"\x00"+row.level] = &row
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// Detached: a transition that was decided must be written even when the
	// pass ran out of time deciding it, or it would be told twice.
	write := context.WithoutCancel(ctx)
	ticks, hold := proxyAlertHold(rule)
	active := map[string]bool{}
	told := map[string][]*proxyAlertStateRow{}
	for _, f := range reading.Firing {
		key := f.Subject + "\x00" + f.Level
		if active[key] {
			continue
		}
		active[key] = true
		row := states[key]
		if row == nil || row.firingSince == 0 {
			// A new episode. A subject muted at any level stays muted at a
			// level it reaches later.
			row = &proxyAlertStateRow{subject: f.Subject, level: f.Level, seen: 1, firingSince: now.Unix()}
			if _, err := a.db.ExecContext(write, `INSERT INTO proxy_alert_state (rule_id, subject, level, label, detail, seen, firing_since, notified_at, recovered_at, muted)
				VALUES (?,?,?,?,?,1,?,0,0,(SELECT COALESCE(MAX(muted),0) FROM proxy_alert_state WHERE rule_id=? AND subject=?))
				ON CONFLICT(rule_id, subject, level) DO UPDATE SET label=excluded.label, detail=excluded.detail, seen=1, firing_since=excluded.firing_since, notified_at=0, recovered_at=0`,
				rule.ID, f.Subject, f.Level, f.Label, f.Detail, now.Unix(), rule.ID, f.Subject); err != nil {
				return err
			}
		} else {
			row.seen++
			if _, err := a.db.ExecContext(write, `UPDATE proxy_alert_state SET label=?, detail=?, seen=? WHERE rule_id=? AND subject=? AND level=?`,
				f.Label, f.Detail, row.seen, rule.ID, f.Subject, f.Level); err != nil {
				return err
			}
		}
		row.label, row.detail = f.Label, f.Detail
		if row.notifiedAt == 0 && row.seen >= ticks && now.Sub(time.Unix(row.firingSince, 0)) >= hold {
			row.notifiedAt = now.Unix()
			if _, err := a.db.ExecContext(write, `UPDATE proxy_alert_state SET notified_at=? WHERE rule_id=? AND subject=? AND level=?`,
				now.Unix(), rule.ID, f.Subject, f.Level); err != nil {
				return err
			}
			told[f.Subject] = append(told[f.Subject], row)
		}
	}

	recovered := map[string][]*proxyAlertStateRow{}
	for key, row := range states {
		if active[key] || row.firingSince == 0 || reading.Unknown[row.subject] {
			continue
		}
		recoveredAt := int64(0)
		if row.notifiedAt > 0 {
			// Only a told firing has a recovery to tell; one still pending
			// ends without a word.
			recoveredAt = now.Unix()
			recovered[row.subject] = append(recovered[row.subject], row)
		}
		if _, err := a.db.ExecContext(write, `UPDATE proxy_alert_state SET seen=0, firing_since=0, notified_at=0, recovered_at=? WHERE rule_id=? AND subject=? AND level=?`,
			recoveredAt, rule.ID, row.subject, row.level); err != nil {
			return err
		}
	}

	// One message per subject, at its most severe level: a certificate first
	// seen at two days is told once, as two days, not as 21, 7 and 3.
	for _, subject := range sortedKeys(told) {
		row := mostSevere(told[subject])
		a.announce(ctx, rule, row, deploy.NotificationEventProxyFiring, row.detail, time.Unix(row.firingSince, 0).UTC())
	}
	for _, subject := range sortedKeys(recovered) {
		row := mostSevere(recovered[subject])
		detail := reading.Now[subject]
		if detail == "" {
			detail = "No longer found by this rule."
		}
		a.announce(ctx, rule, row, deploy.NotificationEventProxyRecovered, detail, now)
	}
	return nil
}

func sortedKeys(m map[string][]*proxyAlertStateRow) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func mostSevere(rows []*proxyAlertStateRow) *proxyAlertStateRow {
	best := rows[0]
	for _, row := range rows[1:] {
		if proxyAlertSeverity(row.level) < proxyAlertSeverity(best.level) {
			best = row
		}
	}
	return best
}

// announce tells the rule's channels, unless the subject is muted, and keeps
// what happened either way for the history.
func (a *proxyAlerts) announce(ctx context.Context, rule proxyAlertRule, row *proxyAlertStateRow, event, detail string, since time.Time) {
	var muted int
	_ = a.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(muted),0) FROM proxy_alert_state WHERE rule_id=? AND subject=?`, rule.ID, row.subject).Scan(&muted)
	delivered, failed := 0, 0
	if muted == 0 {
		envelope := a.envelope(rule, row, event, detail, since)
		for _, id := range a.recipients(ctx, rule) {
			switch err := a.deliver(ctx, id, envelope); {
			case err == nil:
				delivered++
			case errors.Is(err, deploy.ErrHookDisabled):
				// A paused channel is told nothing, and that is not a failure.
			default:
				failed++
				a.warn(fmt.Sprintf("proxy alert delivery failed for channel %d", id), err)
			}
		}
	}
	if _, err := a.db.ExecContext(context.WithoutCancel(ctx), `INSERT INTO proxy_alert_events (rule_id, kind, subject, level, label, event, detail, delivered, failed, muted, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		rule.ID, rule.Kind, row.subject, row.level, row.label, event, detail, delivered, failed, muted, a.now().UTC().Unix()); err != nil {
		a.warn("proxy alert event not recorded", err)
	}
}

// recipients is the rule's channels, or every enabled channel when it names
// none.
func (a *proxyAlerts) recipients(ctx context.Context, rule proxyAlertRule) []int64 {
	if len(rule.Channels) > 0 {
		return rule.Channels
	}
	all, err := a.channels(ctx)
	if err != nil {
		a.warn("notification channels unavailable", err)
		return nil
	}
	ids := []int64{}
	for _, channel := range all {
		if channel.Enabled {
			ids = append(ids, channel.ID)
		}
	}
	return ids
}

func (a *proxyAlerts) envelope(rule proxyAlertRule, row *proxyAlertStateRow, event, detail string, since time.Time) deploy.NotificationEnvelope {
	envelope := deploy.NotificationEnvelope{
		Event: event, SentAt: a.now().UTC(),
		Proxy: &deploy.ProxyAlertEnvelope{
			RuleID: rule.ID, Kind: rule.Kind, Subject: row.subject, Level: row.level,
			Label: row.label, Detail: detail, Since: since,
		},
	}
	if a.url != nil {
		if base := strings.TrimRight(strings.TrimSpace(a.url()), "/"); base != "" {
			envelope.URL = base + "/proxy"
		}
	}
	return envelope
}

// TestEnvelope is what "Send test" delivers to a channel: a proxy alert as
// one would arrive, marked as a test so nobody acts on it.
func (a *proxyAlerts) TestEnvelope() deploy.NotificationEnvelope {
	row := &proxyAlertStateRow{subject: "/etc/letsencrypt/live/example.com/fullchain.pem", level: "7d", label: "example.com"}
	envelope := a.envelope(proxyAlertRule{Kind: proxyAlertCertExpiring}, row, deploy.NotificationEventProxyFiring,
		"6 days left. This is a test from the proxy's Alerts sheet; nothing is expiring.", a.now().UTC())
	envelope.Proxy.Test = true
	return envelope
}

func (a *proxyAlerts) warn(message string, err error) {
	if a.log != nil {
		a.log.Warn(message, "err", err)
	}
}

// --- rules ---

const proxyAlertRuleColumns = `id, kind, params, channels, enabled, created_at, updated_at`

func (a *proxyAlerts) Rules(ctx context.Context, enabledOnly bool) ([]proxyAlertRule, error) {
	query := `SELECT ` + proxyAlertRuleColumns + ` FROM proxy_alert_rules`
	if enabledOnly {
		query += ` WHERE enabled=1`
	}
	rows, err := a.db.QueryContext(ctx, query+` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []proxyAlertRule{}
	for rows.Next() {
		var rule proxyAlertRule
		var params, channels string
		var enabled int
		var created, updated int64
		if err := rows.Scan(&rule.ID, &rule.Kind, &params, &channels, &enabled, &created, &updated); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(params), &rule.Params)
		rule.Channels = []int64{}
		_ = json.Unmarshal([]byte(channels), &rule.Channels)
		rule.Enabled = enabled == 1
		rule.CreatedAt, rule.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
		out = append(out, rule)
	}
	return out, rows.Err()
}

func (a *proxyAlerts) rule(ctx context.Context, id int64) (*proxyAlertRule, error) {
	rules, err := a.Rules(ctx, false)
	if err != nil {
		return nil, err
	}
	for i := range rules {
		if rules[i].ID == id {
			return &rules[i], nil
		}
	}
	return nil, errProxyAlertNotFound
}

// checkChannels refuses a rule naming a channel that does not exist, so a
// typo is a form error now rather than a silent alert later.
func (a *proxyAlerts) checkChannels(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	all, err := a.channels(ctx)
	if err != nil {
		return err
	}
	known := map[int64]bool{}
	for _, channel := range all {
		known[channel.ID] = true
	}
	for _, id := range ids {
		if !known[id] {
			return invalidProxyAlert("notification channel %d does not exist", id)
		}
	}
	return nil
}

func (a *proxyAlerts) CreateRule(ctx context.Context, in proxyAlertWrite) (*proxyAlertRule, error) {
	in, err := validateProxyAlert(in)
	if err != nil {
		return nil, err
	}
	if err := a.checkChannels(ctx, in.Channels); err != nil {
		return nil, err
	}
	enabled := in.Enabled == nil || *in.Enabled
	params, _ := json.Marshal(in.Params)
	channels, _ := json.Marshal(in.Channels)
	now := a.now().UTC().Unix()
	res, err := a.db.ExecContext(ctx, `INSERT INTO proxy_alert_rules (kind, params, channels, enabled, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
		in.Kind, string(params), string(channels), boolInt(enabled), now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return a.rule(ctx, id)
}

func (a *proxyAlerts) UpdateRule(ctx context.Context, id int64, in proxyAlertWrite) (*proxyAlertRule, error) {
	current, err := a.rule(ctx, id)
	if err != nil {
		return nil, err
	}
	in, err = validateProxyAlert(in)
	if err != nil {
		return nil, err
	}
	if err := a.checkChannels(ctx, in.Channels); err != nil {
		return nil, err
	}
	enabled := current.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	params, _ := json.Marshal(in.Params)
	channels, _ := json.Marshal(in.Channels)
	if _, err := a.db.ExecContext(ctx, `UPDATE proxy_alert_rules SET kind=?, params=?, channels=?, enabled=?, updated_at=? WHERE id=?`,
		in.Kind, string(params), string(channels), boolInt(enabled), a.now().UTC().Unix(), id); err != nil {
		return nil, err
	}
	// A rule with new terms, or one paused, starts over: what it was firing
	// on was a verdict on the old terms, and a paused rule follows nothing.
	// Mutes are the operator's and are kept.
	if in.Kind != current.Kind || string(params) != mustJSON(current.Params) || !enabled {
		if _, err := a.db.ExecContext(ctx, `DELETE FROM proxy_alert_state WHERE rule_id=? AND muted=0`, id); err != nil {
			return nil, err
		}
		if _, err := a.db.ExecContext(ctx, `UPDATE proxy_alert_state SET seen=0, firing_since=0, notified_at=0, recovered_at=0 WHERE rule_id=?`, id); err != nil {
			return nil, err
		}
	}
	return a.rule(ctx, id)
}

func mustJSON(v any) string {
	out, _ := json.Marshal(v)
	return string(out)
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (a *proxyAlerts) DeleteRule(ctx context.Context, id int64) (*proxyAlertRule, error) {
	current, err := a.rule(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, err := a.db.ExecContext(ctx, `DELETE FROM proxy_alert_rules WHERE id=?`, id); err != nil {
		return nil, err
	}
	if _, err := a.db.ExecContext(ctx, `DELETE FROM proxy_alert_state WHERE rule_id=?`, id); err != nil {
		return nil, err
	}
	return current, nil
}

// SetMuted mutes or unmutes every level of one subject of one rule.
func (a *proxyAlerts) SetMuted(ctx context.Context, ruleID int64, subject string, muted bool) error {
	res, err := a.db.ExecContext(ctx, `UPDATE proxy_alert_state SET muted=? WHERE rule_id=? AND subject=?`, boolInt(muted), ruleID, subject)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errProxyAlertNotFound
	}
	// A quiet subject that is unmuted has nothing left to keep.
	if !muted {
		_, err = a.db.ExecContext(ctx, `DELETE FROM proxy_alert_state WHERE rule_id=? AND subject=? AND firing_since=0`, ruleID, subject)
	}
	return err
}

// Subjects lists every subject that is firing, pending or muted, one row per
// rule and subject at its most severe level.
func (a *proxyAlerts) Subjects(ctx context.Context) ([]proxyAlertSubject, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT s.rule_id, r.kind, s.subject, s.level, s.label, s.detail, s.firing_since, s.notified_at, s.recovered_at, s.muted
		FROM proxy_alert_state s JOIN proxy_alert_rules r ON r.id = s.rule_id
		WHERE s.firing_since > 0 OR s.muted = 1 ORDER BY s.rule_id, s.subject`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []proxyAlertSubject{}
	index := map[string]int{}
	for rows.Next() {
		var s proxyAlertSubject
		var since, notified, recovered int64
		var muted int
		if err := rows.Scan(&s.RuleID, &s.Kind, &s.Subject, &s.Level, &s.Label, &s.Detail, &since, &notified, &recovered, &muted); err != nil {
			return nil, err
		}
		s.Muted = muted == 1
		s.Since, s.NotifiedAt, s.RecoveredAt = unixTime(since), unixTime(notified), unixTime(recovered)
		switch {
		case notified > 0:
			s.State = "firing"
		case since > 0:
			s.State = "pending"
		default:
			s.State = "quiet"
		}
		key := fmt.Sprintf("%d\x00%s", s.RuleID, s.Subject)
		if i, ok := index[key]; ok {
			if proxyAlertSubjectRank(s) < proxyAlertSubjectRank(out[i]) {
				s.Muted = s.Muted || out[i].Muted
				out[i] = s
			} else {
				out[i].Muted = out[i].Muted || s.Muted
			}
			continue
		}
		index[key] = len(out)
		out = append(out, s)
	}
	return out, rows.Err()
}

// proxyAlertSubjectRank puts a told level before a pending one and a pending
// one before a quiet one, the most severe first within each.
func proxyAlertSubjectRank(s proxyAlertSubject) int {
	state := map[string]int{"firing": 0, "pending": 1, "quiet": 2}[s.State]
	return state*1000 + proxyAlertSeverity(s.Level)
}

func unixTime(seconds int64) *time.Time {
	if seconds <= 0 {
		return nil
	}
	t := time.Unix(seconds, 0).UTC()
	return &t
}

func (a *proxyAlerts) History(ctx context.Context, limit int) ([]proxyAlertEvent, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT id, rule_id, kind, subject, level, label, event, detail, delivered, failed, muted, created_at FROM proxy_alert_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []proxyAlertEvent{}
	for rows.Next() {
		var e proxyAlertEvent
		var muted int
		var created int64
		if err := rows.Scan(&e.ID, &e.RuleID, &e.Kind, &e.Subject, &e.Level, &e.Label, &e.Event, &e.Detail, &e.Delivered, &e.Failed, &muted, &created); err != nil {
			return nil, err
		}
		e.Muted = muted == 1
		e.CreatedAt = time.Unix(created, 0).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- readings ---

func (s *Server) newProxyAlerts() *proxyAlerts {
	return &proxyAlerts{
		db:      s.Store.DB,
		observe: s.observeProxyAlerts,
		deliver: func(ctx context.Context, channelID int64, envelope deploy.NotificationEnvelope) error {
			return s.modules.deployAutomation.DeliverNotification(ctx, nil, channelID, envelope)
		},
		channels: s.modules.deployAutomation.ListNotificationChannels,
		url:      s.dashboardEndpoint,
		now:      time.Now,
		interval: proxyAlertInterval,
		log:      s.Log,
	}
}

// observeProxyAlerts takes each rule's reading from the sources the proxy
// pages read. Each source is read at most once a pass however many rules use
// it, and only when some rule does.
func (s *Server) observeProxyAlerts(ctx context.Context, rules []proxyAlertRule, now time.Time) map[int64]proxyAlertReading {
	uses := map[string]bool{}
	for _, rule := range rules {
		uses[rule.Kind] = true
	}
	var certs []proxysvc.Certificate
	certsRead := false
	if uses[proxyAlertCertExpiring] || uses[proxyAlertCertExpired] {
		var err error
		certs, err = s.modules.proxy.ListCertificates(ctx)
		certsRead = err == nil
	}
	var renewal *proxysvc.CertbotState
	if uses[proxyAlertRenewalFailed] {
		renewal = s.modules.proxy.CertbotState(ctx)
	}
	var drift *proxysvc.DriftReport
	if uses[proxyAlertServedDrift] {
		report := s.modules.proxyExtras.servedCerts.Report(ctx, false)
		drift = &report
	}
	var engine *proxyAlertReading
	if uses[proxyAlertEngineDown] {
		reading := s.readEngineAlert(ctx)
		engine = &reading
	}
	var upstreams *proxyAlertReading
	if uses[proxyAlertUpstreamDown] {
		reading := s.readUpstreamAlert(ctx)
		upstreams = &reading
	}
	var watched []watchedDomain
	watchedRead := false
	if uses[proxyAlertWatchUnreachable] || uses[proxyAlertWatchUntrusted] {
		watched, watchedRead = s.checkWatchedDomains(ctx)
	}

	out := map[int64]proxyAlertReading{}
	for _, rule := range rules {
		switch rule.Kind {
		case proxyAlertCertExpiring, proxyAlertCertExpired:
			if certsRead {
				out[rule.ID] = certificateAlertReading(rule, certs)
			}
		case proxyAlertRenewalFailed:
			out[rule.ID] = renewalAlertReading(renewal)
		case proxyAlertServedDrift:
			out[rule.ID] = servedDriftAlertReading(drift)
		case proxyAlertEngineDown:
			out[rule.ID] = *engine
		case proxyAlertUpstreamDown:
			out[rule.ID] = *upstreams
		case proxyAlertWatchUnreachable, proxyAlertWatchUntrusted:
			if watchedRead {
				out[rule.ID] = watchAlertReading(rule, watched)
			}
		case proxyAlertSiteErrors:
			out[rule.ID] = s.readSiteErrorAlert(ctx, rule, now)
		}
	}
	return out
}

func newProxyAlertReading() proxyAlertReading {
	return proxyAlertReading{Judged: true, Unknown: map[string]bool{}, Now: map[string]string{}}
}

func certificateAlertReading(rule proxyAlertRule, certs []proxysvc.Certificate) proxyAlertReading {
	reading := newProxyAlertReading()
	for _, cert := range certs {
		subject := cert.Path
		if subject == "" {
			subject = cert.Name
		}
		if cert.Error != "" || cert.NotAfter.IsZero() {
			// A file that could not be read is not a certificate that renewed.
			reading.Unknown[subject] = true
			continue
		}
		label := cert.Name
		if len(cert.Domains) > 0 && cert.Domains[0] != cert.Name {
			label += " (" + cert.Domains[0] + ")"
		}
		detail := certificateAlertDetail(cert)
		reading.Now[subject] = detail
		switch rule.Kind {
		case proxyAlertCertExpired:
			if cert.Expired {
				reading.Firing = append(reading.Firing, proxyAlertFinding{subject, proxyAlertFiringLevel, label, detail})
			}
		case proxyAlertCertExpiring:
			// An expired certificate stays past every threshold, so its
			// expiry rule neither re-fires nor reads as recovered when it
			// runs out; only a renewal brings it back.
			for _, days := range rule.Params.Days {
				if cert.Expired || cert.DaysLeft <= days {
					reading.Firing = append(reading.Firing, proxyAlertFinding{subject, fmt.Sprintf("%dd", days), label, detail})
				}
			}
		}
	}
	return reading
}

func certificateAlertDetail(cert proxysvc.Certificate) string {
	expiry := cert.NotAfter.UTC().Format("2006-01-02")
	var detail string
	switch {
	case cert.Expired:
		detail = fmt.Sprintf("Expired on %s.", expiry)
	case cert.DaysLeft == 1:
		detail = fmt.Sprintf("1 day left, expires %s.", expiry)
	default:
		detail = fmt.Sprintf("%d days left, expires %s.", cert.DaysLeft, expiry)
	}
	if len(cert.UsedBy) > 0 {
		detail += " Used by " + strings.Join(cert.UsedBy, ", ") + "."
	}
	return detail
}

func renewalAlertReading(state *proxysvc.CertbotState) proxyAlertReading {
	if state == nil || !state.Available || !state.AutoRenew || state.Health == nil ||
		state.Health.Error != "" || state.Health.State == "unknown" || state.Health.State == "running" ||
		state.Health.State == "never" {
		return proxyAlertReading{}
	}
	reading := newProxyAlertReading()
	const subject = "certbot renewal"
	health := state.Health
	if health.State == "failed" || len(health.HookFailures) > 0 {
		parts := []string{}
		for _, failure := range health.Failures {
			if !failure.RenewedSince {
				parts = append(parts, failure.Lineage+": "+failure.Reason)
			}
		}
		if health.Reason != "" {
			parts = append(parts, health.Reason)
		}
		for _, failure := range health.HookFailures {
			parts = append(parts, fmt.Sprintf("%s failed (exit %d)", failure.Kind, failure.Code))
		}
		if len(parts) == 0 {
			parts = append(parts, "the last renewal run failed")
		}
		reading.Firing = append(reading.Firing, proxyAlertFinding{
			Subject: subject, Level: proxyAlertFiringLevel, Label: "Certbot renewal failed", Detail: strings.Join(parts, "; "),
		})
	} else {
		reading.Now[subject] = "The latest renewal run succeeded or its failed certificates have since renewed."
	}
	return reading
}

func servedDriftAlertReading(report *proxysvc.DriftReport) proxyAlertReading {
	if report == nil {
		return proxyAlertReading{}
	}
	reading := newProxyAlertReading()
	for _, site := range report.Sites {
		subject := site.Site + " " + site.ServerName + " " + site.Address
		switch site.State {
		case proxysvc.DriftStale, proxysvc.DriftMismatch:
			reading.Firing = append(reading.Firing, proxyAlertFinding{
				Subject: subject, Level: proxyAlertFiringLevel, Label: site.Site + " (" + site.ServerName + ")",
				Detail: site.Reason,
			})
		case proxysvc.DriftOK:
			reading.Now[subject] = "The site serves the certificate named by its configuration."
		default:
			reading.Unknown[subject] = true
		}
	}
	return reading
}

// readEngineAlert asks systemd for the engine's unit. A host whose proxy is
// not a unit, or whose systemd cannot be asked, is not judged.
func (s *Server) readEngineAlert(ctx context.Context) proxyAlertReading {
	engine, err := s.modules.proxy.Engine()
	if err != nil {
		return proxyAlertReading{}
	}
	check, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	unit, _, err := s.modules.systemd.Show(check, engine.Unit)
	if err != nil || unit == nil || unit.LoadState == "not-found" || unit.ActiveState == "" {
		return proxyAlertReading{}
	}
	reading := newProxyAlertReading()
	label := fmt.Sprintf("%s (%s)", engine.Name, engine.Unit)
	switch unit.ActiveState {
	case "active", "reloading", "activating":
		reading.Now[engine.Unit] = fmt.Sprintf("%s is %s.", engine.Unit, unit.ActiveState)
	default:
		detail := fmt.Sprintf("%s is %s (%s); every site it serves is offline.", engine.Unit, unit.ActiveState, unit.SubState)
		if unit.Result != "" && unit.Result != "success" {
			detail += " systemd's result: " + unit.Result + "."
		}
		reading.Firing = append(reading.Firing, proxyAlertFinding{engine.Unit, proxyAlertFiringLevel, label, detail})
	}
	return reading
}

var proxyAlertUpstreamDownStates = map[proxysvc.UpstreamState]bool{
	proxysvc.UpstreamRefused: true, proxysvc.UpstreamTimeout: true, proxysvc.UpstreamUnresolvable: true,
	proxysvc.UpstreamMissing: true, proxysvc.UpstreamError: true,
}

// readUpstreamAlert reads the same check the overview shows. Only addresses
// nginx's own configuration names are dialled, never one a rule supplies.
func (s *Server) readUpstreamAlert(ctx context.Context) proxyAlertReading {
	check, cancel := context.WithTimeout(ctx, upstreamBudget)
	defer cancel()
	report, err := s.modules.proxyExtras.upstreams.Report(check, false)
	if err != nil || report == nil {
		return proxyAlertReading{}
	}
	reading := newProxyAlertReading()
	down := map[string]proxyAlertFinding{}
	for _, t := range report.Targets {
		if t.State == proxysvc.UpstreamDynamic {
			continue
		}
		subject := t.Site + " → " + t.Address
		if !proxyAlertUpstreamDownStates[t.State] {
			if _, ok := down[subject]; !ok {
				reading.Now[subject] = fmt.Sprintf("%s answers.", t.Address)
			}
			continue
		}
		detail := fmt.Sprintf("%s is %s", t.Address, t.State)
		if t.Detail != "" {
			detail += ": " + t.Detail
		}
		detail += fmt.Sprintf(" (%s in %s:%d).", t.Directive, t.File, t.Line)
		down[subject] = proxyAlertFinding{subject, proxyAlertFiringLevel, subject, detail}
		delete(reading.Now, subject)
	}
	for _, f := range down {
		reading.Firing = append(reading.Firing, f)
	}
	return reading
}

// checkWatchedDomains handshakes every watched endpoint once for the pass.
func (s *Server) checkWatchedDomains(ctx context.Context) ([]watchedDomain, bool) {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT id, domain, port FROM watched_domains ORDER BY domain`)
	if err != nil {
		return nil, false
	}
	domains := []watchedDomain{}
	for rows.Next() {
		var d watchedDomain
		if err := rows.Scan(&d.ID, &d.Domain, &d.Port); err != nil {
			rows.Close()
			return nil, false
		}
		domains = append(domains, d)
	}
	rows.Close()
	if rows.Err() != nil {
		return nil, false
	}
	check, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i := range domains {
		wg.Add(1)
		go func(d *watchedDomain) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if cert, err := proxysvc.CheckDomain(check, d.Domain, d.Port); err == nil {
				d.Cert = cert
			}
		}(&domains[i])
	}
	wg.Wait()
	return domains, true
}

func watchAlertReading(rule proxyAlertRule, domains []watchedDomain) proxyAlertReading {
	reading := newProxyAlertReading()
	for _, d := range domains {
		subject := fmt.Sprintf("%s:%d", d.Domain, d.Port)
		if d.Cert == nil {
			reading.Unknown[subject] = true
			continue
		}
		reached := !d.Cert.NotAfter.IsZero()
		switch {
		case rule.Kind == proxyAlertWatchUnreachable && !reached:
			reading.Firing = append(reading.Firing, proxyAlertFinding{subject, proxyAlertFiringLevel, subject,
				"No TLS handshake: " + d.Cert.Error})
		case rule.Kind == proxyAlertWatchUntrusted && reached && d.Cert.Error != "":
			reading.Firing = append(reading.Firing, proxyAlertFinding{subject, proxyAlertFiringLevel, subject,
				"The certificate it serves is not trusted: " + d.Cert.Error})
		case rule.Kind == proxyAlertWatchUntrusted && !reached:
			// Unreachable is the other rule's to tell; this one cannot say.
			reading.Unknown[subject] = true
		case reached:
			reading.Now[subject] = fmt.Sprintf("Answers with a trusted certificate, %d days left.", d.Cert.DaysLeft)
		}
	}
	return reading
}

// readSiteErrorAlert reads each nginx site's access log over the rule's
// window, through the same store and cache as the Traffic page.
func (s *Server) readSiteErrorAlert(ctx context.Context, rule proxyAlertRule, now time.Time) proxyAlertReading {
	reading := newProxyAlertReading()
	filter := accesslog.Filter{Since: now.Add(-time.Duration(rule.Params.Minutes) * time.Minute), Until: now, Limit: 1}
	for _, logs := range s.modules.proxy.AllSiteLogs() {
		if logs.Access == "" || ctx.Err() != nil {
			continue
		}
		window, err := s.modules.proxyExtras.siteTraffic.Cached(ctx, logs.Access, filter, time.Minute)
		if err != nil {
			reading.Unknown[logs.Site] = true
			continue
		}
		if !window.Coverage.Exists {
			continue
		}
		summary := window.Result.Summary
		rate := summary.ErrorRate * 100
		detail := fmt.Sprintf("%.1f%% of %d requests answered 5xx over %d min; the line is %.1f%%.",
			rate, summary.Total, rule.Params.Minutes, rule.Params.Threshold)
		label := logs.Site
		if len(logs.ServerNames) > 0 && logs.ServerNames[0] != logs.Site {
			label += " (" + logs.ServerNames[0] + ")"
		}
		if summary.Total >= rule.Params.MinRequests && rate > rule.Params.Threshold {
			reading.Firing = append(reading.Firing, proxyAlertFinding{logs.Site, proxyAlertFiringLevel, label, detail})
		} else {
			reading.Now[logs.Site] = detail
		}
	}
	return reading
}
