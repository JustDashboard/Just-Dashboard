package media

// The live test of the Radarr, Sonarr, Prowlarr, Bazarr and Recyclarr steps:
// the pinned images, started on a network and a shared volume of the test's
// own, configured by the steps in the manifest's order, twice. It runs only
// with JD_STACKS_LIVE=1, as root:
//
//	go test -c -o /tmp/media.test ./internal/stacks/media
//	sudo -n env JD_STACKS_LIVE=1 PATH=$PATH /tmp/media.test -test.run Live -test.v

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks/stackstest"
)

const (
	liveArrNetwork = "jdms-d2-net"
	liveArrVolume  = "jdms-d2-media"
)

// liveArrImages are the references the media-complete template pins.
var liveArrImages = map[string]string{
	"radarr":      "lscr.io/linuxserver/radarr:7.0.3.10918-ls328",
	"sonarr":      "lscr.io/linuxserver/sonarr:4.0.20.3014-ls6",
	"prowlarr":    "lscr.io/linuxserver/prowlarr:3.1.0.5799-ls170",
	"bazarr":      "lscr.io/linuxserver/bazarr:v1.6.2-ls374",
	"qbittorrent": "lscr.io/linuxserver/qbittorrent:5.3.1_v2.1.3-ls490",
	"recyclarr":   "recyclarr/recyclarr:8.8.1",
}

// liveArrSteps are this file's steps; the manifest gives their order.
var liveArrSteps = []string{
	"radarr.auth", "radarr.rootfolder", "sonarr.auth", "sonarr.rootfolder", "prowlarr.auth", "bazarr.auth",
	"recyclarr.sync", "radarr.downloadclient", "sonarr.downloadclient", "prowlarr.apps", "bazarr.link",
	"downloads.verify", "prowlarr.sources",
}

func TestLiveServarrBazarrRecyclarrSteps(t *testing.T) {
	if os.Getenv("JD_STACKS_LIVE") != "1" {
		t.Skip("set JD_STACKS_LIVE=1 to run against real containers")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	containers := map[string]string{}
	for service := range liveArrImages {
		containers[service] = "jdms-d2-" + service
	}
	remove := func() {
		for _, name := range containers {
			_ = exec.Command("docker", "rm", "-f", "-v", name).Run()
		}
		_ = exec.Command("docker", "network", "rm", liveArrNetwork).Run()
		_ = exec.Command("docker", "volume", "rm", "-f", liveArrVolume).Run()
	}
	remove()
	t.Cleanup(remove)

	variables := map[string]string{
		VariableRadarrAPIKey:   liveArrKey(t),
		VariableSonarrAPIKey:   liveArrKey(t),
		VariableProwlarrAPIKey: liveArrKey(t),
	}
	liveArrDocker(t, "network", "create", liveArrNetwork)
	liveArrDocker(t, "volume", "create", liveArrVolume)
	// The environment is the template's: the key by <APP>__AUTH__APIKEY for
	// the three Servarr applications, and both keys for Recyclarr.
	linuxserver := []string{"-e", "PUID=1000", "-e", "PGID=1000", "-e", "TZ=Etc/UTC"}
	start := func(service string, arguments ...string) {
		run := []string{"run", "-d", "--name", containers[service], "--network", liveArrNetwork, "--network-alias", service}
		run = append(run, arguments...)
		liveArrDocker(t, append(run, liveArrImages[service])...)
	}
	media := []string{"-v", liveArrVolume + ":" + DataRoot}
	start("radarr", append(slices.Concat(linuxserver, media), "-e", "RADARR__AUTH__APIKEY="+variables[VariableRadarrAPIKey])...)
	start("sonarr", append(slices.Concat(linuxserver, media), "-e", "SONARR__AUTH__APIKEY="+variables[VariableSonarrAPIKey])...)
	start("prowlarr", append(slices.Clone(linuxserver), "-e", "PROWLARR__AUTH__APIKEY="+variables[VariableProwlarrAPIKey])...)
	start("bazarr", slices.Concat(linuxserver, media)...)
	start("qbittorrent", append(slices.Concat(linuxserver, media), "-e", "WEBUI_PORT=8080", "-e", "TORRENTING_PORT=6881")...)
	start("recyclarr", "-e", "TZ=Etc/UTC", "-e", "CRON_SCHEDULE=@daily",
		"-e", "RADARR_API_KEY="+variables[VariableRadarrAPIKey], "-e", "SONARR_API_KEY="+variables[VariableSonarrAPIKey])

	env := stackstest.Live(t, Manifest(), liveArrNetwork, containers, "complete",
		map[string]string{OptionQuality: "1080p", OptionSubtitleLanguages: "en,ro", OptionRequestApproval: "admin"}, variables)

	// What lane D1's steps do before these run, by the test's own hand: the
	// folders on the shared volume, and qBittorrent's password and folders.
	liveArrDocker(t, "exec", containers["radarr"], "sh", "-c",
		"mkdir -p "+MoviesDownload+" "+TVDownload+" "+MoviesLibrary+" "+TVLibrary+" && chown -R 1000:1000 "+DataRoot)
	for service, probe := range map[string]struct {
		port int
		path string
	}{
		"radarr": {PortRadarr, "/ping"}, "sonarr": {PortSonarr, "/ping"}, "prowlarr": {PortProwlarr, "/ping"},
		"bazarr": {PortBazarr, "/api/system/ping"}, "qbittorrent": {PortQBittorrent, "/"},
	} {
		liveArrWait(ctx, t, env, service, probe.port, probe.path)
	}
	liveArrPrepareQBittorrent(ctx, t, env)

	steps := map[string]stacks.Step{}
	var order []string
	for _, step := range Manifest().Steps {
		if slices.Contains(liveArrSteps, step.ID) {
			steps[step.ID] = step
			order = append(order, step.ID)
		}
	}
	if len(order) != len(liveArrSteps) {
		t.Fatalf("the manifest has %d of the %d steps this test runs", len(order), len(liveArrSteps))
	}
	pass := func(name string) map[string]stacks.Result {
		results := map[string]stacks.Result{}
		for _, id := range order {
			step := steps[id]
			stepCtx, stop := context.WithTimeout(ctx, step.Timeout)
			started := time.Now()
			result, err := stackstest.Run(stepCtx, env, step)
			stop()
			var action *stacks.ActionError
			switch {
			case id == "prowlarr.sources":
				// No source ships with the stack, and the test adds none.
				if !errors.As(err, &action) || action.Action.ID != actionMediaSource || !action.Action.Blocking || action.Action.Service != "prowlarr" {
					t.Fatalf("%s pass, %s: want the media source action, got %v / %+v", name, id, err, result)
				}
				t.Logf("%s pass, %s (%s): action %q: %s", name, id, time.Since(started).Round(time.Millisecond), action.Action.Title, action.Action.Description)
			case err != nil:
				t.Fatalf("%s pass, %s: %v", name, id, err)
			default:
				t.Logf("%s pass, %s (%s): %s %v", name, id, time.Since(started).Round(time.Millisecond), result.Message, result.Outputs)
			}
			results[id] = result
		}
		return results
	}

	first := pass("first")
	before := liveArrSnapshot(ctx, t, env)
	second := pass("second")
	after := liveArrSnapshot(ctx, t, env)

	for name, was := range before {
		if now := after[name]; now != was {
			t.Errorf("the second pass changed %s:\n before: %s\n after:  %s", name, was, now)
		}
	}
	for _, id := range order {
		if id == "prowlarr.sources" || id == "downloads.verify" {
			continue
		}
		if !strings.Contains(second[id].Message, "already") {
			t.Errorf("second pass, %s: the message does not say it was already so: %q", id, second[id].Message)
		}
		for key, value := range first[id].Outputs {
			if second[id].Outputs[key] != value {
				t.Errorf("second pass, %s: output %s was %q and is now %q", id, key, value, second[id].Outputs[key])
			}
		}
	}

	sync := first["recyclarr.sync"].Outputs
	if sync["radarrProfileName"] != "HD Bluray + WEB (Just Dashboard)" || sync["sonarrProfileName"] != "WEB-1080p (Just Dashboard)" || sync["radarrProfileId"] == "" || sync["sonarrProfileId"] == "" {
		t.Errorf("recyclarr.sync outputs: %v", sync)
	}
	if folder := first["radarr.rootfolder"].Outputs; folder["path"] != MoviesLibrary || folder["id"] == "" {
		t.Errorf("radarr.rootfolder outputs: %v", folder)
	}
	if folder := first["sonarr.rootfolder"].Outputs; folder["path"] != TVLibrary || folder["id"] == "" {
		t.Errorf("sonarr.rootfolder outputs: %v", folder)
	}
	// The keys are in Recyclarr's environment, never in its file.
	config, err := env.ReadFile(ctx, "recyclarr", recyclarrConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range variables {
		if strings.Contains(string(config), key) {
			t.Error("Recyclarr's file holds an API key")
		}
	}
	for _, result := range []map[string]stacks.Result{first, second} {
		for id, step := range result {
			for _, secret := range liveArrSecrets(ctx, t, env, variables) {
				if strings.Contains(step.Message, secret) || strings.Contains(fmt.Sprint(step.Outputs), secret) {
					t.Errorf("%s reports a secret", id)
				}
			}
		}
	}

	if !t.Failed() {
		liveArrDrift(ctx, t, env, steps)
	}
}

// liveArrDrift changes, by hand, what a person or a redeploy could change,
// and has the steps put the stack's own settings back in place without
// touching what is not theirs.
func liveArrDrift(ctx context.Context, t *testing.T, env *stacks.DockerEnv, steps map[string]stacks.Step) {
	t.Helper()
	run := func(id string) stacks.Result {
		t.Helper()
		stepCtx, stop := context.WithTimeout(ctx, steps[id].Timeout)
		defer stop()
		result, err := stackstest.Run(stepCtx, env, steps[id])
		if err != nil {
			t.Fatalf("after a change by hand, %s: %v", id, err)
		}
		t.Logf("after a change by hand, %s: %s %v", id, result.Message, result.Outputs)
		return result
	}

	// Radarr's account is given another password in Radarr itself.
	radarr, err := newArrClient(ctx, env, arrRadarr)
	if err != nil {
		t.Fatal(err)
	}
	host, err := radarr.hostConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	host["password"], host["passwordConfirmation"] = "ChangedInRadarrByHand2345", "ChangedInRadarrByHand2345"
	if err := radarr.call(ctx, http.MethodPut, "config/host/1", host, nil); err != nil {
		t.Fatal(err)
	}
	password, _ := env.Credential(ctx, CredentialRadarrAdmin)
	if signedIn, err := radarr.signIn(ctx, AdminUsername, password); err != nil || signedIn {
		t.Fatalf("the password change by hand did not take: %v %v", signedIn, err)
	}
	if result := run("radarr.auth"); strings.Contains(result.Message, "already") {
		t.Errorf("radarr.auth did not notice the changed password: %q", result.Message)
	}
	if signedIn, err := radarr.signIn(ctx, AdminUsername, password); err != nil || !signedIn {
		t.Errorf("the stack's password does not sign in to Radarr again: %v %v", signedIn, err)
	}

	// Sonarr's copy of the stack's download client is pointed elsewhere and
	// given another priority, and a client of the person's own sits beside it.
	sonarr, err := newArrClient(ctx, env, arrSonarr)
	if err != nil {
		t.Fatal(err)
	}
	var clients []map[string]any
	if err := sonarr.call(ctx, http.MethodGet, "downloadclient", nil, &clients); err != nil || len(clients) != 1 {
		t.Fatalf("Sonarr's download clients: %v %v", clients, err)
	}
	mine, own := map[string]any{}, clients[0]
	encoded, _ := json.Marshal(own)
	_ = json.Unmarshal(encoded, &mine)
	delete(mine, "id")
	mine["name"], mine["enable"] = "My own client", false
	force := func(method, path string, body map[string]any) {
		t.Helper()
		// forceSave is the API's own way to save a client its test would
		// refuse, which a pointed-elsewhere one is.
		raw, _ := json.Marshal(body)
		request, _ := http.NewRequestWithContext(ctx, method, stacks.JoinURL(sonarr.base, "/api/v3/"+path, url.Values{"forceSave": {"true"}}), strings.NewReader(string(raw)))
		request.Header.Set("X-Api-Key", sonarr.key)
		request.Header.Set("Content-Type", "application/json")
		response, err := env.HTTP().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		answer, _ := io.ReadAll(response.Body)
		if response.StatusCode > 299 {
			t.Fatalf("Sonarr answered %d to %s %s: %s", response.StatusCode, method, path, answer)
		}
	}
	force(http.MethodPost, "downloadclient", mine)
	own["priority"] = 7
	for _, raw := range own["fields"].([]any) {
		if field := raw.(map[string]any); field["name"] == "host" {
			field["value"] = "somewhere-else"
		}
	}
	id, _ := arrID(own)
	force(http.MethodPut, fmt.Sprintf("downloadclient/%d", id), own)
	if result := run("sonarr.downloadclient"); strings.Contains(result.Message, "already") || result.Outputs["id"] != fmt.Sprint(id) {
		t.Errorf("sonarr.downloadclient did not update its client in place: %+v", result)
	}
	clients = nil
	if err := sonarr.call(ctx, http.MethodGet, "downloadclient", nil, &clients); err != nil || len(clients) != 2 {
		t.Fatalf("Sonarr's download clients afterwards: %d %v", len(clients), err)
	}
	for _, client := range clients {
		fields := map[string]any{}
		for _, raw := range client["fields"].([]any) {
			field := raw.(map[string]any)
			fields[field["name"].(string)] = field["value"]
		}
		switch client["name"] {
		case arrDownloadClientName:
			if fields["host"] != "qbittorrent" || client["priority"] != float64(7) {
				t.Errorf("the stack's client: host %v, priority %v", fields["host"], client["priority"])
			}
		case "My own client":
			if client["enable"] != false || fields["host"] != "qbittorrent" {
				t.Errorf("the person's own client changed: %v", client)
			}
		default:
			t.Errorf("an unexpected client %v", client["name"])
		}
	}
	run("downloads.verify")

	// Bazarr gets a profile and a language of the person's own, and the
	// stack's subtitle languages change.
	key, _, _ := env.LookupCredential(ctx, CredentialBazarrAPIKey)
	bazarr, err := newBazarrClient(ctx, env, key)
	if err != nil {
		t.Fatal(err)
	}
	state, err := bazarr.state(ctx)
	if err != nil {
		t.Fatal(err)
	}
	theirs := json.RawMessage(`{"profileId":9,"name":"Mine","cutoff":null,"items":[{"id":1,"language":"de","audio_exclude":"False","audio_only_include":"False","hi":"True","forced":"False"}],"mustContain":[],"mustNotContain":[],"originalFormat":null,"tag":null}`)
	profiles, _ := json.Marshal(append(slices.Clone(state.rawProfiles), theirs))
	if err := bazarr.saveSettings(ctx, url.Values{"languages-enabled": {"en", "ro", "de"}, "languages-profiles": {string(profiles)}}); err != nil {
		t.Fatal(err)
	}
	env.Options[OptionSubtitleLanguages] = "fr,en"
	if result := run("bazarr.link"); result.Outputs["languages"] != "fr,en" || result.Outputs["profileId"] != "1" {
		t.Errorf("bazarr.link after a language change: %+v", result)
	}
	if state, err = bazarr.state(ctx); err != nil {
		t.Fatal(err)
	}
	var enabled []string
	for _, language := range state.languages {
		if language.Enabled {
			enabled = append(enabled, language.Code2)
		}
	}
	slices.Sort(enabled)
	if !slices.Equal(enabled, []string{"de", "en", "fr", "ro"}) {
		t.Errorf("Bazarr's enabled languages: %v", enabled)
	}
	if len(state.profiles) != 2 {
		t.Fatalf("Bazarr's profiles: %s", state.rawProfiles)
	}
	for index, profile := range state.profiles {
		switch profile.Name {
		case bazarrProfileName:
			if !slices.Equal(bazarrProfileLanguages(profile), []string{"fr", "en"}) || profile.ProfileID != 1 {
				t.Errorf("the stack's profile: %s", state.rawProfiles[index])
			}
		case "Mine":
			var kept, want any
			_ = json.Unmarshal(state.rawProfiles[index], &kept)
			_ = json.Unmarshal(theirs, &want)
			if !arrSameValue(kept, want) {
				t.Errorf("the person's profile changed: %s", state.rawProfiles[index])
			}
		}
	}

	// The quality is changed to one the guides have no profile for.
	formats := func(app arrApp) (int, []string) {
		t.Helper()
		client, err := newArrClient(ctx, env, app)
		if err != nil {
			t.Fatal(err)
		}
		var custom []map[string]any
		var quality []struct{ Name string }
		if err := client.call(ctx, http.MethodGet, "customformat", nil, &custom); err != nil {
			t.Fatal(err)
		}
		if err := client.call(ctx, http.MethodGet, "qualityprofile", nil, &quality); err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, profile := range quality {
			names = append(names, profile.Name)
		}
		return len(custom), names
	}
	formatsBefore, profilesBefore := formats(arrRadarr)
	env.Options[OptionQuality] = "720p"
	lower := run("recyclarr.sync")
	if lower.Outputs["radarrProfileName"] != "HD-720p" || lower.Outputs["sonarrProfileName"] != "HD-720p" || lower.Outputs["radarrProfileId"] != "3" || lower.Outputs["sonarrProfileId"] != "3" {
		t.Errorf("recyclarr.sync for 720p: %+v", lower)
	}
	// Nothing Recyclarr made earlier is deleted by the change.
	if formatsAfter, profilesAfter := formats(arrRadarr); formatsAfter != formatsBefore || !slices.Equal(profilesAfter, profilesBefore) || !slices.Contains(profilesAfter, "HD Bluray + WEB (Just Dashboard)") {
		t.Errorf("Radarr after the change to 720p: %d custom formats (were %d), profiles %v", formatsAfter, formatsBefore, profilesAfter)
	}
}

func liveArrKey(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(raw)
}

func liveArrDocker(t *testing.T, arguments ...string) string {
	t.Helper()
	output, err := exec.Command("docker", arguments...).CombinedOutput()
	if err != nil {
		// The arguments of a run hold the test's API keys; the verb is enough.
		t.Fatalf("docker %s: %v: %s", arguments[0], err, output)
	}
	return string(output)
}

func liveArrWait(ctx context.Context, t *testing.T, env *stacks.DockerEnv, service string, port int, path string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Minute)
	for {
		base, err := env.BaseURL(ctx, service, port)
		if err == nil {
			var response *http.Response
			if response, err = env.HTTP().Get(stacks.JoinURL(base, path, nil)); err == nil {
				response.Body.Close()
				if response.StatusCode < 500 {
					return
				}
				err = fmt.Errorf("status %d", response.StatusCode)
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not come up: %v", service, err)
		}
		time.Sleep(2 * time.Second)
	}
}

// liveArrPrepareQBittorrent signs in with the temporary password qBittorrent
// prints on a first start, and leaves it as lane D1's steps would: the
// stack's own password, downloads under /data/downloads, and the two
// categories.
func liveArrPrepareQBittorrent(ctx context.Context, t *testing.T, env *stacks.DockerEnv) {
	t.Helper()
	password, err := env.Credential(ctx, CredentialQBittorrent)
	if err != nil {
		t.Fatal(err)
	}
	base, err := env.BaseURL(ctx, "qbittorrent", PortQBittorrent)
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`temporary password is provided for this session: (\S+)`)
	var temporary string
	for attempt := 0; attempt < 60 && temporary == ""; attempt++ {
		logs, err := env.Logs(ctx, "qbittorrent", 400)
		if err != nil {
			t.Fatal(err)
		}
		if found := pattern.FindStringSubmatch(logs); found != nil {
			temporary = found[1]
		} else {
			time.Sleep(time.Second)
		}
	}
	if temporary == "" {
		t.Fatal("qBittorrent printed no temporary password")
	}
	var cookie *http.Cookie
	post := func(path string, form url.Values) {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, stacks.JoinURL(base, path, nil), strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Referer", base)
		if cookie != nil {
			request.AddCookie(cookie)
		}
		response, err := env.HTTP().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, response.Body)
		// 409 is a category that is already there.
		if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusConflict {
			t.Fatalf("qBittorrent answered %d to %s", response.StatusCode, path)
		}
		for _, candidate := range response.Cookies() {
			cookie = candidate
		}
	}
	post("/api/v2/auth/login", url.Values{"username": {AdminUsername}, "password": {temporary}})
	if cookie == nil {
		t.Fatal("qBittorrent gave no session for its temporary password")
	}
	preferences, _ := json.Marshal(map[string]any{"web_ui_password": password, "save_path": DownloadsRoot, "temp_path_enabled": false})
	post("/api/v2/app/setPreferences", url.Values{"json": {string(preferences)}})
	post("/api/v2/torrents/createCategory", url.Values{"category": {"movies"}, "savePath": {MoviesDownload}})
	post("/api/v2/torrents/createCategory", url.Values{"category": {"tv"}, "savePath": {TVDownload}})
}

// liveArrSnapshot reads everything the steps configure, as text to compare.
func liveArrSnapshot(ctx context.Context, t *testing.T, env *stacks.DockerEnv) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	read := func(app arrApp, path string, drop ...string) {
		t.Helper()
		client, err := newArrClient(ctx, env, app)
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err := client.call(ctx, http.MethodGet, path, nil, &value); err != nil {
			t.Fatal(err)
		}
		liveArrDrop(value, drop)
		encoded, _ := json.Marshal(value)
		snapshot[app.service+" "+path] = string(encoded)
	}
	for _, app := range []arrApp{arrRadarr, arrSonarr} {
		read(app, "config/host")
		// Free space moves by itself.
		read(app, "rootfolder", "freeSpace")
		read(app, "downloadclient")
		read(app, "qualityprofile")
		read(app, "customformat")
		read(app, "qualitydefinition")
	}
	read(arrProwlarr, "config/host")
	read(arrProwlarr, "applications")
	read(arrProwlarr, "indexer")

	key, found, err := env.LookupCredential(ctx, CredentialBazarrAPIKey)
	if err != nil || !found {
		t.Fatalf("Bazarr's API key is not stored: %v", err)
	}
	bazarr, err := newBazarrClient(ctx, env, key)
	if err != nil {
		t.Fatal(err)
	}
	state, err := bazarr.state(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, section := range []string{"auth", "general", "radarr", "sonarr"} {
		encoded, _ := json.Marshal(state.settings[section])
		snapshot["bazarr settings "+section] = string(encoded)
	}
	profiles, _ := json.Marshal(state.rawProfiles)
	snapshot["bazarr profiles"] = string(profiles)
	var enabled []string
	for _, language := range state.languages {
		if language.Enabled {
			enabled = append(enabled, language.Code2)
		}
	}
	snapshot["bazarr languages"] = strings.Join(enabled, ",")

	config, err := env.ReadFile(ctx, "recyclarr", recyclarrConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot["recyclarr file"] = string(config)
	// A rewrite with the same content would still move the file's time.
	modified, err := env.Exec(ctx, "recyclarr", "", []string{"stat", "-c", "%Y %U:%G %a", recyclarrConfigPath})
	if err != nil || modified.ExitCode != 0 {
		t.Fatalf("stat of Recyclarr's file: %v %s", err, modified.Stderr)
	}
	snapshot["recyclarr file time and owner"] = strings.TrimSpace(modified.Stdout)
	return snapshot
}

func liveArrDrop(value any, names []string) {
	switch typed := value.(type) {
	case map[string]any:
		for _, name := range names {
			delete(typed, name)
		}
		for _, child := range typed {
			liveArrDrop(child, names)
		}
	case []any:
		for _, child := range typed {
			liveArrDrop(child, names)
		}
	}
}

func liveArrSecrets(ctx context.Context, t *testing.T, env *stacks.DockerEnv, variables map[string]string) []string {
	t.Helper()
	var secrets []string
	for _, value := range variables {
		secrets = append(secrets, value)
	}
	for _, name := range []string{CredentialRadarrAdmin, CredentialSonarrAdmin, CredentialProwlarrAdmin, CredentialBazarrAdmin, CredentialQBittorrent, CredentialBazarrAPIKey} {
		if value, found, err := env.LookupCredential(ctx, name); err == nil && found {
			secrets = append(secrets, value)
		}
	}
	return secrets
}
