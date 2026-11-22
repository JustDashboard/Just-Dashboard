package githubrepos_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/githubrepos"
	"github.com/Wayy01/Just-Dashboard/backend/internal/githubrepos/githubfake"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

const origin = "https://dash.example.test"

type harness struct {
	t     *testing.T
	s     *githubrepos.Service
	fake  *githubfake.Fake
	alice int64
	bob   int64
	clock time.Time
}

func (h *harness) now() time.Time { return h.clock }

func newHarness(t *testing.T) *harness {
	t.Helper()
	st, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sealer, err := auth.NewSealer(strings.Repeat("cd", 32))
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, fake: githubfake.New(t), clock: time.Now().UTC()}
	for i, name := range []string{"alice", "bob"} {
		result, err := st.DB.Exec(`INSERT INTO users(username, password_hash, role, created_at) VALUES(?, 'x', 'admin', 0)`, name)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := result.LastInsertId()
		if i == 0 {
			h.alice = id
		} else {
			h.bob = id
		}
	}
	h.s = githubrepos.New(st.DB, sealer)
	h.s.APIURL, h.s.WebURL, h.s.GitURL = h.fake.URL, h.fake.URL, h.fake.URL
	h.s.HTTP = h.fake.Client()
	h.s.SetClock(h.now)
	h.fake.Users[1001] = &githubfake.User{ID: 1001, Login: "octo-dev", Name: "Octo Dev"}
	h.fake.Users[1002] = &githubfake.User{ID: 1002, Login: "other-dev"}
	return h
}

// createApp runs the manifest flow against the fake.
func (h *harness) createApp() {
	h.t.Helper()
	start, err := h.s.BeginManifest(context.Background(), h.alice, githubrepos.ManifestRequest{Origin: origin})
	if err != nil {
		h.t.Fatal(err)
	}
	pending, err := h.s.TakeState(h.alice, start.State)
	if err != nil {
		h.t.Fatal(err)
	}
	h.fake.ManifestCodes["manifestcode1"] = true
	if _, err := h.s.CompleteApp(context.Background(), pending, "manifestcode1"); err != nil {
		h.t.Fatal(err)
	}
}

// connect signs alice in as the fake's user 1001 through the web flow.
func (h *harness) connect(userID, githubID int64) githubrepos.Connection {
	h.t.Helper()
	authorize, err := h.s.AuthorizeURL(context.Background(), userID, origin)
	if err != nil {
		h.t.Fatal(err)
	}
	parsed, _ := url.Parse(authorize)
	q := parsed.Query()
	code := "webcode" + q.Get("state")[:8]
	h.fake.AddCode(code, githubfake.Code{UserID: githubID, Challenge: q.Get("code_challenge"), RedirectURI: q.Get("redirect_uri")})
	pending, err := h.s.TakeState(userID, q.Get("state"))
	if err != nil {
		h.t.Fatal(err)
	}
	connection, err := h.s.CompleteConnection(context.Background(), pending, code)
	if err != nil {
		h.t.Fatal(err)
	}
	return connection
}

func (h *harness) standardRepos() {
	h.fake.Installations = []*githubfake.Installation{
		{ID: 11, Account: "octo-dev", Visible: []int64{1001, 1002}, Repositories: []string{"octo-dev/app", "octo-dev/site"}},
		{ID: 12, Account: "acme-org", Type: "Organization", Visible: []int64{1001}, Selection: "selected", Repositories: []string{"acme-org/api"}},
	}
	h.fake.Repos["octo-dev/app"] = &githubfake.Repo{ID: 1, Owner: "octo-dev", Name: "app", Private: true, Roles: map[int64]map[string]bool{1001: {"admin": true, "push": true, "pull": true}}}
	h.fake.Repos["octo-dev/site"] = &githubfake.Repo{ID: 2, Owner: "octo-dev", Name: "site", Roles: map[int64]map[string]bool{1001: {"admin": true, "push": true, "pull": true}}}
	h.fake.Repos["acme-org/api"] = &githubfake.Repo{ID: 3, Owner: "acme-org", OwnerType: "Organization", Name: "api", Private: true, Roles: map[int64]map[string]bool{1001: {"pull": true}}}
}

func TestTheManifestNeedsNoPublicAddress(t *testing.T) {
	h := newHarness(t)
	start, err := h.s.BeginManifest(context.Background(), h.alice, githubrepos.ManifestRequest{Origin: "http://192.168.1.20:8080", Organization: "acme-org"})
	if err != nil {
		t.Fatal(err)
	}
	m := start.Manifest
	if m.HookAttributes["active"] != false || m.HookAttributes["url"] != "http://192.168.1.20:8080/api/v1/github/hook" {
		t.Fatalf("hook = %v", m.HookAttributes)
	}
	if m.RedirectURL != "http://192.168.1.20:8080/github/callback" || len(m.CallbackURLs) != 1 || m.CallbackURLs[0] != m.RedirectURL ||
		m.SetupURL != "http://192.168.1.20:8080/github?installed=1" || !m.SetupOnUpdate || m.RequestOAuthOnInstall || !m.Public {
		t.Fatalf("manifest = %+v", m)
	}
	want := map[string]string{"administration": "write", "contents": "write", "issues": "write", "pull_requests": "write", "actions": "write", "checks": "read", "statuses": "read", "metadata": "read"}
	if len(m.DefaultPermissions) != len(want) {
		t.Fatalf("permissions = %v", m.DefaultPermissions)
	}
	for k, v := range want {
		if m.DefaultPermissions[k] != v {
			t.Fatalf("permissions = %v", m.DefaultPermissions)
		}
	}
	if _, ok := m.DefaultPermissions["workflows"]; ok || len(m.DefaultEvents) != 0 {
		t.Fatalf("manifest asks for too much: %+v", m)
	}
	if !strings.HasPrefix(start.Action, h.fake.URL+"/organizations/acme-org/settings/apps/new?state=") || !strings.HasPrefix(m.Name, "Just Dashboard Repos ") {
		t.Fatalf("action = %s name = %s", start.Action, m.Name)
	}
	encoded, _ := json.Marshal(m)
	if !strings.Contains(string(encoded), `"request_oauth_on_install":false`) || !strings.Contains(string(encoded), `"default_events":[]`) {
		t.Fatalf("encoded = %s", encoded)
	}
	private := false
	start, err = h.s.BeginManifest(context.Background(), h.alice, githubrepos.ManifestRequest{Origin: origin, Public: &private})
	if err != nil || start.Manifest.Public {
		t.Fatalf("private manifest = %+v %v", start.Manifest, err)
	}
	for _, bad := range []githubrepos.ManifestRequest{
		{Origin: "https://dash.example.test/path"}, {Origin: "ftp://dash.example.test"}, {Origin: "https://user:pw@dash.example.test"},
		{Origin: origin, Name: strings.Repeat("x", 35)}, {Origin: origin, Organization: "-bad"},
	} {
		if _, err := h.s.BeginManifest(context.Background(), h.alice, bad); githubrepos.KindOf(err) != githubrepos.KindValidation {
			t.Errorf("BeginManifest(%+v) = %v", bad, err)
		}
	}
}

func TestAStateIsSingleUseShortLivedAndBoundToItsAccount(t *testing.T) {
	h := newHarness(t)
	start, err := h.s.BeginManifest(context.Background(), h.alice, githubrepos.ManifestRequest{Origin: origin})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.s.TakeState(h.bob, start.State); !errors.Is(err, githubrepos.ErrState) {
		t.Fatalf("another account's state = %v", err)
	}
	// Bob's try did not spend it.
	pending, err := h.s.TakeState(h.alice, start.State)
	if err != nil || pending.Kind != githubrepos.KindApp {
		t.Fatalf("own state = %+v %v", pending, err)
	}
	if _, err := h.s.TakeState(h.alice, start.State); !errors.Is(err, githubrepos.ErrState) {
		t.Fatalf("second use = %v", err)
	}
	// A state of the wrong kind cannot finish the other flow.
	if _, err := h.s.CompleteConnection(context.Background(), pending, "code"); !errors.Is(err, githubrepos.ErrState) {
		t.Fatalf("app state finishing a connection = %v", err)
	}
	start, _ = h.s.BeginManifest(context.Background(), h.alice, githubrepos.ManifestRequest{Origin: origin})
	h.clock = h.clock.Add(11 * time.Minute)
	if _, err := h.s.TakeState(h.alice, start.State); !errors.Is(err, githubrepos.ErrState) {
		t.Fatalf("expired state = %v", err)
	}
}

func TestTheManifestCodeBecomesASealedApp(t *testing.T) {
	h := newHarness(t)
	h.createApp()
	setup, err := h.s.Setup(context.Background())
	if err != nil || !setup.Configured || setup.App.ID != h.fake.AppID || setup.App.SettingsURL != h.fake.URL+"/settings/apps/just-dashboard-repos-test" ||
		setup.App.InstallURL != h.fake.URL+"/apps/just-dashboard-repos-test/installations/new" || len(setup.App.CallbackURLs) != 1 ||
		setup.App.CallbackURLs[0] != origin+"/github/callback" {
		t.Fatalf("setup = %+v %v", setup.App, err)
	}
	encoded, _ := json.Marshal(setup)
	for _, secret := range h.fake.Secrets() {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("setup carries a secret: %s", encoded)
		}
	}
	if _, err := h.s.BeginManifest(context.Background(), h.alice, githubrepos.ManifestRequest{Origin: origin}); !errors.Is(err, githubrepos.ErrAppExists) {
		t.Fatalf("second App = %v", err)
	}
	setup, err = h.s.AddCallbackOrigin(context.Background(), "http://10.0.0.5:3000")
	if err != nil || len(setup.App.CallbackURLs) != 2 || setup.App.CallbackURLs[1] != "http://10.0.0.5:3000/github/callback" {
		t.Fatalf("callback urls = %+v %v", setup.App, err)
	}
	for i := 0; i < 8; i++ {
		if _, err := h.s.AddCallbackOrigin(context.Background(), "http://10.0.0."+string(rune('a'+i))+".test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.s.AddCallbackOrigin(context.Background(), "http://one-too-many.test"); githubrepos.KindOf(err) != githubrepos.KindValidation {
		t.Fatalf("eleventh callback = %v", err)
	}
}

func TestTheWebFlowSendsTheVerifierOfItsChallenge(t *testing.T) {
	h := newHarness(t)
	h.createApp()
	authorize, err := h.s.AuthorizeURL(context.Background(), h.alice, origin)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(authorize)
	q := parsed.Query()
	if parsed.Path != "/login/oauth/authorize" || q.Get("code_challenge_method") != "S256" || q.Get("prompt") != "select_account" ||
		q.Get("client_id") != h.fake.ClientID || q.Get("redirect_uri") != origin+"/github/callback" || len(q.Get("state")) != 64 || q.Get("code_challenge") == "" {
		t.Fatalf("authorize = %s", authorize)
	}
	connection := h.connect(h.alice, 1001)
	if connection.Login != "octo-dev" || connection.Method != "web" || connection.Status != "active" {
		t.Fatalf("connection = %+v", connection)
	}
	if len(h.fake.Verifiers) != 1 || h.fake.Verifiers[0] == "" {
		t.Fatalf("verifiers = %v", h.fake.Verifiers)
	}
	// An address the App was not told about is refused before GitHub is.
	_, err = h.s.AuthorizeURL(context.Background(), h.alice, "http://elsewhere.test")
	var unregistered *githubrepos.CallbackUnregisteredError
	if !errors.As(err, &unregistered) || !strings.Contains(err.Error(), origin+"/github/callback") {
		t.Fatalf("unregistered = %v", err)
	}
	// Reconnecting the same GitHub account updates the row in place.
	again := h.connect(h.alice, 1001)
	if again.ID != connection.ID {
		t.Fatalf("reconnect made a new row: %d then %d", connection.ID, again.ID)
	}
	encoded, _ := json.Marshal(again)
	for _, secret := range h.fake.Secrets() {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("connection carries a secret: %s", encoded)
		}
	}
	if strings.Contains(string(encoded), "1001") || strings.Contains(string(encoded), "ownerUser") {
		t.Fatalf("connection carries internal ids: %s", encoded)
	}
}

func TestASignInRefusalIsReadFromAnOKAnswer(t *testing.T) {
	h := newHarness(t)
	h.createApp()
	authorize, _ := h.s.AuthorizeURL(context.Background(), h.alice, origin)
	parsed, _ := url.Parse(authorize)
	pending, err := h.s.TakeState(h.alice, parsed.Query().Get("state"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.s.CompleteConnection(context.Background(), pending, "nosuchcode")
	var refused *githubrepos.OAuthError
	if !errors.As(err, &refused) || refused.Code != "bad_verification_code" || strings.Contains(err.Error(), "nosuchcode") {
		t.Fatalf("err = %v", err)
	}
	// A verifier that does not match the challenge is refused by GitHub.
	authorize, _ = h.s.AuthorizeURL(context.Background(), h.alice, origin)
	parsed, _ = url.Parse(authorize)
	h.fake.AddCode("mismatch", githubfake.Code{UserID: 1001, Challenge: "not-the-challenge"})
	pending, _ = h.s.TakeState(h.alice, parsed.Query().Get("state"))
	if _, err := h.s.CompleteConnection(context.Background(), pending, "mismatch"); !errors.As(err, &refused) {
		t.Fatalf("mismatched verifier = %v", err)
	}
}

func TestTheDeviceFlowKeepsGitHubsPace(t *testing.T) {
	h := newHarness(t)
	h.createApp()
	start, err := h.s.StartDevice(context.Background(), h.alice)
	if err != nil || start.UserCode != "ABCD-1234" || start.Interval != 5 || len(start.ID) != 32 {
		t.Fatalf("start = %+v %v", start, err)
	}
	for _, code := range h.fake.DeviceCodes() {
		if strings.Contains(start.ID, code) {
			t.Fatal("the device code reached the page")
		}
	}
	h.fake.SetDeviceAnswers(1001, "authorization_pending", "slow_down", "")
	tokenPolls := func() int {
		n := 0
		for _, r := range h.fake.Requests() {
			if r.Path == "/login/oauth/access_token" {
				n++
			}
		}
		return n
	}
	state, err := h.s.PollDevice(context.Background(), h.alice, start.ID)
	if err != nil || state.Status != "pending" || tokenPolls() != 1 {
		t.Fatalf("first poll = %+v %v (%d)", state, err, tokenPolls())
	}
	// Too soon: answered here, GitHub not asked.
	h.clock = h.clock.Add(2 * time.Second)
	state, _ = h.s.PollDevice(context.Background(), h.alice, start.ID)
	if state.Status != "pending" || tokenPolls() != 1 {
		t.Fatalf("early poll reached GitHub: %+v (%d)", state, tokenPolls())
	}
	// Another account's poll of the same id learns nothing.
	if other, _ := h.s.PollDevice(context.Background(), h.bob, start.ID); other.Status != "expired" || tokenPolls() != 1 {
		t.Fatalf("another account's poll = %+v", other)
	}
	h.clock = h.clock.Add(4 * time.Second)
	state, _ = h.s.PollDevice(context.Background(), h.alice, start.ID) // slow_down
	if state.Status != "pending" || tokenPolls() != 2 {
		t.Fatalf("slow_down poll = %+v", state)
	}
	// slow_down added five seconds and GitHub asked for ten: nine is early.
	h.clock = h.clock.Add(9 * time.Second)
	if state, _ = h.s.PollDevice(context.Background(), h.alice, start.ID); tokenPolls() != 2 {
		t.Fatalf("poll after slow_down came too soon: %+v", state)
	}
	h.clock = h.clock.Add(2 * time.Second)
	state, err = h.s.PollDevice(context.Background(), h.alice, start.ID)
	if err != nil || state.Status != "complete" || state.Connection == nil || state.Connection.Method != "device" || state.Connection.Login != "octo-dev" {
		t.Fatalf("complete = %+v %v", state, err)
	}
	if again, _ := h.s.PollDevice(context.Background(), h.alice, start.ID); again.Status != "expired" {
		t.Fatalf("a finished flow answers %+v", again)
	}

	for answer, want := range map[string]string{"access_denied": "denied", "expired_token": "expired"} {
		start, err := h.s.StartDevice(context.Background(), h.alice)
		if err != nil {
			t.Fatal(err)
		}
		h.fake.SetDeviceAnswers(1001, answer)
		h.clock = h.clock.Add(time.Minute)
		if state, err := h.s.PollDevice(context.Background(), h.alice, start.ID); err != nil || state.Status != want {
			t.Fatalf("%s = %+v %v", answer, state, err)
		}
	}
	start, _ = h.s.StartDevice(context.Background(), h.alice)
	h.clock = h.clock.Add(16 * time.Minute)
	if state, _ := h.s.PollDevice(context.Background(), h.alice, start.ID); state.Status != "expired" {
		t.Fatalf("a flow past its expiry = %+v", state)
	}

	h.fake.DeviceFlowDisabled = true
	_, err = h.s.StartDevice(context.Background(), h.alice)
	var disabled *githubrepos.DeviceFlowDisabledError
	if !errors.As(err, &disabled) || !strings.Contains(err.Error(), "Enable Device Flow") || !strings.Contains(err.Error(), "/settings/apps/just-dashboard-repos-test") {
		t.Fatalf("disabled = %v", err)
	}
}

func TestARefreshRotatesOnceAndIsPersistedBeforeUse(t *testing.T) {
	h := newHarness(t)
	h.createApp()
	h.standardRepos()
	connection := h.connect(h.alice, 1001)
	// Seven hours fifty-six minutes later the token has four minutes left:
	// inside the margin, so the next call refreshes first.
	h.clock = h.clock.Add(7*time.Hour + 56*time.Minute)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = h.s.Refresh(context.Background(), h.alice, connection.ID)
		}()
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("refresh errors = %v", errs)
	}
	if h.fake.Refreshes != 1 {
		t.Fatalf("refreshed %d times", h.fake.Refreshes)
	}
	// The new pair was persisted: a service built over the same store, with
	// none of this one's memory, uses it without refreshing again.
	h.clock = h.clock.Add(time.Minute)
	if _, err := h.s.Repositories(context.Background(), h.alice, connection.ID, true); err != nil {
		t.Fatal(err)
	}
	if h.fake.Refreshes != 1 {
		t.Fatalf("refreshed again: %d", h.fake.Refreshes)
	}
}

func TestARefusedRefreshExpiresTheConnection(t *testing.T) {
	h := newHarness(t)
	h.createApp()
	connection := h.connect(h.alice, 1001)
	h.fake.RevokeAll()
	h.clock = h.clock.Add(8 * time.Hour)
	_, err := h.s.Refresh(context.Background(), h.alice, connection.ID)
	var reconnect *githubrepos.ReconnectError
	if !errors.As(err, &reconnect) || reconnect.Status != "expired" {
		t.Fatalf("err = %v", err)
	}
	stored, _ := h.s.Connection(context.Background(), h.alice, connection.ID)
	if stored.Status != "expired" || stored.StatusMessage == "" {
		t.Fatalf("stored = %+v", stored)
	}
	// Signing in again makes it active, in place.
	if again := h.connect(h.alice, 1001); again.ID != connection.ID || again.Status != "active" {
		t.Fatalf("reconnect = %+v", again)
	}
}

func TestA401RevokesTheConnection(t *testing.T) {
	h := newHarness(t)
	h.createApp()
	h.standardRepos()
	connection := h.connect(h.alice, 1001)
	h.fake.RevokeAll()
	_, err := h.s.Repositories(context.Background(), h.alice, connection.ID, true)
	if githubrepos.KindOf(err) != githubrepos.KindUnauthorized {
		t.Fatalf("err = %v", err)
	}
	stored, _ := h.s.Connection(context.Background(), h.alice, connection.ID)
	if stored.Status != "revoked" {
		t.Fatalf("status = %s", stored.Status)
	}
	var reconnect *githubrepos.ReconnectError
	if _, err := h.s.Repositories(context.Background(), h.alice, connection.ID, true); !errors.As(err, &reconnect) || reconnect.Status != "revoked" {
		t.Fatalf("revoked connection = %v", err)
	}
}

func TestTheListWalksPagesDedupesAndKeepsGoingPastAFailure(t *testing.T) {
	h := newHarness(t)
	h.createApp()
	h.standardRepos()
	h.fake.PageSize = 1
	// The same repository through two installations is listed once.
	h.fake.Installations = append(h.fake.Installations,
		&githubfake.Installation{ID: 13, Account: "octo-dev", Visible: []int64{1001}, Repositories: []string{"octo-dev/app"}},
		&githubfake.Installation{ID: 14, Account: "broken-org", Type: "Organization", Visible: []int64{1001}, Fail: 500})
	connection := h.connect(h.alice, 1001)
	list, err := h.s.Repositories(context.Background(), h.alice, connection.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range list.Repositories {
		names = append(names, r.FullName)
	}
	if strings.Join(names, ",") != "acme-org/api,octo-dev/app,octo-dev/site" {
		t.Fatalf("names = %v", names)
	}
	if len(list.Partial) != 1 || list.Partial[0].InstallationID != 14 || list.Partial[0].Account != "broken-org" || list.Truncated {
		t.Fatalf("partial = %+v truncated = %v", list.Partial, list.Truncated)
	}
	if list.RateLimit == nil || list.RateLimit.Limit != 5000 || list.Connection.ID != connection.ID {
		t.Fatalf("list = %+v", list)
	}
	byName := map[string]githubrepos.Repository{}
	for _, r := range list.Repositories {
		byName[r.FullName] = r
	}
	if app := byName["octo-dev/app"]; app.Role != "admin" || app.Relation != "owned" || !app.Can.Delete || !app.Can.Administer || app.Visibility != "private" {
		t.Fatalf("app = %+v", app)
	}
	if api := byName["acme-org/api"]; api.Role != "read" || api.Relation != "organization" || api.Can.Administer || api.Can.Reasons["administer"] == "" || !api.Can.CreateIssues {
		t.Fatalf("api = %+v", api)
	}
	// Answered from memory within the minute.
	h.fake.Reset()
	if _, err := h.s.Repositories(context.Background(), h.alice, connection.ID, false); err != nil || len(h.fake.Requests()) != 0 {
		t.Fatalf("cached list asked GitHub %d times (%v)", len(h.fake.Requests()), err)
	}
	if _, err := h.s.Repositories(context.Background(), h.alice, connection.ID, true); err != nil || len(h.fake.Requests()) == 0 {
		t.Fatalf("refresh did not ask GitHub (%v)", err)
	}
}

func TestTheListStopsAtItsPageBudget(t *testing.T) {
	h := newHarness(t)
	h.createApp()
	h.fake.PageSize = 1
	var repos []string
	for i := range githubrepos.MaxPages + 5 {
		name := "octo-dev/r" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		repos = append(repos, name)
		h.fake.Repos[name] = &githubfake.Repo{ID: int64(100 + i), Owner: "octo-dev", Name: strings.TrimPrefix(name, "octo-dev/")}
	}
	h.fake.Installations = []*githubfake.Installation{{ID: 11, Account: "octo-dev", Visible: []int64{1001}, Repositories: repos}}
	connection := h.connect(h.alice, 1001)
	list, err := h.s.Repositories(context.Background(), h.alice, connection.ID, false)
	if err != nil || !list.Truncated || len(list.Repositories) != githubrepos.MaxPages {
		t.Fatalf("truncated = %v, %d repositories, %v", list.Truncated, len(list.Repositories), err)
	}
}

func TestARateLimitIsTheWholeAnswer(t *testing.T) {
	h := newHarness(t)
	h.createApp()
	h.standardRepos()
	connection := h.connect(h.alice, 1001)
	h.fake.Override = func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, "/repositories") {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", "1900000000")
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
			return true
		}
		return false
	}
	_, err := h.s.Repositories(context.Background(), h.alice, connection.ID, true)
	var api *githubrepos.APIError
	if !errors.As(err, &api) || api.Kind != githubrepos.KindRateLimited || api.RetryAt.Unix() != 1900000000 {
		t.Fatalf("err = %v", err)
	}
}

func TestAConnectionIsItsOwnersAlone(t *testing.T) {
	h := newHarness(t)
	h.createApp()
	h.standardRepos()
	connection := h.connect(h.alice, 1001)
	if _, err := h.s.Connection(context.Background(), h.bob, connection.ID); !errors.Is(err, githubrepos.ErrConnectionNotFound) {
		t.Fatalf("bob reads alice's connection: %v", err)
	}
	if _, err := h.s.Repositories(context.Background(), h.bob, connection.ID, false); !errors.Is(err, githubrepos.ErrConnectionNotFound) {
		t.Fatalf("bob lists alice's repositories: %v", err)
	}
	if _, err := h.s.Access(context.Background(), h.bob, connection.ID, "octo-dev", "app"); !errors.Is(err, githubrepos.ErrConnectionNotFound) {
		t.Fatalf("bob reaches alice's repository: %v", err)
	}
	if _, err := h.s.Disconnect(context.Background(), h.bob, connection.ID); !errors.Is(err, githubrepos.ErrConnectionNotFound) {
		t.Fatalf("bob disconnects alice: %v", err)
	}
	if list, _ := h.s.Connections(context.Background(), h.bob); len(list) != 0 {
		t.Fatalf("bob's list = %+v", list)
	}
}

func TestAccessNeedsAnInstallationThatCoversTheRepository(t *testing.T) {
	h := newHarness(t)
	h.createApp()
	h.standardRepos()
	// A public repository of an organisation the App is installed on with
	// selected repositories, and not selected: readable, but not managed.
	h.fake.Repos["acme-org/public-docs"] = &githubfake.Repo{ID: 4, Owner: "acme-org", OwnerType: "Organization", Name: "public-docs"}
	// A public repository of an account the App is not installed on.
	h.fake.Repos["stranger/tool"] = &githubfake.Repo{ID: 5, Owner: "stranger", Name: "tool"}
	connection := h.connect(h.alice, 1001)
	access, err := h.s.Access(context.Background(), h.alice, connection.ID, "Octo-Dev", "APP")
	if err != nil || access.Owner != "octo-dev" || access.Name != "app" || access.Installation.ID != 11 || access.Repository.Role != "admin" {
		t.Fatalf("access = %+v %v", access, err)
	}
	if _, err := h.s.Access(context.Background(), h.alice, connection.ID, "acme-org", "api"); err != nil {
		t.Fatalf("selected and covered = %v", err)
	}
	for _, name := range [][2]string{{"acme-org", "public-docs"}, {"stranger", "tool"}, {"octo-dev", "missing"}, {"..", "x"}, {"octo-dev", ".."}} {
		if _, err := h.s.Access(context.Background(), h.alice, connection.ID, name[0], name[1]); githubrepos.KindOf(err) != githubrepos.KindNotFound {
			t.Errorf("%s/%s = %v", name[0], name[1], err)
		}
	}
	detail, err := h.s.Detail(context.Background(), access)
	if err != nil || detail.Settings.AllowMergeCommit == nil || !*detail.Settings.AllowMergeCommit || detail.Installation.ID != 11 || !detail.Settings.HasIssues {
		t.Fatalf("detail = %+v %v", detail, err)
	}
}

func TestCreateOptionsAndCreateFollowTheInstallations(t *testing.T) {
	h := newHarness(t)
	h.createApp()
	h.standardRepos()
	h.fake.Installations = append(h.fake.Installations,
		&githubfake.Installation{ID: 15, Account: "readonly-org", Type: "Organization", Visible: []int64{1001}, Permissions: map[string]string{"contents": "read"}},
		&githubfake.Installation{ID: 16, Account: "someone-else", Visible: []int64{1001}})
	connection := h.connect(h.alice, 1001)
	options, err := h.s.CreateOptions(context.Background(), h.alice, connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	var owners []string
	for _, o := range options.Owners {
		owners = append(owners, o.Login)
	}
	if strings.Join(owners, ",") != "octo-dev,acme-org,readonly-org" {
		t.Fatalf("owners = %v", owners)
	}
	if !options.Owners[0].CanCreate || !options.Owners[1].CanCreate || options.Owners[1].Note == "" || options.Owners[2].CanCreate || options.Owners[2].Note == "" {
		t.Fatalf("owners = %+v", options.Owners)
	}
	if len(options.GitignoreTemplates) != 3 || len(options.Licenses) != 1 || options.Licenses[0].SPDXID != "MIT" {
		t.Fatalf("options = %+v", options)
	}

	h.fake.Reset()
	for _, bad := range []githubrepos.CreateRequest{
		{Owner: "octo-dev", Name: "bad name", Visibility: "public"},
		{Owner: "octo-dev", Name: "..", Visibility: "public"},
		{Owner: "octo-dev", Name: "ok", Visibility: "internal"},
		{Owner: "octo-dev", Name: "ok", Visibility: "public", Description: strings.Repeat("d", 351)},
		{Owner: "octo-dev", Name: "ok", Visibility: "public", GitignoreTemplate: "../x"},
	} {
		if _, err := h.s.Create(context.Background(), h.alice, connection.ID, bad); githubrepos.KindOf(err) != githubrepos.KindValidation {
			t.Errorf("Create(%+v) = %v", bad, err)
		}
	}
	var refused *githubrepos.NotPermittedError
	for _, owner := range []string{"readonly-org", "someone-else", "nowhere"} {
		if _, err := h.s.Create(context.Background(), h.alice, connection.ID, githubrepos.CreateRequest{Owner: owner, Name: "ok", Visibility: "public"}); !errors.As(err, &refused) {
			t.Errorf("create under %s = %v", owner, err)
		}
	}
	for _, r := range h.fake.Requests() {
		if r.Method == http.MethodPost {
			t.Fatalf("a refused create reached GitHub: %+v", r)
		}
	}

	result, err := h.s.Create(context.Background(), h.alice, connection.ID, githubrepos.CreateRequest{
		Owner: "octo-dev", Name: "new-tool", Description: "A tool", Visibility: "private", AutoInit: true, GitignoreTemplate: "Go", LicenseTemplate: "mit",
	})
	if err != nil || !result.Reachable || result.Repository.FullName != "octo-dev/new-tool" || !result.Repository.Private || result.Repository.InstallationID != 11 {
		t.Fatalf("user create = %+v %v", result, err)
	}
	var body map[string]any
	for _, r := range h.fake.Requests() {
		if r.Method == http.MethodPost && r.Path == "/user/repos" {
			_ = json.Unmarshal([]byte(r.Body), &body)
		}
	}
	if body["private"] != true || body["auto_init"] != true || body["gitignore_template"] != "Go" || body["license_template"] != "mit" || body["description"] != "A tool" {
		t.Fatalf("body = %v", body)
	}
	result, err = h.s.Create(context.Background(), h.alice, connection.ID, githubrepos.CreateRequest{Owner: "acme-org", Name: "service", Visibility: "public"})
	if err != nil || result.Reachable || result.Notice == "" || result.Repository.Owner.Type != "Organization" {
		t.Fatalf("org create into a selected installation = %+v %v", result, err)
	}
	if !h.fake.Saw(http.MethodPost, "/orgs/acme-org/repos") {
		t.Fatal("the organisation path was not used")
	}
	_, err = h.s.Create(context.Background(), h.alice, connection.ID, githubrepos.CreateRequest{Owner: "octo-dev", Name: "new-tool", Visibility: "public"})
	if githubrepos.KindOf(err) != githubrepos.KindValidation || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate = %v", err)
	}
}

func TestUpdateSendsOnlyWhatChangedInGitHubsNames(t *testing.T) {
	h := newHarness(t)
	h.createApp()
	h.standardRepos()
	connection := h.connect(h.alice, 1001)
	access, err := h.s.Access(context.Background(), h.alice, connection.ID, "octo-dev", "app")
	if err != nil {
		t.Fatal(err)
	}
	name, description, branch, wiki := "app-renamed", "New words", "main", false
	topics := []string{"tools", "go"}
	h.fake.Reset()
	detail, err := h.s.Update(context.Background(), access, githubrepos.RepositoryUpdate{Name: &name, Description: &description, DefaultBranch: &branch, HasWiki: &wiki, Topics: &topics})
	if err != nil || detail.FullName != "octo-dev/app-renamed" || strings.Join(detail.Topics, ",") != "tools,go" {
		t.Fatalf("detail = %+v %v", detail, err)
	}
	var patch map[string]any
	for _, r := range h.fake.Requests() {
		if r.Method == http.MethodPatch {
			_ = json.Unmarshal([]byte(r.Body), &patch)
		}
	}
	if len(patch) != 4 || patch["name"] != "app-renamed" || patch["description"] != "New words" || patch["default_branch"] != "main" || patch["has_wiki"] != false {
		t.Fatalf("patch = %v", patch)
	}
	if !h.fake.Saw(http.MethodPut, "/repos/octo-dev/app-renamed/topics") {
		t.Fatal("topics went to the old name")
	}
	bad := []string{"Not A Topic"}
	if _, err := h.s.Update(context.Background(), access, githubrepos.RepositoryUpdate{Topics: &bad}); githubrepos.KindOf(err) != githubrepos.KindValidation {
		t.Fatalf("bad topic = %v", err)
	}
	if _, err := h.s.Update(context.Background(), access, githubrepos.RepositoryUpdate{}); githubrepos.KindOf(err) != githubrepos.KindValidation {
		t.Fatalf("empty update = %v", err)
	}
}

func TestArchiveVisibilityAndDeleteAreRefusedBeforeGitHubWhenTheyMustBe(t *testing.T) {
	h := newHarness(t)
	h.createApp()
	h.standardRepos()
	connection := h.connect(h.alice, 1001)
	api, err := h.s.Access(context.Background(), h.alice, connection.ID, "acme-org", "api")
	if err != nil {
		t.Fatal(err)
	}
	h.fake.Reset()
	var refused *githubrepos.NotPermittedError
	if _, err := h.s.SetArchived(context.Background(), api, true); !errors.As(err, &refused) {
		t.Fatalf("archive as reader = %v", err)
	}
	if _, err := h.s.SetVisibility(context.Background(), api, "public"); !errors.As(err, &refused) {
		t.Fatalf("visibility as reader = %v", err)
	}
	if err := h.s.Delete(context.Background(), api); !errors.As(err, &refused) {
		t.Fatalf("delete as reader = %v", err)
	}
	if n := len(h.fake.Requests()); n != 0 {
		t.Fatalf("refusals reached GitHub %d times", n)
	}

	app, _ := h.s.Access(context.Background(), h.alice, connection.ID, "octo-dev", "app")
	detail, err := h.s.SetArchived(context.Background(), app, true)
	if err != nil || !detail.Archived {
		t.Fatalf("archive = %+v %v", detail, err)
	}
	app, _ = h.s.Access(context.Background(), h.alice, connection.ID, "octo-dev", "app")
	if !app.Repository.Archived || app.Repository.Can.Push {
		t.Fatalf("the archive did not reach the cached access: %+v", app.Repository)
	}
	description := "x"
	if _, err := h.s.Update(context.Background(), app, githubrepos.RepositoryUpdate{Description: &description}); !errors.As(err, &refused) {
		t.Fatalf("update while archived = %v", err)
	}
	if _, err := h.s.SetVisibility(context.Background(), app, "public"); !errors.As(err, &refused) {
		t.Fatalf("visibility while archived = %v", err)
	}
	if detail, err := h.s.SetArchived(context.Background(), app, false); err != nil || detail.Archived {
		t.Fatalf("unarchive = %+v %v", detail, err)
	}
	app, _ = h.s.Access(context.Background(), h.alice, connection.ID, "octo-dev", "app")
	if _, err := h.s.SetVisibility(context.Background(), app, "internal"); githubrepos.KindOf(err) != githubrepos.KindValidation {
		t.Fatalf("internal visibility = %v", err)
	}
	if detail, err := h.s.SetVisibility(context.Background(), app, "public"); err != nil || detail.Visibility != "public" {
		t.Fatalf("visibility = %+v %v", detail, err)
	}
	if err := h.s.Delete(context.Background(), app); err != nil || !h.fake.Saw(http.MethodDelete, "/repos/octo-dev/app") {
		t.Fatalf("delete = %v", err)
	}
}

func TestDisconnectRevokesTheGrantOnlyWhenNobodyElseUsesIt(t *testing.T) {
	h := newHarness(t)
	h.createApp()
	h.standardRepos()
	alice := h.connect(h.alice, 1001)
	bob := h.connect(h.bob, 1001)
	h.fake.Reset()
	if _, err := h.s.Disconnect(context.Background(), h.alice, alice.ID); err != nil {
		t.Fatal(err)
	}
	if !h.fake.Saw(http.MethodDelete, "/applications/"+h.fake.ClientID+"/token") || h.fake.Saw(http.MethodDelete, "/applications/"+h.fake.ClientID+"/grant") {
		t.Fatalf("shared account revocation = %+v", h.fake.Requests())
	}
	// Bob's connection to the same GitHub account still works.
	if _, err := h.s.Repositories(context.Background(), h.bob, bob.ID, true); err != nil {
		t.Fatalf("bob after alice disconnected = %v", err)
	}
	h.fake.Reset()
	if _, err := h.s.Disconnect(context.Background(), h.bob, bob.ID); err != nil {
		t.Fatal(err)
	}
	if !h.fake.Saw(http.MethodDelete, "/applications/"+h.fake.ClientID+"/grant") {
		t.Fatalf("last connection revocation = %+v", h.fake.Requests())
	}
	for _, r := range h.fake.Requests() {
		if strings.HasPrefix(r.Path, "/applications/") && (strings.Contains(r.Path, "ghu_") || strings.Contains(r.Query, "ghu_")) {
			t.Fatalf("a token went into a URL: %+v", r)
		}
	}
	if list, _ := h.s.Connections(context.Background(), h.bob); len(list) != 0 {
		t.Fatalf("connections left = %+v", list)
	}
	// Removed even when GitHub cannot be reached.
	carol := h.connect(h.alice, 1002)
	h.fake.Override = func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasPrefix(r.URL.Path, "/applications/") {
			w.WriteHeader(502)
			return true
		}
		return false
	}
	if _, err := h.s.Disconnect(context.Background(), h.alice, carol.ID); err != nil {
		t.Fatalf("disconnect with GitHub down = %v", err)
	}
}

func TestForgettingTheAppRemovesEveryConnection(t *testing.T) {
	h := newHarness(t)
	h.createApp()
	h.connect(h.alice, 1001)
	h.connect(h.bob, 1002)
	if err := h.s.ForgetApp(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.Join(paths(h.fake.Requests()), " "), "/grant"); n != 2 {
		t.Fatalf("revoked %d grants", n)
	}
	for _, user := range []int64{h.alice, h.bob} {
		if list, _ := h.s.Connections(context.Background(), user); len(list) != 0 {
			t.Fatalf("left %+v", list)
		}
	}
	if setup, _ := h.s.Setup(context.Background()); setup.Configured {
		t.Fatal("the App is still configured")
	}
}

func paths(requests []githubfake.Request) []string {
	var out []string
	for _, r := range requests {
		out = append(out, r.Method+" "+r.Path)
	}
	return out
}

func TestTheCloneTokenIsDownscopedAndRevoked(t *testing.T) {
	h := newHarness(t)
	h.createApp()
	h.standardRepos()
	connection := h.connect(h.alice, 1001)
	access, _ := h.s.Access(context.Background(), h.alice, connection.ID, "octo-dev", "app")
	token, revoke, err := h.s.CloneToken(context.Background(), access)
	if err != nil || !strings.HasPrefix(token, "ghs_") {
		t.Fatalf("token = %q %v", token, err)
	}
	minted := h.fake.InstallationTokens()[token]
	encoded, _ := json.Marshal(minted)
	if string(encoded) != `{"permissions":{"contents":"read"},"repository_ids":[1]}` {
		t.Fatalf("mint body = %s", encoded)
	}
	if !h.fake.Saw(http.MethodPost, "/app/installations/11/access_tokens") {
		t.Fatal("not minted through the repository's installation")
	}
	revoke()
	if _, ok := h.fake.InstallationTokens()[token]; ok {
		t.Fatal("the token was not revoked")
	}
	site, _ := h.s.Access(context.Background(), h.alice, connection.ID, "octo-dev", "site")
	if token, _, err := h.s.CloneToken(context.Background(), site); err != nil || token != "" {
		t.Fatalf("public repository token = %q %v", token, err)
	}
	if url := h.s.CloneURL(access); url != h.fake.URL+"/octo-dev/app.git" {
		t.Fatalf("clone url = %s", url)
	}
}

func TestTheCloneEnvironmentScopesTheTokenToTheRemote(t *testing.T) {
	env := githubrepos.CloneEnvironment("https://github.com/octo-dev/app.git", "ghs_secret")
	if env["GIT_CONFIG_COUNT"] != "2" || env["GIT_CONFIG_KEY_0"] != "credential.helper" || env["GIT_CONFIG_VALUE_0"] != "" ||
		env["GIT_CONFIG_KEY_1"] != "http.https://github.com/octo-dev/app.git.extraHeader" {
		t.Fatalf("env = %v", env)
	}
	if env["GIT_CONFIG_VALUE_1"] != "Authorization: Basic eC1hY2Nlc3MtdG9rZW46Z2hzX3NlY3JldA==" {
		t.Fatalf("header = %q", env["GIT_CONFIG_VALUE_1"])
	}
	for key, value := range env {
		if strings.Contains(key, "ghs_secret") || strings.Contains(value, "ghs_secret") {
			t.Fatalf("the token is in plain text in %s", key)
		}
	}
	public := githubrepos.CloneEnvironment("https://github.com/octo-dev/site.git", "")
	if public["GIT_CONFIG_COUNT"] != "1" || len(public) != 3 {
		t.Fatalf("public env = %v", public)
	}
}
