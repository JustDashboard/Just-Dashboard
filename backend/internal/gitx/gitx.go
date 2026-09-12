// Package gitx exposes the read side of the git repositories on a server:
// which ones exist, what state each working tree is in, and what its history
// and branches look like.
//
// Everything here runs `git` with an explicit argument vector — never through
// a shell — and every value that reaches an argument is either validated
// against a strict pattern or passed after an explicit `--` separator, so a
// branch called `--upload-pack=…` cannot turn into an option.
//
// Read and write are deliberately separated: this file answers questions,
// while mutating operations live in ops.go behind their own capability check.
package gitx

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

var (
	ErrNotInstalled = errors.New("git is not installed on this host")
	ErrNotARepo     = errors.New("not a git repository")
	ErrOutsideRoots = errors.New("path is outside the configured git roots")
	ErrInvalidRef   = errors.New("ref contains characters that are not allowed")
)

// safeRef is what a branch, tag or commit-ish may contain. git itself allows
// more, but this covers real-world names and refuses anything that could be
// read as an option or a path traversal.
var safeRefChars = func(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return strings.ContainsRune("._-/@+", r)
}

// ValidateRef rejects anything that is not a plausible ref name. The leading
// dash check is the important one: it is what stops a ref from being parsed as
// a flag even before the `--` separator does its job.
func ValidateRef(ref string) error {
	if ref == "" || len(ref) > 255 || strings.HasPrefix(ref, "-") ||
		strings.Contains(ref, "..") || strings.HasSuffix(ref, ".lock") {
		return fmt.Errorf("%w: %q", ErrInvalidRef, ref)
	}
	for _, r := range ref {
		if !safeRefChars(r) {
			return fmt.Errorf("%w: %q", ErrInvalidRef, ref)
		}
	}
	return nil
}

type Service struct {
	roots []string
}

func New(roots []string) *Service {
	cleaned := make([]string, 0, len(roots))
	for _, r := range roots {
		if r = strings.TrimSpace(r); r != "" {
			cleaned = append(cleaned, filepath.Clean(r))
		}
	}
	return &Service{roots: cleaned}
}

func (s *Service) Available() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// Resolve checks that a path is a git repository inside the configured roots.
// Both halves matter: the roots are the boundary an operator configured, and
// symlinks are resolved first so a link cannot point out of them.
func (s *Service) Resolve(path string) (string, error) {
	clean := filepath.Clean(path)
	real, err := filepath.EvalSymlinks(clean)
	if err != nil {
		real = clean
	}
	ok := false
	for _, root := range s.roots {
		if real == root || strings.HasPrefix(real, root+string(os.PathSeparator)) {
			ok = true
			break
		}
	}
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrOutsideRoots, path)
	}
	if fi, err := os.Stat(filepath.Join(real, ".git")); err != nil || (!fi.IsDir() && !fi.Mode().IsRegular()) {
		return "", fmt.Errorf("%w: %s", ErrNotARepo, path)
	}
	return real, nil
}

// Repo is the summary shown in the repository list.
type Repo struct {
	Path      string    `json:"path"`
	Name      string    `json:"name"`
	Branch    string    `json:"branch"`
	Remote    string    `json:"remote,omitempty"`
	Head      string    `json:"head,omitempty"`
	Subject   string    `json:"subject,omitempty"`
	Author    string    `json:"author,omitempty"`
	CommitAt  time.Time `json:"commitAt,omitempty"`
	Dirty     bool      `json:"dirty"`
	Changes   int       `json:"changes"`
	Ahead     int       `json:"ahead"`
	Behind    int       `json:"behind"`
	Detached  bool      `json:"detached"`
	Untracked int       `json:"untracked"`
}

// Discover walks the configured roots looking for working trees.
//
// It stops descending once it finds one — a repository's own subdirectories
// are not separate repositories — and skips the directories that make a naive
// walk unusably slow on a real server.
func (s *Service) Discover(ctx context.Context) ([]Repo, error) {
	if !s.Available() {
		return nil, ErrNotInstalled
	}
	seen := map[string]bool{}
	var found []string
	skip := map[string]bool{
		"node_modules": true, ".cache": true, "vendor": true, "__pycache__": true,
		".venv": true, "venv": true, "target": true, ".next": true, "dist": true,
	}
	for _, root := range s.roots {
		const maxDepth = 5
		rootDepth := strings.Count(root, string(os.PathSeparator))
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || ctx.Err() != nil {
				return nil //nolint:nilerr // an unreadable subtree is skipped, not fatal
			}
			if !d.IsDir() {
				return nil
			}
			if strings.Count(path, string(os.PathSeparator))-rootDepth > maxDepth {
				return filepath.SkipDir
			}
			base := d.Name()
			if base != "." && strings.HasPrefix(base, ".") && base != ".git" || skip[base] {
				return filepath.SkipDir
			}
			if base == ".git" {
				repo := filepath.Dir(path)
				if !seen[repo] {
					seen[repo] = true
					found = append(found, repo)
				}
				return filepath.SkipDir
			}
			return nil
		})
	}
	sort.Strings(found)
	repos := make([]Repo, 0, len(found))
	for _, path := range found {
		r, err := s.Summary(ctx, path)
		if err != nil {
			continue
		}
		repos = append(repos, *r)
	}
	return repos, nil
}

// Summary reads the cheap facts about one repository.
// Toplevel reports the root of the repository containing a path, and an error
// if there is not one.
//
// Summary deliberately never fails — it fills what it can and returns a Repo
// for any directory — which makes it useless for the question "is this a
// checkout at all". A caller that wants to *link* to a repository needs both
// halves of the answer: whether one exists, and where its root is, since a
// subdirectory of a repo is not what the repository list is keyed by.
func (s *Service) Toplevel(ctx context.Context, path string) (string, error) {
	out, err := s.run(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	root := strings.TrimSpace(out)
	if root == "" {
		return "", errors.New("not a git repository")
	}
	return root, nil
}

func (s *Service) Summary(ctx context.Context, path string) (*Repo, error) {
	r := &Repo{Path: path, Name: filepath.Base(path)}

	if head, err := s.run(ctx, path, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		r.Branch = strings.TrimSpace(head)
		if r.Branch == "HEAD" {
			r.Detached = true
			if sha, err := s.run(ctx, path, "rev-parse", "--short", "HEAD"); err == nil {
				r.Branch = "detached at " + strings.TrimSpace(sha)
			}
		}
	}
	if sha, err := s.run(ctx, path, "rev-parse", "--short", "HEAD"); err == nil {
		r.Head = strings.TrimSpace(sha)
	}
	// %x1f is a unit separator: safe against subjects containing anything.
	if out, err := s.run(ctx, path, "log", "-1", "--pretty=format:%s%x1f%an%x1f%ct"); err == nil {
		parts := strings.Split(strings.TrimSpace(out), "\x1f")
		if len(parts) == 3 {
			r.Subject, r.Author = parts[0], parts[1]
			if secs, err := strconv.ParseInt(parts[2], 10, 64); err == nil {
				r.CommitAt = time.Unix(secs, 0).UTC()
			}
		}
	}
	if out, err := s.run(ctx, path, "remote", "get-url", "origin"); err == nil {
		r.Remote = scrubRemote(strings.TrimSpace(out))
	}
	if out, err := s.run(ctx, path, "status", "--porcelain=v1", "--untracked-files=normal"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line == "" {
				continue
			}
			r.Changes++
			if strings.HasPrefix(line, "??") {
				r.Untracked++
			}
		}
		r.Dirty = r.Changes > 0
	}
	if out, err := s.run(ctx, path, "rev-list", "--left-right", "--count", "@{upstream}...HEAD"); err == nil {
		fields := strings.Fields(strings.TrimSpace(out))
		if len(fields) == 2 {
			r.Behind, _ = strconv.Atoi(fields[0])
			r.Ahead, _ = strconv.Atoi(fields[1])
		}
	}
	return r, nil
}

// scrubRemote strips credentials that people sometimes embed in an HTTPS
// remote. The dashboard shows this string on a list page; a token in a URL is
// still a token.
func scrubRemote(url string) string {
	at := strings.LastIndex(url, "@")
	scheme := strings.Index(url, "://")
	if at > 0 && scheme > 0 && at > scheme {
		return url[:scheme+3] + "***@" + url[at+1:]
	}
	return url
}

// FileChange is one entry from `git status`.
type FileChange struct {
	Path     string `json:"path"`
	Index    string `json:"index"`
	Worktree string `json:"worktree"`
	Label    string `json:"label"`
	Staged   bool   `json:"staged"`
}

type Status struct {
	Repo    *Repo        `json:"repo"`
	Files   []FileChange `json:"files"`
	Clean   bool         `json:"clean"`
	Stashes int          `json:"stashes"`
}

var statusLabels = map[byte]string{
	'M': "modified", 'A': "added", 'D': "deleted", 'R': "renamed",
	'C': "copied", 'U': "conflicted", '?': "untracked", '!': "ignored",
}

func (s *Service) Status(ctx context.Context, path string) (*Status, error) {
	repo, err := s.Summary(ctx, path)
	if err != nil {
		return nil, err
	}
	st := &Status{Repo: repo, Files: []FileChange{}}
	out, err := s.run(ctx, path, "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if len(line) < 4 {
			continue
		}
		index, worktree := line[0], line[1]
		name := strings.TrimSpace(line[3:])
		// A rename reads "old -> new"; the new path is the useful one.
		if i := strings.Index(name, " -> "); i >= 0 {
			name = name[i+4:]
		}
		label := statusLabels[worktree]
		if label == "" || worktree == ' ' {
			label = statusLabels[index]
		}
		st.Files = append(st.Files, FileChange{
			Path:     strings.Trim(name, `"`),
			Index:    strings.TrimSpace(string(index)),
			Worktree: strings.TrimSpace(string(worktree)),
			Label:    label,
			Staged:   index != ' ' && index != '?',
		})
	}
	st.Clean = len(st.Files) == 0
	if out, err := s.run(ctx, path, "stash", "list"); err == nil {
		st.Stashes = len(nonEmptyLines(out))
	}
	return st, nil
}

type Commit struct {
	SHA     string    `json:"sha"`
	Short   string    `json:"short"`
	Subject string    `json:"subject"`
	Author  string    `json:"author"`
	Email   string    `json:"email"`
	At      time.Time `json:"at"`
	Refs    string    `json:"refs,omitempty"`
	Insert  int       `json:"insertions"`
	Delete  int       `json:"deletions"`
	Files   int       `json:"files"`
	IsMerge bool      `json:"isMerge"`
	Parents []string  `json:"parents,omitempty"`
}

// commitFields is the unit-separated pretty-format both Log and Graph read:
// hash, short hash, subject, author, email, commit time, ref decoration, parents.
const commitFields = "%H%x1f%h%x1f%s%x1f%an%x1f%ae%x1f%ct%x1f%D%x1f%P"

// parseCommitLine reads one commitFields line into a Commit. The boolean is
// false for a line that is not a commit record (a --shortstat line, blank).
func parseCommitLine(line string) (Commit, bool) {
	if !strings.Contains(line, "\x1f") {
		return Commit{}, false
	}
	p := strings.Split(line, "\x1f")
	if len(p) < 8 {
		return Commit{}, false
	}
	c := Commit{SHA: p[0], Short: p[1], Subject: p[2], Author: p[3], Email: p[4], Refs: p[6]}
	if secs, err := strconv.ParseInt(strings.TrimSpace(p[5]), 10, 64); err == nil {
		c.At = time.Unix(secs, 0).UTC()
	}
	c.Parents = strings.Fields(p[7])
	c.IsMerge = len(c.Parents) > 1
	return c, true
}

// Log reads history for a ref. limit is clamped, because this feeds a page.
func (s *Service) Log(ctx context.Context, path, ref string, limit int) ([]Commit, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []string{"log", "--max-count=" + strconv.Itoa(limit),
		"--pretty=format:" + commitFields, "--shortstat"}
	if ref != "" {
		if err := ValidateRef(ref); err != nil {
			return nil, err
		}
		args = append(args, ref)
	}
	// Everything after -- is a path, so a ref can never be read as an option.
	args = append(args, "--")
	out, err := s.run(ctx, path, args...)
	if err != nil {
		return nil, err
	}
	commits := []Commit{}
	var cur *Commit
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if c, ok := parseCommitLine(line); ok {
			if cur != nil {
				commits = append(commits, *cur)
			}
			cur = &c
			continue
		}
		// --shortstat emits " 3 files changed, 10 insertions(+), 2 deletions(-)",
		// omitting any clause that is zero, so read it as number/noun pairs
		// rather than by position.
		if cur != nil && strings.Contains(line, "changed") {
			fields := strings.Fields(line)
			for i := 0; i+1 < len(fields); i++ {
				n, err := strconv.Atoi(fields[i])
				if err != nil {
					continue
				}
				switch noun := fields[i+1]; {
				case strings.HasPrefix(noun, "file"):
					cur.Files = n
				case strings.HasPrefix(noun, "insertion"):
					cur.Insert = n
				case strings.HasPrefix(noun, "deletion"):
					cur.Delete = n
				}
			}
		}
	}
	if cur != nil {
		commits = append(commits, *cur)
	}
	return commits, nil
}

type Branch struct {
	Name     string    `json:"name"`
	Current  bool      `json:"current"`
	Remote   bool      `json:"remote"`
	Upstream string    `json:"upstream,omitempty"`
	Head     string    `json:"head,omitempty"`
	Subject  string    `json:"subject,omitempty"`
	At       time.Time `json:"at,omitempty"`
	Ahead    int       `json:"ahead"`
	Behind   int       `json:"behind"`
	// Worktree is the path of another worktree that has this branch checked
	// out, empty otherwise. git refuses to delete or switch such a branch even
	// with -D, so the UI disables those actions and shows this as the reason.
	Worktree string `json:"worktree,omitempty"`
}

func (s *Service) Branches(ctx context.Context, path string) ([]Branch, error) {
	// The full refname is what says whether a branch is local or remote —
	// a short name cannot, because a local branch is perfectly entitled to
	// contain a slash ("fix/thing" is not a remote).
	format := strings.Join([]string{
		"%(refname:short)", "%(HEAD)", "%(upstream:short)", "%(objectname:short)",
		"%(contents:subject)", "%(committerdate:unix)", "%(upstream:track)", "%(refname)",
		"%(symref)", "%(worktreepath)",
	}, "%1f")
	out, err := s.run(ctx, path, "for-each-ref", "--format="+format, "refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	branches := []Branch{}
	for _, line := range nonEmptyLines(out) {
		p := strings.Split(line, "\x1f")
		if len(p) < 10 {
			continue
		}
		// refs/remotes/<remote>/HEAD is a symbolic ref pointing at the remote's
		// default branch, not a branch of its own — listing it puts a phantom
		// entry named after the remote next to the real ones.
		if strings.TrimSpace(p[8]) != "" {
			continue
		}
		b := Branch{
			Name: p[0], Current: strings.TrimSpace(p[1]) == "*",
			Upstream: p[2], Head: p[3], Subject: p[4],
			Remote: strings.HasPrefix(p[7], "refs/remotes/"),
		}
		// %(worktreepath) is set for the current worktree too; only another
		// one's checkout is a constraint the operator needs shown.
		if wt := strings.TrimSpace(p[9]); wt != "" && !b.Current {
			b.Worktree = wt
		}
		if secs, err := strconv.ParseInt(strings.TrimSpace(p[5]), 10, 64); err == nil {
			b.At = time.Unix(secs, 0).UTC()
		}
		// "[ahead 2, behind 1]"
		track := p[6]
		b.Ahead = trackCount(track, "ahead ")
		b.Behind = trackCount(track, "behind ")
		branches = append(branches, b)
	}
	sort.Slice(branches, func(i, j int) bool {
		if branches[i].Current != branches[j].Current {
			return branches[i].Current
		}
		if branches[i].Remote != branches[j].Remote {
			return !branches[i].Remote
		}
		return branches[i].At.After(branches[j].At)
	})
	return branches, nil
}

func trackCount(track, key string) int {
	i := strings.Index(track, key)
	if i < 0 {
		return 0
	}
	rest := track[i+len(key):]
	end := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' })
	if end < 0 {
		end = len(rest)
	}
	n, _ := strconv.Atoi(rest[:end])
	return n
}

// Diff returns a unified diff. ref selects a commit; file narrows it.
// Output is capped, because a vendored lockfile can be megabytes and this is
// going into a browser.
func (s *Service) Diff(ctx context.Context, path, ref, file string, staged bool) (string, error) {
	args := []string{"--no-pager"}
	if ref != "" {
		if err := ValidateRef(ref); err != nil {
			return "", err
		}
		args = append(args, "show", "--format=fuller", ref)
	} else {
		args = append(args, "diff")
		if staged {
			args = append(args, "--cached")
		}
	}
	args = append(args, "--")
	if file != "" {
		// After -- this is unambiguously a path, so it needs no ref rules;
		// it does need to stay inside the repository.
		if strings.Contains(file, "..") || strings.HasPrefix(file, "/") {
			return "", fmt.Errorf("%w: %q", ErrInvalidRef, file)
		}
		args = append(args, file)
	}
	out, err := s.run(ctx, path, args...)
	if err != nil {
		return "", err
	}
	const maxDiff = 400 * 1024
	if len(out) > maxDiff {
		return out[:maxDiff] + "\n\n… diff truncated at 400 KB …", nil
	}
	return out, nil
}

func (s *Service) run(ctx context.Context, dir string, args ...string) (string, error) {
	return s.runTimeout(ctx, dir, 30*time.Second, args...)
}

func (s *Service) runTimeout(ctx context.Context, dir string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	// A prompt would hang the request forever: fail instead of asking. The
	// terminal variable covers ssh, the git one covers credential helpers.
	env := append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_PAGER=cat",
		"LC_ALL=C",
	)
	cmd.Env = env
	// A server's repositories usually belong to a service or login account,
	// not to root. Running as that owner means git's "dubious ownership"
	// refusal never arises, a fetch or checkout leaves correctly-owned files
	// behind, and an ssh remote finds that account's keys rather than root's.
	hostexec.AsOwner(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func nonEmptyLines(s string) []string {
	out := []string{}
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
