package media

// A stand-in for one Servarr application, for the unit tests. What it
// answers with is in testdata: trimmed copies of what the pinned images
// (Radarr 7.0.3, Sonarr 4.0.20, Prowlarr 3.1.0) answered in the live run,
// with keys and password hashes replaced. It keeps state the way the
// applications do, so a second run of a step meets what the first one left.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks/stackstest"
)

func init() {
	// Nothing in a unit test is worth waiting a real interval for. The live
	// test waits on real applications, and keeps the real ones.
	if os.Getenv("JD_STACKS_LIVE") != "1" {
		arrPollInterval = time.Millisecond
		bazarrPollInterval = time.Millisecond
		recyclarrPollInterval = time.Millisecond
	}
}

func arrFixture(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return content
}

const (
	arrFakeMask         = "********"
	arrFakePasswordHash = "kept-hashed-by-the-application="
)

type arrFake struct {
	t   *testing.T
	app arrApp
	key string

	mu       sync.Mutex
	host     map[string]any
	password string
	folders  []map[string]any
	// absent are paths that do not exist in the container.
	absent map[string]bool
	// lists are the provider lists by resource; secrets what the
	// application keeps behind each "********", by resource, id and field.
	lists   map[string][]map[string]any
	secrets map[string]string
	nextID  int
	// passes decides the application's own test of a provider from its
	// settings, secrets resolved.
	passes   func(resource string, entry map[string]any, settings map[string]any) bool
	health   []byte
	profiles []map[string]any
	indexers []map[string]any
	// tags are Prowlarr's tags; refuse answers a PUT indexer/N with 400, as
	// Prowlarr does for a source whose site is down.
	tags   []map[string]any
	refuse map[int]bool
	// fail answers a request ("GET rootfolder") with a status instead.
	fail map[string]int

	// writes are the requests that change configuration; calls every
	// request made.
	writes []string
	calls  []string
	server *httptest.Server
}

func newArrFake(t *testing.T, app arrApp) *arrFake {
	t.Helper()
	fake := &arrFake{
		t: t, app: app, key: strings.Repeat(app.service[:1], 32),
		absent: map[string]bool{}, lists: map[string][]map[string]any{}, secrets: map[string]string{}, nextID: 1,
		passes: func(string, map[string]any, map[string]any) bool { return true },
		health: []byte("[]"), fail: map[string]int{}, refuse: map[int]bool{},
	}
	if err := json.Unmarshal(arrFixture(t, app.service+"-config-host.json"), &fake.host); err != nil {
		t.Fatal(err)
	}
	for id, name := range []string{"Any", "SD", "HD-720p", "HD-1080p", "Ultra-HD", "HD - 720p/1080p"} {
		fake.profiles = append(fake.profiles, map[string]any{"id": id + 1, "name": name})
	}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *arrFake) written() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.writes...)
}

func (f *arrFake) forget() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes, f.calls = nil, nil
}

func (f *arrFake) answer(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	switch typed := value.(type) {
	case nil:
	case []byte:
		_, _ = w.Write(typed)
	default:
		_ = json.NewEncoder(w).Encode(typed)
	}
}

func (f *arrFake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/login" && r.Method == http.MethodPost {
		f.calls = append(f.calls, "POST /login")
		_ = r.ParseForm()
		method, _ := f.host["authenticationMethod"].(string)
		username, _ := f.host["username"].(string)
		if method == "forms" && r.PostForm.Get("username") == username && f.password != "" && r.PostForm.Get("password") == f.password {
			http.SetCookie(w, &http.Cookie{Name: f.app.title + "Auth", Value: "session", Path: "/", HttpOnly: true})
			w.Header().Set("Location", "/")
		} else {
			w.Header().Set("Location", "/login?returnUrl=&loginFailed=true")
		}
		w.WriteHeader(http.StatusFound)
		return
	}
	path, isAPI := strings.CutPrefix(r.URL.Path, "/api/"+f.app.api+"/")
	if !isAPI {
		http.NotFound(w, r)
		return
	}
	call := r.Method + " " + path
	f.calls = append(f.calls, call)
	if r.Header.Get("X-Api-Key") != f.key {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if status, found := f.fail[call]; found {
		f.answer(w, status, map[string]string{"message": "the application is having a bad time"})
		return
	}
	var body map[string]any
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			f.answer(w, http.StatusBadRequest, map[string]string{"message": "not JSON"})
			return
		}
	}
	parts := strings.Split(path, "/")

	switch {
	case call == "GET system/status":
		f.answer(w, http.StatusOK, map[string]any{"appName": f.app.title, "instanceName": f.app.title, "version": "9.9.9.1", "authentication": f.host["authenticationMethod"]})
	case call == "GET config/host":
		f.answer(w, http.StatusOK, f.hostView())
	case call == "PUT config/host/1":
		f.writes = append(f.writes, call)
		if password, _ := body["password"].(string); password != "" && password != arrFakePasswordHash {
			if confirmation, _ := body["passwordConfirmation"].(string); confirmation != password {
				f.answer(w, http.StatusBadRequest, []map[string]string{{"propertyName": "PasswordConfirmation", "errorMessage": "Must match Password"}})
				return
			}
			f.password = password
		}
		f.host = body
		f.answer(w, http.StatusAccepted, f.hostView())
	case call == "GET rootfolder":
		f.answer(w, http.StatusOK, f.folders)
	case call == "POST rootfolder":
		target, _ := body["path"].(string)
		if f.absent[target] {
			f.answer(w, http.StatusBadRequest, arrFixture(f.t, "radarr-rootfolder-missing.json"))
			return
		}
		f.writes = append(f.writes, call)
		folder := map[string]any{"path": target, "accessible": true, "freeSpace": int64(82121367552), "unmappedFolders": []any{}, "id": len(f.folders) + 1}
		f.folders = append(f.folders, folder)
		f.answer(w, http.StatusCreated, folder)
	case call == "POST command":
		f.answer(w, http.StatusCreated, map[string]any{"id": 3, "name": body["name"], "status": "started"})
	case call == "GET command/3":
		f.answer(w, http.StatusOK, arrFixture(f.t, "radarr-command.json"))
	case call == "GET health":
		f.answer(w, http.StatusOK, f.health)
	case call == "GET qualityprofile":
		f.answer(w, http.StatusOK, f.profiles)
	case call == "GET indexer":
		if f.indexers == nil {
			f.answer(w, http.StatusOK, []byte("[]"))
			return
		}
		f.answer(w, http.StatusOK, f.indexers)
	case call == "GET tag":
		f.answer(w, http.StatusOK, append([]map[string]any{}, f.tags...))
	case call == "POST tag":
		f.writes = append(f.writes, call)
		tag := map[string]any{"id": f.nextID, "label": body["label"]}
		f.nextID++
		f.tags = append(f.tags, tag)
		f.answer(w, http.StatusCreated, tag)
	case r.Method == http.MethodPut && parts[0] == "indexer" && len(parts) == 2:
		id, _ := strconv.Atoi(parts[1])
		if r.URL.Query().Get("forceSave") != "true" {
			f.t.Errorf("a source was saved without forceSave: %s", r.URL.RawQuery)
		}
		if f.refuse[id] {
			f.answer(w, http.StatusBadRequest, []map[string]any{{"isWarning": false, "propertyName": "", "errorMessage": "Unable to connect to indexer", "severity": "error"}})
			return
		}
		for index, indexer := range f.indexers {
			if arrFakeID(indexer) == id {
				f.writes = append(f.writes, call)
				body["id"] = id
				f.indexers[index] = body
				f.answer(w, http.StatusAccepted, body)
				return
			}
		}
		f.answer(w, http.StatusNotFound, map[string]string{"message": "NotFound"})
	case parts[0] == "downloadclient" || parts[0] == "applications" || parts[0] == "indexerproxy":
		f.provider(w, r.Method, parts, body)
	default:
		f.t.Errorf("%s: unexpected request %s", f.app.title, call)
		http.NotFound(w, r)
	}
}

func (f *arrFake) hostView() map[string]any {
	view := map[string]any{}
	for name, value := range f.host {
		view[name] = value
	}
	view["apiKey"] = f.key
	view["passwordConfirmation"] = ""
	view["password"] = ""
	if f.password != "" {
		view["password"] = arrFakePasswordHash
	}
	return view
}

func (f *arrFake) provider(w http.ResponseWriter, method string, parts []string, body map[string]any) {
	resource := parts[0]
	call := method + " " + strings.Join(parts, "/")
	find := func(id int) int {
		for index, entry := range f.lists[resource] {
			if entry["id"] == id {
				return index
			}
		}
		return -1
	}
	switch {
	case method == http.MethodGet && len(parts) == 1:
		views := []map[string]any{}
		for _, entry := range f.lists[resource] {
			views = append(views, f.masked(resource, entry))
		}
		f.answer(w, http.StatusOK, views)
	case method == http.MethodGet && parts[1] == "schema":
		f.answer(w, http.StatusOK, arrFixture(f.t, f.app.service+"-"+resource+"-schema.json"))
	case method == http.MethodGet:
		id, _ := strconv.Atoi(parts[1])
		if index := find(id); index >= 0 {
			f.answer(w, http.StatusOK, f.masked(resource, f.lists[resource][index]))
			return
		}
		f.answer(w, http.StatusNotFound, map[string]string{"message": "NotFound"})
	case method == http.MethodPost && len(parts) == 2 && parts[1] == "test":
		id := arrFakeID(body)
		if !f.passes(resource, body, f.settings(resource, id, body)) {
			f.answer(w, http.StatusBadRequest, arrFixture(f.t, map[string]string{
				"downloadclient": "radarr-downloadclient-test-failed.json", "applications": "prowlarr-applications-test-failed.json",
				"indexerproxy": "prowlarr-indexerproxy-test-failed.json",
			}[resource]))
			return
		}
		f.answer(w, http.StatusOK, map[string]any{})
	case method == http.MethodPost && len(parts) == 1:
		for _, entry := range f.lists[resource] {
			if entry["name"] == body["name"] {
				f.answer(w, http.StatusBadRequest, []map[string]string{{"propertyName": "Name", "errorMessage": "Should be unique"}})
				return
			}
		}
		f.writes = append(f.writes, call)
		body["id"] = f.nextID
		f.nextID++
		f.keep(resource, body)
		f.lists[resource] = append(f.lists[resource], body)
		f.answer(w, http.StatusCreated, f.masked(resource, body))
	case method == http.MethodDelete && len(parts) == 2:
		id, _ := strconv.Atoi(parts[1])
		index := find(id)
		if index < 0 {
			f.answer(w, http.StatusNotFound, map[string]string{"message": "NotFound"})
			return
		}
		f.writes = append(f.writes, call)
		f.lists[resource] = append(f.lists[resource][:index:index], f.lists[resource][index+1:]...)
		f.answer(w, http.StatusOK, nil)
	case method == http.MethodPut && len(parts) == 2:
		id, _ := strconv.Atoi(parts[1])
		index := find(id)
		if index < 0 {
			f.answer(w, http.StatusNotFound, map[string]string{"message": "NotFound"})
			return
		}
		f.writes = append(f.writes, call)
		body["id"] = id
		f.keep(resource, body)
		f.lists[resource][index] = body
		f.answer(w, http.StatusAccepted, f.masked(resource, body))
	default:
		f.t.Errorf("%s: unexpected request %s", f.app.title, call)
		w.WriteHeader(http.StatusNotFound)
	}
}

func arrFakeID(entry map[string]any) int {
	switch id := entry["id"].(type) {
	case float64:
		return int(id)
	case int:
		return id
	}
	return 0
}

func arrFakeSecret(field map[string]any) bool {
	privacy, _ := field["privacy"].(string)
	return privacy == "password" || privacy == "apiKey"
}

// keep moves an entry's secrets out of it, as the application does: what is
// listed afterwards is "********". A mask sent back keeps the stored value.
func (f *arrFake) keep(resource string, entry map[string]any) {
	fields, _ := entry["fields"].([]any)
	for _, raw := range fields {
		field, _ := raw.(map[string]any)
		if !arrFakeSecret(field) {
			continue
		}
		slot := fmt.Sprintf("%s/%d/%v", resource, arrFakeID(entry), field["name"])
		if value, _ := field["value"].(string); value != "" && value != arrFakeMask {
			f.secrets[slot] = value
		}
		delete(field, "value")
	}
}

func (f *arrFake) masked(resource string, entry map[string]any) map[string]any {
	encoded, _ := json.Marshal(entry)
	var view map[string]any
	_ = json.Unmarshal(encoded, &view)
	fields, _ := view["fields"].([]any)
	for _, raw := range fields {
		field, _ := raw.(map[string]any)
		if arrFakeSecret(field) && f.secrets[fmt.Sprintf("%s/%d/%v", resource, arrFakeID(entry), field["name"])] != "" {
			field["value"] = arrFakeMask
		}
	}
	return view
}

// settings are an entry's settings by name as a test of it sees them: a
// mask replaced by what the application has stored for that entry.
func (f *arrFake) settings(resource string, id int, entry map[string]any) map[string]any {
	settings := map[string]any{}
	fields, _ := entry["fields"].([]any)
	for _, raw := range fields {
		field, _ := raw.(map[string]any)
		name, _ := field["name"].(string)
		value := field["value"]
		if text, _ := value.(string); text == arrFakeMask {
			value = f.secrets[fmt.Sprintf("%s/%d/%s", resource, id, name)]
		}
		settings[name] = value
	}
	return settings
}

// arrFakeStack is the three applications and an environment that reaches them.
type arrFakeStack struct {
	radarr, sonarr, prowlarr *arrFake
	env                      *stackstest.Fake
}

func newArrFakeStack(t *testing.T) *arrFakeStack {
	t.Helper()
	stack := &arrFakeStack{radarr: newArrFake(t, arrRadarr), sonarr: newArrFake(t, arrSonarr), prowlarr: newArrFake(t, arrProwlarr)}
	stack.env = &stackstest.Fake{
		PresetID: "complete",
		Services: map[string]string{
			"radarr": stack.radarr.server.URL, "sonarr": stack.sonarr.server.URL, "prowlarr": stack.prowlarr.server.URL,
			"qbittorrent": "http://qbittorrent.invalid", "recyclarr": "",
		},
		OptionsMap: map[string]string{OptionQuality: "1080p", OptionSubtitleLanguages: "en,ro"},
		Vars: map[string]string{
			VariableRadarrAPIKey: stack.radarr.key, VariableSonarrAPIKey: stack.sonarr.key, VariableProwlarrAPIKey: stack.prowlarr.key,
		},
	}
	return stack
}

func (s *arrFakeStack) apps() []*arrFake { return []*arrFake{s.radarr, s.sonarr, s.prowlarr} }

func (s *arrFakeStack) written() []string {
	var writes []string
	for _, app := range s.apps() {
		for _, write := range app.written() {
			writes = append(writes, app.app.service+": "+write)
		}
	}
	return writes
}

func (s *arrFakeStack) forget() {
	for _, app := range s.apps() {
		app.forget()
	}
}
