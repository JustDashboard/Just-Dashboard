package backups

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type SQLiteSnapshot struct {
	Path   string `json:"path"`
	Method string `json:"method"`
	Digest string `json:"digest"`
}

type SQLiteSnapshotter func(context.Context, string, string) (string, error)

func (r *Runner) WithSQLiteSnapshotter(snapshot SQLiteSnapshotter) *Runner {
	r.sqliteSnapshot = snapshot
	return r
}

// WithLiveSQLite names the SQLite files this process writes all the time —
// the dashboard's request history, committed every few seconds. A job whose
// sources hold one snapshots it as it does the files it lists, whether or not
// it lists it: copied as a file under a writer, the archive's own checks fail
// the run (the size the header promised, the size and time after the copy),
// and a job saved before the file existed — the dashboard's data job from
// before the request history — cannot have listed it.
func (r *Runner) WithLiveSQLite(paths func() []string) *Runner {
	r.liveSQLite = paths
	return r
}

// snapshotPaths is what a run snapshots: the job's own SQLite files, then each
// live one inside its sources that it does not exclude.
func (r *Runner) snapshotPaths(job *Job) []string {
	out := append([]string(nil), job.SQLitePaths...)
	if r.liveSQLite == nil {
		return out
	}
	for _, path := range r.liveSQLite() {
		path = filepath.Clean(path)
		if path == "." || slices.Contains(out, path) || excluded(path, job.Excludes) {
			continue
		}
		inside := false
		for _, source := range job.Sources {
			rel, err := filepath.Rel(filepath.Clean(source), path)
			if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				inside = true
			}
		}
		if info, err := os.Lstat(path); !inside || err != nil || !info.Mode().IsRegular() {
			continue
		}
		out = append(out, path)
	}
	return out
}

func (s *Store) validateSQLitePaths(job *Job) error {
	if len(job.SQLitePaths) > 128 {
		return errors.New("at most 128 SQLite snapshots can be included in a backup")
	}
	seen := map[string]bool{}
	for i, path := range job.SQLitePaths {
		resolved, err := s.paths.Resolve(path)
		if err != nil {
			return err
		}
		covered := false
		for _, source := range job.Sources {
			rel, err := filepath.Rel(source, resolved)
			if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				covered = true
			}
		}
		if !covered || excluded(resolved, job.Excludes) || seen[resolved] {
			return errors.New("each SQLite file must be a distinct, unexcluded file inside the backup sources")
		}
		info, err := os.Lstat(resolved)
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("a SQLite snapshot source is not an accessible regular file")
		}
		job.SQLitePaths[i] = resolved
		seen[resolved] = true
	}
	slices.Sort(job.SQLitePaths)
	return nil
}

func (r *Runner) prepareSQLiteSnapshots(ctx context.Context, job *Job, stage string) (map[string]string, []SQLiteSnapshot, error) {
	paths := map[string]string{}
	var evidence []SQLiteSnapshot
	wanted := r.snapshotPaths(job)
	if len(wanted) > 0 && r.sqliteSnapshot == nil {
		return nil, nil, errors.New("the SQLite snapshot owner is unavailable")
	}
	for i, path := range wanted {
		directory := filepath.Join(stage, fmt.Sprintf("sqlite-%04d", i+1))
		if err := os.Mkdir(directory, 0o700); err != nil {
			return nil, nil, err
		}
		snapshot, err := r.sqliteSnapshot(ctx, path, directory)
		if err != nil {
			return nil, nil, fmt.Errorf("SQLite consistent snapshot failed: %w", err)
		}
		if filepath.Dir(snapshot) != directory {
			return nil, nil, errors.New("SQLite snapshot owner returned an uncontained file")
		}
		info, err := os.Lstat(snapshot)
		if err != nil || !info.Mode().IsRegular() {
			return nil, nil, errors.New("SQLite snapshot is not a regular file")
		}
		digest, err := fileDigest(ctx, snapshot)
		if err != nil {
			return nil, nil, err
		}
		paths[path] = snapshot
		evidence = append(evidence, SQLiteSnapshot{Path: path, Method: "sqlite_vacuum_into", Digest: digest})
	}
	return paths, evidence, nil
}

func sqliteSidecar(path string, snapshots map[string]string) bool {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if original, ok := strings.CutSuffix(path, suffix); ok {
			if _, found := snapshots[original]; found {
				return true
			}
		}
	}
	return false
}
