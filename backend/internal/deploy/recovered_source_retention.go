package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func withRecoveredSourcesLock(ctx context.Context, root string, apply func(*os.Root) error) error {
	if !filepath.IsAbs(root) {
		return ErrInvalidSource
	}
	if err := makePrivateDirectory(root); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return ErrInvalidSource
	}
	directory, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer directory.Close()
	lock, err := directory.OpenFile(".retention.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if info, err := lock.Stat(); err != nil || !info.Mode().IsRegular() {
		return ErrInvalidSource
	}
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return apply(directory)
}

func markRecoveredSnapshotUsed(directory *os.Root, relative string) error {
	info, err := directory.Lstat(relative)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrInvalidSource
	}
	file, err := directory.OpenFile(filepath.Join(relative, "last-used"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return err
	}
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() {
		file.Close()
		return ErrInvalidSource
	}
	_, writeErr := file.WriteString(strconv.FormatInt(time.Now().UTC().Unix(), 10))
	err = errors.Join(writeErr, file.Sync(), file.Close())
	return err
}

// PruneRecoveredSnapshots keeps every stored source revision and unexpired
// draft. Unreferenced snapshots get a full draft lifetime since their last
// capture, including the gap between staging and its draft transaction.
func (s *PlanningStore) PruneRecoveredSnapshots(ctx context.Context, recoveryRoot string) error {
	if !filepath.IsAbs(recoveryRoot) {
		return ErrInvalidSource
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	retained := map[string]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT config_json FROM deploy_sources UNION ALL SELECT json_extract(data_json,'$.source') FROM deploy_drafts WHERE committed_project_id=0 AND expires_at>?`, now.Unix())
	if err != nil {
		return err
	}
	for rows.Next() {
		var raw *string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if raw == nil {
			continue
		}
		var source DraftSourceConfig
		if json.Unmarshal([]byte(*raw), &source) != nil {
			rows.Close()
			return fmt.Errorf("recovered source references cannot be verified")
		}
		if source.Mode == SourceModeRecoveredSnapshot {
			if !recoveredSnapshotDigest(source.ResourceID) {
				rows.Close()
				return ErrInvalidSource
			}
			retained[strings.TrimPrefix(source.ResourceID, "sha256:")] = true
		}
	}
	readErr := rows.Err()
	rows.Close()
	if readErr != nil {
		return readErr
	}
	store := filepath.Join(recoveryRoot, "sources")
	if _, err := os.Lstat(store); os.IsNotExist(err) {
		return nil
	}
	return withRecoveredSourcesLock(ctx, store, func(directory *os.Root) error {
		file, err := directory.Open(".")
		if err != nil {
			return err
		}
		entries, err := file.ReadDir(8193)
		file.Close()
		if err != nil || len(entries) > 8192 {
			return fmt.Errorf("recovered source inventory exceeds its limit")
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			name := entry.Name()
			if !recoveredSnapshotDigest("sha256:"+name) || retained[name] || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			info, err := directory.Lstat(name)
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return ErrInvalidSource
			}
			lastUsed := info.ModTime()
			stamp, err := readRecoveredSnapshotStamp(directory, name)
			if err == nil {
				seconds, parseErr := strconv.ParseInt(string(stamp), 10, 64)
				if parseErr != nil || len(stamp) > 32 {
					return fmt.Errorf("recovered source retention evidence is invalid")
				}
				lastUsed = time.Unix(seconds, 0)
			} else if !os.IsNotExist(err) {
				return err
			}
			if !lastUsed.Before(now.Add(-draftTTL)) {
				continue
			}
			if err := directory.RemoveAll(name); err != nil {
				return err
			}
		}
		return nil
	})
}

func readRecoveredSnapshotStamp(directory *os.Root, name string) ([]byte, error) {
	file, err := directory.OpenFile(filepath.Join(name, "last-used"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 32 {
		return nil, ErrInvalidSource
	}
	return io.ReadAll(io.LimitReader(file, 33))
}

func recoveredSnapshotDigest(digest string) bool {
	return len(digest) == 71 && strings.HasPrefix(digest, "sha256:") && strings.Trim(strings.TrimPrefix(digest, "sha256:"), "0123456789abcdef") == ""
}

func (s *PlanningStore) StartRecoveredSourceCleanup(ctx context.Context, recoveryRoot string, report func(error)) {
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			if err := s.PruneRecoveredSnapshots(ctx, recoveryRoot); err != nil && ctx.Err() == nil && report != nil {
				report(err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
