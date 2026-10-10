package gitx

import (
	"os"
	"path/filepath"
	"strings"
)

// linkedWorktree reads, from the files git keeps rather than from a git
// process, whether a checkout is a linked worktree and which checkout it
// belongs to.
//
// A linked worktree and a submodule checkout both carry a `.git` *file*
// naming a git directory somewhere else, so the file alone does not tell them
// apart. What does is `commondir`: git writes it into every
// `<common>/worktrees/<name>` directory and into nothing under
// `.git/modules`, because a worktree shares its repository's objects and refs
// and a submodule has its own. The main checkout is the directory holding the
// common `.git`; a worktree of a bare repository has none, and reports only
// that it is linked.
func linkedWorktree(path string) (linked bool, main string, common string) {
	gitdir := gitFileTarget(path)
	if gitdir == "" {
		return false, "", ""
	}
	raw, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return false, "", ""
	}
	common = strings.TrimSpace(string(raw))
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitdir, common)
	}
	common = filepath.Clean(common)
	if filepath.Base(common) == ".git" {
		main = filepath.Dir(common)
	}
	return true, main, common
}

// gitFileTarget is the git directory a `.git` file points at, absolute, or
// empty when `.git` is a directory, missing, or not in the `gitdir:` form.
func gitFileTarget(path string) string {
	fi, err := os.Lstat(filepath.Join(path, ".git"))
	if err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(path, ".git"))
	if err != nil {
		return ""
	}
	target, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir:")
	if !ok {
		return ""
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(path, target)
	}
	return filepath.Clean(target)
}

// attachedWorktrees lists the linked worktrees a main checkout's repository
// records, by reading `.git/worktrees/*/gitdir` — each holds the path of the
// worktree's own `.git` file. One whose checkout is gone (what `git worktree
// prune` would clear) is left out: there is nothing there to open or lose.
//
// The walk in Discover skips hidden directories, which is exactly where
// worktrees are usually put — `.worktrees/<task>`, `.claude/worktrees/<task>`
// — so this is how those are found at all.
func attachedWorktrees(repo string) []string {
	dir := filepath.Join(repo, ".git", "worktrees")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := []string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name(), "gitdir"))
		if err != nil {
			continue
		}
		target := strings.TrimSpace(string(raw))
		if target == "" {
			continue
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(dir, entry.Name(), target)
		}
		checkout := filepath.Dir(filepath.Clean(target))
		if fi, err := os.Stat(filepath.Join(checkout, ".git")); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		out = append(out, checkout)
	}
	return out
}
