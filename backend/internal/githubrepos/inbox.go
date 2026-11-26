package githubrepos

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The account's own pull requests and issues, across every repository it
// reaches: GitHub's issue search with @me in the qualifier, which GitHub
// reads as whoever the token belongs to. No login is ever spelled into a
// query, neither a request's nor the one stored when the account was
// connected: an account renamed on GitHub since, whose old login somebody
// else then took, would otherwise list that person's work as its own.

const (
	// InboxPulls and InboxIssues are the two listings.
	InboxPulls  = "pulls"
	InboxIssues = "issues"

	inboxPerPage = 30
	// maxInboxPage is the last page GitHub's search answers at thirty a
	// page: it stops at 1,000 results.
	maxInboxPage = 34
)

// InboxQuery is one page of one listing, already checked by ParseInboxQuery.
type InboxQuery struct {
	// Kind is InboxPulls or InboxIssues.
	Kind string
	// Filter is created, assigned, mentioned or, for pull requests, review.
	Filter string
	// State is open or closed.
	State string
	Page  int
}

type inboxKey struct {
	connection int64
	query      InboxQuery
}

// inboxQualifiers are GitHub's search qualifiers by the page's filter names.
var inboxQualifiers = map[string]string{
	"created":   "author",
	"assigned":  "assignee",
	"mentioned": "mentions",
	"review":    "review-requested",
}

// ParseInboxQuery reads a listing's filter, state and page as a request
// spells them, before anything is asked of GitHub. Empty values are the
// defaults: created, open, the first page.
func ParseInboxQuery(kind, filter, state, page string) (InboxQuery, error) {
	query := InboxQuery{Kind: kind, Filter: filter, State: state, Page: 1}
	if query.Filter == "" {
		query.Filter = "created"
	}
	if query.State == "" {
		query.State = "open"
	}
	if page != "" {
		n, err := strconv.Atoi(page)
		if err != nil || n < 1 {
			return InboxQuery{}, invalid("page %q is not a positive number", page)
		}
		query.Page = n
	}
	if err := query.check(); err != nil {
		return InboxQuery{}, err
	}
	return query, nil
}

func (q InboxQuery) check() error {
	switch q.Kind {
	case InboxPulls, InboxIssues:
	default:
		return invalid("%q is neither pull requests nor issues", q.Kind)
	}
	switch {
	case inboxQualifiers[q.Filter] == "":
		return invalid("filter %q is not created, assigned, mentioned or review", q.Filter)
	case q.Filter == "review" && q.Kind == InboxIssues:
		return invalid("only pull requests are filtered by review; issues take created, assigned or mentioned")
	}
	if q.State != "open" && q.State != "closed" {
		return invalid("state %q is not open or closed", q.State)
	}
	if q.Page < 1 || q.Page > maxInboxPage {
		return invalid("page %d is past what GitHub's search answers: it stops at 1,000 results, page %d", q.Page, maxInboxPage)
	}
	return nil
}

// searchLoginRE is what a login may be to go into a path: GitHub's own
// alphabet, plus the underscore of an enterprise-managed account. A login is
// GitHub's answer and not a request's, but a slash in one would read as
// another path, so it is held to this anyway.
var searchLoginRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,99}$`)

func unusableLogin(path string) error {
	return &APIError{Status: http.StatusBadGateway, Kind: KindUnavailable, Message: "this account's GitHub login is not one GitHub can be asked about", Path: path}
}

// searchPath is GET /search/issues for one listing, newest update first.
func (q InboxQuery) searchPath() (string, error) {
	if err := q.check(); err != nil {
		return "", err
	}
	is := "is:pr"
	if q.Kind == InboxIssues {
		is = "is:issue"
	}
	terms := []string{is, "is:" + q.State, "archived:false", inboxQualifiers[q.Filter] + ":@me"}
	values := url.Values{
		"q": {strings.Join(terms, " ")}, "sort": {"updated"}, "order": {"desc"},
		"per_page": {strconv.Itoa(inboxPerPage)}, "page": {strconv.Itoa(q.Page)},
	}
	return "/search/issues?" + values.Encode(), nil
}

// InboxItem is GitHubInboxItem: one pull request or issue in any repository
// the account reaches.
type InboxItem struct {
	// Repository is owner/name.
	Repository string `json:"repository"`
	Number     int64  `json:"number"`
	Title      string `json:"title"`
	// State is open or closed, or merged for a pull request.
	State string `json:"state"`
	// StateReason is an issue's completed, not_planned or reopened.
	StateReason string    `json:"stateReason,omitempty"`
	Draft       bool      `json:"draft"`
	Author      string    `json:"author"`
	Labels      []Label   `json:"labels"`
	Comments    int       `json:"comments"`
	CreatedAt   time.Time `json:"createdAt,omitzero"`
	UpdatedAt   time.Time `json:"updatedAt,omitzero"`
	ClosedAt    time.Time `json:"closedAt,omitzero"`
	HTMLURL     string    `json:"htmlUrl"`
}

// InboxPage is GitHubInboxPage.
type InboxPage struct {
	Items []InboxItem `json:"items"`
	// Total is GitHub's count of every match, not only this page.
	Total   int  `json:"total"`
	HasMore bool `json:"hasMore"`
	// Incomplete is GitHub saying its search timed out and may have missed
	// some.
	Incomplete bool `json:"incomplete"`
}

// rawSearchItem is one item of GET /search/issues: an issue, with the
// repository only as an API address and, for a pull request, a pull_request
// object that says when it was merged.
type rawSearchItem struct {
	RepositoryURL string     `json:"repository_url"`
	Number        int64      `json:"number"`
	Title         string     `json:"title"`
	State         string     `json:"state"`
	StateReason   *string    `json:"state_reason"`
	Draft         bool       `json:"draft"`
	User          *rawUser   `json:"user"`
	Labels        []rawLabel `json:"labels"`
	Comments      int        `json:"comments"`
	CreatedAt     *time.Time `json:"created_at"`
	UpdatedAt     *time.Time `json:"updated_at"`
	ClosedAt      *time.Time `json:"closed_at"`
	HTMLURL       string     `json:"html_url"`
	PullRequest   *struct {
		MergedAt *time.Time `json:"merged_at"`
	} `json:"pull_request"`
}

// repositoryOfURL reads owner/name out of a repository's API address
// (…/repos/{owner}/{name}), or "" when it is not one.
func repositoryOfURL(address string) string {
	_, rest, found := strings.Cut(address, "/repos/")
	if !found {
		return ""
	}
	owner, name, found := strings.Cut(rest, "/")
	if !found || !searchLoginRE.MatchString(owner) || !ValidRepoName(name) {
		return ""
	}
	return owner + "/" + name
}

func (raw rawSearchItem) item() (InboxItem, bool) {
	repository := repositoryOfURL(raw.RepositoryURL)
	if repository == "" || raw.Number <= 0 {
		return InboxItem{}, false
	}
	out := InboxItem{
		Repository: repository, Number: raw.Number, Title: raw.Title, State: strings.ToLower(raw.State),
		Author: loginOf(raw.User), Labels: labelsOf(raw.Labels), Comments: raw.Comments,
		CreatedAt: timeOf(raw.CreatedAt), UpdatedAt: timeOf(raw.UpdatedAt), ClosedAt: timeOf(raw.ClosedAt),
		HTMLURL: raw.HTMLURL,
	}
	if raw.PullRequest != nil {
		out.Draft = raw.Draft
		// Search says closed for a merged pull request too; merged_at is
		// what tells them apart.
		if raw.PullRequest.MergedAt != nil {
			out.State = "merged"
		}
		return out, true
	}
	if raw.StateReason != nil {
		switch *raw.StateReason {
		case "completed", "not_planned", "reopened":
			out.StateReason = *raw.StateReason
		}
	}
	return out, true
}

// searchInbox reads one page. An item whose repository GitHub did not name
// is left out: the page could not say where it is.
func searchInbox(ctx context.Context, client *Client, query InboxQuery) (InboxPage, error) {
	path, err := query.searchPath()
	if err != nil {
		return InboxPage{}, err
	}
	response, err := client.Request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return InboxPage{}, err
	}
	var body struct {
		TotalCount int             `json:"total_count"`
		Incomplete bool            `json:"incomplete_results"`
		Items      []rawSearchItem `json:"items"`
	}
	if err := json.Unmarshal(response.Body, &body); err != nil {
		return InboxPage{}, &APIError{Status: response.Status, Kind: KindUnavailable, Message: "GitHub's search answer was not readable JSON", Path: "/search/issues"}
	}
	page := InboxPage{
		Items: make([]InboxItem, 0, len(body.Items)), Total: body.TotalCount, Incomplete: body.Incomplete,
		// GitHub's Link header names a next page only inside the 1,000
		// results it serves; the page cap is the same rule held here too.
		HasMore: NextLink(response.Header.Get("Link")) != "" && query.Page < maxInboxPage,
	}
	for _, raw := range body.Items {
		if item, ok := raw.item(); ok {
			page.Items = append(page.Items, item)
		}
	}
	return page, nil
}

// Inbox is one page of the connected account's pull requests or issues
// across every repository, answered from memory for a minute unless refresh
// is set: GitHub's search allows thirty requests a minute.
func (s *Service) Inbox(ctx context.Context, userID, id int64, query InboxQuery, refresh bool) (InboxPage, error) {
	if err := query.check(); err != nil {
		return InboxPage{}, err
	}
	c, err := s.active(ctx, userID, id)
	if err != nil {
		return InboxPage{}, err
	}
	key := inboxKey{connection: id, query: query}
	if !refresh {
		s.mu.Lock()
		cached, ok := s.inboxes.get(key, s.now())
		s.mu.Unlock()
		if ok {
			return cached, nil
		}
	}
	page, err := searchInbox(ctx, s.client(c), query)
	if err != nil {
		return InboxPage{}, err
	}
	s.mu.Lock()
	s.inboxes.put(key, page, s.now(), inboxTTL)
	s.mu.Unlock()
	return page, nil
}
