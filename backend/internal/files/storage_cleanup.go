package files

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

var ErrStorageChanged = errors.New("the measured file changed; scan again")

type StorageSelection struct {
	File   StorageFile  `json:"file"`
	Kind   string       `json:"kind"`
	Keeper *StorageFile `json:"keeper,omitempty"`
	SHA256 string       `json:"sha256,omitempty"`
}

type StorageCleanupItem struct {
	Path      string `json:"path"`
	Removed   bool   `json:"removed"`
	Allocated int64  `json:"allocated"`
	Error     string `json:"error,omitempty"`
}

type StorageCleanupResult struct {
	Items        []StorageCleanupItem `json:"items"`
	RemovedBytes int64                `json:"removedBytes"`
}

// CleanupStorage acts only on the operator's explicit selections. Exact copies
// retain a reverified keeper; old temporary files are candidates, never a claim
// that their age proves they are unused. A private staging directory pins the
// selected entry before its identity is checked a second time and it is removed.
func (s *Service) CleanupStorage(ctx context.Context, selections []StorageSelection) (*StorageCleanupResult, error) {
	// Two concurrent requests must not each delete the other's retained copy.
	select {
	case s.storageCleanups <- struct{}{}:
		defer func() { <-s.storageCleanups }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if len(selections) == 0 || len(selections) > 50 {
		return nil, errors.New("select between 1 and 50 files")
	}
	paths := map[string]bool{}
	for _, selection := range selections {
		path, err := s.ResolveEntry(selection.File.Path)
		if err != nil {
			return nil, err
		}
		if selection.File.Identity == "" || paths[path] {
			return nil, errors.New("each selected file needs a unique path and its measured identity")
		}
		paths[path] = true
		if selection.Kind != "temporary" && selection.Kind != "duplicate" {
			return nil, errors.New("only old temporary files and verified copies can be cleaned here")
		}
	}
	for _, selection := range selections {
		if selection.Kind != "duplicate" {
			continue
		}
		if selection.Keeper == nil || len(selection.SHA256) != 64 {
			return nil, errors.New("a duplicate needs a measured keeper and its checksum")
		}
		if _, err := hex.DecodeString(selection.SHA256); err != nil {
			return nil, errors.New("invalid checksum")
		}
		keeper, err := s.ResolveEntry(selection.Keeper.Path)
		if err != nil {
			return nil, err
		}
		if paths[keeper] {
			return nil, errors.New("a retained copy cannot also be selected for cleanup")
		}
	}
	out := &StorageCleanupResult{Items: []StorageCleanupItem{}}
	for _, selection := range selections {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		item := StorageCleanupItem{Path: selection.File.Path}
		allocated, err := s.cleanupStorageFile(ctx, selection)
		if err != nil {
			item.Error = err.Error()
		} else {
			item.Removed = true
			item.Allocated = allocated
			out.RemovedBytes += allocated
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func (s *Service) cleanupStorageFile(ctx context.Context, selection StorageSelection) (int64, error) {
	path, err := s.ResolveEntry(selection.File.Path)
	if err != nil {
		return 0, err
	}
	for _, allowed := range s.roots {
		if path == allowed {
			return 0, errors.New("a configured root cannot be cleaned")
		}
	}
	root, err := s.openStorageDirectory(filepath.Dir(path))
	if err != nil {
		return 0, err
	}
	defer root.Close()
	name := filepath.Base(path)
	info, err := root.Lstat(name)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() || storageFile(path, info).Identity != selection.File.Identity {
		return 0, ErrStorageChanged
	}
	st := info.Sys().(*syscall.Stat_t)
	var retainedRoot *os.Root
	var retainedName string
	if st.Nlink != 1 {
		return 0, errors.New("this file has hard links; review them in Files")
	}
	if selection.Kind == "temporary" {
		if !storageTemporaryPath(path, s.storageHostRoot) || time.Since(info.ModTime()) < storageTempAge {
			return 0, errors.New("this is not an old temporary file")
		}
	} else {
		if info.Size() < storageMinDuplicate || info.Size() > storageMaxDuplicate {
			return 0, errors.New("copy is outside the verified size range")
		}
		keeperPath, err := s.ResolveEntry(selection.Keeper.Path)
		if err != nil {
			return 0, err
		}
		keeperRoot, err := s.openStorageDirectory(filepath.Dir(keeperPath))
		if err != nil {
			return 0, err
		}
		defer keeperRoot.Close()
		retainedRoot, retainedName = keeperRoot, filepath.Base(keeperPath)
		keeperHash, err := hashStorageFile(ctx, keeperRoot, filepath.Base(keeperPath), *selection.Keeper)
		if err != nil || keeperHash != selection.SHA256 {
			return 0, errors.New("the retained copy changed or cannot be verified; scan again")
		}
		fileHash, err := hashStorageFile(ctx, root, name, selection.File)
		if err != nil || fileHash != keeperHash {
			return 0, errors.New("this file no longer matches the retained copy; scan again")
		}
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := storageNotOpen(ctx, info, s.storageProc); err != nil {
		return 0, err
	}
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return 0, err
	}
	stage := ".jd-cleanup-" + hex.EncodeToString(random[:])
	if err := root.Mkdir(stage, 0o700); err != nil {
		return 0, err
	}
	defer root.Remove(stage)
	parked := filepath.Join(stage, "selected")
	if err := root.Rename(name, parked); err != nil {
		return 0, err
	}
	restore := func(cause error) (int64, error) {
		// Link fails if another entry now owns the old name. Never overwrite it
		// merely to undo a stale cleanup; retain the staged file and name it.
		if err := root.Link(parked, name); err != nil {
			return 0, fmt.Errorf("%v; original retained at %s", cause, filepath.Join(filepath.Dir(path), parked))
		}
		_ = root.Remove(parked)
		return 0, cause
	}
	moved, err := root.Lstat(parked)
	if err != nil {
		return restore(err)
	}
	// Rename updates ctime, so compare the inode, size, mtime and mode directly
	// with the already verified inode rather than its pre-rename fingerprint.
	if !moved.Mode().IsRegular() || !os.SameFile(info, moved) || moved.Size() != info.Size() || !moved.ModTime().Equal(info.ModTime()) || moved.Mode() != info.Mode() {
		return restore(ErrStorageChanged)
	}
	if err := ctx.Err(); err != nil {
		return restore(err)
	}
	if retainedRoot != nil {
		keeperInfo, err := retainedRoot.Lstat(retainedName)
		if err != nil || !keeperInfo.Mode().IsRegular() || storageFile(selection.Keeper.Path, keeperInfo).Identity != selection.Keeper.Identity {
			return restore(errors.New("the retained copy changed before removal; scan again"))
		}
	}
	if err := root.Remove(parked); err != nil {
		return restore(err)
	}
	return moved.Sys().(*syscall.Stat_t).Blocks * 512, nil
}
