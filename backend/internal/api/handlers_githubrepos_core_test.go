package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/githubrepos/githubfake"
	"github.com/Wayy01/Just-Dashboard/backend/internal/gitx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
)

// These drive the GitHub page's core routes through the whole chain, with
// each role's session, against a stand-in GitHub (githubfake). Nothing here
// reaches github.com, and no test walks every route: each calls the routes
// it is about.

const ghOrigin = "https://dash.example.test"

type ghEnv struct {
	t        *testing.T
	s        *Server
	fake     *githubfake.Fake
	admin    *ghClient
	other    *ghClient
	limited  *ghClient
	readonly *ghClient
}

// ghClient is a session that checks every answer it is given for the
// stand-in's secrets: no response may carry a token, a code, the client
// secret or the private key.
type ghClient struct {
	*client
	env *ghEnv
}

func (c *ghClient) call(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	c.t.Helper()
	if headers == nil {
		headers = map[string]string{}
	}
	if _, ok := headers["Origin"]; !ok {
		headers["Origin"] = ghOrigin
	}
	w := c.do(method, path, body, headers)
	for _, secret := range c.env.fake.Secrets() {
		if secret != "" && strings.Contains(w.Body.String(), secret) {
			c.t.Fatalf("%s %s answered a secret (%q): %s", method, path, secret, w.Body)
		}
	}
	return w
}

func ghCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	return linkErrorCode(t, w.Body.String())
}

func newGHEnv(t *testing.T) *ghEnv {
	t.Helper()
	s := testServer(t)
	fake := githubfake.New(t)
	repos := s.modules.githubRepos
	repos.APIURL, repos.WebURL, repos.GitURL = fake.URL, fake.URL, fake.URL
	repos.HTTP = fake.Client()
	fake.Users[1001] = &githubfake.User{ID: 1001, Login: "octo-dev", Name: "Octo Dev"}
	fake.Users[1002] = &githubfake.User{ID: 1002, Login: "other-dev"}
	fake.Installations = []*githubfake.Installation{
		{ID: 11, Account: "octo-dev", Visible: []int64{1001}, Repositories: []string{"octo-dev/app", "octo-dev/site"}},
		{ID: 12, Account: "acme-org", Type: "Organization", Visible: []int64{1001}, Repositories: []string{"acme-org/api"}},
	}
	fake.Repos["octo-dev/app"] = &githubfake.Repo{ID: 1, Owner: "octo-dev", Name: "app", Private: true, Roles: map[int64]map[string]bool{1001: {"admin": true, "push": true, "pull": true}}}
	fake.Repos["octo-dev/site"] = &githubfake.Repo{ID: 2, Owner: "octo-dev", Name: "site", Roles: map[int64]map[string]bool{1001: {"admin": true, "push": true, "pull": true}}}
	fake.Repos["acme-org/api"] = &githubfake.Repo{ID: 3, Owner: "acme-org", OwnerType: "Organization", Name: "api", Private: true, Roles: map[int64]map[string]bool{1001: {"pull": true}}}
	e := &ghEnv{t: t, s: s, fake: fake}
	h := s.Routes()
	session := func(name string, role auth.Role) *ghClient {
		return &ghClient{client: &client{t: t, h: h, cookie: signInAs(t, s, name, role)}, env: e}
	}
	e.admin = session("gh-admin", auth.RoleAdmin)
	e.other = session("gh-other", auth.RoleAdmin)
	e.limited = session("gh-limited", auth.RoleLimited)
	e.readonly = session("gh-readonly", auth.RoleReadOnly)
	return e
}

func (e *ghEnv) createApp() {
	e.t.Helper()
	w := e.admin.call(http.MethodPost, "/api/v1/github/app/manifest", `{"origin":"`+ghOrigin+`"}`, nil)
	if w.Code != 200 {
		e.t.Fatalf("manifest = %d %s", w.Code, w.Body)
	}
	var start struct{ State string }
	_ = json.Unmarshal(w.Body.Bytes(), &start)
	e.fake.ManifestCodes["manifest-code"] = true
	w = e.admin.call(http.MethodPost, "/api/v1/github/callback", `{"code":"manifest-code","state":"`+start.State+`"}`, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"kind":"app"`) {
		e.t.Fatalf("app callback = %d %s", w.Code, w.Body)
	}
}

// connect signs c in to GitHub as githubID through the web flow, and
// answers the connection's id.
func (e *ghEnv) connect(c *ghClient, githubID int64) string {
	e.t.Helper()
	w := c.call(http.MethodPost, "/api/v1/github/connections/authorize", `{"origin":"`+ghOrigin+`"}`, nil)
	if w.Code != 200 {
		e.t.Fatalf("authorize = %d %s", w.Code, w.Body)
	}
	var answer struct{ URL string }
	_ = json.Unmarshal(w.Body.Bytes(), &answer)
	parsed, _ := url.Parse(answer.URL)
	q := parsed.Query()
	code := "code-" + q.Get("state")[:10]
	e.fake.AddCode(code, githubfake.Code{UserID: githubID, Challenge: q.Get("code_challenge"), RedirectURI: q.Get("redirect_uri")})
	w = c.call(http.MethodPost, "/api/v1/github/callback", `{"code":"`+code+`","state":"`+q.Get("state")+`"}`, nil)
	if w.Code != 200 {
		e.t.Fatalf("connection callback = %d %s", w.Code, w.Body)
	}
	var result struct {
		Kind       string
		Connection struct{ ID int64 }
	}
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if result.Kind != "connection" || result.Connection.ID == 0 {
		e.t.Fatalf("callback = %s", w.Body)
	}
	return jsonNumber(result.Connection.ID)
}

func jsonNumber(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestEveryGitHubPageRouteNeedsASession(t *testing.T) {
	e := newGHEnv(t)
	anonymous := &client{t: t, h: e.s.Routes()}
	for _, rt := range [][2]string{
		{"GET", "/api/v1/github/app"}, {"POST", "/api/v1/github/app/manifest"}, {"POST", "/api/v1/github/app/callback-urls"},
		{"DELETE", "/api/v1/github/app"}, {"POST", "/api/v1/github/callback"}, {"GET", "/api/v1/github/connections"},
		{"POST", "/api/v1/github/connections/authorize"}, {"POST", "/api/v1/github/connections/device"},
		{"GET", "/api/v1/github/connections/device/abc"}, {"POST", "/api/v1/github/connections/1/refresh"},
		{"DELETE", "/api/v1/github/connections/1"}, {"GET", "/api/v1/github/connections/1/repositories"},
		{"GET", "/api/v1/github/connections/1/create-options"}, {"POST", "/api/v1/github/connections/1/repositories"},
		{"GET", "/api/v1/github/connections/1/repos/octo-dev/app"}, {"PATCH", "/api/v1/github/connections/1/repos/octo-dev/app"},
		{"POST", "/api/v1/github/connections/1/repos/octo-dev/app/archive"}, {"POST", "/api/v1/github/connections/1/repos/octo-dev/app/visibility"},
		{"DELETE", "/api/v1/github/connections/1/repos/octo-dev/app"}, {"POST", "/api/v1/github/connections/1/repos/octo-dev/app/clone"},
	} {
		if w := anonymous.do(rt[0], rt[1], "{}", nil); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session = %d", rt[0], rt[1], w.Code)
		}
	}
	if n := len(e.fake.Requests()); n != 0 {
		t.Fatalf("GitHub was asked %d times", n)
	}
}

func TestOnlyAnAdministratorCreatesOrForgetsTheApp(t *testing.T) {
	e := newGHEnv(t)
	if w := e.admin.call(http.MethodGet, "/api/v1/github/app", "", nil); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"configured":false}` {
		t.Fatalf("setup = %d %s", w.Code, w.Body)
	}
	for _, c := range []*ghClient{e.limited, e.readonly} {
		if w := c.call(http.MethodPost, "/api/v1/github/app/manifest", `{"origin":"`+ghOrigin+`"}`, nil); w.Code != 403 {
			t.Errorf("manifest as %s = %d", c.cookie[:8], w.Code)
		}
	}
	// The body's origin must be the address the request came from.
	w := e.admin.call(http.MethodPost, "/api/v1/github/app/manifest", `{"origin":"https://elsewhere.test"}`, nil)
	if w.Code != 400 {
		t.Fatalf("mismatched origin = %d %s", w.Code, w.Body)
	}
	w = e.admin.call(http.MethodPost, "/api/v1/github/app/manifest", `{"origin":"`+ghOrigin+`"}`, map[string]string{"Origin": ""})
	if w.Code != 400 {
		t.Fatalf("no Origin header = %d %s", w.Code, w.Body)
	}
	e.createApp()
	w = e.readonly.call(http.MethodGet, "/api/v1/github/app", "", nil)
	var setup struct {
		Configured bool
		App        struct {
			Slug         string
			CallbackURLs []string `json:"callbackUrls"`
		}
	}
	_ = json.Unmarshal(w.Body.Bytes(), &setup)
	if w.Code != 200 || !setup.Configured || setup.App.Slug != "just-dashboard-repos-test" || !slices.Equal(setup.App.CallbackURLs, []string{ghOrigin + "/github/callback"}) {
		t.Fatalf("setup = %d %s", w.Code, w.Body)
	}
	if w := e.limited.call(http.MethodPost, "/api/v1/github/app/callback-urls", `{"origin":"`+ghOrigin+`"}`, nil); w.Code != 403 {
		t.Fatalf("callback url as limited = %d", w.Code)
	}
	w = e.admin.call(http.MethodPost, "/api/v1/github/app/callback-urls", `{"origin":"http://10.1.2.3:8080"}`, map[string]string{"Origin": "http://10.1.2.3:8080"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "http://10.1.2.3:8080/github/callback") {
		t.Fatalf("callback url = %d %s", w.Code, w.Body)
	}
	if w := e.limited.call(http.MethodDelete, "/api/v1/github/app", "", nil); w.Code != 403 {
		t.Fatalf("forget as limited = %d", w.Code)
	}
	e.connect(e.limited, 1001)
	if w := e.admin.call(http.MethodDelete, "/api/v1/github/app", "", nil); w.Code != 204 {
		t.Fatalf("forget = %d %s", w.Code, w.Body)
	}
	if w := e.limited.call(http.MethodGet, "/api/v1/github/connections", "", nil); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("connections after forgetting = %s", w.Body)
	}
}

func TestACallbackIsFinishedOnlyByWhoStartedIt(t *testing.T) {
	e := newGHEnv(t)
	e.createApp()
	w := e.admin.call(http.MethodPost, "/api/v1/github/connections/authorize", `{"origin":"`+ghOrigin+`"}`, nil)
	var answer struct{ URL string }
	_ = json.Unmarshal(w.Body.Bytes(), &answer)
	parsed, _ := url.Parse(answer.URL)
	state := parsed.Query().Get("state")
	e.fake.AddCode("code-1", githubfake.Code{UserID: 1001})
	w = e.other.call(http.MethodPost, "/api/v1/github/callback", `{"code":"code-1","state":"`+state+`"}`, nil)
	if w.Code != 400 || ghCode(t, w) != "github_state" {
		t.Fatalf("another account's state = %d %s", w.Code, w.Body)
	}
	w = e.admin.call(http.MethodPost, "/api/v1/github/callback", `{"code":"code-1","state":"made-up"}`, nil)
	if w.Code != 400 || ghCode(t, w) != "github_state" {
		t.Fatalf("made-up state = %d %s", w.Code, w.Body)
	}
	if e.fake.Saw(http.MethodPost, "/login/oauth/access_token") {
		t.Fatal("a refused state reached GitHub")
	}
	// A sign-in from an address the App has no callback URL for.
	w = e.admin.call(http.MethodPost, "/api/v1/github/connections/authorize", `{"origin":"http://box.lan:8080"}`, map[string]string{"Origin": "http://box.lan:8080"})
	if w.Code != 409 || ghCode(t, w) != "github_callback_unregistered" || !strings.Contains(w.Body.String(), ghOrigin+"/github/callback") {
		t.Fatalf("unregistered = %d %s", w.Code, w.Body)
	}
	e.fake.DeviceFlowDisabled = true
	if w := e.admin.call(http.MethodPost, "/api/v1/github/connections/device", "", nil); w.Code != 409 || ghCode(t, w) != "github_device_flow_disabled" {
		t.Fatalf("device flow disabled = %d %s", w.Code, w.Body)
	}
}

func TestTheDeviceFlowConnectsThroughTheRoutes(t *testing.T) {
	e := newGHEnv(t)
	e.createApp()
	w := e.limited.call(http.MethodPost, "/api/v1/github/connections/device", "", nil)
	if w.Code != 200 {
		t.Fatalf("device = %d %s", w.Code, w.Body)
	}
	var start struct{ ID string }
	_ = json.Unmarshal(w.Body.Bytes(), &start)
	e.fake.SetDeviceAnswers(1001, "")
	if w := e.readonly.call(http.MethodGet, "/api/v1/github/connections/device/"+start.ID, "", nil); w.Code != 403 {
		t.Fatalf("poll as readonly = %d", w.Code)
	}
	w = e.limited.call(http.MethodGet, "/api/v1/github/connections/device/"+start.ID, "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"complete"`) || !strings.Contains(w.Body.String(), `"method":"device"`) {
		t.Fatalf("poll = %d %s", w.Code, w.Body)
	}
}

func TestRolesDecideWhatAConnectionMayDo(t *testing.T) {
	e := newGHEnv(t)
	e.createApp()
	// A readonly account may not connect.
	if w := e.readonly.call(http.MethodPost, "/api/v1/github/connections/authorize", `{"origin":"`+ghOrigin+`"}`, nil); w.Code != 403 {
		t.Fatalf("authorize as readonly = %d", w.Code)
	}
	if w := e.readonly.call(http.MethodPost, "/api/v1/github/connections/device", "", nil); w.Code != 403 {
		t.Fatalf("device as readonly = %d", w.Code)
	}
	limited := e.connect(e.limited, 1001)
	base := "/api/v1/github/connections/" + limited
	if w := e.limited.call(http.MethodGet, base+"/repositories", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"fullName":"octo-dev/app"`) {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}
	if w := e.limited.call(http.MethodGet, base+"/create-options", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"canCreate":true`) {
		t.Fatalf("create options = %d %s", w.Code, w.Body)
	}
	w := e.limited.call(http.MethodPost, base+"/repositories", `{"owner":"octo-dev","name":"new-thing","visibility":"private","autoInit":true}`, nil)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"reachable":true`) {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	if w := e.limited.call(http.MethodPatch, base+"/repos/octo-dev/app", `{"description":"Changed"}`, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"description":"Changed"`) {
		t.Fatalf("update as limited = %d %s", w.Code, w.Body)
	}
	if w := e.limited.call(http.MethodPost, base+"/repos/octo-dev/app/archive", `{"archived":true}`, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"archived":true`) {
		t.Fatalf("archive as limited = %d %s", w.Code, w.Body)
	}
	if w := e.limited.call(http.MethodPost, base+"/repos/octo-dev/app/archive", `{"archived":false}`, nil); w.Code != 200 {
		t.Fatalf("unarchive as limited = %d %s", w.Code, w.Body)
	}
	// Limited holds no destructive capability.
	if w := e.limited.call(http.MethodPost, base+"/repos/octo-dev/app/visibility", `{"visibility":"public"}`, nil); w.Code != 403 {
		t.Fatalf("visibility as limited = %d", w.Code)
	}
	if w := e.limited.call(http.MethodDelete, base+"/repos/octo-dev/app", "", map[string]string{"X-Confirm": "octo-dev/app"}); w.Code != 403 {
		t.Fatalf("delete as limited = %d", w.Code)
	}

	// A readonly account with a connection (made while it was allowed to)
	// reads but changes nothing.
	readonly := e.connect(e.limited, 1002) // a second GitHub account of the limited account
	_ = readonly
	if _, err := e.s.Store.DB.Exec(`UPDATE github_connections SET owner_user_id = (SELECT id FROM users WHERE username = 'gh-readonly') WHERE github_id = 1002`); err != nil {
		t.Fatal(err)
	}
	e.fake.Installations[0].Visible = append(e.fake.Installations[0].Visible, 1002)
	rbase := "/api/v1/github/connections/" + readonly
	if w := e.readonly.call(http.MethodGet, rbase+"/repositories", "", nil); w.Code != 200 {
		t.Fatalf("list as readonly = %d %s", w.Code, w.Body)
	}
	for _, rt := range [][3]string{
		{"POST", rbase + "/repositories", `{"owner":"other-dev","name":"x","visibility":"public"}`},
		{"PATCH", rbase + "/repos/octo-dev/site", `{"description":"x"}`},
		{"POST", rbase + "/repos/octo-dev/site/archive", `{"archived":true}`},
		{"POST", rbase + "/repos/octo-dev/site/clone", `{"parent":"/tmp"}`},
		{"DELETE", rbase, ""},
	} {
		if w := e.readonly.call(rt[0], rt[1], rt[2], nil); w.Code != 403 {
			t.Errorf("%s %s as readonly = %d %s", rt[0], rt[1], w.Code, w.Body)
		}
	}
}

func TestAnotherAccountsConnectionIsAMissingOne(t *testing.T) {
	e := newGHEnv(t)
	e.createApp()
	id := e.connect(e.admin, 1001)
	base := "/api/v1/github/connections/" + id
	e.fake.Reset()
	for _, rt := range [][3]string{
		{"GET", base + "/repositories", ""}, {"GET", base + "/create-options", ""}, {"POST", base + "/refresh", ""},
		{"GET", base + "/repos/octo-dev/app", ""}, {"PATCH", base + "/repos/octo-dev/app", `{"description":"x"}`},
		{"DELETE", base + "/repos/octo-dev/app", ""}, {"DELETE", base, ""},
		{"POST", base + "/repositories", `{"owner":"octo-dev","name":"x","visibility":"public"}`},
		{"GET", "/api/v1/github/connections/999999/repositories", ""}, {"GET", "/api/v1/github/connections/abc/repositories", ""},
	} {
		w := e.other.call(rt[0], rt[1], rt[2], map[string]string{"X-Confirm": "octo-dev/app"})
		if w.Code != 404 || ghCode(t, w) != "github_connection" {
			t.Errorf("%s %s by another administrator = %d %s", rt[0], rt[1], w.Code, w.Body)
		}
	}
	if n := len(e.fake.Requests()); n != 0 {
		t.Fatalf("another account's requests reached GitHub %d times", n)
	}
	if w := e.other.call(http.MethodGet, "/api/v1/github/connections", "", nil); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("other's list = %s", w.Body)
	}
}

func TestDeletingARepositoryTakesItsFullName(t *testing.T) {
	e := newGHEnv(t)
	e.createApp()
	base := "/api/v1/github/connections/" + e.connect(e.admin, 1001)
	if w := e.admin.call(http.MethodGet, base+"/repos/octo-dev/app", "", nil); w.Code != 200 {
		t.Fatalf("detail = %d %s", w.Code, w.Body)
	}
	e.fake.Reset()
	w := e.admin.call(http.MethodDelete, base+"/repos/octo-dev/app", "", nil)
	if w.Code != 428 || !strings.Contains(w.Body.String(), `"phrase":"octo-dev/app"`) {
		t.Fatalf("no phrase = %d %s", w.Code, w.Body)
	}
	if w := e.admin.call(http.MethodDelete, base+"/repos/octo-dev/app", "", map[string]string{"X-Confirm": "app"}); w.Code != 412 {
		t.Fatalf("wrong phrase = %d %s", w.Code, w.Body)
	}
	if e.fake.Saw(http.MethodDelete, "/repos/octo-dev/app") {
		t.Fatal("an unconfirmed delete reached GitHub")
	}
	// A reader is refused for the role, before the phrase is asked for and
	// before GitHub is.
	w = e.admin.call(http.MethodDelete, base+"/repos/acme-org/api", "", map[string]string{"X-Confirm": "acme-org/api"})
	if w.Code != 403 || ghCode(t, w) != "github_permission" || e.fake.Saw(http.MethodDelete, "/repos/acme-org/api") {
		t.Fatalf("delete as reader = %d %s", w.Code, w.Body)
	}
	if w := e.admin.call(http.MethodDelete, base+"/repos/octo-dev/app", "", map[string]string{"X-Confirm": "octo-dev/app"}); w.Code != 204 {
		t.Fatalf("delete = %d %s", w.Code, w.Body)
	}
	if !e.fake.Saw(http.MethodDelete, "/repos/octo-dev/app") {
		t.Fatal("GitHub was not asked")
	}
	// Visibility is an ordinary confirmation, held to the role and grant.
	if w := e.admin.call(http.MethodPost, base+"/repos/octo-dev/site/visibility", `{"visibility":"private"}`, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"visibility":"private"`) {
		t.Fatalf("visibility = %d %s", w.Code, w.Body)
	}
}

func TestARefusalByAbilitiesNeverReachesGitHub(t *testing.T) {
	e := newGHEnv(t)
	e.createApp()
	base := "/api/v1/github/connections/" + e.connect(e.admin, 1001)
	if w := e.admin.call(http.MethodGet, base+"/repos/acme-org/api", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"administer":false`) {
		t.Fatalf("detail = %d %s", w.Code, w.Body)
	}
	e.fake.Reset()
	for _, rt := range [][2]string{
		{"PATCH", `{"description":"x"}`}, {"POST /archive", `{"archived":true}`}, {"POST /visibility", `{"visibility":"public"}`},
	} {
		method, suffix, _ := strings.Cut(rt[0], " ")
		w := e.admin.call(method, base+"/repos/acme-org/api"+suffix, rt[1], nil)
		if w.Code != 403 || ghCode(t, w) != "github_permission" || !strings.Contains(w.Body.String(), "role on acme-org/api is read") {
			t.Errorf("%s = %d %s", rt[0], w.Code, w.Body)
		}
	}
	if requests := e.fake.Requests(); len(requests) != 0 {
		t.Fatalf("refusals reached GitHub: %+v", requests)
	}
}

func TestGitHubsRefusalsAnswerInThePagesWords(t *testing.T) {
	e := newGHEnv(t)
	e.createApp()
	base := "/api/v1/github/connections/" + e.connect(e.admin, 1001)
	if w := e.admin.call(http.MethodGet, base+"/repos/octo-dev/missing", "", nil); w.Code != 404 || ghCode(t, w) != "github_not_found" {
		t.Fatalf("missing = %d %s", w.Code, w.Body)
	}
	e.fake.Override = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/user/installations" {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"message":"You have exceeded a secondary rate limit"}`))
			return true
		}
		return false
	}
	w := e.admin.call(http.MethodGet, base+"/repositories?refresh=1", "", nil)
	if w.Code != 429 || ghCode(t, w) != "github_rate_limited" || w.Header().Get("Retry-After") == "" {
		t.Fatalf("rate limited = %d %s (Retry-After %q)", w.Code, w.Body, w.Header().Get("Retry-After"))
	}
	e.fake.Override = nil
	// GitHub stops accepting the sign-in: 409 github_reconnect, and the
	// connection says so until it is connected again.
	e.fake.RevokeAll()
	if w := e.admin.call(http.MethodGet, base+"/repositories?refresh=1", "", nil); w.Code != 409 || ghCode(t, w) != "github_reconnect" {
		t.Fatalf("revoked = %d %s", w.Code, w.Body)
	}
	if w := e.admin.call(http.MethodGet, base+"/repositories", "", nil); w.Code != 409 || ghCode(t, w) != "github_reconnect" {
		t.Fatalf("revoked, again = %d %s", w.Code, w.Body)
	}
	if w := e.admin.call(http.MethodGet, "/api/v1/github/connections", "", nil); !strings.Contains(w.Body.String(), `"status":"revoked"`) {
		t.Fatalf("connections = %s", w.Body)
	}
	if again := e.connect(e.admin, 1001); "/api/v1/github/connections/"+again != base {
		t.Fatalf("reconnect made a new connection %s", again)
	}
	if w := e.admin.call(http.MethodGet, base+"/repositories", "", nil); w.Code != 200 {
		t.Fatalf("after reconnecting = %d %s", w.Code, w.Body)
	}
	if w := e.admin.call(http.MethodDelete, base, "", nil); w.Code != 204 || !e.fake.Saw(http.MethodDelete, "/applications/"+e.fake.ClientID+"/grant") {
		t.Fatalf("disconnect = %d %s", w.Code, w.Body)
	}
}

func TestAHubSessionCannotSignInToGitHubOrDeleteARepository(t *testing.T) {
	e := newSignInEnv(t)
	hub := e.enter()
	for _, rt := range [][3]string{
		{"POST", "/api/v1/github/connections/device", ""},
		{"GET", "/api/v1/github/connections/device/abc", ""},
		{"POST", "/api/v1/github/connections/authorize", `{"origin":"` + ghOrigin + `"}`},
		{"POST", "/api/v1/github/callback", `{"code":"c","state":"s"}`},
		{"POST", "/api/v1/github/app/manifest", `{"origin":"` + ghOrigin + `"}`},
		{"DELETE", "/api/v1/github/connections/1/repos/octo-dev/app", ""},
	} {
		w := hub.do(rt[0], rt[1], rt[2], map[string]string{"Origin": ghOrigin, "X-Confirm": "octo-dev/app"})
		if w.Code != 403 || codeOf(t, w) != "hub_session_not_allowed" {
			t.Errorf("%s %s with a hub session = %d %s", rt[0], rt[1], w.Code, w.Body)
		}
	}
	// Reading stays open to it.
	if w := hub.do(http.MethodGet, "/api/v1/github/connections", "", nil); w.Code != 200 {
		t.Fatalf("list with a hub session = %d %s", w.Code, w.Body)
	}
}

// gitHTTPServer serves bare repositories under root with git http-backend,
// recording each request's Authorization header.
func gitHTTPServer(t *testing.T, root string) (*httptest.Server, func() []string) {
	t.Helper()
	out, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Skip("git is not installed")
	}
	backend := filepath.Join(strings.TrimSpace(string(out)), "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Skip("git http-backend is not installed")
	}
	var mu sync.Mutex
	var seen []string
	handler := &cgi.Handler{Path: backend, Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"}, Stderr: os.Stderr}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(seen)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.test", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.test")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func waitForGitHubClone(t *testing.T, s *Server, id string) (jobs.Job, []jobs.Line) {
	t.Helper()
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		job, lines, ok := s.modules.jobs.Get(id)
		if !ok {
			t.Fatalf("job %s is gone", id)
		}
		if job.Status != jobs.StatusRunning {
			return job, lines
		}
	}
	t.Fatalf("job %s did not finish", id)
	return jobs.Job{}, nil
}

func TestCloneToServerFetchesWithAReadOnlyTokenItNeverShows(t *testing.T) {
	e := newGHEnv(t)
	e.createApp()
	base := "/api/v1/github/connections/" + e.connect(e.admin, 1001)

	// A bare repository the stand-in git server serves as octo-dev/app.git.
	served := t.TempDir()
	work := t.TempDir()
	runGit(t, work, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-qm", "first")
	if err := os.MkdirAll(filepath.Join(served, "octo-dev"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, served, "clone", "-q", "--bare", work, filepath.Join(served, "octo-dev", "app.git"))
	gitServer, seen := gitHTTPServer(t, served)
	e.s.modules.githubRepos.GitURL = gitServer.URL
	root := t.TempDir()
	e.s.modules.git = gitx.New([]string{root})

	w := e.admin.call(http.MethodPost, base+"/repos/octo-dev/app/clone", `{"parent":"`+root+`"}`, nil)
	if w.Code != 202 {
		t.Fatalf("clone = %d %s", w.Code, w.Body)
	}
	var start struct {
		JobID  string `json:"jobId"`
		Target string `json:"target"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &start)
	if start.Target != filepath.Join(root, "app") || start.JobID == "" {
		t.Fatalf("start = %s", w.Body)
	}
	job, lines := waitForGitHubClone(t, e.s, start.JobID)
	text := []string{}
	for _, line := range lines {
		text = append(text, line.Text)
	}
	if job.Status != jobs.StatusSucceeded || job.Kind != "github.clone" || job.Capability != auth.CapServiceControl {
		t.Fatalf("job = %+v\n%s", job, strings.Join(text, "\n"))
	}
	if data, err := os.ReadFile(filepath.Join(start.Target, "README.md")); err != nil || string(data) != "hello\n" {
		t.Fatalf("cloned file = %q %v", data, err)
	}
	// The git server saw the downscoped token, and nothing else.
	var minted string
	for _, header := range seen() {
		if header == "" {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(header, "Basic "))
		if err != nil || !strings.HasPrefix(string(decoded), "x-access-token:ghs_") {
			t.Fatalf("authorization = %q", header)
		}
		minted = strings.TrimPrefix(string(decoded), "x-access-token:")
	}
	if minted == "" {
		t.Fatalf("the git server never saw a token: %q", seen())
	}
	if _, live := e.fake.InstallationTokens()[minted]; live {
		t.Fatal("the clone's token was not revoked")
	}
	config, _ := os.ReadFile(filepath.Join(start.Target, ".git", "config"))
	for _, where := range []string{string(config), strings.Join(text, "\n"), job.Error, job.Title, job.Target} {
		if strings.Contains(where, minted) || strings.Contains(where, base64.StdEncoding.EncodeToString([]byte("x-access-token:"+minted))) {
			t.Fatalf("the token is visible: %s", where)
		}
	}
	if !strings.Contains(string(config), "url = "+gitServer.URL+"/octo-dev/app.git") {
		t.Fatalf(".git/config = %s", config)
	}
	if !strings.Contains(strings.Join(text, "\n"), "$ git clone -- "+gitServer.URL+"/octo-dev/app.git app") {
		t.Fatalf("console = %s", strings.Join(text, "\n"))
	}
	// Local Git finds it.
	if repos, err := e.s.modules.git.Discover(t.Context()); err != nil || len(repos) != 1 {
		t.Fatalf("discover = %+v %v", repos, err)
	}

	// Never into an existing directory, never outside the roots.
	for _, body := range []string{
		`{"parent":"` + root + `"}`, `{"parent":"` + root + `","name":"../escape"}`,
		`{"parent":"` + t.TempDir() + `"}`, `{"parent":"` + root + `","name":".git"}`,
	} {
		if w := e.admin.call(http.MethodPost, base+"/repos/octo-dev/app/clone", body, nil); w.Code < 400 || w.Code >= 500 {
			t.Errorf("clone %s = %d %s", body, w.Code, w.Body)
		}
	}
}
