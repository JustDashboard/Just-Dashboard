package selfupdate

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/selfupdate/selfupdatetest"
	_ "modernc.org/sqlite"
)

// liveDatabase is a SQLite database in WAL mode with one row in it, copied the
// way the dashboard copies its own: VACUUM INTO on the connection that has it
// open. A fake would say whatever these tests assumed about a file that
// already exists and is empty, which is the one thing the copy depends on.
func liveDatabase(t *testing.T) (*Database, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vpsd.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, statement := range []string{
		`PRAGMA journal_mode = WAL`,
		`CREATE TABLE accounts (name TEXT)`,
		`INSERT INTO accounts VALUES ('before the update')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return &Database{Path: path, CopyTo: func(ctx context.Context, to string) error {
		_, err := db.ExecContext(ctx, "VACUUM INTO ?", to)
		return err
	}}, db
}

func startUpdate(t *testing.T, dir string, db *Database, id string) (*Run, *Store) {
	t.Helper()
	store := NewStore(dir)
	installer := NewInstaller(store, dir, "unix:///var/run/docker.sock", quiet()).WithDatabase(db)
	// Start names a run by the second it began in; two in one test need two names.
	run, err := installer.Start(context.Background(),
		&Location{Dir: "/opt/jd", Image: "img", Compose: "docker-compose.yml"},
		StartRequest{From: "0.5", To: "0.6"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		run.ID = id
	}
	return run, store
}

// The copy is taken before the updater exists, is a whole database as of that
// moment, and is root's alone to read.
func TestAnUpdateCopiesTheDatabaseBeforeItStarts(t *testing.T) {
	calls := selfupdatetest.Docker(t)
	database, live := liveDatabase(t)
	dir := t.TempDir()
	run, store := startUpdate(t, dir, database, "")

	if run.DatabaseCopy == "" || filepath.Dir(run.DatabaseCopy) != filepath.Join(dir, PreUpdateDir) {
		t.Fatalf("copy recorded as %q", run.DatabaseCopy)
	}
	if !strings.Contains(filepath.Base(run.DatabaseCopy), "0.5") {
		t.Errorf("%s does not say which version's data it is", filepath.Base(run.DatabaseCopy))
	}
	info, err := os.Stat(run.DatabaseCopy)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the copy is mode %o; it holds every password hash and sealed secret", info.Mode().Perm())
	}
	if recorded, _ := store.Load(); recorded == nil || recorded.DatabaseCopy != run.DatabaseCopy {
		t.Fatalf("the record the updater reads does not name the copy: %+v", recorded)
	}
	if !strings.Contains(store.Transcript(), "database copy: "+run.DatabaseCopy) {
		t.Errorf("the transcript does not name the copy:\n%s", store.Transcript())
	}

	// What is written after the copy is not in it.
	if _, err := live.Exec(`INSERT INTO accounts VALUES ('after the update')`); err != nil {
		t.Fatal(err)
	}
	copied, err := sql.Open("sqlite", run.DatabaseCopy)
	if err != nil {
		t.Fatal(err)
	}
	defer copied.Close()
	var names string
	if err := copied.QueryRow(`SELECT group_concat(name) FROM accounts`).Scan(&names); err != nil {
		t.Fatalf("the copy does not open as a database: %v", err)
	}
	if names != "before the update" {
		t.Fatalf("the copy holds %q", names)
	}

	// The copy came first: the updater is started only afterwards.
	if got := calls(); len(got) == 0 || !strings.Contains(got[len(got)-1], "run --detach") {
		t.Fatalf("docker calls %q", got)
	}
}

// Two are kept: this update's and the one before it.
func TestOnlyTheNewestTwoCopiesAreKept(t *testing.T) {
	selfupdatetest.Docker(t)
	database, _ := liveDatabase(t)
	dir := t.TempDir()
	copies := filepath.Join(dir, PreUpdateDir)
	if err := os.MkdirAll(copies, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	for i, name := range []string{"vpsd-0.3-20260101T000000Z.db", "vpsd-0.4-20260201T000000Z.db", "notes.txt", "vpsd.db.bak"} {
		path := filepath.Join(copies, name)
		write(t, path, "x")
		at := old.Add(time.Duration(i) * time.Hour)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	run, _ := startUpdate(t, dir, database, "")

	entries, err := os.ReadDir(copies)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	want := []string{"notes.txt", "vpsd-0.4-20260201T000000Z.db", filepath.Base(run.DatabaseCopy), "vpsd.db.bak"}
	if strings.Join(names, " ") != strings.Join(want, " ") {
		t.Fatalf("left %q, want %q: the oldest copy goes, and nothing this did not write", names, want)
	}
}

// A copy that cannot be made is said in the transcript and does not stop the
// update, which may be the fix for whatever is wrong.
func TestAnUpdateStillStartsWhenTheDatabaseCannotBeCopied(t *testing.T) {
	for name, database := range map[string]*Database{
		"the copy fails": {CopyTo: func(context.Context, string) error { return errors.New("disk I/O error") }},
		"no database":    nil,
	} {
		t.Run(name, func(t *testing.T) {
			calls := selfupdatetest.Docker(t)
			dir := t.TempDir()
			run, store := startUpdate(t, dir, database, "")
			if run.DatabaseCopy != "" {
				t.Fatalf("recorded a copy that was not made: %q", run.DatabaseCopy)
			}
			transcript := store.Transcript()
			if !strings.Contains(transcript, "the database was not copied before the update") ||
				!strings.Contains(transcript, "not the data") {
				t.Errorf("the transcript does not say there is no copy:\n%s", transcript)
			}
			if got := strings.Join(calls(), "\n"); !strings.Contains(got, "run --detach") {
				t.Errorf("the update did not start: %s", got)
			}
			if left, _ := filepath.Glob(filepath.Join(dir, PreUpdateDir, "*")); len(left) != 0 {
				t.Errorf("a failed copy left %q behind", left)
			}
		})
	}
}

// The rollback puts back code and images. Its message is where the operator
// learns there is a copy of the data too, and that it was not put back.
func TestARollbackNamesTheCopyOfTheDatabase(t *testing.T) {
	run := &Run{FromVersion: "0.5", ToVersion: "0.6", Dir: "/opt/jd", FromCommit: "abc123",
		DatabaseCopy: "/var/lib/just-dashboard/pre-update/vpsd-0.5-20261110T053000Z.db"}
	cause := errors.New("health check failed")
	for name, err := range map[string]error{
		"a rollback that worked":         rollbackResult(run, "docker-compose.yml", nil, cause, nil),
		"a rollback that did not finish": rollbackResult(run, "docker-compose.yml", nil, cause, []string{"could not move the checkout back"}),
	} {
		for _, want := range []string{run.DatabaseCopy, "was not put back", "administration guide"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s does not say %q:\n%v", name, want, err)
			}
		}
	}
	run.DatabaseCopy = ""
	if err := rollbackResult(run, "docker-compose.yml", nil, cause, nil); strings.Contains(err.Error(), "database") {
		t.Errorf("a rollback with no copy mentions one:\n%v", err)
	}
}

func TestACopyThatWouldLeaveTheDiskShortIsNotMade(t *testing.T) {
	dir := t.TempDir()
	// The file's size is what is asked for, and no disk has an exabyte free.
	huge := filepath.Join(dir, "vpsd.db")
	file, err := os.Create(huge)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(1 << 60); err != nil {
		file.Close()
		t.Skipf("this filesystem cannot hold a sparse file to measure against: %v", err)
	}
	file.Close()
	called := false
	_, err = copyDatabase(context.Background(), dir, &Database{Path: huge, CopyTo: func(context.Context, string) error {
		called = true
		return nil
	}}, &Run{ID: "x", FromVersion: "0.5"})
	if err == nil || called || !strings.Contains(err.Error(), "MB free") {
		t.Fatalf("err %v, copy attempted %v", err, called)
	}
}
