package githubrepos

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// PullRef is one end of a pull request.
type PullRef struct {
	Ref string `json:"ref"`
	SHA string `json:"sha,omitempty"`
	// Repo is owner/name of where the head lives; empty when that fork is
	// gone.
	Repo string `json:"repo,omitempty"`
}

// Pull is one pull request as a list shows it.
type Pull struct {
	Number int64  `json:"number"`
	Title  string `json:"title"`
	// State is open, closed (without merging) or merged.
	State     string    `json:"state"`
	Draft     bool      `json:"draft"`
	Author    string    `json:"author"`
	CreatedAt time.Time `json:"createdAt,omitzero"`
	UpdatedAt time.Time `json:"updatedAt,omitzero"`
	ClosedAt  time.Time `json:"closedAt,omitzero"`
	MergedAt  time.Time `json:"mergedAt,omitzero"`
	Head      PullRef   `json:"head"`
	Base      PullRef   `json:"base"`
	// Fork is a head in another repository, or in one that was deleted.
	Fork    bool    `json:"fork"`
	HTMLURL string  `json:"htmlUrl"`
	Labels  []Label `json:"labels"`
}

// PullPage is one page of pull requests.
type PullPage struct {
	Pulls   []Pull `json:"pulls"`
	HasMore bool   `json:"hasMore"`
}

// Review is one person's latest review on a pull request.
type Review struct {
	ID          int64     `json:"id"`
	Author      string    `json:"author"`
	State       string    `json:"state"`
	SubmittedAt time.Time `json:"submittedAt,omitzero"`
	HTMLURL     string    `json:"htmlUrl"`
}

// Check is one verdict on the head commit: a check run, or a commit status
// folded into the check run's words so the page draws one list.
type Check struct {
	Name string `json:"name"`
	// Status is queued, in_progress or completed for a check run, and
	// pending or completed for a commit status.
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion,omitempty"`
	DetailsURL  string    `json:"detailsUrl,omitempty"`
	Source      string    `json:"source"`
	StartedAt   time.Time `json:"startedAt,omitzero"`
	CompletedAt time.Time `json:"completedAt,omitzero"`
}

// PullDetail is one pull request in full.
type PullDetail struct {
	Pull
	Body string `json:"body"`
	// Mergeable is nil while GitHub is still working it out.
	Mergeable      *bool    `json:"mergeable"`
	MergeableState string   `json:"mergeableState"`
	Additions      int      `json:"additions"`
	Deletions      int      `json:"deletions"`
	ChangedFiles   int      `json:"changedFiles"`
	Commits        int      `json:"commits"`
	Reviews        []Review `json:"reviews"`
	Checks         []Check  `json:"checks"`
	ChecksReadable bool     `json:"checksReadable"`
	ChecksMessage  string   `json:"checksMessage,omitempty"`
	// MergeMethods are the ones the repository's settings allow.
	MergeMethods []string `json:"mergeMethods"`
}

type rawPullRef struct {
	Ref  string `json:"ref"`
	SHA  string `json:"sha"`
	Repo *struct {
		FullName string `json:"full_name"`
	} `json:"repo"`
}

type rawPull struct {
	Number    int64      `json:"number"`
	Title     string     `json:"title"`
	HTMLURL   string     `json:"html_url"`
	State     string     `json:"state"`
	Draft     bool       `json:"draft"`
	Merged    bool       `json:"merged"`
	User      *rawUser   `json:"user"`
	CreatedAt *time.Time `json:"created_at"`
	UpdatedAt *time.Time `json:"updated_at"`
	ClosedAt  *time.Time `json:"closed_at"`
	MergedAt  *time.Time `json:"merged_at"`
	Head      rawPullRef `json:"head"`
	Base      rawPullRef `json:"base"`
	Labels    []rawLabel `json:"labels"`

	Body           *string         `json:"body"`
	Mergeable      json.RawMessage `json:"mergeable"`
	MergeableState string          `json:"mergeable_state"`
	Additions      int             `json:"additions"`
	Deletions      int             `json:"deletions"`
	ChangedFiles   int             `json:"changed_files"`
	Commits        int             `json:"commits"`
}

func (raw rawPull) pull() Pull {
	out := Pull{
		Number: raw.Number, Title: raw.Title, State: strings.ToLower(raw.State), Draft: raw.Draft,
		Author: loginOf(raw.User), CreatedAt: timeOf(raw.CreatedAt), UpdatedAt: timeOf(raw.UpdatedAt),
		ClosedAt: timeOf(raw.ClosedAt), MergedAt: timeOf(raw.MergedAt),
		Head:    PullRef{Ref: raw.Head.Ref, SHA: raw.Head.SHA},
		Base:    PullRef{Ref: raw.Base.Ref},
		HTMLURL: raw.HTMLURL, Labels: labelsOf(raw.Labels),
	}
	// GitHub's list says closed for both; merged_at is what tells them apart.
	if raw.Merged || raw.MergedAt != nil {
		out.State = "merged"
	}
	base := ""
	if raw.Base.Repo != nil {
		base = raw.Base.Repo.FullName
	}
	if raw.Head.Repo != nil {
		out.Head.Repo = raw.Head.Repo.FullName
	}
	out.Fork = raw.Head.Repo == nil || !strings.EqualFold(out.Head.Repo, base)
	return out
}

// Pulls reads one page of 30 pull requests. state is open, closed (closed
// without merging), merged, or all. GitHub's listing knows only open, closed
// and all, so closed and merged both read a page of closed ones and keep
// their half: a page may be short, and whether more exist still comes from
// GitHub's Link header.
func (a *RepoAccess) Pulls(ctx context.Context, state string, page int) (*PullPage, error) {
	query := state
	switch state {
	case "", "open":
		state, query = "open", "open"
	case "all":
	case "closed", "merged":
		query = "closed"
	default:
		return nil, invalid("pull request state %q is not open, closed, merged or all", state)
	}
	var raw []rawPull
	more, err := a.Client.Page(ctx, pagedPath(a.Path("/pulls"), 30, page, url.Values{"state": {query}}), &raw)
	if err != nil {
		return nil, err
	}
	out := &PullPage{Pulls: make([]Pull, 0, len(raw)), HasMore: more}
	for _, item := range raw {
		pull := item.pull()
		if (state == "merged" && pull.State != "merged") || (state == "closed" && pull.State != "closed") {
			continue
		}
		out.Pulls = append(out.Pulls, pull)
	}
	return out, nil
}

func (a *RepoAccess) pullPath(number int64, suffix string) string {
	return a.Path(fmt.Sprintf("/pulls/%d%s", number, suffix))
}

// Pull reads one pull request with its reviews, the checks on its head and
// the merge methods the repository allows.
func (a *RepoAccess) Pull(ctx context.Context, number int64) (*PullDetail, error) {
	if number <= 0 {
		return nil, invalid("a pull request number must be positive")
	}
	var raw rawPull
	if _, err := a.Client.Do(ctx, http.MethodGet, a.pullPath(number, ""), nil, &raw); err != nil {
		return nil, err
	}
	out := &PullDetail{
		Pull: raw.pull(), MergeableState: raw.MergeableState, Additions: raw.Additions,
		Deletions: raw.Deletions, ChangedFiles: raw.ChangedFiles, Commits: raw.Commits,
		Reviews: []Review{}, Checks: []Check{},
	}
	if raw.Body != nil {
		out.Body = *raw.Body
	}
	switch string(raw.Mergeable) {
	case "true":
		yes := true
		out.Mergeable = &yes
	case "false":
		no := false
		out.Mergeable = &no
	}
	reviews, err := a.latestReviews(ctx, number)
	if err != nil {
		return nil, err
	}
	out.Reviews = reviews
	if IsFullSHA(out.Head.SHA) {
		if err := a.foldChecks(ctx, out); err != nil {
			return nil, err
		}
	} else {
		out.ChecksMessage = "GitHub did not say which commit is the head of this pull request."
	}
	methods, err := a.mergeMethods(ctx)
	if err != nil {
		return nil, err
	}
	out.MergeMethods = methods
	return out, nil
}

// latestReviews keeps each reviewer's latest verdict, in the order GitHub
// listed them. A comment after an approval does not withdraw the approval,
// which is how GitHub itself counts them, so a COMMENTED review only stands
// for someone who left nothing else.
func (a *RepoAccess) latestReviews(ctx context.Context, number int64) ([]Review, error) {
	type rawReview struct {
		ID          int64      `json:"id"`
		User        *rawUser   `json:"user"`
		State       string     `json:"state"`
		SubmittedAt *time.Time `json:"submitted_at"`
		HTMLURL     string     `json:"html_url"`
	}
	var all []rawReview
	_, err := a.Client.Walk(ctx, a.pullPath(number, "/reviews?per_page=100"), func(page []byte) error {
		var batch []rawReview
		if err := json.Unmarshal(page, &batch); err != nil {
			return err
		}
		all = append(all, batch...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	latest := map[string]int{}
	order := []string{}
	for i, review := range all {
		author := loginOf(review.User)
		previous, seen := latest[author]
		if !seen {
			order = append(order, author)
			latest[author] = i
			continue
		}
		if review.State != "COMMENTED" || all[previous].State == "COMMENTED" {
			latest[author] = i
		}
	}
	reviews := make([]Review, 0, len(order))
	for _, author := range order {
		review := all[latest[author]]
		reviews = append(reviews, Review{
			ID: review.ID, Author: author, State: strings.ToUpper(review.State),
			SubmittedAt: timeOf(review.SubmittedAt), HTMLURL: review.HTMLURL,
		})
	}
	sort.SliceStable(reviews, func(i, j int) bool { return latest[reviews[i].Author] < latest[reviews[j].Author] })
	return reviews, nil
}

// foldChecks reads the check runs and the combined status of the head and
// folds them into one list, failures first. Either read GitHub refuses
// (checks:read or statuses:read not granted) leaves the other's answer and
// a sentence saying what is missing.
func (a *RepoAccess) foldChecks(ctx context.Context, out *PullDetail) error {
	commit := a.Path("/commits/" + out.Head.SHA)
	var missing []string

	var runs struct {
		CheckRuns []struct {
			Name        string     `json:"name"`
			Status      string     `json:"status"`
			Conclusion  string     `json:"conclusion"`
			DetailsURL  string     `json:"details_url"`
			HTMLURL     string     `json:"html_url"`
			StartedAt   *time.Time `json:"started_at"`
			CompletedAt *time.Time `json:"completed_at"`
		} `json:"check_runs"`
	}
	if _, err := a.Client.Do(ctx, http.MethodGet, commit+"/check-runs?per_page=100", nil, &runs); err != nil {
		if !unreadable(err) {
			return err
		}
		missing = append(missing, "check runs")
	}
	for _, run := range runs.CheckRuns {
		out.Checks = append(out.Checks, Check{
			Name: run.Name, Status: checkStatus(run.Status), Conclusion: strings.ToLower(run.Conclusion),
			DetailsURL: webURL(run.HTMLURL, run.DetailsURL), Source: "check",
			StartedAt: timeOf(run.StartedAt), CompletedAt: timeOf(run.CompletedAt),
		})
	}

	var combined struct {
		Statuses []struct {
			Context   string     `json:"context"`
			State     string     `json:"state"`
			TargetURL string     `json:"target_url"`
			CreatedAt *time.Time `json:"created_at"`
			UpdatedAt *time.Time `json:"updated_at"`
		} `json:"statuses"`
	}
	if _, err := a.Client.Do(ctx, http.MethodGet, commit+"/status?per_page=100", nil, &combined); err != nil {
		if !unreadable(err) {
			return err
		}
		missing = append(missing, "commit statuses")
	}
	// A commit status has three words where a check run has two: success
	// and failure are finished verdicts, error is a failure that never
	// finished and reads the same to someone deciding whether to merge, and
	// pending is still going.
	for _, status := range combined.Statuses {
		check := Check{Name: status.Context, DetailsURL: webURL(status.TargetURL), Source: "status", StartedAt: timeOf(status.CreatedAt)}
		switch strings.ToLower(status.State) {
		case "success":
			check.Status, check.Conclusion, check.CompletedAt = "completed", "success", timeOf(status.UpdatedAt)
		case "failure", "error":
			check.Status, check.Conclusion, check.CompletedAt = "completed", "failure", timeOf(status.UpdatedAt)
		default:
			check.Status = "pending"
		}
		out.Checks = append(out.Checks, check)
	}
	sort.SliceStable(out.Checks, func(i, j int) bool {
		if x, y := checkRank(out.Checks[i]), checkRank(out.Checks[j]); x != y {
			return x < y
		}
		return out.Checks[i].Name < out.Checks[j].Name
	})
	out.ChecksReadable = len(missing) == 0
	if len(missing) > 0 {
		out.ChecksMessage = fmt.Sprintf("GitHub would not show this commit's %s to this account; the App may need the permission accepted on GitHub.", strings.Join(missing, " or "))
	}
	return nil
}

// checkStatus narrows a check run's status to three words. Newer runs also
// report pending, waiting and requested, all of which are "not finished".
func checkStatus(status string) string {
	switch strings.ToLower(status) {
	case "completed":
		return "completed"
	case "queued":
		return "queued"
	}
	return "in_progress"
}

// checkRank orders checks as a reader scans them: what failed, what is
// still running, what passed.
func checkRank(check Check) int {
	if check.Status != "completed" {
		return 1
	}
	switch check.Conclusion {
	case "success", "neutral", "skipped":
		return 2
	}
	return 0
}

// mergeMethods reads the repository's allow_* settings. GitHub states them
// only to people who may push; when it leaves one out, the method is
// offered and GitHub has the last word on the merge itself.
func (a *RepoAccess) mergeMethods(ctx context.Context) ([]string, error) {
	var settings struct {
		AllowMergeCommit *bool `json:"allow_merge_commit"`
		AllowSquashMerge *bool `json:"allow_squash_merge"`
		AllowRebaseMerge *bool `json:"allow_rebase_merge"`
	}
	if _, err := a.Client.Do(ctx, http.MethodGet, a.Path(""), nil, &settings); err != nil {
		return nil, err
	}
	allowed := func(flag *bool) bool { return flag == nil || *flag }
	methods := []string{}
	if allowed(settings.AllowMergeCommit) {
		methods = append(methods, "merge")
	}
	if allowed(settings.AllowSquashMerge) {
		methods = append(methods, "squash")
	}
	if allowed(settings.AllowRebaseMerge) {
		methods = append(methods, "rebase")
	}
	return methods, nil
}

// MergeRequest is a merge as the page asks for it.
type MergeRequest struct {
	Method string `json:"method"`
	// SHA is the head the person saw. It is required: GitHub refuses the
	// merge when the head moved since, so nobody merges commits they never
	// looked at.
	SHA     string `json:"sha"`
	Title   string `json:"title,omitempty"`
	Message string `json:"message,omitempty"`
}

// MergeResult is GitHub's answer to a merge.
type MergeResult struct {
	Merged  bool   `json:"merged"`
	SHA     string `json:"sha,omitempty"`
	Message string `json:"message"`
}

// Merge merges a pull request. GitHub's "not mergeable" (405) and "head
// moved" (409) come back as conflicts.
func (a *RepoAccess) Merge(ctx context.Context, number int64, request MergeRequest) (*MergeResult, error) {
	if number <= 0 {
		return nil, invalid("a pull request number must be positive")
	}
	switch request.Method {
	case "merge", "squash", "rebase":
	default:
		return nil, invalid("merge method %q is not merge, squash or rebase", request.Method)
	}
	sha := strings.ToLower(strings.TrimSpace(request.SHA))
	if !IsFullSHA(sha) {
		return nil, invalid("the merge needs the full 40-character head commit you reviewed")
	}
	if len(request.Title) > 256 {
		return nil, invalid("the commit title is longer than 256 characters")
	}
	if len(request.Message) > 65536 {
		return nil, invalid("the commit message is longer than 65,536 characters")
	}
	if err := a.Require(a.Repository.Can.Merge, "merge"); err != nil {
		return nil, err
	}
	body := map[string]string{"merge_method": request.Method, "sha": sha}
	if request.Title != "" {
		body["commit_title"] = request.Title
	}
	if request.Message != "" {
		body["commit_message"] = request.Message
	}
	var result MergeResult
	if _, err := a.Client.Do(ctx, http.MethodPut, a.pullPath(number, "/merge"), body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SetPullState closes or reopens a pull request.
func (a *RepoAccess) SetPullState(ctx context.Context, number int64, state string) (*Pull, error) {
	if number <= 0 {
		return nil, invalid("a pull request number must be positive")
	}
	if state != "open" && state != "closed" {
		return nil, invalid("a pull request can be set open or closed, not %q", state)
	}
	if err := a.Require(a.Repository.Can.Pulls, "pulls"); err != nil {
		return nil, err
	}
	var raw rawPull
	if _, err := a.Client.Do(ctx, http.MethodPatch, a.pullPath(number, ""), map[string]string{"state": state}, &raw); err != nil {
		return nil, err
	}
	pull := raw.pull()
	return &pull, nil
}
