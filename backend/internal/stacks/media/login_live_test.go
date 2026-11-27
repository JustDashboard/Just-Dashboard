package media

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
)

// The live test of the one login: the pinned images set up as manifest
// 1.0.0 left them, each application on a password of its own, then the
// first pass of this manifest, a change of the login, and a pass after each
// that must change nothing. It runs with the other live tests:
//
//	go test -c -o /tmp/media.test ./internal/stacks/media
//	sudo -n env JD_STACKS_LIVE=1 PATH=$PATH /tmp/media.test -test.run LiveOneLogin -test.v
//
// Everything it creates is named jdms-d1-login-… and removed when it ends.
func TestLiveOneLoginMigratesAStackAndFollowsAChange(t *testing.T) {
	stack := startLiveStack(t, "login", "complete", []string{"jellyfin", "seerr", "qbittorrent", "radarr", "sonarr", "prowlarr", "bazarr"})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	stack.waitReady(ctx)
	env := stack.env
	store := env.Credentials

	// Manifest 1.0.0's state, made with this manifest's own steps: each
	// application set up while the login is its own password, which is then
	// kept under that application's old name.
	own := map[string]string{}
	for _, name := range []string{legacyJellyfinAdmin, legacyQBittorrent, legacyRadarrAdmin, legacySonarrAdmin, legacyProwlarrAdmin, legacyBazarrAdmin} {
		password, err := stacks.GeneratePassword(generatedPasswordLength)
		if err != nil {
			t.Fatal(err)
		}
		own[name] = password
	}
	asBefore := func(legacy string, steps ...string) {
		t.Helper()
		if err := store.Put(ctx, CredentialLogin, own[legacy]); err != nil {
			t.Fatal(err)
		}
		for _, id := range steps {
			stack.mustRun(ctx, id)
		}
		for _, name := range []string{CredentialLogin, markerJellyfinLogin} {
			if err := store.Delete(ctx, name); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.Put(ctx, legacy, own[legacy]); err != nil {
			t.Fatal(err)
		}
	}
	stack.mustRun(ctx, "storage.folders")
	asBefore(legacyJellyfinAdmin, "jellyfin.setup", "jellyfin.libraries", "seerr.setup", "seerr.policy")
	asBefore(legacyQBittorrent, "qbittorrent.auth")
	asBefore(legacyRadarrAdmin, "radarr.auth")
	asBefore(legacySonarrAdmin, "sonarr.auth")
	asBefore(legacyProwlarrAdmin, "prowlarr.auth")
	asBefore(legacyBazarrAdmin, "bazarr.auth")
	for name, password := range map[string]string{"jellyfin": own[legacyJellyfinAdmin], "qbittorrent": own[legacyQBittorrent],
		"radarr": own[legacyRadarrAdmin], "bazarr": own[legacyBazarrAdmin]} {
		if !liveSignsIn(ctx, t, stack, name, password) {
			t.Fatalf("before the pass, %s does not take its own password", name)
		}
	}
	// Each password is different, so a match later is the migration's.
	if own[legacyQBittorrent] == own[legacyJellyfinAdmin] {
		t.Fatal("two generated passwords are the same")
	}
	stack.env.take()

	pass := func(name string) []string {
		t.Helper()
		for _, id := range append(slices.Clone(loginSteps), "seerr.setup", "login.verify") {
			stack.mustRun(ctx, id)
		}
		requests := stack.env.take()
		t.Logf("%s pass changed things with %v", name, changing(requests))
		return requests
	}

	// The first pass of the one login.
	requests := pass("migrating")
	login, _, _ := store.Get(ctx, CredentialLogin)
	if login != own[legacyJellyfinAdmin] {
		t.Fatal("the login is not Jellyfin's administrator password")
	}
	if slices.Contains(requests, "POST /Users/Password") {
		t.Fatal("Jellyfin's password was changed, signing out its devices, though it already was the login")
	}
	liveAllSignIn(ctx, t, stack, login)
	for name := range own {
		if _, found, _ := store.Get(ctx, name); found {
			t.Errorf("the old %s is still kept", name)
		}
	}
	liveNoQBittorrentRefusal(ctx, t, stack)
	if writes := changing(pass("second")); len(writes) != 0 {
		t.Fatalf("the pass after the migration changed %v", writes)
	}

	// A change of the login, as PUT /deploy/{id}/stack/login makes it.
	const changed = "Changed-Through-The-Page-2026"
	if err := stacks.ChangeLogin(ctx, store, changed); err != nil {
		t.Fatal(err)
	}
	if previous, _, _ := store.Get(ctx, CredentialLoginPrevious); previous != login {
		t.Fatal("the login before the change is not kept for the pass")
	}
	requests = pass("after the change")
	if !slices.Contains(requests, "POST /Users/Password") {
		t.Fatal("Jellyfin's password was not changed")
	}
	liveAllSignIn(ctx, t, stack, changed)
	if _, found, _ := store.Get(ctx, CredentialLoginPrevious); found {
		t.Fatal("the login before is still kept after every app took the new one")
	}
	// Seerr's owner signs in with Jellyfin's account, so with the new one.
	seerr, err := seerrWithKey(ctx, env)
	if err != nil {
		t.Fatal(err)
	}
	anonymous := &seerrAPI{env: env, base: seerr.base}
	if reply, err := anonymous.call(ctx, http.MethodPost, "/auth/jellyfin", map[string]any{"username": AdminUsername, "password": changed}); err != nil || reply.Status != http.StatusOK {
		t.Fatalf("Seerr's owner with the new login: %v %d", err, reply.Status)
	}
	if owner, err := seerr.isOwner(ctx); err != nil || !owner {
		t.Fatalf("Seerr's key after the change: %v %v", owner, err)
	}
	liveNoQBittorrentRefusal(ctx, t, stack)
	stack.env.take()
	if writes := changing(pass("third")); len(writes) != 0 {
		t.Fatalf("the pass after the change changed %v", writes)
	}
}

// liveAllSignIn signs in to every application with password, each through
// its own sign-in, and fails the test for any that refuses it.
func liveAllSignIn(ctx context.Context, t *testing.T, stack *liveStack, password string) {
	t.Helper()
	for _, service := range []string{"jellyfin", "qbittorrent", "radarr", "sonarr", "prowlarr", "bazarr"} {
		if !liveSignsIn(ctx, t, stack, service, password) {
			t.Errorf("%s does not take the login", service)
		}
	}
	stack.env.take()
}

func liveSignsIn(ctx context.Context, t *testing.T, stack *liveStack, service, password string) bool {
	t.Helper()
	env := stack.env
	switch service {
	case "jellyfin":
		base, _ := env.BaseURL(ctx, "jellyfin", PortJellyfin)
		api := &jellyfinAPI{env: env, base: base}
		token, ok, err := jellyfinSignIn(ctx, api, password)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			session := &jellyfinAPI{env: env, base: base, token: token}
			_, _ = session.call(ctx, http.MethodPost, "/Sessions/Logout", nil, nil)
		}
		return ok
	case "qbittorrent":
		base, _ := env.BaseURL(ctx, "qbittorrent", PortQBittorrent)
		api := &qbittorrentAPI{env: env, base: base}
		config, err := env.ReadFile(ctx, "qbittorrent", qbittorrentConfigFile)
		if err != nil {
			t.Fatal(err)
		}
		// Asked offline first: a refused sign-in here would be one of five.
		if _, matches := qbittorrentPasswordState(config, password); !matches {
			return false
		}
		outcome, err := api.signIn(ctx, password)
		if err != nil {
			t.Fatal(err)
		}
		api.signOut(ctx)
		return outcome == qbittorrentAccepted
	case "bazarr":
		base, _ := env.BaseURL(ctx, "bazarr", PortBazarr)
		ok, err := (&bazarrClient{env: env, base: base}).signIn(ctx, AdminUsername, password)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	app := map[string]arrApp{"radarr": arrRadarr, "sonarr": arrSonarr, "prowlarr": arrProwlarr}[service]
	client, err := newArrClient(ctx, env, app)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := client.signIn(ctx, AdminUsername, password)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// liveNoQBittorrentRefusal fails the test if qBittorrent logged a refused
// sign-in: five shut the dashboard out for an hour.
func liveNoQBittorrentRefusal(ctx context.Context, t *testing.T, stack *liveStack) {
	t.Helper()
	if logs := liveQBittorrentLog(ctx, t, stack); strings.Contains(strings.ToLower(logs), "login failure") {
		t.Fatalf("qBittorrent refused a sign-in:\n%s", logs)
	}
}

// liveQBittorrentLog is qBittorrent's own log, where it records each refused
// sign-in with the address it counts it against, and what the container
// printed besides.
func liveQBittorrentLog(ctx context.Context, t *testing.T, stack *liveStack) string {
	t.Helper()
	printed, err := stack.env.Logs(ctx, "qbittorrent", 2000)
	if err != nil {
		t.Fatal(err)
	}
	file, _ := stack.env.ReadFile(ctx, "qbittorrent", "/config/qBittorrent/logs/qbittorrent.log")
	return string(file) + "\n" + printed
}
