package githubrepos

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// maxIssueTitle and maxIssueBody are GitHub's own limits, checked here
	// so the refusal says which one and costs no request.
	maxIssueTitle = 256
	maxIssueBody  = 65536
	// maxLabels bounds the labels one issue is given or filtered by.
	maxLabels = 100
	// maxLabelName is GitHub's limit on a label's name.
	maxLabelName = 50
)

// Issue is one issue.
type Issue struct {
	Number int64  `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
	// StateReason is completed, not_planned or reopened; absent when GitHub
	// recorded none.
	StateReason string    `json:"stateReason,omitempty"`
	Author      string    `json:"author"`
	Labels      []Label   `json:"labels"`
	Comments    int       `json:"comments"`
	Assignees   []string  `json:"assignees"`
	CreatedAt   time.Time `json:"createdAt,omitzero"`
	UpdatedAt   time.Time `json:"updatedAt,omitzero"`
	ClosedAt    time.Time `json:"closedAt,omitzero"`
	HTMLURL     string    `json:"htmlUrl"`
	// Body is only read for one issue, never for a list. A pointer, so an
	// issue with an empty body still says so on its own page.
	Body *string `json:"body,omitempty"`
}

// IssuePage is one page of issues.
type IssuePage struct {
	Issues  []Issue `json:"issues"`
	HasMore bool    `json:"hasMore"`
}

type rawIssue struct {
	Number      int64           `json:"number"`
	Title       string          `json:"title"`
	State       string          `json:"state"`
	StateReason *string         `json:"state_reason"`
	User        *rawUser        `json:"user"`
	Labels      []rawLabel      `json:"labels"`
	Comments    int             `json:"comments"`
	Assignees   []rawUser       `json:"assignees"`
	CreatedAt   *time.Time      `json:"created_at"`
	UpdatedAt   *time.Time      `json:"updated_at"`
	ClosedAt    *time.Time      `json:"closed_at"`
	HTMLURL     string          `json:"html_url"`
	Body        *string         `json:"body"`
	PullRequest json.RawMessage `json:"pull_request"`
}

func (raw rawIssue) isPull() bool {
	return len(raw.PullRequest) > 0 && string(raw.PullRequest) != "null"
}

func (raw rawIssue) issue(withBody bool) Issue {
	out := Issue{
		Number: raw.Number, Title: raw.Title, State: strings.ToLower(raw.State), Author: loginOf(raw.User),
		Labels: labelsOf(raw.Labels), Comments: raw.Comments, Assignees: make([]string, 0, len(raw.Assignees)),
		CreatedAt: timeOf(raw.CreatedAt), UpdatedAt: timeOf(raw.UpdatedAt), ClosedAt: timeOf(raw.ClosedAt),
		HTMLURL: raw.HTMLURL,
	}
	if raw.StateReason != nil {
		switch *raw.StateReason {
		case "completed", "not_planned", "reopened":
			out.StateReason = *raw.StateReason
		}
	}
	for _, assignee := range raw.Assignees {
		if assignee.Login != "" {
			out.Assignees = append(out.Assignees, assignee.Login)
		}
	}
	if withBody {
		body := ""
		if raw.Body != nil {
			body = *raw.Body
		}
		out.Body = &body
	}
	return out
}

func validLabels(labels []string) error {
	if len(labels) > maxLabels {
		return invalid("at most %d labels", maxLabels)
	}
	for _, label := range labels {
		if strings.TrimSpace(label) == "" || utf8.RuneCountInString(label) > maxLabelName || strings.ContainsAny(label, "\x00\r\n") {
			return invalid("%q is not a label name", label)
		}
	}
	return nil
}

// Issues reads one page of 30 issues. state is open, closed or all; labels
// keeps only issues carrying every one named. GitHub's issue listing
// includes pull requests, which are left out here (they have their own
// view), so a page may be short.
func (a *RepoAccess) Issues(ctx context.Context, state string, labels []string, page int) (*IssuePage, error) {
	switch state {
	case "":
		state = "open"
	case "open", "closed", "all":
	default:
		return nil, invalid("issue state %q is not open, closed or all", state)
	}
	if err := validLabels(labels); err != nil {
		return nil, err
	}
	query := url.Values{"state": {state}}
	if len(labels) > 0 {
		// GitHub reads the filter as a comma-separated list, so a label
		// with a comma in its name cannot be filtered on.
		for _, label := range labels {
			if strings.Contains(label, ",") {
				return nil, invalid("GitHub cannot filter on a label with a comma in its name (%q)", label)
			}
		}
		query.Set("labels", strings.Join(labels, ","))
	}
	var raw []rawIssue
	more, err := a.Client.Page(ctx, pagedPath(a.Path("/issues"), 30, page, query), &raw)
	if err != nil {
		return nil, err
	}
	out := &IssuePage{Issues: make([]Issue, 0, len(raw)), HasMore: more}
	for _, item := range raw {
		if item.isPull() {
			continue
		}
		out.Issues = append(out.Issues, item.issue(false))
	}
	return out, nil
}

func (a *RepoAccess) issuePath(number int64) string {
	return a.Path(fmt.Sprintf("/issues/%d", number))
}

// Issue reads one issue with its body. A pull request's number is answered
// as GitHub answers a number it does not have: pull requests are not edited
// through the issue view.
func (a *RepoAccess) Issue(ctx context.Context, number int64) (*Issue, error) {
	raw, err := a.rawIssue(ctx, number)
	if err != nil {
		return nil, err
	}
	issue := raw.issue(true)
	return &issue, nil
}

func (a *RepoAccess) rawIssue(ctx context.Context, number int64) (*rawIssue, error) {
	if number <= 0 {
		return nil, invalid("an issue number must be positive")
	}
	var raw rawIssue
	if _, err := a.Client.Do(ctx, http.MethodGet, a.issuePath(number), nil, &raw); err != nil {
		return nil, err
	}
	if raw.isPull() {
		return nil, notFound(a.issuePath(number), fmt.Sprintf("#%d is a pull request, not an issue", number))
	}
	return &raw, nil
}

// IssueCreate is a new issue.
type IssueCreate struct {
	Title  string   `json:"title"`
	Body   string   `json:"body,omitempty"`
	Labels []string `json:"labels,omitempty"`
}

func validTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", invalid("an issue needs a title")
	}
	if utf8.RuneCountInString(title) > maxIssueTitle {
		return "", invalid("the title is longer than %d characters", maxIssueTitle)
	}
	return title, nil
}

func validBody(body string) error {
	if utf8.RuneCountInString(body) > maxIssueBody {
		return invalid("the description is longer than %d characters", maxIssueBody)
	}
	return nil
}

// CreateIssue opens an issue. Anyone who can read the repository may open
// one (Abilities.CreateIssues); labelling it needs triage (Abilities.Issues),
// which GitHub would otherwise drop silently.
func (a *RepoAccess) CreateIssue(ctx context.Context, request IssueCreate) (*Issue, error) {
	title, err := validTitle(request.Title)
	if err != nil {
		return nil, err
	}
	if err := validBody(request.Body); err != nil {
		return nil, err
	}
	if err := validLabels(request.Labels); err != nil {
		return nil, err
	}
	if err := a.Require(a.Repository.Can.CreateIssues, "createIssues"); err != nil {
		return nil, err
	}
	if len(request.Labels) > 0 {
		if err := a.Require(a.Repository.Can.Issues, "issues"); err != nil {
			return nil, err
		}
	}
	body := map[string]any{"title": title}
	if request.Body != "" {
		body["body"] = request.Body
	}
	if len(request.Labels) > 0 {
		body["labels"] = request.Labels
	}
	var raw rawIssue
	if _, err := a.Client.Do(ctx, http.MethodPost, a.Path("/issues"), body, &raw); err != nil {
		return nil, err
	}
	issue := raw.issue(true)
	return &issue, nil
}

// IssueUpdate changes only the fields it carries. Labels set to an empty
// list removes every label; left out, they are not touched.
type IssueUpdate struct {
	Title       *string   `json:"title,omitempty"`
	Body        *string   `json:"body,omitempty"`
	State       *string   `json:"state,omitempty"`
	StateReason *string   `json:"stateReason,omitempty"`
	Labels      *[]string `json:"labels,omitempty"`
}

// UpdateIssue edits an issue. Its state and labels need triage
// (Abilities.Issues). Its title and body are also the author's to edit
// without triage, which only GitHub can judge for a given issue, so that
// case is passed to GitHub rather than refused here.
func (a *RepoAccess) UpdateIssue(ctx context.Context, number int64, request IssueUpdate) (*Issue, error) {
	if number <= 0 {
		return nil, invalid("an issue number must be positive")
	}
	body := map[string]any{}
	if request.Title != nil {
		title, err := validTitle(*request.Title)
		if err != nil {
			return nil, err
		}
		body["title"] = title
	}
	if request.Body != nil {
		if err := validBody(*request.Body); err != nil {
			return nil, err
		}
		body["body"] = *request.Body
	}
	if request.State != nil {
		if *request.State != "open" && *request.State != "closed" {
			return nil, invalid("an issue can be set open or closed, not %q", *request.State)
		}
		body["state"] = *request.State
	}
	if request.StateReason != nil {
		switch *request.StateReason {
		case "completed", "not_planned", "reopened":
		default:
			return nil, invalid("%q is not completed, not_planned or reopened", *request.StateReason)
		}
		body["state_reason"] = *request.StateReason
	}
	if request.Labels != nil {
		if err := validLabels(*request.Labels); err != nil {
			return nil, err
		}
		body["labels"] = *request.Labels
	}
	if len(body) == 0 {
		return nil, invalid("nothing to change")
	}
	_, triage := body["state"]
	if _, ok := body["state_reason"]; ok {
		triage = true
	}
	if _, ok := body["labels"]; ok {
		triage = true
	}
	if triage {
		if err := a.Require(a.Repository.Can.Issues, "issues"); err != nil {
			return nil, err
		}
	}
	// Read first: the issue endpoint edits a pull request's title too, and
	// that is not what the issue view is for.
	if _, err := a.rawIssue(ctx, number); err != nil {
		return nil, err
	}
	var raw rawIssue
	if _, err := a.Client.Do(ctx, http.MethodPatch, a.issuePath(number), body, &raw); err != nil {
		return nil, err
	}
	issue := raw.issue(true)
	return &issue, nil
}

// Labels reads every label of the repository (100 a page, MaxPages at
// most).
func (a *RepoAccess) Labels(ctx context.Context) ([]Label, error) {
	labels := []Label{}
	_, err := a.Client.Walk(ctx, a.Path("/labels?per_page=100"), func(page []byte) error {
		var batch []rawLabel
		if err := json.Unmarshal(page, &batch); err != nil {
			return err
		}
		labels = append(labels, labelsOf(batch)...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return labels, nil
}
