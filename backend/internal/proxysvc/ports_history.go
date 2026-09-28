package proxysvc

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// The ports history: when each listening socket appeared and when it went.
//
// The ports page says what is listening now, and the kernel keeps no record
// of what listened before, so "what opened overnight" — the first question
// after a break-in or a deploy gone wrong — had no answer. PortRecorder
// compares the host's sockets with the ones it saw last, once a minute, and
// writes down each stretch of time one socket listened with one owner: from
// the sample that first saw it to the sample that first missed it.

const (
	// PortHistoryInterval is how often the sockets are compared. A change is
	// known to within it, and a socket that opened and closed between two
	// samples is never seen at all.
	PortHistoryInterval = time.Minute
	// PortHistoryRetention is how long a socket that went is remembered.
	PortHistoryRetention = 30 * 24 * time.Hour
	// portSampleTimeout bounds one walk over every process's descriptors,
	// short of the interval so a slow walk never runs into the next one.
	portSampleTimeout = 45 * time.Second
	// portPruneEvery is how often stretches past the retention are deleted.
	portPruneEvery = time.Hour
)

// ListenerObservation is one stretch of time one socket listened with one
// owner.
type ListenerObservation struct {
	ID       int64
	Protocol string
	Family   string
	Address  string
	Port     uint32
	Process  string
	User     string
	PID      int32
	Cmdline  string
	// OpenedAfter is the sample before the one that first saw it: it opened
	// between the two. Nil for a socket already listening when recording
	// began, whose opening nobody saw.
	OpenedAfter *time.Time
	FirstSeen   time.Time
	// GoneAfter is the last sample that saw it and GoneAt the first that
	// did not: it closed between the two. Both nil while it listens.
	GoneAfter *time.Time
	GoneAt    *time.Time
}

// socketEndpoint is what identifies a socket from one sample to the next:
// the kernel keeps one per protocol, family, address and port.
type socketEndpoint struct {
	protocol, family, address string
	port                      uint32
}

func endpointOfListener(l Listener) socketEndpoint {
	return socketEndpoint{l.Protocol, l.Family, l.Address, l.Port}
}

func endpointOfObservation(o ListenerObservation) socketEndpoint {
	return socketEndpoint{o.Protocol, o.Family, o.Address, o.Port}
}

// listenerChanges is what one sample found against the stretches still open.
type listenerChanges struct {
	// opened are the sockets to begin a stretch for: new ones, and ones a
	// different program now holds.
	opened []Listener
	// closed are the stretches that end: their socket went, or changed hands.
	closed []int64
	// refreshed are the stretches that go on, with the owner's details as
	// read now where they differ from what was written.
	refreshed []ListenerObservation
}

// diffListeners compares the stretches still open with the sockets listening
// now.
//
// A socket is the same one while its owner is: the same program, run by the
// same account. A new PID alone — a service restarted between two samples,
// a reload's new master — is the same socket, since from a minute away it
// never stopped listening; its PID and command line are brought up to date.
// An owner that could not be read (PID 0: a process this account cannot see,
// or one that exited mid-walk) matches any, so a sample that happens to lose
// sight of a process does not record it closing and reopening; a name read
// later fills in one that was not.
func diffListeners(open []ListenerObservation, current []Listener) listenerChanges {
	var changes listenerChanges
	byEndpoint := make(map[socketEndpoint]ListenerObservation, len(open))
	for _, o := range open {
		key := endpointOfObservation(o)
		if _, twice := byEndpoint[key]; twice {
			// One socket cannot listen twice; a second stretch still open for
			// it ends here rather than doubling every event after it.
			changes.closed = append(changes.closed, o.ID)
			continue
		}
		byEndpoint[key] = o
	}
	seen := make(map[socketEndpoint]bool, len(current))
	for _, l := range current {
		key := endpointOfListener(l)
		if seen[key] {
			continue
		}
		seen[key] = true
		o, ok := byEndpoint[key]
		switch {
		case !ok:
			changes.opened = append(changes.opened, l)
		case !sameOwner(o, l):
			changes.closed = append(changes.closed, o.ID)
			changes.opened = append(changes.opened, l)
		default:
			if next, changed := refreshedOwner(o, l); changed {
				changes.refreshed = append(changes.refreshed, next)
			}
		}
	}
	for key, o := range byEndpoint {
		if !seen[key] {
			changes.closed = append(changes.closed, o.ID)
		}
	}
	sort.Slice(changes.closed, func(i, j int) bool { return changes.closed[i] < changes.closed[j] })
	return changes
}

// sameOwner reports whether a socket is still held by the program a stretch
// began with. Either side unread is no evidence of a change.
func sameOwner(o ListenerObservation, l Listener) bool {
	if o.Process == "" || l.Process == "" {
		return true
	}
	if o.Process != l.Process {
		return false
	}
	return o.User == "" || l.User == "" || o.User == l.User
}

// refreshedOwner is the stretch with the owner's details as read now, never
// replacing a detail with one that could not be read.
func refreshedOwner(o ListenerObservation, l Listener) (ListenerObservation, bool) {
	next := o
	if l.Process != "" {
		next.Process = l.Process
	}
	if l.User != "" {
		next.User = l.User
	}
	if l.PID > 0 {
		next.PID = l.PID
		next.Cmdline = l.Cmdline
	}
	changed := next.Process != o.Process || next.User != o.User || next.PID != o.PID || next.Cmdline != o.Cmdline
	return next, changed
}

// recordable leaves out a loopback socket on a port the kernel handed out.
// Language servers, test runners and browsers' debugging ports come and go
// by the dozen, never answer off the machine, and would bury the changes
// that matter; the ports page sets them aside on the same test. Without the
// kernel's range there is nothing to judge by, and everything is kept.
func recordable(l Listener, ephemeral *PortRange) bool {
	if ephemeral == nil || l.Scope != ScopeLoopback {
		return true
	}
	return l.Port < ephemeral.Low || l.Port > ephemeral.High
}

// PortRecorder samples the host's listening sockets on its own timer and
// keeps their history in the dashboard's database.
type PortRecorder struct {
	db        *sql.DB
	log       *slog.Logger
	list      func(context.Context) ([]Listener, error)
	ephemeral func() (PortRange, error)
	now       func() time.Time
	interval  time.Duration
	retention time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	// failing is whether the last sample failed, so a host whose walk keeps
	// failing says so once rather than every minute.
	failing bool
}

// NewPortRecorder records against the dashboard's database, whose
// listener_observations and listener_history tables Store.Open creates.
func NewPortRecorder(db *sql.DB, log *slog.Logger) *PortRecorder {
	return &PortRecorder{
		db:        db,
		log:       log,
		list:      ListListeners,
		ephemeral: EphemeralPorts,
		now:       time.Now,
		interval:  PortHistoryInterval,
		retention: PortHistoryRetention,
	}
}

// Start samples at once and then every interval until the context ends or
// Stop is called. It returns straight away: the first walk can take seconds
// on a busy host, and nothing in the boot waits for it.
func (r *PortRecorder) Start(parent context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	r.cancel, r.done = cancel, make(chan struct{})
	go r.loop(ctx, r.done)
	r.log.Info("recording listening sockets", "interval", r.interval.String(), "retention", r.retention.String())
}

// Stop ends sampling and waits for the loop to leave, so a shutdown never
// races a write against the closing database. A recorder never started is
// left as it is.
func (r *PortRecorder) Stop() {
	r.mu.Lock()
	cancel, done := r.cancel, r.done
	r.cancel, r.done = nil, nil
	r.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (r *PortRecorder) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	r.prune(ctx)
	lastPrune := r.now()
	r.sampleAndLog(ctx)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		r.sampleAndLog(ctx)
		if r.now().Sub(lastPrune) >= portPruneEvery {
			lastPrune = r.now()
			r.prune(ctx)
		}
	}
}

func (r *PortRecorder) sampleAndLog(ctx context.Context) {
	pass, cancel := context.WithTimeout(ctx, portSampleTimeout)
	defer cancel()
	_, err := r.Sample(pass)
	if ctx.Err() != nil {
		return
	}
	switch {
	case err != nil && !r.failing:
		r.log.Warn("listening sockets could not be sampled; the ports history has a gap until they can", "error", err)
	case err == nil && r.failing:
		r.log.Info("listening sockets are being sampled again")
	}
	r.failing = err != nil
}

func (r *PortRecorder) prune(ctx context.Context) {
	cutoff := r.now().Add(-r.retention).Unix()
	if _, err := r.db.ExecContext(ctx, `DELETE FROM listener_observations WHERE gone_at IS NOT NULL AND gone_at < ?`, cutoff); err != nil && ctx.Err() == nil {
		r.log.Warn("old ports history could not be pruned", "error", err)
	}
}

// SampleResult is what one sample changed.
type SampleResult struct {
	At time.Time
	// Baseline is the first sample ever taken: what was listening then is
	// where the history starts, not a socket that opened.
	Baseline bool
	Opened   int
	Closed   int
}

// Sample compares the sockets listening now with the stretches still open
// and records what changed. A walk that fails records nothing: taking it for
// every socket closing would fill the history with an outage that never
// happened, so the next sample that works spans the gap instead, and says
// so by how far apart its two samples are.
func (r *PortRecorder) Sample(ctx context.Context) (SampleResult, error) {
	listeners, err := r.list(ctx)
	if err != nil {
		return SampleResult{}, err
	}
	var span *PortRange
	if got, err := r.ephemeral(); err == nil {
		span = &got
	}
	current := make([]Listener, 0, len(listeners))
	for _, l := range listeners {
		if recordable(l, span) {
			current = append(current, l)
		}
	}
	at := r.now().Truncate(time.Second)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return SampleResult{}, err
	}
	defer tx.Rollback()

	var last int64
	err = tx.QueryRowContext(ctx, `SELECT last_sample FROM listener_history WHERE id = 1`).Scan(&last)
	baseline := errors.Is(err, sql.ErrNoRows)
	if err != nil && !baseline {
		return SampleResult{}, fmt.Errorf("read the last sample: %w", err)
	}
	previous := at
	if !baseline && last < at.Unix() {
		// A clock stepped back leaves the previous sample where it was.
		previous = time.Unix(last, 0)
	}
	open, err := scanObservations(tx.QueryContext(ctx, `SELECT `+observationColumns+`
		FROM listener_observations WHERE gone_at IS NULL ORDER BY id`))
	if err != nil {
		return SampleResult{}, fmt.Errorf("read the open sockets: %w", err)
	}
	changes := diffListeners(open, current)

	for _, id := range changes.closed {
		if _, err := tx.ExecContext(ctx, `UPDATE listener_observations SET gone_after = ?, gone_at = ? WHERE id = ?`,
			previous.Unix(), at.Unix(), id); err != nil {
			return SampleResult{}, fmt.Errorf("close a socket's stretch: %w", err)
		}
	}
	for _, o := range changes.refreshed {
		if _, err := tx.ExecContext(ctx, `UPDATE listener_observations SET process = ?, username = ?, pid = ?, cmdline = ? WHERE id = ?`,
			o.Process, o.User, o.PID, o.Cmdline, o.ID); err != nil {
			return SampleResult{}, fmt.Errorf("update a socket's owner: %w", err)
		}
	}
	var openedAfter any
	if !baseline {
		openedAfter = previous.Unix()
	}
	for _, l := range changes.opened {
		if _, err := tx.ExecContext(ctx, `INSERT INTO listener_observations
			(protocol, family, address, port, process, username, pid, cmdline, opened_after, first_seen)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			l.Protocol, l.Family, l.Address, l.Port, l.Process, l.User, l.PID, l.Cmdline, openedAfter, at.Unix()); err != nil {
			return SampleResult{}, fmt.Errorf("open a socket's stretch: %w", err)
		}
	}
	if baseline {
		_, err = tx.ExecContext(ctx, `INSERT INTO listener_history (id, started_at, last_sample) VALUES (1, ?, ?)`, at.Unix(), at.Unix())
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE listener_history SET last_sample = ? WHERE id = 1`, at.Unix())
	}
	if err != nil {
		return SampleResult{}, fmt.Errorf("record the sample: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return SampleResult{}, err
	}
	return SampleResult{At: at, Baseline: baseline, Opened: len(changes.opened), Closed: len(changes.closed)}, nil
}

const observationColumns = `id, protocol, family, address, port, process, username, pid, cmdline,
	opened_after, first_seen, gone_after, gone_at`

func scanObservations(rows *sql.Rows, err error) ([]ListenerObservation, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ListenerObservation{}
	for rows.Next() {
		var o ListenerObservation
		var openedAfter, goneAfter, goneAt sql.NullInt64
		var firstSeen int64
		if err := rows.Scan(&o.ID, &o.Protocol, &o.Family, &o.Address, &o.Port, &o.Process, &o.User, &o.PID, &o.Cmdline,
			&openedAfter, &firstSeen, &goneAfter, &goneAt); err != nil {
			return nil, err
		}
		o.FirstSeen = time.Unix(firstSeen, 0)
		o.OpenedAfter = unixOrNil(openedAfter)
		o.GoneAfter = unixOrNil(goneAfter)
		o.GoneAt = unixOrNil(goneAt)
		out = append(out, o)
	}
	return out, rows.Err()
}

func unixOrNil(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.Unix(v.Int64, 0)
	return &t
}

// FirstSeen says, for each listener, when the recorder first saw it
// listening — nil for one that was already listening when recording began,
// one another program held when last sampled, or one too new to have been
// sampled yet.
func (r *PortRecorder) FirstSeen(ctx context.Context, listeners []Listener) ([]*time.Time, error) {
	open, err := scanObservations(r.db.QueryContext(ctx, `SELECT `+observationColumns+`
		FROM listener_observations WHERE gone_at IS NULL AND opened_after IS NOT NULL`))
	if err != nil {
		return nil, err
	}
	byEndpoint := make(map[socketEndpoint]ListenerObservation, len(open))
	for _, o := range open {
		byEndpoint[endpointOfObservation(o)] = o
	}
	out := make([]*time.Time, len(listeners))
	for i, l := range listeners {
		if o, ok := byEndpoint[endpointOfListener(l)]; ok && sameOwner(o, l) {
			first := o.FirstSeen
			out[i] = &first
		}
	}
	return out, nil
}

// PortEventKind is which end of a stretch an event is.
type PortEventKind string

const (
	PortOpened PortEventKind = "opened"
	PortClosed PortEventKind = "closed"
)

// PortEvent is a socket opening or closing, as the socket was when it did.
type PortEvent struct {
	Listener
	Kind PortEventKind `json:"kind"`
	// At is the sample that saw the change and After the one before it: the
	// socket opened or closed between the two. They are a minute apart while
	// the dashboard runs, and further across a restart or a failed walk.
	At    time.Time `json:"at"`
	After time.Time `json:"after"`
	// Since is when the socket was first seen listening. Baseline says it was
	// already listening when recording began, so it opened before then.
	Since    time.Time `json:"since"`
	Baseline bool      `json:"baseline,omitempty"`
}

// PortHistory is the record of a window of time.
type PortHistory struct {
	// RecordingSince is the first sample ever taken and LastSample the most
	// recent; both null until the first sample is written.
	RecordingSince *time.Time `json:"recordingSince"`
	LastSample     *time.Time `json:"lastSample"`
	// Stalled says the latest sample is older than three intervals: the
	// dashboard is answering, so its walks are failing, and changes since
	// LastSample are not recorded yet.
	Stalled         bool `json:"stalled"`
	IntervalSeconds int  `json:"intervalSeconds"`
	RetentionDays   int  `json:"retentionDays"`
	// Since is the start of the window the events are from.
	Since time.Time `json:"since"`
	// Events are newest first; within one sample by port, a socket's closing
	// before its opening, so a socket that changed hands reads in order.
	Events []PortEvent `json:"events"`
	// Truncated says the window held more events than the limit.
	Truncated bool `json:"truncated"`
}

// History reads the events at or after since, newest first, at most limit
// of them.
func (r *PortRecorder) History(ctx context.Context, since time.Time, limit int) (PortHistory, error) {
	out := PortHistory{
		IntervalSeconds: int(r.interval / time.Second),
		RetentionDays:   int(r.retention / (24 * time.Hour)),
		Since:           since.Truncate(time.Second),
		Events:          []PortEvent{},
	}
	var started, last int64
	err := r.db.QueryRowContext(ctx, `SELECT started_at, last_sample FROM listener_history WHERE id = 1`).Scan(&started, &last)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return out, nil
	case err != nil:
		return out, err
	}
	out.RecordingSince, out.LastSample = unixOrNil(sql.NullInt64{Int64: started, Valid: true}), unixOrNil(sql.NullInt64{Int64: last, Valid: true})
	out.Stalled = r.now().Sub(*out.LastSample) > 3*r.interval

	rows, err := r.db.QueryContext(ctx, `SELECT `+observationColumns+`, kind, at FROM (
			SELECT `+observationColumns+`, 'opened' AS kind, first_seen AS at FROM listener_observations
			WHERE opened_after IS NOT NULL AND first_seen >= ?1
			UNION ALL
			SELECT `+observationColumns+`, 'closed' AS kind, gone_at AS at FROM listener_observations
			WHERE gone_at IS NOT NULL AND gone_at >= ?1
		)
		ORDER BY at DESC, port, protocol, family, address, kind, id
		LIMIT ?2`, since.Unix(), limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var o ListenerObservation
		var openedAfter, goneAfter, goneAt sql.NullInt64
		var firstSeen, at int64
		var kind string
		if err := rows.Scan(&o.ID, &o.Protocol, &o.Family, &o.Address, &o.Port, &o.Process, &o.User, &o.PID, &o.Cmdline,
			&openedAfter, &firstSeen, &goneAfter, &goneAt, &kind, &at); err != nil {
			return out, err
		}
		if len(out.Events) == limit {
			out.Truncated = true
			break
		}
		o.FirstSeen = time.Unix(firstSeen, 0)
		o.OpenedAfter = unixOrNil(openedAfter)
		event := PortEvent{
			Listener: o.listener(),
			Kind:     PortEventKind(kind),
			At:       time.Unix(at, 0),
			Since:    o.FirstSeen,
			Baseline: o.OpenedAfter == nil,
		}
		if event.Kind == PortOpened {
			event.After = *o.OpenedAfter
		} else if goneAfter.Valid {
			event.After = time.Unix(goneAfter.Int64, 0)
		} else {
			event.After = event.At
		}
		out.Events = append(out.Events, event)
	}
	return out, rows.Err()
}

// listener is the socket a stretch is of, as it was recorded.
func (o ListenerObservation) listener() Listener {
	scope := bindScope(o.Address)
	return Listener{
		Protocol: o.Protocol,
		Family:   o.Family,
		Address:  o.Address,
		Port:     o.Port,
		PID:      o.PID,
		Process:  o.Process,
		Cmdline:  o.Cmdline,
		User:     o.User,
		Scope:    scope,
		Exposed:  scope != ScopeLoopback,
	}
}
