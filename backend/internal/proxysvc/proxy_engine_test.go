package proxysvc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shimOnlyPath makes dir the whole PATH, holding the given shell scripts, so
// what the service finds installed is exactly what the test put there.
func shimOnlyPath(t *testing.T, scripts map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	return dir
}

// The unit a request may start or stop is decided here, never by the caller:
// nginx's where nginx is installed, a host Caddy's otherwise, and none for a
// host whose proxy is only the Docker ingress.
func TestEngineIsTheInstalledProxysOwnUnit(t *testing.T) {
	service := New(t.TempDir(), filepath.Join(t.TempDir(), "Caddyfile"))
	for _, tc := range []struct {
		installed []string
		want      EngineUnit
	}{
		{[]string{"nginx", "caddy"}, EngineUnit{Unit: "nginx.service", Kind: KindNginx, Name: "nginx"}},
		{[]string{"caddy"}, EngineUnit{Unit: "caddy.service", Kind: KindCaddy, Name: "Caddy"}},
	} {
		scripts := map[string]string{}
		for _, name := range tc.installed {
			scripts[name] = "exit 0\n"
		}
		shimOnlyPath(t, scripts)
		got, err := service.Engine()
		if err != nil || got != tc.want {
			t.Fatalf("with %v installed: %+v, %v; want %+v", tc.installed, got, err, tc.want)
		}
	}
	shimOnlyPath(t, nil)
	if _, err := service.Engine(); !errors.Is(err, ErrNoEngineUnit) {
		t.Fatalf("with no proxy installed: %v, want ErrNoEngineUnit", err)
	}
}

// A start or restart over a configuration the engine refuses is never
// attempted: this host's nginx.service tests before it starts, so the restart
// would stop nginx and leave it down. The result carries nginx's own words.
func TestWithTestedConfigHoldsBackWhatTheTestRefuses(t *testing.T) {
	shimOnlyPath(t, map[string]string{"nginx": `echo 'nginx: [emerg] unknown directive "frobnicate" in /etc/nginx/sites-enabled/app:3' >&2
echo 'nginx: configuration file /etc/nginx/nginx.conf test failed' >&2
exit 1
`})
	service := New(t.TempDir(), filepath.Join(t.TempDir(), "Caddyfile"))
	started := 0
	res, err := service.WithTestedConfig(context.Background(), KindNginx, func() error {
		started++
		return nil
	})
	if !errors.Is(err, ErrInvalidConf) || started != 0 {
		t.Fatalf("got %v with %d starts, want ErrInvalidConf and none", err, started)
	}
	if res == nil || res.Valid || len(res.Diagnostics) != 1 || res.Diagnostics[0].Line != 3 ||
		!strings.Contains(res.Output, `unknown directive "frobnicate"`) {
		t.Fatalf("the refusal does not carry nginx's reason: %+v", res)
	}
}

// Once the test passes, the start runs exactly once, with the service lock
// still held — so no candidate Validate stages at a live path can slip in
// between the test and the start — and its failure is the caller's.
func TestWithTestedConfigStartsUnderTheLockOnceTheTestPasses(t *testing.T) {
	shimOnlyPath(t, map[string]string{"nginx": "echo 'nginx: configuration file /etc/nginx/nginx.conf test is successful' >&2\n"})
	service := New(t.TempDir(), filepath.Join(t.TempDir(), "Caddyfile"))
	started, locked := 0, false
	res, err := service.WithTestedConfig(context.Background(), KindNginx, func() error {
		started++
		if service.mu.TryLock() {
			service.mu.Unlock()
		} else {
			locked = true
		}
		return nil
	})
	if err != nil || !res.Valid || started != 1 || !locked {
		t.Fatalf("got %+v, %v; started %d times, lock held %v", res, err, started, locked)
	}
	failed := errors.New("Job for nginx.service failed")
	if _, err := service.WithTestedConfig(context.Background(), KindNginx, func() error { return failed }); !errors.Is(err, failed) {
		t.Fatalf("a start that failed returned %v", err)
	}
}
