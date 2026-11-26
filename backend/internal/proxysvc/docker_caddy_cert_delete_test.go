package proxysvc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const letsEncryptDirectory = "acme-v02.api.letsencrypt.org-directory"

// ingressShim is containerShim for an ingress that is asked what it serves:
// besides running a fixed script over the directory standing in for the
// container (paths under /config and /data), it answers Caddy's admin API
// from admin.json there — failing while that file is missing, as a Caddy
// that cannot be asked does — and `caddy adapt` with the name a route's
// block opens on.
func ingressShim(t *testing.T) (root, log string) {
	t.Helper()
	bin := t.TempDir()
	root, log = t.TempDir(), filepath.Join(bin, "docker.log")
	script := `#!/bin/sh
echo "$*" >> "` + log + `"
[ "$1" = exec ] || exit 1
shift 3
case "$1" in
wget) cat "` + root + `/admin.json"; exit $? ;;
caddy) printf '{"host":["%s"]}' "$(sed -n 's/^\([a-z0-9*.-]*\) {$/\1/p' | head -n 1)"; exit 0 ;;
esac
[ "$1" = sh ] && [ "$2" = -c ] || exit 1
body=$3
shift 4
n=$#
while [ "$n" -gt 0 ]; do
	arg=$1
	shift
	case "$arg" in /config/*|/data/*) arg="` + root + `$arg" ;; esac
	set -- "$@" "$arg"
	n=$((n-1))
done
sh -c "$body" sh "$@" | sed "s#` + root + `/#/#g"
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return root, log
}

// ingressHolding is an ingress holding a certificate for each name, with a route
// serving each of routed, and a service that finds it.
func ingressHolding(t *testing.T, held, routed []string) (root, log string, service *Service) {
	t.Helper()
	useLetsencryptDir(t, t.TempDir())
	root, log = ingressShim(t)
	for _, name := range held {
		certPEM, _, _ := selfSigned(t, name, time.Now().Add(60*24*time.Hour))
		writeFile(t, filepath.Join(root, caddyCertificatePath(name)), certPEM)
	}
	hosts := []string{}
	for i, name := range routed {
		route := "# Managed by Just Dashboard\n" + name + " {\n  respond 204\n}\n"
		writeFile(t, filepath.Join(root, "config", "just-dashboard", "routes", "just-dashboard-env-"+string(rune('1'+i))+".conf.caddy"), route)
		hosts = append(hosts, `"`+name+`"`)
	}
	if err := os.MkdirAll(filepath.Join(root, "config", "just-dashboard", "routes"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "admin.json"), `{"host":[`+strings.Join(hosts, ",")+`]}`)
	service = NewWithDockerIngress(t.TempDir(), filepath.Join(t.TempDir(), "Caddyfile")).
		WithContainerReader(&fakeContainers{running: ingressInspect})
	return root, log, service
}

// caddyCertificatePath is where Caddy files the certificate for a name.
func caddyCertificatePath(name string) string {
	dir := strings.Replace(name, "*", "wildcard_", 1)
	return caddyCertificatesRoot + "/" + letsEncryptDirectory + "/" + dir + "/" + dir + ".crt"
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return err == nil
}

// The operator's every certificate was Caddy's and none could be removed.
// One no route serves goes, and only it: its own directory, not the
// issuer's, not a neighbour's.
func TestDeletingAnUnusedCaddyCertificateRemovesOnlyItsDirectory(t *testing.T) {
	useImportedDir(t)
	root, _, service := ingressHolding(t, []string{"app.example.com", "old.example.com", "older.example.com"}, []string{"app.example.com"})

	out, err := service.DeleteCaddyCertificate(context.Background(), caddyCertificatePath("old.example.com"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != "old.example.com" || len(out.Copies) != 0 || len(out.KeptCopies) != 0 || out.CopiesError != "" {
		t.Fatalf("got %+v", out)
	}
	if exists(t, filepath.Join(root, filepath.Dir(caddyCertificatePath("old.example.com")))) {
		t.Fatal("the certificate's directory is still in Caddy's storage")
	}
	for _, kept := range []string{"app.example.com", "older.example.com"} {
		if !exists(t, filepath.Join(root, caddyCertificatePath(kept))) {
			t.Fatalf("%s went with it", kept)
		}
	}

	// Gone is gone: a second delete finds nothing rather than removing a path.
	if _, err := service.DeleteCaddyCertificate(context.Background(), caddyCertificatePath("old.example.com"), nil); !errors.Is(err, ErrCaddyCertificateNotFound) {
		t.Fatalf("a second delete: %v", err)
	}
}

// The page's list is a moment old. A route that serves the name now refuses
// the delete, by the name itself or under a wildcard, and says which route.
func TestACaddyCertificateARouteServesIsNotDeleted(t *testing.T) {
	useImportedDir(t)
	root, _, service := ingressHolding(t, []string{"app.example.com", "*.example.com"}, []string{"app.example.com"})

	for _, name := range []string{"app.example.com", "*.example.com"} {
		_, err := service.DeleteCaddyCertificate(context.Background(), caddyCertificatePath(name), nil)
		var inUse *CaddyCertificateInUseError
		if !errors.As(err, &inUse) || len(inUse.Routes) != 1 || inUse.Routes[0] != "just-dashboard-env-1.conf" {
			t.Fatalf("%s: got %v, want a refusal naming the route", name, err)
		}
		if !exists(t, filepath.Join(root, caddyCertificatePath(name))) {
			t.Fatalf("%s was removed while a route serves it", name)
		}
	}
}

// Not knowing what Caddy serves is not "unused".
func TestACaddyCertificateIsNotDeletedWhenTheRoutesCannotBeRead(t *testing.T) {
	useImportedDir(t)
	root, _, service := ingressHolding(t, []string{"old.example.com"}, nil)
	if err := os.Remove(filepath.Join(root, "admin.json")); err != nil {
		t.Fatal(err)
	}

	_, err := service.DeleteCaddyCertificate(context.Background(), caddyCertificatePath("old.example.com"), nil)
	if err == nil || !strings.Contains(err.Error(), "could not read which Caddy routes") {
		t.Fatalf("got %v, want a refusal", err)
	}
	if !exists(t, filepath.Join(root, caddyCertificatePath("old.example.com"))) {
		t.Fatal("the certificate was removed without knowing whether a route serves it")
	}
}

// The path goes to rm inside the ingress. Only <root>/<issuer>/<name>/<file>.crt
// is one, and anything else is refused before the ingress is asked anything.
func TestAPathThatIsNotACaddyCertificateIsRefused(t *testing.T) {
	useImportedDir(t)
	root, log, service := ingressHolding(t, []string{"old.example.com"}, nil)
	outside := filepath.Join(root, "data", "caddy", "keep.crt")
	writeFile(t, outside, "not a certificate directory")

	for _, path := range []string{
		"",
		"/etc/letsencrypt/live/app.example.com/fullchain.pem",
		"/data/caddy/keep.crt",
		caddyCertificatesRoot,
		caddyCertificatesRoot + "/" + letsEncryptDirectory,
		caddyCertificatesRoot + "/" + letsEncryptDirectory + "/old.example.com",
		caddyCertificatesRoot + "/" + letsEncryptDirectory + "/old.example.com/old.example.com.key",
		caddyCertificatesRoot + "/" + letsEncryptDirectory + "/old.example.com/deeper/old.example.com.crt",
		caddyCertificatesRoot + "/" + letsEncryptDirectory + "/../../keep.crt",
		caddyCertificatesRoot + "/../../../etc/x/y.crt",
		caddyCertificatesRoot + "/" + letsEncryptDirectory + "/*/old.example.com.crt",
		caddyCertificatesRoot + "/" + letsEncryptDirectory + "/.. /old.example.com.crt",
		caddyCertificatesRoot + "/" + letsEncryptDirectory + "//old.example.com.crt",
		caddyCertificatesRoot + "/" + letsEncryptDirectory + "/old.example.com/old.example.com.crt/",
		caddyCertificatesRoot + "/" + letsEncryptDirectory + "/old.example.com/$(id).crt",
	} {
		if _, err := service.DeleteCaddyCertificate(context.Background(), path, nil); !errors.Is(err, ErrCaddyCertificatePath) {
			t.Errorf("%q: got %v, want ErrCaddyCertificatePath", path, err)
		}
	}
	if runs := dockerRuns(t, log); len(runs) != 0 {
		t.Fatalf("the ingress was asked: %q", runs)
	}
	if !exists(t, outside) || !exists(t, filepath.Join(root, caddyCertificatePath("old.example.com"))) {
		t.Fatal("a refused path removed something")
	}
}

// A release copy is what a rollback activates with. The copy for a deleted
// certificate's name goes with it unless a release still names the domain.
func TestDeletingACaddyCertificateTakesItsReleaseCopyUnlessAReleaseNamesIt(t *testing.T) {
	for name, test := range map[string]struct {
		released []string
		removed  bool
	}{
		"no release names it": {nil, true},
		"a release names it":  {[]string{"old.example.com"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			imported := useImportedDir(t)
			_, _, service := ingressHolding(t, []string{"old.example.com"}, nil)
			certPEM, keyPEM, _ := selfSigned(t, "old.example.com", time.Now().Add(60*24*time.Hour))
			copy := caddyEvidenceName("old.example.com")
			if _, err := keepCaddyEvidence(copy, certPEM, keyPEM); err != nil {
				t.Fatal(err)
			}

			out, err := service.DeleteCaddyCertificate(context.Background(), caddyCertificatePath("old.example.com"), test.released)
			if err != nil || out.CopiesError != "" {
				t.Fatalf("got %+v, %v", out, err)
			}
			if test.removed != (len(out.Copies) == 1 && out.Copies[0] == copy) || test.removed == (len(out.KeptCopies) == 1) {
				t.Fatalf("removed %q, kept %q", out.Copies, out.KeptCopies)
			}
			if exists(t, filepath.Join(imported, copy)) == test.removed {
				t.Fatalf("the copy on disk: removed should be %v", test.removed)
			}
		})
	}
}

// Without an ingress there is nothing of Caddy's to delete.
func TestDeletingACaddyCertificateWithoutAnIngress(t *testing.T) {
	_, err := New(t.TempDir(), "").DeleteCaddyCertificate(context.Background(), caddyCertificatePath("old.example.com"), nil)
	if !errors.Is(err, ErrNoCaddyIngress) {
		t.Fatalf("got %v", err)
	}
}

// Caddy renews once a third of the term is left. A certificate inside that
// window is not the operator's to act on; one well into it is a renewal that
// has been failing, and it was green until the day it expired.
func TestACaddyCertificateIsExpiringOnlyOnceItsRenewalIsOverdue(t *testing.T) {
	day := 24 * time.Hour
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	for name, test := range map[string]struct {
		term, left time.Duration
		want       bool
	}{
		"ninety days, just issued":           {90 * day, 89 * day, false},
		"ninety days, as its window opens":   {90 * day, 30*day - time.Hour, false},
		"ninety days, a day into its window": {90 * day, 29*day - time.Hour, true},
		"ninety days, a week left":           {90 * day, 7 * day, true},
		"ninety days, expired":               {90 * day, -day, false},
		"six days, as its window opens":      {6 * day, 3*day - time.Hour, false},
		"six days, most of its window gone":  {6 * day, 2 * day, true},
		"a year, a month and a half left":    {365 * day, 45 * day, false},
		"a year, three weeks left":           {365 * day, 21 * day, true},
	} {
		notAfter := now.Add(test.left)
		if got := caddyRenewalOverdue(notAfter.Add(-test.term), notAfter, now); got != test.want {
			t.Errorf("%s: expiring %v, want %v", name, got, test.want)
		}
	}
}

// The inventory carries that reading.
func TestCaddyInventoryMarksAnOverdueCertificate(t *testing.T) {
	useImportedDir(t)
	root, _, service := ingressHolding(t, nil, nil)
	// selfSigned starts a certificate an hour ago, so ten hours left of
	// eleven is fresh and one hour left of two days is long overdue.
	for name, notAfter := range map[string]time.Time{
		"fresh.example.com":   time.Now().Add(60 * 24 * time.Hour),
		"overdue.example.com": time.Now().Add(time.Minute),
	} {
		certPEM, _, _ := selfSigned(t, name, notAfter)
		writeFile(t, filepath.Join(root, caddyCertificatePath(name)), certPEM)
	}
	inventory, err := service.CertificateInventory(context.Background())
	if err != nil || len(inventory) != 2 {
		t.Fatalf("got %+v, %v", inventory, err)
	}
	for _, cert := range inventory {
		if cert.Source != "caddy" || cert.Expiring != (cert.Name == "overdue.example.com") {
			t.Errorf("%s: source %q, expiring %v", cert.Name, cert.Source, cert.Expiring)
		}
	}
}
