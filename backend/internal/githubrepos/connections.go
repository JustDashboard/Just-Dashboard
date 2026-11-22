package githubrepos

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const revokedMessage = "GitHub no longer accepts this sign-in: it was revoked on GitHub, or the App's authorization was removed. Connect the account again."
const expiredMessage = "This sign-in has expired: GitHub's refresh token ran out or was refused. Connect the account again."

// Connections lists the signed-in account's own connections.
func (s *Service) Connections(ctx context.Context, userID int64) ([]Connection, error) {
	return s.store.Connections(ctx, userID)
}

// Connection is one of the signed-in account's own connections, or
// ErrConnectionNotFound.
func (s *Service) Connection(ctx context.Context, userID, id int64) (Connection, error) {
	return s.store.Connection(ctx, userID, id)
}

// connect records a sign-in that produced tokens: who GitHub says the token
// belongs to, a new row or the same row reconnected, and the installations
// the account can see.
func (s *Service) connect(ctx context.Context, userID int64, method string, tokens tokenPair) (Connection, error) {
	static := NewClient(s.APIURL, s.HTTP, func(context.Context) (string, error) { return tokens.Access, nil })
	var who profile
	if _, err := static.Do(ctx, http.MethodGet, "/user", nil, &who); err != nil {
		return Connection{}, err
	}
	if who.ID == 0 || who.Login == "" {
		return Connection{}, &APIError{Status: http.StatusBadGateway, Kind: KindUnavailable, Message: "GitHub did not say whose sign-in this is", Path: "/user"}
	}
	id, err := s.store.upsertConnection(ctx, userID, who, method, tokens, s.now())
	if err != nil {
		return Connection{}, err
	}
	s.forgetConnection(id)
	if app, err := s.store.LoadApp(ctx); err == nil {
		// The connection stands without its installations: the list is read
		// again by the next answer that needs it.
		if installations, err := s.readInstallations(ctx, static, app); err == nil {
			_ = s.store.setInstallations(ctx, id, installations, s.now())
		}
	}
	return s.store.Connection(ctx, userID, id)
}

func (s *Service) refreshLock(id int64) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, ok := s.refreshing[id]
	if !ok {
		lock = &sync.Mutex{}
		s.refreshing[id] = lock
	}
	return lock
}

func reconnect(c Connection, status, reason string) *ReconnectError {
	return &ReconnectError{ConnectionID: c.ID, Login: c.Login, Status: status, Reason: reason}
}

// token answers a usable access token for a connection, refreshing it first
// when it expires within five minutes.
//
// GitHub's refresh tokens rotate: once used, the old pair stops working. So
// a refresh happens under the connection's own lock, the row is read again
// inside it (another request may have refreshed already, and its pair is the
// only one that still works), and the new pair is persisted before it is
// used.
func (s *Service) token(ctx context.Context, userID, id int64) (string, error) {
	lock := s.refreshLock(id)
	lock.Lock()
	defer lock.Unlock()
	current, err := s.store.Connection(ctx, userID, id)
	if err != nil {
		return "", err
	}
	switch current.Status {
	case StatusExpired:
		return "", reconnect(current, StatusExpired, current.StatusMessage)
	case StatusRevoked:
		return "", reconnect(current, StatusRevoked, current.StatusMessage)
	}
	now := s.now()
	if current.tokens.AccessExpires.IsZero() || now.Add(refreshMargin).Before(current.tokens.AccessExpires) {
		return current.tokens.Access, nil
	}
	if current.tokens.Refresh == "" || (!current.tokens.RefreshExpires.IsZero() && !now.Before(current.tokens.RefreshExpires)) {
		_ = s.store.setStatus(ctx, id, StatusExpired, expiredMessage, now)
		s.forgetConnection(id)
		return "", reconnect(current, StatusExpired, expiredMessage)
	}
	app, err := s.store.LoadApp(ctx)
	if err != nil {
		return "", err
	}
	pair, err := s.refresh(ctx, app, current.tokens.Refresh)
	if errors.Is(err, errBadRefresh) {
		_ = s.store.setStatus(ctx, id, StatusExpired, expiredMessage, s.now())
		s.forgetConnection(id)
		return "", reconnect(current, StatusExpired, expiredMessage)
	}
	if err != nil {
		return "", err
	}
	if err := s.store.saveTokens(ctx, id, pair, s.now()); err != nil {
		return "", err
	}
	return pair.Access, nil
}

// watchUnauthorized marks a connection revoked when GitHub answers 401 to a
// request made with its token. It sits in the transport, so every call made
// through a connection's client is watched, the repository resources' too.
type watchUnauthorized struct {
	base           http.RoundTripper
	onUnauthorized func(usedToken string)
}

func (w watchUnauthorized) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := w.base.RoundTrip(req)
	if err == nil && response.StatusCode == http.StatusUnauthorized {
		w.onUnauthorized(strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer "))
	}
	return response, err
}

// markRevoked records a 401 as a revoked connection, unless the token that
// was refused is no longer the connection's: a request that started with the
// old pair while another refreshed it is refused for the rotation, not for
// a revocation.
func (s *Service) markRevoked(userID, id int64, usedToken string) {
	ctx, cancel := backgroundContext()
	defer cancel()
	lock := s.refreshLock(id)
	lock.Lock()
	defer lock.Unlock()
	current, err := s.store.Connection(ctx, userID, id)
	if err != nil || current.tokens.Access != usedToken || current.Status != StatusActive {
		return
	}
	_ = s.store.setStatus(ctx, id, StatusRevoked, revokedMessage, s.now())
	s.forgetConnection(id)
}

// client is a client acting as one connection's GitHub account.
func (s *Service) client(c Connection) *Client {
	base := s.HTTP.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	userID, id := c.OwnerUserID, c.ID
	httpClient := &http.Client{
		Timeout: s.HTTP.Timeout,
		Transport: watchUnauthorized{base: base, onUnauthorized: func(used string) {
			s.markRevoked(userID, id, used)
		}},
	}
	return NewClient(s.APIURL, httpClient, func(ctx context.Context) (string, error) {
		return s.token(ctx, userID, id)
	})
}

// active loads a connection and refuses one GitHub no longer accepts.
func (s *Service) active(ctx context.Context, userID, id int64) (Connection, error) {
	c, err := s.store.Connection(ctx, userID, id)
	if err != nil {
		return Connection{}, err
	}
	if c.Status != StatusActive {
		return Connection{}, reconnect(c, c.Status, c.StatusMessage)
	}
	return c, nil
}

// rawInstallation is one entry of GET /user/installations.
type rawInstallation struct {
	ID                  int64             `json:"id"`
	AppID               int64             `json:"app_id"`
	Account             Owner             `json:"account"`
	RepositorySelection string            `json:"repository_selection"`
	Permissions         map[string]string `json:"permissions"`
	SuspendedAt         *time.Time        `json:"suspended_at"`
}

// InstallationSettingsURL is where an installation's repositories and
// permissions are changed on GitHub.
func (s *Service) InstallationSettingsURL(id int64, account Owner) string {
	if account.Type == "Organization" {
		return s.webURL() + "/organizations/" + url.PathEscape(account.Login) + "/settings/installations/" + strconv.FormatInt(id, 10)
	}
	return s.webURL() + "/settings/installations/" + strconv.FormatInt(id, 10)
}

// readInstallations is every installation of this App the connected account
// can see, which is what a user access token's GET /user/installations
// answers.
func (s *Service) readInstallations(ctx context.Context, client *Client, app App) ([]Installation, error) {
	out := []Installation{}
	_, err := client.Walk(ctx, "/user/installations?per_page=100", func(page []byte) error {
		var body struct {
			Installations []rawInstallation `json:"installations"`
		}
		if err := json.Unmarshal(page, &body); err != nil {
			return &APIError{Kind: KindUnavailable, Message: "GitHub's installation list was not readable", Path: "/user/installations"}
		}
		for _, raw := range body.Installations {
			if raw.AppID != 0 && app.ID != 0 && raw.AppID != app.ID {
				continue
			}
			selection := raw.RepositorySelection
			if selection != "selected" {
				selection = "all"
			}
			permissions := raw.Permissions
			if permissions == nil {
				permissions = map[string]string{}
			}
			out = append(out, Installation{
				ID: raw.ID, Account: raw.Account, RepositorySelection: selection, Permissions: permissions,
				SettingsURL: s.InstallationSettingsURL(raw.ID, raw.Account), Suspended: raw.SuspendedAt != nil,
			})
		}
		return nil
	})
	return out, err
}

// installations answers the connection's installations, reading them again
// from GitHub when the stored list is older than a minute or force is set.
func (s *Service) installations(ctx context.Context, c Connection, client *Client, force bool) ([]Installation, error) {
	if !force && !c.InstallationsReadAt.IsZero() && s.now().Sub(c.InstallationsReadAt) < installationsTTL {
		return c.Installations, nil
	}
	app, err := s.store.LoadApp(ctx)
	if err != nil {
		return nil, err
	}
	installations, err := s.readInstallations(ctx, client, app)
	if err != nil {
		return nil, err
	}
	if err := s.store.setInstallations(ctx, c.ID, installations, s.now()); err != nil {
		return nil, err
	}
	return installations, nil
}

// Refresh reads the connection's profile and installations from GitHub again
// and drops everything cached for it: what the page's refresh does after the
// person changed the App's installation on GitHub.
func (s *Service) Refresh(ctx context.Context, userID, id int64) (Connection, error) {
	c, err := s.active(ctx, userID, id)
	if err != nil {
		return Connection{}, err
	}
	s.forgetConnection(id)
	client := s.client(c)
	var who profile
	if _, err := client.Do(ctx, http.MethodGet, "/user", nil, &who); err != nil {
		return Connection{}, err
	}
	if who.Login != "" {
		if err := s.store.setProfile(ctx, id, who, s.now()); err != nil {
			return Connection{}, err
		}
	}
	if _, err := s.installations(ctx, c, client, true); err != nil {
		return Connection{}, err
	}
	return s.store.Connection(ctx, userID, id)
}

// Disconnect revokes the connection on GitHub, best effort, and deletes it
// whether or not GitHub answered. When no other connection on this
// dashboard is the same GitHub account, the whole grant is revoked, which
// ends every token GitHub issued to it through this App; otherwise only this
// connection's token is, so the other dashboard account's connection keeps
// working. It answers the connection it removed, for the audit entry.
func (s *Service) Disconnect(ctx context.Context, userID, id int64) (Connection, error) {
	c, err := s.store.Connection(ctx, userID, id)
	if err != nil {
		return Connection{}, err
	}
	if app, err := s.store.LoadApp(ctx); err == nil {
		access := c.tokens.Access
		if c.Status == StatusActive {
			// An access token past its expiry is refused by the revocation
			// endpoints too; refreshing first gives them one they accept.
			if fresh, err := s.token(ctx, userID, id); err == nil {
				access = fresh
			}
		}
		others, err := s.store.othersOfGitHubUser(ctx, c.GitHubID, c.ID)
		if err == nil && others == 0 {
			if !s.revokeGrant(ctx, app, access) {
				s.revokeToken(ctx, app, access)
			}
		} else {
			s.revokeToken(ctx, app, access)
		}
	}
	if err := s.store.deleteConnection(ctx, userID, id); err != nil {
		return Connection{}, err
	}
	s.forgetConnection(id)
	s.mu.Lock()
	delete(s.refreshing, id)
	s.mu.Unlock()
	return c, nil
}
