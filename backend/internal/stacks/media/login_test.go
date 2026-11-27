package media

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks/stackstest"
)

// loginStack is every application that signs in with the stack's login,
// over fakes, in one environment.
type loginStack struct {
	*arrFakeStack
	jellyfin    *fakeJellyfin
	qbittorrent *fakeQBittorrent
	bazarr      *bazarrFake
}

func newLoginStack(t *testing.T) *loginStack {
	t.Helper()
	stack := &loginStack{arrFakeStack: newArrFakeStack(t)}
	stack.bazarr = newBazarrFake(t, stack.arrFakeStack)
	stack.jellyfin, stack.qbittorrent = newFakeJellyfin(t), newFakeQBittorrent(t)
	stack.env.Services["jellyfin"] = stack.jellyfin.server.URL
	stack.env.Services["qbittorrent"] = stack.qbittorrent.server.URL
	stack.env.Lengths = stacks.CredentialLengths(Manifest())
	stack.qbittorrent.attach(stack.env)
	return stack
}

// pass runs every step that signs in with the login, and the check after
// them, as one pass does.
func (s *loginStack) pass(t *testing.T) {
	t.Helper()
	for _, id := range append(slices.Clone(loginSteps), "login.verify") {
		mustRunStep(t, s.env, id)
	}
}

// changing is every request of the fakes since the last call that is not a
// read, a sign-in or a sign-out.
func (s *loginStack) changing() []string {
	var writes []string
	harmless := []string{"POST /Users/AuthenticateByName", "POST /Sessions/Logout", "POST /api/v2/auth/login", "POST /api/v2/auth/logout"}
	for _, fake := range []*fakeApp{&s.jellyfin.fakeApp, &s.qbittorrent.fakeApp} {
		for _, request := range fake.take() {
			if !strings.HasPrefix(request, "GET ") && !slices.Contains(harmless, request) {
				writes = append(writes, request)
			}
		}
	}
	writes = append(writes, s.written()...)
	for range s.bazarr.posts {
		writes = append(writes, "bazarr: POST /api/system/settings")
	}
	s.bazarr.posts = nil
	s.forget()
	return writes
}

// A stack made before the one login, each application on its own password:
// the first pass makes Jellyfin's the login (Jellyfin and Seerr keep their
// sessions and saved passwords), brings every other application to it
// without one refused sign-in, and keeps nothing else; the second changes
// nothing.
func TestAStackFromBeforeTheOneLoginMovesToItOnItsFirstPass(t *testing.T) {
	stack := newLoginStack(t)
	old := map[string]string{
		legacyJellyfinAdmin: "JellyfinOwnPassword0000aa", legacyQBittorrent: "QBittorrentOwnPassword0a",
		legacyRadarrAdmin: "RadarrOwnPassword00000aa", legacySonarrAdmin: "SonarrOwnPassword00000aa",
		legacyProwlarrAdmin: "ProwlarrOwnPassword000aa", legacyBazarrAdmin: "BazarrOwnPassword00000aa",
	}
	stack.jellyfin.wizardDone, stack.jellyfin.userName, stack.jellyfin.userPassword = true, AdminUsername, old[legacyJellyfinAdmin]
	stack.jellyfin.keys = []jellyfinKey{{AccessToken: "0123456789abcdef0123456789abcdef", AppName: jellyfinKeyName}}
	stack.qbittorrent.password = old[legacyQBittorrent]
	stack.qbittorrent.writeConfig()
	for _, app := range stack.apps() {
		app.password = old[app.app.legacyCredential]
		app.host["authenticationMethod"], app.host["authenticationRequired"], app.host["username"] = "forms", "enabled", AdminUsername
	}
	stack.bazarr.settings["auth"] = map[string]any{"apikey": stack.bazarr.key, "type": "form", "username": AdminUsername,
		"password": bazarrPasswordHash(old[legacyBazarrAdmin])}
	stack.env.Creds.Values = map[string]string{CredentialJellyfinAPIKey: "0123456789abcdef0123456789abcdef", CredentialBazarrAPIKey: stack.bazarr.key}
	for name, value := range old {
		stack.env.Creds.Values[name] = value
	}

	stack.pass(t)
	login := credential(stack.env, CredentialLogin)
	if login != old[legacyJellyfinAdmin] {
		t.Fatal("the login did not take Jellyfin's administrator password")
	}
	if stack.jellyfin.passwordChanges != 0 || stack.jellyfin.signIns != 1 || stack.jellyfin.userPassword != login {
		t.Fatalf("Jellyfin: %d password changes, %d sign-ins", stack.jellyfin.passwordChanges, stack.jellyfin.signIns)
	}
	if stack.qbittorrent.password != login || stack.qbittorrent.refusals != 0 {
		t.Fatalf("qBittorrent is not on the login, after %d refused sign-ins", stack.qbittorrent.refusals)
	}
	for _, app := range stack.apps() {
		if app.password != login {
			t.Fatalf("%s is not on the login", app.app.title)
		}
	}
	if stack.bazarr.settings["auth"]["password"] != bazarrPasswordHash(login) {
		t.Fatal("Bazarr is not on the login")
	}
	var names []string
	for name := range stack.env.Creds.Values {
		names = append(names, name)
	}
	slices.Sort(names)
	if want := []string{CredentialBazarrAPIKey, CredentialJellyfinAPIKey, markerJellyfinLogin, CredentialLogin}; !slices.Equal(names, want) {
		t.Fatalf("the stack keeps %v; want %v", names, want)
	}
	wantNoSecret(t, strings.Join(stack.env.Lines, " "), login, old[legacyQBittorrent], old[legacyRadarrAdmin])
	stack.changing()

	stack.pass(t)
	if writes := stack.changing(); len(writes) != 0 {
		t.Fatalf("the second pass changed %v", writes)
	}
	if stack.qbittorrent.refusals != 0 || stack.jellyfin.signIns != 1 {
		t.Fatalf("the second pass signed in to Jellyfin %d times, qBittorrent refused %d", stack.jellyfin.signIns-1, stack.qbittorrent.refusals)
	}
}

// A new login reaches every application on the next pass: Jellyfin, known
// to be on the one before, is changed with its API key without trying the
// new one first; qBittorrent is signed in to with the one its settings hold;
// the one before is kept until the check after them all. A pass cut short
// halfway still finishes the next time.
func TestAChangedLoginReachesEveryApp(t *testing.T) {
	stack := newLoginStack(t)
	stack.pass(t)
	first := credential(stack.env, CredentialLogin)
	stack.changing()
	signIns := stack.jellyfin.signIns

	const second = "The-Second-Password-2026"
	if err := stacks.ChangeLogin(context.Background(), &stack.env.Creds, second); err != nil {
		t.Fatal(err)
	}
	if credential(stack.env, CredentialLoginPrevious) != first {
		t.Fatal("the login before was not kept")
	}
	// Cut short after Jellyfin.
	mustRunStep(t, stack.env, "jellyfin.setup")
	if stack.jellyfin.userPassword != second || stack.jellyfin.passwordChanges != 1 || stack.jellyfin.signIns != signIns+1 {
		t.Fatalf("Jellyfin: password changed %d times, %d sign-ins", stack.jellyfin.passwordChanges, stack.jellyfin.signIns-signIns)
	}
	stack.pass(t)
	for name, password := range map[string]string{
		"Jellyfin": stack.jellyfin.userPassword, "qBittorrent": stack.qbittorrent.password,
		"Radarr": stack.radarr.password, "Sonarr": stack.sonarr.password, "Prowlarr": stack.prowlarr.password,
	} {
		if password != second {
			t.Fatalf("%s is not on the new login", name)
		}
	}
	if stack.bazarr.settings["auth"]["password"] != bazarrPasswordHash(second) || stack.qbittorrent.refusals != 0 {
		t.Fatalf("Bazarr not on the new login, or qBittorrent refused %d sign-ins", stack.qbittorrent.refusals)
	}
	if _, found := stack.env.Creds.Values[CredentialLoginPrevious]; found {
		t.Fatal("the login before is still kept after every app took the new one")
	}
	stack.changing()
	stack.pass(t)
	if writes := stack.changing(); len(writes) != 0 {
		t.Fatalf("the pass after changed %v", writes)
	}
}

// A login the person chose when deploying seeds a new stack, if the
// dashboard's rule accepts it; a weak one is not used, and the stack says so.
func TestTheDeployTimePasswordSeedsTheLogin(t *testing.T) {
	for _, testCase := range []struct {
		chosen string
		used   bool
	}{{"Chosen-At-Deploy-2026", true}, {"weak", false}, {"", false}} {
		env := &stackstest.Fake{Vars: map[string]string{VariableLoginPassword: testCase.chosen}, Lengths: stacks.CredentialLengths(Manifest())}
		login, err := stackLogin(context.Background(), env)
		if err != nil {
			t.Fatal(err)
		}
		if (login == testCase.chosen) != testCase.used || login == "" {
			t.Fatalf("chosen %q gave a login of %d characters", testCase.chosen, len(login))
		}
		said := strings.Contains(strings.Join(env.Lines, " "), "was not used")
		if said != (testCase.chosen != "" && !testCase.used) {
			t.Fatalf("chosen %q: lines %v", testCase.chosen, env.Lines)
		}
		// Only while the stack has none: the variable is not read again.
		env.Vars[VariableLoginPassword] = "Another-Choice-2026"
		if again, _ := stackLogin(context.Background(), env); again != login {
			t.Fatal("a later value of the variable changed the login")
		}
	}
}
