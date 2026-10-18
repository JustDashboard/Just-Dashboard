package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

var ErrConfigChanged = errors.New("the configuration file changed after the editor read it")

type ConfigSnapshot struct {
	Content  string `json:"content"`
	Revision string `json:"revision"`
}

// A read waits out nginx's temporary validation candidate, so its revision
// always names committed content rather than another operator's dry run.
func (s *Service) ReadConfigSnapshot(path string) (*ConfigSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	full, err := s.allowedPath(path)
	if err != nil {
		return nil, err
	}
	if s.isPasswordFile(full) {
		return nil, fmt.Errorf("%w: %s", ErrProtectedFile, path)
	}
	target, err := s.openConfigTarget(full)
	if err != nil {
		return nil, err
	}
	defer target.parent.Close()
	content, existed, err := target.read()
	if err != nil {
		return nil, err
	}
	if !existed {
		return nil, &os.PathError{Op: "open", Path: full, Err: syscall.ENOENT}
	}
	if err := target.location(); err != nil {
		return nil, err
	}
	return &ConfigSnapshot{Content: content, Revision: ContentDigest(content)}, nil
}

// WriteConfigChecked keeps the editor's precondition under the same writer
// lock as every proxy operation. Native edits during validation are also
// refused, including when nginx's candidate must temporarily occupy the file.
func (s *Service) WriteConfigChecked(ctx context.Context, kind Kind, path, content, expected string) (*ValidationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	full, err := s.allowedPath(path)
	if err != nil {
		return nil, err
	}
	if expected == "" {
		return nil, ErrConfigChanged
	}
	target, err := s.openConfigTarget(full)
	if err != nil {
		return nil, err
	}
	defer target.parent.Close()
	original, err := target.check(expected)
	if err != nil {
		return nil, err
	}
	mode := target.mode()
	var res *ValidationResult
	if kind == KindCaddy {
		res, err = s.validateCaddy(ctx, full, content)
		if err != nil {
			return nil, err
		}
		if !res.Valid {
			return res, ErrInvalidConf
		}
		if err := target.write(content, mode, &expected, true); err != nil {
			return nil, err
		}
	} else {
		s.keepLoaded(full)
		if err := target.write(content, mode, &expected, true); err != nil {
			return nil, err
		}
		started := time.Now()
		res, err = runTest(ctx, "nginx", "-t")
		candidate := ContentDigest(content)
		if _, changed := target.check(candidate); changed != nil {
			// A native writer replaced the staged candidate: rolling back
			// would erase their edit just as surely as a stale save would.
			return nil, changed
		}
		if locationErr := target.location(); locationErr != nil {
			if restoreErr := target.restore(original, true, mode, candidate); restoreErr != nil {
				return nil, restoreErr
			}
			return nil, locationErr
		}
		if err != nil || !res.Valid {
			if restoreErr := target.restore(original, true, mode, candidate); restoreErr != nil {
				return nil, restoreErr
			}
			if err != nil {
				return nil, err
			}
			return res, ErrInvalidConf
		}
		res.Note = s.includeNote(full)
		s.remember(KindNginx, started, res)
	}
	s.recordChange(ctx, Change{Path: full, Action: ChangeWrite,
		Before: []byte(original), BeforeExisted: true, After: []byte(content)})
	return res, nil
}
