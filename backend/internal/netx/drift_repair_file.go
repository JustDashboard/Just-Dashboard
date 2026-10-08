package netx

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var writeSelectedDriftFile = stageSelectedDriftFile

// Journal the staged inode before rename: content alone cannot distinguish an
// owned candidate from a later native replacement with identical bytes.
func stageSelectedDriftFile(path string, data []byte, mode os.FileMode, beforeRename func(string) error) error {
	if err := driftNoSymlinkPath(path); err != nil {
		return err
	}
	parent := filepath.Dir(path)
	tmp, err := os.CreateTemp(parent, "."+filepath.Base(path)+".drift-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	info, err := tmp.Stat()
	if err != nil || driftFileIdentity(info) == "" {
		return errors.Join(fmt.Errorf("the staged file identity cannot be established"), err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := beforeRename(driftFileIdentity(info)); err != nil {
		return err
	}
	if err := driftNoSymlinkPath(path); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	return syncNetworkDirectory(parent)
}

func validSelectedFileSnapshot(f recoverySnapshot) bool {
	digest, err := hex.DecodeString(f.CandidateSHA256)
	if err != nil || len(digest) != 32 || f.CandidateIdentity == "" || f.CandidateMode.Perm() != f.CandidateMode {
		return false
	}
	if !f.Exists {
		return len(f.Data) == 0 && f.BeforeIdentity == "" && f.RestoredIdentity == ""
	}
	return f.BeforeIdentity != "" && f.Mode.Perm() == f.Mode && strings.HasPrefix(string(f.Data), generatedHeader)
}

func selectedDriftFileRestorable(f recoverySnapshot) (bool, error) {
	if err := driftNoSymlinkPath(f.Path); err != nil {
		return false, err
	}
	data, err := readDriftFile(f.Path)
	if errors.Is(err, fs.ErrNotExist) && !f.Exists {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	info, err := os.Lstat(f.Path)
	if err != nil || !info.Mode().IsRegular() {
		return false, errors.Join(fmt.Errorf("the selected file identity cannot be established"), err)
	}
	identity := driftFileIdentity(info)
	if f.Exists && (identity == f.BeforeIdentity || identity == f.RestoredIdentity) && info.Mode().Perm() == f.Mode && string(data) == string(f.Data) {
		return false, nil
	}
	if identity != f.CandidateIdentity || info.Mode().Perm() != f.CandidateMode || digestBytes(data) != f.CandidateSHA256 || !strings.HasPrefix(string(data), generatedHeader) {
		return false, fmt.Errorf("the selected candidate was replaced or changed; native contents were preserved")
	}
	return true, nil
}

func restoreSelectedDriftFile(j *changeJournal, index int) error {
	f := j.Files[index]
	needsRestore, err := selectedDriftFileRestorable(f)
	if err != nil {
		return err
	}
	if !needsRestore {
		if !f.Exists {
			return (savedNetworkFile{}).restore(f.Path)
		}
		return syncNetworkDirectory(filepath.Dir(f.Path))
	}
	if !f.Exists {
		return (savedNetworkFile{}).restore(f.Path)
	}
	return stageSelectedDriftFile(f.Path, f.Data, f.Mode, func(identity string) error {
		j.Files[index].RestoredIdentity = identity
		if err := j.save(); err != nil {
			return err
		}
		if _, err := selectedDriftFileRestorable(f); err != nil {
			return err
		}
		return nil
	})
}
