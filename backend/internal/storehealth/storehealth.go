// Package storehealth watches whether the dashboard's own store can still be
// written, says so where an operator will read it, and keeps a little space
// back for the day the server's disk fills.
//
// Liveness (/healthz) deliberately knows none of this. The container's health
// check reads that route, and so do `compose up --wait` in the updater and its
// rollback: a liveness route that turned red on a full disk would make the
// stack restart-loop and roll an update back at the moment the dashboard is
// the tool needed to find what filled the disk. What is reported here goes to
// a signed-in operator instead, and changes nothing by itself.
package storehealth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"modernc.org/sqlite"
)

// The problems a store can have, as the codes the API reports them under.
const (
	// CodeDiskFull: SQLite was refused space, or the reserve has been given up
	// and the filesystem has not yet room to put it back.
	CodeDiskFull = "disk_full"
	// CodeUnwritable: writes have failed for a while for another reason, a
	// damaged file or one that stays locked.
	CodeUnwritable = "store_unwritable"
	// CodeUnreadable: not even a read answers.
	CodeUnreadable = "store_unreadable"
)

const (
	// DefaultInterval is how often the store is asked. The metrics recorder
	// writes at the same pace, so this adds one small write to each of its.
	DefaultInterval = 15 * time.Second
	// DefaultReserve is the space kept back: enough for SQLite to take
	// thousands of small writes (sessions, audit lines, a setting) on a disk
	// with nothing else left, and small beside any disk a server runs on.
	DefaultReserve = 32 << 20
	// ReserveFile is the reserve's name, in the data directory unless
	// Options.ReservePath says where.
	ReserveFile = "disk-reserve"

	// A write that fails for a reason other than space is reported only once
	// it has kept failing this long: one busy answer during a long migration
	// is not a broken store.
	unwritableAfter = time.Minute
	// The reserve is put down only where it leaves this many times its own
	// size free, so making it can never be what fills a disk, and a disk that
	// hovers at full does not have it written and deleted in turns.
	reserveHeadroom = 8
	// A reserve that could not be written is left alone this long before it
	// is tried again. Each attempt writes all of it, so one made on every
	// pass would be 32 MiB of writing and a log line every fifteen seconds,
	// for as long as a quota or a filesystem that reports room it cannot hand
	// out refuses it.
	reserveRetry = 10 * time.Minute
	// lowWater is the free space under which the disk is called full without
	// waiting for a write to fail. SQLite takes small writes into journal
	// space it already holds, so the probe can pass on a disk where the next
	// larger write, a metrics sample or a sign-in, will not.
	lowWater = 4 << 20

	probeKey     = "store_health_probe"
	probeTimeout = 10 * time.Second
)

// sqliteFull is SQLITE_FULL, SQLite's "database or disk is full".
const sqliteFull = 13

// IsDiskFull reports whether err is the store, or the filesystem under it,
// refusing a write for want of space.
func IsDiskFull(err error) bool {
	var lite *sqlite.Error
	if errors.As(err, &lite) && lite.Code()&0xff == sqliteFull {
		return true
	}
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT)
}

// Status is what the monitor last found.
type Status struct {
	// OK is false while the operator has something to do.
	OK   bool   `json:"ok"`
	Code string `json:"code,omitempty"`
	// Detail is the store's own words for the failure, for the line under the
	// sentence a page writes.
	Detail string `json:"detail,omitempty"`
	// Since is when the present problem began.
	Since *time.Time `json:"since,omitempty"`
	// CheckedAt is the last pass, and LastWriteAt the last one whose write
	// the store accepted.
	CheckedAt   time.Time  `json:"checkedAt"`
	LastWriteAt *time.Time `json:"lastWriteAt,omitempty"`
	// The filesystem holding the data directory.
	Path       string `json:"path"`
	FreeBytes  uint64 `json:"freeBytes"`
	TotalBytes uint64 `json:"totalBytes"`
	// ReserveBytes is the size of the space kept back, 0 where none is kept;
	// ReserveHeld is whether it is on disk now, and ReserveReleased whether
	// it was given up to a full disk and not yet put back.
	ReserveBytes    int64 `json:"reserveBytes"`
	ReserveHeld     bool  `json:"reserveHeld"`
	ReserveReleased bool  `json:"reserveReleased"`
}

// Options configure a Monitor. The zero value of each field but DB and Dir is
// the shipped behaviour.
type Options struct {
	DB  *sql.DB
	Dir string
	Log *slog.Logger
	// Reserve is the size of the space kept back; 0 keeps none.
	Reserve int64
	// ReservePath is where it is kept, on the data directory's filesystem;
	// empty is ReserveFile in Dir. The server keeps it in the data
	// directory's staging folder, which every backup of that directory
	// already leaves out: 32 MB that cannot be compressed has no place in
	// each archive.
	ReservePath string
	Interval    time.Duration

	// Seams for tests.
	Now   func() time.Time
	Space func(dir string) (free, total uint64, err error)
	Write func(ctx context.Context) error
	Read  func(ctx context.Context) error
}

// Monitor probes the store on a timer. Its zero value is not usable; a nil
// *Monitor reports a healthy store, for the server a test builds without one.
type Monitor struct {
	opt Options

	mu           sync.Mutex
	status       Status
	failingSince time.Time
	released     bool
	// reserveFailedAt is when writing the reserve last failed, and reserveErr
	// what it failed with: see reserveRetry.
	reserveFailedAt time.Time
	reserveErr      string
}

func New(opt Options) *Monitor {
	if opt.Interval <= 0 {
		opt.Interval = DefaultInterval
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.Space == nil {
		opt.Space = space
	}
	if opt.Log == nil {
		opt.Log = slog.Default()
	}
	if opt.ReservePath == "" {
		opt.ReservePath = filepath.Join(opt.Dir, ReserveFile)
	}
	m := &Monitor{opt: opt}
	if opt.Write == nil {
		m.opt.Write = m.writeProbe
	}
	if opt.Read == nil {
		m.opt.Read = m.readProbe
	}
	m.status = Status{OK: true, Path: opt.Dir, ReserveBytes: opt.Reserve, CheckedAt: opt.Now()}
	return m
}

// Run checks now and then every interval until ctx ends.
func (m *Monitor) Run(ctx context.Context) {
	if m == nil {
		return
	}
	m.Check(ctx)
	t := time.NewTicker(m.opt.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Check(ctx)
		}
	}
}

// Status is what the last pass found.
func (m *Monitor) Status() Status {
	if m == nil {
		return Status{OK: true}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// Check is one pass: ask the store for a write, give up the reserve if the
// disk is full, put the reserve back when there is room again.
func (m *Monitor) Check(ctx context.Context) Status {
	now := m.opt.Now()
	free, total, spaceErr := m.opt.Space(m.opt.Dir)
	low := spaceErr == nil && free < lowWater

	writeErr := m.opt.Write(ctx)
	if ctx.Err() != nil {
		// Shutting down: an interrupted probe says nothing about the store.
		return m.Status()
	}
	if (low || IsDiskFull(writeErr)) && m.reserveHeld() {
		// The whole point of the reserve: hand its space to SQLite, then see
		// that the store takes writes before saying anything.
		if err := os.Remove(m.reservePath()); err == nil {
			m.mu.Lock()
			m.released = true
			m.mu.Unlock()
			m.opt.Log.Error("the disk holding the dashboard's data is full; its space reserve was released so sign-in and cleanup keep working",
				"path", m.opt.Dir, "freedBytes", m.opt.Reserve, "freeBefore", free)
			if free, total, spaceErr = m.opt.Space(m.opt.Dir); spaceErr == nil {
				low = free < lowWater
			}
			writeErr = m.opt.Write(ctx)
		} else {
			m.opt.Log.Error("the dashboard's space reserve could not be released", "path", m.reservePath(), "error", err)
		}
	}
	var readErr error
	if writeErr != nil {
		readErr = m.opt.Read(ctx)
	} else if m.restoreReserve(now, free) {
		free, total, _ = m.opt.Space(m.opt.Dir)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	before := m.status
	next := Status{
		OK: true, CheckedAt: now, LastWriteAt: before.LastWriteAt,
		Path: m.opt.Dir, FreeBytes: free, TotalBytes: total, ReserveBytes: m.opt.Reserve,
	}
	next.ReserveHeld = m.reserveHeld()
	if next.ReserveHeld {
		m.released = false
	}
	next.ReserveReleased = m.released
	if writeErr == nil {
		next.LastWriteAt = &now
		m.failingSince = time.Time{}
	}
	switch {
	case writeErr == nil && (m.released || low):
		// Writes work, on borrowed space or on the last of it: the disk is
		// as full as it was, and the reader still has it to empty.
		next.OK, next.Code = false, CodeDiskFull
	case writeErr == nil:
	case readErr != nil:
		next.OK, next.Code, next.Detail = false, CodeUnreadable, readErr.Error()
	case IsDiskFull(writeErr):
		next.OK, next.Code, next.Detail = false, CodeDiskFull, writeErr.Error()
	default:
		if m.failingSince.IsZero() {
			m.failingSince = now
		}
		if now.Sub(m.failingSince) >= unwritableAfter {
			next.OK, next.Code, next.Detail = false, CodeUnwritable, writeErr.Error()
		}
	}
	if !next.OK {
		since := now
		switch {
		case before.Code == next.Code && before.Since != nil:
			since = *before.Since
		case next.Code == CodeUnwritable:
			since = m.failingSince
		}
		next.Since = &since
	}
	m.status = next
	// Said when it changes, not every fifteen seconds for as long as it lasts.
	switch {
	case !next.OK && before.Code != next.Code:
		m.opt.Log.Error("the dashboard's store has a problem", "code", next.Code, "detail", next.Detail,
			"path", next.Path, "freeBytes", next.FreeBytes)
	case next.OK && !before.OK:
		m.opt.Log.Info("the dashboard's store is healthy again", "was", before.Code, "freeBytes", next.FreeBytes)
	}
	return next
}

func (m *Monitor) writeProbe(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	// One row, rewritten in place: a real write through the journal that
	// never grows the database.
	_, err := m.opt.DB.ExecContext(ctx,
		`INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		probeKey, strconv.FormatInt(m.opt.Now().Unix(), 10))
	return err
}

func (m *Monitor) readProbe(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	var one int
	return m.opt.DB.QueryRowContext(ctx, `SELECT 1`).Scan(&one)
}

func (m *Monitor) reservePath() string { return m.opt.ReservePath }

// reserveHeld reports whether the whole reserve is on disk.
func (m *Monitor) reserveHeld() bool {
	if m.opt.Reserve <= 0 {
		return false
	}
	info, err := os.Lstat(m.reservePath())
	return err == nil && info.Mode().IsRegular() && info.Size() >= m.opt.Reserve
}

// restoreReserve puts the reserve down where there is room for it and to
// spare, and reports whether it wrote one. It fails quietly: no reserve is the
// state every install was in before this existed. A failure is said once, and
// the next attempt waits reserveRetry.
func (m *Monitor) restoreReserve(now time.Time, free uint64) bool {
	if m.opt.Reserve <= 0 || m.reserveHeld() {
		return false
	}
	// What an interrupted write left behind is space nobody is holding on
	// purpose.
	os.Remove(m.reservePath() + ".tmp")
	if free < uint64(m.opt.Reserve)*reserveHeadroom {
		return false
	}
	m.mu.Lock()
	failedAt, failedWith := m.reserveFailedAt, m.reserveErr
	m.mu.Unlock()
	if !failedAt.IsZero() && now.Sub(failedAt) < reserveRetry {
		return false
	}
	if err := writeReserve(m.reservePath(), m.opt.Reserve); err != nil {
		m.mu.Lock()
		m.reserveFailedAt, m.reserveErr = now, err.Error()
		m.mu.Unlock()
		// The same refusal again is not news; a different one is.
		if err.Error() != failedWith {
			m.opt.Log.Warn("the dashboard's space reserve could not be written; it is tried again every ten minutes",
				"path", m.reservePath(), "error", err)
		}
		return false
	}
	m.mu.Lock()
	m.reserveFailedAt, m.reserveErr = time.Time{}, ""
	m.mu.Unlock()
	m.opt.Log.Info("the dashboard holds a space reserve for a full disk", "path", m.reservePath(), "bytes", m.opt.Reserve)
	return true
}

// writeReserve fills path with size random bytes. Random, not zeros and not a
// preallocation: a filesystem that compresses or stores holes would otherwise
// keep a 32 MB file in a few blocks, and deleting it would free nothing.
func writeReserve(path string, size int64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("write the space reserve: %w", err)
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = io.CopyN(f, rand.Reader, size)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("write the space reserve: %w", err)
	}
	return nil
}

// space is the room left on the filesystem holding dir, as root may use it:
// the backend is root, and ext4's blocks reserved for root are its to spend.
func space(dir string) (free, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, 0, err
	}
	return st.Bfree * uint64(st.Bsize), st.Blocks * uint64(st.Bsize), nil
}
