package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ErrProtected is a checkout the dashboard will not delete: what it is (a
// root, the dashboard's own install, a repository other checkouts still
// depend on) makes deleting it a different act from removing one project.
var ErrProtected = errors.New("this checkout cannot be deleted from here")

// RemovalWorktree is a linked worktree that would be orphaned by deleting
// its main checkout.
type RemovalWorktree struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Dirty  bool   `json:"dirty"`
}

// RemovalBranch is a local branch holding commits no remote has.
type RemovalBranch struct {
	Name     string `json:"name"`
	Unpushed int    `json:"unpushed"`
	Upstream string `json:"upstream,omitempty"`
}

// Removal is what deleting a checkout from this server would lose: the work
// that exists nowhere else, counted, so the confirmation can say it before
// anything is gone.
type Removal struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Worktree bool   `json:"worktree"`
	Main     string `json:"main,omitempty"`
	// Worktrees are the linked worktrees of a main checkout. Deleting it
	// would leave them pointing at a repository that no longer exists, so
	// they have to go first (Protected says so).
	Worktrees []RemovalWorktree `json:"worktrees"`
	// Nested are other checkouts inside this one's directory — an
	// independent clone, or another repository's worktree put here — which
	// deleting the directory would take with it.
	Nested    []string `json:"nested"`
	Changes   int      `json:"changes"`
	Untracked int      `json:"untracked"`
	Conflicts int      `json:"conflicts"`
	Stashes   int      `json:"stashes"`
	// Unpushed counts the commits on local branches that no remote-tracking
	// ref reaches — with no remote at all, every commit. Zero for a linked
	// worktree, along with Stashes and LocalBranches: those live in the
	// repository the worktree belongs to, and survive it.
	Unpushed      int             `json:"unpushed"`
	LocalBranches []RemovalBranch `json:"localBranches"`
	Remotes       int             `json:"remotes"`
	// Protected is why the delete would be refused, in a sentence; empty when
	// it would go ahead.
	Protected string `json:"protected,omitempty"`
}

// maxRemovalBranches bounds the per-branch counts; past it the total in
// Unpushed still covers every branch.
const maxRemovalBranches = 50

// Removal reads what deleting a checkout would lose. installDir is the
// checkout this dashboard was installed from (config UpdateDir), which is
// never deleted from here.
func (s *Service) Removal(ctx context.Context, path, installDir string) (*Removal, error) {
	real, err := s.Resolve(path)
	if err != nil {
		return nil, err
	}
	repo, err := s.Summary(ctx, real)
	if err != nil {
		return nil, err
	}
	rm := &Removal{
		Path: real, Name: filepath.Base(real), Worktree: repo.Worktree, Main: repo.Main,
		Worktrees: []RemovalWorktree{}, Nested: []string{}, LocalBranches: []RemovalBranch{},
		Changes: repo.Changes, Untracked: repo.Untracked, Conflicts: repo.Conflicts,
	}
	if out, err := s.run(ctx, real, "remote"); err == nil {
		rm.Remotes = len(nonEmptyLines(out))
	}
	locked := false
	if repo.Worktree {
		// A worktree's branches and stashes belong to its repository and
		// outlive it: removing one loses only what is uncommitted in it.
		locked = s.worktreeLocked(ctx, real, repo.Main)
	} else {
		if out, err := s.run(ctx, real, "stash", "list"); err == nil {
			rm.Stashes = len(nonEmptyLines(out))
		}
		if out, err := s.run(ctx, real, "rev-list", "--count", "--branches", "--not", "--remotes"); err == nil {
			rm.Unpushed, _ = strconv.Atoi(strings.TrimSpace(out))
		}
		if rm.Unpushed > 0 {
			rm.LocalBranches = s.unpushedBranches(ctx, real)
		}
		rm.Worktrees = s.removalWorktrees(ctx, real)
	}
	rm.Nested = nestedCheckouts(real)
	rm.Protected = s.protection(real, installDir, rm, locked)
	return rm, nil
}

func (s *Service) unpushedBranches(ctx context.Context, path string) []RemovalBranch {
	out, err := s.run(ctx, path, "for-each-ref", "--format=%(refname:short)%1f%(upstream:short)", "refs/heads")
	if err != nil {
		return []RemovalBranch{}
	}
	branches := []RemovalBranch{}
	for i, line := range nonEmptyLines(out) {
		if i >= 4*maxRemovalBranches || len(branches) == maxRemovalBranches {
			break
		}
		name, upstream, _ := strings.Cut(line, "\x1f")
		count, err := s.run(ctx, path, "rev-list", "--count", "refs/heads/"+name, "--not", "--remotes")
		if err != nil {
			continue
		}
		if n, _ := strconv.Atoi(strings.TrimSpace(count)); n > 0 {
			branches = append(branches, RemovalBranch{Name: name, Unpushed: n, Upstream: upstream})
		}
	}
	return branches
}

func (s *Service) removalWorktrees(ctx context.Context, path string) []RemovalWorktree {
	list, err := s.Worktrees(ctx, path)
	if err != nil {
		return []RemovalWorktree{}
	}
	out := []RemovalWorktree{}
	for _, tree := range list {
		if tree.Main || tree.Prunable {
			continue
		}
		row := RemovalWorktree{Path: tree.Path, Branch: tree.Branch}
		if row.Branch == "" && len(tree.Head) >= 7 {
			row.Branch = "detached at " + tree.Head[:7]
		}
		if st, err := s.run(ctx, tree.Path, "status", "--porcelain=v1", "-z", "--untracked-files=normal"); err == nil {
			row.Dirty = len(parseStatus(st)) > 0
		}
		out = append(out, row)
	}
	return out
}

// worktreeLocked asks the repository a linked worktree belongs to whether
// it is locked: `git worktree remove --force` refuses one, and so does this.
func (s *Service) worktreeLocked(ctx context.Context, path, main string) bool {
	dir := main
	if dir == "" {
		dir = path
	}
	list, err := s.Worktrees(ctx, dir)
	if err != nil {
		return false
	}
	for _, tree := range list {
		if sameDir(tree.Path, path) {
			return tree.Locked
		}
	}
	return false
}

// nestedCheckouts finds other checkouts inside a directory: deleting it
// deletes them. A submodule (its git directory inside this repository's
// own) and this repository's own linked worktrees are part of it rather than
// separate work, and the second are reported as worktrees instead.
func nestedCheckouts(root string) []string {
	own := filepath.Join(root, ".git") + string(os.PathSeparator)
	found := []string{}
	rootDepth := strings.Count(root, string(os.PathSeparator))
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || len(found) >= 20 {
			return nil //nolint:nilerr // an unreadable subtree is skipped
		}
		if !d.IsDir() {
			return nil
		}
		if path == root {
			return nil
		}
		base := d.Name()
		if base == ".git" || base == "node_modules" ||
			strings.Count(path, string(os.PathSeparator))-rootDepth > 6 {
			return filepath.SkipDir
		}
		fi, err := os.Lstat(filepath.Join(path, ".git"))
		if err != nil {
			return nil
		}
		if fi.Mode().IsRegular() {
			if target := gitFileTarget(path); target != "" && strings.HasPrefix(target+string(os.PathSeparator), own) {
				return nil
			}
		}
		found = append(found, path)
		return filepath.SkipDir
	})
	return found
}

// protection is why a checkout may not be deleted, or "".
func (s *Service) protection(real, installDir string, rm *Removal, locked bool) string {
	for _, root := range s.roots {
		if sameDir(root, real) {
			return "It is one of the configured git roots (JD_GIT_ROOTS), not a checkout inside one."
		}
	}
	if installDir != "" {
		install := installDir
		if resolved, err := filepath.EvalSymlinks(installDir); err == nil {
			install = resolved
		}
		if sameDir(install, real) || strings.HasPrefix(install, real+string(os.PathSeparator)) {
			return "This dashboard was installed from " + install + " and updates itself from it."
		}
	}
	if locked {
		return "The worktree is locked. Unlock it with git worktree unlock first."
	}
	if len(rm.Worktrees) > 0 {
		paths := make([]string, 0, len(rm.Worktrees))
		for _, tree := range rm.Worktrees {
			paths = append(paths, tree.Path)
		}
		return "Its linked worktrees would be left without a repository. Remove them first: " +
			strings.Join(paths, ", ") + "."
	}
	if len(rm.Nested) > 0 {
		return "Other checkouts are inside it and would be deleted with it: " +
			strings.Join(rm.Nested, ", ") + "."
	}
	return ""
}

// DeleteRepository removes a checkout from this server: a linked worktree
// through its repository (`git worktree remove --force`, then a prune, so no
// record of it is left behind), anything else by deleting its directory.
//
// It refuses what Removal marks protected, re-read here rather than trusted
// from the preview, since the tree can change between the two.
func (s *Service) DeleteRepository(ctx context.Context, path, installDir string) (*Result, error) {
	rm, err := s.Removal(ctx, path, installDir)
	if err != nil {
		return nil, err
	}
	if rm.Protected != "" {
		return nil, fmt.Errorf("%w: %s", ErrProtected, rm.Protected)
	}
	if rm.Worktree {
		_, _, common := linkedWorktree(rm.Path)
		dir := rm.Main
		if dir == "" {
			dir = common
		}
		res, err := s.op(ctx, dir, 2*time.Minute, "worktree", "remove", "--force", "--", rm.Path)
		if err != nil {
			return res, err
		}
		_, _ = s.run(ctx, dir, "worktree", "prune")
		return res, nil
	}
	if err := os.RemoveAll(rm.Path); err != nil {
		return nil, fmt.Errorf("delete %s: %w", rm.Path, err)
	}
	return &Result{Command: "rm -rf " + rm.Path, Output: "Deleted " + rm.Path + ".", OK: true}, nil
}

func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}
