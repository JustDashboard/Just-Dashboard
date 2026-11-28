package gitx

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// What the assistant's workspace does with a checkout beyond the page's own
// operations: a fetch and a push that carry a credential in their environment for
// that one git process (never in its arguments, never in a file), and a commit
// recorded under a name the caller gives when the checkout's account has none.
// Each runs as the checkout's owner, like every other operation here.

// FetchWith fetches the checkout's remotes with extra in git's environment.
func (s *Service) FetchWith(ctx context.Context, path string, extra map[string]string) (*Result, error) {
	return s.opEnv(ctx, path, 3*time.Minute, extra, "fetch", "--prune", "origin")
}

// PushWith pushes the current branch to origin, as branch on the remote when one
// is named and as itself otherwise, setting the upstream when the branch has
// none. A checkout on no branch (a deployment that builds a tag) pushes HEAD to
// the branch named, and needs one. It never forces: a push the remote refuses
// because it moved is answered with git's own words, and nothing on the remote
// is lost.
func (s *Service) PushWith(ctx context.Context, path, branch string, extra map[string]string) (*Result, error) {
	current, err := s.CurrentBranch(ctx, path)
	if err != nil && branch == "" {
		return nil, err
	}
	target := current
	if branch != "" {
		if err := ValidateRef(branch); err != nil {
			return nil, err
		}
		target = branch
	}
	// Every refspec is written out whole: a branch may be called "+main", and a
	// bare name that begins with + is git's mark for a forced update.
	args := []string{"push", "--porcelain", "origin", "HEAD:refs/heads/" + target}
	if target == current && !s.hasUpstream(ctx, path) {
		args = []string{"push", "--porcelain", "--set-upstream", "origin", "refs/heads/" + current + ":refs/heads/" + current}
	}
	return s.opEnv(ctx, path, 3*time.Minute, extra, args...)
}

// CommitAs commits what is staged with message. When the checkout's account has
// no name or address for commits, name and email are used for this one commit,
// through git's own variables rather than its configuration.
func (s *Service) CommitAs(ctx context.Context, path, message, name, email string) (*Result, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil, fmt.Errorf("%w: a commit message is required", ErrInvalidRef)
	}
	extra := map[string]string{}
	if out, err := s.run(ctx, path, "config", "user.email"); err != nil || strings.TrimSpace(out) == "" {
		if name == "" || email == "" || strings.ContainsAny(name+email, "\n\r\x00<>") {
			return nil, fmt.Errorf("%w: the checkout's account has no name for commits and none was given", ErrInvalidRef)
		}
		extra = map[string]string{
			"GIT_AUTHOR_NAME": name, "GIT_AUTHOR_EMAIL": email,
			"GIT_COMMITTER_NAME": name, "GIT_COMMITTER_EMAIL": email,
		}
	}
	return s.opEnv(ctx, path, time.Minute, extra, "commit", "-m", message)
}

// HeadCommit is the commit HEAD names, in full.
func (s *Service) HeadCommit(ctx context.Context, path string) (string, error) {
	out, err := s.run(ctx, path, "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

// OriginURL is the address of the checkout's origin, empty when it has none.
func (s *Service) OriginURL(ctx context.Context, path string) string {
	out, err := s.run(ctx, path, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// DiffStat is what the working tree changes against HEAD, staged or not, as
// git's short summary: "3 files changed, 12 insertions(+), 4 deletions(-)".
func (s *Service) DiffStat(ctx context.Context, path string) string {
	out, err := s.run(ctx, path, "diff", "HEAD", "--shortstat")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func (s *Service) opEnv(ctx context.Context, path string, timeout time.Duration, extra map[string]string, args ...string) (*Result, error) {
	out, err := s.executeEnv(ctx, path, timeout, "", extra, args...)
	res := &Result{Command: "git " + strings.Join(args, " "), Output: strings.TrimSpace(string(out)), OK: err == nil}
	if err != nil {
		return res, fmt.Errorf("git %s: %s", args[0], res.Output)
	}
	if res.Output == "" {
		res.Output = "Done."
	}
	return res, nil
}
