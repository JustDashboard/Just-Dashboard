package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// A copy of the dashboard's own database, taken before an update touches it.
//
// A rollback puts back the code and the images. It never put back data, on the
// grounds that schema changes are additive and the old version can read what
// the new one migrated. That holds for columns. It does not hold for what a
// new version does to rows when it first opens the store (archived project
// names are rewritten in place, legacy deployments are mapped into new
// tables), nor for a release whose migration is simply wrong, and the only way
// back from either was whatever backup job the operator happened to have. The
// copy costs a second or two and the size of the database, and it is taken at
// the one moment that is certain to be before the damage.
//
// It is not restored automatically. Going back to it also throws away
// everything written since, which on a rollback that worked is an hour of
// audit log and metrics lost for nothing, so that is a decision for a person
// with the file's path in front of them. The rollback's message and the
// administration guide give the path and the steps.

const (
	// PreUpdateDir holds the copies, inside the data directory: the one place
	// both halves of an update mount, that an uninstall's wipe removes, and
	// that nobody but root reads.
	PreUpdateDir = "pre-update"
	// keptCopies is how many are left after a new one is made: this update's
	// and the one before, so a problem noticed one release late still has a
	// copy from before it.
	keptCopies = 2
	// copyTimeout bounds the copy. It runs inside the request that starts the
	// update; a database that takes longer than this to copy is one the
	// operator is told was not copied, rather than a button that never answers.
	copyTimeout = 3 * time.Minute
	// copyHeadroom is the free space that must remain after the copy. Filling
	// the disk the live database writes to, in order to protect it, would be
	// the worse outcome, and the build that follows needs room of its own.
	copyHeadroom = 512 << 20
)

// Database is as much of the dashboard's store as an update needs: enough to
// copy it without this package opening it.
type Database struct {
	// Path is the live file, read only for its size.
	Path string
	// CopyTo writes a consistent copy to path, which exists and is empty. It
	// is the store's own connection doing it (VACUUM INTO), so the copy is a
	// whole database as of one moment even while the dashboard is writing.
	CopyTo func(ctx context.Context, path string) error
}

// copyDatabase writes the pre-update copy and returns its path.
//
// An error means there is no copy. It does not stop the update, for the reason
// a failed image tag does not: the update may be the fix for whatever made the
// copy fail, and the operator is told in the transcript before anything is
// built.
func copyDatabase(ctx context.Context, dataDir string, db *Database, run *Run) (string, error) {
	if db == nil || db.CopyTo == nil {
		return "", errors.New("this dashboard has no database of its own to copy")
	}
	dir := filepath.Join(dataDir, PreUpdateDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := roomFor(db.Path, dir); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("vpsd-%s-%s.db", safeName(run.FromVersion), run.ID))
	// Created here, empty and readable by root alone: the copy holds every
	// account's password hash and every sealed secret, and a file SQLite
	// creates takes the process's umask instead.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	file.Close()

	ctx, cancel := context.WithTimeout(ctx, copyTimeout)
	defer cancel()
	if err := db.CopyTo(ctx, path); err != nil {
		os.Remove(path)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("it was not finished within %s", copyTimeout)
		}
		return "", err
	}
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		os.Remove(path)
		return "", errors.New("the copy came out empty")
	}
	pruneCopies(dir, keptCopies)
	return path, nil
}

// roomFor refuses a copy that would leave the disk short.
func roomFor(database, dir string) error {
	var need int64
	for _, name := range []string{database, database + "-wal"} {
		if info, err := os.Stat(name); err == nil {
			need += info.Size()
		}
	}
	var fs syscall.Statfs_t
	if database == "" || syscall.Statfs(dir, &fs) != nil {
		// Not knowing is not a reason to skip the copy: SQLite fails cleanly
		// when it runs out of room, and the partial file is removed.
		return nil
	}
	free := int64(fs.Bavail) * int64(fs.Bsize)
	if free < need+copyHeadroom {
		return fmt.Errorf("the disk holding %s has %d MB free, and the copy needs %d MB with %d MB left over",
			dir, free>>20, need>>20, copyHeadroom>>20)
	}
	return nil
}

// pruneCopies removes all but the newest keep copies. Only files this package
// names are candidates: anything else an operator put in the directory stays.
func pruneCopies(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type copyFile struct {
		path string
		at   time.Time
	}
	var copies []copyFile
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || !strings.HasPrefix(name, "vpsd-") || !strings.HasSuffix(name, ".db") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		copies = append(copies, copyFile{filepath.Join(dir, name), info.ModTime()})
	}
	sort.Slice(copies, func(i, j int) bool {
		if copies[i].at.Equal(copies[j].at) {
			return copies[i].path > copies[j].path
		}
		return copies[i].at.After(copies[j].at)
	})
	for i, c := range copies {
		if i >= keep {
			os.Remove(c.path)
		}
	}
}

// safeName keeps a version usable as part of a file name whatever it holds.
func safeName(version string) string {
	clean := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '.' || r == '-' {
			return r
		}
		return '_'
	}, version)
	if clean == "" {
		return "unknown"
	}
	return clean
}

// copyNote is the sentence a rollback's message ends with about the copy.
func copyNote(run *Run) string {
	if run.DatabaseCopy == "" {
		return ""
	}
	return fmt.Sprintf("\n\nThe dashboard's database as it was before the update is at %s. It was not put back: "+
		"%s reads the database as it is now. If data is wrong after this, the administration guide says how to restore that copy",
		run.DatabaseCopy, run.FromVersion)
}
