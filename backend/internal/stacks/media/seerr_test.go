package media

import (
	"slices"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks/stackstest"
)

// seerrEnv is a Seerr nobody has set up, beside a Jellyfin whose
// administrator and libraries the earlier steps made.
func seerrEnv(t *testing.T, preset string) (*fakeSeerr, *stackstest.Fake) {
	t.Helper()
	seerr := newFakeSeerr(t)
	services := map[string]string{"jellyfin": "", "seerr": seerr.server.URL}
	if preset == "complete" {
		services["radarr"], services["sonarr"] = "", ""
	}
	env := newFake(preset, services)
	password, err := env.Credential(t.Context(), CredentialJellyfinAdmin)
	if err != nil {
		t.Fatal(err)
	}
	seerr.jellyfinPassword = password
	env.SetOutputs("jellyfin.libraries", map[string]string{
		"moviesLibraryId": "f137a2dd21bbc1b99aa5c0f6bf02a805", "showsLibraryId": "a656b907eb3a73532e40e44b968d0225",
	})
	env.SetOutputs("recyclarr.sync", map[string]string{
		"radarrProfileId": "7", "radarrProfileName": "HD Bluray + WEB", "sonarrProfileId": "9", "sonarrProfileName": "WEB-1080p",
	})
	return seerr, env
}

func TestSeerrSetupSignsInOnceAndThenOnlyReads(t *testing.T) {
	seerr, env := seerrEnv(t, "complete")

	result := mustRunStep(t, env, "seerr.setup")
	if seerr.owner != "admin" || seerr.mediaServerType != seerrServerJellyfin {
		t.Fatalf("owner = %q, media server type = %d", seerr.owner, seerr.mediaServerType)
	}
	for key, want := range map[string]any{"username": "admin", "hostname": "jellyfin", "port": float64(8096), "useSsl": false, "urlBase": "", "serverType": float64(2)} {
		if seerr.lastSignIn[key] != want {
			t.Errorf("sign-in %s = %v", key, seerr.lastSignIn[key])
		}
	}
	if credential(env, CredentialSeerrAPIKey) != seerr.apiKey {
		t.Fatal("Seerr's API key was not stored")
	}
	if len(seerr.libraries) != 2 || !seerr.libraries[0].Enabled || !seerr.libraries[1].Enabled {
		t.Fatalf("libraries = %+v", seerr.libraries)
	}
	if seerr.initialized {
		t.Fatal("Seerr left its setup pages before the policy step ran")
	}
	if result.Message != "Signed Seerr in to Jellyfin as admin; Movies and Shows are turned on" {
		t.Fatalf("message = %q", result.Message)
	}
	wantNoSecret(t, result.Message+strings.Join(env.Lines, " "), seerr.apiKey, seerr.jellyfinPassword)
	want := []string{
		"GET /api/v1/settings/public", "POST /api/v1/auth/jellyfin", "GET /api/v1/settings/main", "GET /api/v1/auth/me",
		"GET /api/v1/settings/jellyfin/library", "POST /api/v1/settings/jellyfin/library/sync",
		"PUT /api/v1/settings/jellyfin/library/f137a2dd21bbc1b99aa5c0f6bf02a805", "PUT /api/v1/settings/jellyfin/library/a656b907eb3a73532e40e44b968d0225",
		"GET /api/v1/settings/jellyfin/library",
	}
	if got := seerr.take(); !slices.Equal(got, want) {
		t.Fatalf("first run requests:\n got %v\nwant %v", got, want)
	}

	result = mustRunStep(t, env, "seerr.setup")
	if got := seerr.take(); !slices.Equal(got, []string{"GET /api/v1/settings/public", "GET /api/v1/auth/me", "GET /api/v1/settings/jellyfin/library"}) {
		t.Fatalf("the second run made %v", got)
	}
	if !strings.HasPrefix(result.Message, "Seerr is signed in to Jellyfin;") {
		t.Fatalf("message = %q", result.Message)
	}
}

func TestSeerrSetupKeepsALibrarySomebodyTurnedOnAndTurnsOnOnlyItsOwn(t *testing.T) {
	seerr, env := seerrEnv(t, "complete")
	seerr.jellyfinHas = append(seerr.jellyfinHas, seerrLibrary{ID: "0000000000000000000000000000cccc", Name: "Home videos"})
	mustRunStep(t, env, "seerr.setup")
	if seerr.libraries[2].Enabled {
		t.Fatal("a library that is not the stack's was turned on")
	}
	// Somebody turns the stack's Shows off and their own library on.
	seerr.libraries[1].Enabled, seerr.libraries[2].Enabled = false, true
	seerr.take()
	mustRunStep(t, env, "seerr.setup")
	if writes := changing(seerr.take()); !slices.Equal(writes, []string{"PUT /api/v1/settings/jellyfin/library/a656b907eb3a73532e40e44b968d0225"}) {
		t.Fatalf("changes = %v", writes)
	}
	if !seerr.libraries[1].Enabled || !seerr.libraries[2].Enabled {
		t.Fatalf("libraries = %+v", seerr.libraries)
	}
}

func TestSeerrSetupReadsTheKeyAgainAfterAnAttemptThatNeverStoredIt(t *testing.T) {
	seerr, env := seerrEnv(t, "complete")
	// The first attempt signed in, which set Seerr up, and then lost its
	// answer.
	seerr.force("GET /api/v1/settings/main", 502, "")
	if _, err := runStep(t, env, "seerr.setup"); err == nil || stacks.IsPermanent(err) {
		t.Fatalf("expected an error the runner retries, got %v", err)
	}
	if credential(env, CredentialSeerrAPIKey) != "" || seerr.owner != "admin" {
		t.Fatalf("key stored = %v, owner = %q", credential(env, CredentialSeerrAPIKey) != "", seerr.owner)
	}
	delete(seerr.forced, "GET /api/v1/settings/main")

	result := mustRunStep(t, env, "seerr.setup")
	// Seerr refuses a host once it has one, so the second sign-in names none.
	if _, sent := seerr.lastSignIn["hostname"]; sent {
		t.Fatalf("the second sign-in sent a host: %v", seerr.lastSignIn)
	}
	if credential(env, CredentialSeerrAPIKey) != seerr.apiKey || !strings.Contains(result.Message, "read its API key again") {
		t.Fatalf("message = %q", result.Message)
	}
}

func TestSeerrSetupBySomeoneElseIsAnAction(t *testing.T) {
	for name, arrange := range map[string]func(*fakeSeerr){
		"their owner, and new sign-ins get an ordinary account": func(seerr *fakeSeerr) { seerr.owner = "owner" },
		"their owner, and new sign-ins are refused": func(seerr *fakeSeerr) {
			seerr.owner = "owner"
			seerr.main["newPlexLogin"] = false
		},
		"sign-in with Jellyfin turned off": func(seerr *fakeSeerr) {
			seerr.owner = "owner"
			seerr.main["mediaServerLogin"] = false
		},
		"connected to Plex": func(seerr *fakeSeerr) {
			seerr.owner = "owner"
			seerr.mediaServerType = 1
		},
	} {
		t.Run(name, func(t *testing.T) {
			seerr, env := seerrEnv(t, "complete")
			seerr.mediaServerType, seerr.initialized = seerrServerJellyfin, true
			seerr.libraries = []seerrLibrary{{ID: "f137a2dd21bbc1b99aa5c0f6bf02a805", Name: "Movies"}}
			arrange(seerr)

			_, err := runStep(t, env, "seerr.setup")
			action := wantAction(t, err, actionSeerrTakenOver)
			if action.Service != "seerr" || !strings.Contains(action.Description, "Admin permission") {
				t.Fatalf("action = %+v", action)
			}
			wantNoSecret(t, action.Description, seerr.jellyfinPassword, seerr.apiKey)
			if writes := changing(seerr.take()); len(writes) != 0 {
				t.Fatalf("somebody else's Seerr was changed: %v", writes)
			}
			if credential(env, CredentialSeerrAPIKey) != "" || seerr.libraries[0].Enabled {
				t.Fatal("a key was stored or a library turned on in a Seerr that is not the stack's")
			}
		})
	}
}

func TestSeerrSetupWithAKeySomebodyRegeneratedSignsInAgain(t *testing.T) {
	seerr, env := seerrEnv(t, "complete")
	mustRunStep(t, env, "seerr.setup")
	seerr.apiKey = "regenerated-key-for-tests"
	seerr.take()

	mustRunStep(t, env, "seerr.setup")
	if credential(env, CredentialSeerrAPIKey) != "regenerated-key-for-tests" {
		t.Fatal("the stack kept a key Seerr no longer accepts")
	}
	if writes := changing(seerr.take()); len(writes) != 0 {
		t.Fatalf("changes = %v", writes)
	}
}

func TestSeerrSetupDoesNotRetryAPasswordJellyfinRefused(t *testing.T) {
	seerr, env := seerrEnv(t, "complete")
	seerr.jellyfinPassword = "changed-in-jellyfin"

	_, err := runStep(t, env, "seerr.setup")
	if !stacks.IsPermanent(err) || !strings.Contains(err.Error(), "INVALID_CREDENTIALS") {
		t.Fatalf("error = %v", err)
	}
	wantNoSecret(t, err.Error(), credential(env, CredentialJellyfinAdmin))
}

func TestSeerrSetupRetriesWhileSeerrCannotReachJellyfin(t *testing.T) {
	seerr, env := seerrEnv(t, "complete")
	seerr.jellyfinDown = true
	_, err := runStep(t, env, "seerr.setup")
	if err == nil || stacks.IsPermanent(err) || !strings.Contains(err.Error(), "could not sign in to Jellyfin at jellyfin:8096: HTTP 404: {\"message\":\"INVALID_URL\"}") {
		t.Fatalf("error = %v", err)
	}

	seerr.jellyfinDown = false
	seerr.force("POST /api/v1/settings/jellyfin/library/sync", 502, `{"message":"CONNECTION_ERROR"}`)
	_, err = runStep(t, env, "seerr.setup")
	if err == nil || stacks.IsPermanent(err) || !strings.Contains(err.Error(), "could not read the libraries from Jellyfin: HTTP 502") {
		t.Fatalf("error = %v", err)
	}
	wantNoSecret(t, err.Error(), seerr.apiKey)
}

func TestSeerrSetupSaysWhenCSRFProtectionRefusesItsWrites(t *testing.T) {
	seerr, env := seerrEnv(t, "complete")
	seerr.force("POST /api/v1/auth/jellyfin", 403, `{"message":"invalid csrf token"}`)
	_, err := runStep(t, env, "seerr.setup")
	if !stacks.IsPermanent(err) || !strings.Contains(err.Error(), "CSRF protection") {
		t.Fatalf("error = %v", err)
	}
}

func TestSeerrManagersAreAddedOnceAndThenOnlyTested(t *testing.T) {
	seerr, env := seerrEnv(t, "complete")
	mustRunStep(t, env, "seerr.setup")
	seerr.take()

	result := mustRunStep(t, env, "seerr.managers")
	radarr, sonarr := seerr.managers["radarr"], seerr.managers["sonarr"]
	if len(radarr) != 1 || len(sonarr) != 1 {
		t.Fatalf("radarr = %v, sonarr = %v", radarr, sonarr)
	}
	for key, want := range map[string]any{
		"name": "Radarr (Just Dashboard)", "hostname": "radarr", "port": float64(7878), "apiKey": "radarr-key-for-tests", "useSsl": false,
		"baseUrl": "", "activeProfileId": float64(7), "activeProfileName": "HD Bluray + WEB", "activeDirectory": "/data/media/movies",
		"is4k": false, "isDefault": true, "minimumAvailability": "released", "syncEnabled": true, "preventSearch": false,
	} {
		if radarr[0][key] != want {
			t.Errorf("radarr %s = %v", key, radarr[0][key])
		}
	}
	for key, want := range map[string]any{
		"name": "Sonarr (Just Dashboard)", "hostname": "sonarr", "port": float64(8989), "apiKey": "sonarr-key-for-tests",
		"activeProfileId": float64(9), "activeProfileName": "WEB-1080p", "activeDirectory": "/data/media/tv", "is4k": false, "isDefault": true,
		"enableSeasonFolders": true, "seriesType": "standard", "animeSeriesType": "anime", "monitorNewItems": "all",
	} {
		if sonarr[0][key] != want {
			t.Errorf("sonarr %s = %v", key, sonarr[0][key])
		}
	}
	if result.Outputs["radarrServerId"] != "0" || result.Outputs["sonarrServerId"] != "0" {
		t.Errorf("outputs = %v", result.Outputs)
	}
	if result.Message != "Radarr files into /data/media/movies with the HD Bluray + WEB profile; Sonarr files into /data/media/tv with the WEB-1080p profile" {
		t.Errorf("message = %q", result.Message)
	}
	wantNoSecret(t, result.Message+strings.Join(env.Lines, " "), "radarr-key-for-tests", "sonarr-key-for-tests", seerr.apiKey)
	if writes := changing(seerr.take()); !slices.Equal(writes, []string{"POST /api/v1/settings/radarr", "POST /api/v1/settings/sonarr"}) {
		t.Fatalf("first run changes = %v", writes)
	}

	mustRunStep(t, env, "seerr.managers")
	want := []string{
		"POST /api/v1/settings/radarr/test", "GET /api/v1/settings/radarr", "POST /api/v1/settings/sonarr/test", "GET /api/v1/settings/sonarr",
	}
	if got := seerr.take(); !slices.Equal(got, want) {
		t.Fatalf("the second run made %v", got)
	}
}

func TestSeerrManagersChooseTheProfileByNameWhenNoIdWasReported(t *testing.T) {
	seerr, env := seerrEnv(t, "complete")
	mustRunStep(t, env, "seerr.setup")
	env.SetOutputs("recyclarr.sync", map[string]string{"radarrProfileName": "hd bluray + web", "sonarrProfileName": "HD-1080p"})

	mustRunStep(t, env, "seerr.managers")
	if seerr.managers["radarr"][0]["activeProfileId"] != float64(7) || seerr.managers["sonarr"][0]["activeProfileId"] != float64(4) {
		t.Fatalf("profiles = %v, %v", seerr.managers["radarr"][0]["activeProfileId"], seerr.managers["sonarr"][0]["activeProfileId"])
	}
	// The name is written as the manager spells it.
	if seerr.managers["radarr"][0]["activeProfileName"] != "HD Bluray + WEB" {
		t.Fatalf("profile name = %v", seerr.managers["radarr"][0]["activeProfileName"])
	}
}

func TestSeerrManagersFailClearlyWithoutAProfileOrARootFolder(t *testing.T) {
	for name, test := range map[string]struct {
		arrange func(*fakeSeerr, *stackstest.Fake)
		want    string
	}{
		"no profile was reported": {func(_ *fakeSeerr, env *stackstest.Fake) { env.SetOutputs("recyclarr.sync", nil) }, "did not say which Radarr profile to use"},
		"the named profile is not there": {func(_ *fakeSeerr, env *stackstest.Fake) {
			env.SetOutputs("recyclarr.sync", map[string]string{"radarrProfileName": "UHD Remux"})
		}, `Radarr has no quality profile named "UHD Remux"`},
		"the profile id is not there": {func(_ *fakeSeerr, env *stackstest.Fake) {
			env.SetOutputs("recyclarr.sync", map[string]string{"radarrProfileId": "42"})
		}, "Radarr has no quality profile with id 42"},
		"the root folder is not there": {func(seerr *fakeSeerr, _ *stackstest.Fake) {
			seerr.managerState["radarr"] = managerAnswer("", 7, "HD Bluray + WEB")
		}, "Radarr has no root folder /data/media/movies yet"},
		"the manager does not answer": {func(seerr *fakeSeerr, _ *stackstest.Fake) { seerr.managerState["radarr"] = nil }, "Seerr cannot reach Radarr at radarr:7878: Failed to connect to Radarr"},
	} {
		t.Run(name, func(t *testing.T) {
			seerr, env := seerrEnv(t, "complete")
			mustRunStep(t, env, "seerr.setup")
			test.arrange(seerr, env)
			seerr.take()

			_, err := runStep(t, env, "seerr.managers")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v", err)
			}
			wantNoSecret(t, err.Error(), "radarr-key-for-tests")
			if writes := changing(seerr.take()); len(writes) != 0 || len(seerr.managers["radarr"]) != 0 {
				t.Fatalf("changes = %v", writes)
			}
		})
	}
}

func TestSeerrManagersLookPastWhatSeerrRemembers(t *testing.T) {
	seerr, env := seerrEnv(t, "complete")
	mustRunStep(t, env, "seerr.setup")
	// Seerr asked Radarr before the quality profile and the root folder were
	// made, and answers from that for an hour.
	seerr.remembered["radarr"] = managerAnswer("", 1, "Any")
	seerr.take()

	mustRunStep(t, env, "seerr.managers")
	got := seerr.take()
	want := []string{"POST /api/v1/settings/radarr/test", "POST /api/v1/settings/cache/radarr/flush", "POST /api/v1/settings/radarr/test"}
	if !slices.Equal(got[:3], want) {
		t.Fatalf("requests = %v", got)
	}
	if slices.Contains(got, "POST /api/v1/settings/cache/sonarr/flush") {
		t.Fatalf("Sonarr's memory was emptied without need: %v", got)
	}
	if seerr.managers["radarr"][0]["activeProfileId"] != float64(7) {
		t.Fatalf("radarr = %v", seerr.managers["radarr"][0])
	}
}

func TestSeerrManagersMendTheirEntryAndKeepWhatSomebodyChose(t *testing.T) {
	seerr, env := seerrEnv(t, "complete")
	mustRunStep(t, env, "seerr.setup")
	mustRunStep(t, env, "seerr.managers")
	// Somebody adds a server of their own, makes it the default, and edits
	// the stack's: its folder (the stack's to decide) and when a film counts
	// as available (theirs).
	theirs := map[string]any{
		"id": float64(1), "name": "Radarr 4K", "hostname": "radarr4k", "port": float64(7878), "apiKey": "theirs", "useSsl": false,
		"activeProfileId": float64(1), "activeProfileName": "Any", "activeDirectory": "/films4k", "is4k": true, "isDefault": true, "minimumAvailability": "released",
	}
	seerr.managers["radarr"] = append(seerr.managers["radarr"], theirs)
	own := seerr.managers["radarr"][0]
	own["activeDirectory"], own["isDefault"], own["minimumAvailability"], own["tags"] = "/elsewhere", false, "announced", []any{float64(3)}
	seerr.take()

	mustRunStep(t, env, "seerr.managers")
	if writes := changing(seerr.take()); !slices.Equal(writes, []string{"PUT /api/v1/settings/radarr/0"}) {
		t.Fatalf("changes = %v", writes)
	}
	own = seerr.managers["radarr"][0]
	if own["activeDirectory"] != "/data/media/movies" || own["isDefault"] != true {
		t.Fatalf("the stack's entry was not mended: %v", own)
	}
	if own["minimumAvailability"] != "announced" || len(own["tags"].([]any)) != 1 {
		t.Fatalf("their choices were overwritten: %v", own)
	}
	if len(seerr.managers["radarr"]) != 2 || theirs["hostname"] != "radarr4k" || theirs["isDefault"] != true {
		t.Fatalf("their own server was changed: %v", theirs)
	}
}

func TestSeerrManagersNeedTheVariablesTheTemplateGenerates(t *testing.T) {
	seerr, env := seerrEnv(t, "complete")
	mustRunStep(t, env, "seerr.setup")
	delete(env.Vars, VariableRadarrAPIKey)
	seerr.take()
	if _, err := runStep(t, env, "seerr.managers"); err == nil || !strings.Contains(err.Error(), "RADARR_API_KEY") {
		t.Fatalf("error = %v", err)
	}
	if got := seerr.take(); len(got) != 0 {
		t.Fatalf("requests = %v", got)
	}
}

func TestSeerrPolicyAppliesApprovalAndFinishesSetupOnce(t *testing.T) {
	seerr, env := seerrEnv(t, "essential")
	mustRunStep(t, env, "seerr.setup")
	seerr.take()

	result := mustRunStep(t, env, "seerr.policy")
	if seerr.main["defaultPermissions"] != 32 || !seerr.initialized {
		t.Fatalf("default permissions = %v, initialized = %v", seerr.main["defaultPermissions"], seerr.initialized)
	}
	if !strings.Contains(result.Message, "an administrator approves each request") || result.Outputs["requestApproval"] != "admin" {
		t.Fatalf("result = %+v", result)
	}
	// A new Seerr already has these defaults: only finishing setup is a change.
	if writes := changing(seerr.take()); !slices.Equal(writes, []string{"POST /api/v1/settings/initialize"}) {
		t.Fatalf("first run changes = %v", writes)
	}

	mustRunStep(t, env, "seerr.policy")
	if got := seerr.take(); !slices.Equal(got, []string{"GET /api/v1/settings/main", "GET /api/v1/settings/public"}) {
		t.Fatalf("the second run made %v", got)
	}

	env.OptionsMap[OptionRequestApproval] = "auto"
	result = mustRunStep(t, env, "seerr.policy")
	if seerr.main["defaultPermissions"] != float64(160) || !strings.Contains(result.Message, "approved automatically") {
		t.Fatalf("default permissions = %v, message = %q", seerr.main["defaultPermissions"], result.Message)
	}
	if writes := changing(seerr.take()); !slices.Equal(writes, []string{"POST /api/v1/settings/main"}) {
		t.Fatalf("changes = %v", writes)
	}
}

func TestSeerrPolicyKeepsOtherPermissionsAndTurnsSignInBackOn(t *testing.T) {
	seerr, env := seerrEnv(t, "essential")
	mustRunStep(t, env, "seerr.setup")
	// Somebody lets new accounts report issues and ask in 4K, approves
	// everything automatically, and turns Jellyfin sign-in off.
	const createIssues, request4K = 4194304, 1024
	seerr.main["defaultPermissions"] = 32 | 128 | 256 | 512 | 32768 | createIssues | request4K
	seerr.main["mediaServerLogin"], seerr.main["newPlexLogin"] = false, false

	mustRunStep(t, env, "seerr.policy")
	if seerr.main["defaultPermissions"] != float64(32|createIssues|request4K) {
		t.Fatalf("default permissions = %v", seerr.main["defaultPermissions"])
	}
	if seerr.main["mediaServerLogin"] != true || seerr.main["newPlexLogin"] != true {
		t.Fatalf("sign-in with Jellyfin accounts = %v, %v", seerr.main["mediaServerLogin"], seerr.main["newPlexLogin"])
	}
	for _, privilege := range []int{seerrPermissionAdmin, 4, 8, 16} {
		if int(seerr.main["defaultPermissions"].(float64))&privilege != 0 {
			t.Fatalf("a new account was given a managing permission: %v", seerr.main["defaultPermissions"])
		}
	}
}

func TestSeerrPolicyRefusesAnApprovalItDoesNotKnow(t *testing.T) {
	seerr, env := seerrEnv(t, "essential")
	mustRunStep(t, env, "seerr.setup")
	seerr.take()
	env.OptionsMap[OptionRequestApproval] = "everyone"
	if _, err := runStep(t, env, "seerr.policy"); !stacks.IsPermanent(err) {
		t.Fatalf("error = %v", err)
	}
	delete(env.OptionsMap, OptionRequestApproval)
	mustRunStep(t, env, "seerr.policy")
	if seerr.main["defaultPermissions"] != 32 {
		t.Fatalf("with no option the default is not approval by an administrator: %v", seerr.main["defaultPermissions"])
	}
}

func TestSeerrPolicyReportsARefusedSave(t *testing.T) {
	seerr, env := seerrEnv(t, "essential")
	mustRunStep(t, env, "seerr.setup")
	env.OptionsMap[OptionRequestApproval] = "auto"
	seerr.force("POST /api/v1/settings/main", 500, `{"message":"Something went wrong."}`)
	_, err := runStep(t, env, "seerr.policy")
	if err == nil || stacks.IsPermanent(err) || !strings.Contains(err.Error(), "did not save who may request: HTTP 500") {
		t.Fatalf("error = %v", err)
	}
	if seerr.initialized {
		t.Fatal("setup was finished after the policy failed to save")
	}
}

func TestSeerrVerifyTestsJellyfinAndEveryManager(t *testing.T) {
	seerr, env := seerrEnv(t, "complete")
	mustRunStep(t, env, "seerr.setup")
	mustRunStep(t, env, "seerr.managers")
	seerr.take()

	result := mustRunStep(t, env, "seerr.verify")
	if result.Message != "Seerr reaches Jellyfin, Radarr (Just Dashboard), Sonarr (Just Dashboard)" {
		t.Fatalf("message = %q", result.Message)
	}
	got := seerr.take()
	if writes := changing(got); len(writes) != 0 {
		t.Fatalf("verifying changed %v", writes)
	}
	for _, expected := range []string{"GET /api/v1/settings/jellyfin/users", "POST /api/v1/settings/radarr/test", "POST /api/v1/settings/sonarr/test"} {
		if !slices.Contains(got, expected) {
			t.Errorf("verify did not make %s: %v", expected, got)
		}
	}
}

func TestSeerrVerifySaysWhichConnectionFailed(t *testing.T) {
	for name, test := range map[string]struct {
		arrange func(*fakeSeerr)
		want    []string
	}{
		"Jellyfin": {func(seerr *fakeSeerr) { seerr.jellyfinDown = true }, []string{"failed for Jellyfin (", "CONNECTION_ERROR"}},
		"Sonarr": {func(seerr *fakeSeerr) { seerr.managerState["sonarr"] = nil },
			[]string{"failed for Sonarr (Just Dashboard) (Failed to connect to Sonarr)"}},
		"both managers": {func(seerr *fakeSeerr) { seerr.managerState["radarr"], seerr.managerState["sonarr"] = nil, nil },
			[]string{"Radarr (Just Dashboard) (Failed to connect to Radarr)", "Sonarr (Just Dashboard) (Failed to connect to Sonarr)"}},
		"a root folder removed since, which Seerr still remembers": {func(seerr *fakeSeerr) {
			seerr.managerState["radarr"] = managerAnswer("", 7, "HD Bluray + WEB")
		}, []string{"Radarr (Just Dashboard) (Radarr no longer has the root folder /data/media/movies)"}},
		"a quality profile removed since": {func(seerr *fakeSeerr) {
			seerr.managerState["sonarr"] = managerAnswer("/data/media/tv", 1, "Any")
		}, []string{"Sonarr (Just Dashboard) (Sonarr no longer has the quality profile"}},
		"the stack's server removed from Seerr": {func(seerr *fakeSeerr) { seerr.managers["radarr"] = []map[string]any{} },
			[]string{"Radarr (Just Dashboard) (Seerr does not have it)"}},
	} {
		t.Run(name, func(t *testing.T) {
			seerr, env := seerrEnv(t, "complete")
			mustRunStep(t, env, "seerr.setup")
			mustRunStep(t, env, "seerr.managers")
			test.arrange(seerr)

			_, err := runStep(t, env, "seerr.verify")
			if err == nil {
				t.Fatal("verify passed")
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not say %q", err, want)
				}
			}
			wantNoSecret(t, err.Error(), seerr.apiKey, "radarr-key-for-tests", "sonarr-key-for-tests")
		})
	}
}

func TestSeerrVerifyDoesNotFailTheStackForAServerSomebodyAdded(t *testing.T) {
	seerr, env := seerrEnv(t, "complete")
	mustRunStep(t, env, "seerr.setup")
	mustRunStep(t, env, "seerr.managers")
	seerr.managers["radarr"] = append(seerr.managers["radarr"], map[string]any{
		"id": float64(1), "name": "Radarr 4K", "hostname": "radarr4k", "port": float64(7878), "apiKey": "theirs", "useSsl": false, "is4k": true,
	})

	result := mustRunStep(t, env, "seerr.verify")
	if !strings.Contains(result.Message, "Its test of Radarr 4K failed; that server is not the stack's") {
		t.Fatalf("message = %q", result.Message)
	}
	wantNoSecret(t, result.Message+strings.Join(env.Lines, " "), "theirs")
}

func TestSeerrVerifyInEssentialTestsOnlyJellyfin(t *testing.T) {
	seerr, env := seerrEnv(t, "essential")
	mustRunStep(t, env, "seerr.setup")
	seerr.take()
	result := mustRunStep(t, env, "seerr.verify")
	if result.Message != "Seerr reaches Jellyfin" {
		t.Fatalf("message = %q", result.Message)
	}
	want := []string{"GET /api/v1/settings/jellyfin/users", "GET /api/v1/settings/radarr", "GET /api/v1/settings/sonarr"}
	if got := seerr.take(); !slices.Equal(got, want) {
		t.Fatalf("requests = %v", got)
	}
}

func TestSeerrStepsAfterSetupNeedItsKey(t *testing.T) {
	seerr, env := seerrEnv(t, "complete")
	for _, id := range []string{"seerr.managers", "seerr.policy", "seerr.verify"} {
		if _, err := runStep(t, env, id); err == nil || !strings.Contains(err.Error(), "no Seerr API key") {
			t.Fatalf("%s: error = %v", id, err)
		}
	}
	if got := seerr.take(); len(got) != 0 {
		t.Fatalf("requests without a key: %v", got)
	}
}
