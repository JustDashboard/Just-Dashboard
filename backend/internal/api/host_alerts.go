package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// Host alerts: the machine's own readings, watched while nobody reads them.
//
// A rule names one kind of trouble with the machine itself — its CPU or its
// memory held over a line, a filesystem filling, a container gone down, a
// backup job failed — and the channels to tell. Once a minute a pass takes
// the readings the dashboard already keeps (the metrics recorder's history,
// Docker's containers, the backup runs) and follows each subject the rule
// covers: it tells the channels once when a subject starts firing and once
// when it comes back, and says nothing in between. The channels are the
// deployments' notification channels; there is no second delivery system.
//
// It is the proxy alerts' shape (proxy_alerts.go) — rules, subjects, mutes,
// a bounded history — with the decision moved into one pure function,
// stepHostAlert, and a rule's revision guarding its state the way the
// database alerts' does.
const (
	hostAlertCPUHigh       = "cpu_high"
	hostAlertMemoryHigh    = "memory_high"
	hostAlertDiskFilling   = "disk_filling"
	hostAlertContainerDown = "container_down"
	hostAlertBackupFailed  = "backup_failed"
	// A live release whose image has a fixable vulnerability at or above
	// its environment's floor, built on a base whose tag now names a newer
	// image (deploy/image_scan.go).
	hostAlertImageVulnerable = "image_vulnerable"
	// hostAlertTunnelDown is the Cloudflare Tunnel's connector not running,
	// or running with no connection to Cloudflare's edge: every name routed
	// through it unreachable.
	hostAlertTunnelDown = "tunnel_down"

	// A container down for a minute is most of a minute nobody was told.
	// Every reading but Docker's is a query of what the store already holds.
	hostAlertInterval = time.Minute
	hostAlertHistory  = 500
)

var hostAlertKinds = []string{
	hostAlertCPUHigh, hostAlertMemoryHigh, hostAlertDiskFilling, hostAlertContainerDown, hostAlertBackupFailed,
	hostAlertImageVulnerable,
	hostAlertTunnelDown,
}

// hostAlertSpec is one kind's line and timing: the default and range of its
// percentage, of its minutes, and how many passes in a row must find a
// subject firing before it is told.
type hostAlertSpec struct {
	threshold, min, max             float64
	minutes, minMinutes, maxMinutes int
	passes                          int
	// hold says the minutes are how long a subject must keep firing before it
	// is told. CPU and memory read their minutes as the window they average
	// over instead, which already is the hold: a minute at 100% inside ten at
	// 40% is a spike, and the stored history is what says so.
	hold bool
}

var hostAlertSpecs = map[string]hostAlertSpec{
	// Netdata's stock CPU alarm averages ten minutes; memory is given five,
	// because the kernel's OOM killer does not wait ten.
	hostAlertCPUHigh:    {threshold: 90, min: 10, max: 100, minutes: 10, minMinutes: 1, maxMinutes: 1440, passes: 1},
	hostAlertMemoryHigh: {threshold: 90, min: 10, max: 100, minutes: 5, minMinutes: 1, maxMinutes: 1440, passes: 1},
	// A filesystem's fill is one reading, so two passes in a row keep a
	// temporary file written and removed between them from paging anyone.
	hostAlertDiskFilling: {threshold: 90, min: 10, max: 99, passes: 2},
	// Two passes and two minutes: a container a deploy is replacing, or one
	// its restart policy brings straight back, is not an outage.
	hostAlertContainerDown: {minutes: 2, minMinutes: 1, maxMinutes: 1440, passes: 2, hold: true},
	// A failed run is a fact; one reading of it is the whole of it.
	hostAlertBackupFailed: {passes: 1},
	// So is a scan: it is read once a day, and the next reading of it is
	// the next scan's.
	hostAlertImageVulnerable: {passes: 1},
	// A connector reconnects on its own after a blip of the edge, so two
	// minutes down, read twice, before anybody is told.
	hostAlertTunnelDown: {minutes: 2, minMinutes: 1, maxMinutes: 1440, passes: 2, hold: true},
}

// hostAlertClearLine is where a percentage must fall below before a firing
// subject recovers: five points under the line. Between the two, it stays
// where it was, so a disk that hovers at 90% is one alert and not one a
// minute.
func hostAlertClearLine(threshold float64) float64 {
	return math.Max(threshold-5, 0)
}

var (
	errHostAlertNotFound = errors.New("host alert rule not found")
	errHostAlertInvalid  = errors.New("invalid host alert rule")
)

func invalidHostAlert(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errHostAlertInvalid, fmt.Sprintf(format, args...))
}

// hostAlertParams holds every kind's parameters; each kind reads its own and
// validation clears the rest.
type hostAlertParams struct {
	// Threshold is the line, as a percentage.
	Threshold float64 `json:"threshold,omitempty"`
	// Minutes is CPU's and memory's window, or how long a container must stay
	// down before it is told.
	Minutes int `json:"minutes,omitempty"`
}

type hostAlertRule struct {
	ID     int64           `json:"id"`
	Kind   string          `json:"kind"`
	Params hostAlertParams `json:"params"`
	// Channels names the notification channels the rule tells; empty tells
	// every enabled one.
	Channels  []int64   `json:"channels"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// Unjudged says why the last pass could not judge the rule at all — the
	// metrics history is off, Docker did not answer — so a page can say it
	// rather than show a rule that looks quiet and is blind.
	Unjudged string `json:"unjudged,omitempty"`

	revision  int64
	watchFrom time.Time
}

type hostAlertWrite struct {
	Kind     string          `json:"kind"`
	Params   hostAlertParams `json:"params"`
	Channels []int64         `json:"channels"`
	Enabled  *bool           `json:"enabled,omitempty"`
}

func validateHostAlert(in hostAlertWrite) (hostAlertWrite, error) {
	in.Kind = strings.TrimSpace(strings.ToLower(in.Kind))
	spec, ok := hostAlertSpecs[in.Kind]
	if !ok {
		return in, invalidHostAlert("kind must be one of %s", strings.Join(hostAlertKinds, ", "))
	}
	out := hostAlertParams{}
	if spec.max > 0 {
		out.Threshold = in.Params.Threshold
		if out.Threshold == 0 {
			out.Threshold = spec.threshold
		}
		if math.IsNaN(out.Threshold) || out.Threshold < spec.min || out.Threshold > spec.max {
			return in, invalidHostAlert("the line is a percentage between %.0f and %.0f", spec.min, spec.max)
		}
	}
	if spec.maxMinutes > 0 {
		out.Minutes = in.Params.Minutes
		if out.Minutes == 0 {
			out.Minutes = spec.minutes
		}
		if out.Minutes < spec.minMinutes || out.Minutes > spec.maxMinutes {
			return in, invalidHostAlert("the minutes are between %d and %d", spec.minMinutes, spec.maxMinutes)
		}
	}
	in.Params = out
	in.Channels = uniquePositive(in.Channels)
	return in, nil
}

// --- the state machine -------------------------------------------------------

// hostAlertState is one subject's row of host_alert_state.
type hostAlertState struct {
	Subject, Label, Detail string
	Seen                   int
	FiringSince            int64
	NotifiedAt             int64
	Muted                  bool
}

type hostAlertVerdict int

const (
	// hostAlertUnknown keeps a subject where it was: its reading could not be
	// taken, or it sits between the line and the clearing line.
	hostAlertUnknown hostAlertVerdict = iota
	hostAlertOver
	hostAlertClear
)

type hostAlertTransition int

const (
	hostAlertQuiet hostAlertTransition = iota
	// hostAlertFired is a firing told now.
	hostAlertFired
	// hostAlertRecovered is the end of a told firing, told now.
	hostAlertRecovered
)

// stepHostAlert moves one subject on by one reading. It is the whole of the
// alert's behaviour: a subject is told once when it has been over the line
// for the kind's passes and minutes, once when it comes back, and never in
// between; one that comes back before it was told ends without a word.
func stepHostAlert(rule hostAlertRule, st hostAlertState, verdict hostAlertVerdict, label, detail string, now time.Time) (hostAlertState, hostAlertTransition) {
	at := now.Unix()
	switch verdict {
	case hostAlertOver:
		if label != "" {
			st.Label = label
		}
		st.Detail = detail
		if st.FiringSince == 0 {
			st.FiringSince, st.Seen, st.NotifiedAt = at, 0, 0
		}
		st.Seen++
		if st.NotifiedAt > 0 {
			return st, hostAlertQuiet
		}
		spec := hostAlertSpecs[rule.Kind]
		hold := time.Duration(0)
		if spec.hold {
			hold = time.Duration(rule.Params.Minutes) * time.Minute
		}
		if st.Seen < max(spec.passes, 1) || now.Sub(time.Unix(st.FiringSince, 0)) < hold {
			return st, hostAlertQuiet
		}
		st.NotifiedAt = at
		return st, hostAlertFired
	case hostAlertClear:
		if st.FiringSince == 0 {
			return st, hostAlertQuiet
		}
		told := st.NotifiedAt > 0
		st.FiringSince, st.NotifiedAt, st.Seen = 0, 0, 0
		if label != "" {
			st.Label = label
		}
		st.Detail = detail
		if told {
			return st, hostAlertRecovered
		}
	}
	return st, hostAlertQuiet
}

// --- readings ------------------------------------------------------------------

// hostAlertFinding is one subject a reading found over the line.
type hostAlertFinding struct {
	Subject, Label, Detail string
}

// hostAlertReading is what one pass saw for one rule. A rule that is not
// Judged keeps every subject where it was — a source that could not be read
// is not a recovery — and says Why. A subject in Unknown keeps its own state;
// one in Now is under its clearing line, with the reading its recovery
// message quotes.
type hostAlertReading struct {
	Judged  bool
	Why     string
	Firing  []hostAlertFinding
	Unknown map[string]bool
	Now     map[string]string
	// Labels names the subjects in Now, so a recovery says the name the
	// firing did.
	Labels map[string]string
}

func newHostAlertReading() hostAlertReading {
	return hostAlertReading{Judged: true, Unknown: map[string]bool{}, Now: map[string]string{}, Labels: map[string]string{}}
}

func unjudgedHostAlert(why string) hostAlertReading {
	return hostAlertReading{Why: why}
}

// hostAlertObserver takes every rule's reading at `now`; the server's reads
// the host's own sources, a test's returns what it is told to.
type hostAlertObserver func(ctx context.Context, rules []hostAlertRule, now time.Time) map[int64]hostAlertReading

// --- the evaluator -------------------------------------------------------------

type hostAlerts struct {
	db       *sql.DB
	observe  hostAlertObserver
	deliver  func(ctx context.Context, channelID int64, envelope deploy.NotificationEnvelope) error
	channels func(ctx context.Context) ([]deploy.NotificationChannel, error)
	url      func() string
	// server is the machine's name for the messages, since one channel often
	// hears from several dashboards.
	server   func(ctx context.Context) string
	now      func() time.Time
	interval time.Duration
	log      *slog.Logger

	// mu keeps a pass on demand and the scheduled one from both telling the
	// same transition, and guards the container memory below, which only a
	// pass touches. Rule edits do not take it; the rule's revision keeps a
	// pass from writing over an edit made while it was deciding.
	mu       sync.Mutex
	lastPass atomic.Int64
	// unjudged is why each rule could not be judged on the last pass.
	unjudged sync.Map
	// restarts is each running container's restart count as recent passes
	// read it, for telling a restart loop from one restart.
	restarts map[string][]hostRestartMark
	// exits is what each stopped container's last run left, read once while
	// it stays stopped rather than inspected every pass.
	exits map[string]hostCachedExit

	nameMu       sync.Mutex
	serverName   string
	serverNameAt time.Time
}

// Start runs a pass every interval until the context ends.
func (a *hostAlerts) Start(ctx context.Context) {
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

// LastPass is when the last pass finished; nil before the first.
func (a *hostAlerts) LastPass() *time.Time {
	return unixTime(a.lastPass.Load())
}

func (a *hostAlerts) warn(message string, err error) {
	if a.log != nil {
		a.log.Warn(message, "err", err)
	}
}

// machine is the server's name, asked at most every ten minutes.
func (a *hostAlerts) machine(ctx context.Context) string {
	if a.server == nil {
		return ""
	}
	a.nameMu.Lock()
	defer a.nameMu.Unlock()
	if a.serverName == "" || a.now().Sub(a.serverNameAt) > 10*time.Minute {
		if name := strings.TrimSpace(a.server(ctx)); name != "" {
			a.serverName, a.serverNameAt = name, a.now()
		}
	}
	return a.serverName
}

// Evaluate is one pass over every enabled rule.
func (a *hostAlerts) Evaluate(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	rules, err := a.Rules(ctx, true)
	if err != nil {
		a.warn("host alert rules unavailable", err)
		return
	}
	now := a.now().UTC()
	readings := map[int64]hostAlertReading{}
	if len(rules) > 0 {
		readings = a.observe(ctx, rules, now)
	}
	server := a.machine(ctx)
	for _, rule := range rules {
		if ctx.Err() != nil {
			break
		}
		reading, ok := readings[rule.ID]
		if !ok || !reading.Judged {
			why := reading.Why
			if why == "" {
				why = "The last pass ran out of time before it reached this rule."
			}
			a.unjudged.Store(rule.ID, why)
			continue
		}
		a.unjudged.Delete(rule.ID)
		if err := a.evaluateRule(ctx, rule, reading, now, server); err != nil {
			a.warn(fmt.Sprintf("host alert rule %d not evaluated", rule.ID), err)
		}
	}
	a.lastPass.Store(now.Unix())
	_, _ = a.db.ExecContext(context.WithoutCancel(ctx),
		`DELETE FROM host_alert_events WHERE id <= (SELECT id FROM host_alert_events ORDER BY id DESC LIMIT 1 OFFSET ?)`, hostAlertHistory)
}

type hostAlertTold struct {
	state  hostAlertState
	event  string
	detail string
	since  time.Time
}

func (a *hostAlerts) evaluateRule(ctx context.Context, rule hostAlertRule, reading hostAlertReading, now time.Time, server string) error {
	states, err := a.states(ctx, rule.ID)
	if err != nil {
		return err
	}
	// Detached: a transition that was decided must be written even when the
	// pass ran out of time deciding it, or it would be told twice.
	write := context.WithoutCancel(ctx)
	var told []hostAlertTold
	seen := map[string]bool{}
	step := func(subject string, verdict hostAlertVerdict, label, detail string) error {
		seen[subject] = true
		before, kept := states[subject]
		if !kept {
			if verdict != hostAlertOver {
				// A subject with no row is quiet; a quiet reading of it has
				// nothing to write.
				return nil
			}
			before = hostAlertState{Subject: subject}
		}
		after, transition := stepHostAlert(rule, before, verdict, label, detail, now)
		written, err := a.writeState(write, rule, after)
		if err != nil || !written {
			return err
		}
		switch transition {
		case hostAlertFired:
			told = append(told, hostAlertTold{after, deploy.NotificationEventHostFiring, after.Detail, time.Unix(after.FiringSince, 0).UTC()})
		case hostAlertRecovered:
			told = append(told, hostAlertTold{after, deploy.NotificationEventHostRecovered, after.Detail, now})
		}
		return nil
	}
	for _, f := range reading.Firing {
		if seen[f.Subject] {
			continue
		}
		if err := step(f.Subject, hostAlertOver, f.Label, f.Detail); err != nil {
			return err
		}
	}
	for _, subject := range sortedStrings(reading.Now) {
		if seen[subject] {
			continue
		}
		if err := step(subject, hostAlertClear, reading.Labels[subject], reading.Now[subject]); err != nil {
			return err
		}
	}
	for subject := range reading.Unknown {
		seen[subject] = true
	}
	// A subject the reading no longer finds at all — a container removed, a
	// filesystem unmounted, a backup job deleted — has nothing left to be
	// down about. One that was told firing is told it is over.
	for _, subject := range sortedStateKeys(states) {
		if seen[subject] {
			continue
		}
		if err := step(subject, hostAlertClear, "", "No longer found by this rule."); err != nil {
			return err
		}
	}
	for _, t := range told {
		a.announce(ctx, rule, t.state, t.event, t.detail, t.since, server, false)
	}
	return nil
}

func sortedStrings(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedStateKeys(m map[string]hostAlertState) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (a *hostAlerts) states(ctx context.Context, ruleID int64) (map[string]hostAlertState, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT subject, label, detail, seen, firing_since, notified_at, muted FROM host_alert_state WHERE rule_id=?`, ruleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]hostAlertState{}
	for rows.Next() {
		var st hostAlertState
		var muted int
		if err := rows.Scan(&st.Subject, &st.Label, &st.Detail, &st.Seen, &st.FiringSince, &st.NotifiedAt, &muted); err != nil {
			return nil, err
		}
		st.Muted = muted == 1
		out[st.Subject] = st
	}
	return out, rows.Err()
}

// writeState keeps a subject's state, unless the rule changed while the pass
// was deciding it: a verdict on the old terms is not kept, and is not told.
// A quiet subject nobody muted leaves the table, which is what keeps it from
// growing with every container name a host has ever had.
func (a *hostAlerts) writeState(ctx context.Context, rule hostAlertRule, st hostAlertState) (bool, error) {
	if st.FiringSince == 0 {
		res, err := a.db.ExecContext(ctx, `UPDATE host_alert_state SET label=?, detail=?, seen=0, firing_since=0, notified_at=0
			WHERE rule_id=? AND subject=? AND EXISTS (SELECT 1 FROM host_alert_rules WHERE id=? AND revision=? AND enabled=1)`,
			st.Label, st.Detail, rule.ID, st.Subject, rule.ID, rule.revision)
		if err != nil {
			return false, err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return false, nil
		}
		_, err = a.db.ExecContext(ctx, `DELETE FROM host_alert_state WHERE rule_id=? AND subject=? AND firing_since=0 AND muted=0`, rule.ID, st.Subject)
		return true, err
	}
	res, err := a.db.ExecContext(ctx, `INSERT INTO host_alert_state (rule_id, subject, label, detail, seen, firing_since, notified_at, muted)
		SELECT ?,?,?,?,?,?,?,0 FROM host_alert_rules WHERE id=? AND revision=? AND enabled=1
		ON CONFLICT(rule_id, subject) DO UPDATE SET label=excluded.label, detail=excluded.detail, seen=excluded.seen,
		  firing_since=excluded.firing_since, notified_at=excluded.notified_at`,
		rule.ID, st.Subject, st.Label, st.Detail, st.Seen, st.FiringSince, st.NotifiedAt, rule.ID, rule.revision)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// announce tells the rule's channels, unless the subject is muted, and keeps
// what happened either way for the history.
func (a *hostAlerts) announce(ctx context.Context, rule hostAlertRule, st hostAlertState, event, detail string, since time.Time, server string, test bool) (delivered, failed int) {
	var muted int
	if !test {
		_ = a.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(muted),0) FROM host_alert_state WHERE rule_id=? AND subject=?`, rule.ID, st.Subject).Scan(&muted)
	}
	if muted == 0 {
		envelope := a.envelope(rule, st, event, detail, since, server)
		envelope.Host.Test = test
		for _, id := range a.recipients(ctx, rule) {
			switch err := a.deliver(ctx, id, envelope); {
			case err == nil:
				delivered++
			case errors.Is(err, deploy.ErrHookDisabled):
				// A paused channel is told nothing, and that is not a failure.
			default:
				failed++
				a.warn(fmt.Sprintf("host alert delivery failed for channel %d", id), err)
			}
		}
	}
	if _, err := a.db.ExecContext(context.WithoutCancel(ctx), `INSERT INTO host_alert_events (rule_id, kind, subject, label, event, detail, delivered, failed, muted, test, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		rule.ID, rule.Kind, st.Subject, st.Label, event, detail, delivered, failed, muted, boolInt(test), a.now().UTC().Unix()); err != nil {
		a.warn("host alert event not recorded", err)
	}
	return delivered, failed
}

// recipients is the rule's channels, or every enabled channel when it names
// none.
func (a *hostAlerts) recipients(ctx context.Context, rule hostAlertRule) []int64 {
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

func (a *hostAlerts) envelope(rule hostAlertRule, st hostAlertState, event, detail string, since time.Time, server string) deploy.NotificationEnvelope {
	envelope := deploy.NotificationEnvelope{
		Event: event, SentAt: a.now().UTC(),
		Host: &deploy.HostAlertEnvelope{
			RuleID: rule.ID, Kind: rule.Kind, Subject: st.Subject, Label: st.Label, Server: server,
			Detail: detail, Threshold: rule.Params.Threshold, Since: since,
		},
	}
	if a.url != nil {
		if base := strings.TrimRight(strings.TrimSpace(a.url()), "/"); base != "" {
			envelope.URL = base + hostAlertPage(rule.Kind, st.Subject)
		}
	}
	return envelope
}

// hostAlertPage is where a message sends its reader: the metrics for the
// machine's readings, the container's own page, the backup job's.
func hostAlertPage(kind, subject string) string {
	switch kind {
	case hostAlertContainerDown:
		if subject != "" && subject != "test" {
			return "/docker/containers/" + url.PathEscape(subject)
		}
		return "/docker"
	case hostAlertBackupFailed:
		if id, ok := strings.CutPrefix(subject, "job:"); ok {
			return "/backups/" + id
		}
		return "/backups"
	case hostAlertImageVulnerable:
		// The project's overview carries the panel; which environment the
		// page reads is the reader's own choice, kept in their session.
		if project, _, ok := imageAlertSubjectIDs(subject); ok {
			return "/deploy/" + project + "#vulnerabilities"
		}
		return "/deploy"
	case hostAlertTunnelDown:
		return "/proxy/tunnel"
	}
	return "/metrics"
}

// hostAlertTestDetail is a kind's reading as a test message states it.
var hostAlertTestDetail = map[string]string{
	hostAlertCPUHigh:       "CPU averaged 96% over the last 10 min (peak 100%); the line is 90%.",
	hostAlertMemoryHigh:    "Memory averaged 94% over the last 5 min (peak 97%); the line is 90%.",
	hostAlertDiskFilling:   "93% of / is used (186.0 GB of 200.0 GB); the line is 90%.",
	hostAlertContainerDown: "It exited with code 1.",
	hostAlertBackupFailed:  "The run of nightly that started 2026-10-25 03:00 UTC failed: upload refused.",
	hostAlertImageVulnerable: "Release 12 has 1 critical and 2 high findings with a fix (zlib 1.2.12-r0, fixed in 1.2.12-r2), " +
		"and node:22-alpine now names a newer image than the one it was built on. Rebuild it from the project's overview.",
	hostAlertTunnelDown: "The connector holds no connection to Cloudflare's edge. cloudflared said: Unauthorized: Invalid tunnel secret.",
}

var hostAlertTestLabel = map[string]string{
	hostAlertDiskFilling:     "/",
	hostAlertContainerDown:   "example-api-1",
	hostAlertBackupFailed:    "nightly",
	hostAlertImageVulnerable: "shop · production",
	hostAlertTunnelDown:      "production-tunnel",
}

// testState is what a test message is about: the rule's kind as it would
// arrive, marked as a test so nobody acts on it.
func (a *hostAlerts) testState(kind, server string) hostAlertState {
	label := hostAlertTestLabel[kind]
	if label == "" {
		label = server
	}
	if label == "" {
		label = "this server"
	}
	return hostAlertState{Subject: "test", Label: label,
		Detail: hostAlertTestDetail[kind] + " This is a test from the host's Alerts page; nothing is wrong."}
}

// SendTest tells one rule's channels its message as it would arrive, and
// keeps it in the history marked as a test.
func (a *hostAlerts) SendTest(ctx context.Context, rule hostAlertRule) (delivered, failed int) {
	server := a.machine(ctx)
	st := a.testState(rule.Kind, server)
	return a.announce(ctx, rule, st, deploy.NotificationEventHostFiring, st.Detail, a.now().UTC(), server, true)
}

// TestEnvelope is what a channel's "Send test" delivers: a host alert as one
// would arrive, marked as a test.
func (a *hostAlerts) TestEnvelope(ctx context.Context) deploy.NotificationEnvelope {
	server := a.machine(ctx)
	rule := hostAlertRule{Kind: hostAlertDiskFilling, Params: hostAlertParams{Threshold: 90}}
	st := a.testState(rule.Kind, server)
	envelope := a.envelope(rule, st, deploy.NotificationEventHostFiring, st.Detail, a.now().UTC(), server)
	envelope.Host.Test = true
	return envelope
}

// --- rules ---------------------------------------------------------------------

const hostAlertRuleColumns = `id, kind, params, channels, enabled, revision, watch_from, created_at, updated_at`

func (a *hostAlerts) Rules(ctx context.Context, enabledOnly bool) ([]hostAlertRule, error) {
	query := `SELECT ` + hostAlertRuleColumns + ` FROM host_alert_rules`
	if enabledOnly {
		query += ` WHERE enabled=1`
	}
	rows, err := a.db.QueryContext(ctx, query+` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []hostAlertRule{}
	for rows.Next() {
		var rule hostAlertRule
		var params, channels string
		var enabled int
		var watchFrom, created, updated int64
		if err := rows.Scan(&rule.ID, &rule.Kind, &params, &channels, &enabled, &rule.revision, &watchFrom, &created, &updated); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(params), &rule.Params)
		rule.Channels = []int64{}
		_ = json.Unmarshal([]byte(channels), &rule.Channels)
		rule.Enabled = enabled == 1
		rule.watchFrom = time.Unix(watchFrom, 0).UTC()
		rule.CreatedAt, rule.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
		if why, ok := a.unjudged.Load(rule.ID); ok && rule.Enabled {
			rule.Unjudged = why.(string)
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}

func (a *hostAlerts) rule(ctx context.Context, id int64) (*hostAlertRule, error) {
	rules, err := a.Rules(ctx, false)
	if err != nil {
		return nil, err
	}
	for i := range rules {
		if rules[i].ID == id {
			return &rules[i], nil
		}
	}
	return nil, errHostAlertNotFound
}

// checkChannels refuses a rule naming a channel that does not exist, so a
// typo is a form error now rather than a silent alert later.
func (a *hostAlerts) checkChannels(ctx context.Context, ids []int64) error {
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
			return invalidHostAlert("notification channel %d does not exist", id)
		}
	}
	return nil
}

func (a *hostAlerts) CreateRule(ctx context.Context, in hostAlertWrite) (*hostAlertRule, error) {
	in, err := validateHostAlert(in)
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
	res, err := a.db.ExecContext(ctx, `INSERT INTO host_alert_rules (kind, params, channels, enabled, revision, watch_from, created_at, updated_at) VALUES (?,?,?,?,1,?,?,?)`,
		in.Kind, string(params), string(channels), boolInt(enabled), now, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return a.rule(ctx, id)
}

func (a *hostAlerts) UpdateRule(ctx context.Context, id int64, in hostAlertWrite) (*hostAlertRule, error) {
	current, err := a.rule(ctx, id)
	if err != nil {
		return nil, err
	}
	in, err = validateHostAlert(in)
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
	now := a.now().UTC().Unix()
	// A rule with new terms, or one paused or resumed, starts over: what it
	// was firing on was a verdict on the old terms, a paused rule follows
	// nothing, and a container that stopped while it was paused is not one
	// it saw go down. Mutes are the operator's and are kept. Its channels
	// alone change nothing it follows.
	restart := in.Kind != current.Kind || string(params) != mustJSON(current.Params) || enabled != current.Enabled
	watchFrom := current.watchFrom.Unix()
	if restart {
		watchFrom = now
	}
	if _, err := a.db.ExecContext(ctx, `UPDATE host_alert_rules SET kind=?, params=?, channels=?, enabled=?, revision=revision+1, watch_from=?, updated_at=? WHERE id=?`,
		in.Kind, string(params), string(channels), boolInt(enabled), watchFrom, now, id); err != nil {
		return nil, err
	}
	if restart {
		if _, err := a.db.ExecContext(ctx, `DELETE FROM host_alert_state WHERE rule_id=? AND muted=0`, id); err != nil {
			return nil, err
		}
		if _, err := a.db.ExecContext(ctx, `UPDATE host_alert_state SET seen=0, firing_since=0, notified_at=0 WHERE rule_id=?`, id); err != nil {
			return nil, err
		}
		a.unjudged.Delete(id)
	}
	return a.rule(ctx, id)
}

func (a *hostAlerts) DeleteRule(ctx context.Context, id int64) (*hostAlertRule, error) {
	current, err := a.rule(ctx, id)
	if err != nil {
		return nil, err
	}
	// The state goes with the rule (ON DELETE CASCADE); the history stays.
	if _, err := a.db.ExecContext(ctx, `DELETE FROM host_alert_rules WHERE id=?`, id); err != nil {
		return nil, err
	}
	a.unjudged.Delete(id)
	return current, nil
}

// SetMuted mutes or unmutes one subject of one rule. A subject can be muted
// only once a rule has followed it, which is when it has a row.
func (a *hostAlerts) SetMuted(ctx context.Context, ruleID int64, subject string, muted bool) error {
	res, err := a.db.ExecContext(ctx, `UPDATE host_alert_state SET muted=? WHERE rule_id=? AND subject=?`, boolInt(muted), ruleID, subject)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errHostAlertNotFound
	}
	// A quiet subject that is unmuted has nothing left to keep.
	if !muted {
		_, err = a.db.ExecContext(ctx, `DELETE FROM host_alert_state WHERE rule_id=? AND subject=? AND firing_since=0`, ruleID, subject)
	}
	return err
}

// hostAlertSubject is one subject a rule follows, as the page lists it.
type hostAlertSubject struct {
	RuleID  int64  `json:"ruleId"`
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	Label   string `json:"label"`
	Detail  string `json:"detail"`
	// State is "pending" while it holds before it is told, "firing" once
	// told, and "quiet" for a muted subject that is fine.
	State      string     `json:"state"`
	Since      *time.Time `json:"since,omitempty"`
	NotifiedAt *time.Time `json:"notifiedAt,omitempty"`
	Muted      bool       `json:"muted"`
}

// Subjects lists every subject that is firing, holding or muted.
func (a *hostAlerts) Subjects(ctx context.Context) ([]hostAlertSubject, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT s.rule_id, r.kind, s.subject, s.label, s.detail, s.firing_since, s.notified_at, s.muted
		FROM host_alert_state s JOIN host_alert_rules r ON r.id = s.rule_id
		WHERE s.firing_since > 0 OR s.muted = 1 ORDER BY s.rule_id, s.subject`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []hostAlertSubject{}
	for rows.Next() {
		var s hostAlertSubject
		var since, notified int64
		var muted int
		if err := rows.Scan(&s.RuleID, &s.Kind, &s.Subject, &s.Label, &s.Detail, &since, &notified, &muted); err != nil {
			return nil, err
		}
		s.Muted = muted == 1
		s.Since, s.NotifiedAt = unixTime(since), unixTime(notified)
		switch {
		case notified > 0:
			s.State = "firing"
		case since > 0:
			s.State = "pending"
		default:
			s.State = "quiet"
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

type hostAlertEvent struct {
	ID        int64     `json:"id"`
	RuleID    int64     `json:"ruleId"`
	Kind      string    `json:"kind"`
	Subject   string    `json:"subject"`
	Label     string    `json:"label"`
	Event     string    `json:"event"`
	Detail    string    `json:"detail"`
	Delivered int       `json:"delivered"`
	Failed    int       `json:"failed"`
	Muted     bool      `json:"muted"`
	Test      bool      `json:"test"`
	CreatedAt time.Time `json:"createdAt"`
}

func (a *hostAlerts) History(ctx context.Context, limit int) ([]hostAlertEvent, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT id, rule_id, kind, subject, label, event, detail, delivered, failed, muted, test, created_at FROM host_alert_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []hostAlertEvent{}
	for rows.Next() {
		var e hostAlertEvent
		var muted, test int
		var created int64
		if err := rows.Scan(&e.ID, &e.RuleID, &e.Kind, &e.Subject, &e.Label, &e.Event, &e.Detail, &e.Delivered, &e.Failed, &muted, &test, &created); err != nil {
			return nil, err
		}
		e.Muted, e.Test = muted == 1, test == 1
		e.CreatedAt = time.Unix(created, 0).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}
