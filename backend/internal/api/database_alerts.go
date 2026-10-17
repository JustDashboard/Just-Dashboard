package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// Database alerts: the recorder's readings, watched while nobody reads them.
//
// A rule names a database, or every database, and one kind of trouble — the
// server not answering, its disk filling, its connections running out, its
// replication falling behind, a backup that failed or was missed — with a
// line where the kind has one and the channels to tell. The recorder that
// keeps each server's activity every thirty seconds (database_metrics.go)
// hands its readings here at the end of each pass, and the backup jobs are
// read from the store in the same pass: there is no second poll of anything.
//
// A subject is told once when it crosses its line and has stayed there for
// the rule's minutes, and once when it is back under the clearing line for
// two passes in a row, which is a little under the line itself: a disk that
// hovers at the line is one alert, not one every minute. The cooldown keeps
// a subject that fires again soon after it was told from being told again
// until the cooldown is over, and tells it then if it is still firing. The
// channels are the deployments' notification channels; there is no second
// delivery system here.
const (
	dbAlertHistory = 1000
	// dbAlertMaxCooldown is a week: longer, and a rule is better paused.
	dbAlertMaxCooldown     = 7 * 24 * 60
	dbAlertDefaultCooldown = 30
)

// dbAlertSpec is one kind's line and timing: the default and range of its
// threshold, of its minutes, and how many readings in a row decide it.
type dbAlertSpec struct {
	threshold, min, max             float64
	minutes, minMinutes, maxMinutes int
	// passes is how many readings in a row must be over the line before it
	// fires and under its clearing line before it recovers. A server reading
	// takes two, so one slow answer is not an outage; a backup run is a
	// fact, and one reading of it is the whole of it.
	passes int
}

var dbAlertSpecs = map[string]dbAlertSpec{
	dbx.AlertDown:         {minutes: 2, minMinutes: 1, maxMinutes: 1440, passes: 2},
	dbx.AlertDisk:         {threshold: 90, min: 10, max: 99, minutes: 5, maxMinutes: 1440, passes: 2},
	dbx.AlertConnections:  {threshold: 80, min: 10, max: 100, minutes: 5, maxMinutes: 1440, passes: 2},
	dbx.AlertReplication:  {threshold: 60, min: 1, max: 86400, minutes: 2, maxMinutes: 1440, passes: 2},
	dbx.AlertBackupFailed: {passes: 1},
	dbx.AlertBackupMissed: {minutes: 60, minMinutes: 5, maxMinutes: 7 * 24 * 60, passes: 1},
}

// dbAlertClearLine is where a firing subject has to fall to below before it
// recovers: five points under a percentage, a fifth under a lag. Under the
// line and over this one, it is still firing.
func dbAlertClearLine(kind string, threshold float64) float64 {
	switch kind {
	case dbx.AlertDisk, dbx.AlertConnections:
		return math.Max(threshold-5, 0)
	case dbx.AlertReplication:
		return threshold * 0.8
	}
	return threshold
}

var (
	errDBAlertNotFound = errors.New("database alert rule not found")
	errDBAlertInvalid  = errors.New("invalid database alert rule")
)

func invalidDBAlert(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errDBAlertInvalid, fmt.Sprintf(format, args...))
}

type dbAlertRule struct {
	ID int64 `json:"id"`
	// ConnectionID is the database the rule watches; null watches every
	// database whose engine has the kind.
	ConnectionID    *int64  `json:"connectionId"`
	Kind            string  `json:"kind"`
	Threshold       float64 `json:"threshold"`
	Minutes         int     `json:"minutes"`
	CooldownMinutes int     `json:"cooldownMinutes"`
	NotifyRecovery  bool    `json:"notifyRecovery"`
	// Channels names the notification channels the rule tells; empty tells
	// every enabled one.
	Channels  []int64   `json:"channels"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	revision  int64
}

// dbAlertWrite is what a form sends. An absent figure takes the kind's
// default, so a rule can be made from its kind alone.
type dbAlertWrite struct {
	ConnectionID    *int64   `json:"connectionId"`
	Kind            string   `json:"kind"`
	Threshold       *float64 `json:"threshold,omitempty"`
	Minutes         *int     `json:"minutes,omitempty"`
	CooldownMinutes *int     `json:"cooldownMinutes,omitempty"`
	NotifyRecovery  *bool    `json:"notifyRecovery,omitempty"`
	Channels        []int64  `json:"channels"`
	Enabled         *bool    `json:"enabled,omitempty"`
}

func validateDBAlert(in dbAlertWrite) (dbAlertRule, error) {
	kind := strings.TrimSpace(strings.ToLower(in.Kind))
	spec, ok := dbAlertSpecs[kind]
	if !ok {
		return dbAlertRule{}, invalidDBAlert("kind must be one of %s", strings.Join(dbx.AlertKindOrder, ", "))
	}
	out := dbAlertRule{
		Kind: kind, Threshold: spec.threshold, Minutes: spec.minutes, CooldownMinutes: dbAlertDefaultCooldown,
		NotifyRecovery: true, Channels: uniquePositive(in.Channels), Enabled: in.Enabled == nil || *in.Enabled,
	}
	if in.ConnectionID != nil {
		if *in.ConnectionID <= 0 {
			return out, invalidDBAlert("connectionId names a saved database, or is null for every database")
		}
		id := *in.ConnectionID
		out.ConnectionID = &id
	}
	if spec.max > 0 {
		if in.Threshold != nil {
			out.Threshold = *in.Threshold
		}
		if math.IsNaN(out.Threshold) || out.Threshold < spec.min || out.Threshold > spec.max {
			return out, invalidDBAlert("the line is between %s and %s", dbAlertFigure(kind, spec.min), dbAlertFigure(kind, spec.max))
		}
	} else {
		out.Threshold = 0
	}
	if spec.maxMinutes > 0 {
		if in.Minutes != nil {
			out.Minutes = *in.Minutes
		}
		if out.Minutes < spec.minMinutes || out.Minutes > spec.maxMinutes {
			return out, invalidDBAlert("the minutes are between %d and %d", spec.minMinutes, spec.maxMinutes)
		}
	} else {
		out.Minutes = 0
	}
	if in.CooldownMinutes != nil {
		out.CooldownMinutes = *in.CooldownMinutes
	}
	if out.CooldownMinutes < 0 || out.CooldownMinutes > dbAlertMaxCooldown {
		return out, invalidDBAlert("the cooldown is between 0 and %d minutes", dbAlertMaxCooldown)
	}
	if in.NotifyRecovery != nil {
		out.NotifyRecovery = *in.NotifyRecovery
	}
	return out, nil
}

// dbAlertFigure is a line in its kind's unit.
func dbAlertFigure(kind string, value float64) string {
	figure := strconv.FormatFloat(value, 'f', -1, 64)
	switch kind {
	case dbx.AlertDisk, dbx.AlertConnections:
		return figure + "%"
	case dbx.AlertReplication:
		return figure + " s"
	}
	return figure
}

// --- the evaluator -------------------------------------------------------------

type dbAlerts struct {
	db       *sql.DB
	deliver  func(ctx context.Context, channelID int64, envelope deploy.NotificationEnvelope) error
	channels func(ctx context.Context) ([]deploy.NotificationChannel, error)
	url      func() string
	now      func() time.Time
	log      *slog.Logger

	// mu keeps two passes from telling the same transition. A pass is the
	// recorder's, which runs one at a time; a rule edit does not take it,
	// because a pass can wait on a slow channel, and is instead kept from
	// being overwritten by the rule's revision.
	mu       sync.Mutex
	lastPass atomic.Int64
	// places is where each connection's data is on this machine, as last
	// found, for the disk reading (database_alert_readings.go).
	places sync.Map
}

func (s *Server) newDBAlerts() *dbAlerts {
	return &dbAlerts{
		db: s.Store.DB,
		deliver: func(ctx context.Context, channelID int64, envelope deploy.NotificationEnvelope) error {
			return s.modules.deployAutomation.DeliverNotification(ctx, nil, channelID, envelope)
		},
		channels: s.modules.deployAutomation.ListNotificationChannels,
		url:      s.dashboardEndpoint,
		now:      time.Now,
		log:      s.Log,
	}
}

func (a *dbAlerts) warn(message string, err error) {
	if a.log != nil {
		a.log.Warn(message, "err", err)
	}
}

// LastPass is when the last pass finished; nil before the first.
func (a *dbAlerts) LastPass() *time.Time {
	return unixTime(a.lastPass.Load())
}

// dbAlertConn is a saved connection as a pass judges it.
type dbAlertConn struct {
	ID           int64
	Name, Flavor string
	Driver       dbx.Driver
}

// covers reports whether a rule watches a database: its own, or every one
// whose engine has the kind.
func (r dbAlertRule) covers(conn dbAlertConn) bool {
	if r.ConnectionID != nil && *r.ConnectionID != conn.ID {
		return false
	}
	for _, kind := range dbx.AlertKinds(conn.Driver, conn.Flavor) {
		if kind == r.Kind {
			return true
		}
	}
	return false
}

// dbAlertObservation is what the recorder's pass read of one server, and
// what it was asked to read beyond its snapshot because a rule watches it.
type dbAlertObservation struct {
	Conn dbAlertConn
	// Unread says why the saved connection itself could not be used; nothing
	// is judged about a server the dashboard cannot even address.
	Unread string
	// Error is why the server did not answer; empty when it did.
	Error    string
	Answered bool
	// Sessions is nil where the snapshot did not count them against a limit.
	Sessions *dbAlertSessions
	Disk     *dbAlertDisk
	// DiskUnknown and ReplicationUnknown say why a reading that was asked
	// for could not be taken.
	DiskUnknown        string
	Replication        *dbx.ReplicationLag
	ReplicationUnknown string
}

type dbAlertSessions struct {
	Held, Limit, Reserved int
}

type dbAlertDisk struct {
	Used, Total int64
	Where       string
}

// dbAlertReading is one subject of one rule as a pass saw it: over the line,
// under its clearing line, neither — between the two, where a firing subject
// stays firing — or not judged at all.
type dbAlertReading struct {
	Subject      string
	ConnectionID *int64
	JobID        int64
	Label        string
	Value        float64
	Detail       string
	Over, Clear  bool
	// Unknown is why the subject could not be judged; it keeps whatever
	// state it had, because an unreadable disk is not one that emptied.
	Unknown string
}

// dbAlertState is a subject's row of db_alert_state.
type dbAlertState struct {
	Subject      string
	ConnectionID *int64
	JobID        int64
	Label        string
	Reading      float64
	Detail       string
	Unknown      string
	Seen, Clear  int
	Since        int64
	FiredAt      int64
	NotifiedAt   int64
	ToldAt       int64
	CheckedAt    int64
}

type dbAlertTransition int

const (
	dbAlertQuiet dbAlertTransition = iota
	// dbAlertFired is a firing told now.
	dbAlertFired
	// dbAlertHeld is a firing the cooldown keeps back.
	dbAlertHeld
	// dbAlertRecovered is a recovery told now.
	dbAlertRecovered
	// dbAlertEnded is a recovery nobody is told of: its firing was kept back
	// by the cooldown, or the rule does not tell recoveries.
	dbAlertEnded
)

// stepDBAlert moves one subject on by one reading. It is the whole of the
// alert's behaviour — hold, hysteresis and cooldown — and touches nothing
// but the state it is handed.
func stepDBAlert(rule dbAlertRule, st dbAlertState, r dbAlertReading, now time.Time) (dbAlertState, dbAlertTransition) {
	spec := dbAlertSpecs[rule.Kind]
	passes := max(spec.passes, 1)
	at := now.Unix()
	st.Label, st.CheckedAt = r.Label, at
	if r.Unknown != "" {
		st.Unknown = r.Unknown
		return st, dbAlertQuiet
	}
	st.Unknown, st.Reading, st.Detail = "", r.Value, r.Detail
	cooled := func() bool {
		return st.ToldAt == 0 || now.Sub(time.Unix(st.ToldAt, 0)) >= time.Duration(rule.CooldownMinutes)*time.Minute
	}
	if r.Over {
		st.Clear = 0
		if st.Since == 0 {
			st.Since, st.Seen = at, 0
		}
		st.Seen++
		switch {
		case st.FiredAt == 0:
			held := time.Duration(rule.Minutes) * time.Minute
			if rule.Kind == dbx.AlertBackupMissed {
				// Its minutes are the grace after a run was due, which the
				// reading has already waited out.
				held = 0
			}
			if st.Seen < passes || now.Sub(time.Unix(st.Since, 0)) < held {
				return st, dbAlertQuiet
			}
			st.FiredAt = at
			if !cooled() {
				return st, dbAlertHeld
			}
			st.NotifiedAt, st.ToldAt = at, at
			return st, dbAlertFired
		case st.NotifiedAt == 0 && cooled():
			// Kept back by the cooldown and still firing now it is over.
			st.NotifiedAt, st.ToldAt = at, at
			return st, dbAlertFired
		}
		return st, dbAlertQuiet
	}
	st.Seen = 0
	if st.FiredAt == 0 {
		// Never held long enough to be an alert: it ends without a word.
		st.Since, st.Clear = 0, 0
		return st, dbAlertQuiet
	}
	if !r.Clear {
		// Under the line and over its clearing line: still firing.
		st.Clear = 0
		return st, dbAlertQuiet
	}
	st.Clear++
	if st.Clear < passes {
		return st, dbAlertQuiet
	}
	told := st.NotifiedAt > 0
	st.Since, st.FiredAt, st.NotifiedAt, st.Clear = 0, 0, 0, 0
	if told && rule.NotifyRecovery {
		return st, dbAlertRecovered
	}
	return st, dbAlertEnded
}

// Evaluate is one pass over every enabled rule, with what the recorder read
// of each server in `observed` and the connections it set out to read in
// `conns`. A connection it did not reach is not judged.
func (a *dbAlerts) Evaluate(ctx context.Context, conns []dbAlertConn, observed map[int64]dbAlertObservation) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now().UTC()
	defer func() { a.lastPass.Store(now.Unix()) }()
	rules, err := a.Rules(ctx, nil, true)
	if err != nil {
		a.warn("database alert rules unavailable", err)
		return
	}
	if len(rules) == 0 {
		return
	}
	var jobs []dbAlertJob
	for _, rule := range rules {
		if rule.Kind == dbx.AlertBackupFailed || rule.Kind == dbx.AlertBackupMissed {
			if jobs, err = a.backupJobs(ctx); err != nil {
				a.warn("backup jobs unavailable to the database alerts", err)
			}
			break
		}
	}
	states, err := a.states(ctx)
	if err != nil {
		a.warn("database alert state unavailable", err)
		return
	}
	type told struct {
		rule       dbAlertRule
		state      dbAlertState
		transition dbAlertTransition
		since      time.Time
	}
	var transitions []told
	write := context.WithoutCancel(ctx)
	tx, err := a.db.BeginTx(write, nil)
	if err != nil {
		a.warn("database alert state not written", err)
		return
	}
	defer tx.Rollback()
	for _, rule := range rules {
		kept := states[rule.ID]
		readings, judged := a.readings(rule, conns, observed, jobs, now)
		seen := map[string]bool{}
		for _, reading := range readings {
			seen[reading.Subject] = true
			before := kept[reading.Subject]
			if before == nil {
				before = &dbAlertState{Subject: reading.Subject, ConnectionID: reading.ConnectionID, JobID: reading.JobID}
			}
			since := time.Unix(before.Since, 0).UTC()
			after, transition := stepDBAlert(rule, *before, reading, now)
			if after.Since > 0 {
				since = time.Unix(after.Since, 0).UTC()
			}
			written, err := upsertDBAlertState(write, tx, rule, after)
			if err != nil {
				a.warn(fmt.Sprintf("database alert rule %d not evaluated", rule.ID), err)
				return
			}
			if written && transition != dbAlertQuiet {
				if transition == dbAlertRecovered || transition == dbAlertEnded {
					since = now
				}
				transitions = append(transitions, told{rule, after, transition, since})
			}
		}
		if !judged {
			continue
		}
		// A subject the rule no longer reaches — a job that stopped dumping
		// the database, a database whose engine turned out to lack the kind —
		// leaves. One that was told firing is told it is over.
		for subject, st := range kept {
			if seen[subject] {
				continue
			}
			if _, err := tx.ExecContext(write, `DELETE FROM db_alert_state WHERE rule_id=? AND subject=?`, rule.ID, subject); err != nil {
				a.warn("database alert state not pruned", err)
				return
			}
			if st.NotifiedAt > 0 {
				gone := *st
				gone.Detail = "No longer watched by this rule."
				transition := dbAlertEnded
				if rule.NotifyRecovery {
					transition = dbAlertRecovered
				}
				transitions = append(transitions, told{rule, gone, transition, now})
			}
		}
	}
	if err := tx.Commit(); err != nil {
		a.warn("database alert state not written", err)
		return
	}
	for _, t := range transitions {
		a.announce(ctx, t.rule, t.state, t.transition, t.since)
	}
	if len(transitions) > 0 {
		if _, err := a.db.ExecContext(write,
			`DELETE FROM db_alert_events WHERE id <= (SELECT id FROM db_alert_events ORDER BY id DESC LIMIT 1 OFFSET ?)`, dbAlertHistory); err != nil {
			a.warn("database alert history not pruned", err)
		}
	}
}

// upsertDBAlertState writes a subject's state, unless the rule changed while
// the pass was deciding it: a verdict on the old terms is not kept, and is
// not told.
func upsertDBAlertState(ctx context.Context, tx *sql.Tx, rule dbAlertRule, st dbAlertState) (bool, error) {
	res, err := tx.ExecContext(ctx, `INSERT INTO db_alert_state
		(rule_id, subject, connection_id, job_id, label, reading, detail, unknown, seen, clear, since, fired_at, notified_at, told_at, checked_at)
		SELECT ?,?,?,?,?,?,?,?,?,?,?,?,?,?,? FROM db_alert_rules WHERE id=? AND revision=? AND enabled=1
		  AND (? IS NULL OR EXISTS (SELECT 1 FROM db_connections WHERE id=?))
		ON CONFLICT(rule_id, subject) DO UPDATE SET label=excluded.label, reading=excluded.reading, detail=excluded.detail,
		  unknown=excluded.unknown, seen=excluded.seen, clear=excluded.clear, since=excluded.since, fired_at=excluded.fired_at,
		  notified_at=excluded.notified_at, told_at=excluded.told_at, checked_at=excluded.checked_at`,
		rule.ID, st.Subject, st.ConnectionID, st.JobID, st.Label, st.Reading, st.Detail, st.Unknown, st.Seen, st.Clear,
		st.Since, st.FiredAt, st.NotifiedAt, st.ToldAt, st.CheckedAt, rule.ID, rule.revision, st.ConnectionID, st.ConnectionID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (a *dbAlerts) states(ctx context.Context) (map[int64]map[string]*dbAlertState, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT rule_id, subject, connection_id, job_id, label, reading, detail, unknown,
		seen, clear, since, fired_at, notified_at, told_at, checked_at FROM db_alert_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[string]*dbAlertState{}
	for rows.Next() {
		var ruleID int64
		var conn sql.NullInt64
		st := &dbAlertState{}
		if err := rows.Scan(&ruleID, &st.Subject, &conn, &st.JobID, &st.Label, &st.Reading, &st.Detail, &st.Unknown,
			&st.Seen, &st.Clear, &st.Since, &st.FiredAt, &st.NotifiedAt, &st.ToldAt, &st.CheckedAt); err != nil {
			return nil, err
		}
		st.ConnectionID = nullID(conn)
		if out[ruleID] == nil {
			out[ruleID] = map[string]*dbAlertState{}
		}
		out[ruleID][st.Subject] = st
	}
	return out, rows.Err()
}

func nullID(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	id := v.Int64
	return &id
}

// --- readings ------------------------------------------------------------------

func dbAlertConnSubject(id int64) string { return "connection:" + strconv.FormatInt(id, 10) }

func dbAlertJobSubject(id int64) string { return "job:" + strconv.FormatInt(id, 10) }

// readings is every subject a rule watches, as this pass saw it. judged is
// false when the pass could not say which subjects there are at all — the
// backup jobs could not be read — so none of them is taken to have left.
func (a *dbAlerts) readings(rule dbAlertRule, conns []dbAlertConn, observed map[int64]dbAlertObservation, jobs []dbAlertJob, now time.Time) ([]dbAlertReading, bool) {
	out := []dbAlertReading{}
	switch rule.Kind {
	case dbx.AlertBackupFailed, dbx.AlertBackupMissed:
		if jobs == nil {
			return out, false
		}
		for _, job := range jobs {
			if !job.dumps(rule.ConnectionID) {
				continue
			}
			reading := judgeBackupJob(rule, job, now)
			reading.Subject, reading.JobID, reading.Label = dbAlertJobSubject(job.ID), job.ID, job.Name
			reading.ConnectionID = rule.ConnectionID
			out = append(out, reading)
		}
		return out, true
	}
	for _, conn := range conns {
		obs, ok := observed[conn.ID]
		if ok && obs.Conn.Flavor != "" {
			conn.Flavor = obs.Conn.Flavor
		}
		if !rule.covers(conn) {
			continue
		}
		id := conn.ID
		reading := dbAlertReading{Subject: dbAlertConnSubject(id), ConnectionID: &id, Label: conn.Name}
		switch {
		case !ok:
			reading.Unknown = "The last pass ran out of time before it reached this server."
		case obs.Unread != "":
			reading.Unknown = obs.Unread
		default:
			judgeServer(rule, obs, &reading)
		}
		out = append(out, reading)
	}
	return out, true
}

// judgeServer reads one server's observation against one rule.
func judgeServer(rule dbAlertRule, obs dbAlertObservation, r *dbAlertReading) {
	name := obs.Conn.Name
	line := dbAlertFigure(rule.Kind, rule.Threshold)
	clear := dbAlertClearLine(rule.Kind, rule.Threshold)
	if rule.Kind == dbx.AlertDown {
		r.Over, r.Clear = !obs.Answered, obs.Answered
		if obs.Answered {
			r.Detail = name + " answers."
		} else {
			r.Value = 1
			r.Detail = name + " does not answer: " + obs.Error
		}
		return
	}
	if !obs.Answered {
		r.Unknown = name + " is not answering, so nothing else about it can be read."
		return
	}
	switch rule.Kind {
	case dbx.AlertConnections:
		s := obs.Sessions
		if s == nil || s.Limit <= 0 {
			r.Unknown = name + " does not say how many connections it allows."
			return
		}
		usable, reserved := s.Limit, ""
		if s.Reserved > 0 && s.Reserved < s.Limit {
			usable -= s.Reserved
			reserved = fmt.Sprintf(", %d more kept for superusers", s.Reserved)
		}
		r.Value = float64(s.Held) / float64(usable) * 100
		r.Over, r.Clear = r.Value >= rule.Threshold, r.Value < clear
		r.Detail = fmt.Sprintf("%d of %d connections in use (%s%s); the line is %s.", s.Held, usable, dbAlertPercent(r.Value), reserved, line)
	case dbx.AlertDisk:
		if obs.Disk == nil {
			r.Unknown = obs.DiskUnknown
			if r.Unknown == "" {
				r.Unknown = "The disk under " + name + " was not read."
			}
			return
		}
		d := obs.Disk
		r.Value = dbx.DiskUse{Used: d.Used, Total: d.Total}.Percent()
		r.Over, r.Clear = r.Value >= rule.Threshold, r.Value < clear
		r.Detail = fmt.Sprintf("%s of %s is used (%s of %s); the line is %s.",
			dbAlertPercent(r.Value), d.Where, alertBytes(d.Used), alertBytes(d.Total), line)
	case dbx.AlertReplication:
		lag := obs.Replication
		if lag == nil {
			r.Unknown = obs.ReplicationUnknown
			if r.Unknown == "" {
				r.Unknown = "The replication of " + name + " was not read."
			}
			return
		}
		switch {
		case !lag.Replicating:
			r.Over, r.Clear = false, true
			r.Detail = lag.Note
			if r.Detail == "" {
				r.Detail = name + " neither follows a primary nor has replicas to judge."
			}
		case lag.Stopped != "":
			r.Value, r.Over = -1, true
			r.Detail = lag.Stopped
		default:
			r.Value = lag.Seconds
			r.Over, r.Clear = lag.Seconds >= rule.Threshold, lag.Seconds < clear
			r.Detail = fmt.Sprintf("%s is %s behind; the line is %s.", lag.Of, dbAlertSeconds(lag.Seconds), line)
		}
	}
}

func dbAlertPercent(v float64) string {
	if v >= 10 || v == 0 {
		return fmt.Sprintf("%.0f%%", v)
	}
	return fmt.Sprintf("%.1f%%", v)
}

func dbAlertSeconds(s float64) string {
	if s < 120 {
		return fmt.Sprintf("%.0f s", s)
	}
	return humanAge(time.Duration(s) * time.Second)
}

func humanAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h %d min", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}

func alertBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// dbAlertJob is a backup job that dumps saved databases, with the runs an
// alert reads.
type dbAlertJob struct {
	ID        int64
	Name      string
	Schedule  string
	Enabled   bool
	CreatedAt time.Time
	Databases []int64
	Running   bool
	// Last is the newest run that has finished, and LastSuccess when the
	// newest successful one started.
	Last        *dbAlertRun
	LastSuccess *time.Time
}

type dbAlertRun struct {
	ID        int64
	Status    string
	StartedAt time.Time
	Reason    string
}

// dumps reports whether the job dumps the database, or any database when
// none is named.
func (j dbAlertJob) dumps(conn *int64) bool {
	if conn == nil {
		return len(j.Databases) > 0
	}
	for _, id := range j.Databases {
		if id == *conn {
			return true
		}
	}
	return false
}

// backupJobs reads every job that dumps a database, with its newest finished
// run, its newest success and whether a run is in flight, in three
// statements whatever the number of jobs.
func (a *dbAlerts) backupJobs(ctx context.Context) ([]dbAlertJob, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT id, name, schedule, enabled, created_at, database_dumps FROM backup_jobs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	jobs := []dbAlertJob{}
	index := map[int64]int{}
	for rows.Next() {
		var job dbAlertJob
		var enabled int
		var created int64
		var dumps string
		if err := rows.Scan(&job.ID, &job.Name, &job.Schedule, &enabled, &created, &dumps); err != nil {
			rows.Close()
			return nil, err
		}
		_ = json.Unmarshal([]byte(dumps), &job.Databases)
		if len(job.Databases) == 0 {
			continue
		}
		job.Enabled, job.CreatedAt = enabled == 1, time.Unix(created, 0).UTC()
		index[job.ID] = len(jobs)
		jobs = append(jobs, job)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = a.db.QueryContext(ctx, `SELECT job_id, MAX(status='running'), MAX(CASE WHEN status='success' THEN started_at END)
		FROM backup_runs GROUP BY job_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var jobID int64
		var running int
		var success sql.NullInt64
		if err := rows.Scan(&jobID, &running, &success); err != nil {
			rows.Close()
			return nil, err
		}
		if i, ok := index[jobID]; ok {
			jobs[i].Running = running == 1
			if success.Valid {
				at := time.Unix(success.Int64, 0).UTC()
				jobs[i].LastSuccess = &at
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = a.db.QueryContext(ctx, `SELECT r.job_id, r.id, r.status, r.started_at, substr(r.log, -2000) FROM backup_runs r
		WHERE r.id = (SELECT MAX(x.id) FROM backup_runs x WHERE x.job_id = r.job_id AND x.status <> 'running')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var jobID, started int64
		var run dbAlertRun
		var log string
		if err := rows.Scan(&jobID, &run.ID, &run.Status, &started, &log); err != nil {
			return nil, err
		}
		run.StartedAt = time.Unix(started, 0).UTC()
		run.Reason = backupFailure(log)
		if i, ok := index[jobID]; ok {
			jobs[i].Last = &run
		}
	}
	return jobs, rows.Err()
}

// backupFailure is the line the runner ends a failed run's log with.
func backupFailure(log string) string {
	i := strings.LastIndex(log, "FAILED: ")
	if i < 0 {
		return ""
	}
	line, _, _ := strings.Cut(log[i+len("FAILED: "):], "\n")
	line = strings.TrimSpace(line)
	if len(line) > 300 {
		line = line[:300] + "…"
	}
	return line
}

func alertTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }

// judgeBackupJob reads one job against a backup rule. A job that failed
// stays failed until a run succeeds; a scheduled one is missed once the time
// its next run was due after its last success, and the rule's grace after
// that, have both passed with no success and no run in flight.
func judgeBackupJob(rule dbAlertRule, job dbAlertJob, now time.Time) dbAlertReading {
	r := dbAlertReading{}
	if rule.Kind == dbx.AlertBackupFailed {
		switch {
		case job.Last == nil:
			r.Clear, r.Detail = true, "No run of "+job.Name+" has finished yet."
		case job.Last.Status == string(backups.StatusFailed):
			r.Over, r.Value = true, 1
			r.Detail = fmt.Sprintf("The run of %s that started %s failed", job.Name, alertTime(job.Last.StartedAt))
			if job.Last.Reason != "" {
				r.Detail += ": " + job.Last.Reason
			} else {
				r.Detail += "."
			}
		default:
			r.Clear, r.Detail = true, fmt.Sprintf("The last run of %s, %s, succeeded.", job.Name, alertTime(job.Last.StartedAt))
		}
		return r
	}
	switch {
	case !job.Enabled:
		r.Clear, r.Detail = true, job.Name+" is paused, so no run is due."
		return r
	case job.Schedule == "":
		r.Clear, r.Detail = true, job.Name+" has no schedule; it runs only when started."
		return r
	}
	anchor, since := job.CreatedAt, "it was created"
	if job.LastSuccess != nil {
		anchor, since = *job.LastSuccess, "its last successful run, "+alertTime(*job.LastSuccess)
	}
	due, ok := backups.NextScheduled(job.Schedule, anchor)
	if !ok {
		r.Unknown = "The schedule of " + job.Name + " cannot be read."
		return r
	}
	late := now.Sub(due) - time.Duration(rule.Minutes)*time.Minute
	r.Value = math.Max(now.Sub(anchor).Hours(), 0)
	switch {
	case late < 0:
		r.Clear = true
		r.Detail = fmt.Sprintf("The next run of %s is due %s.", job.Name, alertTime(due))
	case job.Running:
		// Late, and on its way: neither missed nor recovered until it ends.
		r.Detail = fmt.Sprintf("A run of %s is in progress; the one due %s has not succeeded yet.", job.Name, alertTime(due))
	default:
		r.Over = true
		r.Detail = fmt.Sprintf("%s has not succeeded since %s; a run was due %s.", job.Name, since, alertTime(due))
	}
	return r
}

// --- telling -----------------------------------------------------------------

// announce tells the rule's channels, unless the transition is one nobody is
// told of, and keeps what happened either way for the history.
func (a *dbAlerts) announce(ctx context.Context, rule dbAlertRule, st dbAlertState, transition dbAlertTransition, since time.Time) {
	event, kept := "fired", ""
	switch transition {
	case dbAlertHeld:
		kept = "cooldown"
	case dbAlertRecovered:
		event = "recovered"
	case dbAlertEnded:
		event, kept = "recovered", "recovery"
		if rule.NotifyRecovery {
			// Its firing was kept back, so its end is too.
			kept = "cooldown"
		}
	}
	delivered, failed := 0, 0
	if kept == "" {
		envelope := a.envelope(rule, st, event, since)
		for _, id := range a.recipients(ctx, rule) {
			switch err := a.deliver(ctx, id, envelope); {
			case err == nil:
				delivered++
			case errors.Is(err, deploy.ErrHookDisabled):
				// A paused channel is told nothing, and that is not a failure.
			default:
				failed++
				a.warn(fmt.Sprintf("database alert delivery failed for channel %d", id), err)
			}
		}
	}
	if _, err := a.db.ExecContext(context.WithoutCancel(ctx), `INSERT INTO db_alert_events
		(rule_id, connection_id, job_id, kind, subject, label, event, detail, delivered, failed, kept, created_at)
		SELECT ?,?,?,?,?,?,?,?,?,?,?,? WHERE ? IS NULL OR EXISTS (SELECT 1 FROM db_connections WHERE id=?)`,
		rule.ID, st.ConnectionID, st.JobID, rule.Kind, st.Subject, st.Label, event, st.Detail, delivered, failed, kept,
		a.now().UTC().Unix(), st.ConnectionID, st.ConnectionID); err != nil {
		a.warn("database alert event not recorded", err)
	}
}

// recipients is the rule's channels, or every enabled channel when it names
// none.
func (a *dbAlerts) recipients(ctx context.Context, rule dbAlertRule) []int64 {
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

func (a *dbAlerts) envelope(rule dbAlertRule, st dbAlertState, event string, since time.Time) deploy.NotificationEnvelope {
	notification := deploy.NotificationEventDatabaseFiring
	if event == "recovered" {
		notification = deploy.NotificationEventDatabaseRecovered
	}
	alert := &deploy.DatabaseAlertEnvelope{
		RuleID: rule.ID, Kind: rule.Kind, JobID: st.JobID, Subject: st.Subject, Label: st.Label,
		Detail: st.Detail, Reading: st.Reading, Threshold: rule.Threshold, Since: since,
	}
	if st.ConnectionID != nil {
		alert.ConnectionID = *st.ConnectionID
	}
	envelope := deploy.NotificationEnvelope{Event: notification, SentAt: a.now().UTC(), Database: alert}
	if a.url != nil {
		if base := strings.TrimRight(strings.TrimSpace(a.url()), "/"); base != "" {
			envelope.URL = base + dbAlertPage(rule.Kind, st.ConnectionID)
		}
	}
	return envelope
}

// dbAlertPage is where a message sends its reader: the database's Alerts
// page, its Backups page for a backup, or the server's backups for a rule
// that watches every database's.
func dbAlertPage(kind string, connection *int64) string {
	backup := kind == dbx.AlertBackupFailed || kind == dbx.AlertBackupMissed
	switch {
	case connection == nil && backup:
		return "/backups"
	case connection == nil:
		return "/databases"
	case backup:
		return fmt.Sprintf("/databases/%d/backups", *connection)
	}
	return fmt.Sprintf("/databases/%d/alerts", *connection)
}

// testDBAlertDetail is a kind's reading as a test message states it.
var testDBAlertDetail = map[string]string{
	dbx.AlertDown:         "It does not answer: connection refused.",
	dbx.AlertDisk:         "92% of its data volume is used (184.0 GB of 200.0 GB).",
	dbx.AlertConnections:  "92 of 100 connections in use (92%).",
	dbx.AlertReplication:  "A replica is 95 s behind.",
	dbx.AlertBackupFailed: "The last run of its backup job failed: pg_dump: error: connection refused.",
	dbx.AlertBackupMissed: "Its backup job has not succeeded since it was due.",
}

// TestEnvelope is what "Send test" delivers: the rule's message as it would
// arrive, about the database the rule names, marked as a test so nobody acts
// on it.
func (a *dbAlerts) TestEnvelope(rule dbAlertRule, name string) deploy.NotificationEnvelope {
	if name == "" {
		name = "every database"
	}
	if rule.Kind == dbx.AlertBackupFailed || rule.Kind == dbx.AlertBackupMissed {
		name = "the backup job of " + name
	}
	st := dbAlertState{Subject: "test", Label: name, ConnectionID: rule.ConnectionID,
		Detail: testDBAlertDetail[rule.Kind] + " This is a test from the database's Alerts page; nothing is wrong."}
	envelope := a.envelope(rule, st, "fired", a.now().UTC())
	envelope.Database.Test = true
	return envelope
}

// --- rules ---------------------------------------------------------------------

const dbAlertRuleColumns = `id, connection_id, kind, threshold, minutes, cooldown_minutes, notify_recovery, channels, enabled, revision, created_at, updated_at`

// Rules lists the rules, every one or those that watch one database — its own
// and the ones for every database — and only enabled ones when asked.
func (a *dbAlerts) Rules(ctx context.Context, connection *int64, enabledOnly bool) ([]dbAlertRule, error) {
	query := `SELECT ` + dbAlertRuleColumns + ` FROM db_alert_rules WHERE 1=1`
	args := []any{}
	if connection != nil {
		query += ` AND (connection_id IS NULL OR connection_id=?)`
		args = append(args, *connection)
	}
	if enabledOnly {
		query += ` AND enabled=1`
	}
	rows, err := a.db.QueryContext(ctx, query+` ORDER BY connection_id IS NULL, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []dbAlertRule{}
	for rows.Next() {
		var rule dbAlertRule
		var conn sql.NullInt64
		var channels string
		var recovery, enabled int
		var created, updated int64
		if err := rows.Scan(&rule.ID, &conn, &rule.Kind, &rule.Threshold, &rule.Minutes, &rule.CooldownMinutes,
			&recovery, &channels, &enabled, &rule.revision, &created, &updated); err != nil {
			return nil, err
		}
		rule.ConnectionID = nullID(conn)
		rule.Channels = []int64{}
		_ = json.Unmarshal([]byte(channels), &rule.Channels)
		rule.NotifyRecovery, rule.Enabled = recovery == 1, enabled == 1
		rule.CreatedAt, rule.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
		out = append(out, rule)
	}
	return out, rows.Err()
}

func (a *dbAlerts) rule(ctx context.Context, id int64) (*dbAlertRule, error) {
	rules, err := a.Rules(ctx, nil, false)
	if err != nil {
		return nil, err
	}
	for i := range rules {
		if rules[i].ID == id {
			return &rules[i], nil
		}
	}
	return nil, errDBAlertNotFound
}

// checkChannels refuses a rule naming a channel that does not exist, so a
// typo is a form error now rather than a silent alert later.
func (a *dbAlerts) checkChannels(ctx context.Context, ids []int64) error {
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
			return invalidDBAlert("notification channel %d does not exist", id)
		}
	}
	return nil
}

// CreateRule saves a rule. `kinds` is what the named database's engine can be
// watched for, which the caller reads; a rule for every database may be any
// kind, and watches the databases that have it.
func (a *dbAlerts) CreateRule(ctx context.Context, in dbAlertWrite, kinds func(int64) ([]string, error)) (*dbAlertRule, error) {
	rule, err := a.checkRule(ctx, in, kinds)
	if err != nil {
		return nil, err
	}
	channels, _ := json.Marshal(rule.Channels)
	now := a.now().UTC().Unix()
	res, err := a.db.ExecContext(ctx, `INSERT INTO db_alert_rules
		(connection_id, kind, threshold, minutes, cooldown_minutes, notify_recovery, channels, enabled, revision, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,1,?,?)`,
		rule.ConnectionID, rule.Kind, rule.Threshold, rule.Minutes, rule.CooldownMinutes, boolInt(rule.NotifyRecovery),
		string(channels), boolInt(rule.Enabled), now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return a.rule(ctx, id)
}

func (a *dbAlerts) checkRule(ctx context.Context, in dbAlertWrite, kinds func(int64) ([]string, error)) (dbAlertRule, error) {
	rule, err := validateDBAlert(in)
	if err != nil {
		return rule, err
	}
	if rule.ConnectionID != nil {
		offered, err := kinds(*rule.ConnectionID)
		if err != nil {
			return rule, err
		}
		if !slicesContains(offered, rule.Kind) {
			return rule, invalidDBAlert("this database's engine cannot be watched for %s", strings.ReplaceAll(rule.Kind, "_", " "))
		}
	}
	return rule, a.checkChannels(ctx, rule.Channels)
}

func slicesContains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// UpdateRule replaces a rule's terms. One whose line, timing, database or
// kind changed, or that was paused, starts over: what it was following was a
// verdict on the old terms, and a paused rule follows nothing. Its channels,
// cooldown and whether it tells recoveries change how it speaks, not what it
// saw, and keep what it is following.
func (a *dbAlerts) UpdateRule(ctx context.Context, id int64, in dbAlertWrite, kinds func(int64) ([]string, error)) (*dbAlertRule, error) {
	current, err := a.rule(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.Enabled == nil {
		enabled := current.Enabled
		in.Enabled = &enabled
	}
	rule, err := a.checkRule(ctx, in, kinds)
	if err != nil {
		return nil, err
	}
	channels, _ := json.Marshal(rule.Channels)
	if _, err := a.db.ExecContext(ctx, `UPDATE db_alert_rules SET connection_id=?, kind=?, threshold=?, minutes=?,
		cooldown_minutes=?, notify_recovery=?, channels=?, enabled=?, revision=revision+1, updated_at=? WHERE id=?`,
		rule.ConnectionID, rule.Kind, rule.Threshold, rule.Minutes, rule.CooldownMinutes, boolInt(rule.NotifyRecovery),
		string(channels), boolInt(rule.Enabled), a.now().UTC().Unix(), id); err != nil {
		return nil, err
	}
	sameConn := (rule.ConnectionID == nil) == (current.ConnectionID == nil) &&
		(rule.ConnectionID == nil || *rule.ConnectionID == *current.ConnectionID)
	if !rule.Enabled || rule.Kind != current.Kind || rule.Threshold != current.Threshold ||
		rule.Minutes != current.Minutes || !sameConn {
		if _, err := a.db.ExecContext(ctx, `DELETE FROM db_alert_state WHERE rule_id=?`, id); err != nil {
			return nil, err
		}
	}
	return a.rule(ctx, id)
}

func (a *dbAlerts) DeleteRule(ctx context.Context, id int64) (*dbAlertRule, error) {
	current, err := a.rule(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, err := a.db.ExecContext(ctx, `DELETE FROM db_alert_rules WHERE id=?`, id); err != nil {
		return nil, err
	}
	return current, nil
}

// --- the page's reading -----------------------------------------------------------

// dbAlertSubject is one subject a rule watches, as the page lists it.
type dbAlertSubject struct {
	RuleID       int64  `json:"ruleId"`
	Subject      string `json:"subject"`
	ConnectionID *int64 `json:"connectionId,omitempty"`
	JobID        int64  `json:"jobId,omitempty"`
	Label        string `json:"label"`
	// State is "quiet", "pending" while it holds before it is told,
	// "firing" once told, "held" while the cooldown keeps it back, and
	// "unknown" when the last pass could not judge it.
	State      string     `json:"state"`
	Reading    float64    `json:"reading"`
	Detail     string     `json:"detail"`
	Unknown    string     `json:"unknown,omitempty"`
	Since      *time.Time `json:"since,omitempty"`
	FiredAt    *time.Time `json:"firedAt,omitempty"`
	NotifiedAt *time.Time `json:"notifiedAt,omitempty"`
	CheckedAt  *time.Time `json:"checkedAt,omitempty"`
}

type dbAlertEvent struct {
	ID           int64  `json:"id"`
	RuleID       int64  `json:"ruleId"`
	ConnectionID *int64 `json:"connectionId,omitempty"`
	JobID        int64  `json:"jobId,omitempty"`
	Kind         string `json:"kind"`
	Subject      string `json:"subject"`
	Label        string `json:"label"`
	// Event is "fired" or "recovered".
	Event     string `json:"event"`
	Detail    string `json:"detail"`
	Delivered int    `json:"delivered"`
	Failed    int    `json:"failed"`
	// Kept says why nobody was told: "cooldown", or "recovery" for a rule
	// that tells only firings.
	Kept      string    `json:"kept,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// jobsDumping is the ids of the backup jobs that dump a database.
func (a *dbAlerts) jobsDumping(ctx context.Context, connection int64) (map[int64]bool, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT id, database_dumps FROM backup_jobs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		var dumps string
		if err := rows.Scan(&id, &dumps); err != nil {
			return nil, err
		}
		var ids []int64
		_ = json.Unmarshal([]byte(dumps), &ids)
		for _, dumped := range ids {
			if dumped == connection {
				out[id] = true
			}
		}
	}
	return out, rows.Err()
}

// Subjects lists what the rules watch, for one database or all: a server's
// subject is its own, and a backup job's belongs to every database it dumps.
func (a *dbAlerts) Subjects(ctx context.Context, connection *int64) ([]dbAlertSubject, error) {
	jobs := map[int64]bool{}
	if connection != nil {
		var err error
		if jobs, err = a.jobsDumping(ctx, *connection); err != nil {
			return nil, err
		}
	}
	rows, err := a.db.QueryContext(ctx, `SELECT s.rule_id, s.subject, s.connection_id, s.job_id, s.label, s.reading, s.detail,
		s.unknown, s.since, s.fired_at, s.notified_at, s.checked_at
		FROM db_alert_state s JOIN db_alert_rules r ON r.id = s.rule_id WHERE r.enabled = 1 ORDER BY s.rule_id, s.label`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []dbAlertSubject{}
	for rows.Next() {
		var s dbAlertSubject
		var conn sql.NullInt64
		var since, fired, notified, checked int64
		if err := rows.Scan(&s.RuleID, &s.Subject, &conn, &s.JobID, &s.Label, &s.Reading, &s.Detail, &s.Unknown,
			&since, &fired, &notified, &checked); err != nil {
			return nil, err
		}
		s.ConnectionID = nullID(conn)
		if connection != nil && !jobs[s.JobID] && (s.JobID != 0 || s.ConnectionID == nil || *s.ConnectionID != *connection) {
			continue
		}
		s.Since, s.FiredAt, s.NotifiedAt, s.CheckedAt = unixTime(since), unixTime(fired), unixTime(notified), unixTime(checked)
		switch {
		case fired > 0 && notified > 0:
			s.State = "firing"
		case fired > 0:
			s.State = "held"
		case s.Unknown != "":
			s.State = "unknown"
		case since > 0:
			s.State = "pending"
		default:
			s.State = "quiet"
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Events is the newest of what was told or kept back, for one database or
// all.
func (a *dbAlerts) Events(ctx context.Context, connection *int64, limit int) ([]dbAlertEvent, error) {
	query := `SELECT id, rule_id, connection_id, job_id, kind, subject, label, event, detail, delivered, failed, kept, created_at FROM db_alert_events`
	args := []any{}
	if connection != nil {
		jobs, err := a.jobsDumping(ctx, *connection)
		if err != nil {
			return nil, err
		}
		ids := make([]int64, 0, len(jobs))
		for id := range jobs {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		query += ` WHERE connection_id=?`
		args = append(args, *connection)
		if len(ids) > 0 {
			query += ` OR (job_id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + `) AND connection_id IS NULL)`
			for _, id := range ids {
				args = append(args, id)
			}
		}
	}
	rows, err := a.db.QueryContext(ctx, query+` ORDER BY id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []dbAlertEvent{}
	for rows.Next() {
		var e dbAlertEvent
		var conn sql.NullInt64
		var created int64
		if err := rows.Scan(&e.ID, &e.RuleID, &conn, &e.JobID, &e.Kind, &e.Subject, &e.Label, &e.Event, &e.Detail,
			&e.Delivered, &e.Failed, &e.Kept, &created); err != nil {
			return nil, err
		}
		e.ConnectionID = nullID(conn)
		e.CreatedAt = time.Unix(created, 0).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}
