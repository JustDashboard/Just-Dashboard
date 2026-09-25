package ghx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Issue is one issue of a repository, in the shape the GitHub tab lists them.
type Issue struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	State     string    `json:"state"`
	Author    string    `json:"author,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Comments  int       `json:"comments"`
	Labels    []string  `json:"labels,omitempty"`
	Assignees []string  `json:"assignees,omitempty"`
}

const issueFields = "number,title,url,state,author,createdAt,updatedAt,comments,labels,assignees"

// ListIssues lists the issues of a named repository, newest first. state is
// open, closed or all; dir chooses the credential the way ListPullsIn
// describes.
func (s *Service) ListIssues(ctx context.Context, dir, nameWithOwner, state string, limit int) ([]Issue, error) {
	if !s.Available() {
		return nil, ErrNotInstalled
	}
	if !validRepoName(nameWithOwner) {
		return nil, fmt.Errorf("%q is not an owner/repository name", nameWithOwner)
	}
	switch state {
	case "open", "closed", "all":
	default:
		return nil, fmt.Errorf("state %q is not one of open, closed or all", state)
	}
	out, err := s.run(ctx, dir, "", "issue", "list", "--repo", nameWithOwner,
		"--state", state, "--limit", fmt.Sprint(clampLimit(limit, 30)), "--json", issueFields)
	if err != nil {
		return nil, ghErr("list issues", out)
	}
	var body []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		URL    string `json:"url"`
		State  string `json:"state"`
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
		CreatedAt time.Time  `json:"createdAt"`
		UpdatedAt time.Time  `json:"updatedAt"`
		Comments  []struct{} `json:"comments"`
		Labels    []struct {
			Name string `json:"name"`
		} `json:"labels"`
		Assignees []struct {
			Login string `json:"login"`
		} `json:"assignees"`
	}
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		return nil, fmt.Errorf("could not read gh's answer: %w", err)
	}
	issues := make([]Issue, 0, len(body))
	for _, i := range body {
		issue := Issue{
			Number: i.Number, Title: i.Title, URL: i.URL, State: strings.ToLower(i.State),
			Author: i.Author.Login, CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt,
			Comments: len(i.Comments),
		}
		for _, l := range i.Labels {
			if l.Name != "" {
				issue.Labels = append(issue.Labels, l.Name)
			}
		}
		for _, a := range i.Assignees {
			if a.Login != "" {
				issue.Assignees = append(issue.Assignees, a.Login)
			}
		}
		issues = append(issues, issue)
	}
	return issues, nil
}

// CommentIssue posts a plain comment on an issue or a pull request — GitHub
// keeps both conversations under the issues endpoint, which is why one verb
// serves the two. The body travels on stdin, never in argv: it is the
// operator's prose, and prose has no business being a command argument.
func (s *Service) CommentIssue(ctx context.Context, dir, nameWithOwner string, number int, body string) error {
	if !s.Available() {
		return ErrNotInstalled
	}
	if !validRepoName(nameWithOwner) {
		return fmt.Errorf("%q is not an owner/repository name", nameWithOwner)
	}
	if number <= 0 {
		return fmt.Errorf("an issue or pull request number is required")
	}
	body = strings.TrimSpace(body)
	if body == "" || len(body) > 60000 {
		return fmt.Errorf("a comment of at most 60,000 bytes is required")
	}
	endpoint := fmt.Sprintf("repos/%s/issues/%d/comments", nameWithOwner, number)
	return s.apiJSON(ctx, dir, DefaultHost, endpoint, map[string]string{"body": body}, nil)
}
