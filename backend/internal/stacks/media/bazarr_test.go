package media

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
)

// bazarrFake stands in for Bazarr 1.6.2. Its settings, languages and status
// are trimmed copies of what the pinned image answered; saving follows the
// image's own save_settings: text is cast, a password is kept as its MD5,
// "/" is stored as "", and the enabled languages and the profiles are each
// replaced by what was posted.
type bazarrFake struct {
	t   *testing.T
	key string

	mu        sync.Mutex
	settings  map[string]map[string]any
	languages []map[string]any
	profiles  []json.RawMessage
	// radarrKey and sonarrKey are the keys those applications accept.
	radarrKey, sonarrKey string
	fail                 map[string]int
	posts                []url.Values
	calls                []string
	server               *httptest.Server
}

func newBazarrFake(t *testing.T, stack *arrFakeStack) *bazarrFake {
	t.Helper()
	fake := &bazarrFake{t: t, key: "0123456789abcdef0123456789abcdef", radarrKey: stack.radarr.key, sonarrKey: stack.sonarr.key, fail: map[string]int{}}
	if err := json.Unmarshal(arrFixture(t, "bazarr-settings.json"), &fake.settings); err != nil {
		t.Fatal(err)
	}
	fake.settings["auth"]["apikey"] = fake.key
	if err := json.Unmarshal(arrFixture(t, "bazarr-languages.json"), &fake.languages); err != nil {
		t.Fatal(err)
	}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.server.Close)
	stack.env.Services["bazarr"] = fake.server.URL
	stack.env.Files = map[string][]byte{"bazarr:" + bazarrConfigPath: arrFixture(t, "bazarr-config.yaml")}
	return fake
}

func (f *bazarrFake) posted() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.posts)
}

func (f *bazarrFake) forget() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts, f.calls = nil, nil
}

func (f *bazarrFake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := r.Method + " " + r.URL.Path
	f.calls = append(f.calls, call)
	reply := func(status int, value any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if value != nil {
			_ = json.NewEncoder(w).Encode(value)
		}
	}
	if status, found := f.fail[call]; found {
		reply(status, "Internal Server Error")
		return
	}
	_ = r.ParseForm()
	switch call {
	case "GET /api/system/ping":
		reply(http.StatusOK, map[string]string{"status": "OK"})
		return
	case "POST /api/system/account":
		auth := f.settings["auth"]
		switch {
		case auth["type"] != "form" && auth["type"] != "basic":
			reply(http.StatusInternalServerError, "Unknown authentication type define in config")
		case r.PostForm.Get("action") == "login" && r.PostForm.Get("username") == auth["username"] && bazarrPasswordHash(r.PostForm.Get("password")) == auth["password"]:
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "signed-in"})
			w.WriteHeader(http.StatusNoContent)
		default:
			reply(http.StatusForbidden, "Authentication failed")
		}
		return
	}
	if r.Header.Get("X-API-KEY") != f.key {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch call {
	case "GET /api/system/settings":
		reply(http.StatusOK, f.settings)
	case "GET /api/system/languages":
		reply(http.StatusOK, f.languages)
	case "GET /api/system/languages/profiles":
		if f.profiles == nil {
			reply(http.StatusOK, []any{})
			return
		}
		reply(http.StatusOK, f.profiles)
	case "GET /api/system/status":
		var status map[string]map[string]any
		_ = json.Unmarshal(arrFixture(f.t, "bazarr-status.json"), &status)
		status["data"]["radarr_version"] = f.sees("radarr", f.radarrKey, status["data"]["radarr_version"])
		status["data"]["sonarr_version"] = f.sees("sonarr", f.sonarrKey, status["data"]["sonarr_version"])
		reply(http.StatusOK, status)
	case "POST /api/system/settings":
		f.posts = append(f.posts, r.PostForm)
		f.save(r.PostForm)
		w.WriteHeader(http.StatusNoContent)
	default:
		f.t.Errorf("Bazarr: unexpected request %s", call)
		http.NotFound(w, r)
	}
}

// sees is the version Bazarr reports for a manager: "" when it is not
// enabled, "unknown" when it could not be reached with the settings held.
func (f *bazarrFake) sees(name, key string, version any) any {
	if f.settings["general"]["use_"+name] != true {
		return ""
	}
	if f.settings[name]["ip"] != name || f.settings[name]["apikey"] != key || f.settings[name]["ssl"] != false {
		return "unknown"
	}
	return version
}

func (f *bazarrFake) save(form url.Values) {
	if enabled := form["languages-enabled"]; len(enabled) > 0 {
		for _, language := range f.languages {
			language["enabled"] = slices.Contains(enabled, language["code2"].(string))
		}
	}
	if profiles := form.Get("languages-profiles"); profiles != "" {
		f.profiles = nil
		if err := json.Unmarshal([]byte(profiles), &f.profiles); err != nil {
			f.t.Errorf("languages-profiles was not JSON: %v", err)
		}
	}
	for name, values := range form {
		parts := strings.Split(name, "-")
		if parts[0] != "settings" || len(parts) != 3 {
			continue
		}
		var value any = values[0]
		switch text := values[0]; {
		case parts[2] == "password":
			value = bazarrPasswordHash(text)
		case parts[2] == "base_url":
			value = strings.Trim(text, "/")
		case text == "true":
			value = true
		case text == "false":
			value = false
		default:
			if number, err := strconv.Atoi(text); err == nil {
				value = number
			}
		}
		f.settings[parts[1]][parts[2]] = value
	}
}

func (f *bazarrFake) profile(t *testing.T, name string) map[string]any {
	t.Helper()
	for _, raw := range f.profiles {
		var profile map[string]any
		if err := json.Unmarshal(raw, &profile); err != nil {
			t.Fatal(err)
		}
		if profile["name"] == name {
			return profile
		}
	}
	return nil
}

func (f *bazarrFake) enabled() []string {
	var codes []string
	for _, language := range f.languages {
		if language["enabled"] == true {
			codes = append(codes, language["code2"].(string))
		}
	}
	return codes
}

func TestBazarrAuthStoresItsKeyAndSecuresOnce(t *testing.T) {
	stack := newArrFakeStack(t)
	bazarr := newBazarrFake(t, stack)
	ctx := context.Background()

	first := arrRun(t, stack.env, "bazarr.auth")
	if key, found, _ := stack.env.LookupCredential(ctx, CredentialBazarrAPIKey); !found || key != bazarr.key {
		t.Fatal("the API key from Bazarr's configuration file was not stored")
	}
	password, _, _ := stack.env.LookupCredential(ctx, CredentialBazarrAdmin)
	posts := bazarr.posted()
	if len(posts) != 1 || len(posts[0]) != 3 || posts[0].Get("settings-auth-type") != "form" || posts[0].Get("settings-auth-username") != AdminUsername || posts[0].Get("settings-auth-password") != password {
		t.Fatalf("settings posted: %d", len(posts))
	}
	if auth := bazarr.settings["auth"]; auth["type"] != "form" || auth["password"] != bazarrPasswordHash(password) || auth["apikey"] != bazarr.key {
		t.Fatalf("auth settings: %v", auth["type"])
	}
	if !slices.Contains(bazarr.calls, "POST /api/system/account") {
		t.Fatal("the sign-in was not proven")
	}
	if strings.Contains(first.Message, password) || strings.Contains(first.Message, bazarr.key) || strings.Contains(strings.Join(stack.env.Lines, "\n"), bazarr.key) {
		t.Fatal("a secret was reported")
	}

	bazarr.forget()
	second := arrRun(t, stack.env, "bazarr.auth")
	if len(bazarr.posted()) != 0 || !strings.Contains(second.Message, "already") || !slices.Contains(bazarr.calls, "POST /api/system/account") {
		t.Fatalf("second run: %q, %d writes", second.Message, len(bazarr.posted()))
	}

	// Bazarr was given a new key (its volume was replaced): the stored one
	// follows the file, and the sign-in is set again on the new instance.
	bazarr.forget()
	bazarr.key = "fedcba9876543210fedcba9876543210"
	bazarr.settings["auth"] = map[string]any{"apikey": bazarr.key, "type": nil, "username": "", "password": ""}
	stack.env.Files["bazarr:"+bazarrConfigPath] = []byte(strings.ReplaceAll(string(arrFixture(t, "bazarr-config.yaml")), "0123456789abcdef0123456789abcdef", bazarr.key))
	arrRun(t, stack.env, "bazarr.auth")
	if key, _, _ := stack.env.LookupCredential(ctx, CredentialBazarrAPIKey); key != bazarr.key || len(bazarr.posted()) != 1 {
		t.Fatal("a new key was not followed")
	}
}

func TestBazarrAuthWaitsForBazarrAndItsFile(t *testing.T) {
	stack := newArrFakeStack(t)
	bazarr := newBazarrFake(t, stack)

	delete(stack.env.Files, "bazarr:"+bazarrConfigPath)
	if err := arrFail(t, stack.env, "bazarr.auth"); stacks.IsPermanent(err) || !strings.Contains(err.Error(), "has not written its configuration file yet") {
		t.Fatalf("no file yet: %v", err)
	}
	stack.env.Files["bazarr:"+bazarrConfigPath] = []byte("auth:\n  apikey: ''\n")
	if err := arrFail(t, stack.env, "bazarr.auth"); stacks.IsPermanent(err) || !strings.Contains(err.Error(), "no API key yet") {
		t.Fatalf("no key yet: %v", err)
	}
	stack.env.Files["bazarr:"+bazarrConfigPath] = arrFixture(t, "bazarr-config.yaml")
	bazarr.fail["GET /api/system/settings"] = http.StatusInternalServerError
	if err := arrFail(t, stack.env, "bazarr.auth"); stacks.IsPermanent(err) || !strings.Contains(err.Error(), "Bazarr answered 500") {
		t.Fatalf("an upstream error: %v", err)
	}
	if len(bazarr.posted()) != 0 {
		t.Fatal("something was written")
	}
}

func bazarrLinked(t *testing.T) (*arrFakeStack, *bazarrFake) {
	t.Helper()
	stack := newArrFakeStack(t)
	bazarr := newBazarrFake(t, stack)
	arrRun(t, stack.env, "bazarr.auth")
	bazarr.forget()
	return stack, bazarr
}

func TestBazarrLinkConnectsBothManagersAndMakesTheProfileOnce(t *testing.T) {
	stack, bazarr := bazarrLinked(t)

	first := arrRun(t, stack.env, "bazarr.link")
	posts := bazarr.posted()
	if len(posts) != 1 {
		t.Fatalf("%d writes", len(posts))
	}
	for name, want := range map[string]string{
		"settings-general-use_radarr": "true", "settings-radarr-ip": "radarr", "settings-radarr-apikey": stack.radarr.key,
		"settings-general-use_sonarr": "true", "settings-sonarr-ip": "sonarr", "settings-sonarr-apikey": stack.sonarr.key,
		"settings-general-movie_default_enabled": "true", "settings-general-movie_default_profile": "1",
		"settings-general-serie_default_enabled": "true", "settings-general-serie_default_profile": "1",
	} {
		if posts[0].Get(name) != want {
			t.Errorf("%s was not sent as expected", name)
		}
	}
	// What was already right is not sent: the fresh image has each
	// manager's own port, no SSL and no base URL.
	for _, name := range []string{"settings-radarr-port", "settings-radarr-ssl", "settings-radarr-base_url", "settings-sonarr-port", "settings-sonarr-ssl", "settings-sonarr-base_url"} {
		if posts[0].Has(name) {
			t.Errorf("%s was sent though it was already so", name)
		}
	}
	profile := bazarr.profile(t, bazarrProfileName)
	items, _ := json.Marshal(profile["items"])
	if profile["profileId"] != float64(1) || string(items) != `[{"audio_exclude":"False","audio_only_include":"False","forced":"False","hi":"False","id":1,"language":"en"},{"audio_exclude":"False","audio_only_include":"False","forced":"False","hi":"False","id":2,"language":"ro"}]` {
		t.Fatalf("profile: %v %s", profile, items)
	}
	if !slices.Equal(bazarr.enabled(), []string{"en", "ro"}) {
		t.Fatalf("enabled languages: %v", bazarr.enabled())
	}
	if first.Outputs["profileId"] != "1" || first.Outputs["languages"] != "en,ro" ||
		!strings.Contains(first.Message, "English, Romanian") || !strings.Contains(first.Message, "No subtitle provider is set yet") {
		t.Fatalf("first run: %+v", first)
	}
	if strings.Contains(first.Message, stack.radarr.key) || strings.Contains(strings.Join(stack.env.Lines, "\n"), stack.sonarr.key) {
		t.Fatal("an API key was reported")
	}
	if !slices.Contains(bazarr.calls, "GET /api/system/status") {
		t.Fatal("Bazarr was not asked whether it reaches the managers")
	}

	bazarr.forget()
	second := arrRun(t, stack.env, "bazarr.link")
	if len(bazarr.posted()) != 0 || !strings.Contains(second.Message, "already") || second.Outputs["profileId"] != "1" {
		t.Fatalf("second run: %+v, %d writes", second, len(bazarr.posted()))
	}

	// The person's subtitle languages changed: the stack's profile is
	// updated in place, under the same id.
	bazarr.forget()
	stack.env.OptionsMap[OptionSubtitleLanguages] = " FR, en ,fr"
	arrRun(t, stack.env, "bazarr.link")
	posts = bazarr.posted()
	if len(posts) != 1 || posts[0].Has("settings-radarr-apikey") || posts[0].Has("settings-general-movie_default_profile") {
		t.Fatalf("a language change wrote more than languages: %d", len(posts))
	}
	profile = bazarr.profile(t, bazarrProfileName)
	if languages, _ := json.Marshal(profile["items"]); profile["profileId"] != float64(1) || !strings.Contains(string(languages), `"language":"fr"`) || strings.Contains(string(languages), `"ro"`) || len(bazarr.profiles) != 1 {
		t.Fatalf("updated profile: %s", languages)
	}
	// Romanian stays enabled: only the profile stopped asking for it.
	if !slices.Equal(bazarr.enabled(), []string{"en", "fr", "ro"}) {
		t.Fatalf("enabled languages: %v", bazarr.enabled())
	}
}

func TestBazarrLinkKeepsWhatThePersonMade(t *testing.T) {
	stack, bazarr := bazarrLinked(t)
	theirs := `{"profileId":4,"name":"Mine","cutoff":65535,"items":[{"id":1,"language":"de","audio_exclude":"True","audio_only_include":"False","hi":"True","forced":"False"}],"mustContain":["web"],"mustNotContain":[],"originalFormat":1,"tag":"mine"}`
	bazarr.profiles = []json.RawMessage{json.RawMessage(theirs)}
	bazarr.languages[2]["enabled"] = true // German
	bazarr.settings["general"]["movie_default_enabled"] = true
	bazarr.settings["general"]["movie_default_profile"] = 4
	bazarr.settings["general"]["enabled_providers"] = []any{"opensubtitlescom"}

	result := arrRun(t, stack.env, "bazarr.link")
	if len(bazarr.profiles) != 2 || string(bazarr.profiles[0]) != theirs {
		t.Fatalf("the person's profile did not come back as it was: %s", bazarr.profiles[0])
	}
	if own := bazarr.profile(t, bazarrProfileName); own["profileId"] != float64(5) {
		t.Fatalf("the stack's profile: %v", own)
	}
	if !slices.Equal(bazarr.enabled(), []string{"en", "de", "ro"}) {
		t.Fatalf("enabled languages: %v", bazarr.enabled())
	}
	general := bazarr.settings["general"]
	if general["movie_default_profile"] != 4 || general["serie_default_profile"] != 5 || general["serie_default_enabled"] != true {
		t.Fatalf("defaults: films %v, series %v", general["movie_default_profile"], general["serie_default_profile"])
	}
	if !strings.Contains(result.Message, "new films keep the languages profile already chosen") || strings.Contains(result.Message, "No subtitle provider") {
		t.Fatalf("message: %q", result.Message)
	}

	bazarr.forget()
	arrRun(t, stack.env, "bazarr.link")
	if len(bazarr.posted()) != 0 {
		t.Fatal("the second run wrote")
	}
}

func TestBazarrLinkWithoutLanguagesOrWithOneBazarrDoesNotKnow(t *testing.T) {
	stack, bazarr := bazarrLinked(t)
	stack.env.OptionsMap[OptionSubtitleLanguages] = " "
	result := arrRun(t, stack.env, "bazarr.link")
	posts := bazarr.posted()
	if len(posts) != 1 || posts[0].Has("languages-profiles") || posts[0].Has("languages-enabled") || posts[0].Has("settings-general-movie_default_enabled") {
		t.Fatalf("languages were written with none chosen")
	}
	if len(bazarr.profiles) != 0 || len(bazarr.enabled()) != 0 || !strings.Contains(result.Message, "No subtitle language was chosen, so none is set") ||
		!strings.Contains(result.Message, "No subtitle provider is set yet") {
		t.Fatalf("result: %+v", result)
	}

	bazarr.forget()
	stack.env.OptionsMap[OptionSubtitleLanguages] = "en,xx"
	err := arrFail(t, stack.env, "bazarr.link")
	if !stacks.IsPermanent(err) || !strings.Contains(err.Error(), "the code xx") || len(bazarr.posted()) != 0 {
		t.Fatalf("an unknown language: %v, %d writes", err, len(bazarr.posted()))
	}
}

func TestBazarrLinkFailsWhenBazarrCannotReachAManager(t *testing.T) {
	stack, bazarr := bazarrLinked(t)
	bazarr.radarrKey = "radarr was given another key"
	err := arrFail(t, stack.env, "bazarr.link")
	if stacks.IsPermanent(err) || !strings.Contains(err.Error(), "cannot reach Radarr") || strings.Contains(err.Error(), "Sonarr") {
		t.Fatalf("a manager out of reach: %v", err)
	}

	bazarr.forget()
	bazarr.fail["GET /api/system/languages"] = http.StatusBadGateway
	if err := arrFail(t, stack.env, "bazarr.link"); stacks.IsPermanent(err) || !strings.Contains(err.Error(), "Bazarr answered 502") || len(bazarr.posted()) != 0 {
		t.Fatalf("an upstream error: %v", err)
	}

	other := newArrFakeStack(t)
	newBazarrFake(t, other)
	if err := arrFail(t, other.env, "bazarr.link"); !strings.Contains(err.Error(), "API key has not been stored yet") {
		t.Fatalf("no stored key: %v", err)
	}
}
