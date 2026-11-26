package media

import (
	"context"
	"crypto/pbkdf2"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
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

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks/stackstest"
)

// The fakes below answer as the pinned images did when the steps were run
// against them (jellyfin/jellyfin:12.4, ghcr.io/seerr-team/seerr:v3.5.2,
// lscr.io/linuxserver/qbittorrent:5.3.1_v2.1.3-ls490): the same statuses and
// bodies, trimmed to the fields the steps read plus a few they must ignore.
// Ids are the recorded ones; every password and key is made up here.

// fakeApp is what the three fakes share: a server, a record of the requests
// it was sent, and answers a test forces for a request.
type fakeApp struct {
	t        *testing.T
	mu       sync.Mutex
	server   *httptest.Server
	requests []string
	// forced answers "METHOD /path" with a status and body instead of the
	// fake's own, every time, until the test removes it.
	forced map[string]forcedAnswer
}

type forcedAnswer struct {
	status int
	body   string
}

func (f *fakeApp) start(handler func(http.ResponseWriter, *http.Request, []byte)) {
	f.forced = map[string]forcedAnswer{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		key := r.Method + " " + r.URL.Path
		f.requests = append(f.requests, key)
		if answer, found := f.forced[key]; found {
			w.WriteHeader(answer.status)
			_, _ = io.WriteString(w, answer.body)
			return
		}
		handler(w, r, body)
	}))
	f.t.Cleanup(f.server.Close)
}

// take returns the requests since the last take.
func (f *fakeApp) take() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	taken := f.requests
	f.requests = nil
	return taken
}

func (f *fakeApp) force(request string, status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forced[request] = forcedAnswer{status, body}
}

func answerJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func answerText(w http.ResponseWriter, status int, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, text)
}

// newFake is a Complete stack's environment over the given servers.
func newFake(preset string, services map[string]string) *stackstest.Fake {
	return &stackstest.Fake{
		PresetID:   preset,
		Services:   services,
		OptionsMap: map[string]string{OptionRequestApproval: "admin"},
		Vars:       map[string]string{VariableRadarrAPIKey: "radarr-key-for-tests", VariableSonarrAPIKey: "sonarr-key-for-tests"},
		Lengths:    stacks.CredentialLengths(Manifest()),
	}
}

func stepByID(t *testing.T, id string) stacks.Step {
	t.Helper()
	for _, step := range Manifest().Steps {
		if step.ID == id {
			return step
		}
	}
	t.Fatalf("no step %s", id)
	return stacks.Step{}
}

// runStep runs a step once, as one attempt of the runner's.
func runStep(t *testing.T, env *stackstest.Fake, id string) (stacks.Result, error) {
	t.Helper()
	return stackstest.Run(context.Background(), env, stepByID(t, id))
}

func mustRunStep(t *testing.T, env *stackstest.Fake, id string) stacks.Result {
	t.Helper()
	result, err := runStep(t, env, id)
	if err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	return result
}

// wantAction fails unless err is the action with this id, blocking.
func wantAction(t *testing.T, err error, id string) stacks.Action {
	t.Helper()
	var action *stacks.ActionError
	if !errors.As(err, &action) {
		t.Fatalf("expected the %s action, got %v", id, err)
	}
	if action.Action.ID != id || !action.Action.Blocking || action.Action.Description == "" {
		t.Fatalf("action = %+v", action.Action)
	}
	return action.Action
}

// wantNoSecret fails if text holds any of the secrets.
func wantNoSecret(t *testing.T, text string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(text, secret) {
			t.Fatalf("a secret is in %q", text)
		}
	}
}

func credential(env *stackstest.Fake, name string) string {
	value, _, _ := env.LookupCredential(context.Background(), name)
	return value
}

// ---- Jellyfin 12.4.0 ----

type fakeJellyfin struct {
	fakeApp
	wizardDone    bool
	userName      string
	userPassword  string
	administrator bool
	remoteAccess  *bool
	keys          []jellyfinKey
	sessions      map[string]bool
	folders       []jellyfinFolder
	signIns       int
	minted        int
}

func newFakeJellyfin(t *testing.T) *fakeJellyfin {
	fake := &fakeJellyfin{fakeApp: fakeApp{t: t}, userName: "root", administrator: true, sessions: map[string]bool{}}
	fake.start(fake.handle)
	return fake
}

func (f *fakeJellyfin) token(r *http.Request) string {
	_, rest, found := strings.Cut(r.Header.Get("Authorization"), `Token="`)
	if !found {
		return ""
	}
	token, _, _ := strings.Cut(rest, `"`)
	return token
}

func (f *fakeJellyfin) authorized(r *http.Request) bool {
	token := f.token(r)
	if token == "" {
		return false
	}
	return f.sessions[token] || slices.ContainsFunc(f.keys, func(key jellyfinKey) bool { return key.AccessToken == token })
}

func (f *fakeJellyfin) handle(w http.ResponseWriter, r *http.Request, body []byte) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), `MediaBrowser Client="Just Dashboard"`) {
		f.t.Errorf("%s %s without the stack's client header", r.Method, r.URL.Path)
	}
	// Until the wizard is finished its routes and the library's are open;
	// after it they need an administrator, as FirstTimeSetupOrElevated says.
	open := !f.wizardDone
	switch r.Method + " " + r.URL.Path {
	case "GET /System/Info/Public":
		answerJSON(w, 200, map[string]any{
			"LocalAddress": "http://172.27.0.2:8096", "ServerName": "8faf68313861", "Version": "12.4.0",
			"ProductName": "Jellyfin Server", "OperatingSystem": "", "Id": "2a95686363a346c6b47011ae436faed6",
			"StartupWizardCompleted": f.wizardDone,
		})
	case "GET /Startup/User":
		if !open {
			w.WriteHeader(401)
			return
		}
		answerJSON(w, 200, map[string]string{"Name": f.userName})
	case "POST /Startup/User":
		if !open {
			w.WriteHeader(401)
			return
		}
		var user struct{ Name, Password string }
		_ = json.Unmarshal(body, &user)
		f.userName, f.userPassword = user.Name, user.Password
		w.WriteHeader(204)
	case "POST /Startup/RemoteAccess":
		if !open {
			w.WriteHeader(401)
			return
		}
		var access map[string]bool
		if err := json.Unmarshal(body, &access); err != nil || len(access) != 1 {
			answerText(w, 400, "Error processing request.")
			return
		}
		enabled := access["EnableRemoteAccess"]
		f.remoteAccess = &enabled
		w.WriteHeader(204)
	case "POST /Startup/Complete":
		if !open {
			w.WriteHeader(401)
			return
		}
		f.wizardDone = true
		w.WriteHeader(204)
	case "POST /Users/AuthenticateByName":
		f.signIns++
		var attempt struct{ Username, Pw string }
		_ = json.Unmarshal(body, &attempt)
		if attempt.Username != f.userName || attempt.Pw != f.userPassword || f.userPassword == "" {
			answerText(w, 401, "Error processing request.")
			return
		}
		f.minted++
		token := fmt.Sprintf("session%030d", f.minted)
		f.sessions[token] = true
		answerJSON(w, 200, map[string]any{
			"User": map[string]any{
				"Name": f.userName, "ServerId": "2a95686363a346c6b47011ae436faed6", "Id": "e1b04d7430924858afab7bd745bff13a",
				"HasPassword": true, "Policy": map[string]any{"IsAdministrator": f.administrator, "IsHidden": true, "IsDisabled": false},
			},
			"AccessToken": token, "ServerId": "2a95686363a346c6b47011ae436faed6",
		})
	case "POST /Sessions/Logout":
		delete(f.sessions, f.token(r))
		w.WriteHeader(204)
	case "GET /Auth/Keys":
		if !f.authorized(r) {
			w.WriteHeader(401)
			return
		}
		items := []map[string]any{}
		for _, key := range f.keys {
			items = append(items, map[string]any{
				"Id": 0, "AccessToken": key.AccessToken, "DeviceId": "", "AppName": key.AppName, "AppVersion": "", "DeviceName": "",
				"UserId": "00000000000000000000000000000000", "IsActive": false, "DateCreated": "2026-11-26T03:10:44.0280959Z",
			})
		}
		answerJSON(w, 200, map[string]any{"Items": items, "TotalRecordCount": len(items), "StartIndex": 0})
	case "POST /Auth/Keys":
		if !f.authorized(r) {
			w.WriteHeader(401)
			return
		}
		f.minted++
		f.keys = append(f.keys, jellyfinKey{AccessToken: fmt.Sprintf("%032x", f.minted), AppName: r.URL.Query().Get("app")})
		w.WriteHeader(204)
	case "GET /Library/VirtualFolders":
		if !open && !f.authorized(r) {
			w.WriteHeader(401)
			return
		}
		folders := []map[string]any{}
		for _, folder := range f.folders {
			folders = append(folders, map[string]any{
				"Name": folder.Name, "Locations": folder.Locations, "CollectionType": folder.CollectionType,
				"LibraryOptions": map[string]any{"Enabled": true, "EnableRealtimeMonitor": true, "PathInfos": []map[string]string{{"Path": folder.Locations[0]}}},
				"ItemId":         folder.ItemID, "RefreshStatus": "Idle",
			})
		}
		answerJSON(w, 200, folders)
	case "POST /Library/VirtualFolders":
		if !open && !f.authorized(r) {
			w.WriteHeader(401)
			return
		}
		query := r.URL.Query()
		name, location := query.Get("name"), query.Get("paths")
		if !strings.HasPrefix(location, MediaRoot+"/") {
			// A folder that does not exist in the container.
			answerText(w, 400, "Error processing request.")
			return
		}
		// A name that is taken is not refused: the library is made under the
		// name with a 2 after it.
		for slices.ContainsFunc(f.folders, func(folder jellyfinFolder) bool { return folder.Name == name }) {
			name += "2"
		}
		ids := map[string]string{MoviesLibrary: "f137a2dd21bbc1b99aa5c0f6bf02a805", TVLibrary: "a656b907eb3a73532e40e44b968d0225"}
		f.folders = append(f.folders, jellyfinFolder{Name: name, Locations: []string{location}, CollectionType: query.Get("collectionType"), ItemID: ids[location]})
		w.WriteHeader(204)
	default:
		f.t.Errorf("Jellyfin has no %s %s", r.Method, r.URL.Path)
		w.WriteHeader(404)
	}
}

// ---- qBittorrent 5.3.1 (Web API 2.16.2) ----

type fakeQBittorrent struct {
	fakeApp
	env *stackstest.Fake
	// password is the one set, "" while only the temporary one signs in.
	password    string
	temporary   string
	username    string
	banned      bool
	refusals    int
	sessions    map[string]bool
	preferences map[string]any
	categories  map[string]string
	minted      int
}

// qbittorrentFirstLog is the end of the container's log on a first start.
const qbittorrentFirstLog = `[custom-init] No custom files found, skipping...
WebUI will be started shortly after internal preparations. Please wait...

******** Information ********
To control qBittorrent, access the WebUI at: http://localhost:8080
The WebUI administrator username is: admin
The WebUI administrator password was not set. A temporary password is provided for this session: fJYXIRjS2
You should set your own password in program preferences.
Connection to localhost (::1) 8080 port [tcp/http-alt] succeeded!
[ls.io-init] done.
`

func newFakeQBittorrent(t *testing.T) *fakeQBittorrent {
	fake := &fakeQBittorrent{
		fakeApp: fakeApp{t: t}, temporary: "fJYXIRjS2", username: "admin", sessions: map[string]bool{},
		preferences: map[string]any{
			"save_path": "/downloads", "temp_path": "/downloads/incomplete", "temp_path_enabled": false, "auto_tmm_enabled": false,
			"bypass_local_auth": false, "bypass_auth_subnet_whitelist_enabled": false, "web_ui_username": "admin",
			"web_ui_max_auth_fail_count": 5, "web_ui_ban_duration": 3600, "web_ui_port": 8080,
		},
		categories: map[string]string{},
	}
	fake.start(fake.handle)
	return fake
}

// attach gives the fake the environment whose settings file it writes, as
// qBittorrent writes its own after a change.
func (f *fakeQBittorrent) attach(env *stackstest.Fake) {
	f.env = env
	f.env.LogsFunc = func(string) (string, error) { return qbittorrentFirstLog, nil }
	f.writeConfig()
}

func (f *fakeQBittorrent) writeConfig() {
	config := "[BitTorrent]\nSession\\DefaultSavePath=/downloads/\nSession\\Port=6881\n\n[LegalNotice]\nAccepted=true\n\n[Preferences]\nConnection\\ResolvePeerCountries=true\n\n[WebUI]\nAddress=*\n"
	if f.password != "" {
		salt := []byte("0123456789abcdef")
		key, _ := pbkdf2.Key(sha512.New, f.password, salt, 100000, 64)
		config += `Password_PBKDF2="@ByteArray(` + base64.StdEncoding.EncodeToString(salt) + ":" + base64.StdEncoding.EncodeToString(key) + `)"` + "\n"
	}
	config += "ServerDomains=*\n"
	if f.username != "admin" {
		config += "Username=" + f.username + "\n"
	}
	if f.env.Files == nil {
		f.env.Files = map[string][]byte{}
	}
	f.env.Files["qbittorrent:"+qbittorrentConfigFile] = []byte(config)
}

func (f *fakeQBittorrent) signedIn(r *http.Request) bool {
	cookie, err := r.Cookie("QBT_SID_8080")
	return err == nil && f.sessions[cookie.Value]
}

func (f *fakeQBittorrent) handle(w http.ResponseWriter, r *http.Request, body []byte) {
	// The CSRF check: a Referer from anywhere but the address asked is
	// refused outright.
	if referer := r.Header.Get("Referer"); referer != "" && referer != "http://"+r.Host {
		answerText(w, 401, "Unauthorized")
		return
	}
	form, _ := url.ParseQuery(string(body))
	path := r.Method + " " + r.URL.Path
	if path == "POST /api/v2/auth/login" {
		if f.banned {
			answerText(w, 403, "Your IP address has been banned after too many failed authentication attempts.")
			return
		}
		accepted := form.Get("username") == f.username && ((f.password != "" && form.Get("password") == f.password) || (f.password == "" && form.Get("password") == f.temporary))
		if !accepted {
			f.refusals++
			answerText(w, 401, "Unauthorized")
			return
		}
		f.minted++
		session := fmt.Sprintf("sid%d", f.minted)
		f.sessions[session] = true
		http.SetCookie(w, &http.Cookie{Name: "QBT_SID_8080", Value: session, Path: "/", HttpOnly: true})
		w.WriteHeader(204)
		return
	}
	if !f.signedIn(r) {
		answerText(w, 403, "Forbidden")
		return
	}
	switch path {
	case "GET /api/v2/app/version":
		answerText(w, 200, "v5.3.1")
	case "POST /api/v2/auth/logout":
		cookie, _ := r.Cookie("QBT_SID_8080")
		delete(f.sessions, cookie.Value)
		w.WriteHeader(200)
	case "GET /api/v2/app/preferences":
		answerJSON(w, 200, f.preferences)
	case "POST /api/v2/app/setPreferences":
		var change map[string]any
		if err := json.Unmarshal([]byte(form.Get("json")), &change); err != nil {
			answerText(w, 400, "Bad Request")
			return
		}
		for key, value := range change {
			if key == "web_ui_password" {
				f.password, _ = value.(string)
				f.writeConfig()
				continue
			}
			f.preferences[key] = value
		}
		w.WriteHeader(200)
	case "GET /api/v2/torrents/categories":
		categories := map[string]any{}
		for name, savePath := range f.categories {
			categories[name] = map[string]any{
				"download_path": nil, "inactive_seeding_time_limit": -2, "name": name, "ratio_limit": -2,
				"savePath": savePath, "seeding_time_limit": -2, "share_limit_action": "Default", "share_limits_mode": "Default",
			}
		}
		answerJSON(w, 200, categories)
	case "POST /api/v2/torrents/createCategory":
		if _, exists := f.categories[form.Get("category")]; exists {
			answerText(w, 409, "Unable to create category")
			return
		}
		f.categories[form.Get("category")] = form.Get("savePath")
		w.WriteHeader(200)
	case "POST /api/v2/torrents/editCategory":
		if _, exists := f.categories[form.Get("category")]; !exists {
			answerText(w, 404, "Category does not exist")
			return
		}
		f.categories[form.Get("category")] = form.Get("savePath")
		w.WriteHeader(200)
	default:
		f.t.Errorf("qBittorrent has no %s", path)
		w.WriteHeader(404)
	}
}

// ---- Seerr 3.5.2 ----

type fakeSeerr struct {
	fakeApp
	apiKey          string
	mediaServerType int
	initialized     bool
	// owner is the Jellyfin account that is Seerr's user 1, "" before any.
	owner string
	// jellyfinPassword is what the Jellyfin behind this Seerr accepts for
	// admin. newUsersSignIn is its "new Jellyfin sign-in" setting's effect.
	jellyfinPassword string
	jellyfinDown     bool
	main             map[string]any
	libraries        []seerrLibrary
	jellyfinHas      []seerrLibrary
	managers         map[string][]map[string]any
	// managerState is what each manager would answer now; remembered is what
	// Seerr answers from memory until its cache is emptied.
	managerState map[string]*seerrManagerTest
	remembered   map[string]*seerrManagerTest
	managerKeys  map[string]string
	sessions     map[string]string
	minted       int
	lastSignIn   map[string]any
}

func newFakeSeerr(t *testing.T) *fakeSeerr {
	fake := &fakeSeerr{
		fakeApp: fakeApp{t: t}, apiKey: "seerr-api-key-for-tests", mediaServerType: seerrServerNotConfigured,
		main: map[string]any{
			"applicationTitle": "Seerr", "applicationUrl": "", "cacheImages": false, "defaultPermissions": 32,
			"localLogin": true, "mediaServerLogin": true, "newPlexLogin": true, "locale": "en", "partialRequestsEnabled": true,
		},
		jellyfinHas: []seerrLibrary{
			{ID: "f137a2dd21bbc1b99aa5c0f6bf02a805", Name: "Movies"},
			{ID: "a656b907eb3a73532e40e44b968d0225", Name: "Shows"},
		},
		managers: map[string][]map[string]any{"radarr": {}, "sonarr": {}},
		managerState: map[string]*seerrManagerTest{
			"radarr": managerAnswer("/data/media/movies", 1, "Any", 7, "HD Bluray + WEB"),
			"sonarr": managerAnswer("/data/media/tv", 1, "Any", 4, "HD-1080p", 9, "WEB-1080p"),
		},
		remembered:  map[string]*seerrManagerTest{},
		managerKeys: map[string]string{"radarr": "radarr-key-for-tests", "sonarr": "sonarr-key-for-tests"},
		sessions:    map[string]string{},
	}
	fake.start(fake.handle)
	return fake
}

// managerAnswer is a manager's test answer with one root folder and the
// profiles given as id, name pairs.
func managerAnswer(root string, profiles ...any) *seerrManagerTest {
	answer := &seerrManagerTest{}
	if root != "" {
		answer.RootFolders = append(answer.RootFolders, struct {
			Path string `json:"path"`
		}{root})
	}
	for index := 0; index+1 < len(profiles); index += 2 {
		answer.Profiles = append(answer.Profiles, struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}{profiles[index].(int), profiles[index+1].(string)})
	}
	return answer
}

func (f *fakeSeerr) user(r *http.Request) (map[string]any, int) {
	if key := r.Header.Get("X-API-Key"); key != "" {
		if key != f.apiKey || f.owner == "" {
			return nil, 403
		}
		return f.userJSON(f.owner), 0
	}
	cookie, err := r.Cookie("connect.sid")
	if err != nil {
		return nil, 401
	}
	name, found := f.sessions[cookie.Value]
	if !found {
		return nil, 403
	}
	return f.userJSON(name), 0
}

func (f *fakeSeerr) userJSON(name string) map[string]any {
	permissions, id := 32, 2
	if name == f.owner {
		permissions, id = 2, 1
	}
	return map[string]any{
		"permissions": permissions, "warnings": []string{}, "id": id, "email": name, "jellyfinUsername": name,
		"jellyfinUserId": "e1b04d7430924858afab7bd745bff13a", "userType": 3, "displayName": name,
	}
}

func (f *fakeSeerr) handle(w http.ResponseWriter, r *http.Request, body []byte) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	route := r.Method + " " + path
	var payload map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			answerJSON(w, 400, map[string]string{"message": "request body is not JSON"})
			return
		}
	}
	switch route {
	case "GET /settings/public":
		answerJSON(w, 200, map[string]any{
			"initialized": f.initialized, "applicationTitle": "Seerr", "localLogin": true, "mediaServerLogin": f.main["mediaServerLogin"],
			"mediaServerType": f.mediaServerType, "newPlexLogin": f.main["newPlexLogin"], "locale": "en",
		})
		return
	case "POST /auth/jellyfin":
		f.signIn(w, payload)
		return
	}
	user, refused := f.user(r)
	if refused == 401 {
		answerJSON(w, 401, map[string]any{"message": "cookie 'connect.sid' required"})
		return
	}
	if route == "GET /auth/me" {
		if user == nil {
			answerJSON(w, 403, map[string]any{"status": 403, "error": "You do not have permission to access this endpoint"})
			return
		}
		answerJSON(w, 200, user)
		return
	}
	if user == nil || user["permissions"].(int)&seerrPermissionAdmin == 0 {
		answerJSON(w, 403, map[string]any{"status": 403, "error": "You do not have permission to access this endpoint"})
		return
	}
	for _, kind := range []string{"radarr", "sonarr"} {
		if strings.HasPrefix(path, "/settings/"+kind) || path == "/settings/cache/"+kind+"/flush" {
			f.manager(w, r, kind, strings.TrimPrefix(path, "/settings/"+kind), payload)
			return
		}
	}
	switch {
	case route == "GET /settings/main":
		main := map[string]any{"apiKey": f.apiKey, "mediaServerType": f.mediaServerType}
		for key, value := range f.main {
			main[key] = value
		}
		answerJSON(w, 200, main)
	case route == "POST /settings/main":
		for key, value := range payload {
			f.main[key] = value
		}
		answerJSON(w, 200, f.main)
	case route == "GET /settings/jellyfin/library":
		answerJSON(w, 200, f.librariesJSON())
	case route == "POST /settings/jellyfin/library/sync":
		if f.jellyfinDown {
			answerJSON(w, 502, map[string]string{"message": "CONNECTION_ERROR"})
			return
		}
		if len(f.jellyfinHas) == 0 {
			answerJSON(w, 404, map[string]string{"message": "SYNC_ERROR_NO_LIBRARIES"})
			return
		}
		var synced []seerrLibrary
		for _, library := range f.jellyfinHas {
			for _, existing := range f.libraries {
				if existing.ID == library.ID {
					library.Enabled = existing.Enabled
				}
			}
			synced = append(synced, library)
		}
		f.libraries = synced
		answerJSON(w, 200, f.librariesJSON())
	case r.Method == "PUT" && strings.HasPrefix(path, "/settings/jellyfin/library/"):
		id := strings.TrimPrefix(path, "/settings/jellyfin/library/")
		for index := range f.libraries {
			if f.libraries[index].ID == id {
				f.libraries[index].Enabled, _ = payload["enabled"].(bool)
				answerJSON(w, 200, f.librariesJSON()[index])
				return
			}
		}
		answerJSON(w, 404, map[string]string{"message": "Library does not exist."})
	case route == "GET /settings/jellyfin/users":
		if f.jellyfinDown {
			answerJSON(w, 502, map[string]string{"message": "CONNECTION_ERROR"})
			return
		}
		answerJSON(w, 200, []map[string]string{{"username": "admin", "id": "e1b04d7430924858afab7bd745bff13a", "thumb": "/avatarproxy/e1b04d7430924858afab7bd745bff13a", "email": "admin"}})
	case route == "POST /settings/initialize":
		f.initialized = true
		answerJSON(w, 200, map[string]bool{"initialized": true})
	default:
		f.t.Errorf("Seerr has no %s", route)
		w.WriteHeader(404)
	}
}

func (f *fakeSeerr) librariesJSON() []map[string]any {
	libraries := []map[string]any{}
	for _, library := range f.libraries {
		kind := "movie"
		if library.Name == "Shows" {
			kind = "show"
		}
		libraries = append(libraries, map[string]any{"id": library.ID, "name": library.Name, "enabled": library.Enabled, "type": kind})
	}
	return libraries
}

// signIn is POST /auth/jellyfin as server/routes/auth.ts has it.
func (f *fakeSeerr) signIn(w http.ResponseWriter, payload map[string]any) {
	f.lastSignIn = payload
	_, hasHost := payload["hostname"]
	configured := f.mediaServerType != seerrServerNotConfigured
	switch {
	case configured && (f.mediaServerType != seerrServerJellyfin || f.main["mediaServerLogin"] == false):
		answerJSON(w, 500, map[string]string{"error": "Jellyfin login is disabled"})
		return
	case configured && hasHost:
		answerJSON(w, 500, map[string]string{"error": "Jellyfin hostname already configured"})
		return
	case !configured && !hasHost:
		answerJSON(w, 500, map[string]string{"error": "No hostname provided."})
		return
	case !configured && payload["hostname"] != "jellyfin":
		answerJSON(w, 404, map[string]string{"message": "INVALID_URL"})
		return
	case f.jellyfinDown:
		answerJSON(w, 404, map[string]string{"message": "INVALID_URL"})
		return
	case payload["password"] != f.jellyfinPassword || f.jellyfinPassword == "":
		answerJSON(w, 401, map[string]string{"message": "INVALID_CREDENTIALS"})
		return
	}
	name, _ := payload["username"].(string)
	if !configured {
		if payload["serverType"] != float64(seerrServerJellyfin) || payload["port"] != float64(PortJellyfin) || payload["useSsl"] != false {
			answerJSON(w, 500, map[string]string{"message": "NO_ADMIN_USER"})
			return
		}
		f.mediaServerType = seerrServerJellyfin
		f.owner = name
	} else if name != f.owner && f.main["newPlexLogin"] == false {
		answerJSON(w, 403, map[string]string{"message": "Access denied."})
		return
	}
	f.minted++
	session := fmt.Sprintf("s%%3A%d", f.minted)
	f.sessions[session] = name
	http.SetCookie(w, &http.Cookie{Name: "connect.sid", Value: session, Path: "/", HttpOnly: true})
	answerJSON(w, 200, f.userJSON(name))
}

// manager is the Radarr or Sonarr settings routes and their cache.
func (f *fakeSeerr) manager(w http.ResponseWriter, r *http.Request, kind, rest string, payload map[string]any) {
	required := []string{"name", "hostname", "port", "apiKey", "useSsl", "activeProfileId", "activeProfileName", "activeDirectory", "is4k", "isDefault"}
	if kind == "radarr" {
		required = append(required, "minimumAvailability")
	} else {
		required = append(required, "enableSeasonFolders")
	}
	save := func() bool {
		for _, field := range required {
			if _, found := payload[field]; !found {
				answerJSON(w, 400, map[string]string{"message": "request/body must have required property '" + field + "'"})
				return false
			}
		}
		// Seerr gives the id itself and refuses a body that carries one.
		if _, found := payload["id"]; found {
			answerJSON(w, 400, map[string]string{"message": "request/body/id is read-only"})
			return false
		}
		if payload["isDefault"] == true {
			for _, other := range f.managers[kind] {
				if other["is4k"] == payload["is4k"] {
					other["isDefault"] = false
				}
			}
		}
		return true
	}
	switch {
	case strings.HasSuffix(r.URL.Path, "/settings/cache/"+kind+"/flush"):
		delete(f.remembered, kind)
		w.WriteHeader(204)
	case r.Method == "GET" && rest == "":
		answerJSON(w, 200, f.managers[kind])
	case r.Method == "POST" && rest == "":
		if !save() {
			return
		}
		payload["id"] = float64(len(f.managers[kind]))
		f.managers[kind] = append(f.managers[kind], payload)
		answerJSON(w, 201, payload)
	case r.Method == "POST" && rest == "/test":
		// Tags are asked of the manager every time, so a wrong address or key
		// fails even when profiles and folders are remembered.
		if payload["hostname"] != kind || payload["apiKey"] != f.managerKeys[kind] || f.managerState[kind] == nil {
			title := strings.ToUpper(kind[:1]) + kind[1:]
			answerJSON(w, 500, map[string]string{"message": "Failed to connect to " + title})
			return
		}
		if f.remembered[kind] == nil {
			copied := *f.managerState[kind]
			f.remembered[kind] = &copied
		}
		answer := f.remembered[kind]
		profiles := []map[string]any{}
		for _, profile := range answer.Profiles {
			profiles = append(profiles, map[string]any{"id": profile.ID, "name": profile.Name, "upgradeAllowed": false, "cutoff": 20, "items": []any{}})
		}
		folders := []map[string]any{}
		for index, folder := range answer.RootFolders {
			folders = append(folders, map[string]any{"id": index + 1, "path": folder.Path})
		}
		answerJSON(w, 200, map[string]any{"profiles": profiles, "rootFolders": folders, "tags": []any{}, "urlBase": ""})
	case r.Method == "PUT":
		id, err := strconv.Atoi(strings.TrimPrefix(rest, "/"))
		if err != nil || id < 0 || id >= len(f.managers[kind]) {
			answerJSON(w, 404, map[string]string{"message": "Settings instance not found"})
			return
		}
		if !save() {
			return
		}
		payload["id"] = float64(id)
		f.managers[kind][id] = payload
		answerJSON(w, 200, payload)
	default:
		f.t.Errorf("Seerr has no %s %s", r.Method, r.URL.Path)
		w.WriteHeader(404)
	}
}
