package media

import (
	"context"
	"crypto/pbkdf2"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
)

// Every request below is in qBittorrent's WebUI API reference
// (github.com/qbittorrent/qBittorrent/wiki, "WebUI API (qBittorrent 5.0)"),
// checked against 5.3.1 (Web API 2.16.2), which differs from the page in one
// way the code depends on: a refused sign-in is HTTP 401 and an accepted one
// HTTP 204, where older versions answered 200 with "Fails." or "Ok.".

// qbittorrentConfigFile is where the linuxserver.io image keeps the
// application's settings.
const qbittorrentConfigFile = "/config/qBittorrent/qBittorrent.conf"

// The image's own defaults, which point at folders the stack does not mount.
const (
	qbittorrentImageTempPath = "/downloads/incomplete"
	qbittorrentTempPath      = DownloadsRoot + "/incomplete"
)

// qbittorrentCategoriesWanted are the two categories Radarr and Sonarr file
// their downloads under. No other category is touched.
var qbittorrentCategoriesWanted = []struct{ name, path string }{
	{"movies", MoviesDownload},
	{"tv", TVDownload},
}

// The first start of a qBittorrent with no password prints one that lasts
// until it stops.
var qbittorrentTemporaryPassword = regexp.MustCompile(`temporary password is provided for this session:\s*(\S+)`)

// qbittorrentStoredPassword is the web interface's password as the settings
// file holds it: PBKDF2-HMAC-SHA512, 100000 rounds, salt and key in base64.
var qbittorrentStoredPassword = regexp.MustCompile(`^"?@ByteArray\(([A-Za-z0-9+/=]+):([A-Za-z0-9+/=]+)\)"?$`)

type qbittorrentSignIn int

const (
	qbittorrentAccepted qbittorrentSignIn = iota
	qbittorrentRefused
	qbittorrentBanned
)

type qbittorrentAPI struct {
	env     stacks.Env
	base    string
	cookies []*http.Cookie
}

func (q *qbittorrentAPI) call(ctx context.Context, method, path string, form url.Values) (appReply, error) {
	reply, err := callApp(ctx, q.env, appCall{
		Method: method,
		URL:    stacks.JoinURL(q.base, path, nil),
		// qBittorrent refuses a request whose Referer or Origin is another
		// site's (its CSRF check) and one whose Host it was not told to serve.
		// The address asked is the container's own, which the image's
		// ServerDomains=* accepts; the Referer says the same address.
		Header:  map[string]string{"Referer": q.base},
		Form:    form,
		Cookies: q.cookies,
	})
	if err != nil {
		return reply, fmt.Errorf("qBittorrent did not answer: %w", err)
	}
	return reply, nil
}

// signIn keeps the session cookie on success. A refusal is counted by
// qBittorrent, which shuts an address out for an hour after five, so callers
// sign in only with a password they have reason to believe.
func (q *qbittorrentAPI) signIn(ctx context.Context, password string) (qbittorrentSignIn, error) {
	q.cookies = nil
	reply, err := q.call(ctx, http.MethodPost, "/api/v2/auth/login", url.Values{"username": {AdminUsername}, "password": {password}})
	if err != nil {
		return qbittorrentRefused, err
	}
	body := strings.TrimSpace(string(reply.Body))
	switch {
	case reply.Status == http.StatusNoContent, reply.Status == http.StatusOK && body == "Ok.":
		if len(reply.Cookies) == 0 {
			return qbittorrentRefused, errors.New("qBittorrent accepted the sign-in without giving a session")
		}
		q.cookies = reply.Cookies
		return qbittorrentAccepted, nil
	case reply.Status == http.StatusUnauthorized, reply.Status == http.StatusOK && body == "Fails.":
		return qbittorrentRefused, nil
	case reply.Status == http.StatusForbidden:
		return qbittorrentBanned, nil
	}
	return qbittorrentRefused, reply.unexpected("qBittorrent did not answer the sign-in", password)
}

// signOut ends the session: qBittorrent keeps a limited number at once.
func (q *qbittorrentAPI) signOut(ctx context.Context) {
	if len(q.cookies) == 0 {
		return
	}
	_, _ = q.call(context.WithoutCancel(ctx), http.MethodPost, "/api/v2/auth/logout", url.Values{})
	q.cookies = nil
}

var errQBittorrentBanned = stacks.Permanent(errors.New("qBittorrent is refusing sign-ins from the dashboard for an hour after too many wrong passwords; run the setup again after that"))

// qbittorrentAuth replaces the temporary password the image prints with the
// stack's one login, and brings a qBittorrent on an older password of the
// stack's (its own from before the one login, or the login before its last
// change) to the login. Authentication stays as the image has it: on for
// every address, this one included.
//
// Five refused sign-ins shut an address out for an hour, so the password is
// first matched offline against the settings file, and only the one it holds
// is ever tried.
func qbittorrentAuth(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	base, err := env.BaseURL(ctx, "qbittorrent", PortQBittorrent)
	if err != nil {
		return stacks.Result{}, err
	}
	api := &qbittorrentAPI{env: env, base: base}
	// Asked without a session, the version is refused with 403 once the web
	// interface is up. Until then nothing below can be trusted, the log least
	// of all: the temporary password is printed just before it starts.
	reply, err := api.call(ctx, http.MethodGet, "/api/v2/app/version", nil)
	if err != nil {
		return stacks.Result{}, err
	}
	if reply.Status != http.StatusForbidden && reply.Status != http.StatusOK {
		return stacks.Result{}, reply.unexpected("qBittorrent's web interface is not ready")
	}

	login, older, err := loginCandidates(ctx, env, legacyQBittorrent)
	if err != nil {
		return stacks.Result{}, err
	}
	config, configErr := env.ReadFile(ctx, "qbittorrent", qbittorrentConfigFile)
	passwordSet, current := false, ""
	if configErr == nil {
		for _, candidate := range append([]string{login}, older...) {
			set, matches := qbittorrentPasswordState(config, candidate)
			passwordSet = set
			if matches {
				current = candidate
				break
			}
		}
	} else if _, haveLegacy, err := env.LookupCredential(ctx, legacyQBittorrent); err != nil {
		return stacks.Result{}, err
	} else {
		// Without the file, one try: the password this stack set last.
		passwordSet, current = true, login
		if haveLegacy && len(older) > 0 {
			current = older[len(older)-1]
		}
	}

	if current != "" {
		outcome, err := api.signIn(ctx, current)
		if err != nil {
			return stacks.Result{}, err
		}
		switch outcome {
		case qbittorrentBanned:
			return stacks.Result{}, errQBittorrentBanned
		case qbittorrentRefused:
			if configErr == nil {
				// Not retried: every refusal is one of five.
				return stacks.Result{}, stacks.Permanent(errors.New("qBittorrent refused the password its own settings hold"))
			}
			return stacks.Result{}, qbittorrentPasswordAction()
		}
		if current == login {
			api.signOut(ctx)
			if err := forgetLegacy(ctx, env, legacyQBittorrent); err != nil {
				return stacks.Result{}, err
			}
			return stacks.Result{Message: "qBittorrent's web interface signs in with the stack's password"}, nil
		}
		defer api.signOut(ctx)
		if err := qbittorrentSetPassword(ctx, env, api, login, current); err != nil {
			return stacks.Result{}, err
		}
		if err := forgetLegacy(ctx, env, legacyQBittorrent); err != nil {
			return stacks.Result{}, err
		}
		env.Logf("changed qBittorrent's password to the stack's one login")
		return stacks.Result{Message: "qBittorrent's web interface now signs in with the stack's password"}, nil
	}
	if passwordSet {
		return stacks.Result{}, qbittorrentPasswordAction()
	}

	logs, err := env.Logs(ctx, "qbittorrent", 5000)
	if err != nil {
		return stacks.Result{}, fmt.Errorf("could not read qBittorrent's log for its temporary password: %w", err)
	}
	found := qbittorrentTemporaryPassword.FindAllStringSubmatch(logs, -1)
	if len(found) == 0 {
		return stacks.Result{}, errors.New("qBittorrent has no password and its log no longer shows the temporary one; restart the qbittorrent service so it prints a new one, then run the setup again")
	}
	// Each start prints a new one; only the last is alive.
	temporary := found[len(found)-1][1]
	outcome, err := api.signIn(ctx, temporary)
	if err != nil {
		return stacks.Result{}, err
	}
	switch outcome {
	case qbittorrentBanned:
		return stacks.Result{}, errQBittorrentBanned
	case qbittorrentRefused:
		// Not retried: every refusal is one of five.
		return stacks.Result{}, stacks.Permanent(errors.New("qBittorrent refused the temporary password from its log; restart the qbittorrent service so it prints a new one, then run the setup again"))
	}
	defer api.signOut(ctx)
	if err := qbittorrentSetPassword(ctx, env, api, login, temporary); err != nil {
		return stacks.Result{}, err
	}
	env.Logf("replaced qBittorrent's temporary password with the stack's own")
	return stacks.Result{Message: "Replaced qBittorrent's temporary password; " + AdminUsername + " signs in with the stack's"}, nil
}

func qbittorrentPasswordAction() error {
	return stacks.NeedsAction(stacks.Action{
		ID:      actionQBittorrentPassword,
		Service: "qbittorrent",
		Title:   "qBittorrent has a password this stack did not set",
		Description: "Nothing was changed. Sign in to qBittorrent as " + AdminUsername + " and, under Tools, Options, WebUI, " +
			"set the password to the stack's password shown on this page, then run the setup again. " +
			"Radarr and Sonarr cannot hand it downloads until then.",
		Blocking: true,
	})
}

// qbittorrentSetPassword gives the signed-in web interface the login, reads
// it back the only way a password can be (by signing in with it), and waits
// for qBittorrent to write it to its settings file, which it does a moment
// later: the next pass matches the file before it signs in.
func qbittorrentSetPassword(ctx context.Context, env stacks.Env, api *qbittorrentAPI, login, old string) error {
	preferences, err := json.Marshal(map[string]any{"web_ui_password": login})
	if err != nil {
		return stacks.Permanent(err)
	}
	reply, err := api.call(ctx, http.MethodPost, "/api/v2/app/setPreferences", url.Values{"json": {string(preferences)}})
	if err != nil {
		return err
	}
	if reply.Status != http.StatusOK {
		return reply.unexpected("qBittorrent did not take the new password", login, old)
	}
	api.signOut(ctx)
	outcome, err := api.signIn(ctx, login)
	if err != nil {
		return err
	}
	switch outcome {
	case qbittorrentBanned:
		return errQBittorrentBanned
	case qbittorrentRefused:
		return errors.New("qBittorrent took the new password but does not sign in with it yet")
	}
	for wait := 0; wait < 30; wait++ {
		config, err := env.ReadFile(ctx, "qbittorrent", qbittorrentConfigFile)
		if err == nil {
			if _, matches := qbittorrentPasswordState(config, login); matches {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(qbittorrentSaveWait):
		}
	}
	env.Logf("qBittorrent signs in with the new password but has not written it to its settings file yet")
	return nil
}

// qbittorrentSaveWait is how often the settings file is read back after a
// change. A variable so tests need not wait.
var qbittorrentSaveWait = time.Second

// qbittorrentWebSettings is the web interface's part of the settings file.
// 5.x keeps it in a [WebUI] section; versions before kept the same keys in
// [Preferences] with a WebUI\ prefix, and a file carried over from one may
// still have them there.
func qbittorrentWebSettings(config []byte) map[string]string {
	settings := map[string]string{}
	section := ""
	for line := range strings.Lines(string(config)) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = line[1 : len(line)-1]
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		switch section {
		case "WebUI":
			settings[key] = value
		case "Preferences":
			if name, isWeb := strings.CutPrefix(key, `WebUI\`); isWeb {
				if _, newer := settings[name]; !newer {
					settings[name] = value
				}
			}
		}
	}
	return settings
}

// qbittorrentPasswordState reads the settings file: whether the web
// interface has a password at all, and whether admin signs in with candidate.
func qbittorrentPasswordState(config []byte, candidate string) (set, matches bool) {
	settings := qbittorrentWebSettings(config)
	stored := settings["Password_PBKDF2"]
	if stored == "" {
		return false, false
	}
	// The file names the account only once somebody has renamed it.
	if name := strings.Trim(settings["Username"], `"`); name != "" && name != AdminUsername {
		return true, false
	}
	found := qbittorrentStoredPassword.FindStringSubmatch(stored)
	if found == nil || candidate == "" {
		// A password in a form this code does not know is still a password;
		// it just cannot be compared.
		return true, false
	}
	salt, errSalt := base64.StdEncoding.DecodeString(found[1])
	want, errKey := base64.StdEncoding.DecodeString(found[2])
	if errSalt != nil || errKey != nil || len(want) == 0 {
		return true, false
	}
	got, err := pbkdf2.Key(sha512.New, candidate, salt, 100000, len(want))
	if err != nil {
		return true, false
	}
	return true, subtle.ConstantTimeCompare(got, want) == 1
}

type qbittorrentCategory struct {
	SavePath string `json:"savePath"`
}

type qbittorrentPreferences struct {
	SavePath       string `json:"save_path"`
	TempPath       string `json:"temp_path"`
	AutoManagement bool   `json:"auto_tmm_enabled"`
}

// qbittorrentCategories points qBittorrent's downloads at the shared volume
// and gives Radarr and Sonarr a category each.
func qbittorrentCategories(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	base, err := env.BaseURL(ctx, "qbittorrent", PortQBittorrent)
	if err != nil {
		return stacks.Result{}, err
	}
	password, err := stackLogin(ctx, env)
	if err != nil {
		return stacks.Result{}, err
	}
	api := &qbittorrentAPI{env: env, base: base}
	outcome, err := api.signIn(ctx, password)
	if err != nil {
		return stacks.Result{}, err
	}
	switch outcome {
	case qbittorrentBanned:
		return stacks.Result{}, errQBittorrentBanned
	case qbittorrentRefused:
		// Not retried, for the same reason as above.
		return stacks.Result{}, stacks.Permanent(errors.New("qBittorrent refused the stack's password"))
	}
	defer api.signOut(ctx)

	preferences, err := qbittorrentReadPreferences(ctx, api)
	if err != nil {
		return stacks.Result{}, err
	}
	changed := false
	if !samePath(preferences.SavePath, DownloadsRoot) {
		change := map[string]any{
			"save_path": DownloadsRoot,
			// A category's folder is used only under automatic management;
			// without it everything lands in the default folder. It is turned
			// on with the folder and not again: somebody who turns it off
			// later has chosen to.
			"auto_tmm_enabled": true,
		}
		if samePath(preferences.TempPath, qbittorrentImageTempPath) {
			// Not used unless somebody turns it on, and then it must not be
			// a folder inside the container.
			change["temp_path"] = qbittorrentTempPath
		}
		encoded, err := json.Marshal(change)
		if err != nil {
			return stacks.Result{}, stacks.Permanent(err)
		}
		reply, err := api.call(ctx, http.MethodPost, "/api/v2/app/setPreferences", url.Values{"json": {string(encoded)}})
		if err != nil {
			return stacks.Result{}, err
		}
		if reply.Status != http.StatusOK {
			return stacks.Result{}, reply.unexpected("qBittorrent did not take the download folder", password)
		}
		changed = true
		env.Logf("set qBittorrent's default download folder to %s", DownloadsRoot)
	}

	categories, err := qbittorrentReadCategories(ctx, api)
	if err != nil {
		return stacks.Result{}, err
	}
	for _, wanted := range qbittorrentCategoriesWanted {
		existing, found := categories[wanted.name]
		if found && samePath(existing.SavePath, wanted.path) {
			continue
		}
		endpoint, verb := "/api/v2/torrents/createCategory", "create"
		if found {
			endpoint, verb = "/api/v2/torrents/editCategory", "change"
		}
		reply, err := api.call(ctx, http.MethodPost, endpoint, url.Values{"category": {wanted.name}, "savePath": {wanted.path}})
		if err != nil {
			return stacks.Result{}, err
		}
		if reply.Status != http.StatusOK {
			return stacks.Result{}, reply.unexpected(fmt.Sprintf("qBittorrent did not %s the %s category", verb, wanted.name), password)
		}
		changed = true
		env.Logf("%sd the %s category at %s", verb, wanted.name, wanted.path)
	}

	if changed {
		if preferences, err = qbittorrentReadPreferences(ctx, api); err != nil {
			return stacks.Result{}, err
		}
		if !samePath(preferences.SavePath, DownloadsRoot) {
			return stacks.Result{}, fmt.Errorf("qBittorrent's download folder reads back as %q, not %s", preferences.SavePath, DownloadsRoot)
		}
		if categories, err = qbittorrentReadCategories(ctx, api); err != nil {
			return stacks.Result{}, err
		}
		for _, wanted := range qbittorrentCategoriesWanted {
			if !samePath(categories[wanted.name].SavePath, wanted.path) {
				return stacks.Result{}, fmt.Errorf("qBittorrent's %s category reads back at %q, not %s", wanted.name, categories[wanted.name].SavePath, wanted.path)
			}
		}
	}
	message := fmt.Sprintf("Downloads go to %s; movies to %s, tv to %s", DownloadsRoot, MoviesDownload, TVDownload)
	if !preferences.AutoManagement {
		message += ". Automatic torrent management is off in qBittorrent, so categories do not choose the folder"
	}
	return stacks.Result{Message: message, Outputs: map[string]string{"moviesCategory": "movies", "tvCategory": "tv"}}, nil
}

func qbittorrentReadPreferences(ctx context.Context, api *qbittorrentAPI) (qbittorrentPreferences, error) {
	var preferences qbittorrentPreferences
	reply, err := api.call(ctx, http.MethodGet, "/api/v2/app/preferences", nil)
	if err != nil {
		return preferences, err
	}
	if reply.Status != http.StatusOK {
		return preferences, reply.unexpected("qBittorrent did not give its settings")
	}
	return preferences, reply.decode(&preferences)
}

func qbittorrentReadCategories(ctx context.Context, api *qbittorrentAPI) (map[string]qbittorrentCategory, error) {
	reply, err := api.call(ctx, http.MethodGet, "/api/v2/torrents/categories", nil)
	if err != nil {
		return nil, err
	}
	if reply.Status != http.StatusOK {
		return nil, reply.unexpected("qBittorrent did not list its categories")
	}
	categories := map[string]qbittorrentCategory{}
	return categories, reply.decode(&categories)
}

// qbittorrentAddress lets the web interface be used at its public address
// behind the proxy: reverse proxy support on, with the networks its
// container is on as the trusted proxies, so the refused sign-ins it counts
// towards a ban are the visitor's and not the proxy's; and, when it checks
// the Host header against a list, its address on that list. Entries the
// person added to either list stay.
func qbittorrentAddress(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	base, err := env.BaseURL(ctx, "qbittorrent", PortQBittorrent)
	if err != nil {
		return stacks.Result{}, err
	}
	password, err := stackLogin(ctx, env)
	if err != nil {
		return stacks.Result{}, err
	}
	subnets, err := env.NetworkSubnets(ctx, "qbittorrent")
	if err != nil {
		return stacks.Result{}, err
	}
	api := &qbittorrentAPI{env: env, base: base}
	outcome, err := api.signIn(ctx, password)
	if err != nil {
		return stacks.Result{}, err
	}
	switch outcome {
	case qbittorrentBanned:
		return stacks.Result{}, errQBittorrentBanned
	case qbittorrentRefused:
		return stacks.Result{}, stacks.Permanent(errors.New("qBittorrent refused the stack's password"))
	}
	defer api.signOut(ctx)

	read := func() (map[string]any, error) {
		reply, err := api.call(ctx, http.MethodGet, "/api/v2/app/preferences", nil)
		if err != nil {
			return nil, err
		}
		if reply.Status != http.StatusOK {
			return nil, reply.unexpected("qBittorrent did not give its settings")
		}
		preferences := map[string]any{}
		return preferences, reply.decode(&preferences)
	}
	preferences, err := read()
	if err != nil {
		return stacks.Result{}, err
	}
	if _, known := preferences["web_ui_reverse_proxy_enabled"]; !known {
		return stacks.Result{}, stacks.Permanent(errors.New("this qBittorrent has no reverse proxy settings"))
	}
	host := ""
	if address, found := env.PublicURL("qbittorrent"); found {
		if parsed, err := url.Parse(address); err == nil {
			host = parsed.Hostname()
		}
	}
	change := qbittorrentAddressChange(preferences, subnets, host)
	if len(change) > 0 {
		encoded, err := json.Marshal(change)
		if err != nil {
			return stacks.Result{}, stacks.Permanent(err)
		}
		reply, err := api.call(ctx, http.MethodPost, "/api/v2/app/setPreferences", url.Values{"json": {string(encoded)}})
		if err != nil {
			return stacks.Result{}, err
		}
		if reply.Status != http.StatusOK {
			return stacks.Result{}, reply.unexpected("qBittorrent did not take its reverse proxy settings", password)
		}
		if preferences, err = read(); err != nil {
			return stacks.Result{}, err
		}
		if again := qbittorrentAddressChange(preferences, subnets, host); len(again) > 0 {
			return stacks.Result{}, fmt.Errorf("qBittorrent's reverse proxy settings read back different from what was saved: %v", again)
		}
		env.Logf("qBittorrent trusts the proxy at %s", strings.Join(subnets, ", "))
	}
	message := "qBittorrent works behind the proxy and counts each visitor's own address"
	if host != "" {
		message += "; its address is " + host
	}
	return stacks.Result{Message: message}, nil
}

// qbittorrentAddressChange is what differs from the reverse proxy settings
// the stack wants.
func qbittorrentAddressChange(preferences map[string]any, subnets []string, host string) map[string]any {
	change := map[string]any{}
	if enabled, _ := preferences["web_ui_reverse_proxy_enabled"].(bool); !enabled {
		change["web_ui_reverse_proxy_enabled"] = true
	}
	proxies, _ := preferences["web_ui_reverse_proxies_list"].(string)
	if merged, grew := qbittorrentList(proxies, subnets); grew {
		change["web_ui_reverse_proxies_list"] = merged
	}
	if checks, _ := preferences["web_ui_host_header_validation_enabled"].(bool); checks && host != "" {
		domains, _ := preferences["web_ui_domain_list"].(string)
		if merged, grew := qbittorrentList(domains, []string{host}); grew && !slices.Contains(qbittorrentListItems(domains), "*") {
			change["web_ui_domain_list"] = merged
		}
	}
	return change
}

// qbittorrentList adds entries to one of qBittorrent's semicolon lists,
// keeping what is there.
func qbittorrentList(current string, add []string) (string, bool) {
	items := qbittorrentListItems(current)
	grew := false
	for _, entry := range add {
		if !slices.Contains(items, entry) {
			items = append(items, entry)
			grew = true
		}
	}
	return strings.Join(items, ";"), grew
}

func qbittorrentListItems(list string) []string {
	var items []string
	for _, item := range strings.FieldsFunc(list, func(r rune) bool { return r == ';' || r == ',' }) {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}
