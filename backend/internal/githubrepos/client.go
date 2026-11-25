// Package githubrepos is the GitHub page: the GitHub accounts this
// dashboard's people connect, and the repositories, branches, commits, pull
// requests, issues, workflows and collaborators those accounts reach, read
// and changed through GitHub's REST API.
//
// It is a second GitHub App, not the deploy App in internal/githubapp. That
// one is the dashboard's own identity, installed to build and report on what
// deploys here, and it acts with installation tokens and a webhook. This one
// acts *as a person*: every call carries a user access token GitHub issued to
// the person who connected the account, so GitHub itself decides what each
// call may do, from the intersection of what the App was granted, what the
// installation covers and what that person may do on the repository. Nobody
// gains a power on GitHub by having a dashboard account.
//
// Everything here speaks GitHub's REST API with the standard library, as
// githubapp does. The client below is the one HTTP exchange every call goes
// through: JSON in and out, pagination, and GitHub's refusals classified so
// the API layer can answer each in its own words (a sign-in GitHub no longer
// accepts, a rate limit and when it lifts, a permission, a validation).
package githubrepos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultAPIURL and DefaultWebURL are github.com. Tests point a client at
	// a local server; nothing else changes them.
	DefaultAPIURL = "https://api.github.com"
	DefaultWebURL = "https://github.com"

	apiVersion = "2022-11-28"
	userAgent  = "just-dashboard"
	// maxBody bounds every answer read from GitHub.
	maxBody = 8 << 20
	// MaxPages bounds every walk over a listing: an account with more than
	// 3,000 repositories gets its first 3,000 and a flag that says so, not an
	// unbounded crawl that spends the hour's rate limit.
	MaxPages = 30
)

// TokenSource answers the bearer token for the next request. The connection
// layer refreshes an expiring user token inside it, so a caller never sees
// one GitHub would refuse.
type TokenSource func(ctx context.Context) (string, error)

// Client talks to GitHub's REST API with one token source. It is safe for
// concurrent use.
type Client struct {
	APIURL string
	HTTP   *http.Client
	token  TokenSource
	now    func() time.Time

	mu   sync.Mutex
	rate RateLimit
}

// NewClient builds a client. An empty apiURL is github.com, a nil HTTP client
// a 30-second one, and a nil token source sends no Authorization at all (the
// public endpoints: gitignore templates, licences).
func NewClient(apiURL string, httpClient *http.Client, token TokenSource) *Client {
	if apiURL == "" {
		apiURL = DefaultAPIURL
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{APIURL: strings.TrimRight(apiURL, "/"), HTTP: httpClient, token: token, now: time.Now}
}

// Kind is what a refusal from GitHub means for the person reading it.
type Kind string

const (
	// KindUnauthorized is a 401: the token was revoked, expired, or the App
	// was uninstalled. The connection has to be made again.
	KindUnauthorized Kind = "unauthorized"
	// KindForbidden is a 403 that is not a rate limit: the App, the
	// installation or the person may not do this.
	KindForbidden Kind = "forbidden"
	// KindRateLimited is GitHub's primary or secondary rate limit.
	KindRateLimited Kind = "rate_limited"
	// KindNotFound is a 404, which GitHub also answers for a private
	// repository the token cannot see.
	KindNotFound Kind = "not_found"
	// KindValidation is a 422: the request was understood and refused.
	KindValidation Kind = "validation"
	// KindConflict is a 409 (a merge conflict, an empty repository) or a 405
	// GitHub uses for "not mergeable".
	KindConflict Kind = "conflict"
	// KindUnavailable is a 5xx, an unreadable answer or no route to GitHub.
	KindUnavailable Kind = "unavailable"
	// KindOther is any other status.
	KindOther Kind = "other"
)

// APIError is GitHub's answer when it is not the one asked for. Path is the
// API path that was called; it never carries a token, which travels only in
// the Authorization header.
type APIError struct {
	Status  int
	Kind    Kind
	Message string
	// Details are the messages of a 422's `errors` list, which is where
	// GitHub says *which* field it refused ("name already exists on this
	// account").
	Details []string
	Path    string
	// RetryAt is when a rate limit lifts, from x-ratelimit-reset or
	// retry-after. Zero when GitHub did not say.
	RetryAt time.Time
	err     error
}

func (e *APIError) Error() string {
	text := e.Message
	if len(e.Details) > 0 {
		if text != "" {
			text += ": "
		}
		text += strings.Join(e.Details, "; ")
	}
	if text == "" {
		if e.err != nil {
			return fmt.Sprintf("GitHub could not be reached for %s: %v", e.Path, e.err)
		}
		return fmt.Sprintf("GitHub answered %d to %s", e.Status, e.Path)
	}
	return text
}

func (e *APIError) Unwrap() error { return e.err }

// KindOf is the kind of a GitHub refusal anywhere in err's chain, or "" when
// err did not come from GitHub.
func KindOf(err error) Kind {
	var api *APIError
	if errors.As(err, &api) {
		return api.Kind
	}
	return ""
}

// RateLimit is GitHub's budget as the last answer stated it.
type RateLimit struct {
	Limit     int       `json:"limit"`
	Remaining int       `json:"remaining"`
	ResetAt   time.Time `json:"resetAt,omitzero"`
}

// LastRateLimit is the budget the most recent answer reported, zero before
// the first.
func (c *Client) LastRateLimit() RateLimit {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rate
}

// Response is one answer, kept for the callers that read a header (Link,
// Location) or a status GitHub uses as the answer itself (204 against 201).
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Request sends one call. path is relative to the API root and already
// escaped; body, when not nil, is sent as JSON. A status outside 2xx is an
// *APIError.
func (c *Client) Request(ctx context.Context, method, path string, body any) (*Response, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("github path %q is not absolute", path)
	}
	return c.send(ctx, method, c.APIURL+path, path, body)
}

// send is the one exchange: address is where the call goes, path what an
// error names it by (the two differ only for GraphQL, which is not under the
// REST root on GitHub Enterprise).
func (c *Client) send(ctx context.Context, method, address, path string, body any) (*Response, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, address, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != nil {
		token, err := c.token(ctx)
		if err != nil {
			return nil, err
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	response, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &APIError{Kind: KindUnavailable, Path: path, err: err}
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxBody))
	if err != nil {
		return nil, &APIError{Status: response.StatusCode, Kind: KindUnavailable, Path: path, err: err}
	}
	c.noteRate(response.Header)
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, c.refusal(response.StatusCode, response.Header, raw, path)
	}
	return &Response{Status: response.StatusCode, Header: response.Header, Body: raw}, nil
}

// Do is Request with the answer decoded into out (when out is not nil and
// the answer has a body).
func (c *Client) Do(ctx context.Context, method, path string, body, out any) (*Response, error) {
	response, err := c.Request(ctx, method, path, body)
	if err != nil {
		return response, err
	}
	if out != nil && len(bytes.TrimSpace(response.Body)) > 0 {
		if err := json.Unmarshal(response.Body, out); err != nil {
			return response, &APIError{Status: response.Status, Kind: KindUnavailable, Message: "GitHub's answer was not readable JSON", Path: path, err: err}
		}
	}
	return response, nil
}

// GraphQLURL is where GitHub's GraphQL API answers for a REST root:
// https://api.github.com/graphql on github.com, and on GitHub Enterprise,
// whose REST root is https://host/api/v3, https://host/api/graphql.
func GraphQLURL(apiURL string) string {
	root := strings.TrimRight(apiURL, "/")
	if root == "" {
		root = DefaultAPIURL
	}
	if strings.HasSuffix(root, "/api/v3") {
		return strings.TrimSuffix(root, "/v3") + "/graphql"
	}
	return root + "/graphql"
}

// GraphQLError is a GraphQL answer that carried `errors`. GitHub sends those
// with status 200, so it is not an APIError: the caller decides whether the
// rest of its answer stands without this half.
type GraphQLError struct {
	Messages []string
}

func (e *GraphQLError) Error() string {
	if len(e.Messages) == 0 {
		return "GitHub's GraphQL API answered with errors"
	}
	return strings.Join(e.Messages, "; ")
}

// GraphQL sends one query, with the same token and headers as every REST
// call, and decodes its `data` into out. A status outside 2xx is an
// *APIError as for REST; an answer with `errors` is a *GraphQLError.
func (c *Client) GraphQL(ctx context.Context, query string, out any) error {
	const path = "/graphql"
	response, err := c.send(ctx, http.MethodPost, GraphQLURL(c.APIURL), path, map[string]string{"query": query})
	if err != nil {
		return err
	}
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	garbled := &APIError{Status: response.Status, Kind: KindUnavailable, Message: "GitHub's answer was not readable JSON", Path: path}
	if err := json.Unmarshal(response.Body, &envelope); err != nil {
		garbled.err = err
		return garbled
	}
	if len(envelope.Errors) > 0 {
		refused := &GraphQLError{}
		for _, item := range envelope.Errors {
			if message := strings.TrimSpace(item.Message); message != "" {
				refused.Messages = append(refused.Messages, message)
			}
		}
		return refused
	}
	if out == nil {
		return nil
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return garbled
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		garbled.err = err
		return garbled
	}
	return nil
}

// Page reads one page of a listing into out and reports whether GitHub's
// Link header names a next one. path carries its own page and per_page.
func (c *Client) Page(ctx context.Context, path string, out any) (bool, error) {
	response, err := c.Do(ctx, http.MethodGet, path, nil, out)
	if err != nil {
		return false, err
	}
	return NextLink(response.Header.Get("Link")) != "", nil
}

// ErrStopWalk ends a Walk early without an error.
var ErrStopWalk = errors.New("stop walking")

// Walk follows a listing's Link pagination from path until the last page,
// MaxPages, or the visitor returns ErrStopWalk. truncated is true when pages
// remained at the cap.
func (c *Client) Walk(ctx context.Context, path string, visit func(page []byte) error) (truncated bool, err error) {
	next := path
	for page := 0; next != ""; page++ {
		if page == MaxPages {
			return true, nil
		}
		response, err := c.Request(ctx, http.MethodGet, next, nil)
		if err != nil {
			return false, err
		}
		if err := visit(response.Body); err != nil {
			if errors.Is(err, ErrStopWalk) {
				return false, nil
			}
			return false, err
		}
		next = NextLink(response.Header.Get("Link"))
	}
	return false, nil
}

// NextLink reads the rel="next" target out of a Link header as a path
// relative to the API root, so a host named in a header is never followed.
func NextLink(header string) string {
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		if len(fields) < 2 {
			continue
		}
		target := strings.Trim(strings.TrimSpace(fields[0]), "<>")
		for _, attribute := range fields[1:] {
			if strings.TrimSpace(attribute) == `rel="next"` {
				parsed, err := url.Parse(target)
				if err != nil || parsed.Path == "" {
					return ""
				}
				return parsed.RequestURI()
			}
		}
	}
	return ""
}

func (c *Client) noteRate(header http.Header) {
	limit, errLimit := strconv.Atoi(header.Get("X-RateLimit-Limit"))
	remaining, errRemaining := strconv.Atoi(header.Get("X-RateLimit-Remaining"))
	if errLimit != nil || errRemaining != nil {
		return
	}
	rate := RateLimit{Limit: limit, Remaining: remaining}
	if reset, err := strconv.ParseInt(header.Get("X-RateLimit-Reset"), 10, 64); err == nil && reset > 0 {
		rate.ResetAt = time.Unix(reset, 0).UTC()
	}
	c.mu.Lock()
	c.rate = rate
	c.mu.Unlock()
}

// refusal classifies a non-2xx answer. A 403 or 429 is a rate limit when
// GitHub says so — the remaining budget is zero, it sent retry-after, or the
// message names the secondary limit — and a plain refusal otherwise.
func (c *Client) refusal(status int, header http.Header, raw []byte, path string) *APIError {
	var failure struct {
		Message string            `json:"message"`
		Errors  []json.RawMessage `json:"errors"`
	}
	_ = json.Unmarshal(raw, &failure)
	refusal := &APIError{Status: status, Message: strings.TrimSpace(failure.Message), Path: path}
	for _, item := range failure.Errors {
		if detail := validationDetail(item); detail != "" {
			refusal.Details = append(refusal.Details, detail)
		}
	}
	switch {
	case status == http.StatusUnauthorized:
		refusal.Kind = KindUnauthorized
	case (status == http.StatusForbidden || status == http.StatusTooManyRequests) && c.rateLimited(header, refusal):
		refusal.Kind = KindRateLimited
	case status == http.StatusTooManyRequests:
		refusal.Kind = KindRateLimited
	case status == http.StatusForbidden:
		refusal.Kind = KindForbidden
	case status == http.StatusNotFound:
		refusal.Kind = KindNotFound
	case status == http.StatusUnprocessableEntity:
		refusal.Kind = KindValidation
	case status == http.StatusConflict || status == http.StatusMethodNotAllowed:
		refusal.Kind = KindConflict
	case status >= 500:
		refusal.Kind = KindUnavailable
	default:
		refusal.Kind = KindOther
	}
	return refusal
}

func (c *Client) rateLimited(header http.Header, refusal *APIError) bool {
	if seconds, err := strconv.Atoi(strings.TrimSpace(header.Get("Retry-After"))); err == nil && seconds >= 0 {
		refusal.RetryAt = c.now().Add(time.Duration(seconds) * time.Second).UTC()
		return true
	}
	if header.Get("X-RateLimit-Remaining") == "0" {
		if reset, err := strconv.ParseInt(header.Get("X-RateLimit-Reset"), 10, 64); err == nil && reset > 0 {
			refusal.RetryAt = time.Unix(reset, 0).UTC()
		}
		return true
	}
	if strings.Contains(strings.ToLower(refusal.Message), "rate limit") {
		// The secondary limit names itself in the message and asks for a
		// minute's pause when it sends no header.
		refusal.RetryAt = c.now().Add(time.Minute).UTC()
		return true
	}
	return false
}

// validationDetail is one entry of a 422's errors list in words. GitHub sends
// either a string or an object with a message, or a code and a field.
func validationDetail(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text)
	}
	var item struct {
		Resource string `json:"resource"`
		Field    string `json:"field"`
		Code     string `json:"code"`
		Message  string `json:"message"`
	}
	if json.Unmarshal(raw, &item) != nil {
		return ""
	}
	if item.Message != "" {
		return strings.TrimSpace(item.Message)
	}
	switch item.Code {
	case "already_exists":
		return fmt.Sprintf("%s %s already exists", strings.ToLower(item.Resource), item.Field)
	case "missing_field":
		return fmt.Sprintf("%s is required", item.Field)
	case "invalid":
		return fmt.Sprintf("%s is not valid", item.Field)
	case "":
		return ""
	default:
		return strings.TrimSpace(item.Field + " " + strings.ReplaceAll(item.Code, "_", " "))
	}
}

var (
	// ownerRE is GitHub's rule for a login, loosened the way GitHub's own
	// older accounts need: letters, digits and hyphens, not starting with a
	// hyphen, at most 39 characters.
	ownerRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)
	// repoNameRE is what GitHub accepts as a repository name.
	repoNameRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
)

// ValidOwner reports whether s can be a GitHub account or organisation login.
func ValidOwner(s string) bool { return ownerRE.MatchString(s) }

// ValidRepoName reports whether s can be a repository name. "." and ".." are
// refused here as GitHub refuses them, and because either would change the
// path built from it.
func ValidRepoName(s string) bool {
	return repoNameRE.MatchString(s) && s != "." && s != ".."
}

// RepoPath is the API path of one repository, built only from a valid owner
// and name so nothing a request carries can step outside /repos/.
func RepoPath(owner, name string) (string, error) {
	if !ValidOwner(owner) || !ValidRepoName(name) {
		return "", fmt.Errorf("%q/%q is not a GitHub repository name", owner, name)
	}
	return "/repos/" + owner + "/" + name, nil
}

// EscapeRef is a branch or ref name as a path, each segment escaped and the
// slashes kept, which is how GitHub reads refs/heads/feature/x.
func EscapeRef(ref string) string {
	parts := strings.Split(ref, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}
