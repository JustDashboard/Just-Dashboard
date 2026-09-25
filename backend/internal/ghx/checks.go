package ghx

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// CheckRun is one verdict on a commit, whether it came from a check run (an
// App, such as Actions) or from the older commit status API (most third-party
// CI). Both are folded into the check run's vocabulary, since that is the one
// the page draws: Status is queued, in_progress or completed, and Conclusion
// is filled once it is completed.
type CheckRun struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion,omitempty"`
	// URL is where the verdict can be read in full. Third-party apps supply
	// it, so it is kept only when it is an http(s) address rather than a
	// scheme the browser would hand to something else.
	URL         string     `json:"url,omitempty"`
	App         string     `json:"app,omitempty"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
}

// PullChecks reads every check run and commit status on one commit of a
// named repository, failures first so the one that matters is on top. dir
// chooses the credential the way ListPullsIn describes.
//
// The commit is named rather than the pull request: a check belongs to a
// head SHA, and the caller already holds the SHA it is showing, so asking by
// number would only add a read whose answer could differ from what is on
// screen.
func (s *Service) PullChecks(ctx context.Context, dir, nameWithOwner, headSHA string) ([]CheckRun, error) {
	if !s.Available() {
		return nil, ErrNotInstalled
	}
	if !validRepoName(nameWithOwner) {
		return nil, fmt.Errorf("%q is not an owner/repository name", nameWithOwner)
	}
	if !fullSHA.MatchString(headSHA) {
		return nil, fmt.Errorf("a full head commit SHA is required")
	}
	commit := fmt.Sprintf("repos/%s/commits/%s", nameWithOwner, headSHA)
	var runs struct {
		CheckRuns []struct {
			Name        string     `json:"name"`
			Status      string     `json:"status"`
			Conclusion  string     `json:"conclusion"`
			HTMLURL     string     `json:"html_url"`
			DetailsURL  string     `json:"details_url"`
			StartedAt   *time.Time `json:"started_at"`
			CompletedAt *time.Time `json:"completed_at"`
			App         struct {
				Name string `json:"name"`
				Slug string `json:"slug"`
			} `json:"app"`
		} `json:"check_runs"`
	}
	if err := s.apiJSON(ctx, dir, DefaultHost, commit+"/check-runs?per_page=100", nil, &runs); err != nil {
		return nil, err
	}
	var combined struct {
		Statuses []struct {
			Context   string     `json:"context"`
			State     string     `json:"state"`
			TargetURL string     `json:"target_url"`
			CreatedAt *time.Time `json:"created_at"`
			UpdatedAt *time.Time `json:"updated_at"`
			Creator   struct {
				Login string `json:"login"`
			} `json:"creator"`
		} `json:"statuses"`
	}
	if err := s.apiJSON(ctx, dir, DefaultHost, commit+"/status?per_page=100", nil, &combined); err != nil {
		return nil, err
	}
	checks := make([]CheckRun, 0, len(runs.CheckRuns)+len(combined.Statuses))
	for _, r := range runs.CheckRuns {
		app := r.App.Name
		if app == "" {
			app = r.App.Slug
		}
		checks = append(checks, CheckRun{
			Name:        r.Name,
			Status:      checkStatus(r.Status),
			Conclusion:  strings.ToLower(r.Conclusion),
			URL:         webURL(r.HTMLURL, r.DetailsURL),
			App:         app,
			StartedAt:   r.StartedAt,
			CompletedAt: r.CompletedAt,
		})
	}
	// A commit status has three words where a check run has two: success and
	// failure are completed verdicts, error is a failure that never finished
	// running and reads the same to somebody deciding whether to merge, and
	// pending is a run still going.
	for _, st := range combined.Statuses {
		c := CheckRun{Name: st.Context, URL: webURL(st.TargetURL), App: st.Creator.Login, StartedAt: st.CreatedAt}
		switch strings.ToLower(st.State) {
		case "success":
			c.Status, c.Conclusion, c.CompletedAt = "completed", "success", st.UpdatedAt
		case "failure", "error":
			c.Status, c.Conclusion, c.CompletedAt = "completed", "failure", st.UpdatedAt
		default:
			c.Status, c.Conclusion = "in_progress", "pending"
		}
		checks = append(checks, c)
	}
	sort.SliceStable(checks, func(i, j int) bool {
		if a, b := checkRank(checks[i]), checkRank(checks[j]); a != b {
			return a < b
		}
		return checks[i].Name < checks[j].Name
	})
	return checks, nil
}

// checkStatus narrows GitHub's check run status to the three words the page
// knows. Newer runs also report pending, waiting and requested, all of which
// mean "not finished" to a reader.
func checkStatus(status string) string {
	switch strings.ToLower(status) {
	case "completed":
		return "completed"
	case "queued":
		return "queued"
	}
	return "in_progress"
}

// checkRank orders the list the way a reader scans it: what failed, then
// what is still running, then what passed.
func checkRank(c CheckRun) int {
	if c.Status != "completed" {
		return 1
	}
	switch c.Conclusion {
	case "success", "neutral", "skipped":
		return 2
	}
	return 0
}

// webURL returns the first candidate a browser can open as a page: http or
// https, nothing else. Everything else — javascript:, a bare path, an empty
// string — is dropped rather than drawn as a link.
func webURL(candidates ...string) string {
	for _, c := range candidates {
		u, err := url.Parse(c)
		if err != nil || u.Host == "" {
			continue
		}
		if u.Scheme == "http" || u.Scheme == "https" {
			return c
		}
	}
	return ""
}
