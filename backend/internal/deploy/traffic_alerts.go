package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

// Traffic alerts: the request record, watched.
//
// The Logs page answers "is it failing" for whoever is looking at it. Nobody
// is looking at it at four in the morning, which is when it matters. A rule
// here is a reading the evaluator takes every minute from the record the
// store already holds — the 5xx share, the p95, or whether anything arrived
// at all — over the last few minutes, and a channel it tells when the
// reading crosses the line. It tells the channel once on the way over and
// once on the way back; a rule that stays crossed for an hour is one message,
// not sixty.
//
// The channels are the ones deployments already announce themselves on.
// There is no second notification system here, only two more events those
// channels can carry.

const (
	// TrafficAlertErrorRate fires when the share of requests answered 5xx,
	// as a percentage, exceeds the threshold over the window.
	TrafficAlertErrorRate = "error_rate"
	// TrafficAlertLatency fires when the window's p95, in milliseconds,
	// exceeds the threshold.
	TrafficAlertLatency = "latency"
	// TrafficAlertSilence fires when a record that has held traffic holds
	// none for the window — the site went quiet, which for a site that is
	// normally busy is the loudest thing it can do.
	TrafficAlertSilence = "silence"

	trafficAlertStateOK     = "ok"
	trafficAlertStateFiring = "firing"
)

var TrafficAlertKinds = []string{TrafficAlertErrorRate, TrafficAlertLatency, TrafficAlertSilence}

type TrafficAlert struct {
	ID            int64   `json:"id"`
	ProjectID     int64   `json:"projectId"`
	EnvironmentID int64   `json:"environmentId"`
	Kind          string  `json:"kind"`
	Threshold     float64 `json:"threshold"`
	WindowMinutes int     `json:"windowMinutes"`
	// Channels names the notification channels the rule reaches. Empty
	// means every enabled channel.
	Channels []int64 `json:"channels"`
	Enabled  bool    `json:"enabled"`
	// State is what the rule last decided, and Observed what it saw when it
	// did — so the page can say "firing since 03:12, 4.2%" without waiting
	// for the next minute.
	State      string     `json:"state"`
	StateSince *time.Time `json:"stateSince,omitempty"`
	Observed   float64    `json:"observed"`
	CheckedAt  *time.Time `json:"checkedAt,omitempty"`
	FiredAt    *time.Time `json:"firedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

// TrafficAlertWrite is what a form submits.
type TrafficAlertWrite struct {
	Kind          string  `json:"kind"`
	Threshold     float64 `json:"threshold"`
	WindowMinutes int     `json:"windowMinutes"`
	Channels      []int64 `json:"channels"`
	Enabled       *bool   `json:"enabled,omitempty"`
}

var ErrTrafficAlertNotFound = errors.New("traffic alert not found")

func validateTrafficAlert(in TrafficAlertWrite) (TrafficAlertWrite, error) {
	in.Kind = strings.TrimSpace(strings.ToLower(in.Kind))
	switch in.Kind {
	case TrafficAlertErrorRate:
		if in.Threshold < 0.1 || in.Threshold > 100 {
			return in, invalidNotification("a 5xx rate limit is a percentage between 0.1 and 100")
		}
	case TrafficAlertLatency:
		if in.Threshold < 1 || in.Threshold > 600_000 {
			return in, invalidNotification("a p95 limit is between 1 ms and 10 minutes")
		}
	case TrafficAlertSilence:
		in.Threshold = 0
		if in.WindowMinutes < 5 {
			return in, invalidNotification("a silence alert needs a window of at least 5 minutes, or a quiet minute is an outage")
		}
	default:
		return in, invalidNotification("alert kind must be one of error_rate, latency or silence")
	}
	if in.WindowMinutes < 1 || in.WindowMinutes > 1440 {
		return in, invalidNotification("the window is between 1 minute and a day")
	}
	if in.Channels == nil {
		in.Channels = []int64{}
	}
	seen := map[int64]bool{}
	channels := make([]int64, 0, len(in.Channels))
	for _, id := range in.Channels {
		if id > 0 && !seen[id] {
			seen[id] = true
			channels = append(channels, id)
		}
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i] < channels[j] })
	in.Channels = channels
	return in, nil
}

func (s *AutomationStore) ListTrafficAlerts(ctx context.Context, projectID int64) ([]TrafficAlert, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+trafficAlertColumns+` FROM deploy_traffic_alerts WHERE project_id=? ORDER BY id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTrafficAlerts(rows)
}

// EnabledTrafficAlerts is every rule the evaluator has to take a reading for.
func (s *AutomationStore) EnabledTrafficAlerts(ctx context.Context) ([]TrafficAlert, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+trafficAlertColumns+` FROM deploy_traffic_alerts WHERE enabled=1 ORDER BY project_id, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTrafficAlerts(rows)
}

const trafficAlertColumns = `id, project_id, environment_id, kind, threshold, window_minutes, channels, enabled, state, state_since, observed, checked_at, fired_at, created_at, updated_at`

func scanTrafficAlerts(rows *sql.Rows) ([]TrafficAlert, error) {
	out := []TrafficAlert{}
	for rows.Next() {
		var a TrafficAlert
		var channels string
		var enabled int
		var since, checked, fired, created, updated int64
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.EnvironmentID, &a.Kind, &a.Threshold, &a.WindowMinutes, &channels, &enabled,
			&a.State, &since, &a.Observed, &checked, &fired, &created, &updated); err != nil {
			return nil, err
		}
		a.Enabled = enabled == 1
		a.Channels = []int64{}
		_ = json.Unmarshal([]byte(channels), &a.Channels)
		a.StateSince, a.CheckedAt, a.FiredAt = unixPointer(since), unixPointer(checked), unixPointer(fired)
		a.CreatedAt, a.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
		out = append(out, a)
	}
	return out, rows.Err()
}

func unixPointer(seconds int64) *time.Time {
	if seconds <= 0 {
		return nil
	}
	t := time.Unix(seconds, 0).UTC()
	return &t
}

func (s *AutomationStore) trafficAlert(ctx context.Context, projectID, id int64) (*TrafficAlert, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+trafficAlertColumns+` FROM deploy_traffic_alerts WHERE id=? AND project_id=?`, id, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list, err := scanTrafficAlerts(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrTrafficAlertNotFound
	}
	return &list[0], nil
}

// checkChannels refuses a rule naming a channel that does not exist, so a
// typo is a form error now rather than a silent alert later.
func (s *AutomationStore) checkChannels(ctx context.Context, ids []int64) error {
	for _, id := range ids {
		if _, err := s.notificationChannel(ctx, id); err != nil {
			return invalidNotification("notification channel %d does not exist", id)
		}
	}
	return nil
}

func (s *AutomationStore) CreateTrafficAlert(ctx context.Context, projectID, environmentID int64, in TrafficAlertWrite) (*TrafficAlert, error) {
	in, err := validateTrafficAlert(in)
	if err != nil {
		return nil, err
	}
	if err := s.checkChannels(ctx, in.Channels); err != nil {
		return nil, err
	}
	enabled := 1
	if in.Enabled != nil && !*in.Enabled {
		enabled = 0
	}
	channels, _ := json.Marshal(in.Channels)
	now := s.now().UTC().Unix()
	result, err := s.db.ExecContext(ctx, `INSERT INTO deploy_traffic_alerts (project_id, environment_id, kind, threshold, window_minutes, channels, enabled, state, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		projectID, environmentID, in.Kind, in.Threshold, in.WindowMinutes, string(channels), enabled, trafficAlertStateOK, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := result.LastInsertId()
	return s.trafficAlert(ctx, projectID, id)
}

func (s *AutomationStore) UpdateTrafficAlert(ctx context.Context, projectID, id int64, in TrafficAlertWrite) (*TrafficAlert, error) {
	current, err := s.trafficAlert(ctx, projectID, id)
	if err != nil {
		return nil, err
	}
	in, err = validateTrafficAlert(in)
	if err != nil {
		return nil, err
	}
	if err := s.checkChannels(ctx, in.Channels); err != nil {
		return nil, err
	}
	enabled := current.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	channels, _ := json.Marshal(in.Channels)
	// A rule that changed its terms starts over: the state it was in was a
	// verdict on the old ones.
	if _, err := s.db.ExecContext(ctx, `UPDATE deploy_traffic_alerts SET kind=?, threshold=?, window_minutes=?, channels=?, enabled=?, state=?, state_since=0, observed=0, updated_at=? WHERE id=? AND project_id=?`,
		in.Kind, in.Threshold, in.WindowMinutes, string(channels), boolToInt(enabled), trafficAlertStateOK, s.now().UTC().Unix(), id, projectID); err != nil {
		return nil, err
	}
	return s.trafficAlert(ctx, projectID, id)
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (s *AutomationStore) DeleteTrafficAlert(ctx context.Context, projectID, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM deploy_traffic_alerts WHERE id=? AND project_id=?`, id, projectID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrTrafficAlertNotFound
	}
	return nil
}

// recordTrafficAlertCheck writes what the evaluator decided. Detached from
// the caller's context for the same reason a delivery is: a verdict that was
// reached must be recorded even if the pass's deadline expired reaching it.
func (s *AutomationStore) recordTrafficAlertCheck(ctx context.Context, id int64, state string, observed float64, since time.Time, fired *time.Time) error {
	now := s.now().UTC()
	query := `UPDATE deploy_traffic_alerts SET state=?, observed=?, checked_at=?, state_since=? WHERE id=?`
	args := []any{state, observed, now.Unix(), since.Unix(), id}
	if fired != nil {
		query = `UPDATE deploy_traffic_alerts SET state=?, observed=?, checked_at=?, state_since=?, fired_at=? WHERE id=?`
		args = []any{state, observed, now.Unix(), since.Unix(), fired.Unix(), id}
	}
	_, err := s.db.ExecContext(context.WithoutCancel(ctx), query, args...)
	return err
}

// TrafficAlertDeliverer sends one envelope to one channel — the same delivery
// a deployment's own announcement takes.
type TrafficAlertDeliverer func(ctx context.Context, channelID int64, envelope NotificationEnvelope) error

// TrafficAlertNames supplies the words a message needs: the project's name,
// the environment's, and the page to open.
type TrafficAlertNames func(ctx context.Context, projectID, environmentID int64) (project, environment, url string)

// TrafficAlertEvaluator takes every enabled rule's reading once a minute.
type TrafficAlertEvaluator struct {
	store    *AutomationStore
	record   RequestRecord
	deliver  TrafficAlertDeliverer
	names    TrafficAlertNames
	now      func() time.Time
	interval time.Duration
	log      *slog.Logger
}

func NewTrafficAlertEvaluator(store *AutomationStore, record RequestRecord, deliver TrafficAlertDeliverer, names TrafficAlertNames, log *slog.Logger) *TrafficAlertEvaluator {
	return &TrafficAlertEvaluator{store: store, record: record, deliver: deliver, names: names, now: time.Now, interval: time.Minute, log: log}
}

// Start runs the evaluator until the context ends. A minute is the cadence:
// the windows are minutes long, and a reading taken more often than the
// record changes is the same reading.
func (e *TrafficAlertEvaluator) Start(ctx context.Context) {
	if e == nil || e.store == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(e.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			pass, cancel := context.WithTimeout(ctx, 50*time.Second)
			e.Evaluate(pass)
			cancel()
		}
	}()
}

// TrafficAlertOutcome is what one pass decided for one rule, for the tests
// and the log.
type TrafficAlertOutcome struct {
	Alert    TrafficAlert
	Observed float64
	Firing   bool
	Changed  bool
	Skipped  string
}

// Evaluate is one pass over every enabled rule.
func (e *TrafficAlertEvaluator) Evaluate(ctx context.Context) []TrafficAlertOutcome {
	alerts, err := e.store.EnabledTrafficAlerts(ctx)
	if err != nil {
		e.warn("traffic alerts unavailable", err)
		return nil
	}
	var outcomes []TrafficAlertOutcome
	for _, alert := range alerts {
		if ctx.Err() != nil {
			break
		}
		outcomes = append(outcomes, e.evaluateOne(ctx, alert))
	}
	return outcomes
}

func (e *TrafficAlertEvaluator) evaluateOne(ctx context.Context, alert TrafficAlert) TrafficAlertOutcome {
	outcome := TrafficAlertOutcome{Alert: alert}
	if e.record == nil {
		outcome.Skipped = "no request record"
		return outcome
	}
	now := e.now().UTC()
	window, err := e.record.Window(ctx, deploymentRouteName(alert.EnvironmentID), accesslog.Filter{
		Since: now.Add(-time.Duration(alert.WindowMinutes) * time.Minute), Until: now, Limit: 1,
	})
	if err != nil {
		outcome.Skipped = "record unreadable"
		e.warn("traffic alert could not read the record", err)
		return outcome
	}
	// A route with no record at all cannot be judged: a deployment that has
	// never been reached is not silent, it is new.
	if !window.Coverage.Exists {
		outcome.Skipped = "no record yet"
		return outcome
	}
	observed, firing, judged := judgeTrafficAlert(alert, window)
	if !judged {
		outcome.Skipped = "not enough to judge"
		return outcome
	}
	outcome.Observed, outcome.Firing = observed, firing
	state := trafficAlertStateOK
	if firing {
		state = trafficAlertStateFiring
	}
	if state == alert.State {
		// Same verdict as last minute; record the reading and say nothing.
		since := now
		if alert.StateSince != nil {
			since = *alert.StateSince
		}
		if err := e.store.recordTrafficAlertCheck(ctx, alert.ID, state, observed, since, nil); err != nil {
			e.warn("traffic alert check not recorded", err)
		}
		return outcome
	}
	outcome.Changed = true
	var fired *time.Time
	if firing {
		fired = &now
	}
	if err := e.store.recordTrafficAlertCheck(ctx, alert.ID, state, observed, now, fired); err != nil {
		e.warn("traffic alert check not recorded", err)
	}
	event := NotificationEventTrafficRecovered
	if firing {
		event = NotificationEventTrafficFiring
	}
	e.announce(ctx, alert, event, observed, now)
	return outcome
}

// judgeTrafficAlert takes the reading the rule is about. It answers whether it
// could judge at all: a latency rule on a record without durations, or an
// error-rate rule over a window with no requests, has nothing to say.
func judgeTrafficAlert(alert TrafficAlert, window accesslog.Window) (observed float64, firing, judged bool) {
	summary := window.Result.Summary
	switch alert.Kind {
	case TrafficAlertErrorRate:
		if summary.Total == 0 {
			return 0, false, true
		}
		observed = summary.ErrorRate * 100
		return observed, observed > alert.Threshold, true
	case TrafficAlertLatency:
		if summary.Latency == nil {
			return 0, false, false
		}
		observed = summary.Latency.P95
		return observed, observed > alert.Threshold, true
	case TrafficAlertSilence:
		// Silent means "was busy, is not": a record that holds nothing at all
		// is a deployment nobody has reached yet.
		if window.Coverage.Held == 0 {
			return 0, false, false
		}
		return float64(summary.Total), summary.Total == 0, true
	}
	return 0, false, false
}

// announce sends the transition to the rule's channels, or to every enabled
// channel when the rule names none.
func (e *TrafficAlertEvaluator) announce(ctx context.Context, alert TrafficAlert, event string, observed float64, since time.Time) {
	if e.deliver == nil {
		return
	}
	channels := alert.Channels
	if len(channels) == 0 {
		all, err := e.store.ListNotificationChannels(ctx)
		if err != nil {
			e.warn("notification channels unavailable", err)
			return
		}
		for _, channel := range all {
			if channel.Enabled {
				channels = append(channels, channel.ID)
			}
		}
	}
	envelope := e.envelope(ctx, alert, event, observed, since)
	for _, id := range channels {
		if err := e.deliver(ctx, id, envelope); err != nil {
			e.warn(fmt.Sprintf("traffic alert delivery failed for channel %d", id), err)
		}
	}
}

func (e *TrafficAlertEvaluator) envelope(ctx context.Context, alert TrafficAlert, event string, observed float64, since time.Time) NotificationEnvelope {
	envelope := NotificationEnvelope{
		Event: event, ProjectID: alert.ProjectID, EnvironmentID: alert.EnvironmentID,
		SentAt: e.now().UTC(),
		Alert: &TrafficAlertEnvelope{
			ID: alert.ID, Kind: alert.Kind, Threshold: alert.Threshold, Observed: observed,
			WindowMinutes: alert.WindowMinutes, Since: since,
		},
	}
	if e.names != nil {
		envelope.ProjectName, envelope.EnvironmentName, envelope.URL = e.names(ctx, alert.ProjectID, alert.EnvironmentID)
	}
	return envelope
}

// TestEnvelope is what "Send test" delivers: the rule as if it had just
// fired, at twice its own limit, so the message that arrives is the one that
// would.
func (e *TrafficAlertEvaluator) TestEnvelope(ctx context.Context, alert TrafficAlert) NotificationEnvelope {
	observed := alert.Threshold * 2
	if alert.Kind == TrafficAlertSilence {
		observed = 0
	}
	envelope := e.envelope(ctx, alert, NotificationEventTrafficFiring, observed, e.now().UTC())
	// A test is announced as one, in the title, so nobody wakes up for it.
	envelope.ProjectName = "[test] " + envelope.ProjectName
	return envelope
}

func (e *TrafficAlertEvaluator) warn(message string, err error) {
	if e.log != nil {
		e.log.Warn(message, "err", err)
	}
}
