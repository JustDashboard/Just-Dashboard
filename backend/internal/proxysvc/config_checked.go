package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	content, err := s.ReadConfig(path)
	if err != nil {
		return nil, err
	}
	return &ConfigSnapshot{Content: content, Revision: ContentDigest(content)}, nil
}

func checkConfigRevision(path, expected string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%w: the file was removed", ErrConfigChanged)
	}
	if err != nil {
		return "", err
	}
	if expected == "" || ContentDigest(string(b)) != expected {
		return "", ErrConfigChanged
	}
	return string(b), nil
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
	original, err := checkConfigRevision(full, expected)
	if err != nil {
		return nil, err
	}
	var res *ValidationResult
	if kind == KindCaddy {
		res, err = s.validateCaddy(ctx, full, content)
		if err != nil {
			return nil, err
		}
		if !res.Valid {
			return res, ErrInvalidConf
		}
		if err := writeAtomicChecked(full, content, expected); err != nil {
			return nil, err
		}
	} else {
		s.keepLoaded(full)
		if err := writeAtomicChecked(full, content, expected); err != nil {
			return nil, err
		}
		started := time.Now()
		res, err = runTest(ctx, "nginx", "-t")
		candidate := ContentDigest(content)
		if _, changed := checkConfigRevision(full, candidate); changed != nil {
			// A native writer replaced the staged candidate: rolling back
			// would erase their edit just as surely as a stale save would.
			return nil, changed
		}
		if err != nil || !res.Valid {
			if restoreErr := writeAtomicChecked(full, original, candidate); restoreErr != nil {
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
