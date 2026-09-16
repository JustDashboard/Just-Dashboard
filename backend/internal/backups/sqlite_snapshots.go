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
	if len(job.SQLitePaths) > 0 && r.sqliteSnapshot == nil {
		return nil, nil, errors.New("the SQLite snapshot owner is unavailable")
	}
	for i, path := range job.SQLitePaths {
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
