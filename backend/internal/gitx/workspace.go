package gitx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Worktree struct {
	Path       string `json:"path"`
	Head       string `json:"head"`
	Branch     string `json:"branch"`
	Current    bool   `json:"current"`
	Main       bool   `json:"main"`
	Locked     bool   `json:"locked"`
	Prunable   bool   `json:"prunable"`
	Accessible bool   `json:"accessible"`
}

func (s *Service) Worktrees(ctx context.Context, path string) ([]Worktree, error) {
	out, err := s.run(ctx, path, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	list := []Worktree{}
	var current *Worktree
	for _, field := range strings.Split(out, "\x00") {
		key, value, _ := strings.Cut(field, " ")
		if key == "worktree" {
			list = append(list, Worktree{Path: value, Current: value == path, Main: len(list) == 0})
			current = &list[len(list)-1]
			_, err := s.Resolve(value)
			current.Accessible = err == nil
		} else if current != nil {
			switch key {
			case "HEAD":
				current.Head = value
			case "branch":
				current.Branch = strings.TrimPrefix(value, "refs/heads/")
			case "locked":
				current.Locked = true
			case "prunable":
				current.Prunable = true
			}
		}
	}
	return list, nil
}

func (s *Service) AddWorktree(ctx context.Context, path, parent, name, branch, from string, create bool) (*Result, error) {
	dir, err := s.ResolveDir(parent)
	if err != nil {
		return nil, err
	}
	if err := validateDirName(name); err != nil {
		return nil, err
	}
	if err := ValidateRef(branch); err != nil {
		return nil, err
	}
	target := filepath.Join(dir, name)
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: destination must not exist", ErrInvalidRef)
	}
	args := []string{"worktree", "add"}
	if create {
		if from == "" {
			from = "HEAD"
		}
		sha, err := s.commitID(ctx, path, from)
		if err != nil {
			return nil, err
		}
		args = append(args, "-b", branch, "--", target, sha)
	} else {
		args = append(args, "--", target, branch)
	}
	return s.op(ctx, path, time.Minute, args...)
}

// Removal deliberately has no force mode: Git refuses a dirty or locked tree.
// Both the caller's repository and the target are checked against the Git roots.
func (s *Service) RemoveWorktree(ctx context.Context, path, target string) (*Result, error) {
	real, err := s.Resolve(target)
	if err != nil {
		return nil, err
	}
	list, err := s.Worktrees(ctx, path)
	if err != nil {
		return nil, err
	}
	for _, tree := range list {
		if tree.Path != real {
			continue
		}
		if tree.Current || tree.Main || tree.Locked {
			return nil, fmt.Errorf("%w: cannot remove the main, current or locked worktree", ErrInvalidRef)
		}
		return s.op(ctx, path, time.Minute, "worktree", "remove", "--", real)
	}
	return nil, fmt.Errorf("%w: worktree is not attached to this repository", ErrInvalidRef)
}

func (s *Service) Recover(ctx context.Context, path, name, ref string) (*Result, error) {
	if err := ValidateRef(name); err != nil {
		return nil, err
	}
	sha, err := s.commitID(ctx, path, ref)
	if err != nil {
		return nil, err
	}
	return s.op(ctx, path, time.Minute, "branch", "--", name, sha)
}

func (s *Service) SetRemoteURL(ctx context.Context, path, name, url string) (*Result, error) {
	if err := validateRemoteName(name); err != nil {
		return nil, err
	}
	if err := ValidateRemoteURL(url); err != nil {
		return nil, err
	}
	if strings.Contains(url, "***@") {
		return nil, fmt.Errorf("%w: enter the complete remote URL", ErrInvalidRef)
	}
	return s.op(ctx, path, time.Minute, "remote", "set-url", "--", name, url)
}

func (s *Service) SetUpstream(ctx context.Context, path, branch, upstream string) (*Result, error) {
	if err := ValidateRef(branch); err != nil {
		return nil, err
	}
	if upstream == "" {
		return s.op(ctx, path, time.Minute, "branch", "--unset-upstream", "--", branch)
	}
	if err := ValidateRef(upstream); err != nil {
		return nil, err
	}
	return s.op(ctx, path, time.Minute, "branch", "--set-upstream-to="+upstream, "--", branch)
}
