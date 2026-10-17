package api

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// Statement history (dbx/statements_history.go), recorded beside the
// activity recorder and stopped with it: every five minutes each saved SQL
// server's statement list is read, and what each statement added since the
// reading before is kept for seven days.
//
// The reading is the statement list's own (dbx.TopStatements), so the text
// kept of a statement is what the Statements view shows any role — its shape
// — and each reading writes it again, so a statement still in the list is
// kept as the list shapes it now. No other read of a statement's text is
// made for the history.
//
// Reading a history needs no dial: it is the dashboard's own store, there
// while the server is down, as the activity history is.

const (
	statementSnapshotEvery    = 5 * time.Minute
	statementHistoryRetention = 7 * 24 * time.Hour
	// A snapshot older than this is not differenced against: the interval
	// would put an outage's work on a single point.
	statementSnapshotStale = 3 * statementSnapshotEvery
	// statementsKeptPerInterval is how many statements one interval keeps:
	// the ones that took the most time in it. The rest are in its total.
	statementsKeptPerInterval = 50
)

// statementHistory is what the statement recorder holds between readings.
type statementHistory struct {
	mu     sync.Mutex
	done   chan struct{}
	offset int
	// last is each connection's newest snapshot, the one the next is
	// differenced against, with the target it was taken of.
	last map[int64]statementBaseline
	// read is how each connection's newest reading went, for its page.
	read map[int64]statementReading
	// reset marks a connection whose counters were zeroed from here since its
	// newest snapshot.
	reset map[int64]bool
}

type statementBaseline struct {
	identity string
	snap     *dbx.StatementSnapshot
}

// statementReading is how the newest attempt to read a server's statements
// went: when, and what stood in the way if nothing could be read.
type statementReading struct {
	At        time.Time `json:"at"`
	Supported bool      `json:"supported"`
	Reason    string    `json:"reason,omitempty"`
	rowsRead  bool
	durations bool
}

// before is the snapshot the next one is taken against, and whether the
// counters were zeroed from here since it. A snapshot of another target, or
// one too old to difference against, is dropped.
func (h *statementHistory) before(id int64, identity string) (*dbx.StatementSnapshot, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	reset := h.reset[id]
	delete(h.reset, id)
	last, ok := h.last[id]
	if !ok || last.identity != identity || time.Since(last.snap.At) > statementSnapshotStale {
		delete(h.last, id)
		return nil, reset
	}
	return last.snap, reset
}

func (h *statementHistory) keep(id int64, identity string, snap *dbx.StatementSnapshot) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.last == nil {
		h.last = map[int64]statementBaseline{}
	}
	h.last[id] = statementBaseline{identity: identity, snap: snap}
}

func (h *statementHistory) forget(id int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.last, id)
}

func (h *statementHistory) noteRead(id int64, reading statementReading) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.read == nil {
		h.read = map[int64]statementReading{}
	}
	h.read[id] = reading
}

func (h *statementHistory) reading(id int64) (statementReading, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	reading, ok := h.read[id]
	return reading, ok
}

// noteReset marks the counters zeroed from here. MySQL keeps no time of its
// last reset, and a statement run more often after the TRUNCATE than before
// it would otherwise read as having run only the difference.
func (h *statementHistory) noteReset(id int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.reset == nil {
		h.reset = map[int64]bool{}
	}
	h.reset[id] = true
}

func (s *Server) startStatementHistory(ctx context.Context) {
	s.dbStatements.done = make(chan struct{})
	go func() {
		defer close(s.dbStatements.done)
		timer := time.NewTicker(statementSnapshotEvery)
		defer timer.Stop()
		for {
			s.recordStatementHistory(ctx)
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}
	}()
}

func (s *Server) recordStatementHistory(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, statementSnapshotEvery-time.Minute)
	defer cancel()
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT `+dbConnColumns+` FROM db_connections ORDER BY id`)
	if err != nil {
		s.Log.Warn("statement history connections could not be read", "err", err)
		return
	}
	var records []*dbConnRecord
	for rows.Next() {
		rec, err := scanDBConn(rows.Scan)
		if err != nil {
			rows.Close()
			return
		}
		if dbx.Capabilities(rec.conn.Driver, "")["statementHistory"] == true {
			records = append(records, rec)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return
	}
	// Rotated, as the activity recorder's queue is, so servers that time out
	// cannot leave the same ones unread on every cycle. Two workers: a
	// reading is up to two hundred statements and their figures.
	if len(records) > 0 {
		start := s.dbStatements.offset % len(records)
		records = append(records[start:], records[:start]...)
		s.dbStatements.offset = (start + 2) % len(records)
	}
	jobs := make(chan *dbConnRecord)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			for rec := range jobs {
				s.recordStatementSnapshot(ctx, rec)
			}
		})
	}
send:
	for _, rec := range records {
		select {
		case jobs <- rec:
		case <-ctx.Done():
			break send
		}
	}
	close(jobs)
	workers.Wait()
	pruneCtx, pruneCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer pruneCancel()
	s.pruneStatementHistory(pruneCtx, time.Now().Add(-statementHistoryRetention))
}

func (s *Server) recordStatementSnapshot(ctx context.Context, rec *dbConnRecord) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, dsn, err := s.openDBConn(rec)
	if err != nil {
		return
	}
	identity := databaseMetricIdentity(rec)
	prev, resetHere := s.dbStatements.before(conn.ID, identity)
	failed := func(reason string) {
		s.dbStatements.noteRead(conn.ID, statementReading{At: time.Now().UTC(), Reason: reason})
		if resetHere {
			s.dbStatements.noteReset(conn.ID)
		}
	}
	pool, err := s.modules.dbs.Pool(ctx, conn.ID, conn.Driver, dsn)
	if err != nil {
		failed(connectError(dsn, err))
		return
	}
	snap, err := dbx.TakeStatementSnapshot(ctx, pool, conn.Driver, prev)
	if err != nil {
		failed(connectError(dsn, err))
		return
	}
	s.dbStatements.noteRead(conn.ID, statementReading{
		At: snap.At, Supported: snap.Supported, Reason: snap.Reason, rowsRead: snap.RowsRead, durations: snap.Durations,
	})
	if !snap.Supported {
		s.dbStatements.forget(conn.ID)
		return
	}
	s.dbStatements.keep(conn.ID, identity, snap)
	if prev != nil {
		s.saveStatementInterval(ctx, rec, identity, snap, dbx.StatementsBetween(prev, snap, resetHere))
	}
}

// saveStatementInterval keeps one interval: its whole, and the statements
// that took the most time in it. A connection edited or forgotten while the
// read ran keeps nothing of its previous target.
func (s *Server) saveStatementInterval(ctx context.Context, rec *dbConnRecord, identity string,
	snap *dbx.StatementSnapshot, interval *dbx.StatementsInterval) {
	err := func() error {
		tx, err := s.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		at := interval.To.UnixMilli()
		res, err := tx.ExecContext(ctx, `INSERT INTO db_statement_intervals(connection_id, identity, at, seconds, total_ms, reset)
			SELECT id, ?, ?, ?, ?, ? FROM db_connections WHERE id=? AND driver=? AND dsn_enc=?`,
			identity, at, int64(interval.To.Sub(interval.From).Round(time.Second).Seconds()), interval.TotalMs, interval.Reset,
			rec.conn.ID, rec.conn.Driver, rec.dsnEnc)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return err
		}
		// Every statement still in the list is kept as the list shows it
		// now, whether or not it ran in this interval.
		for id, query := range snap.Texts {
			if _, err := tx.ExecContext(ctx, `UPDATE db_statement_shapes SET query=?
				WHERE connection_id=? AND identity=? AND statement=? AND query<>?`, query, rec.conn.ID, identity, id, query); err != nil {
				return err
			}
		}
		kept := 0
		for _, run := range interval.Statements {
			if kept == statementsKeptPerInterval {
				break
			}
			// ClickHouse's window can hold a statement the day's list does not.
			query := snap.Texts[run.ID]
			if query == "" {
				continue
			}
			var shape int64
			if err := tx.QueryRowContext(ctx, `INSERT INTO db_statement_shapes(connection_id, identity, statement, query, seen_at)
				VALUES(?,?,?,?,?) ON CONFLICT(connection_id, identity, statement)
				DO UPDATE SET query=excluded.query, seen_at=excluded.seen_at RETURNING id`,
				rec.conn.ID, identity, run.ID, query, at).Scan(&shape); err != nil {
				return err
			}
			var examined any
			if run.RowsRead >= 0 {
				examined = run.RowsRead
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO db_statement_samples(shape_id, at, calls, total_ms, returned, examined, durations)
				VALUES(?,?,?,?,?,?,?)`, shape, at, run.Calls, run.TotalMs, run.Rows, examined, run.Durations.String()); err != nil {
				return err
			}
			kept++
		}
		return tx.Commit()
	}()
	if err != nil && ctx.Err() == nil {
		s.Log.Warn("statement history could not be saved", "connection", rec.conn.ID, "err", err)
	}
}

// pruneStatementHistory drops what is older than the cutoff, and the shapes
// no sample is left of.
func (s *Server) pruneStatementHistory(ctx context.Context, cutoff time.Time) {
	at := cutoff.UnixMilli()
	for _, stmt := range []string{
		`DELETE FROM db_statement_samples WHERE at < ?`,
		`DELETE FROM db_statement_intervals WHERE at < ?`,
		`DELETE FROM db_statement_shapes WHERE seen_at < ?
		   AND NOT EXISTS (SELECT 1 FROM db_statement_samples WHERE shape_id = db_statement_shapes.id)`,
	} {
		if _, err := s.Store.DB.ExecContext(ctx, stmt, at); err != nil {
			s.Log.Warn("statement history could not be pruned", "err", err)
			return
		}
	}
}

// --- reading a history -------------------------------------------------------

// The orders a history can be listed in; rowsRead and p95 only mean
// something where the engine counts them, and a statement without the
// figure is listed after the ones with it.
var statementHistorySorts = []string{"total", "mean", "calls", "rows", "rowsRead", "p95"}

// How many points a window is cut into: the list's trend is a sparkline, the
// one statement's charts are read off.
const (
	statementTrendPoints = 24
	statementChartPoints = 120
)

// statementBucket is a window's step for about that many points: a whole
// number of readings, and never less than one.
func statementBucket(window time.Duration, points int) time.Duration {
	readings := (window/time.Duration(points) + statementSnapshotEvery - 1) / statementSnapshotEvery
	return max(readings, 1) * statementSnapshotEvery
}

// statementFacts is what is known of a window, beside the statements in it.
type statementFacts struct {
	Supported       bool      `json:"supported"`
	Reason          string    `json:"reason,omitempty"`
	Hours           int       `json:"hours"`
	EverySeconds    int       `json:"everySeconds"`
	RetentionHours  int       `json:"retentionHours"`
	KeptPerInterval int       `json:"keptPerInterval"`
	BucketSeconds   int       `json:"bucketSeconds"`
	From            time.Time `json:"from"`
	Until           time.Time `json:"until"`
	// Intervals is how many readings were differenced in the window, and
	// RecordedSeconds how much of it they cover.
	Intervals       int        `json:"intervals"`
	RecordedSeconds int64      `json:"recordedSeconds"`
	FirstAt         *time.Time `json:"firstAt,omitempty"`
	// TotalMs is the time of every statement the engine counted in the
	// window, the ones not kept included.
	TotalMs     float64 `json:"totalMs"`
	RowsRead    bool    `json:"rowsRead"`
	Percentiles bool    `json:"percentiles"`
	// Resets are the readings across which the counters started again.
	Resets []time.Time `json:"resets"`
	// Reading is how the recorder's newest reading of this server went, since
	// this process started, and NextAt when it reads again.
	Reading *statementReading `json:"reading,omitempty"`
	NextAt  *time.Time        `json:"nextAt,omitempty"`
}

type statementHistoryEntry struct {
	ID       string   `json:"id"`
	Query    string   `json:"query"`
	Calls    int64    `json:"calls"`
	TotalMs  float64  `json:"totalMs"`
	MeanMs   float64  `json:"meanMs"`
	Rows     int64    `json:"rows"`
	RowsRead *int64   `json:"rowsRead,omitempty"`
	P50Ms    *float64 `json:"p50Ms,omitempty"`
	P95Ms    *float64 `json:"p95Ms,omitempty"`
	P99Ms    *float64 `json:"p99Ms,omitempty"`
	// Share is of the time of every statement counted in the window.
	Share float64 `json:"share"`
	// Trend is the time the statement took in each of the report's buckets,
	// and null for one nothing was recorded in.
	Trend []*float64 `json:"trend,omitempty"`
}

type statementHistoryReport struct {
	statementFacts
	Sort string `json:"sort"`
	// Buckets are where each point of a trend ends, in milliseconds.
	Buckets    []int64                 `json:"buckets"`
	Statements []statementHistoryEntry `json:"statements"`
}

type statementHistoryPoint struct {
	// At is where the point's bucket ends, in milliseconds.
	At       int64    `json:"at"`
	Recorded bool     `json:"recorded"`
	Calls    int64    `json:"calls"`
	TotalMs  float64  `json:"totalMs"`
	MeanMs   *float64 `json:"meanMs,omitempty"`
	Rows     int64    `json:"rows"`
	RowsRead *int64   `json:"rowsRead,omitempty"`
	P50Ms    *float64 `json:"p50Ms,omitempty"`
	P95Ms    *float64 `json:"p95Ms,omitempty"`
	P99Ms    *float64 `json:"p99Ms,omitempty"`
}

type statementHistoryDetail struct {
	statementFacts
	Statement statementHistoryEntry   `json:"statement"`
	Points    []statementHistoryPoint `json:"points"`
}

// statementTally is one statement's figures over a window, and in each of
// its buckets.
type statementTally struct {
	id, query string
	all       dbx.StatementFigures
	buckets   map[int64]*dbx.StatementFigures
}

// statementWindow is a connection's recorded history over one window.
type statementWindow struct {
	facts    statementFacts
	bucket   int64
	first    int64
	last     int64
	recorded map[int64]bool
	tallies  map[string]*statementTally
}

func newFigures() dbx.StatementFigures { return dbx.StatementFigures{RowsRead: -1} }

func addFigures(into *dbx.StatementFigures, calls int64, totalMs float64, rows int64, examined sql.NullInt64, durations dbx.Durations) {
	into.Calls += calls
	into.TotalMs += totalMs
	into.Rows += rows
	if examined.Valid {
		into.RowsRead = max(into.RowsRead, 0) + examined.Int64
	}
	if durations != nil {
		if into.Durations == nil {
			into.Durations = dbx.Durations{}
		}
		into.Durations.Add(durations)
	}
}

// statementHistoryRequest is the connection a history is asked of, and the
// window in hours.
func (s *Server) statementHistoryRequest(r *http.Request) (*dbConnRecord, int, error) {
	id, err := parseID(r)
	if err != nil {
		return nil, 0, err
	}
	rec, err := scanDBConn(s.Store.DB.QueryRowContext(r.Context(),
		`SELECT `+dbConnColumns+` FROM db_connections WHERE id=?`, id).Scan)
	if err == sql.ErrNoRows {
		return nil, 0, httpx.ErrNotFound
	}
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	if !rec.conn.Driver.IsSQL() {
		return nil, 0, httpx.BadRequest("this endpoint is for SQL engines; %s uses its own surface", rec.conn.Driver)
	}
	hours := 1
	if raw := r.URL.Query().Get("hours"); raw != "" {
		hours, err = strconv.Atoi(raw)
		if err != nil || hours < 1 || hours > 168 {
			return nil, 0, httpx.BadRequest("hours must be between 1 and 168")
		}
	}
	return rec, hours, nil
}

// readStatementWindow reads a connection's history over the last hours, in
// buckets of about that many points, for every statement or for one.
func (s *Server) readStatementWindow(ctx context.Context, rec *dbConnRecord, hours, points int, only string) (*statementWindow, error) {
	until := time.Now().UTC()
	window := time.Duration(hours) * time.Hour
	bucket := statementBucket(window, points)
	out := &statementWindow{
		facts: statementFacts{
			Supported: true, Hours: hours, EverySeconds: int(statementSnapshotEvery.Seconds()),
			RetentionHours: int(statementHistoryRetention.Hours()), KeptPerInterval: statementsKeptPerInterval,
			BucketSeconds: int(bucket.Seconds()), From: until.Add(-window), Until: until, Resets: []time.Time{},
		},
		bucket: bucket.Milliseconds(), recorded: map[int64]bool{}, tallies: map[string]*statementTally{},
	}
	out.first, out.last = out.facts.From.UnixMilli()/out.bucket, until.UnixMilli()/out.bucket
	if reading, ok := s.dbStatements.reading(rec.conn.ID); ok {
		out.facts.Reading = &reading
		next := reading.At.Add(statementSnapshotEvery)
		out.facts.NextAt = &next
		out.facts.RowsRead, out.facts.Percentiles = reading.rowsRead, reading.durations
	}
	identity := databaseMetricIdentity(rec)
	from := out.facts.From.UnixMilli()

	rows, err := s.Store.DB.QueryContext(ctx, `SELECT at, seconds, total_ms, reset FROM db_statement_intervals
		WHERE connection_id=? AND identity=? AND at>? ORDER BY at`, rec.conn.ID, identity, from)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var at, seconds int64
		var total float64
		var reset bool
		if err := rows.Scan(&at, &seconds, &total, &reset); err != nil {
			rows.Close()
			return nil, err
		}
		if out.facts.Intervals == 0 {
			first := time.UnixMilli(at - seconds*1000).UTC()
			out.facts.FirstAt = &first
		}
		out.facts.Intervals++
		out.facts.RecordedSeconds += seconds
		out.facts.TotalMs += total
		out.recorded[at/out.bucket] = true
		if reset {
			out.facts.Resets = append(out.facts.Resets, time.UnixMilli(at).UTC())
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}

	query := `SELECT sh.statement, sh.query, sa.at, sa.calls, sa.total_ms, sa.returned, sa.examined, sa.durations
		FROM db_statement_shapes sh JOIN db_statement_samples sa ON sa.shape_id = sh.id
		WHERE sh.connection_id=? AND sh.identity=? AND sa.at>?`
	args := []any{rec.conn.ID, identity, from}
	if only != "" {
		query += ` AND sh.statement=?`
		args = append(args, only)
	}
	rows, err = s.Store.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, text, durationsText string
		var at, calls, returned int64
		var total float64
		var examined sql.NullInt64
		if err := rows.Scan(&id, &text, &at, &calls, &total, &returned, &examined, &durationsText); err != nil {
			return nil, err
		}
		tally := out.tallies[id]
		if tally == nil {
			tally = &statementTally{id: id, query: text, all: newFigures(), buckets: map[int64]*dbx.StatementFigures{}}
			out.tallies[id] = tally
		}
		durations := dbx.ParseDurations(durationsText)
		addFigures(&tally.all, calls, total, returned, examined, durations)
		b := at / out.bucket
		inBucket := tally.buckets[b]
		if inBucket == nil {
			f := newFigures()
			inBucket = &f
			tally.buckets[b] = inBucket
		}
		addFigures(inBucket, calls, total, returned, examined, durations)
		out.facts.RowsRead = out.facts.RowsRead || examined.Valid
		out.facts.Percentiles = out.facts.Percentiles || durations != nil
	}
	return out, rows.Err()
}

// quantiles are a statement's p50, p95 and p99, where its durations are known.
func quantiles(d dbx.Durations) (p50, p95, p99 *float64) {
	at := func(q float64) *float64 {
		if v, ok := d.Quantile(q); ok {
			return &v
		}
		return nil
	}
	return at(0.5), at(0.95), at(0.99)
}

func statementEntry(t *statementTally, totalMs float64) statementHistoryEntry {
	f := t.all
	entry := statementHistoryEntry{ID: t.id, Query: t.query, Calls: f.Calls, TotalMs: f.TotalMs, Rows: f.Rows}
	if f.Calls > 0 {
		entry.MeanMs = f.TotalMs / float64(f.Calls)
	}
	if f.RowsRead >= 0 {
		rowsRead := f.RowsRead
		entry.RowsRead = &rowsRead
	}
	if totalMs > 0 {
		entry.Share = min(f.TotalMs/totalMs, 1)
	}
	entry.P50Ms, entry.P95Ms, entry.P99Ms = quantiles(f.Durations)
	return entry
}

// historyOrder ranks entries by the sort asked for, the most first; one
// without the figure comes after every one with it, and a tie goes to the
// statement that took more time.
func historyOrder(entries []statementHistoryEntry, by string) {
	key := func(e statementHistoryEntry) (float64, bool) {
		switch by {
		case "mean":
			return e.MeanMs, true
		case "calls":
			return float64(e.Calls), true
		case "rows":
			return float64(e.Rows), true
		case "rowsRead":
			if e.RowsRead == nil {
				return 0, false
			}
			return float64(*e.RowsRead), true
		case "p95":
			if e.P95Ms == nil {
				return 0, false
			}
			return *e.P95Ms, true
		}
		return e.TotalMs, true
	}
	sort.SliceStable(entries, func(i, j int) bool {
		a, aOK := key(entries[i])
		b, bOK := key(entries[j])
		switch {
		case aOK != bOK:
			return aOK
		case a != b:
			return a > b
		case entries[i].TotalMs != entries[j].TotalMs:
			return entries[i].TotalMs > entries[j].TotalMs
		}
		return entries[i].ID < entries[j].ID
	})
}

// statementHistoryUnsupported is the answer for a SQL engine that keeps no
// statement statistics: SQLite.
func statementHistoryUnsupported(hours int) statementFacts {
	return statementFacts{
		Hours: hours, EverySeconds: int(statementSnapshotEvery.Seconds()),
		RetentionHours: int(statementHistoryRetention.Hours()), Resets: []time.Time{},
		Reason: "this engine keeps no per-statement statistics",
	}
}

// handleDBStatementHistory lists the statements that cost the most over a
// recorded window, each with its trend across it.
func (s *Server) handleDBStatementHistory(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	rec, hours, err := s.statementHistoryRequest(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	by := strings.TrimSpace(q.Get("sort"))
	if by == "" {
		by = "total"
	}
	known := false
	for _, name := range statementHistorySorts {
		known = known || name == by
	}
	if !known {
		return httpx.BadRequest("sort must be one of %s", strings.Join(statementHistorySorts, ", "))
	}
	limit := min(max(atoiDefault(q.Get("limit"), 50), 1), 200)
	if dbx.Capabilities(rec.conn.Driver, "")["statementHistory"] != true {
		httpx.JSON(w, http.StatusOK, statementHistoryReport{
			statementFacts: statementHistoryUnsupported(hours), Sort: by, Buckets: []int64{}, Statements: []statementHistoryEntry{},
		})
		return nil
	}
	window, err := s.readStatementWindow(r.Context(), rec, hours, statementTrendPoints, "")
	if err != nil {
		return httpx.Internal(err)
	}
	report := statementHistoryReport{statementFacts: window.facts, Sort: by, Buckets: []int64{}, Statements: []statementHistoryEntry{}}
	for b := window.first; b <= window.last; b++ {
		report.Buckets = append(report.Buckets, min((b+1)*window.bucket, report.Until.UnixMilli()))
	}
	for _, tally := range window.tallies {
		report.Statements = append(report.Statements, statementEntry(tally, window.facts.TotalMs))
	}
	historyOrder(report.Statements, by)
	if len(report.Statements) > limit {
		report.Statements = report.Statements[:limit]
	}
	for i := range report.Statements {
		tally := window.tallies[report.Statements[i].ID]
		trend := make([]*float64, 0, len(report.Buckets))
		for b := window.first; b <= window.last; b++ {
			switch f := tally.buckets[b]; {
			case f != nil:
				trend = append(trend, &f.TotalMs)
			case window.recorded[b]:
				trend = append(trend, new(float64))
			default:
				trend = append(trend, nil)
			}
		}
		report.Statements[i].Trend = trend
	}
	httpx.JSON(w, http.StatusOK, report)
	return nil
}

// handleDBStatementHistoryOne is one statement's history over a window: its
// figures across it and in each of its buckets, for charts.
func (s *Server) handleDBStatementHistoryOne(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	rec, hours, err := s.statementHistoryRequest(r)
	if err != nil {
		return err
	}
	id := chi.URLParam(r, "statement")
	if id == "" || len(id) > 128 {
		return httpx.BadRequest("a statement id is required")
	}
	var query string
	err = s.Store.DB.QueryRowContext(r.Context(), `SELECT query FROM db_statement_shapes
		WHERE connection_id=? AND identity=? AND statement=?`, rec.conn.ID, databaseMetricIdentity(rec), id).Scan(&query)
	if err == sql.ErrNoRows {
		return httpx.Err(http.StatusNotFound, "not_found", "no history is recorded for this statement")
	}
	if err != nil {
		return httpx.Internal(err)
	}
	window, err := s.readStatementWindow(r.Context(), rec, hours, statementChartPoints, id)
	if err != nil {
		return httpx.Internal(err)
	}
	tally := window.tallies[id]
	if tally == nil {
		tally = &statementTally{id: id, all: newFigures(), buckets: map[int64]*dbx.StatementFigures{}}
	}
	tally.query = query
	detail := statementHistoryDetail{
		statementFacts: window.facts, Statement: statementEntry(tally, window.facts.TotalMs), Points: []statementHistoryPoint{},
	}
	for b := window.first; b <= window.last; b++ {
		point := statementHistoryPoint{At: min((b+1)*window.bucket, detail.Until.UnixMilli()), Recorded: window.recorded[b]}
		if f := tally.buckets[b]; f != nil {
			point.Recorded = true
			point.Calls, point.TotalMs, point.Rows = f.Calls, f.TotalMs, f.Rows
			if f.Calls > 0 {
				mean := f.TotalMs / float64(f.Calls)
				point.MeanMs = &mean
			}
			if f.RowsRead >= 0 {
				rowsRead := f.RowsRead
				point.RowsRead = &rowsRead
			}
			point.P50Ms, point.P95Ms, point.P99Ms = quantiles(f.Durations)
		}
		detail.Points = append(detail.Points, point)
	}
	httpx.JSON(w, http.StatusOK, detail)
	return nil
}
