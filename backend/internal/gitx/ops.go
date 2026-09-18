package gitx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Result is what an operation reports back: the command that ran and what git
// said about it. The output is shown verbatim, because git's own messages are
// better than anything this layer could paraphrase.
type Result struct {
	Command string `json:"command"`
	Output  string `json:"output"`
	OK      bool   `json:"ok"`
}

func (s *Service) op(ctx context.Context, path string, timeout time.Duration, args ...string) (*Result, error) {
	out, err := s.runTimeout(ctx, path, timeout, args...)
	res := &Result{Command: "git " + strings.Join(args, " "), Output: strings.TrimSpace(out), OK: err == nil}
	if err != nil {
		return res, err
	}
	if res.Output == "" {
		res.Output = "Done."
	}
	return res, nil
}

// Fetch updates remote-tracking refs without touching the working tree. It is
// the one network operation that cannot lose anything, which is why it is not
// gated behind a confirmation.
func (s *Service) Fetch(ctx context.Context, path string, prune bool) (*Result, error) {
	args := []string{"fetch", "--all", "--tags"}
	if prune {
		args = append(args, "--prune")
	}
	return s.op(ctx, path, 3*time.Minute, args...)
}

// Pull refuses anything but a fast-forward.
//
// A merge or rebase here could stop halfway and leave conflict markers in a
// working tree the operator cannot easily fix from a web page. Refusing is a
// clear failure they can resolve deliberately; a half-finished merge is not.
func (s *Service) Pull(ctx context.Context, path string) (*Result, error) {
	return s.op(ctx, path, 3*time.Minute, "pull", "--ff-only")
}

// Push sends the current branch to its upstream. No force, ever: this runs
// unattended behind a web request, which is the worst possible place to
// discard someone else's commits.
//
// A branch with no upstream is published rather than refused. Plain `git push`
// answers that case with "the current branch has no upstream branch" and a
// command to copy — which is fine in a terminal and useless here, where the
// operator has just made a branch in this very page and the only thing they
// can do about it is open a shell. Setting the upstream is what they would
// have typed, and it is what makes a pull request possible at all: gh needs
// the branch to exist on the remote before it can open one against it.
func (s *Service) Push(ctx context.Context, path string) (*Result, error) {
	if s.hasUpstream(ctx, path) {
		return s.op(ctx, path, 3*time.Minute, "push")
	}
	branch, err := s.CurrentBranch(ctx, path)
	if err != nil {
		// Detached, or no commits yet. Let plain push produce git's own
		// diagnosis rather than inventing one.
		return s.op(ctx, path, 3*time.Minute, "push")
	}
	return s.op(ctx, path, 3*time.Minute, "push", "--set-upstream", s.remoteFor(ctx, path, branch), branch)
}

// PushTags publishes one tag, or every tag, to the current branch's remote.
// A tag is local until it is pushed — the same surprise as an unpushed
// commit, discovered later and further from the cause.
func (s *Service) PushTags(ctx context.Context, path, name string) (*Result, error) {
	remote := "origin"
	if branch, err := s.CurrentBranch(ctx, path); err == nil {
		remote = s.remoteFor(ctx, path, branch)
	}
	if name == "" {
		return s.op(ctx, path, 3*time.Minute, "push", remote, "--tags")
	}
	if err := ValidateRef(name); err != nil {
		return nil, err
	}
	return s.op(ctx, path, 3*time.Minute, "push", remote, "refs/tags/"+name)
}

// CurrentBranch is the branch HEAD is on, and an error when it is on none.
func (s *Service) CurrentBranch(ctx context.Context, path string) (string, error) {
	out, err := s.run(ctx, path, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		return "", fmt.Errorf("%w: HEAD is not on a branch", ErrInvalidRef)
	}
	branch := strings.TrimSpace(out)
	if err := ValidateRef(branch); err != nil {
		return "", err
	}
	return branch, nil
}

func (s *Service) hasUpstream(ctx context.Context, path string) bool {
	_, err := s.run(ctx, path, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	return err == nil
}

// remoteFor is the remote a branch is configured against, and origin when it
// has none — which is what git itself assumes.
func (s *Service) remoteFor(ctx context.Context, path, branch string) string {
	out, err := s.run(ctx, path, "config", "--get", "branch."+branch+".remote")
	if remote := strings.TrimSpace(out); err == nil && remote != "" && ValidateRef(remote) == nil {
		return remote
	}
	return "origin"
}

// Checkout switches branches. It does not use --force, so git refuses when the
// switch would overwrite local modifications, and the operator is told rather
// than silently losing them.
func (s *Service) Checkout(ctx context.Context, path, ref string) (*Result, error) {
	if err := ValidateRef(ref); err != nil {
		return nil, err
	}
	return s.op(ctx, path, time.Minute, "checkout", ref, "--")
}

// CheckoutRemote makes a local branch that tracks a remote one and switches
// to it. Checking a remote branch out directly lands in a detached HEAD —
// the state a newcomer cannot get out of — so the page never offers that;
// this is the move it offers instead, and it is what `git checkout feature`
// does by itself when only origin/feature exists.
func (s *Service) CheckoutRemote(ctx context.Context, path, remoteRef, local string) (*Result, error) {
	if err := ValidateRef(remoteRef); err != nil {
		return nil, err
	}
	if err := ValidateRef(local); err != nil {
		return nil, err
	}
	return s.op(ctx, path, time.Minute, "checkout", "-b", local, "--track", remoteRef, "--")
}

// CreateBranch branches from `from` — the current HEAD when empty — and
// switches to it. A start point that is a remote-tracking branch becomes the
// new branch's upstream, which is git's own default and the one that makes
// the first push go where the operator expects.
func (s *Service) CreateBranch(ctx context.Context, path, name, from string) (*Result, error) {
	if err := ValidateRef(name); err != nil {
		return nil, err
	}
	args := []string{"checkout", "-b", name}
	if from != "" {
		if err := ValidateRef(from); err != nil {
			return nil, err
		}
		args = append(args, from)
	}
	return s.op(ctx, path, time.Minute, append(args, "--")...)
}

// RenameBranch renames a local branch, keeping its upstream and its reflog.
// Without -M git refuses to overwrite an existing name, which is the refusal
// wanted here.
func (s *Service) RenameBranch(ctx context.Context, path, name, to string) (*Result, error) {
	if err := ValidateRef(name); err != nil {
		return nil, err
	}
	if err := ValidateRef(to); err != nil {
		return nil, err
	}
	return s.op(ctx, path, time.Minute, "branch", "-m", name, to)
}

// DeleteBranch removes a local branch. Without force git refuses to delete one
// whose commits are not merged anywhere — the safety a non-expert most wants —
// and force (-D) overrides that check, which can strand commits. The `--`
// ends the options so the name can never be read as one, on top of
// ValidateRef already refusing a leading dash.
func (s *Service) DeleteBranch(ctx context.Context, path, name string, force bool) (*Result, error) {
	if err := ValidateRef(name); err != nil {
		return nil, err
	}
	flag := "-d"
	if force {
		flag = "-D"
	}
	return s.op(ctx, path, time.Minute, "branch", flag, "--", name)
}

// DeleteRemoteBranch removes a branch from the remote: the cleanup after a
// pull request merges, which otherwise leaves every remote growing a list of
// finished work.
func (s *Service) DeleteRemoteBranch(ctx context.Context, path, remote, name string) (*Result, error) {
	if err := validateRemoteName(remote); err != nil {
		return nil, err
	}
	if err := ValidateRef(name); err != nil {
		return nil, err
	}
	return s.op(ctx, path, 3*time.Minute, "push", remote, "--delete", "refs/heads/"+name)
}

// Merge brings another branch into the current one. A merge that would stop
// on conflicts is abandoned rather than left half-finished: the tree is put
// back exactly as it was, and git's own message says which files clashed, so
// the operator can decide in a terminal where the conflict can actually be
// resolved. --no-edit keeps the default message; the editor is a no-op
// regardless (see execute).
func (s *Service) Merge(ctx context.Context, path, ref string) (*Result, error) {
	if err := ValidateRef(ref); err != nil {
		return nil, err
	}
	return s.abortOnFailure(ctx, path, "merge", "merge", "--no-edit", ref)
}

// Revert records a new commit that undoes one — the safe undo for a commit
// that has already been pushed, since it rewrites nothing. Conflicts abandon
// the revert the way Merge does.
func (s *Service) Revert(ctx context.Context, path, ref string) (*Result, error) {
	if err := ValidateRef(ref); err != nil {
		return nil, err
	}
	return s.abortOnFailure(ctx, path, "revert", "revert", "--no-edit", ref)
}

// CherryPick copies one commit onto the current branch.
func (s *Service) CherryPick(ctx context.Context, path, ref string) (*Result, error) {
	if err := ValidateRef(ref); err != nil {
		return nil, err
	}
	return s.abortOnFailure(ctx, path, "cherry-pick", "cherry-pick", ref)
}

// abortOnFailure runs a merge-like command and, when it fails, runs the
// matching --abort so the working tree never stays in a state this page has
// no controls for. The original failure is what is reported.
func (s *Service) abortOnFailure(ctx context.Context, path, verb string, args ...string) (*Result, error) {
	res, err := s.op(ctx, path, 2*time.Minute, args...)
	if err != nil {
		_, _ = s.run(ctx, path, verb, "--abort")
	}
	return res, err
}

// Stash puts local modifications aside, including untracked files, so the
// operator can switch branches without losing work.
func (s *Service) Stash(ctx context.Context, path, message string) (*Result, error) {
	args := []string{"stash", "push", "--include-untracked"}
	if m := strings.TrimSpace(message); m != "" {
		if len(m) > 200 {
			m = m[:200]
		}
		if strings.HasPrefix(m, "-") {
			return nil, fmt.Errorf("%w: message may not start with a dash", ErrInvalidRef)
		}
		args = append(args, "-m", m)
	}
	return s.op(ctx, path, time.Minute, args...)
}

// StashPop restores the most recent stash.
func (s *Service) StashPop(ctx context.Context, path string) (*Result, error) {
	return s.op(ctx, path, time.Minute, "stash", "pop")
}

// StashApply brings one stash back into the working tree; pop also drops it
// from the list once it has applied cleanly.
func (s *Service) StashApply(ctx context.Context, path string, index int, pop bool) (*Result, error) {
	ref, err := stashRef(index)
	if err != nil {
		return nil, err
	}
	verb := "apply"
	if pop {
		verb = "pop"
	}
	return s.op(ctx, path, time.Minute, "stash", verb, ref)
}

// StashDrop discards one stash. The work in it exists nowhere else, which is
// why the route above it demands a typed confirmation.
func (s *Service) StashDrop(ctx context.Context, path string, index int) (*Result, error) {
	ref, err := stashRef(index)
	if err != nil {
		return nil, err
	}
	return s.op(ctx, path, time.Minute, "stash", "drop", ref)
}

// validatePaths refuses anything that could climb out of the working tree or
// be read as an option. The `--` separator every caller adds stops a leading
// dash from becoming a flag, but a `..` segment still escapes the repository,
// so both checks stay.
func validatePaths(files []string) error {
	for _, f := range files {
		if err := validatePath(f); err != nil {
			return err
		}
	}
	return nil
}

// Stage adds paths to the index so the next commit records them. With no paths
// it stages every change in the working tree — additions, modifications and
// deletions alike — which is the "stage everything" the commit box offers.
//
// Staging is the inverse of Unstage and loses nothing, so neither is gated
// behind a typed confirmation; both sit under service.control with the rest of
// the recoverable operations.
func (s *Service) Stage(ctx context.Context, path string, files []string) (*Result, error) {
	if err := validatePaths(files); err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return s.op(ctx, path, time.Minute, "add", "-A")
	}
	return s.op(ctx, path, time.Minute, append([]string{"add", "-A", "--"}, files...)...)
}

// Unstage moves paths back out of the index without touching the working tree,
// so the edits themselves are never at risk. `reset -q HEAD` is used rather
// than the newer `restore --staged` because it works on every git a server is
// likely to carry; the reset is index-only and cannot lose the file's content.
func (s *Service) Unstage(ctx context.Context, path string, files []string) (*Result, error) {
	if err := validatePaths(files); err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return s.op(ctx, path, time.Minute, "reset", "-q", "HEAD")
	}
	return s.op(ctx, path, time.Minute, append([]string{"reset", "-q", "HEAD", "--"}, files...)...)
}

// Commit records whatever is staged. An empty message is refused unless this is
// an --amend that keeps the previous one, since git would otherwise open an
// editor this request cannot answer. The message reaches git as the argument to
// -m, so it cannot be read as an option however it begins.
//
// A commit needs user.name and user.email in the owner's git config; when they
// are missing git says exactly that, and that message is more useful than
// anything this layer could paraphrase — the operation runs AsOwner, so it is
// the account owning the repository whose identity is used.
func (s *Service) Commit(ctx context.Context, path, message string, amend bool) (*Result, error) {
	msg := strings.TrimSpace(message)
	if msg == "" && !amend {
		return nil, fmt.Errorf("%w: a commit message is required", ErrInvalidRef)
	}
	args := []string{"commit"}
	if amend {
		args = append(args, "--amend")
	}
	if msg != "" {
		args = append(args, "-m", msg)
	} else {
		// --amend with no new message keeps the old one rather than opening an
		// editor the web request has no way to drive.
		args = append(args, "--no-edit")
	}
	return s.op(ctx, path, time.Minute, args...)
}

// SetIdentity writes who commits made here are recorded as, into this
// repository's own config so an account-wide identity is never overruled for
// every other checkout at once.
func (s *Service) SetIdentity(ctx context.Context, path, name, email string) (*Result, error) {
	name, email = strings.TrimSpace(name), strings.TrimSpace(email)
	for _, v := range []string{name, email} {
		if v == "" || len(v) > 200 || strings.HasPrefix(v, "-") || strings.ContainsAny(v, "\n\r\x00") {
			return nil, fmt.Errorf("%w: a name and an address are both required", ErrInvalidRef)
		}
	}
	if !strings.Contains(email, "@") || strings.ContainsAny(email, " \t<>") {
		return nil, fmt.Errorf("%w: %q is not an email address", ErrInvalidRef, email)
	}
	if res, err := s.op(ctx, path, time.Minute, "config", "user.name", name); err != nil {
		return res, err
	}
	return s.op(ctx, path, time.Minute, "config", "user.email", email)
}

// Discard throws away uncommitted changes to one path. A tracked file is
// put back the way the index has it; an untracked one is deleted, which is
// the only meaning "discard" can have for a file with no earlier version.
// Both destroy work that exists nowhere else, which is why the route above
// demands a typed confirmation.
func (s *Service) Discard(ctx context.Context, path, file string) (*Result, error) {
	if err := validatePath(file); err != nil {
		return nil, err
	}
	// A path git knows nothing about — no index entry — is untracked. The
	// error exit is the answer, not a failure.
	if _, err := s.run(ctx, path, "ls-files", "--error-unmatch", "--", file); err != nil {
		return s.op(ctx, path, time.Minute, "clean", "-fd", "--", file)
	}
	return s.op(ctx, path, time.Minute, "checkout", "--", file)
}

// Reset moves the branch to a commit. Hard mode discards the working tree
// along with it — the most destructive thing this package can do — and clean
// goes one step further, deleting the untracked files a hard reset leaves
// behind, so "discard everything" can mean everything.
func (s *Service) Reset(ctx context.Context, path, ref string, hard, clean bool) (*Result, error) {
	if err := ValidateRef(ref); err != nil {
		return nil, err
	}
	mode := "--mixed"
	if hard {
		mode = "--hard"
	}
	res, err := s.op(ctx, path, time.Minute, "reset", mode, ref, "--")
	if err != nil || !hard || !clean {
		return res, err
	}
	cleaned, err := s.op(ctx, path, time.Minute, "clean", "-fd")
	if err != nil {
		return cleaned, err
	}
	res.Command += " && " + cleaned.Command
	if cleaned.Output != "Done." {
		res.Output += "\n" + cleaned.Output
	}
	return res, nil
}

// CreateTag marks a commit — HEAD when ref is empty. A message makes it an
// annotated tag, which records who made it and when; without one it is a
// lightweight pointer, which is fine for a private marker and wrong for a
// release. ValidateRef has already refused a leading dash on both names, so
// neither can be read as an option even though `git tag` takes no `--`.
func (s *Service) CreateTag(ctx context.Context, path, name, ref, message string) (*Result, error) {
	if err := ValidateRef(name); err != nil {
		return nil, err
	}
	args := []string{"tag"}
	if m := strings.TrimSpace(message); m != "" {
		if len(m) > 2000 {
			m = m[:2000]
		}
		args = append(args, "-a", "-m", m)
	}
	args = append(args, name)
	if ref != "" {
		if err := ValidateRef(ref); err != nil {
			return nil, err
		}
		args = append(args, ref)
	}
	return s.op(ctx, path, time.Minute, args...)
}

// DeleteTag removes a local tag. The commit it named is untouched, and a tag
// already pushed lives on at the remote, so this is ordinary-confirmation
// destructive rather than typed.
func (s *Service) DeleteTag(ctx context.Context, path, name string) (*Result, error) {
	if err := ValidateRef(name); err != nil {
		return nil, err
	}
	return s.op(ctx, path, time.Minute, "tag", "-d", name)
}

// validateRemoteName is ValidateRef without the slash: a remote's name is one
// path segment of refs/remotes/<name>/.
func validateRemoteName(name string) error {
	if err := ValidateRef(name); err != nil {
		return err
	}
	if strings.Contains(name, "/") {
		return fmt.Errorf("%w: %q", ErrInvalidRef, name)
	}
	return nil
}

// ValidateRemoteURL accepts the forms a remote is written in — https://,
// http://, ssh://, git:// and the scp-like user@host:path — and nothing that
// could be a local path or an option. `file://` and bare paths are refused
// deliberately: a clone from a path would copy any repository this process
// can read into the roots, and `ext::` is a shell by another name.
func ValidateRemoteURL(url string) error {
	url = strings.TrimSpace(url)
	if url == "" || len(url) > 2048 || strings.HasPrefix(url, "-") ||
		strings.ContainsAny(url, " \t\r\n\x00'\"`") {
		return fmt.Errorf("%w: %q is not a remote URL", ErrInvalidRef, url)
	}
	for _, scheme := range []string{"https://", "http://", "ssh://", "git://"} {
		if strings.HasPrefix(url, scheme) && len(url) > len(scheme) {
			return nil
		}
	}
	// git@github.com:owner/repo.git — an scp-style address has a user, a
	// host and a colon before any slash.
	if at := strings.Index(url, "@"); at > 0 {
		rest := url[at+1:]
		if colon := strings.Index(rest, ":"); colon > 0 && !strings.Contains(rest[:colon], "/") && colon+1 < len(rest) {
			return nil
		}
	}
	return fmt.Errorf("%w: %q is not a remote URL", ErrInvalidRef, url)
}

// AddRemote registers a remote. The name is validated as a ref and the URL as
// a network address; both come after the subcommand as positionals, and
// neither can begin with a dash.
func (s *Service) AddRemote(ctx context.Context, path, name, url string) (*Result, error) {
	if err := validateRemoteName(name); err != nil {
		return nil, err
	}
	if err := ValidateRemoteURL(url); err != nil {
		return nil, err
	}
	return s.op(ctx, path, time.Minute, "remote", "add", name, strings.TrimSpace(url))
}

// RemoveRemote forgets a remote and its tracking refs. Nothing on the remote
// itself changes, and adding it back is the whole of the undo.
func (s *Service) RemoveRemote(ctx context.Context, path, name string) (*Result, error) {
	if err := validateRemoteName(name); err != nil {
		return nil, err
	}
	return s.op(ctx, path, time.Minute, "remote", "remove", name)
}

// validateDirName is what a new checkout may be called: one path segment,
// nothing hidden, nothing that could be read as an option.
func validateDirName(name string) error {
	if name == "" || len(name) > 255 || name == "." || name == ".." ||
		strings.HasPrefix(name, ".") || strings.HasPrefix(name, "-") ||
		strings.ContainsAny(name, "/\\\x00") {
		return fmt.Errorf("%w: %q is not a directory name", ErrInvalidRef, name)
	}
	for _, r := range name {
		if !safeRefChars(r) {
			return fmt.Errorf("%w: %q is not a directory name", ErrInvalidRef, name)
		}
	}
	return nil
}

// Clone fetches a repository into a new directory under one of the roots.
//
// The parent has to be inside the configured roots and exist already — the
// roots are the boundary the operator drew for this page, and a clone that
// created a tree outside them would be a checkout the page could then not
// operate on. The new directory must not exist, so a clone can never write
// into somebody else's tree. It runs as the parent's owner, like everything
// else here, so the files land owned by the account that owns the place they
// were put.
func (s *Service) Clone(ctx context.Context, parent, url, name string) (string, *Result, error) {
	dir, err := s.ResolveDir(parent)
	if err != nil {
		return "", nil, err
	}
	if err := ValidateRemoteURL(url); err != nil {
		return "", nil, err
	}
	if name == "" {
		name = nameFromURL(url)
	}
	if err := validateDirName(name); err != nil {
		return "", nil, err
	}
	target := filepath.Join(dir, name)
	if _, err := os.Lstat(target); err == nil {
		return "", nil, fmt.Errorf("%w: %s already exists", ErrInvalidRef, target)
	}
	res, err := s.op(ctx, dir, 10*time.Minute, "clone", "--", strings.TrimSpace(url), name)
	if err != nil {
		return "", res, err
	}
	return target, res, nil
}

// nameFromURL is what git itself would call a clone of this URL: the last
// path element without its .git.
func nameFromURL(url string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(url), "/")
	trimmed = strings.TrimSuffix(trimmed, ".git")
	if i := strings.LastIndexAny(trimmed, "/:"); i >= 0 {
		trimmed = trimmed[i+1:]
	}
	return trimmed
}

// Init turns an existing directory inside the roots into a repository, on a
// branch called main. Two commands rather than `init -b`, which arrived in
// git 2.28 and is still missing from some servers.
func (s *Service) Init(ctx context.Context, path string) (*Result, error) {
	dir, err := s.ResolveDir(path)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return nil, fmt.Errorf("%w: %s is already a repository", ErrInvalidRef, dir)
	}
	res, err := s.op(ctx, dir, time.Minute, "init", "-q")
	if err != nil {
		return res, err
	}
	if _, err := s.run(ctx, dir, "symbolic-ref", "HEAD", "refs/heads/main"); err != nil {
		return res, err
	}
	res.Output = "Initialised an empty repository in " + dir
	return res, nil
}
