package deploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// The Git hosts a run is reported back to. Gitea and Forgejo share one API,
// so a Forgejo host (Codeberg among them) is a ForgeGitea.
const (
	ForgeGitHub = "github"
	ForgeGitLab = "gitlab"
	ForgeGitea  = "gitea"
)

// The two things written back, and the key of their last outcome.
const (
	forgeReportStatus  = "status"
	forgeReportComment = "comment"
)

var (
	// ErrForgeUnsupported is a source on a host that is neither GitLab nor
	// Gitea/Forgejo (GitHub has its own path), or no Git remote at all.
	ErrForgeUnsupported = errors.New("this Git host has no commit status API the dashboard speaks")
	// ErrForgeNoToken is a supported host with no saved token to write as.
	ErrForgeNoToken = errors.New("no saved token for this Git host")
)

// ForgeToken is a credential the dashboard may write to a Git host's API
// with: the source's own HTTPS token, or a provider token saved for the host.
type ForgeToken struct {
	ID     int64
	Name   string
	Secret string
}

// ForgeTokens finds the token for a host. sourceCredentialID is the
// credential the source clones with, zero for a public clone.
type ForgeTokens interface {
	ForgeToken(ctx context.Context, sourceCredentialID int64, host string) (ForgeToken, bool, error)
}

// ForgeEndpoint is one repository on a GitLab or Gitea host, resolved far
// enough to call: the API's origin (with any subpath the host is served
// under), the repository's path there, and the token to write as.
type ForgeEndpoint struct {
	Provider string
	// Product is what to call the host: gitlab, gitea, forgejo or codeberg.
	// Forgejo speaks Gitea's API, so only its name differs.
	Product    string
	Host       string
	Origin     string
	Repository string
	Credential string
	token      string
	tokenID    int64
}

// ForgeReport is the last outcome of one kind of write-back.
type ForgeReport struct {
	Provider   string    `json:"provider"`
	Repository string    `json:"repository,omitempty"`
	Revision   string    `json:"revision,omitempty"`
	State      string    `json:"state"`
	RunID      int64     `json:"runId,omitempty"`
	OK         bool      `json:"ok"`
	Message    string    `json:"message,omitempty"`
	At         time.Time `json:"at"`
}

// CommitStatusReading is what Settings shows under "Report each release as a
// commit status": where statuses go, as whom, and whether the host took the
// last one.
type CommitStatusReading struct {
	// Provider is github, gitlab or gitea; empty when the source is on no
	// host the dashboard reports to.
	Provider string `json:"provider,omitempty"`
	// Product names the host's software for the page: github, gitlab, gitea,
	// forgejo or codeberg.
	Product    string `json:"product,omitempty"`
	Host       string `json:"host,omitempty"`
	Repository string `json:"repository,omitempty"`
	// Context is the status's name on the host, just-dashboard/<environment>.
	Context string `json:"context,omitempty"`
	// Credential names the saved token a GitLab or Gitea status is written
	// with. GitHub uses the App or the dashboard's gh login instead.
	Credential string `json:"credential,omitempty"`
	// Ready is false when nothing can be written: Reason says why.
	Ready       bool         `json:"ready"`
	Reason      string       `json:"reason,omitempty"`
	LastStatus  *ForgeReport `json:"lastStatus,omitempty"`
	LastComment *ForgeReport `json:"lastComment,omitempty"`
}

// forgeHTTPError is a host's refusal, carried so the reading can say what
// the host answered without the caller parsing text.
type forgeHTTPError struct {
	Provider string
	Status   int
	Message  string
}

func (e *forgeHTTPError) Error() string {
	return forgeRefusal(e.Provider, e.Status, e.Message)
}

// forgeRefusal turns a host's answer into a sentence an operator can act on.
// The common causes of each code are named, in the host's own terms, because
// "403" alone sends somebody to read the provider's documentation.
func forgeRefusal(provider string, status int, message string) string {
	var reason string
	switch status {
	case http.StatusUnauthorized:
		reason = "the host refused the token (401): it is wrong, expired or revoked"
	case http.StatusForbidden:
		reason = "the token may not write here (403)"
		switch provider {
		case ForgeGitLab:
			reason += ": it needs the api scope and at least the Developer role"
		case ForgeGitea:
			reason += ": it needs write:repository for statuses, and write:issue with read:user for preview comments"
		}
	case http.StatusNotFound:
		reason = "the host does not show this repository, commit or request to the token (404)"
	default:
		reason = fmt.Sprintf("the host answered %d", status)
	}
	if message = strings.TrimSpace(message); message != "" {
		reason += ": " + message
	}
	return reason
}

// ForgeClient writes commit statuses and preview comments to GitLab and
// Gitea/Forgejo hosts. Which of the two a self-hosted address runs is asked
// of the host itself, once an hour, rather than configured.
type ForgeClient struct {
	tokens ForgeTokens
	http   *http.Client
	now    func() time.Time

	mu     sync.Mutex
	kinds  map[string]forgeDetection
	owners map[string]forgeOwner
}

type forgeDetection struct {
	provider string
	product  string
	origin   string
	path     string
	until    time.Time
}

type forgeOwner struct {
	id    int64
	until time.Time
}

func NewForgeClient(tokens ForgeTokens) *ForgeClient {
	return &ForgeClient{
		tokens: tokens,
		// A host that redirects an API call is not followed: a redirect is
		// where a token would leave for an address nobody chose.
		http: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
		now:    time.Now,
		kinds:  map[string]forgeDetection{},
		owners: map[string]forgeOwner{},
	}
}

// forgeRemote is a Git remote broken into what an API call needs.
type forgeRemote struct {
	scheme   string
	host     string // with its port, as a credential's target names it
	hostname string
	segments []string
}

// parseForgeRemote reads https, http, ssh:// and scp-style (git@host:path)
// remotes. An SSH remote's API is the host's HTTPS side; its SSH port says
// nothing about where that is.
func parseForgeRemote(remote string) (forgeRemote, bool) {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return forgeRemote{}, false
	}
	var result forgeRemote
	var path string
	if parsed, err := url.Parse(remote); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		switch strings.ToLower(parsed.Scheme) {
		case "https", "http":
			result.scheme = strings.ToLower(parsed.Scheme)
			result.host = strings.ToLower(parsed.Host)
		case "ssh", "git+ssh":
			result.scheme = "https"
			result.host = strings.ToLower(parsed.Hostname())
		default:
			return forgeRemote{}, false
		}
		result.hostname = strings.ToLower(parsed.Hostname())
		path = parsed.Path
	} else if at := strings.Index(remote, "@"); at >= 0 && strings.Contains(remote[at:], ":") && !strings.Contains(remote, "://") {
		hostAndPath := remote[at+1:]
		host, rest, _ := strings.Cut(hostAndPath, ":")
		result.scheme = "https"
		result.host = strings.ToLower(host)
		result.hostname = result.host
		path = rest
	} else {
		return forgeRemote{}, false
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "?#%\\\x00\r\n") {
			return forgeRemote{}, false
		}
		result.segments = append(result.segments, part)
	}
	if len(result.segments) < 2 || result.hostname == "" {
		return forgeRemote{}, false
	}
	return result, true
}

func (r forgeRemote) origin(prefix []string) string {
	origin := r.scheme + "://" + r.host
	if len(prefix) > 0 {
		origin += "/" + strings.Join(prefix, "/")
	}
	return origin
}

// knownForges are hosts whose software is not in doubt, so nothing is asked:
// the API each speaks and what it is called.
var knownForges = map[string][2]string{
	"gitlab.com":   {ForgeGitLab, "gitlab"},
	"codeberg.org": {ForgeGitea, "codeberg"},
	"gitea.com":    {ForgeGitea, "gitea"},
}

// tokenTransportAllowed is the rule every other credentialed call in the
// dashboard follows: a token travels over TLS, or to this machine.
func tokenTransportAllowed(remote forgeRemote) bool {
	if remote.scheme == "https" {
		return true
	}
	ip := net.ParseIP(strings.Trim(remote.hostname, "[]"))
	return ip != nil && ip.IsLoopback()
}

// Resolve finds the host software, the API origin and the token for a
// source. A host it cannot place returns ErrForgeUnsupported; a supported
// host without a token returns the endpoint with ErrForgeNoToken, so the
// reading can still name the host.
func (c *ForgeClient) Resolve(ctx context.Context, source SourceIdentity, credentialID int64) (ForgeEndpoint, error) {
	remote, ok := parseForgeRemote(source.Remote)
	if !ok || remote.hostname == "github.com" || remote.hostname == "www.github.com" {
		return ForgeEndpoint{}, ErrForgeUnsupported
	}
	var token ForgeToken
	var hasToken bool
	if c.tokens != nil {
		var err error
		token, hasToken, err = c.tokens.ForgeToken(ctx, credentialID, remote.host)
		if err != nil {
			return ForgeEndpoint{}, err
		}
	}
	if hasToken && !tokenTransportAllowed(remote) {
		return ForgeEndpoint{}, fmt.Errorf("a token is never sent over plain http; use the host's https address for this source")
	}
	sendToken := ""
	if hasToken {
		sendToken = token.Secret
	}
	detection, err := c.detect(ctx, remote, sendToken)
	if err != nil {
		return ForgeEndpoint{}, err
	}
	endpoint := ForgeEndpoint{
		Provider: detection.provider, Product: detection.product, Host: remote.host, Origin: detection.origin, Repository: detection.path,
	}
	if !hasToken {
		return endpoint, ErrForgeNoToken
	}
	endpoint.Credential, endpoint.token, endpoint.tokenID = token.Name, token.Secret, token.ID
	return endpoint, nil
}

// detect asks a self-hosted address which software it runs. Gitea and
// Forgejo answer /api/v1/version to anyone; a GitLab answers /api/v4/version
// to a token, and without one still marks its refusal with X-Gitlab-Meta. A
// GitLab may sit under a subpath and nest groups, so each split of the
// remote's path into "where GitLab is" and "the project" is tried in turn.
func (c *ForgeClient) detect(ctx context.Context, remote forgeRemote, token string) (forgeDetection, error) {
	key := remote.scheme + "://" + remote.host + "/" + strings.Join(remote.segments, "/")
	now := c.now()
	c.mu.Lock()
	cached, ok := c.kinds[key]
	c.mu.Unlock()
	if ok && now.Before(cached.until) {
		if cached.provider == "" {
			return forgeDetection{}, ErrForgeUnsupported
		}
		return cached, nil
	}
	found := forgeDetection{}
	if known, ok := knownForges[remote.hostname]; ok {
		found = forgeDetection{provider: known[0], product: known[1], origin: remote.origin(nil), path: strings.Join(remote.segments, "/")}
	} else {
		found = c.probe(ctx, remote, token)
	}
	// An unreachable host is asked again soon; an answer holds for an hour.
	found.until = now.Add(time.Hour)
	if found.provider == "" {
		found.until = now.Add(2 * time.Minute)
	}
	c.mu.Lock()
	c.kinds[key] = found
	if len(c.kinds) > 1024 {
		c.kinds = map[string]forgeDetection{key: found}
	}
	c.mu.Unlock()
	if found.provider == "" {
		return forgeDetection{}, ErrForgeUnsupported
	}
	return found, nil
}

func (c *ForgeClient) probe(ctx context.Context, remote forgeRemote, token string) forgeDetection {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	segments := remote.segments
	giteaPrefix := segments[:len(segments)-2]
	if version, ok := c.answersVersion(ctx, remote.origin(giteaPrefix)+"/api/v1/version", "", ""); ok {
		// Forgejo answers with its own version and the Gitea one it is
		// compatible with: "11.0.16+gitea-1.22.0".
		product := "gitea"
		if strings.Contains(version, "+gitea") || strings.Contains(strings.ToLower(version), "forgejo") {
			product = "forgejo"
		}
		return forgeDetection{provider: ForgeGitea, product: product, origin: remote.origin(giteaPrefix), path: strings.Join(segments[len(segments)-2:], "/")}
	}
	for split := 0; split <= len(segments)-2; split++ {
		origin := remote.origin(segments[:split])
		if _, ok := c.answersVersion(ctx, origin+"/api/v4/version", "Authorization", bearer(token)); ok {
			return forgeDetection{provider: ForgeGitLab, product: "gitlab", origin: origin, path: strings.Join(segments[split:], "/")}
		}
	}
	return forgeDetection{}
}

func bearer(token string) string {
	if token == "" {
		return ""
	}
	return "Bearer " + token
}

func (c *ForgeClient) answersVersion(ctx context.Context, endpoint, header, value string) (string, bool) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", false
	}
	request.Header.Set("Accept", "application/json")
	if header != "" && value != "" {
		request.Header.Set(header, value)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return "", false
	}
	defer response.Body.Close()
	if strings.HasSuffix(endpoint, "/api/v4/version") && response.StatusCode == http.StatusUnauthorized &&
		response.Header.Get("X-Gitlab-Meta") != "" {
		return "", true
	}
	if response.StatusCode != http.StatusOK {
		return "", false
	}
	var answer struct {
		Version string `json:"version"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&answer) != nil || answer.Version == "" {
		return "", false
	}
	return answer.Version, true
}

// call makes one authenticated API request and decodes a JSON answer into
// result when one is given.
func (c *ForgeClient) call(ctx context.Context, endpoint ForgeEndpoint, method, path string, body, result any) (http.Header, error) {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.Origin+path, payload)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Just-Dashboard")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	switch endpoint.Provider {
	case ForgeGitLab:
		request.Header.Set("Authorization", "Bearer "+endpoint.token)
	case ForgeGitea:
		request.Header.Set("Authorization", "token "+endpoint.token)
	default:
		return nil, ErrForgeUnsupported
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, &forgeHTTPError{Provider: endpoint.Provider, Status: response.StatusCode, Message: forgeErrorMessage(raw)}
	}
	if result != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, result); err != nil {
			return nil, fmt.Errorf("the host's answer was not the JSON its API documents: %w", err)
		}
	}
	return response.Header, nil
}

// forgeErrorMessage keeps the host's own words about a refusal, bounded:
// GitLab says {"message": …} (a string or a field map) or, for a token's
// scope, {"error_description": …}; Gitea says {"message": …}.
func forgeErrorMessage(raw []byte) string {
	var answer struct {
		Message     json.RawMessage `json:"message"`
		Error       string          `json:"error"`
		Description string          `json:"error_description"`
	}
	if json.Unmarshal(raw, &answer) != nil {
		return ""
	}
	message := answer.Error
	if answer.Description != "" {
		message = answer.Description
	}
	if len(answer.Message) > 0 {
		var text string
		if json.Unmarshal(answer.Message, &text) == nil {
			message = text
		} else {
			message = string(answer.Message)
		}
	}
	return clip(strings.Join(strings.Fields(message), " "), 200)
}

func clip(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit-1]) + "…"
}

// PostStatus writes one commit status. The states arrive in GitHub's words
// (pending, success, failure, error); GitLab's are its pipeline job states.
func (c *ForgeClient) PostStatus(ctx context.Context, endpoint ForgeEndpoint, sha string, status CommitStatus) error {
	if !gitObjectIDRE.MatchString(sha) {
		return fmt.Errorf("commit %q is not a full object id", sha)
	}
	description := clip(status.Description, 140)
	switch endpoint.Provider {
	case ForgeGitLab:
		state := map[string]string{"pending": "running", "success": "success", "failure": "failed", "error": "canceled"}[status.State]
		if state == "" {
			return fmt.Errorf("unknown status state %q", status.State)
		}
		body := map[string]string{"state": state, "name": status.Context, "description": description}
		if status.TargetURL != "" {
			body["target_url"] = status.TargetURL
		}
		_, err := c.call(ctx, endpoint, http.MethodPost, "/api/v4/projects/"+url.PathEscape(endpoint.Repository)+"/statuses/"+sha, body, nil)
		return err
	case ForgeGitea:
		owner, name, ok := strings.Cut(endpoint.Repository, "/")
		if !ok || strings.Contains(name, "/") {
			return fmt.Errorf("repository %q is not owner/name", endpoint.Repository)
		}
		body := map[string]string{"state": status.State, "context": status.Context, "description": description}
		if status.TargetURL != "" {
			body["target_url"] = status.TargetURL
		}
		_, err := c.call(ctx, endpoint, http.MethodPost, "/api/v1/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name)+"/statuses/"+sha, body, nil)
		return err
	}
	return ErrForgeUnsupported
}

// owner is the account the token belongs to. A comment is edited only when
// that account wrote it, so a marker quoted by somebody else is never
// overwritten even when the token is an administrator's.
func (c *ForgeClient) owner(ctx context.Context, endpoint ForgeEndpoint) (int64, error) {
	digest := sha256.Sum256([]byte(endpoint.Provider + "\x00" + endpoint.Origin + "\x00" + endpoint.token))
	key := hex.EncodeToString(digest[:])
	c.mu.Lock()
	cached, ok := c.owners[key]
	c.mu.Unlock()
	if ok && c.now().Before(cached.until) {
		return cached.id, nil
	}
	var user struct {
		ID int64 `json:"id"`
	}
	path := "/api/v4/user"
	if endpoint.Provider == ForgeGitea {
		path = "/api/v1/user"
	}
	if _, err := c.call(ctx, endpoint, http.MethodGet, path, nil, &user); err != nil {
		return 0, err
	}
	if user.ID == 0 {
		return 0, errors.New("the host did not say whom the token belongs to")
	}
	c.mu.Lock()
	c.owners[key] = forgeOwner{id: user.ID, until: c.now().Add(time.Hour)}
	if len(c.owners) > 256 {
		c.owners = map[string]forgeOwner{key: c.owners[key]}
	}
	c.mu.Unlock()
	return user.ID, nil
}

// UpsertComment keeps one comment on a merge or pull request, found again by
// its marker and edited in place.
func (c *ForgeClient) UpsertComment(ctx context.Context, endpoint ForgeEndpoint, number int, marker, body string) error {
	if number <= 0 || strings.TrimSpace(marker) == "" {
		return errors.New("a preview comment needs a request number and a marker")
	}
	owner, err := c.owner(ctx, endpoint)
	if err != nil {
		return err
	}
	// The marker leads the body, invisible in rendered Markdown, the way the
	// GitHub App writes it.
	body = marker + "\n" + body
	switch endpoint.Provider {
	case ForgeGitLab:
		base := "/api/v4/projects/" + url.PathEscape(endpoint.Repository) + "/merge_requests/" + strconv.Itoa(number) + "/notes"
		existing := int64(0)
		// Twenty pages of a hundred notes is a merge request nobody reads to
		// the end; past that a second comment is the lesser harm.
		for page := 1; page <= 20 && existing == 0; page++ {
			var notes []struct {
				ID     int64  `json:"id"`
				Body   string `json:"body"`
				System bool   `json:"system"`
				Author struct {
					ID int64 `json:"id"`
				} `json:"author"`
			}
			header, err := c.call(ctx, endpoint, http.MethodGet, base+"?sort=asc&order_by=created_at&per_page=100&page="+strconv.Itoa(page), nil, &notes)
			if err != nil {
				return err
			}
			for _, note := range notes {
				if !note.System && note.Author.ID == owner && strings.Contains(note.Body, marker) {
					existing = note.ID
					break
				}
			}
			if header.Get("X-Next-Page") == "" {
				break
			}
		}
		if existing != 0 {
			_, err = c.call(ctx, endpoint, http.MethodPut, base+"/"+strconv.FormatInt(existing, 10), map[string]string{"body": body}, nil)
			return err
		}
		_, err = c.call(ctx, endpoint, http.MethodPost, base, map[string]string{"body": body}, nil)
		return err
	case ForgeGitea:
		ownerName, name, ok := strings.Cut(endpoint.Repository, "/")
		if !ok || strings.Contains(name, "/") {
			return fmt.Errorf("repository %q is not owner/name", endpoint.Repository)
		}
		repository := "/api/v1/repos/" + url.PathEscape(ownerName) + "/" + url.PathEscape(name)
		// Gitea answers an issue's comments in one list, without pages.
		var comments []struct {
			ID   int64  `json:"id"`
			Body string `json:"body"`
			User struct {
				ID int64 `json:"id"`
			} `json:"user"`
		}
		if _, err := c.call(ctx, endpoint, http.MethodGet, repository+"/issues/"+strconv.Itoa(number)+"/comments", nil, &comments); err != nil {
			return err
		}
		for _, comment := range comments {
			if comment.User.ID == owner && strings.Contains(comment.Body, marker) {
				_, err := c.call(ctx, endpoint, http.MethodPatch, repository+"/issues/comments/"+strconv.FormatInt(comment.ID, 10), map[string]string{"body": body}, nil)
				return err
			}
		}
		_, err := c.call(ctx, endpoint, http.MethodPost, repository+"/issues/"+strconv.Itoa(number)+"/comments", map[string]string{"body": body}, nil)
		return err
	}
	return ErrForgeUnsupported
}

// markUsed stamps the token's credential as used, so the Credentials page's
// "last used" covers writing statuses as well as cloning.
func (c *ForgeClient) markUsed(ctx context.Context, endpoint ForgeEndpoint) {
	if marker, ok := c.tokens.(interface {
		recordCredentialUsed(context.Context, int64)
	}); ok && endpoint.tokenID != 0 {
		marker.recordCredentialUsed(ctx, endpoint.tokenID)
	}
}

// ForgeToken is the credential lookup the ForgeClient writes with: the
// source's own token when it is an HTTPS token for this host, otherwise the
// newest provider token saved for the host. An SSH key or a registry login
// can never call an API, so a source cloned with one still reports when a
// provider token for its host exists.
func (s *PlanningStore) ForgeToken(ctx context.Context, sourceCredentialID int64, host string) (ForgeToken, bool, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return ForgeToken{}, false, nil
	}
	matches := func(target string) bool {
		target = strings.ToLower(strings.TrimSpace(target))
		if target == "" {
			return false
		}
		if target == host {
			return true
		}
		hostname, _, err := net.SplitHostPort(host)
		return err == nil && target == hostname
	}
	open := func(id int64, name, kind, config, sealed string, sourceOwn bool) (ForgeToken, bool, error) {
		if kind != CredentialProviderToken && kind != CredentialGitBearer {
			return ForgeToken{}, false, nil
		}
		var parsed credentialConfig
		_ = json.Unmarshal([]byte(config), &parsed)
		// The source's own token may name no host (it is scoped by the
		// source it is attached to); a token found by host must name it.
		if (!sourceOwn || parsed.Target != "") && !matches(parsed.Target) {
			return ForgeToken{}, false, nil
		}
		secret, err := s.sealer.Open(sealed)
		if err != nil {
			return ForgeToken{}, false, fmt.Errorf("the token %q cannot be decrypted", name)
		}
		if !validGitBearerToken(secret) {
			return ForgeToken{}, false, nil
		}
		return ForgeToken{ID: id, Name: name, Secret: secret}, true, nil
	}
	if sourceCredentialID != 0 {
		var name, kind, config, sealed string
		err := s.db.QueryRowContext(ctx, `SELECT name, kind, config_json, secret_enc FROM deploy_credentials WHERE id = ?`, sourceCredentialID).
			Scan(&name, &kind, &config, &sealed)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return ForgeToken{}, false, err
		}
		if err == nil {
			if token, ok, err := open(sourceCredentialID, name, kind, config, sealed, true); ok || err != nil {
				return token, ok, err
			}
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, kind, config_json, secret_enc FROM deploy_credentials
		 WHERE kind = ? ORDER BY updated_at DESC, id DESC`, CredentialProviderToken)
	if err != nil {
		return ForgeToken{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name, kind, config, sealed string
		if err := rows.Scan(&id, &name, &kind, &config, &sealed); err != nil {
			return ForgeToken{}, false, err
		}
		if token, ok, err := open(id, name, kind, config, sealed, false); ok || err != nil {
			return token, ok, err
		}
	}
	return ForgeToken{}, false, rows.Err()
}

// recordForgeReport keeps the newest outcome of one kind of write-back.
func (s *OrchestrationStore) recordForgeReport(ctx context.Context, environmentID int64, kind string, report ForgeReport) {
	if environmentID == 0 {
		return
	}
	ok := 0
	if report.OK {
		ok = 1
	}
	_, _ = s.db.ExecContext(ctx, `
		INSERT INTO deploy_forge_reports(environment_id,kind,provider,repository,revision,state,run_id,ok,message,reported_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(environment_id,kind) DO UPDATE SET provider=excluded.provider,repository=excluded.repository,
		  revision=excluded.revision,state=excluded.state,run_id=excluded.run_id,ok=excluded.ok,
		  message=excluded.message,reported_at=excluded.reported_at`,
		environmentID, kind, report.Provider, report.Repository, report.Revision, report.State, report.RunID, ok,
		clip(report.Message, 400), report.At.UTC().Unix())
}

func (s *OrchestrationStore) forgeReports(ctx context.Context, environmentID int64) (map[string]*ForgeReport, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind,provider,repository,revision,state,run_id,ok,message,reported_at
		FROM deploy_forge_reports WHERE environment_id = ?`, environmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*ForgeReport{}
	for rows.Next() {
		var kind string
		var report ForgeReport
		var ok int
		var at int64
		if err := rows.Scan(&kind, &report.Provider, &report.Repository, &report.Revision, &report.State, &report.RunID, &ok, &report.Message, &at); err != nil {
			return nil, err
		}
		report.OK, report.At = ok == 1, time.Unix(at, 0).UTC()
		out[kind] = &report
	}
	return out, rows.Err()
}

// environmentSource is the source an environment deploys at a revision, or
// at its desired revision when revision is zero, with the credential it
// clones with.
func (s *OrchestrationStore) environmentSource(ctx context.Context, environmentID int64, revision int) (SourceIdentity, int64, string, error) {
	var identity, slug string
	var credentialID sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT e.slug, COALESCE(src.identity_json, '{}'), src.credential_id
		  FROM deploy_environments e
		  LEFT JOIN deploy_sources src ON src.environment_id = e.id
		   AND src.revision = CASE WHEN ? > 0 THEN ? ELSE e.desired_revision END
		 WHERE e.id = ?`, revision, revision, environmentID).Scan(&slug, &identity, &credentialID)
	if err != nil {
		return SourceIdentity{}, 0, "", err
	}
	var source SourceIdentity
	if json.Unmarshal([]byte(identity), &source) != nil {
		return SourceIdentity{}, 0, slug, nil
	}
	id := credentialID.Int64
	if id == 0 {
		id = source.CredentialID
	}
	return source, id, slug, nil
}
