package githubrepos

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/githubapp"
)

const (
	// CallbackPath is the page GitHub sends the browser back to, for both
	// the manifest flow and a sign-in. It is the page and not the API: the
	// redirect is a cross-site navigation, on which the SameSite=Strict
	// session cookie is not sent, so the page reads code and state and
	// finishes the exchange with its own session.
	CallbackPath = "/github/callback"
	// stateTTL is how long a started sign-in or App creation may take.
	stateTTL = 10 * time.Minute
	// maxCallbackURLs is GitHub's own limit on an App's callback URLs.
	maxCallbackURLs = 10
)

var (
	// ErrState is a callback whose state this dashboard did not issue to the
	// signed-in account, issued more than ten minutes ago, or already used.
	ErrState = errors.New("this GitHub sign-in link has expired, was already used, or was started by another dashboard account; start again from the GitHub page")
	// ErrAppExists refuses a second App: the connections made through the
	// first would mean nothing to it.
	ErrAppExists = errors.New("this dashboard already has a GitHub App; forget it first to create another")
	// appNameRE is what GitHub accepts as an App name, held to the 34
	// characters githubapp allows.
	appNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,33}$`)
)

// DefaultPermissions is what the App asks every installation for. Each one
// is used by an operation the page offers; nothing is asked for
// speculatively. No workflows: editing workflow files is not offered.
func DefaultPermissions() map[string]string {
	return map[string]string{
		"administration": "write", "contents": "write", "issues": "write", "pull_requests": "write",
		"actions": "write", "checks": "read", "statuses": "read", "metadata": "read",
	}
}

// CallbackUnregisteredError is a sign-in started from an address the App
// was not told about: GitHub would refuse to send the browser back to it.
type CallbackUnregisteredError struct {
	RedirectURI string
	Registered  []string
	SettingsURL string
}

func (e *CallbackUnregisteredError) Error() string {
	registered := "none"
	if len(e.Registered) > 0 {
		registered = strings.Join(e.Registered, ", ")
	}
	return fmt.Sprintf("GitHub only sends a sign-in back to the App's registered callback URLs (%s), and %s is not one. Add it under Callback URL in the App's settings on GitHub (%s), record it here, or sign in with a code instead.",
		registered, e.RedirectURI, e.SettingsURL)
}

// ParseOrigin reads a browser origin: http or https, a host and an optional
// port, nothing else. It is what every address the App is told about is
// built from.
func ParseOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	invalid := fmt.Errorf("%q is not this dashboard's address (an http or https origin with no path)", raw)
	if raw == "" || len(raw) > 255 {
		return "", invalid
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" ||
		strings.ContainsAny(parsed.Host, " \t\r\n\\") {
		return "", invalid
	}
	return parsed.Scheme + "://" + strings.ToLower(parsed.Host), nil
}

// Manifest is what the browser posts to GitHub to create the App.
type Manifest struct {
	Name                  string            `json:"name"`
	URL                   string            `json:"url"`
	HookAttributes        map[string]any    `json:"hook_attributes"`
	RedirectURL           string            `json:"redirect_url"`
	CallbackURLs          []string          `json:"callback_urls"`
	SetupURL              string            `json:"setup_url"`
	SetupOnUpdate         bool              `json:"setup_on_update"`
	RequestOAuthOnInstall bool              `json:"request_oauth_on_install"`
	Public                bool              `json:"public"`
	DefaultPermissions    map[string]string `json:"default_permissions"`
	DefaultEvents         []string          `json:"default_events"`
}

// NewManifest describes the repository App for a dashboard reached at
// origin. The webhook is inactive, so GitHub never needs to reach this
// server: every address in it is a browser navigation, which a private
// address or a name only the operator's network resolves serves as well as
// a public one. request_oauth_on_install is off because GitHub's
// post-install authorization carries no state; the person signs in from the
// page instead.
func NewManifest(name, origin string, public bool) Manifest {
	return Manifest{
		Name: name, URL: origin,
		HookAttributes:        map[string]any{"url": origin + "/api/v1/github/hook", "active": false},
		RedirectURL:           origin + CallbackPath,
		CallbackURLs:          []string{origin + CallbackPath},
		SetupURL:              origin + "/github?installed=1",
		SetupOnUpdate:         true,
		RequestOAuthOnInstall: false,
		Public:                public,
		DefaultPermissions:    DefaultPermissions(),
		DefaultEvents:         []string{},
	}
}

// ManifestRequest is what the page starts the App's creation with.
type ManifestRequest struct {
	Origin       string `json:"origin"`
	Organization string `json:"organization,omitempty"`
	Name         string `json:"name,omitempty"`
	// Public is whether accounts other than the owner may install the App;
	// absent is yes.
	Public *bool `json:"public,omitempty"`
}

// ManifestStart is what the page posts to GitHub (GitHubManifestStart).
type ManifestStart struct {
	Action   string   `json:"action"`
	State    string   `json:"state"`
	Manifest Manifest `json:"manifest"`
}

// Kinds of state.
const (
	KindApp        = "app"
	KindConnection = "connection"
)

type pendingState struct {
	userID      int64
	kind        string
	redirectURI string
	verifier    string
	public      bool
	expires     time.Time
}

// PendingState is a state taken back from a callback: which flow it
// finishes.
type PendingState struct {
	Kind    string
	pending pendingState
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Service) issueState(pending pendingState) (string, error) {
	state, err := randomHex(32)
	if err != nil {
		return "", err
	}
	pending.expires = s.now().Add(stateTTL)
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, p := range s.states {
		if s.now().After(p.expires) {
			delete(s.states, token)
		}
	}
	s.states[state] = pending
	return state, nil
}

// TakeState spends a state for the account finishing a callback. A state
// issued to another account is refused without being spent, so nobody but
// the person who started a flow can end it, even by trying its state first.
func (s *Service) TakeState(userID int64, state string) (*PendingState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, ok := s.states[state]
	if !ok {
		return nil, ErrState
	}
	if s.now().After(pending.expires) {
		delete(s.states, state)
		return nil, ErrState
	}
	if pending.userID != userID {
		return nil, ErrState
	}
	delete(s.states, state)
	return &PendingState{Kind: pending.kind, pending: pending}, nil
}

// SetupApp is the App as the page shows it (GitHubSetup.app).
type SetupApp struct {
	ID           int64             `json:"id"`
	Slug         string            `json:"slug"`
	Name         string            `json:"name"`
	Owner        string            `json:"owner"`
	HTMLURL      string            `json:"htmlUrl"`
	SettingsURL  string            `json:"settingsUrl"`
	InstallURL   string            `json:"installUrl"`
	Public       bool              `json:"public"`
	CallbackURLs []string          `json:"callbackUrls"`
	Permissions  map[string]string `json:"permissions"`
	CreatedAt    time.Time         `json:"createdAt,omitzero"`
}

// Setup is GitHubSetup.
type Setup struct {
	Configured bool      `json:"configured"`
	App        *SetupApp `json:"app,omitempty"`
}

// AppSettingsURL is where the App's own settings (callback URLs, device
// flow, permissions) are changed on GitHub.
func (s *Service) AppSettingsURL(app App) string {
	if app.OwnerType == "Organization" {
		return s.webURL() + "/organizations/" + url.PathEscape(app.Owner) + "/settings/apps/" + url.PathEscape(app.Slug)
	}
	return s.webURL() + "/settings/apps/" + url.PathEscape(app.Slug)
}

// InstallURL is where the App is installed on an account.
func (s *Service) InstallURL(app App) string {
	return s.webURL() + "/apps/" + url.PathEscape(app.Slug) + "/installations/new"
}

// Setup answers whether an App exists and, if so, what the page shows of it.
func (s *Service) Setup(ctx context.Context) (Setup, error) {
	app, err := s.store.LoadApp(ctx)
	if errors.Is(err, ErrNotConfigured) {
		return Setup{Configured: false}, nil
	}
	if err != nil {
		return Setup{}, err
	}
	return Setup{Configured: true, App: &SetupApp{
		ID: app.ID, Slug: app.Slug, Name: app.Name, Owner: app.Owner, HTMLURL: app.HTMLURL,
		SettingsURL: s.AppSettingsURL(app), InstallURL: s.InstallURL(app), Public: app.Public,
		CallbackURLs: app.CallbackURLs, Permissions: app.Permissions, CreatedAt: app.CreatedAt,
	}}, nil
}

// BeginManifest issues the state GitHub's redirect must carry back and the
// manifest the browser posts. origin is the browser's own, which the API
// layer has checked against the request's Origin header.
func (s *Service) BeginManifest(ctx context.Context, userID int64, request ManifestRequest) (ManifestStart, error) {
	origin, err := ParseOrigin(request.Origin)
	if err != nil {
		return ManifestStart{}, refuse(err.Error())
	}
	if _, err := s.store.LoadApp(ctx); err == nil {
		return ManifestStart{}, ErrAppExists
	} else if !errors.Is(err, ErrNotConfigured) {
		return ManifestStart{}, err
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		suffix, err := randomHex(2)
		if err != nil {
			return ManifestStart{}, err
		}
		name = "Just Dashboard Repos " + suffix
	}
	if !appNameRE.MatchString(name) {
		return ManifestStart{}, refuse("the App name may use letters, digits, spaces, dots, dashes and underscores, up to 34 characters")
	}
	organization := strings.TrimSpace(request.Organization)
	if organization != "" && !ValidOwner(organization) {
		return ManifestStart{}, refuse("the organization must be a GitHub login")
	}
	public := request.Public == nil || *request.Public
	redirect := origin + CallbackPath
	state, err := s.issueState(pendingState{userID: userID, kind: KindApp, redirectURI: redirect, public: public})
	if err != nil {
		return ManifestStart{}, err
	}
	action := s.webURL() + "/settings/apps/new?state=" + url.QueryEscape(state)
	if organization != "" {
		action = s.webURL() + "/organizations/" + url.PathEscape(organization) + "/settings/apps/new?state=" + url.QueryEscape(state)
	}
	return ManifestStart{Action: action, State: state, Manifest: NewManifest(name, origin, public)}, nil
}

// validCode is a one-time code GitHub redirected back with, checked before
// it goes into a path or a form.
func validCode(code string) bool {
	return code != "" && len(code) <= 256 && !strings.ContainsAny(code, "/?#&%= \t\r\n")
}

// CompleteApp exchanges the manifest flow's code for the App's credentials
// and stores them sealed. The webhook secret GitHub may or may not send for
// an inactive webhook is not kept: nothing here receives a delivery.
func (s *Service) CompleteApp(ctx context.Context, pending *PendingState, code string) (App, error) {
	if pending == nil || pending.Kind != KindApp {
		return App{}, ErrState
	}
	code = strings.TrimSpace(code)
	if !validCode(code) {
		return App{}, refuse("the code GitHub sent back is malformed")
	}
	var converted struct {
		ID           int64             `json:"id"`
		Slug         string            `json:"slug"`
		Name         string            `json:"name"`
		ClientID     string            `json:"client_id"`
		ClientSecret string            `json:"client_secret"`
		PEM          string            `json:"pem"`
		HTMLURL      string            `json:"html_url"`
		Permissions  map[string]string `json:"permissions"`
		Owner        Owner             `json:"owner"`
	}
	client := NewClient(s.APIURL, s.HTTP, nil)
	if _, err := client.Do(ctx, http.MethodPost, "/app-manifests/"+url.PathEscape(code)+"/conversions", nil, &converted); err != nil {
		return App{}, err
	}
	if converted.ID == 0 || converted.Slug == "" || converted.ClientID == "" || converted.ClientSecret == "" || converted.PEM == "" {
		return App{}, &APIError{Status: http.StatusBadGateway, Kind: KindUnavailable, Message: "GitHub's answer for the new App was incomplete", Path: "/app-manifests/{code}/conversions"}
	}
	if _, err := githubapp.NewClient(githubapp.Credentials{ID: converted.ID, ClientID: converted.ClientID, PrivateKey: converted.PEM}); err != nil {
		return App{}, &APIError{Status: http.StatusBadGateway, Kind: KindUnavailable, Message: "GitHub's private key for the new App could not be read", Path: "/app-manifests/{code}/conversions"}
	}
	permissions := converted.Permissions
	if len(permissions) == 0 {
		permissions = DefaultPermissions()
	}
	ownerType := converted.Owner.Type
	if ownerType == "" {
		ownerType = "User"
	}
	app := App{
		ID: converted.ID, Slug: converted.Slug, Name: converted.Name, Owner: converted.Owner.Login, OwnerType: ownerType,
		HTMLURL: converted.HTMLURL, ClientID: converted.ClientID, Public: pending.pending.public,
		CallbackURLs: []string{pending.pending.redirectURI}, Permissions: permissions, CreatedAt: s.now().UTC(),
		clientSecret: converted.ClientSecret, privateKey: converted.PEM,
	}
	// Two administrators racing the manifest flow both create an App on
	// GitHub; the later one wins here, and whatever was connected through
	// the earlier one is revoked and forgotten with it.
	if _, err := s.store.LoadApp(ctx); err == nil {
		if err := s.ForgetApp(ctx); err != nil {
			return App{}, err
		}
	}
	if err := s.store.SaveApp(ctx, app); err != nil {
		return App{}, err
	}
	s.forgetAll()
	return app, nil
}

// AddCallbackOrigin records origin's callback address as one the admin
// added to the App on GitHub, so a sign-in from it may start. GitHub keeps
// the real list; this is the dashboard's copy, used to refuse a sign-in
// GitHub would refuse with a page the person cannot get back from.
func (s *Service) AddCallbackOrigin(ctx context.Context, origin string) (Setup, error) {
	parsed, err := ParseOrigin(origin)
	if err != nil {
		return Setup{}, refuse(err.Error())
	}
	app, err := s.store.LoadApp(ctx)
	if err != nil {
		return Setup{}, err
	}
	callback := parsed + CallbackPath
	if !slices.Contains(app.CallbackURLs, callback) {
		if len(app.CallbackURLs) >= maxCallbackURLs {
			return Setup{}, refuse(fmt.Sprintf("GitHub allows at most %d callback URLs on an App, and this one has them all", maxCallbackURLs))
		}
		if err := s.store.SetCallbackURLs(ctx, append(app.CallbackURLs, callback)); err != nil {
			return Setup{}, err
		}
	}
	return s.Setup(ctx)
}

// ForgetApp revokes every connection on GitHub, best effort, and forgets the
// App and the connections. The App itself stays on GitHub until its owner
// deletes it there.
func (s *Service) ForgetApp(ctx context.Context) error {
	app, err := s.store.LoadApp(ctx)
	if err != nil {
		return err
	}
	connections, err := s.store.allConnections(ctx)
	if err != nil {
		return err
	}
	revoked := map[int64]bool{}
	for _, c := range connections {
		// One grant per GitHub account: revoking it once ends every token
		// issued to that account through this App.
		if revoked[c.GitHubID] {
			continue
		}
		revoked[c.GitHubID] = true
		s.revokeGrant(ctx, app, c.tokens.Access)
	}
	if err := s.store.DeleteApp(ctx); err != nil {
		return err
	}
	s.forgetAll()
	return nil
}

// refuse is a refusal of the request's own values, decided before GitHub is
// asked; the API answers it as GitHub's own validation refusals are.
func refuse(reason string) *APIError {
	return &APIError{Status: http.StatusUnprocessableEntity, Kind: KindValidation, Message: reason}
}
