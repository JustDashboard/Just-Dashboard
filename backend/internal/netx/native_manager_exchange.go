package netx

import (
	"bytes"
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

var nativeExchangeFiles = func(a, b string) error {
	return unix.Renameat2(unix.AT_FDCWD, nativeHostPath(a), unix.AT_FDCWD, nativeHostPath(b), unix.RENAME_EXCHANGE)
}

var nativeClaimStage = func(a, b string) error {
	return unix.Renameat2(unix.AT_FDCWD, nativeHostPath(a), unix.AT_FDCWD, nativeHostPath(b), unix.RENAME_NOREPLACE)
}

// Exchange retains the displaced inode instead of overwriting it. A native
// writer racing the preflight therefore remains available for verification
// and restoration, even if the applying process dies immediately afterward.
func nativeReplaceProfile(stage, current nativeProfileFile) error {
	if err := nativeCurrentFile(stage, stage.Identity); err != nil {
		return err
	}
	if err := nativeCurrentFile(current, current.Identity); err != nil {
		return err
	}
	if err := nativeExchangeFiles(stage.Path, current.Path); err != nil {
		return errors.New("native profile exchange failed; its files were preserved")
	}
	if err := nativeSyncDirectory(current.Path); err != nil {
		return err
	}
	displaced := current
	displaced.Path = stage.Path
	if err := nativeCurrentFile(displaced, current.Identity); err != nil {
		active := stage
		active.Path = current.Path
		if nativeCurrentFile(active, stage.Identity) == nil {
			if restoreErr := nativeExchangeFiles(stage.Path, current.Path); restoreErr != nil {
				return errors.New("a foreign native profile was retained in private staging; restoration needs review")
			}
			if syncErr := nativeSyncDirectory(current.Path); syncErr != nil {
				return syncErr
			}
		}
		return errors.New("a concurrent native profile change was preserved; activation was refused")
	}
	return nil
}

func nativeStageMatches(file *nativeProfileFile, allowed []nativeProfileFile) bool {
	for _, known := range allowed {
		if file.Identity == known.Identity && bytes.Equal(file.Data, known.Data) {
			return true
		}
	}
	return false
}

// The deterministic claim path makes a crash during cleanup recoverable. A
// no-replace rename cannot overwrite a prior claim; the captured inode/bytes
// are checked again before deletion. An unexpected captured file is restored
// only when its original name is still free, otherwise both entries survive.
func nativeRemoveOwnedStage(path string, allowed ...nativeProfileFile) error {
	claim := path + "-cleanup"
	for attempt := 0; attempt < 5; attempt++ {
		captured, err := nativeReadProfile(claim)
		if err == nil {
			if !nativeStageMatches(captured, allowed) {
				_ = nativeClaimStage(claim, path)
				_ = nativeSyncDirectory(path)
				return errors.New("native staging cleanup preserved foreign claimed ownership")
			}
			if err := os.Remove(nativeHostPath(claim)); err != nil {
				return errors.New("native staging cleanup could not remove its owned claim")
			}
			if err := nativeSyncDirectory(path); err != nil {
				return err
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return errors.New("native staging cleanup preserved unreadable claimed ownership")
		}
		file, err := nativeReadProfile(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil || !nativeStageMatches(file, allowed) {
			return errors.New("native staging cleanup found foreign or unreadable ownership")
		}
		if err := nativeClaimStage(path, claim); err != nil {
			return errors.New("native staging cleanup could not claim its exact file")
		}
		if err := nativeSyncDirectory(path); err != nil {
			return err
		}
	}
	return errors.New("native staging cleanup changed concurrently; ownership was preserved")
}
