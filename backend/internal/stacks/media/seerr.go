package media

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
)

// Every request below is in Seerr's own API document (seerr-api.yml at the
// v3.5.2 tag of github.com/seerr-team/seerr), which the server also holds
// each request to. Two fields the sign-in route reads are not in the
// document: port and useSsl, taken from server/routes/auth.ts at that tag.

// Seerr's media server types and permission bits, from its
// server/constants/server.ts and server/lib/permissions.ts.
const (
	seerrServerJellyfin      = 2
	seerrServerNotConfigured = 4

	seerrPermissionAdmin       = 2
	seerrPermissionRequest     = 32
	seerrPermissionAutoApprove = 128
	// Every way a request can skip approval: all, films, series, and the
	// same three for 4K.
	seerrPermissionsAutoApproval = 128 | 256 | 512 | 32768 | 65536 | 131072
)

type seerrAPI struct {
	env  stacks.Env
	base string
	// key is Seerr's API key, which acts as its owner. cookies hold a session
	// instead, for the one sign-in that reads the key.
	key     string
	cookies []*http.Cookie
}

func (s *seerrAPI) call(ctx context.Context, method, path string, body any) (appReply, error) {
	header := map[string]string{}
	if s.key != "" {
		header["X-API-Key"] = s.key
	}
	reply, err := callApp(ctx, s.env, appCall{
		Method:  method,
		URL:     stacks.JoinURL(s.base, "/api/v1"+path, nil),
		Header:  header,
		JSON:    body,
		Cookies: s.cookies,
	})
	if err != nil {
		return reply, fmt.Errorf("Seerr did not answer: %w", err)
	}
	if reply.Status == http.StatusForbidden && strings.Contains(strings.ToLower(string(reply.Body)), "csrf") {
		return reply, stacks.Permanent(errors.New("Seerr's CSRF protection is on, which refuses every change made with its API key; turn it off under Settings, Network in Seerr, restart it, then run the setup again"))
	}
	return reply, nil
}

// fail is the error for an answer a step has no reading for.
func (s *seerrAPI) fail(reply appReply, what string, secrets ...string) error {
	return reply.unexpected(what, append(secrets, s.key)...)
}

type seerrPublicSettings struct {
	Initialized     bool `json:"initialized"`
	MediaServerType int  `json:"mediaServerType"`
}

func seerrPublic(ctx context.Context, api *seerrAPI) (seerrPublicSettings, error) {
	var public seerrPublicSettings
	reply, err := api.call(ctx, http.MethodGet, "/settings/public", nil)
	if err != nil {
		return public, err
	}
	if reply.Status != http.StatusOK {
		return public, api.fail(reply, "Seerr did not say whether it is set up")
	}
	return public, reply.decode(&public)
}

// isOwner reports whether the key (or session) acts as a Seerr administrator.
func (s *seerrAPI) isOwner(ctx context.Context) (bool, error) {
	reply, err := s.call(ctx, http.MethodGet, "/auth/me", nil)
	if err != nil {
		return false, err
	}
	switch reply.Status {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return false, nil
	default:
		return false, s.fail(reply, "Seerr did not say who the stack's key acts as")
	}
	var user struct {
		Permissions int `json:"permissions"`
	}
	if err := reply.decode(&user); err != nil {
		return false, err
	}
	return user.Permissions&seerrPermissionAdmin != 0, nil
}

// seerrWithKey is the client the steps after seerr.setup use.
func seerrWithKey(ctx context.Context, env stacks.Env) (*seerrAPI, error) {
	base, err := env.BaseURL(ctx, "seerr", PortSeerr)
	if err != nil {
		return nil, err
	}
	key, found, err := env.LookupCredential(ctx, CredentialSeerrAPIKey)
	if err != nil {
		return nil, err
	}
	if !found || key == "" {
		return nil, errors.New("the stack has no Seerr API key yet")
	}
	return &seerrAPI{env: env, base: base, key: key}, nil
}

type seerrLibrary struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// seerrSetup signs Seerr in to Jellyfin as the stack's administrator, which
// makes that account Seerr's owner, keeps Seerr's API key, and turns on the
// Movies and Shows libraries.
func seerrSetup(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	base, err := env.BaseURL(ctx, "seerr", PortSeerr)
	if err != nil {
		return stacks.Result{}, err
	}
	api := &seerrAPI{env: env, base: base}
	public, err := seerrPublic(ctx, api)
	if err != nil {
		return stacks.Result{}, err
	}
	stored, found, err := env.LookupCredential(ctx, CredentialSeerrAPIKey)
	if err != nil {
		return stacks.Result{}, err
	}
	keyed := false
	if found && stored != "" {
		api.key = stored
		if keyed, err = api.isOwner(ctx); err != nil {
			return stacks.Result{}, err
		}
	}
	message := "Seerr is signed in to Jellyfin"
	if !keyed {
		api.key = ""
		if err := seerrSignIn(ctx, env, api, public); err != nil {
			return stacks.Result{}, err
		}
		if public.MediaServerType == seerrServerNotConfigured {
			message = "Signed Seerr in to Jellyfin as " + AdminUsername
		} else {
			message = "Seerr is signed in to Jellyfin; read its API key again"
		}
	}

	wanted := []struct{ name, id string }{
		{"Movies", env.Output("jellyfin.libraries", "moviesLibraryId")},
		{"Shows", env.Output("jellyfin.libraries", "showsLibraryId")},
	}
	for _, library := range wanted {
		if library.id == "" {
			return stacks.Result{}, fmt.Errorf("the Jellyfin step did not report the %s library", library.name)
		}
	}
	reply, err := api.call(ctx, http.MethodGet, "/settings/jellyfin/library", nil)
	if err != nil {
		return stacks.Result{}, err
	}
	if reply.Status != http.StatusOK {
		return stacks.Result{}, api.fail(reply, "Seerr did not list its Jellyfin libraries")
	}
	var libraries []seerrLibrary
	if err := reply.decode(&libraries); err != nil {
		return stacks.Result{}, err
	}
	find := func(id string) (seerrLibrary, bool) {
		for _, library := range libraries {
			if library.ID == id {
				return library, true
			}
		}
		return seerrLibrary{}, false
	}
	changed := false
	for _, library := range wanted {
		if _, found := find(library.id); found {
			continue
		}
		// Seerr knows the libraries it last read from Jellyfin. Reading them
		// again keeps what is already turned on.
		reply, err := api.call(ctx, http.MethodPost, "/settings/jellyfin/library/sync", nil)
		if err != nil {
			return stacks.Result{}, err
		}
		if reply.Status != http.StatusOK {
			return stacks.Result{}, api.fail(reply, "Seerr could not read the libraries from Jellyfin")
		}
		libraries = nil
		if err := reply.decode(&libraries); err != nil {
			return stacks.Result{}, err
		}
		changed = true
		break
	}
	var enabled []string
	for _, library := range wanted {
		existing, found := find(library.id)
		if !found {
			return stacks.Result{}, fmt.Errorf("Seerr does not see the %s library in Jellyfin yet", library.name)
		}
		enabled = append(enabled, existing.Name)
		if existing.Enabled {
			continue
		}
		reply, err := api.call(ctx, http.MethodPut, "/settings/jellyfin/library/"+library.id, map[string]any{"enabled": true})
		if err != nil {
			return stacks.Result{}, err
		}
		if reply.Status != http.StatusOK {
			return stacks.Result{}, api.fail(reply, "Seerr did not turn on the "+library.name+" library")
		}
		changed = true
		env.Logf("turned on the %s library in Seerr", existing.Name)
	}
	if changed {
		reply, err := api.call(ctx, http.MethodGet, "/settings/jellyfin/library", nil)
		if err != nil {
			return stacks.Result{}, err
		}
		if reply.Status != http.StatusOK {
			return stacks.Result{}, api.fail(reply, "Seerr did not list its Jellyfin libraries")
		}
		libraries = nil
		if err := reply.decode(&libraries); err != nil {
			return stacks.Result{}, err
		}
		for _, library := range wanted {
			if existing, found := find(library.id); !found || !existing.Enabled {
				return stacks.Result{}, fmt.Errorf("the %s library reads back as not turned on in Seerr", library.name)
			}
		}
	}
	return stacks.Result{Message: message + "; " + strings.Join(enabled, " and ") + " are turned on"}, nil
}

// seerrSignIn signs in through Seerr with the Jellyfin administrator and
// stores the API key that session can read. On a Seerr nobody has set up it
// is the request that sets it up.
func seerrSignIn(ctx context.Context, env stacks.Env, api *seerrAPI, public seerrPublicSettings) error {
	fresh := public.MediaServerType == seerrServerNotConfigured
	if !fresh && public.MediaServerType != seerrServerJellyfin {
		return seerrTakenOver("Seerr is connected to a media server that is not Jellyfin.")
	}
	password, err := env.Credential(ctx, CredentialJellyfinAdmin)
	if err != nil {
		return err
	}
	body := map[string]any{"username": AdminUsername, "password": password}
	if fresh {
		// Seerr refuses a host once it has one, so these go only the first time.
		body["hostname"] = "jellyfin"
		body["port"] = PortJellyfin
		body["useSsl"] = false
		body["urlBase"] = ""
		body["serverType"] = seerrServerJellyfin
	}
	reply, err := api.call(ctx, http.MethodPost, "/auth/jellyfin", body)
	if err != nil {
		return err
	}
	switch {
	case reply.Status == http.StatusOK:
	case fresh && (reply.Status == http.StatusUnauthorized || reply.Status == http.StatusForbidden):
		// Not retried: Jellyfin counts each refusal towards locking the
		// account out.
		return stacks.Permanent(errors.New("Jellyfin refused the stack's administrator when Seerr signed in with it; " + reply.excerpt(password)))
	case fresh:
		return api.fail(reply, "Seerr could not sign in to Jellyfin at jellyfin:"+strconv.Itoa(PortJellyfin), password)
	case reply.Status == http.StatusUnauthorized, reply.Status == http.StatusForbidden, reply.Status == http.StatusInternalServerError:
		return seerrTakenOver("Seerr is already set up, and it did not let the stack's Jellyfin administrator in.")
	default:
		return api.fail(reply, "Seerr did not answer the sign-in", password)
	}
	var user struct {
		Permissions int `json:"permissions"`
	}
	if err := reply.decode(&user); err != nil {
		return err
	}
	if user.Permissions&seerrPermissionAdmin == 0 {
		return seerrTakenOver("Seerr is already set up with another owner; the stack's Jellyfin administrator signs in to it without the Admin permission.")
	}
	if len(reply.Cookies) == 0 {
		return errors.New("Seerr signed in without giving a session")
	}
	// The session is used for one read and then dropped. Seerr's own sign-out
	// is not called: it also deletes the owner's device in Jellyfin.
	session := &seerrAPI{env: env, base: api.base, cookies: reply.Cookies}
	reply, err = session.call(ctx, http.MethodGet, "/settings/main", nil)
	if err != nil {
		return err
	}
	if reply.Status != http.StatusOK {
		return session.fail(reply, "Seerr did not give its settings to its owner", password)
	}
	var main struct {
		APIKey string `json:"apiKey"`
	}
	if err := reply.decode(&main); err != nil {
		return err
	}
	if main.APIKey == "" {
		return errors.New("Seerr has no API key to give")
	}
	api.key = main.APIKey
	owner, err := api.isOwner(ctx)
	if err != nil {
		return err
	}
	if !owner {
		return errors.New("Seerr does not accept the API key it just gave")
	}
	if err := env.SetCredential(ctx, CredentialSeerrAPIKey, main.APIKey); err != nil {
		return err
	}
	env.Logf("signed in to Seerr as %s and stored its API key", AdminUsername)
	return nil
}

func seerrTakenOver(what string) error {
	return stacks.NeedsAction(stacks.Action{
		ID:      actionSeerrTakenOver,
		Service: "seerr",
		Title:   "Seerr was set up by someone else",
		Description: what + " Nothing was changed. In Seerr, under Users, import the Jellyfin user " + AdminUsername +
			" if it is not listed and give it the Admin permission, then run the setup again.",
		Blocking: true,
	})
}

// seerrManager is one of the two servers the stack keeps in Seerr, found
// again by its name.
type seerrManager struct {
	kind, title, name, host string
	port                    int
	variable, root          string
	profileID, profileName  string
	// defaults are the fields a new entry gets and an existing one keeps as
	// somebody left them.
	defaults map[string]any
}

func seerrManagersWanted(env stacks.Env) []seerrManager {
	return []seerrManager{
		{
			kind: "radarr", title: "Radarr", name: "Radarr (Just Dashboard)", host: "radarr", port: PortRadarr,
			variable: VariableRadarrAPIKey, root: MoviesLibrary,
			profileID:   env.Output("recyclarr.sync", "radarrProfileId"),
			profileName: env.Output("recyclarr.sync", "radarrProfileName"),
			defaults: map[string]any{
				"minimumAvailability": "released",
				"tags":                []int{},
				"externalUrl":         "",
				// Seerr reads what Radarr already has, so a film added there
				// shows as available here.
				"syncEnabled":   true,
				"preventSearch": false,
				"tagRequests":   false,
			},
		},
		{
			kind: "sonarr", title: "Sonarr", name: "Sonarr (Just Dashboard)", host: "sonarr", port: PortSonarr,
			variable: VariableSonarrAPIKey, root: TVLibrary,
			profileID:   env.Output("recyclarr.sync", "sonarrProfileId"),
			profileName: env.Output("recyclarr.sync", "sonarrProfileName"),
			defaults: map[string]any{
				"seriesType":          "standard",
				"animeSeriesType":     "anime",
				"enableSeasonFolders": true,
				"monitorNewItems":     "all",
				"tags":                []int{},
				"animeTags":           []int{},
				"externalUrl":         "",
				"syncEnabled":         true,
				"preventSearch":       false,
				"tagRequests":         false,
			},
		},
	}
}

// seerrManagerTest is what Seerr's own test of a manager answers: what it
// read from the manager with the address and key it was given.
type seerrManagerTest struct {
	Profiles []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"profiles"`
	RootFolders []struct {
		Path string `json:"path"`
	} `json:"rootFolders"`
	URLBase string `json:"urlBase"`
}

func (t seerrManagerTest) hasRoot(root string) bool {
	for _, folder := range t.RootFolders {
		if samePath(folder.Path, root) {
			return true
		}
	}
	return false
}

func (t seerrManagerTest) profileName(id int) (string, bool) {
	for _, profile := range t.Profiles {
		if profile.ID == id {
			return profile.Name, true
		}
	}
	return "", false
}

// seerrTestManager runs Seerr's connection test. failure is Seerr's own
// sentence when the manager did not answer it.
func seerrTestManager(ctx context.Context, api *seerrAPI, kind string, settings map[string]any) (test seerrManagerTest, failure string, err error) {
	apiKey, _ := settings["apiKey"].(string)
	body := map[string]any{
		"hostname": settings["hostname"],
		"port":     settings["port"],
		"apiKey":   apiKey,
		"useSsl":   settings["useSsl"],
	}
	if baseURL, found := settings["baseUrl"].(string); found {
		body["baseUrl"] = baseURL
	}
	reply, err := api.call(ctx, http.MethodPost, "/settings/"+kind+"/test", body)
	if err != nil {
		return test, "", err
	}
	if reply.Status == http.StatusInternalServerError {
		var refusal struct {
			Message string `json:"message"`
		}
		_ = reply.decode(&refusal)
		if refusal.Message == "" {
			refusal.Message = "the connection test failed"
		}
		return test, refusal.Message, nil
	}
	if reply.Status != http.StatusOK {
		return test, "", api.fail(reply, "Seerr did not run its "+kind+" test", apiKey)
	}
	return test, "", reply.decode(&test)
}

// seerrFlushManagerCache empties what Seerr remembers of a manager's
// answers. It keeps quality profiles and root folders for an hour, so a
// profile made a minute ago is not in a test's answer until this.
func seerrFlushManagerCache(ctx context.Context, api *seerrAPI, kind string) error {
	reply, err := api.call(ctx, http.MethodPost, "/settings/cache/"+kind+"/flush", nil)
	if err != nil {
		return err
	}
	if reply.Status != http.StatusNoContent {
		return api.fail(reply, "Seerr did not forget what it remembered of "+kind)
	}
	return nil
}

// seerrChooseProfile is the quality profile the quality step named: by its
// id, or by its name when that step reported no id.
func seerrChooseProfile(manager seerrManager, test seerrManagerTest) (int, string, error) {
	if manager.profileID != "" {
		id, err := strconv.Atoi(manager.profileID)
		if err != nil {
			return 0, "", stacks.Permanent(fmt.Errorf("the quality profile step reported %q as %s's profile id", manager.profileID, manager.title))
		}
		if name, found := test.profileName(id); found {
			return id, name, nil
		}
		return 0, "", fmt.Errorf("%s has no quality profile with id %d", manager.title, id)
	}
	if manager.profileName == "" {
		return 0, "", fmt.Errorf("the quality profile step did not say which %s profile to use", manager.title)
	}
	for _, profile := range test.Profiles {
		if strings.EqualFold(profile.Name, manager.profileName) {
			return profile.ID, profile.Name, nil
		}
	}
	return 0, "", fmt.Errorf("%s has no quality profile named %q", manager.title, manager.profileName)
}

func seerrListManagers(ctx context.Context, api *seerrAPI, kind string) ([]map[string]any, error) {
	reply, err := api.call(ctx, http.MethodGet, "/settings/"+kind, nil)
	if err != nil {
		return nil, err
	}
	if reply.Status != http.StatusOK {
		return nil, api.fail(reply, "Seerr did not list its "+kind+" servers")
	}
	var list []map[string]any
	return list, reply.decode(&list)
}

func seerrFindManager(list []map[string]any, name string) (map[string]any, bool) {
	for _, entry := range list {
		if entry["name"] == name {
			return entry, true
		}
	}
	return nil, false
}

// seerrManagers adds Radarr and Sonarr to Seerr as its default servers, or
// brings the stack's two entries back to what they should be. Any other
// server somebody added is left alone, except that it stops being the
// default, as it would when added by hand.
func seerrManagers(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	api, err := seerrWithKey(ctx, env)
	if err != nil {
		return stacks.Result{}, err
	}
	outputs := map[string]string{}
	var summary []string
	for _, manager := range seerrManagersWanted(env) {
		apiKey, err := env.Variable(ctx, manager.variable)
		if err != nil {
			return stacks.Result{}, err
		}
		address := map[string]any{"hostname": manager.host, "port": manager.port, "apiKey": apiKey, "useSsl": false, "baseUrl": ""}
		var (
			test        seerrManagerTest
			profileID   int
			profileName string
		)
		for attempt := range 2 {
			var failure string
			if test, failure, err = seerrTestManager(ctx, api, manager.kind, address); err != nil {
				return stacks.Result{}, err
			}
			if failure != "" {
				return stacks.Result{}, fmt.Errorf("Seerr cannot reach %s at %s:%d: %s", manager.title, manager.host, manager.port, failure)
			}
			profileID, profileName, err = seerrChooseProfile(manager, test)
			if err == nil && !test.hasRoot(manager.root) {
				err = fmt.Errorf("%s has no root folder %s yet", manager.title, manager.root)
			}
			if err == nil || attempt == 1 || stacks.IsPermanent(err) {
				break
			}
			// What is missing may only be missing from Seerr's memory.
			if flushErr := seerrFlushManagerCache(ctx, api, manager.kind); flushErr != nil {
				return stacks.Result{}, flushErr
			}
		}
		if err != nil {
			return stacks.Result{}, err
		}

		owned := map[string]any{
			"name":              manager.name,
			"hostname":          manager.host,
			"port":              manager.port,
			"apiKey":            apiKey,
			"useSsl":            false,
			"baseUrl":           test.URLBase,
			"activeProfileId":   profileID,
			"activeProfileName": profileName,
			"activeDirectory":   manager.root,
			"is4k":              false,
			"isDefault":         true,
		}
		list, err := seerrListManagers(ctx, api, manager.kind)
		if err != nil {
			return stacks.Result{}, err
		}
		existing, found := seerrFindManager(list, manager.name)
		entry := map[string]any{}
		differs := !found
		for key, value := range manager.defaults {
			entry[key] = value
		}
		for key, value := range existing {
			// The id is Seerr's to give; it refuses one in a body.
			if key != "id" {
				entry[key] = value
			}
		}
		for key, value := range owned {
			if found && !sameValue(existing[key], value) {
				differs = true
			}
			entry[key] = value
		}
		if differs {
			method, target, expect := http.MethodPost, "/settings/"+manager.kind, http.StatusCreated
			if found {
				id, isNumber := existing["id"].(float64)
				if !isNumber {
					return stacks.Result{}, fmt.Errorf("Seerr lists %s without an id", manager.name)
				}
				method, target, expect = http.MethodPut, fmt.Sprintf("/settings/%s/%d", manager.kind, int(id)), http.StatusOK
			}
			reply, err := api.call(ctx, method, target, entry)
			if err != nil {
				return stacks.Result{}, err
			}
			if reply.Status != expect {
				return stacks.Result{}, api.fail(reply, "Seerr did not save "+manager.name, apiKey)
			}
			if found {
				env.Logf("updated %s in Seerr", manager.name)
			} else {
				env.Logf("added %s to Seerr", manager.name)
			}
			if list, err = seerrListManagers(ctx, api, manager.kind); err != nil {
				return stacks.Result{}, err
			}
			saved, found := seerrFindManager(list, manager.name)
			if !found {
				return stacks.Result{}, fmt.Errorf("Seerr does not list %s after saving it", manager.name)
			}
			for key, value := range owned {
				if !sameValue(saved[key], value) {
					return stacks.Result{}, fmt.Errorf("%s reads back from Seerr with a different %s", manager.name, key)
				}
			}
			existing = saved
		}
		if id, isNumber := existing["id"].(float64); isNumber {
			outputs[manager.kind+"ServerId"] = strconv.Itoa(int(id))
		}
		summary = append(summary, fmt.Sprintf("%s files into %s with the %s profile", manager.title, manager.root, profileName))
	}
	return stacks.Result{Message: strings.Join(summary, "; "), Outputs: outputs}, nil
}

// seerrPolicy applies who may ask for a title and whether an administrator
// approves it first, then lets Seerr leave its setup pages.
//
// Only what new accounts are given is set. An account that exists keeps the
// permissions an administrator gave it, auto-approval included.
func seerrPolicy(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	approval := env.Option(OptionRequestApproval)
	if approval == "" {
		approval = "admin"
	}
	if approval != "admin" && approval != "auto" {
		return stacks.Result{}, stacks.Permanent(fmt.Errorf("request approval is %q; it is admin or auto", approval))
	}
	api, err := seerrWithKey(ctx, env)
	if err != nil {
		return stacks.Result{}, err
	}
	type mainSettings struct {
		DefaultPermissions int  `json:"defaultPermissions"`
		MediaServerLogin   bool `json:"mediaServerLogin"`
		NewPlexLogin       bool `json:"newPlexLogin"`
	}
	read := func() (mainSettings, error) {
		var main mainSettings
		reply, err := api.call(ctx, http.MethodGet, "/settings/main", nil)
		if err != nil {
			return main, err
		}
		if reply.Status != http.StatusOK {
			return main, api.fail(reply, "Seerr did not give its settings")
		}
		return main, reply.decode(&main)
	}
	main, err := read()
	if err != nil {
		return stacks.Result{}, err
	}
	// Whatever else somebody added to the default permissions stays; only
	// asking and approval are the stack's.
	permissions := main.DefaultPermissions | seerrPermissionRequest
	if approval == "auto" {
		permissions |= seerrPermissionAutoApprove
	} else {
		permissions &^= seerrPermissionsAutoApproval
	}
	change := map[string]any{}
	if permissions != main.DefaultPermissions {
		change["defaultPermissions"] = permissions
	}
	// Signing in with a Jellyfin account, and an account for whoever does so
	// the first time (the setting keeps its Plex-era name).
	if !main.MediaServerLogin {
		change["mediaServerLogin"] = true
	}
	if !main.NewPlexLogin {
		change["newPlexLogin"] = true
	}
	if len(change) > 0 {
		reply, err := api.call(ctx, http.MethodPost, "/settings/main", change)
		if err != nil {
			return stacks.Result{}, err
		}
		if reply.Status != http.StatusOK {
			return stacks.Result{}, api.fail(reply, "Seerr did not save who may request")
		}
		if main, err = read(); err != nil {
			return stacks.Result{}, err
		}
		if main.DefaultPermissions != permissions || !main.MediaServerLogin || !main.NewPlexLogin {
			return stacks.Result{}, errors.New("Seerr's request settings read back different from what was saved")
		}
		env.Logf("set what a new Seerr account may do (request approval: %s)", approval)
	}

	public, err := seerrPublic(ctx, api)
	if err != nil {
		return stacks.Result{}, err
	}
	if !public.Initialized {
		reply, err := api.call(ctx, http.MethodPost, "/settings/initialize", nil)
		if err != nil {
			return stacks.Result{}, err
		}
		if reply.Status != http.StatusOK {
			return stacks.Result{}, api.fail(reply, "Seerr did not finish its setup")
		}
		if err := reply.decode(&public); err != nil {
			return stacks.Result{}, err
		}
		if !public.Initialized {
			return stacks.Result{}, errors.New("Seerr still shows its setup pages after finishing setup")
		}
		env.Logf("finished Seerr's setup")
	}
	message := "People sign in with their Jellyfin account and may request; an administrator approves each request"
	if approval == "auto" {
		message = "People sign in with their Jellyfin account and may request; requests are approved automatically"
	}
	return stacks.Result{Message: message, Outputs: map[string]string{"requestApproval": approval}}, nil
}

// seerrVerify has Seerr itself reach Jellyfin and every manager it holds.
func seerrVerify(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	api, err := seerrWithKey(ctx, env)
	if err != nil {
		return stacks.Result{}, err
	}
	var failed, others []string
	reached := []string{"Jellyfin"}

	// Seerr has no test route for Jellyfin that does not also save settings.
	// Listing Jellyfin's users is the read it makes with the address and key
	// it holds, and it fails when either is wrong.
	reply, err := api.call(ctx, http.MethodGet, "/settings/jellyfin/users", nil)
	if err != nil {
		return stacks.Result{}, err
	}
	var users []struct {
		ID string `json:"id"`
	}
	if reply.Status != http.StatusOK {
		failed = append(failed, "Jellyfin ("+reply.excerpt(api.key)+")")
		reached = nil
	} else if err := reply.decode(&users); err != nil || len(users) == 0 {
		failed = append(failed, "Jellyfin (it listed no accounts)")
		reached = nil
	}

	for _, manager := range seerrManagersWanted(env) {
		list, err := seerrListManagers(ctx, api, manager.kind)
		if err != nil {
			return stacks.Result{}, err
		}
		_, own := seerrFindManager(list, manager.name)
		if env.Has(manager.kind) && !own {
			failed = append(failed, manager.name+" (Seerr does not have it)")
		}
		if len(list) == 0 {
			continue
		}
		// A test answered from memory would pass a root folder that has since
		// been removed.
		if err := seerrFlushManagerCache(ctx, api, manager.kind); err != nil {
			return stacks.Result{}, err
		}
		for _, entry := range list {
			name, _ := entry["name"].(string)
			test, failure, err := seerrTestManager(ctx, api, manager.kind, entry)
			if err != nil {
				return stacks.Result{}, err
			}
			if failure == "" && name == manager.name {
				directory, _ := entry["activeDirectory"].(string)
				profile, _ := entry["activeProfileId"].(float64)
				if !test.hasRoot(directory) {
					failure = manager.title + " no longer has the root folder " + directory
				} else if _, found := test.profileName(int(profile)); !found {
					failure = manager.title + " no longer has the quality profile Seerr sends requests with"
				}
			}
			switch {
			case failure == "":
				reached = append(reached, name)
			case name == manager.name:
				failed = append(failed, name+" ("+failure+")")
			default:
				// A server somebody added themselves is theirs to mend; it is
				// said, and it does not fail the stack.
				others = append(others, name)
				env.Logf("Seerr's test of %s, which is not the stack's, failed: %s", name, failure)
			}
		}
	}
	if len(failed) > 0 {
		return stacks.Result{}, fmt.Errorf("Seerr's connection test failed for %s", strings.Join(failed, "; "))
	}
	message := "Seerr reaches " + strings.Join(reached, ", ")
	if len(others) > 0 {
		message += ". Its test of " + strings.Join(others, ", ") + " failed; that server is not the stack's"
	}
	return stacks.Result{Message: message}, nil
}
