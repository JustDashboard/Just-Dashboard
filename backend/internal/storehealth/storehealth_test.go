package storehealth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

const (
	kib = 1 << 10
	mib = 1 << 20
	gib = 1 << 30
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func openDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`PRAGMA journal_mode = WAL;
		CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS sessions (id INTEGER PRIMARY KEY, token BLOB NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	return db
}

// sqliteFullError is SQLite's own "database or disk is full", from a database
// held to a few pages: the error a full disk gives, without filling one.
func sqliteFullError(t *testing.T) error {
	t.Helper()
	db := openDB(t, filepath.Join(t.TempDir(), "small.db"))
	if _, err := db.Exec(`PRAGMA max_page_count = 24`); err != nil {
		t.Fatal(err)
	}
	for range 200 {
		if _, err := db.Exec(`INSERT INTO sessions(token) VALUES (randomblob(4096))`); err != nil {
			return err
		}
	}
	t.Fatal("a database held to 24 pages took 200 rows of 4 KiB")
	return nil
}

func TestIsDiskFullKnowsSQLitesAndTheKernelsRefusal(t *testing.T) {
	full := sqliteFullError(t)
	if !IsDiskFull(full) {
		t.Fatalf("IsDiskFull(%v) = false", full)
	}
	if !IsDiskFull(fmt.Errorf("start session: %w", full)) {
		t.Fatal("a wrapped SQLITE_FULL is not seen")
	}
	if !IsDiskFull(&os.PathError{Op: "write", Path: "/x", Err: syscall.ENOSPC}) {
		t.Fatal("ENOSPC is not seen")
	}
	for _, err := range []error{nil, errors.New("database is locked (5) (SQLITE_BUSY)"), sql.ErrNoRows} {
		if IsDiskFull(err) {
			t.Fatalf("IsDiskFull(%v) = true", err)
		}
	}
}

// fakeDisk is a filesystem with room for the reserve and a known amount more.
type fakeDisk struct {
	dir   string
	other uint64 // bytes free besides whatever the reserve occupies
	total uint64
}

func (d *fakeDisk) space(string) (uint64, uint64, error) { return d.other, d.total, nil }

func (d *fakeDisk) reserve() bool {
	_, err := os.Stat(filepath.Join(d.dir, ReserveFile))
	return err == nil
}

func TestAFullDiskGivesUpTheReserveSoWritesGoOn(t *testing.T) {
	full := sqliteFullError(t)
	dir := t.TempDir()
	disk := &fakeDisk{dir: dir, other: 10 * gib, total: 100 * gib}
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	m := New(Options{
		Dir: dir, Log: quiet(), Reserve: 64 * kib,
		Now:   func() time.Time { return now },
		Space: disk.space,
		// The store takes a write only while something on the disk is free:
		// room of its own, or the reserve's once that is given up.
		Write: func(context.Context) error {
			if disk.other == 0 && disk.reserve() {
				return fmt.Errorf("probe: %w", full)
			}
			return nil
		},
		Read: func(context.Context) error { return nil },
	})

	st := m.Check(context.Background())
	if !st.OK || !st.ReserveHeld || st.ReserveReleased || !disk.reserve() {
		t.Fatalf("with room to spare: %+v, want a healthy store holding its reserve", st)
	}
	if info, err := os.Stat(filepath.Join(dir, ReserveFile)); err != nil || info.Size() != 64*kib || info.Mode().Perm() != 0o600 {
		t.Fatalf("reserve = %v, %v; want 64 KiB, private", info, err)
	}

	// The disk fills.
	disk.other = 0
	now = now.Add(15 * time.Second)
	filled := now
	st = m.Check(context.Background())
	if disk.reserve() {
		t.Fatal("the reserve is still on a full disk")
	}
	if st.OK || st.Code != CodeDiskFull || !st.ReserveReleased || st.ReserveHeld {
		t.Fatalf("on a full disk: %+v, want disk_full with the reserve released", st)
	}
	if st.LastWriteAt == nil || !st.LastWriteAt.Equal(filled) {
		t.Fatalf("lastWriteAt = %v: the write must have gone through on the reserve's space", st.LastWriteAt)
	}
	if st.Since == nil || !st.Since.Equal(filled) {
		t.Fatalf("since = %v, want when the disk filled", st.Since)
	}

	// Still full a while later: the same problem, since the same moment, and
	// the reserve is not written back onto a disk with no room for it.
	disk.other = 3 * 64 * kib
	now = now.Add(time.Hour)
	st = m.Check(context.Background())
	if st.OK || st.Code != CodeDiskFull || disk.reserve() || !st.Since.Equal(filled) {
		t.Fatalf("an hour on: %+v, want the same disk_full and no reserve", st)
	}

	// Space is freed: the reserve goes back and the problem is over.
	disk.other = 10 * gib
	now = now.Add(time.Minute)
	st = m.Check(context.Background())
	if !st.OK || st.Code != "" || !st.ReserveHeld || st.ReserveReleased || st.Since != nil || !disk.reserve() {
		t.Fatalf("with space again: %+v, want a healthy store holding its reserve", st)
	}
}

func TestADiskWithNoRoomLeftIsSaidBeforeAWriteFails(t *testing.T) {
	dir := t.TempDir()
	disk := &fakeDisk{dir: dir, other: 1 * mib, total: 100 * gib}
	m := New(Options{
		Dir: dir, Log: quiet(), Space: disk.space,
		Write: func(context.Context) error { return nil },
		Read:  func(context.Context) error { return nil },
	})
	if st := m.Check(context.Background()); st.OK || st.Code != CodeDiskFull || st.FreeBytes != 1*mib {
		t.Fatalf("status = %+v, want disk_full at 1 MiB free", st)
	}
}

func TestTheReserveIsKeptWhereItIsToldTo(t *testing.T) {
	dir := t.TempDir()
	disk := &fakeDisk{dir: dir, other: 10 * gib, total: 100 * gib}
	path := filepath.Join(dir, "staging", ReserveFile)
	m := New(Options{
		Dir: dir, Log: quiet(), Reserve: 64 * kib, ReservePath: path, Space: disk.space,
		Write: func(context.Context) error { return nil },
		Read:  func(context.Context) error { return nil },
	})
	if st := m.Check(context.Background()); !st.OK || !st.ReserveHeld {
		t.Fatalf("status = %+v, want the reserve held", st)
	}
	if info, err := os.Stat(path); err != nil || info.Size() != 64*kib {
		t.Fatalf("reserve at %s = %v, %v", path, info, err)
	}
	if disk.reserve() {
		t.Fatal("a second reserve was written in the data directory itself")
	}
}

func TestTheReserveIsNotWrittenWhereItWouldCrowdTheDisk(t *testing.T) {
	dir := t.TempDir()
	disk := &fakeDisk{dir: dir, other: 100 * mib, total: 200 * mib}
	m := New(Options{
		Dir: dir, Log: quiet(), Reserve: 32 * mib, Space: disk.space,
		Write: func(context.Context) error { return nil },
		Read:  func(context.Context) error { return nil },
	})
	if st := m.Check(context.Background()); !st.OK || st.ReserveHeld || disk.reserve() {
		t.Fatalf("status = %+v, want a healthy store and no reserve on a 200 MiB disk", st)
	}
}

// warnings counts the lines a monitor logs at Warn.
type warnings struct {
	slog.Handler
	n int
}

func (w *warnings) Handle(ctx context.Context, r slog.Record) error {
	if r.Level == slog.LevelWarn {
		w.n++
	}
	return nil
}

func TestAReserveThatCannotBeWrittenIsLeftAloneForAWhile(t *testing.T) {
	dir := t.TempDir()
	disk := &fakeDisk{dir: dir, other: 10 * gib, total: 100 * gib}
	// A directory stands where the reserve goes, so every byte is written and
	// the last step is refused: what a quota, or a filesystem that reports
	// room it cannot hand out, does to the real one.
	path := filepath.Join(dir, "staging", ReserveFile)
	if err := os.MkdirAll(filepath.Join(path, "in-the-way"), 0o700); err != nil {
		t.Fatal(err)
	}
	log := &warnings{Handler: slog.NewTextHandler(io.Discard, nil)}
	now := time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)
	m := New(Options{
		Dir: dir, Log: slog.New(log), Reserve: 64 * kib, ReservePath: path, Space: disk.space,
		Now:   func() time.Time { return now },
		Write: func(context.Context) error { return nil },
		Read:  func(context.Context) error { return nil },
	})
	// Five minutes of passes: one attempt and one line, not twenty of each.
	for range 20 {
		if st := m.Check(context.Background()); !st.OK || st.ReserveHeld {
			t.Fatalf("status = %+v, want a healthy store without its reserve", st)
		}
		now = now.Add(DefaultInterval)
	}
	if log.n != 1 {
		t.Fatalf("the reserve's failure was logged %d times in twenty passes, want once", log.n)
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Fatal("the failed attempt left its temporary file behind")
	}
	// Still refused when it is tried again: tried, and not said a second time.
	now = now.Add(reserveRetry)
	m.Check(context.Background())
	if log.n != 1 {
		t.Fatalf("the same failure was logged %d times, want once", log.n)
	}
	// What was in the way goes: the next attempt, when it is due, succeeds.
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	now = now.Add(DefaultInterval)
	if st := m.Check(context.Background()); st.ReserveHeld {
		t.Fatalf("status = %+v: the reserve was tried again before it was due", st)
	}
	now = now.Add(reserveRetry)
	if st := m.Check(context.Background()); !st.OK || !st.ReserveHeld {
		t.Fatalf("status = %+v, want the reserve held once it can be written", st)
	}
}

func TestABusyStoreIsAProblemOnlyOnceItHasLastedAMinute(t *testing.T) {
	dir := t.TempDir()
	disk := &fakeDisk{dir: dir, other: 10 * gib, total: 100 * gib}
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	began := now
	busy := errors.New("database is locked (5) (SQLITE_BUSY)")
	var writeErr error = busy
	m := New(Options{
		Dir: dir, Log: quiet(), Space: disk.space,
		Now:   func() time.Time { return now },
		Write: func(context.Context) error { return writeErr },
		Read:  func(context.Context) error { return nil },
	})
	for i := range 4 {
		if st := m.Check(context.Background()); !st.OK {
			t.Fatalf("after %ds of failed writes: %+v, want no problem yet", i*15, st)
		}
		now = now.Add(15 * time.Second)
	}
	st := m.Check(context.Background())
	if st.OK || st.Code != CodeUnwritable || st.Detail != busy.Error() || !st.Since.Equal(began) {
		t.Fatalf("after a minute: %+v, want store_unwritable since the first failure", st)
	}
	writeErr = nil
	now = now.Add(15 * time.Second)
	if st := m.Check(context.Background()); !st.OK || st.Since != nil || st.Detail != "" {
		t.Fatalf("after a write went through: %+v, want healthy", st)
	}
}

func TestAStoreThatCannotBeReadIsSaidAtOnce(t *testing.T) {
	dir := t.TempDir()
	disk := &fakeDisk{dir: dir, other: 10 * gib, total: 100 * gib}
	damaged := errors.New("database disk image is malformed (11)")
	m := New(Options{
		Dir: dir, Log: quiet(), Space: disk.space,
		Write: func(context.Context) error { return damaged },
		Read:  func(context.Context) error { return damaged },
	})
	if st := m.Check(context.Background()); st.OK || st.Code != CodeUnreadable || st.Detail != damaged.Error() {
		t.Fatalf("status = %+v, want store_unreadable", st)
	}
}

func TestTheProbeIsOneRowRewrittenInPlace(t *testing.T) {
	dir := t.TempDir()
	db := openDB(t, filepath.Join(dir, "vpsd.db"))
	m := New(Options{DB: db, Dir: dir, Log: quiet()})
	for range 3 {
		if st := m.Check(context.Background()); !st.OK || st.LastWriteAt == nil || st.TotalBytes == 0 {
			t.Fatalf("status = %+v, want a healthy store on a real filesystem", st)
		}
	}
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM settings`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("settings holds %d rows after three probes (%v), want the one", rows, err)
	}
}

func TestANilMonitorReportsAHealthyStore(t *testing.T) {
	var m *Monitor
	if st := m.Status(); !st.OK {
		t.Fatalf("status = %+v", st)
	}
	m.Run(context.Background())
}

// TestARealFullFilesystemStillTakesASignIn needs a small filesystem of its own
// to fill, so it runs only where JD_STOREHEALTH_SMALL_FS names an empty one of
// about 32 MiB, such as a tmpfs in a throwaway container:
//
//	docker run --rm --tmpfs /small:size=32m -e JD_STOREHEALTH_SMALL_FS=/small ...
func TestARealFullFilesystemStillTakesASignIn(t *testing.T) {
	dir := os.Getenv("JD_STOREHEALTH_SMALL_FS")
	if dir == "" {
		t.Skip("set JD_STOREHEALTH_SMALL_FS to an empty filesystem of about 32 MiB")
	}
	db := openDB(t, filepath.Join(dir, "vpsd.db"))
	signIn := func() error {
		_, err := db.Exec(`INSERT INTO sessions(token) VALUES (randomblob(2048))`)
		return err
	}
	m := New(Options{DB: db, Dir: dir, Log: slog.New(slog.NewTextHandler(os.Stderr, nil)), Reserve: 2 * mib})
	if st := m.Check(context.Background()); !st.OK || !st.ReserveHeld {
		t.Fatalf("before the disk fills: %+v, want a healthy store holding its reserve", st)
	}

	// Something else fills the disk to the last block.
	filler, err := os.Create(filepath.Join(dir, "filler"))
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(filler.Name())
	block := make([]byte, 64*kib)
	for {
		if _, err := filler.Write(block); err != nil {
			break
		}
	}
	for size := 4 * kib; size >= 1; size /= 2 {
		for {
			if _, err := filler.Write(block[:size]); err != nil {
				break
			}
		}
	}
	filler.Close()

	// What the audit measured: a sign-in cannot be saved.
	var failed error
	for range 400 {
		if failed = signIn(); failed != nil {
			break
		}
	}
	if !IsDiskFull(failed) {
		t.Fatalf("sign-ins on a full disk ended with %v, want SQLite refused for space", failed)
	}

	st := m.Check(context.Background())
	if st.OK || st.Code != CodeDiskFull || !st.ReserveReleased {
		t.Fatalf("on the full disk: %+v, want disk_full with the reserve released", st)
	}
	// A 2 MiB reserve is a sixteenth of the shipped one, and still room for
	// the sign-in that could not be saved and many after it.
	for i := range 50 {
		if err := signIn(); err != nil {
			t.Fatalf("sign-in %d after the reserve was released: %v", i, err)
		}
	}
	t.Logf("full disk: %d bytes free of %d after the release; 50 sign-ins saved", st.FreeBytes, st.TotalBytes)
}
