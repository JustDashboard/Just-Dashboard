package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
)

// NativeStartupStore retains only selected restart authority, encrypted with
// the installation key. The journal belongs to that authority rather than to
// a run: rollback and process restart must reconcile the same side effects.
type NativeStartupStore struct {
	root   string
	sealer *auth.Sealer
}

func NewNativeStartupStore(recoveryRoot string, sealer *auth.Sealer) *NativeStartupStore {
	return &NativeStartupStore{root: filepath.Join(recoveryRoot, "native-startup"), sealer: sealer}
}

func (a *HostSourceAnalyzer) WithNativeStartupStore(store *NativeStartupStore) *HostSourceAnalyzer {
	a.startup = store
	return a
}

func (s *NativeStartupStore) directory(digest string) (string, error) {
	if s == nil || s.sealer == nil || !filepath.IsAbs(s.root) || len(digest) != 64 || strings.Trim(digest, "0123456789abcdef") != "" {
		return "", fmt.Errorf("the private startup authority is unavailable")
	}
	if err := makePrivateDirectory(s.root); err != nil {
		return "", err
	}
	path := filepath.Join(s.root, digest)
	if err := makePrivateDirectory(path); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return "", ErrInvalidSource
	}
	return path, nil
}

func (s *NativeStartupStore) Stage(ctx context.Context, plan *procs.NativeStartupPlan) error {
	if plan == nil {
		return fmt.Errorf("the verified startup handoff is unavailable")
	}
	return s.withDirectoryLock(ctx, plan.Digest, func(directory *os.Root) error {
		var saved procs.NativeStartupPlan
		err := s.read(directory, "plan.enc", &saved)
		if err == nil {
			if string(mustJSON(saved)) != string(mustJSON(plan)) {
				return procs.ErrHostWorkloadChanged
			}
			var journal procs.NativeStartupJournal
			if err := s.read(directory, "journal.enc", &journal); err != nil {
				return err
			}
			return procs.VerifyStartupHandoff(ctx, &saved, journal)
		}
		if !os.IsNotExist(err) {
			return err
		}
		journal := procs.NativeStartupJournal{Version: 1, PlanDigest: plan.Digest, Phase: "prepared", Actions: make([]procs.NativeStartupActionJournal, len(plan.Actions))}
		for i, action := range plan.Actions {
			journal.Actions[i] = procs.NativeStartupActionJournal{Phase: "prepared", BeforeFingerprint: action.Fingerprint}
		}
		if err := procs.VerifyStartupHandoff(ctx, plan, journal); err != nil {
			return err
		}
		if err := s.write(directory, "journal.enc", journal); err != nil {
			return err
		}
		return s.write(directory, "plan.enc", plan)
	})
}

func (s *NativeStartupStore) withPlan(ctx context.Context, digest string, apply func(*procs.NativeStartupPlan, *procs.NativeStartupJournal, func(procs.NativeStartupJournal) error) error) error {
	return s.withDirectoryLock(ctx, digest, func(directory *os.Root) error {
		var plan procs.NativeStartupPlan
		var journal procs.NativeStartupJournal
		if err := s.read(directory, "plan.enc", &plan); err != nil {
			return fmt.Errorf("the retained startup handoff is unavailable")
		}
		if plan.Digest != digest {
			return procs.ErrHostWorkloadChanged
		}
		if err := s.read(directory, "journal.enc", &journal); err != nil {
			return fmt.Errorf("the durable startup handoff journal is unavailable")
		}
		return apply(&plan, &journal, func(next procs.NativeStartupJournal) error {
			return s.write(directory, "journal.enc", next)
		})
	})
}

func (s *NativeStartupStore) VerifyCapture(ctx context.Context, digest string, capture *procs.HostWorkloadCapture) (*procs.NativeStartupPlan, error) {
	var retained *procs.NativeStartupPlan
	err := s.withPlan(ctx, digest, func(plan *procs.NativeStartupPlan, journal *procs.NativeStartupJournal, _ func(procs.NativeStartupJournal) error) error {
		if err := procs.VerifyCapturedStartup(capture, plan, *journal); err != nil {
			return err
		}
		retained = plan
		return nil
	})
	return retained, err
}

func (s *NativeStartupStore) transition(ctx context.Context, digest string, restore bool) error {
	return s.withPlan(ctx, digest, func(plan *procs.NativeStartupPlan, journal *procs.NativeStartupJournal, persist func(procs.NativeStartupJournal) error) error {
		if restore {
			return procs.RestoreStartupHandoff(ctx, plan, journal, persist)
		}
		return procs.RetireStartupHandoff(ctx, plan, journal, persist)
	})
}

func (s *NativeStartupStore) withDirectoryLock(ctx context.Context, digest string, apply func(*os.Root) error) error {
	path, err := s.directory(digest)
	if err != nil {
		return err
	}
	directory, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	lock, err := directory.OpenFile("authority.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
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

func (s *NativeStartupStore) read(directory *os.Root, name string, value any) error {
	file, err := directory.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return fmt.Errorf("the private startup artifact is invalid")
	}
	raw, err := io.ReadAll(io.LimitReader(file, (16<<20)+1))
	if err != nil {
		return err
	}
	plain, err := s.sealer.Open(string(raw))
	if err != nil || json.Unmarshal([]byte(plain), value) != nil {
		return fmt.Errorf("the private startup artifact cannot be opened")
	}
	return nil
}

func (s *NativeStartupStore) write(directory *os.Root, name string, value any) error {
	plain, err := json.Marshal(value)
	if err != nil {
		return err
	}
	sealed, err := s.sealer.Seal(string(plain))
	if err != nil {
		return err
	}
	temporary := ".journal-" + auth.RandomToken(18)
	file, err := directory.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer directory.Remove(temporary)
	_, writeErr := file.WriteString(sealed)
	err = errors.Join(writeErr, file.Sync(), file.Close())
	if err != nil {
		return err
	}
	if err := directory.Rename(temporary, name); err != nil {
		return err
	}
	parent, err := directory.Open(".")
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}

func removeHandledStartupBlockers(capture *procs.HostWorkloadCapture, plan *procs.NativeStartupPlan) {
	handled := map[string]bool{}
	for _, message := range plan.HandledBlockers {
		handled[message] = true
	}
	remaining := capture.Blockers[:0]
	for _, message := range capture.Blockers {
		if !handled[message] {
			remaining = append(remaining, message)
		}
	}
	capture.Blockers = remaining
}
