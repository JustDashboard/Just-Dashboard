// Package githubapp is the dashboard's own identity on GitHub: a GitHub App
// created once through GitHub's manifest flow and installed on the accounts
// whose repositories deploy here.
//
// It replaces three things an operator used to do by hand. Every repository
// no longer needs a webhook and a pasted secret: the App's single webhook
// delivers for all of them and the dashboard routes each delivery to the
// triggers that asked for it. Private clones and commit statuses no longer
// borrow somebody's personal token: the App mints short-lived installation
// tokens as it needs them. And a pull request no longer has to be checked
// against a run log: the App writes the preview's address and state back on
// the pull request itself.
//
// Everything here speaks GitHub's REST API directly with the standard
// library. The one piece of cryptography, the RS256 JSON web token an App
// authenticates with, is a hundred lines of crypto/rsa rather than a
// dependency.
package githubapp

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
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
	// a local server; nothing else ever changes them.
	DefaultAPIURL = "https://api.github.com"
	DefaultWebURL = "https://github.com"

	apiVersion = "2022-11-28"
	userAgent  = "just-dashboard"
	// maxPages bounds every listing walk: an account with more than 3,000
	// repositories gets its first 3,000, not an unbounded crawl.
	maxPages = 30
)

var (
	// ErrNotConfigured means no App has been created for this dashboard yet.
	ErrNotConfigured = errors.New("no GitHub App is connected to this dashboard")
	// ErrNotInstalled means the App exists but the repository's owner has not
	// installed it, or did not grant it this repository.
	ErrNotInstalled = errors.New("the GitHub App is not installed on this repository")
	// ErrBadState is a manifest callback whose state this dashboard did not
	// issue, or issued more than fifteen minutes ago.
	ErrBadState = errors.New("the GitHub App setup link has expired; start again from the dashboard")
)

var (
	repositoryRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	commitSHARE  = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// Credentials is everything GitHub hands back when the App is created. The
// three secrets never leave the process unsealed except inside an
// Authorization header.
type Credentials struct {
	ID            int64     `json:"id"`
	Slug          string    `json:"slug"`
	Name          string    `json:"name"`
	Owner         string    `json:"owner"`
	HTMLURL       string    `json:"htmlUrl"`
	ClientID      string    `json:"clientId"`
	ClientSecret  string    `json:"-"`
	WebhookSecret string    `json:"-"`
	PrivateKey    string    `json:"-"`
	CreatedAt     time.Time `json:"createdAt"`
}

// Installation is one account that installed the App.
type Installation struct {
	ID                  int64  `json:"id"`
	Account             string `json:"account"`
	AccountType         string `json:"accountType"`
	HTMLURL             string `json:"htmlUrl"`
	RepositorySelection string `json:"repositorySelection"`
	// CredentialID is the deploy credential that mints this installation's
	// tokens for clones; the service fills it in.
	CredentialID int64 `json:"credentialId,omitempty"`
}

// Repository is one repository an installation grants.
type Repository struct {
	InstallationID int64  `json:"installationId"`
	Account        string `json:"account"`
	NameWithOwner  string `json:"nameWithOwner"`
	Name           string `json:"name"`
	Description    string `json:"description,omitempty"`
	Language       string `json:"language,omitempty"`
	Private        bool   `json:"private"`
	DefaultBranch  string `json:"defaultBranch"`
	CloneURL       string `json:"cloneUrl"`
	HTMLURL        string `json:"htmlUrl"`
	// PushedAt, Fork and Archived are what the import picker sorts and marks
	// by. GitHub returns them on the same listing; without them a repository
	// the App grants could not be told apart from one abandoned two years ago,
	// while the CLI's own listing had carried all three since it existed.
	PushedAt     string `json:"pushedAt,omitempty"`
	Fork         bool   `json:"fork,omitempty"`
	Archived     bool   `json:"archived,omitempty"`
	CredentialID int64  `json:"credentialId,omitempty"`
}

// CommitStatus is one state posted against a commit.
type CommitStatus struct {
	State       string
	TargetURL   string
	Description string
	Context     string
}

// APIError is GitHub's answer when it is not the one asked for.
type APIError struct {
	Status  int
	Message string
	Path    string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("GitHub answered %d to %s", e.Status, e.Path)
	}
	return fmt.Sprintf("GitHub answered %d to %s: %s", e.Status, e.Path, e.Message)
}

// Client talks to GitHub as one App. It is safe for concurrent use; the
// installation token cache is the only shared state.
type Client struct {
	APIURL string
	HTTP   *http.Client

	credentials Credentials
	key         *rsa.PrivateKey
	now         func() time.Time

	mu     sync.Mutex
	tokens map[int64]installationToken
}

type installationToken struct {
	value   string
	expires time.Time
}

// NewClient parses the App's private key once. A key GitHub did not issue
// (PKCS#1 "RSA PRIVATE KEY", occasionally PKCS#8) is refused here rather than
// on the first request.
func NewClient(credentials Credentials) (*Client, error) {
	key, err := parsePrivateKey(credentials.PrivateKey)
	if err != nil {
		return nil, err
	}
	return &Client{
		APIURL: DefaultAPIURL, HTTP: &http.Client{Timeout: 30 * time.Second},
		credentials: credentials, key: key, now: time.Now, tokens: map[int64]installationToken{},
	}, nil
}

func parsePrivateKey(raw string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, errors.New("the GitHub App private key is not PEM encoded")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		key, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("the GitHub App private key is not an RSA key")
		}
		return key, nil
	default:
		return nil, fmt.Errorf("the GitHub App private key block is %q, not a private key", block.Type)
	}
}

// JWT is the App's own credential: ten minutes long, issued a minute in the
// past so a clock slightly ahead of GitHub's does not make it "not yet
// valid", and signed with the private key GitHub generated for the App.
func (c *Client) JWT() (string, error) {
	now := c.now().Unix()
	issuer := c.credentials.ClientID
	if issuer == "" {
		issuer = strconv.FormatInt(c.credentials.ID, 10)
	}
	claims, err := json.Marshal(map[string]any{"iat": now - 60, "exp": now + 9*60, "iss": issuer})
	if err != nil {
		return "", err
	}
	signing := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signing))
	signature, err := rsa.SignPKCS1v15(rand.Reader, c.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// Installations lists every account that installed the App.
func (c *Client) Installations(ctx context.Context) ([]Installation, error) {
	token, err := c.JWT()
	if err != nil {
		return nil, err
	}
	installations := []Installation{}
	err = c.walk(ctx, "/app/installations?per_page=100", token, func(page []byte) error {
		var raw []struct {
			ID      int64 `json:"id"`
			Account struct {
				Login   string `json:"login"`
				Type    string `json:"type"`
				HTMLURL string `json:"html_url"`
			} `json:"account"`
			HTMLURL             string `json:"html_url"`
			RepositorySelection string `json:"repository_selection"`
		}
		if err := json.Unmarshal(page, &raw); err != nil {
			return err
		}
		for _, item := range raw {
			installations = append(installations, Installation{
				ID: item.ID, Account: item.Account.Login, AccountType: item.Account.Type,
				HTMLURL: item.HTMLURL, RepositorySelection: item.RepositorySelection,
			})
		}
		return nil
	})
	return installations, err
}

// InstallationToken mints, or reuses, the hour-long token an installation
// acts with. A token is replaced two minutes before GitHub would refuse it,
// so a clone that starts near the end of one never fails halfway through.
func (c *Client) InstallationToken(ctx context.Context, installationID int64) (string, error) {
	if installationID <= 0 {
		return "", ErrNotInstalled
	}
	c.mu.Lock()
	cached, ok := c.tokens[installationID]
	c.mu.Unlock()
	if ok && c.now().Before(cached.expires.Add(-2*time.Minute)) {
		return cached.value, nil
	}
	jwt, err := c.JWT()
	if err != nil {
		return "", err
	}
	var minted struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	path := fmt.Sprintf("/app/installations/%d/access_tokens", installationID)
	if _, err := c.do(ctx, http.MethodPost, path, jwt, map[string]any{}, &minted); err != nil {
		var api *APIError
		if errors.As(err, &api) && api.Status == http.StatusNotFound {
			return "", ErrNotInstalled
		}
		return "", err
	}
	if minted.Token == "" {
		return "", &APIError{Status: http.StatusOK, Message: "installation token response carried no token", Path: path}
	}
	c.mu.Lock()
	c.tokens[installationID] = installationToken{value: minted.Token, expires: minted.ExpiresAt}
	c.mu.Unlock()
	return minted.Token, nil
}

// InstallationRepositories lists what one installation grants.
func (c *Client) InstallationRepositories(ctx context.Context, installation Installation) ([]Repository, error) {
	token, err := c.InstallationToken(ctx, installation.ID)
	if err != nil {
		return nil, err
	}
	repositories := []Repository{}
	err = c.walk(ctx, "/installation/repositories?per_page=100", token, func(page []byte) error {
		var raw struct {
			Repositories []struct {
				FullName      string `json:"full_name"`
				Name          string `json:"name"`
				Description   string `json:"description"`
				Language      string `json:"language"`
				Private       bool   `json:"private"`
				DefaultBranch string `json:"default_branch"`
				CloneURL      string `json:"clone_url"`
				HTMLURL       string `json:"html_url"`
				PushedAt      string `json:"pushed_at"`
				Fork          bool   `json:"fork"`
				Archived      bool   `json:"archived"`
			} `json:"repositories"`
		}
		if err := json.Unmarshal(page, &raw); err != nil {
			return err
		}
		for _, item := range raw.Repositories {
			repositories = append(repositories, Repository{
				InstallationID: installation.ID, Account: installation.Account, CredentialID: installation.CredentialID,
				NameWithOwner: item.FullName, Name: item.Name, Description: item.Description, Language: item.Language,
				Private: item.Private, DefaultBranch: item.DefaultBranch, CloneURL: item.CloneURL, HTMLURL: item.HTMLURL,
				PushedAt: item.PushedAt, Fork: item.Fork, Archived: item.Archived,
			})
		}
		return nil
	})
	return repositories, err
}

// RepositoryInstallation finds which installation grants a repository, or
// reports that none does.
func (c *Client) RepositoryInstallation(ctx context.Context, nameWithOwner string) (int64, error) {
	if !repositoryRE.MatchString(nameWithOwner) {
		return 0, fmt.Errorf("repository %q is not owner/name", nameWithOwner)
	}
	jwt, err := c.JWT()
	if err != nil {
		return 0, err
	}
	var installation struct {
		ID int64 `json:"id"`
	}
	if _, err := c.do(ctx, http.MethodGet, "/repos/"+nameWithOwner+"/installation", jwt, nil, &installation); err != nil {
		var api *APIError
		if errors.As(err, &api) && api.Status == http.StatusNotFound {
			return 0, ErrNotInstalled
		}
		return 0, err
	}
	if installation.ID == 0 {
		return 0, ErrNotInstalled
	}
	return installation.ID, nil
}

// PostCommitStatus writes one status against a commit as the App.
func (c *Client) PostCommitStatus(ctx context.Context, installationID int64, nameWithOwner, sha string, status CommitStatus) error {
	if !repositoryRE.MatchString(nameWithOwner) {
		return fmt.Errorf("repository %q is not owner/name", nameWithOwner)
	}
	sha = strings.ToLower(strings.TrimSpace(sha))
	if !commitSHARE.MatchString(sha) {
		return fmt.Errorf("commit %q is not a full object id", sha)
	}
	switch status.State {
	case "pending", "success", "failure", "error":
	default:
		return fmt.Errorf("commit status state %q is not accepted by GitHub", status.State)
	}
	token, err := c.InstallationToken(ctx, installationID)
	if err != nil {
		return err
	}
	description := strings.TrimSpace(status.Description)
	if len(description) > 140 {
		// GitHub rejects longer descriptions outright rather than truncating.
		description = description[:137] + "..."
	}
	body := map[string]any{"state": status.State, "context": status.Context}
	if description != "" {
		body["description"] = description
	}
	if target := strings.TrimSpace(status.TargetURL); target != "" {
		body["target_url"] = target
	}
	_, err = c.do(ctx, http.MethodPost, "/repos/"+nameWithOwner+"/statuses/"+sha, token, body, nil)
	return err
}

// UpsertPullRequestComment keeps one comment per marker on a pull request:
// the first call creates it and every later one edits it in place, so a
// pull request with ten pushes shows one current line, not ten stale ones.
func (c *Client) UpsertPullRequestComment(ctx context.Context, installationID int64, nameWithOwner string, number int, marker, body string) error {
	if !repositoryRE.MatchString(nameWithOwner) {
		return fmt.Errorf("repository %q is not owner/name", nameWithOwner)
	}
	if number <= 0 || strings.TrimSpace(marker) == "" {
		return errors.New("a pull request comment needs a pull request number and a marker")
	}
	token, err := c.InstallationToken(ctx, installationID)
	if err != nil {
		return err
	}
	existing := int64(0)
	err = c.walk(ctx, fmt.Sprintf("/repos/%s/issues/%d/comments?per_page=100", nameWithOwner, number), token, func(page []byte) error {
		var raw []struct {
			ID   int64  `json:"id"`
			Body string `json:"body"`
		}
		if err := json.Unmarshal(page, &raw); err != nil {
			return err
		}
		for _, comment := range raw {
			if strings.Contains(comment.Body, marker) {
				existing = comment.ID
				return errStopWalk
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	content := map[string]any{"body": marker + "\n" + body}
	if existing != 0 {
		_, err = c.do(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/issues/comments/%d", nameWithOwner, existing), token, content, nil)
		return err
	}
	_, err = c.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", nameWithOwner, number), token, content, nil)
	return err
}

var errStopWalk = errors.New("stop")

// walk follows GitHub's Link pagination until the last page, the page cap,
// or the visitor says it has seen enough.
func (c *Client) walk(ctx context.Context, path, token string, visit func(page []byte) error) error {
	next := path
	for page := 0; next != "" && page < maxPages; page++ {
		raw, header, err := c.request(ctx, http.MethodGet, next, token, nil)
		if err != nil {
			return err
		}
		if err := visit(raw); err != nil {
			if errors.Is(err, errStopWalk) {
				return nil
			}
			return err
		}
		next = nextLink(header.Get("Link"))
	}
	return nil
}

// nextLink reads the rel="next" target out of a Link header, as a path
// relative to the API root so a test server's host is never trusted twice.
func nextLink(header string) string {
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		if len(fields) < 2 {
			continue
		}
		target := strings.Trim(strings.TrimSpace(fields[0]), "<>")
		for _, attribute := range fields[1:] {
			if strings.TrimSpace(attribute) == `rel="next"` {
				parsed, err := url.Parse(target)
				if err != nil {
					return ""
				}
				return parsed.RequestURI()
			}
		}
	}
	return ""
}

func (c *Client) do(ctx context.Context, method, path, token string, body, out any) (http.Header, error) {
	raw, header, err := c.request(ctx, method, path, token, body)
	if err != nil {
		return header, err
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return header, &APIError{Status: http.StatusOK, Message: "unreadable JSON answer", Path: path}
		}
	}
	return header, nil
}

func (c *Client) request(ctx context.Context, method, path, token string, body any) ([]byte, http.Header, error) {
	return request(ctx, c.HTTP, c.APIURL, method, path, token, body)
}

// request is the one HTTP exchange every call goes through: JSON in, JSON
// out, GitHub's error message surfaced with its status, bodies bounded.
func request(ctx context.Context, client *http.Client, base, method, path, token string, body any) ([]byte, http.Header, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, reader)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", userAgent)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, response.Header, err
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		var failure struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &failure)
		return nil, response.Header, &APIError{Status: response.StatusCode, Message: failure.Message, Path: path}
	}
	return raw, response.Header, nil
}
