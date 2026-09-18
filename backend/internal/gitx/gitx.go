// Package gitx exposes the read side of the git repositories on a server:
// which ones exist, what state each working tree is in, and what its history
// and branches look like.
//
// Everything here runs `git` with an explicit argument vector — never through
// a shell — and every value that reaches an argument is either validated
// against a strict pattern or passed after an explicit `--` separator, so a
// branch called `--upload-pack=…` cannot turn into an option.
//
// Read and write are deliberately separated: this file and refs.go answer
// questions, while mutating operations live in ops.go behind their own
// capability check.
package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
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
// read as an option or a path traversal. `~` and `^` are the two pieces of
// revision syntax worth having — HEAD~1, main^ — and neither can start an
// option or leave the repository.
var safeRefChars = func(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return strings.ContainsRune("._-/@+~^", r)
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

// validatePath refuses a working-tree path that could climb out of the
// repository or be read as an option. It is judged by segment rather than by
// substring: `v1..v2.diff` is an ordinary file name, and refusing every path
// containing two dots made such a file impossible to stage, diff or discard.
// The `--` separator every caller adds already stops a leading dash from
// becoming a flag; the check here is the belt to that brace.
func validatePath(file string) error {
	if file == "" || len(file) > 4096 || strings.HasPrefix(file, "/") ||
		strings.HasPrefix(file, "-") || strings.ContainsRune(file, 0) {
		return fmt.Errorf("%w: %q", ErrInvalidRef, file)
	}
	for _, seg := range strings.Split(file, "/") {
		if seg == ".." {
			return fmt.Errorf("%w: %q", ErrInvalidRef, file)
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

// Roots reports the configured roots that exist on this host, for the page
// that offers to clone a repository into one of them.
func (s *Service) Roots() []string {
	out := []string{}
	for _, root := range s.roots {
		if fi, err := os.Stat(root); err == nil && fi.IsDir() {
			out = append(out, root)
		}
	}
	return out
}

// Resolve checks that a path is a git repository inside the configured roots.
// Both halves matter: the roots are the boundary an operator configured, and
// symlinks are resolved first so a link cannot point out of them.
func (s *Service) Resolve(path string) (string, error) {
	real, err := s.ResolveDir(path)
	if err != nil {
		return "", err
	}
	if fi, err := os.Stat(filepath.Join(real, ".git")); err != nil || (!fi.IsDir() && !fi.Mode().IsRegular()) {
		return "", fmt.Errorf("%w: %s", ErrNotARepo, path)
	}
	return real, nil
}

// ResolveDir is the containment half of Resolve on its own: an existing
// directory inside the roots, whether or not it is a checkout yet. Cloning
// and initialising need exactly that — a place that is inside the boundary
// and is not a repository.
func (s *Service) ResolveDir(path string) (string, error) {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return "", fmt.Errorf("%w: %s", ErrOutsideRoots, path)
	}
	real, err := filepath.EvalSymlinks(clean)
	if err != nil {
		real = clean
	}
	if !s.within(real) {
		return "", fmt.Errorf("%w: %s", ErrOutsideRoots, path)
	}
	if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("%w: %s is not a directory", ErrOutsideRoots, path)
	}
	return real, nil
}

func (s *Service) within(real string) bool {
	for _, root := range s.roots {
		if real == root || strings.HasPrefix(real, root+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// Repo is the summary shown in the repository list.
type Repo struct {
	Path     string    `json:"path"`
	Name     string    `json:"name"`
	Branch   string    `json:"branch"`
	Remote   string    `json:"remote,omitempty"`
	Upstream string    `json:"upstream,omitempty"`
	Head     string    `json:"head,omitempty"`
	Subject  string    `json:"subject,omitempty"`
	Author   string    `json:"author,omitempty"`
	CommitAt time.Time `json:"commitAt,omitempty"`
	Dirty    bool      `json:"dirty"`
	Changes  int       `json:"changes"`
	// Staged, Untracked and Conflicts break the change count down: a list can
	// then say "3 staged" or "2 conflicts" rather than only "5 changes".
	Staged    int  `json:"staged"`
	Untracked int  `json:"untracked"`
	Conflicts int  `json:"conflicts"`
	Ahead     int  `json:"ahead"`
	Behind    int  `json:"behind"`
	Detached  bool `json:"detached"`
	// Gone is an upstream that is configured but no longer exists on the
	// remote — the state a branch is left in after its pull request merged
	// and the remote branch was deleted.
	Gone bool `json:"gone,omitempty"`
	// Empty is a repository with no commits yet: HEAD names a branch that has
	// nothing on it.
	Empty bool `json:"empty,omitempty"`
}

// Discover walks the configured roots looking for working trees.
//
// It stops descending once it finds one — a repository's own subdirectories
// are not separate repositories — and skips the directories that make a naive
// walk unusably slow on a real server. A `.git` *file* counts as much as a
// `.git` directory: that is what a linked worktree and a submodule checkout
// carry, and both are repositories an operator works in.
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
	record := func(repo string) {
		if !seen[repo] {
			seen[repo] = true
			found = append(found, repo)
		}
	}
	for _, root := range s.roots {
		const maxDepth = 5
		rootDepth := strings.Count(root, string(os.PathSeparator))
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || ctx.Err() != nil {
				return nil //nolint:nilerr // an unreadable subtree is skipped, not fatal
			}
			if !d.IsDir() {
				if d.Name() == ".git" {
					record(filepath.Dir(path))
					// SkipDir from a file entry ends the containing directory's
					// walk, which is exactly the stop-descending rule above.
					return filepath.SkipDir
				}
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
				record(filepath.Dir(path))
				return filepath.SkipDir
			}
			return nil
		})
	}
	sort.Strings(found)

	// A summary is half a dozen git invocations, and a server with thirty
	// checkouts was spending two seconds answering a list that polls. Four at
	// a time keeps the total under the poll interval without turning a
	// rescan into a load spike.
	repos := make([]*Repo, len(found))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, path := range found {
		wg.Add(1)
		go func(i int, path string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if r, err := s.Summary(ctx, path); err == nil {
				repos[i] = r
			}
		}(i, path)
	}
	wg.Wait()
	out := make([]Repo, 0, len(repos))
	for _, r := range repos {
		if r != nil {
			out = append(out, *r)
		}
	}
	return out, nil
}

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

// Summary reads the cheap facts about one repository.
func (s *Service) Summary(ctx context.Context, path string) (*Repo, error) {
	r := &Repo{Path: path, Name: filepath.Base(path)}

	// symbolic-ref answers with the branch HEAD is on even before the first
	// commit exists, where rev-parse has nothing to abbreviate; it fails only
	// when HEAD is detached. A fresh `git init` used to show no branch at all.
	if out, err := s.run(ctx, path, "symbolic-ref", "--short", "-q", "HEAD"); err == nil {
		r.Branch = strings.TrimSpace(out)
	} else {
		r.Detached = true
		if sha, err := s.run(ctx, path, "rev-parse", "--short", "HEAD"); err == nil {
			r.Branch = "detached at " + strings.TrimSpace(sha)
		}
	}
	if sha, err := s.run(ctx, path, "rev-parse", "--short", "HEAD"); err == nil {
		r.Head = strings.TrimSpace(sha)
	} else {
		r.Empty = true
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
	if out, err := s.run(ctx, path, "status", "--porcelain=v1", "-z", "--untracked-files=normal"); err == nil {
		for _, f := range parseStatus(out) {
			r.Changes++
			if f.Staged {
				r.Staged++
			}
			if f.Label == "untracked" {
				r.Untracked++
			}
			if f.Label == "conflicted" {
				r.Conflicts++
			}
		}
		r.Dirty = r.Changes > 0
	}
	if !r.Detached && r.Branch != "" {
		// The configured upstream and whether it still exists, in one call:
		// %(upstream:track) reads "[gone]" for a remote branch that was
		// deleted, which rev-list's "no upstream" error could not distinguish
		// from a branch that never had one.
		format := "%(upstream:short)%1f%(upstream:track)"
		if out, err := s.run(ctx, path, "for-each-ref", "--format="+format, "refs/heads/"+r.Branch); err == nil {
			parts := strings.Split(strings.TrimSpace(out), "\x1f")
			if len(parts) == 2 {
				r.Upstream = parts[0]
				r.Gone = strings.Contains(parts[1], "gone")
			}
		}
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
//
// A file can be changed on both sides at once — staged, then edited again —
// and it is one entry here with both Staged and Unstaged set, so a client
// lists it under both headings rather than hiding the second edit behind the
// first. The status letters say which side is which.
type FileChange struct {
	Path     string `json:"path"`
	Index    string `json:"index"`
	Worktree string `json:"worktree"`
	Label    string `json:"label"`
	Staged   bool   `json:"staged"`
	Unstaged bool   `json:"unstaged"`
	// From is the previous name of a renamed or copied path.
	From string `json:"from,omitempty"`
}

// Identity is who a commit made here would be recorded as.
type Identity struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

type Status struct {
	Repo     *Repo        `json:"repo"`
	Files    []FileChange `json:"files"`
	Clean    bool         `json:"clean"`
	Stashes  int          `json:"stashes"`
	Identity Identity     `json:"identity"`
	// Operation is a merge, rebase, revert or cherry-pick that stopped
	// halfway — the state this dashboard never creates itself, but which a
	// shell can leave behind and which makes every commit here fail until it
	// is finished or abandoned.
	Operation string `json:"operation,omitempty"`
}

var statusLabels = map[byte]string{
	'M': "modified", 'A': "added", 'D': "deleted", 'R': "renamed",
	'C': "copied", 'U': "conflicted", '?': "untracked", '!': "ignored",
	'T': "modified",
}

// unmerged is every XY code git documents for a path with merge conflicts.
// Two of them — AA and DD — carry no U at all, and reading them letter by
// letter filed a both-added conflict under "ready to commit", where the
// commit it promised could not happen.
var unmerged = map[string]bool{
	"DD": true, "AU": true, "UD": true, "UA": true, "DU": true, "AA": true, "UU": true,
}

// parseStatus reads `git status --porcelain=v1 -z`.
//
// The NUL-separated form is the one whose paths are literal. Without -z git
// quotes any path with a byte outside ASCII — a file named café.txt arrived
// as "caf\303\251.txt", which the page showed verbatim and then could not
// stage, because no such file exists. A rename record is followed by a
// second field carrying the original path.
func parseStatus(out string) []FileChange {
	files := []FileChange{}
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		entry := fields[i]
		if len(entry) < 4 {
			continue
		}
		index, worktree := entry[0], entry[1]
		f := FileChange{
			Path:     entry[3:],
			Index:    strings.TrimSpace(string(index)),
			Worktree: strings.TrimSpace(string(worktree)),
		}
		if index == 'R' || index == 'C' || worktree == 'R' || worktree == 'C' {
			if i+1 < len(fields) {
				i++
				f.From = fields[i]
			}
		}
		switch {
		case unmerged[entry[:2]]:
			f.Label = "conflicted"
			f.Unstaged = true
		case index == '?':
			f.Label = "untracked"
			f.Unstaged = true
		default:
			f.Label = statusLabels[worktree]
			if f.Label == "" || worktree == ' ' {
				f.Label = statusLabels[index]
			}
			f.Staged = index != ' '
			f.Unstaged = worktree != ' '
		}
		files = append(files, f)
	}
	return files
}

func (s *Service) Status(ctx context.Context, path string) (*Status, error) {
	repo, err := s.Summary(ctx, path)
	if err != nil {
		return nil, err
	}
	st := &Status{Repo: repo, Files: []FileChange{}}
	out, err := s.run(ctx, path, "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil {
		return nil, err
	}
	st.Files = parseStatus(out)
	st.Clean = len(st.Files) == 0
	if out, err := s.run(ctx, path, "stash", "list"); err == nil {
		st.Stashes = len(nonEmptyLines(out))
	}
	st.Identity = s.identity(ctx, path)
	st.Operation = s.operationInProgress(ctx, path)
	return st, nil
}

// identity reads the committer git would record here, repository config
// winning over global exactly as git resolves it.
func (s *Service) identity(ctx context.Context, path string) Identity {
	var id Identity
	if out, err := s.run(ctx, path, "config", "--get", "user.name"); err == nil {
		id.Name = strings.TrimSpace(out)
	}
	if out, err := s.run(ctx, path, "config", "--get", "user.email"); err == nil {
		id.Email = strings.TrimSpace(out)
	}
	return id
}

// operationInProgress names the git operation a shell left half-finished, by
// the marker files git itself leaves in the git directory.
func (s *Service) operationInProgress(ctx context.Context, path string) string {
	out, err := s.run(ctx, path, "rev-parse", "--git-dir")
	if err != nil {
		return ""
	}
	gitDir := strings.TrimSpace(out)
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(path, gitDir)
	}
	for _, m := range []struct{ marker, op string }{
		{"MERGE_HEAD", "merge"},
		{"CHERRY_PICK_HEAD", "cherry-pick"},
		{"REVERT_HEAD", "revert"},
		{"rebase-merge", "rebase"},
		{"rebase-apply", "rebase"},
		{"BISECT_LOG", "bisect"},
	} {
		if _, err := os.Stat(filepath.Join(gitDir, m.marker)); err == nil {
			return m.op
		}
	}
	return ""
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

// LogQuery narrows a history read. Every field is optional.
type LogQuery struct {
	// Ref is the tip to walk back from; empty means HEAD.
	Ref string
	// Limit is clamped, because this feeds a page; Skip is how the page asks
	// for the next one.
	Limit, Skip int
	// Search matches the subject and body, literally and case-insensitively.
	Search string
	// File narrows the history to commits touching one path, following it
	// across renames.
	File string
	// Author matches the author name or address, as a substring.
	Author string
}

// Log reads history for a ref.
func (s *Service) Log(ctx context.Context, path string, q LogQuery) ([]Commit, error) {
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []string{"log", "--max-count=" + strconv.Itoa(limit),
		"--pretty=format:" + commitFields, "--shortstat"}
	if q.Skip > 0 {
		args = append(args, "--skip="+strconv.Itoa(q.Skip))
	}
	// The search terms travel inside a single --grep= argument, so a term
	// beginning with a dash is a term rather than an option; --fixed-strings
	// keeps a term containing regex punctuation literal.
	if term := strings.TrimSpace(q.Search); term != "" {
		args = append(args, "--fixed-strings", "--regexp-ignore-case", "--grep="+term)
	}
	if author := strings.TrimSpace(q.Author); author != "" {
		args = append(args, "--fixed-strings", "--regexp-ignore-case", "--author="+author)
	}
	if q.Ref != "" {
		if err := ValidateRef(q.Ref); err != nil {
			return nil, err
		}
		args = append(args, q.Ref)
	}
	// Everything after -- is a path, so a ref can never be read as an option.
	args = append(args, "--")
	if q.File != "" {
		if err := validatePath(q.File); err != nil {
			return nil, err
		}
		// --follow wants exactly one path, which is what a file history is.
		args = append(args[:1], append([]string{"--follow"}, args[1:]...)...)
		args = append(args, q.File)
	}
	out, err := s.run(ctx, path, args...)
	if err != nil {
		return nil, err
	}
	return parseLog(out), nil
}

// parseLog reads commitFields records interleaved with --shortstat lines.
func parseLog(out string) []Commit {
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
		if cur != nil && strings.Contains(line, "changed") {
			cur.Files, cur.Insert, cur.Delete = parseShortstat(line)
		}
	}
	if cur != nil {
		commits = append(commits, *cur)
	}
	return commits
}

// parseShortstat reads " 3 files changed, 10 insertions(+), 2 deletions(-)".
// git omits any clause that is zero, so the line is read as number/noun pairs
// rather than by position.
func parseShortstat(line string) (files, insertions, deletions int) {
	fields := strings.Fields(line)
	for i := 0; i+1 < len(fields); i++ {
		n, err := strconv.Atoi(fields[i])
		if err != nil {
			continue
		}
		switch noun := fields[i+1]; {
		case strings.HasPrefix(noun, "file"):
			files = n
		case strings.HasPrefix(noun, "insertion"):
			insertions = n
		case strings.HasPrefix(noun, "deletion"):
			deletions = n
		}
	}
	return files, insertions, deletions
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
	// Gone is a local branch whose upstream no longer exists on the remote.
	Gone bool `json:"gone,omitempty"`
	// Merged is a local branch whose every commit is already reachable from
	// HEAD — the one it is safe to delete without losing anything.
	Merged bool `json:"merged,omitempty"`
	// RemoteName and Local split a remote-tracking branch's name into the
	// remote it belongs to and the name a local checkout of it would take.
	RemoteName string `json:"remoteName,omitempty"`
	Local      string `json:"local,omitempty"`
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
	// Which local branches HEAD already contains. A failure here — an empty
	// repository has no HEAD to merge into — costs the annotation, not the list.
	merged := map[string]bool{}
	if out, err := s.run(ctx, path, "for-each-ref", "--merged=HEAD", "--format=%(refname)", "refs/heads"); err == nil {
		for _, line := range nonEmptyLines(out) {
			merged[strings.TrimSpace(line)] = true
		}
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
		if b.Remote {
			if remote, local, ok := strings.Cut(strings.TrimPrefix(p[7], "refs/remotes/"), "/"); ok {
				b.RemoteName, b.Local = remote, local
			}
		} else {
			b.Merged = merged[p[7]]
		}
		// %(worktreepath) is set for the current worktree too; only another
		// one's checkout is a constraint the operator needs shown.
		if wt := strings.TrimSpace(p[9]); wt != "" && !b.Current {
			b.Worktree = wt
		}
		if secs, err := strconv.ParseInt(strings.TrimSpace(p[5]), 10, 64); err == nil {
			b.At = time.Unix(secs, 0).UTC()
		}
		// "[ahead 2, behind 1]", or "[gone]"
		track := p[6]
		b.Ahead = trackCount(track, "ahead ")
		b.Behind = trackCount(track, "behind ")
		b.Gone = strings.Contains(track, "gone")
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
//
// A commit's diff is taken against its first parent, for a merge as much as
// for an ordinary commit: `git show` draws a merge as a combined diff, whose
// two-column markers no viewer here reads, and "what did this merge bring
// in" is the first-parent diff anyway. `--format=` keeps the commit header
// out — the page draws that itself from the commit's record.
func (s *Service) Diff(ctx context.Context, path, ref, file string, staged bool) (string, error) {
	args := []string{"--no-pager"}
	if ref != "" {
		if err := ValidateRef(ref); err != nil {
			return "", err
		}
		args = append(args, "show", "--format=", "-m", "--first-parent", "-M", ref)
	} else {
		args = append(args, "diff", "-M")
		if staged {
			args = append(args, "--cached")
		}
	}
	args = append(args, "--")
	if file != "" {
		// After -- this is unambiguously a path, so it needs no ref rules;
		// it does need to stay inside the repository.
		if err := validatePath(file); err != nil {
			return "", err
		}
		args = append(args, file)
	}
	out, err := s.run(ctx, path, args...)
	if err != nil {
		return "", err
	}
	if out == "" && ref == "" && !staged && file != "" {
		out = s.diffUntracked(ctx, path, file)
	}
	return capDiff(out), nil
}

const maxDiff = 400 * 1024

// capDiff truncates a diff at a line boundary, so the last line shown is a
// whole one rather than half a line and half a multibyte character.
func capDiff(out string) string {
	if len(out) <= maxDiff {
		return out
	}
	cut := maxDiff
	if nl := strings.LastIndexByte(out[:maxDiff], '\n'); nl > 0 {
		cut = nl
	}
	return out[:cut] + "\n\n… diff truncated at 400 KB …"
}

// diffUntracked shows a file git does not track yet as the addition it is
// about to be.
//
// `git diff` has nothing to say about an untracked file — there is no earlier
// version to compare against — so the change list offered a diff of exactly
// the files whose every line is new and then showed "no changes" for them.
// `--no-index` against /dev/null produces the ordinary new-file diff, and
// `ls-files --others` is what turns a directory, which status lists as a single
// untracked entry, into the files inside it. The output is capped by the
// caller the same way a tracked diff is.
func (s *Service) diffUntracked(ctx context.Context, path, file string) string {
	listed, err := s.run(ctx, path, "ls-files", "--others", "--exclude-standard", "-z", "--", file)
	if err != nil {
		return ""
	}
	var out strings.Builder
	for _, name := range strings.Split(strings.TrimRight(listed, "\x00"), "\x00") {
		if name == "" {
			continue
		}
		// --no-index exits 1 whenever the two sides differ, which against
		// /dev/null they always do.
		diff, err := s.runAllowing(ctx, path, 1, "--no-pager", "diff", "--no-index", "--", "/dev/null", name)
		if err != nil {
			continue
		}
		out.WriteString(diff)
		if out.Len() > maxDiff {
			break
		}
	}
	return out.String()
}

func (s *Service) run(ctx context.Context, dir string, args ...string) (string, error) {
	return s.runTimeout(ctx, dir, 30*time.Second, args...)
}

func (s *Service) runTimeout(ctx context.Context, dir string, timeout time.Duration, args ...string) (string, error) {
	out, err := s.execute(ctx, dir, timeout, args...)
	if err != nil {
		return string(out), fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// runAllowing is run for the git commands whose non-zero exit is an answer
// rather than a failure: `diff --no-index` reports "these differ" as 1.
func (s *Service) runAllowing(ctx context.Context, dir string, exit int, args ...string) (string, error) {
	out, err := s.execute(ctx, dir, 30*time.Second, args...)
	var exited *exec.ExitError
	if err != nil && !(errors.As(err, &exited) && exited.ExitCode() == exit) {
		return "", fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (s *Service) execute(ctx context.Context, dir string, timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	// A prompt would hang the request forever: fail instead of asking. The
	// terminal variable covers ssh, the git one covers credential helpers,
	// and GIT_EDITOR=true is the editor that accepts whatever git proposes —
	// a merge or revert message — without a terminal to open one in.
	//
	// GIT_OPTIONAL_LOCKS=0 is what lets this page poll. A plain `git status`
	// takes the index lock to refresh stat information as a courtesy, and a
	// dashboard polling every few seconds was racing every commit typed in a
	// terminal for `.git/index.lock` — "Unable to create index.lock: File
	// exists" at the shell, from a page that was only reading.
	env := append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		"GIT_EDITOR=true",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_PAGER=cat",
		"LC_ALL=C",
	)
	cmd.Env = env
	// A server's repositories usually belong to a service or login account,
	// not to root. Running as that owner means git's "dubious ownership"
	// refusal never arises, a fetch or checkout leaves correctly-owned files
	// behind, and an ssh remote finds that account's keys rather than root's.
	hostexec.AsOwner(cmd)
	return cmd.CombinedOutput()
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
