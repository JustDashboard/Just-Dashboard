package media

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks/stackstest"
)

func qbittorrentEnv(t *testing.T) (*fakeQBittorrent, *stackstest.Fake) {
	t.Helper()
	qbittorrent := newFakeQBittorrent(t)
	env := newFake("complete", map[string]string{"qbittorrent": qbittorrent.server.URL})
	qbittorrent.attach(env)
	return qbittorrent, env
}

func TestQBittorrentAuthReplacesTheTemporaryPasswordOnce(t *testing.T) {
	qbittorrent, env := qbittorrentEnv(t)

	result := mustRunStep(t, env, "qbittorrent.auth")
	password := credential(env, CredentialLogin)
	if len(password) != generatedPasswordLength || qbittorrent.password != password {
		t.Fatalf("qBittorrent's password is not the stack's (%d characters stored)", len(password))
	}
	if qbittorrent.refusals != 0 || len(qbittorrent.sessions) != 0 {
		t.Fatalf("refused sign-ins = %d, sessions left open = %d", qbittorrent.refusals, len(qbittorrent.sessions))
	}
	if qbittorrent.preferences["bypass_local_auth"] != false || qbittorrent.preferences["bypass_auth_subnet_whitelist_enabled"] != false {
		t.Fatalf("authentication was loosened: %v", qbittorrent.preferences)
	}
	wantNoSecret(t, result.Message+strings.Join(env.Lines, " "), password, qbittorrent.temporary)
	want := []string{
		"GET /api/v2/app/version", "POST /api/v2/auth/login", "POST /api/v2/app/setPreferences", "POST /api/v2/auth/logout",
		"POST /api/v2/auth/login", "POST /api/v2/auth/logout",
	}
	if got := qbittorrent.take(); !slices.Equal(got, want) {
		t.Fatalf("first run requests:\n got %v\nwant %v", got, want)
	}

	result = mustRunStep(t, env, "qbittorrent.auth")
	if got := qbittorrent.take(); !slices.Equal(got, []string{"GET /api/v2/app/version", "POST /api/v2/auth/login", "POST /api/v2/auth/logout"}) {
		t.Fatalf("the second run made %v", got)
	}
	if credential(env, CredentialLogin) != password || qbittorrent.password != password || qbittorrent.refusals != 0 {
		t.Fatal("the second run changed the password or was refused")
	}
	if !strings.Contains(result.Message, "signs in with the stack's password") {
		t.Fatalf("message = %q", result.Message)
	}
}

func TestQBittorrentAuthUsesTheLastTemporaryPasswordInTheLog(t *testing.T) {
	qbittorrent, env := qbittorrentEnv(t)
	qbittorrent.temporary = "Zq8RestartA"
	env.LogsFunc = func(string) (string, error) {
		// The container was restarted: the first start's line is still there.
		return qbittorrentFirstLog + strings.ReplaceAll(qbittorrentFirstLog, "fJYXIRjS2", "Zq8RestartA"), nil
	}
	mustRunStep(t, env, "qbittorrent.auth")
	if qbittorrent.refusals != 0 || qbittorrent.password != credential(env, CredentialLogin) {
		t.Fatalf("refusals = %d", qbittorrent.refusals)
	}
}

func TestQBittorrentAuthAfterAnAttemptThatStoredAPasswordButNeverSetIt(t *testing.T) {
	qbittorrent, env := qbittorrentEnv(t)
	qbittorrent.force("POST /api/v2/app/setPreferences", 500, "")
	if _, err := runStep(t, env, "qbittorrent.auth"); err == nil || stacks.IsPermanent(err) {
		t.Fatalf("expected an error the runner retries, got %v", err)
	}
	password := credential(env, CredentialLogin)
	delete(qbittorrent.forced, "POST /api/v2/app/setPreferences")

	// The stored password is not yet qBittorrent's, which the settings file
	// shows; it is not tried, and the temporary one still works.
	mustRunStep(t, env, "qbittorrent.auth")
	if qbittorrent.refusals != 0 || qbittorrent.password != password {
		t.Fatalf("refusals = %d, password set = %v", qbittorrent.refusals, qbittorrent.password == password)
	}
}

func TestQBittorrentPasswordSetBySomeoneElseIsAnActionWithoutASignIn(t *testing.T) {
	for name, stored := range map[string]bool{"before the stack had a password": false, "after the stack set one": true} {
		t.Run(name, func(t *testing.T) {
			qbittorrent, env := qbittorrentEnv(t)
			if stored {
				mustRunStep(t, env, "qbittorrent.auth")
			}
			qbittorrent.password = "somebody-elses"
			qbittorrent.writeConfig()
			qbittorrent.take()

			_, err := runStep(t, env, "qbittorrent.auth")
			action := wantAction(t, err, actionQBittorrentPassword)
			if action.Service != "qbittorrent" {
				t.Fatalf("action = %+v", action)
			}
			// Five refusals shut the dashboard's address out for an hour, so a
			// password the settings file says is wrong is never tried.
			if got := qbittorrent.take(); !slices.Equal(got, []string{"GET /api/v2/app/version"}) || qbittorrent.refusals != 0 {
				t.Fatalf("requests = %v, refusals = %d", got, qbittorrent.refusals)
			}
			password := credential(env, CredentialLogin)
			if password == "" || qbittorrent.password != "somebody-elses" {
				t.Fatal("no password was made for the page to show, or theirs was changed")
			}
			wantNoSecret(t, action.Description, password)

			// The person sets the password shown; the next run goes through.
			qbittorrent.password = password
			qbittorrent.writeConfig()
			mustRunStep(t, env, "qbittorrent.auth")
		})
	}
}

func TestQBittorrentAuthARenamedAccountIsSomeoneElses(t *testing.T) {
	qbittorrent, env := qbittorrentEnv(t)
	mustRunStep(t, env, "qbittorrent.auth")
	qbittorrent.username = "owner"
	qbittorrent.writeConfig()
	qbittorrent.take()

	_, err := runStep(t, env, "qbittorrent.auth")
	wantAction(t, err, actionQBittorrentPassword)
	if qbittorrent.refusals != 0 {
		t.Fatalf("refusals = %d", qbittorrent.refusals)
	}
}

func TestQBittorrentAuthStopsWhenTheLogHasNoTemporaryPassword(t *testing.T) {
	qbittorrent, env := qbittorrentEnv(t)
	env.LogsFunc = func(string) (string, error) { return "[ls.io-init] done.\n", nil }

	_, err := runStep(t, env, "qbittorrent.auth")
	var action *stacks.ActionError
	if err == nil || errors.As(err, &action) || !strings.Contains(err.Error(), "restart the qbittorrent service") {
		t.Fatalf("error = %v", err)
	}
	if qbittorrent.refusals != 0 {
		t.Fatalf("refusals = %d", qbittorrent.refusals)
	}
}

func TestQBittorrentAuthDoesNotRetryARefusedTemporaryPassword(t *testing.T) {
	qbittorrent, env := qbittorrentEnv(t)
	qbittorrent.temporary = "another-one"

	_, err := runStep(t, env, "qbittorrent.auth")
	if !stacks.IsPermanent(err) || qbittorrent.refusals != 1 {
		t.Fatalf("error = %v, refusals = %d", err, qbittorrent.refusals)
	}
	wantNoSecret(t, err.Error(), "fJYXIRjS2")
}

func TestQBittorrentAuthSaysWhenTheDashboardIsShutOut(t *testing.T) {
	qbittorrent, env := qbittorrentEnv(t)
	mustRunStep(t, env, "qbittorrent.auth")
	qbittorrent.banned = true

	_, err := runStep(t, env, "qbittorrent.auth")
	if !stacks.IsPermanent(err) || !strings.Contains(err.Error(), "for an hour") {
		t.Fatalf("error = %v", err)
	}
}

func TestQBittorrentAuthWaitsForAWebInterfaceThatIsNotUp(t *testing.T) {
	qbittorrent, env := qbittorrentEnv(t)
	qbittorrent.server.Close()

	_, err := runStep(t, env, "qbittorrent.auth")
	if err == nil || stacks.IsPermanent(err) || !strings.Contains(err.Error(), "qBittorrent did not answer") {
		t.Fatalf("error = %v", err)
	}
}

// The settings file line below is the pinned image's own, written after its
// password was set to the one beside it through the API. It pins the format
// the offline comparison depends on.
func TestQBittorrentPasswordStateReadsTheImagesSettingsFile(t *testing.T) {
	const recorded = "[LegalNotice]\nAccepted=true\n\n[Preferences]\nConnection\\ResolvePeerCountries=true\n\n[WebUI]\nAddress=*\n" +
		"Password_PBKDF2=\"@ByteArray(0Yrq88a3sUYcrBS/JXwXaw==:v6/jHOdU1k1HWdnqfotuXSP0WsriBmtt8yB5by4PNUPzaB187hCJXCs+8PA9lhnj0KsSsk58X/E55ocG7iQ4tA==)\"\nServerDomains=*\n"
	if set, matches := qbittorrentPasswordState([]byte(recorded), "ExploreQb12345"); !set || !matches {
		t.Fatalf("set = %v, matches = %v", set, matches)
	}
	if set, matches := qbittorrentPasswordState([]byte(recorded), "ExploreQb12346"); !set || matches {
		t.Fatalf("a wrong password: set = %v, matches = %v", set, matches)
	}
}

func TestQBittorrentPasswordStateReadsEveryFormOfTheFile(t *testing.T) {
	qbittorrent, env := qbittorrentEnv(t)
	qbittorrent.password = "the-password"
	qbittorrent.writeConfig()
	current := env.Files["qbittorrent:"+qbittorrentConfigFile]
	line := qbittorrentWebSettings(current)["Password_PBKDF2"]
	for name, test := range map[string]struct {
		config       string
		set, matches bool
	}{
		"no password":                  {"[WebUI]\nAddress=*\nServerDomains=*\n", false, false},
		"the 5.x section":              {string(current), true, true},
		"the older prefixed key":       {"[Preferences]\nWebUI\\Port=8080\nWebUI\\Password_PBKDF2=" + line + "\n", true, true},
		"a renamed account":            {string(current) + "Username=owner\n", true, false},
		"a form this code cannot read": {"[WebUI]\nPassword_PBKDF2=something-new\n", true, false},
		"a key of that name elsewhere": {"[Other]\nPassword_PBKDF2=" + line + "\n", false, false},
	} {
		if set, matches := qbittorrentPasswordState([]byte(test.config), "the-password"); set != test.set || matches != test.matches {
			t.Errorf("%s: set = %v, matches = %v", name, set, matches)
		}
	}
}

func TestQBittorrentCategoriesAreSetOnceAndThenOnlyRead(t *testing.T) {
	qbittorrent, env := qbittorrentEnv(t)
	mustRunStep(t, env, "qbittorrent.auth")
	qbittorrent.categories["books"] = "/somewhere/books"
	qbittorrent.take()

	result := mustRunStep(t, env, "qbittorrent.categories")
	if qbittorrent.preferences["save_path"] != "/data/downloads" || qbittorrent.preferences["auto_tmm_enabled"] != true || qbittorrent.preferences["temp_path"] != "/data/downloads/incomplete" {
		t.Fatalf("preferences = %v", qbittorrent.preferences)
	}
	if qbittorrent.preferences["temp_path_enabled"] != false {
		t.Fatal("the incomplete folder was turned on")
	}
	want := map[string]string{"movies": MoviesDownload, "tv": TVDownload, "books": "/somewhere/books"}
	for name, path := range want {
		if qbittorrent.categories[name] != path {
			t.Errorf("category %s at %q", name, qbittorrent.categories[name])
		}
	}
	if len(qbittorrent.categories) != len(want) || len(qbittorrent.sessions) != 0 {
		t.Fatalf("categories = %v, sessions left open = %d", qbittorrent.categories, len(qbittorrent.sessions))
	}
	if result.Message != "Downloads go to /data/downloads; movies to /data/downloads/movies, tv to /data/downloads/tv" {
		t.Fatalf("message = %q", result.Message)
	}
	if writes := changing(qbittorrent.take()); !slices.Equal(writes, []string{"POST /api/v2/app/setPreferences", "POST /api/v2/torrents/createCategory", "POST /api/v2/torrents/createCategory"}) {
		t.Fatalf("first run changes = %v", writes)
	}

	mustRunStep(t, env, "qbittorrent.categories")
	got := qbittorrent.take()
	if writes := changing(got); len(writes) != 0 {
		t.Fatalf("the second run changed %v", writes)
	}
	if !slices.Equal(got, []string{"POST /api/v2/auth/login", "GET /api/v2/app/preferences", "GET /api/v2/torrents/categories", "POST /api/v2/auth/logout"}) {
		t.Fatalf("the second run made %v", got)
	}
}

func TestQBittorrentCategoriesMendOnlyWhatDiffers(t *testing.T) {
	qbittorrent, env := qbittorrentEnv(t)
	mustRunStep(t, env, "qbittorrent.auth")
	mustRunStep(t, env, "qbittorrent.categories")
	// Somebody moves a category and turns automatic management off.
	qbittorrent.categories["tv"] = "/data/elsewhere"
	qbittorrent.preferences["auto_tmm_enabled"] = false
	qbittorrent.take()

	result := mustRunStep(t, env, "qbittorrent.categories")
	if writes := changing(qbittorrent.take()); !slices.Equal(writes, []string{"POST /api/v2/torrents/editCategory"}) {
		t.Fatalf("changes = %v", writes)
	}
	if qbittorrent.categories["tv"] != TVDownload {
		t.Fatalf("tv at %q", qbittorrent.categories["tv"])
	}
	// Their choice of management mode stays, and the page is told what it means.
	if qbittorrent.preferences["auto_tmm_enabled"] != false || !strings.Contains(result.Message, "Automatic torrent management is off") {
		t.Fatalf("auto management = %v, message = %q", qbittorrent.preferences["auto_tmm_enabled"], result.Message)
	}
}

func TestQBittorrentCategoriesKeepAnIncompleteFolderSomebodyChose(t *testing.T) {
	qbittorrent, env := qbittorrentEnv(t)
	mustRunStep(t, env, "qbittorrent.auth")
	qbittorrent.preferences["temp_path"] = "/data/their/incomplete"
	mustRunStep(t, env, "qbittorrent.categories")
	if qbittorrent.preferences["temp_path"] != "/data/their/incomplete" {
		t.Fatalf("temp_path = %v", qbittorrent.preferences["temp_path"])
	}
}

func TestQBittorrentCategoriesReportARefusalAndSignOut(t *testing.T) {
	qbittorrent, env := qbittorrentEnv(t)
	mustRunStep(t, env, "qbittorrent.auth")
	qbittorrent.force("POST /api/v2/torrents/createCategory", 409, "Unable to create category")

	_, err := runStep(t, env, "qbittorrent.categories")
	if err == nil || stacks.IsPermanent(err) || !strings.Contains(err.Error(), "did not create the movies category: HTTP 409: Unable to create category") {
		t.Fatalf("error = %v", err)
	}
	if len(qbittorrent.sessions) != 0 {
		t.Fatal("the session was left open after the failure")
	}
}

func TestQBittorrentCategoriesDoNotRetryARefusedPassword(t *testing.T) {
	qbittorrent, env := qbittorrentEnv(t)
	mustRunStep(t, env, "qbittorrent.auth")
	qbittorrent.password = "changed-since"

	_, err := runStep(t, env, "qbittorrent.categories")
	if !stacks.IsPermanent(err) || qbittorrent.refusals != 1 {
		t.Fatalf("error = %v, refusals = %d", err, qbittorrent.refusals)
	}
}
