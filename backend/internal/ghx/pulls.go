package ghx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Repo is what GitHub knows about this checkout: which repository the remote
// points at, and what a pull request would target by default.
type Repo struct {
	NameWithOwner string `json:"nameWithOwner"`
	DefaultBranch string `json:"defaultBranch"`
	URL           string `json:"url"`
	Private       bool   `json:"private"`
	// Permission is the viewer's own — READ means a pull request has to come
	// from a fork, which is worth saying before the button is pressed rather
	// than after.
	Permission string `json:"permission,omitempty"`
}

// PullRequest is one pull request, in the shape the page lists them.
type PullRequest struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	State     string    `json:"state"`
	Draft     bool      `json:"draft"`
	Head      string    `json:"head"`
	Base      string    `json:"base"`
	Author    string    `json:"author,omitempty"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
	Comments  int       `json:"comments"`
	// Review is GitHub's verdict on the reviews so far: approved,
	// changes_requested, review_required, or empty where nobody is asked.
	Review string `json:"review,omitempty"`
	// Checks folds the status checks on the head commit into one word:
	// success, failure, pending, or empty where there are none.
	Checks string `json:"checks,omitempty"`
	// Mergeable is GitHub's answer to whether the branch applies cleanly:
	// mergeable, conflicting, or unknown while it is still computing.
	Mergeable string `json:"mergeable,omitempty"`
	Additions int    `json:"additions,omitempty"`
	Deletions int    `json:"deletions,omitempty"`
	Files     int    `json:"files,omitempty"`
	Body      string `json:"body,omitempty"`
	HeadSHA   string `json:"headSha,omitempty"`
	BaseSHA   string `json:"baseSha,omitempty"`
	// HeadRepository is the owner/name the head branch lives in. It differs
	// from the base repository exactly when Fork is set, and a preview built
	// from it runs that repository's code on this server — which is why both
	// are said rather than derived.
	HeadRepository string    `json:"headRepository,omitempty"`
	Fork           bool      `json:"fork,omitempty"`
	UpdatedAt      time.Time `json:"updatedAt,omitempty"`
	Labels         []string  `json:"labels,omitempty"`
	Merged         bool      `json:"merged,omitempty"`
}

// pullFields is what `gh pr list` and `gh pr view` are asked for. The review
// decision and the check rollup are cheap; mergeable is left to the detail
// read because GitHub computes it lazily and a list should not wait for it.
// The head commit and its repository are in the list because a preview is
// approved for an exact head, and the list is where the button is.
const pullFields = "number,title,url,state,isDraft,headRefName,baseRefName,author,createdAt,comments,reviewDecision,statusCheckRollup," +
	"headRefOid,headRepository,headRepositoryOwner,isCrossRepository,updatedAt,labels,mergedAt"

// ghPull is gh's JSON for one pull request, with only the fields read here.
type ghPull struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	State   string `json:"state"`
	IsDraft bool   `json:"isDraft"`
	Head    string `json:"headRefName"`
	Base    string `json:"baseRefName"`
	Author  struct {
		Login string `json:"login"`
	} `json:"author"`
	CreatedAt      time.Time  `json:"createdAt"`
	Comments       []struct{} `json:"comments"`
	ReviewDecision string     `json:"reviewDecision"`
	Rollup         []struct {
		State      string `json:"state"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	} `json:"statusCheckRollup"`
	Mergeable    string `json:"mergeable"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	ChangedFiles int    `json:"changedFiles"`
	Body         string `json:"body"`
	HeadSHA      string `json:"headRefOid"`
	BaseSHA      string `json:"baseRefOid"`
	// HeadRepository is null once a fork has been deleted, which leaves the
	// name empty rather than failing the read.
	HeadRepository struct {
		Name string `json:"name"`
	} `json:"headRepository"`
	HeadRepositoryOwner struct {
		Login string `json:"login"`
	} `json:"headRepositoryOwner"`
	IsCrossRepository bool      `json:"isCrossRepository"`
	UpdatedAt         time.Time `json:"updatedAt"`
	Labels            []struct {
		Name string `json:"name"`
	} `json:"labels"`
	MergedAt *time.Time `json:"mergedAt"`
}

func (p ghPull) pullRequest() PullRequest {
	pr := PullRequest{
		Number: p.Number, Title: p.Title, URL: p.URL, State: strings.ToLower(p.State),
		Draft: p.IsDraft, Head: p.Head, Base: p.Base, Author: p.Author.Login,
		CreatedAt: p.CreatedAt, Comments: len(p.Comments),
		Review:    strings.ToLower(p.ReviewDecision),
		Mergeable: strings.ToLower(p.Mergeable),
		Additions: p.Additions, Deletions: p.Deletions, Files: p.ChangedFiles, Body: p.Body,
		HeadSHA: p.HeadSHA, BaseSHA: p.BaseSHA,
		Fork:      p.IsCrossRepository,
		UpdatedAt: p.UpdatedAt,
		Merged:    p.MergedAt != nil || strings.EqualFold(p.State, "MERGED"),
	}
	if p.HeadRepositoryOwner.Login != "" && p.HeadRepository.Name != "" {
		pr.HeadRepository = p.HeadRepositoryOwner.Login + "/" + p.HeadRepository.Name
	}
	for _, l := range p.Labels {
		if l.Name != "" {
			pr.Labels = append(pr.Labels, l.Name)
		}
	}
	// The rollup mixes two shapes — a status context carries `state`, a check
	// run carries `status` and `conclusion` — and one failure outranks any
	// number of passes, one pending outranks any number of passes too.
	for _, c := range p.Rollup {
		verdict := strings.ToUpper(c.State)
		if verdict == "" {
			if strings.ToUpper(c.Status) != "COMPLETED" {
				verdict = "PENDING"
			} else {
				verdict = strings.ToUpper(c.Conclusion)
			}
		}
		switch verdict {
		case "SUCCESS", "NEUTRAL", "SKIPPED":
			if pr.Checks == "" {
				pr.Checks = "success"
			}
		case "PENDING", "EXPECTED", "QUEUED", "IN_PROGRESS", "WAITING", "REQUESTED":
			if pr.Checks != "failure" {
				pr.Checks = "pending"
			}
		default:
			pr.Checks = "failure"
		}
	}
	return pr
}

// RepoInfo reads the repository behind the checkout's remote.
//
// It is a separate call from the pull request list because it answers a
// different question and is wanted at a different time — the list feeds a tab
// that polls, this feeds the create dialog when it opens.
func (s *Service) RepoInfo(ctx context.Context, dir string) (*Repo, error) {
	if !s.Available() {
		return nil, ErrNotInstalled
	}
	out, err := s.run(ctx, dir, "", "repo", "view",
		"--json", "nameWithOwner,defaultBranchRef,url,isPrivate,viewerPermission")
	if err != nil {
		return nil, ghErr("read this repository on GitHub", out)
	}
	var body struct {
		NameWithOwner    string `json:"nameWithOwner"`
		DefaultBranchRef struct {
			Name string `json:"name"`
		} `json:"defaultBranchRef"`
		URL        string `json:"url"`
		IsPrivate  bool   `json:"isPrivate"`
		Permission string `json:"viewerPermission"`
	}
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		return nil, fmt.Errorf("could not read gh's answer: %w", err)
	}
	return &Repo{
		NameWithOwner: body.NameWithOwner,
		DefaultBranch: body.DefaultBranchRef.Name,
		URL:           body.URL,
		Private:       body.IsPrivate,
		Permission:    body.Permission,
	}, nil
}

// ListPulls returns pull requests, newest first. state is open, closed,
// merged or all; anything else reads as open.
func (s *Service) ListPulls(ctx context.Context, dir, state string, limit int) ([]PullRequest, error) {
	switch state {
	case "open", "closed", "merged", "all":
	default:
		state = "open"
	}
	return s.listPulls(ctx, dir, nil, state, limit)
}

// ListPullsIn lists the pull requests of a named repository rather than of a
// checkout: dir "" runs as the dashboard's own account, a directory as the
// account that owns it. state is open, closed, merged or all, and unlike
// ListPulls an unknown state is refused rather than read as open, because
// the callers here pass a request's word through and a typo should be told.
func (s *Service) ListPullsIn(ctx context.Context, dir, nameWithOwner, state string, limit int) ([]PullRequest, error) {
	if !validRepoName(nameWithOwner) {
		return nil, fmt.Errorf("%q is not an owner/repository name", nameWithOwner)
	}
	switch state {
	case "open", "closed", "merged", "all":
	default:
		return nil, fmt.Errorf("state %q is not one of open, closed, merged or all", state)
	}
	return s.listPulls(ctx, dir, []string{"--repo", nameWithOwner}, state, limit)
}

func (s *Service) listPulls(ctx context.Context, dir string, where []string, state string, limit int) ([]PullRequest, error) {
	if !s.Available() {
		return nil, ErrNotInstalled
	}
	args := append([]string{"pr", "list"}, where...)
	args = append(args, "--state", state, "--limit", fmt.Sprint(clampLimit(limit, 30)), "--json", pullFields)
	out, err := s.run(ctx, dir, "", args...)
	if err != nil {
		return nil, ghErr("list pull requests", out)
	}
	var body []ghPull
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		return nil, fmt.Errorf("could not read gh's answer: %w", err)
	}
	pulls := make([]PullRequest, 0, len(body))
	for _, p := range body {
		pulls = append(pulls, p.pullRequest())
	}
	return pulls, nil
}

// clampLimit keeps a page size inside what gh will serve in one call: nothing
// asked for means the caller's usual page, more than a hundred means a hundred.
func clampLimit(limit, fallback int) int {
	if limit <= 0 {
		return fallback
	}
	if limit > 100 {
		return 100
	}
	return limit
}

// pullDetailFields is what ViewPull asks for beyond the list: the lazily
// computed mergeability, the size of the change and the body.
const pullDetailFields = pullFields + ",mergeable,additions,deletions,changedFiles,body,baseRefOid"

// ViewPull reads one pull request in full: the list's fields plus whether
// GitHub thinks it applies cleanly, the size of the change and its body.
func (s *Service) ViewPull(ctx context.Context, dir string, number int) (*PullRequest, error) {
	return s.viewPull(ctx, dir, nil, number)
}

// ViewPullIn reads one pull request of a named repository; dir chooses the
// credential the way ListPullsIn describes.
func (s *Service) ViewPullIn(ctx context.Context, dir, nameWithOwner string, number int) (*PullRequest, error) {
	if !validRepoName(nameWithOwner) {
		return nil, fmt.Errorf("%q is not an owner/repository name", nameWithOwner)
	}
	return s.viewPull(ctx, dir, []string{"--repo", nameWithOwner}, number)
}

func (s *Service) viewPull(ctx context.Context, dir string, where []string, number int) (*PullRequest, error) {
	if !s.Available() {
		return nil, ErrNotInstalled
	}
	if number <= 0 {
		return nil, fmt.Errorf("a pull request number is required")
	}
	args := append([]string{"pr", "view", fmt.Sprint(number)}, where...)
	args = append(args, "--json", pullDetailFields)
	out, err := s.run(ctx, dir, "", args...)
	if err != nil {
		return nil, ghErr("read the pull request", out)
	}
	var body ghPull
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		return nil, fmt.Errorf("could not read gh's answer: %w", err)
	}
	pr := body.pullRequest()
	return &pr, nil
}

// MergePull merges a pull request on GitHub, the way the button on the
// request's page does. method is merge, squash or rebase; deleteBranch
// removes the head branch afterwards, which is what that page offers too.
// headSHA, when given, is the commit the operator looked at: GitHub refuses
// the merge if the branch has moved since, so a push that lands between the
// review and the press merges nothing unseen. Empty leaves the head unpinned.
//
// This changes the base branch on the remote, so it sits under
// service.control with a confirmation rather than being a click: it is the
// most consequential thing this page can do to a shared repository, and it
// is also exactly the thing an operator reviewing from a phone wants.
func (s *Service) MergePull(ctx context.Context, dir string, number int, method string, deleteBranch bool, headSHA string) error {
	return s.mergePull(ctx, dir, nil, number, method, deleteBranch, headSHA)
}

// MergePullIn merges a pull request of a named repository; dir chooses the
// credential the way ListPullsIn describes.
func (s *Service) MergePullIn(ctx context.Context, dir, nameWithOwner string, number int, method string, deleteBranch bool, headSHA string) error {
	if !validRepoName(nameWithOwner) {
		return fmt.Errorf("%q is not an owner/repository name", nameWithOwner)
	}
	return s.mergePull(ctx, dir, []string{"--repo", nameWithOwner}, number, method, deleteBranch, headSHA)
}

func (s *Service) mergePull(ctx context.Context, dir string, where []string, number int, method string, deleteBranch bool, headSHA string) error {
	if !s.Available() {
		return ErrNotInstalled
	}
	if number <= 0 {
		return fmt.Errorf("a pull request number is required")
	}
	flag := "--merge"
	switch method {
	case "", "merge":
	case "squash":
		flag = "--squash"
	case "rebase":
		flag = "--rebase"
	default:
		return fmt.Errorf("merge method %q is not one of merge, squash or rebase", method)
	}
	if headSHA != "" && !fullSHA.MatchString(headSHA) {
		return fmt.Errorf("the head commit to match is not a full SHA")
	}
	args := append([]string{"pr", "merge", fmt.Sprint(number)}, where...)
	args = append(args, flag)
	if deleteBranch {
		args = append(args, "--delete-branch")
	}
	if headSHA != "" {
		args = append(args, "--match-head-commit", headSHA)
	}
	if out, err := s.run(ctx, dir, "", args...); err != nil {
		return ghErr("merge the pull request", out)
	}
	return nil
}

// CheckoutPull fetches a pull request's branch and switches to it — the way
// to try somebody else's change on this server before merging it.
func (s *Service) CheckoutPull(ctx context.Context, dir string, number int) error {
	if !s.Available() {
		return ErrNotInstalled
	}
	if number <= 0 {
		return fmt.Errorf("a pull request number is required")
	}
	if out, err := s.run(ctx, dir, "", "pr", "checkout", fmt.Sprint(number)); err != nil {
		return ghErr("check the pull request out", out)
	}
	return nil
}

// WorkflowRun is one GitHub Actions run, enough to say whether the last push
// passed.
type WorkflowRun struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Workflow   string    `json:"workflow,omitempty"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion,omitempty"`
	URL        string    `json:"url"`
	Branch     string    `json:"branch,omitempty"`
	SHA        string    `json:"sha,omitempty"`
	Event      string    `json:"event,omitempty"`
	CreatedAt  time.Time `json:"createdAt,omitempty"`
}

// ListRuns lists recent Actions runs, for one branch when named.
func (s *Service) ListRuns(ctx context.Context, dir, branch string, limit int) ([]WorkflowRun, error) {
	if !s.Available() {
		return nil, ErrNotInstalled
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	args := []string{"run", "list", "--limit", fmt.Sprint(limit),
		"--json", "databaseId,name,workflowName,status,conclusion,url,headBranch,headSha,event,createdAt"}
	if branch != "" {
		if err := validateBranch(branch); err != nil {
			return nil, err
		}
		args = append(args, "--branch", branch)
	}
	out, err := s.run(ctx, dir, "", args...)
	if err != nil {
		return nil, ghErr("list workflow runs", out)
	}
	var body []struct {
		ID         int64     `json:"databaseId"`
		Name       string    `json:"name"`
		Workflow   string    `json:"workflowName"`
		Status     string    `json:"status"`
		Conclusion string    `json:"conclusion"`
		URL        string    `json:"url"`
		Branch     string    `json:"headBranch"`
		SHA        string    `json:"headSha"`
		Event      string    `json:"event"`
		CreatedAt  time.Time `json:"createdAt"`
	}
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		return nil, fmt.Errorf("could not read gh's answer: %w", err)
	}
	runs := make([]WorkflowRun, 0, len(body))
	for _, r := range body {
		runs = append(runs, WorkflowRun{
			ID: r.ID, Name: r.Name, Workflow: r.Workflow, Status: strings.ToLower(r.Status),
			Conclusion: strings.ToLower(r.Conclusion), URL: r.URL, Branch: r.Branch,
			SHA: r.SHA, Event: r.Event, CreatedAt: r.CreatedAt,
		})
	}
	return runs, nil
}

// NewPull is the description of a pull request to open.
type NewPull struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Base  string `json:"base"`
	Head  string `json:"head"`
	Draft bool   `json:"draft"`
}

// CreatePull opens a pull request from the current branch.
//
// gh's own message is passed through on failure, because the two ways this
// fails are both things only gh can say precisely: there are no commits
// between the branches, or one already exists — and that second message
// carries the URL of the existing one, which is exactly what the operator
// wanted anyway.
func (s *Service) CreatePull(ctx context.Context, dir string, req NewPull) (*PullRequest, error) {
	if !s.Available() {
		return nil, ErrNotInstalled
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return nil, fmt.Errorf("a title is required")
	}
	args := []string{"pr", "create", "--title", title, "--body", req.Body}
	if req.Base != "" {
		if err := validateBranch(req.Base); err != nil {
			return nil, err
		}
		args = append(args, "--base", req.Base)
	}
	if req.Head != "" {
		if err := validateBranch(req.Head); err != nil {
			return nil, err
		}
		args = append(args, "--head", req.Head)
	}
	if req.Draft {
		args = append(args, "--draft")
	}
	out, err := s.run(ctx, dir, "", args...)
	if err != nil {
		return nil, ghErr("open a pull request", out)
	}
	url := lastURL(out)
	if url == "" {
		return nil, fmt.Errorf("gh did not report a pull request URL: %s", firstMeaningfulLine(out))
	}
	return &PullRequest{Title: title, URL: url, Base: req.Base, Head: req.Head, Draft: req.Draft, State: "open"}, nil
}

// validateBranch is the same rule gitx applies to a ref, restated here because
// these names reach gh rather than git and the two packages must not have to
// import each other to agree about it.
func validateBranch(name string) error {
	if name == "" || len(name) > 255 || strings.HasPrefix(name, "-") || strings.Contains(name, "..") {
		return fmt.Errorf("branch name is not allowed: %q", name)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("._-/@+", r):
		default:
			return fmt.Errorf("branch name is not allowed: %q", name)
		}
	}
	return nil
}

func lastURL(out string) string {
	found := ""
	for _, line := range strings.Fields(out) {
		if strings.HasPrefix(line, "https://") {
			found = strings.Trim(line, `"'.,`)
		}
	}
	return found
}

// ghErr keeps gh's own words. They are better than a paraphrase — "must be on
// a branch named differently than the base" is the whole diagnosis — and the
// verb says which operation produced them.
func ghErr(what, out string) error {
	msg := firstMeaningfulLine(out)
	if msg == "" {
		msg = "gh reported no reason"
	}
	return fmt.Errorf("could not %s: %s", what, msg)
}
