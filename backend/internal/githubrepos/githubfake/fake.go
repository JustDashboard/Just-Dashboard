// Package githubfake is a stand-in for github.com and api.github.com that
// the GitHub page's tests run against: the OAuth endpoints (web flow with
// PKCE, device flow, rotating refresh), the App's manifest conversion and
// installation tokens, and the REST endpoints the page's core uses. Nothing
// in a test reaches the real GitHub.
//
// It is deliberately small and strict: a token it did not issue, or one a
// refresh rotated away, is answered 401, as GitHub does.
package githubfake

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Request is one request the fake saw.
type Request struct {
	Method        string
	Path          string
	Query         string
	Authorization string
	Body          string
}

// User is a GitHub account.
type User struct {
	ID    int64
	Login string
	Name  string
}

// Installation is the App installed on an account.
type Installation struct {
	ID        int64
	Account   string
	Type      string // User or Organization
	Selection string // all or selected
	// Permissions defaults to everything the App asks for.
	Permissions map[string]string
	Suspended   bool
	// Visible is the users who can see the installation.
	Visible []int64
	// Repositories are the full names it reaches.
	Repositories []string
	// Fail answers its repository listing with this status.
	Fail int
}

// Repo is a repository.
type Repo struct {
	ID          int64
	Owner       string
	OwnerType   string
	Name        string
	Private     bool
	Archived    bool
	Description string
	Topics      []string
	// Roles is each user's permissions hash on it.
	Roles map[int64]map[string]bool
	Extra map[string]any
}

func (r *Repo) fullName() string { return r.Owner + "/" + r.Name }

// Code is a web flow code waiting for its exchange.
type Code struct {
	UserID      int64
	Challenge   string
	RedirectURI string
}

// Device is one device flow; Answers are given to successive polls, the
// last repeating.
type Device struct {
	UserID  int64
	Answers []string // "", authorization_pending, slow_down, access_denied, expired_token
	polls   int
}

type token struct {
	userID int64
}

// Fake is the stand-in. Fields may be set before the first request.
type Fake struct {
	*httptest.Server
	t *testing.T

	AppID        int64
	ClientID     string
	ClientSecret string
	PEM          string
	// ExpiresIn is the user access token's lifetime GitHub states; zero is
	// an App with expiring tokens turned off (no refresh token).
	ExpiresIn int64
	PageSize  int
	// DeviceFlowDisabled answers device_flow_disabled.
	DeviceFlowDisabled bool
	// Override, when it returns true, answered the request itself.
	Override func(w http.ResponseWriter, r *http.Request) bool

	mu            sync.Mutex
	requests      []Request
	Users         map[int64]*User
	Installations []*Installation
	Repos         map[string]*Repo
	Codes         map[string]Code
	Devices       map[string]*Device
	ManifestCodes map[string]bool
	access        map[string]token
	refresh       map[string]token
	installTokens map[string]map[string]any
	nextID        int64
	issued        int
	// Verifiers are the code_verifier values sent with exchanges.
	Verifiers []string
	// Refreshes counts refresh_token grants answered.
	Refreshes int
}

// New starts a fake with a fresh App identity.
func New(t *testing.T) *Fake {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &Fake{
		t: t, AppID: 4242, ClientID: "Iv1.fakeclient0001", ClientSecret: "fake-client-secret-value",
		PEM:       string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})),
		ExpiresIn: 28800, PageSize: 100,
		Users: map[int64]*User{}, Repos: map[string]*Repo{}, Codes: map[string]Code{}, Devices: map[string]*Device{},
		ManifestCodes: map[string]bool{}, access: map[string]token{}, refresh: map[string]token{},
		installTokens: map[string]map[string]any{}, nextID: 9000,
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Server.Close)
	return f
}

// Requests is every request so far.
func (f *Fake) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

// Reset forgets the requests seen so far.
func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = nil
}

// Saw reports whether a request matched method and path.
func (f *Fake) Saw(method, path string) bool {
	for _, r := range f.Requests() {
		if r.Method == method && r.Path == path {
			return true
		}
	}
	return false
}

// Secrets is every secret the fake has handed out, for tests that assert
// none of them reaches a response.
func (f *Fake) Secrets() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []string{f.ClientSecret, "RSA PRIVATE KEY"}
	for k := range f.access {
		out = append(out, k)
	}
	for k := range f.refresh {
		out = append(out, k)
	}
	for k := range f.installTokens {
		out = append(out, k)
	}
	for k := range f.Devices {
		out = append(out, k)
	}
	return out
}

// IssueToken hands out an access token for a user, as a completed sign-in
// would, and its refresh token.
func (f *Fake) IssueToken(userID int64) (access, refresh string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.issueLocked(userID)
}

func (f *Fake) issueLocked(userID int64) (string, string) {
	f.issued++
	access := fmt.Sprintf("ghu_fake%04d%s", f.issued, randomHex(6))
	refresh := fmt.Sprintf("ghr_fake%04d%s", f.issued, randomHex(6))
	f.access[access] = token{userID: userID}
	if f.ExpiresIn > 0 {
		f.refresh[refresh] = token{userID: userID}
	} else {
		refresh = ""
	}
	return access, refresh
}

// RevokeAll makes every access token GitHub issued stop working, as a
// revoked authorization does.
func (f *Fake) RevokeAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.access = map[string]token{}
	f.refresh = map[string]token{}
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	f.mu.Lock()
	f.requests = append(f.requests, Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Authorization: r.Header.Get("Authorization"), Body: string(body)})
	f.mu.Unlock()
	if f.Override != nil && f.Override(w, r) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost && path == "/login/oauth/access_token":
		f.oauthToken(w, r)
	case r.Method == http.MethodPost && path == "/login/device/code":
		f.deviceCode(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/app-manifests/") && strings.HasSuffix(path, "/conversions"):
		f.conversion(w, strings.TrimSuffix(strings.TrimPrefix(path, "/app-manifests/"), "/conversions"))
	case strings.HasPrefix(path, "/applications/"):
		f.applications(w, r, string(body))
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/app/installations/") && strings.HasSuffix(path, "/access_tokens"):
		f.installationToken(w, r, string(body))
	case r.Method == http.MethodDelete && path == "/installation/token":
		t := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if _, ok := f.installTokens[t]; !ok {
			writeJSON(w, 401, map[string]any{"message": "Bad credentials"})
			return
		}
		delete(f.installTokens, t)
		w.WriteHeader(204)
	default:
		f.api(w, r, string(body))
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *Fake) tokenAnswer(userID int64) map[string]any {
	access, refresh := f.issueLocked(userID)
	answer := map[string]any{"access_token": access, "token_type": "bearer", "scope": ""}
	if f.ExpiresIn > 0 {
		answer["expires_in"] = f.ExpiresIn
		answer["refresh_token"] = refresh
		answer["refresh_token_expires_in"] = 15897600
	}
	return answer
}

func (f *Fake) oauthToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || r.Header.Get("Accept") != "application/json" {
		w.WriteHeader(400)
		return
	}
	if r.PostForm.Get("client_id") != f.ClientID {
		writeJSON(w, 200, map[string]any{"error": "incorrect_client_credentials"})
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "urn:ietf:params:oauth:grant-type:device_code":
		if f.DeviceFlowDisabled {
			writeJSON(w, 200, map[string]any{"error": "device_flow_disabled"})
			return
		}
		device, ok := f.Devices[r.PostForm.Get("device_code")]
		if !ok {
			writeJSON(w, 200, map[string]any{"error": "incorrect_device_code"})
			return
		}
		answer := device.Answers[min(device.polls, len(device.Answers)-1)]
		device.polls++
		if answer != "" {
			writeJSON(w, 200, map[string]any{"error": answer, "interval": 10})
			return
		}
		writeJSON(w, 200, f.tokenAnswer(device.UserID))
	case "refresh_token":
		if r.PostForm.Get("client_secret") != f.ClientSecret {
			writeJSON(w, 200, map[string]any{"error": "incorrect_client_credentials"})
			return
		}
		old, ok := f.refresh[r.PostForm.Get("refresh_token")]
		if !ok {
			writeJSON(w, 200, map[string]any{"error": "bad_refresh_token"})
			return
		}
		// Rotation: the used refresh token and every access token of the
		// user stop working.
		delete(f.refresh, r.PostForm.Get("refresh_token"))
		for k, v := range f.access {
			if v.userID == old.userID {
				delete(f.access, k)
			}
		}
		f.Refreshes++
		writeJSON(w, 200, f.tokenAnswer(old.userID))
	default:
		if r.PostForm.Get("client_secret") != f.ClientSecret {
			writeJSON(w, 200, map[string]any{"error": "incorrect_client_credentials"})
			return
		}
		code, ok := f.Codes[r.PostForm.Get("code")]
		if !ok {
			writeJSON(w, 200, map[string]any{"error": "bad_verification_code", "error_description": "The code passed is incorrect or expired."})
			return
		}
		delete(f.Codes, r.PostForm.Get("code"))
		verifier := r.PostForm.Get("code_verifier")
		f.Verifiers = append(f.Verifiers, verifier)
		sum := sha256.Sum256([]byte(verifier))
		if code.Challenge != "" && base64.RawURLEncoding.EncodeToString(sum[:]) != code.Challenge {
			writeJSON(w, 200, map[string]any{"error": "bad_verification_code", "error_description": "PKCE verification failed."})
			return
		}
		if code.RedirectURI != "" && r.PostForm.Get("redirect_uri") != code.RedirectURI {
			writeJSON(w, 200, map[string]any{"error": "redirect_uri_mismatch"})
			return
		}
		writeJSON(w, 200, f.tokenAnswer(code.UserID))
	}
}

func (f *Fake) deviceCode(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if f.DeviceFlowDisabled {
		writeJSON(w, 400, map[string]any{"error": "device_flow_disabled", "error_description": "Device Flow must be explicitly enabled for this App"})
		return
	}
	code := "devicecode" + randomHex(8)
	if _, ok := f.Devices[code]; !ok {
		f.Devices[code] = &Device{UserID: firstUser(f.Users), Answers: []string{""}}
	}
	writeJSON(w, 200, map[string]any{
		"device_code": code, "user_code": "ABCD-1234", "verification_uri": f.URL + "/login/device", "expires_in": 900, "interval": 5,
	})
}

func firstUser(users map[int64]*User) int64 {
	var ids []int64
	for id := range users {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	if len(ids) == 0 {
		return 0
	}
	return ids[0]
}

// DeviceCodes is every device code handed out, for tests that check none
// reaches the page.
func (f *Fake) DeviceCodes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for k := range f.Devices {
		out = append(out, k)
	}
	return out
}

// SetDeviceAnswers sets what every device flow answers from now on.
func (f *Fake) SetDeviceAnswers(userID int64, answers ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.Devices {
		d.UserID, d.Answers, d.polls = userID, answers, 0
	}
}

// AddCode makes a web flow code.
func (f *Fake) AddCode(code string, c Code) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Codes[code] = c
}

func (f *Fake) conversion(w http.ResponseWriter, code string) {
	if !f.ManifestCodes[code] {
		writeJSON(w, 404, map[string]any{"message": "Not Found"})
		return
	}
	delete(f.ManifestCodes, code)
	writeJSON(w, 201, map[string]any{
		"id": f.AppID, "slug": "just-dashboard-repos-test", "name": "Just Dashboard Repos test",
		"client_id": f.ClientID, "client_secret": f.ClientSecret, "pem": f.PEM, "webhook_secret": nil,
		"html_url": "https://github.com/apps/just-dashboard-repos-test", "owner": map[string]any{"login": "octo-admin", "type": "User"},
		"permissions": map[string]string{"administration": "write", "contents": "write", "metadata": "read"},
	})
}

func (f *Fake) applications(w http.ResponseWriter, r *http.Request, body string) {
	user, pass, ok := r.BasicAuth()
	if !ok || user != f.ClientID || pass != f.ClientSecret {
		writeJSON(w, 401, map[string]any{"message": "Bad credentials"})
		return
	}
	var payload struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal([]byte(body), &payload)
	owner, ok := f.access[payload.AccessToken]
	if !ok {
		writeJSON(w, 404, map[string]any{"message": "Not Found"})
		return
	}
	switch {
	case strings.HasSuffix(r.URL.Path, "/token"):
		delete(f.access, payload.AccessToken)
	case strings.HasSuffix(r.URL.Path, "/grant"):
		for k, v := range f.access {
			if v.userID == owner.userID {
				delete(f.access, k)
			}
		}
		for k, v := range f.refresh {
			if v.userID == owner.userID {
				delete(f.refresh, k)
			}
		}
	}
	w.WriteHeader(204)
}

func (f *Fake) installationToken(w http.ResponseWriter, r *http.Request, body string) {
	auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if strings.Count(auth, ".") != 2 {
		writeJSON(w, 401, map[string]any{"message": "A JSON web token could not be decoded"})
		return
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(body), &payload)
	token := "ghs_fakeinstall" + randomHex(8)
	f.installTokens[token] = payload
	writeJSON(w, 201, map[string]any{"token": token, "expires_at": "2099-01-01T00:00:00Z"})
}

// InstallationTokens is every installation token still valid, with the
// body it was minted with.
func (f *Fake) InstallationTokens() map[string]map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]map[string]any{}
	for k, v := range f.installTokens {
		out[k] = v
	}
	return out
}

func (f *Fake) user(r *http.Request) (*User, bool) {
	t, ok := f.access[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	if !ok {
		return nil, false
	}
	u, ok := f.Users[t.userID]
	return u, ok
}

func (f *Fake) repoJSON(repo *Repo, userID int64) map[string]any {
	visibility := "public"
	if repo.Private {
		visibility = "private"
	}
	ownerType := repo.OwnerType
	if ownerType == "" {
		ownerType = "User"
	}
	permissions := repo.Roles[userID]
	if permissions == nil {
		permissions = map[string]bool{"pull": true}
	}
	out := map[string]any{
		"id": repo.ID, "name": repo.Name, "full_name": repo.fullName(), "owner": map[string]any{"login": repo.Owner, "type": ownerType},
		"description": repo.Description, "visibility": visibility, "private": repo.Private, "archived": repo.Archived,
		"default_branch": "main", "html_url": "https://github.com/" + repo.fullName(), "clone_url": "https://github.com/" + repo.fullName() + ".git",
		"ssh_url": "git@github.com:" + repo.fullName() + ".git", "topics": repo.Topics, "permissions": permissions,
		"has_issues": true, "stargazers_count": 3, "forks_count": 1, "created_at": "2026-01-02T03:04:05Z", "updated_at": "2026-11-01T00:00:00Z",
	}
	if permissions["admin"] {
		out["allow_merge_commit"] = true
		out["allow_squash_merge"] = true
	}
	for k, v := range repo.Extra {
		out[k] = v
	}
	return out
}

func (f *Fake) visibleInstallations(userID int64) []*Installation {
	var out []*Installation
	for _, i := range f.Installations {
		if slices.Contains(i.Visible, userID) {
			out = append(out, i)
		}
	}
	return out
}

func (f *Fake) installationJSON(i *Installation) map[string]any {
	permissions := i.Permissions
	if permissions == nil {
		permissions = map[string]string{"administration": "write", "contents": "write", "issues": "write", "pull_requests": "write", "actions": "write", "metadata": "read"}
	}
	selection := i.Selection
	if selection == "" {
		selection = "all"
	}
	accountType := i.Type
	if accountType == "" {
		accountType = "User"
	}
	out := map[string]any{
		"id": i.ID, "app_id": f.AppID, "account": map[string]any{"login": i.Account, "type": accountType},
		"repository_selection": selection, "permissions": permissions, "suspended_at": nil,
	}
	if i.Suspended {
		out["suspended_at"] = "2026-11-01T00:00:00Z"
	}
	return out
}

// page cuts items into the fake's page size and sets the Link header.
func (f *Fake) page(w http.ResponseWriter, r *http.Request, items []map[string]any) []map[string]any {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	size := f.PageSize
	start := (page - 1) * size
	if start > len(items) {
		start = len(items)
	}
	end := min(start+size, len(items))
	if end < len(items) {
		next := *r.URL
		q := next.Query()
		q.Set("page", strconv.Itoa(page+1))
		next.RawQuery = q.Encode()
		w.Header().Set("Link", fmt.Sprintf(`<%s%s>; rel="next"`, f.URL, next.RequestURI()))
	}
	return items[start:end]
}

func (f *Fake) api(w http.ResponseWriter, r *http.Request, body string) {
	user, ok := f.user(r)
	path := r.URL.Path
	if !ok {
		writeJSON(w, 401, map[string]any{"message": "Bad credentials"})
		return
	}
	w.Header().Set("X-RateLimit-Limit", "5000")
	w.Header().Set("X-RateLimit-Remaining", "4990")
	w.Header().Set("X-RateLimit-Reset", "1900000000")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case r.Method == http.MethodGet && path == "/user":
		writeJSON(w, 200, map[string]any{"id": user.ID, "login": user.Login, "name": user.Name, "html_url": "https://github.com/" + user.Login})
	case r.Method == http.MethodGet && path == "/user/installations":
		var items []map[string]any
		for _, i := range f.visibleInstallations(user.ID) {
			items = append(items, f.installationJSON(i))
		}
		items = f.page(w, r, items)
		writeJSON(w, 200, map[string]any{"total_count": len(items), "installations": nonNil(items)})
	case r.Method == http.MethodGet && len(parts) == 4 && parts[0] == "user" && parts[1] == "installations" && parts[3] == "repositories":
		id, _ := strconv.ParseInt(parts[2], 10, 64)
		var installation *Installation
		for _, i := range f.visibleInstallations(user.ID) {
			if i.ID == id {
				installation = i
			}
		}
		if installation == nil {
			writeJSON(w, 404, map[string]any{"message": "Not Found"})
			return
		}
		if installation.Fail != 0 {
			writeJSON(w, installation.Fail, map[string]any{"message": "Installation listing failed"})
			return
		}
		var items []map[string]any
		for _, name := range installation.Repositories {
			if repo, ok := f.Repos[strings.ToLower(name)]; ok {
				items = append(items, f.repoJSON(repo, user.ID))
			}
		}
		items = f.page(w, r, items)
		writeJSON(w, 200, map[string]any{"total_count": len(items), "repositories": nonNil(items)})
	case r.Method == http.MethodGet && path == "/gitignore/templates":
		writeJSON(w, 200, []string{"Go", "Node", "Python"})
	case r.Method == http.MethodGet && path == "/licenses":
		writeJSON(w, 200, []map[string]any{{"key": "mit", "name": "MIT License", "spdx_id": "MIT"}})
	case r.Method == http.MethodPost && (path == "/user/repos" || (len(parts) == 3 && parts[0] == "orgs" && parts[2] == "repos")):
		var payload map[string]any
		_ = json.Unmarshal([]byte(body), &payload)
		owner, ownerType := user.Login, "User"
		if parts[0] == "orgs" {
			owner, ownerType = parts[1], "Organization"
		}
		name, _ := payload["name"].(string)
		if _, exists := f.Repos[strings.ToLower(owner+"/"+name)]; exists {
			writeJSON(w, 422, map[string]any{"message": "Repository creation failed.", "errors": []map[string]any{{"resource": "Repository", "code": "custom", "field": "name", "message": "name already exists on this account"}}})
			return
		}
		f.nextID++
		private, _ := payload["private"].(bool)
		repo := &Repo{ID: f.nextID, Owner: owner, OwnerType: ownerType, Name: name, Private: private, Roles: map[int64]map[string]bool{user.ID: {"admin": true, "push": true, "pull": true}}}
		f.Repos[strings.ToLower(repo.fullName())] = repo
		for _, i := range f.Installations {
			if strings.EqualFold(i.Account, owner) && i.Selection != "selected" {
				i.Repositories = append(i.Repositories, repo.fullName())
			}
		}
		writeJSON(w, 201, f.repoJSON(repo, user.ID))
	case len(parts) >= 3 && parts[0] == "repos":
		repo, ok := f.Repos[strings.ToLower(parts[1]+"/"+parts[2])]
		if !ok || (repo.Private && repo.Roles[user.ID] == nil) {
			writeJSON(w, 404, map[string]any{"message": "Not Found"})
			return
		}
		switch {
		case len(parts) == 3 && r.Method == http.MethodGet:
			writeJSON(w, 200, f.repoJSON(repo, user.ID))
		case len(parts) == 3 && r.Method == http.MethodPatch:
			var payload map[string]any
			_ = json.Unmarshal([]byte(body), &payload)
			if name, ok := payload["name"].(string); ok {
				delete(f.Repos, strings.ToLower(repo.fullName()))
				repo.Name = name
				f.Repos[strings.ToLower(repo.fullName())] = repo
			}
			if d, ok := payload["description"].(string); ok {
				repo.Description = d
			}
			if a, ok := payload["archived"].(bool); ok {
				repo.Archived = a
			}
			if v, ok := payload["visibility"].(string); ok {
				repo.Private = v == "private"
			}
			if repo.Extra == nil {
				repo.Extra = map[string]any{}
			}
			for k, v := range payload {
				switch k {
				case "name", "description", "archived", "visibility":
				default:
					repo.Extra[k] = v
				}
			}
			writeJSON(w, 200, f.repoJSON(repo, user.ID))
		case len(parts) == 4 && parts[3] == "topics" && r.Method == http.MethodPut:
			var payload struct {
				Names []string `json:"names"`
			}
			_ = json.Unmarshal([]byte(body), &payload)
			repo.Topics = payload.Names
			writeJSON(w, 200, map[string]any{"names": payload.Names})
		case len(parts) == 3 && r.Method == http.MethodDelete:
			delete(f.Repos, strings.ToLower(repo.fullName()))
			w.WriteHeader(204)
		default:
			writeJSON(w, 404, map[string]any{"message": "Not Found"})
		}
	default:
		writeJSON(w, 404, map[string]any{"message": "Not Found", "path": url.PathEscape(path)})
	}
}

func nonNil(items []map[string]any) []map[string]any {
	if items == nil {
		return []map[string]any{}
	}
	return items
}
