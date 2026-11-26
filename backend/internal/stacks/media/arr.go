package media

// The Servarr applications (Radarr, Sonarr and Prowlarr) share one HTTP API
// shape: /api/v3 (v1 in Prowlarr), an X-Api-Key header, a host configuration
// resource, and "providers" (download clients, applications) that are a
// name, an implementation and a list of fields. This file is that shared
// client and the three operations every one of them is configured with.
//
// Every request here is in the application's own OpenAPI document, which
// each repository keeps beside its API project at the tag of the pinned
// version (src/Radarr.Api.V3/openapi.json, src/Sonarr.Api.V3/openapi.json,
// src/Prowlarr.Api.V1/openapi.json), and was run against the pinned image.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
)

// arrApp is one Servarr application as the stack knows it.
type arrApp struct {
	service string
	title   string
	port    int
	// api is the version segment of its API: "v3", or "v1" in Prowlarr.
	api string
	// keyVariable is the deployment variable holding the API key the
	// template generated and handed to the application as
	// <APP>__AUTH__APIKEY; adminCredential the stack's own sign-in for it.
	keyVariable     string
	adminCredential string
}

var (
	arrRadarr   = arrApp{service: "radarr", title: "Radarr", port: PortRadarr, api: "v3", keyVariable: VariableRadarrAPIKey, adminCredential: CredentialRadarrAdmin}
	arrSonarr   = arrApp{service: "sonarr", title: "Sonarr", port: PortSonarr, api: "v3", keyVariable: VariableSonarrAPIKey, adminCredential: CredentialSonarrAdmin}
	arrProwlarr = arrApp{service: "prowlarr", title: "Prowlarr", port: PortProwlarr, api: "v1", keyVariable: VariableProwlarrAPIKey, adminCredential: CredentialProwlarrAdmin}
)

// The names of what the stack makes inside the applications. A step finds
// its own entry by this name and never touches one named otherwise.
const (
	arrDownloadClientName = "qBittorrent (Just Dashboard)"
	arrOwnedSuffix        = " (Just Dashboard)"
)

// arrReplyLimit bounds what is read of one answer. The largest the stack
// asks for is a provider schema, a few hundred kilobytes.
const arrReplyLimit = 8 << 20

// arrClient talks to one application with its API key.
type arrClient struct {
	env  stacks.Env
	app  arrApp
	base string
	key  string
}

func newArrClient(ctx context.Context, env stacks.Env, app arrApp) (*arrClient, error) {
	key, err := env.Variable(ctx, app.keyVariable)
	if err != nil {
		return nil, err
	}
	base, err := env.BaseURL(ctx, app.service, app.port)
	if err != nil {
		return nil, err
	}
	return &arrClient{env: env, app: app, base: base, key: key}, nil
}

// arrProblem is an answer that was not a success: the status and what the
// application said about it, in its own words. The applications answer a
// refused write with a list of validation failures and anything else with
// one message.
type arrProblem struct {
	App     string
	Method  string
	Path    string
	Status  int
	Details []string
}

func (p *arrProblem) Error() string {
	what := fmt.Sprintf("%s answered %d to %s %s", p.App, p.Status, p.Method, p.Path)
	if len(p.Details) == 0 {
		return what
	}
	return what + ": " + strings.Join(p.Details, "; ")
}

// arrProblemDetails reads the two shapes a failure comes in.
func arrProblemDetails(body []byte) []string {
	var failures []struct {
		PropertyName        string `json:"propertyName"`
		ErrorMessage        string `json:"errorMessage"`
		DetailedDescription string `json:"detailedDescription"`
	}
	if json.Unmarshal(body, &failures) == nil && len(failures) > 0 {
		details := make([]string, 0, len(failures))
		for _, failure := range failures {
			text := failure.ErrorMessage
			if failure.DetailedDescription != "" && failure.DetailedDescription != failure.ErrorMessage {
				text += " (" + failure.DetailedDescription + ")"
			}
			if text != "" {
				details = append(details, text)
			}
		}
		return details
	}
	var single struct {
		Message string `json:"message"`
		Title   string `json:"title"`
	}
	if json.Unmarshal(body, &single) == nil {
		if single.Message != "" {
			return []string{single.Message}
		}
		if single.Title != "" {
			return []string{single.Title}
		}
	}
	return nil
}

// call makes one API request. A body is sent as JSON; out, when set, is
// decoded from a successful answer. Any status outside 2xx is an
// *arrProblem. The key travels in a header, so neither the URL nor an error
// built from it can hold it.
func (c *arrClient) call(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	apiPath := "/api/" + c.app.api + "/" + strings.TrimLeft(path, "/")
	request, err := http.NewRequestWithContext(ctx, method, stacks.JoinURL(c.base, apiPath, nil), reader)
	if err != nil {
		return err
	}
	request.Header.Set("X-Api-Key", c.key)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.env.HTTP().Do(request)
	if err != nil {
		return fmt.Errorf("%s did not answer: %w", c.app.title, err)
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(response.Body, arrReplyLimit))
	if err != nil {
		return fmt.Errorf("%s's answer could not be read: %w", c.app.title, err)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		problem := &arrProblem{App: c.app.title, Method: method, Path: apiPath, Status: response.StatusCode, Details: arrProblemDetails(answer)}
		if response.StatusCode == http.StatusUnauthorized {
			// The key is the deployment's own, handed to the application as
			// its environment. One that is refused will be refused again.
			return stacks.Permanent(fmt.Errorf("%s does not accept the API key the deployment gave it (%s): %w", c.app.title, c.app.keyVariable, problem))
		}
		return problem
	}
	if out == nil || len(bytes.TrimSpace(answer)) == 0 {
		return nil
	}
	if err := json.Unmarshal(answer, out); err != nil {
		return fmt.Errorf("%s's answer to %s %s was not what its API documents: %w", c.app.title, method, apiPath, err)
	}
	return nil
}

// arrStatus is the part of GET system/status the stack reads.
type arrStatus struct {
	AppName string `json:"appName"`
	Version string `json:"version"`
}

// signIn proves a user name and password through the application's own
// sign-in form (POST /login, the form its page posts). The applications
// answer 302 either way: to the page asked for with a session cookie, or
// back to /login with loginFailed=true.
func (c *arrClient) signIn(ctx context.Context, username, password string) (bool, error) {
	form := url.Values{"username": {username}, "password": {password}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, stacks.JoinURL(c.base, "/login", nil), strings.NewReader(form.Encode()))
	if err != nil {
		return false, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// The redirect is the answer, so it is not followed.
	client := *c.env.HTTP()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return false, fmt.Errorf("%s did not answer its sign-in: %w", c.app.title, err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, arrReplyLimit))
	if response.StatusCode != http.StatusFound && response.StatusCode != http.StatusSeeOther {
		return false, fmt.Errorf("%s answered %d to its sign-in form", c.app.title, response.StatusCode)
	}
	if strings.Contains(response.Header.Get("Location"), "loginFailed") {
		return false, nil
	}
	return len(response.Cookies()) > 0, nil
}

// arrAuth waits for the application to answer with the key from its
// environment, then gives it a sign-in form asked of every address, with
// the stack's own administrator. It writes only when the application is not
// already so, or no longer lets that administrator in.
func arrAuth(ctx context.Context, env stacks.Env, app arrApp) (stacks.Result, error) {
	client, err := newArrClient(ctx, env, app)
	if err != nil {
		return stacks.Result{}, err
	}
	var status arrStatus
	if err := client.call(ctx, http.MethodGet, "system/status", nil, &status); err != nil {
		return stacks.Result{}, err
	}
	password, err := env.Credential(ctx, app.adminCredential)
	if err != nil {
		return stacks.Result{}, err
	}

	host, err := client.hostConfig(ctx)
	if err != nil {
		return stacks.Result{}, err
	}
	secured := arrHostSecured(host)
	if secured {
		// The password is kept hashed, so only the form can say whether it
		// is still the stack's.
		if secured, err = client.signIn(ctx, AdminUsername, password); err != nil {
			return stacks.Result{}, err
		}
	}
	outputs := map[string]string{"version": status.Version}
	if secured {
		return stacks.Result{
			Message: fmt.Sprintf("%s %s already asks every address to sign in, as %s", app.title, status.Version, AdminUsername),
			Outputs: outputs,
		}, nil
	}

	id, found := arrID(host)
	if !found {
		return stacks.Result{}, fmt.Errorf("%s's host configuration has no id", app.title)
	}
	// The whole resource goes back with four fields changed: the API takes
	// no partial update, and everything else in it is the person's.
	host["authenticationMethod"] = "forms"
	host["authenticationRequired"] = "enabled"
	host["username"] = AdminUsername
	host["password"] = password
	host["passwordConfirmation"] = password
	if err := client.call(ctx, http.MethodPut, "config/host/"+strconv.Itoa(id), host, nil); err != nil {
		return stacks.Result{}, err
	}

	host, err = client.hostConfig(ctx)
	if err != nil {
		return stacks.Result{}, err
	}
	if !arrHostSecured(host) {
		return stacks.Result{}, fmt.Errorf("%s took the sign-in settings but does not report them back", app.title)
	}
	signedIn, err := client.signIn(ctx, AdminUsername, password)
	if err != nil {
		return stacks.Result{}, err
	}
	if !signedIn {
		return stacks.Result{}, fmt.Errorf("%s does not let %s in with the password it was just given", app.title, AdminUsername)
	}
	env.Logf("%s now asks for a sign-in on every address", app.title)
	return stacks.Result{
		Message: fmt.Sprintf("%s %s asks every address to sign in, as %s", app.title, status.Version, AdminUsername),
		Outputs: outputs,
	}, nil
}

func (c *arrClient) hostConfig(ctx context.Context) (map[string]any, error) {
	var host map[string]any
	if err := c.call(ctx, http.MethodGet, "config/host", nil, &host); err != nil {
		return nil, err
	}
	if host == nil {
		return nil, fmt.Errorf("%s returned no host configuration", c.app.title)
	}
	return host, nil
}

// arrHostSecured reports whether the host configuration is a sign-in form,
// for the stack's administrator, with no exception for local addresses.
func arrHostSecured(host map[string]any) bool {
	method, _ := host["authenticationMethod"].(string)
	required, _ := host["authenticationRequired"].(string)
	username, _ := host["username"].(string)
	return strings.EqualFold(method, "forms") && strings.EqualFold(required, "enabled") && username == AdminUsername
}

func arrID(resource map[string]any) (int, bool) {
	// encoding/json reads every number into a float64.
	id, found := resource["id"].(float64)
	return int(id), found && id > 0
}

// arrRootFolderEntry is a root folder as GET rootfolder lists it.
type arrRootFolderEntry struct {
	ID         int    `json:"id"`
	Path       string `json:"path"`
	Accessible bool   `json:"accessible"`
	FreeSpace  *int64 `json:"freeSpace"`
}

// arrRootFolder makes path one of the application's root folders when it is
// not one already, and has the application confirm it can use it.
func arrRootFolder(ctx context.Context, env stacks.Env, app arrApp, library, path string) (stacks.Result, error) {
	client, err := newArrClient(ctx, env, app)
	if err != nil {
		return stacks.Result{}, err
	}
	folder, found, err := client.rootFolder(ctx, path)
	if err != nil {
		return stacks.Result{}, err
	}
	added := false
	if !found {
		if err := client.call(ctx, http.MethodPost, "rootfolder", map[string]string{"path": path}, nil); err != nil {
			return stacks.Result{}, err
		}
		added = true
		if folder, found, err = client.rootFolder(ctx, path); err != nil {
			return stacks.Result{}, err
		}
		if !found {
			return stacks.Result{}, fmt.Errorf("%s took %s as a root folder but does not list it", app.title, path)
		}
		env.Logf("%s now has %s as a root folder", app.title, path)
	}
	if !folder.Accessible {
		return stacks.Result{}, fmt.Errorf("%s cannot use %s: it reports the folder as not accessible", app.title, path)
	}
	if folder.FreeSpace == nil {
		return stacks.Result{}, fmt.Errorf("%s reports no free space for %s", app.title, path)
	}
	verb := "already keeps"
	if added {
		verb = "keeps"
	}
	return stacks.Result{
		Message: fmt.Sprintf("%s %s %s in %s (%s free)", app.title, verb, library, path, arrSize(*folder.FreeSpace)),
		Outputs: map[string]string{"path": folder.Path, "id": strconv.Itoa(folder.ID)},
	}, nil
}

func (c *arrClient) rootFolder(ctx context.Context, path string) (arrRootFolderEntry, bool, error) {
	var folders []arrRootFolderEntry
	if err := c.call(ctx, http.MethodGet, "rootfolder", nil, &folders); err != nil {
		return arrRootFolderEntry{}, false, err
	}
	for _, folder := range folders {
		if strings.TrimRight(folder.Path, "/") == strings.TrimRight(path, "/") {
			return folder, true, nil
		}
	}
	return arrRootFolderEntry{}, false, nil
}

func arrSize(bytes int64) string {
	const gigabyte = 1 << 30
	if bytes >= gigabyte {
		return fmt.Sprintf("%.0f GB", float64(bytes)/gigabyte)
	}
	return fmt.Sprintf("%.0f MB", float64(bytes)/(1<<20))
}

// arrProvider is one entry the stack keeps in an application's list of
// download clients or applications.
type arrProvider struct {
	// resource is the list it lives in: "downloadclient", "applications".
	resource string
	// what names it in a message: "download client", "application".
	what           string
	name           string
	implementation string
	// top are properties of the entry itself; fields its settings by name.
	// secrets are settings the application never reads back (it answers
	// "********"), so they cannot be compared, only tested.
	top     map[string]any
	fields  map[string]any
	secrets map[string]string
}

// ensureProvider makes the entry exist with the stack's settings. A new one
// is taken from the application's own schema, so every setting the stack
// does not name keeps the application's default; an existing one keeps
// whatever else the person set on it. Either way the application's own test
// runs before anything is saved. Entries under any other name are not read
// past their name.
func (c *arrClient) ensureProvider(ctx context.Context, want arrProvider) (changed bool, id int, err error) {
	var entries []map[string]any
	if err := c.call(ctx, http.MethodGet, want.resource, nil, &entries); err != nil {
		return false, 0, err
	}
	var entry map[string]any
	for _, candidate := range entries {
		if name, _ := candidate["name"].(string); name == want.name {
			entry = candidate
			break
		}
	}

	if entry == nil {
		var schema []map[string]any
		if err := c.call(ctx, http.MethodGet, want.resource+"/schema", nil, &schema); err != nil {
			return false, 0, err
		}
		for _, candidate := range schema {
			if implementation, _ := candidate["implementation"].(string); implementation == want.implementation {
				entry = candidate
				break
			}
		}
		if entry == nil {
			return false, 0, stacks.Permanent(fmt.Errorf("%s has no %s of the kind %s", c.app.title, want.what, want.implementation))
		}
		delete(entry, "presets")
		entry["name"] = want.name
		if err := want.apply(entry, true); err != nil {
			return false, 0, stacks.Permanent(fmt.Errorf("%s: %w", c.app.title, err))
		}
		if err := c.testProvider(ctx, want, entry); err != nil {
			return false, 0, err
		}
		var created map[string]any
		if err := c.call(ctx, http.MethodPost, want.resource, entry, &created); err != nil {
			return false, 0, err
		}
		id, found := arrID(created)
		if !found {
			return false, 0, fmt.Errorf("%s saved the %s %q but gave it no id", c.app.title, want.what, want.name)
		}
		return true, id, c.confirmProvider(ctx, want, id)
	}

	id, found := arrID(entry)
	if !found {
		return false, 0, fmt.Errorf("%s lists the %s %q without an id", c.app.title, want.what, want.name)
	}
	if implementation, _ := entry["implementation"].(string); implementation != want.implementation {
		return false, 0, stacks.Permanent(fmt.Errorf("%s has a %s named %q that is %s, not %s; rename or remove it and run this again",
			c.app.title, want.what, want.name, implementation, want.implementation))
	}
	if want.matches(entry) {
		// Everything readable is right. Tested as stored, it also proves
		// the secret the application will not show is still the right one.
		if c.testProvider(ctx, want, entry) == nil {
			return false, id, nil
		}
	}
	if err := want.apply(entry, true); err != nil {
		return false, 0, stacks.Permanent(fmt.Errorf("%s: %w", c.app.title, err))
	}
	if err := c.testProvider(ctx, want, entry); err != nil {
		return false, 0, err
	}
	if err := c.call(ctx, http.MethodPut, want.resource+"/"+strconv.Itoa(id), entry, nil); err != nil {
		return false, 0, err
	}
	return true, id, c.confirmProvider(ctx, want, id)
}

// arrTestFailure is the application's own test of an entry saying no, with
// its reason.
type arrTestFailure struct {
	App, What, Name, Reason string
}

func (f *arrTestFailure) Error() string {
	return fmt.Sprintf("%s's test of the %s %q failed: %s", f.App, f.What, f.Name, f.Reason)
}

// testProvider is the application's own test of an entry, which answers 2xx
// when it could use it and 400 with the reason when not.
func (c *arrClient) testProvider(ctx context.Context, want arrProvider, entry map[string]any) error {
	err := c.call(ctx, http.MethodPost, want.resource+"/test", entry, nil)
	var problem *arrProblem
	if errors.As(err, &problem) && problem.Status == http.StatusBadRequest {
		reason := strings.Join(problem.Details, "; ")
		if reason == "" {
			reason = "it gave no reason"
		}
		return &arrTestFailure{App: c.app.title, What: want.what, Name: want.name, Reason: reason}
	}
	return err
}

func (c *arrClient) confirmProvider(ctx context.Context, want arrProvider, id int) error {
	var saved map[string]any
	if err := c.call(ctx, http.MethodGet, want.resource+"/"+strconv.Itoa(id), nil, &saved); err != nil {
		return err
	}
	if !want.matches(saved) {
		return fmt.Errorf("%s saved the %s %q but reads it back differently", c.app.title, want.what, want.name)
	}
	return nil
}

// apply writes the stack's settings into an entry, with the secrets when
// asked. A setting the application's schema does not have is an error: the
// pinned version is the one these names were read from.
func (p arrProvider) apply(entry map[string]any, withSecrets bool) error {
	for name, value := range p.top {
		entry[name] = value
	}
	fields, _ := entry["fields"].([]any)
	wanted := map[string]any{}
	for name, value := range p.fields {
		wanted[name] = value
	}
	if withSecrets {
		for name, value := range p.secrets {
			wanted[name] = value
		}
	}
	for _, raw := range fields {
		field, _ := raw.(map[string]any)
		name, _ := field["name"].(string)
		if value, found := wanted[name]; found {
			field["value"] = value
			delete(wanted, name)
		}
	}
	for name := range wanted {
		return fmt.Errorf("the %s %s has no setting named %s in this version", p.what, p.implementation, name)
	}
	return nil
}

// matches reports whether everything the application reads back is what the
// stack wants. Secrets are not part of it.
func (p arrProvider) matches(entry map[string]any) bool {
	for name, value := range p.top {
		if !arrSameValue(entry[name], value) {
			return false
		}
	}
	current := map[string]any{}
	fields, _ := entry["fields"].([]any)
	for _, raw := range fields {
		field, _ := raw.(map[string]any)
		if name, _ := field["name"].(string); name != "" {
			current[name] = field["value"]
		}
	}
	for name, value := range p.fields {
		if !arrSameValue(current[name], value) {
			return false
		}
	}
	return true
}

// arrSameValue compares a decoded JSON value with a Go one: 8080 is 8080
// whether it arrived as a float64 or was written as an int.
func arrSameValue(got, want any) bool {
	left, err := json.Marshal(got)
	if err != nil {
		return false
	}
	right, err := json.Marshal(want)
	if err != nil {
		return false
	}
	return bytes.Equal(left, right)
}

// arrDownloadClient connects an application to the stack's qBittorrent,
// under the stack's own name for it.
func arrDownloadClient(ctx context.Context, env stacks.Env, app arrApp, categoryField, category string) (stacks.Result, error) {
	client, err := newArrClient(ctx, env, app)
	if err != nil {
		return stacks.Result{}, err
	}
	password, err := env.Credential(ctx, CredentialQBittorrent)
	if err != nil {
		return stacks.Result{}, err
	}
	changed, id, err := client.ensureProvider(ctx, arrQBittorrent(categoryField, category, password))
	if err != nil {
		return stacks.Result{}, err
	}
	verb := "already sends"
	if changed {
		verb = "sends"
		env.Logf("%s's download client %q was saved after its own test passed", app.title, arrDownloadClientName)
	}
	return stacks.Result{
		Message: fmt.Sprintf("%s %s downloads to qBittorrent in the %s category", app.title, verb, category),
		Outputs: map[string]string{"id": strconv.Itoa(id), "name": arrDownloadClientName, "category": category},
	}, nil
}

func arrQBittorrent(categoryField, category, password string) arrProvider {
	return arrProvider{
		resource:       "downloadclient",
		what:           "download client",
		name:           arrDownloadClientName,
		implementation: "QBittorrent",
		top:            map[string]any{"enable": true},
		fields: map[string]any{
			// Compose's name for the service: the address every container
			// of the stack resolves, whatever its container is recreated as.
			"host":        "qbittorrent",
			"port":        PortQBittorrent,
			"useSsl":      false,
			"username":    AdminUsername,
			categoryField: category,
		},
		secrets: map[string]string{"password": password},
	}
}

// arrHealthEntry is one line of GET health.
type arrHealthEntry struct {
	Source  string `json:"source"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

// health has the application run its health checks now and returns what
// they found. Without the command, GET health answers from the last run,
// which can be from before the stack configured anything.
func (c *arrClient) health(ctx context.Context) ([]arrHealthEntry, error) {
	var command struct {
		ID     int    `json:"id"`
		Status string `json:"status"`
	}
	if err := c.call(ctx, http.MethodPost, "command", map[string]string{"name": "CheckHealth"}, &command); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(45 * time.Second)
	for command.Status != "completed" {
		if command.Status == "failed" || command.Status == "aborted" || command.Status == "cancelled" || command.Status == "orphaned" {
			return nil, fmt.Errorf("%s's health check %s", c.app.title, command.Status)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%s had not finished its health check after 45 seconds", c.app.title)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(arrPollInterval):
		}
		if err := c.call(ctx, http.MethodGet, "command/"+strconv.Itoa(command.ID), nil, &command); err != nil {
			return nil, err
		}
	}
	var entries []arrHealthEntry
	if err := c.call(ctx, http.MethodGet, "health", nil, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// arrPollInterval is how often a running command is asked about. A variable
// so tests need not wait.
var arrPollInterval = time.Second

// downloadsVerify asks Radarr and Sonarr to test the stack's download
// client as they have it stored, then reads what their health checks say
// about downloading. A source being missing is the sources step's to say,
// so the indexer checks are left to it.
func downloadsVerify(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	var problems, notes []string
	for _, app := range []arrApp{arrRadarr, arrSonarr} {
		client, err := newArrClient(ctx, env, app)
		if err != nil {
			return stacks.Result{}, err
		}
		var entries []map[string]any
		if err := client.call(ctx, http.MethodGet, "downloadclient", nil, &entries); err != nil {
			return stacks.Result{}, err
		}
		var entry map[string]any
		for _, candidate := range entries {
			if name, _ := candidate["name"].(string); name == arrDownloadClientName {
				entry = candidate
			}
		}
		if entry == nil {
			problems = append(problems, fmt.Sprintf("%s has no download client named %q", app.title, arrDownloadClientName))
			continue
		}
		if err := client.testProvider(ctx, arrProvider{resource: "downloadclient", what: "download client", name: arrDownloadClientName}, entry); err != nil {
			var failure *arrTestFailure
			if !errors.As(err, &failure) {
				return stacks.Result{}, err
			}
			problems = append(problems, failure.Error())
			continue
		}
		health, err := client.health(ctx)
		if err != nil {
			return stacks.Result{}, err
		}
		for _, entry := range health {
			switch {
			case strings.HasPrefix(entry.Source, "Indexer"):
			case arrDownloadHealth(entry.Source) && entry.Type == "error":
				problems = append(problems, fmt.Sprintf("%s: %s", app.title, entry.Message))
			default:
				notes = append(notes, fmt.Sprintf("%s: %s", app.title, entry.Message))
			}
		}
	}
	if len(problems) > 0 {
		return stacks.Result{}, errors.New(strings.Join(problems, "; "))
	}
	message := "Radarr and Sonarr both reached qBittorrent with the stack's download client, and report no problem with downloading"
	if len(notes) > 0 {
		message += ". They also report: " + strings.Join(notes, "; ")
	}
	return stacks.Result{Message: message}, nil
}

// arrDownloadHealth reports whether a health check is about the download
// client or the path a finished download is imported from.
func arrDownloadHealth(source string) bool {
	return strings.HasPrefix(source, "DownloadClient") || strings.HasPrefix(source, "RemotePathMapping") || strings.HasPrefix(source, "ImportMechanism")
}
