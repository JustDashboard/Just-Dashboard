package githubapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

var (
	// ErrPullRequestNotFound is a pull request number the repository does not
	// have, or one the installation was not granted.
	ErrPullRequestNotFound = errors.New("that pull request does not exist on GitHub")
	// ErrPermission is GitHub refusing a read the App was never granted: an
	// App created before checks:read joined the manifest has to be granted it
	// on GitHub by its owner.
	ErrPermission = errors.New("the GitHub App is not allowed to read that on GitHub; accept its new permissions under the App's settings")
)

// PullRequest is one pull request read as the App. Its JSON is the shape the
// gh-backed reader produces, so a page draws both without knowing which
// identity answered. Review and Checks are missing on purpose: the REST
// listing carries neither, and the checks are read separately.
type PullRequest struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	// State is open, closed or merged, the way gh reports it.
	State  string `json:"state"`
	Draft  bool   `json:"draft"`
	Merged bool   `json:"merged,omitempty"`
	Head   string `json:"head"`
	Base   string `json:"base"`
	// HeadSHA is what a preview is built from and approved for.
	HeadSHA string `json:"headSha,omitempty"`
	BaseSHA string `json:"baseSha,omitempty"`
	Author  string `json:"author,omitempty"`
	// HeadRepository is owner/name of where the head branch lives; empty when
	// the fork it came from was deleted.
	HeadRepository string `json:"headRepository,omitempty"`
	// Fork is a head that lives outside the base repository, including one
	// whose repository is gone. Its code is somebody else's until reviewed.
	Fork bool `json:"fork,omitempty"`
	// Mergeable is mergeable, conflicting, or unknown while GitHub is still
	// computing it; empty on a listing, which never carries it.
	Mergeable string    `json:"mergeable,omitempty"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
	Labels    []string  `json:"labels,omitempty"`
	Comments  int       `json:"comments"`
	Additions int       `json:"additions,omitempty"`
	Deletions int       `json:"deletions,omitempty"`
	Files     int       `json:"files,omitempty"`
	Body      string    `json:"body,omitempty"`
}

// CheckRun is one check on a commit: a check run from an App, or a commit
// status folded into the same shape so the page draws one list.
type CheckRun struct {
	Name string `json:"name"`
	// Status is queued, in_progress or completed.
	Status string `json:"status"`
	// Conclusion is GitHub's word for a completed run: success, failure,
	// neutral, cancelled, skipped, timed_out, action_required, stale.
	Conclusion string `json:"conclusion,omitempty"`
	// URL is where the run's own report lives; kept only when it is a web
	// address, since a third-party App supplies it.
	URL         string     `json:"url,omitempty"`
	App         string     `json:"app,omitempty"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
}

// rawPullRequest is GitHub's REST shape for a pull request, with only the
// fields read here. Mergeable stays raw because absent (a listing), null
// (still computing) and false mean three different things.
type rawPullRequest struct {
	Number   int        `json:"number"`
	Title    string     `json:"title"`
	HTMLURL  string     `json:"html_url"`
	State    string     `json:"state"`
	Draft    bool       `json:"draft"`
	Merged   bool       `json:"merged"`
	MergedAt *time.Time `json:"merged_at"`
	Head     rawPullRef `json:"head"`
	Base     rawPullRef `json:"base"`
	User     struct {
		Login string `json:"login"`
	} `json:"user"`
	Mergeable json.RawMessage `json:"mergeable"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	Labels    []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Comments     int    `json:"comments"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	ChangedFiles int    `json:"changed_files"`
	Body         string `json:"body"`
}

type rawPullRef struct {
	Ref  string `json:"ref"`
	SHA  string `json:"sha"`
	Repo *struct {
		FullName string `json:"full_name"`
	} `json:"repo"`
}

func (r rawPullRequest) pullRequest() PullRequest {
	pr := PullRequest{
		Number: r.Number, Title: r.Title, URL: r.HTMLURL, State: strings.ToLower(r.State), Draft: r.Draft,
		Merged: r.Merged || r.MergedAt != nil,
		Head:   r.Head.Ref, Base: r.Base.Ref, HeadSHA: r.Head.SHA, BaseSHA: r.Base.SHA, Author: r.User.Login,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		Comments: r.Comments, Additions: r.Additions, Deletions: r.Deletions, Files: r.ChangedFiles, Body: r.Body,
	}
	// gh reports a merged pull request as MERGED rather than closed, and the
	// pages tell the two apart by that word.
	if pr.Merged {
		pr.State = "merged"
	}
	if r.Head.Repo != nil {
		pr.HeadRepository = r.Head.Repo.FullName
	}
	base := ""
	if r.Base.Repo != nil {
		base = r.Base.Repo.FullName
	}
	pr.Fork = r.Head.Repo == nil || !strings.EqualFold(pr.HeadRepository, base)
	switch string(r.Mergeable) {
	case "true":
		pr.Mergeable = "mergeable"
	case "false":
		pr.Mergeable = "conflicting"
	case "null":
		pr.Mergeable = "unknown"
	}
	for _, label := range r.Labels {
		if label.Name != "" {
			pr.Labels = append(pr.Labels, label.Name)
		}
	}
	return pr
}

// ListPullRequests reads a repository's pull requests as the App, newest
// activity first. The state is one of GitHub's three words and is checked
// before it reaches a URL.
func (c *Client) ListPullRequests(ctx context.Context, installationID int64, nameWithOwner, state string) ([]PullRequest, error) {
	if !repositoryRE.MatchString(nameWithOwner) {
		return nil, fmt.Errorf("repository %q is not owner/name", nameWithOwner)
	}
	switch state {
	case "open", "closed", "all":
	default:
		return nil, fmt.Errorf("pull request state %q is not open, closed or all", state)
	}
	token, err := c.InstallationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}
	pulls := []PullRequest{}
	err = c.walk(ctx, "/repos/"+nameWithOwner+"/pulls?state="+state+"&per_page=100&sort=updated&direction=desc", token, func(page []byte) error {
		var raw []rawPullRequest
		if err := json.Unmarshal(page, &raw); err != nil {
			return err
		}
		for _, item := range raw {
			pulls = append(pulls, item.pullRequest())
		}
		return nil
	})
	return pulls, err
}

// GetPullRequest reads one pull request in full: the listing's fields plus
// mergeability, the diff size and the body.
func (c *Client) GetPullRequest(ctx context.Context, installationID int64, nameWithOwner string, number int) (*PullRequest, error) {
	if !repositoryRE.MatchString(nameWithOwner) {
		return nil, fmt.Errorf("repository %q is not owner/name", nameWithOwner)
	}
	if number <= 0 {
		return nil, errors.New("a pull request number must be positive")
	}
	token, err := c.InstallationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}
	var raw rawPullRequest
	if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d", nameWithOwner, number), token, nil, &raw); err != nil {
		var api *APIError
		if errors.As(err, &api) && api.Status == http.StatusNotFound {
			return nil, ErrPullRequestNotFound
		}
		return nil, err
	}
	pr := raw.pullRequest()
	return &pr, nil
}

// ListCheckRuns reads the check runs Apps reported on a commit. GitHub
// answers 403 when this App was never granted checks:read; that is reported
// as ErrPermission so the caller can still show the plain statuses.
func (c *Client) ListCheckRuns(ctx context.Context, installationID int64, nameWithOwner, sha string) ([]CheckRun, error) {
	sha, err := validCommit(nameWithOwner, sha)
	if err != nil {
		return nil, err
	}
	token, err := c.InstallationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}
	runs := []CheckRun{}
	err = c.walk(ctx, "/repos/"+nameWithOwner+"/commits/"+sha+"/check-runs?per_page=100", token, func(page []byte) error {
		var raw struct {
			CheckRuns []struct {
				Name        string     `json:"name"`
				Status      string     `json:"status"`
				Conclusion  string     `json:"conclusion"`
				DetailsURL  string     `json:"details_url"`
				HTMLURL     string     `json:"html_url"`
				StartedAt   *time.Time `json:"started_at"`
				CompletedAt *time.Time `json:"completed_at"`
				App         *struct {
					Slug string `json:"slug"`
					Name string `json:"name"`
				} `json:"app"`
			} `json:"check_runs"`
		}
		if err := json.Unmarshal(page, &raw); err != nil {
			return err
		}
		for _, item := range raw.CheckRuns {
			run := CheckRun{
				Name: item.Name, Status: checkStatus(item.Status), Conclusion: strings.ToLower(item.Conclusion),
				URL: webURL(item.DetailsURL), StartedAt: item.StartedAt, CompletedAt: item.CompletedAt,
			}
			if run.URL == "" {
				run.URL = webURL(item.HTMLURL)
			}
			if item.App != nil {
				run.App = item.App.Name
				if run.App == "" {
					run.App = item.App.Slug
				}
			}
			runs = append(runs, run)
		}
		return nil
	})
	if err != nil {
		var api *APIError
		if errors.As(err, &api) && api.Status == http.StatusForbidden {
			return nil, fmt.Errorf("%w: %v", ErrPermission, err)
		}
		return nil, err
	}
	return runs, nil
}

// CombinedStatus reads the commit statuses on a commit, each folded into the
// check run shape: a status has no lifecycle of its own, so success and
// failure read as completed and pending as in progress.
func (c *Client) CombinedStatus(ctx context.Context, installationID int64, nameWithOwner, sha string) ([]CheckRun, error) {
	sha, err := validCommit(nameWithOwner, sha)
	if err != nil {
		return nil, err
	}
	token, err := c.InstallationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Statuses []struct {
			State     string    `json:"state"`
			Context   string    `json:"context"`
			TargetURL string    `json:"target_url"`
			CreatedAt time.Time `json:"created_at"`
			UpdatedAt time.Time `json:"updated_at"`
		} `json:"statuses"`
	}
	if _, err := c.do(ctx, http.MethodGet, "/repos/"+nameWithOwner+"/commits/"+sha+"/status?per_page=100", token, nil, &raw); err != nil {
		return nil, err
	}
	statuses := []CheckRun{}
	for _, item := range raw.Statuses {
		run := CheckRun{Name: item.Context, URL: webURL(item.TargetURL)}
		if !item.CreatedAt.IsZero() {
			started := item.CreatedAt
			run.StartedAt = &started
		}
		switch strings.ToLower(item.State) {
		case "success":
			run.Status, run.Conclusion = "completed", "success"
		case "failure", "error":
			run.Status, run.Conclusion = "completed", "failure"
		default:
			run.Status, run.Conclusion = "in_progress", "pending"
		}
		if run.Status == "completed" && !item.UpdatedAt.IsZero() {
			completed := item.UpdatedAt
			run.CompletedAt = &completed
		}
		statuses = append(statuses, run)
	}
	return statuses, nil
}

func validCommit(nameWithOwner, sha string) (string, error) {
	if !repositoryRE.MatchString(nameWithOwner) {
		return "", fmt.Errorf("repository %q is not owner/name", nameWithOwner)
	}
	sha = strings.ToLower(strings.TrimSpace(sha))
	if !commitSHARE.MatchString(sha) {
		return "", fmt.Errorf("commit %q is not a full object id", sha)
	}
	return sha, nil
}

// checkStatus folds GitHub's newer waiting, requested and pending states
// into queued: the page only tells not started, running and finished apart.
func checkStatus(status string) string {
	switch status = strings.ToLower(status); status {
	case "completed", "in_progress":
		return status
	default:
		return "queued"
	}
}

// webURL keeps a link only when a browser would open it as a page.
func webURL(raw string) string {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	return raw
}

// checkRank orders a list the way a reader scans it: what failed, then what
// is still running, then what passed.
func checkRank(run CheckRun) int {
	if run.Status != "completed" {
		return 1
	}
	switch run.Conclusion {
	case "success", "neutral", "skipped":
		return 2
	default:
		return 0
	}
}

// sortCheckRuns puts failures first, then pending, then successes, each
// group by name so the same commit always lists the same way.
func sortCheckRuns(runs []CheckRun) {
	sort.SliceStable(runs, func(i, j int) bool {
		if a, b := checkRank(runs[i]), checkRank(runs[j]); a != b {
			return a < b
		}
		return runs[i].Name < runs[j].Name
	})
}
