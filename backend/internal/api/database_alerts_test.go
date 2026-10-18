package api

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// stepper walks one subject through readings a pass apart, the way the
// recorder hands them over, and says what each one decided.
type stepper struct {
	t    *testing.T
	rule dbAlertRule
	st   dbAlertState
	now  time.Time
}

func (s *stepper) read(value float64, after time.Duration) dbAlertTransition {
	s.t.Helper()
	s.now = s.now.Add(after)
	r := dbAlertReading{Subject: "connection:1", Label: "shop", Value: value,
		Over: value >= s.rule.Threshold, Clear: value < dbAlertClearLine(s.rule.Kind, s.rule.Threshold)}
	var transition dbAlertTransition
	s.st, transition = stepDBAlert(s.rule, s.st, r, s.now)
	return transition
}

func (s *stepper) expect(value float64, after time.Duration, want dbAlertTransition) {
	s.t.Helper()
	if got := s.read(value, after); got != want {
		s.t.Fatalf("reading %v at +%s: %v, want %v (state %+v)", value, s.now.Sub(start), got, want, s.st)
	}
}

var start = time.Date(2026, time.October, 17, 3, 0, 0, 0, time.UTC)

func diskRule() dbAlertRule {
	rule, err := validateDBAlert(dbAlertWrite{Kind: dbx.AlertDisk})
	if err != nil {
		panic(err)
	}
	return rule
}

// A line crossed is told once it has held for the rule's minutes over two
// readings; a reading between the line and the clearing line under it keeps
// it firing; two readings under the clearing line end it, and that is told.
func TestDBAlertHoldsAndRecoversWithHysteresis(t *testing.T) {
	rule := diskRule()
	if rule.Threshold != 90 || rule.Minutes != 5 || rule.CooldownMinutes != 30 || !rule.NotifyRecovery {
		t.Fatalf("a disk rule's defaults: %+v", rule)
	}
	s := &stepper{t: t, rule: rule, now: start}
	s.expect(91, 0, dbAlertQuiet)
	s.expect(92, 30*time.Second, dbAlertQuiet)
	if s.st.Since != start.Unix() || s.st.Seen != 2 || s.st.FiredAt != 0 {
		t.Fatalf("holding: %+v", s.st)
	}
	s.expect(93, 4*time.Minute+30*time.Second, dbAlertFired)
	s.expect(94, 30*time.Second, dbAlertQuiet)
	// Under the line, over its clearing line at 85: still the same alert.
	s.expect(88, 30*time.Second, dbAlertQuiet)
	s.expect(84, 30*time.Second, dbAlertQuiet)
	// Back over the clearing line, so the two readings under it start again.
	s.expect(87, 30*time.Second, dbAlertQuiet)
	s.expect(84, 30*time.Second, dbAlertQuiet)
	if s.st.FiredAt == 0 || s.st.Clear != 1 {
		t.Fatalf("a firing subject recovered on one reading: %+v", s.st)
	}
	s.expect(83, 30*time.Second, dbAlertRecovered)
	if s.st.Since != 0 || s.st.FiredAt != 0 || s.st.NotifiedAt != 0 || s.st.ToldAt == 0 {
		t.Fatalf("after recovery: %+v", s.st)
	}
	// A blip over the line that does not hold ends without a word.
	s.expect(95, 30*time.Second, dbAlertQuiet)
	s.expect(80, 30*time.Second, dbAlertQuiet)
	if s.st.Since != 0 || s.st.Seen != 0 {
		t.Fatalf("an unheld blip left its episode open: %+v", s.st)
	}
}

// A subject that fires again inside the cooldown is kept back; told once the
// cooldown is over if it is still firing, and its recovery is not told when
// its firing never was.
func TestDBAlertCooldownKeepsARepeatBack(t *testing.T) {
	rule := diskRule()
	rule.Minutes = 0
	s := &stepper{t: t, rule: rule, now: start}
	s.expect(95, 0, dbAlertQuiet)
	// No minutes to hold: two readings decide it.
	s.expect(95, 30*time.Second, dbAlertFired)
	told := s.st.ToldAt
	s.expect(50, time.Minute, dbAlertQuiet)
	s.expect(50, 30*time.Second, dbAlertRecovered)
	s.expect(96, time.Minute, dbAlertQuiet)
	s.expect(96, 30*time.Second, dbAlertHeld)
	if s.st.FiredAt == 0 || s.st.NotifiedAt != 0 || s.st.ToldAt != told {
		t.Fatalf("held: %+v", s.st)
	}
	s.expect(97, time.Minute, dbAlertQuiet)
	// Thirty minutes after the last message, and still over the line.
	s.expect(97, 30*time.Minute-3*time.Minute-30*time.Second, dbAlertFired)
	if s.st.NotifiedAt == 0 || s.st.ToldAt == told {
		t.Fatalf("not told after the cooldown: %+v", s.st)
	}

	s = &stepper{t: t, rule: rule, now: start}
	s.expect(95, 0, dbAlertQuiet)
	s.expect(95, 30*time.Second, dbAlertFired)
	s.expect(50, 30*time.Second, dbAlertQuiet)
	s.expect(50, 30*time.Second, dbAlertRecovered)
	s.expect(96, 30*time.Second, dbAlertQuiet)
	s.expect(96, 30*time.Second, dbAlertHeld)
	s.expect(50, 30*time.Second, dbAlertQuiet)
	s.expect(50, 30*time.Second, dbAlertEnded)

	rule.NotifyRecovery = false
	s = &stepper{t: t, rule: rule, now: start}
	s.expect(95, 0, dbAlertQuiet)
	s.expect(95, 30*time.Second, dbAlertFired)
	s.expect(50, 30*time.Second, dbAlertQuiet)
	s.expect(50, 30*time.Second, dbAlertEnded)
}

// A reading that could not be taken keeps the subject where it was: an
// unreadable disk is not one that emptied, and not one that filled.
func TestDBAlertUnknownReadingKeepsTheState(t *testing.T) {
	rule := diskRule()
	rule.Minutes = 0
	s := &stepper{t: t, rule: rule, now: start}
	s.expect(95, 0, dbAlertQuiet)
	s.expect(95, 30*time.Second, dbAlertFired)
	before := s.st
	after, transition := stepDBAlert(rule, s.st, dbAlertReading{Label: "shop", Unknown: "not answering"}, s.now.Add(30*time.Second))
	if transition != dbAlertQuiet || after.FiredAt != before.FiredAt || after.Seen != before.Seen ||
		after.Detail != before.Detail || after.Unknown != "not answering" {
		t.Fatalf("an unknown reading moved the subject: %+v → %+v", before, after)
	}
}

// A backup run is a fact: one reading fires it and one recovers it.
func TestDBAlertBackupKindsDecideOnOneReading(t *testing.T) {
	rule, err := validateDBAlert(dbAlertWrite{Kind: dbx.AlertBackupFailed})
	if err != nil || rule.Threshold != 0 || rule.Minutes != 0 {
		t.Fatalf("a backup rule: %+v %v", rule, err)
	}
	now := start
	st, transition := stepDBAlert(rule, dbAlertState{}, dbAlertReading{Over: true}, now)
	if transition != dbAlertFired {
		t.Fatalf("a failed run: %v", transition)
	}
	if _, transition = stepDBAlert(rule, st, dbAlertReading{Clear: true}, now.Add(time.Hour)); transition != dbAlertRecovered {
		t.Fatalf("a run that succeeded: %v", transition)
	}
	missed, _ := validateDBAlert(dbAlertWrite{Kind: dbx.AlertBackupMissed})
	if _, transition = stepDBAlert(missed, dbAlertState{}, dbAlertReading{Over: true}, now); transition != dbAlertFired {
		t.Fatalf("a missed run waited a second time for its grace: %v", transition)
	}
}

func TestDBAlertValidationHoldsEachKindToItsRange(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	n := func(v int) *int { return &v }
	zero := int64(0)
	for _, in := range []dbAlertWrite{
		{Kind: "cpu"},
		{Kind: dbx.AlertDisk, Threshold: f(100)},
		{Kind: dbx.AlertDisk, Threshold: f(5)},
		{Kind: dbx.AlertConnections, Threshold: f(101)},
		{Kind: dbx.AlertReplication, Threshold: f(0)},
		{Kind: dbx.AlertDown, Minutes: n(0)},
		{Kind: dbx.AlertDown, Minutes: n(1441)},
		{Kind: dbx.AlertBackupMissed, Minutes: n(1)},
		{Kind: dbx.AlertDisk, CooldownMinutes: n(-1)},
		{Kind: dbx.AlertDisk, CooldownMinutes: n(dbAlertMaxCooldown + 1)},
		{Kind: dbx.AlertDisk, ConnectionID: &zero},
	} {
		if _, err := validateDBAlert(in); err == nil {
			t.Errorf("accepted %+v", in)
		}
	}
	rule, err := validateDBAlert(dbAlertWrite{Kind: " Replication ", Threshold: f(30), Minutes: n(0), CooldownMinutes: n(0),
		NotifyRecovery: new(bool), Channels: []int64{3, 1, 3, -2}})
	if err != nil || rule.Kind != dbx.AlertReplication || rule.Threshold != 30 || rule.Minutes != 0 ||
		rule.CooldownMinutes != 0 || rule.NotifyRecovery || len(rule.Channels) != 2 || rule.Channels[0] != 1 {
		t.Fatalf("replication rule: %+v %v", rule, err)
	}
	// A kind with no line keeps none, whatever is sent.
	if rule, _ := validateDBAlert(dbAlertWrite{Kind: dbx.AlertDown, Threshold: f(50)}); rule.Threshold != 0 || rule.Minutes != 2 {
		t.Fatalf("down rule: %+v", rule)
	}
}

func TestDBAlertJudgesServerReadings(t *testing.T) {
	conn := dbAlertConn{ID: 1, Name: "shop", Driver: dbx.DriverPostgres}
	judge := func(kind string, obs dbAlertObservation) dbAlertReading {
		rule, err := validateDBAlert(dbAlertWrite{Kind: kind})
		if err != nil {
			t.Fatal(err)
		}
		obs.Conn = conn
		var r dbAlertReading
		judgeServer(rule, obs, &r)
		return r
	}
	if r := judge(dbx.AlertDown, dbAlertObservation{Error: "connection refused"}); !r.Over || r.Detail != "shop does not answer: connection refused" {
		t.Fatalf("down: %+v", r)
	}
	if r := judge(dbx.AlertDown, dbAlertObservation{Answered: true}); r.Over || !r.Clear {
		t.Fatalf("up: %+v", r)
	}
	if r := judge(dbx.AlertDisk, dbAlertObservation{Error: "refused"}); r.Unknown == "" {
		t.Fatalf("the disk of a server that is down was judged: %+v", r)
	}
	// Three of a hundred are kept for superusers, so 92 is 92 of 97.
	r := judge(dbx.AlertConnections, dbAlertObservation{Answered: true, Sessions: &dbAlertSessions{Held: 92, Limit: 100, Reserved: 3}})
	if !r.Over || r.Value < 94.8 || r.Value > 94.9 ||
		r.Detail != "92 of 97 connections in use (95%, 3 more kept for superusers); the line is 80%." {
		t.Fatalf("connections: %+v", r)
	}
	if r := judge(dbx.AlertConnections, dbAlertObservation{Answered: true}); !strings.Contains(r.Unknown, "does not say") {
		t.Fatalf("connections with no limit: %+v", r)
	}
	r = judge(dbx.AlertDisk, dbAlertObservation{Answered: true, Disk: &dbAlertDisk{Used: 87 << 30, Total: 100 << 30, Where: "the volume shop-data"}})
	if r.Over || r.Clear || r.Detail != "87% of the volume shop-data is used (87.0 GB of 100.0 GB); the line is 90%." {
		t.Fatalf("disk between the line and its clearing line: %+v", r)
	}
	if r := judge(dbx.AlertDisk, dbAlertObservation{Answered: true, DiskUnknown: "shop runs on another machine"}); r.Unknown != "shop runs on another machine" {
		t.Fatalf("an unmeasured disk: %+v", r)
	}
	r = judge(dbx.AlertReplication, dbAlertObservation{Answered: true, Replication: &dbx.ReplicationLag{Replicating: true, Seconds: 75, Of: "replica-1"}})
	if !r.Over || r.Detail != "replica-1 is 75 s behind; the line is 60 s." {
		t.Fatalf("lag: %+v", r)
	}
	r = judge(dbx.AlertReplication, dbAlertObservation{Answered: true, Replication: &dbx.ReplicationLag{Replicating: true, Stopped: "The link to the primary 10.0.0.1:6379 is down."}})
	if !r.Over || r.Detail != "The link to the primary 10.0.0.1:6379 is down." {
		t.Fatalf("stopped: %+v", r)
	}
	if r := judge(dbx.AlertReplication, dbAlertObservation{Answered: true, Replication: &dbx.ReplicationLag{}}); r.Over || !r.Clear {
		t.Fatalf("a server that does not replicate: %+v", r)
	}
}

func TestDBAlertJudgesBackupJobs(t *testing.T) {
	failed, _ := validateDBAlert(dbAlertWrite{Kind: dbx.AlertBackupFailed})
	missed, _ := validateDBAlert(dbAlertWrite{Kind: dbx.AlertBackupMissed})
	yesterday := start.Add(-24 * time.Hour)
	job := dbAlertJob{ID: 4, Name: "nightly", Schedule: "0 3 * * *", Enabled: true, CreatedAt: start.Add(-72 * time.Hour),
		Last: &dbAlertRun{Status: "failed", StartedAt: start, Reason: "pg_dump: error: connection refused"}, LastSuccess: &yesterday}
	if r := judgeBackupJob(failed, job, start.Add(time.Minute)); !r.Over ||
		r.Detail != "The run of nightly that started 2026-10-17 03:00 UTC failed: pg_dump: error: connection refused" {
		t.Fatalf("failed: %+v", r)
	}
	job.Last.Status = "success"
	if r := judgeBackupJob(failed, job, start.Add(time.Minute)); r.Over || !r.Clear {
		t.Fatalf("succeeded: %+v", r)
	}
	// The scheduler fires in the server's zone; the test reads it the same way.
	local := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 3, 0, 0, 0, time.Local) }
	success := local(start.Add(-24 * time.Hour))
	job.LastSuccess = &success
	due := local(start)
	if r := judgeBackupJob(missed, job, due.Add(59*time.Minute)); r.Over || !r.Clear {
		t.Fatalf("inside its grace: %+v", r)
	}
	if r := judgeBackupJob(missed, job, due.Add(61*time.Minute)); !r.Over || !strings.Contains(r.Detail, "has not succeeded since its last successful run") {
		t.Fatalf("missed: %+v", r)
	}
	job.Running = true
	if r := judgeBackupJob(missed, job, due.Add(61*time.Minute)); r.Over || r.Clear {
		t.Fatalf("late and running is neither missed nor recovered: %+v", r)
	}
	job.Running, job.Enabled = false, false
	if r := judgeBackupJob(missed, job, due.Add(48*time.Hour)); r.Over {
		t.Fatalf("a paused job was missed: %+v", r)
	}
	job.Enabled, job.Schedule = true, ""
	if r := judgeBackupJob(missed, job, due.Add(48*time.Hour)); r.Over {
		t.Fatalf("a job with no schedule was missed: %+v", r)
	}
	job.Schedule, job.LastSuccess = "0 3 * * *", nil
	if r := judgeBackupJob(missed, job, due.Add(61*time.Minute)); !r.Over || !strings.Contains(r.Detail, "since it was created") {
		t.Fatalf("a job that never succeeded: %+v", r)
	}
	if backupFailure("started\nFAILED: backup was interrupted before completion\n") != "backup was interrupted before completion" {
		t.Fatal("the runner's failure line was not read")
	}
}

func TestDBAlertSessionsComeFromTheRecordersSnapshot(t *testing.T) {
	sql := dbStatsResponse{ServerStats: &dbx.ServerStats{Supported: true, Connections: &dbx.ConnectionCounts{Total: 12, Max: 100, Reserved: 3}}}
	if s := dbAlertSessionsOf(sql); s == nil || s.Held != 12 || s.Limit != 100 || s.Reserved != 3 {
		t.Fatalf("sql: %+v", s)
	}
	redis := map[string]any{"server": map[string]any{"counters": map[string]any{"connected_clients": int64(9), "maxclients": int64(10)}}}
	if s := dbAlertSessionsOf(redis); s == nil || s.Held != 9 || s.Limit != 10 {
		t.Fatalf("redis: %+v", s)
	}
	mongo := map[string]any{"server": map[string]any{"connections": map[string]any{"current": int64(5), "available": int64(95)}}}
	if s := dbAlertSessionsOf(mongo); s == nil || s.Held != 5 || s.Limit != 100 {
		t.Fatalf("mongo: %+v", s)
	}
	if s := dbAlertSessionsOf(map[string]any{"server": map[string]any{"counters": map[string]any{"connected_clients": int64(9)}}}); s != nil {
		t.Fatalf("a Redis that does not say its maxclients: %+v", s)
	}
}

func TestDataMountFindsTheVolumeUnderTheDataDirectory(t *testing.T) {
	mounts := []dockerx.MountPoint{
		{Type: "bind", Source: "/srv/shop/conf", Destination: "/etc/postgresql"},
		{Type: "volume", Name: "shop-data", Source: "/var/lib/docker/volumes/shop-data/_data", Destination: "/var/lib/postgresql"},
		{Type: "volume", Name: "shop-pgdata", Source: "/var/lib/docker/volumes/shop-pgdata/_data", Destination: "/var/lib/postgresql/data"},
	}
	source, where := dataMount(mounts, []string{"", "/var/lib/postgresql/data", "/var/lib/postgresql"})
	if source != "/var/lib/docker/volumes/shop-pgdata/_data" || where != "the volume shop-pgdata" {
		t.Fatalf("deepest mount: %s, %s", source, where)
	}
	source, where = dataMount(mounts[:2], []string{"/var/lib/postgresql/data/pgdata"})
	if source != "/var/lib/docker/volumes/shop-data/_data/data/pgdata" || where != "the volume shop-data" {
		t.Fatalf("a directory below a mount: %s, %s", source, where)
	}
	// A prefix of a name is not a parent directory.
	if source, _ := dataMount([]dockerx.MountPoint{{Source: "/srv/x", Destination: "/data"}}, []string{"/database"}); source != "" {
		t.Fatalf("matched by name prefix: %s", source)
	}
}

// The recorder hands its pass to the alerts: a server that stops answering
// is told once it has stayed down for the rule's minutes, and a Redis near
// its maxclients is read from the same INFO the charts are drawn from.
func TestDatabaseRecorderJudgesItsOwnPass(t *testing.T) {
	s := testServer(t)
	var mu sync.Mutex
	clients := "connected_clients:9\r\nmaxclients:10\r\n"
	setClients := func(info string) {
		mu.Lock()
		defer mu.Unlock()
		clients = info
	}
	addr, stop := alertRedis(t, func() string {
		mu.Lock()
		defer mu.Unlock()
		return clients
	})
	id := saveOpsConnection(t, s, "cache", dbx.DriverRedis, "redis://"+addr+"/0")
	hook := newAlertWebhook(t, s)
	a := s.modules.dbAlerts
	now := start
	a.now = func() time.Time { return now }
	ctx := context.Background()
	kinds := func(int64) ([]string, error) { return dbx.AlertKinds(dbx.DriverRedis, ""), nil }
	one := func(v int) *int { return &v }
	if _, err := a.CreateRule(ctx, dbAlertWrite{ConnectionID: &id, Kind: dbx.AlertDown, Minutes: one(1)}, kinds); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CreateRule(ctx, dbAlertWrite{Kind: dbx.AlertConnections, Minutes: one(0)}, kinds); err != nil {
		t.Fatal(err)
	}
	pass := func() {
		t.Helper()
		s.recordDatabaseMetrics(ctx)
		now = now.Add(30 * time.Second)
	}
	pass()
	pass()
	sent := hook.received()
	if len(sent) != 1 || sent[0].Database.Kind != dbx.AlertConnections ||
		sent[0].Database.Detail != "9 of 10 connections in use (90%); the line is 80%." {
		t.Fatalf("connections near maxclients: %+v", sent)
	}
	setClients("connected_clients:2\r\nmaxclients:10\r\n")
	pass()
	pass()
	if sent := hook.received(); len(sent) != 2 || sent[1].Event != "database.alert.recovered" {
		t.Fatalf("connections back under: %+v", sent)
	}
	stop()
	for range 2 {
		pass()
	}
	if sent := hook.received(); len(sent) != 2 {
		t.Fatalf("a server down for less than its rule's minute was told: %+v", sent)
	}
	pass()
	sent = hook.received()
	if len(sent) != 3 || sent[2].Database.Kind != dbx.AlertDown || !strings.HasPrefix(sent[2].Database.Detail, "cache does not answer: ") ||
		sent[2].URL != fmt.Sprintf("https://dash.example.test/databases/%d/alerts", id) {
		t.Fatalf("down: %+v", sent)
	}
	// While it is down, its connections are not read, and not taken as recovered.
	subjects, err := a.Subjects(ctx, &id)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, subject := range subjects {
		rule, _ := a.rule(ctx, subject.RuleID)
		states[rule.Kind] = subject.State
	}
	if states[dbx.AlertDown] != "firing" || states[dbx.AlertConnections] != "unknown" {
		t.Fatalf("subjects while down: %+v", subjects)
	}
}
