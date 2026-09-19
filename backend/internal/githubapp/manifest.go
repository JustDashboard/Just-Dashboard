package githubapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Manifest is what the browser posts to GitHub to create the App. GitHub's
// manifest flow needs a form submission from the operator's own browser, so
// the dashboard hands the manifest to the page and the page posts it.
type Manifest struct {
	Name               string            `json:"name"`
	URL                string            `json:"url"`
	HookAttributes     map[string]any    `json:"hook_attributes"`
	RedirectURL        string            `json:"redirect_url"`
	SetupURL           string            `json:"setup_url"`
	SetupOnUpdate      bool              `json:"setup_on_update"`
	Public             bool              `json:"public"`
	DefaultPermissions map[string]string `json:"default_permissions"`
	DefaultEvents      []string          `json:"default_events"`
}

// ManifestStart is everything the page needs to send the operator to GitHub.
type ManifestStart struct {
	// Action is the GitHub page the form posts to; State is the token that
	// ties GitHub's redirect back to this dashboard.
	Action   string   `json:"action"`
	State    string   `json:"state"`
	Manifest Manifest `json:"manifest"`
}

// NewManifest describes the App this dashboard needs: read access to code and
// metadata for clones, write access to statuses and pull requests for the
// bot's reports, and the two events the deployment pipeline reacts to.
//
// The webhook and redirect targets are built from the dashboard's public
// address, which is why the setup page refuses to start on a dashboard that
// does not know its own address: GitHub would create an App that can never
// deliver.
func NewManifest(name, dashboardURL string) Manifest {
	base := strings.TrimRight(dashboardURL, "/")
	return Manifest{
		Name: name, URL: base,
		HookAttributes: map[string]any{"url": base + "/api/v1/hooks/github-app", "active": true},
		// The page, not the API: GitHub sends the browser back with a
		// cross-site navigation, on which the SameSite=Strict session cookie
		// is not sent, so an API callback answered 401 to every real
		// browser. The page reads code and state and finishes the exchange
		// with its own session.
		RedirectURL:    base + "/deploy/credentials",
		SetupURL:       base + "/deploy/credentials",
		SetupOnUpdate:  true,
		Public:         false,
		DefaultPermissions: map[string]string{
			"contents": "read", "metadata": "read", "pull_requests": "write", "statuses": "write",
		},
		DefaultEvents: []string{"push", "pull_request"},
	}
}

// manifestAction is the GitHub page that accepts the manifest: a personal
// account's settings, or an organization's when one was named.
func manifestAction(webURL, organization, state string) string {
	base := strings.TrimRight(webURL, "/")
	query := "?state=" + url.QueryEscape(state)
	if organization = strings.TrimSpace(organization); organization != "" {
		return base + "/organizations/" + url.PathEscape(organization) + "/settings/apps/new" + query
	}
	return base + "/settings/apps/new" + query
}

// ExchangeManifestCode turns the one-time code GitHub redirects back with
// into the App's credentials. The code is valid for an hour and works once.
func ExchangeManifestCode(ctx context.Context, client *http.Client, apiURL, code string) (Credentials, error) {
	code = strings.TrimSpace(code)
	if code == "" || strings.ContainsAny(code, "/?#& \t\r\n") {
		return Credentials{}, fmt.Errorf("the GitHub App setup code is malformed")
	}
	raw, _, err := request(ctx, client, apiURL, http.MethodPost, "/app-manifests/"+url.PathEscape(code)+"/conversions", "", nil)
	if err != nil {
		return Credentials{}, err
	}
	var converted struct {
		ID            int64  `json:"id"`
		Slug          string `json:"slug"`
		Name          string `json:"name"`
		ClientID      string `json:"client_id"`
		ClientSecret  string `json:"client_secret"`
		WebhookSecret string `json:"webhook_secret"`
		PEM           string `json:"pem"`
		HTMLURL       string `json:"html_url"`
		Owner         struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	if err := json.Unmarshal(raw, &converted); err != nil {
		return Credentials{}, fmt.Errorf("GitHub's App conversion answer was unreadable: %w", err)
	}
	if converted.ID == 0 || converted.PEM == "" || converted.WebhookSecret == "" || converted.Slug == "" {
		return Credentials{}, fmt.Errorf("GitHub's App conversion answer was incomplete")
	}
	if _, err := parsePrivateKey(converted.PEM); err != nil {
		return Credentials{}, err
	}
	return Credentials{
		ID: converted.ID, Slug: converted.Slug, Name: converted.Name, Owner: converted.Owner.Login, HTMLURL: converted.HTMLURL,
		ClientID: converted.ClientID, ClientSecret: converted.ClientSecret, WebhookSecret: converted.WebhookSecret,
		PrivateKey: converted.PEM, CreatedAt: time.Now().UTC(),
	}, nil
}
