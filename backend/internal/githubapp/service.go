package githubapp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// CredentialSync is the deploy side of an installation: a credential row that
// mints the installation's tokens for clones. The service keeps one per
// installation so a repository picked from the App's list can be cloned
// without the operator pasting anything.
type CredentialSync interface {
	EnsureGitHubAppCredential(ctx context.Context, installationID int64, account string) (int64, error)
	RemoveGitHubAppCredentials(ctx context.Context) error
}

// Service is the App as the rest of the dashboard sees it: whether one is
// connected, who installed it, and the three things it does on their behalf.
type Service struct {
	store       *Store
	credentials CredentialSync
	http        *http.Client
	APIURL      string
	WebURL      string
	now         func() time.Time

	mu sync.Mutex
	// client is rebuilt whenever the stored App changes.
	client   *Client
	clientID int64
	// pending manifest states, each good for fifteen minutes.
	states map[string]pendingManifest
	// installations are read from GitHub once a minute at most; the settings
	// page polls, and GitHub's rate limit is not the page's to spend.
	installations   []Installation
	installationsAt time.Time
	installationsOf int64
}

type pendingManifest struct {
	expires time.Time
}

// Status is what the settings page shows.
type Status struct {
	Configured    bool           `json:"configured"`
	App           *AppSummary    `json:"app,omitempty"`
	Installations []Installation `json:"installations"`
	// InstallURL is where an operator adds the App to another account.
	InstallURL string `json:"installUrl,omitempty"`
	WebhookURL string `json:"webhookUrl,omitempty"`
	// Error is a GitHub-side problem worth showing next to a configured App:
	// a deleted App, a revoked key, a network that is down.
	Error string `json:"error,omitempty"`
}

type AppSummary struct {
	ID        int64     `json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	Owner     string    `json:"owner"`
	HTMLURL   string    `json:"htmlUrl"`
	CreatedAt time.Time `json:"createdAt"`
}

// ManifestRequest is what the page asks with: an optional App name and an
// optional organization to own it.
type ManifestRequest struct {
	Name         string `json:"name,omitempty"`
	Organization string `json:"organization,omitempty"`
}

var (
	appNameRE      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,33}$`)
	organizationRE = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
)

func New(store *Store, credentials CredentialSync) *Service {
	return &Service{
		store: store, credentials: credentials, http: &http.Client{Timeout: 30 * time.Second},
		APIURL: DefaultAPIURL, WebURL: DefaultWebURL, now: time.Now, states: map[string]pendingManifest{},
	}
}

// Client returns the connected App's client, building it on first use and
// again after the App changes.
func (s *Service) Client(ctx context.Context) (*Client, error) {
	credentials, err := s.store.Load(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil && s.clientID == credentials.ID {
		return s.client, nil
	}
	client, err := NewClient(credentials)
	if err != nil {
		return nil, err
	}
	client.APIURL, client.HTTP, client.now = s.APIURL, s.http, s.now
	s.client, s.clientID = client, credentials.ID
	return client, nil
}

// WebhookSecret is what the App's deliveries are signed with.
func (s *Service) WebhookSecret(ctx context.Context) (string, error) {
	credentials, err := s.store.Load(ctx)
	if err != nil {
		return "", err
	}
	return credentials.WebhookSecret, nil
}

// BeginManifest issues the state a GitHub redirect must carry back and the
// manifest the browser posts. The dashboard address is the caller's: the
// server knows it from its own configuration, and a request cannot choose it.
func (s *Service) BeginManifest(dashboardURL string, request ManifestRequest) (ManifestStart, error) {
	if !strings.HasPrefix(dashboardURL, "https://") {
		return ManifestStart{}, errors.New("the GitHub App needs this dashboard's public HTTPS address; set it under Settings before connecting")
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		suffix, err := randomToken(2)
		if err != nil {
			return ManifestStart{}, err
		}
		name = "Just Dashboard " + suffix
	}
	if !appNameRE.MatchString(name) {
		return ManifestStart{}, errors.New("the App name may use letters, digits, spaces, dots, dashes and underscores, up to 34 characters")
	}
	if request.Organization != "" && !organizationRE.MatchString(strings.TrimSpace(request.Organization)) {
		return ManifestStart{}, errors.New("the organization must be a GitHub login")
	}
	state, err := randomToken(16)
	if err != nil {
		return ManifestStart{}, err
	}
	s.mu.Lock()
	for token, pending := range s.states {
		if s.now().After(pending.expires) {
			delete(s.states, token)
		}
	}
	s.states[state] = pendingManifest{expires: s.now().Add(15 * time.Minute)}
	s.mu.Unlock()
	return ManifestStart{Action: manifestAction(s.WebURL, request.Organization, state), State: state, Manifest: NewManifest(name, dashboardURL)}, nil
}

// CompleteManifest is GitHub's redirect: a code, exchanged once, stored
// sealed. The state must be one BeginManifest issued.
func (s *Service) CompleteManifest(ctx context.Context, code, state string) (Credentials, error) {
	s.mu.Lock()
	pending, ok := s.states[state]
	delete(s.states, state)
	s.mu.Unlock()
	if !ok || s.now().After(pending.expires) {
		return Credentials{}, ErrBadState
	}
	credentials, err := ExchangeManifestCode(ctx, s.http, s.APIURL, code)
	if err != nil {
		return Credentials{}, err
	}
	credentials.CreatedAt = s.now().UTC()
	if err := s.store.Save(ctx, credentials); err != nil {
		return Credentials{}, err
	}
	s.mu.Lock()
	s.client, s.clientID, s.installations, s.installationsAt = nil, 0, nil, time.Time{}
	s.mu.Unlock()
	return credentials, nil
}

// Disconnect forgets the App. The App itself stays on GitHub until its owner
// deletes it there; the dashboard only stops being able to act as it.
func (s *Service) Disconnect(ctx context.Context) error {
	if err := s.store.Delete(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	s.client, s.clientID, s.installations, s.installationsAt = nil, 0, nil, time.Time{}
	s.mu.Unlock()
	if s.credentials != nil {
		return s.credentials.RemoveGitHubAppCredentials(ctx)
	}
	return nil
}

// Invalidate drops the installation cache; the App's own installation
// events call it so a fresh install shows up without waiting a minute.
func (s *Service) Invalidate() {
	s.mu.Lock()
	s.installations, s.installationsAt = nil, time.Time{}
	s.mu.Unlock()
}

// Status answers the settings page. A configured App whose GitHub side fails
// is still reported as configured, with the failure beside it.
func (s *Service) Status(ctx context.Context, dashboardURL string) (Status, error) {
	credentials, err := s.store.Load(ctx)
	if errors.Is(err, ErrNotConfigured) {
		return Status{Installations: []Installation{}}, nil
	}
	if err != nil {
		return Status{}, err
	}
	status := Status{
		Configured: true, Installations: []Installation{},
		App:        &AppSummary{ID: credentials.ID, Slug: credentials.Slug, Name: credentials.Name, Owner: credentials.Owner, HTMLURL: credentials.HTMLURL, CreatedAt: credentials.CreatedAt},
		InstallURL: strings.TrimRight(s.WebURL, "/") + "/apps/" + credentials.Slug + "/installations/new",
	}
	if base := strings.TrimRight(dashboardURL, "/"); base != "" {
		status.WebhookURL = base + "/api/v1/hooks/github-app"
	}
	installations, err := s.Installations(ctx)
	if err != nil {
		status.Error = err.Error()
		return status, nil
	}
	status.Installations = installations
	return status, nil
}

// Installations lists who installed the App, cached for a minute, with each
// installation's deploy credential ensured.
func (s *Service) Installations(ctx context.Context) ([]Installation, error) {
	client, err := s.Client(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.installations != nil && s.installationsOf == s.clientID && s.now().Sub(s.installationsAt) < time.Minute {
		cached := append([]Installation(nil), s.installations...)
		s.mu.Unlock()
		return cached, nil
	}
	s.mu.Unlock()
	installations, err := client.Installations(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(installations, func(i, j int) bool { return installations[i].Account < installations[j].Account })
	if s.credentials != nil {
		for index := range installations {
			id, err := s.credentials.EnsureGitHubAppCredential(ctx, installations[index].ID, installations[index].Account)
			if err != nil {
				return nil, err
			}
			installations[index].CredentialID = id
		}
	}
	s.mu.Lock()
	s.installations, s.installationsAt, s.installationsOf = append([]Installation(nil), installations...), s.now(), s.clientID
	s.mu.Unlock()
	return installations, nil
}

// Repositories lists every repository every installation grants, for the
// import page.
func (s *Service) Repositories(ctx context.Context) ([]Repository, error) {
	client, err := s.Client(ctx)
	if err != nil {
		return nil, err
	}
	installations, err := s.Installations(ctx)
	if err != nil {
		return nil, err
	}
	repositories := []Repository{}
	for _, installation := range installations {
		granted, err := client.InstallationRepositories(ctx, installation)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", installation.Account, err)
		}
		repositories = append(repositories, granted...)
	}
	sort.Slice(repositories, func(i, j int) bool { return repositories[i].NameWithOwner < repositories[j].NameWithOwner })
	return repositories, nil
}

// InstallationToken mints a token for one installation; the deploy
// credential store calls it when a clone needs one.
func (s *Service) InstallationToken(ctx context.Context, installationID int64) (string, error) {
	client, err := s.Client(ctx)
	if err != nil {
		return "", err
	}
	return client.InstallationToken(ctx, installationID)
}

// PostCommitStatus reports as the App on whichever installation grants the
// repository.
func (s *Service) PostCommitStatus(ctx context.Context, nameWithOwner, sha string, status CommitStatus) error {
	client, err := s.Client(ctx)
	if err != nil {
		return err
	}
	installation, err := client.RepositoryInstallation(ctx, nameWithOwner)
	if err != nil {
		return err
	}
	return client.PostCommitStatus(ctx, installation, nameWithOwner, sha, status)
}

// UpsertPullRequestComment keeps the bot's one comment on a pull request
// current.
func (s *Service) UpsertPullRequestComment(ctx context.Context, nameWithOwner string, number int, marker, body string) error {
	client, err := s.Client(ctx)
	if err != nil {
		return err
	}
	installation, err := client.RepositoryInstallation(ctx, nameWithOwner)
	if err != nil {
		return err
	}
	return client.UpsertPullRequestComment(ctx, installation, nameWithOwner, number, marker, body)
}

// Installed says whether some installation grants the repository, so a
// caller can pick the App over a personal login before reading anything.
func (s *Service) Installed(ctx context.Context, nameWithOwner string) bool {
	client, err := s.Client(ctx)
	if err != nil {
		return false
	}
	_, err = client.RepositoryInstallation(ctx, nameWithOwner)
	return err == nil
}

// PullRequests lists a repository's pull requests through the installation
// that grants it.
func (s *Service) PullRequests(ctx context.Context, nameWithOwner, state string) ([]PullRequest, error) {
	client, err := s.Client(ctx)
	if err != nil {
		return nil, err
	}
	installation, err := client.RepositoryInstallation(ctx, nameWithOwner)
	if err != nil {
		return nil, err
	}
	return client.ListPullRequests(ctx, installation, nameWithOwner, state)
}

// PullRequest reads one pull request through the installation that grants
// its repository.
func (s *Service) PullRequest(ctx context.Context, nameWithOwner string, number int) (*PullRequest, error) {
	client, err := s.Client(ctx)
	if err != nil {
		return nil, err
	}
	installation, err := client.RepositoryInstallation(ctx, nameWithOwner)
	if err != nil {
		return nil, err
	}
	return client.GetPullRequest(ctx, installation, nameWithOwner, number)
}

// PullRequestChecks lists everything reported on a commit: check runs and
// commit statuses in one list, failures first. An App the owner has not yet
// granted checks:read still gets the statuses, with ErrPermission beside
// them so the page can say what is missing rather than show an empty list.
func (s *Service) PullRequestChecks(ctx context.Context, nameWithOwner, sha string) ([]CheckRun, error) {
	client, err := s.Client(ctx)
	if err != nil {
		return nil, err
	}
	installation, err := client.RepositoryInstallation(ctx, nameWithOwner)
	if err != nil {
		return nil, err
	}
	runs, runsErr := client.ListCheckRuns(ctx, installation, nameWithOwner, sha)
	if runsErr != nil && !errors.Is(runsErr, ErrPermission) {
		return nil, runsErr
	}
	statuses, err := client.CombinedStatus(ctx, installation, nameWithOwner, sha)
	if err != nil {
		return nil, err
	}
	checks := append(append([]CheckRun{}, runs...), statuses...)
	sortCheckRuns(checks)
	return checks, runsErr
}

func randomToken(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
