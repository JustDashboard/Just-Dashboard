package media

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks/stackstest"
)

// arrStep is a step of the manifest by id, so a test runs what a release
// runs, wiring included.
func arrStep(t *testing.T, id string) stacks.Step {
	t.Helper()
	for _, step := range Manifest().Steps {
		if step.ID == id {
			return step
		}
	}
	t.Fatalf("the manifest has no step %s", id)
	return stacks.Step{}
}

func arrRun(t *testing.T, env interface {
	stacks.Env
	SetOutputs(string, map[string]string)
}, id string) stacks.Result {
	t.Helper()
	result, err := stackstest.Run(context.Background(), env, arrStep(t, id))
	if err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	return result
}

func arrFail(t *testing.T, env interface {
	stacks.Env
	SetOutputs(string, map[string]string)
}, id string) error {
	t.Helper()
	_, err := stackstest.Run(context.Background(), env, arrStep(t, id))
	if err == nil {
		t.Fatalf("%s: want an error", id)
	}
	return err
}

func arrWant(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("writes:\n got  %q\n want %q", got, want)
	}
}

func TestServarrAuthSecuresOnceThenLeavesAlone(t *testing.T) {
	for _, app := range []arrApp{arrRadarr, arrSonarr, arrProwlarr} {
		t.Run(app.service, func(t *testing.T) {
			stack := newArrFakeStack(t)
			fake := map[string]*arrFake{"radarr": stack.radarr, "sonarr": stack.sonarr, "prowlarr": stack.prowlarr}[app.service]
			step := app.service + ".auth"

			first := arrRun(t, stack.env, step)
			arrWant(t, fake.written(), []string{"PUT config/host/1"})
			password, _, _ := stack.env.LookupCredential(context.Background(), app.adminCredential)
			if fake.password != password || len(password) != generatedPasswordLength {
				t.Fatalf("the application was not given the stack's %d character password", generatedPasswordLength)
			}
			if fake.host["authenticationMethod"] != "forms" || fake.host["authenticationRequired"] != "enabled" || fake.host["username"] != AdminUsername {
				t.Fatalf("host configuration: %v", fake.host)
			}
			// Everything else in the resource went back as it came.
			if fake.host["instanceName"] != app.title || fake.host["backupRetention"] != float64(28) || fake.host["bindAddress"] != "*" {
				t.Fatalf("the rest of the host configuration changed: %v", fake.host)
			}
			if !slices.Contains(fake.calls, "POST /login") {
				t.Fatal("the sign-in was not proven")
			}
			if strings.Contains(first.Message, password) || strings.Contains(first.Message, fake.key) {
				t.Fatal("the message holds a secret")
			}

			fake.forget()
			second := arrRun(t, stack.env, step)
			arrWant(t, fake.written(), nil)
			if !slices.Contains(fake.calls, "POST /login") || !strings.Contains(second.Message, "already") {
				t.Fatalf("second run: %q after %v", second.Message, fake.calls)
			}

			// Someone changed the account's password in the application:
			// the stack's own credential is put back, since it is what the
			// dashboard shows for this application.
			fake.forget()
			fake.password = "something else"
			arrRun(t, stack.env, step)
			arrWant(t, fake.written(), []string{"PUT config/host/1"})
			if fake.password != password {
				t.Fatal("the stack's password was not restored")
			}
		})
	}
}

func TestServarrAuthStopsOnARefusedKeyAndRetriesAnOutage(t *testing.T) {
	stack := newArrFakeStack(t)
	stack.env.Vars[VariableRadarrAPIKey] = "not the key radarr has"
	err := arrFail(t, stack.env, "radarr.auth")
	if !stacks.IsPermanent(err) || !strings.Contains(err.Error(), VariableRadarrAPIKey) || strings.Contains(err.Error(), "not the key radarr has") {
		t.Fatalf("a refused key: %v", err)
	}
	arrWant(t, stack.written(), nil)

	stack.sonarr.fail["GET config/host"] = http.StatusInternalServerError
	err = arrFail(t, stack.env, "sonarr.auth")
	var problem *arrProblem
	if stacks.IsPermanent(err) || !errors.As(err, &problem) || problem.Status != http.StatusInternalServerError || !strings.Contains(err.Error(), "having a bad time") {
		t.Fatalf("an upstream error: %v", err)
	}
	arrWant(t, stack.written(), nil)

	stack.prowlarr.server.Close()
	if err := arrFail(t, stack.env, "prowlarr.auth"); stacks.IsPermanent(err) || !strings.Contains(err.Error(), "Prowlarr did not answer") {
		t.Fatalf("an application that is down: %v", err)
	}
}

func TestServarrRootFolderIsAddedOnce(t *testing.T) {
	stack := newArrFakeStack(t)
	first := arrRun(t, stack.env, "radarr.rootfolder")
	arrWant(t, stack.radarr.written(), []string{"POST rootfolder"})
	if first.Outputs["path"] != MoviesLibrary || first.Outputs["id"] != "1" || !strings.Contains(first.Message, "76 GB free") {
		t.Fatalf("first run: %+v", first)
	}
	stack.forget()
	second := arrRun(t, stack.env, "radarr.rootfolder")
	arrWant(t, stack.radarr.written(), nil)
	if second.Outputs["id"] != "1" || !strings.Contains(second.Message, "already") {
		t.Fatalf("second run: %+v", second)
	}

	// A folder of the person's own is not the stack's to touch or to count.
	stack.sonarr.folders = []map[string]any{{"path": "/data/media/anime", "accessible": true, "freeSpace": 1 << 30, "id": 4}}
	result := arrRun(t, stack.env, "sonarr.rootfolder")
	if result.Outputs["path"] != TVLibrary || result.Outputs["id"] != "2" || len(stack.sonarr.folders) != 2 {
		t.Fatalf("sonarr: %+v with %v", result, stack.sonarr.folders)
	}
}

func TestServarrRootFolderSaysWhyItCannotBeUsed(t *testing.T) {
	stack := newArrFakeStack(t)
	stack.radarr.absent[MoviesLibrary] = true
	err := arrFail(t, stack.env, "radarr.rootfolder")
	if stacks.IsPermanent(err) || !strings.Contains(err.Error(), "Path '/data/media/movies' does not exist") {
		t.Fatalf("a folder that is not there: %v", err)
	}
	arrWant(t, stack.radarr.written(), nil)

	stack.sonarr.folders = []map[string]any{{"path": TVLibrary, "accessible": false, "freeSpace": 0, "id": 1}}
	if err := arrFail(t, stack.env, "sonarr.rootfolder"); !strings.Contains(err.Error(), "not accessible") {
		t.Fatalf("a folder the application cannot use: %v", err)
	}
	stack.sonarr.folders = []map[string]any{{"path": TVLibrary, "accessible": true, "id": 1}}
	if err := arrFail(t, stack.env, "sonarr.rootfolder"); !strings.Contains(err.Error(), "no free space") {
		t.Fatalf("no free space reported: %v", err)
	}
}

// arrQBittorrentAccepts is the applications' own test of a qBittorrent
// client: it passes only at the stack's address with the stack's password.
func arrQBittorrentAccepts(password *string) func(string, map[string]any, map[string]any) bool {
	return func(_ string, _ map[string]any, settings map[string]any) bool {
		return settings["host"] == "qbittorrent" && settings["port"] == float64(PortQBittorrent) &&
			settings["username"] == AdminUsername && settings["password"] == *password
	}
}

func TestServarrDownloadClientIsTestedSavedOnceAndUpdatedInPlace(t *testing.T) {
	for _, app := range []struct {
		arrApp
		field, category string
	}{{arrRadarr, "movieCategory", "movies"}, {arrSonarr, "tvCategory", "tv"}} {
		t.Run(app.service, func(t *testing.T) {
			stack := newArrFakeStack(t)
			fake := map[string]*arrFake{"radarr": stack.radarr, "sonarr": stack.sonarr}[app.service]
			password, _ := stack.env.Credential(context.Background(), CredentialQBittorrent)
			accepted := password
			fake.passes = arrQBittorrentAccepts(&accepted)
			// A client the person made, which must come through untouched.
			theirs := map[string]any{"id": 9, "name": "My seedbox", "implementation": "Transmission", "enable": true,
				"fields": []any{map[string]any{"name": "host", "value": "seedbox.example"}}}
			fake.lists["downloadclient"] = []map[string]any{theirs}
			fake.nextID = 10
			before, _ := json.Marshal(theirs)
			step := app.service + ".downloadclient"

			first := arrRun(t, stack.env, step)
			arrWant(t, fake.written(), []string{"POST downloadclient"})
			if index := slices.Index(fake.calls, "POST downloadclient/test"); index < 0 || index > slices.Index(fake.calls, "POST downloadclient") {
				t.Fatalf("the application's test did not run before the save: %v", fake.calls)
			}
			saved := fake.lists["downloadclient"][1]
			settings := fake.settings("downloadclient", 10, fake.masked("downloadclient", saved))
			if saved["name"] != arrDownloadClientName || saved["enable"] != true || saved["implementation"] != "QBittorrent" ||
				settings["host"] != "qbittorrent" || settings[app.field] != app.category || settings["password"] != password {
				t.Fatalf("saved client: %v %v", saved, settings)
			}
			// Settings the stack does not name keep the schema's defaults.
			if saved["priority"] != float64(1) || saved["removeCompletedDownloads"] != true || settings["initialState"] != float64(0) {
				t.Fatalf("defaults were not kept: %v %v", saved, settings)
			}
			if first.Outputs["id"] != "10" || first.Outputs["category"] != app.category || strings.Contains(first.Message, password) {
				t.Fatalf("first run: %+v", first)
			}

			fake.forget()
			second := arrRun(t, stack.env, step)
			arrWant(t, fake.written(), nil)
			if !slices.Contains(fake.calls, "POST downloadclient/test") || !strings.Contains(second.Message, "already") {
				t.Fatalf("second run: %q after %v", second.Message, fake.calls)
			}

			// The person repointed the stack's client, and changed its
			// priority: the address is put back in place, the priority kept.
			fake.forget()
			saved["priority"] = float64(3)
			saved["fields"].([]any)[0].(map[string]any)["value"] = "10.0.0.9"
			arrRun(t, stack.env, step)
			arrWant(t, fake.written(), []string{"PUT downloadclient/10"})
			saved = fake.lists["downloadclient"][1]
			if saved["priority"] != float64(3) || fake.settings("downloadclient", 10, saved)["host"] != "qbittorrent" || len(fake.lists["downloadclient"]) != 2 {
				t.Fatalf("updated client: %v", saved)
			}

			// The stored password is no longer the right one (the secret is
			// never read back, so only the application's test can say).
			fake.forget()
			fake.secrets["downloadclient/10/password"] = "an older password"
			arrRun(t, stack.env, step)
			arrWant(t, fake.written(), []string{"PUT downloadclient/10"})
			if fake.secrets["downloadclient/10/password"] != password {
				t.Fatal("the password was not put back")
			}

			if after, _ := json.Marshal(fake.lists["downloadclient"][0]); string(after) != string(before) {
				t.Fatalf("the person's own client changed:\n%s\n%s", before, after)
			}
		})
	}
}

func TestServarrDownloadClientIsNotSavedWhenItsTestFails(t *testing.T) {
	stack := newArrFakeStack(t)
	accepted := "qBittorrent has another password"
	stack.radarr.passes = arrQBittorrentAccepts(&accepted)
	err := arrFail(t, stack.env, "radarr.downloadclient")
	var failure *arrTestFailure
	if !errors.As(err, &failure) || stacks.IsPermanent(err) || !strings.Contains(err.Error(), "Unable to connect to qBittorrent") {
		t.Fatalf("a failed test: %v", err)
	}
	arrWant(t, stack.radarr.written(), nil)

	// A client under the stack's name that is another kind is the person's
	// to rename; the stack does not turn it into qBittorrent.
	stack.sonarr.lists["downloadclient"] = []map[string]any{{"id": 1, "name": arrDownloadClientName, "implementation": "Deluge", "fields": []any{}}}
	if err := arrFail(t, stack.env, "sonarr.downloadclient"); !stacks.IsPermanent(err) || !strings.Contains(err.Error(), "Deluge") {
		t.Fatalf("a client of another kind: %v", err)
	}
	arrWant(t, stack.sonarr.written(), nil)
}

func TestProwlarrAppsAreTestedSavedOnceAndFoundByName(t *testing.T) {
	stack := newArrFakeStack(t)
	keys := map[string]string{"http://radarr:7878": stack.radarr.key, "http://sonarr:8989": stack.sonarr.key}
	stack.prowlarr.passes = func(_ string, _ map[string]any, settings map[string]any) bool {
		base, _ := settings["baseUrl"].(string)
		return settings["prowlarrUrl"] == "http://prowlarr:9696" && keys[base] != "" && settings["apiKey"] == keys[base]
	}
	theirs := map[string]any{"id": 5, "name": "Lidarr", "implementation": "Lidarr", "syncLevel": "addOnly", "fields": []any{}}
	stack.prowlarr.lists["applications"] = []map[string]any{theirs}
	stack.prowlarr.nextID = 6

	first := arrRun(t, stack.env, "prowlarr.apps")
	arrWant(t, stack.prowlarr.written(), []string{"POST applications", "POST applications"})
	if first.Outputs["radarrApplicationId"] != "6" || first.Outputs["sonarrApplicationId"] != "7" {
		t.Fatalf("first run: %+v", first)
	}
	for index, want := range []struct{ name, implementation, base string }{
		{"Radarr (Just Dashboard)", "Radarr", "http://radarr:7878"}, {"Sonarr (Just Dashboard)", "Sonarr", "http://sonarr:8989"},
	} {
		saved := stack.prowlarr.lists["applications"][index+1]
		settings := stack.prowlarr.settings("applications", index+6, stack.prowlarr.masked("applications", saved))
		if saved["name"] != want.name || saved["implementation"] != want.implementation || saved["syncLevel"] != "fullSync" ||
			settings["baseUrl"] != want.base || settings["apiKey"] != keys[want.base] {
			t.Fatalf("saved application: %v %v", saved, settings)
		}
		// The categories each application is sent come from Prowlarr's own
		// schema, not from the stack.
		if categories, _ := settings["syncCategories"].([]any); len(categories) < 8 {
			t.Fatalf("the schema's sync categories were not kept: %v", settings)
		}
	}
	for _, message := range []string{first.Message, strings.Join(stack.env.Lines, "\n")} {
		if strings.Contains(message, stack.radarr.key) || strings.Contains(message, stack.sonarr.key) {
			t.Fatal("an API key was reported")
		}
	}

	stack.forget()
	second := arrRun(t, stack.env, "prowlarr.apps")
	arrWant(t, stack.prowlarr.written(), nil)
	if !strings.Contains(second.Message, "already") || len(stack.prowlarr.lists["applications"]) != 3 || stack.prowlarr.lists["applications"][0]["syncLevel"] != "addOnly" {
		t.Fatalf("second run: %q, %v", second.Message, stack.prowlarr.lists["applications"])
	}

	// Sonarr's key changed with a redeploy: its application alone is
	// updated, in place.
	stack.forget()
	stack.env.Vars[VariableSonarrAPIKey] = "a new key for sonarr"
	keys["http://sonarr:8989"] = "a new key for sonarr"
	arrRun(t, stack.env, "prowlarr.apps")
	arrWant(t, stack.prowlarr.written(), []string{"PUT applications/7"})

	stack.forget()
	keys["http://radarr:7878"] = "prowlarr cannot use this one"
	err := arrFail(t, stack.env, "prowlarr.apps")
	if !strings.Contains(err.Error(), "API Key is invalid") || !strings.Contains(err.Error(), "Radarr (Just Dashboard)") {
		t.Fatalf("a failed test: %v", err)
	}
	arrWant(t, stack.prowlarr.written(), nil)
}

func TestDownloadsVerifyTestsBothClientsAndReadsHealth(t *testing.T) {
	stack := newArrFakeStack(t)
	password, _ := stack.env.Credential(context.Background(), CredentialQBittorrent)
	accepted := password
	for _, fake := range []*arrFake{stack.radarr, stack.sonarr} {
		fake.passes = arrQBittorrentAccepts(&accepted)
	}
	arrRun(t, stack.env, "radarr.downloadclient")
	arrRun(t, stack.env, "sonarr.downloadclient")
	stack.forget()

	// A stack with no source yet: both applications say so in their health,
	// and that is the sources step's to report, not this one's.
	stack.radarr.health = []byte(`[{"source":"IndexerRssCheck","type":"error","message":"No indexers available with RSS sync enabled, Radarr will not grab new releases automatically"}]`)
	stack.sonarr.health = []byte(`[{"source":"IndexerSearchCheck","type":"warning","message":"No indexers available with Automatic Search enabled, Sonarr will not provide any automatic search results"},{"source":"UpdateCheck","type":"warning","message":"New update is available"}]`)
	result := arrRun(t, stack.env, "downloads.verify")
	if !strings.Contains(result.Message, "both reached qBittorrent") || !strings.Contains(result.Message, "Sonarr: New update is available") || strings.Contains(result.Message, "indexers") {
		t.Fatalf("message: %q", result.Message)
	}
	for _, fake := range []*arrFake{stack.radarr, stack.sonarr} {
		for _, call := range []string{"POST downloadclient/test", "POST command", "GET command/3", "GET health"} {
			if !slices.Contains(fake.calls, call) {
				t.Fatalf("%s was not asked %s: %v", fake.app.title, call, fake.calls)
			}
		}
	}
	arrWant(t, stack.written(), nil)

	// What Radarr said in the live run before qBittorrent's folders were
	// set: the download client's folder is not one Radarr can see.
	stack.radarr.health = arrFixture(t, "radarr-health.json")
	err := arrFail(t, stack.env, "downloads.verify")
	if !strings.Contains(err.Error(), "places downloads in /downloads but this directory does not appear to exist") || strings.Contains(err.Error(), "RSS") {
		t.Fatalf("a download problem in health: %v", err)
	}
	// Sonarr's, from the same run: no download client at all is a warning
	// about downloading, named but not a failure of a client that tests.
	stack.radarr.health = []byte("[]")
	stack.sonarr.health = arrFixture(t, "sonarr-health.json")
	if result := arrRun(t, stack.env, "downloads.verify"); !strings.Contains(result.Message, "Sonarr: No download client is available") {
		t.Fatalf("message: %q", result.Message)
	}

	accepted = "qBittorrent's password was changed"
	err = arrFail(t, stack.env, "downloads.verify")
	if !strings.Contains(err.Error(), "Radarr's test") || !strings.Contains(err.Error(), "Sonarr's test") || !strings.Contains(err.Error(), "Unable to connect to qBittorrent") {
		t.Fatalf("a failed test in both: %v", err)
	}

	accepted = password
	stack.sonarr.lists["downloadclient"] = nil
	if err := arrFail(t, stack.env, "downloads.verify"); !strings.Contains(err.Error(), `Sonarr has no download client named "qBittorrent (Just Dashboard)"`) {
		t.Fatalf("a missing client: %v", err)
	}
	arrWant(t, stack.written(), nil)
}

func TestProwlarrSourcesAsksForASourceAndNeverAddsOne(t *testing.T) {
	stack := newArrFakeStack(t)
	action := func() stacks.Action {
		t.Helper()
		var needed *stacks.ActionError
		if err := arrFail(t, stack.env, "prowlarr.sources"); !errors.As(err, &needed) {
			t.Fatalf("want an action, got %v", err)
		} else {
			return needed.Action
		}
		return stacks.Action{}
	}
	none := action()
	if none.ID != "media-source" || none.Service != "prowlarr" || !none.Blocking || none.Title != "Add a media source" ||
		!strings.Contains(none.Description, "a source you are allowed to use") || !strings.Contains(none.Description, "None is included") ||
		!strings.Contains(none.Description, "shares it with Radarr and Sonarr") ||
		!strings.Contains(none.Description, "run the setup again") {
		t.Fatalf("action: %+v", none)
	}

	// The fields of an indexer as Prowlarr lists one (GET indexer/schema in
	// the live run), cut to what the step reads.
	stack.prowlarr.indexers = []map[string]any{{"id": 1, "name": "A source", "enable": false, "protocol": "torrent", "privacy": "public"}}
	if off := action(); !strings.Contains(off.Description, "1 media source, but none is switched on") ||
		!strings.Contains(off.Description, "run the setup again") {
		t.Fatalf("action: %+v", off)
	}

	stack.prowlarr.indexers = append(stack.prowlarr.indexers,
		map[string]any{"id": 2, "name": "Another", "enable": true}, map[string]any{"id": 3, "name": "A third", "enable": true})
	result := arrRun(t, stack.env, "prowlarr.sources")
	if result.Outputs["sources"] != "2" || !strings.Contains(result.Message, "2 media sources") {
		t.Fatalf("result: %+v", result)
	}
	arrWant(t, stack.written(), nil)
	for _, call := range stack.prowlarr.calls {
		if call != "GET indexer" {
			t.Fatalf("the step asked Prowlarr for more than its indexers: %v", stack.prowlarr.calls)
		}
	}
}
