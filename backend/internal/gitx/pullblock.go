package gitx

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// PullBlocked is a pull that did not start, with what stood in its way.
//
// Git refuses a fast-forward that would overwrite a local change or an
// untracked file, and names each one. Pull refuses one git would start and
// fail halfway through, because a folder the pulled files land in belongs to
// an account other than the one git runs as: git writes every file it can,
// stops at the first it cannot, and leaves HEAD where it was — so the files
// it did write are now "local changes" to the very pull that wrote them, and
// the next attempt is refused for them. Either way nothing has been changed
// yet, which is what lets the page offer the ways past it instead of a
// paragraph of git's output.
type PullBlocked struct {
	Changed    []string           `json:"changed,omitempty"`
	Untracked  []string           `json:"untracked,omitempty"`
	Unwritable []hostexec.Foreign `json:"unwritable,omitempty"`
	// Owner is the account git runs as in this checkout.
	Owner string `json:"owner,omitempty"`
}

func (b *PullBlocked) Error() string {
	if n := len(b.Unwritable); n > 0 {
		verb := "belong"
		if n == 1 {
			verb = "belongs"
		}
		return fmt.Sprintf("%s in this checkout %s to another account, so git, running as %s, "+
			"could write only some of the pulled files. Nothing has been changed.",
			plural(n, "folder", "folders"), verb, b.Owner)
	}
	return fmt.Sprintf("The pull would overwrite %s here. Nothing has been changed.",
		plural(len(b.Changed)+len(b.Untracked), "local file", "local files"))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// refusedPull reads git's refusal to overwrite local work, which lists each
// file on its own tab-indented line under the sentence that says why.
func refusedPull(output string) *PullBlocked {
	var b PullBlocked
	var list *[]string
	for _, line := range strings.Split(output, "\n") {
		switch {
		case strings.Contains(line, "local changes to the following files would be overwritten"):
			list = &b.Changed
		case strings.Contains(line, "untracked working tree files would be"):
			list = &b.Untracked
		case list != nil && strings.HasPrefix(line, "\t"):
			*list = append(*list, unquotePath(strings.TrimPrefix(line, "\t")))
		default:
			list = nil
		}
	}
	if len(b.Changed)+len(b.Untracked) == 0 {
		return nil
	}
	return &b
}

// unquotePath undoes core.quotePath, which wraps a name holding anything
// outside printable ASCII in double quotes with C-style octal escapes — the
// escapes a Go string literal also reads.
func unquotePath(p string) string {
	if strings.HasPrefix(p, `"`) {
		if u, err := strconv.Unquote(p); err == nil {
			return u
		}
	}
	return p
}

// pullWritable stops a pull that git, running as the checkout's owner, could
// only half finish.
//
// Asking costs a fetch, so it is asked only when there is something to ask
// about: when no directory holding a tracked file belongs to anyone else —
// nearly always — the pull goes ahead untouched. When some do, what decides it
// is whether the pull writes into one of them, which needs the incoming
// commit; a directory a container owns for its own data blocks only the pulls
// that change it. Anything this cannot work out is left to the pull itself,
// which reports its own failure.
func (s *Service) pullWritable(ctx context.Context, path string, fetch []string, target string) error {
	owner, ok := hostexec.OwnerOf(path)
	if !ok || fetch == nil {
		return nil
	}
	tracked, err := s.run(ctx, path, "ls-files", "-z")
	if err != nil || len(hostexec.Unwritable(path, owner, splitNul(tracked))) == 0 {
		return nil
	}
	if _, err := s.runTimeout(ctx, path, 2*time.Minute, fetch...); err != nil {
		return nil
	}
	if _, err := s.run(ctx, path, "merge-base", "--is-ancestor", "HEAD", target); err != nil {
		return nil
	}
	incoming, err := s.run(ctx, path, "diff", "--name-only", "-z", "--no-renames", "HEAD", target, "--")
	if err != nil {
		return nil
	}
	if dirs := hostexec.Unwritable(path, owner, splitNul(incoming)); len(dirs) > 0 {
		return &PullBlocked{Unwritable: dirs, Owner: owner.Name}
	}
	return nil
}

func splitNul(s string) []string {
	var out []string
	for _, entry := range strings.Split(s, "\x00") {
		if entry != "" {
			out = append(out, entry)
		}
	}
	return out
}

// StashAndPull puts every local change aside, untracked files included, and
// pulls. The stash is all of them rather than only the files in the way,
// because a stash of some paths is the one form git can get wrong (see the
// literal-pathspec note in executeEnv); popping it afterwards is Stashes →
// Pop. If the pull still fails the stash stays, and its line in the output
// says so.
func (s *Service) StashAndPull(ctx context.Context, path string) (*Result, error) {
	message := "Put aside to pull"
	if branch, err := s.CurrentBranch(ctx, path); err == nil {
		message += " " + branch
	}
	stashed, err := s.Stash(ctx, path, message)
	if err != nil {
		return stashed, err
	}
	pulled, err := s.Pull(ctx, path)
	return chain(stashed, pulled), err
}

// DiscardAndPull throws away exactly the files git named as in the way — a
// changed file goes back to HEAD, an untracked one is deleted — and pulls.
// The list is git's own, read again here rather than taken from the request,
// so what is discarded is what is in the way now and nothing a client chose.
func (s *Service) DiscardAndPull(ctx context.Context, path string) (*Result, error) {
	res, err := s.Pull(ctx, path)
	var blocked *PullBlocked
	if !errors.As(err, &blocked) || len(blocked.Changed)+len(blocked.Untracked) == 0 {
		return res, err
	}
	if err := validatePaths(append(append([]string{}, blocked.Changed...), blocked.Untracked...)); err != nil {
		return nil, err
	}
	var steps []*Result
	if len(blocked.Changed) > 0 {
		restored, err := s.op(ctx, path, time.Minute, append([]string{"checkout", "HEAD", "--"}, blocked.Changed...)...)
		if err != nil {
			return restored, err
		}
		steps = append(steps, restored)
	}
	if len(blocked.Untracked) > 0 {
		removed, err := s.op(ctx, path, time.Minute, append([]string{"clean", "-f", "--"}, blocked.Untracked...)...)
		if err != nil {
			return removed, err
		}
		steps = append(steps, removed)
	}
	pulled, err := s.Pull(ctx, path)
	return chain(append(steps, pulled)...), err
}

// OwnAndPull hands the folders a pull is blocked on to the checkout's owner
// and pulls. The folders are found again here, not taken from the request.
// What it gave back is returned for the audit trail, because the previous
// owner is not written down anywhere else.
func (s *Service) OwnAndPull(ctx context.Context, path string) (*Result, []hostexec.Foreign, error) {
	res, err := s.Pull(ctx, path)
	var blocked *PullBlocked
	if !errors.As(err, &blocked) || len(blocked.Unwritable) == 0 {
		return res, nil, err
	}
	owner, ok := hostexec.OwnerOf(path)
	if !ok {
		return res, nil, err
	}
	rels := make([]string, len(blocked.Unwritable))
	for i, dir := range blocked.Unwritable {
		rels[i] = dir.Path
	}
	given := &Result{
		Command: "chown " + owner.Name + " -- " + strings.Join(rels, " "),
		Output:  fmt.Sprintf("Gave %s to %s.", plural(len(rels), "folder", "folders"), owner.Name),
		OK:      true,
	}
	if err := hostexec.GiveBack(path, owner, rels); err != nil {
		given.Output, given.OK = err.Error(), false
		return given, blocked.Unwritable, err
	}
	pulled, err := s.Pull(ctx, path)
	return chain(given, pulled), blocked.Unwritable, err
}

// chain reports several steps as the one operation the operator asked for.
func chain(steps ...*Result) *Result {
	out := &Result{OK: true}
	var commands, outputs []string
	for _, step := range steps {
		if step == nil {
			continue
		}
		commands = append(commands, step.Command)
		if step.Output != "" && step.Output != "Done." {
			outputs = append(outputs, step.Output)
		}
		out.OK = out.OK && step.OK
	}
	out.Command = strings.Join(commands, " && ")
	out.Output = strings.Join(outputs, "\n")
	if out.Output == "" {
		out.Output = "Done."
	}
	return out
}
