package dockerx

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// What Docker did to a deployment's containers, kept past the process.
//
// The ring in EventLog is the host's recent past: two thousand events in
// memory, gone with a restart of the dashboard. For the host feed that is the
// right shape — an hour of context on a busy daemon. For a deployment it is
// not: "has this release been steady for a week" and "why did it restart at
// four this morning" are questions about days, and the one thing certain to
// have happened between four and nine is that somebody restarted the
// dashboard to look. So the events a deployment owns are also written to the
// store as they arrive, and the deployment's feed reads both: the ring for
// the moment, the table for the week.
//
// Only a deployment's events are kept. An arbitrary stack's exec_create every
// thirty seconds is the host feed's problem and the ring's bound already
// answers it; a table that kept every event of every container would be the
// biggest thing in the database within a day.

// DefaultEventRetention is how long a deployment's events are kept. Thirty
// days whatever the metrics retention says, because the availability figure
// over thirty days is read from these rows and a week of samples would leave
// it blank; a longer metrics retention lengthens this one with it.
const DefaultEventRetention = 30 * 24 * time.Hour

// eventPruneEvery is how often expired rows are deleted: once an hour, on
// the event that happens to arrive after the hour, so a quiet host prunes
// nothing and a busy one prunes once.
const eventPruneEvery = time.Hour

// A container in a tight restart loop emits four events a second, and thirty
// days of that is ten million rows about one broken thing. The ring keeps
// every one of them for the hour; the table keeps a container's first burst
// and then a sample — one event every ten seconds — which is still every
// restart Docker's own backoff allows once it has reached its ceiling, and
// enough to draw the loop as the one row the feed folds it into anyway.
const (
	eventBurst  = 30
	eventRefill = 10 * time.Second
)

// A kill explains the exit that follows it, if the exit follows soon enough.
const killExplainsExitWithin = 30 * time.Second

// eventTimelineLimit bounds what the availability figure reads: two hundred
// thousand rows is more than a month of a container looping at the sampled
// rate, and a figure read over fewer rows than that is still a figure.
const eventTimelineLimit = 200_000

// EventStore keeps a deployment's events in the dashboard's database.
type EventStore struct {
	db        *sql.DB
	log       *slog.Logger
	retention time.Duration
	now       func() time.Time

	mu        sync.Mutex
	lastPrune time.Time
	// kills remembers the last kill asked of each container, so the exit
	// that follows one is written down as requested rather than as a crash.
	kills map[string]time.Time
	// buckets is the per-container rate limit; a container not seen for a
	// while is forgotten, so a host that churns containers does not keep a
	// bucket for every one it ever ran.
	buckets map[string]*eventBucket
	warned  time.Time
}

type eventBucket struct {
	tokens float64
	last   time.Time
}

// NewEventStore builds the store over the dashboard's database. retention is
// the metrics history's; the events keep at least DefaultEventRetention.
func NewEventStore(db *sql.DB, log *slog.Logger, retention time.Duration) *EventStore {
	if retention < DefaultEventRetention {
		retention = DefaultEventRetention
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &EventStore{
		db: db, log: log, retention: retention, now: time.Now,
		kills: map[string]time.Time{}, buckets: map[string]*eventBucket{},
	}
}

// Retention is how long a row is kept.
func (s *EventStore) Retention() time.Duration { return s.retention }

// Follow writes down every event the log records that a deployment owns, and
// prunes expired rows as it goes. The first prune happens here, so a long
// retention shortened by an operator takes effect at the next start rather
// than an hour after the next event.
func (s *EventStore) Follow(ctx context.Context, events *EventLog) {
	if s == nil || s.db == nil || events == nil {
		return
	}
	if _, err := s.Prune(ctx); err != nil && ctx.Err() == nil {
		s.log.Warn("deployment events prune failed", "error", err)
	}
	events.Keep(func(ev Event) {
		if ctx.Err() != nil {
			return
		}
		// Bounded: the keeper runs on the goroutine reading the daemon's
		// stream, and a database that has stopped answering must cost the
		// ring nothing more than a moment.
		keepCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := s.Record(keepCtx, ev); err != nil {
			s.warn("deployment event not recorded", err)
		}
		s.pruneDue(keepCtx)
	})
}

// warn logs a failure at most once a minute: a database that will not take
// writes says so for every event, and a crash loop would turn that into a
// log line four times a second.
func (s *EventStore) warn(msg string, err error) {
	s.mu.Lock()
	due := s.now().Sub(s.warned) >= time.Minute
	if due {
		s.warned = s.now()
	}
	s.mu.Unlock()
	if due {
		s.log.Warn(msg, "error", err)
	}
}

func (s *EventStore) pruneDue(ctx context.Context) {
	s.mu.Lock()
	due := s.now().Sub(s.lastPrune) >= eventPruneEvery
	s.mu.Unlock()
	if !due {
		return
	}
	if _, err := s.Prune(ctx); err != nil {
		s.warn("deployment events prune failed", err)
	}
}

// Record writes one event down if a deployment owns it, and reports whether
// it did. Everything else the daemon says is left to the ring.
func (s *EventStore) Record(ctx context.Context, ev Event) (bool, error) {
	if s == nil || s.db == nil {
		return false, nil
	}
	environmentID, ok := ev.EnvironmentID()
	if !ok || ev.Time.IsZero() {
		return false, nil
	}
	key := ev.ID
	if key == "" {
		key = ev.Type + ":" + ev.Name
	}
	requested := s.noteRequest(key, ev)
	if !s.admit(key, ev.Time) {
		return false, nil
	}
	// Trigger is left out on purpose: who in this dashboard caused an event
	// is read from the audit log when the row is shown, as it is for the
	// ring, so a correlation made later is not frozen as it stood.
	stored := ev
	stored.Trigger = nil
	body, err := json.Marshal(stored)
	if err != nil {
		return false, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO deploy_container_events
		(ts_nano, environment_id, release_id, release_number, run_id, kind, action, name, container_id,
		 exit_code, signal, requested, source, event_json)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		ev.Time.UnixNano(), environmentID, ev.ownerInt("release-id"), ev.ownerInt("release-number"), ev.ownerInt("run-id"),
		ev.Type, ev.Action, ev.Name, ev.ID, ev.ExitCode, ev.Signal, boolInt(requested), ev.Source, string(body))
	if err != nil {
		return false, err
	}
	return true, nil
}

// noteRequest says whether this event was asked for through the daemon: a
// stop or a kill always was, and an exit within a few seconds of a kill of
// the same container is that kill taking effect rather than a crash. The
// distinction is the one the availability figure and a reader of the feed
// both want on a `die`: somebody stopped it, or it fell over.
func (s *EventStore) noteRequest(key string, ev Event) bool {
	if ev.Type != "container" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch ev.Action {
	case "kill", "stop":
		s.kills[key] = ev.Time
		s.forgetOldKills(ev.Time)
		return true
	case "die":
		at, ok := s.kills[key]
		return ok && !ev.Time.Before(at) && ev.Time.Sub(at) <= killExplainsExitWithin
	case "destroy":
		delete(s.kills, key)
	}
	return false
}

func (s *EventStore) forgetOldKills(now time.Time) {
	if len(s.kills) < 1024 {
		return
	}
	for key, at := range s.kills {
		if now.Sub(at) > killExplainsExitWithin {
			delete(s.kills, key)
		}
	}
}

// admit is the per-container rate limit: a token bucket of eventBurst,
// refilled one every eventRefill.
func (s *EventStore) admit(key string, at time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.buckets[key]
	if b == nil {
		if len(s.buckets) >= 4096 {
			for k, other := range s.buckets {
				if at.Sub(other.last) > time.Hour {
					delete(s.buckets, k)
				}
			}
		}
		b = &eventBucket{tokens: eventBurst, last: at}
		s.buckets[key] = b
	}
	if at.After(b.last) {
		b.tokens = min(eventBurst, b.tokens+at.Sub(b.last).Seconds()/eventRefill.Seconds())
		b.last = at
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Prune deletes rows past the retention and reports how many went.
func (s *EventStore) Prune(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	s.mu.Lock()
	s.lastPrune = s.now()
	s.mu.Unlock()
	res, err := s.db.ExecContext(ctx, `DELETE FROM deploy_container_events WHERE ts_nano < ?`,
		s.now().Add(-s.retention).UnixNano())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// Since is the time of the oldest row kept, for any deployment: how far back
// the record reaches. Zero when nothing has been written down yet. It is the
// store's reach rather than one environment's, because an environment with
// no events over a week has been steady for a week only if the store was
// listening for that week.
func (s *EventStore) Since(ctx context.Context) (time.Time, error) {
	if s == nil || s.db == nil {
		return time.Time{}, nil
	}
	var oldest sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MIN(ts_nano) FROM deploy_container_events`).Scan(&oldest); err != nil {
		return time.Time{}, err
	}
	if !oldest.Valid {
		return time.Time{}, nil
	}
	return time.Unix(0, oldest.Int64).UTC(), nil
}

// Find answers the feed's question for one environment, newest first: the
// kinds and the text the filter names, from `since` on, at most `limit`.
func (s *EventStore) Find(ctx context.Context, environmentID int64, f EventFilter, since time.Time, limit int) ([]Event, error) {
	if s == nil || s.db == nil {
		return []Event{}, nil
	}
	if limit <= 0 {
		limit = 200
	}
	query, args := eventQuery(environmentID, f.Kinds, since)
	// The text filter runs in Go over the rendered event, as the ring's
	// does, so the two halves of the feed agree on what matches; the scan is
	// widened so a narrow search still finds its rows among a wide record.
	scan := limit
	if strings.TrimSpace(f.Search) != "" {
		scan = max(limit, 2000)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY ts_nano DESC, id DESC LIMIT ?`, append(args, scan)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		ev, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		if !f.Match(ev) {
			continue
		}
		out = append(out, ev)
		if len(out) >= limit {
			break
		}
	}
	return out, rows.Err()
}

// Timeline is one environment's container events from `since` on, oldest
// first, for the availability figure: the order a state machine wants.
func (s *EventStore) Timeline(ctx context.Context, environmentID int64, since time.Time) ([]Event, error) {
	if s == nil || s.db == nil {
		return []Event{}, nil
	}
	query, args := eventQuery(environmentID, []string{"container"}, since)
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY ts_nano ASC, id ASC LIMIT ?`, append(args, eventTimelineLimit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		ev, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func eventQuery(environmentID int64, kinds []string, since time.Time) (string, []any) {
	var b strings.Builder
	b.WriteString(`SELECT ts_nano, requested, event_json FROM deploy_container_events WHERE environment_id = ?`)
	args := []any{environmentID}
	if !since.IsZero() {
		b.WriteString(` AND ts_nano >= ?`)
		args = append(args, since.UnixNano())
	}
	named := []string{}
	for _, kind := range kinds {
		if kind != "" {
			named = append(named, kind)
		}
	}
	if len(named) > 0 {
		b.WriteString(` AND kind IN (` + strings.TrimSuffix(strings.Repeat("?,", len(named)), ",") + `)`)
		for _, kind := range named {
			args = append(args, kind)
		}
	}
	return b.String(), args
}

type rowScanner interface{ Scan(dest ...any) error }

// scanEvent rebuilds the event as it arrived. The time is the row's own
// rather than the JSON's, so the nanosecond the ring holds is the one the
// feed dedupes on.
func scanEvent(row rowScanner) (Event, error) {
	var ts int64
	var requested int
	var body string
	if err := row.Scan(&ts, &requested, &body); err != nil {
		return Event{}, err
	}
	var ev Event
	if err := json.Unmarshal([]byte(body), &ev); err != nil {
		return Event{}, fmt.Errorf("deploy_container_events row at %d: %w", ts, err)
	}
	ev.Time = time.Unix(0, ts).UTC()
	ev.Requested = requested == 1
	return ev, nil
}

// ContainerRef names one container a release ran, as its events named it.
type ContainerRef struct {
	ID   string
	Name string
}

// ReleaseContainers is every container the record saw start for one release
// of one environment: the names the exposition reads the latest sample of,
// known without asking the daemon.
func (s *EventStore) ReleaseContainers(ctx context.Context, environmentID, releaseID int64) ([]ContainerRef, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT container_id, name FROM deploy_container_events
		WHERE environment_id = ? AND release_id = ? AND kind = 'container' AND action IN ('create', 'start') AND name <> ''
		ORDER BY name`, environmentID, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ContainerRef{}
	for rows.Next() {
		var ref ContainerRef
		if err := rows.Scan(&ref.ID, &ref.Name); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// EventCounts is what the events table says about one release's containers,
// for the exposition: how often they came back, how often the kernel stopped
// them, and how they exited.
type EventCounts struct {
	// Restarts is every start of a container after its first: a restart
	// policy firing, a `docker restart`, an operator's start after a stop.
	Restarts int
	OOMKills int
	// Exits is the number of exits by status, as Docker reported it.
	Exits map[string]int
}

// ReleaseCounts reads the counts for one release of one environment.
func (s *EventStore) ReleaseCounts(ctx context.Context, environmentID, releaseID int64) (EventCounts, error) {
	counts := EventCounts{Exits: map[string]int{}}
	if s == nil || s.db == nil {
		return counts, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT action, container_id, exit_code, COUNT(*)
		FROM deploy_container_events
		WHERE environment_id = ? AND release_id = ? AND kind = 'container' AND action IN ('start', 'oom', 'die')
		GROUP BY action, container_id, exit_code`, environmentID, releaseID)
	if err != nil {
		return counts, err
	}
	defer rows.Close()
	for rows.Next() {
		var action, container, exitCode string
		var n int
		if err := rows.Scan(&action, &container, &exitCode, &n); err != nil {
			return counts, err
		}
		switch action {
		case "start":
			// The first start of each container is the release arriving;
			// every one after it is the container coming back.
			counts.Restarts += max(0, n-1)
		case "oom":
			counts.OOMKills += n
		case "die":
			counts.Exits[exitCode] += n
		}
	}
	return counts, rows.Err()
}
