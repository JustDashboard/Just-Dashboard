package media

// Bazarr fetches subtitles for what Radarr and Sonarr hold. It generates its
// own API key on first start and keeps it in its configuration file; every
// setting, its sign-in included, is read and written through one resource,
// GET and POST /api/system/settings, which is what its own settings pages
// use. The requests here were read from the pinned version's source
// (bazarr/api/system/settings.py, account.py, languages.py,
// languages_profiles.py, status.py, ping.py and app/config.py's
// save_settings, at tag v1.6.2) and run against the pinned image.

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
)

const (
	// bazarrConfigPath is where the image keeps Bazarr's settings, API key
	// included, once it has started for the first time.
	bazarrConfigPath = "/config/config/config.yaml"
	// bazarrProfileName is the stack's own languages profile.
	bazarrProfileName = "Just Dashboard"
	bazarrReplyLimit  = 8 << 20
)

// bazarrPollInterval is how often Bazarr is asked whether it reached Radarr
// and Sonarr. A variable so tests need not wait.
var bazarrPollInterval = 2 * time.Second

type bazarrClient struct {
	env  stacks.Env
	base string
	key  string
}

func newBazarrClient(ctx context.Context, env stacks.Env, key string) (*bazarrClient, error) {
	base, err := env.BaseURL(ctx, "bazarr", PortBazarr)
	if err != nil {
		return nil, err
	}
	return &bazarrClient{env: env, base: base, key: key}, nil
}

// send makes one request with the API key in its header. A form, when
// given, is posted as Bazarr's own pages post it.
func (c *bazarrClient) send(ctx context.Context, method, path string, form url.Values) (int, []byte, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, stacks.JoinURL(c.base, path, nil), body)
	if err != nil {
		return 0, nil, err
	}
	if c.key != "" {
		request.Header.Set("X-API-KEY", c.key)
	}
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response, err := c.env.HTTP().Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("Bazarr did not answer: %w", err)
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(response.Body, bazarrReplyLimit))
	if err != nil {
		return 0, nil, fmt.Errorf("Bazarr's answer could not be read: %w", err)
	}
	return response.StatusCode, answer, nil
}

func (c *bazarrClient) get(ctx context.Context, path string, out any) error {
	status, answer, err := c.send(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if status == http.StatusUnauthorized {
		return errors.New("Bazarr does not accept the API key read from its configuration file")
	}
	if status != http.StatusOK {
		return fmt.Errorf("Bazarr answered %d to GET %s", status, path)
	}
	if err := json.Unmarshal(answer, out); err != nil {
		return fmt.Errorf("Bazarr's answer to GET %s was not what its API returns: %w", path, err)
	}
	return nil
}

// bazarrSettings is GET /api/system/settings: sections of named values.
type bazarrSettings map[string]map[string]any

func (c *bazarrClient) settings(ctx context.Context) (bazarrSettings, error) {
	var settings bazarrSettings
	if err := c.get(ctx, "/api/system/settings", &settings); err != nil {
		return nil, err
	}
	return settings, nil
}

// saveSettings posts changed settings. Bazarr answers 204, or 406 with the
// reason when its own validation refuses a value.
func (c *bazarrClient) saveSettings(ctx context.Context, form url.Values) error {
	status, answer, err := c.send(ctx, http.MethodPost, "/api/system/settings", form)
	if err != nil {
		return err
	}
	switch status {
	case http.StatusNoContent, http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return errors.New("Bazarr does not accept the API key read from its configuration file")
	case http.StatusNotAcceptable:
		return stacks.Permanent(fmt.Errorf("Bazarr refused the settings: %s", bazarrExcerpt(answer)))
	}
	return fmt.Errorf("Bazarr answered %d to POST /api/system/settings", status)
}

func bazarrExcerpt(answer []byte) string {
	text := strings.Join(strings.Fields(string(answer)), " ")
	if len(text) > 300 {
		text = text[:300] + "…"
	}
	return text
}

// signIn proves a user name and password through the request Bazarr's own
// sign-in page makes: 204 with a session, or 403.
func (c *bazarrClient) signIn(ctx context.Context, username, password string) (bool, error) {
	form := url.Values{"action": {"login"}, "username": {username}, "password": {password}}
	anonymous := &bazarrClient{env: c.env, base: c.base}
	status, _, err := anonymous.send(ctx, http.MethodPost, "/api/system/account", form)
	if err != nil {
		return false, err
	}
	switch status {
	case http.StatusNoContent:
		return true, nil
	case http.StatusForbidden:
		return false, nil
	}
	return false, fmt.Errorf("Bazarr answered %d to its sign-in", status)
}

// bazarrPasswordHash is how Bazarr 1.6 keeps the password in its settings
// (save_settings hashes the value it is given the same way), so the stack
// can tell whether the one stored is its own without writing.
func bazarrPasswordHash(password string) string {
	sum := md5.Sum([]byte(password))
	return hex.EncodeToString(sum[:])
}

// bazarrAuth reads the API key Bazarr generated for itself, keeps it as one
// of the stack's credentials, and gives Bazarr a sign-in form with the
// stack's own administrator.
func bazarrAuth(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	client, err := newBazarrClient(ctx, env, "")
	if err != nil {
		return stacks.Result{}, err
	}
	if status, _, err := client.send(ctx, http.MethodGet, "/api/system/ping", nil); err != nil {
		return stacks.Result{}, err
	} else if status != http.StatusOK {
		return stacks.Result{}, fmt.Errorf("Bazarr is not ready: it answered %d to its ping", status)
	}

	content, err := env.ReadFile(ctx, "bazarr", bazarrConfigPath)
	if err != nil {
		return stacks.Result{}, fmt.Errorf("Bazarr has not written its configuration file yet: %w", err)
	}
	var config struct {
		Auth struct {
			APIKey string `yaml:"apikey"`
		} `yaml:"auth"`
	}
	if err := yaml.Unmarshal(content, &config); err != nil {
		return stacks.Result{}, errors.New("Bazarr's configuration file could not be read as YAML")
	}
	client.key = strings.TrimSpace(config.Auth.APIKey)
	if client.key == "" {
		return stacks.Result{}, errors.New("Bazarr's configuration file holds no API key yet")
	}
	if stored, found, err := env.LookupCredential(ctx, CredentialBazarrAPIKey); err != nil {
		return stacks.Result{}, err
	} else if !found || stored != client.key {
		if err := env.SetCredential(ctx, CredentialBazarrAPIKey, client.key); err != nil {
			return stacks.Result{}, err
		}
		env.Logf("Bazarr's API key was read from its configuration file and stored")
	}

	password, err := env.Credential(ctx, CredentialBazarrAdmin)
	if err != nil {
		return stacks.Result{}, err
	}
	settings, err := client.settings(ctx)
	if err != nil {
		return stacks.Result{}, err
	}
	if bazarrSecured(settings, password) {
		signedIn, err := client.signIn(ctx, AdminUsername, password)
		if err != nil {
			return stacks.Result{}, err
		}
		if signedIn {
			return stacks.Result{Message: "Bazarr already asks for a sign-in, as " + AdminUsername}, nil
		}
	}

	if err := client.saveSettings(ctx, url.Values{
		"settings-auth-type":     {"form"},
		"settings-auth-username": {AdminUsername},
		"settings-auth-password": {password},
	}); err != nil {
		return stacks.Result{}, err
	}
	if settings, err = client.settings(ctx); err != nil {
		return stacks.Result{}, err
	}
	if !bazarrSecured(settings, password) {
		return stacks.Result{}, errors.New("Bazarr took the sign-in settings but does not report them back")
	}
	signedIn, err := client.signIn(ctx, AdminUsername, password)
	if err != nil {
		return stacks.Result{}, err
	}
	if !signedIn {
		return stacks.Result{}, fmt.Errorf("Bazarr does not let %s in with the password it was just given", AdminUsername)
	}
	env.Logf("Bazarr now asks for a sign-in")
	return stacks.Result{Message: "Bazarr asks for a sign-in, as " + AdminUsername}, nil
}

func bazarrSecured(settings bazarrSettings, password string) bool {
	auth := settings["auth"]
	kind, _ := auth["type"].(string)
	username, _ := auth["username"].(string)
	stored, _ := auth["password"].(string)
	return kind == "form" && username == AdminUsername && stored == bazarrPasswordHash(password)
}

// bazarrLanguage is one entry of GET /api/system/languages.
type bazarrLanguage struct {
	Name    string `json:"name"`
	Code2   string `json:"code2"`
	Enabled bool   `json:"enabled"`
}

// bazarrProfile is the part of a languages profile the stack reads; the
// whole of each is kept beside it, since saving profiles replaces the list.
type bazarrProfile struct {
	ProfileID int    `json:"profileId"`
	Name      string `json:"name"`
	Items     []struct {
		Language string `json:"language"`
	} `json:"items"`
}

// bazarrState is everything the link step compares with what it wants.
type bazarrState struct {
	settings  bazarrSettings
	languages []bazarrLanguage
	profiles  []bazarrProfile
	// rawProfiles are the same profiles as Bazarr returned them.
	rawProfiles []json.RawMessage
}

func (c *bazarrClient) state(ctx context.Context) (bazarrState, error) {
	var state bazarrState
	var err error
	if state.settings, err = c.settings(ctx); err != nil {
		return state, err
	}
	if err := c.get(ctx, "/api/system/languages", &state.languages); err != nil {
		return state, err
	}
	if err := c.get(ctx, "/api/system/languages/profiles", &state.rawProfiles); err != nil {
		return state, err
	}
	for _, raw := range state.rawProfiles {
		var profile bazarrProfile
		if err := json.Unmarshal(raw, &profile); err != nil {
			return state, fmt.Errorf("a Bazarr languages profile was not what its API returns: %w", err)
		}
		state.profiles = append(state.profiles, profile)
	}
	return state, nil
}

// bazarrWant is what the link step wants Bazarr to hold.
type bazarrWant struct {
	radarrKey string
	sonarrKey string
	// languages are two-letter codes in the person's order; empty leaves
	// languages alone.
	languages []string
}

// bazarrPlan is the form that would bring Bazarr from state to want: empty
// when nothing differs. notes are things it deliberately leaves as they are.
// The same function reads a write back: a plan that is still not empty
// afterwards names what Bazarr did not keep.
func bazarrPlan(state bazarrState, want bazarrWant) (form url.Values, notes []string, err error) {
	form = url.Values{}
	general := state.settings["general"]
	setting := func(section, name string, value any, sent string) {
		current := state.settings[section][name]
		if name == "base_url" {
			// Bazarr stores "/" as "", and reads either as no prefix.
			text, _ := current.(string)
			if strings.Trim(text, "/") == "" {
				return
			}
		} else if arrSameValue(current, value) {
			return
		}
		form.Set("settings-"+section+"-"+name, sent)
	}
	for _, manager := range []struct {
		name string
		port int
		key  string
	}{{"radarr", PortRadarr, want.radarrKey}, {"sonarr", PortSonarr, want.sonarrKey}} {
		setting("general", "use_"+manager.name, true, "true")
		// Compose's name for the service, which Bazarr's container resolves.
		setting(manager.name, "ip", manager.name, manager.name)
		setting(manager.name, "port", manager.port, strconv.Itoa(manager.port))
		setting(manager.name, "base_url", "", "/")
		setting(manager.name, "ssl", false, "false")
		setting(manager.name, "apikey", manager.key, manager.key)
	}
	if len(want.languages) == 0 {
		return form, notes, nil
	}

	known := map[string]bazarrLanguage{}
	var enabled []string
	for _, language := range state.languages {
		known[language.Code2] = language
		if language.Enabled {
			enabled = append(enabled, language.Code2)
		}
	}
	var unknown []string
	missing := false
	for _, code := range want.languages {
		language, found := known[code]
		if !found {
			unknown = append(unknown, code)
		} else if !language.Enabled {
			missing = true
			enabled = append(enabled, code)
		}
	}
	if len(unknown) > 0 {
		return nil, nil, stacks.Permanent(fmt.Errorf("Bazarr has no subtitle language with the code %s; change the stack's subtitle languages and run this again", strings.Join(unknown, ", ")))
	}
	if missing {
		// Saving the enabled languages replaces the set, so the ones the
		// person enabled go back with the stack's.
		for _, code := range enabled {
			form.Add("languages-enabled", code)
		}
	}

	ownID, highest, ownIndex := 0, 0, -1
	for index, profile := range state.profiles {
		highest = max(highest, profile.ProfileID)
		if profile.Name == bazarrProfileName && ownIndex < 0 {
			ownID, ownIndex = profile.ProfileID, index
		}
	}
	profiles := slices.Clone(state.rawProfiles)
	switch {
	case ownIndex < 0:
		ownID = highest + 1
		created, err := json.Marshal(map[string]any{
			"profileId": ownID, "name": bazarrProfileName, "cutoff": nil, "items": bazarrProfileItems(want.languages),
			"mustContain": []string{}, "mustNotContain": []string{}, "originalFormat": nil, "tag": nil,
		})
		if err != nil {
			return nil, nil, err
		}
		profiles = append(profiles, created)
	case !slices.Equal(bazarrProfileLanguages(state.profiles[ownIndex]), want.languages):
		// The profile keeps everything else the person set on it.
		var own map[string]any
		if err := json.Unmarshal(state.rawProfiles[ownIndex], &own); err != nil {
			return nil, nil, err
		}
		own["items"] = bazarrProfileItems(want.languages)
		own["cutoff"] = nil
		updated, err := json.Marshal(own)
		if err != nil {
			return nil, nil, err
		}
		profiles[ownIndex] = updated
	default:
		profiles = nil
	}
	if profiles != nil {
		// Saving profiles replaces the list too: every one the person made
		// goes back exactly as Bazarr returned it.
		encoded, err := json.Marshal(profiles)
		if err != nil {
			return nil, nil, err
		}
		form.Set("languages-profiles", string(encoded))
	}

	for _, kind := range []struct{ key, what string }{{"movie", "films"}, {"serie", "series"}} {
		isEnabled, _ := general[kind.key+"_default_enabled"].(bool)
		current := bazarrProfileID(general[kind.key+"_default_profile"])
		if isEnabled && current == ownID {
			continue
		}
		if isEnabled && current != 0 && slices.ContainsFunc(state.profiles, func(profile bazarrProfile) bool { return profile.ProfileID == current }) {
			// A default the person chose themselves stays.
			notes = append(notes, fmt.Sprintf("new %s keep the languages profile already chosen as their default in Bazarr", kind.what))
			continue
		}
		form.Set("settings-general-"+kind.key+"_default_enabled", "true")
		form.Set("settings-general-"+kind.key+"_default_profile", strconv.Itoa(ownID))
	}
	return form, notes, nil
}

func bazarrProfileItems(languages []string) []map[string]any {
	items := make([]map[string]any, 0, len(languages))
	for index, code := range languages {
		// Bazarr keeps these flags as the strings "True" and "False".
		items = append(items, map[string]any{
			"id": index + 1, "language": code, "audio_exclude": "False", "audio_only_include": "False", "hi": "False", "forced": "False",
		})
	}
	return items
}

func bazarrProfileLanguages(profile bazarrProfile) []string {
	languages := make([]string, 0, len(profile.Items))
	for _, item := range profile.Items {
		languages = append(languages, item.Language)
	}
	return languages
}

// bazarrProfileID reads a default profile setting, which Bazarr holds as a
// number once set and as "" before.
func bazarrProfileID(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case string:
		id, _ := strconv.Atoi(typed)
		return id
	}
	return 0
}

// bazarrSubtitleLanguages reads the option: two-letter codes, in order,
// without repeats.
func bazarrSubtitleLanguages(option string) []string {
	var languages []string
	for _, part := range strings.Split(option, ",") {
		code := strings.ToLower(strings.TrimSpace(part))
		if code != "" && !slices.Contains(languages, code) {
			languages = append(languages, code)
		}
	}
	return languages
}

// bazarrLink turns Radarr and Sonarr on in Bazarr with their API keys, and
// makes a languages profile from the stack's subtitle languages that new
// films and series get. It adds no subtitle provider: those need an account
// of the person's own.
func bazarrLink(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	key, found, err := env.LookupCredential(ctx, CredentialBazarrAPIKey)
	if err != nil {
		return stacks.Result{}, err
	}
	if !found || key == "" {
		return stacks.Result{}, errors.New("Bazarr's API key has not been stored yet")
	}
	client, err := newBazarrClient(ctx, env, key)
	if err != nil {
		return stacks.Result{}, err
	}
	want := bazarrWant{languages: bazarrSubtitleLanguages(env.Option(OptionSubtitleLanguages))}
	if want.radarrKey, err = env.Variable(ctx, VariableRadarrAPIKey); err != nil {
		return stacks.Result{}, err
	}
	if want.sonarrKey, err = env.Variable(ctx, VariableSonarrAPIKey); err != nil {
		return stacks.Result{}, err
	}

	state, err := client.state(ctx)
	if err != nil {
		return stacks.Result{}, err
	}
	form, notes, err := bazarrPlan(state, want)
	if err != nil {
		return stacks.Result{}, err
	}
	changed := len(form) > 0
	if changed {
		if err := client.saveSettings(ctx, form); err != nil {
			return stacks.Result{}, err
		}
		if state, err = client.state(ctx); err != nil {
			return stacks.Result{}, err
		}
		left, _, err := bazarrPlan(state, want)
		if err != nil {
			return stacks.Result{}, err
		}
		if len(left) > 0 {
			names := make([]string, 0, len(left))
			for name := range left {
				names = append(names, name)
			}
			sort.Strings(names)
			return stacks.Result{}, fmt.Errorf("Bazarr took the settings but reads these back differently: %s", strings.Join(names, ", "))
		}
		env.Logf("Bazarr's connection to Radarr and Sonarr was saved")
	}
	if err := client.reachesManagers(ctx); err != nil {
		return stacks.Result{}, err
	}

	verb := "already follows"
	if changed {
		verb = "follows"
	}
	message := "Bazarr " + verb + " Radarr and Sonarr"
	outputs := map[string]string{"languages": strings.Join(want.languages, ",")}
	if len(want.languages) == 0 {
		message += ". No subtitle language was chosen, so none is set"
	} else {
		names := make([]string, 0, len(want.languages))
		for _, code := range want.languages {
			for _, language := range state.languages {
				if language.Code2 == code {
					names = append(names, language.Name)
				}
			}
		}
		message += fmt.Sprintf(", and its %q profile asks for subtitles in %s", bazarrProfileName, strings.Join(names, ", "))
		for _, profile := range state.profiles {
			if profile.Name == bazarrProfileName {
				outputs["profileId"] = strconv.Itoa(profile.ProfileID)
				break
			}
		}
	}
	for _, note := range notes {
		message += "; " + note
	}
	if providers, _ := state.settings["general"]["enabled_providers"].([]any); len(providers) == 0 {
		message += ". No subtitle provider is set yet: add one in Bazarr with an account of your own before it can fetch anything"
	}
	return stacks.Result{Message: message, Outputs: outputs}, nil
}

// reachesManagers has Bazarr say which Radarr and Sonarr versions it sees:
// GET /api/system/status calls both with the keys it holds, and answers ""
// or "unknown" for one it could not reach. It caches a version for a
// minute, so a change is waited for briefly.
func (c *bazarrClient) reachesManagers(ctx context.Context) error {
	var status struct {
		Data struct {
			Radarr string `json:"radarr_version"`
			Sonarr string `json:"sonarr_version"`
		} `json:"data"`
	}
	var unreached []string
	for attempt := 0; attempt < 10; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(bazarrPollInterval):
			}
		}
		if err := c.get(ctx, "/api/system/status", &status); err != nil {
			return err
		}
		unreached = unreached[:0]
		if status.Data.Radarr == "" || status.Data.Radarr == "unknown" {
			unreached = append(unreached, "Radarr")
		}
		if status.Data.Sonarr == "" || status.Data.Sonarr == "unknown" {
			unreached = append(unreached, "Sonarr")
		}
		if len(unreached) == 0 {
			return nil
		}
	}
	return fmt.Errorf("Bazarr holds the settings but cannot reach %s with them", strings.Join(unreached, " or "))
}
