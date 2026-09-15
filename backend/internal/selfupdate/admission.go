package selfupdate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// LockLifecycle serializes admission before any settings, transcript or run
// record is replaced. Both restart and update share this lock and state check.
// The sibling owns its durable run after admission, so this lock need not be
// inherited across the dashboard's own replacement.
func LockLifecycle(dataDir string) (func(), error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	fd, err := unix.Open(filepath.Join(dataDir, "lifecycle.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		unix.Close(fd)
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrInProgress
		}
		return nil, err
	}
	return func() { unix.Flock(fd, unix.LOCK_UN); unix.Close(fd) }, nil
}

// CheckLifecycleIdle fails closed for unreadable records. A broken record can
// be dismissed explicitly, but must not be mistaken for an idle stack.
func CheckLifecycleIdle(dataDir string) error {
	for _, name := range []string{StateFile, "self-config.json"} {
		body, err := os.ReadFile(filepath.Join(dataDir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		var state struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(body, &state); err != nil {
			return fmt.Errorf("read lifecycle state %s: %w", name, err)
		}
		switch state.Status {
		case "pending", "running":
			return ErrInProgress
		case "success", "failed", "rolled_back":
		default:
			return fmt.Errorf("unknown lifecycle state in %s", name)
		}
	}
	return nil
}
