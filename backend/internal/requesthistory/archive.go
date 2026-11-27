// Package requesthistory keeps what every routed deployment served, past
// what the ingress keeps on disk and past this process.
//
// The request record the Logs page reads (package accesslog) is the
// ingress's own files read back into memory, and the files are a few dozen
// megabytes: a site serving a few thousand requests an hour has under a day
// of them, and the memory goes when the dashboard restarts. Here a recorder
// follows each route's files as they are written, whether or not anybody has
// the page open, and writes every request into a database of its own beside
// the store, requests.db, for thirty days. The store answers a window from
// that history and from memory together (accesslog/history.go).
//
// The history has a file of its own rather than tables in vpsd.db because it
// is written at the rate traffic arrives: in the store's file it would queue
// behind the one writer the session checks and the deployment engine use,
// make every update copy and every backup snapshot of the store carry a month
// of traffic, and grow the file the operator's whole configuration lives in.
package requesthistory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// File is the history's database inside the data directory.
const File = "requests.db"

// What is kept, and how much of it.
//
// A request is kept as one row of about 80 bytes on disk, the B-tree's own
// overhead and the dictionary counted (TestAKeptRequestIsAboutEightyBytes
// measures it on a public site's mix): the instant, its order and a body of
// small integers, its strings being numbers into a dictionary every row
// shares, and the 24 bytes of trace context the ingress gives most requests.
// At that size:
const (
	// Retention is how long a request is kept: a month, the widest window
	// the page offers.
	Retention = 30 * 24 * time.Hour
	// RouteCap bounds one route's rows: four million is about 320 MB, and a
	// month of a route serving 5,500 requests an hour. A busier route keeps
	// its newest four million, and says it has lost the rest.
	RouteCap = 4_000_000
	// TotalCap bounds every route's together: ten million is about 800 MB,
	// so a host of busy sites cannot fill the disk with their history. Past
	// it the routes keeping the most give up their oldest first.
	TotalCap = 10_000_000
)

const schema = `
PRAGMA auto_vacuum = INCREMENTAL;

CREATE TABLE IF NOT EXISTS routes (
  id          INTEGER PRIMARY KEY,
  name        TEXT NOT NULL UNIQUE,
  generation  TEXT NOT NULL DEFAULT '',
  position    INTEGER NOT NULL DEFAULT 0,
  newest      INTEGER NOT NULL DEFAULT 0,
  guard       INTEGER NOT NULL DEFAULT 0,
  lost_at     INTEGER NOT NULL DEFAULT 0,
  kept        INTEGER NOT NULL DEFAULT 0,
  next_seq    INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS strings (
  id    INTEGER PRIMARY KEY AUTOINCREMENT,
  value TEXT NOT NULL UNIQUE,
  used  INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS requests (
  route INTEGER NOT NULL,
  at    INTEGER NOT NULL,
  seq   INTEGER NOT NULL,
  body  BLOB NOT NULL,
  PRIMARY KEY (route, at, seq)
) WITHOUT ROWID;
`

// The tables, as the schema above leaves them:
//
// routes is one row per route ever recorded: where in its files the recorder
// has read to (generation is the file's identity, position the offset past
// the last whole line, newest the latest instant read), the instant at or
// before which a re-read drops what it finds (guard), the newest instant
// something is known to be missing before (lost_at), and a count of its rows
// so the caps do not count them. The cursor is written in the transaction
// that writes the rows it covers, so a restart continues from exactly where
// the last commit left off: no request twice, none skipped.
//
// strings is the dictionary: every method, host, path, query, address, user
// agent, referer, protocol, release, login and edge rule, once, with the hour
// it was last written into a row. AUTOINCREMENT, because an id is never given
// to a second string: a reader that cached one must never read another
// string under it.
//
// requests is clustered by route and instant (WITHOUT ROWID), so a window is
// one range read in order, and pruning is one range delete. seq is the
// route's running count and only breaks ties between two requests of one
// instant. body is the rest of the request (codec.go).

// Cursor is where the recorder has read one route's files to.
type Cursor struct {
	// Generation is the identity of the file being read; empty before the
	// first read and after the files vanished.
	Generation string
	// Offset is just past the last whole line read from it.
	Offset int64
	// Newest is the latest instant read from the route, unix nanoseconds.
	Newest int64
	// Guard, while set, is the instant at or before which a request read is
	// dropped: the recorder is reading bytes it may already have kept.
	Guard int64
	// LostAt is the newest instant before which something is known to be
	// missing; an append only ever moves it later.
	LostAt int64
}

// Archive is the history's database.
type Archive struct {
	db  *store.SideDatabase
	now func() time.Time

	// write serialises every write, so a prune and an append never interleave
	// their use of the dictionary cache.
	write sync.Mutex
	dict  writerDictionary
	// routeIDs caches a route's row id by name; it only grows.
	routeMu  sync.Mutex
	routeIDs map[string]int64
	// names caches the dictionary for readers.
	names readerDictionary

	followMu  sync.Mutex
	following map[string]bool
	// forgotten is every route Forget took away in this process: an append
	// the recorder had in flight for one is dropped rather than starting its
	// history again. A route's name is its environment's id, never given to
	// a second environment, so the set only ever names the gone.
	forgotten map[string]bool
	// forgottenHour is the hour of the last Forget whose strings the next
	// prune owes a sweep (sweepUnnamed), or 0; held under write.
	forgottenHour int64
	// counted says this process has recounted every route's rows (prune.go).
	counted bool
}

// Open opens, creating it on first use, the history in dataDir.
func Open(dataDir string) (*Archive, error) {
	db, err := store.OpenSideDatabase(dataDir, File, schema)
	if err != nil {
		return nil, err
	}
	return newArchive(db), nil
}

func newArchive(db *store.SideDatabase) *Archive {
	return &Archive{
		db: db, now: time.Now,
		dict: newWriterDictionary(), names: newReaderDictionary(),
		routeIDs: map[string]int64{}, following: map[string]bool{}, forgotten: map[string]bool{},
	}
}

// Forget deletes everything kept for route, its requests and its cursor: the
// history of a deployment that is gone. Its rows hold its visitors' addresses
// and agents, and the ingress's own record of them goes with the route
// (proxysvc's RemoveDeploymentRoute), so keeping them a month with nothing
// left to read them would keep personal data for no one. The strings only
// those rows named go on the next hourly prune (sweepUnnamed).
func (a *Archive) Forget(ctx context.Context, route string) error {
	if a == nil {
		return nil
	}
	a.write.Lock()
	a.forgotten[route] = true
	a.write.Unlock()
	var id int64
	err := a.db.Reader.QueryRowContext(ctx, `SELECT id FROM routes WHERE name = ?`, route).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := a.dropBefore(ctx, id, math.MaxInt64, 0); err != nil {
		return err
	}
	a.write.Lock()
	defer a.write.Unlock()
	if _, err := a.db.Writer.ExecContext(ctx, `DELETE FROM routes WHERE id = ?`, id); err != nil {
		return err
	}
	// Its visitors' addresses and agents stay in the dictionary until a
	// prune sweeps the strings no kept row names, rather than for the
	// retention's month.
	a.forgottenHour = a.now().Unix() / 3600
	a.routeMu.Lock()
	delete(a.routeIDs, route)
	a.routeMu.Unlock()
	return nil
}

// Close closes the database.
func (a *Archive) Close() error {
	if a == nil {
		return nil
	}
	return a.db.Close()
}

// Path is the database file.
func (a *Archive) Path() string { return a.db.Path }

// Retention is how long a request is kept.
func (a *Archive) Retention() time.Duration { return Retention }

// follow records which routes the recorder follows, for Span.
func (a *Archive) follow(routes []string) {
	set := make(map[string]bool, len(routes))
	for _, route := range routes {
		set[route] = true
	}
	a.followMu.Lock()
	a.following = set
	a.followMu.Unlock()
}

func (a *Archive) recording(route string) bool {
	a.followMu.Lock()
	defer a.followMu.Unlock()
	return a.following[route]
}

// Span says what is kept for route.
func (a *Archive) Span(ctx context.Context, route string) (accesslog.ArchiveSpan, error) {
	span := accesslog.ArchiveSpan{Recording: a.recording(route)}
	var lost int64
	var from, to sql.NullInt64
	err := a.db.Reader.QueryRowContext(ctx, `SELECT r.lost_at,
		(SELECT at FROM requests WHERE route = r.id ORDER BY at LIMIT 1),
		(SELECT at FROM requests WHERE route = r.id ORDER BY at DESC LIMIT 1)
		FROM routes r WHERE r.name = ?`, route).Scan(&lost, &from, &to)
	if errors.Is(err, sql.ErrNoRows) {
		return span, nil
	}
	if err != nil {
		return span, err
	}
	if from.Valid {
		span.From, span.To = time.Unix(0, from.Int64).UTC(), time.Unix(0, to.Int64).UTC()
	}
	if lost > 0 {
		span.LostAt = time.Unix(0, lost).UTC()
	}
	return span, nil
}

// Cursor is where the recorder last read route's files to, and false when it
// has never recorded the route.
func (a *Archive) Cursor(ctx context.Context, route string) (Cursor, bool, error) {
	var c Cursor
	err := a.db.Reader.QueryRowContext(ctx,
		`SELECT generation, position, newest, guard, lost_at FROM routes WHERE name = ?`, route).
		Scan(&c.Generation, &c.Offset, &c.Newest, &c.Guard, &c.LostAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Cursor{}, false, nil
	}
	if err != nil {
		return Cursor{}, false, err
	}
	return c, true, nil
}

// Append keeps entries as route's, oldest first, and moves the route's
// cursor to cursor, in one transaction: either both are kept or neither is,
// which is what lets a restart continue from the cursor without a request
// counted twice or one skipped.
func (a *Archive) Append(ctx context.Context, route string, entries []accesslog.Entry, cursor Cursor) error {
	a.write.Lock()
	defer a.write.Unlock()
	if a.forgotten[route] {
		return nil
	}
	err := a.append(ctx, route, entries, cursor)
	if err != nil {
		// The caches may name a string or a route the rollback took back out.
		a.dict.reset()
		a.routeMu.Lock()
		a.routeIDs = map[string]int64{}
		a.routeMu.Unlock()
	}
	return err
}

func (a *Archive) append(ctx context.Context, route string, entries []accesslog.Entry, cursor Cursor) error {
	tx, err := a.db.Writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	id, err := a.routeID(ctx, tx, route)
	if err != nil {
		return err
	}
	var next int64
	if err := tx.QueryRowContext(ctx, `SELECT next_seq FROM routes WHERE id = ?`, id).Scan(&next); err != nil {
		return err
	}
	insert, err := tx.PrepareContext(ctx, `INSERT INTO requests(route, at, seq, body) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insert.Close()
	if err := a.dict.begin(ctx, tx); err != nil {
		return err
	}
	defer a.dict.end()
	hour := a.now().Unix() / 3600
	var body []byte
	for _, e := range entries {
		at := e.UnixNano()
		// A string is used until the hour of the latest row naming it, so the
		// dictionary's sweep never takes one a kept row still names — even a
		// row stamped ahead of this host's clock.
		used := hour
		if h := at / int64(time.Hour); h > used {
			used = h
		}
		ids, err := a.dict.intern(ctx, e, used)
		if err != nil {
			return err
		}
		body = encode(body[:0], e, ids)
		if _, err := insert.ExecContext(ctx, id, at, next, body); err != nil {
			return fmt.Errorf("keep a request: %w", err)
		}
		next++
		if at > cursor.Newest {
			cursor.Newest = at
		}
	}
	if err := a.dict.touch(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE routes SET generation = ?, position = ?, newest = ?, guard = ?,
		lost_at = max(lost_at, ?), kept = kept + ?, next_seq = ? WHERE id = ?`,
		cursor.Generation, cursor.Offset, cursor.Newest, cursor.Guard, cursor.LostAt,
		len(entries), next, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	a.dict.commit()
	return nil
}

// routeID is route's row, made on its first append.
func (a *Archive) routeID(ctx context.Context, tx *sql.Tx, route string) (int64, error) {
	a.routeMu.Lock()
	id, ok := a.routeIDs[route]
	a.routeMu.Unlock()
	if ok {
		return id, nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO routes(name) VALUES (?) ON CONFLICT(name) DO NOTHING`, route); err != nil {
		return 0, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM routes WHERE name = ?`, route).Scan(&id); err != nil {
		return 0, err
	}
	// Cached before the transaction commits: a failed append drops the cache
	// (Append), and a committed route's row is deleted only by Forget, which
	// drops its entry and refuses every later append to the route.
	a.routeMu.Lock()
	a.routeIDs[route] = id
	a.routeMu.Unlock()
	return id, nil
}
