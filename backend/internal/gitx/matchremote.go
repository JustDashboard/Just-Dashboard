package gitx

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MatchBranch is one local branch in a MatchPlan.
type MatchBranch struct {
	Name string `json:"name"`
	// Head is the commit the plan was read at. A branch the operator chose to
	// discard is only discarded while it still sits here, so a commit made
	// after the dialog opened is never what the choice throws away.
	Head string `json:"head"`
	// Target is the remote branch this one becomes; empty when it is deleted
	// because the remote has no such branch.
	Target string `json:"target,omitempty"`
	Behind int    `json:"behind,omitempty"`
	// Ahead counts commits the target lacks, which is what decides between a
	// fast-forward and moving the branch back.
	Ahead int `json:"ahead,omitempty"`
	// Commits counts those no ref on the remote reaches: what discarding it
	// would lose.
	Commits  int    `json:"commits,omitempty"`
	Worktree string `json:"worktree,omitempty"`
}

// MatchPlan is what matching this checkout to its remote would do, read before
// the confirmation that asks for it and read again by MatchRemote.
type MatchPlan struct {
	Remote string `json:"remote"`
	// Branch is the remote's default branch, where the checkout ends up; empty
	// when it stays where it is. Switch says that means moving to it.
	Branch string `json:"branch,omitempty"`
	Switch bool   `json:"switch,omitempty"`
	// Stash counts the uncommitted files put aside first. It is zero when the
	// checkout does not have to move, because they are then not in the way.
	Stash int `json:"stash"`
	// Delete and Update lose nothing: every commit on them is on the remote.
	Delete []MatchBranch `json:"delete"`
	Update []MatchBranch `json:"update"`
	// Unique hold commits the remote does not have, and are kept unless the
	// operator names them.
	Unique []MatchBranch `json:"unique"`
	// InUse are checked out in another worktree, where git will not move them.
	InUse []MatchBranch `json:"inUse"`
	// StaleRefs are remote-tracking refs under a name that is no remote here,
	// which no fetch will ever update or prune.
	StaleRefs    []string `json:"staleRefs"`
	StaleRemotes []string `json:"staleRemotes"`
	// Blocked is why nothing can be done yet, in a sentence.
	Blocked string `json:"blocked,omitempty"`
}

// MatchChoice names a branch from MatchPlan.Unique to discard, at the commit
// the operator saw.
type MatchChoice struct {
	Name string `json:"name"`
	Head string `json:"head"`
}

type matchRef struct {
	name, head, upstream, worktree string
	current                        bool
}

// MatchPlan reads what MatchRemote would do from the refs as they are. It does
// not fetch: the page fetches first, and a stale ref can only make this keep
// more, since a branch counts as safe only when a remote ref still holds it.
func (s *Service) MatchPlan(ctx context.Context, path string) (*MatchPlan, error) {
	plan := &MatchPlan{
		Delete: []MatchBranch{}, Update: []MatchBranch{}, Unique: []MatchBranch{},
		InUse: []MatchBranch{}, StaleRefs: []string{}, StaleRemotes: []string{},
	}
	locals, err := s.matchLocals(ctx, path)
	if err != nil {
		return nil, err
	}
	var current *matchRef
	for i := range locals {
		if locals[i].current {
			current = &locals[i]
		}
	}
	currentName := ""
	if current != nil {
		currentName = current.name
	}
	plan.Remote, err = s.matchRemoteName(ctx, path, currentName)
	if err != nil {
		return nil, err
	}
	if plan.Remote == "" {
		plan.Blocked = "This checkout has no remote to match."
		return plan, nil
	}
	if op := s.operationInProgress(ctx, path); op != "" {
		plan.Blocked = "A " + op + " is in progress here. Finish or abort it first."
		return plan, nil
	}

	remoteRefs, err := s.matchRemoteRefs(ctx, path, plan)
	if err != nil {
		return nil, err
	}
	unique, err := s.matchUniqueCommits(ctx, path, plan.Remote, len(locals))
	if err != nil {
		return nil, err
	}
	if current == nil {
		// Detached commits are on no branch, so nothing below would count them
		// and moving away is what strands them.
		if out, err := s.run(ctx, path, "rev-list", "--count", "HEAD", "--not", "--remotes="+plan.Remote); err == nil {
			if n, _ := strconv.Atoi(strings.TrimSpace(out)); n > 0 {
				plan.Blocked = fmt.Sprintf("HEAD is detached on %d commits %s does not have. Put them on a branch first.", n, plan.Remote)
				return plan, nil
			}
		}
	}

	branch := s.matchDefaultBranch(ctx, path, plan.Remote, remoteRefs)
	linked, _, _ := linkedWorktree(path)
	if !linked && branch != "" {
		plan.Branch = branch
		for _, b := range locals {
			if b.name == branch && b.worktree != "" {
				plan.Branch = ""
			}
		}
		plan.Switch = plan.Branch != "" && plan.Branch != currentName
	}

	for _, b := range locals {
		// A branch held by another worktree, and this checkout's own when it
		// is not leaving it, cannot be moved or deleted out from under it.
		if b.worktree != "" {
			plan.InUse = append(plan.InUse, MatchBranch{Name: b.name, Head: b.head, Worktree: b.worktree})
			continue
		}
		target, skip := matchTarget(b, plan.Remote, remoteRefs)
		if skip {
			continue
		}
		// The branch the checkout ends on is the remote's by definition, even
		// if it was left tracking a name that has since gone.
		if b.name == plan.Branch {
			target = "refs/remotes/" + plan.Remote + "/" + b.name
		}
		row := MatchBranch{Name: b.name, Head: b.head, Target: strings.TrimPrefix(target, "refs/remotes/")}
		if target != "" && remoteRefs[target] == b.head {
			continue
		}
		if target != "" {
			row.Ahead, row.Behind, err = s.matchDivergence(ctx, path, b.head, target)
			if err != nil {
				return nil, err
			}
		} else if b.current && plan.Branch == "" {
			// Nowhere to go, so it cannot be deleted.
			plan.InUse = append(plan.InUse, MatchBranch{Name: b.name, Head: b.head, Worktree: path})
			continue
		}
		if !unique[b.head] {
			if target == "" {
				plan.Delete = append(plan.Delete, row)
			} else {
				plan.Update = append(plan.Update, row)
			}
			continue
		}
		out, err := s.run(ctx, path, "rev-list", "--count", b.head, "--not", "--remotes="+plan.Remote)
		if err != nil {
			return nil, err
		}
		row.Commits, _ = strconv.Atoi(strings.TrimSpace(out))
		plan.Unique = append(plan.Unique, row)
	}

	// Files are only in the way of something that rewrites the working tree.
	movesTree := plan.Switch
	for _, row := range plan.Update {
		movesTree = movesTree || row.Name == currentName
	}
	if movesTree {
		plan.Stash = s.matchDirty(ctx, path)
	}
	return plan, nil
}

// MatchRemote makes this checkout look like a fresh copy of its remote without
// losing what the operator did not choose to lose: it ends on the remote's
// default branch, moves every local branch to its remote branch, deletes those
// the remote no longer has, and drops refs left behind by remotes that are no
// longer configured. A branch with commits the remote lacks is kept unless
// discard names it, and uncommitted work goes into a stash. Tags and stashes
// are left alone.
func (s *Service) MatchRemote(ctx context.Context, path string, discard []MatchChoice) (*Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	plan, err := s.MatchPlan(ctx, path)
	if err != nil {
		return nil, err
	}
	if plan.Blocked != "" {
		return &Result{Command: "git match " + plan.Remote, Output: plan.Blocked}, errors.New(plan.Blocked)
	}
	chosen := map[string]string{}
	for _, c := range discard {
		chosen[c.Name] = c.Head
	}
	update := append([]MatchBranch{}, plan.Update...)
	remove := append([]MatchBranch{}, plan.Delete...)
	kept := 0
	for _, row := range plan.Unique {
		switch {
		case chosen[row.Name] != row.Head:
			kept++
		case row.Target == "":
			remove = append(remove, row)
		default:
			update = append(update, row)
		}
	}

	current, _ := s.CurrentBranch(ctx, path)
	done := []string{}
	finish := func(res *Result, err error) (*Result, error) {
		output := err.Error()
		if res != nil && res.Output != "" {
			output = res.Output
		}
		return &Result{Command: "git match " + plan.Remote, Output: strings.Join(append(done, output), " ")}, err
	}

	// The branch this checkout is on, or is about to be on, moves with the
	// working tree; the rest are only refs.
	var landing *MatchBranch
	others := make([]MatchBranch, 0, len(update))
	for i := range update {
		if update[i].Name == current || (plan.Switch && update[i].Name == plan.Branch) {
			landing = &update[i]
		} else {
			others = append(others, update[i])
		}
	}
	if plan.Switch || (landing != nil && landing.Name == current) {
		if dirty := s.matchDirty(ctx, path); dirty > 0 {
			if res, err := s.Stash(ctx, path, "Put aside to match "+plan.Remote); err != nil {
				return finish(res, err)
			}
			done = append(done, "Put "+matchCount(dirty, "uncommitted file")+" in a stash.")
		}
	}

	moved := 0
	for _, row := range others {
		if res, err := s.matchMoveRef(ctx, path, row); err != nil {
			return finish(res, err)
		}
		moved++
	}
	// The branch being switched to moves before the switch, so the working
	// tree goes straight to its final state rather than through the old one.
	if landing != nil && landing.Name != current {
		if res, err := s.matchMoveRef(ctx, path, *landing); err != nil {
			return finish(res, err)
		}
		moved++
	}
	if plan.Switch {
		if res, err := s.matchSwitch(ctx, path, plan); err != nil {
			return finish(res, err)
		}
		done = append(done, "Switched to "+plan.Branch+".")
	}
	if landing != nil && landing.Name == current {
		args := []string{"merge", "--ff-only", "refs/remotes/" + landing.Target}
		if landing.Ahead > 0 {
			args = []string{"reset", "--hard", "refs/remotes/" + landing.Target, "--"}
		}
		if res, err := s.op(ctx, path, time.Minute, args...); err != nil {
			return finish(res, err)
		}
		moved++
	}
	if moved > 0 {
		done = append(done, "Updated "+matchCount(moved, "branch")+".")
	}

	names := make([]string, 0, len(remove))
	for _, row := range remove {
		names = append(names, row.Name)
	}
	if res, err := s.matchDeleteRefs(ctx, path, names, "-D"); err != nil {
		return finish(res, err)
	}
	if len(names) > 0 {
		done = append(done, "Deleted "+matchCount(len(names), "branch")+".")
	}
	if res, err := s.matchDeleteRefs(ctx, path, plan.StaleRefs, "-D", "-r"); err != nil {
		return finish(res, err)
	}
	if n := len(plan.StaleRefs); n > 0 {
		done = append(done, "Removed "+matchCount(n, "leftover ref")+".")
	}
	if kept > 0 {
		done = append(done, "Kept "+matchCount(kept, "branch")+" with commits "+plan.Remote+" does not have.")
	}
	if len(done) == 0 {
		done = append(done, "Already matches "+plan.Remote+".")
	}
	return &Result{Command: "git match " + plan.Remote, Output: strings.Join(done, " "), OK: true}, nil
}

// CheckoutUpdated switches to a local branch and fast-forwards it to target,
// or to its upstream when target is empty. It is what a branch far behind its
// remote needs, and being unable to move is not a failure: the switch was the
// request, so git's reason comes back beside it and history is left alone.
func (s *Service) CheckoutUpdated(ctx context.Context, path, ref, target string) (*Result, error) {
	if err := ValidateRef(ref); err != nil {
		return nil, err
	}
	merge := "@{upstream}"
	if target != "" {
		if err := ValidateRef(target); err != nil {
			return nil, err
		}
		merge = target
	}
	res, err := s.op(ctx, path, time.Minute, "checkout", ref, "--")
	if err != nil {
		return res, err
	}
	updated, err := s.op(ctx, path, time.Minute, "merge", "--ff-only", merge)
	if err != nil {
		return &Result{
			Command: res.Command + " && " + updated.Command, OK: true,
			Output: "Switched to " + ref + ", but could not bring it up to date.\n" + updated.Output,
		}, nil
	}
	return chain(res, updated), nil
}

func (s *Service) matchDirty(ctx context.Context, path string) int {
	st, err := s.run(ctx, path, "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil {
		return 0
	}
	return len(parseStatus(st))
}

func (s *Service) matchLocals(ctx context.Context, path string) ([]matchRef, error) {
	format := strings.Join([]string{"%(refname:lstrip=2)", "%(objectname)", "%(upstream)", "%(HEAD)", "%(worktreepath)"}, "%1f")
	out, err := s.run(ctx, path, "for-each-ref", "--format="+format, "refs/heads")
	if err != nil {
		return nil, err
	}
	locals := []matchRef{}
	for _, line := range nonEmptyLines(out) {
		p := strings.Split(line, "\x1f")
		if len(p) < 5 {
			continue
		}
		b := matchRef{name: p[0], head: p[1], upstream: p[2], current: strings.TrimSpace(p[3]) == "*"}
		// %(worktreepath) names this checkout for its own branch too; only
		// another's is a constraint.
		if wt := strings.TrimSpace(p[4]); wt != "" && !b.current {
			b.worktree = wt
		}
		locals = append(locals, b)
	}
	return locals, nil
}

// matchRemoteName is the current branch's remote when it has one, then origin,
// then the only remote there is; "" when none of those exist or it is a guess.
func (s *Service) matchRemoteName(ctx context.Context, path, current string) (string, error) {
	out, err := s.run(ctx, path, "remote")
	if err != nil {
		return "", err
	}
	remotes := nonEmptyLines(out)
	if current != "" {
		configured := s.remoteFor(ctx, path, current)
		for _, remote := range remotes {
			if remote == configured {
				return remote, nil
			}
		}
	}
	for _, remote := range remotes {
		if remote == "origin" {
			return remote, nil
		}
	}
	if len(remotes) == 1 {
		return remotes[0], nil
	}
	return "", nil
}

// matchRemoteRefs answers every ref of the chosen remote and fills in the
// plan's stale refs: those under a name no configured remote claims.
func (s *Service) matchRemoteRefs(ctx context.Context, path string, plan *MatchPlan) (map[string]string, error) {
	out, err := s.run(ctx, path, "remote")
	if err != nil {
		return nil, err
	}
	remotes := nonEmptyLines(out)
	refs, err := s.run(ctx, path, "for-each-ref", "--format=%(refname)%1f%(objectname)", "refs/remotes")
	if err != nil {
		return nil, err
	}
	found := map[string]string{}
	stale := map[string]bool{}
next:
	for _, line := range nonEmptyLines(refs) {
		name, head, _ := strings.Cut(line, "\x1f")
		for _, remote := range remotes {
			if strings.HasPrefix(name, "refs/remotes/"+remote+"/") {
				if remote == plan.Remote {
					found[name] = head
				}
				continue next
			}
		}
		short := strings.TrimPrefix(name, "refs/remotes/")
		plan.StaleRefs = append(plan.StaleRefs, short)
		group, _, _ := strings.Cut(short, "/")
		stale[group] = true
	}
	for group := range stale {
		plan.StaleRemotes = append(plan.StaleRemotes, group)
	}
	sort.Strings(plan.StaleRemotes)
	return found, nil
}

// matchUniqueCommits is every commit on a local branch that no ref of the
// remote reaches. A branch's tip is in it exactly when the branch holds such
// a commit, so one process answers for every branch on a checkout with
// hundreds of them. A failure must stop the plan rather than read as "none",
// which would call every branch safe to delete.
func (s *Service) matchUniqueCommits(ctx context.Context, path, remote string, locals int) (map[string]bool, error) {
	set := map[string]bool{}
	if locals == 0 {
		return set, nil
	}
	out, err := s.run(ctx, path, "rev-list", "--branches", "--not", "--remotes="+remote)
	if err != nil {
		return nil, err
	}
	for _, sha := range nonEmptyLines(out) {
		set[strings.TrimSpace(sha)] = true
	}
	return set, nil
}

// matchDefaultBranch is what the remote's HEAD names, from the ref a clone
// leaves, then by asking the remote, then the two conventional names.
func (s *Service) matchDefaultBranch(ctx context.Context, path, remote string, refs map[string]string) string {
	prefix := "refs/remotes/" + remote + "/"
	if out, err := s.run(ctx, path, "symbolic-ref", prefix+"HEAD"); err == nil {
		if name := strings.TrimPrefix(strings.TrimSpace(out), prefix); refs[prefix+name] != "" {
			return name
		}
	}
	if out, err := s.run(ctx, path, "ls-remote", "--symref", remote, "HEAD"); err == nil {
		for _, line := range nonEmptyLines(out) {
			if rest, ok := strings.CutPrefix(line, "ref: refs/heads/"); ok {
				if name, _, _ := strings.Cut(rest, "\t"); refs[prefix+name] != "" {
					return name
				}
			}
		}
	}
	for _, name := range []string{"main", "master"} {
		if refs[prefix+name] != "" {
			return name
		}
	}
	return ""
}

// matchTarget is the remote ref a branch should match. skip is a branch that
// follows another remote which still has it, which is none of this remote's
// business.
func matchTarget(b matchRef, remote string, refs map[string]string) (target string, skip bool) {
	prefix := "refs/remotes/" + remote + "/"
	if strings.HasPrefix(b.upstream, prefix) {
		if refs[b.upstream] != "" {
			return b.upstream, false
		}
		// Its own upstream is gone, and a same-named branch on the remote is
		// someone else's work however alike the names are.
		return "", false
	}
	if b.upstream != "" {
		return "", true
	}
	if refs[prefix+b.name] != "" {
		return prefix + b.name, false
	}
	return "", false
}

func (s *Service) matchDivergence(ctx context.Context, path, head, target string) (ahead, behind int, err error) {
	out, err := s.run(ctx, path, "rev-list", "--left-right", "--count", head+"..."+target)
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("unexpected rev-list output: %q", out)
	}
	ahead, _ = strconv.Atoi(fields[0])
	behind, _ = strconv.Atoi(fields[1])
	return ahead, behind, nil
}

// matchMoveRef moves a branch nothing has checked out. Naming the old commit
// makes git refuse if the branch moved since the plan was read.
func (s *Service) matchMoveRef(ctx context.Context, path string, row MatchBranch) (*Result, error) {
	return s.op(ctx, path, time.Minute, "update-ref", "-m", "match remote",
		"refs/heads/"+row.Name, "refs/remotes/"+row.Target, row.Head)
}

func (s *Service) matchSwitch(ctx context.Context, path string, plan *MatchPlan) (*Result, error) {
	if _, err := s.run(ctx, path, "show-ref", "--verify", "--quiet", "refs/heads/"+plan.Branch); err == nil {
		return s.Checkout(ctx, path, plan.Branch)
	}
	return s.CheckoutRemote(ctx, path, plan.Remote+"/"+plan.Branch, plan.Branch)
}

// matchDeleteRefs deletes in batches, because a checkout can hold hundreds and
// an argument list cannot grow without limit. git carries on past a name it
// refuses, so the error says everything it could not do.
func (s *Service) matchDeleteRefs(ctx context.Context, path string, names []string, flags ...string) (*Result, error) {
	const batch = 100
	for start := 0; start < len(names); start += batch {
		end := min(start+batch, len(names))
		args := append(append([]string{"branch"}, flags...), "--")
		if res, err := s.op(ctx, path, time.Minute, append(args, names[start:end]...)...); err != nil {
			return res, err
		}
	}
	return nil, nil
}

func matchCount(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	if strings.HasSuffix(noun, "ch") {
		return strconv.Itoa(n) + " " + noun + "es"
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
