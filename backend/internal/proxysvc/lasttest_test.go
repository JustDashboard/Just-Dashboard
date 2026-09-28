package proxysvc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// nginx warning about a duplicate name and passing, or refusing its
// configuration when the file named by JD_TEST_NGINX_BROKEN exists.
func shimWarningNginx(t *testing.T) string {
	t.Helper()
	broken := filepath.Join(t.TempDir(), "broken")
	shimOnlyPath(t, map[string]string{"nginx": `if [ "$1" = "-s" ]; then exit 0; fi
if [ -e '` + broken + `' ]; then
	echo 'nginx: [emerg] unknown directive "frobnicate" in /etc/nginx/sites-enabled/app:3' >&2
	echo 'nginx: configuration file /etc/nginx/nginx.conf test failed' >&2
	exit 1
fi
echo 'nginx: [warn] conflicting server name "a.test" on 0.0.0.0:80, ignored' >&2
echo 'nginx: configuration file /etc/nginx/nginx.conf test is successful' >&2
`})
	return broken
}

// Whichever command ran the test, it is the engine's last: Test config, a
// reload, and the test a start or restart runs first. A test that passed with
// a warning keeps the warning.
func TestEveryTestOfTheFilesOnDiskIsKeptAsTheLast(t *testing.T) {
	broken := shimWarningNginx(t)
	service := New(t.TempDir(), filepath.Join(t.TempDir(), "Caddyfile"))
	if _, ok := service.LastTest(KindNginx); ok {
		t.Fatal("a service that has run no test has a last one")
	}

	before := time.Now()
	if _, err := service.Test(context.Background(), KindNginx); err != nil {
		t.Fatal(err)
	}
	rec, ok := service.LastTest(KindNginx)
	if !ok || rec.Kind != KindNginx || !rec.Validation.Valid || rec.Validation.Warnings != 1 ||
		rec.CheckedAt.Before(before) || rec.CheckedAt.After(time.Now()) {
		t.Fatalf("after Test config: %+v, %v", rec, ok)
	}

	if err := os.WriteFile(broken, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reload(context.Background(), KindNginx); !errors.Is(err, ErrInvalidConf) {
		t.Fatalf("a reload over a broken file: %v", err)
	}
	if rec, _ := service.LastTest(KindNginx); rec.Validation.Valid || len(rec.Validation.Diagnostics) != 1 ||
		rec.Validation.Diagnostics[0].Line != 3 {
		t.Fatalf("a refused reload's test was not kept: %+v", rec.Validation)
	}

	if err := os.Remove(broken); err != nil {
		t.Fatal(err)
	}
	if _, err := service.WithTestedConfig(context.Background(), KindNginx, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if rec, _ := service.LastTest(KindNginx); !rec.Validation.Valid || rec.Validation.Warnings != 1 {
		t.Fatalf("a start's test was not kept: %+v", rec.Validation)
	}
	if _, ok := service.LastTest(KindCaddy); ok {
		t.Fatal("nginx's test was kept as Caddy's")
	}
}

// Two tests racing: the one that began later read the newer files, so the
// one that began earlier never replaces it, however late it finishes.
func TestAnOlderTestDoesNotReplaceANewerOne(t *testing.T) {
	service := New(t.TempDir(), filepath.Join(t.TempDir(), "Caddyfile"))
	now := time.Now()
	service.remember(KindNginx, now, &ValidationResult{Valid: true, Diagnostics: []Diagnostic{}})
	service.remember(KindNginx, now.Add(-time.Second), &ValidationResult{Valid: false, Diagnostics: []Diagnostic{}})
	if rec, _ := service.LastTest(KindNginx); !rec.Validation.Valid || !rec.CheckedAt.Equal(now) {
		t.Fatalf("the older test replaced the newer: %+v", rec)
	}
	service.remember(KindNginx, now.Add(time.Second), &ValidationResult{Valid: false, Diagnostics: []Diagnostic{}})
	if rec, _ := service.LastTest(KindNginx); rec.Validation.Valid {
		t.Fatalf("a newer test did not replace the kept one: %+v", rec)
	}
}

// What a caller does with the record it was handed — placing a warning, say —
// never changes the one kept, nor the result the test returned.
func TestTheKeptTestIsItsOwnCopy(t *testing.T) {
	service := New(t.TempDir(), filepath.Join(t.TempDir(), "Caddyfile"))
	res := &ValidationResult{Valid: true, Warnings: 1, Diagnostics: []Diagnostic{{
		Level: "warn", Message: "m", Claims: []NameClaim{{File: "/a", Line: 1}},
	}}}
	service.remember(KindNginx, time.Now(), res)
	res.Diagnostics[0].Message = "changed by the caller"
	res.Diagnostics[0].Claims[0].File = "/changed"

	rec, _ := service.LastTest(KindNginx)
	if d := rec.Validation.Diagnostics[0]; d.Message != "m" || d.Claims[0].File != "/a" {
		t.Fatalf("the kept test changed with the caller's: %+v", d)
	}
	rec.Validation.Diagnostics[0].Claims[0].File = "/changed"
	if again, _ := service.LastTest(KindNginx); again.Validation.Diagnostics[0].Claims[0].File != "/a" {
		t.Fatal("a reader's copy shares the kept test's claims")
	}
}

// A save the editor makes is tested again with the file in place and kept,
// so a warning fixed from the editor clears without another press of Test;
// a save the second test refuses is put back, and the files on disk are then
// the ones the last record already describes.
func TestAConfigSaveKeepsTheTestOfTheFileInPlace(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "conf.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Passes both tests, then fails the next save's second one.
	runs := filepath.Join(t.TempDir(), "runs")
	shimOnlyPath(t, map[string]string{"nginx": `echo run >> '` + runs + `'
n=0
while read -r _; do n=$((n + 1)); done < '` + runs + `'
if [ "$n" -eq 4 ]; then
	echo 'nginx: [emerg] unknown directive "frobnicate" in /x:1' >&2
	exit 1
fi
echo 'nginx: configuration file /etc/nginx/nginx.conf test is successful' >&2
`})
	service := New(root, filepath.Join(root, "Caddyfile"))
	file := filepath.Join(root, "conf.d", "app.conf")
	if _, err := service.WriteConfig(context.Background(), KindNginx, file, "server {}\n"); err != nil {
		t.Fatal(err)
	}
	kept, ok := service.LastTest(KindNginx)
	if !ok || !kept.Validation.Valid {
		t.Fatalf("a save's test was not kept: %+v, %v", kept, ok)
	}
	if _, err := service.WriteConfig(context.Background(), KindNginx, file, "server { listen 81; }\n"); !errors.Is(err, ErrInvalidConf) {
		t.Fatalf("the second save: %v", err)
	}
	if again, _ := service.LastTest(KindNginx); !again.CheckedAt.Equal(kept.CheckedAt) {
		t.Fatalf("a save that was put back replaced the last test: %+v", again)
	}
}
