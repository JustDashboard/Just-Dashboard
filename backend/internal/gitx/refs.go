package gitx

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The other things a repository holds besides its branches and its working
// tree: tags, stashes, remotes, and the detail behind one commit. All reads;
// the operations that change them are in ops.go.

// Tag is one tag, whether annotated or lightweight.
type Tag struct {
	Name string `json:"name"`
	// Commit is the commit the tag points at — through the tag object, for an
	// annotated one.
	Commit    string    `json:"commit,omitempty"`
	At        time.Time `json:"at,omitempty"`
	Message   string    `json:"message,omitempty"`
	Annotated bool      `json:"annotated"`
	Tagger    string    `json:"tagger,omitempty"`
}

// Tags lists every tag, newest first.
func (s *Service) Tags(ctx context.Context, path string) ([]Tag, error) {
	format := strings.Join([]string{
		"%(refname:short)", "%(objectname:short)", "%(*objectname:short)",
		"%(creatordate:unix)", "%(contents:subject)", "%(objecttype)", "%(taggername)",
	}, "%1f")
	out, err := s.run(ctx, path, "for-each-ref", "--sort=-creatordate", "--count=500",
		"--format="+format, "refs/tags")
	if err != nil {
		return nil, err
	}
	tags := []Tag{}
	for _, line := range nonEmptyLines(out) {
		p := strings.Split(line, "\x1f")
		if len(p) < 7 {
			continue
		}
		t := Tag{Name: p[0], Commit: p[1], Message: p[4], Tagger: strings.TrimSpace(p[6])}
		if p[5] == "tag" {
			t.Annotated = true
			t.Commit = p[2]
		}
		if secs, err := strconv.ParseInt(strings.TrimSpace(p[3]), 10, 64); err == nil {
			t.At = time.Unix(secs, 0).UTC()
		}
		tags = append(tags, t)
	}
	return tags, nil
}

// Stash is one entry of the stash list.
type Stash struct {
	// Index is the N in stash@{N}; it is what every stash operation takes,
	// because the braces in the ref itself are exactly what ValidateRef
	// refuses.
	Index   int       `json:"index"`
	SHA     string    `json:"sha"`
	At      time.Time `json:"at,omitempty"`
	Branch  string    `json:"branch,omitempty"`
	Message string    `json:"message"`
}

// Stashes lists what has been set aside, newest first.
func (s *Service) Stashes(ctx context.Context, path string) ([]Stash, error) {
	out, err := s.run(ctx, path, "stash", "list", "--format=%gd%x1f%H%x1f%ct%x1f%gs")
	if err != nil {
		return nil, err
	}
	stashes := []Stash{}
	for _, line := range nonEmptyLines(out) {
		p := strings.Split(line, "\x1f")
		if len(p) < 4 {
			continue
		}
		st := Stash{SHA: p[1]}
		// "stash@{3}"
		if open := strings.Index(p[0], "{"); open >= 0 && strings.HasSuffix(p[0], "}") {
			st.Index, _ = strconv.Atoi(p[0][open+1 : len(p[0])-1])
		}
		if secs, err := strconv.ParseInt(strings.TrimSpace(p[2]), 10, 64); err == nil {
			st.At = time.Unix(secs, 0).UTC()
		}
		// git writes "WIP on main: abc123 subject" for an unnamed stash and
		// "On main: message" for a named one.
		st.Branch, st.Message = splitStashSubject(p[3])
		stashes = append(stashes, st)
	}
	return stashes, nil
}

func splitStashSubject(subject string) (branch, message string) {
	rest := subject
	switch {
	case strings.HasPrefix(rest, "WIP on "):
		rest = strings.TrimPrefix(rest, "WIP on ")
	case strings.HasPrefix(rest, "On "):
		rest = strings.TrimPrefix(rest, "On ")
	default:
		return "", subject
	}
	branch, message, ok := strings.Cut(rest, ": ")
	if !ok {
		return "", subject
	}
	return branch, message
}

// StashDiff is what one stash holds, as a diff against the commit it was
// taken on.
func (s *Service) StashDiff(ctx context.Context, path string, index int) (string, error) {
	ref, err := stashRef(index)
	if err != nil {
		return "", err
	}
	// --include-untracked arrived in git 2.32; an older git refuses the
	// option, and then the tracked half is still worth showing.
	out, err := s.run(ctx, path, "--no-pager", "stash", "show", "-p", "--include-untracked", ref)
	if err != nil {
		out, err = s.run(ctx, path, "--no-pager", "stash", "show", "-p", ref)
		if err != nil {
			return "", err
		}
	}
	return capDiff(out), nil
}

// stashRef turns an index into the stash@{N} form git wants, which is built
// here from an integer precisely so nothing brace-shaped is ever accepted from
// a request.
func stashRef(index int) (string, error) {
	if index < 0 || index > 10000 {
		return "", fmt.Errorf("%w: stash index %d", ErrInvalidRef, index)
	}
	return "stash@{" + strconv.Itoa(index) + "}", nil
}

// Remote is one configured remote, its URLs scrubbed of credentials.
type Remote struct {
	Name     string `json:"name"`
	FetchURL string `json:"fetchUrl"`
	PushURL  string `json:"pushUrl,omitempty"`
}

func (s *Service) Remotes(ctx context.Context, path string) ([]Remote, error) {
	out, err := s.run(ctx, path, "remote", "-v")
	if err != nil {
		return nil, err
	}
	byName := map[string]*Remote{}
	order := []string{}
	for _, line := range nonEmptyLines(out) {
		// "origin\thttps://… (fetch)"
		name, rest, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		url, kind, _ := strings.Cut(rest, " ")
		r := byName[name]
		if r == nil {
			r = &Remote{Name: name}
			byName[name] = r
			order = append(order, name)
		}
		if strings.Contains(kind, "push") {
			r.PushURL = scrubRemote(url)
		} else {
			r.FetchURL = scrubRemote(url)
		}
	}
	remotes := make([]Remote, 0, len(order))
	for _, name := range order {
		r := byName[name]
		if r.PushURL == r.FetchURL {
			r.PushURL = ""
		}
		remotes = append(remotes, *r)
	}
	return remotes, nil
}

// ChangedFile is one path a commit touched.
type ChangedFile struct {
	Path string `json:"path"`
	// From is the earlier name, for a rename or copy.
	From       string `json:"from,omitempty"`
	Status     string `json:"status"`
	Insertions int    `json:"insertions"`
	Deletions  int    `json:"deletions"`
	Binary     bool   `json:"binary,omitempty"`
}

// CommitDetail is one commit with its message and the list of what it
// changed — the page a forge shows for a commit, without the diff itself,
// which is fetched per file.
type CommitDetail struct {
	Commit
	Body        string        `json:"body,omitempty"`
	Committer   string        `json:"committer,omitempty"`
	CommittedAt time.Time     `json:"committedAt,omitempty"`
	ChangedFile []ChangedFile `json:"changes"`
}

var changeLabels = map[byte]string{
	'A': "added", 'M': "modified", 'D': "deleted", 'R': "renamed", 'C': "copied", 'T': "modified",
}

// Show reads one commit's record, message and file list.
func (s *Service) Show(ctx context.Context, path, ref string) (*CommitDetail, error) {
	if err := ValidateRef(ref); err != nil {
		return nil, err
	}
	// %at/%cn/%ct are extra to commitFields; %b is the body, kept last since
	// it may contain anything, newlines included.
	out, err := s.run(ctx, path, "show", "--no-patch",
		"--format="+commitFields+"%x1f%cn%x1f%ct%x1f%b", ref, "--")
	if err != nil {
		return nil, err
	}
	p := strings.SplitN(strings.TrimSpace(out), "\x1f", 11)
	if len(p) < 11 {
		return nil, fmt.Errorf("git show: unexpected record for %s", ref)
	}
	c, ok := parseCommitLine(strings.Join(p[:8], "\x1f"))
	if !ok {
		return nil, fmt.Errorf("git show: unexpected record for %s", ref)
	}
	detail := &CommitDetail{Commit: c, Committer: p[8], Body: strings.TrimSpace(p[10])}
	if secs, err := strconv.ParseInt(strings.TrimSpace(p[9]), 10, 64); err == nil {
		detail.CommittedAt = time.Unix(secs, 0).UTC()
	}
	detail.ChangedFile, err = s.changedFiles(ctx, path, c)
	if err != nil {
		return nil, err
	}
	for _, f := range detail.ChangedFile {
		detail.Files++
		detail.Insert += f.Insertions
		detail.Delete += f.Deletions
	}
	return detail, nil
}

// changedFiles lists what a commit changed against its first parent, with a
// status and line counts per path. Two NUL-separated reads — one for the
// status letters, one for the counts — because neither format carries both.
func (s *Service) changedFiles(ctx context.Context, path string, c Commit) ([]ChangedFile, error) {
	// A root commit has nothing to diff against but the empty tree, which is
	// what --root asks diff-tree for; every other commit diffs against its
	// first parent so a merge reads as what it brought in.
	base := func(extra ...string) []string {
		args := []string{"diff-tree", "--root", "-r", "--no-commit-id", "-M", "-z"}
		args = append(args, extra...)
		if len(c.Parents) > 0 {
			args = append(args, c.Parents[0], c.SHA)
		} else {
			args = append(args, c.SHA)
		}
		return append(args, "--")
	}
	statusOut, err := s.run(ctx, path, base("--name-status")...)
	if err != nil {
		return nil, err
	}
	numOut, err := s.run(ctx, path, base("--numstat")...)
	if err != nil {
		return nil, err
	}
	return parseChangedFiles(statusOut, numOut), nil
}

func parseChangedFiles(statusOut, numOut string) []ChangedFile {
	files := []ChangedFile{}
	fields := strings.Split(statusOut, "\x00")
	for i := 0; i+1 < len(fields); i++ {
		code := fields[i]
		if code == "" {
			continue
		}
		f := ChangedFile{Status: changeLabels[code[0]]}
		if f.Status == "" {
			f.Status = "modified"
		}
		i++
		f.Path = fields[i]
		if code[0] == 'R' || code[0] == 'C' {
			// "R100\0old\0new\0": the earlier name comes first.
			f.From = f.Path
			if i+1 < len(fields) {
				i++
				f.Path = fields[i]
			}
		}
		files = append(files, f)
	}
	counts := map[string]ChangedFile{}
	nf := strings.Split(numOut, "\x00")
	for i := 0; i < len(nf); i++ {
		parts := strings.SplitN(nf[i], "\t", 3)
		if len(parts) < 3 {
			continue
		}
		cf := ChangedFile{Path: parts[2]}
		// A rename's record ends after the tab and two path fields follow.
		if cf.Path == "" && i+2 < len(nf) {
			cf.Path = nf[i+2]
			i += 2
		}
		if parts[0] == "-" {
			cf.Binary = true
		} else {
			cf.Insertions, _ = strconv.Atoi(parts[0])
			cf.Deletions, _ = strconv.Atoi(parts[1])
		}
		counts[cf.Path] = cf
	}
	for i := range files {
		if cf, ok := counts[files[i].Path]; ok {
			files[i].Insertions, files[i].Deletions, files[i].Binary = cf.Insertions, cf.Deletions, cf.Binary
		}
	}
	return files
}

// Comparison is what one branch has that another does not: the commits, and
// the size of the diff — what a pull request from head into base would carry.
type Comparison struct {
	Base       string        `json:"base"`
	Head       string        `json:"head"`
	Ahead      int           `json:"ahead"`
	Behind     int           `json:"behind"`
	Commits    []Commit      `json:"commits"`
	Files      int           `json:"files"`
	Insertions int           `json:"insertions"`
	Deletions  int           `json:"deletions"`
	BaseSHA    string        `json:"baseSha"`
	HeadSHA    string        `json:"headSha"`
	Changes    []ChangedFile `json:"changes"`
}

// Compare answers "what would merging head into base bring". The two refs
// are validated separately and joined here, because the `..` that joins them
// is exactly what ValidateRef refuses in a single ref.
func (s *Service) Compare(ctx context.Context, path, base, head string) (*Comparison, error) {
	if err := ValidateRef(base); err != nil {
		return nil, err
	}
	if err := ValidateRef(head); err != nil {
		return nil, err
	}
	cmp := &Comparison{Base: base, Head: head, Commits: []Commit{}}
	var err error
	cmp.BaseSHA, err = s.commitID(ctx, path, base)
	if err != nil {
		return nil, err
	}
	cmp.HeadSHA, err = s.commitID(ctx, path, head)
	if err != nil {
		return nil, err
	}
	base, head = cmp.BaseSHA, cmp.HeadSHA
	out, err := s.run(ctx, path, "rev-list", "--left-right", "--count", base+"..."+head, "--")
	if err != nil {
		return nil, err
	}
	if fields := strings.Fields(strings.TrimSpace(out)); len(fields) == 2 {
		cmp.Behind, _ = strconv.Atoi(fields[0])
		cmp.Ahead, _ = strconv.Atoi(fields[1])
	}
	out, err = s.run(ctx, path, "log", "--max-count=100", "--pretty=format:"+commitFields,
		"--shortstat", base+".."+head, "--")
	if err != nil {
		return nil, err
	}
	cmp.Commits = parseLog(out)
	// Three dots: the diff from the merge base, which is what the merge
	// would apply — two dots would also count everything base did since.
	statusOut, err := s.run(ctx, path, "diff", "--no-ext-diff", "--no-textconv", "-M", "-z", "--name-status", base+"..."+head, "--")
	if err != nil {
		return nil, err
	}
	numOut, err := s.run(ctx, path, "diff", "--no-ext-diff", "--no-textconv", "-M", "-z", "--numstat", base+"..."+head, "--")
	if err != nil {
		return nil, err
	}
	cmp.Changes = parseChangedFiles(statusOut, numOut)
	for _, f := range cmp.Changes {
		cmp.Files++
		cmp.Insertions += f.Insertions
		cmp.Deletions += f.Deletions
	}
	return cmp, nil
}
