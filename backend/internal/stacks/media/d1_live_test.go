package media

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks/stackstest"
)

// These tests start the pinned images themselves and run the storage,
// Jellyfin, qBittorrent and Seerr steps against them, as the runner would.
// They need Docker and root:
//
//	go test -c -o /tmp/media.test ./internal/stacks/media
//	sudo env JD_STACKS_LIVE=1 PATH=$PATH /tmp/media.test -test.run Live -test.v
//
// Everything they create is named jdms-d1-… and removed when they finish.

const (
	liveJellyfinImage    = "jellyfin/jellyfin:12.4"
	liveSeerrImage       = "ghcr.io/seerr-team/seerr:v3.5.2"
	liveQBittorrentImage = "lscr.io/linuxserver/qbittorrent:5.3.1_v2.1.3-ls490"
	liveRadarrImage      = "lscr.io/linuxserver/radarr:7.0.3.10918-ls328"
	liveSonarrImage      = "lscr.io/linuxserver/sonarr:4.0.20.3014-ls6"

	liveRadarrKey = "0123456789abcdef0123456789abcdef"
	liveSonarrKey = "fedcba9876543210fedcba9876543210"
)

// d1StepIDs are this file's steps in the manifest's order.
var d1StepIDs = []string{
	"storage.folders", "jellyfin.setup", "jellyfin.libraries", "qbittorrent.auth", "qbittorrent.categories",
	"seerr.setup", "seerr.managers", "seerr.policy", "seerr.verify",
}

func liveDocker(t *testing.T, args ...string) string {
	t.Helper()
	output, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

// liveStack is one network of containers and the environment over them.
type liveStack struct {
	t          *testing.T
	network    string
	containers map[string]string
	env        *recordingEnv
}

// recordingEnv is the production environment with every request the steps
// make written down, so a second pass can be held to making no change.
type recordingEnv struct {
	*stacks.DockerEnv
	mu       sync.Mutex
	requests []string
	client   *http.Client
}

type recordingTransport struct {
	next http.RoundTripper
	env  *recordingEnv
}

func (r recordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	r.env.mu.Lock()
	r.env.requests = append(r.env.requests, request.Method+" "+request.URL.Path)
	r.env.mu.Unlock()
	return r.next.RoundTrip(request)
}

func (e *recordingEnv) HTTP() *http.Client { return e.client }

func (e *recordingEnv) take() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	taken := e.requests
	e.requests = nil
	return taken
}

// startLiveStack creates the network, the shared media volume and one
// container per service, and removes them all when the test ends.
func startLiveStack(t *testing.T, name, preset string, services []string) *liveStack {
	t.Helper()
	if os.Getenv("JD_STACKS_LIVE") != "1" {
		t.Skip("set JD_STACKS_LIVE=1 to run against real containers")
	}
	stack := &liveStack{t: t, network: "jdms-d1-" + name, containers: map[string]string{}}
	volume := "jdms-d1-" + name + "-media"
	if name == "complete" {
		volume = "jdms-d1-media"
	}
	cleanup := func() {
		for _, service := range services {
			_ = exec.Command("docker", "rm", "-f", "-v", "jdms-d1-"+name+"-"+service).Run()
		}
		_ = exec.Command("docker", "network", "rm", stack.network).Run()
		_ = exec.Command("docker", "volume", "rm", "-f", volume).Run()
	}
	cleanup()
	t.Cleanup(cleanup)
	liveDocker(t, "network", "create", stack.network)
	liveDocker(t, "volume", "create", volume)
	for _, service := range services {
		container := "jdms-d1-" + name + "-" + service
		args := []string{"run", "-d", "--name", container, "--network", stack.network, "--network-alias", service, "-e", "TZ=Etc/UTC"}
		linuxserver := []string{"-e", "PUID=1000", "-e", "PGID=1000", "-v", volume + ":/data"}
		switch service {
		case "jellyfin":
			mount := volume + ":/data"
			if preset == "complete" {
				mount += ":ro"
			}
			args = append(args, "-v", mount, liveJellyfinImage)
		case "seerr":
			args = append(args, "-e", "LOG_LEVEL=info", liveSeerrImage)
		case "qbittorrent":
			args = append(append(args, linuxserver...), "-e", "WEBUI_PORT=8080", "-e", "TORRENTING_PORT=6881", liveQBittorrentImage)
		case "radarr":
			args = append(append(args, linuxserver...), "-e", "RADARR__AUTH__APIKEY="+liveRadarrKey, liveRadarrImage)
		case "sonarr":
			args = append(append(args, linuxserver...), "-e", "SONARR__AUTH__APIKEY="+liveSonarrKey, liveSonarrImage)
		default:
			t.Fatalf("no image for %s", service)
		}
		liveDocker(t, args...)
		stack.containers[service] = container
	}
	docker := stackstest.Live(t, Manifest(), stack.network, stack.containers, preset,
		map[string]string{OptionRequestApproval: "admin"},
		map[string]string{VariableRadarrAPIKey: liveRadarrKey, VariableSonarrAPIKey: liveSonarrKey})
	stack.env = &recordingEnv{DockerEnv: docker}
	client := stacks.StackHTTPClient()
	client.Transport = recordingTransport{next: client.Transport, env: stack.env}
	stack.env.client = client
	return stack
}

// waitReady is the readiness the deployment's health checks give the runner:
// each service answers its own health address.
func (s *liveStack) waitReady(ctx context.Context) {
	s.t.Helper()
	probes := map[string]struct {
		port int
		path string
	}{
		"jellyfin": {PortJellyfin, "/health"}, "seerr": {PortSeerr, "/api/v1/status"}, "qbittorrent": {PortQBittorrent, "/"},
		"radarr": {PortRadarr, "/ping"}, "sonarr": {PortSonarr, "/ping"},
	}
	deadline := time.Now().Add(4 * time.Minute)
	for service := range s.containers {
		probe := probes[service]
		for {
			base, err := s.env.BaseURL(ctx, service, probe.port)
			if err == nil {
				var reply appReply
				if reply, err = callApp(ctx, s.env, appCall{Method: http.MethodGet, URL: stacks.JoinURL(base, probe.path, nil)}); err == nil && reply.Status == http.StatusOK {
					break
				}
			}
			if time.Now().After(deadline) {
				s.t.Fatalf("%s was not ready in time: %v", service, err)
			}
			time.Sleep(2 * time.Second)
		}
	}
	s.env.take()
}

// run runs one step as the runner does: bounded attempts, no retry of a
// permanent failure or of an action.
func (s *liveStack) run(ctx context.Context, id string) (stacks.Result, error) {
	s.t.Helper()
	var step stacks.Step
	for _, candidate := range Manifest().Steps {
		if candidate.ID == id {
			step = candidate
		}
	}
	if step.Run == nil {
		s.t.Fatalf("no step %s", id)
	}
	for attempt := 1; ; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, step.Timeout)
		result, err := stackstest.Run(attemptCtx, s.env, step)
		cancel()
		var action *stacks.ActionError
		if err == nil || errors.As(err, &action) || stacks.IsPermanent(err) || attempt >= step.Attempts {
			return result, err
		}
		s.t.Logf("%s attempt %d: %v", id, attempt, err)
		time.Sleep(5 * time.Second)
	}
}

func (s *liveStack) mustRun(ctx context.Context, id string) stacks.Result {
	s.t.Helper()
	result, err := s.run(ctx, id)
	if err != nil {
		s.t.Fatalf("%s: %v", id, err)
	}
	s.t.Logf("%s: %s %v", id, result.Message, result.Outputs)
	return result
}

// managerCall is a test's own request to Radarr or Sonarr: the root folder
// and profile another lane's steps make in a real stack.
func (s *liveStack) managerCall(ctx context.Context, service string, port int, key, method, path string, body, target any) {
	s.t.Helper()
	base, err := s.env.BaseURL(ctx, service, port)
	if err != nil {
		s.t.Fatal(err)
	}
	reply, err := callApp(ctx, s.env, appCall{Method: method, URL: stacks.JoinURL(base, path, nil), Header: map[string]string{"X-Api-Key": key}, JSON: body})
	if err != nil || reply.Status >= 300 {
		s.t.Fatalf("%s %s %s: %v %d %s", service, method, path, err, reply.Status, reply.excerpt(key))
	}
	if target != nil {
		if err := reply.decode(target); err != nil {
			s.t.Fatal(err)
		}
	}
}

// liveSnapshot is everything the steps configure, read back through each
// application's own API with secrets left out.
func (s *liveStack) snapshot(ctx context.Context) string {
	s.t.Helper()
	state := map[string]any{}
	read := func(name string, reply appReply, err error) {
		s.t.Helper()
		if err != nil || reply.Status != http.StatusOK {
			s.t.Fatalf("snapshot %s: %v %d", name, err, reply.Status)
		}
		var value any
		if err := json.Unmarshal(reply.Body, &value); err != nil {
			value = string(reply.Body)
		}
		state[name] = value
	}
	if s.containers["jellyfin"] != "" {
		base, _ := s.env.BaseURL(ctx, "jellyfin", PortJellyfin)
		key, _, _ := s.env.LookupCredential(ctx, CredentialJellyfinAPIKey)
		api := &jellyfinAPI{env: s.env, base: base, token: key}
		folders, err := jellyfinFolders(ctx, api)
		if err != nil {
			s.t.Fatal(err)
		}
		state["jellyfin libraries"] = folders
		reply, err := api.call(ctx, http.MethodGet, "/Auth/Keys", nil, nil)
		if err != nil || reply.Status != http.StatusOK {
			s.t.Fatalf("snapshot jellyfin keys: %v %d", err, reply.Status)
		}
		var keys struct{ Items []jellyfinKey }
		_ = reply.decode(&keys)
		var names []string
		for _, item := range keys.Items {
			names = append(names, item.AppName)
		}
		slices.Sort(names)
		state["jellyfin keys"] = names
		reply, err = api.call(ctx, http.MethodGet, "/System/Configuration/network", nil, nil)
		read("jellyfin network", reply, err)
		network, _ := state["jellyfin network"].(map[string]any)
		state["jellyfin network"] = map[string]any{"EnableRemoteAccess": network["EnableRemoteAccess"], "EnableUPnP": network["EnableUPnP"]}
		reply, err = api.call(ctx, http.MethodGet, "/Users", nil, nil)
		read("jellyfin users", reply, err)
		users, _ := state["jellyfin users"].([]any)
		state["jellyfin users"] = len(users)
	}
	if s.containers["qbittorrent"] != "" {
		base, _ := s.env.BaseURL(ctx, "qbittorrent", PortQBittorrent)
		password, _, _ := s.env.LookupCredential(ctx, CredentialLogin)
		api := &qbittorrentAPI{env: s.env, base: base}
		if outcome, err := api.signIn(ctx, password); err != nil || outcome != qbittorrentAccepted {
			s.t.Fatalf("snapshot qbittorrent sign-in: %v %d", err, outcome)
		}
		defer api.signOut(ctx)
		reply, err := api.call(ctx, http.MethodGet, "/api/v2/app/preferences", nil)
		read("qbittorrent preferences", reply, err)
		reply, err = api.call(ctx, http.MethodGet, "/api/v2/torrents/categories", nil)
		read("qbittorrent categories", reply, err)
		config, err := s.env.ReadFile(ctx, "qbittorrent", qbittorrentConfigFile)
		if err != nil {
			s.t.Fatal(err)
		}
		state["qbittorrent password line"] = qbittorrentWebSettings(config)["Password_PBKDF2"]
	}
	if s.containers["seerr"] != "" {
		api, err := seerrWithKey(ctx, s.env)
		if err != nil {
			s.t.Fatal(err)
		}
		for _, path := range []string{"/settings/main", "/settings/public", "/settings/jellyfin", "/settings/radarr", "/settings/sonarr", "/user"} {
			reply, err := api.call(ctx, http.MethodGet, path, nil)
			read("seerr "+path, reply, err)
		}
	}
	builder := "jellyfin"
	if s.containers["qbittorrent"] != "" {
		builder = "qbittorrent"
	}
	listing, err := s.env.Exec(ctx, builder, "0:0", []string{"find", DataRoot, "-printf", "%U:%G %m %y %p\n"})
	if err != nil || listing.ExitCode != 0 {
		s.t.Fatalf("snapshot folders: %v %s", err, listing.Stderr)
	}
	lines := strings.Split(strings.TrimSpace(listing.Stdout), "\n")
	slices.Sort(lines)
	state["folders"] = lines
	secrets := []string{liveRadarrKey, liveSonarrKey}
	for _, name := range []string{CredentialLogin, CredentialJellyfinAPIKey, CredentialLogin, CredentialSeerrAPIKey} {
		value, _, _ := s.env.LookupCredential(ctx, name)
		state["credential "+name] = value
		secrets = append(secrets, value)
	}
	encoded, err := json.MarshalIndent(state, "", " ")
	if err != nil {
		s.t.Fatal(err)
	}
	s.env.take()
	// A failed comparison prints the state. Each secret is replaced by a
	// digest, which still differs when the secret does.
	text := string(encoded)
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(secret)))[:19])
		}
	}
	return text
}

// changing reports the requests of a pass that could change an application.
// Signing in and out, an application's own connection test and emptying a
// cache change nothing a person configured.
func changing(requests []string) []string {
	harmless := []string{
		"POST /Users/AuthenticateByName", "POST /Sessions/Logout",
		"POST /api/v2/auth/login", "POST /api/v2/auth/logout",
		"POST /api/v1/auth/jellyfin",
		"POST /api/v1/settings/radarr/test", "POST /api/v1/settings/sonarr/test",
		"POST /api/v1/settings/cache/radarr/flush", "POST /api/v1/settings/cache/sonarr/flush",
	}
	var writes []string
	for _, request := range requests {
		if !strings.HasPrefix(request, "GET ") && !slices.Contains(harmless, request) {
			writes = append(writes, request)
		}
	}
	return writes
}

func TestLiveCompleteStepsConfigureOnceAndThenChangeNothing(t *testing.T) {
	stack := startLiveStack(t, "complete", "complete", []string{"jellyfin", "seerr", "qbittorrent", "radarr", "sonarr"})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	stack.waitReady(ctx)

	// What the Radarr, Sonarr and Recyclarr steps leave for Seerr's: a root
	// folder each (made once the folders exist) and a quality profile, named
	// once by id and once by name only.
	stack.mustRun(ctx, "storage.folders")
	stack.managerCall(ctx, "radarr", PortRadarr, liveRadarrKey, http.MethodPost, "/api/v3/rootfolder", map[string]any{"path": MoviesLibrary}, nil)
	stack.managerCall(ctx, "sonarr", PortSonarr, liveSonarrKey, http.MethodPost, "/api/v3/rootfolder", map[string]any{"path": TVLibrary}, nil)
	var radarrProfiles, sonarrProfiles []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	stack.managerCall(ctx, "radarr", PortRadarr, liveRadarrKey, http.MethodGet, "/api/v3/qualityprofile", nil, &radarrProfiles)
	stack.managerCall(ctx, "sonarr", PortSonarr, liveSonarrKey, http.MethodGet, "/api/v3/qualityprofile", nil, &sonarrProfiles)
	if len(radarrProfiles) == 0 || len(sonarrProfiles) == 0 {
		t.Fatal("the managers have no quality profile to point Seerr at")
	}
	sonarrProfile := sonarrProfiles[len(sonarrProfiles)-1]
	stack.env.SetOutputs("recyclarr.sync", map[string]string{
		"radarrProfileId":   fmt.Sprint(radarrProfiles[0].ID),
		"radarrProfileName": radarrProfiles[0].Name,
		"sonarrProfileName": sonarrProfile.Name,
	})
	stack.env.take()

	for _, id := range d1StepIDs {
		stack.mustRun(ctx, id)
	}
	first := stack.env.take()
	if writes := changing(first); len(writes) == 0 {
		t.Fatal("the first pass made no change at all")
	} else {
		t.Logf("first pass changed things with %d requests: %s", len(writes), strings.Join(writes, ", "))
	}
	before := stack.snapshot(ctx)

	for _, id := range d1StepIDs {
		stack.mustRun(ctx, id)
	}
	second := stack.env.take()
	if writes := changing(second); len(writes) != 0 {
		t.Fatalf("the second pass changed something: %s", strings.Join(writes, ", "))
	}
	t.Logf("second pass made %d requests, none changing anything", len(second))
	if after := stack.snapshot(ctx); after != before {
		t.Fatalf("state differs after the second pass\nbefore: %s\nafter: %s", before, after)
	}
	for _, expected := range []string{
		`"Name": "Movies"`, `"Name": "Shows"`, `"save_path": "/data/downloads"`, `"auto_tmm_enabled": true`,
		`"savePath": "/data/downloads/movies"`, `"savePath": "/data/downloads/tv"`,
		`"name": "Radarr (Just Dashboard)"`, `"name": "Sonarr (Just Dashboard)"`, `"activeProfileName": "` + sonarrProfile.Name + `"`,
		`"initialized": true`, `"defaultPermissions": 32`, `1000:1000 755 d /data/media/movies`, `1000:1000 755 d /data/downloads/tv`,
		`"bypass_local_auth": false`, `"bypass_auth_subnet_whitelist_enabled": false`,
		`"EnableRemoteAccess": true`, `"EnableUPnP": false`,
	} {
		if !strings.Contains(before, expected) {
			t.Errorf("the configured state lacks %s", expected)
		}
	}
	if strings.Contains(before, probeName) {
		t.Error("a write test file was left on the media volume")
	}

	t.Run("a changed option is applied and nothing else", func(t *testing.T) {
		stack.env.Options[OptionRequestApproval] = "auto"
		stack.mustRun(ctx, "seerr.policy")
		if writes := changing(stack.env.take()); !slices.Equal(writes, []string{"POST /api/v1/settings/main"}) {
			t.Fatalf("changing request approval made %v", writes)
		}
		if state := stack.snapshot(ctx); !strings.Contains(state, `"defaultPermissions": 160`) {
			t.Fatal("automatic approval did not reach Seerr's default permissions")
		}
	})

	t.Run("a manager entry somebody edited is brought back, with its id", func(t *testing.T) {
		api, err := seerrWithKey(ctx, stack.env)
		if err != nil {
			t.Fatal(err)
		}
		list, err := seerrListManagers(ctx, api, "radarr")
		if err != nil || len(list) != 1 {
			t.Fatalf("radarr servers: %v %v", list, err)
		}
		// Seerr gives the id and holds a request to its API document, where
		// the id is read-only: a body that carries one is refused.
		if reply, err := api.call(ctx, http.MethodPut, "/settings/radarr/0", list[0]); err != nil || reply.Status != http.StatusBadRequest {
			t.Fatalf("saving an entry with its id in the body: %v %d %s", err, reply.Status, reply.excerpt(liveRadarrKey))
		} else {
			t.Logf("Seerr refuses an id in the body: %s", reply.excerpt(liveRadarrKey))
		}
		edited := map[string]any{}
		for key, value := range list[0] {
			if key != "id" {
				edited[key] = value
			}
		}
		edited["activeDirectory"] = "/somewhere/else"
		edited["minimumAvailability"] = "announced"
		if reply, err := api.call(ctx, http.MethodPut, "/settings/radarr/0", edited); err != nil || reply.Status != http.StatusOK {
			t.Fatalf("editing the entry: %v %d %s", err, reply.Status, reply.excerpt(liveRadarrKey))
		}
		stack.env.take()
		stack.mustRun(ctx, "seerr.managers")
		if writes := changing(stack.env.take()); !slices.Equal(writes, []string{"PUT /api/v1/settings/radarr/0"}) {
			t.Fatalf("repairing the entry made %v", writes)
		}
		list, err = seerrListManagers(ctx, api, "radarr")
		if err != nil || len(list) != 1 {
			t.Fatalf("radarr servers after repair: %v %v", list, err)
		}
		if list[0]["activeDirectory"] != MoviesLibrary || list[0]["minimumAvailability"] != "announced" {
			t.Fatalf("the entry was not repaired in place: %v %v", list[0]["activeDirectory"], list[0]["minimumAvailability"])
		}
		stack.mustRun(ctx, "seerr.verify")
	})

	t.Run("a regenerated Seerr key is read again", func(t *testing.T) {
		api, err := seerrWithKey(ctx, stack.env)
		if err != nil {
			t.Fatal(err)
		}
		old := api.key
		if reply, err := api.call(ctx, http.MethodPost, "/settings/main/regenerate", nil); err != nil || reply.Status != http.StatusOK {
			t.Fatalf("regenerating: %v %d", err, reply.Status)
		}
		stack.mustRun(ctx, "seerr.setup")
		fresh, _, _ := stack.env.LookupCredential(ctx, CredentialSeerrAPIKey)
		if fresh == "" || fresh == old {
			t.Fatal("the stack kept a key Seerr no longer accepts")
		}
		stack.mustRun(ctx, "seerr.verify")
	})

	t.Run("a deleted Jellyfin key is made again, once", func(t *testing.T) {
		base, _ := stack.env.BaseURL(ctx, "jellyfin", PortJellyfin)
		old, _, _ := stack.env.LookupCredential(ctx, CredentialJellyfinAPIKey)
		api := &jellyfinAPI{env: stack.env, base: base, token: old}
		if reply, err := api.call(ctx, http.MethodDelete, "/Auth/Keys/"+url.PathEscape(old), nil, nil); err != nil || reply.Status != http.StatusNoContent {
			t.Fatalf("deleting the key: %v %d", err, reply.Status)
		}
		stack.mustRun(ctx, "jellyfin.setup")
		fresh, _, _ := stack.env.LookupCredential(ctx, CredentialJellyfinAPIKey)
		if fresh == "" || fresh == old {
			t.Fatal("the stack kept a key Jellyfin no longer accepts")
		}
		if state := stack.snapshot(ctx); strings.Count(state, `"Just Dashboard"`) != 1 {
			t.Fatalf("Jellyfin does not hold exactly one key for the stack:\n%s", state)
		}
		stack.mustRun(ctx, "jellyfin.libraries")
	})

	t.Run("a qBittorrent password somebody changed is an action, without a sign-in", func(t *testing.T) {
		base, _ := stack.env.BaseURL(ctx, "qbittorrent", PortQBittorrent)
		password, _, _ := stack.env.LookupCredential(ctx, CredentialLogin)
		api := &qbittorrentAPI{env: stack.env, base: base}
		if outcome, err := api.signIn(ctx, password); err != nil || outcome != qbittorrentAccepted {
			t.Fatalf("sign-in: %v %d", err, outcome)
		}
		if reply, err := api.call(ctx, http.MethodPost, "/api/v2/app/setPreferences", url.Values{"json": {`{"web_ui_password":"SomebodyElses1"}`}}); err != nil || reply.Status != http.StatusOK {
			t.Fatalf("changing the password: %v %d", err, reply.Status)
		}
		api.signOut(ctx)
		// qBittorrent writes its settings file a moment after the change.
		deadline := time.Now().Add(30 * time.Second)
		for {
			config, err := stack.env.ReadFile(ctx, "qbittorrent", qbittorrentConfigFile)
			if _, matches := qbittorrentPasswordState(config, password); err == nil && !matches {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("qBittorrent did not write the changed password to its settings")
			}
			time.Sleep(time.Second)
		}
		stack.env.take()
		_, err := stack.run(ctx, "qbittorrent.auth")
		var action *stacks.ActionError
		if !errors.As(err, &action) || action.Action.ID != actionQBittorrentPassword || !action.Action.Blocking {
			t.Fatalf("expected the password action, got %v", err)
		}
		if requests := stack.env.take(); slices.Contains(requests, "POST /api/v2/auth/login") {
			t.Fatalf("a sign-in was tried with a password known to be wrong: %v", requests)
		}
	})
}

func TestLiveEssentialStepsConfigureOnceAndThenChangeNothing(t *testing.T) {
	stack := startLiveStack(t, "essential", "essential", []string{"jellyfin", "seerr"})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	stack.waitReady(ctx)

	// The runner skips a step whose service the preset does not run.
	steps := []string{"storage.folders", "jellyfin.setup", "jellyfin.libraries", "seerr.setup", "seerr.policy", "seerr.verify"}
	for _, id := range steps {
		stack.mustRun(ctx, id)
	}
	stack.env.take()
	before := stack.snapshot(ctx)
	for _, id := range steps {
		stack.mustRun(ctx, id)
	}
	if writes := changing(stack.env.take()); len(writes) != 0 {
		t.Fatalf("the second pass changed something: %s", strings.Join(writes, ", "))
	}
	if after := stack.snapshot(ctx); after != before {
		t.Fatalf("state differs after the second pass\nbefore: %s\nafter: %s", before, after)
	}
	for _, expected := range []string{`"Name": "Movies"`, `"Name": "Shows"`, `"initialized": true`, `1000:1000 755 d /data/media/tv`} {
		if !strings.Contains(before, expected) {
			t.Errorf("the configured state lacks %s", expected)
		}
	}
}

func TestLiveJellyfinSetUpBySomeoneElseIsAnAction(t *testing.T) {
	stack := startLiveStack(t, "takenover", "essential", []string{"jellyfin"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	stack.waitReady(ctx)

	// Somebody finishes the wizard with an account of their own first.
	base, _ := stack.env.BaseURL(ctx, "jellyfin", PortJellyfin)
	someone := &jellyfinAPI{env: stack.env, base: base}
	for {
		reply, err := someone.call(ctx, http.MethodGet, "/Startup/User", nil, nil)
		if err == nil && reply.Status == http.StatusOK {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("Jellyfin's wizard never became available")
		}
		time.Sleep(2 * time.Second)
	}
	for _, request := range []struct {
		path string
		body any
	}{
		{"/Startup/User", map[string]any{"Name": "owner", "Password": "TheirOwnPassword1"}},
		{"/Startup/RemoteAccess", map[string]any{"EnableRemoteAccess": true}},
		{"/Startup/Complete", nil},
	} {
		if reply, err := someone.call(ctx, http.MethodPost, request.path, nil, request.body); err != nil || reply.Status != http.StatusNoContent {
			t.Fatalf("their %s: %v %d", request.path, err, reply.Status)
		}
	}
	stack.env.take()

	for pass := 1; pass <= 2; pass++ {
		_, err := stack.run(ctx, "jellyfin.setup")
		var action *stacks.ActionError
		if !errors.As(err, &action) || action.Action.ID != actionJellyfinTakenOver || !action.Action.Blocking {
			t.Fatalf("pass %d: expected the owner action, got %v", pass, err)
		}
		requests := stack.env.take()
		if writes := changing(requests); len(writes) != 0 {
			t.Fatalf("pass %d changed a Jellyfin that is not the stack's: %v", pass, writes)
		}
		// The first pass has no password to try. The second has the one it
		// made for the page to show, and tries it once.
		tried := slices.Contains(requests, "POST /Users/AuthenticateByName")
		if tried != (pass == 2) {
			t.Fatalf("pass %d: sign-in tried = %v", pass, tried)
		}
	}
	if _, found, _ := stack.env.LookupCredential(ctx, CredentialLogin); !found {
		t.Fatal("no password was made for the person to set")
	}
	if _, found, _ := stack.env.LookupCredential(ctx, CredentialJellyfinAPIKey); found {
		t.Fatal("an API key was stored for a Jellyfin that refused the stack")
	}
}
