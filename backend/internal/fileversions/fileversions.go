// Package fileversions keeps what the dashboard replaces when it saves over a
// file: the Files and Git editors' saves, a picture saved over its original,
// and a restore of an earlier version. Each content is kept once, under the
// data directory, named by its SHA-256 (the same revision contentguard gives
// the editor), and the store's file_versions table says which path had it,
// when it was replaced and by whom.
//
// What the dashboard last wrote at a path is remembered too
// (file_version_heads), so a save that finds something else there knows the
// file was changed outside the dashboard — in a shell, by a package upgrade —
// and says so on the version it keeps. That is the point of the feature for
// /etc: an edit made between two saves is captured at the next one.
//
// Keeping a version never decides whether a save happens. The files package
// reads what it is about to replace inside the guarded publish step and hands
// it over; Record runs after the save has succeeded and a failure here is
// logged by the caller, never returned to the person saving.
package fileversions

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// MaxFileBytes is the largest content kept. It is the editor's own limit:
// a file larger than this is not edited in the browser, and a picture larger
// than this saved over its original keeps no earlier copy.
const MaxFileBytes = 8 << 20

// Limits bound what is kept. PerFile 0 turns edit history off.
type Limits struct {
	PerFile    int
	TotalBytes int64
}

var DefaultLimits = Limits{PerFile: 50, TotalBytes: 512 << 20}

var (
	ErrNotFound = errors.New("no such version of this file")
	ErrMissing  = errors.New("this version's content is no longer kept")
	ErrDamaged  = errors.New("this version's kept content does not match its revision")
)

// Reasons a version was kept.
const (
	ReasonSave    = "save"
	ReasonImage   = "image"
	ReasonRestore = "restore"
	// ReasonUpload is an upload that replaced the file.
	ReasonUpload = "upload"
	// ReasonDisplaced is the dashboard's own save that was later replaced
	// outside the dashboard. Its time and author are the save's.
	ReasonDisplaced = "displaced"
)

type Keeper struct {
	db     *sql.DB
	dir    string
	limits Limits
	now    func() time.Time
	// mu orders Record, Move and the pruning they do, so a count or a total
	// is never read between another call's insert and its prune.
	mu    sync.Mutex
	swept sync.Once
}

// New keeps contents under dir (made 0700 on first use) and their rows in db.
func New(db *sql.DB, dir string, limits Limits) *Keeper {
	if limits.TotalBytes <= 0 {
		limits.TotalBytes = DefaultLimits.TotalBytes
	}
	return &Keeper{db: db, dir: dir, limits: limits, now: time.Now}
}

func (k *Keeper) Limits() Limits { return k.limits }

// Enabled says whether saves keep anything at all.
func (k *Keeper) Enabled() bool { return k != nil && k.limits.PerFile > 0 }

// Save is one save as the files package saw it.
type Save struct {
	// Path is the resolved path the save published to.
	Path string
	// Existed is whether a regular file was replaced. Content is what it
	// held, nil when it was larger than MaxFileBytes (TooLarge) or unreadable.
	Existed  bool
	Content  []byte
	TooLarge bool
	// Written is what the save put there, when the caller has it (a text
	// save, a restore); WrittenRevision and WrittenSize describe it when it
	// does not (a picture's upload is streamed, never held).
	Written         []byte
	WrittenRevision string
	WrittenSize     int64
	By              string
	Reason          string
}

func revisionOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func validRevision(revision string) bool {
	if len(revision) != 64 {
		return false
	}
	_, err := hex.DecodeString(revision)
	return err == nil
}

type head struct {
	revision string
	size     int64
	at       int64
	by       string
	kept     bool
}

// Record keeps what save replaced and remembers what it wrote.
func (k *Keeper) Record(ctx context.Context, save Save) error {
	if !k.Enabled() || save.Path == "" {
		return nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.swept.Do(func() { _ = k.sweep(ctx) })

	written := save.WrittenRevision
	writtenSize := save.WrittenSize
	if save.Written != nil {
		written, writtenSize = revisionOf(save.Written), int64(len(save.Written))
	}
	reason := save.Reason
	if reason == "" {
		reason = ReasonSave
	}
	now := k.now().UTC().Unix()

	previous, err := k.head(ctx, save.Path)
	if err != nil {
		return err
	}
	tx, err := k.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var failures []error
	if save.Existed && save.Content != nil {
		replaced := revisionOf(save.Content)
		outside := previous != nil && previous.revision != replaced
		if outside && previous.kept && validRevision(previous.revision) {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO file_versions(path, revision, size, at, by, reason, outside) VALUES(?,?,?,?,?,?,0)`,
				save.Path, previous.revision, previous.size, previous.at, previous.by, ReasonDisplaced); err != nil {
				return err
			}
		}
		// A save of the same bytes changed nothing; there is no earlier
		// content to keep.
		if replaced != written {
			if err := k.put(replaced, save.Content); err != nil {
				failures = append(failures, err)
			} else if _, err := tx.ExecContext(ctx,
				`INSERT INTO file_versions(path, revision, size, at, by, reason, outside) VALUES(?,?,?,?,?,?,?)`,
				save.Path, replaced, len(save.Content), now, save.By, reason, outside); err != nil {
				return err
			}
		}
	}
	kept := false
	if save.Written != nil && len(save.Written) <= MaxFileBytes {
		if err := k.put(written, save.Written); err != nil {
			failures = append(failures, err)
		} else {
			kept = true
		}
	}
	if validRevision(written) {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO file_version_heads(path, revision, size, at, by, kept) VALUES(?,?,?,?,?,?)
			 ON CONFLICT(path) DO UPDATE SET revision=excluded.revision, size=excluded.size,
			   at=excluded.at, by=excluded.by, kept=excluded.kept`,
			save.Path, written, writtenSize, now, save.By, kept); err != nil {
			return err
		}
	} else if _, err := tx.ExecContext(ctx, `DELETE FROM file_version_heads WHERE path = ?`, save.Path); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	failures = append(failures, k.prune(ctx, save.Path))
	return errors.Join(failures...)
}

func (k *Keeper) head(ctx context.Context, path string) (*head, error) {
	var h head
	err := k.db.QueryRowContext(ctx,
		`SELECT revision, size, at, by, kept FROM file_version_heads WHERE path = ?`, path).
		Scan(&h.revision, &h.size, &h.at, &h.by, &h.kept)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// Version is one earlier content of a file.
type Version struct {
	ID       int64     `json:"id"`
	Revision string    `json:"revision"`
	Size     int64     `json:"size"`
	At       time.Time `json:"at,omitzero"`
	By       string    `json:"by"`
	Reason   string    `json:"reason"`
	// Outside is set when this content was not what the dashboard last
	// wrote: it had been changed outside the dashboard.
	Outside bool `json:"outside"`
}

// Head is what the dashboard last wrote at a path.
type Head struct {
	Revision string    `json:"revision"`
	At       time.Time `json:"at,omitzero"`
	By       string    `json:"by"`
}

// History is what is kept for one path, newest first.
type History struct {
	Versions []Version `json:"versions"`
	Head     *Head     `json:"lastSave,omitempty"`
}

func (k *Keeper) History(ctx context.Context, path string) (*History, error) {
	out := &History{Versions: []Version{}}
	if k == nil {
		return out, nil
	}
	rows, err := k.db.QueryContext(ctx,
		`SELECT id, revision, size, at, by, reason, outside FROM file_versions WHERE path = ? ORDER BY id DESC`, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v Version
		var at int64
		if err := rows.Scan(&v.ID, &v.Revision, &v.Size, &at, &v.By, &v.Reason, &v.Outside); err != nil {
			return nil, err
		}
		v.At = time.Unix(at, 0).UTC()
		out.Versions = append(out.Versions, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if h, err := k.head(ctx, path); err != nil {
		return nil, err
	} else if h != nil {
		out.Head = &Head{Revision: h.revision, At: time.Unix(h.at, 0).UTC(), By: h.by}
	}
	return out, nil
}

// Content is one version's bytes. A version of another path is not found:
// the id alone never reaches another file's history.
func (k *Keeper) Content(ctx context.Context, path string, id int64) ([]byte, *Version, error) {
	if k == nil {
		return nil, nil, ErrNotFound
	}
	var v Version
	var at int64
	err := k.db.QueryRowContext(ctx,
		`SELECT id, revision, size, at, by, reason, outside FROM file_versions WHERE id = ? AND path = ?`, id, path).
		Scan(&v.ID, &v.Revision, &v.Size, &at, &v.By, &v.Reason, &v.Outside)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	v.At = time.Unix(at, 0).UTC()
	content, err := k.get(v.Revision)
	if err != nil {
		return nil, nil, err
	}
	return content, &v, nil
}

// Move carries the history of from, and of everything under it, to to: a
// move or a rename through the dashboard. A deleted file's history is left
// where it was, so a file put back at its path finds it again.
func (k *Keeper) Move(ctx context.Context, from, to string) error {
	if k == nil || from == "" || to == "" || from == to {
		return nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	tx, err := k.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"file_versions", "file_version_heads"} {
		// Compared as bytes ('/' + 1 is '0'), so a path with any name in it
		// moves, and /srv/app never takes /srv/app2's history along.
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT path FROM `+table+
			` WHERE path = ? OR (path > ? AND path < ?)`, from, from+"/", from+"0")
		if err != nil {
			return err
		}
		var paths []string
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				rows.Close()
				return err
			}
			paths = append(paths, p)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, p := range paths {
			moved := to + p[len(from):]
			if table == "file_version_heads" {
				_, err = tx.ExecContext(ctx, `UPDATE OR REPLACE file_version_heads SET path = ? WHERE path = ?`, moved, p)
			} else {
				_, err = tx.ExecContext(ctx, `UPDATE file_versions SET path = ? WHERE path = ?`, moved, p)
			}
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// prune holds a path to its count and everything to the total, oldest
// first, and removes contents nothing points at any more.
func (k *Keeper) prune(ctx context.Context, path string) error {
	var dropped []string
	rows, err := k.db.QueryContext(ctx,
		`SELECT id, revision FROM file_versions WHERE path = ? ORDER BY id DESC LIMIT -1 OFFSET ?`, path, k.limits.PerFile)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		var revision string
		if err := rows.Scan(&id, &revision); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
		dropped = append(dropped, revision)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := k.db.ExecContext(ctx, `DELETE FROM file_versions WHERE id = ?`, id); err != nil {
			return err
		}
	}
	if err := k.release(ctx, dropped); err != nil {
		return err
	}

	total, err := k.total(ctx)
	if err != nil {
		return err
	}
	for steps := 0; total > k.limits.TotalBytes && steps < 100000; steps++ {
		var kind, where, revision string
		var id, size int64
		err := k.db.QueryRowContext(ctx, `
			SELECT kind, id, path, revision, size FROM (
			  SELECT 'version' AS kind, id, path, revision, size, at FROM file_versions
			  UNION ALL
			  SELECT 'head', 0, path, revision, size, at FROM file_version_heads WHERE kept = 1
			) ORDER BY at, id LIMIT 1`).Scan(&kind, &id, &where, &revision, &size)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return err
		}
		if kind == "version" {
			_, err = k.db.ExecContext(ctx, `DELETE FROM file_versions WHERE id = ?`, id)
		} else {
			_, err = k.db.ExecContext(ctx, `UPDATE file_version_heads SET kept = 0 WHERE path = ?`, where)
		}
		if err != nil {
			return err
		}
		freed, err := k.releaseOne(ctx, revision)
		if err != nil {
			return err
		}
		if freed {
			total -= size
		}
	}
	return nil
}

// total is the size of every content kept, each counted once.
func (k *Keeper) total(ctx context.Context) (int64, error) {
	var total int64
	err := k.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(size), 0) FROM (
		  SELECT revision, MAX(size) AS size FROM (
		    SELECT revision, size FROM file_versions
		    UNION ALL
		    SELECT revision, size FROM file_version_heads WHERE kept = 1
		  ) GROUP BY revision
		)`).Scan(&total)
	return total, err
}

func (k *Keeper) release(ctx context.Context, revisions []string) error {
	for _, revision := range revisions {
		if _, err := k.releaseOne(ctx, revision); err != nil {
			return err
		}
	}
	return nil
}

// releaseOne removes a content once no version and no head points at it.
func (k *Keeper) releaseOne(ctx context.Context, revision string) (bool, error) {
	var used int
	err := k.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM file_versions WHERE revision = ?) +
		(SELECT COUNT(*) FROM file_version_heads WHERE revision = ? AND kept = 1)`, revision, revision).Scan(&used)
	if err != nil || used > 0 {
		return false, err
	}
	if err := k.remove(revision); err != nil {
		return false, err
	}
	return true, nil
}

// sweep removes contents left behind by a save whose row was never written:
// a crash between the copy and the commit. Run once per process.
func (k *Keeper) sweep(ctx context.Context) error {
	objects := filepath.Join(k.dir, "objects")
	shards, err := os.ReadDir(objects)
	if err != nil {
		return nil
	}
	for _, shard := range shards {
		names, err := os.ReadDir(filepath.Join(objects, shard.Name()))
		if err != nil {
			continue
		}
		for _, name := range names {
			revision := name.Name()
			if !validRevision(revision) {
				// A staging file a crash left behind.
				_ = os.Remove(filepath.Join(objects, shard.Name(), revision))
				continue
			}
			if _, err := k.releaseOne(ctx, revision); err != nil {
				return err
			}
		}
	}
	return nil
}

func (k *Keeper) objectPath(revision string) (string, error) {
	if !validRevision(revision) {
		return "", fmt.Errorf("not a content revision: %q", revision)
	}
	return filepath.Join(k.dir, "objects", revision[:2], revision), nil
}

// put keeps content under its revision, root's alone: these are copies of
// files that may hold secrets.
func (k *Keeper) put(revision string, content []byte) error {
	target, err := k.objectPath(revision)
	if err != nil {
		return err
	}
	if _, err := os.Stat(target); err == nil {
		return nil
	}
	shard := filepath.Dir(target)
	for _, dir := range []string{k.dir, filepath.Join(k.dir, "objects"), shard} {
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(shard, ".staging-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}

func (k *Keeper) get(revision string) ([]byte, error) {
	target, err := k.objectPath(revision)
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(target)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrMissing
	}
	if err != nil {
		return nil, err
	}
	if revisionOf(content) != revision {
		return nil, ErrDamaged
	}
	return content, nil
}

func (k *Keeper) remove(revision string) error {
	target, err := k.objectPath(revision)
	if err != nil {
		return nil
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
