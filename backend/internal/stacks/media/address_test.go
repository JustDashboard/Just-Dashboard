package media

import (
	"slices"
	"strings"
	"testing"
)

// Radarr, Sonarr and Prowlarr take their public address as their
// application URL in the same save as the sign-in, once; a new address is
// one save more, and an application served at no address is left as it is.
func TestServarrTakesItsPublicAddress(t *testing.T) {
	stack := newArrFakeStack(t)
	stack.env.URLs = map[string]string{"radarr": "https://media-radarr.example.test"}
	mustRunStep(t, stack.env, "radarr.auth")
	mustRunStep(t, stack.env, "sonarr.auth")
	if stack.radarr.host["applicationUrl"] != "https://media-radarr.example.test" || stack.sonarr.host["applicationUrl"] != "" {
		t.Fatalf("application URLs: radarr %v, sonarr %v", stack.radarr.host["applicationUrl"], stack.sonarr.host["applicationUrl"])
	}
	arrWant(t, stack.radarr.written(), []string{"PUT config/host/1"})
	stack.forget()
	result := mustRunStep(t, stack.env, "radarr.auth")
	arrWant(t, stack.radarr.written(), nil)
	if !strings.Contains(result.Message, "already") || !strings.Contains(result.Message, "https://media-radarr.example.test") {
		t.Fatalf("message = %q", result.Message)
	}
	stack.env.URLs["radarr"] = "https://films.example.test"
	mustRunStep(t, stack.env, "radarr.auth")
	arrWant(t, stack.radarr.written(), []string{"PUT config/host/1"})
	if stack.radarr.host["applicationUrl"] != "https://films.example.test" || stack.radarr.password != credential(stack.env, CredentialLogin) {
		t.Fatalf("after a new address: %v", stack.radarr.host["applicationUrl"])
	}
}

// qBittorrent works behind the proxy: reverse proxy support on with the
// networks its container is on as the trusted proxies, a proxy the person
// added kept, and nothing written again once it is so.
func TestQBittorrentAnswersAtItsAddressBehindTheProxy(t *testing.T) {
	qbittorrent, env := qbittorrentEnv(t)
	env.Subnets = []string{"172.30.0.0/16", "172.31.0.0/16"}
	env.URLs = map[string]string{"qbittorrent": "https://media-downloads.example.test"}
	qbittorrent.preferences["web_ui_reverse_proxies_list"] = "10.9.8.7"
	mustRunStep(t, env, "qbittorrent.auth")
	qbittorrent.take()

	result := mustRunStep(t, env, "qbittorrent.address")
	if qbittorrent.preferences["web_ui_reverse_proxy_enabled"] != true ||
		qbittorrent.preferences["web_ui_reverse_proxies_list"] != "10.9.8.7;172.30.0.0/16;172.31.0.0/16" {
		t.Fatalf("preferences = %v", qbittorrent.preferences)
	}
	if qbittorrent.preferences["web_ui_domain_list"] != "*" {
		t.Fatal("a Host list that admits every name was narrowed")
	}
	if !slices.Contains(qbittorrent.take(), "POST /api/v2/app/setPreferences") || !strings.Contains(result.Message, "media-downloads.example.test") {
		t.Fatalf("message = %q", result.Message)
	}
	mustRunStep(t, env, "qbittorrent.address")
	if slices.Contains(qbittorrent.take(), "POST /api/v2/app/setPreferences") {
		t.Fatal("the second run wrote the settings again")
	}

	// A Host list of the person's own gets the address added to it.
	qbittorrent.preferences["web_ui_domain_list"] = "qbt.home.test"
	mustRunStep(t, env, "qbittorrent.address")
	if qbittorrent.preferences["web_ui_domain_list"] != "qbt.home.test;media-downloads.example.test" || qbittorrent.refusals != 0 {
		t.Fatalf("domain list = %v", qbittorrent.preferences["web_ui_domain_list"])
	}
}

// Jellyfin trusts the networks its container is on as proxies, keeping one
// the person added, and saves its network settings whole.
func TestJellyfinTrustsTheProxyInFrontOfIt(t *testing.T) {
	jellyfin := newFakeJellyfin(t)
	env := newFake("complete", map[string]string{"jellyfin": jellyfin.server.URL})
	env.Subnets = []string{"172.30.0.0/16"}
	mustRunStep(t, env, "jellyfin.setup")
	jellyfin.network["KnownProxies"] = []any{"192.0.2.10"}
	jellyfin.take()

	mustRunStep(t, env, "jellyfin.proxy")
	if got := jellyfin.network["KnownProxies"]; !slices.Equal(got.([]any), []any{"192.0.2.10", "172.30.0.0/16"}) || jellyfin.network["EnableRemoteAccess"] != true {
		t.Fatalf("network = %v", jellyfin.network)
	}
	if !slices.Contains(jellyfin.take(), "POST /System/Configuration/network") {
		t.Fatal("the network settings were not saved")
	}
	mustRunStep(t, env, "jellyfin.proxy")
	if slices.Contains(jellyfin.take(), "POST /System/Configuration/network") {
		t.Fatal("the second run saved them again")
	}
}

// Seerr takes its address, proxy support and Jellyfin's address, once.
func TestSeerrTakesItsAddressAndJellyfins(t *testing.T) {
	seerr, env := seerrEnv(t, "essential")
	mustRunStep(t, env, "seerr.setup")
	env.URLs = map[string]string{"seerr": "https://media-requests.example.test", "jellyfin": "https://media.example.test"}
	seerr.take()

	result := mustRunStep(t, env, "seerr.address")
	if seerr.main["applicationUrl"] != "https://media-requests.example.test" || seerr.network["trustProxy"] != true ||
		seerr.externalHostname != "https://media.example.test" {
		t.Fatalf("main %v, network %v, Jellyfin's address %q", seerr.main["applicationUrl"], seerr.network, seerr.externalHostname)
	}
	writes := 0
	for _, request := range seerr.take() {
		if strings.HasPrefix(request, "POST ") {
			writes++
		}
	}
	if writes != 3 || !strings.Contains(result.Message, "opens Jellyfin at https://media.example.test") {
		t.Fatalf("%d writes, message %q", writes, result.Message)
	}
	mustRunStep(t, env, "seerr.address")
	for _, request := range seerr.take() {
		if !strings.HasPrefix(request, "GET ") {
			t.Fatalf("the second run made %s", request)
		}
	}
	// Served at no address: only proxy support is the stack's to set.
	_, bare := seerrEnv(t, "essential")
	mustRunStep(t, bare, "seerr.setup")
	if result := mustRunStep(t, bare, "seerr.address"); strings.Contains(result.Message, "address is") {
		t.Fatalf("message = %q", result.Message)
	}
}

// Bazarr gets the subtitle sources that need no account, the person's own
// kept beside them; one the person turns off stays off.
func TestBazarrTurnsOnTheSourcesThatNeedNoAccount(t *testing.T) {
	stack := newArrFakeStack(t)
	bazarr := newBazarrFake(t, stack)
	stack.env.Lengths = map[string]int{CredentialLogin: generatedPasswordLength}
	bazarr.settings["general"]["enabled_providers"] = []any{"opensubtitlescom"}
	mustRunStep(t, stack.env, "bazarr.auth")
	bazarr.posts = nil

	result := mustRunStep(t, stack.env, "bazarr.providers")
	enabled := bazarrEnabledProviders(bazarr.settings)
	if !slices.Equal(enabled, append([]string{"opensubtitlescom"}, bazarrProvidersOwned...)) || len(bazarr.posts) != 1 {
		t.Fatalf("enabled = %v after %d saves", enabled, len(bazarr.posts))
	}
	if !strings.Contains(result.Message, "1 source of your own") || result.Outputs["turnedOn"] != "embeddedsubtitles,gestdown,tvsubtitles,yifysubtitles" {
		t.Fatalf("result = %+v", result)
	}
	bazarr.posts = nil
	mustRunStep(t, stack.env, "bazarr.providers")
	if len(bazarr.posts) != 0 {
		t.Fatal("the second run saved the sources again")
	}

	bazarr.settings["general"]["enabled_providers"] = []any{"opensubtitlescom", "embeddedsubtitles", "tvsubtitles", "yifysubtitles"}
	result = mustRunStep(t, stack.env, "bazarr.providers")
	if len(bazarr.posts) != 0 || !strings.Contains(result.Message, "gestdown stay off") {
		t.Fatalf("a source the person turned off was turned on again: %q", result.Message)
	}
}
