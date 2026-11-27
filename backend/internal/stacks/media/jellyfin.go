package media

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
)

// Every request below is in Jellyfin's own OpenAPI document, which the
// running server serves at /api-docs/openapi.json (read from 12.4.0).

// jellyfinKeyName is the application name the stack's API key is listed
// under in Jellyfin's dashboard; the key is found again by it.
const jellyfinKeyName = "Just Dashboard"

// jellyfinClientHeader is how a client names itself to Jellyfin. A session
// is kept per device id, so signing in again replaces the stack's earlier
// session instead of adding one.
const jellyfinClientHeader = `MediaBrowser Client="Just Dashboard", Device="Just Dashboard", DeviceId="just-dashboard-stack", Version="` + Version + `"`

// jellyfinLibrary is a library the stack wants: matched by its folder, since
// Jellyfin renames a second "Movies" to "Movies2" rather than refusing it.
type jellyfinLibrary struct {
	name, kind, path, output string
}

var jellyfinLibrariesWanted = []jellyfinLibrary{
	{name: "Movies", kind: "movies", path: MoviesLibrary, output: "moviesLibraryId"},
	{name: "Shows", kind: "tvshows", path: TVLibrary, output: "showsLibraryId"},
}

type jellyfinAPI struct {
	env  stacks.Env
	base string
	// token is a session's access token or an API key; both go in the same
	// header.
	token string
}

func (j *jellyfinAPI) call(ctx context.Context, method, path string, query url.Values, body any) (appReply, error) {
	authorization := jellyfinClientHeader
	if j.token != "" {
		authorization += `, Token="` + j.token + `"`
	}
	reply, err := callApp(ctx, j.env, appCall{
		Method: method,
		URL:    stacks.JoinURL(j.base, path, query),
		Header: map[string]string{"Authorization": authorization},
		JSON:   body,
	})
	if err != nil {
		return reply, fmt.Errorf("Jellyfin did not answer: %w", err)
	}
	if reply.Status == http.StatusServiceUnavailable {
		return reply, errors.New("Jellyfin is still starting")
	}
	return reply, nil
}

type jellyfinPublicInfo struct {
	ID                     string `json:"Id"`
	Version                string `json:"Version"`
	StartupWizardCompleted bool   `json:"StartupWizardCompleted"`
}

type jellyfinKey struct {
	AccessToken string `json:"AccessToken"`
	AppName     string `json:"AppName"`
}

// Markers kept with the stack's credentials, though neither is a secret.
const (
	// markerJellyfinLogin records which login Jellyfin's administrator was
	// last confirmed to sign in with (loginDigest), so a pass need not try a
	// password to know it is still the one.
	markerJellyfinLogin = "jellyfin-login-applied"
	// markerJellyfinAsked records that the stack told the person to give its
	// account to a Jellyfin someone else set up: from then on its password is
	// one the person may have set, worth one sign-in a pass.
	markerJellyfinAsked = "jellyfin-owner-asked"
)

// jellyfinSetup finishes Jellyfin's first-run wizard with the stack's own
// administrator, keeps an API key for the steps after it, and brings the
// administrator's password to the stack's one login.
//
// A wrong password counts towards Jellyfin locking an account out, so a
// password is tried only when there is no working API key or the login
// changed, and never one Jellyfin cannot have been given.
func jellyfinSetup(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	base, err := env.BaseURL(ctx, "jellyfin", PortJellyfin)
	if err != nil {
		return stacks.Result{}, err
	}
	api := &jellyfinAPI{env: env, base: base}
	reply, err := api.call(ctx, http.MethodGet, "/System/Info/Public", nil, nil)
	if err != nil {
		return stacks.Result{}, err
	}
	if reply.Status != http.StatusOK {
		return stacks.Result{}, reply.unexpected("Jellyfin did not say whether it is set up")
	}
	var info jellyfinPublicInfo
	if err := reply.decode(&info); err != nil {
		return stacks.Result{}, err
	}
	outputs := map[string]string{"serverId": info.ID, "version": info.Version}
	login, older, err := loginCandidates(ctx, env, legacyJellyfinAdmin)
	if err != nil {
		return stacks.Result{}, err
	}

	ranWizard, keyWorks := false, false
	if !info.StartupWizardCompleted {
		if err := jellyfinWizard(ctx, api, login); err != nil {
			return stacks.Result{}, err
		}
		ranWizard = true
		env.Logf("finished Jellyfin's first-run setup with the administrator %s", AdminUsername)
	} else if key, found, err := env.LookupCredential(ctx, CredentialJellyfinAPIKey); err != nil {
		return stacks.Result{}, err
	} else if found && key != "" {
		if keyWorks, err = jellyfinKeyWorks(ctx, env, base, key); err != nil {
			return stacks.Result{}, err
		}
		if !keyWorks {
			env.Logf("Jellyfin no longer accepts the stack's API key; signing in to make another")
		}
	}

	created := false
	if !keyWorks {
		if created, err = jellyfinMakeKey(ctx, env, api, login, older, ranWizard); err != nil {
			return stacks.Result{}, err
		}
	}
	key, _, err := env.LookupCredential(ctx, CredentialJellyfinAPIKey)
	if err != nil {
		return stacks.Result{}, err
	}
	changed, err := jellyfinLogin(ctx, env, base, key, login)
	if err != nil {
		return stacks.Result{}, err
	}
	if err := forgetLegacy(ctx, env, legacyJellyfinAdmin); err != nil {
		return stacks.Result{}, err
	}
	message := "Jellyfin is set up; " + AdminUsername + " signs in with the stack's password"
	switch {
	case ranWizard:
		message = "Finished Jellyfin's first-run setup and created the administrator " + AdminUsername
	case changed:
		message = "Jellyfin's administrator " + AdminUsername + " now signs in with the stack's password"
	case created:
		message = "Jellyfin is set up; made a new API key for the stack"
	case keyWorks:
		message = "Jellyfin is set up, and the stack's API key works"
	}
	return stacks.Result{Message: message, Outputs: outputs}, nil
}

// jellyfinMakeKey signs in as the administrator and makes the stack's API
// key, or finds the one it made before. It reports whether it made one.
func jellyfinMakeKey(ctx context.Context, env stacks.Env, api *jellyfinAPI, login string, older []string, ranWizard bool) (bool, error) {
	if !ranWizard {
		// Somebody else finished the wizard. The stack's account can be
		// there only if the stack set its password before, or asked the
		// person to: otherwise a sign-in would be one refusal spent on a
		// password Jellyfin was never given.
		known := false
		for _, marker := range []string{markerJellyfinLogin, markerJellyfinAsked, legacyJellyfinAdmin} {
			_, found, err := env.LookupCredential(ctx, marker)
			if err != nil {
				return false, err
			}
			known = known || found
		}
		if !known {
			if err := env.SetCredential(ctx, markerJellyfinAsked, "asked"); err != nil {
				return false, err
			}
			return false, jellyfinTakenOver()
		}
	}
	token, signedIn, err := jellyfinSignIn(ctx, api, login)
	if err != nil {
		return false, err
	}
	usedLogin := signedIn
	// A login changed before Jellyfin was brought to it: the one before is
	// tried once, and the steps below then set the new one.
	for _, candidate := range older {
		if signedIn || ranWizard {
			break
		}
		if token, signedIn, err = jellyfinSignIn(ctx, api, candidate); err != nil {
			return false, err
		}
	}
	if !signedIn {
		if ranWizard {
			return false, errors.New("Jellyfin refused the administrator it was just set up with")
		}
		if err := env.SetCredential(ctx, markerJellyfinAsked, "asked"); err != nil {
			return false, err
		}
		return false, jellyfinTakenOver()
	}
	session := &jellyfinAPI{env: env, base: api.base, token: token}
	// The session was only for making the key. Leaving it would add a device
	// to the administrator's list on every run.
	defer func() { _, _ = session.call(context.WithoutCancel(ctx), http.MethodPost, "/Sessions/Logout", nil, nil) }()
	if usedLogin {
		if err := env.SetCredential(ctx, markerJellyfinLogin, loginDigest("jellyfin", login)); err != nil {
			return false, err
		}
	}

	stored, _, err := env.LookupCredential(ctx, CredentialJellyfinAPIKey)
	if err != nil {
		return false, err
	}
	key, err := jellyfinFindKey(ctx, session, stored)
	if err != nil {
		return false, err
	}
	created := false
	if key == "" {
		reply, err := session.call(ctx, http.MethodPost, "/Auth/Keys", url.Values{"app": {jellyfinKeyName}}, nil)
		if err != nil {
			return false, err
		}
		if reply.Status != http.StatusNoContent {
			return false, reply.unexpected("Jellyfin did not create an API key", login, token)
		}
		// Creating a key answers with nothing; the key is read from the list.
		if key, err = jellyfinFindKey(ctx, session, ""); err != nil {
			return false, err
		}
		if key == "" {
			return false, errors.New("Jellyfin created an API key but does not list it")
		}
		created = true
	}
	works, err := jellyfinKeyWorks(ctx, env, api.base, key)
	if err != nil {
		return false, err
	}
	if !works {
		return false, errors.New("Jellyfin lists the stack's API key but does not accept it")
	}
	if key != stored {
		if err := env.SetCredential(ctx, CredentialJellyfinAPIKey, key); err != nil {
			return false, err
		}
	}
	return created, nil
}

// jellyfinLogin brings the administrator's password to the stack's login,
// with the API key as an administrator changes anyone's. Whether it is the
// login already is kept as a marker, so a pass that changes nothing tries
// no password; when the marker does not say, one sign-in does, and that
// sign-in also clears Jellyfin's count of refused ones. It reports whether it
// changed the password.
func jellyfinLogin(ctx context.Context, env stacks.Env, base, key, login string) (bool, error) {
	want := loginDigest("jellyfin", login)
	applied, _, err := env.LookupCredential(ctx, markerJellyfinLogin)
	if err != nil {
		return false, err
	}
	if applied == want {
		return false, nil
	}
	api := &jellyfinAPI{env: env, base: base}
	previous, _, err := env.LookupCredential(ctx, CredentialLoginPrevious)
	if err != nil {
		return false, err
	}
	// Known to be on the previous login: the probe would only be refused.
	if previous == "" || applied != loginDigest("jellyfin", previous) {
		token, signedIn, err := jellyfinSignIn(ctx, api, login)
		if err != nil {
			return false, err
		}
		if signedIn {
			session := &jellyfinAPI{env: env, base: base, token: token}
			_, _ = session.call(context.WithoutCancel(ctx), http.MethodPost, "/Sessions/Logout", nil, nil)
			return false, env.SetCredential(ctx, markerJellyfinLogin, want)
		}
	}
	admin := &jellyfinAPI{env: env, base: base, token: key}
	id, err := jellyfinAdminID(ctx, admin)
	if err != nil {
		return false, err
	}
	reply, err := admin.call(ctx, http.MethodPost, "/Users/Password", url.Values{"userId": {id}},
		map[string]any{"CurrentPw": "", "NewPw": login, "ResetPassword": false})
	if err != nil {
		return false, err
	}
	if reply.Status != http.StatusNoContent {
		return false, reply.unexpected("Jellyfin did not take the stack's password for "+AdminUsername, login, key)
	}
	token, signedIn, err := jellyfinSignIn(ctx, api, login)
	if err != nil {
		return false, err
	}
	if !signedIn {
		return false, errors.New("Jellyfin took the stack's password for " + AdminUsername + " but does not sign in with it")
	}
	session := &jellyfinAPI{env: env, base: base, token: token}
	_, _ = session.call(context.WithoutCancel(ctx), http.MethodPost, "/Sessions/Logout", nil, nil)
	env.Logf("gave Jellyfin's administrator %s the stack's password; its other devices sign in again", AdminUsername)
	return true, env.SetCredential(ctx, markerJellyfinLogin, want)
}

// jellyfinAdminID is the id of the account named admin.
func jellyfinAdminID(ctx context.Context, admin *jellyfinAPI) (string, error) {
	reply, err := admin.call(ctx, http.MethodGet, "/Users", nil, nil)
	if err != nil {
		return "", err
	}
	if reply.Status != http.StatusOK {
		return "", reply.unexpected("Jellyfin did not list its accounts", admin.token)
	}
	var users []struct {
		ID   string `json:"Id"`
		Name string `json:"Name"`
	}
	if err := reply.decode(&users); err != nil {
		return "", err
	}
	for _, user := range users {
		if user.Name == AdminUsername && user.ID != "" {
			return user.ID, nil
		}
	}
	return "", stacks.Permanent(fmt.Errorf("Jellyfin has no account named %s", AdminUsername))
}

// jellyfinWizard is the first-run wizard's own requests, in its order. Each
// is safe to send again after an attempt that stopped halfway.
func jellyfinWizard(ctx context.Context, api *jellyfinAPI, password string) error {
	// Reading the first user is what makes Jellyfin create it; the wizard's
	// page does the same before it posts a name.
	reply, err := api.call(ctx, http.MethodGet, "/Startup/User", nil, nil)
	if err != nil {
		return err
	}
	if reply.Status != http.StatusOK {
		return reply.unexpected("Jellyfin's first-run setup did not offer its first user")
	}
	for _, step := range []struct {
		path string
		body any
		what string
	}{
		{"/Startup/User", map[string]any{"Name": AdminUsername, "Password": password}, "create the administrator"},
		// Remote access on: the deployment's domain reaches Jellyfin through
		// the proxy, which is a remote address to it. 12.4's Startup API has
		// no port-mapping field, and UPnP is off unless somebody turns it on.
		{"/Startup/RemoteAccess", map[string]any{"EnableRemoteAccess": true}, "allow remote access"},
		{"/Startup/Complete", nil, "finish its first-run setup"},
	} {
		reply, err := api.call(ctx, http.MethodPost, step.path, nil, step.body)
		if err != nil {
			return err
		}
		if reply.Status != http.StatusNoContent {
			return reply.unexpected("Jellyfin did not "+step.what, password)
		}
	}
	return nil
}

// jellyfinSignIn returns a session token, or false when Jellyfin refuses the
// name and password.
func jellyfinSignIn(ctx context.Context, api *jellyfinAPI, password string) (string, bool, error) {
	reply, err := api.call(ctx, http.MethodPost, "/Users/AuthenticateByName", nil, map[string]any{"Username": AdminUsername, "Pw": password})
	if err != nil {
		return "", false, err
	}
	switch reply.Status {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return "", false, nil
	default:
		return "", false, reply.unexpected("Jellyfin did not answer the sign-in", password)
	}
	var result struct {
		AccessToken string `json:"AccessToken"`
		User        struct {
			Policy struct {
				IsAdministrator bool `json:"IsAdministrator"`
			} `json:"Policy"`
		} `json:"User"`
	}
	if err := reply.decode(&result); err != nil {
		return "", false, err
	}
	if result.AccessToken == "" {
		return "", false, errors.New("Jellyfin signed in without giving a session")
	}
	if !result.User.Policy.IsAdministrator {
		// An account named admin that is not an administrator is somebody
		// else's setup too.
		probe := &jellyfinAPI{env: api.env, base: api.base, token: result.AccessToken}
		_, _ = probe.call(context.WithoutCancel(ctx), http.MethodPost, "/Sessions/Logout", nil, nil)
		return "", false, nil
	}
	return result.AccessToken, true, nil
}

// jellyfinFindKey is the stack's key from Jellyfin's list: the stored one if
// it is still listed, else any under the stack's name, else "".
func jellyfinFindKey(ctx context.Context, session *jellyfinAPI, stored string) (string, error) {
	reply, err := session.call(ctx, http.MethodGet, "/Auth/Keys", nil, nil)
	if err != nil {
		return "", err
	}
	if reply.Status != http.StatusOK {
		return "", reply.unexpected("Jellyfin did not list its API keys", session.token)
	}
	var list struct {
		Items []jellyfinKey `json:"Items"`
	}
	if err := reply.decode(&list); err != nil {
		return "", err
	}
	found := ""
	for _, item := range list.Items {
		if item.AppName != jellyfinKeyName || item.AccessToken == "" {
			continue
		}
		if item.AccessToken == stored {
			return stored, nil
		}
		if found == "" {
			found = item.AccessToken
		}
	}
	return found, nil
}

// jellyfinKeyWorks asks for something only an administrator's key may read.
func jellyfinKeyWorks(ctx context.Context, env stacks.Env, base, key string) (bool, error) {
	api := &jellyfinAPI{env: env, base: base, token: key}
	reply, err := api.call(ctx, http.MethodGet, "/Auth/Keys", nil, nil)
	if err != nil {
		return false, err
	}
	switch reply.Status {
	case http.StatusOK:
		return true, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return false, nil
	}
	return false, reply.unexpected("Jellyfin did not answer the stack's API key", key)
}

func jellyfinTakenOver() error {
	return stacks.NeedsAction(stacks.Action{
		ID:      actionJellyfinTakenOver,
		Service: "jellyfin",
		Title:   "Jellyfin was set up by someone else",
		Description: "Jellyfin's first-run setup is already finished, and the stack's administrator does not sign in. " +
			"Nothing was changed. In Jellyfin, create an administrator named " + AdminUsername + " (or change that account's password) " +
			"with the stack's password shown on this page, then run the setup again. " +
			"Each run before that is one refused sign-in for " + AdminUsername + ", and Jellyfin locks an account after several.",
		Blocking: true,
	})
}

type jellyfinFolder struct {
	Name           string   `json:"Name"`
	Locations      []string `json:"Locations"`
	CollectionType string   `json:"CollectionType"`
	ItemID         string   `json:"ItemId"`
}

// jellyfinLibraries makes the Movies and Shows libraries over the stack's
// two library folders. A library that already has the folder is the stack's,
// whatever it is called; none is ever removed or renamed.
func jellyfinLibraries(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	base, err := env.BaseURL(ctx, "jellyfin", PortJellyfin)
	if err != nil {
		return stacks.Result{}, err
	}
	key, found, err := env.LookupCredential(ctx, CredentialJellyfinAPIKey)
	if err != nil {
		return stacks.Result{}, err
	}
	if !found || key == "" {
		return stacks.Result{}, errors.New("the stack has no Jellyfin API key yet")
	}
	api := &jellyfinAPI{env: env, base: base, token: key}
	folders, err := jellyfinFolders(ctx, api)
	if err != nil {
		return stacks.Result{}, err
	}
	var made []string
	for _, wanted := range jellyfinLibrariesWanted {
		if _, found := jellyfinFolderFor(folders, wanted.path); found {
			continue
		}
		reply, err := api.call(ctx, http.MethodPost, "/Library/VirtualFolders", url.Values{
			"name":           {wanted.name},
			"collectionType": {wanted.kind},
			"paths":          {wanted.path},
			"refreshLibrary": {"true"},
		}, map[string]any{"LibraryOptions": map[string]any{
			// Radarr and Sonarr move a finished download in at any time;
			// watching the folder shows it without waiting for a scan.
			"EnableRealtimeMonitor": true,
			"PathInfos":             []map[string]string{{"Path": wanted.path}},
		}})
		if err != nil {
			return stacks.Result{}, err
		}
		if reply.Status != http.StatusNoContent {
			return stacks.Result{}, reply.unexpected(fmt.Sprintf("Jellyfin did not create the %s library at %s", wanted.name, wanted.path), key)
		}
		made = append(made, wanted.name)
		env.Logf("created the %s library at %s", wanted.name, wanted.path)
	}
	if len(made) > 0 {
		if folders, err = jellyfinFolders(ctx, api); err != nil {
			return stacks.Result{}, err
		}
	}
	outputs := map[string]string{}
	message := ""
	for _, wanted := range jellyfinLibrariesWanted {
		folder, found := jellyfinFolderFor(folders, wanted.path)
		if !found {
			return stacks.Result{}, fmt.Errorf("Jellyfin has no library at %s after it was created", wanted.path)
		}
		if folder.ItemID == "" {
			return stacks.Result{}, fmt.Errorf("Jellyfin has not given the library at %s an id yet", wanted.path)
		}
		if folder.CollectionType != wanted.kind {
			env.Logf("the library %q at %s is of type %q, not %q; left as it is", folder.Name, wanted.path, folder.CollectionType, wanted.kind)
		}
		outputs[wanted.output] = folder.ItemID
		if message != "" {
			message += ", "
		}
		message += folder.Name + " at " + wanted.path
	}
	return stacks.Result{Message: message, Outputs: outputs}, nil
}

func jellyfinFolders(ctx context.Context, api *jellyfinAPI) ([]jellyfinFolder, error) {
	reply, err := api.call(ctx, http.MethodGet, "/Library/VirtualFolders", nil, nil)
	if err != nil {
		return nil, err
	}
	if reply.Status != http.StatusOK {
		return nil, reply.unexpected("Jellyfin did not list its libraries", api.token)
	}
	var folders []jellyfinFolder
	if err := reply.decode(&folders); err != nil {
		return nil, err
	}
	return folders, nil
}

func jellyfinFolderFor(folders []jellyfinFolder, path string) (jellyfinFolder, bool) {
	for _, folder := range folders {
		for _, location := range folder.Locations {
			if samePath(location, path) {
				return folder, true
			}
		}
	}
	return jellyfinFolder{}, false
}

// jellyfinProxy has Jellyfin trust the proxy in front of it: the networks
// its container is on become known proxies, so it reads each visitor's own
// address from the proxy rather than seeing every one of them as the proxy,
// on this server's own network, which it would count as local. Proxies the
// person added stay. Jellyfin applies the network settings when they are
// saved, without a restart.
func jellyfinProxy(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	base, err := env.BaseURL(ctx, "jellyfin", PortJellyfin)
	if err != nil {
		return stacks.Result{}, err
	}
	key, found, err := env.LookupCredential(ctx, CredentialJellyfinAPIKey)
	if err != nil {
		return stacks.Result{}, err
	}
	if !found || key == "" {
		return stacks.Result{}, errors.New("the stack has no Jellyfin API key yet")
	}
	subnets, err := env.NetworkSubnets(ctx, "jellyfin")
	if err != nil {
		return stacks.Result{}, err
	}
	api := &jellyfinAPI{env: env, base: base, token: key}
	read := func() (map[string]any, error) {
		reply, err := api.call(ctx, http.MethodGet, "/System/Configuration/network", nil, nil)
		if err != nil {
			return nil, err
		}
		if reply.Status != http.StatusOK {
			return nil, reply.unexpected("Jellyfin did not give its network settings", key)
		}
		network := map[string]any{}
		return network, reply.decode(&network)
	}
	network, err := read()
	if err != nil {
		return stacks.Result{}, err
	}
	known, missing := jellyfinProxies(network, subnets)
	if len(missing) > 0 {
		// The whole settings object goes back: Jellyfin replaces it.
		network["KnownProxies"] = append(known, missing...)
		reply, err := api.call(ctx, http.MethodPost, "/System/Configuration/network", nil, network)
		if err != nil {
			return stacks.Result{}, err
		}
		if reply.Status != http.StatusNoContent {
			return stacks.Result{}, reply.unexpected("Jellyfin did not save its known proxies", key)
		}
		if network, err = read(); err != nil {
			return stacks.Result{}, err
		}
		if _, still := jellyfinProxies(network, subnets); len(still) > 0 {
			return stacks.Result{}, fmt.Errorf("Jellyfin's known proxies read back without %s", strings.Join(still, ", "))
		}
		env.Logf("Jellyfin trusts the proxy at %s", strings.Join(missing, ", "))
	}
	return stacks.Result{Message: "Jellyfin reads each visitor's address from the proxy at " + strings.Join(subnets, ", ")}, nil
}

// jellyfinProxies is Jellyfin's known proxies and the subnets not among them.
func jellyfinProxies(network map[string]any, subnets []string) (known, missing []string) {
	listed, _ := network["KnownProxies"].([]any)
	for _, entry := range listed {
		if text, ok := entry.(string); ok && text != "" {
			known = append(known, text)
		}
	}
	for _, subnet := range subnets {
		if !slices.Contains(known, subnet) {
			missing = append(missing, subnet)
		}
	}
	return known, missing
}
