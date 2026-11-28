package requesthistory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

// scanChunk is how many rows one statement of a scan reads. A scan is read a
// chunk at a time, each continuing from the last row of the one before, so no
// read transaction stays open for the length of a week's window — an open one
// holds back the WAL's checkpoint for as long as it lasts — and the strings a
// chunk names are looked up together rather than one by one.
const scanChunk = 4096

// Scan hands fn, oldest first, every kept request of route served at or after
// from and before until, both unix nanoseconds; from at or below zero has no
// lower bound and until math.MaxInt64 no upper one. An error fn returns stops
// the scan and is returned.
//
// The rows are read and decoded on goroutines of their own, a chunk ahead of
// fn: reading a week from SQLite, naming its strings, and folding it into a
// window's readings take about as long as each other, and side by side a week
// takes the time of the slowest rather than of all three.
func (a *Archive) Scan(ctx context.Context, route string, from, until int64, fn func(accesslog.Entry) error) error {
	var id int64
	err := a.db.Reader.QueryRowContext(ctx, `SELECT id FROM routes WHERE name = ?`, route).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if from < 0 {
		from = 0
	}
	if until <= 0 {
		until = math.MaxInt64
	}
	ctx, cancel := context.WithCancel(ctx)
	chunks := make(chan scanned, 2)
	go a.produce(ctx, id, from, until, chunks)
	// However the loop below ends, the reader is stopped and waited for, so
	// nothing of the scan outlives it.
	defer func() {
		cancel()
		for range chunks {
		}
	}()
	for chunk := range chunks {
		if chunk.err != nil {
			return chunk.err
		}
		for _, e := range chunk.entries {
			if err := fn(e); err != nil {
				return err
			}
		}
	}
	// A reader stopped by the caller's context ends the stream early, which
	// is not the end of the rows.
	return ctx.Err()
}

// scanned is one chunk of a scan, or what stopped it.
type scanned struct {
	entries []accesslog.Entry
	err     error
}

// produce reads route's rows from `from` to `until` into out, a chunk at a
// time, and closes it. The rows are read on one goroutine and their strings
// looked up on this one, which are about as slow as each other. A panic is
// the scan's failure rather than the process's: the caller answers from
// memory alone.
func (a *Archive) produce(ctx context.Context, route, from, until int64, out chan<- scanned) {
	defer close(out)
	send := func(chunk scanned) bool {
		select {
		case out <- chunk:
			return true
		case <-ctx.Done():
			return false
		}
	}
	defer func() {
		if p := recover(); p != nil {
			send(scanned{err: fmt.Errorf("reading the request history failed: %v", p)})
		}
	}()
	ctx, cancel := context.WithCancel(ctx)
	raw := make(chan rawChunk, 2)
	go a.readRows(ctx, route, from, until, raw)
	defer func() {
		cancel()
		for range raw {
		}
	}()
	names := map[int64]string{}
	want := make([]int64, 0, 256)
	for chunk := range raw {
		if chunk.err != nil {
			send(scanned{err: chunk.err})
			return
		}
		want = want[:0]
		for i := range chunk.rows {
			for _, sid := range chunk.rows[i].ids {
				if sid == 0 {
					continue
				}
				if _, ok := names[sid]; !ok {
					names[sid] = ""
					want = append(want, sid)
				}
			}
		}
		if len(want) > 0 {
			if err := a.names.resolve(ctx, a.db.Reader, want, names); err != nil {
				send(scanned{err: err})
				return
			}
		}
		entries := make([]accesslog.Entry, len(chunk.rows))
		for i := range chunk.rows {
			entries[i] = chunk.rows[i].entry(names)
		}
		if !send(scanned{entries: entries}) {
			return
		}
		// A window's own strings are few; a map grown past the reader's cap
		// by a week of unique query strings starts again rather than holding
		// every one of them until the scan ends.
		if len(names) > readerDictionaryCap {
			names = map[int64]string{}
		}
	}
	// The rows ran out early because the scan was stopped, not because there
	// were no more.
	if err := ctx.Err(); err != nil {
		send(scanned{err: err})
	}
}

// rawChunk is one chunk of rows as read, before their strings are looked up.
type rawChunk struct {
	rows []decoded
	err  error
}

// readRows reads route's rows a chunk at a time into out, and closes it.
func (a *Archive) readRows(ctx context.Context, route, from, until int64, out chan<- rawChunk) {
	defer close(out)
	send := func(chunk rawChunk) bool {
		select {
		case out <- chunk:
			return true
		case <-ctx.Done():
			return false
		}
	}
	defer func() {
		if p := recover(); p != nil {
			send(rawChunk{err: fmt.Errorf("reading the request history failed: %v", p)})
		}
	}()
	afterAt, afterSeq, first := from, int64(0), true
	for {
		rows := make([]decoded, 0, scanChunk)
		last, read, err := a.chunk(ctx, route, first, afterAt, afterSeq, until, &rows)
		if err != nil {
			send(rawChunk{err: err})
			return
		}
		first = false
		if !send(rawChunk{rows: rows}) || read < scanChunk {
			return
		}
		afterAt, afterSeq = last.at, last.seq
	}
}

// rowKey is where a chunk ended.
type rowKey struct{ at, seq int64 }

// chunk reads the next rows of a scan into out: the first chunk from `from`
// on, each after it past the row the one before ended on. It returns the
// last row read and how many were, which an unreadable row counts in.
func (a *Archive) chunk(ctx context.Context, route int64, first bool, afterAt, afterSeq, until int64, out *[]decoded) (rowKey, int, error) {
	var rows *sql.Rows
	var err error
	if first {
		rows, err = a.db.Reader.QueryContext(ctx, `SELECT at, seq, body FROM requests
			WHERE route = ? AND at >= ? AND at < ? ORDER BY at, seq LIMIT ?`, route, afterAt, until, scanChunk)
	} else {
		rows, err = a.db.Reader.QueryContext(ctx, `SELECT at, seq, body FROM requests
			WHERE route = ? AND (at, seq) > (?, ?) AND at < ? ORDER BY at, seq LIMIT ?`, route, afterAt, afterSeq, until, scanChunk)
	}
	if err != nil {
		return rowKey{}, 0, err
	}
	defer rows.Close()
	var last rowKey
	read := 0
	for rows.Next() {
		// RawBytes: the body is decoded before the next row replaces it, so
		// it is never copied.
		var body sql.RawBytes
		if err := rows.Scan(&last.at, &last.seq, &body); err != nil {
			return rowKey{}, 0, err
		}
		read++
		d, err := decode(last.at, body)
		if err != nil {
			// One unreadable row is skipped rather than costing the window.
			continue
		}
		*out = append(*out, d)
	}
	return last, read, rows.Err()
}
