package main

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/clihost"
)

// The dashboard knows the server's jd by this line of its usage text
// (internal/clihost), and replaces an older one at boot only when it finds
// it: reworded here, every server would keep the jd it was installed with.
func TestTheUsageTextCarriesTheMarkerTheDashboardLooksFor(t *testing.T) {
	if !strings.Contains(usageText, clihost.Marker) {
		t.Fatalf("usageText no longer contains %q", clihost.Marker)
	}
}

// Flags may come before the command, after it, or after the positional
// words, and a flag that takes a value keeps its value wherever it is.
func TestFlagsAreReadWhereverTheyAreWritten(t *testing.T) {
	a := &app{env: func(string) string { return "" }}
	fs := a.newFlags("vars set", "vars set")
	env := fs.String("env", "", "")
	wait := fs.Bool("wait", false, "")
	positional, err := a.parse(fs, []string{"web", "--env", "Production", "KEY=-", "--wait", "OTHER=a=b", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(positional, " ") != "web KEY=- OTHER=a=b" || *env != "Production" || !*wait || !a.opts.json {
		t.Fatalf("positional %q env %q wait %v json %v", positional, *env, *wait, a.opts.json)
	}

	f := newFake(t)
	for _, args := range [][]string{
		{"deploy", "web", "--wait"},
		{"--quiet", "deploy", "--wait", "web"},
		{"--timeout", "1m", "deploy", "web", "--env", "production", "--wait"},
		{"deploy", "--env=Production", "WEB", "--wait"},
	} {
		code, stdout, stderr := jd(t, invocation{env: f.env(t)}, args...)
		if code != exitOK || !strings.Contains(stdout, "serving") {
			t.Errorf("%q: exit %d\n%s%s", args, code, stdout, stderr)
		}
	}
	if code, _, stderr := jd(t, invocation{env: f.env(t)}, "deploy", "web", "--nope"); code != exitUsage ||
		!strings.Contains(stderr, "flag provided but not defined") {
		t.Errorf("an unknown flag: exit %d %s", code, stderr)
	}
	if code, _, _ := jd(t, invocation{env: f.env(t)}, "vars", "set", "web", "NOEQUALS"); code != exitUsage {
		t.Errorf("a variable without a value: exit %d", code)
	}
	if code, _, _ := jd(t, invocation{env: f.env(t)}, "frobnicate"); code != exitUsage {
		t.Errorf("an unknown command: exit %d", code)
	}
	if code, stdout, _ := jd(t, invocation{env: f.env(t)}, "deploy", "-h"); code != exitOK || !strings.Contains(stdout, "--wait") {
		t.Errorf("-h: exit %d %s", code, stdout)
	}
}

// login keeps the URL and key where only the user can read them.
func TestLoginKeepsTheKeyInAPrivateFile(t *testing.T) {
	f := newFake(t)
	env := map[string]string{"XDG_CONFIG_HOME": t.TempDir(), "HOME": t.TempDir()}
	code, stdout, stderr := jd(t, invocation{env: env}, "login", f.server.URL+"/api/v1/", "--key", goodKey)
	if code != exitOK {
		t.Fatalf("login: exit %d %s %s", code, stdout, stderr)
	}
	if strings.Contains(stdout+stderr, goodKey) || !strings.Contains(stderr, maskKey(goodKey)) {
		t.Errorf("login printed the key, or not its prefix: %s %s", stdout, stderr)
	}
	path := configFile(env)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.Stat(strings.TrimSuffix(path, "/config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && (info.Mode().Perm() != 0o600 || dir.Mode().Perm() != 0o700) {
		t.Errorf("config %v in a directory %v; want 0600 in 0700", info.Mode().Perm(), dir.Mode().Perm())
	}
	var stored storedConfig
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &stored); err != nil || stored.URL != f.server.URL || stored.Key != goodKey {
		t.Fatalf("stored %s (%v)", raw, err)
	}
	// The stored credentials are used when the environment names none.
	if code, stdout, stderr := jd(t, invocation{env: env}, "deployments"); code != exitOK || !strings.Contains(stdout, "web") {
		t.Errorf("deployments after login: exit %d %s %s", code, stdout, stderr)
	}
	if code, _, _ := jd(t, invocation{env: env}, "logout"); code != exitOK {
		t.Errorf("logout: exit %d", code)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("logout left %s", path)
	}

	// The key can come from stdin, which keeps it out of shell history.
	if code, _, stderr := jd(t, invocation{env: env, stdin: goodKey + "\n"}, "login", f.server.URL, "--key", "-"); code != exitOK {
		t.Errorf("login with the key on stdin: exit %d %s", code, stderr)
	}
	if code, _, stderr := jd(t, invocation{env: env}, "login", f.server.URL); code != exitUsage || !strings.Contains(stderr, "--key") {
		t.Errorf("login without a key: exit %d %s", code, stderr)
	}
	// A key the dashboard refuses is not stored.
	other := map[string]string{"XDG_CONFIG_HOME": t.TempDir(), "HOME": t.TempDir()}
	if code, _, _ := jd(t, invocation{env: other}, "login", f.server.URL, "--key", "vpsd_revoked0000000000"); code != exitNoSession {
		t.Errorf("login with a refused key: exit %d", code)
	}
	if _, err := os.Stat(configFile(other)); !os.IsNotExist(err) {
		t.Error("a refused key was stored")
	}
}

// JD_URL and JD_KEY win over the stored file, which is how CI passes them.
func TestTheEnvironmentOverridesTheStoredLogin(t *testing.T) {
	f := newFake(t)
	env := f.env(t)
	if err := saveConfig(configFile(env), true, storedConfig{URL: "http://127.0.0.1:1", Key: "vpsd_stale000000000000"}); err != nil {
		t.Fatal(err)
	}
	if code, stdout, stderr := jd(t, invocation{env: env}, "deployments"); code != exitOK || !strings.Contains(stdout, "Api") {
		t.Fatalf("exit %d %s %s", code, stdout, stderr)
	}
	// Without them, the stored (here unreachable) dashboard is used.
	delete(env, "JD_URL")
	delete(env, "JD_KEY")
	if code, _, stderr := jd(t, invocation{env: env}, "deployments"); code != exitRefused || !strings.Contains(stderr, "could not reach") {
		t.Errorf("stored login: exit %d %s", code, stderr)
	}
	// And with neither, jd says how to log in.
	bare := map[string]string{"XDG_CONFIG_HOME": t.TempDir(), "HOME": t.TempDir()}
	if code, _, stderr := jd(t, invocation{env: bare}, "deployments"); code != exitNoSession || !strings.Contains(stderr, "jd login") {
		t.Errorf("not logged in: exit %d %s", code, stderr)
	}
}

// --json is the server's own answer, untouched, for scripts.
func TestJSONIsTheServersAnswer(t *testing.T) {
	f := newFake(t)
	code, stdout, _ := jd(t, invocation{env: f.env(t)}, "deployments", "--json")
	if code != exitOK || strings.TrimSpace(stdout) != string(f.fleetBody) {
		t.Fatalf("exit %d\n%s\nwant\n%s", code, stdout, f.fleetBody)
	}
	for _, args := range [][]string{
		{"--json", "vars", "ls", "web"}, {"runs", "web", "--json"}, {"releases", "web", "--json"},
		{"config", "get", "web", "--json"}, {"logs", "web", "--json"},
	} {
		code, stdout, stderr := jd(t, invocation{env: f.env(t)}, args...)
		if code != exitOK {
			t.Errorf("%q: exit %d %s", args, code, stderr)
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
			if !json.Valid([]byte(line)) {
				t.Errorf("%q printed a line that is not JSON: %q", args, line)
			}
		}
	}
}

// A plain-http address off loopback would carry the key in the clear.
func TestPlainHTTPIsRefusedOffLoopback(t *testing.T) {
	env := map[string]string{"JD_URL": "http://dash.example.com", "JD_KEY": goodKey, "XDG_CONFIG_HOME": t.TempDir()}
	code, _, stderr := jd(t, invocation{env: env}, "deployments")
	if code != exitUsage || !strings.Contains(stderr, "plain http") {
		t.Fatalf("exit %d %s", code, stderr)
	}
	for raw, want := range map[string]string{
		"dash.example.com":                    "https://dash.example.com",
		"https://dash.example.com:8443/":      "https://dash.example.com:8443",
		"https://dash.example.com/api/v1/":    "https://dash.example.com",
		"http://127.0.0.1:8443/prefix/api/v1": "http://127.0.0.1:8443/prefix",
	} {
		if got, err := normaliseURL(raw); err != nil || got != want {
			t.Errorf("normaliseURL(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	if maskKey(goodKey) != "vpsd_testkey0…" || maskKey("short") != "…" {
		t.Errorf("maskKey: %q %q", maskKey(goodKey), maskKey("short"))
	}
}
