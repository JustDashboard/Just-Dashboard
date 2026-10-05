package procs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"golang.org/x/sys/unix"
)

var absentStartupFingerprint = captureDigest([]byte("absent"))

type startupAuthority struct {
	Data              []byte
	Target            string
	UID, GID, Mode    uint32
	Fingerprint       string
	ParentFingerprint string
	ParentUID         uint32
	Absent            bool
}

// All path components below the trusted host root are opened without following
// links. Keeping a directory descriptor across a publish avoids writes through
// a replaced parent, and its captured identity fences later retries.
func openStartupParent(path string) (*os.File, string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "\x00\r\n") {
		return nil, "", ErrHostWorkloadChanged
	}
	fd, err := unix.Open(hostexec.HostPath("/"), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", err
	}
	parts := strings.Split(strings.TrimPrefix(filepath.Dir(path), "/"), "/")
	for _, part := range parts {
		if part == "" {
			continue
		}
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if openErr != nil {
			return nil, "", openErr
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), filepath.Dir(path)), filepath.Base(path), nil
}

func startupParentFingerprint(file *os.File) (string, uint32, error) {
	var st unix.Stat_t
	if unix.Fstat(int(file.Fd()), &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Mode&0022 != 0 {
		return "", 0, fmt.Errorf("startup authority directory is writable by another account")
	}
	return captureDigest([]byte(fmt.Sprintf("%d:%d:%d:%d:%d", st.Dev, st.Ino, st.Mode, st.Uid, st.Gid))), st.Uid, nil
}

func readStartupAuthority(path, kind string) (startupAuthority, error) {
	parent, name, err := openStartupParent(path)
	if err != nil {
		return startupAuthority{}, err
	}
	defer parent.Close()
	return readStartupAt(parent, name, kind)
}
func readStartupAt(parent *os.File, name, kind string) (startupAuthority, error) {
	fingerprint, uid, err := startupParentFingerprint(parent)
	if err != nil {
		return startupAuthority{}, err
	}
	result := startupAuthority{ParentFingerprint: fingerprint, ParentUID: uid}
	var before unix.Stat_t
	err = unix.Fstatat(int(parent.Fd()), name, &before, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		result.Absent = true
		result.Fingerprint = absentStartupFingerprint
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.UID, result.GID, result.Mode = before.Uid, before.Gid, before.Mode&07777
	if kind == "systemd_link" {
		if before.Mode&unix.S_IFMT != unix.S_IFLNK {
			return result, fmt.Errorf("systemd startup authority is not a symlink")
		}
		buffer := make([]byte, 4097)
		n, err := unix.Readlinkat(int(parent.Fd()), name, buffer)
		if err != nil || n > 4096 {
			return result, fmt.Errorf("systemd startup link is unverified")
		}
		result.Target = string(buffer[:n])
		if strings.ContainsAny(result.Target, "\x00\r\n") {
			return result, ErrHostWorkloadChanged
		}
	} else {
		if before.Mode&unix.S_IFMT != unix.S_IFREG || before.Mode&0444 == 0 || before.Size > 4<<20 {
			return result, fmt.Errorf("PM2 startup authority is unavailable")
		}
		fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if err != nil {
			return result, err
		}
		file := os.NewFile(uintptr(fd), name)
		var opened unix.Stat_t
		if unix.Fstat(fd, &opened) != nil || opened.Dev != before.Dev || opened.Ino != before.Ino {
			file.Close()
			return result, ErrHostWorkloadChanged
		}
		result.Data, err = io.ReadAll(io.LimitReader(file, (4<<20)+1))
		file.Close()
		if err != nil || len(result.Data) > 4<<20 {
			return result, fmt.Errorf("PM2 startup authority is unavailable")
		}
	}
	var after unix.Stat_t
	if unix.Fstatat(int(parent.Fd()), name, &after, unix.AT_SYMLINK_NOFOLLOW) != nil || before.Dev != after.Dev || before.Ino != after.Ino || before.Mode != after.Mode || before.Uid != after.Uid || before.Gid != after.Gid || before.Size != after.Size || before.Mtim != after.Mtim || before.Ctim != after.Ctim {
		return result, ErrHostWorkloadChanged
	}
	payload := result.Data
	if kind == "systemd_link" {
		payload = []byte(result.Target)
	}
	result.Fingerprint = startupContentFingerprint(payload, result.UID, result.GID, result.Mode)
	return result, nil
}
func startupContentFingerprint(data []byte, uid, gid, mode uint32) string {
	return captureDigest(append([]byte(fmt.Sprintf("%d:%d:%d\x00", uid, gid, mode)), data...))
}

func executeStartupHandoff(ctx context.Context, plan *NativeStartupPlan, journal *NativeStartupJournal, persist func(NativeStartupJournal) error, restore bool) error {
	if persist == nil {
		return fmt.Errorf("durable startup journal callback is required")
	}
	if err := initializeStartupJournal(plan, journal); err != nil {
		return err
	}
	for i, action := range plan.Actions {
		current, err := readStartupAuthority(action.Path, action.Kind)
		if err != nil || current.ParentFingerprint != action.ParentFingerprint {
			return ErrHostWorkloadChanged
		}
		state := journal.Actions[i]
		allowed := state.BeforeFingerprint
		switch state.Phase {
		case "retired":
			allowed = state.RetiredFingerprint
		case "restored":
			allowed = state.RestoredFingerprint
		case "retiring":
			if current.Fingerprint != state.BeforeFingerprint && current.Fingerprint != state.RetiredFingerprint {
				return ErrHostWorkloadChanged
			}
			continue
		case "restoring":
			if current.Fingerprint != state.RetiredFingerprint && current.Fingerprint != state.RestoredFingerprint {
				return ErrHostWorkloadChanged
			}
			continue
		case "prepared":
		default:
			return ErrHostWorkloadChanged
		}
		if current.Fingerprint != allowed {
			return ErrHostWorkloadChanged
		}
	}
	phase := "retiring"
	finished := "retired"
	if restore {
		phase = "restoring"
		finished = "restored"
	}
	journal.Phase = phase
	if err := persist(*journal); err != nil {
		return err
	}
	for i, action := range plan.Actions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := executeStartupAction(ctx, action, &journal.Actions[i], journal, persist, restore); err != nil {
			return err
		}
	}
	journal.Phase = finished
	return persist(*journal)
}

func executeStartupAction(ctx context.Context, action NativeStartupAction, state *NativeStartupActionJournal, journal *NativeStartupJournal, persist func(NativeStartupJournal) error, restore bool) error {
	parent, name, err := openStartupParent(action.Path)
	if err != nil {
		return err
	}
	defer parent.Close()
	if err := lockStartupParent(ctx, parent); err != nil {
		return err
	}
	defer unix.Flock(int(parent.Fd()), unix.LOCK_UN)
	current, err := readStartupAt(parent, name, action.Kind)
	if err != nil || current.ParentFingerprint != action.ParentFingerprint {
		return ErrHostWorkloadChanged
	}
	if restore && state.Phase == "retiring" {
		// Reconcile an interrupted publish before preparing its inverse.
		unix.Flock(int(parent.Fd()), unix.LOCK_UN)
		if err := executeStartupAction(ctx, action, state, journal, persist, false); err != nil {
			return err
		}
		if err := lockStartupParent(ctx, parent); err != nil {
			return err
		}
		current, err = readStartupAt(parent, name, action.Kind)
		if err != nil {
			return err
		}
	}
	finished, pending := "retired", "retiring"
	source := state.BeforeFingerprint
	planned := state.RetiredFingerprint
	if restore {
		finished, pending = "restored", "restoring"
		source = state.RetiredFingerprint
		planned = state.RestoredFingerprint
	}
	if state.Phase == finished {
		if current.Fingerprint != planned {
			return ErrHostWorkloadChanged
		}
		return cleanupStartupPending(parent, state, action.Kind)
	}
	if restore && (state.Phase == "prepared" || state.Phase == "restored") {
		state.Phase = "restored"
		state.RestoredFingerprint = current.Fingerprint
		return persist(*journal)
	}
	if !restore && state.Phase == "restored" {
		return fmt.Errorf("a restored handoff requires a fresh prepared plan")
	}
	if state.Phase != pending {
		if current.Fingerprint != source {
			return ErrHostWorkloadChanged
		}
		if action.Kind == "absent" || (action.Kind == "pm2_file" && len(action.Selected) == 0) {
			state.Phase = finished
			if restore {
				state.RestoredFingerprint = current.Fingerprint
			} else {
				state.RetiredFingerprint = current.Fingerprint
			}
			return persist(*journal)
		}
		temporary, err := startupTemporaryName()
		if err != nil {
			return err
		}
		state.PendingPath = filepath.Join(filepath.Dir(action.Path), temporary)
		if action.Kind == "pm2_file" {
			data, err := rewriteStartupRows(current.Data, action, restore)
			if err != nil {
				return err
			}
			planned = startupContentFingerprint(data, action.UID, action.GID, action.Mode)
			if err := createStartupTemporaryFile(parent, temporary, data, action); err != nil {
				return err
			}
		} else if restore {
			if !current.Absent {
				return ErrHostWorkloadChanged
			}
			if err := unix.Symlinkat(action.Target, int(parent.Fd()), temporary); err != nil {
				return err
			}
			if err := unix.Fchownat(int(parent.Fd()), temporary, int(action.UID), int(action.GID), unix.AT_SYMLINK_NOFOLLOW); err != nil {
				unix.Unlinkat(int(parent.Fd()), temporary, 0)
				return err
			}
			planned = action.Fingerprint
		} else {
			planned = absentStartupFingerprint
		}
		if err := parent.Sync(); err != nil {
			return err
		}
		state.Phase = pending
		if restore {
			state.RestoredFingerprint = planned
		} else {
			state.RetiredFingerprint = planned
		}
		// Rejected intent cannot publish startup changes. The original stays
		// intact, and a durably recorded intent can rebuild the same temporary
		// bytes from its still-verified original file on retry.
		if err := persist(*journal); err != nil {
			if action.Kind == "pm2_file" || restore {
				temporaryValue, readErr := readStartupAt(parent, temporary, action.Kind)
				if readErr == nil && temporaryValue.Fingerprint == planned {
					_ = unix.Unlinkat(int(parent.Fd()), temporary, 0)
					_ = parent.Sync()
				}
			}
			return err
		}
	}
	if filepath.Dir(state.PendingPath) != filepath.Dir(action.Path) || !strings.HasPrefix(filepath.Base(state.PendingPath), ".jd-startup-") {
		return ErrHostWorkloadChanged
	}
	temporary := filepath.Base(state.PendingPath)
	current, err = readStartupAt(parent, name, action.Kind)
	if err != nil {
		return err
	}
	if current.Fingerprint == source {
		if action.Kind == "pm2_file" {
			prepared, err := readStartupAt(parent, temporary, action.Kind)
			if err == nil && prepared.Absent {
				data, rewriteErr := rewriteStartupRows(current.Data, action, restore)
				if rewriteErr != nil || startupContentFingerprint(data, action.UID, action.GID, action.Mode) != planned {
					return ErrHostWorkloadChanged
				}
				if createErr := createStartupTemporaryFile(parent, temporary, data, action); createErr != nil {
					return createErr
				}
				if syncErr := parent.Sync(); syncErr != nil {
					return syncErr
				}
				prepared, err = readStartupAt(parent, temporary, action.Kind)
			}
			if err != nil || prepared.Fingerprint != planned {
				return ErrHostWorkloadChanged
			}
			if err := unix.Renameat2(int(parent.Fd()), temporary, int(parent.Fd()), name, unix.RENAME_EXCHANGE); err != nil {
				return err
			}
		} else if restore {
			prepared, readErr := readStartupAt(parent, temporary, action.Kind)
			if readErr != nil {
				return readErr
			}
			if prepared.Absent {
				if err := unix.Symlinkat(action.Target, int(parent.Fd()), temporary); err != nil {
					return err
				}
				if err := unix.Fchownat(int(parent.Fd()), temporary, int(action.UID), int(action.GID), unix.AT_SYMLINK_NOFOLLOW); err != nil {
					return err
				}
				if err := parent.Sync(); err != nil {
					return err
				}
				prepared, readErr = readStartupAt(parent, temporary, action.Kind)
			}
			if readErr != nil || prepared.Fingerprint != planned {
				return ErrHostWorkloadChanged
			}
			if err := unix.Renameat2(int(parent.Fd()), temporary, int(parent.Fd()), name, unix.RENAME_NOREPLACE); err != nil {
				return err
			}
		} else {
			if err := unix.Renameat2(int(parent.Fd()), name, int(parent.Fd()), temporary, unix.RENAME_NOREPLACE); err != nil {
				return err
			}
		}
		if err := parent.Sync(); err != nil {
			return err
		}
	} else if current.Fingerprint != planned {
		return ErrHostWorkloadChanged
	}
	if action.Kind == "pm2_file" || !restore {
		displaced, err := readStartupAt(parent, temporary, action.Kind)
		if err != nil || displaced.Fingerprint != source {
			// Never discard a file that raced our comparison. Swap back only while
			// the published path still contains exactly our prepared replacement.
			published, publishErr := readStartupAt(parent, name, action.Kind)
			if publishErr == nil && published.Fingerprint == planned && err == nil && !displaced.Absent {
				if action.Kind == "pm2_file" {
					_ = unix.Renameat2(int(parent.Fd()), temporary, int(parent.Fd()), name, unix.RENAME_EXCHANGE)
				} else {
					_ = unix.Renameat2(int(parent.Fd()), temporary, int(parent.Fd()), name, unix.RENAME_NOREPLACE)
				}
				_ = parent.Sync()
			}
			return ErrHostWorkloadChanged
		}
	}
	published, err := readStartupAt(parent, name, action.Kind)
	if err != nil || published.Fingerprint != planned {
		return ErrHostWorkloadChanged
	}
	state.Phase = finished
	if err := persist(*journal); err != nil {
		return err
	}
	return cleanupStartupPending(parent, state, action.Kind)
}

func cleanupStartupPending(parent *os.File, state *NativeStartupActionJournal, kind string) error {
	if state.PendingPath == "" {
		return nil
	}
	if filepath.Dir(state.PendingPath) != parent.Name() || !strings.HasPrefix(filepath.Base(state.PendingPath), ".jd-startup-") {
		return ErrHostWorkloadChanged
	}
	current, err := readStartupAt(parent, filepath.Base(state.PendingPath), kind)
	if err != nil {
		return err
	}
	if current.Absent {
		return nil
	}
	expected := state.BeforeFingerprint
	if state.Phase == "restored" {
		expected = state.RetiredFingerprint
	}
	if current.Fingerprint != expected {
		return ErrHostWorkloadChanged
	}
	err = unix.Unlinkat(int(parent.Fd()), filepath.Base(state.PendingPath), 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	return parent.Sync()
}
func startupTemporaryName() (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	return ".jd-startup-" + hex.EncodeToString(nonce[:]), nil
}
func createStartupTemporaryFile(parent *os.File, name string, data []byte, action NativeStartupAction) error {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	failed := true
	defer func() {
		if failed {
			_ = unix.Unlinkat(int(parent.Fd()), name, 0)
		}
	}()
	if err := file.Chown(int(action.UID), int(action.GID)); err != nil {
		return err
	}
	if err := file.Chmod(os.FileMode(action.Mode)); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	failed = false
	return nil
}
func rewriteStartupRows(data []byte, action NativeStartupAction, restore bool) ([]byte, error) {
	rows, err := parseStartupRows(data)
	if err != nil {
		return nil, err
	}
	if restore {
		for _, row := range rows {
			match, err := startupRowMatches(row, action.Namespace, action.Name)
			if err != nil || match {
				return nil, ErrHostWorkloadChanged
			}
		}
		for _, entry := range action.Selected {
			if entry.Index < 0 || entry.Index > len(rows) {
				return nil, ErrHostWorkloadChanged
			}
			rows = append(rows, nil)
			copy(rows[entry.Index+1:], rows[entry.Index:])
			rows[entry.Index] = entry.Row
		}
	} else {
		selected := map[int]json.RawMessage{}
		for _, entry := range action.Selected {
			selected[entry.Index] = entry.Row
		}
		retained := make([]json.RawMessage, 0, len(rows))
		found := 0
		for index, row := range rows {
			match, err := startupRowMatches(row, action.Namespace, action.Name)
			if err != nil {
				return nil, err
			}
			if !match {
				retained = append(retained, row)
				continue
			}
			expected, ok := selected[index]
			if !ok || !equalStartupJSON(expected, row) {
				return nil, ErrHostWorkloadChanged
			}
			found++
		}
		if found != len(action.Selected) {
			return nil, ErrHostWorkloadChanged
		}
		rows = retained
	}
	result, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(result, '\n'), nil
}

func lockStartupParent(ctx context.Context, parent *os.File) error {
	for {
		err := unix.Flock(int(parent.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			return err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
