package requesthistory

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

// A row's body.
//
// Everything about a request but its route and instant is one blob of
// varints rather than a column each. A week of a busy site is half a million
// rows read in one go, and through database/sql each column of each row is a
// value boxed into an interface: three columns read several times faster than
// twenty. Packed, a row is also some twenty bytes smaller, since a column
// costs a header byte of its own. The layout, after a version byte:
//
//	status (zig-zag varint), size (zig-zag varint), flags (uvarint),
//	11 dictionary ids (uvarint, 0 for an empty string) in the order of
//	stringFields, then, as flags say, the duration (float64, little-endian,
//	exactly as measured), the trace id (16 bytes) and span (8), the parent
//	span (8).
//
// The duration is the float64 itself so a request read back is the request
// written, and a percentile over the history agrees with one over memory.

const codecVersion = 1

const (
	flagTLS = 1 << iota
	flagTimed
	flagTrace
	flagParent
)

// stringFields is the order of a body's dictionary ids.
const stringFields = 11

type stringIDs [stringFields]int64

func fields(e *accesslog.Entry) [stringFields]*string {
	return [stringFields]*string{
		&e.Method, &e.Path, &e.Query, &e.Host, &e.Proto, &e.RemoteIP,
		&e.UserAgent, &e.Referer, &e.Release, &e.User, &e.EdgeRule,
	}
}

func encode(dst []byte, e accesslog.Entry, ids stringIDs) []byte {
	ms, timed := e.Duration()
	var trace, span, parent []byte
	flags := uint64(0)
	if e.TLS {
		flags |= flagTLS
	}
	if timed {
		flags |= flagTimed
	}
	if e.TraceID != "" {
		t, terr := hex.DecodeString(e.TraceID)
		s, serr := hex.DecodeString(e.SpanID)
		// Parse keeps only a context that is one, so these always decode;
		// one that did not would be no context rather than a wrong one.
		if terr == nil && serr == nil && len(t) == 16 && len(s) == 8 {
			trace, span, flags = t, s, flags|flagTrace
			if p, err := hex.DecodeString(e.ParentSpanID); err == nil && len(p) == 8 {
				parent, flags = p, flags|flagParent
			}
		}
	}
	dst = append(dst, codecVersion)
	dst = binary.AppendVarint(dst, int64(e.Status))
	dst = binary.AppendVarint(dst, e.Size)
	dst = binary.AppendUvarint(dst, flags)
	for _, id := range ids {
		dst = binary.AppendUvarint(dst, uint64(id))
	}
	if timed {
		dst = binary.LittleEndian.AppendUint64(dst, math.Float64bits(ms))
	}
	dst = append(dst, trace...)
	dst = append(dst, span...)
	dst = append(dst, parent...)
	return dst
}

// decoded is a body read back, before its strings are looked up.
type decoded struct {
	at     int64
	status int64
	size   int64
	flags  uint64
	ids    stringIDs
	ms     float64
	trace  [16]byte
	span   [8]byte
	parent [8]byte
}

var errBody = errors.New("a kept request's body cannot be read")

func decode(at int64, body []byte) (decoded, error) {
	d := decoded{at: at}
	if len(body) == 0 || body[0] != codecVersion {
		return d, errBody
	}
	rest := body[1:]
	var n int
	if d.status, n = binary.Varint(rest); n <= 0 {
		return d, errBody
	}
	rest = rest[n:]
	if d.size, n = binary.Varint(rest); n <= 0 {
		return d, errBody
	}
	rest = rest[n:]
	if d.flags, n = binary.Uvarint(rest); n <= 0 {
		return d, errBody
	}
	rest = rest[n:]
	for i := range d.ids {
		id, n := binary.Uvarint(rest)
		if n <= 0 {
			return d, errBody
		}
		d.ids[i], rest = int64(id), rest[n:]
	}
	if d.flags&flagTimed != 0 {
		if len(rest) < 8 {
			return d, errBody
		}
		d.ms, rest = math.Float64frombits(binary.LittleEndian.Uint64(rest)), rest[8:]
	}
	if d.flags&flagTrace != 0 {
		if len(rest) < 24 {
			return d, errBody
		}
		copy(d.trace[:], rest)
		copy(d.span[:], rest[16:])
		rest = rest[24:]
	}
	if d.flags&flagParent != 0 {
		if len(rest) < 8 {
			return d, errBody
		}
		copy(d.parent[:], rest)
	}
	return d, nil
}

// entry builds the request, its strings from names. An id names lacks — a
// string the dictionary's sweep took, which only a row stamped far from this
// host's clock can still name — reads as empty.
func (d decoded) entry(names map[int64]string) accesslog.Entry {
	e := accesslog.Entry{Status: int(d.status), Size: d.size, TLS: d.flags&flagTLS != 0}
	for i, field := range fields(&e) {
		if id := d.ids[i]; id != 0 {
			*field = names[id]
		}
	}
	if d.flags&flagTrace != 0 {
		e.TraceID, e.SpanID = hex.EncodeToString(d.trace[:]), hex.EncodeToString(d.span[:])
		if d.flags&flagParent != 0 {
			e.ParentSpanID = hex.EncodeToString(d.parent[:])
		}
	}
	e.Restore(d.at, d.ms, d.flags&flagTimed != 0)
	return e
}

// writerDictionary is the writer's map from a string to its id, with the
// hour each was last written as used. What one transaction adds is pending
// until it commits, so a rolled-back string is never cached.
type writerDictionary struct {
	ids     map[string]dictEntry
	pending map[string]dictEntry
	// touched is what the transaction moves a string's hour to.
	touched map[int64]int64
	lookup  *sql.Stmt
	insert  *sql.Stmt
}

type dictEntry struct{ id, used int64 }

// writerDictionaryCap bounds the strings the writer remembers. Past it the map
// starts again: what is common comes straight back, and what is not — a
// query string with a token in it — was never worth remembering.
const writerDictionaryCap = 200_000

func newWriterDictionary() writerDictionary {
	return writerDictionary{ids: map[string]dictEntry{}, pending: map[string]dictEntry{}, touched: map[int64]int64{}}
}

func (d *writerDictionary) reset() {
	d.ids, d.pending, d.touched = map[string]dictEntry{}, map[string]dictEntry{}, map[int64]int64{}
}

// begin prepares the lookups for one transaction.
func (d *writerDictionary) begin(ctx context.Context, tx *sql.Tx) error {
	var err error
	if d.lookup, err = tx.PrepareContext(ctx, `SELECT id, used FROM strings WHERE value = ?`); err != nil {
		return err
	}
	d.insert, err = tx.PrepareContext(ctx, `INSERT INTO strings(value, used) VALUES (?, ?) RETURNING id`)
	return err
}

func (d *writerDictionary) end() {
	for _, stmt := range []*sql.Stmt{d.lookup, d.insert} {
		if stmt != nil {
			stmt.Close()
		}
	}
	d.lookup, d.insert = nil, nil
}

func (d *writerDictionary) intern(ctx context.Context, e accesslog.Entry, used int64) (stringIDs, error) {
	var ids stringIDs
	for i, field := range fields(&e) {
		id, err := d.id(ctx, *field, used)
		if err != nil {
			return ids, err
		}
		ids[i] = id
	}
	return ids, nil
}

func (d *writerDictionary) id(ctx context.Context, s string, used int64) (int64, error) {
	if s == "" {
		return 0, nil
	}
	have, ok := d.pending[s]
	if !ok {
		have, ok = d.ids[s]
	}
	if ok {
		if used > have.used {
			have.used = used
			d.pending[s], d.touched[have.id] = have, used
		}
		return have.id, nil
	}
	var id, was int64
	err := d.lookup.QueryRowContext(ctx, s).Scan(&id, &was)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if err := d.insert.QueryRowContext(ctx, s, used).Scan(&id); err != nil {
			return 0, fmt.Errorf("keep a string: %w", err)
		}
		was = used
	case err != nil:
		return 0, err
	case used > was:
		d.touched[id], was = used, used
	}
	d.pending[s] = dictEntry{id: id, used: was}
	return id, nil
}

// touch writes the hours the transaction moved.
func (d *writerDictionary) touch(ctx context.Context, tx *sql.Tx) error {
	if len(d.touched) == 0 {
		return nil
	}
	stmt, err := tx.PrepareContext(ctx, `UPDATE strings SET used = max(used, ?) WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for id, used := range d.touched {
		if _, err := stmt.ExecContext(ctx, used, id); err != nil {
			return err
		}
	}
	return nil
}

// commit makes what the transaction learnt the writer's.
func (d *writerDictionary) commit() {
	if len(d.ids)+len(d.pending) > writerDictionaryCap {
		d.ids = map[string]dictEntry{}
	}
	for s, entry := range d.pending {
		d.ids[s] = entry
	}
	d.pending, d.touched = map[string]dictEntry{}, map[int64]int64{}
}

// readerDictionary is the readers' map from an id to its string, shared by
// every scan. An id is never given to a second string, so what it holds is
// never wrong, only possibly gone from the database.
type readerDictionary struct {
	mu    sync.RWMutex
	names map[int64]string
}

const readerDictionaryCap = 400_000

func newReaderDictionary() readerDictionary {
	return readerDictionary{names: map[int64]string{}}
}

// resolve fills names with every id in want, from the shared map and then the
// database, in batches small enough for one statement each.
func (r *readerDictionary) resolve(ctx context.Context, db *sql.DB, want []int64, names map[int64]string) error {
	missing := want[:0]
	r.mu.RLock()
	for _, id := range want {
		if s, ok := r.names[id]; ok {
			names[id] = s
		} else {
			missing = append(missing, id)
		}
	}
	r.mu.RUnlock()
	const batch = 500
	for len(missing) > 0 {
		n := min(len(missing), batch)
		part := missing[:n]
		missing = missing[n:]
		args := make([]any, len(part))
		for i, id := range part {
			args[i] = id
		}
		rows, err := db.QueryContext(ctx, `SELECT id, value FROM strings WHERE id IN (`+
			strings.Repeat("?,", len(part)-1)+`?)`, args...)
		if err != nil {
			return err
		}
		found := map[int64]string{}
		for rows.Next() {
			var id int64
			var s string
			if err := rows.Scan(&id, &s); err != nil {
				rows.Close()
				return err
			}
			names[id], found[id] = s, s
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		r.mu.Lock()
		if len(r.names)+len(found) > readerDictionaryCap {
			r.names = map[int64]string{}
		}
		for id, s := range found {
			r.names[id] = s
		}
		r.mu.Unlock()
	}
	return nil
}

// forget empties the readers' map, after the sweep took strings out.
func (r *readerDictionary) forget() {
	r.mu.Lock()
	r.names = map[int64]string{}
	r.mu.Unlock()
}
