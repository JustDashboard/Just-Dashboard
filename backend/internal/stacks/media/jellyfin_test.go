package media

import (
	"slices"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
)

func TestJellyfinSetupFinishesTheWizardOnceAndThenOnlyReads(t *testing.T) {
	jellyfin := newFakeJellyfin(t)
	env := newFake("complete", map[string]string{"jellyfin": jellyfin.server.URL})

	result := mustRunStep(t, env, "jellyfin.setup")
	password, key := credential(env, CredentialJellyfinAdmin), credential(env, CredentialJellyfinAPIKey)
	if len(password) != generatedPasswordLength || jellyfin.userName != "admin" || jellyfin.userPassword != password {
		t.Fatalf("the administrator is %q with a password of %d characters", jellyfin.userName, len(jellyfin.userPassword))
	}
	if !jellyfin.wizardDone || jellyfin.remoteAccess == nil || !*jellyfin.remoteAccess {
		t.Fatalf("wizard done = %v, remote access = %v", jellyfin.wizardDone, jellyfin.remoteAccess)
	}
	if len(jellyfin.keys) != 1 || jellyfin.keys[0].AppName != "Just Dashboard" || jellyfin.keys[0].AccessToken != key {
		t.Fatalf("keys = %+v, stored %q", jellyfin.keys, key)
	}
	if len(jellyfin.sessions) != 0 {
		t.Fatalf("the sign-in's session was left open: %v", jellyfin.sessions)
	}
	if result.Outputs["serverId"] != "2a95686363a346c6b47011ae436faed6" || result.Outputs["version"] != "12.4.0" {
		t.Fatalf("outputs = %v", result.Outputs)
	}
	wantNoSecret(t, result.Message+strings.Join(env.Lines, " "), password, key)
	want := []string{
		"GET /System/Info/Public", "GET /Startup/User", "POST /Startup/User", "POST /Startup/RemoteAccess", "POST /Startup/Complete",
		"POST /Users/AuthenticateByName", "GET /Auth/Keys", "POST /Auth/Keys", "GET /Auth/Keys", "GET /Auth/Keys", "POST /Sessions/Logout",
	}
	if got := jellyfin.take(); !slices.Equal(got, want) {
		t.Fatalf("first run requests:\n got %v\nwant %v", got, want)
	}

	result = mustRunStep(t, env, "jellyfin.setup")
	if got := jellyfin.take(); !slices.Equal(got, []string{"GET /System/Info/Public", "GET /Auth/Keys"}) {
		t.Fatalf("the second run made %v", got)
	}
	if credential(env, CredentialJellyfinAdmin) != password || credential(env, CredentialJellyfinAPIKey) != key {
		t.Fatal("the second run changed a credential")
	}
	if !strings.Contains(result.Message, "the stack's API key works") {
		t.Fatalf("message = %q", result.Message)
	}
}

func TestJellyfinSetupFinishesAWizardAnEarlierAttemptLeftHalfway(t *testing.T) {
	jellyfin := newFakeJellyfin(t)
	env := newFake("complete", map[string]string{"jellyfin": jellyfin.server.URL})
	// The first attempt got as far as naming the administrator.
	jellyfin.force("POST /Startup/RemoteAccess", 500, "Error processing request.")
	if _, err := runStep(t, env, "jellyfin.setup"); err == nil || stacks.IsPermanent(err) {
		t.Fatalf("expected an error the runner retries, got %v", err)
	}
	password := credential(env, CredentialJellyfinAdmin)
	if password == "" || jellyfin.wizardDone {
		t.Fatalf("after the failed attempt: password stored = %v, wizard done = %v", password != "", jellyfin.wizardDone)
	}
	delete(jellyfin.forced, "POST /Startup/RemoteAccess")

	mustRunStep(t, env, "jellyfin.setup")
	if !jellyfin.wizardDone || jellyfin.userPassword != password || len(jellyfin.keys) != 1 {
		t.Fatalf("wizard done = %v, same password = %v, keys = %d", jellyfin.wizardDone, jellyfin.userPassword == password, len(jellyfin.keys))
	}
}

func TestJellyfinSetupBySomeoneElseIsAnActionAndChangesNothing(t *testing.T) {
	jellyfin := newFakeJellyfin(t)
	jellyfin.wizardDone, jellyfin.userName, jellyfin.userPassword = true, "owner", "their-own-password"
	env := newFake("complete", map[string]string{"jellyfin": jellyfin.server.URL})

	_, err := runStep(t, env, "jellyfin.setup")
	action := wantAction(t, err, actionJellyfinTakenOver)
	if action.Service != "jellyfin" || !strings.Contains(action.Description, "Nothing was changed") {
		t.Fatalf("action = %+v", action)
	}
	// No password existed to try, so none was: a refusal counts towards
	// Jellyfin locking the account.
	if got := jellyfin.take(); !slices.Equal(got, []string{"GET /System/Info/Public"}) {
		t.Fatalf("requests = %v", got)
	}
	password := credential(env, CredentialJellyfinAdmin)
	if password == "" {
		t.Fatal("no password was made for the page to show")
	}
	wantNoSecret(t, action.Description, password)

	// The next run has that password and tries it once.
	_, err = runStep(t, env, "jellyfin.setup")
	wantAction(t, err, actionJellyfinTakenOver)
	if got := jellyfin.take(); !slices.Equal(got, []string{"GET /System/Info/Public", "POST /Users/AuthenticateByName"}) {
		t.Fatalf("requests = %v", got)
	}
	if jellyfin.userName != "owner" || jellyfin.userPassword != "their-own-password" || len(jellyfin.keys) != 0 {
		t.Fatal("somebody else's Jellyfin was changed")
	}

	// Once the person sets the password shown, the same run goes through.
	jellyfin.userName, jellyfin.userPassword = "admin", password
	mustRunStep(t, env, "jellyfin.setup")
	if len(jellyfin.keys) != 1 || credential(env, CredentialJellyfinAPIKey) != jellyfin.keys[0].AccessToken {
		t.Fatalf("keys = %+v", jellyfin.keys)
	}
}

func TestJellyfinSetupTreatsAnAdminThatIsNotAnAdministratorAsSomeoneElses(t *testing.T) {
	jellyfin := newFakeJellyfin(t)
	env := newFake("complete", map[string]string{"jellyfin": jellyfin.server.URL})
	password, _ := env.Credential(t.Context(), CredentialJellyfinAdmin)
	jellyfin.wizardDone, jellyfin.userName, jellyfin.userPassword, jellyfin.administrator = true, "admin", password, false

	_, err := runStep(t, env, "jellyfin.setup")
	wantAction(t, err, actionJellyfinTakenOver)
	if len(jellyfin.sessions) != 0 {
		t.Fatal("the refused account's session was left open")
	}
}

func TestJellyfinSetupMakesItsKeyAgainWithoutASecondOne(t *testing.T) {
	jellyfin := newFakeJellyfin(t)
	env := newFake("complete", map[string]string{"jellyfin": jellyfin.server.URL})
	mustRunStep(t, env, "jellyfin.setup")
	first := credential(env, CredentialJellyfinAPIKey)

	// Somebody revokes the key in Jellyfin's dashboard.
	jellyfin.keys = nil
	jellyfin.take()
	result := mustRunStep(t, env, "jellyfin.setup")
	second := credential(env, CredentialJellyfinAPIKey)
	if second == "" || second == first || len(jellyfin.keys) != 1 || jellyfin.keys[0].AccessToken != second {
		t.Fatalf("keys = %+v, stored %q", jellyfin.keys, second)
	}
	if !strings.Contains(result.Message, "new API key") {
		t.Fatalf("message = %q", result.Message)
	}

	// The stack loses its copy but Jellyfin still lists the key: it is taken
	// up again, not joined by another.
	_ = env.SetCredential(t.Context(), CredentialJellyfinAPIKey, "")
	jellyfin.keys = append(jellyfin.keys, jellyfinKey{AccessToken: "somebody-elses", AppName: "Seerr"})
	jellyfin.take()
	mustRunStep(t, env, "jellyfin.setup")
	if len(jellyfin.keys) != 2 || credential(env, CredentialJellyfinAPIKey) != second {
		t.Fatalf("keys = %+v", jellyfin.keys)
	}
	if got := jellyfin.take(); slices.Contains(got, "POST /Auth/Keys") {
		t.Fatalf("a second key was made: %v", got)
	}
}

func TestJellyfinSetupWaitsForAServerThatIsStillStarting(t *testing.T) {
	jellyfin := newFakeJellyfin(t)
	env := newFake("complete", map[string]string{"jellyfin": jellyfin.server.URL})
	jellyfin.force("GET /System/Info/Public", 503, "<html>Server is still starting</html>")

	_, err := runStep(t, env, "jellyfin.setup")
	if err == nil || stacks.IsPermanent(err) || !strings.Contains(err.Error(), "still starting") {
		t.Fatalf("expected a retryable starting error, got %v", err)
	}
	if len(env.Creds.Values) != 0 {
		t.Fatalf("a credential was made for a server that has not answered: %v", len(env.Creds.Values))
	}
}

func TestJellyfinSetupReportsARefusedKeyWithoutItsSecrets(t *testing.T) {
	jellyfin := newFakeJellyfin(t)
	env := newFake("complete", map[string]string{"jellyfin": jellyfin.server.URL})
	password, _ := env.Credential(t.Context(), CredentialJellyfinAdmin)
	// An upstream that echoes what it was sent.
	jellyfin.force("POST /Auth/Keys", 500, "could not make a key for "+password)

	_, err := runStep(t, env, "jellyfin.setup")
	if err == nil || !strings.Contains(err.Error(), "did not create an API key: HTTP 500") {
		t.Fatalf("error = %v", err)
	}
	wantNoSecret(t, err.Error(), password)
	if len(jellyfin.sessions) != 0 {
		t.Fatal("the session was left open after the failure")
	}
}

func TestJellyfinLibrariesAreMadeOnceAndFoundByFolder(t *testing.T) {
	jellyfin := newFakeJellyfin(t)
	env := newFake("complete", map[string]string{"jellyfin": jellyfin.server.URL})
	mustRunStep(t, env, "jellyfin.setup")
	jellyfin.take()

	result := mustRunStep(t, env, "jellyfin.libraries")
	if len(jellyfin.folders) != 2 {
		t.Fatalf("libraries = %+v", jellyfin.folders)
	}
	for index, want := range []jellyfinFolder{
		{Name: "Movies", CollectionType: "movies", Locations: []string{MoviesLibrary}},
		{Name: "Shows", CollectionType: "tvshows", Locations: []string{TVLibrary}},
	} {
		got := jellyfin.folders[index]
		if got.Name != want.Name || got.CollectionType != want.CollectionType || !slices.Equal(got.Locations, want.Locations) {
			t.Errorf("library %d = %+v", index, got)
		}
	}
	if result.Message != "Movies at /data/media/movies, Shows at /data/media/tv" {
		t.Errorf("message = %q", result.Message)
	}
	if result.Outputs["moviesLibraryId"] != "f137a2dd21bbc1b99aa5c0f6bf02a805" || result.Outputs["showsLibraryId"] != "a656b907eb3a73532e40e44b968d0225" {
		t.Errorf("outputs = %v", result.Outputs)
	}
	if got := jellyfin.take(); !slices.Equal(got, []string{"GET /Library/VirtualFolders", "POST /Library/VirtualFolders", "POST /Library/VirtualFolders", "GET /Library/VirtualFolders"}) {
		t.Fatalf("first run requests = %v", got)
	}

	mustRunStep(t, env, "jellyfin.libraries")
	if got := jellyfin.take(); !slices.Equal(got, []string{"GET /Library/VirtualFolders"}) {
		t.Fatalf("the second run made %v", got)
	}
}

func TestJellyfinLibrariesKeepALibrarySomebodyMadeOverTheSameFolder(t *testing.T) {
	jellyfin := newFakeJellyfin(t)
	env := newFake("complete", map[string]string{"jellyfin": jellyfin.server.URL})
	mustRunStep(t, env, "jellyfin.setup")
	// Their films under their own name, with a trailing slash as Jellyfin
	// sometimes keeps one, and an unrelated library called Shows.
	jellyfin.folders = []jellyfinFolder{
		{Name: "Films", Locations: []string{"/data/media/movies/"}, CollectionType: "movies", ItemID: "0000000000000000000000000000aaaa"},
		{Name: "Shows", Locations: []string{"/somewhere/else"}, CollectionType: "tvshows", ItemID: "0000000000000000000000000000bbbb"},
	}

	result := mustRunStep(t, env, "jellyfin.libraries")
	if len(jellyfin.folders) != 3 {
		t.Fatalf("libraries = %+v", jellyfin.folders)
	}
	if jellyfin.folders[0].Name != "Films" || jellyfin.folders[1].Locations[0] != "/somewhere/else" {
		t.Fatalf("an existing library was changed: %+v", jellyfin.folders)
	}
	// Jellyfin gives the second Shows a name of its own; it is the stack's
	// by its folder.
	if jellyfin.folders[2].Name != "Shows2" || jellyfin.folders[2].Locations[0] != TVLibrary {
		t.Fatalf("the new library = %+v", jellyfin.folders[2])
	}
	if result.Outputs["moviesLibraryId"] != "0000000000000000000000000000aaaa" || result.Message != "Films at /data/media/movies, Shows2 at /data/media/tv" {
		t.Fatalf("result = %+v", result)
	}
	jellyfin.take()
	mustRunStep(t, env, "jellyfin.libraries")
	if got := jellyfin.take(); !slices.Equal(got, []string{"GET /Library/VirtualFolders"}) || len(jellyfin.folders) != 3 {
		t.Fatalf("the second run made %v, libraries %d", got, len(jellyfin.folders))
	}
}

func TestJellyfinLibrariesReportARefusalAndRetry(t *testing.T) {
	jellyfin := newFakeJellyfin(t)
	env := newFake("complete", map[string]string{"jellyfin": jellyfin.server.URL})
	mustRunStep(t, env, "jellyfin.setup")
	jellyfin.force("POST /Library/VirtualFolders", 400, "Error processing request.")

	_, err := runStep(t, env, "jellyfin.libraries")
	if err == nil || stacks.IsPermanent(err) || !strings.Contains(err.Error(), "did not create the Movies library at /data/media/movies: HTTP 400") {
		t.Fatalf("error = %v", err)
	}
	wantNoSecret(t, err.Error(), credential(env, CredentialJellyfinAPIKey))
}

func TestJellyfinLibrariesNeedTheKeyTheSetupStepStores(t *testing.T) {
	jellyfin := newFakeJellyfin(t)
	env := newFake("complete", map[string]string{"jellyfin": jellyfin.server.URL})
	if _, err := runStep(t, env, "jellyfin.libraries"); err == nil || !strings.Contains(err.Error(), "no Jellyfin API key") {
		t.Fatalf("error = %v", err)
	}
	if got := jellyfin.take(); len(got) != 0 {
		t.Fatalf("requests without a key: %v", got)
	}
}
