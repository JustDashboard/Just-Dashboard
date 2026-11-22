package githubrepos

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Both sign-in flows are GitHub's own for a GitHub App's user access tokens:
// the web flow, with PKCE, where GitHub sends the browser back to a callback
// URL the App registered; and the device flow, where the person types a code
// on github.com, for a dashboard reached at an address GitHub cannot send a
// browser back to. Neither the code, the device code, the verifier nor a
// token ever reaches the page, a log line or an error string.

const (
	// refreshMargin is how long before expiry an access token is replaced,
	// so a request never starts with one that lapses on the way.
	refreshMargin = 5 * time.Minute
	// slowDownStep is what GitHub's slow_down adds to a device flow's
	// interval.
	slowDownStep = 5 * time.Second
)

// OAuthError is GitHub refusing a sign-in exchange. GitHub answers these with
// 200 and an error field, not a status.
type OAuthError struct {
	Code        string
	Description string
}

func (e *OAuthError) Error() string {
	switch e.Code {
	case "bad_verification_code":
		return "GitHub's sign-in code expired or was already used. Start the sign-in again."
	case "redirect_uri_mismatch":
		return "GitHub refused the sign-in address: it is not one of the App's callback URLs. Add it in the App's settings on GitHub."
	case "incorrect_client_credentials":
		return "GitHub did not accept this dashboard's App credentials. The App may have been deleted or its secret reset on GitHub; forget it here and create it again."
	case "unverified_user_email":
		return "GitHub refused the sign-in because the account's primary email address is not verified."
	}
	if e.Description != "" {
		return "GitHub refused the sign-in: " + e.Description
	}
	return "GitHub refused the sign-in (" + e.Code + ")."
}

// DeviceFlowDisabledError is a device sign-in on an App whose "Enable Device
// Flow" box is not ticked. The manifest flow cannot tick it; the App's owner
// does, once, on GitHub.
type DeviceFlowDisabledError struct {
	SettingsURL string
}

func (e *DeviceFlowDisabledError) Error() string {
	return "Signing in with a code is switched off for this dashboard's GitHub App. Tick Enable Device Flow in the App's settings on GitHub (" + e.SettingsURL + "), then try again."
}

// errBadRefresh is GitHub refusing a refresh token: it expired, was used
// already, or the authorization was removed.
var errBadRefresh = errors.New("bad refresh token")

type tokenAnswer struct {
	AccessToken           string `json:"access_token"`
	ExpiresIn             int64  `json:"expires_in"`
	RefreshToken          string `json:"refresh_token"`
	RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"`
	Error                 string `json:"error"`
	Description           string `json:"error_description"`
	Interval              int    `json:"interval"`
}

func (a tokenAnswer) pair(now time.Time) tokenPair {
	pair := tokenPair{Access: a.AccessToken, Refresh: a.RefreshToken}
	// No expires_in is an App with expiring tokens turned off: the token
	// lasts until revoked, and there is no refresh token to rotate.
	if a.ExpiresIn > 0 {
		pair.AccessExpires = now.Add(time.Duration(a.ExpiresIn) * time.Second).UTC()
	}
	if a.RefreshToken != "" && a.RefreshTokenExpiresIn > 0 {
		pair.RefreshExpires = now.Add(time.Duration(a.RefreshTokenExpiresIn) * time.Second).UTC()
	}
	return pair
}

// form posts a form to github.com's OAuth endpoints and decodes the JSON
// answer. GitHub answers form-encoded unless asked for JSON, which would
// decode silently as an empty answer.
func (s *Service) form(ctx context.Context, path string, values url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.webURL()+path, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	response, err := s.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// The transport's error names the URL, which carries nothing
		// secret; the form body is not part of it.
		return &APIError{Kind: KindUnavailable, Path: path, err: err}
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return &APIError{Status: response.StatusCode, Kind: KindUnavailable, Path: path, err: err}
	}
	if json.Unmarshal(raw, out) != nil {
		return &APIError{Status: response.StatusCode, Kind: KindUnavailable, Message: "GitHub's sign-in answer was not readable", Path: path}
	}
	// A refusal with an error field is decoded and returned to the caller,
	// whatever the status; one without is GitHub being unavailable.
	var probe struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &probe)
	if probe.Error == "" && (response.StatusCode < 200 || response.StatusCode > 299) {
		return &APIError{Status: response.StatusCode, Kind: KindUnavailable, Message: fmt.Sprintf("GitHub's sign-in answered %d", response.StatusCode), Path: path}
	}
	return nil
}

// pkce is a verifier and its S256 challenge (RFC 7636).
func pkce() (verifier, challenge string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	return verifier, challengeOf(verifier), nil
}

func challengeOf(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// AuthorizeURL starts the web flow for the signed-in account from the
// dashboard address origin. prompt=select_account lets a person who is
// signed in to GitHub as one account choose another, which is how a second
// GitHub account is connected.
func (s *Service) AuthorizeURL(ctx context.Context, userID int64, origin string) (string, error) {
	parsed, err := ParseOrigin(origin)
	if err != nil {
		return "", refuse(err.Error())
	}
	app, err := s.store.LoadApp(ctx)
	if err != nil {
		return "", err
	}
	redirect := parsed + CallbackPath
	if !slices.Contains(app.CallbackURLs, redirect) {
		return "", &CallbackUnregisteredError{RedirectURI: redirect, Registered: slices.Clone(app.CallbackURLs), SettingsURL: s.AppSettingsURL(app)}
	}
	verifier, challenge, err := pkce()
	if err != nil {
		return "", err
	}
	state, err := s.issueState(pendingState{userID: userID, kind: KindConnection, redirectURI: redirect, verifier: verifier})
	if err != nil {
		return "", err
	}
	query := url.Values{
		"client_id": {app.ClientID}, "redirect_uri": {redirect}, "state": {state},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "prompt": {"select_account"},
	}
	return s.webURL() + "/login/oauth/authorize?" + query.Encode(), nil
}

// CompleteConnection exchanges the web flow's code, with the verifier only
// this server holds, and records the connection.
func (s *Service) CompleteConnection(ctx context.Context, pending *PendingState, code string) (Connection, error) {
	if pending == nil || pending.Kind != KindConnection {
		return Connection{}, ErrState
	}
	code = strings.TrimSpace(code)
	if !validCode(code) {
		return Connection{}, refuse("the code GitHub sent back is malformed")
	}
	app, err := s.store.LoadApp(ctx)
	if err != nil {
		return Connection{}, err
	}
	var answer tokenAnswer
	if err := s.form(ctx, "/login/oauth/access_token", url.Values{
		"client_id": {app.ClientID}, "client_secret": {app.clientSecret}, "code": {code},
		"redirect_uri": {pending.pending.redirectURI}, "code_verifier": {pending.pending.verifier},
	}, &answer); err != nil {
		return Connection{}, err
	}
	if answer.Error != "" {
		return Connection{}, &OAuthError{Code: answer.Error, Description: answer.Description}
	}
	if answer.AccessToken == "" {
		return Connection{}, &OAuthError{Code: "no_token", Description: "GitHub returned no token"}
	}
	return s.connect(ctx, pending.pending.userID, "web", answer.pair(s.now()))
}

// deviceFlow is one device sign-in in progress. It lives on the server: the
// device code is a bearer credential until redeemed, so the page holds an
// opaque id and polls with it.
type deviceFlow struct {
	userID     int64
	deviceCode string
	interval   time.Duration
	next       time.Time
	expires    time.Time
}

// DeviceStart is GitHubDeviceStart.
type DeviceStart struct {
	ID              string `json:"id"`
	UserCode        string `json:"userCode"`
	VerificationURI string `json:"verificationUri"`
	ExpiresIn       int    `json:"expiresIn"`
	Interval        int    `json:"interval"`
}

// DeviceState is GitHubDeviceState.
type DeviceState struct {
	Status     string      `json:"status"`
	Connection *Connection `json:"connection,omitempty"`
	Message    string      `json:"message,omitempty"`
}

// StartDevice asks GitHub for a code the person types on github.com.
func (s *Service) StartDevice(ctx context.Context, userID int64) (DeviceStart, error) {
	app, err := s.store.LoadApp(ctx)
	if err != nil {
		return DeviceStart{}, err
	}
	var answer struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
		Error           string `json:"error"`
		Description     string `json:"error_description"`
	}
	if err := s.form(ctx, "/login/device/code", url.Values{"client_id": {app.ClientID}}, &answer); err != nil {
		return DeviceStart{}, err
	}
	if answer.Error == "device_flow_disabled" {
		return DeviceStart{}, &DeviceFlowDisabledError{SettingsURL: s.AppSettingsURL(app)}
	}
	if answer.Error != "" {
		return DeviceStart{}, &OAuthError{Code: answer.Error, Description: answer.Description}
	}
	if answer.DeviceCode == "" || answer.UserCode == "" {
		return DeviceStart{}, &APIError{Status: http.StatusBadGateway, Kind: KindUnavailable, Message: "GitHub returned no device code", Path: "/login/device/code"}
	}
	if answer.Interval <= 0 {
		answer.Interval = 5
	}
	if answer.ExpiresIn <= 0 {
		answer.ExpiresIn = 900
	}
	if answer.VerificationURI == "" {
		answer.VerificationURI = s.webURL() + "/login/device"
	}
	id, err := randomHex(16)
	if err != nil {
		return DeviceStart{}, err
	}
	now := s.now()
	s.mu.Lock()
	for key, flow := range s.devices {
		if now.After(flow.expires) {
			delete(s.devices, key)
		}
	}
	s.devices[id] = &deviceFlow{
		userID: userID, deviceCode: answer.DeviceCode, interval: time.Duration(answer.Interval) * time.Second,
		next: now, expires: now.Add(time.Duration(answer.ExpiresIn) * time.Second),
	}
	s.mu.Unlock()
	return DeviceStart{ID: id, UserCode: answer.UserCode, VerificationURI: answer.VerificationURI, ExpiresIn: answer.ExpiresIn, Interval: answer.Interval}, nil
}

// PollDevice asks GitHub whether the code was entered, and connects the
// account when it was. The interval is enforced from the flow's own clock: a
// page that polls too fast is answered pending without a request leaving the
// server, because GitHub's answer to that is to slow the whole flow down.
// Another account's flow id answers as an unknown one.
func (s *Service) PollDevice(ctx context.Context, userID int64, id string) (DeviceState, error) {
	expired := DeviceState{Status: "expired", Message: "This sign-in has expired. Start it again."}
	now := s.now()
	s.mu.Lock()
	flow, ok := s.devices[id]
	if !ok || flow.userID != userID {
		s.mu.Unlock()
		return expired, nil
	}
	if now.After(flow.expires) {
		delete(s.devices, id)
		s.mu.Unlock()
		return DeviceState{Status: "expired", Message: "The code expired before it was entered. Start again."}, nil
	}
	if now.Before(flow.next) {
		s.mu.Unlock()
		return DeviceState{Status: "pending"}, nil
	}
	flow.next = now.Add(flow.interval)
	deviceCode := flow.deviceCode
	s.mu.Unlock()

	app, err := s.store.LoadApp(ctx)
	if err != nil {
		return DeviceState{}, err
	}
	var answer tokenAnswer
	if err := s.form(ctx, "/login/oauth/access_token", url.Values{
		"client_id": {app.ClientID}, "device_code": {deviceCode}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"},
	}, &answer); err != nil {
		return DeviceState{}, err
	}
	switch answer.Error {
	case "":
	case "authorization_pending":
		return DeviceState{Status: "pending"}, nil
	case "slow_down":
		s.mu.Lock()
		if f, ok := s.devices[id]; ok {
			f.interval += slowDownStep
			if answer.Interval > 0 && time.Duration(answer.Interval)*time.Second > f.interval {
				f.interval = time.Duration(answer.Interval) * time.Second
			}
			f.next = s.now().Add(f.interval)
		}
		s.mu.Unlock()
		return DeviceState{Status: "pending"}, nil
	case "expired_token":
		s.dropDevice(id)
		return DeviceState{Status: "expired", Message: "The code expired before it was entered. Start again."}, nil
	case "access_denied":
		s.dropDevice(id)
		return DeviceState{Status: "denied", Message: "The sign-in was cancelled on GitHub."}, nil
	case "device_flow_disabled":
		s.dropDevice(id)
		return DeviceState{}, &DeviceFlowDisabledError{SettingsURL: s.AppSettingsURL(app)}
	default:
		s.dropDevice(id)
		return DeviceState{Status: "failed", Message: (&OAuthError{Code: answer.Error, Description: answer.Description}).Error()}, nil
	}
	s.dropDevice(id)
	if answer.AccessToken == "" {
		return DeviceState{Status: "failed", Message: "GitHub returned no token."}, nil
	}
	connection, err := s.connect(ctx, userID, "device", answer.pair(s.now()))
	if err != nil {
		return DeviceState{}, err
	}
	return DeviceState{Status: "complete", Connection: &connection}, nil
}

func (s *Service) dropDevice(id string) {
	s.mu.Lock()
	delete(s.devices, id)
	s.mu.Unlock()
}

// refresh trades a refresh token for a new pair. The old pair stops working
// the moment GitHub answers, so the caller persists the answer before using
// it.
func (s *Service) refresh(ctx context.Context, app App, refreshToken string) (tokenPair, error) {
	var answer tokenAnswer
	if err := s.form(ctx, "/login/oauth/access_token", url.Values{
		"client_id": {app.ClientID}, "client_secret": {app.clientSecret},
		"grant_type": {"refresh_token"}, "refresh_token": {refreshToken},
	}, &answer); err != nil {
		return tokenPair{}, err
	}
	if answer.Error == "bad_refresh_token" {
		return tokenPair{}, errBadRefresh
	}
	if answer.Error != "" {
		return tokenPair{}, &OAuthError{Code: answer.Error, Description: answer.Description}
	}
	if answer.AccessToken == "" {
		return tokenPair{}, &OAuthError{Code: "no_token", Description: "GitHub returned no token"}
	}
	return answer.pair(s.now()), nil
}

// applicationCall is one of the App's own calls about a token (revoking it,
// or the whole grant), authenticated as the App with its client id and
// secret. The token travels in the body, never in the URL.
func (s *Service) applicationCall(ctx context.Context, app App, path, accessToken string) (int, error) {
	body, err := json.Marshal(map[string]string{"access_token": accessToken})
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.apiURL()+"/applications/"+url.PathEscape(app.ClientID)+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.SetBasicAuth(app.ClientID, app.clientSecret)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Content-Type", "application/json")
	response, err := s.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
	return response.StatusCode, nil
}

// revokeToken revokes one user access token on GitHub, best effort: a
// disconnect goes ahead whether or not GitHub answered.
func (s *Service) revokeToken(ctx context.Context, app App, accessToken string) bool {
	if accessToken == "" {
		return false
	}
	status, err := s.applicationCall(ctx, app, "/token", accessToken)
	return err == nil && status >= 200 && status < 300
}

// revokeGrant removes the App's authorization for the token's GitHub
// account, which revokes every token issued to it through this App, refresh
// tokens included. Best effort, as revokeToken.
func (s *Service) revokeGrant(ctx context.Context, app App, accessToken string) bool {
	if accessToken == "" {
		return false
	}
	status, err := s.applicationCall(ctx, app, "/grant", accessToken)
	return err == nil && status >= 200 && status < 300
}
